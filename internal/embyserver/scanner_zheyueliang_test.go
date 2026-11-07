package embyserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/metadata/tmdb"
)

func TestIsScannableMediaFileAll21Extensions(t *testing.T) {
	exts := []string{
		".strm", ".STRM",
		".mp4", ".m4v", ".mkv", ".iso", ".flv", ".avi", ".ts", ".wmv", ".mov", ".rm", ".rmvb", ".webm",
		".wav", ".mp3", ".flac", ".ape", ".wv", ".alac", ".m4a", ".aac", ".wma",
		".M4V", ".MKV", ".RMVB",
	}
	for _, ext := range exts {
		if !isScannableMediaFile("/media/test/video" + ext) {
			t.Errorf("expected %s to be scannable media file", ext)
		}
	}
	nonMedia := []string{".nfo", ".srt", ".ass", ".jpg", ".png", ".webp", ".txt", ""}
	for _, ext := range nonMedia {
		if isScannableMediaFile("/media/test/file" + ext) {
			t.Errorf("expected %s NOT to be scannable media file", ext)
		}
	}
}

func TestZheYueLiangNestedTagSeriesLayout(t *testing.T) {
	tmp := t.TempDir()
	RegisterMediaRoot(tmp)

	libRoot := filepath.Join(tmp, "每日更新", "电视剧", "国产剧")
	seriesDir := filepath.Join(libRoot, "折月亮 (2026)")
	tagDir := filepath.Join(seriesDir, "{tmdb-270431}")
	seasonDir := filepath.Join(tagDir, "Season 1")
	if err := os.MkdirAll(seasonDir, 0o755); err != nil {
		t.Fatalf("mkdir seasonDir: %v", err)
	}

	for i := 1; i <= 27; i++ {
		epPath := filepath.Join(seasonDir, fmt.Sprintf("E%02d.strm", i))
		if err := os.WriteFile(epPath, []byte(fmt.Sprintf("http://xiaoya.host:5678/d/test/E%02d.mp4\n", i)), 0o644); err != nil {
			t.Fatalf("write E%02d.strm: %v", i, err)
		}
		seasonNum, epNum, ok := tmdb.ParseSeasonEpisode(fmt.Sprintf("E%02d", i), 1)
		if !ok || seasonNum != 1 || epNum != i {
			t.Fatalf("E%02d parsed as S%02dE%02d (ok=%v), want S01E%02d", i, seasonNum, epNum, ok, i)
		}
	}

	// seriesDirectory must step past {tmdb-270431} and resolve to 折月亮 (2026)
	gotSeries := seriesDirectory(libRoot, seasonDir)
	if gotSeries != seriesDir {
		t.Fatalf("seriesDirectory() = %q, want %q", gotSeries, seriesDir)
	}

	title, year := tmdb.ParseTitleAndYear(filepath.Base(gotSeries))
	if title != "折月亮" || year != 2026 {
		t.Fatalf("ParseTitleAndYear(%q) = (%q, %d), want (折月亮, 2026)", filepath.Base(gotSeries), title, year)
	}

	// season01-poster.jpg placed in seriesDir should be found even across {tmdb-270431}
	posterFile := filepath.Join(seriesDir, "season01-poster.jpg")
	if err := os.WriteFile(posterFile, []byte("fake-jpg"), 0o644); err != nil {
		t.Fatalf("write poster: %v", err)
	}
	if gotPoster := seasonPoster(seasonDir, 1); gotPoster != posterFile {
		t.Fatalf("seasonPoster() = %q, want %q", gotPoster, posterFile)
	}
	if gotFallback := seriesFallbackPoster(seriesDir); gotFallback != posterFile {
		t.Fatalf("seriesFallbackPoster() = %q, want %q", gotFallback, posterFile)
	}

	// App TMDB ID resolution from immediate {tmdb-270431} child directory
	app := &App{}
	seriesItem := Item{ID: "series-1", Kind: "Series", Name: "折月亮", Year: 2026, Path: seriesDir}
	if id := app.findItemTMDBID(seriesItem); id != "270431" {
		t.Fatalf("findItemTMDBID(series) = %q, want 270431", id)
	}
}

func TestNFOPreferPolicyAndEmptyNFORejection(t *testing.T) {
	tmp := t.TempDir()
	RegisterMediaRoot(tmp)

	seriesDir := filepath.Join(tmp, "折月亮 (2026)")
	tagDir := filepath.Join(seriesDir, "{tmdb-270431}")
	if err := os.MkdirAll(tagDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// 1. Empty tvshow.nfo MUST be rejected (not treated as valid local NFO)
	nfoPath := filepath.Join(tagDir, "tvshow.nfo")
	if err := os.WriteFile(nfoPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write empty nfo: %v", err)
	}
	seriesItem := Item{ID: "series-1", Kind: "Series", Name: "折月亮", Year: 2026, Path: seriesDir}
	if _, ok := readSidecarStatus(seriesItem); ok {
		t.Fatalf("expected empty tvshow.nfo to be rejected")
	}

	// Mock TMDB server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/3/tv/270431" {
			_, _ = w.Write([]byte(`{"id":270431,"name":"折月亮 (TMDB)","first_air_date":"2026-05-01","overview":"TMDB 简介","poster_path":"/p.jpg"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Metadata.TMDB.Enabled = true
	cfg.Metadata.TMDB.APIKey = "test-key"
	cfg.Metadata.TMDB.BaseURL = srv.URL + "/3"
	cfg.Metadata.TMDB.ImageURL = "https://image.tmdb.org/t/p"
	app := &App{cfg: cfg}

	// Since tvshow.nfo is empty, resolveItemSidecar falls back to TMDB scrape
	sc := app.resolveItemSidecar(seriesItem, true)
	if sc.Title != "折月亮 (TMDB)" || sc.TMDB != "270431" || sc.Plot != "TMDB 简介" {
		t.Fatalf("expected TMDB fallback when NFO is empty, got %+v", sc)
	}

	// 2. Valid non-empty tvshow.nfo MUST win (`prefer` policy) over TMDB
	validNFO := `<?xml version="1.0" encoding="UTF-8"?>
<tvshow>
  <title>折月亮（本地NFO优先）</title>
  <plot>来自本地 NFO 的剧情简介</plot>
  <year>2026</year>
  <tmdbid>270431</tmdbid>
</tvshow>`
	if err := os.WriteFile(nfoPath, []byte(validNFO), 0o644); err != nil {
		t.Fatalf("write valid nfo: %v", err)
	}

	scPrefer := app.resolveItemSidecar(seriesItem, true)
	if scPrefer.Title != "折月亮（本地NFO优先）" || scPrefer.Plot != "来自本地 NFO 的剧情简介" {
		t.Fatalf("expected local NFO to win under prefer policy, got %+v", scPrefer)
	}
}
