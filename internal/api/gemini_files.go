package api

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// geminiFileMetadata 为创建文件请求携带的文件元数据
type geminiFileMetadata struct {
	File struct {
		DisplayName string `json:"displayName"`
		MIMEType    string `json:"mimeType"`
	} `json:"file"`
}

// geminiUploadIdle 为可续传上传在没有分块到达时的保留时长，超过后上传会话失效
const geminiUploadIdle = time.Hour

// geminiUpload 为一次可续传上传的元数据、已接收的内容与空闲失效计时器
type geminiUpload struct {
	mu     sync.Mutex
	name   string
	mime   string
	data   bytes.Buffer
	expire *time.Timer
}

// geminiUploads 保存进行中的可续传上传
type geminiUploads struct {
	mu       sync.Mutex
	sessions map[string]*geminiUpload
}

// end 结束一次上传
func (uploads *geminiUploads) end(id string) {
	uploads.mu.Lock()
	delete(uploads.sessions, id)
	uploads.mu.Unlock()
}

// geminiFileObject 构造 Files API 的 File 资源，uri 为 Files API 前缀加文件 ID
func geminiFileObject(file storedFile) map[string]any {
	created := file.CreatedAt.Format(time.RFC3339Nano)
	return map[string]any{
		"name": "files/" + file.ID, "displayName": file.Name, "mimeType": file.MIME,
		"sizeBytes": strconv.FormatInt(file.Size, 10), "createTime": created, "updateTime": created,
		"uri": geminiFilesURIPrefix + file.ID, "state": "ACTIVE", "source": "UPLOADED",
	}
}

// writeGeminiFileError 写入 Files API 错误，文件不存在时为 404，超过大小上限时为 413
func writeGeminiFileError(w http.ResponseWriter, r *http.Request, err error) {
	var maxBytes *http.MaxBytesError
	switch {
	case errors.Is(err, errFileNotFound):
		writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("file %q was not found", r.PathValue("file")))
	case errors.As(err, &maxBytes):
		writeGeminiError(w, http.StatusRequestEntityTooLarge, "INVALID_ARGUMENT", fmt.Sprintf("file exceeds %d bytes", fileMaxBytes))
	default:
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	}
}

// handleGeminiFileUpload 按 X-Goog-Upload-Protocol 或 uploadType 开始可续传上传或完成 multipart 上传
func (s *server) handleGeminiFileUpload(w http.ResponseWriter, r *http.Request) {
	protocol := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Goog-Upload-Protocol")))
	if protocol == "" {
		protocol = strings.ToLower(r.URL.Query().Get("uploadType"))
	}
	switch protocol {
	case "resumable":
		s.startGeminiUpload(w, r)
	case "multipart":
		s.uploadGeminiMultipart(w, r)
	default:
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "X-Goog-Upload-Protocol must be resumable or multipart")
	}
}

// startGeminiUpload 创建可续传上传，响应头 X-Goog-Upload-URL 为后续分块的上传地址
func (s *server) startGeminiUpload(w http.ResponseWriter, r *http.Request) {
	metadata, err := readGeminiFileMetadata(r.Body)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	upload := &geminiUpload{
		name: geminiUploadName(metadata, "", r),
		mime: firstMediaType(r.Header.Get("X-Goog-Upload-Header-Content-Type"), metadata.File.MIMEType),
	}
	id := rand.Text()
	upload.expire = time.AfterFunc(geminiUploadIdle, func() { s.uploads.end(id) })
	s.uploads.mu.Lock()
	s.uploads.sessions[id] = upload
	s.uploads.mu.Unlock()
	w.Header().Set("X-Goog-Upload-URL", requestOrigin(r)+"/upload/v1beta/files?upload_id="+url.QueryEscape(id)+"&upload_protocol=resumable")
	w.Header().Set("X-Goog-Upload-Status", "active")
	w.WriteHeader(http.StatusOK)
}

// geminiUploadHandler 让携带 upload_id 的分块请求凭上传会话鉴权，其余上传请求交给公开路由
func (s *server) geminiUploadHandler(public http.Handler) http.Handler {
	chunks := bodyLimitMiddleware(http.HandlerFunc(s.handleGeminiUploadChunk))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("upload_id") {
			chunks.ServeHTTP(w, r)
			return
		}
		public.ServeHTTP(w, r)
	})
}

// handleGeminiUploadChunk 处理可续传上传的 upload、finalize、query 与 cancel 命令，finalize 时保存全部内容
func (s *server) handleGeminiUploadChunk(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("upload_id")
	s.uploads.mu.Lock()
	upload := s.uploads.sessions[id]
	s.uploads.mu.Unlock()
	if upload == nil {
		w.Header().Set("X-Goog-Upload-Status", "final")
		writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", "upload session was not found")
		return
	}
	upload.expire.Reset(geminiUploadIdle)
	upload.mu.Lock()
	defer upload.mu.Unlock()
	commands := make(map[string]bool)
	for command := range strings.SplitSeq(strings.ToLower(r.Header.Get("X-Goog-Upload-Command")), ",") {
		commands[strings.TrimSpace(command)] = true
	}
	switch {
	case commands["cancel"]:
		s.uploads.end(id)
		w.Header().Set("X-Goog-Upload-Status", "cancelled")
		w.WriteHeader(http.StatusOK)
		return
	case commands["query"]:
		w.Header().Set("X-Goog-Upload-Status", "active")
		w.Header().Set("X-Goog-Upload-Size-Received", strconv.Itoa(upload.data.Len()))
		w.WriteHeader(http.StatusOK)
		return
	case !commands["upload"] && !commands["finalize"]:
		w.Header().Set("X-Goog-Upload-Status", "active")
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "X-Goog-Upload-Command must contain upload, finalize, query or cancel")
		return
	}
	if offset := strings.TrimSpace(r.Header.Get("X-Goog-Upload-Offset")); commands["upload"] && offset != "" && offset != strconv.Itoa(upload.data.Len()) {
		w.Header().Set("X-Goog-Upload-Status", "active")
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("X-Goog-Upload-Offset %s does not match the %d bytes received", offset, upload.data.Len()))
		return
	}
	if commands["upload"] {
		if _, err := upload.data.ReadFrom(io.LimitReader(r.Body, fileMaxBytes-int64(upload.data.Len())+1)); err != nil {
			s.uploads.end(id)
			w.Header().Set("X-Goog-Upload-Status", "final")
			writeGeminiFileError(w, r, err)
			return
		}
	}
	if !commands["finalize"] {
		w.Header().Set("X-Goog-Upload-Status", "active")
		w.Header().Set("X-Goog-Upload-Size-Received", strconv.Itoa(upload.data.Len()))
		w.WriteHeader(http.StatusOK)
		return
	}
	s.uploads.end(id)
	file, err := s.files.save(upload.name, upload.mime, defaultFilePurpose, &upload.data)
	w.Header().Set("X-Goog-Upload-Status", "final")
	if err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": geminiFileObject(file)})
}

// uploadGeminiMultipart 处理元数据与文件内容位于同一 multipart 请求体的上传
func (s *server) uploadGeminiMultipart(w http.ResponseWriter, r *http.Request) {
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") || parameters["boundary"] == "" {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart upload requires a multipart Content-Type with boundary")
		return
	}
	reader := multipart.NewReader(r.Body, parameters["boundary"])
	var metadata geminiFileMetadata
	metadataRead := false
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart upload requires file content")
			return
		}
		if err != nil {
			writeGeminiFileError(w, r, err)
			return
		}
		partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if !metadataRead && part.FileName() == "" && (partType == "application/json" || part.FormName() == "metadata") {
			metadata, err = readGeminiFileMetadata(part)
			part.Close()
			if err != nil {
				writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
				return
			}
			metadataRead = true
			continue
		}
		file, err := s.files.save(geminiUploadName(metadata, part.FileName(), r),
			firstMediaType(part.Header.Get("Content-Type"), r.Header.Get("X-Goog-Upload-Header-Content-Type"), metadata.File.MIMEType), defaultFilePurpose, part)
		part.Close()
		w.Header().Set("X-Goog-Upload-Status", "final")
		if err != nil {
			writeGeminiFileError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"file": geminiFileObject(file)})
		return
	}
}

// readGeminiFileMetadata 读取创建文件请求的元数据，请求体为空时返回空元数据
func readGeminiFileMetadata(body io.Reader) (geminiFileMetadata, error) {
	var metadata geminiFileMetadata
	raw, err := io.ReadAll(body)
	if err != nil || len(bytes.TrimSpace(raw)) == 0 {
		return metadata, err
	}
	raw, _, err = geminiCamelKeys(doubleQuotedJSON(raw))
	if err == nil {
		err = json.Unmarshal(raw, &metadata)
	}
	if err != nil {
		return metadata, fmt.Errorf("file metadata: %w", err)
	}
	return metadata, nil
}

// doubleQuotedJSON 把 Google API 请求体接受的单引号字符串改写为 JSON 双引号字符串
func doubleQuotedJSON(raw []byte) []byte {
	if !bytes.ContainsRune(raw, '\'') {
		return raw
	}
	converted := make([]byte, 0, len(raw)+8)
	var quote byte
	for index := 0; index < len(raw); index++ {
		char := raw[index]
		switch {
		case quote == 0:
			if char == '\'' || char == '"' {
				quote = char
				char = '"'
			}
			converted = append(converted, char)
		case char == '\\' && index+1 < len(raw):
			index++
			if quote == '\'' && raw[index] == '\'' {
				converted = append(converted, '\'')
			} else {
				converted = append(converted, char, raw[index])
			}
		case char == quote:
			quote = 0
			converted = append(converted, '"')
		case char == '"':
			converted = append(converted, '\\', '"')
		default:
			converted = append(converted, char)
		}
	}
	return converted
}

// geminiUploadName 依次取 displayName、文件段名称与 X-Goog-Upload-File-Name 作为文件名
func geminiUploadName(metadata geminiFileMetadata, filename string, r *http.Request) string {
	for _, name := range []string{metadata.File.DisplayName, filename, r.Header.Get("X-Goog-Upload-File-Name")} {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	return "file"
}

// firstMediaType 返回第一个具体的媒体类型声明
func firstMediaType(values ...string) string {
	for _, value := range values {
		if mediaType := declaredMediaType(value); mediaType != "" {
			return mediaType
		}
	}
	return ""
}

// requestOrigin 返回客户端访问本服务使用的协议与主机
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host
}

// handleGeminiFileGet 返回文件的 File 资源
func (s *server) handleGeminiFileGet(w http.ResponseWriter, r *http.Request) {
	file, err := s.files.get(r.PathValue("file"))
	if err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, geminiFileObject(file))
}

// handleGeminiFileDelete 删除文件并返回空对象
func (s *server) handleGeminiFileDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.files.remove(r.PathValue("file")); err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleGeminiFileList 按创建时间从新到旧分页列出文件，pageToken 为下一页的起始位置
func (s *server) handleGeminiFileList(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	size, offset := 10, 0
	if value := firstQueryValue(query, "pageSize", "page_size"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "pageSize must be a non-negative integer")
			return
		}
		if parsed > 0 {
			size = min(parsed, 100)
		}
	}
	if value := firstQueryValue(query, "pageToken", "page_token"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "pageToken is invalid")
			return
		}
		offset = parsed
	}
	files, err := s.files.list()
	if err != nil {
		writeGeminiFileError(w, r, err)
		return
	}
	page := files[min(offset, len(files)):min(offset+size, len(files))]
	objects := make([]map[string]any, 0, len(page))
	for _, file := range page {
		objects = append(objects, geminiFileObject(file))
	}
	body := map[string]any{"files": objects}
	if offset+size < len(files) {
		body["nextPageToken"] = strconv.Itoa(offset + size)
	}
	writeJSON(w, http.StatusOK, body)
}

// firstQueryValue 返回第一个非空查询参数，查询参数接受驼峰与下划线两种字段名
func firstQueryValue(query url.Values, names ...string) string {
	for _, name := range names {
		if value := query.Get(name); value != "" {
			return value
		}
	}
	return ""
}
