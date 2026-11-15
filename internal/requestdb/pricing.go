package requestdb

import (
	"strings"
	"time"
)

// longContext 是长上下文分档的输入 token 界限，输入超过该值的请求按长上下文单价计费
const longContext = 200_000

// tierSuffixes 是上游模型 ID 中的思考档位与思考型号后缀，按长度从长到短匹配
var tierSuffixes = []string{"-extra-low", "-thinking", "-medium", "-tiered", "-xhigh", "-high", "-low"}

// price 是一个模型自 from 起每百万 token 的美元单价，cached 为缓存命中输入单价，long 前缀为长上下文分档单价，0 表示不分档，image 为输出图片时回复 token 的单价，0 表示与 output 相同
type price struct {
	from                              time.Time
	input, cached, output             float64
	longInput, longCached, longOutput float64
	image                             float64
}

// gemini4Raise 是 Gemini API 定价页 Gemini 4 Carbon 与 Argon 推广价结束、标准价生效的时刻（2026-12-01 太平洋时间）
var gemini4Raise = time.Date(2026, 12, 1, 0, 0, 0, 0, time.FixedZone("PST", -8*3600))

// carbon 是 Gemini API 付费层 Gemini 4 Carbon 的单价，推广价到 2026-11-30 为止，之后为标准价
var carbon = []price{{input: 2, cached: 0.10, output: 10}, {from: gemini4Raise, input: 4, cached: 0.20, output: 20}}

// argon 是 Gemini API 付费层 Gemini 4 Argon 的单价，推广价到 2026-11-30 为止，之后为标准价
var argon = []price{{input: 2, cached: 0.10, output: 10}, {from: gemini4Raise, input: 4, cached: 0.20, output: 20}}

// flash38Raise 是 Gemini API 定价页 Gemini 3.8 Flash 涨价生效的时刻（2027-01-01 太平洋时间）
var flash38Raise = time.Date(2027, 1, 1, 0, 0, 0, 0, time.FixedZone("PST", -8*3600))

// hydrogen 是 Gemini API 付费层 Gemini 4 Hydrogen 的标准价
var hydrogen = []price{{input: 1.50, cached: 0.075, output: 7.50}}

// prices 是按去掉档位后缀的上游模型 ID 索引的文本单价，每个模型按生效时刻从早到晚排列，输出单价包含思考 token；来源见各行注释
var prices = map[string][]price{
	// claude-opus-5-5 是 Claude API 价目页 Claude Opus 5.5
	"claude-opus-5-5": {{input: 4, cached: 0.20, output: 20}},
	// claude-sonnet-5-5 是 Claude API 价目页 Claude Sonnet 5.5
	"claude-sonnet-5-5": {{input: 2, cached: 0.10, output: 10}},
	// claude-opus-4-6 是 Claude API 价目页 Claude Opus 4.6
	"claude-opus-4-6": {{input: 5, cached: 0.50, output: 25}},
	// claude-sonnet-4-6 是 Claude API 价目页 Claude Sonnet 4.6
	"claude-sonnet-4-6": {{input: 3, cached: 0.30, output: 15}},
	// gpt-oss-120b 是 Google Cloud 定价页 Model Garden gpt-oss-120b，没有缓存单价
	"gpt-oss-120b":      {{input: 0.09, cached: 0.09, output: 0.36}},
	"gemini-4-carbon":   carbon,
	"gemini-4-argon":    argon,
	"gemini-4-hydrogen": hydrogen,
	// gemini-pro-orchestrator 是官方目录中的 Orchestrator (Carbon)，以 Carbon 为主、部分步骤交给 Hydrogen，按 Carbon 计
	"gemini-pro-orchestrator": carbon,
	// gemini-pro-orchestrator-argon 是官方目录中的 Orchestrator (Argon)，按 Argon 计
	"gemini-pro-orchestrator-argon": argon,
	// gemini-flash-worker-agy 是官方目录中的 Gemini 4 Hydrogen (Orchestrator)，按 Hydrogen 计
	"gemini-flash-worker-agy": hydrogen,
	// gemini-3.8-flash 是 Gemini API 付费层 Gemini 3.8 Flash 的标准价，到 2026-12-31 为止与 2027-01-01 起各一档
	"gemini-3.8-flash": {{input: 0.75, cached: 0.075, output: 3.75}, {from: flash38Raise, input: 1.50, cached: 0.15, output: 7.50}},
	// gemini-3.5-flash 是 Google Cloud 定价页 Gemini 3.5 Flash 全球价
	"gemini-3.5-flash": {{input: 1.50, cached: 0.15, output: 9}},
	// gemini-3-flash-agent 是官方目录中显示为 Gemini 3.5 Flash (High) 的型号，单价同 gemini-3.5-flash
	"gemini-3-flash-agent": {{input: 1.50, cached: 0.15, output: 9}},
	// gemini-3.5-flash-lite 是 Gemini API 付费层 Gemini 3.5 Flash-Lite 标准价
	"gemini-3.5-flash-lite": {{input: 0.30, cached: 0.03, output: 2.50}},
	// gemini-3.1-flash-lite 是 Gemini API 付费层 Gemini 3.1 Flash-Lite 标准价
	"gemini-3.1-flash-lite": {{input: 0.25, cached: 0.025, output: 1.50}},
	// gemini-3.1-flash-image 是 Gemini API 付费层 Gemini 3.1 Flash Image 的单价，图片输出 60，文本与思考输出 3，没有缓存单价
	"gemini-3.1-flash-image": {{input: 0.50, cached: 0.50, output: 3, image: 60}},
	// gemini-3-flash 是 Gemini API 付费层 Gemini 3 Flash Preview 标准价
	"gemini-3-flash": {{input: 0.50, cached: 0.05, output: 3}},
	// gemini-2.5-pro 是 Gemini API 付费层 Gemini 2.5 Pro 标准价
	"gemini-2.5-pro": {{input: 1.25, cached: 0.125, output: 10, longInput: 2.50, longCached: 0.25, longOutput: 15}},
	// gemini-2.5-flash 是 Gemini API 付费层 Gemini 2.5 Flash 标准价
	"gemini-2.5-flash": {{input: 0.30, cached: 0.03, output: 2.50}},
	// gemini-2.5-flash-lite 是 Gemini API 付费层 Gemini 2.5 Flash-Lite 标准价
	"gemini-2.5-flash-lite": {{input: 0.10, cached: 0.01, output: 0.40}},
	// tab_flash_lite_preview 是官方目录中的 Tab 补全模型，名为 Flash-Lite 预览，按 Gemini 2.5 Flash-Lite 计
	"tab_flash_lite_preview": {{input: 0.10, cached: 0.01, output: 0.40}},
	// proactive-observer-v10 是官方目录中的语音转写模型，按 Gemini API 付费层 Gemini 3.5 Transcribe 的音频输入与文本输出单价计
	"proactive-observer-v10": {{input: 2, cached: 2, output: 12}},
}

// Cost 按上游模型 ID 在 at 时刻生效的单价估算一次请求的美元费用并返回是否有单价；cached 是 input 中命中缓存的部分，output 是思考与回复 token 之和
func Cost(model string, at time.Time, input, cached, output int64) (float64, bool) {
	value, ok := priceAt(model, at, input)
	return (float64(input-cached)*value.input + float64(cached)*value.cached + float64(output)*value.output) / 1e6, ok
}

// priceAt 返回上游模型 ID 在 at 时刻、输入为 input 个 token 时生效的单价与是否有单价
func priceAt(model string, at time.Time, input int64) (price, bool) {
	for _, suffix := range tierSuffixes {
		if trimmed, ok := strings.CutSuffix(model, suffix); ok {
			model = trimmed
			break
		}
	}
	tiers, ok := prices[model]
	var value price
	for _, tier := range tiers {
		if !at.Before(tier.from) {
			value = tier
		}
	}
	if value.longInput > 0 && input > longContext {
		value.input, value.cached, value.output = value.longInput, value.longCached, value.longOutput
	}
	return value, ok
}

// rowCost 返回请求记录的估算费用，回答中有图片时回复 token 按图片输出单价计，模型没有单价且消耗了 token 时为 nil
func rowCost(row *Row) *float64 {
	value, ok := priceAt(row.UpstreamModel, row.Time, row.InputTokens)
	if !ok && row.InputTokens+row.ReasoningTokens+row.ReplyTokens > 0 {
		return nil
	}
	reply := value.output
	if row.ImageOutput && value.image > 0 {
		reply = value.image
	}
	cost := (float64(row.InputTokens-row.CachedTokens)*value.input + float64(row.CachedTokens)*value.cached + float64(row.ReasoningTokens)*value.output + float64(row.ReplyTokens)*reply) / 1e6
	return &cost
}
