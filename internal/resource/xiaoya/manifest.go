package xiaoya

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	tmdbRegex   = regexp.MustCompile(`\{tmdb-(\d+)\}`)
	yearRegex   = regexp.MustCompile(`\((\d{4})\)`)
	seasonRegex = regexp.MustCompile(`(?i)(?:Season\s*(\d+)|S(\d+)(?:E|\b))`)
	epRegex     = regexp.MustCompile(`(?i)(?:S\d+E(\d+)|E(\d+)|第\s*(\d+)\s*集)`)
)

// ManifestEntry represents a parsed line from Xiaoya's strm.txt.
type ManifestEntry struct {
	LineNumber int    `json:"line_number"`
	RelPath    string `json:"rel_path"`
	PlayURL    string `json:"play_url"`
	Title      string `json:"title"`
	Year       int    `json:"year,omitempty"`
	TmdbID     string `json:"tmdb_id,omitempty"`
	SeasonNum  int    `json:"season_number,omitempty"`
	EpisodeNum int    `json:"episode_number,omitempty"`
	MediaType  string `json:"media_type"` // "Movie" or "Series"
	Category   string `json:"category,omitempty"`
}

// ManifestScanner provides high-performance streaming queries against Xiaoya's strm.txt manifest.
type ManifestScanner struct {
	mu       sync.RWMutex
	filePath string
	mtime    time.Time
	lineCnt  int
}

// NewManifestScanner creates a scanner pointing to strm.txt.
func NewManifestScanner(filePath string) *ManifestScanner {
	return &ManifestScanner{
		filePath: filePath,
	}
}

// ManifestFingerprint captures file state for change detection.
type ManifestFingerprint struct {
	ModTime time.Time `json:"mod_time"`
	Size    int64     `json:"size"`
}

// Fingerprint returns the current mod time and size of strm.txt.
func (s *ManifestScanner) Fingerprint() (ManifestFingerprint, error) {
	if s == nil || s.filePath == "" {
		return ManifestFingerprint{}, fmt.Errorf("strm.txt not configured")
	}
	fi, err := os.Stat(s.filePath)
	if err != nil {
		return ManifestFingerprint{}, err
	}
	return ManifestFingerprint{ModTime: fi.ModTime(), Size: fi.Size()}, nil
}

// HasChanged returns true if the current file fingerprint differs from the previous one.
func (s *ManifestScanner) HasChanged(prev ManifestFingerprint) bool {
	cur, err := s.Fingerprint()
	if err != nil {
		return false
	}
	return !cur.ModTime.Equal(prev.ModTime) || cur.Size != prev.Size
}

// Available returns true if the strm.txt file exists and is accessible.
func (s *ManifestScanner) Available() bool {
	if s == nil || s.filePath == "" {
		return false
	}
	fi, err := os.Stat(s.filePath)
	return err == nil && fi.Size() > 0
}

// FilePath returns the configured strm.txt path.
func (s *ManifestScanner) FilePath() string {
	if s == nil {
		return ""
	}
	return s.filePath
}

// ParseManifestLine parses a single "RelPath#PlayURL" line into ManifestEntry.
func ParseManifestLine(lineNum int, line string) (*ManifestEntry, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, nil
	}
	parts := strings.SplitN(line, "#", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid manifest line (missing '#'): %s", line)
	}

	relPath := strings.TrimPrefix(strings.TrimSpace(parts[0]), "/")
	playURL := strings.TrimSpace(parts[1])
	if relPath == "" || playURL == "" {
		return nil, nil
	}

	entry := &ManifestEntry{
		LineNumber: lineNum,
		RelPath:    relPath,
		PlayURL:    playURL,
		MediaType:  "Movie",
	}

	// 1. TMDB ID extraction
	if m := tmdbRegex.FindStringSubmatch(relPath); len(m) > 1 {
		entry.TmdbID = m[1]
	}

	// 2. Year extraction
	if m := yearRegex.FindStringSubmatch(relPath); len(m) > 1 {
		if y, err := strconv.Atoi(m[1]); err == nil {
			entry.Year = y
		}
	}

	// 3. Season extraction
	if m := seasonRegex.FindStringSubmatch(relPath); len(m) > 0 {
		for i := 1; i < len(m); i++ {
			if m[i] != "" {
				if sNum, err := strconv.Atoi(m[i]); err == nil {
					entry.SeasonNum = sNum
					entry.MediaType = "Series"
					break
				}
			}
		}
	}

	// 4. Episode extraction
	if m := epRegex.FindStringSubmatch(relPath); len(m) > 0 {
		for i := 1; i < len(m); i++ {
			if m[i] != "" {
				if epNum, err := strconv.Atoi(m[i]); err == nil {
					entry.EpisodeNum = epNum
					entry.MediaType = "Series"
					break
				}
			}
		}
	}

	// 5. Detect series type by path segments
	pathLower := strings.ToLower(relPath)
	if strings.Contains(pathLower, "电视剧") || strings.Contains(pathLower, "tv") || strings.Contains(pathLower, "剧集") || strings.Contains(pathLower, "season") || entry.EpisodeNum > 0 {
		entry.MediaType = "Series"
	}

	// 6. Extract clean Title
	segs := strings.Split(relPath, "/")
	titleSeg := ""
	for i, seg := range segs {
		if tmdbRegex.MatchString(seg) && i > 0 {
			titleSeg = segs[i-1]
			break
		}
	}
	if titleSeg == "" && len(segs) >= 2 {
		if entry.MediaType == "Series" && len(segs) >= 3 {
			// e.g. "走马上任 (2026)" from ".../走马上任 (2026)/Season 1/..."
			titleSeg = segs[len(segs)-2]
			if strings.HasPrefix(strings.ToLower(titleSeg), "season") && len(segs) >= 4 {
				titleSeg = segs[len(segs)-3]
			}
		} else {
			titleSeg = segs[len(segs)-1]
		}
	}
	if titleSeg == "" && len(segs) > 0 {
		titleSeg = segs[len(segs)-1]
	}

	// Clean (2026), .strm, etc.
	titleSeg = yearRegex.ReplaceAllString(titleSeg, "")
	titleSeg = strings.TrimSuffix(titleSeg, ".strm")
	entry.Title = strings.TrimSpace(titleSeg)

	return entry, nil
}

// FindMatching scans strm.txt and returns all entries matching the query keyword or path prefix.
// It supports case-insensitive sub-matching and stops early if limit > 0 and limit is reached.
func (s *ManifestScanner) FindMatching(ctx context.Context, query string, limit int) ([]ManifestEntry, error) {
	if !s.Available() {
		return nil, fmt.Errorf("strm.txt not found at %s", s.filePath)
	}

	f, err := os.Open(s.filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	normQuery := strings.ToLower(strings.Trim(strings.TrimSpace(query), "/"))
	var results []ManifestEntry

	scanner := bufio.NewScanner(f)
	// allow up to 1MB per line just in case of exceptionally long URLs
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if ctx.Err() != nil {
			return results, ctx.Err()
		}

		line := scanner.Text()
		if normQuery != "" && !strings.Contains(strings.ToLower(line), normQuery) {
			continue
		}

		entry, err := ParseManifestLine(lineNum, line)
		if err != nil || entry == nil {
			continue
		}

		results = append(results, *entry)
		if limit > 0 && len(results) >= limit {
			break
		}
	}

	return results, scanner.Err()
}

// StreamPrefix streams entries whose rel_path starts with prefix to the onEntry callback.
func (s *ManifestScanner) StreamPrefix(ctx context.Context, prefix string, onEntry func(entry ManifestEntry) error) (int, error) {
	if !s.Available() {
		return 0, fmt.Errorf("strm.txt not found at %s", s.filePath)
	}

	f, err := os.Open(s.filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	cleanPrefix := strings.ToLower(strings.Trim(strings.TrimSpace(prefix), "/"))
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	count := 0
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if ctx.Err() != nil {
			return count, ctx.Err()
		}

		line := scanner.Text()
		if cleanPrefix != "" && !strings.Contains(strings.ToLower(line), cleanPrefix) {
			continue
		}

		entry, err := ParseManifestLine(lineNum, line)
		if err != nil || entry == nil {
			continue
		}

		if err := onEntry(*entry); err != nil {
			return count, err
		}
		count++
	}

	return count, scanner.Err()
}

// PrefixMatch binds an original prefix string with its clean lowercased version.
type PrefixMatch struct {
	Original string
	Clean    string
}

// StreamMultiPrefix scans strm.txt in a SINGLE PASS and dispatches matching entries
// to onEntry. Longest-prefix matching is used to resolve child/parent overlapping.
func (s *ManifestScanner) StreamMultiPrefix(ctx context.Context, prefixes []string, onEntry func(matchedPrefix string, entry ManifestEntry) error) (int, error) {
	if !s.Available() {
		return 0, fmt.Errorf("strm.txt not found at %s", s.filePath)
	}

	var targets []PrefixMatch
	for _, p := range prefixes {
		cl := strings.ToLower(strings.Trim(strings.TrimSpace(p), "/"))
		if cl != "" {
			targets = append(targets, PrefixMatch{Original: p, Clean: cl})
		}
	}
	if len(targets) == 0 {
		return 0, nil
	}

	// Sort targets by Clean length descending so more specific paths match first
	sort.Slice(targets, func(i, j int) bool {
		return len(targets[i].Clean) > len(targets[j].Clean)
	})

	f, err := os.Open(s.filePath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	count := 0
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if ctx.Err() != nil {
			return count, ctx.Err()
		}

		line := scanner.Text()
		lineLower := strings.ToLower(line)

		var matched *PrefixMatch
		for i := range targets {
			if strings.Contains(lineLower, targets[i].Clean) {
				matched = &targets[i]
				break
			}
		}
		if matched == nil {
			continue
		}

		entry, err := ParseManifestLine(lineNum, line)
		if err != nil || entry == nil {
			continue
		}

		if err := onEntry(matched.Original, *entry); err != nil {
			return count, err
		}
		count++
	}

	return count, scanner.Err()
}

// AutoDetectStrmText attempts to locate strm.txt in candidate locations.
func AutoDetectStrmText(explicitPath string, sqlitePath string) string {
	candidates := []string{
		explicitPath,
		filepath.Join(filepath.Dir(sqlitePath), "strm.txt"),
		"/xiaoya-db/strm.txt",
		"/data/strm.txt",
		"/vol1/1000/xiaoya/strm.txt",
	}

	for _, c := range candidates {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" {
			continue
		}
		if fi, err := os.Stat(trimmed); err == nil && !fi.IsDir() && fi.Size() > 0 {
			return trimmed
		}
	}
	return explicitPath
}
