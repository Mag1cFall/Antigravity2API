package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// fileMaxBytes 为单个上传文件的大小上限
	fileMaxBytes int64 = 512 << 20
	// defaultFilePurpose 为省略 purpose 时的文件用途
	defaultFilePurpose = "user_data"
	// anthropicFilePagePrefix 为 Anthropic Files API next_page 游标的前缀，其余部分为上一页最后一个文件的 ID
	anthropicFilePagePrefix = "page_"
)

// errFileNotFound 表示文件 ID 不存在
var errFileNotFound = errors.New("file not found")

// fileIDPattern 匹配本服务签发的文件 ID
var fileIDPattern = regexp.MustCompile(`^file-[a-z0-9]+$`)

// storedFile 为上传文件的元数据
type storedFile struct {
	ID        string    `json:"id"`
	Name      string    `json:"filename"`
	MIME      string    `json:"mime_type"`
	Purpose   string    `json:"purpose"`
	Size      int64     `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// fileStore 在目录中保存上传文件：内容文件名为文件 ID，元数据为同名 .json
type fileStore struct {
	dir string
}

// fileStoreContext 为请求上下文中文件存储的键
type fileStoreContext struct{}

// withFiles 把文件存储放入请求上下文，供发送前把文件引用内联
func withFiles(store *fileStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), fileStoreContext{}, store)))
	})
}

// filesFrom 返回请求上下文中的文件存储
func filesFrom(ctx context.Context) *fileStore {
	store, _ := ctx.Value(fileStoreContext{}).(*fileStore)
	return store
}

// save 保存文件内容与元数据，未声明具体 MIME 时按内容与文件名判断
func (store *fileStore) save(name, mimeType, purpose string, content io.Reader) (storedFile, error) {
	if err := os.MkdirAll(store.dir, 0o755); err != nil {
		return storedFile{}, err
	}
	probe := make([]byte, 512)
	count, err := io.ReadFull(content, probe)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return storedFile{}, err
	}
	if count == 0 {
		return storedFile{}, errors.New("file must not be empty")
	}
	if mimeType = declaredMediaType(mimeType); mimeType == "" {
		mimeType = detectMediaType(name, probe[:count])
	}
	file := storedFile{ID: "file-" + strings.ToLower(rand.Text()), Name: name, MIME: mimeType, Purpose: purpose, CreatedAt: time.Now().UTC()}
	target, err := os.Create(filepath.Join(store.dir, file.ID))
	if err != nil {
		return storedFile{}, err
	}
	file.Size, err = io.Copy(target, io.LimitReader(io.MultiReader(bytes.NewReader(probe[:count]), content), fileMaxBytes+1))
	err = errors.Join(err, target.Close())
	if err == nil && file.Size > fileMaxBytes {
		err = &http.MaxBytesError{Limit: fileMaxBytes}
	}
	if err == nil {
		var metadata []byte
		metadata, _ = json.Marshal(file)
		err = os.WriteFile(filepath.Join(store.dir, file.ID+".json"), metadata, 0o644)
	}
	if err != nil {
		os.Remove(filepath.Join(store.dir, file.ID))
		return storedFile{}, err
	}
	return file, nil
}

// get 返回文件元数据
func (store *fileStore) get(id string) (storedFile, error) {
	if !fileIDPattern.MatchString(id) {
		return storedFile{}, errFileNotFound
	}
	raw, err := os.ReadFile(filepath.Join(store.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return storedFile{}, errFileNotFound
	}
	if err != nil {
		return storedFile{}, err
	}
	var file storedFile
	return file, json.Unmarshal(raw, &file)
}

// read 返回文件元数据与内容
func (store *fileStore) read(id string) (storedFile, []byte, error) {
	file, err := store.get(id)
	if err != nil {
		return file, nil, err
	}
	data, err := os.ReadFile(filepath.Join(store.dir, id))
	return file, data, err
}

// list 按创建时间从新到旧返回全部文件
func (store *fileStore) list() ([]storedFile, error) {
	entries, err := os.ReadDir(store.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var files []storedFile
	for _, entry := range entries {
		if id, ok := strings.CutSuffix(entry.Name(), ".json"); ok {
			file, err := store.get(id)
			if err != nil {
				return nil, err
			}
			files = append(files, file)
		}
	}
	slices.SortFunc(files, func(a, b storedFile) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return files, nil
}

// remove 删除文件内容与元数据
func (store *fileStore) remove(id string) error {
	if _, err := store.get(id); err != nil {
		return err
	}
	return errors.Join(os.Remove(filepath.Join(store.dir, id+".json")), os.Remove(filepath.Join(store.dir, id)))
}

// declaredMediaType 返回声明的具体媒体类型，缺失、不含子类型或为通用二进制时返回空
func declaredMediaType(value string) string {
	mediaType, _, _ := mime.ParseMediaType(value)
	if !strings.Contains(mediaType, "/") || mediaType == "application/octet-stream" {
		return ""
	}
	return mediaType
}

// anthropicFilesRequest 判断文件请求是否来自 Anthropic Files API 客户端
func anthropicFilesRequest(r *http.Request) bool {
	return r.Header.Get("Anthropic-Version") != "" || strings.Contains(r.Header.Get("Anthropic-Beta"), "files-api-")
}

// fileResponse 按请求协议返回 Anthropic 文件元数据或 OpenAI 文件对象
func fileResponse(r *http.Request, file storedFile) map[string]any {
	if anthropicFilesRequest(r) {
		return map[string]any{
			"id": file.ID, "type": "file", "filename": file.Name, "mime_type": file.MIME,
			"size_bytes": file.Size, "created_at": file.CreatedAt.Format(time.RFC3339), "downloadable": true,
		}
	}
	return map[string]any{
		"id": file.ID, "object": "file", "bytes": file.Size, "created_at": file.CreatedAt.Unix(),
		"filename": file.Name, "purpose": file.Purpose, "status": "processed", "expires_at": nil, "status_details": nil,
	}
}

// writeFileError 按文件请求的协议写入错误
func writeFileError(w http.ResponseWriter, r *http.Request, err error) {
	status, code := http.StatusBadRequest, "invalid_request"
	var maxBytes *http.MaxBytesError
	switch {
	case errors.Is(err, errFileNotFound):
		status, code = http.StatusNotFound, "file_not_found"
		err = fmt.Errorf("no such file: %s", r.PathValue("file"))
	case errors.As(err, &maxBytes):
		status, code = http.StatusRequestEntityTooLarge, "file_too_large"
		err = fmt.Errorf("file exceeds %d bytes", fileMaxBytes)
	}
	if anthropicFilesRequest(r) {
		writeAnthropicError(w, status, anthropicStatusErrorType(status), err.Error())
		return
	}
	writeOpenAIError(w, status, code, err.Error())
}

// handleFileUpload 保存 multipart 的 file 字段，Anthropic 请求返回 Files API 文件元数据，其余返回 OpenAI 文件对象
func (s *server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, fileMaxBytes+1<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeFileError(w, r, err)
		return
	}
	content, header, err := r.FormFile("file")
	if err != nil {
		writeFileError(w, r, fmt.Errorf("file is required"))
		return
	}
	defer content.Close()
	purpose := strings.TrimSpace(r.FormValue("purpose"))
	if purpose == "" {
		purpose = defaultFilePurpose
	}
	file, err := s.files.save(header.Filename, header.Header.Get("Content-Type"), purpose, content)
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fileResponse(r, file))
}

// handleFileGet 返回文件元数据
func (s *server) handleFileGet(w http.ResponseWriter, r *http.Request) {
	file, err := s.files.get(r.PathValue("file"))
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, fileResponse(r, file))
}

// handleFileContent 返回文件内容
func (s *server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	file, data, err := s.files.read(r.PathValue("file"))
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", file.MIME)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// handleFileDelete 删除文件
func (s *server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("file")
	if err := s.files.remove(id); err != nil {
		writeFileError(w, r, err)
		return
	}
	if anthropicFilesRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "type": "file_deleted"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "object": "file", "deleted": true})
}

// handleFileList 列出文件：Anthropic 请求按 limit、after_id、before_id 与 page 游标分页，其余按 OpenAI 的 purpose、order、after 与 limit 过滤分页
func (s *server) handleFileList(w http.ResponseWriter, r *http.Request) {
	files, err := s.files.list()
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	query := r.URL.Query()
	id := func(file storedFile) string { return file.ID }
	body := map[string]any{"has_more": false, "first_id": nil, "last_id": nil}
	var listed []storedFile
	var hasMore bool
	if anthropicFilesRequest(r) {
		if page := query.Get("page"); page != "" {
			query.Set("after_id", strings.TrimPrefix(page, anthropicFilePagePrefix))
		}
		listed, hasMore, err = anthropicListPage(query, files, id, listLimits{fallback: 20, min: 1, max: 1000})
		body["next_page"] = nil
		if hasMore && query.Get("before_id") == "" {
			body["next_page"] = anthropicFilePagePrefix + listed[len(listed)-1].ID
		}
	} else {
		if purpose := query.Get("purpose"); purpose != "" {
			files = slices.DeleteFunc(files, func(file storedFile) bool { return file.Purpose != purpose })
		}
		listed, hasMore, err = openAIListPage(query, files, id, listLimits{fallback: 10000, min: 1, max: 10000})
		body["object"] = "list"
	}
	if err != nil {
		writeFileError(w, r, err)
		return
	}
	data := make([]any, 0, len(listed))
	for _, file := range listed {
		data = append(data, fileResponse(r, file))
	}
	body["data"], body["has_more"] = data, hasMore
	if len(listed) > 0 {
		body["first_id"], body["last_id"] = listed[0].ID, listed[len(listed)-1].ID
	}
	writeJSON(w, http.StatusOK, body)
}

// openAIListPage 按 order、after 与 limit 返回一页对象及其后是否还有对象，items 按创建时间从新到旧排列，未知的 after 返回空页
func openAIListPage[T any](query url.Values, items []T, id func(T) string, limits listLimits) ([]T, bool, error) {
	limit, err := listLimit(query, limits)
	if err != nil {
		return nil, false, err
	}
	switch query.Get("order") {
	case "", "desc":
	case "asc":
		items = slices.Clone(items)
		slices.Reverse(items)
	default:
		return nil, false, errors.New("order must be asc or desc")
	}
	start := 0
	if after := query.Get("after"); after != "" {
		start = len(items)
		if index := slices.IndexFunc(items, func(item T) bool { return id(item) == after }); index >= 0 {
			start = index + 1
		}
	}
	end := min(start+limit, len(items))
	return items[start:end], end < len(items), nil
}
