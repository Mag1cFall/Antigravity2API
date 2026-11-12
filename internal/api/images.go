package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HugoSmits86/nativewebp"
	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"golang.org/x/image/draw"
)

const (
	// maxImageCount 为 OpenAI Images n 的上限
	maxImageCount = 10
	// imageEditMaskInstruction 为紧跟遮罩图片的说明，白色为编辑区域、黑色为保留区域
	imageEditMaskInstruction = "The previous image is an edit mask for the first image: white pixels mark the area to edit and black pixels mark the area to keep. Apply the edit only inside the white area and keep everything else unchanged."
)

// imageAspectRatios 为图像模型接受的宽高比
var imageAspectRatios = []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"}

// openAIImageRequest 为 OpenAI Images 生成与编辑请求
type openAIImageRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n"`
	Size           string `json:"size"`
	Quality        string `json:"quality"`
	ResponseFormat string `json:"response_format"`
	Stream         bool   `json:"stream"`
	// OutputFormat 与 OutputCompression 为输出图片的编码与 JPEG 压缩质量
	OutputFormat      string `json:"output_format"`
	OutputCompression *int   `json:"output_compression"`
	// SafetyIdentifier 与 User 为调用方的终端用户标识，响应回传其中非空的一个
	SafetyIdentifier string `json:"safety_identifier"`
	User             string `json:"user"`
}

// handleOpenAIImages 按提示词生成图片
func (s *server) handleOpenAIImages(w http.ResponseWriter, r *http.Request) {
	var request openAIImageRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.writeOpenAIImages(w, r, request, nil, "image_generation")
}

// handleOpenAIImageEdits 把 multipart 的 image 或 image[] 与 mask 作为参考图片接在提示词之后，按 OpenAI Images 参数生成编辑结果
func (s *server) handleOpenAIImageEdits(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	request := openAIImageRequest{
		Model: r.FormValue("model"), Prompt: r.FormValue("prompt"), Size: r.FormValue("size"),
		Quality: r.FormValue("quality"), ResponseFormat: r.FormValue("response_format"),
		OutputFormat: r.FormValue("output_format"), SafetyIdentifier: r.FormValue("safety_identifier"), User: r.FormValue("user"),
	}
	if value := strings.TrimSpace(r.FormValue("output_compression")); value != "" {
		compression, err := strconv.Atoi(value)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "output_compression must be an integer")
			return
		}
		request.OutputCompression = &compression
	}
	request.Stream, _ = strconv.ParseBool(r.FormValue("stream"))
	if value := strings.TrimSpace(r.FormValue("n")); value != "" {
		count, err := strconv.Atoi(value)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "n must be an integer")
			return
		}
		request.N = count
	}
	var inputs []antigravity.Part
	for _, header := range append(r.MultipartForm.File["image"], r.MultipartForm.File["image[]"]...) {
		data, err := readMultipartFile(header)
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		mimeType := cmp.Or(declaredMediaType(header.Header.Get("Content-Type")), http.DetectContentType(data))
		mimeType, data = normalizeImagePayload(mimeType, data)
		inputs = append(inputs, antigravity.Part{InlineData: &antigravity.Blob{MIME: mimeType, Data: data}})
	}
	if len(inputs) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "image is required")
		return
	}
	if masks := r.MultipartForm.File["mask"]; len(masks) > 0 {
		mask, err := readMultipartFile(masks[0])
		if err == nil {
			mask, err = imageEditMask(mask)
		}
		if err != nil {
			writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		inputs = append(inputs, antigravity.Part{InlineData: &antigravity.Blob{MIME: "image/png", Data: mask}}, antigravity.Part{Text: imageEditMaskInstruction})
	}
	s.writeOpenAIImages(w, r, request, inputs, "image_edit")
}

// writeOpenAIImages 按 n 并发生成图片并写入 OpenAI 图片响应：提示词在前，inputs 为其后的参考图片与说明；未指定模型时取目录中首个图像生成模型，流式时每张图片发送一个 <event>.completed 事件
func (s *server) writeOpenAIImages(w http.ResponseWriter, r *http.Request, request openAIImageRequest, inputs []antigravity.Part, event string) {
	if strings.TrimSpace(request.Prompt) == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "prompt is required")
		return
	}
	request.N = cmp.Or(request.N, 1)
	if request.N < 1 || request.N > maxImageCount {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("n must be between 1 and %d", maxImageCount))
		return
	}
	if request.Model == "" {
		models, err := s.service.Models(r.Context())
		if err != nil {
			writeOpenAIRequestError(w, err)
			return
		}
		request.Model = catalogModel(models, "image_generation")
	}
	config, err := openAIImageConfig(request.Size, request.Quality)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	target, _ := openAIImageSize(request.Size)
	format, compression, err := openAIImageEncoding(request.OutputFormat, request.OutputCompression)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var stream *streamKeepalive
	if request.Stream {
		stream = keepStreamAlive(w, sseHeartbeat)
		defer stream.stop()
	} else {
		var stopKeepalive func()
		w, stopKeepalive = s.nonstreamKeepalive(w)
		defer stopKeepalive()
	}
	// streamed 在流式响应头已由心跳写出时以 SSE 错误事件写出 err 并返回 true
	streamed := func(err error) bool {
		if stream == nil || !stream.stop() {
			return false
		}
		writeOpenAIStreamError(stream, r, err)
		return true
	}
	results, err := s.generateImages(r.Context(), antigravity.GenerateRequest{
		ID:       newID("img"),
		Model:    request.Model,
		Session:  headerSession(r),
		Contents: []antigravity.Content{{Role: antigravity.RoleUser, Parts: append([]antigravity.Part{{Text: request.Prompt}}, inputs...)}},
		Config:   antigravity.GenerationConfig{ResponseModalities: []string{"IMAGE"}, ImageConfig: config},
	}, request.N)
	if err != nil {
		if shouldWriteRequestError(r, err) && !streamed(err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	created := time.Now().Unix()
	output := s.mediaOutputFor(r)
	quality := openAIImageQuality(request.Quality, config)
	identifier := cmp.Or(request.SafetyIdentifier, request.User)
	var data []map[string]any
	var size image.Point
	var outputFormat string
	for _, result := range results {
		if len(result.media) == 0 {
			message := "the model returned no image: finish reason " + result.finishReason
			if !streamed(errors.New(message)) {
				writeOpenAIError(w, http.StatusBadGateway, "upstream_error", message)
			}
			return
		}
		for _, media := range result.media {
			media, size, err = fitImage(media, target, format, compression)
			if err != nil {
				if !streamed(err) {
					writeOpenAIError(w, http.StatusBadGateway, "upstream_error", err.Error())
				}
				return
			}
			outputFormat = strings.TrimPrefix(media.MIME, "image/")
			encoded := base64.StdEncoding.EncodeToString(media.Data)
			item := map[string]any{}
			switch {
			case request.Stream:
				item["type"], item["b64_json"], item["created_at"] = event+".completed", encoded, created
				item["output_format"] = outputFormat
				item["size"], item["quality"], item["background"] = fmt.Sprintf("%dx%d", size.X, size.Y), quality, "opaque"
				item["usage"] = openAIImageUsage(result.usage)
				if identifier != "" {
					item["safety_identifier"] = identifier
				}
			case request.ResponseFormat == "url":
				if err := output.save(media); err != nil {
					if !streamed(err) {
						writeOpenAIRequestError(w, err)
					}
					return
				}
				item["url"] = output.link(media)
			default:
				item["b64_json"] = encoded
			}
			if result.text.Len() > 0 {
				item["revised_prompt"] = result.text.String()
			}
			data = append(data, item)
		}
	}
	if !request.Stream {
		body := map[string]any{
			"created": created, "data": data, "background": "opaque", "output_format": outputFormat,
			"quality": quality, "size": fmt.Sprintf("%dx%d", size.X, size.Y), "usage": openAIImageUsage(choicesUsage(results)),
		}
		if identifier != "" {
			body["safety_identifier"] = identifier
		}
		writeJSON(w, http.StatusOK, officialOrder("openai", body))
		return
	}
	if err := stream.open(); err != nil {
		return
	}
	for _, item := range data {
		if err := writeSSE(stream, item["type"].(string), officialOrder("openai", item)); err != nil {
			return
		}
	}
}

// generateImages 按 count 并发生成并按序返回结果，任一生成失败时返回首个错误
func (s *server) generateImages(ctx context.Context, request antigravity.GenerateRequest, count int) ([]generationResult, error) {
	images := s.startChatChoices(ctx, request, count, false)
	defer images.cancel()
	if err := images.err; err != nil {
		return nil, err
	}
	return images.run(func(_ int, events <-chan antigravity.Event) (generationResult, error) {
		return consumeEvents(images.ctx, events, nil)
	})
}

// catalogModel 返回实时目录中首个带指定能力的模型 ID
func catalogModel(models []antigravity.Model, capability string) string {
	for _, model := range models {
		if model.Capabilities[capability] {
			return model.ID
		}
	}
	return ""
}

// imageResolutions 为图像模型的分辨率档位及其长边像素
var imageResolutions = []struct {
	name   string
	pixels int
}{{"1K", 1024}, {"2K", 2048}, {"4K", 4096}}

// openAIImageConfig 把 OpenAI size 换成最接近的宽高比与能覆盖该尺寸的最小分辨率，quality 换成 1K、2K 或 4K 分辨率，两者取较高的分辨率
func openAIImageConfig(size string, quality string) (*antigravity.ImageConfig, error) {
	config := &antigravity.ImageConfig{}
	target, err := openAIImageSize(size)
	if err != nil {
		return nil, err
	}
	resolution := 0
	if target != (image.Point{}) {
		logRatio := math.Log(float64(target.X) / float64(target.Y))
		best := math.Inf(1)
		for _, ratio := range imageAspectRatios {
			left, right, _ := strings.Cut(ratio, ":")
			numerator, _ := strconv.Atoi(left)
			denominator, _ := strconv.Atoi(right)
			if distance := math.Abs(math.Log(float64(numerator)/float64(denominator)) - logRatio); distance < best {
				best, config.AspectRatio = distance, ratio
			}
		}
		edge := math.Sqrt(float64(target.X) * float64(target.Y))
		for resolution < len(imageResolutions)-1 && float64(imageResolutions[resolution].pixels) < edge {
			resolution++
		}
		config.ImageSize = imageResolutions[resolution].name
	}
	requested := -1
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "", "auto":
	case "low", "standard":
		requested = 0
	case "medium", "hd":
		requested = 1
	case "high", "xhigh", "max":
		requested = 2
	default:
		return nil, fmt.Errorf("quality must be auto, low, medium, high, xhigh, max, standard or hd")
	}
	if requested >= 0 && (config.ImageSize == "" || requested > resolution) {
		config.ImageSize = imageResolutions[requested].name
	}
	if *config == (antigravity.ImageConfig{}) {
		return nil, nil
	}
	return config, nil
}

// openAIImageSize 解析 OpenAI size 的 WIDTHxHEIGHT，auto 或省略时为零
func openAIImageSize(size string) (image.Point, error) {
	size = strings.ToLower(strings.TrimSpace(size))
	if size == "" || size == "auto" {
		return image.Point{}, nil
	}
	widthText, heightText, ok := strings.Cut(size, "x")
	width, widthErr := strconv.Atoi(widthText)
	height, heightErr := strconv.Atoi(heightText)
	if !ok || widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return image.Point{}, fmt.Errorf("size must be auto or WIDTHxHEIGHT")
	}
	return image.Pt(width, height), nil
}

// openAIImageQuality 返回响应中的 quality：请求的 low、medium、high、xhigh、max 原样返回，其余按生成分辨率换为 low、medium 或 high
func openAIImageQuality(quality string, config *antigravity.ImageConfig) string {
	switch quality = strings.ToLower(strings.TrimSpace(quality)); quality {
	case "low", "medium", "high", "xhigh", "max":
		return quality
	}
	if config != nil {
		switch config.ImageSize {
		case "4K":
			return "high"
		case "2K":
			return "medium"
		}
	}
	return "low"
}

// openAIImageEncoding 返回 output_format 对应的 MIME 类型与 output_compression 给出的 JPEG 质量，省略时为 PNG 与 100；webp 为无损编码，不使用质量
func openAIImageEncoding(format string, compression *int) (string, int, error) {
	quality := 100
	if compression != nil {
		if *compression < 0 || *compression > 100 {
			return "", 0, fmt.Errorf("output_compression must be between 0 and 100")
		}
		quality = *compression
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "png":
		return "image/png", quality, nil
	case "jpeg", "jpg":
		return "image/jpeg", quality, nil
	case "webp":
		return "image/webp", quality, nil
	}
	return "", 0, fmt.Errorf("output_format must be png, jpeg or webp")
}

// openAIImageUsage 返回 OpenAI 图片用量，没有用量时各项为 0
func openAIImageUsage(usage *antigravity.Usage) map[string]any {
	usage = cmp.Or(usage, &antigravity.Usage{})
	return map[string]any{"input_tokens": usage.InputTokens, "output_tokens": outputTokens(usage), "total_tokens": usage.TotalTokens}
}

// fitImage 把图片居中裁切缩放为 size 并编码为 mimeType，返回实际尺寸；size 为零或图片任一边小于 size 时保持原尺寸，不放大；mimeType 为空时保持上游编码
func fitImage(media antigravity.Blob, size image.Point, mimeType string, quality int) (antigravity.Blob, image.Point, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(media.Data))
	if err != nil {
		return media, image.Point{}, fmt.Errorf("decode %s output: %w", media.MIME, err)
	}
	bounds := image.Pt(config.Width, config.Height)
	resize := size != (image.Point{}) && bounds != size && bounds.X >= size.X && bounds.Y >= size.Y
	mimeType = cmp.Or(mimeType, media.MIME)
	if !resize && media.MIME == mimeType && (mimeType == "image/png" || quality == 100) {
		return media, bounds, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(media.Data))
	if err != nil {
		return media, image.Point{}, fmt.Errorf("decode %s output: %w", media.MIME, err)
	}
	if resize {
		decoded = coverImage(decoded, size)
	}
	var encoded bytes.Buffer
	switch mimeType {
	case "image/png":
		err = png.Encode(&encoded, decoded)
	case "image/jpeg":
		err = jpeg.Encode(&encoded, decoded, &jpeg.Options{Quality: quality})
	case "image/webp":
		err = nativewebp.Encode(&encoded, decoded, nil)
	default:
		return media, bounds, nil
	}
	if err != nil {
		return media, image.Point{}, fmt.Errorf("convert %s output to %s: %w", media.MIME, mimeType, err)
	}
	return antigravity.Blob{MIME: mimeType, Data: encoded.Bytes()}, decoded.Bounds().Size(), nil
}

// coverImage 居中裁切出与 size 同宽高比的区域并以 CatmullRom 缩放到 size
func coverImage(source image.Image, size image.Point) image.Image {
	crop := source.Bounds()
	if crop.Dx()*size.Y > crop.Dy()*size.X {
		width := crop.Dy() * size.X / size.Y
		crop.Min.X += (crop.Dx() - width) / 2
		crop.Max.X = crop.Min.X + width
	} else {
		height := crop.Dx() * size.Y / size.X
		crop.Min.Y += (crop.Dy() - height) / 2
		crop.Max.Y = crop.Min.Y + height
	}
	scaled := image.NewRGBA(image.Rectangle{Max: size})
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), source, crop, draw.Src, nil)
	return scaled
}

// readMultipartFile 读取 multipart 文件段的全部内容
func readMultipartFile(header *multipart.FileHeader) ([]byte, error) {
	file, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

// imageEditMask 把 OpenAI 遮罩中完全透明的像素换成白色、其余像素换成黑色，返回灰度 PNG
func imageEditMask(data []byte) ([]byte, error) {
	source, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mask must be a PNG image: %w", err)
	}
	bounds := source.Bounds()
	mask := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if _, _, _, alpha := source.At(x, y).RGBA(); alpha == 0 {
				mask.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, mask); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
