# 开发与贡献

Antigravity2API 是单个 Go 进程：公开协议适配、账户池、上游客户端、请求账本、管理 API 与内嵌的 React 管理端都在同一监听上运行。上游请求的格式、身份与伴随请求见 [协议参考](protocol.md)，日志与请求账本见 [运行日志与请求账本](logging.md)，对齐官方新版本的方法见 [逆向指南](reverse-engineering.md)。

## 1. 环境与构建

| 场景 | 必需组件 |
| --- | --- |
| 运行 Release | 对应平台的可执行文件或安装包 |
| 命令行版源码构建 | Go 1.26、Node.js 22.22+ 或 24+、pnpm 11.10 |
| 桌面版源码构建 | 另需 Wails v3 CLI `v3.0.0-beta.29`；macOS 与 Linux 需要 CGO |
| Linux 桌面版 | `libgtk-4-dev`、`libwebkitgtk-6.0-dev` |

Go 程序以 `go:embed` 内嵌 `internal/webui/dist`，构建 Go 之前先构建前端：

```bash
cd web
pnpm install --frozen-lockfile
pnpm run build          # 输出到 ../internal/webui/dist
cd ..
go build ./cmd/antigravity2api                          # 命令行版
go build -tags desktop,production ./cmd/antigravity2api # 桌面版
```

Windows 上构建前用 `wails3 generate syso` 生成图标、清单与版本信息资源，命令行版与桌面版都带这些资源；桌面版另加 `-ldflags='-H windowsgui'`。前端开发时运行 `pnpm dev`，Vite 把 `/api`、`/v1` 与 `/v1beta` 代理到 `http://127.0.0.1:8017`。

前端检查与 CI 相同：

```bash
pnpm run lint
pnpm run typecheck
```

### CI 与发布

`.github/workflows/build.yml` 在推送 `main`、推送 `v*` 标签、PR 与手动触发时运行：

| 作业 | 内容 | 产物 |
| --- | --- | --- |
| `web` | 前端检查与构建，解析版本 | `internal/webui/dist` |
| `headless` | 测试、检查、构建与交叉编译 | Linux 与 macOS 共 4 个命令行包 |
| `desktop-windows` | `desktop,production` 构建，GUI 子系统，带图标、清单与版本信息 | `<版本>-windows-amd64.zip` |
| `desktop-macos` | arm64 与 amd64 合并为通用二进制，ad-hoc 签名的 `Antigravity2API.app` | `desktop-<版本>-darwin-universal.tar.gz` |
| `desktop-linux` | amd64 与 arm64 | `desktop-<版本>-linux-<架构>.tar.gz` |
| `release` | 只在推送标签时运行 | GitHub Release |
| `docker` | 只在推送标签时运行 | `ghcr.io/mag1cfall/antigravity2api` |

- 工具链：全部作业设 `GOTOOLCHAIN=local`。`web` 作业用 Node.js 22.23.3 与 `web/package.json` 的 `packageManager` 指定的 pnpm 11.10.0；`headless` 作业先以 `go.mod` 的 Go 1.26.0 运行 `go test`、`go vet` 与构建，再装最新稳定版 Go 交叉编译命令行包；`desktop-windows`、`desktop-macos`、`desktop-linux` 以最新稳定版 Go 构建
- 包版本：标签构建为标签名，其余构建为提交号前 7 位，用于包名与可执行文件资源；资源与桌面包的数字版本去掉 `v` 与 `-` 之后的部分，非标签构建为 `0.0.0`。程序报告的版本取 Go 构建信息中的模块版本（见[在线更新](#在线更新)）
- 命令行包：`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`，`CGO_ENABLED=0`、`-trimpath -ldflags='-s -w'`，tar.gz 中只有 `antigravity2api`
- Windows 包：zip 中只有桌面版 `antigravity2api.exe`（`-H windowsgui`）；带子命令启动时附加启动它的控制台，没有控制台时新建一个，重定向的标准输入输出保持不变
- 桌面包：macOS 为通用 `Antigravity2API.app` 的 tar.gz，Linux 为只含 `antigravity2api` 的 tar.gz
- 包名：Windows 包与命令行包为 `antigravity2api-<版本>-<系统>-<架构>`，macOS 与 Linux 桌面包为 `antigravity2api-desktop-<版本>-<平台>`
- Release：标签含 `-` 时标为预发布，说明由 GitHub 生成
- 镜像：`linux/amd64` 与 `linux/arm64`，标签取 Git 标签原文（如 `v0.1.3`），标签不含 `-` 时另推 `latest`
- `Dockerfile` 分三段：`node:22-trixie-slim` 以 corepack 启用 pnpm 构建前端；`golang:1.27-trixie` 以 `CGO_ENABLED=0` 交叉编译；`debian:trixie-slim` 加装 `ca-certificates` 与 `tzdata` 运行，工作目录 `/data`，`LISTEN=0.0.0.0:8017`，暴露 8017，默认命令 `antigravity2api serve`

## 2. 目录、组件与依赖

```text
cmd/antigravity2api/   # 子命令、命令行版与桌面版入口（构建标签 desktop）
internal/
  antigravity/         # 上游客户端：OAuth、设备档案、初始化、伴随请求、目录、信封、SSE 解码、图像与语音转写
  api/                 # OpenAI、Anthropic、Gemini 公开协议，Files、Images、Audio、Interactions
  app/                 # 监听装配、访问日志、请求记录、管理 API、管理事件流、登录与配对、版本检查
  auth/                # auth/<账户>/ 的读写与官方客户端凭据导入
  balancer/            # 账户池：模型可用性、冷却、并发槽位、会话粘连
  config/              # .env 读取、校验、保存与代理选择
  proxypool/           # PROXY 代理池：账户绑定、检测与改绑
  desktop/             # Wails 窗口、托盘、单实例与开机启动
  logging/             # slog 输出、日志文件轮转与崩溃文件
  prompt/              # 各系统提示档位的官方段与 user 轮模板
  requestdb/           # SQLite 请求账本、汇总、额度快照、日志与费用估算
  service/             # 生成服务：模型解析、会话记录、请求改写、输出核对、换号与账户生命周期
  tokencount/          # Gemma 3、cl100k_base 与 o200k_base 分词的本地 token 估算
  webui/               # 内嵌管理端静态资源
web/                   # 管理端源码（React 19、Vite、Tailwind CSS）
build/                 # 桌面版图标、Windows 清单与版本信息、Info.plist
```

`build/` 中的打包资源，`0.0.0` 在 CI 中替换为数字版本：

| 文件 | 关键字段 |
| --- | --- |
| `darwin/Info.plist` | `CFBundleIdentifier` 为 `com.mag1cfall.antigravity2api` |
| `darwin/Info.plist` | `CFBundleName` 为 `Antigravity2API`，`CFBundleExecutable` 为 `antigravity2api` |
| `darwin/Info.plist` | `CFBundleIconFile` 为 `icons`，对应 `darwin/icons.icns` |
| `darwin/Info.plist` | `LSMinimumSystemVersion` 为 `12.0.0`，`NSHighResolutionCapable` 为 `true` |
| `darwin/Info.plist` | `NSAppTransportSecurity.NSAllowsLocalNetworking` 为 `true` |
| `darwin/Info.plist` | `CFBundleVersion`、`CFBundleShortVersionString` 为版本占位 |
| `windows/wails.exe.manifest` | `assemblyIdentity` 名称 `com.mag1cfall.antigravity2api` |
| `windows/wails.exe.manifest` | 依赖 Common-Controls 6.0 |
| `windows/wails.exe.manifest` | `dpiAware` 为 `true/pm`，`dpiAwareness` 为 `permonitorv2,permonitor` |
| `windows/wails.exe.manifest` | `requestedExecutionLevel` 为 `asInvoker` |
| `windows/info.json` | `ProductName` 为 `Antigravity2API`，`CompanyName` 为 `Mag1cFall` |
| `windows/info.json` | `FileDescription` 为 `Google Antigravity API proxy` |
| `windows/info.json` | `LegalCopyright` 为 `Copyright (c) 2026 Mag1cFall` |
| `windows/icon.ico` | 可执行文件图标 |

依赖方向：

```text
cmd ──► app ──► api ──► antigravity
 │       ├────► service ──► balancer, prompt, tokencount, auth, requestdb, config ──► antigravity
 │       └────► auth, config, logging, requestdb
 └────► desktop, webui
```

- `recordPublic` 位于 api 层之前：满足条件的 POST 都写入请求账本，包括随后被浏览器来源检查或请求体上限拒绝的请求；API key 不匹配的请求不入账
- `antigravity` 不依赖其他内部包；`api` 只依赖 `antigravity` 的类型与 `Service` 接口（`Models`、`Generate`、`CountTokens`）
- `service.Service` 实现 `antigravity.Service`；`app` 用 `recordingService` 包装它，在生成开始时写请求日志并转发事件给请求记录
- `logging` 只依赖 `config` 的日志文件大小常量

公开生成请求的处理链：

```text
HTTP 请求
  -> access：生成请求 ID，写 X-Request-ID，结束后写访问日志
  -> securityHeaders
  -> recordPublic（POST，且 API key 与配置的某个 key 相同或未配置 key）：建立请求记录、管理端停止入口
  -> api：CORS、请求体上限、浏览器来源检查、API key、Files 存储
  -> 协议适配：转成规范 GenerateRequest
  -> recordingService.Generate：写“请求开始”，转发事件并检测停顿
  -> service.Generate：解析模型、取得会话、改写请求、选账户、组装信封
  -> antigravity.Client.StreamGenerateContent -> DecodeStream
  -> service 输出处理：思考可见性、停止序列、工具约定、JSON 校验、输出上限
  -> 协议投影：SSE 或 JSON
  -> 请求结束：写入请求账本，推送 request 事件，写“请求完成”
```

api 层的公开接口规则：

- CORS：响应带 `Access-Control-Allow-Origin: *` 与 `Access-Control-Expose-Headers: *`；`OPTIONS` 预检回显 `Access-Control-Request-Headers`，允许 `GET, POST, PUT, DELETE, OPTIONS`，返回 204
- 请求体上限 716876456 字节（512 MiB 文件的 base64 长度加 1 MiB），超出时读取请求体失败，返回 400
- 浏览器来源：`API_KEYS` 为空时，`Origin` 为 `null`、无法解析，或为 `http`、`https` 且主机不是 localhost 与回环地址的请求按 API key 不匹配处理；配置了 key 时不检查来源
- API key：依次取 `X-Goog-API-Key`、`X-API-Key`、`Authorization: Bearer`、`?key=`，任一项与配置的某个 key 相同即通过，记录与用量按该 key 统计；都不匹配时返回所属协议的认证错误
- 认证错误的协议：路径以 `/v1beta/`、`/upload/` 或 `/v1/interactions` 开头为 Gemini（带 key 为 400 `INVALID_ARGUMENT`，不带 key 为 403 `PERMISSION_DENIED`），路径以 `/v1/messages` 开头或带 `anthropic-version` 头为 Anthropic 401 `authentication_error`，其余为 OpenAI 401 `invalid_api_key`；错误体见协议参考“错误映射”
- 未知路径：`/v1/`、`/v1beta/`、`/upload/` 下的请求先经过上面的检查，再由 Go `ServeMux` 匹配端点；没有匹配的端点时（路径不存在，或路径存在而方法不同，如 `GET /v1/chat/completions`）以所属协议的错误体返回 404（见协议文档“API 投影”的错误映射）；任一路径的 `OPTIONS` 都返回 204
- 客户端 IP：取直连地址

## 3. 配置与持久状态

### 配置

配置项、默认值与含义见 README 的“环境变量”。读取顺序：

1. 当前目录的 `.env`；文件不存在时全部取默认值
2. 进程环境变量中的同名项覆盖文件值
3. 启动参数 `--listen`、`--auth`、`--runtime`、`--proxy` 覆盖 `LISTEN`、`AUTH_DIR`、`RUNTIME_DIR`、`PROXY`

`LISTEN` 的端口被 Windows 保留（监听返回 WSAEACCES 10013，Hyper-V、WSL2 或 Docker 的 WinNAT 保留端口段）时，服务从下一个端口起依次尝试，在第一个可监听的端口上运行并记一条 WARN；`GET /api/status` 的 `listen` 为实际地址、`reserved_listen` 为被保留的配置地址，管理端各页顶部显示两者。

`.env` 的解析规则：

| 项 | 规则 |
| --- | --- |
| 空行、`#` 开头的行 | 跳过 |
| 没有 `=` 的行 | 报错，带文件名与行号 |
| 不在配置项清单中的键 | 忽略 |
| 单引号值 | 原文 |
| 双引号值 | 按 Go 字符串字面量解码 |
| 裸值 | 去掉 ` #` 之后的行尾注释 |
| 列表项（`API_KEYS`、`PROXY`、`SYSTEM_PROMPT_SECTIONS`、`PREFER_LOWER_TIER_MODELS`、`MODEL_MAPPING`） | 按逗号切分，见表下 |
| `MODEL_MAPPING` | 每项以最后一个 `:` 分成别名与模型 ID，两边各去掉首尾空白 |
| `REQUEST_TIMEOUT`、`QUOTA_REFRESH_INTERVAL` | Go 时长，例如 `30s`、`5m`；`QUOTA_REFRESH_INTERVAL` 另接受 `0` |
| 布尔项（`WEEKLY_QUOTA_WARMUP`、`LOG_FILE`、`REQUEST_LOG_BODIES`、`ADMIN_AUTH_ENABLED`） | Go `strconv.ParseBool`，见表下 |
| 整数项（`ACCOUNT_CONCURRENCY`、`QUOTA_RESERVE_PERCENT`、`LOG_MAX_TOTAL_MB`） | 十进制整数，其他值报错 |
| `MEDIA_OUTPUT`、`LOG_LEVEL`、`LOG_FORMAT` | 去掉首尾空白后转小写 |
| 其他文本项 | 去掉首尾空白，大小写敏感 |

- 列表项每项去掉首尾空白，空项忽略；`PROXY` 保存为去掉首尾空白的原文，使用时切分
- 布尔项接受 `1`、`t`、`T`、`true`、`TRUE`、`True` 与 `0`、`f`、`F`、`false`、`FALSE`、`False`，其他值报错
- `ADMIN_PASSWORD` 保留原文

解析或校验失败时启动报错，管理端保存时返回 400 `invalid_settings`，不取默认值：

| 项 | 合法值 |
| --- | --- |
| `LISTEN` | `host:port` |
| `AUTH_DIR`、`RUNTIME_DIR` | 非空 |
| `PROXY` | 带主机的 `http`、`https` 或 `socks5` URL，逗号分隔 |
| `SYSTEM_PROMPT` | `full`、`medium`、`aggressive`、`custom` |
| `SYSTEM_PROMPT_SECTIONS` | 官方段名 |
| `SYSTEM_PROMPT_WRAP` | `full`、`time`、`none` |
| `MODEL_MAPPING` | 每项为 `别名:模型 ID`，两边非空 |
| `MEDIA_OUTPUT` | `inline`、`url` |
| `MEDIA_BASE_URL` | 空或带主机的 `http`、`https` URL |
| `ACCOUNT_CONCURRENCY` | 正整数，没有上限 |
| `ROUTING_STRATEGY` | `round-robin`、`fill-first` |
| `REQUEST_TIMEOUT` | 正数时长 |
| `QUOTA_REFRESH_INTERVAL` | 0 或不小于 `1m` |
| `QUOTA_RESERVE_PERCENT` | 0 到 99 |
| `LOG_LEVEL` | `debug`、`info`、`warn`、`error` |
| `LOG_FORMAT` | `json`、`text` |
| `LOG_MAX_TOTAL_MB` | 不小于 20 |
| `ADMIN_AUTH_ENABLED` | 为 true 时 `ADMIN_USERNAME` 与 `ADMIN_PASSWORD` 非空 |

- 账户代理同样只接受 `http`、`https`、`socks5` URL，另接受 `direct`

管理端保存设置（`PATCH /api/settings`）的步骤：

| 步骤 | 规则 |
| --- | --- |
| 读取 | `.env` 叠加进程环境变量，启动参数不参与 |
| 合并 | 只替换提交的键，按上方解析与校验规则检查，失败返回 400 `invalid_settings` |
| 写回内容 | 全部项按清单顺序写成 `键=值` 行，见表下 |
| 列表值 | 以 `,` 相连，`MODEL_MAPPING` 每项为 `别名:模型 ID` |
| 字面量 | 含空格、制表符、回车、换行、`#`、`"` 或 `'` 的值写成 Go 双引号字面量 |
| 时长 | 最短形式，如 `30s`、`5m`、`1h` |
| 布尔 | `true`、`false` |
| 写入 | 经同目录临时文件原子替换，权限 0600 |

- 写回的文件只含配置项清单中的键；叠加的环境变量值同样写入文件

保存后的生效：

- 运行配置随之替换，`LISTEN`、`AUTH_DIR` 与 `RUNTIME_DIR` 保持启动值，启动时给出 `--proxy` 时运行配置的 `PROXY` 保持该值，其余项在下一次请求生效
- 日志级别、格式、日志文件与正文保存立即切换
- 管理登录配置变化时撤销全部管理会话
- 代理池换成新的 `PROXY` 列表并请求一次检测

上游代理的选择顺序：账户自设的代理（`direct` 为直连）、`PROXY` 代理池为账户绑定的代理、环境变量 `HTTPS_PROXY`、`HTTP_PROXY` 与 `NO_PROXY`、Windows 当前用户的系统代理设置；不属于某个账户的请求（登录、官方更新清单、在线更新）取 `PROXY` 的第一项。

代理池（`internal/proxypool`）：`PROXY` 的各项（只有一项时也是）组成代理池，未设账户代理的账户在每次请求时取绑定的代理：

| 情形 | 处理 |
| --- | --- |
| 首次检测完成前 | 各代理视为可用 |
| 账户首次请求 | 绑定账户最少的可用代理 |
| 绑定的代理不可用 | 改绑到账户最少的可用代理 |
| 全部代理不可用 | 沿用当前绑定 |
| 保存设置 | 保留仍在列表中的代理的检测结果与绑定 |
| 保存设置后 | 改绑已删除代理上的账户，再重新均衡 |
| 账户停用、删除或改设账户代理 | 解除绑定 |
| 检测 | 并行检测各代理 |

- 账户最少的代理有多个时取列表中靠前的；全部代理不可用时，未绑定的账户在全部代理中取账户最少的
- 已删除代理上的账户按 ID 字典序依次改绑到账户最少的可用代理
- 重新均衡：在可用代理（全部不可用时为全部代理）中，反复把账户最多的代理上 ID 字典序最大的账户移到账户最少的代理，直到两者相差不超过一个；账户最多或最少的代理有多个时取列表中靠前的
- 检测：经代理 GET `https://www.gstatic.com/generate_204`，20 秒内收到任何 HTTP 响应为可用；时机见下表

| 检测时机 | 规则 |
| --- | --- |
| 服务启动 | 立即检测一次 |
| 定时 | 启动后每 5 分钟，计时不因其他检测重置 |
| 请求出错 | 生成类请求的上游尝试遇到连接错误且客户端未取消时请求一次检测 |
| 保存设置 | 请求一次检测 |
| `POST /api/proxies/check` | 在该请求内检测并返回结果 |

- 生成类请求指生成、图像生成、语音转写与 Tab 补全；连接错误的定义见“失败与换号”
- “请求一次检测”交给后台检测循环：已有待处理的请求时合并，检测进行中收到的请求在本次结束后再检测一次

代理池的状态在设置页“网络”分区显示，只有一个代理时同样显示；保存设置后立即重新读取，有未检测的代理时每 2 秒读取一次，其余时间每 30 秒。

系统代理只在 `ProxyEnable` 开启时使用，`ProxyServer` 为 `host:port` 时作为 HTTP 代理，为 `http=…;https=…;socks=…` 时按请求协议选择、其次用 `socks` 项（SOCKS5），`ProxyOverride` 中的主机与 `<local>`（不含点的主机名）直连，回环地址始终直连。

- 系统代理读自注册表 `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings` 的 `ProxyEnable`、`ProxyServer`、`ProxyOverride`，每次选择代理（即每个上游连接请求）都重新读取，运行中修改系统代理后下一个请求即按新设置
- `ProxyOverride` 以 `;` 分隔，每项按 Go `path.Match` 通配匹配小写主机名
- 环境变量 `HTTPS_PROXY`、`HTTP_PROXY`、`NO_PROXY` 经 Go `http.ProxyFromEnvironment` 读取，进程内只读一次
- Windows 以外的系统只用环境变量

### 账户目录

`AUTH_DIR` 下每个账户一个子目录，目录名为邮箱：

| 文件 | 内容 |
| --- | --- |
| `account.json` | 账户资料与设备档案 |
| `token.json` | Go `oauth2.Token` 的 JSON |

- `token.json` 的字段为 `access_token`、`token_type`、`refresh_token`、`expiry`（RFC 3339）与可省略的 `expires_in`；`id_token` 不保存

`account.json` 字段：

| 字段 | 内容 |
| --- | --- |
| `id`、`email` | 邮箱 |
| `label` | 备注 |
| `proxy` | 账户代理，空为跟随全局，`direct` 为直连 |
| `project` | `loadCodeAssist` 的 `cloudaicompanionProject`，空表示待走登录序列 |
| `tier`、`paid_tier` | `currentTier.id` 与 `paidTier.id` |
| `region` | `fetchUserInfo` 的 `regionCode` |
| `device` | 设备档案 |
| `auth_method` | 认证方式，见表下 |
| `wif_provider` | 企业 WIF 账户登录时的 `--provider`，个人账户省略 |
| `enabled` | 是否参与调度 |
| `priority` | 调度优先级，越大越先使用，为 0 时省略 |
| `ai_credits` | 额度用尽后继续以 Google One AI 积分生成，关闭时省略 |
| `credit_balance`、`credit_minimum` | `loadCodeAssist` 的 `paidTier.availableCredits` 中 `GOOGLE_ONE_AI` 项的余额（上游省略 `creditAmount` 时为 `"0"`）与 `minimumCreditAmountForUsage`，没有该项时省略；登录、冷启动与读取额度时更新 |
| `added_at` | 添加时间，RFC 3339 带纳秒 |

- `auth_method` 为空或 `personal_consumer` 时是个人账户，`business_licensed_enterprise` 为企业 WIF 账户
- `added_at` 为 Go `time.Time` 的 JSON；账户按它排序，相同时按 ID

设备档案在账户首次加入时生成，之后保持不变：`login` 与管理端登录的新账户沿用登录前虚拟进程的档案（`installation_id`、系统与时区，用户名与 `app_data_dir` 按邮箱补齐），`import` 与远程配对的新账户随机生成；已有账户再次登录、导入或配对时只更新 `token.json`，档案与其他资料不变。

| 字段 | 取值 |
| --- | --- |
| `os`、`arch` | 生成时运行平台的 `GOOS`、`GOARCH` |
| `user_name` | 邮箱本地部分中的字母与数字 |
| `app_data_dir` | 按系统的主目录路径，见表下 |
| `time_zone` | 本机 IANA 时区名 |
| `installation_id` | 随机 UUID |

- `app_data_dir`：Windows `C:\Users\<用户名>\.gemini\antigravity`，macOS `/Users/<用户名>/.gemini/antigravity`，其余 `/home/<用户名>/.gemini/antigravity`
- `time_zone` 取 `TZ` 或 `/etc/localtime` 链接，都没有时为 `±hh:mm`

档案字段的使用位置：

| 档案字段 | 使用位置 |
| --- | --- |
| `os`、`arch` | User-Agent、JSON 风格、unleash、Hub 事件、系统提示、主机名 |
| `user_name` | unleash `instanceId`；macOS 与 Linux 主机名 |
| `app_data_dir` | 系统提示中的路径 |
| `time_zone` | user 轮本地时间与 unleash 时间的时区 |
| `installation_id` | Windows 主机名；Hub 事件的安装标识；登录前错误上报 |

- `os`、`arch` 决定 User-Agent 的系统、架构与构建号，JSON 正文结构逗号后的空格（macOS 与 Linux 带空格），unleash 的 `platformVersion`，Hub 事件的 `os`，系统提示的 OS 与路径分隔符，以及主机名形式

主机名：Windows 为 `DESKTOP-` 加 `SHA-256(installation_id 的 UUID 文本)` 以 RFC 4648 标准 Base32 字母表 `A–Z2–7` 编码后的前 7 个字符，macOS 为 `<用户名>s-MacBook-Pro.local`，Linux 为 `<用户名>-pc`。

两个文件都是两个空格缩进、以换行结尾的 JSON，经同目录临时文件原子替换写入，目录权限 0700。

账户目录的读取：

| 情形 | 启动与子命令（`Open`） | 每 2 秒同步（`Sync`） |
| --- | --- | --- |
| 不是目录的项 | 跳过 | 跳过 |
| 子目录没有 `account.json` | 跳过 | 视为删除 |
| `account.json` 无法读取或解析 | 失败退出 | 视为删除 |
| `token.json` 缺失或无法解析 | 失败退出 | 保持内存中的账户 |
| `AUTH_DIR` 无法列出 | 失败退出 | 记 `读取账户目录失败` |

- 服务启动与每个读取账户的子命令（`accounts`、`login`、`import`、`status` 等）都以 `Open` 读取账户目录；失败时打印错误并以状态 1 退出
- 账户以 `account.json` 的 `id` 为键，与目录名无关；写回 `account.json` 与 `token.json` 时写到 `<AUTH_DIR>/<id>/`
- 多个目录的 `id` 相同时，按目录名排序靠后的生效
- `Sync` 只在 `token.json` 的 refresh token 与内存不同时换用目录中的资料与 token；目录中消失的账户从账户池移除

### 运行目录

| 路径 | 内容 |
| --- | --- |
| `requests.db` | 请求账本（SQLite，WAL） |
| `logs/antigravity2api.log` | 服务日志，轮转文件在同目录 |
| `logs/crash.log` | 未恢复的 panic 与致命错误 |
| `files/<ID>`、`files/<ID>.json` | Files API 上传的内容与元数据 |
| `media/<SHA-256>.<扩展名>` | `MEDIA_OUTPUT=url` 时按内容命名保存的生成媒体 |
| `conversations/<cascade_id>.json` | 会话记录，空闲 24 小时后删除 |
| `catalogs/<账户>.json` | 该账户最近一次读到的模型目录条目 |
| `hub/<账户>.json` | 虚拟 Hub 状态：该账户经服务开始过的会话数与模型选择器最后选定的模型 |
| `warmups.json` | 各账户各每周额度桶最近一次唤醒的时间 |
| `cooldowns.json` | 各账户未到期的冷却，服务重启后恢复 |
| `.updaterId` | 版本检查使用的更新器标识 |
| `webview/` | 桌面版 WebView 数据 |

运行目录中的 JSON 文件：

| 文件 | 字段 |
| --- | --- |
| `catalogs/<账户>.json` | 目录条目数组，见表下 |
| `hub/<账户>.json` | `{"conversations": <会话数>, "model": "<模型枚举>"}` |
| `warmups.json` | `{"<账户>": {"<额度桶 ID>": "<RFC 3339 时间>"}}` |
| `cooldowns.json` | `{"<账户>": {"<额度组或模型 ID>": "<RFC 3339 截止时间>"}}` |

- `catalogs/<账户>.json` 按公开目录的顺序，每项为 `fetchAvailableModels` 的条目字段加 `id`、`agent`（Agent 模型为 true，否则省略）与 `kind`（`image`、`transcription` 或 `tab`，普通条目省略，含义见协议参考“`fetchAvailableModels` 顶层字段”）
- `hub/<账户>.json` 在没有选定过模型时省略 `model`
- `cooldowns.json` 为紧凑 JSON，每次冷却变化时整体重写
- `catalogs/<账户>.json` 无法解析时跳过该账户保存的目录

会话记录 `conversations/<cascade_id>.json` 为紧凑 JSON 对象：

| 字段 | 类型 | 内容 |
| --- | --- | --- |
| `cascade`、`trajectory` | UUID 字符串 | 会话 ID 与轨迹 ID |
| `length` | 整数 | 最近一次请求在摘要链中的位置 |
| `digest` | 十六进制字符串 | 该位置的链摘要 |
| `answer` | 字符串，可省略 | 该次成功生成的回答指纹 |
| `times` | 整数数组 | 各条消息首次出现的 Unix 秒 |
| `prompt` | 对象，可省略 | 最近一次成功生成的提示规模 |
| `signatures` | 对象，可省略 | 函数调用 ID 到思考签名 |
| `origins` | 对象，可省略 | 签名指纹到模型族 |
| `used` | RFC 3339 字符串 | 最近使用时间，带纳秒 |

| `prompt` 字段 | 类型 | 内容 |
| --- | --- | --- |
| `model` | 字符串 | 模型 ID |
| `length` | 整数 | 规范消息条数 |
| `digest` | 十六进制字符串 | 该处链摘要 |
| `contents` | 整数 | 上游 contents 条数 |
| `tokens` | 整数 | 上游 `promptTokenCount` |

- `origins` 的键为签名 SHA-256 前 16 字节的小写十六进制，值为生成它的模型的 `modelProvider`（`MODEL_PROVIDER_GOOGLE`、`MODEL_PROVIDER_ANTHROPIC`、`MODEL_PROVIDER_OPENAI`）
- 会话记录写入时先写同目录临时文件再替换；启动时读入全部记录，任一文件无法解析时服务启动失败
- 写入时机：请求开始时（取得会话后、选择账户前）写 `length`、`digest`、`times` 与 `used`，`answer` 清空；`times` 先截到上一次的 `length` 再按当前时刻补齐本次多出的消息。生成正常结束后再写 `prompt`、`signatures`、`origins`，会话仍停在本次请求时写 `answer`
- 失败或取消的请求只有请求开始时的写入；之后原样重发时按协议参考「会话 ID」第 3 步匹配回同一会话，沿用已写的 `times`
- 摘要链：`digest(prev, data) = hex(SHA-256(prev + data))`，`prev` 为上一项的小写十六进制文本；`d0 = digest("", {"Session":<分区>,"System":<去掉计费行后的客户端系统提示>,"Tools":<规范工具>})`，`di = digest(d(i-1), 第 i 条规范消息的 JSON)`
- 回答指纹：`digest("", 正文 + NUL + 调用)`。正文为各非思考 part 的 `text` 相连后按 Go `strings.Fields` 切分、以单个空格相连；调用为各函数调用的“函数名 + 参数 JSON”以 NUL 相连，参数 JSON 先解析再编码，对象键按字典序
- 摘要链与回答指纹中的 JSON 为 Go `encoding/json` 编码：字段按下表顺序，标为可省略的字段为零值时不写，`[]byte` 为标准 Base64，`json.RawMessage` 去掉空白，字符串中的 `<`、`>`、`&` 写成 `\u003c`、`\u003e`、`\u0026`

| 规范类型（`internal/antigravity/types.go`） | JSON 字段，按顺序 | 可省略 |
| --- | --- | --- |
| `Content` | `role`（`user`、`assistant`、`tool`）、`parts` | — |
| `Part` | `text`、`inline_data`、`file`、`function_call`、`function_result`、`thought`、`thought_signature`、`video_metadata`、`media_resolution` | 全部 |
| `Blob` | `mime`、`data` | — |
| `FileRef` | `id`、`name`、`mime` | `name`、`mime` |
| `FunctionCall` | `id`、`name`、`arguments`、`thought_signature` | `thought_signature` |
| `FunctionResult` | `id`、`name`、`content`、`parts` | `name`、`parts` |
| `Tools` | `functions`、`google_search`、`search`、`tool_config` | 除 `tool_config` 外 |
| `FunctionDeclaration` | `name`、`description`、`parameters`、`schema`、`strict`、`response`、`response_json_schema`、`behavior` | 除 `name` 外 |
| `SearchOptions` | `context_size`、`location`、`allowed_domains`、`blocked_domains` | 全部 |
| `ToolConfig` | `mode`、`allowed_function_names`、`parallel_calls` | 全部 |

- `video_metadata` 与 `media_resolution` 为客户端 JSON 原文去掉空白
- `SearchOptions.location` 为字符串：城市、地区、国家以 `, ` 相连，有时区时接 `; time zone <时区>`
- `ToolConfig.mode` 为字符串 `auto`、`required`、`validated` 或 `none`，`allowed_function_names` 为字符串数组，`parallel_calls` 为布尔值，客户端没有给出时省略
- `FunctionDeclaration.parameters` 为投影后的上游 Schema（见协议参考“工具声明”，键序与编码同 `marshalSchema`）；`schema` 为整理后的客户端 JSON Schema：`null`、空、`true` 与 `{}` 换成 `{"type":"object"}`，`required` 列出而 `properties` 中没有的属性补为 `{}`；`description` 含投影时追加的完整 Schema 说明
- `google_search` 为客户端 `googleSearch` 的 JSON 原文，`search` 为 `SearchOptions` 对象

## 4. 账户生命周期与调度

### 添加账户

`login`、`import`、远程配对与管理端登录都把 OAuth token 交给账户库：邮箱取自 `id_token` 载荷，没有 `id_token`（官方客户端保存的 token）时以该 token 请求 userinfo；新账户生成设备档案（来源见“账户目录”）并启用，已有账户更新 token 并清除 `auth_method` 与 `wif_provider`，企业 WIF 登录随后写回这两项。`import` 先读系统凭据库（服务 `gemini`、用户 `antigravity`），读取失败时再读 `~/.gemini/jetski-standalone-oauth-token`，取 JSON 的 `token` 字段，导入这一个账户；凭据的 `auth_method` 不参与，导入的账户为个人账户。`POST /api/accounts/import` 的 `accounts` 因此只有一项。

账户 JSON 的导出与导入：

- 导出为数组，每项为 `email`、`refresh_token`、`label`、`proxy`、`priority`（为 0 时省略）、`enabled`、`ai_credits`（关闭时省略）与 `device`（设备档案），没有 token 的账户不导出；Antigravity Manager 按 `email` 与 `refresh_token` 导入同一文件
- 导入在任意层级中把带 `refresh_token`、`refreshToken`（`|` 之后的项目 ID 去掉）或 `token.refresh_token` 且以 `1//` 开头的对象各作为一个账户，`platform` 不是 `antigravity` 的对象跳过，同一 refresh token 只导入一次；整段内容不是合法 JSON 时取文本中全部 `1//` 开头的 token。覆盖的格式为 Antigravity Manager 的导出数组与 `accounts` 对象、CLIProxyAPI 与 gcli2api 的账户文件、opencode-antigravity-auth 与 antigravity-claude-proxy 的 `accounts.json`、sub2api 的数据导出
- 每个账户以 refresh token 换取 access token 并读取 userinfo 的邮箱后按上文写入账户库，同时最多 4 个；项中的 `device` 带 `installation_id`（本服务导出的文件）时新账户沿用该设备档案，并写回备注名、代理、优先级、启用状态与 AI 积分设置（省略的优先级与 AI 积分按 0 与关闭写回），其他来源的设置不写回

系统凭据库经 `zalando/go-keyring` v0.2.8 读写，与官方客户端使用同一位置：

| 系统 | 位置 | 值 |
| --- | --- | --- |
| Windows | 凭据管理器的普通凭据，目标名 `gemini:antigravity`，用户名 `antigravity` | UTF-8 JSON 文本 |
| macOS | 登录钥匙串的通用密码，服务 `gemini`，账户 `antigravity` | 写入为 `go-keyring-base64:` 加 JSON 的 Base64；读取另接受 `go-keyring-encoded:` 加十六进制与原文 |
| Linux | Secret Service 的 `login` 集合（没有时为默认集合），标签 `Password for 'antigravity' on 'gemini'`，属性 `service=gemini`、`username=antigravity` | JSON 文本 |

运行中的服务每 2 秒重新读取账户目录：其他进程（`login`、`import` 或复制进 `auth/` 的目录）加入的账户、refresh token 与内存不同的账户与被删除的账户目录反映到账户库，有变化时后台刷新账户池。

### 切换官方客户端账户

`switch <邮箱>` 与 `POST /api/accounts/{id}/switch` 把账户库中的账户交给本机官方客户端，方向与 `import` 相反，实现在 `internal/auth`（`official.go`、`official_windows.go`、`official_other.go`）：

1. 找到官方客户端，结束它的全部进程并等待退出（下表）
2. 把 token 写入官方客户端的凭据存储（下表）
3. 重新启动官方客户端，language_server 启动时读取新 token

| 步骤 | Windows | macOS | Linux |
| --- | --- | --- | --- |
| 找到客户端 | 注册表中 `antigravity` 协议的打开命令 | `/Applications/Antigravity.app` | PATH 中的 `antigravity` |
| 结束进程 | 安装目录内的全部进程 | `pkill -KILL -f <app>/` | `pkill -KILL -f <安装目录>/` |
| 等待退出 | 逐个等待进程结束 | 每 100 ms 执行 `pgrep -f` | 同 macOS |
| 记下参数 | 主进程的启动参数 | — | — |
| 启动 | 以记下的参数运行 | `open <app> --args` | 在 `setsid` 新会话中运行 |

- Windows 打开命令为 `HKEY_CLASSES_ROOT\antigravity\shell\open\command` 的默认值，可执行文件取其中引号内的路径，没有引号时取第一个空格前的部分；安装目录为它所在的目录
- Windows 结束映像路径位于安装目录内的全部进程，`TerminateProcess` 后逐个等待，无权结束的进程跳过；主进程为映像是该可执行文件、命令行不含 `--type=` 的进程
- Windows 启动时工作目录为安装目录，客户端原本没有运行时不带参数
- Linux 的可执行文件为 PATH 中 `antigravity` 解析符号链接后的文件，安装目录为它所在的目录，启动时工作目录为安装目录
- macOS 与 Linux 在 `pgrep -f` 查不到进程时视为已退出

写入的凭据为官方客户端登录后保存的 JSON `{"token":{"access_token","token_type","refresh_token","expiry"},"auth_method":"consumer"}`，位置取决于标记文件 `~/.gemini/cache/antigravity-keyring-unavailable`：

| 情形 | 写入 |
| --- | --- |
| 标记的修改时间在 1 小时内 | 令牌文件，系统凭据库已有可读凭据时一并更新 |
| 标记不存在或超过 1 小时 | 系统凭据库 |
| 系统凭据库写入失败 | 标记与令牌文件 |
| `agy` 会话文件存在 | 另写该文件 |

- 令牌文件为 `~/.gemini/jetski-standalone-oauth-token`；官方客户端优先读取系统凭据库
- 系统凭据库写入失败的情形如 Linux 没有 Secret Service，此时与官方客户端一样写入标记
- `agy` CLI 的会话文件为 `~/.gemini/antigravity-cli/antigravity-oauth-token`；`agy` 先读系统凭据库再读该文件，切换后新启动的 `agy` 以该账户运行
- 文件权限为 0600

### 初始化

服务启动后与账户变化后在后台刷新账户池，同一时间只运行一次，进行中再次请求时合并为下一次：

1. 按添加时间处理启用账户，跳过已初始化且代理与 refresh token 都未变的账户；相邻两个账户的初始化相隔随机 1–3 秒开始、彼此并发，全部结束后刷新结束
2. 每个账户以设备档案与代理创建上游客户端，读取 `RUNTIME_DIR/hub/<账户>.json`，立即开始 unleash 与 clearcut 上报两项伴随请求，0.6–0.8 秒后初始化
3. 企业账户（`auth_method` 为 `business_licensed_enterprise`）走 `BusinessInitialize`；个人账户在 `project` 为空或 refresh token 已变时走登录序列，其余走冷启动序列（序列见协议参考）。`fetchUserInfo` 显示遥测开启时发 `setUserSettings` 关闭
4. 成功时以目录的公开条目加入账户池，记录额度快照并开始其余伴随请求；失败时停止该账户的全部伴随请求，记下原因并移出调度
5. 停用与已删除的账户移出调度，停止全部伴随请求

伴随请求指虚拟进程在生成请求之外发出的上游请求，清单与时机见协议参考“伴随请求”。

上游读到的账户状态在每次成功读取后立即写回，初始化、刷新轮与额度读取相同，初始化中途失败时已读到的部分照常写回：

| 读取 | 写回 |
| --- | --- |
| `loadCodeAssist` | `account.json` 的 `project`、`tier`、`paid_tier`、`credit_balance`、`credit_minimum`，调度用的套餐等级 |
| `fetchUserInfo` | `account.json` 的 `region` |
| `fetchAvailableModels` | `RUNTIME_DIR/catalogs/<账户>.json`；读取的上游客户端正在调度该账户时另更新账户池中的条目 |
| `retrieveUserQuotaSummary` | 该账户最近一次读到的额度摘要 |

- 账户已删除时不写回，账户目录不会因删除前开始的初始化或读取重新出现

账户池与目录：

- 账户池顺序为各账户最近一次初始化成功的先后：初始化成功时账户先移出再加到末尾；`fill-first` 的首选账户与公开目录的合并都按此顺序
- 伴随请求的第 6、12、18… 轮实验轮询（虚拟进程启动后 6 分 0.7 秒起每 6 分钟）重读目录，更新账户池中的条目与保存的目录，账户位置不变
- 公开模型目录是账户池中各账户目录的合集，再加上尚未就绪的启用账户保存的目录，服务重启后账户初始化完成前即列出全部模型

额度读取：

| 时机 | 读取的账户 |
| --- | --- |
| 定时：服务启动 60 秒后，此后按 `QUOTA_REFRESH_INTERVAL` | 全部账户 |
| 管理端刷新全部或单个账户 | 所选账户 |
| 生成请求遇到额度耗尽（`QUOTA_EXHAUSTED` 或 `INSUFFICIENT_G1_CREDITS_BALANCE`，图像生成模型除外） | 该账户，后台读取 |
| 账户初始化后首次成功生成 | 该账户，不先发 `loadCodeAssist` |
| 周额度唤醒成功 | 该账户 |

- 定时读取由服务启动时开始的每分钟计时器驱动，从服务启动起计时，不对齐时钟整分
- 第 1 次在启动 60 秒后
- 之后每次计时器触发时，距上次读取的分钟数乘 1 分钟不小于 `QUOTA_REFRESH_INTERVAL` 时读取，间隔因此向上取整到整分钟
- 修改间隔在下一次计时器触发时生效；为 0 时不定时读取
- 每次定时读取后，依次重新初始化启用中但初始化失败、认证失败或需要验证的账户（相邻两次间隔 1–3 秒），再执行周额度唤醒
- 已初始化的账户经自己的上游客户端读取，其余账户（含停用账户）以保存的 token、设备档案与代理读取；除首次成功生成后的那次外，每次读取先发 `loadCodeAssist`（端点见协议参考“`retrieveUserQuotaSummary` 的发送时机”），读到的状态按“初始化”一节写回
- 同时最多读取 5 个账户，与账户池刷新依次进行
- 每个账户保留最近一次读到的额度摘要及读取时间：刷新账户池时，还没有读取的账户取请求账本中 7 天内最近一次额度快照；冷启动初始化不读额度，沿用最近一次读取；账户出错或移出调度后保留

周额度唤醒在 `WEEKLY_QUOTA_WARMUP` 开启时、每次定时读取额度之后执行，对满足下表全部条件的账户与额度桶各发一次请求：

| 条件 | 规则 |
| --- | --- |
| 账户 | 在账户池中 |
| 额度桶 | `window` 为 `weekly`，窗口未开始计时 |
| 间隔 | 该账户该桶 6 天内没有唤醒过 |
| 模型 | 该桶额度组中估算费用最低的对话模型 |

- 窗口未开始计时：最近一次读到的该桶剩余比例不低于 99.9%，且 `resetTime` 减读取时间不小于 7 天减 1 分钟；窗口未开始计时时 `resetTime` 随读取时间后移
- 额度组为桶 ID 中 `-` 之前的部分；对话模型为该账户目录中属于该组且 `kind` 为空的模型
- 费用按 1000 输入 token 与 1000 输出 token 以 `requestdb.Cost` 估算；没有单价的模型排在有单价的之后，同价取目录中靠前的；没有对话模型时跳过该桶
- 请求：经生成路径以 `AccountID` 指定该账户，发一条 user 消息 `Hi`，没有客户端系统提示与工具，使用当前的系统提示档位，读完回答；会话分区为空，会话记录空闲 24 小时即删除，因此每次唤醒都新建会话并发 `writeTrajectoryAcls` 与标题请求；唤醒请求不写请求账本
- 结果：成功时记入 `RUNTIME_DIR/warmups.json` 并重新读取该账户的额度；失败写 WARN 日志 `周额度唤醒失败`（见运行日志文档“账户事件”），下一次定时读取后再试

管理端“验证”同步重新初始化单个账户（企业账户走 `BusinessInitialize`，个人账户在 `project` 为空时走登录序列），需要验证或初始化失败的账户由此恢复调度。

生成类请求以外的上游请求失败时：

| 请求 | 失败时 |
| --- | --- |
| 初始化 | 记下原因并移出调度 |
| 刷新轮 | 只写 `upstream.request` 日志 |
| 额度读取 | 写 WARN 日志，列入 `failed` |

- 初始化失败的原因为错误文本；上游返回 403 `VALIDATION_REQUIRED` 时原因为 `需要验证：<上游错误正文>`，管理端从中取验证链接。这类账户在每次定时读取额度后与管理端“验证”时重新初始化
- 刷新轮为每 6 轮实验轮询附带的 `loadCodeAssist`、`fetchAvailableModels`、userinfo、头像与 `fetchUserInfo`；账户保持调度，读到的状态按上文写回
- 额度读取的 `loadCodeAssist` 或 `retrieveUserQuotaSummary` 失败时账户保持调度，额度沿用上一次读取

### 账户状态

| 状态 | 条件 |
| --- | --- |
| `disabled` | 已停用 |
| `error` | 初始化失败或被移出调度，带原因 |
| `pending` | 启用但尚未完成初始化 |
| `cooling` | 至少一个额度组或模型在冷却 |
| `ready` | 可调度 |

- 账户池为初始化成功且未移出调度的账户，含 `cooling` 状态的账户；本文与协议参考中的“就绪账户”都指账户池中的账户
- 按账户池计的位置：选择账户、`GET /api/models` 的 `accounts`、`GET /api/accounts` 的 `shared`、周额度唤醒；仪表盘“就绪数”只计 `ready`

`error` 状态的原因（`GET /api/accounts` 的 `error`）：

| 失败 | 原因文案 |
| --- | --- |
| 初始化失败 | 错误文本 |
| 需要验证 | `需要验证：<上游错误正文>` |
| 认证失败 | `认证失败：<错误文本>` |
| 违反条款 | `违反服务条款已停用：<上游错误正文>` |

- 违反条款时写回 `account.json` 失败，原因后追加 `；写回账户失败：<错误文本>`；该账户已停用，状态显示为 `disabled`
- 尚未完成初始化的账户没有原因；“模型只在未就绪账户的目录中”与模型列表（`账户尚未就绪，还没有模型目录`）的 503 把它写为 `初始化中`

冷却包括调度记下的冷却与额度耗尽：额度摘要中该组的桶或目录 `quotaInfo` 的 `remainingFraction` 不大于 `QUOTA_RESERVE_PERCENT` 对应的比例（默认 0）时，到 `resetTime` 前视为冷却。开启 AI 积分超额使用（`ai_credits`）的账户在 `QUOTA_RESERVE_PERCENT` 为 0 时不按剩余比例视为耗尽，由上游的额度耗尽错误决定冷却。

### 选择账户

1. 账户池为空时返回 503，不解析模型名；否则解析模型名（规则见协议参考“模型 ID 与思考档位”），名称只在未就绪账户保存的目录中时返回 503，都不存在时返回 404；消息见下表
2. 候选账户：目录中有该模型、未被本次请求排除、该模型未冷却；冷却截止取额度组冷却、模型冷却与额度耗尽重置时间中最晚者
3. 没有候选时：本次请求还没有上游失败则返回 429，`Retry-After` 为该模型最早的恢复时刻；已有上游失败则返回最近一次失败，该模型的账户都在冷却时带上其中最早的恢复时刻作为 `Retry-After`
4. 会话粘连：同一会话（会话记录的 `cascade_id`）一小时内沿用上次的账户，该账户在候选中且有空闲槽位时直接使用，否则按第 5、6 步改选；每次选中都把会话绑定到选中的账户，有效期为一小时；粘连每分钟清理一次过期项
5. 在有空闲槽位的候选中取 `priority` 最大的一组；模型在 `PREFER_LOWER_TIER_MODELS` 中（按目录 ID 或去掉 `-low`、`-medium`、`-high` 后缀的族 ID 匹配）时，再取其中套餐等级最低的一组。套餐等级按 `paid_tier`，没有时按 `tier`：ID 含 `ultra` 为 Ultra，含 `pro` 为 Pro，其余为 Free
6. `fill-first`：其中账户池顺序的第一个；`round-robin`：其中进行中请求最少者，并列时取最近一次被选中最早者（从未被选中的最先），再并列时取账户池顺序靠前者；“被选中”按选中时刻计，会话粘连与第 5、6 步的选中都算一次
7. 候选都没有空闲槽位时等待，直到槽位释放或账户池变化后重新选择；等待计入从生成开始的 `REQUEST_TIMEOUT`，超时返回 504 `等待上游超过 REQUEST_TIMEOUT`，客户端断开时停止等待

| 情形 | 状态码 | 消息 |
| --- | --- | --- |
| 账户池为空 | 503 | 首行 `没有就绪账户` |
| 模型只在未就绪账户的目录中 | 503 | 首行 `模型 <名称> 所在的账户尚未就绪` |
| 模型不存在 | 404 | `模型 <名称> 不在账户目录中` |
| 周额度唤醒的账户不在账户池 | 503 | `账户 <账户> 未就绪 <原因>` |

- 账户池为空时，首行之后每行一个 `<账户>: <原因>`，只列出有原因的账户（含因违反条款停用的账户），尚未完成初始化的账户不在其中，按行的字典序排列
- 模型只在未就绪账户的目录中时，首行之后每行一个 `<账户>: <原因>`，列出目录含该模型的已启用未就绪账户，按添加时间排列，没有原因时写 `初始化中`

图像、语音转写与 Tab 补全请求按同样的步骤选择账户，“目录中有该模型”指账户的公开目录（含 `imageGenerationModelIds`、`audioTranscriptionModelIds` 的条目与 Tab 补全条目）中有该 ID；Tab 补全不按会话粘连。

单账户并发上限为 `ACCOUNT_CONCURRENCY`，调度策略为 `ROUTING_STRATEGY`，额度保留比例为 `QUOTA_RESERVE_PERCENT`，每个请求读取当前值。开启 AI 积分超额使用的账户发出的 agent 请求，信封带 `enabledCreditTypes: ["GOOGLE_ONE_AI"]`（见协议参考）。

### 失败与换号

生成从开始到首个上游事件、相邻两个上游事件之间各受 `REQUEST_TIMEOUT` 限制；超时时取消上游，首个事件前返回 504，流开始后以超时错误事件结束。

- 401：上游返回 401 时丢弃 access token，在同一账户上重发一次；token 端点拒绝刷新时不重发
- 处理顺序：仍失败时按下表的原因处理账户，再决定是否换账户
- 换号上限：一个请求依次尝试可调度的账户，每个账户一次，最多 12 个；本次失败为没有恢复时刻的限速 429 时最多 2 个
- 结束：失败不换账户、达到换号上限或候选用尽时返回最近一次失败（候选用尽见“选择账户”第 3 步），超过 `REQUEST_TIMEOUT` 时返回 504
- 流中错误：上游流以错误中断时，流式请求在客户端收到首个事件前、非流式请求在生成结束前（非流式请求的事件在生成结束后才写出）同样按原因换账户重新生成
- 已写出事件：客户端已收到事件后的错误只处理账户，不换号

| 失败 | 判定 | 账户处理 | 换账户 |
| --- | --- | --- | --- |
| 认证失败 | token 端点以 4xx 拒绝刷新，或重发后仍为 401 | 移出调度 | 是 |
| 需要验证 | 403，`VALIDATION_REQUIRED` 或消息含 `verify your account` | 移出调度，保持启用 | 是 |
| 违反条款 | 403，`TOS_VIOLATION` 或消息含 `terms of service` | 写为停用 | 是 |
| 没有许可 | 403，`SUBSCRIPTION_REQUIRED` 或消息含 `license`、`subscription` | 该模型冷却 1 小时 | 是 |
| 额度耗尽 | `QUOTA_EXHAUSTED`、`INSUFFICIENT_G1_CREDITS_BALANCE` | 额度组冷却到恢复时刻 | 是 |
| 容量不足 | `MODEL_CAPACITY_EXHAUSTED` | 不冷却 | 是 |
| 请求内容 | 429，没有 ErrorInfo 与恢复时刻 | 不冷却 | 否 |
| 限速 | 其他 429 | 该模型冷却到恢复时刻，没有时 1 分钟 | 是 |
| 内部错误 | 500 `INTERNAL` | 该模型冷却 n 分钟，n 为连续次数 | 是 |
| 连接错误 | 没有 Cloud Code 的 HTTP 错误响应 | 不冷却，请求检测代理池 | 是 |
| 已下线模型 | 下线提示，见协议参考“已下线与不存在的模型 ID” | 不冷却 | 否，返回 404 |
| 输入过滤 | 过滤提示，见协议参考“输入过滤” | 不冷却 | 否，返回 400 |
| 其他 | 其余上游错误 | 不冷却 | 否 |

- 额度耗尽：同一共享额度池的账户一起冷却，后台读取该账户额度；图像生成模型只冷却该账户的该模型
- 连接错误：DNS、TCP、TLS 与代理失败，token 端点的 5xx 与 429，语音转写的 gRPC 错误；客户端未取消时才请求检测代理池。请求账本中这类尝试的状态码为 0，错误阶段为 `network`
- 恢复时刻依次取 RetryInfo `retryDelay`、ErrorInfo `metadata.quotaResetDelay` 与 `quotaResetTimeStamp`；额度耗尽都没有时取目录 `quotaInfo.resetTime`，再没有时 1 分钟
- 连续 INTERNAL 次数在该账户下一次成功生成后清零
- 限速的恢复时刻在 5 秒内时，请求等到恢复时刻后再次使用该账户，同一请求在每个账户上只等一次，不计入 12 个账户的上限
- 移出调度的原因显示在管理端；需要验证时正文中的 `validation_url` 作为验证链接
- 图像、语音转写与 Tab 补全请求按同一张表处理失败；上游请求成功返回后流中出现的错误不换账户

### 共享额度池

同一 Google 家庭方案的成员账户共享同一组额度桶，本文称为共享额度池。两个就绪账户在某额度组（`gemini` 或 `3p`）属于同一共享额度池，条件是两者最近一次读到的额度摘要中该组的窗口集合相同，且每个窗口的 `resetTime` 文本完全相同；判定随时按最近一次读取进行，与是否耗尽无关。

| 使用位置 | 规则 |
| --- | --- |
| 额度耗尽 | 某账户额度组耗尽时，同一共享额度池的其他账户一起冷却到同一时刻 |
| `GET /api/accounts` 的 `shared` | `{"gemini":["<邮箱>"],"3p":["<邮箱>"]}` |
| 账户页 | 有 `shared` 时显示家庭组徽标（账户表中只有图标，详情中带“家庭组”文字），悬停列出各组成员 |
| 仪表盘额度均值、托盘额度 | 同一共享额度池只计一次 |

- `shared` 按账户池顺序列出同一池的其他成员，没有成员的组省略，两组都没有时省略 `shared`

## 5. 管理 API 与管理端

管理 API 挂在 `/api/`，与公开 API 共用监听：

- 只接受同源请求：`Sec-Fetch-Site: cross-site` 或 `Origin` 与 `Host` 不一致时返回 403 `admin_origin_forbidden`
- 本机回环访问免登录：对端地址是回环 IP，`Host` 的主机部分为 `localhost`（不区分大小写）或回环 IP（如 `localhost:8017`、`127.0.0.1:8017`、`[::1]:8017`），且没有 `X-Forwarded-For`、`Forwarded`、`X-Real-IP`
- 远程访问需要 `ADMIN_AUTH_ENABLED=true`，否则返回 401 `admin_remote_disabled`；未登录返回 401 `admin_login_required`
- 会话 Cookie `antigravity_admin`：12 小时，`Path=/api`、HttpOnly、SameSite=Strict，经 TLS 或 `X-Forwarded-Proto: https` 时带 Secure；会话结束时取消该会话的进行中管理请求
- 同一客户端 IP 一分钟内登录失败 5 次后返回 429 `login_rate_limited`，`Retry-After: 60`
- 响应带 `Cache-Control: no-store`；错误体为 `{"error":{"code","message"}}`；未知路由返回 404 `not_found`，消息为 `管理 API 不存在: <方法> <路径>`

| 方法与路径 | 作用 |
| --- | --- |
| `GET /api/session` | 是否开启登录、当前请求是否可访问 |
| `POST /api/session` | 用户名与密码登录 |
| `DELETE /api/session` | 退出登录 |
| `GET /api/status` | 宿主、版本、监听地址、启动时间、模拟的客户端版本、官方最新版本 |
| `GET /api/update`、`POST /api/update` | 本程序的最新发行版本与在线更新，见第 6 节 |
| `GET /api/proxies` | 代理池中每个代理的地址（密码替换为 `xxxxx`）、是否可用、检测时间、延迟、错误与绑定的账户 |
| `POST /api/proxies/check` | 立即检测代理池，返回检测后的状态 |
| `GET /api/accounts` | 全部账户 |
| `PATCH /api/accounts/{id}` | 启用、停用，设置账户代理、优先级、备注名或 AI 积分超额使用 |
| `DELETE /api/accounts/{id}` | 删除账户目录 |
| `POST /api/accounts/{id}/verify` | 重新初始化 `error` 状态的账户 |
| `POST /api/accounts/import` | 导入本机官方客户端账户 |
| `POST /api/accounts/import/json` | 导入账户 JSON 或含 refresh token 的文本 |
| `GET /api/accounts/export` | 下载账户 JSON |
| `POST /api/accounts/{id}/switch` | 把账户写入本机官方客户端并重新启动它 |
| `POST /api/accounts/{id}/quota` | 读取该账户的额度并返回账户 |
| `POST /api/accounts/refresh` | 同步刷新账户池并读取全部账户的额度 |
| `POST /api/logins` | 开始登录，`mode` 为 `browser` 或 `code` |
| `POST /api/logins/{id}/code` | 提交授权码或回调地址，等到登录结束 |
| `DELETE /api/logins/{id}` | 取消登录 |
| `POST /api/pairing` | 生成远程配对令牌 |
| `POST /api/pairing/accounts` | 凭配对令牌导入 token，不经管理会话 |
| `GET /api/models` | 公开模型目录与各模型的就绪账户数 |
| `GET /api/quota/snapshots` | 范围内的额度快照 |
| `GET /api/usage` | 用量报告 |
| `GET /api/usage/records` | 一页请求记录 |
| `GET /api/usage/records.csv` | 导出请求记录 |
| `GET /api/usage/records/{id}` | 请求记录、公开正文与上游尝试正文 |
| `GET /api/requests` | 进行中的公开请求 |
| `POST /api/requests/{id}/cancel` | 停止进行中的请求 |
| `GET /api/logs` | 一页已入库的 WARN 以上日志 |
| `DELETE /api/logs` | 清空日志 |
| `GET /api/prompt/preview` | 系统提示档位的原文预览 |
| `GET /api/settings` | `.env` 中保存的配置 |
| `PATCH /api/settings` | 合并保存配置 |
| `GET /api/events` | 管理事件流（SSE） |

各接口的请求体与响应：

| 接口 | 请求体 | 成功响应 | 错误 |
| --- | --- | --- | --- |
| `GET /api/session` | — | `{"enabled","authenticated"}`：是否开启登录、当前请求是否可访问 | — |
| `POST /api/session` | `{"username","password"}` | `{"enabled":true,"authenticated":true}` 并设置 Cookie | 400 `invalid_request`、`admin_auth_disabled`，401 `invalid_credentials`，429 `login_rate_limited` |
| `DELETE /api/session` | — | 204 并清除 Cookie | — |
| `GET /api/status` | — | `{"host","version","listen","reserved_listen","started_at","client_version","latest_version"}`，`listen` 为实际监听地址，`reserved_listen` 没有改用端口时为空串，`latest_version` 未读到时为空串 | — |
| `GET /api/update` | — | `{"current","latest","available","mode","notes","url","published_at","checked_at","error","updating"}`，空值字段省略 | 502 `update_check_failed`，见“在线更新” |
| `POST /api/update` | — | 202，同上 | 409 `update_conflict`，502 `update_failed` |
| `GET /api/proxies`、`POST /api/proxies/check` | — | `{"proxies":[{"url","healthy","checked_at","latency_ms","error","accounts"}]}` | — |
| `GET /api/accounts` | — | `{"accounts":[账户]}` | — |
| `PATCH /api/accounts/{id}` | 见下方 | 账户 | 400 `invalid_request`、`invalid_proxy`，404 `account_not_found`，500 `account_update_failed` |
| `DELETE /api/accounts/{id}` | — | 204 | 404 `account_not_found`，500 `account_delete_failed` |
| `POST /api/accounts/{id}/verify` | — | 账户 | 404 `account_not_found`，409 `account_not_verifiable`，502 `account_verify_failed` |
| `POST /api/accounts/import` | — | `{"accounts":["<邮箱>"]}` | 502 `import_failed` |
| `POST /api/accounts/import/json` | 文件内容，至多 16 MiB | `{"accounts":["<邮箱>"],"failed":[{"account","error"}]}` | 400 `invalid_request`、`no_accounts` |
| `GET /api/accounts/export` | 查询参数 `id`，可重复，省略时为全部账户 | 账户 JSON 数组，`Content-Disposition` 文件名 `antigravity2api-accounts-<YYYYMMDD>.json` | — |
| `POST /api/accounts/{id}/switch` | — | `{"account":"<邮箱>"}` | 404 `account_not_found`，502 `switch_failed` |
| `POST /api/accounts/{id}/quota` | — | 账户 | 404 `account_not_found`，502 `quota_refresh_failed` |
| `POST /api/accounts/refresh` | — | `{"accounts":[账户],"failed":{"<邮箱>":"<原因>"}}` | — |
| `POST /api/logins` | `{"mode":"browser"或"code"}` | 201 登录状态 | 400 `invalid_request`，502 `login_failed` |
| `POST /api/logins/{id}/code` | `{"code":"<授权码或回调地址>"}` | 登录结束后的登录状态 | 400 `invalid_request`、`login_callback_rejected`，404 `login_not_found`，409 `login_code_submitted` |
| `DELETE /api/logins/{id}` | — | 204 | 404 `login_not_found` |
| `POST /api/pairing` | — | 201 `{"token","expires_at"}` | — |
| `POST /api/pairing/accounts` | `oauth2.Token` JSON，`Authorization: Bearer <令牌>` | 201 `{"account":"<邮箱>"}` | 400 `invalid_request`，401 `pairing_token_invalid`，502 `pairing_import_failed` |
| `GET /api/models` | — | `{"models":[公开模型对象加 "accounts"]}` | 502 `models_failed` |
| `GET /api/requests` | — | `{"requests":[请求记录]}`，按开始时间升序 | — |
| `POST /api/requests/{id}/cancel` | — | 204 | 404 `request_not_found` |
| `DELETE /api/logs` | — | 204 | 500 `logs_clear_failed` |
| `GET /api/settings`、`PATCH /api/settings` | 见“设置与系统提示预览” | 设置对象 | 400 `invalid_request`、`invalid_settings`，500 `settings_unreadable`、`settings_save_failed`、`settings_apply_failed` |

- 请求体不是合法 JSON 时返回 400 `invalid_request`
- `switch` 在找不到官方客户端、无法结束它或写入凭据失败时返回 502 `switch_failed`
- `POST /api/logins` 的 `mode` 为 `browser` 时等待回环回调，其余值都按 `code` 模式运行，登录状态的 `mode` 为提交的原值

- 登录状态为 `{"id","mode","url","state","account","error","expires_at"}`：`state` 为 `pending`、`completed`、`failed` 或 `canceled`，`account` 只在 `completed` 时出现，`error` 只在 `failed` 与 `canceled` 时出现；事件流的 `login` 事件携带同一对象
- 请求记录与用量的字段见 [运行日志与请求账本](logging.md)；进行中请求的 `state` 为 `running`，结束后的取值见其中的“请求结果”；请求结果写作 `cancelled`，登录状态写作 `canceled`
- 进行中请求的记录：`time` 为开始时间，`duration_ms` 为截至读取时的已用时间，`account` 为最近一次分配的账户，尚未分配时为空串，其余字段为截至读取时的值
- 公开模型对象为 `antigravity.Model` 的 JSON：`id`、`name`、`description`、`methods`、`input_token_limit`、`output_token_limit`、`capabilities`、`provider`、`quota_group`、`thinking_levels`、`default_thinking_level`，空值省略
- `accounts` 为就绪账户中目录含该模型的账户数：族条目按族内任一档位计，每个账户计一次；别名取目标模型的值

同一监听上的其他入口：`/v1/`、`/v1beta/`、`/upload/` 为公开 API（未知路径见第 2 节“api 层的公开接口规则”），`/media/` 为生成媒体（不校验 API key），`POST /api/pairing/accounts` 为远程配对上传；其余路径由管理端静态资源处理：

| 请求 | 响应 |
| --- | --- |
| `GET`、`HEAD` 以外的方法 | 405，`Allow: GET, HEAD` |
| `assets/` 下存在的文件 | 该文件，`Cache-Control: public, max-age=31536000, immutable` |
| 其他存在的文件 | 该文件，`Cache-Control: no-cache` |
| `assets/` 下不存在的文件 | 404 |
| `/`、`/index.html` 与其他路径 | 200 入口页 `index.html`，`Cache-Control: no-cache` |

同一监听上的全部响应带 `X-Frame-Options: DENY`、`Content-Security-Policy: frame-ancestors 'none'`、`X-Content-Type-Options: nosniff` 与 `Referrer-Policy: no-referrer`。

### 账户与登录

`GET /api/accounts` 的每个账户：

| 字段 | 内容 |
| --- | --- |
| `id` | 邮箱 |
| `label` | 备注名，没有时省略 |
| `state` | 见“账户状态” |
| `tier` | 有付费套餐时为 `paid_tier`，否则 `tier` |
| `project` | 上游项目 |
| `proxy` | 账户代理，密码替换为 `xxxxx` |
| `added_at`、`token_expiry` | 添加时间、access token 到期时间 |
| `priority` | 调度优先级 |
| `ai_credits` | 是否开启 AI 积分超额使用 |
| `credit_balance`、`credit_minimum` | Google One AI 积分余额与以积分生成所需的最低余额（数字），付费套餐没有该积分时省略 |
| `active` | 进行中请求数 |
| `error`、`verify_url` | 停止调度的原因、验证链接 |
| `cooldown` | 冷却中的额度组或模型 ID 到截止时间 |
| `quota`、`quota_at` | 最近一次读到的额度桶（`bucket_id`、`window`、`remaining_fraction`、`reset_time`）及读取时间，尚未读取时 `quota` 为空数组、省略 `quota_at` |
| `shared` | 按额度组列出同一共享额度池的其他就绪账户，形状见“共享额度池” |
| `device` | 设备档案各字段，加上由档案得出的 `hostname` 与 `user_agent` |

`PATCH /api/accounts/{id}` 接受 `{"enabled": bool, "proxy": "<URL>", "priority": int, "label": "<备注名>", "ai_credits": bool}` 中的任意几项，`proxy` 去掉首尾空白后为空串时跟随全局，为 `direct` 时直连，其余须为带主机的 `http`、`https` 或 `socks5` URL，否则返回 400 `invalid_proxy`；`label` 去掉首尾空白后保存，为空时清除；保存后后台刷新账户池，代理变化的账户重新初始化。

管理端登录：

1. `POST /api/logins` 在后台开始一次 OAuth 登录，ID 为小写随机文本，限时 5 分钟；授权期间以新生成的设备档案运行登录前虚拟进程
2. 取得授权链接后返回 201 `{id, mode, url, state: "pending", expires_at}`，并推送 `login` 事件
3. `code` 模式：把页面给出的授权码或带 `code` 参数的地址提交给 `POST /api/logins/{id}/code`；已提交过时返回 409 `login_code_submitted`
4. `browser` 模式：服务在本机回环端口等待回调（地址取授权链接的 `redirect_uri`）；浏览器不在同一台机器时，把地址栏中的回调地址提交给 `/code`，服务把其中的查询参数转交给回环监听；回环监听返回 302 以外的状态时返回 400 `login_callback_rejected`，消息为 `回调地址被拒绝：` 加回环监听的应答正文（`state mismatch` 或 `OAuth error: <error>`）。回调带 `error`（在授权页拒绝）而 `state` 正确时回环监听返回 400，登录以 `OAuth error: <error>` 结束为 `failed`（见协议参考「回环回调」）
5. `/code` 等到登录结束后返回登录状态；`DELETE /api/logins/{id}` 取消登录
6. 结束时推送 `login` 事件：成功为 `completed`（带 `account`），随后后台刷新账户池

| 结束情形 | `state` | `error` |
| --- | --- | --- |
| 账户已写入账户目录 | `completed` | — |
| 取消或服务退出 | `canceled` | `登录已取消` |
| 超过 5 分钟 | `failed` | `5 分钟内没有完成 Google 登录` |
| 其他错误 | `failed` | 错误文本 |

远程配对：`POST /api/pairing` 生成 10 分钟有效的令牌，同时只有一个有效令牌，服务只保存其 SHA-256。`login --remote <地址> --token <令牌>`：

- 本机：与 `login` 相同地运行登录前虚拟进程并完成浏览器或授权码登录，拿到 token 后不写本机 `auth/`
- 上传：把 `oauth2.Token` 的 JSON（`access_token`、`token_type`、`refresh_token`、`expiry`，不含 `id_token`）以 `Authorization: Bearer <令牌>` POST 到 `<地址>/api/pairing/accounts`，经进程环境变量的代理发出；返回 201 以外时打印 `远程服务返回 <状态>: <正文>` 并失败
- 远程：以 userinfo 取得邮箱，新账户随机生成设备档案，已有账户只更新 token；成功返回 201 `{"account": "<邮箱>"}` 并使令牌失效，本机打印 `已添加账户 <邮箱> 到 <地址>`

### 设置与系统提示预览

`GET /api/settings` 返回设置对象：

| 字段 | 内容 |
| --- | --- |
| `values` | `.env` 叠加进程环境变量后的各项，不含 `ADMIN_PASSWORD` |
| `admin_password_set` | 是否已设置管理密码 |
| `restart_required` | 需要重启才生效的项 |

- `values` 不叠加启动参数，各值按写回 `.env` 的格式写成文本（见“配置”）
- `restart_required` 列出该配置叠加启动参数后与启动值不同的 `LISTEN`、`AUTH_DIR`、`RUNTIME_DIR`

`PATCH /api/settings` 接受 `{"values": {键: 文本值}}`：

- 只合并提交的键，校验失败返回 400 `invalid_settings`
- 提交 `ADMIN_PASSWORD` 时以提交值（含空串）替换密码，不提交时保持原值；管理端只在密码输入框非空时提交该键

`GET /api/prompt/preview?tier=&sections=&wrap=` 以占位数据渲染档位，返回 `{system, user, sections}`：

| 字段 | 内容 |
| --- | --- |
| `system` | 系统指令 |
| `user` | 首个 user 轮 `<user message>` 包装后的文本 |
| `sections` | `[{"name","text"}]`，按官方顺序列出 11 个段 |

| 占位项 | 值 |
| --- | --- |
| 客户端系统提示 | `<client system prompt>` |
| 会话 ID | `<conversation-id>` |
| `AppDataDir` | `C:\Users\user\.gemini\antigravity` |
| 系统 | `windows` |
| 模型名 | `<model>` |
| 本地时间 | `2026-10-15T14:00:00+08:00` |
| 推荐插件货架正文 | `<featured plugins>` 加 `\n\n` |

- `sections` 的 `text` 为以同一占位数据渲染的段原文
- 省略的参数取当前配置；`sections` 与 `wrap` 只用于 `custom`
- 参数无效时返回 400 `invalid_prompt`

### 用量与记录查询

| 参数 | 用于 | 含义 |
| --- | --- | --- |
| `from`、`to` | 全部查询 | RFC 3339 时间范围，含 `from` 不含 `to`，必填 |
| `model`、`account`、`api_key`、`protocol`、`state`、`quota_group` | 用量、记录 | 逗号分隔的筛选值，同一维度多值为或，维度之间为且 |
| `bucket` | 用量 | `auto` 或 60、300、900、3600、86400 秒 |
| `tz` | 用量 | 分桶对齐的 IANA 时区，默认 UTC |
| `stack` | 用量 | 堆叠维度，取筛选维度之一，默认 `model` |
| `status`、`q` | 记录 | HTTP 状态码、关键字 |
| `limit`、`cursor` | 记录 | 页大小（正整数，默认 50）、上一页的 `next_cursor` |
| `level`、`source`、`request_id`、`model`、`q`、`limit`、`before` | 日志 | 见下方日志参数 |
| `account` | 额度快照 | 账户 |

`bucket=auto` 时按范围选择：2 小时内 1 分钟，12 小时内 5 分钟，8 天内 1 小时，更长 1 天；分桶数超过 1500 时返回 400。报告与记录的字段见 [运行日志与请求账本](logging.md)。

| 接口 | 成功响应 | 账本读取失败 |
| --- | --- | --- |
| `GET /api/usage` | 用量报告对象 | 500 `usage_failed` |
| `GET /api/usage/records` | `{items, next_cursor}` | 500 `records_failed` |
| `GET /api/usage/records.csv` | CSV 附件 | 响应在出错处结束，写 ERROR 日志 |
| `GET /api/usage/records/{id}` | `{record, body, upstream}` | 500 `record_failed` |
| `GET /api/quota/snapshots` | `{snapshots}` | 500 `quota_snapshots_failed` |
| `GET /api/logs` | `{items, next_before}` | 500 `logs_failed` |

- `next_cursor` 只在还有更早的记录时出现，原样作为下一页的 `cursor`；`next_before` 只在还有更早的日志时出现，作为下一页的 `before`
- 列表为空时 `items` 与 `snapshots` 为 `[]`；报告、记录与额度快照中的时间及日志条目的 `time` 为 UTC 的 RFC 3339 文本
- 参数无效返回 400 `invalid_request`，`message` 说明原因：`from` 或 `to` 不是 RFC 3339、`tz` 不是 IANA 时区名、`bucket` 不在可选值中、分桶数超过 1500、`stack` 不是筛选维度、`status` 或 `before` 不是整数、`limit` 不是正整数、`cursor` 无效
- 记录不存在返回 404 `record_not_found`

日志参数：

| 参数 | 匹配 |
| --- | --- |
| `source`、`request_id`、`model` | 单个值，完全相等 |
| `level` | `error` 只返回 ERROR，其他值返回全部 |
| `q` | 消息与属性文本的子串 |
| `limit` | 页大小，默认 100 |
| `before` | 序号小于该值 |

- `level` 不区分大小写；已入库的日志只有 WARN 与 ERROR，`level` 为 `error` 以外的值或省略时两者都返回
- `q` 去掉首尾空白后匹配，ASCII 字母不区分大小写，`%`、`_`、`\` 按字面匹配
- `before` 省略或不大于 0 时从最新一条开始；日志按序号从新到旧排列

### 管理端页面

| 页面 | 内容 |
| --- | --- |
| 仪表盘 | 首次使用三步引导、概览条、推荐账户、最近请求、额度变化 |
| 账户 | 添加、刷新、筛选、搜索、排序、分页、额度与重置倒计时、刷新单个账户额度、家庭组徽标（共享额度池）、验证、启停、批量操作、详情、调度优先级、备注名、AI 积分超额使用、设备档案、切换官方客户端、代理、删除 |
| 接入 | OpenAI、Anthropic 与 Gemini 兼容接口的 base URL（OpenAI 兼容含 Chat 与 Responses 两个协议）、API key 管理、客户端配置片段 |
| 模型 | 额度组、上下文与输出上限、思考档位、可用账户数、模型映射（可编辑） |
| 请求 | 进行中请求（可停止）、请求记录、用量、日志 |
| 设置 | 服务、网络、系统提示（各系统提示档位的原文，`custom` 的段落与包装）、调度（含优先用低套餐账户的模型、额度保留比例与周额度唤醒）、日志、远程访问、关于 |

各页面的组成：

| 页面 | 区块 | 内容 |
| --- | --- | --- |
| 账户 | 账户表 | 列为账户、状态、Gemini、Claude 与 GPT、操作，各行等高 |
| 账户 | 账户详情 | 额度、调度设置、用量、资料、设备档案与操作 |
| 模型 | 模型映射卡片 | 逐行编辑别名与目标模型 |
| 仪表盘 | 概览条 | 就绪账户与账户状态分布、两组额度的平均剩余比例与低额度账户数、24 小时请求 |
| 仪表盘 | 最近请求 | 近 24 小时最近 5 条请求 |
| 账户 | 搜索与分页 | 匹配邮箱、备注名与项目，每页 20 个 |
| 模型 | 搜索 | 匹配模型 ID 与显示名 |
| 请求 | 进行中 | 进行中请求与“停止” |
| 请求 | 记录表 | 每条请求一行 |
| 请求 | 记录详情 | 单条记录与各上游尝试 |
| 用量 | 筛选 | 时间范围；模型、账户与 API key 多选 |
| 用量 | 统计条 | 请求、缓存命中率、token、耗时、费用 |
| 用量 | 趋势与分项 | 用量趋势图与分项表 |

- 账户详情：额度与刷新、调度优先级、备注名、AI 积分超额使用、近 30 天用量、资料（套餐、AI 积分余额与使用积分所需的最低余额、项目、代理、添加时间、access token 到期）、设备档案；操作为重新初始化账户、代理、参与调度、删除、切换到官方客户端
- 模型映射卡片：目标模型的下拉为目录中的模型，可增删行；保存时以 `PATCH /api/settings` 只提交 `MODEL_MAPPING`；别名须非空、不含逗号、不重复，目标模型必选
- 进行中：只在有进行中请求时显示，数据来自事件流；列为时间与协议、模型、账户、已用时、“停止”（`POST /api/requests/{id}/cancel`）
- 记录表的列为时间、结果、状态码、模型、账户、首字、耗时、输入·缓存·输出、估算费用
- 记录详情：失败阶段、上游状态码、协议与路径、请求模型、上游模型、账户、排队·首字·总耗时、token、估算费用、工具调用、流式、请求类型与系统提示档位、会话与步序、API key、客户端、响应 ID、客户端请求 ID、各上游尝试（账户、状态、耗时、错误、重试等待、traceId）与正文
- 概览条：就绪账户为 `state` 为 `ready` 的账户数与账户总数，下方是按状态着色的分布条与各非就绪状态的数量（点击转到账户页该状态）；额度为各已启用账户的平均剩余比例（共享额度池只计一次），每组另有 5 小时与每周两条比例条，下方为任一额度桶低于 20% 的已启用账户数；24 小时请求为请求数、失败数与估算费用，下方逐小时柱状图（有失败的小时为红色）填满格内剩余高度
- 用量筛选：候选为报告 `options` 中这段时间出现过的非空取值，选中的取值以逗号连接写入 URL 的 `model`、`account`、`api_key`，同样带在 `GET /api/usage` 与导出 CSV 的查询参数中；有筛选时显示“清除筛选”
- 统计条的耗时为 p50、p95 与首字；分项表的列为请求数、占比、失败、限流、缓存、思考、回复、p50、p95、费用
- 界面上的“首字”为请求记录的首事件耗时 `first_event_ms`：请求进入到首个正文、思考或工具调用事件的毫秒数，见运行日志文档
- 仪表盘最近请求：读取 `GET /api/usage/records?from=<当前时间减 24 小时>&to=<当前时间加 1 秒>&limit=5`；每行为结果徽标、模型、账户（为空时 `—`）、完成时间与总耗时，点击打开 `/requests?id=<ID>`；没有记录时显示链接到接入页的提示；收到请求结束的 `request` 事件后重新读取
- 缓存命中率按 token 计，为 `cached_tokens / input_tokens`，没有输入 token 时为 0；统计条取范围合计，分项表按各行计算
- 账户用量：账户详情的“近 30 天用量”、账户表的“最近使用”与按最近使用排序都读取 `GET /api/usage?from=<当前时间减 30 天>&to=<当前时间加 1 秒>&tz=<浏览器时区>&bucket=auto`，取 `groups.account` 中该账户的一项；详情显示 `requests`、`total_tokens` 与 `cost_usd`，标题旁显示 `last_at` 的相对时间，没有该项时显示“30 天内未使用”
- 账户页与模型页的 `q` 去掉首尾空白后不区分大小写地按子串匹配：账户页匹配邮箱（`id`）、备注名与项目，模型页匹配模型 ID 与显示名
- 账户页每页 20 个账户，`page` 小于 1 时为第 1 页，超过总页数时为最后一页
- 账户表各行等高：账户列为邮箱与其下的标签区，标签区在窗口宽度 1280 以下分三行（备注名与套餐；家庭组、优先级与 AI 积分的图标标签；“最近使用”与代理），1280 及以上为一行；状态列为状态标签与异常账户的“重新初始化”图标按钮一行，其下为至多两行的原因，待验证账户的原因为“打开验证页面”链接；异常账户的原因为上游错误冒号后的说明（`upstream <状态码> <status> <reason>: <说明>`），悬停显示原文，点击打开详情；每个额度组固定两个额度桶的高度；截断的文字与图标标签的全文在提示中
- 窗口宽度小于 640 时每个账户为四行的卡片：邮箱；状态标签、套餐、AI 积分与一行原因；Gemini 与 Claude 和 GPT 各一行，只显示两个额度桶的剩余百分比；参与调度开关与更多操作在邮箱行右侧。备注名、家庭组、优先级、代理、最近使用、重置倒计时与切换官方客户端在账户详情中
- AI 积分：开启超额使用的账户行显示 AI 积分标签与积分余额，提示为余额与使用积分所需的最低余额；开启超额使用且 `credit_balance` 低于 `credit_minimum` 时，标签、详情中的余额与超额使用开关下方的说明为警示色，说明超额使用不会生效
- 账户行的操作为参与调度开关、刷新额度（窗口宽度 1280 及以上）、切换官方客户端与更多操作；更多操作菜单为详情、刷新额度、代理与删除
- 刷新全部（账户页与仪表盘）对列表中的每个账户调用 `POST /api/accounts/{id}/quota`（状态为 `error` 的账户先调用 `POST /api/accounts/{id}/verify` 重新初始化），同时最多 4 个：按钮显示“刷新中 已完成/总数”，排队的账户在状态标签旁显示沙漏，正在读取的显示转圈；每个账户完成后立即替换该行，读取失败的账户在状态标签旁显示警示图标，悬停为原因，点击打开详情，详情的额度分区显示原因，之后读到该账户的额度时清除；结束后提示失败的账户与原因。进度在切换页面后保留
- 操作结果以右下角轻提示显示，5 秒后消失，悬停、聚焦与窗口失焦不延长；新提示使总数超过 3 条时，从最早的开始移除不带操作按钮的提示，新提示与带“撤销”的提示保留

设置页各分区的配置项（数字项的取值范围见下方）：

| 分区 | 配置项 |
| --- | --- |
| 服务 `service` | `LISTEN`、`AUTH_DIR`、`RUNTIME_DIR`、API key（链接到接入页）、`MEDIA_OUTPUT`、`MEDIA_BASE_URL` |
| 网络 `network` | `PROXY`（每行一个）与代理池状态 |
| 系统提示 `prompt` | `SYSTEM_PROMPT`、原文预览、`SYSTEM_PROMPT_SECTIONS`、`SYSTEM_PROMPT_WRAP` |
| 调度 `scheduling` | `ROUTING_STRATEGY`、`PREFER_LOWER_TIER_MODELS`、`ACCOUNT_CONCURRENCY`、`REQUEST_TIMEOUT`、`QUOTA_REFRESH_INTERVAL`、`QUOTA_RESERVE_PERCENT`、`WEEKLY_QUOTA_WARMUP` |
| 日志 `logging` | `LOG_LEVEL`、`LOG_FORMAT`、`LOG_FILE`、`LOG_MAX_TOTAL_MB`、`REQUEST_LOG_BODIES` |
| 远程访问 `remote` | `ADMIN_AUTH_ENABLED`、`ADMIN_USERNAME`、`ADMIN_PASSWORD`（已设置时输入框为空并显示占位） |
| 关于 `about` | 版本与在线更新、宿主、监听地址、API 地址、启动时间、模拟的客户端版本与官方最新版本、许可证、主页、发行页 |

| 数字项 | 加减按钮范围 | 可提交的值 |
| --- | --- | --- |
| `ACCOUNT_CONCURRENCY` | 1–64 | 正整数 |
| `QUOTA_RESERVE_PERCENT` | 0–99 | 0–99 的整数 |
| `LOG_MAX_TOTAL_MB` | 20–100000 | 不小于 20 的整数 |

- 加减按钮到达范围端点时停用；输入框中直接键入的值不受按钮范围限制，按“可提交的值”校验，与服务端一致

#### 路由与登录

前端源码在 `web/src`，路由定义在 `app/router.tsx`，页面按路由拆分加载。除 `/login` 外，页面都在外壳 `app/Layout.tsx`（顶栏与内容区）内。

| 路径 | 页面 | 查询参数 |
| --- | --- | --- |
| `/login` | 登录 | `next` |
| `/` | 仪表盘 | — |
| `/accounts` | 账户 | `state`、`q`、`sort`、`page`、`account` |
| `/connect` | 接入 | `client` |
| `/models` | 模型 | `group`、`q` |
| `/requests` | 请求记录 | `range`、`state`、`protocol`、`model`、`account`、`q`、`cursor`、`id` |
| `/requests/usage` | 用量 | `range`、`by`、`model`、`account`、`api_key` |
| `/requests/logs` | 日志 | `view`、`level`、`source`、`q`、`range`、`request_id` |
| `/settings/:分区` | 设置 | — |
| 其余路径 | 页面不存在 | — |

设置分区为 `service`、`network`、`prompt`、`scheduling`、`logging`、`remote`、`about`，路径中没有或不认识的分区显示 `service`。

- 外壳先读 `GET /api/session`，`authenticated` 为 false 时以替换历史的方式转到 `/login?next=<原路径与查询>`
- 任一查询返回 401 时重新读取会话，会话无效即转到登录页；查询遇到 5xx 时重试 2 次，4xx 与网络错误不重试
- 登录页：`enabled` 为 false 时只显示开启方法；提交 `POST /api/session`，401 显示用户名或密码错误，429 显示一分钟后再试；成功后转到 `next`（不以 `/` 开头时转到 `/`），已登录时直接转到 `next`
- 顶栏导航为仪表盘、账户、接入、模型、请求、设置；仪表盘只在路径为 `/` 时高亮，其余按路径前缀高亮；“退出登录”只在开启登录且已登录时显示，`DELETE /api/session` 后转到 `/login`
- 登录后订阅 `/api/events`：连接打开时清空日志与进行中请求的缓存；`accounts` 替换账户列表，`log` 追加日志（页面保留最近 2000 条），`request` 维护进行中请求并在请求结束时刷新请求记录；进行中请求的详情显示等待，请求结束后读取记录，`login` 更新对应的登录；断开时内容区顶部显示断线提示，浏览器自动重连并重新收到初始快照
- 数据依赖集中在 `web/src/app/sync.ts`：账户列表缓存的任何写入（事件流、操作结果、重新读取）中，各账户的 ID、状态或 `quota_at` 变化时重新读取模型目录与额度变化曲线；请求结束时重新读取请求记录、最近请求与用量（含仪表盘 24 小时请求与账户的最近使用）；保存设置后重新读取模型目录与代理池；正在读取的查询不打断

#### 添加账户

添加账户对话框有浏览器登录、粘贴授权码、导入官方客户端、远程配对四种方式。打开时的默认方式：桌面窗口，或页面主机为 `localhost`、`127.0.0.1`、`[::1]` 时为浏览器登录，其余为粘贴授权码；从仪表盘引导的方式卡片打开时选中该方式。

| 方式 | 流程 |
| --- | --- |
| 浏览器登录 | `POST /api/logins` `mode=browser` → 登录链接与倒计时 → 等待回调或提交回调地址 → 结束 |
| 粘贴授权码 | `POST /api/logins` `mode=code` → 登录链接与倒计时 → 提交授权码 → 结束 |
| 导入官方客户端 | `POST /api/accounts/import` → 导入数量 |
| 远程配对 | `POST /api/pairing` → 配对命令与倒计时 → 过期后重新生成 |

- 浏览器登录与粘贴授权码生成登录链接后自动打开：浏览器中在点击“生成登录链接”时打开新标签页，链接返回后跳转到登录链接；桌面窗口交给系统浏览器打开
- 登录链接可打开或复制，倒计时取 `expires_at`，格式为 `m:ss`；回调地址与授权码都经 `POST /api/logins/{id}/code` 提交，提交前去掉首尾空白，为空时提示先粘贴
- 登录状态取自 `login` 事件与提交结果：`completed` 显示账户邮箱与“完成”按钮；`failed` 显示原因；`canceled` 或倒计时归零显示已过期；失败与过期时可“重新开始”生成新链接
- 关闭对话框时，`pending` 的登录以 `DELETE /api/logins/{id}` 取消；切换方式时清空当前登录与提交状态
- 配对命令为 `antigravity2api login --remote <页面 origin> --token <令牌>`

#### 首次使用引导

账户列表为空时仪表盘显示三步引导。引导期间添加了账户时保留引导并显示“进入仪表盘”；之后再打开仪表盘时显示总览。

| 步骤 | 完成条件 |
| --- | --- |
| 添加账户 | 账户列表非空 |
| 复制接口地址与 API key | 复制过任一接口地址或 API key，或近 24 小时有请求 |
| 复制客户端配置 | 复制过任一客户端配置片段或配置项，或近 24 小时有请求 |

- 复制记录保存在 `localStorage` 的 `antigravity2api.setup`，内容为 `{"endpoint": bool, "client": bool, "dismissed": bool}`；本地存储不可用时只在当前页面有效
- 有账户、后两步未全部完成且 `dismissed` 不为 true 时，总览顶部显示三步进度条，“隐藏”写入 `dismissed`

#### 接入页

接口地址取页面自身的 origin：OpenAI 兼容为 `<origin>/v1`，Anthropic 兼容与 Gemini 兼容为 `<origin>`。

API key 列表取自 `GET /api/settings` 的 `API_KEYS`（逗号分隔）：

- “添加 API key”对话框预填随机值：`sk-` 加 32 位字母与数字，取自 `crypto.getRandomValues`；可重新生成或换成自定义值
- 修改打开同一对话框，预填原值
- 保存前去掉首尾空白；值不能为空、不能含空白或逗号、不能与其他 key 相同
- 添加、修改与删除都以 `PATCH /api/settings` 只提交 `API_KEYS`；删除立即保存，5 秒内可撤销，撤销时写回原列表

客户端配置片段中的占位取值：

| 占位 | 取值 |
| --- | --- |
| `<origin>` | 页面 origin |
| `<key>` | 选中的 API key，默认第一个；没有 key 时为 `antigravity2api` |
| `<model>` | 选中的模型 |
| `<small>` | Claude Code 后台任务模型 |

- 可选模型为实时目录中的 Agent 模型或普通对话模型，不含图像生成、语音转写与 Tab 补全模型；目录为空时 `<model>` 与 `<small>` 写成 `<模型 ID>`
- `<model>` 的默认值：Claude Code 取第一个 `provider` 含 anthropic 的模型，其余客户端取第一个 ID 含 `flash` 的模型，没有时取第一个模型；`<small>` 默认取第一个 ID 含 `flash` 的模型
- 环境变量按终端选择写法：macOS / Linux 为 `export NAME=value`，Windows PowerShell 为 `$env:NAME = "value"`；默认值按浏览器 User-Agent 是否含 `Windows` 选择
- 选中的客户端写入 `?client=`，取值为 `claude-code`（默认）、`codex`、`opencode`、`cherry-studio`、`curl`

Claude Code 的终端命令（PowerShell 写法同上替换）与 `~/.claude/settings.json`：

```bash
export ANTHROPIC_BASE_URL=<origin>
export ANTHROPIC_AUTH_TOKEN=<key>
export ANTHROPIC_MODEL=<model>
export ANTHROPIC_DEFAULT_HAIKU_MODEL=<small>
claude
```

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "<origin>",
    "ANTHROPIC_AUTH_TOKEN": "<key>",
    "ANTHROPIC_MODEL": "<model>",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "<small>"
  }
}
```

Codex 的 `~/.codex/config.toml` 与终端命令：

```toml
model = "<model>"
model_provider = "antigravity"

[model_providers.antigravity]
name = "Antigravity2API"
base_url = "<origin>/v1"
env_key = "ANTIGRAVITY2API_KEY"
wire_api = "responses"
```

```bash
export ANTIGRAVITY2API_KEY=<key>
codex
```

OpenCode 的 `~/.config/opencode/opencode.json`，`models` 列出全部可选模型，名称为目录中的显示名，没有时为 ID：

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "antigravity2api": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Antigravity2API",
      "options": { "baseURL": "<origin>/v1", "apiKey": "<key>" },
      "models": { "<模型 ID>": { "name": "<显示名>" } }
    }
  },
  "model": "antigravity2api/<model>"
}
```

Cherry Studio 在“设置 › 模型服务”中添加提供商，各项单独复制：

| 项 | 值 |
| --- | --- |
| 提供商类型 | OpenAI |
| API 地址 | `<origin>` |
| API 密钥 | `<key>` |
| 模型 | 全部可选模型 ID，逗号分隔 |

curl 给出三个请求：

```bash
curl <origin>/v1/chat/completions \
  -H "Authorization: Bearer <key>" \
  -H "Content-Type: application/json" \
  -d '{"model": "<model>", "stream": true, "messages": [{"role": "user", "content": "Hello"}]}'

curl <origin>/v1/messages \
  -H "x-api-key: <key>" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model": "<model>", "max_tokens": 1024, "messages": [{"role": "user", "content": "Hello"}]}'

curl "<origin>/v1beta/models/<model>:generateContent" \
  -H "x-goog-api-key: <key>" \
  -H "Content-Type: application/json" \
  -d '{"contents": [{"role": "user", "parts": [{"text": "Hello"}]}]}'
```

#### 账户批量操作与排序

- 勾选账户后显示批量操作栏：启用、停用、刷新额度、重新初始化（只作用于所选账户中 `state` 为 `error` 的账户）、删除；对所选账户逐个调用单账户接口，结束后提示完成数量与失败的账户；批量删除 5 秒内可撤销
- 删除账户（单个与批量）：账户行立即隐藏，5 秒后发出 `DELETE /api/accounts/{id}`；5 秒内撤销，或经导入与登录再次加入同一账户时不发出删除并恢复显示；删除完成且账户列表刷新后不再隐藏该账户，之后再次加入时照常显示
- 排序：按添加时间（默认）；按重置时间，取各额度桶中剩余比例低于 100% 的最早 `reset_time`，从早到晚；按最近使用，取近 30 天用量中各账户的 `last_at`，从近到远。没有对应时间的账户排在后面，保持添加顺序

#### 额度与推荐账户

| 量 | 计算 |
| --- | --- |
| 可用比例 | 一个额度组 5 小时与每周 `remaining_fraction` 中较小的一项 |
| Gemini 额度、Claude 与 GPT 额度 | 两项，分别对应额度组 `gemini` 与 `3p`：已启用账户该组可用比例的平均 |
| 5 小时、每周均值 | 每个额度组各一对：已启用账户该组该窗口剩余比例的平均 |
| 低额度账户 | 已启用且任一额度桶低于 20% 的账户数 |
| 就绪账户 | `state` 为 `ready` 的账户数 / 总数，说明中列出其余状态的数量 |
| 24 小时请求 | 近 24 小时请求数、失败数与估算费用 |
| 推荐账户 | 每个额度组中可用比例最高的账户 |

- 已启用指 `state` 不是 `disabled`；计算额度均值时，同一共享额度池的账户只取列表中第一个出现的，没有该组额度的账户不计入
- 推荐账户的候选：界面状态为就绪或冷却中、该组不在 `cooldown` 中、可用比例大于 0；比例相同时取列表靠前的账户
- 界面状态：`error` 且带 `verify_url` 为“需验证”，其余与 `state` 相同
- 比例条颜色：不低于 30% 为绿色，10% 到 30% 为黄色，低于 10% 为红色
- 重置倒计时取 `reset_time` 与当前时间之差，向上取整到分钟，写成 `6d 15h`、`1h 16m`、`12m`，为零的部分省略，已过重置时间时不显示；账户页与仪表盘每 30 秒刷新一次

#### 图表

| 图表 | 数据 | 系列与单位 | 范围 |
| --- | --- | --- | --- |
| 用量趋势 | `GET /api/usage` 的 `buckets` | 逐桶请求数柱状图 | 24 小时、7 天（默认）、30 天、90 天 |
| 额度变化 | `GET /api/quota/snapshots` | 四个额度桶的剩余比例折线，0–100% | 24 小时（默认）、7 天 |

- 用量趋势：分桶由服务按 `bucket=auto` 选择，`tz` 为浏览器时区；柱高按范围内最大请求数归一，有请求的桶至少 2%；分组为“日期”时以 `bucket=86400` 按本机自然日分桶，不显示趋势
- 额度变化：24 小时分 96 段，7 天分 168 段；每段取各账户在段末之前最近一次快照的剩余比例再求平均，段末之前没有快照的桶在该段断开；Gemini 为蓝色、Claude 与 GPT 为紫色，实线为 5 小时、虚线为每周；可按账户筛选，范围与账户不写入 URL

#### 筛选与 URL

- 页面上的筛选、范围、分组、视图、客户端、页码与打开的详情写入查询参数（见路由表），刷新或分享链接得到同一页面
- 不认识或缺省的取值使用默认值：账户状态全部、按添加时间排序，模型额度组全部，请求记录范围 `24h`，用量范围 `7d`、分组 `model`，日志视图 `live`、级别全部，已保存日志范围 `24h`；用量页之外的默认值不写入 URL
- 筛选变化替换当前历史记录；请求记录翻页与打开详情新增历史记录，其余筛选变化同时清除 `cursor`
- 顶栏导航与请求页的子页面导航回到各页本次打开管理端后最后一次的子页面与查询参数（`web/src/app/remember.ts`），点击当前所在的项保持当前参数；刷新页面后从无参数开始
- 请求记录与已保存日志的时间范围截止到当前时间之后 1 秒；实时日志在页面内按最低级别、来源与关键词（匹配消息与来源，不区分大小写）筛选；每条日志一行，宽度小于 640 时消息换到第二行

| 页面 | 参数取值 |
| --- | --- |
| 账户 | `state`：`ready`、`cooling`、`verify`、`error`、`disabled`；`sort`：`reset`、`used` |
| 模型 | `group`：`gemini`、`3p` |
| 请求记录 | `range`：`1h`、`24h`、`7d`、`30d`；`state`：结果；`protocol`：四种公开协议 |
| 用量 | `range`：`24h`、`7d`、`30d`、`90d`；`by`：`model`、`api_key`、`account`、`day`；`model`、`account`、`api_key`：逗号分隔的筛选值 |
| 日志 | `view`：`live`、`saved`；实时 `level`：`INFO`、`WARN`、`ERROR`；已保存 `range`：`24h`、`7d`、`30d`，`level=ERROR` |

#### 数字与时间格式

数字与时间用 `Intl` 按界面语言的区域格式化（简体中文 `zh-CN`、繁体中文 `zh-TW`、英文 `en`），时间按浏览器时区显示，全站使用等宽数字。

| 类型 | 格式 |
| --- | --- |
| 整数 | 千分位 |
| 大数 | 紧凑写法，最多 1 位小数 |
| token 上限 | 1024 的整数倍按 1024 进制，其余按 1000 进制，写成 K、M |
| 百分比 | 1 位小数 |
| 费用 | 美元；大于 0 且小于 1 时保留 4 位有效数字，其余最多 2 位小数 |
| 时长 | 不足 1 秒为整数毫秒；不足 10 秒为秒且 2 位小数；不足 1 分钟为秒且 1 位小数；不足 1 小时为分钟；其余为小时 |
| 字节 | 1024 进制，字节取整，KB 起 1 位小数 |
| 相对时间 | 秒、分、时、天逐级进位 |
| 日期时间 | 当天只显示时分秒，其余加月日，24 小时制 |
| 完整时间 | 中等长度的日期与时间，24 小时制 |

#### 语言、配色与快捷键

- 语言：简体中文 `zh`、繁体中文（台湾）`zh-TW`、英文 `en`，保存在 `localStorage` 的 `antigravity2api.language`；没有保存值时依次查看浏览器的语言列表，第一个以 `zh` 开头的标签中 `zh-TW`、`zh-HK`、`zh-MO`、`zh-Hant` 开头的选繁体中文，其余选简体中文，先遇到 `en` 开头的标签时选英文，都没有时选英文；`html` 的 `lang` 同步为对应区域
- 文案在 `web/src/app/messages.ts`（外壳与公共文案）与各功能目录的 `web/src/features/<功能>/messages.ts` 中，每份以 `defineMessages(简体中文, 英文, 繁体中文)` 声明，三种语言的键由类型检查保证一致
- 配色：浅色与深色，顶栏配色按钮点击即切换到另一种，新配色从按钮中心以圆形展开（切到浅色）或旧配色收回按钮中心（切到深色），按钮中的太阳与月亮图标旋转交替；切换结果保存在 `localStorage` 的 `antigravity2api.theme`；没有保存值时跟随系统配色并随系统配色变化切换；结果写入 `html` 的 `data-theme`；`index.html` 在样式表之前同步加载 `web/public/theme.js`，首帧绘制前写入 `data-theme`

| 按键 | 作用 | 生效位置 |
| --- | --- | --- |
| `/` | 聚焦本页搜索框 | 账户、模型、请求记录、日志；焦点不在输入框且未按 Ctrl、⌘、Alt |
| Ctrl + S、⌘ + S | 保存设置 | 设置页 |
| Tab | 首个焦点为“跳到主要内容” | 全部页面 |

#### 其他

- 桌面版设置页的远程访问默认折叠，相关字段校验出错时展开
- 管理端从 `/api/events` 接收账户、日志、请求与登录事件；静态资源的响应见“管理 API 与管理端”开头的入口表

### 官方版本检查

服务启动 10 秒后读取一次官方客户端的更新清单，此后每小时一次，结果只保存在内存中，`/api/status` 的 `latest_version` 高于 `client_version` 时管理端提示有新版本。请求形态见 [逆向指南](reverse-engineering.md) 第 4 节。

## 6. 命令行版与桌面版

| 项 | 命令行版 | 桌面版 |
| --- | --- | --- |
| 构建 | 默认 | 标签 `desktop,production` |
| 工作目录 | 启动时的当前目录 | 见表下 |
| 不带参数启动 | `serve --open-ui` | 打开桌面窗口 |
| 子命令 | 支持 | 支持，见表下 |
| 管理端 | 系统浏览器 | 无边框 WebView 窗口，`?shell=desktop` |
| 宿主标识 | `/api/status` 的 `host` 为 `web` | `desktop` |

- 桌面版的工作目录：Windows 为可执行文件所在目录（便携版）；macOS 与 Linux 为用户配置目录下的 `Antigravity2API`；启动时创建并进入
- 桌面版执行子命令时不检查单实例；Windows 上附加启动它的控制台，没有控制台时新建一个

命令行版不带任何参数启动时等同 `serve --open-ui`：服务开始监听后用系统浏览器打开 `http://<实际监听地址的回环形式>/`，Windows 经 `rundll32 url.dll,FileProtocolHandler`，macOS 经 `open`，其余经 `xdg-open`。

桌面版：

- 单实例：Wails `SingleInstance` 的标识为 `com.mag1cfall.antigravity2api`，再次启动时显示已有实例的窗口并退出；第一个参数是子命令（`--hidden` 除外）时先执行子命令，不经过单实例检查
- 服务开始监听后显示窗口，服务启动失败时以原生对话框显示原因并退出；Windows 上配置错误等启动前的错误以系统消息框显示
- 窗口标题 `Antigravity2API`，无边框，背景色 `#e6e7ed`，1280×800，最小 960×600；Windows 与 macOS 关闭窗口后应用留在托盘，窗口与 WebView 进程一并结束，再次显示时按关闭前的位置、大小与最大化状态重建窗口，载入关闭前所在的管理端页面（页面以 `desktop:route:<路径与查询>` 消息报告当前路径）；Linux 关闭窗口即退出
- 托盘：图标在 macOS 菜单栏为单色模板图（`tray-template.png`，系统按菜单栏配色着色），其余平台为应用图标；提示文字为 `Antigravity2API`，点击图标在新的 goroutine 中显示窗口，窗口重建期间的再次点击（如双击）等重建完成后显示同一窗口；菜单文案为固定的简体中文，依次为：

| 菜单项 | 文案与行为 |
| --- | --- |
| 额度组剩余（不可点） | `Gemini 剩余 <x>%`、`Claude 与 GPT 剩余 <x>%` |
| 子菜单 `各账户额度` | 每个账户一行，见表下 |
| `刷新额度` | `POST /api/accounts/refresh` 后重建菜单 |
| `显示窗口` | 有 WebView2 时出现 |
| `在浏览器中打开` | 系统浏览器打开管理端 |
| `复制 API 地址` | 复制 `http://<实际监听地址的回环形式>` |
| `开机启动` | 勾选项，见下方开机启动表 |
| `退出` | 结束应用 |

- 额度组剩余没有数据时 `<x>%` 为 `—`
- `各账户额度` 每行为 `<备注名或邮箱>  Gemini <x>% · Claude 与 GPT <y>%`，停用账户为 `<名称>  已停用`；没有账户时省略该子菜单
- 托盘额度经回环地址读取 `GET /api/accounts`，应用启动后每分钟与“刷新额度”完成后重建菜单：账户在一个额度组的可用比例为该组各额度桶中最低的一项；额度组的剩余为已启用账户可用比例的平均，同一共享额度池的账户只计一次；百分比向下取整；“刷新额度”之后与“开机启动”前后各有一条分隔线
- 带 `--hidden` 启动时只显示托盘，首次显示时创建窗口；开机启动项是否带它见下表
- 页面中的外部链接经系统浏览器打开；拖入窗口的文件与链接不被接受，窗口停留在管理端；Windows 上 WebView 数据保存在 `RUNTIME_DIR/webview`
- Windows 上 WebView2 以 `--disable-lcd-text` 启动，文字使用灰度抗锯齿
- Windows 上经 Wails 查不到 WebView2 运行时版本时只创建托盘，菜单没有“显示窗口”：服务就绪后（`--hidden` 除外）在默认浏览器打开管理端，单击托盘图标或再次运行可执行文件也打开管理端

窗口地址为 `http://<实际监听地址的回环形式>?shell=desktop`，服务开始监听后创建窗口。回环形式按下表由监听地址得到，托盘“复制 API 地址”与窗口地址用实际监听地址，`status` 子命令用 `LISTEN`：

| 监听地址的主机 | 回环形式 |
| --- | --- |
| 空（`:8017`）、`0.0.0.0`、`::`（`[::]:8017`） | `127.0.0.1:<端口>` |
| 其他（回环地址、局域网 IP、主机名） | 原样，IPv6 带方括号 |
| 无法按 `host:port` 解析 | 原文 |

启动时服务在后台协程运行，桌面壳每 50ms 尝试连接监听地址，连上后才运行窗口。

页面与桌面壳的接口：

- 页面把 `?shell=desktop` 存入 `sessionStorage` 的 `antigravity2api.shell`（`web/src/lib/desktop.ts`），同一窗口内跳转与刷新保持桌面模式，顶栏显示窗口按钮并成为拖动区
- 页面加载完成时（Windows `WebViewNavigationCompleted`、macOS `WebViewDidFinishNavigation`、Linux `WindowLoadFinished`），桌面壳先代页面发出 `wails:runtime:ready`，使窗口立即执行脚本，再注入 `internal/desktop/page.js`（`pageScript`）并发送一次最大化状态
- 页面经 `window._wails.invoke(<字符串>)` 向桌面壳发消息：

| 消息 | 作用 |
| --- | --- |
| `desktop:open:<绝对地址>` | 系统浏览器打开，只接受 http 与 https |
| `desktop:window:minimise` | 最小化 |
| `desktop:window:toggle-maximise` | 最大化或还原 |
| `desktop:window:close` | 关闭窗口 |
| `wails:drag` | 开始拖动窗口 |
| `wails:resize:<方向>-resize` | 从边缘缩放，方向为 `n`、`s`、`e`、`w` 与组合 |

- `desktop:open:` 与 `desktop:window:` 由 `RawMessageHandler` 按前缀 `openPrefix`、`windowPrefix` 处理，窗口动作取自 `windowActions`，其他动作名忽略；关闭窗口在 Windows 与 macOS 上结束窗口、应用留在托盘
- 注入脚本：`target="_blank"` 或跨源链接的点击、`window.open` 都换成 `desktop:open:`，相对地址按页面地址补全；在计算样式 `--wails-draggable` 为 `drag` 的元素上按下左键并移动时发 `wails:drag`，双击时发 `desktop:window:toggle-maximise`；距窗口边缘 5px、角落 15px 内按下左键并移动时发缩放消息，鼠标经过边缘时显示对应光标；macOS 与窗口最大化时不做边缘缩放
- 最大化同步：每次窗口尺寸变化与页面加载后，桌面壳执行 `window.dispatchEvent(new CustomEvent("desktop:maximised", {detail: <是否最大化>}))`（`maximisedEvent`）；注入脚本据此停用边缘缩放，顶栏按钮在“最大化 / 还原”之间切换

开机启动由托盘菜单开关，标识为 `com.mag1cfall.antigravity2api`（`enableAutostart`）：

| 平台 | 注册方式 | 保存位置 | 启动参数 |
| --- | --- | --- | --- |
| Windows | 注册表 Run 项 | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 下同名值 | `--hidden` |
| macOS 13 及以上的 `.app` | SMAppService 登录项 | 系统设置的登录项 | 无 |
| macOS 其他情况 | LaunchAgent | `~/Library/LaunchAgents/com.mag1cfall.antigravity2api.plist` | `--hidden` |
| Linux | XDG autostart | `$XDG_CONFIG_HOME/autostart/com.mag1cfall.antigravity2api.desktop` | 无 |

- 开机启动由 Wails `v3.0.0-beta.29` 的 autostart 实现；“可执行文件”为当前可执行文件解析符号链接后的绝对路径
- Windows：Run 值的数据为可执行文件路径加 ` --hidden`；路径含空格、制表符或 `"` 时按 Windows 命令行规则加引号
- macOS：从带 bundle ID 的 `.app` 运行且系统主版本不低于 13 时注册 SMAppService 登录项；其余情况（含 SMAppService 不可用）写 LaunchAgent plist 并以 `launchctl` 载入，plist 的 `Label` 为 `com.mag1cfall.antigravity2api`，`ProgramArguments` 为可执行文件与 `--hidden`，`RunAtLoad` 为 true，`KeepAlive` 为 false
- Linux：`$XDG_CONFIG_HOME` 未设置时为 `~/.config`；`.desktop` 文件内容如下，`Exec` 为可执行文件，含空格或制表符时加双引号并转义 `"`、`\`、`` ` ``、`$`，不带参数

```ini
[Desktop Entry]
Type=Application
Name=Antigravity2API
Exec=<可执行文件>
X-GNOME-Autostart-enabled=true
Hidden=false
NoDisplay=false
Terminal=false
```

- 托盘“开机启动”的勾选状态在每次重建菜单（每分钟与“刷新额度”之后）时读取：Windows 为 Run 值的第一个参数与可执行文件相同（不区分大小写），macOS 为 SMAppService 状态为已启用或 plist 的第一个程序参数与可执行文件相同，Linux 为 `.desktop` 的 `Exec` 第一个参数与可执行文件相同
- 切换失败时勾选状态恢复原值，并以对话框显示错误

### 子命令输出

子命令出错时把错误文本写到标准错误，以退出码 1 结束；成功时退出码为 0。`accounts` 与 `status` 的表格由 Go `text/tabwriter`（最小宽度 0、制表宽度 4、间距 2、以空格填充）写出：每列宽为该列最长单元格的字符数加 2，最后一列不补空格。

| 子命令 | 输出 |
| --- | --- |
| `version` | 一行 `app.Version()`：标签构建为发行版本号，源码构建为伪版本或 `(devel)` |
| `accounts` | 账户表，见表下 |
| `status` | 服务概况与账户表，见表下 |
| `login` | 见下方登录过程表 |
| `import` | 每个导入的账户一行 `已导入账户 <邮箱>` |
| `switch` | 成功时 `官方客户端已切换到账户 <邮箱>` |

- `accounts`：表头 `账户 套餐 状态 添加时间`，每个账户一行：邮箱、`account.json` 的 `tier`（`currentTier.id`）、`启用` 或 `停用`、本地时区的 `YYYY-MM-DD HH:MM:SS`；按添加时间排序
- `status`：第一行 `Antigravity2API <版本> 监听 <listen>，自 <本地时间 YYYY-MM-DD HH:MM:SS> 运行`；随后表头 `账户 状态 套餐 额度`，每个账户一行：`/api/accounts` 的 `id`、`state`、`tier`（有付费套餐时为付费套餐）、额度

`login` 的过程输出：

| 方式 | 打印 | 等待 | 成功 |
| --- | --- | --- | --- |
| 默认 | `在浏览器中完成 Google 登录：`、授权链接 | 回环端口收到回调 | `已添加账户 <邮箱>` |
| `--code` | 提示、授权链接、`授权码: ` | 标准输入的一行 | `已添加账户 <邮箱>` |
| `--cloud` | 提示、授权链接 | 回环端口收到回调 | `已添加企业账户 <邮箱>` |
| `--remote` | 同默认或 `--code` | 同默认或 `--code` | `已添加账户 <邮箱> 到 <地址>` |

- 默认与 `--cloud` 打印链接后用系统浏览器打开它，打开命令同“命令行版不带任何参数启动”，启动失败时以该错误结束
- `--cloud` 的提示为 `在浏览器中完成 Continue with Google Cloud 登录：`
- `--code` 的提示为 `在任意设备的浏览器中打开链接完成 Google 登录，把页面给出的授权码粘贴到这里：`；`授权码: ` 之后不换行，读到的行去掉首尾空白后为空时以 `授权码为空` 结束
- 等待没有时限，Ctrl+C 或 SIGTERM 取消，以 `context canceled` 结束；回调的 `state` 与本次登录不同时回环端口返回 400 `state mismatch` 并继续等待；`state` 相同而带 `error` 时回环端口返回 400，以 `OAuth error: <error>` 结束
- `--remote` 的远程服务返回 201 以外时以 `远程服务返回 <状态码>: <正文>` 结束

参数与用法错误：

| 情形 | 标准错误 |
| --- | --- |
| 未知子命令 | `未知子命令 "<名称>"，可用：serve、login、import、switch、accounts、status、version` |
| `--cloud` 与 `--remote` 或 `--code` 同时使用 | `--cloud 在本机以浏览器登录并写入本机账户库，不能与 --remote、--code 同时使用` |
| `--remote` 没有 `--token` | `--remote 需要同时提供 --token` |
| `switch` 的参数不是一个邮箱 | `用法: antigravity2api switch [参数] <账户邮箱>` |
| `-h`、`-help` | `Usage of <子命令>:` 与各参数的默认值和说明，再一行 `flag: help requested` |
| 未定义的参数或参数值无效 | Go `flag` 包的错误与用法，再一行同一错误 |

- 以上情形的退出码都为 1；命令行版第一个参数以 `-` 开头时按 `serve` 的参数解析，用法标题为 `Usage of serve:`
- 各子命令接受 `--listen`、`--auth`、`--runtime`、`--proxy`；`serve` 另有 `--open-ui`，`login` 另有 `--code`、`--cloud`、`--provider`、`--remote`、`--token`；参数写作 `-名称` 或 `--名称` 均可

| 参数 | 类型 | 默认值 | 说明文字 |
| --- | --- | --- | --- |
| `--listen` | 字符串 | 空 | `监听地址，覆盖 LISTEN` |
| `--auth` | 字符串 | 空 | `账户目录，覆盖 AUTH_DIR` |
| `--runtime` | 字符串 | 空 | `运行数据目录，覆盖 RUNTIME_DIR` |
| `--proxy` | 字符串 | 空 | `上游代理，多个以逗号分隔，覆盖 PROXY` |
| `serve --open-ui` | 布尔 | 见表下 | `启动后用系统浏览器打开管理端，不带任何参数启动时默认打开` |
| `login --code` | 布尔 | false | `打印授权链接并粘贴授权码，用于没有浏览器的环境` |
| `login --cloud` | 布尔 | false | `以 Continue with Google Cloud 的企业 WIF 方式登录` |
| `login --provider` | 字符串 | 空 | `企业 WIF 登录的 provider 名` |
| `login --remote` | 字符串 | 空 | `把账户添加到远程服务，值为其管理端地址，例如 http://server:8017` |
| `login --token` | 字符串 | 空 | `远程服务管理端生成的配对令牌` |
| 桌面版 `--hidden` | 布尔 | false | `启动后只显示托盘` |

- `--open-ui` 在 `serve` 子命令中默认为 false，命令行版不带任何参数启动时为 true
- 四个通用参数为空或只有空白时不覆盖配置；`--proxy` 按 `PROXY` 的规则校验
- `--cloud` 不带 `--provider` 时授权链接的 `provider` 参数为空；`--provider` 只在 `--cloud` 时使用，`--token` 只在 `--remote` 时使用，单独给出时不起作用
- 桌面版的用法标题为 `Usage of Antigravity2API:`
- 桌面版不带子命令启动时，参数解析与配置错误以原生对话框显示；桌面版的参数为 `--hidden` 与上述四个通用参数
- `serve` 启动时没有账户，在标准错误打印 `没有账户，执行 antigravity2api login 添加，或在管理端添加` 并继续运行

- `status` 的额度列为各额度桶 `<bucket_id> <剩余百分比>%` 以空格相连，百分比按 `%.0f` 四舍五入；账户带 `error` 时额度列为该错误文本
- `status` 经 `http://<监听地址的回环形式>` 读取 `/api/status` 与 `/api/accounts`；`/api/status` 连接失败或返回 200 以外时打印 `服务未在 <LISTEN> 运行: <原因>`

```text
$ antigravity2api accounts
账户                 套餐             状态  添加时间
alice@example.com  free-tier      启用  2026-10-14 14:47:22
bob@example.com    standard-tier  停用  2026-10-15 09:03:10

$ antigravity2api status
Antigravity2API v0.1.3 监听 127.0.0.1:8017，自 2026-10-23 09:00:00 运行
账户                 状态     套餐                  额度
alice@example.com  ready  g1-ultra-lite-tier  gemini-weekly 100% gemini-5h 97% 3p-weekly 100% 3p-5h 100%
bob@example.com    error  free-tier           需要验证：...
```

### 在线更新

在线更新在 `internal/app/update.go`（版本查询、下载与校验）、`update_headless.go` 与 `update_desktop.go`（按构建选择发行包与安装方式）和 `internal/desktop/update.go`（Wails 更新器）：

- `GET /api/update` 读取 GitHub 最新发行版本（缓存一小时，`?refresh=1` 重新读取），返回 `current`、`latest`、`available`、`mode`、发布说明与时间；`POST /api/update` 重新读取后安装，成功返回 202，已是最新、源码构建、容器部署或更新进行中返回 409 `update_conflict`，下载或校验失败返回 502 `update_failed`；请求与下载都经 `PROXY`，未设置时用系统代理
- `GET /api/update` 读取 GitHub 失败时返回 200，`error` 为失败原因，`latest`、`notes`、`url` 与 `published_at` 保留上一次成功读取的值；失败的读取同样更新 `checked_at` 并缓存一小时。读取过程中客户端断开时返回 502 `update_check_failed`。`mode` 不为 `binary` 时不读取 GitHub
- 版本取构建信息的模块版本，没有时为 `(devel)`：不带预发布与构建后缀的语义化版本为 `binary`，伪版本、预发布标签、`+dirty` 与 `(devel)` 为 `development`；`binary` 版本在存在 `/.dockerenv` 或 `/run/.containerenv` 时为 `container`
- 镜像构建的上下文包含 `.git` 与全部受版本管理的文件，`.dockerignore` 只排除 Git 忽略的产物与本地数据：标签构建的镜像中版本为该标签，`mode` 为 `container`；有未提交改动的工作区构建为 `+dirty` 版本，`mode` 为 `development`
- 下载与校验：从 GitHub 最新发行版本接口（`https://api.github.com/repos/Mag1cFall/Antigravity2API/releases/latest`）中找到同名资产，按其 `browser_download_url` 下载，以资产的 `digest` 字段（`sha256:<十六进制>`）校验整个发行包；没有该字段或摘要不一致时失败

| 运行方式 | 发行包 | 安装 |
| --- | --- | --- |
| 命令行版 | `antigravity2api-<版本>-<系统>-<架构>`，Windows 为 Windows 包 | 取出可执行文件，经 `minio/selfupdate` 替换后重启 |
| 桌面版的 `serve` 子命令 | 当前平台桌面包，Windows 为 Windows 包 | 同命令行版 |
| 桌面窗口 | 当前平台桌面包 | 经 Wails 更新器暂存，Windows 与 Linux 为包中的可执行文件，macOS 为 `Antigravity2API.app` 的 tar.gz |

- 替换后重启：停止服务后以 `serve`、原参数与 `--open-ui=false` 启动新版本；Windows 上新进程沿用当前控制台，其他系统原地替换进程；HTTP 服务关闭超时也照常重启
- Wails 更新器重启：更新器的辅助进程等当前进程结束后替换文件并启动新版本；Linux 与 macOS 的暂存目录与替换目标位于同一目录
- Windows 上被替换的旧文件留在同一目录：命令行版为隐藏文件 `.antigravity2api.exe.old`，桌面版为 `antigravity2api.exe.old.<时间戳>`
- 管理端事件流断开重连后读取 `/api/status`，版本与上次不同时刷新页面，在线更新重启后任何页面都切换到新版本

设置页“关于”分区的“版本与在线更新”一项读取 `GET /api/update`，按第一条符合的行显示：

| 状态 | 显示 |
| --- | --- |
| 读取中 | `正在读取最新版本` |
| 读取失败 | `无法读取更新状态：<错误>` 与“检查更新” |
| `mode` 为 `container` | `Docker 部署随镜像更新，拉取新镜像后重建容器` |
| `mode` 为 `development` | `源码构建不支持在线更新，新版本从发布页下载` |
| `updating` | `正在重启到 <latest>，页面在新版本就绪后自动刷新` |
| 带 `error` | `无法读取最新版本：<error>` |
| `available` | `新版本 <latest> · <published_at>发布` 与“更新到 <latest>” |
| 其他 | `已是最新版本 · <checked_at>检查` |

- 后三行都带“检查更新”按钮，它请求 `GET /api/update?refresh=1` 并以结果替换显示，失败时弹出错误提示；带 `error` 且 `available` 时同样显示“更新到 <latest>”
- “更新到 <latest>”打开确认对话框：标题 `更新到 <latest>`，说明 `下载 <latest> 的发行包并校验 SHA-256，替换程序后重启，页面在新版本就绪后自动刷新。`，按钮“取消”与“确认更新”；“确认更新”发 `POST /api/update`，成功时关闭对话框，失败时弹出错误提示
- 时间按界面语言的完整日期时间显示；`notes` 与 `url` 不在该项中显示

## 7. 测试

```bash
go test ./...
go vet ./...
```

| 包 | 单元测试内容 |
| --- | --- |
| `config` | `.env` 往返、环境变量覆盖、校验、启动参数覆盖、系统代理解析 |
| `balancer` | 按目录选账户、最久未选中者轮换、等待槽位、冷却、会话粘连、排除账户、目录合并 |
| `app` | 远程管理需要登录、验证链接、代理脱敏 |
| `requestdb` | 记录查询与详情、正文保留条数、过期清理、汇总与记录一致、日志查询、结果判定、上游错误读取、费用 |
| `service` | 流中错误原样返回、客户端取消后排空上游 |
| `api` | 空消息过滤、Gemini 输出 part 与签名、`inlineData` 兼容、API key 匹配 |


修改协议实现时，先对照协议参考中的官方现场形态，再在本地服务上用对应 SDK 发请求核对输出。
