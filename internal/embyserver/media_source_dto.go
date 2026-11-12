package embyserver

type MediaSourceDTO struct {
	ID                         string     `json:"Id"`
	Name                       string     `json:"Name"`
	Path                       string     `json:"Path"`
	Protocol                   string     `json:"Protocol"`
	Type                       string     `json:"Type"`
	Container                  string     `json:"Container"`
	IsRemote                   bool       `json:"IsRemote"`
	SupportsDirectPlay         bool       `json:"SupportsDirectPlay"`
	SupportsDirectStream       bool       `json:"SupportsDirectStream"`
	SupportsTranscoding        bool       `json:"SupportsTranscoding"`
	RequiresOpening            bool       `json:"RequiresOpening"`
	RequiresClosing            bool       `json:"RequiresClosing"`
	MediaStreams               []struct{} `json:"MediaStreams"`
	DirectStreamURL            string     `json:"DirectStreamUrl"`
	AddAPIKeyToDirectStreamURL bool       `json:"AddApiKeyToDirectStreamUrl"`
}

func mediaSourceCount(value any) (int, bool) {
	switch sources := value.(type) {
	case []M:
		return len(sources), true
	case []MediaSourceDTO:
		return len(sources), true
	default:
		return 0, false
	}
}

func (s MediaSourceDTO) asMap() M {
	return M{
		"Id":                         s.ID,
		"Name":                       s.Name,
		"Path":                       s.Path,
		"Protocol":                   s.Protocol,
		"Type":                       s.Type,
		"Container":                  s.Container,
		"IsRemote":                   s.IsRemote,
		"SupportsDirectPlay":         s.SupportsDirectPlay,
		"SupportsDirectStream":       s.SupportsDirectStream,
		"SupportsTranscoding":        s.SupportsTranscoding,
		"RequiresOpening":            s.RequiresOpening,
		"RequiresClosing":            s.RequiresClosing,
		"MediaStreams":               s.MediaStreams,
		"DirectStreamUrl":            s.DirectStreamURL,
		"AddApiKeyToDirectStreamUrl": s.AddAPIKeyToDirectStreamURL,
	}
}
