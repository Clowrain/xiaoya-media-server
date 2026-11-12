package embyserver

import (
	"fmt"
	"net/http"
)

// Only recognized local STRM resolvers are contacted. Viewer authentication and
// device reservation happen in stream before this function is called.
func strmResolver(raw string) string {
	return strmPlaybackURLResolver{}.ResolverEndpoint(raw)
}

func (a *App) resolveSTRM(w http.ResponseWriter, r *http.Request, x Item, endpoint string) {
	resolved, err := a.playbackURLResolution().Resolve(r, x, endpoint)
	if err != nil {
		safeErr := safeProxyError(err)
		RecordGlobalLog("ERROR", "playback", "STRM 直链解析失败", fmt.Sprintf("影片: %s (ID: %s) · 原始 STRM: %s", x.Name, x.ID, redactPlaybackURL(x.URL)), "", safeErr)
		fail(w, http.StatusBadGateway, "STRM 直链解析失败: "+safeErr)
		return
	}
	if !resolved.Cached {
		RecordGlobalLog("302", "playback", "STRM 直链解析成功", fmt.Sprintf("影片: %s · 播放地址: %s", x.Name, redactPlaybackURL(resolved.Location)), "", "")
	}
	w.Header().Set("Location", resolved.Location)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(resolved.Status)
}
