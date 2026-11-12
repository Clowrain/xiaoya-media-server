package embyserver

import (
	"encoding/json"
	"testing"
)

func TestItemsQueryResultDTOPreservesWirePageValues(t *testing.T) {
	cursor, hasMore := "next-page-token", true
	payload, err := json.Marshal(ItemsQueryResultDTO{
		Items:            []M{{"Id": "item-1", "Name": "Film"}},
		TotalRecordCount: 42,
		StartIndex:       10,
		NextCursor:       &cursor,
		HasMore:          &hasMore,
	})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Items            []map[string]any `json:"Items"`
		TotalRecordCount int              `json:"TotalRecordCount"`
		StartIndex       int              `json:"StartIndex"`
		NextCursor       string           `json:"NextCursor"`
		HasMore          bool             `json:"HasMore"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0]["Id"] != "item-1" || response.TotalRecordCount != 42 || response.StartIndex != 10 || response.NextCursor != cursor || !response.HasMore {
		t.Fatalf("Items wire page changed: %s", payload)
	}
}

func TestItemsQueryResultDTOOptionalCursorFields(t *testing.T) {
	base, err := json.Marshal(ItemsQueryResultDTO{
		Items:            []M{},
		TotalRecordCount: 0,
		StartIndex:       0,
	})
	if err != nil {
		t.Fatal(err)
	}
	var baseFields map[string]json.RawMessage
	if err := json.Unmarshal(base, &baseFields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"Items", "TotalRecordCount", "StartIndex"} {
		if _, ok := baseFields[key]; !ok {
			t.Fatalf("missing query result field %s: %s", key, base)
		}
	}
	for _, key := range []string{"NextCursor", "HasMore"} {
		if _, ok := baseFields[key]; ok {
			t.Fatalf("non-paged response must omit %s: %s", key, base)
		}
	}

	next, more := "", false
	paged, err := json.Marshal(ItemsQueryResultDTO{
		Items:            []M{},
		TotalRecordCount: 0,
		StartIndex:       0,
		NextCursor:       &next,
		HasMore:          &more,
	})
	if err != nil {
		t.Fatal(err)
	}
	var pagedFields map[string]json.RawMessage
	if err := json.Unmarshal(paged, &pagedFields); err != nil {
		t.Fatal(err)
	}
	if string(pagedFields["NextCursor"]) != `""` || string(pagedFields["HasMore"]) != "false" {
		t.Fatalf("paged response lost explicit cursor state: %s", paged)
	}
}
