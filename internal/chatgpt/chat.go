package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/google/uuid"
)

// ---------------- chat ----------------

type SendResult struct {
	ConversationID string           `json:"conversation_id"`
	MessageID      string           `json:"message_id,omitempty"`
	Text           string           `json:"text"`
	GenUI          []GenUIComponent `json:"genui,omitempty"`
}

func (c *Client) SendMessage(ctx context.Context, conversationID, parentID, text string, attachments []Attachment, onDelta, onReplace func(string) error, onGenUI func([]GenUIComponent) error) (SendResult, error) {
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

	convID, msgID, full, err := readConversationSSE(resp.Body, onDelta, onReplace, onGenUI)
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
