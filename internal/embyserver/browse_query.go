package embyserver

import (
	"log"
	"net/http"
	"strconv"
	"strings"
)

type itemBrowseQuery struct {
	Where      string
	Args       []any
	Resume     bool
	ParentItem *Item
	Limit      int
	Order      string
}

type browseRequestError struct {
	status  int
	message string
}

func (e *browseRequestError) Error() string { return e.message }

func isResumeBrowse(r *http.Request) bool {
	return strings.HasSuffix(strings.ToLower(r.URL.Path), "/resume") || strings.Contains(strings.ToLower(q(r, "Filters")), "isresumable")
}

func (a *App) buildItemBrowseQuery(r *http.Request, user User, latest bool) (itemBrowseQuery, error) {
	query := itemBrowseQuery{Where: "1=1", Resume: isResumeBrowse(r)}
	if query.Resume {
		resumeFilter := a.userStore().ResumableItemPredicate(user.ID)
		query.Where += " AND kind IN ('Movie','Episode') AND " + resumeFilter.SQL
		query.Args = append(query.Args, resumeFilter.Args...)
	}

	parent := q(r, "ParentId")
	if parent == "root" || parent == a.serverID {
		parent = ""
	}
	if parent != "" {
		isLibrary, err := a.libraryStore().Exists(parent)
		if err != nil {
			// Preserve the previous fallback: an unavailable library lookup was treated as an item parent.
			log.Printf("browse parent library lookup: %v", err)
		}
		if isLibrary {
			query.Where += " AND lib=?"
			query.Args = append(query.Args, parent)
			if !query.Resume && !latest && !strings.EqualFold(q(r, "Recursive"), "true") {
				query.Where += " AND parent=?"
				query.Args = append(query.Args, parent)
			}
		} else if !user.API && a.versionGrouping() != "id" {
			group := a.versionGrouping()
			parents := "SELECT id FROM items WHERE " + group + "=(SELECT " + group + " FROM items WHERE id=?)"
			if strings.EqualFold(q(r, "Recursive"), "true") {
				query.Where += " AND id IN (WITH RECURSIVE tree(id) AS (SELECT id FROM items WHERE parent IN (" + parents + ") UNION SELECT items.id FROM items JOIN tree ON items.parent=tree.id) SELECT id FROM tree)"
			} else {
				query.Where += " AND parent IN (" + parents + ")"
			}
			query.Args = append(query.Args, parent)
		} else if strings.EqualFold(q(r, "Recursive"), "true") {
			query.Where += " AND id IN (WITH RECURSIVE tree(id) AS (SELECT id FROM items WHERE parent=? UNION SELECT items.id FROM items JOIN tree ON items.parent=tree.id) SELECT id FROM tree)"
			query.Args = append(query.Args, parent)
		} else {
			query.Where += " AND parent=?"
			query.Args = append(query.Args, parent)
		}
	}

	if ids := q(r, "Ids"); ids != "" {
		parts := strings.Split(ids, ",")
		if len(parts) > 1000 {
			return itemBrowseQuery{}, &browseRequestError{status: http.StatusBadRequest, message: "too many ids"}
		}
		query.Where += " AND id IN (" + strings.TrimRight(strings.Repeat("?,", len(parts)), ",") + ")"
		for _, id := range parts {
			query.Args = append(query.Args, id)
		}
	}
	if search := strings.TrimSpace(q(r, "SearchTerm")); search != "" {
		initials := a.defaultOn("search_by_initials") && isInitialsQuery(search)
		prefix := "%"
		if initials && len(search) <= 2 {
			prefix = ""
		}
		clause := "name ILIKE ? ESCAPE '\\'"
		search = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(search)
		query.Args = append(query.Args, prefix+search+"%")
		if initials {
			clause += " OR media_initials(name) LIKE ? ESCAPE '\\'"
			query.Args = append(query.Args, prefix+strings.ToLower(search)+"%")
		}
		query.Where += " AND (" + clause + ")"
	}
	if prefix := strings.TrimSpace(q(r, "NameStartsWith")); prefix != "" {
		prefix = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(prefix)
		clause := "name ILIKE ? ESCAPE '\\'"
		query.Args = append(query.Args, prefix+"%")
		if a.defaultOn("search_by_initials") {
			clause += " OR media_initials(name) LIKE ? ESCAPE '\\'"
			query.Args = append(query.Args, strings.ToLower(prefix)+"%")
		}
		query.Where += " AND (" + clause + ")"
	}
	if kinds := q(r, "IncludeItemTypes"); kinds != "" {
		parts := strings.Split(kinds, ",")
		if len(parts) > 10 {
			return itemBrowseQuery{}, &browseRequestError{status: http.StatusBadRequest, message: "too many types"}
		}
		query.Where += " AND kind IN (" + strings.TrimRight(strings.Repeat("?,", len(parts)), ",") + ")"
		for _, kind := range parts {
			query.Args = append(query.Args, kind)
		}
	}

	query.Where, query.Args = a.browseFilters(r, user, query.Where, query.Args, latest)
	if !user.API && !query.Resume && q(r, "Ids") == "" {
		query.Where, query.Args = a.mergeWhere(query.Where, query.Args)
	}

	requestedLimit, _ := strconv.Atoi(q(r, "Limit"))
	maxLimit := 200
	if parent != "" {
		if parentItem, err := a.item(parent); err == nil {
			query.ParentItem = &parentItem
			if parentItem.Kind == "Series" || parentItem.Kind == "Season" {
				maxLimit = 10000
				if requestedLimit < 1 {
					requestedLimit = maxLimit
				}
			}
		}
	}
	if requestedLimit < 1 {
		requestedLimit = 60
	}
	if requestedLimit > maxLimit {
		requestedLimit = maxLimit
	}
	query.Limit = requestedLimit
	query.Order = browseOrderWithContext(r, query.ParentItem)
	if latest {
		query.Order = "mtime DESC,id DESC"
	}
	if query.Resume {
		query.Order = "resume"
	}
	return query, nil
}
