// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package api

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Decode unmarshals body into out. Bambuddy sometimes returns raw control
// characters (newlines in a filename, say) inside strings, which encoding/json
// rejects; Python's strict=False accepts them. On a syntax error this escapes
// those characters and retries once.
func Decode(body []byte, out any) error {
	err := json.Unmarshal(body, out)
	if err == nil {
		return nil
	}
	if _, ok := err.(*json.SyntaxError); !ok {
		return err
	}
	return json.Unmarshal(EscapeControlChars(body), out)
}

// EscapeControlChars rewrites bytes below 0x20 that sit inside JSON strings as
// \uXXXX escapes. Bytes outside strings are left alone.
func EscapeControlChars(in []byte) []byte {
	out := make([]byte, 0, len(in)+16)
	inStr, esc := false, false
	for _, b := range in {
		switch {
		case esc:
			esc = false
			out = append(out, b)
		case inStr && b == '\\':
			esc = true
			out = append(out, b)
		case b == '"':
			inStr = !inStr
			out = append(out, b)
		case inStr && b < 0x20:
			out = append(out, '\\', 'u', '0', '0', "0123456789abcdef"[b>>4], "0123456789abcdef"[b&0xf])
		default:
			out = append(out, b)
		}
	}
	return out
}

// Num is a float that also accepts a quoted number, and treats null or junk
// as zero. Bambuddy's temperatures and nozzle diameters are not consistent
// about which form they use.
type Num float64

// UnmarshalJSON implements json.Unmarshaler.
func (n *Num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		*n = 0
		return nil
	}
	*n = Num(f)
	return nil
}
