// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package plan holds the pure logic behind the tools: trimming printer status,
// matching filament needs to spools, checking the AMS against Spoolman, and the
// pre-print safety gate. It does no I/O, so every function is tested against
// recorded fixtures.
package plan

import (
	"math"
	"strconv"
	"strings"
)

// NormHex returns a colour as six uppercase hex digits with no '#' or alpha,
// or "" if it is not a colour. Bambuddy tray colours are RRGGBBAA.
func NormHex(s string) string {
	s = strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if len(s) != 6 && len(s) != 8 {
		return ""
	}
	if _, err := strconv.ParseUint(s, 16, 32); err != nil {
		return ""
	}
	return s[:6]
}

func rgb(hex string) (r, g, b float64, ok bool) {
	h := NormHex(hex)
	if h == "" {
		return 0, 0, 0, false
	}
	v, _ := strconv.ParseUint(h, 16, 32)
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff), true
}

func lab(r, g, b float64) (float64, float64, float64) {
	lin := func(c float64) float64 {
		c /= 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	lr, lg, lb := lin(r), lin(g), lin(b)
	x := (0.4124*lr + 0.3576*lg + 0.1805*lb) / 0.95047
	y := 0.2126*lr + 0.7152*lg + 0.0722*lb
	z := (0.0193*lr + 0.1192*lg + 0.9505*lb) / 1.08883
	f := func(t float64) float64 {
		if t > 0.008856 {
			return math.Cbrt(t)
		}
		return 7.787*t + 16.0/116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

// DeltaE is the CIE76 distance between two hex colours. ok is false if either
// is not a colour.
func DeltaE(a, b string) (d float64, ok bool) {
	ar, ag, ab, ok1 := rgb(a)
	br, bg, bb, ok2 := rgb(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	l1, a1, b1 := lab(ar, ag, ab)
	l2, a2, b2 := lab(br, bg, bb)
	return math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2)), true
}

// ColourClass names a distance. Thresholds are for CIE76: below about 2.3 is
// not noticeable, and a filament swatch rarely needs better than "close".
func ColourClass(d float64, ok bool) string {
	switch {
	case !ok:
		return "unknown"
	case d <= 5:
		return "exact"
	case d <= 15:
		return "close"
	case d <= 30:
		return "similar"
	default:
		return "different"
	}
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
