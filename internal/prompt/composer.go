package prompt

// Tier 为系统提示档位
type Tier string

const (
	// TierFull 保留官方全部段与 user 轮包装
	TierFull Tier = "full"
	// TierMedium 保留 identity、guidelines、communication_style 三段与 user 轮包装
	TierMedium Tier = "medium"
	// TierAggressive 只保留 identity 段，user 轮不包装
	TierAggressive Tier = "aggressive"
	// TierCustom 使用 Custom 的两个模板
	TierCustom Tier = "custom"
)

// Custom 为一档的 system 模板与 user 模板原文，User 为空时 user 轮不包装
type Custom struct {
	System string `json:"system"`
	User   string `json:"user"`
}
