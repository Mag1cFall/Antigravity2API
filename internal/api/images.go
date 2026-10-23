package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
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
		mask, err := imageEditMask(masks[0])
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
	results, err := s.generateImages(r.Context(), antigravity.GenerateRequest{
		ID:       newID("img"),
		Model:    request.Model,
		Session:  headerSession(r),
		Contents: []antigravity.Content{{Role: antigravity.RoleUser, Parts: append([]antigravity.Part{{Text: request.Prompt}}, inputs...)}},
		Config:   antigravity.GenerationConfig{ResponseModalities: []string{"IMAGE"}, ImageConfig: config},
	}, request.N)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	created := time.Now().Unix()
	output := s.mediaOutputFor(r)
	var data []map[string]any
	for _, result := range results {
		if len(result.media) == 0 {
			writeOpenAIError(w, http.StatusBadGateway, "upstream_error", "the model returned no image: finish reason "+result.finishReason)
			return
		}
		for _, media := range result.media {
			encoded := base64.StdEncoding.EncodeToString(media.Data)
			item := map[string]any{}
			switch {
			case request.Stream:
				item["type"], item["b64_json"], item["created_at"] = event+".completed", encoded, created
				item["output_format"] = strings.TrimPrefix(media.MIME, "image/")
				item["size"], item["quality"], item["background"] = cmp.Or(request.Size, "auto"), cmp.Or(request.Quality, "auto"), "auto"
			case request.ResponseFormat == "url":
				if err := output.save(media); err != nil {
					writeOpenAIRequestError(w, err)
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
		writeJSON(w, http.StatusOK, map[string]any{"created": created, "data": data})
		return
	}
	if err := streamHeaders(w); err != nil {
		return
	}
	for _, item := range data {
		if err := writeSSE(w, item["type"].(string), item); err != nil {
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

// openAIImageConfig 把 OpenAI size 换成最接近的宽高比，quality 换成 1K、2K 或 4K 分辨率
func openAIImageConfig(size string, quality string) (*antigravity.ImageConfig, error) {
	config := &antigravity.ImageConfig{}
	if size = strings.ToLower(strings.TrimSpace(size)); size != "" && size != "auto" {
		widthText, heightText, _ := strings.Cut(size, "x")
		width, widthErr := strconv.Atoi(widthText)
		height, heightErr := strconv.Atoi(heightText)
		if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
			return nil, fmt.Errorf("size must be auto or WIDTHxHEIGHT")
		}
		target := math.Log(float64(width) / float64(height))
		best := math.Inf(1)
		for _, ratio := range imageAspectRatios {
			left, right, _ := strings.Cut(ratio, ":")
			numerator, _ := strconv.Atoi(left)
			denominator, _ := strconv.Atoi(right)
			if distance := math.Abs(math.Log(float64(numerator)/float64(denominator)) - target); distance < best {
				best, config.AspectRatio = distance, ratio
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(quality)) {
	case "", "auto":
	case "low", "standard":
		config.ImageSize = "1K"
	case "medium", "hd":
		config.ImageSize = "2K"
	case "high":
		config.ImageSize = "4K"
	default:
		return nil, fmt.Errorf("quality must be auto, low, medium, high, standard or hd")
	}
	if *config == (antigravity.ImageConfig{}) {
		return nil, nil
	}
	return config, nil
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
func imageEditMask(header *multipart.FileHeader) ([]byte, error) {
	data, err := readMultipartFile(header)
	if err != nil {
		return nil, err
	}
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
