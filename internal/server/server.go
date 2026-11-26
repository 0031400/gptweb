package server

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gptweb/internal/chatgpt"
	"gptweb/internal/config"
)

type Server struct {
	cfg     *config.Config
	clients map[string]*chatgpt.Client
	tokens  []config.Token
	secret  []byte
	webDir  string
	mux     *http.ServeMux
}

func New(cfg *config.Config) (*Server, error) {
	s := &Server{
		cfg:     cfg,
		clients: map[string]*chatgpt.Client{},
		tokens:  cfg.Tokens,
		secret:  []byte(cfg.SessionSecret),
		webDir:  cfg.Resolve(cfg.WebDir),
	}
	for _, t := range cfg.Tokens {
		client, err := chatgpt.NewClient(chatgpt.Options{
			Tag:           t.Tag,
			Name:          t.Name,
			SessionFile:   cfg.Resolve(t.SessionFile),
			ClientTimeout: cfg.RequestTimeout(),
		})
		if err != nil {
			return nil, fmt.Errorf("初始化 token %q 失败: %w", t.Tag, err)
		}
		s.clients[t.Tag] = client
	}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler {
	return s.withAuth(s.mux)
}

func (s *Server) routes() {
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/me", s.handleMe)
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/tokens", s.handleTokens)
	s.mux.HandleFunc("GET /api/conversations", s.handleListConversations)
	s.mux.HandleFunc("GET /api/conversations/{id}", s.handleGetConversation)
	s.mux.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)
	s.mux.HandleFunc("POST /api/upload", s.handleUpload)
	s.mux.HandleFunc("GET /api/files/{id}", s.handleFile)
	s.mux.HandleFunc("POST /api/chat", s.handleChat)
	s.mux.HandleFunc("/", s.serveStatic)
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
		writeError(w, http.StatusNotFound, "接口不存在")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	rel := strings.TrimPrefix(clean, "/")
	file := filepath.Join(s.webDir, filepath.FromSlash(rel))
	if info, err := os.Stat(file); err == nil && !info.IsDir() {
		if strings.HasPrefix(rel, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeFile(w, r, file)
		return
	}
	if rel != "" && path.Ext(rel) != "" {
		http.NotFound(w, r)
		return
	}
	index := filepath.Join(s.webDir, "index.html")
	if _, err := os.Stat(index); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>GptWeb</title>
<body style="font-family:system-ui;padding:40px;line-height:1.7">
<h2>前端文件还没有构建</h2>
<p>请先执行 <code>cd web &amp;&amp; yarn install &amp;&amp; yarn build</code>，或把 config.json 里的 <code>web_dir</code> 指向已构建的目录。</p>
<p>当前目录：%s</p></body>`, s.webDir)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFile(w, r, index)
}

func (s *Server) LogConfig() {
	exists := "存在"
	if _, err := os.Stat(s.webDir); err != nil {
		exists = "不存在（前端未构建）"
	}
	log.Printf("前端目录：%s（%s）", s.webDir, exists)
	for _, t := range s.tokens {
		st := s.clients[t.Tag].Status()
		status := "就绪"
		if !st.Ready {
			status = st.Error
		}
		log.Printf("token [%s] %s：%s", t.Tag, t.Name, status)
	}
}
