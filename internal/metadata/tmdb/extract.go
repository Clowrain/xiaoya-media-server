package tmdb

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	tmdbTagRe      = regexp.MustCompile(`(?i)[\{\[]\s*tmdb(?:id)?[-=:\s]+(\d+)\s*[\}\]]`)
	pureTagDirRe   = regexp.MustCompile(`(?i)^[\{\[]\s*(?:tmdb(?:id)?|imdb(?:id)?|tvdb(?:id)?)[-=:\s]+[^\}\]]+[\}\]]$`)
	parenYearRe    = regexp.MustCompile(`[\(\[（](19\d{2}|20\d{2})[\)\]）]`)
	dotYearRe      = regexp.MustCompile(`[._\s-](19\d{2}|20\d{2})(?:[._\s-]|$)`)
	sxePattern     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])S(\d{1,3})\s*[._-]*\s*E(\d{1,4})(?:[^0-9]|$)`)
	xPattern       = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{1,2})x(\d{1,3})(?:[^0-9]|$)`)
	epOnlyPattern  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])EP?(\d{1,4})(?:[^0-9]|$)`)
	cnEpPattern    = regexp.MustCompile(`第\s*0*(\d{1,4})\s*[集话話期]`)
	pureNumPattern = regexp.MustCompile(`^\s*0*(\d{1,4})\s*$`)
	noiseSuffixRe  = regexp.MustCompile(`(?i)[._\s-]+(2160p|1080p|720p|480p|4k|web-dl|webrip|bluray|bdrip|hdtv|x264|x265|hevc|h264|h265|aac|ddp\d*).*$`)

	// Variety show air date pattern: 20240105, 2024-01-05, 2024.01.05
	varietyDatePattern = regexp.MustCompile(`(?:^|[^0-9])(20\d{2})[._-]?([01]\d)[._-]?([0-3]\d)(?:[^0-9]|$)`)
	// Variety and split-episode segment pattern: 上, 中, 下, 上集, Part 1, Part A, etc.
	segmentPattern = regexp.MustCompile(`(?i)(?:^|[\s._-])(上集?|中集?|下集?|part\s*[a-z0-9]+)(?:[\s._-]|$)`)
)

// EpisodeInfo provides fine-grained details for episodes, including
// variety show air dates and split episode segments (上/中/下, Part 1).
type EpisodeInfo struct {
	Season        int
	Episode       int
	Segment       string // e.g. "上", "下", "Part 1"
	AirDate       string // e.g. "2024-01-05"
	IsDateEpisode bool
	Found         bool
}

// ExtractTMDBID extracts a numeric TMDB ID from any path or folder name containing {tmdb-270431}, [tmdbid=270431], etc.
func ExtractTMDBID(pathOrName string) string {
	matches := tmdbTagRe.FindAllStringSubmatch(pathOrName, -1)
	if len(matches) == 0 {
		return ""
	}
	// Return the most specific (deepest) tag in the path.
	return matches[len(matches)-1][1]
}

// IsPureMetadataTagDir reports whether a directory basename is solely a provider tag such as "{tmdb-270431}".
func IsPureMetadataTagDir(base string) bool {
	return pureTagDirRe.MatchString(strings.TrimSpace(base))
}

// ParseTitleAndYear extracts a clean media title and optional release year from a folder or file stem.
// Example: "折月亮 (2026)" -> ("折月亮", 2026).
func ParseTitleAndYear(raw string) (string, int) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", 0
	}
	// Strip provider tags like {tmdb-270431}
	s = tmdbTagRe.ReplaceAllString(s, " ")

	year := 0
	if m := parenYearRe.FindStringSubmatchIndex(s); len(m) == 4 {
		year, _ = strconv.Atoi(s[m[2]:m[3]])
		s = strings.TrimSpace(s[:m[0]] + " " + s[m[1]:])
	} else if m := dotYearRe.FindStringSubmatchIndex(s); len(m) == 4 {
		year, _ = strconv.Atoi(s[m[2]:m[3]])
		if strings.TrimSpace(s[:m[0]]) != "" {
			s = s[:m[0]]
		}
	}

	if cleaned := noiseSuffixRe.ReplaceAllString(s, ""); strings.TrimSpace(cleaned) != "" {
		s = cleaned
	}
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return strings.TrimSpace(raw), year
	}
	return s, year
}

// ParseEpisodeInfo extracts structured episode information including season,
// episode number, variety show air date, and split-episode segments.
func ParseEpisodeInfo(fileStem string, seasonHint int) EpisodeInfo {
	stem := strings.TrimSpace(fileStem)
	if ext := strings.ToLower(filepath.Ext(stem)); ext != "" {
		switch ext {
		case ".strm", ".mp4", ".m4v", ".mkv", ".avi", ".ts", ".mov", ".flv", ".wmv", ".rmvb", ".webm", ".iso":
			stem = strings.TrimSuffix(stem, filepath.Ext(stem))
		}
	}

	segment := ""
	if sm := segmentPattern.FindStringSubmatch(stem); len(sm) == 2 {
		segment = strings.TrimSpace(sm[1])
	}

	if m := sxePattern.FindStringSubmatch(stem); len(m) == 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return EpisodeInfo{Season: s, Episode: e, Segment: segment, Found: e > 0}
	}
	if m := xPattern.FindStringSubmatch(stem); len(m) == 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return EpisodeInfo{Season: s, Episode: e, Segment: segment, Found: e > 0}
	}

	defSeason := seasonHint
	if defSeason <= 0 {
		defSeason = 1
	}

	if m := epOnlyPattern.FindStringSubmatch(stem); len(m) == 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 {
			return EpisodeInfo{Season: defSeason, Episode: e, Segment: segment, Found: true}
		}
	}
	if m := cnEpPattern.FindStringSubmatch(stem); len(m) == 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 {
			return EpisodeInfo{Season: defSeason, Episode: e, Segment: segment, Found: true}
		}
	}

	// Variety show air dates: e.g. 20240105 or 2024-01-05
	if vm := varietyDatePattern.FindStringSubmatch(stem); len(vm) == 4 {
		year, _ := strconv.Atoi(vm[1])
		month, _ := strconv.Atoi(vm[2])
		day, _ := strconv.Atoi(vm[3])
		airDate := vm[1] + "-" + vm[2] + "-" + vm[3]
		season := defSeason
		if seasonHint <= 0 && year >= 2000 {
			season = year // convention for date-based variety shows: season is year
		}
		epNum := month*100 + day
		return EpisodeInfo{Season: season, Episode: epNum, Segment: segment, AirDate: airDate, IsDateEpisode: true, Found: true}
	}

	// Bare numeric stem (e.g. 01.strm, 108.strm)
	if m := pureNumPattern.FindStringSubmatch(stem); len(m) == 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 && e < 1900 {
			return EpisodeInfo{Season: defSeason, Episode: e, Segment: segment, Found: true}
		}
	}

	return EpisodeInfo{Season: seasonHint, Segment: segment, Found: false}
}

// ParseSeasonEpisode extracts season and episode numbers from an episode filename stem.
// It supports S01E01, 1x01, E01 / EP01 (using seasonHint when > 0, else 1), 第01集, variety dates, and bare numeric stems.
func ParseSeasonEpisode(fileStem string, seasonHint int) (season int, episode int, ok bool) {
	info := ParseEpisodeInfo(fileStem, seasonHint)
	return info.Season, info.Episode, info.Found
}
