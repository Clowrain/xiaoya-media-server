package embyserver

import (
	"bytes"
	"encoding/json"
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

func validateResolvedLink(link string, target *url.URL) (string, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || strings.ContainsAny(link, "\r\n") {
		return "", fmt.Errorf("resolver did not return an HTTP link")
	}
	h := strings.ToLower(u.Hostname())
	ip := net.ParseIP(h)
	if h == "localhost" || h == "xiaoya.host" || h == strings.ToLower(target.Hostname()) || (ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast())) {
		return "", fmt.Errorf("resolver returned an internal address")
	}
	return u.String(), nil
}

func tryXiaoyaDirectGet(r *http.Request, target *url.URL) (string, bool, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("User-Agent", r.UserAgent())
	req.Header.Set("X-Alist-OriUA", r.UserAgent())
	res, err := xiaoyaClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("resolver request failed")
	}
	defer res.Body.Close()

	link := ""
	switch res.StatusCode {
	case 301, 302, 303, 307, 308:
		link = res.Header.Get("Location")
	case 401, 403:
		return "", true, fmt.Errorf("resolver HTTP %d", res.StatusCode)
	case 200:
		b, rErr := io.ReadAll(io.LimitReader(res.Body, 65537))
		if rErr != nil || len(b) > 65536 {
			return "", false, fmt.Errorf("invalid resolver response")
		}
		link = strings.TrimSpace(string(b))
		if strings.HasPrefix(link, "{") {
			var v struct {
				URL     string `json:"url"`
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(b, &v) != nil {
				return "", false, fmt.Errorf("invalid resolver JSON")
			}
			msgLower := strings.ToLower(v.Message)
			if v.URL == "" && (v.Code == 401 || v.Code == 403 || strings.Contains(msgLower, "sign") || strings.Contains(msgLower, "expired")) {
				return "", true, fmt.Errorf("resolver sign rejected: %s", v.Message)
			}
			link = v.URL
		}
	default:
		return "", false, fmt.Errorf("resolver HTTP %d", res.StatusCode)
	}
	validated, vErr := validateResolvedLink(link, target)
	return validated, false, vErr
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
	if resp.Data.RawURL != "" {
		if validated, vErr := validateResolvedLink(resp.Data.RawURL, target); vErr == nil {
			return validated, resp.Data.Sign, nil
		}
	}
	return "", resp.Data.Sign, nil
}

func xiaoyaLink(r *http.Request, raw, endpoint string) (string, error) {
	return xiaoyaLinkWithToken(r, raw, endpoint, os.Getenv("ALIST_TOKEN"))
}

func xiaoyaLinkWithToken(r *http.Request, raw, endpoint, alistToken string) (string, error) {
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
	if !signRejected {
		return "", err
	}

	// Fallback 1: If the STRM carried an expired ?sign=..., retry clean /d/... without sign.
	if hadRealSign {
		cleanTarget := *target
		cleanQuery := source.Query()
		cleanQuery.Del("sign")
		cleanTarget.RawQuery = cleanQuery.Encode()
		if cleanLink, _, cErr := tryXiaoyaDirectGet(r, &cleanTarget); cErr == nil {
			return cleanLink, nil
		}
	}

	// Fallback 2: Refresh raw_url or sign via AList /api/fs/get for /d/... paths.
	if strings.HasPrefix(source.Path, "/d/") {
		alistPath := strings.TrimPrefix(source.Path, "/d")
		rawURL, newSign, fErr := refreshAListSignOrLink(r, baseEndpoint, alistPath, strings.TrimSpace(alistToken), target)
		if fErr == nil && rawURL != "" {
			return rawURL, nil
		}
		if fErr == nil && newSign != "" && newSign != origSign {
			refreshedTarget := *target
			refreshedQuery := source.Query()
			refreshedQuery.Set("sign", newSign)
			refreshedTarget.RawQuery = refreshedQuery.Encode()
			if refreshedLink, _, rErr := tryXiaoyaDirectGet(r, &refreshedTarget); rErr == nil {
				return refreshedLink, nil
			}
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
	link, err := xiaoyaLinkWithToken(r, x.URL, os.Getenv("XIAOYA_URL"), a.alistTokenForPlayback())
	if err != nil {
		fail(w, http.StatusBadGateway, "Xiaoya 直链解析失败: "+err.Error())
		return
	}
	w.Header().Set("Location", link)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusFound)
}
