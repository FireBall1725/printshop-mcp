// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Command spoolcheck exercises the Spoolman write tools against the real
// Spoolman, using records it makes itself. It creates two spare filaments and a
// spare spool carrying a fake extra.tag, moves the spool between the filaments
// through the built MCP server, checks the tag survives, and then deletes only
// the records it created, after confirming its marker on each. Every real spool
// is snapshotted first and compared at the end.
//
// It writes, so it needs -yes. Usage:
//
//	go build -o printshop-mcp ./cmd/printshop-mcp && go run ./scripts/spoolcheck -yes
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	marker  = "printshop-mcp spoolcheck spare; safe to delete"
	prefix  = "PRINTSHOP TEST"
	fakeTag = "SPOOLCHECK0000000000000000000000"
)

// made lists the records this run created, which are the only ones it may delete.
var made struct{ filaments, spools []int }

var cleaning sync.Once

var base = envOr("SPOOLMAN_URL", "http://localhost:7912") + "/api/v1"

func main() {
	bin := flag.String("bin", "./printshop-mcp", "path to the built server")
	yes := flag.Bool("yes", false, "confirm that this writes spare records to the real Spoolman")
	flag.Parse()
	if !*yes {
		fail("this creates and deletes spare records in %s; pass -yes", base)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "spoolcheck", Version: "0"}, nil).Connect(ctx, &mcp.CommandTransport{Command: exec.Command(*bin)}, nil)
	if err != nil {
		fail("cannot start %s: %v", *bin, err)
	}
	defer func() { _ = cs.Close() }()

	before := snapshot()
	fmt.Printf("snapshot: %d real spools\n", len(before))

	defer cleanup()

	tool := func(name string, args map[string]any) map[string]any {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			fail("%s: %v %v", name, err, res)
		}
		m, _ := res.StructuredContent.(map[string]any)
		return m
	}

	// 1. Two spare filaments through create_filament; the vendor already exists.
	mk := func(name, hex string) int {
		out := tool("create_filament", map[string]any{"vendor": "bambu lab", "name": name, "material": "PLA", "color_hex": hex})
		if out["created"] != true || out["vendor_created"] != nil {
			fail("create_filament %s: %v", name, out)
		}
		id := int(out["filament"].(map[string]any)["id"].(float64))
		made.filaments = append(made.filaments, id)
		fmt.Printf("create_filament %-22s -> #%d (vendor reused, density %v)\n", name, id, out["density_g_cm3"])
		return id
	}
	a, b := mk(prefix+" A", "123456"), mk(prefix+" B", "654321")

	// A duplicate must be refused.
	if out := tool("create_filament", map[string]any{"vendor": "Bambu Lab", "name": strings.ToLower(prefix + " A"), "material": "pla", "color_hex": "123456"}); out["existing_id"] != float64(a) {
		fail("duplicate not refused: %v", out)
	}
	fmt.Println("create_filament duplicate -> refused with existing id")

	// 2. The spare spool, made by hand so it can carry an extra.tag.
	spare := post("/spool", map[string]any{
		"filament_id": a, "initial_weight": 1000, "remaining_weight": 1000, "comment": marker,
		"extra": map[string]string{"tag": strconv.Quote(fakeTag)},
	})
	sid := int(spare["id"].(float64))
	made.spools = append(made.spools, sid)
	fmt.Printf("spare spool #%d on filament #%d with tag %s\n", sid, a, fakeTag)

	// 3. The move.
	out := tool("update_spool", map[string]any{"spool_id": sid, "filament_id": b})
	need(out["extra_tag_intact"] == true && out["alert"] == nil, "tag not intact after the move: %v", out)
	need(out["tag_set"] == true && out["extra_tag_before"] == fakeTag && out["extra_tag_after"] == fakeTag, "tag values: %v", out)
	need(get("/spool/" + itoa(sid))["filament"].(map[string]any)["id"] == float64(b), "the spool did not land on filament #%d", b)
	need(reflect.DeepEqual(extra(sid), map[string]any{"tag": strconv.Quote(fakeTag)}), "independent read of extra differs: %v", extra(sid))
	fmt.Printf("update_spool filament_id=%d -> tag intact, independent read agrees\n", b)

	// 4. Move back with another field in the same call.
	out = tool("update_spool", map[string]any{"spool_id": sid, "filament_id": a, "location": "spoolcheck"})
	need(out["extra_tag_intact"] == true && out["location"] == "spoolcheck", "second move: %v", out)
	fmt.Println("update_spool move back + location -> tag intact")

	// 5. update_filament.
	out = tool("update_filament", map[string]any{"filament_id": b, "name": prefix + " B2", "color_hex": "#0A0B0C", "weight_g": 750})
	fil := out["filament"].(map[string]any)
	need(fil["name"] == prefix+" B2" && fil["color_hex"] == "0A0B0C" && fil["weight_g"] == float64(750), "update_filament: %v", out)
	fmt.Println("update_filament -> name, colour and weight changed")

	// 6. list_spools and get_spool show the tag.
	list := tool("list_spools", map[string]any{"in_stock_only": false, "limit": 200})
	found := false
	for _, r := range list["spools"].([]any) {
		if m := r.(map[string]any); m["id"] == float64(sid) {
			found = true
			need(m["tag_set"] == true, "list_spools tag_set: %v", m)
		}
	}
	need(found, "spare spool missing from list_spools")
	need(tool("get_spool", map[string]any{"spool_id": sid})["tag_set"] == true, "get_spool tag_set")
	fmt.Println("list_spools / get_spool -> tag_set true")

	// 7. Probe how this Spoolman treats a PATCH that carries extra, on the spare
	// only. This is an observation, not an assertion: the tool never sends
	// extra either way. The tag is put back between probes.
	tag := map[string]string{"tag": strconv.Quote(fakeTag)}
	patch("/spool/"+itoa(sid), map[string]any{"extra": map[string]string{}})
	fmt.Printf("probe: PATCH extra:{}                     -> tag %s\n", kept(extra(sid), "tag"))
	patch("/spool/"+itoa(sid), map[string]any{"extra": tag})
	patch("/spool/"+itoa(sid), map[string]any{"extra": map[string]string{"bambu_slicer_filament": strconv.Quote("PROBE")}})
	e := extra(sid)
	fmt.Printf("probe: PATCH extra:{other key only}       -> tag %s, other key %s\n", kept(e, "tag"), kept(e, "bambu_slicer_filament"))
	patch("/spool/"+itoa(sid), map[string]any{"extra": tag})
	patch("/spool/"+itoa(sid), map[string]any{"filament_id": b})
	fmt.Printf("probe: PATCH filament_id only             -> tag %s\n", kept(extra(sid), "tag"))

	// 8. Nothing real moved.
	after := snapshot()
	need(reflect.DeepEqual(before, after), "a real spool changed during the run")
	fmt.Printf("real spools unchanged (%d compared)\n", len(after))
	fmt.Println("\nspoolcheck ok (spare records are removed next)")
}

// cleanup deletes what this run created, spool first, each only after its
// marker is confirmed. It runs once, from the normal exit or from fail.
func cleanup() {
	cleaning.Do(func() {
		for _, id := range made.spools {
			remove("spool", id, func(o map[string]any) bool { return strings.Contains(str(o["comment"]), marker) })
		}
		for _, id := range made.filaments {
			remove("filament", id, func(o map[string]any) bool { return strings.HasPrefix(str(o["name"]), prefix) })
		}
	})
}

// snapshot records filament id and extra for every spool that is not ours.
func snapshot() map[int]string {
	var all []map[string]any
	getInto("/spool?allow_archived=true&limit=1000", &all)
	out := map[int]string{}
	for _, s := range all {
		if strings.Contains(str(s["comment"]), marker) {
			continue
		}
		b, _ := json.Marshal([]any{s["filament"].(map[string]any)["id"], s["extra"], s["location"], s["remaining_weight"], s["archived"]})
		out[int(s["id"].(float64))] = string(b)
	}
	return out
}

func extra(spoolID int) map[string]any {
	e, _ := get("/spool/" + itoa(spoolID))["extra"].(map[string]any)
	if e == nil {
		e = map[string]any{}
	}
	return e
}

// remove deletes one record only if it is ours.
func remove(kind string, id int, ours func(map[string]any) bool) {
	resp0, err := http.Get(base + "/" + kind + "/" + itoa(id))
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not read %s #%d to confirm it is ours, so not deleting it: %v\n", kind, id, err)
		return
	}
	data, _ := io.ReadAll(resp0.Body)
	_ = resp0.Body.Close()
	var o map[string]any
	if resp0.StatusCode > 299 || json.Unmarshal(data, &o) != nil {
		fmt.Fprintf(os.Stderr, "%s #%d is not readable (%d), nothing to delete\n", kind, id, resp0.StatusCode)
		return
	}
	if !ours(o) {
		fmt.Fprintf(os.Stderr, "NOT deleting %s #%d: it does not carry the spoolcheck marker\n", kind, id)
		return
	}
	req, _ := http.NewRequest(http.MethodDelete, base+"/"+kind+"/"+itoa(id), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode > 299 {
		fmt.Fprintf(os.Stderr, "could not delete %s #%d: %v\n", kind, id, err)
		return
	}
	_ = resp.Body.Close()
	fmt.Printf("deleted spare %s #%d\n", kind, id)
}

func get(path string) map[string]any {
	var m map[string]any
	getInto(path, &m)
	return m
}

func getInto(path string, out any) {
	resp, err := http.Get(base + path)
	if err != nil {
		fail("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode > 299 || json.Unmarshal(b, out) != nil {
		fail("GET %s: %d %s", path, resp.StatusCode, b)
	}
}

func post(path string, body any) map[string]any  { return send(http.MethodPost, path, body) }
func patch(path string, body any) map[string]any { return send(http.MethodPatch, path, body) }

func send(method, path string, body any) map[string]any {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	var m map[string]any
	if resp.StatusCode > 299 || json.Unmarshal(data, &m) != nil {
		fail("%s %s: %d %s", method, path, resp.StatusCode, data)
	}
	return m
}

func need(ok bool, format string, a ...any) {
	if !ok {
		fail(format, a...)
	}
}

func kept(e map[string]any, key string) string {
	if _, ok := e[key]; ok {
		return "KEPT"
	}
	return "GONE"
}

func str(v any) string  { s, _ := v.(string); return s }
func itoa(i int) string { return strconv.Itoa(i) }

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return strings.TrimSuffix(strings.TrimRight(v, "/"), "/api/v1")
	}
	return def
}

// fail stops the run. os.Exit skips deferred calls, so it cleans up first.
func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "spoolcheck FAILED: "+format+"\n", a...)
	cleanup()
	os.Exit(1)
}
