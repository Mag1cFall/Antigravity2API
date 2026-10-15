package antigravity

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// KindTab 为编辑器版 Tab 补全使用的目录条目
	KindTab = "tab"
	// IDEVersion 为所模拟的编辑器版 Antigravity IDE 版本
	IDEVersion = "2.5.5"
	// requestTypeTab 为 Tab 补全请求的 requestType
	requestTypeTab = "tab"
	// tabMaxOutputTokens 为 Tab 补全请求的输出上限
	tabMaxOutputTokens = 2048
	// tabContext 为 TargetContent 在光标行前后各取的行数
	tabContext = 5
	// tabInstruction 为 Tab 补全请求的用户指令与预填 JSON 的 Instruction
	tabInstruction = "Please modify the following mentioned code block with the logical next edit. The <|cursor|> in the code block represents where my cursor is."
	// tabCursor 为代码块中光标位置的标记
	tabCursor = "<|cursor|>"
)

// tabSystemPrompt 为编辑器版 Tab 补全请求的系统指令
//
//go:embed tab_prompt.txt
var tabSystemPrompt string

// IDEUserAgent 返回编辑器版 language_server 在档案的系统与架构上发送的 User-Agent
func (d Device) IDEUserAgent() string {
	return fmt.Sprintf("antigravity/ide/%s (aidev_client; os_type=%s; arch=%s)", IDEVersion, d.OS, d.Arch)
}

// TabFile 为一次 Tab 补全中光标所在的文件：路径、file URI 与光标前后的全文
type TabFile struct {
	Path   string
	URI    string
	Prefix string
	Suffix string
}

// NewTabFile 返回设备档案主目录 Documents 下纯文本文件中的 Tab 补全文件，Windows 路径的盘符为小写
func NewTabFile(device Device, prefix, suffix string) TabFile {
	if device.OS == "windows" {
		home, _, _ := strings.Cut(device.AppDataDir, `\.gemini`)
		file := strings.ToLower(home[:1]) + home[1:] + `\Documents\completion.txt`
		return TabFile{Path: file, URI: "file:///" + strings.ReplaceAll(file, `\`, "/"), Prefix: prefix, Suffix: suffix}
	}
	home, _, _ := strings.Cut(device.AppDataDir, "/.gemini")
	file := path.Join(home, "Documents", "completion.txt")
	return TabFile{Path: file, URI: "file://" + file, Prefix: prefix, Suffix: suffix}
}

// target 返回光标行前后各 tabContext 行组成的 TargetContent，以及其中光标前后的两段，两端的空行去掉
func (f TabFile) target() (window, before, after string) {
	lines := strings.Split(f.Prefix+f.Suffix, "\n")
	cursor := strings.Count(f.Prefix, "\n")
	start, end := max(0, cursor-tabContext), min(len(lines)-1, cursor+tabContext)
	head := strings.Join(lines[:start], "\n")
	if start > 0 {
		head += "\n"
	}
	window = strings.Join(lines[start:end+1], "\n")
	offset := len(f.Prefix) - len(head)
	before, after = window[:offset], window[offset:]
	trimmed := strings.TrimLeft(before, "\n")
	return strings.Trim(window, "\n"), trimmed, strings.TrimRight(after, "\n")
}

// BuildTabEnvelope 按编辑器版 language_server 的 Tab 补全请求组装信封：查看文件的步骤与文件全文、带光标标记的用户请求，以及写到 ReplacementContent 的 replace_file_content 预填
func BuildTabEnvelope(project string, model CatalogModel, file TabFile, now time.Time) (Envelope, error) {
	content := file.Prefix + file.Suffix
	lines := len(strings.Split(content, "\n"))
	window, _, _ := file.target()
	prefill, err := tabPrefill(file.Path, window)
	if err != nil {
		return Envelope{}, err
	}
	texts := []string{
		fmt.Sprintf("Step Id: 0\nThe USER performed the following action:\nShow the contents of file %s from lines 1 to %d", file.Path, lines+1),
		fmt.Sprintf("Step Id: 0\nFile Path: `%s`\nTotal Lines: 0\nTotal Bytes: 0\nShowing lines 1 to %d\n%s\n\n", file.URI, lines+1, content),
		fmt.Sprintf("Step Id: 0\n<USER_REQUEST>\n%s\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: %s.\n\nThe user's current state is as follows:\nActive Document: %s (LANGUAGE_PLAINTEXT)\nCursor is on line: %d\n\nThe user has mentioned some items in the form @[ITEM]. Here is extra information about the items that were mentioned by the user, in the order that they appear:\n\n@[%s:L1-L%d] is a [Text Block]:\n%s%s%s\n</ADDITIONAL_METADATA>",
			tabInstruction, now.Format(time.RFC3339), file.Path, strings.Count(file.Prefix, "\n")+1, file.Path, lines, file.Prefix, tabCursor, file.Suffix),
	}
	contents := make([]WireContent, 0, len(texts)+1)
	for _, text := range texts {
		contents = append(contents, WireContent{Role: wireRoleUser, Parts: []WirePart{{Text: text}}})
	}
	contents = append(contents, WireContent{Role: wireRoleModel, Parts: []WirePart{{Text: prefill}}})
	budget := int64(0)
	return Envelope{
		Project:   project,
		RequestID: requestTypeTab + "/" + uuid.NewString(),
		Request: WireRequest{
			Contents:          contents,
			SystemInstruction: &WireContent{Role: wireRoleUser, Parts: []WirePart{{Text: tabSystemPrompt}}},
			GenerationConfig:  WireGenerationConfig{MaxOutputTokens: tabMaxOutputTokens, ThinkingConfig: WireThinkingConfig{ThinkingBudget: &budget}},
			SessionID:         sessionID,
		},
		Model:       model.ID,
		UserAgent:   envelopeUserAgent,
		RequestType: requestTypeTab,
	}, nil
}

// tabPrefill 返回模型续写前的 replace_file_content 预填，写到 ReplacementContent 键名为止
func tabPrefill(file, window string) (string, error) {
	quoted := make([]string, 0, 3)
	for _, value := range []string{file, tabInstruction, window} {
		encoded, err := marshal(value)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, string(encoded))
	}
	return "<replace_file_content>\n{\n\t\"TargetFile\": " + quoted[0] + ",\n\t\"CodeMarkdownLanguage\": \"plaintext\",\n\t\"Instruction\": " + quoted[1] + ",\n\t\"ReplacementChunks\": [\n\t\t{\n\t\t\t\"TargetContent\": " + quoted[2] + ",\n\t\t\t\"ReplacementContent", nil
}

// TabInsertion 从模型在预填之后的续写中取出 ReplacementContent，返回它在光标处插入的文字；替换内容改动了光标前后的原文时返回空串
func TabInsertion(file TabFile, completion string) string {
	colon := strings.IndexByte(completion, ':')
	start := strings.IndexByte(completion[colon+1:], '"') + colon + 1
	if colon < 0 || start == colon {
		return ""
	}
	end := start + 1
	for end < len(completion) && completion[end] != '"' {
		if completion[end] == '\\' {
			end++
		}
		end++
	}
	if end >= len(completion) {
		return ""
	}
	var replacement string
	if json.Unmarshal([]byte(completion[start:end+1]), &replacement) != nil {
		return ""
	}
	_, before, after := file.target()
	if len(replacement) < len(before)+len(after) || !strings.HasPrefix(replacement, before) || !strings.HasSuffix(replacement, after) {
		return ""
	}
	return replacement[len(before) : len(replacement)-len(after)]
}
