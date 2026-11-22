package service

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/tokencount"
)

// finishStopSequence 为本地停止序列命中时的结束原因
const finishStopSequence = "STOP_SEQUENCE"

// finishUnexpectedToolCall 为请求没有声明函数而上游返回函数调用时的结束原因
const finishUnexpectedToolCall = "UNEXPECTED_TOOL_CALL"

// output 表示一次生成在转发给客户端前对上游事件的状态与过滤处理器
// 负责输出上限、停止序列、思考可见性、工具约定、JSON 校验、搜索域名过滤与签名收集
type output struct {
	first       time.Time
	thought     time.Time
	limit       *int64
	local       antigravity.Usage
	prompt      antigravity.WireRequest
	names       map[string]string
	unary       bool
	sent        bool
	hide        bool
	stop        *stopMatcher
	noFunctions bool
	tools       *toolContract
	json        *jsonOutput
	search      *antigravity.SearchOptions
	provider    string
	origin      string
	signed      signatureRecord
	usage       *antigravity.Usage
	calls       int
	unexpected  bool
	stopped     *antigravity.Event
	done        bool
	answer      []antigravity.Part
}

// process 返回一个上游事件处理后应转发的事件，把转发的正文与函数调用记入回答，并记下正文上与正文之后的最后一个签名
func (o *output) process(event antigravity.Event) []antigravity.Event {
	if o == nil {
		return []antigravity.Event{event}
	}
	if o.first.IsZero() {
		o.first = time.Now()
	}
	if event.Kind == antigravity.EventReasoning {
		o.thought = time.Now()
	}
	out := o.handle(event)
	for _, forwarded := range out {
		switch forwarded.Kind {
		case antigravity.EventText:
			o.answer = append(o.answer, antigravity.Part{Text: forwarded.Text})
		case antigravity.EventToolCall:
			o.answer = append(o.answer, antigravity.Part{FunctionCall: forwarded.ToolCall})
		}
		if forwarded.Kind == antigravity.EventText || forwarded.Kind == antigravity.EventThoughtSignature && len(o.answer) > 0 && o.answer[len(o.answer)-1].FunctionCall == nil {
			o.signed.reply = cmp.Or(forwarded.ThoughtSignature, o.signed.reply)
		}
	}
	return out
}

// answerText 返回已转发回答的正文
func (o *output) answerText() string {
	var text strings.Builder
	for _, part := range o.answer {
		text.WriteString(part.Text)
	}
	return text.String()
}

// handle 返回单个上游事件处理后应转发的事件
// 停止序列命中或约定不满足后置位 done 丢弃后续事件；命中停止序列而尚无用量时等到用量或结束再收尾
func (o *output) handle(event antigravity.Event) []antigravity.Event {
	if o.done {
		return nil
	}
	if event.Usage != nil {
		o.usage = event.Usage
	}
	if o.stopped != nil {
		if o.usage == nil && event.Kind != antigravity.EventFinish && event.Kind != antigravity.EventError {
			return nil
		}
		return o.finish(*o.stopped)
	}
	o.signed.origin(event.ThoughtSignature, o.origin)
	var out []antigravity.Event
	if event.Kind != antigravity.EventText && event.Kind != antigravity.EventUsage && event.Kind != antigravity.EventCitation && event.Kind != antigravity.EventGrounding {
		if pending := o.stop.flush(); pending != "" {
			out = append(out, o.text(antigravity.Event{Kind: antigravity.EventText, Text: pending, ProviderModel: event.ProviderModel})...)
		}
	}
	switch event.Kind {
	case antigravity.EventText:
		text, matched := o.stop.write(event.Text)
		if text != "" {
			event.Text = text
			out = append(out, o.text(event)...)
		}
		if matched != "" {
			stop := antigravity.Event{Kind: antigravity.EventFinish, FinishReason: finishStopSequence, StopSequence: matched, ProviderModel: event.ProviderModel}
			if o.usage == nil {
				o.stopped = &stop
				return out
			}
			return append(out, o.finish(stop)...)
		}
		return out
	case antigravity.EventReasoning:
		if !o.hide {
			return append(out, event)
		}
		if event.ThoughtSignature == "" {
			return out
		}
		return append(out, antigravity.Event{Kind: antigravity.EventThoughtSignature, Usage: event.Usage, ProviderModel: event.ProviderModel, Final: event.Final, ThoughtSignature: event.ThoughtSignature})
	case antigravity.EventMedia:
		if !o.unary {
			o.sent = true
		}
	case antigravity.EventToolCall:
		if o.noFunctions {
			o.unexpected = true
			return out
		}
		call := *event.ToolCall
		if o.provider == antigravity.ProviderAnthropic {
			call.Arguments = o.claudeArguments(call)
		}
		if name := o.names[call.Name]; name != "" {
			call.Name = name
		}
		event.ToolCall = &call
		o.calls++
		if call.ThoughtSignature != "" {
			o.signed.calls[call.ID] = call.ThoughtSignature
			o.signed.origin(call.ThoughtSignature, o.origin)
		}
		if err := o.tools.check(call, o.calls, o.sent); err != nil {
			o.done = true
			return append(out, antigravity.Event{Kind: antigravity.EventError, Err: err})
		}
		return append(out, event)
	case antigravity.EventUsage:
		if o.json != nil {
			return out
		}
		return append(out, event)
	case antigravity.EventFinish:
		return append(out, o.finish(event)...)
	case antigravity.EventGrounding:
		if o.search != nil {
			event.Grounding = filterSources(event.Grounding, o.search)
		}
	}
	return append(out, event)
}

// filterSources 只保留站点在 AllowedDomains 中且不在 BlockedDomains 中的搜索来源，并按保留的来源重排正文片段的来源序号
func filterSources(grounding *antigravity.Grounding, search *antigravity.SearchOptions) *antigravity.Grounding {
	filtered := *grounding
	filtered.GroundingChunks = nil
	index := map[int]int{}
	for i, chunk := range grounding.GroundingChunks {
		if chunk.Web != nil && (len(search.AllowedDomains) > 0 && !sourceIn(*chunk.Web, search.AllowedDomains) || sourceIn(*chunk.Web, search.BlockedDomains)) {
			continue
		}
		index[i] = len(filtered.GroundingChunks)
		filtered.GroundingChunks = append(filtered.GroundingChunks, chunk)
	}
	filtered.GroundingSupports = nil
	for _, support := range grounding.GroundingSupports {
		var indices []int
		for _, i := range support.GroundingChunkIndices {
			if kept, ok := index[i]; ok {
				indices = append(indices, kept)
			}
		}
		if len(indices) > 0 {
			support.GroundingChunkIndices = indices
			filtered.GroundingSupports = append(filtered.GroundingSupports, support)
		}
	}
	return &filtered
}

// sourceIn 判断网页来源的标题站点或地址主机属于 domains 中的站点或其子域名
func sourceIn(web antigravity.GroundingWeb, domains []string) bool {
	hosts := []string{strings.ToLower(web.Title)}
	if parsed, err := url.Parse(web.URI); err == nil {
		hosts = append(hosts, strings.ToLower(parsed.Hostname()))
	}
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimPrefix(domain, "www."))
		for _, host := range hosts {
			if host == domain || strings.HasSuffix(host, "."+domain) {
				return true
			}
		}
	}
	return false
}

// prefixTokens 返回会话校准记下的前缀 token 数：第一跳的上游输入用量减去本次接在最后一个用户轮上的输出格式指令，下一轮该指令移到新的最后一个用户轮
func (o *output) prefixTokens(reported antigravity.Usage) int64 {
	tokens := cmp.Or(reported.PromptTokens, reported.InputTokens)
	if o.json != nil && o.json.instruction != "" {
		tokens -= tokencount.Text(o.provider, o.json.instruction)
	}
	return tokens
}

// signatureRecord 表示思考签名：calls 为各函数调用 ID 与各回答指纹的签名，origins 为各签名摘要对应的来源（见 signatureOrigin），reply 为本次回答正文上与正文之后的签名
type signatureRecord struct {
	calls   map[string]string
	origins map[string]string
	reply   string
}

// newSignatureRecord 返回空的签名记录
func newSignatureRecord() signatureRecord {
	return signatureRecord{calls: map[string]string{}, origins: map[string]string{}}
}

// origin 记下签名来自的模型（见 signatureOrigin）
func (r signatureRecord) origin(signature, origin string) {
	if signature != "" {
		r.origins[signatureDigest(signature)] = origin
	}
}

// foreign 判断签名不能回放给 origin 对应的模型：Gemini 族请求中不是 Base64 文本的签名，以及会话中记为其他模型生成的签名；其余模型族接受任何签名
func (r signatureRecord) foreign(signature, origin string) bool {
	if signature == "" || !strings.HasPrefix(origin, antigravity.ProviderGoogle) {
		return false
	}
	recorded, ok := r.origins[signatureDigest(signature)]
	return !base64Signature.MatchString(signature) || ok && recorded != origin
}

// signatureOrigin 返回签名来源的记法：Gemini 族为模型族与去掉思考档位的目录 ID（同一模型的各档位可互相回放签名，不同模型不能），其余为模型族
func signatureOrigin(model antigravity.CatalogModel) string {
	if model.ModelProvider != antigravity.ProviderGoogle {
		return model.ModelProvider
	}
	return model.ModelProvider + "/" + levelSuffix.ReplaceAllString(model.ID, "")
}

// signatureDigest 返回签名的 SHA-256 前 16 字节十六进制
func signatureDigest(signature string) string {
	sum := sha256.Sum256([]byte(signature))
	return hex.EncodeToString(sum[:16])
}

// text 转发正文，要求 JSON 输出时暂存到结束
func (o *output) text(event antigravity.Event) []antigravity.Event {
	if o.json == nil {
		if !o.unary && event.Text != "" {
			o.sent = true
		}
		return []antigravity.Event{event}
	}
	o.json.text.WriteString(event.Text)
	o.json.signature = event.ThoughtSignature
	o.json.model = event.ProviderModel
	return nil
}

// finish 在结束事件前核对必需的函数调用并校验 JSON 输出，暂存的正文与用量随结束事件发出；输出达到上限时不校验；丢弃过未声明的函数调用时正常结束改为 UNEXPECTED_TOOL_CALL
func (o *output) finish(event antigravity.Event) []antigravity.Event {
	o.done = true
	if o.unexpected && event.FinishReason == "STOP" {
		event.FinishReason = finishUnexpectedToolCall
	}
	var out []antigravity.Event
	complete := event.FinishReason != "MAX_TOKENS"
	if complete && o.tools != nil && o.tools.required && o.calls == 0 {
		return []antigravity.Event{{Kind: antigravity.EventError, Err: &Error{Status: http.StatusBadGateway, Message: "upstream returned no call to the required tool"}}}
	}
	if o.json != nil {
		text := o.json.text.String()
		if complete && (text != "" || o.calls == 0) {
			trimmed, err := o.json.validate(text)
			if err != nil {
				return []antigravity.Event{{Kind: antigravity.EventError, Err: &Error{Status: http.StatusBadGateway, Message: "upstream output does not match the requested JSON format: " + err.Error()}}}
			}
			text = trimmed
		}
		if text != "" {
			out = append(out, antigravity.Event{Kind: antigravity.EventText, Text: text, ThoughtSignature: o.json.signature, ProviderModel: o.json.model})
		}
	}
	if o.usage != nil && (o.json != nil || event.FinishReason == finishStopSequence) {
		out = append(out, antigravity.Event{Kind: antigravity.EventUsage, Usage: o.usage})
	}
	return append(out, event)
}

// claudeArguments 返回 Claude 族函数调用的参数，按上游声明把多包一层 JSON 引号的枚举字符串还原为枚举值
func (o *output) claudeArguments(call antigravity.FunctionCall) json.RawMessage {
	for _, tool := range o.prompt.Tools {
		for _, declaration := range tool.FunctionDeclarations {
			if declaration.Name != call.Name || !bytes.Contains(declaration.Parameters, []byte(`"enum"`)) {
				continue
			}
			var schema map[string]any
			var arguments any
			decoder := json.NewDecoder(bytes.NewReader(call.Arguments))
			decoder.UseNumber()
			if json.Unmarshal(declaration.Parameters, &schema) != nil || decoder.Decode(&arguments) != nil || !unquoteEnums(schema, arguments) {
				return call.Arguments
			}
			encoded, _ := json.Marshal(arguments)
			return encoded
		}
	}
	return call.Arguments
}

// unquoteEnums 逐层把值不在枚举中、去掉一层 JSON 引号后在枚举中的字符串替换为枚举值，替换过时返回 true
func unquoteEnums(schema map[string]any, value any) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for key, child := range typed {
			property, _ := properties[key].(map[string]any)
			if text, ok := child.(string); ok {
				if decoded, ok := unquoteEnum(property, text); ok {
					typed[key], changed = decoded, true
				}
				continue
			}
			changed = unquoteEnums(property, child) || changed
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for index, child := range typed {
			if text, ok := child.(string); ok {
				if decoded, ok := unquoteEnum(items, text); ok {
					typed[index], changed = decoded, true
				}
				continue
			}
			changed = unquoteEnums(items, child) || changed
		}
	}
	return changed
}

// unquoteEnum 返回去掉一层 JSON 引号后的枚举值，值本身在枚举中或解码后不在枚举中时返回 false
func unquoteEnum(schema map[string]any, text string) (string, bool) {
	enum, _ := schema["enum"].([]any)
	var decoded any
	if len(enum) == 0 || slices.Contains(enum, any(text)) || json.Unmarshal([]byte(text), &decoded) != nil {
		return "", false
	}
	value, ok := decoded.(string)
	if !ok || !slices.Contains(enum, decoded) {
		return "", false
	}
	return value, true
}

// toolContract 表示需要在输出中核对的工具约定：必须调用、可调用的函数、单次调用、各函数的必填参数与参数 Schema
type toolContract struct {
	required  bool
	single    bool
	names     map[string]bool
	arguments map[string][]string
	schemas   map[string]*schemaValidator
}

// callError 表示上游函数调用不符合请求的工具约定，客户端尚未收到内容时重新生成，公开协议按 502 返回
type callError struct{ message string }

// Error 返回错误说明
func (e *callError) Error() string {
	return e.message
}

// HTTPStatus 返回公开协议使用的状态码
func (e *callError) HTTPStatus() int {
	return http.StatusBadGateway
}

// check 核对第 count 个函数调用；sent 为真时客户端已收到正文或媒体、不能再重新生成，不再核对非 strict 声明的必填参数
func (contract *toolContract) check(call antigravity.FunctionCall, count int, sent bool) error {
	if contract == nil {
		return nil
	}
	fail := func(format string, args ...any) error {
		return &callError{fmt.Sprintf(format, args...)}
	}
	switch {
	case !contract.names[call.Name]:
		return fail("upstream called function %q outside the tool choice", call.Name)
	case contract.single && count > 1:
		return fail("upstream returned more than one function call with parallel tool calls disabled")
	}
	if !sent {
		if required := contract.arguments[call.Name]; len(required) > 0 {
			var arguments map[string]json.RawMessage
			json.Unmarshal(call.Arguments, &arguments)
			for _, key := range required {
				if _, ok := arguments[key]; !ok {
					return fail("upstream call of function %q is missing required argument %q", call.Name, key)
				}
			}
		}
	}
	if schema := contract.schemas[call.Name]; schema != nil {
		if err := schema.validate(call.Arguments); err != nil {
			return fail("upstream arguments for function %q violate its strict schema: %v", call.Name, err)
		}
	}
	return nil
}

// jsonOutput 表示暂存的结构化正文、其签名与模型，输出的 Schema 或枚举值，以及接在最后一个用户轮上的输出格式指令
type jsonOutput struct {
	schema      *schemaValidator
	enum        []string
	text        strings.Builder
	signature   string
	model       string
	instruction string
}

// validate 去掉首尾空白与 Markdown 代码块围栏（含写在一行内的 ```json{…}```）后校验 JSON 与 Schema，返回去掉围栏的正文；要求枚举时正文去掉引号后须为其中一个值
func (j *jsonOutput) validate(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if j.enum != nil {
		var quoted string
		if json.Unmarshal([]byte(trimmed), &quoted) == nil {
			trimmed = quoted
		}
		if !slices.Contains(j.enum, trimmed) {
			return "", fmt.Errorf("output is not one of the enum values")
		}
		return trimmed, nil
	}
	if fenced, ok := strings.CutPrefix(trimmed, "```"); ok {
		if newline := strings.IndexByte(fenced, '\n'); newline >= 0 {
			fenced = fenced[newline+1:]
		} else if len(fenced) >= 4 && strings.EqualFold(fenced[:4], "json") {
			fenced = fenced[4:]
		}
		if body, ok := strings.CutSuffix(strings.TrimSpace(fenced), "```"); ok {
			trimmed = strings.TrimSpace(body)
		}
	}
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("output is not JSON")
	}
	if j.schema != nil {
		if err := j.schema.validate(json.RawMessage(trimmed)); err != nil {
			return "", err
		}
	}
	return trimmed, nil
}

// schemaValidator 表示编译后的 JSON Schema
type schemaValidator struct {
	schema *jsonschema.Schema
}

// compileSchema 编译客户端的 JSON Schema，Gemini 大写类型名与 nullable 按 JSON Schema 语义解释，pattern 按 ECMAScript 正则
func compileSchema(raw json.RawMessage) (*schemaValidator, error) {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(schemaLoader{})
	compiler.UseRegexpEngine(compilePattern)
	const resource = "https://schema.invalid/schema.json"
	if err := compiler.AddResource(resource, validationSchema(document)); err != nil {
		return nil, err
	}
	schema, err := compiler.Compile(resource)
	if err != nil {
		return nil, err
	}
	return &schemaValidator{schema: schema}, nil
}

// validate 校验一个 JSON 值
func (v *schemaValidator) validate(raw json.RawMessage) error {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	return v.schema.Validate(value)
}

// schemaLoader 拒绝加载请求之外的 Schema 引用
type schemaLoader struct{}

// Load 返回外部引用不可用的错误
func (schemaLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("schema reference %q is not part of the request", url)
}

// ecmaPattern 以 ECMAScript 语义匹配 pattern
type ecmaPattern struct {
	*regexp2.Regexp
}

// MatchString 返回字符串是否匹配
func (pattern ecmaPattern) MatchString(value string) bool {
	matched, err := pattern.Regexp.MatchString(value)
	return err == nil && matched
}

// compilePattern 编译 ECMAScript 正则
func compilePattern(value string) (jsonschema.Regexp, error) {
	pattern, err := regexp2.Compile(value, regexp2.ECMAScript)
	if err != nil {
		return nil, err
	}
	pattern.MatchTimeout = time.Second
	return ecmaPattern{pattern}, nil
}

// validationSchema 把 Gemini 大写类型名转为小写，nullable 转为允许 null
func validationSchema(value any) any {
	schema, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if name, ok := schema["type"].(string); ok {
		schema["type"] = strings.ToLower(name)
	}
	if names, ok := schema["type"].([]any); ok {
		for index, name := range names {
			if name, ok := name.(string); ok {
				names[index] = strings.ToLower(name)
			}
		}
	}
	for _, key := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if entries, ok := schema[key].(map[string]any); ok {
			for name, child := range entries {
				entries[name] = validationSchema(child)
			}
		}
	}
	for _, key := range []string{"items", "not", "additionalProperties", "contains", "if", "then", "else", "unevaluatedProperties", "unevaluatedItems"} {
		if child, ok := schema[key]; ok {
			schema[key] = validationSchema(child)
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if entries, ok := schema[key].([]any); ok {
			for index, child := range entries {
				entries[index] = validationSchema(child)
			}
		}
	}
	if schema["nullable"] == true {
		delete(schema, "nullable")
		return map[string]any{"anyOf": []any{schema, map[string]any{"type": "null"}}}
	}
	return schema
}

// stopMatcher 在正文流中跨分块匹配停止序列，可能是序列前缀的末尾文本暂存到下一块
type stopMatcher struct {
	sequences []string
	prefixes  []string
	pending   string
}

// newStopMatcher 返回停止序列的匹配器，没有非空序列时返回 nil
func newStopMatcher(sequences []string) *stopMatcher {
	matcher := &stopMatcher{}
	seen := map[string]bool{}
	for _, sequence := range sequences {
		if sequence == "" || seen[sequence] {
			continue
		}
		seen[sequence] = true
		matcher.sequences = append(matcher.sequences, sequence)
		for index := 1; index < len(sequence); index++ {
			if prefix := sequence[:index]; !seen["prefix:"+prefix] {
				seen["prefix:"+prefix] = true
				matcher.prefixes = append(matcher.prefixes, prefix)
			}
		}
	}
	if len(matcher.sequences) == 0 {
		return nil
	}
	sort.Slice(matcher.prefixes, func(left, right int) bool { return len(matcher.prefixes[left]) > len(matcher.prefixes[right]) })
	return matcher
}

// write 返回可以转发的正文与命中的序列，命中时正文截到序列之前
func (m *stopMatcher) write(text string) (string, string) {
	if m == nil {
		return text, ""
	}
	combined := m.pending + text
	m.pending = ""
	index, matched := -1, ""
	for _, sequence := range m.sequences {
		if at := strings.Index(combined, sequence); at >= 0 && (index < 0 || at < index || at == index && len(sequence) < len(matched)) {
			index, matched = at, sequence
		}
	}
	if index >= 0 {
		return combined[:index], matched
	}
	for _, prefix := range m.prefixes {
		if strings.HasSuffix(combined, prefix) {
			m.pending = prefix
			return strings.TrimSuffix(combined, prefix), ""
		}
	}
	return combined, ""
}

// flush 返回并清空暂存的正文
func (m *stopMatcher) flush() string {
	if m == nil {
		return ""
	}
	pending := m.pending
	m.pending = ""
	return pending
}
