package strm

import (
	"fmt"
	"net/url"
	"strings"
)

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// EncodePathLikePython reproduces the exact 11-character encoding table from strm.py/strm2.py:
//
//	' ' -> %20, '\\' -> %5C, '#' -> %23, '^' -> %5E, '{' -> %7B, '}' -> %7D,
//	'?' -> %3F, '&'  -> %26, '=' -> %3D, '+' -> %2B, '@' -> %40
//
// while preserving UTF-8 Chinese characters and preventing double-encoding of existing %XX sequences.
func EncodePathLikePython(rawPath string) string {
	var out strings.Builder
	out.Grow(len(rawPath) + 16)

	for i := 0; i < len(rawPath); i++ {
		ch := rawPath[i]
		if ch == '%' && i+2 < len(rawPath) && isHexDigit(rawPath[i+1]) && isHexDigit(rawPath[i+2]) {
			out.WriteByte('%')
			out.WriteByte(rawPath[i+1])
			out.WriteByte(rawPath[i+2])
			i += 2
			continue
		}
		switch ch {
		case ' ':
			out.WriteString("%20")
		case '\\':
			out.WriteString("%5C")
		case '#':
			out.WriteString("%23")
		case '^':
			out.WriteString("%5E")
		case '{':
			out.WriteString("%7B")
		case '}':
			out.WriteString("%7D")
		case '?':
			out.WriteString("%3F")
		case '&':
			out.WriteString("%26")
		case '=':
			out.WriteString("%3D")
		case '+':
			out.WriteString("%2B")
		case '@':
			out.WriteString("%40")
		default:
			out.WriteByte(ch)
		}
	}
	return out.String()
}

// looksLikeURLQueryString checks whether the tail after '?' is a genuine HTTP query string
// (such as `sign=xxx:0` or `sign=ab+cd=&t=123`) rather than a '?' inside a media filename.
func looksLikeURLQueryString(q string) bool {
	if q == "" {
		return false
	}
	eq := strings.IndexByte(q, '=')
	if eq <= 0 {
		return false
	}
	firstKey := q[:eq]
	if amp := strings.IndexByte(firstKey, '&'); amp >= 0 {
		return false
	}
	for i := 0; i < len(firstKey); i++ {
		c := firstKey[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// StripTransientQuery strips transient query strings (such as ?sign=... or &sign=...) from a URL or file content.
func StripTransientQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if qIdx := strings.IndexByte(raw, '?'); qIdx >= 0 {
		return raw[:qIdx]
	}
	return raw
}

func splitSchemeHostAndRest(rawURL string) (schemeHost, rest string, ok bool) {
	idx := strings.Index(rawURL, "://")
	if idx <= 0 {
		return "", "", false
	}
	afterScheme := rawURL[idx+3:]
	slashIdx := strings.IndexByte(afterScheme, '/')
	if slashIdx < 0 {
		return rawURL, "/", true
	}
	hostEnd := idx + 3 + slashIdx
	return rawURL[:hostEnd], rawURL[hostEnd:], true
}

func shouldReplaceOriginHost(schemeHost string) bool {
	u, err := url.Parse(schemeHost)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "xiaoya.host" || h == "xiaoya" || h == "127.0.0.1" || h == "localhost" || h == "172.17.0.1" {
		return true
	}
	// Docker internal bridge IPs (172.16.x - 172.31.x) or private LAN IPs
	if strings.HasPrefix(h, "172.") || strings.HasPrefix(h, "10.") || strings.HasPrefix(h, "192.168.") {
		return true
	}
	return false
}

// BuildSTRMURL constructs or normalizes the playback URL written into a `.strm` file.
//   - If `sourceContent` is an HTTP(S) URL (from Xiaoya SQLite `nodes.content`), it preserves `?sign=...`
//     query strings intact, converts `/dav/` to `/d/`, avoids `%XX` double-encoding, and encodes the 11 special characters in the path.
//   - Otherwise, it builds `<targetBaseURL>/d/<sourcePath>` and encodes the 11 special characters identically to `strm.py`.
func BuildSTRMURL(sourcePath, sourceContent, targetBaseURL string, forceReplaceHost bool) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(targetBaseURL), "/")
	if base == "" {
		base = "http://xiaoya.host:5678"
	}

	trimmedContent := strings.TrimSpace(sourceContent)
	if strings.HasPrefix(trimmedContent, "http://") || strings.HasPrefix(trimmedContent, "https://") {
		schemeHost, rest, ok := splitSchemeHostAndRest(trimmedContent)
		if !ok {
			return "", fmt.Errorf("invalid source content URL: %q", trimmedContent)
		}
		if forceReplaceHost || shouldReplaceOriginHost(schemeHost) {
			schemeHost = base
		}

		pathPart := rest
		queryPart := ""
		if qIdx := strings.LastIndexByte(rest, '?'); qIdx >= 0 {
			candQuery := rest[qIdx+1:]
			if looksLikeURLQueryString(candQuery) {
				pathPart = rest[:qIdx]
				queryPart = candQuery
			}
		}

		if strings.HasPrefix(pathPart, "/dav/") {
			pathPart = "/d/" + strings.TrimPrefix(pathPart, "/dav/")
		}
		encodedPath := EncodePathLikePython(pathPart)
		if queryPart != "" {
			return schemeHost + encodedPath + "?" + queryPart, nil
		}
		return schemeHost + encodedPath, nil
	}

	cleanSource := strings.TrimSpace(sourcePath)
	if cleanSource == "" {
		return "", fmt.Errorf("both sourceContent and sourcePath are empty")
	}
	if !strings.HasPrefix(cleanSource, "/") {
		cleanSource = "/" + cleanSource
	}
	if strings.HasPrefix(cleanSource, "/dav/") {
		cleanSource = "/d/" + strings.TrimPrefix(cleanSource, "/dav/")
	} else if !strings.HasPrefix(cleanSource, "/d/") {
		cleanSource = "/d" + cleanSource
	}
	return base + EncodePathLikePython(cleanSource), nil
}
