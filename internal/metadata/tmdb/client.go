package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options configures the TMDB v3 API client.
type Options struct {
	APIKey      string
	Language    string
	BaseURL     string
	ImageURL    string
	ProxyURL    string
	HTTPClient  *http.Client
	MinInterval time.Duration
}

// Client talks to TMDB v3 endpoints with lightweight rate limiting and zh-CN -> en-US overview fallback.
type Client struct {
	apiKey      string
	language    string
	baseURL     string
	imageURL    string
	httpClient  *http.Client
	minInterval time.Duration

	mu       sync.Mutex
	lastCall time.Time
}

type SearchResult struct {
	ID            int     `json:"id"`
	MediaType     string  `json:"media_type"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          int     `json:"year"`
	Overview      string  `json:"overview"`
	PosterURL     string  `json:"poster_url"`
	BackdropURL   string  `json:"backdrop_url"`
	Rating        float64 `json:"rating"`
	Name          string  `json:"name,omitempty"`
	PosterPath    string  `json:"poster_path,omitempty"`
	ReleaseDate   string  `json:"release_date,omitempty"`
	FirstAirDate  string  `json:"first_air_date,omitempty"`
}

type PersonCredit struct {
	Name  string `json:"name"`
	Role  string `json:"role"`
	Thumb string `json:"thumb"`
}

type MediaMetadata struct {
	TMDBID        string         `json:"tmdb_id"`
	IMDBID        string         `json:"imdb_id"`
	Title         string         `json:"title"`
	OriginalTitle string         `json:"original_title"`
	Overview      string         `json:"overview"`
	Tagline       string         `json:"tagline"`
	Premiered     string         `json:"premiered"`
	Year          int            `json:"year"`
	Rating        float64        `json:"rating"`
	RuntimeMin    float64        `json:"runtime_min"`
	Genres        []string       `json:"genres"`
	Studios       []string       `json:"studios"`
	Directors     []string       `json:"directors"`
	Actors        []PersonCredit `json:"actors"`
	PosterURL     string         `json:"poster_url"`
	BackdropURL   string         `json:"backdrop_url"`
	ThumbURL      string         `json:"thumb_url"`
	Season        int            `json:"season,omitempty"`
	Episode       int            `json:"episode,omitempty"`
	Status        string         `json:"status,omitempty"`
	InProduction  bool           `json:"in_production,omitempty"`
}

func NewClient(opts Options) *Client {
	lang := strings.TrimSpace(opts.Language)
	if lang == "" {
		lang = "zh-CN"
	}
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "https://api.themoviedb.org/3"
	}
	img := strings.TrimRight(strings.TrimSpace(opts.ImageURL), "/")
	if img == "" {
		img = "https://image.tmdb.org/t/p"
	}
	hc := opts.HTTPClient
	if hc == nil {
		tr := &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			IdleConnTimeout:       60 * time.Second,
			MaxIdleConns:          50,
			MaxIdleConnsPerHost:   10,
		}
		proxyStr := strings.TrimSpace(opts.ProxyURL)
		if proxyStr == "" {
			proxyStr = strings.TrimSpace(os.Getenv("TMDB_PROXY"))
		}
		if proxyStr != "" {
			if pu, err := url.Parse(proxyStr); err == nil {
				tr.Proxy = http.ProxyURL(pu)
			}
		}
		hc = &http.Client{
			Timeout:   15 * time.Second,
			Transport: tr,
		}
	}
	interval := opts.MinInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	return &Client{
		apiKey:      strings.TrimSpace(opts.APIKey),
		language:    lang,
		baseURL:     base,
		imageURL:    img,
		httpClient:  hc,
		minInterval: interval,
	}
}

func (c *Client) Configured() bool {
	return c != nil && c.apiKey != ""
}

func (c *Client) HTTPClient() *http.Client {
	if c == nil {
		return nil
	}
	return c.httpClient
}

func (c *Client) ImageURL(size, relPath string) string {
	p := strings.TrimSpace(relPath)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") {
		return p
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return c.imageURL + "/" + size + p
}

func (c *Client) throttle(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	wait := c.minInterval - now.Sub(c.lastCall)
	if wait > 0 {
		c.lastCall = now.Add(wait)
	} else {
		c.lastCall = now
	}
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func parseRetryAfter(val string) time.Duration {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0
	}
	if sec, err := strconv.Atoi(val); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(val); err == nil {
		diff := time.Until(t)
		if diff > 0 {
			return diff
		}
	}
	return 0
}

// IsRateLimitedOrTimeout checks if an error indicates rate limiting, timeout, or transient network failure.
func IsRateLimitedOrTimeout(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "context deadline exceeded") ||
		strings.Contains(s, "429") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "temporarily unavailable") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "eof")
}

func (c *Client) getJSON(ctx context.Context, apiPath string, params url.Values, out any) error {
	if !c.Configured() {
		return fmt.Errorf("TMDB API key not configured")
	}
	if err := c.throttle(ctx); err != nil {
		return err
	}
	if params == nil {
		params = url.Values{}
	}
	if params.Get("language") == "" {
		params.Set("language", c.language)
	}
	useBearer := strings.Count(c.apiKey, ".") == 2 || strings.HasPrefix(strings.ToLower(c.apiKey), "bearer ")
	if !useBearer {
		params.Set("api_key", c.apiKey)
	}
	reqURL := c.baseURL + apiPath
	if enc := params.Encode(); enc != "" {
		reqURL += "?" + enc
	}
	var lastErr error
	maxRetries := 3
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*500) * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "XiaoyaMediaServer/1.0 (Emby-Compatible)")
		if useBearer {
			tok := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(c.apiKey, "Bearer "), "bearer "))
			req.Header.Set("Authorization", "Bearer "+tok)
		}

		res, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		if res.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
			res.Body.Close()
			msg := strings.TrimSpace(string(bodyBytes))
			if res.StatusCode == http.StatusTooManyRequests ||
				res.StatusCode == http.StatusBadGateway ||
				res.StatusCode == http.StatusServiceUnavailable ||
				res.StatusCode == http.StatusGatewayTimeout {
				retryAfter := parseRetryAfter(res.Header.Get("Retry-After"))
				if retryAfter <= 0 {
					retryAfter = time.Duration((attempt+1)*500) * time.Millisecond
				}
				lastErr = fmt.Errorf("TMDB HTTP %d: %s", res.StatusCode, msg)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryAfter):
				}
				continue
			}
			return fmt.Errorf("TMDB HTTP %d: %s", res.StatusCode, msg)
		}

		decodeErr := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(out)
		res.Body.Close()
		return decodeErr
	}

	if lastErr != nil {
		errStr := lastErr.Error()
		if strings.Contains(errStr, "context deadline exceeded") {
			return fmt.Errorf("TMDB 请求超时 (context deadline exceeded): TMDB 响应过慢或请求过于频繁，任务已加入自动重试队列")
		}
		if strings.Contains(errStr, "429") {
			return fmt.Errorf("TMDB 请求限流 (HTTP 429): TMDB 请求过于频繁，已自动排队稍后重试")
		}
		if strings.Contains(errStr, "EOF") || strings.Contains(errStr, "connection reset") || strings.Contains(errStr, "operation timed out") || strings.Contains(errStr, "no such host") {
			return fmt.Errorf("TMDB 请求失败 (%s): 无法直连 TMDB 官方服务器，请在管理后台 [TMDB 刮削与识别] 中配置网络代理 ProxyURL (如 Clash/局域网代理) 或修改 BaseURL 为可用反代镜像", errStr)
		}
		return lastErr
	}
	return fmt.Errorf("TMDB 请求重试失败")
}

// TestConnection verifies API key validity against /configuration.
func (c *Client) TestConnection(ctx context.Context) error {
	var resp map[string]any
	return c.getJSON(ctx, "/configuration", url.Values{}, &resp)
}

func yearFromDate(d string) int {
	if len(d) >= 4 {
		if y, err := strconv.Atoi(d[:4]); err == nil {
			return y
		}
	}
	return 0
}

// SearchMovie searches TMDB movies by title and optional year.
func (c *Client) SearchMovie(ctx context.Context, query string, year int) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("query", strings.TrimSpace(query))
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	var raw struct {
		Results []struct {
			ID            int     `json:"id"`
			Title         string  `json:"title"`
			OriginalTitle string  `json:"original_title"`
			ReleaseDate   string  `json:"release_date"`
			Overview      string  `json:"overview"`
			PosterPath    string  `json:"poster_path"`
			BackdropPath  string  `json:"backdrop_path"`
			VoteAverage   float64 `json:"vote_average"`
		} `json:"results"`
	}
	if err := c.getJSON(ctx, "/search/movie", q, &raw); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		out = append(out, SearchResult{
			ID:            r.ID,
			MediaType:     "movie",
			Title:         r.Title,
			OriginalTitle: r.OriginalTitle,
			Year:          yearFromDate(r.ReleaseDate),
			Overview:      r.Overview,
			PosterURL:     c.ImageURL("w500", r.PosterPath),
			BackdropURL:   c.ImageURL("w1280", r.BackdropPath),
			Rating:        r.VoteAverage,
			PosterPath:    r.PosterPath,
			ReleaseDate:   r.ReleaseDate,
		})
	}
	return out, nil
}

// SearchTV searches TMDB TV series by name and optional first_air_date_year.
func (c *Client) SearchTV(ctx context.Context, query string, year int) ([]SearchResult, error) {
	q := url.Values{}
	q.Set("query", strings.TrimSpace(query))
	if year > 0 {
		q.Set("first_air_date_year", strconv.Itoa(year))
	}
	var raw struct {
		Results []struct {
			ID           int     `json:"id"`
			Name         string  `json:"name"`
			OriginalName string  `json:"original_name"`
			FirstAirDate string  `json:"first_air_date"`
			Overview     string  `json:"overview"`
			PosterPath   string  `json:"poster_path"`
			BackdropPath string  `json:"backdrop_path"`
			VoteAverage  float64 `json:"vote_average"`
		} `json:"results"`
	}
	if err := c.getJSON(ctx, "/search/tv", q, &raw); err != nil {
		return nil, err
	}
	if len(raw.Results) == 0 && year > 0 {
		// Retry without year filter if release year differs from folder tag by +/-1 year.
		q.Del("first_air_date_year")
		if err := c.getJSON(ctx, "/search/tv", q, &raw); err != nil {
			return nil, err
		}
	}
	out := make([]SearchResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		out = append(out, SearchResult{
			ID:            r.ID,
			MediaType:     "tv",
			Title:         r.Name,
			Name:          r.Name,
			OriginalTitle: r.OriginalName,
			Year:          yearFromDate(r.FirstAirDate),
			Overview:      r.Overview,
			PosterURL:     c.ImageURL("w500", r.PosterPath),
			BackdropURL:   c.ImageURL("w1280", r.BackdropPath),
			Rating:        r.VoteAverage,
			PosterPath:    r.PosterPath,
			FirstAirDate:  r.FirstAirDate,
		})
	}
	return out, nil
}

// GetMovie fetches rich movie metadata including credits and external IDs.
func (c *Client) GetMovie(ctx context.Context, tmdbID string) (*MediaMetadata, error) {
	q := url.Values{"append_to_response": {"credits,external_ids"}}
	var raw struct {
		ID            int     `json:"id"`
		IMDbID        string  `json:"imdb_id"`
		Title         string  `json:"title"`
		OriginalTitle string  `json:"original_title"`
		Overview      string  `json:"overview"`
		Tagline       string  `json:"tagline"`
		ReleaseDate   string  `json:"release_date"`
		Runtime       float64 `json:"runtime"`
		VoteAverage   float64 `json:"vote_average"`
		PosterPath    string  `json:"poster_path"`
		BackdropPath  string  `json:"backdrop_path"`
		Genres        []struct {
			Name string `json:"name"`
		} `json:"genres"`
		ProductionCompanies []struct {
			Name string `json:"name"`
		} `json:"production_companies"`
		Credits struct {
			Cast []struct {
				Name        string `json:"name"`
				Character   string `json:"character"`
				ProfilePath string `json:"profile_path"`
			} `json:"cast"`
			Crew []struct {
				Name string `json:"name"`
				Job  string `json:"job"`
			} `json:"crew"`
		} `json:"credits"`
	}
	if err := c.getJSON(ctx, "/movie/"+url.PathEscape(tmdbID), q, &raw); err != nil {
		return nil, err
	}
	if raw.Overview == "" && !strings.EqualFold(c.language, "en-US") {
		var fallback struct {
			Overview string `json:"overview"`
		}
		if c.getJSON(ctx, "/movie/"+url.PathEscape(tmdbID), url.Values{"language": {"en-US"}}, &fallback) == nil && fallback.Overview != "" {
			raw.Overview = fallback.Overview
		}
	}
	m := &MediaMetadata{
		TMDBID:        strconv.Itoa(raw.ID),
		IMDBID:        raw.IMDbID,
		Title:         raw.Title,
		OriginalTitle: raw.OriginalTitle,
		Overview:      raw.Overview,
		Tagline:       raw.Tagline,
		Premiered:     raw.ReleaseDate,
		Year:          yearFromDate(raw.ReleaseDate),
		Rating:        raw.VoteAverage,
		RuntimeMin:    raw.Runtime,
		PosterURL:     c.ImageURL("w500", raw.PosterPath),
		BackdropURL:   c.ImageURL("w1280", raw.BackdropPath),
	}
	for _, g := range raw.Genres {
		if g.Name != "" {
			m.Genres = append(m.Genres, g.Name)
		}
	}
	for _, s := range raw.ProductionCompanies {
		if s.Name != "" {
			m.Studios = append(m.Studios, s.Name)
		}
	}
	for i, cast := range raw.Credits.Cast {
		if i >= 25 {
			break
		}
		if cast.Name != "" {
			m.Actors = append(m.Actors, PersonCredit{
				Name:  cast.Name,
				Role:  cast.Character,
				Thumb: c.ImageURL("w500", cast.ProfilePath),
			})
		}
	}
	for _, crew := range raw.Credits.Crew {
		if strings.EqualFold(crew.Job, "Director") && crew.Name != "" {
			m.Directors = append(m.Directors, crew.Name)
		}
	}
	return m, nil
}

// GetTV fetches rich TV series metadata including credits and external IDs.
func (c *Client) GetTV(ctx context.Context, tmdbID string) (*MediaMetadata, error) {
	q := url.Values{"append_to_response": {"credits,external_ids"}}
	var raw struct {
		ID               int       `json:"id"`
		Name             string    `json:"name"`
		OriginalName     string    `json:"original_name"`
		Overview         string    `json:"overview"`
		Tagline          string    `json:"tagline"`
		FirstAirDate     string    `json:"first_air_date"`
		EpisodeRunTime   []float64 `json:"episode_run_time"`
		VoteAverage      float64   `json:"vote_average"`
		PosterPath       string    `json:"poster_path"`
		BackdropPath     string    `json:"backdrop_path"`
		Status           string    `json:"status"`
		InProduction     bool      `json:"in_production"`
		Genres           []struct{ Name string `json:"name"` } `json:"genres"`
		Networks         []struct{ Name string `json:"name"` } `json:"networks"`
		ProductionCos    []struct{ Name string `json:"name"` } `json:"production_companies"`
		CreatedBy        []struct{ Name string `json:"name"` } `json:"created_by"`
		ExternalIDs      struct{ IMDbID string `json:"imdb_id"` } `json:"external_ids"`
		Credits          struct {
			Cast []struct {
				Name        string `json:"name"`
				Character   string `json:"character"`
				ProfilePath string `json:"profile_path"`
			} `json:"cast"`
		} `json:"credits"`
	}
	if err := c.getJSON(ctx, "/tv/"+url.PathEscape(tmdbID), q, &raw); err != nil {
		return nil, err
	}
	if raw.Overview == "" && !strings.EqualFold(c.language, "en-US") {
		var fallback struct {
			Overview string `json:"overview"`
		}
		if c.getJSON(ctx, "/tv/"+url.PathEscape(tmdbID), url.Values{"language": {"en-US"}}, &fallback) == nil && fallback.Overview != "" {
			raw.Overview = fallback.Overview
		}
	}
	runtime := 0.0
	if len(raw.EpisodeRunTime) > 0 {
		runtime = raw.EpisodeRunTime[0]
	}
	m := &MediaMetadata{
		TMDBID:        strconv.Itoa(raw.ID),
		IMDBID:        raw.ExternalIDs.IMDbID,
		Title:         raw.Name,
		OriginalTitle: raw.OriginalName,
		Overview:      raw.Overview,
		Tagline:       raw.Tagline,
		Premiered:     raw.FirstAirDate,
		Year:          yearFromDate(raw.FirstAirDate),
		Rating:        raw.VoteAverage,
		RuntimeMin:    runtime,
		PosterURL:     c.ImageURL("w500", raw.PosterPath),
		BackdropURL:   c.ImageURL("w1280", raw.BackdropPath),
		Status:        raw.Status,
		InProduction:  raw.InProduction,
	}
	for _, g := range raw.Genres {
		if g.Name != "" {
			m.Genres = append(m.Genres, g.Name)
		}
	}
	for _, n := range raw.Networks {
		if n.Name != "" {
			m.Studios = append(m.Studios, n.Name)
		}
	}
	if len(m.Studios) == 0 {
		for _, p := range raw.ProductionCos {
			if p.Name != "" {
				m.Studios = append(m.Studios, p.Name)
			}
		}
	}
	for _, cr := range raw.CreatedBy {
		if cr.Name != "" {
			m.Directors = append(m.Directors, cr.Name)
		}
	}
	for i, cast := range raw.Credits.Cast {
		if i >= 25 {
			break
		}
		if cast.Name != "" {
			m.Actors = append(m.Actors, PersonCredit{
				Name:  cast.Name,
				Role:  cast.Character,
				Thumb: c.ImageURL("w500", cast.ProfilePath),
			})
		}
	}
	return m, nil
}

// GetTVSeason fetches season metadata and all episode summaries in one call!
func (c *Client) GetTVSeason(ctx context.Context, tmdbID string, seasonNumber int) (*MediaMetadata, map[int]*MediaMetadata, error) {
	var raw struct {
		ID           int    `json:"id"`
		Name         string `json:"name"`
		Overview     string `json:"overview"`
		AirDate      string `json:"air_date"`
		PosterPath   string `json:"poster_path"`
		SeasonNumber int    `json:"season_number"`
		Episodes     []struct {
			ID            int     `json:"id"`
			EpisodeNumber int     `json:"episode_number"`
			SeasonNumber  int     `json:"season_number"`
			Name          string  `json:"name"`
			Overview      string  `json:"overview"`
			AirDate       string  `json:"air_date"`
			Runtime       float64 `json:"runtime"`
			VoteAverage   float64 `json:"vote_average"`
			StillPath     string  `json:"still_path"`
		} `json:"episodes"`
	}
	apiPath := fmt.Sprintf("/tv/%s/season/%d", url.PathEscape(tmdbID), seasonNumber)
	if err := c.getJSON(ctx, apiPath, url.Values{}, &raw); err != nil {
		return nil, nil, err
	}
	seasonMeta := &MediaMetadata{
		TMDBID:    tmdbID,
		Title:     raw.Name,
		Overview:  raw.Overview,
		Premiered: raw.AirDate,
		Year:      yearFromDate(raw.AirDate),
		PosterURL: c.ImageURL("w500", raw.PosterPath),
		Season:    raw.SeasonNumber,
	}
	episodes := make(map[int]*MediaMetadata, len(raw.Episodes))
	for _, ep := range raw.Episodes {
		episodes[ep.EpisodeNumber] = &MediaMetadata{
			TMDBID:     tmdbID,
			Title:      ep.Name,
			Overview:   ep.Overview,
			Premiered:  ep.AirDate,
			Year:       yearFromDate(ep.AirDate),
			RuntimeMin: ep.Runtime,
			Rating:     ep.VoteAverage,
			ThumbURL:   c.ImageURL("w500", ep.StillPath),
			PosterURL:  c.ImageURL("w500", ep.StillPath),
			Season:     ep.SeasonNumber,
			Episode:    ep.EpisodeNumber,
		}
	}
	return seasonMeta, episodes, nil
}
