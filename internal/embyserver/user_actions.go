package embyserver

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// handleUserActionRoutes intercepts user action endpoints such as FavoriteItems, PlayedItems, NextUp, UserData, Rating.
func (a *App) handleUserActionRoutes(w http.ResponseWriter, r *http.Request, u User, p string) bool {
	l := strings.ToLower(p)
	parts := strings.Split(strings.Trim(p, "/"), "/")

	// 1. NextUp: match /shows/nextup or /users/{userId}/shows/nextup
	if strings.HasSuffix(l, "/shows/nextup") {
		a.nextUp(w, r, u)
		return true
	}

	// 1.5 GET /Users/{userId}/FavoriteItems & /Users/{userId}/PlayedItems: filtered QueryResult lists.
	// Filters is always non-empty here, so parentlessBrowse never short-circuits to libraryDTOs.
	if r.Method == http.MethodGet && len(parts) == 3 && strings.EqualFold(parts[0], "users") && (strings.EqualFold(parts[2], "favoriteitems") || strings.EqualFold(parts[2], "playeditems")) {
		uid := parts[1]
		if uid == "me" || uid == "" {
			uid = u.ID
		}
		if !u.API && !u.Admin && uid != u.ID {
			fail(w, http.StatusForbidden, "无权限")
			return true
		}
		if strings.EqualFold(parts[2], "favoriteitems") {
			setQuery(r, "Filters", "IsFavorite")
		} else {
			setQuery(r, "Filters", "IsPlayed")
		}
		a.items(w, r, u, false)
		return true
	}

	// 2. FavoriteItems & PlayedItems & UserData & Rating:
	if len(parts) >= 2 {
		var uid, itemID string
		isFavRoute := false
		isPlayedRoute := false

		// /users/{userId}/favoriteitems/{itemId}
		if len(parts) == 4 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "favoriteitems") {
			uid = parts[1]
			itemID = parts[3]
			isFavRoute = true
		} else if len(parts) == 2 && strings.EqualFold(parts[0], "favoriteitems") {
			// /favoriteitems/{itemId}
			uid = u.ID
			itemID = parts[1]
			isFavRoute = true
		} else if len(parts) == 4 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "playeditems") {
			// /users/{userId}/playeditems/{itemId}
			uid = parts[1]
			itemID = parts[3]
			isPlayedRoute = true
		} else if len(parts) == 2 && strings.EqualFold(parts[0], "playeditems") {
			// /playeditems/{itemId}
			uid = u.ID
			itemID = parts[1]
			isPlayedRoute = true
		} else if len(parts) == 5 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "items") && strings.EqualFold(parts[4], "userdata") {
			// /users/{userId}/items/{itemId}/userdata
			uid = parts[1]
			itemID = parts[3]
			if uid == "me" || uid == "" {
				uid = u.ID
			}
			a.updateUserData(w, r, u, uid, itemID)
			return true
		} else if len(parts) == 5 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "items") && strings.EqualFold(parts[4], "rating") {
			// /users/{userId}/items/{itemId}/rating
			uid = parts[1]
			itemID = parts[3]
			if uid == "me" || uid == "" {
				uid = u.ID
			}
			likes := strings.EqualFold(q(r, "Likes"), "true")
			if r.Method == http.MethodDelete {
				a.setFavorite(w, r, u, uid, itemID, false)
			} else {
				a.setFavorite(w, r, u, uid, itemID, likes || r.Method == http.MethodPost)
			}
			return true
		} else if len(parts) == 3 && strings.EqualFold(parts[0], "items") && strings.EqualFold(parts[2], "rating") {
			// /items/{itemId}/rating
			uid = u.ID
			itemID = parts[1]
			likes := strings.EqualFold(q(r, "Likes"), "true")
			if r.Method == http.MethodDelete {
				a.setFavorite(w, r, u, uid, itemID, false)
			} else {
				a.setFavorite(w, r, u, uid, itemID, likes || r.Method == http.MethodPost)
			}
			return true
		} else if len(parts) == 3 && strings.EqualFold(parts[0], "items") && strings.EqualFold(parts[2], "userdata") {
			// /items/{itemId}/userdata
			uid = u.ID
			itemID = parts[1]
			a.updateUserData(w, r, u, uid, itemID)
			return true
		}

		if uid == "me" || uid == "" {
			uid = u.ID
		}

		if isFavRoute {
			switch r.Method {
			case http.MethodPost:
				a.setFavorite(w, r, u, uid, itemID, true)
				return true
			case http.MethodDelete:
				a.setFavorite(w, r, u, uid, itemID, false)
				return true
			}
		}

		if isPlayedRoute {
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

// userItemDataDTO returns the protocol state shared by item and user-data endpoints.
func (a *App) userItemDataDTO(uid, itemID string, isFav bool) UserItemDataDTO {
	state, err := a.userStore().GetPlaybackState(uid, itemID)
	if err != nil {
		log.Printf("read user playback state: %v", err)
	}
	return makeUserItemDataDTO(itemID, state.Position, state.Played, isFav)
}

func (a *App) setFavorite(w http.ResponseWriter, r *http.Request, u User, uid, itemID string, fav bool) {
	if !u.API && !u.Admin && uid != u.ID {
		fail(w, http.StatusForbidden, "无权限")
		return
	}
	if err := a.userStore().SetFavorite(uid, itemID, fav, time.Now().Unix()); err != nil {
		log.Printf("persist user favorite state for user %q item %q: %v", uid, itemID, err)
		fail(w, http.StatusInternalServerError, "无法更新收藏状态")
		return
	}
	respond(w, a.userItemDataDTO(uid, itemID, fav))
}

func (a *App) updateUserData(w http.ResponseWriter, r *http.Request, u User, uid, itemID string) {
	if !u.API && !u.Admin && uid != u.ID {
		fail(w, http.StatusForbidden, "无权限")
		return
	}
	var b struct {
		IsFavorite            *bool
		Played                *bool
		PlaybackPositionTicks *int64
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&b); err != nil && err != io.EOF {
		fail(w, http.StatusBadRequest, "无效 JSON")
		return
	} else if err == nil {
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			fail(w, http.StatusBadRequest, "无效 JSON")
			return
		}
	}

	isFav := false
	if a.db != nil {
		var err error
		isFav, err = a.userStore().IsFavorite(uid, itemID)
		if err != nil {
			log.Printf("read user favorite state: %v", err)
			isFav = false
		}
	}

	var writeErr error
	if b.IsFavorite != nil {
		isFav = *b.IsFavorite
		if err := a.userStore().SetFavorite(uid, itemID, isFav, time.Now().Unix()); err != nil {
			log.Printf("persist user favorite state for user %q item %q: %v", uid, itemID, err)
			writeErr = err
		}
	}

	if b.Played != nil {
		if err := a.userStore().SetPlayedItem(uid, itemID, *b.Played); err != nil {
			log.Printf("persist user played state for user %q item %q: %v", uid, itemID, err)
			if writeErr == nil {
				writeErr = err
			}
		}
	}
	if b.PlaybackPositionTicks != nil {
		if err := a.userStore().SetPlaybackPosition(uid, itemID, *b.PlaybackPositionTicks); err != nil {
			log.Printf("persist user playback position for user %q item %q: %v", uid, itemID, err)
			if writeErr == nil {
				writeErr = err
			}
		}
	}
	if writeErr != nil {
		fail(w, http.StatusInternalServerError, "无法更新用户数据")
		return
	}

	respond(w, a.userItemDataDTO(uid, itemID, isFav))
}

func (a *App) setPlayed(w http.ResponseWriter, r *http.Request, u User, uid, itemID string, played bool) {
	if !u.API && !u.Admin && uid != u.ID {
		fail(w, http.StatusForbidden, "无权限")
		return
	}
	if err := a.userStore().SetPlayedTree(uid, itemID, played); err != nil {
		log.Printf("persist user played cascade for user %q item %q: %v", uid, itemID, err)
		fail(w, http.StatusInternalServerError, "无法更新播放状态")
		return
	}
	isFavorite, err := a.userStore().IsFavorite(uid, itemID)
	if err != nil {
		log.Printf("read user favorite state: %v", err)
	}

	dto := a.userItemDataDTO(uid, itemID, isFavorite)
	dto.Played = played
	respond(w, dto)
}
