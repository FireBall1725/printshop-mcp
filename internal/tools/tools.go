// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Package tools is the MCP tool surface. Each tool validates its arguments,
// calls the Bambuddy or Spoolman REST API, and returns a trimmed result.
//
// Bambuddy already links itself to Spoolman: it creates spools there, stores
// the AMS tray_uuid in each spool's extra.tag, keeps its own slot assignments,
// and (sync mode auto) writes weights and print usage back. These tools read
// that link and never edit it: update_spool does not accept extra or
// filament_id, and use_filament warns against logging usage Bambuddy reports.
//
// Deliberately absent: slicing, camera, timelapse, firmware, and every DELETE.
// This server cannot delete a spool, filament, vendor or library file.
package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/hms"
	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// DefaultPrinterID is the only printer today.
const DefaultPrinterID = 1

// Deps is what every tool needs.
type Deps struct {
	Bambuddy *api.Client
	Spoolman *api.Client
	// Model is the printer model print_file requires a file to be sliced for.
	Model string
	// HMS describes HMS codes. May be nil, which describes nothing.
	HMS *hms.Lookup
	// BambuddyURL is the origin, used to build thumbnail links.
	BambuddyURL string
	// PollEvery is how often print_file re-reads the printer while waiting
	// for a print to start. Zero means two seconds.
	PollEvery time.Duration
}

func (d *Deps) pollEvery() time.Duration {
	if d.PollEvery > 0 {
		return d.PollEvery
	}
	return 2 * time.Second
}

// hmsText fetches the published text for the printer's active HMS codes.
func hmsText(ctx context.Context, d *Deps, st api.PrinterStatus) map[string]string {
	return d.HMS.Describe(ctx, plan.HMSCodes(st.HMSErrors))
}

func thumbnailURL(d *Deps, id int, path *string) string {
	if path == nil || *path == "" || d.BambuddyURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/api/v1/library/files/%d/thumbnail", d.BambuddyURL, id)
}

// RegisterAll wires every tool onto the server.
func RegisterAll(srv *mcp.Server, d *Deps) {
	addPrinterTools(srv, d)
	addPrintControlTools(srv, d)
	addLibraryTools(srv, d)
	addPrintTools(srv, d)
	addQueueTools(srv, d)
	addSpoolTools(srv, d)
	addFilamentTools(srv, d)
	addMatchTools(srv, d)
	addControlTools(srv, d)
	addRecordTools(srv, d)
	addProjectTools(srv, d)
}

func ptr[T any](v T) *T { return &v }

func readOnly() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
}

func destructive() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(false)}
}

func writes() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
}

func printerID(id int) int {
	if id <= 0 {
		return DefaultPrinterID
	}
	return id
}

// toolError turns an API failure into something the model can act on.
func toolError(err error) error {
	var e *api.Error
	if !errors.As(err, &e) {
		return err
	}
	switch e.Status {
	case http.StatusUnauthorized, http.StatusForbidden:
		if e.Service == "bambuddy" {
			return fmt.Errorf("bambuddy refused the request (%d): auth may have been turned on; set BAMBUDDY_API_KEY", e.Status)
		}
		return e
	case http.StatusNotFound:
		return fmt.Errorf("not found in %s: %s", e.Service, e.Message)
	}
	return e
}

func statusFor(ctx context.Context, d *Deps, id int) (api.PrinterStatus, error) {
	var st api.PrinterStatus
	_, err := d.Bambuddy.Get(ctx, "/api/v1/printers/"+strconv.Itoa(id)+"/status", nil, &st)
	return st, toolError(err)
}

// allSpools pages through every Spoolman spool, archived ones included.
func allSpools(ctx context.Context, d *Deps) ([]api.Spool, error) {
	const page = 200
	var all []api.Spool
	for offset := 0; ; offset += page {
		var batch []api.Spool
		q := url.Values{"allow_archived": {"true"}, "limit": {strconv.Itoa(page)}, "offset": {strconv.Itoa(offset)}}
		h, err := d.Spoolman.Get(ctx, "/api/v1/spool", q, &batch)
		if err != nil {
			return nil, toolError(err)
		}
		all = append(all, batch...)
		total, _ := strconv.Atoi(h.Get("X-Total-Count"))
		if len(batch) == 0 || len(all) >= total {
			return all, nil
		}
	}
}

func spoolViews(spools []api.Spool) []plan.SpoolView {
	out := make([]plan.SpoolView, len(spools))
	for i, s := range spools {
		out[i] = plan.ViewOf(s)
	}
	return out
}

func slotAssignments(ctx context.Context, d *Deps) ([]api.SlotAssignment, error) {
	var a []api.SlotAssignment
	_, err := d.Bambuddy.Get(ctx, "/api/v1/spoolman/inventory/slot-assignments/all", nil, &a)
	return a, toolError(err)
}

func itoa(i int) string { return strconv.Itoa(i) }
