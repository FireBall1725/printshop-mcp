// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package plan

import (
	"math"

	"github.com/fireball1725/printshop-mcp/internal/api"
)

// PrinterRow is a printer as the model sees it. No serial, IP or access code.
type PrinterRow struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	Location    string `json:"location,omitempty"`
	Active      bool   `json:"active"`
	NozzleCount int    `json:"nozzle_count,omitempty"`
}

// PrinterRows trims printer rows.
func PrinterRows(in []api.PrinterInfo) []PrinterRow {
	out := make([]PrinterRow, 0, len(in))
	for _, p := range in {
		out = append(out, PrinterRow{ID: p.ID, Name: p.Name, Model: p.Model, Location: p.Location, Active: p.IsActive, NozzleCount: p.NozzleCount})
	}
	return out
}

// FilamentUse is grams of one filament in a finished print.
type FilamentUse struct {
	SlotID int     `json:"slot_id"`
	Type   string  `json:"type"`
	Color  string  `json:"color"`
	Grams  float64 `json:"grams"`
}

// ArchiveRow is a finished-print record.
type ArchiveRow struct {
	ID              int           `json:"id"`
	Name            string        `json:"name"`
	Status          string        `json:"status"`
	StartedAt       string        `json:"started_at,omitempty"`
	CompletedAt     string        `json:"completed_at,omitempty"`
	PlannedMinutes  int           `json:"planned_minutes,omitempty"`
	ActualMinutes   int           `json:"actual_minutes,omitempty"`
	TimeAccuracyPct float64       `json:"time_accuracy_pct,omitempty"`
	FilamentG       float64       `json:"filament_g,omitempty"`
	FilamentType    string        `json:"filament_type,omitempty"`
	Color           string        `json:"color,omitempty"`
	LayerHeight     float64       `json:"layer_height,omitempty"`
	Plate           int           `json:"plate,omitempty"`
	SlicedForModel  string        `json:"sliced_for_model,omitempty"`
	FailureReason   string        `json:"failure_reason,omitempty"`
	Notes           string        `json:"notes,omitempty"`
	Filaments       []FilamentUse `json:"filaments,omitempty"`
}

// ArchiveRowOf trims an archive. withSlots adds per-filament grams.
func ArchiveRowOf(a api.Archive, withSlots bool) ArchiveRow {
	r := ArchiveRow{
		ID: a.ID, Name: a.PrintName, Status: a.Status, StartedAt: a.StartedAt, CompletedAt: a.CompletedAt,
		FilamentType: a.FilamentType, Color: NormHex(a.FilamentColor), LayerHeight: float64(a.LayerHeight),
		SlicedForModel: a.SlicedForModel, FailureReason: a.FailureReason, Notes: a.Notes,
	}
	if r.Name == "" {
		r.Name = a.Filename
	}
	if a.PlateID != nil {
		r.Plate = *a.PlateID
	}
	if a.PrintTimeSeconds != nil {
		r.PlannedMinutes = *a.PrintTimeSeconds / 60
	}
	if a.ActualTimeSeconds != nil {
		r.ActualMinutes = *a.ActualTimeSeconds / 60
	}
	if a.TimeAccuracy != nil {
		r.TimeAccuracyPct = round1(*a.TimeAccuracy)
	}
	if a.FilamentUsedGrams != nil {
		r.FilamentG = *a.FilamentUsedGrams
	}
	if withSlots && a.ExtraData != nil {
		for _, s := range a.ExtraData.FilamentSlots {
			r.Filaments = append(r.Filaments, FilamentUse{SlotID: s.SlotID, Type: s.Type, Color: NormHex(s.Color), Grams: s.UsedG})
		}
	}
	return r
}

// StatsRow is the aggregate print statistics.
type StatsRow struct {
	TotalPrints     int            `json:"total_prints"`
	Successful      int            `json:"successful"`
	Failed          int            `json:"failed"`
	Cancelled       int            `json:"cancelled"`
	SuccessRatePct  float64        `json:"success_rate_pct"`
	PrintHours      float64        `json:"print_hours"`
	FilamentG       float64        `json:"filament_g"`
	Cost            float64        `json:"cost"`
	ByFilamentType  map[string]int `json:"prints_by_filament_type,omitempty"`
	TimeAccuracyPct float64        `json:"avg_time_accuracy_pct,omitempty"`
	EnergyKWh       float64        `json:"energy_kwh,omitempty"`
}

// StatsOf trims archive statistics.
func StatsOf(s api.ArchiveStats) StatsRow {
	r := StatsRow{
		TotalPrints: s.TotalPrints, Successful: s.Successful, Failed: s.Failed, Cancelled: s.Cancelled,
		PrintHours: round1(s.TotalHours), FilamentG: round1(s.TotalGrams), Cost: math.Round(s.TotalCost*100) / 100,
		ByFilamentType: s.ByFilamentType, TimeAccuracyPct: round1(s.AverageAccuracy), EnergyKWh: s.TotalEnergyKWh,
	}
	if s.TotalPrints > 0 {
		r.SuccessRatePct = round1(float64(s.Successful) / float64(s.TotalPrints) * 100)
	}
	return r
}

// MaintTask is one maintenance task.
type MaintTask struct {
	ID            int      `json:"id"`
	Name          string   `json:"name"`
	IntervalType  string   `json:"interval_type"`
	IntervalHours float64  `json:"interval_hours,omitempty"`
	HoursUntilDue *float64 `json:"hours_until_due,omitempty"`
	DaysUntilDue  *float64 `json:"days_until_due,omitempty"`
	Due           bool     `json:"due"`
	Warning       bool     `json:"warning"`
	LastPerformed string   `json:"last_performed,omitempty"`
}

// MaintPrinter is a printer's maintenance state.
type MaintPrinter struct {
	PrinterID  int         `json:"printer_id"`
	Name       string      `json:"name"`
	PrintHours float64     `json:"print_hours"`
	Due        int         `json:"due"`
	Warning    int         `json:"warning"`
	Tasks      []MaintTask `json:"tasks"`
}

// MaintenanceReport trims the overview. Disabled tasks are dropped. With
// onlyDue, only tasks that are due or in warning remain. printerID 0 means all.
func MaintenanceReport(in []api.MaintenancePrinter, printerID int, onlyDue bool) []MaintPrinter {
	out := []MaintPrinter{}
	for _, p := range in {
		if printerID > 0 && p.PrinterID != printerID {
			continue
		}
		mp := MaintPrinter{PrinterID: p.PrinterID, Name: p.Name, PrintHours: round1(p.Hours), Tasks: []MaintTask{}}
		for _, it := range p.Items {
			if !it.Enabled {
				continue
			}
			if it.IsDue {
				mp.Due++
			}
			if it.IsWarning {
				mp.Warning++
			}
			if onlyDue && !it.IsDue && !it.IsWarning {
				continue
			}
			t := MaintTask{ID: it.ID, Name: it.Name, IntervalType: it.IntervalType, HoursUntilDue: roundPtr(it.HoursUntilDue),
				DaysUntilDue: roundPtr(it.DaysUntilDue), Due: it.IsDue, Warning: it.IsWarning, LastPerformed: it.LastPerformedAt}
			if it.IntervalType == "hours" {
				t.IntervalHours = it.IntervalHours
			}
			mp.Tasks = append(mp.Tasks, t)
		}
		out = append(out, mp)
	}
	return out
}

func roundPtr(f *float64) *float64 {
	if f == nil {
		return nil
	}
	v := round1(*f)
	return &v
}

// HistorySummary is AMS humidity and temperature over a window.
type HistorySummary struct {
	Points       int              `json:"points"`
	From         string           `json:"from,omitempty"`
	To           string           `json:"to,omitempty"`
	Humidity     Range            `json:"humidity"`
	TemperatureC Range            `json:"temperature_c"`
	Samples      []api.AMSReading `json:"samples"`
}

// Range is min, max, average and the latest reading.
type Range struct {
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Avg    float64 `json:"avg"`
	Latest float64 `json:"latest"`
}

// SummarizeHistory keeps at most n evenly spaced samples, always including the
// newest, and reports the range Bambuddy computed over the whole window.
func SummarizeHistory(h api.AMSHistory, n int) HistorySummary {
	s := HistorySummary{
		Points:       len(h.Data),
		Humidity:     Range{Min: h.MinHumidity, Max: h.MaxHumidity, Avg: round1(h.AvgHumidity)},
		TemperatureC: Range{Min: h.MinTemp, Max: h.MaxTemp, Avg: round1(h.AvgTemp)},
		Samples:      []api.AMSReading{},
	}
	if len(h.Data) == 0 {
		return s
	}
	last := h.Data[len(h.Data)-1]
	s.From, s.To = h.Data[0].RecordedAt, last.RecordedAt
	s.Humidity.Latest, s.TemperatureC.Latest = last.Humidity, last.Temperature
	if n < 2 || len(h.Data) <= n {
		s.Samples = append(s.Samples, h.Data...)
		return s
	}
	for i := 0; i < n; i++ {
		s.Samples = append(s.Samples, h.Data[i*(len(h.Data)-1)/(n-1)])
	}
	return s
}
