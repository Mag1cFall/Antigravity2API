package api

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// maxPublicBodyBytes 为公开接口请求体上限
const maxPublicBodyBytes int64 = 512<<20/3*4 + 1<<20

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

// authMiddleware 在配置了 API key 时要求请求携带其中任一 key，并把通过校验的 key 放入请求上下文
func authMiddleware(keys func() []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if configured := keys(); len(configured) > 0 {
			key, ok := matchedAPIKey(r, configured)
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

// matchedAPIKey 返回请求头或 URL 中与配置相同的 API key
func matchedAPIKey(r *http.Request, keys []string) (string, bool) {
	for _, candidate := range apiKeyCandidates(r) {
		if candidate == "" {
			continue
		}
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
		return strings.TrimSpace(key)
	}
	return ""
}
