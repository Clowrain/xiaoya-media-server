package scheduler

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"xiaoya-media-server/internal/integration"
	"xiaoya-media-server/internal/resource/alist"
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
	importer *alist.Importer

	mu      sync.Mutex
	specs   map[string]*TaskSpec
	order   []string
	running map[string]bool
}

func New(store *discovery.SQLStore, pipeline *integration.Pipeline, importer *alist.Importer, monitorInterval time.Duration, monitorEnabled bool) *Scheduler {
	if monitorInterval <= 0 {
		monitorInterval = 10 * time.Minute
	}
	s := &Scheduler{
		store:    store,
		pipeline: pipeline,
		importer: importer,
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
	s.register(TaskSpec{
		Key:      "tracked_alist_sync",
		Name:     "人工入库剧集目录追更检查",
		Interval: 30 * time.Minute,
		Enabled:  true,
		Fn:       s.runTrackedAListSync,
	})
	s.register(TaskSpec{
		Key:      "attachment_diff_sync",
		Name:     "附件差异同步与重试",
		Interval: 30 * time.Minute,
		Enabled:  true,
		Fn:       s.runFailedRetry,
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
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
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

func (s *Scheduler) runIncrementalMonitorScan(ctx context.Context) error {
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
		if _, sErr := s.pipeline.RunIncrementalSyncRoot(ctx, r.ID); sErr != nil && firstErr == nil {
			firstErr = sErr
		}
	}
	return firstErr
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
		if _, sErr := s.pipeline.RunFullSyncRoot(ctx, r.ID); sErr != nil && firstErr == nil {
			firstErr = sErr
		}
	}
	return firstErr
}

func (s *Scheduler) runTrackedAListSync(ctx context.Context) error {
	if s.importer == nil {
		return nil
	}
	_, err := s.importer.SyncTrackedDirectories(ctx)
	return err
}

func (s *Scheduler) runFailedRetry(ctx context.Context) error {
	if s.pipeline == nil {
		return nil
	}
	failedList, _, err := s.store.ListResources(ctx, discovery.ResourceFilter{
		Status:    discovery.StatusFailed,
		OnlyFiles: true,
		Limit:     100,
	})
	if err != nil {
		return err
	}
	for _, res := range failedList {
		if res.RetryCount >= 5 {
			continue
		}
		_, _ = s.pipeline.Discovery().ReprocessResource(ctx, res.ID)
	}
	s.pipeline.FlushScopedLibraryScans()
	return nil
}

// runSTRMConsistencyCheck ensures all ACTIVE media resources still have their generated `.strm` file on disk
// (while leaving MISSING resources untouched per `keep` policy).
func (s *Scheduler) runSTRMConsistencyCheck(ctx context.Context) error {
	if s.pipeline == nil {
		return nil
	}
	offset := 0
	for {
		items, total, err := s.store.ListResources(ctx, discovery.ResourceFilter{
			Status:    discovery.StatusActive,
			OnlyFiles: true,
			Limit:     200,
			Offset:    offset,
		})
		if err != nil {
			return err
		}
		for _, res := range items {
			if res.LocalSTRMPath == "" {
				continue
			}
			if _, stErr := os.Stat(res.LocalSTRMPath); os.IsNotExist(stErr) {
				_, _ = s.pipeline.Discovery().ReprocessResource(ctx, res.ID)
			}
		}
		offset += len(items)
		if offset >= total || len(items) == 0 {
			break
		}
	}
	s.pipeline.FlushScopedLibraryScans()
	return nil
}
