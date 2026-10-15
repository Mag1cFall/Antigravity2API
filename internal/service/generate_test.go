package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// stream 把事件写入已关闭的上游通道
func stream(events ...antigravity.Event) <-chan antigravity.Event {
	upstream := make(chan antigravity.Event, len(events))
	for _, event := range events {
		upstream <- event
	}
	close(upstream)
	return upstream
}

// collect 运行 forward 并收集转发出的全部事件
func collect(upstream <-chan antigravity.Event) ([]antigravity.Event, error) {
	events := make(chan antigravity.Event)
	result := make(chan error, 1)
	go func() {
		_, _, err := forward(context.Background(), upstream, events, nil, func() {})
		result <- err
		close(events)
	}()
	var forwarded []antigravity.Event
	for event := range events {
		forwarded = append(forwarded, event)
	}
	return forwarded, <-result
}

// TestForwardReturnsStreamError 验证流中错误原样转发并作为生成结果返回
func TestForwardReturnsStreamError(t *testing.T) {
	limited := &antigravity.UpstreamError{Status: http.StatusTooManyRequests}
	events, err := collect(stream(antigravity.Event{Kind: antigravity.EventText, Text: "partial"}, antigravity.Event{Kind: antigravity.EventError, Err: limited}))
	if status(err) != http.StatusTooManyRequests || len(events) != 2 {
		t.Fatalf("forward 返回 %v 与 %d 个事件，期望 429 与原样 2 个事件", err, len(events))
	}
}

// TestForwardDrainsUpstreamAfterCancel 验证客户端取消后继续排空上游事件
func TestForwardDrainsUpstreamAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	upstream := stream(antigravity.Event{Kind: antigravity.EventText, Text: "a"}, antigravity.Event{Kind: antigravity.EventFinish})
	if _, _, err := forward(ctx, upstream, make(chan antigravity.Event), nil, func() {}); err != nil {
		t.Fatalf("forward 返回 %v", err)
	}
	if _, open := <-upstream; open {
		t.Fatal("客户端取消后上游没有被排空")
	}
}
