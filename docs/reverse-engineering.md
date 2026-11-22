# Antigravity 官方客户端逆向指南

Antigravity2API 的上游请求以官方客户端现场为准。本文说明官方客户端的组件与请求归属、抓包与静态分析方法，以及官方发布新版本后的对齐步骤。协议字段、请求顺序与信封见 [协议参考](protocol.md)。

## 1. 组件与请求归属

官方客户端（Antigravity Hub）由 Electron 外壳与 Go 编写的 language server（LS）组成。界面是 LS 内嵌的网页包，由 LS 在 `https://127.0.0.1:<端口>/` 提供，Electron 窗口直接加载它。

| 组件 | 文件 | 发出的请求 | 产品对应实现 |
| --- | --- | --- | --- |
| Electron 外壳 | `Antigravity.exe`、`resources/app.asar` | 更新清单 `latest-x64-win.yml` | `internal/app` 版本检查 |
| language server | `resources/bin/language_server[.exe]` | OAuth、cloudcode `v1internal`、userinfo、头像、插件市场目录、clearcut、`aicode` 的 `fetchConfig`、`fetchLicenses` 与 gRPC 语音转写、语音模式 | `internal/antigravity` |
| 界面 | LS 内嵌网页包 | 仅访问本机 LS | 管理端（`web/`） |

LS 的出站请求均由 Go 标准库发出，按目标分三种传输层：

| 请求类别 | 传输与 ALPN | 说明 |
| --- | --- | --- |
| cloudcode、OAuth、userinfo、头像、`fetchConfig`、`fetchLicenses` | HTTP/1.1（ALPN 仅 `http/1.1`） | userinfo 与头像使用 Go 默认 `Go-http-client/1.1` |
| clearcut、插件市场目录 | HTTP/2（ALPN 为 `h2`、`http/1.1`） | 标准 `http.DefaultTransport` 行为 |
| 语音转写、语音模式 | gRPC（`grpc-go 1.86.0-dev`） | 双向流 |

产品以同一套 Go 标准库发出上述请求，TLS ClientHello 与 HTTP 形态由 Go 版本决定；上游 TCP keep-alive 空闲探测间隔与官方 2.34.0 LS 一致（30 秒）。官方 LS 升级 Go 工具链后需按第 4 节重新对照。

## 2. 抓包

### 环境

| 项 | 做法 |
| --- | --- |
| 代理 | mitmproxy regular 模式双端口，按端口区分 LS 与 Electron |
| 证书 | mitmproxy 根证书装入系统根证书库 |
| 发送进程 | 按连接本地端口反查进程，区分 LS 与 Electron |
| TLS | mitmproxy `tls_clienthello` 钩子加回环网卡抓包 |
| 正文 | SSE 与 gRPC 双向流逐块落盘，保留时间戳与 trailer |
| 界面操作 | 经 CDP 驱动界面，经 inspector 截获登录链接 |
| 隔离用户目录 | 环境变量与 `--user-data-dir` 指向独立目录 |

具体配置：

- **代理与证书**：LS 经 `HTTPS_PROXY`、`HTTP_PROXY` 走一个端口，Electron 经 `--proxy-server` 走另一个端口，同时设 `NO_PROXY=localhost,127.0.0.1` 保留本机连接；Windows 将 mitmproxy 根证书装入 `LocalMachine\Root`（LS 读取系统根证书），抓包结束后移除
- **TLS 与流正文**：在回环网卡抓包（Windows 使用 Npcap 的 `dumpcap`）保留原始 TLS 字节供逐字节对照；SSE 响应与 gRPC 请求流、响应流逐块保存，记录分块时刻与 gRPC trailer
- **界面与登录链接**：Electron 启动参数加 `--remote-debugging-port=<端口>` 与 `--inspect=127.0.0.1:<端口>`；经 inspector 替换主进程 `shell.openExternal` 获取登录链接
- **隔离用户目录**：将 `USERPROFILE`、`HOME`、`APPDATA`、`LOCALAPPDATA` 指向独立目录，Electron 加 `--user-data-dir`；在 `<目录>\.gemini\cache\antigravity-keyring-unavailable` 写入标记后，LS 将 token 存到 `<目录>\.gemini\jetski-standalone-oauth-token`。系统凭据库已有 `gemini:antigravity` 时 LS 仍会读取它，隔离账户应在不含该凭据的系统用户或虚拟机中运行

每个 HTTP 交换记录一行 JSON：

```json
{"sequence": 45, "sender": "language_server", "client": ["127.0.0.1", 49793], "startedAt": 1794435647.6818762, "endedAt": 1794435649.6290452, "method": "POST", "url": "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", "httpVersion": "HTTP/1.1", "tls": {"sni": "daily-cloudcode-pa.googleapis.com", "alpn": "http/1.1", "version": "TLSv1.3", "cipher": "TLS_AES_256_GCM_SHA384"}, "requestHeaders": [["content-type", "application/json"], ["user-agent", "antigravity/hub/2.34.0 (aidev_client; os_type=windows; arch=amd64; cl=1026637980)"], ["authorization", "Bearer <token>"], ["accept-encoding", "gzip"]], "requestBody": "00045.request.bin", "status": 200, "responseHeaders": [["content-type", "text/event-stream"], ["content-disposition", "attachment"], ["vary", "Origin"], ["vary", "X-Origin"], ["vary", "Referer"], ["date", "Wed, 11 Nov 2026 22:20:47 GMT"], ["server", "ESF"], ["x-xss-protection", "0"], ["x-frame-options", "SAMEORIGIN"], ["x-content-type-options", "nosniff"], ["server-timing", "gfet4t7; dur=1822"], ["alt-svc", "h3=\":443\"; ma=2592000,h3-29=\":443\"; ma=2592000"]], "responseBody": "00045.response.sse"}
```

请求头按发送顺序保留（含重复项），正文原样保存原始字节，便于对照头集合与顺序、正文字段顺序、分块方式与连接复用。

### 场景

| 场景 | 操作 | 得到的协议 |
| --- | --- | --- |
| 登录 | 退出登录后点击登录并完成 Google 授权 | 授权链接参数、token 交换、登录后初始化序列 |
| 冷启动 | 已登录状态下启动客户端 | 冷启动初始化序列、`tokeninfo` |
| 空闲 | 启动后无操作 70 分钟以上 | 周期请求与节奏、token 刷新时机 |
| 各模型最小请求 | 每个 Agent 模型发一条短消息 | 信封、`labels`、`generationConfig`、三种模型族 SSE |
| 工具往返 | 让模型调用工具后继续追问 | `functionCall`、`functionResponse`、思考签名回放 |
| 多轮与长推理 | 同一会话连续对话，High 档长推理 | 历史回放、缓存命中、签名 |
| 媒体 | 附图片发送；生成与编辑图片；网页搜索；语音输入 | `inlineData`、`image_gen`、`web_search`、gRPC 语音转写 |
| 停止 | 生成中点击停止 | 取消方式与下一轮历史 |
| 设置 | 修改设置项前后各抓一次空闲与会话 | 设置变更请求，开关对伴随请求的影响 |
| 错误 | 下线模型、额度不足、需要验证的账户 | 错误响应形态 |

每个场景至少重复抓取两次，用以区分稳定字段与每次随机字段（会话标识、`requestId`、执行标识、时间）。

## 3. 静态分析

### Electron 外壳

用 `npx @electron/asar extract app.asar <目录>` 解包 `resources/app.asar`：

| 文件 | 内容 |
| --- | --- |
| `package.json` | 客户端版本（UA 中的版本号） |
| `dist/languageServer.js` | LS 启动参数 |
| `dist/main.js` 与同目录模块 | 窗口、托盘、更新源与检查节奏、深链协议 |

`dist/languageServer.js` 中的关键 LS 启动参数：`--override_ide_name`、`--subclient_type`、`--override_ide_version`、`--override_user_agent_name`、`--cloud_code_endpoint`、`--api_server_url`、`--app_data_dir`。

### language server

| 信息 | 方法 |
| --- | --- |
| 构建号 | 执行 `language_server --stamp`，取 `Built at CL: <数字>` 即 UA 的 `cl=` |
| Go 工具链 | `go version -m <二进制>` 读取工具链版本与 `GOEXPERIMENT` |
| 函数名 | 解析 pclntab 得到全部函数地址与名称，导入 IDA 反汇编 |
| proto | 从 `.rodata` 提取内嵌 `FileDescriptorProto`，得到 `v1internal` 全部方法、消息与枚举 |
| User-Agent | 反汇编 `codeassistclient.UserAgent` |
| 请求头 | 反汇编 `(*CodeAssistClient).setHeaders` |
| OAuth | 字符串中提取 client ID、client secret、scope 与回调地址 |
| 端点选择 | 反汇编 `loadCodeAssist` 之后的端点重算 |
| 内嵌界面 | `.rdata` 中的 zip，按中央目录结束记录定位后解压 |

- `codeassistclient.UserAgent` 格式为 `<name>/<subclient>/<version> (aidev_client; os_type=<GOOS>; arch=<GOARCH>; cl=<CL>)`
- 字符串与反汇编用于定位候选值，实际取值以抓包现场为准

## 4. 官方新版本对齐

官方客户端的更新清单请求：

```text
GET https://antigravity-hub-auto-updater-974169037036.us-central1.run.app/manifest/latest-x64-win.yml?noCache=<随机>
x-app-version: <当前版本>
user-agent: electron-builder
```

服务以官方 Electron 44（Chromium 152）网络栈形态读取清单（`internal/app/updater.go` 为 HTTP/2 与代理层，`updater_quic.go` 与 `updater_h3.go` 为 QUIC 与 HTTP/3，`internal/app/version.go` 为请求与节奏）。

### 请求参数与响应解析

| 项 | 取值 |
| --- | --- |
| `noCache` | 当前毫秒时间戳的 32 进制 |
| `x-user-staging-id` | `RUNTIME_DIR/.updaterId` 的内容 |
| `accept-language` | 系统语言解析出的语言包名（见下） |
| 伪头顺序 | `:method`、`:authority`、`:scheme`、`:path` |
| 单次时限 | 1 分钟 |
| 成功判定 | 状态码 `200`；正文首行 `version:` 后的值为最新版本 |

- `.updaterId` 缺失时以 4096 个随机字节为名称、OID 命名空间生成 UUID v5 并写入
- 请求头顺序：`x-user-staging-id`、`x-app-version`、`user-agent: electron-builder`、`cache-control: no-cache`、`sec-fetch-site: none`、`sec-fetch-mode: no-cors`、`sec-fetch-dest: empty`、`accept-encoding: gzip, deflate, br, zstd`、`accept-language`、`priority: u=4, i`

### HTTP/2 与 HTTP/3 传输层

| 项 | 取值 |
| --- | --- |
| TLS ClientHello | `tls-client` v1.16.0 `Chrome_152_PSK` 去掉扩展 `0xca34` |
| HTTP/2 长连接 | 单连接保持至对端关闭或 GOAWAY，TCP keep-alive 45 秒 |
| HTTP/2 HEADERS 优先级 | 独占、依赖 0、权重字节 109（RFC 9113 权重 110） |
| 代理 | 按 `PROXY` 与系统代理选择；经代理时只走 TCP |
| HTTP/3 替代服务 | 响应 `alt-svc` 中的 `h3=":443"`，有效期取 `ma`（缺省 24 小时） |
| QUIC 空闲关闭 | 30 秒无活动后静默关闭 |

TCP 与 HTTP/2 细节：

- TLS 层为 `github.com/bogdanfinn/utls` v1.7.8-barnius；有缓存会话时 ClientHello 末尾带 `pre_shared_key`，TLS 会话缓存跨连接保留
- HTTP/2 前言之后在同一 TLS 记录中发出 SETTINGS（`HEADER_TABLE_SIZE=65536`、`ENABLE_PUSH=0`、`INITIAL_WINDOW_SIZE=6291456`、`MAX_HEADER_LIST_SIZE=262144`）与连接级 WINDOW_UPDATE `15663105`
- HEADERS 写入 Go `http2.PriorityParam{Exclusive: true, Weight: 109}`；`:path` 使用不索引字面量，各头值在 Huffman 编码更短时使用 Huffman
- 经 HTTP 代理时 CONNECT 带 `Host`、`Proxy-Connection: keep-alive` 与 User-Agent `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Antigravity/<ClientVersion> Chrome/<ChromeVersion> Electron/<ElectronVersion> Safari/537.36`

QUIC、HTTP/3 与竞速细节：

- **竞速**：不经代理且替代服务未过期时，先发出 QUIC Initial，再复用现有 HTTP/2 连接或发起 TCP，先就绪者承载请求；QUIC 在收到 `HANDSHAKE_DONE` 后就绪
- **落后连接**：QUIC 照常完成握手并发出控制流；TCP 照常完成 TLS 握手，不发 HTTP/2 前言，闲置至下一次读取时关闭
- **QUIC Initial**：取 `github.com/refraction-networking/uquic` 的 `QUICSpec`（TLS 层为 `refraction-networking/utls`），1250 字节数据报、8 字节 DCID、空 SCID、包号从 1 开始；ClientHello 只含三个 TLS 1.3 密码套件，扩展与传输参数每次连接重排，带一个 `version_information` GREASE 版本、一个 GREASE 传输参数与 AES-128-GCM GREASE ECH
- **Chaos 保护**：按 Chromium `QuicPacketCreator::MultiPacketChaosProtect` 与 `QuicChaosProtector` 分布在两个 Initial 中：第一个放 ClientHello 开头 55–86 字节与末尾，第二个放中间；每包将 CRYPTO 帧拆段并穿插 2–10 个 PING 与分散的 PADDING，帧序随机
- **HTTP/3 帧**：握手后依次打开控制、QPACK 解码器、QPACK 编码器单向流；控制流 SETTINGS 按标识符升序含 `QPACK_MAX_TABLE_CAPACITY=65536`、`MAX_FIELD_SECTION_SIZE=262144`、`QPACK_BLOCKED_STREAMS=100`、`H3_DATAGRAM=1` 与一个 GREASE 设置，其后跟一个 GREASE 帧；收到对端 SETTINGS 后在编码器流设置动态表容量并插入静态表无精确匹配的字段，请求前发 `PRIORITY_UPDATE`，HEADERS 只引用表项，响应引用动态表时经解码器流确认

### `accept-language` 解析规则

按 Chromium 规则将系统语言解析为 Electron 44 随附的 55 个语言包之一：

1. **来源**：Windows 取 `GetUserDefaultLocaleName`；其余系统取 `LANGUAGE`、`LC_ALL`、`LC_MESSAGES`、`LANG` 中首个非空值，按 `:` 拆分后取首个可解析项
2. **规范化**：去掉 `.` 后的字符集，`_` 换为 `-`，Windows 排序后缀（如 `es-ES_tradnl`）换为 `@` 变体
3. **精确匹配**：不区分大小写匹配语言包名；带 `@` 的名称只做精确匹配
4. **地区回退**：`es-ES` → `es`，其余 `es-*` → `es-419`；`pt-BR` → `pt-BR`，其余 `pt-*` → `pt-PT`；`zh-HK`、`zh-MO` → `zh-TW`，其余 `zh-*` → `zh-CN`；`en-LR`、`en-PH` → `en-US`，其余 `en-*` → `en-GB`；其他语言回退到语言代码
5. **别名**：`en` → `en-US`、`iw` → `he`、`no` → `nb`、`pt` → `pt-BR`、`tl` → `fil`、`zh` → `zh-CN`
6. **默认**：`en-US`

55 个语言包（`internal/app/locale.go` 的 `electronLocales`）：

```text
af am ar bg bn ca cs da de el en-GB en-US es es-419 et fa fi fil fr gu
he hi hr hu id it ja kn ko lt lv ml mr ms nb nl pl pt-BR pt-PT ro
ru sk sl sr sv sw ta te th tr uk ur vi zh-CN zh-TW
```

### 新版本对齐步骤

清单中的 `version` 高于 `internal/antigravity` 的 `ClientVersion` 时，管理端提示有新版本。对齐流程：

1. 下载并安装新版本，记录 `package.json` 的版本与各系统 `language_server --stamp` 的 CL；灰度版本只对部分 `x-user-staging-id` 返回，换随机标识多次读取各系统清单即可取得下载地址
2. 更新 `internal/antigravity/version.go` 的 `ClientVersion`、`ElectronVersion`、`ChromeVersion` 与各平台 `changelists`
3. 核对运行时指纹：
   - 用 `go version -m` 比较 LS 的 Go 工具链；主版本变化时，逐字节对照新版本现场的 ClientHello
   - Electron 版本变化时，对照 `internal/app/updater.go` 的 ClientHello、HTTP/2 帧与代理 CONNECT User-Agent，并对照直连现场的 `updater_quic.go` QUIC Initial 与 `updater_h3.go` HTTP/3 帧
4. 按第 2 节抓取登录、冷启动、空闲与各模型最小请求
5. 逐项对照初始化与冷启动序列、周期请求清单与间隔、信封字段与顺序、`labels`、系统提示各段原文、模型目录字段与伴随请求
6. 按下表修改存在差异的层，并同步更新 [协议参考](protocol.md)

| 变化 | 位置 | 是否改码 |
| --- | --- | --- |
| 模型增删、档位、上限 | 账户实时目录 | 否 |
| 版本号、构建号、Electron 与 Go 运行时 | `internal/antigravity/version.go` | 是 |
| 系统提示段落原文 | `internal/prompt/official.txt` | 是 |
| 系统提示动态段（如推荐插件与实验开关） | `official.txt` 模板字段与 `internal/antigravity` 取值 | 是 |
| Tab 补全提示词 | `internal/antigravity/tab_prompt.txt` | 是 |
| Live 双向流 proto 描述符 | `internal/antigravity/live.desc.gz` | 是 |
| 会话 clearcut 配置（执行配置、工具、提示段、开关） | `internal/antigravity/session.json` | 是 |
| 登录前 clearcut 上报 | `internal/antigravity/prelogin_*.bin.gz` | 是 |
| 初始化、冷启动或周期请求的顺序与节奏 | `internal/antigravity` 初始化与伴随请求 | 是 |
| 信封字段、`labels`、`requestId` 格式 | `internal/antigravity/generate.go` | 是 |
| SSE 字段或新 `finishReason` | `DecodeStream` 与四个协议投影 | 是 |
| TLS 或 HTTP 形态 | `internal/antigravity` 的 Transport | 是 |
| 错误响应的原因与字段 | `UpstreamError` 与冷却原因表 | 是 |

### 失效信号排查

| 失效信号 | 变化层 | 对照项 |
| --- | --- | --- |
| 目录中新型号请求 404 或下线提示 | UA 版本门控 | UA 版本与构建号 |
| 全部请求 403 | 账户状态或身份 | 错误原因、UA、TLS ClientHello |
| 生成 400 `INVALID_ARGUMENT` | 信封 | 新版本现场的同一请求逐字段对照 |
| 无 `details` 的 429 | 系统提示内容 | 去掉客户端系统提示后复试 |
| 新的 SSE 字段被丢弃 | 流解码 | 原始 SSE 与规范事件对照 |
