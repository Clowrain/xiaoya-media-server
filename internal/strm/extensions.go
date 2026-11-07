package strm

import (
	"path/filepath"
	"strings"
)

type NodeCategory int

const (
	CategoryIgnored NodeCategory = iota
	CategoryMedia
	CategorySubtitle
	CategoryImage
	CategoryNFO
)

// Video (12) + Audio (9) = 21 media extensions from strm.py U strm2.py.
var mediaExtensions = map[string]bool{
	"MP4":  true,
	"M4V":  true, // Present in strm.py, missing in strm2.py -> included in union
	"MKV":  true,
	"ISO":  true,
	"FLV":  true,
	"AVI":  true,
	"TS":   true,
	"WMV":  true,
	"MOV":  true,
	"RM":   true,
	"RMVB": true,
	"WEBM": true,
	"WAV":  true,
	"MP3":  true,
	"FLAC": true,
	"APE":  true,
	"WV":   true,
	"ALAC": true,
	"M4A":  true,
	"AAC":  true,
	"WMA":  true,
}

// 6 subtitle extensions shared by strm.py and strm2.py.
var subtitleExtensions = map[string]bool{
	"ASS": true,
	"SRT": true,
	"SSA": true,
	"VTT": true,
	"IDX": true,
	"SUB": true,
}

// Image extensions from strm.py (+ JPEG alias).
var imageExtensions = map[string]bool{
	"JPG":  true,
	"JPEG": true,
	"PNG":  true,
	"WEBP": true,
}

func IsMediaExtension(ext string) bool {
	clean := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	return mediaExtensions[clean]
}

func IsSubtitleExtension(ext string) bool {
	clean := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	return subtitleExtensions[clean]
}

func IsImageExtension(ext string) bool {
	clean := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	return imageExtensions[clean]
}

// ClassifyFile determines how a file node should be handled according to `attachmentMode` ("full" or "subtitles" or "none").
// Supports dotless episode nodes (e.g. `E01` in Xiaoya SQLite) when `sourceContent` contains an HTTP(S) URL.
func ClassifyFile(name, sourceContent, attachmentMode string) NodeCategory {
	ext := strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), "."))
	mode := strings.ToLower(strings.TrimSpace(attachmentMode))
	if mode == "" {
		mode = "full"
	}

	if ext == "STRM" || mediaExtensions[ext] {
		return CategoryMedia
	}
	if subtitleExtensions[ext] {
		if mode == "none" {
			return CategoryIgnored
		}
		return CategorySubtitle
	}
	if imageExtensions[ext] {
		if mode == "full" {
			return CategoryImage
		}
		return CategoryIgnored
	}
	if ext == "NFO" {
		if mode == "full" {
			return CategoryNFO
		}
		return CategoryIgnored
	}

	// Dotless file nodes in Xiaoya SQLite (such as "E01".."E27" in 折月亮) carry a playback URL in `content`.
	if ext == "" {
		trimmed := strings.TrimSpace(sourceContent)
		if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
			return CategoryMedia
		}
	}
	return CategoryIgnored
}
