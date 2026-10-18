// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type printerArgs struct {
	PrinterID int `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1, the X1C."`
}

func addPrinterTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "printer_status",
		Description: "Trimmed status of the printer: state (IDLE, RUNNING, PAUSE, FINISH, FAILED), job name, progress, layer, minutes remaining, " +
			"bed, nozzle and chamber temperatures with targets, door, the loaded AMS trays (slot, type, colour, remaining percent), AMS humidity and drying, the last finished job, and a next_step hint. " +
			"HMS errors come with Bambu's published text and alert level (fatal, serious, common, info), read from the code itself. " +
			"The slot number is what print_file's ams_mapping uses. Call this before starting or stopping anything.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a printerArgs) (*mcp.CallToolResult, plan.StatusSummary, error) {
		pid := printerID(a.PrinterID)
		st, err := statusFor(ctx, d, pid)
		if err != nil {
			return nil, plan.StatusSummary{}, err
		}
		s := plan.Summarize(st, hmsText(ctx, d, st))
		// The last job and next step are extras; a failure here must not hide the status.
		if items, err := fetchQueue(ctx, d, "", pid); err == nil {
			s.AttachLastJob(items)
		}
		return nil, s, nil
	})
}

// ─── pause / resume / stop ───────────────────────────────────────────────────

type confirmArgs struct {
	PrinterID int  `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
	Confirm   bool `json:"confirm" jsonschema:"Must be true. Set it only after the user has said yes to this specific action in this conversation."`
}

type actionResult struct {
	Done    bool   `json:"done"`
	Message string `json:"message,omitempty"`
}

func addControl(srv *mcp.Server, d *Deps, name, desc, verb string, ann *mcp.ToolAnnotations) {
	mcp.AddTool(srv, &mcp.Tool{Name: name, Description: desc, Annotations: ann},
		func(ctx context.Context, _ *mcp.CallToolRequest, a printerArgs) (*mcp.CallToolResult, actionResult, error) {
			return control(ctx, d, a.PrinterID, verb)
		})
}

func control(ctx context.Context, d *Deps, id int, verb string) (*mcp.CallToolResult, actionResult, error) {
	id = printerID(id)
	st, err := statusFor(ctx, d, id)
	if err != nil {
		return nil, actionResult{}, err
	}
	path, label := "/api/v1/printers/"+itoa(id)+"/print/"+verb, verb
	if verb == "" {
		path, label = "/api/v1/printers/"+itoa(id)+"/clear-plate", "clear-plate"
	}
	var out map[string]any
	if err := d.Bambuddy.Send(ctx, "POST", path, nil, &out); err != nil {
		return nil, actionResult{}, toolError(err)
	}
	msg := "sent " + label + "; printer was " + strings.ToUpper(st.State) + " (" + plan.Doing(st) + ")"
	return nil, actionResult{Done: true, Message: msg}, nil
}

func addPrintControlTools(srv *mcp.Server, d *Deps) {
	addControl(srv, d, "print_pause", "Pause the current print. Reversible with print_resume.", "pause", writes())
	addControl(srv, d, "print_resume", "Resume a paused print.", "resume", writes())

	mcp.AddTool(srv, &mcp.Tool{
		Name: "print_stop",
		Description: "DESTRUCTIVE: stops the running print and throws away the job; it cannot be resumed. " +
			"Claude must ask the user first and get a clear yes for this specific print before calling this. Never call it on your own judgment. " +
			"Pass confirm: true only after that yes. Use print_pause if the user may want to continue.",
		Annotations: destructive(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a confirmArgs) (*mcp.CallToolResult, actionResult, error) {
		if !a.Confirm {
			return nil, actionResult{Message: "not stopped: ask the user, then call again with confirm: true"}, nil
		}
		return control(ctx, d, a.PrinterID, "stop")
	})
}
