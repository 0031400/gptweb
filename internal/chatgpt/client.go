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
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

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
	ID          string           `json:"id"`
	Role        string           `json:"role"`
	Text        string           `json:"text"`
	CreateTime  float64          `json:"create_time,omitempty"`
	Attachments []Attachment     `json:"attachments,omitempty"`
	GenUI       []GenUIComponent `json:"genui,omitempty"`
}

// GenUIComponent 是上游 genui_components 的原始范围。
// StartIndex/EndIndex 原样透传；Data 是后端整理后的最小渲染数据。
type GenUIComponent struct {
	Type       string         `json:"type"`
	StartIndex int            `json:"start_index"`
	EndIndex   int            `json:"end_index"`
	Data       map[string]any `json:"data,omitempty"`
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
	// 只使用复数接口：它的 messages 里带有完整的 content.parts。
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
	if role != "user" && role != "assistant" && role != "tool" {
		return Message{}, false
	}
	metadata, _ := m["metadata"].(map[string]any)
	channel, _ := m["channel"].(string)
	content, _ := m["content"].(map[string]any)
	attachments := messageAttachments(content, metadata)
	text := contentText(content)
	var genui []GenUIComponent
	if role == "assistant" {
		genui = normalizeGenUI(text, metadata)
	}

	// 图片生成等工具结果放在 role=tool 的消息里，最终 assistant 文本
	// 并不会带附件；把带附件的 tool 消息作为一条 assistant 附件消息，
	// 否则历史里看不到生成/上传的图片。
	if role == "tool" {
		if len(attachments) == 0 {
			return Message{}, false
		}
		id, _ := m["id"].(string)
		return Message{ID: id, Role: "assistant", CreateTime: messageCreateTime(m), Attachments: attachments}, true
	}

	// 网页端有时会把同一轮回答再生成一条 hidden 的自定义 UI 消息；
	// 只有它自己带正文时才需要保留，否则会与可见消息重复。
	if metadata["is_visually_hidden_from_conversation"] == true && (channel != "final" || strings.TrimSpace(text) == "") {
		return Message{}, false
	}
	if strings.TrimSpace(text) == "" && len(attachments) == 0 {
		return Message{}, false
	}
	id, _ := m["id"].(string)
	return Message{ID: id, Role: role, Text: text, CreateTime: messageCreateTime(m), Attachments: attachments, GenUI: genui}, true
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
				ID:       id,
				Kind:     "image",
				MimeType: stringValue(part["mime_type"]),
				Width:    intValue(part["width"]),
				Height:   intValue(part["height"]),
				Size:     int64Value(part["size_bytes"]),
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

var tagAttributePattern = regexp.MustCompile(`([A-Za-z_][\w-]*)\s*=\s*"([^"]*)"`)

// normalizeGenUI 把上游 genui_components 整理成前端需要的最小数据。
// start_index/end_index 原样透传，前端直接用 text.slice 删除区间并插入组件。
func normalizeGenUI(text string, metadata map[string]any) []GenUIComponent {
	raw, _ := metadata["genui_components"].([]any)
	results := componentResults(metadata)
	components := make([]GenUIComponent, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(map[string]any)
		if !ok {
			continue
		}
		componentType, _ := value["type"].(string)
		if componentType == "" {
			continue
		}
		start := intValue(value["start_index"])
		end := intValue(value["end_index"])
		source := genuiSource(text, start, end)
		components = append(components, GenUIComponent{
			Type:       componentType,
			StartIndex: start,
			EndIndex:   end,
			Data:       genuiData(componentType, source, value, results),
		})
	}
	// {@body ... DIL.useState(...)} 交互块没有 genui_components 条目，
	// 按 parts 里的声明 + 后续顶层容器推导一个区间，前端统一显示「未实现」。
	components = append(components, deriveInteractiveBlocks(text, components)...)
	if len(components) == 0 {
		return nil
	}
	sort.SliceStable(components, func(i, j int) bool {
		return components[i].StartIndex < components[j].StartIndex
	})
	return components
}

func deriveInteractiveBlocks(text string, existing []GenUIComponent) []GenUIComponent {
	var components []GenUIComponent
	searchAt := 0
	for {
		relative := strings.Index(text[searchAt:], "{@body")
		if relative < 0 {
			break
		}
		start := searchAt + relative
		searchAt = start + len("{@body")
		lineStart := strings.LastIndexByte(text[:start], '\n') + 1
		if strings.TrimSpace(text[lineStart:start]) != "" || insideCodeFence(text, start) {
			continue
		}
		cursor := lineStart
		for cursor < len(text) {
			lineEnd := strings.IndexByte(text[cursor:], '\n')
			if lineEnd < 0 {
				lineEnd = len(text)
			} else {
				lineEnd += cursor
			}
			line := strings.TrimSpace(text[cursor:lineEnd])
			if line == "" || strings.HasPrefix(line, "{@body") {
				cursor = lineEnd + 1
				continue
			}
			break
		}
		relative = strings.IndexByte(text[cursor:], '<')
		if relative < 0 {
			continue
		}
		open := cursor + relative
		if strings.TrimSpace(text[cursor:open]) != "" {
			continue
		}
		name := tagNameAt(text, open)
		if name == "" {
			continue
		}
		end := matchingTagEnd(text, open, name)
		if end <= start {
			continue
		}
		startIndex := utf8.RuneCountInString(text[:lineStart])
		endIndex := utf8.RuneCountInString(text[:end])
		if overlapsGenUI(existing, startIndex, endIndex) || overlapsGenUI(components, startIndex, endIndex) {
			searchAt = end
			continue
		}
		components = append(components, GenUIComponent{
			Type:       "interactive_block",
			StartIndex: startIndex,
			EndIndex:   endIndex,
		})
		searchAt = end
	}
	return components
}

func insideCodeFence(text string, position int) bool {
	inFence := false
	for _, line := range strings.Split(text[:position], "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
	}
	return inFence
}

func overlapsGenUI(components []GenUIComponent, start, end int) bool {
	for _, component := range components {
		if start < component.EndIndex && component.StartIndex < end {
			return true
		}
	}
	return false
}

func tagNameAt(text string, position int) string {
	if position >= len(text) || text[position] != '<' {
		return ""
	}
	cursor := position + 1
	if cursor < len(text) && text[cursor] == '/' {
		cursor++
	}
	start := cursor
	for cursor < len(text) {
		char := text[cursor]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' {
			cursor++
			continue
		}
		break
	}
	return text[start:cursor]
}

func matchingTagEnd(text string, open int, name string) int {
	openEnd := tagEnd(text, open)
	if openEnd < 0 {
		return -1
	}
	if openEnd >= 2 && text[openEnd-2] == '/' {
		return openEnd
	}
	depth := 1
	cursor := openEnd
	for cursor < len(text) {
		relative := strings.IndexByte(text[cursor:], '<')
		if relative < 0 {
			return -1
		}
		position := cursor + relative
		next := tagEnd(text, position)
		if next < 0 {
			return -1
		}
		if tagNameAt(text, position) == name {
			closing := position+1 < len(text) && text[position+1] == '/'
			selfClosing := next >= 2 && text[next-2] == '/'
			if closing {
				depth--
				if depth == 0 {
					return next
				}
			} else if !selfClosing {
				depth++
			}
		}
		cursor = next
	}
	return -1
}

func tagEnd(text string, position int) int {
	quote := byte(0)
	braces := 0
	for cursor := position + 1; cursor < len(text); cursor++ {
		char := text[cursor]
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		switch char {
		case '"', '\'':
			quote = char
		case '{':
			braces++
		case '}':
			if braces > 0 {
				braces--
			}
		case '>':
			if braces == 0 {
				return cursor + 1
			}
		}
	}
	return -1
}

func genuiSource(text string, start, end int) string {
	runes := []rune(text)
	if start >= 0 && end > start && end <= len(runes) {
		source := string(runes[start:end])
		if strings.HasPrefix(strings.TrimSpace(source), "<") {
			return source
		}
	}
	units := utf16.Encode(runes)
	if start >= 0 && end > start && end <= len(units) {
		source := string(utf16.Decode(units[start:end]))
		if strings.HasPrefix(strings.TrimSpace(source), "<") {
			return source
		}
	}
	return ""
}

func genuiData(componentType, source string, value map[string]any, results map[string]any) map[string]any {
	switch componentType {
	case "code_block":
		attrs, body, ok := parseWrappedTag(source, "CodeBlock")
		if !ok {
			return nil
		}
		return map[string]any{"language": attrs["language"], "code": body}
	case "writing_block":
		attrs, body, ok := parseWrappedTag(source, "WritingBlock")
		if !ok {
			return nil
		}
		return map[string]any{
			"subject": attrs["subject"],
			"variant": attrs["variant"],
			"body":    body,
		}
	case "async_image":
		attrs := tagAttributes(source)
		data := map[string]any{}
		for _, key := range []string{"query", "aspectRatio", "width", "maxWidth"} {
			if attrs[key] != "" {
				data[key] = attrs[key]
			}
		}
		if id, _ := value["component_resolution_id"].(string); id != "" {
			if state := resolvedState(results, id); state != nil {
				if images := compactImages(state["images"]); len(images) > 0 {
					data["images"] = images
				}
			}
		}
		if len(data) == 0 {
			return nil
		}
		return data
	case "link":
		attrs := tagAttributes(source)
		data := map[string]any{}
		if id, _ := value["component_resolution_id"].(string); id != "" {
			if state := resolvedState(results, id); state != nil {
				if text := stringValue(state["url"]); text != "" {
					data["url"] = text
				}
				if text := stringValue(state["title"]); text != "" {
					data["title"] = text
				}
			}
		}
		if _, exists := data["url"]; !exists && attrs["url"] != "" {
			data["url"] = attrs["url"]
		}
		if _, exists := data["title"]; !exists && attrs["title"] != "" {
			data["title"] = attrs["title"]
		}
		if len(data) == 0 {
			return nil
		}
		return data
	case "charts_widget_v2":
		return chartData(source)
	default:
		// map 等未实现类型不需要携带数据；前端按 type 显示「未实现」。
		return nil
	}
}

func tagAttributes(source string) map[string]string {
	head := source
	if end := strings.IndexByte(source, '>'); end >= 0 {
		head = source[:end]
	}
	result := map[string]string{}
	for _, match := range tagAttributePattern.FindAllStringSubmatch(head, -1) {
		if len(match) >= 3 {
			result[match[1]] = match[2]
		}
	}
	return result
}

func parseWrappedTag(source, tag string) (map[string]string, string, bool) {
	trimmed := strings.TrimSpace(source)
	if !strings.HasPrefix(trimmed, "<"+tag) {
		return nil, "", false
	}
	openEnd := strings.IndexByte(trimmed, '>')
	closeStart := strings.LastIndex(trimmed, "</"+tag+">")
	if openEnd < 0 || closeStart < openEnd {
		return nil, "", false
	}
	body := trimmed[openEnd+1 : closeStart]
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimRight(body, "\n \t")
	return tagAttributes(trimmed[:openEnd+1]), body, true
}

func componentResults(metadata map[string]any) map[string]any {
	dil, _ := metadata["model_dil_v2"].(map[string]any)
	appData, _ := dil["appData"].(map[string]any)
	opGenui, _ := appData["opGenui"].(map[string]any)
	results, _ := opGenui["componentResults"].(map[string]any)
	return results
}

func resolvedState(results map[string]any, id string) map[string]any {
	result, _ := results[id].(map[string]any)
	state, _ := result["state"].(map[string]any)
	return state
}

func compactImages(value any) []any {
	list, _ := value.([]any)
	images := make([]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		image := map[string]any{}
		for _, key := range []string{"thumbnail_url", "content_url", "url", "title"} {
			if text := stringValue(entry[key]); text != "" {
				image[key] = text
			}
		}
		if len(image) > 0 {
			images = append(images, image)
		}
	}
	return images
}

func chartData(source string) map[string]any {
	open := strings.Index(source, "{{")
	close := strings.LastIndex(source, "}}")
	if open < 0 || close <= open {
		return nil
	}
	var content map[string]any
	if json.Unmarshal([]byte("{"+source[open+2:close]+"}"), &content) != nil {
		return nil
	}
	data, _ := content["data"].([]any)
	if len(data) == 0 {
		return nil
	}
	result := map[string]any{
		"chartType": stringValue(content["chartType"]),
		"data":      data,
	}
	for _, key := range []string{"xKey", "nameKey", "valueKey"} {
		if text := stringValue(content[key]); text != "" {
			result[key] = text
		}
	}
	if series, ok := content["series"].([]any); ok && len(series) > 0 {
		compact := make([]any, 0, len(series))
		for _, raw := range series {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			item := map[string]any{}
			for _, key := range []string{"dataKey", "label", "valuePrefix", "valueSuffix"} {
				if text := stringValue(entry[key]); text != "" {
					item[key] = text
				}
			}
			if len(item) > 0 {
				compact = append(compact, item)
			}
		}
		if len(compact) > 0 {
			result["series"] = compact
		}
	}
	if meta, ok := content["meta"].(map[string]any); ok {
		if description := stringValue(meta["description"]); description != "" {
			result["description"] = description
		}
	}
	return result
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
	ConversationID string           `json:"conversation_id"`
	MessageID      string           `json:"message_id,omitempty"`
	Text           string           `json:"text"`
	GenUI          []GenUIComponent `json:"genui,omitempty"`
}

func (c *Client) SendMessage(ctx context.Context, conversationID, parentID, text string, attachments []Attachment, onDelta, onReplace func(string) error) (SendResult, error) {
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

	convID, msgID, full, err := readConversationSSE(resp.Body, onDelta, onReplace)
	if convID == "" {
		convID = conversationID
	}
	result := SendResult{ConversationID: convID, MessageID: msgID, Text: strings.TrimSpace(full)}
	if err == nil && convID != "" {
		// 读一次历史，拿稳定的 message id、content.parts 文本和 genui 范围。
		// 这样 SSE 的 done 事件和历史接口返回的结构一致。
		if conv, getErr := c.GetConversation(ctx, convID); getErr == nil {
			var chosen *Message
			if result.MessageID != "" {
				for index := range conv.Messages {
					if conv.Messages[index].ID == result.MessageID {
						chosen = &conv.Messages[index]
						break
					}
				}
			}
			if chosen == nil {
				for index := len(conv.Messages) - 1; index >= 0; index-- {
					if conv.Messages[index].Role == "assistant" && strings.TrimSpace(conv.Messages[index].Text) != "" {
						chosen = &conv.Messages[index]
						break
					}
				}
			}
			if chosen != nil {
				if strings.TrimSpace(chosen.Text) != "" {
					result.Text = chosen.Text
					if full == "" && onDelta != nil {
						_ = onDelta(result.Text)
					}
				}
				result.MessageID = chosen.ID
				result.GenUI = chosen.GenUI
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

type partsSSEState struct {
	currentID string
	current   string
	bestID    string
	best      string
	emitted   string
	emittedID string
}

func (s *partsSSEState) update(value string, onDelta, onReplace func(string) error) error {
	if value == "" {
		return nil
	}
	s.current = value
	s.best = value
	s.bestID = s.currentID
	if onDelta == nil && onReplace == nil {
		return nil
	}
	if s.emittedID == "" {
		s.emittedID = s.currentID
	}
	// 只对当前消息做增量输出；切换到新的助手消息时等 done 再整体替换。
	if s.emittedID != s.currentID {
		return nil
	}
	if value == s.emitted {
		return nil
	}
	if strings.HasPrefix(value, s.emitted) {
		next := value[len(s.emitted):]
		s.emitted = value
		if next != "" && onDelta != nil {
			return onDelta(next)
		}
		return nil
	}
	// content.parts 也可能用 replace 覆盖整段文本，无法用增量表达。
	s.emitted = value
	if onReplace != nil {
		return onReplace(value)
	}
	return nil
}

func (s *partsSSEState) setMessage(message map[string]any, onDelta, onReplace func(string) error) error {
	author, _ := message["author"].(map[string]any)
	if author["role"] != "assistant" {
		return nil
	}
	channel, _ := message["channel"].(string)
	if channel != "" && channel != "final" {
		return nil
	}
	id, _ := message["id"].(string)
	if id != "" && id != s.currentID {
		s.currentID = id
		s.current = ""
	}
	content, _ := message["content"].(map[string]any)
	if value := contentText(content); value != "" {
		return s.update(value, onDelta, onReplace)
	}
	return nil
}

func (s *partsSSEState) applyPatch(operation map[string]any, onDelta, onReplace func(string) error) error {
	path, _ := operation["p"].(string)
	action, _ := operation["o"].(string)
	value, _ := operation["v"].(string)
	switch {
	case path == "/message/content/parts/0":
		switch action {
		case "append":
			return s.update(s.current+value, onDelta, onReplace)
		case "replace", "add":
			if value != "" {
				return s.update(value, onDelta, onReplace)
			}
			if action == "replace" {
				s.current = ""
			}
		case "remove":
			s.current = ""
		}
	case path == "/message/content/parts":
		if parts, ok := operation["v"].([]any); ok {
			s.current = ""
			if text := contentText(map[string]any{"parts": parts}); text != "" {
				return s.update(text, onDelta, onReplace)
			}
		}
	case path == "/message/content":
		if content, ok := operation["v"].(map[string]any); ok {
			if text := contentText(content); text != "" {
				return s.update(text, onDelta, onReplace)
			}
		}
	case path == "/message":
		if message, ok := operation["v"].(map[string]any); ok {
			return s.setMessage(message, onDelta, onReplace)
		}
	}
	return nil
}

func processPartsEvent(event map[string]any, state *partsSSEState, onDelta, onReplace func(string) error) error {
	if message, ok := event["message"].(map[string]any); ok {
		if err := state.setMessage(message, onDelta, onReplace); err != nil {
			return err
		}
	}
	if nested, ok := event["v"].(map[string]any); ok {
		if message, ok := nested["message"].(map[string]any); ok {
			if err := state.setMessage(message, onDelta, onReplace); err != nil {
				return err
			}
		}
		if _, ok := nested["p"]; ok {
			if err := state.applyPatch(nested, onDelta, onReplace); err != nil {
				return err
			}
		}
	}
	if operations, ok := event["v"].([]any); ok {
		for _, raw := range operations {
			operation, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if value, ok := operation["v"].(map[string]any); ok {
				if message, ok := value["message"].(map[string]any); ok {
					if err := state.setMessage(message, onDelta, onReplace); err != nil {
						return err
					}
				}
			}
			if _, ok := operation["p"]; ok {
				if err := state.applyPatch(operation, onDelta, onReplace); err != nil {
					return err
				}
			}
		}
	}
	if _, ok := event["p"]; ok {
		if err := state.applyPatch(event, onDelta, onReplace); err != nil {
			return err
		}
	}
	// 文本增量有时只带一个 v 字符串、没有 p 路径（事件只有 v 一个字段）。
	if len(event) == 1 {
		if value, ok := event["v"].(string); ok && state.currentID != "" {
			return state.update(state.current+value, onDelta, onReplace)
		}
	}
	return nil
}

// readConversationSSE 从 content.parts 读取助手正文。
// 文本通常通过 /message/content/parts/0 的 append/replace patch 下发，
// 这里维护状态并输出增量；完整的消息也可能直接出现在 add 事件里。
func readConversationSSE(reader io.Reader, onDelta, onReplace func(string) error) (string, string, string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	conversationID := ""
	upstreamError := ""
	state := &partsSSEState{}
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
		if err := processPartsEvent(event, state, onDelta, onReplace); err != nil {
			return conversationID, state.bestID, state.best, err
		}
	}
	if err := scanner.Err(); err != nil {
		return conversationID, state.bestID, state.best, err
	}
	text := state.best
	if text == "" {
		text = state.current
	}
	messageID := state.bestID
	if messageID == "" {
		messageID = state.currentID
	}
	text = strings.TrimSpace(text)
	if text == "" && upstreamError != "" {
		return conversationID, messageID, text, fmt.Errorf("ChatGPT 返回错误：%s", upstreamError)
	}
	return conversationID, messageID, text, nil
}
