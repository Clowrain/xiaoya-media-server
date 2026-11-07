-- 002_resource_engine.sql
-- Unified Xiaoya/AList Resource Discovery, Sync Batches, Cursors, Managed Resources,
-- Change History, Attachments, Scheduled Tasks, and Media Availability (`keep` policy).

ALTER TABLE items ADD COLUMN IF NOT EXISTS availability TEXT NOT NULL DEFAULT 'AVAILABLE';

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
