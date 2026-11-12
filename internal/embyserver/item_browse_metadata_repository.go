package embyserver

import (
	"database/sql"
	"errors"
	"strings"
)

func (r sqlMediaItemRepository) BrowseMetadata(items []Item) (map[string]BrowseItemMetadata, error) {
	metadata := make(map[string]BrowseItemMetadata)
	if r.db == nil || len(items) == 0 {
		return metadata, nil
	}

	seriesIDs := make([]string, 0)
	containerIDs := make([]string, 0)
	episodeIDs := make([]string, 0)
	for _, item := range items {
		switch item.Kind {
		case "Series":
			seriesIDs = append(seriesIDs, item.ID)
			containerIDs = append(containerIDs, item.ID)
			metadata[item.ID] = BrowseItemMetadata{}
		case "Season":
			containerIDs = append(containerIDs, item.ID)
			metadata[item.ID] = BrowseItemMetadata{}
		case "Episode":
			episodeIDs = append(episodeIDs, item.ID)
			metadata[item.ID] = BrowseItemMetadata{}
		}
	}
	var errs []error
	for start := 0; start < len(containerIDs); start += 500 {
		chunk := containerIDs[start:min(start+500, len(containerIDs))]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := r.db.Query("SELECT parent,count(*) FROM items WHERE parent IN ("+placeholders+") GROUP BY parent", args...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for rows.Next() {
			var id string
			var count int
			if err := rows.Scan(&id, &count); err != nil {
				errs = append(errs, err)
				continue
			}
			value := metadata[id]
			value.ChildCount = count
			metadata[id] = value
		}
		if err := rows.Err(); err != nil {
			errs = append(errs, err)
		}
		rows.Close()
	}

	for start := 0; start < len(seriesIDs); start += 500 {
		chunk := seriesIDs[start:min(start+500, len(seriesIDs))]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := r.db.Query(`WITH RECURSIVE tree(series_id,id) AS (
			SELECT series.id,child.id FROM items series JOIN items child ON child.parent=series.id WHERE series.id IN (`+placeholders+`)
			UNION
			SELECT tree.series_id,child.id FROM items child JOIN tree ON child.parent=tree.id
		) SELECT tree.series_id,count(*) FROM tree JOIN items ON items.id=tree.id WHERE items.kind='Episode' GROUP BY tree.series_id`, args...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for rows.Next() {
			var id string
			var count int
			if err := rows.Scan(&id, &count); err != nil {
				errs = append(errs, err)
				continue
			}
			value := metadata[id]
			value.EpisodeCount = count
			metadata[id] = value
		}
		if err := rows.Err(); err != nil {
			errs = append(errs, err)
		}
		rows.Close()
	}

	for start := 0; start < len(episodeIDs); start += 500 {
		chunk := episodeIDs[start:min(start+500, len(episodeIDs))]
		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := r.db.Query(`SELECT ep.id, se.id, se.name, sr.id, sr.name
			FROM items ep
			LEFT JOIN items se ON se.id = ep.parent AND se.kind = 'Season'
			LEFT JOIN items sr ON sr.id = CASE WHEN se.id IS NOT NULL THEN se.parent ELSE ep.parent END
			WHERE ep.id IN (`+placeholders+")", args...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for rows.Next() {
			var id, seasonID, seasonName, seriesID, seriesName sql.NullString
			if err := rows.Scan(&id, &seasonID, &seasonName, &seriesID, &seriesName); err != nil {
				errs = append(errs, err)
				continue
			}
			value := metadata[id.String]
			if seasonID.Valid && seasonID.String != "" {
				value.SeasonID = seasonID.String
				if seasonName.Valid {
					value.SeasonName = seasonName.String
				}
			}
			if seriesID.Valid && seriesID.String != "" {
				value.SeriesID = seriesID.String
				if seriesName.Valid {
					value.SeriesName = seriesName.String
				}
			}
			metadata[id.String] = value
		}
		if err := rows.Err(); err != nil {
			errs = append(errs, err)
		}
		rows.Close()
	}
	return metadata, errors.Join(errs...)
}
