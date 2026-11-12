package embyserver

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type PlaybackCheckInRequest struct {
	ItemID        string       `json:"ItemId"`
	PositionTicks *clientTicks `json:"PositionTicks"`
	RunTimeTicks  clientTicks  `json:"RunTimeTicks"`
	MediaSourceID string       `json:"MediaSourceId"`
}

func parsePlaybackCheckInRequest(w http.ResponseWriter, r *http.Request) PlaybackCheckInRequest {
	var request PlaybackCheckInRequest
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request)
	if request.ItemID == "" {
		request.ItemID = q(r, "ItemId")
	}
	if request.MediaSourceID == "" {
		request.MediaSourceID = q(r, "MediaSourceId")
	}
	if request.ItemID == "" {
		request.ItemID = request.MediaSourceID
	}
	if request.PositionTicks == nil {
		if value := q(r, "PositionTicks"); value != "" {
			if ticks, err := strconv.ParseInt(value, 10, 64); err == nil {
				position := clientTicks(ticks)
				request.PositionTicks = &position
			}
		}
	}
	if request.RunTimeTicks == 0 {
		if value := q(r, "RunTimeTicks"); value != "" {
			if ticks, err := strconv.ParseInt(value, 10, 64); err == nil {
				request.RunTimeTicks = clientTicks(ticks)
			}
		}
	}
	return request
}
