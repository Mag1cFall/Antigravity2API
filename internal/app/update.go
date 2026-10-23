package app

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/minio/selfupdate"
	"golang.org/x/mod/semver"
)

// releaseAPI 是 GitHub 最新发行版本接口
const releaseAPI = "https://api.github.com/repos/Mag1cFall/Antigravity2API/releases/latest"

// releaseCheckInterval 是最新发行版本的缓存时长
const releaseCheckInterval = time.Hour

// Version 返回构建时记录的模块版本，标签构建为发行版本号
func Version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

// githubRelease 是 GitHub 发行版本接口中用到的字段
type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

// githubAsset 是发行版本中的一个文件
type githubAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

// updateStatus 是 GET /api/update 的响应
type updateStatus struct {
	Current     string    `json:"current"`
	Latest      string    `json:"latest,omitempty"`
	Available   bool      `json:"available"`
	Mode        string    `json:"mode"`
	Notes       string    `json:"notes,omitempty"`
	URL         string    `json:"url,omitempty"`
	PublishedAt time.Time `json:"published_at,omitzero"`
	CheckedAt   time.Time `json:"checked_at,omitzero"`
	Error       string    `json:"error,omitempty"`
	Updating    bool      `json:"updating"`
}

// updateConflict 是当前状态不允许更新时的错误，管理 API 以 409 返回
type updateConflict string

// Error 返回冲突原因
func (e updateConflict) Error() string { return string(e) }

// selfUpdater 读取最新发行版本、下载校验发行包并安装到当前可执行文件
type selfUpdater struct {
	server     *server
	version    string
	mode       string
	releaseURL string
	executable string
	mu         sync.Mutex
	release    *githubRelease
	checked    time.Time
	checkErr   error
	updating   bool
}

// newSelfUpdater 按构建版本创建更新器，替换目标为当前可执行文件
func newSelfUpdater(s *server) *selfUpdater {
	version := Version()
	executable, _ := os.Executable()
	return &selfUpdater{server: s, version: version, mode: updateMode(version), releaseURL: releaseAPI, executable: executable}
}

// updateMode 返回更新方式：development 为源码构建，container 随镜像更新，binary 可原地替换
func updateMode(version string) string {
	if !semver.IsValid(version) || semver.Prerelease(version) != "" || semver.Build(version) != "" {
		return "development"
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return "container"
		}
	}
	return "binary"
}

// status 返回当前版本与最新发行版本，refresh 为 true 或缓存过期时重新读取；非 binary 方式不读取
func (u *selfUpdater) status(ctx context.Context, refresh bool) (updateStatus, error) {
	u.mu.Lock()
	stale := u.mode == "binary" && (refresh || time.Since(u.checked) >= releaseCheckInterval)
	u.mu.Unlock()
	if stale {
		release, err := u.fetchRelease(ctx)
		if ctx.Err() != nil {
			return updateStatus{}, ctx.Err()
		}
		u.mu.Lock()
		u.checked, u.checkErr = time.Now(), err
		if err == nil {
			u.release = release
		}
		u.mu.Unlock()
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.statusLocked(), nil
}

// apply 下载最新发行包，校验 SHA-256 后安装并重启到新版本
func (u *selfUpdater) apply(ctx context.Context) (updateStatus, error) {
	u.mu.Lock()
	switch {
	case u.mode == "container":
		u.mu.Unlock()
		return updateStatus{}, updateConflict("Docker 部署随镜像更新")
	case u.mode != "binary":
		u.mu.Unlock()
		return updateStatus{}, updateConflict("源码构建不支持在线更新")
	case u.updating:
		u.mu.Unlock()
		return updateStatus{}, updateConflict("更新正在进行")
	}
	u.updating = true
	u.mu.Unlock()
	release, err := u.fetchRelease(ctx)
	if err == nil && semver.Compare(release.TagName, u.version) <= 0 {
		err = updateConflict("当前已是最新版本 " + u.version)
	}
	if err == nil {
		slog.Info("在线更新 | 阶段=1/2 下载发行包 | 版本="+release.TagName, "source", "service")
		err = u.install(ctx, release)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if err != nil {
		u.updating = false
		slog.Error("在线更新失败 | 错误="+err.Error(), "source", "service")
		return updateStatus{}, err
	}
	u.release, u.checked, u.checkErr = release, time.Now(), nil
	slog.Info("在线更新完成 | 版本="+release.TagName, "source", "service")
	status := u.statusLocked()
	u.server.deps.Restart()
	return status, nil
}

// statusLocked 在持有锁时生成更新状态
func (u *selfUpdater) statusLocked() updateStatus {
	status := updateStatus{Current: u.version, Mode: u.mode, CheckedAt: u.checked, Updating: u.updating}
	if u.checkErr != nil {
		status.Error = u.checkErr.Error()
	}
	if release := u.release; release != nil {
		status.Latest, status.Notes, status.URL, status.PublishedAt = release.TagName, release.Body, release.HTMLURL, release.PublishedAt
		status.Available = semver.Compare(release.TagName, u.version) > 0
	}
	return status
}

// fetchRelease 读取 GitHub 最新发行版本
func (u *selfUpdater) fetchRelease(ctx context.Context) (*githubRelease, error) {
	response, err := u.get(ctx, u.releaseURL, "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("读取最新发行版本: %w", err)
	}
	defer response.Body.Close()
	var release githubRelease
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("解析最新发行版本: %w", err)
	}
	return &release, nil
}

// download 下载 release 中名为 name 的发行包并按发行版本记录的 SHA-256 校验
func (u *selfUpdater) download(ctx context.Context, release *githubRelease, name string) ([]byte, error) {
	index := slices.IndexFunc(release.Assets, func(asset githubAsset) bool { return asset.Name == name })
	if index < 0 {
		return nil, fmt.Errorf("发行版本 %s 没有 %s", release.TagName, name)
	}
	asset := release.Assets[index]
	expected, ok := strings.CutPrefix(asset.Digest, "sha256:")
	if !ok {
		return nil, fmt.Errorf("发行包 %s 缺少 SHA-256 摘要", name)
	}
	response, err := u.get(ctx, asset.URL, "application/octet-stream")
	if err != nil {
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	archive, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("下载 %s: %w", name, err)
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != expected {
		return nil, fmt.Errorf("发行包 %s 的 SHA-256 与发行版本记录不一致", name)
	}
	return archive, nil
}

// get 按实时代理配置发出 GET，非 200 响应返回错误
func (u *selfUpdater) get(ctx context.Context, address string, accept string) (*http.Response, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(r *http.Request) (*url.URL, error) { return u.server.live.Load().ProxyURL(r) }
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", accept)
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		response.Body.Close()
		return nil, fmt.Errorf("HTTP %d %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return response, nil
}

// replaceExecutable 下载当前构建对应的发行包，校验后取出可执行文件替换正在运行的可执行文件
func (u *selfUpdater) replaceExecutable(ctx context.Context, release *githubRelease) error {
	name := releasePackage(release.TagName)
	archive, err := u.download(ctx, release, name)
	if err != nil {
		return err
	}
	executable, err := extractExecutable(archive, name, executableName())
	if err != nil {
		return err
	}
	slog.Info("在线更新 | 阶段=2/2 替换可执行文件 | 版本="+release.TagName, "source", "service")
	if err := selfupdate.Apply(bytes.NewReader(executable), selfupdate.Options{TargetPath: u.executable}); err != nil {
		if rollback := selfupdate.RollbackError(err); rollback != nil {
			return fmt.Errorf("替换可执行文件: %w；恢复原文件失败: %v", err, rollback)
		}
		return fmt.Errorf("替换可执行文件: %w", err)
	}
	return nil
}

// executableName 返回发行包中可执行文件的文件名
func executableName() string {
	if runtime.GOOS == "windows" {
		return "antigravity2api.exe"
	}
	return "antigravity2api"
}

// extractExecutable 从 zip 或 tar.gz 发行包中取出名为 binary 的可执行文件
func extractExecutable(archive []byte, name string, binary string) ([]byte, error) {
	if strings.HasSuffix(name, ".zip") {
		reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("读取发行包: %w", err)
		}
		for _, file := range reader.File {
			if path.Base(file.Name) != binary {
				continue
			}
			opened, err := file.Open()
			if err != nil {
				return nil, fmt.Errorf("读取发行包: %w", err)
			}
			defer opened.Close()
			return io.ReadAll(opened)
		}
		return nil, fmt.Errorf("发行包中没有 %s", binary)
	}
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("读取发行包: %w", err)
	}
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("发行包中没有 %s", binary)
		}
		if err != nil {
			return nil, fmt.Errorf("读取发行包: %w", err)
		}
		if header.Typeflag == tar.TypeReg && path.Base(header.Name) == binary {
			return io.ReadAll(reader)
		}
	}
}

// getUpdate 返回当前版本与最新发行版本，?refresh=1 时重新读取
func (s *server) getUpdate(w http.ResponseWriter, r *http.Request) {
	status, err := s.updater.status(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		writeError(w, http.StatusBadGateway, "update_check_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// postUpdate 下载并安装最新发行版本，成功后服务重启到新版本；管理端断开时更新照常完成
func (s *server) postUpdate(w http.ResponseWriter, r *http.Request) {
	status, err := s.updater.apply(context.WithoutCancel(r.Context()))
	var conflict updateConflict
	switch {
	case errors.As(err, &conflict):
		writeError(w, http.StatusConflict, "update_conflict", err.Error())
	case err != nil:
		writeError(w, http.StatusBadGateway, "update_failed", err.Error())
	default:
		writeJSON(w, http.StatusAccepted, status)
	}
}
