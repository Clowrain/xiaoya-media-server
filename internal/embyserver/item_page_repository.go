package embyserver

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type ItemPageQuery struct {
	Where        string
	Args         []any
	Order        string
	UserID       string
	CursorValues []string
	Position     int
	Limit        int
}

type ItemPageResult struct {
	Items        []Item
	CursorValues []string
	HasMore      bool
}

type ItemPageRepository interface {
	Count(where string, args []any) (int, error)
	LoadPage(query ItemPageQuery) (ItemPageResult, error)
}

func itemSortValue(item Item, column string) string {
	switch column {
	case "id":
		return item.ID
	case "name":
		return item.Name
	case "year":
		return strconv.Itoa(item.Year)
	case "mtime":
		return strconv.FormatInt(item.Mtime, 10)
	case "season":
		return strconv.Itoa(item.Season)
	case "episode":
		return strconv.Itoa(item.Episode)
	}
	return ""
}

func (r sqlMediaItemRepository) Count(where string, args []any) (int, error) {
	var count int
	err := r.db.QueryRow("SELECT count(*) FROM items WHERE "+where, args...).Scan(&count)
	return count, err
}

func (r sqlMediaItemRepository) LoadPage(query ItemPageQuery) (ItemPageResult, error) {
	columns := []string{}
	descending := strings.Contains(query.Order, "DESC")
	resumeOrder := query.Order == "resume"
	if resumeOrder {
		columns = []string{"COALESCE((SELECT updated FROM resume_activity WHERE user_id=? AND item=items.id),0)", "id"}
		descending = true
	} else {
		for _, part := range strings.Split(query.Order, ",") {
			fields := strings.Fields(part)
			if len(fields) == 0 {
				return ItemPageResult{}, errors.New("invalid item sort order")
			}
			columns = append(columns, fields[0])
		}
	}
	direction, operator := " ASC", ">"
	if descending {
		direction, operator = " DESC", "<"
	}
	orderParts := make([]string, 0, len(columns))
	for _, column := range columns {
		orderParts = append(orderParts, column+direction)
	}

	where := query.Where
	params := append([]any{}, query.Args...)
	if len(query.CursorValues) > 0 {
		if len(query.CursorValues) != len(columns) {
			return ItemPageResult{}, errors.New("invalid cursor keys")
		}
		cursorValues := strings.TrimRight(strings.Repeat("?,", len(columns)), ",")
		if resumeOrder {
			cursorValues = "CAST(? AS BIGINT),?"
		}
		where += " AND (" + strings.Join(columns, ",") + ") " + operator + " (" + cursorValues + ")"
		if resumeOrder {
			params = append(params, query.UserID)
		}
		for _, value := range query.CursorValues {
			params = append(params, value)
		}
	}
	if resumeOrder {
		params = append(params, query.UserID)
	}
	params = append(params, query.Limit+1)
	statement := "SELECT " + cols + " FROM items WHERE " + where + " ORDER BY " + strings.Join(orderParts, ",") + " LIMIT ?"
	if len(query.CursorValues) == 0 && query.Position > 0 {
		statement += " OFFSET ?"
		params = append(params, query.Position)
	}
	rows, err := r.db.Query(statement, params...)
	if err != nil {
		return ItemPageResult{}, err
	}
	loaded := make([]Item, 0)
	for rows.Next() {
		item, scanErr := readItem(rows)
		if scanErr != nil {
			rows.Close()
			return ItemPageResult{}, scanErr
		}
		loaded = append(loaded, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ItemPageResult{}, err
	}
	rows.Close()

	result := ItemPageResult{Items: loaded, HasMore: len(loaded) > query.Limit}
	if !result.HasMore {
		return result, nil
	}
	result.Items = loaded[:query.Limit]
	last := result.Items[len(result.Items)-1]
	result.CursorValues = make([]string, 0, len(columns))
	for _, column := range columns {
		if strings.HasPrefix(column, "COALESCE") {
			var updated int64
			err := r.db.QueryRow("SELECT COALESCE((SELECT updated FROM resume_activity WHERE user_id=? AND item=?),0)", query.UserID, last.ID).Scan(&updated)
			if err != nil {
				return ItemPageResult{}, err
			}
			result.CursorValues = append(result.CursorValues, fmt.Sprint(updated))
		} else if column == "rating" {
			var rating float64
			if err := r.db.QueryRow("SELECT rating FROM items WHERE id=?", last.ID).Scan(&rating); err != nil {
				return ItemPageResult{}, err
			}
			result.CursorValues = append(result.CursorValues, strconv.FormatFloat(rating, 'g', -1, 64))
		} else {
			result.CursorValues = append(result.CursorValues, itemSortValue(last, column))
		}
	}
	return result, nil
}

func (a *App) itemPageStore() ItemPageRepository {
	if a.itemPageRepository != nil {
		return a.itemPageRepository
	}
	return sqlMediaItemRepository{db: a.db}
}
