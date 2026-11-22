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
- **Gemini 兼容**：`/v1beta/models/{model}:generateContent`、`:streamGenerateContent`、`:countTokens`、上下文缓存、Files API、Interactions API、Live API
- **官方客户端身份**：以纯 Go HTTP 按 Antigravity 官方客户端的 TLS 与 HTTP 形态、User-Agent、请求信封、初始化顺序与伴随请求访问 Cloud Code，每个账户使用导入时生成的设备档案
- **Agent 模型**：Gemini、Claude 与 GPT-OSS 的全部 Agent 模型，按账户实时目录公开，思考参数映射到同族 Low、Medium、High、XHigh 思考档位
- **流式输出**：文本、思考与思考签名实时增量返回，工具调用、并行函数调用、图片与 PDF 输入在 OpenAI Chat、OpenAI Responses、Claude Messages 与 Gemini 四个协议间互通
- **图像与语音**：
  - 目录中的图像生成模型用于生成与编辑图片，支持上述四个协议与 OpenAI Images 接口输出
  - 目录中的语音转写模型经官方语音输入通道转写音频；OpenAI 转写与翻译接口省略 `model` 时自动选用，Chat `input_audio` 与 Gemini 音频输入在选定转写模型时转写、选定其他模型时直接传入该模型
- **文件**：OpenAI、Claude 与 Gemini 的 Files API 将文件保存在 `RUNTIME_DIR/files/`，请求中的文件引用在发送前内联
- **系统提示档位**：全保留、中等、激进与自定义四档，控制官方系统提示与用户消息包装的保留程度
- **多账户调度**：
  - 按账户优先级与 Gemini、Gemini Argon（账户含独立 Argon 周额度桶时）、第三方模型额度组调度，同级账户间轮换，同一会话固定在同一账户，指定模型可优先使用低套餐账户
  - 额度耗尽冷却至重置时间，限速、容量不足与服务端错误按原因冷却或换号；各账户额度定时刷新，账户页支持随时刷新全部或单个账户
- **token 计数**：按模型族本地计算输入 token，GPT-OSS 按官方聊天模板渲染后计数，Gemini 与 Claude 族同一会话以上游实际用量校准
- **管理端与桌面版**：
  - 网页管理端提供首次引导、仪表盘、账户、接入配置、模型、请求记录、用量与费用、进行中请求、日志与设置；账户详情展示可用模型与共享每周额度的同组账户（通常为同一 Google One 家庭群组）
  - 用量与请求记录支持预设或自定义时间范围与趋势图悬停查看；导出文件在本机访问时保存到所选目录，远程访问时由浏览器下载
  - 支持简体中文、繁体中文（台湾）、英文、日文、韩文、法文与德文，服务日志与错误说明随界面语言显示；Windows、macOS、Linux 桌面版带托盘、单实例与开机启动
- **运行日志与请求账本**：结构化日志按请求记录选号、换号、冷却与上游尝试；SQLite 账本保存每个请求的账户、模型、token、缓存命中与分段耗时，并按官方 API 单价估算费用
- **登录与迁移**：支持浏览器登录、粘贴授权码登录、导入本机官方客户端账户、跨机配对上传，以及账户 JSON 导出与勾选导入（兼容 Antigravity Manager、CLIProxyAPI、sub2api、gcli2api、opencode-antigravity-auth 与 antigravity-claude-proxy）
- **代理**：HTTP、HTTPS、SOCKS5 全局代理与每账户代理，未设置时跟随系统代理

## 支持的模型

| 模型 ID | 名称 | 思考档位 |
| --- | --- | --- |
| `gemini-4-neon` | Gemini 4 Neon | Low、Medium、High、XHigh |
| `gemini-4-carbon` | Gemini 4 Carbon | Low、Medium、High |
| `gemini-4-argon` | Gemini 4 Argon | Low、Medium、High |
| `gemini-4-hydrogen` | Gemini 4 Hydrogen | Low、Medium、High、XHigh |
| `gemini-pro-orchestrator-neon` | Orchestrator (Neon) | — |
| `gemini-pro-orchestrator` | Orchestrator (Carbon) | — |
| `gemini-pro-orchestrator-argon` | Orchestrator (Argon) | — |
| `gemini-3.8-flash` | Gemini 3.8 Flash | Low、Medium、High |
| `claude-opus-5-5` | Claude Opus 5.5 | Low、Medium、High |
| `claude-sonnet-5-5` | Claude Sonnet 5.5 | Low、Medium、High |
| `gpt-oss-120b` | GPT-OSS 120B | Medium |

服务启动时读取各账户的 Antigravity 模型目录并保存到 `RUNTIME_DIR/catalogs/`，运行后以 `/v1/models` 为准：

- 上表是官方客户端模型选择器中的 Agent 模型，带思考档位的条目同时给出可选思考档位与默认档位：OpenAI 格式为 `supported_reasoning_levels` 与 `default_reasoning_level`，Anthropic 格式为 `thinking_levels` 与 `default_thinking_level`，Gemini 格式为 `thinkingLevels` 与 `defaultThinkingLevel`
- 目录中的图像生成模型（例如 `gemini-3.1-flash-image`）与语音转写模型（例如 `proactive-observer-v10`）随后列出，能力分别标注 `image_generation` 与 `transcription`
- 目录中其他可用条目以目录 ID 公开，例如 Pro 账户目录中的 `claude-sonnet-4-6` 与 `claude-opus-4-6-thinking`
- 重启后账户初始化完成前，`/v1/models` 列出上一次保存的目录；只在初始化未完成的账户目录中的模型返回 503
- 各模型的上下文窗口、输出上限与能力取自目录，账户的套餐决定可用型号与额度

思考档位按以下顺序确定：

1. 模型名中指定的档位：`claude-opus-5-5-high` 这类带档位的目录 ID、括号后缀 `claude-opus-5-5(low)`（括号内可写 `none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`、思考预算 token 数，或不指定档位的 `auto`）、`-thinking` 后缀（High）
2. 请求的思考参数：OpenAI `reasoning_effort` 的 `none`、`minimal`、`low` 为 Low，`medium` 为 Medium，`high` 为 High，`xhigh`、`max` 为 XHigh（模型没有 XHigh 档时为 High）；Anthropic `thinking.budget_tokens` 与 Gemini `thinkingConfig.thinkingBudget` 不超过 1024 为 Low，不超过 8192 为 Medium，不超过 16384 为 High，更大为 XHigh（模型没有 XHigh 档时为 High），`-1` 等负数不指定档位
3. Medium；模型只有一档时取该档

模型名另按以下规则匹配目录：

| 写法 | 结果 |
| --- | --- |
| 带 `models/` 前缀 | 去掉前缀后匹配 |
| 带 `[<数字>k]` 或 `[<数字>m]` 上下文窗口后缀，例如 `claude-opus-5-5[1m]` | 去掉后匹配 |
| 带日期版本或 `-latest`，例如 `claude-opus-5-5-20261001` | 去掉后匹配 |
| `MODEL_MAPPING` 中的别名（可与目录模型同名） | 映射到的模型，按完整名称或去掉 `[<数字>k]`、`[<数字>m]` 后缀匹配 |

## 快速开始

### 桌面版

从 [Releases](https://github.com/Mag1cFall/Antigravity2API/releases) 下载对应系统的便携包，解压即可运行：

| 系统 | 文件 | 启动 | 数据目录（`.env`、`auth/` 与 `runtime/`） |
| --- | --- | --- | --- |
| Windows | `windows-amd64.zip` | 双击 `antigravity2api.exe` | `antigravity2api.exe` 所在目录（整目录可移动） |
| macOS | `desktop-<版本>-darwin-universal.tar.gz` | 打开 `Antigravity2API.app` | `~/Library/Application Support/Antigravity2API` |
| Linux | `desktop-<版本>-linux-amd64.tar.gz` 或 `linux-arm64.tar.gz` | 运行 `antigravity2api` | `~/.config/Antigravity2API` |

- **运行环境**：Linux 桌面版需要系统的 GTK 3 与 WebKitGTK 4.1，Ubuntu 22.04 先执行 `sudo apt install libwebkit2gtk-4.1-0`；Windows 缺少 WebView2 运行时，管理端自动在默认浏览器中打开
- **首次添加账户**：窗口打开管理端后，在账户页登录 Google 账户或导入本机官方客户端的账户
- **窗口与托盘**：
  - Windows 与 macOS 关闭窗口后留在托盘：Windows 双击托盘图标、macOS 单击菜单栏图标恢复窗口，右键打开托盘菜单（额度摘要、刷新额度、在浏览器中打开、复制 API 地址、开机启动与退出；文字随界面语言变化，Windows 配色随界面配色变化）
  - Linux 关闭窗口即退出；桌面环境没有系统托盘（如未装 AppIndicator 扩展的 GNOME）时只使用窗口，`--hidden` 启动同样打开窗口

### 命令行版

从 [Releases](https://github.com/Mag1cFall/Antigravity2API/releases) 下载命令行版 `<版本>-<系统>-<架构>.tar.gz` 并解压。Windows 直接使用桌面版的 `antigravity2api.exe`，在 cmd 或 PowerShell 中写作 `cmd /c start /wait antigravity2api.exe <子命令>` 以便等待程序结束。

首次使用先添加账户：

```bash
./antigravity2api login
```

`login` 在系统浏览器中打开 Google 授权页，完成后账户保存到 `auth/<邮箱>/`，服务启动时按官方客户端顺序完成初始化。无浏览器环境（如 SSH 服务器）使用授权码登录：

```bash
./antigravity2api login --code
```

终端打印授权链接，在任意设备的浏览器中授权后，将页面显示的授权码粘贴回终端。两种方式均无时限，按 Ctrl+C 取消。

本机已登录 Antigravity 官方客户端时可直接导入：

```bash
./antigravity2api import
```

`import` 依次尝试从系统凭据库（服务名 `gemini`、用户名 `antigravity`）、`~/.gemini/jetski-standalone-oauth-token` 与官方 agy CLI 的 `~/.gemini/antigravity-cli/antigravity-oauth-token` 读取并导入该账户。

把本机官方客户端切换到账户库中的某个账户：

```bash
./antigravity2api switch <邮箱>
```

`switch` 结束运行中的官方客户端，写入该账户的 token 后重新打开官方客户端。

启动服务：

```bash
./antigravity2api serve
```

默认监听 `127.0.0.1:8017`，读取当前目录的 `.env`、`auth/` 与 `runtime/`。命令行版无参数启动等同于 `serve --open-ui`，就绪后用浏览器打开管理端；Windows 的 `antigravity2api.exe` 双击打开桌面窗口。

### 远程服务器

服务部署在远程机器时，在服务器管理端的账户页生成配对令牌（10 分钟内有效），随后在有浏览器的电脑上登录并上传：

```bash
./antigravity2api login --remote https://server.example.com --token <配对令牌>
```

本机默认使用浏览器登录，加 `--code` 使用授权码；完成后 token 仅上传到远程服务并保存在远程 `auth/`，本机 `auth/` 保持不变。也可以在服务器管理端账户页直接发起授权码登录，或在服务器上运行 `login --code`。

### Docker

```bash
docker run -d --name antigravity2api -p 8017:8017 -v ./data:/data ghcr.io/mag1cfall/antigravity2api:latest
```

- **镜像与存储**：`latest` 指向最新正式版，`v<版本>` 固定指定版本；工作目录为 `/data`，`.env`、`auth/` 与 `runtime/` 保存在挂载目录中，容器监听 `0.0.0.0:8017`
- **远程访问安全**：端口对外开放时设置 `API_KEYS`；从容器外访问管理端需设置 `ADMIN_AUTH_ENABLED=true` 与 `ADMIN_PASSWORD`
- **添加账户**：使用管理端授权码登录、远程配对、执行 `docker exec -it antigravity2api antigravity2api login --code`，或复制其他电脑的 `auth/<邮箱>/` 到 `data/auth/`（运行中的服务 2 秒内自动载入）

### 代理

`PROXY` 是全局 HTTP、HTTPS 或 SOCKS5 代理（例如 `socks5://127.0.0.1:1080`、`http://user:pass@127.0.0.1:7890`），登录、token 刷新、账户初始化、生成与伴随请求都经其发出；代理出口需位于 Antigravity 支持的地区。

- **代理池**：`PROXY` 中的代理（逗号分隔，管理端按行填写，单个代理同样适用）组成代理池，每个账户绑定当前账户数最少的代理；登录与在线更新等非账户请求使用第一个代理
- **健康检测**：服务启动时、每 5 分钟、公开 API 遇上游连接错误时（连续错误合并为一次）、保存设置时与点击“立即检测”时检测各代理；代理不可用时，其上的账户自动改绑到可用代理中账户最少者；设置页显示各代理状态、延迟与绑定账户
- **系统代理**：`PROXY` 为空时跟随环境变量 `HTTPS_PROXY`、`HTTP_PROXY`、`NO_PROXY`，Windows 另读取系统代理设置
- **账户代理**：账户页可为单个账户单独指定代理，填 `direct` 时直连

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

- **图像模型与工具**：用图像生成模型请求 Chat、Responses 或 Claude 时输出图片（Chat 与 Claude 正文为 data URL 的 Markdown 图片，Responses 为 `image_generation_call`）；Responses 的 `image_generation` 工具由文本模型调用、图像生成模型执行
- **Tab 补全**：`/v1/completions` 以 `prompt` 为光标前、`suffix` 为光标后的文字，由 Tab 补全模型生成光标处的补全
- **Images 接口**：`/v1/images/generations` 与 `/v1/images/edits` 省略 `model` 时选用目录中的图像生成模型，`size` 映射到最接近的宽高比并缩放到目标尺寸（不放大），`quality` 映射到 1K、2K 或 4K；`/v1/images/edits` 支持 `multipart/form-data`（`image` / `image[]` 与 `mask`）与 `application/json`（`images` 与 `mask`，每项填 `image_url` 或 `file_id`），`mask` 透明区域为编辑区域
- **Audio 接口**：`/v1/audio/transcriptions` 省略 `model` 或指定转写模型时转写 WAV、MP3、FLAC 或 PCM 音频，按实时节奏发送（用时约等于音频时长），`response_format` 支持 `json` 或 `text`；`/v1/audio/translations` 转写后由目录首个文本模型译为英文，指定文本模型时由其转写并翻译

### Claude 兼容

```text
POST   /v1/messages
POST   /v1/messages/count_tokens
GET    /v1/models
```

带 `anthropic-version` 请求头的 `/v1/models` 按 Anthropic 格式返回；带 `anthropic-version` 或 `anthropic-beta: files-api-*` 请求头的 `/v1/files` 按 Anthropic Files API 格式返回。

### Gemini 兼容

```text
POST   /v1beta/models/{model}:generateContent
POST   /v1beta/models/{model}:streamGenerateContent?alt=sse
POST   /v1beta/models/{model}:countTokens
GET    /v1beta/models
GET    /v1beta/models/{model}
POST   /v1beta/cachedContents
GET    /v1beta/cachedContents
GET    /v1beta/cachedContents/{cache}
PATCH  /v1beta/cachedContents/{cache}
DELETE /v1beta/cachedContents/{cache}
POST   /upload/v1beta/files
GET    /v1beta/files
GET    /v1beta/files/{name}
DELETE /v1beta/files/{name}
POST   /v1beta/interactions
GET    /v1beta/interactions/{id}
DELETE /v1beta/interactions/{id}
POST   /v1beta/interactions/{id}/cancel
GET    /ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent
```

- **流式与路径兼容**：`streamGenerateContent` 不带 `alt=sse` 时返回 JSON 数组；模型方法、上下文缓存、文件上传与 Interactions 均同时提供 `/v1` 路径（将 `v1beta` 换为 `v1`）；`/v1/models` 与 `/v1/files` 以 `x-goog-api-key` 或 `key` 认证时按 Gemini 格式返回
- **Live API**：经上面的 WebSocket 地址连接（`v1alpha` 同样可用）；转写模型返回语音的文字转写，Agent 模型以语音模式对话并输出 24 kHz 语音
- **图像输出**：`generationConfig.responseModalities` 含 `IMAGE` 时需指定图像生成模型，图片以 `inlineData` part 返回，`imageConfig` 的 `aspectRatio` 与 `imageSize` 原样生效

### 认证

- **API key**：`API_KEYS` 设置一个或多个公开 API key，请求经 `Authorization: Bearer <key>`、`x-api-key`、`x-goog-api-key` 或 `?key=` 携带；请求记录与用量按 key 统计
- **无 key 时的网页保护**：`API_KEYS` 为空时不校验 key，但带 `Origin` 头的浏览器请求仅接受 localhost 与回环地址，其他来源与 `Origin: null` 返回 401 `Invalid API Key`；从网页客户端接入需设置 `API_KEYS`
- **指定账户**：请求带 `X-Antigravity-Account-Id: <账户>`（填写账户页显示的邮箱）时仅由该账户处理；该账户不存在、不在该 key 绑定账户中或不可用时直接返回写明原因的错误，不换账户

### 管理端与服务状态

- **访问与登录**：浏览器打开 `http://127.0.0.1:8017/` 进入管理端（接口位于 `/api/*`）；本机回环地址免登录，设置 `ADMIN_AUTH_ENABLED=true`、`ADMIN_USERNAME` 与 `ADMIN_PASSWORD` 后可从其他设备登录
- **反向代理**：经反向代理访问时原样转发 `Host`（如 nginx `proxy_set_header Host $http_host;`），管理端按访问域名识别远程访问并要求登录
- **配置生效**：设置页保存的配置写回 `.env`；`LISTEN`、`AUTH_DIR` 与 `RUNTIME_DIR` 重启生效，其余项下一次请求生效
- **在线更新**：设置页“关于”显示 GitHub 最新发行版，确认后经 `PROXY` 下载对应平台发行包、校验 SHA-256、替换程序并重启；Docker 部署随镜像更新

## 客户端接入

OpenAI SDK 的 base URL 使用 `http://127.0.0.1:8017/v1`，Anthropic 与 Google GenAI SDK 使用 `http://127.0.0.1:8017`。

- 管理端的接入页按当前地址、API key 与实时模型目录生成 Claude Code、Codex、OpenCode、OpenClaw、Magpie、pi、omp、dsh、Cherry Studio 与 curl 的配置，可直接复制
- OpenCode、OpenClaw、pi、omp、dsh 与 Cherry Studio 支持切换接入协议；Claude Code、Codex、OpenCode、OpenClaw、pi、Cherry Studio 与 Magpie 提供一键导入链接
- 接入页同时列出全部接口与常用能力，点开可查看主要参数、curl 示例与官方文档

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

Claude Code 后台任务使用的小模型名可用 `MODEL_MAPPING` 映射到目录中的模型，例如 `MODEL_MAPPING=claude-haiku-5-5:gemini-3.8-flash-low`；管理端模型页的“模型映射”可逐行添加与修改。

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
| `custom` | `SYSTEM_PROMPT_TEMPLATE` 的模板 | `SYSTEM_PROMPT_USER_TEMPLATE` 的模板 |

管理端设置页的“查看原文”显示各档位发给上游的系统指令与首轮用户消息。选择 `custom` 后可分别编辑两份 Go `text/template` 模板，下方实时显示代入示例数据后的字符数或模板错误：

- **系统指令**：按官方顺序逐段列出全部官方段落与客户端系统提示，每段可单独开关、展开查看或编辑正文，支持“恢复官方原文”与光标处“插入”变量（`{{.Client}}` 表示客户端自己的系统提示）；顶部“载入预设”可一键填入内置三档全文
- **user 轮包装**：直接编辑包装模板，`{{.Text}}` 表示用户消息原文，同样提供“载入预设”与变量“插入”

Google 会直接拒绝系统提示中含某些客户端身份句与说明文字的请求（返回 429）。服务在发送前删去匹配句子，规则列表位于 [`rules/rejected.txt`](rules/rejected.txt)，启动时与每小时自动更新；设置页显示列表版本与上次检查结果，支持“立即更新”与自定义规则（`REJECTED_RULES`，保存后下一次请求生效）。

## 环境变量

| 变量 | 说明 | 默认值 |
| --- | --- | --- |
| `LISTEN` | 监听地址（公开 API、管理 API 与管理端） | `127.0.0.1:8017` |
| `API_KEYS` | 公开 API key，逗号分隔 | 空 |
| `API_KEY_ACCOUNTS` | API key 绑定的账户（`key:账户\|账户`，逗号分隔） | 空 |
| `API_KEY_NAMES` | API key 备注名（`key:备注名`，逗号分隔），在管理端显示 | 空 |
| `AUTH_DIR` | 账户目录 | `auth` |
| `RUNTIME_DIR` | 运行数据目录（账本、日志、文件、媒体、会话与目录缓存） | `runtime` |
| `PROXY` | 全局 HTTP、HTTPS 或 SOCKS5 代理（逗号分隔组成代理池） | 空 |
| `SYSTEM_PROMPT` | 系统提示档位：`full`、`medium`、`aggressive`、`custom` | `full` |
| `SYSTEM_PROMPT_TEMPLATE` | `custom` 档位系统指令模板（多行用双引号加 `\n`） | `medium` 档系统指令 |
| `SYSTEM_PROMPT_USER_TEMPLATE` | `custom` 档位用户消息模板，为空时原样发送 | `medium` 档用户包装 |
| `REJECTED_RULES` | 自定义拒绝句规则，与自动更新列表合并，语法见 `rules/rejected.txt` | 空 |
| `MODEL_MAPPING` | 客户端模型名映射（`别名:模型 ID`，逗号分隔） | 空 |
| `MEDIA_OUTPUT` | 生成媒体输出方式：`inline` 或 `url` | `inline` |
| `MEDIA_BASE_URL` | 媒体链接使用的服务地址，如 `https://ai.example.com` | 空 |
| `ACCOUNT_CONCURRENCY` | 单账户同时处理的请求数 | `2` |
| `ROUTING_STRATEGY` | 调度策略：`round-robin` 或 `fill-first` | `round-robin` |
| `PREFER_LOWER_TIER_MODELS` | 优先使用低套餐账户的模型 ID，逗号分隔 | 空 |
| `REQUEST_TIMEOUT` | 等待首个上游事件及相邻两事件之间的最长时长 | `5m` |
| `FIRST_EVENT_HEDGE` | 首字超时备用请求策略：`auto`、正整数秒或 `off` | `auto` |
| `FIRST_EVENT_HEDGE_MODELS` | 发起备用请求的模型 ID，逗号分隔 | `gemini-4-carbon,gemini-4-argon` |
| `NONSTREAM_KEEPALIVE` | 非流式请求等待保活 | `true` |
| `FAKE_STREAM` | 流式请求先收齐完整结果再按流式输出，中途失败时换号重生成 | `false` |
| `QUOTA_REFRESH_INTERVAL` | 后台刷新全部账户（含停用账户）额度的间隔，最短 `1m`，`0` 关闭 | `15m` |
| `QUOTA_RESERVE_PERCENT` | 额度保留比例，0–99 | `0` |
| `WEEKLY_QUOTA_WARMUP` | 自动开始每周额度计时 | `false` |
| `LOG_LEVEL` | 日志级别：`debug`、`info`、`warn`、`error` | `info` |
| `LOG_FORMAT` | 日志格式：`json` 或 `text` | `json` |
| `LOG_FILE` | 同时把日志写入 `RUNTIME_DIR/logs/`，单文件 10 MB 轮转 | `true` |
| `LOG_MAX_TOTAL_MB` | 日志文件总量上限（MB，不小于 20） | `500` |
| `REQUEST_LOG_BODIES` | 保存请求与响应正文 | `false` |
| `ADMIN_AUTH_ENABLED` | 远程访问管理端时的登录 | `false` |
| `ADMIN_USERNAME` | 管理端用户名 | `admin` |
| `ADMIN_PASSWORD` | 管理端密码 | 空 |

- `LISTEN`：端口被 Windows 保留时改用其后第一个可用端口，管理端顶部显示实际地址
- `API_KEY_ACCOUNTS`：绑定了账户的 key 仅使用这些账户
- `PROXY`：为空时跟随系统代理
- `MEDIA_OUTPUT` 与 `MEDIA_BASE_URL`：作用于 Chat Completions、Messages 正文与 Images `response_format: url`；`inline` 以 data URL 内联，`url` 按内容命名保存到 `RUNTIME_DIR/media/` 并给出 `/media/<文件名>` 链接（该路径不校验 API key）；`MEDIA_BASE_URL` 为空时使用请求访问的地址，经反向代理时跟随 `X-Forwarded-Proto`
- `ROUTING_STRATEGY`：在优先级最高的账户中，`round-robin` 选进行中请求最少者，并列时选最近一次被选中最早的账户；`fill-first` 先用满一个账户
- `PREFER_LOWER_TIER_MODELS`：同一优先级内依次先用 Free、Pro、Ultra 账户
- `FIRST_EVENT_HEDGE` 与 `FIRST_EVENT_HEDGE_MODELS`：原请求迟迟未出首字时用空闲账户（其他账户优先，否则用原账户的空闲槽位）发起备用请求，先出首字的一方继续、另一方取消，后台 Responses 不发起；`auto` 按各模型近 24 小时首字等待的 p95（样本少于 20 次或请求正文超过近期 p95 字节数时不发起），正整数为等待秒数，`off` 关闭；备用请求同样计入账户额度
- `NONSTREAM_KEEPALIVE`：非流式请求等待超过 10 秒时先返回 200，之后每 10 秒写出一个换行，避免反向代理因长时间无数据断开连接
- `QUOTA_RESERVE_PERCENT`：账户某个额度组的 5 小时或每周剩余比例不高于此百分比时，调度跳过该组直到窗口重置；`0` 时只跳过已用尽的组
- `WEEKLY_QUOTA_WARMUP`：每次自动刷新额度后，为每周额度窗口尚未开始计时的就绪账户发一条 `Hi` 启动计时；同一账户的每个额度组 6 天内最多一次
- `LOG_MAX_TOTAL_MB`：保留 `LOG_MAX_TOTAL_MB / 10 − 1` 个轮转文件，超出时删除最旧的
- `REQUEST_LOG_BODIES`：保存公开请求与响应正文和成功请求的上游尝试正文，失败请求的上游尝试正文始终保存；各截断到 64 KiB，各保留最近 1000 条

进程环境变量覆盖 `.env` 中的同名项。

## 命令行

| 命令 | 作用 |
| --- | --- |
| `serve` | 运行服务；`--open-ui` 在就绪后打开管理端 |
| `login` | 登录 Google 账户并加入账户库 |
| `import` | 导入本机 Antigravity 官方客户端的账户 |
| `switch <邮箱>` | 把本机官方客户端切换到账户库中的该账户 |
| `accounts` | 读取账户目录，按添加时间列出账户、套餐、启用状态与添加时间 |
| `status` | 查看运行中服务与各账户的状态 |
| `version` | 打印程序版本 |

- `login` 参数：默认浏览器登录；`--code` 粘贴授权码登录；`--remote <地址> --token <令牌>` 上传到远程服务（可与 `--code` 同用）；`--proxy <URL>` 指定本次登录代理并绑定到该账户（`direct` 直连，可与其他参数同用）；`--cloud --provider <名称>` 在本机以 Continue with Google Cloud 的企业 WIF 方式登录（不与 `--code`、`--remote` 同用）
- `status`：经回环地址免登录读取 `/api/status` 与 `/api/accounts`，打印版本、监听地址、启动时间及各账户状态、套餐与各额度桶剩余比例
- 通用覆盖参数：各命令均接受 `--listen`、`--auth`、`--runtime` 与 `--proxy`，覆盖 `LISTEN`、`AUTH_DIR`、`RUNTIME_DIR` 与 `PROXY`（`login` 的 `--proxy` 见上）
- 退出码与桌面版：出错时向标准错误写明原因并以退出码 1 结束（如服务未运行时 `status` 打印 `服务未在 <LISTEN> 运行: <原因>`）；`accounts` 与 `status` 输出格式见 [开发文档](docs/development.md#子命令输出)；桌面版可执行文件同样接受上述子命令，无子命令时打开桌面窗口，`--hidden` 启动后只显示托盘

## 文档

- [开发与贡献](docs/development.md)：环境构建、目录结构、配置、账户调度、管理 API 与测试
- [协议参考](docs/protocol.md)：登录、初始化、请求信封、伴随请求与流式解码
- [运行日志与请求账本](docs/logging.md)：日志格式、事件清单、SQLite 账本、费用与管理事件流
- [逆向指南](docs/reverse-engineering.md)：官方客户端抓包、静态分析与新版本对齐
