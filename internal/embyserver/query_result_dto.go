package embyserver

type ItemsQueryResultDTO struct {
	Items            []M     `json:"Items"`
	TotalRecordCount int     `json:"TotalRecordCount"`
	StartIndex       int     `json:"StartIndex"`
	NextCursor       *string `json:"NextCursor,omitempty"`
	HasMore          *bool   `json:"HasMore,omitempty"`
}
