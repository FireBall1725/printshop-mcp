// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// PreflightInput is everything the print gate looks at.
type PreflightInput struct {
	Status api.PrinterStatus
	File   api.LibraryFile
	Needs  []Need
	// Mapping is the caller's ams_mapping: index is filament slot_id-1, value
	// is the AMS slot.
	Mapping []int
	UseAMS  bool
	Force   bool
	// Model is the printer model a file must be sliced for.
	Model  string
	Lookup *SpoolLookup
	// HMSText is the published description per full_code, if it could be fetched.
	HMSText map[string]string
}

// SlotCheck is the filament verdict for one file slot.
type SlotCheck struct {
	SlotID      int     `json:"slot_id"`
	NeedG       float64 `json:"need_g"`
	NeedColor   string  `json:"need_color,omitempty"`
	AMSSlot     int     `json:"ams_slot"`
	TrayType    string  `json:"tray_type,omitempty"`
	TrayColor   string  `json:"tray_color,omitempty"`
	ColorDeltaE float64 `json:"color_delta_e"`
	ColorMatch  string  `json:"color_match,omitempty"`
	SpoolID     int     `json:"spool_id,omitempty"`
	RemainingG  float64 `json:"remaining_g,omitempty"`
	LeftAfterG  float64 `json:"left_after_g,omitempty"`
}

// PreflightResult says whether a print may start and why not.
type PreflightResult struct {
	Allowed      bool         `json:"allowed"`
	PrinterState string       `json:"printer_state"`
	PrinterDoing string       `json:"printer_doing"`
	Refusals     []string     `json:"refusals,omitempty"`
	Warnings     []string     `json:"warnings,omitempty"`
	HMS          []HMSSummary `json:"hms_errors,omitempty"`
	Slots        []SlotCheck  `json:"slots,omitempty"`
	// AMSMapping is the mapping that would be sent. MappingSource is "explicit"
	// when the caller gave one and "auto" when it was chosen here from the
	// file's material and colour against the loaded trays.
	AMSMapping    []int  `json:"ams_mapping,omitempty"`
	MappingSource string `json:"mapping_source,omitempty"`
}

// shortfallTolerance is the share of a slot's grams a spool may be short, or
// have to spare, before the gate stops warning and acts.
const shortfallTolerance = 0.10

// Preflight applies the safety rules. It never talks to the network.
func Preflight(in PreflightInput) PreflightResult {
	if in.Lookup == nil {
		in.Lookup = NewLookup(0, nil, nil)
	}
	st := in.Status
	state := strings.ToUpper(st.State)
	r := PreflightResult{PrinterState: state, PrinterDoing: Doing(st)}
	refuse := func(f string, a ...any) { r.Refusals = append(r.Refusals, fmt.Sprintf(f, a...)) }
	warn := func(f string, a ...any) { r.Warnings = append(r.Warnings, fmt.Sprintf(f, a...)) }

	// Printer.
	if !st.Connected {
		refuse("Bambuddy reports the printer as disconnected")
	}
	if state != "IDLE" && state != "FINISH" {
		refuse("printer state is %s; a print only starts from IDLE or FINISH", state)
	}
	if len(st.HMSErrors) > 0 {
		r.HMS = HMSSummaries(st.HMSErrors, in.HMSText)
		lines := make([]string, len(r.HMS))
		worst := 9
		for i, h := range r.HMS {
			lines[i] = HMSLine(h)
			if h.Level > 0 && h.Level < worst {
				worst = h.Level
			}
		}
		switch {
		case in.Force:
			warn("%d HMS error(s) present; continuing because force is true: %s", len(r.HMS), strings.Join(lines, "; "))
		case worst <= 2:
			refuse("%d HMS error(s) present: %s. Level 1 and 2 errors stop or pause a print; pass force: true only if the user says so", len(r.HMS), strings.Join(lines, "; "))
		default:
			refuse("%d HMS error(s) present: %s. These are notices rather than stops; pass force: true to start anyway", len(r.HMS), strings.Join(lines, "; "))
		}
	}
	if st.DoorOpen {
		warn("the printer door is open")
	}
	if st.AwaitingPlateClear {
		warn("Bambuddy is waiting for the build plate to be marked clear")
	}

	// File.
	f := in.File
	if f.FileType != "gcode.3mf" {
		refuse("%q is a %s file; only sliced .gcode.3mf files can be printed", f.Filename, orNone(f.FileType))
	}
	model := ""
	if f.SlicedForModel != nil {
		model = *f.SlicedForModel
	} else if f.Metadata != nil {
		model = f.Metadata.SlicedForModel
	}
	switch {
	case model == "":
		refuse("%q does not say which printer it was sliced for; expected %s", f.Filename, in.Model)
	case !strings.EqualFold(model, in.Model):
		refuse("%q was sliced for %s, not %s", f.Filename, model, in.Model)
	}
	if f.Metadata != nil && f.Metadata.NozzleDiameter > 0 && len(st.Nozzles) > 0 && st.Nozzles[0].Diameter > 0 &&
		f.Metadata.NozzleDiameter != st.Nozzles[0].Diameter {
		warn("file was sliced for a %.2f mm nozzle; the printer has %.2f mm", float64(f.Metadata.NozzleDiameter), float64(st.Nozzles[0].Diameter))
	}

	// Filament.
	if len(in.Needs) == 0 {
		refuse("no filament requirements could be read from the file, so stock cannot be checked")
		r.Allowed = len(r.Refusals) == 0
		return r
	}
	if !in.UseAMS {
		warn("use_ams is false; filament on the external spool is not checked against stock")
		r.Allowed = len(r.Refusals) == 0
		return r
	}
	checkFilament(&r, in, refuse, warn)
	r.Allowed = len(r.Refusals) == 0
	return r
}

func checkFilament(r *PreflightResult, in PreflightInput, refuse, warn func(string, ...any)) {
	trays := loadedTrays(in.Status)
	mapping := in.Mapping
	r.MappingSource = "explicit"
	if len(mapping) == 0 {
		var unmatched []Need
		mapping, unmatched = autoMap(in.Needs, trays, in.Lookup)
		r.MappingSource = "auto"
		for _, n := range unmatched {
			refuse("no loaded AMS tray holds %s for filament slot %d (#%s, %.0f g); load it or pass ams_mapping", n.Type, n.SlotID, NormHex(n.Color), n.Grams)
		}
		if len(unmatched) > 0 {
			return
		}
	}
	r.AMSMapping = mapping

	// A tray feeding two filaments is drawn on twice, so grams add up per spool.
	committed := map[int]float64{}
	for _, n := range in.Needs {
		if n.SlotID < 1 || n.SlotID > len(mapping) {
			refuse("ams_mapping has no entry for filament slot %d", n.SlotID)
			continue
		}
		slot := mapping[n.SlotID-1]
		sc := SlotCheck{SlotID: n.SlotID, NeedG: n.Grams, NeedColor: NormHex(n.Color), AMSSlot: slot}
		t, ok := trays[slot]
		if !ok {
			refuse("filament slot %d maps to AMS slot %d, which has nothing loaded", n.SlotID, slot)
			r.Slots = append(r.Slots, sc)
			continue
		}
		sc.TrayType, sc.TrayColor = t.tray.Type, NormHex(t.tray.Color)
		d, dok := DeltaE(n.Color, t.tray.Color)
		sc.ColorDeltaE, sc.ColorMatch = round1(d), ColourClass(d, dok)
		if dok && d > colourFlagDeltaE {
			warn("filament slot %d is #%s in the file but AMS slot %d holds #%s (%s, ΔE %.0f)%s", n.SlotID, sc.NeedColor, slot, sc.TrayColor, sc.ColorMatch, d, overrideHint(r.MappingSource))
		}
		if !equalFold(t.tray.Type, n.Type) {
			refuse("filament slot %d needs %s but AMS slot %d holds %s", n.SlotID, n.Type, slot, t.tray.Type)
		}
		spool, _, linked := in.Lookup.For(t.amsID, t.tray)
		if !linked {
			warn("AMS slot %d has no linked Spoolman spool, so %.0f g for slot %d is unchecked", slot, n.Grams, n.SlotID)
			r.Slots = append(r.Slots, sc)
			continue
		}
		committed[spool.ID] += n.Grams
		need := committed[spool.ID]
		sc.SpoolID, sc.RemainingG = spool.ID, spool.RemainingG
		sc.LeftAfterG = round1(spool.RemainingG - need)
		short := need - spool.RemainingG
		switch {
		case short > need*shortfallTolerance:
			refuse("spool #%d (%s %s) has %.0f g but filament slot %d needs %.0f g", spool.ID, spool.Vendor, spool.Name, spool.RemainingG, n.SlotID, need)
		case short > 0:
			warn("spool #%d has %.0f g and slot %d needs %.0f g; short by %.0f g, within 10%%, so it may run out near the end", spool.ID, spool.RemainingG, n.SlotID, need, short)
		case spool.RemainingG-need < need*shortfallTolerance:
			warn("spool #%d has %.0f g and slot %d needs %.0f g; only %.0f g to spare", spool.ID, spool.RemainingG, n.SlotID, need, spool.RemainingG-need)
		}
		r.Slots = append(r.Slots, sc)
	}
}

func overrideHint(source string) string {
	if source == "auto" {
		return "; pass ams_mapping to choose a different tray"
	}
	return ""
}

type loadedTray struct {
	amsID int
	tray  api.Tray
}

func loadedTrays(st api.PrinterStatus) map[int]loadedTray {
	m := map[int]loadedTray{}
	for _, u := range st.AMS {
		for _, t := range u.Trays {
			if t.Exists && t.Type != "" {
				m[GlobalSlot(u.ID, t.ID)] = loadedTray{u.ID, t}
			}
		}
	}
	return m
}

// autoMap picks an AMS slot for each filament: loaded trays of the same
// material only, nearest colour first. Colours within a few ΔE of each other
// count as equal, and then a tray whose linked spool has the grams wins, then
// one not already used, then the spool with more filament, then the lower
// slot. Biggest filaments choose first. The mapping has -1 for file slots
// that need nothing. Needs with no loaded tray of their material come back
// unmatched.
func autoMap(needs []Need, trays map[int]loadedTray, lookup *SpoolLookup) (mapping []int, unmatched []Need) {
	top := 0
	for _, n := range needs {
		if n.SlotID > top {
			top = n.SlotID
		}
	}
	mapping = make([]int, top)
	for i := range mapping {
		mapping[i] = -1
	}
	order := append([]Need(nil), needs...)
	sort.SliceStable(order, func(i, j int) bool { return order[i].Grams > order[j].Grams })
	used := map[int]bool{}
	for _, n := range order {
		type cand struct {
			slot  int
			key   float64
			short bool
			used  bool
			rem   float64
		}
		var cs []cand
		for slot, t := range trays {
			if !equalFold(t.tray.Type, n.Type) {
				continue
			}
			d, ok := DeltaE(n.Color, t.tray.Color)
			if !ok {
				d = 500
			}
			if d <= 5 {
				d = 0
			}
			c := cand{slot: slot, key: d, used: used[slot]}
			if sp, _, linked := lookup.For(t.amsID, t.tray); linked {
				c.rem, c.short = sp.RemainingG, sp.RemainingG < n.Grams
			}
			cs = append(cs, c)
		}
		if len(cs) == 0 || n.SlotID < 1 {
			unmatched = append(unmatched, n)
			continue
		}
		sort.Slice(cs, func(i, j int) bool {
			a, b := cs[i], cs[j]
			switch {
			case a.key != b.key:
				return a.key < b.key
			case a.short != b.short:
				return !a.short
			case a.used != b.used:
				return !a.used
			case a.rem != b.rem:
				return a.rem > b.rem
			}
			return a.slot < b.slot
		})
		mapping[n.SlotID-1] = cs[0].slot
		used[cs[0].slot] = true
	}
	sort.SliceStable(unmatched, func(i, j int) bool { return unmatched[i].SlotID < unmatched[j].SlotID })
	return mapping, unmatched
}

// Doing describes what the printer is up to, for the result text.
func Doing(st api.PrinterStatus) string {
	state := strings.ToUpper(st.State)
	job := st.SubtaskName
	if job == "" {
		job = st.CurrentPrint
	}
	switch state {
	case "RUNNING", "PAUSE":
		verb := "printing"
		if state == "PAUSE" {
			verb = "paused on"
		}
		return fmt.Sprintf("%s %q, %.0f%%, layer %d of %d", verb, job, float64(st.Progress), st.LayerNum, st.TotalLayers)
	case "FINISH":
		return fmt.Sprintf("finished %q", job)
	case "FAILED":
		return fmt.Sprintf("failed %q", job)
	case "IDLE":
		return "idle"
	}
	return strings.ToLower(state)
}

func orNone(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
