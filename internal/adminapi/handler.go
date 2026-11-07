package adminapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

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
}

func NewHandler(cfg *config.Config, store *discovery.SQLStore, pipeline *integration.Pipeline, importer *alist.Importer, sched *scheduler.Scheduler) *Handler {
	return &Handler{
		cfg:      cfg,
		store:    store,
		pipeline: pipeline,
		importer: importer,
		sched:    sched,
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
		embyserver.RecordGlobalLog("INFO", "scan", "开始 AList 入库", fmt.Sprintf("请求路径: %s · 追更模式: %v", req.Path, req.TrackUpdates), "", "")
		sum, err := h.importer.Import(ctx, req)
		if err != nil {
			embyserver.RecordGlobalLog("ERROR", "scan", "AList 入库失败", fmt.Sprintf("路径: %s · 错误: %v", req.Path, err), "", err.Error())
			writeErr(w, http.StatusBadGateway, err.Error())
			return true
		}
		embyserver.RecordGlobalLog("INFO", "scan", "AList 入库完成", fmt.Sprintf("路径: %s · 成功生成 STRM: %d 个 · 目录: %d 个 · 失败: %d 个", sum.SourcePath, sum.ImportedFiles, sum.ImportedDirs, sum.FailedFiles), "", "")
		writeJSON(w, http.StatusOK, sum)
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
				atts, _ := h.store.ListAttachments(ctx, resID)
				preview, _ := h.store.PreviewCleanup(ctx, resID)
				writeJSON(w, http.StatusOK, map[string]any{
					"resource":        res,
					"attachments":     atts,
					"cleanup_preview": preview,
				})
			case http.MethodDelete:
				confirmed := r.URL.Query().Get("confirm") == "true" || r.URL.Query().Get("confirm") == "1"
				preview, err := h.store.ConfirmDeleteResource(ctx, resID, confirmed)
				if errors.Is(err, discovery.ErrConfirmationRequired) {
					writeJSON(w, http.StatusConflict, map[string]any{
						"error":   "删除受 keep 策略保护，请确认影响范围后附带 ?confirm=true 重试",
						"preview": preview,
					})
					return true
				}
				if err == sql.ErrNoRows {
					writeErr(w, http.StatusNotFound, "资源不存在")
					return true
				}
				if err != nil {
					writeErr(w, http.StatusInternalServerError, err.Error())
					return true
				}
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": preview})
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
				res, err := h.store.GetResource(ctx, resID)
				if err != nil {
					writeErr(w, http.StatusNotFound, "资源不存在")
					return true
				}
				_ = h.store.UpsertManagedResource(ctx, h.store.DB(), discovery.ManagedResource{
					ResourceID:     res.ID,
					ManageMode:     discovery.ManageManualFile,
					Enabled:        false,
					DeletionPolicy: "keep",
				})
				writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
			_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req)
		}
		full := p == "/api/sync/full"
		h.handleTriggerSync(ctx, w, strings.TrimSpace(req.RootID), full)
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
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			_ = h.sched.RunTaskNow(bgCtx, key)
		}()
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "task_key": key})
		return true
	}

	return false
}

func (h *Handler) handleTriggerSync(ctx context.Context, w http.ResponseWriter, rootID string, full bool) {
	var targetRoots []discovery.MonitorRoot
	if rootID != "" {
		r, err := h.store.GetMonitorRoot(ctx, rootID)
		if err != nil {
			writeErr(w, http.StatusNotFound, "监控目录不存在")
			return
		}
		targetRoots = []discovery.MonitorRoot{r}
	} else {
		all, err := h.store.ListMonitorRoots(ctx)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, r := range all {
			if r.Enabled {
				targetRoots = append(targetRoots, r)
			}
		}
	}

	var batches []discovery.SyncBatch
	var errs []string
	for _, r := range targetRoots {
		var b discovery.SyncBatch
		var err error
		if full {
			b, err = h.pipeline.RunFullSyncRoot(ctx, r.ID)
		} else {
			b, err = h.pipeline.RunIncrementalSyncRoot(ctx, r.ID)
		}
		batches = append(batches, b)
		if err != nil {
			errs = append(errs, r.RootPath+": "+err.Error())
		}
	}
	status := http.StatusOK
	if len(errs) > 0 && len(batches) == 0 {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{
		"batches": batches,
		"errors":  errs,
	})
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
		"strm_count":          activeCount,
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
