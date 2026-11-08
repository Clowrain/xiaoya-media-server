package strm

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xiaoya-media-server/internal/metadata/tmdb"
	"xiaoya-media-server/internal/resource/discovery"
)

type PathStore interface {
	ClaimMaterializedPath(context.Context, string, string) (string, error)
	ReleaseUnusedPath(context.Context, string, string) error
}

type Config struct {
	PathStore               PathStore
	OutputRoot              string
	PreserveSourceStructure bool
	MaxPathLength           int
	AttachmentsMode         string // "full", "subtitles", "none"
	TargetBaseURL           string // e.g. "http://xiaoya.host:5678"
	DownloadBaseURL         string // Optional internal AList HTTP address for downloading attachments (defaults to TargetBaseURL)
	ForceReplaceHost        bool
	RetryDelay              time.Duration
	HTTPClient              *http.Client
	AttachmentStore         *discovery.SQLStore
	OnMediaMaterialized     func(localPath string)
}

type Engine struct {
	cfg     Config
	tracker *CollisionTracker
	client  *http.Client
}

func NewEngine(cfg Config) *Engine {
	if cfg.MaxPathLength <= 0 {
		cfg.MaxPathLength = DefaultMaxSTRMPathRunes
	}
	if cfg.AttachmentsMode == "" {
		cfg.AttachmentsMode = "full"
	}
	if cfg.TargetBaseURL == "" {
		cfg.TargetBaseURL = "http://xiaoya.host:5678"
	}
	if cfg.DownloadBaseURL == "" {
		cfg.DownloadBaseURL = cfg.TargetBaseURL
	}
	cli := cfg.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 30 * time.Second}
	}
	return &Engine{
		cfg:     cfg,
		tracker: NewCollisionTracker(),
		client:  cli,
	}
}

func sha256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func randSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// WriteAtomicIdempotent writes `data` to `targetPath` using a temp file + fsync + os.Rename.
// If `targetPath` already exists with identical SHA-256 bytes, it skips writing to preserve mtime.
// If OS path creation fails due to special characters, it falls back to ApplyFallbackTranslation.
func WriteAtomicIdempotent(outputRoot, targetPath string, data []byte) (actualPath string, changed bool, err error) {
	changed, err = writeAtomicAt(outputRoot, targetPath, data)
	if err == nil {
		return targetPath, changed, nil
	}
	fallbackPath := ApplyFallbackTranslation(outputRoot, targetPath)
	if fallbackPath == targetPath {
		return targetPath, false, err
	}
	changed, fbErr := writeAtomicAt(outputRoot, fallbackPath, data)
	if fbErr != nil {
		return targetPath, false, fmt.Errorf("write %q failed (%v); fallback %q also failed (%w)", targetPath, err, fallbackPath, fbErr)
	}
	return fallbackPath, changed, nil
}

func writeAtomicAt(root, path string, data []byte) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	if !filepath.IsLocal(rel) || rel == "." {
		return false, fmt.Errorf("目标超出输出目录：%s", path)
	}
	if err = os.MkdirAll(root, 0755); err != nil {
		return false, err
	}
	fs, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer fs.Close()
	if old, e := fs.ReadFile(rel); e == nil && bytes.Equal(old, data) {
		return false, nil
	}
	dir := filepath.Dir(rel)
	if err = fs.MkdirAll(dir, 0755); err != nil {
		return false, err
	}
	tmp := filepath.Join(dir, ".strm-"+randSuffix())
	f, err := fs.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	defer fs.Remove(tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return false, err
	}
	if err = fs.Rename(tmp, rel); err != nil {
		return false, err
	}
	return true, nil
}

// isValidNFOPayload enforces V1.0 `prefer` NFO rules: non-empty, not HTML error page, not AList JSON error.
func isValidNFOPayload(b []byte) bool {
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))
	if len(trimmed) == 0 {
		return false
	}
	lower := strings.ToLower(string(trimmed[:min(len(trimmed), 256)]))
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		return false
	}
	if strings.HasPrefix(lower, "{") && strings.Contains(lower, `"code"`) && strings.Contains(lower, `"message"`) {
		return false
	}
	return strings.Contains(lower, "<")
}

func (e *Engine) downloadAttachmentBytes(ctx context.Context, sourcePath, sourceContent string) (string, []byte, error) {
	dlURL, err := BuildSTRMURL(sourcePath, sourceContent, e.cfg.DownloadBaseURL, true)
	if err != nil {
		return "", nil, err
	}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		if attempt > 1 && e.cfg.RetryDelay > 0 {
			select {
			case <-ctx.Done():
				return dlURL, nil, ctx.Err()
			case <-time.After(e.cfg.RetryDelay):
			}
		}
		reqCtx, reqCancel := context.WithTimeout(ctx, 4*time.Second)
		req, rErr := http.NewRequestWithContext(reqCtx, http.MethodGet, dlURL, nil)
		if rErr != nil {
			reqCancel()
			return dlURL, nil, rErr
		}
		resp, doErr := e.client.Do(req)
		if doErr != nil {
			reqCancel()
			lastErr = doErr
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		reqCancel()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("attachment HTTP %d", resp.StatusCode)
			continue
		}
		if len(body) == 0 {
			lastErr = errors.New("empty attachment response body")
			continue
		}
		return dlURL, body, nil
	}
	return dlURL, nil, lastErr
}

// GenerateSTRM generates or updates a single `.strm` file according to strm.py/strm2.py rules.
func (e *Engine) GenerateSTRM(outputRoot, sourcePath, relPath, sourceContent string) (actualPath string, strmURL string, changed bool, err error) {
	root := outputRoot
	if strings.TrimSpace(root) == "" {
		root = e.cfg.OutputRoot
	}
	targetPath, err := MapLocalSTRMPath(root, sourcePath, relPath, e.cfg.PreserveSourceStructure, e.cfg.MaxPathLength, e.tracker)
	if err != nil {
		return "", "", false, err
	}
	strmURL, err = BuildSTRMURL(sourcePath, sourceContent, e.cfg.TargetBaseURL, e.cfg.ForceReplaceHost)
	if err != nil {
		return targetPath, "", false, err
	}
	actualPath, changed, err = WriteAtomicIdempotent(root, targetPath, []byte(strmURL))
	if err == nil && changed && e.cfg.OnMediaMaterialized != nil {
		e.cfg.OnMediaMaterialized(actualPath)
	}
	return actualPath, strmURL, changed, nil
}

// SyncAttachment downloads and atomically writes a companion subtitle, image, or NFO file.
// Never creates empty NFOs, and updates existing NFOs when remote SHA-256 changes.
func (e *Engine) SyncAttachment(ctx context.Context, resID, outputRoot, sourcePath, relPath, sourceContent string, cat NodeCategory) (string, error) {
	root := outputRoot
	if strings.TrimSpace(root) == "" {
		root = e.cfg.OutputRoot
	}
	localPath, err := MapLocalAttachmentPath(root, sourcePath, relPath, e.cfg.PreserveSourceStructure)
	if err != nil {
		return "", err
	}

	dlURL, payload, err := e.downloadAttachmentBytes(ctx, sourcePath, sourceContent)
	if err != nil {
		return localPath, err
	}
	if cat == CategoryNFO && !isValidNFOPayload(payload) {
		return localPath, errors.New("rejected invalid or empty NFO payload under prefer policy")
	}

	actualPath, changed, err := WriteAtomicIdempotent(root, localPath, payload)
	if err != nil {
		return localPath, err
	}

	if e.cfg.AttachmentStore != nil && resID != "" {
		attType := "SUBTITLE"
		switch cat {
		case CategoryImage:
			attType = "IMAGE"
		case CategoryNFO:
			attType = "NFO"
		}
		_ = e.cfg.AttachmentStore.UpsertAttachment(ctx, discovery.MediaAttachment{
			ResourceID:     resID,
			AttachmentType: attType,
			Ext:            strings.TrimPrefix(strings.ToLower(filepath.Ext(actualPath)), "."),
			SourceURL:      dlURL,
			LocalPath:      actualPath,
			ContentHash:    sha256Bytes(payload),
			SizeBytes:      int64(len(payload)),
			Status:         "SYNCED",
			SyncedAt:       time.Now().Unix(),
		})
	}
	if changed && e.cfg.OnMediaMaterialized != nil {
		e.cfg.OnMediaMaterialized(actualPath)
	}
	return actualPath, nil
}

func (e *Engine) writeOwnedSTRM(ctx context.Context, root string, res discovery.MediaResource) (string, error) {
	data, err := BuildSTRMURL(res.SourcePath, res.SourceContent, e.cfg.TargetBaseURL, e.cfg.ForceReplaceHost)
	if err != nil {
		return "", err
	}
	store := e.cfg.PathStore
	p, err := store.ClaimMaterializedPath(ctx, res.ID, res.LocalSTRMPath)
	if err != nil {
		return "", err
	}
	if res.LocalSTRMPath != "" && p != res.LocalSTRMPath {
		return "", fmt.Errorf("旧 STRM 路径归属不明：%s", res.LocalSTRMPath)
	}
	fresh := p == ""
	if fresh {
		var base string
		var standard bool
		base, standard, err = MapStandardEpisodePath(root, res.SourcePath, res.RelPath, e.cfg.PreserveSourceStructure, e.cfg.MaxPathLength, e.tracker)
		if err != nil {
			return "", err
		}
		if !standard {
			base, err = MapLocalSTRMPath(root, res.SourcePath, res.RelPath, e.cfg.PreserveSourceStructure, e.cfg.MaxPathLength, nil)
		}
		if err != nil {
			return "", err
		}
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(filepath.Base(base), ext)
		for attempt := 0; p == "" && attempt < 32; attempt++ {
			candidate := base
			if attempt > 0 {
				suffix := "_" + shortSHA1(fmt.Sprintf("%s:%d", res.ID, attempt))
				candidate = filepath.Join(filepath.Dir(base), truncateUTF8BaseBytes(stem, 255-len(suffix)-len(ext))+suffix+ext)
			}
			if _, err = os.Lstat(candidate); err == nil {
				continue
			}
			if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
			p, err = store.ClaimMaterializedPath(ctx, res.ID, candidate)
			if err != nil {
				return "", err
			}
		}
		if p == "" {
			return "", fmt.Errorf("无可用 STRM 路径：%s", base)
		}
	}
	_, err = writeAtomicAt(root, p, []byte(data))
	if err != nil && fresh {
		fallback := ApplyFallbackTranslation(root, p)
		// Never fall back to a pre-existing file, even if it has no recorded owner.
		if _, statErr := os.Lstat(fallback); fallback != p && errors.Is(statErr, os.ErrNotExist) {
			if e := store.ReleaseUnusedPath(ctx, res.ID, p); e != nil {
				return "", errors.Join(err, e)
			}
			if claimed, e := store.ClaimMaterializedPath(ctx, res.ID, fallback); e != nil {
				return "", errors.Join(err, e)
			} else if claimed == fallback {
				p = claimed
				_, err = writeAtomicAt(root, p, []byte(data))
			}
		}
	}
	if err != nil {
		return "", err
	}
	if res.OriginType == discovery.OriginXiaoyaSQLite {
		if err := writeStandardNFO(root, p, res); err != nil {
			return "", err
		}
	}
	if e.cfg.OnMediaMaterialized != nil {
		e.cfg.OnMediaMaterialized(p)
	}
	return p, nil
}

type standardNFO struct {
	XMLName xml.Name `xml:"movie"`
	Title   string   `xml:"title,omitempty"`
}

type standardEpisodeNFO struct {
	XMLName xml.Name `xml:"episodedetails"`
	Title   string   `xml:"title,omitempty"`
	Season  int      `xml:"season,omitempty"`
	Episode int      `xml:"episode,omitempty"`
}

func writeStandardNFO(root, strmPath string, res discovery.MediaResource) error {
	base := strings.TrimSuffix(strmPath, filepath.Ext(strmPath))
	stem := filepath.Base(base)
	title, _ := tmdb.ParseTitleAndYear(stem)
	if title == "" {
		title = res.Name
	}
	n := standardNFO{Title: title}
	var payload []byte
	var err error
	if season, episode, ok := tmdb.ParseSeasonEpisode(stem, 0); ok {
		ep := standardEpisodeNFO{Title: title, Season: season, Episode: episode}
		if dir := filepath.Base(filepath.Dir(filepath.Dir(strmPath))); dir != "." && dir != "" {
			if seriesTitle, _ := tmdb.ParseTitleAndYear(dir); seriesTitle != "" {
				ep.Title = seriesTitle
			}
		}
		payload, err = xml.MarshalIndent(ep, "", "  ")
	} else {
		payload, err = xml.MarshalIndent(n, "", "  ")
	}
	if len(payload) == 0 {
		payload, err = xml.MarshalIndent(standardNFO{Title: res.Name}, "", "  ")
	}
	if err != nil {
		return err
	}
	payload = append([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"), payload...)
	_, _, err = WriteAtomicIdempotent(root, base+".nfo", payload)
	return err
}

// ProcessResource implements discovery.ResourceProcessor, routing discovered file nodes
// to STRM generation or companion attachment sync while ensuring attachment failures never block STRM ingestion.
func (e *Engine) ProcessResource(ctx context.Context, _ discovery.MonitorRoot, res discovery.MediaResource, _ string) (string, error) {
	if res.IsDir {
		return "", nil
	}
	root := e.cfg.OutputRoot
	if res.OutputDir != "" {
		root = res.OutputDir
	}
	cat := ClassifyFile(res.Name, res.SourceContent, e.cfg.AttachmentsMode)
	switch cat {
	case CategoryMedia:
		if e.cfg.PathStore != nil {
			return e.writeOwnedSTRM(ctx, root, res)
		}
		actualPath, _, _, err := e.GenerateSTRM(root, res.SourcePath, res.RelPath, res.SourceContent)
		return actualPath, err
	case CategorySubtitle, CategoryImage, CategoryNFO:
		actualPath, err := e.SyncAttachment(ctx, res.ID, root, res.SourcePath, res.RelPath, res.SourceContent, cat)
		// Per Spec Section 5.4 & 5.7: attachment download failure is recorded in media_attachments
		// if needed, and returned so retry queue can retry without blocking sibling STRM files.
		return actualPath, err
	default:
		return "", nil
	}
}

func (e *Engine) IsAttachment(r discovery.MediaResource) bool {
	switch ClassifyFile(r.Name, r.SourceContent, e.cfg.AttachmentsMode) {
	case CategoryImage, CategorySubtitle, CategoryNFO:
		return true
	}
	return false
}

func (e *Engine) RequiresSTRMRepair(r discovery.MediaResource) bool {
	expected, err := BuildSTRMURL(r.SourcePath, r.SourceContent, e.cfg.TargetBaseURL, e.cfg.ForceReplaceHost)
	if err != nil {
		return true
	}
	actual, err := os.ReadFile(r.LocalSTRMPath)
	return err != nil || !bytes.Equal(actual, []byte(expected))
}
