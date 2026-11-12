package embyserver

import (
	"net/http"
	"net/url"
	"os"
	"strings"
)

func (a *App) resolvePlaybackRedirect(w http.ResponseWriter, r *http.Request, user User, item Item) {
	if !user.API {
		if target := a.thirdPartyPlaybackURL(r); target != "" {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Location", target)
			w.WriteHeader(http.StatusFound)
			return
		}
	}
	if endpoint := a.playbackURLResolution().ResolverEndpoint(item.URL); !user.API && endpoint != "" {
		a.resolveSTRM(w, r, item, endpoint)
		return
	}
	if !user.API && os.Getenv("NANSHARE_URL") != "" {
		a.resolveNanShare(w, r)
		return
	}
	if !user.API && os.Getenv("PUBLIC_PLAYBACK_URL") != "" {
		w.Header().Set("Location", strings.TrimRight(os.Getenv("PUBLIC_PLAYBACK_URL"), "/")+a.playURL(item, token(r)))
		w.WriteHeader(http.StatusFound)
		return
	}
	target, err := url.Parse(item.URL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		fail(w, http.StatusUnprocessableEntity, "仅支持 HTTP/HTTPS STRM")
		return
	}
	w.Header().Set("Location", target.String())
	w.WriteHeader(http.StatusFound)
}
