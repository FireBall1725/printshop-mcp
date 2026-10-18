// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultDiameterMM = 1.75
	defaultWeightG    = 1000.0
	syncNote          = "Bambuddy may recreate or rename filaments on its next AMS sync; re-check list_filaments after one."
)

type createFilamentArgs struct {
	VendorID int      `json:"vendor_id,omitempty" jsonschema:"Spoolman vendor id. Give this or vendor."`
	Vendor   string   `json:"vendor,omitempty" jsonschema:"Vendor name, such as Bambu Lab. An existing vendor with the same name (ignoring case) is reused; otherwise one is created."`
	Name     string   `json:"name" jsonschema:"Filament name that tells it apart within the vendor, such as PLA Matte Black."`
	Material string   `json:"material" jsonschema:"Plain material, such as PLA. Keep product lines like Matte or Basic out of it and in the name."`
	ColorHex string   `json:"color_hex" jsonschema:"Colour as hex, such as 000000 or #FF69B4."`
	Diameter *float64 `json:"diameter_mm,omitempty" jsonschema:"Defaults to 1.75."`
	Weight   *float64 `json:"weight_g,omitempty" jsonschema:"Net weight of a full spool. Defaults to 1000."`
	Density  *float64 `json:"density_g_cm3,omitempty" jsonschema:"Spoolman requires it. Defaults to a typical value for PLA, PETG, ABS, ASA, TPU, PC, PA and PVA; any other material needs it given."`
	NozzleC  *int     `json:"nozzle_temp_c,omitempty" jsonschema:"Print temperature override."`
	BedC     *int     `json:"bed_temp_c,omitempty" jsonschema:"Bed temperature override."`
}

type updateFilamentArgs struct {
	FilamentID int      `json:"filament_id" jsonschema:"Spoolman filament id from list_filaments."`
	Name       *string  `json:"name,omitempty"`
	ColorHex   *string  `json:"color_hex,omitempty" jsonschema:"Hex, such as 000000."`
	Material   *string  `json:"material,omitempty" jsonschema:"Plain material, such as PLA."`
	Weight     *float64 `json:"weight_g,omitempty" jsonschema:"Net weight of a full spool."`
	NozzleC    *int     `json:"nozzle_temp_c,omitempty"`
	BedC       *int     `json:"bed_temp_c,omitempty"`
}

type filamentResult struct {
	Created       bool         `json:"created,omitempty"`
	ExistingID    int          `json:"existing_id,omitempty"`
	VendorCreated bool         `json:"vendor_created,omitempty"`
	Filament      *filamentRow `json:"filament,omitempty"`
	DensityG      float64      `json:"density_g_cm3,omitempty"`
	SpoolsUsing   int          `json:"spools_using,omitempty"`
	TaggedSpools  int          `json:"spools_tracked_by_bambuddy,omitempty"`
	Message       string       `json:"message,omitempty"`
	Warnings      []string     `json:"warnings,omitempty"`
}

func rowOfFilament(f api.Filament) *filamentRow {
	vendor := ""
	if f.Vendor != nil {
		vendor = f.Vendor.Name
	}
	return &filamentRow{
		ID: f.ID, Vendor: vendor, Name: f.Name, Material: f.Material, ColorHex: plan.NormHex(f.ColorHex),
		WeightG: f.Weight, SpoolWeight: f.SpoolWeight, Diameter: f.Diameter, ExtruderC: f.ExtruderTemp, BedC: f.BedTemp,
	}
}

func checkTemps(nozzle, bed *int) error {
	if nozzle != nil && (*nozzle < 100 || *nozzle > 400) {
		return fmt.Errorf("nozzle_temp_c must be 100 to 400")
	}
	if bed != nil && (*bed < 0 || *bed > 150) {
		return fmt.Errorf("bed_temp_c must be 0 to 150")
	}
	return nil
}

func addFilamentTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "create_filament",
		Description: "Add a filament type to Spoolman: vendor (id or name), name, material, colour, diameter (1.75), net weight (1000 g) and optional temperatures. " +
			"A vendor whose name matches (ignoring case) is reused, and a new vendor is created only if none matches. " +
			"It refuses to create a second filament with the same vendor, name and material, and returns the existing id instead. " +
			"Keep the material plain (\"PLA\") and put the product line in the name (\"PLA Matte Black\"): stock_for_print and print_file match on material first, then nearest colour. " +
			"Move spools onto it with update_spool filament_id. " + syncNote,
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a createFilamentArgs) (*mcp.CallToolResult, filamentResult, error) {
		var res filamentResult
		name, material := strings.TrimSpace(a.Name), strings.TrimSpace(a.Material)
		hex := plan.NormHex(a.ColorHex)
		switch {
		case name == "" || material == "":
			return nil, res, fmt.Errorf("name and material are required")
		case hex == "":
			return nil, res, fmt.Errorf("color_hex must be a hex colour such as 000000")
		case (a.VendorID > 0) == (strings.TrimSpace(a.Vendor) != ""):
			return nil, res, fmt.Errorf("give exactly one of vendor_id and vendor")
		}
		if err := checkTemps(a.NozzleC, a.BedC); err != nil {
			return nil, res, err
		}
		diameter, weight := defaultDiameterMM, defaultWeightG
		if a.Diameter != nil {
			diameter = *a.Diameter
		}
		if a.Weight != nil {
			weight = *a.Weight
		}
		if diameter <= 0 || weight <= 0 {
			return nil, res, fmt.Errorf("diameter_mm and weight_g must be above zero")
		}
		density, ok := plan.DensityFor(material)
		if a.Density != nil {
			density, ok = *a.Density, *a.Density > 0
		}
		if !ok {
			return nil, res, fmt.Errorf("no typical density is known for %q; give density_g_cm3", material)
		}
		if w := plan.MaterialWarning(material); w != "" {
			res.Warnings = append(res.Warnings, w)
		}

		var vendors []api.Vendor
		if _, err := d.Spoolman.Get(ctx, "/api/v1/vendor", nil, &vendors); err != nil {
			return nil, res, toolError(err)
		}
		var vendor api.Vendor
		if a.VendorID > 0 {
			found := false
			for _, v := range vendors {
				if v.ID == a.VendorID {
					vendor, found = v, true
				}
			}
			if !found {
				return nil, res, fmt.Errorf("there is no vendor with id %d (list_vendors)", a.VendorID)
			}
		} else if v, found := plan.FindVendor(vendors, a.Vendor); found {
			vendor = v
		}

		if vendor.ID != 0 {
			var all []api.Filament
			if _, err := d.Spoolman.Get(ctx, "/api/v1/filament", nil, &all); err != nil {
				return nil, res, toolError(err)
			}
			if dup, found := plan.FindDuplicate(all, vendor.ID, name, material); found {
				res.ExistingID, res.Filament = dup.ID, rowOfFilament(dup)
				res.Message = fmt.Sprintf("not created: %s %q (%s) already exists as filament #%d", vendor.Name, dup.Name, dup.Material, dup.ID)
				return nil, res, nil
			}
		} else {
			if err := d.Spoolman.Send(ctx, "POST", "/api/v1/vendor", map[string]any{"name": strings.TrimSpace(a.Vendor)}, &vendor); err != nil {
				return nil, res, toolError(err)
			}
			res.VendorCreated = true
		}

		body := map[string]any{
			"vendor_id": vendor.ID, "name": name, "material": material, "color_hex": hex,
			"density": density, "diameter": diameter, "weight": weight,
		}
		if a.NozzleC != nil {
			body["settings_extruder_temp"] = *a.NozzleC
		}
		if a.BedC != nil {
			body["settings_bed_temp"] = *a.BedC
		}
		var f api.Filament
		if err := d.Spoolman.Send(ctx, "POST", "/api/v1/filament", body, &f); err != nil {
			return nil, res, toolError(err)
		}
		res.Created, res.Filament, res.DensityG = true, rowOfFilament(f), density
		res.Message = fmt.Sprintf("created filament #%d", f.ID)
		if a.Density == nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("density %.2f g/cm3 is the typical value for %s; set density_g_cm3 if this product differs (Bambu matte PLA, for one, is heavier)", density, strings.ToUpper(material)))
		}
		res.Warnings = append(res.Warnings, syncNote)
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "update_filament",
		Description: "Change a filament type in Spoolman: name, colour, material, net weight or temperatures. Only the fields you pass change; extra is never sent. " +
			"This changes every spool on that filament, and the result says how many, and how many Bambuddy tracks by AMS tag. " +
			"Changing the material affects stock_for_print and print_file, which match on material first; keep it plain (\"PLA\"). " + syncNote,
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a updateFilamentArgs) (*mcp.CallToolResult, filamentResult, error) {
		var res filamentResult
		body := map[string]any{}
		if a.Name != nil {
			if strings.TrimSpace(*a.Name) == "" {
				return nil, res, fmt.Errorf("name cannot be empty")
			}
			body["name"] = strings.TrimSpace(*a.Name)
		}
		if a.ColorHex != nil {
			hex := plan.NormHex(*a.ColorHex)
			if hex == "" {
				return nil, res, fmt.Errorf("color_hex must be a hex colour such as 000000")
			}
			body["color_hex"] = hex
		}
		if a.Material != nil {
			m := strings.TrimSpace(*a.Material)
			if m == "" {
				return nil, res, fmt.Errorf("material cannot be empty")
			}
			body["material"] = m
			if w := plan.MaterialWarning(m); w != "" {
				res.Warnings = append(res.Warnings, w)
			}
		}
		if a.Weight != nil {
			if *a.Weight <= 0 {
				return nil, res, fmt.Errorf("weight_g must be above zero")
			}
			body["weight"] = *a.Weight
		}
		if err := checkTemps(a.NozzleC, a.BedC); err != nil {
			return nil, res, err
		}
		if a.NozzleC != nil {
			body["settings_extruder_temp"] = *a.NozzleC
		}
		if a.BedC != nil {
			body["settings_bed_temp"] = *a.BedC
		}
		if len(body) == 0 {
			return nil, res, fmt.Errorf("nothing to change")
		}

		var before api.Filament
		if _, err := d.Spoolman.Get(ctx, "/api/v1/filament/"+itoa(a.FilamentID), nil, &before); err != nil {
			return nil, res, toolError(err)
		}
		var f api.Filament
		if err := d.Spoolman.Send(ctx, "PATCH", "/api/v1/filament/"+itoa(a.FilamentID), body, &f); err != nil {
			return nil, res, toolError(err)
		}
		res.Filament, res.Message = rowOfFilament(f), fmt.Sprintf("updated filament #%d", f.ID)
		if all, err := allSpools(ctx, d); err == nil {
			for _, s := range spoolViews(all) {
				if s.FilamentID == f.ID {
					res.SpoolsUsing++
					if s.TagSet {
						res.TaggedSpools++
					}
				}
			}
		}
		if _, ok := body["material"]; ok && !strings.EqualFold(before.Material, f.Material) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("material changed from %s to %s; stock_for_print and print_file match on material first", before.Material, f.Material))
		}
		if res.TaggedSpools > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%d spool(s) on this filament are tracked by Bambuddy and may be renamed or moved by its next AMS sync", res.TaggedSpools))
		}
		res.Warnings = append(res.Warnings, syncNote)
		return nil, res, nil
	})
}
