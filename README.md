# printshop-mcp

An MCP server for [Bambuddy](https://github.com/maziggy/bambuddy) (print manager for a Bambu Lab X1C) and [Spoolman](https://github.com/Donkie/Spoolman) (filament inventory). Claude can see what filament is in stock, check the printer, send a sliced 3MF to it, and watch the job.

Bambuddy already links itself to Spoolman. It creates the spools, stores each AMS tray's RFID uuid in the spool's `extra.tag`, keeps its own slot assignments, and writes weights and print usage back. This server reads that link and leaves it alone: no tool writes `extra`, and `use_filament` is for prints Bambuddy didn't run. `update_spool` can move a spool to another filament (for example from "Black" to "PLA Matte Black"); that is a PATCH of `filament_id` alone, and the spool is read back to confirm `extra.tag` is unchanged. `create_filament` and `update_filament` keep the material plain ("PLA") and put the product line in the name, because `stock_for_print` and `print_file` match on material first. Bambuddy may recreate or rename filaments on its next AMS sync, so re-check after one.

## Tools

| Group | Tools |
| --- | --- |
| Printer | `list_printers`, `printer_status`, `print_pause`, `print_resume`, `print_stop`, `print_speed`, `clear_plate`, `clear_hms` |
| Mid-print | `list_printable_objects`, `skip_objects` |
| AMS | `start_drying`, `stop_drying`, `ams_history`, `assign_spool_to_slot`, `unassign_spool` |
| Files and queue | `list_library`, `upload_file`, `print_file`, `queue_list`, `queue_cancel`, `print_history` |
| Records | `list_archives`, `get_archive`, `archive_stats`, `maintenance_overview` |
| Projects | `list_projects`, `get_project`, `create_project`, `update_project`, `delete_project`, `project_add_items`, `project_remove_archives`, `list_project_templates`, `create_project_from_template`, `save_project_as_template` |
| Project BOM | `bom_add_item`, `bom_update_item`, `bom_delete_item` |
| Project files | `project_upload_file`, `project_download_file`, `project_delete_file`, `export_project`, `import_project` |
| Filament | `list_spools`, `list_filaments`, `list_vendors`, `get_spool`, `use_filament`, `measure_spool`, `update_spool`, `create_spool`, `create_filament`, `update_filament` |
| Matching | `stock_for_print`, `ams_vs_stock` |

`print_file` queues a sliced `.gcode.3mf` and refuses unless all of these hold:

- The printer is `IDLE` or `FINISH`. The result says what it was doing otherwise.
- No HMS errors are active. They are returned with Bambu's text and alert level, and `force: true` overrides this check only.
- The file was sliced for the configured model (`X1C`).
- Each filament maps to a loaded AMS tray of the same material, and the linked spool has the grams. Without an `ams_mapping`, the tray is chosen per filament by material and nearest colour, and the choice is reported. When several trays match equally, the one with the least filament left is chosen, so a nearly empty spool is used up before the AMS moves on to its twin. A tray with no linked spool is never picked as the nearly empty one; the AMS remaining percent ranks those only when none of the matching trays is linked.
- Trays on the same printer with the same material, colour and product line or vendor are a group, because the AMS switches to the next one by itself when a spool runs out. The mapped spool and its backups are added together against the need, and a backup with no linked spool adds nothing. If the mapped spool alone is short but the group covers it, the print is allowed with a warning that names the backup tray. A group short by up to 10% of the need, or with under 10% to spare, warns instead of blocking; short by more is refused. Filaments in one file that land in the same group share its grams. `dry_run` returns the groups it found (`groups`) and why each tray was chosen (`mapping_reasons`).

After queuing, `print_file` waits up to 30 seconds for the printer to leave `IDLE` or `FINISH` and reports its real state. `started` is false, with the reason, if it never does.

`print_stop`, `queue_cancel` and `skip_objects` tell Claude to ask first, and also need `confirm: true`; so do `clear_plate` (a claim about the physical plate) and `clear_hms` (it hides an error without fixing its cause). `skip_objects` refuses an unknown or already skipped object, and one that would skip everything. The project deletes (`delete_project`, `bom_delete_item`, `project_delete_file`) are gated the same way, and without `confirm` they delete nothing and say what would go. No tool deletes a spool, filament, library file or archive; the only deletes are the three project ones above. `unassign_spool` removes only Bambuddy's local slot link. `list_printers` never returns the printer's access code, serial or IP. Slicing, camera, timelapse and firmware are not covered.

## Configuration

| Variable | Default | |
| --- | --- | --- |
| `BAMBUDDY_URL` | `http://localhost:8000` | Origin, without `/api/v1` |
| `BAMBUDDY_API_KEY` | none | Sent as `X-API-Key` when set. Never logged. |
| `SPOOLMAN_URL` | `http://localhost:7912` | Origin, without `/api/v1` |
| `PRINTSHOP_PRINTER_MODEL` | `X1C` | Model a file must be sliced for |
| `PRINTSHOP_HMS_LOOKUP` | `on` | `off` stops HMS codes being looked up. See below. |
| `PRINTSHOP_MCP_LISTEN` | none | Serve streamable HTTP at `/mcp` on this address instead of stdio |
| `PRINTSHOP_MCP_TOKEN` | none | Bearer token for HTTP mode. Required with `PRINTSHOP_MCP_LISTEN`. |

## Projects

A project groups prints, a bill of materials, notes and files, for example one per build. `print_file` takes a `project_id` and files the print under it, and `project_add_items` files finished prints and queue items afterwards. Bambuddy has no call to take a queue item back out of a project, so adding one can't be undone from here. Deleting a project keeps its prints and queue items; they just lose the project.

Attachments are stored under a random name, so `get_project` shows both that and the original, and the download and delete tools take either. File paths are on the machine running this server. Downloads and exports never overwrite a file unless asked, and a cover image is only replaced with `replace: true`.

## HMS text

Bambuddy 1.2.5.1 returns HMS errors as bare codes. This server looks each one up in Bambu's public code list (`e.bambulab.com`) and caches the answer. Only the hex code is sent: no serial, address or access code. The alert level is read from the code itself, because Bambuddy 1.2.5.1 reports the code's part byte as `severity`; its raw number is still shown as `bambuddy_severity`.

## Use with Claude Code

```sh
go build -o printshop-mcp ./cmd/printshop-mcp
claude mcp add printshop --scope user \
  -e BAMBUDDY_URL=http://bambuddy.local:8000 \
  -e SPOOLMAN_URL=http://spoolman.local:7912 \
  -- "$PWD/printshop-mcp"
```

The name comes first because `-e` takes several values. The URLs are your own Bambuddy and Spoolman; the defaults assume both run on the same machine. Add `-e BAMBUDDY_API_KEY=...` if you turn on Bambuddy's API key.

With Docker (HTTP mode):

```sh
docker build -t printshop-mcp .
docker run -p 8090:8090 -e PRINTSHOP_MCP_TOKEN=... printshop-mcp
claude mcp add --transport http printshop http://localhost:8090/mcp --header "Authorization: Bearer ..."
```

## Safety

This server can start, pause and stop prints, and the tools that cannot be taken back are gated as described above. Bambuddy and Spoolman can run with no authentication, so keep them off the public internet. HTTP mode refuses to start without a bearer token.

## Development

```sh
go test ./...
go build -o printshop-mcp ./cmd/printshop-mcp
go run ./scripts/smoke              # read-only calls against the real services
go run ./scripts/smoke -preflight   # also runs print_file with dry_run
go run ./scripts/spoolcheck -yes    # writes: makes spare records, tests the filament tools, deletes them
go run ./scripts/projectcheck -yes  # writes: makes a throwaway project, tests the project tools, deletes it
```

Tested against Bambuddy 1.2.5.1 and Spoolman 0.26.1. Bambuddy changes quickly, so check an endpoint with `scripts/smoke` before relying on it in a newer version. Test fixtures in `internal/plan/testdata` are recorded from those versions, with serial numbers, access codes, hostnames and spool RFID identifiers replaced.

Questions, updates, and works in progress: [FireBall Codes on Discord](https://discord.gg/QpV82CFfVD).

Licensed AGPL-3.0-only. Author: FireBall1725.
