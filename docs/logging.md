# 运行日志与请求账本

服务日志由 Go `slog` 写出，同一条事件同时进入三处：

| 去向 | 内容 |
| --- | --- |
| 标准错误与 `RUNTIME_DIR/logs/antigravity2api.log` | 达到 `LOG_LEVEL` 的全部事件，格式由 `LOG_FORMAT` 决定 |
| 管理事件流 `GET /api/events` | 同一组事件 |
| 请求账本 `RUNTIME_DIR/requests.db` 的 `logs` 表 | 同一组事件中的 WARN 与 ERROR，保留 90 天 |

- 三处共用 `LOG_LEVEL` 过滤：低于 `LOG_LEVEL` 的事件不进入任何一处，`LOG_LEVEL=error` 时 `logs` 表只有 ERROR
- 管理事件流的日志保存在服务内存中，达到 2200 条时裁剪为最近 2000 条；新连接先收到其中最近 200 条
- 管理端“日志”页的实时视图来自管理事件流，保留最近 2000 条；已保存视图来自 `logs` 表

`LOG_LEVEL`、`LOG_FORMAT`、`LOG_FILE`、`LOG_MAX_TOTAL_MB` 与 `REQUEST_LOG_BODIES` 在管理端设置页保存后立即生效。

| 文件 | 规则 |
| --- | --- |
| `antigravity2api.log` | `LOG_FILE=true` 时写入，达到 10 MB 时轮转 |
| `antigravity2api-<本地时间>.log` | 轮转出的旧文件，不压缩 |
| `crash.log` | 运行时崩溃报告，不轮转 |

- 日志文件由 `natefinch/lumberjack` v2 管理
- 旧文件名中的时间为服务器本地时间，格式为 `2006-01-02T15-04-05.000`；保留 `LOG_MAX_TOTAL_MB / 10 − 1`（整数除法）个，超出时删除最旧的
- `crash.log` 在服务启动时以追加方式打开，经 Go `debug.SetCrashOutput` 接收未恢复的 panic 与致命错误的消息及全部 goroutine 栈，内容与标准错误上的崩溃输出相同

## 日志结构

JSON 格式每行一条事件：

```json
{"time":"2026-10-15T09:11:14.507+08:00","level":"INFO","caller":"app/app.go:166","msg":"200 | 3.113s | 127.0.0.1 | POST /v1/responses","source":"http","event":"http.access","status":200,"latency_ms":3112,"client_ip":"127.0.0.1","method":"POST","path":"/v1/responses","request_id":"req_nka6uxkxk4ssb6g4rvcbsu6mpv"}
```

| 字段 | 含义 |
| --- | --- |
| `time` | 本地时间，RFC 3339 |
| `level` | `DEBUG`、`INFO`、`WARN`、`ERROR` |
| `caller` | 源文件位置，如 `app/app.go:166` |
| `msg` | 中文消息，如 `摘要 \| 键=值` |
| `source` | 事件来源 |
| `event` | 稳定事件名，请求与上游事件带此字段 |
| `request_id` | 请求 ID，与响应头 `X-Request-ID` 相同 |
| `request` | 请求开始与结束事件附带的请求记录 |

- `caller` 为写日志的源文件所在目录名、文件名与行号；`msg` 形如 `摘要 | 键=值 | 键=值`
- 请求 ID 为 `req_` 加 Go `crypto/rand.Text()` 的 26 个字符转小写，字母表为 `a–z` 与 `2–7`；`request` 的字段见“请求账本”
- 属性键为英文：全部事件带 `source`；请求与上游事件带 `event`；带请求 ID 的事件带 `request_id`；`request.started` 与 `request.finished` 带 `request`；`http.access` 另带 `status`、`latency_ms`、`client_ip`、`method`、`path`
- `msg` 中的 `键=值` 只是消息文本，不另作属性
- `request_id` 由日志处理器从请求的 context 取出后加入；`上游尝试完成` 与 `上游尝试失败` 显式带 `request_id`
- 不带 `source` 的事件只有 `没有找到 WebView2 运行时，管理端在默认浏览器中打开`，在管理事件流与 `logs` 表中来源记为 `service`

`text` 格式为 slog 文本格式，字段相同。下文的日志示例只列出每条事件的 `level`、`source` 与 `msg` 三项，以空格对齐，省略其余字段；它们是阅读用的摘要，实际输出为上面的 JSON 或 slog 文本格式。

| `source` | 事件 |
| --- | --- |
| `service` | 启动、账户池刷新、配置保存、版本检查、账本维护 |
| `http` | 访问日志 |
| `request` | 尚未分配账户的请求事件、管理端停止请求 |
| `auth` | 登录、导入、远程配对、删除账户 |
| 账户邮箱 | 该账户的初始化、上游请求、冷却与已分配账户的请求事件 |
| `login` | 登录前虚拟进程的上游请求 |

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
INFO  service  官方客户端最新版本 | 版本=2.29.0 当前=2.28.1
INFO  service  账户池刷新完成 | 耗时=40.272s
```

监听建立后写 `服务就绪`，随后在后台逐个初始化账户（顺序见 [开发文档](development.md) “初始化”）：

| 时机 | 消息 | 级别 | 来源 |
| --- | --- | --- | --- |
| 监听建立 | `服务就绪 \| 地址=...` | INFO | `service` |
| 账户的初始化、周期请求与 clearcut 上报 | `上游请求 \| 方法 主机路径 \| 状态=... \| 耗时=...` | INFO，失败时 WARN | 账户 |
| 单个账户初始化失败 | `账户初始化失败 \| 错误=...` | ERROR | 账户 |
| 账户池刷新结束 | `账户池刷新完成 \| 耗时=...` | INFO | `service` |
| 账户池刷新有账户失败 | `账户池刷新部分失败 \| 耗时=... \| 错误=...` | WARN | `service` |
| 读取全部账户的额度后 | `额度刷新完成 \| 失败=... \| 耗时=...` | INFO | `service` |
| 单个账户读取额度失败 | `读取额度摘要失败 \| 错误=...` | WARN | 账户 |
| 读取官方更新清单 | `官方客户端最新版本 \| 版本=... 当前=...` | INFO | `service` |
| 读取官方更新清单失败 | `读取官方客户端最新版本失败 \| 错误=...` | WARN | `service` |
| 重新初始化失败的账户 | `异常账户重新初始化完成 \| 重试=... \| 仍失败=...` | INFO | `service` |
| 同步账户目录失败 | `读取账户目录失败 \| 错误=...` | WARN | `service` |
| 账户目录有变化 | `账户目录已变化，刷新账户池` | INFO | `service` |
| 检测到代理不可用 | `代理不可用 \| 代理=... \| 原因=...` | WARN | `service` |
| 保存 `cooldowns.json` 失败 | `保存冷却失败 \| 错误=...` | WARN | `service` |
| 保存 `warmups.json` 失败 | `保存周额度唤醒记录失败 \| 错误=...` | WARN | `service` |
| 桌面版没有 WebView2 运行时 | `没有找到 WebView2 运行时，管理端在默认浏览器中打开` | WARN | 无，记为 `service` |

- “账户”指该账户的邮箱；登录前虚拟进程的 `上游请求` 来源为 `login`
- 单个账户初始化失败时该账户移出调度
- 官方更新清单在启动 10 秒后读取，此后每小时一次；失败的账户在每次定时读取额度后重新初始化；账户目录每 2 秒同步一次；代理池每次检测时每个不可用的代理各写一条
- `代理不可用` 中的代理地址密码替换为 `xxxxx`

## 访问日志

每个 HTTP 请求结束写一条 `http.access`，消息为 `状态 | 耗时 | 客户端 IP | 方法 路径`，属性带 `status`、`latency_ms`、`client_ip`、`method`、`path`。级别：5xx 为 ERROR，4xx 为 WARN，静态资源为 DEBUG，其余为 INFO。客户端 IP 为直连地址。

## API 请求

通过 API key 校验的公开 POST 请求写开始、进展与结束三类事件，都带 `request_id`；不调用生成的请求只写结束事件（见下方“不调用生成的请求”）：

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

结束消息带状态码、耗时、账户、公开模型、上游模型与 token 用量，失败、取消与终止时另带 `错误=`。客户端断开与管理端停止记为 `499`；等待上游超过 `REQUEST_TIMEOUT` 记为 `504`，错误阶段为 `timeout`。

不调用生成的请求：Files 上传、计数端点、Responses 与 Interactions 的 `/cancel` 等公开 POST 同样从进入起记为进行中，出现在 `GET /api/requests` 与事件流快照中，可由管理端停止：

| 项 | 取值 |
| --- | --- |
| `请求开始` 日志、开始时的 `request` 事件 | 无 |
| 结束 | `request.finished` 日志、`request` 事件与账本记录 |
| 结束日志的 `source` | `request` |
| `model` | 请求体的 `model`，其次路径中的模型 |
| `request_type`、`response_id`、`account` | 空串 |
| `state` | 按“请求账本”的规则 |
| 失败时的 `error_phase` | 状态码小于 500 为 `request`，否则 `internal` |

## 流式等待

生成事件流连续 15 秒没有新事件时写一条 WARN，下一个事件到达时写恢复：

```text
WARN  account@example.com  上游首事件等待 | 模型=gemini-3.8-flash-high | 已等待=15s
DEBUG account@example.com  上游首事件到达 | 模型=gemini-3.8-flash-high | 等待=21.448s | 事件=reasoning
WARN  account@example.com  事件流停顿 | 模型=gemini-3.8-flash-high | 已等待=15s | 最近事件=text
INFO  account@example.com  事件流恢复 | 模型=gemini-3.8-flash-high | 停顿=17.208s | 当前事件=text
```

客户端断开后服务继续读完上游事件流，账本记下完整用量；上面四条等待诊断只在客户端连接期间写出。

## 账户事件

生成失败时按原因处理账户（规则见 [开发文档](development.md) “失败与换号”），每种处理写一条以账户为来源的事件：

| 消息 | 级别 | 处理 |
| --- | --- | --- |
| `账户认证失败，移出调度 \| 错误=...` | WARN | token 刷新被拒或重发后仍为 401 |
| `账户需要验证，停止请求` | WARN | 需要验证 |
| `账户违反服务条款，已停用` | ERROR | 违反条款 |
| `账户冷却 \| 范围=... \| 时长=... \| 原因=... \| 系统提示档位=...` | WARN | 额度组或模型冷却 |
| `同额度池账户冷却 \| 范围=... \| 时长=... \| 耗尽账户=...` | WARN | 以同一共享额度池的其他账户为来源，各写一条 |
| `模型容量不足，换账户重试 \| 模型=...` | WARN | 容量不足 |
| `上游拒绝请求内容，不冷却账户 \| 原因=... \| 系统提示档位=...` | WARN | 没有 ErrorInfo 与恢复时刻的 429 |
| `上游流在客户端收到事件前中断，换账户重新生成` | WARN | 流中错误换账户，以新账户为来源 |

账户运行中的其他事件：

| 消息 | 级别 |
| --- | --- |
| `token 已刷新 \| 到期=...` | INFO |
| `读取额度摘要失败 \| 错误=...` | WARN |
| `关闭账户遥测失败 \| 错误=...` | WARN |
| `保存虚拟 Hub 状态失败 \| 错误=...` | WARN |
| `写入会话记录失败 \| 错误=...` | WARN |
| `保存账户目录失败 \| 错误=...` | WARN |
| `周额度唤醒完成 \| 额度桶=... \| 模型=... \| 耗时=...` | INFO |
| `周额度唤醒失败 \| 额度桶=... \| 模型=... \| 错误=...` | WARN |

`上游请求 | 方法 主机路径 | 状态=... | 耗时=...ms` 记录生成以外的每次上游请求（初始化、目录、额度、unleash、clearcut 上报、遥测设置），失败时为 WARN 并带 `错误=`。DEBUG 级别另记录每次上游连接：`上游连接 | 主机=... | 复用=... | 空闲=... | 代理=...`。

管理端与命令行的账户操作：

| 消息 | 级别 | 来源 |
| --- | --- | --- |
| `账户登录开始 \| 方式=browser/code \| 登录=...` | INFO | `auth` |
| `账户添加完成 \| 账户=...` | INFO | `auth` |
| `账户登录已取消 \| 登录=...` | WARN | `auth` |
| `账户添加失败 \| 登录=... \| 错误=...` | ERROR | `auth` |
| `官方客户端账户导入完成 \| 账户=...` | INFO | `auth` |
| `官方客户端账户导入失败 \| 错误=...` | ERROR | `auth` |
| `远程配对令牌已生成 \| 有效期=10m` | INFO | `auth` |
| `远程配对导入完成 \| 账户=...` | INFO | `auth` |
| `远程配对导入失败 \| 错误=...` | ERROR | `auth` |
| `账户已删除 \| 账户=...` | INFO | `auth` |
| `账户已启用`、`账户已停用` | INFO | 账户 |
| `账户代理已设置 \| 代理=...` | INFO | 账户 |
| `账户优先级已设置 \| 优先级=...` | INFO | 账户 |
| `账户备注名已设置 \| 备注名=...` | INFO | 账户 |
| `账户 AI 积分超额使用已设置 \| 开启=true/false` | INFO | 账户 |
| `账户验证完成 \| 耗时=...` | INFO | 账户 |
| `账户验证失败 \| 耗时=... \| 错误=...` | ERROR | 账户 |
| `读取额度摘要失败 \| 错误=...` | WARN | 账户 |
| `官方客户端已切换到该账户并重新启动` | INFO | 账户 |
| `官方客户端切换账户失败 \| 错误=...` | ERROR | 账户 |
| `服务配置已保存 \| 变更=...` | INFO | `service` |
| `管理端停止请求 \| 请求=...` | WARN | `request` |
| `请求记录导出失败 \| 错误=...` | ERROR | `service` |

账户代理在日志中显示时密码替换为 `xxxxx`，跟随全局时为 `跟随全局`。命令行的 `login`、`import` 与 `switch` 只向终端输出。

在线更新（`source` 为 `service`）：

| 时机 | 消息 | 级别 |
| --- | --- | --- |
| 开始下载 | `在线更新 \| 阶段=1/2 下载发行包 \| 版本=...` | INFO |
| 命令行版替换可执行文件 | `在线更新 \| 阶段=2/2 替换可执行文件 \| 版本=...` | INFO |
| 桌面窗口暂存新版本 | `在线更新 \| 阶段=2/2 暂存新版本 \| 版本=...` | INFO |
| 安装完成，即将重启 | `在线更新完成 \| 版本=...` | INFO |
| 下载、校验或安装失败 | `在线更新失败 \| 错误=...` | ERROR |
| 桌面窗口重启失败 | `在线更新重启失败 \| 错误=...` | ERROR |

本文各表与示例列出服务写出的全部日志消息。

## 请求账本

`RUNTIME_DIR/requests.db` 是 SQLite（WAL）账本。每个公开 POST 请求（API key 与配置的某个 key 相同，或未配置 key）结束后写入一行请求记录，入账条件见 [开发文档](development.md) “目录、组件与依赖”的处理链；写入由后台协程按批提交，响应先于写入完成，等待写入的条目超过 4096 条时丢弃新条目。

| 字段 | 内容 |
| --- | --- |
| `id`、`time` | 请求 ID、完成时间 |
| `client_request_id` | 客户端发来的 `X-Request-ID` |
| `response_id` | 公开协议响应中的 ID |
| `protocol`、`path` | 协议（下表）、请求路径 |
| `api_key` | API key 掩码，见表下 |
| `client_ip`、`user_agent`、`stream` | 客户端 IP、User-Agent、是否流式 |
| `model` | 公开模型名 |
| `upstream_model`、`model_version` | 发往上游的模型 ID、上游返回的模型版本 |
| `request_type`、`prompt_tier` | 上游请求类型、系统提示档位，取值见下 |
| `cascade_id`、`step_index` | 上游会话 ID 与步序号，取值见下表 |
| `account`、`quota_group` | 最终执行账户、额度组 |
| `status`、`state` | HTTP 状态码、请求结果 |
| `error_phase` | 失败阶段：`request`、`routing`、`upstream`、`network`、`timeout`、`internal` |
| `upstream_status`、`error_code` | 最后一次上游尝试的状态码与错误码 |
| `duration_ms` | 请求进入到结束的耗时 |
| `first_event_ms` | 请求进入到首个正文、思考或工具调用事件，管理端显示为“首字” |
| `queue_ms` | 请求进入到取得首个账户 |
| `input_tokens`、`cached_tokens` | 输入 token、其中命中缓存的部分 |
| `reasoning_tokens`、`reply_tokens` | 思考 token、回复 token |
| `tool_calls` | 工具调用数 |
| `cost_usd` | 估算美元费用，模型没有单价时为 `null` |
| `error` | 错误摘要 |
| `attempts` | 全部上游尝试 |

- `api_key` 保留前 `min(3, n/4)` 与后 `min(4, n/4)` 个字节，中间为 `…`（n 为 key 的字节数，整数除法），未配置 key 时为空串

| 请求 | `request_type` |
| --- | --- |
| Agent 模型与普通对话模型 | `agent` |
| 图像生成 | `image_gen` |
| 语音转写 | `audio_transcription` |
| Tab 补全 | `tab` |
| 没有选到账户 | 空串 |

- `prompt_tier` 为请求开始时 `SYSTEM_PROMPT` 的值，每条记录都写；图像生成、语音转写与 Tab 补全不套用系统提示档位，该列只记下当时的设置

| 请求 | `cascade_id` | `step_index` |
| --- | --- | --- |
| Agent 模型与普通对话模型 | 会话的 `cascade_id` | 与 `requestId` 末段相同 |
| 图像生成 | 会话的 `cascade_id` | 与 `requestId` 末段相同 |
| 语音转写 | 会话的 `cascade_id` | 0 |
| Tab 补全 | 空串 | 0 |
| 没有选到账户 | 空串 | 0 |

- `requestId` 末段为协议参考“labels 与会话标识”中的 S，图像生成请求为 S+1

`attempts` 是 JSON 数组，每次上游尝试一项，字段顺序固定，可省略的字段为空时不写：

```json
{"account":"alice@example.com","url":"https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse","status":429,"code":"QUOTA_EXHAUSTED","retry_delay":"6h0m0s","error":"You have exhausted your capacity on this model. Your quota will reset after 6h0m0s.","duration_ms":812}
```

| 字段 | 内容 |
| --- | --- |
| `account` | 尝试使用的账户 |
| `url` | 上游地址，见下 |
| `status` | 上游 HTTP 状态码；连接错误为 0；流开始后中断为 200 |
| `code` | 可省略，上游错误码，见下 |
| `retry_delay` | 可省略，上游给出的重试等待原文，见下 |
| `trace_id`、`response_id` | 可省略；上游最后一个事件的 `traceId` 与 `responseId` |
| `error` | 可省略，错误文本，见下 |
| `duration_ms` | 该次尝试的耗时毫秒数 |

- `url`：生成与 Tab 补全为 `<base>/v1internal:streamGenerateContent?alt=sse`，图像生成为 `<base>/v1internal:generateContent`，语音转写为 `https://aicode.googleapis.com/google.gca.aicode.v1alpha.PredictionService/BidiGenerateContent`；没有发出请求时为空串
- `code`、`retry_delay` 与 `error` 在状态码不小于 400 时从错误体读取，错误体是 JSON 数组时取第一项：
  - `code`：`details` 中最后一个非空的 `reason`，没有时为 `error.status`
  - `retry_delay`：`details` 中最后一个非空的 RetryInfo `retryDelay` 原文（如 `3.2s`），没有时为最后一个非空的 ErrorInfo `metadata.quotaResetDelay` 原文（如 `6h0m0s`）
  - `error`：`error.message`
- 连接错误与流中断时 `error` 为错误文本；连接错误的定义见开发文档“失败与换号”

请求结束时按以下顺序判定 `state`，取第一条成立的规则：

1. 等待上游超过 `REQUEST_TIMEOUT` 且上游没有发出终止事件：`failed`，`status` 为 504，`error_phase` 为 `timeout`
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
- 超时为 `等待上游超过 REQUEST_TIMEOUT`
- `cancelled` 为 `客户端断开连接`，管理端停止时为 `管理端停止了请求`，后台响应被取消时为 `客户端取消了后台响应`
- `blocked` 为终止原因原文
- 其余 `failed` 依次取生成错误的文本、响应体的 `error.message`、状态码的标准说明（如 `Bad Gateway`）

第 3 条规则判定的 `failed` 按下表顺序取 `error_phase`；除超时外，其他结果的 `error_phase` 为空串：

| 条件 | `error_phase` |
| --- | --- |
| 未调用生成，状态码小于 500 | `request` |
| 未调用生成 | `internal` |
| 没有上游尝试 | `routing` |
| 最后一次尝试为连接错误 | `network` |
| 其余 | `upstream` |

`upstream_status` 与 `error_code` 取最后一次上游尝试的 `status` 与 `code`，没有尝试时为 0 与空串。

| 路径 | `protocol` |
| --- | --- |
| `/v1/chat/completions` | `openai-chat` |
| `/v1/responses` 及其子路径（含 `/v1/responses/input_tokens`、`/v1/responses/compact`） | `openai-responses` |
| `/v1/messages` 及其子路径（含 `/v1/messages/count_tokens`） | `anthropic` |
| `/v1beta/` 下的路径（含 `:countTokens`、`/v1beta/interactions`、`/v1beta/files`） | `gemini` |
| 其余（`/v1/completions`、`/v1/images/*`、`/v1/audio/*`、`/v1/files`、`/v1/interactions`、`/upload/v1beta/files`） | `other` |

上游尝试的请求与响应正文：失败的请求不论 `REQUEST_LOG_BODIES` 取值都保存，成功的请求只在 `REQUEST_LOG_BODIES=true` 时保存。公开协议的请求与响应正文只在 `REQUEST_LOG_BODIES=true` 时保存。正文为明文，各截断到 64 KiB 并记录原始字节数，公开正文与上游正文各保留最近 1000 条。

汇总与保留：

- 累加：写入请求记录的同一事务按 UTC 小时（`hours` 表）与服务器本地日（`days` 表）累加汇总，汇总含请求数、各类 token、费用与耗时分布
- 读取：用量报告何时读取汇总、何时读取原始记录见“用量报告”的分桶规则
- 时区变化：每次打开账本时检查 `days` 表中各个不同的 `start`，任一值不是当前服务器时区的本地零点时，删除全部本地日汇总，从最早的小时汇总起到当前时间按天重建，并写 `请求账本已按服务器时区重建本地日汇总`
- 保留：请求记录、汇总、正文、额度快照与日志保留 90 天；服务启动时与之后每 24 小时清理一次；截止时刻为当前时间减 90 天所在本地日的零点，`days` 按它删除，其余表按它向下取整到 UTC 整点后的时刻删除

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

建表语句（`internal/requestdb/requestdb.go` 的 `schema`，`hours` 与 `days` 共用 `rollupColumns`）：

```sql
CREATE TABLE IF NOT EXISTS requests (
 id TEXT PRIMARY KEY, time INTEGER NOT NULL, client_request_id TEXT NOT NULL, response_id TEXT NOT NULL,
 protocol TEXT NOT NULL, path TEXT NOT NULL, api_key TEXT NOT NULL, client_ip TEXT NOT NULL, user_agent TEXT NOT NULL,
 stream INTEGER NOT NULL, model TEXT NOT NULL, upstream_model TEXT NOT NULL, model_version TEXT NOT NULL,
 request_type TEXT NOT NULL, prompt_tier TEXT NOT NULL, cascade_id TEXT NOT NULL, step_index INTEGER NOT NULL,
 account TEXT NOT NULL, quota_group TEXT NOT NULL, status INTEGER NOT NULL, state TEXT NOT NULL,
 error_phase TEXT NOT NULL, upstream_status INTEGER NOT NULL, error_code TEXT NOT NULL,
 duration_ms INTEGER NOT NULL, first_event_ms INTEGER NOT NULL, queue_ms INTEGER NOT NULL,
 input_tokens INTEGER NOT NULL, cached_tokens INTEGER NOT NULL, reasoning_tokens INTEGER NOT NULL, reply_tokens INTEGER NOT NULL,
 tool_calls INTEGER NOT NULL, cost_usd REAL, error TEXT NOT NULL, attempts TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS requests_time ON requests(time);
CREATE INDEX IF NOT EXISTS requests_model_time ON requests(model, time);
CREATE INDEX IF NOT EXISTS requests_account_time ON requests(account, time);
CREATE INDEX IF NOT EXISTS requests_state_time ON requests(state, time);
CREATE TABLE IF NOT EXISTS hours (
 start INTEGER NOT NULL, model TEXT NOT NULL, account TEXT NOT NULL, api_key TEXT NOT NULL, protocol TEXT NOT NULL,
 state TEXT NOT NULL, quota_group TEXT NOT NULL, status INTEGER NOT NULL, requests INTEGER NOT NULL,
 input_tokens INTEGER NOT NULL, cached_tokens INTEGER NOT NULL, reasoning_tokens INTEGER NOT NULL, reply_tokens INTEGER NOT NULL,
 duration_ms INTEGER NOT NULL, first_event_ms INTEGER NOT NULL, first_events INTEGER NOT NULL, queue_ms INTEGER NOT NULL,
 cost_usd REAL NOT NULL, unpriced INTEGER NOT NULL, duration_hist BLOB NOT NULL, first_event_hist BLOB NOT NULL, last_time INTEGER NOT NULL,
 PRIMARY KEY (start, model, account, api_key, protocol, state, quota_group, status)) WITHOUT ROWID;
-- days 与 hours 的列和主键相同
CREATE TABLE IF NOT EXISTS bodies (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, time INTEGER NOT NULL,
 request TEXT NOT NULL, request_size INTEGER NOT NULL, response TEXT NOT NULL, response_size INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS upstream (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL, attempt INTEGER NOT NULL, time INTEGER NOT NULL,
 request TEXT NOT NULL, request_size INTEGER NOT NULL, response TEXT NOT NULL, response_size INTEGER NOT NULL,
 UNIQUE (id, attempt));
CREATE TABLE IF NOT EXISTS quota_snapshots (
 time INTEGER NOT NULL, account TEXT NOT NULL, bucket_id TEXT NOT NULL, window TEXT NOT NULL,
 remaining_fraction REAL NOT NULL, reset_time INTEGER);
CREATE INDEX IF NOT EXISTS quota_snapshots_account_time ON quota_snapshots(account, time);
CREATE TABLE IF NOT EXISTS logs (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER NOT NULL, level TEXT NOT NULL, source TEXT NOT NULL,
 event TEXT NOT NULL, message TEXT NOT NULL, request_id TEXT NOT NULL, model TEXT NOT NULL, data TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS logs_time ON logs(time);
CREATE INDEX IF NOT EXISTS logs_request_id ON logs(request_id);
```

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
| `recent` | `{"minutes":5,"requests","tokens"}`，最近 5 分钟的请求数与 token，用于当前吞吐 |
| `buckets` | 逐桶汇总数组，每项为 `at`（分桶起点）加汇总字段 |
| `stack_by` | 堆叠维度 |
| `series` | 堆叠系列数组 |
| `groups` | 按维度的分项 |
| `pairs` | 按账户与模型的分项 |
| `options` | 各筛选维度的可选值 |

- `series` 每项为 `{"key","other","requests":[…],"tokens":[…]}`，`other` 只在合并项出现
- `groups` 的键为 `model`、`account`、`api_key`、`protocol`、`state`、`quota_group`、`status`，值为数组，每项为 `key`（该维度的取值）加汇总字段；没有记录时为 `[]`
- `pairs` 每项为 `account`、`model` 加汇总字段
- `options` 的键为 `model`、`account`、`api_key`、`protocol`、`state`、`quota_group`，值为字符串数组

```json
{
  "from": "2026-10-22T08:00:00Z", "to": "2026-10-23T08:00:00Z", "bucket_seconds": 3600,
  "generated_at": "2026-10-23T08:00:00Z", "latest_at": "2026-10-23T07:58:12Z",
  "totals": {<汇总>}, "previous": {<汇总>},
  "recent": {"minutes": 5, "requests": 3, "tokens": 18230},
  "buckets": [{"at": "2026-10-22T08:00:00Z", <汇总字段>}, ...],
  "stack_by": "model",
  "series": [{"key": "gemini-3.8-flash", "requests": [4, ...], "tokens": [9120, ...]}, {"key": "", "other": true, "requests": [1, ...], "tokens": [800, ...]}],
  "groups": {"model": [{"key": "gemini-3.8-flash", <汇总字段>}], "account": [], "api_key": [], "protocol": [], "state": [], "quota_group": [], "status": [{"key": "200", <汇总字段>}]},
  "pairs": [{"account": "alice@example.com", "model": "gemini-3.8-flash", <汇总字段>}],
  "options": {"model": ["gemini-3.8-flash"], "account": ["alice@example.com"], "api_key": [""], "protocol": ["openai-chat"], "state": ["completed"], "quota_group": ["gemini"]}
}
```

每个汇总的字段：

| 字段 | 内容 |
| --- | --- |
| `requests` | 请求数 |
| `succeeded` | `completed`、`tool_calls`、`limited` 与 `blocked` |
| `failed`、`canceled` | 结果为 `failed`、`cancelled` 的请求数；汇总字段名写作 `canceled` |
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
- 汇总只在服务器本地日或 UTC 整小时完整落在查询范围与一个分桶内时读取，日汇总优先，其余部分读取小时汇总，剩下的读取原始记录：分桶小于 1 小时时范围内全部读取原始记录；按小时分桶且 `tz` 偏移不是整小时时读取原始记录；按天分桶时，`tz` 自然日与服务器本地日不重合的日子读取小时汇总，跨越当地零点的 UTC 小时读取原始记录；`previous` 不分桶，不受此限制

排名：

- `groups` 每个维度按请求数降序，请求数相同时按取值的字节序升序；`status` 的取值为十进制文本
- `pairs` 按请求数降序，其次按账户、模型的字节序升序
- `series` 取 `groups[stack_by]` 的前 8 名，按名次各成一个系列；第 9 名起合并为最后一个系列，`other` 为 `true`，`key` 为空串；取值不超过 8 个时没有该系列
- 各系列的 `requests` 与 `tokens` 是与 `buckets` 等长的数组，`tokens` 为输入、思考与回复 token 之和

## 管理事件流

`GET /api/events` 是 SSE 流，每个 `data` 行为 `{"type":"<类型>","data":<对象>}`：

| 类型 | 内容 |
| --- | --- |
| `accounts` | 账户数组，每项同 `GET /api/accounts` 的账户，内容变化时推送 |
| `log` | 一条日志，字段同 `GET /api/logs` 的条目 |
| `request` | 请求开始时的进行中记录与结束时的完整记录 |
| `login` | 管理端登录的状态变化 |

- `accounts` 的 `data` 为数组本身，例如 `{"type":"accounts","data":[{"id":"alice@example.com",...}]}`
- 进行中记录的 `state` 为 `running`，`time` 为开始时间，`duration_ms` 为截至推送时的已用时间；请求开始时还没有分配账户，`account` 为空串
- 连接建立时与 `GET /api/requests` 给出的进行中记录取当时的值，`account` 为最近一次分配的账户
- 生成请求开始时推送进行中记录，之后分配账户与换账户时各再推送一次

连接建立时依次收到当前账户列表、服务内存中最近 200 条日志与按开始时间升序的进行中请求，随后接收增量事件。连接期间每 30 秒发送一次 `: ping` 注释帧。单个连接在初始事件之外堆积超过 1024 条未发送事件时服务端关闭该连接，管理端重新连接。

日志条目字段：`seq`（入库序号，实时事件没有）、`time`、`level`、`source`、`event`、`message`、`request_id`、`model`、`request`（请求开始与结束事件）与 `attrs`（其余属性）。

- `source`、`event` 取同名属性，没有 `source` 时为 `service`
- `request` 属性为请求记录时，`request` 为去掉上游正文的该记录，`request_id` 与 `model` 取记录的 `id` 与 `model`（公开模型名）；其余事件的 `model` 为空串
- `request_id` 另取同名属性，写日志的 context 带请求 ID 时以它为准
- `time` 为 UTC；`attrs` 中的键与日志输出的属性键相同，没有其余属性时省略

`GET /api/logs` 按 `seq` 倒序返回一页已入库日志，还有更早的日志时带 `next_before`，下一页以 `before=<next_before>` 读取；`level` 不区分大小写，`level=error` 只返回 ERROR，`q` 匹配消息与属性文本。`DELETE /api/logs` 清空内存与账本中的日志。
