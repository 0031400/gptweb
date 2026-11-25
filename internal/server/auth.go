package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"gptweb/internal/config"
)

const sessionCookie = "gptweb_session"

type authClaims struct {
	Username string `json:"u"`
	Expires  int64  `json:"e"`
}

type contextKey string

const userContextKey contextKey = "user"

func userFrom(ctx context.Context) (config.User, bool) {
	u, ok := ctx.Value(userContextKey).(config.User)
	return u, ok
}

func (s *Server) sign(value string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) issueCookie(w http.ResponseWriter, r *http.Request, username string) {
	claims := authClaims{Username: username, Expires: time.Now().Add(time.Duration(s.cfg.SessionHours) * time.Hour).Unix()}
	data, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(data)
	value := payload + "." + s.sign(payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   s.cfg.SessionHours * 3600,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteLaxMode})
}

func (s *Server) readCookie(r *http.Request) (config.User, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return config.User{}, false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return config.User{}, false
	}
	if !hmac.Equal([]byte(s.sign(parts[0])), []byte(parts[1])) {
		return config.User{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return config.User{}, false
	}
	var claims authClaims
	if json.Unmarshal(raw, &claims) != nil {
		return config.User{}, false
	}
	if time.Now().Unix() >= claims.Expires {
		return config.User{}, false
	}
	return s.cfg.User(claims.Username)
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasPrefix(path, "/api/") || path == "/api/login" || path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := s.readCookie(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}

func passwordsEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// loginLimiter 对连续失败做简单的按 IP 限制，避免暴力猜密码。
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
}

type loginEntry struct {
	count int
	until time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: map[string]*loginEntry{}}
}

func (l *loginLimiter) clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (l *loginLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		return false
	}
	if time.Now().After(e.until) {
		delete(l.entries, key)
		return false
	}
	return e.count >= 8
}

func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[key]
	if e == nil {
		e = &loginEntry{}
		l.entries[key] = e
	}
	e.count++
	e.until = time.Now().Add(10 * time.Minute)
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
