package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

const (
	// assistantPrefixInstruction 为末尾 assistant 轮改写为续写前缀时追加的系统指令
	assistantPrefixInstruction = "Continue the assistant response after the following prefix. Return only the continuation, without repeating the prefix."
	// jsonInstruction 为要求 JSON 输出时追加的系统指令
	jsonInstruction = "Respond with a single JSON value only, without Markdown code fences or any other text."
	// schemaInstruction 为要求按 JSON Schema 输出时追加的系统指令前缀
	schemaInstruction = "Respond with a single JSON value that conforms to this JSON Schema, without Markdown code fences or any other text:\n"
)

// billingPrefix 为 Claude Code 计费行的开头
const billingPrefix = "x-anthropic-billing-header:"

// claudeFunctionName 为 Claude 族接受的函数名
var claudeFunctionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// prepare 把客户端请求改写为上游接受的形状并返回输出处理：目录条目不支持图片的模型收到图片时返回 400，去掉其他模型族生成的签名并补回会话保存的函数调用签名，末尾 assistant 轮与只有系统提示的请求按模型族改写，工具选择、单调用与结构化输出写入系统指令并在输出中核对，Claude 族不接受的函数名可逆改名
func prepare(request antigravity.GenerateRequest, model antigravity.CatalogModel, signed signatureRecord) (antigravity.GenerateRequest, *output, error) {
	if !model.SupportsImages && hasImage(request.Contents) {
		return request, nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("model %s does not support image input", model.ID)}
	}
	request.Contents = restoreSignatures(cloneContents(request.Contents), signed, model.ModelProvider)
	var err error
	if request, err = normalizeTurns(request, model); err != nil {
		return request, nil, err
	}
	out := &output{hide: request.Config.HideThinking, limit: request.Config.MaxOutputTokens, stop: newStopMatcher(request.Config.StopSequences), provider: model.ModelProvider, signed: newSignatureRecord()}
	if request, out.tools, err = prepareTools(request); err != nil {
		return request, nil, err
	}
	if request, out.json, err = prepareJSON(request); err != nil {
		return request, nil, err
	}
	if model.ModelProvider == antigravity.ProviderAnthropic {
		request, out.names = renameFunctions(request)
	}
	return request, out, nil
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

// restoreSignatures 去掉会话中记为其他模型族生成的签名，去掉后为空的 part 一并删除，再为客户端未回传签名的函数调用补回会话保存的签名
func restoreSignatures(contents []antigravity.Content, signed signatureRecord, provider string) []antigravity.Content {
	for index := range contents {
		parts := contents[index].Parts[:0]
		for _, part := range contents[index].Parts {
			if signed.foreign(part.ThoughtSignature, provider) {
				part.ThoughtSignature = ""
				if part.Text == "" && part.InlineData == nil && part.File == nil && part.FunctionCall == nil && part.FunctionResult == nil {
					continue
				}
			}
			if call := part.FunctionCall; call != nil {
				if signed.foreign(call.ThoughtSignature, provider) {
					call.ThoughtSignature = ""
				}
				if restored := signed.calls[call.ID]; call.ThoughtSignature == "" && part.ThoughtSignature == "" && !signed.foreign(restored, provider) {
					call.ThoughtSignature = restored
				}
			}
			parts = append(parts, part)
		}
		contents[index].Parts = parts
	}
	return contents
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

// prepareTools 按工具选择改写声明：none 不声明工具，指定函数与 allowed_tools 只声明所选函数，required 与单调用写入系统指令，strict 函数与 validated 模式在输出中校验参数；返回需要在输出中核对的约定
func prepareTools(request antigravity.GenerateRequest) (antigravity.GenerateRequest, *toolContract, error) {
	config := request.Tools.ToolConfig
	if config.Mode == "none" {
		request.Tools.Functions, request.Tools.GoogleSearch = nil, nil
		return request, nil, nil
	}
	contract := &toolContract{required: config.Mode == "required", names: map[string]bool{}, schemas: map[string]*schemaValidator{}}
	functions := make([]antigravity.FunctionDeclaration, 0, len(request.Tools.Functions))
	for _, declaration := range request.Tools.Functions {
		if len(config.AllowedFunctionNames) > 0 && !slices.Contains(config.AllowedFunctionNames, declaration.Name) {
			continue
		}
		contract.names[declaration.Name] = true
		functions = append(functions, declaration)
		if !declaration.Strict && config.Mode != "validated" {
			continue
		}
		schema := declaration.Schema
		if len(schema) == 0 {
			schema = declaration.Parameters
		}
		validator, err := compileSchema(schema)
		if err != nil {
			return request, nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("function %q parameters: %v", declaration.Name, err)}
		}
		contract.schemas[declaration.Name] = validator
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
	if !contract.required && !contract.single && len(contract.schemas) == 0 && len(config.AllowedFunctionNames) == 0 {
		return request, nil, nil
	}
	return request, contract, nil
}

// prepareJSON 把 JSON 输出要求写入系统指令，返回输出时的 JSON 与 Schema 校验
func prepareJSON(request antigravity.GenerateRequest) (antigravity.GenerateRequest, *jsonOutput, error) {
	config := request.Config
	if len(config.ResponseSchema) == 0 && !strings.EqualFold(config.ResponseMIMEType, "application/json") {
		return request, nil, nil
	}
	if len(config.ResponseSchema) == 0 {
		request.System = joinInstruction(request.System, jsonInstruction)
		return request, &jsonOutput{}, nil
	}
	validator, err := compileSchema(config.ResponseSchema)
	if err != nil {
		return request, nil, &Error{Status: http.StatusBadRequest, Message: "response schema: " + err.Error()}
	}
	request.System = joinInstruction(request.System, schemaInstruction+string(config.ResponseSchema))
	return request, &jsonOutput{schema: validator}, nil
}

// renameFunctions 把 Claude 族不接受的函数名换成只含字母、数字、下划线与连字符的名称，超长或重名时截断并加原名摘要；返回上游名到原名的映射
func renameFunctions(request antigravity.GenerateRequest) (antigravity.GenerateRequest, map[string]string) {
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
	for _, name := range names {
		if claudeFunctionName.MatchString(name) {
			continue
		}
		safe := strings.Map(func(char rune) rune {
			if char == '_' || char == '-' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
				return char
			}
			return '_'
		}, name)
		if len(safe) > 128 || used[safe] || original[safe] != "" {
			sum := sha256.Sum256([]byte(name))
			safe = safe[:min(len(safe), 119)] + "_" + hex.EncodeToString(sum[:4])
		}
		renamed[name], original[safe] = safe, name
	}
	if len(renamed) == 0 {
		return request, nil
	}
	rename := func(name string) string { return cmpName(renamed[name], name) }
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

// cmpName 返回改名后的名称，没有改名时返回原名
func cmpName(renamed, name string) string {
	if renamed != "" {
		return renamed
	}
	return name
}
