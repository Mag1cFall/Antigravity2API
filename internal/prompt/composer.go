package prompt

// Tier 为系统提示档位
type Tier string

const (
	// TierFull 保留官方全部 10 段与 user 轮包装
	TierFull Tier = "full"
	// TierMedium 保留 identity、guidelines、communication_style 三段与 user 轮包装
	TierMedium Tier = "medium"
	// TierAggressive 只保留 identity 段，user 轮不包装
	TierAggressive Tier = "aggressive"
	// TierCustom 保留 Custom 选中的官方段，user 轮按 Custom 的包装方式
	TierCustom Tier = "custom"
)

const (
	// WrapFull 为官方完整包装：请求、本地时间与首轮的模型选择说明
	WrapFull = "full"
	// WrapTime 为只包装请求与本地时间
	WrapTime = "time"
	// WrapNone 为不包装
	WrapNone = "none"
)

// Wraps 是 user 轮包装方式
var Wraps = []string{WrapFull, WrapTime, WrapNone}

// Custom 为自定义档的选项：Sections 为保留的官方段名，按官方顺序拼接；Wrap 为 user 轮包装方式
type Custom struct {
	Sections []string
	Wrap     string
}

// templates 为一档的 system 与 user 模板原文，User 为空时 user 轮不包装
type templates struct {
	System, User string
}
