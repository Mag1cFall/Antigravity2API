package app

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// pairingLifetime 是远程配对令牌的有效期
const pairingLifetime = 10 * time.Minute

// pairingToken 是 POST /api/pairing 的响应
type pairingToken struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// pairing 保存当前唯一有效配对令牌的摘要，令牌成功导入一个账户后失效
type pairing struct {
	mu      sync.Mutex
	digest  [sha256.Size]byte
	expires time.Time
}

// issue 生成新令牌并使旧令牌失效
func (p *pairing) issue(now time.Time) pairingToken {
	token := rand.Text()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.digest, p.expires = sha256.Sum256([]byte(token)), now.Add(pairingLifetime)
	return pairingToken{Token: token, ExpiresAt: p.expires.UTC()}
}

// valid 判断令牌是否为当前未过期的配对令牌
func (p *pairing) valid(token string, now time.Time) bool {
	digest := sha256.Sum256([]byte(token))
	p.mu.Lock()
	defer p.mu.Unlock()
	return now.Before(p.expires) && subtle.ConstantTimeCompare(digest[:], p.digest[:]) == 1
}

// revoke 使当前配对令牌失效
func (p *pairing) revoke() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expires = time.Time{}
}

// createPairing 生成供 login --remote 使用的配对令牌
func (s *server) createPairing(w http.ResponseWriter, r *http.Request) {
	token := s.pairing.issue(time.Now())
	slog.InfoContext(r.Context(), "远程配对令牌已生成 | 有效期=10m", "source", "auth")
	writeJSON(w, http.StatusCreated, token)
}

// pairAccount 凭配对令牌导入本机登录得到的 OAuth token，不经过管理会话
func (s *server) pairAccount(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	scheme, value, _ := strings.Cut(r.Header.Get("Authorization"), " ")
	if !strings.EqualFold(scheme, "Bearer") || !s.pairing.valid(strings.TrimSpace(value), time.Now()) {
		writeError(w, http.StatusUnauthorized, "pairing_token_invalid", "配对令牌无效或已过期")
		return
	}
	var token oauth2.Token
	if err := decodeJSON(r, &token); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	added, err := s.deps.Store.Add(r.Context(), &token)
	if err != nil {
		slog.ErrorContext(r.Context(), "远程配对导入失败 | 错误="+err.Error(), "source", "auth")
		writeError(w, http.StatusBadGateway, "pairing_import_failed", err.Error())
		return
	}
	s.pairing.revoke()
	slog.InfoContext(r.Context(), "远程配对导入完成 | 账户="+added.ID, "source", "auth")
	s.refreshLater()
	writeJSON(w, http.StatusCreated, map[string]string{"account": added.ID})
}
