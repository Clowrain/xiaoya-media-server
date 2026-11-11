package integration

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"xiaoya-media-server/internal/resource/discovery"
	"xiaoya-media-server/internal/resource/xiaoya"
)

const maxOpenManifestSpools = 16

type manifestSpools struct {
	dir   string
	files map[int]*os.File
	lru   *list.List
	items map[int]*list.Element
}

func newManifestSpools(dir string) *manifestSpools {
	return &manifestSpools{dir: dir, files: map[int]*os.File{}, lru: list.New(), items: map[int]*list.Element{}}
}

func (s *manifestSpools) file(root int) (*os.File, error) {
	if f := s.files[root]; f != nil {
		s.lru.MoveToFront(s.items[root])
		return f, nil
	}
	if len(s.files) == maxOpenManifestSpools {
		oldest := s.lru.Back()
		index := oldest.Value.(int)
		if err := s.files[index].Close(); err != nil {
			return nil, err
		}
		delete(s.files, index)
		delete(s.items, index)
		s.lru.Remove(oldest)
	}
	path := filepath.Join(s.dir, fmt.Sprintf("root-%d.jsonl", root))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	s.files[root] = f
	s.items[root] = s.lru.PushFront(root)
	return f, nil
}

func (s *manifestSpools) write(root int, entry xiaoya.ManifestEntry) error {
	f, err := s.file(root)
	if err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(entry)
}

func (s *manifestSpools) close() error {
	var failures []error
	for root, f := range s.files {
		if err := f.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close root spool %d: %w", root, err))
		}
	}
	s.files = map[int]*os.File{}
	return errors.Join(failures...)
}

func addMatchingRoots(path string, roots map[string][]int, matched map[int]bool) {
	if path == "" {
		return
	}
	candidate := path
	for {
		for _, index := range roots[candidate] {
			matched[index] = true
		}
		if candidate == "/" {
			return
		}
		slash := strings.LastIndex(candidate, "/")
		if slash < 0 {
			return
		}
		if slash == 0 {
			candidate = "/"
		} else {
			candidate = candidate[:slash]
		}
	}
}

// 清单先顺序落入临时文件并核对版本，再分批写业务库；损坏或更新中的清单不参与缺失对账。
func (p *Pipeline) syncManifestRoots(ctx context.Context, roots []discovery.MonitorRoot) (map[string]discovery.SyncBatch, error) {
	result := map[string]discovery.SyncBatch{}
	if len(roots) == 0 {
		return result, nil
	}
	if p.manifest == nil {
		return result, errors.New("strm.txt 清单文件不可用")
	}
	before, err := p.manifest.Fingerprint()
	if err != nil {
		return result, err
	}
	dir, err := os.MkdirTemp("", "xiaoya-manifest-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)
	prefixes := make([]string, 0, len(roots))
	rootIndexes := make(map[string][]int, len(roots))
	for i, r := range roots {
		norm, _, e := xiaoya.NormalizeMonitorPath(r.RootPath)
		if e != nil {
			return result, e
		}
		prefixes = append(prefixes, norm)
		rootIndexes[norm] = append(rootIndexes[norm], i)
	}
	spools := newManifestSpools(dir)
	defer spools.close()
	_, err = p.manifest.StreamMultiPrefix(ctx, prefixes, func(_ string, entry xiaoya.ManifestEntry) error {
		matched := map[int]bool{}
		relPath := "/" + strings.TrimPrefix(entry.RelPath, "/")
		addMatchingRoots(entry.AListPath, rootIndexes, matched)
		addMatchingRoots(relPath, rootIndexes, matched)
		for index := range matched {
			if err := spools.write(index, entry); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	if err = spools.close(); err != nil {
		return result, err
	}
	after, err := p.manifest.Fingerprint()
	if err != nil {
		return result, err
	}
	if before != after {
		return result, errors.New("strm.txt 在读取期间变化，稍后重试")
	}
	var failures []error
	for i, r := range roots {
		prefix := prefixes[i]
		spoolPath := filepath.Join(dir, fmt.Sprintf("root-%d.jsonl", i))
		m := discovery.ManualSnapshot{RootPath: prefix, OriginType: discovery.OriginXiaoyaSQLite, Mode: discovery.ManageManualSeriesDir}
		b, e := p.discovery.ApplyManualStream(ctx, m, r.ID, func(yield func(discovery.ManualNode) error) error {
			if e := yield(discovery.ManualNode{Path: prefix, RelPath: strings.TrimPrefix(prefix, "/"), Name: filepath.Base(prefix), IsDir: true}); e != nil {
				return e
			}
			f, err := os.Open(spoolPath)
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			defer f.Close()
			dec := json.NewDecoder(f)
			for {
				var entry xiaoya.ManifestEntry
				err := dec.Decode(&entry)
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
				aListPath := entry.AListPath
				entryRelPath := "/" + strings.TrimPrefix(entry.RelPath, "/")
				cleanPrefix := strings.TrimRight(prefix, "/") + "/"
				matchedAList := aListPath == prefix || strings.HasPrefix(aListPath, cleanPrefix)
				matchedRel := entryRelPath == prefix || strings.HasPrefix(entryRelPath, cleanPrefix)
				effectivePath := aListPath
				if !matchedAList && matchedRel {
					effectivePath = entryRelPath
				}
				cleanURL := entry.CleanPlayURL
				if cleanURL == "" {
					cleanURL = entry.PlayURL
				}
				if err = yield(discovery.ManualNode{Path: effectivePath, RelPath: entry.RelPath, Name: filepath.Base(effectivePath), Content: cleanURL}); err != nil {
					return err
				}
			}
		}, p.consumeMaterialized)
		result[r.ID] = b
		if e != nil {
			failures = append(failures, fmt.Errorf("%s: %w", prefix, e))
		}
	}
	return result, errors.Join(failures...)
}

func (p *Pipeline) consumeMaterialized(paths []string) int {
	n := 0
	if p.app != nil {
		n = len(p.app.UnmatchedLibraryPaths(paths))
	}
	p.dirtyDirsMu.Lock()
	dirs := make([]string, 0, len(p.dirtyDirs))
	for d := range p.dirtyDirs {
		dirs = append(dirs, d)
	}
	p.dirtyDirs = map[string]bool{}
	p.dirtyDirsMu.Unlock()
	if p.app != nil {
		p.app.ScanScopedPaths(dirs)
	}
	return n
}

func (p *Pipeline) importManifestStream(ctx context.Context, root, name, library, output string, track *bool) (discovery.SyncBatch, error) {
	before, err := p.manifest.Fingerprint()
	if err != nil {
		return discovery.SyncBatch{}, err
	}
	f, err := os.CreateTemp("", "xiaoya-import-*.jsonl")
	if err != nil {
		return discovery.SyncBatch{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	enc := json.NewEncoder(f)
	tracked := map[string]bool{}
	write := func(e xiaoya.ManifestEntry) error {
		target := "/" + strings.TrimPrefix(e.RelPath, "/")
		if e.SeriesRootPath != "" {
			target = "/" + strings.TrimPrefix(e.SeriesRootPath, "/")
		}
		tracked[target] = true
		return enc.Encode(e)
	}
	count, err := p.manifest.StreamPrefix(ctx, root, write)
	if err != nil {
		return discovery.SyncBatch{}, err
	}
	exact := count > 0
	if count == 0 {
		count, err = p.manifest.StreamMatching(ctx, name, write)
	}
	if err != nil {
		return discovery.SyncBatch{}, err
	}
	if count == 0 {
		return discovery.SyncBatch{}, fmt.Errorf("在 strm.txt 清单中未找到与 '%s' 匹配的条目", root)
	}
	after, err := p.manifest.Fingerprint()
	if err != nil {
		return discovery.SyncBatch{}, err
	}
	if before != after {
		return discovery.SyncBatch{}, errors.New("strm.txt 在读取期间变化，稍后重试")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return discovery.SyncBatch{}, err
	}
	mode := discovery.ManageManualSeriesDir
	if count == 1 {
		mode = discovery.ManageManualFile
	}
	m := discovery.ManualSnapshot{RootPath: root, Mode: mode, LibraryID: library, OutputDir: output, Track: track, OriginType: discovery.OriginXiaoyaSQLite}
	var generated []string
	b, err := p.discovery.ApplyManualStream(ctx, m, "", func(yield func(discovery.ManualNode) error) error {
		if e := yield(discovery.ManualNode{Path: root, RelPath: strings.TrimPrefix(root, "/"), Name: name, IsDir: true}); e != nil {
			return e
		}
		dec := json.NewDecoder(f)
		for {
			var entry xiaoya.ManifestEntry
			e := dec.Decode(&entry)
			if e == io.EOF {
				return nil
			}
			if e != nil {
				return e
			}
			cleanURL := entry.CleanPlayURL
			if cleanURL == "" {
				cleanURL = entry.PlayURL
			}
			if e = yield(discovery.ManualNode{Path: "/" + entry.RelPath, RelPath: entry.RelPath, Name: filepath.Base(entry.RelPath), Content: cleanURL}); e != nil {
				return e
			}
		}
	}, func(paths []string) int { generated = append(generated, paths...); return p.consumeMaterialized(paths) })
	b.MaterializedSTRMs = generated
	if err != nil {
		return b, err
	}
	if track != nil && *track {
		targets := []string{root}
		if !exact {
			targets = nil
			for target := range tracked {
				targets = append(targets, target)
			}
		}
		_, err = p.discovery.RegisterMonitorRoots(ctx, targets, 10*time.Minute)
	}
	return b, err
}
