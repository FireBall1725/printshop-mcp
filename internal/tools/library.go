// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/fireball1725/printshop-mcp/internal/api"
	"github.com/fireball1725/printshop-mcp/internal/plan"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	slicedSuffix  = ".gcode.3mf"
	maxUploadSize = 512 << 20
)

type libraryArgs struct {
	Name       string `json:"name,omitempty" jsonschema:"Case-insensitive substring of the file name."`
	SlicedOnly bool   `json:"sliced_only,omitempty" jsonschema:"Only .gcode.3mf files, the ones print_file accepts."`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum files to return. Defaults to 50."`
}

// FileRow is a library file as the model sees it.
type FileRow struct {
	ThumbnailURL     string  `json:"thumbnail_url,omitempty"`
	ID               int     `json:"id"`
	Filename         string  `json:"filename"`
	FileType         string  `json:"file_type"`
	SizeKB           int64   `json:"size_kb"`
	SlicedForModel   string  `json:"sliced_for_model,omitempty"`
	PrintTimeMinutes int     `json:"print_time_minutes,omitempty"`
	FilamentG        float64 `json:"filament_g,omitempty"`
	PrintCount       int     `json:"print_count,omitempty"`
}

type libraryResult struct {
	Files     []FileRow `json:"files"`
	Total     int       `json:"total"`
	Truncated bool      `json:"truncated,omitempty"`
}

type uploadArgs struct {
	Path string `json:"path" jsonschema:"Path to a sliced .gcode.3mf on the machine running this server. A leading ~/ is expanded."`
}

// FilamentRow is one filament slot read from a sliced file.
type FilamentRow struct {
	SlotID int     `json:"slot_id"`
	Type   string  `json:"type"`
	Color  string  `json:"color"`
	Grams  float64 `json:"grams"`
}

type uploadResult struct {
	ID               int           `json:"id"`
	Filename         string        `json:"filename"`
	DuplicateOf      *int          `json:"duplicate_of,omitempty"`
	SlicedForModel   string        `json:"sliced_for_model,omitempty"`
	PrintTimeMinutes int           `json:"print_time_minutes,omitempty"`
	FilamentG        float64       `json:"filament_g,omitempty"`
	Filaments        []FilamentRow `json:"filaments"`
	LayerHeight      float64       `json:"layer_height,omitempty"`
	NozzleMM         float64       `json:"nozzle_mm,omitempty"`
	ThumbnailURL     string        `json:"thumbnail_url,omitempty"`
	Warnings         []string      `json:"warnings,omitempty"`
}

func addLibraryTools(srv *mcp.Server, d *Deps) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "list_library",
		Description: "List files in the Bambuddy library with the model each was sliced for, print time and filament grams. " +
			"Use the id with print_file or stock_for_print. Only .gcode.3mf files are sliced; STL and plain 3MF cannot be printed.",
		Annotations: readOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a libraryArgs) (*mcp.CallToolResult, libraryResult, error) {
		var files []api.LibraryFile
		if _, err := d.Bambuddy.Get(ctx, "/api/v1/library/files/", nil, &files); err != nil {
			return nil, libraryResult{}, toolError(err)
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 50
		}
		res := libraryResult{Files: []FileRow{}}
		for _, f := range files {
			if a.Name != "" && !strings.Contains(strings.ToLower(f.Filename), strings.ToLower(a.Name)) {
				continue
			}
			if a.SlicedOnly && f.FileType != "gcode.3mf" {
				continue
			}
			res.Total++
			if len(res.Files) >= limit {
				res.Truncated = true
				continue
			}
			row := FileRow{ID: f.ID, Filename: f.Filename, FileType: f.FileType, SizeKB: f.FileSize / 1024, PrintCount: f.PrintCount, ThumbnailURL: thumbnailURL(d, f.ID, f.ThumbnailPath)}
			if f.SlicedForModel != nil {
				row.SlicedForModel = *f.SlicedForModel
			}
			if f.PrintTimeSeconds != nil {
				row.PrintTimeMinutes = *f.PrintTimeSeconds / 60
			}
			if f.FilamentUsedGrams != nil {
				row.FilamentG = *f.FilamentUsedGrams
			}
			res.Files = append(res.Files, row)
		}
		return nil, res, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name: "upload_file",
		Description: "Upload a sliced .gcode.3mf from this machine into the Bambuddy library. Returns the new library id plus the print time, grams, " +
			"sliced-for model and filament slots Bambuddy read from the file. Does not start a print; use print_file for that. Only .gcode.3mf is accepted.",
		Annotations: writes(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, a uploadArgs) (*mcp.CallToolResult, uploadResult, error) {
		path := a.Path
		if strings.HasPrefix(path, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, uploadResult{}, err
			}
			path = filepath.Join(home, path[2:])
		}
		if !strings.HasSuffix(strings.ToLower(path), slicedSuffix) {
			return nil, uploadResult{}, fmt.Errorf("only sliced %s files are accepted", slicedSuffix)
		}
		fh, err := os.Open(path)
		if err != nil {
			return nil, uploadResult{}, fmt.Errorf("cannot open file: %w", err)
		}
		defer func() { _ = fh.Close() }()
		info, err := fh.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, uploadResult{}, fmt.Errorf("%s is not a regular file", filepath.Base(path))
		}
		if info.Size() > maxUploadSize {
			return nil, uploadResult{}, fmt.Errorf("file is %d MB; the limit is %d MB", info.Size()>>20, maxUploadSize>>20)
		}
		var up api.UploadResult
		if err := d.Bambuddy.Upload(ctx, "/api/v1/library/files/", filepath.Base(path), fh, &up); err != nil {
			return nil, uploadResult{}, toolError(err)
		}
		res := uploadResult{ID: up.ID, Filename: up.Filename, DuplicateOf: up.DuplicateOf, Filaments: []FilamentRow{}, ThumbnailURL: thumbnailURL(d, up.ID, up.ThumbnailPath)}
		if m := up.Metadata; m != nil {
			res.SlicedForModel = m.SlicedForModel
			res.PrintTimeMinutes = m.PrintTimeSeconds / 60
			res.FilamentG = m.FilamentUsedGrams
			res.LayerHeight, res.NozzleMM = float64(m.LayerHeight), float64(m.NozzleDiameter)
			for _, s := range m.FilamentSlots {
				res.Filaments = append(res.Filaments, FilamentRow{SlotID: s.SlotID, Type: s.Type, Color: s.Color, Grams: s.UsedG})
			}
			if !strings.EqualFold(m.SlicedForModel, d.Model) {
				res.Warnings = append(res.Warnings, fmt.Sprintf("sliced for %q, not %s; print_file will refuse it", m.SlicedForModel, d.Model))
			}
		} else {
			res.Warnings = append(res.Warnings, "Bambuddy returned no metadata for this file")
		}
		if up.DuplicateOf != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("identical to library file %d", *up.DuplicateOf))
		}
		return nil, res, nil
	})
}

// fileDetail fetches one library file.
func fileDetail(ctx context.Context, d *Deps, id int) (api.LibraryFile, error) {
	var f api.LibraryFile
	_, err := d.Bambuddy.Get(ctx, "/api/v1/library/files/"+itoa(id), nil, &f)
	return f, toolError(err)
}

// fileNeeds reads the filament a plate uses from Bambuddy's requirements
// endpoint, which reports per-plate grams.
func fileNeeds(ctx context.Context, d *Deps, id, plate int) ([]plan.Need, error) {
	var req api.Requirements
	q := url.Values{"plate_id": {itoa(plate)}}
	if _, err := d.Bambuddy.Get(ctx, "/api/v1/library/files/"+itoa(id)+"/filament-requirements", q, &req); err != nil {
		return nil, toolError(err)
	}
	var used, all []plan.Need
	for _, r := range req.Filaments {
		n := plan.Need{SlotID: r.SlotID, Type: r.Type, Color: r.Color, Grams: r.UsedGrams}
		all = append(all, n)
		if r.UsedInPlate {
			used = append(used, n)
		}
	}
	if len(used) == 0 {
		return all, nil
	}
	return used, nil
}
