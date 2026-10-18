// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type printersResult struct {
	Printers []plan.PrinterRow `json:"printers"`
}

type clearHMSResult struct {
	Cleared   bool              `json:"cleared"`
	Message   string            `json:"message"`
	Before    []plan.HMSSummary `json:"before,omitempty"`
	Remaining []plan.HMSSummary `json:"remaining,omitempty"`
}

type speedArgs struct {
	PrinterID int `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
	Mode      int `json:"mode" jsonschema:"1 silent, 2 standard, 3 sport, 4 ludicrous."`
}

type objectsArgs struct {
	PrinterID int `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
}

type objectsResult struct {
	IsPrinting   bool              `json:"is_printing"`
	Objects      []api.PrintObject `json:"objects"`
	SkippedCount int               `json:"skipped_count"`
}

type skipArgs struct {
	PrinterID int   `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
	ObjectIDs []int `json:"object_ids" jsonschema:"Ids from list_printable_objects."`
	Confirm   bool  `json:"confirm" jsonschema:"Must be true. Set it only after the user has said yes to skipping these named objects."`
}

type dryStartArgs struct {
	PrinterID  int    `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
	AMSID      int    `json:"ams_id" jsonschema:"AMS unit id from printer_status ams_units. 0 is the first unit."`
	TempC      int    `json:"temp_c,omitempty" jsonschema:"45 to 85. Omit for Bambuddy's default (45)."`
	Hours      int    `json:"hours,omitempty" jsonschema:"1 to 24. Omit for Bambuddy's default (4)."`
	Filament   string `json:"filament,omitempty" jsonschema:"Filament type being dried, such as PLA."`
	RotateTray bool   `json:"rotate_tray,omitempty" jsonschema:"Rotate the spools during the cycle."`
}

type dryStopArgs struct {
	PrinterID int `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
	AMSID     int `json:"ams_id" jsonschema:"AMS unit id."`
}

func addControlTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_printers",
		Description: "List the printers Bambuddy manages: id, name, model, location, whether active. Other tools default to printer 1, so this is only needed if a second printer is added.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, printersResult, error) {
		var ps []api.PrinterInfo
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/printers/", nil, &ps); err != nil {
			return nil, printersResult{}, toolError(err)
		}
		return nil, printersResult{Printers: plan.PrinterRows(ps)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "clear_plate",
		Description: "Tell Bambuddy the build plate has been cleared after a finished or failed print, so the next queued print may start. " +
			"Nothing is sent to the printer. This is a claim about the physical plate: ask the user whether they have taken the print off, and pass confirm: true only after they say yes. " +
			"printer_status shows awaiting_plate_clear when Bambuddy is waiting for this.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a confirmArgs) (*mcp.CallToolResult, actionResult, error) {
		if !a.Confirm {
			return nil, actionResult{Message: "not acknowledged: ask the user whether the plate is clear, then call again with confirm: true"}, nil
		}
		return control(ctx, d, a.PrinterID, "")
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "clear_hms",
		Description: "Clear the active HMS and print errors on the printer so print_file stops refusing on them. " +
			"This hides the error; it does not fix the cause, and the printer raises it again if the fault is still there. " +
			"Read the error text to the user and ask whether the cause is dealt with (card space freed, door closed, and so on). " +
			"Level 1 and 2 errors (fatal, serious) stop or pause prints, so never clear those without a clear yes. " +
			"Pass confirm: true only after that yes. The result lists what was cleared and anything that came straight back.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a confirmArgs) (*mcp.CallToolResult, clearHMSResult, error) {
		id := printerID(a.PrinterID)
		before, err := statusFor(ctx, d, id)
		if err != nil {
			return nil, clearHMSResult{}, err
		}
		list := plan.HMSSummaries(before.HMSErrors, hmsText(ctx, d, before))
		if len(list) == 0 {
			return nil, clearHMSResult{Message: "there are no active HMS errors to clear"}, nil
		}
		if !a.Confirm {
			return nil, clearHMSResult{Before: list, Message: "not cleared: read these to the user, ask whether the cause is dealt with, then call again with confirm: true"}, nil
		}
		var out map[string]any
		if err := d.Bambuddy.Send(ctx, "POST", "/api/v1/printers/"+itoa(id)+"/hms/clear", nil, &out); err != nil {
			return nil, clearHMSResult{}, toolError(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(d.pollEvery()):
		}
		res := clearHMSResult{Before: list}
		after, err := statusFor(ctx, d, id)
		if err != nil {
			res.Cleared, res.Message = true, "clear sent; the printer could not be read afterwards"
			return nil, res, nil
		}
		res.Remaining = plan.HMSSummaries(after.HMSErrors, hmsText(ctx, d, after))
		res.Cleared = len(res.Remaining) < len(list)
		switch {
		case len(res.Remaining) == 0:
			res.Message = fmt.Sprintf("cleared %d error(s)", len(list))
		case res.Cleared:
			res.Message = fmt.Sprintf("cleared some; %d error(s) remain or came straight back, so the cause is probably still there", len(res.Remaining))
		default:
			res.Message = "clear was sent but every error is still present, so the cause is probably still there"
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "print_speed",
		Description: "Set the speed mode of the running print: 1 silent, 2 standard, 3 sport, 4 ludicrous. " +
			"Higher modes are louder and can hurt quality on fine detail. Change it only when the user asks.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a speedArgs) (*mcp.CallToolResult, actionResult, error) {
		if a.Mode < 1 || a.Mode > 4 {
			return nil, actionResult{}, fmt.Errorf("mode must be 1 to 4")
		}
		id := printerID(a.PrinterID)
		st, err := statusFor(ctx, d, id)
		if err != nil {
			return nil, actionResult{}, err
		}
		var out map[string]any
		q := url.Values{"mode": {itoa(a.Mode)}}
		if err := d.Bambuddy.SendQuery(ctx, "POST", "/api/v1/printers/"+itoa(id)+"/print-speed", q, nil, &out); err != nil {
			return nil, actionResult{}, toolError(err)
		}
		return nil, actionResult{Done: true, Message: fmt.Sprintf("speed mode %d sent; printer was %s (%s)", a.Mode, strings.ToUpper(st.State), plan.Doing(st))}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_printable_objects",
		Description: "The objects on the plate of the running print, with ids and whether each is already skipped. Use the ids with skip_objects.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a objectsArgs) (*mcp.CallToolResult, objectsResult, error) {
		po, err := printObjects(ctx, d, printerID(a.PrinterID))
		if err != nil {
			return nil, objectsResult{}, err
		}
		res := objectsResult{IsPrinting: po.IsPrinting, Objects: po.Objects, SkippedCount: po.SkippedCount}
		if res.Objects == nil {
			res.Objects = []api.PrintObject{}
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "skip_objects",
		Description: "DESTRUCTIVE: skip objects on the running plate. A skipped object is not printed and cannot be brought back. " +
			"Claude must ask the user first, naming the objects, and get a clear yes before calling this. Never call it on your own judgment. Pass confirm: true only after that yes. " +
			"Refused if an id is unknown or already skipped, or if it would skip everything (use print_stop for that, which needs its own yes).",
		Annotations: destructive(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a skipArgs) (*mcp.CallToolResult, actionResult, error) {
		if !a.Confirm {
			return nil, actionResult{Message: "not skipped: ask the user which objects, then call again with confirm: true"}, nil
		}
		id := printerID(a.PrinterID)
		po, err := printObjects(ctx, d, id)
		if err != nil {
			return nil, actionResult{}, err
		}
		if why := plan.CheckSkip(po, a.ObjectIDs); len(why) > 0 {
			return nil, actionResult{Message: "not skipped: " + strings.Join(why, "; ")}, nil
		}
		var out map[string]any
		if err := d.Bambuddy.Send(ctx, "POST", "/api/v1/printers/"+itoa(id)+"/print/skip-objects", a.ObjectIDs, &out); err != nil {
			return nil, actionResult{}, toolError(err)
		}
		return nil, actionResult{Done: true, Message: fmt.Sprintf("skipped object(s) %v", a.ObjectIDs)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "start_drying",
		Description: "Start an AMS drying cycle on one AMS unit: temp_c 45 to 85, hours 1 to 24 (Bambuddy's defaults are 45 and 4). It heats the AMS for hours, so do it only when the user asks. " +
			"Refused if the printer is mid-print and does not support drying while printing, or if the unit does not exist. printer_status ams_units shows humidity and any active cycle.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a dryStartArgs) (*mcp.CallToolResult, actionResult, error) {
		id := printerID(a.PrinterID)
		st, err := statusFor(ctx, d, id)
		if err != nil {
			return nil, actionResult{}, err
		}
		if why := plan.CheckDrying(st, a.AMSID, a.TempC, a.Hours); len(why) > 0 {
			return nil, actionResult{Message: "not started: " + strings.Join(why, "; ")}, nil
		}
		q := url.Values{"ams_id": {itoa(a.AMSID)}}
		if a.TempC > 0 {
			q.Set("temp", itoa(a.TempC))
		}
		if a.Hours > 0 {
			q.Set("duration", itoa(a.Hours))
		}
		if a.Filament != "" {
			q.Set("filament", a.Filament)
		}
		if a.RotateTray {
			q.Set("rotate_tray", "true")
		}
		var out map[string]any
		if err := d.Bambuddy.SendQuery(ctx, "POST", "/api/v1/printers/"+itoa(id)+"/drying/start", q, nil, &out); err != nil {
			return nil, actionResult{}, toolError(err)
		}
		return nil, actionResult{Done: true, Message: fmt.Sprintf("drying start sent for AMS %d; printer was %s", a.AMSID, strings.ToUpper(st.State))}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "stop_drying",
		Description: "Stop the drying cycle on one AMS unit.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a dryStopArgs) (*mcp.CallToolResult, actionResult, error) {
		var out map[string]any
		q := url.Values{"ams_id": {itoa(a.AMSID)}}
		if err := d.Bambuddy.SendQuery(ctx, "POST", "/api/v1/printers/"+itoa(printerID(a.PrinterID))+"/drying/stop", q, nil, &out); err != nil {
			return nil, actionResult{}, toolError(err)
		}
		return nil, actionResult{Done: true, Message: fmt.Sprintf("drying stop sent for AMS %d", a.AMSID)}, nil
	})
}

func printObjects(ctx context.Context, d *Deps, id int) (api.PrintObjects, error) {
	var po api.PrintObjects
	_, err := d.Bambuddy.Get(ctx, "/api/v1/printers/"+itoa(id)+"/print/objects", nil, &po)
	return po, toolError(err)
}
