package antigravity

// ClientVersion 为所模拟的官方客户端版本
const ClientVersion = "2.28.1"

// ElectronVersion 为官方客户端的 Electron 版本
const ElectronVersion = "44.5.1"

// ChromeVersion 为官方客户端 Electron 内置的 Chromium 版本
const ChromeVersion = "152.0.7977.130"

// changelists 为官方各系统 language_server 的构建号，即 User-Agent 中的 cl
var changelists = map[string]string{
	"windows": "1009128362",
	"darwin":  "1009128362",
	"linux":   "1009128362",
}

// serverRuntimes 为官方各系统 language_server 的 Go 运行时版本，即 unleash 的 platformVersion
var serverRuntimes = map[string]string{
	"windows": "go1.28-20261015-RC02 cl/1000353293 +18a2e1f59d X:boringcrypto,simd,nogolistexportnewformat",
	"darwin":  "go1.28-20261015-RC02 cl/1000353293 +18a2e1f59d X:fieldtrack,boringcrypto,simd,nogolistexportnewformat",
	"linux":   "go1.28-20261015-RC02 cl/1000353293 +18a2e1f59d X:fieldtrack,boringcrypto,simd,nogolistexportnewformat",
}
