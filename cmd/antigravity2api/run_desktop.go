//go:build desktop

package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/Mag1cFall/Antigravity2API/internal/config"
	"github.com/Mag1cFall/Antigravity2API/internal/desktop"
)

// run 先切换到数据目录；有子命令时附加父进程控制台后执行它，否则运行桌面应用
func run(ctx context.Context, args []string) error {
	if err := enterDataDir(); err != nil {
		alert(err)
		return err
	}
	if len(args) != 0 && args[0] != "-"+desktop.HiddenFlag && args[0] != "--"+desktop.HiddenFlag {
		attachConsole()
		if done, err := dispatch(ctx, args); done {
			return err
		}
	}
	flags := flag.NewFlagSet(title, flag.ContinueOnError)
	var o options
	o.bind(flags)
	hidden := flags.Bool(desktop.HiddenFlag, false, "启动后只显示托盘")
	if err := flags.Parse(args); err != nil {
		alert(err)
		return err
	}
	cfg, err := o.load()
	if err != nil {
		alert(err)
		return err
	}
	return desktop.Run(desktop.Options{
		Title:   title,
		DataDir: filepath.Join(cfg.RuntimeDir, "webview"),
		Hidden:  *hidden,
		Serve: func(ctx context.Context, ready func(string)) error {
			return serve(ctx, "desktop", cfg, o.overrides, func(address string) { ready(loopback(address)) }, func() {
				go func() {
					if err := desktop.Restart(); err != nil {
						slog.Error("在线更新重启失败 | 错误="+err.Error(), "source", "service")
					}
				}()
			})
		},
	})
}

// enterDataDir 创建并进入数据目录，.env、auth 与 runtime 都放在这里；首次启动写入默认配置的 .env 并创建 auth
func enterDataDir() error {
	dir, err := dataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	if _, err := os.Stat(config.File); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	defaults := config.Default()
	if err := os.MkdirAll(defaults.AuthDir, 0o700); err != nil {
		return err
	}
	return defaults.Save(config.File)
}

// dataDir 返回桌面版数据目录：Windows 便携版为可执行文件所在目录，macOS 与 Linux 的程序包只读，为用户配置目录下的 Antigravity2API
func dataDir() (string, error) {
	if runtime.GOOS == "windows" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.Dir(exe), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, title), nil
}
