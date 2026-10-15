package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// bootDelay 为虚拟进程启动到第一次 loadCodeAssist 的最短间隔，官方冷启动现场为 0.6–0.8 s
const bootDelay = 600 * time.Millisecond

// boot 开始账户的虚拟客户端：unleash 与 clearcut 上报循环随 watch 立即开始，0.6–0.8 s 后刚登录或导入的账户走登录序列、其余走冷启动序列，账户遥测开启时按官方设置请求关闭
func boot(ctx, watch context.Context, client *antigravity.Client, id string, login bool) (*antigravity.Companion, antigravity.Initialization, error) {
	companion := antigravity.NewCompanion(client)
	go companion.Unleash(watch)
	go companion.Clearcut(watch)
	select {
	case <-time.After(bootDelay + rand.N(200*time.Millisecond)):
	case <-ctx.Done():
		return nil, antigravity.Initialization{}, ctx.Err()
	}
	initialize := client.ColdStart
	if login {
		initialize = client.Initialize
	}
	initialization, err := initialize(ctx)
	if err == nil && initialization.Settings.UserSettings.TelemetryEnabled {
		if err := client.DisableTelemetry(ctx); err != nil {
			slog.WarnContext(ctx, "关闭账户遥测失败 | 错误="+err.Error(), "source", id)
		}
	}
	return companion, initialization, err
}

// setCatalog 以伴随请求周期读取的目录更新仍在调度中的账户
func (s *Service) setCatalog(current *account, catalog antigravity.Catalog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts[current.id] == current {
		s.setModels(current.id, catalog.PublicModels())
	}
}

// hubPath 返回账户虚拟 Hub 状态文件的路径
func (s *Service) hubPath(id string) string {
	return filepath.Join(s.live.Load().RuntimeDir, "hub", id+".json")
}

// loadHub 读取账户的虚拟 Hub 状态，文件不存在时为初始状态
func loadHub(path string) antigravity.HubState {
	var state antigravity.HubState
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &state)
	}
	return state
}

// saveHub 返回把虚拟 Hub 状态写入 path 的函数
func saveHub(path string) func(antigravity.HubState) error {
	return func(state antigravity.HubState) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		data, err := json.Marshal(state)
		if err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
}
