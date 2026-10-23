package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/balancer"
	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
	"github.com/Mag1cFall/Antigravity2API/internal/tokencount"
)

const (
	// retiredText 为上游对已下线模型以 HTTP 200 返回的提示中的固定片段
	retiredText = " is no longer available. Please switch to "
	// maxAccounts 为单个请求最多尝试的账户数
	maxAccounts = 12
	// maxLimitedAccounts 为没有恢复时刻的 429 限速在单个请求中最多尝试的账户数
	maxLimitedAccounts = 2
	// shortCooldown 为 429 限速没有给出恢复时间时的冷却时长，也是连续 INTERNAL 500 每次递增的冷却时长
	shortCooldown = time.Minute
	// licenseCooldown 为账户没有产品许可或订阅的 403 冷却该模型的时长
	licenseCooldown = time.Hour
	// maxRetryWait 为 429 限速的恢复时间不超过该时长时，在请求内等到恢复时间后再次使用该账户
	maxRetryWait = 5 * time.Second
)

// tierSuffixes 为目录中同族模型 ID 的档位后缀
var tierSuffixes = []string{"-low", "-medium", "-high"}

// Error 表示带 HTTP 状态语义的服务错误
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return e.Message
}

// HTTPStatus 返回公开协议使用的状态码
func (e *Error) HTTPStatus() int {
	return e.Status
}

// watchdog 是一次生成的上游 context：首个上游事件前与相邻两个上游事件之间超过 REQUEST_TIMEOUT 时以 ErrRequestTimeout 取消
type watchdog struct {
	context.Context
	cancel  context.CancelCauseFunc
	timer   *time.Timer
	timeout time.Duration
}

// watch 返回从现在开始计时的 watchdog，超时原因同时记入请求账本
func watch(ctx context.Context, timeout time.Duration) *watchdog {
	stream, cancel := context.WithCancelCause(ctx)
	w := &watchdog{Context: stream, cancel: cancel, timeout: timeout}
	w.timer = time.AfterFunc(timeout, func() {
		requestdb.TraceFrom(ctx).Cancel(requestdb.ErrRequestTimeout)
		cancel(requestdb.ErrRequestTimeout)
	})
	return w
}

// touch 在收到上游事件后重新计时
func (w *watchdog) touch() {
	w.timer.Reset(w.timeout)
}

// stop 停止计时并取消上游
func (w *watchdog) stop() {
	w.timer.Stop()
	w.cancel(context.Canceled)
}

// timedOut 判断上游是否因超时被取消
func (w *watchdog) timedOut() bool {
	return errors.Is(context.Cause(w), requestdb.ErrRequestTimeout)
}

// failure 返回生成开始前的错误：超时取消时为 ErrRequestTimeout
func (w *watchdog) failure(err error) error {
	if w.timedOut() {
		return requestdb.ErrRequestTimeout
	}
	return err
}

// model 按 resolve 的规则与账户目录确定请求使用的模型，没有就绪账户时列出各账户的原因，指定的账户须已就绪
func (s *Service) model(request antigravity.GenerateRequest) (antigravity.CatalogModel, error) {
	if s.pool.Len() == 0 {
		s.mu.Lock()
		reasons := []string{"没有就绪账户"}
		for id, reason := range s.failures {
			reasons = append(reasons, id+": "+reason)
		}
		s.mu.Unlock()
		slices.Sort(reasons[1:])
		return antigravity.CatalogModel{}, &Error{Status: http.StatusServiceUnavailable, Message: strings.Join(reasons, "\n")}
	}
	if request.AccountID != "" && s.account(request.AccountID) == nil {
		s.mu.Lock()
		reason := s.failures[request.AccountID]
		s.mu.Unlock()
		return antigravity.CatalogModel{}, &Error{Status: http.StatusServiceUnavailable, Message: strings.TrimSpace(fmt.Sprintf("账户 %s 未就绪 %s", request.AccountID, reason))}
	}
	return s.resolve(request.Model, request.Config)
}

// Generate 去掉客户端系统提示中的计费行，按实时配置与目录确定模型，图像与转写模型及要求输出图片的请求交给 generateMedia，其余请求取得会话后选定账户，以该账户的设备档案组装信封并转发事件，上游请求发出前与成功结束后记录会话 clearcut 事件；首字节前按错误原因换账户重试，429 限速的恢复时间在 maxRetryWait 内时等待后再次使用该账户、只重发生成请求且不计入换号，单个请求最多尝试的账户数取 accountLimit，指定账户时只用该账户；首个上游事件前与相邻两个上游事件之间受 REQUEST_TIMEOUT 限制，每次上游尝试写入请求账本
func (s *Service) Generate(ctx context.Context, request antigravity.GenerateRequest) (<-chan antigravity.Event, error) {
	request.System = dropBilling(request.System)
	cfg := s.live.Load()
	composer, err := s.composer(cfg)
	if err != nil {
		return nil, &Error{Status: http.StatusInternalServerError, Message: "系统提示模板: " + err.Error()}
	}
	s.configure(cfg)
	model, err := s.model(request)
	if err != nil {
		return nil, err
	}
	if request.Suffix != "" && model.Kind != antigravity.KindTab {
		var completions []string
		for _, candidate := range s.pool.Models() {
			if candidate.Kind == antigravity.KindTab {
				completions = append(completions, candidate.ID)
			}
		}
		return nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("model %s does not fill in between prompt and suffix; suffix requires a completion model (%s)", model.ID, strings.Join(completions, ", "))}
	}
	if model.Kind != "" || wantsImage(request.Config) {
		return s.generateMedia(ctx, request, model, composer.Tier())
	}
	digests := chain(request)
	session, err := s.conversations.begin(digests, replies(request))
	if err != nil {
		return nil, err
	}
	original, signed := request, signatureRecord{calls: session.Signatures, origins: session.Origins}
	request, out, err := prepare(original, model, signed)
	if err != nil {
		return nil, err
	}
	conversation := antigravity.NewConversation(session.Cascade, session.Trajectory, request.Contents)
	times := session.Times
	for len(times) < len(request.Contents) {
		times = append(times, time.Now().Unix())
	}
	trace := requestdb.TraceFrom(ctx)
	deadline := watch(ctx, cfg.RequestTimeout)
	tried := make(map[string]bool)
	waited := make(map[string]bool)
	if request.AccountID != "" {
		s.mu.Lock()
		for id := range s.accounts {
			tried[id] = id != request.AccountID
		}
		s.mu.Unlock()
	}
	var last error
	attempts := 0
	turns := make(map[string]*antigravity.SessionTurn)
	// open 在尚未尝试的账户上发出生成请求，每个账户只在第一次尝试前发出会话开头的伴随请求与会话事件，返回已开始的上游尝试；broken 为上一次尝试的流中断原因，此时换新的输出处理，已尝试 maxAccounts 个账户或超时后返回它
	open := func(broken error) (*upstreamStream, error) {
		if broken != nil {
			if attempts >= maxAccounts || deadline.Err() != nil {
				return nil, deadline.failure(broken)
			}
			last = broken
			_, out, _ = prepare(original, model, signed)
		}
		for {
			attempts++
			lease, err := s.pool.Acquire(deadline, model.ID, session.Cascade, tried, s.lowerTier(model))
			if err != nil {
				var cooldown *balancer.CooldownError
				if last != nil && errors.As(err, &cooldown) {
					return nil, cooled(last, s.pool.Until(model.ID))
				}
				return nil, deadline.failure(err)
			}
			tried[lease.ID] = true
			current := s.account(lease.ID)
			if current == nil {
				lease.Release()
				continue
			}
			turn := turns[lease.ID]
			var anchor time.Time
			switch {
			case turn != nil:
			case session.New:
				anchor = current.client.StartTrajectory(deadline, session.Trajectory, request.Contents)
			default:
				anchor = current.client.LoadMarketplace(deadline)
			}
			plugins := current.client.Plugins()
			composed := composer.Compose(request, lease.Model, session.Cascade, localTimes(times, current.device), current.device, plugins)
			envelope, err := antigravity.BuildEnvelope(current.client.Project(), lease.Model, conversation, composed.System, composed.Contents, composed.Tools, composed.Config, time.Now())
			if err != nil {
				lease.Release()
				return nil, &Error{Status: http.StatusBadRequest, Message: err.Error()}
			}
			if lease.Credits {
				envelope.EnabledCreditTypes = []string{antigravity.CreditGoogleOneAI}
			}
			out.prompt = envelope.Request
			if turn == nil {
				turn = &antigravity.SessionTurn{Conversation: conversation, New: session.New, Model: lease.Model, Contents: composed.Contents, Sections: sectionTokens(lease.Model, session.Cascade, current.device, plugins), Anchor: anchor}
				current.client.SessionSend(*turn)
				turns[lease.ID] = turn
			}
			trace.SetAccount(lease.ID)
			trace.SetUpstream(lease.Model.ID, lease.Model.QuotaGroup(), envelope.RequestType)
			trace.SetConversation(session.Cascade, conversation.Step)
			started := time.Now()
			upstream, exchange, err := current.client.StreamGenerateContent(deadline, envelope)
			if status(err) == http.StatusUnauthorized {
				trace.AddAttempt(attempt(lease.ID, exchange, started, err, antigravity.Event{}))
				current.source.Expire()
				started = time.Now()
				upstream, exchange, err = current.client.StreamGenerateContent(deadline, envelope)
			}
			if err == nil {
				return &upstreamStream{lease: lease, current: current, events: upstream, exchange: exchange, started: started, out: out, turn: turn}, nil
			}
			trace.AddAttempt(attempt(lease.ID, exchange, started, err, antigravity.Event{}))
			lease.Release()
			switched := s.fail(ctx, current, lease.Model, err, composer.Tier())
			if switched && waitRateLimit(deadline, lease.ID, err, waited) {
				delete(tried, lease.ID)
				attempts--
				continue
			}
			if !switched || attempts >= accountLimit(err) || deadline.Err() != nil {
				return nil, deadline.failure(err)
			}
			last = err
		}
	}
	stream, err := open(nil)
	if err != nil {
		deadline.stop()
		return nil, err
	}
	return s.relay(ctx, deadline, stream, open, request.Unary, digests, session.Cascade, composer.Tier()), nil
}

// upstreamStream 表示一次已开始的上游尝试：账户租约、上游事件、往返正文、开始时刻、本次尝试的输出处理与会话轮次
type upstreamStream struct {
	lease    *balancer.Lease
	current  *account
	events   <-chan antigravity.Event
	exchange *antigravity.Exchange
	started  time.Time
	out      *output
	turn     *antigravity.SessionTurn
}

// retryError 为上游错误附上账户冷却到期的时刻，作为客户端的可重试时刻
type retryError struct {
	error
	until time.Time
}

// Unwrap 返回上游错误
func (e *retryError) Unwrap() error { return e.error }

// RetryAt 返回账户冷却到期的时刻
func (e *retryError) RetryAt(time.Time) time.Time { return e.until }

// cooled 返回请求内最后一次上游错误，该模型的账户都在冷却时附上最早的到期时刻
func cooled(last error, until time.Time) error {
	if until.IsZero() {
		return last
	}
	return &retryError{last, until}
}

// waitRateLimit 在 429 限速给出不超过 maxRetryWait 的恢复时间、且本请求尚未在该账户上等待过时等到恢复时间，返回是否再次使用该账户；等待中超时或取消时返回 false
func waitRateLimit(deadline *watchdog, id string, err error, waited map[string]bool) bool {
	var upstream *antigravity.UpstreamError
	if waited[id] || !errors.As(err, &upstream) || upstream.Status != http.StatusTooManyRequests || quotaExhausted(upstream.Reason()) {
		return false
	}
	now := time.Now()
	wait := upstream.RetryAt(now).Sub(now)
	if wait <= 0 || wait > maxRetryWait {
		return false
	}
	waited[id] = true
	select {
	case <-time.After(wait):
		return true
	case <-deadline.Done():
		return false
	}
}

// attempt 返回一次上游尝试的账本记录：上游错误取其状态码，错误码与说明由账本从响应正文读取；连接错误状态码为 0；流开始后的尝试状态码为 200，流中错误写入 Error
func attempt(account string, exchange *antigravity.Exchange, started time.Time, err error, final antigravity.Event) requestdb.Attempt {
	record := requestdb.Attempt{Account: account, Status: http.StatusOK, TraceID: final.TraceID, ResponseID: final.ResponseID, DurationMS: time.Since(started).Milliseconds()}
	if exchange != nil {
		record.URL, record.Request, record.Response = exchange.URL, exchange.Request, exchange.Response
	}
	var upstream *antigravity.UpstreamError
	switch {
	case errors.As(err, &upstream) && final.Kind == "":
		record.Status = upstream.Status
	case err != nil && final.Kind == "":
		record.Status, record.Error = 0, err.Error()
	case err != nil:
		record.Error = err.Error()
	}
	return record
}

// fail 按上游错误原因处理账户并写日志：输入过滤与本地错误不换账户，认证失败与需要验证的 403 移出调度，token 端点 5xx 与 429 按网络错误换账户，违反条款的 403 停用，没有产品许可或订阅的 403 冷却该模型 licenseCooldown，QUOTA_EXHAUSTED 与 AI 积分不足把该账户与同额度池账户的额度组冷却到重置时间并在后台读取该账户的额度，图像模型只冷却该账户的该模型，没有 ErrorInfo 与 RetryInfo 的 429 由请求内容触发、直接返回，其他 429 短冷却该模型，连续 INTERNAL 500 递增冷却该模型；返回是否换账户重试
func (s *Service) fail(ctx context.Context, current *account, model antigravity.CatalogModel, err error, tier prompt.Tier) bool {
	var blocked *antigravity.PromptBlockedError
	var local *Error
	if errors.As(err, &blocked) || errors.As(err, &local) {
		return false
	}
	var upstream *antigravity.UpstreamError
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) && refused(retrieve) || status(err) == http.StatusUnauthorized {
		slog.WarnContext(ctx, "账户认证失败，移出调度 | 错误="+err.Error(), "source", current.id)
		s.mu.Lock()
		s.stop(current.id, "认证失败："+err.Error())
		s.mu.Unlock()
		return true
	}
	if !errors.As(err, &upstream) {
		if ctx.Err() == nil {
			s.requestProxyCheck()
		}
		return true
	}
	now := time.Now()
	state, message := upstream.Message()
	reason := upstream.Reason()
	limit := func(scope string, until time.Time) {
		s.pool.Limit(current.id, scope, until)
		s.saveCooldowns()
		slog.WarnContext(ctx, fmt.Sprintf("账户冷却 | 范围=%s | 时长=%s | 原因=%d %s %s | 系统提示档位=%s", scope, until.Sub(now).Round(time.Second), upstream.Status, state, reason, tier), "source", current.id)
	}
	switch {
	case upstream.Status == http.StatusForbidden && (reason == "VALIDATION_REQUIRED" || strings.Contains(strings.ToLower(message), "verify your account")):
		slog.WarnContext(ctx, "账户需要验证，停止请求", "source", current.id)
		s.mu.Lock()
		s.stop(current.id, "需要验证："+string(upstream.Body))
		s.mu.Unlock()
	case upstream.Status == http.StatusForbidden && (reason == "TOS_VIOLATION" || strings.Contains(strings.ToLower(message), "terms of service")):
		slog.ErrorContext(ctx, "账户违反服务条款，已停用", "source", current.id)
		s.disable(current.id, "违反服务条款已停用："+string(upstream.Body))
	case upstream.Status == http.StatusForbidden && (reason == "SUBSCRIPTION_REQUIRED" || strings.Contains(strings.ToLower(message), "license") || strings.Contains(strings.ToLower(message), "subscription")):
		limit(model.ID, now.Add(licenseCooldown))
	case quotaExhausted(reason):
		until := upstream.RetryAt(now)
		if reset, err := time.Parse(time.RFC3339, model.QuotaInfo.ResetTime); until.IsZero() && err == nil {
			until = reset
		}
		until = cmp.Or(until, now.Add(shortCooldown))
		if model.Kind == antigravity.KindImage {
			limit(model.ID, until)
			break
		}
		limit(model.QuotaGroup(), until)
		for _, peer := range s.pool.Peers(current.id, model.QuotaGroup()) {
			s.pool.Limit(peer, model.QuotaGroup(), until)
			s.saveCooldowns()
			slog.WarnContext(ctx, fmt.Sprintf("同额度池账户冷却 | 范围=%s | 时长=%s | 耗尽账户=%s", model.QuotaGroup(), until.Sub(now).Round(time.Second), current.id), "source", peer)
		}
		go func() {
			if err := s.RefreshQuota(context.WithoutCancel(ctx), current.id); err != nil {
				slog.WarnContext(ctx, "读取额度摘要失败 | 错误="+err.Error(), "source", current.id)
			}
		}()
	case reason == "MODEL_CAPACITY_EXHAUSTED":
		slog.WarnContext(ctx, "模型容量不足，换账户重试 | 模型="+model.ID, "source", current.id)
	case upstream.Status == http.StatusTooManyRequests && reason == "" && upstream.RetryAt(now).IsZero():
		slog.WarnContext(ctx, fmt.Sprintf("上游拒绝请求内容，不冷却账户 | 原因=%d %s %s | 系统提示档位=%s", upstream.Status, state, message, tier), "source", current.id)
		return false
	case upstream.Status == http.StatusTooManyRequests:
		limit(model.ID, cmp.Or(upstream.RetryAt(now), now.Add(shortCooldown)))
	case upstream.Status == http.StatusInternalServerError && state == "INTERNAL":
		s.mu.Lock()
		current.internal++
		penalty := time.Duration(current.internal) * shortCooldown
		s.mu.Unlock()
		limit(model.ID, now.Add(penalty))
	default:
		return false
	}
	return true
}

// refused 判断 token 端点是否拒绝了刷新，5xx 与 429 为端点暂时不可用
func refused(retrieve *oauth2.RetrieveError) bool {
	return retrieve.Response.StatusCode < http.StatusInternalServerError && retrieve.Response.StatusCode != http.StatusTooManyRequests
}

// accountLimit 返回以该错误换账户时单个请求最多尝试的账户数：没有恢复时刻且不是额度耗尽的 429 为 maxLimitedAccounts，其余为 maxAccounts
func accountLimit(err error) int {
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) && upstream.Status == http.StatusTooManyRequests && !quotaExhausted(upstream.Reason()) && upstream.RetryAt(time.Now()).IsZero() {
		return maxLimitedAccounts
	}
	return maxAccounts
}

// quotaExhausted 判断 ErrorInfo reason 是否为额度耗尽：额度用尽，或开启 AI 积分超额使用后额度与积分都已用尽
func quotaExhausted(reason string) bool {
	return reason == "QUOTA_EXHAUSTED" || reason == "INSUFFICIENT_G1_CREDITS_BALANCE"
}

// disable 把账户写为停用并移出调度，记下原因
func (s *Service) disable(id, reason string) {
	for _, record := range s.store.Accounts() {
		if record.ID == id {
			record.Enabled = false
			if err := s.store.Save(record); err != nil {
				reason += "；写回账户失败：" + err.Error()
			}
		}
	}
	s.mu.Lock()
	s.stop(id, reason)
	s.mu.Unlock()
}

// CountTokens 以与生成相同的模型、会话与系统提示估算请求的 promptTokenCount，没有记录时间的消息取当前时间；Gemini 与 Claude 族在同一会话于该模型上一次成功生成的消息是本次的前缀时，取其上游用量并只估算该次上游 contents 之后新增的 contents，GPT-OSS 族计整个信封
func (s *Service) CountTokens(_ context.Context, request antigravity.GenerateRequest) (int64, error) {
	request.System = dropBilling(request.System)
	composer, err := s.composer(s.live.Load())
	if err != nil {
		return 0, &Error{Status: http.StatusInternalServerError, Message: "系统提示模板: " + err.Error()}
	}
	model, err := s.model(request)
	if err != nil {
		return 0, err
	}
	digests := chain(request)
	session, ok := s.conversations.find(digests, replies(request))
	if !ok {
		session = conversation{Cascade: uuid.Nil.String(), Trajectory: uuid.Nil.String()}
	}
	request, _, err = prepare(request, model, signatureRecord{calls: session.Signatures, origins: session.Origins})
	if err != nil {
		return 0, err
	}
	times := session.Times
	for len(times) < len(request.Contents) {
		times = append(times, time.Now().Unix())
	}
	device, plugins := s.countProfile(request.AccountID)
	composed := composer.Compose(request, model, session.Cascade, localTimes(times, device), device, plugins)
	envelope, err := antigravity.BuildEnvelope("", model, antigravity.NewConversation(session.Cascade, session.Trajectory, request.Contents), composed.System, composed.Contents, composed.Tools, composed.Config, time.Now())
	if err != nil {
		return 0, &Error{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if usage, ok := s.conversations.usage(digests, model.ID); ok && model.ModelProvider != antigravity.ProviderOpenAI {
		return usage.Tokens + tokencount.Prompt(model.ModelProvider, antigravity.WireRequest{Contents: envelope.Request.Contents[usage.Contents:]}), nil
	}
	return tokencount.Prompt(model.ModelProvider, envelope.Request), nil
}

// countProfile 返回计数时组装系统提示所用的设备档案与推荐插件段的货架正文：取指定账户的，否则取最早添加的就绪账户的
func (s *Service) countProfile(id string) (antigravity.Device, string) {
	for _, record := range s.store.Accounts() {
		if current := s.account(record.ID); current != nil && (id == "" || id == record.ID) {
			return current.device, current.client.Plugins()
		}
	}
	return antigravity.Device{}, ""
}

// localTimes 把各消息首次出现的 Unix 秒按设备时区写成 RFC 3339
func localTimes(times []int64, device antigravity.Device) []string {
	location := device.Location()
	formatted := make([]string, len(times))
	for index, unix := range times {
		formatted[index] = time.Unix(unix, 0).In(location).Format(time.RFC3339)
	}
	return formatted
}

// relay 按尝试的 out 处理并转发上游事件，结束后按原因处理流中的错误、写入本次上游尝试；unary 时事件在生成结束后才写出；上游流以错误结束、客户端尚未收到事件且 fail 判定换账户时，经 open 在下一个账户上重新生成，open 为 nil 时不重新生成；正常结束时记下本次的上游输入用量、回答指纹与函数调用签名，再归还槽位并关闭输出，客户端收到结束时下一轮请求已能匹配会话；尝试带会话轮次时正常结束后记录会话回复事件；账户初始化后首次成功生成后读取额度摘要
func (s *Service) relay(ctx context.Context, deadline *watchdog, stream *upstreamStream, open func(error) (*upstreamStream, error), unary bool, digests []string, cascade string, tier prompt.Tier) <-chan antigravity.Event {
	events := make(chan antigravity.Event)
	go func() {
		defer deadline.stop()
		for {
			var held []antigravity.Event
			delivered := false
			emit := func(event antigravity.Event) {
				if unary || !delivered && event.Kind == antigravity.EventError {
					held = append(held, event)
					return
				}
				delivered = true
				deliver(ctx, events, event)
			}
			reported, final, err := forward(stream.events, emit, stream.out, deadline)
			switched := err != nil && s.fail(ctx, stream.current, stream.lease.Model, err, tier)
			requestdb.TraceFrom(ctx).AddAttempt(attempt(stream.lease.ID, stream.exchange, stream.started, err, final))
			if switched && open != nil && !delivered && ctx.Err() == nil {
				stream.lease.Release()
				next, err := open(err)
				if err != nil {
					deliver(ctx, events, antigravity.Event{Kind: antigravity.EventError, Err: err})
					close(events)
					return
				}
				slog.WarnContext(ctx, "上游流在客户端收到事件前中断，换账户重新生成", "source", next.lease.ID)
				stream = next
				continue
			}
			for _, event := range held {
				deliver(ctx, events, event)
			}
			succeeded := err == nil && reported != nil
			if succeeded && stream.out != nil {
				if err := s.conversations.record(cascade, digests, stream.out.reply(), stream.lease.Model.ID, len(stream.out.prompt.Contents), reported.InputTokens, stream.out.signatures()); err != nil {
					slog.WarnContext(ctx, "写入会话记录失败 | 错误="+err.Error(), "source", stream.lease.ID)
				}
			}
			stream.lease.Release()
			close(events)
			if succeeded && stream.turn != nil {
				stream.current.client.SessionReply(*stream.turn, sessionReply(stream.out, final))
			}
			if succeeded {
				s.quote(ctx, stream.current)
			}
			return
		}
	}()
	return events
}

// forward 把上游事件经 out 处理后交给 emit，返回最后一次转发的用量、上游最后一个事件与流中的错误；正文、思考、签名或函数调用事件使累计输出超过 out 的上限时丢弃它及其后的上游事件，以已转发部分的用量和 MAX_TOKENS 结束，Claude 与 GPT-OSS 的块不带逐块累计的输出用量，累计输出按已转发正文、思考与函数调用的本地计数，输入取块上的上游用量，没有时按信封的本地计数；out 发出结束事件或核对失败后关闭上游并丢弃其后的事件；首个事件为下线提示且其后流未完成时改发 404 错误事件；每个上游事件为 deadline 重新计时，上游因超时中断时错误改为 REQUEST_TIMEOUT；客户端取消后 emit 丢弃事件，上游照常排空
func forward(upstream <-chan antigravity.Event, emit func(antigravity.Event), out *output, deadline *watchdog) (*antigravity.Usage, antigravity.Event, error) {
	var failure error
	var usage *antigravity.Usage
	var final antigravity.Event
	var held []antigravity.Event
	var limit *int64
	if out != nil {
		limit = out.limit
	}
	var local antigravity.Usage
	counting := out != nil && (out.provider == antigravity.ProviderAnthropic || out.provider == antigravity.ProviderOpenAI)
	send := func(event antigravity.Event) {
		for _, processed := range out.process(event) {
			if processed.Kind == antigravity.EventError && failure == nil {
				failure = processed.Err
			}
			emit(processed)
		}
	}
	truncated := false
	count := 0
	for event := range upstream {
		deadline.touch()
		count++
		final = event
		if truncated {
			continue
		}
		if count == 1 && event.Kind == antigravity.EventText && strings.Contains(event.Text, retiredText) || len(held) != 0 && event.Kind == antigravity.EventUsage {
			held = append(held, event)
			continue
		}
		if event.Kind == antigravity.EventError && deadline.timedOut() {
			event.Err = requestdb.ErrRequestTimeout
		}
		if len(held) != 0 && event.Kind == antigravity.EventError && errors.Is(event.Err, antigravity.ErrIncompleteStream) {
			event.Err = &Error{Status: http.StatusNotFound, Message: held[0].Text}
			held = nil
		}
		for _, previous := range held {
			send(previous)
		}
		held = nil
		upstreamUsage := event.Usage
		if counting {
			upstreamUsage = nil
		}
		counted := local
		if limit != nil && upstreamUsage == nil {
			switch event.Kind {
			case antigravity.EventText:
				counted.OutputTokens += tokencount.Text(out.provider, event.Text)
			case antigravity.EventReasoning:
				counted.ReasoningTokens += tokencount.Text(out.provider, event.Text)
			case antigravity.EventToolCall:
				counted.OutputTokens += tokencount.Text(out.provider, event.ToolCall.Name) + tokencount.Text(out.provider, string(event.ToolCall.Arguments))
			}
		}
		if limit != nil && event.Kind != antigravity.EventUsage && produced(upstreamUsage, counted, out.provider) > *limit {
			truncated = true
			deadline.stop()
			var reported antigravity.Usage
			switch {
			case !counting && usage != nil:
				reported = *usage
			case !counting && event.Usage != nil:
				reported = *event.Usage
				reported.OutputTokens, reported.ReasoningTokens, reported.TotalTokens = 0, 0, reported.InputTokens
			default:
				if prompt := cmp.Or(usage, event.Usage); prompt != nil {
					reported.InputTokens, reported.CachedTokens = prompt.InputTokens, prompt.CachedTokens
				} else {
					reported.InputTokens = tokencount.Prompt(out.provider, out.prompt)
				}
				reported.OutputTokens, reported.ReasoningTokens = tokencount.Scale(out.provider, local.OutputTokens), tokencount.Scale(out.provider, local.ReasoningTokens)
				reported.TotalTokens = reported.InputTokens + reported.OutputTokens + reported.ReasoningTokens
			}
			usage = &reported
			send(antigravity.Event{Kind: antigravity.EventUsage, Usage: usage})
			send(antigravity.Event{Kind: antigravity.EventFinish, FinishReason: "MAX_TOKENS"})
			continue
		}
		local = counted
		if event.Usage != nil {
			usage = event.Usage
		}
		send(event)
		if out != nil && out.done && event.Kind != antigravity.EventFinish {
			truncated = true
			deadline.stop()
		}
	}
	for _, previous := range held {
		send(previous)
	}
	return usage, final, failure
}

// produced 返回累计输出与思考 token：有上游用量时取上游用量，否则按模型族分词的本地计数换算为模型族的 token 数
func produced(usage *antigravity.Usage, counted antigravity.Usage, provider string) int64 {
	if usage != nil {
		return usage.OutputTokens + usage.ReasoningTokens
	}
	return tokencount.Scale(provider, counted.OutputTokens) + tokencount.Scale(provider, counted.ReasoningTokens)
}

// deliver 写出一个事件，客户端已取消时丢弃
func deliver(ctx context.Context, events chan<- antigravity.Event, event antigravity.Event) {
	select {
	case events <- event:
	case <-ctx.Done():
	}
}

// effortLevel 返回客户端思考参数对应的档位，预算分界取 1024 与 8192，未指定与负数的动态预算为空，由模型默认档位决定
func effortLevel(config antigravity.GenerationConfig) string {
	switch strings.ToLower(config.ReasoningEffort) {
	case "none", "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh", "max":
		return "high"
	}
	if config.ThinkingBudget == nil || *config.ThinkingBudget < 0 {
		return ""
	}
	switch budget := *config.ThinkingBudget; {
	case budget <= 1024:
		return "low"
	case budget <= 8192:
		return "medium"
	default:
		return "high"
	}
}

// status 返回上游错误的 HTTP 状态码，其他错误为 0
func status(err error) int {
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) {
		return upstream.Status
	}
	return 0
}

// sectionTokens 返回官方系统提示各段的 token 数，顺序同 antigravity.SessionSections
func sectionTokens(model antigravity.CatalogModel, conversation string, device antigravity.Device, plugins string) []int {
	tokens := prompt.SectionTokens(model, conversation, device, plugins)
	out := make([]int, len(antigravity.SessionSections))
	for i, name := range antigravity.SessionSections {
		out[i] = tokens[name]
	}
	return out
}

// sessionReply 以已转发的回答、上游用量与最后一个上游事件组成会话事件所需的模型调用结果
func sessionReply(out *output, final antigravity.Event) antigravity.SessionReply {
	reply := antigravity.SessionReply{First: out.first, Thought: out.thought, Done: time.Now(), ResponseID: final.ResponseID, TraceID: final.TraceID, Model: final.ProviderModel}
	if out.usage != nil {
		reply.Usage = *out.usage
	}
	for _, part := range out.answer {
		if part.FunctionCall != nil {
			reply.Calls = append(reply.Calls, *part.FunctionCall)
		}
	}
	return reply
}
