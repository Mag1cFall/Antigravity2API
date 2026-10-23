//go:build !desktop

package app

import (
	"context"
	"fmt"
	"runtime"
)

// install 替换可执行文件，重启后运行新版本
func (u *selfUpdater) install(ctx context.Context, release *githubRelease) error {
	return u.replaceExecutable(ctx, release)
}

// releasePackage 返回当前系统与架构的命令行版发行包文件名，Windows 只有桌面版的 Windows 包
func releasePackage(tag string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("antigravity2api-%s-windows-%s.zip", tag, runtime.GOARCH)
	}
	return fmt.Sprintf("antigravity2api-%s-%s-%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
}
