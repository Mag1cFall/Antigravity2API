package balancer

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// sessionTTL 为会话粘连在最后一次使用后的保留时长
const sessionTTL = time.Hour

// CooldownError 表示支持该模型的账户都在冷却或已被本次请求排除
type CooldownError struct {
	Model string
	Until time.Time
}

func (e *CooldownError) Error() string {
	if e.Until.IsZero() {
		return fmt.Sprintf("模型 %s 没有可用账户", e.Model)
	}
	return fmt.Sprintf("模型 %s 的账户均在冷却，最早恢复时间 %s", e.Model, e.Until.Local().Format(time.RFC3339))
}

// HTTPStatus 返回公开协议使用的状态码
func (e *CooldownError) HTTPStatus() int {
	return http.StatusTooManyRequests
}

// RetryAt 返回最早有账户恢复的时刻，没有冷却截止时间时为零值
func (e *CooldownError) RetryAt(time.Time) time.Time {
	return e.Until
}

// Status 表示一个账户的调度状态，Cooldown 为各额度组或模型的不可用截止时间
type Status struct {
	Active   int
	Cooldown map[string]time.Time
	Quota    antigravity.QuotaSummary
}

// Lease 表示占用中的一个账户并发槽位，Model 为该账户目录中的模型条目，Credits 为该账户是否开启 AI 积分超额使用
type Lease struct {
	ID      string
	Model   antigravity.CatalogModel
	Credits bool
	pool    *Pool
	entry   *entry
}

// Release 归还并发槽位
func (l *Lease) Release() {
	l.pool.mu.Lock()
	defer l.pool.mu.Unlock()
	l.entry.active--
	l.pool.notify()
}

// entry 表示一个就绪账户的目录、额度摘要、按额度组或模型 ID 记录的冷却与并发
type entry struct {
	id       string
	models   []antigravity.CatalogModel
	quota    antigravity.QuotaSummary
	cooldown map[string]time.Time
	active   int
	// priority 为账户优先级，越大越先使用
	priority int
	// tier 为套餐等级，Free 为 0、Pro 为 1、Ultra 为 2
	tier int
	// used 为最近一次被选中时的选择序号，从未被选中时为 0
	used uint64
	// credits 为真时额度用尽后以 AI 积分继续生成
	credits bool
}

// binding 表示会话到账户的粘连
type binding struct {
	id      string
	expires time.Time
}

// Pool 按目录、额度、冷却与并发在账户之间调度请求，fillFirst 为真时先用满排在前面的账户
type Pool struct {
	mu      sync.Mutex
	entries []*entry
	// cooldowns 是各账户按额度组或模型记录的冷却，账户移出与重新加入后保留
	cooldowns   map[string]map[string]time.Time
	sessions    map[string]binding
	sweptAt     time.Time
	concurrency int
	fillFirst   bool
	changed     chan struct{}
	now         func() time.Time
	// picks 为已完成的选择次数
	picks uint64
	// reserve 为额度保留比例，额度组剩余比例不高于它时视为耗尽
	reserve float64
}

// New 创建账户池，concurrency 为单账户并发上限
func New(concurrency int) *Pool {
	return &Pool{cooldowns: make(map[string]map[string]time.Time), sessions: make(map[string]binding), concurrency: concurrency, changed: make(chan struct{}), now: time.Now}
}

// Configure 设置单账户并发上限与调度策略
func (p *Pool) Configure(concurrency int, fillFirst bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.concurrency != concurrency || p.fillFirst != fillFirst {
		p.concurrency, p.fillFirst = concurrency, fillFirst
		p.notify()
	}
}

// Set 加入账户或更新其目录与各模型的 quotaInfo，保留冷却与并发状态
func (p *Pool) Set(id string, models []antigravity.CatalogModel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entry(id); e != nil {
		e.models = models
	} else {
		p.entries = append(p.entries, &entry{id: id, models: models, cooldown: p.cooldown(id)})
	}
	p.notify()
}

// Reserve 设置额度保留比例
func (p *Pool) Reserve(fraction float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reserve != fraction {
		p.reserve = fraction
		p.notify()
	}
}

// Credits 设置账户是否开启 AI 积分超额使用，账户不在池中时忽略
func (p *Pool) Credits(id string, enabled bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entry(id); e != nil && e.credits != enabled {
		e.credits = enabled
		p.notify()
	}
}

// Profile 设置账户的优先级与套餐等级，账户不在池中时忽略
func (p *Pool) Profile(id string, priority, tier int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entry(id); e != nil {
		e.priority, e.tier = priority, tier
	}
}

// Remove 移出账户
func (p *Pool) Remove(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for index, e := range p.entries {
		if e.id == id {
			p.entries = append(p.entries[:index], p.entries[index+1:]...)
			break
		}
	}
	p.notify()
}

// SetQuota 记录账户最新的额度摘要
func (p *Pool) SetQuota(id string, summary antigravity.QuotaSummary) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.entry(id); e != nil {
		e.quota = summary
	}
	p.notify()
}

// Limit 把账户的一个额度组或一个模型冷却到 until
func (p *Pool) Limit(id, scope string, until time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cooldown := p.cooldown(id); until.After(cooldown[scope]) {
		cooldown[scope] = until
	}
	p.notify()
}

// Cooldowns 返回各账户尚未到期的冷却
func (p *Pool) Cooldowns() map[string]map[string]time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	result := make(map[string]map[string]time.Time)
	for id, cooldown := range p.cooldowns {
		for scope, until := range cooldown {
			if now.Before(until) {
				if result[id] == nil {
					result[id] = make(map[string]time.Time)
				}
				result[id][scope] = until
			}
		}
	}
	return result
}

// RestoreCooldowns 恢复先前保存的冷却，已到期的忽略
func (p *Pool) RestoreCooldowns(saved map[string]map[string]time.Time) {
	for id, cooldown := range saved {
		for scope, until := range cooldown {
			p.Limit(id, scope, until)
		}
	}
}

// cooldown 返回账户的冷却表，没有时创建，调用方持有 p.mu
func (p *Pool) cooldown(id string) map[string]time.Time {
	if p.cooldowns[id] == nil {
		p.cooldowns[id] = make(map[string]time.Time)
	}
	return p.cooldowns[id]
}

// Peers 返回与账户 id 共享该组额度池的其他账户：额度摘要中该组每个桶的窗口与重置时间都与 id 的相同，家庭方案的成员账户共享同一组桶
func (p *Pool) Peers(id, group string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	self := p.entry(id)
	if self == nil {
		return nil
	}
	own := resets(self.buckets(group))
	if len(own) == 0 {
		return nil
	}
	var peers []string
	for _, e := range p.entries {
		if e != self && maps.Equal(resets(e.buckets(group)), own) {
			peers = append(peers, e.id)
		}
	}
	return peers
}

// resets 返回各窗口的重置时间
func resets(buckets []antigravity.QuotaBucket) map[string]string {
	windows := make(map[string]string, len(buckets))
	for _, bucket := range buckets {
		windows[bucket.Window] = bucket.ResetTime
	}
	return windows
}

// Models 按账户顺序合并各账户目录并按 ID 去重
func (p *Pool) Models() []antigravity.CatalogModel {
	p.mu.Lock()
	defer p.mu.Unlock()
	var models []antigravity.CatalogModel
	seen := make(map[string]bool)
	for _, e := range p.entries {
		for _, model := range e.models {
			if !seen[model.ID] {
				seen[model.ID] = true
				models = append(models, model)
			}
		}
	}
	return models
}

// Model 返回首个包含该模型的账户目录条目
func (p *Pool) Model(id string) (antigravity.CatalogModel, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range p.entries {
		if model, ok := e.model(id); ok {
			return model, true
		}
	}
	return antigravity.CatalogModel{}, false
}

// Len 返回就绪账户数
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Status 返回账户的调度状态，账户不在池中时 ok 为 false
func (p *Pool) Status(id string) (Status, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.entry(id)
	if e == nil {
		return Status{}, false
	}
	now := p.now()
	status := Status{Active: e.active, Cooldown: make(map[string]time.Time), Quota: e.quota}
	for scope, until := range e.cooldown {
		if now.Before(until) {
			status.Cooldown[scope] = until
		}
	}
	for _, model := range e.models {
		if reset := e.exhausted(model, p.reserve); now.Before(reset) && reset.After(status.Cooldown[model.QuotaGroup()]) {
			status.Cooldown[model.QuotaGroup()] = reset
		}
	}
	return status, true
}

// Acquire 为模型选择账户并占用一个并发槽位，session 非空时优先沿用该会话上次的账户，exclude 中的账户不参与选择，lowerTier 时同优先级内先用套餐低的账户；候选账户并发已满时等待槽位
func (p *Pool) Acquire(ctx context.Context, model, session string, exclude map[string]bool, lowerTier bool) (*Lease, error) {
	for {
		p.mu.Lock()
		lease, err := p.pick(model, session, exclude, lowerTier)
		if lease != nil || err != nil {
			p.mu.Unlock()
			return lease, err
		}
		changed := p.changed
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

// Until 返回该模型最早有账户不在冷却的时刻，已有账户可用时为零值
func (p *Pool) Until(id string) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	var earliest time.Time
	for _, e := range p.entries {
		model, ok := e.model(id)
		if !ok {
			continue
		}
		until := e.until(model, p.reserve)
		if !now.Before(until) {
			return time.Time{}
		}
		if earliest.IsZero() || until.Before(earliest) {
			earliest = until
		}
	}
	return earliest
}

// pick 选出账户并占用槽位，有候选但并发全满时返回两个 nil
func (p *Pool) pick(id, session string, exclude map[string]bool, lowerTier bool) (*Lease, error) {
	now := p.now()
	p.sweep(now)
	var candidates []*entry
	var earliest time.Time
	for _, e := range p.entries {
		model, ok := e.model(id)
		if !ok || exclude[e.id] {
			continue
		}
		if until := e.until(model, p.reserve); now.Before(until) {
			if earliest.IsZero() || until.Before(earliest) {
				earliest = until
			}
			continue
		}
		candidates = append(candidates, e)
	}
	if len(candidates) == 0 {
		return nil, &CooldownError{Model: id, Until: earliest}
	}
	chosen := p.sticky(session, candidates, now)
	if chosen == nil {
		chosen = p.best(candidates, lowerTier)
	}
	if chosen == nil {
		return nil, nil
	}
	chosen.active++
	p.picks++
	chosen.used = p.picks
	if session != "" {
		p.sessions[session] = binding{id: chosen.id, expires: now.Add(sessionTTL)}
	}
	model, _ := chosen.model(id)
	return &Lease{ID: chosen.id, Model: model, Credits: chosen.credits, pool: p, entry: chosen}, nil
}

// sticky 返回会话粘连且仍有空闲槽位的候选账户
func (p *Pool) sticky(session string, candidates []*entry, now time.Time) *entry {
	bound, ok := p.sessions[session]
	if session == "" || !ok || !now.Before(bound.expires) {
		return nil
	}
	for _, e := range candidates {
		if e.id == bound.id && e.active < p.concurrency {
			return e
		}
	}
	return nil
}

// best 在有空闲槽位的候选中选账户：先取优先级最高的一组，lowerTier 时再取其中套餐最低的一组；fillFirst 时取账户顺序中的第一个，否则取进行中请求最少者，并列时取最久未被选中者
func (p *Pool) best(candidates []*entry, lowerTier bool) *entry {
	var free []*entry
	for _, e := range p.entries {
		if slices.Contains(candidates, e) && e.active < p.concurrency {
			free = append(free, e)
		}
	}
	if len(free) == 0 {
		return nil
	}
	top := slices.MaxFunc(free, func(a, b *entry) int { return a.priority - b.priority }).priority
	free = slices.DeleteFunc(free, func(e *entry) bool { return e.priority != top })
	if lowerTier {
		low := slices.MinFunc(free, func(a, b *entry) int { return a.tier - b.tier }).tier
		free = slices.DeleteFunc(free, func(e *entry) bool { return e.tier != low })
	}
	if p.fillFirst {
		return free[0]
	}
	return slices.MinFunc(free, func(a, b *entry) int {
		if a.active != b.active {
			return a.active - b.active
		}
		return cmp.Compare(a.used, b.used)
	})
}

// sweep 每分钟清理一次过期的会话粘连
func (p *Pool) sweep(now time.Time) {
	if now.Sub(p.sweptAt) < time.Minute {
		return
	}
	for session, bound := range p.sessions {
		if !now.Before(bound.expires) {
			delete(p.sessions, session)
		}
	}
	p.sweptAt = now
}

// entry 返回指定账户
func (p *Pool) entry(id string) *entry {
	for _, e := range p.entries {
		if e.id == id {
			return e
		}
	}
	return nil
}

// notify 唤醒等待槽位的请求
func (p *Pool) notify() {
	close(p.changed)
	p.changed = make(chan struct{})
}

// model 返回账户目录中的模型
func (e *entry) model(id string) (antigravity.CatalogModel, bool) {
	for _, model := range e.models {
		if model.ID == id {
			return model, true
		}
	}
	return antigravity.CatalogModel{}, false
}

// until 返回模型的不可用截止时间，取额度组冷却、模型冷却与额度耗尽的重置时间中最晚者
func (e *entry) until(model antigravity.CatalogModel, reserve float64) time.Time {
	until := e.exhausted(model, reserve)
	for _, cooldown := range []time.Time{e.cooldown[model.QuotaGroup()], e.cooldown[model.ID]} {
		if cooldown.After(until) {
			until = cooldown
		}
	}
	return until
}

// exhausted 返回该组剩余比例不高于 reserve 的摘要桶与模型 quotaInfo 的重置时间中最晚者；开启 AI 积分且 reserve 为 0 的账户额度用尽后以积分生成，不按额度判定耗尽
func (e *entry) exhausted(model antigravity.CatalogModel, reserve float64) time.Time {
	var until time.Time
	if e.credits && reserve == 0 {
		return until
	}
	resets := []antigravity.QuotaInfo{model.QuotaInfo}
	for _, bucket := range e.buckets(model.QuotaGroup()) {
		resets = append(resets, antigravity.QuotaInfo{RemainingFraction: bucket.RemainingFraction, ResetTime: bucket.ResetTime})
	}
	for _, info := range resets {
		if reset, err := time.Parse(time.RFC3339, info.ResetTime); err == nil && info.RemainingFraction <= reserve && reset.After(until) {
			until = reset
		}
	}
	return until
}

// buckets 返回额度摘要中属于该组的桶，组名取 bucketId 的前缀
func (e *entry) buckets(group string) []antigravity.QuotaBucket {
	var buckets []antigravity.QuotaBucket
	for _, quotaGroup := range e.quota.Groups {
		for _, bucket := range quotaGroup.Buckets {
			if prefix, _, _ := strings.Cut(bucket.BucketID, "-"); prefix == group {
				buckets = append(buckets, bucket)
			}
		}
	}
	return buckets
}
