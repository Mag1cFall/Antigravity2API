package service

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/proxypool"
)

// proxyCheckInterval 是定时检测代理池的间隔
const proxyCheckInterval = 5 * time.Minute

// probeURL 是检测代理时请求的地址，Chromium 以它检测网络连通
const probeURL = "https://www.gstatic.com/generate_204"

// SetProxies 替换代理池的代理列表，已删除或不可用代理上的账户在下一次请求时改绑
func (s *Service) SetProxies(proxies []string) {
	s.proxies.SetProxies(proxies)
	s.requestProxyCheck()
}

// ProxyStatus 返回代理池中每个代理的状态与绑定的账户
func (s *Service) ProxyStatus() []proxypool.Status {
	return s.proxies.Status()
}

// CheckProxies 检测代理池并返回检测后的状态，不可用代理上的账户在下一次请求时改绑到可用代理
func (s *Service) CheckProxies(ctx context.Context) []proxypool.Status {
	s.proxies.Check(ctx, probeProxy)
	statuses := s.proxies.Status()
	for _, status := range statuses {
		if !status.Healthy {
			slog.Warn("代理不可用 | 代理="+status.URL+" | 原因="+status.Error, "source", "service")
		}
	}
	return statuses
}

// WatchProxies 立即检测代理池，之后按间隔或收到请求时检测，直到 ctx 结束
func (s *Service) WatchProxies(ctx context.Context) {
	ticker := time.NewTicker(proxyCheckInterval)
	defer ticker.Stop()
	for {
		s.CheckProxies(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.proxyCheck:
		}
	}
}

// requestProxyCheck 请求立即检测代理池，已有待处理的请求时忽略
func (s *Service) requestProxyCheck() {
	select {
	case s.proxyCheck <- struct{}{}:
	default:
	}
}

// probeProxy 经代理请求 probeURL，任何 HTTP 响应都表示代理可用，返回耗时
func probeProxy(ctx context.Context, proxy string) (time.Duration, error) {
	target, err := url.Parse(proxy)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return 0, err
	}
	started := time.Now()
	response, err := (&http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(target), DisableKeepAlives: true}}).Do(request)
	if err != nil {
		return 0, err
	}
	response.Body.Close()
	return time.Since(started), nil
}
