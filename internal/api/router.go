package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// Options 为公开协议层的运行参数
type Options struct {
	// APIKeys 返回当前接受的公开 API key，为空时不校验
	APIKeys func() []string
	// FilesDir 为 Files API 上传文件的保存目录
	FilesDir string
	// MediaDir 为以本服务链接输出时生成媒体的保存目录
	MediaDir string
	// MediaLinks 返回是否以本服务链接输出生成的媒体，以及设置的服务地址
	MediaLinks func() (bool, string)
}

type server struct {
	service        antigravity.Service
	responseStates *responseStateStore
	interactions   *responseStateStore
	files          *fileStore
	uploads        *geminiUploads
	mediaDir       string
	mediaLinks     func() (bool, string)
	// background 按响应 ID 保存进行中的 Responses 后台响应
	background sync.Map
}

var idSequence atomic.Uint64

// NewHandler 返回挂载四个公开协议与目录端点的 http.Handler
func NewHandler(service antigravity.Service, options Options) http.Handler {
	s := &server{service: service, responseStates: newResponseStateStore(), interactions: newResponseStateStore(), files: &fileStore{dir: options.FilesDir}, uploads: &geminiUploads{sessions: make(map[string]*geminiUpload)}, mediaDir: options.MediaDir, mediaLinks: options.MediaLinks}
	public := http.NewServeMux()
	public.HandleFunc("GET /v1/models", s.handleOpenAIModels)
	public.HandleFunc("GET /v1/models/{model...}", s.handleOpenAIModel)
	public.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	public.HandleFunc("POST /v1/completions", s.handleCompletions)
	public.HandleFunc("POST /v1/responses", s.handleResponses)
	public.HandleFunc("POST /v1/responses/input_tokens", s.handleResponsesInputTokens)
	public.HandleFunc("POST /v1/responses/compact", s.handleResponsesCompact)
	public.HandleFunc("GET /v1/responses/{response}", s.handleResponseGet)
	public.HandleFunc("DELETE /v1/responses/{response}", s.handleResponseDelete)
	public.HandleFunc("GET /v1/responses/{response}/input_items", s.handleResponseInputItems)
	public.HandleFunc("POST /v1/responses/{response}/cancel", s.handleResponseCancel)
	public.HandleFunc("POST /v1/images/generations", s.handleOpenAIImages)
	public.HandleFunc("POST /v1/images/edits", s.handleOpenAIImageEdits)
	public.HandleFunc("POST /v1/audio/transcriptions", s.handleOpenAITranscription)
	public.HandleFunc("POST /v1/audio/translations", s.handleOpenAITranslation)
	public.HandleFunc("POST /v1/files", s.handleFileUpload)
	public.HandleFunc("GET /v1/files", s.handleFileList)
	public.HandleFunc("GET /v1/files/{file}", s.handleFileGet)
	public.HandleFunc("GET /v1/files/{file}/content", s.handleFileContent)
	public.HandleFunc("DELETE /v1/files/{file}", s.handleFileDelete)
	for _, prefix := range []string{"/v1", "/v1beta"} {
		public.HandleFunc("POST "+prefix+"/interactions", s.handleInteraction)
		public.HandleFunc("GET "+prefix+"/interactions/{id}", s.handleInteractionGet)
		public.HandleFunc("DELETE "+prefix+"/interactions/{id}", s.handleInteractionDelete)
		public.HandleFunc("POST "+prefix+"/interactions/{id}/cancel", s.handleInteractionCancel)
	}
	public.HandleFunc("POST /v1/messages", s.handleAnthropicMessages)
	public.HandleFunc("POST /v1/messages/count_tokens", s.handleAnthropicCountTokens)
	public.HandleFunc("GET /v1beta/models", s.handleGeminiModels)
	public.HandleFunc("GET /v1beta/models/{model...}", s.handleGeminiModel)
	public.HandleFunc("POST /v1beta/models/{action}", s.handleGeminiAction)
	public.HandleFunc("POST /upload/v1beta/files", s.handleGeminiFileUpload)
	public.HandleFunc("GET /v1beta/files", s.handleGeminiFileList)
	public.HandleFunc("GET /v1beta/files/{file}", s.handleGeminiFileGet)
	public.HandleFunc("DELETE /v1beta/files/{file}", s.handleGeminiFileDelete)
	public.HandleFunc("/", writeRouteNotFound)

	keys := func() []string {
		var keys []string
		for _, key := range options.APIKeys() {
			if key = strings.TrimSpace(key); key != "" {
				keys = append(keys, key)
			}
		}
		return keys
	}
	handler := corsMiddleware(bodyLimitMiddleware(browserOriginMiddleware(keys, authMiddleware(keys, withFiles(s.files, public)))))
	root := http.NewServeMux()
	root.Handle("/v1/", handler)
	root.Handle("/v1beta/", handler)
	root.Handle("/upload/", corsMiddleware(s.geminiUploadHandler(handler)))
	if options.MediaDir != "" {
		root.Handle("GET /media/{file}", corsMiddleware(http.HandlerFunc(s.handleMedia)))
	}
	return root
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), idSequence.Add(1))
}
