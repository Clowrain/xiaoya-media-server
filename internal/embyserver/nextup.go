package embyserver

import (
	"net/http"
	"sort"
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
	// 用户明确要求移除“接下来观看”，直接响应空列表
	respond(w, M{"Items": []M{}, "TotalRecordCount": 0, "StartIndex": 0, "HasMore": false})
}
