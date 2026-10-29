package auth

import (
	"cmp"
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// Transfer 是账户 JSON 中的一个账户：Email 为文件中写的邮箱，备注名、代理、优先级、启用、AI 积分与设备档案取自本服务导出的文件（带 device.installation_id）
type Transfer struct {
	Email        string             `json:"email"`
	RefreshToken string             `json:"refresh_token"`
	Label        string             `json:"label,omitempty"`
	Proxy        string             `json:"proxy,omitempty"`
	Priority     int                `json:"priority,omitempty"`
	Enabled      *bool              `json:"enabled,omitempty"`
	AICredits    bool               `json:"ai_credits,omitempty"`
	Device       antigravity.Device `json:"device"`
}

// refreshPattern 匹配 Google OAuth refresh token
var refreshPattern = regexp.MustCompile(`1//[0-9A-Za-z_\-]+`)

// Export 返回账户的导出项，refresh token 取自 token.json，没有 token 的账户跳过
func (s *Store) Export(ids []string) []Transfer {
	var items []Transfer
	for _, account := range s.Accounts() {
		token := s.Token(account.ID)
		if token == nil || token.RefreshToken == "" || len(ids) != 0 && !slices.Contains(ids, account.ID) {
			continue
		}
		enabled := account.Enabled
		items = append(items, Transfer{Email: account.Email, RefreshToken: token.RefreshToken, Label: account.Label, Proxy: account.Proxy, Priority: account.Priority, Enabled: &enabled, AICredits: account.AICredits, Device: account.Device})
	}
	return items
}

// ParseTransfers 从账户 JSON 中取出账户，按 refresh token 去重：任意层级中带 refresh_token、refreshToken 或 token.refresh_token 的对象各为一个账户（本服务与 Antigravity Manager 的导出、CLIProxyAPI 与 gcli2api 的账户文件、opencode-antigravity-auth 与 antigravity-claude-proxy 的 accounts.json、sub2api 的数据导出），platform 不是 antigravity 的对象跳过，refreshToken 中 “|” 之后的项目 ID 去掉；内容不是 JSON 时取文本中的全部 refresh token
func ParseTransfers(data []byte) []Transfer {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		var items []Transfer
		for _, token := range refreshPattern.FindAllString(string(data), -1) {
			items = appendTransfer(items, Transfer{RefreshToken: token})
		}
		return items
	}
	return collect(root, nil)
}

// collect 深度优先收集对象中的账户
func collect(node any, items []Transfer) []Transfer {
	switch value := node.(type) {
	case []any:
		for _, item := range value {
			items = collect(item, items)
		}
	case map[string]any:
		if platform, ok := value["platform"].(string); ok && platform != "antigravity" {
			return items
		}
		if item, ok := transfer(value); ok {
			return appendTransfer(items, item)
		}
		for _, child := range value {
			items = collect(child, items)
		}
	}
	return items
}

// transfer 把带 refresh token 的对象转为账户
func transfer(value map[string]any) (Transfer, bool) {
	token, _ := cmp.Or(value["refresh_token"], value["refreshToken"]).(string)
	if nested, ok := value["token"].(map[string]any); ok && token == "" {
		token, _ = nested["refresh_token"].(string)
	}
	token, _, _ = strings.Cut(strings.TrimSpace(token), "|")
	if !strings.HasPrefix(token, "1//") {
		return Transfer{}, false
	}
	var item Transfer
	if device, ok := value["device"].(map[string]any); ok && device["installation_id"] != nil {
		data, _ := json.Marshal(value)
		json.Unmarshal(data, &item)
	}
	email, _ := cmp.Or(value["email"], value["name"]).(string)
	item.Email, item.RefreshToken = strings.TrimSpace(email), token
	return item, true
}

// appendTransfer 追加 refresh token 尚未出现的账户
func appendTransfer(items []Transfer, item Transfer) []Transfer {
	for _, existing := range items {
		if existing.RefreshToken == item.RefreshToken {
			return items
		}
	}
	return append(items, item)
}
