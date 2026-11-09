package embyserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSTRMMediaInfoPreservesRawContentAndKeepsPlaybackGateway(t *testing.T) {
	a := &App{}

	// 模拟已入库的 STRM 项目
	rawSTRM := "http://xiaoya.host:5678/d/每日更新/电视剧/泰剧/午夜规则/S01E01.mp4"
	item := Item{
		ID:       "test-strm-item",
		Lib:      "lib1",
		Name:     "午夜规则 S01E01",
		Kind:     "Episode",
		Path:     "/data/xiaoya/每日更新/泰剧/午夜规则/S01E01.strm",
		URL:      rawSTRM,
		Overview: "剧情介绍",
		Season:   1,
		Episode:  1,
	}

	req := httptest.NewRequest("GET", "http://server:7799/Items/"+item.ID+"?api_key=test-token", nil)
	u := User{ID: "viewer"}

	// 1. 验证 viewerDTO（客户端如 SenPlayer 打开详情页，获取媒体信息）
	dto := a.viewerDTO(item, req, u)

	// 根级别 Path 字段应保留原始 STRM 内容
	if dto["Path"] != rawSTRM {
		t.Errorf("条目 Path 字段未保留原始 STRM 内容: got %v, want %v", dto["Path"], rawSTRM)
	}

	// MediaSources[0].Path 应为原始 STRM 内容（SenPlayer 底部媒体信息展示字段）
	sources, ok := dto["MediaSources"].([]M)
	if !ok || len(sources) == 0 {
		t.Fatalf("未找到 MediaSources: %v", dto["MediaSources"])
	}
	src0 := sources[0]
	if src0["Path"] != rawSTRM {
		t.Errorf("MediaSources[0].Path 未保留原始 STRM 内容: got %v, want %v", src0["Path"], rawSTRM)
	}

	// DirectStreamUrl 必须保留作为服务端流网关
	directURL, _ := src0["DirectStreamUrl"].(string)
	if !strings.Contains(directURL, "/Videos/"+item.ID+"/stream.") {
		t.Errorf("DirectStreamUrl 未正确指向本服务流网关: got %v", directURL)
	}

	// 2. 验证 versionSources（PlaybackInfo 接口底层调用的核心函数）
	vSources := a.versionSources(item, req, u, false)
	if len(vSources) == 0 {
		t.Fatalf("versionSources 返回为空")
	}
	if vSources[0]["Path"] != rawSTRM {
		t.Errorf("versionSources MediaSources[0].Path 未保留原始 STRM 内容: got %v, want %v", vSources[0]["Path"], rawSTRM)
	}

	// 3. 验证本地文件依然使用服务网关流地址，并隐藏本地物理存储路径
	localItem := Item{
		ID:   "local-item",
		Name: "本地电影",
		Kind: "Movie",
		Path: "/vol1/storage/movies/test.mp4",
		URL:  "file:///vol1/storage/movies/test.mp4",
	}
	localDTO := a.viewerDTO(localItem, req, u)
	if localDTO["Path"] != nil {
		t.Errorf("本地文件非 API 用户不应暴露本地物理路径: got %v", localDTO["Path"])
	}
	localSrc := a.viewerSource(localItem, req, u)
	if !strings.Contains(localSrc["Path"].(string), "server:7799/emby/Videos/local-item/stream.") {
		t.Errorf("本地文件应走本服务网关出流: got %v", localSrc["Path"])
	}

	// 4. 验证上次修好的直链解析与 302 出流逻辑依然完整运作：请求流网关时能正常解析并出流
	fakeXiaoya := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://cdn.example.com/resolved_video.mp4")
		w.WriteHeader(302)
	}))
	defer fakeXiaoya.Close()

	reqStream := httptest.NewRequest("GET", "/emby/Videos/"+item.ID+"/stream.mp4?api_key=test-token", nil)
	wStream := httptest.NewRecorder()
	a.resolveSTRM(wStream, reqStream, item, fakeXiaoya.URL)

	if wStream.Code != 302 {
		t.Fatalf("流网关应成功 302 重定向出流: status=%d body=%s", wStream.Code, wStream.Body.String())
	}
	if wStream.Header().Get("Location") != "https://cdn.example.com/resolved_video.mp4" {
		t.Fatalf("出流地址不匹配: got %v", wStream.Header().Get("Location"))
	}
}

func TestSTRMMediaInfoCleansTransientSignForClientDisplay(t *testing.T) {
	a := &App{}

	signedSTRM := "http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/走马上任/01.mp4?sign=SIGN_STR"
	wantClean := "http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/走马上任/01.mp4"

	item := Item{
		ID:      "zmsr-ep1",
		Lib:     "lib1",
		Name:    "走马上任 E01",
		Kind:    "Episode",
		Path:    "/data/xiaoya/每日更新/电视剧/国产剧/走马上任/01.strm",
		URL:     signedSTRM,
		Season:  1,
		Episode: 1,
	}

	req := httptest.NewRequest("GET", "http://server:7799/Items/"+item.ID+"?api_key=test-token", nil)
	u := User{ID: "viewer"}

	dto := a.viewerDTO(item, req, u)
	if dto["Path"] != wantClean {
		t.Fatalf("viewerDTO Path 应净化掉 sign: got %v, want %v", dto["Path"], wantClean)
	}

	sources, ok := dto["MediaSources"].([]M)
	if !ok || len(sources) == 0 {
		t.Fatalf("未找到 MediaSources")
	}
	if sources[0]["Path"] != wantClean {
		t.Fatalf("MediaSources[0].Path 应净化掉 sign: got %v, want %v", sources[0]["Path"], wantClean)
	}

	vSources := a.versionSources(item, req, u, false)
	if len(vSources) == 0 || vSources[0]["Path"] != wantClean {
		t.Fatalf("versionSources[0].Path 应净化掉 sign: got %v, want %v", vSources[0]["Path"], wantClean)
	}
}
