# Antigravity2API

[简体中文](README.md) | [English](README_en.md)

Turns the Agent models of Google Antigravity into OpenAI, Claude and Gemini compatible APIs.

## Features

- **OpenAI compatible**: `/v1/chat/completions`, `/v1/completions`, `/v1/responses`, `/v1/models`, `/v1/images`, `/v1/audio`, `/v1/files`
- **Claude compatible**: `/v1/messages`, `/v1/messages/count_tokens`
- **Gemini compatible**: `/v1beta/models/{model}:generateContent`, `:streamGenerateContent`, `:countTokens`, Files API, Interactions API
- **Official client identity**: plain Go HTTP that reaches Cloud Code with the TLS and HTTP shape, User-Agent, request envelope, initialization order, periodic requests and companion requests of the official Antigravity client; every account uses the device profile generated when it was added
- **Agent models**: every Gemini, Claude and GPT-OSS Agent model, published from each account's live catalog; reasoning effort parameters map to the Low, Medium and High tiers of the same family
- **Streaming**: text, thinking and thought signatures stream incrementally; tool calls, parallel function calls, image and PDF input work across all four protocols
- **Images and speech**: the image generation model of the catalog generates and edits images, returned by all four protocols and the OpenAI Images endpoints; the speech transcription model of the catalog transcribes audio over the channel of the official voice input, shared by the OpenAI transcription and translation endpoints, Chat `input_audio` and Gemini audio input
- **Files**: the OpenAI, Claude and Gemini Files APIs store files in `RUNTIME_DIR/files/`, and file references in requests are inlined before sending
- **System prompt tiers**: full, medium, aggressive and custom tiers control how much of the official system prompt and user message wrapping is kept
- **Multi-account scheduling**: accounts are picked by account priority and the Gemini and third-party quota groups, requests rotate across accounts of the same priority, and a conversation stays on one account; selected models can prefer lower-plan accounts; exhausted quota cools down until reset, rate limits, capacity errors and server errors cool down or switch accounts by reason; account quota refreshes on a schedule, and the Accounts page can refresh all accounts or one at any time
- **Token counting**: input tokens are estimated locally per model family and calibrated with the upstream usage of the same conversation
- **Admin UI and desktop app**: the web admin UI provides a first-run guide, a dashboard, accounts, client setup, models, request records, usage and cost, in-flight requests, logs and settings, in Simplified Chinese, Traditional Chinese (Taiwan) and English; desktop apps for Windows, macOS and Linux add a tray icon, single instance and launch at login
- **Logs and request ledger**: structured logs record account selection, switches, cooldowns and upstream attempts per request; the SQLite ledger stores the account, model, tokens, cache hits and timing of every request and estimates cost with official API prices
- **Sign-in**: browser sign-in, pasted authorization code, import from the local official client, upload from another computer by pairing
- **Proxy**: global and per-account HTTP, HTTPS and SOCKS5 proxies; the system proxy is used when none is set

## Supported Models

| Model ID | Name | Thinking levels |
| --- | --- | --- |
| `gemini-4-argon` | Gemini 4 Argon | Low, Medium, High |
| `gemini-3.8-flash` | Gemini 3.8 Flash | Low, Medium, High |
| `claude-opus-5-5` | Claude Opus 5.5 | Low, Medium, High |
| `claude-sonnet-5-5` | Claude Sonnet 5.5 | Low, Medium, High |
| `gpt-oss-120b` | GPT-OSS 120B | Medium |

The service reads the Antigravity model catalog of every account at startup and saves it to `RUNTIME_DIR/catalogs/`; after a restart `/v1/models` lists the saved catalogs until the accounts finish initialization, and `/v1/models` is authoritative once it runs. The table lists the Agent models of the official model picker; each entry carries `thinking_levels` and `default_thinking_level`. The image generation model (for example `gemini-3.1-flash-image`) and the speech transcription model (for example `proactive-observer-v10`) of the catalog follow, with the capabilities `image_generation` and `transcription`. Other usable entries in the catalog are published with their catalog IDs, for example `claude-sonnet-4-6` and `claude-opus-4-6-thinking` in the catalog of a Pro account; after a restart, models that only appear in the catalogs of accounts still initializing return 503. Context windows, output limits and capabilities come from the catalog, and the account plan decides the available models and quota.

The thinking level is decided in this order:

1. A level named in the model name: a catalog ID with a level such as `claude-opus-5-5-high`, a parenthesized suffix such as `claude-opus-5-5(low)` (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max` or a thinking budget in tokens), or the `-thinking` suffix (High)
2. The thinking parameters of the request: OpenAI `reasoning_effort` `none`, `minimal` and `low` map to Low, `medium` to Medium, `high`, `xhigh` and `max` to High; Anthropic `thinking.budget_tokens` and Gemini `thinkingConfig.thinkingBudget` up to 1024 map to Low, up to 8192 to Medium, larger or `-1` to High
3. Medium, or the only level of the model

Model names also match the catalog as follows:

| Name | Result |
| --- | --- |
| With a date version or `-latest`, such as `claude-opus-5-5-20261001` | Matched after removing it |
| With the `models/` prefix | Matched after removing the prefix |
| An alias in `MODEL_MAPPING`, including one named like a catalog model | The mapped model |

## Quick Start

### Desktop App

Download the desktop app for your system from [Releases](https://github.com/Mag1cFall/Antigravity2API/releases): the portable `desktop-<version>-windows-amd64.zip` on Windows (unzip it and double-click `antigravity2api.exe`), `darwin-universal.dmg` on macOS, `AppImage` or `deb` on Linux. The app opens the admin UI in a window; sign in with a Google account or import the account of the local official client on the Accounts page. On Windows and macOS closing the window keeps the app in the tray; on Linux closing the window quits. `.env`, `auth/` and `runtime/` live next to `antigravity2api.exe` on Windows, so the whole folder can be copied or moved as is; on macOS they live in `~/Library/Application Support/Antigravity2API`, on Linux in `~/.config/Antigravity2API`.

### Command Line

Download and extract the command-line build from [Releases](https://github.com/Mag1cFall/Antigravity2API/releases): `windows-amd64.zip` on Windows, `tar.gz` on Linux and macOS. Add an account first:

```bash
./antigravity2api login
```

`login` opens the Google authorization page in the system browser. After authorization the account is saved to `auth/<email>/`; the service initializes it in the order of the official client at startup. Where no browser is available (a server reached over SSH), sign in with an authorization code:

```bash
./antigravity2api login --code
```

The command prints an authorization link. Open it in a browser on any device, authorize, and paste the code shown on the page back into the terminal.

When the official Antigravity client is signed in on this computer, import its account directly:

```bash
./antigravity2api import
```

`import` reads the token saved by the official client from the system credential store (service `gemini`, user `antigravity`) or from `~/.gemini/jetski-standalone-oauth-token`.

In the other direction, switch the official client to an account in the account store:

```bash
./antigravity2api switch <email>
```

`switch` closes the running official client, writes the account's token where the official client keeps its sign-in, and opens the official client again.

Start the service:

```bash
./antigravity2api serve
```

It listens on `127.0.0.1:8017` by default and reads `.env`, `auth/` and `runtime/` from the current directory. On Windows, double-clicking `antigravity2api.exe` runs `serve` and opens the admin UI in the browser once the service is ready.

### Remote Server

When the service runs on another machine, create a pairing token on the Accounts page of the server's admin UI (valid for 10 minutes), then sign in and upload from a computer with a browser:

```bash
./antigravity2api login --remote https://server.example.com --token <pairing token>
```

The Accounts page of the server's admin UI can also start an authorization code sign-in, or run `login --code` on the server.

### Docker

```bash
docker run -d --name antigravity2api -p 8017:8017 -v ./data:/data ghcr.io/mag1cfall/antigravity2api:latest
```

The image works in `/data`; `.env`, `auth/` and `runtime/` are stored in the mounted directory, and the container listens on `0.0.0.0:8017`. Set `API_KEYS` when the port is reachable from outside; opening the admin UI from outside the container requires `ADMIN_AUTH_ENABLED=true` and `ADMIN_PASSWORD`. Add accounts with the authorization code sign-in of the admin UI, by remote pairing, with `docker exec -it antigravity2api antigravity2api login --code`, or by copying `auth/<email>/` from another computer into `data/auth/`; the running service loads new accounts within 2 seconds.

### Proxy

`PROXY` is a global HTTP, HTTPS or SOCKS5 proxy such as `socks5://127.0.0.1:1080` or `http://user:pass@127.0.0.1:7890`. Sign-in, token refresh, account initialization, generation and companion requests all go through it. When `PROXY` is empty the system proxy is used: the `HTTPS_PROXY`, `HTTP_PROXY` and `NO_PROXY` environment variables, plus the system proxy settings on Windows. A single account can have its own proxy on the Accounts page of the admin UI; all requests of that account then use it. The proxy exit must be in a region supported by Antigravity.

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

- Requests to Chat, Responses or Claude with an image generation model return images: a Markdown image with a data URL in the Chat and Claude content, an `image_generation_call` in Responses. The Responses `image_generation` tool is offered to text models, and its calls are served by the image generation model
- `/v1/completions` treats `prompt` as the text before the cursor and `suffix` as the text after it, and the Tab completion model generates the completion at the cursor
- `/v1/images/generations` and `/v1/images/edits` use the image generation model of the catalog when `model` is omitted; `size` maps to the nearest aspect ratio and `quality` to a 1K, 2K or 4K resolution; the transparent area of the edit `mask` is the area to edit
- `/v1/audio/transcriptions` without `model` or with the transcription model transcribes WAV, MP3, FLAC or PCM audio with the transcription model; audio is sent at real-time pace, so a transcription takes about as long as the audio; `response_format` is `json` or `text`; `/v1/audio/translations` transcribes and then translates to English with the first text model of the catalog, or transcribes and translates with the given text model

### Claude Compatible

```text
POST   /v1/messages
POST   /v1/messages/count_tokens
GET    /v1/models
```

`/v1/models` with an `anthropic-version` header returns the Anthropic format, and `/v1/files` with an `anthropic-version` or `anthropic-beta: files-api-*` header returns the Anthropic Files API format.

### Gemini Compatible

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

`streamGenerateContent` without `alt=sse` returns a JSON array. `generationConfig.responseModalities` with `IMAGE` requires an image generation model; images return as `inlineData` parts, and `aspectRatio` and `imageSize` of `imageConfig` apply as given.

### Authentication

`API_KEYS` sets one or more public API keys. Requests carry a key with `Authorization: Bearer <key>`, `x-api-key`, `x-goog-api-key` or `?key=`; keys are not checked when `API_KEYS` is empty. Request records and usage are grouped by key.

### Admin UI and Service Status

Open `http://127.0.0.1:8017/` in a browser for the admin UI, which uses the `/api/*` endpoints. Loopback access needs no sign-in; after setting `ADMIN_AUTH_ENABLED=true`, `ADMIN_USERNAME` and `ADMIN_PASSWORD`, other devices can sign in. Settings saved in the UI are written back to `.env`; `LISTEN`, `AUTH_DIR` and `RUNTIME_DIR` take effect after a restart, the others on the next request. `GET /readyz` returns 200 once at least one account is ready.

## Client Setup

OpenAI SDKs use the base URL `http://127.0.0.1:8017/v1`; Anthropic and Google GenAI SDKs use `http://127.0.0.1:8017`. The Connect page of the admin UI generates ready-to-copy configs for Claude Code, Codex, OpenCode, Cherry Studio and curl from the current address, API key and live model catalog.

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

Map the small model name used by Claude Code background tasks to a catalog model with `MODEL_MAPPING`, for example `MODEL_MAPPING=claude-haiku-5-5:gemini-3.8-flash-low`.

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

| Tier | Official system prompt | User messages |
| --- | --- | --- |
| `full` | All sections, client system prompt appended | Wrapped in the official format with local time and model selection |
| `medium` | identity, guidelines, communication_style | Wrapped in the official format with local time |
| `aggressive` | identity only | Sent as is |
| `custom` | Sections selected in `SYSTEM_PROMPT_SECTIONS` | Wrapped as set by `SYSTEM_PROMPT_WRAP` |

"View text" on the settings page of the admin UI shows the system instruction and first user message each tier sends upstream. For the custom tier, the settings page lists the official sections to keep, each with its text, and the user message wrapper.

## Project Layout

```text
cmd/antigravity2api/  # entry point and subcommands
internal/
  antigravity/        # Cloud Code client: sign-in, initialization, catalog, envelope and stream decoding
  api/                # OpenAI, Claude and Gemini protocol adapters
  app/                # HTTP server, admin API and event stream
  auth/               # account files and official client credential import
  balancer/           # multi-account scheduling and cooldowns
  config/             # configuration loading, validation and saving
  desktop/            # desktop window and tray
  logging/            # service logs
  prompt/             # system prompt tiers and templates
  requestdb/          # request ledger, usage and cost
  service/            # generation service: account selection, conversations, retries and account lifecycle
  tokencount/         # local token counting
  webui/              # embedded admin UI
web/                  # admin UI source
build/                # desktop icons and packaging configuration
docs/                 # protocol, development and logging docs
```

## Environment Variables

| Variable | Description | Default |
| --- | --- | --- |
| `LISTEN` | Listen address of the public API, admin API and admin UI | `127.0.0.1:8017` |
| `API_KEYS` | Public API keys, comma separated | empty |
| `AUTH_DIR` | Account directory | `auth` |
| `RUNTIME_DIR` | Runtime data directory for `requests.db`, `logs/` and uploaded `files/` | `runtime` |
| `PROXY` | Global HTTP, HTTPS or SOCKS5 proxy; the system proxy is used when empty | empty |
| `SYSTEM_PROMPT` | System prompt tier: `full`, `medium`, `aggressive`, `custom` | `full` |
| `SYSTEM_PROMPT_SECTIONS` | Official section names the `custom` tier keeps, comma-separated, joined in the official order | `identity,guidelines,communication_style` |
| `SYSTEM_PROMPT_WRAP` | User message wrapper of the `custom` tier: `full` full official wrapper, `time` request and local time, `none` no wrapper | `time` |
| `MODEL_MAPPING` | Client model names mapped to catalog model IDs, each item `alias:model ID`, comma separated | empty |
| `ACCOUNT_CONCURRENCY` | Concurrent requests per account | `2` |
| `ROUTING_STRATEGY` | Among the highest-priority accounts, `round-robin` picks the one with the fewest running requests and, on ties, the one used longest ago; `fill-first` fills one account first | `round-robin` |
| `PREFER_LOWER_TIER_MODELS` | Model IDs that prefer lower-plan accounts, comma-separated; within the same priority, Free, then Pro, then Ultra accounts are used | empty |
| `REQUEST_TIMEOUT` | Maximum duration of a public request | `5m` |
| `QUOTA_REFRESH_INTERVAL` | Interval for reading the quota of every account, disabled ones included, in the background; minimum `1m`, `0` turns it off | `15m` |
| `TRUSTED_PROXIES` | IPs or CIDRs of trusted reverse proxies whose `X-Forwarded-For` decides the client IP | empty |
| `LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `LOG_FORMAT` | Log format: `json` or `text` | `json` |
| `LOG_FILE` | Also write logs to `RUNTIME_DIR/logs/`, rotating at 10 MB per file | `true` |
| `LOG_MAX_TOTAL_MB` | Total size limit of the log directory; the oldest files are removed beyond it; at least 20 | `500` |
| `REQUEST_LOG_BODIES` | Store public request and response bodies and upstream attempt bodies, each truncated to 64 KiB, keeping the latest 1000 | `false` |
| `ADMIN_AUTH_ENABLED` | Sign-in for remote access to the admin UI | `false` |
| `ADMIN_USERNAME` | Admin user name | `admin` |
| `ADMIN_PASSWORD` | Admin password | empty |

Process environment variables override the same keys in `.env`.

## Command Line

| Command | Purpose |
| --- | --- |
| `serve` | Run the service; `--open-ui` opens the admin UI when ready |
| `login` | Browser sign-in; `--code` signs in with a pasted authorization code; `--remote <address> --token <token>` uploads the account to a remote service |
| `import` | Import the account of the local official Antigravity client |
| `accounts` | List accounts, plans and states |
| `status` | Read account states and quota from the running service |
| `version` | Print the version |

Every command accepts `--listen`, `--auth`, `--runtime` and `--proxy`, overriding `LISTEN`, `AUTH_DIR`, `RUNTIME_DIR` and `PROXY`. The desktop executable accepts the same subcommands; without a subcommand it opens the desktop window, and `--hidden` starts with only the tray icon.

## Troubleshooting

### Windows Reserved Ports

If startup reports that the `LISTEN` port is in use but Task Manager shows no process holding it, the NAT service of Hyper-V, WSL2 or Docker may have reserved the port range. Run the following commands in an elevated PowerShell or CMD.

```powershell
netsh interface ipv4 show excludedportrange protocol=tcp
```

If `8017` falls between `Start Port` and `End Port`, change `LISTEN`, or restart WinNAT and check again:

```powershell
net stop winnat
net start winnat
```

### Account Verification

When the upstream requires account verification, the Accounts page shows a verification link and the account stops receiving requests. Open the link in a browser signed in to that account, complete the verification, then click Verify on the account.

## Notes

The internal Antigravity protocol changes with official client updates; model IDs follow the live catalog returned after startup. Issues and PRs are welcome.

See [docs/development.md](docs/development.md) for development and contribution, [docs/protocol.md](docs/protocol.md) for sign-in, initialization, the request envelope and stream decoding, [docs/logging.md](docs/logging.md) for logs and the request ledger, and [docs/reverse-engineering.md](docs/reverse-engineering.md) for capturing and analyzing the official client.
