package app

import (
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/config"
)

// settings 是 /api/settings 的响应
type settings struct {
	// Values 是除 ADMIN_PASSWORD 外各配置项在 .env 中的文本值
	Values           map[string]string `json:"values"`
	AdminPasswordSet bool              `json:"admin_password_set"`
	// RestartRequired 是保存值与运行值不同、重启服务后才生效的配置项
	RestartRequired []string `json:"restart_required"`
}

// getSettings 返回 .env 中保存的配置
func (s *server) getSettings(w http.ResponseWriter, _ *http.Request) {
	saved, err := config.Load(config.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_unreadable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.settings(saved))
}

// updateSettings 按 .env 的解析与校验规则合并提交的配置项，原子写回 .env；除监听地址与目录外的项替换运行配置，下一次请求生效
func (s *server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Values map[string]string `json:"values"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	cfg, err := config.Load(config.File)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "settings_unreadable", err.Error())
		return
	}
	saved := cfg
	if err := cfg.Set(input.Values); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_settings", err.Error())
		return
	}
	if err := cfg.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_settings", err.Error())
		return
	}
	if err := cfg.Save(config.File); err != nil {
		writeError(w, http.StatusInternalServerError, "settings_save_failed", err.Error())
		return
	}
	running := cfg
	running.Listen, running.AuthDir, running.RuntimeDir = s.boot.Listen, s.boot.AuthDir, s.boot.RuntimeDir
	s.live.Store(&running)
	s.deps.Logger.SetLevel(running.LogLevel)
	s.deps.Logger.SetFormat(running.LogFormat)
	if err := s.deps.Logger.SetFile(running.LogFile, running.LogMaxTotalMB); err != nil {
		writeError(w, http.StatusInternalServerError, "settings_apply_failed", err.Error())
		return
	}
	s.ledger.SetBodyCapture(running.RequestLogBodies)
	s.auth.update(running)
	slog.InfoContext(r.Context(), "服务配置已保存 | 变更="+strings.Join(saved.Changed(cfg), ","), "source", "service")
	writeJSON(w, http.StatusOK, s.settings(cfg))
}

// settings 投影保存的配置，并列出叠加命令行覆盖后与启动值不同的监听地址与目录
func (s *server) settings(saved config.Config) settings {
	values := saved.Values()
	delete(values, "ADMIN_PASSWORD")
	effective := saved
	s.deps.Overrides.Apply(&effective)
	restart := []string{}
	for _, key := range effective.Changed(s.boot) {
		if slices.Contains(config.RestartKeys, key) {
			restart = append(restart, key)
		}
	}
	return settings{Values: values, AdminPasswordSet: saved.AdminPassword != "", RestartRequired: restart}
}
