package api

import (
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

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
	base  string
	store *mediaStore
}

// mediaStore 是按内容命名保存生成媒体的目录，总量超过 limit 字节时删除最久未使用的文件
type mediaStore struct {
	dir   string
	limit int64
	mu    sync.Mutex
}

// mediaFile 是媒体目录中的一个文件，修改时间为最近一次使用的时间
type mediaFile struct {
	name string
	size int64
	used time.Time
}

// save 写入媒体文件，已存在的文件只刷新使用时间，写入新文件后按上限回收
func (store *mediaStore) save(name string, data []byte) error {
	path := filepath.Join(store.dir, name)
	if _, err := os.Stat(path); err == nil {
		store.touch(name)
		return nil
	}
	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	if err := store.trim(); err != nil {
		slog.Warn("媒体目录回收失败", "dir", store.dir, "error", err)
	}
	return nil
}

// touch 把媒体目录中文件的修改时间刷新为当前时间，指向目录外的名称不处理
func (store *mediaStore) touch(name string) {
	if !filepath.IsLocal(name) {
		return
	}
	now := time.Now()
	if err := os.Chtimes(filepath.Join(store.dir, name), now, now); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("刷新媒体文件使用时间失败", "file", name, "error", err)
	}
}

// trim 在目录总量超过上限时按使用时间从旧到新删除文件，直到总量不超过上限
func (store *mediaStore) trim() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	entries, err := os.ReadDir(store.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	files := make([]mediaFile, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		files = append(files, mediaFile{name: entry.Name(), size: info.Size(), used: info.ModTime()})
		total += info.Size()
	}
	slices.SortFunc(files, func(a, b mediaFile) int { return a.used.Compare(b.used) })
	var removeErr error
	for _, file := range files {
		if total <= store.limit {
			break
		}
		if err := os.Remove(filepath.Join(store.dir, file.name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			removeErr = errors.Join(removeErr, err)
			continue
		}
		total -= file.size
	}
	return removeErr
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
	return mediaOutput{base: strings.TrimRight(cmp.Or(base, requestOrigin(r)), "/"), store: s.media}
}

// save 在链接模式下把媒体按内容命名写入媒体目录，同名文件已存在时只刷新使用时间
func (output mediaOutput) save(media antigravity.Blob) error {
	if output.base == "" {
		return nil
	}
	return output.store.save(mediaFileName(media), media.Data)
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

// assistantMediaPattern 匹配 assistant 正文中以 data URL 或本服务 /media/ 链接写出的 Markdown 图片，第一组为 data URL，第二组为媒体文件名
var assistantMediaPattern = regexp.MustCompile(`!\[[^\]]*\]\((?:(data:image/[A-Za-z0-9.+-]+;base64,[A-Za-z0-9+/_=\r\n-]+)|https?://[^\s)]+/media/([0-9a-f]{64}\.(?:jpg|png|webp|gif)))\)`)

// restoreMediaLinks 把 assistant 正文中本服务写出的 Markdown 图片恢复为图片输入，媒体目录中已不存在的链接保留为文本
func (s *server) restoreMediaLinks(contents []antigravity.Content) error {
	for index := range contents {
		if contents[index].Role != antigravity.RoleAssistant {
			continue
		}
		parts := make([]antigravity.Part, 0, len(contents[index].Parts))
		for _, part := range contents[index].Parts {
			restored, err := s.mediaLinkParts(part)
			if err != nil {
				return err
			}
			parts = append(parts, restored...)
		}
		contents[index].Parts = parts
	}
	return nil
}

// mediaLinkParts 把文本 part 按其中的 Markdown 图片拆成文本与图片 part
func (s *server) mediaLinkParts(part antigravity.Part) ([]antigravity.Part, error) {
	if part.Text == "" || part.Thought {
		return []antigravity.Part{part}, nil
	}
	text := part.Text
	var parts []antigravity.Part
	position := 0
	for _, match := range assistantMediaPattern.FindAllStringSubmatchIndex(text, -1) {
		var image antigravity.Part
		if match[2] >= 0 {
			decoded, err := dataURLPart(text[match[2]:match[3]], "")
			if err != nil {
				return nil, err
			}
			image = decoded
		} else {
			name := text[match[4]:match[5]]
			if s.media.dir == "" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(s.media.dir, name))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			s.media.touch(name)
			image = antigravity.Part{InlineData: &antigravity.Blob{MIME: mediaTypes[filepath.Ext(name)], Data: data}}
		}
		if match[0] > position {
			parts = append(parts, antigravity.Part{Text: text[position:match[0]]})
		}
		parts = append(parts, image)
		position = match[1]
	}
	if position == 0 {
		return []antigravity.Part{part}, nil
	}
	if position < len(text) {
		parts = append(parts, antigravity.Part{Text: text[position:]})
	}
	return parts, nil
}

// mediaTypes 为媒体目录中图片扩展名对应的 MIME 类型
var mediaTypes = map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".webp": "image/webp", ".gif": "image/gif"}

// handleMedia 返回链接模式下保存的生成媒体并刷新它的使用时间，内容不变，长期缓存
func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFileFS(w, r, os.DirFS(s.media.dir), r.PathValue("file"))
	s.media.touch(r.PathValue("file"))
}
