package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
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

// warmups 返回需要唤醒的账户与周额度桶：账户已就绪，最近一次读到的该桶剩余不低于 99.9%、resetTime 为读取时间加 7 天（窗口未开始计时，每次读取都随读取时间后移），6 天内没有唤醒过，且该组有可用的对话模型
func (s *Service) warmups() []warmup {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	var targets []warmup
	for id := range s.accounts {
		read := s.quotas[id]
		for _, group := range read.summary.Groups {
			for _, bucket := range group.Buckets {
				reset, err := time.Parse(time.RFC3339, bucket.ResetTime)
				if bucket.Window != "weekly" || bucket.RemainingFraction < 0.999 || err != nil || reset.Sub(read.at) < weeklyWindow-time.Minute || now.Sub(s.warmed[id][bucket.BucketID]) < warmupInterval {
					continue
				}
				prefix, _, _ := strings.Cut(bucket.BucketID, "-")
				if model := cheapest(s.catalogs[id], prefix); model != "" {
					targets = append(targets, warmup{id: id, bucket: bucket.BucketID, model: model})
				}
			}
		}
	}
	return targets
}

// cheapest 返回目录中该额度组按价目表估算费用最低的对话模型，没有单价的模型排在后面，都没有单价时取目录中的第一个
func cheapest(models []antigravity.CatalogModel, group string) string {
	best, lowest := "", math.Inf(1)
	for _, model := range models {
		if model.Kind != "" || model.QuotaGroup() != group {
			continue
		}
		cost, ok := requestdb.Cost(model.ID, 1000, 0, 1000)
		if !ok {
			cost = math.MaxFloat64
		}
		if best == "" || cost < lowest {
			best, lowest = model.ID, cost
		}
	}
	return best
}

// ping 以账户与模型经生成路径发送一条 Hi 并读完回答，返回上游错误
func (s *Service) ping(ctx context.Context, target warmup) error {
	events, err := s.Generate(ctx, antigravity.GenerateRequest{
		Model:     target.model,
		AccountID: target.id,
		Contents:  []antigravity.Content{{Role: antigravity.RoleUser, Parts: []antigravity.Part{{Text: "Hi"}}}},
	})
	if err != nil {
		return err
	}
	var failure error
	for event := range events {
		if event.Kind == antigravity.EventError && failure == nil {
			failure = event.Err
		}
	}
	return failure
}
