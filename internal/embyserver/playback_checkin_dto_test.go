package embyserver

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParsePlaybackCheckInRequestPrefersBodyAndFallsBackToQuery(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/Sessions/Playing/Progress?ItemId=query-item&PositionTicks=500&RunTimeTicks=600&MediaSourceId=query-source", strings.NewReader(`{"ItemId":"body-item","PositionTicks":125,"RunTimeTicks":456,"MediaSourceId":"body-source"}`))
	request := parsePlaybackCheckInRequest(w, r)
	if request.ItemID != "body-item" || request.MediaSourceID != "body-source" || request.PositionTicks == nil || int64(*request.PositionTicks) != 125 || int64(request.RunTimeTicks) != 456 {
		t.Fatalf("body values were not preferred: %+v", request)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/Sessions/Playing/Progress?MediaSourceId=query-source&PositionTicks=123&RunTimeTicks=456", strings.NewReader(`{"PositionTicks":null,"RunTimeTicks":null}`))
	request = parsePlaybackCheckInRequest(w, r)
	if request.ItemID != "query-source" || request.MediaSourceID != "query-source" || request.PositionTicks == nil || int64(*request.PositionTicks) != 123 || int64(request.RunTimeTicks) != 456 {
		t.Fatalf("query fallbacks were not applied: %+v", request)
	}
}

func TestParsePlaybackCheckInRequestToleratesMalformedBody(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/Sessions/Playing?ItemId=query-item&PositionTicks=789", strings.NewReader("{"))
	request := parsePlaybackCheckInRequest(w, r)
	if request.ItemID != "query-item" || request.PositionTicks == nil || int64(*request.PositionTicks) != 789 {
		t.Fatalf("malformed body should fall back to query values: %+v", request)
	}
}
