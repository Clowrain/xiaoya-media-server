package discovery

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ApplyManualStream 分批应用已完整校验的来源快照；监控根只在读取成功后进行缺失对账。
// consume 接收每批生成的路径，避免完整导入结果常驻内存。
func (s *Service) ApplyManualStream(ctx context.Context, m ManualSnapshot, rootID string, read func(func(ManualNode) error) error, consume func([]string) int) (SyncBatch, error) {
	root := MonitorRoot{ID: rootID, RootPath: m.RootPath}
	if root.ID == "" {
		root.ID = "import:" + m.RootPath
	}
	release, ok := s.acquireRoot(root)
	if !ok {
		return SyncBatch{}, ErrRootScanInProgress
	}
	defer release()
	b, err := s.store.CreateBatch(ctx, rootID, m.RootPath, ScanModeManualImport, false)
	if err != nil {
		return b, err
	}
	page := make([]ManualNode, 0, 200)
	flush := func() error {
		if len(page) == 0 {
			return nil
		}
		m.Nodes = page
		pending, _, e := s.commitManual(ctx, &b, m)
		if e != nil {
			return e
		}
		if e = s.materializePending(ctx, &b, pending); e != nil {
			return e
		}
		if consume != nil {
			b.UnindexedSTRMCount += consume(b.MaterializedSTRMs)
		}
		b.MaterializedSTRMs = nil
		page = page[:0]
		return nil
	}
	err = read(func(n ManualNode) error {
		page = append(page, n)
		if len(page) == cap(page) {
			return flush()
		}
		return ctx.Err()
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		return s.failBatch(b, err)
	}
	if rootID != "" {
		if err = s.reconcileManualMissing(ctx, &b, m); err != nil {
			return s.failBatch(b, err)
		}
	}
	b, err = s.finishMaterialization(ctx, b, nil, 0)
	if err == nil && b.FailedCount > 0 {
		err = fmt.Errorf("%d 个资源处理失败: %s", b.FailedCount, b.ErrorMessage)
	}
	return b, err
}

func (s *Service) reconcileManualMissing(ctx context.Context, b *SyncBatch, m ManualSnapshot) error {
	s.writes.Lock()
	defer s.writes.Unlock()
	origin := m.OriginType
	if origin == "" {
		origin = OriginAListManual
	}
	after := ""
	prefix := strings.TrimRight(m.RootPath, "/") + "/"
	for {
		tx, err := s.store.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		exclusions, err := s.store.suppressions(ctx, tx, origin)
		if err != nil {
			tx.Rollback()
			return err
		}
		rows, err := tx.QueryContext(ctx, s.store.rebind("SELECT "+mediaResourceCols+" FROM media_resources WHERE id>? AND origin_type=? AND (source_path=? OR substr(source_path,1,length(?))=?) AND last_seen_batch_id<>? AND status<>? ORDER BY id LIMIT 200"), after, origin, m.RootPath, prefix, prefix, b.ID, StatusMissing)
		if err != nil {
			tx.Rollback()
			return err
		}
		page := []MediaResource{}
		for rows.Next() {
			r, e := scanMediaResource(rows)
			if e != nil {
				rows.Close()
				tx.Rollback()
				return e
			}
			page = append(page, r)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			tx.Rollback()
			return err
		}
		for _, r := range page {
			after = r.ID
			if !suppressed(exclusions, r) {
				if err = s.store.markMissing(ctx, tx, b, r, time.Now().Unix()); err != nil {
					tx.Rollback()
					return err
				}
			}
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if len(page) < 200 {
			return nil
		}
	}
}
