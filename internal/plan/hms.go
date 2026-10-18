// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// HMSSummary is one active HMS error. Level is Bambu's alert level, read from
// the code itself. BambuddySeverity is the raw number Bambuddy reported: on
// 1.2.5.1 it is the code's part byte, not the level (Bambuddy fixed this in a
// later release, issue 2728), so it is shown but not trusted.
type HMSSummary struct {
	Code             string `json:"code"`
	FullCode         string `json:"full_code"`
	Display          string `json:"display"`
	Level            int    `json:"level"`
	Severity         string `json:"severity"`
	SeveritySource   string `json:"severity_source"`
	BambuddySeverity int    `json:"bambuddy_severity"`
	Module           int    `json:"module"`
	Text             string `json:"text,omitempty"`
}

// HMSLevel returns the alert level of an HMS error and where it came from.
// A 16-digit code is attr (8 hex) then code (8 hex); the level is the high
// half of code, as Bambu Studio decodes it. A shorter code carries no such
// field, so Bambuddy's own number is used and the source says so.
func HMSLevel(h api.HMSError) (level int, source string) {
	if len(h.FullCode) == 16 {
		if v, err := strconv.ParseUint(h.FullCode[8:12], 16, 16); err == nil {
			return int(v), "code"
		}
	}
	return h.Severity, "bambuddy"
}

// SeverityName maps Bambu's alert level to its word.
func SeverityName(level int) string {
	switch level {
	case 1:
		return "fatal"
	case 2:
		return "serious"
	case 3:
		return "common"
	case 4:
		return "info"
	}
	return "unknown"
}

// DashedCode writes a 16-digit code as 0500-0100-0003-0004, the form in
// Bambu's wiki and on the printer screen.
func DashedCode(full string) string {
	if len(full) != 16 {
		return full
	}
	return strings.ToUpper(full[0:4] + "-" + full[4:8] + "-" + full[8:12] + "-" + full[12:16])
}

// HMSSummaries trims HMS errors. texts is keyed by full_code and may be nil.
func HMSSummaries(errs []api.HMSError, texts map[string]string) []HMSSummary {
	out := make([]HMSSummary, 0, len(errs))
	for _, h := range errs {
		lvl, src := HMSLevel(h)
		out = append(out, HMSSummary{
			Code: h.Code, FullCode: h.FullCode, Display: DashedCode(h.FullCode),
			Level: lvl, Severity: SeverityName(lvl), SeveritySource: src,
			BambuddySeverity: h.Severity, Module: h.Module, Text: texts[h.FullCode],
		})
	}
	return out
}

// HMSLine is a one-line description for a refusal message.
func HMSLine(h HMSSummary) string {
	name := h.Display
	if name == "" {
		name = h.Code
	}
	line := fmt.Sprintf("%s (%s, level %d)", name, h.Severity, h.Level)
	if h.Text != "" {
		line += ": " + clip(h.Text, 140)
	}
	return line
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// HMSCodes lists the full codes, for looking up their text.
func HMSCodes(errs []api.HMSError) []string {
	out := make([]string, 0, len(errs))
	for _, h := range errs {
		if h.FullCode != "" {
			out = append(out, h.FullCode)
		}
	}
	return out
}
