package prompt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/rules"
)

// Rejected 是解析后的上游拒绝句规则：上游见到即以不带 reason 的 429 拒绝请求的文字，system 匹配客户端系统提示，user 匹配 user 轮
type Rejected struct {
	// Version 是列表 `! Version:` 头的值，没有时为空
	Version string
	// Rules 是规则条数
	Rules int
	// Digest 是各份列表原文的 SHA-256
	Digest string
	system []*regexp.Regexp
	user   []*regexp.Regexp
}

// rejected 是请求使用的拒绝句规则，初值为内置列表
var rejected atomic.Pointer[Rejected]

func init() {
	builtin, err := ParseRejected(string(rules.Rejected))
	if err != nil {
		panic(err)
	}
	rejected.Store(builtin)
}

// SetRejected 替换之后的请求使用的拒绝句规则
func SetRejected(r *Rejected) {
	rejected.Store(r)
}

// RejectedDigest 返回请求当前使用的拒绝句规则的摘要
func RejectedDigest() string {
	return rejected.Load().Digest
}

// ParseRejected 依次解析拒绝句列表文本并合并：`!` 开头的行为注释，第一份文本中的 `! Version:` 为版本；`[system]` 与 `[user]` 切换所在节，节标记之前的行属于 system；`/…/` 为 Go 正则，其余非空行为短语，不区分大小写，短语中的空格可对应任意空白序列
func ParseRejected(texts ...string) (*Rejected, error) {
	result := &Rejected{}
	var system, user []string
	hash := sha256.New()
	for index, text := range texts {
		hash.Write(append([]byte(text), 0))
		section := &system
		for number, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case line == "":
			case strings.HasPrefix(line, "!"):
				if version, ok := strings.CutPrefix(strings.TrimSpace(line[1:]), "Version:"); ok && index == 0 && result.Version == "" {
					result.Version = strings.TrimSpace(version)
				}
			case line == "[system]":
				section = &system
			case line == "[user]":
				section = &user
			case len(line) > 2 && strings.HasPrefix(line, "/") && strings.HasSuffix(line, "/"):
				pattern, err := regexp.Compile(line[1 : len(line)-1])
				if err != nil {
					return nil, fmt.Errorf("第 %d 份列表第 %d 行: %w", index+1, number+1, err)
				}
				if section == &system {
					result.system = append(result.system, pattern)
				} else {
					result.user = append(result.user, pattern)
				}
				result.Rules++
			default:
				*section = append(*section, line)
				result.Rules++
			}
		}
	}
	if len(system) > 0 {
		result.system = append(result.system, phrases(system...))
	}
	if len(user) > 0 {
		result.user = append(result.user, phrases(user...))
	}
	result.Digest = hex.EncodeToString(hash.Sum(nil))
	return result, nil
}

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

// dropUserRejected 返回删去 patterns 所匹配整句后的 contents，只改 user 轮的文本，删句后为空的文本 part 一并去掉，没有匹配时原样返回
func dropUserRejected(contents []antigravity.Content, patterns []*regexp.Regexp) []antigravity.Content {
	var dropped []antigravity.Content
	for index, content := range contents {
		if content.Role != antigravity.RoleUser || !slices.ContainsFunc(content.Parts, func(part antigravity.Part) bool { return matches(patterns, part.Text) }) {
			continue
		}
		if dropped == nil {
			dropped = slices.Clone(contents)
		}
		parts := make([]antigravity.Part, 0, len(content.Parts))
		for _, part := range content.Parts {
			if part.Text != "" {
				if part.Text = dropRejected(part.Text, patterns); part.Text == "" {
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
