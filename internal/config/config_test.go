// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package config

import "testing"

func clear(t *testing.T) {
	for _, k := range []string{"BAMBUDDY_URL", "BAMBUDDY_API_KEY", "SPOOLMAN_URL", "PRINTSHOP_PRINTER_MODEL", "PRINTSHOP_HMS_LOOKUP", "PRINTSHOP_MCP_LISTEN", "PRINTSHOP_MCP_TOKEN"} {
		t.Setenv(k, "")
	}
}

func TestDefaultsAssumeAMachineOnTheSameHost(t *testing.T) {
	clear(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.BambuddyURL != "http://localhost:8000" || c.SpoolmanURL != "http://localhost:7912" || c.PrinterModel != "X1C" || !c.HMSLookup {
		t.Fatalf("%+v", c)
	}
}

func TestURLsAreTrimmed(t *testing.T) {
	clear(t)
	t.Setenv("BAMBUDDY_URL", " http://bambuddy.local:8000/api/v1/ ")
	t.Setenv("SPOOLMAN_URL", "http://spoolman.local:7912/")
	c, _ := Load()
	if c.BambuddyURL != "http://bambuddy.local:8000" || c.SpoolmanURL != "http://spoolman.local:7912" {
		t.Fatalf("%+v", c)
	}
}

func TestHMSLookupSwitch(t *testing.T) {
	clear(t)
	for v, want := range map[string]bool{"": true, "on": true, "off": false, " OFF ": false} {
		t.Setenv("PRINTSHOP_HMS_LOOKUP", v)
		if c, _ := Load(); c.HMSLookup != want {
			t.Errorf("%q: %v", v, c.HMSLookup)
		}
	}
}

func TestHTTPModeNeedsAToken(t *testing.T) {
	clear(t)
	t.Setenv("PRINTSHOP_MCP_LISTEN", ":8090")
	if _, err := Load(); err == nil {
		t.Fatal("HTTP mode without a token must not start; it can start prints")
	}
	t.Setenv("PRINTSHOP_MCP_TOKEN", "x")
	if c, err := Load(); err != nil || c.Listen != ":8090" {
		t.Fatalf("%v %+v", err, c)
	}
}
