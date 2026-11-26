package chatgpt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Session 是一个 ChatGPT 登录态，仅供内部使用。
// 文件内容必须是 https://chatgpt.com/api/auth/session 的原始响应。
type Session struct {
	AccessToken string
	AccountID   string
	Email       string
	Plan        string
	ExpiresAt   int64
	UpdatedAt   time.Time
}

type sessionWire struct {
	AccessToken string `json:"accessToken"`
	Expires     string `json:"expires"`
	User        struct {
		Email string `json:"email"`
	} `json:"user"`
	Account struct {
		ID       string `json:"id"`
		PlanType string `json:"planType"`
	} `json:"account"`
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
	if wire.AccessToken == "" {
		return nil, fmt.Errorf("session 文件 %s 不是 chatgpt.com/api/auth/session 的格式（缺少 accessToken）", path)
	}
	s := &Session{
		AccessToken: wire.AccessToken,
		AccountID:   wire.Account.ID,
		Email:       wire.User.Email,
		Plan:        wire.Account.PlanType,
	}
	if wire.Expires != "" {
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
