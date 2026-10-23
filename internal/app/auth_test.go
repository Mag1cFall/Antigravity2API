package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mag1cFall/Antigravity2API/internal/config"
)

// newAuthServer 创建只含运行配置与管理认证的 server
func newAuthServer(cfg config.Config) *server {
	live := &atomic.Pointer[config.Config]{}
	live.Store(&cfg)
	return &server{live: live, boot: cfg, auth: newAdminAuth(cfg)}
}

// adminRequest 以指定对端地址、Host、请求头与 Cookie 访问 handler 并返回状态码与错误码
func adminRequest(handler http.Handler, method, path, remote, host string, header map[string]string, body string) (int, string, *http.Response) {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr, request.Host = remote, host
	for name, value := range header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	code := ""
	if text := recorder.Body.String(); strings.Contains(text, `"code":"`) {
		code = strings.Split(strings.Split(text, `"code":"`)[1], `"`)[0]
	}
	return recorder.Code, code, recorder.Result()
}

// TestRemoteAdminRequiresLogin 验证非回环来源与带转发头或非回环 Host 的回环连接都需要登录，回环直连免登录，退出登录与改密后会话失效
func TestRemoteAdminRequiresLogin(t *testing.T) {
	cfg := config.Default()
	cfg.AdminAuthEnabled, cfg.AdminPassword = true, "pass"
	s := newAuthServer(cfg)
	admin := http.NewServeMux()
	admin.HandleFunc("POST /api/session", s.login)
	admin.HandleFunc("DELETE /api/session", s.logout)
	admin.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := s.requireAdmin(admin)
	remote := []struct {
		name, addr, host string
		header           map[string]string
	}{
		{"非回环对端", "203.0.113.5:4000", "127.0.0.1:8017", nil},
		{"X-Forwarded-For 写回环", "127.0.0.1:5000", "127.0.0.1:8017", map[string]string{"X-Forwarded-For": "127.0.0.1"}},
		{"X-Real-IP 写回环", "127.0.0.1:5000", "127.0.0.1:8017", map[string]string{"X-Real-IP": "127.0.0.1"}},
		{"Forwarded 写回环", "127.0.0.1:5000", "127.0.0.1:8017", map[string]string{"Forwarded": "for=127.0.0.1"}},
		{"非回环 Host", "127.0.0.1:5000", "admin.example.com", nil},
	}
	for _, item := range remote {
		for _, route := range [][2]string{{"GET", "/api/status"}, {"PATCH", "/api/settings"}, {"GET", "/api/events"}, {"GET", "/api/unknown"}} {
			if status, code, _ := adminRequest(handler, route[0], route[1], item.addr, item.host, item.header, ""); status != http.StatusUnauthorized || code != "admin_login_required" {
				t.Errorf("%s %s %s: %d %s", item.name, route[0], route[1], status, code)
			}
		}
	}
	if status, _, _ := adminRequest(handler, "GET", "/api/status", "[::1]:5000", "localhost:8017", nil, ""); status != http.StatusNoContent {
		t.Fatalf("回环直连 %d", status)
	}
	if status, code, _ := adminRequest(handler, "GET", "/api/status", "127.0.0.1:5000", "127.0.0.1:8017", map[string]string{"Origin": "http://evil.example"}, ""); status != http.StatusForbidden || code != "admin_origin_forbidden" {
		t.Fatalf("跨站请求 %d %s", status, code)
	}
	status, _, response := adminRequest(handler, "POST", "/api/session", "203.0.113.5:4000", "proxy.example.com", nil, `{"username":"admin","password":"pass"}`)
	if status != http.StatusOK || len(response.Cookies()) != 1 {
		t.Fatalf("登录 %d", status)
	}
	cookie := map[string]string{"Cookie": sessionCookie + "=" + response.Cookies()[0].Value}
	if status, _, _ := adminRequest(handler, "GET", "/api/status", "203.0.113.5:4000", "proxy.example.com", cookie, ""); status != http.StatusNoContent {
		t.Fatalf("登录后访问 %d", status)
	}
	changed := cfg
	changed.AdminPassword = "changed"
	s.auth.update(changed)
	if status, _, _ := adminRequest(handler, "GET", "/api/status", "203.0.113.5:4000", "proxy.example.com", cookie, ""); status != http.StatusUnauthorized {
		t.Fatalf("改密后旧会话 %d", status)
	}
	for range loginFailures {
		adminRequest(handler, "POST", "/api/session", "203.0.113.5:4000", "proxy.example.com", nil, `{"username":"admin","password":"bad"}`)
	}
	if status, code, _ := adminRequest(handler, "POST", "/api/session", "203.0.113.5:4000", "proxy.example.com", nil, `{"username":"admin","password":"changed"}`); status != http.StatusTooManyRequests {
		t.Fatalf("连续失败后登录 %d %s", status, code)
	}
	disabled := newAuthServer(config.Default())
	if status, code, _ := adminRequest(disabled.requireAdmin(admin), "GET", "/api/status", "203.0.113.5:4000", "127.0.0.1:8017", nil, ""); status != http.StatusUnauthorized || code != "admin_remote_disabled" {
		t.Fatalf("未开启登录的远程访问 %d %s", status, code)
	}
	if status, code, _ := adminRequest(http.HandlerFunc(s.pairAccount), "POST", "/api/pairing/accounts", "203.0.113.5:4000", "proxy.example.com", map[string]string{"Authorization": "Bearer guess"}, "{}"); status != http.StatusUnauthorized || code != "pairing_token_invalid" {
		t.Fatalf("配对令牌错误 %d %s", status, code)
	}
}
