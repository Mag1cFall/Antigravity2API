package api

import (
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// mediaExtensions 为生成媒体类型对应的文件扩展名
var mediaExtensions = map[string]string{
	"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/gif": ".gif",
	"audio/mpeg": ".mp3", "audio/wav": ".wav", "video/mp4": ".mp4",
}

// mediaOutput 决定 Chat、Messages 正文与 Images url 格式引用生成媒体的方式
type mediaOutput struct {
	// base 为媒体链接的服务地址，为空时以 data URL 内联
	base string
	dir  string
}

// mediaOutputFor 按当前设置与请求地址返回本次请求的媒体输出方式，MEDIA_BASE_URL 为空时取请求的协议与 Host
func (s *server) mediaOutputFor(r *http.Request) mediaOutput {
	if s.mediaLinks == nil {
		return mediaOutput{}
	}
	enabled, base := s.mediaLinks()
	if !enabled {
		return mediaOutput{}
	}
	return mediaOutput{base: strings.TrimRight(cmp.Or(base, requestOrigin(r)), "/"), dir: s.mediaDir}
}

// save 在链接模式下把媒体按内容命名写入媒体目录，同名文件已存在时不再写入
func (output mediaOutput) save(media antigravity.Blob) error {
	if output.base == "" {
		return nil
	}
	path := filepath.Join(output.dir, mediaFileName(media))
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(output.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, media.Data, 0o644)
}

// saveResults 保存生成结果中的全部媒体
func (output mediaOutput) saveResults(results ...generationResult) error {
	for _, result := range results {
		for _, media := range result.media {
			if err := output.save(media); err != nil {
				return err
			}
		}
	}
	return nil
}

// link 返回媒体的引用地址：链接模式下为本服务的 /media/ 链接，否则为 data URL
func (output mediaOutput) link(media antigravity.Blob) string {
	if output.base != "" {
		return output.base + "/media/" + mediaFileName(media)
	}
	return "data:" + media.MIME + ";base64," + base64.StdEncoding.EncodeToString(media.Data)
}

// markdown 把媒体写成 Markdown，图片用图片语法
func (output mediaOutput) markdown(media antigravity.Blob) string {
	if strings.HasPrefix(media.MIME, "image/") {
		return "![image](" + output.link(media) + ")"
	}
	return "[media](" + output.link(media) + ")"
}

// mediaFileName 返回按内容 SHA-256 命名的文件名
func mediaFileName(media antigravity.Blob) string {
	sum := sha256.Sum256(media.Data)
	mimeType, _, _ := strings.Cut(media.MIME, ";")
	return hex.EncodeToString(sum[:]) + cmp.Or(mediaExtensions[strings.ToLower(strings.TrimSpace(mimeType))], ".bin")
}

// handleMedia 返回链接模式下保存的生成媒体，内容不变，长期缓存
func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFileFS(w, r, os.DirFS(s.mediaDir), r.PathValue("file"))
}
