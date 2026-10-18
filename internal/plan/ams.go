// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"fmt"
	"math"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// GlobalSlot is the AMS slot number used in ams_mapping. Standard AMS units
// (ids 0 to 3) hold four trays each; an AMS HT uses its own unit id.
func GlobalSlot(amsID, trayID int) int {
	if amsID < 4 {
		return amsID*4 + trayID
	}
	return amsID
}

// PairOf is the inverse of GlobalSlot for the loaded-tray range. ok is false
// for the external spool (254) and anything else outside the AMS.
func PairOf(slot int) (amsID, trayID int, ok bool) {
	switch {
	case slot >= 0 && slot < 16:
		return slot / 4, slot % 4, true
	case slot >= 128 && slot < 136:
		return slot, 0, true
	}
	return 0, 0, false
}

// TrayCheck compares one loaded AMS tray with the spool it maps to.
type TrayCheck struct {
	Slot        int      `json:"slot"`
	AMSID       int      `json:"ams_id"`
	TrayID      int      `json:"tray_id"`
	TrayType    string   `json:"tray_type"`
	TrayColor   string   `json:"tray_color"`
	TrayRemain  *float64 `json:"tray_remain_pct,omitempty"`
	SpoolID     int      `json:"spool_id,omitempty"`
	LinkedBy    string   `json:"linked_by,omitempty"`
	Spool       string   `json:"spool,omitempty"`
	SpoolColor  string   `json:"spool_color,omitempty"`
	SpoolRemain float64  `json:"spool_remaining_g,omitempty"`
	SpoolPct    *float64 `json:"spool_remaining_pct,omitempty"`
	Flags       []string `json:"flags,omitempty"`
}

// Tolerances for flagging a mismatch.
const (
	colourFlagDeltaE = 15.0
	remainFlagPoints = 15.0
)

// SpoolLookup finds the spool for an AMS tray: Bambuddy's slot assignment
// first, then the spool whose extra.tag equals the tray's tray_uuid.
type SpoolLookup struct {
	Assign map[[2]int]int
	Spools map[int]SpoolView
	byTag  map[string]SpoolView
}

// NewLookup indexes assignments and spools.
func NewLookup(printerID int, assigns []api.SlotAssignment, spools []SpoolView) *SpoolLookup {
	l := &SpoolLookup{Assign: map[[2]int]int{}, Spools: map[int]SpoolView{}, byTag: map[string]SpoolView{}}
	for _, a := range assigns {
		if a.PrinterID == printerID {
			l.Assign[[2]int{a.AMSID, a.TrayID}] = a.SpoolID
		}
	}
	for _, s := range spools {
		l.Spools[s.ID] = s
		if s.Tag != "" {
			l.byTag[s.Tag] = s
		}
	}
	return l
}

// For returns the spool for a tray and how it was found.
func (l *SpoolLookup) For(amsID int, t api.Tray) (SpoolView, string, bool) {
	if id, ok := l.Assign[[2]int{amsID, t.ID}]; ok {
		if s, ok := l.Spools[id]; ok {
			return s, "slot assignment", true
		}
	}
	if t.TrayUUID != "" {
		if s, ok := l.byTag[UnquoteTag(t.TrayUUID)]; ok {
			return s, "rfid tag", true
		}
	}
	return SpoolView{}, "", false
}

// CheckAMS compares every loaded tray with its Spoolman spool and flags
// mismatches. Empty trays are skipped.
func CheckAMS(st api.PrinterStatus, l *SpoolLookup) []TrayCheck {
	var out []TrayCheck
	for _, u := range st.AMS {
		for _, t := range u.Trays {
			if !t.Exists && t.Type == "" {
				continue
			}
			c := TrayCheck{
				Slot: GlobalSlot(u.ID, t.ID), AMSID: u.ID, TrayID: t.ID,
				TrayType: t.Type, TrayColor: NormHex(t.Color),
			}
			rfid := t.TagUID != "" && t.Remain >= 0
			if rfid {
				r := float64(t.Remain)
				c.TrayRemain = &r
			}
			s, how, ok := l.For(u.ID, t)
			if !ok {
				c.Flags = append(c.Flags, "no Spoolman spool is linked to this tray")
				out = append(out, c)
				continue
			}
			c.SpoolID, c.LinkedBy, c.Spool = s.ID, how, s.Vendor+" "+s.Name
			c.SpoolColor, c.SpoolRemain = s.ColorHex, s.RemainingG
			if s.Archived {
				c.Flags = append(c.Flags, "linked spool is archived")
			}
			if t.Type != "" && s.Material != "" && !equalFold(t.Type, s.Material) {
				c.Flags = append(c.Flags, fmt.Sprintf("material differs: tray %s, spool %s", t.Type, s.Material))
			}
			if d, ok := DeltaE(c.TrayColor, s.ColorHex); ok && d > colourFlagDeltaE {
				c.Flags = append(c.Flags, fmt.Sprintf("colour differs: tray #%s, spool #%s (ΔE %.0f)", c.TrayColor, s.ColorHex, d))
			}
			if s.InitialG > 0 {
				p := round1(s.RemainingG / s.InitialG * 100)
				c.SpoolPct = &p
				if rfid && math.Abs(float64(t.Remain)-p) > remainFlagPoints {
					c.Flags = append(c.Flags, fmt.Sprintf("remaining differs: AMS %.0f%%, Spoolman %.0f%% (%.0f g)", float64(t.Remain), p, s.RemainingG))
				}
			}
			out = append(out, c)
		}
	}
	return out
}
