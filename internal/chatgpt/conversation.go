package chatgpt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

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
