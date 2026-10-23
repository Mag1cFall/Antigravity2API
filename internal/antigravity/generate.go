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

// promptBlockedText 为上游输入过滤提示的开头，该提示以不带 role 的 candidate 正文代替回答
const promptBlockedText = "This request was blocked by Gemini's filters."

// PromptBlockedError 表示上游输入过滤拒绝当前请求，Message 为上游提示原文
type PromptBlockedError struct {
	Message string
}

func (e *PromptBlockedError) Error() string {
	return e.Message
}

// HTTPStatus 返回输入被拒绝时公开协议使用的状态码
func (e *PromptBlockedError) HTTPStatus() int {
	return http.StatusBadRequest
}

// NewConversation 以会话的 cascade 与 trajectory 标识和规范历史推导步序号：Turn 为历史中的模型轮数，Step 为历史按官方步骤模型展开的步骤数，Execution 为当前用户请求的序号
func NewConversation(cascade, trajectory string, contents []Content) Conversation {
	turn, execution := 0, -1
	for _, content := range contents {
		if content.Role == RoleAssistant {
			turn++
		}
		if userRequest(content) {
			execution++
		}
	}
	conversation := Conversation{CascadeID: cascade, TrajectoryID: trajectory, Turn: turn, Step: len(sessionSteps(contents, nil)), Execution: max(execution, 0)}
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

// WireThinkingConfig 表示思考档位，按目录取 thinkingLevel 或 thinkingBudget
type WireThinkingConfig struct {
	IncludeThoughts bool   `json:"includeThoughts"`
	ThinkingLevel   string `json:"thinkingLevel,omitempty"`
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

var thinkingLevels = map[int]string{1: "LOW", 2: "MEDIUM", 3: "HIGH"}

// BuildEnvelope 按官方客户端的信封格式组装一次 Agent 生成请求，system 为已按档位组装好的系统指令，config 中只有模型族接受且生效的参数进入 generationConfig，GPT-OSS 族没有描述的函数以函数名为描述，Claude 族的参数 Schema 去掉 anyOf；函数结果无法关联到调用时返回错误
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

// wireContents 把规范消息转成上游消息：Claude 族没有 ID 的函数调用生成 ID，函数结果按调用补齐名称与 ID，结果所在轮的角色 Claude 族为 user、其余族为 model，相邻消息的结果并入同一轮，同一消息中的其余 part 在其他族另成 user 轮；Gemini 族缺签名的函数调用连同结果转写为文本
func wireContents(contents []Content, model CatalogModel) ([]WireContent, error) {
	claude := model.ModelProvider == ProviderAnthropic
	google := model.ModelProvider == ProviderGoogle
	transcribed := unsignedCalls(contents, google)
	var wired []WireContent
	var pendingCalls []FunctionCall
	for index, content := range contents {
		if claude {
			content = identifyCalls(content, index)
		}
		content, err := linkFunctionResults(content, &pendingCalls)
		if err != nil {
			return nil, fmt.Errorf("contents[%d]: %w", index, err)
		}
		if content.Role == RoleAssistant {
			wired = appendContent(wired, wireRoleModel, modelParts(content.Parts, claude, google, transcribed))
			continue
		}
		var results, rest, ordered []WirePart
		for _, part := range content.Parts {
			item := wirePart(part, google)
			if result := part.FunctionResult; result != nil {
				if transcribed[result.ID] {
					item = WirePart{Text: transcript("functionResponse", map[string]any{"name": result.Name, "response": map[string]any{"output": functionOutput(result.Content)}})}
					rest = append(rest, item)
				} else {
					results = append(results, item)
				}
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

// modelParts 转换 assistant 消息：思考文本只在 Claude 族且本轮带签名时保留，转写的函数调用写成文本，只有签名的 part 并入相邻正文
func modelParts(parts []Part, claude, google bool, transcribed map[string]bool) []WirePart {
	signed := slices.ContainsFunc(parts, func(part Part) bool {
		return part.ThoughtSignature != "" || part.FunctionCall != nil && part.FunctionCall.ThoughtSignature != ""
	})
	var wired []WirePart
	for _, part := range parts {
		switch {
		case part.Thought && (!claude || !signed || part.Text == ""):
			continue
		case part.FunctionCall != nil && transcribed[part.FunctionCall.ID]:
			wired = append(wired, WirePart{Text: transcript("functionCall", map[string]any{"name": part.FunctionCall.Name, "args": json.RawMessage(canonicalJSON(part.FunctionCall.Arguments))})})
		default:
			wired = append(wired, wirePart(part, google))
		}
	}
	return attachSignatures(wired)
}

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

// unsignedCalls 返回 Gemini 族需转写为文本的函数调用 ID：assistant 消息中首个函数调用没有签名时，该消息的全部调用都转写
func unsignedCalls(contents []Content, google bool) map[string]bool {
	transcribed := map[string]bool{}
	if !google {
		return transcribed
	}
	for _, content := range contents {
		if content.Role != RoleAssistant {
			continue
		}
		first := slices.IndexFunc(content.Parts, func(part Part) bool { return part.FunctionCall != nil })
		if first < 0 || content.Parts[first].ThoughtSignature != "" || content.Parts[first].FunctionCall.ThoughtSignature != "" {
			continue
		}
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				transcribed[part.FunctionCall.ID] = true
			}
		}
	}
	return transcribed
}

// transcript 返回函数调用或结果的 Gemini JSON 文本
func transcript(kind string, value map[string]any) string {
	data, _ := marshal(map[string]any{kind: value})
	return string(data)
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

// linkFunctionResults 记录消息中的函数调用，为缺少名称或 ID 的函数结果先按调用 ID、再按唯一待返回调用补齐；不含函数结果的 user 轮清空待返回调用，无法关联时返回错误
func linkFunctionResults(content Content, pendingCalls *[]FunctionCall) (Content, error) {
	if content.Role == RoleUser && !slices.ContainsFunc(content.Parts, func(part Part) bool { return part.FunctionResult != nil }) {
		*pendingCalls = nil
	}
	cloned := false
	for index, part := range content.Parts {
		if part.FunctionCall != nil {
			*pendingCalls = append(*pendingCalls, *part.FunctionCall)
		}
		result := part.FunctionResult
		if result == nil {
			continue
		}
		matched := slices.IndexFunc(*pendingCalls, func(call FunctionCall) bool { return result.ID != "" && call.ID == result.ID })
		if matched < 0 && len(*pendingCalls) == 1 && (result.Name == "" || result.Name == (*pendingCalls)[0].Name) {
			matched = 0
		}
		if matched < 0 {
			if result.Name == "" {
				return Content{}, fmt.Errorf("part %d: function result has no name and matches no function call", index)
			}
			continue
		}
		if result.Name == "" || result.ID == "" {
			if !cloned {
				content.Parts = slices.Clone(content.Parts)
				cloned = true
			}
			linked := *result
			linked.Name, linked.ID = cmp.Or(result.Name, (*pendingCalls)[matched].Name), cmp.Or(result.ID, (*pendingCalls)[matched].ID)
			content.Parts[index].FunctionResult = &linked
		}
		*pendingCalls = slices.Delete(*pendingCalls, matched, matched+1)
	}
	return content, nil
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

// canonicalJSON 按官方客户端回放函数参数的方式重写 JSON：对象键排序、去空白、不转义 HTML 字符
func canonicalJSON(raw json.RawMessage) json.RawMessage {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	data, err := marshal(value)
	if err != nil {
		return raw
	}
	return data
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
			GroundingMetadata *Grounding  `json:"groundingMetadata"`
		} `json:"candidates"`
		UsageMetadata *struct {
			PromptTokenCount        int64 `json:"promptTokenCount"`
			CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
			ThoughtsTokenCount      int64 `json:"thoughtsTokenCount"`
			CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
			TotalTokenCount         int64 `json:"totalTokenCount"`
		} `json:"usageMetadata"`
		ModelVersion string `json:"modelVersion"`
		ResponseID   string `json:"responseId"`
	} `json:"response"`
	TraceID string `json:"traceId"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

// Exchange 表示一次 streamGenerateContent 的地址、请求正文与响应正文，流式响应的正文在事件通道关闭时完整
type Exchange struct {
	URL      string
	Request  []byte
	Response []byte
}

// StreamGenerateContent 发送信封并把 SSE 响应解码为规范事件，同时返回本次请求与响应的正文；Tab 补全信封以编辑器版的 User-Agent 发出
func (c *Client) StreamGenerateContent(ctx context.Context, envelope Envelope) (<-chan Event, *Exchange, error) {
	encode := c.device.hubJSON
	if envelope.RequestType == requestTypeTab {
		encode = marshal
	}
	payload, err := encode(envelope)
	if err != nil {
		return nil, nil, err
	}
	exchange := &Exchange{URL: c.base + "/v1internal:streamGenerateContent?alt=sse", Request: payload}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, exchange.URL, io.NopCloser(bytes.NewReader(payload)))
	if err != nil {
		return nil, exchange, err
	}
	agent := c.device.UserAgent()
	if envelope.RequestType == requestTypeTab {
		agent = c.device.IDEUserAgent()
	}
	request.Header.Set("User-Agent", agent)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(c.traced(request))
	if err != nil {
		return nil, exchange, err
	}
	if response.StatusCode/100 != 2 {
		defer response.Body.Close()
		exchange.Response, _ = io.ReadAll(response.Body)
		return nil, exchange, &UpstreamError{Status: response.StatusCode, Body: exchange.Response}
	}
	events := make(chan Event)
	go func() {
		defer close(events)
		defer response.Body.Close()
		var body bytes.Buffer
		DecodeStream(io.TeeReader(response.Body, &body), events)
		exchange.Response = body.Bytes()
	}()
	return events, exchange, nil
}

// DecodeStream 逐行读取 SSE，把 data 事件转成规范事件，candidate 的 groundingMetadata 转成搜索事件，正文、思考、函数调用与签名事件带所在事件的累计用量与 modelVersion；结束时发出 usage 与带 finishReason、finishMessage 的 finish，只有 finishMessage 的 candidate 以 IMAGE_OTHER 结束，两者都没有时以 ErrIncompleteStream 结束；不带 role 的输入过滤提示以 PromptBlockedError 结束
func DecodeStream(reader io.Reader, events chan<- Event) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	var usage *Usage
	finish := Event{Kind: EventFinish}
	for scanner.Scan() {
		line, ok := strings.CutPrefix(scanner.Text(), "data: ")
		if !ok {
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			events <- Event{Kind: EventError, Err: err, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
			return
		}
		finish.TraceID = cmp.Or(chunk.TraceID, finish.TraceID)
		if chunk.Error != nil {
			events <- Event{Kind: EventError, Err: &UpstreamError{Status: chunk.Error.Code, Body: []byte(line)}, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
			return
		}
		finish.ResponseID = cmp.Or(chunk.Response.ResponseID, finish.ResponseID)
		if chunk.Response.ModelVersion != "" {
			finish.ProviderModel = chunk.Response.ModelVersion
		}
		if metadata := chunk.Response.UsageMetadata; metadata != nil {
			usage = &Usage{
				InputTokens:     metadata.PromptTokenCount,
				CachedTokens:    metadata.CachedContentTokenCount,
				OutputTokens:    metadata.CandidatesTokenCount,
				ReasoningTokens: metadata.ThoughtsTokenCount,
				TotalTokens:     metadata.TotalTokenCount,
			}
		}
		for _, candidate := range chunk.Response.Candidates {
			if parts := candidate.Content.Parts; candidate.Content.Role == "" && len(parts) == 1 && strings.HasPrefix(parts[0].Text, promptBlockedText) {
				events <- Event{Kind: EventError, Err: &PromptBlockedError{Message: parts[0].Text}, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
				return
			}
			for _, part := range candidate.Content.Parts {
				emitPart(part, chunk.Response.UsageMetadata != nil, usage, chunk.Response.ModelVersion, events)
			}
			if candidate.GroundingMetadata != nil {
				events <- Event{Kind: EventGrounding, ProviderModel: chunk.Response.ModelVersion, Grounding: candidate.GroundingMetadata}
			}
			switch {
			case candidate.FinishReason != "":
				finish.FinishReason, finish.FinishMessage = candidate.FinishReason, candidate.FinishMessage
			case candidate.FinishMessage != "":
				finish.FinishReason, finish.FinishMessage = "IMAGE_OTHER", candidate.FinishMessage
			}
		}
	}
	if err := scanner.Err(); err != nil {
		events <- Event{Kind: EventError, Err: err, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
		return
	}
	if usage != nil {
		events <- Event{Kind: EventUsage, Usage: usage}
	}
	if finish.FinishReason == "" {
		events <- Event{Kind: EventError, Err: ErrIncompleteStream, TraceID: finish.TraceID, ResponseID: finish.ResponseID}
		return
	}
	events <- finish
}

// emitPart 把上游的一个 part 转成文本、思考、函数调用、内联媒体或独立签名事件，所在事件带 usageMetadata 时附上累计用量，model 为所在事件的 modelVersion
func emitPart(part WirePart, counted bool, usage *Usage, model string, events chan<- Event) {
	if !counted {
		usage = nil
	}
	switch {
	case part.FunctionCall != nil:
		id := part.FunctionCall.ID
		if id == "" {
			id = fmt.Sprintf("call_%06d", rand.IntN(1000000))
		}
		events <- Event{Kind: EventToolCall, Usage: usage, ProviderModel: model, ToolCall: &FunctionCall{ID: id, Name: part.FunctionCall.Name, Arguments: part.FunctionCall.Args, ThoughtSignature: part.ThoughtSignature}}
	case part.InlineData != nil:
		events <- Event{Kind: EventMedia, Usage: usage, ProviderModel: model, Media: &Blob{MIME: part.InlineData.MIMEType, Data: part.InlineData.Data}, ThoughtSignature: part.ThoughtSignature}
	case part.Thought && part.Text != "":
		events <- Event{Kind: EventReasoning, Usage: usage, ProviderModel: model, Text: part.Text, ThoughtSignature: part.ThoughtSignature}
	case part.Text != "":
		events <- Event{Kind: EventText, Usage: usage, ProviderModel: model, Text: part.Text, ThoughtSignature: part.ThoughtSignature}
	case part.ThoughtSignature != "":
		events <- Event{Kind: EventThoughtSignature, Usage: usage, ProviderModel: model, ThoughtSignature: part.ThoughtSignature}
	}
}
