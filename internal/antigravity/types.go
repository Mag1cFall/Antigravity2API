package antigravity

import (
	"context"
	"encoding/json"
)

// Role 表示规范消息角色
type Role string

const (
	// RoleUser 表示用户消息
	RoleUser Role = "user"
	// RoleAssistant 表示模型消息
	RoleAssistant Role = "assistant"
	// RoleTool 表示工具结果消息
	RoleTool Role = "tool"
)

// Blob 表示内联二进制内容
type Blob struct {
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}

// FunctionCall 表示模型发起的函数调用
type FunctionCall struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Arguments        json.RawMessage `json:"arguments"`
	ThoughtSignature string          `json:"thought_signature,omitempty"`
}

// FunctionResult 表示客户端返回的函数结果，Parts 为结果中的媒体
type FunctionResult struct {
	ID      string          `json:"id"`
	Name    string          `json:"name,omitempty"`
	Content json.RawMessage `json:"content"`
	Parts   []Part          `json:"parts,omitempty"`
}

// FileRef 表示 http(s) 远程媒体，ID 为 URL；YouTube 链接作为上游 fileData 发送，其余由公开协议层下载并内联
type FileRef struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	MIME string `json:"mime,omitempty"`
}

// Part 表示规范消息中的一个内容块，VideoMetadata 与 MediaResolution 为 Gemini 媒体 part 的原样字段
type Part struct {
	Text             string          `json:"text,omitempty"`
	InlineData       *Blob           `json:"inline_data,omitempty"`
	File             *FileRef        `json:"file,omitempty"`
	FunctionCall     *FunctionCall   `json:"function_call,omitempty"`
	FunctionResult   *FunctionResult `json:"function_result,omitempty"`
	Thought          bool            `json:"thought,omitempty"`
	ThoughtSignature string          `json:"thought_signature,omitempty"`
	VideoMetadata    json.RawMessage `json:"video_metadata,omitempty"`
	MediaResolution  json.RawMessage `json:"media_resolution,omitempty"`
}

// Content 表示一条规范消息
type Content struct {
	Role  Role   `json:"role"`
	Parts []Part `json:"parts"`
}

// FunctionDeclaration 表示客户端声明的函数：Parameters 为上游 Schema，Schema 为客户端原始 JSON Schema，Response、ResponseJSONSchema 与 Behavior 为 Gemini 声明的原样字段
type FunctionDeclaration struct {
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	Parameters         json.RawMessage `json:"parameters,omitempty"`
	Schema             json.RawMessage `json:"schema,omitempty"`
	Strict             bool            `json:"strict,omitempty"`
	Response           json.RawMessage `json:"response,omitempty"`
	ResponseJSONSchema json.RawMessage `json:"response_json_schema,omitempty"`
	Behavior           string          `json:"behavior,omitempty"`
}

// ToolConfig 表示工具启用策略
type ToolConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowed_function_names,omitempty"`
	ParallelCalls        *bool    `json:"parallel_calls,omitempty"`
}

// Tools 表示一次请求启用的工具，GoogleSearch 为客户端请求的内置搜索工具配置，Search 为公开协议搜索工具的选项
type Tools struct {
	Functions    []FunctionDeclaration `json:"functions,omitempty"`
	GoogleSearch json.RawMessage       `json:"google_search,omitempty"`
	Search       *SearchOptions        `json:"search,omitempty"`
	ToolConfig   ToolConfig            `json:"tool_config,omitempty"`
}

// SearchOptions 表示搜索工具的选项：ContextSize 为 low、medium 或 high，Location 为用户的大致位置描述，AllowedDomains 与 BlockedDomains 为只用与不用的来源站点
type SearchOptions struct {
	ContextSize    string   `json:"context_size,omitempty"`
	Location       string   `json:"location,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	BlockedDomains []string `json:"blocked_domains,omitempty"`
}

// SafetySetting 表示一个安全类别的拦截阈值
type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

// GenerationConfig 表示客户端的生成参数：ResponseMIMEType 为 application/json 时由服务要求并校验 JSON 输出，ResponseSchema 为输出的 JSON Schema
type GenerationConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	TopK             *int            `json:"top_k,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	MaxOutputTokens  *int64          `json:"max_output_tokens,omitempty"`
	StopSequences    []string        `json:"stop_sequences,omitempty"`
	ResponseMIMEType string          `json:"response_mime_type,omitempty"`
	ResponseSchema   json.RawMessage `json:"response_schema,omitempty"`
	ReasoningEffort  string          `json:"reasoning_effort,omitempty"`
	ThinkingBudget   *int64          `json:"thinking_budget,omitempty"`
	Seed             *int64          `json:"seed,omitempty"`
	SafetySettings   []SafetySetting `json:"safety_settings,omitempty"`
	MediaResolution  string          `json:"media_resolution,omitempty"`
	// ImageConfig 为图像模型输出的宽高比与分辨率
	ImageConfig *ImageConfig `json:"image_config,omitempty"`
	// ResponseModalities 为客户端要求的输出模态，取 TEXT 与 IMAGE
	ResponseModalities []string `json:"response_modalities,omitempty"`
	// HideThinking 控制公开响应的思考块可见性
	HideThinking bool `json:"hide_thinking,omitempty"`
}

// ImageConfig 表示图像输出的宽高比与分辨率
type ImageConfig struct {
	AspectRatio string `json:"aspect_ratio,omitempty"`
	ImageSize   string `json:"image_size,omitempty"`
}

// GenerateRequest 表示供应商无关的生成请求，Session 为客户端提供的会话标识
type GenerateRequest struct {
	ID        string           `json:"id"`
	Model     string           `json:"model"`
	Session   string           `json:"session,omitempty"`
	System    string           `json:"system,omitempty"`
	Contents  []Content        `json:"contents"`
	Config    GenerationConfig `json:"config,omitempty"`
	Tools     Tools            `json:"tools,omitempty"`
	AccountID string           `json:"account_id,omitempty"`
	// Suffix 为补全请求中光标之后的文字
	Suffix string `json:"suffix,omitempty"`
	// Unary 标记非流式请求，生成结束后才输出
	Unary bool `json:"-"`
}

// Model 表示实时模型目录中的模型
type Model struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description,omitempty"`
	Methods          []string        `json:"methods"`
	InputTokenLimit  int64           `json:"input_token_limit,omitempty"`
	OutputTokenLimit int64           `json:"output_token_limit,omitempty"`
	Capabilities     map[string]bool `json:"capabilities,omitempty"`
	// Provider 为上游模型提供方，取目录的 modelProvider
	Provider string `json:"provider,omitempty"`
	// QuotaGroup 为额度桶分组，Gemini 模型为 gemini，其余为 3p
	QuotaGroup string `json:"quota_group,omitempty"`
	// ThinkingLevels 为族模型可选的思考档位，按 low、medium、high 排列
	ThinkingLevels []string `json:"thinking_levels,omitempty"`
	// DefaultThinkingLevel 为请求不带思考参数时使用的档位
	DefaultThinkingLevel string `json:"default_thinking_level,omitempty"`
}

// Usage 表示一次生成的 token 用量，CachedTokens 是 InputTokens 中命中上游缓存的部分
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	CachedTokens    int64 `json:"cached_tokens,omitempty"`
	OutputTokens    int64 `json:"output_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens     int64 `json:"total_tokens"`
}

// EventKind 表示规范流事件类型
type EventKind string

const (
	// EventText 表示正文增量
	EventText EventKind = "text"
	// EventReasoning 表示思考增量
	EventReasoning EventKind = "reasoning"
	// EventToolCall 表示函数调用
	EventToolCall EventKind = "tool_call"
	// EventThoughtSignature 表示独立思考签名
	EventThoughtSignature EventKind = "thought_signature"
	// EventMedia 表示模型输出的内联媒体
	EventMedia EventKind = "media"
	// EventUsage 表示权威用量
	EventUsage EventKind = "usage"
	// EventFinish 表示正常完成
	EventFinish EventKind = "finish"
	// EventError 表示上游错误
	EventError EventKind = "error"
	// EventGrounding 表示 Google 搜索的查询、来源与正文片段的来源
	EventGrounding EventKind = "grounding"
)

// Grounding 表示上游 candidate 的 groundingMetadata
type Grounding struct {
	WebSearchQueries  []string           `json:"webSearchQueries,omitempty"`
	SearchEntryPoint  json.RawMessage    `json:"searchEntryPoint,omitempty"`
	GroundingChunks   []GroundingChunk   `json:"groundingChunks,omitempty"`
	GroundingSupports []GroundingSupport `json:"groundingSupports,omitempty"`
}

// GroundingChunk 表示一个搜索来源
type GroundingChunk struct {
	Web *GroundingWeb `json:"web,omitempty"`
}

// GroundingWeb 表示网页来源的地址与标题
type GroundingWeb struct {
	URI   string `json:"uri"`
	Title string `json:"title,omitempty"`
}

// GroundingSupport 表示一段正文与支持它的来源序号
type GroundingSupport struct {
	Segment               GroundingSegment `json:"segment"`
	GroundingChunkIndices []int            `json:"groundingChunkIndices,omitempty"`
}

// GroundingSegment 表示正文片段，下标为 UTF-8 字节偏移
type GroundingSegment struct {
	PartIndex  int    `json:"partIndex,omitempty"`
	StartIndex int    `json:"startIndex,omitempty"`
	EndIndex   int    `json:"endIndex,omitempty"`
	Text       string `json:"text,omitempty"`
}

// Event 表示协议核心输出的规范事件
type Event struct {
	Kind         EventKind     `json:"kind"`
	Text         string        `json:"text,omitempty"`
	ToolCall     *FunctionCall `json:"tool_call,omitempty"`
	Media        *Blob         `json:"media,omitempty"`
	Usage        *Usage        `json:"usage,omitempty"`
	FinishReason string        `json:"finish_reason,omitempty"`
	// FinishMessage 为上游 candidate 的 finishMessage，只在结束事件上
	FinishMessage string `json:"finish_message,omitempty"`
	ProviderModel string `json:"provider_model,omitempty"`
	// TraceID 与 ResponseID 为上游流的 traceId 与 responseId，只在结束与错误事件上
	TraceID          string `json:"trace_id,omitempty"`
	ResponseID       string `json:"response_id,omitempty"`
	ThoughtSignature string `json:"thought_signature,omitempty"`
	// StopSequence 为本地停止序列命中时结束事件上的序列
	StopSequence string `json:"stop_sequence,omitempty"`
	// Grounding 为搜索事件的来源
	Grounding *Grounding `json:"grounding,omitempty"`
	Err       error      `json:"-"`
}

// Service 定义公开协议适配器依赖的最小能力
type Service interface {
	Models(context.Context) ([]Model, error)
	Generate(context.Context, GenerateRequest) (<-chan Event, error)
	CountTokens(context.Context, GenerateRequest) (int64, error)
}
