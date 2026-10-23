package api

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// interactionRequest 为 Interactions 创建请求
type interactionRequest struct {
	Model      string          `json:"model"`
	Input      json.RawMessage `json:"input"`
	System     string          `json:"system_instruction"`
	Stream     bool            `json:"stream"`
	Store      *bool           `json:"store"`
	PreviousID string          `json:"previous_interaction_id"`
	Formats    json.RawMessage `json:"response_format"`
	MIME       string          `json:"response_mime_type"`
	Modalities []string        `json:"response_modalities"`
	Safety     []struct {
		Type      string `json:"type"`
		Threshold string `json:"threshold"`
	} `json:"safety_settings"`
	Tools      []responsesTool `json:"tools"`
	Generation struct {
		Temperature       *float64        `json:"temperature"`
		TopP              *float64        `json:"top_p"`
		TopK              *int            `json:"top_k"`
		MaxOutputTokens   *int64          `json:"max_output_tokens"`
		Seed              *int64          `json:"seed"`
		StopSequences     []string        `json:"stop_sequences"`
		ThinkingLevel     string          `json:"thinking_level"`
		ThinkingSummaries string          `json:"thinking_summaries"`
		Speech            json.RawMessage `json:"speech_config"`
		ToolChoice        json.RawMessage `json:"tool_choice"`
		Image             *struct {
			AspectRatio string `json:"aspect_ratio"`
			ImageSize   string `json:"image_size"`
		} `json:"image_config"`
	} `json:"generation_config"`
}

// interactionFormat 为文本与图片的返回配置
type interactionFormat struct {
	Type        string          `json:"type"`
	MIME        string          `json:"mime_type"`
	Schema      json.RawMessage `json:"schema"`
	AspectRatio string          `json:"aspect_ratio"`
	ImageSize   string          `json:"image_size"`
}

// interactionInput 为输入内容块或执行步骤
type interactionInput struct {
	Type      string             `json:"type"`
	Text      *string            `json:"text"`
	Data      string             `json:"data"`
	URI       string             `json:"uri"`
	MIME      string             `json:"mime_type"`
	Content   []interactionInput `json:"content"`
	ID        string             `json:"id"`
	CallID    string             `json:"call_id"`
	Name      string             `json:"name"`
	Arguments json.RawMessage    `json:"arguments"`
	Result    json.RawMessage    `json:"result"`
	IsError   bool               `json:"is_error"`
	Signature string             `json:"signature"`
}

// interactionRecord 为已保存交互的资源对象与请求输入步骤
type interactionRecord struct {
	object json.RawMessage
	input  []json.RawMessage
}

// handleInteraction 把 Interactions 请求接入规范生成链路，previous_interaction_id 续接已保存的交互
func (s *server) handleInteraction(w http.ResponseWriter, r *http.Request) {
	var request interactionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	generate, err := request.toGenerateRequest(newID("int"))
	if err == nil {
		err = inlineRemoteMedia(r.Context(), generate.Contents)
	}
	if err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	generate.Session = clientSession(r)
	current := cloneResponseContents(generate.Contents)
	var previous responseHistory
	if request.PreviousID != "" {
		var ok bool
		if previous, ok = s.interactions.Load(request.PreviousID); !ok {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "previous_interaction_id was not found")
			return
		}
		generate.Contents = append(cloneResponseContents(previous.Contents), generate.Contents...)
	}
	if err := resolveInteractionResults(generate.Contents); err != nil {
		writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	r = r.WithContext(ctx)
	generate.Unary = !request.Stream
	events, err := s.service.Generate(ctx, generate)
	if err == nil && request.Stream {
		events, _, err = awaitStreamStart(ctx, events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	created := time.Now().UTC().Format(time.RFC3339)
	if request.Stream {
		s.streamInteraction(w, r, request, generate, previous, current, created, events)
		return
	}
	result, err := consumeEvents(ctx, events, nil)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeGeminiRequestError(w, err)
		}
		return
	}
	response := interactionObject(generate, created, interactionStatus(result), result.usage)
	response["steps"] = interactionSteps(result, request.Generation.ThinkingSummaries != "none")
	if request.Store == nil || *request.Store {
		s.storeInteraction(request, previous, current, result, response)
	}
	writeJSON(w, http.StatusOK, response)
}

// interactionList 解析单对象或数组形式的联合字段
func interactionList[T any](raw json.RawMessage) ([]T, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return nil, nil
	}
	var values []T
	if strings.HasPrefix(value, "[") {
		err := json.Unmarshal(raw, &values)
		return values, err
	}
	var item T
	if err := json.Unmarshal(raw, &item); err != nil {
		return nil, err
	}
	return []T{item}, nil
}

// toGenerateRequest 映射模型、输入步骤、生成参数、返回格式与工具
func (request interactionRequest) toGenerateRequest(id string) (antigravity.GenerateRequest, error) {
	generate := antigravity.GenerateRequest{ID: id, Model: strings.TrimPrefix(strings.TrimSpace(request.Model), "models/"), System: request.System}
	if generate.Model == "" {
		return generate, fmt.Errorf("model is required")
	}
	var err error
	if generate.Contents, err = interactionContents(request.Input); err != nil {
		return generate, err
	}
	generation := request.Generation
	generate.Config = antigravity.GenerationConfig{
		Temperature: generation.Temperature, TopP: generation.TopP, TopK: generation.TopK,
		MaxOutputTokens: generation.MaxOutputTokens, Seed: generation.Seed,
		StopSequences: normalizeStopSequences(generation.StopSequences), ReasoningEffort: generation.ThinkingLevel,
		HideThinking: generation.ThinkingSummaries == "none",
	}
	if generation.ThinkingSummaries != "" && generation.ThinkingSummaries != "auto" && generation.ThinkingSummaries != "none" {
		return generate, fmt.Errorf("thinking_summaries must be auto or none")
	}
	if rawJSONConfigured(generation.Speech) {
		return generate, fmt.Errorf("audio output is not supported: no model in the catalog generates audio")
	}
	formats, err := interactionList[json.RawMessage](request.Formats)
	if err != nil {
		return generate, fmt.Errorf("response_format: %w", err)
	}
	for _, raw := range formats {
		var format interactionFormat
		if err := json.Unmarshal(raw, &format); err != nil {
			return generate, fmt.Errorf("response_format: %w", err)
		}
		switch format.Type {
		case "text":
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, "TEXT")
			if format.MIME == "" && len(format.Schema) > 0 {
				format.MIME = "application/json"
			}
			if format.MIME != "" && format.MIME != "text/plain" && format.MIME != "application/json" {
				return generate, fmt.Errorf("text mime_type must be text/plain or application/json")
			}
			if len(format.Schema) > 0 && format.MIME != "application/json" {
				return generate, fmt.Errorf("response_format.schema requires application/json")
			}
			generate.Config.ResponseMIMEType, generate.Config.ResponseSchema = format.MIME, format.Schema
		case "", "object", "array", "string", "number", "integer", "boolean", "null":
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, "TEXT")
			generate.Config.ResponseMIMEType, generate.Config.ResponseSchema = "application/json", raw
		case "image":
			if format.MIME != "" && format.MIME != "image/jpeg" {
				return generate, fmt.Errorf("image mime_type must be image/jpeg")
			}
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, "IMAGE")
			generate.Config.ImageConfig = &antigravity.ImageConfig{AspectRatio: format.AspectRatio, ImageSize: format.ImageSize}
		case "audio":
			return generate, fmt.Errorf("audio output is not supported: no model in the catalog generates audio")
		default:
			return generate, fmt.Errorf("unsupported response_format type %q", format.Type)
		}
	}
	if request.MIME != "" {
		generate.Config.ResponseMIMEType = request.MIME
	}
	if len(generate.Config.ResponseModalities) == 0 {
		for _, modality := range request.Modalities {
			if strings.EqualFold(modality, "audio") {
				return generate, fmt.Errorf("audio output is not supported: no model in the catalog generates audio")
			}
			generate.Config.ResponseModalities = append(generate.Config.ResponseModalities, strings.ToUpper(modality))
		}
	}
	if image := generation.Image; image != nil && generate.Config.ImageConfig == nil {
		generate.Config.ImageConfig = &antigravity.ImageConfig{AspectRatio: image.AspectRatio, ImageSize: image.ImageSize}
	}
	var safety []antigravity.SafetySetting
	for _, setting := range request.Safety {
		safety = append(safety, antigravity.SafetySetting{Category: "HARM_CATEGORY_" + setting.Type, Threshold: setting.Threshold})
	}
	generate.Config.SafetySettings = geminiSafetySettings(safety)
	tools := make([]responsesTool, 0, len(request.Tools))
	for _, tool := range request.Tools {
		switch tool.Type {
		case "function":
		case "google_search":
			tool.Type = "web_search"
		case "url_context", "google_maps", "code_execution", "computer_use", "file_search", "mcp_server", "retrieval":
			continue
		default:
			return generate, fmt.Errorf("unsupported interaction tool %q", tool.Type)
		}
		tools = append(tools, tool)
	}
	if generate.Tools, err = mapResponsesTools(tools, nil); err != nil {
		return generate, err
	}
	generate.Tools.ToolConfig, err = interactionToolChoice(generation.ToolChoice)
	return generate, err
}

// interactionToolChoice 把调用模式或 allowed_tools 映射为工具选择
func interactionToolChoice(raw json.RawMessage) (antigravity.ToolConfig, error) {
	if !rawJSONConfigured(raw) {
		return antigravity.ToolConfig{Mode: "auto"}, nil
	}
	var mode string
	var names []string
	if json.Unmarshal(raw, &mode) != nil {
		var config struct {
			AllowedTools struct {
				Mode  string   `json:"mode"`
				Tools []string `json:"tools"`
			} `json:"allowed_tools"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			return antigravity.ToolConfig{}, fmt.Errorf("generation_config.tool_choice: %w", err)
		}
		mode, names = config.AllowedTools.Mode, config.AllowedTools.Tools
	}
	switch mode {
	case "", "auto":
		mode = "auto"
	case "any":
		mode = "required"
	case "none", "validated":
	default:
		return antigravity.ToolConfig{}, fmt.Errorf("unsupported generation_config.tool_choice %q", mode)
	}
	return antigravity.ToolConfig{Mode: mode, AllowedFunctionNames: names}, nil
}

// interactionContents 按输入步骤顺序转换内容，合并相邻同角色的内容，思考签名接到其后的模型内容
func interactionContents(raw json.RawMessage) ([]antigravity.Content, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil, nil
		}
		return []antigravity.Content{{Role: antigravity.RoleUser, Parts: []antigravity.Part{{Text: text}}}}, nil
	}
	items, err := interactionList[interactionInput](raw)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}
	var contents []antigravity.Content
	pendingSignature := ""
	for _, item := range items {
		role := antigravity.RoleUser
		var parts []antigravity.Part
		switch item.Type {
		case "user_input", "model_output":
			if item.Type == "model_output" {
				role = antigravity.RoleAssistant
			}
			for _, content := range item.Content {
				mapped, err := content.parts()
				if err != nil {
					return nil, err
				}
				parts = append(parts, mapped...)
			}
		case "function_call":
			if item.ID == "" || item.Name == "" || len(item.Arguments) == 0 {
				return nil, fmt.Errorf("function_call requires id, name and arguments")
			}
			role = antigravity.RoleAssistant
			parts = []antigravity.Part{{FunctionCall: &antigravity.FunctionCall{ID: item.ID, Name: item.Name, Arguments: item.Arguments, ThoughtSignature: pendingSignature}, ThoughtSignature: pendingSignature}}
			pendingSignature = ""
		case "function_result":
			if item.CallID == "" || len(item.Result) == 0 {
				return nil, fmt.Errorf("function_result requires call_id and result")
			}
			content, media, err := functionResultContent(item.Result, interactionResultParts)
			if err != nil {
				return nil, fmt.Errorf("function_result: %w", err)
			}
			if item.IsError {
				content = json.RawMessage(`{"error":` + string(content) + `}`)
			}
			role = antigravity.RoleTool
			parts = []antigravity.Part{{FunctionResult: &antigravity.FunctionResult{ID: item.CallID, Name: item.Name, Content: content, Parts: media}}}
		case "thought":
			role = antigravity.RoleAssistant
			if item.Signature != "" {
				pendingSignature = item.Signature
			}
		case "google_search_call", "google_search_result", "url_context_call", "url_context_result", "code_execution_call", "code_execution_result",
			"google_maps_call", "google_maps_result", "file_search_call", "file_search_result",
			"mcp_server_tool_call", "mcp_server_tool_result", "retrieval_call", "retrieval_result", "processing_call", "processing_result":
			continue
		default:
			if parts, err = item.parts(); err != nil {
				return nil, err
			}
		}
		if len(parts) == 0 {
			continue
		}
		if pendingSignature != "" {
			if role == antigravity.RoleAssistant {
				parts[0].ThoughtSignature = pendingSignature
			} else if len(contents) > 0 && contents[len(contents)-1].Role == antigravity.RoleAssistant {
				contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, antigravity.Part{ThoughtSignature: pendingSignature})
			}
			pendingSignature = ""
		}
		if len(contents) > 0 && contents[len(contents)-1].Role == role {
			contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, parts...)
		} else {
			contents = append(contents, antigravity.Content{Role: role, Parts: parts})
		}
	}
	if pendingSignature != "" && len(contents) > 0 && contents[len(contents)-1].Role == antigravity.RoleAssistant {
		contents[len(contents)-1].Parts = append(contents[len(contents)-1].Parts, antigravity.Part{ThoughtSignature: pendingSignature})
	}
	return contents, nil
}

// interactionResultParts 转换函数结果中的媒体内容块
func interactionResultParts(raw json.RawMessage) ([]antigravity.Part, error) {
	items, err := interactionList[interactionInput](raw)
	if err != nil {
		return nil, err
	}
	var parts []antigravity.Part
	for _, item := range items {
		mapped, err := item.parts()
		if err != nil {
			return nil, err
		}
		parts = append(parts, mapped...)
	}
	return parts, nil
}

// parts 复用 Gemini 的文本、内联媒体与文件 URI 输入映射
func (content interactionInput) parts() ([]antigravity.Part, error) {
	var part geminiPart
	switch content.Type {
	case "text":
		if content.Text == nil {
			return nil, fmt.Errorf("text content requires text")
		}
		part.Text = content.Text
	case "image", "audio", "video", "document":
		if (content.Data == "") == (content.URI == "") {
			return nil, fmt.Errorf("%s content requires exactly one of data or uri", content.Type)
		}
		if content.Data != "" {
			mimeType := content.MIME
			if mimeType == "" {
				data, err := decodeBase64Flexible(content.Data)
				if err != nil {
					return nil, fmt.Errorf("%s data: %w", content.Type, err)
				}
				mimeType = detectMediaType("", data)
			}
			part.InlineData = &geminiBlobPart{MIMEType: mimeType, Data: content.Data}
		} else {
			part.FileData, _ = json.Marshal(map[string]string{"mimeType": content.MIME, "fileUri": content.URI})
		}
	default:
		return nil, fmt.Errorf("unsupported input type %q", content.Type)
	}
	parts, _, err := mapGeminiParts([]geminiPart{part})
	return parts, err
}

// resolveInteractionResults 按完整调用历史补齐函数结果的函数名
func resolveInteractionResults(contents []antigravity.Content) error {
	calls := make(map[string]string)
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				calls[part.FunctionCall.ID] = part.FunctionCall.Name
			}
			if result := part.FunctionResult; result != nil {
				name := cmp.Or(calls[result.ID], result.Name)
				if name == "" || result.Name != "" && result.Name != name {
					return fmt.Errorf("function result %q must match a preceding function call", result.ID)
				}
				result.Name = name
			}
		}
	}
	return nil
}

// interactionObject 构造交互资源的标识、状态与用量
func interactionObject(request antigravity.GenerateRequest, created, status string, usage *antigravity.Usage) map[string]any {
	object := map[string]any{
		"id": request.ID, "object": "interaction", "model": request.Model,
		"created": created, "updated": time.Now().UTC().Format(time.RFC3339), "status": status,
	}
	if usage != nil {
		object["usage"] = map[string]any{
			"total_input_tokens": usage.InputTokens, "total_cached_tokens": usage.CachedTokens, "total_output_tokens": usage.OutputTokens,
			"total_thought_tokens": usage.ReasoningTokens, "total_tokens": usage.TotalTokens,
		}
	}
	return object
}

// interactionStatus 把生成终态换成交互状态：有函数调用为 requires_action，非正常结束为 incomplete
func interactionStatus(result generationResult) string {
	switch openAIFinishReason(result.finishReason, len(result.toolCalls) > 0) {
	case "length", "content_filter":
		return "incomplete"
	case "tool_calls":
		return "requires_action"
	}
	return "completed"
}

// interactionStep 把一个规范事件投影为步骤与流式增量
func interactionStep(event antigravity.Event) (map[string]any, map[string]any) {
	var content map[string]any
	switch event.Kind {
	case antigravity.EventText:
		content = map[string]any{"type": "text", "text": event.Text}
	case antigravity.EventReasoning:
		content = map[string]any{"type": "text", "text": event.Text}
		step := map[string]any{"type": "thought", "summary": []map[string]any{content}}
		if event.ThoughtSignature != "" {
			step["signature"] = event.ThoughtSignature
		}
		return step, map[string]any{"type": "thought_summary", "content": content}
	case antigravity.EventThoughtSignature:
		if event.ThoughtSignature == "" {
			return nil, nil
		}
		return map[string]any{"type": "thought", "signature": event.ThoughtSignature}, map[string]any{"type": "thought_signature", "signature": event.ThoughtSignature}
	case antigravity.EventToolCall:
		call := event.ToolCall
		return map[string]any{"type": "function_call", "id": call.ID, "name": call.Name, "arguments": call.Arguments}, nil
	case antigravity.EventMedia:
		kind, _, _ := strings.Cut(event.Media.MIME, "/")
		if kind != "image" && kind != "audio" && kind != "video" {
			kind = "document"
		}
		content = map[string]any{"type": kind, "mime_type": event.Media.MIME, "data": base64.StdEncoding.EncodeToString(event.Media.Data)}
	}
	if content == nil {
		return nil, nil
	}
	return map[string]any{"type": "model_output", "content": []map[string]any{content}}, content
}

// interactionEvents 返回要输出的事件：正文、媒体与函数调用的签名先作为思考步骤，不要求思考摘要时思考只保留签名
func interactionEvents(event antigravity.Event, summaries bool) []antigravity.Event {
	if event.Kind == antigravity.EventReasoning && !summaries {
		if event.ThoughtSignature == "" {
			return nil
		}
		event = antigravity.Event{Kind: antigravity.EventThoughtSignature, ThoughtSignature: event.ThoughtSignature}
	}
	signature := event.ThoughtSignature
	switch event.Kind {
	case antigravity.EventToolCall:
		signature = cmp.Or(event.ToolCall.ThoughtSignature, signature)
	case antigravity.EventText, antigravity.EventMedia:
	default:
		signature = ""
	}
	if signature != "" {
		return []antigravity.Event{{Kind: antigravity.EventThoughtSignature, ThoughtSignature: signature}, event}
	}
	return []antigravity.Event{event}
}

// interactionSteps 汇总完整步骤，相邻同类的模型输出与思考合并，搜索来源写入正文 text 内容的 annotations
func interactionSteps(result generationResult, summaries bool) []map[string]any {
	steps := make([]map[string]any, 0)
	for _, original := range result.events {
		for _, event := range interactionEvents(original, summaries) {
			step, _ := interactionStep(event)
			if step == nil {
				continue
			}
			if len(steps) == 0 || step["type"] != steps[len(steps)-1]["type"] || step["type"] == "function_call" {
				steps = append(steps, step)
				continue
			}
			previous := steps[len(steps)-1]
			field := "content"
			if step["type"] == "thought" {
				field = "summary"
			}
			items, _ := step[field].([]map[string]any)
			prior, _ := previous[field].([]map[string]any)
			for _, item := range items {
				if len(prior) > 0 && item["type"] == "text" && prior[len(prior)-1]["type"] == "text" {
					prior[len(prior)-1]["text"] = prior[len(prior)-1]["text"].(string) + item["text"].(string)
				} else {
					prior = append(prior, item)
				}
			}
			if len(prior) > 0 {
				previous[field] = prior
			}
			if signature, ok := step["signature"]; ok {
				previous["signature"] = signature
			}
		}
	}
	if result.grounding != nil {
		for _, step := range steps {
			contents, _ := step["content"].([]map[string]any)
			for _, content := range contents {
				if text, ok := content["text"].(string); ok {
					if annotations := interactionAnnotations(text, result.grounding); len(annotations) > 0 {
						content["annotations"] = annotations
					}
				}
			}
		}
	}
	return steps
}

// streamInteraction 输出 interaction.created、各步骤的 step.start、step.delta、step.stop 与 interaction.completed；正文之后的独立签名暂存到下一个事件，使搜索来源先以 text_annotation_delta 写在正文步骤内
func (s *server) streamInteraction(w http.ResponseWriter, r *http.Request, request interactionRequest, generate antigravity.GenerateRequest, previous responseHistory, current []antigravity.Content, created string, events <-chan antigravity.Event) {
	if err := streamHeaders(w); err != nil {
		return
	}
	send := func(kind string, value map[string]any) error {
		value["event_type"] = kind
		return writeSSE(w, kind, value)
	}
	if err := send("interaction.created", map[string]any{"interaction": interactionObject(generate, created, "in_progress", nil)}); err != nil {
		return
	}
	index := -1
	active := ""
	closeStep := func() error {
		if active == "" {
			return nil
		}
		active = ""
		return send("step.stop", map[string]any{"index": index})
	}
	summaries := request.Generation.ThinkingSummaries != "none"
	var text strings.Builder
	var held []antigravity.Event
	write := func(original antigravity.Event) error {
		for _, event := range interactionEvents(original, summaries) {
			step, delta := interactionStep(event)
			if step == nil {
				continue
			}
			kind := step["type"].(string)
			if active != kind || kind == "function_call" {
				if err := closeStep(); err != nil {
					return err
				}
				index++
				active = kind
				start := map[string]any{"type": kind}
				switch kind {
				case "model_output":
					start["content"] = []any{}
				case "thought":
					start["summary"] = []any{}
				case "function_call":
					start = step
				}
				if err := send("step.start", map[string]any{"index": index, "step": start}); err != nil {
					return err
				}
				text.Reset()
			}
			if delta != nil {
				if delta["type"] == "text" {
					text.WriteString(delta["text"].(string))
				} else {
					text.Reset()
				}
				if err := send("step.delta", map[string]any{"index": index, "delta": delta}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	result, err := consumeStreamEvents(r.Context(), events, func(event antigravity.Event) error {
		switch {
		case event.Kind == antigravity.EventGrounding && text.Len() > 0:
			if annotations := interactionAnnotations(text.String(), event.Grounding); len(annotations) > 0 {
				return send("step.delta", map[string]any{"index": index, "delta": map[string]any{"type": "text_annotation_delta", "annotations": annotations}})
			}
			return nil
		case event.Kind == antigravity.EventThoughtSignature && text.Len() > 0:
			held = append(held, event)
			return nil
		}
		for _, signature := range held {
			if err := write(signature); err != nil {
				return err
			}
		}
		held = nil
		return write(event)
	}, func() error { return writeSSEHeartbeat(w) })
	if err != nil {
		if shouldWriteRequestError(r, err) {
			send("error", map[string]any{"error": map[string]any{"code": geminiErrorStatus(err), "message": err.Error()}})
		}
		return
	}
	if err := closeStep(); err != nil {
		return
	}
	completed := interactionObject(generate, created, interactionStatus(result), result.usage)
	if request.Store == nil || *request.Store {
		s.storeInteraction(request, previous, current, result, completed)
	}
	send("interaction.completed", map[string]any{"interaction": completed})
}

// storeInteraction 保存续接上下文与可检索的交互资源，对象不含步骤时按生成结果补齐
func (s *server) storeInteraction(request interactionRequest, previous responseHistory, current []antigravity.Content, result generationResult, object map[string]any) {
	stored := maps.Clone(object)
	if _, ok := stored["steps"]; !ok {
		stored["steps"] = interactionSteps(result, request.Generation.ThinkingSummaries != "none")
	}
	encoded, _ := json.Marshal(stored)
	id, _ := object["id"].(string)
	s.interactions.Store(id, previous, result, responseState{Contents: current, Interaction: &interactionRecord{object: encoded, input: interactionInputSteps(request.Input)}})
}

// Interaction 返回已保存的交互资源
func (store *responseStateStore) Interaction(id string) (*interactionRecord, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[id]
	return state.Interaction, exists && state.Interaction != nil
}

// interactionInputSteps 把请求输入整理为输入步骤，连续的内容块合并为一个 user_input 步骤
func interactionInputSteps(raw json.RawMessage) []json.RawMessage {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		step, _ := json.Marshal(map[string]any{"type": "user_input", "content": []map[string]any{{"type": "text", "text": text}}})
		return []json.RawMessage{step}
	}
	items, _ := interactionList[json.RawMessage](raw)
	steps := make([]json.RawMessage, 0, len(items))
	var content []json.RawMessage
	flush := func() {
		if len(content) > 0 {
			step, _ := json.Marshal(map[string]any{"type": "user_input", "content": content})
			steps = append(steps, step)
			content = nil
		}
	}
	for _, item := range items {
		switch responsesItemType(item) {
		case "text", "image", "audio", "video", "document":
			content = append(content, item)
		default:
			flush()
			steps = append(steps, item)
		}
	}
	flush()
	return steps
}

// handleInteractionGet 返回已保存的交互，include_input 时在步骤前加入请求输入，stream 时按流式事件重放
func (s *server) handleInteractionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	record, ok := s.interactions.Interaction(id)
	if !ok {
		writeInteractionNotFound(w, id)
		return
	}
	query := r.URL.Query()
	if stream, _ := strconv.ParseBool(query.Get("stream")); stream {
		replayInteraction(w, record.object, query.Get("last_event_id"))
		return
	}
	if include, _ := strconv.ParseBool(query.Get("include_input")); include && len(record.input) > 0 {
		var fields map[string]json.RawMessage
		var steps []json.RawMessage
		json.Unmarshal(record.object, &fields)
		json.Unmarshal(fields["steps"], &steps)
		fields["steps"], _ = json.Marshal(append(slices.Clone(record.input), steps...))
		writeJSON(w, http.StatusOK, fields)
		return
	}
	writeJSON(w, http.StatusOK, record.object)
}

// handleInteractionDelete 删除已保存的交互，之后不能再作为 previous_interaction_id
func (s *server) handleInteractionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.interactions.Interaction(id); !ok || !s.interactions.Delete(id) {
		writeInteractionNotFound(w, id)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

// handleInteractionCancel 返回已保存的交互，交互在请求内完成，没有进行中的后台交互
func (s *server) handleInteractionCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	record, ok := s.interactions.Interaction(id)
	if !ok {
		writeInteractionNotFound(w, id)
		return
	}
	writeJSON(w, http.StatusOK, record.object)
}

// replayInteraction 按创建时的流式事件格式重放已保存的交互，只输出 last_event_id 之后的事件
func replayInteraction(w http.ResponseWriter, object json.RawMessage, lastEventID string) {
	type replayEvent struct {
		kind  string
		value map[string]any
	}
	var fields map[string]json.RawMessage
	var steps []map[string]json.RawMessage
	json.Unmarshal(object, &fields)
	json.Unmarshal(fields["steps"], &steps)
	delete(fields, "steps")
	created := maps.Clone(fields)
	created["status"] = json.RawMessage(`"in_progress"`)
	delete(created, "usage")
	events := []replayEvent{{"interaction.created", map[string]any{"interaction": created}}}
	for index, step := range steps {
		var kind string
		json.Unmarshal(step["type"], &kind)
		switch kind {
		case "model_output":
			var content []json.RawMessage
			json.Unmarshal(step["content"], &content)
			events = append(events, replayEvent{"step.start", map[string]any{"index": index, "step": map[string]any{"type": kind, "content": []any{}}}})
			for _, item := range content {
				var delta map[string]json.RawMessage
				json.Unmarshal(item, &delta)
				annotations, annotated := delta["annotations"]
				delete(delta, "annotations")
				events = append(events, replayEvent{"step.delta", map[string]any{"index": index, "delta": delta}})
				if annotated {
					events = append(events, replayEvent{"step.delta", map[string]any{"index": index, "delta": map[string]any{"type": "text_annotation_delta", "annotations": annotations}}})
				}
			}
		case "thought":
			var summary []json.RawMessage
			json.Unmarshal(step["summary"], &summary)
			events = append(events, replayEvent{"step.start", map[string]any{"index": index, "step": map[string]any{"type": kind, "summary": []any{}}}})
			for _, item := range summary {
				events = append(events, replayEvent{"step.delta", map[string]any{"index": index, "delta": map[string]any{"type": "thought_summary", "content": item}}})
			}
			if signature, ok := step["signature"]; ok {
				events = append(events, replayEvent{"step.delta", map[string]any{"index": index, "delta": map[string]any{"type": "thought_signature", "signature": signature}}})
			}
		default:
			events = append(events, replayEvent{"step.start", map[string]any{"index": index, "step": step}})
		}
		events = append(events, replayEvent{"step.stop", map[string]any{"index": index}})
	}
	events = append(events, replayEvent{"interaction.completed", map[string]any{"interaction": fields}})
	start := 0
	if lastEventID != "" {
		sequence, err := strconv.Atoi(lastEventID)
		if err != nil || sequence < 1 || sequence > len(events) {
			writeGeminiError(w, http.StatusBadRequest, "INVALID_ARGUMENT", fmt.Sprintf("last_event_id %q was not found", lastEventID))
			return
		}
		start = sequence
	}
	if err := streamHeaders(w); err != nil {
		return
	}
	for sequence := start; sequence < len(events); sequence++ {
		event := events[sequence]
		event.value["event_type"] = event.kind
		event.value["event_id"] = strconv.Itoa(sequence + 1)
		if err := writeSSE(w, event.kind, event.value); err != nil {
			return
		}
	}
}

// writeInteractionNotFound 返回交互不存在的 404
func writeInteractionNotFound(w http.ResponseWriter, id string) {
	writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", fmt.Sprintf("interaction %q was not found", id))
}
