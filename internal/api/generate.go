package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// errIncompleteStream 表示上游事件流没有结束事件就关闭
var errIncompleteStream = errors.New("upstream stream closed before finish")

// streamHeartbeatInterval 为流式响应的心跳间隔，也是写出响应头前等待首个事件的时长
const streamHeartbeatInterval = 10 * time.Second

// generationResult 为一次生成的全部事件与从中累积的正文、思考、函数调用、媒体、用量与结束信息
type generationResult struct {
	events        []antigravity.Event
	text          bytes.Buffer
	reasoning     bytes.Buffer
	toolCalls     []antigravity.FunctionCall
	media         []antigravity.Blob
	usage         *antigravity.Usage
	finishReason  string
	finishMessage string
	stopSequence  string
	providerModel string
	grounding     *antigravity.Grounding
	finished      bool
}

// apply 把一个事件累积进结果，错误事件返回其错误，函数调用的空参数补为空对象
func (result *generationResult) apply(event antigravity.Event) error {
	if event.ProviderModel != "" {
		result.providerModel = event.ProviderModel
	}
	if event.Usage != nil {
		usage := *event.Usage
		result.usage = &usage
	}
	if event.Kind == antigravity.EventError {
		return event.Err
	}
	switch event.Kind {
	case antigravity.EventText:
		result.text.WriteString(event.Text)
	case antigravity.EventReasoning:
		result.reasoning.WriteString(event.Text)
	case antigravity.EventToolCall:
		call := *event.ToolCall
		if len(call.Arguments) == 0 {
			call.Arguments = json.RawMessage(`{}`)
		}
		if !json.Valid(call.Arguments) {
			return fmt.Errorf("upstream tool call %q arguments are not JSON", call.Name)
		}
		event.ToolCall = &call
		result.toolCalls = append(result.toolCalls, call)
	case antigravity.EventMedia:
		result.media = append(result.media, *event.Media)
	case antigravity.EventGrounding:
		result.grounding = event.Grounding
	case antigravity.EventFinish:
		result.finishReason, result.finishMessage = event.FinishReason, event.FinishMessage
		result.stopSequence = event.StopSequence
		result.finished = true
	}
	result.events = append(result.events, event)
	return nil
}

// awaitStreamStart 在写出流式响应头前等待首个事件，首个事件为错误时返回该错误，心跳间隔内没有事件时直接返回原事件流；返回的首个事件在事件流中仍会重放
func awaitStreamStart(ctx context.Context, events <-chan antigravity.Event) (<-chan antigravity.Event, antigravity.Event, error) {
	timer := time.NewTimer(streamHeartbeatInterval)
	defer timer.Stop()
	var first antigravity.Event
	select {
	case <-ctx.Done():
		return nil, first, ctx.Err()
	case <-timer.C:
		return events, first, nil
	case event, ok := <-events:
		if !ok {
			return nil, first, errIncompleteStream
		}
		if event.Kind == antigravity.EventError {
			return nil, first, event.Err
		}
		first = event
	}
	replay := make(chan antigravity.Event)
	go func() {
		defer close(replay)
		for event, ok := first, true; ok; event, ok = <-events {
			select {
			case replay <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return replay, first, nil
}

// consumeEvents 收集全部事件，emit 不为空时逐个交给 emit
func consumeEvents(ctx context.Context, events <-chan antigravity.Event, emit func(antigravity.Event) error) (generationResult, error) {
	var result generationResult
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case event, ok := <-events:
			if !ok {
				if !result.finished {
					return result, errIncompleteStream
				}
				return result, nil
			}
			if err := result.apply(event); err != nil {
				return result, err
			}
			if emit != nil {
				if err := emit(event); err != nil {
					return result, err
				}
			}
		}
	}
}

// mergedTextEvents 把相邻、同为正文或同为思考且都不带思考签名的事件合并为一个事件，其余事件原样保留
func mergedTextEvents(events []antigravity.Event) []antigravity.Event {
	merged := make([]antigravity.Event, 0, len(events))
	var text strings.Builder
	open := false
	flush := func() {
		if open {
			merged[len(merged)-1].Text = text.String()
			text.Reset()
		}
	}
	for _, event := range events {
		mergeable := (event.Kind == antigravity.EventText || event.Kind == antigravity.EventReasoning) && event.ThoughtSignature == ""
		if !mergeable || !open || merged[len(merged)-1].Kind != event.Kind {
			flush()
			merged = append(merged, event)
			open = mergeable
		}
		if mergeable {
			text.WriteString(event.Text)
		}
	}
	flush()
	return merged
}

// renderedText 按事件顺序拼接正文与媒体 Markdown，媒体前后各起一行
func renderedText(events []antigravity.Event, media mediaOutput) string {
	var content strings.Builder
	for _, event := range events {
		switch event.Kind {
		case antigravity.EventText:
			content.WriteString(event.Text)
		case antigravity.EventMedia:
			if content.Len() > 0 && !strings.HasSuffix(content.String(), "\n") {
				content.WriteByte('\n')
			}
			content.WriteString(media.markdown(*event.Media) + "\n")
		}
	}
	return content.String()
}

// outputTokens 返回含思考在内的输出 token 数
func outputTokens(usage *antigravity.Usage) int64 {
	return usage.OutputTokens + usage.ReasoningTokens
}

// clientSession 返回请求的会话标识：通过校验的 API key 与 values 中首个非空的客户端会话标识，以 NUL 相连
func clientSession(r *http.Request, values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return requestAPIKey(r) + "\x00" + value
		}
	}
	return requestAPIKey(r) + "\x00"
}

// headerSession 返回 Images 与 Audio 请求的会话分区，客户端会话标识只取 session_id 与 conversation_id 请求头
func headerSession(r *http.Request) string {
	return clientSession(r, r.Header.Get("session_id"), r.Header.Get("conversation_id"))
}

// functionResultContent 把工具结果转成函数结果内容与媒体：字符串与其他 JSON 原样保留；内容块数组中带 text 的块与 parse 转出的文本以换行相连写成 JSON 字符串，其余块由 parse 转成函数结果的媒体 part
func functionResultContent(raw json.RawMessage, parse func(json.RawMessage) ([]antigravity.Part, error)) (json.RawMessage, []antigravity.Part, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(`""`), nil, nil
	}
	if !json.Valid(raw) {
		return nil, nil, fmt.Errorf("function result must be JSON")
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return raw, nil, nil
	}
	var texts []string
	var media []antigravity.Part
	for _, block := range blocks {
		var text struct {
			Text *string `json:"text"`
		}
		if json.Unmarshal(block, &text) == nil && text.Text != nil {
			texts = append(texts, *text.Text)
			continue
		}
		parts, err := parse(json.RawMessage("[" + string(block) + "]"))
		if err != nil {
			return nil, nil, err
		}
		for _, part := range parts {
			if part.Text != "" {
				texts = append(texts, part.Text)
			} else {
				media = append(media, part)
			}
		}
	}
	output, _ := json.Marshal(strings.Join(texts, "\n"))
	return output, media, nil
}

// systemReminder 把对话中的 system 消息写成 system-reminder 文本 part
func systemReminder(text string) antigravity.Part {
	return antigravity.Part{Text: "<system-reminder>\n" + text + "\n</system-reminder>"}
}

// appendWithReminders 追加一条消息，待并入的 system-reminder 排在下一个 user 消息开头的函数结果之后
func appendWithReminders(contents []antigravity.Content, content antigravity.Content, reminders []antigravity.Part) ([]antigravity.Content, []antigravity.Part) {
	if content.Role == antigravity.RoleUser && len(reminders) > 0 {
		results := 0
		for results < len(content.Parts) && content.Parts[results].FunctionResult != nil {
			results++
		}
		content.Parts = slices.Concat(content.Parts[:results], reminders, content.Parts[results:])
		reminders = nil
	}
	return append(contents, content), reminders
}
