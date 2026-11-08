package strm

import (
	"path/filepath"
	"testing"
)

func TestMapStandardEpisodePathFlatNumeric(t *testing.T) {
	root := t.TempDir()
	path, ok, err := MapStandardEpisodePath(root, "/每日更新/电视剧/国产剧/折月亮/01.strm", "", true, 235, NewCollisionTracker())
	if err != nil || !ok {
		t.Fatalf("map flat episode: path=%q ok=%v err=%v", path, ok, err)
	}
	want := filepath.Join(root, "每日更新", "电视剧", "国产剧", "折月亮", "Season 1", "折月亮 - S01E01.strm")
	if path != want {
		t.Fatalf("path=%q, want %q", path, want)
	}
}

func TestMapStandardEpisodePathMovieUnchanged(t *testing.T) {
	path, ok, err := MapStandardEpisodePath(t.TempDir(), "/每日更新/电影/中国/八仙！ (2026)/八仙！ (2026).strm", "", true, 235, NewCollisionTracker())
	if err != nil || ok || path != "" {
		t.Fatalf("movie should remain on movie mapper: path=%q ok=%v err=%v", path, ok, err)
	}
}

func TestMapStandardEpisodePathDoesNotClassifyMovieNumericFile(t *testing.T) {
	path, ok, err := MapStandardEpisodePath(t.TempDir(), "/每日更新/电影/中国/一部电影/01.strm", "", true, 235, NewCollisionTracker())
	if err != nil || ok || path != "" {
		t.Fatalf("numeric movie should remain movie layout: path=%q ok=%v err=%v", path, ok, err)
	}
}

func TestMapStandardEpisodePathPreservesSeasonZero(t *testing.T) {
	root := t.TempDir()
	path, ok, err := MapStandardEpisodePath(root, "/综艺/节目/Season 0/E01.strm", "", true, 235, NewCollisionTracker())
	if err != nil || !ok {
		t.Fatalf("map season zero: path=%q ok=%v err=%v", path, ok, err)
	}
	if want := filepath.Join(root, "综艺", "节目", "Season 0", "节目 - S00E01.strm"); path != want {
		t.Fatalf("path=%q, want %q", path, want)
	}
}
