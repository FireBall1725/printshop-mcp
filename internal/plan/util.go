// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"strconv"
	"strings"
)

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', 1, 64) }

func equalFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
