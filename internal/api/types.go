// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package api

// ─── Bambuddy ────────────────────────────────────────────────────────────────

// PrinterStatus is the slice of GET /printers/{id}/status this server reads.
// The full payload is about 4 KB; everything not listed here is ignored.
type PrinterStatus struct {
	ID                 int         `json:"id"`
	Name               string      `json:"name"`
	Connected          bool        `json:"connected"`
	State              string      `json:"state"`
	CurrentPrint       string      `json:"current_print"`
	SubtaskName        string      `json:"subtask_name"`
	Progress           Num         `json:"progress"`
	RemainingTime      Num         `json:"remaining_time"`
	LayerNum           int         `json:"layer_num"`
	TotalLayers        int         `json:"total_layers"`
	Temperatures       Temps       `json:"temperatures"`
	HMSErrors          []HMSError  `json:"hms_errors"`
	AMS                []AMSUnit   `json:"ams"`
	VtTray             []Tray      `json:"vt_tray"`
	DoorOpen           bool        `json:"door_open"`
	Nozzles            []NozzleRef `json:"nozzles"`
	StageName          string      `json:"stg_cur_name"`
	AwaitingPlateClear bool        `json:"awaiting_plate_clear"`
	SupportsDrying     bool        `json:"supports_drying"`
	DryWhilePrinting   bool        `json:"supports_drying_while_printing"`
}

// Temps are the bed, nozzle and chamber readings and targets in °C.
type Temps struct {
	Bed           Num `json:"bed"`
	BedTarget     Num `json:"bed_target"`
	Nozzle        Num `json:"nozzle"`
	NozzleTarget  Num `json:"nozzle_target"`
	Chamber       Num `json:"chamber"`
	ChamberTarget Num `json:"chamber_target"`
}

// HMSError is one active Health Management System error.
type HMSError struct {
	Code     string `json:"code"`
	FullCode string `json:"full_code"`
	Module   int    `json:"module"`
	Severity int    `json:"severity"`
}

// NozzleRef is an installed nozzle.
type NozzleRef struct {
	Diameter Num `json:"nozzle_diameter"`
}

// AMSUnit is one AMS (or AMS HT) and its trays.
type AMSUnit struct {
	ID          int    `json:"id"`
	IsHT        bool   `json:"is_ams_ht"`
	Humidity    Num    `json:"humidity"`
	Temp        Num    `json:"temp"`
	DryTime     Num    `json:"dry_time"`
	DryStatus   Num    `json:"dry_status"`
	DryTarget   Num    `json:"dry_target_temp"`
	DryFilament string `json:"dry_filament"`
	Trays       []Tray `json:"tray"`
}

// Tray is one AMS slot. TrayColor is RRGGBBAA; Remain is a percentage, or
// negative when the AMS does not know.
type Tray struct {
	ID       int    `json:"id"`
	Color    string `json:"tray_color"`
	Type     string `json:"tray_type"`
	SubBrand string `json:"tray_sub_brands"`
	Remain   Num    `json:"remain"`
	TagUID   string `json:"tag_uid"`
	TrayUUID string `json:"tray_uuid"`
	Exists   bool   `json:"exists"`
}

// SlotAssignment links an AMS tray to a Spoolman spool. This is Bambuddy's
// own record of which spool sits in which slot.
type SlotAssignment struct {
	PrinterID int `json:"printer_id"`
	AMSID     int `json:"ams_id"`
	TrayID    int `json:"tray_id"`
	SpoolID   int `json:"spoolman_spool_id"`
}

// LibraryFile is a library row from the list or detail endpoint.
type LibraryFile struct {
	ThumbnailPath     *string   `json:"thumbnail_path"`
	ID                int       `json:"id"`
	Filename          string    `json:"filename"`
	FileType          string    `json:"file_type"`
	FileSize          int64     `json:"file_size"`
	PrintTimeSeconds  *int      `json:"print_time_seconds"`
	FilamentUsedGrams *float64  `json:"filament_used_grams"`
	SlicedForModel    *string   `json:"sliced_for_model"`
	Metadata          *FileMeta `json:"metadata"`
	PrintCount        int       `json:"print_count"`
}

// FileMeta is what Bambuddy read out of the 3MF.
type FileMeta struct {
	SlicedForModel    string         `json:"sliced_for_model"`
	PrintTimeSeconds  int            `json:"print_time_seconds"`
	FilamentUsedGrams float64        `json:"filament_used_grams"`
	FilamentSlots     []FilamentSlot `json:"filament_slots"`
	NozzleDiameter    Num            `json:"nozzle_diameter"`
	LayerHeight       Num            `json:"layer_height"`
	TotalLayers       int            `json:"total_layers"`
	BedType           string         `json:"bed_type"`
}

// FilamentSlot is one filament in a sliced file.
type FilamentSlot struct {
	SlotID int     `json:"slot_id"`
	UsedG  float64 `json:"used_g"`
	Type   string  `json:"type"`
	Color  string  `json:"color"`
}

// UploadResult is the POST /library/files/ response.
type UploadResult struct {
	ID            int       `json:"id"`
	Filename      string    `json:"filename"`
	FileType      string    `json:"file_type"`
	FileSize      int64     `json:"file_size"`
	ThumbnailPath *string   `json:"thumbnail_path"`
	DuplicateOf   *int      `json:"duplicate_of"`
	Metadata      *FileMeta `json:"metadata"`
}

// Requirements is GET /library/files/{id}/filament-requirements.
type Requirements struct {
	FileID    int           `json:"file_id"`
	Filename  string        `json:"filename"`
	PlateID   *int          `json:"plate_id"`
	Filaments []Requirement `json:"filaments"`
}

// Requirement is one filament a plate needs.
type Requirement struct {
	SlotID      int     `json:"slot_id"`
	Type        string  `json:"type"`
	Color       string  `json:"color"`
	UsedGrams   float64 `json:"used_grams"`
	UsedInPlate bool    `json:"used_in_plate"`
}

// QueueItem is a print queue row.
type QueueItem struct {
	ID                int      `json:"id"`
	PrinterID         *int     `json:"printer_id"`
	PrinterName       string   `json:"printer_name"`
	Status            string   `json:"status"`
	Position          int      `json:"position"`
	ArchiveName       string   `json:"archive_name"`
	LibraryFileName   string   `json:"library_file_name"`
	LibraryFileID     *int     `json:"library_file_id"`
	PlateID           *int     `json:"plate_id"`
	AMSMapping        []int    `json:"ams_mapping"`
	StartedAt         string   `json:"started_at"`
	CompletedAt       string   `json:"completed_at"`
	CreatedAt         string   `json:"created_at"`
	ErrorMessage      string   `json:"error_message"`
	WaitingReason     string   `json:"waiting_reason"`
	PrintTimeSeconds  *int     `json:"print_time_seconds"`
	FilamentUsedGrams *float64 `json:"filament_used_grams"`
	FilamentType      string   `json:"filament_type"`
	SlicedForModel    string   `json:"sliced_for_model"`
}

// QueueCreate is the POST /queue/ body.
type QueueCreate struct {
	PrinterID     int    `json:"printer_id"`
	LibraryFileID int    `json:"library_file_id"`
	PlateID       int    `json:"plate_id"`
	AMSMapping    []int  `json:"ams_mapping,omitempty"`
	UseAMS        bool   `json:"use_ams"`
	BedLevelling  string `json:"bed_levelling"`
	ProjectID     *int   `json:"project_id,omitempty"`
}

// PrinterInfo is a printer row. It lists only the safe fields on purpose:
// GET /printers/ also returns the printer's LAN access code, serial number
// and IP address, and none of those may reach a tool result.
type PrinterInfo struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Model       string `json:"model"`
	Location    string `json:"location"`
	IsActive    bool   `json:"is_active"`
	NozzleCount int    `json:"nozzle_count"`
}

// PrintObjects is GET /printers/{id}/print/objects.
type PrintObjects struct {
	Objects      []PrintObject `json:"objects"`
	Total        int           `json:"total"`
	SkippedCount int           `json:"skipped_count"`
	IsPrinting   bool          `json:"is_printing"`
}

// PrintObject is one object on the plate.
type PrintObject struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Skipped bool   `json:"skipped"`
}

// Archive is a finished-print record.
type Archive struct {
	ID                int      `json:"id"`
	PrinterID         *int     `json:"printer_id"`
	Filename          string   `json:"filename"`
	PrintName         string   `json:"print_name"`
	PlateID           *int     `json:"plate_id"`
	Status            string   `json:"status"`
	StartedAt         string   `json:"started_at"`
	CompletedAt       string   `json:"completed_at"`
	PrintTimeSeconds  *int     `json:"print_time_seconds"`
	ActualTimeSeconds *int     `json:"actual_time_seconds"`
	TimeAccuracy      *float64 `json:"time_accuracy"`
	FilamentUsedGrams *float64 `json:"filament_used_grams"`
	FilamentType      string   `json:"filament_type"`
	FilamentColor     string   `json:"filament_color"`
	LayerHeight       Num      `json:"layer_height"`
	TotalLayers       *int     `json:"total_layers"`
	SlicedForModel    string   `json:"sliced_for_model"`
	FailureReason     string   `json:"failure_reason"`
	Notes             string   `json:"notes"`
	Cost              *float64 `json:"cost"`
	ExtraData         *struct {
		FilamentSlots []FilamentSlot `json:"filament_slots"`
	} `json:"extra_data"`
}

// ArchiveStats is GET /archives/stats.
type ArchiveStats struct {
	TotalPrints     int            `json:"total_prints"`
	Successful      int            `json:"successful_prints"`
	Failed          int            `json:"failed_prints"`
	Cancelled       int            `json:"cancelled_prints"`
	TotalHours      float64        `json:"total_print_time_hours"`
	TotalGrams      float64        `json:"total_filament_grams"`
	TotalCost       float64        `json:"total_cost"`
	ByFilamentType  map[string]int `json:"prints_by_filament_type"`
	AverageAccuracy float64        `json:"average_time_accuracy"`
	TotalEnergyKWh  float64        `json:"total_energy_kwh"`
}

// MaintenancePrinter is one printer in GET /maintenance/overview.
type MaintenancePrinter struct {
	PrinterID int               `json:"printer_id"`
	Name      string            `json:"printer_name"`
	Hours     float64           `json:"total_print_hours"`
	Items     []MaintenanceItem `json:"maintenance_items"`
}

// MaintenanceItem is one scheduled maintenance task.
type MaintenanceItem struct {
	ID              int      `json:"id"`
	Name            string   `json:"maintenance_type_name"`
	Enabled         bool     `json:"enabled"`
	IntervalHours   float64  `json:"interval_hours"`
	IntervalType    string   `json:"interval_type"`
	HoursUntilDue   *float64 `json:"hours_until_due"`
	DaysUntilDue    *float64 `json:"days_until_due"`
	IsDue           bool     `json:"is_due"`
	IsWarning       bool     `json:"is_warning"`
	LastPerformedAt string   `json:"last_performed_at"`
}

// AMSHistory is GET /ams-history/{printer}/{ams}.
type AMSHistory struct {
	AvgHumidity float64      `json:"avg_humidity"`
	MinHumidity float64      `json:"min_humidity"`
	MaxHumidity float64      `json:"max_humidity"`
	AvgTemp     float64      `json:"avg_temperature"`
	MinTemp     float64      `json:"min_temperature"`
	MaxTemp     float64      `json:"max_temperature"`
	Data        []AMSReading `json:"data"`
}

// AMSReading is one humidity and temperature sample.
type AMSReading struct {
	RecordedAt  string  `json:"recorded_at"`
	Humidity    float64 `json:"humidity"`
	Temperature float64 `json:"temperature"`
}

// Project is a Bambuddy project. The list endpoints fill the count fields; the
// detail endpoint fills Stats.
type Project struct {
	ID               int           `json:"id"`
	Name             string        `json:"name"`
	Description      *string       `json:"description"`
	Color            *string       `json:"color"`
	Status           string        `json:"status"`
	Priority         string        `json:"priority"`
	TargetCount      *int          `json:"target_count"`
	TargetPartsCount *int          `json:"target_parts_count"`
	Notes            *string       `json:"notes"`
	Tags             *string       `json:"tags"`
	DueDate          *string       `json:"due_date"`
	Budget           *float64      `json:"budget"`
	URL              *string       `json:"url"`
	IsTemplate       bool          `json:"is_template"`
	ParentID         *int          `json:"parent_id"`
	ParentName       *string       `json:"parent_name"`
	Children         []ProjectRef  `json:"children"`
	Attachments      []Attachment  `json:"attachments"`
	CoverImage       *string       `json:"cover_image_filename"`
	CreatedAt        string        `json:"created_at"`
	UpdatedAt        string        `json:"updated_at"`
	Stats            *ProjectStats `json:"stats"`
	ArchiveCount     int           `json:"archive_count"`
	TotalItems       int           `json:"total_items"`
	CompletedCount   int           `json:"completed_count"`
	FailedCount      int           `json:"failed_count"`
	QueueCount       int           `json:"queue_count"`
	ProgressPercent  *float64      `json:"progress_percent"`
}

// ProjectRef is a child project.
type ProjectRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Attachment is a file attached to a project. Filename is the name Bambuddy
// stored it under, which is the one its download and delete routes take.
type Attachment struct {
	Filename     string `json:"filename"`
	OriginalName string `json:"original_name"`
	Size         int64  `json:"size"`
	UploadedAt   string `json:"uploaded_at"`
}

// ProjectStats is the detail endpoint's roll-up.
type ProjectStats struct {
	TotalArchives    int      `json:"total_archives"`
	TotalItems       int      `json:"total_items"`
	CompletedPrints  int      `json:"completed_prints"`
	FailedPrints     int      `json:"failed_prints"`
	QueuedPrints     int      `json:"queued_prints"`
	InProgressPrints int      `json:"in_progress_prints"`
	PrintTimeHours   float64  `json:"total_print_time_hours"`
	FilamentGrams    float64  `json:"total_filament_grams"`
	ProgressPercent  *float64 `json:"progress_percent"`
	PartsProgress    *float64 `json:"parts_progress_percent"`
	EstimatedCost    float64  `json:"estimated_cost"`
	EnergyKWh        float64  `json:"total_energy_kwh"`
	RemainingPrints  *int     `json:"remaining_prints"`
	RemainingParts   *int     `json:"remaining_parts"`
	BOMTotal         int      `json:"bom_total_items"`
	BOMCompleted     int      `json:"bom_completed_items"`
	BOMCost          float64  `json:"bom_cost"`
}

// BOMItem is a project bill-of-materials line.
type BOMItem struct {
	ID               int      `json:"id"`
	ProjectID        int      `json:"project_id"`
	Name             string   `json:"name"`
	QuantityNeeded   int      `json:"quantity_needed"`
	QuantityAcquired int      `json:"quantity_acquired"`
	UnitPrice        *float64 `json:"unit_price"`
	SourcingURL      *string  `json:"sourcing_url"`
	ArchiveID        *int     `json:"archive_id"`
	ArchiveName      *string  `json:"archive_name"`
	STLFilename      *string  `json:"stl_filename"`
	Remarks          *string  `json:"remarks"`
	IsComplete       bool     `json:"is_complete"`
}

// TimelineEvent is one entry in a project's history.
type TimelineEvent struct {
	EventType   string         `json:"event_type"`
	Timestamp   string         `json:"timestamp"`
	Title       string         `json:"title"`
	Description *string        `json:"description"`
	Metadata    map[string]any `json:"metadata"`
}

// ─── Spoolman ────────────────────────────────────────────────────────────────

// Vendor is a Spoolman vendor.
type Vendor struct {
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Comment string `json:"comment"`
}

// Filament is a Spoolman filament type.
type Filament struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	Vendor       *Vendor  `json:"vendor"`
	Material     string   `json:"material"`
	Density      float64  `json:"density"`
	Diameter     float64  `json:"diameter"`
	Weight       *float64 `json:"weight"`
	SpoolWeight  *float64 `json:"spool_weight"`
	ColorHex     string   `json:"color_hex"`
	ExtruderTemp *int     `json:"settings_extruder_temp"`
	BedTemp      *int     `json:"settings_bed_temp"`
	Comment      string   `json:"comment"`
}

// Spool is a Spoolman spool. Extra["tag"] is a JSON-quoted string holding the
// AMS tray_uuid for spools Bambuddy created.
type Spool struct {
	ID              int               `json:"id"`
	FirstUsed       string            `json:"first_used"`
	LastUsed        string            `json:"last_used"`
	Filament        Filament          `json:"filament"`
	RemainingWeight *float64          `json:"remaining_weight"`
	InitialWeight   *float64          `json:"initial_weight"`
	UsedWeight      float64           `json:"used_weight"`
	SpoolWeight     *float64          `json:"spool_weight"`
	Price           *float64          `json:"price"`
	Location        string            `json:"location"`
	LotNr           string            `json:"lot_nr"`
	Comment         string            `json:"comment"`
	Archived        bool              `json:"archived"`
	Extra           map[string]string `json:"extra"`
}
