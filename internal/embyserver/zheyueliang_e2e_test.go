package embyserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/metadata/tmdb"
	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/resource/xiaoya"
	"xiaoya-media-server/internal/strm"
)

type staticTransport func(*http.Request) (*http.Response, error)

func (f staticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// TestZheYueLiang14StepEndToEnd executes all 14 verification steps defined in V1.0 Spec Section 11.5
// against the authentic 30-node `折月亮 (2026)` SQLite tree (IDs 568922..641745, TMDB 270431, Season 1, E01..E27).
func TestZheYueLiang14StepEndToEnd(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	strmRoot := filepath.Join(tmp, "media-strm")
	RegisterMediaRoot(strmRoot)

	// Prepare Xiaoya strm_internal.db with exact V1.0 Spec Section 3.1 IDs and structure
	xiaoyaDBPath := filepath.Join(tmp, "strm_internal.db")
	rwXiaoya, err := sql.Open("sqlite", xiaoyaDBPath)
	if err != nil {
		t.Fatalf("open rw xiaoya sqlite: %v", err)
	}
	defer rwXiaoya.Close()

	if _, err := rwXiaoya.Exec(`
		PRAGMA journal_mode=WAL;
		CREATE TABLE nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			parent_id INTEGER NOT NULL DEFAULT 0,
			is_dir BOOLEAN NOT NULL DEFAULT 0,
			content TEXT NOT NULL DEFAULT ''
		);
	`); err != nil {
		t.Fatalf("create nodes table: %v", err)
	}

	insertNode := func(id int64, name string, parentID int64, isDir bool, content string) {
		flag := 0
		if isDir {
			flag = 1
		}
		if _, iErr := rwXiaoya.Exec("INSERT INTO nodes(id, name, parent_id, is_dir, content) VALUES(?, ?, ?, ?, ?)", id, name, parentID, flag, content); iErr != nil {
			t.Fatalf("insert node %d (%s): %v", id, name, iErr)
		}
	}

	insertNode(568922, "每日更新", 0, true, "")
	insertNode(617762, "电视剧", 568922, true, "")
	insertNode(639780, "国产剧", 617762, true, "")
	insertNode(641716, "折月亮 (2026)", 639780, true, "")
	insertNode(641717, "{tmdb-270431}", 641716, true, "")
	insertNode(641718, "Season 1", 641717, true, "")
	for ep := 1; ep <= 27; ep++ {
		nodeID := int64(641718 + ep) // 641719..641745
		epName := fmt.Sprintf("E%02d", ep)
		epURL := fmt.Sprintf("http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/%02d.mp4?sign=INIT_SIGN_%02d:0", ep, ep)
		insertNode(nodeID, epName, 641718, false, epURL)
	}

	// Open strictly read-only Xiaoya SQLite reader
	roReader, err := xiaoya.OpenReader(xiaoyaDBPath)
	if err != nil {
		t.Fatalf("OpenReader: %v", err)
	}
	defer roReader.Close()
	if err := roReader.VerifyReadOnly(ctx); err != nil {
		t.Fatalf("VerifyReadOnly: %v", err)
	}

	// Open business store
	bizDB, err := sql.Open("sqlite", filepath.Join(tmp, "business.db"))
	if err != nil {
		t.Fatalf("open business db: %v", err)
	}
	defer bizDB.Close()
	store := discovery.NewSQLStore(bizDB, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// Mock attachment HTTP transport for Step 07
	attClient := &http.Client{
		Transport: staticTransport(func(r *http.Request) (*http.Response, error) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/poster.jpg"):
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("JPEG_POSTER_DATA")), Header: make(http.Header)}, nil
			case strings.HasSuffix(r.URL.Path, "/E01.chs.srt"):
				srt := "1\r\n00:00:02,100 --> 00:00:05,800\r\n《折月亮》第一集字幕\r\n"
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(srt)), Header: make(http.Header)}, nil
			default:
				return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header)}, nil
			}
		}),
	}

	strmEng := strm.NewEngine(strm.Config{
		OutputRoot:              strmRoot,
		PreserveSourceStructure: true,
		MaxPathLength:           235,
		AttachmentsMode:         "full",
		TargetBaseURL:           "http://xiaoya.host:5678",
		DownloadBaseURL:         "http://xiaoya.host:5678",
		HTTPClient:              attClient,
		AttachmentStore:         store,
	})
	discSvc := discovery.NewService(store, roReader, strmEng)

	// =========================================================================
	// Step 01: Configure monitor root `/每日更新/电视剧/国产剧` and verify exact path matching
	// =========================================================================
	if _, missErr := roReader.ResolvePath(ctx, "/每日更新/电视剧/国产剧/折月亮"); !errors.Is(missErr, xiaoya.ErrPathNotFound) {
		t.Fatalf("Step 01: expected un-suffixed '/折月亮' to return ErrPathNotFound, got %v", missErr)
	}
	roots, err := discSvc.RegisterMonitorRoots(ctx, []string{"/每日更新/电视剧/国产剧"}, 10*time.Minute)
	if err != nil || len(roots) != 1 {
		t.Fatalf("Step 01: RegisterMonitorRoots failed: %v", err)
	}
	rootID := roots[0].ID

	// =========================================================================
	// Step 02 & Step 05: Recursive read of 30 nodes (3 dirs, 27 files) & STRM generation
	// =========================================================================
	batch1, err := discSvc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("Step 02: ScanRootFull failed: %v", err)
	}
	if !batch1.IsBaseline || batch1.TotalSeen != 30 || batch1.AddedCount != 30 || batch1.FailedCount != 0 {
		t.Fatalf("Step 02: expected 30 baseline nodes with 0 failures, got %+v", batch1)
	}

	// =========================================================================
	// Step 03, 04, 06: Verify TMDB ID 270431, Season 1 / E01..E27, local paths & STRM URLs
	// =========================================================================
	seriesLocalDir := filepath.Join(strmRoot, "每日更新", "电视剧", "国产剧", "折月亮 (2026)")
	seasonLocalDir := filepath.Join(seriesLocalDir, "{tmdb-270431}", "Season 1")
	ep01Path := filepath.Join(seasonLocalDir, "E01.strm")
	ep27Path := filepath.Join(seasonLocalDir, "E27.strm")

	if gotTMDB := tmdb.ExtractTMDBID(seasonLocalDir); gotTMDB != "270431" {
		t.Fatalf("Step 03: expected TMDB ID 270431, got %q", gotTMDB)
	}
	for ep := 1; ep <= 27; ep++ {
		p := filepath.Join(seasonLocalDir, fmt.Sprintf("E%02d.strm", ep))
		raw, rErr := os.ReadFile(p)
		if rErr != nil {
			t.Fatalf("Step 05/06: missing E%02d.strm at %s: %v", ep, p, rErr)
		}
		wantURL := fmt.Sprintf("http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/%02d.mp4?sign=INIT_SIGN_%02d:0", ep, ep)
		if string(raw) != wantURL {
			t.Fatalf("Step 06: E%02d.strm URL = %q, want %q", ep, string(raw), wantURL)
		}
		sNum, eNum, ok := tmdb.ParseSeasonEpisode(fmt.Sprintf("E%02d", ep), 1)
		if !ok || sNum != 1 || eNum != ep {
			t.Fatalf("Step 04: E%02d parsed as S%02dE%02d", ep, sNum, eNum)
		}
	}

	// =========================================================================
	// Step 07: Companion subtitle & poster sync + verify no empty NFO created
	// =========================================================================
	posterPath, err := strmEng.SyncAttachment(ctx, "res-poster", strmRoot,
		"/每日更新/电视剧/国产剧/折月亮 (2026)/poster.jpg",
		"折月亮 (2026)/poster.jpg", "", strm.CategoryImage)
	if err != nil {
		t.Fatalf("Step 07: poster sync failed: %v", err)
	}
	subPath, err := strmEng.SyncAttachment(ctx, "res-sub01", strmRoot,
		"/每日更新/电视剧/国产剧/折月亮 (2026)/{tmdb-270431}/Season 1/E01.chs.srt",
		"折月亮 (2026)/{tmdb-270431}/Season 1/E01.chs.srt", "", strm.CategorySubtitle)
	if err != nil {
		t.Fatalf("Step 07: subtitle sync failed: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(seriesLocalDir, "tvshow.nfo")); !os.IsNotExist(statErr) {
		t.Fatalf("Step 07: must NOT create placeholder empty tvshow.nfo when source has no NFO")
	}
	if _, statErr := os.Stat(posterPath); statErr != nil {
		t.Fatalf("Step 07: poster missing at %s", posterPath)
	}
	if _, statErr := os.Stat(subPath); statErr != nil {
		t.Fatalf("Step 07: subtitle missing at %s", subPath)
	}

	// =========================================================================
	// Step 08 & Step 09: Series/Season/Episode hierarchy & TMDB 270431 scraping
	// =========================================================================
	libRoot := filepath.Join(strmRoot, "每日更新", "电视剧", "国产剧")
	resolvedSeriesDir := seriesDirectory(libRoot, ep01Path)
	if resolvedSeriesDir != seriesLocalDir {
		t.Fatalf("Step 08: seriesDirectory = %q, want %q", resolvedSeriesDir, seriesLocalDir)
	}

	mockTMDB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/tv/270431":
			_, _ = w.Write([]byte(`{
				"id": 270431,
				"name": "折月亮",
				"original_name": "折月亮",
				"first_air_date": "2026-06-01",
				"overview": "云厘与傅识则跨越青春与重逢的治愈爱情故事。",
				"vote_average": 8.4,
				"poster_path": "/zheyueliang_poster.jpg",
				"backdrop_path": "/zheyueliang_backdrop.jpg",
				"genres": [{"id": 18, "name": "剧情"}]
			}`))
		case "/3/tv/270431/season/1":
			_, _ = w.Write([]byte(`{
				"id": 9001,
				"name": "第 1 季",
				"season_number": 1,
				"episodes": [
					{"episode_number": 1, "name": "第 1 集：初遇南芜", "overview": "云厘初识傅识则。", "runtime": 45, "still_path": "/ep01.jpg"},
					{"episode_number": 2, "name": "第 2 集：月亮折痕", "overview": "两人再次相遇。", "runtime": 45, "still_path": "/ep02.jpg"}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockTMDB.Close()

	cfg := config.Default()
	cfg.STRM.OutputRoot = strmRoot
	cfg.Metadata.TMDB.Enabled = true
	cfg.Metadata.TMDB.APIKey = "mock-tmdb-key"
	cfg.Metadata.TMDB.BaseURL = mockTMDB.URL + "/3"
	app := &App{cfg: cfg}

	seriesItem := Item{ID: "ser-zyl", Kind: "Series", Name: "折月亮", Year: 2026, Path: seriesLocalDir}
	seriesMeta := app.resolveItemSidecar(seriesItem, true)
	if seriesMeta.TMDB != "270431" || seriesMeta.Title != "折月亮" || !strings.Contains(seriesMeta.Plot, "傅识则") {
		t.Fatalf("Step 09: unexpected TMDB 270431 series metadata: %+v", seriesMeta)
	}

	ep01Item := Item{ID: "ep-01", Kind: "Episode", Name: "E01", Season: 1, Episode: 1, Path: ep01Path, URL: "http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/01.mp4?sign=INIT_SIGN_01:0"}
	ep01Meta := app.resolveItemSidecar(ep01Item, true)
	if ep01Meta.Title != "第 1 集：初遇南芜" || ep01Meta.Runtime != 45 {
		t.Fatalf("Step 09: unexpected TMDB Episode 1 metadata: %+v", ep01Meta)
	}

	// =========================================================================
	// Step 10: Client playback redirect, external subtitle WebVTT, and NextUp progression
	// =========================================================================
	mockXiaoya := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://cdn-media.example/zheyueliang/01.mp4?token=ok")
		w.WriteHeader(http.StatusFound)
	}))
	defer mockXiaoya.Close()

	playReq := httptest.NewRequest(http.MethodGet, "/Videos/ep-01/stream.mp4", nil)
	resolvedCDN, err := xiaoyaLinkWithToken(playReq, ep01Item.URL, mockXiaoya.URL, "")
	if err != nil || resolvedCDN != "https://cdn-media.example/zheyueliang/01.mp4?token=ok" {
		t.Fatalf("Step 10: playback resolution failed: link=%q err=%v", resolvedCDN, err)
	}

	extSubs := discoverExternalSubtitles(ep01Item)
	if len(extSubs) != 1 || extSubs[0].Language != "zh-CN" {
		t.Fatalf("Step 10: expected 1 zh-CN external subtitle for E01, got %+v", extSubs)
	}
	rawSubBytes, _ := os.ReadFile(extSubs[0].Path)
	vttBytes := srtToWebVTT(rawSubBytes)
	if !strings.Contains(string(vttBytes), "00:00:02.100 --> 00:00:05.800") {
		t.Fatalf("Step 10: WebVTT subtitle conversion failed:\n%s", string(vttBytes))
	}

	// Simulate user finishing E01 -> NextUp must return E02
	nextUpList := computeNextUpPerSeries([]nextUpCandidate{
		{Item: ep01Item, SeriesID: "ser-zyl", Played: true, LastActivity: 1000},
		{Item: Item{ID: "ep-02", Kind: "Episode", Name: "E02", Season: 1, Episode: 2}, SeriesID: "ser-zyl", Played: false},
	}, "")
	if len(nextUpList) != 1 || nextUpList[0].ID != "ep-02" {
		t.Fatalf("Step 10: expected NextUp to return ep-02 after E01 played, got %+v", nextUpList)
	}

	// =========================================================================
	// Step 11: Rescan idempotency (zero duplicate entities, zero unnecessary STRM rewrites)
	// =========================================================================
	pastTime := time.Now().Add(-5 * time.Minute).Truncate(time.Second)
	_ = os.Chtimes(ep01Path, pastTime, pastTime)
	_ = os.Chtimes(ep27Path, pastTime, pastTime)
	st01Before, _ := os.Stat(ep01Path)

	batch2, err := discSvc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("Step 11: second ScanRootFull failed: %v", err)
	}
	st01After, _ := os.Stat(ep01Path)
	if batch2.AddedCount != 0 || batch2.ModifiedCount != 0 || !st01After.ModTime().Equal(st01Before.ModTime()) {
		t.Fatalf("Step 11: rescan was not idempotent: batch=%+v mtimeBefore=%v mtimeAfter=%v", batch2, st01Before.ModTime(), st01After.ModTime())
	}

	// =========================================================================
	// Step 12: Simulate newly added episode E28 (ID 641746) -> automatic incremental ingest
	// =========================================================================
	ep28URL := "http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/28.mp4?sign=INIT_SIGN_28:0"
	insertNode(641746, "E28", 641718, false, ep28URL)

	batch3, err := discSvc.ScanRootIncremental(ctx, rootID)
	if err != nil {
		t.Fatalf("Step 12: ScanRootIncremental for E28 failed: %v", err)
	}
	ep28Path := filepath.Join(seasonLocalDir, "E28.strm")
	ep28Bytes, err := os.ReadFile(ep28Path)
	if err != nil || batch3.AddedCount != 1 || string(ep28Bytes) != ep28URL {
		t.Fatalf("Step 12: E28 incremental generation failed: batch=%+v content=%q err=%v", batch3, string(ep28Bytes), err)
	}

	// =========================================================================
	// Step 13: Simulate E01 `content` URL change -> atomic update of E01.strm
	// =========================================================================
	newEp01URL := "http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/01_remastered_4k.mp4?sign=NEW_4K_SIGN:0"
	if _, err := rwXiaoya.Exec("UPDATE nodes SET content = ? WHERE id = 641719", newEp01URL); err != nil {
		t.Fatalf("Step 13: update SQLite E01 content: %v", err)
	}
	batch4, err := discSvc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("Step 13: ScanRootFull after E01 URL update failed: %v", err)
	}
	updatedEp01Bytes, _ := os.ReadFile(ep01Path)
	if batch4.ModifiedCount != 1 || string(updatedEp01Bytes) != newEp01URL {
		t.Fatalf("Step 13: E01.strm not updated: batch=%+v got=%q want=%q", batch4, string(updatedEp01Bytes), newEp01URL)
	}

	// =========================================================================
	// Step 14: Simulate E28 disappearance -> mark MISSING while keeping local STRM/attachments (`keep` policy)
	// =========================================================================
	if _, err := rwXiaoya.Exec("DELETE FROM nodes WHERE id = 641746"); err != nil {
		t.Fatalf("Step 14: delete E28 from SQLite: %v", err)
	}
	batch5, err := discSvc.ScanRootFull(ctx, rootID)
	if err != nil {
		t.Fatalf("Step 14: ScanRootFull after E28 removal failed: %v", err)
	}
	ep28Res, found, err := store.GetResourceBySourceKey(ctx, "sqlite:641746")
	if err != nil || !found || batch5.MissingCount != 1 || ep28Res.Status != discovery.StatusMissing {
		t.Fatalf("Step 14: expected E28 marked MISSING, got batch=%+v res=%+v err=%v", batch5, ep28Res, err)
	}
	if _, statErr := os.Stat(ep28Path); statErr != nil {
		t.Fatalf("Step 14 (`keep` policy): local E28.strm MUST be preserved when source disappears, statErr=%v", statErr)
	}
	if _, statErr := os.Stat(subPath); statErr != nil {
		t.Fatalf("Step 14 (`keep` policy): companion subtitle MUST be preserved, statErr=%v", statErr)
	}
}
