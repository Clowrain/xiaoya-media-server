package embyserver

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildItemBrowseQueryPreservesFiltersAndPageSettings(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("INSERT INTO settings(k,v) VALUES('search_by_initials','false')"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES(?,?,?,?)", "query-lib", "Movies", "/media", "movies"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/emby/Items?ParentId=query-lib&IncludeItemTypes=Movie&SearchTerm=Alpha&NameStartsWith=A&Limit=15", nil)

	query, err := app.buildItemBrowseQuery(request, User{API: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Where, "lib=?") || !strings.Contains(query.Where, "parent=?") || !strings.Contains(query.Where, "kind IN (?)") || !strings.Contains(query.Where, "name ILIKE") {
		t.Fatalf("browse predicates were not assembled: %q", query.Where)
	}
	wantArgs := []any{"query-lib", "query-lib", "%Alpha%", "A%", "Movie"}
	if len(query.Args) != len(wantArgs) {
		t.Fatalf("browse arguments = %v, want %v", query.Args, wantArgs)
	}
	for i := range wantArgs {
		if query.Args[i] != wantArgs[i] {
			t.Fatalf("browse arguments = %v, want %v", query.Args, wantArgs)
		}
	}
	if query.Limit != 15 || query.Resume {
		t.Fatalf("unexpected pagination settings: %+v", query)
	}
}

func TestBuildItemBrowseQueryUsesUserResumePredicate(t *testing.T) {
	app := newTestSQLiteApp(t)
	request := httptest.NewRequest("GET", "/Items/Resume", nil)
	query, err := app.buildItemBrowseQuery(request, User{ID: "resume-user"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !query.Resume || !strings.Contains(query.Where, "kind IN ('Movie','Episode')") || !strings.Contains(query.Where, "id IN (SELECT item FROM userdata") {
		t.Fatalf("resumable item predicate missing: %+v", query)
	}
	if len(query.Args) != 1 || query.Args[0] != "resume-user" {
		t.Fatalf("resume arguments = %v", query.Args)
	}
}

func TestBuildItemBrowseQueryRejectsExcessiveIDs(t *testing.T) {
	app := newTestSQLiteApp(t)
	ids := strings.TrimSuffix(strings.Repeat("x,", 1001), ",")
	request := httptest.NewRequest("GET", "/Items?Ids="+ids, nil)

	_, err := app.buildItemBrowseQuery(request, User{API: true}, false)
	requestErr, ok := err.(*browseRequestError)
	if !ok || requestErr.status != 400 || requestErr.message != "too many ids" {
		t.Fatalf("unexpected ID limit error: %v", err)
	}
}
