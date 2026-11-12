package embyserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type pageToken struct {
	Query    string
	Values   []string
	Position int
	Expires  int64
	Count    int
}
type pageEntry struct {
	Token string
	Until time.Time
}
type pageCache struct {
	sync.Mutex
	Entries map[string]pageEntry
}

func (a *App) pageSignature(r *http.Request, where, order string, args []any) string {
	b, _ := json.Marshal([]any{where, order, args, token(r), q(r, "Limit"), q(r, "EnableTotalRecordCount")})
	return digest(string(b))
}
func (a *App) encodePage(p pageToken) string {
	b, _ := json.Marshal(p)
	h := hmac.New(sha256.New, []byte(a.cursorSecret))
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (a *App) decodePage(s, key string) (pageToken, error) {
	var p pageToken
	if len(s) > 8192 {
		return p, errors.New("invalid cursor")
	}
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return p, errors.New("invalid cursor")
	}
	b, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return p, e
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return p, e
	}
	h := hmac.New(sha256.New, []byte(a.cursorSecret))
	h.Write(b)
	if !hmac.Equal(sig, h.Sum(nil)) {
		return p, errors.New("invalid cursor signature")
	}
	if e = json.Unmarshal(b, &p); e != nil {
		return p, e
	}
	if p.Query != key || p.Expires < time.Now().Unix() {
		return p, errors.New("cursor expired or query changed; restart from first page")
	}
	return p, nil
}
func (a *App) pageStart(r *http.Request, key string) (pageToken, error) {
	start, e := strconv.Atoi(q(r, "StartIndex"))
	if q(r, "StartIndex") == "" {
		start = 0
		e = nil
	}
	if e != nil || start < 0 {
		return pageToken{}, errors.New("invalid StartIndex")
	}
	cursor := q(r, "Cursor")
	if cursor == "" && start > 0 {
		a.pages.Lock()
		v := a.pages.Entries[key+":"+strconv.Itoa(start)]
		a.pages.Unlock()
		if time.Now().Before(v.Until) {
			cursor = v.Token
		}
		if cursor == "" {
			return pageToken{Query: key, Position: start, Count: -1, Expires: time.Now().Add(15 * time.Minute).Unix()}, nil
		}
	}
	if cursor != "" {
		p, e := a.decodePage(cursor, key)
		if e == nil && start != 0 && start != p.Position {
			return p, errors.New("cursor position mismatch")
		}
		return p, e
	}
	return pageToken{Query: key, Count: -1, Expires: time.Now().Add(15 * time.Minute).Unix()}, nil
}
func (a *App) savePage(p pageToken) string {
	s := a.encodePage(p)
	a.pages.Lock()
	defer a.pages.Unlock()
	if a.pages.Entries == nil {
		a.pages.Entries = map[string]pageEntry{}
	}
	if len(a.pages.Entries) >= 2048 {
		now := time.Now()
		for k, v := range a.pages.Entries {
			if now.After(v.Until) {
				delete(a.pages.Entries, k)
			}
		}
		if len(a.pages.Entries) >= 2048 {
			for k := range a.pages.Entries {
				delete(a.pages.Entries, k)
				break
			}
		}
	}
	a.pages.Entries[p.Query+":"+strconv.Itoa(p.Position)] = pageEntry{s, time.Unix(p.Expires, 0)}
	return s
}
func (a *App) pageItems(r *http.Request, u User, where string, args []any, order string, limit int) ([]Item, pageToken, string, bool, error) {
	key := a.pageSignature(r, where, order, args)
	page, err := a.pageStart(r, key)
	if err != nil {
		return nil, page, "", false, err
	}
	// Optional totals are calculated once per cursor chain, never on every deep page.
	if page.Count < 0 && !strings.EqualFold(q(r, "EnableTotalRecordCount"), "false") {
		page.Count, err = a.itemPageStore().Count(where, args)
		if err != nil {
			return nil, page, "", false, err
		}
	}
	result, err := a.itemPageStore().LoadPage(ItemPageQuery{
		Where:        where,
		Args:         args,
		Order:        order,
		UserID:       u.ID,
		CursorValues: page.Values,
		Position:     page.Position,
		Limit:        limit,
	})
	if err != nil {
		return nil, page, "", false, err
	}
	next := ""
	if result.HasMore {
		page.Expires = time.Now().Add(15 * time.Minute).Unix()
		page.Position += len(result.Items)
		page.Values = result.CursorValues
		next = a.savePage(page)
	}
	return result.Items, page, next, result.HasMore, nil
}

// Listing avoids sidecar parsing, actor lookups, filesystem image discovery and probing.
func (a *App) listDTO(x Item, r *http.Request, u User) M {
	// Clients such as Infuse use episode lists as their detail payload.
	if hasBrowseDetailFields(r) {
		m := a.viewerDTO(x, r, u)
		if requestedField(r, "Etag") {
			b, _ := json.Marshal(m)
			m["Etag"] = digest(string(b))
		}
		return m
	}
	folder := x.Kind == "Series" || x.Kind == "Season"
	m := M{"Id": x.ID, "ServerId": a.serverID, "ParentId": x.Parent, "Name": x.Name, "SortName": itemSortName(x), "Overview": x.Overview, "Type": x.Kind, "IsFolder": folder, "LocationType": "FileSystem", "ProductionYear": x.Year, "IndexNumber": x.Episode, "ParentIndexNumber": x.Season, "ImageTags": M{}, "BackdropImageTags": []string{}, "PrimaryImageAspectRatio": 2.0 / 3, "DateCreated": time.Unix(0, x.Mtime).UTC().Format(time.RFC3339), "UserData": makeUserItemDataDTO(x.ID, 0, false, false)}
	if !folder {
		m["MediaType"] = "Video"
	}
	if x.Poster != "" {
		m["ImageTags"] = M{"Primary": a.listImageTag(x)}
	} else if p := a.imagePath(x.ID, "Primary"); p != "" {
		m["ImageTags"] = M{"Primary": a.imageTag(x.ID, "Primary", p)}
	} else {
		m["ImageTags"] = M{"Primary": a.generatedTag(x.ID, "Primary")}
	}
	if x.Kind == "Episode" {
		m["PrimaryImageAspectRatio"] = 16.0 / 9
		thumbPath := episodeThumb(x)
		if thumbPath == "" && x.Poster != "" {
			thumbPath = x.Poster
		}
		if thumbPath != "" {
			if _, ok := m["ImageTags"].(M); !ok {
				m["ImageTags"] = M{}
			}
			m["ImageTags"].(M)["Thumb"] = a.imageTag(x.ID, "Thumb", thumbPath)
			m["ImageTags"].(M)["Primary"] = a.imageTag(x.ID, "Primary", thumbPath)
		}
	}
	if u.API {
		m["Path"] = x.Path
		if x.URL != "" && strings.Contains(strings.ToLower(q(r, "Fields")), "mediasources") {
			m["MediaSources"] = []MediaSourceDTO{a.source(x, token(r))}
		}
	}
	if !u.API && r != nil && x.URL != "" && strings.Contains(strings.ToLower(q(r, "Fields")), "mediasources") {
		m["MediaSources"] = []M{a.viewerSource(x, r, u)}
	}
	if x.Kind == "Movie" || x.Kind == "Series" {
		delete(m, "IndexNumber")
		delete(m, "ParentIndexNumber")
		if x.Kind == "Series" {
			m["ChildCount"] = 0
			m["RecursiveItemCount"] = 0
			m["EpisodeCount"] = 0
		}
	}
	if x.Year <= 0 {
		delete(m, "ProductionYear")
	}

	if x.Kind == "Season" {
		m["IndexNumber"] = x.Season
		m["SeriesId"] = x.Parent
	}
	return m
}

// Browse item relationships are batched by the repository to avoid per-row SQL.
func (a *App) attachBrowseMetadata(items []Item, out []M) {
	if a.db == nil || len(items) == 0 {
		return
	}
	metadata, err := a.mediaItems().BrowseMetadata(items)
	if err != nil {
		log.Printf("load browse item metadata: %v", err)
	}
	for i, item := range items {
		if i >= len(out) {
			continue
		}
		value, ok := metadata[item.ID]
		if !ok {
			continue
		}
		if item.Kind == "Series" {
			out[i]["ChildCount"] = value.ChildCount
			out[i]["RecursiveItemCount"] = value.EpisodeCount
			out[i]["EpisodeCount"] = value.EpisodeCount
		}
		if item.Kind == "Episode" {
			if value.SeasonID != "" {
				out[i]["SeasonId"] = value.SeasonID
				if value.SeasonName != "" {
					out[i]["SeasonName"] = value.SeasonName
				}
			}
			if value.SeriesID != "" {
				out[i]["SeriesId"] = value.SeriesID
				if value.SeriesName != "" {
					out[i]["SeriesName"] = value.SeriesName
				}
			}
		}
	}
}
func (a *App) listDTOs(items []Item, r *http.Request, u User) []M {
	out := make([]M, 0, len(items))
	positions := map[string]int{}
	itemIDs := make([]string, 0, len(items))
	for _, x := range items {
		positions[x.ID] = len(out)
		itemIDs = append(itemIDs, x.ID)
		m := a.listDTO(x, r, u)
		if !m["IsFolder"].(bool) {
			if count, ok := mediaSourceCount(m["MediaSources"]); ok {
				m["MediaSourceCount"] = count
			} else {
				delete(m, "MediaSourceCount")
			}
		}
		out = append(out, m)
	}
	if !u.API && len(items) > 0 && !strings.EqualFold(q(r, "EnableUserData"), "false") && a.db != nil {
		states, err := a.userStore().ListPlaybackStates(u.ID, itemIDs)
		if err != nil {
			log.Printf("list user playback data: %v", err)
		} else {
			for itemID, state := range states {
				if i, ok := positions[itemID]; ok {
					out[i]["UserData"] = makeUserItemDataDTO(itemID, state.Position, state.Played, false)
				}
			}
		}
		favorites, err := a.userStore().ListFavorites(u.ID, itemIDs)
		if err != nil {
			log.Printf("list user favorites: %v", err)
		} else {
			for itemID := range favorites {
				if i, ok := positions[itemID]; ok {
					if ud, ok := out[i]["UserData"].(UserItemDataDTO); ok {
						ud.IsFavorite = true
						out[i]["UserData"] = ud
					} else {
						out[i]["UserData"] = makeUserItemDataDTO(itemID, 0, false, true)
					}
				}
			}
		}
	}
	a.attachBrowseMetadata(items, out)
	return out
}

func (a *App) listImageTag(x Item) string {
	h := hmac.New(sha256.New, []byte(a.cursorSecret))
	h.Write([]byte("list-primary|" + x.ID + "|" + x.Poster + "|" + strconv.FormatInt(x.Mtime, 10)))
	return fmt.Sprintf("%x", h.Sum(nil))[:32]
}

func requestedField(r *http.Request, name string) bool {
	if r == nil {
		return false
	}
	for _, field := range strings.Split(q(r, "Fields"), ",") {
		if strings.EqualFold(strings.TrimSpace(field), name) {
			return true
		}
	}
	return false
}
func hasBrowseDetailFields(r *http.Request) bool {
	return requestedField(r, "Overview") || requestedField(r, "Genres") || requestedField(r, "ProviderIds") || requestedField(r, "Etag") || requestedField(r, "AlternateMediaSources")
}
func browseMediaDetails(r *http.Request) bool {
	// A season list (even Limit=1) does not identify the episode being viewed.
	ids := strings.TrimSpace(q(r, "Ids"))
	return requestedField(r, "MediaSources") && requestedField(r, "Overview") && ids != "" && !strings.Contains(ids, ",")
}
