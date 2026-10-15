package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

const (
	// remoteMediaUserAgent 是下载外部媒体时声明的客户端标识
	remoteMediaUserAgent = "Antigravity2API (+https://github.com/Mag1cFall/Antigravity2API)"
	// remoteMediaMaxBytes 是单个外部媒体的大小上限
	remoteMediaMaxBytes int64 = 512 << 20
	// geminiFilesURIPrefix 为 Gemini Files API 文件 URI 的前缀
	geminiFilesURIPrefix = "https://generativelanguage.googleapis.com/v1beta/files/"
)

// remoteMediaClient 是下载外部媒体的 HTTP 客户端，每次重定向前检查目标主机
var remoteMediaClient = &http.Client{CheckRedirect: func(request *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return checkRemoteMediaHost(request.Context(), request.URL.Hostname())
}}

// inlineRemoteMedia 把内容与函数结果中的文件引用换成内联媒体：Files API 文件 ID 读取已上传的内容，http(s) URL 下载，YouTube 视频保留为远程引用，同一 URL 只下载一次
func inlineRemoteMedia(ctx context.Context, contents []antigravity.Content) error {
	downloaded := make(map[string]*antigravity.Blob)
	for contentIndex := range contents {
		if err := inlineRemoteParts(ctx, contents[contentIndex].Parts, downloaded); err != nil {
			return err
		}
	}
	return nil
}

// inlineRemoteParts 下载一组 part 中的文件引用
func inlineRemoteParts(ctx context.Context, parts []antigravity.Part, downloaded map[string]*antigravity.Blob) error {
	for partIndex := range parts {
		part := &parts[partIndex]
		if part.FunctionResult != nil {
			if err := inlineRemoteParts(ctx, part.FunctionResult.Parts, downloaded); err != nil {
				return err
			}
		}
		if part.File == nil {
			continue
		}
		if _, ok := youtubeURL(part.File.ID); ok {
			continue
		}
		if !remoteMediaURL(part.File.ID) {
			file, data, err := filesFrom(ctx).read(part.File.ID)
			if err != nil {
				return fmt.Errorf("file %s: %w", part.File.ID, err)
			}
			part.File, part.InlineData = nil, &antigravity.Blob{MIME: file.MIME, Data: data}
			continue
		}
		blob, ok := downloaded[part.File.ID]
		if !ok {
			var err error
			if blob, err = downloadRemoteMedia(ctx, part.File.ID, part.File.MIME); err != nil {
				return fmt.Errorf("Error while downloading %s: %w", part.File.ID, err)
			}
			downloaded[part.File.ID] = blob
		}
		part.File, part.InlineData = nil, blob
	}
	return nil
}

// remoteMediaURL 返回值是否为 http 或 https URL
func remoteMediaURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// checkRemoteMediaHost 拒绝解析到本机、私网、链路本地、组播或未指定地址的主机
func checkRemoteMediaHost(ctx context.Context, host string) error {
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	for _, address := range addresses {
		address = address.Unmap()
		if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsMulticast() || address.IsUnspecified() {
			return fmt.Errorf("host %s resolves to non-public address %s", host, address)
		}
	}
	return nil
}

// downloadRemoteMedia 在请求 context 内下载外部媒体，MIME 依次取响应 Content-Type、声明值与内容判断
func downloadRemoteMedia(ctx context.Context, url string, declared string) (*antigravity.Blob, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if err := checkRemoteMediaHost(ctx, request.URL.Hostname()); err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", remoteMediaUserAgent)
	response, err := remoteMediaClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	if response.ContentLength > remoteMediaMaxBytes {
		return nil, fmt.Errorf("content exceeds %d bytes", remoteMediaMaxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, remoteMediaMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > remoteMediaMaxBytes {
		return nil, fmt.Errorf("content exceeds %d bytes", remoteMediaMaxBytes)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("response body is empty")
	}
	mimeType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mimeType == "application/octet-stream" || mimeType == "binary/octet-stream" {
		mimeType = declared
		if mimeType == "" {
			mimeType = detectMediaType("", data)
		}
	}
	mimeType, data = normalizeImagePayload(mimeType, data)
	return &antigravity.Blob{MIME: mimeType, Data: data}, nil
}
