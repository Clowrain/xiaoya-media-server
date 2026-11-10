package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/embyserver"
	"xiaoya-media-server/internal/metadata/tmdb"
	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/resource/xiaoya"
	"xiaoya-media-server/internal/strm"
)

// Pipeline unites the Xiaoya SQLite Discovery Engine, the Python-compatible STRM & Attachment Engine,
// and the Emby-compatible Media Server Scanner.
type Pipeline struct {
	cfg       *config.Config
	app       *embyserver.App
	store     *discovery.SQLStore
	discovery *discovery.Service
	strmEng   *strm.Engine

	syncMu   sync.Mutex
	readerMu sync.Mutex
	reader   *xiaoya.Reader
	manifest *xiaoya.ManifestScanner

	dirtyDirsMu sync.Mutex
	dirtyDirs   map[string]bool
}

func NewPipeline(ctx context.Context, cfg *config.Config, app *embyserver.App, store *discovery.SQLStore) (*Pipeline, error) {
	p := &Pipeline{
		cfg:       cfg,
		app:       app,
		store:     store,
		dirtyDirs: make(map[string]bool),
	}

	downloadBase := cfg.AList.BaseURL
	if strings.TrimSpace(downloadBase) == "" {
		downloadBase = cfg.STRM.TargetBaseURL
	}

	p.strmEng = strm.NewEngine(strm.Config{
		OutputRoot:              cfg.STRM.OutputRoot,
		PreserveSourceStructure: cfg.STRM.PreserveSourceStructure,
		MaxPathLength:           cfg.STRM.MaxPathLength,
		AttachmentsMode:         cfg.STRM.Attachments,
		TargetBaseURL:           cfg.STRM.TargetBaseURL,
		DownloadBaseURL:         downloadBase,
		AttachmentStore:         store,
		PathStore:               store,
		OnMediaMaterialized: func(localPath string) {
			p.markDirtyDir(filepath.Dir(localPath))
		},
	})

	manifestPath := xiaoya.AutoDetectStrmText(cfg.Xiaoya.ResolveStrmTextPath(), cfg.Xiaoya.SQLitePath)
	p.manifest = xiaoya.NewManifestScanner(manifestPath)
	p.discovery = discovery.NewService(store, nil, p.strmEng)

	if cfg.Monitor.Enabled && len(cfg.Monitor.Roots) > 0 {
		if _, err := p.discovery.RegisterMonitorRoots(ctx, cfg.Monitor.Roots, cfg.Monitor.Interval); err != nil {
			log.Printf("register initial monitor roots warning: %v", err)
		}
	}
	return p, nil
}

func (p *Pipeline) markDirtyDir(dir string) {
	if dir == "" {
		return
	}
	p.dirtyDirsMu.Lock()
	p.dirtyDirs[filepath.Clean(dir)] = true
	p.dirtyDirsMu.Unlock()
}

// FlushScopedLibraryScans triggers scoped Emby library scans for any directories touched during STRM/attachment sync.
func (p *Pipeline) FlushScopedLibraryScans(files ...string) []string {
	for _, file := range files {
		p.markDirtyDir(filepath.Dir(file))
	}
	p.dirtyDirsMu.Lock()
	dirs := make([]string, 0, len(p.dirtyDirs))
	for d := range p.dirtyDirs {
		dirs = append(dirs, d)
	}
	p.dirtyDirs = make(map[string]bool)
	p.dirtyDirsMu.Unlock()

	if p.app != nil {
		p.app.TriggerScopedPathScans(dirs)
	}
	return dirs
}

// EnsureSQLiteReader is a no-op kept for backwards compatibility since Xiaoya SQLite has been superseded by strm.txt manifest.
func (p *Pipeline) EnsureSQLiteReader(ctx context.Context) error {
	return nil
}

func (p *Pipeline) Discovery() *discovery.Service {
	return p.discovery
}

func (p *Pipeline) ManifestScanner() *xiaoya.ManifestScanner {
	return p.manifest
}

func (p *Pipeline) STRMEngine() *strm.Engine {
	return p.strmEng
}

// ImportManifestPath resolves an administrator-selected path or keyword against Xiaoya's
// authoritative strm.txt manifest and imports that subtree or matching works.
func (p *Pipeline) ImportManifestPath(ctx context.Context, rootPath, libraryID, outputDir string, track *bool) (discovery.SyncBatch, error) {
	if p.manifest == nil || !p.manifest.Available() {
		return discovery.SyncBatch{}, errors.New("strm.txt 权威清单未找到，请检查宿主机小雅主目录是否正确挂载")
	}
	if p.app != nil {
		var err error
		outputDir, err = p.app.ResolveImportTarget(libraryID, outputDir)
		if err != nil {
			return discovery.SyncBatch{}, err
		}
	}
	normPath, _, err := xiaoya.NormalizeMonitorPath(rootPath)
	if err != nil {
		return discovery.SyncBatch{}, err
	}
	cleanName := filepath.Base(strings.TrimRight(normPath, "/"))

	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	return p.importManifestStream(ctx, normPath, cleanName, libraryID, outputDir, track)

}

// ImportSQLitePath provides backwards compatibility by delegating directly to ImportManifestPath.
func (p *Pipeline) ImportSQLitePath(ctx context.Context, rootPath, libraryID, outputDir string, track *bool) (discovery.SyncBatch, error) {
	return p.ImportManifestPath(ctx, rootPath, libraryID, outputDir, track)
}

func (p *Pipeline) tmdbClient() *tmdb.Client {
	if p == nil {
		return nil
	}
	if p.app != nil {
		if cli := p.app.TMDBClient(); cli != nil {
			return cli
		}
	}
	if p.cfg == nil || !p.cfg.Metadata.TMDB.Enabled || strings.TrimSpace(p.cfg.Metadata.TMDB.APIKey) == "" {
		return nil
	}
	proxyStr := strings.TrimSpace(p.cfg.Metadata.TMDB.ProxyURL)
	if proxyStr == "" {
		proxyStr = strings.TrimSpace(os.Getenv("TMDB_PROXY"))
	}
	interval := time.Duration(p.cfg.Metadata.TMDB.RateLimitMs) * time.Millisecond
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	return tmdb.NewClient(tmdb.Options{
		APIKey:      p.cfg.Metadata.TMDB.APIKey,
		Language:    p.cfg.Metadata.TMDB.Language,
		BaseURL:     p.cfg.Metadata.TMDB.BaseURL,
		ImageURL:    p.cfg.Metadata.TMDB.ImageURL,
		ProxyURL:    proxyStr,
		MinInterval: interval,
	})
}

// RunMetadataMatchJobs performs bounded TMDB matching for deterministic V2
// identities. A candidate is auto-bound only when it is unambiguous; otherwise
// the recognition row remains available for administrator confirmation.
func (p *Pipeline) RunMetadataMatchJobs(ctx context.Context, limit int) error {
	cli := p.tmdbClient()
	if cli == nil {
		return nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	worker := "metadata-match"
	cache := make(map[string]tmdb.MediaMetadata, limit)
	type seasonBundle struct {
		season   *tmdb.MediaMetadata
		episodes map[int]*tmdb.MediaMetadata
	}
	seasonCache := make(map[string]seasonBundle, limit)
	for i := 0; i < limit; i++ {
		job, err := p.store.ClaimV2Job(ctx, worker, 5*time.Minute)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var payload struct {
			ResourceID string `json:"resource_id"`
		}
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil || payload.ResourceID == "" {
			_ = p.store.FailV2Job(ctx, job.ID, "metadata_match payload 无效", time.Now().Unix()+300)
			continue
		}
		res, err := p.store.GetResource(ctx, payload.ResourceID)
		if err != nil {
			_ = p.store.FailV2Job(ctx, job.ID, err.Error(), time.Now().Unix()+300)
			continue
		}
		if job.Revision > 0 && res.LastChangedAt > 0 && job.Revision != res.LastChangedAt {
			_ = p.store.CompleteV2Job(ctx, job.ID) // stale revision; a newer scan queued its own job.
			continue
		}
		identity := discovery.ParseMediaIdentity(res)
		if identity.State != "READY_FOR_MATCH" {
			_ = p.store.MarkV2RecognitionState(ctx, res.ID, "NEEDS_CONFIRMATION", identity.Evidence)
			_ = p.store.CompleteV2Job(ctx, job.ID)
			continue
		}
		cacheKey := identity.MediaType + ":" + identity.Title + ":" + fmt.Sprint(identity.Year)
		meta, ok := cache[cacheKey]
		if !ok {
			var result tmdb.SearchResult
			var matched bool
			if identity.MediaType == "MOVIE" {
				results, searchErr := cli.SearchMovie(ctx, identity.Title, identity.Year)
				if searchErr != nil {
					_ = p.store.FailV2Job(ctx, job.ID, searchErr.Error(), time.Now().Unix()+300)
					continue
				}
				result, matched = chooseTMDBResult(results, identity.Title, identity.Year)
			} else {
				results, searchErr := cli.SearchTV(ctx, identity.Title, identity.Year)
				if searchErr != nil {
					_ = p.store.FailV2Job(ctx, job.ID, searchErr.Error(), time.Now().Unix()+300)
					continue
				}
				result, matched = chooseTMDBResult(results, identity.Title, identity.Year)
			}
			if !matched {
				_ = p.store.MarkV2RecognitionState(ctx, res.ID, "NEEDS_CONFIRMATION", "TMDB 候选存在歧义或没有精确匹配")
				_ = p.store.CompleteV2Job(ctx, job.ID)
				continue
			}
			if identity.MediaType == "MOVIE" {
				full, getErr := cli.GetMovie(ctx, fmt.Sprint(result.ID))
				if getErr != nil {
					_ = p.store.FailV2Job(ctx, job.ID, getErr.Error(), time.Now().Unix()+300)
					continue
				}
				meta = *full
			} else {
				full, getErr := cli.GetTV(ctx, fmt.Sprint(result.ID))
				if getErr != nil {
					_ = p.store.FailV2Job(ctx, job.ID, getErr.Error(), time.Now().Unix()+300)
					continue
				}
				meta = *full
			}
			cache[cacheKey] = meta
		}
		var seasonMeta, episodeMeta *tmdb.MediaMetadata
		if identity.MediaType != "MOVIE" {
			seasonKey := meta.TMDBID + ":" + fmt.Sprint(identity.SeasonNumber)
			bundle, ok := seasonCache[seasonKey]
			if !ok {
				season, episodes, seasonErr := cli.GetTVSeason(ctx, meta.TMDBID, identity.SeasonNumber)
				if seasonErr != nil {
					_ = p.store.FailV2Job(ctx, job.ID, seasonErr.Error(), time.Now().Unix()+300)
					continue
				}
				bundle = seasonBundle{season: season, episodes: episodes}
				seasonCache[seasonKey] = bundle
			}
			seasonMeta = bundle.season
			episodeMeta = bundle.episodes[identity.EpisodeNumber]
		}
		mediaID := StableMediaID(identity)
		if err := p.store.UpdateV2Match(ctx, res.ID, mediaID, meta.TMDBID, meta.Title, meta.Year, meta.PosterURL, meta.BackdropURL); err != nil {
			_ = p.store.FailV2Job(ctx, job.ID, err.Error(), time.Now().Unix()+300)
			continue
		}
		if err := p.publishMatchedArtifacts(ctx, res, identity, &meta, seasonMeta, episodeMeta); err != nil {
			_ = p.store.FailV2Job(ctx, job.ID, err.Error(), time.Now().Unix()+300)
			continue
		}
		if err := p.store.CompleteV2Job(ctx, job.ID); err != nil {
			return err
		}
	}
	return nil
}

// ProcessPendingScrapes delegates to the underlying Emby app to retry transient scrape failures
// and refresh ongoing or newly-listed TMDB series.
func (p *Pipeline) ProcessPendingScrapes(ctx context.Context, limit int) (int, error) {
	if p == nil || p.app == nil {
		return 0, nil
	}
	return p.app.ProcessPendingScrapes(ctx, limit)
}

// ConfirmMetadataMatch applies an administrator-selected TMDB ID and uses the
// same media/version identity update as automatic matching.
func (p *Pipeline) ConfirmMetadataMatch(ctx context.Context, resourceID, externalID string) error {
	if p == nil || p.cfg == nil || !p.cfg.Metadata.TMDB.Enabled || strings.TrimSpace(p.cfg.Metadata.TMDB.APIKey) == "" {
		return errors.New("TMDB 未配置")
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return errors.New("TMDB ID 不能为空")
	}
	res, err := p.store.GetResource(ctx, resourceID)
	if err != nil {
		return err
	}
	identity := discovery.ParseMediaIdentity(res)
	if identity.State == "" || identity.MediaType == "" {
		return errors.New("资源无法确定电影或节目类型")
	}
	cli := p.tmdbClient()
	if cli == nil {
		return errors.New("TMDB 服务未配置或未启用")
	}
	var meta *tmdb.MediaMetadata
	if identity.MediaType == "MOVIE" {
		meta, err = cli.GetMovie(ctx, externalID)
	} else {
		meta, err = cli.GetTV(ctx, externalID)
	}
	if err != nil {
		return err
	}
	if meta == nil {
		return errors.New("TMDB 没有返回媒体详情")
	}
	var seasonMeta, episodeMeta *tmdb.MediaMetadata
	if identity.MediaType != "MOVIE" {
		var episodes map[int]*tmdb.MediaMetadata
		var seasonErr error
		seasonMeta, episodes, seasonErr = cli.GetTVSeason(ctx, meta.TMDBID, identity.SeasonNumber)
		if seasonErr != nil {
			return seasonErr
		}
		episodeMeta = episodes[identity.EpisodeNumber]
	}
	if err := p.store.UpdateV2Match(ctx, resourceID, StableMediaID(identity), meta.TMDBID, meta.Title, meta.Year, meta.PosterURL, meta.BackdropURL); err != nil {
		return err
	}
	if err := p.publishMatchedArtifacts(ctx, res, identity, meta, seasonMeta, episodeMeta); err != nil {
		return err
	}
	return p.store.CompleteV2JobsForScope(ctx, resourceID)
}

func StableMediaID(identity discovery.ParsedMediaIdentity) string {
	if identity.MediaType == "MOVIE" {
		return discovery.StableID("media:MOVIE:" + identity.Title + ":" + fmt.Sprint(identity.Year))
	}
	return discovery.StableID("media:EPISODE:" + identity.ParentID + ":" + fmt.Sprint(identity.SeasonNumber) + ":" + fmt.Sprint(identity.EpisodeNumber))
}

func chooseTMDBResult(results []tmdb.SearchResult, title string, year int) (tmdb.SearchResult, bool) {
	if len(results) == 0 {
		return tmdb.SearchResult{}, false
	}
	want := normalizeMatchTitle(title)
	var exact []tmdb.SearchResult
	for _, result := range results {
		if normalizeMatchTitle(result.Title) == want || normalizeMatchTitle(result.Name) == want || normalizeMatchTitle(result.OriginalTitle) == want {
			if year == 0 || result.Year == 0 || result.Year == year {
				exact = append(exact, result)
			}
		}
	}
	if len(exact) == 1 {
		return exact[0], true
	}
	if len(results) == 1 && (year == 0 || results[0].Year == 0 || results[0].Year == year) {
		return results[0], true
	}
	return tmdb.SearchResult{}, false
}

func normalizeMatchTitle(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	v = strings.NewReplacer(" ", "", "　", "", ".", "", "_", "", "-", "", "！", "!", "（", "(", "）", ")").Replace(v)
	return v
}

func (p *Pipeline) Store() *discovery.SQLStore {
	return p.store
}

// Manifest returns the active ManifestScanner for strm.txt.
func (p *Pipeline) Manifest() *xiaoya.ManifestScanner {
	return p.manifest
}

// SyncRootsFromManifest scans strm.txt in a single pass across all provided roots,
// importing discovered media items and updating root sync timestamps.
func (p *Pipeline) SyncRootsFromManifest(ctx context.Context, roots []discovery.MonitorRoot) (map[string]discovery.SyncBatch, error) {
	p.syncMu.Lock()
	defer p.syncMu.Unlock()
	result, err := p.syncManifestRoots(ctx, roots)
	if err != nil {
		for _, r := range roots {
			if b, ok := result[r.ID]; ok && b.ID != "" {
				continue
			}
			b, e := p.store.CreateBatch(ctx, r.ID, r.RootPath, "full", false)
			if e != nil {
				err = errors.Join(err, e)
				continue
			}
			b.Status = discovery.BatchFailed
			b.ErrorMessage = err.Error()
			b.FinishedAt = time.Now().Unix()
			err = errors.Join(err, p.store.FinishBatch(ctx, b), p.store.UpdateMonitorRootStatus(ctx, r.ID, 0, b.ID, b.Status, b.ErrorMessage, b.FinishedAt))
			result[r.ID] = b
		}
	}
	return result, err
}

func (p *Pipeline) RunFullSyncRoot(ctx context.Context, rootID string) (discovery.SyncBatch, error) {
	rootPath := rootID
	if p.store != nil {
		if r, err := p.store.GetMonitorRoot(ctx, rootID); err == nil {
			rootPath = r.RootPath
		}
	}
	if p.manifest == nil || !p.manifest.Available() {
		err := errors.New("strm.txt 权威清单未就绪，请检查宿主机小雅主目录挂载")
		var batch discovery.SyncBatch
		if p.store != nil {
			batch, _ = p.store.CreateBatch(ctx, rootID, rootPath, "full", false)
			batch.Status = discovery.BatchFailed
			batch.ErrorMessage = err.Error()
			batch.FinishedAt = time.Now().Unix()
			_ = p.store.FinishBatch(ctx, batch)
			_ = p.store.UpdateMonitorRootStatus(ctx, rootID, 0, batch.ID, discovery.BatchFailed, err.Error(), batch.FinishedAt)
		} else {
			batch = discovery.SyncBatch{RootID: rootID, Status: discovery.BatchFailed, ErrorMessage: err.Error()}
		}
		return batch, err
	}
	root, err := p.store.GetMonitorRoot(ctx, rootID)
	if err != nil {
		return discovery.SyncBatch{}, err
	}
	batches, sErr := p.SyncRootsFromManifest(ctx, []discovery.MonitorRoot{root})
	if b, ok := batches[rootID]; ok {
		return b, sErr
	}
	if sErr != nil {
		return discovery.SyncBatch{RootID: rootID, Status: discovery.BatchFailed}, sErr
	}

	return discovery.SyncBatch{RootID: rootID, Status: discovery.BatchFailed}, errors.New("监控根未生成同步结果")
}

func (p *Pipeline) RunIncrementalSyncRoot(ctx context.Context, rootID string) (discovery.SyncBatch, error) {
	return p.RunFullSyncRoot(ctx, rootID)
}

func (p *Pipeline) finishSync(batch discovery.SyncBatch) discovery.SyncBatch {
	if p.app != nil && len(batch.MaterializedSTRMs) > 0 {
		batch.UnindexedSTRMCount = len(p.app.UnmatchedLibraryPaths(batch.MaterializedSTRMs))
	}
	batch.MaterializedSTRMs = nil
	p.FlushScopedLibraryScans()
	return batch
}

func (p *Pipeline) Close() error {
	p.readerMu.Lock()
	defer p.readerMu.Unlock()
	if p.reader != nil {
		err := p.reader.Close()
		p.reader = nil
		return err
	}
	return nil
}
