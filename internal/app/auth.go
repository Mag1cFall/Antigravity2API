package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/api"
	"github.com/Mag1cFall/Antigravity2API/internal/config"
)

const (
	// sessionCookie 是管理会话 Cookie 名
	sessionCookie = "antigravity_admin"
	// sessionLifetime 是管理会话有效期
	sessionLifetime = 12 * time.Hour
	// loginFailures 是同一来源一分钟内允许的登录失败次数
	loginFailures = 5
	// sessionPath 是会话接口路径，不要求已登录
	sessionPath = "/api/session"
)

// sessionState 是 /api/session 的响应
type sessionState struct {
	// Enabled 表示开启了 ADMIN_AUTH_ENABLED
	Enabled bool `json:"enabled"`
	// Authenticated 表示当前请求能访问管理 API：回环访问或已登录
	Authenticated bool `json:"authenticated"`
}

// adminSession 是一个管理会话，ctx 在退出登录、到期或登录配置变化时取消
type adminSession struct {
	ctx     context.Context
	cancel  context.CancelFunc
	expires time.Time
}

// loginAttempt 是来源地址在一分钟窗口内的失败次数
type loginAttempt struct {
	count int
	until time.Time
}

// adminAuth 保存管理登录配置、会话与登录失败计数
type adminAuth struct {
	mu       sync.Mutex
	enabled  bool
	username string
	password string
	sessions map[string]*adminSession
	attempts map[string]loginAttempt
}

// newAdminAuth 按启动时的管理登录配置创建认证
func newAdminAuth(cfg config.Config) *adminAuth {
	auth := &adminAuth{sessions: map[string]*adminSession{}, attempts: map[string]loginAttempt{}}
	auth.update(cfg)
	return auth
}

// update 应用新的管理登录配置，开关、用户名或密码变化时撤销全部会话
func (auth *adminAuth) update(cfg config.Config) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	if auth.enabled == cfg.AdminAuthEnabled && auth.username == cfg.AdminUsername && auth.password == cfg.AdminPassword {
		return
	}
	auth.enabled, auth.username, auth.password = cfg.AdminAuthEnabled, cfg.AdminUsername, cfg.AdminPassword
	for token := range auth.sessions {
		auth.remove(token)
	}
}

// requireAdmin 要求同源浏览器请求；回环请求直接放行，远程请求需要有效会话，未开启登录时远程请求无法登录
func (s *server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "admin_origin_forbidden", "管理 API 只接受同源请求")
			return
		}
		if r.URL.Path == sessionPath || loopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		session, enabled := s.auth.session(r)
		if !enabled {
			writeError(w, http.StatusUnauthorized, "admin_remote_disabled", "远程访问管理端需要开启 ADMIN_AUTH_ENABLED")
			return
		}
		if session == nil {
			writeError(w, http.StatusUnauthorized, "admin_login_required", "请登录管理端")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(session.ctx, cancel)
		defer stop()
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// session 返回请求 Cookie 对应的有效会话与是否开启了登录
func (auth *adminAuth) session(r *http.Request) (*adminSession, bool) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || !auth.enabled {
		return nil, auth.enabled
	}
	session := auth.sessions[cookie.Value]
	if session != nil && !time.Now().Before(session.expires) {
		auth.remove(cookie.Value)
		return nil, true
	}
	return session, true
}

// getSession 返回当前请求的会话状态
func (s *server) getSession(w http.ResponseWriter, r *http.Request) {
	session, enabled := s.auth.session(r)
	writeJSON(w, http.StatusOK, sessionState{Enabled: enabled, Authenticated: loopback(r) || session != nil})
}

// login 校验用户名与密码并签发会话 Cookie，同一客户端 IP 一分钟内失败 5 次后拒绝
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	auth, peer, now := s.auth, clientIP(r), time.Now()
	auth.mu.Lock()
	if !auth.enabled {
		auth.mu.Unlock()
		writeError(w, http.StatusBadRequest, "admin_auth_disabled", "未开启 ADMIN_AUTH_ENABLED")
		return
	}
	for address, attempt := range auth.attempts {
		if !now.Before(attempt.until) {
			delete(auth.attempts, address)
		}
	}
	if auth.attempts[peer].count >= loginFailures {
		auth.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "login_rate_limited", "登录失败次数过多，请一分钟后再试")
		return
	}
	userOK := subtle.ConstantTimeCompare([]byte(strings.TrimSpace(input.Username)), []byte(auth.username))
	passwordOK := subtle.ConstantTimeCompare([]byte(input.Password), []byte(auth.password))
	if userOK&passwordOK != 1 {
		attempt := auth.attempts[peer]
		if attempt.count == 0 {
			attempt.until = now.Add(time.Minute)
		}
		attempt.count++
		auth.attempts[peer] = attempt
		auth.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	delete(auth.attempts, peer)
	if old, err := r.Cookie(sessionCookie); err == nil {
		auth.remove(old.Value)
	}
	token, expires := rand.Text(), now.Add(sessionLifetime)
	ctx, cancel := context.WithDeadline(context.Background(), expires)
	auth.sessions[token] = &adminSession{ctx: ctx, cancel: cancel, expires: expires}
	auth.mu.Unlock()
	setSessionCookie(w, r, token, int(sessionLifetime.Seconds()))
	writeJSON(w, http.StatusOK, sessionState{Enabled: true, Authenticated: true})
}

// logout 撤销当前会话并清除 Cookie
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.auth.mu.Lock()
		s.auth.remove(cookie.Value)
		s.auth.mu.Unlock()
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// remove 在持锁期间撤销会话
func (auth *adminAuth) remove(token string) {
	if session := auth.sessions[token]; session != nil {
		session.cancel()
		delete(auth.sessions, token)
	}
}

// setSessionCookie 写入只供管理 API 使用的 HttpOnly 会话 Cookie
func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, age int) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/api", MaxAge: age, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
	})
}

// loopback 判断请求来自本机回环：对端与 Host 都是回环地址，且没有经过反向代理
func loopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() || !api.LoopbackHost(r.Host) {
		return false
	}
	return r.Header.Get("X-Forwarded-For") == "" && r.Header.Get("Forwarded") == "" && r.Header.Get("X-Real-IP") == ""
}

// sameOrigin 拒绝跨站浏览器请求与 Origin 和 Host 不一致的请求
func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	value := strings.TrimSpace(r.Header.Get("Origin"))
	if value == "" {
		return true
	}
	origin, err := url.Parse(value)
	return err == nil && (origin.Scheme == "http" || origin.Scheme == "https") && origin.User == nil &&
		origin.Host != "" && strings.EqualFold(origin.Host, r.Host)
}
