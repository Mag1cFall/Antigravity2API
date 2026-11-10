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
	// clearcutOffset 为 clearcut 上报循环相对虚拟进程启动的最小偏移，另加 0–200 ms
	clearcutOffset = 130 * time.Millisecond
	// clearcutBatch 为一次 clearcut 上报最多带的事件数
	clearcutBatch = 200
	// clearcutBacklog 为 clearcut 上报队列保留的事件数上限
	clearcutBacklog = 5 * clearcutBatch
	// sidebarRefresh 为界面加载后再次统计侧栏会话数的间隔
	sidebarRefresh = time.Minute
	// statusRefresh 为界面刷新账户设置的间隔，也是账户设置的有效期
	statusRefresh = 5 * time.Minute
	// profileRefresh 为 userinfo 的有效期
	profileRefresh = 10 * time.Minute
	// searchIndexPages 为会话搜索索引数据库的最少页数
	searchIndexPages = 12
)

// HubState 是虚拟 Hub 保存在本地的状态，Conversations 为该账户经服务开始过的会话数，Model 为模型选择器中最后选定的模型枚举，Marketplaces 为按目录名保存的插件市场目录
type HubState struct {
	Conversations int                         `json:"conversations"`
	Model         string                      `json:"model,omitempty"`
	Marketplaces  map[string]MarketplaceCache `json:"marketplaces,omitempty"`
}

// hub 表示虚拟 Hub 的本地状态、保存函数、language_server 启动时刻、界面会话标识、是否冷启动、启动耗时与启动耗时事件的记录时刻、冷启动 userinfo 的发出时刻、最近一次 fetchUserInfo 的结束时刻、是否已加载过会话、各会话的进度、待上报的 clearcut 事件、本虚拟进程第一次生成前是否已读取插件市场目录与待记录的目录读取事件
type hub struct {
	state     HubState
	save      func(HubState) error
	launched  time.Time
	browser   string
	cold      bool
	duration  int
	booted    time.Time
	profiled  time.Time
	refreshed time.Time
	loaded    bool
	sessions  map[string]*sessionTrack
	queue     [][]byte
	fetched   bool
	market    []marketplaceEvent
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

// launch 记下虚拟进程启动：started 为 language_server 的启动时刻，界面会话标识随机生成，第一次生成前重新读取插件市场目录
func (c *Client) launch(started time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hub.launched, c.hub.browser, c.hub.fetched = started, uuid.NewString(), false
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

// log 把一个 clearcut 事件放入上报队列，队列已有 clearcutBacklog 个事件时丢弃该事件
func (c *Client) log(event []byte) {
	c.mu.Lock()
	if len(c.hub.queue) < clearcutBacklog {
		c.hub.queue = append(c.hub.queue, event)
	}
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

// Clearcut 在虚拟进程启动 5.13–5.33 s 后首次上报队列中的事件，此后每次上报结束 5 s 后再上报，ctx 结束时返回
func (m *Companion) Clearcut(ctx context.Context) {
	wait := time.Until(m.started.Add(clearcutOffset + rand.N(200*time.Millisecond) + clearcutPeriod))
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

// hubLoaded 记录冷启动第一次 fetchUserInfo 之后的安装状态加载、用户配置与 Hub 存储加载，会话摘要核对按官方 2.30.0 的 15 次冷启动以 1:13:1 插在三者之前、安装状态之后或最后，安装状态与会话摘要核对不带套餐与地区
func (c *Client) hubLoaded() {
	conversations := c.conversations()
	reconcile := map[string]any{"duration_ms": between(0, 19), "outcome": "success", "rows": conversations, "trigger": "startup", "pruned": 0}
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
	slot := 1
	switch between(0, 14) {
	case 0:
		slot = 0
	case 1:
		slot = len(stores)
	}
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

// startupRegions 为 LS_BINARY_STARTUP 中耗时为 0 的启动阶段
var startupRegions = []string{
	"LoadVoiceContextFiles", "PopulateBuiltins", "RunMigrations", "SetUpCompletionProvider", "SetUpConversationAnnotationsManager", "SetUpCustomizationOptions", "SetUpExtraFragmentEnv",
	"SetUpImplicitTrajectoryManager", "SetUpImplicitTrajectorySaver", "SetUpInstallationId", "SetUpInstanceCheckin", "SetUpLspOrFileWatcher", "SetUpMcpManager", "SetUpMetadataProvider", "SetUpProjectsStore",
	"SetUpTelemetryErrorReporter", "SetUpTrajectorySaver", "SetUpWorkspaceManager", "StartSidecarManager", "WatchPluginEnablementForDataDirs", "WatchPluginEnablementForMcp",
}

// hubStarted 记录 language_server 的启动耗时事件并记下耗时与事件时刻：各阶段中 HTTP 服务 30–50 ms、摘要存储 8–51 ms、客户端 2–3 ms、会话管理 0–1 ms，状态存储占去其余耗时中除 13–21 ms 以外的部分，其他阶段为 0
func (c *Client) hubStarted() time.Time {
	duration := c.sinceLaunch()
	mux, summaries, clients, cascade := between(30, 50), between(8, 51), between(2, 3), between(0, 1)
	params := map[string]any{
		"durationMs": duration, "regionSetUpHttpServeMuxMs": mux, "regionSetUpSummariesStoreMs": summaries, "regionSetUpClientsMs": clients, "regionSetUpCascadeManagerMs": cascade,
		"regionSetUpStateStoreMs": duration - mux - summaries - clients - cascade - between(13, 21),
	}
	for _, region := range startupRegions {
		params["region"+region+"Ms"] = 0
	}
	at := c.hubEvent("LS_BINARY_STARTUP", eventShape{}, params)
	c.mu.Lock()
	c.hub.duration, c.hub.booted = duration, at
	c.mu.Unlock()
	return at
}

// hubMigrations 记录冷启动的迁移、摘要订阅、摘要核对、搜索索引预热与摘要存储对照事件，后四个事件的先后按 migrationOrders 随机取一种；实验开关为空（listExperiments 失败）时以 retroactive_migration_skipped_feature_disabled 代替前两个迁移事件
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
	events := map[byte]func(){
		's': func() { c.hubEvent("summaries_store_subscribe_called", eventShape{}, nil) },
		'r': func() {
			c.hubEvent("summaries_reconciled", eventShape{}, c.installation(map[string]any{
				"checked": conversations, "cached": conversations, "restored": 0, "dropped": 0, "skipped": 0, "cancelled": "false",
				"trajectories_in_cascade_map": 0, "trajectories_in_cascade_map_start": 0, "duration_ms": 0,
			}))
		},
		'j': func() {
			c.hubEvent("summary_store_json_vs_sqlite", eventShape{}, map[string]any{
				"json_count": conversations, "store_count": conversations, "raw_summary_count": conversations, "raw_summary_bytes": conversations * between(285, 314),
				"count_delta": 0, "missing_in_store": 0, "extra_in_store": 0, "field_mismatch_count": 0, "missing_settled": 0,
				"db_size_bytes": 81920 + conversations*between(3400, 8300), "store_read_error": "false", "store_read_error_reason": "", "raw_summary_missing": 0, "raw_summary_drift": 0,
			})
		},
		'w': func() { c.hubEvent("conversation_search_index_warm", eventShape{}, c.searchIndex(conversations)) },
	}
	for _, event := range migrationOrders[between(0, len(migrationOrders)-1)] {
		events[byte(event)]()
	}
}

// migrationOrders 为冷启动迁移组后四个事件的先后：s 摘要订阅、r 摘要核对、j 摘要存储对照、w 搜索索引预热，按官方 2.30.0 的 11 次冷启动出现的次数各列一次
var migrationOrders = []string{"swrj", "swrj", "swrj", "swrj", "swrj", "swrj", "swrj", "swrj", "srwj", "srwj", "srjw"}

// searchIndex 返回会话搜索索引预热事件的参数，数据库页数随会话数增长，不少于 searchIndexPages
func (c *Client) searchIndex(conversations int) map[string]any {
	return map[string]any{"convo_count": conversations, "duration_ms": 0, "extracted_count": 0, "db_size_bytes": 4096 * max(searchIndexPages, between(conversations+4, 2*conversations+8))}
}

// hubLogin 记录登录序列中对照事件之前的迁移、摘要订阅、搜索索引预热与权限迁移事件，前四个的开关字段留空，后两个带实验开关
func (c *Client) hubLogin() {
	c.hubEvent("retroactive_migration_attempted", eventShape{blank: true, empty: true}, nil)
	c.hubEvent("retroactive_migration_skipped_already_completed", eventShape{blank: true, empty: true}, map[string]any{"status": "RETROACTIVE_MIGRATION_STATUS_COMPLETED_UNNECESSARY"})
	c.hubEvent("projects_migration_skipped_already_completed", eventShape{blank: true, empty: true}, map[string]any{"status": "MIGRATION_STATUS_COMPLETED"})
	c.hubEvent("summaries_store_subscribe_called", eventShape{empty: true}, nil)
	c.hubEvent("conversation_search_index_warm", eventShape{flags: true}, c.searchIndex(c.conversations()))
	c.hubEvent("app_migration_permission_grants_v2_completed", eventShape{blank: true, flags: true}, map[string]any{
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

// loadOrders 为界面加载中摘要订阅之后各事件的先后：a 依次为摘要加载、项目列表、会话数统计与带项目标识的项目列表，b 依次为不带开关的项目列表、会话数统计、带项目标识的项目列表、摘要加载与侧栏加载；按官方 2.30.0 的 8 次冷启动出现的次数各列一次
var loadOrders = []byte("aaaaabbb")

// clusterOrders 为界面加载末尾会话列表加载完成、分组方式与分组会话数统计三个事件的先后：l 列表加载完成、g 分组方式、c 分组会话数，取官方 2.30.0 冷启动中出现过的四种
var clusterOrders = []string{"lgc", "lcg", "cgl", "clg"}

// hubInterface 按官方界面加载的先后与间隔记录侧栏、项目与输入区事件，从启动耗时事件之后 0.60–0.79 s 开始：摘要订阅之后的事件按 loadOrders 取一种先后；摘要订阅之后 0.33–0.96 s 记录 clusterOrders 的三个事件与输入区渲染，随后读取 cascadeNuxes 并此后每 120 s 读取一次，第一次读取发出 40–180 ms 后读取插件市场目录；首次会话数统计 60 s 后再统计一次，此后每过 statusRefresh 按 refreshSettings 读取账户设置，第一次同时记录分组方式与分组会话数；ctx 结束时返回
func (c *Client) hubInterface(ctx context.Context) {
	conversations := c.conversations()
	shift := between(48, 111)
	since := func() int { return c.sinceLaunch() - shift }
	wait := func(low, high int) bool { return pause(ctx, low, high) == nil }
	interfaceEvent := func(name string, env bool, params map[string]any) time.Time {
		return c.hubEvent(name, eventShape{flags: true, ui: true, env: env}, params)
	}
	c.mu.Lock()
	booted, duration := c.hub.booted, c.hub.duration
	c.mu.Unlock()
	if until(ctx, booted.Add(time.Duration(between(597, 793))*time.Millisecond)) != nil {
		return
	}
	subscribed := c.hubEvent("summaries_store_subscribe_called", eventShape{}, nil)
	var counted time.Time
	listed := func(flags bool) func() {
		return func() {
			c.hubEvent("projects_listed", eventShape{blank: true, flags: flags}, map[string]any{"num_projects": 1})
		}
	}
	identified := func() {
		c.hubEvent("projects_listed", eventShape{blank: true, flags: true}, map[string]any{"num_projects": 1, "project_ids": "outside-of-project"})
	}
	count := func() {
		counted = interfaceEvent("sidebar_conversations", false, map[string]any{"conversationCount": conversations})
	}
	summaries := func(duration int) func() {
		return func() { interfaceEvent("trajectory_summary_load", false, map[string]any{"durationMs": duration}) }
	}
	projects := func() { interfaceEvent("project_list_load", false, map[string]any{"durationMs": between(70, 134)}) }
	type step struct {
		low, high int
		record    func()
	}
	steps := []step{{20, 38, summaries(between(16, 26))}, {1, 6, listed(true)}, {1, 5, count}, {3, 7, identified}, {65, 171, projects}}
	if loadOrders[between(0, len(loadOrders)-1)] == 'b' {
		load := between(33, 35)
		steps = []step{{16, 17, listed(false)}, {1, 2, count}, {3, 5, identified}, {10, 10, summaries(load)}, {1, 2, func() {
			interfaceEvent("sidebar_load", false, map[string]any{"durationMs": load - 18})
		}}, {67, 68, projects}}
	}
	for _, step := range steps {
		if !wait(step.low, step.high) {
			return
		}
		step.record()
	}
	if until(ctx, subscribed.Add(time.Duration(between(331, 962))*time.Millisecond)) != nil {
		return
	}
	cluster := map[byte]func(){
		'l': func() {
			interfaceEvent("sidebar_conversation_list_loaded", false, map[string]any{"conversationCount": conversations, "latencyMs": since()})
		},
		'g': func() {
			interfaceEvent("sidebar_group_by_state", true, map[string]any{"groupBy": "project", "mode": "agentManager", "project_4k_mode": "default"})
		},
		'c': func() { c.sidebarGrouped(conversations) },
	}
	for _, event := range clusterOrders[between(0, len(clusterOrders)-1)] {
		cluster[byte(event)]()
	}
	if !wait(4, 15) {
		return
	}
	rendered := since()
	electron, process, serving, index, html := between(214, 465), between(145, 258), between(3, 6), between(1, 3), between(594, 791)
	total := rendered + electron + process + between(58, 102)
	interfaceEvent("input_area_rendered", false, map[string]any{
		"iframeLocalTtirMs": rendered, "endToEndTtirMs": total, "startupColdLaunch": "true", "phaseElectronToLsMs": electron, "phaseLsProcessToInitMs": process, "phaseLsInitMs": duration,
		"phaseLsServingToIndexMs": serving, "phaseIndexToHtmlMs": index, "phaseHtmlToDomReadyMs": html, "phaseDomReadyToInputMs": total - electron - process - duration - serving - index - html,
	})
	if !wait(11, 20) {
		return
	}
	go func() {
		for next := time.Now(); until(ctx, next) == nil; next = next.Add(nuxPeriod) {
			c.nuxes(ctx)
		}
	}()
	if wait(40, 180) {
		c.readMarketplaces(ctx)
		c.logMarketplace()
	}
	if until(ctx, counted.Add(sidebarRefresh+time.Duration(between(9, 34))*time.Millisecond)) != nil {
		return
	}
	interfaceEvent("sidebar_conversations", false, map[string]any{"conversationCount": c.conversations()})
	go c.refreshProfile(ctx)
	tick := counted.Add(statusRefresh + time.Duration(between(24, 34))*time.Millisecond)
	if until(ctx, tick) != nil {
		return
	}
	cluster['g']()
	c.sidebarGrouped(c.conversations())
	for wait(2, 2) {
		c.refreshSettings(ctx)
		tick = tick.Add(statusRefresh + time.Duration(between(10, 40))*time.Millisecond)
		if until(ctx, tick) != nil {
			return
		}
	}
}

// sidebarGrouped 记录按项目分组的侧栏会话数统计
func (c *Client) sidebarGrouped(conversations int) {
	c.hubEvent("sidebar_conversations", eventShape{flags: true, ui: true, env: true}, map[string]any{
		"conversationCount": conversations, "orphanedConversationCount": 0, "projectlessConversationCount": 0, "pinnedConversationCount": 0, "loadedProjectCount": 1, "knownProjectCount": 1, "mode": "agentManager", "project_4k_mode": "default",
	})
}

// refreshSettings 在上一次 fetchUserInfo 结束已满 statusRefresh 时重新读取账户设置
func (c *Client) refreshSettings(ctx context.Context) {
	c.mu.Lock()
	stale := time.Since(c.hub.refreshed) >= statusRefresh
	c.mu.Unlock()
	if stale {
		c.FetchUserInfo(ctx)
	}
}

// refreshProfile 在冷启动的 userinfo 发出 profileRefresh 又 1.38–1.60 s 后重新读取 userinfo，同时按 refreshSettings 读取账户设置，userinfo 结束 190–587 ms 后读头像
func (c *Client) refreshProfile(ctx context.Context) {
	c.mu.Lock()
	profiled := c.hub.profiled
	c.mu.Unlock()
	if until(ctx, profiled.Add(profileRefresh+time.Duration(between(1382, 1597))*time.Millisecond)) != nil {
		return
	}
	go c.refreshSettings(ctx)
	if profile, err := c.Userinfo(ctx); err == nil && pause(ctx, 190, 587) == nil {
		c.avatar(ctx, profile.Picture)
	}
}
