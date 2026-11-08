package embyserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var xiaoyaClient = &http.Client{
	Timeout: 40 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var errXiaoyaRedirectLoop = errors.New("resolver redirect loop detected")

// Resolve the original STRM, never the viewer-facing authenticated playback URL.
func xiaoyaSource(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if (h == "xiaoya.host" || h == "xiaoya" || h == "172.17.0.1" || h == "127.0.0.1" || h == "localhost") && u.Port() == "5678" {
		return true
	}
	for _, envKey := range []string{"XIAOYA_URL", "STRM_TARGET_BASE_URL"} {
		if cfgBase := strings.TrimSpace(os.Getenv(envKey)); cfgBase != "" {
			if cu, cErr := url.Parse(cfgBase); cErr == nil && cu.Host != "" && strings.EqualFold(u.Host, cu.Host) {
				return true
			}
		}
	}
	return false
}

func shouldRewriteInternalHost(h string, ip net.IP, target *url.URL) bool {
	if target == nil || target.Host == "" {
		return false
	}
	if h == "localhost" || h == "xiaoya.host" || h == "xiaoya" {
		return true
	}
	if ip != nil && ip.IsLoopback() {
		return true
	}
	// If the IP is in Docker bridge range (172.16.0.0/12) and target is not in that range (e.g. target is LAN IP 192.168.x.x or domain)
	if ip != nil {
		if v4 := ip.To4(); v4 != nil && v4[0] == 172 && (v4[1] >= 16 && v4[1] <= 31) {
			targetIP := net.ParseIP(target.Hostname())
			if targetIP == nil || targetIP.To4() == nil || targetIP.To4()[0] != 172 {
				return true
			}
		}
	}
	return false
}

func validateResolvedLink(link string, target *url.URL) (string, error) {
	link = strings.TrimSpace(link)
	if link == "" || strings.ContainsAny(link, "\r\n") {
		return "", fmt.Errorf("resolver did not return an HTTP link")
	}
	u, err := url.Parse(link)
	if err != nil || u.User != nil {
		return "", fmt.Errorf("resolver did not return an HTTP link")
	}

	// Resolve relative redirects against target (must start with / e.g. /p/...)
	if !u.IsAbs() || u.Host == "" {
		if strings.HasPrefix(link, "/") && target != nil && target.Host != "" {
			u = target.ResolveReference(u)
		} else {
			return "", fmt.Errorf("resolver did not return an HTTP link")
		}
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("resolver did not return an HTTP link")
	}

	h := strings.ToLower(u.Hostname())
	ip := net.ParseIP(h)

	// Block cloud metadata services and unspecified IPs (SSRF protection)
	if h == "metadata.google.internal" || h == "instance-data" || (ip != nil && (ip.IsUnspecified() || ip.IsLinkLocalUnicast())) {
		return "", fmt.Errorf("resolver returned an internal address")
	}

	// If resolved URL points to container loopback, internal docker host, or docker bridge network,
	// LAN clients won't be able to reach it. Rewrite host and scheme to target's reachable endpoint.
	if shouldRewriteInternalHost(h, ip, target) {
		u.Scheme = target.Scheme
		u.Host = target.Host
	}

	return u.String(), nil
}

func isInternalXiaoya(u *url.URL, target *url.URL) bool {
	if u == nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || h == "xiaoya.host" || h == "xiaoya" {
		return true
	}
	ip := net.ParseIP(h)
	if ip != nil {
		if ip.IsLoopback() {
			return true
		}
		// Docker default bridge (172.16.0.0/12)
		if v4 := ip.To4(); v4 != nil && v4[0] == 172 && (v4[1] >= 16 && v4[1] <= 31) {
			return true
		}
	}
	if target != nil && target.Hostname() != "" {
		if strings.EqualFold(h, target.Hostname()) {
			return true
		}
	}
	return false
}

func tryXiaoyaDirectGet(r *http.Request, target *url.URL) (string, bool, error) {
	currentURL := target.String()
	userAgent := r.UserAgent()
	ctx := r.Context()

	const maxHops = 10
	visited := make(map[string]bool, maxHops)
	for hop := 0; hop < maxHops; hop++ {
		if visited[currentURL] {
			return "", false, errXiaoyaRedirectLoop
		}
		visited[currentURL] = true
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return "", false, err
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("X-Alist-OriUA", userAgent)
		res, err := xiaoyaClient.Do(req)
		if err != nil {
			return "", false, fmt.Errorf("resolver request failed: %w", err)
		}

		switch res.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			loc := strings.TrimSpace(res.Header.Get("Location"))
			res.Body.Close()
			if loc == "" {
				return "", false, fmt.Errorf("resolver returned redirect without Location")
			}

			// Validate and resolve relative/internal URLs
			validated, vErr := validateResolvedLink(loc, target)
			if vErr != nil {
				return "", false, vErr
			}

			parsedLoc, pErr := url.Parse(validated)
			if pErr != nil {
				return "", false, fmt.Errorf("invalid redirected link: %w", pErr)
			}

			// If parsedLoc is an external CDN or playable link (not internal Xiaoya), we found the final direct link!
			if !isInternalXiaoya(parsedLoc, target) {
				return validated, false, nil
			}

			// If it's still an internal Xiaoya URL (e.g. /p/..., internal token refresh, or local proxy),
			// continue following the next redirect hop.
			currentURL = validated

		case http.StatusUnauthorized, http.StatusForbidden:
			res.Body.Close()
			return "", true, fmt.Errorf("resolver HTTP %d", res.StatusCode)

		case http.StatusOK:
			// 小雅直接出流时交由客户端访问；解析阶段关闭响应，不搬运视频数据。
			ct := strings.ToLower(res.Header.Get("Content-Type"))
			if strings.Contains(ct, "video/") || strings.Contains(ct, "audio/") {
				res.Body.Close()
				return currentURL, false, nil
			}

			b, rErr := io.ReadAll(io.LimitReader(res.Body, 65537))
			res.Body.Close()
			if rErr != nil || len(b) > 65536 {
				return "", false, fmt.Errorf("invalid resolver response")
			}
			bodyStr := strings.TrimSpace(string(b))

			// Handle JSON response
			if strings.HasPrefix(bodyStr, "{") {
				var v struct {
					URL     string `json:"url"`
					Code    int    `json:"code"`
					Message string `json:"message"`
					Data    struct {
						RawURL string `json:"raw_url"`
						URL    string `json:"url"`
					} `json:"data"`
				}
				if json.Unmarshal(b, &v) != nil {
					return "", false, fmt.Errorf("invalid resolver JSON")
				}
				candURL := v.URL
				if candURL == "" {
					candURL = v.Data.RawURL
				}
				if candURL == "" {
					candURL = v.Data.URL
				}

				msgLower := strings.ToLower(v.Message)
				if candURL == "" && (v.Code == 401 || v.Code == 403 || strings.Contains(msgLower, "sign") || strings.Contains(msgLower, "expired")) {
					return "", true, fmt.Errorf("resolver sign rejected: %s", v.Message)
				}
				if candURL == "" {
					return "", false, fmt.Errorf("resolver JSON missing url: %s", v.Message)
				}

				validated, vErr := validateResolvedLink(candURL, target)
				if vErr != nil {
					return "", false, vErr
				}
				parsedCand, _ := url.Parse(validated)
				if !isInternalXiaoya(parsedCand, target) {
					return validated, false, nil
				}
				currentURL = validated
				continue
			}

			// Handle plain text URL
			if strings.HasPrefix(bodyStr, "http://") || strings.HasPrefix(bodyStr, "https://") {
				validated, vErr := validateResolvedLink(bodyStr, target)
				if vErr != nil {
					return "", false, vErr
				}
				parsedText, _ := url.Parse(validated)
				if !isInternalXiaoya(parsedText, target) {
					return validated, false, nil
				}
				currentURL = validated
				continue
			}

			// Handle HTML error responses
			if strings.HasPrefix(strings.ToLower(bodyStr), "<html") || strings.HasPrefix(strings.ToLower(bodyStr), "<!doctype") || strings.Contains(ct, "text/html") {
				return "", false, fmt.Errorf("resolver returned HTML error page")
			}

			// Other 200 OK content
			return currentURL, false, nil

		default:
			res.Body.Close()
			return "", false, fmt.Errorf("resolver HTTP %d", res.StatusCode)
		}
	}

	return "", false, fmt.Errorf("resolver redirect limit exceeded (%d requests)", maxHops)
}

func refreshAListSignOrLink(r *http.Request, baseEndpoint, alistPath, alistToken string, target *url.URL) (rawURL string, newSign string, err error) {
	apiBase, err := url.Parse(baseEndpoint)
	if err != nil || apiBase.Host == "" {
		return "", "", fmt.Errorf("invalid alist endpoint")
	}
	apiBase.Path = strings.TrimRight(apiBase.Path, "/") + "/api/fs/get"
	apiBase.RawPath = ""
	apiBase.RawQuery = ""

	payload, _ := json.Marshal(map[string]string{
		"path":     alistPath,
		"password": "",
	})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, apiBase.String(), bytes.NewReader(payload))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", r.UserAgent())
	req.Header.Set("X-Alist-OriUA", r.UserAgent())
	if alistToken != "" {
		req.Header.Set("Authorization", alistToken)
	}
	res, err := xiaoyaClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("alist fs/get HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 65536))
	if err != nil {
		return "", "", err
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			RawURL string `json:"raw_url"`
			Sign   string `json:"sign"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &resp); err != nil || resp.Code != 200 {
		return "", "", fmt.Errorf("alist fs/get failed: %s", resp.Message)
	}
	return validateAListFile(r, target, resp.Data.RawURL, resp.Data.Sign)
}

func validateAListFile(r *http.Request, target *url.URL, raw, sign string) (string, string, error) {
	if raw == "" {
		return "", sign, nil
	}
	validated, err := validateResolvedLink(raw, target)
	if err != nil {
		return "", sign, err
	}
	parsedURL, _ := url.Parse(validated)
	if !isInternalXiaoya(parsedURL, target) {
		return validated, sign, nil
	}
	// 内部地址必须解析成功；保留新签名供调用方在 raw_url 失败时重试。
	finalLink, _, err := tryXiaoyaDirectGet(r, parsedURL)
	return finalLink, sign, err
}

func xiaoyaLink(r *http.Request, raw, endpoint string) (string, error) {
	return xiaoyaLinkWithToken(r, raw, endpoint, os.Getenv("ALIST_TOKEN"))
}

func xiaoyaLinkWithToken(r *http.Request, raw, endpoint, alistToken string) (string, error) {
	// Kept for the low-level resolver tests and backwards-compatible callers.
	// Playback requests from App use xiaoyaLinkWithoutAList below.
	return xiaoyaLinkWithRefresh(r, raw, endpoint, alistToken, nil)
}

type alistFileLookup func(context.Context, string, string) (string, string, error)

func (a *App) SetAListPlayback(base string, get alistFileLookup) {
	a.alistPlaybackBase = strings.TrimRight(base, "/")
	a.alistPlayback = get
}

func (a *App) resolvePlaybackLink(r *http.Request, raw, endpoint string) (string, error) {
	return xiaoyaLinkWithoutAList(r, raw, endpoint)
}

// xiaoyaLinkWithoutAList resolves the stable STRM endpoint only.  Playback
// must not turn into an AList resource lookup: the SQLite sync already stored
// the canonical source path and a failed direct request is surfaced to the
// client for a targeted resync instead of issuing /api/fs/get.
func xiaoyaLinkWithoutAList(r *http.Request, raw, endpoint string) (string, error) {
	return xiaoyaLinkWithRefreshPolicy(r, raw, endpoint, "", nil, false)
}

func xiaoyaLinkWithRefresh(r *http.Request, raw, endpoint, alistToken string, get alistFileLookup) (string, error) {
	return xiaoyaLinkWithRefreshPolicy(r, raw, endpoint, alistToken, get, true)
}

func xiaoyaLinkWithRefreshPolicy(r *http.Request, raw, endpoint, alistToken string, get alistFileLookup, allowAListFallback bool) (string, error) {

	source, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid STRM")
	}
	target, err := url.Parse(endpoint)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return "", fmt.Errorf("invalid resolver address")
	}
	baseEndpoint := target.String()
	target.Path = strings.TrimRight(target.Path, "/") + source.Path
	target.RawPath = ""

	query := source.Query()
	origSign := query.Get("sign")
	hadRealSign := origSign != "" && origSign != "SIGN_STR"
	if origSign == "SIGN_STR" {
		query.Del("sign")
	}
	target.RawQuery = query.Encode()

	link, signRejected, err := tryXiaoyaDirectGet(r, target)
	if err == nil {
		return link, nil
	}
	// 容器内部地址改写后可能回到入口；使用同一小雅服务的已认证 AList 接口解析。
	if !signRejected && !errors.Is(err, errXiaoyaRedirectLoop) {
		return "", err
	}

	// Fallback 1: If the STRM carried an expired ?sign=..., retry clean /d/... without sign.
	if signRejected && hadRealSign {
		cleanTarget := *target
		cleanQuery := source.Query()
		cleanQuery.Del("sign")
		cleanTarget.RawQuery = cleanQuery.Encode()
		if cleanLink, _, cErr := tryXiaoyaDirectGet(r, &cleanTarget); cErr == nil {
			return cleanLink, nil
		}
	}

	// Legacy low-level callers may explicitly opt into AList refresh. The
	// application playback path passes false and therefore never calls the
	// resource API.
	if allowAListFallback && strings.HasPrefix(source.Path, "/d/") {
		alistPath := strings.TrimPrefix(source.Path, "/d")
		var rawURL, newSign string
		var fErr error
		if get != nil {
			rawURL, newSign, fErr = get(r.Context(), alistPath, r.UserAgent())
			if fErr == nil {
				rawURL, newSign, fErr = validateAListFile(r, target, rawURL, newSign)
			}
		} else {
			rawURL, newSign, fErr = refreshAListSignOrLink(r, baseEndpoint, alistPath, strings.TrimSpace(alistToken), target)
		}
		if fErr == nil && rawURL != "" {
			return rawURL, nil
		}
		if newSign != "" && newSign != origSign {
			refreshedTarget := *target
			refreshedQuery := source.Query()
			refreshedQuery.Set("sign", newSign)
			refreshedTarget.RawQuery = refreshedQuery.Encode()
			if refreshedLink, _, rErr := tryXiaoyaDirectGet(r, &refreshedTarget); rErr == nil {
				return refreshedLink, nil
			} else {
				return "", rErr
			}
		}
		if fErr != nil {
			return "", errors.Join(err, fErr)
		}
	}

	return "", err
}

func (a *App) alistTokenForPlayback() string {
	if a != nil && a.cfg != nil && strings.TrimSpace(a.cfg.AList.Token) != "" {
		return strings.TrimSpace(a.cfg.AList.Token)
	}
	return strings.TrimSpace(os.Getenv("ALIST_TOKEN"))
}

func (a *App) resolveXiaoya(w http.ResponseWriter, r *http.Request, x Item) {
	link, err := a.resolvePlaybackLink(r, x.URL, os.Getenv("XIAOYA_URL"))
	if err != nil {
		fail(w, http.StatusBadGateway, "Xiaoya 直链解析失败: "+err.Error())
		return
	}
	w.Header().Set("Location", link)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}
