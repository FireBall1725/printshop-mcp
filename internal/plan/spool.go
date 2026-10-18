// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"strconv"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// SpoolView is a Spoolman spool flattened to what matching needs.
type SpoolView struct {
	ID         int     `json:"id"`
	Vendor     string  `json:"vendor,omitempty"`
	Name       string  `json:"filament"`
	FilamentID int     `json:"filament_id"`
	Material   string  `json:"material"`
	ColorHex   string  `json:"color_hex,omitempty"`
	RemainingG float64 `json:"remaining_g"`
	InitialG   float64 `json:"initial_g,omitempty"`
	Location   string  `json:"location,omitempty"`
	Archived   bool    `json:"archived,omitempty"`
	// TagSet is true when extra.tag is set, which is how Bambuddy tracks a
	// spool against an AMS tray. Always present, so false is visible.
	TagSet bool `json:"tag_set"`
	// Tag is the AMS tray_uuid Bambuddy stored, unquoted. Empty for spools
	// Bambuddy did not create.
	Tag string `json:"-"`
}

// ViewOf flattens a Spoolman spool. A missing initial weight falls back to the
// filament's nominal weight.
func ViewOf(s api.Spool) SpoolView {
	v := SpoolView{
		ID:         s.ID,
		Name:       s.Filament.Name,
		FilamentID: s.Filament.ID,
		Material:   s.Filament.Material,
		ColorHex:   NormHex(s.Filament.ColorHex),
		Location:   s.Location,
		Archived:   s.Archived,
		Tag:        UnquoteTag(s.Extra["tag"]),
	}
	if s.Filament.Vendor != nil {
		v.Vendor = s.Filament.Vendor.Name
	}
	if s.RemainingWeight != nil {
		v.RemainingG = round1(*s.RemainingWeight)
	}
	switch {
	case s.InitialWeight != nil:
		v.InitialG = *s.InitialWeight
	case s.Filament.Weight != nil:
		v.InitialG = *s.Filament.Weight
	}
	v.TagSet = v.Tag != ""
	return v
}

// UnquoteTag undoes Spoolman's text extra field encoding, which stores the
// value as a JSON string: the tag arrives as "\"7FF9...\"".
func UnquoteTag(raw string) string {
	raw = strings.TrimSpace(raw)
	if u, err := strconv.Unquote(raw); err == nil {
		return strings.ToUpper(u)
	}
	return strings.ToUpper(strings.Trim(raw, `"`))
}

// InStock reports whether a spool is usable: not archived and above minG.
func (s SpoolView) InStock(minG float64) bool {
	return !s.Archived && s.RemainingG > minG
}
