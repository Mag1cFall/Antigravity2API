# Antigravity2API

[简体中文](README.md) | [English](README_en.md)

将 Google Antigravity 的 Agent 模型转换为 OpenAI、Claude 和 Gemini 兼容 API。

<table>
  <tr>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/dashboard-dark.webp"><img src="docs/images/dashboard-light.webp" alt="仪表盘"></picture><br><b>仪表盘</b><br>额度与请求概览</td>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/accounts-dark.webp"><img src="docs/images/accounts-light.webp" alt="账户"></picture><br><b>账户</b><br>额度、状态与设备</td>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/connect-dark.webp"><img src="docs/images/connect-light.webp" alt="接入"></picture><br><b>接入</b><br>客户端配置</td>
  </tr>
  <tr>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/models-dark.webp"><img src="docs/images/models-light.webp" alt="模型"></picture><br><b>模型</b><br>实时目录</td>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/usage-dark.webp"><img src="docs/images/usage-light.webp" alt="用量"></picture><br><b>用量</b><br>请求与 token</td>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/prompt-dark.webp"><img src="docs/images/prompt-light.webp" alt="系统提示"></picture><br><b>系统提示</b><br>四档原文</td>
  </tr>
</table>

## 特性

- **OpenAI 兼容**：`/v1/chat/completions`、`/v1/completions`、`/v1/responses`、`/v1/models`、`/v1/images`、`/v1/audio`、`/v1/files`
- **Claude 兼容**：`/v1/messages`、`/v1/messages/count_tokens`
- **Gemini 兼容**：`/v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`、Files API、Interactions API
- **官方客户端身份**：以纯 Go HTTP 按 Antigravity 官方客户端的 TLS 与 HTTP 形态、User-Agent、请求信封、初始化顺序与伴随请求访问 Cloud Code，每个账户使用导入时生成的设备档案
- **Agent 模型**：Gemini、Claude 与 GPT-OSS 的全部 Agent 模型，按账户实时目录公开，思考参数映射到同族 Low、Medium、High 思考档位
- **流式输出**：文本、思考与思考签名实时增量返回，工具调用、并行函数调用、图片与 PDF 输入在 OpenAI Chat、OpenAI Responses、Claude Messages 与 Gemini 四个协议间互通
- **图像与语音**：目录中的图像生成模型生成与编辑图片，上述四个协议与 OpenAI Images 接口输出图片；目录中的语音转写模型以官方语音输入的通道转写音频，OpenAI 转写与翻译接口省略 `model` 时使用它，Chat `input_audio` 与 Gemini 音频输入在所选模型为转写模型时由它转写，所选模型为其他模型时音频直接发给该模型
- **文件**：OpenAI、Claude 与 Gemini 的 Files API 把文件保存在 `RUNTIME_DIR/files/`，请求中的文件引用在发送前内联
- **系统提示档位**：全保留、中等、激进与自定义四档，控制官方系统提示与用户消息包装的保留程度
- **多账户调度**：按账户优先级与 Gemini、第三方模型两组额度选择账户，请求在同级账户间轮换，同一会话固定在同一账户；指定的模型可优先使用低套餐账户；额度耗尽冷却到重置时间，限速、容量不足与服务端错误按原因冷却或换号；各账户额度定时刷新，账户页可随时刷新全部或单个账户
- **token 计数**：按模型族本地计算输入 token，GPT-OSS 按官方聊天模板渲染后计数，Gemini 与 Claude 族同一会话以上游实际用量校准
- **管理端与桌面版**：网页管理端提供首次使用引导、仪表盘、账户、客户端接入配置、模型、请求记录、用量与费用、进行中请求、日志与设置，界面支持简体中文、繁体中文（台湾）与英文；Windows、macOS、Linux 桌面版带托盘、单实例与开机启动
- **运行日志与请求账本**：结构化日志按请求记录选号、换号、冷却与上游尝试；SQLite 账本保存每个请求的账户、模型、token、缓存命中与分段耗时，并按官方 API 单价估算费用
- **登录方式**：浏览器登录、粘贴授权码登录、导入本机官方客户端的账户、从另一台电脑配对上传
- **代理**：HTTP、HTTPS、SOCKS5 全局代理与每账户代理，未设置时跟随系统代理

## 支持的模型

| 模型 ID | 名称 | 思考档位 |
| --- | --- | --- |
| `gemini-4-argon` | Gemini 4 Argon | Low、Medium、High |
| `gemini-3.8-flash` | Gemini 3.8 Flash | Low、Medium、High |
| `claude-opus-5-5` | Claude Opus 5.5 | Low、Medium、High |
| `claude-sonnet-5-5` | Claude Sonnet 5.5 | Low、Medium、High |
| `gpt-oss-120b` | GPT-OSS 120B | Medium |

服务启动时读取各账户的 Antigravity 模型目录并保存到 `RUNTIME_DIR/catalogs/`，运行后以 `/v1/models` 为准：

- 上表是官方客户端模型选择器中的 Agent 模型，每个模型的条目带 `thinking_levels` 与 `default_thinking_level`
- 目录中的图像生成模型（例如 `gemini-3.1-flash-image`）与语音转写模型（例如 `proactive-observer-v10`）随后列出，能力分别标注 `image_generation` 与 `transcription`
- 目录中其他可用条目以目录 ID 公开，例如 Pro 账户目录中的 `claude-sonnet-4-6` 与 `claude-opus-4-6-thinking`
- 重启后账户初始化完成前，`/v1/models` 列出上一次保存的目录；只在初始化未完成的账户目录中的模型返回 503
- 各模型的上下文窗口、输出上限与能力取自目录，账户的套餐决定可用型号与额度

思考档位按以下顺序确定：

1. 模型名中指定的档位：`claude-opus-5-5-high` 这类带档位的目录 ID、括号后缀 `claude-opus-5-5(low)`（括号内可写 `none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max` 或思考预算 token 数）、`-thinking` 后缀（High）
2. 请求的思考参数：OpenAI `reasoning_effort` 的 `none`、`minimal`、`low` 为 Low，`medium` 为 Medium，`high`、`xhigh`、`max` 为 High；Anthropic `thinking.budget_tokens` 与 Gemini `thinkingConfig.thinkingBudget` 不超过 1024 为 Low，不超过 8192 为 Medium，更大为 High，`-1` 等负数不指定档位
3. Medium；模型只有一档时取该档

模型名另按以下规则匹配目录：

| 写法 | 结果 |
| --- | --- |
| 带日期版本或 `-latest`，例如 `claude-opus-5-5-20261001` | 去掉后匹配 |
| 带 `models/` 前缀 | 去掉前缀后匹配 |
| `MODEL_MAPPING` 中的别名（可与目录模型同名） | 映射到的模型 |

## 快速开始

### 桌面版

从 [Releases](https://github.com/Mag1cFall/Antigravity2API/releases) 下载对应系统的桌面版，都是解压即用的便携包：

| 系统 | 文件 | 启动 | 数据目录（`.env`、`auth/` 与 `runtime/`） |
| --- | --- | --- | --- |
| Windows | `windows-amd64.zip` | 双击 `antigravity2api.exe` | `antigravity2api.exe` 所在目录，整个目录可以直接复制或移动 |
| macOS | `desktop-<版本>-darwin-universal.tar.gz` | 打开 `Antigravity2API.app` | `~/Library/Application Support/Antigravity2API` |
| Linux | `desktop-<版本>-linux-amd64.tar.gz` 或 `linux-arm64.tar.gz` | 运行 `antigravity2api`，需要系统的 GTK 4 与 WebKitGTK 6.0 | `~/.config/Antigravity2API` |

桌面版在窗口中打开管理端，在管理端的账户页登录 Google 账户或导入本机官方客户端的账户。Windows 系统没有 WebView2 运行时时管理端在默认浏览器中打开。Windows 与 macOS 关闭窗口后留在托盘，Linux 关闭窗口即退出。

### 命令行版

从 [Releases](https://github.com/Mag1cFall/Antigravity2API/releases) 下载命令行版 `<版本>-<系统>-<架构>.tar.gz` 并解压。Windows 直接使用桌面版的 `antigravity2api.exe`，下文的命令在 cmd 或 PowerShell 中写作 `cmd /c start /wait antigravity2api.exe <子命令>`，命令行等程序结束后返回。首次使用先添加账户：

```bash
./antigravity2api login
```

`login` 在系统浏览器中打开 Google 授权页，授权完成后账户保存到 `auth/<邮箱>/`，服务启动时按官方客户端的顺序完成账户初始化。没有浏览器的环境（SSH 登录的服务器）使用授权码登录：

```bash
./antigravity2api login --code
```

命令打印授权链接，在任意设备的浏览器中打开并授权，把页面显示的授权码粘贴回终端。两种方式都等到授权完成才结束，没有时限，按 Ctrl+C 取消。

本机已登录 Antigravity 官方客户端时，可以直接导入它的账户：

```bash
./antigravity2api import
```

`import` 读取官方客户端保存的 token 并导入这一个账户：先读系统凭据库（服务名 `gemini`、用户名 `antigravity`），读不到时再读 `~/.gemini/jetski-standalone-oauth-token`。

反过来，把官方客户端切换到账户库中的某个账户：

```bash
./antigravity2api switch <邮箱>
```

`switch` 结束运行中的官方客户端，把该账户的 token 写入官方客户端保存登录状态的位置，再重新打开官方客户端。

启动服务：

```bash
./antigravity2api serve
```

默认监听 `127.0.0.1:8017`，读取当前目录的 `.env`、`auth/` 与 `runtime/`。命令行版不带任何参数运行时等同于 `serve --open-ui`，服务就绪后用浏览器打开管理端；Windows 的 `antigravity2api.exe` 是桌面版，双击打开桌面窗口。

### 远程服务器

服务部署在另一台机器上时，在服务器的管理端账户页生成配对令牌（10 分钟内有效），然后在有浏览器的电脑上登录并上传：

```bash
./antigravity2api login --remote https://server.example.com --token <配对令牌>
```

本机登录使用浏览器，加 `--code` 时使用授权码。登录完成后 token 只上传到远程服务，账户保存在远程服务的 `auth/` 中，本机的 `auth/` 不变。

也可以在服务器的管理端账户页直接发起授权码登录，或在服务器上执行 `login --code`。

### Docker

```bash
docker run -d --name antigravity2api -p 8017:8017 -v ./data:/data ghcr.io/mag1cfall/antigravity2api:latest
```

`latest` 指向最新的正式版本，也可以用 `v<版本>` 标签指定版本。镜像的工作目录为 `/data`，`.env`、`auth/` 与 `runtime/` 都保存在挂载的目录中，容器监听 `0.0.0.0:8017`。端口对外开放时设置 `API_KEYS`；从容器外打开管理端需要 `ADMIN_AUTH_ENABLED=true` 与 `ADMIN_PASSWORD`。账户用管理端的授权码登录、远程配对、`docker exec -it antigravity2api antigravity2api login --code`，或把其他电脑上的 `auth/<邮箱>/` 复制到 `data/auth/`；运行中的服务在 2 秒内载入新账户。

### 代理

`PROXY` 是全局 HTTP、HTTPS 或 SOCKS5 代理，例如 `socks5://127.0.0.1:1080`、`http://user:pass@127.0.0.1:7890`，登录、token 刷新、账户初始化、生成与伴随请求都经过它。代理出口需要位于 Antigravity 支持的地区。

- 代理池：`PROXY` 中的代理（逗号分隔，管理端按行填写，只填一个时也是）组成代理池，每个账户固定使用账户最少的一个代理；登录与在线更新等不属于某个账户的请求使用第一个代理
- 检测：服务启动时、每 5 分钟、公开 API 请求访问上游遇到连接错误时（连续多次合并为一次）、保存设置时与设置页点“立即检测”时检测各代理，代理不可用时其上的账户改用可用代理中账户最少的一个；设置页显示各代理的状态、延迟与使用它的账户
- 系统代理：`PROXY` 为空时跟随系统代理，即环境变量 `HTTPS_PROXY`、`HTTP_PROXY`、`NO_PROXY`，Windows 上另读取系统代理设置
- 账户代理：单个账户可以在管理端的账户页设置自己的代理，该账户的全部请求改走这个代理，填 `direct` 时直连

## API 端点

### OpenAI 兼容

```text
POST   /v1/chat/completions
POST   /v1/completions
POST   /v1/responses
GET    /v1/responses/{id}
DELETE /v1/responses/{id}
GET    /v1/responses/{id}/input_items
POST   /v1/responses/{id}/cancel
POST   /v1/responses/input_tokens
POST   /v1/responses/compact
GET    /v1/models
GET    /v1/models/{model}
POST   /v1/images/generations
POST   /v1/images/edits
POST   /v1/audio/transcriptions
POST   /v1/audio/translations
POST   /v1/files
GET    /v1/files
GET    /v1/files/{id}
GET    /v1/files/{id}/content
DELETE /v1/files/{id}
```

- 以图像生成模型请求 Chat、Responses 或 Claude 接口时输出图片：Chat 与 Claude 的正文中为 data URL 形式的 Markdown 图片，Responses 为 `image_generation_call`。Responses 的 `image_generation` 工具交给文本模型调用，调用由图像生成模型完成
- `/v1/completions` 以 `prompt` 为光标前、`suffix` 为光标后的文字，由 Tab 补全模型生成光标处的补全
- `/v1/images/generations` 与 `/v1/images/edits` 省略 `model` 时使用目录中的图像生成模型，`size` 换成最接近的宽高比，`quality` 换成 1K、2K 或 4K 分辨率；编辑接口的 `mask` 透明区域为编辑区域
- `/v1/audio/transcriptions` 省略 `model` 或指定转写模型时由转写模型转写 WAV、MP3、FLAC 或 PCM 音频，音频按实时节奏发送，用时约等于音频时长，`response_format` 为 `json` 或 `text`；`/v1/audio/translations` 转写后由目录中的首个文本模型译为英文，指定文本模型时由它转写并翻译

### Claude 兼容

```text
POST   /v1/messages
POST   /v1/messages/count_tokens
GET    /v1/models
```

带 `anthropic-version` 请求头的 `/v1/models` 按 Anthropic 格式返回，带 `anthropic-version` 或 `anthropic-beta: files-api-*` 请求头的 `/v1/files` 按 Anthropic Files API 格式返回。

### Gemini 兼容

```text
POST   /v1beta/models/{model}:generateContent
POST   /v1beta/models/{model}:streamGenerateContent?alt=sse
POST   /v1beta/models/{model}:countTokens
GET    /v1beta/models
GET    /v1beta/models/{model}
POST   /upload/v1beta/files
GET    /v1beta/files
GET    /v1beta/files/{name}
DELETE /v1beta/files/{name}
POST   /v1beta/interactions
GET    /v1beta/interactions/{id}
DELETE /v1beta/interactions/{id}
POST   /v1beta/interactions/{id}/cancel
```

`streamGenerateContent` 不带 `alt=sse` 时返回 JSON 数组。Interactions 的四个端点也可用 `/v1/interactions` 路径。`generationConfig.responseModalities` 含 `IMAGE` 时需要图像生成模型，图片以 `inlineData` part 返回，`imageConfig` 的 `aspectRatio` 与 `imageSize` 原样生效。

### 认证

`API_KEYS` 设置一个或多个公开 API key，请求以 `Authorization: Bearer <key>`、`x-api-key`、`x-goog-api-key` 或 `?key=` 携带；`API_KEYS` 为空时不校验 key，但带 `Origin` 头的浏览器请求只接受来自 localhost 与回环地址的网页，其他网页来源与 `Origin: null` 返回 401 `Invalid API Key`；从网页客户端接入时设置 `API_KEYS`。请求记录与用量按 key 统计。

### 管理端与服务状态

浏览器打开 `http://127.0.0.1:8017/` 进入管理端，页面使用 `/api/*` 接口。本机回环地址访问免登录；设置 `ADMIN_AUTH_ENABLED=true`、`ADMIN_USERNAME` 与 `ADMIN_PASSWORD` 后可从其他设备登录访问。设置页修改的配置写回 `.env`，`LISTEN`、`AUTH_DIR` 与 `RUNTIME_DIR` 重启服务后生效，其余项下一次请求生效。设置页的“关于”显示 GitHub 最新发行版本，确认后下载当前平台的发行包、校验 SHA-256、替换程序并重启到新版本，下载经 `PROXY`；Docker 部署随镜像更新。

## 客户端接入

OpenAI SDK 的 base URL 使用 `http://127.0.0.1:8017/v1`，Anthropic 与 Google GenAI SDK 使用 `http://127.0.0.1:8017`。管理端的接入页按当前地址、API key 与实时模型目录生成 Claude Code、Codex、OpenCode、Cherry Studio 与 curl 的配置，可直接复制。

```bash
curl http://127.0.0.1:8017/v1/chat/completions \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-3.8-flash",
    "messages": [{"role": "user", "content": "Hello"}],
    "reasoning_effort": "medium",
    "stream": true
  }'
```

Claude Code：

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8017
export ANTHROPIC_AUTH_TOKEN=YOUR_API_KEY
export ANTHROPIC_MODEL=claude-opus-5-5
claude
```

Claude Code 后台任务使用的小模型名可用 `MODEL_MAPPING` 映射到目录中的模型，例如 `MODEL_MAPPING=claude-haiku-5-5:gemini-3.8-flash-low`。

Codex（`~/.codex/config.toml`）：

```toml
model = "gemini-3.8-flash"
model_provider = "antigravity"

[model_providers.antigravity]
name = "Antigravity2API"
base_url = "http://127.0.0.1:8017/v1"
env_key = "ANTIGRAVITY2API_KEY"
wire_api = "responses"
```

## 系统提示档位

每个生成请求的系统指令由官方客户端的系统提示与客户端自己的系统提示组成，`SYSTEM_PROMPT` 决定官方部分的保留程度：

| 系统提示档位 | 官方系统提示 | 用户消息 |
| --- | --- | --- |
| `full` | 全部段落，客户端系统提示接在末尾 | 按官方格式包装，带本地时间与模型选择 |
| `medium` | identity、guidelines、communication_style 三段 | 按官方格式包装，带本地时间 |
| `aggressive` | identity 一段 | 原样发送 |
| `custom` | `SYSTEM_PROMPT_SECTIONS` 选中的段落 | 按 `SYSTEM_PROMPT_WRAP` 包装 |

管理端设置页的“查看原文”显示各系统提示档位发给上游的系统指令与首轮用户消息。`custom` 在设置页逐段勾选保留的官方段落，每段可展开原文，再选择用户消息的包装方式。

## 目录结构

```text
cmd/antigravity2api/  # 程序入口与子命令
internal/
  antigravity/        # Cloud Code 客户端：登录、初始化、目录、信封与流解码
  api/                # OpenAI、Claude、Gemini 协议适配
  app/                # HTTP 服务、管理 API 与事件流
  auth/               # 账户文件与官方客户端凭据导入
  balancer/           # 多账户调度与冷却
  config/             # 配置读取、校验与保存
  desktop/            # 桌面窗口与托盘
  logging/            # 服务日志
  proxypool/          # 代理池：账户绑定、检测与改绑
  prompt/             # 系统提示档位与模板
  requestdb/          # 请求账本、用量与费用
  service/            # 生成服务：选号、会话、重试与账户生命周期
  tokencount/         # 本地 token 计数
  webui/              # 内嵌的管理端页面
web/                  # 管理端前端源码
build/                # 桌面版图标与打包配置
docs/                 # 协议、开发与日志文档
```

## 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `LISTEN` | 公开 API、管理 API 与管理端的监听地址 | `127.0.0.1:8017` |
| `API_KEYS` | 公开 API key，逗号分隔多个 | 空 |
| `AUTH_DIR` | 账户目录 | `auth` |
| `RUNTIME_DIR` | 运行数据目录：请求账本 `requests.db`、日志 `logs/`、上传的 `files/`、生成媒体 `media/`、会话记录、模型目录与其他运行状态 | `runtime` |
| `PROXY` | 全局 HTTP、HTTPS 或 SOCKS5 代理，逗号分隔的各项组成代理池，为空时跟随系统代理 | 空 |
| `SYSTEM_PROMPT` | 系统提示档位：`full`、`medium`、`aggressive`、`custom` | `full` |
| `SYSTEM_PROMPT_SECTIONS` | `custom` 档位保留的官方段名，逗号分隔，按官方顺序拼接 | `identity,guidelines,communication_style` |
| `SYSTEM_PROMPT_WRAP` | `custom` 档位的用户消息包装：`full` 官方完整包装、`time` 请求与本地时间、`none` 不包装 | `time` |
| `MODEL_MAPPING` | 客户端模型名到目录模型 ID 的映射，每项写成 `别名:模型 ID`，逗号分隔多项 | 空 |
| `MEDIA_OUTPUT` | 生成媒体的给出方式：`inline` 或 `url` | `inline` |
| `MEDIA_BASE_URL` | 媒体链接使用的服务地址，例如 `https://ai.example.com`；为空时使用每个请求访问的地址，经反向代理时跟随 `X-Forwarded-Proto` | 空 |
| `ACCOUNT_CONCURRENCY` | 单账户同时处理的请求数 | `2` |
| `ROUTING_STRATEGY` | 调度策略：`round-robin` 或 `fill-first` | `round-robin` |
| `PREFER_LOWER_TIER_MODELS` | 优先用低套餐账户的模型 ID，逗号分隔；同一优先级内依次先用 Free、Pro、Ultra 账户 | 空 |
| `REQUEST_TIMEOUT` | 等待首个上游事件与相邻两个上游事件之间的最长时长 | `5m` |
| `QUOTA_REFRESH_INTERVAL` | 后台读取全部账户（含停用账户）额度的间隔，最短 `1m`，`0` 为不自动刷新 | `15m` |
| `QUOTA_RESERVE_PERCENT` | 额度保留比例，0–99 | `0` |
| `WEEKLY_QUOTA_WARMUP` | 自动开始每周额度计时 | `false` |
| `LOG_LEVEL` | 日志级别：`debug`、`info`、`warn`、`error` | `info` |
| `LOG_FORMAT` | 日志格式：`json` 或 `text` | `json` |
| `LOG_FILE` | 同时把日志写入 `RUNTIME_DIR/logs/`，单文件 10 MB 轮转 | `true` |
| `LOG_MAX_TOTAL_MB` | 日志文件总量上限：保留 `LOG_MAX_TOTAL_MB / 10 − 1` 个轮转文件，超出时删除最旧的，不小于 20 | `500` |
| `REQUEST_LOG_BODIES` | 保存请求与响应正文 | `false` |
| `ADMIN_AUTH_ENABLED` | 远程访问管理端时的登录 | `false` |
| `ADMIN_USERNAME` | 管理端用户名 | `admin` |
| `ADMIN_PASSWORD` | 管理端密码 | 空 |

- `MEDIA_OUTPUT` 作用于 Chat Completions、Messages 正文与 Images `response_format: url`：`inline` 以 data URL 内联；`url` 按内容命名保存到 `RUNTIME_DIR/media/`，以本服务的 `/media/<文件名>` 链接给出，该路径不校验 API key
- `ROUTING_STRATEGY`：在优先级最高的账户中，`round-robin` 选进行中请求最少者，并列时选最近一次被选中最早的账户；`fill-first` 先用满一个账户
- `QUOTA_RESERVE_PERCENT`：账户某个额度组的 5 小时或每周剩余比例不高于此百分比时，调度跳过该组，直到该窗口重置；`0` 时只跳过已用尽的组
- `WEEKLY_QUOTA_WARMUP`：每次自动刷新额度后，为每周额度窗口尚未开始计时的就绪账户发一条 `Hi`，开始本周计时；同一账户的每个额度组 6 天内最多一次
- `REQUEST_LOG_BODIES`：保存公开请求与响应正文和成功请求的上游尝试正文，失败请求的上游尝试正文总是保存；各截断到 64 KiB，各保留最近 1000 条

进程环境变量覆盖 `.env` 中的同名项。

## 命令行

| 命令 | 作用 |
| --- | --- |
| `serve` | 运行服务；`--open-ui` 在就绪后打开管理端 |
| `login` | 登录 Google 账户并加入账户库 |
| `import` | 导入本机 Antigravity 官方客户端的账户 |
| `switch <邮箱>` | 把本机官方客户端切换到账户库中的该账户 |
| `accounts` | 读取账户目录，按添加时间列出账户、套餐、启用或停用与添加时间 |
| `status` | 查看运行中服务与各账户的状态 |
| `version` | 打印程序版本 |

- `login` 默认在浏览器中登录；`--code` 粘贴授权码登录；`--remote <地址> --token <令牌>` 把账户上传到远程服务，可与 `--code` 同用；`--cloud --provider <名称>` 在本机以 Continue with Google Cloud 的企业 WIF 方式登录，不与 `--code`、`--remote` 同用
- `status` 经回环地址读取运行中服务的 `/api/status` 与 `/api/accounts`，打印版本、监听地址、启动时间与各账户的状态、套餐和各额度桶剩余比例；本机回环访问免登录

各命令都接受 `--listen`、`--auth`、`--runtime` 与 `--proxy`，分别覆盖 `LISTEN`、`AUTH_DIR`、`RUNTIME_DIR` 与 `PROXY`。命令出错时把原因写到标准错误并以退出码 1 结束，例如服务未运行时 `status` 打印 `服务未在 <LISTEN> 运行: <原因>`；`accounts` 与 `status` 的输出格式见 [开发文档](docs/development.md#子命令输出)。桌面版的可执行文件同样接受这些子命令，不带子命令时打开桌面窗口，`--hidden` 启动后只显示托盘。

## 故障排除

### Windows 端口被系统保留

如果启动时提示 `LISTEN` 配置的端口被占用，任务管理器中又找不到占用进程，可能是 Hyper-V、WSL2 或 Docker 的 NAT 服务保留了端口段。以下命令需要在管理员权限的 PowerShell 或 CMD 中运行。

```powershell
netsh interface ipv4 show excludedportrange protocol=tcp
```

如果 `8017` 落在输出的 `Start Port` 和 `End Port` 范围内，可以修改 `LISTEN`，或重启 WinNAT 服务后再次检查：

```powershell
net stop winnat
net start winnat
```

### 账户需要验证

上游要求账户验证时，管理端账户页显示验证链接，该账户停止接收请求。在浏览器中用该账户打开链接完成验证，再点击账户的“重试”恢复。

## 注意

Antigravity 内部协议随官方客户端更新，模型 ID 以服务启动后返回的动态目录为准。欢迎提 Issue 和 PR。

开发与贡献见 [docs/development.md](docs/development.md)，登录、初始化、请求信封与流式解码见 [docs/protocol.md](docs/protocol.md)，日志与请求账本见 [docs/logging.md](docs/logging.md)，官方客户端的抓包与静态分析方法见 [docs/reverse-engineering.md](docs/reverse-engineering.md)。
