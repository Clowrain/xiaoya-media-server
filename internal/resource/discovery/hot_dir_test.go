package discovery

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestHotDirectoryBackoff(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	store := NewSQLStore(db, "sqlite")
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	rootID := "root-1"
	dirPath := "/每日更新/电视剧/折月亮"

	// 1. Initial touch with changes: 30 minutes (1800s)
	if err := store.TouchHotDirectory(ctx, rootID, dirPath, 12345, true); err != nil {
		t.Fatalf("initial touch: %v", err)
	}
	hotID := StableID("hot:" + rootID + ":" + dirPath)
	h, err := store.GetHotDirectory(ctx, hotID)
	if err != nil {
		t.Fatalf("get hot directory: %v", err)
	}
	if h.IntervalSec != 1800 || h.ConsecutiveUnchanged != 0 {
		t.Fatalf("expected initial 1800s with 0 consecutive, got interval=%d consecutive=%d", h.IntervalSec, h.ConsecutiveUnchanged)
	}

	// 2. Scan yields no changes: backoff to 60 minutes (3600s)
	if err := store.TouchHotDirectory(ctx, rootID, dirPath, 12345, false); err != nil {
		t.Fatalf("touch 1 unchanged: %v", err)
	}
	h, _ = store.GetHotDirectory(ctx, hotID)
	if h.IntervalSec != 3600 || h.ConsecutiveUnchanged != 1 {
		t.Fatalf("expected 3600s with 1 consecutive, got interval=%d consecutive=%d", h.IntervalSec, h.ConsecutiveUnchanged)
	}

	// 3. Scan yields no changes again: backoff to 120 minutes (7200s)
	if err := store.TouchHotDirectory(ctx, rootID, dirPath, 12345, false); err != nil {
		t.Fatalf("touch 2 unchanged: %v", err)
	}
	h, _ = store.GetHotDirectory(ctx, hotID)
	if h.IntervalSec != 7200 || h.ConsecutiveUnchanged != 2 {
		t.Fatalf("expected 7200s with 2 consecutive, got interval=%d consecutive=%d", h.IntervalSec, h.ConsecutiveUnchanged)
	}

	// 4. Scan yields no changes 3rd time: capped at 360 minutes (21600s / 6 hours)
	if err := store.TouchHotDirectory(ctx, rootID, dirPath, 12345, false); err != nil {
		t.Fatalf("touch 3 unchanged: %v", err)
	}
	h, _ = store.GetHotDirectory(ctx, hotID)
	if h.IntervalSec != 21600 || h.ConsecutiveUnchanged != 3 {
		t.Fatalf("expected 21600s max backoff, got interval=%d consecutive=%d", h.IntervalSec, h.ConsecutiveUnchanged)
	}

	// 5. New episode added: resets back to 30 minutes (1800s)
	if err := store.TouchHotDirectory(ctx, rootID, dirPath, 12345, true); err != nil {
		t.Fatalf("touch reset changes: %v", err)
	}
	h, _ = store.GetHotDirectory(ctx, hotID)
	if h.IntervalSec != 1800 || h.ConsecutiveUnchanged != 0 {
		t.Fatalf("expected reset to 1800s, got interval=%d consecutive=%d", h.IntervalSec, h.ConsecutiveUnchanged)
	}

	// 6. Test GetDueHotDirectories
	future := time.Now().Unix() + 2000
	dueList, err := store.GetDueHotDirectories(ctx, future, 10)
	if err != nil {
		t.Fatalf("get due hot dirs: %v", err)
	}
	if len(dueList) == 0 || dueList[0].ID != hotID {
		t.Fatalf("expected hot directory in due list, got %v", dueList)
	}
}
