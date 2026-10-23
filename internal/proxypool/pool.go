package proxypool

import (
	"context"
	"net/url"
	"sort"
	"sync"
	"time"
)

// Direct 是账户代理中表示直连的值
const Direct = "direct"

// checkTimeout 是检测单个代理的最长时间
const checkTimeout = 20 * time.Second

// Pool 是未设置账户代理的账户共用的代理池：每个账户绑定一个代理，代理不可用时改绑到可用代理中账户最少的一个，代理列表变化时在可用代理间均衡
type Pool struct {
	mu       sync.Mutex
	proxies  []*proxyState
	bindings map[string]*proxyState
}

// proxyState 是代理池中一个代理的检测结果
type proxyState struct {
	url     string
	healthy bool
	checked time.Time
	latency time.Duration
	err     string
}

// Status 是代理池中一个代理的当前状态与绑定的账户
type Status struct {
	URL       string    `json:"url"`
	Healthy   bool      `json:"healthy"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	LatencyMS int64     `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
	Accounts  []string  `json:"accounts"`
}

// New 创建代理池，代理在首次检测前视为可用
func New(urls []string) *Pool {
	pool := &Pool{bindings: make(map[string]*proxyState)}
	pool.SetProxies(urls)
	return pool
}

// SetProxies 替换代理列表，仍在列表中的代理保留检测结果与绑定，已删除代理上的账户改绑到账户最少的可用代理，再以最少的改绑使可用代理绑定的账户数相差不超过一个
func (p *Pool) SetProxies(urls []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	previous := make(map[string]*proxyState, len(p.proxies))
	for _, state := range p.proxies {
		previous[state.url] = state
	}
	p.proxies = nil
	for _, value := range urls {
		state := previous[value]
		if state == nil {
			state = &proxyState{url: value, healthy: true}
		}
		if !p.contains(state) {
			p.proxies = append(p.proxies, state)
		}
	}
	orphans := []string{}
	for id, state := range p.bindings {
		if !p.contains(state) {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	for _, id := range orphans {
		if len(p.proxies) == 0 {
			delete(p.bindings, id)
			continue
		}
		p.bindings[id] = p.leastBound(id)
	}
	p.rebalance()
}

// rebalance 每次把账户最多的可用代理上 ID 最大的账户改绑到账户最少的可用代理，直到两者相差不超过一个
func (p *Pool) rebalance() {
	anyHealthy := p.anyHealthy()
	accounts := make(map[*proxyState][]string, len(p.proxies))
	for _, state := range p.proxies {
		if !anyHealthy || state.healthy {
			accounts[state] = []string{}
		}
	}
	for id, state := range p.bindings {
		if ids, ok := accounts[state]; ok {
			accounts[state] = append(ids, id)
		}
	}
	for {
		var most, least *proxyState
		for _, state := range p.proxies {
			ids, ok := accounts[state]
			if !ok {
				continue
			}
			if most == nil || len(ids) > len(accounts[most]) {
				most = state
			}
			if least == nil || len(ids) < len(accounts[least]) {
				least = state
			}
		}
		if most == nil || len(accounts[most])-len(accounts[least]) <= 1 {
			return
		}
		ids := accounts[most]
		sort.Strings(ids)
		id := ids[len(ids)-1]
		accounts[most], accounts[least] = ids[:len(ids)-1], append(accounts[least], id)
		p.bindings[id] = least
	}
}

// contains 报告代理是否仍在列表中
func (p *Pool) contains(target *proxyState) bool {
	for _, state := range p.proxies {
		if state == target {
			return true
		}
	}
	return false
}

// Assign 返回账户使用的代理：已绑定的代理可用时沿用，否则绑定可用代理中账户最少的一个；全部不可用时保持原绑定，代理池为空时返回空串
func (p *Pool) Assign(accountID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.proxies) == 0 {
		delete(p.bindings, accountID)
		return ""
	}
	if current := p.bindings[accountID]; current != nil && (current.healthy || !p.anyHealthy()) {
		return current.url
	}
	best := p.leastBound(accountID)
	p.bindings[accountID] = best
	return best.url
}

// anyHealthy 报告是否有可用代理
func (p *Pool) anyHealthy() bool {
	for _, state := range p.proxies {
		if state.healthy {
			return true
		}
	}
	return false
}

// leastBound 返回除 accountID 外绑定账户最少的代理，有可用代理时只在可用代理中选，同数时取列表中靠前的一个
func (p *Pool) leastBound(accountID string) *proxyState {
	counts := make(map[*proxyState]int, len(p.proxies))
	for id, state := range p.bindings {
		if id != accountID {
			counts[state]++
		}
	}
	anyHealthy := p.anyHealthy()
	var best *proxyState
	for _, state := range p.proxies {
		if anyHealthy && !state.healthy {
			continue
		}
		if best == nil || counts[state] < counts[best] {
			best = state
		}
	}
	return best
}

// Release 解除账户与代理的绑定
func (p *Pool) Release(accountID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.bindings, accountID)
}

// Check 以 probe 并行检测全部代理并更新状态
func (p *Pool) Check(ctx context.Context, probe func(context.Context, string) (time.Duration, error)) {
	p.mu.Lock()
	targets := append([]*proxyState(nil), p.proxies...)
	p.mu.Unlock()
	type result struct {
		latency time.Duration
		err     error
	}
	results := make([]result, len(targets))
	var wait sync.WaitGroup
	for index, state := range targets {
		wait.Go(func() {
			checkContext, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			latency, err := probe(checkContext, state.url)
			results[index] = result{latency, err}
		})
	}
	wait.Wait()

	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for index, state := range targets {
		state.checked, state.latency, state.err = now, results[index].latency, ""
		state.healthy = results[index].err == nil
		if !state.healthy {
			state.latency, state.err = 0, results[index].err.Error()
		}
	}
}

// Status 返回代理池中每个代理的状态，代理 URL 隐去密码
func (p *Pool) Status() []Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	accounts := make(map[*proxyState][]string, len(p.proxies))
	for id, state := range p.bindings {
		accounts[state] = append(accounts[state], id)
	}
	result := make([]Status, 0, len(p.proxies))
	for _, state := range p.proxies {
		ids := append([]string{}, accounts[state]...)
		sort.Strings(ids)
		result = append(result, Status{
			URL: Redact(state.url), Healthy: state.healthy, CheckedAt: state.checked,
			LatencyMS: state.latency.Milliseconds(), Error: state.err, Accounts: ids,
		})
	}
	return result
}

// Redact 返回隐去密码的代理 URL
func Redact(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	return parsed.Redacted()
}
