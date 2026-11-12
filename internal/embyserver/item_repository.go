package embyserver

import (
	"database/sql"
	"errors"
)

type Item struct {
	ID, Lib, Parent, Name, Kind, Path, URL, Overview, Poster string
	Year, Season, Episode                                    int
	Mtime, Size                                              int64
}

type ItemPathRecord struct {
	ID   string
	Path string
}

type BrowseItemMetadata struct {
	ChildCount   int
	EpisodeCount int
	SeasonID     string
	SeasonName   string
	SeriesID     string
	SeriesName   string
}

type PersonRecord struct {
	ID   string
	Name string
}

type PersonPageQuery struct {
	Where                 string
	Args                  []any
	AfterName             string
	AfterID               string
	Position              int
	Limit                 int
	RequireAvailableImage bool
	ImageCheckedAt        int64
}

type itemWriter interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type MediaItemRepository interface {
	GetByID(id string) (Item, error)
	Exists(id string) (bool, error)
	LibraryRootForItem(id string) (string, error)
	DeleteItemsUnderLibraryPath(writer itemWriter, libraryID, path string) error
	DeleteItemsByLibrary(writer itemWriter, libraryID string) error
	SetManualName(writer itemWriter, itemID, name string) error
	DeleteItem(writer itemWriter, itemID string) error
	ItemTree(itemID string) ([]Item, error)
	ItemsAtOrUnderPath(path string) ([]ItemPathRecord, error)
	CountByKind(kind string) (int, error)
	CountAll() (int, error)
	CountPlayableByLibrary(libraryID string) (int, error)
	NextEpisode(seriesID string, season, episode int) (Item, error)
	LibraryImageCandidate(libraryID, kind string) (Item, error)
	LibraryCollageCandidates(libraryID string) ([]Item, error)
	YearsInLibrary(libraryID string) ([]int, error)
	GenreNamesByIDs(ids []string) ([]string, error)
	GenreNamesInLibrary(libraryID string) ([]string, error)
	VersionsByGrouping(grouping, itemID string) ([]Item, error)
	SimilarItems(item Item, apiUser bool, grouping string, limit int) ([]Item, error)
	BrowseMetadata(items []Item) (map[string]BrowseItemMetadata, error)
	PersonName(personID string) (string, error)
	PersonItemCount(personID string) (int, error)
	CountPeople(query PersonPageQuery) (int, error)
	ListPeople(query PersonPageQuery) ([]PersonRecord, error)
	PersonImageThumb(personID string) (string, error)
	PersonImageFailed(personID, path string, now int64) (bool, error)
	RecordPersonImageFailure(personID, path string, expires int64) error
}

type sqlMediaItemRepository struct {
	db *Database
}

const cols = "id,lib,parent,name,kind,path,url,overview,poster,year,season,episode,mtime,size"

func readItem(scanner interface{ Scan(...any) error }) (Item, error) {
	var item Item
	err := scanner.Scan(&item.ID, &item.Lib, &item.Parent, &item.Name, &item.Kind, &item.Path, &item.URL, &item.Overview, &item.Poster, &item.Year, &item.Season, &item.Episode, &item.Mtime, &item.Size)
	return item, err
}

func (r sqlMediaItemRepository) GetByID(id string) (Item, error) {
	if r.db == nil || id == "" {
		return Item{}, sql.ErrNoRows
	}
	return readItem(r.db.QueryRow("SELECT "+cols+" FROM items WHERE id=?", id))
}

func (r sqlMediaItemRepository) Exists(id string) (bool, error) {
	if r.db == nil {
		return false, errors.New("media item database is unavailable")
	}
	var exists int
	err := r.db.QueryRow("SELECT 1 FROM items WHERE id=?", id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return exists != 0, err
}

func (r sqlMediaItemRepository) LibraryRootForItem(id string) (string, error) {
	if r.db == nil {
		return "", errors.New("media item database is unavailable")
	}
	var root string
	err := r.db.QueryRow("SELECT l.path FROM libraries l JOIN items i ON i.lib=l.id WHERE i.id=?", id).Scan(&root)
	return root, err
}

func (r sqlMediaItemRepository) DeleteItemsUnderLibraryPath(writer itemWriter, libraryID, path string) error {
	if writer == nil {
		return errors.New("media item writer is unavailable")
	}
	_, err := writer.Exec("DELETE FROM items WHERE lib=? AND (path=? OR substr(path,1,length(?))=?)", libraryID, path, path+"/", path+"/")
	return err
}

func (r sqlMediaItemRepository) DeleteItemsByLibrary(writer itemWriter, libraryID string) error {
	if writer == nil {
		return errors.New("media item writer is unavailable")
	}
	_, err := writer.Exec("DELETE FROM items WHERE lib=?", libraryID)
	return err
}

func (r sqlMediaItemRepository) SetManualName(writer itemWriter, itemID, name string) error {
	if writer == nil {
		return errors.New("media item writer is unavailable")
	}
	if _, err := writer.Exec("INSERT INTO media_display_names(item,name) VALUES(?,?) ON CONFLICT(item) DO UPDATE SET name=excluded.name", itemID, name); err != nil {
		return err
	}
	_, err := writer.Exec("UPDATE items SET name=? WHERE id=?", name, itemID)
	return err
}

func (r sqlMediaItemRepository) DeleteItem(writer itemWriter, itemID string) error {
	if writer == nil {
		return errors.New("media item writer is unavailable")
	}
	_, err := writer.Exec("DELETE FROM items WHERE id=?", itemID)
	return err
}

func (r sqlMediaItemRepository) ItemTree(itemID string) ([]Item, error) {
	if r.db == nil {
		return nil, errors.New("media item database is unavailable")
	}
	rows, err := r.db.Query("WITH RECURSIVE tree AS (SELECT "+cols+" FROM items WHERE id=? UNION ALL SELECT "+prefixedCols("i")+" FROM items i JOIN tree t ON i.parent=t.id) SELECT "+cols+" FROM tree", itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		item, err := readItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r sqlMediaItemRepository) ItemsAtOrUnderPath(path string) ([]ItemPathRecord, error) {
	if r.db == nil {
		return nil, errors.New("media item database is unavailable")
	}
	rows, err := r.db.Query("SELECT id,path FROM items WHERE path=? OR substr(path,1,length(?))=?", path, path+"/", path+"/")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ItemPathRecord, 0)
	for rows.Next() {
		var item ItemPathRecord
		if err := rows.Scan(&item.ID, &item.Path); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r sqlMediaItemRepository) CountByKind(kind string) (int, error) {
	if r.db == nil {
		return 0, errors.New("media item database is unavailable")
	}
	var count int
	err := r.db.QueryRow("SELECT count(*) FROM items WHERE kind=?", kind).Scan(&count)
	return count, err
}

func (r sqlMediaItemRepository) CountAll() (int, error) {
	if r.db == nil {
		return 0, errors.New("media item database is unavailable")
	}
	var count int
	err := r.db.QueryRow("SELECT count(*) FROM items").Scan(&count)
	return count, err
}

func (r sqlMediaItemRepository) CountPlayableByLibrary(libraryID string) (int, error) {
	if r.db == nil {
		return 0, errors.New("media item database is unavailable")
	}
	var count int
	err := r.db.QueryRow("SELECT count(*) FROM items WHERE lib=? AND kind IN ('Movie','Episode')", libraryID).Scan(&count)
	return count, err
}

func (r sqlMediaItemRepository) NextEpisode(seriesID string, season, episode int) (Item, error) {
	if r.db == nil {
		return Item{}, errors.New("media item database is unavailable")
	}
	return readItem(r.db.QueryRow("SELECT "+cols+" FROM items WHERE kind='Episode' AND (parent=? OR parent IN (SELECT id FROM items WHERE parent=? AND kind='Season')) AND (season>? OR (season=? AND episode>?)) ORDER BY season,episode,id LIMIT 1", seriesID, seriesID, season, season, episode))
}

func (r sqlMediaItemRepository) LibraryImageCandidate(libraryID, kind string) (Item, error) {
	if r.db == nil {
		return Item{}, sql.ErrNoRows
	}
	var filter string
	switch kind {
	case "Primary":
		filter = "poster<>''"
	case "Backdrop":
		filter = "(url LIKE '%.jpg' OR poster<>'')"
	default:
		return Item{}, errors.New("unsupported library image kind")
	}
	return readItem(r.db.QueryRow("SELECT "+cols+" FROM items WHERE lib=? AND "+filter+" ORDER BY name,id LIMIT 1", libraryID))
}

func (r sqlMediaItemRepository) LibraryCollageCandidates(libraryID string) ([]Item, error) {
	if r.db == nil {
		return nil, errors.New("media item database is unavailable")
	}
	rows, err := r.db.Query(`SELECT `+cols+` FROM items WHERE lib=?
		ORDER BY CASE WHEN kind IN ('Series','Movie') THEN 0 ELSE 1 END,
			CASE WHEN poster<>'' THEN 0 ELSE 1 END,id DESC LIMIT 30`, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Item, 0, 30)
	for rows.Next() {
		item, err := readItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r sqlMediaItemRepository) YearsInLibrary(libraryID string) ([]int, error) {
	if r.db == nil {
		return nil, errors.New("media item database is unavailable")
	}
	rows, err := r.db.Query("SELECT DISTINCT year FROM items WHERE year>0 AND (?='' OR lib=?) ORDER BY year DESC", libraryID, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	years := make([]int, 0)
	for rows.Next() {
		var year int
		if err := rows.Scan(&year); err != nil {
			return nil, err
		}
		years = append(years, year)
	}
	return years, rows.Err()
}

func (r sqlMediaItemRepository) GenreNamesByIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	if r.db == nil {
		return nil, sql.ErrNoRows
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[id] = struct{}{}
	}
	rows, err := r.db.Query("SELECT DISTINCT genre FROM item_genres")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if _, ok := wanted["genre-"+digest(name)[:32]]; ok {
			names = append(names, name)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func (r sqlMediaItemRepository) GenreNamesInLibrary(libraryID string) ([]string, error) {
	if r.db == nil {
		return nil, sql.ErrNoRows
	}
	rows, err := r.db.Query("SELECT DISTINCT g.genre FROM item_genres g JOIN items i ON i.id=g.item WHERE (?='' OR i.lib=?) ORDER BY g.genre", libraryID, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return names, nil
}

func (r sqlMediaItemRepository) VersionsByGrouping(grouping, itemID string) ([]Item, error) {
	if r.db == nil || grouping == "" || itemID == "" {
		return nil, sql.ErrNoRows
	}
	rows, err := r.db.Query("SELECT "+cols+" FROM items WHERE "+grouping+"=(SELECT "+grouping+" FROM items WHERE id=?) AND url<>'' ORDER BY id", itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		item, err := readItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (r sqlMediaItemRepository) SimilarItems(item Item, apiUser bool, grouping string, limit int) ([]Item, error) {
	if r.db == nil {
		return nil, errors.New("media item database is unavailable")
	}
	if limit < 1 || limit > 50 {
		return nil, errors.New("similar items limit is out of range")
	}
	filter := "id IN (SELECT id FROM candidates) AND kind=? AND id<>?"
	filterArgs := []any{item.Kind, item.ID}
	if !apiUser {
		if grouping == "" {
			return nil, errors.New("media version grouping is unavailable")
		}
		filter = "id IN (SELECT id FROM candidates) AND kind=? AND " + grouping + "<>(SELECT " + grouping + " FROM items WHERE id=?)"
		filterArgs = []any{item.Kind, item.ID}
		if grouping != "id" {
			filter = "id IN (SELECT DISTINCT ON (" + grouping + ") id FROM items WHERE " + filter + " ORDER BY " + grouping + ",id)"
		}
	}
	args := []any{item.ID, item.Kind, item.Year, item.ID, item.Kind, item.ID}
	args = append(args, filterArgs...)
	args = append(args, item.ID, item.Year, limit)
	rows, err := r.db.Query(`WITH candidates AS (
		SELECT id FROM (SELECT item AS id FROM item_genres WHERE genre IN (SELECT genre FROM item_genres WHERE item=?) LIMIT 512)
		UNION
		SELECT id FROM (SELECT id FROM items WHERE kind=? AND year=? AND id<>? ORDER BY name,id LIMIT 128)
		UNION
		SELECT id FROM (SELECT id FROM items WHERE kind=? AND id<>? ORDER BY name,id LIMIT 128)
	) SELECT `+cols+` FROM items WHERE `+filter+`
	ORDER BY (SELECT count(*) FROM item_genres g WHERE g.item=items.id AND g.genre IN (SELECT genre FROM item_genres WHERE item=?)) DESC,abs(year-?),name,id LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Item, 0, limit)
	for rows.Next() {
		candidate, err := readItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, candidate)
	}
	return items, rows.Err()
}

func (r sqlMediaItemRepository) PersonName(personID string) (string, error) {
	if r.db == nil {
		return "", sql.ErrNoRows
	}
	var name string
	err := r.db.QueryRow("SELECT name FROM item_people WHERE person=? LIMIT 1", personID).Scan(&name)
	return name, err
}

func (r sqlMediaItemRepository) PersonItemCount(personID string) (int, error) {
	if r.db == nil {
		return 0, sql.ErrNoRows
	}
	var count int
	err := r.db.QueryRow("SELECT count(DISTINCT item) FROM item_people WHERE person=?", personID).Scan(&count)
	return count, err
}

func (r sqlMediaItemRepository) CountPeople(query PersonPageQuery) (int, error) {
	if r.db == nil {
		return 0, sql.ErrNoRows
	}
	where, args := personQueryFilter(query)
	var count int
	err := r.db.QueryRow("SELECT count(DISTINCT person) FROM item_people WHERE "+where, args...).Scan(&count)
	return count, err
}

func personQueryFilter(query PersonPageQuery) (string, []any) {
	where := query.Where
	if where == "" {
		where = "1=1"
	}
	args := append([]any{}, query.Args...)
	if query.RequireAvailableImage {
		where += " AND EXISTS (SELECT 1 FROM item_people visible WHERE visible.person=item_people.person AND visible.thumb<>'' AND NOT EXISTS (SELECT 1 FROM actor_image_failures f WHERE f.person=visible.person AND f.path=visible.thumb AND f.expires>?))"
		args = append(args, query.ImageCheckedAt)
	}
	return where, args
}

func (r sqlMediaItemRepository) PersonImageThumb(personID string) (string, error) {
	if r.db == nil {
		return "", sql.ErrNoRows
	}
	var thumb string
	err := r.db.QueryRow("SELECT thumb FROM item_people WHERE person=? AND thumb<>'' ORDER BY item LIMIT 1", personID).Scan(&thumb)
	return thumb, err
}

func (r sqlMediaItemRepository) PersonImageFailed(personID, path string, now int64) (bool, error) {
	if r.db == nil {
		return false, sql.ErrNoRows
	}
	var exists int
	err := r.db.QueryRow("SELECT 1 FROM actor_image_failures WHERE person=? AND path=? AND expires>?", personID, path, now).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r sqlMediaItemRepository) RecordPersonImageFailure(personID, path string, expires int64) error {
	if r.db == nil {
		return sql.ErrNoRows
	}
	_, err := r.db.Exec("INSERT INTO actor_image_failures(person,path,expires) VALUES(?,?,?) ON CONFLICT(person) DO UPDATE SET path=excluded.path,expires=excluded.expires", personID, path, expires)
	return err
}

func (r sqlMediaItemRepository) ListPeople(query PersonPageQuery) ([]PersonRecord, error) {
	if r.db == nil {
		return nil, sql.ErrNoRows
	}
	where, args := personQueryFilter(query)
	if query.AfterName != "" || query.AfterID != "" {
		where += " AND (name,person)>(?,?)"
		args = append(args, query.AfterName, query.AfterID)
	}
	statement := "SELECT DISTINCT person,name FROM item_people WHERE " + where + " ORDER BY name,person LIMIT ?"
	args = append(args, query.Limit+1)
	if query.AfterName == "" && query.AfterID == "" && query.Position > 0 {
		statement += " OFFSET ?"
		args = append(args, query.Position)
	}
	rows, err := r.db.Query(statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	people := make([]PersonRecord, 0, query.Limit+1)
	for rows.Next() {
		var person PersonRecord
		if err := rows.Scan(&person.ID, &person.Name); err != nil {
			return nil, err
		}
		people = append(people, person)
	}
	return people, rows.Err()
}

func (a *App) mediaItems() MediaItemRepository {
	if a.mediaItemRepository != nil {
		return a.mediaItemRepository
	}
	return sqlMediaItemRepository{db: a.db}
}

func (a *App) item(id string) (Item, error) {
	return a.mediaItems().GetByID(id)
}
