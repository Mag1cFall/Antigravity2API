package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// place 是文件选择器侧栏中的一个常用位置，Name 为 downloads、desktop、documents 或 home
type place struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// listPlaces 返回服务所在电脑上存在的下载、桌面、文档与主目录
func (s *server) listPlaces(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"places": places()})
}

// places 返回存在的常用位置，下载目录在前
func places() []place {
	home, _ := os.UserHomeDir()
	result := []place{}
	for _, name := range []string{"downloads", "desktop", "documents", "home"} {
		dir := home
		if name != "home" {
			dir = userDir(home, name)
		}
		if info, err := os.Stat(dir); dir != "" && err == nil && info.IsDir() {
			result = append(result, place{Name: name, Path: dir})
		}
	}
	return result
}

// revealPath 在服务所在电脑的文件管理器中显示文件，只接受来自这台电脑的请求
func (s *server) revealPath(w http.ResponseWriter, r *http.Request) {
	if !loopback(r) {
		writeError(w, http.StatusForbidden, "reveal_remote", "只能在运行服务的电脑上打开文件夹")
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if _, err := os.Stat(input.Path); err != nil {
		writeError(w, http.StatusNotFound, "file_not_found", err.Error())
		return
	}
	if err := reveal(input.Path); err != nil {
		writeError(w, http.StatusInternalServerError, "reveal_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// saveFile 以 write 写入目录 dir 中名为 name 的新文件，同名文件已存在时在扩展名前加 (2)、(3)，返回写入的绝对路径；写入失败时删除该文件
func saveFile(dir, name string, perm fs.FileMode, write func(io.Writer) error) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("文件夹不存在: %s", dir)
	}
	extension := filepath.Ext(name)
	base := strings.TrimSuffix(name, extension)
	for index := 1; ; index++ {
		path := filepath.Join(dir, name)
		if index > 1 {
			path = filepath.Join(dir, base+" ("+strconv.Itoa(index)+")"+extension)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		err = errors.Join(write(file), file.Close())
		if err != nil {
			os.Remove(path)
			return "", err
		}
		return path, nil
	}
}
