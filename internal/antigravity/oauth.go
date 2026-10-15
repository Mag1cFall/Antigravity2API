package antigravity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const (
	// successURL 为浏览器登录完成后官方客户端跳转的页面
	successURL = "https://antigravity.google/auth-success?app=antigravity"
	// terminalRedirectURL 为终端登录的回调页，页面显示授权码供用户复制
	terminalRedirectURL = "https://antigravity.google/oauth-callback"
)

// OAuthConfig 为官方客户端个人账户登录使用的 OAuth 客户端
var OAuthConfig = oauth2.Config{
	ClientID:     "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com",
	ClientSecret: "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",
	Endpoint: oauth2.Endpoint{
		AuthURL:   "https://accounts.google.com/o/oauth2/auth",
		TokenURL:  "https://oauth2.googleapis.com/token",
		AuthStyle: oauth2.AuthStyleInParams,
	},
	Scopes: []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
		"https://www.googleapis.com/auth/aicode",
	},
}

// exchangeContext 返回换取 token 的上下文，请求经克隆的 http.DefaultTransport 与代理发出
func exchangeContext(ctx context.Context, proxy Proxy) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Transport: webTransport(proxy)})
}

// LoginBrowser 在本机回环端口等待授权回调，open 负责打开授权链接，授权码经 proxy 换取 token
func LoginBrowser(ctx context.Context, proxy Proxy, open func(url string) error) (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	config := OAuthConfig
	config.RedirectURL = fmt.Sprintf("http://localhost:%d/auth/callback", listener.Addr().(*net.TCPAddr).Port)
	verifier := oauth2.GenerateVerifier()
	state := randomState()
	codes := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		codes <- r.URL.Query().Get("code")
		http.Redirect(w, r, successURL, http.StatusFound)
	})
	server := &http.Server{Handler: mux}
	go server.Serve(listener)
	defer server.Close()
	if err := open(config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce, oauth2.S256ChallengeOption(verifier))); err != nil {
		return nil, err
	}
	select {
	case code := <-codes:
		return config.Exchange(exchangeContext(ctx, proxy), code, oauth2.VerifierOption(verifier))
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// LoginTerminal 打印授权链接并从 prompt 取回用户粘贴的授权码，授权码经 proxy 换取 token
func LoginTerminal(ctx context.Context, proxy Proxy, prompt func(url string) (string, error)) (*oauth2.Token, error) {
	config := OAuthConfig
	config.RedirectURL = terminalRedirectURL
	verifier := oauth2.GenerateVerifier()
	code, err := prompt(config.AuthCodeURL(randomState(), oauth2.AccessTypeOffline, oauth2.ApprovalForce, oauth2.S256ChallengeOption(verifier)))
	if err != nil {
		return nil, err
	}
	if code == "" {
		return nil, errors.New("授权码为空")
	}
	return config.Exchange(exchangeContext(ctx, proxy), code, oauth2.VerifierOption(verifier))
}

// refreshAhead 为官方客户端刷新 access token 时的剩余有效期阈值
const refreshAhead = 300 * time.Second

// TokenSource 是自动刷新的 token 源，access token 距到期不足 refreshAhead 时由下一次请求触发刷新，变化时调用 save 写回，可并发使用
type TokenSource struct {
	ctx    context.Context
	save   func(*oauth2.Token) error
	mu     sync.Mutex
	source oauth2.TokenSource
	token  *oauth2.Token
}

// NewTokenSource 以已保存的 token 创建 token 源，刷新请求经克隆的 http.DefaultTransport 与账户代理发出
func NewTokenSource(token *oauth2.Token, proxy Proxy, save func(*oauth2.Token) error) *TokenSource {
	ctx := exchangeContext(context.Background(), proxy)
	return &TokenSource{ctx: ctx, save: save, source: oauth2.ReuseTokenSourceWithExpiry(token, OAuthConfig.TokenSource(ctx, token), refreshAhead), token: token}
}

// Token 返回有效的 access token，过期时刷新并写回
func (s *TokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	if token.AccessToken != s.token.AccessToken {
		if err := s.save(token); err != nil {
			return nil, err
		}
		s.token = token
	}
	return token, nil
}

// Expire 丢弃当前 access token，下一次 Token 用 refresh token 换新
func (s *TokenSource) Expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	expired := &oauth2.Token{RefreshToken: s.token.RefreshToken}
	s.source = oauth2.ReuseTokenSourceWithExpiry(expired, OAuthConfig.TokenSource(s.ctx, expired), refreshAhead)
}

// randomState 生成 16 字节的 state 参数
func randomState() string {
	var buffer [16]byte
	rand.Read(buffer[:])
	return base64.RawURLEncoding.EncodeToString(buffer[:])
}
