package xiaoya

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestLine(t *testing.T) {
	// Sample line 1: 走马上任
	line1 := `每日更新/电视剧/国产剧/走马上任 (2026)/{tmdb-275297}/Season 1/走马上任.2026.S01E01.strm#http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/走马上任/01.mp4?sign=SIGN_STR`
	e1, err := ParseManifestLine(511674, line1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e1.Title != "走马上任" {
		t.Errorf("expected Title 走马上任, got %s", e1.Title)
	}
	if e1.Year != 2026 {
		t.Errorf("expected Year 2026, got %d", e1.Year)
	}
	if e1.TmdbID != "275297" {
		t.Errorf("expected TmdbID 275297, got %s", e1.TmdbID)
	}
	if e1.SeasonNum != 1 || e1.EpisodeNum != 1 {
		t.Errorf("expected S01E01, got S%02dE%02d", e1.SeasonNum, e1.EpisodeNum)
	}
	if e1.MediaType != "Series" {
		t.Errorf("expected Series, got %s", e1.MediaType)
	}
	if e1.PlayURL != "http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/走马上任/01.mp4?sign=SIGN_STR" {
		t.Errorf("unexpected PlayURL: %s", e1.PlayURL)
	}

	// Sample line 2: 奥本海默 (IMAX Movie)
	line2 := `115/IMAX/奥本海默 - IMAX (2023)/{tmdb-872585}/奥本海默.2023.strm#http://xiaoya.host:5678/d/test.mkv?sign=SIGN_STR`
	e2, err := ParseManifestLine(10, line2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e2.Title != "奥本海默 - IMAX" {
		t.Errorf("expected Title 奥本海默 - IMAX, got %s", e2.Title)
	}
	if e2.Year != 2023 {
		t.Errorf("expected Year 2023, got %d", e2.Year)
	}
	if e2.TmdbID != "872585" {
		t.Errorf("expected TmdbID 872585, got %s", e2.TmdbID)
	}
	if e2.MediaType != "Movie" {
		t.Errorf("expected Movie, got %s", e2.MediaType)
	}
}

func TestManifestScannerFindMatching(t *testing.T) {
	content := `115/IMAX/奥本海默 - IMAX (2023)/{tmdb-872585}/奥本海默.2023.strm#http://xiaoya.host:5678/d/oppenheimer.mkv?sign=SIGN_STR
每日更新/电视剧/国产剧/走马上任 (2026)/{tmdb-275297}/Season 1/走马上任.2026.S01E01.strm#http://xiaoya.host:5678/d/01.mp4?sign=SIGN_STR
每日更新/电视剧/国产剧/走马上任 (2026)/{tmdb-275297}/Season 1/走马上任.2026.S01E02.strm#http://xiaoya.host:5678/d/02.mp4?sign=SIGN_STR
每日更新/电视剧/国产剧/折月亮 (2026)/{tmdb-270431}/Season 1/折月亮.2026.S01E01.strm#http://xiaoya.host:5678/d/zy01.mp4?sign=SIGN_STR
`
	tmp := t.TempDir()
	txtPath := filepath.Join(tmp, "strm.txt")
	if err := os.WriteFile(txtPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewManifestScanner(txtPath)
	if !scanner.Available() {
		t.Fatal("expected scanner to be available")
	}

	ctx := context.Background()
	results, err := scanner.FindMatching(ctx, "走马上任", 10)
	if err != nil {
		t.Fatalf("FindMatching failed: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 episodes of 走马上任, got %d", len(results))
	}
	if results[0].EpisodeNum != 1 || results[1].EpisodeNum != 2 {
		t.Errorf("unexpected episodes: %+v", results)
	}

	// Test aggregated search
	agg, err := scanner.FindAggregated(ctx, "", 10)
	if err != nil {
		t.Fatalf("FindAggregated failed: %v", err)
	}
	// 3 works: 奥本海默 (1), 走马上任 (2 eps), 折月亮 (1 ep)
	if len(agg) != 3 {
		t.Fatalf("expected 3 aggregated works, got %d", len(agg))
	}
	var zmsr *AggregatedManifestItem
	for i := range agg {
		if strings.Contains(agg[i].Title, "走马上任") {
			zmsr = &agg[i]
			break
		}
	}
	if zmsr == nil || zmsr.EpisodeCount != 2 {
		t.Errorf("expected 走马上任 to have 2 episodes, got %+v", zmsr)
	}
	if zmsr.MediaType != "Series" {
		t.Errorf("expected Series media type, got %s", zmsr.MediaType)
	}
}

func TestStreamMultiPrefixAndFingerprint(t *testing.T) {
	content := `115/IMAX/奥本海默 - IMAX (2023)/{tmdb-872585}/奥本海默.2023.strm#http://xiaoya.host:5678/d/oppenheimer.mkv?sign=SIGN_STR
每日更新/电视剧/国产剧/走马上任 (2026)/{tmdb-275297}/Season 1/走马上任.2026.S01E01.strm#http://xiaoya.host:5678/d/01.mp4?sign=SIGN_STR
每日更新/电视剧/国产剧/走马上任 (2026)/{tmdb-275297}/Season 1/走马上任.2026.S01E02.strm#http://xiaoya.host:5678/d/02.mp4?sign=SIGN_STR
每日更新/电视剧/韩剧/黑暗荣耀 (2022)/{tmdb-136283}/Season 1/黑暗荣耀.2022.S01E01.strm#http://xiaoya.host:5678/d/glory.mp4?sign=SIGN_STR
`
	tmp := t.TempDir()
	txtPath := filepath.Join(tmp, "strm.txt")
	if err := os.WriteFile(txtPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewManifestScanner(txtPath)
	fp1, err := scanner.Fingerprint()
	if err != nil {
		t.Fatalf("Fingerprint failed: %v", err)
	}
	if fp1.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), fp1.Size)
	}
	if scanner.HasChanged(fp1) {
		t.Errorf("fingerprint should not have changed")
	}

	ctx := context.Background()
	// Test multi prefix with overlapping / child paths
	prefixes := []string{
		"/每日更新/电视剧/国产剧",
		"/每日更新/电视剧/韩剧",
		"/115/IMAX",
	}

	matchedCount := make(map[string]int)
	total, err := scanner.StreamMultiPrefix(ctx, prefixes, func(matchedPrefix string, entry ManifestEntry) error {
		matchedCount[matchedPrefix]++
		return nil
	})
	if err != nil {
		t.Fatalf("StreamMultiPrefix failed: %v", err)
	}
	if total != 4 {
		t.Fatalf("expected total 4, got %d", total)
	}
	if matchedCount["/每日更新/电视剧/国产剧"] != 2 {
		t.Errorf("expected 2 for 国产剧, got %d", matchedCount["/每日更新/电视剧/国产剧"])
	}
	if matchedCount["/每日更新/电视剧/韩剧"] != 1 {
		t.Errorf("expected 1 for 韩剧, got %d", matchedCount["/每日更新/电视剧/韩剧"])
	}
	if matchedCount["/115/IMAX"] != 1 {
		t.Errorf("expected 1 for 115/IMAX, got %d", matchedCount["/115/IMAX"])
	}
}
