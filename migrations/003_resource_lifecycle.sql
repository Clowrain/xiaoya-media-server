-- Apply after 002 in a transaction; startup applies the same migration.
CREATE TABLE IF NOT EXISTS pending_materialization (
    resource_id TEXT PRIMARY KEY,
    queued_at BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS resource_suppressions (
    resource_id TEXT PRIMARY KEY, origin_type TEXT NOT NULL,
    source_path TEXT NOT NULL, is_dir BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS materialized_paths (
    local_path TEXT PRIMARY KEY, owner_key TEXT NOT NULL UNIQUE
);
CREATE TABLE IF NOT EXISTS alist_tracking_roots (
    resource_id TEXT PRIMARY KEY, review BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS resource_migrations (id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS admin_jobs (
    id TEXT PRIMARY KEY, kind TEXT NOT NULL, scope TEXT NOT NULL,
    status TEXT NOT NULL, result TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL, finished_at BIGINT NOT NULL DEFAULT 0
);

-- Distinguish explicit tracking roots, ambiguous legacy children, and one-shot roots.
INSERT INTO alist_tracking_roots(resource_id,review)
SELECT r.id, CASE WHEN EXISTS(
 SELECT 1 FROM media_resources p JOIN managed_resources pm ON p.id=pm.resource_id
 WHERE p.is_dir=1 AND p.origin_type=r.origin_type AND pm.manage_mode='MANUAL_SERIES_DIR' AND pm.enabled=0
 AND substr(r.source_path,1,length(p.source_path)+1)=p.source_path||'/'
) THEN 1 ELSE 0 END
FROM media_resources r JOIN managed_resources m ON r.id=m.resource_id
WHERE r.origin_type='ALIST_MANUAL' AND r.is_dir=1 AND m.manage_mode='MANUAL_SERIES_DIR' AND m.enabled=1
AND NOT EXISTS(SELECT 1 FROM media_resources p JOIN managed_resources pm ON p.id=pm.resource_id
 WHERE p.is_dir=1 AND p.origin_type=r.origin_type AND pm.manage_mode='MANUAL_SERIES_DIR' AND pm.enabled=1
 AND substr(r.source_path,1,length(p.source_path)+1)=p.source_path||'/')
AND NOT EXISTS(SELECT 1 FROM resource_migrations WHERE id='alist_tracking_roots_v1')
ON CONFLICT DO NOTHING;
INSERT INTO alist_tracking_roots(resource_id,review)
SELECT r.id,2 FROM media_resources r JOIN managed_resources m ON m.resource_id=r.id
WHERE r.origin_type='ALIST_MANUAL' AND r.is_dir=1 AND m.manage_mode='MANUAL_SERIES_DIR' AND m.enabled=0
AND NOT EXISTS(SELECT 1 FROM resource_migrations WHERE id='alist_tracking_roots_v1')
ON CONFLICT DO NOTHING;
INSERT INTO resource_suppressions(resource_id,origin_type,source_path,is_dir)
SELECT r.id,r.origin_type,r.source_path,r.is_dir FROM media_resources r
JOIN managed_resources m ON m.resource_id=r.id
WHERE m.manage_mode='MANUAL_FILE' AND m.enabled=0 AND NOT EXISTS(SELECT 1 FROM resource_migrations WHERE id='alist_tracking_roots_v1')
ON CONFLICT DO NOTHING;

INSERT INTO pending_materialization(resource_id,queued_at)
SELECT id,last_changed_at FROM media_resources WHERE status='FAILED' AND is_dir=0
AND NOT EXISTS(SELECT 1 FROM resource_migrations WHERE id='alist_tracking_roots_v1')
ON CONFLICT DO NOTHING;

INSERT INTO resource_migrations(id) VALUES('alist_tracking_roots_v1') ON CONFLICT DO NOTHING;
