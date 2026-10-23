// Package config 读取、校验与保存 .env 配置，并应用本次启动的命令行覆盖
package config

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
)

// File 是服务读取与保存配置的 env 文件
const File = ".env"

// Keys 是 .env 的全部配置项，顺序即保存顺序
var Keys = []string{
	"LISTEN",
	"API_KEYS",
	"AUTH_DIR",
	"RUNTIME_DIR",
	"PROXY",
	"SYSTEM_PROMPT",
	"SYSTEM_PROMPT_SECTIONS",
	"SYSTEM_PROMPT_WRAP",
	"MODEL_MAPPING",
	"MEDIA_OUTPUT",
	"MEDIA_BASE_URL",
	"ACCOUNT_CONCURRENCY",
	"ROUTING_STRATEGY",
	"PREFER_LOWER_TIER_MODELS",
	"REQUEST_TIMEOUT",
	"QUOTA_REFRESH_INTERVAL",
	"QUOTA_RESERVE_PERCENT",
	"WEEKLY_QUOTA_WARMUP",
	"LOG_LEVEL",
	"LOG_FORMAT",
	"LOG_FILE",
	"LOG_MAX_TOTAL_MB",
	"REQUEST_LOG_BODIES",
	"ADMIN_AUTH_ENABLED",
	"ADMIN_USERNAME",
	"ADMIN_PASSWORD",
}

// RestartKeys 是重启服务后才生效的配置项，其余项保存后下一次请求生效
var RestartKeys = []string{"LISTEN", "AUTH_DIR", "RUNTIME_DIR"}

// SystemPrompts 是系统提示档位：全保留、中等、激进与自定义
var SystemPrompts = []string{"full", "medium", "aggressive", "custom"}

// MediaOutputs 是 MEDIA_OUTPUT 的取值：inline 以 data URL 内联，url 保存后以本服务链接引用
var MediaOutputs = []string{"inline", "url"}

// RoutingStrategies 是账户调度策略：轮询与先用满一个账户
var RoutingStrategies = []string{"round-robin", "fill-first"}

// LogLevels 是服务日志级别
var LogLevels = []string{"debug", "info", "warn", "error"}

// LogFormats 是服务日志格式
var LogFormats = []string{"json", "text"}

// LogFileMB 是单个日志文件的轮转大小
const LogFileMB = 10

// Config 保存服务的全部配置
type Config struct {
	Listen       string
	APIKeys      []string
	AuthDir      string
	RuntimeDir   string
	Proxy        string
	SystemPrompt string
	// SystemPromptSections 是 custom 档保留的官方段名
	SystemPromptSections []string
	// SystemPromptWrap 是 custom 档的 user 轮包装方式
	SystemPromptWrap string
	// ModelMapping 是客户端模型名到目录模型 ID 的映射，按书写顺序匹配
	ModelMapping []ModelAlias
	// MediaOutput 是 Chat、Messages 正文与 Images url 格式中生成媒体的输出方式
	MediaOutput string
	// MediaBaseURL 是媒体链接使用的服务地址，为空时取请求的地址
	MediaBaseURL       string
	AccountConcurrency int
	RoutingStrategy    string
	// PreferLowerTierModels 是同优先级内先用套餐低的账户的模型 ID
	PreferLowerTierModels []string
	RequestTimeout        time.Duration
	// QuotaRefreshInterval 是后台刷新全部账户额度的间隔，为 0 时不刷新
	QuotaRefreshInterval time.Duration
	LogLevel             string
	LogFormat            string
	LogFile              bool
	LogMaxTotalMB        int
	RequestLogBodies     bool
	AdminAuthEnabled     bool
	AdminUsername        string
	AdminPassword        string
	// QuotaReservePercent 是额度保留比例，额度组剩余比例不高于它时调度视为该组耗尽
	QuotaReservePercent int
	// WeeklyQuotaWarmup 为真时在定时额度刷新后为周额度窗口未开始计时的账户发一次最小请求
	WeeklyQuotaWarmup bool
}

// Default 返回可直接启动的默认配置
func Default() Config {
	return Config{
		Listen:               "127.0.0.1:8017",
		AuthDir:              "auth",
		RuntimeDir:           "runtime",
		SystemPrompt:         "full",
		SystemPromptSections: []string{"identity", "guidelines", "communication_style"},
		SystemPromptWrap:     prompt.WrapTime,
		MediaOutput:          "inline",
		AccountConcurrency:   2,
		RoutingStrategy:      "round-robin",
		RequestTimeout:       5 * time.Minute,
		QuotaRefreshInterval: 15 * time.Minute,
		LogLevel:             "info",
		LogFormat:            "json",
		LogFile:              true,
		LogMaxTotalMB:        500,
		AdminUsername:        "admin",
	}
}

// Load 读取 env 文件，进程环境变量中的同名项覆盖文件值，文件不存在时使用默认值
func Load(path string) (Config, error) {
	values, err := readEnvFile(path)
	if err != nil {
		return Config{}, err
	}
	for _, key := range Keys {
		if value, ok := os.LookupEnv(key); ok {
			values[key] = value
		}
	}
	cfg := Default()
	if err := cfg.Set(values); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Set 把 env 文本值解析到配置，未出现的项保持原值
func (c *Config) Set(values map[string]string) error {
	var err error
	text := func(key string, target *string) {
		if value, ok := values[key]; ok {
			*target = strings.TrimSpace(value)
		}
	}
	boolean := func(key string, target *bool) {
		if value, ok := values[key]; ok && err == nil {
			if *target, err = strconv.ParseBool(strings.TrimSpace(value)); err != nil {
				err = fmt.Errorf("%s 必须是 true 或 false", key)
			}
		}
	}
	integer := func(key string, target *int) {
		if value, ok := values[key]; ok && err == nil {
			if *target, err = strconv.Atoi(strings.TrimSpace(value)); err != nil {
				err = fmt.Errorf("%s 必须是整数", key)
			}
		}
	}
	duration := func(key string, target *time.Duration) {
		if value, ok := values[key]; ok && err == nil {
			if *target, err = time.ParseDuration(strings.TrimSpace(value)); err != nil {
				err = fmt.Errorf("%s 必须是时长，例如 30s 或 2m", key)
			}
		}
	}
	text("LISTEN", &c.Listen)
	if value, ok := values["API_KEYS"]; ok {
		c.APIKeys = SplitList(value)
	}
	text("AUTH_DIR", &c.AuthDir)
	text("RUNTIME_DIR", &c.RuntimeDir)
	text("PROXY", &c.Proxy)
	text("SYSTEM_PROMPT", &c.SystemPrompt)
	if value, ok := values["SYSTEM_PROMPT_SECTIONS"]; ok {
		c.SystemPromptSections = SplitList(value)
	}
	text("SYSTEM_PROMPT_WRAP", &c.SystemPromptWrap)
	if value, ok := values["MODEL_MAPPING"]; ok && err == nil {
		c.ModelMapping, err = parseModelMapping(value)
	}
	text("MEDIA_OUTPUT", &c.MediaOutput)
	c.MediaOutput = strings.ToLower(c.MediaOutput)
	text("MEDIA_BASE_URL", &c.MediaBaseURL)
	integer("ACCOUNT_CONCURRENCY", &c.AccountConcurrency)
	text("ROUTING_STRATEGY", &c.RoutingStrategy)
	if value, ok := values["PREFER_LOWER_TIER_MODELS"]; ok {
		c.PreferLowerTierModels = SplitList(value)
	}
	duration("REQUEST_TIMEOUT", &c.RequestTimeout)
	duration("QUOTA_REFRESH_INTERVAL", &c.QuotaRefreshInterval)
	integer("QUOTA_RESERVE_PERCENT", &c.QuotaReservePercent)
	boolean("WEEKLY_QUOTA_WARMUP", &c.WeeklyQuotaWarmup)
	text("LOG_LEVEL", &c.LogLevel)
	c.LogLevel = strings.ToLower(c.LogLevel)
	text("LOG_FORMAT", &c.LogFormat)
	c.LogFormat = strings.ToLower(c.LogFormat)
	boolean("LOG_FILE", &c.LogFile)
	integer("LOG_MAX_TOTAL_MB", &c.LogMaxTotalMB)
	boolean("REQUEST_LOG_BODIES", &c.RequestLogBodies)
	boolean("ADMIN_AUTH_ENABLED", &c.AdminAuthEnabled)
	text("ADMIN_USERNAME", &c.AdminUsername)
	if value, ok := values["ADMIN_PASSWORD"]; ok {
		c.AdminPassword = value
	}
	return err
}

// Values 返回各配置项的 env 文本值
func (c Config) Values() map[string]string {
	return map[string]string{
		"LISTEN":                   c.Listen,
		"API_KEYS":                 strings.Join(c.APIKeys, ","),
		"AUTH_DIR":                 c.AuthDir,
		"RUNTIME_DIR":              c.RuntimeDir,
		"PROXY":                    c.Proxy,
		"SYSTEM_PROMPT":            c.SystemPrompt,
		"SYSTEM_PROMPT_SECTIONS":   strings.Join(c.SystemPromptSections, ","),
		"SYSTEM_PROMPT_WRAP":       c.SystemPromptWrap,
		"MODEL_MAPPING":            formatModelMapping(c.ModelMapping),
		"MEDIA_OUTPUT":             c.MediaOutput,
		"MEDIA_BASE_URL":           c.MediaBaseURL,
		"ACCOUNT_CONCURRENCY":      strconv.Itoa(c.AccountConcurrency),
		"ROUTING_STRATEGY":         c.RoutingStrategy,
		"PREFER_LOWER_TIER_MODELS": strings.Join(c.PreferLowerTierModels, ","),
		"REQUEST_TIMEOUT":          formatDuration(c.RequestTimeout),
		"QUOTA_REFRESH_INTERVAL":   formatDuration(c.QuotaRefreshInterval),
		"QUOTA_RESERVE_PERCENT":    strconv.Itoa(c.QuotaReservePercent),
		"WEEKLY_QUOTA_WARMUP":      strconv.FormatBool(c.WeeklyQuotaWarmup),
		"LOG_LEVEL":                c.LogLevel,
		"LOG_FORMAT":               c.LogFormat,
		"LOG_FILE":                 strconv.FormatBool(c.LogFile),
		"LOG_MAX_TOTAL_MB":         strconv.Itoa(c.LogMaxTotalMB),
		"REQUEST_LOG_BODIES":       strconv.FormatBool(c.RequestLogBodies),
		"ADMIN_AUTH_ENABLED":       strconv.FormatBool(c.AdminAuthEnabled),
		"ADMIN_USERNAME":           c.AdminUsername,
		"ADMIN_PASSWORD":           c.AdminPassword,
	}
}

// Changed 返回与 other 取值不同的配置项，按 Keys 顺序
func (c Config) Changed(other Config) []string {
	left, right := c.Values(), other.Values()
	var keys []string
	for _, key := range Keys {
		if left[key] != right[key] {
			keys = append(keys, key)
		}
	}
	return keys
}

// Save 校验后把配置原子写入 env 文件
func (c Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	values := c.Values()
	var output strings.Builder
	for _, key := range Keys {
		output.WriteString(key + "=" + formatEnvValue(values[key]) + "\n")
	}
	return atomicWrite(path, []byte(output.String()))
}

// Validate 校验配置能否用于启动服务
func (c Config) Validate() error {
	if err := validateListen(c.Listen); err != nil {
		return err
	}
	if c.AuthDir == "" {
		return fmt.Errorf("AUTH_DIR 不能为空")
	}
	if c.RuntimeDir == "" {
		return fmt.Errorf("RUNTIME_DIR 不能为空")
	}
	if err := ValidateProxies(c.Proxy); err != nil {
		return err
	}
	if !slices.Contains(SystemPrompts, c.SystemPrompt) {
		return fmt.Errorf("SYSTEM_PROMPT 必须是 %s", strings.Join(SystemPrompts, "、"))
	}
	if _, err := prompt.New(prompt.TierCustom, prompt.Custom{Sections: c.SystemPromptSections, Wrap: c.SystemPromptWrap}); err != nil {
		return fmt.Errorf("SYSTEM_PROMPT_SECTIONS 或 SYSTEM_PROMPT_WRAP 无效: %w", err)
	}
	if !slices.Contains(MediaOutputs, c.MediaOutput) {
		return fmt.Errorf("MEDIA_OUTPUT 必须是 %s", strings.Join(MediaOutputs, "、"))
	}
	if c.MediaBaseURL != "" {
		if base, err := url.Parse(c.MediaBaseURL); err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
			return fmt.Errorf("MEDIA_BASE_URL 必须是 http 或 https 地址")
		}
	}
	if c.AccountConcurrency <= 0 {
		return fmt.Errorf("ACCOUNT_CONCURRENCY 必须是正整数")
	}
	if !slices.Contains(RoutingStrategies, c.RoutingStrategy) {
		return fmt.Errorf("ROUTING_STRATEGY 必须是 %s", strings.Join(RoutingStrategies, "、"))
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("REQUEST_TIMEOUT 必须是正数时长")
	}
	if c.QuotaRefreshInterval != 0 && c.QuotaRefreshInterval < time.Minute {
		return fmt.Errorf("QUOTA_REFRESH_INTERVAL 必须是 0 或不小于 1m 的时长")
	}
	if c.QuotaReservePercent < 0 || c.QuotaReservePercent > 99 {
		return fmt.Errorf("QUOTA_RESERVE_PERCENT 必须是 0 到 99 的整数")
	}
	if !slices.Contains(LogLevels, c.LogLevel) {
		return fmt.Errorf("LOG_LEVEL 必须是 %s", strings.Join(LogLevels, "、"))
	}
	if !slices.Contains(LogFormats, c.LogFormat) {
		return fmt.Errorf("LOG_FORMAT 必须是 %s", strings.Join(LogFormats, "、"))
	}
	if c.LogMaxTotalMB < 2*LogFileMB {
		return fmt.Errorf("LOG_MAX_TOTAL_MB 不能小于 %d", 2*LogFileMB)
	}
	if c.AdminAuthEnabled && (c.AdminUsername == "" || c.AdminPassword == "") {
		return fmt.Errorf("开启 ADMIN_AUTH_ENABLED 需要 ADMIN_USERNAME 与 ADMIN_PASSWORD")
	}
	return nil
}

// ProxyURL 返回不属于某个账户的请求使用的上游代理：PROXY 的第一个代理，为空时跟随系统代理
func (c *Config) ProxyURL(r *http.Request) (*url.URL, error) {
	if proxies := c.Proxies(); len(proxies) > 0 {
		return url.Parse(proxies[0])
	}
	return systemProxy(r)
}

// Proxies 返回 PROXY 中逗号分隔的代理
func (c *Config) Proxies() []string {
	return SplitList(c.Proxy)
}

// Overrides 是本次启动由命令行覆盖的配置项，空值表示未覆盖
type Overrides struct {
	Listen     string
	AuthDir    string
	RuntimeDir string
	Proxy      string
}

// BindFlags 在 flags 上注册 --listen、--auth、--runtime 与 --proxy
func (o *Overrides) BindFlags(flags *flag.FlagSet) {
	flags.StringVar(&o.Listen, "listen", "", "监听地址，覆盖 LISTEN")
	flags.StringVar(&o.AuthDir, "auth", "", "账户目录，覆盖 AUTH_DIR")
	flags.StringVar(&o.RuntimeDir, "runtime", "", "运行数据目录，覆盖 RUNTIME_DIR")
	flags.StringVar(&o.Proxy, "proxy", "", "上游代理，多个以逗号分隔，覆盖 PROXY")
}

// Apply 把命令行覆盖写入配置
func (o Overrides) Apply(c *Config) {
	if value := strings.TrimSpace(o.Listen); value != "" {
		c.Listen = value
	}
	if value := strings.TrimSpace(o.AuthDir); value != "" {
		c.AuthDir = value
	}
	if value := strings.TrimSpace(o.RuntimeDir); value != "" {
		c.RuntimeDir = value
	}
	if value := strings.TrimSpace(o.Proxy); value != "" {
		c.Proxy = value
	}
}

// ModelAlias 是一条客户端模型名到目录模型 ID 的映射
type ModelAlias struct {
	Alias string
	Model string
}

// parseModelMapping 解析逗号分隔的 别名:模型 ID，别名可含冒号，以最后一个冒号分隔
func parseModelMapping(value string) ([]ModelAlias, error) {
	var mapping []ModelAlias
	for _, item := range SplitList(value) {
		index := strings.LastIndexByte(item, ':')
		alias, model := strings.TrimSpace(item[:max(index, 0)]), strings.TrimSpace(item[index+1:])
		if index < 0 || alias == "" || model == "" {
			return nil, fmt.Errorf("MODEL_MAPPING 的每一项写成 别名:模型 ID，%q 不符合", item)
		}
		mapping = append(mapping, ModelAlias{Alias: alias, Model: model})
	}
	return mapping, nil
}

// formatModelMapping 把映射写回 .env 文本
func formatModelMapping(mapping []ModelAlias) string {
	items := make([]string, len(mapping))
	for index, alias := range mapping {
		items[index] = alias.Alias + ":" + alias.Model
	}
	return strings.Join(items, ",")
}

// SplitList 把逗号分隔的文本拆成去空白的非空项
func SplitList(value string) []string {
	var items []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

// ValidateProxies 校验逗号分隔的代理列表中的每一项
func ValidateProxies(value string) error {
	for _, proxy := range SplitList(value) {
		if err := ValidateProxy(proxy); err != nil {
			return err
		}
	}
	return nil
}

// ValidateProxy 校验 HTTP、HTTPS 或 SOCKS5 代理 URL，空值表示跟随系统代理
func ValidateProxy(value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || !slices.Contains([]string{"http", "https", "socks5"}, parsed.Scheme) {
		return fmt.Errorf("代理必须是 http、https 或 socks5 URL")
	}
	return nil
}

// formatDuration 去掉时长文本末尾的零分零秒，例如 5m0s 写作 5m
func formatDuration(value time.Duration) string {
	text := value.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}

// validateListen 要求 host:port 形式
func validateListen(value string) error {
	if _, _, err := net.SplitHostPort(value); err != nil {
		return fmt.Errorf("LISTEN 必须是 host:port")
	}
	return nil
}

// readEnvFile 读取 env 文件中的已知配置项，文件不存在时返回空集合
func readEnvFile(path string) (map[string]string, error) {
	values := map[string]string{}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return values, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, raw, ok := strings.Cut(text, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d 缺少等号", path, line)
		}
		key = strings.TrimSpace(key)
		if !slices.Contains(Keys, key) {
			continue
		}
		value, err := parseEnvValue(strings.TrimSpace(raw))
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		values[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取配置文件: %w", err)
	}
	return values, nil
}

// parseEnvValue 解析单引号、双引号或带行尾注释的裸值
func parseEnvValue(value string) (string, error) {
	switch {
	case value == "":
		return "", nil
	case value[0] == '\'':
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return "", fmt.Errorf("单引号未闭合")
		}
		return value[1 : len(value)-1], nil
	case value[0] == '"':
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("双引号值无效")
		}
		return parsed, nil
	}
	if index := strings.Index(value, " #"); index >= 0 {
		value = strings.TrimSpace(value[:index])
	}
	return value, nil
}

// formatEnvValue 为含空白、注释符或引号的值加双引号
func formatEnvValue(value string) string {
	if strings.ContainsAny(value, " \t\r\n#\"'") {
		return strconv.Quote(value)
	}
	return value
}

// atomicWrite 经同目录临时文件替换目标文件，权限为 0600
func atomicWrite(path string, data []byte) error {
	target, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("解析配置路径: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("创建配置目录: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".env-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时配置: %w", err)
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("设置配置权限: %w", err)
	}
	if _, err := bytes.NewReader(data).WriteTo(temporary); err != nil {
		temporary.Close()
		return fmt.Errorf("写入配置: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("同步配置: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭配置: %w", err)
	}
	if err := os.Rename(temporary.Name(), target); err != nil {
		return fmt.Errorf("替换配置: %w", err)
	}
	return nil
}
