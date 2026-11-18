package service

import (
	"slices"
	"testing"
	"time"
)

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

// TestRewindToEarlierMessage 验证从任意更早的消息处回退、改写或截短的请求仍用原会话，第一条消息不同的请求新建会话
func TestRewindToEarlierMessage(t *testing.T) {
	records, err := openConversations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := []string{"head", "m1", "m2", "m3", "m4", "m5", "m6"}
	for _, test := range []struct {
		name    string
		request []string
		same    bool
	}{
		{"回退一条", []string{"head", "m1", "m2", "m3", "m4", "m5", "x", "y"}, true},
		{"改写第二条后保留其后的消息", []string{"head", "m1", "x", "m3", "m4", "m5", "m6", "y"}, true},
		{"截短到第二条", []string{"head", "m1", "m2"}, true},
		{"第一条不同", []string{"head", "x", "m2", "m3"}, false},
	} {
		// 各用例的消息摘要带用例名，在同一个目录中互不匹配
		mark := func(digests []string) []string {
			marked := []string{digests[0]}
			for index := range digests[1:] {
				marked = append(marked, digest(marked[index], []byte(test.name+digests[index+1])))
			}
			return marked
		}
		first, err := records.begin(mark(base), make([]string, len(base)-1))
		if err != nil {
			t.Fatal(err)
		}
		next, err := records.begin(mark(test.request), make([]string, len(test.request)-1))
		if err != nil || (next.Cascade == first.Cascade) != test.same || next.New == test.same {
			t.Fatalf("%s：返回 %v，会话 %s，New 为 %v，原会话 %s，期望沿用原会话为 %v", test.name, err, next.Cascade, next.New, first.Cascade, test.same)
		}
	}
}

// TestRewindPrefersLongerSharedConversation 验证另有一个只含相同首条消息的会话时，改写长会话最后一条消息的请求仍回到长会话，并沿用长会话中相同消息的时间
func TestRewindPrefersLongerSharedConversation(t *testing.T) {
	records, err := openConversations(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Unix(1700000000, 0)
	records.now = func() time.Time { return clock }
	chained := func(messages ...string) []string {
		digests := []string{"head"}
		for index, message := range messages {
			digests = append(digests, digest(digests[index], []byte(message)))
		}
		return digests
	}
	long, err := records.begin(chained("first", "reply", "second"), make([]string, 3))
	if err != nil {
		t.Fatal(err)
	}
	if err := records.record(long.Cascade, chained("first", "reply", "second"), "answer", "model", 3, 10, signatureRecord{}); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	short, err := records.begin(chained("first"), make([]string, 1))
	if err != nil || !short.New {
		t.Fatalf("以相同首条消息新开的请求返回 %v，New 为 %v，期望新建会话", err, short.New)
	}
	clock = clock.Add(time.Minute)
	edited, err := records.begin(chained("first", "reply", "changed"), make([]string, 3))
	if err != nil || edited.Cascade != long.Cascade || !slices.Equal(edited.Times[:2], long.Times[:2]) || edited.Times[2] != clock.Unix() {
		t.Fatalf("改写长会话最后一条的请求返回 %v，会话 %s 时间 %v，期望长会话 %s、前 2 条时间 %v、第 3 条为当前时间", err, edited.Cascade, edited.Times, long.Cascade, long.Times[:2])
	}
}
