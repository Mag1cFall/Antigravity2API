package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// finishStopSequence 为本地停止序列命中时的结束原因
const finishStopSequence = "STOP_SEQUENCE"

// output 表示一次生成在转发给客户端前对上游事件的处理：输出上限与本地计数所用的上游请求、函数名还原、思考可见性、停止序列、工具约定与 JSON 输出，并按模型族记下思考签名
type output struct {
	limit    *int64
	prompt   antigravity.WireRequest
	names    map[string]string
	hide     bool
	stop     *stopMatcher
	tools    *toolContract
	json     *jsonOutput
	provider string
	signed   signatureRecord
	usage    *antigravity.Usage
	calls    int
	stopped  *antigravity.Event
	done     bool
	answer   []antigravity.Part
}

// process 返回一个上游事件处理后应转发的事件，并把转发的正文与函数调用记入回答
func (o *output) process(event antigravity.Event) []antigravity.Event {
	if o == nil {
		return []antigravity.Event{event}
	}
	out := o.handle(event)
	for _, forwarded := range out {
		switch forwarded.Kind {
		case antigravity.EventText:
			o.answer = append(o.answer, antigravity.Part{Text: forwarded.Text})
		case antigravity.EventToolCall:
			o.answer = append(o.answer, antigravity.Part{FunctionCall: forwarded.ToolCall})
		}
	}
	return out
}

// reply 返回已转发回答的指纹
func (o *output) reply() string {
	if o == nil {
		return ""
	}
	return answerKey(o.answer)
}

// handle 返回一个上游事件处理后应转发的事件；停止序列命中或约定不满足时 done 置位，其后的上游事件丢弃；停止序列命中时上游尚未给出用量则丢弃其后的事件直到上游给出用量或结束，再随用量发出结束事件
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
	o.signed.origin(event.ThoughtSignature, o.provider)
	var out []antigravity.Event
	if event.Kind != antigravity.EventText && event.Kind != antigravity.EventUsage {
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
		return append(out, antigravity.Event{Kind: antigravity.EventThoughtSignature, Usage: event.Usage, ProviderModel: event.ProviderModel, ThoughtSignature: event.ThoughtSignature})
	case antigravity.EventToolCall:
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
			o.signed.origin(call.ThoughtSignature, o.provider)
		}
		if err := o.tools.check(call, o.calls); err != nil {
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
	}
	return append(out, event)
}

// signatures 返回本次输出中的思考签名
func (o *output) signatures() signatureRecord {
	if o == nil {
		return signatureRecord{}
	}
	return o.signed
}

// signatureRecord 表示思考签名：calls 为各函数调用 ID 的签名，origins 为各签名摘要对应的模型族
type signatureRecord struct {
	calls   map[string]string
	origins map[string]string
}

// newSignatureRecord 返回空的签名记录
func newSignatureRecord() signatureRecord {
	return signatureRecord{calls: map[string]string{}, origins: map[string]string{}}
}

// origin 记下签名来自的模型族
func (r signatureRecord) origin(signature, provider string) {
	if signature != "" {
		r.origins[signatureDigest(signature)] = provider
	}
}

// foreign 判断签名是否由 provider 之外的模型族生成，Gemini 族以外的请求接受任何签名
func (r signatureRecord) foreign(signature, provider string) bool {
	if signature == "" || provider != antigravity.ProviderGoogle {
		return false
	}
	origin, ok := r.origins[signatureDigest(signature)]
	return ok && origin != provider
}

// signatureDigest 返回签名的 SHA-256 前 16 字节十六进制
func signatureDigest(signature string) string {
	sum := sha256.Sum256([]byte(signature))
	return hex.EncodeToString(sum[:16])
}

// text 转发正文，要求 JSON 输出时暂存到结束
func (o *output) text(event antigravity.Event) []antigravity.Event {
	if o.json == nil {
		return []antigravity.Event{event}
	}
	o.json.text.WriteString(event.Text)
	o.json.signature = event.ThoughtSignature
	o.json.model = event.ProviderModel
	return nil
}

// finish 在结束事件前核对必需的函数调用并校验 JSON 输出，暂存的正文与用量随结束事件发出；输出达到上限时不校验
func (o *output) finish(event antigravity.Event) []antigravity.Event {
	o.done = true
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
			if encoded, err := json.Marshal(arguments); err == nil {
				return encoded
			}
			return call.Arguments
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

// toolContract 表示需要在输出中核对的工具约定：必须调用、可调用的函数、单次调用与参数 Schema
type toolContract struct {
	required bool
	single   bool
	names    map[string]bool
	schemas  map[string]*schemaValidator
}

// check 核对第 count 个函数调用
func (contract *toolContract) check(call antigravity.FunctionCall, count int) error {
	if contract == nil {
		return nil
	}
	switch {
	case !contract.names[call.Name]:
		return &Error{Status: http.StatusBadGateway, Message: fmt.Sprintf("upstream called function %q outside the tool choice", call.Name)}
	case contract.single && count > 1:
		return &Error{Status: http.StatusBadGateway, Message: "upstream returned more than one function call with parallel tool calls disabled"}
	}
	if schema := contract.schemas[call.Name]; schema != nil {
		if err := schema.validate(call.Arguments); err != nil {
			return &Error{Status: http.StatusBadGateway, Message: fmt.Sprintf("upstream arguments for function %q violate its strict schema: %v", call.Name, err)}
		}
	}
	return nil
}

// jsonOutput 表示暂存的 JSON 正文、其签名与模型，以及输出的 Schema
type jsonOutput struct {
	schema    *schemaValidator
	text      strings.Builder
	signature string
	model     string
}

// validate 去掉首尾空白与 Markdown 代码块围栏后校验 JSON 与 Schema，返回去掉围栏的正文
func (j *jsonOutput) validate(text string) (string, error) {
	trimmed := strings.TrimSpace(text)
	if fenced, ok := strings.CutPrefix(trimmed, "```"); ok {
		if newline := strings.IndexByte(fenced, '\n'); newline >= 0 {
			fenced = fenced[newline+1:]
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
