package strm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// runPythonReference executes the exact path-mapping and URL-encoding expressions from scripts/ref/strm.py (L49-51, L123-129, L156)
// using local python3 so we verify byte-for-byte parity on identical inputs.
func runPythonReference(t *testing.T, outputRoot, scanPath string, relFiles []string) map[string][2]string {
	t.Helper()
	pyBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found in PATH")
	}

	pyScript := `
import json, sys

payload = json.loads(sys.stdin.read())
output_root = payload["output_root"]
scan_path = payload["scan_path"]
rel_files = payload["rel_files"]

webdav_url = "http://docker:5678/dav" + scan_path + "/"
webdav_url_final = "http://xiaoya.host:5678/d" + scan_path + "/"
save_mulu = output_root + scan_path.replace("%20", " ") + "/"

out = {}
for rel in rel_files:
    b = webdav_url + rel
    prefix = ".".join(b.replace(webdav_url, "").split(".")[0:-1])
    strm_file_temp = save_mulu + prefix.replace("%20", " ")
    if len(strm_file_temp) > 235:
        strm_file = strm_file_temp[:235] + "_xxx.strm"
    else:
        strm_file = strm_file_temp + ".strm"
    url = (
        b.replace(webdav_url, webdav_url_final)
        .replace(" ", "%20")
        .replace("\\", "%5C")
        .replace("#", "%23")
        .replace("^", "%5E")
        .replace("{", "%7B")
        .replace("}", "%7D")
        .replace("?", "%3F")
        .replace("&", "%26")
        .replace("=", "%3D")
        .replace("+", "%2B")
        .replace("@", "%40")
    )
    out[rel] = [strm_file, url]

print(json.dumps(out, ensure_ascii=False))
`
	inBytes, _ := json.Marshal(map[string]any{
		"output_root": outputRoot,
		"scan_path":   scanPath,
		"rel_files":   relFiles,
	})
	cmd := exec.Command(pyBin, "-c", pyScript)
	cmd.Stdin = bytes.NewReader(inBytes)
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python3 execution failed: %v\noutput: %s", err, string(outBytes))
	}
	var parsed map[string][2]string
	if err := json.Unmarshal(outBytes, &parsed); err != nil {
		t.Fatalf("unmarshal python output: %v (%s)", err, string(outBytes))
	}
	return parsed
}

func TestPythonGoldenParityTC01ToTC07(t *testing.T) {
	tmpOut := t.TempDir()
	scanPath := "/每日更新/电视剧/国产剧"

	// Build a long path whose rune length exceeds 235 for TC-07
	longSub := strings.Repeat("超长剧集目录名称测试", 22) + "/Season 1/S01E01.mkv"

	samples := []string{
		// TC-01: Standard Chinese episode path
		"折月亮 (2026)/Season 1/01.mp4",
		// TC-02: M4V union extension (in strm.py, missing in strm2.py)
		"Movie Collection/sample.m4v",
		// TC-03: Special characters space, #, &, +, =, ?, @, ^, {tmdb-270431}
		"折月亮 (2026)/{tmdb-270431}/Season 1/EP01 #1 A&B+C=D?E@F^G.mkv",
		// TC-07: Single 235-rune truncation -> exact [:235] + "_xxx.strm"
		longSub,
	}

	pyOut := runPythonReference(t, tmpOut, scanPath, samples)
	eng := NewEngine(Config{
		OutputRoot:              tmpOut,
		PreserveSourceStructure: true,
		MaxPathLength:           235,
		AttachmentsMode:         "full",
		TargetBaseURL:           "http://xiaoya.host:5678",
	})

	for _, rel := range samples {
		sourcePath := scanPath + "/" + rel
		gotPath, gotURL, _, err := eng.GenerateSTRM(tmpOut, sourcePath, rel, "")
		if err != nil {
			t.Fatalf("GenerateSTRM(%q): %v", rel, err)
		}
		wantPath := filepath.Clean(pyOut[rel][0])
		wantURL := pyOut[rel][1]
		if gotPath != wantPath {
			t.Errorf("path mismatch for %q:\n got (%d runes): %s\nwant (%d runes): %s",
				rel, utf8.RuneCountInString(gotPath), gotPath, utf8.RuneCountInString(wantPath), wantPath)
		}
		if gotURL != wantURL {
			t.Errorf("URL mismatch for %q:\n got: %s\nwant: %s", rel, gotURL, wantURL)
		}
	}
}

func TestTC04ToTC10EdgeCasesAndEnhancements(t *testing.T) {
	tmpOut := t.TempDir()
	eng := NewEngine(Config{
		OutputRoot:              tmpOut,
		PreserveSourceStructure: true,
		MaxPathLength:           235,
		AttachmentsMode:         "full",
		TargetBaseURL:           "http://xiaoya.host:5678",
	})

	// TC-04: Pre-encoded URL (%20, %E6%8A%98) must NOT be double-encoded into %2520
	_, gotEncodedURL, _, err := eng.GenerateSTRM(
		tmpOut,
		"/每日更新/My%20Show/01.mp4",
		"My%20Show/01.mp4",
		"http://xiaoya.host:5678/d/%E6%8A%98%E6%9C%88%E4%BA%AE/My%20Show%20#1.mp4?sign=ab+cd=123:0",
	)
	if err != nil {
		t.Fatalf("TC-04 failed: %v", err)
	}
	wantEncoded := "http://xiaoya.host:5678/d/%E6%8A%98%E6%9C%88%E4%BA%AE/My%20Show%20%231.mp4?sign=ab+cd=123:0"
	if gotEncodedURL != wantEncoded {
		t.Fatalf("TC-04 double-encoding/sign bug:\n got: %s\nwant: %s", gotEncodedURL, wantEncoded)
	}

	// TC-05: Anti-.strm.strm on `E01.strm` and `01.mp4.strm`
	for _, tc := range []struct{ in, wantBase string }{
		{"/每日更新/Show/E01.strm", "E01.strm"},
		{"/每日更新/Show/01.mp4.strm", "01.strm"},
	} {
		p, _, _, gErr := eng.GenerateSTRM(tmpOut, tc.in, tc.in, "http://xiaoya.host:5678/d/Show/01.mp4")
		if gErr != nil || filepath.Base(p) != tc.wantBase || strings.HasSuffix(p, ".strm.strm") {
			t.Fatalf("TC-05 %q -> %q (err=%v), want base %q", tc.in, p, gErr, tc.wantBase)
		}
	}

	// TC-06: Dotless SQLite episode node (`E01` with `content = "http://xiaoya.host:5678/d/...?sign=ab+cd="`)
	p6, u6, _, err := eng.GenerateSTRM(
		tmpOut,
		"/每日更新/电视剧/国产剧/折月亮 (2026)/{tmdb-270431}/Season 1/E01",
		"折月亮 (2026)/{tmdb-270431}/Season 1/E01",
		"http://xiaoya.host:5678/d/每日更新/电视剧/国产剧/折月亮/01.mp4?sign=ab+cd=",
	)
	if err != nil {
		t.Fatalf("TC-06 failed: %v", err)
	}
	if filepath.Base(p6) != "E01.strm" || !strings.HasSuffix(u6, "?sign=ab+cd=") {
		t.Fatalf("TC-06 unexpected output: path=%q url=%q", p6, u6)
	}

	// TC-08: Multi-episode 235-rune collision disambiguation (zero silent overwrite!)
	sharedPrefix := "/每日更新/" + strings.Repeat("超长同名前缀剧集目录A", 20) + "/Season 1/Episode_With_Very_Long_Identical_Prefix_"
	epA, _, _, errA := eng.GenerateSTRM(tmpOut, sharedPrefix+"01.mkv", "", "http://xiaoya.host:5678/d/long/01.mkv")
	epB, _, _, errB := eng.GenerateSTRM(tmpOut, sharedPrefix+"02.mkv", "", "http://xiaoya.host:5678/d/long/02.mkv")
	if errA != nil || errB != nil || epA == epB {
		t.Fatalf("TC-08 collision not disambiguated: epA=%q epB=%q errA=%v errB=%v", epA, epB, errA, errB)
	}
	if !strings.HasSuffix(epA, "_xxx.strm") {
		t.Fatalf("TC-08 first truncated file should keep Python-compatible _xxx.strm suffix, got %q", epA)
	}

	// TC-09: >5 directory levels (8 levels deep)
	deepSource := "/L1/L2/L3/L4/L5/L6/L7/L8/DeepEpisode.mkv"
	p9, _, _, err := eng.GenerateSTRM(tmpOut, deepSource, deepSource, "http://xiaoya.host:5678/d"+deepSource)
	if err != nil || !strings.HasSuffix(p9, "/L1/L2/L3/L4/L5/L6/L7/L8/DeepEpisode.strm") {
		t.Fatalf("TC-09 deep hierarchy failed: path=%q err=%v", p9, err)
	}

	// TC-10: Idempotent write preserves mtime when content unchanged, atomic update when URL changes
	st1, _ := os.Stat(p9)
	oldMtime := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	_ = os.Chtimes(p9, oldMtime, oldMtime)
	stBefore, _ := os.Stat(p9)

	_, _, changed2, err := eng.GenerateSTRM(tmpOut, deepSource, deepSource, "http://xiaoya.host:5678/d"+deepSource)
	stAfterSame, _ := os.Stat(p9)
	if err != nil || changed2 || !stAfterSame.ModTime().Equal(stBefore.ModTime()) {
		t.Fatalf("TC-10 expected unchanged write to skip rewrite and preserve mtime (changed=%v, before=%v, after=%v, orig=%v)", changed2, stBefore.ModTime(), stAfterSame.ModTime(), st1.ModTime())
	}

	_, _, changed3, err := eng.GenerateSTRM(tmpOut, deepSource, deepSource, "http://xiaoya.host:5678/d"+deepSource+"?sign=UPDATED:0")
	rawUpdated, _ := os.ReadFile(p9)
	if err != nil || !changed3 || !strings.HasSuffix(string(rawUpdated), "?sign=UPDATED:0") {
		t.Fatalf("TC-10 expected modified URL to update STRM atomically: changed=%v content=%q err=%v", changed3, string(rawUpdated), err)
	}
}

func TestTC11AttachmentSyncAndEmptyNFORejection(t *testing.T) {
	tmpOut := t.TempDir()
	files := map[string]struct {
		status int
		body   string
	}{
		"/d/Show/tvshow.nfo":   {200, `<?xml version="1.0"?><tvshow><title>折月亮</title></tvshow>`},
		"/d/Show/empty.nfo":    {200, `   `},
		"/d/Show/error.nfo":    {200, `{"code":500,"message":"internal error"}`},
		"/d/Show/E01.chs.ass":  {200, "[Script Info]\nTitle: E01\n"},
		"/d/Show/poster.jpg":   {200, "FAKE_JPEG_BYTES"},
		"/d/Show/fanart.webp":  {200, "FAKE_WEBP_BYTES"},
		"/d/Show/missing.srt":  {500, "upstream failure"},
	}

	mockHTTP := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			entry, ok := files[r.URL.Path]
			if !ok {
				return &http.Response{
					StatusCode: 404,
					Body:       io.NopCloser(strings.NewReader("not found")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: entry.status,
				Body:       io.NopCloser(strings.NewReader(entry.body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	eng := NewEngine(Config{
		OutputRoot:              tmpOut,
		PreserveSourceStructure: true,
		AttachmentsMode:         "full",
		TargetBaseURL:           "http://xiaoya.host:5678",
		DownloadBaseURL:         "http://mock-alist:5678",
		HTTPClient:              mockHTTP,
	})

	ctx := context.Background()
	// Valid NFO, subtitle, JPG, WEBP must be written
	for _, tc := range []struct {
		src string
		cat NodeCategory
	}{
		{"/Show/tvshow.nfo", CategoryNFO},
		{"/Show/E01.chs.ass", CategorySubtitle},
		{"/Show/poster.jpg", CategoryImage},
		{"/Show/fanart.webp", CategoryImage},
	} {
		localPath, err := eng.SyncAttachment(ctx, "res-1", tmpOut, tc.src, tc.src, "", tc.cat)
		if err != nil {
			t.Fatalf("SyncAttachment(%s) failed: %v", tc.src, err)
		}
		if st, sErr := os.Stat(localPath); sErr != nil || st.Size() == 0 {
			t.Fatalf("expected non-empty attachment at %s", localPath)
		}
	}

	// Empty NFO, AList JSON error NFO, and HTTP 500 subtitle MUST be rejected and NEVER leave empty files on disk
	for _, bad := range []struct {
		src string
		cat NodeCategory
	}{
		{"/Show/empty.nfo", CategoryNFO},
		{"/Show/error.nfo", CategoryNFO},
		{"/Show/missing.srt", CategorySubtitle},
	} {
		localPath, err := eng.SyncAttachment(ctx, "res-bad", tmpOut, bad.src, bad.src, "", bad.cat)
		if err == nil {
			t.Fatalf("expected SyncAttachment(%s) to fail", bad.src)
		}
		if _, sErr := os.Stat(localPath); !os.IsNotExist(sErr) {
			t.Fatalf("rejected attachment %s must NOT create file on disk", localPath)
		}
	}
}
