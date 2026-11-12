package embyserver

import (
	"errors"
	"testing"
)

type fakeMediaVersionRepository struct {
	versions []Item
	err      error
	grouping string
	itemID   string
	calls    int
}

func (r *fakeMediaVersionRepository) VersionsByGrouping(grouping, itemID string) ([]Item, error) {
	r.grouping = grouping
	r.itemID = itemID
	r.calls++
	return r.versions, r.err
}

func TestGroupedMediaSourceSelectorHonorsAPIAndUserPaths(t *testing.T) {
	base := Item{ID: "media-1", URL: "https://media.example/1080.mkv"}
	other := Item{ID: "media-2", URL: "https://media.example/2160.mkv"}
	repository := &fakeMediaVersionRepository{versions: []Item{base, other}}
	selector := groupedMediaSourceSelector{versions: repository}

	apiSources := selector.Select(base, User{API: true}, "lib")
	if len(apiSources) != 1 || apiSources[0].ID != base.ID || repository.calls != 0 {
		t.Fatalf("API-key selection should retain only the requested source: %+v calls=%d", apiSources, repository.calls)
	}
	userSources := selector.Select(base, User{}, "lib")
	if len(userSources) != 2 || userSources[0].ID != base.ID || userSources[1].ID != other.ID {
		t.Fatalf("user selection should include grouped source versions: %+v", userSources)
	}
	if repository.calls != 1 || repository.grouping != "lib" || repository.itemID != base.ID {
		t.Fatalf("version repository arguments were not forwarded: %+v", repository)
	}
}

func TestGroupedMediaSourceSelectorFallsBackToRequestedItem(t *testing.T) {
	base := Item{ID: "media-1", URL: "https://media.example/1080.mkv"}
	repository := &fakeMediaVersionRepository{err: errors.New("repository unavailable")}
	selector := groupedMediaSourceSelector{versions: repository}

	if sources := selector.Select(base, User{}, "group"); len(sources) != 1 || sources[0].ID != base.ID {
		t.Fatalf("version lookup failure should fall back to requested source: %+v", sources)
	}
	if sources := selector.Select(Item{ID: "no-url"}, User{}, "group"); len(sources) != 1 || sources[0].ID != "no-url" {
		t.Fatalf("items without URLs should not query alternate sources: %+v", sources)
	}
	if repository.calls != 1 {
		t.Fatalf("expected one version lookup, got %d", repository.calls)
	}
}
