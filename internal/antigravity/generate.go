package antigravity

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// sessionID 为官方客户端 request.sessionId 的固定值，即 FNV-1a 64 位初值的有符号表示
	sessionID = "-3750763034362895579"
	// requestTypeAgent 为 Agent 对话请求的 requestType
	requestTypeAgent = "agent"
	// envelopeUserAgent 为信封中的 userAgent
	envelopeUserAgent = "antigravity"
	// wireRoleUser 为上游 contents 与 systemInstruction 的用户角色
	wireRoleUser = "user"
	// wireRoleModel 为上游 contents 的模型角色
	wireRoleModel = "model"
)

// Conversation 标识一条会话在上游的 cascade、trajectory、已完成的模型轮数、历史步骤数与上一次执行
type Conversation struct {
	CascadeID       string
	TrajectoryID    string
	Turn            int
	Step            int
	Execution       int
	LastExecutionID string
}

// ErrIncompleteStream 表示上游流在出现 finishReason 前结束
var ErrIncompleteStream = errors.New("upstream stream ended without finishReason")

// filterBlockedText 为上游过滤提示的开头，该提示以不带 role 的 candidate 正文代替回答
const filterBlockedText = "This request was blocked by Gemini's filters."

// restrictedText 为上游对受限账户的通知开头，该通知以不带 role 的 candidate 正文代替回答
const restrictedText = "Request is blocked due to the account restrictions"

// AccountRestrictedError 表示上游以账户受限通知拒绝请求，Message 为通知原文
type AccountRestrictedError struct {
	Message string
}

// Error 返回带通知原文的错误说明
func (e *AccountRestrictedError) Error() string {
	return "account restricted by upstream: " + e.Message
}

// HTTPStatus 返回公开协议使用的状态码
func (e *AccountRestrictedError) HTTPStatus() int {
	return http.StatusForbidden
}

// ContentPolicyError 表示上游以内容策略类原因结束生成，Reason 为上游 finishReason（过滤提示为 OTHER），Message 为上游 finishMessage 或过滤提示原文
type ContentPolicyError struct {
	Reason  string
	Message string
}

func (e *ContentPolicyError) Error() string {
	if e.Message != "" {
		return "上游因内容策略结束生成: " + e.Reason + ": " + e.Message
	}
	return "上游因内容策略结束生成: " + e.Reason
}

// AbnormalFinishError 表示上游以格式错误的函数调用、意外的工具调用或格式错误的回复结束、重新生成或续写用完仍如此且这次生成没有函数调用，Usage 为上游最后给出的用量
type AbnormalFinishError struct {
	Reason  string
	Message string
	Usage   *Usage
}

func (e *AbnormalFinishError) Error() string {
	if e.Message != "" {
		return "上游异常结束生成: " + e.Reason + ": " + e.Message
	}
	return "上游异常结束生成: " + e.Reason
}

// PromptFeedbackError 表示上游以 promptFeedback 按输入本身拒绝请求且没有返回候选，同一输入重发仍被拒绝；Reason 为 blockReason，Message 为 blockReasonMessage，Usage 为上游计入的输入用量
type PromptFeedbackError struct {
	Reason  string
	Message string
	Usage   *Usage
}

func (e *PromptFeedbackError) Error() string {
	if e.Message != "" {
		return "上游拒绝当前输入: " + e.Reason + ": " + e.Message
	}
	return "上游拒绝当前输入: " + e.Reason
}

// HTTPStatus 返回输入拒绝对应的 400
func (e *PromptFeedbackError) HTTPStatus() int {
	return http.StatusBadRequest
}

// inputBlock 判断 promptFeedback 的 blockReason 是否表示按输入本身拒绝：安全设置、屏蔽词与禁止内容
func inputBlock(reason string) bool {
	switch reason {
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT":
		return true
	}
	return false
}

// contentPolicyFinish 判断 finishReason 是否属于内容策略类：安全、背诵、屏蔽词、禁止内容、个人敏感信息与对应的图片原因
func contentPolicyFinish(reason string) bool {
	switch reason {
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "IMAGE_SAFETY", "IMAGE_PROHIBITED_CONTENT", "IMAGE_RECITATION":
		return true
	}
	return false
}

// NewConversation 以会话的 cascade 与 trajectory 标识和规范历史推导步序号：Turn 为历史中的模型轮数，Step 为历史按官方步骤模型展开的步骤数，Execution 为当前用户请求的序号
func NewConversation(cascade, trajectory string, contents []Content) Conversation {
	turn, execution, step := 0, -1, 0
	for _, content := range contents {
		switch {
		case content.Role == RoleAssistant:
			turn++
			step++
		case userRequest(content):
			execution++
			step++
		default:
			for _, part := range content.Parts {
				if part.FunctionResult != nil {
					step++
				}
			}
		}
	}
	conversation := Conversation{CascadeID: cascade, TrajectoryID: trajectory, Turn: turn, Step: step, Execution: max(execution, 0)}
	if execution > 0 {
		conversation.LastExecutionID = conversation.ExecutionID(execution - 1)
	}
	return conversation
}

// ExecutionID 返回会话中第 index 个用户请求的执行标识
func (c Conversation) ExecutionID(index int) string {
	return uuidOf("execution:" + c.CascadeID + ":" + strconv.Itoa(index))
}

// userRequest 判断消息是否为一次新的用户请求，只含工具结果或夹带工具结果的轮属于上一次执行
func userRequest(content Content) bool {
	if content.Role != RoleUser {
		return false
	}
	for _, part := range content.Parts {
		if part.FunctionResult != nil {
			return false
		}
	}
	return len(content.Parts) != 0
}

// uuidOf 把文本的 SHA-256 前 16 字节写成 UUIDv4 形式
func uuidOf(text string) string {
	sum := sha256.Sum256([]byte(text))
	sum[6] = sum[6]&0x0f | 0x40
	sum[8] = sum[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

// Envelope 表示 streamGenerateContent 的请求体
type Envelope struct {
	Project     string      `json:"project"`
	RequestID   string      `json:"requestId"`
	Request     WireRequest `json:"request"`
	Model       string      `json:"model"`
	UserAgent   string      `json:"userAgent"`
	RequestType string      `json:"requestType"`
	// EnabledCreditTypes 是额度用尽后可用的积分类型，账户开启 AI 积分超额使用时为 GOOGLE_ONE_AI
	EnabledCreditTypes []string `json:"enabledCreditTypes,omitempty"`
	// payload 是 Client.Encode 按发出编码写好的请求正文
	payload []byte
}

// CreditGoogleOneAI 是官方客户端开启 Enable AI Credit Overages 后 agent 信封带的积分类型
const CreditGoogleOneAI = "GOOGLE_ONE_AI"

// WireRequest 表示信封内的 Gemini 形状请求
type WireRequest struct {
	Contents          []WireContent        `json:"contents"`
	SystemInstruction *WireContent         `json:"systemInstruction,omitempty"`
	Tools             []WireTool           `json:"tools,omitempty"`
	SafetySettings    []SafetySetting      `json:"safetySettings,omitempty"`
	Labels            map[string]string    `json:"labels,omitempty"`
	GenerationConfig  WireGenerationConfig `json:"generationConfig"`
	SessionID         string               `json:"sessionId"`
	ContinuationToken string               `json:"continuationToken,omitempty"`
}

// WireContent 表示上游的一条消息
type WireContent struct {
	Role  string     `json:"role"`
	Parts []WirePart `json:"parts"`
}

// WirePart 表示上游消息中的一个内容块
type WirePart struct {
	Text             string                `json:"text,omitempty"`
	InlineData       *WireBlob             `json:"inlineData,omitempty"`
	FileData         *WireFileData         `json:"fileData,omitempty"`
	VideoMetadata    json.RawMessage       `json:"videoMetadata,omitempty"`
	MediaResolution  json.RawMessage       `json:"mediaResolution,omitempty"`
	FunctionCall     *WireFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *WireFunctionResponse `json:"functionResponse,omitempty"`
	Thought          bool                  `json:"thought,omitempty"`
	ThoughtSignature string                `json:"thoughtSignature,omitempty"`
}

// WireBlob 表示内联媒体
type WireBlob struct {
	MIMEType string `json:"mimeType"`
	Data     []byte `json:"data"`
}

// WireFileData 表示按 URI 引用的媒体
type WireFileData struct {
	MIMEType string `json:"mimeType,omitempty"`
	FileURI  string `json:"fileUri"`
}

// WireFunctionCall 表示上游的函数调用
type WireFunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// WireFunctionResponse 表示回传上游的函数结果，response 为官方客户端的 {"output": 文本}，parts 为结果中的媒体
type WireFunctionResponse struct {
	ID       string `json:"id,omitempty"`
	Name     string `json:"name"`
	Response struct {
		Output string `json:"output"`
	} `json:"response"`
	Parts []WirePart `json:"parts,omitempty"`
}

// WireTool 表示一组函数声明或一个内置工具
type WireTool struct {
	FunctionDeclarations []WireFunctionDeclaration `json:"functionDeclarations,omitempty"`
	GoogleSearch         json.RawMessage           `json:"googleSearch,omitempty"`
}

// WireFunctionDeclaration 表示上游的函数声明
type WireFunctionDeclaration struct {
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	Parameters         json.RawMessage `json:"parameters,omitempty"`
	Response           json.RawMessage `json:"response,omitempty"`
	ResponseJSONSchema json.RawMessage `json:"responseJsonSchema,omitempty"`
	Behavior           string          `json:"behavior,omitempty"`
}

// WireThinkingConfig 表示思考档位，按目录取 thinkingLevel 或 thinkingBudget；thinkingLevel 为枚举名，没有枚举名的目录值以数字发出
type WireThinkingConfig struct {
	IncludeThoughts bool   `json:"includeThoughts"`
	ThinkingLevel   any    `json:"thinkingLevel,omitempty"`
	ThinkingBudget  *int64 `json:"thinkingBudget,omitempty"`
}

// WireGenerationConfig 表示上游生成参数，采样参数只发给接受且生效的模型族
type WireGenerationConfig struct {
	MaxOutputTokens  int64              `json:"maxOutputTokens"`
	ThinkingConfig   WireThinkingConfig `json:"thinkingConfig"`
	Temperature      *float64           `json:"temperature,omitempty"`
	TopP             *float64           `json:"topP,omitempty"`
	TopK             *int               `json:"topK,omitempty"`
	Seed             *int64             `json:"seed,omitempty"`
	PresencePenalty  *float64           `json:"presencePenalty,omitempty"`
	FrequencyPenalty *float64           `json:"frequencyPenalty,omitempty"`
	MediaResolution  string             `json:"mediaResolution,omitempty"`
}

// thinkingLevels 为目录 thinkingLevel 中有枚举名的取值，其余取值（如 XHigh 档的 100）按官方客户端以数字发出
var thinkingLevels = map[int]string{1: "LOW", 2: "MEDIUM", 3: "HIGH"}

// BuildEnvelope 按官方客户端格式组装一次 Agent 生成请求信封
// 参数筛选、工具声明改写与函数结果配对见 docs/protocol.md“信封”与“工具声明”
func BuildEnvelope(project string, model CatalogModel, conversation Conversation, system string, contents []Content, tools Tools, config GenerationConfig, now time.Time) (Envelope, error) {
	wired, err := wireContents(contents, model)
	if err != nil {
		return Envelope{}, err
	}
	request := WireRequest{
		Contents:  wired,
		Labels:    labels(model, conversation),
		SessionID: sessionID,
		GenerationConfig: WireGenerationConfig{
			MaxOutputTokens: model.MaxOutputTokens,
			ThinkingConfig:  WireThinkingConfig{IncludeThoughts: true},
		},
	}
	if level, ok := thinkingLevels[model.ThinkingLevel]; ok {
		request.GenerationConfig.ThinkingConfig.ThinkingLevel = level
	} else if model.ThinkingLevel != 0 {
		request.GenerationConfig.ThinkingConfig.ThinkingLevel = model.ThinkingLevel
	} else {
		request.GenerationConfig.ThinkingConfig.ThinkingBudget = model.ThinkingBudget
	}
	if system != "" {
		request.SystemInstruction = &WireContent{Role: wireRoleUser, Parts: []WirePart{{Text: system}}}
	}
	for _, function := range tools.Functions {
		description := function.Description
		if description == "" && model.ModelProvider == ProviderOpenAI {
			description = function.Name
		}
		parameters := function.Parameters
		if model.ModelProvider == ProviderAnthropic && len(parameters) > 0 {
			var lossy bool
			if parameters, lossy, err = claudeParameters(parameters); err != nil {
				return Envelope{}, fmt.Errorf("function %q parameters: %w", function.Name, err)
			}
			if lossy && !strings.Contains(description, schemaArgumentsNote) {
				description += schemaArgumentsNote + string(function.Schema)
			}
		}
		request.Tools = append(request.Tools, WireTool{FunctionDeclarations: []WireFunctionDeclaration{{
			Name: function.Name, Description: description, Parameters: parameters,
			Response: function.Response, ResponseJSONSchema: function.ResponseJSONSchema, Behavior: function.Behavior,
		}}})
	}
	generation := &request.GenerationConfig
	switch model.ModelProvider {
	case ProviderGoogle:
		if len(tools.GoogleSearch) != 0 && len(tools.Functions) == 0 {
			request.Tools = append(request.Tools, WireTool{GoogleSearch: tools.GoogleSearch})
		}
		generation.Temperature, generation.TopP, generation.TopK, generation.Seed = config.Temperature, config.TopP, config.TopK, config.Seed
		generation.MediaResolution = config.MediaResolution
		request.SafetySettings = config.SafetySettings
	case ProviderOpenAI:
		generation.Temperature, generation.TopP, generation.TopK, generation.Seed = config.Temperature, config.TopP, config.TopK, config.Seed
		generation.PresencePenalty, generation.FrequencyPenalty = config.PresencePenalty, config.FrequencyPenalty
	}
	return Envelope{
		Project:     project,
		RequestID:   fmt.Sprintf("%s/%s/%d/%s/%d", requestTypeAgent, conversation.CascadeID, now.UnixMilli(), conversation.TrajectoryID, conversation.Step),
		Request:     request,
		Model:       model.ID,
		UserAgent:   envelopeUserAgent,
		RequestType: requestTypeAgent,
	}, nil
}

// labels 生成官方客户端随请求附带的 labels
func labels(model CatalogModel, conversation Conversation) map[string]string {
	claude := model.ModelProvider == ProviderAnthropic
	values := map[string]string{
		"cascade_id":               conversation.CascadeID,
		"root_cascade_id":          conversation.CascadeID,
		"trajectory_id":            conversation.TrajectoryID,
		"request_id":               conversation.TrajectoryID + "-" + strconv.Itoa(conversation.Turn),
		"last_step_index":          strconv.Itoa(conversation.Step - 1),
		"model_enum":               model.Model,
		"used_claude":              strconv.FormatBool(claude),
		"used_claude_conservative": strconv.FormatBool(claude),
		"used_non_gemini_model":    strconv.FormatBool(model.ModelProvider != ProviderGoogle),
	}
	if conversation.LastExecutionID != "" {
		values["last_execution_id"] = conversation.LastExecutionID
	}
	return values
}

// wireContents 把规范消息转换为上游 contents，并按模型族处理函数调用与函数结果
// 角色映射、合并与 Claude 族中断结果补齐规则见 docs/protocol.md“contents 与工具往返编码”
func wireContents(contents []Content, model CatalogModel) ([]WireContent, error) {
	claude := model.ModelProvider == ProviderAnthropic
	google := model.ModelProvider == ProviderGoogle
	if !google {
		contents = slices.Clone(contents)
		for index := range contents {
			contents[index] = identifyCalls(contents[index], index)
		}
	}
	contents = regroupResults(contents)
	var wired []WireContent
	var pendingCalls []FunctionCall
	answering := false
	for index, content := range contents {
		answered := slices.ContainsFunc(content.Parts, func(part Part) bool { return part.FunctionResult != nil })
		if claude && len(pendingCalls) > 0 && (content.Role == RoleUser && !answered || content.Role == RoleAssistant && answering) {
			wired = appendResults(wired, wireRoleUser, interruptedResults(pendingCalls))
			pendingCalls = nil
		}
		answering = content.Role != RoleAssistant
		content, err := linkFunctionResults(content, &pendingCalls)
		if err != nil {
			return nil, fmt.Errorf("contents[%d]: %w", index, err)
		}
		if content.Role == RoleAssistant {
			wired = appendContent(wired, wireRoleModel, modelParts(content.Parts, claude, google))
			continue
		}
		var results, rest, ordered []WirePart
		for _, part := range content.Parts {
			item := wirePart(part, google)
			if part.FunctionResult != nil {
				results = append(results, item)
			} else {
				rest = append(rest, item)
			}
			ordered = append(ordered, item)
		}
		switch {
		case len(results) == 0:
			wired = appendContent(wired, wireRoleUser, ordered)
		case claude:
			wired = appendResults(wired, wireRoleUser, ordered)
		default:
			wired = appendContent(appendResults(wired, wireRoleModel, results), wireRoleUser, rest)
		}
	}
	if claude && len(pendingCalls) > 0 && answering {
		wired = appendResults(wired, wireRoleUser, interruptedResults(pendingCalls))
	}
	return wired, nil
}

// appendResults 追加含函数结果的上游消息，前一条消息同角色且只含函数结果时并入该消息
func appendResults(wired []WireContent, role string, parts []WirePart) []WireContent {
	if last := len(wired) - 1; last >= 0 && wired[last].Role == role && !slices.ContainsFunc(wired[last].Parts, func(part WirePart) bool { return part.FunctionResponse == nil }) {
		wired[last].Parts = append(wired[last].Parts, parts...)
		return wired
	}
	return appendContent(wired, role, parts)
}

// appendContent 追加一条上游消息，parts 为空时不追加
func appendContent(wired []WireContent, role string, parts []WirePart) []WireContent {
	if len(parts) == 0 {
		return wired
	}
	return append(wired, WireContent{Role: role, Parts: parts})
}

// wirePart 转换一个非思考 part：YouTube 等远程视频在 Gemini 族为 fileData、其他族为链接文本，videoMetadata 与 mediaResolution 只发给 Gemini 族，函数结果中的媒体放进 functionResponse.parts
func wirePart(part Part, google bool) WirePart {
	wired := WirePart{Text: part.Text, ThoughtSignature: part.ThoughtSignature}
	if google {
		wired.VideoMetadata, wired.MediaResolution = part.VideoMetadata, part.MediaResolution
	}
	if part.InlineData != nil {
		wired.InlineData = &WireBlob{MIMEType: part.InlineData.MIME, Data: part.InlineData.Data}
	}
	if part.File != nil {
		if google {
			wired.FileData = &WireFileData{MIMEType: part.File.MIME, FileURI: part.File.ID}
		} else {
			wired = WirePart{Text: part.File.ID}
		}
	}
	if part.FunctionCall != nil {
		wired.FunctionCall = &WireFunctionCall{ID: part.FunctionCall.ID, Name: part.FunctionCall.Name, Args: canonicalJSON(part.FunctionCall.Arguments)}
		if wired.ThoughtSignature == "" {
			wired.ThoughtSignature = part.FunctionCall.ThoughtSignature
		}
	}
	if result := part.FunctionResult; result != nil {
		wired.FunctionResponse = &WireFunctionResponse{ID: result.ID, Name: result.Name}
		wired.FunctionResponse.Response.Output = functionOutput(result.Content)
		for _, media := range result.Parts {
			wired.FunctionResponse.Parts = append(wired.FunctionResponse.Parts, wirePart(media, google))
		}
	}
	return wired
}

// modelParts 转换 assistant 消息：思考文本只在 Claude 族且本轮带签名时保留，Gemini 族首个函数调用没有签名时带跳过校验的占位签名，只有签名的 part 并入相邻正文
func modelParts(parts []Part, claude, google bool) []WirePart {
	signed := slices.ContainsFunc(parts, func(part Part) bool {
		return part.ThoughtSignature != "" || part.FunctionCall != nil && part.FunctionCall.ThoughtSignature != ""
	})
	var wired []WirePart
	called := false
	for _, part := range parts {
		if part.Thought && (!claude || !signed || part.Text == "") {
			continue
		}
		item := wirePart(part, google)
		if google && part.FunctionCall != nil && !called && item.ThoughtSignature == "" {
			item.ThoughtSignature = skipThoughtSignature
		}
		called = called || part.FunctionCall != nil
		wired = append(wired, item)
	}
	return attachSignatures(wired)
}

// skipThoughtSignature 为 Gemini API 文档给出的跳过签名校验的占位签名
const skipThoughtSignature = "skip_thought_signature_validator"

// attachSignatures 把只有签名的 part 并入其后、其次其前不带签名的正文，Claude 族签名随后续回答、Gemini 族签名随前面的回答回放
func attachSignatures(parts []WirePart) []WirePart {
	text := func(index int) bool {
		return index >= 0 && index < len(parts) && parts[index].Text != "" && !parts[index].Thought && parts[index].ThoughtSignature == ""
	}
	merged := parts[:0:0]
	for index := 0; index < len(parts); index++ {
		part := parts[index]
		if part.ThoughtSignature == "" || part.Text != "" || part.Thought || part.InlineData != nil || part.FileData != nil || part.FunctionCall != nil || part.FunctionResponse != nil {
			merged = append(merged, part)
			continue
		}
		switch {
		case text(index + 1):
			parts[index+1].ThoughtSignature = part.ThoughtSignature
		case len(merged) > 0 && merged[len(merged)-1].Text != "" && !merged[len(merged)-1].Thought && merged[len(merged)-1].ThoughtSignature == "":
			merged[len(merged)-1].ThoughtSignature = part.ThoughtSignature
		default:
			merged = append(merged, part)
		}
	}
	return merged
}

// identifyCalls 为消息中没有 ID 的函数调用生成 call_<消息序号>_<part 序号>
func identifyCalls(content Content, index int) Content {
	cloned := false
	for position, part := range content.Parts {
		if part.FunctionCall == nil || part.FunctionCall.ID != "" {
			continue
		}
		if !cloned {
			content.Parts = slices.Clone(content.Parts)
			cloned = true
		}
		call := *part.FunctionCall
		call.ID = fmt.Sprintf("call_%d_%d", index, position)
		content.Parts[position].FunctionCall = &call
	}
	return content
}

// regroupResults 把与其调用之间隔着不含函数结果的 user 轮的函数结果按调用 ID 移到调用所在轮之后的结果轮
func regroupResults(contents []Content) []Content {
	calls := map[string]int{}
	separated := map[string]bool{}
	var regrouped []Content
	for _, content := range contents {
		if content.Role == RoleAssistant {
			for _, part := range content.Parts {
				if part.FunctionCall != nil && part.FunctionCall.ID != "" {
					calls[part.FunctionCall.ID] = len(regrouped)
					delete(separated, part.FunctionCall.ID)
				}
			}
			regrouped = append(regrouped, content)
			continue
		}
		if !slices.ContainsFunc(content.Parts, func(part Part) bool { return part.FunctionResult != nil }) {
			if content.Role == RoleUser {
				for id := range calls {
					separated[id] = true
				}
			}
			regrouped = append(regrouped, content)
			continue
		}
		var kept []Part
		for _, part := range content.Parts {
			result := part.FunctionResult
			if result == nil || !separated[result.ID] {
				if result != nil {
					delete(calls, result.ID)
				}
				kept = append(kept, part)
				continue
			}
			target := calls[result.ID] + 1
			delete(calls, result.ID)
			delete(separated, result.ID)
			if target < len(regrouped) && regrouped[target].Role != RoleAssistant && !slices.ContainsFunc(regrouped[target].Parts, func(part Part) bool { return part.FunctionResult == nil }) {
				regrouped[target].Parts = append(slices.Clone(regrouped[target].Parts), part)
				continue
			}
			regrouped = slices.Insert(regrouped, target, Content{Role: content.Role, Parts: []Part{part}})
			for id, position := range calls {
				if position >= target {
					calls[id] = position + 1
				}
			}
		}
		if len(kept) > 0 {
			content.Parts = kept
			regrouped = append(regrouped, content)
		}
	}
	return regrouped
}

// linkFunctionResults 按调用 ID 与名称把函数结果配对到待返回的函数调用，未配对结果转录为文本
// 配对与转录规则见 docs/protocol.md“contents 与工具往返编码”
func linkFunctionResults(content Content, pendingCalls *[]FunctionCall) (Content, error) {
	if content.Role == RoleUser && !slices.ContainsFunc(content.Parts, func(part Part) bool { return part.FunctionResult != nil }) {
		*pendingCalls = nil
	}
	parts := make([]Part, 0, len(content.Parts))
	for index, part := range content.Parts {
		if part.FunctionCall != nil {
			*pendingCalls = append(*pendingCalls, *part.FunctionCall)
		}
		result := part.FunctionResult
		if result == nil {
			parts = append(parts, part)
			continue
		}
		matched := slices.IndexFunc(*pendingCalls, func(call FunctionCall) bool { return result.ID != "" && call.ID == result.ID })
		byID := matched >= 0
		if !byID {
			matched = slices.IndexFunc(*pendingCalls, func(call FunctionCall) bool { return result.Name == "" || call.Name == result.Name })
		}
		if matched < 0 {
			transcript, err := marshal(map[string]any{"functionResponse": map[string]any{"name": result.Name, "response": result.Content}})
			if err != nil {
				return Content{}, fmt.Errorf("part %d: function result: %w", index, err)
			}
			parts = append(append(parts, Part{Text: string(transcript)}), result.Parts...)
			continue
		}
		call := (*pendingCalls)[matched]
		name := cmp.Or(result.Name, call.Name)
		if byID {
			name = call.Name
		}
		if name != result.Name || result.ID == "" {
			linked := *result
			linked.Name, linked.ID = name, cmp.Or(result.ID, call.ID)
			part.FunctionResult = &linked
		}
		parts = append(parts, part)
		*pendingCalls = slices.Delete(*pendingCalls, matched, matched+1)
	}
	content.Parts = parts
	return content, nil
}

// interruptedOutput 为 Claude 族中没有回传结果的函数调用补上的函数结果正文
const interruptedOutput = "Tool execution interrupted. No result provided."

// interruptedResults 为没有回传结果的函数调用生成正文为 interruptedOutput 的函数结果
func interruptedResults(calls []FunctionCall) []WirePart {
	parts := make([]WirePart, 0, len(calls))
	for _, call := range calls {
		response := &WireFunctionResponse{ID: call.ID, Name: call.Name}
		response.Response.Output = interruptedOutput
		parts = append(parts, WirePart{FunctionResponse: response})
	}
	return parts
}

// functionOutput 返回函数结果在 response.output 中的文本：JSON 字符串与只有 output 字符串的对象取该文本，其余 JSON 取紧凑编码
func functionOutput(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(content, &object) == nil && len(object) == 1 && json.Unmarshal(object["output"], &text) == nil {
		return text
	}
	return string(canonicalJSON(content))
}

// canonicalJSON 按官方客户端回放函数参数的方式重写 JSON：对象键排序、去空白、不转义 HTML 字符，数字按 float64 写出，按 float64 写出与原文不同的整数保留原文
func canonicalJSON(raw json.RawMessage) json.RawMessage {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || decoder.More() {
		return raw
	}
	data, _ := marshal(exactNumbers(value))
	return data
}

// exactNumbers 把 json.Number 换成 float64，按 float64 写出与原文不同的整数保留为 json.Number
func exactNumbers(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, item := range value {
			value[key] = exactNumbers(item)
		}
	case []any:
		for index, item := range value {
			value[index] = exactNumbers(item)
		}
	case json.Number:
		float, err := value.Float64()
		if err != nil {
			return value
		}
		if _, ok := new(big.Int).SetString(value.String(), 10); ok {
			if encoded, _ := json.Marshal(float); string(encoded) != value.String() {
				return value
			}
		}
		return float
	}
	return value
}

// marshal 按官方客户端的 JSON 编码输出，不转义 HTML 字符，末尾不带换行
func marshal(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// hubJSON 按档案系统的官方 Hub language_server 的 protojson 输出编码请求正文：构建号为奇数时在 marshal 结果的每个结构逗号后加一个空格，否则为紧凑写法
func (d Device) hubJSON(value any) ([]byte, error) {
	data, err := marshal(value)
	if err != nil || !d.spaced() {
		return data, err
	}
	out := make([]byte, 0, len(data)+len(data)/16)
	quoted, escaped := false, false
	for _, b := range data {
		out = append(out, b)
		switch {
		case escaped:
			escaped = false
		case quoted && b == '\\':
			escaped = true
		case b == '"':
			quoted = !quoted
		case !quoted && b == ',':
			out = append(out, ' ')
		}
	}
	return out, nil
}

// streamChunk 表示上游 SSE 的一个 data 事件
type streamChunk struct {
	Response struct {
		Candidates []struct {
			Content           WireContent `json:"content"`
			FinishReason      string      `json:"finishReason"`
			FinishMessage     string      `json:"finishMessage"`
			ContinuationToken string      `json:"continuationToken"`
			GroundingMetadata *Grounding  `json:"groundingMetadata"`
			CitationMetadata  *struct {
				Citations []CitationSource `json:"citations"`
			} `json:"citationMetadata"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount        int64 `json:"promptTokenCount"`
			CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
			ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
			CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
			TotalTokenCount         int64 `json:"totalTokenCount"`
		} `json:"usageMetadata"`
		PromptFeedback *struct {
			BlockReason        string `json:"blockReason"`
			BlockReasonMessage string `json:"blockReasonMessage"`
		} `json:"promptFeedback"`
		ModelVersion string `json:"modelVersion"`
		ResponseID   string `json:"responseId"`
	} `json:"response"`
	TraceID string `json:"traceId"`
	Error   *struct {
		Code int `json:"code"`
	} `json:"error"`
}

// Exchange 表示一次 streamGenerateContent 的地址、请求正文与响应正文，流式响应的正文在事件通道关闭时完整
type Exchange struct {
	URL      string
	Request  []byte
	Response []byte
}

// StreamGenerateContent 发送信封并把 SSE 响应解码为规范事件，同时返回请求与响应正文
// Tab 补全信封以编辑器版 User-Agent 发出；遇 CONTINUATION 或中断续接令牌时自动续接，规则见 docs/protocol.md“长解码续接”
func (c *Client) StreamGenerateContent(ctx context.Context, envelope Envelope) (<-chan Event, *Exchange, error) {
	exchange := &Exchange{URL: c.base + "/v1internal:streamGenerateContent?alt=sse"}
	response, payload, err := c.streamHop(ctx, envelope, exchange)
	exchange.Request = payload
	if err != nil {
		return nil, exchange, err
	}
	events := make(chan Event)
	go func() {
		defer close(events)
		var before Usage
		resumes := 0
		for {
			hop := make(chan Event)
			var body bytes.Buffer
			go func() {
				defer close(hop)
				defer response.Body.Close()
				DecodeStream(io.TeeReader(response.Body, &body), hop)
			}()
			var token string
			var resumed bool
			var last Usage
			var usage *Event
			for event := range hop {
				if event.Kind == EventFinish && event.FinishReason == "CONTINUATION" && event.ContinuationToken != "" {
					token, usage = event.ContinuationToken, nil
					continue
				}
				if event.Kind == EventError && event.ContinuationToken != "" && resumes < midStreamResumes && ctx.Err() == nil {
					token, resumed, usage = event.ContinuationToken, true, nil
					continue
				}
				if event.Usage != nil {
					last = *event.Usage
					event.Usage = addUsage(before, last)
				}
				if event.Kind == EventUsage {
					usage = &event
					continue
				}
				if usage != nil {
					events <- *usage
					usage = nil
				}
				events <- event
			}
			if usage != nil {
				events <- *usage
			}
			exchange.Response = append(exchange.Response, body.Bytes()...)
			if token == "" {
				return
			}
			if resumed {
				resumes++
			} else {
				resumes = 0
			}
			before = *addUsage(before, last)
			envelope.Request.ContinuationToken, envelope.payload = token, nil
			if response, _, err = c.streamHop(ctx, envelope, exchange); err != nil {
				events <- Event{Kind: EventError, Err: err}
				return
			}
		}
	}()
	return events, exchange, nil
}

// midStreamResumes 是同一跳中断后以续接令牌重拨的次数上限
const midStreamResumes = 3

// addUsage 返回此前各跳的合计 a 与这一跳的用量 b 逐项相加的结果，PromptTokens 为第一跳的输入
func addUsage(a, b Usage) *Usage {
	return &Usage{
		InputTokens:     a.InputTokens + b.InputTokens,
		CachedTokens:    a.CachedTokens + b.CachedTokens,
		OutputTokens:    a.OutputTokens + b.OutputTokens,
		ReasoningTokens: a.ReasoningTokens + b.ReasoningTokens,
		TotalTokens:     a.TotalTokens + b.TotalTokens,
		PromptTokens:    cmp.Or(a.PromptTokens, b.InputTokens),
	}
}

// Encode 按发出时的编码写好信封的请求正文并返回其字节数，之后发出这个信封时直接使用这份正文
func (c *Client) Encode(envelope *Envelope) (int, error) {
	payload, err := c.encode(*envelope)
	envelope.payload = payload
	return len(payload), err
}

// encode 按发出时的编码返回信封的请求正文：Tab 补全为紧凑 JSON，其余按 hubJSON
func (c *Client) encode(envelope Envelope) ([]byte, error) {
	if envelope.RequestType == requestTypeTab {
		return marshal(envelope)
	}
	return c.device.hubJSON(envelope)
}

// streamHop 发出一次 streamGenerateContent，返回状态码 2xx 的响应与请求正文；没有拿到响应时在 WARN 级记下失败所处的连接阶段；信封带有 Encode 写好的正文时直接使用；非 2xx 时读出正文追加到交换记录并返回 UpstreamError
func (c *Client) streamHop(ctx context.Context, envelope Envelope, exchange *Exchange) (*http.Response, []byte, error) {
	payload := envelope.payload
	if payload == nil {
		var err error
		if payload, err = c.encode(envelope); err != nil {
			return nil, nil, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, exchange.URL, io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		return nil, payload, err
	}
	agent := c.device.UserAgent()
	if envelope.RequestType == requestTypeTab {
		agent = c.device.IDEUserAgent()
	}
	request.Header.Set("User-Agent", agent)
	request.Header.Set("Content-Type", "application/json")
	c.userProject(request)
	request = c.traced(request)
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() == nil {
			slog.WarnContext(ctx, "上游生成请求失败 | "+request.URL.Host+request.URL.Path+connPhase(request)+" | 错误="+err.Error(), "event", "upstream.request", "source", c.account)
		}
		return nil, payload, err
	}
	if response.StatusCode/100 != 2 {
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		exchange.Response = append(exchange.Response, body...)
		return nil, payload, &UpstreamError{Status: response.StatusCode, Body: body, RetryAfter: response.Header.Get("Retry-After")}
	}
	return response, payload, nil
}

// DecodeStream 逐行读取 SSE 响应并向 events 发送规范事件
// 事件映射、累计用量、内容策略、输入拒绝与异常结束规则见 docs/protocol.md“规范事件”与“过滤提示与内容策略”
func DecodeStream(reader io.Reader, events chan<- Event) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, 64<<20)
	var usage *Usage
	var called, filtered, prompted bool
	// checkpoint 是流中最近一次出现的续接令牌，流中断或没有结束原因时带在错误事件上
	var checkpoint string
	finish := Event{Kind: EventFinish}
	for scanner.Scan() {
		line, ok := bytes.CutPrefix(scanner.Bytes(), []byte("data:"))
		if ok {
			line = bytes.TrimPrefix(line, []byte(" "))
		} else if len(line) == 0 || line[0] != '{' || !json.Valid(line) {
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			events <- Event{Kind: EventError, Err: err, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
			return
		}
		finish.TraceID = cmp.Or(chunk.TraceID, finish.TraceID)
		if chunk.Error != nil {
			events <- Event{Kind: EventError, Err: &UpstreamError{Status: chunk.Error.Code, Body: bytes.Clone(line)}, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
			return
		}
		finish.ResponseID = cmp.Or(chunk.Response.ResponseID, finish.ResponseID)
		if chunk.Response.ModelVersion != "" {
			finish.ProviderModel = chunk.Response.ModelVersion
		}
		if metadata := chunk.Response.UsageMetadata; metadata != nil {
			usage = &Usage{
				InputTokens:     cmp.Or(metadata.PromptTokenCount, metadata.TotalTokenCount-metadata.CandidatesTokenCount-metadata.ThoughtsTokenCount),
				CachedTokens:    metadata.CachedContentTokenCount,
				OutputTokens:    metadata.CandidatesTokenCount,
				ReasoningTokens: metadata.ThoughtsTokenCount,
				TotalTokens:     metadata.TotalTokenCount,
			}
		}
		if feedback := chunk.Response.PromptFeedback; feedback != nil && feedback.BlockReason != "" && feedback.BlockReason != "BLOCK_REASON_UNSPECIFIED" {
			prompted = true
			finish.FinishReason, finish.FinishMessage = feedback.BlockReason, feedback.BlockReasonMessage
		}
		for _, candidate := range chunk.Response.Candidates {
			if candidate.FinishReason == "FINISH_REASON_UNSPECIFIED" {
				candidate.FinishReason = ""
			}
			if parts := candidate.Content.Parts; candidate.Content.Role == "" && len(parts) == 1 && strings.HasPrefix(parts[0].Text, restrictedText) {
				events <- Event{Kind: EventError, Err: &AccountRestrictedError{Message: parts[0].Text}, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
				return
			}
			if parts := candidate.Content.Parts; candidate.Content.Role == "" && len(parts) == 1 && strings.HasPrefix(parts[0].Text, filterBlockedText) {
				filtered = true
				finish.FinishReason, finish.FinishMessage = "OTHER", parts[0].Text
				continue
			}
			for _, part := range candidate.Content.Parts {
				called = called || part.FunctionCall != nil
				emitPart(part, chunk.Response.UsageMetadata != nil, candidate.FinishReason != "" || candidate.FinishMessage != "", usage, chunk.Response.ModelVersion, events)
			}
			if candidate.GroundingMetadata != nil {
				events <- Event{Kind: EventGrounding, ProviderModel: chunk.Response.ModelVersion, Grounding: candidate.GroundingMetadata}
			}
			if candidate.CitationMetadata != nil && len(candidate.CitationMetadata.Citations) > 0 {
				events <- Event{Kind: EventCitation, ProviderModel: chunk.Response.ModelVersion, Citations: candidate.CitationMetadata.Citations}
			}
			checkpoint = cmp.Or(candidate.ContinuationToken, checkpoint)
			switch {
			case candidate.FinishReason != "":
				finish.FinishReason, finish.FinishMessage, finish.ContinuationToken = candidate.FinishReason, candidate.FinishMessage, candidate.ContinuationToken
			case candidate.FinishMessage != "":
				finish.FinishReason, finish.FinishMessage = "IMAGE_OTHER", candidate.FinishMessage
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			events <- Event{Kind: EventError, Err: err, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
			return
		}
		if !called {
			events <- Event{Kind: EventError, Err: err, TraceID: finish.TraceID, ResponseID: finish.ResponseID, ContinuationToken: checkpoint}
			return
		}
		finish.Err = err
	}
	if usage != nil {
		events <- Event{Kind: EventUsage, Usage: usage}
	}
	if finish.FinishReason == "" {
		if !called {
			events <- Event{Kind: EventError, Err: ErrIncompleteStream, TraceID: finish.TraceID, ResponseID: finish.ResponseID, ContinuationToken: checkpoint}
			return
		}
		finish.FinishReason, finish.Err = "STOP", cmp.Or(finish.Err, ErrIncompleteStream)
	}
	if prompted && !called && inputBlock(finish.FinishReason) {
		events <- Event{Kind: EventError, Err: &PromptFeedbackError{Reason: finish.FinishReason, Message: finish.FinishMessage, Usage: usage}, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
		return
	}
	if filtered || prompted || contentPolicyFinish(finish.FinishReason) {
		policy := &ContentPolicyError{Reason: finish.FinishReason, Message: finish.FinishMessage}
		if !called {
			events <- Event{Kind: EventError, Err: policy, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
			return
		}
		finish.FinishReason, finish.FinishMessage, finish.Err = "STOP", "", policy
	}
	events <- finish
}

// emitPart 把上游的一个 part 转为规范事件写入 events
// 思考中的内联媒体只保留签名，字段映射见 docs/protocol.md“规范事件”
func emitPart(part WirePart, counted, final bool, usage *Usage, model string, events chan<- Event) {
	if !counted {
		usage = nil
	}
	switch {
	case part.FunctionCall != nil:
		id := part.FunctionCall.ID
		if id == "" {
			id = fmt.Sprintf("call_%06d", rand.IntN(1000000))
		}
		events <- Event{Kind: EventToolCall, Usage: usage, ProviderModel: model, Final: final, ToolCall: &FunctionCall{ID: id, Name: part.FunctionCall.Name, Arguments: part.FunctionCall.Args, ThoughtSignature: part.ThoughtSignature}}
	case part.InlineData != nil && part.Thought:
		if part.ThoughtSignature != "" {
			events <- Event{Kind: EventThoughtSignature, Usage: usage, ProviderModel: model, Final: final, ThoughtSignature: part.ThoughtSignature}
		}
	case part.InlineData != nil:
		events <- Event{Kind: EventMedia, Usage: usage, ProviderModel: model, Final: final, Media: &Blob{MIME: part.InlineData.MIMEType, Data: part.InlineData.Data}, ThoughtSignature: part.ThoughtSignature}
	case part.Thought && part.Text != "":
		events <- Event{Kind: EventReasoning, Usage: usage, ProviderModel: model, Final: final, Text: part.Text, ThoughtSignature: part.ThoughtSignature}
	case part.Text != "":
		events <- Event{Kind: EventText, Usage: usage, ProviderModel: model, Final: final, Text: part.Text, ThoughtSignature: part.ThoughtSignature}
	case part.ThoughtSignature != "":
		events <- Event{Kind: EventThoughtSignature, Usage: usage, ProviderModel: model, Final: final, ThoughtSignature: part.ThoughtSignature}
	}
}
