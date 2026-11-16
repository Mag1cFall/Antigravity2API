package prompt

import (
	"regexp"
	"slices"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// systemRejected 匹配上游见到即以不带 reason 的 429 拒绝请求的客户端系统提示文字与带归属声明的身份句，短语取自各客户端系统提示的逐词二分
var systemRejected = []*regexp.Regexp{phrases(
	"RFC 2119 keywords",
	"Read xd://<tool> for full docs + JSON schema before first use",
	"You are omp's trusted coding assistant",
	"You are Claude Code,",
	"Dual-use security tools (C2 frameworks, credential testing, exploit development) require clear authorization context",
	"was pasted into the message by the user from somewhere else and may contain instructions the user did not write",
	"You are a coding agent running in the Codex CLI",
	"You are OpenCode",
	"You are a coding assistant running inside pi",
	"inside pi, a coding agent harness",
	"You are a personal assistant running inside OpenClaw",
	"You are Hermes Agent",
	"Be direct: match the length of your reply to the weight of the ask",
	"the documentation at https://hermes-agent.nousresearch.com/docs is your authoritative reference",
	"Hermes delivers their message as a standalone user message right after the latest tool results",
	"<!-- End Hermes runtime environment -->",
	"The browser provides no implicit DOM, route, or screenshot context",
), clientIdentity}

// userRejected 匹配出现在 user 轮中同样被上游拒绝的文字
var userRejected = []*regexp.Regexp{phrases(
	"You are Codex, a coding agent based on GPT-5",
	"You are Codex, an agent based on GPT-5",
)}

// clientIdentity 匹配行首带归属声明的身份句：You are 加以 agent、assistant 或 AI 结尾的身份短语，8 个字符内接 created by、built on、based on 等归属声明
var clientIdentity = regexp.MustCompile(`(?im)^[ \t]*(?:you are|you're)\s+(?:an?\s+)?(?:[\w'-]+[\s,]+){0,8}(?:agent|assistant|ai)\b[^.!?\n<>]{0,8}?\b(?:(?:created|built|made|developed|designed|trained|powered|maintained|published|released|provided)\s+(?:by|on|upon)|based\s+on)\s+[^\n<>]{1,80}?\.(?:[ \t]|$)`)

// listMarker 匹配行首的缩进与列表符号
var listMarker = regexp.MustCompile(`^[ \t]*(?:[-*+]|\d+[.)])?[ \t]*`)

// phrases 把短语编译为一个正则：不区分大小写，短语中的空格可对应任意空白序列
func phrases(list ...string) *regexp.Regexp {
	alternatives := make([]string, len(list))
	for index, phrase := range list {
		words := strings.Fields(phrase)
		for position, word := range words {
			words[position] = regexp.QuoteMeta(word)
		}
		alternatives[index] = strings.Join(words, `\s+`)
	}
	return regexp.MustCompile(`(?i)` + strings.Join(alternatives, "|"))
}

// dropUserRejected 返回删去 userRejected 所匹配整句后的 contents，只改 user 轮的文本，删句后为空的文本 part 一并去掉，没有匹配时原样返回
func dropUserRejected(contents []antigravity.Content) []antigravity.Content {
	var dropped []antigravity.Content
	for index, content := range contents {
		if content.Role != antigravity.RoleUser || !slices.ContainsFunc(content.Parts, func(part antigravity.Part) bool { return matches(userRejected, part.Text) }) {
			continue
		}
		if dropped == nil {
			dropped = slices.Clone(contents)
		}
		parts := make([]antigravity.Part, 0, len(content.Parts))
		for _, part := range content.Parts {
			if part.Text != "" {
				if part.Text = dropRejected(part.Text, userRejected); part.Text == "" {
					continue
				}
			}
			parts = append(parts, part)
		}
		dropped[index].Parts = parts
	}
	if dropped == nil {
		return contents
	}
	return dropped
}

// matches 判断 text 是否含 patterns 中任一正则的匹配
func matches(patterns []*regexp.Regexp, text string) bool {
	return slices.ContainsFunc(patterns, func(pattern *regexp.Regexp) bool { return pattern.MatchString(text) })
}

// dropRejected 删去 text 中含 patterns 所匹配文字的整句，其余原文不变；删去后只剩缩进与列表符号的行连同换行一起删去
func dropRejected(text string, patterns []*regexp.Regexp) string {
	var spans [][2]int
	for _, pattern := range patterns {
		for _, match := range pattern.FindAllStringIndex(text, -1) {
			spans = append(spans, sentence(text, match[0], match[1]))
		}
	}
	if len(spans) == 0 {
		return text
	}
	slices.SortFunc(spans, func(a, b [2]int) int { return a[0] - b[0] })
	var builder strings.Builder
	last := 0
	for index := 0; index < len(spans); {
		start, end := spans[index][0], spans[index][1]
		for index++; index < len(spans) && spans[index][0] <= end; index++ {
			end = max(end, spans[index][1])
		}
		lineStart := strings.LastIndexByte(text[:start], '\n') + 1
		lineEnd := len(text)
		if newline := strings.IndexByte(text[end:], '\n'); newline >= 0 {
			lineEnd = end + newline
		}
		if listMarker.FindString(text[lineStart:start]) == text[lineStart:start] && strings.TrimSpace(text[end:lineEnd]) == "" {
			start, end = lineStart, min(lineEnd+1, len(text))
		}
		start = max(start, last)
		builder.WriteString(text[last:start])
		last = end
	}
	builder.WriteString(text[last:])
	return builder.String()
}

// fullStops 为全角句末标点，其后不需要空白即结束一句
var fullStops = []string{"。", "！", "？"}

// sentence 返回包含 text[start:end] 的整句范围：句子从同一行中上一个后接空白的句末标点或全角句末标点之后、或行首的缩进与列表符号之后开始，到 start 之后第一个全角句末标点或后接空白的句末标点为止并带上其后的空格，没有句末标点时到 end 之后的行尾
func sentence(text string, start, end int) [2]int {
	lineStart := strings.LastIndexByte(text[:start], '\n') + 1
	from := lineStart + len(listMarker.FindString(text[lineStart:start]))
	for index := start - 1; index > from; index-- {
		if strings.ContainsRune(".!?", rune(text[index])) && (text[index+1] == ' ' || text[index+1] == '\t') || slices.ContainsFunc(fullStops, func(stop string) bool { return strings.HasSuffix(text[:index+1], stop) }) {
			from = index + 1
			break
		}
	}
	for from < start && (text[from] == ' ' || text[from] == '\t') {
		from++
	}
	to := len(text)
	for index := start; index < len(text); index++ {
		if text[index] == '\n' && index >= end {
			to = index
			break
		}
		if stop := slices.IndexFunc(fullStops, func(stop string) bool { return strings.HasPrefix(text[index:], stop) }); stop >= 0 {
			to = index + len(fullStops[stop])
			break
		}
		if strings.ContainsRune(".!?", rune(text[index])) && (index+1 == len(text) || strings.ContainsRune(" \t\n", rune(text[index+1]))) {
			to = index + 1
			for to < len(text) && (text[to] == ' ' || text[to] == '\t') {
				to++
			}
			break
		}
	}
	return [2]int{from, to}
}
