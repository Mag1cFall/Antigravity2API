package service

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/auth"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

const (
	// warmupsFile 是 RUNTIME_DIR 下记录各账户各周额度桶最近一次唤醒时间的文件
	warmupsFile = "warmups.json"
	// warmupInterval 是同一账户同一额度桶两次唤醒的最短间隔
	warmupInterval = 6 * 24 * time.Hour
	// weeklyWindow 是周额度窗口的长度，窗口未开始计时时额度摘要的 resetTime 为读取时间加上它
	weeklyWindow = 7 * 24 * time.Hour
)

// warmup 是一次周额度唤醒：账户、周额度桶与发送请求的模型
type warmup struct {
	id     string
	bucket string
	model  string
}

// loadWarmups 读取各账户各周额度桶最近一次唤醒的时间，文件不存在或无法解析时为空
func loadWarmups(path string) map[string]map[string]time.Time {
	warmups := make(map[string]map[string]time.Time)
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &warmups)
	}
	return warmups
}

// Warmup 在 WEEKLY_QUOTA_WARMUP 开启时为周额度窗口尚未开始计时的就绪账户各发一次最小生成请求，使该窗口开始计时；同一账户同一额度桶 6 天内只唤醒一次，成功的唤醒记入 RUNTIME_DIR/warmups.json 并重新读取该账户的额度
func (s *Service) Warmup(ctx context.Context) {
	if !s.live.Load().WeeklyQuotaWarmup {
		return
	}
	for _, target := range s.warmups() {
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		if err := s.ping(ctx, target); err != nil {
			slog.WarnContext(ctx, fmt.Sprintf("周额度唤醒失败 | 额度桶=%s | 模型=%s | 错误=%v", target.bucket, target.model, err), "source", target.id)
			continue
		}
		slog.InfoContext(ctx, fmt.Sprintf("周额度唤醒完成 | 额度桶=%s | 模型=%s | 耗时=%s", target.bucket, target.model, time.Since(started).Round(time.Millisecond)), "source", target.id)
		s.mu.Lock()
		if s.warmed[target.id] == nil {
			s.warmed[target.id] = make(map[string]time.Time)
		}
		s.warmed[target.id][target.bucket] = started
		data, err := json.Marshal(s.warmed)
		s.mu.Unlock()
		if err == nil {
			err = os.WriteFile(filepath.Join(s.live.Load().RuntimeDir, warmupsFile), data, 0o600)
		}
		if err != nil {
			slog.Warn("保存周额度唤醒记录失败 | 错误="+err.Error(), "source", "service")
		}
		if err := s.RefreshQuota(ctx, target.id); err != nil {
			slog.WarnContext(ctx, "读取额度摘要失败 | 错误="+err.Error(), "source", target.id)
		}
	}
}

// warmups 返回需要唤醒的账户与周额度桶：账户已启用且就绪，最近一次读到的该桶剩余不低于 99.9%、resetTime 为读取时间加 7 天（窗口未开始计时，每次读取都随读取时间后移），6 天内没有唤醒过，且该组有可用的对话模型
func (s *Service) warmups() []warmup {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var targets []warmup
	for id := range s.accounts {
		if !s.store.Enabled(id) {
			continue
		}
		read := s.quotas[id]
		for _, group := range read.summary.Groups {
			for _, bucket := range group.Buckets {
				reset, err := time.Parse(time.RFC3339, bucket.ResetTime)
				if bucket.Window != "weekly" || bucket.RemainingFraction < 0.999 || err != nil || reset.Sub(read.at) < weeklyWindow-time.Minute || now.Sub(s.warmed[id][bucket.BucketID]) < warmupInterval {
					continue
				}
				if model := cheapest(s.catalogs[id], antigravity.BucketGroup(bucket.BucketID)); model != "" {
					targets = append(targets, warmup{id: id, bucket: bucket.BucketID, model: model})
				}
			}
		}
	}
	return targets
}

// WarmupModels 按账户顺序合并全部账户（含停用账户）上一次保存的目录中的对话模型并去重，按 public 合并 Agent 模型的档位，供管理端预热选择模型；defaults 为各额度组由 cheapest 选出的模型的公开 ID
func (s *Service) WarmupModels() (models []antigravity.Model, defaults []string) {
	records := s.store.Accounts()
	ids := make(map[string]string)
	var catalog []antigravity.CatalogModel
	s.mu.Lock()
	for _, record := range records {
		for _, model := range s.catalogs[record.ID] {
			if model.Kind == "" && ids[model.ID] == "" {
				family, _ := familyOf(model)
				ids[model.ID] = cmp.Or(family, model.ID)
				catalog = append(catalog, model)
			}
		}
	}
	s.mu.Unlock()
	groups := make(map[string]bool)
	for _, model := range catalog {
		if group := model.QuotaGroup(); !groups[group] {
			groups[group] = true
			defaults = append(defaults, ids[cheapest(catalog, group)])
		}
	}
	return public(catalog), defaults
}

// cheapest 返回目录中该额度组按价目表估算费用最低的对话模型，没有单价的模型排在后面，都没有单价时取目录中的第一个
func cheapest(models []antigravity.CatalogModel, group string) string {
	best, lowest := "", math.Inf(1)
	for _, model := range models {
		if model.Kind != "" || model.QuotaGroup() != group {
			continue
		}
		cost, ok := requestdb.Cost(model.ID, time.Now(), 1000, 0, 1000)
		if !ok {
			cost = math.MaxFloat64
		}
		if best == "" || cost < lowest {
			best, lowest = model.ID, cost
		}
	}
	return best
}

// Prewarm 是管理端的手动预热：以账户与模型经生成路径发送一条 Hi 并读完回答，成功后重新读取该账户的额度，返回模型所属额度组；停用账户按冷启动临时初始化，只供手动预热使用，没有进行中的预热满 borrowLinger 后移出调度；账户不存在、未就绪或模型目录中没有该模型时不发送并返回原因
func (s *Service) Prewarm(ctx context.Context, id, name string) (string, error) {
	record, ok := s.store.Account(id)
	if !ok {
		return "", &Error{Status: http.StatusNotFound, Message: "账户不存在: " + id}
	}
	if !record.Enabled {
		if err := s.borrow(ctx, &record); err != nil {
			return "", &Error{Status: http.StatusServiceUnavailable, Message: "账户未就绪 " + err.Error()}
		}
		defer s.giveBack(id)
	}
	switch {
	case s.account(id) == nil:
		s.mu.Lock()
		reason := s.failures[id]
		s.mu.Unlock()
		return "", &Error{Status: http.StatusServiceUnavailable, Message: strings.TrimSpace("账户未就绪 " + reason)}
	}
	model, err := s.resolve(name, antigravity.GenerationConfig{})
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	listed := slices.ContainsFunc(s.catalogs[id], func(entry antigravity.CatalogModel) bool { return entry.ID == model.ID })
	s.mu.Unlock()
	if !listed {
		return model.QuotaGroup(), &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("账户的模型目录中没有 %s", model.ID)}
	}
	if err := s.ping(ctx, warmup{id: id, model: model.ID}); err != nil {
		return model.QuotaGroup(), err
	}
	if err := s.RefreshQuota(ctx, id); err != nil {
		slog.WarnContext(ctx, "读取额度摘要失败 | 错误="+err.Error(), "source", id)
	}
	return model.QuotaGroup(), nil
}

// ping 以账户与模型经生成路径发送一条 Hi 并读完回答，事件交给 context 中的请求 Trace，返回上游错误
func (s *Service) ping(ctx context.Context, target warmup) error {
	events, err := s.Generate(ctx, antigravity.GenerateRequest{
		Model:     target.model,
		AccountID: target.id,
		Contents:  []antigravity.Content{{Role: antigravity.RoleUser, Parts: []antigravity.Part{{Text: "Hi"}}}},
	})
	if err != nil {
		return err
	}
	trace := requestdb.TraceFrom(ctx)
	var failure error
	for event := range events {
		trace.Observe(event)
		if event.Kind == antigravity.EventError && failure == nil {
			failure = event.Err
		}
	}
	return failure
}

// borrowLinger 是手动预热临时启动的停用账户在没有进行中的预热后保留的时长，同一账户接着预热其他模型时复用这次初始化
const borrowLinger = 10 * time.Second

// borrowed 是一个手动预热临时启动的停用账户，active 为进行中的预热数，retire 为没有进行中的预热后移出调度的计时器
type borrowed struct {
	active int
	retire *time.Timer
}

// borrow 为停用账户的一次手动预热取得临时启动的账户：停止待执行的移出，账户不在账户池时按冷启动临时初始化
func (s *Service) borrow(ctx context.Context, record *auth.Account) error {
	s.mu.Lock()
	item := s.borrowed[record.ID]
	if item == nil {
		item = &borrowed{}
		s.borrowed[record.ID] = item
	}
	item.active++
	if item.retire != nil {
		item.retire.Stop()
		item.retire = nil
	}
	_, running := s.accounts[record.ID]
	s.mu.Unlock()
	if running {
		return nil
	}
	if err := s.initialize(ctx, record, true); err != nil {
		s.giveBack(record.ID)
		return err
	}
	return nil
}

// giveBack 结束停用账户的一次手动预热，没有进行中的预热满 borrowLinger 后在账户仍为停用时移出调度并停止伴随请求
func (s *Service) giveBack(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.borrowed[id]
	if item.active--; item.active > 0 {
		return
	}
	item.retire = time.AfterFunc(borrowLinger, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.borrowed[id] != item || item.active > 0 {
			return
		}
		delete(s.borrowed, id)
		if !s.store.Enabled(id) {
			s.remove(id)
		}
	})
}
