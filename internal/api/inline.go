package api

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// audioExtensionTypes 为音频文件扩展名对应的 MIME
var audioExtensionTypes = map[string]string{
	".flac": "audio/flac", ".m4a": "audio/mp4", ".mp3": "audio/mpeg", ".mp4": "video/mp4", ".mpeg": "audio/mpeg",
	".mpga": "audio/mpeg", ".ogg": "audio/ogg", ".wav": "audio/wav", ".webm": "audio/webm",
}

// dataURLPart 把 data URL 解析为内联媒体
func dataURLPart(value string, name string) (antigravity.Part, error) {
	if !strings.HasPrefix(value, "data:") {
		return antigravity.Part{}, fmt.Errorf("media URL must be a data URL")
	}
	metadata, encoded, ok := strings.Cut(strings.TrimPrefix(value, "data:"), ",")
	if !ok {
		return antigravity.Part{}, fmt.Errorf("data URL must contain a comma")
	}
	var data []byte
	if mediaType, isBase64 := strings.CutSuffix(metadata, ";base64"); isBase64 {
		decoded, err := decodeBase64Flexible(encoded)
		if err != nil {
			return antigravity.Part{}, fmt.Errorf("data URL: %w", err)
		}
		metadata, data = mediaType, decoded
	} else {
		decoded, err := url.PathUnescape(encoded)
		if err != nil {
			return antigravity.Part{}, fmt.Errorf("data URL: %w", err)
		}
		data = []byte(decoded)
	}
	mimeType, _, err := mime.ParseMediaType(metadata)
	if err != nil {
		mimeType = detectMediaType(name, data)
	}
	mimeType, data = normalizeImagePayload(mimeType, data)
	return antigravity.Part{InlineData: &antigravity.Blob{MIME: mimeType, Data: data}}, nil
}

// mediaURLPart 把 YouTube 视频记为远程视频引用，其他 http(s) URL 记为发送前下载的文件引用，其余按 data URL 解析为内联媒体
func mediaURLPart(value, name, mimeType string) (antigravity.Part, error) {
	if video, ok := youtubeURL(value); ok {
		return antigravity.Part{File: &antigravity.FileRef{ID: video, Name: name, MIME: "video/*"}}, nil
	}
	if remoteMediaURL(value) {
		return antigravity.Part{File: &antigravity.FileRef{ID: value, Name: name, MIME: mimeType}}, nil
	}
	return dataURLPart(value, name)
}

// youtubeURL 把 YouTube 视频链接规范为 watch 地址
func youtubeURL(value string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	var video string
	switch strings.ToLower(parsed.Hostname()) {
	case "youtu.be":
		video = segments[0]
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		switch {
		case segments[0] == "watch":
			video = parsed.Query().Get("v")
		case len(segments) >= 2 && (segments[0] == "shorts" || segments[0] == "live" || segments[0] == "embed"):
			video = segments[1]
		}
	}
	if video == "" {
		return "", false
	}
	return "https://www.youtube.com/watch?v=" + url.QueryEscape(video), true
}

// fileDataPart 解析 data URL 或 base64 文件内容
func fileDataPart(value string, name string) (antigravity.Part, error) {
	if strings.HasPrefix(value, "data:") {
		return dataURLPart(value, name)
	}
	data, err := decodeBase64Flexible(value)
	if err != nil {
		return antigravity.Part{}, fmt.Errorf("file_data: %w", err)
	}
	mimeType, data := normalizeImagePayload(detectMediaType(name, data), data)
	return antigravity.Part{InlineData: &antigravity.Blob{MIME: mimeType, Data: data}}, nil
}

// detectMediaType 按内容前缀判断不带参数的 MIME，无法识别时依次按 FLAC 头、MPEG 音频帧头与扩展名判断
func detectMediaType(name string, data []byte) string {
	detected, _, _ := mime.ParseMediaType(http.DetectContentType(data))
	switch {
	case detected == "application/ogg":
		return "audio/ogg"
	case detected == "audio/wave":
		return "audio/wav"
	case detected != "application/octet-stream":
		return detected
	case bytes.HasPrefix(data, []byte("fLaC")):
		return "audio/flac"
	case len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0 && data[1]&0x06 != 0:
		return "audio/mpeg"
	}
	extension := strings.ToLower(path.Ext(name))
	if mediaType := audioExtensionTypes[extension]; mediaType != "" {
		return mediaType
	}
	if mediaType, _, err := mime.ParseMediaType(mime.TypeByExtension(extension)); err == nil {
		return mediaType
	}
	return detected
}

// audioMIME 返回 input_audio 格式名对应的 MIME
func audioMIME(format string) string {
	switch strings.ToLower(format) {
	case "mp3", "mpeg":
		return "audio/mpeg"
	case "wav":
		return "audio/wav"
	default:
		return "audio/" + strings.ToLower(format)
	}
}

// decodeBase64Flexible 根据字母表和填充形式解码 Base64 与 Data URL
func decodeBase64Flexible(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, ","); idx != -1 && strings.HasPrefix(s, "data:") {
		s = s[idx+1:]
	}
	encoding := base64.StdEncoding
	if index := strings.IndexAny(s, "+/-_"); index >= 0 && (s[index] == '-' || s[index] == '_') {
		encoding = base64.URLEncoding
	}
	if !strings.HasSuffix(s, "=") {
		encoding = encoding.WithPadding(base64.NoPadding)
	}
	return encoding.DecodeString(s)
}

// normalizeImagePayload 将 GIF 首帧按逻辑画布转换为 PNG 图片
func normalizeImagePayload(mimeType string, data []byte) (string, []byte) {
	lowerMIME := strings.ToLower(strings.TrimSpace(mimeType))
	if lowerMIME == "image/gif" || (len(data) >= 3 && string(data[:3]) == "GIF") {
		if img, err := gif.Decode(bytes.NewReader(data)); err == nil {
			config, err := gif.DecodeConfig(bytes.NewReader(data))
			if err != nil {
				return mimeType, data
			}
			canvas := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
			transparent := false
			for _, entry := range img.(*image.Paletted).Palette {
				_, _, _, alpha := entry.RGBA()
				transparent = transparent || alpha == 0
			}
			// GIF 背景色来自全局色表，透明首帧保留透明画布
			if palette, ok := config.ColorModel.(color.Palette); ok && !transparent && int(data[11]) < len(palette) {
				draw.Draw(canvas, canvas.Bounds(), image.NewUniform(palette[data[11]]), image.Point{}, draw.Src)
			}
			draw.Draw(canvas, img.Bounds(), img, img.Bounds().Min, draw.Over)
			var buf bytes.Buffer
			if err := png.Encode(&buf, canvas); err == nil {
				return "image/png", buf.Bytes()
			}
		}
	}
	return mimeType, data
}
