// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Command projectcheck exercises the project tools against the real Bambuddy
// through the built MCP server, using a throwaway project it creates itself.
// It adds one existing archive to the project and takes it out again, and
// compares every archive and queue item before and after so it can show nothing
// real changed. It removes only projects whose name carries its prefix.
//
// It writes, so it needs -yes. Usage:
//
//	go build -o printshop-mcp ./cmd/printshop-mcp && go run ./scripts/projectcheck -yes
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
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const prefix = "PRINTSHOP TEST"

var (
	base     = envOr("BAMBUDDY_URL", "http://localhost:8000") + "/api/v1"
	made     []int
	cleaning sync.Once
)

func main() {
	bin := flag.String("bin", "./printshop-mcp", "path to the built server")
	yes := flag.Bool("yes", false, "confirm that this creates and deletes a throwaway project in the real Bambuddy")
	flag.Parse()
	if !*yes {
		fail("this creates and deletes a throwaway project in %s; pass -yes", base)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "projectcheck", Version: "0"}, nil).Connect(ctx, &mcp.CommandTransport{Command: exec.Command(*bin)}, nil)
	if err != nil {
		fail("cannot start %s: %v", *bin, err)
	}
	defer func() { _ = cs.Close() }()
	defer cleanup()

	tool := func(name string, args map[string]any) map[string]any {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			b, _ := json.Marshal(res)
			fail("%s: %v %s", name, err, b)
		}
		m, _ := res.StructuredContent.(map[string]any)
		return m
	}
	toolFails := func(name string, args map[string]any) string {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return err.Error()
		}
		if !res.IsError {
			fail("%s should have failed", name)
		}
		b, _ := json.Marshal(res.Content)
		return string(b)
	}
	tmp, err := os.MkdirTemp("", "projectcheck")
	if err != nil {
		fail("%v", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	before := snapshot()
	fmt.Printf("snapshot: %d archives, %d queue items, %d projects\n", len(before.archives), len(before.queue), before.projects)

	// 1. Create, read back, update.
	p := tool("create_project", map[string]any{"name": prefix + " (delete me)", "description": "throwaway from projectcheck", "color": "ff69b4", "tags": []string{"test", "projectcheck"}, "due_date": "2026-12-31", "priority": "high", "budget": 5.5, "target_count": 2})
	pid := int(p["id"].(float64))
	made = append(made, pid)
	need(p["color"] == "#FF69B4" && p["tags"] == "test,projectcheck" && p["due_date"] == "2026-12-31", "create_project: %v", p)
	fmt.Printf("create_project -> #%d (colour normalised to %v, due date %v)\n", pid, p["color"], p["due_date"])
	need(strings.Contains(toolFails("update_project", map[string]any{"project_id": pid, "status": "bogus"}), "active, completed, archived"), "no enum hint")
	need(strings.Contains(toolFails("create_project", map[string]any{"name": prefix + " bad", "color": "notacolor"}), "six-digit hex"), "bad colour accepted")
	u := tool("update_project", map[string]any{"project_id": pid, "status": "completed", "notes": "round trip"})
	need(u["status"] == "completed" && u["notes"] == "round trip", "update_project: %v", u)
	tool("update_project", map[string]any{"project_id": pid, "status": "active"})
	fmt.Println("update_project -> status and notes changed; bogus status and bad colour rejected with hints")

	// 2. Archives: add one real archive, read it back through the project, take it out.
	arch := firstArchiveID()
	tool("project_add_items", map[string]any{"project_id": pid, "archive_ids": []int{arch}})
	g := tool("get_project", map[string]any{"project_id": pid, "include_archives": true, "include_timeline": true, "include_queue": true})
	need(len(g["archive_list"].([]any)) == 1 && g["archives"].(float64) == 1 && len(g["timeline"].([]any)) >= 2, "get_project with sections: %v", g)
	tool("project_remove_archives", map[string]any{"project_id": pid, "archive_ids": []int{arch}})
	need(getJSON("/archives/" + strconv.Itoa(arch))["project_id"] == nil, "archive %d did not return to no project", arch)
	fmt.Printf("project_add_items / project_remove_archives -> archive #%d filed and unfiled; its project is back to none\n", arch)

	// 3. BOM, with the delete gate.
	b := tool("bom_add_item", map[string]any{"project_id": pid, "name": "M3x8 screws", "quantity_needed": 8, "unit_price": 0.1})
	bid := int(b["item"].(map[string]any)["id"].(float64))
	bu := tool("bom_update_item", map[string]any{"project_id": pid, "item_id": bid, "quantity_acquired": 8})
	need(bu["item"].(map[string]any)["complete"] == true, "bom complete: %v", bu)
	need(tool("bom_delete_item", map[string]any{"project_id": pid, "item_id": bid, "confirm": false})["done"] == false, "bom deleted without confirm")
	need(len(tool("get_project", map[string]any{"project_id": pid, "include_bom": true})["bom"].([]any)) == 1, "bom line vanished without a yes")
	need(tool("bom_delete_item", map[string]any{"project_id": pid, "item_id": bid, "confirm": true})["done"] == true, "bom delete")
	fmt.Println("bom_add_item / bom_update_item / bom_delete_item -> line completed; delete refused without confirm, done with it")

	// 4. Attachments: round trip, ambiguity, delete gate.
	src := filepath.Join(tmp, "notes.txt")
	body := []byte("projectcheck attachment " + time.Now().Format(time.RFC3339Nano))
	_ = os.WriteFile(src, body, 0o600)
	up1 := tool("project_upload_file", map[string]any{"project_id": pid, "kind": "attachment", "path": src})
	stored := up1["filename"].(string)
	got := filepath.Join(tmp, "got.txt")
	tool("project_download_file", map[string]any{"project_id": pid, "kind": "attachment", "filename": "notes.txt", "save_to": got})
	data, _ := os.ReadFile(got)
	need(bytes.Equal(data, body), "attachment bytes differ after the round trip")
	need(strings.Contains(toolFails("project_download_file", map[string]any{"project_id": pid, "kind": "attachment", "filename": "notes.txt", "save_to": got}), "already exists"), "overwrote a file")
	tool("project_upload_file", map[string]any{"project_id": pid, "kind": "attachment", "path": src}) // a second one with the same name
	need(strings.Contains(toolFails("project_download_file", map[string]any{"project_id": pid, "kind": "attachment", "filename": "notes.txt", "save_to": filepath.Join(tmp, "x.txt")}), "2 attachments"), "ambiguous name not caught")
	need(tool("project_delete_file", map[string]any{"project_id": pid, "kind": "attachment", "filename": stored, "confirm": false})["done"] == false, "deleted without confirm")
	need(tool("project_delete_file", map[string]any{"project_id": pid, "kind": "attachment", "filename": stored, "confirm": true})["done"] == true, "attachment delete")
	fmt.Println("project_upload_file / download / delete (attachment) -> bytes identical; overwrite and ambiguous names refused; delete gated")

	// 5. Cover: set, refuse a silent replace, replace, download, delete.
	png := filepath.Join(tmp, "cover.png")
	_ = os.WriteFile(png, tinyPNG, 0o600)
	tool("project_upload_file", map[string]any{"project_id": pid, "kind": "cover", "path": png})
	need(tool("project_upload_file", map[string]any{"project_id": pid, "kind": "cover", "path": png})["done"] == false, "cover replaced without replace: true")
	need(tool("project_upload_file", map[string]any{"project_id": pid, "kind": "cover", "path": png, "replace": true})["done"] == true, "cover replace")
	cov := filepath.Join(tmp, "cover-out.png")
	tool("project_download_file", map[string]any{"project_id": pid, "kind": "cover", "save_to": cov})
	cb, _ := os.ReadFile(cov)
	need(bytes.Equal(cb, tinyPNG), "cover bytes differ")
	need(tool("project_delete_file", map[string]any{"project_id": pid, "kind": "cover", "confirm": true})["done"] == true, "cover delete")
	fmt.Println("project_upload_file / download / delete (cover) -> replace needed replace: true; bytes identical")

	// 6. Export, import, templates.
	z := tool("export_project", map[string]any{"project_id": pid, "save_to": tmp})
	need(strings.HasSuffix(z["saved_to"].(string), ".zip"), "export zip: %v", z)
	jp := filepath.Join(tmp, "p.json")
	tool("export_project", map[string]any{"project_id": pid, "format": "json", "save_to": jp})
	imp := tool("import_project", map[string]any{"path": jp})
	iid := int(imp["id"].(float64))
	made = append(made, iid)
	need(iid != pid && strings.HasPrefix(imp["name"].(string), prefix), "import_project: %v", imp)
	tpl := tool("save_project_as_template", map[string]any{"project_id": pid})
	made = append(made, int(tpl["id"].(float64)))
	need(tpl["is_template"] == true, "template: %v", tpl)
	fromT := tool("create_project_from_template", map[string]any{"template_id": int(tpl["id"].(float64)), "name": prefix + " from template"})
	made = append(made, int(fromT["id"].(float64)))
	need(len(tool("list_project_templates", nil)["projects"].([]any)) >= 1, "templates")
	fmt.Println("export_project (zip, json) / import_project / save_project_as_template / create_project_from_template -> ok")

	// 7. print_file accepts the project and, with the printer busy or not, stops at the checks and queues nothing.
	pf := tool("print_file", map[string]any{"library_file_id": libraryFileID(), "project_id": pid, "dry_run": true})
	need(pf["queued"] == false, "print_file dry run: %v", pf)
	fmt.Println("print_file project_id + dry_run -> project accepted, nothing queued")

	// 8. delete_project through the tool, with its gate.
	d1 := tool("delete_project", map[string]any{"project_id": pid, "confirm": false})
	need(d1["done"] == false && d1["impact"] != "", "delete impact: %v", d1)
	need(getJSON("/projects/" + strconv.Itoa(pid))["id"] != nil, "project vanished without a yes")
	need(tool("delete_project", map[string]any{"project_id": pid, "confirm": true})["done"] == true, "delete_project")
	fmt.Printf("delete_project -> impact shown without confirm (%v); deleted with it\n", d1["impact"])

	cleanup()
	after := snapshot()
	need(reflect.DeepEqual(before, after), "something real changed: before %+v after %+v", before, after)
	fmt.Printf("\nreal data unchanged: %d archives, %d queue items, %d projects\nprojectcheck ok\n", len(after.archives), len(after.queue), after.projects)
}

type snap struct {
	archives map[int]string
	queue    map[int]string
	projects int
}

func snapshot() snap {
	s := snap{archives: map[int]string{}, queue: map[int]string{}}
	var as, qs, ps []map[string]any
	getInto("/archives/?limit=1000", &as)
	getInto("/queue/", &qs)
	getInto("/projects/", &ps)
	for _, a := range as {
		s.archives[int(a["id"].(float64))] = fmt.Sprint(a["project_id"], a["status"], a["print_name"])
	}
	for _, q := range qs {
		s.queue[int(q["id"].(float64))] = fmt.Sprint(q["project_id"], q["status"])
	}
	for _, p := range ps {
		if !strings.HasPrefix(str(p["name"]), prefix) {
			s.projects++
		}
	}
	return s
}

func firstArchiveID() int {
	var as []map[string]any
	getInto("/archives/?limit=50", &as)
	for _, a := range as {
		// A finished print that has no project, so taking it out again restores it exactly.
		if a["status"] == "completed" && a["project_id"] == nil {
			return int(a["id"].(float64))
		}
	}
	fail("no completed archive without a project to test with")
	return 0
}

func libraryFileID() int {
	var fs []map[string]any
	getInto("/library/files/", &fs)
	for _, f := range fs {
		if f["file_type"] == "gcode.3mf" {
			return int(f["id"].(float64))
		}
	}
	fail("no sliced library file to test with")
	return 0
}

// cleanup deletes the projects this run made, each only if its name carries the
// prefix. It runs once, from the normal exit or from fail.
func cleanup() {
	cleaning.Do(func() {
		for i := len(made) - 1; i >= 0; i-- {
			id := made[i]
			resp, err := http.Get(base + "/projects/" + strconv.Itoa(id))
			if err != nil {
				fmt.Fprintf(os.Stderr, "could not read project #%d, not deleting it: %v\n", id, err)
				continue
			}
			data, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			var o map[string]any
			if resp.StatusCode > 299 || json.Unmarshal(data, &o) != nil {
				continue // already gone
			}
			if !strings.HasPrefix(str(o["name"]), prefix) {
				fmt.Fprintf(os.Stderr, "NOT deleting project #%d %q: it does not carry the projectcheck prefix\n", id, str(o["name"]))
				continue
			}
			req, _ := http.NewRequest(http.MethodDelete, base+"/projects/"+strconv.Itoa(id), nil)
			if r, err := http.DefaultClient.Do(req); err == nil {
				_ = r.Body.Close()
				fmt.Printf("deleted throwaway project #%d\n", id)
			}
		}
	})
}

func getJSON(path string) map[string]any {
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
		fail("GET %s: %d %.200s", path, resp.StatusCode, b)
	}
}

func need(ok bool, format string, a ...any) {
	if !ok {
		fail(format, a...)
	}
}

func str(v any) string { s, _ := v.(string); return s }

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return strings.TrimSuffix(strings.TrimRight(v, "/"), "/api/v1")
	}
	return def
}

// fail stops the run. os.Exit skips deferred calls, so it cleans up first.
func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "projectcheck FAILED: "+format+"\n", a...)
	cleanup()
	os.Exit(1)
}

// tinyPNG is a valid 1x1 PNG.
var tinyPNG = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x60, 0xf8, 0xcf, 0xc0, 0xf0, 0x1f, 0x00, 0x05, 0x00, 0x02, 0x01, 0xa5, 0xf6, 0x45, 0x40, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82}
