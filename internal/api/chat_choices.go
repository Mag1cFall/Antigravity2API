package api

import (
	"context"
	"sync"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// maxChatChoices 是 Chat n 的上限
const maxChatChoices = 128

// chatChoices 是一次请求按 n 并发启动的生成，任一生成失败时整个请求按首个错误失败
type chatChoices struct {
	ctx    context.Context
	cancel context.CancelFunc
	events []<-chan antigravity.Event
	err    error
	once   sync.Once
}

// startChatChoices 在各自 goroutine 中启动每个 choice 并立即读取其事件；非流式请求返回前等待各 Generate 返回，流式请求等待首个就绪的 choice，其余 choice 的启动错误作为该 choice 的流内错误
func (s *server) startChatChoices(ctx context.Context, request antigravity.GenerateRequest, count int, stream bool) *chatChoices {
	request.Unary = !stream
	choices := &chatChoices{events: make([]<-chan antigravity.Event, count)}
	choices.ctx, choices.cancel = context.WithCancel(ctx)
	started := make(chan error, count)
	for index := range count {
		choice := request
		if index > 0 {
			choice.Contents = cloneResponseContents(request.Contents)
			if request.Config.Seed != nil {
				seed := *request.Config.Seed + int64(index)
				choice.Config.Seed = &seed
			}
		}
		events := make(chan antigravity.Event)
		choices.events[index] = events
		go func() {
			upstream, err := s.service.Generate(choices.ctx, choice)
			source := upstream
			if err == nil && stream {
				source, _, err = awaitStreamStart(choices.ctx, upstream)
			}
			started <- err
			if err != nil {
				failed := make(chan antigravity.Event, 1)
				failed <- antigravity.Event{Kind: antigravity.EventError, Err: err}
				close(failed)
				source = failed
			}
			relayEvents(choices.ctx, source, events)
			if upstream != nil {
				for range upstream {
				}
			}
		}()
	}
	waits := count
	if stream {
		waits = 1
	}
	for range waits {
		if err := <-started; err != nil {
			choices.fail(err)
			break
		}
	}
	return choices
}

// relayEvents 经内存队列把 source 的事件转发到 out
func relayEvents(ctx context.Context, source <-chan antigravity.Event, out chan<- antigravity.Event) {
	var queue []antigravity.Event
	for source != nil || len(queue) > 0 {
		var send chan<- antigravity.Event
		var next antigravity.Event
		if len(queue) > 0 {
			send, next = out, queue[0]
		}
		select {
		case <-ctx.Done():
			return
		case send <- next:
			queue = queue[1:]
		case event, ok := <-source:
			if !ok {
				source = nil
				continue
			}
			queue = append(queue, event)
		}
	}
	close(out)
}

// fail 记录首个错误并取消其余生成
func (choices *chatChoices) fail(err error) {
	choices.once.Do(func() {
		choices.err = err
		choices.cancel()
	})
}

// run 并发消费各 choice 的事件流并按 index 返回结果
func (choices *chatChoices) run(consume func(int, <-chan antigravity.Event) (generationResult, error)) ([]generationResult, error) {
	results := make([]generationResult, len(choices.events))
	var wait sync.WaitGroup
	for index, events := range choices.events {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, err := consume(index, events)
			if err != nil {
				choices.fail(err)
			}
			results[index] = result
		}()
	}
	wait.Wait()
	return results, choices.err
}

// sumUsage 返回各次生成用量之和，均无用量时返回 nil
func sumUsage(results []generationResult) *antigravity.Usage {
	var total *antigravity.Usage
	for index := range results {
		usage := results[index].usage
		if usage == nil {
			continue
		}
		if total == nil {
			total = &antigravity.Usage{}
		}
		total.InputTokens += usage.InputTokens
		total.CachedTokens += usage.CachedTokens
		total.OutputTokens += usage.OutputTokens
		total.ReasoningTokens += usage.ReasoningTokens
		total.TotalTokens += usage.TotalTokens
	}
	return total
}
