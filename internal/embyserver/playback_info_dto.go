package embyserver

type PlaybackInfoResponseDTO struct {
	MediaSources  []M    `json:"MediaSources"`
	PlaySessionID string `json:"PlaySessionId"`
	ErrorCode     string `json:"ErrorCode,omitempty"`
}
