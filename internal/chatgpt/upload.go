package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	_ "golang.org/x/image/webp"
)

// Attachment 既用于上传结果，也用于历史消息里的图片/文件。
type Attachment struct {
	ID            string `json:"id"`
	LibraryFileID string `json:"library_file_id,omitempty"`
	Name          string `json:"name"`
	MimeType      string `json:"mime_type"`
	Size          int64  `json:"size"`
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	Kind          string `json:"kind"`
}

func (a Attachment) IsImage() bool {
	return a.Kind == "image" || strings.HasPrefix(strings.ToLower(a.MimeType), "image/")
}

var libraryIDPattern = regexp.MustCompile(`"library_file_id"\s*:\s*"([^"]+)"`)

// Upload 把图片/文件传给 ChatGPT，返回可以用于发送消息的附件信息。
func (c *Client) Upload(ctx context.Context, name, providedMime string, data []byte) (Attachment, error) {
	if len(data) == 0 {
		return Attachment{}, fmt.Errorf("文件内容为空")
	}
	s, err := c.sessionOrError(false)
	if err != nil {
		return Attachment{}, err
	}
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." {
		name = "file"
	}
	mimeType := normalizeMime(name, providedMime)
	isImage := strings.HasPrefix(mimeType, "image/")
	useCase := "my_files"
	indexForRetrieval := true
	if isImage {
		useCase = "multimodal"
		indexForRetrieval = false
	}

	createBody := map[string]any{
		"entry_surface":                       "chat_composer",
		"file_name":                           name,
		"file_size":                           len(data),
		"client_resolved_mime_type":           mimeType,
		"mime_resolution_source":              "filename_extension",
		"mime_type":                           mimeType,
		"reset_rate_limits":                   false,
		"supports_direct_azure_multipart":     true,
		"supports_web_upload_transport_layer": true,
		"supports_direct_azure_multipart_connection_sharding": false,
		"timezone_offset_min":      -480,
		"use_case":                 useCase,
		"store_in_library":         true,
		"library_persistence_mode": "opportunistic",
	}
	resp, err := c.request(ctx, s, fhttp.MethodPost, "/backend-api/files", createBody, nil)
	if err != nil {
		return Attachment{}, err
	}
	if resp.StatusCode != fhttp.StatusOK {
		return Attachment{}, readError(resp, "创建上传任务")
	}
	var created struct {
		UploadURL string `json:"upload_url"`
		FileID    string `json:"file_id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if err != nil {
		return Attachment{}, err
	}
	if created.UploadURL == "" || created.FileID == "" {
		return Attachment{}, fmt.Errorf("ChatGPT 没有返回上传地址")
	}

	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodPut, created.UploadURL, bytes.NewReader(data))
	if err != nil {
		return Attachment{}, err
	}
	req.Header.Set("Content-Type", mimeType)
	req.Header.Set("x-ms-blob-type", "BlockBlob")
	req.Header.Set("x-ms-version", "2020-04-08")
	req.Header.Set("x-ms-blob-content-type", mimeType)
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("User-Agent", upstreamUA)
	uploadResp, err := c.http.Do(req)
	if err != nil {
		return Attachment{}, fmt.Errorf("上传文件内容失败: %w", err)
	}
	uploadBody, _ := io.ReadAll(io.LimitReader(uploadResp.Body, 8192))
	uploadResp.Body.Close()
	if uploadResp.StatusCode < 200 || uploadResp.StatusCode >= 300 {
		return Attachment{}, fmt.Errorf("上传文件内容失败（HTTP %d）：%s", uploadResp.StatusCode, strings.TrimSpace(string(uploadBody)))
	}

	processBody := map[string]any{
		"entry_surface":            "chat_composer",
		"file_id":                  created.FileID,
		"file_name":                name,
		"index_for_retrieval":      indexForRetrieval,
		"library_persistence_mode": "opportunistic",
		"metadata":                 map[string]any{"store_in_library": true, "is_project_thread": false},
		"mime_type":                mimeType,
		"use_case":                 useCase,
	}
	processResp, err := c.request(ctx, s, fhttp.MethodPost, "/backend-api/files/process_upload_stream", processBody, map[string]string{"Accept": "text/event-stream"})
	if err != nil {
		return Attachment{}, err
	}
	if processResp.StatusCode != fhttp.StatusOK {
		return Attachment{}, readError(processResp, "处理上传文件")
	}
	processBytes, _ := io.ReadAll(io.LimitReader(processResp.Body, 4<<20))
	processResp.Body.Close()

	attachment := Attachment{
		ID:       created.FileID,
		Name:     name,
		MimeType: mimeType,
		Size:     int64(len(data)),
		Kind:     "file",
	}
	if match := libraryIDPattern.FindSubmatch(processBytes); len(match) == 2 {
		attachment.LibraryFileID = string(match[1])
	}
	if isImage {
		attachment.Kind = "image"
		attachment.Width, attachment.Height = imageDimensions(data)
	}
	return attachment, nil
}

func normalizeMime(name, provided string) string {
	value := strings.ToLower(strings.TrimSpace(strings.Split(provided, ";")[0]))
	if value == "" || value == "application/octet-stream" {
		if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
			value = strings.ToLower(strings.Split(t, ";")[0])
		}
	}
	if value == "" || value == "application/octet-stream" {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".md", ".markdown", ".txt", ".log", ".csv", ".tsv", ".json", ".yaml", ".yml",
			".go", ".js", ".jsx", ".ts", ".tsx", ".py", ".java", ".c", ".cc", ".cpp", ".h",
			".html", ".css", ".xml", ".sql", ".sh", ".ps1", ".ini", ".toml", ".conf":
			value = "text/plain"
		}
	}
	if value == "" {
		value = "application/octet-stream"
	}
	return value
}

func imageDimensions(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// DownloadFile 读取 ChatGPT 文件并返回字节和内容类型。
func (c *Client) DownloadFile(ctx context.Context, fileID, conversationID string) ([]byte, string, error) {
	s, err := c.sessionOrError(false)
	if err != nil {
		return nil, "", err
	}
	path := "/backend-api/files/download/" + url.PathEscape(fileID) + "?download_intent=false&inline=true"
	if conversationID != "" {
		path += "&conversation_id=" + url.QueryEscape(conversationID)
	}
	resp, err := c.request(ctx, s, fhttp.MethodGet, path, nil, map[string]string{"Accept": "*/*"})
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != fhttp.StatusOK {
		return nil, "", readError(resp, "获取文件下载地址")
	}
	var meta struct {
		DownloadURL string `json:"download_url"`
		MimeType    string `json:"mime_type"`
	}
	err = json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()
	if err != nil {
		return nil, "", err
	}
	if meta.DownloadURL == "" {
		return nil, "", fmt.Errorf("ChatGPT 没有返回文件下载地址")
	}

	req, err := fhttp.NewRequestWithContext(ctx, fhttp.MethodGet, meta.DownloadURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", upstreamUA)
	req.Header.Set("Referer", baseURL+"/library")
	if strings.HasPrefix(meta.DownloadURL, baseURL) {
		req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	}
	fileResp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer fileResp.Body.Close()
	if fileResp.StatusCode < 200 || fileResp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(fileResp.Body, 4096))
		return nil, "", fmt.Errorf("下载文件失败（HTTP %d）：%s", fileResp.StatusCode, strings.TrimSpace(string(body)))
	}
	data, err := io.ReadAll(io.LimitReader(fileResp.Body, 60<<20))
	if err != nil {
		return nil, "", err
	}
	contentType := fileResp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = meta.MimeType
	}
	return data, contentType, nil
}
