// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"fmt"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// NormalizeColor returns a project colour as #RRGGBB, or false if it is not a
// colour. Bambuddy stores whatever string it is given, so this checks first.
func NormalizeColor(s string) (string, bool) {
	h := NormHex(s)
	if h == "" || len(strings.TrimPrefix(strings.TrimSpace(s), "#")) != 6 {
		return "", false
	}
	return "#" + h, true
}

// shortDate drops a midnight time, which is how Bambuddy returns a plain due date.
func shortDate(s string) string { return strings.TrimSuffix(s, "T00:00:00") }

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

// ProjectRow is a project in a list.
type ProjectRow struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	Color       string   `json:"color,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        string   `json:"tags,omitempty"`
	DueDate     string   `json:"due_date,omitempty"`
	URL         string   `json:"url,omitempty"`
	Budget      *float64 `json:"budget,omitempty"`
	TargetCount *int     `json:"target_count,omitempty"`
	TargetParts *int     `json:"target_parts_count,omitempty"`
	ProgressPct *float64 `json:"progress_pct,omitempty"`
	Archives    int      `json:"archives"`
	QueueItems  int      `json:"queue_items"`
	Completed   int      `json:"completed"`
	Failed      int      `json:"failed"`
	HasCover    bool     `json:"has_cover"`
	IsTemplate  bool     `json:"is_template,omitempty"`
}

// ProjectRowOf trims a list entry.
func ProjectRowOf(p api.Project) ProjectRow {
	return ProjectRow{
		ID: p.ID, Name: p.Name, Status: p.Status, Priority: p.Priority, Color: deref(p.Color),
		Description: clip(deref(p.Description), 200), Tags: deref(p.Tags), DueDate: shortDate(deref(p.DueDate)), URL: deref(p.URL),
		Budget: p.Budget, TargetCount: p.TargetCount, TargetParts: p.TargetPartsCount, ProgressPct: roundPtr(p.ProgressPercent),
		Archives: p.ArchiveCount, QueueItems: p.QueueCount, Completed: p.CompletedCount, Failed: p.FailedCount,
		HasCover: p.CoverImage != nil && *p.CoverImage != "", IsTemplate: p.IsTemplate,
	}
}

// AttachmentRow is a project attachment. Filename is the stored name that the
// download and delete tools take; Name is what it was called when uploaded.
type AttachmentRow struct {
	Filename string `json:"filename"`
	Name     string `json:"name"`
	SizeKB   int64  `json:"size_kb"`
	Uploaded string `json:"uploaded,omitempty"`
}

// StatsView is a project's roll-up.
type StatsView struct {
	Archives        int      `json:"archives"`
	Completed       int      `json:"completed_prints"`
	Failed          int      `json:"failed_prints"`
	Queued          int      `json:"queued_prints"`
	InProgress      int      `json:"in_progress_prints"`
	PrintHours      float64  `json:"print_hours"`
	FilamentG       float64  `json:"filament_g"`
	ProgressPct     *float64 `json:"progress_pct,omitempty"`
	PartsProgress   *float64 `json:"parts_progress_pct,omitempty"`
	RemainingPrints *int     `json:"remaining_prints,omitempty"`
	RemainingParts  *int     `json:"remaining_parts,omitempty"`
	EstimatedCost   float64  `json:"estimated_cost"`
	BOMItems        int      `json:"bom_items"`
	BOMDone         int      `json:"bom_items_complete"`
	BOMCost         float64  `json:"bom_cost"`
}

// ProjectDetail is one project in full.
type ProjectDetail struct {
	ProjectRow
	Notes       string           `json:"notes,omitempty"`
	Parent      *api.ProjectRef  `json:"parent,omitempty"`
	Children    []api.ProjectRef `json:"children,omitempty"`
	Attachments []AttachmentRow  `json:"attachments"`
	Cover       string           `json:"cover_image,omitempty"`
	Stats       *StatsView       `json:"stats,omitempty"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
}

// ProjectDetailOf trims a project. Description is not clipped here.
func ProjectDetailOf(p api.Project) ProjectDetail {
	d := ProjectDetail{
		ProjectRow: ProjectRowOf(p), Notes: deref(p.Notes), Children: p.Children, Cover: deref(p.CoverImage),
		Attachments: []AttachmentRow{}, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	d.Description = deref(p.Description)
	if p.ParentID != nil {
		d.Parent = &api.ProjectRef{ID: *p.ParentID, Name: deref(p.ParentName)}
	}
	for _, a := range p.Attachments {
		d.Attachments = append(d.Attachments, AttachmentRow{Filename: a.Filename, Name: a.OriginalName, SizeKB: a.Size / 1024, Uploaded: a.UploadedAt})
	}
	if s := p.Stats; s != nil {
		d.Stats = &StatsView{
			Archives: s.TotalArchives, Completed: s.CompletedPrints, Failed: s.FailedPrints, Queued: s.QueuedPrints, InProgress: s.InProgressPrints,
			PrintHours: round1(s.PrintTimeHours), FilamentG: round1(s.FilamentGrams), ProgressPct: roundPtr(s.ProgressPercent),
			PartsProgress: roundPtr(s.PartsProgress), RemainingPrints: s.RemainingPrints, RemainingParts: s.RemainingParts,
			EstimatedCost: round1(s.EstimatedCost), BOMItems: s.BOMTotal, BOMDone: s.BOMCompleted, BOMCost: round1(s.BOMCost),
		}
		// The detail endpoint carries the totals the list gets from counts.
		d.Archives, d.Completed, d.Failed = s.TotalArchives, s.CompletedPrints, s.FailedPrints
		d.QueueItems, d.ProgressPct = s.QueuedPrints+s.InProgressPrints, roundPtr(s.ProgressPercent)
	}
	return d
}

// BOMRow is a bill-of-materials line.
type BOMRow struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Needed      int      `json:"needed"`
	Acquired    int      `json:"acquired"`
	Complete    bool     `json:"complete"`
	UnitPrice   *float64 `json:"unit_price,omitempty"`
	SourcingURL string   `json:"sourcing_url,omitempty"`
	Archive     string   `json:"archive,omitempty"`
	ArchiveID   *int     `json:"archive_id,omitempty"`
	STL         string   `json:"stl_filename,omitempty"`
	Remarks     string   `json:"remarks,omitempty"`
}

// BOMRowOf trims a BOM item.
func BOMRowOf(b api.BOMItem) BOMRow {
	return BOMRow{
		ID: b.ID, Name: b.Name, Needed: b.QuantityNeeded, Acquired: b.QuantityAcquired, Complete: b.IsComplete,
		UnitPrice: b.UnitPrice, SourcingURL: deref(b.SourcingURL), Archive: deref(b.ArchiveName), ArchiveID: b.ArchiveID,
		STL: deref(b.STLFilename), Remarks: deref(b.Remarks),
	}
}

// TimelineRow is one project event.
type TimelineRow struct {
	When        string         `json:"when"`
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
}

// TimelineRows trims events.
func TimelineRows(in []api.TimelineEvent) []TimelineRow {
	out := make([]TimelineRow, 0, len(in))
	for _, e := range in {
		out = append(out, TimelineRow{When: e.Timestamp, Type: e.EventType, Title: e.Title, Description: deref(e.Description), Detail: e.Metadata})
	}
	return out
}

// ResolveAttachment finds an attachment by its stored name, or else by the
// name it was uploaded with. An original name shared by several attachments is
// ambiguous and is refused, listing the stored names.
func ResolveAttachment(list []api.Attachment, name string) (api.Attachment, error) {
	name = strings.TrimSpace(name)
	for _, a := range list {
		if a.Filename == name {
			return a, nil
		}
	}
	var hits []api.Attachment
	for _, a := range list {
		if strings.EqualFold(a.OriginalName, name) {
			hits = append(hits, a)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return api.Attachment{}, fmt.Errorf("no attachment called %q on this project (get_project lists them)", name)
	}
	stored := make([]string, len(hits))
	for i, h := range hits {
		stored[i] = h.Filename
	}
	return api.Attachment{}, fmt.Errorf("%d attachments are called %q; use one of the stored names: %s", len(hits), name, strings.Join(stored, ", "))
}

// DeleteImpact says what deleting a project removes and what it leaves alone.
// Bambuddy keeps the archives and queue items and only clears their project.
func DeleteImpact(d ProjectDetail, bomItems int) string {
	cover := ""
	if d.Cover != "" {
		cover = ", its cover image"
	}
	return fmt.Sprintf("project %q: its %d archive(s) and %d queue item(s) stay but lose their project; its %d BOM item(s), %d attachment(s)%s and its settings are removed",
		d.Name, d.Archives, d.QueueItems, bomItems, len(d.Attachments), cover)
}
