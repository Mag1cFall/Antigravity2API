package service

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/image/draw"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

const (
	// assistantPrefixInstruction 为末尾 assistant 轮改写为续写前缀时追加的系统指令
	assistantPrefixInstruction = "Continue the assistant response after the following prefix. Return only the continuation, without repeating the prefix."
	// jsonInstruction 为要求 JSON 输出时追加的输出格式指令
	jsonInstruction = "Respond with a single JSON value only, without Markdown code fences or any other text."
	// schemaInstruction 为要求按 JSON Schema 输出时追加的输出格式指令前缀
	schemaInstruction = "Respond with a single JSON value that conforms to this JSON Schema, without Markdown code fences or any other text:\n"
	// enumInstruction 为要求输出枚举值时追加的输出格式指令前缀
	enumInstruction = "Respond with exactly one of the following values, without quotes, Markdown code fences or any other text:\n"
)

// billingPrefix 为 Claude Code 计费行的开头
const billingPrefix = "x-anthropic-billing-header:"

// claudeFunctionName 为 Claude 族接受的函数名
var claudeFunctionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// base64Signature 匹配 Base64 与 URL 安全 Base64 文本，上游的思考签名都是这种形式
var base64Signature = regexp.MustCompile(`^[A-Za-z0-9+/_-]+={0,2}$`)

// levelSuffix 匹配目录 ID 末尾的思考档位
var levelSuffix = regexp.MustCompile(`-(?:minimal|low|medium|high|xhigh|max)$`)

// claudeCallID 为 Claude 族接受的函数调用 ID
var claudeCallID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// functionName 为其他模型族接受的函数名：字母或下划线开头，只含字母、数字、下划线、点、冒号与连字符
var functionName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.:-]{0,127}$`)

// prepare 把客户端请求改写为上游接受的形状并返回对应的输出处理器
// 涵盖图片检查与缩放、签名过滤补回、末尾 assistant 轮改写、函数与调用 ID 改名及工具与结构化输出约定
func prepare(request antigravity.GenerateRequest, model antigravity.CatalogModel, signed signatureRecord) (antigravity.GenerateRequest, *output, error) {
	if !model.SupportsImages && hasImage(request.Contents) {
		return request, nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("model %s does not support image input", model.ID)}
	}
	request.Contents = restoreSignatures(cloneContents(request.Contents), signed, signatureOrigin(model))
	var err error
	if model.ModelProvider == antigravity.ProviderAnthropic {
		fitClaudeImages(request.Contents)
	}
	if request, err = normalizeTurns(request, model); err != nil {
		return request, nil, err
	}
	out := &output{hide: request.Config.HideThinking, limit: request.Config.MaxOutputTokens, stop: newStopMatcher(request.Config.StopSequences), provider: model.ModelProvider, origin: signatureOrigin(model), signed: newSignatureRecord()}
	request, out.names = renameFunctions(request, model.ModelProvider == antigravity.ProviderAnthropic)
	if model.ModelProvider == antigravity.ProviderAnthropic {
		renameCalls(request.Contents)
	}
	if request, out.tools, err = prepareTools(request, out.names); err != nil {
		return request, nil, err
	}
	out.noFunctions = len(request.Tools.Functions) == 0
	if request, out.json, err = prepareJSON(request); err != nil {
		return request, nil, err
	}
	request, out.search = prepareSearch(request)
	return request, out, nil
}

// claudeImageSide 是 Claude 族图片单边的像素上限，上限见 docs/protocol.md 的兼容改写表
const claudeImageSide = 8000

// fitClaudeImages 把消息与工具结果中单边超过 claudeImageSide 的图片按原宽高比缩小到该上限并编码为 JPEG；不能解码的图片原样保留
func fitClaudeImages(contents []antigravity.Content) {
	var fit func(parts []antigravity.Part) []antigravity.Part
	fit = func(parts []antigravity.Part) []antigravity.Part {
		for index, part := range parts {
			if part.FunctionResult != nil && len(part.FunctionResult.Parts) > 0 {
				result := *part.FunctionResult
				result.Parts = fit(slices.Clone(result.Parts))
				parts[index].FunctionResult = &result
			}
			if part.InlineData != nil && strings.HasPrefix(part.InlineData.MIME, "image/") {
				parts[index].InlineData = fitImage(part.InlineData)
			}
		}
		return parts
	}
	for index := range contents {
		contents[index].Parts = fit(contents[index].Parts)
	}
}

// fitImage 返回单边不超过 claudeImageSide 的图片：已满足时为原图，否则在白底上按原宽高比缩小后以 JPEG 编码
func fitImage(blob *antigravity.Blob) *antigravity.Blob {
	config, _, err := image.DecodeConfig(bytes.NewReader(blob.Data))
	if err != nil || max(config.Width, config.Height) <= claudeImageSide {
		return blob
	}
	decoded, _, err := image.Decode(bytes.NewReader(blob.Data))
	if err != nil {
		return blob
	}
	scale := float64(claudeImageSide) / float64(max(config.Width, config.Height))
	scaled := image.NewRGBA(image.Rectangle{Max: image.Pt(max(1, int(float64(config.Width)*scale)), max(1, int(float64(config.Height)*scale)))})
	draw.Draw(scaled, scaled.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), decoded, decoded.Bounds(), draw.Over, nil)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, scaled, &jpeg.Options{Quality: 85}); err != nil {
		return blob
	}
	return &antigravity.Blob{MIME: "image/jpeg", Data: encoded.Bytes()}
}

// hasImage 判断消息或工具结果中是否有图片
func hasImage(contents []antigravity.Content) bool {
	var image func(parts []antigravity.Part) bool
	image = func(parts []antigravity.Part) bool {
		for _, part := range parts {
			switch {
			case part.InlineData != nil && strings.HasPrefix(part.InlineData.MIME, "image/"),
				part.File != nil && strings.HasPrefix(part.File.MIME, "image/"),
				part.FunctionResult != nil && image(part.FunctionResult.Parts):
				return true
			}
		}
		return false
	}
	for _, content := range contents {
		if image(content.Parts) {
			return true
		}
	}
	return false
}

// cloneContents 复制消息、part 与函数调用，改写不影响调用方的请求
func cloneContents(contents []antigravity.Content) []antigravity.Content {
	cloned := slices.Clone(contents)
	for index := range cloned {
		cloned[index].Parts = slices.Clone(contents[index].Parts)
		for partIndex, part := range cloned[index].Parts {
			if part.FunctionCall != nil {
				call := *part.FunctionCall
				cloned[index].Parts[partIndex].FunctionCall = &call
			}
			if part.FunctionResult != nil {
				result := *part.FunctionResult
				cloned[index].Parts[partIndex].FunctionResult = &result
			}
		}
	}
	return cloned
}

// restoreSignatures 去掉会话中按 signatureOrigin 判定为其他来源的签名与占位签名，去掉后为空的 part 一并删除，再为客户端未回传签名的函数调用（按调用 ID）与没有签名的 assistant 轮补回会话保存的签名
func restoreSignatures(contents []antigravity.Content, signed signatureRecord, origin string) []antigravity.Content {
	for index := range contents {
		parts := contents[index].Parts[:0]
		for _, part := range contents[index].Parts {
			if signed.foreign(part.ThoughtSignature, origin) || sentinelSignatures[part.ThoughtSignature] {
				part.ThoughtSignature = ""
				if part.Text == "" && part.InlineData == nil && part.File == nil && part.FunctionCall == nil && part.FunctionResult == nil {
					continue
				}
			}
			if call := part.FunctionCall; call != nil {
				if signed.foreign(call.ThoughtSignature, origin) || sentinelSignatures[call.ThoughtSignature] {
					call.ThoughtSignature = ""
				}
				if restored := callSignature(signed.calls, call.ID); call.ThoughtSignature == "" && part.ThoughtSignature == "" && !signed.foreign(restored, origin) {
					call.ThoughtSignature = restored
				}
			}
			parts = append(parts, part)
		}
		contents[index].Parts = parts
		if contents[index].Role == antigravity.RoleAssistant && !signedContent(contents[index]) {
			if restored := signed.calls[replySignatureKey(answerKey(parts), index)]; restored != "" && !signed.foreign(restored, origin) {
				contents[index].Parts = signReply(parts, restored)
			}
		}
	}
	return contents
}

// sentinelSignatures 为 Gemini API 文档给出的跳过签名校验的占位签名及其 Base64 形态，上游现场的签名不含这些值
var sentinelSignatures = map[string]bool{"skip_thought_signature_validator": true, "context_engineering_is_the_way_to_go": true, "c2tpcF90aG91Z2h0X3NpZ25hdHVyZV92YWxpZGF0b3I=": true, "Y29udGV4dF9lbmdpbmVlcmluZ19pc190aGVfd2F5X3RvX2dv": true}

// callSignature 返回函数调用 ID 的会话签名，没有同 ID 的记录时取只含字母与数字部分相同的 ID 的签名
func callSignature(calls map[string]string, id string) string {
	if signature, ok := calls[id]; ok || id == "" {
		return signature
	}
	key := alphanumeric(id)
	for saved, signature := range calls {
		if alphanumeric(saved) == key {
			return signature
		}
	}
	return ""
}

// alphanumeric 返回字符串中的字母与数字
func alphanumeric(text string) string {
	return strings.Map(func(char rune) rune {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			return char
		}
		return -1
	}, text)
}

// signedContent 判断消息中是否有带签名的 part 或函数调用
func signedContent(content antigravity.Content) bool {
	return slices.ContainsFunc(content.Parts, func(part antigravity.Part) bool {
		return part.ThoughtSignature != "" || part.FunctionCall != nil && part.FunctionCall.ThoughtSignature != ""
	})
}

// signReply 把回答正文的签名放到最后一个正文 part 上
func signReply(parts []antigravity.Part, signature string) []antigravity.Part {
	for index := len(parts) - 1; index >= 0; index-- {
		if parts[index].Text != "" && !parts[index].Thought {
			parts[index].ThoughtSignature = signature
			break
		}
	}
	return parts
}

// normalizeTurns 把末尾连续 assistant 轮改写为系统指令中的续写前缀，GPT-OSS 接受以 model 轮结尾时原样发送；没有对话内容时以客户端系统提示作为 user 轮
func normalizeTurns(request antigravity.GenerateRequest, model antigravity.CatalogModel) (antigravity.GenerateRequest, error) {
	if model.ModelProvider != antigravity.ProviderOpenAI {
		tail := len(request.Contents)
		for tail > 0 && request.Contents[tail-1].Role == antigravity.RoleAssistant {
			tail--
		}
		if prefix := assistantPrefix(request.Contents[tail:]); prefix != "" {
			request.System = joinInstruction(request.System, assistantPrefixInstruction+"\n<assistant_prefix>"+prefix+"</assistant_prefix>")
		}
		request.Contents = request.Contents[:tail]
	}
	if len(request.Contents) == 0 {
		if strings.TrimSpace(request.System) == "" {
			return request, &Error{Status: http.StatusBadRequest, Message: "request has no messages"}
		}
		request.Contents = []antigravity.Content{{Role: antigravity.RoleUser, Parts: []antigravity.Part{{Text: request.System}}}}
		request.System = ""
	}
	return request, nil
}

// assistantPrefix 按顺序拼接 assistant 轮的正文，思考与函数调用不进入前缀
func assistantPrefix(contents []antigravity.Content) string {
	var prefix strings.Builder
	for _, content := range contents {
		for _, part := range content.Parts {
			if !part.Thought {
				prefix.WriteString(part.Text)
			}
		}
	}
	if strings.TrimSpace(prefix.String()) == "" {
		return ""
	}
	return prefix.String()
}

// dropBilling 去掉客户端系统提示中的计费行
func dropBilling(system string) string {
	return strings.Join(slices.DeleteFunc(strings.Split(system, "\n"), func(line string) bool { return strings.HasPrefix(line, billingPrefix) }), "\n")
}

// joinInstruction 把指令以换行接在系统提示之后
func joinInstruction(system, instruction string) string {
	if system == "" {
		return instruction
	}
	return system + "\n" + instruction
}

// prepareTools 按工具选择筛选已改名的函数声明并写入系统指令，返回按原名核对的输出约定
// 无声明函数或 mode 为 none 时返回 nil
func prepareTools(request antigravity.GenerateRequest, original map[string]string) (antigravity.GenerateRequest, *toolContract, error) {
	config := request.Tools.ToolConfig
	if config.Mode == "none" {
		request.Tools.Functions, request.Tools.GoogleSearch = nil, nil
		return request, nil, nil
	}
	contract := &toolContract{required: config.Mode == "required", names: map[string]bool{}, arguments: map[string][]string{}, schemas: map[string]*schemaValidator{}}
	functions := make([]antigravity.FunctionDeclaration, 0, len(request.Tools.Functions))
	for _, declaration := range request.Tools.Functions {
		name := cmp.Or(original[declaration.Name], declaration.Name)
		if len(config.AllowedFunctionNames) > 0 && !slices.Contains(config.AllowedFunctionNames, name) {
			continue
		}
		contract.names[name] = true
		functions = append(functions, declaration)
		schema := declaration.Schema
		if len(schema) == 0 {
			schema = declaration.Parameters
		}
		var object struct {
			Required []string `json:"required"`
		}
		if json.Unmarshal(schema, &object) == nil && len(object.Required) > 0 {
			contract.arguments[name] = object.Required
		}
		if !declaration.Strict && config.Mode != "validated" {
			continue
		}
		validator, err := compileSchema(schema)
		if err != nil {
			return request, nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("function %q parameters: %v", name, err)}
		}
		contract.schemas[name] = validator
	}
	for _, name := range config.AllowedFunctionNames {
		if !contract.names[name] {
			return request, nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("tool_choice names undeclared function %q", name)}
		}
	}
	request.Tools.Functions = functions
	if contract.required {
		if len(functions) == 0 {
			return request, nil, &Error{Status: http.StatusBadRequest, Message: "tool_choice requires a tool but the request declares no function"}
		}
		names := make([]string, 0, len(functions))
		for _, declaration := range functions {
			names = append(names, declaration.Name)
		}
		request.System = joinInstruction(request.System, "Use one of these tools in this response: "+strings.Join(names, ", ")+". Use the tool before answering.")
	}
	if config.ParallelCalls != nil && !*config.ParallelCalls && len(functions) > 0 {
		contract.single = true
		request.System = joinInstruction(request.System, "Call at most one function in this response.")
	}
	if len(functions) == 0 {
		return request, nil, nil
	}
	return request, contract, nil
}

// prepareSearch 在只启用 Google 搜索的请求中把搜索上下文大小、用户位置与来源站点写入系统指令，返回需要筛选搜索来源的站点选项
func prepareSearch(request antigravity.GenerateRequest) (antigravity.GenerateRequest, *antigravity.SearchOptions) {
	search := request.Tools.Search
	if search == nil || len(request.Tools.GoogleSearch) == 0 || len(request.Tools.Functions) > 0 {
		return request, nil
	}
	if search.ContextSize != "" {
		request.System = joinInstruction(request.System, "Use "+search.ContextSize+" search context depth.")
	}
	if search.Location != "" {
		request.System = joinInstruction(request.System, "The user's approximate location is "+search.Location+". Use it for location-dependent questions and searches.")
	}
	if len(search.AllowedDomains) > 0 {
		request.System = joinInstruction(request.System, "Restrict web searches to these domains using site: queries: "+strings.Join(search.AllowedDomains, ", "))
	}
	if len(search.BlockedDomains) > 0 {
		request.System = joinInstruction(request.System, "Exclude these domains from web searches using -site: queries: "+strings.Join(search.BlockedDomains, ", "))
	}
	if len(search.AllowedDomains) == 0 && len(search.BlockedDomains) == 0 {
		return request, nil
	}
	return request, search
}

// prepareJSON 把 JSON 或枚举输出要求写入最后一个用户轮，返回输出时的 JSON、Schema 或枚举校验
func prepareJSON(request antigravity.GenerateRequest) (antigravity.GenerateRequest, *jsonOutput, error) {
	config := request.Config
	if strings.EqualFold(config.ResponseMIMEType, "text/x.enum") {
		var schema struct {
			Enum []string `json:"enum"`
		}
		if json.Unmarshal(config.ResponseSchema, &schema) != nil || len(schema.Enum) == 0 {
			return request, nil, &Error{Status: http.StatusBadRequest, Message: "responseMimeType text/x.enum requires a responseSchema with a string enum"}
		}
		return requireOutput(request, &jsonOutput{enum: schema.Enum}, enumInstruction+strings.Join(schema.Enum, "\n"))
	}
	if len(config.ResponseSchema) == 0 && !strings.EqualFold(config.ResponseMIMEType, "application/json") {
		return request, nil, nil
	}
	if len(config.ResponseSchema) == 0 {
		return requireOutput(request, &jsonOutput{}, jsonInstruction)
	}
	validator, err := compileSchema(config.ResponseSchema)
	if err != nil {
		return request, nil, &Error{Status: http.StatusBadRequest, Message: "response schema: " + err.Error()}
	}
	return requireOutput(request, &jsonOutput{schema: validator}, schemaInstruction+string(config.ResponseSchema))
}

// requireOutput 把输出格式指令作为一个文本 part 接在最后一个用户轮末尾并记入 out.instruction，没有用户轮时接在系统提示之后
func requireOutput(request antigravity.GenerateRequest, out *jsonOutput, instruction string) (antigravity.GenerateRequest, *jsonOutput, error) {
	for index := len(request.Contents) - 1; index >= 0; index-- {
		if request.Contents[index].Role == antigravity.RoleUser {
			request.Contents[index].Parts = append(request.Contents[index].Parts, antigravity.Part{Text: instruction})
			out.instruction = instruction
			return request, out, nil
		}
	}
	request.System = joinInstruction(request.System, instruction)
	return request, out, nil
}

// renameCalls 把 Claude 族不接受或重复的函数调用 ID 改写为合规 ID，并同步更新配对的函数结果
// 不允许的字符替换为下划线，超过 64 字符或重复时截断并附原 ID 与序号摘要
func renameCalls(contents []antigravity.Content) {
	used, current := map[string]bool{}, map[string]string{}
	for _, content := range contents {
		for _, part := range content.Parts {
			if call := part.FunctionCall; call != nil && call.ID != "" {
				id := call.ID
				if !claudeCallID.MatchString(id) || used[id] {
					id = strings.Map(func(char rune) rune {
						if char == '_' || char == '-' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
							return char
						}
						return '_'
					}, id)
					for occurrence := 0; len(id) > 64 || used[id]; occurrence++ {
						sum := sha256.Sum256(fmt.Appendf(nil, "%d:%s", occurrence, call.ID))
						id = id[:min(len(id), 55)] + "_" + hex.EncodeToString(sum[:4])
					}
				}
				used[id], current[call.ID] = true, id
				call.ID = id
			}
			if result := part.FunctionResult; result != nil && current[result.ID] != "" {
				result.ID = current[result.ID]
			}
		}
	}
}

// renameFunctions 把模型族不接受的函数名改写为合规名称，返回上游名到原名的映射
// 字符集与前缀按模型族处理，超长或重名时截断并附原名摘要
func renameFunctions(request antigravity.GenerateRequest, claude bool) (antigravity.GenerateRequest, map[string]string) {
	used := map[string]bool{}
	var names []string
	add := func(name string) {
		if name != "" && !used[name] {
			used[name] = true
			names = append(names, name)
		}
	}
	for _, declaration := range request.Tools.Functions {
		add(declaration.Name)
	}
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				add(part.FunctionCall.Name)
			}
			if part.FunctionResult != nil {
				add(part.FunctionResult.Name)
			}
		}
	}
	renamed, original := map[string]string{}, map[string]string{}
	valid, symbols := functionName, "_-.:"
	if claude {
		valid, symbols = claudeFunctionName, "_-"
	}
	for _, name := range names {
		if valid.MatchString(name) {
			continue
		}
		safe := strings.Map(func(char rune) rune {
			if strings.ContainsRune(symbols, char) || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
				return char
			}
			return '_'
		}, name)
		if !claude && strings.ContainsAny(safe[:1], "0123456789-.:") {
			safe = "_" + safe
		}
		if len(safe) > 128 || used[safe] || original[safe] != "" {
			sum := sha256.Sum256([]byte(name))
			safe = safe[:min(len(safe), 119)] + "_" + hex.EncodeToString(sum[:4])
		}
		renamed[name], original[safe] = safe, name
	}
	if len(renamed) == 0 {
		return request, nil
	}
	rename := func(name string) string { return cmp.Or(renamed[name], name) }
	request.Tools.Functions = slices.Clone(request.Tools.Functions)
	for index := range request.Tools.Functions {
		request.Tools.Functions[index].Name = rename(request.Tools.Functions[index].Name)
	}
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			if part.FunctionCall != nil {
				part.FunctionCall.Name = rename(part.FunctionCall.Name)
			}
			if part.FunctionResult != nil {
				part.FunctionResult.Name = rename(part.FunctionResult.Name)
			}
		}
	}
	return request, original
}
