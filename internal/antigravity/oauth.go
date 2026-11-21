package antigravity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
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

// OAuthConfigCloud 为官方客户端“Continue with Google Cloud”以 Google 账户登录使用的 OAuth 客户端
var OAuthConfigCloud = oauth2.Config{
	ClientID:     "884354919052-36trc1jjb3tguiac32ov6cod268c5blh.apps.googleusercontent.com",
	ClientSecret: "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",
	Endpoint:     OAuthConfig.Endpoint,
	Scopes: []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	},
}

// exchangeContext 返回换取 token 的上下文，请求经 http1Transport 与代理发出，没有收到响应时重发
func exchangeContext(ctx context.Context, proxy Proxy) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Transport: retryTransport{newProxyTransport(proxy, http1Transport)}})
}

// tokenAttempts 为一次换取或刷新 token 在没有收到响应时最多发出的请求数
const tokenAttempts = 3

// retryTransport 在没有收到响应（代理关闭了空闲连接、连接被重置）时重发 token 请求，第 n 次重发前等待 n × 250 ms；收到任何响应即返回
type retryTransport struct{ base http.RoundTripper }

// RoundTrip 发出请求，没有收到响应时以重新取得的请求正文重发，最多 tokenAttempts 次
func (t retryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		response, err := t.base.RoundTrip(request)
		if err == nil || attempt == tokenAttempts || request.GetBody == nil {
			return response, err
		}
		select {
		case <-request.Context().Done():
			return nil, err
		case <-time.After(time.Duration(attempt) * 250 * time.Millisecond):
		}
		body, bodyErr := request.GetBody()
		if bodyErr != nil {
			return nil, err
		}
		request = request.Clone(request.Context())
		request.Body = body
	}
}

// LoginBrowser 在本机回环端口等待授权回调，open 负责打开授权链接，授权码经 proxy 换取 token
func LoginBrowser(ctx context.Context, proxy Proxy, open func(url string) error) (*oauth2.Token, error) {
	return loginLoopback(ctx, OAuthConfig, proxy, open, oauth2.ApprovalForce)
}

// loginLoopback 在本机回环端口等待授权回调并换取 token，extra 附加到授权链接参数
func loginLoopback(ctx context.Context, config oauth2.Config, proxy Proxy, open func(url string) error, extra ...oauth2.AuthCodeOption) (*oauth2.Token, error) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	config.RedirectURL = fmt.Sprintf("http://localhost:%d/auth/callback", listener.Addr().(*net.TCPAddr).Port)
	verifier := oauth2.GenerateVerifier()
	state := randomState()
	callbacks := make(chan callback, 1)
	server := &http.Server{Handler: callbackHandler(state, callbacks)}
	go server.Serve(listener)
	defer server.Close()
	options := append([]oauth2.AuthCodeOption{oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier)}, extra...)
	if err := open(config.AuthCodeURL(state, options...)); err != nil {
		return nil, err
	}
	select {
	case result := <-callbacks:
		if result.err != nil {
			return nil, result.err
		}
		return config.Exchange(exchangeContext(ctx, proxy), result.code, oauth2.VerifierOption(verifier))
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// callback 为回环回调的结果：授权码或授权页返回的错误
type callback struct {
	code string
	err  error
}

// callbackHandler 返回回环监听的 /auth/callback 处理器：state 不符时返回 400 并继续等待，带 error 时返回 400 并以该错误结束登录，否则交出授权码并 302 到成功页
func callbackHandler(state string, callbacks chan<- callback) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		if reason := query.Get("error"); reason != "" {
			message := "OAuth error: " + reason
			http.Error(w, message, http.StatusBadRequest)
			callbacks <- callback{err: errors.New(message)}
			return
		}
		callbacks <- callback{code: query.Get("code")}
		http.Redirect(w, r, successURL, http.StatusFound)
	})
	return mux
}

// LoginTerminal 打印授权链接并从 prompt 取回用户粘贴的授权码（见 pastedCode），授权码经 proxy 换取 token
func LoginTerminal(ctx context.Context, proxy Proxy, prompt func(url string) (string, error)) (*oauth2.Token, error) {
	config := OAuthConfig
	config.RedirectURL = terminalRedirectURL
	verifier := oauth2.GenerateVerifier()
	pasted, err := prompt(config.AuthCodeURL(randomState(), oauth2.AccessTypeOffline, oauth2.ApprovalForce, oauth2.S256ChallengeOption(verifier)))
	if err != nil {
		return nil, err
	}
	code := pastedCode(pasted)
	if code == "" {
		return nil, errors.New("授权码为空")
	}
	return config.Exchange(exchangeContext(ctx, proxy), code, oauth2.VerifierOption(verifier))
}

// pastedCode 从用户粘贴的内容中取出授权码：带 code 参数的回调地址取该参数，从地址栏复制的 URL 编码授权码（4%2F…）解码
func pastedCode(value string) string {
	value = strings.TrimSpace(value)
	if parsed, err := url.Parse(value); err == nil && parsed.Query().Get("code") != "" {
		return parsed.Query().Get("code")
	}
	if decoded, err := url.PathUnescape(value); err == nil {
		return decoded
	}
	return value
}

// refreshAhead 为官方客户端刷新 access token 时的剩余有效期阈值
const refreshAhead = 300 * time.Second

// TokenSource 是自动刷新的 token 源，access token 距到期不足 refreshAhead 时由下一次请求触发刷新，变化时调用 save 写回，可并发使用
type TokenSource struct {
	ctx    context.Context
	config oauth2.Config
	save   func(*oauth2.Token) error
	mu     sync.Mutex
	source oauth2.TokenSource
	token  *oauth2.Token
}

// NewTokenSource 以已保存的 token 创建自动刷新的 token 源
// OAuth 客户端按 method 与 provider 选择，规则见 docs/protocol.md“token 刷新”
func NewTokenSource(token *oauth2.Token, method, provider string, proxy Proxy, save func(*oauth2.Token) error) *TokenSource {
	config := OAuthConfig
	switch {
	case provider != "":
		config = OAuthConfigWIF
	case method == AuthMethodEnterprise:
		config = OAuthConfigCloud
	}
	ctx := exchangeContext(context.Background(), proxy)
	return &TokenSource{ctx: ctx, config: config, save: save, source: oauth2.ReuseTokenSourceWithExpiry(token, config.TokenSource(ctx, token), refreshAhead), token: token}
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

// Stale 判断 access token 是否已到刷新时机
func (s *TokenSource) Stale() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token.AccessToken == "" || time.Until(s.token.Expiry) < refreshAhead
}

// Refresh 不经缓存以 refresh token 换新 access token，写回并作为此后 Token 的结果；并发调用各自发出刷新请求
func (s *TokenSource) Refresh() (*oauth2.Token, error) {
	s.mu.Lock()
	expired := &oauth2.Token{RefreshToken: s.token.RefreshToken}
	s.mu.Unlock()
	token, err := s.config.TokenSource(s.ctx, expired).Token()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.save(token); err != nil {
		return nil, err
	}
	s.token, s.source = token, oauth2.ReuseTokenSourceWithExpiry(token, s.config.TokenSource(s.ctx, token), refreshAhead)
	return token, nil
}

// Expire 丢弃当前 access token，下一次 Token 以创建时选定的 OAuth 客户端用 refresh token 换新
func (s *TokenSource) Expire() {
	s.mu.Lock()
	defer s.mu.Unlock()
	expired := &oauth2.Token{RefreshToken: s.token.RefreshToken}
	s.source = oauth2.ReuseTokenSourceWithExpiry(expired, s.config.TokenSource(s.ctx, expired), refreshAhead)
}

// randomState 生成 16 字节的 state 参数
func randomState() string {
	var buffer [16]byte
	rand.Read(buffer[:])
	return base64.RawURLEncoding.EncodeToString(buffer[:])
}
