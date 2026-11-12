-- Canonical portable resource-engine schema used by SQLStore.EnsureSchema.
-- Postgres-only items.availability changes are applied by the store's dialect branch.

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
