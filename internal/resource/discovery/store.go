package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var ErrConfirmationRequired = errors.New("manual cleanup confirmation required under keep policy")

type SQLStore struct {
	db      *sql.DB
	dialect string // "postgres" or "sqlite"
}

func NewSQLStore(db *sql.DB, dialect string) *SQLStore {
	d := strings.ToLower(strings.TrimSpace(dialect))
	if d == "" {
		d = "postgres"
	}
	return &SQLStore{db: db, dialect: d}
}

func (s *SQLStore) DB() *sql.DB {
	return s.db
}

func (s *SQLStore) rebind(query string) string {
	if s.dialect != "postgres" {
		return query
	}
	var out strings.Builder
	out.Grow(len(query) + 32)
	n := 1
	inQuote := false
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if ch == '\'' {
			inQuote = !inQuote
			out.WriteByte(ch)
			continue
		}
		if ch == '?' && !inQuote {
			out.WriteByte('$')
			out.WriteString(strconv.Itoa(n))
			n++
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

func boolToInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

const resourceEngineDDL = `
CREATE TABLE IF NOT EXISTS monitor_roots (
    id TEXT PRIMARY KEY,
    root_path TEXT UNIQUE NOT NULL,
    source_node_id BIGINT NOT NULL DEFAULT 0,
    enabled BIGINT NOT NULL DEFAULT 1,
    interval_sec BIGINT NOT NULL DEFAULT 600,
    last_batch_id TEXT NOT NULL DEFAULT '',
    last_status TEXT NOT NULL DEFAULT 'IDLE',
    last_error TEXT NOT NULL DEFAULT '',
    last_scanned_at BIGINT NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_batches (
    id TEXT PRIMARY KEY,
    root_id TEXT NOT NULL DEFAULT '',
    root_path TEXT NOT NULL DEFAULT '',
    scan_mode TEXT NOT NULL,
    is_baseline BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    started_at BIGINT NOT NULL,
    finished_at BIGINT NOT NULL DEFAULT 0,
    total_seen BIGINT NOT NULL DEFAULT 0,
    added_count BIGINT NOT NULL DEFAULT 0,
    modified_count BIGINT NOT NULL DEFAULT 0,
    renamed_count BIGINT NOT NULL DEFAULT 0,
    moved_count BIGINT NOT NULL DEFAULT 0,
    missing_count BIGINT NOT NULL DEFAULT 0,
    restored_count BIGINT NOT NULL DEFAULT 0,
    failed_count BIGINT NOT NULL DEFAULT 0,
    max_source_node_id BIGINT NOT NULL DEFAULT 0,
    error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sync_batches_root_started ON sync_batches(root_id, started_at DESC);

CREATE TABLE IF NOT EXISTS sync_cursors (
    root_id TEXT PRIMARY KEY,
    root_node_id BIGINT NOT NULL DEFAULT 0,
    last_max_node_id BIGINT NOT NULL DEFAULT 0,
    last_success_batch_id TEXT NOT NULL DEFAULT '',
    last_complete_scan_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS media_resources (
    id TEXT PRIMARY KEY,
    source_key TEXT UNIQUE NOT NULL,
    origin_type TEXT NOT NULL,
    root_id TEXT NOT NULL DEFAULT '',
    source_node_id BIGINT NOT NULL DEFAULT 0,
    source_parent_id BIGINT NOT NULL DEFAULT 0,
    source_path TEXT NOT NULL,
    rel_path TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    is_dir BIGINT NOT NULL DEFAULT 0,
    source_content TEXT NOT NULL DEFAULT '',
    name_hash TEXT NOT NULL,
    path_hash TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    snapshot_hash TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    local_strm_path TEXT NOT NULL DEFAULT '',
    media_item_id TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    retry_count BIGINT NOT NULL DEFAULT 0,
    first_seen_batch_id TEXT NOT NULL DEFAULT '',
    last_seen_batch_id TEXT NOT NULL DEFAULT '',
    first_seen_at BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    last_changed_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_resources_root_status ON media_resources(root_id, status);
CREATE INDEX IF NOT EXISTS idx_media_resources_source_path ON media_resources(source_path);
CREATE INDEX IF NOT EXISTS idx_media_resources_local_strm ON media_resources(local_strm_path);

CREATE TABLE IF NOT EXISTS managed_resources (
    id TEXT PRIMARY KEY,
    resource_id TEXT UNIQUE NOT NULL,
    manage_mode TEXT NOT NULL,
    root_id TEXT NOT NULL DEFAULT '',
    library_id TEXT NOT NULL DEFAULT '',
    output_dir TEXT NOT NULL DEFAULT '',
    enabled BIGINT NOT NULL DEFAULT 1,
    deletion_policy TEXT NOT NULL DEFAULT 'keep',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_managed_resources_mode ON managed_resources(manage_mode, enabled);

CREATE TABLE IF NOT EXISTS resource_change_history (
    id TEXT PRIMARY KEY,
    batch_id TEXT NOT NULL,
    root_id TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL,
    source_node_id BIGINT NOT NULL DEFAULT 0,
    change_type TEXT NOT NULL,
    old_path TEXT NOT NULL DEFAULT '',
    new_path TEXT NOT NULL DEFAULT '',
    old_content_hash TEXT NOT NULL DEFAULT '',
    new_content_hash TEXT NOT NULL DEFAULT '',
    old_snapshot_hash TEXT NOT NULL DEFAULT '',
    new_snapshot_hash TEXT NOT NULL DEFAULT '',
    detected_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_resource_changes_batch ON resource_change_history(batch_id);
CREATE INDEX IF NOT EXISTS idx_resource_changes_detected ON resource_change_history(detected_at DESC);

CREATE TABLE IF NOT EXISTS media_attachments (
    id TEXT PRIMARY KEY,
    resource_id TEXT NOT NULL,
    attachment_type TEXT NOT NULL,
    ext TEXT NOT NULL,
    source_url TEXT NOT NULL DEFAULT '',
    local_path TEXT UNIQUE NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'SYNCED',
    last_error TEXT NOT NULL DEFAULT '',
    synced_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_media_attachments_resource ON media_attachments(resource_id);

CREATE TABLE IF NOT EXISTS scheduled_tasks (
    task_key TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    interval_sec BIGINT NOT NULL,
    enabled BIGINT NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'IDLE',
    last_run_at BIGINT NOT NULL DEFAULT 0,
    next_run_at BIGINT NOT NULL DEFAULT 0,
    last_duration_ms BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT ''
);
`

func (s *SQLStore) EnsureSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, resourceEngineDDL); err != nil {
		return fmt.Errorf("ensure resource engine schema: %w", err)
	}
	// If `items` exists in the same DB (unified PostgreSQL), ensure `availability` column exists.
	if s.dialect == "postgres" {
		_, _ = s.db.ExecContext(ctx, "ALTER TABLE items ADD COLUMN IF NOT EXISTS availability TEXT NOT NULL DEFAULT 'AVAILABLE'")
	}
	return nil
}

func (s *SQLStore) UpsertMonitorRoot(ctx context.Context, rootPath string, enabled bool, intervalSec int64) (MonitorRoot, error) {
	now := time.Now().Unix()
	if intervalSec <= 0 {
		intervalSec = 600
	}
	id := StableID("root:" + rootPath)
	q := s.rebind(`
INSERT INTO monitor_roots(id, root_path, enabled, interval_sec, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(root_path) DO UPDATE SET
	enabled = excluded.enabled,
	interval_sec = excluded.interval_sec,
	updated_at = excluded.updated_at`)
	if _, err := s.db.ExecContext(ctx, q, id, rootPath, boolToInt(enabled), intervalSec, now, now); err != nil {
		return MonitorRoot{}, err
	}
	return s.GetMonitorRootByPath(ctx, rootPath)
}

func scanMonitorRoot(scanner interface{ Scan(...any) error }) (MonitorRoot, error) {
	var r MonitorRoot
	var en int64
	err := scanner.Scan(
		&r.ID, &r.RootPath, &r.SourceNodeID, &en, &r.IntervalSec,
		&r.LastBatchID, &r.LastStatus, &r.LastError, &r.LastScannedAt,
		&r.CreatedAt, &r.UpdatedAt,
	)
	r.Enabled = en != 0
	return r, err
}

const monitorRootCols = "id, root_path, source_node_id, enabled, interval_sec, last_batch_id, last_status, last_error, last_scanned_at, created_at, updated_at"

func (s *SQLStore) GetMonitorRoot(ctx context.Context, id string) (MonitorRoot, error) {
	row := s.db.QueryRowContext(ctx, s.rebind("SELECT "+monitorRootCols+" FROM monitor_roots WHERE id = ?"), id)
	return scanMonitorRoot(row)
}

func (s *SQLStore) GetMonitorRootByPath(ctx context.Context, rootPath string) (MonitorRoot, error) {
	row := s.db.QueryRowContext(ctx, s.rebind("SELECT "+monitorRootCols+" FROM monitor_roots WHERE root_path = ?"), rootPath)
	return scanMonitorRoot(row)
}

func (s *SQLStore) ListMonitorRoots(ctx context.Context) ([]MonitorRoot, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+monitorRootCols+" FROM monitor_roots ORDER BY created_at ASC, id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MonitorRoot
	for rows.Next() {
		r, sErr := scanMonitorRoot(rows)
		if sErr != nil {
			return nil, sErr
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLStore) UpdateMonitorRootStatus(ctx context.Context, id string, sourceNodeID int64, batchID, status, errMsg string, scannedAt int64) error {
	now := time.Now().Unix()
	q := s.rebind(`
UPDATE monitor_roots
SET source_node_id = CASE WHEN ? > 0 THEN ? ELSE source_node_id END,
    last_batch_id = CASE WHEN ? <> '' THEN ? ELSE last_batch_id END,
    last_status = ?,
    last_error = ?,
    last_scanned_at = CASE WHEN ? > 0 THEN ? ELSE last_scanned_at END,
    updated_at = ?
WHERE id = ?`)
	_, err := s.db.ExecContext(ctx, q, sourceNodeID, sourceNodeID, batchID, batchID, status, errMsg, scannedAt, scannedAt, now, id)
	return err
}

func (s *SQLStore) DeleteMonitorRoot(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, s.rebind("DELETE FROM monitor_roots WHERE id = ?"), id)
	return err
}

func (s *SQLStore) CreateBatch(ctx context.Context, rootID, rootPath, scanMode string, isBaseline bool) (SyncBatch, error) {
	b := SyncBatch{
		ID:         NewID(),
		RootID:     rootID,
		RootPath:   rootPath,
		ScanMode:   scanMode,
		IsBaseline: isBaseline,
		Status:     BatchRunning,
		StartedAt:  time.Now().Unix(),
	}
	q := s.rebind(`
INSERT INTO sync_batches(id, root_id, root_path, scan_mode, is_baseline, status, started_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`)
	if _, err := s.db.ExecContext(ctx, q, b.ID, b.RootID, b.RootPath, b.ScanMode, boolToInt(b.IsBaseline), b.Status, b.StartedAt); err != nil {
		return SyncBatch{}, err
	}
	return b, nil
}

func (s *SQLStore) FinishBatch(ctx context.Context, b SyncBatch) error {
	if b.FinishedAt == 0 {
		b.FinishedAt = time.Now().Unix()
	}
	q := s.rebind(`
UPDATE sync_batches
SET status = ?, finished_at = ?, total_seen = ?, added_count = ?, modified_count = ?,
    renamed_count = ?, moved_count = ?, missing_count = ?, restored_count = ?, failed_count = ?,
    max_source_node_id = ?, error_message = ?
WHERE id = ?`)
	_, err := s.db.ExecContext(ctx, q,
		b.Status, b.FinishedAt, b.TotalSeen, b.AddedCount, b.ModifiedCount,
		b.RenamedCount, b.MovedCount, b.MissingCount, b.RestoredCount, b.FailedCount,
		b.MaxSourceNodeID, b.ErrorMessage, b.ID,
	)
	return err
}

func (s *SQLStore) ListBatches(ctx context.Context, rootID string, limit int) ([]SyncBatch, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := s.rebind(`
SELECT id, root_id, root_path, scan_mode, is_baseline, status, started_at, finished_at,
       total_seen, added_count, modified_count, renamed_count, moved_count, missing_count,
       restored_count, failed_count, max_source_node_id, error_message
FROM sync_batches
WHERE (? = '' OR root_id = ?)
ORDER BY started_at DESC, id DESC
LIMIT ?`)
	rows, err := s.db.QueryContext(ctx, q, rootID, rootID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncBatch
	for rows.Next() {
		var b SyncBatch
		var bl int64
		if sErr := rows.Scan(
			&b.ID, &b.RootID, &b.RootPath, &b.ScanMode, &bl, &b.Status, &b.StartedAt, &b.FinishedAt,
			&b.TotalSeen, &b.AddedCount, &b.ModifiedCount, &b.RenamedCount, &b.MovedCount, &b.MissingCount,
			&b.RestoredCount, &b.FailedCount, &b.MaxSourceNodeID, &b.ErrorMessage,
		); sErr != nil {
			return nil, sErr
		}
		b.IsBaseline = bl != 0
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *SQLStore) GetCursor(ctx context.Context, rootID string) (SyncCursor, bool, error) {
	var c SyncCursor
	q := s.rebind(`SELECT root_id, root_node_id, last_max_node_id, last_success_batch_id, last_complete_scan_at, updated_at FROM sync_cursors WHERE root_id = ?`)
	err := s.db.QueryRowContext(ctx, q, rootID).Scan(
		&c.RootID, &c.RootNodeID, &c.LastMaxNodeID, &c.LastSuccessBatchID, &c.LastCompleteScanAt, &c.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return SyncCursor{}, false, nil
	}
	if err != nil {
		return SyncCursor{}, false, err
	}
	return c, true, nil
}

func (s *SQLStore) SaveCursor(ctx context.Context, c SyncCursor) error {
	if c.UpdatedAt == 0 {
		c.UpdatedAt = time.Now().Unix()
	}
	q := s.rebind(`
INSERT INTO sync_cursors(root_id, root_node_id, last_max_node_id, last_success_batch_id, last_complete_scan_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(root_id) DO UPDATE SET
	root_node_id = excluded.root_node_id,
	last_max_node_id = CASE WHEN excluded.last_max_node_id > sync_cursors.last_max_node_id THEN excluded.last_max_node_id ELSE sync_cursors.last_max_node_id END,
	last_success_batch_id = excluded.last_success_batch_id,
	last_complete_scan_at = CASE WHEN excluded.last_complete_scan_at > 0 THEN excluded.last_complete_scan_at ELSE sync_cursors.last_complete_scan_at END,
	updated_at = excluded.updated_at`)
	_, err := s.db.ExecContext(ctx, q, c.RootID, c.RootNodeID, c.LastMaxNodeID, c.LastSuccessBatchID, c.LastCompleteScanAt, c.UpdatedAt)
	return err
}

const mediaResourceCols = `id, source_key, origin_type, root_id, source_node_id, source_parent_id,
source_path, rel_path, name, is_dir, source_content, name_hash, path_hash, content_hash, snapshot_hash,
status, local_strm_path, media_item_id, last_error, retry_count, first_seen_batch_id, last_seen_batch_id,
first_seen_at, last_seen_at, last_changed_at`

func scanMediaResource(scanner interface{ Scan(...any) error }) (MediaResource, error) {
	var m MediaResource
	var isDirInt int64
	err := scanner.Scan(
		&m.ID, &m.SourceKey, &m.OriginType, &m.RootID, &m.SourceNodeID, &m.SourceParentID,
		&m.SourcePath, &m.RelPath, &m.Name, &isDirInt, &m.SourceContent,
		&m.NameHash, &m.PathHash, &m.ContentHash, &m.SnapshotHash,
		&m.Status, &m.LocalSTRMPath, &m.MediaItemID, &m.LastError, &m.RetryCount,
		&m.FirstSeenBatchID, &m.LastSeenBatchID, &m.FirstSeenAt, &m.LastSeenAt, &m.LastChangedAt,
	)
	m.IsDir = isDirInt != 0
	return m, err
}

func (s *SQLStore) LoadRootResourcesBySourceKey(ctx context.Context, rootID string) (map[string]MediaResource, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE root_id = ?"), rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]MediaResource)
	for rows.Next() {
		m, sErr := scanMediaResource(rows)
		if sErr != nil {
			return nil, sErr
		}
		out[m.SourceKey] = m
	}
	return out, rows.Err()
}

func (s *SQLStore) GetResource(ctx context.Context, id string) (MediaResource, error) {
	row := s.db.QueryRowContext(ctx, s.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE id = ?"), id)
	return scanMediaResource(row)
}

func (s *SQLStore) GetResourceBySourceKey(ctx context.Context, sourceKey string) (MediaResource, bool, error) {
	row := s.db.QueryRowContext(ctx, s.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE source_key = ?"), sourceKey)
	m, err := scanMediaResource(row)
	if err == sql.ErrNoRows {
		return MediaResource{}, false, nil
	}
	if err != nil {
		return MediaResource{}, false, err
	}
	return m, true, nil
}

type ResourceFilter struct {
	RootID      string
	Status      string
	OriginType  string
	SearchQuery string
	OnlyFiles   bool
	SinceUnix   int64
	Limit       int
	Offset      int
}

func (s *SQLStore) ListResources(ctx context.Context, f ResourceFilter) ([]MediaResource, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	conds := []string{"1=1"}
	args := []any{}
	if f.RootID != "" {
		conds = append(conds, "root_id = ?")
		args = append(args, f.RootID)
	}
	if f.Status != "" {
		conds = append(conds, "status = ?")
		args = append(args, f.Status)
	}
	if f.OriginType != "" {
		conds = append(conds, "origin_type = ?")
		args = append(args, f.OriginType)
	}
	if f.OnlyFiles {
		conds = append(conds, "is_dir = 0")
	}
	if f.SinceUnix > 0 {
		conds = append(conds, "first_seen_at >= ?")
		args = append(args, f.SinceUnix)
	}
	if strings.TrimSpace(f.SearchQuery) != "" {
		conds = append(conds, "LOWER(source_path) LIKE ?")
		args = append(args, "%"+strings.ToLower(strings.TrimSpace(f.SearchQuery))+"%")
	}
	where := strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRowContext(ctx, s.rebind("SELECT COUNT(*) FROM media_resources WHERE "+where), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	qArgs := append(append([]any{}, args...), f.Limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, s.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE "+where+" ORDER BY last_changed_at DESC, id ASC LIMIT ? OFFSET ?"), qArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []MediaResource
	for rows.Next() {
		m, sErr := scanMediaResource(rows)
		if sErr != nil {
			return nil, 0, sErr
		}
		out = append(out, m)
	}
	return out, total, rows.Err()
}

func (s *SQLStore) UpsertResource(ctx context.Context, execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, m MediaResource) error {
	q := s.rebind(`
INSERT INTO media_resources(
	id, source_key, origin_type, root_id, source_node_id, source_parent_id,
	source_path, rel_path, name, is_dir, source_content,
	name_hash, path_hash, content_hash, snapshot_hash,
	status, local_strm_path, media_item_id, last_error, retry_count,
	first_seen_batch_id, last_seen_batch_id, first_seen_at, last_seen_at, last_changed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source_key) DO UPDATE SET
	root_id = excluded.root_id,
	source_parent_id = excluded.source_parent_id,
	source_path = excluded.source_path,
	rel_path = excluded.rel_path,
	name = excluded.name,
	is_dir = excluded.is_dir,
	source_content = excluded.source_content,
	name_hash = excluded.name_hash,
	path_hash = excluded.path_hash,
	content_hash = excluded.content_hash,
	snapshot_hash = excluded.snapshot_hash,
	status = excluded.status,
	local_strm_path = CASE WHEN excluded.local_strm_path <> '' THEN excluded.local_strm_path ELSE media_resources.local_strm_path END,
	media_item_id = CASE WHEN excluded.media_item_id <> '' THEN excluded.media_item_id ELSE media_resources.media_item_id END,
	last_error = excluded.last_error,
	retry_count = excluded.retry_count,
	last_seen_batch_id = excluded.last_seen_batch_id,
	last_seen_at = excluded.last_seen_at,
	last_changed_at = excluded.last_changed_at`)
	_, err := execer.ExecContext(ctx, q,
		m.ID, m.SourceKey, m.OriginType, m.RootID, m.SourceNodeID, m.SourceParentID,
		m.SourcePath, m.RelPath, m.Name, boolToInt(m.IsDir), m.SourceContent,
		m.NameHash, m.PathHash, m.ContentHash, m.SnapshotHash,
		m.Status, m.LocalSTRMPath, m.MediaItemID, m.LastError, m.RetryCount,
		m.FirstSeenBatchID, m.LastSeenBatchID, m.FirstSeenAt, m.LastSeenAt, m.LastChangedAt,
	)
	return err
}

func (s *SQLStore) UpdateResourceSTRMResult(ctx context.Context, resourceID, localSTRMPath, status, errMsg string) error {
	now := time.Now().Unix()
	q := s.rebind(`
UPDATE media_resources
SET local_strm_path = CASE WHEN ? <> '' THEN ? ELSE local_strm_path END,
    status = ?,
    last_error = ?,
    retry_count = CASE WHEN ? = 'FAILED' THEN retry_count + 1 ELSE 0 END,
    last_changed_at = ?
WHERE id = ?`)
	_, err := s.db.ExecContext(ctx, q, localSTRMPath, localSTRMPath, status, errMsg, status, now, resourceID)
	return err
}

func (s *SQLStore) UpsertManagedResource(ctx context.Context, execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, mr ManagedResource) error {
	now := time.Now().Unix()
	if mr.ID == "" {
		mr.ID = StableID("managed:" + mr.ResourceID)
	}
	if mr.DeletionPolicy == "" {
		mr.DeletionPolicy = "keep"
	}
	if mr.CreatedAt == 0 {
		mr.CreatedAt = now
	}
	mr.UpdatedAt = now
	q := s.rebind(`
INSERT INTO managed_resources(id, resource_id, manage_mode, root_id, library_id, output_dir, enabled, deletion_policy, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(resource_id) DO UPDATE SET
	manage_mode = excluded.manage_mode,
	root_id = excluded.root_id,
	library_id = CASE WHEN excluded.library_id <> '' THEN excluded.library_id ELSE managed_resources.library_id END,
	output_dir = CASE WHEN excluded.output_dir <> '' THEN excluded.output_dir ELSE managed_resources.output_dir END,
	enabled = excluded.enabled,
	updated_at = excluded.updated_at`)
	_, err := execer.ExecContext(ctx, q,
		mr.ID, mr.ResourceID, mr.ManageMode, mr.RootID, mr.LibraryID, mr.OutputDir,
		boolToInt(mr.Enabled), mr.DeletionPolicy, mr.CreatedAt, mr.UpdatedAt,
	)
	return err
}

func (s *SQLStore) InsertChange(ctx context.Context, execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, c ResourceChange) error {
	if c.ID == "" {
		c.ID = NewID()
	}
	if c.DetectedAt == 0 {
		c.DetectedAt = time.Now().Unix()
	}
	q := s.rebind(`
INSERT INTO resource_change_history(
	id, batch_id, root_id, resource_id, source_node_id, change_type,
	old_path, new_path, old_content_hash, new_content_hash, old_snapshot_hash, new_snapshot_hash, detected_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err := execer.ExecContext(ctx, q,
		c.ID, c.BatchID, c.RootID, c.ResourceID, c.SourceNodeID, c.ChangeType,
		c.OldPath, c.NewPath, c.OldContentHash, c.NewContentHash, c.OldSnapshotHash, c.NewSnapshotHash, c.DetectedAt,
	)
	return err
}

func (s *SQLStore) ListChanges(ctx context.Context, batchID, changeType string, sinceUnix int64, limit int) ([]ResourceChange, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	conds := []string{"1=1"}
	args := []any{}
	if batchID != "" {
		conds = append(conds, "batch_id = ?")
		args = append(args, batchID)
	}
	if changeType != "" {
		conds = append(conds, "change_type = ?")
		args = append(args, changeType)
	}
	if sinceUnix > 0 {
		conds = append(conds, "detected_at >= ?")
		args = append(args, sinceUnix)
	}
	args = append(args, limit)
	q := s.rebind(`
SELECT id, batch_id, root_id, resource_id, source_node_id, change_type,
       old_path, new_path, old_content_hash, new_content_hash, old_snapshot_hash, new_snapshot_hash, detected_at
FROM resource_change_history
WHERE ` + strings.Join(conds, " AND ") + `
ORDER BY detected_at DESC, id ASC
LIMIT ?`)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResourceChange
	for rows.Next() {
		var c ResourceChange
		if sErr := rows.Scan(
			&c.ID, &c.BatchID, &c.RootID, &c.ResourceID, &c.SourceNodeID, &c.ChangeType,
			&c.OldPath, &c.NewPath, &c.OldContentHash, &c.NewContentHash, &c.OldSnapshotHash, &c.NewSnapshotHash, &c.DetectedAt,
		); sErr != nil {
			return nil, sErr
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLStore) UpsertAttachment(ctx context.Context, a MediaAttachment) error {
	if a.ID == "" {
		a.ID = StableID("att:" + a.LocalPath)
	}
	if a.SyncedAt == 0 {
		a.SyncedAt = time.Now().Unix()
	}
	q := s.rebind(`
INSERT INTO media_attachments(id, resource_id, attachment_type, ext, source_url, local_path, content_hash, size_bytes, status, last_error, synced_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(local_path) DO UPDATE SET
	resource_id = excluded.resource_id,
	source_url = excluded.source_url,
	content_hash = excluded.content_hash,
	size_bytes = excluded.size_bytes,
	status = excluded.status,
	last_error = excluded.last_error,
	synced_at = excluded.synced_at`)
	_, err := s.db.ExecContext(ctx, q,
		a.ID, a.ResourceID, a.AttachmentType, a.Ext, a.SourceURL, a.LocalPath,
		a.ContentHash, a.SizeBytes, a.Status, a.LastError, a.SyncedAt,
	)
	return err
}

func (s *SQLStore) ListAttachments(ctx context.Context, resourceID string) ([]MediaAttachment, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`
SELECT id, resource_id, attachment_type, ext, source_url, local_path, content_hash, size_bytes, status, last_error, synced_at
FROM media_attachments WHERE resource_id = ? ORDER BY local_path ASC`), resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaAttachment
	for rows.Next() {
		var a MediaAttachment
		if sErr := rows.Scan(&a.ID, &a.ResourceID, &a.AttachmentType, &a.Ext, &a.SourceURL, &a.LocalPath, &a.ContentHash, &a.SizeBytes, &a.Status, &a.LastError, &a.SyncedAt); sErr != nil {
			return nil, sErr
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PreviewCleanup calculates the impact scope of deleting a resource, as required by Spec Section 5.9.
func (s *SQLStore) PreviewCleanup(ctx context.Context, resourceID string) (CleanupImpactPreview, error) {
	res, err := s.GetResource(ctx, resourceID)
	if err != nil {
		return CleanupImpactPreview{}, err
	}
	atts, _ := s.ListAttachments(ctx, resourceID)
	attPaths := make([]string, 0, len(atts))
	for _, a := range atts {
		attPaths = append(attPaths, a.LocalPath)
	}
	affected := 1
	if res.IsDir {
		_ = s.db.QueryRowContext(ctx, s.rebind("SELECT COUNT(*) FROM media_resources WHERE source_path = ? OR source_path LIKE ?"), res.SourcePath, res.SourcePath+"/%").Scan(&affected)
	}
	return CleanupImpactPreview{
		ResourceID:        res.ID,
		SourcePath:        res.SourcePath,
		Status:            res.Status,
		LocalSTRMPath:     res.LocalSTRMPath,
		AttachmentPaths:   attPaths,
		AffectedResources: affected,
		RequiresConfirm:   true,
	}, nil
}

// ConfirmDeleteResource deletes a resource record ONLY when confirmed=true, enforcing V1.0 `keep` safety.
func (s *SQLStore) ConfirmDeleteResource(ctx context.Context, resourceID string, confirmed bool) (CleanupImpactPreview, error) {
	preview, err := s.PreviewCleanup(ctx, resourceID)
	if err != nil {
		return CleanupImpactPreview{}, err
	}
	if !confirmed {
		return preview, ErrConfirmationRequired
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return preview, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, s.rebind("DELETE FROM media_attachments WHERE resource_id = ?"), resourceID); err != nil {
		return preview, err
	}
	if _, err := tx.ExecContext(ctx, s.rebind("DELETE FROM managed_resources WHERE resource_id = ?"), resourceID); err != nil {
		return preview, err
	}
	if _, err := tx.ExecContext(ctx, s.rebind("DELETE FROM media_resources WHERE id = ?"), resourceID); err != nil {
		return preview, err
	}
	if err := tx.Commit(); err != nil {
		return preview, err
	}
	return preview, nil
}

func (s *SQLStore) SetItemAvailabilityBySTRMPath(ctx context.Context, localSTRMPath, availability string) {
	if localSTRMPath == "" || s.dialect != "postgres" {
		return
	}
	_, _ = s.db.ExecContext(ctx, s.rebind("UPDATE items SET availability = ? WHERE path = ?"), availability, localSTRMPath)
}

func (s *SQLStore) UpsertScheduledTask(ctx context.Context, t ScheduledTaskState) error {
	q := s.rebind(`
INSERT INTO scheduled_tasks(task_key, name, interval_sec, enabled, status, last_run_at, next_run_at, last_duration_ms, last_error)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(task_key) DO UPDATE SET
	name = excluded.name,
	interval_sec = excluded.interval_sec,
	enabled = excluded.enabled,
	status = excluded.status,
	last_run_at = excluded.last_run_at,
	next_run_at = excluded.next_run_at,
	last_duration_ms = excluded.last_duration_ms,
	last_error = excluded.last_error`)
	_, err := s.db.ExecContext(ctx, q,
		t.TaskKey, t.Name, t.IntervalSec, boolToInt(t.Enabled), t.Status,
		t.LastRunAt, t.NextRunAt, t.LastDurationMs, t.LastError,
	)
	return err
}

func (s *SQLStore) ListScheduledTasks(ctx context.Context) ([]ScheduledTaskState, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT task_key, name, interval_sec, enabled, status, last_run_at, next_run_at, last_duration_ms, last_error FROM scheduled_tasks ORDER BY task_key ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScheduledTaskState
	for rows.Next() {
		var t ScheduledTaskState
		var en int64
		if sErr := rows.Scan(&t.TaskKey, &t.Name, &t.IntervalSec, &en, &t.Status, &t.LastRunAt, &t.NextRunAt, &t.LastDurationMs, &t.LastError); sErr != nil {
			return nil, sErr
		}
		t.Enabled = en != 0
		out = append(out, t)
	}
	return out, rows.Err()
}
