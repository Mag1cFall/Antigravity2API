package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

const (
	// ClientVersion 为所模拟的官方客户端版本
	ClientVersion = "2.23.1"
	// ClientChangelist 为官方客户端 User-Agent 中的构建号
	ClientChangelist = "998881661"
	// EndpointProd 为登录后首次 loadCodeAssist 使用的 cloudcode 端点
	EndpointProd = "https://cloudcode-pa.googleapis.com"
	// EndpointDaily 为个人账户初始化后全部 cloudcode 请求使用的端点
	EndpointDaily    = "https://daily-cloudcode-pa.googleapis.com"
	endpointUserinfo = "https://www.googleapis.com/oauth2/v2/userinfo"
	endpointAICode   = "https://aicode.googleapis.com"
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

// UpstreamError 表示 cloudcode 返回的非 2xx 响应或流中的 error 帧
type UpstreamError struct {
	Status int
	Body   []byte
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

// RetryAt 返回上游错误给出的恢复时刻：RetryInfo 的 retryDelay，其次 ErrorInfo 的 quotaResetDelay 与 quotaResetTimeStamp；都没有时为零值
func (e *UpstreamError) RetryAt(now time.Time) time.Time {
	details := e.body().Error.Details
	for _, key := range []string{"retryDelay", "quotaResetDelay", "quotaResetTimeStamp"} {
		for _, detail := range details {
			value := detail.Metadata[key]
			if key == "retryDelay" {
				value = detail.RetryDelay
			}
			if delay, err := time.ParseDuration(value); err == nil {
				return now.Add(delay)
			}
			if at, err := time.Parse(time.RFC3339, value); err == nil {
				return at
			}
		}
	}
	return time.Time{}
}

// Client 以官方客户端身份访问 cloudcode 的一个账户会话，account 为日志中的账户，web 为头像等 HTTP/2 请求的客户端，auth 为带 Bearer 的 HTTP/2 客户端，state 为伴随请求所需的账户状态
type Client struct {
	account string
	http    *http.Client
	web     *http.Client
	auth    *http.Client
	source  oauth2.TokenSource
	proxy   Proxy
	base    string
	project string
	device  Device
	mu      sync.Mutex
	state   companionState
	hub     hub
}

// NewClient 创建以账户设备档案与代理发出请求的客户端：cloudcode、userinfo、aicode 走只开 HTTP/1 的 Transport，Authorization 由 oauth2.Transport 注入；头像、tokeninfo、unleash 与 clearcut 走克隆的 http.DefaultTransport，clearcut 另由 oauth2.Transport 注入 Authorization
func NewClient(account string, source oauth2.TokenSource, device Device, proxy Proxy) *Client {
	transport := &http.Transport{Proxy: proxy}
	transport.Protocols = new(http.Protocols)
	transport.Protocols.SetHTTP1(true)
	web := webTransport(proxy)
	return &Client{
		account: account,
		http:    &http.Client{Transport: &oauth2.Transport{Source: source, Base: transport}},
		web:     &http.Client{Transport: web},
		auth:    &http.Client{Transport: &oauth2.Transport{Source: source, Base: web}},
		source:  source,
		proxy:   proxy,
		base:    EndpointDaily,
		device:  device,
	}
}

// webTransport 返回克隆 http.DefaultTransport 并使用账户代理的 Transport，token 与头像请求经它以 HTTP/2 发出
func webTransport(proxy Proxy) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxy
	return transport
}

// traced 给请求挂上 httptrace，在 debug 级记录连接复用、空闲时长与代理方案
func (c *Client) traced(request *http.Request) *http.Request {
	scheme := "direct"
	if c.proxy != nil {
		if proxy, err := c.proxy(request); err == nil && proxy != nil {
			scheme = proxy.Scheme
		}
	}
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
		slog.DebugContext(request.Context(), fmt.Sprintf("上游连接 | 主机=%s | 复用=%t | 空闲=%s | 代理=%s", request.URL.Host, info.Reused, info.IdleTime, scheme), "source", c.account)
	}}
	return request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
}

// Project 返回 loadCodeAssist 返回的 cloudaicompanionProject
func (c *Client) Project() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.project
}

// Tier 表示 loadCodeAssist 返回的套餐
type Tier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// LoadCodeAssistResponse 表示账户的套餐与项目
type LoadCodeAssistResponse struct {
	CurrentTier             Tier   `json:"currentTier"`
	PaidTier                *Tier  `json:"paidTier"`
	AllowedTiers            []Tier `json:"allowedTiers"`
	CloudAICompanionProject string `json:"cloudaicompanionProject"`
	GCPManaged              bool   `json:"gcpManaged"`
}

// Userinfo 表示 Google 账户资料
type Userinfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	VerifiedEmail bool   `json:"verified_email"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	FamilyName    string `json:"family_name"`
	Picture       string `json:"picture"`
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

// Initialization 表示账户初始化取得的套餐、资料、设置、目录与额度
type Initialization struct {
	Assist   LoadCodeAssistResponse
	User     Userinfo
	Settings UserInfoResponse
	Catalog  Catalog
	Quota    QuotaSummary
}

// Initialize 按官方客户端登录后的请求与并发关系完成账户初始化：prod loadCodeAssist 之后两轮 userinfo 与 daily loadCodeAssist 并发，userinfo 之后读头像；第一轮 loadCodeAssist 之后是 fetchUserInfo，第二轮之后 fetchUserInfo、fetchAvailableModels 与 fetchAdminControls 并发，fetchUserInfo 之后 retrieveUserQuotaSummary 与 listExperiments 并发，listExperiments 之后 fetchConfig、cascadeNuxes 与第二次 listExperiments 并发，第二次之后再取一次 fetchConfig
func (c *Client) Initialize(ctx context.Context) (Initialization, error) {
	var result Initialization
	if _, err := c.LoadCodeAssist(ctx, EndpointProd); err != nil {
		return Initialization{}, err
	}
	experiments := func() error {
		return c.listExperiments(ctx)
	}
	config := func() error {
		return c.fetchConfig(ctx)
	}
	settings := func() (err error) {
		result.Settings, err = c.FetchUserInfo(ctx)
		return err
	}
	settled := func() error {
		if err := settings(); err != nil {
			return err
		}
		return parallel(func() (err error) {
			result.Quota, err = c.RetrieveUserQuotaSummary(ctx)
			return err
		}, func() error {
			if err := experiments(); err != nil {
				return err
			}
			return parallel(config, func() error {
				return c.get(ctx, c.base+"/v1internal/cascadeNuxes?page_size=1000", nil)
			}, func() error {
				if err := experiments(); err != nil {
					return err
				}
				return config()
			})
		})
	}
	for round := range 2 {
		err := parallel(func() (err error) {
			if result.User, err = c.Userinfo(ctx); err == nil {
				c.avatar(ctx, result.User.Picture)
			}
			return err
		}, func() (err error) {
			if result.Assist, err = c.LoadCodeAssist(ctx, EndpointDaily); err != nil {
				return err
			}
			if round == 0 {
				return settings()
			}
			return parallel(settled, func() (err error) {
				if result.Catalog, err = c.FetchAvailableModels(ctx); err == nil {
					c.recordCatalog(result.Catalog)
				}
				return err
			}, func() error {
				return c.post(ctx, c.base+"/v1internal:fetchAdminControls", projectRequest{Project: c.Project()}, nil)
			})
		})
		if err != nil {
			return Initialization{}, err
		}
	}
	return result, nil
}

// parallel 并发执行各请求，全部结束后返回其中的错误
func parallel(steps ...func() error) error {
	errs := make([]error, len(steps))
	var group sync.WaitGroup
	for index, step := range steps {
		group.Go(func() { errs[index] = step() })
	}
	group.Wait()
	return errors.Join(errs...)
}

// avatar 以 HTTP/2 读取账户头像，请求不带 Authorization 与自定义请求头，结果不影响初始化
func (c *Client) avatar(ctx context.Context, picture string) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, picture, nil)
	if err != nil {
		return
	}
	started := time.Now()
	response, err := c.web.Do(c.traced(request))
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
	tier := response.CurrentTier.ID
	if response.PaidTier != nil {
		tier = response.PaidTier.ID
	}
	c.mu.Lock()
	c.project, c.state.tier = response.CloudAICompanionProject, tier
	c.mu.Unlock()
	return response, nil
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

// FetchUserInfo 读取账户设置与地区，并记入伴随状态
func (c *Client) FetchUserInfo(ctx context.Context) (UserInfoResponse, error) {
	var response UserInfoResponse
	if err := c.post(ctx, c.base+"/v1internal:fetchUserInfo", projectRequest{Project: c.Project()}, &response); err != nil {
		return UserInfoResponse{}, err
	}
	c.mu.Lock()
	c.state.region, c.state.telemetry = response.RegionCode, response.UserSettings.TelemetryEnabled
	c.mu.Unlock()
	return response, nil
}

// RetrieveUserQuotaSummary 读取两组额度桶的剩余比例与重置时间
func (c *Client) RetrieveUserQuotaSummary(ctx context.Context) (QuotaSummary, error) {
	var summary QuotaSummary
	return summary, c.post(ctx, c.base+"/v1internal:retrieveUserQuotaSummary", projectRequest{Project: c.Project()}, &summary)
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
	payload, err := hubJSON(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", c.device.UserAgent())
	request.Header.Set("Content-Type", "application/json")
	return c.do(request, out)
}

// get 发送 GET 并解码 JSON 响应
func (c *Client) get(ctx context.Context, target string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", c.device.UserAgent())
	return c.do(request, out)
}

// do 发送非生成请求并解码 JSON 响应，结束后写 upstream.request 日志
func (c *Client) do(request *http.Request, out any) (err error) {
	started := time.Now()
	status := 0
	defer func() { c.logRequest(request, status, started, err) }()
	response, err := c.http.Do(c.traced(request))
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
		return &UpstreamError{Status: response.StatusCode, Body: data}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// logRequest 写一条非生成上游请求的日志，连接错误只写原因，不写带查询参数的地址
func (c *Client) logRequest(request *http.Request, status int, started time.Time, err error) {
	level := slog.LevelInfo
	message := fmt.Sprintf("上游请求 | %s %s%s | 状态=%d | 耗时=%dms", request.Method, request.URL.Host, request.URL.Path, status, time.Since(started).Milliseconds())
	if address, ok := errors.AsType[*url.Error](err); ok {
		err = address.Err
	}
	if err != nil {
		level, message = slog.LevelWarn, message+" | 错误="+err.Error()
	}
	slog.Log(request.Context(), level, message, "event", "upstream.request", "source", c.account)
}
