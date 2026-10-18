// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package config resolves runtime configuration from environment variables.
package config

import (
	"errors"
	"os"
	"strings"
)

const (
	defaultBambuddyURL = "http://localhost:8000"
	defaultSpoolmanURL = "http://localhost:7912"
	defaultModel       = "X1C"
)

// Config is the resolved runtime shape.
type Config struct {
	// BambuddyURL is the Bambuddy origin, without /api/v1.
	BambuddyURL string
	// BambuddyAPIKey is optional; sent as X-API-Key when set. Never logged.
	BambuddyAPIKey string
	// SpoolmanURL is the Spoolman origin, without /api/v1.
	SpoolmanURL string
	// PrinterModel is the model print_file requires a sliced file to target.
	PrinterModel string
	// HMSLookup turns HMS codes into text via Bambu's public code list. Only the
	// hex code is sent. Set PRINTSHOP_HMS_LOOKUP=off to disable.
	HMSLookup bool
	// Listen, when set, serves streamable HTTP on that address instead of stdio.
	Listen string
	// MCPToken is the bearer token HTTP clients must present. Required with Listen.
	MCPToken string
}

// Load reads the environment.
func Load() (Config, error) {
	c := Config{
		BambuddyURL:    trimURL(env("BAMBUDDY_URL", defaultBambuddyURL)),
		BambuddyAPIKey: os.Getenv("BAMBUDDY_API_KEY"),
		SpoolmanURL:    trimURL(env("SPOOLMAN_URL", defaultSpoolmanURL)),
		PrinterModel:   env("PRINTSHOP_PRINTER_MODEL", defaultModel),
		HMSLookup:      !strings.EqualFold(strings.TrimSpace(os.Getenv("PRINTSHOP_HMS_LOOKUP")), "off"),
		Listen:         os.Getenv("PRINTSHOP_MCP_LISTEN"),
		MCPToken:       os.Getenv("PRINTSHOP_MCP_TOKEN"),
	}
	if c.Listen != "" && c.MCPToken == "" {
		return c, errors.New("PRINTSHOP_MCP_LISTEN is set but PRINTSHOP_MCP_TOKEN is empty; HTTP mode can start prints, so it needs a bearer token")
	}
	return c, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func trimURL(u string) string {
	u = strings.TrimRight(strings.TrimSpace(u), "/")
	return strings.TrimSuffix(u, "/api/v1")
}
