package embyserver

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LogEntry represents a single full-chain system/Emby lifecycle log entry.
type LogEntry struct {
	ID         string    `json:"id"`
	Time       time.Time `json:"time"`
	Level      string    `json:"level"`       // "INFO", "WARN", "ERROR", "302"
	Category   string    `json:"category"`    // "playback", "redirect", "http", "scan", "sync", "tmdb", "error"
	Source     string    `json:"source,omitempty"` // Alias for Category (frontend compat)
	Title      string    `json:"title"`       // e.g. "播放重定向 302", "客户端请求", "小雅增量同步"
	Method     string    `json:"method"`      // GET, POST, PUT, DELETE...
	Path       string    `json:"path"`        // /emby/Videos/...
	Status     int       `json:"status"`      // HTTP Status code
	DurationMs float64   `json:"duration_ms"` // Execution latency
	Client     string    `json:"client"`      // User-Agent (SenPlayer, Infuse, VidHub...)
	IP         string    `json:"ip"`          // Client IP
	Target     string    `json:"target,omitempty"` // 302 Location or upstream target
	Message    string    `json:"message"`     // Human-readable summary
	Msg        string    `json:"msg,omitempty"`    // Alias for Message (frontend compat)
	Detail     string    `json:"detail,omitempty"`  // Extra metadata or upstream response
	Error      string    `json:"error,omitempty"`   // Error description if any
}

type systemLogRing struct {
	mu      sync.RWMutex
	entries []*LogEntry
	maxSize int
}

var sysLogger = &systemLogRing{
	maxSize: 1500,
	entries: make([]*LogEntry, 0, 1500),
}

func (r *systemLogRing) add(e *LogEntry) {
	if e == nil {
		return
	}
	if e.Source == "" {
		e.Source = e.Category
	}
	if e.Msg == "" {
		if e.Message != "" {
			e.Msg = e.Message
		} else if e.Title != "" {
			e.Msg = e.Title
		} else if e.Path != "" {
			e.Msg = fmt.Sprintf("%s %s (%d)", e.Method, e.Path, e.Status)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) >= r.maxSize {
		r.entries = r.entries[1:]
	}
	r.entries = append(r.entries, e)
}

func (r *systemLogRing) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = r.entries[:0]
}

func (r *systemLogRing) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.entries)
}

func (r *systemLogRing) snapshot(category, level, keyword, order string, limit int) []*LogEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	cat := strings.ToLower(strings.TrimSpace(category))
	lvl := strings.ToUpper(strings.TrimSpace(level))
	kw := strings.ToLower(strings.TrimSpace(keyword))

	result := make([]*LogEntry, 0, limit)
	// Iterate in reverse to collect the latest N matching logs
	for i := len(r.entries) - 1; i >= 0 && len(result) < limit; i-- {
		e := r.entries[i]
		if lvl != "" && lvl != "ALL" {
			if lvl == "ERROR" {
				if e.Level != "ERROR" && e.Status < 400 && e.Error == "" {
					continue
				}
			} else if lvl == "INFO" {
				if e.Level != "INFO" && e.Level != "302" && e.Level != "" && e.Status >= 400 {
					continue
				}
			} else if !strings.EqualFold(e.Level, lvl) {
				continue
			}
		}
		if cat != "" && cat != "all" {
			if cat == "error" {
				if e.Level != "ERROR" && e.Status < 400 && e.Error == "" {
					continue
				}
			} else if cat == "playback" || cat == "redirect" {
				if e.Category != "playback" && e.Category != "redirect" && e.Level != "302" {
					continue
				}
			} else if cat == "emby" {
				if e.Category != "http" && e.Category != "playback" && e.Category != "redirect" {
					continue
				}
			} else if cat == "scanner" || cat == "scan" {
				if e.Category != "scan" {
					continue
				}
			} else if !strings.EqualFold(e.Category, cat) && !strings.EqualFold(e.Source, cat) {
				continue
			}
		}
		if kw != "" {
			combined := strings.ToLower(e.Path + " " + e.Message + " " + e.Client + " " + e.Target + " " + e.Error + " " + e.Title + " " + e.IP + " " + e.Method + " " + strconv.Itoa(e.Status))
			if !strings.Contains(combined, kw) {
				continue
			}
		}
		result = append(result, e)
	}

	// Standard terminal flow: chronological order (oldest at top, newest at bottom)
	// Unless client explicitly requests order=desc (newest on top)
	if !strings.EqualFold(order, "desc") {
		for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
			result[i], result[j] = result[j], result[i]
		}
	}

	return result
}

// RecordGlobalLog logs a system event to both stdout and in-memory ring buffer.
func RecordGlobalLog(level, category, title, message, detail, errStr string) {
	now := time.Now()
	entry := &LogEntry{
		ID:       fmt.Sprintf("%d-%s", now.UnixNano(), id()[:6]),
		Time:     now,
		Level:    strings.ToUpper(level),
		Category: strings.ToLower(category),
		Title:    title,
		Message:  message,
		Detail:   detail,
		Error:    errStr,
	}
	sysLogger.add(entry)
	if errStr != "" {
		log.Printf("[%s] [%s] %s: %s (err: %s)", entry.Level, entry.Category, title, message, errStr)
	} else {
		log.Printf("[%s] [%s] %s: %s", entry.Level, entry.Category, title, message)
	}
}

// fullChainResponseWriter intercepts status, headers and body for full audit.
type fullChainResponseWriter struct {
	http.ResponseWriter
	status    int
	wrote     bool
	location  string
	bodyBuf   strings.Builder
	writeErr  error
}

func (w *fullChainResponseWriter) WriteHeader(status int) {
	if !w.wrote {
		w.status = status
		w.wrote = true
		if loc := w.Header().Get("Location"); loc != "" {
			w.location = loc
		}
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *fullChainResponseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if w.status >= 400 && w.bodyBuf.Len() < 2048 {
		remain := 2048 - w.bodyBuf.Len()
		if remain > len(b) {
			remain = len(b)
		}
		w.bodyBuf.Write(b[:remain])
	}
	n, err := w.ResponseWriter.Write(b)
	if err != nil && w.writeErr == nil {
		w.writeErr = err
	}
	return n, err
}

func (w *fullChainResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// shouldSkipLog skips noise like favicon, internal poll, strings translations.
func shouldSkipLog(p string) bool {
	l := strings.ToLower(p)
	if l == "/admin/logs" || strings.HasPrefix(l, "/admin/logs?") {
		return true
	}
	if strings.HasPrefix(l, "/web/strings/") || l == "/favicon.ico" || l == "/web/manifest.json" {
		return true
	}
	return false
}

// clientIP extracts the most relevant client IP from RemoteAddr or proxy headers.
func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		candidate := strings.TrimSpace(parts[0])
		if net.ParseIP(candidate) != nil {
			return candidate
		}
	}
	if realIP := r.Header.Get("X-Real-IP"); realIP != "" {
		if net.ParseIP(realIP) != nil {
			return realIP
		}
	}
	return ip
}

// handleUnifiedLogs serves GET /admin/logs with full-chain search and category filters.
func (a *App) handleUnifiedLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		sysLogger.clear()
		a.activity.mu.Lock()
		for _, v := range a.activity.entries {
			v.Hidden = true
		}
		a.activity.mu.Unlock()
		respond(w, M{"ok": true, "message": "全量运行日志已清空"})
		return
	}
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "GET or DELETE required")
		return
	}

	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if category == "" {
		category = strings.TrimSpace(r.URL.Query().Get("source"))
	}
	if category == "" {
		category = "all"
	}
	level := strings.TrimSpace(r.URL.Query().Get("level"))
	keyword := strings.TrimSpace(r.URL.Query().Get("q"))
	order := strings.TrimSpace(r.URL.Query().Get("order"))
	if order == "" {
		order = "asc" // default chronological order
	}
	limit := 300
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	items := sysLogger.snapshot(category, level, keyword, order, limit)

	// Collect online sessions for header stats
	onlineCount := 0
	a.activity.mu.Lock()
	for _, v := range a.activity.entries {
		if !v.Hidden && v.Category == "playback" && v.State == "requested" && time.Since(v.Updated) < 90*time.Second {
			onlineCount++
		}
	}
	a.activity.mu.Unlock()

	// Compatibility with legacy frontend activity format
	legacyEntries := make([]activityEntry, 0, len(items))
	for _, it := range items {
		state := "complete"
		if it.Level == "ERROR" || it.Status >= 400 {
			state = "error"
		} else if it.Level == "302" {
			state = "running"
		}
		legacyEntries = append(legacyEntries, activityEntry{
			ID:       it.ID,
			Category: it.Category,
			Name:     fmt.Sprintf("[%s] %s", it.Method, it.Path),
			State:    state,
			Current:  it.Message,
			Error:    it.Error,
			IP:       it.IP,
			Client:   it.Client,
			Started:  it.Time,
			Updated:  it.Time,
		})
	}

	w.Header().Set("Cache-Control", "no-store")
	respond(w, M{
		"logs":            items,
		"Items":           items,
		"Entries":         legacyEntries,
		"Total":           sysLogger.count(),
		"OnlinePlayback":  onlineCount,
		"ProbeRunning":    0,
		"ProbeWaiting":    0,
		"Retention":       "内存保留最近1500条全链路日志，支持分类与关键词即时检索",
	})
}

// detectClientInfo extracts human-readable client name from Emby authorization headers, params or User-Agent.
func detectClientInfo(r *http.Request) string {
	if r == nil {
		return ""
	}
	// 1. Check X-Emby-Authorization header (e.g. MediaBrowser Client="SenPlayer", Device="iPhone", ...)
	auth := r.Header.Get("X-Emby-Authorization")
	if auth == "" {
		auth = r.Header.Get("Authorization")
	}
	if auth != "" {
		if c := extractAuthClient(auth); c != "" {
			return c
		}
	}

	// 2. Check X-Emby-Client header or query parameter
	if c := strings.TrimSpace(r.Header.Get("X-Emby-Client")); c != "" {
		return c
	}
	if c := strings.TrimSpace(r.URL.Query().Get("X-Emby-Client")); c != "" {
		return c
	}

	// 3. Fallback to User-Agent matching
	ua := strings.TrimSpace(r.UserAgent())
	if ua == "" {
		return "Unknown"
	}
	uaLower := strings.ToLower(ua)
	switch {
	case strings.Contains(uaLower, "senplayer"):
		return "SenPlayer"
	case strings.Contains(uaLower, "infuse"):
		return "Infuse"
	case strings.Contains(uaLower, "vidhub"):
		return "VidHub"
	case strings.Contains(uaLower, "afusekt"):
		return "AfuseKt"
	case strings.Contains(uaLower, "fileball"):
		return "Fileball"
	case strings.Contains(uaLower, "forward"):
		return "Forward"
	case strings.Contains(uaLower, "hamme"):
		return "Hamme"
	case strings.Contains(uaLower, "yamby"):
		return "Yamby"
	case strings.Contains(uaLower, "emby"):
		return "Emby Client"
	case strings.Contains(uaLower, "cfnetwork") || strings.Contains(uaLower, "darwin"):
		if dev := strings.TrimSpace(r.Header.Get("X-Emby-Device-Name")); dev != "" {
			return dev
		}
		return "iOS Player"
	case strings.Contains(uaLower, "okhttp"):
		if dev := strings.TrimSpace(r.Header.Get("X-Emby-Device-Name")); dev != "" {
			return dev
		}
		return "Android Player"
	case strings.Contains(uaLower, "mozilla") || strings.Contains(uaLower, "chrome") || strings.Contains(uaLower, "safari") || strings.Contains(uaLower, "edge"):
		return "Web Browser"
	default:
		if len(ua) > 24 {
			return ua[:24] + "..."
		}
		return ua
	}
}

func extractAuthClient(auth string) string {
	for _, part := range strings.Split(auth, ",") {
		part = strings.TrimSpace(part)
		if idx := strings.Index(part, "Client="); idx >= 0 {
			val := strings.Trim(strings.TrimSpace(part[idx+7:]), `"`)
			if val != "" {
				return val
			}
		}
	}
	return ""
}
