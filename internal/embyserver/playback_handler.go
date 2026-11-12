package embyserver

import "net/http"

func (a *App) playback(w http.ResponseWriter, r *http.Request, u User, itemID string) {
	item, err := a.item(itemID)
	if err != nil {
		fail(w, http.StatusNotFound, "媒体不存在")
		return
	}
	if item.URL == "" {
		respond(w, a.playbackInfoDTO(item, r, u))
		return
	}
	if !u.API {
		if err := a.reserve(u, itemID); err != nil {
			fail(w, http.StatusForbidden, err.Error())
			return
		}
	}
	respond(w, a.playbackInfoDTO(item, r, u))
}

func (a *App) playbackInfoDTO(item Item, r *http.Request, user User) PlaybackInfoResponseDTO {
	if item.URL == "" {
		return PlaybackInfoResponseDTO{
			MediaSources:  []M{},
			PlaySessionID: id(),
			ErrorCode:     "NoCompatibleStreams",
		}
	}
	return PlaybackInfoResponseDTO{
		MediaSources:  a.versionSources(item, r, user, false),
		PlaySessionID: id(),
	}
}
