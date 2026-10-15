# 开发与贡献

Antigravity2API 是单个 Go 进程：公开协议适配、账户池、上游客户端、请求账本、管理 API 与内嵌的 React 管理端都在同一监听上运行。上游请求的格式、身份与伴随请求见 [协议参考](protocol.md)，日志与请求账本见 [运行日志与请求账本](logging.md)，对齐官方新版本的方法见 [逆向指南](reverse-engineering.md)。

## 1. 环境与构建

| 场景 | 必需组件 |
| --- | --- |
| 运行 Release | 对应平台的可执行文件或安装包 |
| 命令行版源码构建 | Go 1.27、Node.js 22.22+ 或 24+、pnpm 11.10 |
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

Windows 上构建前用 `wails3 generate syso` 生成图标、清单与版本信息资源，命令行版与桌面版都带这些资源；桌面版另加 `-ldflags='-H windowsgui'`。前端开发时运行 `pnpm dev`，Vite 把 `/api`、`/v1`、`/v1beta` 与 `/readyz` 代理到 `http://127.0.0.1:8017`。

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
| `headless` | `go test`、`go vet`、以 `go.mod` 的 Go 版本构建，再以最新稳定版交叉编译，Windows 包带图标、清单与版本信息 | 5 个命令行包 |
| `desktop-windows` | `desktop,production` 构建，带图标、清单与版本信息，打成便携 zip | `desktop-<版本>-windows-amd64.zip` |
| `desktop-macos` | arm64 与 amd64 合并为通用二进制，ad-hoc 签名 | `darwin-universal.dmg` |
| `desktop-linux` | amd64 与 arm64 | `AppImage`、`deb` |
| `release` | 只在推送标签时运行 | GitHub Release |
| `docker` | 只在推送标签时运行 | `ghcr.io/mag1cfall/antigravity2api` |

- 版本：标签构建为标签名，可执行文件资源与桌面包的数字版本去掉 `v` 与 `-` 之后的部分；其余构建为提交号前 7 位，数字版本为 `0.0.0`
- 命令行包：`windows/amd64`、`linux/amd64`、`linux/arm64`、`darwin/amd64`、`darwin/arm64`，`CGO_ENABLED=0`、`-trimpath -ldflags='-s -w'`；包内附 `README*.md`、`LICENSE`、`.env.example` 与 `docs/`；Windows 为 zip，其余为 tar.gz
- 包名：`antigravity2api-<版本>-<系统>-<架构>`，桌面版为 `antigravity2api-desktop-<版本>-<平台>`
- Release：标签含 `-` 时标为预发布，说明由 GitHub 生成
- 镜像：`linux/amd64` 与 `linux/arm64`，标签取 Git 标签原文；`Dockerfile` 分三段，Node 构建前端、Go 交叉编译、`debian:trixie-slim` 运行，工作目录 `/data`，`LISTEN=0.0.0.0:8017`，入口 `antigravity2api serve`

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
  desktop/             # Wails 窗口、托盘、单实例与开机启动
  logging/             # slog 输出、日志文件轮转与崩溃文件
  prompt/              # 四档系统提示与 user 轮模板
  requestdb/           # SQLite 请求账本、汇总、额度快照、日志与费用估算
  service/             # 生成服务：模型解析、会话记录、请求改写、输出核对、换号与账户生命周期
  tokencount/          # Gemma 3 分词的本地 token 估算
  webui/               # 内嵌管理端静态资源
web/                   # 管理端源码（React 19、Vite、Tailwind CSS）
build/                 # 桌面版图标、Windows 清单与版本信息、Info.plist、nfpm 与 .desktop 配置
```

依赖方向：

```text
cmd ──► app ──► api ──► antigravity
 │       ├────► service ──► balancer, prompt, tokencount, auth, requestdb, config ──► antigravity
 │       └────► auth, config, logging, requestdb
 └────► desktop, webui
```

- `antigravity` 不依赖其他内部包；`api` 只依赖 `antigravity` 的类型与 `Service` 接口（`Models`、`Generate`、`CountTokens`）
- `service.Service` 实现 `antigravity.Service`；`app` 用 `recordingService` 包装它，在生成开始时写请求日志并转发事件给请求记录
- `logging` 只依赖 `config` 的日志文件大小常量

公开生成请求的处理链：

```text
HTTP 请求
  -> access：生成请求 ID，写 X-Request-ID，结束后写访问日志
  -> securityHeaders
  -> recordPublic（通过 API key 校验的 POST）：建立请求记录、REQUEST_TIMEOUT、管理端停止入口
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
- 浏览器来源：`API_KEYS` 为空时，`Origin` 为 `null`、无法解析，或为 `http`、`https` 且主机不是 localhost 与回环地址的请求返回 401；配置了 key 时不检查来源
- API key：依次取 `X-Goog-API-Key`、`X-API-Key`、`Authorization: Bearer`、`?key=`，任一项与配置的某个 key 相同即通过，记录与用量按该 key 统计；都不匹配时按路径返回所属协议的 401（`/v1beta/` 为 Gemini `UNAUTHENTICATED`，`/v1/messages` 或带 `anthropic-version` 为 Anthropic `authentication_error`，其余为 OpenAI `invalid_api_key`）
- 客户端 IP：直连地址属于 `TRUSTED_PROXIES` 时，从右向左读 `X-Forwarded-For`（多个头按逗号拼接），取第一个不可信地址；遇到无法解析的项或全部可信时取直连地址

## 3. 配置与持久状态

### 配置

配置项、默认值与含义见 README 的“环境变量”。读取顺序：

1. 当前目录的 `.env`；文件不存在时全部取默认值
2. 进程环境变量中的同名项覆盖文件值
3. 启动参数 `--listen`、`--auth`、`--runtime`、`--proxy` 覆盖 `LISTEN`、`AUTH_DIR`、`RUNTIME_DIR`、`PROXY`

`.env` 的解析规则：

| 项 | 规则 |
| --- | --- |
| 空行、`#` 开头的行 | 跳过 |
| 没有 `=` 的行 | 报错，带文件名与行号 |
| 不在配置项清单中的键 | 忽略 |
| 单引号值 | 原文 |
| 双引号值 | 按 Go 字符串字面量解码 |
| 裸值 | 去掉 ` #` 之后的行尾注释 |
| `MODEL_MAPPING` | 逗号分隔，每项以最后一个 `:` 分成别名与模型 ID |
| `TRUSTED_PROXIES` | 逗号分隔的 IP 或 CIDR |
| `SYSTEM_PROMPT_SECTIONS`、`PREFER_LOWER_TIER_MODELS` | 逗号分隔 |
| `REQUEST_TIMEOUT` | Go 时长，例如 `30s`、`5m` |

校验失败时启动报错：`LISTEN` 须为 `host:port`；`AUTH_DIR`、`RUNTIME_DIR` 非空；代理须为 `http`、`https` 或 `socks5` URL；`SYSTEM_PROMPT_SECTIONS` 只含官方段名，`SYSTEM_PROMPT_WRAP` 为 `full`、`time` 或 `none`；`ACCOUNT_CONCURRENCY` 为正整数；`REQUEST_TIMEOUT` 为正数时长；`LOG_MAX_TOTAL_MB` 不小于 20；开启 `ADMIN_AUTH_ENABLED` 须有用户名与密码。

管理端保存设置时，服务读取 `.env`（叠加进程环境变量）、合并提交的项并校验，按配置项清单的顺序写回全部项：含空白、`#` 或引号的值写成双引号形式，文件经同目录临时文件原子替换，权限 0600。运行配置随之替换，`LISTEN`、`AUTH_DIR` 与 `RUNTIME_DIR` 保持启动值，其余项在下一次请求生效；日志级别、格式、日志文件与正文保存立即切换，管理登录配置变化时撤销全部管理会话。

上游代理的选择顺序：账户自设的代理、`PROXY`、环境变量 `HTTPS_PROXY`、`HTTP_PROXY` 与 `NO_PROXY`、Windows 当前用户的系统代理设置。系统代理只在 `ProxyEnable` 开启时使用，`ProxyServer` 为 `host:port` 时作为 HTTP 代理，为 `http=…;https=…;socks=…` 时按请求协议选择、其次用 `socks` 项（SOCKS5），`ProxyOverride` 中的主机与 `<local>`（不含点的主机名）直连，回环地址始终直连。

### 账户目录

`AUTH_DIR` 下每个账户一个子目录，目录名为邮箱：

| 文件 | 内容 |
| --- | --- |
| `account.json` | 账户资料与设备档案 |
| `token.json` | `oauth2.Token` JSON：access token、refresh token、到期时间与 `id_token` 等附加字段 |

`account.json` 字段：

| 字段 | 内容 |
| --- | --- |
| `id`、`email` | 邮箱 |
| `label` | 备注 |
| `proxy` | 账户代理，空为跟随全局 |
| `project` | `loadCodeAssist` 的 `cloudaicompanionProject`，空表示待走登录序列 |
| `tier`、`paid_tier` | `currentTier.id` 与 `paidTier.id` |
| `region` | `fetchUserInfo` 的 `regionCode` |
| `device` | 设备档案 |
| `enabled` | 是否参与调度 |
| `priority` | 调度优先级，越大越先使用，为 0 时省略 |
| `added_at` | 添加时间，账户按它排序 |

设备档案在账户首次加入时生成，之后保持不变：

| 字段 | 取值 |
| --- | --- |
| `os`、`arch` | 生成时运行平台的 `GOOS`、`GOARCH` |
| `user_name` | 邮箱本地部分中的字母与数字 |
| `app_data_dir` | Windows `C:\Users\<用户名>\.gemini\antigravity`，macOS `/Users/<用户名>/.gemini/antigravity`，其余 `/home/<用户名>/.gemini/antigravity` |
| `time_zone` | 本机 IANA 时区名（取 `TZ` 或 `/etc/localtime` 链接），都没有时为 `±hh:mm` |
| `installation_id` | 随机 UUID |

档案决定账户的 User-Agent 系统与架构、系统提示中的 OS、路径与分隔符、user 轮本地时间的时区、unleash `instanceId` 与主机名（Windows 为 `DESKTOP-` 加 `installation_id` SHA-256 的 Base32 前 7 位，macOS 为 `<用户名>s-MacBook-Pro.local`，Linux 为 `<用户名>-pc`）。

两个文件都经同目录临时文件原子替换写入，目录权限 0700。

### 运行目录

| 路径 | 内容 |
| --- | --- |
| `requests.db` | 请求账本（SQLite，WAL） |
| `logs/antigravity2api.log` | 服务日志，轮转文件在同目录 |
| `logs/crash.log` | 未恢复的 panic 与致命错误 |
| `files/<ID>`、`files/<ID>.json` | Files API 上传的内容与元数据 |
| `conversations/<cascade_id>.json` | 会话记录，空闲 24 小时后删除 |
| `catalogs/<账户>.json` | 该账户最近一次读到的模型目录条目 |
| `hub/<账户>.json` | 虚拟 Hub 状态：该账户经服务开始过的会话数 |
| `.updaterId` | 版本检查使用的更新器标识 |
| `webview/` | 桌面版 WebView 数据 |

运行目录中的 JSON 文件：

| 文件 | 字段 |
| --- | --- |
| `conversations/<cascade_id>.json` | `cascade`、`trajectory`、`length`、`digest`、`answer`、`times`、`prompt`、`signatures`、`origins`、`used` |
| `prompt` | `model`、`length`、`digest`、`contents`、`tokens` |
| `catalogs/<账户>.json` | 目录条目数组，每项为 `fetchAvailableModels` 的条目字段加 `id`、`agent`、`kind` |
| `hub/<账户>.json` | `{"conversations": <会话数>}` |

- 会话记录：`length` 与 `digest` 为最近一次请求在摘要链中的位置与该处摘要，`answer` 为该次成功生成的回答指纹，`times` 为各条消息首次出现的 Unix 秒，`prompt` 为最近一次成功生成的模型、消息条数、该处链摘要、上游 contents 条数与上游 `promptTokenCount`，`signatures` 为函数调用 ID 到思考签名，`origins` 为签名的 SHA-256 到生成它的模型族，`used` 为最近使用时间。写入时先写同目录临时文件再替换；启动时读入全部记录，任一文件无法解析时服务启动失败
- 摘要链：`d0 = hex(SHA-256({"Session","System","Tools"} 的 JSON))`，`di = hex(SHA-256(d(i-1) 的十六进制文本 + 第 i 条规范消息的 JSON))`，JSON 为 Go `encoding/json` 对规范类型的编码；回答指纹为 `hex(SHA-256(非思考正文按空白归一 + NUL 字符 + 各函数调用的“函数名 + 按键排序的参数 JSON”以 NUL 字符 相连))`
- 目录文件无法解析时跳过该账户

## 4. 账户生命周期与调度

### 添加账户

`login`、`import`、远程配对与管理端登录都把 OAuth token 交给账户库：邮箱取自 `id_token` 载荷，没有 `id_token`（官方客户端保存的 token）时以该 token 请求 userinfo；新账户生成设备档案并启用，已有账户只更新 token。`import` 依次读取系统凭据库（服务 `gemini`、用户 `antigravity`）与 `~/.gemini/jetski-standalone-oauth-token`，取 JSON 的 `token` 字段。

运行中的服务每 2 秒重新读取账户目录：其他进程（`login`、`import` 或复制进 `auth/` 的目录）加入的账户、refresh token 与内存不同的账户与被删除的账户目录反映到账户库，有变化时后台刷新账户池。

### 切换官方客户端账户

`switch <邮箱>` 与 `POST /api/accounts/{id}/switch` 把账户库中的账户交给本机官方客户端，方向与 `import` 相反：

1. 找到官方客户端：Windows 为 `antigravity://` 协议登记的可执行文件，macOS 为 `/Applications/Antigravity.app`，Linux 为 PATH 中 `antigravity` 指向的文件
2. 结束官方客户端目录内的全部进程（Windows 按可执行文件路径，macOS 与 Linux 按命令行）并等待退出；Windows 上记下主进程（没有 `--type=` 参数的 `Antigravity.exe`）的启动参数
3. 把 token 写成官方客户端登录后保存的 JSON `{"token":{access_token,token_type,refresh_token,expiry},"auth_method":"consumer"}`：`~/.gemini/cache/antigravity-keyring-unavailable` 的修改时间在 1 小时内时写 `~/.gemini/jetski-standalone-oauth-token`，系统凭据库（服务 `gemini`、用户 `antigravity`）已有可读凭据时一并更新（官方客户端优先读取它）；标记不在 1 小时内时只写系统凭据库；系统凭据库不可用（如 Linux 没有 Secret Service）时与官方客户端一样写入该标记，并写 `~/.gemini/jetski-standalone-oauth-token`
4. 以记下的启动参数启动官方客户端，language_server 启动时读取新 token

### 初始化

服务启动后与账户变化后在后台刷新账户池，同一时间只运行一次，进行中再次请求时合并为下一次：

1. 按添加时间逐个处理启用账户，跳过已初始化且代理与 refresh token 都未变的账户；相邻两次初始化之间随机间隔 1–3 秒
2. 每个账户以设备档案与代理创建上游客户端，读取 `RUNTIME_DIR/hub/<账户>.json`，立即开始 unleash 与 clearcut 上报循环，0.6–0.8 秒后初始化
3. `project` 为空或 refresh token 已变的账户走登录序列，其余走冷启动序列（序列见协议参考）；`fetchUserInfo` 显示遥测开启时发 `setUserSettings` 关闭
4. 成功时写回 `project`、`tier`、`paid_tier`、`region`，以目录的公开条目加入账户池并保存到 `RUNTIME_DIR/catalogs/<账户>.json`，记录额度快照并开始周期请求；失败时停止该账户的伴随请求，记下原因并移出调度
5. 停用与已删除的账户移出调度，停止周期请求

账户池顺序为初始化完成的顺序。周期请求每 6 轮重读目录，更新账户池中的条目与保存的目录。公开模型目录是账户池中各账户目录的合集，再加上尚未就绪的启用账户保存的目录，服务重启后账户初始化完成前即列出全部模型；账户初始化后首次成功生成后读取一次额度摘要。每个账户保留最近一次读到的额度摘要及读取时间：刷新账户池时，还没有读取的账户取请求账本中 7 天内最近一次额度快照；冷启动初始化不读额度，沿用最近一次读取；账户出错或移出调度后保留。

管理端“验证”同步重新初始化单个账户（`project` 为空时走登录序列），需要验证或初始化失败的账户由此恢复调度。

### 账户状态

| 状态 | 条件 |
| --- | --- |
| `disabled` | 已停用 |
| `error` | 初始化失败或被移出调度，带原因 |
| `pending` | 启用但尚未完成初始化 |
| `cooling` | 至少一个额度组或模型在冷却 |
| `ready` | 可调度 |

冷却包括调度记下的冷却与额度耗尽：额度摘要中该组的桶或目录 `quotaInfo` 的 `remainingFraction` 不大于 0 时，到 `resetTime` 前视为冷却。

### 选择账户

1. 解析模型名（规则见协议参考“模型 ID 与思考档位”），没有就绪账户时返回 503，正文列出各账户的失败原因
2. 候选账户：目录中有该模型、未被本次请求排除、该模型未冷却；冷却截止取额度组冷却、模型冷却与额度耗尽重置时间中最晚者
3. 没有候选时返回 `CooldownError`（429），`Retry-After` 为最早恢复时刻；之前已有上游失败时返回那次失败
4. 会话粘连：同一会话（会话记录的 `cascade_id`）一小时内沿用上次的账户，该账户有空闲槽位时直接使用；粘连每分钟清理一次过期项
5. 在有空闲槽位的候选中取 `priority` 最大的一组；模型在 `PREFER_LOWER_TIER_MODELS` 中（按目录 ID 或去掉 `-low`、`-medium`、`-high` 后缀的族 ID 匹配）时，再取其中套餐等级最低的一组。套餐等级按 `paid_tier`，没有时按 `tier`：ID 含 `ultra` 为 Ultra，含 `pro` 为 Pro，其余为 Free
6. `fill-first`：其中账户池顺序的第一个；`round-robin`：其中进行中请求最少者，并列时取最近一次被选中最早者，从未被选中的最先
7. 候选都没有空闲槽位时等待，直到槽位释放、账户池变化或请求超时

单账户并发上限为 `ACCOUNT_CONCURRENCY`，调度策略为 `ROUTING_STRATEGY`，每个请求读取当前值。

### 失败与换号

上游返回 401 时丢弃 access token，在同一账户上重发一次。仍失败时按原因处理账户，再决定是否换账户；一个请求最多尝试 2 个账户，响应流开始后的错误只处理账户、不换号：

| 失败 | 判定 | 账户处理 | 换账户 |
| --- | --- | --- | --- |
| 认证失败 | token 刷新被拒，或重发后仍为 401 | 移出调度 | 是 |
| 需要验证 | 403，`VALIDATION_REQUIRED` 或消息含 `verify your account` | 移出调度，保持启用 | 是 |
| 违反条款 | 403，`TOS_VIOLATION` 或消息含 `terms of service` | 写为停用 | 是 |
| 额度耗尽 | `QUOTA_EXHAUSTED` | 额度组冷却到恢复时刻，同额度池账户一起冷却 | 是 |
| 容量不足 | `MODEL_CAPACITY_EXHAUSTED` | 不冷却 | 是 |
| 请求内容 | 429，没有 ErrorInfo 与恢复时刻 | 不冷却 | 否 |
| 限速 | 其他 429 | 该模型冷却到恢复时刻，没有时 1 分钟 | 是 |
| 内部错误 | 500 `INTERNAL` | 该模型冷却 n 分钟，n 为连续次数 | 是 |
| 连接错误 | 非上游错误响应 | 不冷却 | 是 |
| 其他 | 其余上游错误 | 不冷却 | 否 |

- 恢复时刻依次取 RetryInfo `retryDelay`、ErrorInfo `metadata.quotaResetDelay` 与 `quotaResetTimeStamp`；额度耗尽都没有时取目录 `quotaInfo.resetTime`，再没有时 1 分钟
- 连续 INTERNAL 次数在该账户下一次成功生成后清零
- 移出调度的原因显示在管理端；需要验证时正文中的 `validation_url` 作为验证链接

### 共享额度池

家庭方案的成员账户共享同一组额度桶。某账户额度组耗尽时，额度摘要中该组每个窗口的重置时间都与它相同的其他账户视为同额度池，一起冷却到同一时刻。

## 5. 管理 API 与管理端

管理 API 挂在 `/api/`，与公开 API 共用监听：

- 只接受同源请求：`Sec-Fetch-Site: cross-site` 或 `Origin` 与 `Host` 不一致时返回 403 `admin_origin_forbidden`
- 本机回环访问免登录：对端与 `Host` 都是回环地址，且没有 `X-Forwarded-For`、`Forwarded`、`X-Real-IP`
- 远程访问需要 `ADMIN_AUTH_ENABLED=true`，否则返回 401 `admin_remote_disabled`；未登录返回 401 `admin_login_required`
- 会话 Cookie `antigravity_admin`：12 小时，`Path=/api`、HttpOnly、SameSite=Strict，经 TLS 或 `X-Forwarded-Proto: https` 时带 Secure；会话结束时取消该会话的进行中管理请求
- 同一客户端 IP 一分钟内登录失败 5 次后返回 429 `login_rate_limited`，`Retry-After: 60`
- 响应带 `Cache-Control: no-store`；错误体为 `{"error":{"code","message"}}`；未知路由返回 404 `not_found`

| 方法与路径 | 作用 |
| --- | --- |
| `GET /api/session` | 是否开启登录、当前请求是否可访问 |
| `POST /api/session` | 用户名与密码登录 |
| `DELETE /api/session` | 退出登录 |
| `GET /api/status` | 宿主、版本、监听地址、启动时间、模拟的客户端版本、官方最新版本 |
| `GET /api/accounts` | 全部账户 |
| `PATCH /api/accounts/{id}` | 启用、停用或设置账户代理 |
| `DELETE /api/accounts/{id}` | 删除账户目录 |
| `POST /api/accounts/{id}/verify` | 重新初始化 `error` 状态的账户，其他状态返回 409，初始化失败返回 502 |
| `POST /api/accounts/import` | 导入本机官方客户端账户 |
| `POST /api/accounts/{id}/switch` | 把账户写入本机官方客户端并重新启动它，返回 `{"account": "<邮箱>"}`；账户不存在返回 404，官方客户端未找到、无法结束或凭据写入失败返回 502 |
| `POST /api/accounts/refresh` | 同步刷新账户池 |
| `POST /api/logins` | 开始登录，`mode` 为 `browser` 或 `code` |
| `POST /api/logins/{id}/code` | 提交授权码或回调地址，等到登录结束 |
| `DELETE /api/logins/{id}` | 取消登录 |
| `POST /api/pairing` | 生成远程配对令牌 |
| `POST /api/pairing/accounts` | 凭配对令牌导入 token，不经管理会话 |
| `GET /api/models` | 公开模型目录，每项的 `accounts` 为目录含该模型的就绪账户数，别名取目标模型的值 |
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

同一监听上的其他入口：`GET /readyz` 在至少一个启用账户就绪时返回 200 `{"status":"ready"}`，否则 503 `{"status":"not_ready"}`；`/v1/`、`/v1beta/`、`/upload/` 为公开 API；其余路径为管理端静态资源。全部响应带 `X-Frame-Options: DENY`、`Content-Security-Policy: frame-ancestors 'none'`、`X-Content-Type-Options: nosniff` 与 `Referrer-Policy: no-referrer`。

### 账户与登录

`GET /api/accounts` 的每个账户：

| 字段 | 内容 |
| --- | --- |
| `id` | 邮箱 |
| `state` | 见“账户状态” |
| `tier` | 有付费套餐时为 `paid_tier`，否则 `tier` |
| `project` | 上游项目 |
| `proxy` | 账户代理，密码替换为 `xxxxx` |
| `added_at`、`token_expiry` | 添加时间、access token 到期时间 |
| `priority` | 调度优先级 |
| `active` | 进行中请求数 |
| `error`、`verify_url` | 停止调度的原因、验证链接 |
| `cooldown` | 冷却中的额度组或模型 ID 到截止时间 |
| `quota`、`quota_at` | 最近一次读到的额度桶（`bucket_id`、`window`、`remaining_fraction`、`reset_time`）及读取时间，尚未读取时 `quota` 为空数组、省略 `quota_at` |
| `shared` | 按额度组（`gemini`、`3p`）列出与该账户共享额度池的其他就绪账户（规则见“共享额度池”），没有时省略 |

`PATCH /api/accounts/{id}` 接受 `{"enabled": bool, "proxy": "<URL>", "priority": int}` 中的任意几项，`proxy` 为空串时跟随全局；保存后后台刷新账户池，代理变化的账户重新初始化。

管理端登录：`POST /api/logins` 在后台开始一次 OAuth 登录（5 分钟内完成），取得授权链接后返回 `{id, mode, url, state: "pending", expires_at}`。`code` 模式提交页面给出的授权码或带 `code` 参数的地址；`browser` 模式在服务所在机器的回环端口等待回调，浏览器不在同一台机器时把地址栏中的回调地址提交给 `/code`，服务转交给回环监听（`code` 或 `state` 不一致时返回 400）。结束状态为 `completed`（带 `account`）、`failed`、`canceled`，经事件流推送；成功后后台刷新账户池。

远程配对：`POST /api/pairing` 生成 10 分钟有效的令牌，同时只有一个有效令牌，服务只保存其 SHA-256；`login --remote <地址> --token <令牌>` 在本机登录后把 token JSON 以 `Authorization: Bearer <令牌>` POST 到 `/api/pairing/accounts`，成功返回 201 `{"account": "<邮箱>"}` 并使令牌失效。

### 设置与系统提示预览

`GET /api/settings` 返回 `{values, admin_password_set, restart_required}`：`values` 为除 `ADMIN_PASSWORD` 外各项的 `.env` 文本值，`restart_required` 为保存值（叠加启动参数）与启动值不同的 `LISTEN`、`AUTH_DIR`、`RUNTIME_DIR`。`PATCH /api/settings` 接受 `{"values": {键: 文本值}}`，校验失败返回 400 `invalid_settings`。

`GET /api/prompt/preview?tier=&sections=&wrap=` 以占位数据渲染档位，返回 `{system, user, sections}`：`system` 为系统指令，其中客户端系统提示为 `<client system prompt>`、会话 ID 为 `<conversation-id>`、`AppDataDir` 为 `C:\Users\user\.gemini\antigravity`、系统为 `windows`、模型名为 `<model>`；`user` 为首个 user 轮 `<user message>` 包装后的文本；`sections` 为 10 个官方段的段名与渲染后的原文。省略的参数取当前配置，`sections` 与 `wrap` 只用于 `custom`，无效时返回 400 `invalid_prompt`。

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
| `level`、`source`、`request_id`、`model`、`q`、`limit`、`before` | 日志 | `level=error` 只看 ERROR，页大小为正整数，默认 100 |
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

### 管理端页面

| 页面 | 内容 |
| --- | --- |
| 仪表盘 | 首次使用三步引导、账户与额度总览、推荐账户、最近请求、额度变化 |
| 账户 | 添加、刷新、筛选、分页、额度与重置倒计时、家庭组、验证、启停、详情、调度优先级、切换官方客户端、代理、删除 |
| 接入 | 三种协议的 base URL、API key 管理、客户端配置片段 |
| 模型 | 额度组、上下文与输出上限、思考档位、可用账户数、模型映射 |
| 请求 | 请求记录、用量、日志 |
| 设置 | 服务、网络、系统提示（各档原文、自定义档的段落与包装）、调度（含优先用低套餐账户的模型）、日志、远程访问、关于 |

#### 路由与登录

前端源码在 `web/src`，路由定义在 `app/router.tsx`，页面按路由拆分加载。除 `/login` 外，页面都在外壳 `app/Layout.tsx`（顶栏与内容区）内。

| 路径 | 页面 | 查询参数 |
| --- | --- | --- |
| `/login` | 登录 | `next` |
| `/` | 仪表盘 | — |
| `/accounts` | 账户 | `state`、`q`、`page`、`account` |
| `/connect` | 接入 | `client` |
| `/models` | 模型 | `group`、`q` |
| `/requests` | 请求记录 | `range`、`state`、`protocol`、`model`、`account`、`q`、`cursor`、`id` |
| `/requests/usage` | 用量 | `range`、`by` |
| `/requests/logs` | 日志 | `view`、`level`、`source`、`q`、`range`、`request_id` |
| `/settings/:分区` | 设置 | — |
| 其余路径 | 页面不存在 | — |

设置分区为 `service`、`network`、`prompt`、`scheduling`、`logging`、`remote`、`about`，路径中没有或不认识的分区显示 `service`。

- 外壳先读 `GET /api/session`，`authenticated` 为 false 时以替换历史的方式转到 `/login?next=<原路径与查询>`
- 任一查询返回 401 时重新读取会话，会话无效即转到登录页；查询遇到 5xx 时重试 2 次，4xx 与网络错误不重试
- 登录页：`enabled` 为 false 时只显示开启方法；提交 `POST /api/session`，401 显示用户名或密码错误，429 显示一分钟后再试；成功后转到 `next`（不以 `/` 开头时转到 `/`），已登录时直接转到 `next`
- 顶栏导航为仪表盘、账户、接入、模型、请求、设置；仪表盘只在路径为 `/` 时高亮，其余按路径前缀高亮；“退出登录”只在开启登录且已登录时显示，`DELETE /api/session` 后转到 `/login`
- 登录后订阅 `/api/events`：连接打开时清空日志与进行中请求的缓存；`accounts` 替换账户列表，`log` 追加日志（页面保留最近 2000 条），`request` 维护进行中请求并在请求结束时刷新请求记录，`login` 更新对应的登录；断开时内容区顶部显示断线提示，浏览器自动重连并重新收到初始快照

#### 添加账户

添加账户对话框有浏览器登录、粘贴授权码、导入官方客户端、远程配对四种方式。打开时的默认方式：桌面窗口，或页面主机为 `localhost`、`127.0.0.1`、`[::1]` 时为浏览器登录，其余为粘贴授权码；从仪表盘引导的方式卡片打开时选中该方式。

| 方式 | 流程 |
| --- | --- |
| 浏览器登录 | `POST /api/logins` `mode=browser` → 登录链接与倒计时 → 等待回调或提交回调地址 → 结束 |
| 粘贴授权码 | `POST /api/logins` `mode=code` → 登录链接与倒计时 → 提交授权码 → 结束 |
| 导入官方客户端 | `POST /api/accounts/import` → 导入数量 |
| 远程配对 | `POST /api/pairing` → 配对命令与倒计时 → 过期后重新生成 |

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

- “添加 API key”对话框预填随机值：`sk-` 加 32 位字母与数字，取自 `crypto.getRandomValues`；可重新生成或改为自定义值
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

- 可选模型为实时目录中的对话模型，不含生图、语音转写与代码补全模型；目录为空时 `<model>` 与 `<small>` 写成 `<模型 ID>`
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

#### 额度与推荐账户

| 量 | 计算 |
| --- | --- |
| 可用比例 | 一个额度组 5 小时与每周 `remaining_fraction` 中较小的一项 |
| Gemini、Claude 与 GPT 额度 | 已启用账户可用比例的平均 |
| 5 小时、每周均值 | 已启用账户该窗口剩余比例的平均 |
| 低额度账户 | 已启用且任一额度桶低于 20% 的账户数 |
| 就绪账户 | 就绪数 / 总数，说明中列出其余状态的数量 |
| 24 小时请求 | 近 24 小时请求数、失败数与估算费用 |
| 推荐账户 | 每个额度组中可用比例最高的账户 |

- 已启用指 `state` 不是 `disabled`；计算额度均值时，同一组的 `shared` 成员只取列表中第一个出现的账户，没有该组额度的账户不计入
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
- 不认识或缺省的取值使用默认值：账户状态全部，模型额度组全部，请求记录范围 `24h`，用量范围 `7d`、分组 `model`，日志视图 `live`、级别全部，已保存日志范围 `24h`；用量页之外的默认值不写入 URL
- 筛选变化替换当前历史记录；请求记录翻页与打开详情新增历史记录，其余筛选变化同时清除 `cursor`
- 请求记录与已保存日志的时间范围截止到当前时间之后 1 秒；实时日志在页面内按最低级别、来源与关键词（匹配消息与来源，不区分大小写）筛选

| 页面 | 参数取值 |
| --- | --- |
| 账户 | `state`：`ready`、`cooling`、`verify`、`error`、`disabled` |
| 模型 | `group`：`gemini`、`3p` |
| 请求记录 | `range`：`1h`、`24h`、`7d`、`30d`；`state`：结果；`protocol`：四种公开协议 |
| 用量 | `range`：`24h`、`7d`、`30d`、`90d`；`by`：`model`、`api_key`、`account`、`day` |
| 日志 | `view`：`live`、`saved`；实时 `level`：`INFO`、`WARN`、`ERROR`；已保存 `range`：`24h`、`7d`、`30d`，`level=ERROR` |

#### 数字与时间格式

数字与时间用 `Intl` 按界面语言的区域格式化（简体中文 `zh-CN`、繁体中文 `zh-TW`、英文 `en`），时间按浏览器时区显示，全站使用等宽数字。

| 类型 | 格式 |
| --- | --- |
| 整数 | 千分位 |
| 大数 | 紧凑写法，最多 1 位小数 |
| token 上限 | 1024 的整数倍按 1024 进制，其余按 1000 进制，写成 K、M |
| 百分比 | 最多 1 位小数 |
| 费用 | 美元；大于 0 且小于 1 时保留 4 位有效数字，其余最多 2 位小数 |
| 时长 | 不足 1 秒为整数毫秒；不足 10 秒为秒且 2 位小数；不足 1 分钟为秒且 1 位小数；不足 1 小时为分钟；其余为小时 |
| 字节 | 1024 进制，字节取整，KB 起 1 位小数 |
| 相对时间 | 秒、分、时、天逐级进位 |
| 日期时间 | 当天只显示时分秒，其余加月日，24 小时制 |
| 完整时间 | 中等长度的日期与时间，24 小时制 |

#### 语言、配色与快捷键

- 语言：简体中文 `zh`、繁体中文（台湾）`zh-TW`、英文 `en`，保存在 `localStorage` 的 `antigravity2api.language`；没有保存值时依次查看浏览器的语言列表，第一个以 `zh` 开头的标签中 `zh-TW`、`zh-HK`、`zh-MO`、`zh-Hant` 开头的选繁体中文，其余选简体中文，先遇到 `en` 开头的标签时选英文，都没有时选英文；`html` 的 `lang` 同步为对应区域
- 每份文案以 `defineMessages(简体中文, 英文, 繁体中文)` 声明，三种语言的键由类型检查保证一致
- 配色：跟随系统、浅色、深色；浅色与深色保存在 `localStorage` 的 `antigravity2api.theme`，跟随系统时删除该键；结果写入 `html` 的 `data-theme`，跟随系统时随系统配色变化切换；页面渲染前先应用

| 按键 | 作用 | 生效位置 |
| --- | --- | --- |
| `/` | 聚焦本页搜索框 | 账户、模型、请求记录、日志；焦点不在输入框且未按 Ctrl、⌘、Alt |
| Ctrl + S、⌘ + S | 保存设置 | 设置页 |
| Tab | 首个焦点为“跳到主要内容” | 全部页面 |

#### 其他

- 桌面版设置页的远程访问默认折叠，相关字段校验出错时展开
- 管理端从 `/api/events` 接收账户、日志、请求与登录事件。静态资源中 `assets/` 下带哈希的文件长期缓存，其余文件与未知页面路径返回不缓存的入口页。

### 官方版本检查

服务启动 10 秒后读取一次官方客户端的更新清单，此后每小时一次，结果只保存在内存中，`/api/status` 的 `latest_version` 高于 `client_version` 时管理端提示有新版本。请求形态见 [逆向指南](reverse-engineering.md) 第 4 节。

## 6. 命令行版与桌面版

| 项 | 命令行版 | 桌面版 |
| --- | --- | --- |
| 构建 | 默认 | 标签 `desktop,production` |
| 工作目录 | 启动时的当前目录 | Windows 为可执行文件所在目录（便携版）；macOS 与 Linux 为用户配置目录下的 `Antigravity2API`；启动时创建并进入 |
| 不带参数启动 | `serve --open-ui` | 打开桌面窗口 |
| 子命令 | 支持 | 支持，Windows 上接回父进程控制台 |
| 管理端 | 系统浏览器 | 无边框 WebView 窗口，`?shell=desktop` |
| 宿主标识 | `/api/status` 的 `host` 为 `web` | `desktop` |

命令行版不带任何参数启动时等同 `serve --open-ui`：服务就绪（`/api/status` 可访问）后用系统浏览器打开管理端，Windows 经 `rundll32 url.dll,FileProtocolHandler`，macOS 经 `open`，其余经 `xdg-open`。

桌面版：

- 单实例：再次启动时唤起已有窗口并退出
- 服务开始监听后显示窗口，服务启动失败时以原生对话框显示原因并退出；Windows 上配置错误等启动前的错误以系统消息框显示
- 窗口 1280×800，最小 960×600；Windows 与 macOS 关闭窗口时隐藏到托盘，Linux 关闭窗口即退出
- 托盘菜单：显示窗口、在浏览器中打开、复制 API 地址、开机启动、退出；点击托盘图标显示窗口
- 带 `--hidden` 启动时只显示托盘；开机启动项是否带它见下表
- 页面中的外部链接经系统浏览器打开；Windows 上 WebView 数据保存在 `RUNTIME_DIR/webview`

窗口地址为 `http://<监听地址的回环形式>/?shell=desktop`。启动时服务在后台协程运行，桌面壳每 50ms 尝试连接监听地址，连上后才运行窗口。

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

- `desktop:open:` 与 `desktop:window:` 由 `RawMessageHandler` 按前缀 `openPrefix`、`windowPrefix` 处理，窗口动作取自 `windowActions`，其他动作名忽略；关闭窗口在 Windows 与 macOS 上隐藏到托盘
- 注入脚本：`target="_blank"` 或跨源链接的点击、`window.open` 都改为 `desktop:open:`，相对地址按页面地址补全；在计算样式 `--wails-draggable` 为 `drag` 的元素上按下左键并移动时发 `wails:drag`，双击时发 `desktop:window:toggle-maximise`；距窗口边缘 5px、角落 15px 内按下左键并移动时发缩放消息，鼠标经过边缘时显示对应光标；macOS 与窗口最大化时不做边缘缩放
- 最大化同步：每次窗口尺寸变化与页面加载后，桌面壳执行 `window.dispatchEvent(new CustomEvent("desktop:maximised", {detail: <是否最大化>}))`（`maximisedEvent`）；注入脚本据此停用边缘缩放，顶栏按钮在“最大化 / 还原”之间切换

开机启动由托盘菜单开关，标识为 `com.mag1cfall.antigravity2api`（`enableAutostart`）：

| 平台 | 注册方式 | 保存位置 | 启动参数 |
| --- | --- | --- | --- |
| Windows | 注册表 Run 项 | `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 下同名值 | `--hidden` |
| macOS 13 及以上的 `.app` | SMAppService 登录项 | 系统设置的登录项 | 无 |
| macOS 其他情况 | LaunchAgent | `~/Library/LaunchAgents/com.mag1cfall.antigravity2api.plist` | `--hidden` |
| Linux | XDG autostart | `$XDG_CONFIG_HOME/autostart/com.mag1cfall.antigravity2api.desktop`，默认 `~/.config` | 无 |

## 7. 测试

```bash
go test ./...
go vet ./...
```

| 包 | 单元测试内容 |
| --- | --- |
| `config` | `.env` 往返、环境变量覆盖、校验、启动参数覆盖、可信代理、系统代理解析 |
| `balancer` | 按目录选账户、最久未选中者轮换、等待槽位、冷却、会话粘连、排除账户、目录合并 |
| `app` | 远程管理需要登录、客户端 IP、验证链接、代理脱敏 |
| `requestdb` | 记录查询与详情、正文保留条数、过期清理、汇总与记录一致、日志查询、结果判定、上游错误读取、费用 |
| `service` | 流中错误原样返回、客户端取消后排空上游 |
| `api` | 空消息过滤、Gemini 输出 part 与签名、`inlineData` 兼容、API key 匹配 |


修改协议实现时，先对照协议参考中的官方现场形态，再在本地服务上用对应 SDK 发请求核对输出。
