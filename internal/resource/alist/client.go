package alist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

type Options struct {
	BaseURL    string
	Username   string
	Password   string
	Token      string
	HTTPClient *http.Client
}

type Entry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	Sign     string `json:"sign"`
	Thumb    string `json:"thumb"`
	Type     int    `json:"type"`
	FullPath string `json:"full_path"`
}

type SearchItem struct {
	Parent   string `json:"parent"`
	Name     string `json:"name"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Type     int    `json:"type"`
	FullPath string `json:"full_path"`
}

type FileDetail struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	IsDir    bool   `json:"is_dir"`
	Modified string `json:"modified"`
	Sign     string `json:"sign"`
	RawURL   string `json:"raw_url"`
	Provider string `json:"provider"`
	FullPath string `json:"full_path"`
}

type Client struct {
	baseURL  string
	username string
	password string
	httpCli  *http.Client

	mu    sync.RWMutex
	token string
}

func NewClient(opts Options) *Client {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = "http://xiaoya.host:5678"
	}
	cli := opts.HTTPClient
	if cli == nil {
		cli = &http.Client{Timeout: 25 * time.Second}
	}
	return &Client{
		baseURL:  base,
		username: strings.TrimSpace(opts.Username),
		password: opts.Password,
		token:    strings.TrimSpace(opts.Token),
		httpCli:  cli,
	}
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func normalizeAListPath(p string) string {
	trimmed := strings.TrimSpace(p)
	if trimmed == "" || trimmed == "/" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func joinAListPath(parent, name string) string {
	p := normalizeAListPath(parent)
	if p == "/" {
		return "/" + strings.TrimLeft(name, "/")
	}
	return p + "/" + strings.TrimLeft(name, "/")
}

func (c *Client) ensureToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	tok := c.token
	c.mu.RUnlock()
	if tok != "" || c.username == "" {
		return tok, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" {
		return c.token, nil
	}
	body, _ := json.Marshal(map[string]string{
		"username": c.username,
		"password": c.password,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return "", fmt.Errorf("alist login request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	var res struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("alist login decode: %w", err)
	}
	if res.Code != 200 || res.Data.Token == "" {
		return "", fmt.Errorf("alist login failed (code=%d): %s", res.Code, res.Message)
	}
	c.token = res.Data.Token
	return c.token, nil
}

func (c *Client) postJSON(ctx context.Context, apiPath string, reqBody any, outData any) error {
	tok, err := c.ensureToken(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+apiPath, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if tok != "" {
		req.Header.Set("Authorization", tok)
	}

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return fmt.Errorf("alist %s: %w", apiPath, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("alist %s HTTP %d", apiPath, resp.StatusCode)
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("alist %s decode: %w", apiPath, err)
	}
	if envelope.Code != 200 {
		return fmt.Errorf("alist %s error (code=%d): %s", apiPath, envelope.Code, envelope.Message)
	}
	if outData != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, outData); err != nil {
			return fmt.Errorf("alist %s data decode: %w", apiPath, err)
		}
	}
	return nil
}

// List browses an AList directory via POST /api/fs/list.
func (c *Client) List(ctx context.Context, dirPath string, page, perPage int, refresh bool) ([]Entry, int, error) {
	normDir := normalizeAListPath(dirPath)
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > 500 {
		perPage = 100
	}
	var data struct {
		Content []Entry `json:"content"`
		Total   int     `json:"total"`
	}
	err := c.postJSON(ctx, "/api/fs/list", map[string]any{
		"path":     normDir,
		"password": "",
		"page":     page,
		"per_page": perPage,
		"refresh":  refresh,
	}, &data)
	if err != nil {
		return nil, 0, err
	}
	for i := range data.Content {
		data.Content[i].FullPath = joinAListPath(normDir, data.Content[i].Name)
	}
	return data.Content, data.Total, nil
}

// Search searches AList resources by keyword via POST /api/fs/search.
func (c *Client) Search(ctx context.Context, parentPath, keywords string, scope, page, perPage int) ([]SearchItem, int, error) {
	normParent := normalizeAListPath(parentPath)
	kw := strings.TrimSpace(keywords)
	if kw == "" {
		return nil, 0, fmt.Errorf("search keywords cannot be empty")
	}
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > 200 {
		perPage = 50
	}
	var data struct {
		Content []SearchItem `json:"content"`
		Total   int          `json:"total"`
	}
	err := c.postJSON(ctx, "/api/fs/search", map[string]any{
		"parent":   normParent,
		"keywords": kw,
		"scope":    scope, // 0=all, 1=dir, 2=file
		"page":     page,
		"per_page": perPage,
		"password": "",
	}, &data)
	if err != nil {
		return nil, 0, err
	}
	for i := range data.Content {
		data.Content[i].FullPath = joinAListPath(data.Content[i].Parent, data.Content[i].Name)
	}
	return data.Content, data.Total, nil
}

// Get fetches single resource metadata and sign/raw_url via POST /api/fs/get.
func (c *Client) Get(ctx context.Context, targetPath string) (*FileDetail, error) {
	norm := normalizeAListPath(targetPath)
	var detail FileDetail
	if err := c.postJSON(ctx, "/api/fs/get", map[string]any{
		"path":     norm,
		"password": "",
	}, &detail); err != nil {
		return nil, err
	}
	detail.FullPath = norm
	return &detail, nil
}
