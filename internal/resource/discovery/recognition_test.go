package discovery

import (
	"context"
	"testing"
)

func TestParseMediaIdentityFlatEpisodeAndMovie(t *testing.T) {
	ep := ParseMediaIdentity(MediaResource{SourcePath: "/每日更新/电视剧/国产剧/折月亮/01.strm", Name: "01.strm"})
	if ep.MediaType != "EPISODE" || ep.Title != "折月亮" || ep.SeasonNumber != 1 || ep.EpisodeNumber != 1 || ep.State != "READY_FOR_MATCH" {
		t.Fatalf("flat episode identity=%+v", ep)
	}
	movie := ParseMediaIdentity(MediaResource{SourcePath: "/每日更新/电影/中国/八仙！ (2026)/八仙！ (2026).strm", Name: "八仙！ (2026).strm"})
	if movie.MediaType != "MOVIE" || movie.Title != "八仙！" || movie.Year != 2026 {
		t.Fatalf("movie identity=%+v", movie)
	}
}

func TestPersistV2IdentityCreatesMediaVersionAndJob(t *testing.T) {
	_, _, _, store := setupTestXiaoyaAndStore(t)
	svc := NewService(store, nil, nil)
	ctx := context.Background()
	res := MediaResource{
		ID: "sqlite-resource-1", OriginType: OriginXiaoyaSQLite,
		SourcePath: "/每日更新/电视剧/国产剧/折月亮/01.strm", Name: "01.strm",
		SourceContent: "http://xiaoya.host:5678/d/折月亮/01.mp4", LastChangedAt: 11,
	}
	if err := svc.PersistV2Identity(ctx, res); err != nil {
		t.Fatalf("persist identity: %v", err)
	}
	var mediaType, title string
	if err := store.DB().QueryRowContext(ctx, "SELECT media_type,title FROM recognition_results WHERE source_resource_id=?", res.ID).Scan(&mediaType, &title); err != nil {
		t.Fatal(err)
	}
	if mediaType != "EPISODE" || title != "折月亮" {
		t.Fatalf("recognition row=(%s,%s)", mediaType, title)
	}
	var versions, jobs int
	if err := store.DB().QueryRowContext(ctx, "SELECT count(*) FROM media_versions_v2 WHERE source_resource_id=?", res.ID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx, "SELECT count(*) FROM work_jobs_v2 WHERE kind='metadata_match' AND scope_key=?", res.ID).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if versions != 1 || jobs != 1 {
		t.Fatalf("versions=%d jobs=%d", versions, jobs)
	}
}
