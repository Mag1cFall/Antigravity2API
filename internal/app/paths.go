package app

import (
	"cmp"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// pathRootsQuery 是目录选择器请求文件系统根时的 path 取值
const pathRootsQuery = "roots"

// pathEntry 是目录选择器中的一个目录或文件，File 为真时是文件，Size 为文件字节数
type pathEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	File bool   `json:"file,omitempty"`
	Size int64  `json:"size,omitempty"`
}

// pathListing 是目录选择器当前位置的子目录，Path 为空表示文件系统根列表
type pathListing struct {
	Path    string      `json:"path"`
	Parent  string      `json:"parent"`
	Entries []pathEntry `json:"entries"`
}

// listPaths 列出服务所在机器上 path 或其最近的已存在上级目录下不以点开头的子目录，files 为逗号分隔的扩展名时再列出这些扩展名的文件（排在目录之后），相对路径按工作目录解析，上级目录都不存在时返回文件系统根
func (s *server) listPaths(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == pathRootsQuery {
		writeJSON(w, http.StatusOK, pathListing{Entries: pathRoots()})
		return
	}
	absolute, err := filepath.Abs(cmp.Or(path, "."))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	for {
		if info, err := os.Stat(absolute); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			writeJSON(w, http.StatusOK, pathListing{Entries: pathRoots()})
			return
		}
		absolute = parent
	}
	items, err := os.ReadDir(absolute)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	extensions := strings.Split(strings.ToLower(r.URL.Query().Get("files")), ",")
	entries := make([]pathEntry, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(item.Name(), ".") {
			continue
		}
		if item.IsDir() {
			entries = append(entries, pathEntry{Name: item.Name(), Path: filepath.Join(absolute, item.Name())})
		} else if extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(item.Name())), "."); extension != "" && slices.Contains(extensions, extension) {
			if info, err := item.Info(); err == nil && info.Mode().IsRegular() {
				entries = append(entries, pathEntry{Name: item.Name(), Path: filepath.Join(absolute, item.Name()), File: true, Size: info.Size()})
			}
		}
	}
	slices.SortFunc(entries, func(a, b pathEntry) int {
		if a.File != b.File {
			if a.File {
				return 1
			}
			return -1
		}
		return cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	parent := filepath.Dir(absolute)
	if parent == absolute {
		parent = pathRootsQuery
	}
	writeJSON(w, http.StatusOK, pathListing{Path: absolute, Parent: parent, Entries: entries})
}

// pathRoots 返回文件系统根：Windows 为存在的盘符，其余系统为 /
func pathRoots() []pathEntry {
	if runtime.GOOS != "windows" {
		return []pathEntry{{Name: "/", Path: "/"}}
	}
	roots := make([]pathEntry, 0, 4)
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err == nil {
			roots = append(roots, pathEntry{Name: root, Path: root})
		}
	}
	return roots
}
