package integration

import (
	"testing"

	"xiaoya-media-server/internal/metadata/tmdb"
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
