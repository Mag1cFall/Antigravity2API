package tokencount

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// harmonySystem 为 gpt-oss 聊天模板的 system 消息正文，Current date 取固定日期，YYYY-MM-DD 格式的各日期 token 数相同
const harmonySystem = "You are ChatGPT, a large language model trained by OpenAI.\nKnowledge cutoff: 2024-06\nCurrent date: 2026-01-01\n\nReasoning: medium\n\n# Valid channels: analysis, commentary, final. Channel must be included for every message."

// harmonyToolsLine 为请求带函数声明时 system 消息追加的一行
const harmonyToolsLine = "\nCalls to these tools must go to the commentary channel: 'functions'."

// harmonyNestedGap 为聊天模板渲染嵌套对象属性时冒号后保留的模板空白
const harmonyNestedGap = "\n                "

// harmonySpecial 匹配 harmony 格式的特殊 token
var harmonySpecial = regexp.MustCompile(`<\|(start|end|message|channel|constrain|call|return)\|>`)

// harmonyTokens 返回 harmony 提示按 o200k_base 分词的 token 数，特殊 token 各计 1
func harmonyTokens(prompt string) int64 {
	var total int64
	last := 0
	for _, match := range harmonySpecial.FindAllStringIndex(prompt, -1) {
		total += Text(antigravity.ProviderOpenAI, prompt[last:match[0]]) + 1
		last = match[1]
	}
	return total + Text(antigravity.ProviderOpenAI, prompt[last:])
}

// harmonyPrompt 按 gpt-oss 聊天模板（openai/gpt-oss-120b chat_template.jinja）渲染上游请求：先渲染 system 消息，带系统指令或函数声明时渲染 developer 消息；contents 依次渲染为 user、assistant 回答、函数调用与函数结果消息，一条 content 中的多个函数调用只渲染第一个，多个函数结果以换行连接第一个之后的各结果；最后以回答开头结束
func harmonyPrompt(request antigravity.WireRequest) string {
	var prompt strings.Builder
	var declarations []antigravity.WireFunctionDeclaration
	for _, tool := range request.Tools {
		declarations = append(declarations, tool.FunctionDeclarations...)
	}
	prompt.WriteString("<|start|>system<|message|>" + harmonySystem)
	if len(declarations) > 0 {
		prompt.WriteString(harmonyToolsLine)
	}
	prompt.WriteString("<|end|>")
	var instructions string
	if request.SystemInstruction != nil {
		for _, part := range request.SystemInstruction.Parts {
			instructions += part.Text
		}
	}
	if instructions != "" || len(declarations) > 0 {
		prompt.WriteString("<|start|>developer<|message|>")
		if instructions != "" {
			prompt.WriteString("# Instructions\n\n" + instructions + "\n\n")
		}
		if len(declarations) > 0 {
			prompt.WriteString("# Tools\n\n" + harmonyNamespace(declarations))
		}
		prompt.WriteString("<|end|>")
	}
	called := ""
	for index, content := range request.Contents {
		var text strings.Builder
		var calls []*antigravity.WireFunctionCall
		var results []*antigravity.WireFunctionResponse
		for _, part := range content.Parts {
			text.WriteString(part.Text)
			if part.FunctionCall != nil {
				calls = append(calls, part.FunctionCall)
			}
			if part.FunctionResponse != nil {
				results = append(results, part.FunctionResponse)
			}
		}
		switch {
		case len(calls) > 0:
			if text.Len() > 0 && !slices.ContainsFunc(request.Contents[index+1:], harmonyFinal) {
				prompt.WriteString("<|start|>assistant<|channel|>analysis<|message|>" + text.String() + "<|end|>")
			}
			called = calls[0].Name
			prompt.WriteString("<|start|>assistant to=functions." + called + "<|channel|>commentary json<|message|>" + pythonJSON(calls[0].Args) + "<|call|>")
		case len(results) > 0:
			if called == "" {
				called = results[0].Name
			}
			if len(results) > 1 {
				results = results[1:]
			}
			outputs := make([]string, len(results))
			for index, result := range results {
				outputs[index] = `{"output":` + pythonString(result.Response.Output) + "}"
			}
			prompt.WriteString("<|start|>functions." + called + " to=assistant<|channel|>commentary<|message|>" + pythonString(strings.Join(outputs, "\n")) + "<|end|>")
		case content.Role == "user":
			prompt.WriteString("<|start|>user<|message|>" + text.String() + "<|end|>")
		default:
			prompt.WriteString("<|start|>assistant<|channel|>final<|message|>" + text.String() + "<|end|>")
			called = ""
		}
	}
	prompt.WriteString("<|start|>assistant")
	return prompt.String()
}

// harmonyFinal 报告 content 是否渲染为 assistant 的最终回答
func harmonyFinal(content antigravity.WireContent) bool {
	return content.Role != "user" && !slices.ContainsFunc(content.Parts, func(part antigravity.WirePart) bool {
		return part.FunctionCall != nil || part.FunctionResponse != nil
	})
}

// harmonyNamespace 按聊天模板把函数声明渲染为 TypeScript 的 functions 命名空间
func harmonyNamespace(declarations []antigravity.WireFunctionDeclaration) string {
	var namespace strings.Builder
	namespace.WriteString("## functions\n\nnamespace functions {\n\n")
	for _, declaration := range declarations {
		namespace.WriteString("// " + declaration.Description + "\n")
		namespace.WriteString("type " + declaration.Name + " = ")
		var parameters schema
		json.Unmarshal(declaration.Parameters, &parameters)
		if len(parameters.Properties) == 0 {
			namespace.WriteString("() => any;\n\n")
			continue
		}
		namespace.WriteString("(_: {\n")
		for _, property := range parameters.Properties {
			if property.Schema.Description != "" {
				namespace.WriteString("// " + property.Schema.Description + "\n")
			}
			namespace.WriteString(property.Name + optional(parameters.Required, property.Name) + ": " + typescript(property.Schema) + ",\n")
		}
		namespace.WriteString("}) => any;\n\n")
	}
	namespace.WriteString("} // namespace functions")
	return namespace.String()
}

// schema 为函数声明参数中渲染 TypeScript 类型所用的 Gemini Schema 字段，Properties 保持原顺序
type schema struct {
	Type        string
	Description string
	Enum        []string
	Nullable    bool
	Items       *schema
	Required    []string
	Properties  []property
}

// property 为对象类型的一个属性
type property struct {
	Name   string
	Schema schema
}

// UnmarshalJSON 解析 Gemini Schema，类型转为小写，属性按 JSON 中的顺序保存
func (s *schema) UnmarshalJSON(data []byte) error {
	var fields struct {
		Type        string          `json:"type"`
		Description string          `json:"description"`
		Enum        []string        `json:"enum"`
		Nullable    bool            `json:"nullable"`
		Items       *schema         `json:"items"`
		Required    []string        `json:"required"`
		Properties  json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	*s = schema{Type: strings.ToLower(fields.Type), Description: fields.Description, Enum: fields.Enum, Nullable: fields.Nullable, Items: fields.Items, Required: fields.Required}
	if len(fields.Properties) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(fields.Properties))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return err
		}
		var value schema
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		s.Properties = append(s.Properties, property{Name: fmt.Sprint(name), Schema: value})
	}
	return nil
}

// typescript 按聊天模板的 render_typescript_type 把 Gemini Schema 渲染为 TypeScript 类型
func typescript(s schema) string {
	switch s.Type {
	case "array":
		rendered := "any[]"
		if s.Items != nil {
			switch s.Items.Type {
			case "string", "number", "boolean":
				rendered = s.Items.Type + "[]"
			case "integer":
				rendered = "number[]"
			default:
				if inner := typescript(*s.Items); utf8.RuneCountInString(inner) <= 50 {
					rendered = inner + "[]"
				}
			}
		}
		if s.Nullable {
			rendered += " | null"
		}
		return rendered
	case "string":
		if len(s.Enum) > 0 {
			return `"` + strings.Join(s.Enum, `" | "`) + `"`
		}
		if s.Nullable {
			return "string | null"
		}
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "object":
		if len(s.Properties) == 0 {
			return "object"
		}
		fields := make([]string, len(s.Properties))
		for index, property := range s.Properties {
			fields[index] = property.Name + optional(s.Required, property.Name) + ": " + harmonyNestedGap + typescript(property.Schema)
		}
		return "{\n" + strings.Join(fields, ", ") + "}"
	}
	return "any"
}

// optional 返回不在 required 中的属性名后缀 "?"
func optional(required []string, name string) string {
	if slices.Contains(required, name) {
		return ""
	}
	return "?"
}

// pythonJSON 按 Python json.dumps 的默认格式重写 JSON 文本：", " 与 ": " 分隔、不转义非 ASCII，键序与数字原样保留
func pythonJSON(raw json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out strings.Builder
	var write func() error
	write = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch value := token.(type) {
		case json.Delim:
			out.WriteRune(rune(value))
			for index := 0; decoder.More(); index++ {
				if index > 0 {
					out.WriteString(", ")
				}
				if value == '{' {
					key, err := decoder.Token()
					if err != nil {
						return err
					}
					out.WriteString(pythonString(fmt.Sprint(key)) + ": ")
				}
				if err := write(); err != nil {
					return err
				}
			}
			closing, err := decoder.Token()
			if err != nil {
				return err
			}
			out.WriteRune(rune(closing.(json.Delim)))
		case string:
			out.WriteString(pythonString(value))
		case json.Number:
			out.WriteString(value.String())
		case bool:
			out.WriteString(fmt.Sprint(value))
		case nil:
			out.WriteString("null")
		}
		return nil
	}
	write()
	return out.String()
}

// pythonString 按 Python json.dumps(ensure_ascii=False) 转义字符串并加引号
func pythonString(value string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			out.WriteString(`\"`)
		case '\\':
			out.WriteString(`\\`)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\b':
			out.WriteString(`\b`)
		case '\f':
			out.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&out, `\u%04x`, r)
			} else {
				out.WriteRune(r)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}
