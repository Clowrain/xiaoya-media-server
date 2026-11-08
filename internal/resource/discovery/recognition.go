package discovery

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"xiaoya-media-server/internal/metadata/tmdb"
)

// ParseMediaIdentity performs only deterministic parsing. It never chooses a
// TMDB candidate, so ambiguous titles remain NEEDS_CONFIRMATION instead of
// silently binding to the wrong work.
func ParseMediaIdentity(res MediaResource) ParsedMediaIdentity {
	if res.IsDir {
		return ParsedMediaIdentity{}
	}
	parts := splitSourcePath(res.SourcePath)
	if len(parts) == 0 {
		return ParsedMediaIdentity{State: "NEEDS_CONFIRMATION", Evidence: "empty source path"}
	}
	if isMoviePath(parts) {
		title, year := tmdb.ParseTitleAndYear(filepath.Base(filepath.Dir(res.SourcePath)))
		if title == "" {
			title, year = tmdb.ParseTitleAndYear(strings.TrimSuffix(res.Name, filepath.Ext(res.Name)))
		}
		if title == "" {
			return ParsedMediaIdentity{MediaType: "MOVIE", State: "NEEDS_CONFIRMATION", Evidence: "movie title not found"}
		}
		return ParsedMediaIdentity{MediaType: "MOVIE", Title: title, Year: year, State: "READY_FOR_MATCH", Evidence: "movie directory and title parser"}
	}

	seasonHint, seasonEvidence := seasonFromPath(parts)
	epInfo := tmdb.ParseEpisodeInfo(strings.TrimSuffix(res.Name, filepath.Ext(res.Name)), seasonHint)
	seriesIdx := seriesIndex(parts, seasonEvidence >= 0)
	if !epInfo.Found || seriesIdx < 0 {
		return ParsedMediaIdentity{MediaType: "EPISODE", State: "NEEDS_CONFIRMATION", Evidence: "episode number or series directory is ambiguous"}
	}
	seriesTitle, year := tmdb.ParseTitleAndYear(parts[seriesIdx])
	if seriesTitle == "" {
		return ParsedMediaIdentity{MediaType: "EPISODE", State: "NEEDS_CONFIRMATION", Evidence: "series title is empty after normalization"}
	}
	parentID := StableID("media:SERIES:" + seriesTitle + ":" + strconv.Itoa(year))
	evidence := "flat numeric/default Season 1"
	if seasonEvidence >= 0 {
		evidence = "explicit Season directory"
	}
	if epInfo.IsDateEpisode {
		evidence = "variety show air date (" + epInfo.AirDate + ")"
	}
	if epInfo.Segment != "" {
		evidence += " [segment: " + epInfo.Segment + "]"
	}
	return ParsedMediaIdentity{
		MediaType: "EPISODE", Title: seriesTitle, Year: year, ParentID: parentID,
		SeasonNumber: epInfo.Season, EpisodeNumber: epInfo.Episode, Segment: epInfo.Segment,
		State: "READY_FOR_MATCH", Evidence: evidence,
	}
}

func splitSourcePath(p string) []string {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" && part != "." {
			out = append(out, part)
		}
	}
	return out
}

func isMoviePath(parts []string) bool {
	for _, part := range parts {
		if part == "电影" || strings.EqualFold(part, "movie") || strings.EqualFold(part, "movies") {
			return true
		}
	}
	return false
}

func seasonFromPath(parts []string) (int, int) {
	for i := len(parts) - 2; i >= 0; i-- {
		base := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(parts[i], "_", " "), ".", " "))
		if strings.HasPrefix(base, "season ") {
			var n int
			if _, err := fmt.Sscanf(base, "season %d", &n); err == nil {
				return n, i
			}
		}
	}
	return 1, -1
}

func seriesIndex(parts []string, hasSeasonDir bool) int {
	i := len(parts) - 2
	if hasSeasonDir {
		for i >= 0 && strings.Contains(strings.ToLower(parts[i]), "tmdb") {
			i--
		}
		i--
	}
	for i >= 0 && tmdb.IsPureMetadataTagDir(parts[i]) {
		i--
	}
	return i
}
