package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

const (
	baseURL      = "https://chatgpt.com"
	upstreamUA   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/158.0.0.0 Safari/537.36"
	defaultBuild = "6af93f86ec4cabd04aa39a01f6f7c1ead95de625"
	// 本客户端固定使用上游自动选择模型。
	upstreamModel = "auto"
)

var (
	ErrUnauthorized = errors.New("ChatGPT 登录态已失效，请重新登录或刷新 session")
	ErrRateLimited  = errors.New("ChatGPT 请求过于频繁，请稍后再试")
)

type Options struct {
	Tag           string
	Name          string
	SessionFile   string
	ClientTimeout time.Duration
}

type Client struct {
	opts Options
	http tls_client.HttpClient

	mu         sync.Mutex
	session    *Session
	sessionMod time.Time
	deviceID   string
	sessionID  string
}

func NewClient(opts Options) (*Client, error) {
	if opts.ClientTimeout <= 0 {
		opts.ClientTimeout = 300 * time.Second
	}
	client, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithTimeoutSeconds(int(opts.ClientTimeout.Seconds())),
		tls_client.WithClientProfile(profiles.Chrome_146),
		tls_client.WithRandomTLSExtensionOrder(),
	)
	if err != nil {
		return nil, err
	}
	c := &Client{
		opts:      opts,
		http:      client,
		deviceID:  uuid.NewString(),
		sessionID: uuid.NewString(),
	}
	c.loadFromDisk()
	return c, nil
}

type Status struct {
	Tag       string    `json:"tag"`
	Name      string    `json:"name"`
	Email     string    `json:"email,omitempty"`
	Plan      string    `json:"plan,omitempty"`
	ExpiresAt int64     `json:"expires_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	Ready     bool      `json:"ready"`
	Error     string    `json:"error,omitempty"`
}

func (c *Client) Status() Status {
	st := Status{Tag: c.opts.Tag, Name: c.opts.Name}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.maybeReloadLocked()
	if c.session == nil {
		st.Error = "还没有 session 文件"
		return st
	}
	st.Email, st.Plan, st.ExpiresAt, st.UpdatedAt = c.session.Email, c.session.Plan, c.session.ExpiresAt, c.session.UpdatedAt
	st.Ready = !c.session.Expired()
	if !st.Ready {
		st.Error = "登录态已过期，请更新 session 文件"
	}
	return st
}

func (c *Client) loadFromDisk() {
	s, err := LoadSession(c.opts.SessionFile)
	if err != nil {
		return
	}
	c.session = s
	if info, err := os.Stat(c.opts.SessionFile); err == nil {
		c.sessionMod = info.ModTime()
	}
}

// maybeReloadLocked 在 session 文件被外部更新后自动重新加载，
// 这样用户直接替换文件里的 access_token 即可生效，无需重启服务。
func (c *Client) maybeReloadLocked() {
	info, err := os.Stat(c.opts.SessionFile)
	if err != nil {
		return
	}
	if c.session == nil || info.ModTime().After(c.sessionMod) {
		c.loadFromDisk()
	}
}

func (c *Client) sessionOrError(reload bool) (*Session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if reload {
		c.loadFromDisk()
	} else {
		c.maybeReloadLocked()
	}
	if c.session == nil {
		return nil, fmt.Errorf("读取 session 文件失败：%s（请先创建该文件）", c.opts.SessionFile)
	}
	if c.session.Expired() {
		return nil, ErrUnauthorized
	}
	return c.session, nil
}

func (c *Client) applyHeaders(req *fhttp.Request, s *Session, path string) {
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("User-Agent", upstreamUA)
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Priority", "u=1, i")
	req.Header.Set("Sec-Ch-Ua", `"Chromium";v="158", "Not.A/Brand";v="24", "Google Chrome";v="158"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("OAI-Device-Id", c.deviceID)
	req.Header.Set("OAI-DID", c.deviceID)
	req.Header.Set("OAI-Session-Id", c.sessionID)
	req.Header.Set("OAI-Language", "zh-CN")
	req.Header.Set("Originator", "Codex Browser")
	req.Header.Set("X-OpenAI-Web-Frontend", "codex_webview")
	req.Header.Set("X-OpenAI-Codex-Window-Type", "browser")
	req.Header.Set("X-OpenAI-Rate-Limit-Join-Workspace", "1")
	req.Header.Set("X-OpenAI-Rejoin-Pro-CTA", "1")
	if s.AccountID != "" {
		req.Header.Set("ChatGPT-Account-ID", s.AccountID)
	}
	req.Header.Set("X-OAI-MCP-Form-Version", "2")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if path != "" {
		target := strings.Split(path, "?")[0]
		req.Header.Set("X-OpenAI-Target-Path", target)
		req.Header.Set("X-OpenAI-Target-Route", target)
	}
}

func (c *Client) request(ctx context.Context, s *Session, method, path string, payload any, extra map[string]string) (*fhttp.Response, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := fhttp.NewRequestWithContext(ctx, method, baseURL+path, body)
	if err != nil {
		return nil, err
	}
	c.applyHeaders(req, s, path)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 ChatGPT 失败: %w", err)
	}
	return resp, nil
}

func (c *Client) do(ctx context.Context, method, path string, payload any, extra map[string]string) (*fhttp.Response, error) {
	s, err := c.sessionOrError(false)
	if err != nil {
		return nil, err
	}
	resp, err := c.request(ctx, s, method, path, payload, extra)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == fhttp.StatusUnauthorized {
		// 可能是用户在外部更新了 session 文件，重新读一次再试。
		resp.Body.Close()
		s, reloadErr := c.sessionOrError(true)
		if reloadErr != nil {
			return nil, reloadErr
		}
		return c.request(ctx, s, method, path, payload, extra)
	}
	return resp, nil
}

func readError(resp *fhttp.Response, action string) error {
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	message := strings.TrimSpace(string(data))
	switch resp.StatusCode {
	case fhttp.StatusUnauthorized:
		return ErrUnauthorized
	case fhttp.StatusTooManyRequests:
		return ErrRateLimited
	}
	if message == "" {
		message = resp.Status
	}
	return fmt.Errorf("%s失败（HTTP %d）：%s", action, resp.StatusCode, message)
}
