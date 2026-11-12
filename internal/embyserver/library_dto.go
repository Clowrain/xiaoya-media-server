package embyserver

import "log"

func (a *App) libraries() []M {
	records, err := a.libraryStore().List()
	if err != nil {
		log.Printf("list libraries: %v", err)
		return []M{}
	}
	out := make([]M, 0, len(records))
	for _, record := range records {
		out = append(out, M{
			"Id":             record.ID,
			"ItemId":         record.ID,
			"Name":           record.Name,
			"Path":           record.Path,
			"Locations":      []string{record.Path},
			"Paths":          []string{record.Path},
			"CollectionType": record.Kind,
			"Kind":           record.Kind,
			"Status":         record.Status,
			"Scanned":        record.Scanned,
			"Error":          record.Error,
			"Count":          record.Count,
			"Items":          record.Count,
			"Duration":       record.Duration,
		})
	}
	for _, library := range out {
		paths := a.libraryPaths(library["Id"].(string), library["Path"].(string))
		library["Locations"] = paths
		library["Paths"] = paths
	}
	return out
}

func (a *App) libraryDTOs() []M {
	out := []M{}
	for _, library := range a.libraries() {
		id := library["Id"].(string)
		out = append(out, M{
			"Id":                      id,
			"Name":                    library["Name"],
			"Type":                    "CollectionFolder",
			"IsFolder":                true,
			"CollectionType":          library["CollectionType"],
			"SortName":                library["Name"],
			"ForcedSortName":          library["Name"],
			"ExternalUrls":            []M{},
			"ProviderIds":             M{},
			"RemoteTrailers":          []M{},
			"Taglines":                []string{},
			"LockedFields":            []string{},
			"LockData":                false,
			"CanDelete":               false,
			"CanDownload":             false,
			"PresentationUniqueKey":   id,
			"ServerId":                a.serverID,
			"ImageTags":               a.libraryImageTags(id),
			"ParentId":                "root",
			"LocationType":            "Virtual",
			"ChildCount":              library["Count"],
			"RecursiveItemCount":      library["Count"],
			"PrimaryImageAspectRatio": 2.0 / 3,
			"BackdropImageTags":       []string{},
			"UserData":                makeUserItemDataDTO(id, 0, false, false),
			"DisplayPreferencesId":    id,
		})
	}
	return out
}
