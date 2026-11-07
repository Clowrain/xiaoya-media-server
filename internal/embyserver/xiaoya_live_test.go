package embyserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"xiaoya-media-server/internal/resource/alist"
	"xiaoya-media-server/internal/strm"
)

// 显式指定服务与资源才访问真实网盘；普通回归不依赖账号或外网。
func TestXiaoyaLivePlayback(t *testing.T) {
	base, path := os.Getenv("LIVE_XIAOYA_URL"), os.Getenv("LIVE_XIAOYA_PATH")
	if base == "" || path == "" {
		t.Skip("设置 LIVE_XIAOYA_URL 和 LIVE_XIAOYA_PATH 后运行实机播放检查")
	}
	client := alist.NewClient(alist.Options{
		BaseURL: base, Username: os.Getenv("XIAOYA_USERNAME"),
		Password: os.Getenv("XIAOYA_PASSWORD"), Token: os.Getenv("ALIST_TOKEN"),
	})
	a := &App{}
	a.SetAListPlayback(client.BaseURL(), client.PlaybackFile)
	raw, err := strm.BuildSTRMURL(path, "", "http://xiaoya.host:5678", false)
	if err != nil {
		t.Fatal("无法生成样本 STRM")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	r := httptest.NewRequest("GET", "/Videos/live/stream", nil).WithContext(ctx)
	r.Header.Set("User-Agent", "SenPlayer/6.1.8")
	link, err := a.resolvePlaybackLink(r, raw, base)
	if err != nil {
		// 网络错误可能携带签名地址，实机结果只输出阶段与错误类型。
		t.Fatalf("真实资源解析失败 (%T)，请检查 AList 账号权限及服务连通性", err)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal("解析结果不是合法 URL")
	}
	t.Logf("解析成功，播放主机=%s", u.Hostname())
	httpClient := &http.Client{Timeout: 20 * time.Second}
	for _, offset := range []int64{0, 1 << 20} {
		req, err := http.NewRequestWithContext(ctx, "GET", link, nil)
		if err != nil {
			t.Fatal("无法创建媒体分段请求")
		}
		req.Header.Set("User-Agent", r.UserAgent())
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+15))
		res, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("媒体分段请求失败 (%T)，offset=%d", err, offset)
		}
		data, readErr := io.ReadAll(io.LimitReader(res.Body, 17))
		res.Body.Close()
		wantRange := fmt.Sprintf("bytes %d-%d/", offset, offset+15)
		if readErr != nil || res.StatusCode != 206 || len(data) != 16 || !strings.HasPrefix(res.Header.Get("Content-Range"), wantRange) {
			t.Fatalf("分段读取失败: status=%d bytes=%d offset=%d", res.StatusCode, len(data), offset)
		}
		t.Logf("Range offset=%d: HTTP 206，读取 16 字节", offset)
	}
}
