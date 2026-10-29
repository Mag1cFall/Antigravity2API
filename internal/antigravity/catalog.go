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
	ID                       string          `json:"-"`
	Model                    string          `json:"model"`
	DisplayName              string          `json:"displayName"`
	APIProvider              string          `json:"apiProvider"`
	ModelProvider            string          `json:"modelProvider"`
	VertexModelID            string          `json:"vertexModelId"`
	MaxOutputTokens          int64           `json:"maxOutputTokens"`
	MaxTokens                int64           `json:"maxTokens"`
	ThinkingLevel            int             `json:"thinkingLevel"`
	ThinkingBudget           *int64          `json:"thinkingBudget"`
	MinThinkingBudget        int64           `json:"minThinkingBudget"`
	SupportsThinking         bool            `json:"supportsThinking"`
	SupportsAdaptiveThinking bool            `json:"supportsAdaptiveThinking"`
	SupportsImages           bool            `json:"supportsImages"`
	SupportsVideo            bool            `json:"supportsVideo"`
	SupportedMIMETypes       map[string]bool `json:"supportedMimeTypes"`
	Recommended              bool            `json:"recommended"`
	TagTitle                 string          `json:"tagTitle"`
	TagDescription           string          `json:"tagDescription"`
	IsInternal               bool            `json:"isInternal"`
	RequiresLeadInGeneration bool            `json:"requiresLeadInGeneration"`
	TabJumpPrintLineRange    bool            `json:"tabJumpPrintLineRange"`
	Disabled                 bool            `json:"disabled"`
	QuotaInfo                QuotaInfo       `json:"quotaInfo"`
	// Agent 表示条目列在 Agent 模型选择器中
	Agent bool `json:"-"`
	// Kind 为图像生成、语音转写用途列表中的条目用途或 Tab 补全条目，其余条目为空
	Kind string `json:"-"`
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
	DefaultAgentModelID        string                  `json:"defaultAgentModelId"`
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

// retiredLabel 匹配选择器外旧 Agent 条目显示名末尾的档位，这类条目请求时返回下线提示
var retiredLabel = regexp.MustCompile(` \((Low|Medium|High)\)$`)

// PublicModels 返回可请求的条目：先按选择器顺序列出未禁用的 Agent 模型，再列出图像生成与语音转写用途列表中的条目（目录没有对应模型条目时只有 ID），最后按 ID 列出其余非内部、未禁用、有输出上限的条目：有显示名且显示名不带档位的条目，以及没有显示名、要求预填续写且不打印行范围的 Tab 补全条目；套餐不含的模型在目录中为 disabled
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
		case model.DisplayName != "" && !retiredLabel.MatchString(model.DisplayName):
			others = append(others, model)
		}
	}
	slices.SortFunc(others, func(a, b CatalogModel) int { return cmp.Compare(a.ID, b.ID) })
	return append(models, others...)
}

// QuotaGroup 返回模型所属的额度桶组
func (m CatalogModel) QuotaGroup() string {
	if m.ModelProvider == ProviderGoogle {
		return QuotaGroupGemini
	}
	return QuotaGroupThirdParty
}

// Public 返回公开目录中的模型条目，用途列表中的条目以用途标注能力，没有显示名时以 ID 为名称
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
	return Model{
		ID:               m.ID,
		Name:             cmp.Or(m.DisplayName, m.ID),
		Description:      m.TagDescription,
		Methods:          []string{"generateContent", "streamGenerateContent"},
		InputTokenLimit:  m.MaxTokens,
		OutputTokenLimit: m.MaxOutputTokens,
		Capabilities:     capabilities,
		Provider:         m.ModelProvider,
		QuotaGroup:       m.QuotaGroup(),
	}
}
