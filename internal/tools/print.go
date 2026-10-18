// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type printFileArgs struct {
	LibraryFileID int    `json:"library_file_id" jsonschema:"Library file id from list_library or upload_file. Must be a sliced .gcode.3mf."`
	AMSMapping    []int  `json:"ams_mapping,omitempty" jsonschema:"AMS slot for each filament in the file: index 0 is the file's filament slot 1. Slots come from printer_status (AMS unit 0 tray 1 is slot 1). Use -1 for an unused filament. If omitted, a tray is chosen per filament from the file's material and colour (loaded trays only, nearest colour; among trays that match equally, the one with the least filament left, so a nearly empty spool is used up first) and the choice and the reason are returned in the result; pass it to override."`
	PlateID       int    `json:"plate_id,omitempty" jsonschema:"Plate to print. Defaults to 1."`
	UseAMS        *bool  `json:"use_ams,omitempty" jsonschema:"Feed from the AMS. Defaults to true. False prints from the external spool and skips the stock check."`
	BedLevelling  string `json:"bed_levelling,omitempty" jsonschema:"auto, on or off. Defaults to auto."`
	PrinterID     int    `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
	Force         bool   `json:"force,omitempty" jsonschema:"Start despite active HMS errors. Does not override the printer state, model or filament checks."`
	DryRun        bool   `json:"dry_run,omitempty" jsonschema:"Run every check and report the chosen ams_mapping, but do not queue anything."`
	ProjectID     int    `json:"project_id,omitempty" jsonschema:"File the print under this Bambuddy project (list_projects)."`
	WaitSeconds   *int   `json:"wait_seconds,omitempty" jsonschema:"After queuing, wait up to this long (default 30, 0 to skip, at most 120) for the printer to leave IDLE or FINISH and report its real state."`
}

// QueueRow is a queue item as the model sees it.
type QueueRow struct {
	ID               int     `json:"id"`
	Status           string  `json:"status"`
	Name             string  `json:"name,omitempty"`
	Printer          string  `json:"printer,omitempty"`
	Plate            int     `json:"plate,omitempty"`
	Position         int     `json:"position,omitempty"`
	WaitingReason    string  `json:"waiting_reason,omitempty"`
	StartedAt        string  `json:"started_at,omitempty"`
	CompletedAt      string  `json:"completed_at,omitempty"`
	PrintTimeMinutes int     `json:"print_time_minutes,omitempty"`
	FilamentG        float64 `json:"filament_g,omitempty"`
	FilamentType     string  `json:"filament_type,omitempty"`
	Error            string  `json:"error,omitempty"`
}

func queueRow(q api.QueueItem) QueueRow {
	name := q.ArchiveName
	if name == "" {
		name = q.LibraryFileName
	}
	r := QueueRow{
		ID: q.ID, Status: q.Status, Name: name, Printer: q.PrinterName, Position: q.Position,
		WaitingReason: q.WaitingReason, StartedAt: q.StartedAt, CompletedAt: q.CompletedAt,
		FilamentType: q.FilamentType, Error: q.ErrorMessage,
	}
	if q.PlateID != nil {
		r.Plate = *q.PlateID
	}
	if q.PrintTimeSeconds != nil {
		r.PrintTimeMinutes = *q.PrintTimeSeconds / 60
	}
	if q.FilamentUsedGrams != nil {
		r.FilamentG = *q.FilamentUsedGrams
	}
	return r
}

type printFileResult struct {
	// Queued means Bambuddy accepted the job. Started means the printer was
	// then seen out of IDLE and FINISH; the two differ when it never began.
	Queued        bool                 `json:"queued"`
	Started       bool                 `json:"started"`
	DryRun        bool                 `json:"dry_run,omitempty"`
	Summary       string               `json:"summary"`
	PrinterNow    string               `json:"printer_now,omitempty"`
	WaitedSeconds float64              `json:"waited_seconds,omitempty"`
	Preflight     plan.PreflightResult `json:"preflight"`
	QueueItem     *QueueRow            `json:"queue_item,omitempty"`
}

func addPrintTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "print_file",
		Description: "Queue a sliced library file on the printer; Bambuddy starts it at once when the printer is idle. " +
			"Checks first and refuses unless ALL hold: printer state is IDLE or FINISH (the result says what it was doing otherwise); " +
			"no HMS errors (returned to you; force: true overrides only this); the file is a .gcode.3mf sliced for the configured model (X1C); " +
			"every filament slot maps to a loaded AMS tray of the same material (chosen for you by material and nearest colour when ams_mapping is omitted); and the Spoolman spool in that tray has the grams needed. " +
			"Other loaded trays with the same material, colour (exact or within a very small distance) and product line or vendor count as backups: the AMS switches to one by itself when the mapped spool runs out, so the mapped spool and its backups are added together against the need. " +
			"If the mapped spool alone is short but the group covers it, the print is allowed with a warning naming the backup tray (the AMS has to switch over mid-print). " +
			"A group short by up to 10% of the need only warns, as does one with under 10% to spare; short by more is refused. Backups with no linked spool add no grams. " +
			"A tray with no linked spool is warned about, not blocked, and is never picked as the nearly empty one. Use dry_run to see the verdict, the tray groups it found and why it chose each tray, without queuing. " +
			"After queuing it waits for the printer to actually leave IDLE or FINISH and reports its state; started is false, with the reason, if it never does. " +
			"Tell the user what the printer was doing and any warnings. Starting a print is the user's decision: confirm the file, plate and filament mapping with them first.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a printFileArgs) (*mcp.CallToolResult, printFileResult, error) {
		if a.LibraryFileID <= 0 {
			return nil, printFileResult{}, fmt.Errorf("library_file_id is required")
		}
		pid := printerID(a.PrinterID)
		plate := a.PlateID
		if plate <= 0 {
			plate = 1
		}
		useAMS := a.UseAMS == nil || *a.UseAMS
		bed := a.BedLevelling
		if bed == "" {
			bed = "auto"
		}

		st, err := statusFor(ctx, d, pid)
		if err != nil {
			return nil, printFileResult{}, err
		}
		file, err := fileDetail(ctx, d, a.LibraryFileID)
		if err != nil {
			return nil, printFileResult{}, err
		}
		if a.ProjectID > 0 {
			if _, err := getProject(ctx, d, a.ProjectID); err != nil {
				return nil, printFileResult{}, fmt.Errorf("not printing: %w", err)
			}
		}
		needs, err := fileNeeds(ctx, d, a.LibraryFileID, plate)
		if err != nil {
			return nil, printFileResult{}, err
		}
		lookup := plan.NewLookup(pid, nil, nil)
		if useAMS {
			spools, err := allSpools(ctx, d)
			if err != nil {
				return nil, printFileResult{}, fmt.Errorf("cannot check stock, so not printing: %w", err)
			}
			assigns, err := slotAssignments(ctx, d)
			if err != nil {
				return nil, printFileResult{}, fmt.Errorf("cannot check stock, so not printing: %w", err)
			}
			lookup = plan.NewLookup(pid, assigns, spoolViews(spools))
		}

		pf := plan.Preflight(plan.PreflightInput{
			Status: st, File: file, Needs: needs, Mapping: a.AMSMapping, UseAMS: useAMS,
			Force: a.Force, Model: d.Model, Lookup: lookup, HMSText: hmsText(ctx, d, st),
		})
		res := printFileResult{Preflight: pf, DryRun: a.DryRun}
		switch {
		case !pf.Allowed:
			res.Summary = "NOT started. Printer was " + pf.PrinterDoing + ". " + strings.Join(pf.Refusals, "; ") + "."
			return nil, res, nil
		case a.DryRun:
			res.Summary = "Dry run: all checks passed. Printer is " + pf.PrinterDoing + "." + mappingNote(pf)
			return nil, res, nil
		}

		body := api.QueueCreate{PrinterID: pid, LibraryFileID: a.LibraryFileID, PlateID: plate, UseAMS: useAMS, BedLevelling: bed}
		if useAMS {
			body.AMSMapping = pf.AMSMapping
		}
		if a.ProjectID > 0 {
			body.ProjectID = &a.ProjectID
		}
		var item api.QueueItem
		if err := d.Bambuddy.Send(ctx, "POST", "/api/v1/queue/", body, &item); err != nil {
			return nil, res, toolError(err)
		}
		res.Queued = true
		wait := 30
		if a.WaitSeconds != nil {
			wait = min(max(*a.WaitSeconds, 0), 120)
		}
		queued := fmt.Sprintf("Queued %q as item %d", file.Filename, item.ID)
		if wait == 0 {
			row := queueRow(item)
			res.QueueItem = &row
			res.Summary = queued + fmt.Sprintf(" (status %s); not waiting, so the printer was not checked.", item.Status) + mappingNote(pf)
			return nil, res, nil
		}

		after, waited, started, err := waitForStart(ctx, d, pid, time.Duration(wait)*time.Second)
		res.WaitedSeconds = float64(int(waited.Seconds()*10)) / 10
		var latest api.QueueItem
		if _, qerr := d.Bambuddy.Get(ctx, "/api/v1/queue/"+itoa(item.ID), nil, &latest); qerr == nil && latest.ID != 0 {
			item = latest
		}
		row := queueRow(item)
		res.QueueItem = &row
		if err != nil {
			res.Summary = queued + "; the printer could not be read afterwards: " + err.Error()
			return nil, res, nil
		}
		res.PrinterNow = plan.NowLine(after)
		res.Started = started
		switch {
		case started:
			res.Summary = fmt.Sprintf("%s. Printer is now %s (after %.0f s).", queued, res.PrinterNow, waited.Seconds())
		case strings.EqualFold(after.State, "FAILED"):
			res.Summary = fmt.Sprintf("%s, but the printer reports FAILED, so it did not start. Check printer_status for the cause.", queued)
		case after.AwaitingPlateClear:
			res.Summary = fmt.Sprintf("%s, but after %d s the printer is still %s, so it has not started. Bambuddy is waiting for the plate to be marked clear (clear_plate, after asking the user).", queued, wait, strings.ToUpper(after.State))
		default:
			res.Summary = fmt.Sprintf("%s, but after %d s the printer is still %s, so it has not started. Queue item status: %s. Check queue_list and printer_status.", queued, wait, strings.ToUpper(after.State), item.Status)
		}
		res.Summary += mappingNote(pf)
		return nil, res, nil
	})
}

// mappingNote tells the caller which AMS mapping was used when it was chosen
// for them, so they can check it against what they meant.
func mappingNote(pf plan.PreflightResult) string {
	if pf.MappingSource != "auto" {
		return ""
	}
	return fmt.Sprintf(" AMS mapping %v was chosen automatically by material and colour.", pf.AMSMapping)
}

// waitForStart polls the printer until it leaves IDLE and FINISH or the time
// is up. It reports the last status read and whether the print began.
func waitForStart(ctx context.Context, d *Deps, pid int, limit time.Duration) (api.PrinterStatus, time.Duration, bool, error) {
	began := time.Now()
	var st api.PrinterStatus
	for {
		select {
		case <-ctx.Done():
			return st, time.Since(began), false, ctx.Err()
		case <-time.After(d.pollEvery()):
		}
		var err error
		if st, err = statusFor(ctx, d, pid); err != nil {
			return st, time.Since(began), false, err
		}
		switch strings.ToUpper(st.State) {
		case "IDLE", "FINISH":
		case "FAILED":
			return st, time.Since(began), false, nil
		default:
			return st, time.Since(began), true, nil
		}
		if time.Since(began) >= limit {
			return st, time.Since(began), false, nil
		}
	}
}

// ─── queue ───────────────────────────────────────────────────────────────────

type queueListArgs struct {
	Status    string `json:"status,omitempty" jsonschema:"Filter by status, such as pending or printing. Default: everything not yet finished."`
	PrinterID int    `json:"printer_id,omitempty" jsonschema:"Only this printer."`
}

type queueResult struct {
	Items []QueueRow `json:"items"`
}

type queueCancelArgs struct {
	ItemID  int  `json:"item_id" jsonschema:"Queue item id from queue_list."`
	Confirm bool `json:"confirm" jsonschema:"Must be true. Set it only after the user has said yes to cancelling this item."`
}

type historyArgs struct {
	Status string `json:"status,omitempty" jsonschema:"completed, failed or cancelled. Default: all three."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum jobs, newest first. Defaults to 20."`
}

var finished = map[string]bool{"completed": true, "failed": true, "cancelled": true, "skipped": true}

func fetchQueue(ctx context.Context, d *Deps, status string, pid int) ([]api.QueueItem, error) {
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if pid > 0 {
		q.Set("printer_id", itoa(pid))
	}
	var items []api.QueueItem
	_, err := d.Bambuddy.Get(ctx, "/api/v1/queue/", q, &items)
	return items, toolError(err)
}

func addQueueTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "queue_list",
		Description: "List queued and running print jobs (pending, printing and so on), not finished ones; see print_history for those. Use it to watch a job you started.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a queueListArgs) (*mcp.CallToolResult, queueResult, error) {
		items, err := fetchQueue(ctx, d, a.Status, a.PrinterID)
		if err != nil {
			return nil, queueResult{}, err
		}
		res := queueResult{Items: []QueueRow{}}
		for _, it := range items {
			if a.Status == "" && finished[it.Status] {
				continue
			}
			res.Items = append(res.Items, queueRow(it))
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "queue_cancel",
		Description: "DESTRUCTIVE: cancels a queue item so it never prints. " +
			"Claude must ask the user first and get a clear yes for this specific item before calling this. Never call it on your own judgment. Pass confirm: true only after that yes. " +
			"To stop a print already running, use print_stop (also needs a yes).",
		Annotations: destructive(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a queueCancelArgs) (*mcp.CallToolResult, actionResult, error) {
		if a.ItemID <= 0 {
			return nil, actionResult{}, fmt.Errorf("item_id is required")
		}
		if !a.Confirm {
			return nil, actionResult{Message: "not cancelled: ask the user, then call again with confirm: true"}, nil
		}
		var out map[string]any
		if err := d.Bambuddy.Send(ctx, "POST", "/api/v1/queue/"+itoa(a.ItemID)+"/cancel", nil, &out); err != nil {
			return nil, actionResult{}, toolError(err)
		}
		return nil, actionResult{Done: true, Message: fmt.Sprintf("queue item %d cancelled", a.ItemID)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "print_history",
		Description: "Finished, failed and cancelled print jobs, newest first, with print time and grams of filament. Use it to see how a job ended or what was printed lately.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a historyArgs) (*mcp.CallToolResult, queueResult, error) {
		items, err := fetchQueue(ctx, d, a.Status, 0)
		if err != nil {
			return nil, queueResult{}, err
		}
		var done []api.QueueItem
		for _, it := range items {
			if finished[it.Status] {
				done = append(done, it)
			}
		}
		sort.SliceStable(done, func(i, j int) bool { return stamp(done[i]) > stamp(done[j]) })
		limit := a.Limit
		if limit <= 0 {
			limit = 20
		}
		if len(done) > limit {
			done = done[:limit]
		}
		res := queueResult{Items: make([]QueueRow, 0, len(done))}
		for _, it := range done {
			res.Items = append(res.Items, queueRow(it))
		}
		return nil, res, nil
	})
}

func stamp(q api.QueueItem) string {
	if q.CompletedAt != "" {
		return q.CompletedAt
	}
	return q.CreatedAt
}
