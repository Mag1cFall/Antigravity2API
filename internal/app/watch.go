package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
	"github.com/Mag1cFall/Antigravity2API/internal/service"
)

// pollInterval 是读取账户状态的间隔
const pollInterval = 2 * time.Second

// account 是管理端账户
type account struct {
	// ID 是账户邮箱
	ID string `json:"id"`
	// Label 是账户备注名
	Label string `json:"label,omitempty"`
	// State 为 ready、cooling、pending、error 或 disabled
	State string `json:"state"`
	// Tier 是套餐，有付费套餐时为付费套餐 ID
	Tier    string `json:"tier"`
	Project string `json:"project"`
	// Proxy 是账户代理，密码替换为 xxxxx，为空时跟随全局 PROXY
	Proxy string `json:"proxy,omitempty"`
	// Priority 是调度优先级，越大越先使用
	Priority int       `json:"priority"`
	AddedAt  time.Time `json:"added_at"`
	// TokenExpiry 是 access token 的到期时间
	TokenExpiry *time.Time `json:"token_expiry,omitempty"`
	// Active 是进行中的请求数
	Active int `json:"active"`
	// Error 是最近一次初始化错误或停止调度的原因
	Error string `json:"error,omitempty"`
	// VerifyURL 是上游要求验证账户时错误正文中的验证链接
	VerifyURL string `json:"verify_url,omitempty"`
	// Cooldown 是冷却中的额度组及其截止时间
	Cooldown map[string]time.Time `json:"cooldown,omitempty"`
	// Quota 是最近一次读到的额度桶，账户出错或重新初始化后保留
	Quota []quotaBucket `json:"quota"`
	// QuotaAt 是 Quota 的读取时间，尚未读取时省略
	QuotaAt *time.Time `json:"quota_at,omitempty"`
	// Shared 按额度组（gemini、3p）列出与该账户共享额度池的其他就绪账户
	Shared map[string][]string `json:"shared,omitempty"`
	// Family 是与该账户共用同一组每周额度桶（同一 Google One 家庭群组）的其他账户，含停用与出错的账户
	Family []string `json:"family,omitempty"`
	// AICredits 为真时额度用尽后以 AI 积分继续生成
	AICredits bool `json:"ai_credits"`
	// CreditBalance 为 Google One AI 积分余额，CreditMinimum 为以积分生成所需的最低余额，付费套餐没有该积分时都省略
	CreditBalance *int64 `json:"credit_balance,omitempty"`
	CreditMinimum *int64 `json:"credit_minimum,omitempty"`
	// Device 是账户的设备档案
	Device device `json:"device"`
}

// device 是账户的设备档案及由它决定的主机名与 User-Agent
type device struct {
	antigravity.Device
	Hostname  string `json:"hostname"`
	UserAgent string `json:"user_agent"`
}

// quotaBucket 是一个额度桶，BucketID 为 gemini-5h、gemini-weekly、3p-5h 或 3p-weekly
type quotaBucket struct {
	BucketID string `json:"bucket_id"`
	// Window 为 5h 或 weekly
	Window            string     `json:"window"`
	RemainingFraction float64    `json:"remaining_fraction"`
	ResetTime         *time.Time `json:"reset_time,omitempty"`
}

// validationURL 匹配上游错误正文 ErrorInfo.metadata 中的 validation_url、违反条款时的 appeal_url 与不能使用的套餐的 validationUrl
var validationURL = regexp.MustCompile(`"(?:validation_url|appeal_url|validationUrl)"\s*:\s*("(?:[^"\\]|\\.)*")`)

// verifyURL 返回账户错误中上游给出的验证或申诉链接
func verifyURL(message string) string {
	match := validationURL.FindStringSubmatch(message)
	if match == nil {
		return ""
	}
	var link string
	_ = json.Unmarshal([]byte(match[1]), &link)
	return link
}

// redactProxy 把代理 URL 中的密码替换为 xxxxx
func redactProxy(proxy string) string {
	parsed, err := url.Parse(proxy)
	if err != nil {
		return proxy
	}
	return parsed.Redacted()
}

// watch 定期同步账户目录与读取账户状态，目录变化时后台刷新账户池，列表变化时推送 accounts 事件
func (s *server) watch() {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if changed, err := s.deps.Store.Sync(); err != nil {
			slog.Warn("读取账户目录失败 | 错误="+err.Error(), "source", "service")
		} else if changed {
			slog.Info("账户目录已变化，刷新账户池", "source", "service")
			s.refreshLater()
		}
		s.events.setAccounts(s.accounts())
		select {
		case <-ticker.C:
		case <-s.ctx.Done():
			return
		}
	}
}

// accounts 把账户池状态投影为管理端账户
func (s *server) accounts() []account {
	now := time.Now()
	statuses := s.deps.Service.Accounts()
	accounts := make([]account, 0, len(statuses))
	for _, status := range statuses {
		accounts = append(accounts, s.account(status, now))
	}
	family := s.family.members(s.ledger, statuses)
	for index := range accounts {
		accounts[index].Family = family[accounts[index].ID]
	}
	return accounts
}

const (
	// familySkew 是同一额度桶在不同账户上读到的重置时刻的最大差
	familySkew = 2 * time.Second
	// familyKeep 是每个每周额度桶本次计时内保留的剩余比例个数
	familyKeep = 256
)

// weeklyRun 是一个每周额度桶本次计时的重置时刻与计时内读到过的剩余比例
type weeklyRun struct {
	reset     time.Time
	fractions []float64
}

// familyBook 记录各账户每周额度桶的计时与读到过的剩余比例，用来找出共用同一组额度桶的家庭群组成员；首次使用时从账本补入最近 8 天的额度快照
type familyBook struct {
	mu     sync.Mutex
	seeded bool
	seen   map[string]map[string]*weeklyRun
}

// observe 记下一次读到的每周额度桶：重置时刻与本次计时相差超过 familySkew 时开始新的计时，剩余比例在 0 与 99.9% 之间（已开始消耗且未耗尽）时记入；尚未开始计时的窗口重置时刻随读取时间顺延，剩余 100%，不记比例
func (book *familyBook) observe(id, bucket string, reset time.Time, fraction float64) {
	buckets := book.seen[id]
	if buckets == nil {
		buckets = map[string]*weeklyRun{}
		book.seen[id] = buckets
	}
	run := buckets[bucket]
	if run == nil || run.reset.Sub(reset).Abs() > familySkew {
		run = &weeklyRun{reset: reset}
		buckets[bucket] = run
	}
	if fraction > 0 && fraction < 0.999 && !slices.Contains(run.fractions, fraction) {
		run.fractions = append(run.fractions, fraction)
		if len(run.fractions) > familyKeep {
			run.fractions = run.fractions[1:]
		}
	}
}

// members 记下各账户最近一次读到的每周额度桶，返回每个账户同一家庭群组的其他账户：两个账户都已消耗的每周额度桶重置时刻相差都不超过 familySkew，且其中至少一个桶在两个账户上读到过相同的剩余比例（同一个额度桶）
func (book *familyBook) members(ledger *requestdb.Store, statuses []service.AccountStatus) map[string][]string {
	book.mu.Lock()
	defer book.mu.Unlock()
	if book.seen == nil {
		book.seen = map[string]map[string]*weeklyRun{}
	}
	if !book.seeded {
		book.seeded = true
		now := time.Now()
		snapshots, err := ledger.QuotaSnapshots(context.Background(), now.Add(-8*24*time.Hour), now, "")
		if err != nil {
			slog.Warn("读取额度快照失败 | 错误="+err.Error(), "source", "service")
		}
		for _, snapshot := range snapshots {
			if snapshot.Window == "weekly" && snapshot.ResetTime != nil {
				book.observe(snapshot.Account, snapshot.BucketID, *snapshot.ResetTime, snapshot.RemainingFraction)
			}
		}
	}
	for _, status := range statuses {
		for _, group := range status.Quota {
			for _, bucket := range group.Buckets {
				if reset, err := time.Parse(time.RFC3339, bucket.ResetTime); bucket.Window == "weekly" && err == nil {
					book.observe(status.ID, bucket.BucketID, reset, bucket.RemainingFraction)
				}
			}
		}
	}
	family := map[string][]string{}
	for _, a := range statuses {
		for _, b := range statuses {
			if a.ID != b.ID && sharesBuckets(book.seen[a.ID], book.seen[b.ID]) {
				family[a.ID] = append(family[a.ID], b.ID)
			}
		}
	}
	return family
}

// sharesBuckets 判断两个账户共用同一组每周额度桶
func sharesBuckets(a, b map[string]*weeklyRun) bool {
	matched := false
	for bucket, x := range a {
		y := b[bucket]
		if y == nil || len(x.fractions) == 0 || len(y.fractions) == 0 {
			continue
		}
		if x.reset.Sub(y.reset).Abs() > familySkew {
			return false
		}
		matched = matched || slices.ContainsFunc(x.fractions, func(fraction float64) bool { return slices.Contains(y.fractions, fraction) })
	}
	return matched
}

// account 投影一个账户，只保留未到期的冷却
func (s *server) account(status service.AccountStatus, now time.Time) account {
	item := account{
		ID: status.ID, Label: status.Label, Tier: status.Tier, Project: status.Project, Proxy: redactProxy(status.Proxy), Priority: status.Priority, AddedAt: status.AddedAt.UTC(),
		Active: status.Active, Error: status.Error, VerifyURL: verifyURL(status.Error), Quota: []quotaBucket{}, Shared: status.Shared, AICredits: status.AICredits,
		Device: device{Device: status.Device, Hostname: status.Device.Hostname(), UserAgent: status.Device.UserAgent()},
	}
	if balance, err := strconv.ParseInt(status.CreditBalance, 10, 64); err == nil {
		minimum, _ := strconv.ParseInt(status.CreditMinimum, 10, 64)
		item.CreditBalance, item.CreditMinimum = &balance, &minimum
	}
	if status.PaidTier != "" {
		item.Tier = status.PaidTier
	}
	if !status.QuotaAt.IsZero() {
		at := status.QuotaAt.UTC()
		item.QuotaAt = &at
	}
	if token := s.deps.Store.Token(status.ID); token != nil && !token.Expiry.IsZero() {
		expiry := token.Expiry.UTC()
		item.TokenExpiry = &expiry
	}
	for group, until := range status.Cooldown {
		if until.After(now) {
			if item.Cooldown == nil {
				item.Cooldown = map[string]time.Time{}
			}
			item.Cooldown[group] = until.UTC()
		}
	}
	for _, group := range status.Quota {
		for _, bucket := range group.Buckets {
			value := quotaBucket{BucketID: bucket.BucketID, Window: bucket.Window, RemainingFraction: bucket.RemainingFraction}
			if reset, err := time.Parse(time.RFC3339, bucket.ResetTime); err == nil {
				reset = reset.UTC()
				value.ResetTime = &reset
			}
			item.Quota = append(item.Quota, value)
		}
	}
	switch {
	case !status.Enabled:
		item.State = "disabled"
	case status.Error != "":
		item.State = "error"
	case !status.Ready:
		item.State = "pending"
	case item.Cooldown != nil:
		item.State = "cooling"
	default:
		item.State = "ready"
	}
	return item
}
