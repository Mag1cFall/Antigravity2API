package antigravity

import (
	"bytes"
	"context"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	// clearcutPeriod 为 language_server 上报 clearcut 队列的间隔，从上一次上报结束开始计时
	clearcutPeriod = 5 * time.Second
	// clearcutOffset 为 clearcut 上报循环相对虚拟进程启动的偏移
	clearcutOffset = 250 * time.Millisecond
	// sidebarRefresh 为界面再次统计侧栏会话数相对首次统计的间隔
	sidebarRefresh = time.Minute
)

// HubState 是虚拟 Hub 保存在本地的状态，Conversations 为该账户经服务开始过的会话数
type HubState struct {
	Conversations int `json:"conversations"`
}

// hub 表示虚拟 Hub 的本地状态、保存函数、language_server 启动时刻、界面会话标识、是否冷启动与待上报的 clearcut 事件
type hub struct {
	state    HubState
	save     func(HubState) error
	launched time.Time
	browser  string
	cold     bool
	queue    [][]byte
}

// eventShape 表示 clearcut 事件的外层形态：bare 为真时套餐与地区留空，blank 为真时公共参数留空，flags 为真时附实验开关，ui 为真时带界面会话与事件时刻，env 为真时载荷带客户端环境
type eventShape struct {
	bare, blank, flags, ui, env bool
}

// startup 记录一次冷启动中 Hub 事件的进度：loaded 计第三次 loadCodeAssist 与 fetchAvailableModels 的完成，stored 计本地存储事件与随后 fetchUserInfo 的完成，configs 计 fetchConfig 的开始
type startup struct {
	loaded, stored, configs atomic.Int32
}

// SetHub 设置虚拟 Hub 的本地状态与保存函数
func (c *Client) SetHub(state HubState, save func(HubState) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hub.state, c.hub.save = state, save
}

// launch 记下虚拟进程启动：language_server 在首个 unleash 请求前 416–530 ms 启动，界面会话标识随机生成
func (c *Client) launch(started time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hub.launched, c.hub.browser = started.Add(-time.Duration(between(416, 530))*time.Millisecond), uuid.NewString()
}

// conversations 返回虚拟 Hub 的会话数
func (c *Client) conversations() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hub.state.Conversations
}

// countConversation 记下虚拟 Hub 新增一个会话并保存
func (c *Client) countConversation() {
	c.mu.Lock()
	c.hub.state.Conversations++
	state, save := c.hub.state, c.hub.save
	c.mu.Unlock()
	if save == nil {
		return
	}
	if err := save(state); err != nil {
		slog.Warn("保存虚拟 Hub 状态失败 | 错误="+err.Error(), "source", c.account)
	}
}

// log 把一个 clearcut 事件放入上报队列
func (c *Client) log(event []byte) {
	c.mu.Lock()
	c.hub.queue = append(c.hub.queue, event)
	c.mu.Unlock()
}

// flushClearcut 把队列中的全部事件合成一个 LogRequest，以账户 Bearer 经 HTTP/2 上报，队列为空时不发
func (c *Client) flushClearcut(ctx context.Context) {
	c.mu.Lock()
	events := c.hub.queue
	c.hub.queue = nil
	c.mu.Unlock()
	if len(events) == 0 {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointClearcut, bytes.NewReader(clearcutRequest(events, time.Now())))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	c.discard(c.auth, request)
}

// Clearcut 在虚拟进程启动 0.25 s 后开始上报循环，每次上报结束 5 s 后上报队列中的事件，ctx 结束时返回
func (m *Companion) Clearcut(ctx context.Context) {
	wait := time.Until(m.started.Add(clearcutOffset + clearcutPeriod))
	for {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
		m.client.flushClearcut(ctx)
		wait = clearcutPeriod
	}
}

// between 返回 [low, high] 内均匀分布的整数
func between(low, high int) int {
	return low + rand.N(high-low+1)
}

// hubParams 返回 Hub 事件的公共参数，blank 为真时全部留空
func hubParams(device Device, blank bool, params map[string]any) map[string]any {
	out := map[string]any{"machineId": "", "subclientType": "hub", "environmentType": "", "ideVersion": ClientVersion, "ideName": "antigravity", "os": device.OS}
	if blank {
		out = map[string]any{"machineId": "", "subclientType": "", "environmentType": "", "ideVersion": "", "ideName": "", "os": ""}
	}
	for key, value := range params {
		out[key] = value
	}
	return out
}

// hubEnvironment 返回界面事件载荷第 4 字段的客户端环境
func hubEnvironment(device Device) []byte {
	env := appendBytes(nil, 1, []byte("electron"))
	env = appendBytes(env, 2, []byte("hub"))
	env = appendBytes(env, 3, []byte("desktop"))
	env = appendBytes(env, 4, []byte("external"))
	env = appendBytes(env, 5, []byte(strings.ToLower(device.OS)))
	return appendBytes(env, 7, []byte("unknown"))
}

// hubEvent 把一个 Hub 事件放入上报队列并返回其时刻，界面事件的 client_event_time_ms 比 language_server 收到时早 2–3 ms
func (c *Client) hubEvent(name string, shape eventShape, params map[string]any) time.Time {
	at := time.Now()
	c.mu.Lock()
	state, browser := c.state, c.hub.browser
	c.mu.Unlock()
	params = hubParams(c.device, shape.blank, params)
	if shape.ui {
		params["browserSessionId"] = browser
		params["client_event_time_ms"] = int(at.UnixMilli()) - between(2, 3)
	}
	c.log(state.clearcutEvent(at, c.device, name, params, shape))
	return at
}

// installation 给参数加上 Hub 安装标识
func (c *Client) installation(params map[string]any) map[string]any {
	params["installation_id"] = c.device.InstallationID
	params["antigravity_ide_installation_id"] = c.device.InstallationID
	return params
}

// sinceLaunch 返回 language_server 启动至今的毫秒数
func (c *Client) sinceLaunch() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int(time.Since(c.hub.launched).Milliseconds())
}

// hubLoaded 在第三次 loadCodeAssist 与 fetchAvailableModels 都完成时记录会话摘要核对与安装状态加载，两者在套餐写入前生成
func (c *Client) hubLoaded(progress *startup) {
	if progress.loaded.Add(1) != 2 {
		return
	}
	conversations := c.conversations()
	reconcile := map[string]any{"duration_ms": between(3, 6), "outcome": "success", "rows": conversations, "trigger": "startup", "pruned": 0}
	if conversations == 0 {
		reconcile = map[string]any{"duration_ms": 0, "outcome": "empty", "rows": 0, "trigger": "empty_on_open", "pruned": 0}
	}
	c.hubEvent("summary_store_reconcile", eventShape{bare: true}, reconcile)
	c.hubEvent("hub_store_load_succeeded", eventShape{bare: true, blank: true}, c.installation(map[string]any{"file_type": "app_state"}))
	c.hubStored(progress)
}

// hubStored 在安装状态加载与第三次 loadCodeAssist 之后的 fetchUserInfo 都完成时记录用户配置与 Hub 存储加载
func (c *Client) hubStored(progress *startup) {
	if progress.stored.Add(1) != 2 {
		return
	}
	c.hubEvent("hub_store_load_succeeded", eventShape{blank: true}, c.installation(map[string]any{"file_type": "user_config", "auto_execution_policy": "CASCADE_COMMANDS_AUTO_EXECUTION_OFF"}))
	c.hubEvent("hub_store_load_succeeded", eventShape{blank: true}, c.installation(map[string]any{}))
}

// hubMigrations 记录第二次 fetchConfig 开始时的迁移、摘要订阅、启动耗时与摘要核对事件
func (c *Client) hubMigrations() {
	conversations := c.conversations()
	c.hubEvent("retroactive_migration_attempted", eventShape{blank: true}, nil)
	c.hubEvent("retroactive_migration_skipped_already_completed", eventShape{blank: true}, map[string]any{"status": "RETROACTIVE_MIGRATION_STATUS_COMPLETED_UNNECESSARY"})
	c.hubEvent("projects_migration_skipped_already_completed", eventShape{blank: true}, map[string]any{"status": "MIGRATION_STATUS_COMPLETED"})
	c.hubEvent("summaries_store_subscribe_called", eventShape{}, nil)
	c.hubEvent("summaries_reconciled", eventShape{}, c.installation(map[string]any{
		"checked": conversations, "cached": conversations, "restored": 0, "dropped": 0, "skipped": 0, "cancelled": "false",
		"trajectories_in_cascade_map": 0, "trajectories_in_cascade_map_start": 0, "duration_ms": between(0, 1),
	}))
	if conversations > 0 {
		c.hubEvent("conversation_search_index_warm", eventShape{}, map[string]any{"convo_count": conversations, "duration_ms": 0, "extracted_count": 0, "db_size_bytes": 4096 * between(conversations+4, 2*conversations+8)})
	}
	c.hubEvent("LS_BINARY_STARTUP", eventShape{}, map[string]any{"durationMs": c.sinceLaunch()})
	c.hubEvent("summary_store_json_vs_sqlite", eventShape{}, map[string]any{
		"json_count": conversations, "store_count": conversations, "raw_summary_count": conversations, "raw_summary_bytes": conversations * between(285, 314),
		"count_delta": 0, "missing_in_store": 0, "extra_in_store": 0, "field_mismatch_count": 0, "missing_settled": 0,
		"db_size_bytes": 53248 + conversations*between(4300, 10900), "store_read_error": "false", "store_read_error_reason": "",
	})
}

// startupConfig 包装冷启动中的 fetchConfig：第二次开始前记录迁移事件
func (c *Client) startupConfig(ctx context.Context, progress *startup) error {
	if progress.configs.Add(1) == 2 {
		c.hubMigrations()
	}
	return c.fetchConfig(ctx)
}

// coldStarted 返回虚拟进程是否以冷启动序列初始化
func (c *Client) coldStarted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hub.cold
}

// hubInterface 按官方界面加载的先后与间隔记录侧栏、项目与输入区事件，加载项目列表时读取 cascadeNuxes 并此后每 120 s 读取一次，两次统计侧栏会话数各在 60 s 后再统计一次；ctx 结束时返回
func (c *Client) hubInterface(ctx context.Context) {
	conversations := c.conversations()
	shift := between(63, 83)
	since := func() int { return c.sinceLaunch() - shift }
	wait := func(low, high int) bool {
		select {
		case <-time.After(time.Duration(between(low, high)) * time.Millisecond):
			return true
		case <-ctx.Done():
			return false
		}
	}
	interfaceEvent := func(name string, env bool, params map[string]any) time.Time {
		return c.hubEvent(name, eventShape{flags: true, ui: true, env: env}, params)
	}
	sidebar := func(count int) time.Time {
		return interfaceEvent("sidebar_conversations", false, map[string]any{"conversationCount": count})
	}
	grouped := func(count int) time.Time {
		return interfaceEvent("sidebar_conversations", true, map[string]any{"conversationCount": count, "orphanedConversationCount": 0, "projectlessConversationCount": 0, "mode": "agentManager"})
	}
	group := func() {
		interfaceEvent("sidebar_group_by_state", true, map[string]any{"groupBy": "project", "mode": "agentManager"})
	}
	if !wait(297, 370) {
		return
	}
	c.hubEvent("summaries_store_subscribe_called", eventShape{}, nil)
	if !wait(24, 27) {
		return
	}
	counted := sidebar(0)
	if !wait(35, 40) {
		return
	}
	c.hubEvent("projects_listed", eventShape{blank: true, flags: true}, map[string]any{"num_projects": 1, "project_ids": "outside-of-project"})
	if !wait(24, 25) {
		return
	}
	load := between(84, 93)
	interfaceEvent("trajectory_summary_load", false, map[string]any{"durationMs": load})
	if !wait(2, 3) {
		return
	}
	interfaceEvent("sidebar_load", false, map[string]any{"durationMs": load - between(21, 24)})
	if conversations > 0 {
		if !wait(158, 160) {
			return
		}
		interfaceEvent("sidebar_conversation_list_loaded", false, map[string]any{"conversationCount": conversations, "latencyMs": since()})
	}
	if !wait(1, 2) {
		return
	}
	group()
	regrouped := grouped(conversations)
	if !wait(20, 21) {
		return
	}
	interfaceEvent("input_area_rendered", false, map[string]any{"iframeLocalTtirMs": since()})
	if !wait(153, 161) {
		return
	}
	group()
	if !wait(2, 2) {
		return
	}
	interfaceEvent("project_list_load", false, map[string]any{"durationMs": between(398, 414)})
	go func() {
		for {
			c.nuxes(ctx)
			select {
			case <-time.After(nuxPeriod):
			case <-ctx.Done():
				return
			}
		}
	}()
	for _, next := range []struct {
		at    time.Time
		count func(int) time.Time
	}{{counted, sidebar}, {regrouped, grouped}} {
		select {
		case <-time.After(time.Until(next.at.Add(sidebarRefresh + time.Duration(between(0, 2))*time.Millisecond))):
		case <-ctx.Done():
			return
		}
		next.count(c.conversations())
	}
}
