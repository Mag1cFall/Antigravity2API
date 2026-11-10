package app

import (
	"net/http"

	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
)

// promptTemplates 为内置三档的模板与全部官方段的原文
type promptTemplates struct {
	Tiers    map[prompt.Tier]prompt.Custom `json:"tiers"`
	Sections []prompt.Section              `json:"sections"`
}

// promptPreviewRequest 为预览请求：档位，自定义档另带 system 模板与 user 模板
type promptPreviewRequest struct {
	Tier prompt.Tier `json:"tier"`
	prompt.Custom
}

// getPromptTemplates 返回内置三档的模板与按官方顺序排列的官方段原文
func (s *server) getPromptTemplates(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, promptTemplates{Tiers: prompt.Builtins, Sections: prompt.Sections})
}

// previewPrompt 以预览数据渲染档位的系统指令与首轮 user 消息，自定义档用请求中的模板
func (s *server) previewPrompt(w http.ResponseWriter, r *http.Request) {
	var request promptPreviewRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	preview, err := prompt.Render(request.Tier, request.Custom)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_prompt", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
