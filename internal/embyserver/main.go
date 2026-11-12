package embyserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/web"
)

type M = map[string]any
type ExtensionHandler func(w http.ResponseWriter, r *http.Request, u User) bool

type App struct {
	cfg                       *config.Config
	extHandler                ExtensionHandler
	alistPlaybackBase         string
	alistPlayback             alistFileLookup
	scanControlMu             sync.Mutex
	scanResume                chan struct{}
	pages                     pageCache
	cursorSecret              string
	proxyDebug                proxyDebugState
	activity                  activityState
	probes                    probeState
	db                        *Database
	authenticator             Authenticator
	mediaItemRepository       MediaItemRepository
	itemPageRepository        ItemPageRepository
	libraryRepository         LibraryRepository
	userRepository            UserRepository
	coverRepository           CoverRepository
	playbackSessionRepository PlaybackSessionRepository
	playbackSourceSelector    mediaSourceSelector
	playbackURLResolver       playbackURLResolver
	databaseDSN               string
	backupMu                  sync.Mutex
	write                     sync.Mutex
	libraryConfig             sync.Mutex
	scan                      sync.RWMutex
	jobs                      libraryJobQueue
	playing                   sync.Mutex
	loginMu                   sync.Mutex
	attempts                  map[string][]time.Time
	serverID                  string
	suppressor                watchSuppressor
}
type User struct {
	ID     string
	Name   string
	Hash   string
	Admin  bool
	First  bool
	Max    int
	API    bool
	Device string
}

func id() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func must(e error) {
	if e != nil {
		log.Fatal(e)
	}
}
func (a *App) init() {
	must(a.ensureSchema())
	if os.Getenv("SCHEMA_ONLY") == "1" {
		return
	}
	if err := a.libraryStore().MarkInterruptedScans("服务重启，请重新扫描"); err != nil {
		log.Printf("mark interrupted library scans: %v", err)
	}
	if flag.Lookup("test.v") == nil {
		libCount, countErr := a.libraryStore().Count()
		if countErr == nil && libCount == 0 {
			strmRoot := os.Getenv("MEDIA_ROOT_PATH")
			if strmRoot == "" {
				strmRoot = "/media/strm"
			}
			libID := id()
			if err := a.libraryStore().Create(a.db, LibraryRecord{ID: libID, Name: "小雅影视", Path: strmRoot, Kind: "tvshows"}); err == nil {
				if err := a.libraryStore().SavePaths(a.db, libID, []string{strmRoot}); err != nil {
					log.Printf("save bootstrap library paths: %v", err)
				}
			}
		}
	}
	n, err := a.userStore().CountAccounts()
	must(err)
	if n == 0 {
		p := os.Getenv("ADMIN_PASSWORD")
		if len(p) < 12 {
			log.Fatal("ADMIN_PASSWORD must contain at least 12 characters")
		}
		h, e := bcrypt.GenerateFromPassword([]byte(p), 12)
		must(e)
		must(a.userStore().CreateAccount(UserAccount{ID: id(), Name: "admin", Hash: string(h), Admin: true, First: true, MaxDevices: 5}, nil))
	}
	if k := os.Getenv("BOOTSTRAP_API_KEY"); k != "" {
		var initialized string
		e := a.db.QueryRow("SELECT v FROM settings WHERE k='key_initialized'").Scan(&initialized)
		if e == sql.ErrNoRows {
			_, e = a.db.Exec("INSERT INTO api_keys VALUES(?,?,?,?) ON CONFLICT DO NOTHING", id(), "NanShare", digest(k), time.Now().Unix())
			must(e)
			_, e = a.db.Exec("INSERT INTO settings VALUES('key_initialized','1')")
			must(e)
		}
	}
	a.db.Exec("INSERT INTO settings VALUES('server_id',?) ON CONFLICT DO NOTHING", id())
	must(a.db.QueryRow("SELECT v FROM settings WHERE k='server_id'").Scan(&a.serverID))
}
func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, s string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(M{"error": s, "Message": s})
}
func body(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if e := json.NewDecoder(r.Body).Decode(v); e != nil {
		fail(w, 400, "无效 JSON")
		return false
	}
	return true
}
func q(r *http.Request, k string) string {
	for key, v := range r.URL.Query() {
		if strings.EqualFold(key, k) && len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

var authField = regexp.MustCompile(`(?i)(Token|DeviceId|Client|Device|Version)\s*=\s*"([^"]*)"`)

func field(r *http.Request, k string) string {
	for _, value := range []string{r.Header.Get("X-Emby-Authorization"), r.Header.Get("Authorization"), q(r, "X-Emby-Authorization")} {
		for _, v := range authField.FindAllStringSubmatch(value, -1) {
			if strings.EqualFold(v[1], k) {
				return v[2]
			}
		}
	}
	return ""
}
func token(r *http.Request) string {
	for _, s := range []string{r.Header.Get("X-Emby-Token"), r.Header.Get("X-MediaBrowser-Token"), q(r, "api_key"), q(r, "X-Emby-Token"), q(r, "X-MediaBrowser-Token"), field(r, "Token"), r.Header.Get("X-Emby-Api-Key"), q(r, "X-Emby-Api-Key")} {
		if s != "" {
			return s
		}
	}
	authz := r.Header.Get("Authorization")
	if len(authz) >= 7 && strings.EqualFold(authz[:7], "bearer ") {
		return authz[7:]
	}
	return authz
}
func device(r *http.Request) string {
	for _, s := range []string{field(r, "DeviceId"), q(r, "DeviceId"), q(r, "X-Emby-Device-Id"), r.Header.Get("X-Emby-Device-Id")} {
		if s != "" {
			return s
		}
	}
	return ""
}

// Some clients join an /emby base URL with an already prefixed API path.
func embyPath(p string) string {
	// Normalize duplicate API separators without redirecting POST requests.
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	p = strings.TrimRight(p, "/")
	for strings.HasPrefix(strings.ToLower(p), "/emby/") {
		p = p[5:]
	}
	return p
}
func (a *App) serverInfo() M {
	return M{"Id": a.serverID, "ServerName": a.displayName(), "Name": a.displayName(), "Version": "4.8.0.80", "OperatingSystem": "Linux", "ProductName": "Xiaoya Media Server", "LocalAddress": os.Getenv("PUBLIC_URL"), "WanAddress": os.Getenv("PUBLIC_URL"), "LocalAddresses": []string{os.Getenv("PUBLIC_URL")}, "RemoteAddresses": []string{os.Getenv("PUBLIC_URL")}, "StartupWizardCompleted": true, "SupportsLibraryMonitor": false, "HasUpdateAvailable": false}
}
func (a *App) serve(w http.ResponseWriter, r *http.Request) {
	if !shouldSkipLog(r.URL.Path) {
		reqStarted := time.Now()
		tracked := &fullChainResponseWriter{ResponseWriter: w, status: 200}
		w = tracked
		defer func() {
			elapsed := time.Since(reqStarted)
			durMs := float64(elapsed.Microseconds()) / 1000.0
			status := tracked.status
			if status == 0 {
				status = 200
			}
			level := "INFO"
			category := "http"
			title := "客户端请求"
			target := tracked.location
			errStr := ""
			msg := fmt.Sprintf("%s %s · HTTP %d (%.1fms)", r.Method, r.URL.Path, status, durMs)

			pLower := strings.ToLower(r.URL.Path)
			isVideo := isVideoRequest(r.URL.Path) || strings.Contains(pLower, "/playbackinfo") || strings.HasSuffix(pLower, "/stream.mp4")

			if isVideo {
				category = "playback"
				if status == http.StatusFound || status == http.StatusMovedPermanently || status == http.StatusTemporaryRedirect {
					level = "302"
					title = "播放直链 302 重定向"
					target = redactPlaybackURL(target)
					msg = fmt.Sprintf("播放重定向成功 -> 目标 CDN: %s (耗时 %.1fms)", target, durMs)
				} else if status >= 400 {
					level = "ERROR"
					title = "播放请求失败"
					errStr = tracked.bodyBuf.String()
					msg = fmt.Sprintf("播放请求失败 · HTTP %d · %s", status, errStr)
				} else {
					title = "播放链路处理"
				}
			} else if strings.HasPrefix(pLower, "/admin/scan") || strings.HasPrefix(pLower, "/api/sync") {
				category = "scan"
				title = "媒体库扫描/同步"
			} else if strings.HasPrefix(pLower, "/admin/tmdb") {
				category = "tmdb"
				title = "TMDB 元数据"
			}

			if status >= 400 && level != "ERROR" {
				level = "ERROR"
				errStr = tracked.bodyBuf.String()
			}

			entry := &LogEntry{
				ID:         fmt.Sprintf("%d-%s", reqStarted.UnixNano(), id()[:6]),
				Time:       reqStarted,
				Level:      level,
				Category:   category,
				Title:      title,
				Method:     r.Method,
				Path:       r.URL.Path,
				Status:     status,
				DurationMs: durMs,
				Client:     detectClientInfo(r),
				IP:         clientIP(r),
				Target:     target,
				Message:    msg,
				Detail:     tracked.bodyBuf.String(),
				Error:      errStr,
			}
			sysLogger.add(entry)
		}()
	}

	if done := a.beginProxyDebug(&w, r); done != nil {
		defer done()
	}
	if isVideoRequest(r.URL.Path) || strings.Contains(strings.ToLower(r.URL.Path), "/playbackinfo") {
		tracked := &errorResponse{ResponseWriter: w, status: 200}
		w = tracked
		started := time.Now()
		defer func() {
			if tracked.status >= 400 {
				a.recordError(r, "播放请求错误", fmt.Sprintf("%s %s · HTTP %d · 耗时 %s · %s", r.Method, r.URL.Path, tracked.status, time.Since(started), tracked.detail.String()))
			}
		}()
	}
	if client := strings.ToLower(r.UserAgent()); strings.Contains(client, "lenna") || strings.Contains(client, "infuse") || strings.Contains(client, "senplayer") {
		tracked := &clientResponse{ResponseWriter: w, status: 200}
		w = tracked
		started := time.Now()
		defer func() {
			log.Printf("client request ua=%q method=%s path=%q status=%d duration=%s", r.UserAgent(), r.Method, r.URL.Path, tracked.status, time.Since(started))
		}()
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if r.Header.Get("Origin") == "emby-local://app" {
		w.Header().Set("Access-Control-Allow-Origin", "emby-local://app")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, DELETE, PATCH, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Accept-Language, Content-Type, Authorization, Cache-Control, Origin, OriginToken, Pragma, Range, If-Modified-Since, If-None-Match, X-Emby-Language, X-Emby-Authorization, X-Emby-Token, X-MediaBrowser-Token, X-Emby-Client, X-Emby-Client-Version, X-Emby-Device-Id, X-Emby-Device-Name, X-Emby-Api-Key")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if a.filesRoute(w, r) {
		return
	}
	p := embyPath(r.URL.Path)
	l := strings.ToLower(p)
	if strings.Contains(l, "/download") || strings.HasPrefix(l, "/sync") {
		fail(w, 403, "下载媒体已禁用")
		return
	}
	if l == "/health" || l == "/api/system/health" {
		var v int
		if e := a.db.QueryRow("SELECT 1").Scan(&v); e != nil {
			fail(w, 503, "database unavailable")
			return
		}
		respond(w, M{"status": "ok", "service": "xiaoya-media-server"})
		return
	}
	if l == "/system/ping" {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Emby Server"))
		return
	}
	if l == "/system/info/public" {
		respond(w, a.serverInfo())
		return
	}
	// Third-party client multi-domain extension endpoint (SenPlayer, VidHub, AfuseKt, etc.)
	if l == "/system/ext/serverdomains" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		host := r.Host
		if host == "" {
			host = "127.0.0.1:8096"
		}
		respond(w, M{
			"ok": true,
			"data": []M{
				{
					"name": "当前线路 (直连)",
					"url":  fmt.Sprintf("%s://%s", scheme, host),
				},
			},
		})
		return
	}
	// Emby web hits Branding on the login screen, before any token exists.
	if l == "/branding/configuration" {
		respond(w, M{"LoginDisclaimer": "", "CustomCss": "", "SplashscreenEnabled": false})
		return
	}
	if l == "/users/authenticatebyname" {
		a.login(w, r)
		return
	}
	if l == "/users/authenticate" {
		a.login(w, r)
		return
	}
	if strings.HasPrefix(l, "/web/strings/") {
		if r.Method != "GET" && r.Method != "HEAD" {
			fail(w, 405, "GET or HEAD required")
			return
		}
		name := strings.TrimPrefix(l, "/web/strings/")
		if strings.Contains(name, "/") || !strings.HasSuffix(name, ".json") {
			fail(w, 404, "Resource not found")
			return
		}
		entries, _ := web.Assets.ReadDir("strings")
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(entry.Name(), name) {
				b, err := web.Assets.ReadFile("strings/" + entry.Name())
				if err != nil {
					fail(w, 404, "Resource not found")
					return
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				if r.Method == "GET" {
					w.Write(b)
				}
				return
			}
		}
		fail(w, 404, "Resource not found")
		return
	}
	if l == "/web/manifest.json" && (r.Method == "GET" || r.Method == "HEAD") {
		respond(w, M{"name": a.displayName(), "short_name": "Emby", "start_url": "index.html", "display": "standalone", "icons": []M{}})
		return
	}
	if l == "/favicon.ico" {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><text y=".9em" font-size="90">🎬</text></svg>`))
		return
	}
	if l == "/users/public" {
		respond(w, []any{})
		return
	}
	if p == "" || l == "/web" || l == "/web/" || strings.HasPrefix(l, "/web/manage") || l == "/web/index.html" || l == "/web/dashboard.html" || l == "/index.html" || l == "/admin" || l == "/wall" {
		b, _ := web.Assets.ReadFile("index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
		return
	}
	if strings.Contains(l, "/assets/") {
		idx := strings.Index(l, "/assets/")
		assetPath := strings.TrimPrefix(p[idx:], "/")

		b, err := web.Assets.ReadFile(assetPath)
		if err != nil {
			// Case-insensitive fallback lookup
			entries, _ := web.Assets.ReadDir("assets")
			baseName := filepath.Base(assetPath)
			for _, entry := range entries {
				if strings.EqualFold(entry.Name(), baseName) {
					assetPath = "assets/" + entry.Name()
					b, err = web.Assets.ReadFile(assetPath)
					break
				}
			}
		}
		if err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte("Asset not found"))
			return
		}

		ext := strings.ToLower(filepath.Ext(assetPath))
		var ct string
		switch ext {
		case ".js", ".mjs":
			ct = "application/javascript; charset=utf-8"
		case ".css":
			ct = "text/css; charset=utf-8"
		case ".svg":
			ct = "image/svg+xml"
		case ".woff2":
			ct = "font/woff2"
		case ".woff":
			ct = "font/woff"
		case ".ttf":
			ct = "font/ttf"
		case ".png":
			ct = "image/png"
		case ".jpg", ".jpeg":
			ct = "image/jpeg"
		case ".ico":
			ct = "image/x-icon"
		case ".json":
			ct = "application/json; charset=utf-8"
		default:
			ct = mime.TypeByExtension(ext)
			if ct == "" {
				ct = "application/octet-stream"
			}
		}

		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Write(b)
		return
	}
	if l == "/auth/playback" {
		a.checkPlayback(w, r)
		return
	}
	if a.taggedImage(w, r, p) {
		return
	}
	u, e := a.auth(r)
	if e != nil {
		a.authWarning(r, "", "令牌缺失、无效或已过期")
		fail(w, 401, "请先登录")
		return
	}
	if a.extHandler != nil && strings.HasPrefix(l, "/api/") {
		if !u.Admin {
			fail(w, 403, "需要管理员权限")
			return
		}
		if a.extHandler(w, r, u) {
			return
		}
	}
	if a.imageUploadRoute(w, r, u, p) {
		return
	}
	if a.browseRoute(w, r, u, p) {
		return
	}
	if strings.HasPrefix(l, "/admin/") {
		if !u.Admin || u.API {
			fail(w, 403, "需要管理员账号")
			return
		}
		a.admin(w, r, u, l)
		return
	}
	if l == "/system/endpoint" {
		serveEndpoint(w, r)
		return
	}
	if l == "/system/info" {
		respond(w, a.serverInfo())
		return
	}
	if l == "/auth/keys" {
		if !u.Admin {
			fail(w, 403, "无权限")
			return
		}
		a.admin(w, r, u, "/admin/keys")
		return
	}
	if l == "/users/me" {
		if u.API {
			fail(w, 403, "API key has no user")
			return
		}
		respond(w, a.userDTO(u))
		return
	}
	if l == "/users" {
		if !u.Admin && !u.API {
			fail(w, 403, "无权限")
			return
		}
		respond(w, a.users())
		return
	}
	if l == "/sessions/logout" {
		if err := a.resolveAuthenticator().Revoke(token(r)); err != nil {
			fail(w, 500, "登出失败")
			return
		}
		w.WriteHeader(204)
		return
	}
	if l == "/library/virtualfolders" {
		respond(w, a.libraries())
		return
	}
	if l == "/library/refresh" && r.Method == "POST" {
		if !u.Admin && !u.API {
			fail(w, 403, "无权限")
			return
		}
		go a.scanAll()
		w.WriteHeader(204)
		return
	}
	if l == "/items/counts" {
		m := M{}
		for k, v := range map[string]string{"MovieCount": "Movie", "EpisodeCount": "Episode", "SeriesCount": "Series"} {
			n, _ := a.mediaItems().CountByKind(v)
			m[k] = n
		}
		respond(w, m)
		return
	}
	if l == "/scheduledtasks" {
		respond(w, []M{})
		return
	}
	if strings.HasPrefix(l, "/scheduledtasks/running/") {
		w.WriteHeader(204)
		return
	}
	if l == "/sessions" {
		respond(w, []any{})
		return
	}
	if strings.HasPrefix(l, "/sessions/playing") {
		if u.API {
			fail(w, 403, "播放需要用户令牌")
			return
		}
		b := parsePlaybackCheckInRequest(w, r)
		if b.ItemID == "" {
			currentItem, err := a.playbackSessionStore().CurrentItem(u.ID, u.Device)
			if err != nil {
				log.Printf("read current playback session: %v", err)
			} else {
				b.ItemID = currentItem
			}
		}
		if b.ItemID == "" && b.PositionTicks == nil && l != "/sessions/playing/stopped" {
			fail(w, 400, "缺少播放参数")
			return
		}
		if l == "/sessions/playing/stopped" {
			if err := a.playbackSessionStore().Release(u.ID, u.Device); err != nil {
				log.Printf("release playback session: %v", err)
			}
		} else if e := a.reserve(u, b.ItemID); e != nil {
			fail(w, 403, e.Error())
			return
		}
		if b.ItemID != "" {
			if x, err := a.item(b.ItemID); err == nil && l != "/sessions/playing/stopped" {
				a.logPlayback(r, u, x)
				if l == "/sessions/playing" {
					a.scheduleNext(x, r)
				}
			}
			if b.PositionTicks != nil {
				a.playbackProgress(u, b.ItemID, int64(*b.PositionTicks), int64(b.RunTimeTicks))
			}
			a.updatePlaybackActivity(u, b.ItemID, l == "/sessions/playing/stopped")
			if b.PositionTicks != nil {
				if err := a.userStore().SetPlaybackPosition(u.ID, b.ItemID, max(int64(*b.PositionTicks), 0)); err != nil {
					log.Printf("update session playback position: %v", err)
				}
			}
			if err := a.playbackSessionStore().TouchResume(u.ID, b.ItemID, time.Now().UnixNano()); err != nil {
				log.Printf("update resume activity: %v", err)
			}
		}
		w.WriteHeader(204)
		return
	}
	if strings.HasPrefix(l, "/users/") {
		s := strings.Split(p, "/")
		if len(s) >= 3 {
			uid := s[2]
			if !u.API && !u.Admin && uid != u.ID {
				fail(w, 403, "无权限")
				return
			}
			if len(s) == 3 {
				account, e := a.userStore().GetAccount(uid)
				if e != nil {
					fail(w, 404, "用户不存在")
					return
				}
				x := User{ID: account.ID, Name: account.Name, Hash: account.Hash, Admin: account.Admin, First: account.First, Max: account.MaxDevices}
				respond(w, a.userDTO(x))
				return
			}
			switch strings.ToLower(s[3]) {
			case "password":
				a.password(w, r, u, uid)
				return
			case "views":
				respond(w, ItemsQueryResultDTO{Items: a.libraryDTOs(), TotalRecordCount: len(a.libraries()), StartIndex: 0})
				return
			case "items":
				if len(s) > 5 {
					switch strings.ToLower(s[5]) {
					case "similar":
						a.similar(w, r, u, s[4])
						return
					case "playbackinfo":
						a.playback(w, r, u, s[4])
						return
					case "images":
						a.poster(w, r, s[4])
						return
					case "refreshmediainfo":
						a.probeMedia(w, r, u, s[4])
						return
					case "remotesearch":
						respond(w, []any{})
						return
					case "refresh":
						if !u.API && !u.Admin {
							fail(w, 403, "无权限")
							return
						}
						go func(targetID string) {
							if err := a.refreshSingleItemMetadata(targetID, true); err != nil {
								a.scanAll()
							}
						}(s[4])
						w.WriteHeader(204)
						return
					default:
						fail(w, 404, "未实现此接口")
						return
					}
				}
				if len(s) == 5 && strings.EqualFold(s[4], "folderviews") {
					respond(w, ItemsQueryResultDTO{Items: a.libraryDTOs(), TotalRecordCount: len(a.libraries()), StartIndex: 0})
					return
				}
				if len(s) > 4 && strings.ToLower(s[4]) != "latest" && strings.ToLower(s[4]) != "resume" {
					a.single(w, r, u, s[4])
					return
				}
				a.items(w, r, u, strings.HasSuffix(l, "/latest"))
				return
			case "shows":
				// /Users/{uid}/Shows/{sid}/Seasons|Episodes mirrors the top-level /Shows/{sid}/... routes.
				if len(s) == 6 && (strings.EqualFold(s[5], "seasons") || strings.EqualFold(s[5], "episodes")) {
					setQuery(r, "ParentId", s[4])
					if strings.EqualFold(s[5], "seasons") {
						setQuery(r, "IncludeItemTypes", "Season")
					} else {
						setQuery(r, "IncludeItemTypes", "Episode")
						setQuery(r, "Recursive", "true")
						if sid := q(r, "SeasonId"); sid != "" {
							setQuery(r, "ParentId", sid)
						}
					}
					setQuery(r, "SortBy", "ParentIndexNumber,IndexNumber")
					a.items(w, r, u, false)
					return
				}
				fail(w, 404, "未实现此接口")
				return
			}
		}
	}
	if l == "/items" || l == "/items/resume" {
		a.items(w, r, u, false)
		return
	}
	if strings.HasPrefix(l, "/items/") {
		s := strings.Split(p, "/")
		if len(s) == 3 {
			a.single(w, r, u, s[2])
			return
		}
		if len(s) > 3 {
			switch strings.ToLower(s[3]) {
			case "extras":
				respond(w, M{"Items": []M{}, "TotalRecordCount": 0, "StartIndex": 0})
				return
			case "refreshmediainfo":
				a.probeMedia(w, r, u, s[2])
				return
			case "similar":
				a.similar(w, r, u, s[2])
				return
			case "playbackinfo":
				a.playback(w, r, u, s[2])
				return
			case "images":
				a.poster(w, r, s[2])
				return
			case "remotesearch":
				respond(w, []any{})
				return
			case "refresh":
				if !u.API && !u.Admin {
					fail(w, 403, "无权限")
					return
				}
				go func(targetID string) {
					if err := a.refreshSingleItemMetadata(targetID, true); err != nil {
						a.scanAll()
					}
				}(s[2])
				w.WriteHeader(204)
				return
			}
		}
	}
	if strings.HasPrefix(l, "/videos/") {
		s := strings.Split(p, "/")
		if strings.Contains(l, "/subtitles/") {
			a.serveVideoSubtitles(w, r, s)
			return
		}
		if len(s) >= 4 && (strings.HasPrefix(strings.ToLower(s[3]), "stream") || strings.HasPrefix(strings.ToLower(s[3]), "original")) {
			a.stream(w, r, u, s[2])
			return
		}
		fail(w, 400, "不支持转码或 HLS，请选择直接播放")
		return
	}
	if strings.HasPrefix(l, "/shows/") {
		s := strings.Split(p, "/")
		if len(s) == 3 {
			a.single(w, r, u, s[2])
			return
		}
		if len(s) > 3 {
			switch strings.ToLower(s[3]) {
			case "extras", "intros", "themesongs", "themevideos", "specialfeatures":
				respond(w, M{"Items": []M{}, "TotalRecordCount": 0, "StartIndex": 0})
				return
			}
			r.URL.RawQuery = r.URL.Query().Encode() + "&ParentId=" + url.QueryEscape(s[2])
			a.items(w, r, u, false)
			return
		}
	}
	fail(w, 404, "未实现此接口")
}
func (a *App) users() []UserDTO {
	out := []UserDTO{}
	// Load account rows before DTO mapping because userDTO/canPlay issue nested queries.
	accounts, err := a.userStore().ListAccounts()
	if err != nil {
		return out
	}
	for _, account := range accounts {
		user := User{ID: account.ID, Name: account.Name, Hash: account.Hash, Admin: account.Admin, First: account.First, Max: account.MaxDevices}
		dto := a.userDTO(user)
		if account.LastLogin > 0 {
			dto.LastLoginDate = time.Unix(account.LastLogin, 0).UTC().Format("2006-01-02T15:04:05.0000000Z")
		}
		out = append(out, dto)
	}
	return out
}
func (a *App) password(w http.ResponseWriter, r *http.Request, u User, uid string) {
	if r.Method != "POST" {
		fail(w, 405, "POST required")
		return
	}
	if u.API {
		fail(w, 403, "无权限")
		return
	}
	var b struct{ CurrentPw, NewPw string }
	if !body(w, r, &b) {
		return
	}
	if len(b.NewPw) < 10 || len(b.NewPw) > 72 {
		fail(w, 400, "密码需要 10–72 字节")
		return
	}
	if uid == u.ID && bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(b.CurrentPw)) != nil {
		fail(w, 403, "当前密码不正确")
		return
	}
	h, e := bcrypt.GenerateFromPassword([]byte(b.NewPw), 12)
	if e != nil {
		fail(w, 500, "密码生成失败")
		return
	}
	e = a.userStore().UpdatePasswordAndRevokeTokens(uid, string(h))
	if e != nil {
		fail(w, 500, "保存失败")
		return
	}
	respond(w, M{"ok": true})
}
func (a *App) admin(w http.ResponseWriter, r *http.Request, u User, p string) {
	if r.Method != "GET" {
		a.write.Lock()
		defer a.write.Unlock()
	}
	switch p {
	case "/admin/tmdb", "/admin/tmdb/test", "/admin/tmdb/search", "/admin/tmdb/match":
		a.adminTMDB(w, r, p)
	case "/admin/media-info/restore":
		a.restoreMediaInfo(w, r)
	case "/admin/media-item":
		a.manageMediaItem(w, r)
	case "/admin/enhancements":
		a.enhancementSettings(w, r)
	case "/admin/media-info":
		a.mediaSettings(w, r)
	case "/admin/directories", "/admin/order", "/admin/cover":
		a.extraAdmin(w, r, p)
	case "/admin/library-folders":
		a.libraryFolders(w, r)
	case "/admin/libraries":
		if r.Method == http.MethodGet {
			respond(w, a.libraries())
			return
		}
		if r.Method == http.MethodDelete {
			targetID := strings.TrimSpace(r.URL.Query().Get("id"))
			if targetID == "" {
				targetID = strings.TrimSpace(r.URL.Query().Get("ID"))
			}
			if targetID == "" {
				var b struct {
					ID string `json:"ID"`
				}
				_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&b)
				targetID = strings.TrimSpace(b.ID)
			}
			if targetID == "" {
				fail(w, 400, "缺少媒体库 ID 参数")
				return
			}
			a.deleteLibrary(w, targetID)
			return
		}
		if r.Method == http.MethodPut {
			var b struct {
				ID, Name, Kind string
				Paths          []string
			}
			if !body(w, r, &b) {
				return
			}
			a.updateLibrary(w, b.ID, b.Name, b.Kind, b.Paths)
			return
		}
		if r.Method == http.MethodPost {
			var b struct {
				Name, Path, Kind string
				Paths            []string
			}
			if !body(w, r, &b) {
				return
			}
			a.createLibrary(w, b.Name, b.Kind, b.Path, b.Paths)
			return
		}
		fail(w, 405, "Method not allowed")

	case "/admin/scan-control":
		a.scanControlAPI(w, r)
	case "/admin/scan-all":
		if r.Method != "POST" {
			fail(w, 405, "POST required")
			return
		}
		for _, lib := range a.libraries() {
			go a.scanLibraryMode(lib["Id"].(string), true)
		}
		respond(w, M{"queued": true})
	case "/admin/scan-schedule":
		a.scanScheduleAPI(w, r)
	case "/admin/scan":
		if r.Method != "POST" {
			fail(w, 405, "method")
			return
		}
		var b struct{ ID, Mode string }
		if !body(w, r, &b) {
			return
		}
		exists, lookupErr := a.libraryStore().Exists(b.ID)
		if lookupErr != nil || !exists {
			fail(w, 404, "媒体库不存在")
			return
		}
		incremental := false
		switch strings.ToLower(b.Mode) {
		case "update", "incremental":
			incremental = true
		case "scan", "refresh", "full", "":
			incremental = false
		default:
			fail(w, 400, "无效扫描模式")
			return
		}
		go a.scanLibraryMode(b.ID, incremental)
		respond(w, M{"queued": true})
	case "/admin/user-identity":
		a.userIdentity(w, r)
	case "/admin/users":
		if r.Method == "GET" {
			respond(w, a.users())
			return
		}
		var b struct {
			ID, Name, Password string
			MaxDevices         int
			Admin              bool
			AllowPlayback      *bool
		}
		if !body(w, r, &b) {
			return
		}
		if r.Method == "DELETE" {
			first, e := a.userStore().IsFirstAdmin(b.ID)
			if e != nil {
				fail(w, 404, "用户不存在")
				return
			}
			if first || b.ID == u.ID {
				fail(w, 403, "不能删除首位管理员或当前账号")
				return
			}
			if e = a.userStore().DeleteUser(b.ID); e != nil {
				fail(w, 500, "删除失败")
				return
			}
			if err := a.playbackSessionStore().ReleaseUser(b.ID); err != nil {
				log.Printf("release deleted user playback sessions: %v", err)
			}
			respond(w, M{"ok": true})
			return
		}
		if b.MaxDevices < 1 || b.MaxDevices > 100 {
			fail(w, 400, "设备数量范围 1–100")
			return
		}
		if r.Method == "PUT" {
			first, lookupErr := a.userStore().IsFirstAdmin(b.ID)
			if lookupErr != nil {
				fail(w, 404, "用户不存在")
				return
			}
			if first {
				b.Admin = true
			}
			e := a.userStore().SetAccountSettings(b.ID, b.MaxDevices, b.Admin)
			if e != nil {
				fail(w, 500, "保存失败")
				return
			}
			if b.AllowPlayback != nil {
				if e := a.userStore().SetPlaybackAllowed(b.ID, *b.AllowPlayback); e != nil {
					fail(w, 500, "播放权限保存失败")
					return
				}
				if !*b.AllowPlayback {
					if err := a.playbackSessionStore().ReleaseUser(b.ID); err != nil {
						log.Printf("release playback sessions after permission change: %v", err)
					}
				}
			}
			respond(w, M{"ok": true})
			return
		}
		if len(b.Password) < 10 || len(b.Password) > 72 || strings.TrimSpace(b.Name) == "" {
			fail(w, 400, "账号不能为空，密码需要 10–72 字节")
			return
		}
		h, e := bcrypt.GenerateFromPassword([]byte(b.Password), 12)
		if e != nil {
			fail(w, 500, "密码生成失败")
			return
		}
		uid := id()
		e = a.userStore().CreateAccount(UserAccount{ID: uid, Name: b.Name, Hash: string(h), Admin: b.Admin, MaxDevices: b.MaxDevices}, b.AllowPlayback)
		if e != nil {
			fail(w, 409, "用户名重复或保存失败")
			return
		}
		respond(w, M{"ok": true})
	case "/admin/keys":
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == "GET" {
			rows, e := a.db.Query("SELECT id,name,created FROM api_keys ORDER BY created")
			if e != nil {
				fail(w, 500, "读取失败")
				return
			}
			defer rows.Close()
			out := []M{}
			for rows.Next() {
				var i, n string
				var t int64
				rows.Scan(&i, &n, &t)
				out = append(out, M{"Id": i, "Name": n, "Created": t, "MaskedKey": a.maskedKey(i)})
			}
			respond(w, out)
			return
		}
		var b struct{ ID, Name string }
		if !body(w, r, &b) {
			return
		}
		if r.Method == "DELETE" {
			_, e := a.db.Exec("DELETE FROM api_keys WHERE id=?", b.ID)
			if e != nil {
				fail(w, 500, "删除失败")
				return
			}
			respond(w, M{"ok": true})
			return
		}
		if r.Method != "POST" {
			fail(w, 405, "POST required")
			return
		}
		if strings.TrimSpace(b.Name) == "" {
			fail(w, 400, "请输入名称")
			return
		}
		t := id() + id()
		keyID := id()
		tx, e := a.db.Begin()
		if e != nil {
			fail(w, 500, "写入失败")
			return
		}
		defer tx.Rollback()
		_, e = tx.Exec("INSERT INTO api_keys VALUES(?,?,?,?)", keyID, b.Name, digest(t), time.Now().Unix())
		if e == nil {
			_, e = tx.Exec("INSERT INTO settings(k,v) VALUES(?,?)", "key-mask:"+keyID, t[:6]+"••••••••••••"+t[len(t)-4:])
		}
		w.Header().Set("Cache-Control", "no-store")
		if e != nil {
			fail(w, 500, "写入失败")
			return
		}
		if e = tx.Commit(); e != nil {
			fail(w, 500, "写入失败")
			return
		}
		respond(w, M{"Token": t})
	case "/admin/backup":
		if r.Method != "POST" {
			fail(w, 405, "method")
			return
		}
		path, e := a.backup()
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		respond(w, M{"Path": path})
	case "/admin/logs":
		a.handleUnifiedLogs(w, r)
	case "/admin/status":
		n, _ := a.mediaItems().CountAll()
		respond(w, M{"Items": n, "Database": "PostgreSQL", "Transcoding": false, "Playback": "302 redirect", "DeviceLeaseSeconds": 180})
	default:
		fail(w, 404, "not found")
	}
}
func (a *App) dto(x Item) M {
	folder := x.Kind == "Series" || x.Kind == "Season"
	tags := M{}
	if x.Poster != "" {
		tags["Primary"] = digest(x.Poster + strconv.FormatInt(x.Mtime, 10))[:12]
	}
	m := M{"Id": x.ID, "ServerId": a.serverID, "ParentId": x.Parent, "Name": x.Name, "SortName": itemSortName(x), "Type": x.Kind, "IsFolder": folder, "Path": x.Path, "Overview": x.Overview, "ProductionYear": x.Year, "IndexNumber": x.Episode, "ParentIndexNumber": x.Season, "ImageTags": tags, "BackdropImageTags": []string{}, "LocationType": "FileSystem", "UserData": makeUserItemDataDTO(x.ID, 0, false, false), "CanDownload": false}
	if !folder {
		m["MediaType"] = "Video"
		m["MediaSources"] = []MediaSourceDTO{a.source(x, "")}
	}
	a.enrich(x, m)
	if x.Kind == "Movie" || x.Kind == "Series" {
		delete(m, "IndexNumber")
		delete(m, "ParentIndexNumber")
	}
	if x.Year <= 0 {
		delete(m, "ProductionYear")
	}
	if x.Kind == "Season" {
		m["IndexNumber"] = x.Season
	}
	return m
}
func (a *App) source(x Item, t string) MediaSourceDTO {
	cleanURL := cleanDisplayXiaoyaURL(x.URL)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(strings.Split(x.URL, "?")[0]), "."))
	if ext == "" || len(ext) > 5 {
		ext = "mkv"
	}
	name := x.Name
	if isRemoteURL(x.URL) {
		name = cleanURL
	}
	return MediaSourceDTO{
		ID:                         x.ID,
		Name:                       name,
		Path:                       cleanURL,
		Protocol:                   "Http",
		Type:                       "Default",
		Container:                  ext,
		IsRemote:                   true,
		SupportsDirectPlay:         true,
		SupportsDirectStream:       true,
		MediaStreams:               []struct{}{},
		DirectStreamURL:            "/emby/Videos/" + x.ID + "/stream." + ext + "?Static=true&api_key=" + url.QueryEscape(t),
		AddAPIKeyToDirectStreamURL: false,
	}
}
func (a *App) reserve(u User, item string) error {
	if !a.canPlay(u.ID) {
		return errors.New("管理员已禁止该用户播放")
	}
	if u.API {
		return errors.New("播放需要用户令牌")
	}
	a.playing.Lock()
	defer a.playing.Unlock()
	limitReached, err := a.playbackSessionStore().Reserve(u.ID, u.Device, item, u.Max, time.Now().Unix())
	if err != nil {
		return err
	}
	if limitReached {
		return errors.New("已达到同时播放设备上限")
	}
	return nil
}
func (a *App) stream(w http.ResponseWriter, r *http.Request, u User, i string) {
	if r.Method != "GET" && r.Method != "HEAD" {
		fail(w, 405, "GET required")
		return
	}
	x, e := a.item(i)
	if e != nil || x.URL == "" {
		fail(w, 404, "无可播放源")
		return
	}
	// 客户端可能在 MediaSourceId 中指定同一版本的另一条目；无效值静默回退到路径条目。
	if ms := q(r, "MediaSourceId"); ms != "" && ms != x.ID {
		for _, v := range a.versions(x) {
			if v.ID == ms && v.URL != "" {
				x = v
				break
			}
		}
	}
	if !u.API {
		if e = a.reserve(u, i); e != nil {
			fail(w, 403, e.Error())
			return
		}
	}
	a.logPlayback(r, u, x)
	if strings.HasPrefix(x.URL, "file://") {
		if !u.API && r.Method == http.MethodGet {
			defer a.scheduleNext(x, r)
		}
		serveLocalMediaStream(w, r, x.URL)
		return
	}
	if !u.API && r.Method == http.MethodGet {
		tracked := &clientResponse{ResponseWriter: w, status: http.StatusOK}
		w = tracked
		defer func() {
			if tracked.status == http.StatusFound || tracked.status == http.StatusTemporaryRedirect {
				a.scheduleNext(x, r)
			}
		}()
	}
	a.resolvePlaybackRedirect(w, r, u, x)
}
func (a *App) poster(w http.ResponseWriter, r *http.Request, i string) { a.serveImage(w, r, i) }

// SetExtensionHandler registers the authenticated resource management API.
func (a *App) SetExtensionHandler(h ExtensionHandler) { a.extHandler = h }

// DB exposes the underlying PostgreSQL *sql.DB for unified repositories and migrations.
func (a *App) SQLDB() *sql.DB {
	if a.db == nil {
		return nil
	}
	return a.db.DB
}

// Handler returns the root HTTP handler of the media server.
func (a *App) Handler() http.Handler {
	return http.HandlerFunc(a.serve)
}

// TriggerScanAll queues an incremental scan across all configured libraries.
func (a *App) TriggerScanAll() {
	for _, lib := range a.libraries() {
		if libID, ok := lib["Id"].(string); ok && libID != "" {
			go a.scanLibraryMode(libID, true)
		}
	}
}

// TriggerScopedPathScan triggers an incremental scoped scan on any library containing targetPath.
func (a *App) TriggerScopedPathScan(targetPath string) {
	if strings.TrimSpace(targetPath) != "" {
		a.TriggerScopedPathScans([]string{targetPath})
	}
}

// PrepareSchema initializes and migrates the PostgreSQL database schema and exits cleanly.
func PrepareSchema(cfg *config.Config, extraMigrate func(*sql.DB) error) error {
	dsn, err := cfg.ResolveDatabaseDSN()
	if err != nil {
		return err
	}
	db, err := openDatabase(dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_ = os.Setenv("SCHEMA_ONLY", "1")
	defer os.Unsetenv("SCHEMA_ONLY")
	a := &App{cfg: cfg, db: db, attempts: map[string][]time.Time{}}
	a.init()
	if extraMigrate != nil {
		if err := extraMigrate(db.DB); err != nil {
			return err
		}
	}
	return nil
}

// OpenApp initializes the server App, database connection, and background media routines.
func OpenApp(ctx context.Context, cfg *config.Config, extraMigrate func(*sql.DB) error) (*App, func(), error) {
	dsn, err := cfg.ResolveDatabaseDSN()
	if err != nil {
		return nil, nil, err
	}
	if cfg.AList.BaseURL != "" && os.Getenv("XIAOYA_URL") == "" {
		_ = os.Setenv("XIAOYA_URL", cfg.AList.BaseURL)
	}
	if cfg.STRM.OutputRoot != "" {
		RegisterMediaRoot(cfg.STRM.OutputRoot)
	}
	db, err := openDatabase(dsn)
	if err != nil {
		return nil, nil, err
	}
	a := &App{
		cfg:                       cfg,
		db:                        db,
		databaseDSN:               dsn,
		attempts:                  map[string][]time.Time{},
		authenticator:             authenticationService{repository: sqlAuthRepository{db: db}},
		mediaItemRepository:       sqlMediaItemRepository{db: db},
		itemPageRepository:        sqlMediaItemRepository{db: db},
		libraryRepository:         sqlLibraryRepository{db: db},
		userRepository:            sqlUserRepository{db: db},
		coverRepository:           sqlCoverRepository{db: db},
		playbackSessionRepository: sqlPlaybackSessionRepository{db: db},
		playbackSourceSelector:    groupedMediaSourceSelector{versions: sqlMediaItemRepository{db: db}},
	}
	a.playbackURLResolver = strmPlaybackURLResolver{cache: &strmLinks, resolveLink: a.resolvePlaybackLink}
	a.init()
	if extraMigrate != nil {
		if err := extraMigrate(db.DB); err != nil {
			db.Close()
			return nil, nil, err
		}
	}
	go a.reclaimIdleMemory()
	go a.watchMediaContext(ctx)
	go a.runScanSchedule()
	if e := a.migrateMedia(); e != nil {
		log.Printf("media cache migration: %v", e)
	}
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if e := a.cleanupMedia(); e != nil {
					log.Printf("media cleanup: %v", e)
				}
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, e := a.backup(); e != nil {
					log.Printf("backup failed: %v", e)
				}
			}
		}
	}()
	cleanup := func() {
		a.scan.Lock()
		defer a.scan.Unlock()
		_ = db.Close()
	}
	return a, cleanup, nil
}

// Run starts the HTTP server until ctx is cancelled and performs graceful shutdown.
func Run(ctx context.Context, cfg *config.Config, setup func(*App) error, extraMigrate func(*sql.DB) error) error {
	a, cleanup, err := OpenApp(ctx, cfg, extraMigrate)
	if err != nil {
		return err
	}
	defer cleanup()
	if setup != nil {
		if err := setup(a); err != nil {
			return err
		}
	}
	listen := cfg.Server.Listen
	if listen == "" {
		listen = ":8096"
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("Xiaoya Media Server listening on %s; redirect playback enabled", listen)
		if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			errCh <- e
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	}
}
