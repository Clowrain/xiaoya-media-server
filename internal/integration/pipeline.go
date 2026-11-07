package integration

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"xiaoya-media-server/internal/config"
	"xiaoya-media-server/internal/embyserver"
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

	readerMu sync.Mutex
	reader   *xiaoya.Reader

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
		OnMediaMaterialized: func(localPath string) {
			p.markDirtyDir(filepath.Dir(localPath))
		},
	})

	_ = p.EnsureSQLiteReader(ctx)
	p.discovery = discovery.NewService(store, p.reader, p.strmEng)

	// Clean up legacy erroneous default roots if user has not customized them
	_, _ = store.DB().ExecContext(ctx, "DELETE FROM monitor_roots WHERE root_path IN ('/每日更新/电视剧/国产剧（115）', '/每日更新/电视剧/国产剧') AND (last_error LIKE '%not found%' OR last_error LIKE '%no such table%')")

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
func (p *Pipeline) FlushScopedLibraryScans() []string {
	p.dirtyDirsMu.Lock()
	dirs := make([]string, 0, len(p.dirtyDirs))
	for d := range p.dirtyDirs {
		dirs = append(dirs, d)
	}
	p.dirtyDirs = make(map[string]bool)
	p.dirtyDirsMu.Unlock()

	if p.app != nil {
		for _, d := range dirs {
			p.app.TriggerScopedPathScan(d)
		}
	}
	return dirs
}

// EnsureSQLiteReader opens or verifies the read-only Xiaoya SQLite connection lazily so startup
// succeeds even before external container volumes are populated.
func (p *Pipeline) EnsureSQLiteReader(ctx context.Context) error {
	p.readerMu.Lock()
	defer p.readerMu.Unlock()

	if p.reader != nil {
		if err := p.reader.VerifyReadOnly(ctx); err == nil {
			return nil
		}
		_ = p.reader.Close()
		p.reader = nil
	}

	dbPath := strings.TrimSpace(p.cfg.Xiaoya.SQLitePath)
	if dbPath == "" {
		return os.ErrNotExist
	}
	if _, err := os.Stat(dbPath); err != nil {
		return err
	}
	r, err := xiaoya.OpenReader(dbPath)
	if err != nil {
		return err
	}
	if err := r.VerifyReadOnly(ctx); err != nil {
		_ = r.Close()
		return err
	}
	p.reader = r
	if p.discovery != nil {
		p.discovery.SetReader(r)
	}
	return nil
}

func (p *Pipeline) Discovery() *discovery.Service {
	return p.discovery
}

func (p *Pipeline) STRMEngine() *strm.Engine {
	return p.strmEng
}

func (p *Pipeline) Store() *discovery.SQLStore {
	return p.store
}

func (p *Pipeline) RunFullSyncRoot(ctx context.Context, rootID string) (discovery.SyncBatch, error) {
	_ = p.EnsureSQLiteReader(ctx)
	batch, err := p.discovery.ScanRootFull(ctx, rootID)
	p.FlushScopedLibraryScans()
	return batch, err
}

func (p *Pipeline) RunIncrementalSyncRoot(ctx context.Context, rootID string) (discovery.SyncBatch, error) {
	_ = p.EnsureSQLiteReader(ctx)
	batch, err := p.discovery.ScanRootIncremental(ctx, rootID)
	p.FlushScopedLibraryScans()
	return batch, err
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
