# 运行日志与请求账本

服务日志由 Go `slog` 写出，同一条事件同时进入三处：

| 去向 | 内容 |
| --- | --- |
| 标准错误与 `RUNTIME_DIR/logs/antigravity2api.log` | 达到 `LOG_LEVEL` 的全部事件，格式由 `LOG_FORMAT` 决定 |
| 管理事件流 `GET /api/events` | 同一组事件，内存保留最近 2000 条，管理端“日志”页的实时视图 |
| 请求账本 `RUNTIME_DIR/requests.db` 的 `logs` 表 | WARN 与 ERROR，保留 90 天，管理端“日志”页的已保存视图 |

`LOG_LEVEL`、`LOG_FORMAT`、`LOG_FILE`、`LOG_MAX_TOTAL_MB` 与 `REQUEST_LOG_BODIES` 在管理端设置页保存后立即生效。日志文件单个 10 MB 轮转，轮转文件名带时间，目录总量超过 `LOG_MAX_TOTAL_MB` 时删除最旧的文件。未恢复的 panic 与 Go 运行时致命错误写入同目录的 `crash.log`。

## 日志结构

JSON 格式每行一条事件：

```json
{"time":"2026-10-15T09:11:14.507+08:00","level":"INFO","caller":"app/app.go:166","msg":"200 | 3.113s | 127.0.0.1 | POST /v1/responses","source":"http","event":"http.access","status":200,"latency_ms":3112,"client_ip":"127.0.0.1","method":"POST","path":"/v1/responses","request_id":"req_nka6uxkxk4ssb6g4rvcbsu6mpv"}
```

| 字段 | 含义 |
| --- | --- |
| `time` | 本地时间，RFC 3339 |
| `level` | `DEBUG`、`INFO`、`WARN`、`ERROR` |
| `caller` | 写日志的源文件与行号 |
| `msg` | 形如 `摘要 | 键=值 | 键=值` 的中文消息 |
| `source` | 事件来源 |
| `event` | 稳定事件名，请求与上游事件带此字段 |
| `request_id` | 请求 ID，与响应头 `X-Request-ID` 相同 |
| `request` | 请求开始与结束事件附带的请求记录，字段见“请求账本” |

`text` 格式为 slog 文本格式，字段相同。

| `source` | 事件 |
| --- | --- |
| `service` | 启动、账户池刷新、配置保存、版本检查、账本维护 |
| `http` | 访问日志 |
| `request` | 尚未分配账户的请求事件、管理端停止请求 |
| `auth` | 登录、导入、远程配对、删除账户 |
| 账户邮箱 | 该账户的初始化、上游请求、冷却与已分配账户的请求事件 |

| `event` | 时机 |
| --- | --- |
| `http.access` | 每个 HTTP 请求结束 |
| `request.started` | 公开请求首次调用生成 |
| `request.progress` | 分配账户、换账户、上游尝试结束、流式等待 |
| `request.finished` | 公开请求结束并写入账本 |
| `upstream.request` | 生成以外的一次上游请求结束 |

## 启动过程

```text
INFO  service  服务就绪 | 地址=127.0.0.1:8017
INFO  account@example.com  token 已刷新 | 到期=2026-10-15T10:08:15+08:00
INFO  account@example.com  上游请求 | POST daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist | 状态=200 | 耗时=412ms
INFO  service  官方客户端最新版本 | 版本=2.23.1 当前=2.23.0
INFO  service  账户池刷新完成 | 耗时=40.272s
```

监听建立后写 `服务就绪`，随后在后台逐个初始化账户（顺序见 [开发文档](development.md) “初始化”）。每个账户的初始化序列、周期请求与 clearcut 上报各写一条 `上游请求`。单个账户失败写 `账户初始化失败 | 错误=...`（ERROR），该账户移出调度，刷新结束时写 `账户池刷新部分失败 | 耗时=... | 错误=...`（WARN）。启动 10 秒后读取官方更新清单，写 `官方客户端最新版本`，读取失败写 WARN。

## 访问日志

每个 HTTP 请求结束写一条 `http.access`，消息为 `状态 | 耗时 | 客户端 IP | 方法 路径`，属性带 `status`、`latency_ms`、`client_ip`、`method`、`path`。级别：5xx 为 ERROR，4xx 为 WARN，`/readyz` 与静态资源为 DEBUG，其余为 INFO。客户端 IP 的取法见 `TRUSTED_PROXIES`。

## API 请求

通过 API key 校验的公开 POST 请求写开始、进展与结束三类事件，都带 `request_id`：

```text
INFO  request              请求开始 | 模型=gpt-oss-120b-medium | 协议=openai-responses | 流式=true | 档位=full | 温度=- | TopP=- | 思考=- | 思考预算=- | 最大输出=-
DEBUG account@example.com  分配账户 | 账户=account@example.com | 排队=3ms
WARN  account@example.com  上游尝试失败 | 第 1 次 | 状态=503 | 错误码=MODEL_CAPACITY_EXHAUSTED | 重试延迟= | 耗时=959ms | 错误=No capacity available for model gpt-oss-120b-medium on the server
WARN  account@example.com  模型容量不足，换账户重试 | 模型=gpt-oss-120b-medium
WARN  other@example.com    切换账户 | 账户=other@example.com | 上一账户=account@example.com
DEBUG other@example.com    上游尝试完成 | 第 2 次 | 状态=200 | traceId=5f0c2a7d9b1e4c33 | 耗时=812ms
INFO  other@example.com    工具调用完成 | 状态=200 | 耗时=3.11s | 账户=other@example.com | 模型=gpt-oss-120b-medium | 上游模型=gpt-oss-120b-medium | 输入=31327 | 缓存=10219 | 思考=96 | 回复=253
```

`请求开始` 列出公开模型、协议、是否流式、系统提示档位与请求的采样参数，未设置的参数写 `-`。结束消息按请求结果：

| 结果 `state` | 消息 | 级别 |
| --- | --- | --- |
| `completed` | `请求完成` | INFO |
| `tool_calls` | `工具调用完成` | INFO |
| `limited` | `达到输出上限` | WARN |
| `blocked` | `上游终止生成` | WARN |
| `cancelled` | `请求已取消` | WARN |
| `failed` | `请求失败` | ERROR |

结束消息带状态码、耗时、账户、公开模型、上游模型与 token 用量，失败、取消与终止时另带 `错误=`。客户端断开与管理端停止记为 `499`；超过 `REQUEST_TIMEOUT` 记为 `504`，错误阶段为 `timeout`。

## 流式等待

生成事件流连续 15 秒没有新事件时写一条 WARN，下一个事件到达时写恢复：

```text
WARN  account@example.com  上游首事件等待 | 模型=gemini-3.8-flash-high | 已等待=15s
DEBUG account@example.com  上游首事件到达 | 模型=gemini-3.8-flash-high | 等待=21.448s | 事件=reasoning
WARN  account@example.com  事件流停顿 | 模型=gemini-3.8-flash-high | 已等待=15s | 最近事件=text
INFO  account@example.com  事件流恢复 | 模型=gemini-3.8-flash-high | 停顿=17.208s | 当前事件=text
```

客户端断开后服务继续读完上游事件流，以便账本记下完整用量；这段时间不写等待诊断。

## 账户事件

生成失败时按原因处理账户（规则见 [开发文档](development.md) “失败与换号”），每种处理写一条以账户为来源的事件：

| 消息 | 级别 | 处理 |
| --- | --- | --- |
| `账户认证失败，移出调度 | 错误=...` | WARN | token 刷新被拒或重发后仍为 401 |
| `账户需要验证，停止请求` | WARN | 需要验证 |
| `账户违反服务条款，已停用` | ERROR | 违反条款 |
| `账户冷却 | 范围=... | 时长=... | 原因=... | 系统提示档位=...` | WARN | 额度组或模型冷却 |
| `同额度池账户冷却 | 范围=... | 时长=... | 耗尽账户=...` | WARN | 以同额度池的账户为来源，各写一条 |
| `模型容量不足，换账户重试 | 模型=...` | WARN | 容量不足 |
| `上游拒绝请求内容，不冷却账户 | 原因=... | 系统提示档位=...` | WARN | 没有 ErrorInfo 与恢复时刻的 429 |

账户运行中的其他事件：

| 消息 | 级别 |
| --- | --- |
| `token 已刷新 | 到期=...` | INFO |
| `读取额度摘要失败 | 错误=...` | WARN |
| `关闭账户遥测失败 | 错误=...` | WARN |
| `保存虚拟 Hub 状态失败 | 错误=...` | WARN |
| `写入会话记录失败 | 错误=...` | WARN |

`上游请求 | 方法 主机路径 | 状态=... | 耗时=...ms` 记录生成以外的每次上游请求（初始化、目录、额度、unleash、clearcut 上报、遥测设置），失败时为 WARN 并带 `错误=`。DEBUG 级别另记录每次上游连接：`上游连接 | 主机=... | 复用=... | 空闲=... | 代理=...`。

管理端与命令行的账户操作：

| 消息 | 来源 |
| --- | --- |
| `账户登录开始 | 方式=browser/code | 登录=...` | `auth` |
| `账户添加完成 | 账户=...`、`账户登录已取消`、`账户添加失败` | `auth` |
| `官方客户端账户导入完成 | 账户=...`、`官方客户端账户导入失败` | `auth` |
| `远程配对令牌已生成 | 有效期=10m` | `auth` |
| `远程配对导入完成 | 账户=...`、`远程配对导入失败` | `auth` |
| `账户已删除 | 账户=...` | `auth` |
| `账户已启用`、`账户已停用`、`账户代理已设置 | 代理=...`、`账户优先级已设置 | 优先级=...` | 账户 |
| `账户验证完成 | 耗时=...`、`账户验证失败` | 账户 |
| `服务配置已保存 | 变更=...` | `service` |
| `管理端停止请求 | 请求=...` | `request` |

账户代理在日志中显示时密码替换为 `xxxxx`。

## 请求账本

`RUNTIME_DIR/requests.db` 是 SQLite（WAL）账本。每个通过 API key 校验的公开 POST 请求结束后写入一行请求记录；写入由后台协程按批提交，不阻塞响应，等待写入的条目超过 4096 条时丢弃新条目。

| 字段 | 内容 |
| --- | --- |
| `id`、`time` | 请求 ID、完成时间 |
| `client_request_id` | 客户端发来的 `X-Request-ID` |
| `response_id` | 公开协议响应中的 ID |
| `protocol`、`path` | `openai-chat`、`openai-responses`、`anthropic`、`gemini`、`other`，请求路径 |
| `api_key` | API key 掩码 |
| `client_ip`、`user_agent`、`stream` | 客户端 IP、User-Agent、是否流式 |
| `model` | 公开模型名 |
| `upstream_model`、`model_version` | 发往上游的模型 ID、上游返回的模型版本 |
| `request_type`、`prompt_tier` | 上游信封的 `requestType`、系统提示档位 |
| `cascade_id`、`step_index` | 上游会话 ID 与步序号 |
| `account`、`quota_group` | 最终执行账户、额度组 |
| `status`、`state` | HTTP 状态码、请求结果 |
| `error_phase` | 失败阶段：`request`、`routing`、`upstream`、`network`、`timeout`、`internal` |
| `upstream_status`、`error_code` | 最后一次上游尝试的状态码与错误码 |
| `duration_ms` | 请求进入到结束的耗时 |
| `first_event_ms` | 请求进入到首个正文、思考或工具调用事件 |
| `queue_ms` | 请求进入到取得首个账户 |
| `input_tokens`、`cached_tokens` | 输入 token、其中命中缓存的部分 |
| `reasoning_tokens`、`reply_tokens` | 思考 token、回复 token |
| `tool_calls` | 工具调用数 |
| `cost_usd` | 估算美元费用，模型没有单价时为 `null` |
| `error` | 错误摘要 |
| `attempts` | 全部上游尝试 |

每次上游尝试记录 `account`、`url`、`status`（连接失败为 0）、`code`（ErrorInfo `reason`，没有时为 `error.status`）、`retry_delay`、`trace_id`、`response_id`、`error` 与 `duration_ms`。

请求结束时按以下顺序判定 `state`，取第一条成立的规则：

1. 超过 `REQUEST_TIMEOUT` 且上游没有发出终止事件：`failed`，`status` 为 504，`error_phase` 为 `timeout`
2. 有取消原因且上游没有发出终止事件，或响应状态码为 499，或生成错误是 context 取消：`cancelled`，`status` 为 499
3. 响应状态码不小于 400，或有生成错误：`failed`；响应状态码小于 400（响应头发出后流中出错）时，`status` 取错误自带的 HTTP 状态码，其次上游状态码，都没有时为 502
4. 终止原因为 `MAX_TOKENS`（不区分大小写）：`limited`
5. 终止原因非空且不是 `STOP`：`blocked`
6. 工具调用数大于 0：`tool_calls`
7. 其余：`completed`

- 响应状态码：处理链写出的第一个状态码，没有写出时为 200
- 取消原因：请求 context 结束的原因（客户端断开、管理端停止、`REQUEST_TIMEOUT`），或 Responses 后台请求经 cancel 接口终止；后台请求不随客户端断开而取消
- 生成错误：同一请求最近一次生成未能开始的错误，或事件流中的错误事件
- 终止事件：上游的 finish 事件，终止原因为其中的 `finishReason`；该事件到达后，客户端断开与超时不改变结果

`error` 的取值：

- `completed`、`tool_calls`、`limited` 为空
- 超时为 `请求超过 REQUEST_TIMEOUT`
- `cancelled` 为 `客户端断开连接`，管理端停止时为 `管理端停止了请求`，后台响应被取消时为 `客户端取消了后台响应`
- `blocked` 为终止原因原文
- 其余 `failed` 依次取生成错误的文本、响应体的 `error.message`、状态码的标准说明（如 `Bad Gateway`）

第 3 条规则判定的 `failed` 按下表顺序取 `error_phase`；除超时外，其他结果的 `error_phase` 为空串：

| 条件 | `error_phase` |
| --- | --- |
| 未调用生成，状态码小于 500 | `request` |
| 未调用生成 | `internal` |
| 没有上游尝试 | `routing` |
| 最后一次尝试连接失败 | `network` |
| 其余 | `upstream` |

`upstream_status` 与 `error_code` 取最后一次上游尝试的 `status` 与 `code`，没有尝试时为 0 与空串。

失败请求总是保存上游尝试的请求与响应正文；`REQUEST_LOG_BODIES=true` 时成功请求也保存，并另存公开协议的请求与响应正文。正文各截断到 64 KiB 并记录原始字节数，公开正文与上游正文各保留最近 1000 条。正文包含提示词与模型输出，以明文保存在本机。

写入请求记录的同一事务按 UTC 小时与服务器本地日累加汇总，汇总含请求数、各类 token、费用与耗时分布。查询时完整落在查询范围且落在单个分桶内的服务器本地日读取日汇总，其余部分中同样完整落在范围与单个分桶内的 UTC 整小时读取小时汇总，剩下的读取原始记录。服务器时区变化后，下次启动时从小时汇总与原始记录重建本地日汇总。请求记录、汇总、正文、额度快照与日志保留 90 天，每天清理一次。

费用按上游模型 ID（去掉思考档位后缀）对应的官方 API 文本单价逐条计算：缓存命中的输入按缓存单价，输出单价包含思考 token，输入超过 200,000 token 的请求按长上下文单价。

- 模型名：`upstream_model` 依次尝试去掉末尾的 `-extra-low`、`-thinking`、`-medium`、`-tiered`、`-high`、`-low`，只去掉第一个匹配的后缀，结果与下表模型名完全相同时有单价；例如 `claude-opus-4-6-thinking` 按 `claude-opus-4-6`、`gpt-oss-120b-medium` 按 `gpt-oss-120b` 计费
- 费用 =（`input_tokens` − `cached_tokens`）× 输入单价 + `cached_tokens` × 缓存单价 +（`reasoning_tokens` + `reply_tokens`）× 输出单价，再除以 1,000,000；`cached_tokens` 是 `input_tokens` 中命中缓存的部分，token 取同一请求各次生成的累计值
- 长上下文按 `input_tokens`（含缓存部分）大于 200,000 判断，输入、缓存与输出三项单价一起切换；只有列出长上下文单价的模型分档
- 模型没有单价时，消耗了 token 的请求 `cost_usd` 为 `null` 并计入汇总的 `unpriced`，没有消耗 token 的请求为 0

单价，单位为美元每百万 token：

| 上游模型 | 输入 | 缓存命中输入 | 输出 |
| --- | --- | --- | --- |
| `claude-opus-5-5` | 4 | 0.20 | 20 |
| `claude-sonnet-5-5` | 2 | 0.10 | 10 |
| `claude-opus-4-6` | 5 | 0.50 | 25 |
| `claude-sonnet-4-6` | 3 | 0.30 | 15 |
| `gpt-oss-120b` | 0.09 | 0.09 | 0.36 |
| `gemini-4-argon` | 2 | 0.10 | 10 |
| `gemini-3.8-flash` | 0.75 | 0.075 | 3.75 |
| `gemini-3.5-flash` | 1.50 | 0.15 | 9 |
| `gemini-3-flash-agent` | 1.50 | 0.15 | 9 |
| `gemini-3.5-flash-lite` | 0.30 | 0.03 | 2.50 |
| `gemini-3.1-flash-lite` | 0.25 | 0.025 | 1.50 |
| `gemini-3.1-flash-image` | 0.50 | 0.50 | 3 |
| `gemini-3-flash` | 0.50 | 0.05 | 3 |
| `gemini-2.5-pro` | 1.25 | 0.125 | 10 |
| `gemini-2.5-flash` | 0.30 | 0.03 | 2.50 |
| `gemini-2.5-flash-lite` | 0.10 | 0.01 | 0.40 |

长上下文单价，单位为美元每百万 token：

| 上游模型 | 输入 | 缓存命中输入 | 输出 |
| --- | --- | --- | --- |
| `gemini-2.5-pro` | 2.50 | 0.25 | 15 |

```text
INFO  service  请求账本已按服务器时区重建本地日汇总 | 天数=<COUNT>
WARN  service  请求账本写入队列已满 | 丢弃=<COUNT>
ERROR service  请求账本写入失败 | 条目=<COUNT> | 错误=<ERROR>
ERROR service  请求账本清理失败 | 错误=<ERROR>
ERROR service  请求记录导出失败 | 错误=<ERROR>
```

### 存储结构

账本经 `modernc.org/sqlite` 打开，连接参数为 `busy_timeout(10000)`、`journal_mode(WAL)`、`synchronous(NORMAL)`，最多 4 个连接。写入协程把连续到达的条目合并为一个事务，每个事务最多 256 条。

| 表 | 一行内容 | 主要列 | 键与索引 |
| --- | --- | --- | --- |
| `requests` | 一条请求记录 | 上文请求记录的全部字段 | 主键 `id`；索引 `requests_time(time)`、`requests_model_time(model, time)`、`requests_account_time(account, time)`、`requests_state_time(state, time)` |
| `hours` | 一个 UTC 小时内同键请求的汇总 | `start`、7 个维度列、累计列 | 主键 `start` 加 7 个维度列，`WITHOUT ROWID` |
| `days` | 一个服务器本地日内同键请求的汇总 | 同 `hours` | 同 `hours` |
| `bodies` | 一个请求的公开协议正文 | `seq`、`id`、`time`、`request`、`request_size`、`response`、`response_size` | 自增主键 `seq`；`id` 唯一 |
| `upstream` | 一次上游尝试的正文 | `seq`、`id`、`attempt`、`time`、正文四列 | 自增主键 `seq`；`(id, attempt)` 唯一 |
| `quota_snapshots` | 一个额度桶某一时刻的剩余比例 | `time`、`account`、`bucket_id`、`window`、`remaining_fraction`、`reset_time` | 索引 `quota_snapshots_account_time(account, time)` |
| `logs` | 一条 WARN 以上日志 | `seq`、`time`、`level`、`source`、`event`、`message`、`request_id`、`model`、`data` | 自增主键 `seq`；索引 `logs_time(time)`、`logs_request_id(request_id)` |

- 汇总的 7 个维度列：`model`（公开模型名）、`account`、`api_key`、`protocol`、`state`、`quota_group`、`status`
- 汇总的累计列：`requests`、四类 token、`duration_ms`、`first_event_ms` 与 `queue_ms` 的总和，`first_events`（有首事件的请求数），`cost_usd`（有单价请求的费用之和），`unpriced`，耗时分布 `duration_hist` 与 `first_event_hist`，`last_time`（最晚完成时间）
- 时间列（`time`、`start`、`last_time`、`reset_time`）为 Unix 毫秒整数；`hours.start` 为 UTC 整点，`days.start` 为服务器本地日零点；接口读出的时间为 UTC 的 RFC 3339 文本
- `requests.stream` 为 0 或 1；`requests.cost_usd` 与 `quota_snapshots.reset_time` 没有值时为 NULL
- `requests.attempts` 为上游尝试的 JSON 数组，字段同上文，不含正文；没有尝试时为 `[]`
- `logs.data` 为 JSON 对象 `{"request": 请求记录, "attrs": 其余属性}`，没有的键省略
- 耗时分布列按分桶序号升序，每个非空分桶依次写两个无符号 varint：与前一个非空分桶的序号差、请求数；分桶规则见“用量报告”
- 正文列为 TEXT，按字节截断到 64 KiB，`request_size` 与 `response_size` 为原始字节数；`bodies.time` 与 `upstream.time` 为所属请求的完成时间；`upstream.attempt` 从 0 开始，与 `attempts` 的下标对应，上游正文按尝试计数保留最近 1000 条

### 记录查询与导出

`GET /api/usage/records` 返回完成时间在 `[from, to)` 内的记录，按完成时间倒序，完成时间相同时按 `id` 倒序。

- 维度筛选与 `status` 都按完整取值匹配，`status` 与维度筛选同时生效
- `q` 去掉首尾空白后按子串匹配 `id`、`client_request_id`、`response_id`、`cascade_id` 与 `error`，`%`、`_` 与 `\` 按字面匹配，ASCII 字母不区分大小写
- `next_cursor` 是 `<完成时间的 Unix 毫秒>|<id>` 的 base64url 编码（无填充），指向本页最后一条；以它为 `cursor` 时返回完成时间更早、或完成时间相同且 `id` 更小的记录

`GET /api/usage/records/{id}` 返回：

| 字段 | 内容 |
| --- | --- |
| `record` | 请求记录 |
| `body` | 公开协议正文，没有保存或已超出保留条数时为 `null` |
| `upstream` | 上游尝试正文，按尝试顺序，没有时为空数组 |

正文对象的字段为 `request`、`request_size`、`response`、`response_size`。公开协议的 `response` 是写给客户端的响应体，流式请求为 SSE 文本。

`GET /api/quota/snapshots` 返回范围内的额度快照，按 `time` 升序，同一时刻按写入顺序。账户每读取一次额度摘要，每个额度桶写一条快照，字段为 `time`、`account`、`bucket_id`、`window`、`remaining_fraction` 与 `reset_time`（没有时省略）。

`GET /api/usage/records.csv` 使用与记录页相同的范围、筛选与排序，导出全部符合条件的记录，`limit` 与 `cursor` 不参与导出：

- 文件名 `antigravity2api-requests-<from>-<to>.csv`，时间为 UTC 的 `YYYYMMDDTHHMMSSZ`；`Content-Type: text/csv; charset=utf-8`，以附件下载
- 开头为 UTF-8 BOM，首行为列名，列与顺序同 `requests` 表（`id` 到 `attempts` 共 35 列）
- 由 Go `encoding/csv` 写出：逗号分隔，含逗号、双引号、换行或以空白开头的值加双引号，值内的双引号写两次，行尾为 LF
- `time` 为 UTC 的 RFC 3339 文本，精确到毫秒；`stream` 为 `true` 或 `false`；`cost_usd` 为 `null` 时留空，否则为不带指数的最短十进制表示；`attempts` 为 JSON 数组
- 以 `=`、`+`、`-`、`@`、制表符或回车开头的文本前加单引号 `'`，适用于 `client_request_id`、`response_id`、`path`、`api_key`、`user_agent`、`model`、`upstream_model`、`model_version`、`request_type`、`cascade_id`、`account`、`error_code`、`error`

### 用量报告

`GET /api/usage`（参数见 [开发文档](development.md) “用量与记录查询”）返回：

| 字段 | 内容 |
| --- | --- |
| `from`、`to` | 查询范围，UTC |
| `bucket_seconds` | 分桶时长（秒） |
| `generated_at` | 生成报告的时间 |
| `latest_at` | 账本中最晚一条记录的完成时间，没有记录时省略 |
| `totals` | 范围内的汇总 |
| `previous` | 等长的上一周期汇总 |
| `recent` | 最近 `minutes`（5）分钟的 `requests` 与 `tokens`，用于当前吞吐 |
| `buckets` | 逐桶汇总，`at` 为分桶起点 |
| `stack_by`、`series` | 堆叠维度，前 8 个取值逐桶的请求数与 token，其余合并为 `other` |
| `groups` | 按模型、账户、API key、协议、结果、额度组与状态码的分项 |
| `pairs` | 账户与模型组合的分项 |
| `options` | 各维度的筛选候选 |

每个汇总的字段：

| 字段 | 内容 |
| --- | --- |
| `requests` | 请求数 |
| `succeeded` | `completed`、`tool_calls`、`limited` 与 `blocked` |
| `failed`、`canceled` | 失败、取消 |
| `rate_limited` | 状态码 429 |
| `input_tokens`、`cached_tokens`、`reasoning_tokens`、`reply_tokens`、`total_tokens` | token 用量 |
| `cost_usd`、`unpriced` | 有单价请求的费用之和、消耗 token 但没有单价的请求数 |
| `duration`、`first_event` | 成功请求的耗时与首事件耗时：`avg_ms`、`p50_ms`、`p95_ms`、`p99_ms` |
| `queue_avg_ms` | 平均排队时间 |
| `last_at` | 最后一个请求的时间 |

分位数取自对数分桶的耗时分布，相对误差约 0.5%。

- 耗时按毫秒记入 1840 个分桶：不大于 0 的值记入 0 号；正值 v 记入 ⌊ln v / ln 1.01⌋ + 1 号，第 b 号覆盖 [1.01^(b−1), 1.01^b) 毫秒；1.01^1838 ≈ 87,636,498 毫秒（约 24.3 小时）及以上的值都记入最后的 1839 号
- 分位数按最近秩法：N 个样本的 p 分位取升序第 max(⌈p × N⌉, 1) 个样本所在的分桶，返回该分桶上下界的几何中点 1.01^(b−0.5) 毫秒，0 号分桶返回 0；1839 号分桶内的值不受 0.5% 误差限制
- `avg_ms` 为毫秒总和除以样本数，不经分桶
- `duration` 的样本为成功请求，`first_event` 的样本为有首事件的成功请求；没有样本时各项为 0
- `queue_avg_ms` 按全部请求平均；`total_tokens` 为输入、思考与回复 token 之和，缓存部分已含在输入中；没有请求时省略 `last_at`

范围与对比时段：

- 查询范围为 `[from, to)`：完成时间不早于 `from` 且早于 `to` 的记录计入 `totals`、`buckets`、`series`、`groups` 与 `pairs`
- `previous` 的范围为 `[from − (to − from), from)`，与查询范围等长并紧接其前，筛选与查询相同
- `recent` 的范围为生成时间前 5 分钟到生成时间（含），与 `from`、`to` 无关，筛选与查询相同
- `latest_at` 不受范围与筛选限制
- `options` 列出 6 个筛选维度在查询范围内出现过的取值，按字节序升序，空值以空串列出：没有筛选时取自本次统计的记录，有筛选时取自范围内的全部记录，不受筛选影响；对比时段的取值不计入

分桶：

- `bucket` 为 1 天时，分桶为 `tz` 中的自然日：第一个分桶从 `from` 所在当地日的零点开始，之后每个分桶从下一个当地零点开始，夏令时切换日为 23 或 25 小时；当地零点因夏令时不存在时，以 Go `time.Date` 归一化后的时刻为界
- 其余分桶按 `from` 时刻在 `tz` 中的 UTC 偏移对齐到当地时间的整分桶，之后每个分桶等长；范围内的偏移变化不改变对齐
- 第一个分桶的起点可以早于 `from`，最后一个分桶可以延续到 `to` 之后，分桶只统计范围内的记录；`at` 为分桶起点的 UTC 时间
- 汇总只在服务器本地日或 UTC 整小时完整落在一个分桶内时读取：分桶小于 1 小时时范围内全部读取原始记录；按小时分桶且 `tz` 偏移不是整小时时读取原始记录；按天分桶时，`tz` 自然日与服务器本地日不重合的日子读取小时汇总，跨越当地零点的 UTC 小时读取原始记录；`previous` 不分桶，不受此限制

排名：

- `groups` 每个维度按请求数降序，请求数相同时按取值的字节序升序；`status` 的取值为十进制文本
- `pairs` 按请求数降序，其次按账户、模型的字节序升序
- `series` 取 `groups[stack_by]` 的前 8 名，按名次各成一个系列；第 9 名起合并为最后一个系列，`other` 为 `true`，`key` 为空串；取值不超过 8 个时没有该系列
- 各系列的 `requests` 与 `tokens` 是与 `buckets` 等长的数组，`tokens` 为输入、思考与回复 token 之和

## 管理事件流

`GET /api/events` 是 SSE 流，每个 `data` 行为 `{"type":"<类型>","data":<对象>}`：

| 类型 | 内容 |
| --- | --- |
| `accounts` | 全部账户，内容变化时推送，字段同 `GET /api/accounts` |
| `log` | 一条日志，字段同 `GET /api/logs` 的条目 |
| `request` | 请求开始时的进行中记录（`state` 为 `running`）与结束时的完整记录 |
| `login` | 管理端登录的状态变化 |

连接建立时依次收到当前账户列表、最近 200 条日志与按开始时间排序的进行中请求，随后接收增量事件。空闲 30 秒发送 `: ping` 注释帧。单个连接堆积超过 1024 条未发送事件时服务端关闭该连接，管理端重新连接。

日志条目字段：`seq`（入库序号，实时事件没有）、`time`、`level`、`source`、`event`、`message`、`request_id`、`model`、`request`（请求开始与结束事件）与 `attrs`（其余属性）。

`GET /api/logs` 按 `seq` 倒序返回一页已入库日志，还有更早的日志时带 `next_before`，下一页以 `before=<next_before>` 读取；`level=ERROR` 只返回 ERROR，`q` 匹配消息与属性文本。`DELETE /api/logs` 清空内存与账本中的日志。
