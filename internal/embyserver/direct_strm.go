package embyserver

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Only recognized local STRM resolvers are contacted. Viewer authentication and
// device reservation happen in stream before this function is called.
func strmResolver(raw string) string {
	if xiaoyaSource(raw) {
		return os.Getenv("XIAOYA_URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	if u.Scheme == "http" && u.Port() == "8115" && u.Path == "/api/" && (h == "172.17.0.1" || h == "127.0.0.1" || h == "localhost") {
		return os.Getenv("NANSHARE_URL")
	}
	return ""
}

var strmLinks = struct {
	sync.Mutex
	entries map[string]cdnLink
}{entries: make(map[string]cdnLink)}

func (a *App) resolveSTRM(w http.ResponseWriter, r *http.Request, x Item, endpoint string) {
	key := digest(endpoint + "|" + x.URL + "|" + token(r) + "|" + r.UserAgent() + "|" + r.Header.Get("X-Real-IP"))
	strmLinks.Lock()
	hit, ok := strmLinks.entries[key]
	strmLinks.Unlock()
	if ok && time.Now().Before(hit.until) {
		w.Header().Set("Location", hit.location)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusFound)
		return
	}
	link, err := a.resolvePlaybackLink(r, x.URL, endpoint)
	if err != nil {
		RecordGlobalLog("ERROR", "playback", "STRM 直链解析失败", fmt.Sprintf("影片: %s (ID: %s) · 原始 STRM: %s", x.Name, x.ID, x.URL), "", err.Error())
		fail(w, http.StatusBadGateway, "STRM 直链解析失败: "+err.Error())
		return
	}
	RecordGlobalLog("302", "playback", "STRM 直链解析成功", fmt.Sprintf("影片: %s · 播放地址: %s", x.Name, link), "", "")
	strmLinks.Lock()
	now := time.Now()
	for k, v := range strmLinks.entries {
		if !now.Before(v.until) {
			delete(strmLinks.entries, k)
		}
	}
	if len(strmLinks.entries) >= 512 {
		for k := range strmLinks.entries {
			delete(strmLinks.entries, k)
			break
		}
	}
	strmLinks.entries[key] = cdnLink{link, http.StatusFound, now.Add(5 * time.Second)}
	strmLinks.Unlock()
	w.Header().Set("Location", link)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}
