# Antigravity2API

[简体中文](README.md) | [English](README_en.md)

Turns the Agent models of Google Antigravity into OpenAI, Claude and Gemini compatible APIs.

<table>
  <tr>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/dashboard-dark.webp"><img src="docs/images/dashboard-light.webp" alt="Dashboard"></picture><br><b>Dashboard</b><br>Quota, requests</td>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/accounts-dark.webp"><img src="docs/images/accounts-light.webp" alt="Accounts"></picture><br><b>Accounts</b><br>Quota, state, device</td>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/connect-dark.webp"><img src="docs/images/connect-light.webp" alt="Connect"></picture><br><b>Connect</b><br>Client setup</td>
  </tr>
  <tr>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/models-dark.webp"><img src="docs/images/models-light.webp" alt="Models"></picture><br><b>Models</b><br>Live catalog</td>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/usage-dark.webp"><img src="docs/images/usage-light.webp" alt="Usage"></picture><br><b>Usage</b><br>Requests, tokens</td>
    <td width="33%" valign="top"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/images/prompt-dark.webp"><img src="docs/images/prompt-light.webp" alt="System prompt"></picture><br><b>System prompt</b><br>Four tiers</td>
  </tr>
</table>

## Features

- **OpenAI compatible**: `/v1/chat/completions`, `/v1/completions`, `/v1/responses`, `/v1/models`, `/v1/images`, `/v1/audio`, `/v1/files`
- **Claude compatible**: `/v1/messages`, `/v1/messages/count_tokens`
- **Gemini compatible**: `/v1beta/models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens`, Context Caching, Files API, Interactions API, Live API
- **Official client identity**: plain Go HTTP reaching Cloud Code with the TLS and HTTP shape, User-Agent, request envelope, initialization order and companion requests of the official Antigravity client; every account uses the device profile generated when added
- **Agent models**: every Gemini, Claude and GPT-OSS Agent model published from each account's live catalog; thinking parameters map to the Low, Medium, High and XHigh thinking levels of the same family
- **Streaming**: text, thinking and thought signatures stream incrementally; tool calls, parallel function calls, image and PDF input work across OpenAI Chat, OpenAI Responses, Claude Messages and Gemini
- **Images and speech**:
  - The catalog image generation model generates and edits images across the four protocols and OpenAI Images endpoints
  - The catalog speech transcription model transcribes audio over the official voice input channel; OpenAI transcription and translation endpoints use it when `model` is omitted, while Chat `input_audio` and Gemini audio input are transcribed when it is selected or passed directly to any other selected model
- **Files**: OpenAI, Claude and Gemini Files APIs store files in `RUNTIME_DIR/files/` and inline file references before sending
- **System prompt tiers**: full, medium, aggressive and custom tiers control how much of the official system prompt and user message wrapping is kept
- **Multi-account scheduling**:
  - Schedules by account priority and the Gemini, Gemini Argon (when the account has a dedicated Argon weekly bucket) and third-party quota groups, rotating within the same priority, keeping a conversation on one account, and letting selected models prefer lower-plan accounts
  - Exhausted quota cools down until reset, while rate limits, capacity errors and server errors cool down or switch accounts by reason; quota refreshes on a schedule and on demand for all accounts or a single account
- **Token counting**: counts input tokens locally per model family; renders GPT-OSS with the official chat template and calibrates Gemini and Claude counts against upstream usage of the same conversation
- **Admin UI and desktop app**:
  - Web admin UI with first-run guide, dashboard, accounts, client setup, models, request records, usage and cost, in-flight requests, logs and settings; account details show available models and accounts sharing weekly quota (typically the same Google One family group)
  - Preset and custom time ranges for usage and request records, interactive trend chart hover details, local folder export on the host machine and browser download on remote machines
  - Available in Simplified Chinese, Traditional Chinese (Taiwan), English, Japanese, Korean, French and German with localized logs and errors; Windows, macOS and Linux desktop apps include a tray icon, single instance and launch at login
- **Logs and request ledger**: structured logs record account selection, switches, cooldowns and upstream attempts per request; the SQLite ledger stores account, model, tokens, cache hits, timings and official-price cost estimates
- **Sign-in and migration**: browser sign-in, pasted authorization code, local official client import, cross-machine pairing upload, and account JSON export and selective import (accepting files from Antigravity Manager, CLIProxyAPI, sub2api, gcli2api, opencode-antigravity-auth and antigravity-claude-proxy)
- **Proxy**: global and per-account HTTP, HTTPS and SOCKS5 proxies; falls back to the system proxy when unset

## Supported Models

| Model ID | Name | Thinking levels |
| --- | --- | --- |
| `gemini-4-neon` | Gemini 4 Neon | Low, Medium, High, XHigh |
| `gemini-4-carbon` | Gemini 4 Carbon | Low, Medium, High |
| `gemini-4-argon` | Gemini 4 Argon | Low, Medium, High |
| `gemini-4-hydrogen` | Gemini 4 Hydrogen | Low, Medium, High, XHigh |
| `gemini-pro-orchestrator-neon` | Orchestrator (Neon) | — |
| `gemini-pro-orchestrator` | Orchestrator (Carbon) | — |
| `gemini-pro-orchestrator-argon` | Orchestrator (Argon) | — |
| `gemini-3.8-flash` | Gemini 3.8 Flash | Low, Medium, High |
| `claude-opus-5-5` | Claude Opus 5.5 | Low, Medium, High |
| `claude-sonnet-5-5` | Claude Sonnet 5.5 | Low, Medium, High |
| `gpt-oss-120b` | GPT-OSS 120B | Medium |

The service reads the Antigravity model catalog of every account at startup and saves it to `RUNTIME_DIR/catalogs/`; `/v1/models` is authoritative once it runs:

- The table lists the Agent models of the official model picker; entries with thinking levels carry their available and default levels: `supported_reasoning_levels` and `default_reasoning_level` in OpenAI format, `thinking_levels` and `default_thinking_level` in Anthropic format, `thinkingLevels` and `defaultThinkingLevel` in Gemini format
- The image generation model (for example `gemini-3.1-flash-image`) and the speech transcription model (for example `proactive-observer-v10`) follow, marked with `image_generation` and `transcription`
- Other usable catalog entries are published with their catalog IDs, such as `claude-sonnet-4-6` and `claude-opus-4-6-thinking` on Pro accounts
- After a restart, `/v1/models` serves saved catalogs until accounts finish initializing; models only present on still-initializing accounts return 503
- Context windows, output limits and capabilities come from the catalog, and the account plan decides available models and quota

The thinking level is decided in this order:

1. A level named in the model name: a tiered catalog ID such as `claude-opus-5-5-high`, a parenthesized suffix such as `claude-opus-5-5(low)` (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`, a thinking budget in tokens, or `auto` for no level), or the `-thinking` suffix (High)
2. Request thinking parameters: OpenAI `reasoning_effort` maps `none`, `minimal`, `low` to Low, `medium` to Medium, `high` to High, and `xhigh`, `max` to XHigh (High when the model has no XHigh level); Anthropic `thinking.budget_tokens` and Gemini `thinkingConfig.thinkingBudget` map up to 1024 to Low, up to 8192 to Medium, up to 16384 to High, and larger to XHigh (High when the model has no XHigh level), while `-1` and other negative values set no level
3. Medium, or the model's only level

Model names also match the catalog as follows:

| Name | Result |
| --- | --- |
| With the `models/` prefix | Matched after removing the prefix |
| With a `[<number>k]` or `[<number>m]` context window suffix, such as `claude-opus-5-5[1m]` | Matched after removing it |
| With a date version or `-latest`, such as `claude-opus-5-5-20261001` | Matched after removing it |
| An alias in `MODEL_MAPPING` (including one named like a catalog model) | The mapped model, matched by exact name or after removing a `[<number>k]` / `[<number>m]` suffix |

## Quick Start

### Desktop App

Download the portable desktop package for your system from [Releases](https://github.com/Mag1cFall/Antigravity2API/releases) and extract to run:

| System | File | Start | Data directory (`.env`, `auth/` and `runtime/`) |
| --- | --- | --- | --- |
| Windows | `windows-amd64.zip` | Double-click `antigravity2api.exe` | Folder of `antigravity2api.exe` (portable as a whole folder) |
| macOS | `desktop-<version>-darwin-universal.tar.gz` | Open `Antigravity2API.app` | `~/Library/Application Support/Antigravity2API` |
| Linux | `desktop-<version>-linux-amd64.tar.gz` or `linux-arm64.tar.gz` | Run `antigravity2api` | `~/.config/Antigravity2API` |

- **Runtime requirements**: Linux requires system GTK 3 and WebKitGTK 4.1 (on Ubuntu 22.04 run `sudo apt install libwebkit2gtk-4.1-0` first); on Windows without the WebView2 runtime, the admin UI opens in the default browser
- **First account**: open the Accounts page in the window to sign in with Google or import the account of the local official client
- **Window and tray**:
  - On Windows and macOS, closing the window keeps the app in the tray: double-click the Windows tray icon or click the macOS menu bar icon to show the window, and right-click for the tray menu (quota summary, Refresh Quota, Open in Browser, Copy API Address, Start at Login and Quit; menu text follows the UI language and Windows menu colors follow the UI theme)
  - On Linux, closing the window quits; on desktops without a system tray (such as GNOME without the AppIndicator extension) the window is always used, including with `--hidden`

### Command Line

Download and extract `<version>-<os>-<arch>.tar.gz` from [Releases](https://github.com/Mag1cFall/Antigravity2API/releases). On Windows use the desktop `antigravity2api.exe`, written as `cmd /c start /wait antigravity2api.exe <command>` in cmd or PowerShell so the prompt waits until the command exits.

Add an account first:

```bash
./antigravity2api login
```

`login` opens Google authorization in the system browser, saves the account to `auth/<email>/`, and initializes it in the official client order at startup. On headless hosts (such as over SSH), sign in with an authorization code:

```bash
./antigravity2api login --code
```

Open the printed link in a browser on any device, authorize, and paste the code back into the terminal. Both flows wait with no time limit; press Ctrl+C to cancel.

Import an account directly when the official Antigravity client is already signed in on this computer:

```bash
./antigravity2api import
```

`import` checks the system credential store (service `gemini`, user `antigravity`), then `~/.gemini/jetski-standalone-oauth-token`, then `~/.gemini/antigravity-cli/antigravity-oauth-token` of the official agy CLI, importing the first token found.

Switch the local official client to an account in the store:

```bash
./antigravity2api switch <email>
```

`switch` closes the running official client, writes that account's token to the official client's sign-in location, and relaunches it.

Start the service:

```bash
./antigravity2api serve
```

Listens on `127.0.0.1:8017` by default and reads `.env`, `auth/` and `runtime/` from the current directory. Running the command-line build with no arguments runs `serve --open-ui` and opens the admin UI in the browser once ready; double-clicking `antigravity2api.exe` on Windows opens the desktop window.

### Remote Server

When running on another machine, create a pairing token on the server's Accounts page (valid for 10 minutes), then sign in and upload from a computer with a browser:

```bash
./antigravity2api login --remote https://server.example.com --token <pairing token>
```

Local sign-in uses the browser by default, or an authorization code with `--code`; the token is uploaded only to the remote service's `auth/` while local `auth/` stays unchanged. You can also start authorization-code sign-in directly on the server's Accounts page or run `login --code` on the server.

### Docker

```bash
docker run -d --name antigravity2api -p 8017:8017 -v ./data:/data ghcr.io/mag1cfall/antigravity2api:latest
```

- **Image and storage**: `latest` tracks the latest stable release and `v<version>` pins a release; the container works in `/data` storing `.env`, `auth/` and `runtime/` in the mounted directory and listening on `0.0.0.0:8017`
- **Remote security**: set `API_KEYS` when exposing the port; opening the admin UI from outside the container requires `ADMIN_AUTH_ENABLED=true` and `ADMIN_PASSWORD`
- **Adding accounts**: use authorization-code sign-in in the admin UI, remote pairing, `docker exec -it antigravity2api antigravity2api login --code`, or copy `auth/<email>/` from another machine into `data/auth/` (loaded automatically within 2 seconds)

### Proxy

`PROXY` configures a global HTTP, HTTPS or SOCKS5 proxy (such as `socks5://127.0.0.1:1080` or `http://user:pass@127.0.0.1:7890`) for sign-in, token refresh, account initialization, generation and companion requests; its exit must be in a region supported by Antigravity.

- **Proxy pool**: proxies in `PROXY` (comma separated; one per line in the admin UI; a single proxy as well) form a pool where each account binds to the proxy with the fewest accounts; non-account requests such as sign-in and online update use the first proxy
- **Health checks**: run at startup, every 5 minutes, on upstream connection errors from public API requests (repeated errors merge into one check), when settings are saved, and via "Check now" in Settings; accounts on an unavailable proxy move to the available proxy with the fewest accounts; Settings shows each proxy's status, latency and bound accounts
- **System proxy**: when `PROXY` is empty, `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` are used, plus Windows system proxy settings
- **Account proxy**: individual accounts can set their own proxy on the Accounts page; `direct` connects without a proxy

## API Endpoints

### OpenAI Compatible

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

- **Image models and tool**: calling Chat, Responses or Claude with an image generation model returns images (Markdown data URL image in Chat and Claude, `image_generation_call` in Responses); the Responses `image_generation` tool is exposed to text models and executed by the image generation model
- **Tab completion**: `/v1/completions` treats `prompt` as text before the cursor and `suffix` as text after it, generating the completion with the Tab completion model
- **Images endpoints**: `/v1/images/generations` and `/v1/images/edits` use the catalog image generation model when `model` is omitted, map `size` to the nearest aspect ratio and scale down to the target size without upscaling, and map `quality` to 1K, 2K or 4K; `/v1/images/edits` accepts both `multipart/form-data` (`image` / `image[]` and `mask`) and `application/json` (`images` and `mask`, each with `image_url` or `file_id`), editing the transparent region of `mask`
- **Audio endpoints**: `/v1/audio/transcriptions` transcribes WAV, MP3, FLAC or PCM audio using the transcription model when `model` is omitted or set to it, streamed at real-time pace (taking about the audio duration), with `response_format` `json` or `text`; `/v1/audio/translations` transcribes and translates to English using the first catalog text model, or uses the specified text model directly

### Claude Compatible

```text
POST   /v1/messages
POST   /v1/messages/count_tokens
GET    /v1/models
```

`/v1/models` with an `anthropic-version` header returns the Anthropic format; `/v1/files` with an `anthropic-version` or `anthropic-beta: files-api-*` header returns the Anthropic Files API format.

### Gemini Compatible

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

- **Streaming and `/v1` paths**: `streamGenerateContent` without `alt=sse` returns a JSON array; model methods, context caching, file upload and Interactions are also served under `/v1` (replace `v1beta` with `v1`); `/v1/models` and `/v1/files` return Gemini format when authenticated with `x-goog-api-key` or `key`
- **Live API**: connects at the WebSocket path above (`v1alpha` also works); transcription models return text transcripts of speech, and Agent models talk in voice mode with 24 kHz audio output
- **Image output**: `generationConfig.responseModalities` with `IMAGE` requires an image generation model; images return as `inlineData` parts and `imageConfig.aspectRatio` and `imageConfig.imageSize` apply as given

### Authentication

- **API keys**: `API_KEYS` sets one or more public API keys passed via `Authorization: Bearer <key>`, `x-api-key`, `x-goog-api-key` or `?key=`; request records and usage are grouped by key
- **Browser origin protection**: when `API_KEYS` is empty, browser requests with an `Origin` header are accepted only from localhost or loopback addresses; other origins and `Origin: null` return 401 `Invalid API Key`, so set `API_KEYS` for web clients
- **Pinned account**: `X-Antigravity-Account-Id: <account>` (the email shown on the Accounts page) routes the request exclusively to that account; if it does not exist, is not bound to the key, or is unavailable, the request returns an error stating the reason without switching accounts

### Admin UI and Service Status

- **Access and sign-in**: open `http://127.0.0.1:8017/` for the admin UI (backed by `/api/*`); loopback access requires no sign-in, while setting `ADMIN_AUTH_ENABLED=true`, `ADMIN_USERNAME` and `ADMIN_PASSWORD` enables remote sign-in
- **Reverse proxy**: forward `Host` unchanged (`proxy_set_header Host $http_host;` in nginx) so the admin UI recognizes remote access by host and requires sign-in
- **Settings activation**: settings saved in the UI write back to `.env`; `LISTEN`, `AUTH_DIR` and `RUNTIME_DIR` take effect after restart, all other settings on the next request
- **Online update**: the About section of Settings displays the latest GitHub release; upon confirmation, the app downloads the platform package through `PROXY`, verifies SHA-256, replaces the binary and restarts (Docker deployments update via the image)

## Client Setup

OpenAI SDKs use the base URL `http://127.0.0.1:8017/v1`; Anthropic and Google GenAI SDKs use `http://127.0.0.1:8017`.

- The Connect page generates ready-to-copy configs for Claude Code, Codex, OpenCode, OpenClaw, Magpie, pi, omp, dsh, Cherry Studio and curl from the current address, API key and live catalog
- OpenCode, OpenClaw, pi, omp, dsh and Cherry Studio support protocol selection; Claude Code, Codex, OpenCode, OpenClaw, pi, Cherry Studio and Magpie include one-click import links
- The Connect page also lists all endpoints and common capabilities with key parameters, curl examples and official documentation links

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

Claude Code:

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8017
export ANTHROPIC_AUTH_TOKEN=YOUR_API_KEY
export ANTHROPIC_MODEL=claude-opus-5-5
claude
```

Map the small model name used by Claude Code background tasks to a catalog model with `MODEL_MAPPING`, for example `MODEL_MAPPING=claude-haiku-5-5:gemini-3.8-flash-low`. The "Model mapping" tab on the Models page adds and edits entries row by row.

Codex (`~/.codex/config.toml`):

```toml
model = "gemini-3.8-flash"
model_provider = "antigravity"

[model_providers.antigravity]
name = "Antigravity2API"
base_url = "http://127.0.0.1:8017/v1"
env_key = "ANTIGRAVITY2API_KEY"
wire_api = "responses"
```

## System Prompt Tiers

The system instruction of every generation request combines the system prompt of the official client with the client's own system prompt. `SYSTEM_PROMPT` decides how much of the official part is kept:

| System prompt tier | Official system prompt | User messages |
| --- | --- | --- |
| `full` | All sections, client system prompt appended | Wrapped in the official format with local time and model selection |
| `medium` | identity, guidelines, communication_style | Wrapped in the official format with local time |
| `aggressive` | identity only | Sent as is |
| `custom` | The template in `SYSTEM_PROMPT_TEMPLATE` | The template in `SYSTEM_PROMPT_USER_TEMPLATE` |

"View text" on the Settings page shows the system instruction and first user message each tier sends upstream. With `custom`, Settings edits two Go `text/template` templates and shows the sample-rendered character count or template error beneath each:

- **System instruction**: lists every official section plus the client system prompt in official order; each section can be toggled on or off, expanded to inspect or edit its body, restored with "Restore official text", and edited with variable "Insert" (`{{.Client}}` represents the client's own system prompt); "Load preset" fills the full template of a built-in tier
- **User turn wrapper**: edits the message wrapper template directly (`{{.Text}}` represents the original user message), also with "Load preset" and variable "Insert"

Google rejects requests whose system prompt contains certain client identity sentences and instructions (HTTP 429). The service strips matching sentences before sending using [`rules/rejected.txt`](rules/rejected.txt), updated automatically at startup and every hour; Settings shows the rule list version and last check result, offers "Update now", and accepts custom rules (`REJECTED_RULES`, active on the next request after saving).

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `LISTEN` | Listen address for public API, admin API and admin UI | `127.0.0.1:8017` |
| `API_KEYS` | Public API keys, comma separated | empty |
| `API_KEY_ACCOUNTS` | Accounts bound to API keys (`key:account\|account`, comma separated) | empty |
| `API_KEY_NAMES` | Display names for API keys (`key:name`, comma separated) | empty |
| `AUTH_DIR` | Account directory | `auth` |
| `RUNTIME_DIR` | Runtime data directory (ledger, logs, files, media, conversations and catalogs) | `runtime` |
| `PROXY` | Global HTTP, HTTPS or SOCKS5 proxy (comma separated for a pool) | empty |
| `SYSTEM_PROMPT` | System prompt tier: `full`, `medium`, `aggressive`, `custom` | `full` |
| `SYSTEM_PROMPT_TEMPLATE` | `custom` system instruction template (use double quotes with `\n` for multi-line) | `medium` tier system instruction |
| `SYSTEM_PROMPT_USER_TEMPLATE` | `custom` user message template; empty sends messages as is | `medium` tier user wrapper |
| `REJECTED_RULES` | Custom rejected-sentence rules merged with the auto-updated list (syntax in `rules/rejected.txt`) | empty |
| `MODEL_MAPPING` | Client model aliases (`alias:model ID`, comma separated) | empty |
| `MEDIA_OUTPUT` | Generated media output mode: `inline` or `url` | `inline` |
| `MEDIA_BASE_URL` | Service address used in media links, e.g. `https://ai.example.com` | empty |
| `ACCOUNT_CONCURRENCY` | Concurrent requests per account | `2` |
| `ROUTING_STRATEGY` | Routing strategy: `round-robin` or `fill-first` | `round-robin` |
| `PREFER_LOWER_TIER_MODELS` | Model IDs preferring lower-plan accounts, comma separated | empty |
| `REQUEST_TIMEOUT` | Maximum wait for the first upstream event and between consecutive events | `5m` |
| `FIRST_EVENT_HEDGE` | Backup request trigger on slow first token: `auto`, positive integer seconds, or `off` | `auto` |
| `FIRST_EVENT_HEDGE_MODELS` | Model IDs eligible for backup requests, comma separated | `gemini-4-carbon,gemini-4-argon` |
| `NONSTREAM_KEEPALIVE` | Keep-alive newlines for slow non-streaming requests | `true` |
| `FAKE_STREAM` | Buffer full result before streaming; regenerate on another account on mid-stream failure | `false` |
| `QUOTA_REFRESH_INTERVAL` | Background quota refresh interval for all accounts (including disabled); min `1m`, `0` disables | `15m` |
| `QUOTA_RESERVE_PERCENT` | Quota reserve percentage, 0–99 | `0` |
| `WEEKLY_QUOTA_WARMUP` | Start weekly quota timers automatically | `false` |
| `LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `LOG_FORMAT` | Log format: `json` or `text` | `json` |
| `LOG_FILE` | Also write logs to `RUNTIME_DIR/logs/`, rotating at 10 MB per file | `true` |
| `LOG_MAX_TOTAL_MB` | Total log file size limit in MB (minimum 20) | `500` |
| `REQUEST_LOG_BODIES` | Store request and response bodies | `false` |
| `ADMIN_AUTH_ENABLED` | Sign-in for remote access to the admin UI | `false` |
| `ADMIN_USERNAME` | Admin user name | `admin` |
| `ADMIN_PASSWORD` | Admin password | empty |

- `LISTEN`: when Windows reserves the configured port, listens on the next available port and displays the actual address in the admin UI header
- `API_KEY_ACCOUNTS`: a key with bound accounts uses only those accounts
- `PROXY`: uses the system proxy when empty
- `MEDIA_OUTPUT` and `MEDIA_BASE_URL`: apply to Chat Completions and Messages content and Images `response_format: url`; `inline` embeds a data URL, while `url` saves files by content under `RUNTIME_DIR/media/` and serves `/media/<file>` without API key checks; when `MEDIA_BASE_URL` is empty, each request's address is used, following `X-Forwarded-Proto` behind a reverse proxy
- `ROUTING_STRATEGY`: among highest-priority accounts, `round-robin` picks the one with the fewest running requests and least recent pick on ties; `fill-first` fills one account first
- `PREFER_LOWER_TIER_MODELS`: within the same priority, prefers Free, then Pro, then Ultra accounts
- `FIRST_EVENT_HEDGE` and `FIRST_EVENT_HEDGE_MODELS`: when the primary request stalls before the first token, sends a backup request on an idle account (another account first, otherwise a free slot on the primary account); the first to emit continues while the other is cancelled, and background Responses requests are excluded; `auto` uses each model's 24-hour first-token p95 (skipping models with fewer than 20 samples or requests whose upstream body exceeds the recent p95 size), a positive integer sets seconds, and `off` disables hedging; backup requests count against account quota
- `NONSTREAM_KEEPALIVE`: when a non-streaming request waits over 10 seconds, sends status 200 and writes a newline every 10 seconds to keep reverse-proxy connections open
- `QUOTA_RESERVE_PERCENT`: skips an account's quota group when its 5-hour or weekly remaining share is at or below this percentage until that window resets; `0` skips only exhausted groups
- `WEEKLY_QUOTA_WARMUP`: after each automatic quota refresh, sends `Hi` from ready accounts whose weekly window has not started yet, at most once per account and quota group every 6 days
- `LOG_MAX_TOTAL_MB`: keeps `LOG_MAX_TOTAL_MB / 10 − 1` rotated files and removes the oldest beyond that
- `REQUEST_LOG_BODIES`: stores public request/response bodies and upstream attempt bodies of successful requests (failed upstream attempt bodies are always stored); each is truncated to 64 KiB, keeping the latest 1000 of each

Process environment variables override the same keys in `.env`.

## Command Line

| Command | Purpose |
| --- | --- |
| `serve` | Run the service; `--open-ui` opens the admin UI when ready |
| `login` | Sign in to a Google account and add it to the account store |
| `import` | Import the account of the local official Antigravity client |
| `switch <email>` | Switch the local official client to that account of the account store |
| `accounts` | Read the account directory and list accounts, plans, enabled state and time added |
| `status` | Show the state of the running service and each account |
| `version` | Print the program version |

- `login` flags: defaults to browser sign-in; `--code` signs in with a pasted authorization code; `--remote <address> --token <token>` uploads to a remote service (combinable with `--code`); `--proxy <URL>` sets the sign-in proxy and binds it to the account (`direct` for no proxy, combinable with other flags); `--cloud --provider <name>` signs in locally with Continue with Google Cloud enterprise WIF (cannot combine with `--code` or `--remote`)
- `status`: reads `/api/status` and `/api/accounts` over loopback without sign-in, printing version, listen address, start time and each account's state, plan and remaining share per quota bucket
- Global overrides: every command accepts `--listen`, `--auth`, `--runtime` and `--proxy` to override `LISTEN`, `AUTH_DIR`, `RUNTIME_DIR` and `PROXY` (`login --proxy` behaves as above)
- Exit status and desktop app: errors print the reason to stderr and exit with code 1 (e.g. `status` prints `服务未在 <LISTEN> 运行: <reason>` when stopped); output formats for `accounts` and `status` are in the [development guide](docs/development.md#子命令输出); the desktop executable accepts all subcommands, opens the desktop window when run with none, and starts in the tray only with `--hidden`

## Documentation

- [Development Guide](docs/development.md): build environment, project layout, configuration, scheduling, admin API and tests
- [Protocol Reference](docs/protocol.md): sign-in, initialization, request envelope, companion requests and stream decoding
- [Logging and Request Ledger](docs/logging.md): log format, event catalog, SQLite ledger, pricing and admin event stream
- [Reverse Engineering Guide](docs/reverse-engineering.md): capturing, static analysis and aligning with new official client releases
