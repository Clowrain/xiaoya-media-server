package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xiaoya-media-server/internal/metadata/tmdb"
	"xiaoya-media-server/internal/resource/discovery"
)

func TestPublishMatchedMovieArtifacts(t *testing.T) {
	dir := t.TempDir()
	strmPath := filepath.Join(dir, "八仙！ (2026).strm")
	if err := os.WriteFile(strmPath, []byte("http://example/video"), 0644); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{}
	err := p.publishMatchedArtifacts(context.Background(), discovery.MediaResource{LocalSTRMPath: strmPath}, discovery.ParsedMediaIdentity{MediaType: "MOVIE"}, &tmdb.MediaMetadata{Title: "八仙！", OriginalTitle: "The Eight Immortals", Overview: "剧情", Year: 2026}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"movie.nfo", "八仙！ (2026).nfo", "八仙！ (2026)-poster.jpg"} {
		if name == "八仙！ (2026)-poster.jpg" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "movie.nfo"))
	if err != nil || !strings.Contains(string(b), "<title>八仙！</title>") {
		t.Fatalf("movie nfo missing title: %s (%v)", b, err)
	}
}

func TestPublishMatchedEpisodeArtifacts(t *testing.T) {
	dir := t.TempDir()
	seasonDir := filepath.Join(dir, "折月亮 (2026)", "Season 1")
	if err := os.MkdirAll(seasonDir, 0755); err != nil {
		t.Fatal(err)
	}
	strmPath := filepath.Join(seasonDir, "折月亮 - S01E01.strm")
	if err := os.WriteFile(strmPath, []byte("http://example/video"), 0644); err != nil {
		t.Fatal(err)
	}
	p := &Pipeline{}
	identity := discovery.ParsedMediaIdentity{MediaType: "EPISODE", Title: "折月亮", SeasonNumber: 1, EpisodeNumber: 1}
	err := p.publishMatchedArtifacts(context.Background(), discovery.MediaResource{LocalSTRMPath: strmPath}, identity,
		&tmdb.MediaMetadata{Title: "折月亮", OriginalTitle: "Moon", Overview: "剧集", Year: 2026},
		&tmdb.MediaMetadata{Title: "雨伞杀手竟是白月光", Season: 1},
		&tmdb.MediaMetadata{Title: "雨伞杀手竟是白月光", Overview: "第一集", Season: 1, Episode: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(dir, "折月亮 (2026)", "tvshow.nfo"),
		filepath.Join(seasonDir, "season.nfo"),
		filepath.Join(seasonDir, "折月亮 - S01E01.nfo"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(seasonDir, "折月亮 - S01E01.nfo"))
	if err != nil || !strings.Contains(string(b), "<title>雨伞杀手竟是白月光</title>") {
		t.Fatalf("episode nfo missing title: %s (%v)", b, err)
	}
}
