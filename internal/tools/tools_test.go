// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/hms"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fixtures = "../plan/testdata/"

// fake stands in for both services, serving the recorded fixtures, and keeps
// every request that is not a GET so tests can prove nothing was written.
type fake struct {
	proj                       *fakeProjects
	t                          *testing.T
	spools, filaments, vendors map[int]map[string]any
	smWrites, smBodies         []string
	dropTagOnPatch             bool
	needG                      float64
	unassignedTray             map[int]bool
	hold                       bool
	hmsSrv                     *httptest.Server
	mu                         sync.Mutex
	status                     map[string]any
	writes                     []string
	bodies                     []string
	bb, sm                     *httptest.Server
}

func readJSON(t *testing.T, name string) any {
	t.Helper()
	b, err := os.ReadFile(fixtures + name)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func newFake(t *testing.T) *fake {
	f := &fake{t: t, status: readJSON(t, "status_running.json").(map[string]any)}
	spools, assigns := readJSON(t, "spools.json"), readJSON(t, "assignments.json")
	file, reqs := readJSON(t, "library_file_x1c.json"), readJSON(t, "requirements_file.json")
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }
	extra := map[string]any{}
	for _, n := range []string{"queue_items.json", "printers.json", "archives.json", "archive_stats.json", "maintenance_overview.json", "print_objects.json", "ams_history.json"} {
		extra[n] = readJSON(t, n)
	}

	f.bb = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != http.MethodGet {
			b, _ := io.ReadAll(r.Body)
			f.writes, f.bodies = append(f.writes, r.Method+" "+r.URL.RequestURI()), append(f.bodies, string(b))
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/projects") {
			f.serveProjects(w, r)
			return
		}
		switch {
		case r.URL.Path == "/api/v1/printers/1/status":
			write(w, f.status)
		case r.URL.Path == "/api/v1/printers/":
			write(w, extra["printers.json"])
		case r.URL.Path == "/api/v1/archives/":
			write(w, extra["archives.json"])
		case r.URL.Path == "/api/v1/archives/20":
			write(w, extra["archives.json"].([]any)[0])
		case r.URL.Path == "/api/v1/archives/stats":
			write(w, extra["archive_stats.json"])
		case r.URL.Path == "/api/v1/maintenance/overview":
			write(w, extra["maintenance_overview.json"])
		case r.URL.Path == "/api/v1/printers/1/print/objects":
			write(w, extra["print_objects.json"])
		case r.URL.Path == "/api/v1/ams-history/1/0":
			write(w, extra["ams_history.json"])
		case r.URL.Path == "/api/v1/library/files/9":
			write(w, file)
		case r.URL.Path == "/api/v1/library/files/9/filament-requirements":
			if f.needG > 0 {
				reqs.(map[string]any)["filaments"].([]any)[0].(map[string]any)["used_grams"] = f.needG
			}
			write(w, reqs)
		case r.URL.Path == "/api/v1/spoolman/inventory/slot-assignments/all":
			var kept []any
			for _, a := range assigns.([]any) {
				if !f.unassignedTray[int(a.(map[string]any)["tray_id"].(float64))] {
					kept = append(kept, a)
				}
			}
			write(w, kept)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/queue/":
			write(w, extra["queue_items.json"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/queue/77":
			status := "pending"
			if !f.hold {
				status = "printing"
			}
			write(w, map[string]any{"id": 77, "status": status, "archive_name": "DeskControllerCaseV1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/library/files/":
			write(w, []any{map[string]any{"id": 9, "filename": "DeskControllerCaseV1.gcode.3mf", "file_type": "gcode.3mf", "file_size": 2955937, "thumbnail_path": "archive/library/thumbnails/x.png", "sliced_for_model": "X1C"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/printers/1/hms/clear":
			f.status["hms_errors"] = []any{}
			write(w, map[string]any{"success": true})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/queue/":
			if !f.hold {
				f.status["state"], f.status["stg_cur_name"] = "RUNNING", "Heatbed preheating"
				f.status["temperatures"] = map[string]any{"nozzle": 75, "nozzle_target": 220, "bed": 40, "bed_target": 55}
			}
			write(w, map[string]any{"id": 77, "status": "pending", "archive_name": "DeskControllerCaseV1"})
		case r.Method == http.MethodPost || r.Method == http.MethodDelete:
			write(w, map[string]any{"success": true})
		default:
			http.NotFound(w, r)
		}
	}))
	// A stateful stand-in for Spoolman, including its behaviour that matters
	// here, in its worst case: a PATCH carrying extra replaces the whole dict.
	// Real Spoolman 0.26.1 merges instead (scripts/spoolcheck confirms it), so
	// this fake is stricter than the thing it stands in for.
	f.spools, f.filaments, f.vendors = map[int]map[string]any{}, map[int]map[string]any{}, map[int]map[string]any{}
	for _, sp := range spools.([]any) {
		m := sp.(map[string]any)
		f.spools[int(m["id"].(float64))] = m
		fil := m["filament"].(map[string]any)
		f.filaments[int(fil["id"].(float64))] = fil
		if v, ok := fil["vendor"].(map[string]any); ok {
			f.vendors[int(v["id"].(float64))] = v
		}
	}
	f.sm = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var body map[string]any
		if r.Method != http.MethodGet {
			b, _ := io.ReadAll(r.Body)
			f.smWrites, f.smBodies = append(f.smWrites, r.Method+" "+r.URL.Path), append(f.smBodies, string(b))
			_ = json.Unmarshal(b, &body)
		}
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/"), "/")
		id := -1
		if len(parts) == 2 {
			id, _ = strconv.Atoi(parts[1])
		}
		sorted := func(m map[int]map[string]any) []any {
			keys := make([]int, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Ints(keys)
			out := make([]any, 0, len(keys))
			for _, k := range keys {
				out = append(out, m[k])
			}
			return out
		}
		merge := func(dst map[string]any) {
			for k, v := range body {
				dst[k] = v // extra included, replacing the dict outright: the worst case
			}
		}
		switch {
		case parts[0] == "spool" && len(parts) == 1 && r.Method == http.MethodGet:
			w.Header().Set("X-Total-Count", strconv.Itoa(len(f.spools)))
			write(w, sorted(f.spools))
		case parts[0] == "spool" && id > 0 && f.spools[id] != nil && r.Method == http.MethodGet:
			write(w, f.spools[id])
		case parts[0] == "spool" && id > 0 && f.spools[id] != nil && r.Method == http.MethodPatch:
			merge(f.spools[id])
			if fid, ok := body["filament_id"]; ok {
				f.spools[id]["filament"] = f.filaments[int(fid.(float64))]
				delete(f.spools[id], "filament_id")
			}
			if f.dropTagOnPatch {
				f.spools[id]["extra"] = map[string]any{}
			}
			write(w, f.spools[id])
		case parts[0] == "filament" && len(parts) == 1 && r.Method == http.MethodGet:
			write(w, sorted(f.filaments))
		case parts[0] == "filament" && id > 0 && f.filaments[id] != nil && r.Method == http.MethodGet:
			write(w, f.filaments[id])
		case parts[0] == "filament" && id > 0 && f.filaments[id] != nil && r.Method == http.MethodPatch:
			merge(f.filaments[id])
			write(w, f.filaments[id])
		case parts[0] == "filament" && len(parts) == 1 && r.Method == http.MethodPost:
			nid := 100 + len(f.filaments)
			body["id"] = float64(nid)
			if v, ok := body["vendor_id"].(float64); ok {
				body["vendor"] = f.vendors[int(v)]
			}
			f.filaments[nid] = body
			write(w, body)
		case parts[0] == "vendor" && len(parts) == 1 && r.Method == http.MethodGet:
			write(w, sorted(f.vendors))
		case parts[0] == "vendor" && len(parts) == 1 && r.Method == http.MethodPost:
			nid := 50 + len(f.vendors)
			body["id"] = float64(nid)
			f.vendors[nid] = body
			write(w, body)
		default:
			http.NotFound(w, r)
		}
	}))
	lookup, err := os.ReadFile(fixtures + "../../hms/testdata/lookup_ok.json")
	if err != nil {
		t.Fatal(err)
	}
	f.hmsSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(lookup) }))
	t.Cleanup(func() { f.bb.Close(); f.sm.Close(); f.hmsSrv.Close() })
	return f
}

func (f *fake) setState(state string, hms bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status["state"] = state
	if !hms {
		f.status["hms_errors"] = []any{}
	}
}

func (f *fake) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func session(t *testing.T, f *fake) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	RegisterAll(srv, &Deps{
		Bambuddy:    api.New("bambuddy", f.bb.URL, map[string]string{"X-API-Key": "secret-key"}),
		Spoolman:    api.New("spoolman", f.sm.URL, nil),
		Model:       "X1C",
		HMS:         hms.New(f.hmsSrv.URL),
		BambuddyURL: f.bb.URL,
		PollEvery:   10 * time.Millisecond,
	})
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		b, _ := json.Marshal(res.Content)
		t.Fatalf("%s returned an error: %s", name, b)
	}
	m, _ := res.StructuredContent.(map[string]any)
	return m
}

var projectDeleters = map[string]bool{"delete_project": true, "bom_delete_item": true, "project_delete_file": true}

func TestToolListMatchesTheDocumentedSet(t *testing.T) {
	cs := session(t, newFake(t))
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := "printer_status list_library upload_file print_file queue_list queue_cancel print_stop print_pause print_resume print_history " +
		"list_spools list_filaments list_vendors get_spool use_filament measure_spool update_spool create_spool stock_for_print ams_vs_stock " +
		"list_printers clear_plate print_speed list_printable_objects skip_objects start_drying stop_drying assign_spool_to_slot unassign_spool " +
		"list_projects get_project list_project_templates create_project create_project_from_template save_project_as_template update_project delete_project " +
		"project_add_items project_remove_archives bom_add_item bom_update_item bom_delete_item project_upload_file project_download_file project_delete_file export_project import_project " +
		"list_archives get_archive archive_stats maintenance_overview ams_history clear_hms create_filament update_filament"
	got := map[string]bool{}
	for _, tl := range list.Tools {
		got[tl.Name] = true
		// Deletion is allowed only for project data, each behind an ask-first
		// confirm (TestProjectToolsAreGated). Nothing deletes a spool, filament,
		// vendor, library file or archive.
		if strings.Contains(tl.Name, "delete") && !projectDeleters[tl.Name] {
			t.Errorf("an unexpected delete tool exists: %s", tl.Name)
		}
	}
	for _, n := range strings.Fields(want) {
		if !got[n] {
			t.Errorf("missing tool %s", n)
		}
	}
	if len(got) != len(strings.Fields(want)) {
		t.Errorf("tool count %d, want %d", len(got), len(strings.Fields(want)))
	}
	for _, tl := range list.Tools {
		if (tl.Name == "print_stop" || tl.Name == "queue_cancel" || tl.Name == "skip_objects") && !strings.Contains(tl.Description, "must ask the user first") {
			t.Errorf("%s description must tell Claude to ask first", tl.Name)
		}
	}
}

func TestPrintFileRefusedWhileRunning(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "ams_mapping": []int{1}})
	if out["started"] != false {
		t.Fatalf("%v", out)
	}
	if s, _ := out["summary"].(string); !strings.Contains(s, "RUNNING") || !strings.Contains(s, "pixelbar-mount-prototype-v6") {
		t.Fatalf("summary should say what the printer is doing: %q", s)
	}
	if f.writeCount() != 0 {
		t.Fatalf("a refused print wrote to Bambuddy: %v", f.writes)
	}
}

func TestPrintFileHMSBlocksUntilForce(t *testing.T) {
	f := newFake(t)
	f.setState("IDLE", true)
	cs := session(t, f)
	out := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "ams_mapping": []int{1}})
	pf := out["preflight"].(map[string]any)
	if out["started"] != false || len(pf["hms_errors"].([]any)) != 1 || f.writeCount() != 0 {
		t.Fatalf("%v", out)
	}
	out = call(t, cs, "print_file", map[string]any{"library_file_id": 9, "ams_mapping": []int{1}, "force": true})
	if out["started"] != true {
		t.Fatalf("force should start: %v", out)
	}
}

func TestPrintFileQueuesWithExpectedBody(t *testing.T) {
	f := newFake(t)
	f.setState("FINISH", false)
	cs := session(t, f)
	if out := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "ams_mapping": []int{1}, "dry_run": true}); out["started"] != false || f.writeCount() != 0 {
		t.Fatalf("dry run wrote: %v", out)
	}
	out := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "ams_mapping": []int{1}})
	if out["started"] != true {
		t.Fatalf("%v", out)
	}
	if len(f.writes) != 1 || f.writes[0] != "POST /api/v1/queue/" {
		t.Fatalf("writes %v", f.writes)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(f.bodies[0]), &body)
	want := `{"ams_mapping":[1],"bed_levelling":"auto","library_file_id":9,"plate_id":1,"printer_id":1,"use_ams":true}`
	gotJSON, _ := json.Marshal(body)
	if string(gotJSON) != want {
		t.Fatalf("queue body\n got %s\nwant %s", gotJSON, want)
	}
}

func TestStopAndCancelNeedConfirm(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	for name, args := range map[string]map[string]any{"print_stop": {"confirm": false}, "queue_cancel": {"item_id": 3, "confirm": false}} {
		out := call(t, cs, name, args)
		if out["done"] != false || f.writeCount() != 0 {
			t.Fatalf("%s acted without confirm: %v %v", name, out, f.writes)
		}
	}
	if out := call(t, cs, "print_stop", map[string]any{"confirm": true}); out["done"] != true || f.writes[0] != "POST /api/v1/printers/1/print/stop" {
		t.Fatalf("%v %v", out, f.writes)
	}
}

func TestStockAndAMSTools(t *testing.T) {
	cs := session(t, newFake(t))
	out := call(t, cs, "stock_for_print", map[string]any{"library_file_id": 9})
	if out["all_enough"] != true || out["file"] != "DeskControllerCaseV1.gcode.3mf" {
		t.Fatalf("%v", out)
	}
	out = call(t, cs, "ams_vs_stock", map[string]any{})
	if out["trays_with_flags"].(float64) != 1 {
		t.Fatalf("expected the magenta tray flagged: %v", out)
	}
	out = call(t, cs, "list_spools", map[string]any{"material": "pla", "color": "#000000", "limit": 2})
	rows := out["spools"].([]any)
	if len(rows) != 2 || rows[0].(map[string]any)["id"].(float64) != 9 /* 765 g beats the 25 g black at equal colour */ || out["truncated"] != true {
		t.Fatalf("nearest black first expected: %v", out)
	}
}

func TestListPrintersLeaksNothing(t *testing.T) {
	cs := session(t, newFake(t))
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_printers", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("%v %v", err, res)
	}
	b, _ := json.Marshal(res) // structured content and the text copy
	for _, bad := range []string{"TESTCODE", "TESTSERIAL", "192.0.2", "access_code", "serial", "ip_address"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("list_printers leaked %q: %s", bad, b)
		}
	}
	if !strings.Contains(string(b), `"X1C"`) {
		t.Fatalf("expected the printer: %s", b)
	}
}

func TestClearPlateAndSkipNeedConfirm(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	for name, args := range map[string]map[string]any{
		"clear_plate":  {"confirm": false},
		"skip_objects": {"object_ids": []int{29}, "confirm": false},
	} {
		if out := call(t, cs, name, args); out["done"] != false || f.writeCount() != 0 {
			t.Fatalf("%s acted without confirm: %v %v", name, out, f.writes)
		}
	}
	if out := call(t, cs, "clear_plate", map[string]any{"confirm": true}); out["done"] != true || f.writes[0] != "POST /api/v1/printers/1/clear-plate" {
		t.Fatalf("%v %v", out, f.writes)
	}
}

func TestSkipRefusesTheOnlyObject(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "skip_objects", map[string]any{"object_ids": []int{29}, "confirm": true})
	if out["done"] != false || !strings.Contains(out["message"].(string), "every remaining object") || f.writeCount() != 0 {
		t.Fatalf("%v %v", out, f.writes)
	}
}

func TestPrintSpeedAndDrying(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	if res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "print_speed", Arguments: map[string]any{"mode": 9}}); res == nil || !res.IsError || f.writeCount() != 0 {
		t.Fatal("mode 9 must be rejected without a write")
	}
	call(t, cs, "print_speed", map[string]any{"mode": 3})
	call(t, cs, "start_drying", map[string]any{"ams_id": 0, "temp_c": 55, "hours": 6, "filament": "PLA"})
	if len(f.writes) != 2 || f.writes[0] != "POST /api/v1/printers/1/print-speed?mode=3" ||
		f.writes[1] != "POST /api/v1/printers/1/drying/start?ams_id=0&duration=6&filament=PLA&temp=55" {
		t.Fatalf("%v", f.writes)
	}
	before := f.writeCount()
	if out := call(t, cs, "start_drying", map[string]any{"ams_id": 7}); out["done"] != false || f.writeCount() != before {
		t.Fatalf("unknown AMS unit must be refused without a write: %v", out)
	}
}

func TestAssignSpoolWarnsAndPosts(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "assign_spool_to_slot", map[string]any{"spool_id": 8, "slot": 1})
	warns, _ := out["warnings"].([]any)
	if out["assigned"] != true || len(warns) < 2 {
		t.Fatalf("a colour-mismatched, already-linked spool should assign with warnings: %v", out)
	}
	var body map[string]int
	_ = json.Unmarshal([]byte(f.bodies[0]), &body)
	if f.writes[0] != "POST /api/v1/spoolman/inventory/slot-assignments" || body["spoolman_spool_id"] != 8 || body["ams_id"] != 0 || body["tray_id"] != 1 || body["printer_id"] != 1 {
		t.Fatalf("%v %v", f.writes, body)
	}
	before := f.writeCount()
	if res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "assign_spool_to_slot", Arguments: map[string]any{"spool_id": 8, "slot": 99}}); res == nil || !res.IsError || f.writeCount() != before {
		t.Fatal("slot 99 must be rejected without a write")
	}
	call(t, cs, "unassign_spool", map[string]any{"spool_id": 8})
	if last := f.writes[len(f.writes)-1]; last != "DELETE /api/v1/spoolman/inventory/slot-assignments/8" {
		t.Fatalf("%v", f.writes)
	}
}

func TestRecordTools(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	if out := call(t, cs, "list_archives", map[string]any{"date_from": "2026-10-01"}); len(out["archives"].([]any)) != 3 {
		t.Fatalf("%v", out)
	}
	if out := call(t, cs, "get_archive", map[string]any{"archive_id": 20}); out["name"] == nil {
		t.Fatalf("%v", out)
	}
	if out := call(t, cs, "archive_stats", map[string]any{}); out["total_prints"].(float64) != 23 {
		t.Fatalf("%v", out)
	}
	if out := call(t, cs, "maintenance_overview", map[string]any{"only_due": true}); len(out["printers"].([]any)) != 1 {
		t.Fatalf("%v", out)
	}
	if out := call(t, cs, "ams_history", map[string]any{"ams_id": 0}); len(out["samples"].([]any)) == 0 {
		t.Fatalf("%v", out)
	}
	if out := call(t, cs, "list_printable_objects", map[string]any{}); out["is_printing"] != true {
		t.Fatalf("%v", out)
	}
	if res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "archive_stats", Arguments: map[string]any{"date_from": "yesterday"}}); res == nil || !res.IsError {
		t.Fatal("a bad date should be an error")
	}
	if f.writeCount() != 0 {
		t.Fatalf("read tools wrote: %v", f.writes)
	}
}

// ─── feedback round: HMS text, clear_hms, auto mapping, waiting, status extras ─

func TestPrinterStatusCarriesHMSTextLastJobAndNextStep(t *testing.T) {
	cs := session(t, newFake(t))
	out := call(t, cs, "printer_status", map[string]any{})
	hmsList := out["hms_errors"].([]any)
	h := hmsList[0].(map[string]any)
	if !strings.Contains(h["text"].(string), "Not enough space on MicroSD Card") ||
		h["severity"] != "common" || h["level"].(float64) != 3 || h["bambuddy_severity"].(float64) != 1 || h["display"] != "0500-0100-0003-0004" {
		t.Fatalf("hms: %v", h)
	}
	if lj, ok := out["last_job"].(map[string]any); !ok || lj["id"].(float64) != 29 {
		t.Fatalf("last_job: %v", out["last_job"])
	}
	if _, ok := out["awaiting_plate_clear"]; !ok || out["next_step"] == "" {
		t.Fatalf("awaiting_plate_clear and next_step must always be present: %v", out)
	}
}

func TestPrintFileAutoMapsAndReportsTheRealState(t *testing.T) {
	f := newFake(t)
	f.setState("FINISH", false)
	cs := session(t, f)

	dry := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "dry_run": true})
	pf := dry["preflight"].(map[string]any)
	if dry["queued"] != false || pf["mapping_source"] != "auto" || fmt.Sprint(pf["ams_mapping"]) != "[1]" || f.writeCount() != 0 {
		t.Fatalf("dry run should pick slot 1 and write nothing: %v", dry)
	}

	out := call(t, cs, "print_file", map[string]any{"library_file_id": 9})
	if out["queued"] != true || out["started"] != true {
		t.Fatalf("%v", out)
	}
	if now, _ := out["printer_now"].(string); now != "RUNNING, Heatbed preheating, nozzle 75/220 °C, bed 40/55 °C" {
		t.Fatalf("printer_now %q", now)
	}
	if s, _ := out["summary"].(string); !strings.Contains(s, "Printer is now RUNNING") || !strings.Contains(s, "chosen automatically") {
		t.Fatalf("summary %q", s)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(f.bodies[0]), &body)
	if fmt.Sprint(body["ams_mapping"]) != "[1]" {
		t.Fatalf("the automatic mapping must be what is sent: %s", f.bodies[0])
	}
}

func TestPrintFileSaysSoWhenThePrinterNeverStarts(t *testing.T) {
	f := newFake(t)
	f.setState("FINISH", false)
	f.hold = true
	f.status["awaiting_plate_clear"] = true
	cs := session(t, f)
	out := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "wait_seconds": 1})
	if out["queued"] != true || out["started"] != false {
		t.Fatalf("queued but not started: %v", out)
	}
	s, _ := out["summary"].(string)
	for _, want := range []string{"still FINISH", "has not started", "clear_plate"} {
		if !strings.Contains(s, want) {
			t.Errorf("summary missing %q: %s", want, s)
		}
	}
	if out["waited_seconds"].(float64) < 1 {
		t.Fatalf("it should have waited the full second: %v", out["waited_seconds"])
	}
}

func TestPrintFileWaitSecondsZeroDoesNotPoll(t *testing.T) {
	f := newFake(t)
	f.setState("IDLE", false)
	cs := session(t, f)
	out := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "wait_seconds": 0})
	if out["queued"] != true || out["started"] != false || !strings.Contains(out["summary"].(string), "not waiting") {
		t.Fatalf("%v", out)
	}
}

func TestClearHMS(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "clear_hms", map[string]any{"confirm": false})
	before := out["before"].([]any)
	if out["cleared"] != false || len(before) != 1 || f.writeCount() != 0 ||
		!strings.Contains(before[0].(map[string]any)["text"].(string), "MicroSD") {
		t.Fatalf("without confirm it must only describe: %v", out)
	}
	out = call(t, cs, "clear_hms", map[string]any{"confirm": true})
	if out["cleared"] != true || f.writes[0] != "POST /api/v1/printers/1/hms/clear" || out["remaining"] != nil {
		t.Fatalf("%v %v", out, f.writes)
	}
	n := f.writeCount()
	if out := call(t, cs, "clear_hms", map[string]any{"confirm": true}); out["cleared"] != false || f.writeCount() != n {
		t.Fatalf("nothing left to clear must not write: %v", out)
	}
}

func TestListLibraryHasThumbnailURL(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "list_library", map[string]any{})
	row := out["files"].([]any)[0].(map[string]any)
	if row["thumbnail_url"] != f.bb.URL+"/api/v1/library/files/9/thumbnail" {
		t.Fatalf("%v", row)
	}
}

// ─── fixing Spoolman data: filaments and spool moves ─────────────────────────

func smBody(t *testing.T, f *fake, i int) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(f.smBodies[i]), &m); err != nil {
		t.Fatalf("body %d: %v", i, err)
	}
	return m
}

func TestMoveSpoolSendsOnlyFilamentIDAndKeepsTheTag(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	created := call(t, cs, "create_filament", map[string]any{"vendor": "bambu lab", "name": "PLA Matte Black", "material": "PLA", "color_hex": "#000000"})
	newID := int(created["filament"].(map[string]any)["id"].(float64))
	tagBefore := f.spools[9]["extra"].(map[string]any)["tag"]

	out := call(t, cs, "update_spool", map[string]any{"spool_id": 9, "filament_id": newID})
	if out["extra_tag_intact"] != true || out["alert"] != nil {
		t.Fatalf("%v", out)
	}
	if out["filament"] != "PLA Matte Black" || out["moved_from"] != "#2 Black" || out["tag_set"] != true {
		t.Fatalf("%v", out)
	}
	if out["extra_tag_before"] == "" || out["extra_tag_before"] != out["extra_tag_after"] {
		t.Fatalf("tag before and after must match and be shown: %v", out)
	}
	if f.spools[9]["extra"].(map[string]any)["tag"] != tagBefore {
		t.Fatal("the tag changed in Spoolman")
	}

	// The create went to /filament; the move is exactly one PATCH with one key.
	if len(f.smWrites) != 2 || f.smWrites[0] != "POST /api/v1/filament" || f.smWrites[1] != "PATCH /api/v1/spool/9" {
		t.Fatalf("writes %v", f.smWrites)
	}
	if body := smBody(t, f, 1); len(body) != 1 || body["filament_id"] != float64(newID) {
		t.Fatalf("the PATCH must contain filament_id and nothing else: %s", f.smBodies[1])
	}
	for i, b := range f.smBodies {
		if strings.Contains(b, `"extra"`) {
			t.Fatalf("extra was sent in write %d: %s", i, b)
		}
	}
	warns, _ := json.Marshal(out["warnings"])
	if !strings.Contains(string(warns), "may recreate or rename filaments on its next AMS sync") {
		t.Fatalf("warnings %s", warns)
	}
}

func TestMoveSpoolShoutsWhenTheTagIsLost(t *testing.T) {
	f := newFake(t)
	f.dropTagOnPatch = true // a Spoolman that loses extra on the write
	cs := session(t, f)
	out := call(t, cs, "update_spool", map[string]any{"spool_id": 9, "filament_id": 3})
	if out["extra_tag_intact"] != false {
		t.Fatalf("a lost tag must be reported: %v", out)
	}
	alert, _ := out["alert"].(string)
	if !strings.HasPrefix(alert, "ALERT:") || !strings.Contains(alert, "00000000000000000000000000000004") || out["tag_set"] != false {
		t.Fatalf("alert %q, result %v", alert, out)
	}
	if out["extra_tag_before"] != "00000000000000000000000000000004" || out["extra_tag_after"] != nil {
		t.Fatalf("%v", out)
	}
}

func TestMoveSpoolWithOtherFieldsUsesASecondPatch(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	call(t, cs, "update_spool", map[string]any{"spool_id": 9, "filament_id": 3, "location": "shelf 2"})
	if len(f.smWrites) != 2 || f.smWrites[0] != "PATCH /api/v1/spool/9" || f.smWrites[1] != "PATCH /api/v1/spool/9" {
		t.Fatalf("%v", f.smWrites)
	}
	if first := smBody(t, f, 0); len(first) != 1 || first["filament_id"] != float64(3) {
		t.Fatalf("the filament change goes alone: %s", f.smBodies[0])
	}
	if second := smBody(t, f, 1); len(second) != 1 || second["location"] != "shelf 2" {
		t.Fatalf("%s", f.smBodies[1])
	}
}

func TestMoveSpoolRefusalsWriteNothing(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	for name, args := range map[string]map[string]any{
		"same filament":    {"spool_id": 9, "filament_id": 2},
		"no such filament": {"spool_id": 9, "filament_id": 999},
		"no such spool":    {"spool_id": 999, "filament_id": 3},
		"zero id":          {"spool_id": 9, "filament_id": 0},
		"both weights":     {"spool_id": 9, "filament_id": 3, "remaining_weight_g": 1, "used_weight_g": 1},
	} {
		if res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "update_spool", Arguments: args}); res == nil || !res.IsError {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(f.smWrites) != 0 {
		t.Fatalf("a refused move wrote: %v", f.smWrites)
	}
}

func TestUpdateSpoolWithoutFilamentStillNeverSendsExtra(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	call(t, cs, "update_spool", map[string]any{"spool_id": 9, "location": "drawer"})
	if len(f.smBodies) != 1 || f.smBodies[0] != `{"location":"drawer"}` {
		t.Fatalf("%v", f.smBodies)
	}
}

func TestCreateFilament(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)

	// Defaults, and an existing vendor is reused by name.
	out := call(t, cs, "create_filament", map[string]any{"vendor": " BAMBU lab ", "name": "PLA Basic Black", "material": "PLA", "color_hex": "#000000"})
	if out["created"] != true || out["vendor_created"] != nil {
		t.Fatalf("%v", out)
	}
	body := smBody(t, f, 0)
	if len(f.smWrites) != 1 || f.smWrites[0] != "POST /api/v1/filament" {
		t.Fatalf("no vendor should be created: %v", f.smWrites)
	}
	want := map[string]any{"vendor_id": float64(1), "name": "PLA Basic Black", "material": "PLA", "color_hex": "000000", "density": 1.24, "diameter": 1.75, "weight": float64(1000)}
	if len(body) != len(want) {
		t.Fatalf("body %v", body)
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("%s: got %v want %v", k, body[k], v)
		}
	}

	// A new vendor is created first, and temperatures are passed through.
	out = call(t, cs, "create_filament", map[string]any{"vendor": "Polymaker", "name": "Polyterra Charcoal", "material": "PETG", "color_hex": "333333", "nozzle_temp_c": 240, "bed_temp_c": 70, "weight_g": 750})
	if out["vendor_created"] != true || len(f.smWrites) != 3 || f.smWrites[1] != "POST /api/v1/vendor" || f.smWrites[2] != "POST /api/v1/filament" {
		t.Fatalf("%v %v", out, f.smWrites)
	}
	if b := smBody(t, f, 2); b["settings_extruder_temp"] != float64(240) || b["settings_bed_temp"] != float64(70) || b["weight"] != float64(750) || b["density"] != 1.27 {
		t.Fatalf("%v", b)
	}

	// A duplicate is refused with the existing id.
	n := len(f.smWrites)
	out = call(t, cs, "create_filament", map[string]any{"vendor_id": 1, "name": "black", "material": "pla", "color_hex": "000000"})
	if out["created"] != nil || out["existing_id"].(float64) != 2 || len(f.smWrites) != n {
		t.Fatalf("a duplicate must not be written: %v", out)
	}

	// A product line in the material warns but is allowed.
	out = call(t, cs, "create_filament", map[string]any{"vendor_id": 1, "name": "Odd", "material": "PLA Matte", "color_hex": "111111"})
	if w, _ := json.Marshal(out["warnings"]); !strings.Contains(string(w), "plain material") {
		t.Fatalf("warnings %s", w)
	}
}

func TestCreateFilamentValidation(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	ok := func() map[string]any {
		return map[string]any{"vendor": "Bambu Lab", "name": "X", "material": "PLA", "color_hex": "000000"}
	}
	with := func(k string, v any) map[string]any { m := ok(); m[k] = v; return m }
	for name, args := range map[string]map[string]any{
		"no colour":         with("color_hex", ""),
		"bad colour":        with("color_hex", "blue"),
		"no name":           with("name", " "),
		"vendor and id":     with("vendor_id", 1),
		"neither vendor":    {"name": "X", "material": "PLA", "color_hex": "000000"},
		"unknown vendor id": {"vendor_id": 99, "name": "X", "material": "PLA", "color_hex": "000000"},
		"unknown density":   with("material", "Unobtainium"),
		"hot nozzle":        with("nozzle_temp_c", 900),
		"zero weight":       with("weight_g", 0),
	} {
		if res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_filament", Arguments: args}); res == nil || !res.IsError {
			t.Errorf("%s was accepted", name)
		}
	}
	if len(f.smWrites) != 0 {
		t.Fatalf("a rejected request wrote: %v", f.smWrites)
	}
	// An unknown material is fine once a density is given.
	m := with("material", "Unobtainium")
	m["density_g_cm3"] = 1.5
	call(t, cs, "create_filament", m)
}

func TestUpdateFilament(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "update_filament", map[string]any{"filament_id": 2, "name": "PLA Basic Black", "color_hex": "#0a0a0a", "weight_g": 1000})
	if len(f.smWrites) != 1 || f.smWrites[0] != "PATCH /api/v1/filament/2" {
		t.Fatalf("%v", f.smWrites)
	}
	if b := smBody(t, f, 0); len(b) != 3 || b["name"] != "PLA Basic Black" || b["color_hex"] != "0A0A0A" {
		t.Fatalf("only the given fields, normalised, and never extra: %s", f.smBodies[0])
	}
	if strings.Contains(f.smBodies[0], "extra") {
		t.Fatal("extra was sent")
	}
	fil := out["filament"].(map[string]any)
	if fil["name"] != "PLA Basic Black" || fil["id"].(float64) != 2 {
		t.Fatalf("%v", out)
	}
	// Spools 3 and 9 are on "Black" and both carry a Bambuddy tag.
	if out["spools_using"].(float64) != 2 || out["spools_tracked_by_bambuddy"].(float64) != 2 {
		t.Fatalf("%v", out)
	}
	if w, _ := json.Marshal(out["warnings"]); !strings.Contains(string(w), "next AMS sync") || !strings.Contains(string(w), "tracked by Bambuddy") {
		t.Fatalf("warnings %s", w)
	}
	// The renamed filament shows up under its new name on the spools.
	if got := f.spools[9]["filament"].(map[string]any)["name"]; got != "PLA Basic Black" {
		t.Fatalf("spool 9 filament name %v", got)
	}

	f.smWrites, f.smBodies = nil, nil
	out = call(t, cs, "update_filament", map[string]any{"filament_id": 2, "material": "PETG"})
	if w, _ := json.Marshal(out["warnings"]); !strings.Contains(string(w), "match on material first") {
		t.Fatalf("a material change must warn: %s", w)
	}
	for name, args := range map[string]map[string]any{
		"nothing":    {"filament_id": 2},
		"bad colour": {"filament_id": 2, "color_hex": "zz"},
		"empty name": {"filament_id": 2, "name": " "},
		"missing":    {"filament_id": 999, "name": "x"},
		"zero wt":    {"filament_id": 2, "weight_g": 0},
	} {
		n := len(f.smWrites)
		if res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "update_filament", Arguments: args}); res == nil || !res.IsError || len(f.smWrites) != n {
			t.Errorf("%s: accepted or wrote", name)
		}
	}
}

func TestSpoolListsShowTagAndSlot(t *testing.T) {
	f := newFake(t)
	delete(f.spools[2], "extra") // a spool Bambuddy does not track
	cs := session(t, f)
	out := call(t, cs, "list_spools", map[string]any{"in_stock_only": false})
	byID := map[int]map[string]any{}
	for _, r := range out["spools"].([]any) {
		m := r.(map[string]any)
		byID[int(m["id"].(float64))] = m
		if _, present := m["tag_set"]; !present {
			t.Fatalf("tag_set must be on every row: %v", m)
		}
	}
	if byID[2]["tag_set"] != false || byID[9]["tag_set"] != true {
		t.Fatalf("spool 2 untracked, 9 tracked: %v %v", byID[2], byID[9])
	}
	if byID[9]["ams_slot"].(float64) != 1 || byID[8]["ams_slot"].(float64) != 3 || byID[2]["ams_slot"] != nil {
		t.Fatalf("slots: %v %v %v", byID[9]["ams_slot"], byID[8]["ams_slot"], byID[2]["ams_slot"])
	}
	one := call(t, cs, "get_spool", map[string]any{"spool_id": 9})
	if one["tag_set"] != true {
		t.Fatalf("%v", one)
	}
	if call(t, cs, "get_spool", map[string]any{"spool_id": 2})["tag_set"] != false {
		t.Fatal("get_spool must show false too")
	}
}

// ─── same-filament backup trays ──────────────────────────────────────────────

// twinTrays makes AMS slot 3 a second tray of the same black PLA Matte as
// slot 1, with the given grams on the spools linked to each, and sets the
// file's need.
func twinTrays(f *fake, slot1G, slot3G, needG float64) {
	tray := f.status["ams"].([]any)[0].(map[string]any)["tray"].([]any)
	tray[3].(map[string]any)["tray_color"] = "000000FF"
	tray[3].(map[string]any)["tray_sub_brands"] = "PLA Matte"
	f.spools[9]["remaining_weight"] = slot1G
	f.spools[8]["remaining_weight"] = slot3G
	f.needG = needG
}

func TestPrintFileUsesABackupTrayOfTheSameFilament(t *testing.T) {
	tests := []struct {
		name             string
		args             map[string]any
		slot1G, slot3G   float64
		needG            float64
		wantAllowed      bool
		wantMapping      string
		wantWarn, wantNo string
	}{
		{"explicit mapping, 127 g plus 854 g for 146 g", map[string]any{"ams_mapping": []int{1}}, 127, 854, 146, true, "[1]", "AMS slot 3 (spool #8, 854 g)", ""},
		{"default mapping picks the 127 g tray", nil, 127, 854, 146, true, "[1]", "switch over", ""},
		{"default mapping picks the nearly empty tray even in the higher slot", nil, 854, 127, 146, true, "[3]", "AMS slot 1 (spool #9, 854 g)", ""},
		{"group total short is refused", map[string]any{"ams_mapping": []int{1}}, 50, 60, 146, false, "[1]", "", "have 110 g between them but filament slot 1 needs 146 g"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.setState("FINISH", false)
			twinTrays(f, tc.slot1G, tc.slot3G, tc.needG)
			cs := session(t, f)
			args := map[string]any{"library_file_id": 9, "dry_run": true}
			for k, v := range tc.args {
				args[k] = v
			}
			out := call(t, cs, "print_file", args)
			pf := out["preflight"].(map[string]any)
			if pf["allowed"] != tc.wantAllowed || fmt.Sprint(pf["ams_mapping"]) != tc.wantMapping {
				t.Fatalf("%v", out)
			}
			text := fmt.Sprint(pf["warnings"], pf["refusals"])
			if !strings.Contains(text, tc.wantWarn) || (tc.wantNo != "" && !strings.Contains(text, tc.wantNo)) {
				t.Fatalf("want %q / %q in %s", tc.wantWarn, tc.wantNo, text)
			}
			if tc.wantAllowed {
				groups, _ := pf["groups"].([]any)
				if len(groups) != 1 || fmt.Sprint(groups[0].(map[string]any)["ams_slots"]) != "[1 3]" {
					t.Fatalf("dry run should show the group it found: %v", pf["groups"])
				}
			}
			if f.writeCount() != 0 {
				t.Fatalf("dry run wrote: %v", f.writes)
			}
		})
	}
}

func TestPrintFileDryRunSaysWhyItChoseATray(t *testing.T) {
	f := newFake(t)
	f.setState("FINISH", false)
	twinTrays(f, 127, 854, 146)
	cs := session(t, f)
	pf := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "dry_run": true})["preflight"].(map[string]any)
	why := fmt.Sprint(pf["mapping_reasons"])
	for _, want := range []string{"AMS slots 1, 3 match equally", "least filament left (127 g)", "group of 2 trays holds 981 g"} {
		if !strings.Contains(why, want) {
			t.Errorf("mapping_reasons missing %q: %s", want, why)
		}
	}
}

func TestPrintFileDoesNotCountADifferentColourTray(t *testing.T) {
	f := newFake(t)
	f.setState("FINISH", false)
	twinTrays(f, 127, 854, 146)
	f.status["ams"].([]any)[0].(map[string]any)["tray"].([]any)[3].(map[string]any)["tray_color"] = "EC008CFF"
	cs := session(t, f)
	out := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "ams_mapping": []int{1}, "dry_run": true})
	pf := out["preflight"].(map[string]any)
	if pf["allowed"] != false || !strings.Contains(fmt.Sprint(pf["refusals"]), "has 127 g but filament slot 1 needs 146 g") || pf["groups"] != nil {
		t.Fatalf("a magenta tray is not a backup for black: %v", out)
	}
}

func TestPrintFileNeverCountsAnUnlinkedTrayAsTheNearlyEmptyOne(t *testing.T) {
	tests := []struct {
		name           string
		slot1G, slot3G float64
		args           map[string]any
		wantAllowed    bool
		wantMapping    string
		wantText       string
		wantNot        string
	}{
		// Slot 3 reads 5% on the AMS but has no spool, so it is not the nearly empty one.
		{"default mapping keeps to the linked tray", 854, 127, nil, true, "[1]", "", "least filament left (127 g)"},
		// An unlinked backup adds no grams, so 127 g alone cannot cover 146 g.
		{"unlinked backup adds nothing", 127, 854, map[string]any{"ams_mapping": []int{1}}, false, "[1]", "AMS slot 3 has no linked spool, so its grams are not counted", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake(t)
			f.setState("FINISH", false)
			twinTrays(f, tc.slot1G, tc.slot3G, 146)
			f.unassignedTray = map[int]bool{3: true}
			f.spools[8]["extra"] = map[string]any{}
			tray := f.status["ams"].([]any)[0].(map[string]any)["tray"].([]any)[3].(map[string]any)
			tray["remain"] = 5
			cs := session(t, f)
			args := map[string]any{"library_file_id": 9, "dry_run": true}
			for k, v := range tc.args {
				args[k] = v
			}
			out := call(t, cs, "print_file", args)
			pf := out["preflight"].(map[string]any)
			if pf["allowed"] != tc.wantAllowed || fmt.Sprint(pf["ams_mapping"]) != tc.wantMapping {
				t.Fatalf("%v", out)
			}
			text := fmt.Sprint(pf["warnings"], pf["refusals"], pf["mapping_reasons"])
			if tc.wantText != "" && !strings.Contains(text, tc.wantText) {
				t.Fatalf("want %q in %s", tc.wantText, text)
			}
			if tc.wantNot != "" && strings.Contains(text, tc.wantNot) {
				t.Fatalf("did not want %q in %s", tc.wantNot, text)
			}
		})
	}
}
