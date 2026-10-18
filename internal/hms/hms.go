// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package hms turns an HMS error code into the text Bambu publishes for it.
// Bambuddy 1.2.5.1 returns codes with no description, so this asks Bambu's
// public code list. Only the hex code leaves the machine: no serial, address
// or access code is ever sent.
package hms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultBase is Bambu's public error lookup.
const DefaultBase = "https://e.bambulab.com"

const (
	callTimeout = 4 * time.Second
	failTTL     = 5 * time.Minute
	maxCodes    = 8
)

// codeRE accepts a print error (8 hex digits) or an HMS code (16). Anything
// else is not sent anywhere.
var codeRE = regexp.MustCompile(`^[0-9A-Fa-f]{8}([0-9A-Fa-f]{8})?$`)

// Lookup fetches and caches descriptions. The zero value and a nil *Lookup
// both describe nothing.
type Lookup struct {
	base   string
	hc     *http.Client
	mu     sync.Mutex
	found  map[string]string
	failed map[string]time.Time
}

// New returns a Lookup against base. An empty base disables lookups.
func New(base string) *Lookup {
	return &Lookup{base: strings.TrimRight(base, "/"), hc: &http.Client{}, found: map[string]string{}, failed: map[string]time.Time{}}
}

// Describe returns the text for each code it could find, keyed by the code as
// given. Failures are quiet: a missing entry just means no text.
func (l *Lookup) Describe(ctx context.Context, codes []string) map[string]string {
	out := map[string]string{}
	if l == nil || l.base == "" {
		return out
	}
	for i, c := range codes {
		if i >= maxCodes {
			break
		}
		if text := l.one(ctx, c); text != "" {
			out[c] = text
		}
	}
	return out
}

func (l *Lookup) one(ctx context.Context, code string) string {
	if !codeRE.MatchString(code) {
		return ""
	}
	key := strings.ToUpper(code)
	l.mu.Lock()
	if t, ok := l.found[key]; ok {
		l.mu.Unlock()
		return t
	}
	if at, ok := l.failed[key]; ok && time.Since(at) < failTTL {
		l.mu.Unlock()
		return ""
	}
	l.mu.Unlock()

	text := l.fetch(ctx, key)
	l.mu.Lock()
	defer l.mu.Unlock()
	if text == "" {
		l.failed[key] = time.Now()
		return ""
	}
	l.found[key] = text
	return text
}

func (l *Lookup) fetch(ctx context.Context, key string) string {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/query.php?lang=en&e=%s", l.base, key), nil)
	if err != nil {
		return ""
	}
	resp, err := l.hc.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return ""
	}
	return ParseIntro(body)
}

// ParseIntro pulls the English text from a lookup response. HMS codes come
// back under device_hms and print errors under device_error. A nonzero
// result, or an empty list, gives "".
func ParseIntro(body []byte) string {
	var r struct {
		Result int `json:"result"`
		Data   struct {
			HMS   *entries `json:"device_hms"`
			Error *entries `json:"device_error"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &r) != nil || r.Result != 0 {
		return ""
	}
	for _, e := range []*entries{r.Data.HMS, r.Data.Error} {
		if e != nil && len(e.EN) > 0 {
			return strings.TrimSpace(e.EN[0].Intro)
		}
	}
	return ""
}

type entries struct {
	EN []struct {
		Intro string `json:"intro"`
	} `json:"en"`
}
