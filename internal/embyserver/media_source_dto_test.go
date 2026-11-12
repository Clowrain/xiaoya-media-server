package embyserver

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMediaSourceDTOJSONMatchesMapProjection(t *testing.T) {
	a := &App{}
	source := a.source(Item{
		ID:   "source-1",
		Name: "电影",
		URL:  "https://media.example/movie.mkv?sign=secret",
	}, "api-token")

	typedJSON, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	mapJSON, err := json.Marshal(source.asMap())
	if err != nil {
		t.Fatal(err)
	}
	var typedFields, mapFields map[string]any
	if err := json.Unmarshal(typedJSON, &typedFields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(mapJSON, &mapFields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(typedFields, mapFields) {
		t.Fatalf("typed media source changed protocol fields:\ntyped: %s\nmap:   %s", typedJSON, mapJSON)
	}
	for _, key := range []string{"Id", "Name", "Path", "Protocol", "Type", "Container", "IsRemote", "SupportsDirectPlay", "SupportsDirectStream", "SupportsTranscoding", "RequiresOpening", "RequiresClosing", "MediaStreams", "DirectStreamUrl", "AddApiKeyToDirectStreamUrl"} {
		if _, ok := typedFields[key]; !ok {
			t.Fatalf("missing media source field %s: %s", key, typedJSON)
		}
	}
	want := map[string]any{
		"SupportsDirectPlay":         true,
		"SupportsDirectStream":       true,
		"SupportsTranscoding":        false,
		"IsRemote":                   true,
		"RequiresOpening":            false,
		"RequiresClosing":            false,
		"AddApiKeyToDirectStreamUrl": false,
		"DirectStreamUrl":            "/emby/Videos/source-1/stream.mkv?Static=true&api_key=api-token",
	}
	for key, expected := range want {
		if actual := typedFields[key]; actual != expected {
			t.Errorf("MediaSource.%s = %#v, want %#v", key, actual, expected)
		}
	}
}
