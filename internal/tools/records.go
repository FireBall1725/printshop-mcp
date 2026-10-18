// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type assignArgs struct {
	SpoolID   int `json:"spool_id" jsonschema:"Spoolman spool id."`
	Slot      int `json:"slot" jsonschema:"AMS slot number as shown by printer_status (AMS unit 0 tray 1 is slot 1)."`
	PrinterID int `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
}

type assignResult struct {
	Assigned bool     `json:"assigned"`
	Message  string   `json:"message"`
	Warnings []string `json:"warnings,omitempty"`
}

type unassignArgs struct {
	SpoolID int `json:"spool_id" jsonschema:"Spoolman spool id whose AMS slot link to remove."`
}

type listArchivesArgs struct {
	PrinterID int    `json:"printer_id,omitempty" jsonschema:"Only this printer."`
	DateFrom  string `json:"date_from,omitempty" jsonschema:"YYYY-MM-DD."`
	DateTo    string `json:"date_to,omitempty" jsonschema:"YYYY-MM-DD."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum records, newest first. Defaults to 20, at most 200."`
	Offset    int    `json:"offset,omitempty"`
}

type archivesResult struct {
	Archives []plan.ArchiveRow `json:"archives"`
}

type archiveIDArgs struct {
	ArchiveID int `json:"archive_id" jsonschema:"Archive id from list_archives."`
}

type statsArgs struct {
	DateFrom string `json:"date_from,omitempty" jsonschema:"YYYY-MM-DD."`
	DateTo   string `json:"date_to,omitempty" jsonschema:"YYYY-MM-DD."`
}

type maintArgs struct {
	PrinterID int  `json:"printer_id,omitempty" jsonschema:"Only this printer. Default: all."`
	OnlyDue   bool `json:"only_due,omitempty" jsonschema:"Only tasks that are due or in warning."`
}

type maintResult struct {
	Printers []plan.MaintPrinter `json:"printers"`
}

type historyAMSArgs struct {
	PrinterID int `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
	AMSID     int `json:"ams_id" jsonschema:"AMS unit id. 0 is the first unit."`
	Hours     int `json:"hours,omitempty" jsonschema:"Window in hours. Defaults to 24, at most 168."`
}

func checkDate(name, v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return fmt.Errorf("%s must be YYYY-MM-DD", name)
	}
	return nil
}

func addRecordTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "assign_spool_to_slot",
		Description: "Link a Spoolman spool to an AMS slot so Bambuddy tracks usage against it. Use it to fix a tray ams_vs_stock reports as unlinked. " +
			"The link is stored in Bambuddy only; Spoolman's location field is not touched. Mismatches (material, colour, slot or spool already linked elsewhere) come back as warnings, " +
			"so read them and tell the user: a wrong link sends usage to the wrong spool. Undo with unassign_spool.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a assignArgs) (*mcp.CallToolResult, assignResult, error) {
		pid := printerID(a.PrinterID)
		amsID, trayID, ok := plan.PairOf(a.Slot)
		if !ok {
			return nil, assignResult{}, fmt.Errorf("slot %d is not an AMS slot", a.Slot)
		}
		var sp api.Spool
		if _, err := d.Spoolman.Get(ctx, "/api/v1/spool/"+itoa(a.SpoolID), nil, &sp); err != nil {
			return nil, assignResult{}, toolError(err)
		}
		st, err := statusFor(ctx, d, pid)
		if err != nil {
			return nil, assignResult{}, err
		}
		assigns, err := slotAssignments(ctx, d)
		if err != nil {
			return nil, assignResult{}, err
		}
		refusals, warnings := plan.CheckAssign(st, pid, a.Slot, plan.ViewOf(sp), assigns)
		if len(refusals) > 0 {
			return nil, assignResult{Message: "not assigned: " + join(refusals), Warnings: warnings}, nil
		}
		body := map[string]int{"spoolman_spool_id": a.SpoolID, "printer_id": pid, "ams_id": amsID, "tray_id": trayID}
		var out map[string]any
		if err := d.Bambuddy.Send(ctx, "POST", "/api/v1/spoolman/inventory/slot-assignments", body, &out); err != nil {
			return nil, assignResult{}, toolError(err)
		}
		return nil, assignResult{Assigned: true, Message: fmt.Sprintf("spool #%d linked to AMS slot %d", a.SpoolID, a.Slot), Warnings: warnings}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "unassign_spool",
		Description: "Remove a spool's AMS slot link in Bambuddy. The spool, its grams and its Spoolman record are untouched; only the local slot link goes.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a unassignArgs) (*mcp.CallToolResult, actionResult, error) {
		var out map[string]any
		if err := d.Bambuddy.Send(ctx, "DELETE", "/api/v1/spoolman/inventory/slot-assignments/"+itoa(a.SpoolID), nil, &out); err != nil {
			return nil, actionResult{}, toolError(err)
		}
		return nil, actionResult{Done: true, Message: fmt.Sprintf("spool #%d unlinked from its AMS slot", a.SpoolID)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_archives",
		Description: "Records of prints Bambuddy has run, newest first: status, planned and actual minutes, grams used, filament, failure reason. " +
			"Use it to see how a finished job went; print_history is the lighter queue view.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a listArchivesArgs) (*mcp.CallToolResult, archivesResult, error) {
		if err := checkDate("date_from", a.DateFrom); err != nil {
			return nil, archivesResult{}, err
		}
		if err := checkDate("date_to", a.DateTo); err != nil {
			return nil, archivesResult{}, err
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 20
		}
		limit = min(limit, 200)
		q := url.Values{"limit": {itoa(limit)}, "offset": {itoa(max(a.Offset, 0))}}
		if a.PrinterID > 0 {
			q.Set("printer_id", itoa(a.PrinterID))
		}
		if a.DateFrom != "" {
			q.Set("date_from", a.DateFrom)
		}
		if a.DateTo != "" {
			q.Set("date_to", a.DateTo)
		}
		var rows []api.Archive
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/archives/", q, &rows); err != nil {
			return nil, archivesResult{}, toolError(err)
		}
		res := archivesResult{Archives: make([]plan.ArchiveRow, 0, len(rows))}
		for _, r := range rows {
			res.Archives = append(res.Archives, plan.ArchiveRowOf(r, false))
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_archive",
		Description: "One print record in full, including grams per filament slot and any notes.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a archiveIDArgs) (*mcp.CallToolResult, plan.ArchiveRow, error) {
		var r api.Archive
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/archives/"+itoa(a.ArchiveID), nil, &r); err != nil {
			return nil, plan.ArchiveRow{}, toolError(err)
		}
		return nil, plan.ArchiveRowOf(r, true), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "archive_stats",
		Description: "Totals across Bambuddy's print records: prints, success rate, print hours, grams of filament, cost, prints by filament type, and how close the slicer's time estimate was. Optionally limited to a date range.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a statsArgs) (*mcp.CallToolResult, plan.StatsRow, error) {
		if err := checkDate("date_from", a.DateFrom); err != nil {
			return nil, plan.StatsRow{}, err
		}
		if err := checkDate("date_to", a.DateTo); err != nil {
			return nil, plan.StatsRow{}, err
		}
		q := url.Values{}
		if a.DateFrom != "" {
			q.Set("date_from", a.DateFrom)
		}
		if a.DateTo != "" {
			q.Set("date_to", a.DateTo)
		}
		var s api.ArchiveStats
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/archives/stats", q, &s); err != nil {
			return nil, plan.StatsRow{}, toolError(err)
		}
		return nil, plan.StatsOf(s), nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "maintenance_overview",
		Description: "Maintenance tasks per printer (carbon rods, nozzle, belts and so on): hours or days until due, which are due or in warning, and when each was last done. Read-only; it does not mark anything as done.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a maintArgs) (*mcp.CallToolResult, maintResult, error) {
		var in []api.MaintenancePrinter
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/maintenance/overview", nil, &in); err != nil {
			return nil, maintResult{}, toolError(err)
		}
		return nil, maintResult{Printers: plan.MaintenanceReport(in, a.PrinterID, a.OnlyDue)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "ams_history",
		Description: "Humidity and temperature of one AMS unit over the last N hours: min, max, average, latest and up to 24 evenly spaced samples. " +
			"Use it to judge whether filament needs drying.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a historyAMSArgs) (*mcp.CallToolResult, plan.HistorySummary, error) {
		hours := a.Hours
		if hours <= 0 {
			hours = 24
		}
		hours = min(hours, 168)
		var h api.AMSHistory
		q := url.Values{"hours": {itoa(hours)}}
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/ams-history/"+itoa(printerID(a.PrinterID))+"/"+itoa(a.AMSID), q, &h); err != nil {
			return nil, plan.HistorySummary{}, toolError(err)
		}
		return nil, plan.SummarizeHistory(h, 24), nil
	})
}

func join(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += "; "
		}
		out += x
	}
	return out
}
