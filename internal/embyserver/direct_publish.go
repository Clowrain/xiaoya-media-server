package embyserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DirectMediaPublication represents the fully scrape-backed media item
// directly published to Emby's index by the V2 pipeline, bypassing the
// need for redundant local directory rescanning.
type DirectMediaPublication struct {
	MediaType     string   // "MOVIE" or "EPISODE"
	LocalSTRM     string   // Absolute path to the .strm file
	StreamURL     string   // Upstream streaming URL or playback locator
	Title         string   // Title of Movie or Series
	OriginalTitle string   // Original title
	Year          int      // Production year
	Rating        float64  // Critic/TMDB rating
	Overview      string   // Plot overview
	Tagline       string   // Tagline
	Premiered     string   // Premiered date (YYYY-MM-DD)
	RuntimeMin    float64  // Runtime in minutes
	TMDBID        string   // TMDB external ID
	Genres        []string // Genre list
	PosterPath    string   // Local path or URL to poster image
	BackdropPath  string   // Local path or URL to backdrop image
	SeasonNumber  int      // Season number (for EPISODE)
	EpisodeNumber int      // Episode number (for EPISODE)
	EpisodeTitle  string   // Specific title for episode
	EpisodePlot   string   // Specific plot for episode
	EpisodeThumb  string   // Specific thumbnail for episode
}

// PublishDirectMedia writes or updates a media item directly into Emby's
// database (items and item_metadata). For an EPISODE, it ensures Series, Season,
// and Episode hierarchy within a single database transaction.
func (a *App) PublishDirectMedia(pub DirectMediaPublication) error {
	if a == nil || a.db == nil {
		return errors.New("emby app or db is not initialized")
	}
	cleanPath := filepath.Clean(strings.TrimSpace(pub.LocalSTRM))
	if cleanPath == "" || cleanPath == "." {
		return errors.New("empty local strm path")
	}

	locations, err := a.mediaLibraryLocations()
	if err != nil {
		return fmt.Errorf("lookup library locations: %w", err)
	}

	libID := ""
	for id, roots := range locations {
		for _, r := range roots {
			if pathWithin(resolveSymlinkAllowNonExistent(r), cleanPath) {
				libID = id
				break
			}
		}
		if libID != "" {
			break
		}
	}

	// If the file path doesn't fall within an existing library root,
	// find the most appropriate library by MediaType, or fallback to the first.
	if libID == "" {
		expectedKind := "movies"
		if pub.MediaType == "EPISODE" {
			expectedKind = "tvshows"
		}
		var fallbackID string
		_ = a.db.QueryRow("SELECT id FROM libraries WHERE kind=? ORDER BY id LIMIT 1", expectedKind).Scan(&fallbackID)
		if fallbackID == "" {
			_ = a.db.QueryRow("SELECT id FROM libraries ORDER BY id ASC LIMIT 1").Scan(&fallbackID)
		}
		libID = fallbackID
	}
	if libID == "" {
		libID = "default"
	}

	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now().UnixNano()
	fi, _ := os.Stat(cleanPath)
	fileSize := int64(0)
	if fi != nil {
		fileSize = fi.Size()
	}

	if pub.MediaType == "MOVIE" {
		movieID := digest(cleanPath)[:32]
		movieName := pub.Title
		if movieName == "" {
			movieName = strings.TrimSuffix(filepath.Base(cleanPath), filepath.Ext(cleanPath))
		}

		itemStmt := `INSERT INTO items(id,lib,parent,name,kind,path,url,overview,poster,year,season,episode,mtime,size,seen)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)
ON CONFLICT(path) DO UPDATE SET
  lib=excluded.lib,parent=excluded.parent,name=excluded.name,kind=excluded.kind,url=excluded.url,
  overview=excluded.overview,poster=CASE WHEN excluded.poster<>'' THEN excluded.poster ELSE items.poster END,
  year=CASE WHEN excluded.year>0 THEN excluded.year ELSE items.year END,
  mtime=excluded.mtime,size=excluded.size,seen=excluded.seen`

		if _, err = tx.Exec(itemStmt, movieID, libID, libID, movieName, "Movie", cleanPath, pub.StreamURL, pub.Overview, pub.PosterPath, pub.Year, 0, 0, now, fileSize); err != nil {
			return fmt.Errorf("upsert direct movie: %w", err)
		}

		if pub.Rating > 0 {
			_, _ = tx.Exec("UPDATE items SET rating=? WHERE path=?", pub.Rating, cleanPath)
		}

		metaJSON := map[string]any{
			"Title":         pub.Title,
			"OriginalTitle": pub.OriginalTitle,
			"Plot":          pub.Overview,
			"Tagline":       pub.Tagline,
			"Premiered":     pub.Premiered,
			"Year":          pub.Year,
			"Rating":        pub.Rating,
			"Runtime":       pub.RuntimeMin,
			"TMDB":          pub.TMDBID,
			"Genres":        pub.Genres,
		}
		if rawMeta, mErr := json.Marshal(metaJSON); mErr == nil {
			_, _ = tx.Exec("INSERT INTO item_metadata(item,data) VALUES(?,?) ON CONFLICT(item) DO UPDATE SET data=excluded.data", movieID, string(rawMeta))
		}
	} else {
		// EPISODE hierarchy: Series -> Season -> Episode
		seasonDir := filepath.Dir(cleanPath)
		seriesDir := filepath.Dir(seasonDir)
		if strings.EqualFold(filepath.Base(seasonDir), filepath.Base(seriesDir)) || !strings.HasPrefix(strings.ToLower(filepath.Base(seasonDir)), "season") {
			// In case of flat episode, seasonDir is the seriesDir
			seriesDir = seasonDir
		}

		seriesID := digest(seriesDir)[:32]
		seriesName := pub.Title
		if seriesName == "" {
			seriesName = filepath.Base(seriesDir)
		}

		// 1. Series
		seriesStmt := `INSERT INTO items(id,lib,parent,name,kind,path,url,overview,poster,year,season,episode,mtime,size,seen)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)
ON CONFLICT(path) DO UPDATE SET
  lib=excluded.lib,parent=excluded.parent,name=excluded.name,kind=excluded.kind,
  overview=CASE WHEN excluded.overview<>'' THEN excluded.overview ELSE items.overview END,
  poster=CASE WHEN excluded.poster<>'' THEN excluded.poster ELSE items.poster END,
  year=CASE WHEN excluded.year>0 THEN excluded.year ELSE items.year END,
  mtime=excluded.mtime,seen=excluded.seen`

		if _, err = tx.Exec(seriesStmt, seriesID, libID, libID, seriesName, "Series", seriesDir, "", pub.Overview, pub.PosterPath, pub.Year, 0, 0, now, 0); err != nil {
			return fmt.Errorf("upsert direct series: %w", err)
		}

		seriesMetaJSON := map[string]any{
			"Title":         pub.Title,
			"OriginalTitle": pub.OriginalTitle,
			"Plot":          pub.Overview,
			"Premiered":     pub.Premiered,
			"Year":          pub.Year,
			"Rating":        pub.Rating,
			"TMDB":          pub.TMDBID,
			"Genres":        pub.Genres,
		}
		if rawMeta, mErr := json.Marshal(seriesMetaJSON); mErr == nil {
			_, _ = tx.Exec("INSERT INTO item_metadata(item,data) VALUES(?,?) ON CONFLICT(item) DO UPDATE SET data=excluded.data", seriesID, string(rawMeta))
		}

		// 2. Season
		seasonNum := pub.SeasonNumber
		if seasonNum < 0 {
			seasonNum = 0
		}
		if seasonDir == seriesDir {
			seasonDir = filepath.Join(seriesDir, fmt.Sprintf("Season %d", seasonNum))
		}
		seasonID := digest(seasonDir)[:32]
		seasonName := fmt.Sprintf("Season %d", seasonNum)
		seasonStmt := `INSERT INTO items(id,lib,parent,name,kind,path,url,overview,poster,year,season,episode,mtime,size,seen)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)
ON CONFLICT(path) DO UPDATE SET
  lib=excluded.lib,parent=excluded.parent,name=excluded.name,kind=excluded.kind,
  poster=CASE WHEN excluded.poster<>'' THEN excluded.poster ELSE items.poster END,
  season=excluded.season,mtime=excluded.mtime,seen=excluded.seen`

		if _, err = tx.Exec(seasonStmt, seasonID, libID, seriesID, seasonName, "Season", seasonDir, "", "", pub.PosterPath, pub.Year, seasonNum, seasonNum, now, 0); err != nil {
			return fmt.Errorf("upsert direct season: %w", err)
		}

		// 3. Episode
		epID := digest(cleanPath)[:32]
		epNum := pub.EpisodeNumber
		if epNum <= 0 {
			epNum = 1
		}
		epTitle := strings.TrimSpace(pub.EpisodeTitle)
		if epTitle == "" {
			epTitle = fmt.Sprintf("第 %02d 集", epNum)
		}

		epStmt := `INSERT INTO items(id,lib,parent,name,kind,path,url,overview,poster,year,season,episode,mtime,size,seen)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,1)
ON CONFLICT(path) DO UPDATE SET
  lib=excluded.lib,parent=excluded.parent,name=excluded.name,kind=excluded.kind,url=excluded.url,
  overview=excluded.overview,poster=CASE WHEN excluded.poster<>'' THEN excluded.poster ELSE items.poster END,
  season=excluded.season,episode=excluded.episode,mtime=excluded.mtime,size=excluded.size,seen=excluded.seen`

		poster := pub.EpisodeThumb
		if poster == "" {
			poster = pub.PosterPath
		}
		if _, err = tx.Exec(epStmt, epID, libID, seasonID, epTitle, "Episode", cleanPath, pub.StreamURL, pub.EpisodePlot, poster, pub.Year, seasonNum, epNum, now, fileSize); err != nil {
			return fmt.Errorf("upsert direct episode: %w", err)
		}

		if pub.Rating > 0 {
			_, _ = tx.Exec("UPDATE items SET rating=? WHERE path=?", pub.Rating, cleanPath)
		}
	}

	return tx.Commit()
}
