package embyserver

import (
	"encoding/json"
	"testing"
)

func TestPlaybackInfoResponseDTOShapes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dto      PlaybackInfoResponseDTO
		hasError bool
	}{
		{name: "compatible sources", dto: PlaybackInfoResponseDTO{MediaSources: []M{}, PlaySessionID: "session-1"}},
		{name: "no compatible streams", dto: PlaybackInfoResponseDTO{MediaSources: []M{}, PlaySessionID: "session-2", ErrorCode: "NoCompatibleStreams"}, hasError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(tc.dto)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"MediaSources", "PlaySessionId"} {
				if _, ok := fields[key]; !ok {
					t.Fatalf("missing PlaybackInfo field %s: %s", key, body)
				}
			}
			_, hasError := fields["ErrorCode"]
			if hasError != tc.hasError {
				t.Fatalf("unexpected ErrorCode presence: %s", body)
			}
		})
	}
}
