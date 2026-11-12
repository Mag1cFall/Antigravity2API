package service

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/balancer"
	"github.com/Mag1cFall/Antigravity2API/internal/config"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

const (
	// firstEventSampleLimit 是每个模型保留的近期原请求首字样本数
	firstEventSampleLimit = 200
	// firstEventSampleAge 是首字样本参与统计的最长时间
	firstEventSampleAge = 24 * time.Hour
	// firstEventMinSamples 是 auto 阈值需要的最少样本数，不足时不发起备用请求
	firstEventMinSamples = 20
	// hedgeLimit 是同时进行的备用请求数上限
	hedgeLimit = 4
	// hedgeRecheck 是没有空闲账户或备用请求已达上限时再次尝试发起的间隔
	hedgeRecheck = time.Second
)

// firstEventSample 是一次原请求从发出到首个上游事件或被备用请求取代的时长与上游请求正文字节数
type firstEventSample struct {
	at    time.Time
	wait  time.Duration
	bytes int
}

// firstEventStats 按目录模型 ID 保存近期原请求的首字等待，备用请求不计入
type firstEventStats struct {
	mu      sync.Mutex
	samples map[string][]firstEventSample
}

// observe 记录一个原请求样本，每个模型只保留最近 firstEventSampleLimit 个
func (stats *firstEventStats) observe(model string, at time.Time, wait time.Duration, bytes int) {
	stats.mu.Lock()
	defer stats.mu.Unlock()
	samples := append(stats.samples[model], firstEventSample{at: at, wait: wait, bytes: bytes})
	stats.samples[model] = samples[max(0, len(samples)-firstEventSampleLimit):]
}

// p95 按最近秩法返回模型 firstEventSampleAge 内样本的 p95 与样本数，没有样本时 p95 为 0
func (stats *firstEventStats) p95(model string, now time.Time) (time.Duration, int) {
	stats.mu.Lock()
	var waits []time.Duration
	for _, sample := range stats.samples[model] {
		if now.Sub(sample.at) <= firstEventSampleAge {
			waits = append(waits, sample.wait)
		}
	}
	stats.mu.Unlock()
	if len(waits) == 0 {
		return 0, 0
	}
	slices.Sort(waits)
	return waits[(len(waits)*95+99)/100-1], len(waits)
}

// size 按最近秩法返回模型 firstEventSampleAge 内样本上游请求正文字节数的 p95，没有样本时为 0
func (stats *firstEventStats) size(model string, now time.Time) int {
	stats.mu.Lock()
	var sizes []int
	for _, sample := range stats.samples[model] {
		if now.Sub(sample.at) <= firstEventSampleAge {
			sizes = append(sizes, sample.bytes)
		}
	}
	stats.mu.Unlock()
	if len(sizes) == 0 {
		return 0
	}
	slices.Sort(sizes)
	return sizes[(len(sizes)*95+99)/100-1]
}

// seed 按时间顺序从请求账本记录补入原请求样本：被备用请求取代的原请求取其已等时长与正文字节数，其余有首字的记录取首字耗时减排队与最后一次尝试的正文字节数（没有尝试时为 0）
func (stats *firstEventStats) seed(rows []requestdb.Row) {
	slices.SortFunc(rows, func(a, b requestdb.Row) int { return a.Time.Compare(b.Time) })
	for _, row := range rows {
		superseded := slices.IndexFunc(row.Attempts, func(attempt requestdb.Attempt) bool { return attempt.Superseded && !attempt.Hedge })
		switch {
		case superseded >= 0:
			stats.observe(row.UpstreamModel, row.Time, time.Duration(row.Attempts[superseded].DurationMS)*time.Millisecond, row.Attempts[superseded].RequestBytes)
		case row.FirstEventMS > 0 && row.UpstreamModel != "":
			bytes := 0
			if len(row.Attempts) > 0 {
				bytes = row.Attempts[len(row.Attempts)-1].RequestBytes
			}
			stats.observe(row.UpstreamModel, row.Time, time.Duration(row.FirstEventMS-row.QueueMS)*time.Millisecond, bytes)
		}
	}
}

// seedFirstEvents 从请求账本读取 firstEventSampleAge 内的全部记录补入首字样本
func (s *Service) seedFirstEvents() error {
	now := time.Now()
	rows, _, err := s.ledger.Records(context.Background(), requestdb.RecordQuery{From: now.Add(-firstEventSampleAge), To: now, Limit: 1 << 20})
	if err != nil {
		return err
	}
	s.firstEvents.seed(rows)
	return nil
}

// FirstEventLatency 是一个目录模型近期原请求首字等待的 p95 与样本数
type FirstEventLatency struct {
	Model   string `json:"model"`
	Samples int    `json:"samples"`
	P95MS   int64  `json:"p95_ms"`
}

// FirstEventLatencies 返回各目录模型 firstEventSampleAge 内原请求首字等待的 p95 与样本数，按模型 ID 排序，没有近期样本的模型不列出
func (s *Service) FirstEventLatencies() []FirstEventLatency {
	s.firstEvents.mu.Lock()
	models := slices.Sorted(maps.Keys(s.firstEvents.samples))
	s.firstEvents.mu.Unlock()
	now := time.Now()
	latencies := []FirstEventLatency{}
	for _, model := range models {
		if p95, samples := s.firstEvents.p95(model, now); samples > 0 {
			latencies = append(latencies, FirstEventLatency{Model: model, Samples: samples, P95MS: p95.Milliseconds()})
		}
	}
	return latencies
}

// hedgeDelay 返回上游正文为 bytes 字节的原请求等待首字多久后发起备用请求；后台响应、FIRST_EVENT_HEDGE 为 off、模型不在 FIRST_EVENT_HEDGE_MODELS 中、auto 的样本少于 firstEventMinSamples 或 bytes 超过样本正文字节数的 p95 时第二个返回值为 false
func (s *Service) hedgeDelay(cfg *config.Config, request antigravity.GenerateRequest, model antigravity.CatalogModel, bytes int) (time.Duration, bool) {
	if request.Background || !listed(cfg.FirstEventHedgeModels, model) {
		return 0, false
	}
	auto, after, _ := config.ParseFirstEventHedge(cfg.FirstEventHedge)
	if !auto {
		return after, after > 0
	}
	now := time.Now()
	p95, samples := s.firstEvents.p95(model.ID, now)
	return p95, samples >= firstEventMinSamples && bytes <= s.firstEvents.size(model.ID, now)
}

// hedgeLease 不等待槽位，立即为备用请求取得租约：先用请求可用、尚未尝试且没有进行中请求的其他账户，再用原账户的空闲槽位；都没有时返回 nil
func (s *Service) hedgeLease(request antigravity.GenerateRequest, primary string, tried map[string]bool, model antigravity.CatalogModel) *balancer.Lease {
	now, cancel := context.WithCancel(context.Background())
	cancel()
	s.mu.Lock()
	ids := slices.Collect(maps.Keys(s.accounts))
	s.mu.Unlock()
	idle := make(map[string]bool, len(ids))
	for _, id := range ids {
		if state, ok := s.pool.Status(id); ok && state.Active == 0 {
			idle[id] = true
		}
	}
	excluded := skip(request, tried)
	lowerTier := s.lowerTier(model)
	if lease, err := s.pool.Acquire(now, model.ID, "", func(id string) bool { return excluded(id) || !idle[id] }, lowerTier); err == nil {
		return lease
	}
	if lease, err := s.pool.Acquire(now, model.ID, "", func(id string) bool { return id != primary }, lowerTier); err == nil {
		return lease
	}
	return nil
}

// hedgePlan 是原请求可以发起的备用请求：等待首字的阈值与发起备用请求的函数，没有空闲账户时 start 返回 nil
type hedgePlan struct {
	delay time.Duration
	start func() *upstreamStream
}

// await 等待原请求的首个上游事件，正常开始的原请求的首字等待记为样本；plan 不为 nil 时原请求到阈值仍没有首个事件则发起一个备用请求，没有空闲账户或已有 hedgeLimit 个备用请求在进行时每 hedgeRecheck 再试；先返回首个事件的一方胜出、另一方取消，一方失败而另一方仍在等待时交给 failed 处理，一方的首个事件为错误而另一方仍在等待时取代它；返回胜出的一方，两方都失败时返回后失败的一方
func (s *Service) await(ctx context.Context, primary *upstreamStream, plan *hedgePlan, failed func(*upstreamStream)) *upstreamStream {
	model := primary.lease.Model.ID
	var check <-chan time.Time
	if plan != nil {
		timer := time.NewTimer(plan.delay - time.Since(primary.begun))
		defer timer.Stop()
		check = timer.C
	}
	var hedge *upstreamStream
	defer func() {
		if hedge != nil {
			s.hedges.Add(-1)
		}
	}()
	primaryDone := primary.done
	var hedgeDone chan struct{}
	for {
		select {
		case <-primaryDone:
			primaryDone, check = nil, nil
			if primary.err != nil && hedgeDone != nil {
				failed(primary)
				continue
			}
			if primary.first.Kind == antigravity.EventError && hedgeDone != nil {
				supersede(ctx, primary, "原请求的首个上游事件为错误")
				continue
			}
			if primary.err == nil && primary.first.Kind != antigravity.EventError {
				s.firstEvents.observe(model, primary.firstAt, primary.firstAt.Sub(primary.begun), primary.bytes)
			}
			if primary.err == nil && hedgeDone != nil {
				supersede(ctx, hedge, "原请求先返回首个上游事件")
				slog.InfoContext(ctx, fmt.Sprintf("原请求胜出，取消备用请求 | 备用账户=%s | 模型=%s", hedge.lease.ID, model), "source", primary.lease.ID)
			}
			return primary
		case <-hedgeDone:
			hedgeDone = nil
			if hedge.err != nil && primaryDone != nil {
				failed(hedge)
				continue
			}
			if hedge.first.Kind == antigravity.EventError && primaryDone != nil {
				supersede(ctx, hedge, "备用请求的首个上游事件为错误")
				continue
			}
			if hedge.err != nil {
				return hedge
			}
			waited := time.Since(primary.begun)
			if primaryDone != nil {
				s.firstEvents.observe(model, time.Now(), waited, primary.bytes)
				supersede(ctx, primary, "备用请求先返回首个上游事件")
			}
			slog.InfoContext(ctx, fmt.Sprintf("备用请求胜出 | 原请求已等待=%s | 模型=%s", waited.Round(time.Millisecond), model), "source", hedge.lease.ID)
			return hedge
		case <-check:
			check = nil
			if s.hedges.Add(1) > hedgeLimit {
				s.hedges.Add(-1)
				check = time.After(hedgeRecheck)
				continue
			}
			if hedge = plan.start(); hedge == nil {
				s.hedges.Add(-1)
				check = time.After(hedgeRecheck)
				continue
			}
			hedgeDone = hedge.done
			slog.WarnContext(ctx, fmt.Sprintf("备用请求发起 | 原请求账户=%s | 原请求已等待=%s | 阈值=%s | 模型=%s",
				primary.lease.ID, time.Since(primary.begun).Round(time.Millisecond), plan.delay.Round(time.Millisecond), model), "source", hedge.lease.ID)
		}
	}
}

// supersede 取消落败的一方，排空它的事件后记为被取代的尝试并归还槽位
func supersede(ctx context.Context, loser *upstreamStream, reason string) {
	loser.cancel()
	<-loser.done
	if loser.events != nil {
		for range loser.events {
		}
	}
	record := loser.record(loser.err, antigravity.Event{})
	record.Error, record.Superseded = reason, true
	requestdb.TraceFrom(ctx).AddAttempt(record)
	loser.lease.Release()
}
