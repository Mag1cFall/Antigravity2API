package app

import (
	"encoding/json"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
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

// validationURL 匹配上游错误正文 ErrorInfo.metadata 中的 validation_url
var validationURL = regexp.MustCompile(`"validation_url"\s*:\s*("(?:[^"\\]|\\.)*")`)

// verifyURL 返回账户错误中上游给出的验证链接
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
	return accounts
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
