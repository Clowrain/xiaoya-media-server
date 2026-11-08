package integration

import (
	"os"
	"testing"

	"xiaoya-media-server/internal/metadata/tmdb"
	"xiaoya-media-server/internal/resource/xiaoya"
)

func TestChooseTMDBResultRequiresUnambiguousMatch(t *testing.T) {
	results := []tmdb.SearchResult{
		{ID: 1, Title: "折月亮", Year: 2026, MediaType: "tv"},
		{ID: 2, Title: "折月亮", Year: 2025, MediaType: "tv"},
	}
	if _, ok := chooseTMDBResult(results, "折月亮", 0); ok {
		t.Fatal("same-title candidates should remain ambiguous")
	}
	result, ok := chooseTMDBResult(results[:1], "折月亮", 2026)
	if !ok || result.ID != 1 {
		t.Fatalf("exact single candidate=%+v ok=%v", result, ok)
	}
}

func TestManifestScannerAutoDetect(t *testing.T) {
	tmp := t.TempDir()
	txtPath := tmp + "/strm.txt"
	if err := os.WriteFile(txtPath, []byte("每日更新/走马上任.strm#http://xiaoya.host:5678/d/01.mp4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	detected := xiaoya.AutoDetectStrmText("", tmp+"/strm_internal.db")
	if detected != txtPath {
		t.Fatalf("expected auto-detected %s, got %s", txtPath, detected)
	}
}
