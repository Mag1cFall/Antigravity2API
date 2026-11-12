package antigravity

// ClientVersion 为所模拟的官方客户端版本
const ClientVersion = "2.31.2"

// ElectronVersion 为官方客户端的 Electron 版本
const ElectronVersion = "44.9.1"

// ChromeVersion 为官方客户端 Electron 内置的 Chromium 版本
const ChromeVersion = "152.0.7977.167"

// changelists 为官方各系统与架构的 language_server 构建号，即 User-Agent 中的 cl，键为“系统/架构”
var changelists = map[string]string{
	"windows/amd64": "1019359392",
	"windows/arm64": "1019359392",
	"darwin/amd64":  "1019359392",
	"darwin/arm64":  "1019359392",
	"linux/amd64":   "1019359392",
	"linux/arm64":   "1019359392",
}
