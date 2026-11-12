package embyserver

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

type playbackURLResolver interface {
	ResolverEndpoint(raw string) string
	Resolve(r *http.Request, item Item, endpoint string) (playbackLinkResolution, error)
}

type playbackLinkResolution struct {
	Location string
	Status   int
	Cached   bool
}

type strmLinkCache struct {
	sync.Mutex
	entries map[string]cdnLink
}

var strmLinks = strmLinkCache{entries: make(map[string]cdnLink)}

type strmPlaybackURLResolver struct {
	cache       *strmLinkCache
	resolveLink func(r *http.Request, raw, endpoint string) (string, error)
}

func (s strmPlaybackURLResolver) ResolverEndpoint(raw string) string {
	if xiaoyaSource(raw) {
		return os.Getenv("XIAOYA_URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme == "http" && parsed.Port() == "8115" && parsed.Path == "/api/" && (host == "172.17.0.1" || host == "127.0.0.1" || host == "localhost") {
		return os.Getenv("NANSHARE_URL")
	}
	return ""
}

func (s strmPlaybackURLResolver) Resolve(r *http.Request, item Item, endpoint string) (playbackLinkResolution, error) {
	key := digest(endpoint + "|" + item.URL + "|" + token(r) + "|" + r.UserAgent() + "|" + r.Header.Get("X-Real-IP"))
	now := time.Now()
	s.cache.Lock()
	hit, ok := s.cache.entries[key]
	s.cache.Unlock()
	if ok && now.Before(hit.until) {
		return playbackLinkResolution{Location: hit.location, Status: hit.status, Cached: true}, nil
	}

	link, err := s.resolveLink(r, item.URL, endpoint)
	if err != nil {
		return playbackLinkResolution{}, err
	}
	s.cache.Lock()
	for k, entry := range s.cache.entries {
		if !now.Before(entry.until) {
			delete(s.cache.entries, k)
		}
	}
	if len(s.cache.entries) >= 512 {
		for k := range s.cache.entries {
			delete(s.cache.entries, k)
			break
		}
	}
	s.cache.entries[key] = cdnLink{link, http.StatusFound, now.Add(5 * time.Second)}
	s.cache.Unlock()
	return playbackLinkResolution{Location: link, Status: http.StatusFound}, nil
}

func (a *App) playbackURLResolution() playbackURLResolver {
	if a.playbackURLResolver != nil {
		return a.playbackURLResolver
	}
	return strmPlaybackURLResolver{cache: &strmLinks, resolveLink: a.resolvePlaybackLink}
}
