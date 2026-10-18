// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listSpoolsArgs struct {
	Material     string  `json:"material,omitempty" jsonschema:"Material, such as PLA or PETG. Case-insensitive, exact."`
	Color        string  `json:"color,omitempty" jsonschema:"Hex colour, such as 000000 or #FF69B4. Results are sorted nearest first and carry color_delta_e."`
	Vendor       string  `json:"vendor,omitempty" jsonschema:"Case-insensitive substring of the vendor name."`
	InStockOnly  *bool   `json:"in_stock_only,omitempty" jsonschema:"Only unarchived spools with grams left above min_remaining_g. Defaults to true."`
	MinRemaining float64 `json:"min_remaining_g,omitempty" jsonschema:"Stock threshold in grams. Defaults to 0."`
	Limit        int     `json:"limit,omitempty" jsonschema:"Maximum spools. Defaults to 50."`
}

type spoolRow struct {
	plan.SpoolView
	AMSSlot     *int     `json:"ams_slot,omitempty"`
	ColorDeltaE *float64 `json:"color_delta_e,omitempty"`
	ColorMatch  string   `json:"color_match,omitempty"`
}

type listSpoolsResult struct {
	Spools    []spoolRow `json:"spools"`
	Total     int        `json:"total"`
	Truncated bool       `json:"truncated,omitempty"`
}

type listFilamentsArgs struct {
	Material string `json:"material,omitempty" jsonschema:"Material, exact, case-insensitive."`
	Vendor   string `json:"vendor,omitempty" jsonschema:"Substring of the vendor name."`
}

type filamentRow struct {
	ID          int      `json:"id"`
	Vendor      string   `json:"vendor,omitempty"`
	Name        string   `json:"name"`
	Material    string   `json:"material"`
	ColorHex    string   `json:"color_hex,omitempty"`
	WeightG     *float64 `json:"weight_g,omitempty"`
	SpoolWeight *float64 `json:"empty_spool_g,omitempty"`
	Diameter    float64  `json:"diameter_mm,omitempty"`
	ExtruderC   *int     `json:"extruder_temp_c,omitempty"`
	BedC        *int     `json:"bed_temp_c,omitempty"`
}

type listFilamentsResult struct {
	Filaments []filamentRow `json:"filaments"`
}

type vendorsResult struct {
	Vendors []api.Vendor `json:"vendors"`
}

type noArgs struct{}

type spoolIDArgs struct {
	SpoolID int `json:"spool_id" jsonschema:"Spoolman spool id."`
}

type spoolDetail struct {
	plan.SpoolView
	UsedG     float64           `json:"used_g"`
	FirstUsed string            `json:"first_used,omitempty"`
	LastUsed  string            `json:"last_used,omitempty"`
	Lot       string            `json:"lot_nr,omitempty"`
	Comment   string            `json:"comment,omitempty"`
	Price     *float64          `json:"price,omitempty"`
	AMS       *amsLocation      `json:"ams_slot,omitempty"`
	Extra     map[string]string `json:"extra,omitempty"`
}

type amsLocation struct {
	PrinterID int `json:"printer_id"`
	Slot      int `json:"slot"`
}

type useArgs struct {
	SpoolID   int     `json:"spool_id" jsonschema:"Spoolman spool id."`
	UseWeight float64 `json:"use_weight_g,omitempty" jsonschema:"Grams consumed. Give this or use_length_mm."`
	UseLength float64 `json:"use_length_mm,omitempty" jsonschema:"Millimetres of filament consumed. Give this or use_weight_g."`
}

type measureArgs struct {
	SpoolID int     `json:"spool_id" jsonschema:"Spoolman spool id."`
	Weight  float64 `json:"gross_weight_g" jsonschema:"What the scale shows for the whole spool, filament plus empty spool, in grams. Spoolman subtracts the empty-spool weight."`
}

type updateArgs struct {
	SpoolID    int      `json:"spool_id" jsonschema:"Spoolman spool id."`
	Location   *string  `json:"location,omitempty" jsonschema:"Where the spool is kept. Empty string clears it."`
	Comment    *string  `json:"comment,omitempty"`
	LotNr      *string  `json:"lot_nr,omitempty"`
	Price      *float64 `json:"price,omitempty"`
	Archived   *bool    `json:"archived,omitempty" jsonschema:"Archive (true) or restore (false). Archived spools drop out of stock."`
	Remaining  *float64 `json:"remaining_weight_g,omitempty" jsonschema:"Set grams of filament left."`
	Used       *float64 `json:"used_weight_g,omitempty" jsonschema:"Set grams used so far."`
	Initial    *float64 `json:"initial_weight_g,omitempty" jsonschema:"Filament weight when new."`
	Empty      *float64 `json:"spool_weight_g,omitempty" jsonschema:"Weight of the empty spool."`
	FilamentID *int     `json:"filament_id,omitempty" jsonschema:"Move the spool to another filament (list_filaments). Sent on its own, and extra is never sent. The spool is read back afterwards and extra.tag is checked."`
}

type createArgs struct {
	FilamentID int      `json:"filament_id" jsonschema:"Spoolman filament id from list_filaments."`
	Initial    *float64 `json:"initial_weight_g,omitempty" jsonschema:"Filament weight when new. Defaults to the filament's weight."`
	Remaining  *float64 `json:"remaining_weight_g,omitempty" jsonschema:"Grams left, if the spool is not new."`
	Empty      *float64 `json:"spool_weight_g,omitempty" jsonschema:"Weight of the empty spool."`
	Location   string   `json:"location,omitempty"`
	LotNr      string   `json:"lot_nr,omitempty"`
	Price      *float64 `json:"price,omitempty"`
	Comment    string   `json:"comment,omitempty"`
}

func addSpoolTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_spools",
		Description: "List filament spools from Spoolman: id, vendor, filament name, material, colour hex, grams remaining, location. " +
			"Filter by material, vendor and colour (nearest first). By default only spools in stock: not archived and above min_remaining_g. " +
			"tag_set says whether extra.tag is set, which is how Bambuddy tracks a spool against an AMS tray; ams_slot shows where Bambuddy has it assigned.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a listSpoolsArgs) (*mcp.CallToolResult, listSpoolsResult, error) {
		raw, err := allSpools(ctx, d)
		if err != nil {
			return nil, listSpoolsResult{}, err
		}
		slots := map[int]int{}
		if as, err := slotAssignments(ctx, d); err == nil { // best effort: a Bambuddy outage must not hide the list
			for _, x := range as {
				slots[x.SpoolID] = plan.GlobalSlot(x.AMSID, x.TrayID)
			}
		}
		inStock := a.InStockOnly == nil || *a.InStockOnly
		res := listSpoolsResult{Spools: []spoolRow{}}
		var rows []spoolRow
		for _, v := range spoolViews(raw) {
			if inStock && !v.InStock(a.MinRemaining) {
				continue
			}
			if a.Material != "" && !strings.EqualFold(v.Material, a.Material) {
				continue
			}
			if a.Vendor != "" && !strings.Contains(strings.ToLower(v.Vendor), strings.ToLower(a.Vendor)) {
				continue
			}
			row := spoolRow{SpoolView: v}
			if slot, ok := slots[v.ID]; ok {
				row.AMSSlot = &slot
			}
			if a.Color != "" {
				de, ok := plan.DeltaE(a.Color, v.ColorHex)
				if ok {
					de = float64(int(de*10+0.5)) / 10
					row.ColorDeltaE = &de
				}
				row.ColorMatch = plan.ColourClass(de, ok)
			}
			rows = append(rows, row)
		}
		if a.Color != "" {
			sort.SliceStable(rows, func(i, j int) bool {
				if di, dj := deltaOf(rows[i]), deltaOf(rows[j]); di != dj {
					return di < dj
				}
				return rows[i].RemainingG > rows[j].RemainingG
			})
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 50
		}
		res.Total = len(rows)
		if len(rows) > limit {
			rows, res.Truncated = rows[:limit], true
		}
		if rows != nil {
			res.Spools = rows
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_filaments",
		Description: "List filament types in Spoolman (not individual spools): vendor, name, material, colour, nominal weight, print temperatures. Use the id with create_spool.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a listFilamentsArgs) (*mcp.CallToolResult, listFilamentsResult, error) {
		var fs []api.Filament
		if _, err := d.Spoolman.Get(ctx, "/api/v1/filament", nil, &fs); err != nil {
			return nil, listFilamentsResult{}, toolError(err)
		}
		res := listFilamentsResult{Filaments: []filamentRow{}}
		for _, f := range fs {
			vendor := ""
			if f.Vendor != nil {
				vendor = f.Vendor.Name
			}
			if a.Material != "" && !strings.EqualFold(f.Material, a.Material) {
				continue
			}
			if a.Vendor != "" && !strings.Contains(strings.ToLower(vendor), strings.ToLower(a.Vendor)) {
				continue
			}
			res.Filaments = append(res.Filaments, filamentRow{
				ID: f.ID, Vendor: vendor, Name: f.Name, Material: f.Material, ColorHex: plan.NormHex(f.ColorHex),
				WeightG: f.Weight, SpoolWeight: f.SpoolWeight, Diameter: f.Diameter, ExtruderC: f.ExtruderTemp, BedC: f.BedTemp,
			})
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_vendors",
		Description: "List filament vendors in Spoolman.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, vendorsResult, error) {
		res := vendorsResult{Vendors: []api.Vendor{}}
		if _, err := d.Spoolman.Get(ctx, "/api/v1/vendor", nil, &res.Vendors); err != nil {
			return nil, vendorsResult{}, toolError(err)
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_spool",
		Description: "One spool in full: grams left and used, dates, lot, comment, whether Bambuddy tracks it, and which AMS slot it sits in if any.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a spoolIDArgs) (*mcp.CallToolResult, spoolDetail, error) {
		var s api.Spool
		if _, err := d.Spoolman.Get(ctx, "/api/v1/spool/"+itoa(a.SpoolID), nil, &s); err != nil {
			return nil, spoolDetail{}, toolError(err)
		}
		det := spoolDetail{
			SpoolView: plan.ViewOf(s), UsedG: s.UsedWeight, FirstUsed: s.FirstUsed, LastUsed: s.LastUsed,
			Lot: s.LotNr, Comment: s.Comment, Price: s.Price, Extra: s.Extra,
		}
		if as, err := slotAssignments(ctx, d); err == nil {
			for _, x := range as {
				if x.SpoolID == s.ID {
					det.AMS = &amsLocation{PrinterID: x.PrinterID, Slot: plan.GlobalSlot(x.AMSID, x.TrayID)}
				}
			}
		}
		return nil, det, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "use_filament",
		Description: "Record filament consumed from a spool, by weight (g) or length (mm). " +
			"Do NOT use this for prints made through Bambuddy: it is set to sync automatically and reports their usage itself, so logging them here counts them twice. " +
			"Use it for prints made elsewhere or one-off corrections. Give exactly one of use_weight_g and use_length_mm.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a useArgs) (*mcp.CallToolResult, spoolRow, error) {
		if (a.UseWeight > 0) == (a.UseLength > 0) {
			return nil, spoolRow{}, fmt.Errorf("give exactly one of use_weight_g and use_length_mm, above zero")
		}
		body := map[string]float64{}
		if a.UseWeight > 0 {
			body["use_weight"] = a.UseWeight
		} else {
			body["use_length"] = a.UseLength
		}
		return putSpool(ctx, d, "PUT", "/api/v1/spool/"+itoa(a.SpoolID)+"/use", body)
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "measure_spool",
		Description: "Set a spool's remaining filament from a scale reading. Pass the gross weight of the whole spool in grams; " +
			"Spoolman subtracts the empty-spool weight (set it with update_spool spool_weight_g if the result looks wrong).",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a measureArgs) (*mcp.CallToolResult, spoolRow, error) {
		if a.Weight <= 0 {
			return nil, spoolRow{}, fmt.Errorf("gross_weight_g must be above zero")
		}
		return putSpool(ctx, d, "PUT", "/api/v1/spool/"+itoa(a.SpoolID)+"/measure", map[string]float64{"weight": a.Weight})
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_spool",
		Description: "Change a spool's location, comment, lot, price, weights, archived flag, or which filament it belongs to. Only the fields you pass change. " +
			"filament_id moves the spool to another filament, for example from \"Black\" to \"PLA Matte Black\" (list_filaments for ids). " +
			"That is sent as its own PATCH containing only filament_id. extra is never sent, so this tool cannot overwrite Bambuddy's AMS tag (extra.tag), whatever a given Spoolman version does with extra on a PATCH. " +
			"The spool is read back afterwards and extra is compared; extra_tag_intact and alert say whether the tag survived. If alert is set, tell the user straight away. " +
			"Bambuddy may recreate or rename filaments on its next AMS sync, so re-check the spool's filament after one. " +
			"This tool cannot write extra at all. Archiving removes a spool from stock but deletes nothing.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a updateArgs) (*mcp.CallToolResult, updateSpoolResult, error) {
		body := map[string]any{}
		if a.Location != nil {
			body["location"] = *a.Location
		}
		if a.Comment != nil {
			body["comment"] = *a.Comment
		}
		if a.LotNr != nil {
			body["lot_nr"] = *a.LotNr
		}
		if a.Price != nil {
			body["price"] = *a.Price
		}
		if a.Archived != nil {
			body["archived"] = *a.Archived
		}
		if a.Remaining != nil {
			body["remaining_weight"] = *a.Remaining
		}
		if a.Used != nil {
			body["used_weight"] = *a.Used
		}
		if a.Initial != nil {
			body["initial_weight"] = *a.Initial
		}
		if a.Empty != nil {
			body["spool_weight"] = *a.Empty
		}
		if len(body) == 0 && a.FilamentID == nil {
			return nil, updateSpoolResult{}, fmt.Errorf("nothing to change")
		}
		if a.Remaining != nil && a.Used != nil {
			return nil, updateSpoolResult{}, fmt.Errorf("give remaining_weight_g or used_weight_g, not both")
		}
		if a.FilamentID != nil {
			return moveSpool(ctx, d, a.SpoolID, *a.FilamentID, body)
		}
		var s api.Spool
		if err := d.Spoolman.Send(ctx, "PATCH", "/api/v1/spool/"+itoa(a.SpoolID), body, &s); err != nil {
			return nil, updateSpoolResult{}, toolError(err)
		}
		return nil, updateSpoolResult{SpoolView: plan.ViewOf(s)}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_spool",
		Description: "Add a spool of an existing filament to Spoolman. A spool made here has no AMS tag, so Bambuddy does not know it; " +
			"spools Bambuddy creates itself when it sees a new Bambu RFID spool. Use list_filaments for the filament id.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a createArgs) (*mcp.CallToolResult, spoolRow, error) {
		if a.FilamentID <= 0 {
			return nil, spoolRow{}, fmt.Errorf("filament_id is required")
		}
		body := map[string]any{"filament_id": a.FilamentID}
		for k, v := range map[string]*float64{"initial_weight": a.Initial, "remaining_weight": a.Remaining, "spool_weight": a.Empty, "price": a.Price} {
			if v != nil {
				body[k] = *v
			}
		}
		for k, v := range map[string]string{"location": a.Location, "lot_nr": a.LotNr, "comment": a.Comment} {
			if v != "" {
				body[k] = v
			}
		}
		return putSpool(ctx, d, "POST", "/api/v1/spool", body)
	})
}

func deltaOf(r spoolRow) float64 {
	if r.ColorDeltaE == nil {
		return 999
	}
	return *r.ColorDeltaE
}

func putSpool(ctx context.Context, d *Deps, method, path string, body any) (*mcp.CallToolResult, spoolRow, error) {
	var s api.Spool
	if err := d.Spoolman.Send(ctx, method, path, body, &s); err != nil {
		return nil, spoolRow{}, toolError(err)
	}
	return nil, spoolRow{SpoolView: plan.ViewOf(s)}, nil
}

type updateSpoolResult struct {
	plan.SpoolView
	MovedFrom      string   `json:"moved_from,omitempty"`
	ExtraTagIntact *bool    `json:"extra_tag_intact,omitempty"`
	TagBefore      string   `json:"extra_tag_before,omitempty"`
	TagAfter       string   `json:"extra_tag_after,omitempty"`
	Alert          string   `json:"alert,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
}

// moveSpool puts a spool on another filament. The filament change goes in a
// PATCH of its own with nothing else in it, any other fields follow in a
// second PATCH, and the spool is then read back to confirm extra is unchanged.
func moveSpool(ctx context.Context, d *Deps, spoolID, filamentID int, other map[string]any) (*mcp.CallToolResult, updateSpoolResult, error) {
	var res updateSpoolResult
	if filamentID <= 0 {
		return nil, res, fmt.Errorf("filament_id must be a Spoolman filament id")
	}
	var before api.Spool
	if _, err := d.Spoolman.Get(ctx, "/api/v1/spool/"+itoa(spoolID), nil, &before); err != nil {
		return nil, res, toolError(err)
	}
	if before.Filament.ID == filamentID {
		return nil, res, fmt.Errorf("spool #%d is already on filament #%d", spoolID, filamentID)
	}
	var target api.Filament
	if _, err := d.Spoolman.Get(ctx, "/api/v1/filament/"+itoa(filamentID), nil, &target); err != nil {
		return nil, res, toolError(err)
	}
	if !strings.EqualFold(before.Filament.Material, target.Material) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("material changes from %s to %s; stock_for_print and print_file match on material first", before.Filament.Material, target.Material))
	}
	if de, ok := plan.DeltaE(before.Filament.ColorHex, target.ColorHex); ok && de > 5 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("colour changes from #%s to #%s", plan.NormHex(before.Filament.ColorHex), plan.NormHex(target.ColorHex)))
	}

	// Only filament_id. Never extra.
	if err := d.Spoolman.Send(ctx, "PATCH", "/api/v1/spool/"+itoa(spoolID), map[string]any{"filament_id": filamentID}, nil); err != nil {
		return nil, res, toolError(err)
	}
	if len(other) > 0 {
		if err := d.Spoolman.Send(ctx, "PATCH", "/api/v1/spool/"+itoa(spoolID), other, nil); err != nil {
			return nil, res, fmt.Errorf("the spool moved to filament #%d but the other changes failed: %w", filamentID, toolError(err))
		}
	}

	var after api.Spool
	if _, err := d.Spoolman.Get(ctx, "/api/v1/spool/"+itoa(spoolID), nil, &after); err != nil {
		return nil, res, fmt.Errorf("the spool moved to filament #%d but could not be read back to check extra.tag: %w", filamentID, toolError(err))
	}
	intact, alert := plan.CompareExtra(before.Extra, after.Extra)
	res.SpoolView = plan.ViewOf(after)
	res.MovedFrom = fmt.Sprintf("#%d %s", before.Filament.ID, before.Filament.Name)
	res.ExtraTagIntact = &intact
	res.TagBefore, res.TagAfter, res.Alert = plan.UnquoteTag(before.Extra["tag"]), plan.UnquoteTag(after.Extra["tag"]), alert
	if after.Filament.ID != filamentID {
		res.Warnings = append(res.Warnings, fmt.Sprintf("the spool reads back on filament #%d, not #%d", after.Filament.ID, filamentID))
	}
	res.Warnings = append(res.Warnings, "Bambuddy may recreate or rename filaments on its next AMS sync; re-check this spool's filament afterwards")
	return nil, res, nil
}
