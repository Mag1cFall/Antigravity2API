package requestdb

import "strings"

// longContext 是长上下文分档的输入 token 界限，输入超过该值的请求按长上下文单价计费
const longContext = 200_000

// tierSuffixes 是上游模型 ID 中的思考档位与思考型号后缀，按长度从长到短匹配
var tierSuffixes = []string{"-extra-low", "-thinking", "-medium", "-tiered", "-high", "-low"}

// price 是一个模型每百万 token 的美元单价，cached 为缓存命中输入单价，long 前缀为长上下文分档单价，0 表示不分档
type price struct {
	input, cached, output             float64
	longInput, longCached, longOutput float64
}

// prices 是按去掉档位后缀的上游模型 ID 索引的文本单价，输出单价包含思考 token；来源见各行注释
var prices = map[string]price{
	// claude-opus-5-5 是 Claude API 价目页 Claude Opus 5.5
	"claude-opus-5-5": {input: 4, cached: 0.20, output: 20},
	// claude-sonnet-5-5 是 Claude API 价目页 Claude Sonnet 5.5
	"claude-sonnet-5-5": {input: 2, cached: 0.10, output: 10},
	// claude-opus-4-6 是 Claude API 价目页 Claude Opus 4.6
	"claude-opus-4-6": {input: 5, cached: 0.50, output: 25},
	// claude-sonnet-4-6 是 Claude API 价目页 Claude Sonnet 4.6
	"claude-sonnet-4-6": {input: 3, cached: 0.30, output: 15},
	// gpt-oss-120b 是 Google Cloud 定价页 Model Garden gpt-oss-120b，没有缓存单价
	"gpt-oss-120b": {input: 0.09, cached: 0.09, output: 0.36},
	// gemini-4-argon 是 Google Cloud 定价页 Gemini 4 Argon 2026-11-30 前的全球价
	"gemini-4-argon": {input: 2, cached: 0.10, output: 10},
	// gemini-3.8-flash 是 Gemini API 付费层 Gemini 3.8 Flash 2026-12-31 前的标准价
	"gemini-3.8-flash": {input: 0.75, cached: 0.075, output: 3.75},
	// gemini-3.5-flash 是 Google Cloud 定价页 Gemini 3.5 Flash 全球价
	"gemini-3.5-flash": {input: 1.50, cached: 0.15, output: 9},
	// gemini-3-flash-agent 是官方目录中显示为 Gemini 3.5 Flash (High) 的型号，单价同 gemini-3.5-flash
	"gemini-3-flash-agent": {input: 1.50, cached: 0.15, output: 9},
	// gemini-3.5-flash-lite 是 Gemini API 付费层 Gemini 3.5 Flash-Lite 标准价
	"gemini-3.5-flash-lite": {input: 0.30, cached: 0.03, output: 2.50},
	// gemini-3.1-flash-lite 是 Gemini API 付费层 Gemini 3.1 Flash-Lite 标准价
	"gemini-3.1-flash-lite": {input: 0.25, cached: 0.025, output: 1.50},
	// gemini-3.1-flash-image 是 Gemini API 付费层 Gemini 3.1 Flash Image 的文本单价，没有缓存单价
	"gemini-3.1-flash-image": {input: 0.50, cached: 0.50, output: 3},
	// gemini-3-flash 是 Gemini API 付费层 Gemini 3 Flash Preview 标准价
	"gemini-3-flash": {input: 0.50, cached: 0.05, output: 3},
	// gemini-2.5-pro 是 Gemini API 付费层 Gemini 2.5 Pro 标准价
	"gemini-2.5-pro": {input: 1.25, cached: 0.125, output: 10, longInput: 2.50, longCached: 0.25, longOutput: 15},
	// gemini-2.5-flash 是 Gemini API 付费层 Gemini 2.5 Flash 标准价
	"gemini-2.5-flash": {input: 0.30, cached: 0.03, output: 2.50},
	// gemini-2.5-flash-lite 是 Gemini API 付费层 Gemini 2.5 Flash-Lite 标准价
	"gemini-2.5-flash-lite": {input: 0.10, cached: 0.01, output: 0.40},
}

// Cost 按上游模型 ID 的单价估算一次请求的美元费用并返回是否有单价；cached 是 input 中命中缓存的部分，output 是思考与回复 token 之和
func Cost(model string, input, cached, output int64) (float64, bool) {
	for _, suffix := range tierSuffixes {
		if trimmed, ok := strings.CutSuffix(model, suffix); ok {
			model = trimmed
			break
		}
	}
	value, ok := prices[model]
	if value.longInput > 0 && input > longContext {
		value.input, value.cached, value.output = value.longInput, value.longCached, value.longOutput
	}
	return (float64(input-cached)*value.input + float64(cached)*value.cached + float64(output)*value.output) / 1e6, ok
}

// rowCost 返回请求记录的估算费用，模型没有单价且消耗了 token 时为 nil
func rowCost(row *Row) *float64 {
	output := row.ReasoningTokens + row.ReplyTokens
	cost, ok := Cost(row.UpstreamModel, row.InputTokens, row.CachedTokens, output)
	if !ok && row.InputTokens+output > 0 {
		return nil
	}
	return &cost
}
