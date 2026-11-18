//go:build !windows

package app

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// xdgKeys 是常用位置在 XDG user-dirs.dirs 中的键
var xdgKeys = map[string]string{"downloads": "XDG_DOWNLOAD_DIR", "desktop": "XDG_DESKTOP_DIR", "documents": "XDG_DOCUMENTS_DIR"}

// defaultDirs 是常用位置在主目录下的默认文件夹名
var defaultDirs = map[string]string{"downloads": "Downloads", "desktop": "Desktop", "documents": "Documents"}

// userDir 返回常用位置：Linux 先读 ~/.config/user-dirs.dirs，其余为主目录下的默认文件夹
func userDir(home, name string) string {
	if home == "" {
		return ""
	}
	if runtime.GOOS == "linux" {
		if file, err := os.Open(filepath.Join(home, ".config", "user-dirs.dirs")); err == nil {
			defer file.Close()
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				if value, ok := strings.CutPrefix(strings.TrimSpace(scanner.Text()), xdgKeys[name]+"="); ok {
					return strings.Replace(strings.Trim(value, `"`), "$HOME", home, 1)
				}
			}
		}
	}
	return filepath.Join(home, defaultDirs[name])
}

// reveal 在文件管理器中显示 path：macOS 选中该文件，其余系统打开所在文件夹
func reveal(path string) error {
	command := exec.Command("xdg-open", filepath.Dir(path))
	if runtime.GOOS == "darwin" {
		command = exec.Command("open", "-R", path)
	}
	if err := command.Start(); err != nil {
		return err
	}
	go command.Wait()
	return nil
}
