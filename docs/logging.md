# 运行日志与请求账本

服务日志由 Go `slog` 写出，同一条事件同时进入三处：

| 去向 | 内容 |
| --- | --- |
| 标准错误与 `RUNTIME_DIR/logs/antigravity2api.log` | 达到 `LOG_LEVEL` 的全部事件，格式由 `LOG_FORMAT` 决定 |
| 管理事件流 `GET /api/events` | 同一组事件 |
| 请求账本 `RUNTIME_DIR/requests.db` 的 `logs` 表 | 同一组事件中的 WARN 与 ERROR，保留 90 天 |

- 三处共用 `LOG_LEVEL` 过滤，只输出不低于该级别的事件；`LOG_LEVEL=error` 时 `logs` 表只存 ERROR
- 管理事件流的日志保存在服务内存中，达到 2200 条时裁剪为最近 2000 条；新连接先收到其中最近 200 条
- 管理端“日志”页的实时视图来自管理事件流，保留最近 2000 条；已保存视图来自 `logs` 表

`LOG_LEVEL`、`LOG_FORMAT`、`LOG_FILE`、`LOG_MAX_TOTAL_MB` 与 `REQUEST_LOG_BODIES` 在管理端设置页保存后立即生效。

| 文件 | 规则 |
| --- | --- |
| `antigravity2api.log` | `LOG_FILE=true` 时写入，达到 10 MB 时轮转 |
| `antigravity2api-<本地时间>.log` | 轮转出的旧文件，原样保存 |
| `crash.log` | 运行时崩溃报告，持续追加 |

- 日志文件由 `natefinch/lumberjack` v2 管理，轮转出的旧文件不压缩
- 旧文件名中的时间为服务器本地时间，格式为 `2006-01-02T15-04-05.000`；保留 `LOG_MAX_TOTAL_MB / 10 − 1`（整数除法）个，超出时删除最旧的
- `crash.log` 在服务启动时以追加方式打开，经 Go `debug.SetCrashOutput` 接收未恢复的 panic 与致命错误的消息及全部 goroutine 栈，内容与标准错误上的崩溃输出相同
- 每次启动在 `crash.log` 末尾写一行 `=== start <启动时刻> ===`；上次运行崩溃时，启动后写一条 ERROR 日志 `上次运行异常退出`，附带崩溃的第一行与文件路径

## 日志结构

JSON 格式每行一条事件：

```json
{"time":"2026-10-15T09:11:14.507+08:00","level":"INFO","caller":"app/app.go:223","msg":"200 | 3.113s | 127.0.0.1 | POST /v1/responses","source":"http","event":"http.access","status":200,"latency_ms":3112,"client_ip":"127.0.0.1","method":"POST","path":"/v1/responses","request_id":"req_nka6uxkxk4ssb6g4rvcbsu6mpv"}
```

| 字段 | 含义 |
| --- | --- |
| `time` | 本地时间，RFC 3339 |
| `level` | `DEBUG`、`INFO`、`WARN`、`ERROR` |
| `caller` | 源文件位置，如 `app/app.go:223` |
| `msg` | 中文消息，形如 `摘要 \| 键=值` |
| `source` | 事件来源 |
| `event` | 稳定事件名，请求与上游事件带此字段 |
| `request_id` | 请求 ID，与响应头 `X-Request-ID` 相同 |
| `request` | 请求开始与结束事件附带的请求记录 |

- `caller` 为写日志的源文件所在目录名、文件名与行号；`msg` 中的 `键=值` 属于消息文本，结构化过滤使用顶层属性与 `attrs`
- 请求 ID 为 `req_` 加 Go `crypto/rand.Text()` 的 26 个字符转小写（字母表 `a–z` 与 `2–7`）；`request` 的字段见“请求账本”
- 属性键均为英文：全部事件带 `source`；请求与上游事件带 `event`；带请求 ID 的事件带 `request_id`；`request.started` 与 `request.finished` 带 `request`；`http.access` 另带 `status`、`latency_ms`、`client_ip`、`method`、`path`
- `request_id` 由日志处理器从请求的 context 取出后加入；`上游尝试完成` 与 `上游尝试失败` 显式携带 `request_id`
- 各去向的 `msg` 均为简体中文原文；管理端“日志”页按界面语言显示译文，翻译表见 [开发文档](development.md) “语言、配色与快捷键”
- 以下事件写出时不带 `source` 属性，在管理事件流与 `logs` 表中来源记为 `service`：
  - 桌面端运行环境提示：`没有找到 WebView2 运行时，管理端在默认浏览器中打开`、`桌面环境没有系统托盘，托盘图标在系统托盘可用后出现`
  - 上传与媒体目录回收警告：`回收上传文件失败`、`媒体目录回收失败`、`刷新媒体文件使用时间失败`

`text` 格式为 slog 文本格式，字段与 JSON 格式相同。下文的日志示例为阅读摘要，只列出每条事件的 `level`、`source` 与 `msg` 三项并以空格对齐，实际输出为 JSON 或 slog 文本格式。

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
| `request.started` | 公开请求首次调用生成或打开 Live 会话 |
| `request.progress` | 分配账户、换账户、上游尝试结束、排队与请求准备等待、流式等待 |
| `request.finished` | 公开请求结束并写入账本 |
| `upstream.request` | 生成以外的一次上游请求结束，或上游生成请求发出失败 |

## 启动过程

```text
INFO  service  服务就绪 | 地址=127.0.0.1:8017
INFO  account@example.com  token 已刷新 | 到期=2026-10-15T10:08:15+08:00
INFO  account@example.com  上游请求 | POST daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist | 状态=200 | 耗时=412ms
INFO  service  官方客户端最新版本 | 版本=2.35.0 当前=2.34.0
INFO  service  账户池刷新完成 | 耗时=40.272s
```

监听建立后写 `服务就绪`，随后在后台逐个初始化账户（顺序见 [开发文档](development.md) “初始化”）：

| 时机 | 消息 | 级别 | 来源 |
| --- | --- | --- | --- |
| 配置项取值无效 | `配置项取值无效，按默认值运行 \| 配置项=... \| 原因=...` | WARN | `service` |
| 上次运行崩溃 | `上次运行异常退出 \| 原因=... \| 记录=...` | ERROR | `service` |
| 监听端口被 Windows 保留 | `监听端口被 Windows 保留，改用其后第一个可用端口 \| 配置=... \| 地址=...` | WARN | `service` |
| 监听建立 | `服务就绪 \| 地址=...` | INFO | `service` |
| 账户初始化、周期请求与 clearcut 上报 | `上游请求 \| 方法 主机路径 \| 状态=... \| 耗时=...` | INFO / WARN | 账户 |
| 单个账户初始化失败 | `账户初始化失败 \| 错误=...` | ERROR | 账户 |
| 账户不能使用套餐 | `账户不能使用套餐 \| 原因=...` | ERROR | 账户 |
| 账户池刷新结束 | `账户池刷新完成 \| 耗时=...` | INFO | `service` |
| 账户池刷新有账户失败 | `账户池刷新部分失败 \| 耗时=... \| 错误=...` | WARN | `service` |
| 读取全部账户的额度后 | `额度刷新完成 \| 失败=... \| 耗时=...` | INFO | `service` |
| 单个账户读取额度失败 | `读取额度摘要失败 \| 错误=...` | WARN | 账户 |
| 读取官方更新清单 | `官方客户端最新版本 \| 版本=... 当前=...` | INFO | `service` |
| 读取官方更新清单失败 | `读取官方客户端最新版本失败 \| 错误=...` | WARN | `service` |
| 更新上游拒绝句列表 | `上游拒绝句列表已更新 \| 版本=... \| 条数=...` | INFO | `service` |
| 检查上游拒绝句列表失败 | `检查上游拒绝句列表失败 \| 错误=...` | WARN | `service` |
| 检查本程序在线更新失败 | `检查更新失败 \| 错误=...` | WARN | `service` |
| 删除更新留下的旧版本失败 | `删除在线更新留下的旧版本失败 \| 错误=...` | WARN | `service` |
| 重新初始化失败的账户 | `异常账户重新初始化完成 \| 重试=... \| 仍失败=...` | INFO | `service` |
| 同步账户目录失败 | `读取账户目录失败 \| 错误=...` | WARN | `service` |
| 账户目录有变化 | `账户目录已变化，刷新账户池` | INFO | `service` |
| 检测到代理不可用 | `代理不可用 \| 代理=... \| 原因=...` | WARN | `service` |
| 读取额度快照失败 | `读取额度快照失败 \| 错误=...` | WARN | `service` |
| 读写续接状态失败 | `读取续接状态失败` / `保存续接状态失败`（附 `错误=...`） | WARN | `service` |
| 保存 `cooldowns.json` 失败 | `保存冷却失败 \| 错误=...` | WARN | `service` |
| 保存 `warmups.json` 失败 | `保存周额度唤醒记录失败 \| 错误=...` | WARN | `service` |
| 上传或媒体目录回收失败 | `回收上传文件失败` / `媒体目录回收失败` / `刷新媒体文件使用时间失败` | WARN | `service`（无 `source`） |
| 桌面版缺少 WebView2 运行时 | `没有找到 WebView2 运行时，管理端在默认浏览器中打开` | WARN | `service`（无 `source`） |
| Linux 桌面环境无系统托盘 | `桌面环境没有系统托盘，托盘图标在系统托盘可用后出现` | INFO | `service`（无 `source`） |

- “账户”指该账户的邮箱；登录前虚拟进程的 `上游请求` 来源为 `login`；`上游请求` 失败时级别为 WARN，附连接阶段与 `错误=...`
- 单个账户初始化失败时该账户移出调度；上传与媒体目录回收警告另附 `dir` / `file` 与 `error` 属性
- 周期节奏：官方更新清单在启动 10 秒后读取，此后每小时一次；失败账户在每次定时读取额度后重新初始化；账户目录每 2 秒同步一次；代理池每次检测时，每个不可用代理各写一条日志（密码替换为 `xxxxx`）

## 访问日志

每个 HTTP 请求结束写一条 `http.access`（`source=http`），消息为 `状态 | 耗时 | 客户端 IP | 方法 路径`，属性带 `status`、`latency_ms`、`client_ip`、`method`、`path`；客户端 IP 为直连地址。

| 条件 | 级别 |
| --- | --- |
| 5xx | ERROR |
| 4xx | WARN |
| 路径不以 `/api/` 与 `/v1` 开头的 2xx、3xx（静态资源、`/ws/`、`/upload/`、`/media/`） | DEBUG |
| 其余 | INFO |

## API 请求

通过 API key 校验的公开 POST 请求与 `/ws/` 下的 Live WebSocket 请求写开始、进展与结束三类事件，均带 `request_id`：

```text
INFO  request              请求开始 | 模型=gpt-oss-120b-medium | 协议=openai-responses | 流式=true | 档位=full | 温度=- | TopP=- | 思考=- | 思考预算=- | 最大输出=-
DEBUG account@example.com  分配账户 | 账户=account@example.com | 排队=3ms
WARN  account@example.com  上游尝试失败 | 第 1 次 | 状态=503 | 错误码=MODEL_CAPACITY_EXHAUSTED | 重试延迟= | 耗时=959ms | 错误=No capacity available for model gpt-oss-120b-medium on the server
WARN  account@example.com  模型容量不足，换账户重试 | 模型=gpt-oss-120b-medium
WARN  other@example.com    切换账户 | 账户=other@example.com | 上一账户=account@example.com
DEBUG other@example.com    上游尝试完成 | 第 2 次 | 状态=200 | traceId=5f0c2a7d9b1e4c33 | 耗时=812ms
INFO  other@example.com    工具调用完成 | 状态=200 | 耗时=3.11s | 账户=other@example.com | 模型=gpt-oss-120b-medium | 上游模型=gpt-oss-120b-medium | 输入=31327 | 缓存=10219 | 思考=96 | 回复=253
```

开始事件：

- 普通生成请求写 `请求开始`，列出公开模型、协议、是否流式、系统提示档位与采样参数，未设置的参数写 `-`
- Live WebSocket 会话写 `Live 会话开始 | 模型=... | 协议=gemini`

结束事件按请求结果 `state` 确定消息与级别：

| 结果 `state` | 消息 | 级别 |
| --- | --- | --- |
| `completed` | `请求完成` | INFO |
| `tool_calls` | `工具调用完成` | INFO |
| `limited` | `达到输出上限` | WARN |
| `blocked` | `上游终止生成` | WARN |
| `cancelled` | `请求已取消` | WARN |
| `failed` | `请求失败` | ERROR |

- 结束消息带状态码、耗时、账户、公开模型、上游模型与 token 用量；失败、取消与终止时另带 `错误=`
- 客户端断开与管理端停止记为 `499`；等待上游超过 `REQUEST_TIMEOUT` 记为 `504`，错误阶段为 `timeout`

不调用生成的公开 POST 请求（Files 上传、计数端点、Responses 与 Interactions 的 `/cancel` 等）从进入起记为进行中，出现在 `GET /api/requests` 与事件流快照中，可由管理端停止，结束时写结束事件与账本记录：

| 项 | 取值 |
| --- | --- |
| 事件输出 | 只写 `request.finished` 日志、结束时的 `request` 事件与账本记录 |
| 结束日志的 `source` | `request` |
| `model` | 请求体的 `model`，其次路径中的模型 |
| `request_type`、`response_id`、`account` | 空串 |
| `state` | 按“请求账本”的规则 |
| 失败时的 `error_phase` | 状态码小于 500 为 `request`，否则为 `internal` |

## 流式等待

请求等待生成开始、等待上游首个事件或事件流连续 15 秒没有新事件时写一条 WARN，之后每 30 秒再写一条（`已等待` 为实际等待时长）；等待结束时写结束或恢复日志：

- 等待生成开始期间：尚未选定账户时写 `排队等待`，已选定账户时写 `请求准备等待`（刷新 token、读取项目等），准备完成后写 `请求准备结束`
- 上游流期间：首事件前写 `上游首事件等待` / `上游首事件到达`，流中途停顿写 `事件流停顿` / `事件流恢复`

```text
WARN  request              排队等待 | 已等待=15s | 模型=gemini-3.8-flash-high
WARN  account@example.com  请求准备等待 | 已等待=45s | 账户=account@example.com | 模型=gemini-3.8-flash-high
INFO  account@example.com  请求准备结束 | 等待=52.310s | 模型=gemini-3.8-flash-high
WARN  account@example.com  上游首事件等待 | 模型=gemini-3.8-flash-high | 已等待=15s
DEBUG account@example.com  上游首事件到达 | 模型=gemini-3.8-flash-high | 等待=21.448s | 事件=reasoning
WARN  account@example.com  事件流停顿 | 模型=gemini-3.8-flash-high | 已等待=15s | 最近事件=text
INFO  account@example.com  事件流恢复 | 模型=gemini-3.8-flash-high | 停顿=17.208s | 当前事件=text
```

- 上游首事件与事件流的四条诊断只在客户端连接期间写出；客户端断开后服务继续读完上游事件流，账本记下完整用量
- 管理端请求页与日志页的进行中请求，以该请求最近一条 `request.progress` 的消息作为当前阶段

## 账户事件

生成失败、对冲与异常恢复时按原因处理账户（规则见 [开发文档](development.md) “失败与换号”），每种处理写一条以账户为来源的事件：

| 消息 | 级别 | 处理 |
| --- | --- | --- |
| `账户认证失败，移出调度 \| 错误=...` | WARN | token 刷新被拒或重发后仍为 401 |
| `账户需要验证，停止请求` | WARN | 需要验证 |
| `账户违反服务条款，已停用` | ERROR | 违反条款 |
| `账户受限，已停用 \| 通知=...` | ERROR | 流中收到账户受限通知 |
| `账户缺少产品许可，停止请求` | WARN | 额度摘要返回缺少产品许可的 403 |
| `账户冷却 \| 范围=... \| 时长=... \| 原因=... \| 系统提示档位=...` | WARN | 额度组或模型冷却 |
| `同额度池账户冷却 \| 范围=... \| 时长=... \| 耗尽账户=...` | WARN | 同共享额度池的其他账户各写一条 |
| `模型容量不足，换账户重试 \| 模型=...` | WARN | 容量不足 |
| `上游拒绝请求内容，不冷却账户 \| 原因=... \| 系统提示档位=...` | WARN | 无 ErrorInfo 与恢复时刻的 429 |
| `上游流在客户端收到事件前中断，换账户重新生成` | WARN | 流中错误换账户（来源为新账户） |
| `客户端尚未收到内容，重新生成 \| 第 <n> 次 \| 原因=...` | WARN | 客户端未收到内容时重新生成（来源为新账户） |
| `上游在正文之后中断或异常结束，以已转发的正文续写 \| 第 <n> 次 \| 原因=...` | WARN | 已输出正文后中断并续写（来源为续写账户） |
| `先返回首个事件的一方在给出内容前失败，改用另一方 \| 原因=...` | WARN | 对冲胜出方输出前失败，后备接管（来源为后备账户） |
| `备用请求发起 \| 原请求账户=... \| 原请求已等待=... \| 阈值=... \| 模型=...` | WARN | 首字等待达 `FIRST_EVENT_HEDGE` 阈值（来源为备用账户） |
| `备用请求胜出 \| 原请求已等待=... \| 模型=...` | INFO | 备用请求先返回首事件，原请求留作后备（来源为备用账户） |
| `原请求胜出，备用请求留作后备 \| 备用账户=... \| 模型=...` | INFO | 原请求先返回首事件，备用请求留作后备 |
| `上游在函数调用之后异常结束，按 STOP 结束 \| 错误=...` | WARN | 已发出函数调用后上游过滤或断流，按函数调用正常结束 |

账户运行中的其他事件：

| 消息 | 级别 |
| --- | --- |
| `token 已刷新 \| 到期=...` | INFO |
| `已清除冷却 \| 范围=...` | INFO |
| `关闭账户遥测失败 \| 错误=...` | WARN |
| `保存账户失败 \| 错误=...` | WARN |
| `删除已移除账户的运行数据失败 \| 错误=...` | WARN |
| `保存虚拟 Hub 状态失败 \| 错误=...` | WARN |
| `写入会话记录失败 \| 错误=...` | WARN |
| `保存账户目录失败 \| 错误=...` | WARN |
| `周额度唤醒完成 \| 额度桶=... \| 模型=... \| 耗时=...` | INFO |
| `周额度唤醒失败 \| 额度桶=... \| 模型=... \| 错误=...` | WARN |

上游网络与连接日志：

| 消息 | 级别 | 说明 |
| --- | --- | --- |
| `上游请求 \| 方法 主机路径 \| 状态=... \| 耗时=...ms` | INFO / WARN | 生成以外的上游请求（初始化、目录、额度、插件市场目录、clearcut、遥测设置）；失败时为 WARN 并附 `阶段=... \| 复用=... \| 空闲=... \| 代理=... \| 错误=...` |
| `上游生成请求失败 \| 主机路径 \| 阶段=... \| 复用=... \| 空闲=... \| 代理=... \| 错误=...` | WARN | 上游生成请求发出失败，`event=upstream.request` |
| `上游连接 \| 主机=... \| 复用=... \| 空闲=... \| 代理=...` | DEBUG | 每次建立或复用上游连接 |

管理端与命令行的账户及服务操作：

| 消息 | 级别 | 来源 |
| --- | --- | --- |
| `账户登录开始 \| 方式=browser/code \| 登录=...` | INFO | `auth` |
| `账户添加完成 \| 账户=...` | INFO | `auth` |
| `账户登录已取消 \| 登录=...` | WARN | `auth` |
| `账户添加失败 \| 登录=... \| 错误=...` | ERROR | `auth` |
| `官方客户端账户导入完成 \| 账户=...` | INFO | `auth` |
| `官方客户端账户导入失败 \| 错误=...` | ERROR | `auth` |
| `账户 JSON 导入完成 \| 账户=... \| 失败=...` | INFO | `auth` |
| `账户已导出 \| 数量=...` / `账户已导出 \| 数量=... \| 文件=...` | INFO | `auth` |
| `远程配对令牌已生成 \| 有效期=10m` | INFO | `auth` |
| `远程配对导入完成 \| 账户=...` | INFO | `auth` |
| `远程配对导入失败 \| 错误=...` | ERROR | `auth` |
| `账户已删除 \| 账户=...` | INFO | `auth` |
| `账户已启用` / `账户已停用` | INFO | 账户 |
| `账户代理已设置 \| 代理=...` | INFO | 账户 |
| `账户优先级已设置 \| 优先级=...` | INFO | 账户 |
| `账户备注名已设置 \| 备注名=...` | INFO | 账户 |
| `账户 AI 积分超额使用已设置 \| 开启=true/false` | INFO | 账户 |
| `账户验证完成 \| 耗时=...` | INFO | 账户 |
| `账户验证失败 \| 耗时=... \| 错误=...` | ERROR | 账户 |
| `读取额度摘要失败 \| 错误=...` | WARN | 账户 |
| `官方客户端已切换到该账户并重新启动` | INFO | 账户 |
| `官方客户端切换账户失败 \| 错误=...` | ERROR | 账户 |
| `手动预热完成 \| 模型=... \| 耗时=...ms` | INFO | 账户 |
| `手动预热失败 \| 模型=... \| 耗时=...ms \| 错误=...` | WARN | 账户 |
| `服务配置已保存 \| 变更=...` | INFO | `service` |
| `管理端停止请求 \| 请求=...` | WARN | `request` |
| `请求记录已导出 \| 文件=...` | INFO | `service` |
| `请求记录导出失败 \| 错误=...` | ERROR | `service` |

- 账户代理在日志中显示时密码替换为 `xxxxx`，跟随全局时为 `跟随全局`
- 命令行的 `login`、`import` 与 `switch` 只向终端输出

在线更新（`source=service`）：

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

`RUNTIME_DIR/requests.db` 是 SQLite（WAL）账本：

- **入账范围**：通过 API key 校验（或未配置 key）的公开 POST 请求、`/ws/` 下的 Live WebSocket 请求，以及每次管理端手动预热（处理链见 [开发文档](development.md) “目录、组件与依赖”）
- **写入方式**：后台协程按批提交，响应先于写入完成；等待写入的条目超过 4096 条时丢弃新条目，恢复写入后记录丢弃数

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
| `first_output_ms` | 请求进入到首个正文、媒体或函数调用交出，没有时为 0 |
| `queue_ms` | 请求进入到取得首个账户 |
| `input_tokens`、`cached_tokens` | 输入 token、其中命中缓存的部分 |
| `reasoning_tokens`、`reply_tokens` | 思考 token、回复 token |
| `tool_calls` | 工具调用数 |
| `cost_usd` | 估算美元费用，模型没有单价时为 `null` |
| `error` | 错误摘要 |
| `attempts` | 全部上游尝试 |

- `api_key` 保留前 `min(3, n/4)` 与后 `min(4, n/4)` 个字节，中间为 `…`（n 为 key 的字节数，整数除法），未配置 key 时为空串
- `first_output_ms` 中，函数调用在该次上游尝试结束时交出，非流式请求在生成结束时交出
- `prompt_tier` 记录请求开始时 `SYSTEM_PROMPT` 的设置值；图像生成、语音转写、Live 与 Tab 补全只套用各自的上游格式，该列记下当时的配置

| 请求 | `request_type` | `cascade_id` | `step_index` |
| --- | --- | --- | --- |
| Agent 模型与普通对话模型 | `agent` | 会话的 `cascade_id` | 与 `requestId` 末段相同（S） |
| 图像生成 | `image_gen` | 会话的 `cascade_id` | 与 `requestId` 末段相同（S+1） |
| 语音转写 | `audio_transcription` | 会话的 `cascade_id` | `0` |
| Live 转写会话 | `audio_transcription` | 空串 | `0` |
| Live 语音模式 | `voice` | 空串 | `0` |
| Tab 补全 | `tab` | 空串 | `0` |
| 没有选到账户 | 空串 | 空串 | `0` |

- `requestId` 末段 S 见 [协议参考](protocol.md) “labels 与会话标识”
- 发起备用请求时账本保持原请求的归属字段；当备用请求胜出，或先返回首事件的一方在输出内容前失败并由后备一方接管时，`account`、`upstream_model`、`quota_group`、`request_type`、`cascade_id` 与 `step_index` 全部更新为胜出或接管一方的值

### 上游尝试 `attempts`

`attempts` 是 JSON 数组，每次上游尝试一项，字段顺序固定，可省略的字段为空时不写：

```json
{"account":"alice@example.com","url":"https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse","status":429,"code":"QUOTA_EXHAUSTED","retry_delay":"6h0m0s","error":"You have exhausted your capacity on this model. Your quota will reset after 6h0m0s.","duration_ms":812}
```

| 字段 | 内容 |
| --- | --- |
| `account` | 尝试使用的账户 |
| `url` | 上游地址（下表）；未发出请求时为空串 |
| `status` | 上游 HTTP 状态码；连接错误为 0；流开始后中断为 200 |
| `code` | 可省略，上游错误码 |
| `retry_delay` | 可省略，上游给出的重试等待原文 |
| `trace_id`、`response_id` | 可省略，上游最后一个事件的 `traceId` 与 `responseId` |
| `error` | 可省略，错误文本 |
| `duration_ms` | 该次尝试的耗时毫秒数 |
| `hedge` | 可省略，原请求首字超时后发起的备用请求为 `true` |
| `superseded` | 可省略，另一方先返回首个上游事件后被取消的尝试为 `true` |
| `request_bytes` | 可省略，发往上游的请求正文字节数 |
| `usage` | 可省略，被丢弃的尝试中上游已给出的用量，计入记录的用量汇总 |

`url` 取值：

| 请求类型 | `url` |
| --- | --- |
| 对话生成与 Tab 补全 | `<base>/v1internal:streamGenerateContent?alt=sse` |
| 图像生成 | `<base>/v1internal:generateContent` |
| 语音转写与 Live 转写会话 | `https://aicode.googleapis.com/google.gca.aicode.v1alpha.PredictionService/BidiGenerateContent` |
| Live 语音模式 | `https://aicode.googleapis.com/google.gca.aicode.v1alpha.JetskiRemoteAgentProxyService/StreamVoice` |

`code`、`retry_delay` 与 `error` 的提取规则：

- 状态码不小于 400 时从错误体读取（错误体是 JSON 数组时取第一项）：
  - `code`：`details` 中最后一个非空的 `reason`，否则取 `error.status`
  - `retry_delay`：`details` 中最后一个非空的 RetryInfo `retryDelay` 原文（如 `3.2s`），否则取最后一个非空的 ErrorInfo `metadata.quotaResetDelay` 原文（如 `6h0m0s`）
  - `error`：`error.message`
- 连接错误与流中断时 `error` 为错误文本（连接错误定义见 [开发文档](development.md) “失败与换号”）

### 结果状态与协议归类

请求结束时按以下顺序判定 `state`，取第一条成立的规则：

1. 等待上游超过 `REQUEST_TIMEOUT` 且上游未发出终止事件：`failed`，`status` 为 `504`，`error_phase` 为 `timeout`
2. 有取消原因且上游未发出终止事件，或响应状态码为 `499`，或生成错误是 context 取消：`cancelled`，`status` 为 `499`
3. 响应状态码不小于 400，或有生成错误：`failed`；响应状态码小于 400（响应头发出后流中出错）时，`status` 依次取错误自带的 HTTP 状态码、上游状态码、`502`
4. 终止原因为 `MAX_TOKENS`（不区分大小写）：`limited`
5. 终止原因非空且不是 `STOP`：`blocked`
6. 工具调用数大于 0：`tool_calls`
7. 其余：`completed`

- **响应状态码**：处理链写出的第一个状态码，未写出时为 `200`
- **取消原因**：请求 context 结束的原因（客户端断开、管理端停止、`REQUEST_TIMEOUT`），或 Responses 与 Interactions 后台请求经 cancel 接口终止；后台请求保持运行直到完成或被 cancel
- **生成错误**：同一请求最近一次生成未能开始的错误，或事件流中的错误事件
- **终止事件**：上游的 finish 事件（终止原因为 `finishReason`）；该事件到达后结果确定

`error` 的取值：

- `completed`、`tool_calls`、`limited`：空串
- 超时：`等待上游超过 REQUEST_TIMEOUT`
- `cancelled`：客户端断开为 `客户端断开连接`，管理端停止为 `管理端停止了请求`，后台响应被取消为 `客户端取消了后台响应`
- `blocked`：终止原因原文
- 其余 `failed`：依次取生成错误的文本、响应体的 `error.message`、状态码的标准说明（如 `Bad Gateway`）

第 3 条规则判定的 `failed` 按下表顺序取 `error_phase`；除超时外，其他结果的 `error_phase` 为空串：

| 条件 | `error_phase` |
| --- | --- |
| 未调用生成，状态码小于 500 | `request` |
| 未调用生成 | `internal` |
| 没有上游尝试 | `routing` |
| 最后一次尝试为连接错误 | `network` |
| 其余 | `upstream` |

`upstream_status` 与 `error_code` 取最后一次上游尝试的 `status` 与 `code`，没有尝试时为 `0` 与空串。

| 路径 | `protocol` |
| --- | --- |
| `/v1/chat/completions` | `openai-chat` |
| `/v1/responses` 及其子路径（含 `/v1/responses/input_tokens`、`/v1/responses/compact`） | `openai-responses` |
| `/v1/messages` 及其子路径（含 `/v1/messages/count_tokens`） | `anthropic` |
| `/v1beta/`、`/ws/`、`/v1/models/`、`/v1/cachedContents`、`/v1/interactions` 及其子路径 | `gemini` |
| 其余（`/v1/completions`、`/v1/images/*`、`/v1/audio/*`、`/v1/files`、`/upload/v1beta/files`） | `other` |
| 管理端手动预热 `/api/warmup` | `warmup` |

### 正文、汇总与费用

正文保存规则：

| 类别 | 保存条件 | 保留条数 |
| --- | --- | --- |
| 公开协议请求与响应正文 | `REQUEST_LOG_BODIES=true` | 最近 1000 条 |
| 成功请求的上游尝试正文 | `REQUEST_LOG_BODIES=true` | 最近 1000 条（与失败尝试合计） |
| 失败请求的上游尝试正文 | 始终保存 | 最近 1000 条（与成功尝试合计） |

- 正文为明文，各自截断到 64 KiB 并记录原始字节数，以 gzip 压缩后写入账本

汇总与保留：

- **累加**：写入请求记录的同一事务按 UTC 小时（`hours` 表）与服务器本地日（`days` 表）累加汇总，包含请求数、各类 token、费用与耗时分布
- **读取**：用量报告何时读取汇总、何时读取原始记录见“用量报告”的分桶规则
- **时区变化**：每次打开账本时检查 `days` 表中各个不同的 `start`，任一值不是当前服务器时区的本地零点时，删除全部本地日汇总，从最早的小时汇总起到当前时间按天重建，并写 `请求账本已按服务器时区重建本地日汇总`
- **保留期**：请求记录、汇总、正文、额度快照与日志保留 90 天；服务启动时与之后每 24 小时清理一次；截止时刻为当前时间减 90 天所在本地日的零点，`days` 按该时刻删除，其余表按其向下取整到 UTC 整点后的时刻删除
- **文件收缩**：账本启用增量收缩（`auto_vacuum=INCREMENTAL`），每次过期清理与清空日志后把全部空闲页交还文件系统并将 WAL 文件截短为 0；检查点后 WAL 文件不超过 512 KiB

费用按上游模型 ID（去掉思考档位后缀）在请求完成时刻生效的官方 API 单价逐条计算：

- **去后缀**：`upstream_model` 依次尝试去掉末尾的 `-extra-low`、`-thinking`、`-medium`、`-tiered`、`-xhigh`、`-high`、`-low`，只去掉第一个匹配的后缀，结果与下表模型名完全相同时计费（例如 `claude-opus-4-6-thinking` 按 `claude-opus-4-6`、`gemini-4-hydrogen-xhigh` 按 `gemini-4-hydrogen`）
- **计算公式**：`((input_tokens − cached_tokens) × 输入单价 + cached_tokens × 缓存单价 + reasoning_tokens × 输出单价 + reply_tokens × 回复单价) / 1,000,000`
  - `cached_tokens` 是 `input_tokens` 中命中缓存的部分；token 取同一请求各次生成的累计值
  - 回复单价默认为输出单价；模型有图片输出单价且回答含模型输出的图片时取图片输出单价
- **长上下文**：`input_tokens`（含缓存部分）大于 200,000 时，输入、缓存与输出三项单价一起切换到长上下文档（仅列出长上下文单价的模型分档）
- **无单价模型**：消耗了 token 时 `cost_usd` 为 `null` 并计入汇总的 `unpriced`，未消耗 token 时为 `0`

单价（美元每百万 token）：

| 上游模型 | 输入 | 缓存命中输入 | 输出 | 说明 |
| --- | --- | --- | --- | --- |
| `claude-opus-5-5` | 4 | 0.20 | 20 | — |
| `claude-sonnet-5-5` | 2 | 0.10 | 10 | — |
| `claude-opus-4-6` | 5 | 0.50 | 25 | — |
| `claude-sonnet-4-6` | 3 | 0.30 | 15 | — |
| `gpt-oss-120b` | 0.09 | 0.09 | 0.36 | — |
| `gemini-4-carbon`、`gemini-pro-orchestrator` | 2 / 4 | 0.10 / 0.20 | 10 / 20 | 2026-12-01 PST 起切换后档 |
| `gemini-4-argon`、`gemini-pro-orchestrator-argon` | 2 / 4 | 0.10 / 0.20 | 10 / 20 | 2026-12-01 PST 起切换后档 |
| `gemini-4-hydrogen`、`gemini-flash-worker-agy` | 1.50 | 0.075 | 7.50 | — |
| `gemini-3.8-flash` | 0.75 / 1.50 | 0.075 / 0.15 | 3.75 / 7.50 | 2027-01-01 PST 起切换后档 |
| `gemini-3.5-flash`、`gemini-3-flash-agent` | 1.50 | 0.15 | 9 | — |
| `gemini-3.5-flash-lite` | 0.30 | 0.03 | 2.50 | — |
| `gemini-3.1-flash-lite` | 0.25 | 0.025 | 1.50 | — |
| `gemini-3.1-flash-image` | 0.50 | 0.50 | 3 | 输出图片时回复 token 60 |
| `gemini-3-flash` | 0.50 | 0.05 | 3 | — |
| `gemini-2.5-pro` | 1.25 | 0.125 | 10 | 见长上下文表 |
| `gemini-2.5-flash` | 0.30 | 0.03 | 2.50 | — |
| `gemini-2.5-flash-lite`、`tab_flash_lite_preview` | 0.10 | 0.01 | 0.40 | — |
| `proactive-observer-v10` | 2 | 2 | 12 | — |

长上下文单价（美元每百万 token，输入大于 200,000 token）：

| 上游模型 | 输入 | 缓存命中输入 | 输出 |
| --- | --- | --- | --- |
| `gemini-2.5-pro` | 2.50 | 0.25 | 15 |

账本维护日志：

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
 tool_calls INTEGER NOT NULL, cost_usd REAL, error TEXT NOT NULL, attempts TEXT NOT NULL,
 first_output_ms INTEGER NOT NULL DEFAULT 0);
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
CREATE TABLE IF NOT EXISTS request_bodies (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, time INTEGER NOT NULL,
 request BLOB NOT NULL, request_size INTEGER NOT NULL, response BLOB NOT NULL, response_size INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS upstream_bodies (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL, attempt INTEGER NOT NULL, time INTEGER NOT NULL,
 request BLOB NOT NULL, request_size INTEGER NOT NULL, response BLOB NOT NULL, response_size INTEGER NOT NULL,
 UNIQUE (id, attempt));
CREATE TABLE IF NOT EXISTS quota_snapshots (
 time INTEGER NOT NULL, account TEXT NOT NULL, bucket_id TEXT NOT NULL, window TEXT NOT NULL,
 remaining_fraction REAL NOT NULL, reset_time INTEGER);
CREATE INDEX IF NOT EXISTS quota_snapshots_account_time ON quota_snapshots(account, time);
CREATE INDEX IF NOT EXISTS quota_snapshots_time ON quota_snapshots(time);
CREATE TABLE IF NOT EXISTS logs (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER NOT NULL, level TEXT NOT NULL, source TEXT NOT NULL,
 event TEXT NOT NULL, message TEXT NOT NULL, request_id TEXT NOT NULL, model TEXT NOT NULL, data TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS logs_time ON logs(time);
CREATE INDEX IF NOT EXISTS logs_request_id ON logs(request_id);
```

连接与初始化（`internal/sqlitedb` 的 `Open`，`states.db` 共用同一流程）：

- **驱动与参数**：经 `modernc.org/sqlite` 打开，连接参数为 `busy_timeout(10000)`、`journal_mode(WAL)`、`journal_size_limit(524288)`、`synchronous(NORMAL)`，最多 4 个连接
- **批量提交**：写入协程把连续到达的条目合并为一个事务，每个事务最多 256 条
- **打开顺序**：按库当前状态依次执行：非增量收缩时设为增量收缩并执行一次 `VACUUM`；为已有表补上建表语句中有而缺少的列；执行建表语句；清理建表语句以外的表、索引、视图与触发器；交还空闲页并截短 WAL 文件

| 表 | 一行内容 | 键与索引 |
| --- | --- | --- |
| `requests` | 一条请求记录 | 主键 `id`；索引 `(time)`、`(model, time)`、`(account, time)`、`(state, time)` |
| `hours` | 一个 UTC 小时内同键请求的汇总 | 主键 `start` 加 7 个维度列，`WITHOUT ROWID` |
| `days` | 一个服务器本地日内同键请求的汇总 | 同 `hours` |
| `request_bodies` | 一个请求的公开协议正文 | 自增主键 `seq`；`id` 唯一 |
| `upstream_bodies` | 一次上游尝试的正文 | 自增主键 `seq`；`(id, attempt)` 唯一 |
| `quota_snapshots` | 一个额度桶某一时刻的剩余比例 | 索引 `(account, time)`、`(time)` |
| `logs` | 一条 WARN 以上日志 | 自增主键 `seq`；索引 `(time)`、`(request_id)` |

- **汇总列**：7 个维度列为 `model`（公开模型名）、`account`、`api_key`、`protocol`、`state`、`quota_group`、`status`；累计列为 `requests`、四类 token、`duration_ms`、`first_event_ms` 与 `queue_ms` 总和、`first_events`（有首事件的请求数）、`cost_usd`（有单价请求的费用之和）、`unpriced`、耗时分布 `duration_hist` 与 `first_event_hist`、`last_time`（最晚完成时间）
- **时间编码**：`time`、`start`、`last_time`、`reset_time` 存 Unix 毫秒整数；`hours.start` 为 UTC 整点，`days.start` 为服务器本地日零点；接口读出时转为 UTC RFC 3339 文本
- **标量与 JSON 列**：`requests.stream` 为 0 或 1；`requests.cost_usd` 与 `quota_snapshots.reset_time` 无值时为 `NULL`；`requests.attempts` 为不含正文的 JSON 数组（无尝试时为 `[]`）；`logs.data` 为 `{"request": 请求记录, "attrs": 其余属性}`（缺失键省略）
- **耗时分布列**：按分桶序号升序编码，每个非空分桶依次写两个无符号 varint（与前一个非空分桶的序号差、请求数）；分桶规则见“用量报告”
- **正文列**：`request` 与 `response` 为按字节截断到 64 KiB 后 gzip 压缩的 BLOB，`request_size` 与 `response_size` 为原始字节数；`request_bodies.time` 与 `upstream_bodies.time` 为所属请求的完成时间；`upstream_bodies.attempt` 从 0 开始对应 `attempts` 下标，上游正文按尝试条数保留最近 1000 条

### 记录查询与导出

`GET /api/usage/records` 返回完成时间在 `[from, to)` 内的记录，按完成时间倒序，完成时间相同时按 `id` 倒序：

- 维度筛选与 `status` 均按完整取值匹配，同时生效
- `q` 去掉首尾空白后按子串匹配 `id`、`client_request_id`、`response_id`、`cascade_id` 与 `error`，`%`、`_` 与 `\` 按字面匹配，ASCII 字母不区分大小写
- `next_cursor` 是 `<完成时间的 Unix 毫秒>|<id>` 的无填充 base64url 编码，指向本页最后一条；以它为 `cursor` 读取完成时间更早、或完成时间相同且 `id` 更小的记录

`GET /api/usage/records/{id}` 返回一条记录及已保存正文：

| 字段 | 内容 |
| --- | --- |
| `record` | 请求记录 |
| `body` | 公开协议正文，未保存或超出保留条数时为 `null` |
| `upstream` | 上游尝试正文数组，按尝试顺序，无保存时为空数组 |

正文对象的字段为 `request`、`request_size`、`response`、`response_size`；公开协议的 `response` 是写给客户端的响应体（流式请求为 SSE 文本）。

`GET /api/quota/snapshots` 返回范围内的额度快照，按 `time` 升序，同一时刻按写入顺序。账户每次读取额度摘要时，每个额度桶写一条快照，字段为 `time`、`account`、`bucket_id`、`window`、`remaining_fraction` 与 `reset_time`（无值时省略）。

`GET /api/usage/records.csv` 使用与记录页相同的范围、筛选与排序，导出全部符合条件的记录（忽略 `limit` 与 `cursor`）：

- `GET` 返回 `Content-Type: text/csv; charset=utf-8` 正文；`POST` 写入请求体 `dir` 指定的文件夹，文件名见 [开发文档](development.md) “用量与记录查询”
- 开头为 UTF-8 BOM，首行为列名，列与顺序同 `requests` 表（`id` 到 `first_output_ms` 共 36 列）
- 由 Go `encoding/csv` 写出：逗号分隔，含逗号、双引号、换行或以空白开头的值加双引号，内部双引号写两次，行尾为 LF
- 值格式：`time` 为精确到毫秒的 UTC RFC 3339 文本；`stream` 为 `true` 或 `false`；`cost_usd` 为 `null` 时留空，否则为最短十进制表示（无指数）；`attempts` 为 JSON 数组
- 公式防注入：以 `=`、`+`、`-`、`@`、制表符或回车开头的文本前加单引号 `'`，适用于 `client_request_id`、`response_id`、`path`、`api_key`、`user_agent`、`model`、`upstream_model`、`model_version`、`request_type`、`cascade_id`、`account`、`error_code`、`error`

### 用量报告

`GET /api/usage`（参数见 [开发文档](development.md) “用量与记录查询”）返回：

| 字段 | 内容 |
| --- | --- |
| `from`、`to` | 查询范围，UTC |
| `bucket_seconds` | 分桶时长（秒） |
| `generated_at` | 生成报告的时间 |
| `latest_at` | 账本中最晚一条记录的完成时间，无记录时省略 |
| `totals` | 范围内的汇总 |
| `previous` | 等长的上一周期汇总 |
| `recent` | 最近 5 分钟的吞吐：`{"minutes":5,"requests","tokens"}` |
| `buckets` | 逐桶汇总数组，每项为 `at`（分桶起点）加汇总字段 |
| `stack_by` | 堆叠维度 |
| `series` | 堆叠系列数组 |
| `groups` | 按维度的分项 |
| `pairs` | 按账户与模型的分项 |
| `options` | 各筛选维度的可选值 |

- `series` 每项为 `{"key","other","requests":[…],"tokens":[…]}`，`other` 只在合并项出现
- `groups` 的键为 `model`、`account`、`api_key`、`protocol`、`state`、`quota_group`、`status`，值为数组，每项为 `key` 加汇总字段；无记录时为 `[]`
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

每个汇总对象的字段：

| 字段 | 内容 |
| --- | --- |
| `requests` | 请求数 |
| `succeeded` | `completed`、`tool_calls`、`limited` 与 `blocked` |
| `failed`、`canceled` | 结果为 `failed`、`cancelled` 的请求数（汇总字段名写作 `canceled`） |
| `rate_limited` | 状态码 `429` |
| `input_tokens`、`cached_tokens`、`reasoning_tokens`、`reply_tokens`、`total_tokens` | token 用量 |
| `cost_usd`、`unpriced` | 有单价请求的费用之和、消耗 token 但无单价的请求数 |
| `duration`、`first_event` | 成功请求的耗时与首事件耗时：`avg_ms`、`p50_ms`、`p95_ms`、`p99_ms` |
| `queue_avg_ms` | 全部请求的平均排队时间 |
| `last_at` | 最后一个请求的时间，无请求时省略 |

耗时与分位数计算（对数分桶，相对误差约 0.5%）：

- 耗时按毫秒记入 1840 个分桶：不大于 0 的值记入 0 号；正值 v 记入 `⌊ln v / ln 1.01⌋ + 1` 号，第 b 号覆盖 `[1.01^(b−1), 1.01^b)` 毫秒；`1.01^1838 ≈ 87,636,498` 毫秒（约 24.3 小时）及以上记入 1839 号
- 分位数按最近秩法：N 个样本的 p 分位取升序第 `max(⌈p × N⌉, 1)` 个样本所在分桶的几何中点 `1.01^(b−0.5)` 毫秒，0 号分桶返回 0；1839 号分桶内的值超出 0.5% 误差界
- `avg_ms` 为毫秒总和除以样本数，直接计算不经分桶
- `duration` 样本为成功请求，`first_event` 样本为有首事件的成功请求，无样本时各项为 0
- `total_tokens` 为输入、思考与回复 token 之和（缓存命中部分已计入输入）

范围与对比时段：

- 查询范围为 `[from, to)`，计入 `totals`、`buckets`、`series`、`groups` 与 `pairs`
- `previous` 为等长的前一周期 `[from − (to − from), from)`，沿用查询筛选
- `recent` 为生成时间前 5 分钟到生成时间（含），沿用查询筛选，独立于 `from`、`to`
- `latest_at` 取全账本最晚记录，不受范围与筛选影响
- `options` 列出 6 个筛选维度在 `[from, to)` 内出现过的取值（字节序升序，空值写作空串），不受筛选与对比时段影响

分桶与表选择：

- `bucket` 为 1 天时，按 `tz` 的自然日对齐：首桶从 `from` 所在当地日零点开始，此后每桶从下一个当地零点开始（夏令时切换日为 23 或 25 小时；零点因夏令时跳过时按 Go `time.Date` 归一化时刻）
- 其余 `bucket` 按 `from` 在 `tz` 中的 UTC 偏移对齐到当地时间整分桶，此后等长，范围内偏移变化不改变对齐
- 首桶起点可早于 `from`，末桶可延续到 `to` 之后，统计时只计 `[from, to)` 内的记录；`at` 为分桶起点的 UTC 时间
- 读取来源按“日汇总 → 小时汇总 → 原始记录”的顺序选取，只在完整落在查询范围与单个分桶内时使用汇总表（`previous` 不分桶，只要求完整落在范围内）：
  - 完整服务器本地日读取 `days`；其余完整 UTC 整小时读取 `hours`；剩余片段读取 `requests`
  - 分桶小于 1 小时时全范围读取 `requests`；按小时分桶且 `tz` 偏移非整小时时读取 `requests`
  - 按天分桶时，`tz` 自然日与服务器本地日不重合的日子读取 `hours`，跨越当地零点的 UTC 小时读取 `requests`

排名规则：

- `groups` 每个维度按请求数降序，并列时按取值的字节序升序；`status` 取值为十进制文本
- `pairs` 按请求数降序，并列时依次按 `account`、`model` 的字节序升序
- `series` 取 `groups[stack_by]` 前 8 名各成一个系列；超过 8 个取值时，第 9 名起合并为最后一个系列（`other: true`、`key: ""`）
- 各系列的 `requests` 与 `tokens` 数组与 `buckets` 等长，`tokens` 为输入、思考与回复 token 之和

## 管理事件流

`GET /api/events` 是 SSE 流，每个 `data` 行为 `{"type":"<类型>","data":<对象>}`：

| 类型 | 内容 |
| --- | --- |
| `accounts` | 账户数组（每项同 `GET /api/accounts`），内容变化时推送 |
| `log` | 一条日志（字段同 `GET /api/logs`） |
| `request` | 进行中记录（开始、分配或换账户时）与结束时的完整记录 |
| `login` | 管理端登录的状态变化 |
| `ready` | 初始快照发送结束（`data` 为 `null`） |

- `accounts` 的 `data` 为数组本身，例如 `{"type":"accounts","data":[{"id":"alice@example.com",...}]}`
- 进行中记录的 `state` 为 `running`，`time` 为开始时间，`duration_ms` 为截至推送时的已用时间；开始时 `account` 为空串，随后分配账户与换账户时各再推送一次
- 新连接快照与 `GET /api/requests` 均取请求当前状态，`account` 为最近一次分配的账户

连接建立后按顺序收到初始快照，随后接收增量事件：

1. 当前账户列表（`accounts`）
2. 服务内存中最近 200 条日志（`log`）
3. 按开始时间升序的进行中请求（`request`）
4. `ready`

连接期间每 30 秒发送一次 `: ping` 注释帧；单个连接在初始事件之外堆积超过 1024 条未发送事件时由服务端断开，管理端自动重连。

日志条目字段为 `seq`（入库序号，实时事件省略）、`time`（UTC）、`level`、`source`、`event`、`message`、`request_id`、`model`、`request`（请求开始与结束事件）与 `attrs`（其余属性，无值时省略）：

- `source` 与 `event` 取同名属性，缺 `source` 时填 `service`
- `request` 属性为请求记录时，去掉上游正文后填入 `request`，同时将 `request_id` 与 `model` 设为记录的 `id` 与公开模型名；其余事件的 `model` 为空串
- `request_id` 优先取写日志 context 中的请求 ID，其次取同名属性
- `attrs` 的键与日志输出属性键相同

已入库日志接口：

- `GET /api/logs` 按 `seq` 倒序返回一页已入库日志；存在更早日志时返回 `next_before`，下一页传 `before=<next_before>`
- 筛选参数：`level` 不区分大小写，`level=error` 只返回 ERROR；`q` 匹配消息与属性文本
- `DELETE /api/logs` 清空内存与账本中的全部日志
