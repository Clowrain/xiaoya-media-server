package embyserver

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var externalSubtitleExts = map[string]struct {
	codec  string
	isText bool
}{
	".srt": {codec: "subrip", isText: true},
	".vtt": {codec: "vtt", isText: true},
	".ass": {codec: "ass", isText: true},
	".ssa": {codec: "ssa", isText: true},
	".sub": {codec: "sub", isText: false},
	".idx": {codec: "vobsub", isText: false},
}

type externalSubtitle struct {
	ExtIndex     int
	Path         string
	Ext          string
	Codec        string
	Language     string
	Title        string
	DisplayTitle string
	IsDefault    bool
	IsForced     bool
	IsText       bool
}

var srtTimestampLineRe = regexp.MustCompile(`^(\d{1,2}:\d{2}:\d{2}|\d{2}:\d{2}),(\d{3})\s*-->\s*(\d{1,2}:\d{2}:\d{2}|\d{2}:\d{2}),(\d{3})(.*)$`)

func mediaBaseStems(mediaPath string) []string {
	base := filepath.Base(mediaPath)
	ext := filepath.Ext(base)
	stem1 := strings.TrimSuffix(base, ext)
	stems := []string{strings.ToLower(stem1)}
	// Also strip inner media extension if file is named like "E01.mp4.strm"
	if innerExt := filepath.Ext(stem1); innerExt != "" && scannableMediaExts[strings.ToLower(innerExt)] {
		stem2 := strings.TrimSuffix(stem1, innerExt)
		if stem2 != "" {
			stems = append(stems, strings.ToLower(stem2))
		}
	}
	return stems
}

func matchesMediaStem(subFilename string, mediaStems []string, singleMediaDir bool) (suffix string, ok bool) {
	ext := filepath.Ext(subFilename)
	subStem := strings.TrimSuffix(subFilename, ext)
	lowerSub := strings.ToLower(subStem)
	for _, ms := range mediaStems {
		if ms == "" {
			continue
		}
		if lowerSub == ms {
			return "", true
		}
		if strings.HasPrefix(lowerSub, ms) {
			rest := subStem[len(ms):]
			if len(rest) > 0 && (rest[0] == '.' || rest[0] == '_' || rest[0] == '-' || rest[0] == ' ') {
				return strings.TrimLeft(rest, "._- "), true
			}
		}
	}
	if singleMediaDir {
		return subStem, true
	}
	return "", false
}

func parseSubtitleTags(rawSuffix, codec string) (lang, title, displayTitle string, isDefault, isForced bool) {
	tokens := strings.FieldsFunc(rawSuffix, func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == ' '
	})
	label := ""
	for _, tok := range tokens {
		lt := strings.ToLower(strings.TrimSpace(tok))
		switch lt {
		case "default":
			isDefault = true
		case "forced", "force":
			isForced = true
		case "chs", "sc", "zh", "zho", "chi", "cn", "zh-cn", "zh-hans", "简", "简体", "简中", "中字":
			if lang == "" {
				lang = "zh-CN"
				label = "简中"
			}
		case "cht", "tc", "tw", "hk", "zh-tw", "zh-hk", "zh-hant", "繁", "繁体", "繁中":
			if lang == "" {
				lang = "zh-TW"
				label = "繁中"
			}
		case "ch", "chs&eng", "zhongying", "双语", "中英":
			lang = "zh-CN"
			label = "中英双语"
		case "en", "eng", "english":
			if lang == "" {
				lang = "en"
				label = "English"
			}
		case "ja", "jpn", "jp", "japanese":
			if lang == "" {
				lang = "ja"
				label = "日本語"
			}
		case "ko", "kor", "kr", "korean":
			if lang == "" {
				lang = "ko"
				label = "한국어"
			}
		}
	}
	if lang == "" {
		lang = "und"
	}
	title = strings.TrimSpace(rawSuffix)
	parts := []string{}
	if label != "" {
		parts = append(parts, label)
	} else if title != "" {
		parts = append(parts, title)
	} else {
		parts = append(parts, "外挂字幕")
	}
	parts = append(parts, strings.ToUpper(codec))
	if isForced {
		parts = append(parts, "Forced")
	}
	displayTitle = strings.Join(parts, " · ")
	return
}

// discoverExternalSubtitles scans the media item's directory (and Subs/Subtitles child dirs)
// for companion subtitle files (.srt, .ass, .ssa, .vtt, .sub, .idx).
func discoverExternalSubtitles(x Item) []externalSubtitle {
	if x.Path == "" || (x.Kind != "Movie" && x.Kind != "Episode" && x.Kind != "Video") {
		return nil
	}
	dir := filepath.Dir(x.Path)
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil || !allowedMediaPath(realDir) {
		return nil
	}

	entries, err := os.ReadDir(realDir)
	if err != nil {
		return nil
	}
	mediaCount := 0
	subDirs := []string{realDir}
	for _, ent := range entries {
		if ent.IsDir() {
			dn := strings.ToLower(ent.Name())
			if dn == "subs" || dn == "sub" || dn == "subtitles" || dn == "字幕" {
				if subReal, sErr := filepath.EvalSymlinks(filepath.Join(realDir, ent.Name())); sErr == nil && allowedMediaPath(subReal) {
					subDirs = append(subDirs, subReal)
				}
			}
			continue
		}
		if isScannableMediaFile(ent.Name()) {
			mediaCount++
		}
	}
	singleMediaDir := mediaCount <= 1 && x.Kind == "Movie"
	mediaStems := mediaBaseStems(x.Path)

	type candidate struct {
		path   string
		name   string
		ext    string
		codec  string
		isText bool
		suffix string
	}
	var cands []candidate
	for _, scanDir := range subDirs {
		dEntries, dErr := os.ReadDir(scanDir)
		if dErr != nil {
			continue
		}
		for _, ent := range dEntries {
			if ent.IsDir() {
				continue
			}
			ext := strings.ToLower(filepath.Ext(ent.Name()))
			spec, ok := externalSubtitleExts[ext]
			if !ok {
				continue
			}
			suffix, matched := matchesMediaStem(ent.Name(), mediaStems, singleMediaDir)
			if !matched {
				continue
			}
			full := filepath.Join(scanDir, ent.Name())
			realFile, fErr := filepath.EvalSymlinks(full)
			if fErr != nil || !allowedMediaPath(realFile) {
				continue
			}
			st, stErr := os.Stat(realFile)
			if stErr != nil || !st.Mode().IsRegular() || st.Size() == 0 {
				continue
			}
			cands = append(cands, candidate{
				path:   realFile,
				name:   ent.Name(),
				ext:    ext,
				codec:  spec.codec,
				isText: spec.isText,
				suffix: suffix,
			})
		}
	}

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].name != cands[j].name {
			return cands[i].name < cands[j].name
		}
		return cands[i].path < cands[j].path
	})

	out := make([]externalSubtitle, 0, len(cands))
	for idx, c := range cands {
		lang, title, display, isDef, isForced := parseSubtitleTags(c.suffix, c.codec)
		out = append(out, externalSubtitle{
			ExtIndex:     idx,
			Path:         c.path,
			Ext:          strings.TrimPrefix(c.ext, "."),
			Codec:        c.codec,
			Language:     lang,
			Title:        title,
			DisplayTitle: display,
			IsDefault:    isDef,
			IsForced:     isForced,
			IsText:       c.isText,
		})
	}
	return out
}

func appendExternalSubtitles(x Item, dst M, tok string) {
	subs := discoverExternalSubtitles(x)
	if len(subs) == 0 {
		return
	}
	var streams []M
	switch cur := dst["MediaStreams"].(type) {
	case []M:
		// Filter out any previously appended external streams to avoid duplicates
		for _, s := range cur {
			if s["IsExternal"] == true {
				continue
			}
			streams = append(streams, s)
		}
	default:
		streams = []M{}
	}

	baseIdx := len(streams)
	for i, sub := range subs {
		streamIdx := baseIdx + i
		deliveryPath := "/Videos/" + x.ID + "/" + x.ID + "/Subtitles/" + strconv.Itoa(streamIdx) + "/Stream." + sub.Ext
		if tok != "" {
			deliveryPath += "?api_key=" + url.QueryEscape(tok)
		}
		entry := M{
			"Type":                   "Subtitle",
			"Index":                  streamIdx,
			"Codec":                  sub.Codec,
			"Language":               sub.Language,
			"Title":                  sub.Title,
			"DisplayTitle":           sub.DisplayTitle,
			"IsExternal":             true,
			"IsTextSubtitleStream":   sub.IsText,
			"SupportsExternalStream": true,
			"IsDefault":              sub.IsDefault,
			"IsForced":               sub.IsForced,
			"DeliveryMethod":         "External",
			"DeliveryUrl":            deliveryPath,
		}
		streams = append(streams, entry)
	}
	dst["MediaStreams"] = streams
}

// srtToWebVTT converts UTF-8 SubRip (.srt) subtitle bytes into WebVTT (.vtt).
func srtToWebVTT(srt []byte) []byte {
	srt = bytes.TrimPrefix(srt, []byte("\xef\xbb\xbf"))
	text := strings.ReplaceAll(string(srt), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")

	var buf strings.Builder
	buf.Grow(len(text) + 16)
	buf.WriteString("WEBVTT\n\n")
	for _, line := range lines {
		if m := srtTimestampLineRe.FindStringSubmatch(line); len(m) == 6 {
			buf.WriteString(m[1])
			buf.WriteByte('.')
			buf.WriteString(m[2])
			buf.WriteString(" --> ")
			buf.WriteString(m[3])
			buf.WriteByte('.')
			buf.WriteString(m[4])
			buf.WriteString(m[5])
			buf.WriteByte('\n')
		} else {
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	return []byte(buf.String())
}

func subtitleContentType(ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "vtt", "webvtt":
		return "text/vtt; charset=utf-8"
	case "srt":
		return "application/x-subrip; charset=utf-8"
	case "ass", "ssa":
		return "text/x-ssa; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// serveVideoSubtitles handles:
//   GET/HEAD /Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/Stream.{ext}
//   GET/HEAD /Videos/{itemId}/{mediaSourceId}/Subtitles/{index}/{offsetTicks}/Stream.{ext}
//   GET/HEAD /Videos/{itemId}/Subtitles/{index}/Stream.{ext}
func (a *App) serveVideoSubtitles(w http.ResponseWriter, r *http.Request, parts []string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, http.StatusMethodNotAllowed, "GET or HEAD required")
		return
	}
	// parts[0] == "", parts[1] == "Videos", parts[2] == itemId
	if len(parts) < 6 {
		fail(w, http.StatusNotFound, "字幕未找到")
		return
	}
	itemID := parts[2]
	subKeywordPos := -1
	for idx := 3; idx < len(parts); idx++ {
		if strings.EqualFold(parts[idx], "Subtitles") {
			subKeywordPos = idx
			break
		}
	}
	if subKeywordPos < 0 || subKeywordPos+2 >= len(parts) {
		fail(w, http.StatusNotFound, "字幕路由无效")
		return
	}
	reqIndex, err := strconv.Atoi(parts[subKeywordPos+1])
	if err != nil || reqIndex < 0 {
		fail(w, http.StatusBadRequest, "无效字幕索引")
		return
	}
	lastSeg := parts[len(parts)-1]
	reqExt := strings.ToLower(strings.TrimPrefix(filepath.Ext(lastSeg), "."))

	// If mediaSourceId points to a specific merged version item, prefer that item's directory.
	targetItemID := itemID
	if subKeywordPos == 4 && parts[3] != "" {
		if _, vErr := a.item(parts[3]); vErr == nil {
			targetItemID = parts[3]
		}
	}
	x, err := a.item(targetItemID)
	if err != nil {
		fail(w, http.StatusNotFound, "媒体不存在")
		return
	}

	subs := discoverExternalSubtitles(x)
	if len(subs) == 0 {
		fail(w, http.StatusNotFound, "无外挂字幕")
		return
	}

	// Calculate embedded stream offset so reqIndex matches MediaStreams[i].Index,
	// while also falling back to 0-based external subtitle index if needed.
	meta := M{}
	a.enrichMedia(x, a.metadata(x), meta)
	embeddedCount := 0
	if cur, ok := meta["MediaStreams"].([]M); ok {
		for _, s := range cur {
			if s["IsExternal"] != true {
				embeddedCount++
			}
		}
	}
	var chosen *externalSubtitle
	extIdx := reqIndex - embeddedCount
	if extIdx >= 0 && extIdx < len(subs) {
		chosen = &subs[extIdx]
	} else if reqIndex >= 0 && reqIndex < len(subs) {
		chosen = &subs[reqIndex]
	}
	if chosen == nil {
		fail(w, http.StatusNotFound, "字幕索引超出范围")
		return
	}

	real, err := filepath.EvalSymlinks(chosen.Path)
	if err != nil || !allowedMediaPath(real) {
		fail(w, http.StatusForbidden, "字幕路径未授权")
		return
	}
	st, err := os.Stat(real)
	if err != nil || !st.Mode().IsRegular() {
		fail(w, http.StatusNotFound, "字幕文件不存在")
		return
	}

	// Convert .srt -> WebVTT dynamically when player requests .vtt / .webvtt
	if chosen.Ext == "srt" && (reqExt == "vtt" || reqExt == "webvtt") {
		f, oErr := os.Open(real)
		if oErr != nil {
			fail(w, http.StatusInternalServerError, "读取字幕失败")
			return
		}
		raw, rErr := io.ReadAll(io.LimitReader(f, 8<<20))
		f.Close()
		if rErr != nil {
			fail(w, http.StatusInternalServerError, "读取字幕失败")
			return
		}
		vtt := srtToWebVTT(raw)
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		http.ServeContent(w, r, strings.TrimSuffix(filepath.Base(real), filepath.Ext(real))+".vtt", st.ModTime(), bytes.NewReader(vtt))
		return
	}

	f, err := os.Open(real)
	if err != nil {
		fail(w, http.StatusInternalServerError, "打开字幕失败")
		return
	}
	defer f.Close()
	outExt := chosen.Ext
	if reqExt != "" {
		outExt = reqExt
	}
	w.Header().Set("Content-Type", subtitleContentType(outExt))
	http.ServeContent(w, r, filepath.Base(real), st.ModTime(), f)
}

func localMediaContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".avi":
		return "video/x-msvideo"
	case ".ts":
		return "video/mp2t"
	case ".flv":
		return "video/x-flv"
	case ".wmv":
		return "video/x-ms-wmv"
	case ".mp3":
		return "audio/mpeg"
	case ".flac":
		return "audio/flac"
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".wav":
		return "audio/wav"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

// serveLocalMediaStream serves local file:// media items directly with full HTTP Range and HEAD support.
func serveLocalMediaStream(w http.ResponseWriter, r *http.Request, fileURL string) {
	rawPath := strings.TrimPrefix(fileURL, "file://")
	if rawPath == "" {
		fail(w, http.StatusNotFound, "本地媒体路径为空")
		return
	}
	real, err := filepath.EvalSymlinks(rawPath)
	if err != nil {
		if os.IsNotExist(err) {
			fail(w, http.StatusNotFound, "本地媒体文件不存在")
			return
		}
		fail(w, http.StatusForbidden, "无法解析本地媒体路径")
		return
	}
	if !allowedMediaPath(real) {
		fail(w, http.StatusForbidden, "本地媒体路径不在允许根目录内")
		return
	}
	st, err := os.Stat(real)
	if err != nil || !st.Mode().IsRegular() {
		fail(w, http.StatusNotFound, "本地媒体不可读")
		return
	}
	f, err := os.Open(real)
	if err != nil {
		fail(w, http.StatusInternalServerError, "打开媒体失败")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", localMediaContentType(real))
	http.ServeContent(w, r, filepath.Base(real), st.ModTime().UTC().Truncate(time.Second), f)
}
