package embyserver

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"xiaoya-media-server/internal/metadata/tmdb"
)

func (a *App) browseSchema() error {
	_, e := a.db.Exec(`CREATE TABLE IF NOT EXISTS item_metadata(item TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS item_people(item TEXT REFERENCES items(id) ON DELETE CASCADE,person TEXT,name TEXT,role TEXT,type TEXT,thumb TEXT,PRIMARY KEY(item,person,type));
 CREATE INDEX IF NOT EXISTS people_person ON item_people(person);
 CREATE TABLE IF NOT EXISTS actor_image_failures(person TEXT PRIMARY KEY, path TEXT NOT NULL, expires BIGINT NOT NULL);`)
	if e == nil {
		_, e = a.db.Exec("INSERT INTO settings(k,v) VALUES('image_secret',?) ON CONFLICT DO NOTHING", id()+id())
	}
	return e
}
func personID(name string) string { return "person-" + digest(strings.TrimSpace(name))[:32] }
func (a *App) metadata(x Item) sidecar {
	var raw string
	var n sidecar
	if a.db != nil && a.db.QueryRow("SELECT data FROM item_metadata WHERE item=?", x.ID).Scan(&raw) == nil && json.Unmarshal([]byte(raw), &n) == nil {
		return n
	}
	return readSidecar(x)
}
func (a *App) metadataJSON(x Item) string     { b, _ := json.Marshal(a.metadata(x)); return string(b) }
func (a *App) indexMetadata(lib string) error { return a.indexMetadataGeneration(lib, "") }
func (a *App) indexMetadataGeneration(lib, generation string, jobs ...string) error {
	job := ""
	if len(jobs) > 0 {
		job = jobs[0]
	}
	// Page the catalog and close rows before writing, keeping memory and locks bounded.
	after := ""
	for {
		rows, e := a.db.Query("SELECT "+cols+" FROM items WHERE id>? AND (?='' OR lib=?) AND (?='' OR seen=?) ORDER BY id LIMIT 100", after, lib, lib, generation, generation)
		if e != nil {
			return e
		}
		batch := []Item{}
		for rows.Next() {
			x, e := readItem(rows)
			if e != nil {
				rows.Close()
				return e
			}
			batch = append(batch, x)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if len(batch) == 0 {
			return nil
		}
		lastID := batch[len(batch)-1].ID
		// Sort batch by kind so Series is parsed and matches TMDB first, enabling Seasons and Episodes to inherit its tmdbID!
		sort.SliceStable(batch, func(i, j int) bool {
			rank := func(k string) int {
				switch k {
				case "Series":
					return 1
				case "Season":
					return 2
				case "Movie":
					return 3
				default:
					return 4
				}
			}
			return rank(batch[i].Kind) < rank(batch[j].Kind)
		})
		// Parse files before taking the SQLite writer lock.
		metas := make([]sidecar, len(batch))
		for j, x := range batch {
			a.waitScan(job)
			metas[j] = a.resolveItemSidecar(x, false)
			if x.Kind == "Series" && metas[j].TMDB != "" {
				_, _ = a.db.Exec("INSERT INTO tmdb_item_matches(item,tmdb_id,media_type,updated) VALUES(?,?,?,?) ON CONFLICT(item) DO UPDATE SET tmdb_id=excluded.tmdb_id,media_type=excluded.media_type,updated=excluded.updated", x.ID, metas[j].TMDB, "tv", time.Now().Unix())
			}
		}
		tx, e := a.db.Begin()
		if e != nil {
			return e
		}
		for j, x := range batch {
			n := metas[j]
			b, _ := json.Marshal(n)
			var previous string
			if tx.QueryRow("SELECT data FROM item_metadata WHERE item=?", x.ID).Scan(&previous) == nil && previous == string(b) {
				continue
			}
			if _, e = tx.Exec("INSERT INTO item_metadata VALUES(?,?) ON CONFLICT(item) DO UPDATE SET data=excluded.data", x.ID, string(b)); e != nil {
				break
			}
			if n.Title != "" || n.Plot != "" || n.Year > 0 || n.ThumbURL != "" || n.PosterURL != "" {
				newName := x.Name
				if n.Title != "" && x.Kind != "Season" {
					var manual string
					if tx.QueryRow("SELECT name FROM media_display_names WHERE item=?", x.ID).Scan(&manual) != nil {
						newName = n.Title
					}
				}
				newOverview := x.Overview
				if n.Plot != "" {
					newOverview = n.Plot
				}
				newYear := x.Year
				if n.Year > 0 {
					newYear = n.Year
				}
				newPoster := x.Poster
				if n.ThumbURL != "" {
					newPoster = n.ThumbURL
				} else if n.PosterURL != "" && newPoster == "" {
					newPoster = n.PosterURL
				}
				newSeason := x.Season
				newEpisode := x.Episode
				if x.Kind == "Episode" && (newSeason <= 0 || newEpisode <= 0) {
					if sNum, eNum, ok := tmdb.ParseSeasonEpisode(x.Name, 1); ok {
						if newSeason <= 0 {
							newSeason = sNum
						}
						if newEpisode <= 0 {
							newEpisode = eNum
						}
					} else if sNum, eNum, ok := tmdb.ParseSeasonEpisode(filepath.Base(x.Path), 1); ok {
						if newSeason <= 0 {
							newSeason = sNum
						}
						if newEpisode <= 0 {
							newEpisode = eNum
						}
					}
				}
				if _, e = tx.Exec("UPDATE items SET name=?,overview=?,year=?,poster=?,season=?,episode=? WHERE id=?", newName, newOverview, newYear, newPoster, newSeason, newEpisode, x.ID); e != nil {
					break
				}
			}
			if _, e = tx.Exec("DELETE FROM item_people WHERE item=?", x.ID); e != nil {
				break
			}
			for _, p := range n.Actors {
				if p.Name == "" {
					continue
				}
				_, e = tx.Exec("INSERT INTO item_people VALUES(?,?,?,?,?,?) ON CONFLICT(item,person,type) DO UPDATE SET name=excluded.name,role=excluded.role,thumb=excluded.thumb", x.ID, personID(p.Name), p.Name, p.Role, "Actor", actorImage(x, p.Name, p.Thumb))
				if e != nil {
					break
				}
			}
			if e != nil {
				break
			}
			for _, name := range n.Directors {
				_, e = tx.Exec("INSERT INTO item_people VALUES(?,?,?,?,?,?) ON CONFLICT DO NOTHING", x.ID, personID(name), name, "", "Director", "")
				if e != nil {
					break
				}
			}
			if e != nil {
				break
			}
		}
		if e != nil {
			tx.Rollback()
			return e
		}
		if e = tx.Commit(); e != nil {
			return e
		}
		for j, x := range batch {
			a.maybePersistArtifactsToDisk(x, metas[j])
		}
		after = lastID
	}
}
func safeImage(p string) string {
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	real, e := filepath.EvalSymlinks(p)
	if e != nil || !allowedMediaPath(real) {
		return ""
	}
	st, e := os.Stat(real)
	if e != nil || !st.Mode().IsRegular() || st.Size() > 20<<20 {
		return ""
	}
	switch strings.ToLower(filepath.Ext(real)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return real
	}
	return ""
}
func actorImage(x Item, name, thumb string) string {
	dir := filepath.Dir(x.Path)
	if x.Kind == "Series" || x.Kind == "Season" {
		dir = x.Path
	}
	for _, p := range []string{filepath.Join(dir, ".actors", strings.ReplaceAll(name, " ", "_")+".jpg"), filepath.Join(dir, thumb)} {
		if p = safeImage(p); p != "" {
			return p
		}
	}
	u, e := url.Parse(thumb)
	if e == nil && u.Scheme == "https" && u.Host == "image.tmdb.org" && strings.HasPrefix(u.Path, "/t/p/") {
		return u.String()
	}
	return ""
}
func (a *App) imagePath(i, kind string) string {
	if a.db == nil {
		return ""
	}
	if kind == "Primary" {
		if exists, err := a.coverStore().Exists(i); err == nil && exists {
			return "cover"
		}
		if strings.HasPrefix(i, "person-") {
			p, _ := a.mediaItems().PersonImageThumb(i)
			failed, _ := a.mediaItems().PersonImageFailed(i, p, time.Now().Unix())
			if failed {
				return ""
			}
			if strings.HasPrefix(p, "https://image.tmdb.org/") {
				return p
			}
			return safeImage(p)
		}
	}
	x, e := a.item(i)
	if e != nil {
		if p := a.libraryPhysicalImage(i, kind); p != "" {
			return p
		}
		if kind == "Primary" || kind == "Backdrop" {
			x, e = a.mediaItems().LibraryImageCandidate(i, kind)
		}
	}
	if e != nil {
		return ""
	}
	// 1. Prioritize local physical disk files: Zero-network overhead, instant SSD local response!
	dir := filepath.Dir(x.Path)
	if x.Kind == "Series" || x.Kind == "Season" {
		dir = x.Path
	}
	base := strings.TrimSuffix(x.Path, filepath.Ext(x.Path))
	if x.Kind == "Episode" && (kind == "Primary" || kind == "Thumb") {
		if p := episodeThumb(x); p != "" {
			return p
		}
	}
	names := map[string][]string{
		"Primary":  {"poster", "folder", "cover"},
		"Backdrop": {"backdrop", "fanart"},
		"Thumb":    {"thumb", "landscape"},
		"Logo":     {"logo", "clearlogo"},
		"Banner":   {"banner"},
	}[kind]
	for _, n := range names {
		for _, ext := range []string{".jpg", ".png", ".webp", ".jpeg"} {
			for _, p := range []string{base + "-" + n + ext, filepath.Join(dir, n+ext)} {
				if p = safeImage(p); p != "" {
					return p
				}
			}
		}
	}
	if kind == "Primary" {
		if x.Kind == "Series" {
			if p := safeImage(seriesFallbackPoster(x.Path)); p != "" {
				return p
			}
		}
		if x.Kind == "Season" {
			if p := safeImage(seasonPoster(x.Path, x.Season)); p != "" {
				return p
			}
		}
	}

	// 2. Fallback to cached or database-persisted poster URL / sidecar metadata
	if x.Poster != "" && (kind == "Primary" || (x.Kind == "Episode" && kind == "Thumb")) {
		if p := safeImage(x.Poster); p != "" {
			return p
		}
	}
	meta := a.metadata(x)
	switch kind {
	case "Primary":
		if strings.HasPrefix(meta.PosterURL, "https://image.tmdb.org/") {
			return meta.PosterURL
		}
	case "Backdrop":
		if strings.HasPrefix(meta.BackdropURL, "https://image.tmdb.org/") {
			return meta.BackdropURL
		}
	case "Thumb":
		if strings.HasPrefix(meta.ThumbURL, "https://image.tmdb.org/") {
			return meta.ThumbURL
		}
	}
	if kind == "Primary" && x.Parent != x.Lib && x.Parent != x.ID {
		if parent, e := a.item(x.Parent); e == nil {
			return a.imagePath(parent.ID, kind)
		}
	}
	return ""
}
func (a *App) imageTag(i, kind, p string) string {
	secret := a.cursorSecret
	if secret == "" {
		a.db.QueryRow("SELECT v FROM settings WHERE k='image_secret'").Scan(&secret)
	}
	revision := p
	if p == "cover" {
		cover, _ := a.coverStore().Get(i)
		revision = digest(string(cover.Data))
	} else if st, e := os.Stat(p); e == nil {
		revision += fmt.Sprint(st.ModTime().UnixNano(), st.Size())
	}
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(i + "|" + kind + "|" + revision))
	return hex.EncodeToString(h.Sum(nil))[:32]
}
func (a *App) decorateImages(x Item, m M) {
	tags := M{}
	back := []string{}
	for _, kind := range []string{"Primary", "Backdrop", "Thumb", "Logo", "Banner"} {
		if p := a.imagePath(x.ID, kind); p != "" {
			tag := a.imageTag(x.ID, kind, p)
			if kind == "Backdrop" {
				back = append(back, tag)
			} else {
				tags[kind] = tag
			}
		}
	}
	if tags["Primary"] == nil {
		tags["Primary"] = a.generatedTag(x.ID, "Primary")
	}
	m["ImageTags"] = tags
	m["BackdropImageTags"] = back
	m["PrimaryImageAspectRatio"] = 2.0 / 3
	if x.Kind == "Episode" {
		m["PrimaryImageAspectRatio"] = 16.0 / 9
	}
	m["DateCreated"] = time.Unix(0, x.Mtime).UTC().Format(time.RFC3339)
	if x.Kind == "Series" || x.Kind == "Season" {
		metadata, err := a.mediaItems().BrowseMetadata([]Item{x})
		if err != nil {
			log.Printf("load detail browse metadata for %s: %v", x.ID, err)
		}
		value := metadata[x.ID]
		m["ChildCount"] = value.ChildCount
		if x.Kind == "Series" {
			m["RecursiveItemCount"] = value.EpisodeCount
			m["EpisodeCount"] = value.EpisodeCount
		}
	}
	if x.Kind == "Season" {
		m["IndexNumber"] = x.Season
		if p, e := a.item(x.Parent); e == nil {
			m["SeriesId"] = p.ID
			m["SeriesName"] = p.Name
		}
	}
}
func imageRequest(p string) (string, string, bool) {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 4 || !strings.EqualFold(parts[0], "items") || !strings.EqualFold(parts[2], "images") {
		return "", "", false
	}
	kind := ""
	for _, k := range []string{"Primary", "Backdrop", "Thumb", "Logo", "Banner"} {
		if strings.EqualFold(parts[3], k) {
			kind = k
		}
	}
	return parts[1], kind, kind != ""
}
func (a *App) taggedImage(w http.ResponseWriter, r *http.Request, p string) bool {
	i, k, ok := imageRequest(p)
	if !ok || (r.Method != "GET" && r.Method != "HEAD") {
		return false
	}
	tag := q(r, "tag")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if tag == "" && len(parts) > 5 {
		tag = parts[5]
	}
	if tag == "" {
		return false
	}
	if k == "Primary" {
		if x, e := a.item(i); e == nil && x.Poster != "" && hmac.Equal([]byte(tag), []byte(a.listImageTag(x))) {
			a.serveImage(w, r, i)
			return true
		}
	}
	path := a.imagePath(i, k)
	if path == "" {
		if k == "Primary" && hmac.Equal([]byte(tag), []byte(a.generatedTag(i, k))) {
			a.serveImage(w, r, i)
			return true
		}
		return false
	}
	if !hmac.Equal([]byte(tag), []byte(a.imageTag(i, k, path))) {
		return false
	}
	a.serveImage(w, r, i)
	return true
}

var posterHTTP = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
	if len(via) > 2 || r.URL.Scheme != "https" || r.URL.Host != "image.tmdb.org" {
		return fmt.Errorf("image redirect rejected")
	}
	return nil
}}

func (a *App) serveImage(w http.ResponseWriter, r *http.Request, i string) {
	if r.Method != "GET" && r.Method != "HEAD" {
		fail(w, 405, "GET or HEAD required")
		return
	}
	p := strings.TrimPrefix(strings.ToLower(r.URL.Path), "/emby")
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 3 {
		out := []M{}
		for _, k := range []string{"Primary", "Backdrop", "Thumb", "Logo", "Banner"} {
			if path := a.imagePath(i, k); path != "" {
				out = append(out, M{"ImageType": k, "ImageIndex": 0, "ImageTag": a.imageTag(i, k, path)})
			}
		}
		respond(w, out)
		return
	}
	_, kind, ok := imageRequest(p)
	if !ok {
		fail(w, 404, "图片不存在")
		return
	}
	if len(parts) > 4 && parts[4] != "0" {
		fail(w, 404, "图片索引不存在")
		return
	}
	path := a.imagePath(i, kind)
	if path == "" {
		if kind != "Primary" {
			fail(w, 404, "图片不存在")
			return
		}
		var isLib bool
		var libName, libKind string
		if library, err := a.libraryStore().GetByID(i); err == nil {
			libName, libKind = library.Name, library.Kind
			isLib = true
		} else {
			if exists, err := a.mediaItems().Exists(i); err != nil || !exists {
				fail(w, 404, "图片不存在")
				return
			}
		}
		genTag := a.generatedTag(i, kind)
		w.Header().Set("ETag", `"`+genTag+`"`)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Header.Get("If-None-Match") == `"`+genTag+`"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		if r.Method != "HEAD" {
			var rawBytes []byte
			if isLib {
				if cached, ok := a.getCachedCollage(i); ok {
					rawBytes = cached
				} else {
					img := a.generateLibraryCollage(i, libName, libKind)
					var buf bytes.Buffer
					png.Encode(&buf, img)
					rawBytes = buf.Bytes()
					a.setCachedCollage(i, rawBytes)
				}
			} else {
				it, _ := a.item(i)
				img := generatePlaceholderPoster(i, it.Name, it.Kind, it.Year)
				var buf bytes.Buffer
				png.Encode(&buf, img)
				rawBytes = buf.Bytes()
			}
			if serveThumbnail(w, r, bytes.NewReader(rawBytes)) {
				return
			}
			w.Write(rawBytes)
		}
		return
	}
	tag := a.imageTag(i, kind, path)
	w.Header().Set("ETag", `"`+tag+`"`)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.Header.Get("If-None-Match") == `"`+tag+`"` {
		w.WriteHeader(304)
		return
	}
	if path == "cover" {
		cover, err := a.coverStore().Get(i)
		if err != nil {
			fail(w, 404, "图片不存在")
			return
		}
		w.Header().Set("Content-Type", cover.MIME)
		if serveThumbnail(w, r, bytes.NewReader(cover.Data)) {
			return
		}
		http.ServeContent(w, r, "cover", time.Time{}, bytes.NewReader(cover.Data))
		return
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		req, e := http.NewRequestWithContext(r.Context(), "GET", path, nil)
		if e != nil {
			fail(w, 502, "图片地址无效")
			return
		}
		cli := posterHTTP
		if tmdbCli := a.TMDBClient(); tmdbCli != nil && tmdbCli.HTTPClient() != nil {
			cli = tmdbCli.HTTPClient()
		}
		res, e := cli.Do(req)
		if e != nil {
			a.markActorImageFailure(i, path)
			fail(w, 502, "图片暂不可用")
			return
		}
		defer res.Body.Close()
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "image/") {
			a.markActorImageFailure(i, path)
			fail(w, 502, "图片暂不可用")
			return
		}
		b, e := io.ReadAll(io.LimitReader(res.Body, 5<<20+1))
		if e != nil || len(b) > 5<<20 {
			fail(w, 502, "图片过大")
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(b))
		if serveThumbnail(w, r, bytes.NewReader(b)) {
			return
		}
		http.ServeContent(w, r, "actor", time.Time{}, bytes.NewReader(b))
		return
	}
	if path = safeImage(path); path == "" {
		fail(w, 404, "图片不存在")
		return
	}
	if f, e := os.Open(path); e == nil {
		defer f.Close()
		if serveThumbnail(w, r, f) {
			return
		}
	}
	http.ServeFile(w, r, path)
}
func (a *App) rootDTO() M {
	return M{"Id": "root", "Name": "媒体库", "Type": "UserRootFolder", "IsFolder": true, "DisplayPreferencesId": "root", "SortName": "媒体库", "ExternalUrls": []M{}, "ProviderIds": M{}, "Taglines": []string{}, "RemoteTrailers": []M{}, "ServerId": a.serverID, "ChildCount": len(a.libraries()), "ImageTags": M{}, "BackdropImageTags": []string{}, "UserData": makeUserItemDataDTO("root", 0, false, false)}
}
func setQuery(r *http.Request, k, v string) {
	qv := r.URL.Query()
	for key := range qv {
		if strings.EqualFold(key, k) {
			qv.Del(key)
		}
	}
	qv.Set(k, v)
	r.URL.RawQuery = qv.Encode()
}
func (a *App) browseRoute(w http.ResponseWriter, r *http.Request, u User, p string) bool {
	if a.handleUserActionRoutes(w, r, u, p) {
		return true
	}
	l := strings.ToLower(p)
	if l == "/enhancements" && r.Method == "GET" {
		respond(w, a.enhancementValues())
		return true
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	// Keep per-user authorization checks consistent with the original dispatcher.
	if len(parts) >= 2 && strings.EqualFold(parts[0], "users") && !u.Admin && !u.API && parts[1] != u.ID && parts[1] != "me" {
		return false
	}
	if l == "/library/mediafolders" {
		respond(w, M{"Items": a.libraryDTOs(), "TotalRecordCount": len(a.libraries()), "StartIndex": 0})
		return true
	}
	if len(parts) == 4 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "items") && strings.EqualFold(parts[3], "root") {
		respond(w, a.rootDTO())
		return true
	}
	if len(parts) == 3 && strings.EqualFold(parts[0], "users") && strings.EqualFold(parts[2], "groupingoptions") {
		respond(w, []M{})
		return true
	}
	if l == "/sessions/capabilities" || l == "/sessions/capabilities/full" {
		w.WriteHeader(204)
		return true
	}
	if strings.HasPrefix(l, "/displaypreferences/") {
		if len(parts) < 2 {
			fail(w, 400, "缺少 DisplayPreferences id")
			return true
		}
		if r.Method != "GET" && r.Method != "POST" {
			fail(w, 405, "GET or POST required")
			return true
		}
		client := field(r, "Client")
		if client == "" {
			client = r.Header.Get("X-Emby-Client")
		}
		key := "dispref:" + u.ID + ":" + client + ":" + parts[1]
		prefs := M{"Id": parts[1], "SortBy": "SortName", "SortOrder": "Ascending", "IndexBy": "None", "ViewType": "Poster", "CustomPrefs": M{}, "RememberIndexing": false, "RememberSorting": true}
		type disprefBody struct {
			SortBy           string `json:"SortBy"`
			SortOrder        string `json:"SortOrder"`
			IndexBy          string `json:"IndexBy"`
			ViewType         string `json:"ViewType"`
			RememberIndexing bool   `json:"RememberIndexing"`
			RememberSorting  bool   `json:"RememberSorting"`
			CustomPrefs      M      `json:"CustomPrefs"`
		}
		if r.Method == "POST" {
			var body disprefBody
			b, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
			if e := json.Unmarshal(b, &body); e == nil {
				if body.SortBy == "" {
					body.SortBy = "SortName"
				}
				if body.SortOrder == "" {
					body.SortOrder = "Ascending"
				}
				if body.ViewType == "" {
					body.ViewType = "Poster"
				}
				if body.CustomPrefs == nil {
					body.CustomPrefs = M{}
				}
				if v, e := json.Marshal(body); e == nil {
					a.db.Exec("INSERT INTO settings(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", key, string(v))
				}
			}
			w.WriteHeader(http.StatusNoContent)
			return true
		}
		var stored string
		if a.db.QueryRow("SELECT v FROM settings WHERE k=?", key).Scan(&stored) == nil {
			var body disprefBody
			if e := json.Unmarshal([]byte(stored), &body); e == nil {
				if body.SortBy != "" {
					prefs["SortBy"] = body.SortBy
				}
				if body.SortOrder != "" {
					prefs["SortOrder"] = body.SortOrder
				}
				if body.IndexBy != "" {
					prefs["IndexBy"] = body.IndexBy
				}
				if body.ViewType != "" {
					prefs["ViewType"] = body.ViewType
				}
				if body.RememberIndexing {
					prefs["RememberIndexing"] = true
				}
				if body.RememberSorting {
					prefs["RememberSorting"] = true
				}
				if body.CustomPrefs != nil {
					prefs["CustomPrefs"] = body.CustomPrefs
				}
			}
		}
		respond(w, prefs)
		return true
	}
	if l == "/items/latest" {
		a.items(w, r, u, true)
		return true
	}
	if l == "/shows/nextup" {
		a.nextUp(w, r, u)
		return true
	}
	if len(parts) == 3 && strings.EqualFold(parts[0], "shows") && (strings.EqualFold(parts[2], "seasons") || strings.EqualFold(parts[2], "episodes")) {
		setQuery(r, "ParentId", parts[1])
		if strings.EqualFold(parts[2], "seasons") {
			setQuery(r, "IncludeItemTypes", "Season")
		} else {
			setQuery(r, "IncludeItemTypes", "Episode")
			setQuery(r, "Recursive", "true")
			if s := q(r, "SeasonId"); s != "" {
				setQuery(r, "ParentId", s)
			}
		}
		setQuery(r, "SortBy", "ParentIndexNumber,IndexNumber")
		a.items(w, r, u, false)
		return true
	}
	if l == "/episodes" {
		sid := q(r, "SeriesId")
		if sid == "" {
			respond(w, M{"Items": []M{}, "TotalRecordCount": 0, "StartIndex": 0})
			return true
		}
		setQuery(r, "ParentId", sid)
		setQuery(r, "IncludeItemTypes", "Episode")
		setQuery(r, "Recursive", "true")
		if s := q(r, "SeasonId"); s != "" {
			setQuery(r, "ParentId", s)
		}
		setQuery(r, "SortBy", "ParentIndexNumber,IndexNumber")
		a.items(w, r, u, false)
		return true
	}
	if l == "/persons" || strings.HasSuffix(l, "/persons") {
		a.persons(w, r, u)
		return true
	}
	if len(parts) >= 2 && strings.EqualFold(parts[0], "persons") {
		i := parts[1]
		if !strings.HasPrefix(i, "person-") {
			i = personID(i)
		}
		if len(parts) >= 4 && strings.EqualFold(parts[2], "images") {
			clone := r.Clone(r.Context())
			v := *r.URL
			clone.URL = &v
			clone.URL.Path = "/Items/" + i + "/Images/" + parts[3]
			a.serveImage(w, clone, i)
		} else {
			a.personDetail(w, r, u, i)
		}
		return true
	}
	if l == "/genres" || strings.HasSuffix(l, "/genres") || l == "/items/filters" || l == "/items/filters2" {
		a.genres(w, r, l == "/genres" || strings.HasSuffix(l, "/genres"))
		return true
	}
	if l == "/studios" || strings.HasSuffix(l, "/studios") || l == "/tags" || strings.HasSuffix(l, "/tags") {
		respond(w, M{"Items": []M{}, "TotalRecordCount": 0, "StartIndex": 0})
		return true
	}
	if l == "/movies/recommendations" {
		seed, err := a.userStore().ResumeSeed(u.ID, q(r, "ParentId"))
		if err != nil {
			respond(w, []M{})
			return true
		}
		rr := r.Clone(r.Context())
		v := *r.URL
		rr.URL = &v
		if n := q(r, "ItemLimit"); n != "" {
			setQuery(rr, "Limit", n)
		}
		rec := &jsonCapture{header: http.Header{}}
		a.similar(rec, rr, u, seed.ID)
		var result struct{ Items []M }
		if json.Unmarshal(rec.buf.Bytes(), &result) != nil || result.Items == nil {
			fail(w, 500, "推荐查询失败")
			return true
		}
		respond(w, []M{{"BaselineItemName": seed.Name, "CategoryId": 1, "RecommendationType": "SimilarToRecentlyPlayed", "Items": result.Items}})
		return true
	}
	if strings.HasPrefix(l, "/searchhints") {
		term := q(r, "SearchTerm")
		if term == "" {
			term = q(r, "query")
		}
		if term == "" {
			respond(w, M{"SearchHints": []M{}, "TotalRecordCount": 0})
			return true
		}
		limit := 20
		if n, err := strconv.Atoi(q(r, "Limit")); err == nil && n > 0 {
			limit = n
		}
		if limit > 50 {
			limit = 50
		}
		setQuery(r, "SearchTerm", term)
		setQuery(r, "Limit", strconv.Itoa(limit))
		rec := &jsonCapture{header: http.Header{}}
		a.items(rec, r, u, false)
		var result struct {
			Items            []M
			TotalRecordCount int
		}
		if json.Unmarshal(rec.buf.Bytes(), &result) != nil {
			fail(w, 500, "搜索提示查询失败")
			return true
		}
		hints := []M{}
		for _, item := range result.Items {
			mediaType := ""
			if isFolder, ok := item["IsFolder"].(bool); ok && !isFolder {
				mediaType = "Video"
			}
			hints = append(hints, M{
				"Id":          item["Id"],
				"ItemId":      item["Id"],
				"Name":        item["Name"],
				"MatchedTerm": term,
				"Type":        item["Type"],
				"MediaType":   mediaType,
			})
		}
		respond(w, M{"SearchHints": hints, "TotalRecordCount": result.TotalRecordCount})
		return true
	}
	if l == "/searchsuggestions" {
		if q(r, "SearchTerm") == "" {
			if v := q(r, "query"); v != "" {
				setQuery(r, "SearchTerm", v)
			}
		}
		a.items(w, r, u, false)
		return true
	}
	if l == "/years" || strings.HasSuffix(l, "/years") {
		parent := q(r, "ParentId")
		years, e := a.mediaItems().YearsInLibrary(parent)
		if e != nil {
			fail(w, 500, "年份查询失败")
			return true
		}
		out := make([]M, 0, len(years))
		for _, year := range years {
			out = append(out, M{"Name": strconv.Itoa(year), "Id": strconv.Itoa(year)})
		}
		respond(w, M{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0})
		return true
	}
	if len(parts) >= 3 && strings.EqualFold(parts[0], "genres") && strings.EqualFold(parts[2], "items") {
		name, err := url.PathUnescape(parts[1])
		if err != nil {
			name = parts[1]
		}
		setQuery(r, "Genres", name)
		a.items(w, r, u, false)
		return true
	}

	return false
}

type jsonCapture struct {
	header http.Header
	buf    bytes.Buffer
}

func (c *jsonCapture) Header() http.Header         { return c.header }
func (c *jsonCapture) Write(b []byte) (int, error) { return c.buf.Write(b) }
func (c *jsonCapture) WriteHeader(int)             {}
func (a *App) personDTO(i, name, uid string) M {
	var isFav bool
	if a.db != nil && uid != "" {
		favorite, err := a.userStore().IsFavorite(uid, i)
		if err != nil {
			log.Printf("read person favorite: %v", err)
		} else {
			isFav = favorite
		}
	}
	m := M{"Id": i, "Name": name, "Type": "Person", "ServerId": a.serverID, "IsFolder": false, "ImageTags": a.libraryImageTags(i), "BackdropImageTags": []string{}, "Overview": "", "PrimaryImageAspectRatio": 2.0 / 3, "UserData": makeUserItemDataDTO(i, 0, false, isFav)}
	n, err := a.mediaItems().PersonItemCount(i)
	if err != nil {
		log.Printf("count person media items: %v", err)
	}
	m["MovieCount"] = n
	return m
}
func (a *App) personDetail(w http.ResponseWriter, r *http.Request, u User, i string) {
	name, err := a.mediaItems().PersonName(i)
	if err != nil {
		fail(w, 404, "演员不存在")
		return
	}
	respond(w, a.personDTO(i, name, u.ID))
}
func (a *App) persons(w http.ResponseWriter, r *http.Request, u User) {
	where := "1=1"
	args := []any{}
	requireAvailableImage := a.hideMissingActors()
	imageCheckedAt := int64(0)
	if requireAvailableImage {
		imageCheckedAt = time.Now().Unix()
	}
	filters := strings.ToLower(q(r, "Filters"))
	if strings.EqualFold(q(r, "IsFavorite"), "true") || strings.Contains(filters, "isfavorite") {
		predicate := a.userStore().FavoritePredicate(u.ID, FavoritePerson, false)
		where += " AND " + predicate.SQL
		args = append(args, predicate.Args...)
	}
	if strings.EqualFold(q(r, "IsFavorite"), "false") {
		predicate := a.userStore().FavoritePredicate(u.ID, FavoritePerson, true)
		where += " AND " + predicate.SQL
		args = append(args, predicate.Args...)
	}
	if s := q(r, "SearchTerm"); s != "" {
		where += " AND name ILIKE ?"
		args = append(args, "%"+s+"%")
	}
	if p := q(r, "PersonIds"); p != "" {
		where += " AND person=?"
		args = append(args, p)
	}
	limit, _ := strconv.Atoi(q(r, "Limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	signatureArgs := append([]any{}, args...)
	if requireAvailableImage {
		signatureArgs = append(signatureArgs, imageCheckedAt)
	}
	key := a.pageSignature(r, where, "persons", signatureArgs)
	p, e := a.pageStart(r, key)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	personPage := PersonPageQuery{
		Where: where, Args: args, Position: p.Position, Limit: limit,
		RequireAvailableImage: requireAvailableImage, ImageCheckedAt: imageCheckedAt,
	}
	if p.Count < 0 && !strings.EqualFold(q(r, "EnableTotalRecordCount"), "false") {
		if p.Count, e = a.mediaItems().CountPeople(personPage); e != nil {
			fail(w, 500, "演员查询失败: "+e.Error())
			return
		}
	}
	if len(p.Values) > 0 {
		if len(p.Values) != 2 {
			fail(w, 400, "invalid cursor")
			return
		}
		personPage.AfterName = p.Values[0]
		personPage.AfterID = p.Values[1]
	}
	people, e := a.mediaItems().ListPeople(personPage)
	if e != nil {
		fail(w, 500, "演员查询失败")
		return
	}
	pairs := make([][2]string, 0, len(people))
	for _, person := range people {
		pairs = append(pairs, [2]string{person.ID, person.Name})
	}
	more := len(pairs) > limit
	if more {
		pairs = pairs[:limit]
	}
	next := ""
	if more {
		n := p
		n.Position += len(pairs)
		last := pairs[len(pairs)-1]
		n.Values = []string{last[1], last[0]}
		next = a.savePage(n)
	}
	out := []M{}
	personIDs := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		personIDs = append(personIDs, pair[0])
	}
	favorites, favoriteErr := a.userStore().ListFavorites(u.ID, personIDs)
	if favoriteErr != nil {
		log.Printf("list person favorites: %v", favoriteErr)
	}
	for _, pair := range pairs {
		out = append(out, M{
			"Id":        pair[0],
			"Name":      pair[1],
			"Type":      "Person",
			"ImageTags": a.libraryImageTags(pair[0]),
			"IsFolder":  false,
			"UserData":  makeUserItemDataDTO(pair[0], 0, false, favorites[pair[0]]),
		})
	}
	if p.Count < 0 {
		p.Count = p.Position + len(out)
		if more {
			p.Count++
		}
	}
	respond(w, M{"Items": out, "TotalRecordCount": p.Count, "StartIndex": p.Position, "NextCursor": next, "HasMore": more})

}
func (a *App) genres(w http.ResponseWriter, r *http.Request, list bool) {
	names, err := a.mediaItems().GenreNamesInLibrary(q(r, "ParentId"))
	if err != nil {
		fail(w, 500, "类型查询失败")
		return
	}
	out := []M{}
	for _, name := range names {
		out = append(out, M{"Id": "genre-" + digest(name)[:32], "Name": name, "Type": "Genre", "ImageTags": M{}})
	}
	if list {
		respond(w, M{"Items": out, "TotalRecordCount": len(out), "StartIndex": 0})
	} else {
		if strings.HasSuffix(strings.ToLower(r.URL.Path), "filters2") {
			pairs := []M{}
			for _, v := range out {
				pairs = append(pairs, M{"Name": v["Name"], "Id": v["Id"]})
			}
			respond(w, M{"Genres": pairs, "Tags": []string{}})
		} else {
			respond(w, M{"Genres": names, "Tags": []string{}, "Years": []int{}, "OfficialRatings": []string{}})
		}
	}
}
func (a *App) browseFilters(r *http.Request, u User, where string, args []any, latest bool) (string, []any) {
	filters := strings.ToLower(q(r, "Filters"))
	if strings.EqualFold(q(r, "IsPlayed"), "true") || strings.Contains(filters, "isplayed") {
		predicate := a.userStore().PlayedItemPredicate(u.ID, false)
		where += " AND " + predicate.SQL
		args = append(args, predicate.Args...)
	}
	if strings.EqualFold(q(r, "IsPlayed"), "false") || strings.Contains(filters, "isunplayed") {
		predicate := a.userStore().PlayedItemPredicate(u.ID, true)
		where += " AND " + predicate.SQL
		args = append(args, predicate.Args...)
	}
	if strings.EqualFold(q(r, "IsFavorite"), "true") || strings.Contains(filters, "isfavorite") {
		predicate := a.userStore().FavoritePredicate(u.ID, FavoriteMediaItem, false)
		where += " AND " + predicate.SQL
		args = append(args, predicate.Args...)
	}
	if strings.EqualFold(q(r, "IsFavorite"), "false") {
		predicate := a.userStore().FavoritePredicate(u.ID, FavoriteMediaItem, true)
		where += " AND " + predicate.SQL
		args = append(args, predicate.Args...)
	}
	if strings.Contains(strings.ToLower(r.URL.Path), "/shows/nextup") {
		// Only series with playback history can produce a NextUp item.
		predicate := a.userStore().NextUpPredicate(u.ID)
		where += " AND " + predicate.SQL
		args = append(args, predicate.Args...)
	}
	if latest && q(r, "IncludeItemTypes") == "" {
		where += " AND kind IN ('Movie','Series')"
	}
	if v := q(r, "IsFolder"); v != "" {
		if strings.EqualFold(v, "true") {
			where += " AND kind IN ('Series','Season')"
		} else {
			where += " AND kind NOT IN ('Series','Season')"
		}
	}
	for _, pair := range [][2]string{{"PersonIds", "person"}, {"Person", "name"}} {
		if v := q(r, pair[0]); v != "" {
			values := strings.Split(v, ",")
			if len(values) > 100 {
				values = values[:100]
			}
			where += " AND id IN (SELECT item FROM item_people WHERE " + pair[1] + " IN (" + strings.TrimRight(strings.Repeat("?,", len(values)), ",") + "))"
			for _, s := range values {
				args = append(args, s)
			}
		}
	}
	if v := q(r, "GenreIds"); v != "" {
		names, err := a.mediaItems().GenreNamesByIDs(strings.Split(v, ","))
		if err != nil {
			log.Printf("genre id filter lookup: %v", err)
		}
		if len(names) == 0 {
			where += " AND 0=1"
		} else {
			setQuery(r, "Genres", strings.Join(names, "|"))
		}
	}
	if v := q(r, "Genres"); v != "" {
		values := strings.FieldsFunc(v, func(c rune) bool { return c == '|' || c == ',' })
		if len(values) > 0 {
			where += " AND id IN (SELECT item FROM item_genres WHERE genre IN (" + strings.TrimRight(strings.Repeat("?,", len(values)), ",") + "))"
			for _, s := range values {
				args = append(args, s)
			}
		}
	}
	if v := q(r, "Season"); v != "" {
		if n, e := strconv.Atoi(v); e == nil {
			where += " AND season=?"
			args = append(args, n)
		}
	}
	if v := q(r, "Years"); v != "" {
		values := strings.Split(v, ",")
		where += " AND year IN (" + strings.TrimRight(strings.Repeat("?,", len(values)), ",") + ")"
		for _, s := range values {
			args = append(args, s)
		}
	}
	return where, args
}
func itemSortName(x Item) string {
	switch x.Kind {
	case "Episode":
		if x.Episode > 0 {
			if x.Season > 0 {
				return fmt.Sprintf("%04d - %04d - %s", x.Season, x.Episode, x.Name)
			}
			return fmt.Sprintf("%04d - %s", x.Episode, x.Name)
		}
	case "Season":
		if x.Season > 0 {
			return fmt.Sprintf("%04d - %s", x.Season, x.Name)
		}
	}
	return x.Name
}

func browseOrder(r *http.Request) string {
	return browseOrderWithContext(r, nil)
}

func browseOrderWithContext(r *http.Request, parentItem *Item) string {
	sortByRaw := strings.TrimSpace(q(r, "SortBy"))
	sortByLower := strings.ToLower(sortByRaw)
	includeTypes := strings.ToLower(q(r, "IncludeItemTypes"))

	isEpisode := strings.Contains(includeTypes, "episode")
	if !isEpisode && parentItem != nil && parentItem.Kind == "Season" {
		isEpisode = true
	}
	if !isEpisode && parentItem != nil && parentItem.Kind == "Series" && strings.EqualFold(q(r, "Recursive"), "true") && !strings.Contains(includeTypes, "season") {
		isEpisode = true
	}

	isSeason := !isEpisode && (strings.Contains(includeTypes, "season") || (parentItem != nil && parentItem.Kind == "Series" && !strings.EqualFold(q(r, "Recursive"), "true") && includeTypes == ""))

	dir := " ASC"
	if strings.EqualFold(q(r, "SortOrder"), "Descending") {
		dir = " DESC"
	}

	if sortByRaw == "" || sortByLower == "sortname" || sortByLower == "name" || sortByLower == "default" {
		if isEpisode {
			return strings.Join([]string{"season" + dir, "episode" + dir, "name" + dir, "id" + dir}, ",")
		}
		if isSeason {
			return strings.Join([]string{"season" + dir, "name" + dir, "id" + dir}, ",")
		}
	}

	out := []string{}
	for _, key := range strings.Split(sortByLower, ",") {
		if col := map[string]string{"sortname": "name", "name": "name", "seriessortname": "name", "productionyear": "year", "datecreated": "mtime", "datelastmediaadded": "mtime", "premieredate": "year", "indexnumber": "episode", "parentindexnumber": "season", "communityrating": "rating", "random": "id"}[key]; col != "" {
			out = append(out, col+dir)
		}
	}
	if len(out) == 0 {
		out = append(out, "name"+dir)
	}
	return strings.Join(append(out, "id"+dir), ",")
}

func (a *App) markActorImageFailure(person, path string) {
	if a.db != nil && strings.HasPrefix(person, "person-") {
		if err := a.mediaItems().RecordPersonImageFailure(person, path, time.Now().Add(time.Hour).Unix()); err != nil {
			log.Printf("record person image failure: %v", err)
		}
	}
}

func episodeThumb(x Item) string {
	base := strings.TrimSuffix(x.Path, filepath.Ext(x.Path))
	for _, suffix := range []string{"-thumb.jpg", ".jpg", ".jpeg", ".png", ".webp"} {
		if p := safeImage(base + suffix); p != "" {
			return p
		}
	}
	return ""
}
