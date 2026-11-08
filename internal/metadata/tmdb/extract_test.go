package tmdb

import "testing"

func TestParseSeasonEpisodeFlatNumericDefaultsToSeasonOne(t *testing.T) {
	season, episode, ok := ParseSeasonEpisode("01", 0)
	if !ok || season != 1 || episode != 1 {
		t.Fatalf("ParseSeasonEpisode(01, 0) = S%02dE%02d (ok=%v), want S01E01", season, episode, ok)
	}
	season, episode, ok = ParseSeasonEpisode("30.strm", 0)
	if !ok || season != 1 || episode != 30 {
		t.Fatalf("ParseSeasonEpisode(30.strm, 0) = S%02dE%02d (ok=%v), want S01E30", season, episode, ok)
	}
}

func TestParseVarietyDatesAndSegments(t *testing.T) {
	// Variety show air date
	info := ParseEpisodeInfo("歌手2024.20240510.mp4", 0)
	if !info.Found || info.Season != 2024 || info.AirDate != "2024-05-10" {
		t.Fatalf("unexpected variety info: %+v", info)
	}

	// Variety show with segment (上 / 下)
	infoSplit := ParseEpisodeInfo("奔跑吧.20240615.上集.strm", 0)
	if !infoSplit.Found || infoSplit.Segment != "上集" {
		t.Fatalf("expected segment '上集', got %+v", infoSplit)
	}

	// Anime large absolute number
	infoAnime := ParseEpisodeInfo("海贼王 - 第1088集.mkv", 1)
	if !infoAnime.Found || infoAnime.Episode != 1088 {
		t.Fatalf("expected episode 1088, got %+v", infoAnime)
	}
}
