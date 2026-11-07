package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"xiaoya-media-server/internal/resource/discovery"
)

type jobRunner struct {
	ctx    context.Context
	db     *discovery.SQLStore
	mu     sync.Mutex
	active map[string]bool
}

func (h *Handler) Start(ctx context.Context) error {
	if err := h.store.InterruptRunningWork(ctx); err != nil {
		return err
	}
	h.jobs.ctx = ctx
	return nil
}

func (j *jobRunner) acquire(scope string) (func(), bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for active := range j.active {
		if active == scope ||
			(strings.HasPrefix(active, "scan:") && strings.HasPrefix(scope, "scan:") && (active == "scan:*" || scope == "scan:*" || strings.HasPrefix(active, scope+"/") || strings.HasPrefix(scope, active+"/"))) ||
			(strings.HasPrefix(active, "import:") && strings.HasPrefix(scope, "import:") && (strings.HasPrefix(active, scope+"/") || strings.HasPrefix(scope, active+"/"))) {
			return nil, false
		}
	}
	j.active[scope] = true
	return func() { j.mu.Lock(); delete(j.active, scope); j.mu.Unlock() }, true
}

func (j *jobRunner) submit(w http.ResponseWriter, r *http.Request, kind, scope string, work func(context.Context) (string, any, error)) {
	release, ok := j.acquire(scope)
	if !ok {
		writeErr(w, http.StatusConflict, "同一范围已有任务在运行")
		return
	}
	job := discovery.AdminJob{ID: discovery.NewID(), Kind: kind, Scope: scope, Status: "RUNNING", CreatedAt: time.Now().Unix()}
	if err := j.db.CreateAdminJob(r.Context(), job.ID, kind, scope, job.CreatedAt); err != nil {
		release()
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	go func() {
		defer release()
		status, value, err := work(j.ctx)
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		if j.ctx.Err() != nil {
			status = "INTERRUPTED"
			detail = "服务已停止，可重新提交"
		}
		raw, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			status = "FAILED"
			detail = encodeErr.Error()
			raw = []byte("null")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := j.db.FinishAdminJob(ctx, job.ID, status, string(raw), detail); err != nil {
			log.Printf("persist admin job %s: %v", job.ID, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, job)
}

func (h *Handler) serveJob(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	job, err := h.store.GetAdminJob(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, job)
}
