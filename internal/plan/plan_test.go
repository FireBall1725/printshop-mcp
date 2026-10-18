// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// Fixtures are recorded from a live Bambuddy 1.2.5.1 and Spoolman 0.26.1,
// with serial numbers redacted. The printer was RUNNING with one HMS error.

func load[T any](t *testing.T, name string) T {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err := api.Decode(b, &v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

func fixtures(t *testing.T) (api.PrinterStatus, []SpoolView, *SpoolLookup) {
	st := load[api.PrinterStatus](t, "status_running.json")
	raw := load[[]api.Spool](t, "spools.json")
	views := make([]SpoolView, len(raw))
	for i, s := range raw {
		views[i] = ViewOf(s)
	}
	return st, views, NewLookup(1, load[[]api.SlotAssignment](t, "assignments.json"), views)
}

func TestDecodeRawControlCharacters(t *testing.T) {
	body := []byte("{\"name\":\"line one\nline two\ttab\",\"n\":1}")
	var v struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	if err := api.Decode(body, &v); err != nil {
		t.Fatal(err)
	}
	if v.Name != "line one\nline two\ttab" || v.N != 1 {
		t.Fatalf("got %+v", v)
	}
	// Control characters outside strings and escaped quotes must survive.
	if err := api.Decode([]byte("{\n\"a\": \"q\\\"x\ny\"\n}"), &v); err != nil {
		t.Fatal(err)
	}
}

func TestSummarizeRunningFixture(t *testing.T) {
	st, _, _ := fixtures(t)
	s := Summarize(st, nil)
	if s.State != "RUNNING" || s.Job != "pixelbar-mount-prototype-v6" {
		t.Fatalf("state/job: %+v", s)
	}
	if s.ProgressPct != 13 || s.TotalLayers != 158 || s.RemainingMinutes != 40 {
		t.Fatalf("progress: %+v", s)
	}
	if s.Temps.Nozzle != 142 || s.Temps.NozzleTarget != 140 || s.Temps.Bed != 55 {
		t.Fatalf("temps: %+v", s.Temps)
	}
	if len(s.AMS) != 2 || s.AMS[0].Slot != 1 || s.AMS[1].Slot != 3 {
		t.Fatalf("empty trays must be dropped, got %+v", s.AMS)
	}
	if s.AMS[0].Color != "000000" || s.AMS[0].Type != "PLA" || s.AMS[0].RemainPct == nil || *s.AMS[0].RemainPct != 78 {
		t.Fatalf("tray 1: %+v", s.AMS[0])
	}
	h := s.HMS[0]
	if len(s.HMS) != 1 || h.Code != "0x30004" || h.FullCode != "0500010000030004" || h.Display != "0500-0100-0003-0004" {
		t.Fatalf("hms: %+v", s.HMS)
	}
	// Bambuddy 1.2.5.1 reported severity 1 here. The level in the code is 3.
	if h.Level != 3 || h.Severity != "common" || h.SeveritySource != "code" || h.BambuddySeverity != 1 {
		t.Fatalf("severity must come from the code and show Bambuddy's raw number: %+v", h)
	}
	if s.External != nil {
		t.Fatalf("empty external spool should be omitted")
	}
}

func TestDeltaE(t *testing.T) {
	if d, ok := DeltaE("000000", "#000000FF"); !ok || d != 0 {
		t.Fatalf("same colour: %v %v", d, ok)
	}
	if d, _ := DeltaE("000000", "FFFFFF"); d < 90 {
		t.Fatalf("black to white should be about 100, got %v", d)
	}
	black, _ := DeltaE("000000", "0A0A0A")
	magenta, _ := DeltaE("000000", "EC008C")
	if black >= magenta {
		t.Fatalf("near-black %v should be closer than magenta %v", black, magenta)
	}
	if _, ok := DeltaE("zzz", "000000"); ok {
		t.Fatal("junk colour accepted")
	}
	if ColourClass(0, true) != "exact" || ColourClass(60, true) != "different" || ColourClass(0, false) != "unknown" {
		t.Fatal("classes")
	}
}

func TestUnquoteTag(t *testing.T) {
	if got := UnquoteTag(`"00000000000000000000000000000005"`); got != "00000000000000000000000000000005" {
		t.Fatal(got)
	}
	if UnquoteTag("") != "" {
		t.Fatal("empty")
	}
}

func TestPlanStockPicksNearestColour(t *testing.T) {
	_, views, _ := fixtures(t)
	p := PlanStock([]Need{{SlotID: 1, Type: "pla", Color: "#000000", Grams: 78.6}}, views)
	if !p.AllEnough {
		t.Fatalf("%+v", p)
	}
	s := p.Slots[0].Spool
	if s == nil || s.SpoolID != 9 || s.ColorMatch != "exact" {
		t.Fatalf("expected the black PLA spool 9, got %+v", s)
	}
	if s.LeftAfterG != 686.4 {
		t.Fatalf("left %v", s.LeftAfterG)
	}
}

func TestPlanStockShortListsAlternatives(t *testing.T) {
	_, views, _ := fixtures(t)
	p := PlanStock([]Need{{SlotID: 1, Type: "PLA", Color: "000000", Grams: 800}}, views)
	r := p.Slots[0]
	if p.AllEnough || r.Enough || r.Spool.SpoolID != 9 {
		t.Fatalf("black should be short: %+v", r)
	}
	if len(r.Alternatives) == 0 || r.Alternatives[0].LeftAfterG < 0 {
		t.Fatalf("alternatives with enough grams expected: %+v", r.Alternatives)
	}
	if !strings.Contains(r.Note, "short by 35 g") {
		t.Fatalf("note %q", r.Note)
	}
}

func TestPlanStockSharedSpoolAndMissingMaterial(t *testing.T) {
	_, views, _ := fixtures(t)
	p := PlanStock([]Need{
		{SlotID: 1, Type: "PLA", Color: "000000", Grams: 700},
		{SlotID: 2, Type: "PLA", Color: "000000", Grams: 100},
		{SlotID: 3, Type: "PETG", Color: "000000", Grams: 10},
	}, views)
	if !p.Slots[0].Enough {
		t.Fatalf("first slot: %+v", p.Slots[0])
	}
	if p.Slots[1].Enough || p.Slots[1].Spool.SpoolID != 9 {
		t.Fatalf("the same spool cannot cover 700 + 100 of 765: %+v", p.Slots[1])
	}
	if p.Slots[2].Spool != nil || !strings.Contains(p.Slots[2].Note, "no unarchived PETG") {
		t.Fatalf("petg: %+v", p.Slots[2])
	}
}

func TestPlanStockSkipsArchived(t *testing.T) {
	_, views, _ := fixtures(t)
	for i := range views {
		if views[i].ID == 9 {
			views[i].Archived = true
		}
	}
	p := PlanStock([]Need{{SlotID: 1, Type: "PLA", Color: "000000", Grams: 10}}, views)
	if p.Slots[0].Spool.SpoolID == 9 {
		t.Fatal("archived spool offered")
	}
}

func TestCheckAMSFlagsRemainingMismatch(t *testing.T) {
	st, _, l := fixtures(t)
	checks := CheckAMS(st, l)
	if len(checks) != 2 {
		t.Fatalf("%+v", checks)
	}
	black, magenta := checks[0], checks[1]
	if black.Slot != 1 || black.SpoolID != 9 || black.LinkedBy != "slot assignment" || len(black.Flags) != 0 {
		t.Fatalf("black tray should be clean: %+v", black)
	}
	if magenta.SpoolID != 8 || len(magenta.Flags) != 1 || !strings.Contains(magenta.Flags[0], "remaining differs") {
		t.Fatalf("magenta tray should flag 34%% vs 96%%: %+v", magenta)
	}
}

func TestCheckAMSFallsBackToRFIDTag(t *testing.T) {
	st, views, _ := fixtures(t)
	l := NewLookup(1, nil, views) // no slot assignments at all
	checks := CheckAMS(st, l)
	if checks[0].SpoolID != 9 || checks[0].LinkedBy != "rfid tag" {
		t.Fatalf("tag link: %+v", checks[0])
	}
}

func TestCheckAMSFlagsUnlinkedAndColour(t *testing.T) {
	st, views, _ := fixtures(t)
	checks := CheckAMS(st, NewLookup(1, nil, nil))
	if len(checks[0].Flags) != 1 || !strings.Contains(checks[0].Flags[0], "no Spoolman spool") {
		t.Fatalf("%+v", checks[0])
	}
	// Point the black tray at the magenta spool.
	l := NewLookup(1, []api.SlotAssignment{{PrinterID: 1, AMSID: 0, TrayID: 1, SpoolID: 8}}, views)
	got := strings.Join(CheckAMS(st, l)[0].Flags, "|")
	if !strings.Contains(got, "colour differs") {
		t.Fatalf("flags %q", got)
	}
}

// idleFixture is the recorded status turned into a clean idle printer.
func idleFixture(t *testing.T) PreflightInput {
	st, _, l := fixtures(t)
	st.State, st.HMSErrors, st.CurrentPrint, st.SubtaskName = "IDLE", nil, "", ""
	return PreflightInput{
		Status:  st,
		File:    load[api.LibraryFile](t, "library_file_x1c.json"),
		Needs:   []Need{{SlotID: 1, Type: "PLA", Color: "#000000", Grams: 78.6}},
		Mapping: []int{1},
		UseAMS:  true,
		Model:   "X1C",
		Lookup:  l,
	}
}

func TestPreflightAllowsCleanIdlePrint(t *testing.T) {
	r := Preflight(idleFixture(t))
	if !r.Allowed || len(r.Refusals) != 0 || r.PrinterDoing != "idle" {
		t.Fatalf("%+v", r)
	}
	if len(r.Slots) != 1 || r.Slots[0].SpoolID != 9 || r.Slots[0].LeftAfterG != 686.4 {
		t.Fatalf("%+v", r.Slots)
	}
}

func TestPreflightRefusesWhileRunningAndSaysWhy(t *testing.T) {
	in := idleFixture(t)
	st, _, _ := fixtures(t)
	in.Status = st
	r := Preflight(in)
	if r.Allowed || r.PrinterState != "RUNNING" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(r.PrinterDoing, `printing "pixelbar-mount-prototype-v6", 13%, layer 0 of 158`) {
		t.Fatalf("doing %q", r.PrinterDoing)
	}
	// force must not override the state rule.
	in.Force = true
	if Preflight(in).Allowed {
		t.Fatal("force started a print on a running printer")
	}
}

func TestPreflightAllowsFinishAndRefusesOtherStates(t *testing.T) {
	for state, want := range map[string]bool{"FINISH": true, "IDLE": true, "PAUSE": false, "FAILED": false, "RUNNING": false, "": false} {
		in := idleFixture(t)
		in.Status.State = state
		if got := Preflight(in).Allowed; got != want {
			t.Errorf("state %q: allowed=%v want %v", state, got, want)
		}
	}
}

func TestPreflightHMSNeedsForce(t *testing.T) {
	in := idleFixture(t)
	in.Status.HMSErrors = []api.HMSError{{Code: "0x30004", FullCode: "0500010000030004", Severity: 1}}
	r := Preflight(in)
	if r.Allowed || len(r.HMS) != 1 || r.HMS[0].Code != "0x30004" {
		t.Fatalf("%+v", r)
	}
	in.Force = true
	r = Preflight(in)
	if !r.Allowed || len(r.HMS) != 1 || len(r.Warnings) == 0 {
		t.Fatalf("force should allow and still report: %+v", r)
	}
}

func TestPreflightModelAndFileType(t *testing.T) {
	in := idleFixture(t)
	other := "P1S"
	in.File.SlicedForModel = &other
	if r := Preflight(in); r.Allowed || !strings.Contains(strings.Join(r.Refusals, "|"), "sliced for P1S") {
		t.Fatalf("%+v", r)
	}
	in = idleFixture(t)
	in.File.SlicedForModel, in.File.Metadata = nil, nil
	if Preflight(in).Allowed {
		t.Fatal("unknown model must be refused")
	}
	in = idleFixture(t)
	in.File.FileType = "stl"
	if Preflight(in).Allowed {
		t.Fatal("stl accepted")
	}
}

func TestPreflightFilamentGrams(t *testing.T) {
	cases := []struct {
		name      string
		grams     float64
		wantAllow bool
		wantWarn  string
	}{
		{"plenty", 78.6, true, ""},
		{"short within 10 percent", 830, true, "within 10%"},
		{"short beyond 10 percent", 900, false, ""},
		{"tight margin", 700, true, "to spare"},
	}
	for _, c := range cases {
		in := idleFixture(t)
		in.Needs[0].Grams = c.grams
		r := Preflight(in)
		if r.Allowed != c.wantAllow {
			t.Errorf("%s: allowed=%v refusals=%v", c.name, r.Allowed, r.Refusals)
		}
		if c.wantWarn != "" && !strings.Contains(strings.Join(r.Warnings, "|"), c.wantWarn) {
			t.Errorf("%s: warnings %v", c.name, r.Warnings)
		}
	}
}

func TestPreflightMappingProblems(t *testing.T) {
	in := idleFixture(t)
	in.Mapping = nil
	r := Preflight(in)
	if !r.Allowed || r.MappingSource != "auto" || len(r.AMSMapping) != 1 || r.AMSMapping[0] != 1 {
		t.Fatalf("black PLA should be mapped to slot 1 automatically: %+v", r)
	}
	in = idleFixture(t)
	in.Mapping = []int{0} // tray 0 is empty
	if r := Preflight(in); r.Allowed || !strings.Contains(strings.Join(r.Refusals, "|"), "nothing loaded") {
		t.Fatalf("%+v", r)
	}
	in = idleFixture(t)
	in.Mapping = []int{3} // magenta PLA holds the right material but we asked for black
	r = Preflight(in)
	if !r.Allowed || r.MappingSource != "explicit" {
		t.Fatalf("same material, different colour is the caller's call: %+v", r)
	}
	if !strings.Contains(strings.Join(r.Warnings, "|"), "is #000000 in the file but AMS slot 3 holds #EC008C") {
		t.Fatalf("a colour mismatch on an explicit mapping should warn: %v", r.Warnings)
	}
	in = idleFixture(t)
	in.Needs[0].Type = "PETG"
	if r := Preflight(in); r.Allowed || !strings.Contains(strings.Join(r.Refusals, "|"), "needs PETG but AMS slot 1 holds PLA") {
		t.Fatalf("%+v", r)
	}
	in = idleFixture(t)
	in.Mapping = []int{}
	in.Needs = append(in.Needs, Need{SlotID: 2, Type: "PLA", Color: "000000", Grams: 1})
	in.Mapping = []int{1}
	if r := Preflight(in); r.Allowed {
		t.Fatalf("mapping shorter than the file's slots: %+v", r)
	}
}

func TestPreflightUnlinkedTrayWarnsAndNoAMSSkips(t *testing.T) {
	in := idleFixture(t)
	in.Lookup = NewLookup(1, nil, nil)
	r := Preflight(in)
	if !r.Allowed || !strings.Contains(strings.Join(r.Warnings, "|"), "no linked Spoolman spool") {
		t.Fatalf("%+v", r)
	}
	in = idleFixture(t)
	in.UseAMS, in.Mapping = false, nil
	if r := Preflight(in); !r.Allowed || len(r.Warnings) == 0 {
		t.Fatalf("%+v", r)
	}
	in = idleFixture(t)
	in.Needs = nil
	if Preflight(in).Allowed {
		t.Fatal("no requirements must be refused")
	}
}

// ─── newer tools ─────────────────────────────────────────────────────────────

func TestPrinterRowsNeverCarrySecrets(t *testing.T) {
	// The fixture's access code, serial and IP are fake but present, as they
	// are in the real GET /printers/ response.
	raw, err := os.ReadFile("testdata/printers.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "TESTCODE-NOT-REAL") {
		t.Fatal("fixture lost its secret; the test would prove nothing")
	}
	rows := PrinterRows(load[[]api.PrinterInfo](t, "printers.json"))
	b, _ := json.Marshal(rows)
	for _, bad := range []string{"TESTCODE", "TESTSERIAL", "192.0.2", "access", "serial", "ip_address"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("printer row leaks %q: %s", bad, b)
		}
	}
	if len(rows) != 1 || rows[0].ID != 1 || rows[0].Model != "X1C" {
		t.Fatalf("%+v", rows)
	}
}

func TestSummarizeAMSUnitsAndDrying(t *testing.T) {
	st, _, _ := fixtures(t)
	s := Summarize(st, nil)
	if len(s.AMSUnits) != 1 || s.AMSUnits[0].Humidity != 35 || s.AMSUnits[0].Drying != nil {
		t.Fatalf("idle unit should show humidity and no drying: %+v", s.AMSUnits)
	}
	st.AMS[0].DryStatus, st.AMS[0].DryTime, st.AMS[0].DryTarget, st.AMS[0].DryFilament = 1, 180, 55, "PLA"
	d := Summarize(st, nil).AMSUnits[0].Drying
	if d == nil || d.TimeLeft != 180 || d.TargetC != 55 || d.Filament != "PLA" {
		t.Fatalf("%+v", d)
	}
}

func TestCheckSkip(t *testing.T) {
	objs := load[api.PrintObjects](t, "print_objects.json")
	if len(objs.Objects) != 1 {
		t.Fatalf("fixture: %+v", objs)
	}
	cases := []struct {
		name string
		objs api.PrintObjects
		ids  []int
		want string
	}{
		{"only object", objs, []int{29}, "every remaining object"},
		{"unknown", objs, []int{5}, "not on this plate"},
		{"empty", objs, nil, "empty"},
		{"not printing", api.PrintObjects{Objects: objs.Objects}, []int{29}, "nothing to skip"},
		{"already skipped", api.PrintObjects{IsPrinting: true, Objects: []api.PrintObject{{ID: 1, Name: "a", Skipped: true}, {ID: 2, Name: "b"}}}, []int{1}, "already skipped"},
	}
	for _, c := range cases {
		got := strings.Join(CheckSkip(c.objs, c.ids), "|")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	two := api.PrintObjects{IsPrinting: true, Objects: []api.PrintObject{{ID: 1}, {ID: 2}}}
	if r := CheckSkip(two, []int{1}); len(r) != 0 {
		t.Fatalf("skipping one of two is fine: %v", r)
	}
	if r := CheckSkip(two, []int{1, 2}); len(r) == 0 {
		t.Fatal("skipping both must be refused")
	}
	if r := CheckSkip(two, []int{1, 1}); len(r) != 0 {
		t.Fatalf("a repeated id must not count twice: %v", r)
	}
}

func TestCheckDrying(t *testing.T) {
	st, _, _ := fixtures(t)
	st.SupportsDrying, st.DryWhilePrinting = true, false
	if r := CheckDrying(st, 0, 0, 0); len(r) != 1 || !strings.Contains(r[0], "RUNNING") {
		t.Fatalf("mid-print without support: %v", r)
	}
	st.State = "IDLE"
	if r := CheckDrying(st, 0, 55, 6); len(r) != 0 {
		t.Fatalf("%v", r)
	}
	for name, c := range map[string][3]int{"cold": {0, 40, 4}, "hot": {0, 90, 4}, "long": {0, 50, 48}, "no unit": {3, 50, 4}} {
		if len(CheckDrying(st, c[0], c[1], c[2])) == 0 {
			t.Errorf("%s accepted", name)
		}
	}
	st.SupportsDrying = false
	if len(CheckDrying(st, 0, 0, 0)) == 0 {
		t.Error("unsupported printer accepted")
	}
	st.SupportsDrying, st.DryWhilePrinting, st.State = true, true, "RUNNING"
	if r := CheckDrying(st, 0, 0, 0); len(r) != 0 {
		t.Fatalf("drying while printing is supported: %v", r)
	}
}

func TestCheckAssign(t *testing.T) {
	st, views, _ := fixtures(t)
	black, magenta := views[0], views[0]
	for _, v := range views {
		switch v.ID {
		case 9:
			black = v
		case 8:
			magenta = v
		}
	}
	assigns := load[[]api.SlotAssignment](t, "assignments.json")
	if ref, warn := CheckAssign(st, 1, 1, black, assigns); len(ref) != 0 || len(warn) != 0 {
		t.Fatalf("the existing link is clean: %v %v", ref, warn)
	}
	_, warn := CheckAssign(st, 1, 1, magenta, assigns)
	if got := strings.Join(warn, "|"); !strings.Contains(got, "colour differs") || !strings.Contains(got, "already linked to spool #9") || !strings.Contains(got, "already linked to AMS 0 tray 3") {
		t.Fatalf("warnings %q", got)
	}
	if _, warn := CheckAssign(st, 1, 0, black, nil); !strings.Contains(strings.Join(warn, "|"), "empty") {
		t.Fatalf("empty tray should warn: %v", warn)
	}
	if ref, _ := CheckAssign(st, 1, 99, black, nil); len(ref) == 0 {
		t.Fatal("slot 99 accepted")
	}
	black.Archived = true
	if ref, _ := CheckAssign(st, 1, 1, black, nil); len(ref) == 0 {
		t.Fatal("archived spool accepted")
	}
}

func TestArchiveAndStatsRows(t *testing.T) {
	as := load[[]api.Archive](t, "archives.json")
	r := ArchiveRowOf(as[0], true)
	if r.ID != 20 || r.Name != "pixelbar-mount-prototype-v6" || r.PlannedMinutes != 47 || r.FilamentG != 19.37 || r.Color != "000000" {
		t.Fatalf("%+v", r)
	}
	if len(r.Filaments) != 1 || r.Filaments[0].Grams != 19.37 {
		t.Fatalf("slots: %+v", r.Filaments)
	}
	if got := ArchiveRowOf(as[0], false); got.Filaments != nil {
		t.Fatal("slots should only appear when asked for")
	}
	st := StatsOf(load[api.ArchiveStats](t, "archive_stats.json"))
	if st.TotalPrints != 23 || st.Failed != 1 || st.SuccessRatePct != 95.7 || st.PrintHours != 40 || st.FilamentG != 1235.5 {
		t.Fatalf("%+v", st)
	}
}

func TestMaintenanceReport(t *testing.T) {
	in := load[[]api.MaintenancePrinter](t, "maintenance_overview.json")
	all := MaintenanceReport(in, 0, false)
	if len(all) != 1 || all[0].Name != "X1C" || len(all[0].Tasks) == 0 {
		t.Fatalf("%+v", all)
	}
	if all[0].Due != 1 {
		t.Fatalf("the live overview reported one due task, got %d", all[0].Due)
	}
	due := MaintenanceReport(in, 1, true)
	if len(due[0].Tasks) != 1 || !due[0].Tasks[0].Due {
		t.Fatalf("only_due: %+v", due[0].Tasks)
	}
	if len(MaintenanceReport(in, 2, false)) != 0 {
		t.Fatal("unknown printer id should match nothing")
	}
}

func TestSummarizeHistory(t *testing.T) {
	h := load[api.AMSHistory](t, "ams_history.json")
	s := SummarizeHistory(h, 8)
	if s.Points != len(h.Data) || len(s.Samples) != 8 {
		t.Fatalf("points %d samples %d", s.Points, len(s.Samples))
	}
	if s.Samples[len(s.Samples)-1] != h.Data[len(h.Data)-1] || s.Samples[0] != h.Data[0] {
		t.Fatal("first and newest readings must be kept")
	}
	if s.Humidity.Latest != h.Data[len(h.Data)-1].Humidity || s.From != h.Data[0].RecordedAt {
		t.Fatalf("%+v", s)
	}
	if small := SummarizeHistory(api.AMSHistory{Data: h.Data[:3]}, 8); len(small.Samples) != 3 {
		t.Fatal("short series must pass through")
	}
	if e := SummarizeHistory(api.AMSHistory{}, 8); e.Points != 0 || e.Samples == nil {
		t.Fatalf("empty series: %+v", e)
	}
}

// ─── HMS level, text and refusal wording ─────────────────────────────────────

func TestHMSLevelComesFromTheCode(t *testing.T) {
	cases := []struct {
		name   string
		h      api.HMSError
		level  int
		source string
	}{
		// Recorded: the SD card notice. Bambuddy 1.2.5.1 said severity 1.
		{"sd card full", api.HMSError{FullCode: "0500010000030004", Severity: 1}, 3, "code"},
		// From Bambuddy's own tests: a task-stopping fault and a pause.
		{"nozzle temp", api.HMSError{FullCode: "0300020000010008", Severity: 2}, 1, "code"},
		{"pause", api.HMSError{FullCode: "0500060000020005", Severity: 6}, 2, "code"},
		{"short code has no level field", api.HMSError{FullCode: "03004000", Severity: 1}, 1, "bambuddy"},
	}
	for _, c := range cases {
		lvl, src := HMSLevel(c.h)
		if lvl != c.level || src != c.source {
			t.Errorf("%s: got %d/%s want %d/%s", c.name, lvl, src, c.level, c.source)
		}
	}
	if SeverityName(1) != "fatal" || SeverityName(2) != "serious" || SeverityName(3) != "common" || SeverityName(4) != "info" || SeverityName(0) != "unknown" {
		t.Fatal("labels")
	}
	if DashedCode("0500010000030004") != "0500-0100-0003-0004" || DashedCode("03004000") != "03004000" {
		t.Fatal("dashed")
	}
}

func TestPreflightHMSRefusalCarriesTheText(t *testing.T) {
	text := "Not enough space on MicroSD Card. Video recording and timelapse recording cannot be performed. Please clear some space."
	in := idleFixture(t)
	in.Status.HMSErrors = []api.HMSError{{Code: "0x30004", FullCode: "0500010000030004", Severity: 1}}
	in.HMSText = map[string]string{"0500010000030004": text}
	r := Preflight(in)
	msg := strings.Join(r.Refusals, "|")
	for _, want := range []string{"0500-0100-0003-0004 (common, level 3)", "Not enough space on MicroSD Card", "notices rather than stops", "force: true"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal missing %q: %s", want, msg)
		}
	}
	if r.HMS[0].Text != text {
		t.Fatalf("the structured error should carry the text: %+v", r.HMS[0])
	}
	// A level 1 error gets the stronger wording.
	in.Status.HMSErrors = []api.HMSError{{Code: "0x10008", FullCode: "0300020000010008", Severity: 2}}
	if msg := strings.Join(Preflight(in).Refusals, "|"); !strings.Contains(msg, "stop or pause a print") {
		t.Fatalf("%s", msg)
	}
}

// ─── automatic ams_mapping ───────────────────────────────────────────────────

func TestAutoMapPicksNearestColourAmongLoadedTrays(t *testing.T) {
	in := idleFixture(t)
	in.Mapping = nil
	in.Needs = []Need{{SlotID: 1, Type: "PLA", Color: "#EC008C", Grams: 20}, {SlotID: 2, Type: "PLA", Color: "#000000", Grams: 30}}
	r := Preflight(in)
	if !r.Allowed || r.MappingSource != "auto" || len(r.AMSMapping) != 2 || r.AMSMapping[0] != 3 || r.AMSMapping[1] != 1 {
		t.Fatalf("magenta -> slot 3, black -> slot 1: %+v", r)
	}
	if r.Slots[0].ColorMatch != "exact" || r.Slots[1].ColorMatch != "exact" {
		t.Fatalf("%+v", r.Slots)
	}
}

func TestAutoMapLeavesUnusedFileSlotsAtMinusOne(t *testing.T) {
	in := idleFixture(t)
	in.Mapping = nil
	in.Needs = []Need{{SlotID: 3, Type: "PLA", Color: "#000000", Grams: 10}}
	r := Preflight(in)
	if len(r.AMSMapping) != 3 || r.AMSMapping[0] != -1 || r.AMSMapping[1] != -1 || r.AMSMapping[2] != 1 {
		t.Fatalf("%v", r.AMSMapping)
	}
}

func TestAutoMapRefusesWhenNoLoadedTrayMatches(t *testing.T) {
	in := idleFixture(t)
	in.Mapping = nil
	in.Needs = []Need{{SlotID: 1, Type: "PETG", Color: "#000000", Grams: 10}}
	r := Preflight(in)
	if r.Allowed || !strings.Contains(strings.Join(r.Refusals, "|"), "no loaded AMS tray holds PETG for filament slot 1") {
		t.Fatalf("%+v", r)
	}
}

func TestAutoMapWarnsWhenTheBestColourIsFarOff(t *testing.T) {
	in := idleFixture(t)
	in.Mapping = nil
	in.Needs = []Need{{SlotID: 1, Type: "PLA", Color: "#0086D6", Grams: 10}} // cyan; only black and magenta are loaded
	r := Preflight(in)
	if !r.Allowed || !strings.Contains(strings.Join(r.Warnings, "|"), "pass ams_mapping to choose a different tray") {
		t.Fatalf("a poor auto match should be allowed but called out: %+v", r)
	}
}

func TestAutoMapPrefersATrayWhoseSpoolHasTheGrams(t *testing.T) {
	st, views, _ := fixtures(t)
	// Make both trays black, and give tray 3's spool almost nothing.
	st.AMS[0].Trays[3].Color = "000000FF"
	for i := range views {
		if views[i].ID == 8 {
			views[i].RemainingG = 5
		}
	}
	l := NewLookup(1, load[[]api.SlotAssignment](t, "assignments.json"), views)
	m, un := autoMap([]Need{{SlotID: 1, Type: "PLA", Color: "000000", Grams: 40}}, loadedTrays(st), l)
	if len(un) != 0 || m[0] != 1 {
		t.Fatalf("slot 1 has the grams, slot 3 does not: %v %v", m, un)
	}
}

func TestSharedTrayGramsAddUp(t *testing.T) {
	in := idleFixture(t)
	in.Needs = []Need{{SlotID: 1, Type: "PLA", Color: "000000", Grams: 500}, {SlotID: 2, Type: "PLA", Color: "000000", Grams: 500}}
	in.Mapping = []int{1, 1} // both filaments draw on spool 9, which has 765 g
	r := Preflight(in)
	if r.Allowed || !strings.Contains(strings.Join(r.Refusals, "|"), "has 765 g but filament slot 2 needs 1000 g") {
		t.Fatalf("one spool cannot cover 500 + 500 of 765: %+v", r)
	}
}

// ─── status extras ───────────────────────────────────────────────────────────

func TestNowLine(t *testing.T) {
	st, _, _ := fixtures(t)
	st.State, st.StageName = "RUNNING", "Heatbed preheating"
	st.Temperatures.Nozzle, st.Temperatures.NozzleTarget, st.Temperatures.Bed, st.Temperatures.BedTarget = 75, 220, 40, 55
	if got := NowLine(st); got != "RUNNING, Heatbed preheating, nozzle 75/220 °C, bed 40/55 °C" {
		t.Fatal(got)
	}
	st.Temperatures = api.Temps{}
	st.StageName = ""
	if got := NowLine(st); got != "RUNNING" {
		t.Fatal(got)
	}
}

func TestLastJobAndNextStep(t *testing.T) {
	st, _, _ := fixtures(t)
	st.State, st.HMSErrors, st.AwaitingPlateClear = "FINISH", nil, true
	s := Summarize(st, nil)
	items := load[[]api.QueueItem](t, "queue_items.json")
	s.AttachLastJob(items)
	if s.LastJob == nil || s.LastJob.ID != 29 || s.LastJob.Status != "completed" || s.LastJob.FilamentG != 19.37 {
		t.Fatalf("newest finished job is 29, and item 30 is still printing so it is not the last job: %+v", s.LastJob)
	}
	if !strings.Contains(s.NextStep, "clear_plate") || !strings.Contains(s.NextStep, "ask them first") {
		t.Fatalf("%q", s.NextStep)
	}
	if !s.PlateClearPending {
		t.Fatal("awaiting_plate_clear")
	}
	b, _ := json.Marshal(s)
	if !strings.Contains(string(b), `"awaiting_plate_clear":true`) {
		t.Fatalf("flag missing from JSON: %s", b)
	}
	st.AwaitingPlateClear = false
	if got := Summarize(st, nil).NextStep; got != "Ready for print_file." {
		t.Fatal(got)
	}
	if b, _ := json.Marshal(Summarize(st, nil)); !strings.Contains(string(b), `"awaiting_plate_clear":false`) {
		t.Fatalf("the flag must be present when false too: %s", b)
	}
}

func TestNextStepByState(t *testing.T) {
	st, _, _ := fixtures(t)
	cases := []struct {
		state, want string
		hms         bool
		conn        bool
	}{
		{"RUNNING", "Printing", false, true},
		{"PAUSE", "print_resume", false, true},
		{"FAILED", "The last print failed", false, true},
		{"IDLE", "Ready for print_file", false, true},
		{"IDLE", "1 HMS error(s) are active", true, true},
		{"IDLE", "cannot reach the printer", false, false},
	}
	for _, c := range cases {
		x := st
		x.State, x.Connected, x.AwaitingPlateClear = c.state, c.conn, false
		if !c.hms {
			x.HMSErrors = nil
		}
		if got := Summarize(x, nil).NextStep; !strings.Contains(got, c.want) {
			t.Errorf("%s hms=%v: %q does not contain %q", c.state, c.hms, got, c.want)
		}
	}
}

// ─── filament maintenance ────────────────────────────────────────────────────

func TestCompareExtra(t *testing.T) {
	tag := map[string]string{"tag": `"00000000000000000000000000000004"`}
	if ok, alert := CompareExtra(tag, map[string]string{"tag": `"00000000000000000000000000000004"`}); !ok || alert != "" {
		t.Fatalf("identical extra: %v %q", ok, alert)
	}
	if ok, _ := CompareExtra(nil, map[string]string{}); !ok {
		t.Fatal("nil and empty are the same")
	}
	for name, after := range map[string]map[string]string{
		"dropped":   {},
		"nil":       nil,
		"changed":   {"tag": `"SOMETHINGELSE"`},
		"extra key": {"tag": `"00000000000000000000000000000004"`, "other": `"x"`},
	} {
		ok, alert := CompareExtra(tag, after)
		if ok || !strings.HasPrefix(alert, "ALERT:") || !strings.Contains(alert, "extra.") || !strings.Contains(alert, "AMS link") {
			t.Errorf("%s: ok=%v alert=%q", name, ok, alert)
		}
	}
	_, alert := CompareExtra(tag, nil)
	if !strings.Contains(alert, "00000000000000000000000000000004") || !strings.Contains(alert, "now missing") {
		t.Fatalf("the alert must carry the lost value so it can be restored: %q", alert)
	}
}

func TestTagSetFlagOnSpoolViews(t *testing.T) {
	raw := load[[]api.Spool](t, "spools.json")
	for _, s := range raw {
		if v := ViewOf(s); !v.TagSet || v.Tag == "" {
			t.Fatalf("every recorded spool was made by Bambuddy and carries a tag: %+v", v)
		}
	}
	raw[0].Extra = map[string]string{}
	v := ViewOf(raw[0])
	if v.TagSet {
		t.Fatal("no extra.tag means not tracked")
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"tag_set":false`) {
		t.Fatalf("tag_set must be present when false: %s", b)
	}
}

func TestFilamentHelpers(t *testing.T) {
	if d, ok := DensityFor(" pla "); !ok || d != 1.24 {
		t.Fatal("PLA density")
	}
	if d, ok := DensityFor("PLA Matte"); !ok || d != 1.24 {
		t.Fatal("a product line after the material should still find PLA")
	}
	if _, ok := DensityFor("Unobtainium"); ok {
		t.Fatal("an unknown material must have no default")
	}
	if MaterialWarning("PLA") != "" || MaterialWarning("PLA-CF") != "" || MaterialWarning("PLA+") != "" {
		t.Fatal("plain materials should not warn")
	}
	if w := MaterialWarning("PLA Matte"); !strings.Contains(w, "plain material") {
		t.Fatalf("%q", w)
	}
	vs := []api.Vendor{{ID: 1, Name: "Bambu Lab"}}
	if v, ok := FindVendor(vs, "  bambu LAB "); !ok || v.ID != 1 {
		t.Fatal("vendor match should ignore case and space")
	}
	if _, ok := FindVendor(vs, "Polymaker"); ok {
		t.Fatal("no match expected")
	}
	fs := []api.Filament{{ID: 2, Name: "Black", Material: "PLA", Vendor: &api.Vendor{ID: 1}}}
	if f, ok := FindDuplicate(fs, 1, "black", "pla"); !ok || f.ID != 2 {
		t.Fatal("duplicate")
	}
	if _, ok := FindDuplicate(fs, 1, "PLA Matte Black", "PLA"); ok {
		t.Fatal("a different name is not a duplicate")
	}
	if _, ok := FindDuplicate(fs, 2, "Black", "PLA"); ok {
		t.Fatal("a different vendor is not a duplicate")
	}
}

// ─── projects ────────────────────────────────────────────────────────────────

func TestProjectDetailFromRealFixture(t *testing.T) {
	d := ProjectDetailOf(load[api.Project](t, "project_detail.json"))
	if d.Name != "PixelBar" || d.Color != "#FF69B4" || d.Priority != "high" || d.Status != "active" || d.Tags != "pixelbar,mount" || d.DueDate != "2026-11-30" {
		t.Fatalf("%+v", d.ProjectRow)
	}
	if d.Notes != "v7 is the keeper" || !d.HasCover || d.Cover == "" || d.Description != "Wall mount and enclosure for the PixelBar" {
		t.Fatalf("%+v", d)
	}
	if len(d.Attachments) != 2 || d.Attachments[0].Name != "mount-notes.txt" || len(d.Attachments[0].Filename) < 30 {
		t.Fatalf("an attachment carries both its stored and original name: %+v", d.Attachments)
	}
	s := d.Stats
	if s == nil || s.Archives != 1 || s.Completed != 1 || s.BOMItems != 2 || s.BOMDone != 1 || s.BOMCost != 0.8 || *s.RemainingPrints != 2 || *s.ProgressPct != 33.3 {
		t.Fatalf("%+v", s)
	}
	if d.Archives != 1 || d.Completed != 1 || d.QueueItems != 0 {
		t.Fatalf("the detail view takes its counts from stats: %+v", d.ProjectRow)
	}
}

func TestProjectListRowFromRealFixture(t *testing.T) {
	rows := load[[]api.Project](t, "project_list.json")
	r := ProjectRowOf(rows[0])
	if r.Name != "PixelBar" || r.Archives != 1 || r.Completed != 1 || r.ProgressPct == nil || *r.ProgressPct != 33.3 || !r.HasCover {
		t.Fatalf("%+v", r)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "thumbnail") || strings.Contains(string(b), "archive_list") {
		t.Fatalf("a list row stays small: %s", b)
	}
	long := rows[0]
	text := strings.Repeat("x", 500)
	long.Description = &text
	if got := ProjectRowOf(long).Description; len([]rune(got)) > 201 {
		t.Fatalf("list descriptions are clipped, got %d chars", len(got))
	}
}

func TestBOMAndTimelineRows(t *testing.T) {
	bom := load[[]api.BOMItem](t, "project_bom.json")
	if len(bom) != 2 {
		t.Fatalf("%+v", bom)
	}
	a, b := BOMRowOf(bom[0]), BOMRowOf(bom[1])
	if a.Name != "M3x8 screws" || a.Needed != 8 || a.Acquired != 0 || a.Complete || a.UnitPrice == nil || a.SourcingURL == "" {
		t.Fatalf("%+v", a)
	}
	if b.Name != "Mounting bracket" || !b.Complete || b.ArchiveID == nil || *b.ArchiveID != 20 || b.Archive == "" {
		t.Fatalf("a line linked to a print shows it: %+v", b)
	}
	tl := TimelineRows(load[[]api.TimelineEvent](t, "project_timeline.json"))
	if len(tl) < 2 || tl[0].Type == "" || tl[0].Title == "" {
		t.Fatalf("%+v", tl)
	}
}

func TestNormalizeColor(t *testing.T) {
	for in, want := range map[string]string{"ff69b4": "#FF69B4", "#FF69B4": "#FF69B4", " #0a0b0c ": "#0A0B0C"} {
		if got, ok := NormalizeColor(in); !ok || got != want {
			t.Errorf("%q: %q %v", in, got, ok)
		}
	}
	// Bambuddy accepted "notacolor"; this must not.
	for _, bad := range []string{"", "notacolor", "#12345", "12345678", "ZZZZZZ"} {
		if _, ok := NormalizeColor(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestResolveAttachment(t *testing.T) {
	d := load[api.Project](t, "project_detail.json")
	first := d.Attachments[0].Filename
	if a, err := ResolveAttachment(d.Attachments, first); err != nil || a.Filename != first {
		t.Fatalf("the stored name resolves: %v", err)
	}
	// Both fixture attachments were uploaded as mount-notes.txt.
	_, err := ResolveAttachment(d.Attachments, "Mount-Notes.TXT")
	if err == nil || !strings.Contains(err.Error(), "2 attachments") || !strings.Contains(err.Error(), first) || !strings.Contains(err.Error(), d.Attachments[1].Filename) {
		t.Fatalf("a shared original name is ambiguous and lists both stored names: %v", err)
	}
	if _, err := ResolveAttachment(d.Attachments, "nope.txt"); err == nil || !strings.Contains(err.Error(), "no attachment called") {
		t.Fatalf("%v", err)
	}
	one := d.Attachments[:1]
	if a, err := ResolveAttachment(one, "MOUNT-NOTES.txt"); err != nil || a.Filename != first {
		t.Fatalf("a unique original name resolves, ignoring case: %v", err)
	}
}

func TestDeleteImpact(t *testing.T) {
	d := ProjectDetailOf(load[api.Project](t, "project_detail.json"))
	got := DeleteImpact(d, 2)
	for _, want := range []string{`"PixelBar"`, "1 archive(s)", "0 queue item(s)", "stay but lose their project", "2 BOM item(s)", "2 attachment(s)", "cover image"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
}
