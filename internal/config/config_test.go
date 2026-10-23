package config

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// isolateEnv 在测试期间移除进程环境中的全部配置项
func isolateEnv(t *testing.T) {
	t.Helper()
	for _, key := range Keys {
		if value, ok := os.LookupEnv(key); ok {
			os.Unsetenv(key)
			t.Cleanup(func() { os.Setenv(key, value) })
		}
	}
}

// TestSaveLoadRoundTrip 验证保存后的 env 文件读回同一配置，含引号、注释符与多个 key
func TestSaveLoadRoundTrip(t *testing.T) {
	isolateEnv(t)
	path := filepath.Join(t.TempDir(), ".env")
	cfg := Default()
	cfg.APIKeys = []string{"sk-one", "sk-two"}
	cfg.Proxy = "socks5://user:pass@127.0.0.1:1080"
	cfg.SystemPrompt = "custom"
	cfg.SystemPromptSections = []string{"identity", "skills"}
	cfg.SystemPromptWrap = "none"
	cfg.PreferLowerTierModels = []string{"gemini-3.8-flash", "gpt-oss-120b"}
	cfg.LogLevel = "debug"
	cfg.LogFile = false
	cfg.RequestLogBodies = true
	cfg.RoutingStrategy = "fill-first"
	cfg.RequestTimeout = 90 * time.Minute
	cfg.LogFormat = "text"
	cfg.AdminAuthEnabled = true
	cfg.AdminPassword = `p a#s"s'`
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if changed := cfg.Changed(loaded); len(changed) != 0 {
		t.Fatalf("读回后不同的项: %v", changed)
	}
}

// TestLoadDefaultsAndEnvOverride 验证文件缺失时使用默认值，进程环境覆盖文件值
func TestLoadDefaultsAndEnvOverride(t *testing.T) {
	isolateEnv(t)
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "missing.env"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:8017" || cfg.AuthDir != "auth" || cfg.RuntimeDir != "runtime" || cfg.LogMaxTotalMB != 500 {
		t.Fatalf("默认值 %+v", cfg)
	}
	path := filepath.Join(dir, ".env")
	content := "# 注释\nLISTEN=0.0.0.0:9000 # 行尾注释\nAPI_KEYS= a , ,b \nUNKNOWN=1\nLOG_LEVEL=WARN\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LISTEN", "127.0.0.1:9100")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9100" || !slices.Equal(cfg.APIKeys, []string{"a", "b"}) || cfg.LogLevel != "warn" {
		t.Fatalf("cfg=%+v", cfg)
	}
}

// TestValidateRejects 验证各类无效配置被拒绝
func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"监听地址无端口":  func(c *Config) { c.Listen = "127.0.0.1" },
		"代理协议无效":   func(c *Config) { c.Proxy = "ftp://127.0.0.1:21" },
		"未知系统提示档位": func(c *Config) { c.SystemPrompt = "none" },
		"自定义档段名无效": func(c *Config) { c.SystemPromptSections = []string{"identity", "tools"} },
		"自定义档包装无效": func(c *Config) { c.SystemPromptWrap = "partial" },
		"额度刷新间隔过短": func(c *Config) { c.QuotaRefreshInterval = 30 * time.Second },
		"并发为零":     func(c *Config) { c.AccountConcurrency = 0 },
		"未知日志级别":   func(c *Config) { c.LogLevel = "trace" },
		"日志总量过小":   func(c *Config) { c.LogMaxTotalMB = 10 },
		"未知调度策略":   func(c *Config) { c.RoutingStrategy = "random" },
		"超时为零":     func(c *Config) { c.RequestTimeout = 0 },
		"未知日志格式":   func(c *Config) { c.LogFormat = "xml" },
		"开启登录缺密码":  func(c *Config) { c.AdminAuthEnabled = true },
	}
	for name, mutate := range cases {
		cfg := Default()
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: 被接受", name)
		}
	}
	if err := Default().Validate(); err != nil {
		t.Fatalf("默认配置被拒绝: %v", err)
	}
}

// TestOverrides 验证命令行参数只覆盖显式给出的项
func TestOverrides(t *testing.T) {
	var overrides Overrides
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	overrides.BindFlags(flags)
	if err := flags.Parse([]string{"--listen", "0.0.0.0:8017", "--runtime", "data"}); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	overrides.Apply(&cfg)
	if cfg.Listen != "0.0.0.0:8017" || cfg.RuntimeDir != "data" || cfg.AuthDir != "auth" {
		t.Fatalf("cfg=%+v", cfg)
	}
	if changed := cfg.Changed(Default()); !slices.Equal(changed, []string{"LISTEN", "RUNTIME_DIR"}) {
		t.Fatalf("changed=%v", changed)
	}
}

// TestSetRejects 验证无法解析的文本值被拒绝
func TestSetRejects(t *testing.T) {
	cases := map[string]string{
		"REQUEST_TIMEOUT":  "5",
		"LOG_FILE":         "yes",
		"LOG_MAX_TOTAL_MB": "1g",
	}
	for key, value := range cases {
		cfg := Default()
		if err := cfg.Set(map[string]string{key: value}); err == nil {
			t.Errorf("%s=%s 被接受", key, value)
		}
	}
}

// TestSystemProxy 验证 Windows 代理设置的单一地址、按协议地址、SOCKS、例外列表与回环直连
func TestSystemProxy(t *testing.T) {
	cases := []struct {
		server, bypass, target, want string
	}{
		{"127.0.0.1:7890", "", "https://cloudcode-pa.googleapis.com/v1", "http://127.0.0.1:7890"},
		{"http=127.0.0.1:8080;https=127.0.0.1:8443", "", "https://oauth2.googleapis.com/token", "http://127.0.0.1:8443"},
		{"socks=127.0.0.1:1080", "", "https://oauth2.googleapis.com/token", "socks5://127.0.0.1:1080"},
		{"127.0.0.1:7890", "*.googleapis.com;<local>", "https://oauth2.googleapis.com/token", ""},
		{"127.0.0.1:7890", "<local>", "http://intranet/x", ""},
		{"127.0.0.1:7890", "", "http://localhost:51121/auth/callback", ""},
		{"", "", "https://oauth2.googleapis.com/token", ""},
	}
	for _, item := range cases {
		request := httptest.NewRequest(http.MethodGet, item.target, nil)
		proxy, err := parseSystemProxy(item.server, item.bypass, request)
		got := ""
		if proxy != nil {
			got = proxy.String()
		}
		if err != nil || got != item.want {
			t.Errorf("%s %s %s: %q %v", item.server, item.bypass, item.target, got, err)
		}
	}
}
