package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

type anthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []anthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system"`
	MaxTokens     *int64             `json:"max_tokens"`
	StopSequences []string           `json:"stop_sequences"`
	Stream        bool               `json:"stream"`
	Temperature   *float64           `json:"temperature"`
	TopP          *float64           `json:"top_p"`
	TopK          *int               `json:"top_k"`
	Tools         []anthropicTool    `json:"tools"`
	ToolChoice    json.RawMessage    `json:"tool_choice"`
	Thinking      *struct {
		Type         string `json:"type"`
		BudgetTokens *int64 `json:"budget_tokens"`
		Display      string `json:"display"`
	} `json:"thinking"`
	OutputConfig *struct {
		Effort string `json:"effort"`
		Format *struct {
			Type   string          `json:"type"`
			Schema json.RawMessage `json:"schema"`
		} `json:"format"`
	} `json:"output_config"`
	Metadata *struct {
		UserID string `json:"user_id"`
	} `json:"metadata"`
}

// legacySession 匹配 Claude Code 旧式 user_id 末尾的 session 段
var legacySession = regexp.MustCompile(`_session_([0-9a-fA-F-]+)$`)

// session 返回 metadata.user_id 中的会话标识：JSON 形式取 session_id，旧式取 _session_ 之后的段，其余取原值
func (request anthropicRequest) session() string {
	if request.Metadata == nil {
		return ""
	}
	userID := request.Metadata.UserID
	var structured struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(userID), &structured) == nil && structured.SessionID != "" {
		return structured.SessionID
	}
	if match := legacySession.FindStringSubmatch(userID); match != nil {
		return match[1]
	}
	return userID
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicTool struct {
	Type        string                     `json:"type"`
	Name        string                     `json:"name"`
	Description string                     `json:"description"`
	InputSchema json.RawMessage            `json:"input_schema"`
	Options     map[string]json.RawMessage `json:"-"`
}

func (tool *anthropicTool) UnmarshalJSON(data []byte) error {
	type knownTool anthropicTool
	var known knownTool
	if err := json.Unmarshal(data, &known); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	delete(fields, "type")
	delete(fields, "name")
	delete(fields, "description")
	delete(fields, "input_schema")
	*tool = anthropicTool(known)
	tool.Options = fields
	return nil
}

type anthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	Signature *string         `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	Citations []any           `json:"citations,omitempty"`
}

func (s *server) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	var request anthropicRequest
	if err := decodeJSON(r, &request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if request.Model == "" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	messageID := newID("msg")
	generateRequest, err := s.anthropicGenerateRequest(r, request, messageID)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	events, err := s.service.Generate(r.Context(), generateRequest)
	var first antigravity.Event
	if err == nil && request.Stream {
		events, first, err = awaitStreamStart(r.Context(), events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeAnthropicRequestError(w, err)
		}
		return
	}
	if request.Stream {
		if err := streamHeaders(w); err != nil {
			return
		}
		usage := first.Usage
		if usage == nil {
			tokens, _ := s.service.CountTokens(r.Context(), generateRequest)
			usage = &antigravity.Usage{InputTokens: tokens}
		}
		writer := &anthropicStreamWriter{
			w: w, id: messageID, model: request.Model, usage: usage,
			omitted: request.Thinking != nil && request.Thinking.Display == "omitted",
			search:  searchOnly(generateRequest.Tools),
		}
		if err := writer.start(); err != nil {
			return
		}
		s.streamAnthropic(r, writer, events)
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeAnthropicRequestError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, buildAnthropicResponse(messageID, request.Model, result, request.Thinking != nil && request.Thinking.Display == "omitted"))
}

func (s *server) handleAnthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	var request anthropicRequest
	if err := decodeJSON(r, &request); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if request.Model == "" {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	generateRequest, err := s.anthropicGenerateRequest(r, request, newID("count"))
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	tokens, err := s.service.CountTokens(r.Context(), generateRequest)
	if err != nil {
		writeAnthropicRequestError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"input_tokens": tokens})
}

// anthropicGenerateRequest 转换请求，带上会话标识并下载远程媒体
func (s *server) anthropicGenerateRequest(r *http.Request, request anthropicRequest, id string) (antigravity.GenerateRequest, error) {
	generateRequest, err := request.toGenerateRequest(id)
	if err != nil {
		return antigravity.GenerateRequest{}, err
	}
	generateRequest.Session = clientSession(r, request.session())
	return generateRequest, inlineRemoteMedia(r.Context(), generateRequest.Contents)
}

func (request anthropicRequest) toGenerateRequest(id string) (antigravity.GenerateRequest, error) {
	if request.Thinking != nil {
		switch request.Thinking.Type {
		case "enabled":
			if request.Thinking.BudgetTokens == nil {
				return antigravity.GenerateRequest{}, fmt.Errorf("thinking.budget_tokens is required when thinking.type is enabled")
			}
		case "adaptive":
		case "disabled":
		default:
			return antigravity.GenerateRequest{}, fmt.Errorf("thinking.type must be enabled, adaptive or disabled")
		}
	}
	system, err := anthropicSystemText(request.System)
	if err != nil {
		return antigravity.GenerateRequest{}, err
	}
	contents := make([]antigravity.Content, 0, len(request.Messages))
	var reminders []antigravity.Part
	for _, message := range request.Messages {
		if message.Role == "system" {
			text, err := anthropicSystemText(message.Content)
			if err != nil {
				return antigravity.GenerateRequest{}, fmt.Errorf("system message: %w", err)
			}
			if strings.TrimSpace(text) != "" {
				reminders = append(reminders, systemReminder(text))
			}
			continue
		}
		role, err := anthropicRole(message.Role)
		if err != nil {
			return antigravity.GenerateRequest{}, err
		}
		parts, err := anthropicParts(message.Content)
		if err != nil {
			return antigravity.GenerateRequest{}, fmt.Errorf("%s message: %w", message.Role, err)
		}
		if len(parts) == 0 {
			continue
		}
		contents, reminders = appendWithReminders(contents, antigravity.Content{Role: role, Parts: parts}, reminders)
	}
	if len(reminders) > 0 {
		contents = append(contents, antigravity.Content{Role: antigravity.RoleUser, Parts: reminders})
	}
	tools, err := mapAnthropicTools(request.Tools, request.ToolChoice)
	if err != nil {
		return antigravity.GenerateRequest{}, err
	}
	config := antigravity.GenerationConfig{
		Temperature:     request.Temperature,
		TopP:            request.TopP,
		TopK:            request.TopK,
		MaxOutputTokens: request.MaxTokens,
		StopSequences:   normalizeStopSequences(request.StopSequences),
	}
	if request.Thinking != nil && request.Thinking.Type == "enabled" {
		config.ThinkingBudget = request.Thinking.BudgetTokens
	}
	if request.Thinking == nil || request.Thinking.Type == "disabled" {
		config.HideThinking = true
	}
	if request.Thinking != nil && request.Thinking.Type == "disabled" {
		config.ReasoningEffort = "none"
	}
	if request.OutputConfig != nil {
		config.ReasoningEffort = request.OutputConfig.Effort
		if format := request.OutputConfig.Format; format != nil {
			switch format.Type {
			case "json_schema":
				config.ResponseMIMEType, config.ResponseSchema = "application/json", format.Schema
			default:
				return antigravity.GenerateRequest{}, fmt.Errorf("unsupported output_config.format type %q", format.Type)
			}
		}
	}
	return antigravity.GenerateRequest{
		ID: id, Model: request.Model, System: system, Contents: contents, Config: config, Tools: tools,
	}, nil
}

func anthropicSystemText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("system must be a string or text block array")
	}
	texts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type != "text" {
			return "", fmt.Errorf("unsupported system block type %q", block.Type)
		}
		if block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

func anthropicRole(role string) (antigravity.Role, error) {
	switch role {
	case "user":
		return antigravity.RoleUser, nil
	case "assistant":
		return antigravity.RoleAssistant, nil
	default:
		return "", fmt.Errorf("unsupported message role %q", role)
	}
}

// anthropicParts 转换消息块并保留工具调用的思考签名
func anthropicParts(raw json.RawMessage) ([]antigravity.Part, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" {
			return nil, nil
		}
		return []antigravity.Part{{Text: text}}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("content must be a string or block array")
	}
	parts := make([]antigravity.Part, 0, len(blocks))
	pendingSignature := ""
	flushPendingSignature := func() {
		if pendingSignature == "" {
			return
		}
		parts = append(parts, antigravity.Part{ThoughtSignature: pendingSignature})
		pendingSignature = ""
	}
	for _, rawBlock := range blocks {
		var block struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			Data      string          `json:"data"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
			IsError   bool            `json:"is_error"`
			Signature string          `json:"signature"`
			Thinking  string          `json:"thinking"`
			Source    json.RawMessage `json:"source"`
			Title     string          `json:"title"`
		}
		if err := json.Unmarshal(rawBlock, &block); err != nil {
			return nil, err
		}
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			flushPendingSignature()
			parts = append(parts, antigravity.Part{Text: block.Text})
		case "thinking":
			flushPendingSignature()
			if block.Thinking != "" {
				parts = append(parts, antigravity.Part{Text: block.Thinking, Thought: true})
			}
			pendingSignature = block.Signature
		case "redacted_thinking":
			flushPendingSignature()
			if block.Data == "" {
				return nil, fmt.Errorf("redacted_thinking data is required")
			}
			pendingSignature = block.Data
		case "image", "document":
			flushPendingSignature()
			if !rawJSONConfigured(block.Source) {
				return nil, fmt.Errorf("%s source is required", block.Type)
			}
			var source struct {
				Type      string          `json:"type"`
				MediaType string          `json:"media_type"`
				Data      string          `json:"data"`
				URL       string          `json:"url"`
				FileID    string          `json:"file_id"`
				Content   json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(block.Source, &source); err != nil {
				return nil, fmt.Errorf("%s source: %w", block.Type, err)
			}
			switch source.Type {
			case "base64":
				data, err := decodeBase64Flexible(source.Data)
				if err != nil {
					return nil, fmt.Errorf("%s source data: %w", block.Type, err)
				}
				mediaType, data := normalizeImagePayload(source.MediaType, data)
				parts = append(parts, antigravity.Part{InlineData: &antigravity.Blob{MIME: mediaType, Data: data}})
			case "url":
				part, err := mediaURLPart(source.URL, "", source.MediaType)
				if err != nil {
					return nil, fmt.Errorf("%s source: %w", block.Type, err)
				}
				parts = append(parts, part)
			case "file":
				parts = append(parts, antigravity.Part{File: &antigravity.FileRef{ID: source.FileID}})
			case "text":
				if source.Data != "" {
					parts = append(parts, antigravity.Part{Text: source.Data})
				}
			case "content":
				nested, err := anthropicParts(source.Content)
				if err != nil {
					return nil, fmt.Errorf("%s source content: %w", block.Type, err)
				}
				parts = append(parts, nested...)
			default:
				return nil, fmt.Errorf("unsupported source type %q", source.Type)
			}
		case "tool_use":
			input := block.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			parts = append(parts, antigravity.Part{FunctionCall: &antigravity.FunctionCall{
				ID: block.ID, Name: block.Name, Arguments: input, ThoughtSignature: pendingSignature,
			}})
			pendingSignature = ""
		case "tool_result":
			flushPendingSignature()
			content, media, err := functionResultContent(block.Content, anthropicParts)
			if err != nil {
				return nil, fmt.Errorf("tool_result: %w", err)
			}
			if block.IsError {
				content = json.RawMessage(`{"error":` + string(content) + `}`)
			}
			parts = append(parts, antigravity.Part{FunctionResult: &antigravity.FunctionResult{
				ID: block.ToolUseID, Content: content, Parts: media,
			}})
		case "search_result":
			flushPendingSignature()
			var source string
			_ = json.Unmarshal(block.Source, &source)
			nested, err := anthropicParts(block.Content)
			if err != nil {
				return nil, fmt.Errorf("search_result content: %w", err)
			}
			parts = append(parts, antigravity.Part{Text: "Search result: " + block.Title + "\n" + source})
			parts = append(parts, nested...)
		case "server_tool_use":
			flushPendingSignature()
			parts = append(parts, antigravity.Part{Text: "Server tool call " + block.Name + ": " + string(block.Input)})
		case "web_fetch_tool_result":
			flushPendingSignature()
			var result struct {
				URL       string          `json:"url"`
				ErrorCode string          `json:"error_code"`
				Content   json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(block.Content, &result); err != nil {
				return nil, fmt.Errorf("web_fetch_tool_result content: %w", err)
			}
			if result.ErrorCode != "" {
				parts = append(parts, antigravity.Part{Text: "Web fetch failed: " + result.ErrorCode})
			} else {
				document, err := anthropicParts(json.RawMessage("[" + string(result.Content) + "]"))
				if err != nil {
					return nil, fmt.Errorf("web_fetch_tool_result content: %w", err)
				}
				parts = append(parts, antigravity.Part{Text: "Web fetch: " + result.URL})
				parts = append(parts, document...)
			}
		case "web_search_tool_result", "code_execution_tool_result", "bash_code_execution_tool_result", "text_editor_code_execution_tool_result", "tool_search_tool_result":
			flushPendingSignature()
			parts = append(parts, antigravity.Part{Text: "Server tool result " + block.Type + ": " + string(block.Content)})
		default:
			return nil, fmt.Errorf("unsupported content block type %q", block.Type)
		}
	}
	flushPendingSignature()
	return parts, nil
}

func mapAnthropicTools(tools []anthropicTool, choice json.RawMessage) (antigravity.Tools, error) {
	var mapped antigravity.Tools
	for _, tool := range tools {
		typeName := strings.ToLower(tool.Type)
		switch {
		case strings.HasPrefix(typeName, "bash_"), strings.HasPrefix(typeName, "text_editor_"), strings.HasPrefix(typeName, "memory_"):
			declaration, err := anthropicClientTool(tool, typeName)
			if err != nil {
				return antigravity.Tools{}, err
			}
			mapped.Functions = append(mapped.Functions, declaration)
		case strings.HasPrefix(typeName, "web_search_"):
			mapped.GoogleSearch = json.RawMessage(`{}`)
		case strings.HasPrefix(typeName, "web_fetch_"), strings.HasPrefix(typeName, "code_execution_"),
			strings.HasPrefix(typeName, "tool_search_tool_"), strings.Contains(typeName, "toolset"):
		case typeName == "", typeName == "custom":
			if tool.Name == "" {
				return antigravity.Tools{}, fmt.Errorf("tool name is required")
			}
			var strict bool
			if raw := tool.Options["strict"]; rawJSONConfigured(raw) {
				if err := json.Unmarshal(raw, &strict); err != nil {
					return antigravity.Tools{}, fmt.Errorf("tool strict must be a boolean")
				}
			}
			parameters := tool.InputSchema
			if len(parameters) == 0 {
				parameters = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			mapped.Functions = append(mapped.Functions, antigravity.FunctionDeclaration{
				Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: strict,
			})
		default:
			return antigravity.Tools{}, fmt.Errorf("unsupported tool type %q", tool.Type)
		}
	}
	config, err := anthropicToolChoice(choice)
	if err != nil {
		return antigravity.Tools{}, err
	}
	mapped.ToolConfig = config
	if mapped.Functions, err = upstreamFunctions(mapped.Functions); err != nil {
		return antigravity.Tools{}, err
	}
	return mapped, nil
}

// anthropicClientTool 将 Anthropic 定义的客户端工具映射为固定参数的函数声明
func anthropicClientTool(tool anthropicTool, typeName string) (antigravity.FunctionDeclaration, error) {
	if tool.Name == "" {
		return antigravity.FunctionDeclaration{}, fmt.Errorf("tool name is required")
	}
	declaration := antigravity.FunctionDeclaration{Name: tool.Name}
	switch {
	case strings.HasPrefix(typeName, "bash_"):
		declaration.Description = "Run a shell command in a persistent bash session. Set restart to true to restart the session."
		declaration.Parameters = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"restart":{"type":"boolean"}}}`)
	case strings.HasPrefix(typeName, "text_editor_"):
		commands := `"view","create","str_replace","insert"`
		if typeName == "text_editor_20241022" || typeName == "text_editor_20250124" {
			commands += `,"undo_edit"`
		}
		declaration.Description = "View, create and edit text files. view reads a file or lists a directory, create writes file_text, str_replace replaces old_str with new_str, insert adds insert_text after insert_line."
		declaration.Parameters = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","enum":[` + commands + `]},"path":{"type":"string"},"file_text":{"type":"string"},"old_str":{"type":"string"},"new_str":{"type":"string"},"insert_line":{"type":"integer"},"insert_text":{"type":"string"},"view_range":{"type":"array","items":{"type":"integer"}}},"required":["command","path"]}`)
	default:
		declaration.Description = "Read and write persistent memory files in the /memories directory. view reads a file or lists a directory, create writes file_text, str_replace replaces old_str with new_str, insert adds insert_text after insert_line, delete removes path, rename moves old_path to new_path."
		declaration.Parameters = json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","enum":["view","create","str_replace","insert","delete","rename"]},"path":{"type":"string"},"file_text":{"type":"string"},"old_str":{"type":"string"},"new_str":{"type":"string"},"insert_line":{"type":"integer"},"insert_text":{"type":"string"},"view_range":{"type":"array","items":{"type":"integer"}},"old_path":{"type":"string"},"new_path":{"type":"string"}},"required":["command"]}`)
	}
	return declaration, nil
}

func anthropicToolChoice(raw json.RawMessage) (antigravity.ToolConfig, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return antigravity.ToolConfig{Mode: "auto"}, nil
	}
	var choice struct {
		Type            string `json:"type"`
		Name            string `json:"name"`
		DisableParallel *bool  `json:"disable_parallel_tool_use"`
	}
	if err := json.Unmarshal(raw, &choice); err != nil {
		return antigravity.ToolConfig{}, fmt.Errorf("invalid tool_choice: %w", err)
	}
	config := antigravity.ToolConfig{}
	if choice.DisableParallel != nil {
		parallel := !*choice.DisableParallel
		config.ParallelCalls = &parallel
	}
	switch choice.Type {
	case "auto":
		config.Mode = "auto"
	case "none":
		config.Mode = "none"
	case "any":
		config.Mode = "required"
	case "tool":
		if choice.Name == "" {
			return antigravity.ToolConfig{}, fmt.Errorf("tool_choice name is required")
		}
		config.Mode, config.AllowedFunctionNames = "required", []string{choice.Name}
	default:
		return antigravity.ToolConfig{}, fmt.Errorf("unsupported tool_choice type %q", choice.Type)
	}
	return config, nil
}

// buildAnthropicResponse 构造非流式 Message，omitted 为 thinking.display 为 omitted
func buildAnthropicResponse(id string, model string, result generationResult, omitted bool) map[string]any {
	var stopSequence any
	if result.stopSequence != "" {
		stopSequence = result.stopSequence
	}
	response := map[string]any{
		"id":            id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       anthropicBlocks(id, result, omitted),
		"stop_reason":   anthropicStop(result.finishReason, len(result.toolCalls) > 0),
		"stop_sequence": stopSequence,
	}
	if result.providerModel != "" {
		response["provider_model"] = result.providerModel
	}
	if result.usage != nil {
		usage := anthropicUsage(result.usage)
		if result.grounding != nil {
			usage["server_tool_use"] = anthropicSearchUsage(result.grounding)
		}
		response["usage"] = usage
	}
	return response
}

// anthropicBlocks 按事件顺序构造内容块：思考事件为 thinking 块，独立签名与函数调用签名并入正在进行的 thinking 块，正文与函数调用为 text 与 tool_use 块，搜索来源为排在首个 text 块之前的 server_tool_use 与 web_search_tool_result 块，text 块带引用的来源
func anthropicBlocks(id string, result generationResult, omitted bool) []anthropicContentBlock {
	blocks := make([]anthropicContentBlock, 0)
	thinking := func() *anthropicContentBlock {
		if len(blocks) > 0 && blocks[len(blocks)-1].Type == "thinking" {
			return &blocks[len(blocks)-1]
		}
		return nil
	}
	sign := func(signature string) {
		if block := thinking(); block != nil && signature != "" && *block.Signature == "" {
			*block.Signature = signature
		}
	}
	for _, event := range result.events {
		switch event.Kind {
		case antigravity.EventText:
			if len(blocks) > 0 && blocks[len(blocks)-1].Type == "text" {
				blocks[len(blocks)-1].Text += event.Text
			} else {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: event.Text})
			}
		case antigravity.EventReasoning:
			if thinking() == nil {
				text, signature := "", ""
				blocks = append(blocks, anthropicContentBlock{Type: "thinking", Thinking: &text, Signature: &signature})
			}
			if !omitted {
				*thinking().Thinking += event.Text
			}
			sign(event.ThoughtSignature)
		case antigravity.EventThoughtSignature:
			sign(event.ThoughtSignature)
		case antigravity.EventMedia:
			blocks = append(blocks, anthropicContentBlock{Type: "text", Text: mediaMarkdown(*event.Media)})
		case antigravity.EventToolCall:
			if event.ToolCall != nil {
				sign(event.ToolCall.ThoughtSignature)
				blocks = append(blocks, anthropicContentBlock{
					Type: "tool_use", ID: event.ToolCall.ID, Name: event.ToolCall.Name, Input: event.ToolCall.Arguments,
				})
			}
		case antigravity.EventGrounding:
			position := slices.IndexFunc(blocks, func(block anthropicContentBlock) bool { return block.Type == "text" })
			if position < 0 {
				position = len(blocks)
			}
			blocks = slices.Insert(blocks, position, anthropicSearchBlocks(serverToolID(id), event.Grounding)...)
		}
	}
	if result.grounding != nil {
		for index := range blocks {
			if blocks[index].Type == "text" {
				blocks[index].Citations = anthropicCitations(blocks[index].Text, result.grounding)
			}
		}
	}
	return blocks
}

// serverToolID 返回消息中搜索调用的 server_tool_use ID
func serverToolID(messageID string) string {
	return "srvtoolu_" + strings.TrimPrefix(messageID, "msg_")
}

func anthropicStop(reason string, hasTools bool) string {
	normalized := strings.ToLower(strings.TrimSpace(reason))
	switch normalized {
	case "max_tokens", "max_output_tokens", "length":
		return "max_tokens"
	case "stop_sequence":
		return "stop_sequence"
	case "pause_turn":
		return "pause_turn"
	default:
		if contentFilterFinishReason(normalized) {
			return "refusal"
		}
		if hasTools {
			return "tool_use"
		}
		return "end_turn"
	}
}

func anthropicUsage(usage *antigravity.Usage) map[string]any {
	return map[string]any{
		"input_tokens":                inputTokens(usage) - usage.CachedTokens,
		"cache_creation_input_tokens": 0,
		"cache_read_input_tokens":     usage.CachedTokens,
		"output_tokens":               outputTokens(usage),
	}
}

// writeAnthropicModels 按 Anthropic Models API 的生命周期、游标与条数参数返回一页模型，省略 limit 时返回全部
func writeAnthropicModels(w http.ResponseWriter, r *http.Request, models []antigravity.Model) {
	query := r.URL.Query()
	if lifecycles := append(query["lifecycle"], query["lifecycle[]"]...); len(lifecycles) > 0 && !slices.Contains(strings.Split(strings.Join(lifecycles, ","), ","), "active") {
		models = nil
	}
	page, hasMore, err := anthropicListPage(query, models, func(model antigravity.Model) string { return model.ID }, listLimits{fallback: len(models), min: 1, max: 1000})
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	data := make([]map[string]any, 0, len(page))
	for _, model := range page {
		data = append(data, anthropicModelObject(model))
	}
	response := map[string]any{"data": data, "has_more": hasMore, "first_id": nil, "last_id": nil}
	if len(page) > 0 {
		response["first_id"] = page[0].ID
		response["last_id"] = page[len(page)-1].ID
	}
	writeJSON(w, http.StatusOK, response)
}

// anthropicModelObject 投影列表与单模型共用的 Anthropic ModelInfo 字段，输入与输出上限取实时目录
func anthropicModelObject(model antigravity.Model) map[string]any {
	item := map[string]any{
		"type": "model", "id": model.ID, "display_name": model.Name, "created_at": "1970-01-01T00:00:00Z",
		"max_input_tokens": anthropicTokenLimit(model.InputTokenLimit), "max_tokens": anthropicTokenLimit(model.OutputTokenLimit),
		"capabilities": nil, "lifecycle": "active", "line": nil, "deprecated_at": nil, "retires_at": nil,
	}
	if len(model.ThinkingLevels) > 0 {
		item["thinking_levels"], item["default_thinking_level"] = model.ThinkingLevels, model.DefaultThinkingLevel
	}
	return item
}

// anthropicTokenLimit 将目录缺失的 token 上限投影为 null
func anthropicTokenLimit(limit int64) any {
	if limit <= 0 {
		return nil
	}
	return limit
}

// anthropicStreamWriter 把规范事件写成 Messages 流，usage 为 message_start 的输入用量，omitted 时 thinking 块不带思考正文
type anthropicStreamWriter struct {
	w                 http.ResponseWriter
	id                string
	model             string
	usage             *antigravity.Usage
	omitted           bool
	blockIndex        int
	currentBlock      string
	thinkingSignature string
	// search 为请求只声明了 Google 搜索，grounding 为已到达的搜索来源，text 为当前 text 块的正文
	search    bool
	grounding *antigravity.Grounding
	text      strings.Builder
}

func (s *server) streamAnthropic(r *http.Request, writer *anthropicStreamWriter, events <-chan antigravity.Event) {
	emit := writer.live
	if writer.search {
		emit = holdUntilGrounding(writer.live)
	}
	result, err := consumeStreamEvents(r.Context(), events, emit, func() error { return writeAnthropicPing(writer.w) })
	if err != nil {
		if shouldWriteRequestError(r, err) {
			_ = writer.error(err)
		}
		return
	}
	_ = writer.finish(result)
}

func (writer *anthropicStreamWriter) emit(eventType string, payload any) error {
	return writeSSE(writer.w, eventType, payload)
}

// start 写出 message_start，输入用量取首个上游事件的用量，没有时为本地计数
func (writer *anthropicStreamWriter) start() error {
	usage := anthropicUsage(writer.usage)
	usage["output_tokens"] = 0
	return writer.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": writer.id, "type": "message", "role": "assistant", "model": writer.model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usage,
		},
	})
}

// live 写出一个事件：思考为 thinking 块，独立签名与函数调用签名并入正在进行的 thinking 块，搜索来源为 server_tool_use 与 web_search_tool_result 块
func (writer *anthropicStreamWriter) live(event antigravity.Event) error {
	switch event.Kind {
	case antigravity.EventGrounding:
		if err := writer.closeBlock(); err != nil {
			return err
		}
		writer.grounding = event.Grounding
		for _, block := range anthropicSearchBlocks(serverToolID(writer.id), event.Grounding) {
			start := block
			if block.Type == "server_tool_use" {
				start.Input = json.RawMessage(`{}`)
			}
			if err := writer.emit("content_block_start", map[string]any{"type": "content_block_start", "index": writer.blockIndex, "content_block": start}); err != nil {
				return err
			}
			writer.currentBlock = block.Type
			if block.Type == "server_tool_use" {
				if err := writer.emit("content_block_delta", map[string]any{
					"type": "content_block_delta", "index": writer.blockIndex,
					"delta": map[string]any{"type": "input_json_delta", "partial_json": string(block.Input)},
				}); err != nil {
					return err
				}
			}
			if err := writer.closeBlock(); err != nil {
				return err
			}
		}
		return nil
	case antigravity.EventText:
		if err := writer.ensureBlock("text"); err != nil {
			return err
		}
		writer.text.WriteString(event.Text)
		return writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": event.Text},
		})
	case antigravity.EventReasoning:
		if err := writer.ensureBlock("thinking"); err != nil {
			return err
		}
		writer.sign(event.ThoughtSignature)
		if writer.omitted {
			return nil
		}
		return writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "thinking_delta", "thinking": event.Text},
		})
	case antigravity.EventThoughtSignature:
		writer.sign(event.ThoughtSignature)
	case antigravity.EventMedia:
		if err := writer.closeBlock(); err != nil {
			return err
		}
		if err := writer.ensureBlock("text"); err != nil {
			return err
		}
		if err := writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "text_delta", "text": mediaMarkdown(*event.Media)},
		}); err != nil {
			return err
		}
		return writer.closeBlock()
	case antigravity.EventToolCall:
		if event.ToolCall == nil {
			return nil
		}
		call := event.ToolCall
		writer.sign(call.ThoughtSignature)
		if err := writer.closeBlock(); err != nil {
			return err
		}
		if err := writer.emit("content_block_start", map[string]any{
			"type": "content_block_start", "index": writer.blockIndex,
			"content_block": map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": map[string]any{}},
		}); err != nil {
			return err
		}
		writer.currentBlock = "tool_use"
		if err := writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": string(call.Arguments)},
		}); err != nil {
			return err
		}
		return writer.closeBlock()
	}
	return nil
}

// sign 记下正在进行的 thinking 块的签名
func (writer *anthropicStreamWriter) sign(signature string) {
	if writer.currentBlock == "thinking" && writer.thinkingSignature == "" {
		writer.thinkingSignature = signature
	}
}

func (writer *anthropicStreamWriter) ensureBlock(blockType string) error {
	if writer.currentBlock == blockType {
		return nil
	}
	if err := writer.closeBlock(); err != nil {
		return err
	}
	block := map[string]any{"type": blockType}
	if blockType == "thinking" {
		block["thinking"], block["signature"] = "", ""
	} else {
		block["text"] = ""
	}
	if err := writer.emit("content_block_start", map[string]any{
		"type": "content_block_start", "index": writer.blockIndex, "content_block": block,
	}); err != nil {
		return err
	}
	writer.currentBlock = blockType
	return nil
}

func (writer *anthropicStreamWriter) closeBlock() error {
	if writer.currentBlock == "" {
		return nil
	}
	if writer.currentBlock == "text" && writer.grounding != nil {
		for _, citation := range anthropicCitations(writer.text.String(), writer.grounding) {
			if err := writer.emit("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": writer.blockIndex,
				"delta": map[string]any{"type": "citations_delta", "citation": citation},
			}); err != nil {
				return err
			}
		}
	}
	writer.text.Reset()
	if writer.currentBlock == "thinking" && writer.thinkingSignature != "" {
		if err := writer.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": writer.blockIndex,
			"delta": map[string]any{"type": "signature_delta", "signature": writer.thinkingSignature},
		}); err != nil {
			return err
		}
	}
	if err := writer.emit("content_block_stop", map[string]any{
		"type": "content_block_stop", "index": writer.blockIndex,
	}); err != nil {
		return err
	}
	writer.blockIndex++
	writer.currentBlock = ""
	writer.thinkingSignature = ""
	return nil
}

func (writer *anthropicStreamWriter) finish(result generationResult) error {
	if err := writer.closeBlock(); err != nil {
		return err
	}
	usage := map[string]any{"output_tokens": 0}
	if result.usage != nil {
		usage = anthropicUsage(result.usage)
	}
	if result.grounding != nil {
		usage["server_tool_use"] = anthropicSearchUsage(result.grounding)
	}
	var stopSequence any
	if result.stopSequence != "" {
		stopSequence = result.stopSequence
	}
	delta := map[string]any{
		"stop_reason": anthropicStop(result.finishReason, len(result.toolCalls) > 0), "stop_sequence": stopSequence,
	}
	if err := writer.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": delta,
		"usage": usage,
	}); err != nil {
		return err
	}
	return writer.emit("message_stop", map[string]string{"type": "message_stop"})
}

func (writer *anthropicStreamWriter) error(err error) error {
	return writer.emit("error", map[string]any{
		"type":  "error",
		"error": map[string]string{"type": anthropicErrorType(err), "message": anthropicErrorMessage(err)},
	})
}

// anthropicListPage 按 before_id、after_id 与 limit 返回一页对象及该方向是否还有对象，未知游标返回空页
func anthropicListPage[T any](query url.Values, items []T, id func(T) string, limits listLimits) ([]T, bool, error) {
	limit, err := listLimit(query, limits)
	if err != nil {
		return nil, false, err
	}
	cursor := func(value string) int { return slices.IndexFunc(items, func(item T) bool { return id(item) == value }) }
	if before := query.Get("before_id"); before != "" {
		end := max(cursor(before), 0)
		start := max(end-limit, 0)
		return items[start:end], start > 0, nil
	}
	start := 0
	if after := query.Get("after_id"); after != "" {
		start = len(items)
		if index := cursor(after); index >= 0 {
			start = index + 1
		}
	}
	end := min(start+limit, len(items))
	return items[start:end], end < len(items), nil
}
