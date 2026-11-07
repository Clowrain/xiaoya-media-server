package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/embyserver"
	"xiaoya-media-server/internal/integration"
	"xiaoya-media-server/internal/resource/alist"
	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/resource/xiaoya"
	"xiaoya-media-server/internal/scheduler"
)

type Handler struct {
	cfg      *config.Config
	store    *discovery.SQLStore
	pipeline *integration.Pipeline
	importer *alist.Importer
	sched    *scheduler.Scheduler
	jobs     *jobRunner
}

func NewHandler(cfg *config.Config, store *discovery.SQLStore, pipeline *integration.Pipeline, importer *alist.Importer, sched *scheduler.Scheduler) *Handler {
	return &Handler{
		cfg:      cfg,
		store:    store,
		pipeline: pipeline,
		importer: importer,
		sched:    sched,
		jobs:     &jobRunner{ctx: context.Background(), db: store, active: map[string]bool{}},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func normalizeAPIPath(raw string) string {
	p := strings.TrimRight(raw, "/")
	if strings.HasPrefix(strings.ToLower(p), "/emby/") {
		p = p[5:]
	}
	if p == "" {
		return "/"
	}
	return p
}

// ServeExtension implements embyserver.ExtensionHandler for all `/api/*` unified resource management routes.
func (h *Handler) ServeExtension(w http.ResponseWriter, r *http.Request, _ embyserver.User) bool {
	p := normalizeAPIPath(r.URL.Path)
	if !strings.HasPrefix(p, "/api/") {
		return false
	}
	ctx := r.Context()

	switch {
	case p == "/api/jobs":
		if r.Method != http.MethodGet {
			writeErr(w, 405, "GET required")
			return true
		}
		items, err := h.store.ListAdminJobs(ctx)
		if err != nil {
			writeErr(w, 500, err.Error())
			return true
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return true
	case p == "/api/alist/tracking-review":
		if r.Method != http.MethodGet {
			writeErr(w, 405, "GET required")
			return true
		}
		items, err := h.store.TrackedAListRoots(ctx, true)
		if err != nil {
			writeErr(w, 500, err.Error())
			return true
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return true
	case strings.HasPrefix(p, "/api/jobs/"):
		h.serveJob(w, r, strings.TrimPrefix(p, "/api/jobs/"))
		return true
	case p == "/api/system/overview" || p == "/api/system/health":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET required")
			return true
		}
		h.handleSystemOverview(ctx, w)
		return true

	case p == "/api/monitor/roots":
		switch r.Method {
		case http.MethodGet:
			roots, err := h.store.ListMonitorRoots(ctx)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return true
			}
			if roots == nil {
				roots = []discovery.MonitorRoot{}
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": roots, "total": len(roots)})
		case http.MethodPost:
			var req struct {
				RootPath    string `json:"root_path"`
				Enabled     *bool  `json:"enabled"`
				IntervalSec int64  `json:"interval_sec"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
				writeErr(w, http.StatusBadRequest, "无效 JSON 请求")
				return true
			}
			norm, _, err := xiaoya.NormalizeMonitorPath(req.RootPath)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err.Error())
				return true
			}
			en := true
			if req.Enabled != nil {
				en = *req.Enabled
			}
			mr, err := h.store.UpsertMonitorRoot(ctx, norm, en, req.IntervalSec)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return true
			}
			writeJSON(w, http.StatusCreated, mr)
		default:
			writeErr(w, http.StatusMethodNotAllowed, "GET or POST required")
		}
		return true

	case strings.HasPrefix(p, "/api/monitor/roots/"):
		id := strings.TrimPrefix(p, "/api/monitor/roots/")
		if id == "" || strings.Contains(id, "/") {
			writeErr(w, http.StatusBadRequest, "invalid root id")
			return true
		}
		switch r.Method {
		case http.MethodPut:
			cur, err := h.store.GetMonitorRoot(ctx, id)
			if err == sql.ErrNoRows {
				writeErr(w, http.StatusNotFound, "监控目录不存在")
				return true
			}
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return true
			}
			var req struct {
				Enabled     *bool  `json:"enabled"`
				IntervalSec *int64 `json:"interval_sec"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
				writeErr(w, http.StatusBadRequest, "无效 JSON 请求")
				return true
			}
			en := cur.Enabled
			if req.Enabled != nil {
				en = *req.Enabled
			}
			iv := cur.IntervalSec
			if req.IntervalSec != nil && *req.IntervalSec > 0 {
				iv = *req.IntervalSec
			}
			updated, err := h.store.UpsertMonitorRoot(ctx, cur.RootPath, en, iv)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return true
			}
			writeJSON(w, http.StatusOK, updated)
		case http.MethodDelete:
			if err := h.store.DeleteMonitorRoot(ctx, id); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return true
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			writeErr(w, http.StatusMethodNotAllowed, "PUT or DELETE required")
		}
		return true

	case p == "/api/resources":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET required")
			return true
		}
		qv := r.URL.Query()
		limit, _ := strconv.Atoi(qv.Get("limit"))
		offset, _ := strconv.Atoi(qv.Get("offset"))
		since, _ := strconv.ParseInt(qv.Get("since"), 10, 64)
		items, total, err := h.store.ListResources(ctx, discovery.ResourceFilter{
			RootID:      qv.Get("root_id"),
			Status:      qv.Get("status"),
			OriginType:  qv.Get("origin_type"),
			SearchQuery: qv.Get("q"),
			OnlyFiles:   qv.Get("only_files") == "1" || strings.EqualFold(qv.Get("only_files"), "true"),
			SinceUnix:   since,
			Limit:       limit,
			Offset:      offset,
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return true
		}
		if items == nil {
			items = []discovery.MediaResource{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
		return true

	case p == "/api/resources/changes":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET required")
			return true
		}
		qv := r.URL.Query()
		limit, _ := strconv.Atoi(qv.Get("limit"))
		since, _ := strconv.ParseInt(qv.Get("since"), 10, 64)
		changes, err := h.store.ListChanges(ctx, qv.Get("batch_id"), qv.Get("change_type"), since, limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return true
		}
		if changes == nil {
			changes = []discovery.ResourceChange{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": changes, "total": len(changes)})
		return true

	case p == "/api/resources/import":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST required")
			return true
		}
		if h.importer == nil {
			writeErr(w, http.StatusServiceUnavailable, "AList 入库器未配置")
			return true
		}
		var req alist.ImportRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "无效 JSON 请求")
			return true
		}
		if strings.TrimSpace(req.Path) == "" {
			writeErr(w, http.StatusBadRequest, "入库路径 path 不能为空")
			return true
		}
		scope := "import:" + alist.NormalizePath(req.Path)
		if h.importer.Busy(req.Path) {
			writeErr(w, 409, alist.ErrImportInProgress.Error())
			return true
		}
		run := func(ctx context.Context) (string, any, error) {
			sum, err := h.importer.Import(ctx, req)
			if err != nil || sum.FailedFiles > 0 {
				embyserver.RecordGlobalLog("WARN", "scan", "AList 入库有失败项", fmt.Sprintf("%s · %d 个文件失败", sum.SourcePath, sum.FailedFiles), "", "")
			}
			if err != nil || sum.FailedFiles > 0 {
				if sum.ImportedFiles > 0 {
					return "PARTIAL", sum, err
				}
				return "FAILED", sum, err
			}
			if sum.ImportedFiles == 0 && sum.ImportedDirs == 0 {
				return "FAILED", sum, errors.New("未找到可入库的资源")
			}
			return "SUCCESS", sum, nil
		}
		if r.URL.Query().Get("async") == "true" {
			h.jobs.submit(w, r, "import", scope, run)
			return true
		}
		release, ok := h.jobs.acquire(scope)
		if !ok {
			writeErr(w, 409, "同一范围已有任务在运行")
			return true
		}
		defer release()
		_, result, err := run(ctx)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, result)
		return true

	case strings.HasPrefix(p, "/api/resources/"):
		tail := strings.TrimPrefix(p, "/api/resources/")
		segs := strings.Split(tail, "/")
		resID := segs[0]
		if len(segs) == 1 {
			switch r.Method {
			case http.MethodGet:
				res, err := h.store.GetResource(ctx, resID)
				if err == sql.ErrNoRows {
					writeErr(w, http.StatusNotFound, "资源不存在")
					return true
				}
				if err != nil {
					writeErr(w, http.StatusInternalServerError, err.Error())
					return true
				}
				atts, err := h.store.ListAttachments(ctx, resID)
				if err != nil {
					writeErr(w, 500, err.Error())
					return true
				}
				preview, err := h.store.PreviewCleanup(ctx, resID)
				if err != nil {
					writeErr(w, 500, err.Error())
					return true
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"resource":        res,
					"attachments":     atts,
					"cleanup_preview": preview,
				})
			case http.MethodDelete:
				h.unmanage(w, r, resID)
			default:
				writeErr(w, http.StatusMethodNotAllowed, "GET or DELETE required")
			}
			return true
		}
		if len(segs) == 2 && r.Method == http.MethodPost {
			action := segs[1]
			switch action {
			case "retry", "reprocess":
				if h.pipeline == nil {
					writeErr(w, http.StatusServiceUnavailable, "流水线未初始化")
					return true
				}
				updated, err := h.pipeline.Discovery().ReprocessResource(ctx, resID)
				h.pipeline.FlushScopedLibraryScans()
				if err != nil {
					writeErr(w, http.StatusBadGateway, err.Error())
					return true
				}
				writeJSON(w, http.StatusOK, updated)
				return true
			case "unmanage":
				h.unmanage(w, r, resID)
				return true
			case "restore-management":
				if h.pipeline == nil {
					writeErr(w, 503, "流水线未初始化")
					return true
				}
				if err := h.pipeline.Discovery().RestoreManagement(ctx, resID); err != nil {
					code := 500
					if errors.Is(err, sql.ErrNoRows) {
						code = 404
					}
					writeErr(w, code, err.Error())
					return true
				}
				writeJSON(w, 200, map[string]any{"ok": true, "message": "已恢复管理"})
				return true
			case "track":
				if err := h.store.SetAListTracking(ctx, resID, true); err != nil {
					writeErr(w, 400, err.Error())
					return true
				}
				writeJSON(w, 200, map[string]any{"ok": true})
				return true
			}
		}
		writeErr(w, http.StatusNotFound, "not found")
		return true

	case p == "/api/batches":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET required")
			return true
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		batches, err := h.store.ListBatches(ctx, r.URL.Query().Get("root_id"), limit)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return true
		}
		if batches == nil {
			batches = []discovery.SyncBatch{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": batches, "total": len(batches)})
		return true

	case p == "/api/sync/full" || p == "/api/sync/incremental":
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST required")
			return true
		}
		if h.pipeline == nil {
			writeErr(w, http.StatusServiceUnavailable, "同步流水线未就绪")
			return true
		}
		var req struct {
			RootID string `json:"root_id"`
		}
		if r.Body != nil {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil && err != io.EOF {
				writeErr(w, 400, "无效 JSON 请求")
				return true
			}
		}
		rootID := strings.TrimSpace(req.RootID)
		scope := "scan:*"
		full := p == "/api/sync/full"
		roots, err := h.syncRoots(ctx, rootID)
		if err != nil {
			code := 500
			if errors.Is(err, sql.ErrNoRows) {
				code = 404
			}
			writeErr(w, code, err.Error())
			return true
		}
		if rootID != "" {
			scope = "scan:" + roots[0].RootPath
		}
		run := func(ctx context.Context) (string, any, error) {
			status, res := h.runSync(ctx, roots, full)
			return status, res, nil
		}
		for _, root := range roots {
			if h.pipeline.Discovery().RootPathBusy(root.RootPath) {
				writeErr(w, 409, "同一范围已有任务在运行")
				return true
			}
		}
		if r.URL.Query().Get("async") == "true" {
			h.jobs.submit(w, r, "sync", scope, run)
		} else {
			release, ok := h.jobs.acquire(scope)
			if !ok {
				writeErr(w, 409, "同一范围已有任务在运行")
				return true
			}
			defer release()
			_, res := h.runSync(ctx, roots, full)
			writeJSON(w, 200, res)
		}
		return true

	case p == "/api/alist/browse":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET required")
			return true
		}
		if h.importer == nil {
			writeErr(w, http.StatusServiceUnavailable, "AList 客户端未就绪")
			return true
		}
		qv := r.URL.Query()
		dirPath := qv.Get("path")
		if dirPath == "" {
			dirPath = "/"
		}
		page, _ := strconv.Atoi(qv.Get("page"))
		perPage, _ := strconv.Atoi(qv.Get("per_page"))
		refresh := qv.Get("refresh") == "1" || strings.EqualFold(qv.Get("refresh"), "true")
		entries, total, err := h.importer.Client().List(ctx, dirPath, page, perPage, refresh)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": dirPath, "items": entries, "total": total})
		return true

	case p == "/api/alist/search":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET required")
			return true
		}
		if h.importer == nil {
			writeErr(w, http.StatusServiceUnavailable, "AList 客户端未就绪")
			return true
		}
		qv := r.URL.Query()
		kw := strings.TrimSpace(qv.Get("keywords"))
		if kw == "" {
			kw = strings.TrimSpace(qv.Get("keyword"))
		}
		if kw == "" {
			kw = strings.TrimSpace(qv.Get("q"))
		}
		if kw == "" {
			writeErr(w, http.StatusBadRequest, "请输入搜索关键词 keywords")
			return true
		}
		parent := strings.TrimSpace(qv.Get("parent"))
		if parent == "" {
			parent = strings.TrimSpace(qv.Get("path"))
		}
		if parent == "" {
			parent = "/"
		}
		scope, _ := strconv.Atoi(qv.Get("scope"))
		page, _ := strconv.Atoi(qv.Get("page"))
		perPage, _ := strconv.Atoi(qv.Get("per_page"))
		items, total, err := h.importer.Client().Search(ctx, parent, kw, scope, page, perPage)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"path": parent, "items": items, "total": total})
		return true

	case p == "/api/tasks":
		if r.Method != http.MethodGet {
			writeErr(w, http.StatusMethodNotAllowed, "GET required")
			return true
		}
		tasks, err := h.store.ListScheduledTasks(ctx)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return true
		}
		if tasks == nil {
			tasks = []discovery.ScheduledTaskState{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": tasks, "total": len(tasks)})
		return true

	case strings.HasPrefix(p, "/api/tasks/") && strings.HasSuffix(p, "/run"):
		if r.Method != http.MethodPost {
			writeErr(w, http.StatusMethodNotAllowed, "POST required")
			return true
		}
		if h.sched == nil {
			writeErr(w, http.StatusServiceUnavailable, "调度器未启用")
			return true
		}
		key := strings.TrimSuffix(strings.TrimPrefix(p, "/api/tasks/"), "/run")
		if !h.sched.HasTask(key) {
			writeErr(w, 404, "任务不存在")
			return true
		}
		h.jobs.submit(w, r, "task", "task:"+key, func(ctx context.Context) (string, any, error) {
			if err := h.sched.RunTaskNow(ctx, key); err != nil {
				return "FAILED", nil, err
			}
			return "SUCCESS", map[string]string{"task_key": key}, nil
		})
		return true
	}

	return false
}

func (h *Handler) syncRoots(ctx context.Context, id string) ([]discovery.MonitorRoot, error) {
	if id != "" {
		r, err := h.store.GetMonitorRoot(ctx, id)
		if err != nil {
			return nil, err
		}
		return []discovery.MonitorRoot{r}, nil
	}
	all, err := h.store.ListMonitorRoots(ctx)
	if err != nil {
		return nil, err
	}
	var enabled []discovery.MonitorRoot
	for _, r := range all {
		if r.Enabled {
			enabled = append(enabled, r)
		}
	}
	return enabled, nil
}

type syncResult struct {
	Batches []discovery.SyncBatch `json:"batches"`
	Errors  []string              `json:"errors"`
	Outcome string                `json:"outcome"`
}

func (h *Handler) runSync(ctx context.Context, roots []discovery.MonitorRoot, full bool) (string, syncResult) {
	res := syncResult{Batches: []discovery.SyncBatch{}, Errors: []string{}}
	ok := 0
	for _, root := range roots {
		var batch discovery.SyncBatch
		var err error
		if full {
			batch, err = h.pipeline.RunFullSyncRoot(ctx, root.ID)
		} else {
			batch, err = h.pipeline.RunIncrementalSyncRoot(ctx, root.ID)
		}
		if batch.ID != "" {
			res.Batches = append(res.Batches, batch)
		}
		if err != nil {
			res.Errors = append(res.Errors, root.RootPath+": "+err.Error())
			continue
		}
		ok++
		if batch.FailedCount > 0 {
			detail := fmt.Sprintf("%s：%d 个资源处理失败", root.RootPath, batch.FailedCount)
			if batch.ErrorMessage != "" {
				detail += "；" + batch.ErrorMessage
			}
			res.Errors = append(res.Errors, detail)
		}
	}
	if len(roots) == 0 {
		res.Errors = append(res.Errors, "没有已启用的监控目录")
	}
	switch {
	case ok == 0:
		res.Outcome = "FAILED"
	case len(res.Errors) > 0:
		res.Outcome = "PARTIAL"
	default:
		res.Outcome = "SUCCESS"
	}
	return res.Outcome, res
}

func (h *Handler) handleSystemOverview(ctx context.Context, w http.ResponseWriter) {
	sqliteOK := false
	sqliteErr := ""
	if h.pipeline != nil {
		if err := h.pipeline.EnsureSQLiteReader(ctx); err == nil {
			sqliteOK = true
		} else {
			sqliteErr = err.Error()
		}
	}
	roots, _ := h.store.ListMonitorRoots(ctx)
	_, activeCount, _ := h.store.ListResources(ctx, discovery.ResourceFilter{Status: discovery.StatusActive, OnlyFiles: true, Limit: 1})
	_, missingCount, _ := h.store.ListResources(ctx, discovery.ResourceFilter{Status: discovery.StatusMissing, OnlyFiles: true, Limit: 1})
	_, failedCount, _ := h.store.ListResources(ctx, discovery.ResourceFilter{Status: discovery.StatusFailed, OnlyFiles: true, Limit: 1})

	outRoot := ""
	alistBase := ""
	sqlitePath := ""
	if h.cfg != nil {
		outRoot = h.cfg.STRM.OutputRoot
		alistBase = h.cfg.AList.BaseURL
		sqlitePath = h.cfg.Xiaoya.SQLitePath
	}

	var sqliteSize int64
	if sqlitePath != "" {
		if fi, err := os.Stat(sqlitePath); err == nil {
			sqliteSize = fi.Size()
		}
	}

	strmCount, countErr := h.store.CountSTRMPaths(ctx)
	if countErr != nil {
		writeErr(w, 500, countErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "ok",
		"service":             "xiaoya-media-server",
		"nfo_policy":          "prefer",
		"deletion_policy":     "keep",
		"sqlite_path":         sqlitePath,
		"sqlite_read_only":    true,
		"sqlite_connected":    sqliteOK,
		"sqlite_error":        sqliteErr,
		"strm_output_root":    outRoot,
		"alist_base_url":      alistBase,
		"alist_host":          alistBase,
		"strm_count":          strmCount,
		"cache_db_path":       sqlitePath,
		"cache_db_size_bytes": sqliteSize,
		"monitor_roots":       len(roots),
		"resources": map[string]int{
			"active_files":  activeCount,
			"missing_files": missingCount,
			"failed_files":  failedCount,
		},
	})
}

func (h *Handler) unmanage(w http.ResponseWriter, r *http.Request, id string) {
	flag := r.URL.Query().Get("confirm")
	confirm := flag == "true" || flag == "1"
	var preview discovery.CleanupImpactPreview
	var err error
	if h.pipeline != nil {
		preview, err = h.pipeline.Discovery().SetUnmanaged(r.Context(), id, confirm)
	} else {
		preview, err = h.store.ConfirmDeleteResource(r.Context(), id, confirm)
	}
	switch {
	case errors.Is(err, discovery.ErrConfirmationRequired):
		writeJSON(w, 409, map[string]any{"error": "确认影响范围后附带 ?confirm=true 取消管理", "preview": preview})
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, 404, "资源不存在")
	case err != nil:
		writeErr(w, 500, err.Error())
	default:
		writeJSON(w, 200, map[string]any{"ok": true, "unmanaged": true, "preview": preview, "message": "已取消管理"})
	}
}
