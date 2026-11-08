package alist

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"

	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/strm"
)

type ImportRequest struct {
	Path         string `json:"path"`
	IsDir        *bool  `json:"is_dir,omitempty"`
	TrackUpdates bool   `json:"track_updates"`
	LibraryID    string `json:"library_id"`
	OutputDir    string `json:"output_dir"`
	Source       string `json:"source,omitempty"`
}

type ImportSummary struct {
	RootResourceID  string   `json:"root_resource_id"`
	SourcePath      string   `json:"source_path"`
	ImportedFiles   int      `json:"imported_files"`
	ImportedDirs    int      `json:"imported_dirs"`
	FailedFiles     int      `json:"failed_files"`
	GeneratedSTRMs  []string `json:"generated_strms"`
	UnindexedSTRMs  []string `json:"unindexed_strms"`
	Errors          []string `json:"errors,omitempty"`
	TrackingEnabled bool     `json:"tracking_enabled"`
	BatchID         string   `json:"batch_id"`
}

var ErrImportInProgress = errors.New("同一 AList 范围已有入库任务")

type Importer struct {
	client        *Client
	discSvc       *discovery.Service
	onSynced      func(paths []string)
	resolveTarget func(libraryID, outputDir string) (string, error)
	unindexed     func(paths []string) []string
	mu            sync.Mutex
	active        map[string]bool
}

func NewImporter(client *Client, discSvc *discovery.Service, _ *strm.Engine, onSynced func(paths []string)) *Importer {
	return &Importer{client: client, discSvc: discSvc, onSynced: onSynced, active: map[string]bool{}}
}

func (im *Importer) SetLibraryTarget(resolve func(string, string) (string, error), unindexed func([]string) []string) {
	im.resolveTarget = resolve
	im.unindexed = unindexed
}
func (im *Importer) Client() *Client { return im.client }

func (im *Importer) Busy(p string) bool {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.overlapsActive(normalizeAListPath(p))
}
func (im *Importer) overlapsActive(p string) bool {
	for active := range im.active {
		if active == p || strings.HasPrefix(active, p+"/") || strings.HasPrefix(p, active+"/") {
			return true
		}
	}
	return false
}

func (im *Importer) acquire(p string) (func(), bool) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.overlapsActive(p) {
		return nil, false
	}
	im.active[p] = true
	return func() { im.mu.Lock(); delete(im.active, p); im.mu.Unlock() }, true
}

func (im *Importer) contentURL(p, sign string) string {
	u, _ := strm.BuildSTRMURL(p, "", im.client.BaseURL(), false)
	if sign != "" {
		u += "?sign=" + sign
	}
	return u
}

func (im *Importer) Import(ctx context.Context, req ImportRequest) (ImportSummary, error) {
	p := normalizeAListPath(req.Path)
	sum := ImportSummary{SourcePath: p}
	if p == "/" {
		return sum, errors.New("不能导入整个 AList 根目录")
	}
	release, ok := im.acquire(p)
	if !ok {
		return sum, ErrImportInProgress
	}
	defer release()
	if im.resolveTarget != nil {
		var err error
		req.OutputDir, err = im.resolveTarget(req.LibraryID, req.OutputDir)
		if err != nil {
			return sum, err
		}
	}
	var detail *FileDetail
	var err error
	if req.IsDir == nil || !*req.IsDir {
		detail, err = im.client.Get(ctx, p)
		if err != nil {
			return sum, im.discSvc.RecordManualFailure(ctx, p, err)
		}
	}
	isDir := (req.IsDir != nil && *req.IsDir) || (detail != nil && detail.IsDir)
	if isDir {
		return im.importDirectory(ctx, p, req.LibraryID, req.OutputDir, &req.TrackUpdates)
	}
	n := discovery.ManualNode{Path: p, RelPath: strings.TrimPrefix(p, "/"), Name: path.Base(p), Content: im.contentURL(p, detail.Sign)}
	if strm.ClassifyFile(n.Name, n.Content, "full") == strm.CategoryIgnored {
		return sum, errors.New("不支持该文件类型")
	}
	return im.apply(ctx, discovery.ManualSnapshot{RootPath: p, Mode: discovery.ManageManualFile, LibraryID: req.LibraryID, OutputDir: req.OutputDir, Nodes: []discovery.ManualNode{n}}, false)
}

func (im *Importer) listing(ctx context.Context, p string, refresh bool) ([]Entry, error) {
	var result []Entry
	seen := map[string]bool{}
	expected := -1
	for page := 1; ; page++ {
		items, total, err := im.client.list(ctx, p, page, 200, refresh && page == 1, true)
		if err != nil {
			return nil, err
		}
		if expected < 0 {
			expected = total
		}
		if expected != total {
			return nil, fmt.Errorf("目录 %s 的列表在分页期间发生变化", p)
		}
		for _, item := range items {
			if item.Name == "" || item.Name == "." || item.Name == ".." || strings.Contains(item.Name, "/") || seen[item.Name] {
				return nil, fmt.Errorf("目录 %s 返回了重复或无效的名称", p)
			}
			seen[item.Name] = true
			result = append(result, item)
		}
		if len(result) == total {
			return result, nil
		}
		if len(items) == 0 || len(result) > total {
			return nil, fmt.Errorf("目录 %s 的分页列表不完整", p)
		}
	}
}

func (im *Importer) importDirectory(ctx context.Context, root, lib, out string, track *bool) (ImportSummary, error) {
	sum := ImportSummary{SourcePath: root}
	m := discovery.ManualSnapshot{RootPath: root, Mode: discovery.ManageManualSeriesDir, LibraryID: lib, OutputDir: out, Track: track}
	m.Nodes = append(m.Nodes, discovery.ManualNode{Path: root, RelPath: strings.TrimPrefix(root, "/"), Name: path.Base(root), IsDir: true})
	seen := map[string]bool{root: true}
	dirs := map[string]bool{root: true}
	queue := []string{root}
	excluded := map[string]bool{}
	if track == nil {
		rules, err := im.discSvc.Store().AListTrackingExclusions(ctx)
		if err != nil {
			return sum, im.discSvc.RecordManualFailure(ctx, root, err)
		}
		excluded = rules
		delete(excluded, root)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur != root {
			blocked, err := im.discSvc.Store().IsUnmanaged(ctx, discovery.MediaResource{ID: discovery.StableID("alist:" + cur), SourcePath: cur, OriginType: discovery.OriginAListManual})
			if err != nil {
				return sum, im.discSvc.RecordManualFailure(ctx, root, err)
			}
			if blocked || excluded[cur] {
				excluded[cur] = true
				continue
			}
		}
		items, err := im.listing(ctx, cur, track == nil)
		if err != nil {
			return sum, im.discSvc.RecordManualFailure(ctx, root, err)
		}
		for _, item := range items {
			p := item.FullPath
			seen[p] = true
			n := discovery.ManualNode{Path: p, RelPath: strings.TrimPrefix(p, "/"), Name: item.Name, IsDir: item.IsDir}
			if item.IsDir {
				queue = append(queue, p)
				dirs[p] = true
			} else {
				n.Content = im.contentURL(p, item.Sign)
				if strm.ClassifyFile(n.Name, n.Content, "full") == strm.CategoryIgnored {
					continue
				}
			}
			m.Nodes = append(m.Nodes, n)
		}
	}
	if track == nil || *track {
		known, err := im.discSvc.Store().AListSubtree(ctx, root)
		if err != nil {
			return sum, im.discSvc.RecordManualFailure(ctx, root, err)
		}
		checked := map[string]map[string]bool{}
		for _, old := range known {
			if seen[old.SourcePath] || old.Status == discovery.StatusMissing {
				continue
			}
			ignored := false
			for p := old.SourcePath; p != root && p != "/"; p = path.Dir(p) {
				if excluded[p] {
					ignored = true
					break
				}
			}
			if ignored {
				continue
			}
			child, parent := old.SourcePath, path.Dir(old.SourcePath)
			for !dirs[parent] {
				child, parent = parent, path.Dir(parent)
			}
			fresh, ok := checked[parent]
			if !ok {
				items, e := im.listing(ctx, parent, true)
				if e != nil {
					return sum, im.discSvc.RecordManualFailure(ctx, root, e)
				}
				fresh = map[string]bool{}
				for _, item := range items {
					fresh[item.FullPath] = true
				}
				checked[parent] = fresh
			}
			if fresh[child] {
				err := fmt.Errorf("复核时 %s 重新出现，目录可能正在更新", child)
				return sum, im.discSvc.RecordManualFailure(ctx, root, err)
			}
			m.Missing = append(m.Missing, old.SourcePath)
		}
	}
	return im.apply(ctx, m, track == nil || *track)
}

func (im *Importer) apply(ctx context.Context, m discovery.ManualSnapshot, tracked bool) (ImportSummary, error) {
	sum := ImportSummary{SourcePath: m.RootPath, TrackingEnabled: tracked}
	batch, rs, err := im.discSvc.ApplyManualSnapshot(ctx, m)
	sum.BatchID = batch.ID
	if err != nil {
		return sum, err
	}
	var touched []string
	for _, r := range rs {
		if r.SourcePath == m.RootPath {
			sum.RootResourceID = r.ID
		}
		switch {
		case r.IsDir:
			sum.ImportedDirs++
		case r.Status == discovery.StatusFailed:
			sum.FailedFiles++
			sum.Errors = append(sum.Errors, r.SourcePath+": "+r.LastError)
		case r.LocalSTRMPath != "":
			sum.ImportedFiles++
			touched = append(touched, r.LocalSTRMPath)
			if strings.EqualFold(path.Ext(r.LocalSTRMPath), ".strm") {
				sum.GeneratedSTRMs = append(sum.GeneratedSTRMs, r.LocalSTRMPath)

			}
		}
	}
	if im.unindexed != nil && len(sum.GeneratedSTRMs) > 0 {
		sum.UnindexedSTRMs = im.unindexed(sum.GeneratedSTRMs)
	}
	if im.onSynced != nil {
		if m.Mode == discovery.ManageManualSeriesDir && m.Track == nil {
			im.onSynced(nil)
		} else {
			im.onSynced(touched)
		}
	}
	return sum, nil
}

func (im *Importer) SyncTrackedDirectories(ctx context.Context) (int, error) {
	roots, err := im.discSvc.Store().TrackedAListRoots(ctx, false)
	if err != nil {
		return 0, err
	}
	synced := 0
	var failed []error
	for _, root := range roots {
		r, err := im.discSvc.Store().GetResource(ctx, root.ResourceID)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		unmanaged, err := im.discSvc.Store().IsUnmanaged(ctx, r)
		if err != nil {
			failed = append(failed, err)
			continue
		}
		if unmanaged {
			continue
		}
		release, ok := im.acquire(root.Path)
		if !ok {
			continue
		}
		sum, err := im.importDirectory(ctx, root.Path, root.LibraryID, root.OutputDir, nil)
		release()
		if err != nil {
			failed = append(failed, err)
			continue
		}
		if sum.FailedFiles > 0 {
			failed = append(failed, fmt.Errorf("%s: %d 个文件处理失败", root.Path, sum.FailedFiles))
			continue
		}
		synced++
	}
	return synced, errors.Join(failed...)
}
