package embyserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
func itemSortValue(x Item, col string) string {
	switch col {
	case "id":
		return x.ID
	case "name":
		return x.Name
	case "year":
		return strconv.Itoa(x.Year)
	case "mtime":
		return strconv.FormatInt(x.Mtime, 10)
	case "season":
		return strconv.Itoa(x.Season)
	case "episode":
		return strconv.Itoa(x.Episode)
	}
	return ""
}
func (a *App) pageItems(r *http.Request, u User, where string, args []any, order string, limit int) ([]Item, pageToken, string, bool, error) {
	key := a.pageSignature(r, where, order, args)
	p, e := a.pageStart(r, key)
	if e != nil {
		return nil, p, "", false, e
	}
	// Optional totals are calculated once per cursor chain, never on every deep page.
	if p.Count < 0 && !strings.EqualFold(q(r, "EnableTotalRecordCount"), "false") {
		if e = a.db.QueryRow("SELECT count(*) FROM items WHERE "+where, args...).Scan(&p.Count); e != nil {
			return nil, p, "", false, e
		}
	}
	columns := []string{}
	desc := strings.Contains(order, "DESC")
	if order == "resume" {
		columns = []string{"COALESCE((SELECT updated FROM resume_activity WHERE user_id='" + strings.ReplaceAll(u.ID, "'", "''") + "' AND item=items.id),0)", "id"}
		desc = true
	} else {
		for _, part := range strings.Split(order, ",") {
			columns = append(columns, strings.Fields(part)[0])
		}
	}
	direction, op := " ASC", ">"
	if desc {
		direction = " DESC"
		op = "<"
	}
	orderParts := []string{}
	for _, col := range columns {
		orderParts = append(orderParts, col+direction)
	}
	params := append([]any{}, args...)
	if len(p.Values) > 0 {
		if len(p.Values) != len(columns) {
			return nil, p, "", false, errors.New("invalid cursor keys")
		}
		where += " AND (" + strings.Join(columns, ",") + ") " + op + " (" + strings.TrimRight(strings.Repeat("?,", len(columns)), ",") + ")"
		for _, v := range p.Values {
			params = append(params, v)
		}
	}
	params = append(params, limit+1)
	sql := "SELECT " + cols + " FROM items WHERE " + where + " ORDER BY " + strings.Join(orderParts, ",") + " LIMIT ?"
	if len(p.Values) == 0 && p.Position > 0 {
		sql += " OFFSET ?"
		params = append(params, p.Position)
	}
	rows, e := a.db.Query(sql, params...)
	if e != nil {
		return nil, p, "", false, e
	}
	loaded := []Item{}
	for rows.Next() {
		x, e := readItem(rows)
		if e != nil {
			rows.Close()
			return nil, p, "", false, e
		}
		loaded = append(loaded, x)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, p, "", false, e
	}
	more := len(loaded) > limit
	if more {
		loaded = loaded[:limit]
	}
	next := ""
	if more {
		last := loaded[len(loaded)-1]
		n := p
		n.Expires = time.Now().Add(15 * time.Minute).Unix()
		n.Position += len(loaded)
		n.Values = nil
		for _, col := range columns {
			if strings.HasPrefix(col, "COALESCE") {
				var updated int64
				e = a.db.QueryRow("SELECT COALESCE((SELECT updated FROM resume_activity WHERE user_id=? AND item=?),0)", u.ID, last.ID).Scan(&updated)
				if e != nil {
					return nil, p, "", false, e
				}
				n.Values = append(n.Values, fmt.Sprint(updated))
			} else if col == "rating" {
				// rating lives outside cols/Item; read the cursor key like the resume column above.
				var rating float64
				e = a.db.QueryRow("SELECT rating FROM items WHERE id=?", last.ID).Scan(&rating)
				if e != nil {
					return nil, p, "", false, e
				}
				n.Values = append(n.Values, strconv.FormatFloat(rating, 'g', -1, 64))
			} else {
				n.Values = append(n.Values, itemSortValue(last, col))
			}
		}
		next = a.savePage(n)
	}
	return loaded, p, next, more, nil
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
	m := M{"Id": x.ID, "ServerId": a.serverID, "ParentId": x.Parent, "Name": x.Name, "SortName": x.Name, "Overview": x.Overview, "Type": x.Kind, "IsFolder": folder, "LocationType": "FileSystem", "ProductionYear": x.Year, "IndexNumber": x.Episode, "ParentIndexNumber": x.Season, "ImageTags": M{}, "BackdropImageTags": []string{}, "PrimaryImageAspectRatio": 2.0 / 3, "DateCreated": time.Unix(0, x.Mtime).UTC().Format(time.RFC3339), "UserData": userItemDataMap(x.ID, 0, false, false)}
	if !folder {
		m["MediaType"] = "Video"
	}
	if x.Poster != "" {
		m["ImageTags"] = M{"Primary": a.listImageTag(x)}
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
			m["MediaSources"] = []M{a.source(x, token(r))}
		}
	}
	if !u.API && r != nil && x.URL != "" && strings.Contains(strings.ToLower(q(r, "Fields")), "mediasources") {
		m["MediaSources"] = []M{a.viewerSource(x, r, u)}
	}
	if x.Kind == "Movie" || x.Kind == "Series" {
		delete(m, "IndexNumber")
		delete(m, "ParentIndexNumber")
		if x.Kind == "Series" {
			var n int
			a.db.QueryRow("SELECT count(*) FROM items WHERE parent=?", x.ID).Scan(&n)
			m["ChildCount"] = n
			var episodes int
			a.db.QueryRow(`WITH RECURSIVE tree(id) AS (
				SELECT id FROM items WHERE parent=?
				UNION
				SELECT items.id FROM items JOIN tree ON items.parent=tree.id
			) SELECT count(*) FROM items WHERE id IN (SELECT id FROM tree) AND kind='Episode'`, x.ID).Scan(&episodes)
			m["RecursiveItemCount"] = episodes
			m["EpisodeCount"] = episodes
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

// Episode rows in a listing still need their series/season ancestry; one chunked
// join query serves the whole page without sidecar reads or probe triggers.
func (a *App) attachSeriesInfo(items []Item, out []M) {
	if a.db == nil {
		return
	}
	rowOf := map[string]int{}
	ids := []string{}
	for i, x := range items {
		if x.Kind == "Episode" {
			rowOf[x.ID] = i
			ids = append(ids, x.ID)
		}
	}
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		args := make([]any, len(chunk))
		for j, v := range chunk {
			args[j] = v
		}
		rows, e := a.db.Query(`SELECT ep.id, se.id, se.name, sr.id, sr.name
FROM items ep
LEFT JOIN items se ON se.id = ep.parent AND se.kind = 'Season'
LEFT JOIN items sr ON sr.id = CASE WHEN se.id IS NOT NULL THEN se.parent ELSE ep.parent END
WHERE ep.id IN (`+strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")+`)`, args...)
		if e != nil {
			return
		}
		for rows.Next() {
			var id, seasonID, seasonName, seriesID, seriesName sql.NullString
			if e := rows.Scan(&id, &seasonID, &seasonName, &seriesID, &seriesName); e != nil {
				continue
			}
			i, ok := rowOf[id.String]
			if !ok {
				continue
			}
			if seasonID.Valid && seasonID.String != "" {
				out[i]["SeasonId"] = seasonID.String
				if seasonName.Valid {
					out[i]["SeasonName"] = seasonName.String
				}
			}
			if seriesID.Valid && seriesID.String != "" {
				out[i]["SeriesId"] = seriesID.String
				if seriesName.Valid {
					out[i]["SeriesName"] = seriesName.String
				}
			}
		}
		rows.Close()
		if e := rows.Err(); e != nil {
			return
		}
	}
}

func (a *App) listDTOs(items []Item, r *http.Request, u User) []M {
	out := make([]M, 0, len(items))
	positions := map[string]int{}
	args := []any{u.ID}
	for _, x := range items {
		positions[x.ID] = len(out)
		args = append(args, x.ID)
		m := a.listDTO(x, r, u)
		if !m["IsFolder"].(bool) {
			if sources, ok := m["MediaSources"].([]M); ok {
				m["MediaSourceCount"] = len(sources)
			} else {
				delete(m, "MediaSourceCount")
			}
		}
		out = append(out, m)
	}
	if !u.API && len(items) > 0 && !strings.EqualFold(q(r, "EnableUserData"), "false") && a.db != nil {
		rows, e := a.db.Query("SELECT item,position,played FROM userdata WHERE user_id=? AND item IN ("+strings.TrimRight(strings.Repeat("?,", len(items)), ",")+")", args...)
		if e == nil {
			defer rows.Close()
			for rows.Next() {
				var item string
				var pos int64
				var played bool
				if rows.Scan(&item, &pos, &played) == nil {
					if i, ok := positions[item]; ok {
						out[i]["UserData"] = userItemDataMap(item, pos, played, false)
					}
				}
			}
		}
		favRows, fe := a.db.Query("SELECT item FROM user_favorites WHERE user_id=? AND item IN ("+strings.TrimRight(strings.Repeat("?,", len(items)), ",")+")", args...)
		if fe == nil {
			defer favRows.Close()
			for favRows.Next() {
				var item string
				if favRows.Scan(&item) == nil {
					if i, ok := positions[item]; ok {
						if ud, ok := out[i]["UserData"].(M); ok {
							ud["IsFavorite"] = true
						} else {
							out[i]["UserData"] = userItemDataMap(item, 0, false, true)
						}
					}
				}
			}
		}
	}
	a.attachSeriesInfo(items, out)
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
