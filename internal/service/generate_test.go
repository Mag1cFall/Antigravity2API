package service

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

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
		_, _, err := forward(upstream, func(event antigravity.Event) { events <- event }, nil, watch(context.Background(), time.Minute))
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
	if _, _, err := forward(upstream, func(event antigravity.Event) { deliver(ctx, make(chan antigravity.Event), event) }, nil, watch(ctx, time.Minute)); err != nil {
		t.Fatalf("forward 返回 %v", err)
	}
	if _, open := <-upstream; open {
		t.Fatal("客户端取消后上游没有被排空")
	}
}

// TestBeginKeepsConversationOfIdenticalRequest 验证成功后原样重发的请求归入原会话并沿用各消息的时间，之后一次多出几条消息的请求仍续接这个会话，改写已发消息与回退到早先消息的请求仍用原会话并沿用相同消息的时间，第一条消息不同的请求新建会话
func TestBeginKeepsConversationOfIdenticalRequest(t *testing.T) {
	records, err := openConversations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1700000000, 0)
	records.now = func() time.Time { return clock }
	digests, answers := []string{"head", "first", "second", "third"}, []string{"", "reply", ""}
	first, err := records.begin(digests, answers)
	if err != nil || !first.New {
		t.Fatalf("首次请求返回 %v，New 为 %v，期望新建会话", err, first.New)
	}
	if err := records.record(first.Cascade, digests, "answer", "model", 1, 10, signatureRecord{}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(4 * time.Minute)
	again, err := records.begin(digests, answers)
	if err != nil || again.New || again.Cascade != first.Cascade || again.Trajectory != first.Trajectory || !slices.Equal(again.Times, first.Times) {
		t.Fatalf("原样重发返回 %v，会话 %s 时间 %v，期望原会话 %s 时间 %v", err, again.Cascade, again.Times, first.Cascade, first.Times)
	}
	next, err := records.begin([]string{"head", "first", "second", "third", "fourth", "fifth"}, []string{"", "reply", "", "answer", ""})
	if err != nil || next.New || next.Cascade != first.Cascade || next.Times[0] != first.Times[0] {
		t.Fatalf("续接请求返回 %v，会话 %s 时间 %v，期望原会话 %s 且首条时间不变", err, next.Cascade, next.Times, first.Cascade)
	}
	clock = clock.Add(time.Minute)
	edited, err := records.begin([]string{"head", "first", "second", "third", "fourth", "changed", "sixth", "seventh"}, []string{"", "reply", "", "answer", "", "", ""})
	if err != nil || edited.New || edited.Cascade != first.Cascade || !slices.Equal(edited.Times[:4], next.Times[:4]) || edited.Times[4] != clock.Unix() {
		t.Fatalf("改写已发消息的请求返回 %v，会话 %s 时间 %v，期望原会话 %s、前 4 条时间不变、其后为当前时间", err, edited.Cascade, edited.Times, first.Cascade)
	}
	back, err := records.begin([]string{"head", "first", "second"}, []string{"", "reply"})
	if err != nil || back.New || back.Cascade != first.Cascade || !slices.Equal(back.Times, first.Times[:2]) {
		t.Fatalf("回退的请求返回 %v，会话 %s 时间 %v，期望原会话 %s 与前 2 条的时间", err, back.Cascade, back.Times, first.Cascade)
	}
	other, err := records.begin([]string{"head", "another", "more"}, []string{"", ""})
	if err != nil || !other.New {
		t.Fatalf("第一条消息不同的请求返回 %v，New 为 %v，期望新建会话", err, other.New)
	}
}

// TestBeginStartsConversationForRepeatedFirstMessage 验证只有一条消息的请求成功后原样重发时新建会话
func TestBeginStartsConversationForRepeatedFirstMessage(t *testing.T) {
	records, err := openConversations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	digests := []string{"head", "first"}
	first, err := records.begin(digests, []string{""})
	if err != nil {
		t.Fatal(err)
	}
	if err := records.record(first.Cascade, digests, "answer", "model", 1, 10, signatureRecord{}); err != nil {
		t.Fatal(err)
	}
	again, err := records.begin(digests, []string{""})
	if err != nil || !again.New || again.Cascade == first.Cascade {
		t.Fatalf("原样重发返回 %v，会话 %s，New 为 %v，期望新建会话", err, again.Cascade, again.New)
	}
}

// TestRewindWithinKeptDigests 验证会话记录只保留链位置之前最后几项链摘要时，回退不超过保留条数的请求仍用原会话，超过的与短于保留范围的新建会话；保留条数为 0 或多于已有消息时保留链首之后的全部
func TestRewindWithinKeptDigests(t *testing.T) {
	records, err := openConversations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"head", "m1", "m2", "m3", "m4", "m5", "m6"}
	for _, test := range []struct {
		name    string
		keep    int
		request []string
		stored  int
		same    bool
	}{
		{"回退一条", 3, []string{"head", "m1", "m2", "m3", "m4", "m5", "x", "y"}, 3, true},
		{"回退条数等于保留条数", 3, []string{"head", "m1", "m2", "m3", "x"}, 3, true},
		{"回退条数超过保留条数", 3, []string{"head", "m1", "m2", "x", "y"}, 3, false},
		{"截短到保留范围内", 3, []string{"head", "m1", "m2", "m3"}, 3, true},
		{"截短到保留范围之前", 3, []string{"head", "m1", "m2"}, 3, false},
		{"只保留一条时回退一条", 1, []string{"head", "m1", "m2", "m3", "m4", "m5", "x"}, 1, true},
		{"只保留一条时回退两条", 1, []string{"head", "m1", "m2", "m3", "m4", "x"}, 1, false},
		{"不限制", 0, []string{"head", "m1", "x"}, 5, true},
		{"保留条数多于已有消息", 10, []string{"head", "m1", "x"}, 5, true},
	} {
		// 各用例的消息摘要带用例名，在同一个目录中互不匹配
		mark := func(digests []string) []string {
			marked := []string{digests[0]}
			for _, digest := range digests[1:] {
				marked = append(marked, test.name+digest)
			}
			return marked
		}
		records.retain(test.keep)
		first, err := records.begin(mark(base), make([]string, len(base)-1))
		if err != nil {
			t.Fatal(err)
		}
		if stored := len(records.items[first.Cascade].Chain); stored != test.stored {
			t.Fatalf("%s：记录保留了 %d 项链摘要，期望 %d 项", test.name, stored, test.stored)
		}
		next, err := records.begin(mark(test.request), make([]string, len(test.request)-1))
		if err != nil || (next.Cascade == first.Cascade) != test.same || next.New == test.same {
			t.Fatalf("%s：返回 %v，会话 %s，New 为 %v，原会话 %s，期望沿用原会话为 %v", test.name, err, next.Cascade, next.New, first.Cascade, test.same)
		}
	}
	records = &conversations{keep: 3}
	for length, stored := range []int{0, 0, 1, 2, 3, 3} {
		if kept := len(records.tail(make([]string, length+1))); kept != stored {
			t.Fatalf("%d 条消息的请求保留了 %d 项链摘要，期望 %d 项", length, kept, stored)
		}
	}
}
