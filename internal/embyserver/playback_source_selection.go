package embyserver

type mediaVersionRepository interface {
	VersionsByGrouping(grouping, itemID string) ([]Item, error)
}

type mediaSourceSelector interface {
	Select(item Item, user User, grouping string) []Item
}

type groupedMediaSourceSelector struct {
	versions mediaVersionRepository
}

func (s groupedMediaSourceSelector) Select(item Item, user User, grouping string) []Item {
	selected := []Item{item}
	if user.API || item.URL == "" || s.versions == nil {
		return selected
	}
	versions, err := s.versions.VersionsByGrouping(grouping, item.ID)
	if err != nil {
		return selected
	}
	for _, version := range versions {
		if version.ID != item.ID {
			selected = append(selected, version)
		}
	}
	return selected
}

func (a *App) mediaSourceSelector() mediaSourceSelector {
	if a.playbackSourceSelector != nil {
		return a.playbackSourceSelector
	}
	return groupedMediaSourceSelector{versions: a.mediaItems()}
}
