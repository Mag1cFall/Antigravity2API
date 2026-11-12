package service

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/auth"
	"github.com/Mag1cFall/Antigravity2API/internal/balancer"
	"github.com/Mag1cFall/Antigravity2API/internal/config"
	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
	"github.com/Mag1cFall/Antigravity2API/internal/proxypool"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

var _ antigravity.Service = (*Service)(nil)

// Options 表示服务的依赖：Live 为实时配置，每次请求读取；Ledger 为请求账本，记录额度快照
type Options struct {
	Live   *atomic.Pointer[config.Config]
	Ledger *requestdb.Store
}

// AccountStatus 表示管理端展示的账户状态，Cooldown 为各额度组或模型的不可用截止时间，Quota 为两组额度桶
type AccountStatus struct {
	auth.Account
	Ready    bool                     `json:"ready"`
	Error    string                   `json:"error,omitempty"`
	Active   int                      `json:"active"`
	Cooldown map[string]time.Time     `json:"cooldown,omitempty"`
	Quota    []antigravity.QuotaGroup `json:"quota,omitempty"`
	// QuotaAt 是 Quota 的读取时间
	QuotaAt time.Time `json:"quota_at,omitzero"`
	// Shared 按额度组列出与该账户共享额度池的其他就绪账户，家庭方案的成员共享额度池并一起冷却
	Shared map[string][]string `json:"shared,omitempty"`
}

// quotaRead 是账户最近一次读到的额度摘要及读取时间
type quotaRead struct {
	summary antigravity.QuotaSummary
	at      time.Time
}

// Service 在账户池上实现 antigravity.Service
type Service struct {
	store         *auth.Store
	live          *atomic.Pointer[config.Config]
	ledger        *requestdb.Store
	pool          *balancer.Pool
	conversations *conversations
	mu            sync.Mutex
	accounts      map[string]*account
	failures      map[string]string
	// quotas 是各账户最近一次读到的额度摘要，账户移出调度与重新初始化后保留
	quotas map[string]quotaRead
	// catalogs 是各账户最近一次读到的公开目录，保存在 RUNTIME_DIR/catalogs，账户初始化完成前用于公开目录
	catalogs map[string][]antigravity.CatalogModel
	prompt   composerCache
	// proxies 是未设账户代理的账户共用的 PROXY 代理池，proxyCheck 请求立即检测代理池
	proxies    *proxypool.Pool
	proxyCheck chan struct{}
	// launched 在服务启动后第一个账户就绪或首次刷新结束时关闭
	launched   chan struct{}
	launchOnce sync.Once
	// warmed 是各账户各周额度桶最近一次唤醒的时间，保存在 RUNTIME_DIR/warmups.json
	warmed map[string]map[string]time.Time
	// saving 串行化冷却的读取与写入
	saving sync.Mutex
	// firstEvents 是各模型近期原请求的首字等待，启动时从请求账本补入
	firstEvents *firstEventStats
	// hedges 是正在进行的备用请求数
	hedges atomic.Int32
}

// composerCache 表示按档位与自定义档模板缓存的 Composer，key 由档位、system 模板与 user 模板组成
type composerCache struct {
	key      string
	composer *prompt.Composer
}

// account 表示一个已初始化账户的上游客户端、设备档案、账户自设代理与初始化时的 refresh token；quoted 表示初始化后首次成功生成后的额度摘要已读取，internal 为连续 INTERNAL 500 次数
type account struct {
	id       string
	proxy    string
	refresh  string
	client   *antigravity.Client
	source   *antigravity.TokenSource
	device   antigravity.Device
	cancel   context.CancelFunc
	quoted   bool
	internal int
}

// New 创建服务，读取 RUNTIME_DIR 下的会话记录与各账户上一次的目录，从请求账本补入近期首字样本，账户在 Refresh 完成初始化后接收请求
func New(store *auth.Store, options Options) (*Service, error) {
	cfg := options.Live.Load()
	conversations, err := openConversations(filepath.Join(cfg.RuntimeDir, "conversations"))
	if err != nil {
		return nil, err
	}
	catalogs, err := loadCatalogs(filepath.Join(cfg.RuntimeDir, "catalogs"))
	if err != nil {
		return nil, err
	}
	pool := balancer.New(cfg.AccountConcurrency)
	if data, err := os.ReadFile(filepath.Join(cfg.RuntimeDir, cooldownsFile)); err == nil {
		var saved map[string]map[string]time.Time
		if json.Unmarshal(data, &saved) == nil {
			pool.RestoreCooldowns(saved)
		}
	}
	service := &Service{
		store:         store,
		live:          options.Live,
		ledger:        options.Ledger,
		pool:          pool,
		conversations: conversations,
		accounts:      make(map[string]*account),
		failures:      make(map[string]string),
		quotas:        make(map[string]quotaRead),
		catalogs:      catalogs,
		proxies:       proxypool.New(cfg.Proxies()),
		proxyCheck:    make(chan struct{}, 1),
		warmed:        loadWarmups(filepath.Join(cfg.RuntimeDir, warmupsFile)),
		launched:      make(chan struct{}),
		firstEvents:   &firstEventStats{samples: make(map[string][]firstEventSample)},
	}
	if err := service.seedFirstEvents(); err != nil {
		return nil, err
	}
	return service, nil
}

// markLaunched 结束启动期间的等待
func (s *Service) markLaunched() {
	s.launchOnce.Do(func() { close(s.launched) })
}

// WaitLaunch 在服务启动后还没有就绪账户且首次刷新未结束时等待，ctx 结束时返回
func (s *Service) WaitLaunch(ctx context.Context) {
	select {
	case <-s.launched:
	case <-ctx.Done():
	}
}

// savedModel 是 RUNTIME_DIR/catalogs 中的目录条目，模型 ID、选择器标记与用途和上游字段并列保存
type savedModel struct {
	ID    string `json:"id"`
	Agent bool   `json:"agent,omitempty"`
	Kind  string `json:"kind,omitempty"`
	antigravity.CatalogModel
}

// loadCatalogs 读取目录下各账户的 <ID>.json 公开目录，目录不存在时为空
func loadCatalogs(dir string) (map[string][]antigravity.CatalogModel, error) {
	catalogs := make(map[string][]antigravity.CatalogModel)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return catalogs, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var saved []savedModel
		if json.Unmarshal(data, &saved) == nil {
			models := make([]antigravity.CatalogModel, len(saved))
			for i, model := range saved {
				models[i] = model.CatalogModel
				models[i].ID, models[i].Agent, models[i].Kind = model.ID, model.Agent, model.Kind
			}
			catalogs[id] = models
		}
	}
	return catalogs, nil
}

// setModels 记下账户的公开目录并保存到 RUNTIME_DIR/catalogs，调用方持有 s.mu
func (s *Service) setModels(id string, models []antigravity.CatalogModel) {
	s.catalogs[id] = models
	dir := filepath.Join(s.live.Load().RuntimeDir, "catalogs")
	saved := make([]savedModel, len(models))
	for i, model := range models {
		saved[i] = savedModel{ID: model.ID, Agent: model.Agent, Kind: model.Kind, CatalogModel: model}
	}
	data, _ := json.Marshal(saved)
	err := os.MkdirAll(dir, 0o700)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, id+".json"), data, 0o600)
	}
	if err != nil {
		slog.Warn("保存账户目录失败 | 错误="+err.Error(), "source", id)
	}
}

// latestQuota 把账本中同一次读取的额度桶还原为额度摘要，按额度组分组，取最近一次读取
func latestQuota(snapshots []requestdb.QuotaSnapshot) (quotaRead, bool) {
	if len(snapshots) == 0 {
		return quotaRead{}, false
	}
	at := snapshots[len(snapshots)-1].Time
	read := quotaRead{at: at}
	groups := map[string]int{}
	for _, snapshot := range snapshots {
		if !snapshot.Time.Equal(at) {
			continue
		}
		bucket := antigravity.QuotaBucket{BucketID: snapshot.BucketID, Window: snapshot.Window, RemainingFraction: snapshot.RemainingFraction}
		if snapshot.ResetTime != nil {
			bucket.ResetTime = snapshot.ResetTime.Format(time.RFC3339)
		}
		group := antigravity.BucketGroup(snapshot.BucketID)
		index, ok := groups[group]
		if !ok {
			index = len(read.summary.Groups)
			groups[group] = index
			read.summary.Groups = append(read.summary.Groups, antigravity.QuotaGroup{})
		}
		read.summary.Groups[index].Buckets = append(read.summary.Groups[index].Buckets, bucket)
	}
	return read, true
}

// Models 按账户顺序合并各就绪账户的目录，再并入尚未就绪的已启用账户上一次的目录并去重，按 public 合并 Agent 模型的档位，随后列出 MODEL_MAPPING 的别名；合并结果为空且有尚未就绪的已启用账户时返回 503 与各账户的原因
func (s *Service) Models(context.Context) ([]antigravity.Model, error) {
	catalog := s.pool.Models()
	seen := make(map[string]bool, len(catalog))
	for _, model := range catalog {
		seen[model.ID] = true
	}
	records := s.store.Accounts()
	var waiting []string
	s.mu.Lock()
	for _, record := range records {
		if _, ready := s.pool.Status(record.ID); ready || !record.Enabled {
			continue
		}
		waiting = append(waiting, record.ID+": "+cmp.Or(s.failures[record.ID], "初始化中"))
		for _, model := range s.catalogs[record.ID] {
			if !seen[model.ID] {
				seen[model.ID] = true
				catalog = append(catalog, model)
			}
		}
	}
	s.mu.Unlock()
	if len(catalog) == 0 && len(waiting) > 0 {
		return nil, &Error{Status: http.StatusServiceUnavailable, Message: "账户尚未就绪，还没有模型目录\n" + strings.Join(waiting, "\n")}
	}
	return s.aliases(public(catalog)), nil
}

// Accounts 返回账户库中全部账户及其就绪、错误、并发、冷却与额度，冷却按实时配置的额度保留比例计算
func (s *Service) Accounts() []AccountStatus {
	s.configure(s.live.Load())
	records := s.store.Accounts()
	s.mu.Lock()
	failures := maps.Clone(s.failures)
	quotas := maps.Clone(s.quotas)
	catalogs := maps.Clone(s.catalogs)
	s.mu.Unlock()
	statuses := make([]AccountStatus, 0, len(records))
	for _, record := range records {
		status := AccountStatus{Account: *record, Error: failures[record.ID], Quota: renewed(usableQuota(quotas[record.ID].summary.Groups, catalogs[record.ID]), time.Now()), QuotaAt: quotas[record.ID].at}
		if state, ok := s.pool.Status(record.ID); ok {
			status.Ready, status.Active = true, state.Active
			if len(state.Cooldown) != 0 {
				status.Cooldown = state.Cooldown
			}
			for _, group := range []string{antigravity.QuotaGroupGemini, antigravity.QuotaGroupArgon, antigravity.QuotaGroupThirdParty} {
				if peers := s.pool.Peers(record.ID, group); len(peers) > 0 {
					if status.Shared == nil {
						status.Shared = make(map[string][]string)
					}
					status.Shared[group] = peers
				}
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// renewed 返回额度组的副本，重置时刻已过的桶剩余比例为 1 且不带重置时刻，与调度把该桶视为可用一致
func renewed(groups []antigravity.QuotaGroup, now time.Time) []antigravity.QuotaGroup {
	result := make([]antigravity.QuotaGroup, len(groups))
	for index, group := range groups {
		group.Buckets = slices.Clone(group.Buckets)
		for bucket := range group.Buckets {
			if reset, err := time.Parse(time.RFC3339, group.Buckets[bucket].ResetTime); err == nil && !now.Before(reset) {
				group.Buckets[bucket].RemainingFraction, group.Buckets[bucket].ResetTime = 1, ""
			}
		}
		result[index] = group
	}
	return result
}

// usableQuota 返回额度摘要中账户目录有对应模型的额度桶，没有目录时原样返回；失去第三方模型权限的账户上游仍返回 3p 桶
func usableQuota(groups []antigravity.QuotaGroup, catalog []antigravity.CatalogModel) []antigravity.QuotaGroup {
	if len(catalog) == 0 {
		return groups
	}
	var usable []antigravity.QuotaGroup
	for _, group := range groups {
		buckets := slices.DeleteFunc(slices.Clone(group.Buckets), func(bucket antigravity.QuotaBucket) bool {
			return !slices.ContainsFunc(catalog, func(model antigravity.CatalogModel) bool {
				return model.QuotaGroup() == antigravity.BucketGroup(bucket.BucketID)
			})
		})
		if len(buckets) > 0 {
			group.Buckets = buckets
			usable = append(usable, group)
		}
	}
	return usable
}

// pace 等待相邻两个账户的初始化之间的随机 1 到 3 秒间隔，ctx 结束时返回其错误
func pace(ctx context.Context) error {
	select {
	case <-time.After(time.Second + rand.N(2*time.Second)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Refresh 为还没有额度读取的账户取账本中 7 天内最近一次额度摘要，结束时结束启动期间的等待，初始化尚未就绪、账户代理已变或重新登录过的启用账户：相邻两个账户的初始化相隔 pace 开始、彼此并发，全部结束后返回；停用或已删除的账户移出调度，已删除账户的目录与 Hub 状态文件随之删除
func (s *Service) Refresh(ctx context.Context) error {
	defer s.markLaunched()
	records := s.store.Accounts()
	for _, record := range records {
		s.mu.Lock()
		_, read := s.quotas[record.ID]
		s.mu.Unlock()
		if read {
			continue
		}
		now := time.Now()
		snapshots, err := s.ledger.QuotaSnapshots(ctx, now.AddDate(0, 0, -7), now, record.ID)
		if err != nil {
			return err
		}
		if latest, ok := latestQuota(snapshots); ok {
			s.mu.Lock()
			if _, read := s.quotas[record.ID]; !read {
				s.quotas[record.ID] = latest
			}
			s.mu.Unlock()
		}
	}
	known, enabled := make(map[string]bool), make(map[string]bool)
	var errs []error
	var started sync.WaitGroup
	var collect sync.Mutex
	waited := false
	for _, record := range records {
		known[record.ID] = true
		token := s.store.Token(record.ID)
		if !record.Enabled || token == nil {
			continue
		}
		enabled[record.ID] = true
		s.pool.Profile(record.ID, record.Priority, record.Rank())
		s.pool.Credits(record.ID, record.AICredits)
		if current := s.account(record.ID); current != nil && current.proxy == record.Proxy && current.refresh == token.RefreshToken {
			continue
		}
		if waited {
			if err := pace(ctx); err != nil {
				started.Wait()
				return errors.Join(append(errs, err)...)
			}
		}
		waited = true
		started.Go(func() {
			err := s.initialize(ctx, record)
			collect.Lock()
			errs = append(errs, err)
			collect.Unlock()
		})
	}
	started.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.accounts {
		if !enabled[id] {
			s.remove(id)
			s.proxies.Release(id)
		}
	}
	for id := range s.failures {
		if !known[id] {
			delete(s.failures, id)
		}
	}
	for id := range s.quotas {
		if !known[id] {
			delete(s.quotas, id)
		}
	}
	s.forget()
	return errors.Join(errs...)
}

// forget 删除不在账户库中的账户的目录缓存及其留在 RUNTIME_DIR/catalogs 与 hub 的文件，调用方持有 s.mu
func (s *Service) forget() {
	for _, name := range []string{"catalogs", "hub"} {
		dir := filepath.Join(s.live.Load().RuntimeDir, name)
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			id, ok := strings.CutSuffix(entry.Name(), ".json")
			if _, known := s.store.Account(id); !ok || known {
				continue
			}
			delete(s.catalogs, id)
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				slog.Warn("删除已移除账户的运行数据失败 | 错误="+err.Error(), "source", id)
			}
		}
	}
}

// Verify 重新初始化一个启用账户，需要验证或初始化失败的账户由此恢复调度
func (s *Service) Verify(ctx context.Context, id string) error {
	if record, ok := s.store.Account(id); ok && record.Enabled {
		return s.initialize(ctx, &record)
	}
	return fmt.Errorf("账户 %s 不存在或已停用", id)
}

// RetryFailed 依次重新初始化启用中但初始化失败、认证失败或需要验证的账户，相邻两次之间相隔 pace，返回重试的账户数与仍失败的账户数
func (s *Service) RetryFailed(ctx context.Context) (int, int) {
	s.mu.Lock()
	var failed []auth.Account
	for _, record := range s.store.Accounts() {
		if _, ok := s.failures[record.ID]; ok && record.Enabled {
			failed = append(failed, *record)
		}
	}
	s.mu.Unlock()
	remaining := 0
	for index, record := range failed {
		if index > 0 && pace(ctx) != nil {
			return index, remaining
		}
		if s.initialize(ctx, &record) != nil {
			remaining++
		}
	}
	return len(failed), remaining
}

// proxy 返回账户的代理选择函数：账户设了代理时用它，direct 直连，否则每次请求取代理池为账户绑定的代理，代理池为空时用系统代理
func (s *Service) proxy(record *auth.Account) antigravity.Proxy {
	switch record.Proxy {
	case "":
		id := record.ID
		return func(request *http.Request) (*url.URL, error) {
			if proxy := s.proxies.Assign(id); proxy != "" {
				return url.Parse(proxy)
			}
			return s.live.Load().ProxyURL(request)
		}
	case proxypool.Direct:
		s.proxies.Release(record.ID)
		return func(*http.Request) (*url.URL, error) { return nil, nil }
	}
	s.proxies.Release(record.ID)
	proxy, err := url.Parse(record.Proxy)
	return func(*http.Request) (*url.URL, error) { return proxy, err }
}

// Import 把账户 JSON 中的一个账户写入账户库，换取 token 与读取邮箱经该账户的代理发出：导入项带代理时用它，否则用代理池按项中邮箱绑定的代理；项中邮箱不是账户库中的账户时解除它的绑定
func (s *Service) Import(ctx context.Context, item auth.Transfer) (*auth.Account, error) {
	if err := config.ValidateProxy(item.Proxy); err != nil && item.Proxy != proxypool.Direct {
		return nil, err
	}
	account, err := s.store.Import(ctx, item, s.proxy(&auth.Account{ID: item.Email, Proxy: item.Proxy}))
	if _, known := s.store.Account(item.Email); !known {
		s.proxies.Release(item.Email)
	}
	return account, err
}

// assisted 返回按 loadCodeAssist 响应更新项目、当前套餐、付费套餐与 Google One AI 积分余额后的账户
func assisted(record auth.Account, assist antigravity.LoadCodeAssistResponse) auth.Account {
	record.Project, record.Tier, record.PaidTier = assist.CloudAICompanionProject, assist.CurrentTier.ID, ""
	record.CreditBalance, record.CreditMinimum = "", ""
	if assist.PaidTier != nil {
		record.PaidTier = assist.PaidTier.ID
		for _, credit := range assist.PaidTier.AvailableCredits {
			if credit.CreditType == antigravity.CreditGoogleOneAI {
				record.CreditBalance, record.CreditMinimum = cmp.Or(credit.CreditAmount, "0"), credit.MinimumCreditAmountForUsage
			}
		}
	}
	return record
}

// tokenSource 返回账户保存的 token 的来源，刷新后写回账户库
func (s *Service) tokenSource(record *auth.Account, token *oauth2.Token, proxy antigravity.Proxy) *antigravity.TokenSource {
	return antigravity.NewTokenSource(token, record.AuthMethod, record.WIFProvider, proxy, func(token *oauth2.Token) error {
		slog.Info(fmt.Sprintf("token 已刷新 | 到期=%s", token.Expiry.Format(time.RFC3339)), "source", record.ID)
		return s.store.UpdateToken(record.ID, token)
	})
}

// initialize 以账户的设备档案与代理开始虚拟客户端，项目为空或 refresh token 与调度中的客户端不同时按登录序列、否则按冷启动序列初始化，读到的套餐、项目、地区、目录与额度经 observe 写回（冷启动不读额度，沿用最近一次读取），loadCodeAssist 给出不能使用的套餐且没有项目时按初始化失败处理、原因为 reasonMessage 与套餐原文，成功且账户仍在账户库中启用时加入调度并开始伴随请求；失败时停止伴随请求并把账户移出调度，需要验证时原因带上游错误正文中的验证链接；账户已从账户库删除时不初始化
func (s *Service) initialize(ctx context.Context, record *auth.Account) error {
	token := s.store.Token(record.ID)
	if token == nil {
		return nil
	}
	proxy := s.proxy(record)
	source := s.tokenSource(record, token, proxy)
	client := antigravity.NewClient(record.ID, source, record.Device, proxy)
	client.Observe(s.observe(record.ID, client))
	path := s.hubPath(record.ID)
	client.SetHub(loadHub(path), saveHub(path))
	watch, cancel := context.WithCancel(context.Background())
	running := s.account(record.ID)
	login := record.Project == "" || running != nil && running.refresh != token.RefreshToken
	companion, initialization, err := boot(ctx, watch, client, record.ID, login, record.AuthMethod == antigravity.AuthMethodEnterprise)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		cancel()
		slog.ErrorContext(ctx, "账户初始化失败 | 错误="+err.Error(), "source", record.ID)
		reason := err.Error()
		if upstream, ok := errors.AsType[*antigravity.UpstreamError](err); ok && upstream.Reason() == "VALIDATION_REQUIRED" {
			reason = "需要验证：" + string(upstream.Body)
		}
		s.stop(record.ID, reason)
		return fmt.Errorf("%s: %w", record.ID, err)
	}
	if assist := initialization.Assist; assist.CloudAICompanionProject == "" && len(assist.IneligibleTiers) > 0 {
		cancel()
		messages := make([]string, len(assist.IneligibleTiers))
		for index, tier := range assist.IneligibleTiers {
			messages[index] = tier.ReasonMessage
		}
		detail, _ := json.Marshal(assist.IneligibleTiers)
		slog.ErrorContext(ctx, "账户不能使用套餐 | 原因="+strings.Join(messages, "；"), "source", record.ID)
		s.stop(record.ID, "不能使用套餐："+strings.Join(messages, "；")+" "+string(detail))
		return fmt.Errorf("%s: %s", record.ID, strings.Join(messages, "；"))
	}
	if !s.store.Enabled(record.ID) {
		cancel()
		return nil
	}
	s.remove(record.ID)
	current := &account{id: record.ID, proxy: record.Proxy, refresh: token.RefreshToken, client: client, source: source, device: record.Device, cancel: cancel}
	s.accounts[record.ID] = current
	delete(s.failures, record.ID)
	s.pool.Set(record.ID, s.catalogs[record.ID])
	s.pool.Profile(record.ID, record.Priority, assisted(*record, initialization.Assist).Rank())
	s.pool.Credits(record.ID, record.AICredits)
	s.pool.SetQuota(record.ID, s.quotas[record.ID].summary)
	s.markLaunched()
	go companion.Run(watch)
	return nil
}

// observe 返回账户客户端的读取回调，是上游账户状态唯一的写回点：套餐、项目、AI 积分与地区写回仍在账户库中的账户并更新调度档案，目录与额度摘要记下并更新账户池，目录只在账户由该客户端调度时进入账户池；账户已删除时不写回
func (s *Service) observe(id string, client *antigravity.Client) func(antigravity.Observed) {
	return func(observed antigravity.Observed) {
		record, ok, err := s.store.Update(id, func(record *auth.Account) {
			if observed.Assist != nil {
				*record = assisted(*record, *observed.Assist)
			}
			if observed.Settings != nil {
				record.Region = observed.Settings.RegionCode
			}
		})
		if err != nil {
			slog.Warn("保存账户失败 | 错误="+err.Error(), "source", id)
		}
		if !ok {
			return
		}
		switch {
		case observed.Assist != nil:
			s.pool.Profile(id, record.Priority, record.Rank())
		case observed.Catalog != nil:
			models := observed.Catalog.PublicModels()
			s.mu.Lock()
			s.setModels(id, models)
			if current := s.accounts[id]; current != nil && current.client == client {
				s.pool.Set(id, models)
			}
			s.mu.Unlock()
		case observed.Quota != nil:
			s.setQuota(id, *observed.Quota)
		}
	}
}

// account 返回已初始化的账户
func (s *Service) account(id string) *account {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accounts[id]
}

// stop 把账户移出调度并记下原因，调用方持有 s.mu
func (s *Service) stop(id, reason string) {
	s.remove(id)
	s.failures[id] = reason
}

// remove 停止账户的周期请求并移出调度，调用方持有 s.mu
func (s *Service) remove(id string) {
	if current := s.accounts[id]; current != nil {
		current.cancel()
		delete(s.accounts, id)
	}
	s.pool.Remove(id)
}

// quote 在账户初始化后首次成功生成后读取一次额度摘要，并清零连续 INTERNAL 500 次数
func (s *Service) quote(ctx context.Context, current *account) {
	s.mu.Lock()
	quoted := current.quoted
	current.quoted, current.internal = true, 0
	s.mu.Unlock()
	if quoted {
		return
	}
	if _, err := current.client.RetrieveUserQuotaSummary(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "读取额度摘要失败 | 错误="+err.Error(), "source", current.id)
	}
}

// setQuota 记下账户的额度摘要及读取时间，更新账户池并写入额度快照
func (s *Service) setQuota(id string, summary antigravity.QuotaSummary) {
	s.mu.Lock()
	s.quotas[id] = quotaRead{summary: summary, at: time.Now()}
	s.mu.Unlock()
	s.pool.SetQuota(id, summary)
	s.ledger.RecordQuota(id, summary.Groups)
}

// quotaWorkers 是同时读取额度摘要的账户数上限
const quotaWorkers = 5

// RefreshQuota 实时读取一个账户的套餐与额度摘要：已初始化的账户经自己的客户端读取，其余账户（含停用）以保存的 token、设备档案与代理读取；先读套餐与项目（已初始化的账户从当前 cloudcode 端点，未初始化的企业 WIF 账户按 BusinessAssist，其余为 daily loadCodeAssist），读到的套餐与额度经 observe 写回
func (s *Service) RefreshQuota(ctx context.Context, id string) error {
	record, ok := s.store.Account(id)
	if !ok {
		return fmt.Errorf("账户 %s 不存在", id)
	}
	var client *antigravity.Client
	var err error
	if current := s.account(id); current != nil {
		client = current.client
		_, err = client.Assist(ctx)
	} else {
		proxy := s.proxy(&record)
		client = antigravity.NewClient(id, s.tokenSource(&record, s.store.Token(id), proxy), record.Device, proxy)
		client.Observe(s.observe(id, client))
		if record.AuthMethod == antigravity.AuthMethodEnterprise {
			_, err = client.BusinessAssist(ctx)
		} else {
			_, err = client.LoadCodeAssist(ctx, antigravity.EndpointDaily)
		}
	}
	if err != nil {
		return err
	}
	_, err = client.RetrieveUserQuotaSummary(ctx)
	return err
}

// RefreshQuotas 读取全部账户的额度摘要，同时最多 quotaWorkers 个，返回读取失败的账户及原因
func (s *Service) RefreshQuotas(ctx context.Context) map[string]error {
	var (
		mu      sync.Mutex
		workers sync.WaitGroup
		failed  = map[string]error{}
		slots   = make(chan struct{}, quotaWorkers)
	)
	for _, record := range s.store.Accounts() {
		slots <- struct{}{}
		workers.Go(func() {
			defer func() { <-slots }()
			if err := s.RefreshQuota(ctx, record.ID); err != nil {
				slog.WarnContext(ctx, "读取额度摘要失败 | 错误="+err.Error(), "source", record.ID)
				mu.Lock()
				failed[record.ID] = err
				mu.Unlock()
			}
		})
	}
	workers.Wait()
	return failed
}

// configure 把实时配置的单账户并发上限、调度策略与额度保留比例交给账户池
func (s *Service) configure(cfg *config.Config) {
	s.pool.Configure(cfg.AccountConcurrency, cfg.RoutingStrategy == "fill-first")
	s.pool.Reserve(float64(cfg.QuotaReservePercent) / 100)
}

// composer 返回实时配置档位对应的 Composer，档位或自定义档的模板变化时重建
func (s *Service) composer(cfg *config.Config) (*prompt.Composer, error) {
	tier := prompt.Tier(cfg.SystemPrompt)
	custom := prompt.Custom{System: cfg.SystemPromptTemplate, User: cfg.SystemPromptUserTemplate}
	key := string(tier) + "\x00" + custom.System + "\x00" + custom.User
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prompt.composer != nil && s.prompt.key == key {
		return s.prompt.composer, nil
	}
	composer, err := prompt.New(tier, custom)
	if err != nil {
		return nil, err
	}
	s.prompt = composerCache{key: key, composer: composer}
	return composer, nil
}

// cooldownsFile 是 RUNTIME_DIR 下保存各账户冷却的文件，服务重启后恢复
const cooldownsFile = "cooldowns.json"

// ClearCooldown 清除账户在一个额度组或模型上的冷却并保存，下一次请求即可调度该账户，返回清除前是否有该项冷却
func (s *Service) ClearCooldown(ctx context.Context, id, scope string) bool {
	if !s.pool.Clear(id, scope) {
		return false
	}
	s.saveCooldowns()
	slog.InfoContext(ctx, "已清除冷却 | 范围="+scope, "source", id)
	return true
}

// saveCooldowns 把各账户尚未到期的冷却写入 RUNTIME_DIR/cooldowns.json，同时只有一次读取与写入
func (s *Service) saveCooldowns() {
	s.saving.Lock()
	defer s.saving.Unlock()
	data, err := json.Marshal(s.pool.Cooldowns())
	if err == nil {
		err = os.WriteFile(filepath.Join(s.live.Load().RuntimeDir, cooldownsFile), data, 0o600)
	}
	if err != nil {
		slog.Warn("保存冷却失败 | 错误="+err.Error(), "source", "service")
	}
}
