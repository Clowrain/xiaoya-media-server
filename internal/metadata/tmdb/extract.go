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
)

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

// ParseSeasonEpisode extracts season and episode numbers from an episode filename stem.
// It supports S01E01, 1x01, E01 / EP01 (using seasonHint when > 0, else 1), 第01集, and bare numeric stems inside a Season folder.
func ParseSeasonEpisode(fileStem string, seasonHint int) (season int, episode int, ok bool) {
	stem := strings.TrimSpace(fileStem)
	// Strip trailing media extension if caller passed "E01.mp4" stem inside "E01.mp4.strm"
	if ext := strings.ToLower(filepath.Ext(stem)); ext != "" {
		switch ext {
		case ".strm", ".mp4", ".m4v", ".mkv", ".avi", ".ts", ".mov", ".flv", ".wmv", ".rmvb", ".webm", ".iso":
			stem = strings.TrimSuffix(stem, filepath.Ext(stem))
		}
	}

	if m := sxePattern.FindStringSubmatch(stem); len(m) == 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, e > 0
	}
	if m := xPattern.FindStringSubmatch(stem); len(m) == 3 {
		s, _ := strconv.Atoi(m[1])
		e, _ := strconv.Atoi(m[2])
		return s, e, e > 0
	}
	defSeason := seasonHint
	if defSeason <= 0 {
		defSeason = 1
	}
	if m := epOnlyPattern.FindStringSubmatch(stem); len(m) == 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 {
			return defSeason, e, true
		}
	}
	if m := cnEpPattern.FindStringSubmatch(stem); len(m) == 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 {
			return defSeason, e, true
		}
	}
	// A bare number is a common Xiaoya flat episode layout (01.strm, 02.strm,
	// ...).  When the caller has no explicit Season directory, use Season 1.
	// This parser is only used for TV episode candidates, so treating a numeric
	// stem as an episode is preferable to dropping the item from the season.
	if m := pureNumPattern.FindStringSubmatch(stem); len(m) == 2 {
		e, _ := strconv.Atoi(m[1])
		if e > 0 && e < 1900 {
			return defSeason, e, true
		}
	}
	return seasonHint, 0, false
}
