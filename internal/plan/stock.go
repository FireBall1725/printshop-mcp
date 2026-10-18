// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"sort"
	"strings"
)

// Need is one filament a print requires.
type Need struct {
	SlotID int     `json:"slot_id"`
	Type   string  `json:"type"`
	Color  string  `json:"color"`
	Grams  float64 `json:"grams"`
}

// SpoolPick is a spool offered for a need.
type SpoolPick struct {
	SpoolID     int     `json:"spool_id"`
	Vendor      string  `json:"vendor,omitempty"`
	Filament    string  `json:"filament"`
	ColorHex    string  `json:"color_hex,omitempty"`
	RemainingG  float64 `json:"remaining_g"`
	LeftAfterG  float64 `json:"left_after_g"`
	ColorDeltaE float64 `json:"color_delta_e"`
	ColorMatch  string  `json:"color_match"`
	Location    string  `json:"location,omitempty"`
}

// SlotResult is the outcome for one need.
type SlotResult struct {
	Need         Need        `json:"need"`
	Enough       bool        `json:"enough"`
	Spool        *SpoolPick  `json:"spool,omitempty"`
	Alternatives []SpoolPick `json:"alternatives,omitempty"`
	Note         string      `json:"note,omitempty"`
}

// StockPlan is the result for a whole print.
type StockPlan struct {
	AllEnough bool         `json:"all_enough"`
	Slots     []SlotResult `json:"slots"`
}

const maxAlternatives = 3

// PlanStock matches each need to a spool: material first (case-insensitive),
// then nearest colour. Archived spools are never offered. If the nearest
// colour lacks the grams, the slot is marked short and spools that do have
// enough are listed as alternatives with their colour distance, so the caller
// decides whether a different colour is acceptable. A spool wanted by two
// slots has its grams counted against both.
func PlanStock(needs []Need, spools []SpoolView) StockPlan {
	results := make([]SlotResult, len(needs))
	allocated := map[int]float64{}

	// Biggest need picks first so a large job is not starved by a small one.
	order := make([]int, len(needs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return needs[order[a]].Grams > needs[order[b]].Grams })

	for _, i := range order {
		n := needs[i]
		res := SlotResult{Need: n}
		var cands []SpoolPick
		for _, s := range spools {
			if s.Archived || !strings.EqualFold(s.Material, n.Type) {
				continue
			}
			d, ok := DeltaE(n.Color, s.ColorHex)
			if !ok {
				d = 999
			}
			left := s.RemainingG - allocated[s.ID] - n.Grams
			cands = append(cands, SpoolPick{
				SpoolID: s.ID, Vendor: s.Vendor, Filament: s.Name, ColorHex: s.ColorHex,
				RemainingG: s.RemainingG - allocated[s.ID], LeftAfterG: round1(left),
				ColorDeltaE: round1(d), ColorMatch: ColourClass(d, ok && d != 999), Location: s.Location,
			})
		}
		if len(cands) == 0 {
			res.Note = "no unarchived " + n.Type + " spool in stock"
			results[i] = res
			continue
		}
		sort.SliceStable(cands, func(a, b int) bool {
			if cands[a].ColorDeltaE != cands[b].ColorDeltaE {
				return cands[a].ColorDeltaE < cands[b].ColorDeltaE
			}
			return cands[a].RemainingG > cands[b].RemainingG
		})
		best := cands[0]
		res.Spool = &best
		res.Enough = best.LeftAfterG >= 0
		allocated[best.SpoolID] += n.Grams
		if !res.Enough {
			res.Note = "nearest-colour spool is short by " + fmtG(-best.LeftAfterG)
			for _, c := range cands[1:] {
				if c.LeftAfterG >= 0 && len(res.Alternatives) < maxAlternatives {
					res.Alternatives = append(res.Alternatives, c)
				}
			}
		}
		results[i] = res
	}

	plan := StockPlan{AllEnough: true, Slots: results}
	for _, r := range results {
		if !r.Enough {
			plan.AllEnough = false
		}
	}
	return plan
}

func fmtG(g float64) string {
	return strings.TrimRight(strings.TrimRight(formatFloat(round1(g)), "0"), ".") + " g"
}
