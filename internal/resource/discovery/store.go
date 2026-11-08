package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"xiaoya-media-server/migrations"
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

-- V2 model. These tables are additive during the development cut-over; the
-- legacy resource tables above remain readable until the new pipeline owns all
-- callers.
CREATE TABLE IF NOT EXISTS media_pipeline_schema (
    component TEXT PRIMARY KEY,
    version BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS source_connections (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    sqlite_path TEXT NOT NULL DEFAULT '',
    generation BIGINT NOT NULL DEFAULT 1,
    data_version BIGINT NOT NULL DEFAULT 0,
    enabled BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_source_connections_path ON source_connections(kind, sqlite_path);

CREATE TABLE IF NOT EXISTS source_resources_v2 (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    source_generation BIGINT NOT NULL,
    source_node_id BIGINT NOT NULL,
    parent_node_id BIGINT NOT NULL DEFAULT 0,
    path TEXT NOT NULL,
    rel_path TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    is_dir BIGINT NOT NULL DEFAULT 0,
    content TEXT NOT NULL DEFAULT '',
    name_hash TEXT NOT NULL DEFAULT '',
    path_hash TEXT NOT NULL DEFAULT '',
    content_hash TEXT NOT NULL DEFAULT '',
    snapshot_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'ACTIVE',
    first_seen_at BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    UNIQUE(source_id, source_generation, source_node_id),
    UNIQUE(source_id, source_generation, path)
);
CREATE INDEX IF NOT EXISTS idx_source_resources_v2_parent ON source_resources_v2(source_id, source_generation, parent_node_id);

CREATE TABLE IF NOT EXISTS recognition_results (
    id TEXT PRIMARY KEY,
    source_resource_id TEXT NOT NULL UNIQUE,
    media_type TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    year BIGINT NOT NULL DEFAULT 0,
    external_id TEXT NOT NULL DEFAULT '',
    season_number BIGINT NOT NULL DEFAULT 0,
    episode_number BIGINT NOT NULL DEFAULT 0,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
    evidence TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'PENDING',
    revision BIGINT NOT NULL DEFAULT 1,
    last_error TEXT NOT NULL DEFAULT '',
    updated_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_recognition_results_state ON recognition_results(state, updated_at);

CREATE TABLE IF NOT EXISTS media_items_v2 (
    id TEXT PRIMARY KEY,
    media_type TEXT NOT NULL,
    title TEXT NOT NULL,
    sort_title TEXT NOT NULL DEFAULT '',
    year BIGINT NOT NULL DEFAULT 0,
    tmdb_id TEXT NOT NULL DEFAULT '',
    tvdb_id TEXT NOT NULL DEFAULT '',
    poster_url TEXT NOT NULL DEFAULT '',
    backdrop_url TEXT NOT NULL DEFAULT '',
    logo_url TEXT NOT NULL DEFAULT '',
    parent_id TEXT NOT NULL DEFAULT '',
    season_number BIGINT NOT NULL DEFAULT 0,
    episode_number BIGINT NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'ACTIVE',
    revision BIGINT NOT NULL DEFAULT 1,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_items_v2_identity ON media_items_v2(media_type, title, year, parent_id, season_number, episode_number);

CREATE TABLE IF NOT EXISTS media_versions_v2 (
    id TEXT PRIMARY KEY,
    media_id TEXT NOT NULL,
    source_resource_id TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    stream_uri TEXT NOT NULL,
    stable_suffix TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'ACTIVE',
    revision BIGINT NOT NULL DEFAULT 1,
    updated_at BIGINT NOT NULL,
    UNIQUE(media_id, source_resource_id)
);

CREATE TABLE IF NOT EXISTS media_artifacts_v2 (
    id TEXT PRIMARY KEY,
    media_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    path TEXT NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'PENDING',
    revision BIGINT NOT NULL DEFAULT 1,
    updated_at BIGINT NOT NULL,
    UNIQUE(media_id, kind, path)
);

CREATE TABLE IF NOT EXISTS work_jobs_v2 (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL,
    scope_key TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1,
    payload TEXT NOT NULL DEFAULT '{}',
    status TEXT NOT NULL DEFAULT 'QUEUED',
    attempts BIGINT NOT NULL DEFAULT 0,
    available_at BIGINT NOT NULL,
    lease_until BIGINT NOT NULL DEFAULT 0,
    worker TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_work_jobs_v2_dedupe ON work_jobs_v2(kind, scope_key, revision, status);
CREATE INDEX IF NOT EXISTS idx_work_jobs_v2_claim ON work_jobs_v2(status, available_at, lease_until);

CREATE TABLE IF NOT EXISTS metadata_cache_v2 (
    cache_key TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    payload TEXT NOT NULL,
    expires_at BIGINT NOT NULL DEFAULT 0,
    updated_at BIGINT NOT NULL
);
`

func (s *SQLStore) EnsureSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, resourceEngineDDL); err != nil {
		return fmt.Errorf("ensure resource engine schema: %w", err)
	}
	if err := s.ensureV2MediaColumns(ctx); err != nil {
		return err
	}
	// If `items` exists in the same DB (unified PostgreSQL), ensure `availability` column exists.
	if s.dialect == "postgres" {
		if _, err := s.db.ExecContext(ctx, "ALTER TABLE IF EXISTS items ADD COLUMN IF NOT EXISTS availability TEXT NOT NULL DEFAULT 'AVAILABLE'"); err != nil {
			return err
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, migrations.ResourceLifecycle); err != nil {
		return fmt.Errorf("migrate resource lifecycle: %w", err)
	}
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, s.rebind(`
INSERT INTO media_pipeline_schema(component, version, created_at, updated_at)
VALUES('xiaoya-media-pipeline', 2, ?, ?)
ON CONFLICT(component) DO UPDATE SET version=excluded.version, updated_at=excluded.updated_at`), now, now); err != nil {
		return fmt.Errorf("record media pipeline schema: %w", err)
	}
	return tx.Commit()
}

func (s *SQLStore) ensureV2MediaColumns(ctx context.Context) error {
	columns := []string{"poster_url", "backdrop_url", "logo_url"}
	if s.dialect == "postgres" {
		for _, column := range columns {
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE media_items_v2 ADD COLUMN IF NOT EXISTS %s TEXT NOT NULL DEFAULT ''", column)); err != nil {
				return fmt.Errorf("ensure media_items_v2.%s: %w", column, err)
			}
		}
		return nil
	}
	rows, err := s.db.QueryContext(ctx, "PRAGMA table_info(media_items_v2)")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		seen[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, column := range columns {
		if !seen[column] {
			if _, err := s.db.ExecContext(ctx, "ALTER TABLE media_items_v2 ADD COLUMN "+column+" TEXT NOT NULL DEFAULT ''"); err != nil {
				return fmt.Errorf("ensure media_items_v2.%s: %w", column, err)
			}
		}
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

// EnqueueV2Job persists a deduplicated background job.  A queued/running job
// for the same kind, scope and revision is reused rather than duplicated.
func (s *SQLStore) EnqueueV2Job(ctx context.Context, kind, scopeKey string, revision int64, payload string, availableAt int64) (V2WorkJob, error) {
	if revision <= 0 {
		revision = 1
	}
	if availableAt <= 0 {
		availableAt = time.Now().Unix()
	}
	now := time.Now().Unix()
	if payload == "" {
		payload = "{}"
	}
	id := StableID(fmt.Sprintf("v2-job:%s:%s:%d:%d", kind, scopeKey, revision, now))
	q := s.rebind(`INSERT INTO work_jobs_v2(id,kind,scope_key,revision,payload,status,available_at,created_at,updated_at)
VALUES(?,?,?,?,?,'QUEUED',?,?,?) ON CONFLICT DO NOTHING`)
	if _, err := s.db.ExecContext(ctx, q, id, kind, scopeKey, revision, payload, availableAt, now, now); err != nil {
		return V2WorkJob{}, err
	}
	row := s.db.QueryRowContext(ctx, s.rebind(`SELECT id,kind,scope_key,revision,payload,status,attempts,available_at,lease_until,worker,last_error,created_at,updated_at
FROM work_jobs_v2 WHERE kind=? AND scope_key=? AND revision=? AND status IN ('QUEUED','RUNNING') ORDER BY created_at DESC LIMIT 1`), kind, scopeKey, revision)
	return scanV2Job(row)
}

func scanV2Job(scanner interface{ Scan(...any) error }) (V2WorkJob, error) {
	var j V2WorkJob
	err := scanner.Scan(&j.ID, &j.Kind, &j.ScopeKey, &j.Revision, &j.Payload, &j.Status, &j.Attempts, &j.AvailableAt, &j.LeaseUntil, &j.Worker, &j.LastError, &j.CreatedAt, &j.UpdatedAt)
	return j, err
}

// ClaimV2Job leases one ready job. PostgreSQL uses SKIP LOCKED; SQLite uses a
// short transaction and the same status predicate, keeping the NAS path
// single-worker and avoiding long-lived database locks.
func (s *SQLStore) ClaimV2Job(ctx context.Context, worker string, lease time.Duration) (V2WorkJob, error) {
	if lease <= 0 {
		lease = 10 * time.Minute
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return V2WorkJob{}, err
	}
	defer tx.Rollback()
	_, _ = tx.ExecContext(ctx, s.rebind(`UPDATE work_jobs_v2 SET status='QUEUED', lease_until=0, worker='', updated_at=? WHERE status='RUNNING' AND lease_until>0 AND lease_until<?`), now, now)
	query := `SELECT id,kind,scope_key,revision,payload,status,attempts,available_at,lease_until,worker,last_error,created_at,updated_at
FROM work_jobs_v2 WHERE status='QUEUED' AND available_at<=? ORDER BY available_at ASC, created_at ASC LIMIT 1`
	if s.dialect == "postgres" {
		query += " FOR UPDATE SKIP LOCKED"
	}
	row := tx.QueryRowContext(ctx, s.rebind(query), now)
	job, err := scanV2Job(row)
	if err != nil {
		return V2WorkJob{}, err
	}
	if _, err = tx.ExecContext(ctx, s.rebind(`UPDATE work_jobs_v2 SET status='RUNNING', attempts=attempts+1, lease_until=?, worker=?, updated_at=? WHERE id=? AND status='QUEUED'`), now+int64(lease.Seconds()), worker, now, job.ID); err != nil {
		return V2WorkJob{}, err
	}
	job.Status, job.Attempts, job.LeaseUntil, job.Worker, job.UpdatedAt = "RUNNING", job.Attempts+1, now+int64(lease.Seconds()), worker, now
	if err = tx.Commit(); err != nil {
		return V2WorkJob{}, err
	}
	return job, nil
}

func (s *SQLStore) CompleteV2Job(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE work_jobs_v2 SET status='SUCCESS', lease_until=0, worker='', updated_at=? WHERE id=? AND status='RUNNING'`), time.Now().Unix(), id)
	return err
}

func (s *SQLStore) FailV2Job(ctx context.Context, id, message string, retryAt int64) error {
	if retryAt <= 0 {
		retryAt = time.Now().Unix()
	}
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE work_jobs_v2 SET status='QUEUED', available_at=?, lease_until=0, worker='', last_error=?, updated_at=? WHERE id=? AND status='RUNNING'`), retryAt, message, time.Now().Unix(), id)
	return err
}

func (s *SQLStore) UpdateV2Match(ctx context.Context, resourceID, mediaID, tmdbID, title string, year int, posterURL, backdropURL string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, s.rebind(`UPDATE recognition_results SET external_id=?, title=?, year=?, state='MATCHED', last_error='', updated_at=? WHERE source_resource_id=?`), tmdbID, title, year, now, resourceID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.rebind(`UPDATE media_items_v2 SET tmdb_id=?, title=?, sort_title=?, year=?, poster_url=?, backdrop_url=?, updated_at=? WHERE id=?`), tmdbID, title, title, year, posterURL, backdropURL, now, mediaID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.rebind(`UPDATE media_items_v2 SET tmdb_id=?, poster_url=?, backdrop_url=?, updated_at=? WHERE id=(SELECT parent_id FROM media_items_v2 WHERE id=? AND media_type='EPISODE')`), tmdbID, posterURL, backdropURL, now, mediaID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) MarkV2RecognitionState(ctx context.Context, resourceID, state, message string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE recognition_results SET state=?, last_error=?, updated_at=? WHERE source_resource_id=?`), state, message, time.Now().Unix(), resourceID)
	return err
}

func (s *SQLStore) CompleteV2JobsForScope(ctx context.Context, scopeKey string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE work_jobs_v2 SET status='SUCCESS', lease_until=0, worker='', updated_at=? WHERE scope_key=? AND status IN ('QUEUED','RUNNING')`), time.Now().Unix(), scopeKey)
	return err
}

func (s *SQLStore) ListV2RecognitionPending(ctx context.Context, limit int) ([]RecognitionPending, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT r.id,r.source_resource_id,m.source_path,r.media_type,r.title,r.year,r.season_number,r.episode_number,r.state,r.evidence,r.external_id,r.updated_at
FROM recognition_results r JOIN media_resources m ON m.id=r.source_resource_id
WHERE r.state IN ('NEEDS_CONFIRMATION','READY_FOR_MATCH') ORDER BY r.updated_at ASC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecognitionPending
	for rows.Next() {
		var item RecognitionPending
		if err := rows.Scan(&item.ID, &item.ResourceID, &item.SourcePath, &item.MediaType, &item.Title, &item.Year, &item.SeasonNumber, &item.EpisodeNumber, &item.State, &item.Evidence, &item.ExternalID, &item.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
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
	return s.saveCursor(ctx, s.db, c)
}

func (s *SQLStore) saveCursor(ctx context.Context, db sqlExecer, c SyncCursor) error {
	if c.UpdatedAt == 0 {
		c.UpdatedAt = time.Now().Unix()
	}
	q := s.rebind(`
INSERT INTO sync_cursors(root_id, root_node_id, last_max_node_id, last_success_batch_id, last_complete_scan_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?)
ON CONFLICT(root_id) DO UPDATE SET
	root_node_id = excluded.root_node_id,
	last_max_node_id = excluded.last_max_node_id,
	last_success_batch_id = excluded.last_success_batch_id,
	last_complete_scan_at = CASE WHEN excluded.last_complete_scan_at > 0 THEN excluded.last_complete_scan_at ELSE sync_cursors.last_complete_scan_at END,
	updated_at = excluded.updated_at`)
	_, err := db.ExecContext(ctx, q, c.RootID, c.RootNodeID, c.LastMaxNodeID, c.LastSuccessBatchID, c.LastCompleteScanAt, c.UpdatedAt)
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
	return s.loadRootResources(ctx, s.db, rootID)
}

func (s *SQLStore) loadRootResources(ctx context.Context, db sqlQueryer, rootID string) (map[string]MediaResource, error) {
	rows, err := db.QueryContext(ctx, s.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE root_id = ?"), rootID)
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
	return s.resourceByKey(ctx, s.db, sourceKey)
}

func (s *SQLStore) resourceByKey(ctx context.Context, db sqlQueryer, sourceKey string) (MediaResource, bool, error) {
	row := db.QueryRowContext(ctx, s.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE source_key = ?"), sourceKey)
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

// PreviewCleanup reports which resources will stop syncing; the catalog and files stay intact.
func (s *SQLStore) PreviewCleanup(ctx context.Context, id string) (CleanupImpactPreview, error) {
	res, err := s.GetResource(ctx, id)
	if err != nil {
		return CleanupImpactPreview{}, err
	}
	atts, err := s.ListAttachments(ctx, id)
	if err != nil {
		return CleanupImpactPreview{}, err
	}
	paths := make([]string, 0, len(atts))
	for _, a := range atts {
		paths = append(paths, a.LocalPath)
	}
	count := 1
	if res.IsDir {
		prefix := res.SourcePath + "/"
		err = s.db.QueryRowContext(ctx, s.rebind("SELECT COUNT(*) FROM media_resources WHERE origin_type=? AND (id=? OR substr(source_path,1,length(?))=?)"), res.OriginType, id, prefix, prefix).Scan(&count)
		if err != nil {
			return CleanupImpactPreview{}, err
		}
	}
	blocked, err := s.IsUnmanaged(ctx, res)
	return CleanupImpactPreview{ResourceID: id, SourcePath: res.SourcePath, Status: res.Status, LocalSTRMPath: res.LocalSTRMPath, AttachmentPaths: paths, AffectedResources: count, RequiresConfirm: true, Unmanaged: blocked}, err
}

func (s *SQLStore) ConfirmDeleteResource(ctx context.Context, id string, confirmed bool) (CleanupImpactPreview, error) {
	preview, err := s.PreviewCleanup(ctx, id)
	if err != nil {
		return preview, err
	}
	if !confirmed {
		return preview, ErrConfirmationRequired
	}
	_, err = s.db.ExecContext(ctx, s.rebind(`INSERT INTO resource_suppressions(resource_id,origin_type,source_path,is_dir)
SELECT id,origin_type,source_path,is_dir FROM media_resources WHERE id=?
ON CONFLICT(resource_id) DO UPDATE SET source_path=excluded.source_path`), id)
	if err == nil {
		preview.Unmanaged = true
	}
	return preview, err
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
