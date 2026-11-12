package embyserver

import (
	"encoding/json"
	"strconv"
	"testing"
)

func TestUserItemDataDTOShapeAndState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		position  int64
		played    bool
		favorite  bool
		playCount int
		playedPct float64
	}{
		{name: "unplayed", position: 420, playCount: 0, playedPct: 0},
		{name: "played favorite", position: 987, played: true, favorite: true, playCount: 1, playedPct: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := makeUserItemDataDTO("item-1", tc.position, tc.played, tc.favorite)
			encoded, err := json.Marshal(data)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"Rating", "PlayedPercentage", "UnplayedItemCount", "PlaybackPositionTicks", "PlayCount", "IsFavorite", "Likes", "LastPlayedDate", "Played", "Key", "ItemId"} {
				if _, ok := fields[key]; !ok {
					t.Fatalf("missing UserData field %s: %s", key, encoded)
				}
			}
			for _, key := range []string{"Rating", "Likes", "LastPlayedDate"} {
				if string(fields[key]) != "null" {
					t.Fatalf("%s should remain null, got %s", key, fields[key])
				}
			}
			if string(fields["PlaybackPositionTicks"]) != strconv.FormatInt(tc.position, 10) || string(fields["PlayCount"]) != strconv.Itoa(tc.playCount) {
				t.Fatalf("unexpected playback values: %s", encoded)
			}
			if string(fields["PlayedPercentage"]) != strconv.FormatFloat(tc.playedPct, 'f', -1, 64) {
				t.Fatalf("unexpected played percentage: %s", encoded)
			}
			if data.Played != tc.played || data.IsFavorite != tc.favorite || data.Key != "item-1" || data.ItemID != "item-1" {
				t.Fatalf("unexpected UserData state: %+v", data)
			}
		})
	}
}
