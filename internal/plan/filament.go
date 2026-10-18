// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// typicalDensity lists densities (g/cm3) for common materials, used only when
// the caller gives none. Spoolman requires a density; an unlisted material
// has no default, so the caller must supply one rather than get a guess.
var typicalDensity = map[string]float64{
	"PLA": 1.24, "PETG": 1.27, "ABS": 1.04, "ASA": 1.07, "TPU": 1.21, "PC": 1.20, "PA": 1.14, "PVA": 1.23,
}

// DensityFor returns the typical density of a material.
func DensityFor(material string) (float64, bool) {
	// "PLA Matte" is still PLA for density; MaterialWarning flags the spelling.
	if f := strings.Fields(strings.ToUpper(material)); len(f) > 0 {
		d, ok := typicalDensity[f[0]]
		return d, ok
	}
	return 0, false
}

// MaterialWarning flags a material that carries a product line. Stock
// matching compares the material first and then the nearest colour, so
// "PLA Matte" would never match a file that asks for "PLA".
func MaterialWarning(material string) string {
	if strings.ContainsAny(strings.TrimSpace(material), " \t") {
		return fmt.Sprintf("material %q has a space in it; stock_for_print and print_file match on the plain material (for example \"PLA\"), so put the product line in the filament name instead", material)
	}
	return ""
}

// FindVendor returns the vendor whose name matches, ignoring case and
// surrounding space.
func FindVendor(vendors []api.Vendor, name string) (api.Vendor, bool) {
	for _, v := range vendors {
		if equalFold(v.Name, name) {
			return v, true
		}
	}
	return api.Vendor{}, false
}

// FindDuplicate returns an existing filament with the same vendor, name and
// material.
func FindDuplicate(fs []api.Filament, vendorID int, name, material string) (api.Filament, bool) {
	for _, f := range fs {
		if f.Vendor != nil && f.Vendor.ID == vendorID && equalFold(f.Name, name) && equalFold(f.Material, material) {
			return f, true
		}
	}
	return api.Filament{}, false
}

// CompareExtra checks that a spool's extra fields came through a change
// untouched. Bambuddy keeps its AMS tag in extra.tag. None of these tools send
// extra, and Spoolman 0.26.1 merges it rather than replacing it, but a version
// that replaced the dict would lose the tag, so the check stays: any difference
// here means the spool may have dropped out of the AMS link. alert is empty
// when intact.
func CompareExtra(before, after map[string]string) (intact bool, alert string) {
	var diffs []string
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		b, bok := before[k]
		a, aok := after[k]
		switch {
		case bok && !aok:
			diffs = append(diffs, fmt.Sprintf("extra.%s was %s and is now missing", k, b))
		case !bok && aok:
			diffs = append(diffs, fmt.Sprintf("extra.%s was unset and is now %s", k, a))
		case a != b:
			diffs = append(diffs, fmt.Sprintf("extra.%s changed from %s to %s", k, b, a))
		}
	}
	if len(diffs) == 0 {
		return true, ""
	}
	return false, "ALERT: the spool's extra fields changed during this update: " + strings.Join(diffs, "; ") +
		". Bambuddy keeps its AMS link in extra.tag, so this spool may have dropped out of that link. Restore the value above in Spoolman (Settings, then the spool's extra fields) before the next AMS sync."
}
