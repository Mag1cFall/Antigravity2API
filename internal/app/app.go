// Package app 在同一监听上装配公开 API、请求账本、管理 API、管理事件流、健康检查与管理端静态资源
package app

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/api"
	"github.com/Mag1cFall/Antigravity2API/internal/auth"
	"github.com/Mag1cFall/Antigravity2API/internal/config"
	"github.com/Mag1cFall/Antigravity2API/internal/logging"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
	"github.com/Mag1cFall/Antigravity2API/internal/service"
)

// Deps 是 Serve 使用的运行时组件，由 cmd 装配
type Deps struct {
	Service *service.Service
	Store   *auth.Store
	// Ledger 是请求账本，服务在取得额度时写入额度快照
	Ledger *requestdb.Store
	// Logger 是服务日志，Serve 运行期间在其上叠加管理事件流与日志入库
	Logger *logging.Logger
	// Overrides 是本次启动的命令行覆盖
	Overrides config.Overrides
	// Host 是管理端宿主：web 或 desktop
	Host string
	// UI 是管理端静态资源处理器，为 nil 时不挂载
	UI http.Handler
	// Restart 在在线更新安装完成后重启到新版本
	Restart func()
	// Ready 在开始监听后以实际监听地址调用
	Ready func(address string)
}

// shutdownTimeout 是退出时等待进行中请求的上限
const shutdownTimeout = 10 * time.Second

// server 持有一次 Serve 的运行状态
type server struct {
	ctx  context.Context
	live *atomic.Pointer[config.Config]
	// boot 是启动时生效的配置，监听地址与目录以它为准
	boot    config.Config
	deps    Deps
	ledger  *requestdb.Store
	events  *hub
	auth    *adminAuth
	logins  *logins
	pairing pairing
	started time.Time
	// refreshes 合并后台刷新账户池的请求
	refreshes chan struct{}
	refreshMu sync.Mutex
	// settingsMu 串行读取、合并与保存 .env
	settingsMu sync.Mutex
	// latest 是官方更新清单中的最新客户端版本
	latest atomic.Pointer[string]
	// updater 是本程序的在线更新
	updater *selfUpdater
	// listen 是实际监听地址，reserved 是被 Windows 保留而改用 listen 的配置地址，没有改用时为空
	listen, reserved string
}

// Serve 在启动配置的监听地址上运行服务直到 ctx 结束；live 是已应用命令行覆盖的配置，设置保存后替换为新值供下一次请求读取，账户池在启动后于后台初始化
func Serve(ctx context.Context, live *atomic.Pointer[config.Config], deps Deps) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	boot := *live.Load()
	s := &server{
		ctx: ctx, live: live, boot: boot, deps: deps, ledger: deps.Ledger, events: newHub(), started: time.Now().UTC(),
		auth: newAdminAuth(boot), refreshes: make(chan struct{}, 1),
	}
	s.logins = &logins{server: s, items: map[string]*login{}}
	s.updater = newSelfUpdater(s)
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewMultiHandler(deps.Logger.Handler(), s.events.handler(deps.Logger.Leveler(), s.ledger))))
	defer slog.SetDefault(previous)
	s.ledger.SetBodyCapture(boot.RequestLogBodies)
	listener, err := net.Listen("tcp", boot.Listen)
	if errors.Is(err, portReserved) {
		if listener, err = listenAfter(boot.Listen); err == nil {
			s.reserved = boot.Listen
			slog.Warn(fmt.Sprintf("监听端口被 Windows 保留，改用其后第一个可用端口 | 配置=%s | 地址=%s", boot.Listen, listener.Addr()), "source", "service")
		}
	}
	if err != nil {
		return fmt.Errorf("监听 %s: %w", boot.Listen, err)
	}
	s.listen = listener.Addr().String()
	httpServer := &http.Server{
		Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
		ErrorLog: slog.NewLogLogger(slog.Default().Handler(), slog.LevelError),
	}
	var workers sync.WaitGroup
	workers.Go(s.watch)
	workers.Go(s.refreshWorker)
	workers.Go(s.quotaWorker)
	workers.Go(s.checkVersions)
	workers.Go(func() { deps.Service.WatchProxies(ctx) })
	defer workers.Wait()
	defer cancel()
	s.refreshLater()
	served := make(chan error, 1)
	go func() { served <- httpServer.Serve(listener) }()
	slog.Info("服务就绪 | 地址="+s.listen, "source", "service")
	deps.Ready(s.listen)
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), shutdownTimeout)
		defer stop()
		if err := httpServer.Shutdown(shutdown); err != nil {
			return fmt.Errorf("关闭 HTTP 服务: %w", err)
		}
		if err := <-served; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// routes 挂载公开 API、Gemini 文件上传与生成媒体、健康检查、远程配对、管理 API 与管理端静态资源，全部请求经过访问日志
func (s *server) routes() http.Handler {
	root := http.NewServeMux()
	keys := func() []string { return s.live.Load().APIKeys }
	media := func() (bool, string) {
		cfg := s.live.Load()
		return cfg.MediaOutput == "url", cfg.MediaBaseURL
	}
	public := s.recordPublic(api.NewHandler(recordingService{Service: s.deps.Service, server: s}, api.Options{
		APIKeys: keys, FilesDir: filepath.Join(s.boot.RuntimeDir, "files"), MediaDir: filepath.Join(s.boot.RuntimeDir, "media"), MediaLinks: media,
	}))
	root.Handle("/v1/", public)
	root.Handle("/v1beta/", public)
	root.Handle("/upload/", public)
	root.Handle("/media/", public)
	root.HandleFunc("POST /api/pairing/accounts", s.pairAccount)
	root.Handle("/api/", s.requireAdmin(s.admin()))
	if s.deps.UI != nil {
		root.Handle("/", s.deps.UI)
	}
	return s.access(securityHeaders(root))
}

// access 为每个请求生成请求 ID 并经 X-Request-ID 回传，结束后写一行访问日志；静态资源成功只在 debug 级别记录
func (s *server) access(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := "req_" + strings.ToLower(rand.Text())
		w.Header().Set("X-Request-ID", id)
		ctx := logging.WithRequestID(r.Context(), id)
		writer := &recordWriter{ResponseWriter: w}
		next.ServeHTTP(writer, r.WithContext(ctx))
		status, latency, ip := cmp.Or(writer.status, http.StatusOK), time.Since(started), clientIP(r)
		level := slog.LevelInfo
		switch {
		case status >= http.StatusInternalServerError:
			level = slog.LevelError
		case status >= http.StatusBadRequest:
			level = slog.LevelWarn
		case !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/v1"):
			level = slog.LevelDebug
		}
		slog.Log(ctx, level, fmt.Sprintf("%d | %s | %s | %s %s", status, latency.Round(time.Millisecond), ip, r.Method, r.URL.Path),
			"source", "http", "event", "http.access", "status", status, "latency_ms", latency.Milliseconds(), "client_ip", ip,
			"method", r.Method, "path", r.URL.Path)
	})
}

// clientIP 返回直连客户端的 IP
func clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

// refreshLater 请求后台刷新账户池，刷新进行中时合并为下一次
func (s *server) refreshLater() {
	select {
	case s.refreshes <- struct{}{}:
	default:
	}
}

// refreshWorker 逐次执行后台刷新直到服务退出
func (s *server) refreshWorker() {
	for {
		select {
		case <-s.refreshes:
			s.refresh(s.ctx)
		case <-s.ctx.Done():
			return
		}
	}
}

// refresh 串行刷新账户池，单个账户的失败写入该账户状态
func (s *server) refresh(ctx context.Context) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	started := time.Now()
	if err := s.deps.Service.Refresh(ctx); err != nil && ctx.Err() == nil {
		slog.Warn(fmt.Sprintf("账户池刷新部分失败 | 耗时=%s | 错误=%v", time.Since(started).Round(time.Millisecond), err), "source", "service")
		return
	}
	slog.Info(fmt.Sprintf("账户池刷新完成 | 耗时=%s", time.Since(started).Round(time.Millisecond)), "source", "service")
}

// quotaWorker 按实时配置的 QUOTA_REFRESH_INTERVAL 定时读取全部账户的额度，再重新初始化异常账户与唤醒周额度：每分钟检查一次，启动约 1 分钟后读取第一次，此后距上次读取的整分钟数达到间隔时再读取，间隔为 0 时不读取
func (s *server) quotaWorker() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	minutes := -1
	for {
		select {
		case <-ticker.C:
		case <-s.ctx.Done():
			return
		}
		minutes++
		if interval := s.live.Load().QuotaRefreshInterval; interval > 0 && (minutes == 0 || time.Duration(minutes)*time.Minute >= interval) {
			minutes = 0
			s.readQuotas(s.ctx)
			s.retryAccounts(s.ctx)
			s.deps.Service.Warmup(s.ctx)
		}
	}
}

// readQuotas 在进行中的账户池刷新结束后读取全部账户的额度，返回读取失败的账户及原因
func (s *server) readQuotas(ctx context.Context) map[string]error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	started := time.Now()
	failed := s.deps.Service.RefreshQuotas(ctx)
	slog.Info(fmt.Sprintf("额度刷新完成 | 失败=%d | 耗时=%s", len(failed), time.Since(started).Round(time.Millisecond)), "source", "service")
	return failed
}

// retryAccounts 在定时额度刷新后重新初始化异常与需要验证的账户，成功的账户恢复调度
func (s *server) retryAccounts(ctx context.Context) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if retried, remaining := s.deps.Service.RetryFailed(ctx); retried > 0 {
		slog.Info(fmt.Sprintf("异常账户重新初始化完成 | 重试=%d | 仍失败=%d", retried, remaining), "source", "service")
	}
}

// securityHeaders 禁止页面被其他站点嵌入并关闭 MIME 嗅探与 Referer
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("X-Frame-Options", "DENY")
		header.Set("Content-Security-Policy", "frame-ancestors 'none'")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// portReserved 是 Windows 拒绝监听系统保留端口（Hyper-V、WSL2、Docker 的 WinNAT 保留端口段）时返回的 WSAEACCES
const portReserved = syscall.Errno(10013)

// listenAfter 从 address 的下一个端口起依次监听，返回第一个监听成功的监听器
func listenAfter(address string) (net.Listener, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	first, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}
	for next := first + 1; next <= 65535; next++ {
		if listener, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(next))); err == nil {
			return listener, nil
		}
	}
	return nil, fmt.Errorf("%s 之后没有可监听的端口", address)
}
