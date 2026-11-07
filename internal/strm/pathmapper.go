package strm

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const DefaultMaxSTRMPathRunes = 235

// CollisionTracker tracks truncated 235-rune target paths to match strm.py (`_xxx.strm`) on the first item
// while deterministically disambiguating (`_xxx_<hash6>.strm`) if sibling episodes collide at 235 runes.
type CollisionTracker struct {
	mu     sync.Mutex
	owners map[string]string // localPath -> sourceIdentity
}

func NewCollisionTracker() *CollisionTracker {
	return &CollisionTracker{
		owners: make(map[string]string),
	}
}

func (c *CollisionTracker) claim(candidatePath, sourceIdentity string) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	owner, exists := c.owners[candidatePath]
	if !exists || owner == sourceIdentity {
		c.owners[candidatePath] = sourceIdentity
		return true
	}
	return false
}

func shortSHA1(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:6]
}

// StripMediaStem converts a source filename into its local `.strm` stem:
//   - Decodes literal `%20` to `" "` (matching strm.py L125)
//   - Prevents `.strm.strm` when SQLite node name already ends in `.strm` or `.mp4.strm`
//   - Preserves dotless episode node names like `E01`
func StripMediaStem(filename string) string {
	s := strings.ReplaceAll(filename, "%20", " ")
	if strings.EqualFold(filepath.Ext(s), ".strm") {
		s = strings.TrimSuffix(s, filepath.Ext(s))
	}
	if ext := filepath.Ext(s); ext != "" && IsMediaExtension(ext) {
		stem := strings.TrimSuffix(s, ext)
		if stem != "" {
			s = stem
		}
	}
	return s
}

func sanitizeRelativeSegments(rawRel string) ([]string, error) {
	replaced := strings.ReplaceAll(rawRel, "%20", " ")
	parts := strings.Split(strings.ReplaceAll(replaced, "\\", "/"), "/")
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			return nil, fmt.Errorf("illegal parent traversal segment '..' in %q", rawRel)
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return nil, fmt.Errorf("empty relative path %q", rawRel)
	}
	return clean, nil
}

// truncateUTF8BaseBytes ensures a single filename component never exceeds Linux's 255-byte NAME_MAX limit.
func truncateUTF8BaseBytes(stem string, maxBytes int) string {
	if len(stem) <= maxBytes {
		return stem
	}
	var b strings.Builder
	for _, r := range stem {
		rLen := utf8.RuneLen(r)
		if b.Len()+rLen > maxBytes {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// MapLocalSTRMPath computes the target local `.strm` path under `outputRoot`, enforcing:
//  1. Source structure preservation (full source path vs monitor-relative path)
//  2. `%20` -> space restoration & anti-`.strm.strm`
//  3. 255-byte single-component UTF-8 safety
//  4. 235-rune truncation (`[:235] + "_xxx.strm"`) + deterministic collision suffix (`_xxx_<hash6>.strm`)
//  5. Strict confinement inside `outputRoot`
func MapLocalSTRMPath(outputRoot, sourcePath, relPath string, preserveSourceStructure bool, maxRunes int, tracker *CollisionTracker) (string, error) {
	cleanRoot := filepath.Clean(strings.TrimSpace(outputRoot))
	if cleanRoot == "" || cleanRoot == "." || cleanRoot == "/" {
		return "", fmt.Errorf("invalid strm output_root %q", outputRoot)
	}
	if maxRunes <= 0 {
		maxRunes = DefaultMaxSTRMPathRunes
	}

	chosenRel := sourcePath
	if !preserveSourceStructure && strings.TrimSpace(relPath) != "" {
		chosenRel = relPath
	}
	segs, err := sanitizeRelativeSegments(chosenRel)
	if err != nil {
		return "", err
	}

	lastIdx := len(segs) - 1
	stem := StripMediaStem(segs[lastIdx])
	// Reserve 16 bytes for ".strm" / "_xxx_123456.strm" within 255-byte NAME_MAX
	stem = truncateUTF8BaseBytes(stem, 238)
	segs[lastIdx] = stem

	strmFileTemp := cleanRoot + string(filepath.Separator) + strings.Join(segs, string(filepath.Separator))
	sourceIdentity := sourcePath
	if sourceIdentity == "" {
		sourceIdentity = chosenRel
	}

	runes := []rune(strmFileTemp)
	var finalPath string
	if len(runes) > maxRunes {
		cand1 := string(runes[:maxRunes]) + "_xxx.strm"
		if tracker.claim(cand1, sourceIdentity) {
			finalPath = cand1
		} else {
			keep := maxRunes - 7
			if keep < len([]rune(cleanRoot))+2 {
				keep = maxRunes
			}
			finalPath = string(runes[:keep]) + "_xxx_" + shortSHA1(sourceIdentity) + ".strm"
			tracker.claim(finalPath, sourceIdentity)
		}
	} else {
		cand := strmFileTemp + ".strm"
		if tracker.claim(cand, sourceIdentity) {
			finalPath = cand
		} else {
			finalPath = strmFileTemp + "_" + shortSHA1(sourceIdentity) + ".strm"
			tracker.claim(finalPath, sourceIdentity)
		}
	}

	finalClean := filepath.Clean(finalPath)
	if !strings.HasPrefix(finalClean, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("mapped path %q escapes output_root %q", finalClean, cleanRoot)
	}
	return finalClean, nil
}

// MapLocalAttachmentPath maps a companion attachment (.nfo, .srt, .ass, .jpg, .png, .webp) to its local path.
func MapLocalAttachmentPath(outputRoot, sourcePath, relPath string, preserveSourceStructure bool) (string, error) {
	cleanRoot := filepath.Clean(strings.TrimSpace(outputRoot))
	if cleanRoot == "" || cleanRoot == "." || cleanRoot == "/" {
		return "", fmt.Errorf("invalid attachment output_root %q", outputRoot)
	}
	chosenRel := sourcePath
	if !preserveSourceStructure && strings.TrimSpace(relPath) != "" {
		chosenRel = relPath
	}
	segs, err := sanitizeRelativeSegments(chosenRel)
	if err != nil {
		return "", err
	}
	finalClean := filepath.Clean(cleanRoot + string(filepath.Separator) + strings.Join(segs, string(filepath.Separator)))
	if !strings.HasPrefix(finalClean, cleanRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("mapped attachment path %q escapes output_root %q", finalClean, cleanRoot)
	}
	return finalClean, nil
}

var fallbackCharReplacer = strings.NewReplacer(
	"：", ".",
	":", ".",
	"|", ".",
	"$", ".",
	"%", ".",
	"&", "_",
	"#", ".",
	"+", "_",
	"\\", ".",
	"{", ".",
	"}", ".",
	"'", ".",
)

// ApplyFallbackTranslation applies strm.py L161-177 character fallback translation ONLY to the path
// relative to `outputRoot`, preserving `outputRoot` itself (including Windows drive letters or mount paths).
func ApplyFallbackTranslation(outputRoot, targetPath string) string {
	cleanRoot := filepath.Clean(outputRoot)
	rel, err := filepath.Rel(cleanRoot, targetPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fallbackCharReplacer.Replace(targetPath)
	}
	translatedRel := fallbackCharReplacer.Replace(rel)
	return filepath.Join(cleanRoot, translatedRel)
}
