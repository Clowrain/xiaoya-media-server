package embyserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"xiaoya-media-server/internal/metadata/tmdb"
)

// All 21 media extensions from strm.py U strm2.py, plus .strm itself.
var scannableMediaExts = map[string]bool{
	".strm": true,
	".mp4":  true,
	".m4v":  true,
	".mkv":  true,
	".iso":  true,
	".flv":  true,
	".avi":  true,
	".ts":   true,
	".wmv":  true,
	".mov":  true,
	".rm":   true,
	".rmvb": true,
	".webm": true,
	".wav":  true,
	".mp3":  true,
	".flac": true,
	".ape":  true,
	".wv":   true,
	".alac": true,
	".m4a":  true,
	".aac":  true,
	".wma":  true,
}

func isScannableMediaFile(path string) bool {
	return scannableMediaExts[strings.ToLower(filepath.Ext(path))]
}

func isSTRMFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".strm")
}

func (a *App) tmdbSchema() error {
	_, err := a.db.Exec(`
CREATE TABLE IF NOT EXISTS tmdb_metadata_cache(
	cache_key TEXT PRIMARY KEY,
	tmdb_id TEXT NOT NULL,
	kind TEXT NOT NULL,
	data TEXT NOT NULL,
	updated BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS tmdb_item_matches(
	item TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
	tmdb_id TEXT NOT NULL,
	media_type TEXT NOT NULL,
	updated BIGINT NOT NULL
);
CREATE TABLE IF NOT EXISTS tmdb_scrape_tasks(
	item_id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	title TEXT NOT NULL,
	tmdb_id TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL,
	retry_count INT NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	next_retry_at BIGINT NOT NULL DEFAULT 0,
	updated_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tmdb_scrape_tasks_state ON tmdb_scrape_tasks(state, next_retry_at);
`)
	return err
}

type tmdbStoredSettings struct {
	Enabled                 bool   `json:"enabled"`
	APIKey                  string `json:"api_key"`
	Language                string `json:"language"`
	BaseURL                 string `json:"base_url"`
	ImageURL                string `json:"image_url"`
	ProxyURL                string `json:"proxy_url,omitempty"`
	SaveArtifactsToMediaDir bool   `json:"save_artifacts_to_media_dir"`
}

func (a *App) currentTMDBSettings() tmdbStoredSettings {
	s := tmdbStoredSettings{
		Enabled:  true,
		Language: "zh-CN",
		BaseURL:  "https://api.themoviedb.org/3",
		ImageURL: "https://image.tmdb.org/t/p",
		ProxyURL: strings.TrimSpace(os.Getenv("TMDB_PROXY")),
	}
	if a.cfg != nil {
		s.Enabled = a.cfg.Metadata.TMDB.Enabled
		s.APIKey = a.cfg.Metadata.TMDB.APIKey
		if a.cfg.Metadata.TMDB.Language != "" {
			s.Language = a.cfg.Metadata.TMDB.Language
		}
		if a.cfg.Metadata.TMDB.BaseURL != "" {
			s.BaseURL = a.cfg.Metadata.TMDB.BaseURL
		}
		if a.cfg.Metadata.TMDB.ImageURL != "" {
			s.ImageURL = a.cfg.Metadata.TMDB.ImageURL
		}
	}
	if envKey := strings.TrimSpace(os.Getenv("TMDB_API_KEY")); envKey != "" {
		s.APIKey = envKey
	}
	if a.db != nil {
		var raw string
		if a.db.QueryRow("SELECT v FROM settings WHERE k='tmdb_settings'").Scan(&raw) == nil && raw != "" {
			var stored tmdbStoredSettings
			if json.Unmarshal([]byte(raw), &stored) == nil {
				s.Enabled = stored.Enabled
				if stored.APIKey != "" {
					s.APIKey = stored.APIKey
				}
				if stored.Language != "" {
					s.Language = stored.Language
				}
				if stored.BaseURL != "" {
					s.BaseURL = stored.BaseURL
				}
				if stored.ImageURL != "" {
					s.ImageURL = stored.ImageURL
				}
				if stored.ProxyURL != "" {
					s.ProxyURL = stored.ProxyURL
				}
				s.SaveArtifactsToMediaDir = stored.SaveArtifactsToMediaDir
			}
		}
	}
	return s
}

func (a *App) TMDBClient() *tmdb.Client {
	if a == nil {
		return nil
	}
	s := a.currentTMDBSettings()
	if !s.Enabled || strings.TrimSpace(s.APIKey) == "" {
		return nil
	}
	return tmdb.NewClient(tmdb.Options{
		APIKey:   s.APIKey,
		Language: s.Language,
		BaseURL:  s.BaseURL,
		ImageURL: s.ImageURL,
		ProxyURL: s.ProxyURL,
	})
}

func (a *App) tmdbClient() *tmdb.Client {
	return a.TMDBClient()
}

func tmdbMetaToSidecar(m *tmdb.MediaMetadata) sidecar {
	if m == nil {
		return sidecar{}
	}
	var s sidecar
	s.Title = m.Title
	s.Plot = m.Overview
	s.Year = m.Year
	s.Runtime = m.RuntimeMin
	s.Rating = m.Rating
	s.Genres = append([]string{}, m.Genres...)
	s.Studios = append([]string{}, m.Studios...)
	s.OriginalTitle = m.OriginalTitle
	s.Premiered = m.Premiered
	s.Tagline = m.Tagline
	s.Directors = append([]string{}, m.Directors...)
	s.TMDB = m.TMDBID
	s.IMDB = m.IMDBID
	s.PosterURL = m.PosterURL
	s.BackdropURL = m.BackdropURL
	s.ThumbURL = m.ThumbURL
	for _, act := range m.Actors {
		s.Actors = append(s.Actors, struct {
			Name  string `xml:"name"`
			Role  string `xml:"role"`
			Thumb string `xml:"thumb"`
		}{
			Name:  act.Name,
			Role:  act.Role,
			Thumb: act.Thumb,
		})
	}
	return s
}

// findItemTMDBID resolves TMDB ID from manual binding, path tag ({tmdb-270431}), or parent Series/Season.
func (a *App) findItemTMDBID(x Item) string {
	if a.db != nil {
		var matched string
		if a.db.QueryRow("SELECT tmdb_id FROM tmdb_item_matches WHERE item=?", x.ID).Scan(&matched) == nil && matched != "" {
			return matched
		}
	}
	if id := tmdb.ExtractTMDBID(x.Path); id != "" {
		return id
	}
	// Check immediate children for pure metadata tag directory such as "折月亮 (2026)/{tmdb-270431}"
	if x.Kind == "Series" {
		if entries, err := os.ReadDir(x.Path); err == nil {
			for _, d := range entries {
				if d.IsDir() {
					if id := tmdb.ExtractTMDBID(d.Name()); id != "" {
						return id
					}
				}
			}
		}
	}
	if a.db != nil && x.Parent != "" && x.Parent != x.Lib {
		if parent, err := a.item(x.Parent); err == nil {
			if id := a.findItemTMDBID(parent); id != "" {
				return id
			}
			if pMeta := a.metadata(parent); pMeta.TMDB != "" {
				return pMeta.TMDB
			}
		}
	}
	return ""
}

type ScrapeState string

const (
	ScrapeStatePendingTMDB ScrapeState = "PENDING_TMDB"
	ScrapeStateUpdating    ScrapeState = "UPDATING"
	ScrapeStateRetryQueued ScrapeState = "RETRY_QUEUED"
	ScrapeStateCompleted   ScrapeState = "COMPLETED"
)

func (a *App) recordScrapeTask(itemID, kind, title, tmdbID string, state ScrapeState, lastErr error) {
	if a == nil || a.db == nil || strings.TrimSpace(itemID) == "" {
		return
	}
	now := time.Now().Unix()
	var nextRetry int64
	errMsg := ""
	if lastErr != nil {
		errMsg = lastErr.Error()
	}

	var currentCount int
	_ = a.db.QueryRow("SELECT retry_count FROM tmdb_scrape_tasks WHERE item_id=?", itemID).Scan(&currentCount)

	switch state {
	case ScrapeStateRetryQueued:
		currentCount++
		backoffSeconds := int64(60)
		switch {
		case currentCount == 2:
			backoffSeconds = 300
		case currentCount == 3:
			backoffSeconds = 900
		case currentCount >= 4:
			backoffSeconds = 3600
		}
		nextRetry = now + backoffSeconds
	case ScrapeStatePendingTMDB:
		currentCount++
		backoffSeconds := int64(6 * 3600)
		if currentCount > 2 {
			backoffSeconds = int64(24 * 3600)
		}
		nextRetry = now + backoffSeconds
	case ScrapeStateUpdating:
		nextRetry = now + 6*3600
	case ScrapeStateCompleted:
		nextRetry = 0
	}

	_, _ = a.db.Exec(`
INSERT INTO tmdb_scrape_tasks(item_id, kind, title, tmdb_id, state, retry_count, last_error, next_retry_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(item_id) DO UPDATE SET
	kind=excluded.kind,
	title=excluded.title,
	tmdb_id=CASE WHEN excluded.tmdb_id != '' THEN excluded.tmdb_id ELSE tmdb_scrape_tasks.tmdb_id END,
	state=excluded.state,
	retry_count=excluded.retry_count,
	last_error=excluded.last_error,
	next_retry_at=excluded.next_retry_at,
	updated_at=excluded.updated_at
`, itemID, kind, title, tmdbID, string(state), currentCount, errMsg, nextRetry, now)
}

func (a *App) cachedTMDBMeta(cacheKey string) (*tmdb.MediaMetadata, bool) {
	if a.db == nil {
		return nil, false
	}
	var raw string
	var updated int64
	var kind string
	if a.db.QueryRow("SELECT data, updated, kind FROM tmdb_metadata_cache WHERE cache_key=?", cacheKey).Scan(&raw, &updated, &kind) != nil || raw == "" {
		return nil, false
	}
	var m tmdb.MediaMetadata
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil, false
	}
	// For ongoing/updating series or season, check TTL (12 hours)
	if m.InProduction || m.Status == "Returning Series" || (kind == "Season" && len(m.Overview) == 0) {
		if time.Now().Unix()-updated > 12*3600 {
			return nil, false
		}
	}
	return &m, true
}

func (a *App) storeTMDBMeta(cacheKey, tmdbID, kind string, m *tmdb.MediaMetadata) {
	if a.db == nil || m == nil {
		return
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	_, _ = a.db.Exec(
		"INSERT INTO tmdb_metadata_cache(cache_key,tmdb_id,kind,data,updated) VALUES(?,?,?,?,?) ON CONFLICT(cache_key) DO UPDATE SET tmdb_id=excluded.tmdb_id,data=excluded.data,updated=excluded.updated",
		cacheKey, tmdbID, kind, string(b), time.Now().Unix(),
	)
}

// resolveItemSidecar implements the V1.0 `prefer` strategy:
// 1. Local non-empty NFO always wins (`prefer`).
// 2. Always preserves extracted {tmdb-ID} even without NFO or TMDB API key.
// 3. When no local NFO exists and TMDB is enabled, scrapes TMDB (never blocking scan on failure).
func (a *App) resolveItemSidecar(x Item, forceRefresh bool) sidecar {
	n, hasLocalNFO := readSidecarStatus(x)
	extractedID := a.findItemTMDBID(x)
	if n.TMDB == "" && extractedID != "" {
		n.TMDB = extractedID
	}
	// Under prefer policy, local non-empty NFO with plot always wins.
	// Empty/placeholder NFO (missing plot) falls back to TMDB scraping.
	if hasLocalNFO && strings.TrimSpace(n.Plot) != "" {
		return n
	}

	cli := a.tmdbClient()
	if cli == nil {
		return n
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch x.Kind {
	case "Movie":
		tmdbID := n.TMDB
		if tmdbID == "" {
			cleanTitle, year := tmdb.ParseTitleAndYear(x.Name)
			if year == 0 {
				year = x.Year
			}
			results, err := cli.SearchMovie(ctx, cleanTitle, year)
			if err != nil {
				if tmdb.IsRateLimitedOrTimeout(err) {
					a.recordScrapeTask(x.ID, "Movie", x.Name, "", ScrapeStateRetryQueued, err)
				}
				return n
			}
			if len(results) == 0 {
				a.recordScrapeTask(x.ID, "Movie", x.Name, "", ScrapeStatePendingTMDB, nil)
				return n
			}
			tmdbID = strconv.Itoa(results[0].ID)
		}
		if tmdbID == "" {
			return n
		}
		key := "movie:" + tmdbID
		if !forceRefresh {
			if cached, ok := a.cachedTMDBMeta(key); ok {
				return tmdbMetaToSidecar(cached)
			}
		}
		meta, err := cli.GetMovie(ctx, tmdbID)
		if err != nil {
			if tmdb.IsRateLimitedOrTimeout(err) {
				a.recordScrapeTask(x.ID, "Movie", x.Name, tmdbID, ScrapeStateRetryQueued, err)
			}
			return n
		}
		if meta != nil {
			a.storeTMDBMeta(key, tmdbID, "Movie", meta)
			a.recordScrapeTask(x.ID, "Movie", x.Name, tmdbID, ScrapeStateCompleted, nil)
			return tmdbMetaToSidecar(meta)
		}

	case "Series":
		tmdbID := n.TMDB
		if tmdbID == "" {
			cleanTitle, year := tmdb.ParseTitleAndYear(filepath.Base(x.Path))
			if year == 0 {
				year = x.Year
			}
			results, err := cli.SearchTV(ctx, cleanTitle, year)
			if err != nil {
				if tmdb.IsRateLimitedOrTimeout(err) {
					a.recordScrapeTask(x.ID, "Series", x.Name, "", ScrapeStateRetryQueued, err)
				}
				return n
			}
			if len(results) == 0 {
				a.recordScrapeTask(x.ID, "Series", x.Name, "", ScrapeStatePendingTMDB, nil)
				return n
			}
			tmdbID = strconv.Itoa(results[0].ID)
		}
		if tmdbID == "" {
			return n
		}
		key := "tv:" + tmdbID
		if !forceRefresh {
			if cached, ok := a.cachedTMDBMeta(key); ok {
				return tmdbMetaToSidecar(cached)
			}
		}
		meta, err := cli.GetTV(ctx, tmdbID)
		if err != nil {
			if tmdb.IsRateLimitedOrTimeout(err) {
				a.recordScrapeTask(x.ID, "Series", x.Name, tmdbID, ScrapeStateRetryQueued, err)
			}
			return n
		}
		if meta != nil {
			a.storeTMDBMeta(key, tmdbID, "Series", meta)
			if meta.InProduction || meta.Status == "Returning Series" {
				a.recordScrapeTask(x.ID, "Series", x.Name, tmdbID, ScrapeStateUpdating, nil)
			} else {
				a.recordScrapeTask(x.ID, "Series", x.Name, tmdbID, ScrapeStateCompleted, nil)
			}
			return tmdbMetaToSidecar(meta)
		}

	case "Season", "Episode":
		tmdbID := n.TMDB
		if tmdbID == "" {
			tmdbID = a.findItemTMDBID(x)
		}
		if tmdbID == "" {
			return n
		}
		seasonNum := x.Season
		if seasonNum <= 0 {
			seasonNum = 1
		}
		epNum := x.Episode
		if epNum <= 0 && x.Kind == "Episode" {
			if sNum, eNum, ok := tmdb.ParseSeasonEpisode(x.Name, seasonNum); ok {
				if sNum > 0 {
					seasonNum = sNum
				}
				epNum = eNum
			} else if sNum, eNum, ok := tmdb.ParseSeasonEpisode(filepath.Base(x.Path), seasonNum); ok {
				if sNum > 0 {
					seasonNum = sNum
				}
				epNum = eNum
			}
		}
		seasonKey := "tv:" + tmdbID + ":s:" + strconv.Itoa(seasonNum)
		epKey := seasonKey + ":e:" + strconv.Itoa(epNum)
		if !forceRefresh {
			if x.Kind == "Season" {
				if cached, ok := a.cachedTMDBMeta(seasonKey); ok {
					return tmdbMetaToSidecar(cached)
				}
			} else if cached, ok := a.cachedTMDBMeta(epKey); ok {
				return tmdbMetaToSidecar(cached)
			}
		} else if x.Kind == "Episode" {
			// Even under forceRefresh, if this season's episodes were populated during the
			// current cascade or recently stored in cache, reuse it to avoid repeated HTTP calls per episode!
			if cached, ok := a.cachedTMDBMeta(epKey); ok {
				return tmdbMetaToSidecar(cached)
			}
		}
		sMeta, eps, err := cli.GetTVSeason(ctx, tmdbID, seasonNum)
		if err != nil {
			if tmdb.IsRateLimitedOrTimeout(err) {
				a.recordScrapeTask(x.ID, x.Kind, x.Name, tmdbID, ScrapeStateRetryQueued, err)
			}
			return n
		}
		if sMeta != nil {
			a.storeTMDBMeta(seasonKey, tmdbID, "Season", sMeta)
		}
		for epIdx, epMeta := range eps {
			a.storeTMDBMeta(seasonKey+":e:"+strconv.Itoa(epIdx), tmdbID, "Episode", epMeta)
		}
		if x.Kind == "Season" && sMeta != nil {
			a.recordScrapeTask(x.ID, "Season", x.Name, tmdbID, ScrapeStateCompleted, nil)
			return tmdbMetaToSidecar(sMeta)
		}
		if x.Kind == "Episode" {
			if epMeta := eps[epNum]; epMeta != nil {
				sc := tmdbMetaToSidecar(epMeta)
				if sc.ThumbURL == "" && sc.PosterURL == "" && sMeta != nil && sMeta.PosterURL != "" {
					sc.ThumbURL = sMeta.PosterURL
				}
				a.recordScrapeTask(x.ID, "Episode", x.Name, tmdbID, ScrapeStateCompleted, nil)
				return sc
			}
			// Episode not yet listed in TMDB season (e.g. newly broadcasted) -> mark updating
			a.recordScrapeTask(x.ID, "Episode", x.Name, tmdbID, ScrapeStateUpdating, nil)
		}
	}
	return n
}

// ProcessPendingScrapes executes retries for failed/timed-out scrapes and reconciles ongoing or unlisted TMDB items.
func (a *App) ProcessPendingScrapes(ctx context.Context, limit int) (int, error) {
	if a == nil || a.db == nil {
		return 0, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	now := time.Now().Unix()
	rows, err := a.db.Query(`
SELECT item_id, kind, state, title, tmdb_id FROM tmdb_scrape_tasks
WHERE state IN ('RETRY_QUEUED', 'PENDING_TMDB', 'UPDATING')
  AND next_retry_at <= ?
ORDER BY next_retry_at ASC
LIMIT ?
`, now, limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type taskItem struct {
		ItemID string
		Kind   string
		State  string
		Title  string
		TMDBID string
	}
	var tasks []taskItem
	for rows.Next() {
		var it taskItem
		if err := rows.Scan(&it.ItemID, &it.Kind, &it.State, &it.Title, &it.TMDBID); err == nil {
			tasks = append(tasks, it)
		}
	}
	rows.Close()

	processed := 0
	for _, task := range tasks {
		if ctx.Err() != nil {
			break
		}
		var itemExists int
		if a.db.QueryRow("SELECT 1 FROM items WHERE id=?", task.ItemID).Scan(&itemExists) != nil {
			_, _ = a.db.Exec("DELETE FROM tmdb_scrape_tasks WHERE item_id=?", task.ItemID)
			continue
		}

		if err := a.refreshSingleItemMetadata(task.ItemID, true); err != nil {
			a.recordScrapeTask(task.ItemID, task.Kind, task.Title, task.TMDBID, ScrapeStateRetryQueued, err)
		}
		processed++
	}
	return processed, nil
}

func (a *App) refreshSingleItemMetadata(itemID string, force bool) error {
	x, err := a.item(itemID)
	if err != nil {
		return err
	}
	itemsToRefresh := []Item{x}
	if x.Kind == "Series" || x.Kind == "Season" {
		rows, qErr := a.db.Query(`WITH RECURSIVE tree(id) AS (
			SELECT id FROM items WHERE parent=?
			UNION
			SELECT items.id FROM items JOIN tree ON items.parent=tree.id
		) SELECT `+cols+` FROM items WHERE id IN (SELECT id FROM tree)`, x.ID)
		if qErr == nil {
			for rows.Next() {
				if child, sErr := readItem(rows); sErr == nil {
					itemsToRefresh = append(itemsToRefresh, child)
				}
			}
			rows.Close()
		}
	}

	for _, cur := range itemsToRefresh {
		n := a.resolveItemSidecar(cur, force)
		b, _ := json.Marshal(n)
		tx, txErr := a.db.Begin()
		if txErr != nil {
			return txErr
		}
		_, txErr = tx.Exec("INSERT INTO item_metadata VALUES(?,?) ON CONFLICT(item) DO UPDATE SET data=excluded.data", cur.ID, string(b))
		if txErr == nil && (n.Title != "" || n.Plot != "" || n.Year > 0 || n.ThumbURL != "" || n.PosterURL != "") {
			newName := cur.Name
			if n.Title != "" && cur.Kind != "Season" {
				// Respect manual display name override if present
				var manual string
				if tx.QueryRow("SELECT name FROM media_display_names WHERE item=?", cur.ID).Scan(&manual) == sql.ErrNoRows {
					newName = n.Title
				}
			}
			newYear := cur.Year
			if n.Year > 0 {
				newYear = n.Year
			}
			newOverview := cur.Overview
			if n.Plot != "" {
				newOverview = n.Plot
			}
			newPoster := cur.Poster
			if n.ThumbURL != "" {
				newPoster = n.ThumbURL
			} else if n.PosterURL != "" {
				newPoster = n.PosterURL
			}
			newSeason := cur.Season
			newEpisode := cur.Episode
			if cur.Kind == "Episode" && (newSeason <= 0 || newEpisode <= 0) {
				if sNum, eNum, ok := tmdb.ParseSeasonEpisode(cur.Name, 1); ok {
					if newSeason <= 0 {
						newSeason = sNum
					}
					if newEpisode <= 0 {
						newEpisode = eNum
					}
				} else if sNum, eNum, ok := tmdb.ParseSeasonEpisode(filepath.Base(cur.Path), 1); ok {
					if newSeason <= 0 {
						newSeason = sNum
					}
					if newEpisode <= 0 {
						newEpisode = eNum
					}
				}
			}
			_, txErr = tx.Exec("UPDATE items SET name=?,overview=?,year=?,poster=?,season=?,episode=? WHERE id=?", newName, newOverview, newYear, newPoster, newSeason, newEpisode, cur.ID)
		}
		if txErr == nil && cur.Kind == "Series" && n.TMDB != "" {
			_, _ = tx.Exec("INSERT INTO tmdb_item_matches(item,tmdb_id,media_type,updated) VALUES(?,?,?,?) ON CONFLICT(item) DO UPDATE SET tmdb_id=excluded.tmdb_id,media_type=excluded.media_type,updated=excluded.updated", cur.ID, n.TMDB, "tv", time.Now().Unix())
		}
		if txErr == nil {
			_, txErr = tx.Exec("DELETE FROM item_people WHERE item=?", cur.ID)
		}
		if txErr == nil {
			for _, p := range n.Actors {
				if p.Name == "" {
					continue
				}
				if _, txErr = tx.Exec("INSERT INTO item_people VALUES(?,?,?,?,?,?) ON CONFLICT(item,person,type) DO UPDATE SET name=excluded.name,role=excluded.role,thumb=excluded.thumb", cur.ID, personID(p.Name), p.Name, p.Role, "Actor", actorImage(cur, p.Name, p.Thumb)); txErr != nil {
					break
				}
			}
		}
		if txErr == nil {
			for _, dirName := range n.Directors {
				if _, txErr = tx.Exec("INSERT INTO item_people VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING", cur.ID, personID(dirName), dirName, "", "Director", ""); txErr != nil {
					break
				}
			}
		}
		if txErr != nil {
			tx.Rollback()
			return txErr
		}
		if txErr = tx.Commit(); txErr != nil {
			return txErr
		}
		a.maybePersistArtifactsToDisk(cur, n)
	}
	return nil
}

func downloadAndSaveImage(ctx context.Context, targetPath, rawURL string) error {
	if strings.TrimSpace(rawURL) == "" || strings.TrimSpace(targetPath) == "" {
		return nil
	}
	if st, err := os.Stat(targetPath); err == nil && st.Size() > 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil || len(data) == 0 {
		return err
	}
	return os.WriteFile(targetPath, data, 0644)
}

func (a *App) maybePersistArtifactsToDisk(cur Item, n sidecar) {
	if !a.currentTMDBSettings().SaveArtifactsToMediaDir {
		return
	}
	if strings.TrimSpace(cur.Path) == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		switch cur.Kind {
		case "Series":
			seriesDir := cur.Path
			if n.Title != "" || n.Plot != "" {
				seriesPayload, err := xml.MarshalIndent(struct {
					XMLName   xml.Name `xml:"tvshow"`
					Title     string   `xml:"title,omitempty"`
					Plot      string   `xml:"plot,omitempty"`
					Premiered string   `xml:"premiered,omitempty"`
					Year      int      `xml:"year,omitempty"`
					Rating    float64  `xml:"rating,omitempty"`
					TMDB      string   `xml:"tmdbid,omitempty"`
				}{Title: n.Title, Plot: n.Plot, Premiered: n.Premiered, Year: n.Year, Rating: n.Rating, TMDB: n.TMDB}, "", "  ")
				if err == nil {
					_ = os.WriteFile(filepath.Join(seriesDir, "tvshow.nfo"), append([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"), seriesPayload...), 0644)
				}
			}
			if n.PosterURL != "" {
				_ = downloadAndSaveImage(ctx, filepath.Join(seriesDir, "poster.jpg"), n.PosterURL)
			}
			if n.BackdropURL != "" {
				_ = downloadAndSaveImage(ctx, filepath.Join(seriesDir, "fanart.jpg"), n.BackdropURL)
			}
		case "Season":
			seasonDir := cur.Path
			payload, err := xml.MarshalIndent(struct {
				XMLName xml.Name `xml:"season"`
				Season  int      `xml:"seasonnumber"`
			}{Season: cur.Season}, "", "  ")
			if err == nil {
				_ = os.WriteFile(filepath.Join(seasonDir, "season.nfo"), append([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"), payload...), 0644)
			}
			if n.PosterURL != "" {
				_ = downloadAndSaveImage(ctx, filepath.Join(seasonDir, "poster.jpg"), n.PosterURL)
			}
		case "Episode":
			base := strings.TrimSuffix(cur.Path, filepath.Ext(cur.Path))
			if n.Title != "" || n.Plot != "" {
				epPayload, err := xml.MarshalIndent(struct {
					XMLName   xml.Name `xml:"episodedetails"`
					Title     string   `xml:"title,omitempty"`
					Plot      string   `xml:"plot,omitempty"`
					Premiered string   `xml:"aired,omitempty"`
					Season    int      `xml:"season"`
					Episode   int      `xml:"episode"`
					Rating    float64  `xml:"rating,omitempty"`
				}{Title: n.Title, Plot: n.Plot, Premiered: n.Premiered, Season: cur.Season, Episode: cur.Episode, Rating: n.Rating}, "", "  ")
				if err == nil {
					_ = os.WriteFile(base+".nfo", append([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"), epPayload...), 0644)
				}
			}
			thumbURL := n.ThumbURL
			if thumbURL == "" {
				thumbURL = n.PosterURL
			}
			if thumbURL != "" {
				_ = downloadAndSaveImage(ctx, base+"-thumb.jpg", thumbURL)
			}
		case "Movie":
			base := strings.TrimSuffix(cur.Path, filepath.Ext(cur.Path))
			movieDir := filepath.Dir(cur.Path)
			if n.Title != "" || n.Plot != "" {
				moviePayload, err := xml.MarshalIndent(struct {
					XMLName   xml.Name `xml:"movie"`
					Title     string   `xml:"title,omitempty"`
					Plot      string   `xml:"plot,omitempty"`
					Premiered string   `xml:"premiered,omitempty"`
					Year      int      `xml:"year,omitempty"`
					Rating    float64  `xml:"rating,omitempty"`
					TMDB      string   `xml:"tmdbid,omitempty"`
				}{Title: n.Title, Plot: n.Plot, Premiered: n.Premiered, Year: n.Year, Rating: n.Rating, TMDB: n.TMDB}, "", "  ")
				if err == nil {
					_ = os.WriteFile(base+".nfo", append([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"), moviePayload...), 0644)
					_ = os.WriteFile(filepath.Join(movieDir, "movie.nfo"), append([]byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"), moviePayload...), 0644)
				}
			}
			if n.PosterURL != "" {
				_ = downloadAndSaveImage(ctx, filepath.Join(movieDir, "poster.jpg"), n.PosterURL)
			}
			if n.BackdropURL != "" {
				_ = downloadAndSaveImage(ctx, filepath.Join(movieDir, "fanart.jpg"), n.BackdropURL)
			}
		}
	}()
}

// Admin TMDB endpoints: /admin/tmdb, /admin/tmdb/test, /admin/tmdb/search, /admin/tmdb/match
func (a *App) adminTMDB(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "/admin/tmdb":
		if r.Method == http.MethodGet {
			s := a.currentTMDBSettings()
			masked := ""
			if len(s.APIKey) > 6 {
				masked = s.APIKey[:3] + "••••••" + s.APIKey[len(s.APIKey)-3:]
			} else if s.APIKey != "" {
				masked = "••••••"
			}
			respond(w, M{
				"Enabled":                 s.Enabled,
				"Configured":              s.APIKey != "",
				"MaskedKey":               masked,
				"Language":                s.Language,
				"BaseURL":                 s.BaseURL,
				"ImageURL":                s.ImageURL,
				"ProxyURL":                s.ProxyURL,
				"NFOPolicy":               "prefer",
				"SaveArtifactsToMediaDir": s.SaveArtifactsToMediaDir,
			})
			return
		}
		if r.Method != http.MethodPut {
			fail(w, 405, "GET or PUT required")
			return
		}
		cur := a.currentTMDBSettings()
		var req struct {
			Enabled                 *bool   `json:"Enabled"`
			APIKey                  *string `json:"APIKey"`
			Language                *string `json:"Language"`
			BaseURL                 *string `json:"BaseURL"`
			ImageURL                *string `json:"ImageURL"`
			ProxyURL                *string `json:"ProxyURL"`
			SaveArtifactsToMediaDir *bool   `json:"SaveArtifactsToMediaDir"`
		}
		if !body(w, r, &req) {
			return
		}
		if req.Enabled != nil {
			cur.Enabled = *req.Enabled
		}
		if req.SaveArtifactsToMediaDir != nil {
			cur.SaveArtifactsToMediaDir = *req.SaveArtifactsToMediaDir
		}
		if req.APIKey != nil && strings.TrimSpace(*req.APIKey) != "" {
			cur.APIKey = strings.TrimSpace(*req.APIKey)
		}
		if req.Language != nil && strings.TrimSpace(*req.Language) != "" {
			cur.Language = strings.TrimSpace(*req.Language)
		}
		if req.BaseURL != nil && strings.TrimSpace(*req.BaseURL) != "" {
			cur.BaseURL = strings.TrimSpace(*req.BaseURL)
		}
		if req.ImageURL != nil && strings.TrimSpace(*req.ImageURL) != "" {
			cur.ImageURL = strings.TrimSpace(*req.ImageURL)
		}
		if req.ProxyURL != nil {
			cur.ProxyURL = strings.TrimSpace(*req.ProxyURL)
		}
		raw, _ := json.Marshal(cur)
		if _, err := a.db.Exec("INSERT INTO settings(k,v) VALUES('tmdb_settings',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", string(raw)); err != nil {
			fail(w, 500, "保存 TMDB 配置失败")
			return
		}
		respond(w, M{"ok": true})

	case "/admin/tmdb/test":
		if r.Method != http.MethodPost {
			fail(w, 405, "POST required")
			return
		}
		cli := a.tmdbClient()
		if cli == nil {
			fail(w, 400, "TMDB 未启用或未配置 API Key")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if err := cli.TestConnection(ctx); err != nil {
			fail(w, 502, "TMDB 连通性测试失败: "+err.Error())
			return
		}
		respond(w, M{"ok": true})

	case "/admin/tmdb/search":
		if r.Method != http.MethodGet {
			fail(w, 405, "GET required")
			return
		}
		cli := a.tmdbClient()
		if cli == nil {
			fail(w, 400, "TMDB 未配置")
			return
		}
		query := strings.TrimSpace(q(r, "q"))
		if query == "" {
			fail(w, 400, "请输入搜索关键词 q")
			return
		}
		year, _ := strconv.Atoi(q(r, "year"))
		kind := strings.ToLower(q(r, "kind"))
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if kind == "series" || kind == "tv" || kind == "tvshows" {
			res, err := cli.SearchTV(ctx, query, year)
			if err != nil {
				fail(w, 502, err.Error())
				return
			}
			respond(w, M{"Items": res, "results": res})
			return
		}
		res, err := cli.SearchMovie(ctx, query, year)
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		respond(w, M{"Items": res, "results": res})

	case "/admin/tmdb/match":
		if r.Method != http.MethodPost {
			fail(w, 405, "POST required")
			return
		}
		var req struct {
			ItemID    string `json:"ItemId"`
			TMDBID    string `json:"TmdbId"`
			MediaType string `json:"MediaType"`
		}
		if !body(w, r, &req) {
			return
		}
		req.ItemID = strings.TrimSpace(req.ItemID)
		req.TMDBID = strings.TrimSpace(req.TMDBID)
		if req.ItemID == "" || req.TMDBID == "" {
			fail(w, 400, "ItemId 和 TmdbId 不能为空")
			return
		}
		if req.MediaType == "" {
			req.MediaType = "auto"
		}
		if _, err := a.db.Exec(
			"INSERT INTO tmdb_item_matches(item,tmdb_id,media_type,updated) VALUES(?,?,?,?) ON CONFLICT(item) DO UPDATE SET tmdb_id=excluded.tmdb_id,media_type=excluded.media_type,updated=excluded.updated",
			req.ItemID, req.TMDBID, req.MediaType, time.Now().Unix(),
		); err != nil {
			fail(w, 500, "保存匹配绑定失败")
			return
		}
		if err := a.refreshSingleItemMetadata(req.ItemID, true); err != nil {
			fail(w, 500, "刷新元数据失败: "+err.Error())
			return
		}
		respond(w, M{"ok": true})

	default:
		fail(w, 404, "not found")
	}
}
