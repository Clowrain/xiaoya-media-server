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
