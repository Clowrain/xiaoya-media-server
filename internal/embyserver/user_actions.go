package embyserver

import (
	"net/http"
	"strings"
	"time"
)

// handleUserActionRoutes intercepts user action endpoints such as FavoriteItems, PlayedItems, NextUp.
func (a *App) handleUserActionRoutes(w http.ResponseWriter, r *http.Request, u User, p string) bool {
	l := strings.ToLower(p)
	parts := strings.Split(strings.Trim(p, "/"), "/")

	// 1. NextUp: match /shows/nextup or /users/{userId}/shows/nextup
	if strings.HasSuffix(l, "/shows/nextup") {
		a.nextUp(w, r, u)
		return true
	}

	// 2. FavoriteItems & PlayedItems:
	// /users/{userId}/favoriteitems/{itemId}
	// /favoriteitems/{itemId}
	// /users/{userId}/playeditems/{itemId}
	// /playeditems/{itemId}
	if len(parts) >= 2 {
		var uid, itemID string
		isFav := false
		isPlayed := false

		if len(parts) == 4 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "favoriteitems") {
			uid = parts[1]
			itemID = parts[3]
			isFav = true
		} else if len(parts) == 2 && strings.EqualFold(parts[0], "favoriteitems") {
			uid = u.ID
			itemID = parts[1]
			isFav = true
		} else if len(parts) == 4 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "playeditems") {
			uid = parts[1]
			itemID = parts[3]
			isPlayed = true
		} else if len(parts) == 2 && strings.EqualFold(parts[0], "playeditems") {
			uid = u.ID
			itemID = parts[1]
			isPlayed = true
		} else if len(parts) == 5 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "items") && strings.EqualFold(parts[4], "rating") {
			uid = parts[1]
			itemID = parts[3]
			if r.Method == http.MethodDelete {
				a.setFavorite(w, r, u, uid, itemID, false)
				return true
			}
		}

		if uid == "me" || uid == "" {
			uid = u.ID
		}

		if isFav {
			switch r.Method {
			case http.MethodPost:
				a.setFavorite(w, r, u, uid, itemID, true)
				return true
			case http.MethodDelete:
				a.setFavorite(w, r, u, uid, itemID, false)
				return true
			}
		}

		if isPlayed {
			switch r.Method {
			case http.MethodPost:
				a.setPlayed(w, r, u, uid, itemID, true)
				return true
			case http.MethodDelete:
				a.setPlayed(w, r, u, uid, itemID, false)
				return true
			}
		}
	}

	return false
}

func (a *App) setFavorite(w http.ResponseWriter, r *http.Request, u User, uid, itemID string, fav bool) {
	if !u.API && !u.Admin && uid != u.ID {
		fail(w, http.StatusForbidden, "无权限")
		return
	}
	if a.db != nil {
		if fav {
			_, _ = a.db.Exec("INSERT INTO user_favorites(user_id, item, created_at) VALUES(?,?,?) ON CONFLICT(user_id,item) DO NOTHING", uid, itemID, time.Now().Unix())
		} else {
			_, _ = a.db.Exec("DELETE FROM user_favorites WHERE user_id=? AND item=?", uid, itemID)
		}
	}
	var pos int64
	var played bool
	if a.db != nil {
		a.db.QueryRow("SELECT position,played FROM userdata WHERE user_id=? AND item=?", uid, itemID).Scan(&pos, &played)
	}
	respond(w, M{
		"Played":                played,
		"IsFavorite":            fav,
		"PlaybackPositionTicks": pos,
		"Key":                   itemID,
		"ItemId":                itemID,
	})
}

func (a *App) setPlayed(w http.ResponseWriter, r *http.Request, u User, uid, itemID string, played bool) {
	if !u.API && !u.Admin && uid != u.ID {
		fail(w, http.StatusForbidden, "无权限")
		return
	}
	if a.db != nil {
		playedVal := int64(0)
		if played {
			playedVal = 1
		}
		// 1. 更新当前 item
		_, _ = a.db.Exec("INSERT INTO userdata(user_id,item,played,position) VALUES(?,?,?,0) ON CONFLICT(user_id,item) DO UPDATE SET played=excluded.played, position=0", uid, itemID, playedVal)

		// 2. 如果是 Series 或 Season，级联更新其下的 Episode
		var kind string
		if a.db.QueryRow("SELECT kind FROM items WHERE id=?", itemID).Scan(&kind) == nil {
			if kind == "Series" {
				_, _ = a.db.Exec(`INSERT INTO userdata(user_id,item,played,position)
					SELECT ?, id, ?, 0 FROM items 
					WHERE kind='Episode' AND (parent=? OR parent IN (SELECT id FROM items WHERE parent=? AND kind='Season'))
					ON CONFLICT(user_id,item) DO UPDATE SET played=excluded.played, position=0`, uid, playedVal, itemID, itemID)
			} else if kind == "Season" {
				_, _ = a.db.Exec(`INSERT INTO userdata(user_id,item,played,position)
					SELECT ?, id, ?, 0 FROM items 
					WHERE kind='Episode' AND parent=?
					ON CONFLICT(user_id,item) DO UPDATE SET played=excluded.played, position=0`, uid, playedVal, itemID)
			}
		}

		if !played {
			// 标记未播放时，从继续观看活跃记录中删除
			_, _ = a.db.Exec("DELETE FROM resume_activity WHERE user_id=? AND item=?", uid, itemID)
			_, _ = a.db.Exec("DELETE FROM plays WHERE user_id=? AND item=?", uid, itemID)
		}
	}

	var isFav bool
	if a.db != nil {
		var n int
		a.db.QueryRow("SELECT 1 FROM user_favorites WHERE user_id=? AND item=?", uid, itemID).Scan(&n)
		isFav = (n > 0)
	}

	respond(w, M{
		"Played":                played,
		"IsFavorite":            isFav,
		"PlaybackPositionTicks": 0,
		"Key":                   itemID,
		"ItemId":                itemID,
	})
}
