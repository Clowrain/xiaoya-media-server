package scheduler

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
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

	sched := New(store, pipe, 10*time.Minute, true)
	if err := sched.InitTasks(ctx); err != nil {
		t.Fatalf("init tasks: %v", err)
	}

	tasks, err := store.ListScheduledTasks(ctx)
	if err != nil {
		t.Fatalf("list scheduled tasks: %v", err)
	}
	if len(tasks) != 10 {
		t.Fatalf("expected 10 scheduled tasks, got %d", len(tasks))
	}
	if !sched.HasTask("hot_directory_reconcile") {
		t.Fatalf("expected hot_directory_reconcile task to be registered")
	}
	if !sched.HasTask("tmdb_scrape_retry") {
		t.Fatalf("expected tmdb_scrape_retry task to be registered")
	}
	if !sched.HasTask("tmdb_ongoing_reconcile") {
		t.Fatalf("expected tmdb_ongoing_reconcile task to be registered")
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
	if err := os.WriteFile(reprocessed.LocalSTRMPath, []byte("corrupt"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := sched.RunTaskNow(ctx, "strm_consistency_check"); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(reprocessed.LocalSTRMPath); err != nil || string(b) != res.SourceContent {
		t.Fatalf("corrupt STRM not repaired: %q %v", b, err)
	}

}

func TestAttachmentTaskRefreshesSameURLWithoutNewSourceMetadata(t *testing.T) {
	ctx := context.Background()
	d := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(d, "business.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := discovery.NewSQLStore(db, "sqlite")
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var changed atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/d/Show/tvshow.nfo" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		body := "<tvshow><plot>First</plot></tvshow>"
		if changed.Load() {
			body = "<tvshow><plot>Updated</plot></tvshow>"
		}
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()
	cfg := config.Default()
	cfg.AList.BaseURL = upstream.URL
	cfg.STRM.OutputRoot = filepath.Join(d, "strm")
	cfg.Xiaoya.SQLitePath = filepath.Join(d, "no-source.db")
	p, err := integration.NewPipeline(ctx, cfg, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r, err := p.Discovery().RegisterManualResource(ctx, "/Show/tvshow.nfo", "Show/tvshow.nfo", "tvshow.nfo", false, upstream.URL+"/d/Show/tvshow.nfo", discovery.ManageManualFile, "", "")
	if err != nil || r.LocalSTRMPath == "" {
		t.Fatalf("attachment not created: %+v %v", r, err)
	}
	changed.Store(true)
	s := New(store, p, time.Minute, true)
	if err = s.RunTaskNow(ctx, "attachment_diff_sync"); err != nil {
		t.Fatal(err)
	}
	if data, e := os.ReadFile(r.LocalSTRMPath); e != nil || string(data) != "<tvshow><plot>Updated</plot></tvshow>" {
		t.Fatalf("same URL update not synced: %s %v", data, e)
	}
}

func TestConfiguredRootDoesNotOverrideStoredMonitorSettings(t *testing.T) {
	ctx := context.Background()
	d := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(d, "business.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := discovery.NewSQLStore(db, "sqlite")
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := store.UpsertMonitorRoot(ctx, "/Paused", false, 33)
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.UpsertMonitorRoot(ctx, "/Slow", true, 600)
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.UpsertMonitorRoot(ctx, "/Fast", true, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{b.ID, c.ID} {
		if err = store.UpdateMonitorRootStatus(ctx, id, 0, "", "SUCCESS", "", time.Now().Unix()-10); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Monitor.Roots = []string{"/Paused"}
	cfg.Xiaoya.SQLitePath = filepath.Join(d, "no-source.db")
	p, err := integration.NewPipeline(ctx, cfg, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	s := New(store, p, time.Minute, true)
	paused, err := store.GetMonitorRoot(ctx, a.ID)
	if err != nil || paused.Enabled || paused.IntervalSec != 33 {
		t.Fatalf("seed overwrote stored settings: %+v %v", paused, err)
	}
	due, err := s.dueMonitorRoots(ctx)
	if err != nil || len(due) != 1 || due[0].ID != c.ID {
		t.Fatalf("scheduler ignored individual interval: %+v %v", due, err)
	}
}

func TestManifestCheckpointRetriesAndIdleChecks(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(dir, "biz.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	store := discovery.NewSQLStore(db, "sqlite")
	if e = store.EnsureSchema(ctx); e != nil {
		t.Fatal(e)
	}
	manifest := filepath.Join(dir, "strm.txt")
	if e = os.WriteFile(manifest, []byte("show/A.strm#https://example.com/a\nother/B.strm#https://example.com/b\n"), 0600); e != nil {
		t.Fatal(e)
	}
	cfg := config.Default()
	cfg.Xiaoya.StrmTextPath = manifest
	cfg.STRM.OutputRoot = filepath.Join(dir, "out")
	cfg.Monitor.Enabled = false
	p, e := integration.NewPipeline(ctx, cfg, nil, store)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	roots, e := p.Discovery().RegisterMonitorRoots(ctx, []string{"/show"}, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	s := New(store, p, time.Minute, true)
	// 首次启动要处理当前清单；失败不能记作已处理指纹。
	if _, e = db.Exec("CREATE TRIGGER reject_resource BEFORE INSERT ON media_resources BEGIN SELECT RAISE(ABORT,'injected failure'); END"); e != nil {
		t.Fatal(e)
	}
	if e = s.runIncrementalMonitorScan(ctx); e == nil {
		t.Fatal("同步失败被吞掉")
	}
	if len(s.manifestFP) != 0 {
		t.Fatal("失败推进了指纹")
	}
	db.Exec("DROP TRIGGER reject_resource")
	store.UpdateMonitorRootStatus(ctx, roots[0].ID, 0, "", discovery.BatchFailed, "", time.Now().Unix()-120)
	if e = s.runIncrementalMonitorScan(ctx); e != nil {
		t.Fatal(e)
	}
	if len(s.manifestFP) != 1 {
		t.Fatal("成功后未保存指纹")
	}
	var before, after int
	db.QueryRow("SELECT count(*) FROM sync_batches").Scan(&before)
	store.UpdateMonitorRootStatus(ctx, roots[0].ID, 0, "", discovery.BatchSuccess, "", time.Now().Unix()-120)
	if e = s.runIncrementalMonitorScan(ctx); e != nil {
		t.Fatal(e)
	}
	db.QueryRow("SELECT count(*) FROM sync_batches").Scan(&after)
	if before != after {
		t.Fatal("无变化仍创建同步批次")
	}
	due, e := s.dueMonitorRoots(ctx)
	if e != nil || len(due) != 0 {
		t.Fatalf("无变化检查未推进下次时间: %v %v", due, e)
	}
	if _, e = p.Discovery().RegisterMonitorRoots(ctx, []string{"/other"}, time.Minute); e != nil {
		t.Fatal(e)
	}
	if e = s.runIncrementalMonitorScan(ctx); e != nil {
		t.Fatal(e)
	}
	if len(s.manifestFP) != 2 {
		t.Fatal("同一清单新增根没有同步")
	}
}
