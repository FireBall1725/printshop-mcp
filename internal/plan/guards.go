// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"fmt"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// Drying limits. The temperature range is Bambuddy's documented range; the
// hour cap is this server's own sanity limit.
const (
	MinDryTempC = 45
	MaxDryTempC = 85
	MaxDryHours = 24
)

func printing(state string) bool {
	s := strings.ToUpper(state)
	return s == "RUNNING" || s == "PAUSE"
}

// CheckSkip returns the reasons a skip request must not go ahead. Skipping
// cannot be undone, and skipping every object is a stop in disguise.
func CheckSkip(objs api.PrintObjects, ids []int) []string {
	var out []string
	if !objs.IsPrinting {
		return []string{"no print is running, so there is nothing to skip"}
	}
	if len(ids) == 0 {
		return []string{"object_ids is empty"}
	}
	byID := map[int]api.PrintObject{}
	live := 0
	for _, o := range objs.Objects {
		byID[o.ID] = o
		if !o.Skipped {
			live++
		}
	}
	seen := map[int]bool{}
	for _, id := range ids {
		o, ok := byID[id]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("object %d is not on this plate", id))
		case o.Skipped:
			out = append(out, fmt.Sprintf("object %d (%s) is already skipped", id, o.Name))
		case !seen[id]:
			live--
		}
		seen[id] = true
	}
	if len(out) == 0 && live <= 0 {
		out = append(out, "that would skip every remaining object; use print_stop for that, which needs its own yes")
	}
	return out
}

// CheckDrying returns the reasons an AMS drying start must not go ahead.
// A zero temp or hours means "use Bambuddy's default".
func CheckDrying(st api.PrinterStatus, amsID, tempC, hours int) []string {
	var out []string
	if !st.Connected {
		out = append(out, "Bambuddy reports the printer as disconnected")
	}
	if !st.SupportsDrying {
		out = append(out, "this printer does not report AMS drying support")
	}
	found := false
	for _, u := range st.AMS {
		if u.ID == amsID {
			found = true
		}
	}
	if !found {
		out = append(out, fmt.Sprintf("there is no AMS unit with id %d", amsID))
	}
	if printing(st.State) && !st.DryWhilePrinting {
		out = append(out, fmt.Sprintf("printer is %s and does not support drying while printing", strings.ToUpper(st.State)))
	}
	if tempC != 0 && (tempC < MinDryTempC || tempC > MaxDryTempC) {
		out = append(out, fmt.Sprintf("temp_c must be %d to %d", MinDryTempC, MaxDryTempC))
	}
	if hours != 0 && (hours < 1 || hours > MaxDryHours) {
		out = append(out, fmt.Sprintf("hours must be 1 to %d", MaxDryHours))
	}
	return out
}

// CheckAssign judges a request to link a Spoolman spool to an AMS slot.
// Mismatches warn rather than block, because the link is local to Bambuddy and
// easy to undo, but a wrong one makes usage land on the wrong spool.
func CheckAssign(st api.PrinterStatus, printerID, slot int, spool SpoolView, assigns []api.SlotAssignment) (refusals, warnings []string) {
	amsID, trayID, ok := PairOf(slot)
	if !ok {
		return []string{fmt.Sprintf("slot %d is not an AMS slot; slots come from printer_status (0 to 15, or an AMS HT unit id)", slot)}, nil
	}
	if spool.Archived {
		refusals = append(refusals, fmt.Sprintf("spool #%d is archived", spool.ID))
	}
	var tray *api.Tray
	for _, u := range st.AMS {
		if u.ID != amsID {
			continue
		}
		for i := range u.Trays {
			if u.Trays[i].ID == trayID {
				tray = &u.Trays[i]
			}
		}
	}
	switch {
	case tray == nil:
		refusals = append(refusals, fmt.Sprintf("the printer has no AMS slot %d", slot))
	case !tray.Exists && tray.Type == "":
		warnings = append(warnings, fmt.Sprintf("AMS slot %d is empty right now", slot))
	default:
		if tray.Type != "" && spool.Material != "" && !equalFold(tray.Type, spool.Material) {
			warnings = append(warnings, fmt.Sprintf("slot %d holds %s but spool #%d is %s", slot, tray.Type, spool.ID, spool.Material))
		}
		if d, ok := DeltaE(tray.Color, spool.ColorHex); ok && d > colourFlagDeltaE {
			warnings = append(warnings, fmt.Sprintf("colour differs: tray #%s, spool #%s (ΔE %.0f)", NormHex(tray.Color), spool.ColorHex, d))
		}
	}
	for _, a := range assigns {
		if a.PrinterID == printerID && a.AMSID == amsID && a.TrayID == trayID && a.SpoolID != spool.ID {
			warnings = append(warnings, fmt.Sprintf("slot %d is already linked to spool #%d", slot, a.SpoolID))
		}
		sameSlot := a.PrinterID == printerID && a.AMSID == amsID && a.TrayID == trayID
		if a.SpoolID == spool.ID && !sameSlot {
			warnings = append(warnings, fmt.Sprintf("spool #%d is already linked to AMS %d tray %d", spool.ID, a.AMSID, a.TrayID))
		}
	}
	return refusals, warnings
}
