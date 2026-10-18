// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var coverExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true}

// ─── arguments ───────────────────────────────────────────────────────────────

type listProjectsArgs struct {
	Status string `json:"status,omitempty" jsonschema:"Only projects with this status: active, completed or archived."`
}

type projectsResult struct {
	Projects []plan.ProjectRow `json:"projects"`
}

type getProjectArgs struct {
	ProjectID       int  `json:"project_id" jsonschema:"Bambuddy project id from list_projects."`
	IncludeArchives bool `json:"include_archives,omitempty" jsonschema:"Also list the project's finished prints."`
	IncludeQueue    bool `json:"include_queue,omitempty" jsonschema:"Also list the project's queue items."`
	IncludeBOM      bool `json:"include_bom,omitempty" jsonschema:"Also list the bill of materials."`
	IncludeTimeline bool `json:"include_timeline,omitempty" jsonschema:"Also list recent events."`
	Limit           int  `json:"limit,omitempty" jsonschema:"Rows for archives and the timeline. Defaults to 20."`
}

type projectView struct {
	plan.ProjectDetail
	ArchiveList []plan.ArchiveRow  `json:"archive_list,omitempty"`
	QueueList   []QueueRow         `json:"queue_list,omitempty"`
	BOM         []plan.BOMRow      `json:"bom,omitempty"`
	Timeline    []plan.TimelineRow `json:"timeline,omitempty"`
}

type projectFields struct {
	Description      *string  `json:"description,omitempty"`
	Color            *string  `json:"color,omitempty" jsonschema:"Hex such as FF69B4 or #FF69B4."`
	TargetCount      *int     `json:"target_count,omitempty" jsonschema:"How many plates or runs the project is meant to produce."`
	TargetPartsCount *int     `json:"target_parts_count,omitempty" jsonschema:"How many parts the project is meant to produce."`
	Notes            *string  `json:"notes,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	DueDate          *string  `json:"due_date,omitempty" jsonschema:"YYYY-MM-DD."`
	Priority         *string  `json:"priority,omitempty" jsonschema:"low, normal, high or urgent. Bambuddy rejects values it does not know."`
	Budget           *float64 `json:"budget,omitempty"`
	ParentID         *int     `json:"parent_id,omitempty" jsonschema:"Nest this project under another."`
	URL              *string  `json:"url,omitempty"`
}

type createProjectArgs struct {
	Name string `json:"name" jsonschema:"Project name, such as PixelBar."`
	projectFields
}

type updateProjectArgs struct {
	ProjectID int     `json:"project_id" jsonschema:"Bambuddy project id."`
	Name      *string `json:"name,omitempty"`
	Status    *string `json:"status,omitempty" jsonschema:"active, completed or archived. Bambuddy rejects values it does not know."`
	projectFields
}

type fromTemplateArgs struct {
	TemplateID int    `json:"template_id" jsonschema:"Template id from list_project_templates."`
	Name       string `json:"name" jsonschema:"Name for the new project."`
}

type deleteProjectArgs struct {
	ProjectID int  `json:"project_id" jsonschema:"Bambuddy project id."`
	Confirm   bool `json:"confirm" jsonschema:"Must be true. Set it only after the user has said yes to deleting this named project."`
}

type projectIDArgs struct {
	ProjectID int `json:"project_id" jsonschema:"Bambuddy project id."`
}

type addItemsArgs struct {
	ProjectID    int   `json:"project_id" jsonschema:"Bambuddy project id."`
	ArchiveIDs   []int `json:"archive_ids,omitempty" jsonschema:"Finished prints (list_archives ids) to file under the project."`
	QueueItemIDs []int `json:"queue_item_ids,omitempty" jsonschema:"Queue items (queue_list ids) to file under the project."`
}

type removeArchivesArgs struct {
	ProjectID  int   `json:"project_id" jsonschema:"Bambuddy project id."`
	ArchiveIDs []int `json:"archive_ids" jsonschema:"Archives to take out of the project."`
}

type bomFields struct {
	QuantityNeeded *int     `json:"quantity_needed,omitempty"`
	UnitPrice      *float64 `json:"unit_price,omitempty"`
	SourcingURL    *string  `json:"sourcing_url,omitempty"`
	ArchiveID      *int     `json:"archive_id,omitempty" jsonschema:"The print that makes this part, if it is printed."`
	STLFilename    *string  `json:"stl_filename,omitempty"`
	Remarks        *string  `json:"remarks,omitempty"`
}

type bomAddArgs struct {
	ProjectID int    `json:"project_id" jsonschema:"Bambuddy project id."`
	Name      string `json:"name" jsonschema:"Part name, such as M3x8 screws."`
	bomFields
}

type bomUpdateArgs struct {
	ProjectID        int     `json:"project_id" jsonschema:"Bambuddy project id."`
	ItemID           int     `json:"item_id" jsonschema:"BOM item id from get_project with include_bom."`
	Name             *string `json:"name,omitempty"`
	QuantityAcquired *int    `json:"quantity_acquired,omitempty" jsonschema:"How many you have. Reaching quantity_needed marks the line complete."`
	bomFields
}

type bomDeleteArgs struct {
	ProjectID int  `json:"project_id" jsonschema:"Bambuddy project id."`
	ItemID    int  `json:"item_id" jsonschema:"BOM item id."`
	Confirm   bool `json:"confirm" jsonschema:"Must be true. Set it only after the user has said yes to removing this line."`
}

type uploadFileArgs struct {
	ProjectID int    `json:"project_id" jsonschema:"Bambuddy project id."`
	Kind      string `json:"kind" jsonschema:"attachment for a general file, or cover for the project's cover image."`
	Path      string `json:"path" jsonschema:"File on the machine running this server. A leading ~/ is expanded. A cover must be png, jpg, jpeg, webp or gif."`
	Replace   bool   `json:"replace,omitempty" jsonschema:"Needed to swap in a new cover when one exists; the old image file is deleted. Ask the user first."`
}

type downloadFileArgs struct {
	ProjectID int    `json:"project_id" jsonschema:"Bambuddy project id."`
	Kind      string `json:"kind" jsonschema:"attachment or cover."`
	Filename  string `json:"filename,omitempty" jsonschema:"For an attachment: its stored name or the name it was uploaded with (get_project lists both)."`
	SaveTo    string `json:"save_to" jsonschema:"A file path, or an existing folder, on the machine running this server."`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"Replace an existing file. Off by default."`
}

type deleteFileArgs struct {
	ProjectID int    `json:"project_id" jsonschema:"Bambuddy project id."`
	Kind      string `json:"kind" jsonschema:"attachment or cover."`
	Filename  string `json:"filename,omitempty" jsonschema:"For an attachment: its stored name or the name it was uploaded with."`
	Confirm   bool   `json:"confirm" jsonschema:"Must be true. Set it only after the user has said yes to deleting this named file."`
}

type exportArgs struct {
	ProjectID int    `json:"project_id" jsonschema:"Bambuddy project id."`
	Format    string `json:"format,omitempty" jsonschema:"zip (default, includes the files) or json (settings and BOM only)."`
	SaveTo    string `json:"save_to" jsonschema:"A file path, or an existing folder, on the machine running this server."`
	Overwrite bool   `json:"overwrite,omitempty"`
}

type importArgs struct {
	Path string `json:"path" jsonschema:"A .zip or .json made by export_project, on the machine running this server."`
}

type fileResult struct {
	Done     bool   `json:"done"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Name     string `json:"name,omitempty"`
	SavedTo  string `json:"saved_to,omitempty"`
	SizeKB   int64  `json:"size_kb,omitempty"`
}

type projectAction struct {
	Done    bool   `json:"done"`
	Message string `json:"message"`
	Impact  string `json:"impact,omitempty"`
}

type bomResult struct {
	Done    bool         `json:"done"`
	Message string       `json:"message"`
	Item    *plan.BOMRow `json:"item,omitempty"`
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func projectPath(id int) string { return "/api/v1/projects/" + itoa(id) }

func getProject(ctx context.Context, d *Deps, id int) (api.Project, error) {
	var p api.Project
	if id <= 0 {
		return p, fmt.Errorf("project_id is required")
	}
	_, err := d.Bambuddy.Get(ctx, projectPath(id), nil, &p)
	return p, toolError(err)
}

// expandPath turns a leading ~/ into the home directory.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	}
	return p, nil
}

// tildePath writes a path under the home directory as ~/..., so results do not
// carry the account name.
func tildePath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func checkDateString(v string) error {
	if v == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02T15:04:05"} {
		if _, err := time.Parse(layout, v); err == nil {
			return nil
		}
	}
	return fmt.Errorf("due_date must be YYYY-MM-DD")
}

// projectBody validates the shared fields and returns the JSON body for them.
func projectBody(f projectFields) (map[string]any, error) {
	body := map[string]any{}
	if f.Description != nil {
		body["description"] = *f.Description
	}
	if f.Color != nil {
		c, ok := plan.NormalizeColor(*f.Color)
		if !ok {
			return nil, fmt.Errorf("color must be a six-digit hex colour such as FF69B4")
		}
		body["color"] = c
	}
	if f.TargetCount != nil {
		if *f.TargetCount < 0 {
			return nil, fmt.Errorf("target_count cannot be negative")
		}
		body["target_count"] = *f.TargetCount
	}
	if f.TargetPartsCount != nil {
		if *f.TargetPartsCount < 0 {
			return nil, fmt.Errorf("target_parts_count cannot be negative")
		}
		body["target_parts_count"] = *f.TargetPartsCount
	}
	if f.Notes != nil {
		body["notes"] = *f.Notes
	}
	if f.Tags != nil {
		var tags []string
		for _, t := range f.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
		body["tags"] = strings.Join(tags, ",")
	}
	if f.DueDate != nil {
		if err := checkDateString(*f.DueDate); err != nil {
			return nil, err
		}
		body["due_date"] = *f.DueDate
	}
	if f.Priority != nil {
		body["priority"] = strings.TrimSpace(*f.Priority)
	}
	if f.Budget != nil {
		if *f.Budget < 0 {
			return nil, fmt.Errorf("budget cannot be negative")
		}
		body["budget"] = *f.Budget
	}
	if f.ParentID != nil {
		body["parent_id"] = *f.ParentID
	}
	if f.URL != nil {
		body["url"] = *f.URL
	}
	return body, nil
}

// projectErr adds the values Bambuddy accepted when it rejects a status or
// priority, since its own message only says "Invalid status".
func projectErr(err error) error {
	err = toolError(err)
	switch {
	case strings.Contains(err.Error(), "Invalid status"):
		return fmt.Errorf("%w (status values seen to work: active, completed, archived)", err)
	case strings.Contains(err.Error(), "Invalid priority"):
		return fmt.Errorf("%w (priority values seen to work: low, normal, high, urgent)", err)
	}
	return err
}

func positiveIDs(name string, ids []int) ([]int, error) {
	seen := map[int]bool{}
	var out []int
	for _, id := range ids {
		if id <= 0 {
			return nil, fmt.Errorf("%s must be positive ids", name)
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

func bomBody(f bomFields) (map[string]any, error) {
	body := map[string]any{}
	if f.QuantityNeeded != nil {
		if *f.QuantityNeeded < 1 {
			return nil, fmt.Errorf("quantity_needed must be at least 1")
		}
		body["quantity_needed"] = *f.QuantityNeeded
	}
	if f.UnitPrice != nil {
		if *f.UnitPrice < 0 {
			return nil, fmt.Errorf("unit_price cannot be negative")
		}
		body["unit_price"] = *f.UnitPrice
	}
	if f.SourcingURL != nil {
		body["sourcing_url"] = *f.SourcingURL
	}
	if f.ArchiveID != nil {
		body["archive_id"] = *f.ArchiveID
	}
	if f.STLFilename != nil {
		body["stl_filename"] = *f.STLFilename
	}
	if f.Remarks != nil {
		body["remarks"] = *f.Remarks
	}
	return body, nil
}

// openUpload opens a local file for upload after checking it is a regular file
// of a sane size.
func openUpload(path string) (*os.File, string, error) {
	full, err := expandPath(path)
	if err != nil {
		return nil, "", err
	}
	fh, err := os.Open(full)
	if err != nil {
		return nil, "", fmt.Errorf("cannot open file: %w", err)
	}
	info, err := fh.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = fh.Close()
		return nil, "", fmt.Errorf("%s is not a regular file", filepath.Base(full))
	}
	if info.Size() > maxUploadSize {
		_ = fh.Close()
		return nil, "", fmt.Errorf("file is %d MB; the limit is %d MB", info.Size()>>20, maxUploadSize>>20)
	}
	return fh, filepath.Base(full), nil
}

// saveDownload fetches apiPath into dest. dest may be a file path or an
// existing folder, in which case the name comes from the server's
// Content-Disposition, or fallback. Nothing is overwritten unless asked, and
// the file appears only once the whole body has arrived.
func saveDownload(ctx context.Context, d *Deps, apiPath string, q url.Values, dest, fallback string, overwrite bool) (string, int64, error) {
	full, err := expandPath(dest)
	if err != nil {
		return "", 0, err
	}
	dir, name := filepath.Dir(full), filepath.Base(full)
	if fi, err := os.Stat(full); err == nil && fi.IsDir() {
		dir, name = full, ""
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", 0, fmt.Errorf("the folder %s does not exist", tildePath(dir))
	}
	tmp, err := os.CreateTemp(dir, ".printshop-*.part")
	if err != nil {
		return "", 0, fmt.Errorf("cannot write in %s: %w", tildePath(dir), err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after a rename; cleans up after a failure

	hdr, n, err := d.Bambuddy.Download(ctx, apiPath, q, tmp, maxUploadSize)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", 0, toolError(err)
	}
	if name == "" {
		name = fallback
		if _, params, perr := mime.ParseMediaType(hdr.Get("Content-Disposition")); perr == nil && params["filename"] != "" {
			name = params["filename"]
		}
		name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
		if name == "." || name == "/" || name == "" {
			name = fallback
		}
	}
	final := filepath.Join(dir, name)
	if _, err := os.Stat(final); err == nil && !overwrite {
		return "", 0, fmt.Errorf("%s already exists; pass overwrite: true to replace it", tildePath(final))
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", 0, err
	}
	return tildePath(final), n, nil
}

func kindOf(k string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "attachment":
		return "attachment", nil
	case "cover":
		return "cover", nil
	}
	return "", fmt.Errorf("kind must be attachment or cover")
}

// ─── tools ───────────────────────────────────────────────────────────────────

func addProjectTools(srv *mcp.Server, d *Deps) {
	addProjectReads(srv, d)
	addProjectWrites(srv, d)
	addProjectFiles(srv, d)
}

func addProjectReads(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_projects",
		Description: "List Bambuddy projects: name, status, priority, colour, tags, due date, how many prints and queue items are filed under each, and progress. Use get_project for one in full.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a listProjectsArgs) (*mcp.CallToolResult, projectsResult, error) {
		q := url.Values{}
		if a.Status != "" {
			q.Set("status", a.Status)
		}
		var ps []api.Project
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/projects/", q, &ps); err != nil {
			return nil, projectsResult{}, toolError(err)
		}
		res := projectsResult{Projects: make([]plan.ProjectRow, 0, len(ps))}
		for _, p := range ps {
			res.Projects = append(res.Projects, plan.ProjectRowOf(p))
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_project_templates",
		Description: "List project templates. Use the id with create_project_from_template.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, projectsResult, error) {
		var ps []api.Project
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/projects/templates", nil, &ps); err != nil {
			return nil, projectsResult{}, toolError(err)
		}
		res := projectsResult{Projects: make([]plan.ProjectRow, 0, len(ps))}
		for _, p := range ps {
			res.Projects = append(res.Projects, plan.ProjectRowOf(p))
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_project",
		Description: "One project in full: settings, notes, parent and child projects, attachments (stored name and original name), cover image, and a roll-up of its prints, filament, time, cost and BOM. " +
			"Set include_archives, include_queue, include_bom or include_timeline to add those lists.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a getProjectArgs) (*mcp.CallToolResult, projectView, error) {
		p, err := getProject(ctx, d, a.ProjectID)
		if err != nil {
			return nil, projectView{}, err
		}
		v := projectView{ProjectDetail: plan.ProjectDetailOf(p)}
		limit := a.Limit
		if limit <= 0 {
			limit = 20
		}
		limit = min(limit, 200)
		q := url.Values{"limit": {itoa(limit)}}
		if a.IncludeArchives {
			var as []api.Archive
			if _, err := d.Bambuddy.Get(ctx, projectPath(a.ProjectID)+"/archives", q, &as); err != nil {
				return nil, v, toolError(err)
			}
			for _, x := range as {
				v.ArchiveList = append(v.ArchiveList, plan.ArchiveRowOf(x, false))
			}
		}
		if a.IncludeQueue {
			var qs []api.QueueItem
			if _, err := d.Bambuddy.Get(ctx, projectPath(a.ProjectID)+"/queue", nil, &qs); err != nil {
				return nil, v, toolError(err)
			}
			for _, x := range qs {
				v.QueueList = append(v.QueueList, queueRow(x))
			}
		}
		if a.IncludeBOM {
			var bs []api.BOMItem
			if _, err := d.Bambuddy.Get(ctx, projectPath(a.ProjectID)+"/bom", nil, &bs); err != nil {
				return nil, v, toolError(err)
			}
			for _, x := range bs {
				v.BOM = append(v.BOM, plan.BOMRowOf(x))
			}
		}
		if a.IncludeTimeline {
			var ev []api.TimelineEvent
			if _, err := d.Bambuddy.Get(ctx, projectPath(a.ProjectID)+"/timeline", q, &ev); err != nil {
				return nil, v, toolError(err)
			}
			v.Timeline = plan.TimelineRows(ev)
		}
		return nil, v, nil
	})
}

func addProjectWrites(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_project",
		Description: "Create a Bambuddy project to group prints, a bill of materials, notes and files, for example one per build such as PixelBar. " +
			"Only name is required. Then file prints under it with project_add_items or print_file project_id.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a createProjectArgs) (*mcp.CallToolResult, plan.ProjectDetail, error) {
		name := strings.TrimSpace(a.Name)
		if name == "" {
			return nil, plan.ProjectDetail{}, fmt.Errorf("name is required")
		}
		body, err := projectBody(a.projectFields)
		if err != nil {
			return nil, plan.ProjectDetail{}, err
		}
		body["name"] = name
		var p api.Project
		if err := d.Bambuddy.Send(ctx, "POST", "/api/v1/projects/", body, &p); err != nil {
			return nil, plan.ProjectDetail{}, projectErr(err)
		}
		return nil, plan.ProjectDetailOf(p), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_project_from_template",
		Description: "Create a project from a template (list_project_templates), with a name of your choosing.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a fromTemplateArgs) (*mcp.CallToolResult, plan.ProjectDetail, error) {
		if a.TemplateID <= 0 || strings.TrimSpace(a.Name) == "" {
			return nil, plan.ProjectDetail{}, fmt.Errorf("template_id and name are required")
		}
		var p api.Project
		q := url.Values{"name": {strings.TrimSpace(a.Name)}}
		if err := d.Bambuddy.SendQuery(ctx, "POST", "/api/v1/projects/from-template/"+itoa(a.TemplateID), q, nil, &p); err != nil {
			return nil, plan.ProjectDetail{}, toolError(err)
		}
		return nil, plan.ProjectDetailOf(p), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "save_project_as_template",
		Description: "Save a project's settings as a reusable template. The project itself is unchanged.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a projectIDArgs) (*mcp.CallToolResult, plan.ProjectRow, error) {
		var p api.Project
		if a.ProjectID <= 0 {
			return nil, plan.ProjectRow{}, fmt.Errorf("project_id is required")
		}
		if err := d.Bambuddy.Send(ctx, "POST", projectPath(a.ProjectID)+"/create-template", nil, &p); err != nil {
			return nil, plan.ProjectRow{}, toolError(err)
		}
		return nil, plan.ProjectRowOf(p), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_project",
		Description: "Change a project's name, status, priority, colour, description, notes, tags, due date, targets, budget, parent or link. Only the fields you pass change. " +
			"To finish a project set status to completed. Bambuddy rejects status and priority values it does not know and says so.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a updateProjectArgs) (*mcp.CallToolResult, plan.ProjectDetail, error) {
		body, err := projectBody(a.projectFields)
		if err != nil {
			return nil, plan.ProjectDetail{}, err
		}
		if a.Name != nil {
			if strings.TrimSpace(*a.Name) == "" {
				return nil, plan.ProjectDetail{}, fmt.Errorf("name cannot be empty")
			}
			body["name"] = strings.TrimSpace(*a.Name)
		}
		if a.Status != nil {
			body["status"] = strings.TrimSpace(*a.Status)
		}
		if a.ProjectID <= 0 {
			return nil, plan.ProjectDetail{}, fmt.Errorf("project_id is required")
		}
		if len(body) == 0 {
			return nil, plan.ProjectDetail{}, fmt.Errorf("nothing to change")
		}
		var p api.Project
		if err := d.Bambuddy.Send(ctx, "PATCH", projectPath(a.ProjectID), body, &p); err != nil {
			return nil, plan.ProjectDetail{}, projectErr(err)
		}
		return nil, plan.ProjectDetailOf(p), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "delete_project",
		Description: "DESTRUCTIVE: delete a project with its bill of materials, attachments, cover image and settings. The prints and queue items filed under it are kept and only lose their project. " +
			"Claude must ask the user first, naming the project, and get a clear yes before calling this. Never call it on your own judgment. " +
			"Without confirm: true it deletes nothing and returns what would be removed.",
		Annotations: destructive(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a deleteProjectArgs) (*mcp.CallToolResult, projectAction, error) {
		p, err := getProject(ctx, d, a.ProjectID)
		if err != nil {
			return nil, projectAction{}, err
		}
		var bom []api.BOMItem
		if _, err := d.Bambuddy.Get(ctx, projectPath(a.ProjectID)+"/bom", nil, &bom); err != nil {
			return nil, projectAction{}, toolError(err)
		}
		impact := plan.DeleteImpact(plan.ProjectDetailOf(p), len(bom))
		if !a.Confirm {
			return nil, projectAction{Message: "not deleted: ask the user, then call again with confirm: true", Impact: impact}, nil
		}
		if err := d.Bambuddy.Send(ctx, "DELETE", projectPath(a.ProjectID), nil, nil); err != nil {
			return nil, projectAction{}, toolError(err)
		}
		return nil, projectAction{Done: true, Message: fmt.Sprintf("deleted project #%d %q", p.ID, p.Name), Impact: impact}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "project_add_items",
		Description: "File finished prints (archive_ids) and queue items (queue_item_ids) under a project. Archives can be taken out again with project_remove_archives. " +
			"There is no matching removal for queue items in Bambuddy's API, so adding one cannot be undone from here: be sure of the project first.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a addItemsArgs) (*mcp.CallToolResult, projectAction, error) {
		arch, err := positiveIDs("archive_ids", a.ArchiveIDs)
		if err != nil {
			return nil, projectAction{}, err
		}
		qs, err := positiveIDs("queue_item_ids", a.QueueItemIDs)
		if err != nil {
			return nil, projectAction{}, err
		}
		if a.ProjectID <= 0 || len(arch)+len(qs) == 0 {
			return nil, projectAction{}, fmt.Errorf("give project_id and at least one archive_ids or queue_item_ids")
		}
		if _, err := getProject(ctx, d, a.ProjectID); err != nil {
			return nil, projectAction{}, err
		}
		var parts []string
		if len(arch) > 0 {
			if err := d.Bambuddy.Send(ctx, "POST", projectPath(a.ProjectID)+"/add-archives", map[string][]int{"archive_ids": arch}, nil); err != nil {
				return nil, projectAction{}, toolError(err)
			}
			parts = append(parts, fmt.Sprintf("%d archive(s)", len(arch)))
		}
		if len(qs) > 0 {
			if err := d.Bambuddy.Send(ctx, "POST", projectPath(a.ProjectID)+"/add-queue", map[string][]int{"queue_item_ids": qs}, nil); err != nil {
				return nil, projectAction{}, fmt.Errorf("%s were added but the queue items failed: %w", strings.Join(parts, " and "), toolError(err))
			}
			parts = append(parts, fmt.Sprintf("%d queue item(s)", len(qs)))
		}
		return nil, projectAction{Done: true, Message: fmt.Sprintf("added %s to project #%d", strings.Join(parts, " and "), a.ProjectID)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "project_remove_archives",
		Description: "Take archives out of a project. The prints themselves are kept; they just stop being filed under it.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a removeArchivesArgs) (*mcp.CallToolResult, projectAction, error) {
		ids, err := positiveIDs("archive_ids", a.ArchiveIDs)
		if err != nil || a.ProjectID <= 0 || len(ids) == 0 {
			return nil, projectAction{}, fmt.Errorf("give project_id and at least one archive id")
		}
		if err := d.Bambuddy.Send(ctx, "POST", projectPath(a.ProjectID)+"/remove-archives", map[string][]int{"archive_ids": ids}, nil); err != nil {
			return nil, projectAction{}, toolError(err)
		}
		return nil, projectAction{Done: true, Message: fmt.Sprintf("removed %d archive(s) from project #%d; the prints are kept", len(ids), a.ProjectID)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "bom_add_item",
		Description: "Add a line to a project's bill of materials: a part, how many are needed, and optionally a price, a source link, the print that makes it, and remarks.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a bomAddArgs) (*mcp.CallToolResult, bomResult, error) {
		if strings.TrimSpace(a.Name) == "" || a.ProjectID <= 0 {
			return nil, bomResult{}, fmt.Errorf("project_id and name are required")
		}
		body, err := bomBody(a.bomFields)
		if err != nil {
			return nil, bomResult{}, err
		}
		body["name"] = strings.TrimSpace(a.Name)
		var it api.BOMItem
		if err := d.Bambuddy.Send(ctx, "POST", projectPath(a.ProjectID)+"/bom", body, &it); err != nil {
			return nil, bomResult{}, toolError(err)
		}
		row := plan.BOMRowOf(it)
		return nil, bomResult{Done: true, Message: fmt.Sprintf("added BOM line #%d", it.ID), Item: &row}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "bom_update_item",
		Description: "Change a bill-of-materials line: name, quantities, price, link, linked print or remarks. Set quantity_acquired as parts arrive; reaching quantity_needed marks the line complete.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a bomUpdateArgs) (*mcp.CallToolResult, bomResult, error) {
		body, err := bomBody(a.bomFields)
		if err != nil {
			return nil, bomResult{}, err
		}
		if a.Name != nil {
			if strings.TrimSpace(*a.Name) == "" {
				return nil, bomResult{}, fmt.Errorf("name cannot be empty")
			}
			body["name"] = strings.TrimSpace(*a.Name)
		}
		if a.QuantityAcquired != nil {
			if *a.QuantityAcquired < 0 {
				return nil, bomResult{}, fmt.Errorf("quantity_acquired cannot be negative")
			}
			body["quantity_acquired"] = *a.QuantityAcquired
		}
		if a.ProjectID <= 0 || a.ItemID <= 0 || len(body) == 0 {
			return nil, bomResult{}, fmt.Errorf("give project_id, item_id and at least one field to change")
		}
		var it api.BOMItem
		if err := d.Bambuddy.Send(ctx, "PATCH", projectPath(a.ProjectID)+"/bom/"+itoa(a.ItemID), body, &it); err != nil {
			return nil, bomResult{}, toolError(err)
		}
		row := plan.BOMRowOf(it)
		return nil, bomResult{Done: true, Message: fmt.Sprintf("updated BOM line #%d", it.ID), Item: &row}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "bom_delete_item",
		Description: "DESTRUCTIVE: remove a line from a project's bill of materials. " +
			"Claude must ask the user first, naming the part, and get a clear yes before calling this. Pass confirm: true only after that yes.",
		Annotations: destructive(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a bomDeleteArgs) (*mcp.CallToolResult, bomResult, error) {
		if a.ProjectID <= 0 || a.ItemID <= 0 {
			return nil, bomResult{}, fmt.Errorf("project_id and item_id are required")
		}
		var bom []api.BOMItem
		if _, err := d.Bambuddy.Get(ctx, projectPath(a.ProjectID)+"/bom", nil, &bom); err != nil {
			return nil, bomResult{}, toolError(err)
		}
		var row *plan.BOMRow
		for _, b := range bom {
			if b.ID == a.ItemID {
				r := plan.BOMRowOf(b)
				row = &r
			}
		}
		if row == nil {
			return nil, bomResult{}, fmt.Errorf("project #%d has no BOM line #%d", a.ProjectID, a.ItemID)
		}
		if !a.Confirm {
			return nil, bomResult{Item: row, Message: fmt.Sprintf("not removed: ask the user about %q, then call again with confirm: true", row.Name)}, nil
		}
		if err := d.Bambuddy.Send(ctx, "DELETE", projectPath(a.ProjectID)+"/bom/"+itoa(a.ItemID), nil, nil); err != nil {
			return nil, bomResult{}, toolError(err)
		}
		return nil, bomResult{Done: true, Item: row, Message: fmt.Sprintf("removed BOM line #%d %q", row.ID, row.Name)}, nil
	})
}

func addProjectFiles(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "project_upload_file",
		Description: "Attach a local file to a project (kind attachment), or set its cover image (kind cover; png, jpg, jpeg, webp or gif). The path is on the machine running this server. " +
			"A cover replaces the existing one and Bambuddy deletes the old image file, so with a cover already set this needs replace: true, which Claude should ask the user about first.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a uploadFileArgs) (*mcp.CallToolResult, fileResult, error) {
		kind, err := kindOf(a.Kind)
		if err != nil {
			return nil, fileResult{}, err
		}
		p, err := getProject(ctx, d, a.ProjectID)
		if err != nil {
			return nil, fileResult{}, err
		}
		hadCover := p.CoverImage != nil && *p.CoverImage != ""
		if kind == "cover" {
			if !coverExts[strings.ToLower(filepath.Ext(a.Path))] {
				return nil, fileResult{}, fmt.Errorf("a cover must be a png, jpg, jpeg, webp or gif image")
			}
			if hadCover && !a.Replace {
				return nil, fileResult{Message: fmt.Sprintf("not uploaded: the project already has a cover (%s) and replacing it deletes that file; ask the user, then pass replace: true", *p.CoverImage)}, nil
			}
		}
		fh, name, err := openUpload(a.Path)
		if err != nil {
			return nil, fileResult{}, err
		}
		defer func() { _ = fh.Close() }()
		route := "/attachments"
		if kind == "cover" {
			route = "/cover-image"
		}
		var out struct {
			Filename     string `json:"filename"`
			OriginalName string `json:"original_name"`
			Size         int64  `json:"size"`
		}
		if err := d.Bambuddy.Upload(ctx, projectPath(a.ProjectID)+route, name, fh, &out); err != nil {
			return nil, fileResult{}, toolError(err)
		}
		msg := fmt.Sprintf("uploaded %s as %s for project #%d", name, kind, a.ProjectID)
		if kind == "cover" && hadCover {
			msg += "; the previous cover image was replaced"
		}
		return nil, fileResult{Done: true, Message: msg, Filename: out.Filename, Name: name, SizeKB: out.Size / 1024}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "project_download_file",
		Description: "Save a project's attachment (by stored name or the name it was uploaded with) or its cover image to a path on the machine running this server. " +
			"It will not overwrite an existing file unless overwrite is true, and the file only appears once the whole download has arrived.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a downloadFileArgs) (*mcp.CallToolResult, fileResult, error) {
		kind, err := kindOf(a.Kind)
		if err != nil {
			return nil, fileResult{}, err
		}
		if strings.TrimSpace(a.SaveTo) == "" {
			return nil, fileResult{}, fmt.Errorf("save_to is required")
		}
		p, err := getProject(ctx, d, a.ProjectID)
		if err != nil {
			return nil, fileResult{}, err
		}
		route, fallback, name := "/cover-image", "", ""
		if kind == "cover" {
			if p.CoverImage == nil || *p.CoverImage == "" {
				return nil, fileResult{}, fmt.Errorf("project #%d has no cover image", a.ProjectID)
			}
			fallback = *p.CoverImage
		} else {
			att, err := plan.ResolveAttachment(p.Attachments, a.Filename)
			if err != nil {
				return nil, fileResult{}, err
			}
			route, name = "/attachments/"+url.PathEscape(att.Filename), att.Filename
			fallback = att.OriginalName
			if fallback == "" {
				fallback = att.Filename
			}
		}
		saved, n, err := saveDownload(ctx, d, projectPath(a.ProjectID)+route, nil, a.SaveTo, fallback, a.Overwrite)
		if err != nil {
			return nil, fileResult{}, err
		}
		return nil, fileResult{Done: true, Message: "saved " + saved, Filename: name, SavedTo: saved, SizeKB: n / 1024}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "project_delete_file",
		Description: "DESTRUCTIVE: delete a project's attachment or cover image; the file is gone from Bambuddy. " +
			"Claude must ask the user first, naming the file, and get a clear yes before calling this. Pass confirm: true only after that yes. " +
			"Without confirm it deletes nothing and says which file it would remove.",
		Annotations: destructive(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a deleteFileArgs) (*mcp.CallToolResult, fileResult, error) {
		kind, err := kindOf(a.Kind)
		if err != nil {
			return nil, fileResult{}, err
		}
		p, err := getProject(ctx, d, a.ProjectID)
		if err != nil {
			return nil, fileResult{}, err
		}
		route, stored, shown := "/cover-image", "", "the cover image"
		if kind == "cover" {
			if p.CoverImage == nil || *p.CoverImage == "" {
				return nil, fileResult{}, fmt.Errorf("project #%d has no cover image", a.ProjectID)
			}
			stored = *p.CoverImage
		} else {
			att, err := plan.ResolveAttachment(p.Attachments, a.Filename)
			if err != nil {
				return nil, fileResult{}, err
			}
			route, stored = "/attachments/"+url.PathEscape(att.Filename), att.Filename
			shown = fmt.Sprintf("attachment %q (%s)", att.OriginalName, att.Filename)
		}
		if !a.Confirm {
			return nil, fileResult{Filename: stored, Message: fmt.Sprintf("not deleted: ask the user about %s, then call again with confirm: true", shown)}, nil
		}
		if err := d.Bambuddy.Send(ctx, "DELETE", projectPath(a.ProjectID)+route, nil, nil); err != nil {
			return nil, fileResult{}, toolError(err)
		}
		return nil, fileResult{Done: true, Filename: stored, Message: "deleted " + shown}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "export_project",
		Description: "Export a project to a file on the machine running this server: zip (default, with its files) or json (settings and BOM only). " +
			"It will not overwrite an existing file unless overwrite is true. Import it again with import_project.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a exportArgs) (*mcp.CallToolResult, fileResult, error) {
		format := strings.ToLower(strings.TrimSpace(a.Format))
		if format == "" {
			format = "zip"
		}
		if format != "zip" && format != "json" {
			return nil, fileResult{}, fmt.Errorf("format must be zip or json")
		}
		if a.ProjectID <= 0 || strings.TrimSpace(a.SaveTo) == "" {
			return nil, fileResult{}, fmt.Errorf("project_id and save_to are required")
		}
		saved, n, err := saveDownload(ctx, d, projectPath(a.ProjectID)+"/export", url.Values{"format": {format}}, a.SaveTo, fmt.Sprintf("project-%d.%s", a.ProjectID, format), a.Overwrite)
		if err != nil {
			return nil, fileResult{}, err
		}
		return nil, fileResult{Done: true, Message: "exported to " + saved, SavedTo: saved, SizeKB: n / 1024}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "import_project",
		Description: "Create a project from a .zip or .json file made by export_project, on the machine running this server. It always creates a new project; nothing existing is changed.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a importArgs) (*mcp.CallToolResult, plan.ProjectDetail, error) {
		if ext := strings.ToLower(filepath.Ext(a.Path)); ext != ".zip" && ext != ".json" {
			return nil, plan.ProjectDetail{}, fmt.Errorf("import takes a .zip or .json file")
		}
		fh, name, err := openUpload(a.Path)
		if err != nil {
			return nil, plan.ProjectDetail{}, err
		}
		defer func() { _ = fh.Close() }()
		var p api.Project
		if err := d.Bambuddy.Upload(ctx, "/api/v1/projects/import/file", name, fh, &p); err != nil {
			return nil, plan.ProjectDetail{}, toolError(err)
		}
		return nil, plan.ProjectDetailOf(p), nil
	})
}
