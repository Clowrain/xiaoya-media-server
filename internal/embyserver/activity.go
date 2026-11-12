package embyserver

import (
	"net/http"
	"sync"
	"time"
)

type activityEntry struct {
	ID, Category, ItemID, Name, State, Current, Error string
	DeviceKey                                         string
	UserID                                            string
	PositionTicks, RunTimeTicks                       int64
	Progress                                          float64
	Online                                            bool
	Hidden                                            bool
	Username, IP, Device, Client                      string
	Total, Done                                       int
	Started, Updated                                  time.Time
}
type activityState struct {
	mu      sync.Mutex
	entries []*activityEntry
}

func (a *App) newActivity(category, item, name string) string {
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	// Retain recent completed entries; never discard active work.
	if len(a.activity.entries) >= 1000 {
		for i, v := range a.activity.entries {
			if v.State == "complete" || v.State == "error" || v.Category == "playback" {
				a.activity.entries = append(a.activity.entries[:i], a.activity.entries[i+1:]...)
				break
			}
		}
	}
	key := id()
	now := time.Now()
	a.activity.entries = append(a.activity.entries, &activityEntry{ID: key, Category: category, ItemID: item, Name: name, State: "waiting", Started: now, Updated: now})
	return key
}
func (a *App) changeActivity(key string, change func(*activityEntry)) {
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	for _, v := range a.activity.entries {
		if v.ID == key {
			change(v)
			v.Updated = time.Now()
			return
		}
	}
}
func (a *App) finishActivity(key string, err error) {
	a.changeActivity(key, func(v *activityEntry) {
		v.State = "complete"
		if err != nil {
			v.State = "error"
			v.Error = err.Error()
		}
	})
}
func (a *App) activitySnapshot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("category") == "proxy" {
		a.proxyDebugLogs(w, r)
		return
	}
	if r.Method == "DELETE" {
		a.activity.mu.Lock()
		for _, v := range a.activity.entries {
			v.Hidden = true
		}
		a.activity.mu.Unlock()
		respond(w, M{"ok": true})
		return
	}
	if r.Method != "GET" {
		fail(w, 405, "GET required")
		return
	}
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	entries := make([]activityEntry, 0, len(a.activity.entries))
	waiting, running := 0, 0
	online := map[string]bool{}
	for i := len(a.activity.entries) - 1; i >= 0; i-- {
		v := a.activity.entries[i]
		if v.Hidden {
			continue
		}
		snapshot := *v
		snapshot.Online = v.Category == "playback" && v.State == "requested" && time.Since(v.Updated) < 90*time.Second
		entries = append(entries, snapshot)
		if snapshot.Online {
			online[v.UserID+":"+v.DeviceKey] = true
		}
		if v.Category == "probe" {
			if v.State == "waiting" {
				waiting++
			}
			if v.State == "running" {
				running++
			}
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	respond(w, M{"OnlinePlayback": len(online), "Entries": entries, "ProbeConcurrency": a.probeSettings().Concurrency, "ProbeRunning": running, "ProbeWaiting": waiting, "Retention": "最近1000条，服务重启清空"})
}
