package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gptweb/internal/config"
	"gptweb/internal/server"
)

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	if cfg.SessionSecret == "change-me-to-a-long-random-string" || len(cfg.SessionSecret) < 16 {
		log.Printf("警告：session_secret 仍为默认值或过短，部署前请修改")
	}
	for _, user := range cfg.Users {
		if user.Password == "admin123" {
			log.Printf("警告：用户 %s 仍在使用默认密码 admin123，请修改 config.json", user.Username)
		}
	}
	app, err := server.New(cfg)
	if err != nil {
		log.Fatalf("启动失败: %v", err)
	}
	app.LogConfig()

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	url := cfg.Listen
	if strings.HasPrefix(url, ":") {
		url = "http://127.0.0.1" + url
	} else if !strings.Contains(url, "://") {
		url = "http://" + url
	}
	log.Printf("GptWeb 已启动：%s", url)

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		log.Fatalf("HTTP 服务异常退出: %v", err)
	case <-stop:
		log.Printf("正在关闭…")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}
}
