package app

import (
	"cmp"
	"net/http"

	"github.com/Mag1cFall/Antigravity2API/internal/config"
	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
)

// getPromptPreview 以预览数据渲染档位的系统指令与首轮 user 消息并列出全部官方段；tier、sections 与 wrap 未给出时取实时配置
func (s *server) getPromptPreview(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	cfg := s.live.Load()
	custom := prompt.Custom{Sections: cfg.SystemPromptSections, Wrap: cfg.SystemPromptWrap}
	if query.Has("sections") {
		custom.Sections = config.SplitList(query.Get("sections"))
	}
	if query.Has("wrap") {
		custom.Wrap = query.Get("wrap")
	}
	preview, err := prompt.Render(prompt.Tier(cmp.Or(query.Get("tier"), cfg.SystemPrompt)), custom)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_prompt", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, preview)
}
