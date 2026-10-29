package antigravity

import (
	"cmp"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// sessionJSON 为从官方会话现场生成的会话事件模板
//
//go:embed session.json
var sessionJSON []byte

// sessionTemplate 表示官方会话 clearcut 事件中随客户端版本固定的部分：各模型族的执行配置、请求覆盖配置、代理解析、已加载定制、提示段元数据、执行配置中的实验开关名、内置工具与各模型族的工具 token 数、系统提示段名与 Model、APIProvider 枚举
type sessionTemplate struct {
	Cascade        map[string][]byte `json:"cascade"`
	Override       []byte            `json:"override"`
	Resolution     []byte            `json:"resolution"`
	Customizations []byte            `json:"customizations"`
	PromptSections []byte            `json:"promptSections"`
	Experiments    []string          `json:"experiments"`
	Tools          []string          `json:"tools"`
	ToolTokens     map[string][]int  `json:"toolTokens"`
	Sections       []string          `json:"sections"`
	Models         map[string]int    `json:"models"`
	Providers      map[string]int    `json:"providers"`
}

// sessionFixed 为解析后的会话事件模板
var sessionFixed = func() sessionTemplate {
	var template sessionTemplate
	if err := json.Unmarshal(sessionJSON, &template); err != nil {
		panic(err)
	}
	return template
}()

// SessionSections 为官方系统提示 token 明细中各段的段名
var SessionSections = sessionFixed.Sections

const (
	// stepUser 为用户输入步骤的类型
	stepUser = 14
	// stepPlanner 为模型回复步骤的类型
	stepPlanner = 15
	// stepCommand 为终端命令步骤的类型
	stepCommand = 21
	// stepGeneric 为终端命令步骤内嵌通用步骤的类型
	stepGeneric = 132
	// stepDone 为已完成步骤的状态
	stepDone = 3
	// trajectoryCascade 为 cascade 会话的 trajectory 类型
	trajectoryCascade = 4
	// outsideProject 为不在项目中的会话的项目标识
	outsideProject = "outside-of-project"
	// noToolCall 为执行因模型回复不含工具调用而结束的原因
	noToolCall = "EXECUTOR_TERMINATION_REASON_NO_TOOL_CALL"
	// sessionTrackTTL 为会话进度在最后一次使用后的保留时长
	sessionTrackTTL = 24 * time.Hour
)

// postHooks 为每次模型调用之后依次执行的内置钩子
var postHooks = []string{"command_assessor", "message_continue_check", "terminal_step_check", "max_generator_invocations_check", "empty_output_continuation_check", "force_invocation", "no_tool_call_check"}

// stopHooks 为执行结束前依次执行的内置钩子
var stopHooks = []string{"goal_stop", "idle_subagent_guard"}

// SessionTurn 表示一次生成请求在官方会话中对应的一轮：Contents 为包装后的规范消息，Sections 为官方系统提示各段的 token 数，顺序同 SessionSections，Anchor 为本轮生成前读取了插件市场目录时界面事件对齐的时刻：新会话为标题请求写出的时刻，其余为目录读取的开始时刻，没有读取时为零值
type SessionTurn struct {
	Conversation Conversation
	New          bool
	Model        CatalogModel
	Contents     []Content
	Sections     []int
	Anchor       time.Time
}

// SessionReply 表示一次模型调用的结果：首个事件、最后一个思考事件与结束时刻、上游用量、responseId、traceId、modelVersion 与函数调用
type SessionReply struct {
	First, Thought, Done time.Time
	Usage                Usage
	ResponseID           string
	TraceID              string
	Model                string
	Calls                []FunctionCall
}

// sessionTrack 表示虚拟 language_server 中一条会话的进度：mu 串行化同一会话的事件，used 为最后一次使用时刻，sent 为当前执行的发送时刻，invoked 为当前模型调用的开始时刻，seq 为执行内已用的钩子序号，outputs 为各模型回复步骤的输出 token 数，model 为上一次用户请求的模型，first 与 replied 为该次回复的首个事件与结束时刻
type sessionTrack struct {
	mu             sync.Mutex
	used           time.Time
	sent, invoked  time.Time
	seq            int
	outputs        map[int]int64
	model          string
	first, replied time.Time
}

// sessionStep 表示官方会话中的一个步骤：kind 为步骤类型，tokens 为 token 明细中的数，calls 为模型回复的函数调用，call 为工具步骤对应的调用
type sessionStep struct {
	kind   int
	tokens int64
	calls  []FunctionCall
	call   FunctionCall
}

// sessionSteps 按官方步骤模型展开规范消息：用户请求、模型回复与每个函数结果各占一个步骤，函数结果的步骤带上一次模型回复中同标识同名的调用；用户输入与工具结果按 ceil(UTF-8 字节数/3)+5 计 token，模型回复取记下的输出 token 数，没有记录时按 ceil(字节数/3)
func sessionSteps(contents []Content, outputs map[int]int64) []sessionStep {
	var steps []sessionStep
	var calls []FunctionCall
	for _, content := range contents {
		switch {
		case content.Role == RoleAssistant:
			step := sessionStep{kind: stepPlanner}
			size := 0
			for _, part := range content.Parts {
				if part.FunctionCall != nil {
					step.calls = append(step.calls, *part.FunctionCall)
					size += len(part.FunctionCall.Name) + len(part.FunctionCall.Arguments)
				} else if !part.Thought {
					size += len(part.Text)
				}
			}
			step.tokens = int64(ceilThird(size))
			if output, ok := outputs[len(steps)]; ok {
				step.tokens = output
			}
			calls = step.calls
			steps = append(steps, step)
		case userRequest(content):
			size := 0
			for _, part := range content.Parts {
				size += len(part.Text)
			}
			steps = append(steps, sessionStep{kind: stepUser, tokens: int64(ceilThird(size) + 5)})
		default:
			for _, part := range content.Parts {
				if part.FunctionResult != nil {
					call := FunctionCall{ID: part.FunctionResult.ID, Name: part.FunctionResult.Name}
					if index := slices.IndexFunc(calls, func(c FunctionCall) bool { return c.ID == call.ID && c.Name == call.Name }); index >= 0 {
						call = calls[index]
					}
					steps = append(steps, sessionStep{kind: stepCommand, tokens: int64(ceilThird(len(resultText(part.FunctionResult.Content))) + 5), call: call})
				}
			}
		}
	}
	return steps
}

// resultText 返回函数结果的文本：JSON 字符串取其值，其余取 JSON 原文
func resultText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	return string(content)
}

// ceilThird 返回 ceil(n/3)
func ceilThird(n int) int {
	return (n + 2) / 3
}

// sessionFamily 返回模型所属的执行配置族
func sessionFamily(model CatalogModel) string {
	switch {
	case model.ModelProvider == ProviderAnthropic:
		return "claude"
	case model.ModelProvider == ProviderOpenAI:
		return "gpt"
	case strings.Contains(model.ID, "argon"):
		return "argon"
	}
	return "gemini"
}

// shellName 返回设备系统上官方终端工具使用的 shell
func shellName(os string) string {
	switch os {
	case "windows":
		return "powershell"
	case "darwin":
		return "zsh"
	}
	return "bash"
}

// contentID 返回步骤的 model_api_content_id
func contentID(trajectory string, step int) string {
	return strings.ReplaceAll(uuidOf("content:"+trajectory+":"+strconv.Itoa(step)), "-", "")
}

// botID 返回模型回复步骤的消息标识
func botID(trajectory string, step int) string {
	return "bot-" + uuidOf("bot:"+trajectory+":"+strconv.Itoa(step))
}

// pbSet 把 path 处第一个同号字段换成 node，中间各层按原顺序重新编码
func pbSet(data []byte, path []int, node pbNode) []byte {
	nodes := pbParse(data)
	for i := range nodes {
		if nodes[i].num != path[0] {
			continue
		}
		if len(path) == 1 {
			node.num = path[0]
			nodes[i] = node
		} else {
			nodes[i].raw = pbSet(nodes[i].raw, path[1:], node)
		}
		break
	}
	return pbEncode(nodes)
}

// pbGet 返回 path 处第一个同号字段
func pbGet(data []byte, path ...int) pbNode {
	var node pbNode
	for _, number := range path {
		for _, item := range pbParse(data) {
			if item.num == number {
				node = item
				break
			}
		}
		data = node.raw
	}
	return node
}

// appendTime 追加 Timestamp 字段，纳秒为 0 时省略
func appendTime(b []byte, field int, at time.Time) []byte {
	value := appendVarint(nil, 1, uint64(at.Unix()))
	if nanos := at.Nanosecond(); nanos != 0 {
		value = appendVarint(value, 2, uint64(nanos))
	}
	return appendBytes(b, field, value)
}

// appendDuration 追加 Duration 字段，秒或纳秒为 0 时省略
func appendDuration(b []byte, field int, d time.Duration) []byte {
	var value []byte
	if seconds := int64(d / time.Second); seconds != 0 {
		value = appendVarint(value, 1, uint64(seconds))
	}
	if nanos := int64(d % time.Second); nanos != 0 {
		value = appendVarint(value, 2, uint64(nanos))
	}
	return appendBytes(b, field, value)
}

// appendString 追加字符串字段
func appendString(b []byte, field int, value string) []byte {
	return appendBytes(b, field, []byte(value))
}

// appendInt 追加有符号整数的 varint 字段，负数按 64 位补码
func appendInt(b []byte, field int, value int64) []byte {
	return appendVarint(b, field, uint64(value))
}

// appendSome 追加非零的 varint 字段
func appendSome(b []byte, field int, value int64) []byte {
	if value == 0 {
		return b
	}
	return appendInt(b, field, value)
}

// appendDouble 追加 double 字段
func appendDouble(b []byte, field int, value float64) []byte {
	return binary.LittleEndian.AppendUint64(appendTag(b, field, 1), math.Float64bits(value))
}

// sessionCascade 按模型族模板填写执行配置：模型枚举、输出上限、模型名、终端 shell 与按 mendelFlags 取值的实验开关
func (s companionState) sessionCascade(model CatalogModel, device Device) []byte {
	config := sessionFixed.Cascade[sessionFamily(model)]
	config = pbSet(config, []int{1, 1}, pbNode{wire: 0, u: uint64(sessionFixed.Models[model.Model])})
	config = pbSet(config, []int{1, 6}, pbNode{wire: 0, u: uint64(model.MaxOutputTokens)})
	config = pbSet(config, []int{1, 28}, pbNode{wire: 2, raw: []byte(model.ID)})
	config = pbSet(config, []int{1, 13, 8, 5}, pbNode{wire: 2, raw: []byte(shellName(device.OS))})
	return pbSet(config, []int{1, 43}, pbNode{wire: 2, raw: s.sessionExperiments()})
}

// sessionExperiments 编码执行配置的实验开关 map：开关名取模板，值取 mendelFlags 中的同名开关，布尔值为 bool_value，整数与小数为 number_value，字符串为 string_value，没有该开关时为 false
func (s companionState) sessionExperiments() []byte {
	flags := make(map[string]experimentFlag)
	for _, flag := range s.mendelFlags() {
		flags[flag.Name] = flag
	}
	var out []byte
	for _, name := range sessionFixed.Experiments {
		value := appendVarint(nil, 1, 0)
		switch entry := flags[name]; {
		case entry.BoolValue != nil:
			value = appendVarint(nil, 1, flag(*entry.BoolValue))
		case entry.IntValue != nil:
			number, _ := strconv.ParseFloat(*entry.IntValue, 64)
			value = appendDouble(nil, 3, number)
		case entry.FloatValue != nil:
			value = appendDouble(nil, 3, *entry.FloatValue)
		case entry.StringValue != nil:
			value = appendString(nil, 2, *entry.StringValue)
		}
		out = appendBytes(out, 1, appendBytes(appendString(nil, 1, name), 2, value))
	}
	return out
}

// mendelFlags 返回会话事件所用的实验开关：use-aicode-mendel 开启时为最近一次 fetchConfig，否则为最近一次 listExperiments
func (s companionState) mendelFlags() []experimentFlag {
	if s.routed() {
		return s.aicode.ExperimentConfig.ExperimentFlags
	}
	return s.ccpa.Flags
}

// mendelIDs 编码实验 ID 与 fetchAvailableModels 实验 ID 的升序并集：use-aicode-mendel 开启时实验 ID 取最近一次 fetchConfig，否则取最近一次 listExperiments
func (s companionState) mendelIDs() []byte {
	ids := s.ccpa.ExperimentIDs
	if s.routed() {
		ids = s.aicode.ExperimentConfig.EnabledExperimentIDs
	}
	ids = slices.Concat(ids, s.catalogExperiments)
	slices.Sort(ids)
	var out []byte
	for _, id := range slices.Compact(ids) {
		out = binary.AppendUvarint(out, uint64(id))
	}
	return out
}

// sessionOverride 返回填写模型枚举的请求覆盖配置
func sessionOverride(model CatalogModel) []byte {
	return pbSet(sessionFixed.Override, []int{1, 1}, pbNode{wire: 0, u: uint64(sessionFixed.Models[model.Model])})
}

// structuredEvent 编码带结构化载荷的会话 clearcut 事件：公共字段与名称事件相同并附实验开关，载荷写入扩展的 field，事件码为 kind
func (s companionState) structuredEvent(at time.Time, device Device, kind, field int, payload []byte) []byte {
	ext := appendBytes(s.extension(device, eventShape{flags: true}), field, payload)
	event := appendVarint(nil, 1, uint64(at.UnixMilli()))
	event = appendBytes(event, 6, ext)
	return appendVarint(event, 11, uint64(kind))
}

// sessionEvent 把一个结构化会话事件放入上报队列
func (c *Client) sessionEvent(at time.Time, kind, field int, payload []byte) {
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	c.log(state.structuredEvent(at, c.device, kind, field, payload))
}

// track 返回 trajectory 的会话进度，没有时新建，同时清理超过保留时长未使用的进度
func (c *Client) track(trajectory string, now time.Time) *sessionTrack {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hub.sessions == nil {
		c.hub.sessions = map[string]*sessionTrack{}
	}
	track := c.hub.sessions[trajectory]
	if track == nil {
		for id, old := range c.hub.sessions {
			if now.Sub(old.used) > sessionTrackTTL {
				delete(c.hub.sessions, id)
			}
		}
		track = &sessionTrack{outputs: map[int]int64{}}
		c.hub.sessions[trajectory] = track
	}
	track.used = now
	return track
}

// sessionUI 记录带界面会话与客户端环境的会话界面事件
func (c *Client) sessionUI(at time.Time, name string, params map[string]any) {
	params["mode"], params["project_4k_mode"] = "agentManager", "default"
	c.hubEventAt(at, name, eventShape{flags: true, ui: true, env: true}, params)
}

// stepInfo 编码步骤元数据中的来源步骤信息
func stepInfo(conversation Conversation, index, metadata int) []byte {
	info := appendString(nil, 1, conversation.TrajectoryID)
	info = appendSome(info, 2, int64(index))
	info = appendSome(info, 3, int64(metadata))
	return appendString(info, 4, conversation.CascadeID)
}

// transitions 编码步骤状态变化：依次为状态与时刻
func transitions(changes ...any) []byte {
	var out []byte
	for i := 0; i < len(changes); i += 2 {
		change := appendVarint(nil, 1, uint64(changes[i].(int)))
		change = appendTime(change, 2, changes[i+1].(time.Time))
		out = appendBytes(out, 1, change)
	}
	return out
}

// stepPayload 编码码 11 的步骤事件载荷
func stepPayload(conversation Conversation, step []byte, index, metadata int, execution string) []byte {
	reference := appendString(nil, 1, conversation.TrajectoryID)
	reference = appendSome(reference, 2, int64(index))
	reference = appendVarint(reference, 3, trajectoryCascade)
	payload := appendBytes(nil, 1, step)
	payload = appendBytes(payload, 2, reference)
	payload = appendString(payload, 3, conversation.TrajectoryID)
	payload = appendString(payload, 4, execution)
	payload = appendVarint(payload, 5, uint64(metadata))
	return appendString(payload, 8, conversation.CascadeID)
}

// hookPayload 编码码 37 的钩子事件载荷，duration 为耗时毫秒，step 为 -1 时表示不属于工具步骤
func hookPayload(conversation Conversation, execution, kind, name string, started time.Time, duration, seq, metadata int, step int64, result []byte) []byte {
	payload := appendString(nil, 1, kind)
	payload = appendString(payload, 2, name)
	payload = appendString(payload, 3, "builtin")
	payload = appendString(payload, 4, "success")
	payload = appendVarint(payload, 5, uint64(duration))
	payload = appendVarint(payload, 6, uint64(started.UnixMicro()))
	payload = appendString(payload, 7, conversation.TrajectoryID)
	payload = appendString(payload, 8, execution)
	payload = appendVarint(payload, 9, uint64(seq))
	payload = appendVarint(payload, 10, uint64(metadata))
	payload = appendInt(payload, 11, step)
	if result != nil {
		payload = appendBytes(payload, 12, result)
	}
	return payload
}

// invocationResult 编码调用类钩子的结果，reason 为执行结束原因
func invocationResult(reason string) []byte {
	return appendBytes(nil, 1, appendString(nil, 1, reason))
}

// hooks 依次记录一组钩子事件并推进执行内的钩子序号，上下文摘要钩子在会话已有模型调用时耗时 1–4 ms，其余耗时为 0
func (c *Client) hooks(at time.Time, turn SessionTurn, track *sessionTrack, kind string, names []string, metadata int, step int64, result func(name string) []byte) {
	execution := turn.Conversation.ExecutionID(turn.Conversation.Execution)
	for _, name := range names {
		track.seq++
		duration := 0
		if name == "context_summary" && metadata > 0 {
			duration = between(1, 4)
		}
		c.sessionEvent(at, 37, 38, hookPayload(turn.Conversation, execution, kind, name, at, duration, track.seq, metadata, step, result(name)))
	}
}

// preTool 记录一个函数调用执行前的三个钩子
func (c *Client) preTool(at time.Time, turn SessionTurn, track *sessionTrack, metadata int, step int64) {
	c.hooks(at, turn, track, "pre_tool", []string{"battle_mode_tool_args", "command_assessor", "timeout_long_running_search_command"}, metadata, step, func(name string) []byte {
		if name != "timeout_long_running_search_command" {
			return nil
		}
		return appendBytes(nil, 3, appendString(nil, 1, "allow"))
	})
}

// preInvocation 记录模型调用前的消息投递与上下文摘要钩子
func (c *Client) preInvocation(at time.Time, turn SessionTurn, track *sessionTrack, metadata int) {
	c.hooks(at, turn, track, "pre_invocation", []string{"message_delivery", "context_summary"}, metadata, -1, func(name string) []byte {
		if name == "message_delivery" {
			return nil
		}
		return invocationResult("")
	})
}

// postInvocation 记录模型调用后的钩子，final 为真时回复不含工具调用，另记执行结束前的钩子
func (c *Client) postInvocation(at time.Time, turn SessionTurn, track *sessionTrack, metadata int, final bool) {
	c.hooks(at, turn, track, "post_invocation", postHooks, metadata, -1, func(name string) []byte {
		if final && name == "no_tool_call_check" {
			return invocationResult(noToolCall)
		}
		return invocationResult("")
	})
	if final {
		c.hooks(at, turn, track, "stop", stopHooks, metadata, -1, func(string) []byte {
			return appendBytes(nil, 2, appendVarint(nil, 1, 0))
		})
	}
}

// userStep 编码用户输入步骤：previous 非空时附上一次用户请求的配置
func userStep(conversation Conversation, model CatalogModel, previous string, index, metadata int, execution string, created, done time.Time) []byte {
	meta := appendTime(nil, 1, created)
	meta = appendVarint(meta, 3, 4)
	meta = appendString(meta, 12, execution)
	meta = appendBytes(meta, 20, stepInfo(conversation, index, metadata))
	meta = appendBytes(meta, 26, transitions(stepDone, done))
	meta = appendString(meta, 35, contentID(conversation.TrajectoryID, index))
	input := appendBytes(nil, 3, nil)
	input = appendBytes(input, 4, nil)
	input = appendBytes(input, 12, sessionOverride(model))
	if previous != "" {
		input = appendBytes(input, 13, sessionOverride(CatalogModel{Model: previous}))
	}
	input = appendBytes(input, 18, pbGet(sessionFixed.Resolution, 1).raw)
	step := appendVarint(nil, 1, stepUser)
	step = appendVarint(step, 4, stepDone)
	step = appendBytes(step, 5, meta)
	return appendBytes(step, 19, input)
}

// usagePayload 编码模型调用的用量：输入为 promptTokenCount 减去缓存命中，缓存命中另记
func usagePayload(conversation Conversation, model CatalogModel, reply SessionReply, index int) []byte {
	usage := appendVarint(nil, 1, uint64(sessionFixed.Models[model.Model]))
	usage = appendSome(usage, 2, reply.Usage.InputTokens-reply.Usage.CachedTokens)
	usage = appendSome(usage, 3, reply.Usage.OutputTokens)
	usage = appendSome(usage, 5, reply.Usage.CachedTokens)
	usage = appendVarint(usage, 6, uint64(sessionFixed.Providers[model.APIProvider]))
	usage = appendString(usage, 7, botID(conversation.TrajectoryID, index))
	usage = appendBytes(usage, 8, appendString(appendString(nil, 1, "sessionID"), 2, sessionID))
	usage = appendSome(usage, 10, reply.Usage.OutputTokens)
	return appendString(usage, 11, reply.ResponseID)
}

// toolCall 编码步骤中的工具调用
func toolCall(call FunctionCall) []byte {
	return appendString(appendString(nil, 1, call.ID), 2, call.Name)
}

// plannerStep 编码模型回复步骤，回复含思考时记录调用开始到最后一个思考事件的思考耗时
func plannerStep(conversation Conversation, model CatalogModel, reply SessionReply, index, metadata int, execution string, created time.Time) []byte {
	meta := appendTime(nil, 1, created)
	meta = appendVarint(meta, 3, 2)
	meta = appendTime(meta, 6, reply.First)
	meta = appendTime(meta, 7, reply.Done)
	meta = appendTime(meta, 8, reply.Done)
	meta = appendBytes(meta, 9, usagePayload(conversation, model, reply, index))
	meta = appendVarint(meta, 11, uint64(sessionFixed.Models[model.Model]))
	meta = appendString(meta, 12, execution)
	meta = appendBytes(meta, 20, stepInfo(conversation, index, metadata))
	meta = appendBytes(meta, 26, transitions(8, reply.First, stepDone, reply.Done))
	meta = appendTime(meta, 32, reply.Done)
	meta = appendString(meta, 35, contentID(conversation.TrajectoryID, index))
	response := appendString(nil, 6, botID(conversation.TrajectoryID, index))
	for _, call := range reply.Calls {
		response = appendBytes(response, 7, toolCall(call))
	}
	if !reply.Thought.IsZero() {
		response = appendDuration(response, 11, reply.Thought.Sub(created))
	}
	response = appendVarint(response, 12, 2)
	step := appendVarint(nil, 1, stepPlanner)
	step = appendVarint(step, 4, stepDone)
	step = appendBytes(step, 5, meta)
	return appendBytes(step, 20, response)
}

// commandStep 编码工具步骤：客户端工具按官方终端命令步骤的形状记录，调用参数中的 WaitMsBeforeAsync 写入命令步骤，内嵌同样元数据的通用步骤
func commandStep(conversation Conversation, model CatalogModel, device Device, call FunctionCall, index, metadata int, execution string, created, finished, completed time.Time) []byte {
	started := finished.Add(5 * time.Millisecond)
	meta := appendTime(nil, 1, created)
	meta = appendVarint(meta, 3, 2)
	meta = appendBytes(meta, 4, toolCall(call))
	meta = appendTime(meta, 6, finished)
	meta = appendTime(meta, 7, finished)
	meta = appendTime(meta, 8, completed)
	meta = appendVarint(meta, 11, uint64(sessionFixed.Models[model.Model]))
	meta = appendString(meta, 12, execution)
	meta = appendBytes(meta, 20, stepInfo(conversation, index, metadata))
	meta = appendBytes(meta, 26, transitions(1, finished, 2, started, stepDone, completed))
	meta = appendTime(meta, 32, started)
	meta = appendBytes(meta, 34, appendString(nil, 4, "allow"))
	meta = appendString(meta, 35, contentID(conversation.TrajectoryID, index))
	meta = appendVarint(meta, 38, 1)
	render := appendString(nil, 6, "terminal")
	render = appendString(render, 7, "terminal")
	render = appendBytes(render, 9, nil)
	render = appendBytes(render, 10, appendBytes(nil, 3, appendVarint(nil, 3, 0)))
	generic := appendVarint(nil, 1, stepGeneric)
	generic = appendVarint(generic, 4, stepDone)
	generic = appendBytes(generic, 5, meta)
	generic = appendBytes(generic, 140, appendBytes(nil, 2, appendBytes(nil, 3, render)))
	command := appendVarint(nil, 6, 0)
	command = appendVarint(command, 11, 1)
	var args struct {
		WaitMsBeforeAsync int64
	}
	json.Unmarshal(call.Arguments, &args)
	command = appendSome(command, 12, args.WaitMsBeforeAsync)
	command = appendBytes(command, 21, nil)
	command = appendString(command, 31, shellName(device.OS))
	step := appendVarint(nil, 1, stepCommand)
	step = appendVarint(step, 4, stepDone)
	step = appendBytes(step, 5, meta)
	step = appendBytes(step, 159, generic)
	return appendBytes(step, 28, command)
}

// generatorPayloads 编码一次模型调用的两条码 2 事件载荷：完整条含全部消息，增量条只含上一次模型回复之后的消息；步骤序号为模型回复与其工具调用的步骤
func (s companionState) generatorPayloads(turn SessionTurn, device Device, steps []sessionStep, reply SessionReply, metadata int, invoked time.Time) [][]byte {
	conversation, model := turn.Conversation, turn.Model
	family := sessionFamily(model)
	execution := conversation.ExecutionID(conversation.Execution)
	planner := len(steps)
	common := -1
	for index, step := range steps {
		if step.kind == stepPlanner {
			common = index
		}
	}
	names := map[int]string{stepUser: "USER_INPUT", stepPlanner: "PLANNER_RESPONSE", stepCommand: "GENERIC"}
	group := func(name string, kind int, system bool, children []byte, total int64) []byte {
		out := appendString(nil, 1, name)
		out = appendVarint(out, 2, uint64(kind))
		if system {
			out = appendVarint(out, 3, 1)
		}
		out = appendVarint(out, 4, uint64(total))
		return append(out, children...)
	}
	child := func(name string, tokens int64) []byte {
		return appendBytes(nil, 5, appendVarint(appendString(nil, 1, name), 3, uint64(tokens)))
	}
	var systemChildren, toolChildren, chatChildren []byte
	var systemTotal, toolTotal, chatTotal int64
	for i, name := range sessionFixed.Sections {
		tokens := int64(turn.Sections[i])
		systemChildren, systemTotal = append(systemChildren, child(name, tokens)...), systemTotal+tokens
	}
	for i, name := range sessionFixed.Tools {
		tokens := int64(sessionFixed.ToolTokens[family][i])
		toolChildren, toolTotal = append(toolChildren, child(name, tokens)...), toolTotal+tokens
	}
	for index, step := range steps {
		chatChildren, chatTotal = append(chatChildren, child(strconv.Itoa(index)+": "+names[step.kind], step.tokens)...), chatTotal+step.tokens
	}
	total := systemTotal + toolTotal + chatTotal
	breakdown := appendBytes(nil, 1, group("System Prompt", 1, true, systemChildren, systemTotal))
	breakdown = appendBytes(breakdown, 1, group("Tools", 2, true, toolChildren, toolTotal))
	breakdown = appendBytes(breakdown, 1, group("Chat Messages", 7, false, chatChildren, chatTotal))
	breakdown = appendVarint(breakdown, 2, uint64(total))
	window := appendVarint(nil, 1, uint64(total))
	window = appendBytes(window, 3, breakdown)
	window = appendVarint(window, 4, pbGet(sessionFixed.Cascade[family], 2, 5).u)
	start := appendInt(nil, 2, -1)
	start = appendBytes(start, 10, window)
	usage := usagePayload(conversation, model, reply, planner)
	prompt := func(index int) []byte {
		step := steps[index]
		var out []byte
		switch step.kind {
		case stepUser:
			out = appendVarint(out, 2, 1)
			out = appendVarint(out, 4, uint64(step.tokens))
		case stepPlanner:
			out = appendString(out, 1, botID(conversation.TrajectoryID, index))
			out = appendVarint(out, 2, 2)
			out = appendVarint(out, 4, uint64(step.tokens))
			for _, call := range step.calls {
				out = appendBytes(out, 6, toolCall(call))
			}
		default:
			out = appendVarint(out, 2, 4)
			out = appendVarint(out, 4, uint64(step.tokens))
			out = appendString(out, 7, step.call.ID)
		}
		out = appendSome(out, 18, int64(index))
		return appendString(out, 21, contentID(conversation.TrajectoryID, index))
	}
	current := appendVarint(nil, 2, 2)
	for _, call := range reply.Calls {
		current = appendBytes(current, 6, toolCall(call))
	}
	current = appendSome(current, 18, int64(planner))
	indices := binary.AppendUvarint(nil, uint64(planner))
	for i := range reply.Calls {
		indices = binary.AppendUvarint(indices, uint64(planner+1+i))
	}
	variant := func(from, last int, full bool) []byte {
		chat := []byte(nil)
		for index := from; index < planner; index++ {
			chat = appendBytes(chat, 2, prompt(index))
		}
		chat = appendBytes(chat, 2, current)
		chat = appendVarint(chat, 3, uint64(sessionFixed.Models[model.Model]))
		chat = appendBytes(chat, 4, usage)
		for _, name := range sessionFixed.Tools {
			chat = appendBytes(chat, 8, appendString(nil, 1, name))
		}
		chat = appendBytes(chat, 9, start)
		for index := from; index < planner; index++ {
			chat = appendBytes(chat, 10, appendSome(nil, 1, int64(index)))
		}
		chat = appendBytes(chat, 10, appendVarint(appendSome(nil, 1, int64(planner-1)), 2, 1))
		chat = appendDuration(chat, 11, reply.First.Sub(invoked))
		chat = appendDuration(chat, 12, reply.Done.Sub(reply.First))
		chat = append(chat, sessionFixed.PromptSections...)
		chat = appendBytes(chat, 17, appendString(appendBytes(nil, 2, usage), 4, reply.TraceID))
		chat = appendString(chat, 19, reply.Model)
		chat = appendString(chat, 21, model.DisplayName)
		generator := appendBytes(nil, 2, indices)
		generator = appendBytes(generator, 3, pbGet(s.sessionCascade(model, device), 1).raw)
		generator = appendString(generator, 4, execution)
		generator = appendBytes(generator, 8, s.mendelIDs())
		if model.ModelProvider != ProviderGoogle {
			generator = appendVarint(generator, 10, 1)
		}
		generator = appendBytes(generator, 1, chat)
		payload := appendString(nil, 1, conversation.TrajectoryID)
		payload = appendString(payload, 2, conversation.CascadeID)
		payload = appendBytes(payload, 3, generator)
		payload = appendVarint(payload, 4, uint64(metadata))
		payload = appendInt(payload, 5, int64(last))
		payload = appendVarint(payload, 6, flag(full))
		payload = appendString(payload, 11, execution)
		return appendString(payload, 12, model.Model)
	}
	return [][]byte{variant(0, -1, true), variant(common+1, common, false)}
}

// executionPayload 编码执行结束时的码 1 事件载荷
func (s companionState) executionPayload(turn SessionTurn, device Device, invocations, last int) []byte {
	conversation, model := turn.Conversation, turn.Model
	execution := conversation.ExecutionID(conversation.Execution)
	metadata := appendVarint(nil, 1, 4)
	metadata = appendVarint(metadata, 2, uint64(invocations))
	metadata = appendVarint(metadata, 3, uint64(last))
	metadata = appendString(metadata, 9, execution)
	metadata = appendBytes(metadata, 10, s.sessionCascade(model, device))
	metadata = appendBytes(metadata, 11, s.mendelIDs())
	if model.ModelProvider != ProviderGoogle {
		metadata = appendVarint(metadata, 13, 1)
	}
	metadata = appendBytes(metadata, 16, sessionFixed.Resolution)
	metadata = appendBytes(metadata, 17, sessionOverride(model))
	metadata = appendBytes(metadata, 18, sessionFixed.Customizations)
	payload := appendString(nil, 2, execution)
	payload = appendString(payload, 3, conversation.CascadeID)
	payload = appendString(payload, 4, conversation.TrajectoryID)
	payload = appendBytes(payload, 7, s.sessionCascade(model, device))
	payload = appendBytes(payload, 8, s.mendelIDs())
	payload = appendVarint(payload, 9, 4)
	return appendBytes(payload, 10, metadata)
}

// trajectoryPayload 编码新会话的码 10 事件载荷
func (s companionState) trajectoryPayload(conversation Conversation, device Device, created time.Time) []byte {
	metadata := appendTime(nil, 2, created)
	metadata = appendString(metadata, 3, uuid.NewString())
	metadata = appendString(metadata, 6, conversation.CascadeID)
	metadata = appendBytes(metadata, 15, s.mendelIDs())
	metadata = appendString(metadata, 18, outsideProject)
	payload := appendString(nil, 1, conversation.TrajectoryID)
	payload = appendString(payload, 2, conversation.CascadeID)
	payload = appendVarint(payload, 3, trajectoryCascade)
	payload = appendVarint(payload, 4, 1)
	payload = appendBytes(payload, 6, metadata)
	payload = appendString(payload, 7, ClientVersion)
	payload = appendString(payload, 8, "antigravity")
	return appendString(payload, 9, device.OS)
}

// mediaCounts 返回最后一条用户请求中图片、音频与视频的个数
func mediaCounts(contents []Content) (images, audio, video int) {
	for _, part := range contents[len(contents)-1].Parts {
		if part.InlineData == nil {
			continue
		}
		switch {
		case strings.HasPrefix(part.InlineData.MIME, "image/"):
			images++
		case strings.HasPrefix(part.InlineData.MIME, "audio/"):
			audio++
		case strings.HasPrefix(part.InlineData.MIME, "video/"):
			video++
		}
	}
	return images, audio, video
}

// flag 返回布尔值的 varint 取值
func flag(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

// completed 返回步骤中已完成的模型调用数
func completed(steps []sessionStep) int {
	count := 0
	for _, step := range steps {
		if step.kind == stepPlanner {
			count++
		}
	}
	return count
}

// SessionSend 在生成请求发出前按官方会话的先后记录 clearcut 事件：用户请求记录发送事件、用户输入步骤与调用前钩子，新会话另记新建会话、项目、trajectory、首次加载与标题事件，模型与上次不同时记录切换模型；带回工具结果的请求按工具依次记录工具前钩子（第一个工具除外）、工具后钩子与工具步骤，再记调用后钩子与调用前钩子；本轮读取过插件市场目录时，读取事件记在用户输入步骤或工具步骤之前，发送事件及更早的事件改以读取开始时刻为基准
func (c *Client) SessionSend(turn SessionTurn) {
	now := time.Now()
	conversation, model := turn.Conversation, turn.Model
	execution := conversation.ExecutionID(conversation.Execution)
	track := c.track(conversation.TrajectoryID, now)
	track.mu.Lock()
	defer track.mu.Unlock()
	steps := sessionSteps(turn.Contents, track.outputs)
	metadata := completed(steps)
	ago := func(low, high int) time.Time {
		return now.Add(-time.Duration(between(low, high)) * time.Millisecond)
	}
	if !userRequest(turn.Contents[len(turn.Contents)-1]) {
		c.logMarketplace()
		first, replied := track.first, track.replied
		if replied.IsZero() {
			first, replied = ago(1000, 1500), ago(900, 1000)
		}
		finished := ago(7, 9)
		start := len(steps)
		for start > 0 && steps[start-1].kind == stepCommand {
			start--
		}
		for index := start; index < len(steps); index++ {
			if index > start {
				c.preTool(finished, turn, track, metadata, int64(index))
			}
			c.hooks(finished, turn, track, "post_tool", []string{"command_assessor"}, metadata, int64(index), func(string) []byte { return nil })
			c.sessionEvent(ago(6, 7), 11, 12, stepPayload(conversation, commandStep(conversation, model, c.device, steps[index].call, index, metadata-1, execution, first, replied, finished), index, metadata-1, execution))
		}
		hooked := ago(4, 5)
		c.postInvocation(hooked, turn, track, metadata, false)
		c.preInvocation(hooked, turn, track, metadata)
		track.invoked = hooked
		return
	}
	early := ago
	if !turn.Anchor.IsZero() {
		anchor := turn.Anchor.Add(time.Duration(between(5, 15)) * time.Millisecond)
		if turn.New {
			anchor = turn.Anchor.Add(time.Duration(between(245, 255)) * time.Millisecond)
		}
		early = func(low, high int) time.Time {
			return anchor.Add(-time.Duration(between(low, high)) * time.Millisecond)
		}
	}
	c.mu.Lock()
	changed := c.hub.state.Model != model.Model
	loaded := turn.New && !c.hub.loaded
	c.hub.loaded = c.hub.loaded || turn.New
	c.hub.state.Model = model.Model
	state, save := c.hub.state, c.hub.save
	c.mu.Unlock()
	if changed && save != nil {
		if err := save(state); err != nil {
			slog.Warn("保存虚拟 Hub 状态失败 | 错误="+err.Error(), "source", c.account)
		}
	}
	images, audio, video := mediaCounts(turn.Contents)
	media := images + audio + video
	if turn.New {
		opened := early(3300, 3800)
		c.sessionUI(opened, "conversation_new", map[string]any{"ui_location": "new_conversation_button", "trigger_type": "click"})
		c.sessionUI(opened, "AGENT_MANAGER_NEW_WORKSPACE_CONVERSATION", map[string]any{})
		c.sessionUI(opened.Add(time.Millisecond), "SWITCH_CONVERSATION", map[string]any{"cascadeId": ""})
		c.sessionUI(opened.Add(8*time.Millisecond), "AGENT_MANAGER_HOME", map[string]any{})
	}
	if changed {
		c.sessionUI(early(1400, 1900), "model_changed", map[string]any{"ui_location": "model_selector_button", "trigger_type": "click", "name": model.DisplayName})
	}
	created, sent := early(32, 36), early(28, 31)
	if turn.New {
		c.sessionUI(early(650, 720), "PROJECT_RESOURCES_FINALIZED", map[string]any{"projectId": outsideProject, "workspace_type": "local", "environmentType2": "sot"})
		c.sessionUI(early(530, 600), "CASCADE_CONVERSATION_STARTED", map[string]any{"cascadeId": conversation.CascadeID, "projectId": outsideProject})
		c.mu.Lock()
		state := c.state
		c.mu.Unlock()
		c.sessionEvent(early(400, 410), 10, 11, state.trajectoryPayload(conversation, c.device, early(460, 470)))
		if loaded {
			c.hubEventAt(early(275, 285), "conversation_load_completed", eventShape{flags: true, ui: true}, map[string]any{
				"coldLoadLatencyMs": c.sinceLaunch(), "loadType": "cold", "stepCount": 1, "toolCallCount": 0, "turnCount": 1, "workspaceCount": 0,
			})
		}
		c.hubEventAt(early(263, 266), "conversation_title_generation", eventShape{flags: true, blank: true}, map[string]any{
			"conversation_id": conversation.CascadeID, "user_input_media_count": media, "user_input_media_mime_types": "",
			"title_generation_request_media_count": media, "title_generation_request_media_mime_types": "",
		})
		created, sent = early(259, 262), early(255, 258)
	}
	latency := between(0, 3)
	c.hubEventAt(sent, "SEND_USER_CASCADE_MESSAGE_LATENCY", eventShape{flags: true}, map[string]any{
		"cascade_id": conversation.CascadeID, "trajectory_id": conversation.TrajectoryID, "message_origin": "AGENT_MESSAGE_ORIGIN_UNSPECIFIED", "status": "OK", "blocking": "false",
		"total_latency_ms": latency, "cascade_send_latency_ms": latency, "hydrate_slash_commands_latency_ms": 0, "active_user_state_latency_ms": 0, "load_trajectory_latency_ms": 0,
		"resolve_agent_config_latency_ms": 0, "save_media_latency_ms": 0, "media_summaries_latency_ms": 0, "media_count": media, "saved_media_count": media,
	})
	delay := 9
	if turn.New {
		delay = between(16, 47)
	}
	c.sessionUI(sent.Add(time.Duration(delay)*time.Millisecond), "CASCADE_MESSAGE_SENT", map[string]any{
		"is_new_conversation": strconv.FormatBool(turn.New), "model": model.DisplayName, "num_images": images, "num_audio": audio, "num_videos": video, "project_type": "normal",
	})
	index := len(steps) - 1
	previous := ""
	if index > 0 {
		previous = cmp.Or(track.model, model.Model)
	}
	c.logMarketplace()
	c.sessionEvent(ago(6, 9), 11, 12, stepPayload(conversation, userStep(conversation, model, previous, index, metadata, execution, created, ago(10, 12)), index, metadata, execution))
	hooked := ago(3, 5)
	track.seq, track.sent, track.invoked, track.model = 0, sent, hooked, model.Model
	c.preInvocation(hooked, turn, track, metadata)
}

// SessionReply 在模型调用完成后记录回复事件：模型回复步骤；有工具调用时记录码 2 与第一个调用的工具前钩子，否则记录首 token 耗时、码 2、调用后与结束钩子和码 1
func (c *Client) SessionReply(turn SessionTurn, reply SessionReply) {
	conversation, model := turn.Conversation, turn.Model
	execution := conversation.ExecutionID(conversation.Execution)
	track := c.track(conversation.TrajectoryID, time.Now())
	track.mu.Lock()
	defer track.mu.Unlock()
	steps := sessionSteps(turn.Contents, track.outputs)
	planner, metadata := len(steps), completed(steps)
	track.outputs[planner] = reply.Usage.OutputTokens
	track.first, track.replied = reply.First, reply.Done
	invoked, sent := track.invoked, track.sent
	if invoked.IsZero() {
		invoked, sent = reply.First.Add(-time.Second), reply.First.Add(-time.Second)
	}
	c.mu.Lock()
	state := c.state
	c.mu.Unlock()
	generators := state.generatorPayloads(turn, c.device, steps, reply, metadata, invoked)
	if len(reply.Calls) > 0 {
		c.sessionEvent(time.Now(), 11, 12, stepPayload(conversation, plannerStep(conversation, model, reply, planner, metadata, execution, invoked), planner, metadata, execution))
		hooked := time.Now()
		for _, payload := range generators {
			c.sessionEvent(hooked, 2, 3, payload)
		}
		c.preTool(hooked, turn, track, metadata+1, int64(planner+1))
		return
	}
	turns, tools, invocations := 0, 0, 1
	for _, step := range steps {
		switch step.kind {
		case stepUser:
			turns++
		case stepCommand:
			tools++
		}
	}
	for index := planner - 1; index >= 0 && steps[index].kind != stepUser; index-- {
		if steps[index].kind == stepPlanner {
			invocations++
		}
	}
	c.hubEvent("ttft_completed", eventShape{flags: true, ui: true}, map[string]any{
		"latencyMs": int(reply.First.Sub(sent).Milliseconds()), "turnCount": turns - 1, "toolCallCount": tools, "stepCount": planner + 1, "workspaceCount": 0,
		"modelId": strings.TrimPrefix(model.Model, "MODEL_"),
	})
	c.sessionEvent(time.Now(), 11, 12, stepPayload(conversation, plannerStep(conversation, model, reply, planner, metadata, execution, invoked), planner, metadata, execution))
	hooked := time.Now()
	for _, payload := range generators {
		c.sessionEvent(hooked, 2, 3, payload)
	}
	c.postInvocation(hooked, turn, track, metadata+1, true)
	c.sessionEvent(time.Now(), 1, 2, state.executionPayload(turn, c.device, invocations, planner))
}
