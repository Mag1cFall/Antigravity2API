package api

import (
	"context"
	"fmt"
	"log/slog"
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
	// FilesLimit 为上传文件目录的总字节上限，超过时从最早上传的文件起删除
	FilesLimit int64
	// MediaDir 为以本服务链接输出时生成媒体的保存目录
	MediaDir string
	// MediaLimit 为媒体目录的总字节上限，超过时删除最久未使用的文件
	MediaLimit int64
	// MediaLinks 返回是否以本服务链接输出生成的媒体，以及设置的服务地址
	MediaLinks func() (bool, string)
	// WaitLaunch 在服务启动、还没有就绪账户期间等待，公开 API 请求经它等待后再处理
	WaitLaunch func(context.Context)
	// ResponseModes 返回当前的非流式保活与假流式设置
	ResponseModes func() ResponseModes
	// ResponseStates 与 InteractionStates 持久保存 Responses 与 Interactions 的续接节点，为空时只在内存中保存
	ResponseStates    StateStore
	InteractionStates StateStore
}

// ResponseModes 是公开 API 的响应方式设置
type ResponseModes struct {
	// NonstreamKeepalive 让非流式请求在等待结果期间定时写出换行
	NonstreamKeepalive bool
	// FakeStream 让流式请求收齐完整结果后再按流式输出
	FakeStream bool
}

// server 是公开协议层的处理函数共用的状态
type server struct {
	service        antigravity.Service
	responseStates *responseStateStore
	interactions   *responseStateStore
	files          *fileStore
	uploads        *geminiUploads
	media          *mediaStore
	mediaLinks     func() (bool, string)
	responseModes  func() ResponseModes
	// background 按响应 ID 保存进行中的 Responses 后台响应
	background sync.Map
}

// idSequence 是 newID 的进程内序号
var idSequence atomic.Uint64

// NewHandler 返回挂载四个公开协议与目录端点的 http.Handler
func NewHandler(service antigravity.Service, options Options) http.Handler {
	s := &server{service: service, responseStates: newResponseStateStore(options.ResponseStates), interactions: newResponseStateStore(options.InteractionStates), files: &fileStore{dir: options.FilesDir, limit: options.FilesLimit}, uploads: &geminiUploads{sessions: make(map[string]*geminiUpload)}, media: &mediaStore{dir: options.MediaDir, limit: options.MediaLimit}, mediaLinks: options.MediaLinks, responseModes: options.ResponseModes}
	if options.FilesDir != "" {
		go s.files.reclaim()
	}
	if options.MediaDir != "" {
		if err := s.media.trim(); err != nil {
			slog.Warn("媒体目录回收失败", "dir", options.MediaDir, "error", err)
		}
	}
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
	handler := corsMiddleware(bodyLimitMiddleware(browserOriginMiddleware(keys, authMiddleware(keys, waitLaunchMiddleware(options.WaitLaunch, withFiles(s.files, public))))))
	root := http.NewServeMux()
	root.Handle("/v1/", handler)
	root.Handle("/v1beta/", handler)
	root.Handle("/upload/", corsMiddleware(s.geminiUploadHandler(handler)))
	if options.MediaDir != "" {
		root.Handle("GET /media/{file}", corsMiddleware(http.HandlerFunc(s.handleMedia)))
	}
	return root
}

// waitLaunchMiddleware 让请求在服务启动、还没有就绪账户期间等待后再处理，wait 为空时直接处理
func waitLaunchMiddleware(wait func(context.Context), next http.Handler) http.Handler {
	if wait == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wait(r.Context())
		next.ServeHTTP(w, r)
	})
}

// newID 返回带前缀、纳秒时间与序号的 ID
func newID(prefix string) string {
	return fmt.Sprintf("%s_%d_%d", prefix, time.Now().UnixNano(), idSequence.Add(1))
}
