package embyserver

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWatchSuppressor(t *testing.T) {
	s := &watchSuppressor{}
	dir := "/media/strm/电视剧/庆余年"
	file := filepath.Join(dir, "poster.jpg")
	subFile := filepath.Join(dir, "Season 1", "01.nfo")
	otherDir := "/media/strm/电影/流浪地球"

	if s.isSuppressed(file) {
		t.Fatal("expected unsuppressed before suppress call")
	}

	// 1. 测试目录级抑制能够向下覆盖子文件与子目录
	s.suppress(dir, 100*time.Millisecond)
	if !s.isSuppressed(file) {
		t.Fatal("expected file to be suppressed via parent dir")
	}
	if !s.isSuppressed(subFile) {
		t.Fatal("expected subFile to be suppressed via ancestor dir")
	}
	if s.isSuppressed(otherDir) {
		t.Fatal("otherDir should not be suppressed")
	}

	// 2. 测试过期自动失效
	time.Sleep(120 * time.Millisecond)
	if s.isSuppressed(file) {
		t.Fatal("expected suppression to expire")
	}

	// 3. 测试 cleanup
	s.cleanup()
	if len(s.entries) != 0 {
		t.Fatalf("expected 0 entries after cleanup, got %d", len(s.entries))
	}
}

func TestWatcherMediaFileFilter(t *testing.T) {
	// 验证可扫描媒体文件与应被忽略的文件的判定
	mediaFiles := []string{
		"movie.strm",
		"ep01.mp4",
		"video.mkv",
		"disc.iso",
		"clip.ts",
	}
	for _, f := range mediaFiles {
		if !isScannableMediaFile(f) {
			t.Errorf("expected %s to be scannable media file", f)
		}
	}

	ignoredFiles := []string{
		"poster.jpg",
		"folder.png",
		"fanart.webp",
		"cover.jpeg",
		"tvshow.nfo",
		"season.nfo",
		"movie.nfo",
		"meta.xml",
		"subtitles.srt",
		"audio.ass",
		"notes.txt",
		".DS_Store",
		".download.tmp",
		"file.part",
	}
	for _, f := range ignoredFiles {
		if isScannableMediaFile(f) {
			t.Errorf("expected %s to be ignored (non-media)", f)
		}
	}
}
