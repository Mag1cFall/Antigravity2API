package api

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

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

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiBlobPart struct {
	MIMEType      string `json:"mimeType"`
	MIMETypeSnake string `json:"mime_type"`
	Data          string `json:"data"`
}

func (b *geminiBlobPart) MIME() string {
	if b == nil {
		return ""
	}
	if b.MIMEType != "" {
		return b.MIMEType
	}
	return b.MIMETypeSnake
}

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
	SpeechConfig     json.RawMessage `json:"speechConfig"`
	MediaResolution  string          `json:"mediaResolution"`
	PresencePenalty  *float64        `json:"presencePenalty"`
	FrequencyPenalty *float64        `json:"frequencyPenalty"`
	Seed             *int64          `json:"seed"`
	ThinkingConfig   *struct {
		ThinkingBudget  *int64 `json:"thinkingBudget"`
		ThinkingLevel   string `json:"thinkingLevel"`
		IncludeThoughts bool   `json:"includeThoughts"`
	} `json:"thinkingConfig"`
}

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

type geminiToolConfig struct {
	FunctionCallingConfig struct {
		Mode                 string   `json:"mode"`
		AllowedFunctionNames []string `json:"allowedFunctionNames"`
	} `json:"functionCallingConfig"`
}

func (s *server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, geminiModelObject(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": data})
}

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
		writeJSON(w, http.StatusOK, geminiModelObject(model))
		return
	}
	writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("model %q is unavailable", modelID))
}

func geminiModelObject(model antigravity.Model) map[string]any {
	item := map[string]any{
		"name":                       "models/" + model.ID,
		"displayName":                model.Name,
		"description":                model.Description,
		"supportedGenerationMethods": model.Methods,
		"inputTokenLimit":            model.InputTokenLimit,
		"outputTokenLimit":           model.OutputTokenLimit,
	}
	if len(model.Capabilities) > 0 {
		item["capabilities"] = model.Capabilities
	}
	if len(model.ThinkingLevels) > 0 {
		item["thinkingLevels"], item["defaultThinkingLevel"] = model.ThinkingLevels, model.DefaultThinkingLevel
	}
	return item
}

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
	body, err := io.ReadAll(r.Body)
	if err == nil {
		body, _, err = geminiCamelKeys(body)
	}
	if err == nil {
		err = json.Unmarshal(body, &request)
	}
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if method == "countTokens" && request.GenerateContentRequest != nil {
		request = *request.GenerateContentRequest
	}
	if request.CachedContent != "" {
		writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("cachedContent %q was not found: this service does not store cached contents", request.CachedContent))
		return
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
	if method == "countTokens" {
		tokens, err := s.service.CountTokens(r.Context(), generateRequest)
		if err != nil {
			writeGeminiRequestError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"totalTokens": tokens})
		return
	}
	s.handleGeminiGenerate(w, r, generateRequest, method == "streamGenerateContent", request.GenerationConfig.candidates())
}

// geminiOpaqueFields 为取值是自由 JSON 的字段，其中的键名属于用户数据
var geminiOpaqueFields = map[string]bool{
	"args": true, "response": true, "parameters": true, "parametersJsonSchema": true,
	"responseSchema": true, "responseJsonSchema": true, "partMetadata": true,
}

// geminiCamelKeys 按 proto3 JSON 规则把各层消息字段名转为 lowerCamelCase，同层两种写法并存时保留驼峰字段，自由 JSON 字段的取值原样保留
func geminiCamelKeys(raw []byte) ([]byte, bool, error) {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || value[0] != '{' && value[0] != '[' {
		return raw, false, nil
	}
	changed := false
	if value[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(value, &items); err != nil {
			return nil, false, err
		}
		for index, item := range items {
			converted, itemChanged, err := geminiCamelKeys(item)
			if err != nil {
				return nil, false, err
			}
			items[index], changed = converted, changed || itemChanged
		}
		if !changed {
			return raw, false, nil
		}
		encoded, err := json.Marshal(items)
		return encoded, true, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return nil, false, err
	}
	converted := make(map[string]json.RawMessage, len(fields))
	for key, field := range fields {
		name := geminiJSONName(key)
		if name != key {
			changed = true
			if _, exists := fields[name]; exists {
				continue
			}
		}
		if !geminiOpaqueFields[name] {
			var fieldChanged bool
			var err error
			field, fieldChanged, err = geminiCamelKeys(field)
			if err != nil {
				return nil, false, err
			}
			changed = changed || fieldChanged
		}
		converted[name] = field
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := json.Marshal(converted)
	return encoded, true, err
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
				return antigravity.GenerateRequest{}, fmt.Errorf("systemInstruction cannot contain function calls or responses")
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

// geminiMaxCandidates 是 candidateCount 的上限
const geminiMaxCandidates = 8

// validate 校验候选数与输出模态：目录中没有输出音频或语音的模型，图片输出由所选模型是否生成图片决定
func (config geminiGenerationConfig) validate() error {
	if config.CandidateCount != nil && (*config.CandidateCount < 0 || *config.CandidateCount > geminiMaxCandidates) {
		return fmt.Errorf("generationConfig.candidateCount must be between 1 and %d", geminiMaxCandidates)
	}
	if rawJSONConfigured(config.SpeechConfig) || slices.ContainsFunc(config.ResponseModalities, func(modality string) bool { return strings.EqualFold(modality, "AUDIO") }) {
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

// handleGeminiGenerate 为每个候选启动一次生成，任一候选失败时整个请求按首个错误失败
func (s *server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, request antigravity.GenerateRequest, stream bool, count int) {
	candidates := s.startChatChoices(r.Context(), request, count, stream)
	defer candidates.cancel()
	if err := candidates.err; err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	if stream {
		s.streamGemini(w, r, request, candidates)
		return
	}
	results, err := candidates.run(func(_ int, events <-chan antigravity.Event) (generationResult, error) {
		return consumeEvents(candidates.ctx, events, nil)
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, buildGeminiResponse(request, results))
}

// buildGeminiResponse 按候选序号输出各候选内容与终止原因，用量为全部候选之和
func buildGeminiResponse(request antigravity.GenerateRequest, results []generationResult) map[string]any {
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
	if usage := sumUsage(results); usage != nil {
		response["usageMetadata"] = geminiUsage(usage)
	}
	return response
}

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

	for _, event := range result.events {
		switch event.Kind {
		case antigravity.EventText:
			if len(parts) > 0 {
				last := parts[len(parts)-1]
				if lastText, ok := last["text"].(string); ok && last["thought"] != true && last["thoughtSignature"] == nil && event.ThoughtSignature == "" {
					last["text"] = lastText + event.Text
					continue
				}
			}
			appendPart(map[string]any{"text": event.Text}, event.ThoughtSignature)
		case antigravity.EventReasoning:
			if len(parts) > 0 {
				last := parts[len(parts)-1]
				if lastText, ok := last["text"].(string); ok && last["thought"] == true && last["thoughtSignature"] == nil && event.ThoughtSignature == "" {
					last["text"] = lastText + event.Text
					continue
				}
			}
			appendPart(map[string]any{"text": event.Text, "thought": true}, event.ThoughtSignature)
		case antigravity.EventToolCall:
			if event.ToolCall != nil {
				appendPart(geminiFunctionCallPart(*event.ToolCall), event.ThoughtSignature)
			}
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

func geminiFunctionCallPart(call antigravity.FunctionCall) map[string]any {
	part := map[string]any{"functionCall": map[string]any{
		"id": call.ID, "name": call.Name, "args": call.Arguments,
	}}
	if call.ThoughtSignature != "" {
		part["thoughtSignature"] = call.ThoughtSignature
	}
	return part
}

func geminiSignedPart(part map[string]any, signature string) map[string]any {
	if signature != "" {
		part["thoughtSignature"] = signature
	}
	return part
}

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

// setGeminiCandidateEnd 写入候选的终止原因与搜索来源
func setGeminiCandidateEnd(candidate map[string]any, result generationResult) {
	candidate["finishReason"] = geminiFinishReason(result.finishReason)
	if result.grounding != nil {
		candidate["groundingMetadata"] = result.grounding
	}
}

func geminiUsage(usage *antigravity.Usage) map[string]any {
	metadata := map[string]any{
		"promptTokenCount":     usage.InputTokens,
		"candidatesTokenCount": usage.OutputTokens,
		"thoughtsTokenCount":   usage.ReasoningTokens,
		"totalTokenCount":      usage.TotalTokens,
	}
	if usage.CachedTokens > 0 {
		metadata["cachedContentTokenCount"] = usage.CachedTokens
	}
	return metadata
}

// streamGemini 按到达顺序输出各候选的增量块，最后一块携带全部候选的终止原因与用量之和；alt=sse 时为 SSE，否则与官方一样为逐个对象写出的 JSON 数组
func (s *server) streamGemini(w http.ResponseWriter, r *http.Request, request antigravity.GenerateRequest, candidates *chatChoices) {
	sse := r.URL.Query().Get("alt") == "sse"
	if sse {
		if err := streamHeaders(w); err != nil {
			return
		}
	} else {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
	}
	var writing sync.Mutex
	written := false
	write := func(value map[string]any) error {
		writing.Lock()
		defer writing.Unlock()
		if sse {
			return writeSSE(w, "", value)
		}
		data, err := json.Marshal(value)
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
	if !sse {
		defer func() {
			tail := "\r\n]"
			if !written {
				tail = "[]"
			}
			io.WriteString(w, tail)
		}()
	}
	heartbeat := func() error {
		if !sse {
			return nil
		}
		writing.Lock()
		defer writing.Unlock()
		return writeSSEHeartbeat(w)
	}
	results, err := candidates.run(func(index int, events <-chan antigravity.Event) (generationResult, error) {
		return consumeStreamEvents(candidates.ctx, events, func(event antigravity.Event) error {
			candidate := geminiStreamCandidate(event, index)
			if candidate == nil {
				return nil
			}
			return write(map[string]any{"responseId": request.ID, "modelVersion": cmp.Or(event.ProviderModel, request.Model), "candidates": []any{candidate}})
		}, heartbeat)
	})
	if err != nil {
		if !shouldWriteRequestError(r, err) {
			return
		}
		_ = write(map[string]any{"error": map[string]any{
			"code": statusFromError(err), "message": err.Error(), "status": geminiErrorStatus(err),
		}})
		return
	}
	model := request.Model
	if results[0].providerModel != "" {
		model = results[0].providerModel
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
	if usage := sumUsage(results); usage != nil {
		final["usageMetadata"] = geminiUsage(usage)
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
		if event.ToolCall == nil {
			return nil
		}
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
	return map[string]any{"index": index, "content": map[string]any{"role": "model", "parts": []any{part}}}
}
