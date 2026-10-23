//go:build desktop

package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"

	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"

	"github.com/Mag1cFall/Antigravity2API/internal/desktop"
)

// install 桌面窗口运行时经 Wails 更新器暂存新版本，重启时由更新器的辅助进程替换文件；serve 子命令直接替换可执行文件
func (u *selfUpdater) install(ctx context.Context, release *githubRelease) error {
	if u.server.deps.Host != "desktop" {
		return u.replaceExecutable(ctx, release)
	}
	return desktop.Install(ctx, strings.TrimPrefix(u.version, "v"), &releaseProvider{updater: u, release: release})
}

// releasePackage 返回当前平台桌面版的发行包文件名
func releasePackage(tag string) string {
	switch runtime.GOOS {
	case "windows":
		return fmt.Sprintf("antigravity2api-%s-windows-%s.zip", tag, runtime.GOARCH)
	case "darwin":
		return fmt.Sprintf("antigravity2api-desktop-%s-darwin-universal.tar.gz", tag)
	}
	return fmt.Sprintf("antigravity2api-desktop-%s-linux-%s.tar.gz", tag, runtime.GOARCH)
}

// releaseProvider 把已读取的 GitHub 发行版本交给 Wails 更新器
type releaseProvider struct {
	updater *selfUpdater
	release *githubRelease
}

// Name 返回更新来源名称
func (p *releaseProvider) Name() string { return "github" }

// Check 返回发行版本中当前平台的桌面版：Windows 与 Linux 为发行包中的可执行文件，macOS 为只含 Antigravity2API.app 的 tar.gz
func (p *releaseProvider) Check(context.Context, wailsupdater.CheckRequest) (*wailsupdater.Release, error) {
	name := releasePackage(p.release.TagName)
	if runtime.GOOS != "darwin" {
		name = executableName()
	}
	return &wailsupdater.Release{
		Version:     strings.TrimPrefix(p.release.TagName, "v"),
		Notes:       p.release.Body,
		PublishedAt: p.release.PublishedAt,
		Artifact:    wailsupdater.Artifact{Filename: name, Platform: runtime.GOOS, Arch: runtime.GOARCH},
	}, nil
}

// Download 下载并按发行版本记录的 SHA-256 校验发行包，Windows 与 Linux 写出其中的可执行文件，macOS 写出整个 tar.gz
func (p *releaseProvider) Download(ctx context.Context, _ *wailsupdater.Release, dst io.Writer, onProgress func(written, total int64)) error {
	name := releasePackage(p.release.TagName)
	payload, err := p.updater.download(ctx, p.release, name)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		if payload, err = extractExecutable(payload, name, executableName()); err != nil {
			return err
		}
	}
	slog.Info("在线更新 | 阶段=2/2 暂存新版本 | 版本="+p.release.TagName, "source", "service")
	written, err := io.Copy(dst, bytes.NewReader(payload))
	onProgress(written, int64(len(payload)))
	if err != nil {
		return fmt.Errorf("暂存新版本: %w", err)
	}
	return nil
}
