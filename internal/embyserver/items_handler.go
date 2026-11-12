package embyserver

import (
	"errors"
	"log"
	"net/http"
	"strings"
)

func (a *App) single(w http.ResponseWriter, r *http.Request, u User, i string) {
	if i == "root" || i == a.serverID {
		respond(w, a.rootDTO())
		return
	}
	if strings.HasPrefix(i, "person-") {
		a.personDetail(w, r, u, i)
		return
	}
	x, e := a.item(i)
	if e != nil {
		for _, v := range a.libraryDTOs() {
			if v["Id"] == i {
				respond(w, v)
				return
			}
		}
		fail(w, 404, "媒体不存在")
		return
	}
	// Respond with local/cached metadata first; network probing never blocks detail rendering.
	if cfg := a.probeSettings(); !u.API && cfg.Browse {
		defer func() {
			a.queueProbe(x, token(r), r.UserAgent(), false)
		}()
	}
	m := a.viewerDTO(x, r, u)
	if !u.API {
		var pos int64
		var played bool
		var fav int
		if a.db != nil {
			state, stateErr := a.userStore().GetPlaybackState(u.ID, i)
			if stateErr != nil {
				log.Printf("read user playback state: %v", stateErr)
			} else {
				pos, played = state.Position, state.Played
			}
			isFavorite, favoriteErr := a.userStore().IsFavorite(u.ID, i)
			if favoriteErr != nil {
				log.Printf("read user favorite state: %v", favoriteErr)
			} else if isFavorite {
				fav = 1
			}
		}
		m["UserData"] = makeUserItemDataDTO(i, pos, played, fav > 0)
	}
	respond(w, m)
	_ = http.NewResponseController(w).Flush()
}

func (a *App) items(w http.ResponseWriter, r *http.Request, u User, latest bool) {
	a.normalizeBrowseTypes(r, latest)
	resume := isResumeBrowse(r)
	if !resume && !latest && (parentlessBrowse(r) || libraryTypes(r)) {
		respond(w, ItemsQueryResultDTO{Items: a.libraryDTOs(), TotalRecordCount: len(a.libraries()), StartIndex: 0})
		return
	}
	parent := q(r, "ParentId")
	if (parent == "root" || parent == a.serverID) && !strings.EqualFold(q(r, "Recursive"), "true") && q(r, "IncludeItemTypes") == "" {
		respond(w, ItemsQueryResultDTO{Items: a.libraryDTOs(), TotalRecordCount: len(a.libraries()), StartIndex: 0})
		return
	}

	query, err := a.buildItemBrowseQuery(r, u, latest)
	if err != nil {
		var requestErr *browseRequestError
		if errors.As(err, &requestErr) {
			fail(w, requestErr.status, requestErr.message)
			return
		}
		log.Printf("build items query: %v", err)
		fail(w, 500, "查询失败: "+err.Error())
		return
	}
	loaded, page, next, more, err := a.pageItems(r, u, query.Where, query.Args, query.Order, query.Limit)
	if err != nil {
		log.Printf("items query: %v", err)
		fail(w, 500, "查询失败: "+err.Error())
		return
	}
	if !u.API && browseMediaDetails(r) && len(loaded) == 1 && loaded[0].ID == strings.TrimSpace(q(r, "Ids")) && a.probeSettings().Browse {
		defer func() {
			_ = http.NewResponseController(w).Flush()
			a.queueProbe(loaded[0], token(r), r.UserAgent(), false)
		}()
	}
	out := a.listDTOs(loaded, r, u)
	if page.Count < 0 {
		page.Count = page.Position + len(out)
		if more {
			page.Count++
		}
	}
	if next != "" {
		w.Header().Set("X-Next-Cursor", next)
	}
	if latest {
		respond(w, out)
	} else {
		respond(w, ItemsQueryResultDTO{
			Items:            out,
			TotalRecordCount: page.Count,
			StartIndex:       page.Position,
			NextCursor:       &next,
			HasMore:          &more,
		})
	}
}
