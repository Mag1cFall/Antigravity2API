package api

import (
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

var errIncompleteStream = errors.New("upstream stream closed before finish")
var errUpstreamStream = errors.New("upstream stream error")

const streamHeartbeatInterval = 10 * time.Second

type generationResult struct {
	events        []antigravity.Event
	text          strings.Builder
	reasoning     strings.Builder
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

func (result *generationResult) apply(event antigravity.Event) error {
	if event.ProviderModel != "" {
		result.providerModel = event.ProviderModel
	}
	if event.Usage != nil {
		usage := *event.Usage
		result.usage = &usage
	}
	if event.Kind == antigravity.EventError {
		if event.Err != nil {
			return event.Err
		}
		return errUpstreamStream
	}
	switch event.Kind {
	case antigravity.EventText:
		result.text.WriteString(event.Text)
	case antigravity.EventReasoning:
		result.reasoning.WriteString(event.Text)
	case antigravity.EventToolCall:
		if event.ToolCall != nil {
			call := *event.ToolCall
			if len(call.Arguments) == 0 {
				call.Arguments = json.RawMessage(`{}`)
			}
			if !json.Valid(call.Arguments) {
				return fmt.Errorf("upstream tool call %q arguments are not JSON", call.Name)
			}
			event.ToolCall = &call
			result.toolCalls = append(result.toolCalls, call)
		}
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
			if event.Err != nil {
				return nil, first, event.Err
			}
			return nil, first, errUpstreamStream
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

func consumeEvents(ctx context.Context, events <-chan antigravity.Event, emit func(antigravity.Event) error) (result generationResult, resultErr error) {
	return consumeEventsWithHeartbeat(ctx, events, emit, nil)
}

func consumeStreamEvents(
	ctx context.Context,
	events <-chan antigravity.Event,
	emit func(antigravity.Event) error,
	heartbeat func() error,
) (generationResult, error) {
	return consumeEventsWithHeartbeat(ctx, events, emit, heartbeat)
}

func consumeEventsWithHeartbeat(
	ctx context.Context,
	events <-chan antigravity.Event,
	emit func(antigravity.Event) error,
	heartbeat func() error,
) (result generationResult, resultErr error) {
	var heartbeatTimer *time.Timer
	var heartbeatTick <-chan time.Time
	if heartbeat != nil {
		heartbeatTimer = time.NewTimer(streamHeartbeatInterval)
		heartbeatTick = heartbeatTimer.C
		defer heartbeatTimer.Stop()
	}
	resetHeartbeat := func() {
		if heartbeatTimer == nil {
			return
		}
		if !heartbeatTimer.Stop() {
			select {
			case <-heartbeatTimer.C:
			default:
			}
		}
		heartbeatTimer.Reset(streamHeartbeatInterval)
	}
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-heartbeatTick:
			if err := heartbeat(); err != nil {
				return result, err
			}
			resetHeartbeat()
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
			resetHeartbeat()
		}
	}
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

func outputTokens(usage *antigravity.Usage) int64 {
	if usage == nil {
		return 0
	}
	return usage.OutputTokens + usage.ReasoningTokens
}

func inputTokens(usage *antigravity.Usage) int64 {
	if usage == nil {
		return 0
	}
	return usage.InputTokens
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
