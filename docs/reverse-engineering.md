# Antigravity 官方客户端逆向指南

Antigravity2API 的上游请求以官方客户端的现场为准。本文说明官方客户端的组件与请求归属、抓包与静态分析方法，以及官方发布新版本后重新对齐的步骤。协议字段、请求顺序与信封见 [协议参考](protocol.md)。

## 1. 组件与请求归属

官方客户端（Antigravity Hub）由 Electron 外壳与 Go 编写的 language server（LS）组成。界面是 LS 内嵌的网页，由 LS 在 `https://127.0.0.1:<端口>/` 提供，Electron 窗口加载它。

| 组件 | 文件 | 发出的请求 | 产品中的对应实现 |
| --- | --- | --- | --- |
| Electron 外壳 | `Antigravity.exe`、`resources/app.asar` | 更新清单 `latest-x64-win.yml` | `internal/app` 的版本检查 |
| language server | `resources/bin/language_server[.exe]` | OAuth 登录与刷新、cloudcode `v1internal`、userinfo、头像、unleash、clearcut、`aicode.googleapis.com` 的 `fetchConfig` 与 gRPC 语音转写 | `internal/antigravity` |
| 界面 | LS 内嵌的网页包 | 只访问本机 LS | 管理端（`web/`） |

LS 的出站请求全部使用 Go 标准库：cloudcode 请求为 HTTP/1.1；userinfo 使用 Go 默认 User-Agent；头像经 HTTP/2；gRPC 使用 grpc-go 1.86.0-dev。产品以同一套 Go 标准库发送这些请求，TLS ClientHello 与 HTTP 形态由 Go 版本决定，官方 LS 的 Go 工具链升级后需要重新对照（第 4 节）。

## 2. 抓包

### 环境

| 项 | 做法 |
| --- | --- |
| 代理 | mitmproxy regular 模式的两个端口，见表下 |
| 证书 | mitmproxy 根证书装入系统根证书库，见表下 |
| 发送进程 | 按连接的本地端口查出发送进程，区分 LS 与 Electron |
| TLS | 记录 ClientHello 并在回环网卡抓包，见表下 |
| 正文 | SSE 与 gRPC 流逐块保存，见表下 |
| 界面操作 | 经 CDP 操作界面，经 inspector 取得登录链接 |
| 隔离用户目录 | 用户目录环境变量指向独立目录 |

- 代理：LS 经环境变量 `HTTPS_PROXY`、`HTTP_PROXY` 使用一个端口，Electron 经启动参数 `--proxy-server` 使用另一个，`NO_PROXY=localhost,127.0.0.1` 保留本机连接；按端口区分发送方
- 证书：Windows 装入 `LocalMachine\Root`，LS 使用系统根证书；抓包结束后移除
- TLS：mitmproxy 的 `tls_clienthello` 钩子记录每个 ClientHello；同时在回环网卡抓包（Windows 用 Npcap 的 dumpcap），保留原始 TLS 字节供逐字节对照
- 正文：SSE 响应、gRPC 请求流与响应流逐块保存，记录分块时刻与 gRPC trailer
- 界面操作：Electron 启动参数加 `--remote-debugging-port=<端口>` 与 `--inspect=127.0.0.1:<端口>`；经 inspector 替换主进程的 `shell.openExternal`，登录链接由此取得
- 隔离用户目录：`USERPROFILE`、`HOME`、`APPDATA`、`LOCALAPPDATA` 指向独立目录，Electron 加 `--user-data-dir`
- 在 `<目录>\.gemini\cache\antigravity-keyring-unavailable` 写入标记后，LS 把 token 保存到 `<目录>\.gemini\jetski-standalone-oauth-token`
- 系统凭据库已有 `gemini:antigravity` 时 LS 仍读取它；隔离账户在没有该凭据的系统用户或虚拟机中运行

每个 HTTP 交换记录一行：

```json
{"sequence": 59, "sender": "language_server", "client": ["127.0.0.1", 61486], "startedAt": 1791960442.96527, "endedAt": 1791960449.91067, "method": "POST", "url": "https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse", "httpVersion": "HTTP/1.1", "tls": {"sni": "daily-cloudcode-pa.googleapis.com", "alpn": "", "version": "TLSv1.3", "cipher": "TLS_AES_256_GCM_SHA384"}, "requestHeaders": [["Host", "daily-cloudcode-pa.googleapis.com"], ["User-Agent", "antigravity/hub/2.25.0 (aidev_client; os_type=windows; arch=amd64; cl=1003374690)"], ["Transfer-Encoding", "chunked"], ["Authorization", "Bearer <token>"], ["Content-Type", "application/json"], ["Accept-Encoding", "gzip"]], "requestBody": "00059.request.bin", "status": 200, "responseHeaders": [["Content-Type", "text/event-stream"], ["Transfer-Encoding", "chunked"]], "responseBody": "00059.response.sse"}
```

请求头保持发送顺序与重复项，正文保存原始字节。对照时比较头集合与顺序、正文字段顺序、分块方式与连接复用，压缩或重新序列化会隐藏这些差异。

### 场景

| 场景 | 操作 | 得到的协议 |
| --- | --- | --- |
| 登录 | 退出登录后点击登录并完成 Google 授权 | 授权链接参数、token 交换、登录后初始化序列 |
| 冷启动 | 已登录状态下启动客户端 | 冷启动初始化序列、`tokeninfo` |
| 空闲 | 启动后不操作 70 分钟以上 | 周期请求与节奏、token 刷新时机 |
| 各模型最小请求 | 每个 Agent 模型发一条短消息 | 信封、`labels`、`generationConfig`、三种模型族的 SSE |
| 工具往返 | 让模型调用工具后继续追问 | `functionCall`、`functionResponse`、思考签名回放 |
| 多轮与长推理 | 同一会话连续对话，High 档长推理 | 历史回放、缓存命中、签名 |
| 媒体 | 附图片发送；生成与编辑图片；网页搜索；语音输入 | `inlineData`、`image_gen`、`web_search`、gRPC 语音转写 |
| 停止 | 生成中点击停止 | 取消方式与下一轮历史 |
| 设置 | 修改设置项前后各抓一次空闲与会话 | 修改设置的请求，开关对伴随请求的影响 |
| 错误 | 下线模型、额度不足、需要验证的账户 | 错误响应形态 |

同一场景至少重复两次，区分稳定字段与每次随机的字段（会话标识、`requestId`、执行标识、时间）。

## 3. 静态分析

### Electron 外壳

`app.asar` 用 `npx @electron/asar extract app.asar <目录>` 解包。

| 文件 | 内容 |
| --- | --- |
| `package.json` | 客户端版本（UA 中的版本号） |
| `dist/languageServer.js` | LS 启动参数：`--override_ide_name`、`--subclient_type`、`--override_ide_version`、`--override_user_agent_name`、`--cloud_code_endpoint`、`--api_server_url`、`--app_data_dir` 等 |
| `dist/main.js` 与同目录模块 | 窗口、托盘、更新源与检查节奏、深链协议 |

### language server

| 信息 | 方法 |
| --- | --- |
| 构建号 | 执行 `language_server --stamp`，输出中 `Built at CL: <数字>` 即 UA 的 `cl=` |
| Go 工具链 | `go version -m <二进制>` 输出工具链版本与 GOEXPERIMENT |
| 函数名 | 解析 pclntab 得到全部函数地址与名称，导入 IDA 后反汇编 |
| proto | 从 `.rodata` 提取内嵌的 FileDescriptorProto，得到 `v1internal` 全部方法、请求与响应消息、枚举值 |
| User-Agent | 反汇编 `codeassistclient.UserAgent`：`<name>/<subclient>/<version> (aidev_client; os_type=<GOOS>; arch=<GOARCH>; cl=<CL>)` |
| 请求头 | 反汇编 `(*CodeAssistClient).setHeaders` |
| OAuth | 字符串中的 client ID、client secret、scope 与回调地址 |
| 端点选择 | 反汇编 `loadCodeAssist` 之后的端点重算 |
| 内嵌界面 | `.rdata` 中的 zip，按中央目录结束记录定位后解压 |

字符串与反汇编给出候选值，抓包确认实际取值；两者不一致时以抓包为准。

## 4. 官方新版本对齐

官方客户端的更新清单为：

```text
GET https://antigravity-hub-auto-updater-974169037036.us-central1.run.app/manifest/latest-x64-win.yml?noCache=<随机>
x-app-version: <当前版本>
user-agent: electron-builder
```

服务以官方 Electron 44（Chromium 152）网络栈的形态读取清单（`internal/app/updater.go` 为传输层，`updater_quic.go` 与 `updater_h3.go` 为 QUIC 与 HTTP/3，`internal/app/version.go` 为请求与节奏）：

| 项 | 取值 |
| --- | --- |
| ClientHello | uTLS `Chrome_152_PSK` 去掉扩展 `0xca34` |
| HTTP/2 前言之后 | SETTINGS 与连接级 WINDOW_UPDATE，见表下 |
| 连接 | 一条连接保持到对端关闭或 GOAWAY，TCP keep-alive 45 秒 |
| HEADERS | 独占、依赖 0、权重字节 109 |
| 伪头顺序 | `:method`、`:authority`、`:scheme`、`:path` |
| 请求头顺序 | 见表下 |
| `noCache` | 当前毫秒时间戳的 32 进制 |
| `x-user-staging-id` | `RUNTIME_DIR/.updaterId` 的内容 |
| `accept-language` | 系统语言解析出的语言包名，规则见下方 |
| 代理 | 按 `PROXY` 与系统代理选择；经代理时只走 TCP |
| HTTP/3 替代服务 | 响应 `alt-svc` 中的 `h3=":443"`，有效期取 `ma`，缺省 24 小时 |
| 竞速 | 不经代理且替代服务未过期时，先发出 QUIC 的 Initial，再使用现有 HTTP/2 连接或开始 TCP，先就绪的连接承载请求；QUIC 连接收到 HANDSHAKE_DONE 后才算就绪 |
| 落后的连接 | QUIC 照常完成握手并发出控制流；TCP 照常完成 TLS 握手，不发 HTTP/2 前言，闲置到下一次读取时关闭 |
| QUIC Initial | 1250 字节数据报，8 字节 DCID、空 SCID，包号从 1 开始，ClientHello 分布在两个 Initial 中 |
| HTTP/3 | 控制流 SETTINGS `QPACK_MAX_TABLE_CAPACITY=65536`、`MAX_FIELD_SECTION_SIZE=262144`、`QPACK_BLOCKED_STREAMS=100`、`H3_DATAGRAM=1`；请求前发 PRIORITY_UPDATE，请求头经 QPACK 动态表 |
| QUIC 空闲 | 30 秒无活动后静默关闭 |
| 时限与解析 | 单次读取 1 分钟，见表下 |

- HTTP/2 前言之后为 SETTINGS `HEADER_TABLE_SIZE=65536`、`ENABLE_PUSH=0`、`INITIAL_WINDOW_SIZE=6291456`、`MAX_HEADER_LIST_SIZE=262144`，接着连接级 WINDOW_UPDATE 15663105，三者在同一 TLS 记录中
- HEADERS 帧的优先级字段写入 Go `http2.PriorityParam{Exclusive: true, Weight: 109}`：帧中的权重字节为 109，按 RFC 9113 表示权重 110
- 请求头顺序为 `x-user-staging-id`、`x-app-version`、`user-agent: electron-builder`、`cache-control: no-cache`、`sec-fetch-site: none`、`sec-fetch-mode: no-cors`、`sec-fetch-dest: empty`、`accept-encoding: gzip, deflate, br, zstd`、`accept-language`、`priority: u=4, i`
- `.updaterId` 不存在时以 4096 个随机字节为名称、OID 命名空间生成 UUID v5 并写入
- 非 200 为失败；正文中第一行 `version:` 之后的值为最新版本
- ClientHello 取 `github.com/bogdanfinn/tls-client` v1.16.0 的 `profiles.Chrome_152_PSK`（TLS 层为 `github.com/bogdanfinn/utls` v1.7.8-barnius），去掉 `0xca34` 扩展；有缓存的会话时末尾带 `pre_shared_key`，TLS 会话缓存跨连接保留
- `:path` 为不索引字面量；各头的值在 Huffman 编码更短时用 Huffman
- QUIC 取 `github.com/refraction-networking/uquic` 的 `QUICSpec`（TLS 层为 `github.com/refraction-networking/utls`）：ClientHello 只含 TLS 1.3 的三个密码套件，扩展与传输参数每次连接重新排列，`version_information` 带一个 GREASE 版本，另有一个 GREASE 传输参数与 AES-128-GCM 的 GREASE ECH
- Initial 按 Chromium `QuicPacketCreator::MultiPacketChaosProtect` 与 `QuicChaosProtector` 布置：第一个 Initial 放 ClientHello 开头 55–86 字节与末尾，第二个放中间；每个包再把 CRYPTO 帧拆成多段，加 2–10 个 PING，PADDING 分散在各帧之间，帧序随机
- HTTP/3 握手完成后依次打开控制、QPACK 解码器、QPACK 编码器单向流，控制流的 SETTINGS 按标识符升序并带一个 GREASE 设置，其后是一个 GREASE 帧；收到对端 SETTINGS 后发出请求：编码器流设置动态表容量并插入静态表中没有精确匹配的字段，HEADERS 只引用表项，引用动态表的响应头块在解码器流上确认
- 经 HTTP 代理时 CONNECT 带 `Host`、`Proxy-Connection: keep-alive` 与 User-Agent `Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Antigravity/<ClientVersion> Chrome/152.0.7977.130 Electron/44.4.3 Safari/537.36`

`accept-language` 按 Chromium 规则把系统语言解析为 Electron 随附的 55 个语言包之一：

- 来源：Windows 为 `GetUserDefaultLocaleName`；其他系统取环境变量 `LANGUAGE`、`LC_ALL`、`LC_MESSAGES`、`LANG` 中第一个非空的，按 `:` 分成多个名称，依次解析，取第一个能解析的
- 规范化：去掉 `.` 之后的字符集，`_` 换成 `-`，Windows 名称中的排序后缀（如 `es-ES_tradnl`）换成 `@` 变体
- 精确匹配：与语言包名比较，不区分大小写；带 `@` 的名称只做精确匹配
- 地区回退：`es-ES` 为 `es`，其余 `es-*` 为 `es-419`；`pt-BR` 为 `pt-BR`，其余 `pt-*` 为 `pt-PT`；`zh-HK`、`zh-MO` 为 `zh-TW`，其余 `zh-*` 为 `zh-CN`；`en-LR`、`en-PH` 为 `en-US`，其余 `en-*` 为 `en-GB`；其他语言回退到语言代码
- 别名：语言代码 `en` 为 `en-US`、`iw` 为 `he`、`no` 为 `nb`、`pt` 为 `pt-BR`、`tl` 为 `fil`、`zh` 为 `zh-CN`
- 默认：以上都不成时为 `en-US`

Electron 44 随附的 55 个语言包（`internal/app/locale.go` 的 `electronLocales`）：

```text
af am ar bg bn ca cs da de el en-GB en-US es es-419 et fa fi fil fr gu
he hi hr hu id it ja kn ko lt lv ml mr ms nb nl pl pt-BR pt-PT ro
ru sk sl sr sv sw ta te th tr uk ur vi zh-CN zh-TW
```

响应中的 `version` 高于 `internal/antigravity` 的 `ClientVersion` 时，管理端提示有新版本。对齐步骤：

1. 下载并安装新版本，记下 `package.json` 的版本与各系统 `language_server --stamp` 的 CL；灰度版本只对部分 `x-user-staging-id` 返回，换随机标识多次读取各系统的清单可得到下载地址
2. 更新 `internal/antigravity/client.go` 的 `ClientVersion`、`internal/antigravity/device.go` 的 `changelists` 与 `internal/antigravity/companion.go` 的 `serverRuntimes`（unleash `platformVersion`，取自各系统 `go version -m` 的工具链行）
3. 用 `go version -m` 比较 LS 的 Go 工具链；主版本变化时，用新版本现场的 ClientHello 与产品逐字节对照；Electron 版本变化时，用新版本的更新清单现场对照 `internal/app/updater.go` 的 ClientHello、HTTP/2 帧与 CONNECT 的 User-Agent，不经代理的现场对照 `updater_quic.go` 的 QUIC Initial 与 `updater_h3.go` 的 HTTP/3 帧
4. 抓登录、冷启动、空闲与各模型最小请求（第 2 节）
5. 逐项对照：初始化与冷启动序列、周期请求清单与间隔、信封字段与顺序、`labels`、系统提示各段原文、模型目录字段、伴随请求
6. 有差异的层按下表修改，并更新 [协议参考](protocol.md)

| 变化 | 位置 | 是否改码 |
| --- | --- | --- |
| 模型增删、档位、上限 | 账户实时目录 | 否 |
| 版本号、构建号与 Go 运行时 | `ClientVersion`、`changelists`、`serverRuntimes` | 是 |
| 系统提示段落原文 | `internal/prompt/official.txt` | 是 |
| 系统提示中随实验开关与远端目录生成的内容 | `official.txt` 的模板字段与 `internal/antigravity` 中的取值，如推荐插件段与插件市场目录 | 是 |
| 会话 clearcut 事件的执行配置、内置工具、提示段与实验开关名 | `internal/antigravity/session.json` | 是 |
| 登录前的 clearcut 上报 | `internal/antigravity/prelogin_*.bin.gz` | 是 |
| 初始化、冷启动或周期请求的顺序与节奏 | `internal/antigravity` 的初始化与伴随请求 | 是 |
| 信封字段、`labels`、`requestId` 格式 | `internal/antigravity/generate.go` | 是 |
| SSE 字段或新的 `finishReason` | `DecodeStream` 与四个协议的投影 | 是 |
| TLS 或 HTTP 形态 | `internal/antigravity` 的 Transport | 是 |
| 错误响应的原因与字段 | `UpstreamError` 与冷却原因表 | 是 |

| 失效信号 | 变化层 | 对照项 |
| --- | --- | --- |
| 目录中新型号请求 404 或下线提示 | UA 版本门控 | UA 版本与构建号 |
| 全部请求 403 | 账户状态或身份 | 错误原因、UA、TLS ClientHello |
| 生成 400 `INVALID_ARGUMENT` | 信封 | 新版本现场的同一请求逐字段对照 |
| 无 `details` 的 429 | 系统提示内容 | 去掉客户端系统提示后复试 |
| 新的 SSE 字段被丢弃 | 流解码 | 原始 SSE 与规范事件对照 |
