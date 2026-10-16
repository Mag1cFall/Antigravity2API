package service

import (
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
}

// composerCache 表示按档位与自定义档选项缓存的 Composer，key 由档位、段名与包装方式组成
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

// New 创建服务并读取 RUNTIME_DIR 下的会话记录与各账户上一次的目录，账户在 Refresh 完成初始化后接收请求
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
	return &Service{
		store:         store,
		live:          options.Live,
		ledger:        options.Ledger,
		pool:          balancer.New(cfg.AccountConcurrency),
		conversations: conversations,
		accounts:      make(map[string]*account),
		failures:      make(map[string]string),
		quotas:        make(map[string]quotaRead),
		catalogs:      catalogs,
	}, nil
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

// setModels 把账户的公开目录交给账户池并保存到 RUNTIME_DIR/catalogs，调用方持有 s.mu
func (s *Service) setModels(id string, models []antigravity.CatalogModel) {
	s.pool.Set(id, models)
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

// latestQuota 把账本中同一次读取的额度桶还原为额度摘要，按 bucketId 前缀分组，取最近一次读取
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
		prefix, _, _ := strings.Cut(snapshot.BucketID, "-")
		index, ok := groups[prefix]
		if !ok {
			index = len(read.summary.Groups)
			groups[prefix] = index
			read.summary.Groups = append(read.summary.Groups, antigravity.QuotaGroup{})
		}
		read.summary.Groups[index].Buckets = append(read.summary.Groups[index].Buckets, bucket)
	}
	return read, true
}

// Models 按账户顺序合并各就绪账户的目录，再并入尚未就绪的已启用账户上一次的目录并去重，按 public 合并 Agent 模型的档位，随后列出 MODEL_MAPPING 的别名
func (s *Service) Models(context.Context) ([]antigravity.Model, error) {
	catalog := s.pool.Models()
	seen := make(map[string]bool, len(catalog))
	for _, model := range catalog {
		seen[model.ID] = true
	}
	records := s.store.Accounts()
	s.mu.Lock()
	for _, record := range records {
		if _, ready := s.pool.Status(record.ID); ready || !record.Enabled {
			continue
		}
		for _, model := range s.catalogs[record.ID] {
			if !seen[model.ID] {
				seen[model.ID] = true
				catalog = append(catalog, model)
			}
		}
	}
	s.mu.Unlock()
	return s.aliases(public(catalog)), nil
}

// Accounts 返回账户库中全部账户及其就绪、错误、并发、冷却与额度
func (s *Service) Accounts() []AccountStatus {
	records := s.store.Accounts()
	s.mu.Lock()
	failures := maps.Clone(s.failures)
	quotas := maps.Clone(s.quotas)
	s.mu.Unlock()
	statuses := make([]AccountStatus, 0, len(records))
	for _, record := range records {
		status := AccountStatus{Account: *record, Error: failures[record.ID], Quota: quotas[record.ID].summary.Groups, QuotaAt: quotas[record.ID].at}
		if state, ok := s.pool.Status(record.ID); ok {
			status.Ready, status.Active = true, state.Active
			if len(state.Cooldown) != 0 {
				status.Cooldown = state.Cooldown
			}
			for _, group := range []string{antigravity.QuotaGroupGemini, antigravity.QuotaGroupThirdParty} {
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

// Refresh 为还没有额度读取的账户取账本中 7 天内最近一次额度摘要，逐个初始化尚未就绪、账户代理已变或重新登录过的启用账户，相邻两次初始化之间随机间隔 1 到 3 秒；停用或已删除的账户移出调度
func (s *Service) Refresh(ctx context.Context) error {
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
	waited := false
	for _, record := range records {
		known[record.ID] = true
		if !record.Enabled {
			continue
		}
		enabled[record.ID] = true
		s.pool.Profile(record.ID, record.Priority, record.Rank())
		current := s.account(record.ID)
		refresh := s.store.Token(record.ID).RefreshToken
		if current != nil && current.proxy == record.Proxy && current.refresh == refresh {
			continue
		}
		if waited {
			select {
			case <-time.After(time.Second + rand.N(2*time.Second)):
			case <-ctx.Done():
				return errors.Join(append(errs, ctx.Err())...)
			}
		}
		waited = true
		errs = append(errs, s.initialize(ctx, record, record.Project == "" || current != nil && current.refresh != refresh))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.accounts {
		if !enabled[id] {
			s.remove(id)
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
	return errors.Join(errs...)
}

// Verify 重新初始化一个启用账户，需要验证或初始化失败的账户由此恢复调度
func (s *Service) Verify(ctx context.Context, id string) error {
	for _, record := range s.store.Accounts() {
		if record.ID == id && record.Enabled {
			return s.initialize(ctx, record, record.Project == "")
		}
	}
	return fmt.Errorf("账户 %s 不存在或已停用", id)
}

// proxy 返回账户的代理选择函数：账户设了代理时用它，否则每次请求按实时配置的 PROXY 或系统代理
func (s *Service) proxy(record *auth.Account) antigravity.Proxy {
	if record.Proxy != "" {
		proxy, err := url.Parse(record.Proxy)
		return func(*http.Request) (*url.URL, error) { return proxy, err }
	}
	return func(request *http.Request) (*url.URL, error) { return s.live.Load().ProxyURL(request) }
}

// assisted 返回按 loadCodeAssist 响应更新项目、当前套餐与付费套餐后的账户
func assisted(record auth.Account, assist antigravity.LoadCodeAssistResponse) auth.Account {
	record.Project, record.Tier, record.PaidTier = assist.CloudAICompanionProject, assist.CurrentTier.ID, ""
	if assist.PaidTier != nil {
		record.PaidTier = assist.PaidTier.ID
	}
	return record
}

// tokenSource 返回账户保存的 token 的来源，刷新后写回账户库
func (s *Service) tokenSource(record *auth.Account, proxy antigravity.Proxy) *antigravity.TokenSource {
	return antigravity.NewTokenSource(s.store.Token(record.ID), proxy, func(token *oauth2.Token) error {
		slog.Info(fmt.Sprintf("token 已刷新 | 到期=%s", token.Expiry.Format(time.RFC3339)), "source", record.ID)
		return s.store.SaveToken(record.ID, token)
	})
}

// initialize 以账户的设备档案与代理开始虚拟客户端，login 为真时按登录序列、否则按冷启动序列初始化，写回套餐、项目与地区，记下额度快照（冷启动不读额度，沿用最近一次读取）并开始伴随请求；失败时停止伴随请求并把账户移出调度
func (s *Service) initialize(ctx context.Context, record *auth.Account, login bool) error {
	token := s.store.Token(record.ID)
	proxy := s.proxy(record)
	source := s.tokenSource(record, proxy)
	client := antigravity.NewClient(record.ID, source, record.Device, proxy)
	path := s.hubPath(record.ID)
	client.SetHub(loadHub(path), saveHub(path))
	watch, cancel := context.WithCancel(context.Background())
	companion, initialization, err := boot(ctx, watch, client, record.ID, login)
	updated := *record
	if err == nil {
		updated = assisted(*record, initialization.Assist)
		updated.Region = initialization.Settings.RegionCode
		if updated != *record {
			err = s.store.Save(&updated)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		cancel()
		slog.ErrorContext(ctx, "账户初始化失败 | 错误="+err.Error(), "source", record.ID)
		s.stop(record.ID, err.Error())
		return fmt.Errorf("%s: %w", record.ID, err)
	}
	s.remove(record.ID)
	current := &account{id: record.ID, proxy: record.Proxy, refresh: token.RefreshToken, client: client, source: source, device: record.Device, cancel: cancel}
	s.accounts[record.ID] = current
	delete(s.failures, record.ID)
	s.setModels(record.ID, initialization.Catalog.PublicModels())
	s.pool.Profile(record.ID, updated.Priority, updated.Rank())
	if len(initialization.Quota.Groups) > 0 {
		s.quotas[record.ID] = quotaRead{summary: initialization.Quota, at: time.Now()}
		s.ledger.RecordQuota(record.ID, initialization.Quota.Groups)
	}
	s.pool.SetQuota(record.ID, s.quotas[record.ID].summary)
	go companion.Run(watch, func(catalog antigravity.Catalog) { s.setCatalog(current, catalog) })
	return nil
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
	summary, err := current.client.RetrieveUserQuotaSummary(context.WithoutCancel(ctx))
	if err != nil {
		slog.WarnContext(ctx, "读取额度摘要失败 | 错误="+err.Error(), "source", current.id)
		return
	}
	s.setQuota(current.id, summary)
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

// RefreshQuota 实时读取一个账户的套餐与额度摘要：已初始化的账户经自己的客户端读取，其余账户（含停用）以保存的 token、设备档案与代理读取；先发 daily loadCodeAssist，项目或套餐变化时写回账户库并更新调度等级
func (s *Service) RefreshQuota(ctx context.Context, id string) error {
	var record *auth.Account
	for _, item := range s.store.Accounts() {
		if item.ID == id {
			record = item
		}
	}
	if record == nil {
		return fmt.Errorf("账户 %s 不存在", id)
	}
	var client *antigravity.Client
	if current := s.account(id); current != nil {
		client = current.client
	} else {
		proxy := s.proxy(record)
		client = antigravity.NewClient(id, s.tokenSource(record, proxy), record.Device, proxy)
	}
	assist, err := client.LoadCodeAssist(ctx, antigravity.EndpointDaily)
	if err != nil {
		return err
	}
	if updated := assisted(*record, assist); updated != *record {
		if err := s.store.Save(&updated); err != nil {
			return err
		}
		s.pool.Profile(id, updated.Priority, updated.Rank())
	}
	summary, err := client.RetrieveUserQuotaSummary(ctx)
	if err != nil {
		return err
	}
	s.setQuota(id, summary)
	return nil
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

// composer 返回实时配置档位对应的 Composer，档位或自定义档的段与包装方式变化时重建
func (s *Service) composer(cfg *config.Config) (*prompt.Composer, error) {
	tier := prompt.Tier(cfg.SystemPrompt)
	custom := prompt.Custom{Sections: cfg.SystemPromptSections, Wrap: cfg.SystemPromptWrap}
	key := string(tier) + "|" + strings.Join(custom.Sections, ",") + "|" + custom.Wrap
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
