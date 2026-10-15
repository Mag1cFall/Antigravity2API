package api

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

type responsesRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"`
	Instructions       string            `json:"instructions"`
	Stream             bool              `json:"stream"`
	Tools              []responsesTool   `json:"tools"`
	ToolChoice         json.RawMessage   `json:"tool_choice"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"top_p"`
	MaxOutputTokens    *int64            `json:"max_output_tokens"`
	Reasoning          json.RawMessage   `json:"reasoning"`
	Text               json.RawMessage   `json:"text"`
	PreviousResponseID string            `json:"previous_response_id"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls"`
	Truncation         string            `json:"truncation"`
	Metadata           map[string]string `json:"metadata"`
	Store              *bool             `json:"store"`
	Background         bool              `json:"background"`
	PromptCacheKey     string            `json:"prompt_cache_key"`
	User               string            `json:"user"`
	Include            []string          `json:"include"`
	Conversation       json.RawMessage   `json:"conversation"`
	Prompt             json.RawMessage   `json:"prompt"`
	ContextManagement  []struct {
		Type             string `json:"type"`
		CompactThreshold *int64 `json:"compact_threshold"`
	} `json:"context_management"`
}

// includesEncrypted 返回请求是否要求 reasoning 项带 encrypted_content
func (request responsesRequest) includesEncrypted() bool {
	return slices.Contains(request.Include, "reasoning.encrypted_content")
}

// compactThreshold 返回 context_management 中 compaction 的触发 token 数，没有时为 0
func (request responsesRequest) compactThreshold() int64 {
	for _, entry := range request.ContextManagement {
		if entry.Type == "compaction" && entry.CompactThreshold != nil {
			return *entry.CompactThreshold
		}
	}
	return 0
}

type responsesTool struct {
	Type              string          `json:"type"`
	Name              string          `json:"name,omitempty"`
	Description       string          `json:"description,omitempty"`
	Parameters        json.RawMessage `json:"parameters,omitempty"`
	SearchContextSize string          `json:"search_context_size,omitempty"`
	UserLocation      json.RawMessage `json:"user_location,omitempty"`
	Filters           json.RawMessage `json:"filters,omitempty"`
	Container         json.RawMessage `json:"container,omitempty"`
	Format            json.RawMessage `json:"format,omitempty"`
	Strict            *bool           `json:"strict,omitempty"`
	Tools             []responsesTool `json:"tools,omitempty"`
	Size              string          `json:"size,omitempty"`
	Quality           string          `json:"quality,omitempty"`
}

type responsesInputItem struct {
	Type             string          `json:"type"`
	ID               string          `json:"id"`
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	CallID           string          `json:"call_id"`
	Name             string          `json:"name"`
	Namespace        string          `json:"namespace"`
	Arguments        string          `json:"arguments"`
	Input            string          `json:"input"`
	Action           json.RawMessage `json:"action"`
	Operation        json.RawMessage `json:"operation"`
	Status           string          `json:"status"`
	Output           json.RawMessage `json:"output"`
	EncryptedContent string          `json:"encrypted_content"`
	Summary          []struct {
		Text string `json:"text"`
	} `json:"summary"`
	Tools     []responsesTool `json:"tools"`
	Reasoning *struct {
		Effort string `json:"effort"`
	} `json:"reasoning"`
}

type responseState struct {
	ParentID           string
	Contents           []antigravity.Content
	InlineInstructions []string
	Record             *responseRecord
	// Interaction 为 Interactions 接口保存的交互资源
	Interaction *interactionRecord
}

// responseRecord 是 Responses 接口保存的响应对象、输入项与输出项
type responseRecord struct {
	Model      string
	Shell      json.RawMessage
	Input      []storedItem
	Output     []storedItem
	Background bool
	// Stream 为后台流式响应写出的事件，续传请求从中回放
	Stream *streamLog
}

// storedItem 是带 ID 的 Responses 输入或输出项
type storedItem struct {
	ID  string
	Raw json.RawMessage
}

const responseStateCapacity = 256

// responseHistory 是续接起点的响应 ID 及其按顺序展开的完整上下文
type responseHistory struct {
	ID                 string
	Model              string
	Contents           []antigravity.Content
	InlineInstructions []string
	Items              []storedItem
}

type responseStateStore struct {
	mu     sync.Mutex
	states map[string]responseState
	order  []string
}

func newResponseStateStore() *responseStateStore {
	return &responseStateStore{states: make(map[string]responseState, responseStateCapacity)}
}

func (store *responseStateStore) Load(id string) (responseHistory, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	history := responseHistory{ID: id, Contents: make([]antigravity.Content, 0), InlineInstructions: make([]string, 0)}
	if state, exists := store.states[id]; exists && state.Record != nil {
		history.Model = state.Record.Model
	}
	chain := make([]responseState, 0)
	for id != "" {
		state, exists := store.states[id]
		if !exists {
			return responseHistory{}, false
		}
		chain = append(chain, state)
		id = state.ParentID
	}
	for index := len(chain) - 1; index >= 0; index-- {
		history.Contents = append(history.Contents, cloneResponseContents(chain[index].Contents)...)
		history.InlineInstructions = append(history.InlineInstructions, chain[index].InlineInstructions...)
		if record := chain[index].Record; record != nil {
			history.Items = append(append(history.Items, record.Input...), record.Output...)
		}
	}
	return history, true
}

// Store 保存本次输入与生成输出组成的续接节点，同一 ID 再次保存时替换该节点；续接起点在生成期间已被淘汰时，把请求开始时读取的完整上下文并入该节点
func (store *responseStateStore) Store(id string, previous responseHistory, result generationResult, state responseState) {
	state.ParentID = previous.ID
	state.Contents = cloneResponseContents(state.Contents)
	if output := responseHistoryOutput(result); len(output.Parts) > 0 {
		state.Contents = append(state.Contents, output)
	}
	state.InlineInstructions = append([]string(nil), state.InlineInstructions...)
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.states[previous.ID]; previous.ID != "" && !exists {
		state.ParentID = ""
		state.Contents = append(cloneResponseContents(previous.Contents), state.Contents...)
		state.InlineInstructions = append(append([]string(nil), previous.InlineInstructions...), state.InlineInstructions...)
		if state.Record != nil {
			state.Record.Input = append(append([]storedItem(nil), previous.Items...), state.Record.Input...)
		}
	}
	if _, exists := store.states[id]; !exists {
		store.order = append(store.order, id)
	}
	store.states[id] = state
	for len(store.order) > responseStateCapacity {
		id := store.order[0]
		store.order = store.order[1:]
		store.removeLocked(id)
	}
}

// removeLocked 删除一个节点，并把它的上下文并入以它为续接起点的子节点
func (store *responseStateStore) removeLocked(id string) {
	parent := store.states[id]
	for childID, child := range store.states {
		if child.ParentID != id {
			continue
		}
		child.ParentID = parent.ParentID
		child.Contents = append(cloneResponseContents(parent.Contents), child.Contents...)
		child.InlineInstructions = append(append([]string(nil), parent.InlineInstructions...), child.InlineInstructions...)
		if child.Record != nil && parent.Record != nil {
			merged := *child.Record
			merged.Input = append(append(append([]storedItem(nil), parent.Record.Input...), parent.Record.Output...), child.Record.Input...)
			child.Record = &merged
		}
		store.states[childID] = child
	}
	delete(store.states, id)
}

// Stream 返回后台流式响应的事件记录
func (store *responseStateStore) Stream(id string) (*streamLog, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[id]
	if !exists || state.Record == nil || state.Record.Stream == nil {
		return nil, false
	}
	return state.Record.Stream, true
}

// Response 返回 Responses 接口保存的完整响应对象及其是否以后台模式创建
func (store *responseStateStore) Response(id string) (json.RawMessage, bool, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[id]
	if !exists || state.Record == nil {
		return nil, false, false
	}
	var shell map[string]json.RawMessage
	_ = json.Unmarshal(state.Record.Shell, &shell)
	output := make([]json.RawMessage, 0, len(state.Record.Output))
	for _, item := range state.Record.Output {
		output = append(output, item.Raw)
	}
	shell["output"], _ = json.Marshal(output)
	encoded, _ := json.Marshal(shell)
	return encoded, state.Record.Background, true
}

// InputItems 返回生成该响应时的全部输入项：续接链上各前序响应的输入与输出，以及本次输入
func (store *responseStateStore) InputItems(id string) ([]storedItem, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.states[id]
	if !exists || state.Record == nil {
		return nil, false
	}
	items := append([]storedItem(nil), state.Record.Input...)
	for parentID := state.ParentID; parentID != ""; {
		parent, exists := store.states[parentID]
		if !exists {
			break
		}
		if parent.Record != nil {
			items = append(append(append([]storedItem(nil), parent.Record.Input...), parent.Record.Output...), items...)
		}
		parentID = parent.ParentID
	}
	return items, true
}

// Delete 删除 Responses 接口保存的响应，以它为起点的续接保留完整上下文
func (store *responseStateStore) Delete(id string) bool {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.states[id]; !exists {
		return false
	}
	store.removeLocked(id)
	store.order = slices.DeleteFunc(store.order, func(value string) bool { return value == id })
	return true
}

// ResolveItems 将输入中的 item_reference 替换为已保存的同 ID 输入或输出项
func (store *responseStateStore) ResolveItems(raw json.RawMessage) (json.RawMessage, error) {
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return raw, nil
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	resolved := false
	for index, item := range items {
		var head struct {
			Type string `json:"type"`
			Role string `json:"role"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(item, &head) != nil || head.Type != "item_reference" && (head.Type != "" || head.Role != "" || head.ID == "") {
			continue
		}
		found := false
		for _, state := range store.states {
			if state.Record == nil {
				continue
			}
			for _, stored := range slices.Concat(state.Record.Input, state.Record.Output) {
				if stored.ID == head.ID {
					items[index], found = stored.Raw, true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("item %q was not found", head.ID)
		}
		resolved = true
	}
	if !resolved {
		return raw, nil
	}
	return json.Marshal(items)
}

func cloneResponseContents(contents []antigravity.Content) []antigravity.Content {
	cloned := append([]antigravity.Content(nil), contents...)
	for index := range cloned {
		cloned[index].Parts = append([]antigravity.Part(nil), contents[index].Parts...)
	}
	return cloned
}

func (s *server) handleResponses(w http.ResponseWriter, r *http.Request) {
	var request responsesRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	if rawJSONConfigured(request.Conversation) || rawJSONConfigured(request.Prompt) {
		writeOpenAIError(w, http.StatusNotFound, "not_found", "conversation and prompt objects are not stored by this service; send the full input or previous_response_id")
		return
	}
	responseID := newID("resp")
	prepared, err := s.prepareResponses(r, &request, responseID)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if responsesCompactionTrigger(request.Input) {
		s.handleCompactionTrigger(w, r, request, prepared, responseID)
		return
	}
	if request.Background && !request.Stream {
		s.respondBackground(w, r, request, prepared, responseID)
		return
	}
	if request.Background {
		s.streamBackground(w, r, request, prepared, responseID)
		return
	}
	compaction, err := s.compactOverThreshold(r, &request, &prepared, responseID)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	events, err := s.generateResponses(r.Context(), request, &prepared)
	if err == nil {
		events = s.hostedImages(r.Context(), request.Tools, prepared.generate.Contents, events)
	}
	if err == nil && request.Stream {
		events, _, err = awaitStreamStart(r.Context(), events)
	}
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	created := time.Now().Unix()
	if request.Stream {
		s.streamResponses(w, r, request, prepared, responseID, created, events, compaction)
		return
	}
	result, err := consumeEvents(r.Context(), events, nil)
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	response := buildResponsesObject(responseID, created, request, result, compaction, searchOnly(prepared.generate.Tools))
	if request.storesResponse() {
		s.storeResponse(responseID, request, prepared, result, response)
	}
	writeJSON(w, http.StatusOK, response)
}

// generateResponses 发起生成；truncation 为 auto 且上游报告上下文超限时，按完整轮次从最早处删除历史后重试，删除量按本地计数与上游给出的上限估算
func (s *server) generateResponses(ctx context.Context, request responsesRequest, prepared *responsesPreparation) (<-chan antigravity.Event, error) {
	for {
		events, err := s.service.Generate(ctx, prepared.generate)
		var upstream *antigravity.UpstreamError
		if err == nil || request.Truncation != "auto" || !errors.As(err, &upstream) {
			return events, err
		}
		overflow, input, limit := upstream.ContextOverflow()
		if !overflow {
			return nil, err
		}
		trimmed, ok := s.truncateTurns(ctx, prepared.generate, input, limit)
		if !ok {
			return nil, err
		}
		prepared.generate = trimmed
	}
}

// truncateTurns 删除最早的完整轮次使本地计数按上游实际计数校准后不超过上限，至少删除一轮；只剩最后一轮时返回 false
func (s *server) truncateTurns(ctx context.Context, request antigravity.GenerateRequest, input, limit int64) (antigravity.GenerateRequest, bool) {
	var starts []int
	for index, content := range request.Contents {
		if index > 0 && content.Role == antigravity.RoleUser && !slices.ContainsFunc(content.Parts, func(part antigravity.Part) bool { return part.FunctionResult != nil }) {
			starts = append(starts, index)
		}
	}
	if len(starts) == 0 {
		return request, false
	}
	estimate, err := s.service.CountTokens(ctx, request)
	if err != nil || estimate <= 0 {
		return request, false
	}
	scale := 1.0
	if input > estimate {
		scale = float64(input) / float64(estimate)
	}
	trimmed := func(cut int) antigravity.GenerateRequest {
		candidate := request
		candidate.Contents = request.Contents[starts[cut]:]
		return candidate
	}
	cut := sort.Search(len(starts)-1, func(cut int) bool {
		tokens, err := s.service.CountTokens(ctx, trimmed(cut))
		return err == nil && float64(tokens)*scale <= float64(limit)
	})
	return trimmed(cut), true
}

// compactOverThreshold 在 context_management 的 compaction 阈值被超过时先压缩输入，并以保留的 user 消息与 compaction 项作为本次输入；返回生成前插入输出的 compaction 项
func (s *server) compactOverThreshold(r *http.Request, request *responsesRequest, prepared *responsesPreparation, id string) (map[string]any, error) {
	threshold := request.compactThreshold()
	if threshold <= 0 {
		return nil, nil
	}
	tokens, err := s.service.CountTokens(r.Context(), prepared.generate)
	if err != nil || tokens <= threshold {
		return nil, err
	}
	item, retained, _, err := s.compactResponses(r.Context(), *request, prepared.previous, id)
	if err != nil {
		return nil, err
	}
	encoded, _ := json.Marshal(item)
	request.Input, _ = json.Marshal(append(slices.Clone(retained), encoded))
	request.PreviousResponseID = ""
	compacted, err := s.prepareResponses(r, request, id)
	if err != nil {
		return nil, err
	}
	*prepared = compacted
	return item, nil
}

// responsesPreparation 是转换后的生成请求，以及保存续接节点所需的前序上下文与本次输入
type responsesPreparation struct {
	generate           antigravity.GenerateRequest
	previous           responseHistory
	contents           []antigravity.Content
	inlineInstructions []string
	input              []storedItem
	// stream 为后台流式响应的事件记录，随响应对象保存
	stream *streamLog
}

// prepareResponses 展开 item_reference、转换请求、下载远程媒体、带上会话标识并接上 previous_response_id 的上下文
func (s *server) prepareResponses(r *http.Request, request *responsesRequest, id string) (responsesPreparation, error) {
	input, err := s.responseStates.ResolveItems(request.Input)
	if err != nil {
		return responsesPreparation{}, err
	}
	request.Input = input
	generateRequest, inlineInstructions, err := request.toGenerateRequest(id)
	if err == nil {
		err = inlineRemoteMedia(r.Context(), generateRequest.Contents)
	}
	if err != nil {
		return responsesPreparation{}, err
	}
	generateRequest.Session = clientSession(r, r.Header.Get("session_id"), r.Header.Get("conversation_id"), request.PromptCacheKey, request.User)
	prepared := responsesPreparation{
		generate: generateRequest, contents: cloneResponseContents(generateRequest.Contents),
		inlineInstructions: append([]string(nil), inlineInstructions...), input: responsesInputItems(request.Input, id),
	}
	if request.PreviousResponseID == "" {
		return prepared, nil
	}
	previous, ok := s.responseStates.Load(request.PreviousResponseID)
	if !ok {
		return responsesPreparation{}, fmt.Errorf("previous response %q was not found", request.PreviousResponseID)
	}
	prepared.previous = previous
	prepared.generate.Contents = append(cloneResponseContents(previous.Contents), generateRequest.Contents...)
	inlineInstructions = append(append([]string(nil), previous.InlineInstructions...), inlineInstructions...)
	instructions := make([]string, 0, 1+len(inlineInstructions))
	if request.Instructions != "" {
		instructions = append(instructions, request.Instructions)
	}
	instructions = append(instructions, inlineInstructions...)
	prepared.generate.System = strings.Join(instructions, "\n")
	return prepared, nil
}

// storesResponse 返回响应是否保存为可续接与查询的节点，后台请求同样保存以供轮询
func (request responsesRequest) storesResponse() bool {
	return request.Store == nil || *request.Store || request.Background
}

// storeResponse 保存续接上下文与可查询的响应对象、输入项和输出项
func (s *server) storeResponse(id string, request responsesRequest, prepared responsesPreparation, result generationResult, response map[string]any) {
	shell := maps.Clone(response)
	delete(shell, "output")
	encoded, _ := json.Marshal(shell)
	record := &responseRecord{Model: request.Model, Shell: encoded, Input: prepared.input, Background: request.Background, Stream: prepared.stream}
	for _, item := range response["output"].([]any) {
		object, _ := item.(map[string]any)
		if itemID, _ := object["id"].(string); itemID != "" {
			raw, _ := json.Marshal(object)
			record.Output = append(record.Output, storedItem{ID: itemID, Raw: raw})
		}
	}
	s.responseStates.Store(id, prepared.previous, result, responseState{Contents: prepared.contents, InlineInstructions: prepared.inlineInstructions, Record: record})
}

// responsesInputItems 把请求输入整理为带 ID 的输入项：字符串输入与字符串内容转为内容数组，缺少 ID 的项按位置编号
func responsesInputItems(raw json.RawMessage, responseID string) []storedItem {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		encoded, _ := json.Marshal([]map[string]string{{"role": "user", "content": text}})
		raw = encoded
	}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	stored := make([]storedItem, 0, len(items))
	for index, rawItem := range items {
		var item map[string]json.RawMessage
		var head struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(rawItem, &item) != nil || json.Unmarshal(rawItem, &head) != nil {
			continue
		}
		if head.Type == "" || head.Type == "message" {
			item["type"] = json.RawMessage(`"message"`)
			var content string
			if json.Unmarshal(head.Content, &content) == nil {
				part := map[string]any{"type": "input_text", "text": content}
				if head.Role == "assistant" {
					part = map[string]any{"type": "output_text", "text": content, "annotations": []any{}}
				}
				item["content"], _ = json.Marshal([]any{part})
			}
			if _, exists := item["status"]; !exists {
				item["status"] = json.RawMessage(`"completed"`)
			}
		}
		if head.ID == "" {
			prefix := "item"
			if head.Type == "" || head.Type == "message" {
				prefix = "msg"
			}
			head.ID = fmt.Sprintf("%s_%s_%d", prefix, responseID, index)
			item["id"], _ = json.Marshal(head.ID)
		}
		encoded, _ := json.Marshal(item)
		stored = append(stored, storedItem{ID: head.ID, Raw: encoded})
	}
	return stored
}

// responseHistoryOutput 把一次生成写成续接用的 assistant 消息：相邻的正文与思考增量各合并为一个 part，签名留在所在 part 或单独成 part
func responseHistoryOutput(result generationResult) antigravity.Content {
	output := antigravity.Content{Role: antigravity.RoleAssistant}
	appendText := func(text string, thought bool, signature string) {
		if last := len(output.Parts) - 1; last >= 0 && output.Parts[last].FunctionCall == nil && output.Parts[last].Text != "" && output.Parts[last].Thought == thought && output.Parts[last].ThoughtSignature == "" {
			output.Parts[last].Text += text
			output.Parts[last].ThoughtSignature = signature
			return
		}
		output.Parts = append(output.Parts, antigravity.Part{Text: text, Thought: thought, ThoughtSignature: signature})
	}
	for _, event := range result.events {
		switch event.Kind {
		case antigravity.EventText:
			if event.Text != "" {
				appendText(event.Text, false, event.ThoughtSignature)
			}
		case antigravity.EventReasoning:
			appendText(event.Text, true, event.ThoughtSignature)
		case antigravity.EventToolCall:
			if event.ToolCall != nil {
				call := *event.ToolCall
				call.Arguments = append(json.RawMessage(nil), call.Arguments...)
				output.Parts = append(output.Parts, antigravity.Part{FunctionCall: &call, ThoughtSignature: event.ThoughtSignature})
			}
		case antigravity.EventThoughtSignature:
			if event.ThoughtSignature != "" {
				output.Parts = append(output.Parts, antigravity.Part{ThoughtSignature: event.ThoughtSignature})
			}
		case antigravity.EventMedia:
			media := *event.Media
			output.Parts = append(output.Parts, antigravity.Part{InlineData: &media, ThoughtSignature: event.ThoughtSignature})
		}
	}
	return output
}

func (request responsesRequest) toGenerateRequest(id string) (antigravity.GenerateRequest, []string, error) {
	switch request.Truncation {
	case "", "disabled", "auto":
	default:
		return antigravity.GenerateRequest{}, nil, fmt.Errorf("unsupported truncation %q", request.Truncation)
	}
	input, err := responsesContents(request.Input)
	if err != nil {
		return antigravity.GenerateRequest{}, nil, err
	}
	contents, inlineInstructions := input.contents, input.instructions
	instructions := make([]string, 0, 1+len(inlineInstructions))
	if request.Instructions != "" {
		instructions = append(instructions, request.Instructions)
	}
	instructions = append(instructions, inlineInstructions...)
	tools, err := mapResponsesTools(mergeResponsesTools(request.Tools, input.tools), request.ToolChoice)
	if err != nil {
		return antigravity.GenerateRequest{}, nil, err
	}
	tools.ToolConfig.ParallelCalls = request.ParallelToolCalls
	config := antigravity.GenerationConfig{
		Temperature:     request.Temperature,
		TopP:            request.TopP,
		MaxOutputTokens: request.MaxOutputTokens,
	}
	config.HideThinking = true
	if len(request.Reasoning) > 0 && string(request.Reasoning) != "null" {
		var reasoning struct {
			Effort          string `json:"effort"`
			Summary         string `json:"summary"`
			GenerateSummary string `json:"generate_summary"`
		}
		if err := json.Unmarshal(request.Reasoning, &reasoning); err != nil {
			return antigravity.GenerateRequest{}, nil, fmt.Errorf("invalid reasoning: %w", err)
		}
		config.ReasoningEffort = reasoning.Effort
		summary := cmp.Or(reasoning.Summary, reasoning.GenerateSummary)
		config.HideThinking = summary == "" || summary == "none"
	}
	if input.effort != "" {
		config.ReasoningEffort = input.effort
	}
	if len(request.Text) > 0 && string(request.Text) != "null" {
		var text struct {
			Format struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			} `json:"format"`
		}
		if err := json.Unmarshal(request.Text, &text); err != nil {
			return antigravity.GenerateRequest{}, nil, fmt.Errorf("invalid text config: %w", err)
		}
		switch text.Format.Type {
		case "json_object":
			config.ResponseMIMEType = "application/json"
		case "json_schema":
			config.ResponseMIMEType = "application/json"
			config.ResponseSchema = text.Format.Schema
		case "", "text":
		default:
			return antigravity.GenerateRequest{}, nil, fmt.Errorf("unsupported text format %q", text.Format.Type)
		}
	}
	return antigravity.GenerateRequest{
		ID:       id,
		Model:    request.Model,
		System:   strings.Join(instructions, "\n"),
		Contents: contents,
		Config:   config,
		Tools:    tools,
	}, inlineInstructions, nil
}

// responsesInput 表示转换后的 Responses 输入：消息、首部的 system 与 developer 文本、additional_tools 声明的工具与最后一个 configuration_update 的 effort
type responsesInput struct {
	contents     []antigravity.Content
	instructions []string
	tools        []responsesTool
	effort       string
}

// responsesContents 转换 Responses 输入项：首部 system、developer 消息为指令，对话中的作为 system-reminder 并入下一个 user 消息；reasoning 项的摘要与签名并入其后的 assistant 消息
func responsesContents(raw json.RawMessage) (responsesInput, error) {
	var input responsesInput
	if value := strings.TrimSpace(string(raw)); value == "" || value == "null" {
		return input, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		input.contents = []antigravity.Content{{Role: antigravity.RoleUser, Parts: []antigravity.Part{{Text: text}}}}
		return input, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return input, fmt.Errorf("input must be a string or item array")
	}
	window, err := splitCompaction(items)
	if err != nil {
		return input, err
	}
	items = window.items()
	contents := make([]antigravity.Content, 0, len(items))
	var systemMedia, reminders, reasoning []antigravity.Part
	add := func(content antigravity.Content) {
		if content.Role == antigravity.RoleAssistant && len(reasoning) > 0 {
			content.Parts = append(reasoning, content.Parts...)
			reasoning = nil
		}
		contents, reminders = appendWithReminders(contents, content, reminders)
	}
	flushReasoning := func() {
		if len(reasoning) == 0 {
			return
		}
		if last := len(contents) - 1; last >= 0 && contents[last].Role == antigravity.RoleAssistant {
			parts := contents[last].Parts
			calls := len(parts)
			for calls > 0 && parts[calls-1].FunctionCall != nil {
				calls--
			}
			if signature := reasoning[len(reasoning)-1]; calls < len(parts) && signature.Text == "" && parts[calls].FunctionCall.ThoughtSignature == "" {
				call := *parts[calls].FunctionCall
				call.ThoughtSignature = signature.ThoughtSignature
				parts[calls].FunctionCall = &call
				reasoning = reasoning[:len(reasoning)-1]
			}
			contents[last].Parts = slices.Concat(parts[:calls], reasoning, parts[calls:])
		} else {
			contents = append(contents, antigravity.Content{Role: antigravity.RoleAssistant, Parts: reasoning})
		}
		reasoning = nil
	}
	for index, rawItem := range items {
		var item responsesInputItem
		switch kind := responsesItemType(rawItem); kind {
		case "compaction_trigger":
			if index != len(items)-1 {
				return input, fmt.Errorf("compaction_trigger must be the final input item")
			}
			continue
		case "web_search_call", "code_interpreter_call", "image_generation_call", "file_search_call", "computer_call", "computer_call_output",
			"mcp_call", "mcp_list_tools", "mcp_approval_request", "mcp_approval_response", "tool_search_call", "tool_search_output", "program", "program_output":
			content, err := responsesHostedItem(kind, rawItem)
			if err != nil {
				return input, fmt.Errorf("%s: %w", kind, err)
			}
			flushReasoning()
			add(content)
			continue
		default:
			if err := json.Unmarshal(rawItem, &item); err != nil {
				return input, fmt.Errorf("input item: %w", err)
			}
		}
		if item.Namespace != "" {
			item.Name = item.Namespace + "." + item.Name
		}
		call := antigravity.FunctionCall{ID: item.CallID, Name: item.Name}
		switch item.Type {
		case "additional_tools":
			input.tools = append(input.tools, item.Tools...)
			continue
		case "configuration_update":
			if item.Reasoning != nil && item.Reasoning.Effort != "" {
				input.effort = item.Reasoning.Effort
			}
			continue
		case "", "message":
			if item.Role == "system" || item.Role == "developer" {
				parts, err := openAIContentParts(item.Content)
				if err != nil {
					return input, fmt.Errorf("%s message: %w", item.Role, err)
				}
				var text strings.Builder
				for _, part := range parts {
					if part.Text == "" {
						systemMedia = append(systemMedia, part)
					}
					text.WriteString(part.Text)
				}
				switch {
				case text.Len() == 0:
				case len(contents) == 0:
					input.instructions = append(input.instructions, text.String())
				default:
					reminders = append(reminders, systemReminder(text.String()))
				}
				continue
			}
			role, err := openAIRole(item.Role)
			if err != nil {
				return input, err
			}
			parts, err := openAIContentParts(item.Content)
			if err != nil {
				return input, err
			}
			if role != antigravity.RoleAssistant {
				flushReasoning()
			}
			add(antigravity.Content{Role: role, Parts: parts})
			continue
		case "function_call":
			call.Arguments = functionCallArguments(item.Arguments)
		case "custom_tool_call":
			call.Arguments = customToolArguments(item.Input)
		case "local_shell_call":
			call.Name, call.Arguments = "local_shell", localShellArguments(item.Action)
		case "shell_call":
			call.Name, call.Arguments = "shell", rawObjectArguments(item.Action)
		case "apply_patch_call":
			call.Name, call.Arguments = "apply_patch", rawObjectArguments(item.Operation)
		case "function_call_output", "custom_tool_call_output", "local_shell_call_output", "shell_call_output", "apply_patch_call_output":
			if item.CallID == "" {
				item.CallID = item.ID
			}
			if item.Type == "apply_patch_call_output" {
				item.Output, _ = json.Marshal(map[string]any{"status": item.Status, "output": rawJSONValue(item.Output, nil)})
			}
			output, media, err := functionResultContent(item.Output, openAIContentParts)
			if err != nil {
				return input, fmt.Errorf("%s: %w", item.Type, err)
			}
			flushReasoning()
			add(antigravity.Content{Role: antigravity.RoleTool, Parts: []antigravity.Part{{FunctionResult: &antigravity.FunctionResult{
				ID: item.CallID, Content: output, Parts: media,
			}}}})
			continue
		case "reasoning":
			flushReasoning()
			var summary strings.Builder
			for _, part := range item.Summary {
				summary.WriteString(part.Text)
			}
			if summary.Len() > 0 {
				reasoning = append(reasoning, antigravity.Part{Text: summary.String(), Thought: true})
			}
			if item.EncryptedContent != "" {
				reasoning = append(reasoning, antigravity.Part{ThoughtSignature: item.EncryptedContent})
			}
			continue
		default:
			return input, fmt.Errorf("unsupported input item type %q", item.Type)
		}
		if last := len(reasoning) - 1; last >= 0 && reasoning[last].Text == "" {
			call.ThoughtSignature = reasoning[last].ThoughtSignature
			reasoning = reasoning[:last]
		}
		add(antigravity.Content{Role: antigravity.RoleAssistant, Parts: []antigravity.Part{{FunctionCall: &call}}})
	}
	flushReasoning()
	if len(reminders) > 0 {
		contents = append(contents, antigravity.Content{Role: antigravity.RoleUser, Parts: reminders})
	}
	input.contents = prependSystemMedia(contents, systemMedia)
	return input, nil
}

// responsesItemType 读取输入项的 type 字段
func responsesItemType(raw json.RawMessage) string {
	var head struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(raw, &head)
	return head.Type
}

// responsesHostedItem 把托管工具与 computer、MCP 历史项转为文本摘要，截图与生成图片保留为图片
func responsesHostedItem(kind string, raw json.RawMessage) (antigravity.Content, error) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return antigravity.Content{}, err
	}
	content := antigravity.Content{Role: antigravity.RoleAssistant}
	var media []antigravity.Part
	switch kind {
	case "image_generation_call":
		var result string
		if json.Unmarshal(item["result"], &result) == nil && result != "" {
			data, err := decodeBase64Flexible(result)
			if err != nil {
				return antigravity.Content{}, fmt.Errorf("result: %w", err)
			}
			media = append(media, antigravity.Part{InlineData: &antigravity.Blob{MIME: detectMediaType("", data), Data: data}})
			delete(item, "result")
		}
	case "computer_call_output":
		content.Role = antigravity.RoleUser
		var output struct {
			ImageURL string `json:"image_url"`
		}
		if json.Unmarshal(item["output"], &output) == nil && output.ImageURL != "" {
			part, err := mediaURLPart(output.ImageURL, "", "")
			if err != nil {
				return antigravity.Content{}, fmt.Errorf("output: %w", err)
			}
			media = append(media, part)
			delete(item, "output")
		}
	case "mcp_approval_response":
		content.Role = antigravity.RoleUser
	}
	delete(item, "id")
	delete(item, "type")
	delete(item, "status")
	summary, _ := json.Marshal(item)
	content.Parts = append([]antigravity.Part{{Text: kind + " " + string(summary)}}, media...)
	return content, nil
}

func mapResponsesTools(tools []responsesTool, choice json.RawMessage) (antigravity.Tools, error) {
	var mapped antigravity.Tools
	names := make(map[string]bool)
	addDeclaration := func(declaration antigravity.FunctionDeclaration) error {
		if declaration.Name == "" {
			return fmt.Errorf("function tool name is required")
		}
		if names[declaration.Name] {
			return fmt.Errorf("function tool name %q is duplicated", declaration.Name)
		}
		names[declaration.Name] = true
		mapped.Functions = append(mapped.Functions, declaration)
		return nil
	}
	addTool := func(tool responsesTool) error {
		if tool.Type == "custom" {
			return addDeclaration(customToolDeclaration(tool.Name, tool.Description, tool.Format))
		}
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		return addDeclaration(antigravity.FunctionDeclaration{
			Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: tool.Strict != nil && *tool.Strict,
		})
	}
	for _, tool := range tools {
		switch tool.Type {
		case "function", "custom":
			if err := addTool(tool); err != nil {
				return antigravity.Tools{}, err
			}
		case "local_shell", "shell", "apply_patch":
			if err := addDeclaration(clientToolDeclarations[tool.Type]); err != nil {
				return antigravity.Tools{}, err
			}
		case "namespace":
			if tool.Name == "" {
				return antigravity.Tools{}, fmt.Errorf("namespace name is required")
			}
			for _, inner := range tool.Tools {
				if inner.Type != "function" && inner.Type != "custom" {
					return antigravity.Tools{}, fmt.Errorf("namespace %q tool type %q is not supported", tool.Name, inner.Type)
				}
				inner.Name = tool.Name + "." + inner.Name
				if err := addTool(inner); err != nil {
					return antigravity.Tools{}, err
				}
			}
		case "web_search", "web_search_2025_08_26", "web_search_preview", "web_search_preview_2025_03_11":
			mapped.GoogleSearch = json.RawMessage(`{}`)
		case "image_generation":
			if err := addDeclaration(imageToolDeclaration); err != nil {
				return antigravity.Tools{}, err
			}
		case "code_interpreter", "file_search", "mcp", "computer", "computer_use_preview", "tool_search", "programmatic_tool_calling":
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

// mergeResponsesTools 把 additional_tools 中与顶层同名之外的工具并入工具列表
func mergeResponsesTools(tools, additional []responsesTool) []responsesTool {
	merged := slices.Clone(tools)
	for _, tool := range additional {
		if !slices.ContainsFunc(merged, func(existing responsesTool) bool { return existing.Type == tool.Type && existing.Name == tool.Name }) {
			merged = append(merged, tool)
		}
	}
	return merged
}

// responsesOutput 按事件生成顺序累积 Responses 输出项：思考为 reasoning 项，正文为 message 项，函数调用为调用项；请求 encrypted_content 时签名写入其前的 reasoning 项
type responsesOutput struct {
	id        string
	request   responsesRequest
	items     []map[string]any
	reasoning map[string]any
	message   map[string]any
	images    int
	grounding *antigravity.Grounding
}

// itemID 返回新输出项的 ID：同类首项为前缀加响应 ID，之后的项再加序号
func (o *responsesOutput) itemID(prefix string) string {
	if !slices.ContainsFunc(o.items, func(item map[string]any) bool { return item["id"] == prefix+o.id }) {
		return prefix + o.id
	}
	return fmt.Sprintf("%s%s_%d", prefix, o.id, len(o.items))
}

// add 处理一个事件，返回新增的输出项；reasoning 与正文接在同类项之后时并入该项，否则开始新项；搜索来源作为 web_search_call 排在已有输出项之后
func (o *responsesOutput) add(event antigravity.Event) (added []map[string]any) {
	encrypted := o.request.includesEncrypted()
	last := func() map[string]any {
		if len(o.items) == 0 {
			return nil
		}
		return o.items[len(o.items)-1]
	}
	isLast := func(item map[string]any) bool {
		return len(o.items) > 0 && o.items[len(o.items)-1]["id"] == item["id"]
	}
	signed := func(id, signature string) {
		if signature == "" || !encrypted {
			return
		}
		if item := last(); item != nil && item["type"] == "reasoning" && item["encrypted_content"] == nil {
			item["encrypted_content"] = signature
			return
		}
		item := map[string]any{"id": id, "type": "reasoning", "status": "completed", "summary": []any{}, "encrypted_content": signature}
		o.items = append(o.items, item)
		added = append(added, item)
	}
	switch event.Kind {
	case antigravity.EventReasoning:
		if o.reasoning == nil || !isLast(o.reasoning) {
			o.reasoning = map[string]any{"id": o.itemID("rs_"), "type": "reasoning", "status": "completed", "summary": []any{map[string]any{"type": "summary_text", "text": ""}}}
			o.items = append(o.items, o.reasoning)
			added = append(added, o.reasoning)
		}
		summary := o.reasoning["summary"].([]any)[0].(map[string]any)
		summary["text"] = summary["text"].(string) + event.Text
		signed(fmt.Sprintf("rs_%s_%d", o.id, len(o.items)), event.ThoughtSignature)
	case antigravity.EventThoughtSignature:
		signed(fmt.Sprintf("rs_%s_%d", o.id, len(o.items)), event.ThoughtSignature)
	case antigravity.EventText:
		if o.message == nil || !isLast(o.message) {
			o.message = map[string]any{"id": o.itemID("msg_"), "type": "message", "status": "completed", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}}
			o.items = append(o.items, o.message)
			added = append(added, o.message)
		}
		part := o.message["content"].([]any)[0].(map[string]any)
		part["text"] = part["text"].(string) + event.Text
	case antigravity.EventToolCall:
		if event.ToolCall != nil {
			signed("rs_"+event.ToolCall.ID, event.ToolCall.ThoughtSignature)
			item := responseFunctionCall(*event.ToolCall, o.request.Tools)
			o.items = append(o.items, item)
			added = append(added, item)
		}
	case antigravity.EventMedia:
		id := fmt.Sprintf("ig_%s_%d", o.id, o.images)
		o.images++
		signed("rs_"+id, event.ThoughtSignature)
		item := map[string]any{"id": id, "type": "image_generation_call", "status": "completed", "result": base64.StdEncoding.EncodeToString(event.Media.Data)}
		o.items = append(o.items, item)
		added = append(added, item)
	case antigravity.EventGrounding:
		o.grounding = event.Grounding
		action := map[string]any{"type": "search"}
		if queries := event.Grounding.WebSearchQueries; len(queries) > 0 {
			action["query"], action["queries"] = queries[0], queries
		}
		if slices.Contains(o.request.Include, "web_search_call.action.sources") {
			sources := []any{}
			for _, source := range groundingSources(event.Grounding) {
				sources = append(sources, map[string]any{"type": "url", "url": source.URI})
			}
			action["sources"] = sources
		}
		item := map[string]any{"id": o.itemID("ws_"), "type": "web_search_call", "status": "completed", "action": action}
		o.items = append(o.items, item)
		added = append(added, item)
	}
	return added
}

// annotate 按搜索来源写入 message 项正文的 url_citation 注释，返回写入的注释
func (o *responsesOutput) annotate(message map[string]any) []any {
	if o.grounding == nil {
		return nil
	}
	part := message["content"].([]any)[0].(map[string]any)
	part["annotations"] = responsesAnnotations(part["text"].(string), o.grounding)
	return part["annotations"].([]any)
}

// buildResponsesObject 构造完整响应对象，compaction 不为空时排在输出首位；search 为真时与流式输出相同，正文排在首个搜索来源之后
func buildResponsesObject(id string, created int64, request responsesRequest, result generationResult, compaction map[string]any, search bool) map[string]any {
	collected := &responsesOutput{id: id, request: request}
	add := func(event antigravity.Event) error {
		collected.add(event)
		return nil
	}
	if search {
		add = holdUntilGrounding(add)
	}
	for _, event := range result.events {
		_ = add(event)
	}
	for _, item := range collected.items {
		if item["type"] == "message" {
			collected.annotate(item)
		}
	}
	output := make([]any, 0, 1+len(collected.items))
	if compaction != nil {
		output = append(output, compaction)
	}
	for _, item := range collected.items {
		output = append(output, item)
	}
	status := "completed"
	incompleteReason := ""
	switch openAIFinishReason(result.finishReason, false) {
	case "length":
		status = "incomplete"
		incompleteReason = "max_output_tokens"
	case "content_filter":
		status = "incomplete"
		incompleteReason = "content_filter"
	}
	response := responseShell(id, created, status, request)
	response["completed_at"] = time.Now().Unix()
	if status == "incomplete" {
		response["incomplete_details"] = map[string]any{"reason": incompleteReason}
	}
	response["output"] = output
	response["output_text"] = result.text.String()
	if result.providerModel != "" {
		response["provider_model"] = result.providerModel
	}
	if result.usage != nil {
		response["usage"] = responsesUsage(result.usage)
	}
	return response
}

func responseShell(id string, created int64, status string, request responsesRequest) map[string]any {
	parallelToolCalls := true
	if request.ParallelToolCalls != nil {
		parallelToolCalls = *request.ParallelToolCalls
	}
	truncation := request.Truncation
	if truncation == "" {
		truncation = "disabled"
	}
	var instructions any
	if request.Instructions != "" {
		instructions = request.Instructions
	}
	text := rawJSONValue(request.Text, map[string]any{"format": map[string]string{"type": "text"}})
	reasoning := rawJSONValue(request.Reasoning, nil)
	toolChoice := rawJSONValue(request.ToolChoice, "auto")
	tools := request.Tools
	if tools == nil {
		tools = []responsesTool{}
	}
	var previousResponseID any
	if request.PreviousResponseID != "" {
		previousResponseID = request.PreviousResponseID
	}
	return map[string]any{
		"id":                   id,
		"object":               "response",
		"created_at":           created,
		"completed_at":         nil,
		"status":               status,
		"error":                nil,
		"incomplete_details":   nil,
		"instructions":         instructions,
		"metadata":             request.Metadata,
		"model":                request.Model,
		"output":               []any{},
		"output_text":          "",
		"parallel_tool_calls":  parallelToolCalls,
		"previous_response_id": previousResponseID,
		"background":           request.Background,
		"reasoning":            reasoning,
		"temperature":          request.Temperature,
		"text":                 text,
		"tool_choice":          toolChoice,
		"tools":                tools,
		"top_p":                request.TopP,
		"truncation":           truncation,
		"max_output_tokens":    request.MaxOutputTokens,
		"usage":                nil,
	}
}

func rawJSONValue(raw json.RawMessage, defaultValue any) any {
	if len(raw) == 0 || string(raw) == "null" {
		return defaultValue
	}
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}

func rawJSONConfigured(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null"
}

// responseFunctionCall 按声明的工具类型把函数调用投影为 Responses 输出项
func responseFunctionCall(call antigravity.FunctionCall, tools []responsesTool) map[string]any {
	item := map[string]any{"id": "fc_" + call.ID, "status": "completed", "call_id": call.ID}
	switch responsesToolKind(tools, call.Name) {
	case "local_shell":
		item["type"], item["action"] = "local_shell_call", localShellAction(call.Arguments)
		return item
	case "shell":
		item["type"], item["action"], item["environment"] = "shell_call", shellAction(call.Arguments), nil
		return item
	case "apply_patch":
		item["type"], item["operation"] = "apply_patch_call", rawJSONValue(call.Arguments, map[string]any{})
		return item
	case "custom":
		item["type"], item["name"], item["input"] = "custom_tool_call", call.Name, customToolInput(call.Arguments)
	default:
		item["type"], item["name"], item["arguments"] = "function_call", call.Name, string(call.Arguments)
	}
	if namespace := responsesNamespace(tools, call.Name); namespace != "" {
		item["namespace"] = namespace
		item["name"] = strings.TrimPrefix(call.Name, namespace+".")
	}
	return item
}

// responsesNamespace 返回函数所属的命名空间工具名
func responsesNamespace(tools []responsesTool, name string) string {
	for _, tool := range tools {
		if tool.Type != "namespace" {
			continue
		}
		for _, inner := range tool.Tools {
			if tool.Name+"."+inner.Name == name {
				return tool.Name
			}
		}
	}
	return ""
}

func responsesUsage(usage *antigravity.Usage) map[string]any {
	return map[string]any{
		"input_tokens":  inputTokens(usage),
		"output_tokens": outputTokens(usage),
		"total_tokens":  usage.TotalTokens,
		"input_tokens_details": map[string]any{
			"cached_tokens":      usage.CachedTokens,
			"cache_write_tokens": 0,
		},
		"output_tokens_details": map[string]any{
			"reasoning_tokens": usage.ReasoningTokens,
		},
	}
}

// responsesStreamWriter 把规范事件写成 Responses 流，输出项与非流式响应按同一顺序与内容累积
type responsesStreamWriter struct {
	w        http.ResponseWriter
	sequence int
	id       string
	created  int64
	request  responsesRequest
	output   *responsesOutput
	open     []map[string]any
	base     int
}

func (s *server) streamResponses(w http.ResponseWriter, r *http.Request, request responsesRequest, prepared responsesPreparation, id string, created int64, events <-chan antigravity.Event, compaction map[string]any) {
	if err := streamHeaders(w); err != nil {
		return
	}
	writer := &responsesStreamWriter{w: w, id: id, created: created, request: request, output: &responsesOutput{id: id, request: request}}
	if err := writer.emit("response.created", map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
		return
	}
	if err := writer.emit("response.in_progress", map[string]any{"response": responseShell(id, created, "in_progress", request)}); err != nil {
		return
	}
	s.streamOutput(r.Context(), writer, prepared, events, compaction, func(err error) bool { return shouldWriteRequestError(r, err) })
}

// streamOutput 写出输出项事件与终止事件并保存最终响应，report 决定生成出错时是否写出 response.failed；返回生成或写出的错误
func (s *server) streamOutput(ctx context.Context, writer *responsesStreamWriter, prepared responsesPreparation, events <-chan antigravity.Event, compaction map[string]any, report func(error) bool) error {
	id, created, request, w := writer.id, writer.created, writer.request, writer.w
	if compaction != nil {
		writer.base = 1
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": 0, "item": compaction}); err != nil {
			return err
		}
		if err := writer.emit("response.output_item.done", map[string]any{"output_index": 0, "item": compaction}); err != nil {
			return err
		}
	}
	emit := writer.live
	if searchOnly(prepared.generate.Tools) {
		emit = holdUntilGrounding(writer.live)
	}
	result, err := consumeStreamEvents(ctx, events, emit, func() error { return writeSSEHeartbeat(w) })
	if err != nil {
		if report(err) {
			_ = writer.failed(err)
		}
		return err
	}
	response := buildResponsesObject(id, created, request, result, compaction, searchOnly(prepared.generate.Tools))
	completeErr := writer.complete()
	if request.storesResponse() {
		s.storeResponse(id, request, prepared, result, response)
	}
	if completeErr != nil {
		_ = writer.failed(completeErr)
		return completeErr
	}
	if err := writer.terminal(response); err != nil {
		_ = writer.failed(err)
		return err
	}
	return nil
}

func (writer *responsesStreamWriter) emit(eventType string, payload map[string]any) error {
	payload["type"] = eventType
	payload["sequence_number"] = writer.sequence
	writer.sequence++
	return writeSSE(writer.w, eventType, payload)
}

// index 返回输出项在响应 output 中的位置
func (writer *responsesStreamWriter) index(item map[string]any) int {
	return writer.base + slices.IndexFunc(writer.output.items, func(candidate map[string]any) bool { return candidate["id"] == item["id"] })
}

// live 写出一个事件：新输出项开始前先完成仍打开的项，带摘要的 reasoning 与 message 项保持打开到下一个输出项或结束，函数调用项与只带签名的 reasoning 项立即完成
func (writer *responsesStreamWriter) live(event antigravity.Event) error {
	for _, item := range writer.output.add(event) {
		if err := writer.added(item); err != nil {
			return err
		}
	}
	switch event.Kind {
	case antigravity.EventReasoning:
		return writer.emit("response.reasoning_summary_text.delta", map[string]any{
			"item_id": writer.output.reasoning["id"], "output_index": writer.index(writer.output.reasoning), "summary_index": 0, "delta": event.Text,
		})
	case antigravity.EventText:
		return writer.emit("response.output_text.delta", map[string]any{
			"item_id": writer.output.message["id"], "output_index": writer.index(writer.output.message), "content_index": 0, "delta": event.Text, "logprobs": []any{},
		})
	}
	return nil
}

// added 完成仍打开的项后写出新输出项的 added 事件；函数调用与只带签名的 reasoning 项随即写出参数与 done 事件
func (writer *responsesStreamWriter) added(item map[string]any) error {
	if err := writer.complete(); err != nil {
		return err
	}
	index := writer.index(item)
	switch {
	case item["type"] == "reasoning" && len(item["summary"].([]any)) > 0:
		pending := map[string]any{"id": item["id"], "type": "reasoning", "status": "in_progress", "summary": []any{}}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		writer.open = append(writer.open, item)
		return writer.emit("response.reasoning_summary_part.added", map[string]any{
			"item_id": item["id"], "output_index": index, "summary_index": 0,
			"part": map[string]any{"type": "summary_text", "text": ""},
		})
	case item["type"] == "message":
		pending := map[string]any{"id": item["id"], "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		writer.open = append(writer.open, item)
		return writer.emit("response.content_part.added", map[string]any{
			"item_id": item["id"], "output_index": index, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		})
	case item["type"] == "reasoning":
		pending := maps.Clone(item)
		pending["status"] = "in_progress"
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	case item["type"] == "web_search_call":
		pending := map[string]any{"id": item["id"], "type": "web_search_call", "status": "in_progress", "action": item["action"]}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		for _, stage := range []string{"in_progress", "searching", "completed"} {
			if err := writer.emit("response.web_search_call."+stage, map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
				return err
			}
		}
		return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	case item["type"] == "image_generation_call":
		pending := map[string]any{"id": item["id"], "type": "image_generation_call", "status": "in_progress", "result": nil}
		if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": pending}); err != nil {
			return err
		}
		if err := writer.emit("response.image_generation_call.in_progress", map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
			return err
		}
		if err := writer.emit("response.image_generation_call.completed", map[string]any{"item_id": item["id"], "output_index": index}); err != nil {
			return err
		}
		return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item})
	}
	return writer.toolCall(item, index)
}

// toolCall 写出函数调用项的 added、参数增量与 done 事件
func (writer *responsesStreamWriter) toolCall(completed map[string]any, index int) error {
	id := completed["id"]
	item := maps.Clone(completed)
	item["status"] = "in_progress"
	switch completed["type"] {
	case "function_call":
		item["arguments"] = ""
	case "custom_tool_call":
		item["input"] = ""
	}
	if err := writer.emit("response.output_item.added", map[string]any{"output_index": index, "item": item}); err != nil {
		return err
	}
	switch completed["type"] {
	case "function_call":
		if err := writer.emit("response.function_call_arguments.delta", map[string]any{
			"item_id": id, "output_index": index, "delta": completed["arguments"],
		}); err != nil {
			return err
		}
		if err := writer.emit("response.function_call_arguments.done", map[string]any{
			"item_id": id, "output_index": index, "arguments": completed["arguments"], "name": completed["name"],
		}); err != nil {
			return err
		}
	case "custom_tool_call":
		if err := writer.emit("response.custom_tool_call_input.delta", map[string]any{
			"item_id": id, "output_index": index, "delta": completed["input"],
		}); err != nil {
			return err
		}
		if err := writer.emit("response.custom_tool_call_input.done", map[string]any{
			"item_id": id, "output_index": index, "input": completed["input"],
		}); err != nil {
			return err
		}
	}
	return writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": completed})
}

// complete 结束仍打开的 reasoning 与 message 项
func (writer *responsesStreamWriter) complete() error {
	open := writer.open
	writer.open = nil
	for _, item := range open {
		index := writer.index(item)
		if item["type"] == "reasoning" {
			part := item["summary"].([]any)[0]
			text := part.(map[string]any)["text"]
			if err := writer.emit("response.reasoning_summary_text.done", map[string]any{
				"item_id": item["id"], "output_index": index, "summary_index": 0, "text": text,
			}); err != nil {
				return err
			}
			if err := writer.emit("response.reasoning_summary_part.done", map[string]any{
				"item_id": item["id"], "output_index": index, "summary_index": 0, "part": part,
			}); err != nil {
				return err
			}
		} else {
			for position, annotation := range writer.output.annotate(item) {
				if err := writer.emit("response.output_text.annotation.added", map[string]any{
					"item_id": item["id"], "output_index": index, "content_index": 0, "annotation_index": position, "annotation": annotation,
				}); err != nil {
					return err
				}
			}
			part := item["content"].([]any)[0]
			if err := writer.emit("response.output_text.done", map[string]any{
				"item_id": item["id"], "output_index": index, "content_index": 0, "text": part.(map[string]any)["text"], "logprobs": []any{},
			}); err != nil {
				return err
			}
			if err := writer.emit("response.content_part.done", map[string]any{
				"item_id": item["id"], "output_index": index, "content_index": 0, "part": part,
			}); err != nil {
				return err
			}
		}
		if err := writer.emit("response.output_item.done", map[string]any{"output_index": index, "item": item}); err != nil {
			return err
		}
	}
	return nil
}

// terminal 发送携带完整响应对象的终止事件
func (writer *responsesStreamWriter) terminal(response map[string]any) error {
	eventType := "response.completed"
	if response["status"] == "incomplete" {
		eventType = "response.incomplete"
	}
	return writer.emit(eventType, map[string]any{"response": response})
}

// failed 发送 response.failed：output 为已写出的输出项，error.code 取 Responses 错误枚举
func (writer *responsesStreamWriter) failed(err error) error {
	response := responseShell(writer.id, writer.created, "failed", writer.request)
	output := make([]any, 0, len(writer.output.items))
	for _, item := range writer.output.items {
		output = append(output, item)
	}
	response["output"] = output
	response["error"] = map[string]any{"code": responsesErrorCode(err), "message": err.Error()}
	return writer.emit("response.failed", map[string]any{"response": response})
}

// responsesErrorCode 返回 Responses 失败响应 error.code 的枚举值：限流为 rate_limit_exceeded，请求内容被拒为 invalid_prompt，其余为 server_error
func responsesErrorCode(err error) string {
	switch statusFromError(err) {
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	case http.StatusBadRequest:
		return "invalid_prompt"
	}
	return "server_error"
}
