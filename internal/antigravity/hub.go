package antigravity

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"slices"
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

// hub 表示虚拟 Hub 的本地状态、保存函数、language_server 启动时刻、界面会话标识、是否冷启动、最早一个带实验开关的 Hub 事件的时刻、启动耗时与启动耗时事件的记录时刻、冷启动 userinfo 的发出时刻、最近一次 fetchUserInfo 的结束时刻、是否已加载过会话、各会话的进度、待上报的 clearcut 事件、本虚拟进程第一次生成前是否已读取插件市场目录、待记录的目录读取事件、下一个新会话是否列出远程插件、是否轮询远程列表、改写栈指针的偏移与最近一次解析会话配置的 goroutine 编号
type hub struct {
	state     HubState
	save      func(HubState) error
	launched  time.Time
	browser   string
	cold      bool
	flagged   time.Time
	duration  int
	booted    time.Time
	profiled  time.Time
	refreshed time.Time
	loaded    bool
	sessions  map[string]*sessionTrack
	queue     [][]byte
	fetched   bool
	market    []marketplaceEvent
	plugins   bool
	remote    bool
	shift     stackShift
	goroutine int
}

// eventShape 表示 clearcut 事件的外层形态：blank 为真时公共参数留空，flags 为真时附实验开关，empty 为真时附空的开关字段，ui 为真时带界面会话与事件时刻，env 为真时载荷带客户端环境，compared 为真时开关取对照用的 listExperiments 开关
type eventShape struct {
	blank, flags, empty, ui, env, compared bool
}

// SetHub 设置虚拟 Hub 的本地状态与保存函数
func (c *Client) SetHub(state HubState, save func(HubState) error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hub.state, c.hub.save = state, save
}

// launch 记下虚拟进程启动：started 为 language_server 的启动时刻，界面会话标识随机生成，第一次生成前重新读取插件市场目录，新会话列出远程插件，栈指针偏移按 newStackShift 取，界面渲染时解析会话配置的 goroutine 编号为 1007–1244（官方 2.31.x 冷启动）
func (c *Client) launch(started time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hub.launched, c.hub.browser, c.hub.fetched, c.hub.plugins = started, uuid.NewString(), false, true
	c.hub.shift = newStackShift(c.device)
	c.hub.goroutine = between(1007, 1244)
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

// log 把一个 clearcut 事件按事件时刻插入上报队列，时刻相同的排在已有事件之后；队列已有 clearcutBacklog 个事件时丢弃该事件
func (c *Client) log(event []byte) {
	at := eventMillis(event)
	c.mu.Lock()
	if len(c.hub.queue) < clearcutBacklog {
		index := len(c.hub.queue)
		for index > 0 && eventMillis(c.hub.queue[index-1]) > at {
			index--
		}
		c.hub.queue = slices.Insert(c.hub.queue, index, event)
	}
	c.mu.Unlock()
}

// eventMillis 返回 clearcut 事件开头第 1 字段的事件时刻毫秒数
func eventMillis(event []byte) uint64 {
	at, _ := binary.Uvarint(event[1:])
	return at
}

// flushClearcut 把队列前部最多 clearcutBatch 个事件合成一个 LogRequest，以账户 Bearer 经 HTTP/2 上报，队列为空时不发；连接失败或返回 408、429、5xx 时这些事件留在队列前部随下一次上报重发，其余状态码时把发出的这些事件移出队列，上报期间新插入的事件留在队列中
func (c *Client) flushClearcut(ctx context.Context) {
	c.mu.Lock()
	events := slices.Clone(c.hub.queue[:min(len(c.hub.queue), clearcutBatch)])
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
	sent := make(map[*byte]bool, len(events))
	for _, event := range events {
		sent[&event[0]] = true
	}
	c.mu.Lock()
	c.hub.queue = slices.DeleteFunc(c.hub.queue, func(event []byte) bool { return sent[&event[0]] })
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
	if shape.flags && (c.hub.flagged.IsZero() || at.Before(c.hub.flagged)) {
		c.hub.flagged = at
	}
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

// startupRegions 为 LS_BINARY_STARTUP 中耗时为 0 的启动阶段
var startupRegions = []string{
	"LoadVoiceContextFiles", "RunMigrations", "SetUpCompletionProvider", "SetUpConversationAnnotationsManager", "SetUpCustomizationOptions", "SetUpExtraFragmentEnv",
	"SetUpImplicitTrajectoryManager", "SetUpImplicitTrajectorySaver", "SetUpInstallationId", "SetUpInstanceCheckin", "SetUpLspOrFileWatcher", "SetUpMetadataProvider",
	"SetUpTelemetryErrorReporter", "SetUpWorkspaceManager",
}

// hubStartup 在冷启动第一次 fetchUserInfo 之后同一时刻记录安装状态、用户配置与 Hub 存储加载、会话摘要核对、language_server 启动耗时与摘要订阅事件，并记下启动耗时与记录时刻：client_event_time_ms 为各事件在 language_server 启动后实际发生的时刻，三个存储加载在 34–111 ms 内相隔 0–2 ms，会话摘要核对在第一个存储加载之后 0–313 ms，启动耗时事件在各阶段耗时之和再加 18–35 ms 处，摘要订阅在 1127–2769 ms，事件按实际时刻排列
func (c *Client) hubStartup() time.Time {
	conversations := c.conversations()
	c.mu.Lock()
	launched := c.hub.launched
	c.mu.Unlock()
	at := time.Now()
	moment := func(offset int) map[string]any {
		return map[string]any{"client_event_time_ms": int(launched.UnixMilli()) + offset}
	}
	with := func(params map[string]any, extra map[string]any) map[string]any {
		for key, value := range extra {
			params[key] = value
		}
		return params
	}
	loaded := between(34, 111)
	offsets := []int{loaded, loaded + between(0, 2)}
	offsets = append(offsets, offsets[1]+between(0, 2))
	stores := []map[string]any{{"file_type": "app_state"}, {"file_type": "user_config", "auto_execution_policy": "CASCADE_COMMANDS_AUTO_EXECUTION_OFF"}, {}}
	reconcile := map[string]any{"duration_ms": between(5, 317), "outcome": "success", "rows": conversations, "trigger": "startup", "pruned": 0}
	if conversations == 0 {
		reconcile = map[string]any{"duration_ms": 0, "outcome": "empty", "rows": 0, "trigger": "empty_on_open", "pruned": 0}
	}
	reconciled, pending := loaded+between(0, 313), true
	for index, store := range stores {
		c.hubEventAt(at, "hub_store_load_succeeded", eventShape{blank: true}, c.installation(with(moment(offsets[index]), store)))
		if pending && (index == len(stores)-1 || reconciled < offsets[index+1]) {
			c.hubEventAt(at, "summary_store_reconcile", eventShape{}, with(moment(reconciled), reconcile))
			pending = false
		}
	}
	regions := map[string]int{
		"SetUpHttpServeMux": between(66, 326), "SetUpSummariesStore": between(13, 60), "SetUpStateStore": between(5, 32), "SetUpClients": between(3, 25), "SetUpCascadeManager": between(1, 64),
		"StartSidecarManager": between(0, 241), "SetUpMcpManager": between(0, 14), "PopulateBuiltins": between(0, 21), "SetUpProjectsStore": between(0, 7), "SetUpTrajectorySaver": between(0, 2),
		"WatchPluginEnablementForDataDirs": between(0, 7), "WatchPluginEnablementForMcp": between(0, 10),
	}
	duration := between(18, 35)
	params := map[string]any{}
	for region, spent := range regions {
		params["region"+region+"Ms"] = spent
		duration += spent
	}
	for _, region := range startupRegions {
		params["region"+region+"Ms"] = 0
	}
	params["durationMs"] = duration
	c.hubEventAt(at, "LS_BINARY_STARTUP", eventShape{}, with(moment(duration), params))
	c.hubEventAt(at, "summaries_store_subscribe_called", eventShape{}, moment(between(1127, 2769)))
	c.mu.Lock()
	c.hub.duration, c.hub.booted = duration, at
	c.mu.Unlock()
	return at
}

// hubMigrations 记录冷启动带实验开关的迁移、摘要订阅、摘要核对、搜索索引预热与摘要存储对照事件，后四个事件的先后按 migrationOrders 随机取一种；实验开关为空（listExperiments 失败）时以 retroactive_migration_skipped_feature_disabled 代替前两个迁移事件
func (c *Client) hubMigrations() {
	conversations := c.conversations()
	c.mu.Lock()
	disabled := len(c.state.ccpa.Flags) == 0
	c.mu.Unlock()
	if disabled {
		c.hubEvent("retroactive_migration_skipped_feature_disabled", eventShape{blank: true, flags: true}, nil)
	} else {
		c.hubEvent("retroactive_migration_attempted", eventShape{blank: true, flags: true}, nil)
		c.hubEvent("retroactive_migration_skipped_already_completed", eventShape{blank: true, flags: true}, map[string]any{"status": "RETROACTIVE_MIGRATION_STATUS_COMPLETED_UNNECESSARY"})
	}
	c.hubEvent("projects_migration_skipped_already_completed", eventShape{blank: true, flags: true}, map[string]any{"status": "MIGRATION_STATUS_COMPLETED"})
	events := map[byte]func(){
		's': func() { c.hubEvent("summaries_store_subscribe_called", eventShape{flags: true}, nil) },
		'r': func() {
			c.hubEvent("summaries_reconciled", eventShape{flags: true}, c.installation(map[string]any{
				"checked": conversations, "cached": conversations, "restored": 0, "dropped": 0, "skipped": 0, "cancelled": "false",
				"trajectories_in_cascade_map": 0, "trajectories_in_cascade_map_start": 0, "duration_ms": 0,
			}))
		},
		'j': func() {
			c.hubEvent("summary_store_json_vs_sqlite", eventShape{flags: true}, map[string]any{
				"json_count": conversations, "store_count": conversations, "raw_summary_count": conversations, "raw_summary_bytes": conversations * between(285, 314),
				"count_delta": 0, "missing_in_store": 0, "extra_in_store": 0, "field_mismatch_count": 0, "missing_settled": 0,
				"db_size_bytes": 81920 + conversations*between(3400, 8300), "store_read_error": "false", "store_read_error_reason": "", "raw_summary_missing": 0, "raw_summary_drift": 0,
			})
		},
		'w': func() {
			c.hubEvent("conversation_search_index_warm", eventShape{flags: true}, c.searchIndex(conversations))
		},
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

// hubLogin 记录登录序列的迁移、摘要订阅、sidecar 启动、搜索索引预热与权限迁移事件，前五个的开关字段留空，后两个带实验开关
func (c *Client) hubLogin() {
	c.hubEvent("retroactive_migration_attempted", eventShape{blank: true, empty: true}, nil)
	c.hubEvent("retroactive_migration_skipped_already_completed", eventShape{blank: true, empty: true}, map[string]any{"status": "RETROACTIVE_MIGRATION_STATUS_COMPLETED_UNNECESSARY"})
	c.hubEvent("projects_migration_skipped_already_completed", eventShape{blank: true, empty: true}, map[string]any{"status": "MIGRATION_STATUS_COMPLETED"})
	c.hubEvent("summaries_store_subscribe_called", eventShape{empty: true}, nil)
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	c.log(state.structuredEvent(time.Now(), c.device, eventShape{empty: true}, 25, 26, sidecarPayload("sidecar_execution", "sidecar_id", "sidecar_path", "sidecar_file_path", "command")))
	c.hubEvent("conversation_search_index_warm", eventShape{flags: true}, c.searchIndex(c.conversations()))
	c.hubEvent("app_migration_permission_grants_v2_completed", eventShape{blank: true, flags: true}, map[string]any{
		"projects_total": 1, "projects_migrated": 0, "projects_failed": 0, "global_failed": "false", "deduped": 0, "dropped_allow": 0, "invalid": 0,
		"rewritten_allow": 0, "rewritten_deny": 0, "rewritten_ask": 0,
	})
}

// hubOnboarded 记录登录引导结束后界面渲染的分组方式、分组会话数与只带 iframeLocalTtirMs 的输入区渲染事件，返回输入区渲染的时刻
func (c *Client) hubOnboarded() time.Time {
	c.hubEvent("sidebar_group_by_state", eventShape{flags: true, ui: true, env: true}, map[string]any{"groupBy": "project", "mode": "agentManager", "project_4k_mode": "default"})
	c.sidebarGrouped(c.conversations())
	return c.hubEvent("input_area_rendered", eventShape{flags: true, ui: true}, map[string]any{"iframeLocalTtirMs": c.sinceLaunch()})
}

// coldStarted 返回虚拟进程是否以冷启动序列初始化
func (c *Client) coldStarted() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hub.cold
}

// loadOrders 为界面加载各事件的先后，最后都是侧栏加载与项目列表加载：a 依次为摘要加载、项目列表、会话数统计与带项目标识的项目列表，b 依次为不带开关的项目列表、会话数统计、带项目标识的项目列表与摘要加载，c 依次为摘要加载、会话数统计、项目列表与带项目标识的项目列表；会话数统计之后随即记录 sidecar 启动事件；按官方 2.31.1 的 10 次冷启动出现的次数各列一次
var loadOrders = []byte("aaaaabccc")

// clusterOrders 为界面加载末尾会话列表加载完成、分组方式与分组会话数统计三个事件的先后：l 列表加载完成、g 分组方式、c 分组会话数，取官方 2.30.0 冷启动中出现过的四种
var clusterOrders = []string{"lgc", "lcg", "cgl", "clg"}

// hubInterface 按官方界面加载的先后与间隔记录侧栏、项目与输入区事件：启动事件之后 376–457 ms 起按 loadOrders 取一种先后记录界面加载，1.24–1.65 s 记录 clusterOrders 的三个事件，再过 12–40 ms 记录输入区渲染并按 slashErrors 记录 GetSlashCommands 的查询失败，渲染后 13–139 ms 读取 cascadeNuxes 并此后每 120 s 读取一次，第一次读取的时刻之后 −4–320 ms 列出远程插件，再过 145–469 ms 读取插件市场目录；首次会话数统计 60 s 后再统计一次，此后每过 statusRefresh 按 refreshSettings 读取账户设置，第一次同时记录分组方式与分组会话数；ctx 结束时返回
func (c *Client) hubInterface(ctx context.Context) {
	conversations := c.conversations()
	shift := between(83, 143)
	since := func() int { return c.sinceLaunch() - shift }
	wait := func(low, high int) bool { return pause(ctx, low, high) == nil }
	interfaceEvent := func(name string, env bool, params map[string]any) time.Time {
		return c.hubEvent(name, eventShape{flags: true, ui: true, env: env}, params)
	}
	c.mu.Lock()
	booted, duration := c.hub.booted, c.hub.duration
	c.mu.Unlock()
	if until(ctx, booted.Add(time.Duration(between(376, 457))*time.Millisecond)) != nil {
		return
	}
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
		c.sessionEvent(time.Now(), 25, 26, sidecarPayload("sidecar_execution", "sidecar_id", "sidecar_path", "sidecar_file_path", "command"))
	}
	summaries := func(duration int) func() {
		return func() { interfaceEvent("trajectory_summary_load", false, map[string]any{"durationMs": duration}) }
	}
	projects := func() { interfaceEvent("project_list_load", false, map[string]any{"durationMs": between(174, 282)}) }
	sidebar := func(duration int) func() {
		return func() { interfaceEvent("sidebar_load", false, map[string]any{"durationMs": duration}) }
	}
	type step struct {
		low, high int
		record    func()
	}
	var steps []step
	switch loadOrders[between(0, len(loadOrders)-1)] {
	case 'a':
		steps = []step{{0, 0, summaries(between(860, 1598))}, {2, 9, listed(true)}, {0, 10, count}, {2, 22, identified}}
	case 'b':
		steps = []step{{0, 0, listed(false)}, {0, 1, count}, {6, 9, identified}, {5, 7, summaries(between(860, 1598))}}
	default:
		steps = []step{{0, 0, summaries(between(860, 1598))}, {1, 6, count}, {0, 2, listed(true)}, {6, 11, identified}}
	}
	steps = append(steps, step{119, 253, sidebar(between(97, 213))}, step{31, 68, projects})
	for _, step := range steps {
		if !wait(step.low, step.high) {
			return
		}
		step.record()
	}
	if until(ctx, booted.Add(time.Duration(between(1242, 1645))*time.Millisecond)) != nil {
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
	if !wait(12, 40) {
		return
	}
	rendered := since()
	electron, process, serving, index, html := between(369, 610), between(214, 382), between(5, 21), between(2, 4), between(883, 1270)
	total := rendered + electron + process + between(84, 145)
	renderedAt := interfaceEvent("input_area_rendered", false, map[string]any{
		"iframeLocalTtirMs": rendered, "endToEndTtirMs": total, "startupColdLaunch": "true", "phaseElectronToLsMs": electron, "phaseLsProcessToInitMs": process, "phaseLsInitMs": duration,
		"phaseLsServingToIndexMs": serving, "phaseIndexToHtmlMs": index, "phaseHtmlToDomReadyMs": html, "phaseDomReadyToInputMs": total - electron - process - duration - serving - index - html,
	})
	go c.slashErrors(ctx, renderedAt)
	nuxed := renderedAt.Add(time.Duration(between(13, 139)) * time.Millisecond)
	go func() {
		for next := nuxed; until(ctx, next) == nil; next = next.Add(nuxPeriod) {
			c.nuxes(ctx)
		}
	}()
	if until(ctx, nuxed.Add(time.Duration(between(-4, 320))*time.Millisecond)) != nil {
		return
	}
	go c.listPlugins(ctx)
	if wait(145, 469) {
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
