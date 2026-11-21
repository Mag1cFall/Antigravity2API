package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
)

// maxPublicBodyBytes 为公开接口请求体上限
const maxPublicBodyBytes int64 = 512<<20/3*4 + 1<<20

// corsMiddleware 允许任意来源的跨域请求并直接响应预检请求
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Expose-Headers", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// LoopbackHost 判断 Host 或 Origin 主机名是否为 localhost 或回环地址
func LoopbackHost(host string) bool {
	name := host
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		name = hostname
	}
	name = strings.Trim(name, "[]")
	if strings.EqualFold(name, "localhost") {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// apiKeyContext 为请求上下文中通过校验的 API key 的键
type apiKeyContext struct{}

// browserOriginMiddleware 在未配置 API key 时拒绝外部网页与 null 来源的浏览器请求
func browserOriginMiddleware(keys func() []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originValue := strings.TrimSpace(r.Header.Get("Origin"))
		if originValue == "" || len(keys()) > 0 {
			next.ServeHTTP(w, r)
			return
		}
		origin, err := url.Parse(originValue)
		webOrigin := err != nil || originValue == "null" || origin.Scheme == "http" || origin.Scheme == "https"
		if webOrigin && (err != nil || !LoopbackHost(origin.Host)) {
			writeAuthError(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bodyLimitMiddleware 限制公开接口请求体大小
func bodyLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxPublicBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// DecodeBody 把 gzip、x-gzip 与 zstd 编码的请求体换成解压后的正文并去掉 Content-Encoding 与 Content-Length，解压后的正文同样受 maxPublicBodyBytes 限制；没有编码或为 identity 时不变；其他编码返回 UnsupportedEncodingError，正文不是该编码时返回解压错误
func DecodeBody(w http.ResponseWriter, r *http.Request) error {
	var decoded io.ReadCloser
	switch encoding := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))); encoding {
	case "", "identity":
		return nil
	case "gzip", "x-gzip":
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			return err
		}
		decoded = reader
	case "zstd":
		reader, err := zstd.NewReader(r.Body, zstd.WithDecoderConcurrency(1))
		if err != nil {
			return err
		}
		decoded = reader.IOReadCloser()
	default:
		return &UnsupportedEncodingError{Encoding: encoding}
	}
	r.Body = http.MaxBytesReader(w, decoded, maxPublicBodyBytes)
	r.Header.Del("Content-Encoding")
	r.Header.Del("Content-Length")
	r.ContentLength = -1
	return nil
}

// UnsupportedEncodingError 是请求体使用了不支持的 Content-Encoding
type UnsupportedEncodingError struct {
	Encoding string
}

func (e *UnsupportedEncodingError) Error() string {
	return fmt.Sprintf("unsupported Content-Encoding %q (supported: gzip, zstd)", e.Encoding)
}

// decodeMiddleware 以 DecodeBody 解开请求体，不支持的编码返回 415，正文不是所写编码时返回 400，错误体按请求所属协议
func decodeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := DecodeBody(w, r); err != nil {
			status := http.StatusBadRequest
			if _, unsupported := err.(*UnsupportedEncodingError); unsupported {
				status = http.StatusUnsupportedMediaType
			}
			message := "request body: " + err.Error()
			switch protocolForRequest(r) {
			case "anthropic":
				writeAnthropicError(w, status, "invalid_request_error", message)
			case "gemini":
				writeGeminiError(w, status, "INVALID_ARGUMENT", message)
			default:
				writeOpenAIError(w, status, "invalid_request", message)
			}
			return
		}
		defer r.Body.Close()
		next.ServeHTTP(w, r)
	})
}

// authMiddleware 在配置了 API key 时要求请求携带其中任一 key，并把通过校验的 key 放入请求上下文
func authMiddleware(keys func() []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if configured := keys(); len(configured) > 0 {
			key, ok := MatchedAPIKey(r, configured)
			if !ok {
				writeAuthError(w, r)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), apiKeyContext{}, key))
		}
		next.ServeHTTP(w, r)
	})
}

// requestAPIKey 返回请求通过校验的 API key，未配置 key 时为空
func requestAPIKey(r *http.Request) string {
	key, _ := r.Context().Value(apiKeyContext{}).(string)
	return key
}

// MatchedAPIKey 返回请求头或 URL 中与配置相同的 API key，依次取 X-Goog-API-Key、X-API-Key、Authorization Bearer 与 key 查询参数
func MatchedAPIKey(r *http.Request, keys []string) (string, bool) {
	for _, candidate := range apiKeyCandidates(r) {
		for _, key := range keys {
			if subtle.ConstantTimeCompare([]byte(candidate), []byte(key)) == 1 {
				return key, true
			}
		}
	}
	return "", false
}

// apiKeyCandidates 返回请求头与 URL 中给出的 API key，去掉首尾空白
func apiKeyCandidates(r *http.Request) []string {
	candidates := []string{
		r.Header.Get("X-Goog-API-Key"),
		r.Header.Get("X-API-Key"),
		bearerToken(r.Header.Get("Authorization")),
		r.URL.Query().Get("key"),
	}
	for index, candidate := range candidates {
		candidates[index] = strings.TrimSpace(candidate)
	}
	return candidates
}

// bearerToken 返回 Authorization Bearer 凭据
func bearerToken(authorization string) string {
	scheme, key, ok := strings.Cut(strings.TrimSpace(authorization), " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		return key
	}
	return ""
}
