package antigravity

import (
	"cmp"
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

const (
	// ProviderGoogle 为目录中 Gemini 模型的 modelProvider
	ProviderGoogle = "MODEL_PROVIDER_GOOGLE"
	// ProviderAnthropic 为目录中 Claude 模型的 modelProvider
	ProviderAnthropic = "MODEL_PROVIDER_ANTHROPIC"
	// ProviderOpenAI 为目录中 GPT 模型的 modelProvider
	ProviderOpenAI = "MODEL_PROVIDER_OPENAI"
	// QuotaGroupGemini 为 Gemini 模型共享的额度桶组
	QuotaGroupGemini = "gemini"
	// QuotaGroupThirdParty 为 Claude 与 GPT 模型共享的额度桶组
	QuotaGroupThirdParty = "3p"
	// QuotaGroupArgon 为 Gemini Argon 模型独占的周额度桶组，只在带 Argon 授权的账户上出现
	QuotaGroupArgon = "gemini-argon-wtu"
	// KindImage 为 imageGenerationModelIds 中的图像生成模型
	KindImage = "image"
	// KindTranscription 为 audioTranscriptionModelIds 中的语音转写模型
	KindTranscription = "transcription"
)

// QuotaInfo 表示目录条目附带的额度状态
type QuotaInfo struct {
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime"`
}

// CatalogModel 表示 fetchAvailableModels 返回的一个模型
type CatalogModel struct {
	ID                       string     `json:"-"`
	Model                    string     `json:"model"`
	DisplayName              string     `json:"displayName"`
	APIProvider              string     `json:"apiProvider"`
	ModelProvider            string     `json:"modelProvider"`
	MaxOutputTokens          int64      `json:"maxOutputTokens"`
	MaxTokens                int64      `json:"maxTokens"`
	ThinkingLevel            int        `json:"thinkingLevel"`
	ThinkingBudget           *int64     `json:"thinkingBudget"`
	SupportsThinking         bool       `json:"supportsThinking"`
	SupportsImages           bool       `json:"supportsImages"`
	SupportsVideo            bool       `json:"supportsVideo"`
	TagDescription           string     `json:"tagDescription"`
	IsInternal               bool       `json:"isInternal"`
	RequiresLeadInGeneration bool       `json:"requiresLeadInGeneration"`
	TabJumpPrintLineRange    bool       `json:"tabJumpPrintLineRange"`
	Disabled                 bool       `json:"disabled"`
	QuotaInfo                QuotaInfo  `json:"quotaInfo"`
	ModelExperiments         struct {
		Experiments map[string]ModelExperiment `json:"experiments"`
	} `json:"modelExperiments"`
	// Agent 表示条目列在 Agent 模型选择器中
	Agent bool `json:"-"`
	// Kind 为图像生成、语音转写用途列表中的条目用途或 Tab 补全条目，其余条目为空
	Kind string `json:"-"`
}

// ModelExperiment 表示目录条目 modelExperiments 中的一项实验，StringValue 为字符串取值
type ModelExperiment struct {
	StringValue string `json:"stringValue"`
}

// ModelGroup 表示选择器中的一组模型
type ModelGroup struct {
	ModelIDs []string `json:"modelIds"`
}

// ModelSort 表示选择器的一种排序
type ModelSort struct {
	DisplayName string       `json:"displayName"`
	Groups      []ModelGroup `json:"groups"`
}

// Catalog 表示 fetchAvailableModels 的响应
type Catalog struct {
	Models                     map[string]CatalogModel `json:"models"`
	AgentModelSorts            []ModelSort             `json:"agentModelSorts"`
	ImageGenerationModelIDs    []string                `json:"imageGenerationModelIds"`
	AudioTranscriptionModelIDs []string                `json:"audioTranscriptionModelIds"`
	Raw                        json.RawMessage         `json:"-"`
}

// FetchAvailableModels 读取账户的模型目录
func (c *Client) FetchAvailableModels(ctx context.Context) (Catalog, error) {
	var raw json.RawMessage
	if err := c.post(ctx, c.base+"/v1internal:fetchAvailableModels", projectRequest{Project: c.Project()}, &raw); err != nil {
		return Catalog{}, err
	}
	var catalog Catalog
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return Catalog{}, err
	}
	for id, model := range catalog.Models {
		model.ID = strings.TrimPrefix(id, "models/")
		catalog.Models[id] = model
	}
	catalog.Raw = raw
	c.notify(Observed{Catalog: &catalog})
	return catalog, nil
}

// TierLabel 匹配模型显示名末尾的思考档位后缀
var TierLabel = regexp.MustCompile(` \((Low|Medium|High|XHigh)\)$`)

// PublicModels 返回账户可请求的模型条目
// 排序与过滤规则见 docs/protocol.md“`fetchAvailableModels` 顶层字段”
func (catalog Catalog) PublicModels() []CatalogModel {
	var models []CatalogModel
	listed := make(map[string]bool)
	for _, sort := range catalog.AgentModelSorts {
		for _, group := range sort.Groups {
			for _, id := range group.ModelIDs {
				if model, ok := catalog.Models[id]; ok && !listed[id] && !model.Disabled {
					model.Agent = true
					models = append(models, model)
					listed[id] = true
				}
			}
		}
	}
	for _, purpose := range []struct {
		kind string
		ids  []string
	}{{KindImage, catalog.ImageGenerationModelIDs}, {KindTranscription, catalog.AudioTranscriptionModelIDs}} {
		for _, id := range purpose.ids {
			id = strings.TrimPrefix(id, "models/")
			model, ok := catalog.Models[id]
			if !ok {
				model = CatalogModel{ID: id, ModelProvider: ProviderGoogle}
			}
			if !listed[id] && !model.Disabled {
				model.Kind = purpose.kind
				models = append(models, model)
				listed[id] = true
			}
		}
	}
	var others []CatalogModel
	for id, model := range catalog.Models {
		if listed[id] || model.IsInternal || model.Disabled || model.MaxOutputTokens <= 0 {
			continue
		}
		switch {
		case model.DisplayName == "" && model.RequiresLeadInGeneration && !model.TabJumpPrintLineRange:
			model.Kind = KindTab
			others = append(others, model)
		case model.DisplayName != "" && !TierLabel.MatchString(model.DisplayName):
			others = append(others, model)
		}
	}
	slices.SortFunc(others, func(a, b CatalogModel) int { return cmp.Compare(a.ID, b.ID) })
	return append(models, others...)
}

// QuotaGroup 返回模型所属的额度桶组，Argon 模型只计入自己的周额度桶
func (m CatalogModel) QuotaGroup() string {
	switch {
	case m.ModelProvider == ProviderGoogle && strings.Contains(m.ID, "argon"):
		return QuotaGroupArgon
	case m.ModelProvider == ProviderGoogle:
		return QuotaGroupGemini
	}
	return QuotaGroupThirdParty
}

// BucketGroup 返回额度桶所属的额度组，即 bucketId 最后一个 - 之前的部分，如 gemini-argon-wtu-weekly 属于 gemini-argon-wtu
func BucketGroup(bucketID string) string {
	if index := strings.LastIndexByte(bucketID, '-'); index > 0 {
		return bucketID[:index]
	}
	return bucketID
}

// Public 返回公开目录中的模型条目，用途列表中的条目以用途标注能力，没有显示名时以 ID 为名称；转写模型与 Agent 模型另有 Live 会话的 bidiGenerateContent
func (m CatalogModel) Public() Model {
	capabilities := map[string]bool{"thinking": m.SupportsThinking, "images": m.SupportsImages, "video": m.SupportsVideo}
	switch m.Kind {
	case KindImage:
		capabilities["image_generation"] = true
	case KindTranscription:
		capabilities["transcription"] = true
	case KindTab:
		capabilities["completion"] = true
	}
	methods := []string{"generateContent", "countTokens", "createCachedContent"}
	if m.Kind == KindTranscription || m.Agent {
		methods = append(methods, "bidiGenerateContent")
	}
	return Model{
		ID:               m.ID,
		Name:             cmp.Or(m.DisplayName, m.ID),
		Description:      m.TagDescription,
		Methods:          methods,
		InputTokenLimit:  m.MaxTokens,
		OutputTokenLimit: m.MaxOutputTokens,
		Capabilities:     capabilities,
		Provider:         m.ModelProvider,
		QuotaGroup:       m.QuotaGroup(),
	}
}
