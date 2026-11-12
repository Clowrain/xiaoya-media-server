package embyserver

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"
)

func (a *App) logPlayback(r *http.Request, u User, x Item) {
	if u.API || strings.EqualFold(q(r, "GoEmbyProbe"), "true") {
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	// Caddy overwrites X-Forwarded-For; use its rightmost address only on the local proxy hop.
	peer := net.ParseIP(ip)
	if peer != nil && (peer.IsLoopback() || ip == "172.18.0.1") {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		candidate := strings.TrimSpace(parts[len(parts)-1])
		if net.ParseIP(candidate) != nil {
			ip = candidate
		}
	}
	device := u.Device
	client := r.UserAgent()
	auth := r.Header.Get("X-Emby-Authorization")
	if auth == "" {
		auth = r.Header.Get("Authorization")
	}
	for _, part := range strings.Split(auth, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, "\"")
		if key == "Device" {
			device = value + " (" + u.Device + ")"
		}
		if key == "Emby Client" || key == "Client" {
			client = value
		}
	}
	a.activity.mu.Lock()
	for i := len(a.activity.entries) - 1; i >= 0; i-- {
		entry := a.activity.entries[i]
		if entry.Category == "playback" && entry.State == "requested" && entry.Username == u.Name && entry.ItemID == x.ID && entry.DeviceKey == u.Device && entry.IP == ip && time.Since(entry.Updated) < 180*time.Second {
			entry.Updated = time.Now()
			a.activity.mu.Unlock()
			return
		}
	}
	if len(a.activity.entries) >= 1000 {
		for i, entry := range a.activity.entries {
			if entry.Category == "playback" || entry.State == "complete" || entry.State == "error" {
				a.activity.entries = append(a.activity.entries[:i], a.activity.entries[i+1:]...)
				break
			}
		}
	}
	now := time.Now()
	a.activity.entries = append(a.activity.entries, &activityEntry{ID: id(), Category: "playback", ItemID: x.ID, Name: x.Name, State: "requested", UserID: u.ID, Username: u.Name, IP: ip, Device: device, DeviceKey: u.Device, Client: client, Started: now, Updated: now})
	a.activity.mu.Unlock()
}

func (a *App) updatePlaybackActivity(u User, item string, stopped bool) {
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	for _, entry := range a.activity.entries {
		if entry.Category == "playback" && entry.Username == u.Name && entry.DeviceKey == u.Device && entry.ItemID == item && entry.State == "requested" {
			entry.Updated = time.Now()
			if stopped {
				entry.State = "complete"
			}
		}
	}
}

func (a *App) playbackProgress(u User, item string, position, duration int64) {
	if duration <= 0 {
		var raw string
		if a.db.QueryRow("SELECT data FROM media_probe WHERE item=?", item).Scan(&raw) == nil {
			var metadata struct{ RunTimeTicks int64 }
			json.Unmarshal([]byte(raw), &metadata)
			duration = metadata.RunTimeTicks
		}
	}
	if duration <= 0 {
		if mediaItem, err := a.item(item); err == nil {
			media := M{}
			a.enrichMedia(mediaItem, a.metadata(mediaItem), media)
			switch runtime := media["RunTimeTicks"].(type) {
			case int64:
				duration = runtime
			case float64:
				duration = int64(runtime)
			}
		}
	}
	a.activity.mu.Lock()
	defer a.activity.mu.Unlock()
	for _, entry := range a.activity.entries {
		if entry.Category == "playback" && entry.Username == u.Name && entry.DeviceKey == u.Device && entry.ItemID == item && entry.State == "requested" {
			entry.PositionTicks = max(position, 0)
			if duration > 0 {
				entry.RunTimeTicks = duration
			}
			if entry.RunTimeTicks > 0 {
				entry.Progress = min(100, float64(entry.PositionTicks)*100/float64(entry.RunTimeTicks))
			}
			entry.Updated = time.Now()
		}
	}
}
