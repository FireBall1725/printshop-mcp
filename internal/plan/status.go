// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"fmt"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// StatusSummary is the trimmed printer status handed to the model.
type StatusSummary struct {
	PrinterID         int           `json:"printer_id"`
	Name              string        `json:"name"`
	Connected         bool          `json:"connected"`
	State             string        `json:"state"`
	Job               string        `json:"job,omitempty"`
	ProgressPct       float64       `json:"progress_pct"`
	Layer             int           `json:"layer"`
	TotalLayers       int           `json:"total_layers"`
	RemainingMinutes  float64       `json:"remaining_minutes"`
	Stage             string        `json:"stage,omitempty"`
	Temps             TempSummary   `json:"temps_c"`
	DoorOpen          bool          `json:"door_open"`
	PlateClearPending bool          `json:"awaiting_plate_clear"`
	AMS               []TraySummary `json:"ams"`
	AMSUnits          []UnitSummary `json:"ams_units"`
	External          *TraySummary  `json:"external_spool,omitempty"`
	HMS               []HMSSummary  `json:"hms_errors"`
	LastJob           *LastJob      `json:"last_job,omitempty"`
	NextStep          string        `json:"next_step"`
}

// TempSummary groups current and target temperatures.
type TempSummary struct {
	Bed           float64 `json:"bed"`
	BedTarget     float64 `json:"bed_target"`
	Nozzle        float64 `json:"nozzle"`
	NozzleTarget  float64 `json:"nozzle_target"`
	Chamber       float64 `json:"chamber"`
	ChamberTarget float64 `json:"chamber_target"`
}

// UnitSummary is an AMS unit's environment and drying state. The drying
// numbers are the printer's own and are shown only while a cycle is active.
type UnitSummary struct {
	AMSID    int         `json:"ams_id"`
	Humidity float64     `json:"humidity"`
	TempC    float64     `json:"temp_c"`
	Drying   *DrySummary `json:"drying,omitempty"`
}

// DrySummary is an active drying cycle.
type DrySummary struct {
	Status   float64 `json:"status"`
	TimeLeft float64 `json:"time_left"`
	TargetC  float64 `json:"target_c"`
	Filament string  `json:"filament,omitempty"`
}

// TraySummary is one loaded AMS slot. Empty slots are omitted.
type TraySummary struct {
	Slot      int      `json:"slot"`
	AMSID     int      `json:"ams_id"`
	TrayID    int      `json:"tray_id"`
	Type      string   `json:"type"`
	SubBrand  string   `json:"sub_brand,omitempty"`
	Color     string   `json:"color"`
	RemainPct *float64 `json:"remaining_pct,omitempty"`
}

// Summarize trims a printer status. hmsText is keyed by full_code and may be nil.
func Summarize(st api.PrinterStatus, hmsText map[string]string) StatusSummary {
	job := st.SubtaskName
	if job == "" {
		job = st.CurrentPrint
	}
	s := StatusSummary{
		PrinterID: st.ID, Name: st.Name, Connected: st.Connected,
		State: strings.ToUpper(st.State), Job: job,
		ProgressPct: float64(st.Progress), Layer: st.LayerNum, TotalLayers: st.TotalLayers,
		RemainingMinutes: float64(st.RemainingTime), Stage: st.StageName,
		DoorOpen: st.DoorOpen, PlateClearPending: st.AwaitingPlateClear,
		Temps: TempSummary{
			Bed: float64(st.Temperatures.Bed), BedTarget: float64(st.Temperatures.BedTarget),
			Nozzle: float64(st.Temperatures.Nozzle), NozzleTarget: float64(st.Temperatures.NozzleTarget),
			Chamber: float64(st.Temperatures.Chamber), ChamberTarget: float64(st.Temperatures.ChamberTarget),
		},
		AMS: []TraySummary{}, AMSUnits: []UnitSummary{},
		HMS: []HMSSummary{},
	}
	for _, u := range st.AMS {
		us := UnitSummary{AMSID: u.ID, Humidity: float64(u.Humidity), TempC: float64(u.Temp)}
		if u.DryStatus > 0 || u.DryTime > 0 {
			us.Drying = &DrySummary{Status: float64(u.DryStatus), TimeLeft: float64(u.DryTime), TargetC: float64(u.DryTarget), Filament: u.DryFilament}
		}
		s.AMSUnits = append(s.AMSUnits, us)
		for _, t := range u.Trays {
			if !t.Exists || t.Type == "" {
				continue
			}
			s.AMS = append(s.AMS, traySummary(GlobalSlot(u.ID, t.ID), u.ID, t))
		}
	}
	for _, t := range st.VtTray {
		if t.Type != "" {
			ts := traySummary(254, 254, t)
			s.External = &ts
		}
	}
	s.HMS = HMSSummaries(st.HMSErrors, hmsText)
	s.NextStep = NextStep(s)
	return s
}

func traySummary(slot, amsID int, t api.Tray) TraySummary {
	ts := TraySummary{Slot: slot, AMSID: amsID, TrayID: t.ID, Type: t.Type, SubBrand: t.SubBrand, Color: NormHex(t.Color)}
	if t.TagUID != "" && t.Remain >= 0 {
		r := float64(t.Remain)
		ts.RemainPct = &r
	}
	return ts
}

// LastJob is the most recent finished, failed or cancelled queue item.
type LastJob struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	CompletedAt string  `json:"completed_at,omitempty"`
	Minutes     int     `json:"print_minutes,omitempty"`
	FilamentG   float64 `json:"filament_g,omitempty"`
	Error       string  `json:"error,omitempty"`
}

var finishedStatus = map[string]bool{"completed": true, "failed": true, "cancelled": true, "skipped": true}

// AttachLastJob sets the last finished job from queue items and refreshes the
// next-step hint, which can depend on it.
func (s *StatusSummary) AttachLastJob(items []api.QueueItem) {
	var best *api.QueueItem
	key := func(q *api.QueueItem) string {
		if q.CompletedAt != "" {
			return q.CompletedAt
		}
		return q.CreatedAt
	}
	for i := range items {
		it := &items[i]
		if finishedStatus[it.Status] && (best == nil || key(it) > key(best)) {
			best = it
		}
	}
	if best != nil {
		j := LastJob{ID: best.ID, Name: best.ArchiveName, Status: best.Status, CompletedAt: best.CompletedAt, Error: best.ErrorMessage}
		if j.Name == "" {
			j.Name = best.LibraryFileName
		}
		if best.PrintTimeSeconds != nil {
			j.Minutes = *best.PrintTimeSeconds / 60
		}
		if best.FilamentUsedGrams != nil {
			j.FilamentG = *best.FilamentUsedGrams
		}
		s.LastJob = &j
	}
	s.NextStep = NextStep(*s)
}

// NextStep says what makes sense next, from state alone.
func NextStep(s StatusSummary) string {
	var parts []string
	switch {
	case !s.Connected:
		return "Bambuddy cannot reach the printer; nothing can be sent until it reconnects."
	case s.State == "RUNNING":
		return "Printing. Check again with printer_status or queue_list."
	case s.State == "PAUSE":
		return "Paused. print_resume continues it; print_stop (ask the user first) abandons it."
	}
	if s.PlateClearPending {
		parts = append(parts, "Bambuddy is waiting for the plate: once the user has taken the print off, call clear_plate (ask them first) so the next print can start.")
	}
	if n := len(s.HMS); n > 0 {
		parts = append(parts, fmt.Sprintf("%d HMS error(s) are active; print_file refuses until they clear or force is true.", n))
	}
	if s.State == "FAILED" {
		msg := "The last print failed"
		if s.LastJob != nil && s.LastJob.Error != "" {
			msg += " (" + clip(s.LastJob.Error, 100) + ")"
		}
		parts = append(parts, msg+".")
	}
	if len(parts) == 0 {
		return "Ready for print_file."
	}
	return strings.Join(parts, " ")
}

// NowLine is a short live description for a result, such as
// "RUNNING, Heatbed preheating, nozzle 75/220 °C, bed 40/55 °C".
func NowLine(st api.PrinterStatus) string {
	parts := []string{strings.ToUpper(st.State)}
	if st.StageName != "" {
		parts = append(parts, st.StageName)
	}
	t := st.Temperatures
	for _, x := range []struct {
		name        string
		now, target api.Num
	}{{"nozzle", t.Nozzle, t.NozzleTarget}, {"bed", t.Bed, t.BedTarget}} {
		switch {
		case x.target > 0:
			parts = append(parts, fmt.Sprintf("%s %.0f/%.0f °C", x.name, float64(x.now), float64(x.target)))
		case x.now > 0:
			parts = append(parts, fmt.Sprintf("%s %.0f °C", x.name, float64(x.now)))
		}
	}
	return strings.Join(parts, ", ")
}
