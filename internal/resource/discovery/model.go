package discovery

// OriginType distinguishes Xiaoya SQLite auto-discovered nodes from AList manual imports.
const (
	OriginXiaoyaSQLite = "XIAOYA_SQLITE"
	OriginAListManual  = "ALIST_MANUAL"
)

// ResourceStatus tracks lifecycle states under the V1.0 `keep` deletion policy.
const (
	StatusActive  = "ACTIVE"
	StatusMissing = "MISSING"
	StatusFailed  = "FAILED"
	StatusIgnored = "IGNORED"
)

// ManageMode tracks how a resource entered management.
const (
	ManageAutoMonitor     = "AUTO_MONITOR"
	ManageManualFile      = "MANUAL_FILE"
	ManageManualSeriesDir = "MANUAL_SERIES_DIR"
)

// ScanMode describes the type of batch execution.
const (
	ScanModeBaseline     = "BASELINE"
	ScanModeFull         = "FULL"
	ScanModeIncremental  = "INCREMENTAL"
	ScanModeManualImport = "MANUAL_IMPORT"
)

// BatchStatus describes batch execution outcome.
const (
	BatchRunning = "RUNNING"
	BatchSuccess = "SUCCESS"
	BatchFailed  = "FAILED"
)

// ChangeType enumerates auditable resource transitions.
const (
	ChangeBaselineAdded  = "BASELINE_ADDED"
	ChangeAdded          = "ADDED"
	ChangeModified       = "MODIFIED"
	ChangeRenamed        = "RENAMED"
	ChangeMoved          = "MOVED"
	ChangeMissing        = "MISSING"
	ChangeRestored       = "RESTORED"
	ChangeManualImported = "MANUAL_IMPORTED"
)

type MonitorRoot struct {
	ID            string `json:"id"`
	RootPath      string `json:"root_path"`
	SourceNodeID  int64  `json:"source_node_id"`
	Enabled       bool   `json:"enabled"`
	IntervalSec   int64  `json:"interval_sec"`
	LastBatchID   string `json:"last_batch_id"`
	LastStatus    string `json:"last_status"`
	LastError     string `json:"last_error"`
	LastScannedAt int64  `json:"last_scanned_at"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

type SyncBatch struct {
	ID              string `json:"id"`
	RootID          string `json:"root_id"`
	RootPath        string `json:"root_path"`
	ScanMode        string `json:"scan_mode"`
	IsBaseline      bool   `json:"is_baseline"`
	Status          string `json:"status"`
	StartedAt       int64  `json:"started_at"`
	FinishedAt      int64  `json:"finished_at"`
	TotalSeen       int64  `json:"total_seen"`
	AddedCount      int64  `json:"added_count"`
	ModifiedCount   int64  `json:"modified_count"`
	RenamedCount    int64  `json:"renamed_count"`
	MovedCount      int64  `json:"moved_count"`
	MissingCount    int64  `json:"missing_count"`
	RestoredCount   int64  `json:"restored_count"`
	FailedCount     int64  `json:"failed_count"`
	MaxSourceNodeID int64  `json:"max_source_node_id"`
	ErrorMessage    string `json:"error_message"`
}

type SyncCursor struct {
	RootID             string `json:"root_id"`
	RootNodeID         int64  `json:"root_node_id"`
	LastMaxNodeID      int64  `json:"last_max_node_id"`
	LastSuccessBatchID string `json:"last_success_batch_id"`
	LastCompleteScanAt int64  `json:"last_complete_scan_at"`
	UpdatedAt          int64  `json:"updated_at"`
}

type MediaResource struct {
	ID               string `json:"id"`
	SourceKey        string `json:"source_key"`
	OriginType       string `json:"origin_type"`
	RootID           string `json:"root_id"`
	SourceNodeID     int64  `json:"source_node_id"`
	SourceParentID   int64  `json:"source_parent_id"`
	SourcePath       string `json:"source_path"`
	RelPath          string `json:"rel_path"`
	Name             string `json:"name"`
	IsDir            bool   `json:"is_dir"`
	SourceContent    string `json:"source_content"`
	NameHash         string `json:"name_hash"`
	PathHash         string `json:"path_hash"`
	ContentHash      string `json:"content_hash"`
	SnapshotHash     string `json:"snapshot_hash"`
	Status           string `json:"status"`
	LocalSTRMPath    string `json:"local_strm_path"`
	MediaItemID      string `json:"media_item_id"`
	LastError        string `json:"last_error"`
	RetryCount       int64  `json:"retry_count"`
	FirstSeenBatchID string `json:"first_seen_batch_id"`
	LastSeenBatchID  string `json:"last_seen_batch_id"`
	FirstSeenAt      int64  `json:"first_seen_at"`
	LastSeenAt       int64  `json:"last_seen_at"`
	LastChangedAt    int64  `json:"last_changed_at"`
}

type ManagedResource struct {
	ID             string `json:"id"`
	ResourceID     string `json:"resource_id"`
	ManageMode     string `json:"manage_mode"`
	RootID         string `json:"root_id"`
	LibraryID      string `json:"library_id"`
	OutputDir      string `json:"output_dir"`
	Enabled        bool   `json:"enabled"`
	DeletionPolicy string `json:"deletion_policy"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

type ResourceChange struct {
	ID              string `json:"id"`
	BatchID         string `json:"batch_id"`
	RootID          string `json:"root_id"`
	ResourceID      string `json:"resource_id"`
	SourceNodeID    int64  `json:"source_node_id"`
	ChangeType      string `json:"change_type"`
	OldPath         string `json:"old_path"`
	NewPath         string `json:"new_path"`
	OldContentHash  string `json:"old_content_hash"`
	NewContentHash  string `json:"new_content_hash"`
	OldSnapshotHash string `json:"old_snapshot_hash"`
	NewSnapshotHash string `json:"new_snapshot_hash"`
	DetectedAt      int64  `json:"detected_at"`
}

type MediaAttachment struct {
	ID             string `json:"id"`
	ResourceID     string `json:"resource_id"`
	AttachmentType string `json:"attachment_type"`
	Ext            string `json:"ext"`
	SourceURL      string `json:"source_url"`
	LocalPath      string `json:"local_path"`
	ContentHash    string `json:"content_hash"`
	SizeBytes      int64  `json:"size_bytes"`
	Status         string `json:"status"`
	LastError      string `json:"last_error"`
	SyncedAt       int64  `json:"synced_at"`
}

type ScheduledTaskState struct {
	TaskKey        string `json:"task_key"`
	Name           string `json:"name"`
	IntervalSec    int64  `json:"interval_sec"`
	Enabled        bool   `json:"enabled"`
	Status         string `json:"status"`
	LastRunAt      int64  `json:"last_run_at"`
	NextRunAt      int64  `json:"next_run_at"`
	LastDurationMs int64  `json:"last_duration_ms"`
	LastError      string `json:"last_error"`
}

// CleanupImpactPreview describes affected artifacts before manual cleanup confirmation (`keep` policy).
type CleanupImpactPreview struct {
	ResourceID        string   `json:"resource_id"`
	SourcePath        string   `json:"source_path"`
	Status            string   `json:"status"`
	LocalSTRMPath     string   `json:"local_strm_path"`
	AttachmentPaths   []string `json:"attachment_paths"`
	AffectedResources int      `json:"affected_resources"`
	RequiresConfirm   bool     `json:"requires_confirm"`
}
