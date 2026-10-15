package balancer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// testPool 创建时钟可拨动的账户池
func testPool(concurrency int) (*Pool, *time.Time) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	pool := New(concurrency)
	pool.now = func() time.Time { return now }
	return pool, &now
}

// models 按 ID 前缀生成 Gemini 或 Claude 目录条目
func models(ids ...string) []antigravity.CatalogModel {
	var catalog []antigravity.CatalogModel
	for _, id := range ids {
		provider := antigravity.ProviderAnthropic
		if strings.HasPrefix(id, "gemini") {
			provider = antigravity.ProviderGoogle
		}
		catalog = append(catalog, antigravity.CatalogModel{ID: id, ModelProvider: provider})
	}
	return catalog
}

// remaining 生成带 quotaInfo 剩余比例的目录条目
func remaining(id string, fraction float64, reset time.Time) antigravity.CatalogModel {
	model := models(id)[0]
	model.QuotaInfo = antigravity.QuotaInfo{RemainingFraction: fraction, ResetTime: reset.Format(time.RFC3339)}
	return model
}

// quota 生成两组各 5 小时与每周桶的额度摘要
func quota(reset time.Time, gemini5h, geminiWeekly, thirdParty5h, thirdPartyWeekly float64) antigravity.QuotaSummary {
	bucket := func(id string, fraction float64) antigravity.QuotaBucket {
		return antigravity.QuotaBucket{BucketID: id, ResetTime: reset.Format(time.RFC3339), RemainingFraction: fraction}
	}
	return antigravity.QuotaSummary{Groups: []antigravity.QuotaGroup{
		{DisplayName: "Gemini Models", Buckets: []antigravity.QuotaBucket{bucket("gemini-weekly", geminiWeekly), bucket("gemini-5h", gemini5h)}},
		{DisplayName: "Claude and GPT models", Buckets: []antigravity.QuotaBucket{bucket("3p-weekly", thirdPartyWeekly), bucket("3p-5h", thirdParty5h)}},
	}}
}

// acquire 立即获取租约并在失败时终止测试
func acquire(t *testing.T, pool *Pool, model, session string, exclude map[string]bool) *Lease {
	t.Helper()
	lease, err := pool.Acquire(context.Background(), model, session, exclude, false)
	if err != nil {
		t.Fatalf("Acquire(%s) 失败: %v", model, err)
	}
	return lease
}

func TestAcquireUsesAccountsWhoseCatalogHasModel(t *testing.T) {
	pool, _ := testPool(2)
	pool.Set("gemini-only", models("gemini-4-argon-high"))
	pool.Set("claude-only", models("claude-opus-5-5-high"))
	lease := acquire(t, pool, "claude-opus-5-5-high", "", nil)
	if lease.ID != "claude-only" || lease.Model.ID != "claude-opus-5-5-high" {
		t.Fatalf("租约 = %s %s，期望 claude-only 上的 claude-opus-5-5-high", lease.ID, lease.Model.ID)
	}
	var cooldown *CooldownError
	if _, err := pool.Acquire(context.Background(), "claude-sonnet-4-6", "", nil, false); !errors.As(err, &cooldown) || !cooldown.Until.IsZero() {
		t.Fatalf("不在目录中的模型返回 %v，期望无截止时间的 CooldownError", err)
	}
}

func TestAcquireRotatesLeastRecentlyUsed(t *testing.T) {
	pool, _ := testPool(2)
	for _, id := range []string{"a", "b", "c"} {
		pool.Set(id, models("claude-opus-5-5-high"))
	}
	var order []string
	for range 6 {
		lease := acquire(t, pool, "claude-opus-5-5-high", "", nil)
		order = append(order, lease.ID)
		lease.Release()
	}
	if got := strings.Join(order, ","); got != "a,b,c,a,b,c" {
		t.Fatalf("选择顺序 = %s，期望 a,b,c,a,b,c", got)
	}
}

func TestAcquireWaitsForFreeSlot(t *testing.T) {
	pool, _ := testPool(1)
	pool.Set("a", models("claude-opus-5-5-high"))
	first := acquire(t, pool, "claude-opus-5-5-high", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := pool.Acquire(ctx, "claude-opus-5-5-high", "", nil, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("并发已满时 Acquire 返回 %v，期望等待到 ctx 超时", err)
	}
	result := make(chan *Lease)
	go func() {
		lease, _ := pool.Acquire(context.Background(), "claude-opus-5-5-high", "", nil, false)
		result <- lease
	}()
	first.Release()
	select {
	case lease := <-result:
		if lease == nil || lease.ID != "a" {
			t.Fatalf("释放后取得 %v，期望账户 a", lease)
		}
	case <-time.After(time.Second):
		t.Fatal("释放槽位后等待者没有取得租约")
	}
}

func TestLimitCoolsOnlyThatGroupUntilCooldownEnds(t *testing.T) {
	pool, now := testPool(2)
	pool.Set("a", models("gemini-4-argon-high", "claude-opus-5-5-high"))
	pool.Limit("a", antigravity.QuotaGroupThirdParty, now.Add(time.Minute))
	var cooldown *CooldownError
	if _, err := pool.Acquire(context.Background(), "claude-opus-5-5-high", "", nil, false); !errors.As(err, &cooldown) || !cooldown.Until.Equal(now.Add(time.Minute)) {
		t.Fatalf("429 冷却中返回 %v，期望截止 %s 的 CooldownError", err, now.Add(time.Minute))
	}
	acquire(t, pool, "gemini-4-argon-high", "", nil).Release()
	*now = now.Add(time.Minute)
	acquire(t, pool, "claude-opus-5-5-high", "", nil)
}

func TestExhaustedBucketCoolsGroupUntilReset(t *testing.T) {
	pool, now := testPool(2)
	reset := now.Add(30 * time.Minute)
	pool.Set("a", models("claude-opus-5-5-high"))
	pool.Set("b", models("claude-opus-5-5-high"))
	pool.SetQuota("a", quota(reset, 1, 1, 0, 0.7))
	pool.SetQuota("b", quota(reset, 1, 1, 0.1, 0.1))
	if lease := acquire(t, pool, "claude-opus-5-5-high", "", nil); lease.ID != "b" {
		t.Fatalf("选中 %s，期望跳过 5 小时桶耗尽的 a", lease.ID)
	}
	status, _ := pool.Status("a")
	if until := status.Cooldown[antigravity.QuotaGroupThirdParty]; !until.Equal(reset) {
		t.Fatalf("a 的第三方组截止 = %s，期望桶重置时间 %s", until, reset)
	}
	*now = reset
	if lease := acquire(t, pool, "claude-opus-5-5-high", "", nil); lease.ID != "a" {
		t.Fatalf("重置后选中 %s，期望已过重置时间按满额计的 a", lease.ID)
	}
}

func TestSessionSticksUntilAccountUnavailable(t *testing.T) {
	pool, now := testPool(2)
	reset := now.Add(time.Hour)
	pool.Set("a", models("claude-opus-5-5-high"))
	pool.Set("b", models("claude-opus-5-5-high"))
	pool.Set("a", []antigravity.CatalogModel{remaining("claude-opus-5-5-high", 0.9, reset)})
	pool.Set("b", []antigravity.CatalogModel{remaining("claude-opus-5-5-high", 0.5, reset)})
	acquire(t, pool, "claude-opus-5-5-high", "cascade", nil).Release()
	pool.Set("b", []antigravity.CatalogModel{remaining("claude-opus-5-5-high", 0.95, reset)})
	if lease := acquire(t, pool, "claude-opus-5-5-high", "cascade", nil); lease.ID != "a" {
		t.Fatalf("同一会话选中 %s，期望沿用 a", lease.ID)
	} else {
		lease.Release()
	}
	pool.Limit("a", antigravity.QuotaGroupThirdParty, now.Add(time.Minute))
	acquire(t, pool, "claude-opus-5-5-high", "cascade", nil).Release()
	*now = now.Add(time.Minute)
	if lease := acquire(t, pool, "claude-opus-5-5-high", "cascade", nil); lease.ID != "b" {
		t.Fatalf("冷却结束后同一会话选中 %s，期望沿用改绑后的 b", lease.ID)
	}
}

func TestAcquireSkipsExcludedAccounts(t *testing.T) {
	pool, _ := testPool(2)
	pool.Set("a", models("claude-opus-5-5-high"))
	pool.Set("b", models("claude-opus-5-5-high"))
	if lease := acquire(t, pool, "claude-opus-5-5-high", "", map[string]bool{"a": true}); lease.ID != "b" {
		t.Fatalf("排除 a 后选中 %s，期望 b", lease.ID)
	}
	var cooldown *CooldownError
	if _, err := pool.Acquire(context.Background(), "claude-opus-5-5-high", "", map[string]bool{"a": true, "b": true}, false); !errors.As(err, &cooldown) {
		t.Fatalf("全部排除时返回 %v，期望 CooldownError", err)
	}
}

func TestModelsMergesCatalogsInOrder(t *testing.T) {
	pool, _ := testPool(2)
	pool.Set("a", models("gemini-4-argon-high", "claude-opus-5-5-high"))
	pool.Set("b", models("claude-opus-5-5-high", "claude-sonnet-5-5-low"))
	var ids []string
	for _, model := range pool.Models() {
		ids = append(ids, model.ID)
	}
	if got := strings.Join(ids, ","); got != "gemini-4-argon-high,claude-opus-5-5-high,claude-sonnet-5-5-low" {
		t.Fatalf("合并目录 = %s", got)
	}
}
