package api

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// geminiRequest 为 generateContent、streamGenerateContent 与 countTokens 请求中服务使用的字段
type geminiRequest struct {
	Contents          []geminiContent             `json:"contents"`
	SystemInstruction *geminiContent              `json:"systemInstruction"`
	GenerationConfig  geminiGenerationConfig      `json:"generationConfig"`
	Tools             []geminiToolGroup           `json:"tools"`
	ToolConfig        geminiToolConfig            `json:"toolConfig"`
	SafetySettings    []antigravity.SafetySetting `json:"safetySettings"`
	CachedContent     string                      `json:"cachedContent"`
	// GenerateContentRequest 为 countTokens 携带的完整生成请求
	GenerateContentRequest *geminiRequest `json:"generateContentRequest"`
}

// geminiContent 为一条内容
type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

// geminiBlobPart 为 inlineData，mimeType 接受驼峰与下划线两种字段名
type geminiBlobPart struct {
	MIMEType      string `json:"mimeType"`
	MIMETypeSnake string `json:"mime_type"`
	Data          string `json:"data"`
}

// MIME 返回 inlineData 的媒体类型
func (b *geminiBlobPart) MIME() string {
	if b.MIMEType != "" {
		return b.MIMEType
	}
	return b.MIMETypeSnake
}

// geminiPart 为内容中的一个 part，inlineData 与 fileData 接受驼峰与下划线两种字段名
type geminiPart struct {
	Text             *string         `json:"text"`
	Thought          bool            `json:"thought"`
	ThoughtSignature string          `json:"thoughtSignature"`
	InlineData       *geminiBlobPart `json:"inlineData"`
	InlineDataSnake  *geminiBlobPart `json:"inline_data"`
	FileData         json.RawMessage `json:"fileData"`
	FileDataSnake    json.RawMessage `json:"file_data"`
	FunctionCall     *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
		Parts    []geminiPart    `json:"parts"`
	} `json:"functionResponse"`
	VideoMetadata   json.RawMessage `json:"videoMetadata"`
	MediaResolution json.RawMessage `json:"mediaResolution"`
}

// inline 返回 part 携带的 inlineData
func (p geminiPart) inline() *geminiBlobPart {
	if p.InlineData != nil {
		return p.InlineData
	}
	return p.InlineDataSnake
}

// hasFile 判断 part 是否携带 fileData
func (p geminiPart) hasFile() bool {
	return rawJSONConfigured(p.FileData) || rawJSONConfigured(p.FileDataSnake)
}

// geminiGenerationConfig 为 generationConfig 中服务使用的字段
type geminiGenerationConfig struct {
	Temperature        *float64        `json:"temperature"`
	TopP               *float64        `json:"topP"`
	TopK               *int            `json:"topK"`
	CandidateCount     *int64          `json:"candidateCount"`
	MaxOutputTokens    *int64          `json:"maxOutputTokens"`
	StopSequences      []string        `json:"stopSequences"`
	ResponseMIMEType   string          `json:"responseMimeType"`
	ResponseSchema     json.RawMessage `json:"responseSchema"`
	ResponseJSONSchema json.RawMessage `json:"responseJsonSchema"`
	ResponseModalities []string        `json:"responseModalities"`
	ImageConfig        *struct {
		AspectRatio string `json:"aspectRatio"`
		ImageSize   string `json:"imageSize"`
	} `json:"imageConfig"`
	ResponseFormat   geminiResponseFormats `json:"responseFormat"`
	SpeechConfig     json.RawMessage       `json:"speechConfig"`
	MediaResolution  string                `json:"mediaResolution"`
	PresencePenalty  *float64              `json:"presencePenalty"`
	FrequencyPenalty *float64              `json:"frequencyPenalty"`
	Seed             *int64                `json:"seed"`
	ThinkingConfig   *struct {
		ThinkingBudget  *int64 `json:"thinkingBudget"`
		ThinkingLevel   string `json:"thinkingLevel"`
		IncludeThoughts bool   `json:"includeThoughts"`
	} `json:"thinkingConfig"`
}

// geminiResponseFormat 为 generationConfig.responseFormat 的一项：文本的 MIME 与 Schema、图片的宽高比与尺寸、音频输出
type geminiResponseFormat struct {
	Text *struct {
		MIMEType string          `json:"mimeType"`
		Schema   json.RawMessage `json:"schema"`
	} `json:"text"`
	Image *struct {
		AspectRatio string `json:"aspectRatio"`
		ImageSize   string `json:"imageSize"`
		// Delivery 为 URI 时生成的图片存入 Files API，响应以 fileData 引用
		Delivery string `json:"delivery"`
	} `json:"image"`
	Audio json.RawMessage `json:"audio"`
}

// imageURIDelivery 判断 responseFormat 是否要求以 Files API 文件 URI 返回生成的图片
func (formats geminiResponseFormats) imageURIDelivery() bool {
	return slices.ContainsFunc(formats, func(format geminiResponseFormat) bool {
		return format.Image != nil && strings.EqualFold(format.Image.Delivery, "URI")
	})
}

// geminiResponseFormats 为 responseFormat，REST 写法为单个对象，SDK 写法为数组
type geminiResponseFormats []geminiResponseFormat

// UnmarshalJSON 按单个对象或数组读取 responseFormat
func (formats *geminiResponseFormats) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '{' {
		var format geminiResponseFormat
		if err := json.Unmarshal(trimmed, &format); err != nil {
			return err
		}
		*formats = geminiResponseFormats{format}
		return nil
	}
	return json.Unmarshal(data, (*[]geminiResponseFormat)(formats))
}

// geminiResponseFormatValues 为 responseFormat 枚举名对应的上游取值
var geminiResponseFormatValues = map[string]string{
	"APPLICATION_JSON":                "application/json",
	"TEXT_PLAIN":                      "text/plain",
	"ASPECT_RATIO_ONE_BY_ONE":         "1:1",
	"ASPECT_RATIO_TWO_BY_THREE":       "2:3",
	"ASPECT_RATIO_THREE_BY_TWO":       "3:2",
	"ASPECT_RATIO_THREE_BY_FOUR":      "3:4",
	"ASPECT_RATIO_FOUR_BY_THREE":      "4:3",
	"ASPECT_RATIO_FOUR_BY_FIVE":       "4:5",
	"ASPECT_RATIO_FIVE_BY_FOUR":       "5:4",
	"ASPECT_RATIO_NINE_BY_SIXTEEN":    "9:16",
	"ASPECT_RATIO_SIXTEEN_BY_NINE":    "16:9",
	"ASPECT_RATIO_TWENTY_ONE_BY_NINE": "21:9",
	"ASPECT_RATIO_ONE_BY_EIGHT":       "1:8",
	"ASPECT_RATIO_EIGHT_BY_ONE":       "8:1",
	"ASPECT_RATIO_ONE_BY_FOUR":        "1:4",
	"ASPECT_RATIO_FOUR_BY_ONE":        "4:1",
	"IMAGE_SIZE_FIVE_TWELVE":          "512",
	"IMAGE_SIZE_ONE_K":                "1K",
	"IMAGE_SIZE_TWO_K":                "2K",
	"IMAGE_SIZE_FOUR_K":               "4K",
}

// geminiResponseFormatValue 把 responseFormat 枚举名换成上游取值，其他值原样返回
func geminiResponseFormatValue(value string) string {
	if mapped, ok := geminiResponseFormatValues[value]; ok {
		return mapped
	}
	return value
}

// geminiToolGroup 为 tools 的一项：函数声明与 Google 搜索
type geminiToolGroup struct {
	FunctionDeclarations []struct {
		Name                 string          `json:"name"`
		Description          string          `json:"description"`
		Parameters           json.RawMessage `json:"parameters"`
		ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema"`
		Response             json.RawMessage `json:"response"`
		ResponseJSONSchema   json.RawMessage `json:"responseJsonSchema"`
		Behavior             string          `json:"behavior"`
	} `json:"functionDeclarations"`
	GoogleSearch          json.RawMessage `json:"googleSearch"`
	GoogleSearchRetrieval json.RawMessage `json:"googleSearchRetrieval"`
}

// geminiToolConfig 为 toolConfig 的函数调用配置
type geminiToolConfig struct {
	FunctionCallingConfig struct {
		Mode                 string   `json:"mode"`
		AllowedFunctionNames []string `json:"allowedFunctionNames"`
	} `json:"functionCallingConfig"`
}

// handleGeminiModels 分页列出实时目录中的模型
func (s *server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	page, next, err := geminiPage(r.URL.Query(), models, 50, 1000)
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	data := make([]map[string]any, 0, len(page))
	for _, model := range page {
		data = append(data, geminiModelObject(model))
	}
	response := map[string]any{"models": data}
	if next != "" {
		response["nextPageToken"] = next
	}
	writeJSON(w, http.StatusOK, officialOrder("gemini", response))
}

// geminiPage 按 pageSize 与 pageToken 返回列表的一页与下一页令牌，pageSize 为 0 时取 defaultSize、最多 maxSize，pageToken 为下一页的起始位置
func geminiPage[T any](query url.Values, items []T, defaultSize int, maxSize int) ([]T, string, error) {
	size, offset := defaultSize, 0
	if value := firstQueryValue(query, "pageSize", "page_size"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return nil, "", fmt.Errorf("invalid pageSize %q", value)
		}
		if parsed > 0 {
			size = min(parsed, maxSize)
		}
	}
	if token := firstQueryValue(query, "pageToken", "page_token"); token != "" {
		parsed, err := strconv.Atoi(token)
		if err != nil || parsed < 0 || parsed > len(items) {
			return nil, "", fmt.Errorf("invalid pageToken %q", token)
		}
		offset = parsed
	}
	end := min(offset+size, len(items))
	if end < len(items) {
		return items[offset:end], strconv.Itoa(end), nil
	}
	return items[offset:end], "", nil
}

// handleGeminiModel 返回单个模型，目录中没有时为 404
func (s *server) handleGeminiModel(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("model")
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	if model, ok := lookupPublicModel(models, modelID); ok {
		writeJSON(w, http.StatusOK, officialOrder("gemini", geminiModelObject(model)))
		return
	}
	writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("model %q is unavailable", modelID))
}

// geminiModelObject 把目录模型投影为 Gemini Model 资源
func geminiModelObject(model antigravity.Model) map[string]any {
	item := map[string]any{
		"name":                       "models/" + model.ID,
		"baseModelId":                model.ID,
		"displayName":                model.Name,
		"description":                model.Description,
		"supportedGenerationMethods": model.Methods,
		"inputTokenLimit":            model.InputTokenLimit,
		"outputTokenLimit":           model.OutputTokenLimit,
	}
	if len(model.Capabilities) > 0 {
		item["capabilities"] = model.Capabilities
	}
	if model.Capabilities["thinking"] {
		item["thinking"] = true
	}
	if len(model.ThinkingLevels) > 0 {
		item["thinkingLevels"], item["defaultThinkingLevel"] = model.ThinkingLevels, model.DefaultThinkingLevel
	}
	return item
}

// handleGeminiAction 处理 models/{model}:{method} 的 generateContent、streamGenerateContent 与 countTokens
func (s *server) handleGeminiAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	separator := strings.LastIndex(action, ":")
	if separator < 1 {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "expected models/{model}:{method}")
		return
	}
	model := strings.TrimPrefix(action[:separator], "models/")
	method := action[separator+1:]
	var request geminiRequest
	if err := decodeGeminiJSON(r, &request); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if method == "countTokens" && request.GenerateContentRequest != nil {
		request = *request.GenerateContentRequest
	}
	generateRequest, err := request.toGenerateRequest(newID("resp"), model)
	if err == nil {
		err = inlineRemoteMedia(r.Context(), generateRequest.Contents)
	}
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	generateRequest.Session = clientSession(r)
	switch method {
	case "countTokens", "generateContent", "streamGenerateContent":
	default:
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "unknown method: "+method)
		return
	}
	cache, ok := s.applyGeminiCache(w, request.CachedContent, &generateRequest)
	if !ok {
		return
	}
	if method == "countTokens" {
		tokens, err := s.service.CountTokens(r.Context(), generateRequest)
		if err != nil {
			writeGeminiRequestError(w, err)
			return
		}
		response := map[string]any{"totalTokens": tokens}
		if cache != nil {
			response["cachedContentTokenCount"] = cache.tokens
		}
		writeJSON(w, http.StatusOK, officialOrder("gemini", response))
		return
	}
	s.handleGeminiGenerate(w, r, generateRequest, method == "streamGenerateContent", request.GenerationConfig.candidates(), geminiOutput{imageURI: request.GenerationConfig.ResponseFormat.imageURIDelivery(), cache: cache})
}

// decodeGeminiJSON 读取 Gemini 请求体，按 proto3 JSON 规则把字段名转为 lowerCamelCase 后解码到 target
func decodeGeminiJSON(r *http.Request, target any) error {
	body, err := io.ReadAll(r.Body)
	if err == nil {
		body, err = geminiCamelKeys(body)
	}
	if err == nil {
		err = json.Unmarshal(body, target)
	}
	return err
}

// geminiOutput 是 Gemini 生成响应的输出选项：imageURI 时生成的图片存入 Files API 并以 fileData 返回，cache 为请求引用的显式缓存
type geminiOutput struct {
	imageURI bool
	cache    *geminiCache
}

// geminiOpaqueFields 为取值是自由 JSON 的字段，其中的键名属于用户数据
var geminiOpaqueFields = map[string]bool{
	"args": true, "response": true, "parameters": true, "parametersJsonSchema": true,
	"responseSchema": true, "responseJsonSchema": true, "partMetadata": true, "schema": true,
}

// geminiCamelKeys 按 proto3 JSON 规则把各层消息字段名转为 lowerCamelCase，同层两种写法并存时保留驼峰字段，自由 JSON 字段的取值原样保留
func geminiCamelKeys(raw []byte) ([]byte, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '{' && value[0] != '[' {
		return raw, nil
	}
	if camel, err := geminiKeysCamel(value); err == nil && camel {
		return raw, nil
	}
	output := bytes.NewBuffer(make([]byte, 0, len(value)))
	if err := writeGeminiCamelKeys(output, value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// writeGeminiCamelKeys 把 JSON 对象或数组 value 中各层消息字段名转为 lowerCamelCase 后写入 output，字段取值按原字节写出，根值之后还有其他内容时返回错误
func writeGeminiCamelKeys(output *bytes.Buffer, value []byte) error {
	decoder := jsontext.NewDecoder(bytes.NewBuffer(value), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(true))
	// end 读取根值的结束括号，确认其后只有空白
	end := func() error {
		if _, err := decoder.ReadToken(); err != nil {
			return err
		}
		if _, err := decoder.ReadToken(); err != io.EOF {
			return cmp.Or(err, errors.New("unexpected data after top-level value"))
		}
		return nil
	}
	// next 读取下一个值，返回它在 value 中的原字节
	next := func() ([]byte, error) {
		item, err := decoder.ReadValue()
		end := decoder.InputOffset()
		return value[end-int64(len(item)) : end], err
	}
	write := func(item []byte, opaque bool) error {
		if opaque || item[0] != '{' && item[0] != '[' {
			output.Write(item)
			return nil
		}
		return writeGeminiCamelKeys(output, item)
	}
	open, err := decoder.ReadToken()
	if err != nil {
		return err
	}
	if open.Kind() == '[' {
		output.WriteByte('[')
		for index := 0; decoder.PeekKind() != ']'; index++ {
			item, err := next()
			if err != nil {
				return err
			}
			if index > 0 {
				output.WriteByte(',')
			}
			if err := write(item, false); err != nil {
				return err
			}
		}
		output.WriteByte(']')
		return end()
	}
	type member struct {
		key   string
		value []byte
	}
	var members []member
	keys := make(map[string]bool)
	for decoder.PeekKind() != '}' {
		token, err := decoder.ReadToken()
		if err != nil {
			return err
		}
		key := token.String()
		item, err := next()
		if err != nil {
			return err
		}
		members = append(members, member{key: key, value: item})
		keys[key] = true
	}
	output.WriteByte('{')
	written := 0
	for _, field := range members {
		name := geminiJSONName(field.key)
		if name != field.key && keys[name] {
			continue
		}
		if written > 0 {
			output.WriteByte(',')
		}
		written++
		quoted, err := jsontext.AppendQuote(output.AvailableBuffer(), name)
		if err != nil {
			return err
		}
		output.Write(quoted)
		output.WriteByte(':')
		if err := write(field.value, geminiOpaqueFields[name]); err != nil {
			return err
		}
	}
	output.WriteByte('}')
	return end()
}

// geminiKeysCamel 逐个读取 JSON token，判断各层消息字段名是否都已是 lowerCamelCase，不读取自由 JSON 字段的取值
func geminiKeysCamel(raw []byte) (bool, error) {
	decoder := jsontext.NewDecoder(bytes.NewBuffer(raw))
	for {
		kind, length := decoder.StackIndex(decoder.StackDepth())
		token, err := decoder.ReadToken()
		if err == io.EOF {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		if kind != '{' || length%2 != 0 || token.Kind() != '"' {
			continue
		}
		name := token.String()
		if geminiJSONName(name) != name {
			return false, nil
		}
		if geminiOpaqueFields[name] {
			if err := decoder.SkipValue(); err != nil {
				return false, err
			}
		}
	}
}

// geminiJSONName 返回 proto 字段名对应的 JSON 名
func geminiJSONName(name string) string {
	if !strings.Contains(name, "_") {
		return name
	}
	var builder strings.Builder
	upper := false
	for _, char := range name {
		if char == '_' {
			upper = true
			continue
		}
		if upper {
			char, upper = unicode.ToUpper(char), false
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

// toGenerateRequest 把 Gemini 请求转换为生成请求，systemInstruction 中的媒体移到首个 user 消息开头
func (request geminiRequest) toGenerateRequest(id string, model string) (antigravity.GenerateRequest, error) {
	if err := request.GenerationConfig.validate(); err != nil {
		return antigravity.GenerateRequest{}, err
	}
	var system string
	var systemMedia []antigravity.Part
	if request.SystemInstruction != nil {
		parts, _, err := mapGeminiParts(request.SystemInstruction.Parts)
		if err != nil {
			return antigravity.GenerateRequest{}, fmt.Errorf("systemInstruction: %w", err)
		}
		var text strings.Builder
		for _, part := range parts {
			if part.FunctionCall != nil || part.FunctionResult != nil {
				text.WriteString(geminiToolTranscript(part))
				continue
			}
			if part.Text == "" && (part.InlineData != nil || part.File != nil) {
				systemMedia = append(systemMedia, part)
			}
			if !part.Thought {
				text.WriteString(part.Text)
			}
		}
		system = text.String()
	}
	contents := make([]antigravity.Content, 0, len(request.Contents))
	for _, content := range request.Contents {
		parts, hasResult, err := mapGeminiParts(content.Parts)
		if err != nil {
			return antigravity.GenerateRequest{}, err
		}
		if len(parts) == 0 {
			continue
		}
		role, err := geminiRole(content.Role)
		if err != nil {
			return antigravity.GenerateRequest{}, err
		}
		if hasResult {
			role = antigravity.RoleTool
		}
		contents = append(contents, antigravity.Content{Role: role, Parts: parts})
	}
	contents = prependSystemMedia(contents, systemMedia)
	tools, err := mapGeminiTools(request.Tools, request.ToolConfig)
	if err != nil {
		return antigravity.GenerateRequest{}, err
	}
	config := antigravity.GenerationConfig{
		Temperature:        request.GenerationConfig.Temperature,
		TopP:               request.GenerationConfig.TopP,
		TopK:               request.GenerationConfig.TopK,
		PresencePenalty:    request.GenerationConfig.PresencePenalty,
		FrequencyPenalty:   request.GenerationConfig.FrequencyPenalty,
		MaxOutputTokens:    request.GenerationConfig.MaxOutputTokens,
		StopSequences:      normalizeStopSequences(request.GenerationConfig.StopSequences),
		ResponseMIMEType:   request.GenerationConfig.ResponseMIMEType,
		ResponseSchema:     request.GenerationConfig.ResponseSchema,
		Seed:               request.GenerationConfig.Seed,
		SafetySettings:     geminiSafetySettings(request.SafetySettings),
		ResponseModalities: request.GenerationConfig.ResponseModalities,
	}
	if image := request.GenerationConfig.ImageConfig; image != nil {
		config.ImageConfig = &antigravity.ImageConfig{AspectRatio: image.AspectRatio, ImageSize: image.ImageSize}
	}
	if resolution := request.GenerationConfig.MediaResolution; !strings.HasSuffix(resolution, "_UNSPECIFIED") {
		config.MediaResolution = resolution
	}
	if len(request.GenerationConfig.ResponseJSONSchema) > 0 {
		config.ResponseSchema = request.GenerationConfig.ResponseJSONSchema
	}
	for _, format := range request.GenerationConfig.ResponseFormat {
		if text := format.Text; text != nil {
			config.ResponseMIMEType = geminiResponseFormatValue(text.MIMEType)
			if rawJSONConfigured(text.Schema) {
				config.ResponseSchema = text.Schema
			}
		}
		if image := format.Image; image != nil {
			config.ImageConfig = &antigravity.ImageConfig{AspectRatio: geminiResponseFormatValue(image.AspectRatio), ImageSize: geminiResponseFormatValue(image.ImageSize)}
		}
	}
	config.HideThinking = true
	if request.GenerationConfig.ThinkingConfig != nil {
		config.ThinkingBudget = request.GenerationConfig.ThinkingConfig.ThinkingBudget
		config.ReasoningEffort = request.GenerationConfig.ThinkingConfig.ThinkingLevel
		config.HideThinking = !request.GenerationConfig.ThinkingConfig.IncludeThoughts
	}
	return antigravity.GenerateRequest{
		ID: id, Model: model, System: system, Contents: contents, Config: config, Tools: tools,
	}, nil
}

// geminiToolTranscript 把系统指令中的 functionCall 或 functionResponse part 写成 Gemini JSON 文本
func geminiToolTranscript(part antigravity.Part) string {
	value := map[string]any{}
	if call := part.FunctionCall; call != nil {
		value["functionCall"] = map[string]any{"name": call.Name, "args": rawJSONValue(call.Arguments, map[string]any{})}
	} else {
		value["functionResponse"] = map[string]any{"name": part.FunctionResult.Name, "response": rawJSONValue(part.FunctionResult.Content, map[string]any{})}
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// geminiMaxCandidates 是 candidateCount 的上限
const geminiMaxCandidates = 8

// validate 校验候选数与输出模态：目录中没有输出音频或语音的模型，图片输出由所选模型是否生成图片决定
func (config geminiGenerationConfig) validate() error {
	if config.CandidateCount != nil && (*config.CandidateCount < 0 || *config.CandidateCount > geminiMaxCandidates) {
		return fmt.Errorf("generationConfig.candidateCount must be between 1 and %d", geminiMaxCandidates)
	}
	audioFormat := slices.ContainsFunc(config.ResponseFormat, func(format geminiResponseFormat) bool { return rawJSONConfigured(format.Audio) })
	if audioFormat || rawJSONConfigured(config.SpeechConfig) || slices.ContainsFunc(config.ResponseModalities, func(modality string) bool { return strings.EqualFold(modality, "AUDIO") }) {
		return fmt.Errorf("audio output is not supported: no model in the catalog generates audio")
	}
	return nil
}

// geminiCurrentHarmCategories 为上游接受的安全类别
var geminiCurrentHarmCategories = []string{"HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_HATE_SPEECH", "HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_DANGEROUS_CONTENT", "HARM_CATEGORY_CIVIC_INTEGRITY"}

// geminiSafetySettings 保留当前安全类别的设置，旧类别与未指定的类别、阈值视为缺省
func geminiSafetySettings(settings []antigravity.SafetySetting) []antigravity.SafetySetting {
	var kept []antigravity.SafetySetting
	for _, setting := range settings {
		setting.Category, setting.Threshold = strings.ToUpper(setting.Category), strings.ToUpper(setting.Threshold)
		if slices.Contains(geminiCurrentHarmCategories, setting.Category) && setting.Threshold != "" && setting.Threshold != "HARM_BLOCK_THRESHOLD_UNSPECIFIED" {
			kept = append(kept, setting)
		}
	}
	return kept
}

// candidates 返回请求的候选数，省略或为 0 时为 1
func (config geminiGenerationConfig) candidates() int {
	if config.CandidateCount == nil || *config.CandidateCount == 0 {
		return 1
	}
	return int(*config.CandidateCount)
}

// geminiRole 把内容角色换成上游角色，省略时为 user
func geminiRole(role string) (antigravity.Role, error) {
	switch role {
	case "", "user":
		return antigravity.RoleUser, nil
	case "model", "assistant":
		return antigravity.RoleAssistant, nil
	case "function", "tool":
		return antigravity.RoleTool, nil
	default:
		return "", fmt.Errorf("unsupported content role %q", role)
	}
}

// mapGeminiParts 转换 part 列表并返回其中是否有函数结果，每个 part 只能有一种数据，没有数据的 part 只保留思考签名
func mapGeminiParts(input []geminiPart) ([]antigravity.Part, bool, error) {
	parts := make([]antigravity.Part, 0, len(input))
	hasResult := false
	for index, part := range input {
		variants := 0
		if part.Text != nil {
			variants++
		}
		inline := part.inline()
		if inline != nil {
			variants++
		}
		if part.hasFile() {
			variants++
		}
		if part.FunctionCall != nil {
			variants++
		}
		if part.FunctionResponse != nil {
			variants++
		}
		if variants == 0 {
			if part.ThoughtSignature != "" {
				parts = append(parts, antigravity.Part{ThoughtSignature: part.ThoughtSignature})
			}
			continue
		}
		if variants > 1 {
			return nil, false, fmt.Errorf("parts[%d] must contain exactly one data field", index)
		}
		switch {
		case inline != nil:
			mime := inline.MIME()
			if mime == "" || inline.Data == "" {
				return nil, false, fmt.Errorf("inlineData requires mimeType and data")
			}
			data, err := decodeBase64Flexible(inline.Data)
			if err != nil {
				return nil, false, fmt.Errorf("inlineData.data: %w", err)
			}
			mimeType, data := normalizeImagePayload(mime, data)
			parts = append(parts, antigravity.Part{
				InlineData:       &antigravity.Blob{MIME: mimeType, Data: data},
				ThoughtSignature: part.ThoughtSignature,
				VideoMetadata:    part.VideoMetadata,
				MediaResolution:  part.MediaResolution,
			})
		case part.hasFile():
			raw := part.FileData
			if !rawJSONConfigured(raw) {
				raw = part.FileDataSnake
			}
			var file struct {
				MIMEType    string `json:"mimeType"`
				FileURI     string `json:"fileUri"`
				DisplayName string `json:"displayName"`
			}
			if err := json.Unmarshal(raw, &file); err != nil {
				return nil, false, fmt.Errorf("parts[%d].fileData: %w", index, err)
			}
			if !remoteMediaURL(file.FileURI) {
				return nil, false, fmt.Errorf("parts[%d].fileData.fileUri must be an http(s) URL", index)
			}
			reference := &antigravity.FileRef{ID: file.FileURI, Name: file.DisplayName, MIME: file.MIMEType}
			if id, ok := strings.CutPrefix(file.FileURI, geminiFilesURIPrefix); ok {
				reference.ID = id
			}
			if video, ok := youtubeURL(file.FileURI); ok {
				reference.ID, reference.MIME = video, cmp.Or(file.MIMEType, "video/*")
			}
			parts = append(parts, antigravity.Part{File: reference, ThoughtSignature: part.ThoughtSignature, VideoMetadata: part.VideoMetadata, MediaResolution: part.MediaResolution})
		case part.FunctionCall != nil:
			if part.FunctionCall.Name == "" {
				return nil, false, fmt.Errorf("functionCall requires name")
			}
			arguments, err := geminiJSONObject(part.FunctionCall.Args, "functionCall.args")
			if err != nil {
				return nil, false, err
			}
			parts = append(parts, antigravity.Part{
				FunctionCall: &antigravity.FunctionCall{
					ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Arguments: arguments, ThoughtSignature: part.ThoughtSignature,
				},
				ThoughtSignature: part.ThoughtSignature,
			})
		case part.FunctionResponse != nil:
			if part.FunctionResponse.Name == "" {
				return nil, false, fmt.Errorf("functionResponse requires name")
			}
			response, err := geminiJSONObject(part.FunctionResponse.Response, "functionResponse.response")
			if err != nil {
				return nil, false, err
			}
			media, _, err := mapGeminiParts(part.FunctionResponse.Parts)
			if err != nil {
				return nil, false, fmt.Errorf("functionResponse.parts: %w", err)
			}
			parts = append(parts, antigravity.Part{
				FunctionResult: &antigravity.FunctionResult{
					ID: part.FunctionResponse.ID, Name: part.FunctionResponse.Name, Content: response, Parts: media,
				},
				ThoughtSignature: part.ThoughtSignature,
			})
			hasResult = true
		default:
			if *part.Text == "" {
				if part.ThoughtSignature != "" {
					parts = append(parts, antigravity.Part{ThoughtSignature: part.ThoughtSignature})
				}
				continue
			}
			parts = append(parts, antigravity.Part{Text: *part.Text, Thought: part.Thought, ThoughtSignature: part.ThoughtSignature})
		}
	}
	return parts, hasResult, nil
}

// geminiJSONObject 校验字段为 JSON object，省略时为空对象
func geminiJSONObject(raw json.RawMessage, field string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%s must be an object", field)
	}
	return raw, nil
}

// mapGeminiTools 转换函数声明、Google 搜索与函数调用配置
func mapGeminiTools(groups []geminiToolGroup, config geminiToolConfig) (antigravity.Tools, error) {
	var mapped antigravity.Tools
	for _, group := range groups {
		for _, declaration := range group.FunctionDeclarations {
			if declaration.Name == "" {
				return antigravity.Tools{}, fmt.Errorf("function declaration name is required")
			}
			parameters := declaration.Parameters
			if len(declaration.ParametersJSONSchema) > 0 {
				parameters = declaration.ParametersJSONSchema
			}
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			mapped.Functions = append(mapped.Functions, antigravity.FunctionDeclaration{
				Name: declaration.Name, Description: declaration.Description, Parameters: parameters,
				Response: declaration.Response, ResponseJSONSchema: declaration.ResponseJSONSchema, Behavior: declaration.Behavior,
			})
		}
		switch {
		case rawJSONConfigured(group.GoogleSearch):
			mapped.GoogleSearch = group.GoogleSearch
		case rawJSONConfigured(group.GoogleSearchRetrieval) && mapped.GoogleSearch == nil:
			mapped.GoogleSearch = json.RawMessage(`{}`)
		}
	}
	var toolConfig antigravity.ToolConfig
	toolConfig.AllowedFunctionNames = config.FunctionCallingConfig.AllowedFunctionNames
	switch strings.ToUpper(config.FunctionCallingConfig.Mode) {
	case "", "AUTO", "MODE_UNSPECIFIED":
		toolConfig.Mode = "auto"
	case "ANY":
		toolConfig.Mode = "required"
	case "VALIDATED":
		toolConfig.Mode = "validated"
	case "NONE":
		toolConfig.Mode = "none"
	default:
		return antigravity.Tools{}, fmt.Errorf("unsupported functionCallingConfig mode %q", config.FunctionCallingConfig.Mode)
	}
	mapped.ToolConfig = toolConfig
	var err error
	if mapped.Functions, err = upstreamFunctions(mapped.Functions); err != nil {
		return antigravity.Tools{}, err
	}
	return mapped, nil
}

// handleGeminiGenerate 为每个候选启动一次生成，任一候选失败时整个请求按首个错误失败，按 output 输出图片与缓存用量
func (s *server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, request antigravity.GenerateRequest, stream bool, count int, output geminiOutput) {
	if !stream {
		var stopKeepalive func()
		w, stopKeepalive = s.nonstreamKeepalive(w)
		defer stopKeepalive()
	}
	candidates := s.startChatChoices(r.Context(), request, count, stream)
	defer candidates.cancel()
	if err := candidates.err; err != nil {
		writeGeminiFailure(w, r, err)
		return
	}
	if stream {
		s.streamGemini(w, r, request, candidates, output)
		return
	}
	results, err := candidates.run(func(_ int, events <-chan antigravity.Event) (generationResult, error) {
		return consumeEvents(candidates.ctx, events, nil)
	})
	if err != nil {
		writeGeminiFailure(w, r, err)
		return
	}
	response := buildGeminiResponse(request, output.cache, results)
	if output.imageURI {
		for _, candidate := range response["candidates"].([]any) {
			if content, ok := candidate.(map[string]any)["content"].(map[string]any); ok {
				if err := s.deliverGeminiImages(content["parts"].([]map[string]any)); err != nil {
					writeGeminiRequestError(w, err)
					return
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, officialOrder("gemini", response))
}

// deliverGeminiImages 把 part 中的内联图片存入 Files API，改为引用该文件的 fileData
func (s *server) deliverGeminiImages(parts []map[string]any) error {
	for _, part := range parts {
		inline, ok := part["inlineData"].(map[string]any)
		if !ok {
			continue
		}
		mimeType := inline["mimeType"].(string)
		data, _ := base64.StdEncoding.DecodeString(inline["data"].(string))
		file, err := s.files.save("image"+cmp.Or(mediaExtensions[mimeType], ".bin"), mimeType, defaultFilePurpose, geminiFileRetention, bytes.NewReader(data))
		if err != nil {
			return err
		}
		delete(part, "inlineData")
		part["fileData"] = map[string]any{"mimeType": mimeType, "fileUri": geminiFilesURIPrefix + file.ID}
	}
	return nil
}

// writeGeminiFailure 写出响应头之前的生成失败
func writeGeminiFailure(w http.ResponseWriter, r *http.Request, err error) {
	if shouldWriteRequestError(r, err) {
		writeGeminiRequestError(w, err)
	}
}

// buildGeminiResponse 按候选序号输出各候选内容与终止原因，用量为全部候选之和，cache 非空时计入缓存部分
func buildGeminiResponse(request antigravity.GenerateRequest, cache *geminiCache, results []generationResult) map[string]any {
	candidates := make([]any, 0, len(results))
	for index, result := range results {
		candidate := map[string]any{"index": index}
		if parts := geminiOutputParts(result); len(parts) > 0 {
			candidate["content"] = map[string]any{"role": "model", "parts": parts}
		}
		setGeminiCandidateEnd(candidate, result)
		candidates = append(candidates, candidate)
	}
	response := map[string]any{
		"candidates":   candidates,
		"modelVersion": request.Model,
		"responseId":   request.ID,
	}
	if results[0].providerModel != "" {
		response["modelVersion"] = results[0].providerModel
	}
	if usage := choicesUsage(results); usage != nil {
		response["usageMetadata"] = geminiUsage(cache.usage(usage))
	}
	return response
}

// geminiOutputParts 按事件顺序构造候选的 part：相邻的同类文本合并，独立签名并入相邻的没有签名的 part，否则由空文本 part 承载
func geminiOutputParts(result generationResult) []map[string]any {
	parts := make([]map[string]any, 0)
	var pendingSignature string

	appendPart := func(part map[string]any, sig string) {
		if sig != "" {
			part["thoughtSignature"] = sig
		}
		if pendingSignature != "" {
			if part["thoughtSignature"] == nil {
				part["thoughtSignature"] = pendingSignature
			} else {
				parts = append(parts, geminiSignaturePart(pendingSignature))
			}
			pendingSignature = ""
		}
		parts = append(parts, part)
	}

	for _, event := range mergedTextEvents(result.events) {
		switch event.Kind {
		case antigravity.EventText, antigravity.EventReasoning:
			thought := event.Kind == antigravity.EventReasoning
			if len(parts) > 0 {
				last := parts[len(parts)-1]
				if lastText, ok := last["text"].(string); ok && (last["thought"] == true) == thought && last["thoughtSignature"] == nil && event.ThoughtSignature == "" {
					last["text"] = lastText + event.Text
					continue
				}
			}
			part := map[string]any{"text": event.Text}
			if thought {
				part["thought"] = true
			}
			appendPart(part, event.ThoughtSignature)
		case antigravity.EventToolCall:
			appendPart(geminiFunctionCallPart(*event.ToolCall), event.ThoughtSignature)
		case antigravity.EventMedia:
			appendPart(geminiInlinePart(*event.Media), event.ThoughtSignature)
		case antigravity.EventThoughtSignature:
			if event.ThoughtSignature == "" {
				continue
			}
			if pendingSignature != "" {
				parts = append(parts, geminiSignaturePart(pendingSignature))
				pendingSignature = ""
			}
			if len(parts) > 0 && parts[len(parts)-1]["thoughtSignature"] == nil {
				parts[len(parts)-1]["thoughtSignature"] = event.ThoughtSignature
			} else if len(parts) > 0 {
				parts = append(parts, geminiSignaturePart(event.ThoughtSignature))
			} else {
				pendingSignature = event.ThoughtSignature
			}
		}
	}
	if pendingSignature != "" {
		parts = append(parts, geminiSignaturePart(pendingSignature))
	}
	return parts
}

// geminiInlinePart 构造模型输出的 inlineData part
func geminiInlinePart(media antigravity.Blob) map[string]any {
	return map[string]any{"inlineData": map[string]any{"mimeType": media.MIME, "data": base64.StdEncoding.EncodeToString(media.Data)}}
}

// geminiSignaturePart 用空文本 Part 承载没有正文的独立签名
func geminiSignaturePart(signature string) map[string]any {
	return map[string]any{"text": "", "thoughtSignature": signature}
}

// geminiFunctionCallPart 构造 functionCall part，带函数调用的思考签名
func geminiFunctionCallPart(call antigravity.FunctionCall) map[string]any {
	part := map[string]any{"functionCall": map[string]any{
		"id": call.ID, "name": call.Name, "args": call.Arguments,
	}}
	if call.ThoughtSignature != "" {
		part["thoughtSignature"] = call.ThoughtSignature
	}
	return part
}

// geminiSignedPart 在签名不为空时给 part 加上 thoughtSignature
func geminiSignedPart(part map[string]any, signature string) map[string]any {
	if signature != "" {
		part["thoughtSignature"] = signature
	}
	return part
}

// geminiFinishReason 把结束原因换成 Gemini 的 FinishReason，未知原因为 OTHER
func geminiFinishReason(reason string) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "", "stop", "stop_sequence":
		return "STOP"
	case "unspecified":
		return "FINISH_REASON_UNSPECIFIED"
	case "max_tokens", "max_output_tokens", "length":
		return "MAX_TOKENS"
	case "safety", "content_filter", "blocked":
		return "SAFETY"
	case "recitation":
		return "RECITATION"
	case "language":
		return "LANGUAGE"
	case "other":
		return "OTHER"
	case "blocklist":
		return "BLOCKLIST"
	case "prohibited_content":
		return "PROHIBITED_CONTENT"
	case "spii":
		return "SPII"
	case "malformed_function_call":
		return "MALFORMED_FUNCTION_CALL"
	case "image_safety":
		return "IMAGE_SAFETY"
	case "unexpected_tool_call":
		return "UNEXPECTED_TOOL_CALL"
	case "too_many_tool_calls":
		return "TOO_MANY_TOOL_CALLS"
	case "image_prohibited_content":
		return "IMAGE_PROHIBITED_CONTENT"
	case "image_other":
		return "IMAGE_OTHER"
	case "no_image":
		return "NO_IMAGE"
	case "image_recitation":
		return "IMAGE_RECITATION"
	default:
		return "OTHER"
	}
}

// setGeminiCandidateEnd 写入候选的终止原因、终止说明与搜索来源
func setGeminiCandidateEnd(candidate map[string]any, result generationResult) {
	candidate["finishReason"] = geminiFinishReason(result.finishReason)
	if result.finishMessage != "" {
		candidate["finishMessage"] = result.finishMessage
	}
	if result.grounding != nil {
		candidate["groundingMetadata"] = result.grounding
	}
}

// geminiUsage 把用量投影为 usageMetadata，与官方一样省略值为 0 的计数
func geminiUsage(usage *antigravity.Usage) map[string]any {
	metadata := map[string]any{}
	for name, value := range map[string]int64{
		"promptTokenCount":        usage.InputTokens,
		"candidatesTokenCount":    usage.OutputTokens,
		"thoughtsTokenCount":      usage.ReasoningTokens,
		"totalTokenCount":         usage.TotalTokens,
		"cachedContentTokenCount": usage.CachedTokens,
	} {
		if value != 0 {
			metadata[name] = value
		}
	}
	return metadata
}

// streamGemini 按到达顺序输出各候选的增量块，各块带所在上游块的累计用量；单个候选时最后一个内容块与终止原因、用量同在最后一块，多个候选时最后一块携带全部候选的终止原因与用量之和；alt=sse 时为 SSE，否则与官方一样为逐个对象写出的 JSON 数组
func (s *server) streamGemini(w http.ResponseWriter, r *http.Request, request antigravity.GenerateRequest, candidates *chatChoices, output geminiOutput) {
	sse := r.URL.Query().Get("alt") == "sse"
	if sse {
		stream, err := openStream(w, geminiHeartbeat)
		defer stream.stop()
		if err != nil {
			return
		}
		w = stream
	} else {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		if http.NewResponseController(w).Flush() != nil {
			return
		}
	}
	var writing sync.Mutex
	written := false
	write := func(value map[string]any) error {
		writing.Lock()
		defer writing.Unlock()
		if sse {
			return writeSSE(w, "", officialOrder("gemini", value))
		}
		data, err := json.Marshal(officialOrder("gemini", value))
		if err != nil {
			return err
		}
		prefix := "["
		if written {
			prefix = "\r\n,\r\n"
		}
		written = true
		if _, err := io.WriteString(w, prefix+string(data)); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
	// fail 写出流内错误：SSE 按官方 SDK 的解析方式写成不带 data: 前缀的整块 JSON，JSON 数组中写为一个元素
	fail := func(value map[string]any) error {
		if !sse {
			return write(value)
		}
		writing.Lock()
		defer writing.Unlock()
		data, err := json.Marshal(officialOrder("gemini", value))
		if err != nil {
			return err
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
	if !sse {
		defer func() {
			tail := "\r\n]"
			if !written {
				tail = "[]"
			}
			io.WriteString(w, tail)
		}()
	}
	single := len(candidates.events) == 1
	var last map[string]any
	results, err := candidates.run(func(index int, events <-chan antigravity.Event) (generationResult, error) {
		return consumeEvents(candidates.ctx, events, func(event antigravity.Event) error {
			candidate := geminiStreamCandidate(event, index)
			if candidate == nil {
				return nil
			}
			if output.imageURI {
				if err := s.deliverGeminiImages(candidate["content"].(map[string]any)["parts"].([]map[string]any)); err != nil {
					return err
				}
			}
			chunk := map[string]any{"responseId": request.ID, "modelVersion": cmp.Or(event.ProviderModel, request.Model), "candidates": []any{candidate}}
			if single && event.Usage != nil {
				chunk["usageMetadata"] = geminiUsage(output.cache.usage(event.Usage))
			}
			if !single {
				return write(chunk)
			}
			previous := last
			last = chunk
			if previous == nil {
				return nil
			}
			return write(previous)
		})
	})
	if err != nil {
		if !shouldWriteRequestError(r, err) || last != nil && write(last) != nil {
			return
		}
		_ = fail(map[string]any{"error": map[string]any{
			"code": statusFromError(err), "message": err.Error(), "status": geminiErrorStatus(err),
		}})
		return
	}
	model := request.Model
	if results[0].providerModel != "" {
		model = results[0].providerModel
	}
	if last != nil {
		setGeminiCandidateEnd(last["candidates"].([]any)[0].(map[string]any), results[0])
		if results[0].usage != nil {
			last["usageMetadata"] = geminiUsage(output.cache.usage(results[0].usage))
		}
		_ = write(last)
		return
	}
	finished := make([]any, 0, len(results))
	for index, result := range results {
		candidate := map[string]any{"index": index, "content": map[string]any{"role": "model", "parts": []any{}}}
		setGeminiCandidateEnd(candidate, result)
		finished = append(finished, candidate)
	}
	final := map[string]any{
		"responseId": request.ID, "modelVersion": model,
		"candidates": finished,
	}
	if usage := choicesUsage(results); usage != nil {
		final["usageMetadata"] = geminiUsage(output.cache.usage(usage))
	}
	_ = write(final)
}

// geminiStreamCandidate 把规范事件投影为指定序号候选的流式内容，没有可输出内容时返回 nil
func geminiStreamCandidate(event antigravity.Event, index int) map[string]any {
	var part map[string]any
	switch event.Kind {
	case antigravity.EventText:
		part = geminiSignedPart(map[string]any{"text": event.Text}, event.ThoughtSignature)
	case antigravity.EventReasoning:
		part = geminiSignedPart(map[string]any{"text": event.Text, "thought": true}, event.ThoughtSignature)
	case antigravity.EventToolCall:
		part = geminiSignedPart(geminiFunctionCallPart(*event.ToolCall), event.ThoughtSignature)
	case antigravity.EventThoughtSignature:
		if event.ThoughtSignature == "" {
			return nil
		}
		part = geminiSignaturePart(event.ThoughtSignature)
	case antigravity.EventMedia:
		part = geminiSignedPart(geminiInlinePart(*event.Media), event.ThoughtSignature)
	default:
		return nil
	}
	return map[string]any{"index": index, "content": map[string]any{"role": "model", "parts": []map[string]any{part}}}
}
