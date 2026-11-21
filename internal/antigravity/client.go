package antigravity

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const (
	// EndpointProd 为登录后首次 loadCodeAssist 使用的 cloudcode 端点
	EndpointProd = "https://cloudcode-pa.googleapis.com"
	// EndpointDaily 为个人账户初始化后全部 cloudcode 请求使用的端点
	EndpointDaily    = "https://daily-cloudcode-pa.googleapis.com"
	endpointUserinfo = "https://www.googleapis.com/oauth2/v2/userinfo"
	endpointAICode   = "https://aicode.googleapis.com"
	// endpointConfig 为 aicode 实验配置接口
	endpointConfig = endpointAICode + "/v1alpha:fetchConfig"
	// ideType 为 loadCodeAssist 元数据中的客户端类型
	ideType = "ANTIGRAVITY"
	// updateChannel 为 listExperiments 元数据中的更新通道
	updateChannel = "stable"
)

// Proxy 为按请求选择代理的函数，与 http.Transport.Proxy 相同
type Proxy func(*http.Request) (*url.URL, error)

var (
	// projectReference 匹配错误文本中的 projects/<项目>
	projectReference = regexp.MustCompile(`(?i)\bprojects/[a-z0-9][a-z0-9._:-]*`)
	// emailAddress 匹配错误文本中的邮箱与服务账号
	emailAddress = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	// projectNumber 匹配错误文本中 consumer 或 project 之后的项目号
	projectNumber = regexp.MustCompile(`(?i)\b(consumer|project(?:[ _-]?(?:id|number))?)(\s*[:=]?\s*['"]?)[0-9]{6,}`)
)

// UpstreamError 表示 cloudcode 返回的非 2xx 响应或流中的 error 帧，RetryAfter 为响应头 Retry-After 的原文
type UpstreamError struct {
	Status     int
	Body       []byte
	RetryAfter string
}

// Error 返回状态码、上游 status、ErrorInfo reason 与 message，项目号、项目 ID 与邮箱替换为 ***；正文不是错误 JSON 时取正文
func (e *UpstreamError) Error() string {
	status, message := e.Message()
	if status == "" && message == "" {
		message = string(e.Body)
	}
	message = projectReference.ReplaceAllString(message, "projects/***")
	message = emailAddress.ReplaceAllString(message, "***")
	message = projectNumber.ReplaceAllString(message, "$1$2***")
	return strings.Join(strings.Fields(fmt.Sprintf("upstream %d %s %s", e.Status, status, e.Reason())), " ") + ": " + message
}

// errorBody 表示 cloudcode 错误正文
type errorBody struct {
	Error struct {
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Reason     string            `json:"reason"`
			RetryDelay string            `json:"retryDelay"`
			Metadata   map[string]string `json:"metadata"`
		} `json:"details"`
	} `json:"error"`
}

// body 解析错误正文，无法解析时为零值
func (e *UpstreamError) body() errorBody {
	var body errorBody
	json.Unmarshal(e.Body, &body)
	return body
}

// Reason 返回错误正文 details 中首个 ErrorInfo 的 reason
func (e *UpstreamError) Reason() string {
	for _, detail := range e.body().Error.Details {
		if detail.Reason != "" {
			return detail.Reason
		}
	}
	return ""
}

// anthropicError 表示 Claude 错误 message 中内嵌的 Anthropic 错误 JSON
type anthropicError struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Message 返回错误正文的 status 与 message，Claude 错误取内嵌 Anthropic 错误的 message
func (e *UpstreamError) Message() (status, message string) {
	body := e.body()
	var inner anthropicError
	if json.Unmarshal([]byte(body.Error.Message), &inner) == nil && inner.Error.Message != "" {
		return body.Error.Status, inner.Error.Message
	}
	return body.Error.Status, body.Error.Message
}

// AnthropicType 返回 Claude 错误内嵌的 Anthropic 错误类型，其他错误为空
func (e *UpstreamError) AnthropicType() string {
	var inner anthropicError
	json.Unmarshal([]byte(e.body().Error.Message), &inner)
	return inner.Error.Type
}

// overflowTokens 匹配 Claude 与 GPT-OSS 上下文超限错误中的输入 token 数与上限
var overflowTokens = regexp.MustCompile(`(?:prompt is too long: (\d+) tokens > (\d+) maximum|The input \((\d+) tokens\) is longer than the model's context length \((\d+) tokens\))`)

// requestedTokens 匹配 GPT-OSS 输入加输出上限超出上下文时的上下文长度、输入 token 数与输出上限
var requestedTokens = regexp.MustCompile(`maximum context length of (\d+) tokens\. You requested a total of \d+ tokens: (\d+) tokens from the input messages and (\d+) tokens for the completion`)

// ContextOverflow 返回错误是否为输入超出模型上下文窗口，以及上游给出的输入 token 数与输入可用的上限，Gemini 只给出上限；GPT-OSS 报输入加输出超限时上限为上下文长度减去输出上限
func (e *UpstreamError) ContextOverflow() (bool, int64, int64) {
	if e.Status != http.StatusBadRequest {
		return false, 0, 0
	}
	_, message := e.Message()
	if match := overflowTokens.FindStringSubmatch(message); match != nil {
		input, _ := strconv.ParseInt(match[1]+match[3], 10, 64)
		limit, _ := strconv.ParseInt(match[2]+match[4], 10, 64)
		return true, input, limit
	}
	if match := requestedTokens.FindStringSubmatch(message); match != nil {
		window, _ := strconv.ParseInt(match[1], 10, 64)
		input, _ := strconv.ParseInt(match[2], 10, 64)
		completion, _ := strconv.ParseInt(match[3], 10, 64)
		return true, input, window - completion
	}
	if strings.Contains(message, "The input token count exceeds the maximum number of tokens allowed") {
		var limit int64
		fmt.Sscanf(message[strings.LastIndex(message, "(")+1:], "%d", &limit)
		return true, 0, limit
	}
	return false, 0, 0
}

// ContentRejected 判断错误是否为上游按请求内容拒绝：没有 ErrorInfo 与恢复时刻的 429，由系统提示或消息中的文字触发，同一请求重发仍被拒绝
func (e *UpstreamError) ContentRejected() bool {
	return e.Status == http.StatusTooManyRequests && e.Reason() == "" && e.RetryAt(time.Now()).IsZero()
}

// RetryAt 返回上游错误给出的恢复时刻：RetryInfo 的 retryDelay、ErrorInfo 的 quotaResetDelay、quotaResetTimeStamp 与响应头 Retry-After（秒数或 HTTP 日期）中最晚者；都没有时为零值
func (e *UpstreamError) RetryAt(now time.Time) time.Time {
	var latest time.Time
	if seconds, err := strconv.ParseInt(e.RetryAfter, 10, 64); err == nil {
		latest = now.Add(time.Duration(seconds) * time.Second)
	} else if at, err := http.ParseTime(e.RetryAfter); err == nil {
		latest = at
	}
	for _, detail := range e.body().Error.Details {
		for _, value := range []string{detail.RetryDelay, detail.Metadata["quotaResetDelay"], detail.Metadata["quotaResetTimeStamp"]} {
			at, err := time.Parse(time.RFC3339, value)
			if delay, parsed := time.ParseDuration(value); parsed == nil {
				at, err = now.Add(delay), nil
			}
			if err == nil && at.After(latest) {
				latest = at
			}
		}
	}
	return latest
}

// Client 以官方客户端身份访问 cloudcode 的一个账户会话
// 各 HTTP 客户端的分工与认证见 docs/protocol.md“请求身份”
type Client struct {
	account string
	http    *http.Client
	web     *http.Client
	h2      *http.Client
	auth    *http.Client
	source  oauth2.TokenSource
	proxy   Proxy
	base    string
	project string
	device  Device
	mu      sync.Mutex
	state   companionState
	hub     hub
	observe func(Observed)
}

// Observed 是客户端一次成功读取的账户状态，只有读到的那一项非空
type Observed struct {
	Assist   *LoadCodeAssistResponse
	Settings *UserInfoResponse
	Catalog  *Catalog
	Quota    *QuotaSummary
}

// Observe 设置接收每次成功读取的套餐与项目、账户设置、目录与额度摘要的函数，在发出请求前调用
func (c *Client) Observe(observe func(Observed)) {
	c.observe = observe
}

// notify 把一次成功读取交给 Observe 设置的函数
func (c *Client) notify(observed Observed) {
	if c.observe != nil {
		c.observe(observed)
	}
}

// NewClient 创建以账户设备档案与代理发出请求的客户端
// 传输层与认证配置见 docs/protocol.md“请求身份”
func NewClient(account string, source oauth2.TokenSource, device Device, proxy Proxy) *Client {
	plain := newProxyTransport(proxy, http1Transport)
	multiplexed := newProxyTransport(proxy, http2Transport)
	return &Client{
		account: account,
		http:    &http.Client{Transport: &oauth2.Transport{Source: source, Base: plain}},
		web:     &http.Client{Transport: plain},
		h2:      &http.Client{Transport: multiplexed},
		auth:    &http.Client{Transport: &oauth2.Transport{Source: source, Base: multiplexed}},
		source:  source,
		proxy:   proxy,
		base:    EndpointDaily,
		device:  device,
	}
}

// http1Transport 返回只走 HTTP/1.1 的 Transport：ALPN 只带 http/1.1，每主机保留的空闲连接数与总数相同，其余字段同 http.DefaultTransport（空闲连接 90 s 后关闭）
func http1Transport(proxy Proxy) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxy
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	transport.TLSClientConfig = &tls.Config{NextProtos: []string{"http/1.1"}}
	transport.MaxIdleConnsPerHost = transport.MaxIdleConns
	return transport
}

// http2Transport 返回以代理选择函数克隆的 http.DefaultTransport，ALPN 为 h2 与 http/1.1
func http2Transport(proxy Proxy) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxy
	return transport
}

// proxyTransport 为每个请求按代理选择函数选出的代理使用各自的 Transport，HTTP/2 连接只在经同一代理的请求间复用
type proxyTransport struct {
	proxy      Proxy
	build      func(Proxy) *http.Transport
	mu         sync.Mutex
	transports map[string]*http.Transport
}

// newProxyTransport 返回按 proxy 选出的代理以 build 创建 Transport 的 proxyTransport
func newProxyTransport(proxy Proxy, build func(Proxy) *http.Transport) *proxyTransport {
	return &proxyTransport{proxy: proxy, build: build, transports: make(map[string]*http.Transport)}
}

// RoundTrip 以请求所选代理对应的 Transport 发出请求，没有代理选择函数或选出空代理时直连
func (t *proxyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var proxy *url.URL
	if t.proxy != nil {
		var err error
		if proxy, err = t.proxy(request); err != nil {
			return nil, err
		}
	}
	key := ""
	if proxy != nil {
		key = proxy.String()
	}
	t.mu.Lock()
	transport, ok := t.transports[key]
	if !ok {
		transport = t.build(func(*http.Request) (*url.URL, error) { return proxy, nil })
		t.transports[key] = transport
	}
	t.mu.Unlock()
	return transport.RoundTrip(request)
}

// connTrace 记录一次上游请求的连接经过：是否拿到连接、连接是否复用与空闲时长、请求是否写完、是否收到响应首字节与代理方案
type connTrace struct {
	mu                        sync.Mutex
	got, reused, wrote, first bool
	idle                      time.Duration
	scheme                    string
}

// connTraceKey 是请求上下文中 connTrace 的键
type connTraceKey struct{}

// describe 返回请求失败时所处的连接阶段与连接情况
func (t *connTrace) describe() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	phase := "读响应"
	switch {
	case !t.got:
		phase = "建立连接"
	case !t.wrote:
		phase = "写请求"
	case !t.first:
		phase = "等响应头"
	}
	return fmt.Sprintf("阶段=%s | 复用=%t | 空闲=%s | 代理=%s", phase, t.reused, t.idle, t.scheme)
}

// traced 给请求挂上 httptrace，在 debug 级记录连接复用、空闲时长与代理方案，并在上下文中记下连接经过供失败日志使用
func (c *Client) traced(request *http.Request) *http.Request {
	scheme := "direct"
	if c.proxy != nil {
		if proxy, err := c.proxy(request); err == nil && proxy != nil {
			scheme = proxy.Scheme
		}
	}
	state := &connTrace{scheme: scheme}
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			state.mu.Lock()
			state.got, state.reused, state.idle = true, info.Reused, info.IdleTime
			state.mu.Unlock()
			slog.DebugContext(request.Context(), fmt.Sprintf("上游连接 | 主机=%s | 复用=%t | 空闲=%s | 代理=%s", request.URL.Host, info.Reused, info.IdleTime, scheme), "source", c.account)
		},
		WroteRequest: func(httptrace.WroteRequestInfo) {
			state.mu.Lock()
			state.wrote = true
			state.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			state.mu.Lock()
			state.first = true
			state.mu.Unlock()
		},
	}
	return request.WithContext(context.WithValue(httptrace.WithClientTrace(request.Context(), trace), connTraceKey{}, state))
}

// connPhase 返回经 traced 发出的请求失败时的连接阶段说明，没有挂 traced 时为空
func connPhase(request *http.Request) string {
	if state, ok := request.Context().Value(connTraceKey{}).(*connTrace); ok {
		return " | " + state.describe()
	}
	return ""
}

// Project 返回 loadCodeAssist 返回的 cloudaicompanionProject
func (c *Client) Project() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.project
}

// Tier 表示 loadCodeAssist 返回的套餐
type Tier struct {
	ID               string   `json:"id"`
	AvailableCredits []Credit `json:"availableCredits"`
}

// Credit 表示付费套餐的一种积分：余额为零时上游省略 creditAmount，MinimumCreditAmountForUsage 为以积分生成所需的最低余额
type Credit struct {
	CreditType                  string `json:"creditType"`
	CreditAmount                string `json:"creditAmount"`
	MinimumCreditAmountForUsage string `json:"minimumCreditAmountForUsage"`
}

// LoadCodeAssistResponse 表示账户的套餐与项目
type LoadCodeAssistResponse struct {
	CurrentTier             Tier             `json:"currentTier"`
	PaidTier                *Tier            `json:"paidTier"`
	CloudAICompanionProject string           `json:"cloudaicompanionProject"`
	IneligibleTiers         []IneligibleTier `json:"ineligibleTiers,omitempty"`
}

// tier 返回付费套餐 ID，没有付费套餐时返回当前套餐 ID
func (r LoadCodeAssistResponse) tier() string {
	if r.PaidTier != nil {
		return r.PaidTier.ID
	}
	return r.CurrentTier.ID
}

// IneligibleTier 表示账户不能使用的套餐及原因，如 RESTRICTED_AGE 的年龄验证
type IneligibleTier struct {
	ReasonCode    string `json:"reasonCode"`
	ReasonMessage string `json:"reasonMessage"`
	TierID        string `json:"tierId,omitempty"`
	ValidationURL string `json:"validationUrl,omitempty"`
}

// Userinfo 表示 Google 账户资料
type Userinfo struct {
	Email   string `json:"email"`
	Picture string `json:"picture"`
}

// UserInfoResponse 表示 fetchUserInfo 返回的设置与地区
type UserInfoResponse struct {
	UserSettings struct {
		TelemetryEnabled bool `json:"telemetryEnabled"`
	} `json:"userSettings"`
	RegionCode string `json:"regionCode"`
}

// QuotaBucket 表示一个额度窗口
type QuotaBucket struct {
	BucketID          string  `json:"bucketId"`
	DisplayName       string  `json:"displayName"`
	Window            string  `json:"window"`
	ResetTime         string  `json:"resetTime"`
	RemainingFraction float64 `json:"remainingFraction"`
	// Disabled 表示上游停用了这个桶
	Disabled bool `json:"disabled,omitempty"`
}

// QuotaGroup 表示共享额度的一组模型
type QuotaGroup struct {
	Buckets     []QuotaBucket `json:"buckets"`
	DisplayName string        `json:"displayName"`
	Description string        `json:"description"`
}

// QuotaSummary 表示 retrieveUserQuotaSummary 的响应
type QuotaSummary struct {
	Groups      []QuotaGroup `json:"groups"`
	Description string       `json:"description"`
}

// Initialization 表示账户初始化取得的套餐与设置
type Initialization struct {
	Assist   LoadCodeAssistResponse
	Settings UserInfoResponse
}

// Initialize 按官方客户端登录后的请求顺序完成账户初始化
// 并发关系、等待间隔与伴随请求见 docs/protocol.md“账户初始化”
// 只有 loadCodeAssist 与 fetchAvailableModels 失败时返回错误
func (c *Client) Initialize(ctx context.Context) (Initialization, error) {
	var result Initialization
	user := optional(func() error {
		info, err := c.Userinfo(ctx)
		if err == nil {
			c.avatar(ctx, info.Picture)
		}
		return err
	})
	settings := optional(func() (err error) {
		result.Settings, err = c.FetchUserInfo(ctx)
		return err
	})
	assist := func() (err error) {
		result.Assist, err = c.LoadCodeAssist(ctx, EndpointDaily)
		return err
	}
	catalog := func() error {
		models, err := c.FetchAvailableModels(ctx)
		if err == nil {
			c.recordCatalog(models)
			c.listRemotes(context.WithoutCancel(ctx), loginLists)
		}
		return err
	}
	quota := optional(func() error {
		_, err := c.RetrieveUserQuotaSummary(ctx)
		return err
	})
	experiments := func() error {
		if c.listExperiments(ctx) == nil && pause(ctx, 113, 175) == nil {
			c.fetchConfig(ctx)
			if pause(ctx, 3, 15) == nil {
				c.report()
			}
		}
		return nil
	}
	nux := optional(func() error { return c.nuxes(ctx) })
	controls := optional(func() error {
		return c.post(ctx, c.base+"/v1internal:fetchAdminControls", projectRequest{Project: c.Project()}, nil)
	})
	if _, err := c.LoadCodeAssist(ctx, EndpointProd); err != nil {
		return Initialization{}, err
	}
	if err := parallel(user, sequence(assist, settings)); err != nil {
		return Initialization{}, err
	}
	err := parallel(user, sequence(assist, func() error {
		if err := pause(ctx, 42, 51); err != nil {
			return err
		}
		return parallel(experiments, sequence(func() error { return parallel(settings, quota, catalog, controls) }, nux))
	}))
	if err != nil {
		return Initialization{}, err
	}
	return result, nil
}

// optional 把伴随请求包成不中断初始化的步骤：官方客户端在这些请求失败时照常继续后续请求，失败由请求日志记录
func optional(step func() error) func() error {
	return func() error {
		step()
		return nil
	}
}

// sequence 依次执行各步骤，返回第一个错误并停止
func sequence(steps ...func() error) func() error {
	return func() error {
		for _, step := range steps {
			if err := step(); err != nil {
				return err
			}
		}
		return nil
	}
}

// parallel 并发执行各请求，全部结束后返回其中的错误，文字相同的错误只保留一个
func parallel(steps ...func() error) error {
	errs := make([]error, len(steps))
	var group sync.WaitGroup
	for index, step := range steps {
		group.Go(func() { errs[index] = step() })
	}
	group.Wait()
	var distinct []error
	for _, err := range errs {
		if err != nil && !slices.ContainsFunc(distinct, func(seen error) bool { return seen.Error() == err.Error() }) {
			distinct = append(distinct, err)
		}
	}
	return errors.Join(distinct...)
}

// avatar 以 HTTP/1.1 读取账户头像，请求不带 Authorization 与自定义请求头，结果不影响初始化
func (c *Client) avatar(ctx context.Context, picture string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, picture, nil)
	if err != nil {
		return
	}
	started := time.Now()
	request = c.traced(request)
	response, err := c.web.Do(request)
	if err != nil {
		c.logRequest(request, 0, started, err)
		return
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	c.logRequest(request, response.StatusCode, started, nil)
}

// LoadCodeAssist 向指定端点查询套餐与项目，把项目记为后续请求的 project，付费套餐（没有时为当前套餐）记入伴随状态
func (c *Client) LoadCodeAssist(ctx context.Context, base string) (LoadCodeAssistResponse, error) {
	var response LoadCodeAssistResponse
	if err := c.post(ctx, base+"/v1internal:loadCodeAssist", assistRequest{Metadata: assistMetadata{IDEType: ideType}}, &response); err != nil {
		return LoadCodeAssistResponse{}, err
	}
	c.mu.Lock()
	c.project, c.state.tier = response.CloudAICompanionProject, response.tier()
	c.mu.Unlock()
	c.notify(Observed{Assist: &response})
	return response, nil
}

// Assist 从账户当前的 cloudcode 端点读取套餐与项目：个人账户为 daily，企业 WIF 账户初始化后为 businessaicode
func (c *Client) Assist(ctx context.Context) (LoadCodeAssistResponse, error) {
	return c.LoadCodeAssist(ctx, c.base)
}

// Userinfo 读取 Google 账户资料，请求不设 User-Agent，由 net/http 写出默认值
func (c *Client) Userinfo(ctx context.Context) (Userinfo, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointUserinfo, nil)
	if err != nil {
		return Userinfo{}, err
	}
	var user Userinfo
	return user, c.do(request, &user)
}

// FetchUserInfo 读取账户设置与地区，并记入伴随状态与读取结束的时刻
func (c *Client) FetchUserInfo(ctx context.Context) (UserInfoResponse, error) {
	var response UserInfoResponse
	if err := c.post(ctx, c.base+"/v1internal:fetchUserInfo", projectRequest{Project: c.Project()}, &response); err != nil {
		return UserInfoResponse{}, err
	}
	c.mu.Lock()
	c.state.region, c.state.telemetry, c.hub.refreshed = response.RegionCode, response.UserSettings.TelemetryEnabled, time.Now()
	c.mu.Unlock()
	c.notify(Observed{Settings: &response})
	return response, nil
}

// RetrieveUserQuotaSummary 读取两组额度桶的剩余比例与重置时间；周额度用完时同组的 5 小时桶不生效，去掉这样的桶与上游标为 disabled 的桶
func (c *Client) RetrieveUserQuotaSummary(ctx context.Context) (QuotaSummary, error) {
	var summary QuotaSummary
	if err := c.post(ctx, c.base+"/v1internal:retrieveUserQuotaSummary", projectRequest{Project: c.Project()}, &summary); err != nil {
		return QuotaSummary{}, err
	}
	for index, group := range summary.Groups {
		weeklyOut := slices.ContainsFunc(group.Buckets, func(bucket QuotaBucket) bool { return bucket.Window == "weekly" && bucket.RemainingFraction <= 0 })
		summary.Groups[index].Buckets = slices.DeleteFunc(group.Buckets, func(bucket QuotaBucket) bool { return bucket.Disabled || weeklyOut && bucket.Window == "5h" })
	}
	c.notify(Observed{Quota: &summary})
	return summary, nil
}

type assistMetadata struct {
	IDEType string `json:"ideType"`
}

type assistRequest struct {
	Metadata assistMetadata `json:"metadata"`
}

type projectRequest struct {
	Project string `json:"project"`
}

type experimentsMetadata struct {
	UpdateChannel string `json:"updateChannel"`
}

type experimentsRequest struct {
	Project  string              `json:"project"`
	Metadata experimentsMetadata `json:"metadata"`
}

// post 以 Content-Length 发送 JSON 正文并解码 JSON 响应
func (c *Client) post(ctx context.Context, target string, body any, out any) error {
	payload, err := c.device.hubJSON(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", c.device.UserAgent())
	request.Header.Set("Content-Type", "application/json")
	c.userProject(request)
	return c.do(request, out)
}

// get 发送 GET 并解码 JSON 响应
func (c *Client) get(ctx context.Context, target string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", c.device.UserAgent())
	c.userProject(request)
	return c.do(request, out)
}

// do 发送非生成请求并解码 JSON 响应，结束后写 upstream.request 日志
func (c *Client) do(request *http.Request, out any) (err error) {
	started := time.Now()
	status := 0
	request = c.traced(request)
	defer func() { c.logRequest(request, status, started, err) }()
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	status = response.StatusCode
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode/100 != 2 {
		return &UpstreamError{Status: response.StatusCode, Body: data, RetryAfter: response.Header.Get("Retry-After")}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// logRequest 写一条非生成上游请求的日志，连接错误只写原因与连接阶段，不写带查询参数的地址
func (c *Client) logRequest(request *http.Request, status int, started time.Time, err error) {
	level := slog.LevelInfo
	message := fmt.Sprintf("上游请求 | %s %s%s | 状态=%d | 耗时=%dms", request.Method, request.URL.Host, request.URL.Path, status, time.Since(started).Milliseconds())
	if address, ok := errors.AsType[*url.Error](err); ok {
		err = address.Err
	}
	if err != nil {
		level, message = slog.LevelWarn, message+connPhase(request)+" | 错误="+err.Error()
	}
	slog.Log(request.Context(), level, message, "event", "upstream.request", "source", c.account)
}
