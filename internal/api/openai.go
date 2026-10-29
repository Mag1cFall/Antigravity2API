package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

type chatRequest struct {
	Model               string            `json:"model"`
	Messages            []chatMessage     `json:"messages"`
	Stream              bool              `json:"stream"`
	StreamOptions       chatStreamOptions `json:"stream_options"`
	Tools               []openAITool      `json:"tools"`
	ToolChoice          json.RawMessage   `json:"tool_choice"`
	Temperature         *float64          `json:"temperature"`
	TopP                *float64          `json:"top_p"`
	TopK                *int              `json:"top_k"`
	MaxTokens           *int64            `json:"max_tokens"`
	MaxCompletionTokens *int64            `json:"max_completion_tokens"`
	N                   *int64            `json:"n"`
	ParallelToolCalls   *bool             `json:"parallel_tool_calls"`
	Stop                json.RawMessage   `json:"stop"`
	ResponseFormat      json.RawMessage   `json:"response_format"`
	ReasoningEffort     string            `json:"reasoning_effort"`
	Reasoning           json.RawMessage   `json:"reasoning"`
	Seed                *int64            `json:"seed"`
	PresencePenalty     *float64          `json:"presence_penalty"`
	FrequencyPenalty    *float64          `json:"frequency_penalty"`
	Audio               json.RawMessage   `json:"audio"`
	Modalities          []string          `json:"modalities"`
	WebSearchOptions    json.RawMessage   `json:"web_search_options"`
	Functions           []json.RawMessage `json:"functions"`
	FunctionCall        json.RawMessage   `json:"function_call"`
	PromptCacheKey      string            `json:"prompt_cache_key"`
	User                string            `json:"user"`
	ExtraBody           chatExtraBody     `json:"extra_body"`
}

// chatExtraBody 为 Gemini OpenAI 兼容层的 extra_body 扩展
type chatExtraBody struct {
	Google struct {
		ThinkingConfig *struct {
			IncludeThoughts *bool  `json:"include_thoughts"`
			ThinkingLevel   string `json:"thinking_level"`
			ThinkingBudget  *int64 `json:"thinking_budget"`
		} `json:"thinking_config"`
	} `json:"google"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role             string           `json:"role"`
	Content          json.RawMessage  `json:"content"`
	ReasoningContent string           `json:"reasoning_content"`
	Name             string           `json:"name"`
	ToolCallID       string           `json:"tool_call_id"`
	ToolCalls        []openAIToolCall `json:"tool_calls"`
	FunctionCall     *struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function_call"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	Custom struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	} `json:"custom"`
	ExtraContent struct {
		Google struct {
			ThoughtSignature string `json:"thought_signature"`
		} `json:"google"`
	} `json:"extra_content"`
}

type openAITool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		Strict      *bool           `json:"strict"`
	} `json:"function"`
	Custom struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Format      json.RawMessage `json:"format"`
	} `json:"custom"`
	legacy bool
}

// openAIModelObject 投影列表与单模型共用的公开字段
func openAIModelObject(model antigravity.Model) map[string]any {
	item := map[string]any{
		"id": model.ID, "object": "model", "created": 0, "owned_by": "google",
		"name": model.Name, "description": model.Description,
		"supported_generation_methods": model.Methods,
		"input_token_limit":            model.InputTokenLimit, "output_token_limit": model.OutputTokenLimit,
	}
	if len(model.Capabilities) > 0 {
		item["capabilities"] = model.Capabilities
	}
	if len(model.ThinkingLevels) > 0 {
		item["thinking_levels"], item["default_thinking_level"] = model.ThinkingLevels, model.DefaultThinkingLevel
	}
	return item
}

// lookupPublicModel 在实时目录中按 ID 查找模型，带档位后缀的目录 ID 返回所属族条目
func lookupPublicModel(models []antigravity.Model, id string) (antigravity.Model, bool) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "models/")
	if id == "" {
		return antigravity.Model{}, false
	}
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	for _, model := range models {
		if level, ok := strings.CutPrefix(id, model.ID+"-"); ok && slices.Contains(model.ThinkingLevels, level) {
			return model, true
		}
	}
	return antigravity.Model{}, false
}

func (s *server) handleOpenAIModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			if r.Header.Get("Anthropic-Version") != "" {
				writeAnthropicRequestError(w, err)
			} else {
				writeOpenAIRequestError(w, err)
			}
		}
		return
	}
	if r.Header.Get("Anthropic-Version") != "" {
		writeAnthropicModels(w, r, models)
		return
	}
	data := make([]map[string]any, 0, len(models))
	for _, model := range models {
		data = append(data, openAIModelObject(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

// handleOpenAIModel 返回实时目录中的单模型或协议化错误
func (s *server) handleOpenAIModel(w http.ResponseWriter, r *http.Request) {
	models, err := s.service.Models(r.Context())
	if err != nil {
		if shouldWriteRequestError(r, err) {
			if r.Header.Get("Anthropic-Version") != "" {
				writeAnthropicRequestError(w, err)
			} else {
				writeOpenAIRequestError(w, err)
			}
		}
		return
	}
	model, ok := lookupPublicModel(models, r.PathValue("model"))
	if !ok {
		message := fmt.Sprintf("model %q is unavailable", r.PathValue("model"))
		if r.Header.Get("Anthropic-Version") != "" {
			writeAnthropicError(w, http.StatusNotFound, "not_found_error", message)
		} else {
			writeOpenAIError(w, http.StatusNotFound, "model_not_found", message)
		}
		return
	}
	if r.Header.Get("Anthropic-Version") != "" {
		writeJSON(w, http.StatusOK, anthropicModelObject(model))
	} else {
		writeJSON(w, http.StatusOK, openAIModelObject(model))
	}
}

func (s *server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var request chatRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	if err := request.mergeLegacyFunctions(); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	requestID := newID("chatcmpl")
	generateRequest, err := request.toGenerateRequest(requestID)
	if err == nil {
		err = inlineRemoteMedia(r.Context(), generateRequest.Contents)
	}
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	generateRequest.Session = clientSession(r, r.Header.Get("session_id"), r.Header.Get("conversation_id"), request.PromptCacheKey, request.User)
	count := 1
	if request.N != nil {
		count = int(*request.N)
	}
	choices := s.startChatChoices(r.Context(), generateRequest, count, request.Stream)
	defer choices.cancel()
	if err := choices.err; err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	created := time.Now().Unix()
	if request.Stream {
		s.streamChatCompletion(w, r, request, requestID, created, choices)
		return
	}
	results, err := choices.run(func(_ int, events <-chan antigravity.Event) (generationResult, error) {
		return consumeEvents(choices.ctx, events, nil)
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	media := s.mediaOutputFor(r)
	if err := media.saveResults(results...); err != nil {
		writeOpenAIRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildChatCompletion(requestID, created, request.Model, results, request.Tools, media))
}

// mergeLegacyFunctions 把旧式 functions 与 function_call 并入 tools 与 tool_choice，旧式函数每轮最多一个调用
func (request *chatRequest) mergeLegacyFunctions() error {
	for _, raw := range request.Functions {
		tool := openAITool{Type: "function", legacy: true}
		if err := json.Unmarshal(raw, &tool.Function); err != nil {
			return fmt.Errorf("functions: %w", err)
		}
		request.Tools = append(request.Tools, tool)
	}
	if len(request.ToolChoice) == 0 && len(request.FunctionCall) > 0 {
		request.ToolChoice = request.FunctionCall
		var call struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(request.FunctionCall, &call) == nil && call.Name != "" {
			request.ToolChoice, _ = json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": call.Name}})
		}
	}
	if len(request.Functions) > 0 {
		single := false
		request.ParallelToolCalls = &single
	}
	return nil
}

// toGenerateRequest 转换 Chat 请求：首部连续的 system、developer 消息合并为系统提示，对话中的 system、developer 消息作为 system-reminder 并入下一个 user 消息，系统消息中的媒体移到首个 user 消息
func (request chatRequest) toGenerateRequest(id string) (antigravity.GenerateRequest, error) {
	if outputsAudio(request.Modalities) || rawJSONConfigured(request.Audio) {
		return antigravity.GenerateRequest{}, fmt.Errorf("audio output is not supported: no model in the catalog generates audio")
	}
	var system []string
	var systemMedia, reminders []antigravity.Part
	contents := make([]antigravity.Content, 0, len(request.Messages))
	for _, message := range request.Messages {
		if message.Role == "system" || message.Role == "developer" {
			parts, err := openAIContentParts(message.Content)
			if err != nil {
				return antigravity.GenerateRequest{}, fmt.Errorf("%s message: %w", message.Role, err)
			}
			var text strings.Builder
			for _, part := range parts {
				if part.Text == "" {
					systemMedia = append(systemMedia, part)
				}
				text.WriteString(part.Text)
			}
			value := text.String()
			if message.Name != "" && value != "" {
				value = message.Name + ": " + value
			}
			switch {
			case value == "":
			case len(contents) == 0:
				system = append(system, value)
			default:
				reminders = append(reminders, systemReminder(value))
			}
			continue
		}
		content, err := chatMessageContent(message)
		if err != nil {
			return antigravity.GenerateRequest{}, err
		}
		if len(content.Parts) == 0 {
			continue
		}
		contents, reminders = appendWithReminders(contents, content, reminders)
	}
	if len(reminders) > 0 {
		contents = append(contents, antigravity.Content{Role: antigravity.RoleUser, Parts: reminders})
	}
	contents = prependSystemMedia(contents, systemMedia)
	tools, err := mapOpenAITools(request.Tools, request.ToolChoice)
	if err != nil {
		return antigravity.GenerateRequest{}, err
	}
	if rawJSONConfigured(request.WebSearchOptions) {
		var options struct {
			SearchContextSize string          `json:"search_context_size"`
			UserLocation      json.RawMessage `json:"user_location"`
		}
		if err := json.Unmarshal(request.WebSearchOptions, &options); err != nil {
			return antigravity.GenerateRequest{}, fmt.Errorf("web_search_options: %w", err)
		}
		if tools.Search, err = searchOptions(options.SearchContextSize, options.UserLocation, nil, nil); err != nil {
			return antigravity.GenerateRequest{}, err
		}
		tools.GoogleSearch = json.RawMessage(`{}`)
	}
	tools.ToolConfig.ParallelCalls = request.ParallelToolCalls
	config, err := request.generationConfig()
	if err != nil {
		return antigravity.GenerateRequest{}, err
	}
	return antigravity.GenerateRequest{
		ID:       id,
		Model:    request.Model,
		System:   strings.Join(system, "\n"),
		Contents: contents,
		Config:   config,
		Tools:    tools,
	}, nil
}

func chatMessageContent(message chatMessage) (antigravity.Content, error) {
	role, err := openAIRole(message.Role)
	if err != nil {
		return antigravity.Content{}, err
	}
	if role == antigravity.RoleTool {
		content, media, err := functionResultContent(message.Content, openAIContentParts)
		if err != nil {
			return antigravity.Content{}, fmt.Errorf("tool message content: %w", err)
		}
		return antigravity.Content{Role: role, Parts: []antigravity.Part{{FunctionResult: &antigravity.FunctionResult{
			ID:      message.ToolCallID,
			Name:    message.Name,
			Content: content,
			Parts:   media,
		}}}}, nil
	}
	parts, err := openAIContentParts(message.Content)
	if err != nil {
		return antigravity.Content{}, fmt.Errorf("%s message content: %w", message.Role, err)
	}
	if message.Name != "" {
		if index := slices.IndexFunc(parts, func(part antigravity.Part) bool { return part.Text != "" }); index >= 0 {
			parts[index].Text = message.Name + ": " + parts[index].Text
		}
	}
	if role == antigravity.RoleAssistant && message.ReasoningContent != "" {
		parts = append([]antigravity.Part{{Text: message.ReasoningContent, Thought: true}}, parts...)
	}
	for _, call := range message.ToolCalls {
		functionCall := antigravity.FunctionCall{ID: call.ID, ThoughtSignature: call.ExtraContent.Google.ThoughtSignature}
		switch call.Type {
		case "", "function":
			functionCall.Name = call.Function.Name
			functionCall.Arguments = functionCallArguments(call.Function.Arguments)
		case "custom":
			functionCall.Name = call.Custom.Name
			functionCall.Arguments = customToolArguments(call.Custom.Input)
		default:
			return antigravity.Content{}, fmt.Errorf("unsupported tool call type %q", call.Type)
		}
		parts = append(parts, antigravity.Part{FunctionCall: &functionCall})
	}
	if message.FunctionCall != nil {
		parts = append(parts, antigravity.Part{FunctionCall: &antigravity.FunctionCall{
			Name: message.FunctionCall.Name, Arguments: functionCallArguments(message.FunctionCall.Arguments),
		}})
	}
	return antigravity.Content{Role: role, Parts: parts}, nil
}

func openAIRole(role string) (antigravity.Role, error) {
	switch role {
	case "user":
		return antigravity.RoleUser, nil
	case "assistant":
		return antigravity.RoleAssistant, nil
	case "tool", "function":
		return antigravity.RoleTool, nil
	default:
		return "", fmt.Errorf("unsupported message role %q", role)
	}
}

// outputsAudio 判断输出模态是否包含音频
func outputsAudio(modalities []string) bool {
	return slices.ContainsFunc(modalities, func(modality string) bool { return strings.EqualFold(modality, "audio") })
}

// prependSystemMedia 把系统提示中的媒体移到首个 user 消息开头，没有 user 消息时作为首条 user 消息
func prependSystemMedia(contents []antigravity.Content, media []antigravity.Part) []antigravity.Content {
	if len(media) == 0 {
		return contents
	}
	index := slices.IndexFunc(contents, func(content antigravity.Content) bool { return content.Role == antigravity.RoleUser })
	if index < 0 {
		return append([]antigravity.Content{{Role: antigravity.RoleUser, Parts: media}}, contents...)
	}
	contents[index].Parts = append(slices.Clone(media), contents[index].Parts...)
	return contents
}

// openAIContentParts 转换文本与媒体并省略空文本占位
func openAIContentParts(raw json.RawMessage) ([]antigravity.Part, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []antigravity.Part{{Text: text}}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("content must be a string or array")
	}
	parts := make([]antigravity.Part, 0, len(blocks))
	for _, block := range blocks {
		part, err := openAIContentPart(block)
		if err != nil {
			return nil, err
		}
		if part.Text == "" && part.InlineData == nil && part.File == nil {
			continue
		}
		parts = append(parts, part)
	}
	return parts, nil
}

func openAIContentPart(raw json.RawMessage) (antigravity.Part, error) {
	var block struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Refusal  string          `json:"refusal"`
		ImageURL json.RawMessage `json:"image_url"`
		FileID   string          `json:"file_id"`
		Filename string          `json:"filename"`
		FileData string          `json:"file_data"`
		FileURL  string          `json:"file_url"`
		File     *struct {
			FileID   string `json:"file_id"`
			Filename string `json:"filename"`
			FileData string `json:"file_data"`
			FileURL  string `json:"file_url"`
		} `json:"file"`
		InputAudio *struct {
			Data   string `json:"data"`
			Format string `json:"format"`
		} `json:"input_audio"`
		VideoURL json.RawMessage `json:"video_url"`
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return antigravity.Part{}, err
	}
	switch block.Type {
	case "text", "input_text", "output_text":
		return antigravity.Part{Text: block.Text}, nil
	case "refusal":
		return antigravity.Part{Text: block.Refusal}, nil
	case "image_url", "input_image":
		if !rawJSONConfigured(block.ImageURL) && block.FileID != "" {
			return antigravity.Part{File: &antigravity.FileRef{ID: block.FileID}}, nil
		}
		url, err := imageURLString(block.ImageURL)
		if err != nil {
			return antigravity.Part{}, err
		}
		return mediaURLPart(url, "", "")
	case "file", "input_file":
		if block.File != nil {
			block.FileID, block.Filename, block.FileData, block.FileURL = block.File.FileID, block.File.Filename, block.File.FileData, block.File.FileURL
		}
		if block.FileData == "" && block.FileURL != "" {
			return mediaURLPart(block.FileURL, block.Filename, "")
		}
		if block.FileData == "" && block.FileID != "" {
			return antigravity.Part{File: &antigravity.FileRef{ID: block.FileID, Name: block.Filename}}, nil
		}
		if block.FileData == "" {
			return antigravity.Part{}, fmt.Errorf("%s requires file_data, file_url or file_id", block.Type)
		}
		return fileDataPart(block.FileData, block.Filename)
	case "input_audio":
		if block.InputAudio == nil {
			return antigravity.Part{}, fmt.Errorf("input_audio is required")
		}
		data, err := decodeBase64Flexible(block.InputAudio.Data)
		if err != nil {
			return antigravity.Part{}, fmt.Errorf("input_audio.data: %w", err)
		}
		return antigravity.Part{InlineData: &antigravity.Blob{MIME: audioMIME(block.InputAudio.Format), Data: data}}, nil
	case "video_url", "input_video":
		url, err := imageURLString(block.VideoURL)
		if err != nil {
			return antigravity.Part{}, fmt.Errorf("video_url must contain url")
		}
		return mediaURLPart(url, "", "")
	default:
		return antigravity.Part{}, fmt.Errorf("unsupported content type %q", block.Type)
	}
}

func imageURLString(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	var object struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &object); err != nil || object.URL == "" {
		return "", fmt.Errorf("image_url must contain url")
	}
	return object.URL, nil
}

func mapOpenAITools(tools []openAITool, choice json.RawMessage) (antigravity.Tools, error) {
	var mapped antigravity.Tools
	for _, tool := range tools {
		switch tool.Type {
		case "function":
			if tool.Function.Name == "" {
				return antigravity.Tools{}, fmt.Errorf("function tool name is required")
			}
			parameters := tool.Function.Parameters
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			mapped.Functions = append(mapped.Functions, antigravity.FunctionDeclaration{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  parameters,
				Strict:      tool.Function.Strict != nil && *tool.Function.Strict,
			})
		case "custom":
			if tool.Custom.Name == "" {
				return antigravity.Tools{}, fmt.Errorf("custom tool name is required")
			}
			mapped.Functions = append(mapped.Functions, customToolDeclaration(tool.Custom.Name, tool.Custom.Description, tool.Custom.Format))
		default:
			return antigravity.Tools{}, fmt.Errorf("unsupported tool type %q", tool.Type)
		}
	}
	config, allowed, err := openAIToolChoice(choice)
	if err != nil {
		return antigravity.Tools{}, err
	}
	if allowed != nil {
		mapped = restrictAllowedTools(mapped, allowed)
	}
	mapped.ToolConfig = config
	if mapped.Functions, err = upstreamFunctions(mapped.Functions); err != nil {
		return antigravity.Tools{}, err
	}
	return mapped, nil
}

// openAIToolChoice 转换 tool_choice，类型为 allowed_tools 时另外返回允许的工具引用
func openAIToolChoice(raw json.RawMessage) (antigravity.ToolConfig, []toolReference, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return antigravity.ToolConfig{Mode: "auto"}, nil, nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		switch mode {
		case "auto", "none", "required":
			return antigravity.ToolConfig{Mode: mode}, nil, nil
		default:
			return antigravity.ToolConfig{}, nil, fmt.Errorf("unsupported tool_choice %q", mode)
		}
	}
	var object struct {
		toolReference
		Mode         string          `json:"mode"`
		Tools        []toolReference `json:"tools"`
		AllowedTools *struct {
			Mode  string          `json:"mode"`
			Tools []toolReference `json:"tools"`
		} `json:"allowed_tools"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return antigravity.ToolConfig{}, nil, fmt.Errorf("invalid tool_choice: %w", err)
	}
	switch object.Type {
	case "allowed_tools":
		if object.AllowedTools != nil {
			object.Mode, object.Tools = object.AllowedTools.Mode, object.AllowedTools.Tools
		}
		if object.Mode != "auto" && object.Mode != "required" {
			return antigravity.ToolConfig{}, nil, fmt.Errorf("allowed_tools mode must be auto or required")
		}
		config := antigravity.ToolConfig{Mode: object.Mode}
		for _, tool := range object.Tools {
			if name := tool.functionName(); name != "" {
				config.AllowedFunctionNames = append(config.AllowedFunctionNames, name)
			}
		}
		return config, append([]toolReference{}, object.Tools...), nil
	case "web_search_preview", "web_search_preview_2025_03_11", "code_interpreter", "file_search", "computer", "computer_use",
		"computer_use_preview", "mcp", "programmatic_tool_calling", "tool_search":
		return antigravity.ToolConfig{Mode: "auto"}, nil, nil
	case "image_generation":
		return antigravity.ToolConfig{Mode: "required", AllowedFunctionNames: []string{imageToolName}}, nil, nil
	}
	if name := object.functionName(); name != "" {
		return antigravity.ToolConfig{Mode: "required", AllowedFunctionNames: []string{name}}, nil, nil
	}
	if object.Type == "function" || object.Type == "custom" {
		return antigravity.ToolConfig{}, nil, fmt.Errorf("named tool_choice requires a name")
	}
	return antigravity.ToolConfig{}, nil, fmt.Errorf("unsupported tool_choice type %q", object.Type)
}

// restrictAllowedTools 只保留 allowed_tools 引用的函数
func restrictAllowedTools(tools antigravity.Tools, allowed []toolReference) antigravity.Tools {
	names := make(map[string]bool)
	for _, reference := range allowed {
		if name := reference.functionName(); name != "" {
			names[name] = true
		}
	}
	tools.Functions = slices.DeleteFunc(tools.Functions, func(declaration antigravity.FunctionDeclaration) bool { return !names[declaration.Name] })
	return tools
}

// toolReference 是 tool_choice 与 allowed_tools 中对单个工具的引用
type toolReference struct {
	Type      string `json:"type"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Function  *struct {
		Name string `json:"name"`
	} `json:"function"`
	Custom *struct {
		Name string `json:"name"`
	} `json:"custom"`
}

// functionName 返回引用对应的声明函数名，客户端执行工具使用固定函数名，托管工具返回空串
func (reference toolReference) functionName() string {
	switch reference.Type {
	case "function", "custom":
		name := reference.Name
		if reference.Function != nil {
			name = reference.Function.Name
		}
		if reference.Custom != nil {
			name = reference.Custom.Name
		}
		if reference.Namespace != "" && name != "" {
			name = reference.Namespace + "." + name
		}
		return name
	case "local_shell", "shell", "apply_patch":
		return reference.Type
	}
	return ""
}

func (request chatRequest) generationConfig() (antigravity.GenerationConfig, error) {
	if request.N != nil && (*request.N < 1 || *request.N > maxChatChoices) {
		return antigravity.GenerationConfig{}, fmt.Errorf("n must be between 1 and %d", maxChatChoices)
	}
	config := antigravity.GenerationConfig{
		Temperature:      request.Temperature,
		TopP:             request.TopP,
		TopK:             request.TopK,
		PresencePenalty:  request.PresencePenalty,
		FrequencyPenalty: request.FrequencyPenalty,
		ReasoningEffort:  request.ReasoningEffort,
		Seed:             request.Seed,
	}
	if request.MaxCompletionTokens != nil {
		config.MaxOutputTokens = request.MaxCompletionTokens
	} else {
		config.MaxOutputTokens = request.MaxTokens
	}
	stop, err := decodeStopSequences(request.Stop)
	if err != nil {
		return config, err
	}
	config.StopSequences = stop
	if len(request.Reasoning) > 0 && string(request.Reasoning) != "null" {
		var reasoning struct {
			Effort  string `json:"effort"`
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(request.Reasoning, &reasoning); err != nil {
			return config, fmt.Errorf("invalid reasoning: %w", err)
		}
		if reasoning.Effort != "" {
			config.ReasoningEffort = reasoning.Effort
		}
		config.HideThinking = strings.EqualFold(reasoning.Summary, "none")
	}
	if thinking := request.ExtraBody.Google.ThinkingConfig; thinking != nil {
		if thinking.ThinkingLevel != "" {
			config.ReasoningEffort = thinking.ThinkingLevel
		} else if thinking.ThinkingBudget != nil {
			config.ReasoningEffort, config.ThinkingBudget = "", thinking.ThinkingBudget
		}
		if thinking.IncludeThoughts != nil {
			config.HideThinking = !*thinking.IncludeThoughts
		}
	}
	config.HideThinking = config.HideThinking || strings.EqualFold(config.ReasoningEffort, "none")
	if len(request.ResponseFormat) > 0 && string(request.ResponseFormat) != "null" {
		var format struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if err := json.Unmarshal(request.ResponseFormat, &format); err != nil {
			return config, fmt.Errorf("invalid response_format: %w", err)
		}
		switch format.Type {
		case "json_object":
			config.ResponseMIMEType = "application/json"
		case "json_schema":
			config.ResponseMIMEType = "application/json"
			config.ResponseSchema = format.JSONSchema.Schema
		case "", "text":
		default:
			return config, fmt.Errorf("unsupported response_format type %q", format.Type)
		}
	}
	return config, nil
}

func decodeStopSequences(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return normalizeStopSequences([]string{single}), nil
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		return nil, fmt.Errorf("stop must be a string or string array")
	}
	return normalizeStopSequences(multiple), nil
}

// normalizeStopSequences 删除不会形成停止条件的空字符串
func normalizeStopSequences(values []string) []string {
	var normalized []string
	for _, value := range values {
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}

func buildChatCompletion(id string, created int64, model string, results []generationResult, tools []openAITool, media mediaOutput) map[string]any {
	choices := make([]any, 0, len(results))
	response := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   model,
	}
	for index := range results {
		choices = append(choices, chatCompletionChoice(index, results[index], tools, media))
		if results[index].providerModel != "" && response["provider_model"] == nil {
			response["provider_model"] = results[index].providerModel
		}
	}
	response["choices"] = choices
	if usage := sumUsage(results); usage != nil {
		response["usage"] = openAIUsage(usage)
	}
	return response
}

// chatCompletionChoice 构造非流式响应中第 index 个 choice
func chatCompletionChoice(index int, result generationResult, tools []openAITool, media mediaOutput) map[string]any {
	content := any(result.text.String())
	if len(result.media) > 0 {
		content = renderedText(result.events, media)
	}
	if result.text.Len() == 0 && len(result.media) == 0 && len(result.toolCalls) > 0 {
		content = nil
	}
	message := map[string]any{"role": "assistant", "content": content}
	if result.reasoning.Len() > 0 {
		message["reasoning_content"] = result.reasoning.String()
	}
	if result.grounding != nil {
		message["annotations"] = chatAnnotations(result.text.String(), result.grounding)
	}
	if len(result.toolCalls) > 0 && legacyFunctionTools(tools) {
		message["function_call"] = legacyFunctionCall(result.toolCalls[0])
	} else if len(result.toolCalls) > 0 {
		message["tool_calls"] = openAIToolCallOutput(result.toolCalls, tools)
	}
	return map[string]any{
		"index":         index,
		"message":       message,
		"logprobs":      nil,
		"finish_reason": chatFinishReason(result, tools),
	}
}

func (s *server) streamChatCompletion(w http.ResponseWriter, r *http.Request, request chatRequest, id string, created int64, choices *chatChoices) {
	if err := streamHeaders(w); err != nil {
		return
	}
	var writing sync.Mutex
	write := func(index int, delta map[string]any, finish *string) error {
		writing.Lock()
		defer writing.Unlock()
		return writeChatChunk(w, id, created, request.Model, index, delta, finish, request.StreamOptions.IncludeUsage)
	}
	for index := range choices.events {
		if err := write(index, map[string]any{"role": "assistant", "content": ""}, nil); err != nil {
			return
		}
	}
	heartbeat := func() error {
		writing.Lock()
		defer writing.Unlock()
		return writeSSEHeartbeat(w)
	}
	media := s.mediaOutputFor(r)
	results, err := choices.run(func(index int, events <-chan antigravity.Event) (generationResult, error) {
		choice := &chatStreamChoice{tools: request.Tools, index: index, write: write, media: media}
		result, err := consumeStreamEvents(choices.ctx, events, choice.emit, heartbeat)
		if err != nil {
			return result, err
		}
		finish := chatFinishReason(result, request.Tools)
		return result, write(index, map[string]any{}, &finish)
	})
	if err != nil {
		writeOpenAIStreamError(w, r, err)
		return
	}
	if usage := sumUsage(results); request.StreamOptions.IncludeUsage && usage != nil {
		chunk := map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": created,
			"model":   request.Model,
			"choices": []any{},
			"usage":   openAIUsage(usage),
		}
		if err := writeSSE(w, "", chunk); err != nil {
			return
		}
	}
	_ = writeSSEText(w, "[DONE]")
}

// chatStreamChoice 把一个 choice 的事件写成带 index 的 Chat chunk
type chatStreamChoice struct {
	tools     []openAITool
	index     int
	write     func(int, map[string]any, *string) error
	toolIndex int
	// openLine 表示已写出的正文末尾没有换行
	openLine bool
	text     strings.Builder
	media    mediaOutput
}

// emit 写出一个上游事件对应的 delta
func (choice *chatStreamChoice) emit(event antigravity.Event) error {
	switch event.Kind {
	case antigravity.EventText:
		choice.openLine = !strings.HasSuffix(event.Text, "\n")
		choice.text.WriteString(event.Text)
		return choice.write(choice.index, map[string]any{"content": event.Text}, nil)
	case antigravity.EventGrounding:
		return choice.write(choice.index, map[string]any{"annotations": chatAnnotations(choice.text.String(), event.Grounding)}, nil)
	case antigravity.EventReasoning:
		return choice.write(choice.index, map[string]any{"reasoning_content": event.Text}, nil)
	case antigravity.EventMedia:
		if err := choice.media.save(*event.Media); err != nil {
			return err
		}
		content := choice.media.markdown(*event.Media) + "\n"
		if choice.openLine {
			content = "\n" + content
		}
		choice.openLine = false
		return choice.write(choice.index, map[string]any{"content": content}, nil)
	case antigravity.EventToolCall:
		if event.ToolCall == nil {
			return nil
		}
		call := event.ToolCall
		if legacyFunctionTools(choice.tools) {
			return choice.write(choice.index, map[string]any{"function_call": legacyFunctionCall(*call)}, nil)
		}
		toolCall := openAIToolCallItem(*call, choice.tools)
		toolCall["index"] = choice.toolIndex
		choice.toolIndex++
		return choice.write(choice.index, map[string]any{"tool_calls": []any{toolCall}}, nil)
	}
	return nil
}

func writeChatChunk(w http.ResponseWriter, id string, created int64, model string, index int, delta map[string]any, finish *string, includeUsage bool) error {
	choice := map[string]any{
		"index":         index,
		"delta":         delta,
		"finish_reason": finish,
	}
	chunk := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{choice},
	}
	if includeUsage {
		chunk["usage"] = nil
	}
	return writeSSE(w, "", chunk)
}

func openAIFinishReason(reason string, hasTools bool) string {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "max_tokens", "max_output_tokens", "length":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		if contentFilterFinishReason(reason) {
			return "content_filter"
		}
		if hasTools {
			return "tool_calls"
		}
		return "stop"
	}
}

// chatFinishReason 返回 choice 的 finish_reason，旧式函数调用为 function_call
func chatFinishReason(result generationResult, tools []openAITool) string {
	finish := openAIFinishReason(result.finishReason, len(result.toolCalls) > 0)
	if finish == "tool_calls" && legacyFunctionTools(tools) {
		return "function_call"
	}
	return finish
}

// legacyFunctionTools 判断工具是否来自旧式 functions
func legacyFunctionTools(tools []openAITool) bool {
	return slices.ContainsFunc(tools, func(tool openAITool) bool { return tool.legacy })
}

// legacyFunctionCall 把函数调用投影为旧式 function_call
func legacyFunctionCall(call antigravity.FunctionCall) map[string]any {
	return map[string]any{"name": call.Name, "arguments": string(call.Arguments)}
}

// contentFilterFinishReason 判断上游结束原因是否表示内容策略拦截
func contentFilterFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "safety", "content_filter", "blocked", "recitation", "blocklist", "prohibited_content", "spii",
		"image_safety", "image_prohibited_content", "image_recitation":
		return true
	}
	return false
}

func openAIToolCallOutput(calls []antigravity.FunctionCall, tools []openAITool) []map[string]any {
	output := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		output = append(output, openAIToolCallItem(call, tools))
	}
	return output
}

// openAIToolCallItem 把函数调用投影为 Chat tool_calls 项，custom 工具使用 custom 形状
func openAIToolCallItem(call antigravity.FunctionCall, tools []openAITool) map[string]any {
	item := map[string]any{"id": call.ID, "type": "function"}
	if chatCustomTool(tools, call.Name) {
		item["type"] = "custom"
		item["custom"] = map[string]any{"name": call.Name, "input": customToolInput(call.Arguments)}
	} else {
		item["function"] = map[string]any{"name": call.Name, "arguments": string(call.Arguments)}
	}
	if call.ThoughtSignature != "" {
		item["extra_content"] = openAIGoogleThoughtSignature(call.ThoughtSignature)
	}
	return item
}

func openAIGoogleThoughtSignature(signature string) map[string]any {
	return map[string]any{"google": map[string]string{"thought_signature": signature}}
}

func openAIUsage(usage *antigravity.Usage) map[string]any {
	return map[string]any{
		"prompt_tokens":     inputTokens(usage),
		"completion_tokens": outputTokens(usage),
		"total_tokens":      usage.TotalTokens,
		"prompt_tokens_details": map[string]any{
			"cached_tokens": usage.CachedTokens,
		},
		"completion_tokens_details": map[string]any{
			"reasoning_tokens": usage.ReasoningTokens,
		},
	}
}
