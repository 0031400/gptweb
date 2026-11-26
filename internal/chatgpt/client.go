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
	"regexp"
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
	ID          string       `json:"id"`
	Role        string       `json:"role"`
	Text        string       `json:"text"`
	CreateTime  float64      `json:"create_time,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
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
	// 只使用复数接口：它的 messages 里带有完整的 fallbackMarkdown。
	path := "/backend-api/conversations/" + id + "?num_turns=100&include_has_versions=true"
	resp, err := c.do(ctx, fhttp.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != fhttp.StatusOK {
		return nil, readError(resp, "读取会话")
	}
	defer resp.Body.Close()
	var detail map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, err
	}
	return normalizeConversation(detail), nil
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
	if conv.CurrentNode == "" && len(conv.Messages) > 0 {
		conv.CurrentNode = conv.Messages[len(conv.Messages)-1].ID
	}
	return conv
}

func normalizeMessage(m map[string]any) (Message, bool) {
	author, _ := m["author"].(map[string]any)
	role, _ := author["role"].(string)
	if role != "user" && role != "assistant" {
		return Message{}, false
	}
	metadata, _ := m["metadata"].(map[string]any)
	channel, _ := m["channel"].(string)
	fallback := fallbackMarkdown(metadata)
	// 网页端有时会把同一轮回答再生成一条 hidden 的自定义 UI 消息；
	// 只有它自己带 fallbackMarkdown 时才需要保留，否则会与可见消息重复。
	if metadata["is_visually_hidden_from_conversation"] == true && (channel != "final" || fallback == "") {
		return Message{}, false
	}
	content, _ := m["content"].(map[string]any)
	text := cleanAssistantText(contentText(content))
	if fallback != "" {
		text = cleanAssistantText(fallback)
	}
	attachments := messageAttachments(content, metadata)
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
		return Message{}, false
	}
	id, _ := m["id"].(string)
	return Message{ID: id, Role: role, Text: text, CreateTime: messageCreateTime(m), Attachments: attachments}, true
}

func messageAttachments(content, metadata map[string]any) []Attachment {
	var result []Attachment
	indexByID := map[string]int{}
	addOrMerge := func(item Attachment) {
		if item.ID == "" {
			return
		}
		if index, ok := indexByID[item.ID]; ok {
			mergeAttachment(&result[index], item)
			return
		}
		indexByID[item.ID] = len(result)
		result = append(result, item)
	}

	if parts, ok := content["parts"].([]any); ok {
		for _, raw := range parts {
			part, ok := raw.(map[string]any)
			if !ok || part["content_type"] != "image_asset_pointer" {
				continue
			}
			pointer, _ := part["asset_pointer"].(string)
			id := attachmentIDFromPointer(pointer)
			if id == "" {
				continue
			}
			addOrMerge(Attachment{
				ID:     id,
				Kind:   "image",
				Width:  intValue(part["width"]),
				Height: intValue(part["height"]),
				Size:   int64Value(part["size_bytes"]),
			})
		}
	}

	if list, ok := metadata["attachments"].([]any); ok {
		for _, raw := range list {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := item["id"].(string)
			id = attachmentIDFromPointer(id)
			if id == "" {
				continue
			}
			attachment := Attachment{
				ID:            id,
				LibraryFileID: stringValue(item["library_file_id"]),
				Name:          stringValue(item["name"]),
				MimeType:      stringValue(item["mime_type"]),
				Size:          int64Value(item["size"]),
				Width:         intValue(item["width"]),
				Height:        intValue(item["height"]),
			}
			attachment.Kind = kindForMime(attachment.MimeType)
			if attachment.Kind == "" && attachment.Width > 0 {
				attachment.Kind = "image"
			}
			addOrMerge(attachment)
		}
	}
	return result
}

func mergeAttachment(dst *Attachment, src Attachment) {
	if src.LibraryFileID != "" {
		dst.LibraryFileID = src.LibraryFileID
	}
	if src.Name != "" {
		dst.Name = src.Name
	}
	if src.MimeType != "" {
		dst.MimeType = src.MimeType
	}
	if src.Size > 0 {
		dst.Size = src.Size
	}
	if src.Width > 0 {
		dst.Width = src.Width
	}
	if src.Height > 0 {
		dst.Height = src.Height
	}
	if src.Kind != "" {
		dst.Kind = src.Kind
	}
	if dst.Kind == "" {
		dst.Kind = "file"
	}
}

func attachmentIDFromPointer(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "sediment://")
	value = strings.TrimPrefix(value, "file-service://")
	return value
}

func kindForMime(mimeType string) string {
	if strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return "image"
	}
	if mimeType != "" {
		return "file"
	}
	return ""
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func intValue(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case int:
		return v
	}
	return 0
}

func int64Value(value any) int64 {
	switch v := value.(type) {
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

var (
	uiSelfClosingPattern = regexp.MustCompile(`<(?:icon|Cite|FileCite)\b[^>]*/>`)
	uiContainerPattern   = regexp.MustCompile(`</?(?:badge|box|text|divider)\b[^>]*>`)
)

// cleanAssistantText 去掉模型输出里的引用标记和 ChatGPT 专属 UI 标签，
// 保留容器标签内的文字，避免前端直接看到 `<badge>`、`<box>` 这类私有标记。
func cleanAssistantText(value string) string {
	value = uiSelfClosingPattern.ReplaceAllString(value, "")
	value = uiContainerPattern.ReplaceAllString(value, "")
	return strings.TrimSpace(value)
}

func hasUITags(value string) bool {
	for _, tag := range []string{"<badge", "<box", "<text", "<divider", "<icon", "<Cite", "<FileCite"} {
		if strings.Contains(value, tag) {
			return true
		}
	}
	return false
}

func fallbackMarkdown(metadata map[string]any) string {
	dil, _ := metadata["model_dil_v2"].(map[string]any)
	if dil == nil {
		return ""
	}
	value, _ := dil["fallbackMarkdown"].(string)
	return value
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

func (c *Client) SendMessage(ctx context.Context, conversationID, parentID, text string, attachments []Attachment, onDelta func(string) error) (SendResult, error) {
	text = strings.TrimSpace(text)
	if text == "" && len(attachments) == 0 {
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

	pToken, cfg := (POW{}).BuildPToken(upstreamUA, defaultBuild)
	sentinel, proof, err := c.chatRequirements(ctx, s, pToken, cfg)
	if err != nil {
		return SendResult{}, err
	}

	messageID := uuid.NewString()
	parts := make([]any, 0, len(attachments)+1)
	metaAttachments := make([]any, 0, len(attachments))
	hasImage := false
	for _, attachment := range attachments {
		if attachment.ID == "" {
			continue
		}
		meta := map[string]any{
			"id":        attachment.ID,
			"name":      attachment.Name,
			"size":      attachment.Size,
			"mime_type": attachment.MimeType,
			"source":    "local",
		}
		if attachment.LibraryFileID != "" {
			meta["library_file_id"] = attachment.LibraryFileID
		}
		if attachment.IsImage() {
			hasImage = true
			parts = append(parts, map[string]any{
				"content_type":  "image_asset_pointer",
				"asset_pointer": "sediment://" + attachment.ID,
				"width":         attachment.Width,
				"height":        attachment.Height,
				"size_bytes":    attachment.Size,
			})
			meta["width"] = attachment.Width
			meta["height"] = attachment.Height
		}
		metaAttachments = append(metaAttachments, meta)
	}
	if text != "" || !hasImage {
		parts = append(parts, text)
	}
	contentType := "text"
	if hasImage {
		contentType = "multimodal_text"
	}
	messageMetadata := map[string]any{
		"automation_creation_attribution": map[string]any{"origin": "conversation", "flow_id": messageID},
	}
	if len(metaAttachments) > 0 {
		messageMetadata["attachments"] = metaAttachments
	}
	message := map[string]any{
		"id":       messageID,
		"author":   map[string]any{"role": "user"},
		"content":  map[string]any{"content_type": contentType, "parts": parts},
		"metadata": messageMetadata,
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
	traceID := uuid.NewString()
	extra := map[string]string{
		"Accept": "text/event-stream",
		"OpenAI-Sentinel-Chat-Requirements-Token": sentinel,
		"X-OAI-MCP-Form-Version":                  "2",
		"X-OAI-Turn-Trace-Id":                     traceID,
	}
	if proof != "" {
		extra["OpenAI-Sentinel-Proof-Token"] = proof
	}
	if len(metaAttachments) > 0 {
		// prepare 不是发送成功的硬性条件，失败时仍然尝试正式发送。
		if conduit, prepareErr := c.prepareTurn(ctx, s, conversationID, parentID, attachments, traceID); prepareErr == nil && conduit != "" {
			extra["X-Conduit-Token"] = conduit
		}
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
	result := SendResult{ConversationID: convID, MessageID: msgID, Text: cleanAssistantText(full)}
	if err == nil && convID != "" && (full == "" || hasUITags(full)) {
		// 没有正文，或流里带了 ChatGPT 自定义 UI 标签时，回头读一次会话：
		// 历史归一化会优先使用 metadata.model_dil_v2.fallbackMarkdown。
		if conv, getErr := c.GetConversation(ctx, convID); getErr == nil {
			for i := len(conv.Messages) - 1; i >= 0; i-- {
				if conv.Messages[i].Role == "assistant" {
					result.Text = conv.Messages[i].Text
					result.MessageID = conv.Messages[i].ID
					if full == "" && onDelta != nil {
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

func (c *Client) prepareTurn(ctx context.Context, s *Session, conversationID, parentID string, attachments []Attachment, traceID string) (string, error) {
	body := map[string]any{
		"client_prepare_state":    "sent",
		"action":                  "next",
		"model":                   upstreamModel,
		"parent_message_id":       parentID,
		"timezone":                "Asia/Shanghai",
		"timezone_offset_min":     -480,
		"is_do_not_remember":      false,
		"local_function_names":    []string{"local.continue_in_work"},
		"client_prepare_dispatch": "debounced",
		"client_prepare_source":   "composer_editor_state",
	}
	if conversationID != "" {
		body["conversation_id"] = conversationID
	}
	var mimeTypes []string
	var imageIDs []string
	seenMime := map[string]bool{}
	for _, attachment := range attachments {
		if attachment.MimeType != "" && !seenMime[attachment.MimeType] {
			seenMime[attachment.MimeType] = true
			mimeTypes = append(mimeTypes, attachment.MimeType)
		}
		// prepare 接口对图片 ID 数量有限制，超过两个容易 422；正式消息里仍会带全部附件。
		if attachment.IsImage() && attachment.ID != "" && len(imageIDs) < 2 {
			imageIDs = append(imageIDs, attachment.ID)
		}
	}
	if len(mimeTypes) > 0 {
		body["attachment_mime_types"] = mimeTypes
	}
	if len(imageIDs) > 0 {
		body["prepare_image_upload_ids"] = imageIDs
	}
	resp, err := c.request(ctx, s, fhttp.MethodPost, "/backend-api/f/conversation/prepare", body, map[string]string{
		"X-OAI-Turn-Trace-Id":    traceID,
		"X-OAI-MCP-Form-Version": "2",
	})
	if err != nil {
		return "", err
	}
	if resp.StatusCode != fhttp.StatusOK {
		return "", readError(resp, "准备附件消息")
	}
	defer resp.Body.Close()
	var result struct {
		ConduitToken *string `json:"conduit_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if result.ConduitToken == nil {
		return "", nil
	}
	return *result.ConduitToken, nil
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
		proof, err = (POW{}).BuildProofToken(config, prepare.ProofOfWork.Seed, prepare.ProofOfWork.Difficulty, upstreamUA, defaultBuild)
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
