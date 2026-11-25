package chatgpt

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
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
	if did := s.Cookies["oai-did"]; did != "" {
		c.deviceID = did
	}
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
	if s.Build != "" {
		req.Header.Set("OAI-Client-Build-Number", s.Build)
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
	if len(s.Cookies) > 0 {
		parts := make([]string, 0, len(s.Cookies))
		for k, v := range s.Cookies {
			parts = append(parts, k+"="+v)
		}
		req.Header.Set("Cookie", strings.Join(parts, "; "))
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

// ---------------- conversation list ----------------

type ConversationSummary struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	CreateTime string `json:"create_time,omitempty"`
	UpdateTime string `json:"update_time,omitempty"`
	Archived   bool   `json:"archived,omitempty"`
}

func (c *Client) ListConversations(ctx context.Context, offset, limit int) ([]ConversationSummary, int, error) {
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	path := fmt.Sprintf("/backend-api/conversations?offset=%d&limit=%d&order=updated", offset, limit)
	resp, err := c.do(ctx, fhttp.MethodGet, path, nil, nil)
	if err != nil {
		return nil, 0, err
	}
	if resp.StatusCode != fhttp.StatusOK {
		return nil, 0, readError(resp, "读取会话列表")
	}
	defer resp.Body.Close()
	var payload struct {
		Items []struct {
			ID         string `json:"id"`
			Title      string `json:"title"`
			CreateTime string `json:"create_time"`
			UpdateTime string `json:"update_time"`
			IsArchived bool   `json:"is_archived"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, 0, err
	}
	items := make([]ConversationSummary, 0, len(payload.Items))
	for _, item := range payload.Items {
		items = append(items, ConversationSummary{ID: item.ID, Title: displayTitle(item.Title), CreateTime: item.CreateTime, UpdateTime: item.UpdateTime, Archived: item.IsArchived})
	}
	return items, payload.Total, nil
}

func displayTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" || strings.EqualFold(title, "new chat") {
		return "新对话"
	}
	return title
}

// ---------------- conversation detail ----------------

type Message struct {
	ID         string  `json:"id"`
	Role       string  `json:"role"`
	Text       string  `json:"text"`
	CreateTime float64 `json:"create_time,omitempty"`
}

type Conversation struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Model       string    `json:"model,omitempty"`
	CurrentNode string    `json:"current_node,omitempty"`
	CreateTime  string    `json:"create_time,omitempty"`
	UpdateTime  string    `json:"update_time,omitempty"`
	Messages    []Message `json:"messages"`
}

func (c *Client) GetConversation(ctx context.Context, id string) (*Conversation, error) {
	paths := []string{
		"/backend-api/conversations/" + id + "?num_turns=100&include_has_versions=true",
		"/backend-api/conversation/" + id + "?include_model_switcher_metadata=false",
	}
	var lastErr error
	for _, path := range paths {
		resp, err := c.do(ctx, fhttp.MethodGet, path, nil, nil)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != fhttp.StatusOK {
			lastErr = readError(resp, "读取会话")
			continue
		}
		var detail map[string]any
		err = json.NewDecoder(resp.Body).Decode(&detail)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return normalizeConversation(detail), nil
	}
	if lastErr == nil {
		lastErr = errors.New("读取会话失败")
	}
	return nil, lastErr
}

func normalizeConversation(detail map[string]any) *Conversation {
	conv := &Conversation{Messages: []Message{}}
	conv.ID, _ = detail["conversation_id"].(string)
	conv.Title, _ = detail["title"].(string)
	conv.Model, _ = detail["default_model_slug"].(string)
	conv.CurrentNode, _ = detail["current_node"].(string)
	conv.CreateTime, _ = detail["create_time"].(string)
	conv.UpdateTime, _ = detail["update_time"].(string)
	conv.Title = displayTitle(conv.Title)

	if raw, ok := detail["messages"].([]any); ok {
		for _, item := range raw {
			if m, ok := item.(map[string]any); ok {
				if msg, ok := normalizeMessage(m); ok {
					conv.Messages = append(conv.Messages, msg)
				}
			}
		}
	}
	if len(conv.Messages) == 0 {
		if mapping, ok := detail["mapping"].(map[string]any); ok {
			var ordered []map[string]any
			if conv.CurrentNode != "" {
				ordered = branchMessages(mapping, conv.CurrentNode)
			}
			if len(ordered) == 0 {
				ordered = allMessages(mapping)
			}
			for _, m := range ordered {
				if msg, ok := normalizeMessage(m); ok {
					conv.Messages = append(conv.Messages, msg)
				}
			}
		}
	}
	if conv.CurrentNode == "" && len(conv.Messages) > 0 {
		conv.CurrentNode = conv.Messages[len(conv.Messages)-1].ID
	}
	return conv
}

func branchMessages(mapping map[string]any, current string) []map[string]any {
	var branch []map[string]any
	seen := map[string]bool{}
	for nodeID := current; nodeID != "" && !seen[nodeID]; {
		seen[nodeID] = true
		node, _ := mapping[nodeID].(map[string]any)
		if node == nil {
			break
		}
		if message, ok := node["message"].(map[string]any); ok {
			branch = append(branch, message)
		}
		nodeID, _ = node["parent"].(string)
	}
	for i, j := 0, len(branch)-1; i < j; i, j = i+1, j-1 {
		branch[i], branch[j] = branch[j], branch[i]
	}
	return branch
}

func allMessages(mapping map[string]any) []map[string]any {
	var result []map[string]any
	for _, raw := range mapping {
		node, _ := raw.(map[string]any)
		if message, ok := node["message"].(map[string]any); ok {
			result = append(result, message)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		return messageCreateTime(result[i]) < messageCreateTime(result[j])
	})
	return result
}

func normalizeMessage(m map[string]any) (Message, bool) {
	author, _ := m["author"].(map[string]any)
	role, _ := author["role"].(string)
	if role != "user" && role != "assistant" {
		return Message{}, false
	}
	metadata, _ := m["metadata"].(map[string]any)
	if metadata["is_visually_hidden_from_conversation"] == true {
		return Message{}, false
	}
	content, _ := m["content"].(map[string]any)
	text := contentText(content)
	if strings.TrimSpace(text) == "" {
		return Message{}, false
	}
	id, _ := m["id"].(string)
	return Message{ID: id, Role: role, Text: text, CreateTime: messageCreateTime(m)}, true
}

func contentText(content map[string]any) string {
	var b strings.Builder
	if parts, ok := content["parts"].([]any); ok {
		for _, part := range parts {
			switch v := part.(type) {
			case string:
				b.WriteString(v)
			}
		}
	}
	if b.Len() == 0 {
		if text, ok := content["text"].(string); ok {
			b.WriteString(text)
		}
	}
	return b.String()
}

func messageCreateTime(message map[string]any) float64 {
	switch value := message["create_time"].(type) {
	case float64:
		return value
	case json.Number:
		v, _ := value.Float64()
		return v
	}
	return 0
}

func (c *Client) DeleteConversation(ctx context.Context, id string) error {
	resp, err := c.do(ctx, fhttp.MethodDelete, "/backend-api/conversation/id/"+id, nil, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == fhttp.StatusOK || resp.StatusCode == fhttp.StatusNoContent {
		resp.Body.Close()
		return nil
	}
	firstErr := readError(resp, "删除会话")
	resp, err = c.do(ctx, fhttp.MethodDelete, "/backend-api/conversation/"+id, nil, nil)
	if err != nil {
		return firstErr
	}
	if resp.StatusCode == fhttp.StatusOK || resp.StatusCode == fhttp.StatusNoContent {
		resp.Body.Close()
		return nil
	}
	return readError(resp, "删除会话")
}

// ---------------- chat ----------------

type SendResult struct {
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id,omitempty"`
	Text           string `json:"text"`
}

func (c *Client) SendMessage(ctx context.Context, conversationID, parentID, text string, onDelta func(string) error) (SendResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return SendResult{}, errors.New("消息不能为空")
	}
	s, err := c.sessionOrError(false)
	if err != nil {
		return SendResult{}, err
	}
	if conversationID != "" && parentID == "" {
		conv, err := c.GetConversation(ctx, conversationID)
		if err != nil {
			return SendResult{}, err
		}
		parentID = conv.CurrentNode
	}
	if conversationID == "" {
		parentID = "client-created-root"
	}
	if parentID == "" {
		return SendResult{}, errors.New("无法定位会话最后一条消息，请刷新会话后重试")
	}

	pToken, cfg := (POW{}).BuildPToken(upstreamUA, s.Build)
	sentinel, proof, err := c.chatRequirements(ctx, s, pToken, cfg)
	if err != nil {
		return SendResult{}, err
	}

	messageID := uuid.NewString()
	message := map[string]any{
		"id":     messageID,
		"author": map[string]any{"role": "user"},
		"content": map[string]any{
			"content_type": "text",
			"parts":        []any{text},
		},
	}
	payload := map[string]any{
		"turn_attribution":       map[string]any{"turn_trigger": "composer"},
		"action":                 "next",
		"messages":               []any{message},
		"model":                  upstreamModel,
		"parent_message_id":      parentID,
		"timezone":               "Asia/Shanghai",
		"timezone_offset_min":    -480,
		"is_do_not_remember":     false,
		"client_contextual_info": map[string]any{"app_name": "chatgpt.com", "app_surface": "codex_browser", "has_web_push_capabilities": true, "web_push_notification_permission": "default"},
		"local_function_names":   []string{"local.continue_in_work"},
		"supported_encodings":    []string{"v1"},
		"genui_state_snapshots":  []any{},
		"client_prepare_state":   "success",
	}
	if conversationID != "" {
		payload["conversation_id"] = conversationID
	}
	extra := map[string]string{
		"Accept": "text/event-stream",
		"OpenAI-Sentinel-Chat-Requirements-Token": sentinel,
		"X-OAI-MCP-Form-Version":                  "2",
		"X-OAI-Turn-Trace-Id":                     uuid.NewString(),
	}
	if proof != "" {
		extra["OpenAI-Sentinel-Proof-Token"] = proof
	}
	resp, err := c.request(ctx, s, fhttp.MethodPost, "/backend-api/f/conversation", payload, extra)
	if err != nil {
		return SendResult{}, err
	}
	if resp.StatusCode != fhttp.StatusOK {
		return SendResult{}, readError(resp, "发送消息")
	}
	defer resp.Body.Close()

	convID, msgID, full, err := readConversationSSE(resp.Body, onDelta)
	if convID == "" {
		convID = conversationID
	}
	result := SendResult{ConversationID: convID, MessageID: msgID, Text: full}
	if err == nil && full == "" && convID != "" {
		// 有些响应只给事件补丁，没给出完整文本；回头读一次会话兜底。
		if conv, getErr := c.GetConversation(ctx, convID); getErr == nil {
			for i := len(conv.Messages) - 1; i >= 0; i-- {
				if conv.Messages[i].Role == "assistant" {
					result.Text = conv.Messages[i].Text
					result.MessageID = conv.Messages[i].ID
					if onDelta != nil {
						_ = onDelta(result.Text)
					}
					break
				}
			}
		}
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) chatRequirements(ctx context.Context, s *Session, pToken string, config []any) (string, string, error) {
	resp, err := c.request(ctx, s, fhttp.MethodPost, "/backend-api/sentinel/chat-requirements/prepare", map[string]any{"p": pToken}, nil)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != fhttp.StatusOK {
		return "", "", readError(resp, "获取 sentinel 准备状态")
	}
	var prepare struct {
		PrepareToken string `json:"prepare_token"`
		ProofOfWork  *struct {
			Required   bool   `json:"required"`
			Seed       string `json:"seed"`
			Difficulty string `json:"difficulty"`
		} `json:"proofofwork"`
		Turnstile *struct {
			Required bool   `json:"required"`
			DX       string `json:"dx"`
		} `json:"turnstile"`
	}
	err = json.NewDecoder(resp.Body).Decode(&prepare)
	resp.Body.Close()
	if err != nil {
		return "", "", err
	}
	if prepare.PrepareToken == "" {
		return "", "", errors.New("ChatGPT 未返回 sentinel prepare_token")
	}
	proof := ""
	if prepare.ProofOfWork != nil && prepare.ProofOfWork.Required {
		proof, err = (POW{}).BuildProofToken(config, prepare.ProofOfWork.Seed, prepare.ProofOfWork.Difficulty, upstreamUA, s.Build)
		if err != nil {
			return "", "", err
		}
	}
	turnstile := ""
	if prepare.Turnstile != nil && prepare.Turnstile.Required {
		turnstile = (Turnstile{}).Solve(prepare.Turnstile.DX, pToken)
	}
	body := map[string]any{"prepare_token": prepare.PrepareToken, "proof_token": proof, "turnstile_token": turnstile}
	resp, err = c.request(ctx, s, fhttp.MethodPost, "/backend-api/sentinel/chat-requirements/finalize", body, nil)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != fhttp.StatusOK {
		return "", "", readError(resp, "获取 sentinel token")
	}
	defer resp.Body.Close()
	var finalize struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&finalize); err != nil {
		return "", "", err
	}
	if finalize.Token == "" {
		return "", "", errors.New("ChatGPT 未返回 sentinel token")
	}
	return finalize.Token, proof, nil
}

// ---------------- SSE ----------------

func readConversationSSE(reader io.Reader, delta func(string) error) (string, string, string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	conversationID, current, messageID := "", "", ""
	started := false
	upstreamError := ""
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		value := strings.TrimPrefix(line, "data: ")
		value = strings.TrimPrefix(value, "data:")
		value = strings.TrimSpace(value)
		if value == "[DONE]" {
			break
		}
		var event map[string]any
		if json.Unmarshal([]byte(value), &event) != nil {
			continue
		}
		if errValue := event["error"]; errValue != nil {
			upstreamError = fmt.Sprint(errValue)
		} else if code := event["error_code"]; code != nil {
			upstreamError = fmt.Sprint(code)
		}
		if id, ok := event["conversation_id"].(string); ok && id != "" {
			conversationID = id
		}
		if nested, ok := event["v"].(map[string]any); ok {
			if id, ok := nested["conversation_id"].(string); ok && id != "" {
				conversationID = id
			}
		}
		text := current
		message := assistantMessage(event)
		if message != nil {
			id, _ := message["id"].(string)
			status, _ := message["status"].(string)
			if !started && (message["end_turn"] == true || strings.HasPrefix(status, "finished")) {
				continue
			}
			if messageID != "" && id != "" && id != messageID {
				continue
			}
			started = true
			if messageID == "" {
				messageID = id
			}
			if content, ok := message["content"].(map[string]any); ok {
				if value := contentText(content); value != "" {
					text = value
				}
			}
		} else if started {
			text = applyConversationTextPatch(event, current)
		} else {
			continue
		}
		if text == current {
			continue
		}
		next := text
		if strings.HasPrefix(text, current) {
			next = strings.TrimPrefix(text, current)
		}
		current = text
		if next != "" && delta != nil {
			if err := delta(next); err != nil {
				return conversationID, messageID, current, err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return conversationID, messageID, current, err
	}
	if current == "" && upstreamError != "" {
		return conversationID, messageID, current, fmt.Errorf("ChatGPT 返回错误：%s", upstreamError)
	}
	return conversationID, messageID, current, nil
}

func assistantMessage(event map[string]any) map[string]any {
	candidates := []map[string]any{event}
	if value, ok := event["v"].(map[string]any); ok {
		candidates = append(candidates, value)
	}
	for _, candidate := range candidates {
		message, _ := candidate["message"].(map[string]any)
		author, _ := message["author"].(map[string]any)
		metadata, _ := message["metadata"].(map[string]any)
		recipient, _ := message["recipient"].(string)
		channel, _ := message["channel"].(string)
		if author["role"] == "assistant" && (metadata["is_visually_hidden_from_conversation"] != true || channel == "final") && (recipient == "" || recipient == "all") && (channel == "" || channel == "final") {
			return message
		}
	}
	return nil
}

func applyConversationTextPatch(event map[string]any, current string) string {
	if event["p"] == "/message/content/parts/0" {
		value, _ := event["v"].(string)
		if event["o"] == "replace" {
			return value
		}
		return current + value
	}
	if value, ok := event["v"].(string); ok && event["p"] == nil && event["o"] == nil {
		return current + value
	}
	if values, ok := event["v"].([]any); ok {
		for _, raw := range values {
			if operation, ok := raw.(map[string]any); ok {
				current = applyConversationTextPatch(operation, current)
			}
		}
	}
	return current
}
