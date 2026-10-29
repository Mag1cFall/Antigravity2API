package antigravity

import (
	"bytes"
	"context"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// clearcutPeriod 为 language_server 上报 clearcut 队列的间隔，从上一次上报结束开始计时
	clearcutPeriod = 5 * time.Second
	// clearcutOffset 为 clearcut 上报循环相对虚拟进程启动的偏移
	clearcutOffset = 700 * time.Millisecond
	// clearcutBatch 为一次 clearcut 上报最多带的事件数
	clearcutBatch = 200
	// sidebarRefresh 为界面定时统计侧栏会话数的间隔
	sidebarRefresh = time.Minute
	// searchIndexPages 为会话搜索索引数据库的最少页数
	searchIndexPages = 12
)

// HubState 是虚拟 Hub 保存在本地的状态，Conversations 为该账户经服务开始过的会话数，Model 为模型选择器中最后选定的模型枚举，Marketplaces 为按目录名保存的插件市场目录
type HubState struct {
	Conversations int                         `json:"conversations"`
	Model         string                      `json:"model,omitempty"`
	Marketplaces  map[string]MarketplaceCache `json:"marketplaces,omitempty"`
}

// hub 表示虚拟 Hub 的本地状态、保存函数、language_server 启动时刻、界面会话标识、是否冷启动、冷启动迁移组的记录时刻、是否已加载过会话、各会话的进度、待上报的 clearcut 事件、本虚拟进程是否已读取插件市场目录与待记录的目录读取事件
type hub struct {
	state    HubState
	save     func(HubState) error
	launched time.Time
	browser  string
	cold     bool
	migrated time.Time
	loaded   bool
	sessions map[string]*sessionTrack
	queue    [][]byte
	fetched  bool
	market   []marketplaceEvent
}

// eventShape 表示 clearcut 事件的外层形态：bare 为真时套餐与地区留空，blank 为真时公共参数留空，flags 为真时附实验开关，empty 为真时附空的开关字段，ui 为真时带界面会话与事件时刻，env 为真时载荷带客户端环境，compared 为真时开关取对照用的 listExperiments 开关
type eventShape struct {
	bare, blank, flags, empty, ui, env, compared bool
}

// SetHub 设置虚拟 Hub 的本地状态与保存函数
func (c *Client) SetHub(state HubState, save func(HubState) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hub.state, c.hub.save = state, save
}

// launch 记下虚拟进程启动：language_server 在首个 unleash 请求前 416–530 ms 启动，界面会话标识随机生成，插件市场目录等首次生成时再读取
func (c *Client) launch(started time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hub.launched, c.hub.browser, c.hub.fetched = started.Add(-time.Duration(between(416, 530))*time.Millisecond), uuid.NewString(), false
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

// flushClearcut 把队列前部最多 clearcutBatch 个事件合成一个 LogRequest，以账户 Bearer 经 HTTP/2 上报，队列为空时不发；连接失败或返回 408、429、5xx 时这些事件留在队列前部随下一次上报重发，其余状态码时移出队列
func (c *Client) flushClearcut(ctx context.Context) {
	c.mu.Lock()
	events := c.hub.queue[:min(len(c.hub.queue), clearcutBatch)]
	c.mu.Unlock()
	if len(events) == 0 {
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointClearcut, bytes.NewReader(clearcutRequest(events, time.Now())))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	status, err := c.discard(c.auth, request)
	if err != nil || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return
	}
	c.mu.Lock()
	c.hub.queue = c.hub.queue[len(events):]
	c.mu.Unlock()
}

// Clearcut 在虚拟进程启动 5.7 s 后首次上报队列中的事件，此后每次上报结束 5 s 后再上报，ctx 结束时返回
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
	env = appendBytes(env, 3, []byte("desktop"))
	env = appendBytes(env, 4, []byte("external"))
	env = appendBytes(env, 5, []byte(strings.ToLower(device.OS)))
	return appendBytes(env, 7, []byte("unknown"))
}

// hubEvent 把一个此刻的 Hub 事件放入上报队列并返回其时刻
func (c *Client) hubEvent(name string, shape eventShape, params map[string]any) time.Time {
	return c.hubEventAt(time.Now(), name, shape, params)
}

// hubEventAt 把一个时刻为 at 的 Hub 事件放入上报队列并返回其时刻，界面事件的 client_event_time_ms 比 language_server 收到时早 1–4 ms
func (c *Client) hubEventAt(at time.Time, name string, shape eventShape, params map[string]any) time.Time {
	c.mu.Lock()
	state, browser := c.state, c.hub.browser
	c.mu.Unlock()
	params = hubParams(c.device, shape.blank, params)
	if shape.ui {
		params["browserSessionId"] = browser
		params["client_event_time_ms"] = int(at.UnixMilli()) - between(1, 4)
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

// hubLoaded 记录冷启动第一次 fetchUserInfo 之后的安装状态加载、用户配置与 Hub 存储加载，会话摘要核对按官方 2.28.1 的 11 次冷启动以 3:6:1:1 插在三者之前、安装状态之后、用户配置之后或最后，安装状态与会话摘要核对不带套餐与地区
func (c *Client) hubLoaded() {
	conversations := c.conversations()
	reconcile := map[string]any{"duration_ms": between(3, 6), "outcome": "success", "rows": conversations, "trigger": "startup", "pruned": 0}
	if conversations == 0 {
		reconcile = map[string]any{"duration_ms": 0, "outcome": "empty", "rows": 0, "trigger": "empty_on_open", "pruned": 0}
	}
	stores := []func(){
		func() {
			c.hubEvent("hub_store_load_succeeded", eventShape{bare: true, blank: true}, c.installation(map[string]any{"file_type": "app_state"}))
		},
		func() {
			c.hubEvent("hub_store_load_succeeded", eventShape{blank: true}, c.installation(map[string]any{"file_type": "user_config", "auto_execution_policy": "CASCADE_COMMANDS_AUTO_EXECUTION_OFF"}))
		},
		func() {
			c.hubEvent("hub_store_load_succeeded", eventShape{blank: true}, c.installation(map[string]any{}))
		},
	}
	slot := []int{0, 0, 0, 1, 1, 1, 1, 1, 1, 2, 3}[between(0, 10)]
	for index, store := range stores {
		if index == slot {
			c.hubEvent("summary_store_reconcile", eventShape{bare: true}, reconcile)
		}
		store()
	}
	if slot == len(stores) {
		c.hubEvent("summary_store_reconcile", eventShape{bare: true}, reconcile)
	}
}

// hubMigrations 记录冷启动的迁移、摘要订阅、摘要核对、摘要存储对照、启动耗时与搜索索引预热事件并记下搜索索引预热的时刻，后五个事件的先后按 migrationOrders 随机取一种；实验开关为空（listExperiments 失败）时以 retroactive_migration_skipped_feature_disabled 代替前两个迁移事件
func (c *Client) hubMigrations() {
	conversations := c.conversations()
	c.mu.Lock()
	disabled := len(c.state.ccpa.Flags) == 0
	c.mu.Unlock()
	if disabled {
		c.hubEvent("retroactive_migration_skipped_feature_disabled", eventShape{blank: true}, nil)
	} else {
		c.hubEvent("retroactive_migration_attempted", eventShape{blank: true}, nil)
		c.hubEvent("retroactive_migration_skipped_already_completed", eventShape{blank: true}, map[string]any{"status": "RETROACTIVE_MIGRATION_STATUS_COMPLETED_UNNECESSARY"})
	}
	c.hubEvent("projects_migration_skipped_already_completed", eventShape{blank: true}, map[string]any{"status": "MIGRATION_STATUS_COMPLETED"})
	var migrated time.Time
	events := map[byte]func(){
		's': func() { c.hubEvent("summaries_store_subscribe_called", eventShape{}, nil) },
		'r': func() {
			c.hubEvent("summaries_reconciled", eventShape{}, c.installation(map[string]any{
				"checked": conversations, "cached": conversations, "restored": 0, "dropped": 0, "skipped": 0, "cancelled": "false",
				"trajectories_in_cascade_map": 0, "trajectories_in_cascade_map_start": 0, "duration_ms": between(0, 1),
			}))
		},
		'j': func() {
			c.hubEvent("summary_store_json_vs_sqlite", eventShape{}, map[string]any{
				"json_count": conversations, "store_count": conversations, "raw_summary_count": conversations, "raw_summary_bytes": conversations * between(285, 314),
				"count_delta": 0, "missing_in_store": 0, "extra_in_store": 0, "field_mismatch_count": 0, "missing_settled": 0,
				"db_size_bytes": 53248 + conversations*between(4300, 10900), "store_read_error": "false", "store_read_error_reason": "", "raw_summary_missing": 0, "raw_summary_drift": 0,
			})
		},
		'l': func() { c.hubEvent("LS_BINARY_STARTUP", eventShape{}, map[string]any{"durationMs": c.sinceLaunch()}) },
		'w': func() {
			migrated = c.hubEvent("conversation_search_index_warm", eventShape{}, c.searchIndex(conversations))
		},
	}
	for _, event := range migrationOrders[between(0, len(migrationOrders)-1)] {
		events[byte(event)]()
	}
	c.mu.Lock()
	c.hub.migrated = migrated
	c.mu.Unlock()
}

// migrationOrders 为冷启动迁移组后五个事件的先后：s 摘要订阅、r 摘要核对、j 摘要存储对照、l 启动耗时、w 搜索索引预热，按官方 2.28.1 的 6 次冷启动出现的次数各列一次
var migrationOrders = []string{"rsjlw", "rsjwl", "srljw", "srwlj", "srjlw", "srjlw"}

// searchIndex 返回会话搜索索引预热事件的参数，数据库页数随会话数增长，不少于 searchIndexPages
func (c *Client) searchIndex(conversations int) map[string]any {
	return map[string]any{"convo_count": conversations, "duration_ms": 0, "extracted_count": 0, "db_size_bytes": 4096 * max(searchIndexPages, between(conversations+4, 2*conversations+8))}
}

// hubLogin 记录登录序列中对照事件之前的迁移、摘要订阅、搜索索引预热与权限迁移事件，前五个的开关字段留空；权限迁移事件在 fetchConfig 之前生成，use-aicode-mendel 开启时开关字段同样留空
func (c *Client) hubLogin() {
	mendel := c.routed()
	c.hubEvent("retroactive_migration_attempted", eventShape{blank: true, empty: true}, nil)
	c.hubEvent("retroactive_migration_skipped_already_completed", eventShape{blank: true, empty: true}, map[string]any{"status": "RETROACTIVE_MIGRATION_STATUS_COMPLETED_UNNECESSARY"})
	c.hubEvent("projects_migration_skipped_already_completed", eventShape{blank: true, empty: true}, map[string]any{"status": "MIGRATION_STATUS_COMPLETED"})
	c.hubEvent("summaries_store_subscribe_called", eventShape{empty: true}, nil)
	c.hubEvent("conversation_search_index_warm", eventShape{empty: true}, c.searchIndex(c.conversations()))
	c.hubEvent("app_migration_permission_grants_v2_completed", eventShape{blank: true, flags: !mendel, empty: mendel}, map[string]any{
		"projects_total": 1, "projects_migrated": 0, "projects_failed": 0, "global_failed": "false", "deduped": 0, "dropped_allow": 0, "invalid": 0,
		"rewritten_allow": 0, "rewritten_deny": 0, "rewritten_ask": 0,
	})
}

// coldStarted 返回虚拟进程是否以冷启动序列初始化
func (c *Client) coldStarted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hub.cold
}

// hubInterface 按官方界面加载的先后与间隔记录侧栏、项目与输入区事件，从冷启动迁移组之后 0.59–0.68 s 开始；加载项目列表后读取 cascadeNuxes 并此后每 120 s 读取一次，每次读取后记录一次会话面板展示事件，两种侧栏会话数统计此后各约每 60 s 统计一次当时的会话数；ctx 结束时返回
func (c *Client) hubInterface(ctx context.Context) {
	conversations := c.conversations()
	shift := between(63, 83)
	since := func() int { return c.sinceLaunch() - shift }
	until := func(at time.Time) bool {
		select {
		case <-time.After(time.Until(at)):
			return true
		case <-ctx.Done():
			return false
		}
	}
	wait := func(low, high int) bool {
		return until(time.Now().Add(time.Duration(between(low, high)) * time.Millisecond))
	}
	interfaceEvent := func(name string, env bool, params map[string]any) time.Time {
		return c.hubEvent(name, eventShape{flags: true, ui: true, env: env}, params)
	}
	sidebar := func(count int) time.Time {
		return interfaceEvent("sidebar_conversations", false, map[string]any{"conversationCount": count})
	}
	grouped := func(count int) time.Time {
		return interfaceEvent("sidebar_conversations", true, map[string]any{"conversationCount": count, "orphanedConversationCount": 0, "projectlessConversationCount": 0, "pinnedConversationCount": 0, "loadedProjectCount": 1, "knownProjectCount": 1, "mode": "agentManager", "project_4k_mode": "default"})
	}
	c.mu.Lock()
	migrated := c.hub.migrated
	c.mu.Unlock()
	if !until(migrated.Add(time.Duration(between(590, 680)) * time.Millisecond)) {
		return
	}
	c.hubEvent("summaries_store_subscribe_called", eventShape{}, nil)
	if !wait(24, 40) {
		return
	}
	c.hubEvent("projects_listed", eventShape{blank: true}, map[string]any{"num_projects": 1})
	if !wait(1, 7) {
		return
	}
	counted := sidebar(0)
	if !wait(38, 64) {
		return
	}
	c.hubEvent("projects_listed", eventShape{blank: true, flags: true}, map[string]any{"num_projects": 1, "project_ids": "outside-of-project"})
	if !wait(8, 19) {
		return
	}
	load := between(84, 93)
	interfaceEvent("trajectory_summary_load", false, map[string]any{"durationMs": load})
	if !wait(1, 3) {
		return
	}
	loaded := interfaceEvent("sidebar_load", false, map[string]any{"durationMs": load - between(21, 30)})
	if !wait(83, 118) {
		return
	}
	interfaceEvent("project_list_load", false, map[string]any{"durationMs": between(440, 780)})
	go func() {
		if !wait(227, 251) {
			return
		}
		for {
			if c.nuxes(ctx) == nil && wait(60, 70) {
				interfaceEvent("CASCADE_IMPRESSION_EVENT", true, map[string]any{"type": "", "mode": "agentManager", "project_4k_mode": "default"})
			}
			select {
			case <-time.After(nuxPeriod):
			case <-ctx.Done():
				return
			}
		}
	}()
	if !until(loaded.Add(time.Duration(between(258, 282)) * time.Millisecond)) {
		return
	}
	if conversations > 0 {
		interfaceEvent("sidebar_conversation_list_loaded", false, map[string]any{"conversationCount": conversations, "latencyMs": since()})
	}
	if !wait(3, 5) {
		return
	}
	regrouped := grouped(conversations)
	interfaceEvent("sidebar_group_by_state", true, map[string]any{"groupBy": "project", "mode": "agentManager", "project_4k_mode": "default"})
	if !wait(28, 34) {
		return
	}
	rendered := since()
	interfaceEvent("input_area_rendered", false, map[string]any{"iframeLocalTtirMs": rendered, "endToEndTtirMs": rendered + between(580, 776)})
	recounts := []struct {
		at    time.Time
		count func(int) time.Time
	}{{counted.Add(sidebarRefresh + time.Duration(between(460, 660))*time.Millisecond), sidebar}, {regrouped.Add(sidebarRefresh + time.Duration(between(890, 990))*time.Millisecond), grouped}}
	for {
		next := &recounts[0]
		if recounts[1].at.Before(next.at) {
			next = &recounts[1]
		}
		select {
		case <-time.After(time.Until(next.at)):
		case <-ctx.Done():
			return
		}
		next.at = next.count(c.conversations()).Add(sidebarRefresh + time.Duration(between(1, 3))*time.Millisecond)
	}
}
