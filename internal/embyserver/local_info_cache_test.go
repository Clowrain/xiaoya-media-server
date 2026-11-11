package embyserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalInfoCacheIsScanScopedAndReusesSharedSidecars(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "Show.S01E01.strm")
	secondPath := filepath.Join(dir, "Show.S01E02.strm")
	if err := os.WriteFile(filepath.Join(dir, "tvshow.nfo"), []byte("<tvshow><title>First title</title><plot>First plot</plot></tvshow>"), 0600); err != nil {
		t.Fatal(err)
	}
	poster := filepath.Join(dir, "poster.jpg")
	if err := os.WriteFile(poster, []byte("poster"), 0600); err != nil {
		t.Fatal(err)
	}

	cache := newLocalInfoCache()
	first := Item{Path: firstPath, Name: "fallback"}
	cache.localInfo(firstPath, &first)
	second := Item{Path: secondPath, Name: "fallback"}
	cache.localInfo(secondPath, &second)
	if first.Name != "First title" || second.Name != "First title" || first.Overview != "First plot" || second.Poster != poster {
		t.Fatalf("shared sidecars were not applied: first=%+v second=%+v", first, second)
	}
	if len(cache.nfos) != 2 || len(cache.posters) != 1 || !cache.posters[poster] {
		t.Fatalf("shared probes were not memoized: nfos=%d posters=%d", len(cache.nfos), len(cache.posters))
	}

	if err := os.WriteFile(filepath.Join(dir, "tvshow.nfo"), []byte("<tvshow><title>Updated title</title></tvshow>"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(poster); err != nil {
		t.Fatal(err)
	}
	// A new scan gets a new cache, so changed and removed sidecars are observed.
	next := Item{Path: firstPath, Name: "fallback"}
	newLocalInfoCache().localInfo(firstPath, &next)
	if next.Name != "Updated title" || next.Poster != "" {
		t.Fatalf("next scan reused stale sidecars: %+v", next)
	}
}
