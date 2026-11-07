package scheduler

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/integration"
	"xiaoya-media-server/internal/resource/discovery"
	_ "xiaoya-media-server/internal/resource/xiaoya"
)

func TestSchedulerInitAndSTRMConsistencySelfHeal(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	dbPath := filepath.Join(tmpDir, "biz.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	store := discovery.NewSQLStore(db, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	cfg := config.Default()
	cfg.STRM.OutputRoot = filepath.Join(tmpDir, "media-strm")
	cfg.STRM.TargetBaseURL = "http://xiaoya.host:5678"
	cfg.AList.BaseURL = "http://xiaoya.host:5678"
	cfg.Monitor.Enabled = false
	cfg.Xiaoya.SQLitePath = filepath.Join(tmpDir, "nonexistent_strm_internal.db")

	pipe, err := integration.NewPipeline(ctx, cfg, nil, store)
	if err != nil {
		t.Fatalf("new pipeline: %v", err)
	}
	defer pipe.Close()

	sched := New(store, pipe, nil, 10*time.Minute, true)
	if err := sched.InitTasks(ctx); err != nil {
		t.Fatalf("init tasks: %v", err)
	}

	tasks, err := store.ListScheduledTasks(ctx)
	if err != nil {
		t.Fatalf("list scheduled tasks: %v", err)
	}
	if len(tasks) != 7 {
		t.Fatalf("expected 7 scheduled tasks, got %d", len(tasks))
	}

	// Register an ACTIVE manual media resource, generate its STRM, delete the STRM on disk, and verify self-heal.
	res, err := pipe.Discovery().RegisterManualResource(
		ctx,
		"/每日更新/电视剧/国产剧/折月亮 (2026)/Season 1/E01.mp4",
		"每日更新/电视剧/国产剧/折月亮 (2026)/Season 1/E01.mp4",
		"E01.mp4",
		false,
		"http://xiaoya.host:5678/d/%E6%AF%8F%E6%97%A5%E6%9B%B4%E6%96%B0/%E7%94%B5%E8%A7%86%E5%89%A7/%E5%9B%BD%E4%BA%A7%E5%89%A7/%E6%8A%98%E6%9C%88%E4%BA%AE%20(2026)/Season%201/E01.mp4",
		discovery.ManageManualFile,
		"",
		"",
	)
	if err != nil {
		t.Fatalf("register manual resource: %v", err)
	}
	reprocessed, err := pipe.Discovery().ReprocessResource(ctx, res.ID)
	if err != nil {
		t.Fatalf("initial reprocess: %v", err)
	}
	if reprocessed.LocalSTRMPath == "" {
		t.Fatalf("expected non-empty LocalSTRMPath")
	}

	// Simulate accidental deletion of local STRM file.
	if err := os.Remove(reprocessed.LocalSTRMPath); err != nil {
		t.Fatalf("remove local strm: %v", err)
	}

	if err := sched.RunTaskNow(ctx, "strm_consistency_check"); err != nil {
		t.Fatalf("run strm_consistency_check: %v", err)
	}

	if _, err := os.Stat(reprocessed.LocalSTRMPath); err != nil {
		t.Fatalf("expected strm_consistency_check to self-heal missing STRM, stat err: %v", err)
	}
}
