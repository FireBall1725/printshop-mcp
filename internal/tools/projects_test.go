// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A stateful stand-in for Bambuddy's project routes, seeded from the recorded
// project: two attachments that share an original name, a cover, two BOM lines
// and one archive. It enforces the status and priority sets Bambuddy accepted.

type fakeProject struct {
	data    map[string]any
	bom     []map[string]any
	atts    []map[string]any
	files   map[string][]byte
	cover   string
	coverB  []byte
	archive []int
	queue   []int
}

type fakeProjects struct {
	byID map[int]*fakeProject
	next int
}

var (
	okStatus   = map[string]bool{"active": true, "completed": true, "archived": true}
	okPriority = map[string]bool{"low": true, "normal": true, "high": true, "urgent": true}
)

func jsonCopy(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func (f *fake) projects(t *testing.T) *fakeProjects {
	if f.proj != nil {
		return f.proj
	}
	detail := readJSON(t, "project_detail.json").(map[string]any)
	bom := readJSON(t, "project_bom.json").([]any)
	p := &fakeProject{data: detail, files: map[string][]byte{}, archive: []int{20}}
	for i, a := range detail["attachments"].([]any) {
		m := a.(map[string]any)
		p.atts = append(p.atts, m)
		p.files[m["filename"].(string)] = []byte(fmt.Sprintf("attachment body %d", i))
	}
	for _, b := range bom {
		p.bom = append(p.bom, b.(map[string]any))
	}
	p.cover, p.coverB = detail["cover_image_filename"].(string), []byte("\x89PNG-old-cover")
	f.proj = &fakeProjects{byID: map[int]*fakeProject{int(detail["id"].(float64)): p}, next: 50}
	return f.proj
}

func (f *fake) serveProjects(w http.ResponseWriter, r *http.Request) {
	t := f.t
	fp := f.projects(t)
	out := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	fail := func(code int, msg string) { w.WriteHeader(code); out(map[string]any{"detail": msg}) }
	body := ""
	if len(f.bodies) > 0 && r.Method != http.MethodGet {
		body = f.bodies[len(f.bodies)-1]
	}
	var in map[string]any
	_ = json.Unmarshal([]byte(body), &in)

	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/projects"), "/")
	parts := strings.Split(path, "/")
	view := func(p *fakeProject) map[string]any {
		m := jsonCopy(p.data)
		atts := make([]any, 0)
		for _, a := range p.atts {
			atts = append(atts, a)
		}
		if len(atts) > 0 {
			m["attachments"] = atts
		} else {
			m["attachments"] = nil
		}
		if p.cover != "" {
			m["cover_image_filename"] = p.cover
		} else {
			m["cover_image_filename"] = nil
		}
		m["stats"] = map[string]any{"total_archives": len(p.archive), "total_items": len(p.archive) + len(p.queue), "completed_prints": len(p.archive), "failed_prints": 0,
			"queued_prints": len(p.queue), "in_progress_prints": 0, "bom_total_items": len(p.bom), "bom_completed_items": 0}
		return m
	}
	newProject := func(m map[string]any) int {
		fp.next++
		m["id"] = float64(fp.next)
		for k, v := range map[string]any{"status": "active", "priority": "normal", "is_template": false, "children": []any{}, "created_at": "2026-10-17T00:00:00", "updated_at": "2026-10-17T00:00:00"} {
			if _, ok := m[k]; !ok {
				m[k] = v
			}
		}
		fp.byID[fp.next] = &fakeProject{data: m, files: map[string][]byte{}}
		return fp.next
	}

	// Collection routes.
	switch {
	case path == "" && r.Method == http.MethodGet:
		var rows []any
		for _, p := range fp.byID {
			if p.data["is_template"] != true {
				rows = append(rows, view(p))
			}
		}
		out(rows)
		return
	case path == "templates":
		var rows []any
		for _, p := range fp.byID {
			if p.data["is_template"] == true {
				rows = append(rows, view(p))
			}
		}
		out(rows)
		return
	case path == "" && r.Method == http.MethodPost:
		out(view(fp.byID[newProject(in)]))
		return
	case parts[0] == "from-template" && len(parts) == 2:
		id, _ := strconv.Atoi(parts[1])
		src := fp.byID[id]
		if src == nil {
			fail(404, "Template not found")
			return
		}
		m := jsonCopy(src.data)
		m["is_template"], m["name"] = false, r.URL.Query().Get("name")
		out(view(fp.byID[newProject(m)]))
		return
	case path == "import/file":
		var name string
		if mr, err := multipartReader(r, body); err == nil {
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				b, _ := io.ReadAll(part)
				var j map[string]any
				if json.Unmarshal(b, &j) == nil {
					name, _ = j["name"].(string)
				}
			}
		}
		if name == "" {
			name = "Imported"
		}
		out(view(fp.byID[newProject(map[string]any{"name": name})]))
		return
	}

	id, _ := strconv.Atoi(parts[0])
	p := fp.byID[id]
	if p == nil {
		w.WriteHeader(404)
		out(map[string]any{"detail": "Project not found"})
		return
	}
	sub := ""
	if len(parts) > 1 {
		sub = parts[1]
	}
	switch {
	case sub == "" && r.Method == http.MethodGet:
		out(view(p))
	case sub == "" && r.Method == http.MethodPatch:
		if s, ok := in["status"].(string); ok && !okStatus[s] {
			fail(400, "Invalid status")
			return
		}
		if s, ok := in["priority"].(string); ok && !okPriority[s] {
			fail(400, "Invalid priority")
			return
		}
		for k, v := range in {
			p.data[k] = v
		}
		out(view(p))
	case sub == "" && r.Method == http.MethodDelete:
		delete(fp.byID, id)
		out(map[string]any{"message": "deleted"})
	case sub == "create-template":
		m := jsonCopy(p.data)
		m["is_template"], m["name"] = true, p.data["name"].(string)+" (Template)"
		out(view(fp.byID[newProject(m)]))
	case sub == "archives":
		out(readJSON(t, "archives.json").([]any)[:len(p.archive)])
	case sub == "queue":
		out([]any{})
	case sub == "add-archives":
		for _, a := range in["archive_ids"].([]any) {
			p.archive = append(p.archive, int(a.(float64)))
		}
		out(map[string]any{"message": "ok"})
	case sub == "add-queue":
		for _, a := range in["queue_item_ids"].([]any) {
			p.queue = append(p.queue, int(a.(float64)))
		}
		out(map[string]any{"message": "ok"})
	case sub == "remove-archives":
		drop := map[int]bool{}
		for _, a := range in["archive_ids"].([]any) {
			drop[int(a.(float64))] = true
		}
		keep := p.archive[:0]
		for _, a := range p.archive {
			if !drop[a] {
				keep = append(keep, a)
			}
		}
		p.archive = keep
		out(map[string]any{"message": "ok"})
	case sub == "timeline":
		out(readJSON(t, "project_timeline.json"))
	case sub == "bom" && len(parts) == 2 && r.Method == http.MethodGet:
		rows := make([]any, 0)
		for _, b := range p.bom {
			rows = append(rows, b)
		}
		out(rows)
	case sub == "bom" && len(parts) == 2 && r.Method == http.MethodPost:
		in["id"], in["project_id"], in["quantity_acquired"], in["is_complete"] = float64(900+len(p.bom)), float64(id), float64(0), false
		if _, ok := in["quantity_needed"]; !ok {
			in["quantity_needed"] = float64(1)
		}
		p.bom = append(p.bom, in)
		out(in)
	case sub == "bom" && len(parts) == 3:
		bid, _ := strconv.Atoi(parts[2])
		for i, b := range p.bom {
			if int(b["id"].(float64)) != bid {
				continue
			}
			if r.Method == http.MethodDelete {
				p.bom = append(p.bom[:i], p.bom[i+1:]...)
				out(map[string]any{"message": "deleted"})
				return
			}
			for k, v := range in {
				b[k] = v
			}
			b["is_complete"] = b["quantity_acquired"].(float64) >= b["quantity_needed"].(float64)
			out(b)
			return
		}
		fail(404, "BOM item not found")
	case sub == "attachments" && len(parts) == 2 && r.Method == http.MethodPost:
		name, content := formFile(r, body)
		stored := fmt.Sprintf("stored%d%s", len(p.atts)+len(p.files), filepath.Ext(name))
		p.atts = append(p.atts, map[string]any{"filename": stored, "original_name": name, "size": float64(len(content)), "uploaded_at": "2026-10-17T00:00:00"})
		p.files[stored] = content
		out(map[string]any{"status": "success", "filename": stored, "original_name": name})
	case sub == "attachments" && len(parts) == 3:
		stored := parts[2]
		content, ok := p.files[stored]
		if !ok {
			fail(404, "Attachment not found")
			return
		}
		if r.Method == http.MethodDelete {
			delete(p.files, stored)
			keep := p.atts[:0]
			for _, a := range p.atts {
				if a["filename"] != stored {
					keep = append(keep, a)
				}
			}
			p.atts = keep
			out(map[string]any{"message": "deleted"})
			return
		}
		_, _ = w.Write(content)
	case sub == "cover-image" && r.Method == http.MethodPost:
		name, content := formFile(r, body)
		p.cover, p.coverB = "cover_new"+filepath.Ext(name), content
		out(map[string]any{"status": "success", "filename": p.cover, "size": float64(len(content))})
	case sub == "cover-image" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(p.coverB)
	case sub == "cover-image" && r.Method == http.MethodDelete:
		p.cover, p.coverB = "", nil
		out(map[string]any{"message": "deleted"})
	case sub == "export":
		if r.URL.Query().Get("format") == "json" {
			out(map[string]any{"name": p.data["name"]})
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="PixelBar_2026-10-17.zip"`)
		_, _ = w.Write([]byte("PK\x03\x04zipbytes"))
	default:
		http.NotFound(w, r)
	}
}

func multipartReader(r *http.Request, body string) (*multipart.Reader, error) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	return multipart.NewReader(strings.NewReader(body), params["boundary"]), nil
}

func formFile(r *http.Request, body string) (name string, content []byte) {
	mr, err := multipartReader(r, body)
	if err != nil {
		return "", nil
	}
	for {
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		if part.FormName() == "file" {
			b, _ := io.ReadAll(part)
			return part.FileName(), b
		}
	}
}

func fail2(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error()
	}
	if !res.IsError {
		t.Fatalf("%s should have failed: %v", name, res.StructuredContent)
	}
	b, _ := json.Marshal(res.Content)
	return string(b)
}

const projID = 4 // the id of the recorded project

func TestProjectToolsListAndGet(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	list := call(t, cs, "list_projects", map[string]any{})
	rows := list["projects"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["name"] != "PixelBar" || rows[0].(map[string]any)["due_date"] != "2026-11-30" {
		t.Fatalf("%v", list)
	}
	one := call(t, cs, "get_project", map[string]any{"project_id": projID, "include_archives": true, "include_queue": true, "include_bom": true, "include_timeline": true})
	for _, k := range []string{"archive_list", "bom", "timeline", "attachments", "stats"} {
		if one[k] == nil {
			t.Errorf("get_project lost %s: %v", k, one)
		}
	}
	if len(one["bom"].([]any)) != 2 || one["archives"].(float64) != 1 || one["cover_image"] == nil {
		t.Fatalf("%v", one)
	}
	plain := call(t, cs, "get_project", map[string]any{"project_id": projID})
	if plain["bom"] != nil || plain["archive_list"] != nil {
		t.Fatalf("the sections are opt-in: %v", plain)
	}
	if res, _ := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_project", Arguments: map[string]any{"project_id": 999}}); res == nil || !res.IsError {
		t.Fatal("a missing project is an error")
	}
	if f.writeCount() != 0 {
		t.Fatalf("reads wrote: %v", f.writes)
	}
}

func TestCreateAndUpdateProject(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "create_project", map[string]any{"name": " PixelBar v2 ", "color": "ff69b4", "tags": []string{"a", " b ", ""}, "due_date": "2026-12-01", "priority": "urgent", "budget": 40, "target_count": 5})
	if out["name"] != "PixelBar v2" || out["color"] != "#FF69B4" || out["tags"] != "a,b" || out["priority"] != "urgent" {
		t.Fatalf("%v", out)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(f.bodies[0]), &sent)
	if sent["color"] != "#FF69B4" || sent["tags"] != "a,b" || sent["name"] != "PixelBar v2" {
		t.Fatalf("what was sent: %s", f.bodies[0])
	}
	id := int(out["id"].(float64))

	up := call(t, cs, "update_project", map[string]any{"project_id": id, "status": "completed", "notes": "done"})
	if up["status"] != "completed" || up["notes"] != "done" {
		t.Fatalf("%v", up)
	}
	if len(f.bodies) != 2 || f.bodies[1] != `{"notes":"done","status":"completed"}` {
		t.Fatalf("only the given fields: %v", f.bodies)
	}

	// Bad input is rejected without a write; Bambuddy's own rejection gets a hint.
	n := f.writeCount()
	for name, args := range map[string]map[string]any{
		"empty name": {"name": " "},
		"bad colour": {"name": "x", "color": "notacolor"},
		"bad date":   {"name": "x", "due_date": "next week"},
		"negative":   {"name": "x", "budget": -1},
		"neg target": {"name": "x", "target_count": -2},
	} {
		fail2(t, cs, "create_project", args)
		if f.writeCount() != n {
			t.Fatalf("%s wrote", name)
		}
	}
	if msg := fail2(t, cs, "update_project", map[string]any{"project_id": id, "status": "bogus"}); !strings.Contains(msg, "active, completed, archived") {
		t.Fatalf("hint missing: %s", msg)
	}
	if msg := fail2(t, cs, "update_project", map[string]any{"project_id": id, "priority": "bogus"}); !strings.Contains(msg, "low, normal, high, urgent") {
		t.Fatalf("hint missing: %s", msg)
	}
	if msg := fail2(t, cs, "update_project", map[string]any{"project_id": id}); !strings.Contains(msg, "nothing to change") {
		t.Fatalf("%s", msg)
	}
}

func TestTemplates(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	tpl := call(t, cs, "save_project_as_template", map[string]any{"project_id": projID})
	if tpl["is_template"] != true || tpl["name"] != "PixelBar (Template)" {
		t.Fatalf("%v", tpl)
	}
	tid := tpl["id"].(float64)
	if got := call(t, cs, "list_project_templates", map[string]any{})["projects"].([]any); len(got) != 1 {
		t.Fatalf("%v", got)
	}
	if got := call(t, cs, "list_projects", map[string]any{})["projects"].([]any); len(got) != 1 {
		t.Fatalf("a template is not a project: %v", got)
	}
	np := call(t, cs, "create_project_from_template", map[string]any{"template_id": tid, "name": "PixelBar Mini"})
	if np["name"] != "PixelBar Mini" || np["is_template"] == true {
		t.Fatalf("%v", np)
	}
	fail2(t, cs, "create_project_from_template", map[string]any{"template_id": tid, "name": " "})
}

func TestDeleteProjectNeedsConfirm(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "delete_project", map[string]any{"project_id": projID, "confirm": false})
	if out["done"] != false || f.writeCount() != 0 {
		t.Fatalf("deleted without confirm: %v %v", out, f.writes)
	}
	imp, _ := out["impact"].(string)
	for _, want := range []string{`"PixelBar"`, "2 BOM item(s)", "2 attachment(s)", "stay but lose their project"} {
		if !strings.Contains(imp, want) {
			t.Errorf("impact missing %q: %s", want, imp)
		}
	}
	out = call(t, cs, "delete_project", map[string]any{"project_id": projID, "confirm": true})
	if out["done"] != true || len(f.writes) != 1 || f.writes[0] != "DELETE /api/v1/projects/4" {
		t.Fatalf("%v %v", out, f.writes)
	}
	if len(call(t, cs, "list_projects", map[string]any{})["projects"].([]any)) != 0 {
		t.Fatal("still listed")
	}
}

func TestProjectItemsAndRemoval(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	out := call(t, cs, "project_add_items", map[string]any{"project_id": projID, "archive_ids": []int{21, 21, 22}, "queue_item_ids": []int{30}})
	if out["done"] != true || !strings.Contains(out["message"].(string), "2 archive(s) and 1 queue item(s)") {
		t.Fatalf("duplicate ids collapse: %v", out)
	}
	if f.writes[0] != "POST /api/v1/projects/4/add-archives" || f.bodies[0] != `{"archive_ids":[21,22]}` || f.writes[1] != "POST /api/v1/projects/4/add-queue" {
		t.Fatalf("%v %v", f.writes, f.bodies)
	}
	if out := call(t, cs, "project_remove_archives", map[string]any{"project_id": projID, "archive_ids": []int{21, 22}}); !strings.Contains(out["message"].(string), "prints are kept") {
		t.Fatalf("%v", out)
	}
	n := f.writeCount()
	for name, args := range map[string]map[string]any{
		"nothing given":   {"project_id": projID},
		"bad id":          {"project_id": projID, "archive_ids": []int{0}},
		"missing project": {"project_id": 999, "archive_ids": []int{1}},
	} {
		fail2(t, cs, "project_add_items", args)
		if f.writeCount() != n {
			t.Fatalf("%s wrote", name)
		}
	}
}

func TestBOMTools(t *testing.T) {
	f := newFake(t)
	cs := session(t, f)
	add := call(t, cs, "bom_add_item", map[string]any{"project_id": projID, "name": " Heat inserts ", "quantity_needed": 6, "unit_price": 0.2, "sourcing_url": "https://example.com/inserts"})
	item := add["item"].(map[string]any)
	id := int(item["id"].(float64))
	if item["name"] != "Heat inserts" || item["needed"].(float64) != 6 || item["complete"] != false {
		t.Fatalf("%v", add)
	}
	up := call(t, cs, "bom_update_item", map[string]any{"project_id": projID, "item_id": id, "quantity_acquired": 6})
	if up["item"].(map[string]any)["complete"] != true {
		t.Fatalf("reaching the quantity completes the line: %v", up)
	}

	n := f.writeCount()
	out := call(t, cs, "bom_delete_item", map[string]any{"project_id": projID, "item_id": id, "confirm": false})
	if out["done"] != false || f.writeCount() != n || !strings.Contains(out["message"].(string), "Heat inserts") {
		t.Fatalf("deleted without confirm: %v", out)
	}
	if out := call(t, cs, "bom_delete_item", map[string]any{"project_id": projID, "item_id": id, "confirm": true}); out["done"] != true {
		t.Fatalf("%v", out)
	}
	for name, args := range map[string]map[string]any{
		"add no name":  {"project_id": projID, "name": " "},
		"add zero qty": {"project_id": projID, "name": "x", "quantity_needed": 0},
	} {
		fail2(t, cs, "bom_add_item", args)
		_ = name
	}
	fail2(t, cs, "bom_update_item", map[string]any{"project_id": projID, "item_id": id})
	fail2(t, cs, "bom_delete_item", map[string]any{"project_id": projID, "item_id": 424242, "confirm": true})
}

func TestProjectFiles(t *testing.T) {
	f := newFake(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cs := session(t, f)
	p := f.projects(t).byID[projID]
	first, second := p.atts[0]["filename"].(string), p.atts[1]["filename"].(string)

	// Upload: a note, then a cover is refused over the existing one without replace.
	src := filepath.Join(home, "fit-notes.txt")
	if err := os.WriteFile(src, []byte("tight fit at the left tab"), 0o600); err != nil {
		t.Fatal(err)
	}
	up := call(t, cs, "project_upload_file", map[string]any{"project_id": projID, "kind": "attachment", "path": "~/fit-notes.txt"})
	stored := up["filename"].(string)
	if up["done"] != true || up["name"] != "fit-notes.txt" || string(p.files[stored]) != "tight fit at the left tab" {
		t.Fatalf("%v", up)
	}
	img := filepath.Join(home, "new.png")
	_ = os.WriteFile(img, []byte("\x89PNG-new"), 0o600)
	n := f.writeCount()
	if out := call(t, cs, "project_upload_file", map[string]any{"project_id": projID, "kind": "cover", "path": img}); out["done"] != false || f.writeCount() != n || !strings.Contains(out["message"].(string), "replace: true") {
		t.Fatalf("a cover must not be replaced without replace: %v", out)
	}
	if string(p.coverB) != "\x89PNG-old-cover" {
		t.Fatal("the old cover changed")
	}
	if out := call(t, cs, "project_upload_file", map[string]any{"project_id": projID, "kind": "cover", "path": img, "replace": true}); out["done"] != true || !strings.Contains(out["message"].(string), "replaced") || string(p.coverB) != "\x89PNG-new" {
		t.Fatalf("%v", out)
	}
	fail2(t, cs, "project_upload_file", map[string]any{"project_id": projID, "kind": "cover", "path": src, "replace": true}) // not an image
	fail2(t, cs, "project_upload_file", map[string]any{"project_id": projID, "kind": "attachment", "path": filepath.Join(home, "missing.bin")})
	fail2(t, cs, "project_upload_file", map[string]any{"project_id": projID, "kind": "other", "path": src})

	// Download: by stored name; the shared original name is ambiguous; a folder takes the original name.
	dest := filepath.Join(home, "got.txt")
	dl := call(t, cs, "project_download_file", map[string]any{"project_id": projID, "kind": "attachment", "filename": first, "save_to": "~/got.txt"})
	if b, _ := os.ReadFile(dest); string(b) != "attachment body 0" || dl["saved_to"] != "~/got.txt" {
		t.Fatalf("%v", dl)
	}
	if strings.Contains(fmt.Sprint(dl), home) {
		t.Fatalf("the result must not carry the home path: %v", dl)
	}
	if msg := fail2(t, cs, "project_download_file", map[string]any{"project_id": projID, "kind": "attachment", "filename": "mount-notes.txt", "save_to": filepath.Join(home, "x.txt")}); !strings.Contains(msg, "2 attachments") || !strings.Contains(msg, second) {
		t.Fatalf("%s", msg)
	}
	if msg := fail2(t, cs, "project_download_file", map[string]any{"project_id": projID, "kind": "attachment", "filename": first, "save_to": dest}); !strings.Contains(msg, "already exists") {
		t.Fatalf("no silent overwrite: %s", msg)
	}
	if b, _ := os.ReadFile(dest); string(b) != "attachment body 0" {
		t.Fatal("the existing file was changed")
	}
	outDir := filepath.Join(home, "out")
	if err := os.Mkdir(outDir, 0o700); err != nil {
		t.Fatal(err)
	}
	call(t, cs, "project_download_file", map[string]any{"project_id": projID, "kind": "attachment", "filename": "fit-notes.txt", "save_to": outDir})
	if b, _ := os.ReadFile(filepath.Join(outDir, "fit-notes.txt")); string(b) != "tight fit at the left tab" {
		t.Fatal("a folder target takes the original name")
	}
	call(t, cs, "project_download_file", map[string]any{"project_id": projID, "kind": "attachment", "filename": second, "save_to": dest, "overwrite": true})
	if b, _ := os.ReadFile(dest); string(b) != "attachment body 1" {
		t.Fatal("overwrite: true should replace it")
	}
	fail2(t, cs, "project_download_file", map[string]any{"project_id": projID, "kind": "attachment", "filename": first, "save_to": filepath.Join(home, "nodir", "a.txt")})
	if leftovers, _ := filepath.Glob(filepath.Join(home, ".printshop-*")); len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
	call(t, cs, "project_download_file", map[string]any{"project_id": projID, "kind": "cover", "save_to": filepath.Join(home, "cover.png")})
	if b, _ := os.ReadFile(filepath.Join(home, "cover.png")); string(b) != "\x89PNG-new" {
		t.Fatal("cover download")
	}

	// Delete needs a yes.
	n = f.writeCount()
	out := call(t, cs, "project_delete_file", map[string]any{"project_id": projID, "kind": "attachment", "filename": stored, "confirm": false})
	if out["done"] != false || f.writeCount() != n || p.files[stored] == nil || !strings.Contains(out["message"].(string), "fit-notes.txt") {
		t.Fatalf("deleted without confirm: %v", out)
	}
	if out := call(t, cs, "project_delete_file", map[string]any{"project_id": projID, "kind": "attachment", "filename": "fit-notes.txt", "confirm": true}); out["done"] != true || p.files[stored] != nil {
		t.Fatalf("%v", out)
	}
	if out := call(t, cs, "project_delete_file", map[string]any{"project_id": projID, "kind": "cover", "confirm": false}); out["done"] != false || p.cover == "" {
		t.Fatalf("cover deleted without confirm: %v", out)
	}
	if out := call(t, cs, "project_delete_file", map[string]any{"project_id": projID, "kind": "cover", "confirm": true}); out["done"] != true || p.cover != "" {
		t.Fatalf("%v", out)
	}
	fail2(t, cs, "project_download_file", map[string]any{"project_id": projID, "kind": "cover", "save_to": home}) // none left
}

func TestExportAndImport(t *testing.T) {
	f := newFake(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cs := session(t, f)

	z := call(t, cs, "export_project", map[string]any{"project_id": projID, "save_to": home})
	if z["saved_to"] != "~/PixelBar_2026-10-17.zip" {
		t.Fatalf("a folder target takes the server's filename: %v", z)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "PixelBar_2026-10-17.zip")); !bytes.HasPrefix(b, []byte("PK")) {
		t.Fatal("zip content")
	}
	fail2(t, cs, "export_project", map[string]any{"project_id": projID, "save_to": home}) // would overwrite
	j := call(t, cs, "export_project", map[string]any{"project_id": projID, "format": "json", "save_to": "~/pixelbar.json"})
	if b, _ := os.ReadFile(filepath.Join(home, "pixelbar.json")); !strings.Contains(string(b), "PixelBar") || j["done"] != true {
		t.Fatalf("%v %s", j, b)
	}
	fail2(t, cs, "export_project", map[string]any{"project_id": projID, "format": "xml", "save_to": home})

	imp := call(t, cs, "import_project", map[string]any{"path": "~/pixelbar.json"})
	if imp["name"] != "PixelBar" || imp["id"].(float64) == projID {
		t.Fatalf("an import makes a new project: %v", imp)
	}
	if len(f.projects(t).byID) != 2 {
		t.Fatal("the original must be untouched")
	}
	_ = os.WriteFile(filepath.Join(home, "x.stl"), []byte("x"), 0o600)
	fail2(t, cs, "import_project", map[string]any{"path": "~/x.stl"})
}

func TestPrintFileFilesUnderAProject(t *testing.T) {
	f := newFake(t)
	f.setState("FINISH", false)
	cs := session(t, f)
	n := f.writeCount()
	if msg := fail2(t, cs, "print_file", map[string]any{"library_file_id": 9, "project_id": 999}); !strings.Contains(msg, "not printing") || f.writeCount() != n {
		t.Fatalf("an unknown project must stop the print before it is queued: %s %v", msg, f.writes)
	}
	out := call(t, cs, "print_file", map[string]any{"library_file_id": 9, "project_id": projID, "wait_seconds": 0})
	if out["queued"] != true {
		t.Fatalf("%v", out)
	}
	var body map[string]any
	_ = json.Unmarshal([]byte(f.bodies[len(f.bodies)-1]), &body)
	if body["project_id"] != float64(projID) {
		t.Fatalf("project_id missing from the queue body: %v", body)
	}
	// Without a project the field is absent.
	f.setState("FINISH", false)
	call(t, cs, "print_file", map[string]any{"library_file_id": 9, "wait_seconds": 0})
	if strings.Contains(f.bodies[len(f.bodies)-1], "project_id") {
		t.Fatalf("project_id must be omitted when not given: %s", f.bodies[len(f.bodies)-1])
	}
}

func TestProjectToolsAreGated(t *testing.T) {
	cs := session(t, newFake(t))
	list, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]*mcp.Tool{}
	for _, tl := range list.Tools {
		have[tl.Name] = tl
	}
	for _, n := range strings.Fields("list_projects get_project list_project_templates create_project create_project_from_template save_project_as_template update_project delete_project " +
		"project_add_items project_remove_archives bom_add_item bom_update_item bom_delete_item project_upload_file project_download_file project_delete_file export_project import_project") {
		if have[n] == nil {
			t.Errorf("missing tool %s", n)
		}
	}
	for _, n := range []string{"delete_project", "bom_delete_item", "project_delete_file"} {
		tl := have[n]
		if tl == nil || !strings.Contains(tl.Description, "must ask the user first") || tl.Annotations == nil || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint {
			t.Errorf("%s must be marked destructive and tell Claude to ask first", n)
		}
	}
}
