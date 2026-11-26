package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gptweb/internal/chatgpt"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "请求内容格式不正确")
		return false
	}
	return true
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	user, ok := s.cfg.User(strings.TrimSpace(body.Username))
	if !ok || !passwordsEqual(user.Password, body.Password) {
		writeError(w, http.StatusUnauthorized, "用户名或密码不正确")
		return
	}
	s.issueCookie(w, r, user.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   user.Username,
		"token_tags": user.TokenTags,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"username":   user.Username,
		"token_tags": user.TokenTags,
	})
}

func (s *Server) resolveClient(w http.ResponseWriter, r *http.Request, tag string) (*chatgpt.Client, bool) {
	user, _ := userFrom(r.Context())
	tag = strings.TrimSpace(tag)
	if tag == "" && len(user.TokenTags) == 1 {
		tag = user.TokenTags[0]
	}
	if tag == "" {
		writeError(w, http.StatusBadRequest, "请选择一个 token")
		return nil, false
	}
	if !user.CanUse(tag) {
		writeError(w, http.StatusForbidden, "无权使用该 token")
		return nil, false
	}
	client, ok := s.clients[tag]
	if !ok {
		writeError(w, http.StatusNotFound, "token 不存在")
		return nil, false
	}
	return client, true
}

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r, r.URL.Query().Get("tag"))
	if !ok {
		return
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = 30
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	items, total, err := client.ListConversations(ctx, offset, limit)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "offset": offset, "limit": limit})
}

func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r, r.URL.Query().Get("tag"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	conversation, err := client.GetConversation(ctx, r.PathValue("id"))
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conversation)
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r, r.URL.Query().Get("tag"))
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := client.DeleteConversation(ctx, r.PathValue("id")); err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 26<<20)
	if err := r.ParseMultipartForm(26 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "上传内容过大或格式不正确")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	client, ok := s.resolveClient(w, r, r.FormValue("tag"))
	if !ok {
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "没有收到文件")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 20<<20+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取上传文件失败")
		return
	}
	if len(data) > 20<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "单个文件不能超过 20MB")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	attachment, err := client.Upload(ctx, header.Filename, header.Header.Get("Content-Type"), data)
	if err != nil {
		writeServerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, attachment)
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	client, ok := s.resolveClient(w, r, r.URL.Query().Get("tag"))
	if !ok {
		return
	}
	fileID := r.PathValue("id")
	if fileID == "" {
		writeError(w, http.StatusBadRequest, "文件编号不能为空")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	data, contentType, err := client.DownloadFile(ctx, fileID, r.URL.Query().Get("conversation_id"))
	if err != nil {
		writeServerError(w, err)
		return
	}
	name := filepath.Base(r.URL.Query().Get("name"))
	if name == "" || name == "." {
		name = fileID
	}
	if contentType == "" || contentType == "application/octet-stream" {
		if byName := mime.TypeByExtension(filepath.Ext(name)); byName != "" {
			contentType = byName
		}
	}
	if (contentType == "" || contentType == "application/octet-stream") && r.URL.Query().Get("kind") == "image" {
		contentType = "image/png"
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := "inline"
	if !strings.HasPrefix(contentType, "image/") && !strings.HasPrefix(contentType, "text/") && contentType != "application/pdf" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q; filename*=UTF-8''%s", disposition, asciiFilename(name), url.PathEscape(name)))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func asciiFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 32 || r > 126 || r == '"' || r == '\\' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Tag             string               `json:"tag"`
		ConversationID  string               `json:"conversation_id"`
		ParentMessageID string               `json:"parent_message_id"`
		Message         string               `json:"message"`
		Attachments     []chatgpt.Attachment `json:"attachments"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	body.Message = strings.TrimSpace(body.Message)
	if body.Message == "" && len(body.Attachments) == 0 {
		writeError(w, http.StatusBadRequest, "消息不能为空")
		return
	}
	if len([]rune(body.Message)) > 20000 {
		writeError(w, http.StatusBadRequest, "消息太长（最多 20000 字）")
		return
	}
	if len(body.Attachments) > 8 {
		writeError(w, http.StatusBadRequest, "一次最多发送 8 个附件")
		return
	}
	for _, attachment := range body.Attachments {
		if strings.TrimSpace(attachment.ID) == "" {
			writeError(w, http.StatusBadRequest, "附件编号不能为空")
			return
		}
	}
	client, ok := s.resolveClient(w, r, body.Tag)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前服务器不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := &sseWriter{w: w, flusher: flusher}
	stream.event(map[string]any{"type": "start"})

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout())
	defer cancel()

	sentDelta := false
	result, err := client.SendMessage(ctx, body.ConversationID, body.ParentMessageID, body.Message, body.Attachments, func(delta string) error {
		sentDelta = true
		return stream.event(map[string]any{"type": "delta", "text": delta})
	})
	if err != nil {
		stream.event(map[string]any{"type": "error", "message": displayError(err)})
		return
	}
	if !sentDelta && result.Text != "" {
		_ = stream.event(map[string]any{"type": "delta", "text": result.Text})
	}
	stream.event(map[string]any{
		"type":            "done",
		"conversation_id": result.ConversationID,
		"message_id":      result.MessageID,
		"text":            result.Text,
	})
}

func displayError(err error) string {
	switch {
	case errors.Is(err, chatgpt.ErrUnauthorized):
		return "ChatGPT 登录态已失效，请更新 session 文件后重试"
	case errors.Is(err, chatgpt.ErrRateLimited):
		return "ChatGPT 请求过于频繁，请稍后再试"
	}
	return err.Error()
}

func writeServerError(w http.ResponseWriter, err error) {
	// 上游 ChatGPT 登录态失效不能返回 401，否则前端会误以为自己的 GptWeb 登录过期。
	status := http.StatusBadGateway
	if errors.Is(err, chatgpt.ErrRateLimited) {
		status = http.StatusTooManyRequests
	}
	writeError(w, status, displayError(err))
}

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	dead    bool
}

func (s *sseWriter) event(value any) error {
	if s.dead {
		return errors.New("连接已关闭")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", data); err != nil {
		s.dead = true
		return err
	}
	s.flusher.Flush()
	return nil
}
