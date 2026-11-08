package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xiaoya-media-server/internal/integration"
	"xiaoya-media-server/internal/resource/discovery"
)

type TaskSpec struct {
	Key      string
	Name     string
	Interval time.Duration
	Enabled  bool
	Fn       func(ctx context.Context) error
}

type Scheduler struct {
	store    *discovery.SQLStore
	pipeline *integration.Pipeline

	mu      sync.Mutex
	specs   map[string]*TaskSpec
	order   []string
	running map[string]bool
}

func New(store *discovery.SQLStore, pipeline *integration.Pipeline, monitorInterval time.Duration, monitorEnabled bool) *Scheduler {
	if monitorInterval <= 0 {
		monitorInterval = 10 * time.Minute
	}
	s := &Scheduler{
		store:    store,
		pipeline: pipeline,
		specs:    make(map[string]*TaskSpec),
		running:  make(map[string]bool),
	}

	s.register(TaskSpec{
		Key:      "monitor_root_scan",
		Name:     "指定监控目录增量同步",
		Interval: monitorInterval,
		Enabled:  monitorEnabled,
		Fn:       s.runIncrementalMonitorScan,
	})
	// AList is an interactive browse/search source only.  Tracking and
	//追更由 SQLite monitor roots统一驱动，避免后台任务调用 AList 资源 API。
	s.register(TaskSpec{
		Key:      "attachment_diff_sync",
		Name:     "附件差异同步与重试",
		Interval: 30 * time.Minute,
		Enabled:  true,
		Fn:       s.runAttachmentDiff,
	})
	s.register(TaskSpec{
		Key:      "daily_full_diff",
		Name:     "完整目录树差异校验（移动/改名/MISSING）",
		Interval: 24 * time.Hour,
		Enabled:  monitorEnabled,
		Fn:       s.runFullDiffScan,
	})
	s.register(TaskSpec{
		Key:      "strm_consistency_check",
		Name:     "本地 STRM 一致性自检与修复",
		Interval: 24 * time.Hour,
		Enabled:  true,
		Fn:       s.runSTRMConsistencyCheck,
	})
	s.register(TaskSpec{
		Key:      "media_index_check",
		Name:     "媒体库局部索引刷新检查",
		Interval: 30 * time.Minute,
		Enabled:  true,
		Fn: func(_ context.Context) error {
			if s.pipeline != nil {
				s.pipeline.FlushScopedLibraryScans()
			}
			return nil
		},
	})
	s.register(TaskSpec{
		Key:      "failed_resource_retry",
		Name:     "失败资源自动重试",
		Interval: 15 * time.Minute,
		Enabled:  true,
		Fn:       s.runFailedRetry,
	})
	s.register(TaskSpec{
		Key:      "metadata_match",
		Name:     "TMDB 作品匹配与元数据缓存",
		Interval: 10 * time.Minute,
		Enabled:  true,
		Fn: func(ctx context.Context) error {
			if s.pipeline == nil {
				return nil
			}
			return s.pipeline.RunMetadataMatchJobs(ctx, 20)
		},
	})
	s.register(TaskSpec{
		Key:      "hot_directory_reconcile",
		Name:     "热目录定点对账（退避调度）",
		Interval: 5 * time.Minute,
		Enabled:  true,
		Fn:       s.runHotDirectoryReconcile,
	})
	return s
}

func (s *Scheduler) register(spec TaskSpec) {
	cp := spec
	s.specs[spec.Key] = &cp
	s.order = append(s.order, spec.Key)
}

// InitTasks seeds `scheduled_tasks` rows in the business database.
func (s *Scheduler) InitTasks(ctx context.Context) error {
	now := time.Now().Unix()
	for _, key := range s.order {
		sp := s.specs[key]
		if err := s.store.UpsertScheduledTask(ctx, discovery.ScheduledTaskState{
			TaskKey:     sp.Key,
			Name:        sp.Name,
			IntervalSec: int64(sp.Interval.Seconds()),
			Enabled:     sp.Enabled,
			Status:      "IDLE",
			NextRunAt:   now + int64(sp.Interval.Seconds()),
		}); err != nil {
			return err
		}
	}
	return nil
}

// Start launches background ticker loops for all enabled tasks until `ctx` is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	_ = s.InitTasks(ctx)
	for _, key := range s.order {
		sp := s.specs[key]
		if !sp.Enabled || sp.Interval <= 0 {
			continue
		}
		taskKey := sp.Key
		interval := sp.Interval
		if taskKey == "monitor_root_scan" {
			interval = time.Second
		}
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if taskKey == "monitor_root_scan" {
						roots, err := s.dueMonitorRoots(ctx)
						if err == nil && len(roots) == 0 {
							continue
						}
					}
					_ = s.RunTaskNow(ctx, taskKey)
				}
			}
		}()
	}
}

// RunTaskNow executes a task immediately if it is not already running.
func (s *Scheduler) RunTaskNow(ctx context.Context, taskKey string) error {
	s.mu.Lock()
	sp, exists := s.specs[taskKey]
	if !exists {
		s.mu.Unlock()
		return fmt.Errorf("unknown scheduled task %q", taskKey)
	}
	if s.running[taskKey] {
		s.mu.Unlock()
		return fmt.Errorf("task %q is already running", taskKey)
	}
	s.running[taskKey] = true
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.running, taskKey)
		s.mu.Unlock()
	}()

	started := time.Now()
	_ = s.store.UpsertScheduledTask(ctx, discovery.ScheduledTaskState{
		TaskKey:     sp.Key,
		Name:        sp.Name,
		IntervalSec: int64(sp.Interval.Seconds()),
		Enabled:     sp.Enabled,
		Status:      "RUNNING",
		LastRunAt:   started.Unix(),
		NextRunAt:   started.Add(sp.Interval).Unix(),
	})

	runErr := sp.Fn(ctx)
	durMs := time.Since(started).Milliseconds()
	status := "IDLE"
	errMsg := ""
	if runErr != nil {
		status = "FAILED"
		errMsg = runErr.Error()
	}

	bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.store.UpsertScheduledTask(bgCtx, discovery.ScheduledTaskState{
		TaskKey:        sp.Key,
		Name:           sp.Name,
		IntervalSec:    int64(sp.Interval.Seconds()),
		Enabled:        sp.Enabled,
		Status:         status,
		LastRunAt:      started.Unix(),
		NextRunAt:      time.Now().Add(sp.Interval).Unix(),
		LastDurationMs: durMs,
		LastError:      errMsg,
	})
	return runErr
}

func (s *Scheduler) dueMonitorRoots(ctx context.Context) ([]discovery.MonitorRoot, error) {
	roots, err := s.store.ListMonitorRoots(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var due []discovery.MonitorRoot
	for _, r := range roots {
		if r.Enabled && now >= r.LastScannedAt+r.IntervalSec && (s.pipeline == nil || !s.pipeline.Discovery().RootPathBusy(r.RootPath)) {
			due = append(due, r)
		}
	}
	return due, nil
}

func (s *Scheduler) runIncrementalMonitorScan(ctx context.Context) error {
	if s.pipeline == nil {
		return nil
	}
	roots, err := s.dueMonitorRoots(ctx)
	if err != nil {
		return err
	}
	var result []error
	for _, r := range roots {
		b, err := s.pipeline.RunIncrementalSyncRoot(ctx, r.ID)
		if err != nil {
			result = append(result, err)
		} else if b.FailedCount > 0 {
			result = append(result, fmt.Errorf("%s: %d 个资源处理失败", r.RootPath, b.FailedCount))
		}
	}
	return errors.Join(result...)
}

func (s *Scheduler) runFullDiffScan(ctx context.Context) error {
	if s.pipeline == nil {
		return nil
	}
	roots, err := s.store.ListMonitorRoots(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, r := range roots {
		if !r.Enabled {
			continue
		}
		b, sErr := s.pipeline.RunFullSyncRoot(ctx, r.ID)
		if firstErr == nil {
			if sErr != nil {
				firstErr = sErr
			} else if b.FailedCount > 0 {
				firstErr = fmt.Errorf("%s: %d 个资源处理失败", r.RootPath, b.FailedCount)
			}
		}
	}
	return firstErr
}

func (s *Scheduler) runFailedRetry(ctx context.Context) error {
	if s.pipeline == nil {
		return nil
	}
	ids, err := s.store.PendingResourceIDs(ctx, 100)
	if err != nil {
		return err
	}
	var first error
	for _, id := range ids {
		_, err = s.pipeline.Discovery().ReprocessResource(ctx, id)
		if first == nil && err != nil && !errors.Is(err, discovery.ErrResourceUnmanaged) && !errors.Is(err, discovery.ErrResourceMissing) {
			first = err
		}
	}
	s.pipeline.FlushScopedLibraryScans()
	return first
}

func (s *Scheduler) runAttachmentDiff(ctx context.Context) error {
	return s.recheckFiles(ctx, true)
}

func (s *Scheduler) runSTRMConsistencyCheck(ctx context.Context) error {
	return s.recheckFiles(ctx, false)
}

func (s *Scheduler) recheckFiles(ctx context.Context, attachments bool) error {
	if s.pipeline == nil {
		return nil
	}
	var first error
	cursor := ""
	for {
		res, err := s.store.MaintenanceFiles(ctx, cursor, 200)
		if err != nil {
			return err
		}
		for _, r := range res {
			cursor = r.ID
			if attachments {
				if !s.pipeline.STRMEngine().IsAttachment(r) {
					continue
				}
			} else {
				if !strings.EqualFold(filepath.Ext(r.LocalSTRMPath), ".strm") {
					continue
				}
				if !s.pipeline.STRMEngine().RequiresSTRMRepair(r) {
					continue
				}
			}
			_, err = s.pipeline.Discovery().ReprocessResource(ctx, r.ID)
			if first == nil && err != nil && !errors.Is(err, discovery.ErrResourceUnmanaged) && !errors.Is(err, discovery.ErrResourceMissing) {
				first = err
			}
		}
		if len(res) < 200 {
			break
		}
	}
	s.pipeline.FlushScopedLibraryScans()
	return first
}

func (s *Scheduler) runHotDirectoryReconcile(ctx context.Context) error {
	if s.pipeline == nil || s.pipeline.Discovery() == nil {
		return nil
	}
	now := time.Now().Unix()
	dueList, err := s.store.GetDueHotDirectories(ctx, now, 10)
	if err != nil {
		return err
	}
	var firstErr error
	for _, hot := range dueList {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := s.pipeline.Discovery().ScanHotDirectory(ctx, hot.ID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if len(dueList) > 0 {
		s.pipeline.FlushScopedLibraryScans()
	}
	return firstErr
}

func (s *Scheduler) HasTask(key string) bool {
	_, ok := s.specs[key]
	return ok
}
