// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

// Command smoke runs the built printshop-mcp binary over stdio against the real
// Bambuddy and Spoolman and calls read-only tools. It refuses to call any tool
// that is not on the allowlist below, so it cannot start, stop or change
// anything. Usage: go build -o printshop-mcp ./cmd/printshop-mcp && go run ./scripts/smoke
//
// With -preflight it also calls print_file with dry_run set, which runs every
// safety check and writes nothing.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var readOnly = map[string]bool{
	"printer_status": true, "list_library": true, "queue_list": true, "print_history": true,
	"list_spools": true, "list_filaments": true, "list_vendors": true, "get_spool": true,
	"stock_for_print": true, "ams_vs_stock": true,
	"list_printers": true, "list_printable_objects": true, "list_archives": true, "get_archive": true,
	"archive_stats": true, "maintenance_overview": true, "ams_history": true,
}

func main() {
	bin := flag.String("bin", "./printshop-mcp", "path to the built server")
	preflight := flag.Bool("preflight", false, "also call print_file with dry_run (reads only; runs the safety checks)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "smoke", Version: "0"}, nil)
	cs, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(*bin)}, nil)
	if err != nil {
		fail("cannot start %s: %v", *bin, err)
	}
	defer func() { _ = cs.Close() }()

	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		fail("list tools: %v", err)
	}
	fmt.Printf("== %d tools registered\n", len(list.Tools))

	run := func(name string, args map[string]any) map[string]any {
		dryPreflight := name == "print_file" && *preflight && args["dry_run"] == true
		if !readOnly[name] && !dryPreflight {
			fail("%s is not on the read-only allowlist", name)
		}
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			fail("%s: %v", name, err)
		}
		if res.IsError {
			fail("%s returned an error: %v", name, res.Content)
		}
		out, _ := res.StructuredContent.(map[string]any)
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Printf("\n== %s %v\n%s\n", name, args, b)
		return out
	}

	run("printer_status", nil)
	run("list_spools", map[string]any{"in_stock_only": true})
	lib := run("list_library", map[string]any{"sliced_only": true})
	files, _ := lib["files"].([]any)
	if len(files) == 0 {
		fail("the library has no sliced file to check stock against")
	}
	id := int(files[0].(map[string]any)["id"].(float64))
	run("stock_for_print", map[string]any{"library_file_id": id})
	run("ams_vs_stock", nil)
	run("queue_list", nil)
	run("print_history", map[string]any{"limit": 3})
	run("list_printers", nil)
	run("list_printable_objects", nil)
	arch := run("list_archives", map[string]any{"limit": 2})
	if rows, _ := arch["archives"].([]any); len(rows) > 0 {
		run("get_archive", map[string]any{"archive_id": int(rows[0].(map[string]any)["id"].(float64))})
	}
	run("archive_stats", nil)
	run("maintenance_overview", nil)
	run("ams_history", map[string]any{"ams_id": 0, "hours": 6})
	if *preflight {
		run("print_file", map[string]any{"library_file_id": id, "dry_run": true})
	}
	fmt.Println("\nsmoke ok")
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "smoke FAILED: "+format+"\n", a...)
	os.Exit(1)
}
