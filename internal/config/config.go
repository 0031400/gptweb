package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config 是服务端唯一的配置文件。ChatGPT 的登录态不直接放在这里，
// 而是存放在每个 token 的 session_file 指向的 JSON 文件里。
type Config struct {
	Listen                string  `json:"listen"`
	WebDir                string  `json:"web_dir"`
	SessionSecret         string  `json:"session_secret"`
	SessionHours          int     `json:"session_hours"`
	RequestTimeoutSeconds int     `json:"request_timeout_seconds"`
	Users                 []User  `json:"users"`
	Tokens                []Token `json:"tokens"`

	// Dir 是配置文件所在目录，用于解析相对路径。
	Dir string `json:"-"`
}

type User struct {
	Username  string   `json:"username"`
	Password  string   `json:"password"`
	TokenTags []string `json:"token_tags"`
}

// Token 描述一个 ChatGPT 登录态以及它对应的标签。
// Tag 供用户配置引用，SessionFile 指向保存 access token / cookies 的文件。
type Token struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	SessionFile string `json:"session_file"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg.Dir = filepath.Dir(abs)
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.WebDir == "" {
		c.WebDir = "./web/dist"
	}
	if c.SessionHours <= 0 {
		c.SessionHours = 24 * 7
	}
	if c.RequestTimeoutSeconds <= 0 {
		c.RequestTimeoutSeconds = 300
	}
	if c.SessionSecret == "" {
		c.SessionSecret = "gptweb-change-this-secret"
	}
}

func (c *Config) validate() error {
	if len(c.Users) == 0 {
		return fmt.Errorf("users 不能为空")
	}
	tags := map[string]bool{}
	for i := range c.Tokens {
		t := &c.Tokens[i]
		t.Tag = strings.TrimSpace(t.Tag)
		if t.Tag == "" {
			return fmt.Errorf("tokens[%d].tag 不能为空", i)
		}
		if tags[t.Tag] {
			return fmt.Errorf("token tag %q 重复", t.Tag)
		}
		tags[t.Tag] = true
		if t.Name == "" {
			t.Name = t.Tag
		}
		if t.SessionFile == "" {
			t.SessionFile = filepath.Join("sessions", t.Tag+".json")
		}
	}
	for i, u := range c.Users {
		if strings.TrimSpace(u.Username) == "" {
			return fmt.Errorf("users[%d].username 不能为空", i)
		}
		if u.Password == "" {
			return fmt.Errorf("用户 %s 的密码不能为空", u.Username)
		}
		for _, tag := range u.TokenTags {
			if !tags[tag] {
				return fmt.Errorf("用户 %s 引用了不存在的 token tag %q", u.Username, tag)
			}
		}
	}
	return nil
}

func (c *Config) RequestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

// Resolve 把配置中的相对路径解析成绝对路径。
func (c *Config) Resolve(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(c.Dir, path)
}

func (c *Config) User(name string) (User, bool) {
	for _, u := range c.Users {
		if u.Username == name {
			return u, true
		}
	}
	return User{}, false
}

func (u User) CanUse(tag string) bool {
	for _, t := range u.TokenTags {
		if t == tag {
			return true
		}
	}
	return false
}
