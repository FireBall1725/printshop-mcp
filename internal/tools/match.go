// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"fmt"

	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type needArg struct {
	Type  string  `json:"type" jsonschema:"Material, such as PLA."`
	Color string  `json:"color" jsonschema:"Hex colour of the filament in the slicer."`
	Grams float64 `json:"grams" jsonschema:"Grams the print uses."`
}

type stockArgs struct {
	LibraryFileID int       `json:"library_file_id,omitempty" jsonschema:"A library file id; its filament slots are read from Bambuddy. Give this or slots."`
	PlateID       int       `json:"plate_id,omitempty" jsonschema:"Plate when using library_file_id. Defaults to 1."`
	Slots         []needArg `json:"slots,omitempty" jsonschema:"Filament slots as type, colour and grams, for a file that is not in the library."`
}

type stockResult struct {
	File string `json:"file,omitempty"`
	plan.StockPlan
}

type amsArgs struct {
	PrinterID int `json:"printer_id,omitempty" jsonschema:"Bambuddy printer id. Defaults to 1."`
}

type amsResult struct {
	Trays    []plan.TrayCheck `json:"trays"`
	Mismatch int              `json:"trays_with_flags"`
}

func addMatchTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "stock_for_print",
		Description: "Say whether the stock covers a sliced print. Give a library_file_id (grams per filament read from the file) or slots (type, colour, grams). " +
			"For each filament it matches material first, then the nearest colour among unarchived spools, and reports the spool, its grams and what would be left. " +
			"If the nearest colour is short, other spools with enough are listed with their colour distance. color_match is exact, close, similar or different. " +
			"This checks stock only; it does not know what is loaded in the AMS (see ams_vs_stock).",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a stockArgs) (*mcp.CallToolResult, stockResult, error) {
		var res stockResult
		var needs []plan.Need
		switch {
		case a.LibraryFileID > 0 && len(a.Slots) > 0:
			return nil, res, fmt.Errorf("give library_file_id or slots, not both")
		case a.LibraryFileID > 0:
			f, err := fileDetail(ctx, d, a.LibraryFileID)
			if err != nil {
				return nil, res, err
			}
			plate := a.PlateID
			if plate <= 0 {
				plate = 1
			}
			if needs, err = fileNeeds(ctx, d, a.LibraryFileID, plate); err != nil {
				return nil, res, err
			}
			res.File = f.Filename
		case len(a.Slots) > 0:
			for i, s := range a.Slots {
				needs = append(needs, plan.Need{SlotID: i + 1, Type: s.Type, Color: s.Color, Grams: s.Grams})
			}
		default:
			return nil, res, fmt.Errorf("give library_file_id or slots")
		}
		if len(needs) == 0 {
			return nil, res, fmt.Errorf("the file reports no filament use; it may not be a sliced .gcode.3mf")
		}
		spools, err := allSpools(ctx, d)
		if err != nil {
			return nil, res, err
		}
		res.StockPlan = plan.PlanStock(needs, spoolViews(spools))
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "ams_vs_stock",
		Description: "Compare each loaded AMS tray with the Spoolman spool it maps to (Bambuddy's slot assignment, else the spool whose tag equals the tray's RFID uuid) " +
			"and flag mismatches: no linked spool, material or colour differs, or the AMS remaining percent and Spoolman's disagree by more than 15 points. " +
			"Read-only; it never changes either side.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a amsArgs) (*mcp.CallToolResult, amsResult, error) {
		pid := printerID(a.PrinterID)
		st, err := statusFor(ctx, d, pid)
		if err != nil {
			return nil, amsResult{}, err
		}
		spools, err := allSpools(ctx, d)
		if err != nil {
			return nil, amsResult{}, err
		}
		assigns, err := slotAssignments(ctx, d)
		if err != nil {
			return nil, amsResult{}, err
		}
		trays := plan.CheckAMS(st, plan.NewLookup(pid, assigns, spoolViews(spools)))
		res := amsResult{Trays: trays}
		if res.Trays == nil {
			res.Trays = []plan.TrayCheck{}
		}
		for _, t := range trays {
			if len(t.Flags) > 0 {
				res.Mismatch++
			}
		}
		return nil, res, nil
	})
}
