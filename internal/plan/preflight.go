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
	// BackupSlots are the other loaded trays with the same filament, and
	// GroupRemainingG is what the whole group has left. Both are empty when the
	// tray has no backup.
	BackupSlots     []int   `json:"backup_slots,omitempty"`
	GroupRemainingG float64 `json:"group_remaining_g,omitempty"`
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
	// MappingReasons says why each tray was chosen when the mapping is "auto".
	MappingReasons []string `json:"mapping_reasons,omitempty"`
	// Groups lists the mapped trays that have a same-filament backup tray.
	Groups []TrayGroup `json:"groups,omitempty"`
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
		mapping, unmatched, r.MappingReasons = autoMapExplained(in.Needs, trays, in.Lookup)
		r.MappingSource = "auto"
		for _, n := range unmatched {
			refuse("no loaded AMS tray holds %s for filament slot %d (#%s, %.0f g); load it or pass ams_mapping", n.Type, n.SlotID, NormHex(n.Color), n.Grams)
		}
		if len(unmatched) > 0 {
			return
		}
	}
	r.AMSMapping = mapping
	r.Groups = groupsOf(mapping, trays, in.Lookup)

	// A tray feeding two filaments is drawn on twice, and so is its group of
	// same-filament trays, so grams add up per group (keyed by its first slot).
	demand := map[int]float64{}
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
		g := groupOf(slot, trays, in.Lookup)
		demand[g.Slots[0]] += n.Grams
		need := demand[g.Slots[0]]
		sc.SpoolID, sc.RemainingG = spool.ID, spool.RemainingG
		sc.LeftAfterG = round1(g.TotalG - need)
		var backups []string
		for _, s := range g.Slots {
			if s == slot {
				continue
			}
			sc.BackupSlots = append(sc.BackupSlots, s)
			bt := trays[s]
			if bs, _, ok := in.Lookup.For(bt.amsID, bt.tray); ok {
				backups = append(backups, fmt.Sprintf("AMS slot %d (spool #%d, %.0f g)", s, bs.ID, bs.RemainingG))
			}
		}
		if len(sc.BackupSlots) > 0 {
			sc.GroupRemainingG = g.TotalG
		}
		short := need - g.TotalG
		switch {
		case short > need*shortfallTolerance && len(backups) == 0:
			refuse("spool #%d (%s %s) has %.0f g but filament slot %d needs %.0f g%s", spool.ID, spool.Vendor, spool.Name, spool.RemainingG, n.SlotID, need, unlinkedNote(g))
		case short > need*shortfallTolerance:
			refuse("spool #%d (%s %s) and its backup tray(s) at AMS slot %s have %.0f g between them but filament slot %d needs %.0f g%s", spool.ID, spool.Vendor, spool.Name, slotList(g.Slots), g.TotalG, n.SlotID, need, unlinkedNote(g))
		case short > 0:
			warn("spool #%d has %.0f g and slot %d needs %.0f g; short by %.0f g, within 10%%, so it may run out near the end", spool.ID, g.TotalG, n.SlotID, need, short)
		case spool.RemainingG < need && len(backups) > 0:
			warn("spool #%d has %.0f g but filament slot %d needs %.0f g; %s holds the same filament, so the AMS has to switch over to it mid-print", spool.ID, spool.RemainingG, n.SlotID, need, strings.Join(backups, " and "))
		case g.TotalG-need < need*shortfallTolerance:
			warn("spool #%d has %.0f g and slot %d needs %.0f g; only %.0f g to spare", spool.ID, g.TotalG, n.SlotID, need, g.TotalG-need)
		}
		r.Slots = append(r.Slots, sc)
	}
}

// unlinkedNote says which trays in a group add no grams to its total.
func unlinkedNote(g TrayGroup) string {
	if len(g.UnlinkedSlots) == 0 {
		return ""
	}
	return fmt.Sprintf(" (AMS slot %s has no linked spool, so its grams are not counted)", slotList(g.UnlinkedSlots))
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
// count as equal, and then a tray whose group of same-filament trays has the
// grams wins, then one not already used, then the tray with the least filament
// left (so a nearly empty spool is used up before the AMS moves on to its
// backup), then the lower slot. A tray with no linked spool never counts as
// the nearly empty one; it ranks after the linked trays, by the AMS remaining
// percent. Biggest filaments choose first. The mapping has -1 for file slots
// that need nothing. Needs with no loaded tray of their material come back
// unmatched.
func autoMap(needs []Need, trays map[int]loadedTray, lookup *SpoolLookup) (mapping []int, unmatched []Need) {
	mapping, unmatched, _ = autoMapExplained(needs, trays, lookup)
	return mapping, unmatched
}

// autoMapExplained is autoMap plus a line for each chosen tray saying why.
func autoMapExplained(needs []Need, trays map[int]loadedTray, lookup *SpoolLookup) (mapping []int, unmatched []Need, reasons []string) {
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
			slot     int
			key      float64
			delta    float64
			short    bool
			used     bool
			linked   bool
			rem      float64
			pct      float64 // AMS remaining percent; -1 when unknown
			groupTot float64
			nBackup  int
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
			c := cand{slot: slot, key: d, delta: d, used: used[slot], pct: -1}
			if d <= 5 {
				c.key = 0
			}
			g := groupOf(slot, trays, lookup)
			c.groupTot, c.nBackup, c.short = g.TotalG, len(g.Slots)-1, g.TotalG < n.Grams
			if sp, _, linked := lookup.For(t.amsID, t.tray); linked {
				c.linked, c.rem = true, sp.RemainingG
			} else if t.tray.Remain >= 0 {
				c.pct = float64(t.tray.Remain)
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
			case a.linked != b.linked:
				return a.linked
			case a.linked && a.rem != b.rem:
				return a.rem < b.rem
			case !a.linked && a.pct != b.pct:
				// Unknown (-1) goes last.
				return b.pct < 0 || (a.pct >= 0 && a.pct < b.pct)
			}
			return a.slot < b.slot
		})
		best := cs[0]
		mapping[n.SlotID-1] = best.slot
		used[best.slot] = true

		var tied, unlinkedTied []int
		for _, c := range cs {
			if c.key == best.key && c.short == best.short && c.used == best.used {
				tied = append(tied, c.slot)
				if !c.linked {
					unlinkedTied = append(unlinkedTied, c.slot)
				}
			}
		}
		sort.Ints(tied)
		sort.Ints(unlinkedTied)
		why := fmt.Sprintf("filament slot %d (%s #%s, %.0f g): AMS slot %d, colour %s (ΔE %.0f)", n.SlotID, n.Type, NormHex(n.Color), n.Grams, best.slot, ColourClass(best.delta, best.delta < 500), best.delta)
		switch {
		case len(tied) > 1 && best.linked && len(unlinkedTied) > 0:
			why += fmt.Sprintf("; AMS slots %s match equally but AMS slot %s has no linked spool, so only linked trays were compared", slotList(tied), slotList(unlinkedTied))
		case len(tied) > 1 && best.linked:
			why += fmt.Sprintf("; AMS slots %s match equally, chose the one with the least filament left (%.0f g)", slotList(tied), best.rem)
		case len(tied) > 1:
			why += fmt.Sprintf("; AMS slots %s match equally and none has a linked spool, so the AMS remaining percent decided", slotList(tied))
		}
		if best.nBackup > 0 {
			why += fmt.Sprintf("; its group of %d trays holds %.0f g", best.nBackup+1, best.groupTot)
		}
		reasons = append(reasons, why)
	}
	sort.SliceStable(unmatched, func(i, j int) bool { return unmatched[i].SlotID < unmatched[j].SlotID })
	return mapping, unmatched, reasons
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
