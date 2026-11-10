// Package webui 提供内嵌的管理端静态资源
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/vearutop/statigz"
)

//go:embed all:dist
var embedded embed.FS

// Handler 返回管理端静态资源处理器：构建产物以 gzip 内嵌，接受 gzip 的客户端直接收到，其余客户端收到解压后的内容；assets 下带哈希的文件长期缓存，其余文件与未知页面路径返回的入口页不缓存
func Handler() http.Handler {
	index, err := fs.ReadFile(embedded, "dist/index.html")
	if err != nil {
		panic(err)
	}
	static := statigz.FileServer(embedded, statigz.FSPrefix("dist"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name := path.Clean("/" + r.URL.Path)
		asset := strings.HasPrefix(name, "/assets/")
		if name != "/" && name != "/index.html" && static.Found(r) {
			if asset {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			r.Header.Del("If-None-Match")
			static.ServeHTTP(w, r)
			return
		}
		if asset {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
	})
}
