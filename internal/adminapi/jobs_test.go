package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"xiaoya-media-server/internal/resource/discovery"
)

func TestJobRunnerReleasesAdmissionWhenJobRecordCreationFails(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := discovery.NewSQLStore(db, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	runner := &jobRunner{ctx: ctx, db: store, active: map[string]bool{}, slots: make(chan struct{}, 1)}
	w := httptest.NewRecorder()
	runner.submit(w, httptest.NewRequest(http.MethodPost, "/", nil), "sync", "scan:/one", func(context.Context) (string, any, error) {
		return "SUCCESS", nil, nil
	})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("submit status=%d body=%s", w.Code, w.Body.String())
	}
	if release, status := runner.acquire("scan:/one"); status != http.StatusOK {
		t.Fatalf("failed create leaked admission slot: status=%d", status)
	} else {
		release()
	}
}

func TestJobRunnerCapsConcurrentWork(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	store := discovery.NewSQLStore(db, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	runner := &jobRunner{
		ctx:    ctx,
		db:     store,
		active: map[string]bool{},
		slots:  make(chan struct{}, 1),
	}

	started := make(chan struct{})
	finish := make(chan struct{})
	first := httptest.NewRecorder()
	runner.submit(first, httptest.NewRequest(http.MethodPost, "/", nil), "sync", "scan:/one", func(context.Context) (string, any, error) {
		close(started)
		<-finish
		return "SUCCESS", map[string]bool{"ok": true}, nil
	})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submit status=%d body=%s", first.Code, first.Body.String())
	}
	<-started

	conflict := httptest.NewRecorder()
	runner.submit(conflict, httptest.NewRequest(http.MethodPost, "/", nil), "sync", "scan:/one", func(context.Context) (string, any, error) {
		return "SUCCESS", nil, nil
	})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("same-scope submit status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	overloaded := httptest.NewRecorder()
	runner.submit(overloaded, httptest.NewRequest(http.MethodPost, "/", nil), "sync", "scan:/two", func(context.Context) (string, any, error) {
		return "SUCCESS", nil, nil
	})
	if overloaded.Code != http.StatusServiceUnavailable {
		t.Fatalf("over-capacity submit status=%d body=%s", overloaded.Code, overloaded.Body.String())
	}
	var errorBody map[string]string
	if err := json.Unmarshal(overloaded.Body.Bytes(), &errorBody); err != nil || errorBody["error"] == "" {
		t.Fatalf("invalid overload response: %s", overloaded.Body.String())
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM admin_jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("overloaded submission created a job row: count=%d", count)
	}

	close(finish)
	deadline := time.Now().Add(3 * time.Second)
	for len(runner.slots) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(runner.slots) != 0 {
		t.Fatal("job slot was not released after completion")
	}

	next := httptest.NewRecorder()
	runner.submit(next, httptest.NewRequest(http.MethodPost, "/", nil), "sync", "scan:/two", func(context.Context) (string, any, error) {
		return "SUCCESS", nil, nil
	})
	if next.Code != http.StatusAccepted {
		t.Fatalf("submit after release status=%d body=%s", next.Code, next.Body.String())
	}
}
