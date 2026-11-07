package alist

import (
	"context"
	"fmt"
	"path"
	"strings"

	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/strm"
)

type ImportRequest struct {
	Path         string `json:"path"`
	IsDir        *bool  `json:"is_dir,omitempty"`
	TrackUpdates bool   `json:"track_updates"`
	LibraryID    string `json:"library_id"`
	OutputDir    string `json:"output_dir"`
}

type ImportSummary struct {
	RootResourceID  string   `json:"root_resource_id"`
	SourcePath      string   `json:"source_path"`
	ImportedFiles   int      `json:"imported_files"`
	ImportedDirs    int      `json:"imported_dirs"`
	FailedFiles     int      `json:"failed_files"`
	GeneratedSTRMs  []string `json:"generated_strms"`
	TrackingEnabled bool     `json:"tracking_enabled"`
}

// Importer handles single-file and whole-directory manual AList imports into the unified resource model,
// as well as periodic follow-up syncing for tracked TV series directories.
type Importer struct {
	client   *Client
	discSvc  *discovery.Service
	strmEng  *strm.Engine
	onSynced func(paths []string)
}

func NewImporter(client *Client, discSvc *discovery.Service, strmEng *strm.Engine, onSynced func(paths []string)) *Importer {
	return &Importer{
		client:   client,
		discSvc:  discSvc,
		strmEng:  strmEng,
		onSynced: onSynced,
	}
}

func (im *Importer) Client() *Client {
	return im.client
}

func (im *Importer) buildPlaybackContentURL(fullPath, sign string) string {
	u, err := strm.BuildSTRMURL(fullPath, "", im.client.BaseURL(), false)
	if err != nil {
		return ""
	}
	if strings.TrimSpace(sign) != "" {
		return u + "?sign=" + strings.TrimSpace(sign)
	}
	return u
}

func (im *Importer) Import(ctx context.Context, req ImportRequest) (ImportSummary, error) {
	normPath := normalizeAListPath(req.Path)
	if normPath == "/" {
		return ImportSummary{}, fmt.Errorf("refusing to import entire AList root '/'")
	}

	isDir := false
	var detail *FileDetail
	if req.IsDir != nil {
		isDir = *req.IsDir
	} else {
		d, err := im.client.Get(ctx, normPath)
		if err != nil {
			return ImportSummary{}, err
		}
		detail = d
		isDir = d.IsDir
	}

	if !isDir {
		if detail == nil {
			d, err := im.client.Get(ctx, normPath)
			if err != nil {
				return ImportSummary{}, err
			}
			detail = d
		}
		contentURL := im.buildPlaybackContentURL(normPath, detail.Sign)
		rel := strings.TrimPrefix(normPath, "/")
		res, err := im.discSvc.RegisterManualResource(
			ctx, normPath, rel, path.Base(normPath), false, contentURL,
			discovery.ManageManualFile, req.LibraryID, req.OutputDir,
		)
		if err != nil {
			return ImportSummary{}, err
		}
		summary := ImportSummary{
			RootResourceID: res.ID,
			SourcePath:     normPath,
			ImportedFiles:  1,
		}
		if res.Status == discovery.StatusFailed {
			summary.FailedFiles = 1
		} else if res.LocalSTRMPath != "" {
			summary.GeneratedSTRMs = []string{res.LocalSTRMPath}
			if im.onSynced != nil {
				im.onSynced(summary.GeneratedSTRMs)
			}
		}
		return summary, nil
	}

	return im.importDirectoryTree(ctx, normPath, req.TrackUpdates, req.LibraryID, req.OutputDir)
}

func (im *Importer) importDirectoryTree(ctx context.Context, rootDir string, trackUpdates bool, libraryID, outputDir string) (ImportSummary, error) {
	mode := discovery.ManageManualSeriesDir
	rootRel := strings.TrimPrefix(rootDir, "/")
	rootRes, err := im.discSvc.RegisterManualResource(
		ctx, rootDir, rootRel, path.Base(rootDir), true, "",
		mode, libraryID, outputDir,
	)
	if err != nil {
		return ImportSummary{}, err
	}
	if !trackUpdates {
		// Mark disabled for automatic periodic follow-up while keeping one-shot import records
		_ = im.discSvc.Store().UpsertManagedResource(ctx, im.discSvc.Store().DB(), discovery.ManagedResource{
			ResourceID:     rootRes.ID,
			ManageMode:     mode,
			LibraryID:      libraryID,
			OutputDir:      outputDir,
			Enabled:        false,
			DeletionPolicy: "keep",
		})
	}

	summary := ImportSummary{
		RootResourceID:  rootRes.ID,
		SourcePath:      rootDir,
		ImportedDirs:    1,
		TrackingEnabled: trackUpdates,
	}

	const maxDepth = 24
	type queueItem struct {
		dirPath string
		depth   int
	}
	queue := []queueItem{{dirPath: rootDir, depth: 0}}

	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		cur := queue[0]
		queue = queue[1:]
		if cur.depth > maxDepth {
			continue
		}

		page := 1
		for {
			entries, total, lErr := im.client.List(ctx, cur.dirPath, page, 200, false)
			if lErr != nil {
				return summary, lErr
			}
			for _, ent := range entries {
				rel := strings.TrimPrefix(ent.FullPath, "/")
				if ent.IsDir {
					_, dErr := im.discSvc.RegisterManualResource(
						ctx, ent.FullPath, rel, ent.Name, true, "",
						mode, libraryID, outputDir,
					)
					if dErr == nil {
						summary.ImportedDirs++
					}
					queue = append(queue, queueItem{dirPath: ent.FullPath, depth: cur.depth + 1})
					continue
				}

				contentURL := im.buildPlaybackContentURL(ent.FullPath, ent.Sign)
				cat := strm.ClassifyFile(ent.Name, contentURL, "full")
				if cat == strm.CategoryIgnored {
					continue
				}
				fRes, fErr := im.discSvc.RegisterManualResource(
					ctx, ent.FullPath, rel, ent.Name, false, contentURL,
					mode, libraryID, outputDir,
				)
				if fErr != nil || fRes.Status == discovery.StatusFailed {
					summary.FailedFiles++
					continue
				}
				summary.ImportedFiles++
				if fRes.LocalSTRMPath != "" {
					summary.GeneratedSTRMs = append(summary.GeneratedSTRMs, fRes.LocalSTRMPath)
				}
			}
			if page*200 >= total || len(entries) == 0 {
				break
			}
			page++
		}
	}

	if len(summary.GeneratedSTRMs) > 0 && im.onSynced != nil {
		im.onSynced(summary.GeneratedSTRMs)
	}
	return summary, nil
}

// SyncTrackedDirectories re-scans all enabled MANUAL_SERIES_DIR directories so newly added episodes
// in manually imported TV shows are automatically ingested (Spec Section 11.6).
func (im *Importer) SyncTrackedDirectories(ctx context.Context) (int, error) {
	store := im.discSvc.Store()
	rows, err := store.DB().QueryContext(ctx, `
SELECT r.source_path, m.library_id, m.output_dir
FROM managed_resources m
JOIN media_resources r ON r.id = m.resource_id
WHERE m.manage_mode = 'MANUAL_SERIES_DIR' AND m.enabled = 1 AND r.is_dir = 1 AND r.origin_type = 'ALIST_MANUAL'`)
	if err != nil {
		return 0, err
	}
	type tracked struct {
		path      string
		libraryID string
		outputDir string
	}
	var dirs []tracked
	for rows.Next() {
		var t tracked
		if rows.Scan(&t.path, &t.libraryID, &t.outputDir) == nil {
			dirs = append(dirs, t)
		}
	}
	rows.Close()

	// Only re-scan top-level tracked roots (skip child Season directories whose parent is already in dirs)
	topSet := map[string]bool{}
	for _, d := range dirs {
		topSet[d.path] = true
	}
	synced := 0
	for _, d := range dirs {
		parent := path.Dir(d.path)
		if parent != "/" && topSet[parent] {
			continue
		}
		if _, sErr := im.importDirectoryTree(ctx, d.path, true, d.libraryID, d.outputDir); sErr == nil {
			synced++
		}
	}
	return synced, nil
}
