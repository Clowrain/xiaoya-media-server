package embyserver

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

type nextUpCandidate struct {
	Item         Item
	SeriesID     string
	Played       bool
	Position     int64
	LastActivity int64
}

// computeNextUpPerSeries selects the next episode to watch for each Series.
// Rules:
//  1. Unless explicitSeriesID is set, a Series must have at least one episode with Played=true or Position>0.
//  2. Ignore Season 0 (Specials) when regular Season >= 1 episodes exist.
//  3. If an unplayed episode has Position > 0 (currently resumable), choose the most recently active resumable episode.
//  4. Otherwise, choose the earliest unplayed episode strictly after the highest (Season, Episode) with Played=true.
//  5. Sort across Series by most recent user activity DESC, then SeriesID ASC.
func computeNextUpPerSeries(candidates []nextUpCandidate, explicitSeriesID string) []Item {
	type seriesBucket struct {
		seriesID    string
		maxActivity int64
		hasHistory  bool
		hasRegular  bool
		eps         []nextUpCandidate
	}
	buckets := map[string]*seriesBucket{}
	for _, c := range candidates {
		if c.SeriesID == "" {
			continue
		}
		if explicitSeriesID != "" && c.SeriesID != explicitSeriesID {
			continue
		}
		b := buckets[c.SeriesID]
		if b == nil {
			b = &seriesBucket{seriesID: c.SeriesID}
			buckets[c.SeriesID] = b
		}
		if c.Played || c.Position > 0 {
			b.hasHistory = true
		}
		if c.LastActivity > b.maxActivity {
			b.maxActivity = c.LastActivity
		}
		if c.Item.Season > 0 {
			b.hasRegular = true
		}
		b.eps = append(b.eps, c)
	}

	type picked struct {
		item     Item
		activity int64
		seriesID string
	}
	var results []picked

	for _, b := range buckets {
		if explicitSeriesID == "" && !b.hasHistory {
			continue
		}
		sort.Slice(b.eps, func(i, j int) bool {
			a, z := b.eps[i].Item, b.eps[j].Item
			if a.Season != z.Season {
				return a.Season < z.Season
			}
			if a.Episode != z.Episode {
				return a.Episode < z.Episode
			}
			return a.ID < z.ID
		})

		var chosen *Item
		var bestResumeAct int64 = -1
		maxPlayedSeason := 0
		maxPlayedEpisode := 0

		for i := range b.eps {
			ep := &b.eps[i]
			if b.hasRegular && ep.Item.Season <= 0 {
				continue
			}
			if ep.Played {
				if ep.Item.Season > maxPlayedSeason || (ep.Item.Season == maxPlayedSeason && ep.Item.Episode > maxPlayedEpisode) {
					maxPlayedSeason = ep.Item.Season
					maxPlayedEpisode = ep.Item.Episode
				}
				continue
			}
			if ep.Position > 0 && ep.LastActivity > bestResumeAct {
				bestResumeAct = ep.LastActivity
				itemCopy := ep.Item
				chosen = &itemCopy
			}
		}

		if chosen == nil {
			for i := range b.eps {
				ep := &b.eps[i]
				if b.hasRegular && ep.Item.Season <= 0 {
					continue
				}
				if ep.Played {
					continue
				}
				if ep.Item.Season > maxPlayedSeason || (ep.Item.Season == maxPlayedSeason && ep.Item.Episode > maxPlayedEpisode) {
					itemCopy := ep.Item
					chosen = &itemCopy
					break
				}
			}
		}

		if chosen != nil {
			results = append(results, picked{
				item:     *chosen,
				activity: b.maxActivity,
				seriesID: b.seriesID,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].activity != results[j].activity {
			return results[i].activity > results[j].activity
		}
		return results[i].seriesID < results[j].seriesID
	})

	out := make([]Item, 0, len(results))
	for _, r := range results {
		out = append(out, r.item)
	}
	return out
}

func (a *App) nextUp(w http.ResponseWriter, r *http.Request, u User) {
	uid := strings.TrimSpace(q(r, "UserId"))
	if uid == "" || uid == "me" || (!u.Admin && !u.API) {
		uid = u.ID
	}
	if uid == "" {
		respond(w, M{"Items": []M{}, "TotalRecordCount": 0, "StartIndex": 0, "HasMore": false})
		return
	}

	seriesFilter := strings.TrimSpace(q(r, "SeriesId"))
	parentFilter := strings.TrimSpace(q(r, "ParentId"))
	limit, _ := strconv.Atoi(q(r, "Limit"))
	if limit <= 0 || limit > 100 {
		limit = 24
	}
	startIndex, _ := strconv.Atoi(q(r, "StartIndex"))
	if startIndex < 0 {
		startIndex = 0
	}

	// Select episodes belonging either to the requested SeriesId or to Series the user has watched/started.
	query := `
WITH ep_series AS (
	SELECT
		ep.id, ep.lib, ep.parent, ep.name, ep.kind, ep.path, ep.url, ep.overview, ep.poster,
		ep.year, ep.season, ep.episode, ep.mtime, ep.size,
		CASE WHEN p.kind = 'Season' THEN p.parent ELSE ep.parent END AS series_id,
		COALESCE(ud.played, 0) AS played,
		COALESCE(ud.position, 0) AS position,
		COALESCE(ra.updated, 0) AS last_activity
	FROM items ep
	LEFT JOIN items p ON p.id = ep.parent
	LEFT JOIN userdata ud ON ud.item = ep.id AND ud.user_id = ?
	LEFT JOIN resume_activity ra ON ra.item = ep.id AND ra.user_id = ?
	WHERE ep.kind = 'Episode' AND ep.url <> ''
	  AND (? = '' OR ep.lib = ?)
),
active_series AS (
	SELECT DISTINCT series_id
	FROM ep_series
	WHERE (? <> '' AND series_id = ?)
	   OR (? = '' AND (played > 0 OR position > 0))
)
SELECT
	e.id, e.lib, e.parent, e.name, e.kind, e.path, e.url, e.overview, e.poster,
	e.year, e.season, e.episode, e.mtime, e.size,
	e.series_id, e.played, e.position, e.last_activity
FROM ep_series e
JOIN active_series a ON a.series_id = e.series_id
ORDER BY e.series_id, e.season, e.episode, e.id`

	rows, err := a.db.Query(
		query,
		uid, uid,
		parentFilter, parentFilter,
		seriesFilter, seriesFilter,
		seriesFilter,
	)
	if err != nil {
		fail(w, http.StatusInternalServerError, "NextUp 查询失败")
		return
	}
	defer rows.Close()

	var candidates []nextUpCandidate
	for rows.Next() {
		var x Item
		var seriesID string
		var playedInt int64
		var pos, act int64
		if sErr := rows.Scan(
			&x.ID, &x.Lib, &x.Parent, &x.Name, &x.Kind, &x.Path, &x.URL, &x.Overview, &x.Poster,
			&x.Year, &x.Season, &x.Episode, &x.Mtime, &x.Size,
			&seriesID, &playedInt, &pos, &act,
		); sErr != nil {
			fail(w, http.StatusInternalServerError, "NextUp 读取失败")
			return
		}
		candidates = append(candidates, nextUpCandidate{
			Item:         x,
			SeriesID:     seriesID,
			Played:       playedInt > 0,
			Position:     pos,
			LastActivity: act,
		})
	}
	if err := rows.Err(); err != nil {
		fail(w, http.StatusInternalServerError, "NextUp 读取失败")
		return
	}

	picked := computeNextUpPerSeries(candidates, seriesFilter)
	total := len(picked)
	if startIndex >= total {
		respond(w, M{"Items": []M{}, "TotalRecordCount": total, "StartIndex": startIndex, "HasMore": false})
		return
	}
	end := startIndex + limit
	hasMore := false
	if end < total {
		hasMore = true
	} else {
		end = total
	}

	out := make([]M, 0, end-startIndex)
	viewer := u
	viewer.ID = uid
	for _, ep := range picked[startIndex:end] {
		out = append(out, a.viewerDTO(ep, r, viewer))
	}
	respond(w, M{
		"Items":            out,
		"TotalRecordCount": total,
		"StartIndex":       startIndex,
		"HasMore":          hasMore,
	})
}
