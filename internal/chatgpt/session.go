package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Session 是一个 ChatGPT 登录态。它会被单独写入 session_file，
// 配置文件只保存这个文件的路径。
type Session struct {
	AccessToken string            `json:"access_token"`
	AccountID   string            `json:"account_id,omitempty"`
	Cookies     map[string]string `json:"cookies,omitempty"`
	Build       string            `json:"build,omitempty"`
	Email       string            `json:"email,omitempty"`
	Plan        string            `json:"plan,omitempty"`
	ExpiresAt   int64             `json:"expires_at,omitempty"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// sessionWire 同时兼容 ChatGPT /api/auth/session 的原始响应和旧版 snake_case 文件。
// 原始响应可以直接整段复制到 session_file 里。
type sessionWire struct {
	AccessToken       string            `json:"accessToken"`
	AccessTokenLegacy string            `json:"access_token"`
	Expires           string            `json:"expires"`
	ExpiresAt         int64             `json:"expires_at"`
	Cookies           map[string]string `json:"cookies"`
	Build             string            `json:"build"`
	Email             string            `json:"email"`
	Plan              string            `json:"plan"`
	AccountID         string            `json:"account_id"`
	User              struct {
		Email string `json:"email"`
	} `json:"user"`
	Account struct {
		ID       string `json:"id"`
		PlanType string `json:"planType"`
	} `json:"account"`
	SessionToken string `json:"sessionToken"`
}

func LoadSession(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wire sessionWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return nil, fmt.Errorf("解析 session 文件 %s 失败: %w", path, err)
	}
	s := &Session{
		AccessToken: firstNonEmpty(wire.AccessToken, wire.AccessTokenLegacy),
		AccountID:   firstNonEmpty(wire.Account.ID, wire.AccountID),
		Cookies:     wire.Cookies,
		Build:       wire.Build,
		Email:       firstNonEmpty(wire.User.Email, wire.Email),
		Plan:        firstNonEmpty(wire.Account.PlanType, wire.Plan),
		ExpiresAt:   wire.ExpiresAt,
	}
	if s.AccessToken == "" {
		return nil, fmt.Errorf("session 文件 %s 里没有 accessToken", path)
	}
	if s.ExpiresAt == 0 && wire.Expires != "" {
		if t, err := time.Parse(time.RFC3339, wire.Expires); err == nil {
			s.ExpiresAt = t.Unix()
		}
	}
	s.fillFromToken()
	if info, err := os.Stat(path); err == nil {
		s.UpdatedAt = info.ModTime().UTC()
	}
	return s, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func (s *Session) Save(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	s.UpdatedAt = time.Now()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Expired 判断 access token 是否已过期（留 60 秒余量）。
func (s *Session) Expired() bool {
	if s.AccessToken == "" {
		return true
	}
	exp := s.ExpiresAt
	if exp == 0 {
		exp = jwtExp(s.AccessToken)
	}
	if exp == 0 {
		return false
	}
	return time.Now().Unix() >= exp-60
}

func (s *Session) fillFromToken() {
	if s.AccessToken == "" {
		return
	}
	claims := jwtClaims(s.AccessToken)
	// JWT 是权威来源：即使 session 文件里的字段没跟着 token 一起更新，也以 token 为准。
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if v, ok := auth["chatgpt_account_id"].(string); ok && v != "" {
			s.AccountID = v
		}
		if v, ok := auth["chatgpt_plan_type"].(string); ok && v != "" {
			s.Plan = v
		}
	}
	if profile, ok := claims["https://api.openai.com/profile"].(map[string]any); ok {
		if v, ok := profile["email"].(string); ok && v != "" {
			s.Email = v
		}
	}
	if exp := jwtExp(s.AccessToken); exp > 0 {
		// JWT 里的 exp 才是权威值，避免 session 文件里手写的过期时间已经过时。
		s.ExpiresAt = exp
	}
}

func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

func jwtExp(token string) int64 {
	claims := jwtClaims(token)
	if claims == nil {
		return 0
	}
	switch v := claims["exp"].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}
