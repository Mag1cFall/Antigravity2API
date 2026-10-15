//go:build !windows

package auth

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// officialApp 返回官方客户端：macOS 为 /Applications/Antigravity.app，其他系统为 PATH 中 antigravity 指向的可执行文件
func officialApp() (string, error) {
	if runtime.GOOS == "darwin" {
		const app = "/Applications/Antigravity.app"
		if _, err := os.Stat(app); err != nil {
			return "", fmt.Errorf("未找到官方客户端: %w", err)
		}
		return app, nil
	}
	path, err := exec.LookPath("antigravity")
	if err != nil {
		return "", fmt.Errorf("未找到官方客户端: %w", err)
	}
	return filepath.EvalSymlinks(path)
}

// officialDir 返回官方客户端全部进程所在的目录
func officialDir(app string) string {
	if runtime.GOOS == "darwin" {
		return app + "/"
	}
	return filepath.Dir(app) + "/"
}

// stopOfficial 结束命令行含官方客户端目录的全部进程并等待其退出
func stopOfficial(app string) ([]string, error) {
	dir := officialDir(app)
	if err := exec.Command("pkill", "-KILL", "-f", dir).Run(); err != nil && !noProcess(err) {
		return nil, fmt.Errorf("结束官方客户端: %w", err)
	}
	for {
		err := exec.Command("pgrep", "-f", dir).Run()
		if noProcess(err) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("查询官方客户端进程: %w", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// noProcess 判断 pkill 或 pgrep 的退出码是否表示没有匹配的进程
func noProcess(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == 1
}

// startOfficial 以 args 启动官方客户端：macOS 经 open，其他系统在新会话中运行可执行文件并在其退出时回收
func startOfficial(app string, args []string) error {
	if runtime.GOOS == "darwin" {
		if output, err := exec.Command("open", append([]string{app, "--args"}, args...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("启动官方客户端: %w: %s", err, output)
		}
		return nil
	}
	cmd := exec.Command(app, args...)
	cmd.Dir = filepath.Dir(app)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动官方客户端: %w", err)
	}
	go cmd.Wait()
	return nil
}
