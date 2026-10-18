// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"fmt"
	"sort"
	"strings"
)

// groupDeltaE is the largest colour distance between two trays that still
// counts as the same filament. It is the same cut as the "exact" class.
const groupDeltaE = 5.0

// TrayGroup is a set of loaded trays holding the same filament: the AMS moves
// to the next tray in the set by itself when one runs out.
type TrayGroup struct {
	Slots    []int `json:"ams_slots"`
	SpoolIDs []int `json:"spool_ids,omitempty"`
	// TotalG adds up the linked spools. Trays with no linked spool add nothing,
	// so UnlinkedSlots says what the total leaves out.
	TotalG        float64 `json:"total_g"`
	UnlinkedSlots []int   `json:"unlinked_slots,omitempty"`
}

// sameFilament reports whether the AMS would treat two trays as one filament:
// same material, same colour (or a very small distance), and the same product
// line and vendor wherever both are known.
func sameFilament(a, b loadedTray, lookup *SpoolLookup) bool {
	if !equalFold(a.tray.Type, b.tray.Type) {
		return false
	}
	d, ok := DeltaE(a.tray.Color, b.tray.Color)
	if !ok || d > groupDeltaE {
		return false
	}
	if a.tray.SubBrand != "" && b.tray.SubBrand != "" && !equalFold(a.tray.SubBrand, b.tray.SubBrand) {
		return false
	}
	sa, _, la := lookup.For(a.amsID, a.tray)
	sb, _, lb := lookup.For(b.amsID, b.tray)
	if la && lb && sa.Vendor != "" && sb.Vendor != "" && !equalFold(sa.Vendor, sb.Vendor) {
		return false
	}
	return true
}

// groupOf is the tray in slot plus every other loaded tray with the same
// filament, in slot order.
func groupOf(slot int, trays map[int]loadedTray, lookup *SpoolLookup) TrayGroup {
	self := trays[slot]
	g := TrayGroup{}
	for s, t := range trays {
		if s == slot || sameFilament(self, t, lookup) {
			g.Slots = append(g.Slots, s)
		}
	}
	sort.Ints(g.Slots)
	for _, s := range g.Slots {
		t := trays[s]
		if sp, _, ok := lookup.For(t.amsID, t.tray); ok {
			g.SpoolIDs = append(g.SpoolIDs, sp.ID)
			g.TotalG += sp.RemainingG
		} else {
			g.UnlinkedSlots = append(g.UnlinkedSlots, s)
		}
	}
	g.TotalG = round1(g.TotalG)
	return g
}

// groupsOf lists each distinct group of two or more trays among slots.
func groupsOf(slots []int, trays map[int]loadedTray, lookup *SpoolLookup) []TrayGroup {
	var out []TrayGroup
	seen := map[string]bool{}
	for _, s := range slots {
		if _, ok := trays[s]; !ok {
			continue
		}
		g := groupOf(s, trays, lookup)
		key := fmt.Sprint(g.Slots)
		if len(g.Slots) < 2 || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slots[0] < out[j].Slots[0] })
	return out
}

func slotList(slots []int) string {
	parts := make([]string, len(slots))
	for i, s := range slots {
		parts[i] = fmt.Sprint(s)
	}
	return strings.Join(parts, ", ")
}
