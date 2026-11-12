package embyserver

import (
	"database/sql"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"xiaoya-media-server/internal/metadata/tmdb"
)

type nfo struct {
	Title   string  `xml:"title"`
	Plot    string  `xml:"plot"`
	Year    int     `xml:"year"`
	Season  int     `xml:"season"`
	Episode int     `xml:"episode"`
	Rating  float64 `xml:"rating"`
}

var epPattern = regexp.MustCompile(`(?i)S(\d{1,3})E(\d{1,4})`)

type cachedNFO struct {
	value nfo
	valid bool
}

const maxLocalProbeCacheEntries = 512

type localInfoCache struct {
	nfos    map[string]cachedNFO
	posters map[string]bool
	regular map[string]bool
	dirs    map[string][]os.DirEntry
}

func newLocalInfoCache() *localInfoCache {
	return &localInfoCache{nfos: map[string]cachedNFO{}, posters: map[string]bool{}, regular: map[string]bool{}, dirs: map[string][]os.DirEntry{}}
}

func (c *localInfoCache) hasRoom() bool {
	return len(c.nfos)+len(c.posters)+len(c.regular)+len(c.dirs) < maxLocalProbeCacheEntries
}

func (c *localInfoCache) readNFO(path string, memoize bool) cachedNFO {
	if memoize {
		if result, ok := c.nfos[path]; ok {
			return result
		}
	}
	result := cachedNFO{}
	f, err := os.Open(path)
	if err == nil {
		var value nfo
		err = xml.NewDecoder(io.LimitReader(f, 2<<20)).Decode(&value)
		_ = f.Close()
		if err == nil {
			result = cachedNFO{value: value, valid: true}
		}
	}
	if memoize && c.hasRoom() {
		c.nfos[path] = result
	}
	return result
}

func (c *localInfoCache) posterExists(path string, memoize bool) bool {
	if memoize {
		if exists, ok := c.posters[path]; ok {
			return exists
		}
	}
	info, err := os.Stat(path)
	exists := err == nil && !info.IsDir()
	if memoize && c.hasRoom() {
		c.posters[path] = exists
	}
	return exists
}

func (c *localInfoCache) regularPosterExists(path string) bool {
	if exists, ok := c.regular[path]; ok {
		return exists
	}
	info, err := os.Stat(path)
	exists := err == nil && info.Mode().IsRegular()
	if c.hasRoom() {
		c.regular[path] = exists
	}
	return exists
}

func (c *localInfoCache) readDir(path string) ([]os.DirEntry, error) {
	if entries, ok := c.dirs[path]; ok {
		return entries, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	if len(entries) <= 128 && c.hasRoom() {
		c.dirs[path] = entries
	}
	return entries, nil
}

// localInfo returns the sidecar rating so the scan can persist it without
// extending Item; zero means the parsed NFO carried no usable rating.
func localInfo(path string, x *Item) float64 {
	return newLocalInfoCache().localInfo(path, x)
}

func (c *localInfoCache) localInfo(path string, x *Item) float64 {
	rating := 0.0
	base := strings.TrimSuffix(path, filepath.Ext(path))
	dir := filepath.Dir(path)
	for index, p := range []string{base + ".nfo", filepath.Join(dir, "movie.nfo"), filepath.Join(dir, "tvshow.nfo")} {
		result := c.readNFO(p, index > 0)
		if !result.valid {
			continue
		}
		n := result.value
		if n.Title != "" {
			x.Name = n.Title
		}
		x.Overview = n.Plot
		x.Year = n.Year
		if n.Season > 0 {
			x.Season = n.Season
		}
		if n.Episode > 0 {
			x.Episode = n.Episode
		}
		if n.Rating > 0 {
			rating = n.Rating
		}
		break
	}
	for index, p := range []string{base + "-poster.jpg", base + ".jpg", filepath.Join(dir, "poster.jpg"), filepath.Join(dir, "folder.jpg"), filepath.Join(dir, "poster.png")} {
		if c.posterExists(p, index >= 2) {
			x.Poster = p
			break
		}
	}
	return rating
}
func (a *App) scanAll() {
	for _, l := range a.libraries() {
		go a.scanLibrary(l["Id"].(string))
	}
}
func (a *App) scanLibrary(lib string) { a.scanLibraryMode(lib, false) }
func (a *App) scanLibraryMode(lib string, incremental bool) {
	a.scanLibraryOptions(lib, incremental, false)
}
func (a *App) scanLibraryOptions(lib string, incremental, allowEmpty bool) {
	a.scanLibraryScoped(lib, incremental, allowEmpty, nil)
}
func (a *App) scanLibraryScoped(lib string, incremental, allowEmpty bool, scopes []string) {
	category := "scan"
	if incremental || len(scopes) > 0 {
		category = "update"
	}
	queuedLibrary, queuedErr := a.libraryStore().GetByID(lib)
	queuedName := queuedLibrary.Name
	if queuedErr != nil {
		log.Printf("read library name before scan: %v", queuedErr)
	}
	job := a.newActivity(category, lib, queuedName)
	release := a.acquireLibraryJob(lib, incremental || len(scopes) > 0)
	defer release()
	a.scan.RLock()
	defer a.scan.RUnlock()
	var jobErr error
	defer func() { a.finishActivity(job, jobErr) }()
	library, err := a.libraryStore().GetByID(lib)
	if err != nil {
		jobErr = fmt.Errorf("媒体库不存在")
		return
	}
	root, kind, libraryName := library.Path, library.Kind, library.Name
	a.changeActivity(job, func(v *activityEntry) { v.State = "counting"; v.Name = libraryName })
	start := time.Now()
	gen := id()
	if e := a.libraryStore().SetScanStatus(lib, "scanning", ""); e != nil {
		jobErr = e
		return
	}
	var scanErr error
	defer func() {
		jobErr = scanErr
		status := "idle"
		message := ""
		if scanErr != nil {
			status = "error"
			message = scanErr.Error()
		}
		count, _ := a.mediaItems().CountPlayableByLibrary(lib)
		if err := a.libraryStore().CompleteScan(lib, status, message, int64(count), time.Since(start).Seconds(), time.Now().Unix()); err != nil {
			log.Printf("update library scan result: %v", err)
		}
		a.invalidateLibraryCollage(lib)
		// 释放扫描 15,000+ 部媒体产生的临时堆对象，及时向操作系统归还内存
		debug.FreeOSMemory()
	}()
	if _, err := a.db.Exec("DELETE FROM scan_file_stage WHERE lib=?", lib); err != nil {
		scanErr = err
		return
	}
	inventory := &scanInventory{app: a, lib: lib, generation: gen}
	defer inventory.close()
	roots := a.libraryPaths(lib, root)
	for _, p := range roots {
		if fi, e := os.Stat(p); e != nil || !fi.IsDir() {
			scanErr = fmt.Errorf("媒体目录不可用，保留原索引")
			return
		}
	}
	walk := func(fn fs.WalkDirFunc) error {
		for _, p := range roots {
			root = p
			targets := []string{p}
			if len(scopes) > 0 {
				targets = nil
				for _, scope := range scopes {
					if scope == p || strings.HasPrefix(scope, p+"/") {
						targets = append(targets, scope)
					}
				}
			}
			for _, target := range targets {
				if _, err := os.Stat(target); os.IsNotExist(err) && len(scopes) > 0 {
					continue
				}
				if e := boundedWalk(target, fn); e != nil {
					return e
				}
			}
		}
		return nil
	}

	total := 0
	scanErr = walk(func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		a.waitScan(job)
		fi, e := d.Info()
		if e != nil {
			return e
		}
		if e := inventory.add(path, fileStamp{fi.Size(), fi.ModTime().UnixNano()}); e != nil {
			return e
		}
		if isScannableMediaFile(path) {
			total++
		}
		return nil
	})
	if scanErr != nil {
		return
	}
	if scanErr = inventory.flush(); scanErr != nil {
		return
	}
	a.changeActivity(job, func(v *activityEntry) { v.Total = total; v.State = "running" })
	var tx *Transaction
	var stmt *sql.Stmt
	batch := 0
	begin := func() error {
		var e error
		tx, e = a.db.Begin()
		if e != nil {
			return e
		}
		stmt, e = tx.Prepare(`INSERT INTO items(` + cols + `,seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET parent=excluded.parent,name=COALESCE((SELECT name FROM media_display_names WHERE item=excluded.id),excluded.name),kind=excluded.kind,url=excluded.url,overview=excluded.overview,poster=excluded.poster,year=excluded.year,season=excluded.season,episode=excluded.episode,mtime=excluded.mtime,size=excluded.size,seen=excluded.seen`)
		return e
	}
	flush := func() error {
		if stmt != nil {
			stmt.Close()
		}
		if tx == nil {
			return nil
		}
		e := tx.Commit()
		tx = nil
		batch = 0
		return e
	}
	defer func() {
		if stmt != nil {
			stmt.Close()
		}
		if tx != nil {
			tx.Rollback()
		}
	}()
	save := func(x Item, rating float64) error {
		if tx == nil {
			if e := begin(); e != nil {
				return e
			}
		}
		_, e := stmt.Exec(x.ID, x.Lib, x.Parent, x.Name, x.Kind, x.Path, x.URL, x.Overview, x.Poster, x.Year, x.Season, x.Episode, x.Mtime, x.Size, gen)
		if e != nil {
			return e
		}
		// The rating column lives outside cols/Item; persist sidecar ratings in
		// the same transaction. Unrated files never overwrite an existing value.
		if rating > 0 {
			if _, e := tx.Exec("UPDATE items SET rating=? WHERE path=?", rating, x.Path); e != nil {
				return e
			}
		}
		batch++
		if batch >= 500 {
			return flush()
		}
		return nil
	}
	lastSeries, lastSeason := "", ""
	dirtyDirs, e := inventory.changedSidecars(scopes)
	if e != nil {
		scanErr = e
		return
	}
	// Cache sidecar probes only for this scan; changed sidecars bypass the unchanged-item path above.
	probeCache := newLocalInfoCache()
	// A directory-sized working set, no full-library item map.
	scanErr = walk(func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !isScannableMediaFile(path) {
			return nil
		}
		a.changeActivity(job, func(v *activityEntry) { v.Current = d.Name() })
		defer func() { a.changeActivity(job, func(v *activityEntry) { v.Done++ }) }()
		fi, e := d.Info()
		if e != nil {
			return e
		}
		cleanTitle, parsedYear := tmdb.ParseTitleAndYear(strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())))
		x := Item{ID: digest(path)[:32], Lib: lib, Parent: lib, Name: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())), Kind: "Movie", Path: path, Year: parsedYear, Mtime: fi.ModTime().UnixNano(), Size: fi.Size()}
		if kind != "tvshows" && cleanTitle != "" {
			x.Name = cleanTitle
		}
		a.waitScan(job)
		if (incremental || len(scopes) > 0) && !dependencyChanged(path, dirtyDirs) {
			unchanged, err := inventory.unchanged(path, fileStamp{fi.Size(), fi.ModTime().UnixNano()}, x.ID)
			if err != nil {
				return err
			}
			if unchanged {
				return nil
			}
		}
		if kind == "tvshows" {
			x.Kind = "Episode"
			seasonHint := 0
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			if rel != "." {
				seriesPath := seriesDirectoryWithCache(root, path, probeCache)
				seriesTitle, seriesYear := tmdb.ParseTitleAndYear(filepath.Base(seriesPath))
				if seriesTitle == "" {
					seriesTitle = filepath.Base(seriesPath)
				}
				series := Item{ID: digest(seriesPath)[:32], Lib: lib, Parent: lib, Name: seriesTitle, Kind: "Series", Path: seriesPath, Year: seriesYear}
				if lastSeries != seriesPath {
					seriesRating := probeCache.localInfo(filepath.Join(seriesPath, "tvshow.strm"), &series)
					if entries, rErr := probeCache.readDir(seriesPath); rErr == nil && series.Overview == "" {
						for _, ent := range entries {
							if ent.IsDir() && tmdb.IsPureMetadataTagDir(ent.Name()) {
								if r := probeCache.localInfo(filepath.Join(seriesPath, ent.Name(), "tvshow.strm"), &series); r > 0 {
									seriesRating = r
								}
							}
						}
					}
					if series.Poster == "" {
						series.Poster = seriesFallbackPosterWithCache(seriesPath, probeCache)
					}
					if e := save(series, seriesRating); e != nil {
						return e
					}
					lastSeries = seriesPath
				}
				x.Parent = series.ID
				if filepath.Dir(path) != seriesPath && !tmdb.IsPureMetadataTagDir(filepath.Base(filepath.Dir(path))) {
					seasonPath := filepath.Dir(path)
					if m := seasonDirPattern.FindStringSubmatch(filepath.Base(seasonPath)); len(m) == 2 {
						seasonHint, _ = strconv.Atoi(m[1])
					}
					sNum, epNum, _ := tmdb.ParseSeasonEpisode(x.Name, seasonHint)
					x.Season = sNum
					x.Episode = epNum
					season := Item{ID: digest(seasonPath)[:32], Lib: lib, Parent: series.ID, Name: filepath.Base(seasonPath), Kind: "Season", Path: seasonPath, Season: x.Season, Episode: x.Season}
					if seasonHint > 0 {
						season.Season = seasonHint
						season.Episode = seasonHint
						if x.Season == 0 {
							x.Season = seasonHint
						}
					}
					if lastSeason != seasonPath {
						seasonRating := probeCache.localInfo(filepath.Join(seasonPath, "season.strm"), &season)
						season.Poster = seasonPosterWithCache(seasonPath, season.Season, probeCache)
						if e := save(season, seasonRating); e != nil {
							return e
						}
						lastSeason = seasonPath
					}
					x.Parent = season.ID
				} else {
					// Xiaoya commonly exposes a flat series directory as 01.strm,
					// 02.strm, ... with no physical Season folder.  Keep the source
					// path intact, but materialize a logical Season 1 parent so Emby
					// and clients can enumerate episodes normally.
					seasonHint = 1
					seasonNum, epNum, ok := tmdb.ParseSeasonEpisode(x.Name, seasonHint)
					if ok {
						x.Season, x.Episode = seasonNum, epNum
						seasonPath := filepath.Join(seriesPath, "Season 1")
						season := Item{ID: digest(seasonPath)[:32], Lib: lib, Parent: series.ID, Name: "Season 1", Kind: "Season", Path: seasonPath, Season: 1, Episode: 1}
						if lastSeason != seasonPath {
							season.Poster = seasonPosterWithCache(seriesPath, 1, probeCache)
							if e := save(season, 0); e != nil {
								return e
							}
							lastSeason = seasonPath
						}
						x.Parent = season.ID
					}
				}
			}
			if x.Episode == 0 {
				if sNum, epNum, ok := tmdb.ParseSeasonEpisode(x.Name, seasonHint); ok {
					if x.Season == 0 {
						x.Season = sNum
					}
					x.Episode = epNum
				} else if m := epPattern.FindStringSubmatch(x.Name); len(m) == 3 {
					x.Season, _ = strconv.Atoi(m[1])
					x.Episode, _ = strconv.Atoi(m[2])
				}
			}
		}
		// Reuse unchanged STRM payload; local media files use file:// URL.
		if isSTRMFile(path) {
			f, e := os.Open(path)
			if e != nil {
				return e
			}
			b, e := io.ReadAll(io.LimitReader(f, 65537))
			f.Close()
			if e != nil {
				return e
			}
			if len(b) > 65536 {
				return fmt.Errorf("STRM 超过 64KB: %s", path)
			}
			for _, line := range strings.Split(strings.TrimPrefix(string(b), "\ufeff"), "\n") {
				line = strings.TrimSpace(line)
				if line != "" && !strings.HasPrefix(line, "#") {
					x.URL = line
					break
				}
			}
		} else {
			x.URL = "file://" + path
		}
		rating := probeCache.localInfo(path, &x)
		return save(x, rating)
	})
	if scanErr != nil {
		return
	}
	if scanErr = flush(); scanErr != nil {
		return
	}
	a.changeActivity(job, func(v *activityEntry) { v.State = "indexing"; v.Current = "索引本次更新的元数据" })
	if scanErr = a.indexMetadataGeneration(lib, gen, job); scanErr != nil {
		return
	}
	a.waitScan(job)
	a.changeActivity(job, func(v *activityEntry) { v.State = "cleaning"; v.Current = "提交文件快照并清理已删除条目" })
	scanErr = inventory.commit(allowEmpty, scopes)
	if scanErr == nil && len(scopes) == 0 {
		scanErr = a.cleanupMedia()
	}

}

// Read directory entries in fixed batches instead of sorting a whole directory in RAM.
func boundedWalk(root string, fn fs.WalkDirFunc) error {
	info, e := os.Lstat(root)
	if e != nil {
		return fn(root, nil, e)
	}
	var visit func(string, fs.DirEntry) error
	visit = func(path string, d fs.DirEntry) error {
		if e := fn(path, d, nil); e != nil {
			if e == filepath.SkipDir && d.IsDir() {
				return nil
			}
			return e
		}
		if !d.IsDir() {
			return nil
		}
		f, e := os.Open(path)
		if e != nil {
			return fn(path, d, e)
		}
		defer f.Close()
		for {
			entries, e := f.ReadDir(256)
			for _, entry := range entries {
				if err := visit(filepath.Join(path, entry.Name()), entry); err != nil {
					return err
				}
			}
			if e == io.EOF {
				return nil
			}
			if e != nil {
				return fn(path, d, e)
			}
		}
	}
	return visit(root, fs.FileInfoToDirEntry(info))
}
