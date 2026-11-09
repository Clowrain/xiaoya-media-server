package embyserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleUnifiedLogsCompatAndFiltering(t *testing.T) {
	a := &App{}

	// Clean logger for reproducible test
	sysLogger.clear()

	// Add test entries
	sysLogger.add(&LogEntry{
		ID:         "log-1",
		Time:       time.Now(),
		Level:      "INFO",
		Category:   "http",
		Method:     "GET",
		Path:       "/emby/System/Info",
		Status:     200,
		DurationMs: 1.5,
		Client:     "SenPlayer/2.1",
		IP:         "192.168.1.100",
		Message:    "系统信息查询",
	})

	sysLogger.add(&LogEntry{
		ID:         "log-2",
		Time:       time.Now(),
		Level:      "302",
		Category:   "playback",
		Method:     "GET",
		Path:       "/emby/Videos/123/stream.mp4",
		Status:     302,
		DurationMs: 12.3,
		Client:     "Infuse/7.6",
		IP:         "192.168.1.101",
		Target:     "https://aliyun.example.com/play/123.mp4",
		Message:    "播放 302 重定向成功",
	})

	sysLogger.add(&LogEntry{
		ID:         "log-3",
		Time:       time.Now(),
		Level:      "ERROR",
		Category:   "sync",
		Title:      "小雅增量同步",
		Message:    "清单同步超时",
		Error:      "context deadline exceeded",
	})

	// 1. Test GET /admin/logs with default params
	{
		r := httptest.NewRequest(http.MethodGet, "/admin/logs", nil)
		w := httptest.NewRecorder()
		a.handleUnifiedLogs(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to parse json: %v", err)
		}

		logs, ok := resp["logs"].([]interface{})
		if !ok || len(logs) != 3 {
			t.Fatalf("expected 3 logs in 'logs' key, got %#v", resp["logs"])
		}

		items, ok := resp["Items"].([]interface{})
		if !ok || len(items) != 3 {
			t.Fatalf("expected 3 logs in 'Items' key, got %#v", resp["Items"])
		}

		// Verify last item is latest (log-3) in default ascending chronological order
		last := logs[len(logs)-1].(map[string]interface{})
		if last["level"] != "ERROR" || last["msg"] == "" || last["source"] == "" {
			t.Fatalf("expected populated msg and source, got %#v", last)
		}
	}

	// 2. Test order=desc
	{
		r := httptest.NewRequest(http.MethodGet, "/admin/logs?order=desc", nil)
		w := httptest.NewRecorder()
		a.handleUnifiedLogs(w, r)

		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		logs := resp["logs"].([]interface{})
		first := logs[0].(map[string]interface{})
		if first["id"] != "log-3" {
			t.Fatalf("expected log-3 first with order=desc, got %v", first["id"])
		}
	}

	// 2. Test filter by level=ERROR
	{
		r := httptest.NewRequest(http.MethodGet, "/admin/logs?level=ERROR", nil)
		w := httptest.NewRecorder()
		a.handleUnifiedLogs(w, r)

		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		logs := resp["logs"].([]interface{})
		if len(logs) != 1 {
			t.Fatalf("expected 1 error log, got %d", len(logs))
		}
	}

	// 3. Test filter by source=playback
	{
		r := httptest.NewRequest(http.MethodGet, "/admin/logs?source=playback", nil)
		w := httptest.NewRecorder()
		a.handleUnifiedLogs(w, r)

		var resp map[string]interface{}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		logs := resp["logs"].([]interface{})
		if len(logs) != 1 {
			t.Fatalf("expected 1 playback log, got %d", len(logs))
		}
	}

	// 4. Test DELETE /admin/logs
	{
		r := httptest.NewRequest(http.MethodDelete, "/admin/logs", nil)
		w := httptest.NewRecorder()
		a.handleUnifiedLogs(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on delete, got %d", w.Code)
		}

		if sysLogger.count() != 0 {
			t.Fatalf("expected logger empty after clear, got %d", sysLogger.count())
		}
	}
}

func TestDetectClientInfo(t *testing.T) {
	// 1. From X-Emby-Authorization header
	r1 := httptest.NewRequest("GET", "/emby/Items", nil)
	r1.Header.Set("X-Emby-Authorization", `MediaBrowser Client="SenPlayer", Device="iPhone", DeviceId="123", Version="2.1.0"`)
	if got := detectClientInfo(r1); got != "SenPlayer" {
		t.Fatalf("expected SenPlayer, got %s", got)
	}

	// 2. From X-Emby-Client header
	r2 := httptest.NewRequest("GET", "/emby/Items", nil)
	r2.Header.Set("X-Emby-Client", "VidHub")
	if got := detectClientInfo(r2); got != "VidHub" {
		t.Fatalf("expected VidHub, got %s", got)
	}

	// 3. From User-Agent containing SenPlayer
	r3 := httptest.NewRequest("GET", "/emby/Items", nil)
	r3.Header.Set("User-Agent", "SenPlayer/2.1.0 (iOS 17.5)")
	if got := detectClientInfo(r3); got != "SenPlayer" {
		t.Fatalf("expected SenPlayer from UA, got %s", got)
	}
}
