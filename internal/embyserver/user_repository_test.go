package embyserver

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestSQLUserRepositoryAccountDeletion(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("CREATE TABLE user_logins(user_id TEXT PRIMARY KEY, last_login BIGINT NOT NULL DEFAULT 0)"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec("CREATE TABLE user_playback(user_id TEXT PRIMARY KEY,allowed BIGINT NOT NULL DEFAULT 1)"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec("INSERT INTO users(id,name,hash,admin,first_admin,max_devices) VALUES('user-a','Alice','hash',0,0,2),('admin','Admin','hash',1,1,2)"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec("INSERT INTO user_logins(user_id,last_login) VALUES('user-a',123)"); err != nil {
		t.Fatal(err)
	}
	repository := sqlUserRepository{db: app.db}
	allowPlayback := false
	if err := repository.CreateAccount(UserAccount{ID: "user-b", Name: "Bob", Hash: "hash-b", MaxDevices: 3}, &allowPlayback); err != nil {
		t.Fatal(err)
	}
	account, err := repository.GetAccount("user-b")
	if err != nil || account.Name != "Bob" || account.MaxDevices != 3 || account.First {
		t.Fatalf("created account = %+v, err=%v", account, err)
	}
	if err := repository.SetAccountSettings("user-b", 4, true); err != nil {
		t.Fatal(err)
	}
	account, err = repository.GetAccount("user-b")
	if err != nil || !account.Admin || account.MaxDevices != 4 {
		t.Fatalf("updated account = %+v, err=%v", account, err)
	}
	if _, err := app.db.Exec("INSERT INTO tokens(hash,user_id,device,expires) VALUES('token-hash','user-b','device',999)"); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdatePasswordAndRevokeTokens("user-b", "new-hash"); err != nil {
		t.Fatal(err)
	}
	account, err = repository.GetAccount("user-b")
	if err != nil || account.Hash != "new-hash" {
		t.Fatalf("password update = %+v, err=%v", account, err)
	}
	var tokenCount int
	if err := app.db.QueryRow("SELECT count(*) FROM tokens WHERE user_id=?", "user-b").Scan(&tokenCount); err != nil || tokenCount != 0 {
		t.Fatalf("remaining user tokens = %d, err=%v", tokenCount, err)
	}
	accounts, err := repository.ListAccounts()
	if err != nil || len(accounts) != 4 || accounts[0].ID != "admin" || accounts[1].ID != "test-user" || accounts[2].Name != "Alice" || accounts[2].LastLogin != 123 || accounts[3].Name != "Bob" {
		t.Fatalf("ordered user accounts = %+v, err=%v", accounts, err)
	}
	var playbackAllowed bool
	if err := app.db.QueryRow("SELECT allowed FROM user_playback WHERE user_id=?", "user-b").Scan(&playbackAllowed); err != nil || playbackAllowed {
		t.Fatalf("created playback permission = %v, err=%v", playbackAllowed, err)
	}
	if canPlay, err := repository.CanPlay("user-b"); err != nil || canPlay {
		t.Fatalf("CanPlay(user-b) = %v, err=%v", canPlay, err)
	}
	if err := repository.SetPlaybackAllowed("user-a", false); err != nil {
		t.Fatal(err)
	}
	if canPlay, err := repository.CanPlay("user-a"); err != nil || canPlay {
		t.Fatalf("updated CanPlay(user-a) = %v, err=%v", canPlay, err)
	}
	if canPlay, err := repository.CanPlay("admin"); err != nil || !canPlay {
		t.Fatalf("default CanPlay(admin) = %v, err=%v", canPlay, err)
	}
	firstAdmin, err := repository.IsFirstAdmin("admin")
	if err != nil || !firstAdmin {
		t.Fatalf("IsFirstAdmin(admin) = %v, err=%v", firstAdmin, err)
	}
	if _, err := repository.IsFirstAdmin("missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing user error = %v, want sql.ErrNoRows", err)
	}
	if err := repository.DeleteUser("user-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.IsFirstAdmin("user-a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted user lookup error = %v, want sql.ErrNoRows", err)
	}
	if firstAdmin, err := repository.IsFirstAdmin("admin"); err != nil || !firstAdmin {
		t.Fatalf("first admin unexpectedly changed: %v, err=%v", firstAdmin, err)
	}
}

func TestSQLUserRepositoryListAccountsWithoutOptionalLoginTable(t *testing.T) {
	app := newTestSQLiteApp(t)
	accounts, err := (sqlUserRepository{db: app.db}).ListAccounts()
	if err != nil || len(accounts) == 0 {
		t.Fatalf("legacy account list=%+v err=%v", accounts, err)
	}
	for _, account := range accounts {
		if account.LastLogin != 0 {
			t.Fatalf("legacy account has unexpected login time: %+v", account)
		}
	}
}

func TestSQLUserRepositoryListsPlaybackAndFavoritesByRequestedItems(t *testing.T) {
	app := newTestSQLiteApp(t)
	for _, itemID := range []string{"item-a", "item-b"} {
		if _, err := app.db.Exec("INSERT INTO userdata(user_id,item,position,played) VALUES(?,?,?,?)", "user-a", itemID, 42, itemID == "item-a"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.db.Exec("INSERT INTO user_favorites(user_id,item,created_at) VALUES(?,?,?)", "user-a", "item-b", 1); err != nil {
		t.Fatal(err)
	}
	repository := sqlUserRepository{db: app.db}
	states, err := repository.ListPlaybackStates("user-a", []string{"item-a", "item-b", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 2 || states["item-a"].Position != 42 || !states["item-a"].Played || states["item-b"].Played {
		t.Fatalf("unexpected batch playback state: %+v", states)
	}
	favorites, err := repository.ListFavorites("user-a", []string{"item-a", "item-b", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 || !favorites["item-b"] {
		t.Fatalf("unexpected batch favorites: %+v", favorites)
	}
	other, err := repository.ListPlaybackStates("other-user", []string{"item-a", "item-b"})
	if err != nil || len(other) != 0 {
		t.Fatalf("batch playback state leaked across users: %+v err=%v", other, err)
	}
}

func TestSQLUserRepositoryResumeSeedUsesLatestActivityAndLibraryScope(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("INSERT INTO libraries(id,name,path,kind) VALUES('lib-a','A','/media/a','movies'),('lib-b','B','/media/b','movies')"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []Item{
		{ID: "old", Lib: "lib-a", Parent: "lib-a", Name: "Older", Kind: "Movie"},
		{ID: "new", Lib: "lib-a", Parent: "lib-a", Name: "Newest", Kind: "Movie"},
		{ID: "other-lib", Lib: "lib-b", Parent: "lib-b", Name: "Other library", Kind: "Movie"},
	} {
		if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview, item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size, "t"); err != nil {
			t.Fatal(err)
		}
	}
	for _, state := range [][3]any{{"old", true, int64(0)}, {"new", true, int64(10)}, {"other-lib", true, int64(100)}} {
		if _, err := app.db.Exec("INSERT INTO userdata(user_id,item,played,position) VALUES(?,?,?,?)", "resume-user", state[0], state[1], state[2]); err != nil {
			t.Fatal(err)
		}
	}
	for _, activity := range [][2]any{{"old", int64(50)}, {"new", int64(200)}, {"other-lib", int64(900)}} {
		if _, err := app.db.Exec("INSERT INTO resume_activity(user_id,item,updated) VALUES(?,?,?)", "resume-user", activity[0], activity[1]); err != nil {
			t.Fatal(err)
		}
	}
	seed, err := (sqlUserRepository{db: app.db}).ResumeSeed("resume-user", "lib-a")
	if err != nil || seed.ID != "new" || seed.Name != "Newest" {
		t.Fatalf("resume seed = %+v, err=%v", seed, err)
	}
}

func TestSQLUserRepositoryBuildsUserFilterPredicatesWithBoundArguments(t *testing.T) {
	repository := sqlUserRepository{}
	played := repository.PlayedItemPredicate("user-a' OR 1=1 --", false)
	if played.SQL != "id IN (SELECT item FROM userdata WHERE user_id=? AND played=1)" || len(played.Args) != 1 || played.Args[0] != "user-a' OR 1=1 --" {
		t.Fatalf("played predicate is not safely parameterized: %+v", played)
	}
	unplayed := repository.PlayedItemPredicate("user-a", true)
	if !strings.Contains(unplayed.SQL, "NOT IN") {
		t.Fatalf("unplayed predicate = %+v", unplayed)
	}
	personFavorite := repository.FavoritePredicate("user-a", FavoritePerson, false)
	if !strings.HasPrefix(personFavorite.SQL, "person IN ") || personFavorite.Args[0] != "user-a" {
		t.Fatalf("person favorite predicate = %+v", personFavorite)
	}
	itemFavorite := repository.FavoritePredicate("user-a", FavoriteMediaItem, true)
	if !strings.HasPrefix(itemFavorite.SQL, "id NOT IN ") || itemFavorite.Args[0] != "user-a" {
		t.Fatalf("item favorite predicate = %+v", itemFavorite)
	}
	nextUp := repository.NextUpPredicate("user-a")
	if strings.Count(nextUp.SQL, "user_id=?") != 2 || len(nextUp.Args) != 2 || nextUp.Args[0] != "user-a" || nextUp.Args[1] != "user-a" {
		t.Fatalf("NextUp predicate arguments = %+v", nextUp)
	}
}

func TestSQLUserRepositoryPlaybackAndFavoriteStateIsUserScoped(t *testing.T) {
	app := newTestSQLiteApp(t)
	if _, err := app.db.Exec("INSERT INTO userdata(user_id,item,position,played) VALUES(?,?,?,?)", "test-user", "media-1", 900, true); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec("INSERT INTO user_favorites(user_id,item,created_at) VALUES(?,?,?)", "test-user", "media-1", 1); err != nil {
		t.Fatal(err)
	}
	repository := sqlUserRepository{db: app.db}

	state, err := repository.GetPlaybackState("test-user", "media-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Position != 900 || !state.Played {
		t.Fatalf("unexpected playback state: %+v", state)
	}
	favorite, err := repository.IsFavorite("test-user", "media-1")
	if err != nil || !favorite {
		t.Fatalf("favorite state = %t, err=%v", favorite, err)
	}

	otherState, err := repository.GetPlaybackState("other-user", "media-1")
	if err != nil {
		t.Fatal(err)
	}
	if otherState.Position != 0 || otherState.Played {
		t.Fatalf("playback state leaked across users: %+v", otherState)
	}
	otherFavorite, err := repository.IsFavorite("other-user", "media-1")
	if err != nil || otherFavorite {
		t.Fatalf("favorite state leaked across users: favorite=%t err=%v", otherFavorite, err)
	}
}

func TestSQLUserRepositorySetFavoriteIsIdempotent(t *testing.T) {
	app := newTestSQLiteApp(t)
	repository := sqlUserRepository{db: app.db}
	if err := repository.SetFavorite("test-user", "media-1", true, 100); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetFavorite("test-user", "media-1", true, 200); err != nil {
		t.Fatal(err)
	}
	var createdAt int64
	if err := app.db.QueryRow("SELECT created_at FROM user_favorites WHERE user_id=? AND item=?", "test-user", "media-1").Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	if createdAt != 100 {
		t.Fatalf("repeated favorite changed creation time: %d", createdAt)
	}
	if err := repository.SetFavorite("test-user", "media-1", false, 0); err != nil {
		t.Fatal(err)
	}
	if favorite, err := repository.IsFavorite("test-user", "media-1"); err != nil || favorite {
		t.Fatalf("removed favorite state = %t, err=%v", favorite, err)
	}
}

func TestSQLUserRepositorySetPlayedTreeCascadesAndClearsResume(t *testing.T) {
	app := newTestSQLiteApp(t)
	items := []Item{
		{ID: "series-1", Lib: "lib-1", Parent: "lib-1", Name: "Series", Kind: "Series"},
		{ID: "season-1", Lib: "lib-1", Parent: "series-1", Name: "Season 1", Kind: "Season", Season: 1},
		{ID: "episode-1", Lib: "lib-1", Parent: "season-1", Name: "Episode 1", Kind: "Episode", Season: 1, Episode: 1},
		{ID: "episode-2", Lib: "lib-1", Parent: "season-1", Name: "Episode 2", Kind: "Episode", Season: 1, Episode: 2},
		{ID: "episode-special", Lib: "lib-1", Parent: "series-1", Name: "Special", Kind: "Episode"},
	}
	for _, item := range items {
		if _, err := app.db.Exec("INSERT INTO items("+cols+",seen) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'t')",
			item.ID, item.Lib, item.Parent, item.Name, item.Kind, item.Path, item.URL, item.Overview,
			item.Poster, item.Year, item.Season, item.Episode, item.Mtime, item.Size); err != nil {
			t.Fatal(err)
		}
	}
	repository := sqlUserRepository{db: app.db}
	if err := repository.SetPlayedTree("test-user", "series-1", true); err != nil {
		t.Fatal(err)
	}
	for _, itemID := range []string{"series-1", "episode-1", "episode-2", "episode-special"} {
		state, err := repository.GetPlaybackState("test-user", itemID)
		if err != nil || !state.Played || state.Position != 0 {
			t.Fatalf("series cascade failed for %s: state=%+v err=%v", itemID, state, err)
		}
	}
	seasonState, err := repository.GetPlaybackState("test-user", "season-1")
	if err != nil || seasonState.Played {
		t.Fatalf("series mark should not change the season row: state=%+v err=%v", seasonState, err)
	}

	if _, err := app.db.Exec("INSERT INTO resume_activity(user_id,item,updated) VALUES(?,?,?)", "test-user", "season-1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := app.db.Exec("INSERT INTO plays(user_id,device,item,updated) VALUES(?,?,?,?)", "test-user", "device-1", "season-1", 1); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetPlayedTree("test-user", "season-1", false); err != nil {
		t.Fatal(err)
	}
	for _, itemID := range []string{"season-1", "episode-1", "episode-2"} {
		state, err := repository.GetPlaybackState("test-user", itemID)
		if err != nil || state.Played || state.Position != 0 {
			t.Fatalf("season reset failed for %s: state=%+v err=%v", itemID, state, err)
		}
	}
	specialState, err := repository.GetPlaybackState("test-user", "episode-special")
	if err != nil || !specialState.Played {
		t.Fatalf("season reset changed direct series episode: state=%+v err=%v", specialState, err)
	}
	var resumeCount, playCount int
	if err := app.db.QueryRow("SELECT count(*) FROM resume_activity WHERE user_id=? AND item=?", "test-user", "season-1").Scan(&resumeCount); err != nil {
		t.Fatal(err)
	}
	if err := app.db.QueryRow("SELECT count(*) FROM plays WHERE user_id=? AND item=?", "test-user", "season-1").Scan(&playCount); err != nil {
		t.Fatal(err)
	}
	if resumeCount != 0 || playCount != 0 {
		t.Fatalf("unplayed cleanup failed: resume=%d plays=%d", resumeCount, playCount)
	}
}

func TestSQLUserRepositorySetPlaybackPositionPreservesPlayedState(t *testing.T) {
	app := newTestSQLiteApp(t)
	repository := sqlUserRepository{db: app.db}
	if err := repository.SetPlayedItem("test-user", "existing-item", true); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetPlaybackPosition("test-user", "existing-item", 12345); err != nil {
		t.Fatal(err)
	}
	state, err := repository.GetPlaybackState("test-user", "existing-item")
	if err != nil || !state.Played || state.Position != 12345 {
		t.Fatalf("position update changed played state: state=%+v err=%v", state, err)
	}

	if err := repository.SetPlaybackPosition("test-user", "new-item", 6789); err != nil {
		t.Fatal(err)
	}
	state, err = repository.GetPlaybackState("test-user", "new-item")
	if err != nil || state.Played || state.Position != 6789 {
		t.Fatalf("new position state is incorrect: state=%+v err=%v", state, err)
	}
}
