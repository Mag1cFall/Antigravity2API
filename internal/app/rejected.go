package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
	"github.com/Mag1cFall/Antigravity2API/internal/robustio"
	"github.com/Mag1cFall/Antigravity2API/rules"
)

// rejectedURL 是 main 分支的上游拒绝句列表，经 GitHub contents 接口以原文返回
const rejectedURL = "https://api.github.com/repos/Mag1cFall/Antigravity2API/contents/rules/rejected.txt?ref=main"

// rejectedInterval 是自动检查上游拒绝句列表的间隔
const rejectedInterval = time.Hour

// rejectedTimeout 是一次读取上游拒绝句列表的时限
const rejectedTimeout = time.Minute

// rejectedFeed 维护请求使用的上游拒绝句规则：列表取内置与数据目录中已下载的两份中版本较新者，启动时与每小时从 GitHub 检查更新，与自定义规则合并后交给 prompt
type rejectedFeed struct {
	server *server
	mu     sync.Mutex
	// list 是当前列表原文，remote 为真时取自 GitHub
	list   string
	remote bool
	etag   string
	// checked 是最近一次检查的时刻，updated 是当前列表下载的时刻
	checked, updated time.Time
	// failure 是最近一次检查失败的原因，成功后清空
	failure string
}

// rejectedStatus 是管理端显示的上游拒绝句列表状态
type rejectedStatus struct {
	Version   string     `json:"version"`
	Rules     int        `json:"rules"`
	Custom    int        `json:"custom"`
	Source    string     `json:"source"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Error     string     `json:"error,omitempty"`
	List      string     `json:"list"`
}

// newRejectedFeed 读入内置列表与数据目录中已下载的列表，取版本较新者与自定义规则一起生效
func newRejectedFeed(s *server) *rejectedFeed {
	feed := &rejectedFeed{server: s, list: string(rules.Rejected)}
	path := feed.path()
	if data, err := os.ReadFile(path); err == nil {
		if saved, err := prompt.ParseRejected(string(data)); err == nil && version(saved.Version) > version(feed.parsed().Version) {
			feed.list, feed.remote = string(data), true
			if info, err := os.Stat(path); err == nil {
				feed.updated = info.ModTime()
			}
		}
	}
	feed.apply()
	return feed
}

// path 返回数据目录中保存已下载列表的文件
func (f *rejectedFeed) path() string {
	return filepath.Join(f.server.boot.RuntimeDir, "rejected.txt")
}

// parsed 返回当前列表的解析结果
func (f *rejectedFeed) parsed() *prompt.Rejected {
	result, _ := prompt.ParseRejected(f.list)
	return result
}

// apply 把当前列表与实时配置中的自定义规则合并后交给 prompt
func (f *rejectedFeed) apply() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyLocked()
}

// applyLocked 同 apply，调用方持有 mu；列表与自定义规则都已校验，合并不会出错
func (f *rejectedFeed) applyLocked() {
	merged, _ := prompt.ParseRejected(f.list, f.server.live.Load().RejectedRules)
	prompt.SetRejected(merged)
}

// watch 启动时检查一次上游拒绝句列表，此后每小时检查一次
func (f *rejectedFeed) watch() {
	ticker := time.NewTicker(rejectedInterval)
	defer ticker.Stop()
	for {
		f.check(f.server.ctx)
		select {
		case <-ticker.C:
		case <-f.server.ctx.Done():
			return
		}
	}
}

// check 按实时代理配置从 GitHub 读取上游拒绝句列表，列表没变化时 GitHub 返回 304；读到版本不低于当前列表的有效列表时保存到数据目录并生效，返回检查后的状态
func (f *rejectedFeed) check(ctx context.Context) rejectedStatus {
	ctx, cancel := context.WithTimeout(ctx, rejectedTimeout)
	defer cancel()
	f.mu.Lock()
	etag := f.etag
	f.mu.Unlock()
	data, tag, err := f.fetch(ctx, etag)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checked, f.failure = time.Now(), ""
	if err == nil && data != nil {
		err = f.store(data, tag)
	}
	if err != nil {
		f.failure = err.Error()
		slog.WarnContext(ctx, "检查上游拒绝句列表失败 | 错误="+err.Error(), "source", "service")
	}
	return f.statusLocked()
}

// fetch 发出带 If-None-Match 的 GET，列表没变化时返回空正文
func (f *rejectedFeed) fetch(ctx context.Context, etag string) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rejectedURL, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Accept", "application/vnd.github.raw")
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := f.server.httpClient().Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNotModified:
		return nil, etag, nil
	case http.StatusOK:
		data, err := io.ReadAll(response.Body)
		return data, response.Header.Get("ETag"), err
	}
	body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
	return nil, "", fmt.Errorf("HTTP %d %s", response.StatusCode, body)
}

// store 校验下载的列表，版本不低于当前列表时写入数据目录并生效，调用方持有 mu
func (f *rejectedFeed) store(data []byte, etag string) error {
	downloaded, err := prompt.ParseRejected(string(data))
	if err != nil {
		return fmt.Errorf("下载的拒绝句列表无效: %w", err)
	}
	f.etag = etag
	current := f.parsed()
	if version(downloaded.Version) < version(current.Version) || f.remote && string(data) == f.list {
		return nil
	}
	temporary := f.path() + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return err
	}
	if err := robustio.Rename(temporary, f.path()); err != nil {
		return err
	}
	f.list, f.remote, f.updated = string(data), true, time.Now()
	if downloaded.Version != current.Version {
		slog.Info(fmt.Sprintf("上游拒绝句列表已更新 | 版本=%s | 条数=%d", downloaded.Version, downloaded.Rules), "source", "service")
	}
	f.applyLocked()
	return nil
}

// status 返回当前列表的版本、条数、来源、检查与下载时刻和原文
func (f *rejectedFeed) status() rejectedStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusLocked()
}

// statusLocked 同 status，调用方持有 mu
func (f *rejectedFeed) statusLocked() rejectedStatus {
	list := f.parsed()
	status := rejectedStatus{Version: list.Version, Rules: list.Rules, Source: "builtin", Error: f.failure, List: f.list}
	custom, _ := prompt.ParseRejected(f.server.live.Load().RejectedRules)
	status.Custom = custom.Rules
	if f.remote {
		status.Source = "remote"
	}
	if checked := f.checked; !checked.IsZero() {
		status.CheckedAt = &checked
	}
	if updated := f.updated; !updated.IsZero() {
		status.UpdatedAt = &updated
	}
	return status
}

// version 把列表版本解析为整数，不是十进制整数时为 0
func version(value string) uint64 {
	number, _ := strconv.ParseUint(value, 10, 64)
	return number
}

// getRejected 返回上游拒绝句列表状态
func (s *server) getRejected(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.rejected.status())
}

// checkRejected 立即检查上游拒绝句列表并返回检查后的状态
func (s *server) checkRejected(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rejected.check(r.Context()))
}
