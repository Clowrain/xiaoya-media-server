package embyserver

type UserItemDataDTO struct {
	Rating                *float64 `json:"Rating"`
	PlayedPercentage      float64  `json:"PlayedPercentage"`
	UnplayedItemCount     int      `json:"UnplayedItemCount"`
	PlaybackPositionTicks int64    `json:"PlaybackPositionTicks"`
	PlayCount             int      `json:"PlayCount"`
	IsFavorite            bool     `json:"IsFavorite"`
	Likes                 *bool    `json:"Likes"`
	LastPlayedDate        *string  `json:"LastPlayedDate"`
	Played                bool     `json:"Played"`
	Key                   string   `json:"Key"`
	ItemID                string   `json:"ItemId"`
}

func makeUserItemDataDTO(itemID string, position int64, played, isFavorite bool) UserItemDataDTO {
	dto := UserItemDataDTO{
		PlayedPercentage:      0,
		UnplayedItemCount:     0,
		PlaybackPositionTicks: position,
		PlayCount:             0,
		IsFavorite:            isFavorite,
		Played:                played,
		Key:                   itemID,
		ItemID:                itemID,
	}
	if played {
		dto.PlayCount = 1
		dto.PlayedPercentage = 100
	}
	return dto
}
