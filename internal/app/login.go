package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/auth"
)

// loginTimeout 是一次登录等待回调或授权码的时长
const loginTimeout = 5 * time.Minute

// loginState 是一次添加账户的 Google 登录的状态
type loginState struct {
	ID string `json:"id"`
	// Mode 为 browser 或 code
	Mode string `json:"mode"`
	// URL 是 Google 授权链接
	URL string `json:"url"`
	// State 为 pending、completed、failed 或 canceled
	State string `json:"state"`
	// Account 是完成后的账户 ID
	Account   string    `json:"account,omitempty"`
	Error     string    `json:"error,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// login 是一次进行中的登录
type login struct {
	state  loginState
	cancel context.CancelFunc
	// codes 传递 code 模式提交的授权码
	codes chan string
	done  chan struct{}
	// callback 是 browser 模式在本机回环端口等待的回调地址
	callback *url.URL
	// device 是登录开始时生成的设备档案，登录前虚拟进程与登录成功后的账户共用它
	device antigravity.Device
}

// logins 保存进行中的登录
type logins struct {
	server *server
	mu     sync.Mutex
	items  map[string]*login
}

// createLogin 开始一次登录并返回授权链接
func (s *server) createLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	state, err := s.logins.start(r.Context(), input.Mode)
	if err != nil {
		writeError(w, http.StatusBadGateway, "login_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, state)
}

// cancelLogin 取消进行中的登录，结果经事件流推送
func (s *server) cancelLogin(w http.ResponseWriter, r *http.Request) {
	item := s.logins.get(r.PathValue("id"))
	if item == nil {
		writeError(w, http.StatusNotFound, "login_not_found", "登录不存在或已结束")
		return
	}
	item.cancel()
	w.WriteHeader(http.StatusNoContent)
}

// submitLoginCode 提交 code 模式的授权码或 browser 模式的回调地址，等待登录结束后返回结果
func (s *server) submitLoginCode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item := s.logins.get(r.PathValue("id"))
	if item == nil {
		writeError(w, http.StatusNotFound, "login_not_found", "登录不存在或已结束")
		return
	}
	value := strings.TrimSpace(input.Code)
	if item.callback == nil {
		select {
		case item.codes <- authorizationCode(value):
		default:
			writeError(w, http.StatusConflict, "login_code_submitted", "授权码已提交，正在完成登录")
			return
		}
	} else if err := forwardCallback(item.callback, value); err != nil {
		writeError(w, http.StatusBadRequest, "login_callback_rejected", err.Error())
		return
	}
	select {
	case <-item.done:
		writeJSON(w, http.StatusOK, s.logins.snapshot(item))
	case <-r.Context().Done():
	}
}

// start 在后台运行登录，取得授权链接后登记并返回
func (l *logins) start(ctx context.Context, mode string) (loginState, error) {
	s := l.server
	flow, cancel := context.WithTimeout(s.ctx, loginTimeout)
	item := &login{cancel: cancel, codes: make(chan string, 1), done: make(chan struct{}), device: antigravity.NewLoginDevice()}
	item.state = loginState{ID: strings.ToLower(rand.Text()), Mode: mode, State: "pending", ExpiresAt: time.Now().Add(loginTimeout).UTC()}
	links := make(chan string, 1)
	go l.run(flow, item, mode, links)
	select {
	case link := <-links:
		l.mu.Lock()
		item.state.URL = link
		if mode == "browser" {
			parsed, _ := url.Parse(link)
			item.callback, _ = url.Parse(parsed.Query().Get("redirect_uri"))
		}
		l.items[item.state.ID] = item
		state := item.state
		l.mu.Unlock()
		slog.Info(fmt.Sprintf("账户登录开始 | 方式=%s | 登录=%s", mode, state.ID), "source", "auth")
		s.events.publish(event{Type: "login", Data: state})
		return state, nil
	case <-item.done:
		return loginState{}, errors.New(item.state.Error)
	case <-ctx.Done():
		cancel()
		return loginState{}, ctx.Err()
	}
}

// run 完成 OAuth 授权并把账户写入账户目录，授权等待期间以登录开始的设备档案运行登录前虚拟进程
func (l *logins) run(ctx context.Context, item *login, mode string, links chan<- string) {
	defer item.cancel()
	var token *oauth2.Token
	var err error
	proxy := func(request *http.Request) (*url.URL, error) { return l.server.live.Load().ProxyURL(request) }
	prelogin, stop := context.WithCancel(ctx)
	defer stop()
	go antigravity.NewPrelogin(item.device, proxy).Run(prelogin)
	if mode == "browser" {
		token, err = antigravity.LoginBrowser(ctx, proxy, func(link string) error {
			links <- link
			return nil
		})
	} else {
		token, err = antigravity.LoginTerminal(ctx, proxy, func(link string) (string, error) {
			links <- link
			select {
			case code := <-item.codes:
				return code, nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		})
	}
	stop()
	id := ""
	if err == nil {
		var added *auth.Account
		if added, err = l.server.deps.Store.AddWithDevice(l.server.ctx, token, item.device); err == nil {
			id = added.ID
		}
	}
	l.finish(item, id, err)
}

// finish 记录登录结果并推送事件，成功时在后台刷新账户池
func (l *logins) finish(item *login, account string, err error) {
	l.mu.Lock()
	switch {
	case err == nil:
		item.state.State, item.state.Account = "completed", account
	case errors.Is(err, context.Canceled):
		item.state.State, item.state.Error = "canceled", "登录已取消"
	case errors.Is(err, context.DeadlineExceeded):
		item.state.State, item.state.Error = "failed", "5 分钟内没有完成 Google 登录"
	default:
		item.state.State, item.state.Error = "failed", err.Error()
	}
	state := item.state
	delete(l.items, state.ID)
	l.mu.Unlock()
	close(item.done)
	l.server.events.publish(event{Type: "login", Data: state})
	switch state.State {
	case "completed":
		slog.Info("账户添加完成 | 账户="+account, "source", "auth")
		l.server.refreshLater()
	case "canceled":
		slog.Warn("账户登录已取消 | 登录="+state.ID, "source", "auth")
	default:
		slog.Error("账户添加失败 | 登录="+state.ID+" | 错误="+state.Error, "source", "auth")
	}
}

// get 返回进行中的登录，不存在时返回 nil
func (l *logins) get(id string) *login {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.items[id]
}

// snapshot 返回登录状态副本
func (l *logins) snapshot(item *login) loginState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return item.state
}

// authorizationCode 从授权码或带 code 参数的回调地址中取出授权码
func authorizationCode(value string) string {
	if parsed, err := url.Parse(value); err == nil && parsed.Query().Get("code") != "" {
		return parsed.Query().Get("code")
	}
	return value
}

// forwardCallback 把浏览器地址栏中回调地址的查询参数转交本机回环监听，监听没有接受时返回带其应答正文的错误
func forwardCallback(callback *url.URL, value string) error {
	forward := *callback
	if pasted, err := url.Parse(value); err == nil {
		forward.RawQuery = pasted.RawQuery
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(forward.String())
	if err != nil {
		return fmt.Errorf("转交回调: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		reason, _ := io.ReadAll(response.Body)
		return fmt.Errorf("回调地址被拒绝：%s", strings.TrimSpace(string(reason)))
	}
	return nil
}
