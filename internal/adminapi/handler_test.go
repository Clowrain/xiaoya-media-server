package adminapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/embyserver"
	"xiaoya-media-server/internal/integration"
	"xiaoya-media-server/internal/resource/alist"
	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/scheduler"
)

func TestAdminAPIRoutesMonitorSyncCleanupAndTasks(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Seed Xiaoya SQLite DB
	sqlitePath := filepath.Join(tmp, "strm_internal.db")
	rwX, err := sql.Open("sqlite", sqlitePath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer rwX.Close()
	_, _ = rwX.Exec(`CREATE TABLE nodes (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, parent_id INTEGER NOT NULL DEFAULT 0, is_dir BOOLEAN NOT NULL DEFAULT 0, content TEXT NOT NULL DEFAULT '')`)
	_, _ = rwX.Exec(`INSERT INTO nodes(id,name,parent_id,is_dir,content) VALUES
		(1, '每日更新', 0, 1, ''),
		(2, '电视剧', 1, 1, ''),
		(3, '国产剧', 2, 1, ''),
		(4, '折月亮 (2026)', 3, 1, ''),
		(5, 'E01', 4, 0, 'http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/01.mp4')`)

	bizDB, err := sql.Open("sqlite", filepath.Join(tmp, "biz.db"))
	if err != nil {
		t.Fatalf("open biz db: %v", err)
	}
	defer bizDB.Close()

	store := discovery.NewSQLStore(bizDB, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	cfg := config.Default()
	cfg.Xiaoya.SQLitePath = sqlitePath
	cfg.STRM.OutputRoot = filepath.Join(tmp, "strm-out")
	cfg.Monitor.Roots = []string{"/每日更新/电视剧/国产剧"}

	pipe, err := integration.NewPipeline(ctx, cfg, nil, store)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	defer pipe.Close()

	alistCli := alist.NewClient(alist.Options{BaseURL: "http://xiaoya.host:5678"})
	imp := alist.NewImporter(alistCli, pipe.Discovery(), pipe.STRMEngine(), nil)
	sched := scheduler.New(store, pipe, imp, 10*time.Minute, true)
	_ = sched.InitTasks(ctx)

	h := NewHandler(cfg, store, pipe, imp, sched)
	adminUser := embyserver.User{ID: "u1", Admin: true}

	call := func(method, target string, body any) *httptest.ResponseRecorder {
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req := httptest.NewRequest(method, target, rdr)
		rec := httptest.NewRecorder()
		if !h.ServeExtension(rec, req, adminUser) {
			t.Fatalf("route not handled: %s %s", method, target)
		}
		return rec
	}

	// 1. GET /api/monitor/roots
	rRoots := call(http.MethodGet, "/api/monitor/roots", nil)
	if rRoots.Code != http.StatusOK {
		t.Fatalf("GET /api/monitor/roots = %d: %s", rRoots.Code, rRoots.Body.String())
	}

	// 2. POST /api/sync/full
	rSync := call(http.MethodPost, "/api/sync/full", map[string]any{})
	if rSync.Code != http.StatusOK {
		t.Fatalf("POST /api/sync/full = %d: %s", rSync.Code, rSync.Body.String())
	}

	// 3. GET /api/resources?only_files=1
	rRes := call(http.MethodGet, "/api/resources?only_files=1", nil)
	var resPayload struct {
		Items []discovery.MediaResource `json:"items"`
		Total int                       `json:"total"`
	}
	_ = json.Unmarshal(rRes.Body.Bytes(), &resPayload)
	if rRes.Code != http.StatusOK || resPayload.Total != 1 {
		t.Fatalf("GET /api/resources unexpected: code=%d payload=%+v", rRes.Code, resPayload)
	}
	resID := resPayload.Items[0].ID

	// 4. DELETE /api/resources/{id} without ?confirm=true MUST return 409 Conflict (`keep` guard)
	rDelConflict := call(http.MethodDelete, "/api/resources/"+resID, nil)
	if rDelConflict.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict without ?confirm=true, got %d", rDelConflict.Code)
	}

	// 5. GET /api/tasks & GET /api/batches & GET /api/system/overview
	for _, ep := range []string{"/api/tasks", "/api/batches", "/api/resources/changes", "/api/system/overview"} {
		rec := call(http.MethodGet, ep, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s failed: %d %s", ep, rec.Code, rec.Body.String())
		}
	}
}

func TestBackgroundSyncPersistsFailureAndInterruptsStaleJobs(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(tmp, "biz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := discovery.NewSQLStore(db, "sqlite")
	if err = store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Xiaoya.SQLitePath = filepath.Join(tmp, "absent.db")
	cfg.STRM.OutputRoot = filepath.Join(tmp, "out")
	cfg.Monitor.Roots = []string{"/不可用"}
	pipe, err := integration.NewPipeline(ctx, cfg, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	if err = store.CreateAdminJob(ctx, "stale", "sync", "scan:*", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(cfg, store, pipe, nil, nil)
	if err = h.Start(ctx); err != nil {
		t.Fatal(err)
	}
	call := func(method, route string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeExtension(w, httptest.NewRequest(method, route, nil), embyserver.User{Admin: true})
		return w
	}
	var old discovery.AdminJob
	if w := call("GET", "/api/jobs/stale"); json.Unmarshal(w.Body.Bytes(), &old) != nil || old.Status != "INTERRUPTED" {
		t.Fatalf("stale job not interrupted: %s", w.Body.String())
	}
	w := call("POST", "/api/sync/full?async=true")
	var j discovery.AdminJob
	if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &j) != nil || j.ID == "" {
		t.Fatalf("submit status=%d body=%s", w.Code, w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w = call("GET", "/api/jobs/"+j.ID)
		if json.Unmarshal(w.Body.Bytes(), &j) != nil {
			t.Fatalf("invalid job response %s", w.Body.String())
		}
		if j.Status != "RUNNING" {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	var result syncResult
	if j.Status != "FAILED" || json.Unmarshal(j.Result, &result) != nil || len(result.Errors) != 1 || len(result.Batches) != 1 || result.Batches[0].Status != discovery.BatchFailed {
		t.Fatalf("background failure hidden: %+v %s", j, j.Result)
	}
}
