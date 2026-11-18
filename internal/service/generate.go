package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
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
	// unsupportedText 为上游对低于最低客户端版本的请求以 HTTP 200 返回的提示开头
	unsupportedText = "This version of Antigravity is no longer supported."
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
	// regenerations 为客户端尚未收到正文、函数调用与媒体时重新生成的次数上限
	regenerations = 3
)

// tierSuffixes 为目录中同族模型 ID 的档位后缀
var tierSuffixes = []string{"-low", "-medium", "-high", "-xhigh"}

// Error 表示带 HTTP 状态语义的服务错误，States 是账户都不可调度时各账户的英文状态
type Error struct {
	Status  int
	Message string
	States  []string
}

// Error 返回错误说明
func (e *Error) Error() string {
	return e.Message
}

// HTTPStatus 返回公开协议使用的状态码
func (e *Error) HTTPStatus() int {
	return e.Status
}

// AccountStates 返回账户都不可调度时各账户的英文状态，公开错误按它给出账户数
func (e *Error) AccountStates() []string {
	return e.States
}

// accountState 返回不可调度账户在公开错误中的英文状态：已停用、待验证、初始化失败或初始化中
func accountState(enabled bool, reason string, failed bool) string {
	switch {
	case !enabled:
		return "disabled"
	case strings.HasPrefix(reason, verifyReason(nil)):
		return "verification required"
	case failed:
		return "unavailable"
	}
	return "initializing"
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

// model 按 resolve 的规则与账户目录确定请求使用的模型，没有就绪账户时列出账户库中各账户的原因；指定的账户须在账户库中、属于请求 API key 可用的账户且已就绪
func (s *Service) model(request antigravity.GenerateRequest) (antigravity.CatalogModel, error) {
	if request.AccountID != "" {
		if _, ok := s.store.Account(request.AccountID); !ok {
			return antigravity.CatalogModel{}, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("账户 %s 不存在", request.AccountID)}
		}
		if len(request.Accounts) > 0 && !slices.Contains(request.Accounts, request.AccountID) {
			return antigravity.CatalogModel{}, &Error{Status: http.StatusForbidden, Message: fmt.Sprintf("账户 %s 不在该 API key 可用的账户中", request.AccountID)}
		}
	}
	if s.pool.Len() == 0 {
		s.mu.Lock()
		reasons, states := []string{"没有就绪账户"}, []string(nil)
		for _, record := range s.store.Accounts() {
			reason, failed := s.failures[record.ID]
			states = append(states, accountState(record.Enabled, reason, failed))
			switch {
			case failed:
			case !record.Enabled:
				reason = "已停用"
			default:
				reason = "初始化中"
			}
			reasons = append(reasons, record.ID+": "+reason)
		}
		s.mu.Unlock()
		return antigravity.CatalogModel{}, &Error{Status: http.StatusServiceUnavailable, Message: strings.Join(reasons, "\n"), States: states}
	}
	if request.AccountID != "" && s.account(request.AccountID) == nil {
		s.mu.Lock()
		reason, failed := s.failures[request.AccountID]
		s.mu.Unlock()
		record, _ := s.store.Account(request.AccountID)
		return antigravity.CatalogModel{}, &Error{Status: http.StatusServiceUnavailable, Message: strings.TrimSpace(fmt.Sprintf("账户 %s 未就绪 %s", request.AccountID, reason)), States: []string{accountState(record.Enabled, reason, failed)}}
	}
	return s.resolve(request.Model, request.Config)
}

// Generate 去掉客户端系统提示中的计费行，按实时配置与目录确定模型，图像与转写模型及要求输出图片的请求交给 generateMedia，其余请求取得会话后选定账户，以该账户的设备档案组装信封并转发事件，上游请求发出前与成功结束后记录会话 clearcut 事件；首字节前按错误原因换账户重试，429 限速的恢复时间在 maxRetryWait 内时等待后再次使用该账户、只重发生成请求且不计入换号，单个请求最多尝试的账户数取 accountLimit，指定账户时只用该账户；原请求等待首个上游事件达到 hedgeDelay 时发起一个备用请求（后台响应不发起），先返回首个事件的一方继续；首个上游事件前与相邻两个上游事件之间受 REQUEST_TIMEOUT 限制，每次上游尝试写入请求账本
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
	id := identify(request)
	session, err := s.conversations.begin(id)
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
	// reconnect 为最近一次连接错误的账户，没有其他可调度账户时在它上面再试一次，每个请求只再试一次
	reconnect, reconnected := "", false
	var last error
	attempts := 0
	turns := make(map[string]*antigravity.SessionTurn)
	// begin 以租约账户的设备档案组装信封并在后台发出生成请求，每个账户只在第一次尝试前发出会话开头的伴随请求与会话事件；备用请求不改写请求账本的执行账户与上游字段
	begin := func(lease *balancer.Lease, current *account, out *output, hedge bool) (*upstreamStream, error) {
		turn := turns[lease.ID]
		var anchor time.Time
		switch {
		case turn != nil:
		case session.New:
			anchor = current.client.StartTrajectory(deadline, session.Trajectory, request.Contents)
		default:
			anchor = current.client.LoadMarketplace(deadline)
		}
		plugins, experiments := current.client.Plugins(), current.client.Experiments()
		composed, err := composer.Compose(request, lease.Model, session.Cascade, localTimes(times, current.device), current.device, plugins, experiments)
		if err != nil {
			return nil, &Error{Status: http.StatusInternalServerError, Message: "系统提示模板: " + err.Error()}
		}
		envelope, err := antigravity.BuildEnvelope(current.client.Project(), lease.Model, conversation, composed.System, composed.Contents, composed.Tools, composed.Config, time.Now())
		if err != nil {
			return nil, &Error{Status: http.StatusBadRequest, Message: err.Error()}
		}
		if lease.Credits {
			envelope.EnabledCreditTypes = []string{antigravity.CreditGoogleOneAI}
		}
		out.prompt = envelope.Request
		if turn == nil {
			turn = &antigravity.SessionTurn{Conversation: conversation, New: session.New, Model: lease.Model, Contents: composed.Contents, Sections: sectionTokens(lease.Model, session.Cascade, current.device, plugins, experiments), Anchor: anchor}
			current.client.SessionSend(*turn)
			turns[lease.ID] = turn
		}
		if !hedge {
			trace.SetAccount(lease.ID)
			trace.SetUpstream(lease.Model.ID, lease.Model.QuotaGroup(), envelope.RequestType)
			trace.SetConversation(session.Cascade, conversation.Step)
		}
		size, err := current.client.Encode(&envelope)
		if err != nil {
			return nil, &Error{Status: http.StatusBadRequest, Message: err.Error()}
		}
		return send(deadline, trace, &upstreamStream{lease: lease, current: current, out: out, turn: turn, hedge: hedge, bytes: size}, envelope), nil
	}
	// settle 把失败的尝试写入账本、归还槽位并按失败原因处理账户，返回是否换账户重试
	settle := func(stream *upstreamStream) bool {
		trace.AddAttempt(stream.record(stream.err, antigravity.Event{}))
		stream.lease.Release()
		return s.fail(ctx, stream.current, stream.lease.Model, stream.err, composer.Tier())
	}
	hedged := false
	// open 在尚未尝试的账户上发出生成请求并等待首个上游事件，本请求还没有发起过备用请求时按 hedgeDelay 发起一个，返回胜出的上游尝试；broken 为上一次尝试的流中断原因，此时换新的输出处理，已尝试 maxAccounts 个账户或超时后返回它；again 不为空时该账户可再次使用且不计入尝试数；没有其他可调度账户时在最近一次连接错误的账户上再试一次
	open := func(broken error, again string) (*upstreamStream, error) {
		if again != "" {
			delete(tried, again)
			attempts--
		}
		if broken != nil {
			if attempts >= maxAccounts || deadline.Err() != nil {
				return nil, deadline.failure(broken)
			}
			last = broken
			_, out, _ = prepare(original, model, signed)
		}
		for {
			attempts++
			lease, err := s.pool.Acquire(deadline, model.ID, session.Cascade, skip(request, tried, s.store.Enabled), s.lowerTier(model))
			if err != nil && reconnect != "" && !reconnected && deadline.Err() == nil {
				reconnected = true
				delete(tried, reconnect)
				attempts--
				continue
			}
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
			stream, err := begin(lease, current, out, false)
			if err != nil {
				lease.Release()
				return nil, err
			}
			var plan *hedgePlan
			if delay, ok := s.hedgeDelay(cfg, original, lease.Model, stream.bytes); ok && !hedged {
				primary := lease.ID
				plan = &hedgePlan{delay: delay, start: func() *upstreamStream {
					backup := s.hedgeLease(request, primary, tried, model)
					if backup == nil {
						return nil
					}
					current := s.account(backup.ID)
					if current == nil {
						backup.Release()
						return nil
					}
					_, out, _ := prepare(original, model, signed)
					hedge, err := begin(backup, current, out, true)
					if err != nil {
						backup.Release()
						return nil
					}
					hedged, tried[backup.ID] = true, true
					attempts++
					return hedge
				}}
			}
			stream = s.await(ctx, stream, plan, func(failed *upstreamStream) { settle(failed) })
			if stream.err == nil {
				if stream.hedge {
					trace.SetAccount(stream.lease.ID)
				}
				return stream, nil
			}
			err = stream.err
			switched := settle(stream)
			if switched && waitRateLimit(deadline, stream.lease.ID, err, waited) {
				delete(tried, stream.lease.ID)
				attempts--
				continue
			}
			if switched && connectionFailure(err) {
				reconnect = stream.lease.ID
			}
			if !switched || attempts >= accountLimit(err) || deadline.Err() != nil {
				return nil, deadline.failure(err)
			}
			if last == nil || !unauthorized(err) {
				last = err
			}
		}
	}
	stream, err := open(nil, "")
	if err != nil {
		deadline.stop()
		return nil, err
	}
	return s.relay(ctx, deadline, stream, open, request, id, session.Cascade, composer.Tier()), nil
}

// upstreamStream 表示一次上游尝试：账户租约、上游事件、往返正文、开始时刻、本次尝试的输出处理与会话轮次；hedge 标记备用请求，begun 为发出时刻，first 与 firstAt 为首个上游事件及其到达时刻，err 为流开始前的错误，cancel 取消本次尝试，done 在流开始失败或收到首个事件后关闭，standby 为首个事件落后、仍在进行的另一方
type upstreamStream struct {
	lease    *balancer.Lease
	current  *account
	events   <-chan antigravity.Event
	exchange *antigravity.Exchange
	started  time.Time
	out      *output
	turn     *antigravity.SessionTurn
	hedge    bool
	begun    time.Time
	first    antigravity.Event
	firstAt  time.Time
	err      error
	cancel   context.CancelFunc
	done     chan struct{}
	bytes    int
	standby  *upstreamStream
}

// record 返回本次尝试的账本记录，备用请求带 Hedge 标记
func (stream *upstreamStream) record(err error, final antigravity.Event) requestdb.Attempt {
	record := attempt(stream.lease.ID, stream.exchange, stream.started, err, final)
	record.Hedge, record.RequestBytes = stream.hedge, stream.bytes
	return record
}

// send 在后台向上游发出生成请求并等待首个上游事件，401 时把这次尝试写入账本、让 token 过期后重发一次；done 关闭后 events、exchange、started、first、firstAt 与 err 可读，events 从首个事件开始
func send(deadline context.Context, trace *requestdb.Trace, stream *upstreamStream, envelope antigravity.Envelope) *upstreamStream {
	ctx, cancel := context.WithCancel(deadline)
	stream.cancel, stream.done, stream.begun = cancel, make(chan struct{}), time.Now()
	go func() {
		defer close(stream.done)
		stream.started = stream.begun
		upstream, exchange, err := stream.current.client.StreamGenerateContent(ctx, envelope)
		if status(err) == http.StatusUnauthorized {
			stream.exchange = exchange
			trace.AddAttempt(stream.record(err, antigravity.Event{}))
			stream.current.source.Expire()
			stream.started = time.Now()
			upstream, exchange, err = stream.current.client.StreamGenerateContent(ctx, envelope)
		}
		stream.exchange, stream.err = exchange, err
		if err != nil {
			return
		}
		first, ok := <-upstream
		stream.first, stream.firstAt = first, time.Now()
		events := make(chan antigravity.Event)
		stream.events = events
		go func() {
			defer close(events)
			if ok {
				events <- first
			}
			for event := range upstream {
				events <- event
			}
		}()
	}()
	return stream
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
	case final.Err != nil:
		record.Error = final.Err.Error()
	}
	return record
}

// fail 按上游错误原因处理账户并写日志：内容策略结束（含过滤提示）、输入拒绝、异常结束用完重新生成与本地错误不换账户，认证失败与需要验证的 403 移出调度，token 端点 5xx 与 429 按网络错误换账户，违反条款的 403 与账户受限通知停用，没有产品许可或订阅的 403 冷却该模型 licenseCooldown，QUOTA_EXHAUSTED 把该账户与同额度池账户的该模型、AI 积分不足把它们的额度组冷却到重置时间并在后台读取该账户的额度，图像模型只冷却该账户的该模型，没有 ErrorInfo 与 RetryInfo 的 429 由请求内容触发、直接返回，其他 429 短冷却该模型，连续 INTERNAL 500 递增冷却该模型，404 NOT_FOUND 短冷却该账户的该模型；返回是否换账户重试
func (s *Service) fail(ctx context.Context, current *account, model antigravity.CatalogModel, err error, tier prompt.Tier) bool {
	var policy *antigravity.ContentPolicyError
	var feedback *antigravity.PromptFeedbackError
	var abnormal *antigravity.AbnormalFinishError
	var local *Error
	var call *callError
	if errors.As(err, &policy) || errors.As(err, &feedback) || errors.As(err, &abnormal) || errors.As(err, &local) || errors.As(err, &call) {
		return false
	}
	var upstream *antigravity.UpstreamError
	var restricted *antigravity.AccountRestrictedError
	if errors.As(err, &restricted) {
		slog.ErrorContext(ctx, "账户受限，已停用 | 通知="+restricted.Message, "source", current.id)
		s.disable(current.id, "账户受限已停用："+restricted.Message)
		return true
	}
	if unauthorized(err) {
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
	case s.forbid(ctx, current.id, upstream):
	case upstream.Status == http.StatusForbidden && (reason == "SUBSCRIPTION_REQUIRED" || strings.Contains(strings.ToLower(message), "license") || strings.Contains(strings.ToLower(message), "subscription")):
		limit(model.ID, now.Add(licenseCooldown))
	case quotaExhausted(reason):
		until := upstream.RetryAt(now)
		if reset, err := time.Parse(time.RFC3339, model.QuotaInfo.ResetTime); until.IsZero() && err == nil {
			until = reset
		}
		until = cmp.Or(until, now.Add(shortCooldown))
		scope := model.ID
		if reason == "INSUFFICIENT_G1_CREDITS_BALANCE" {
			scope = model.QuotaGroup()
		}
		limit(scope, until)
		if model.Kind == antigravity.KindImage {
			break
		}
		for _, peer := range s.pool.Peers(current.id, model.QuotaGroup()) {
			s.pool.Limit(peer, scope, until)
			s.saveCooldowns()
			slog.WarnContext(ctx, fmt.Sprintf("同额度池账户冷却 | 范围=%s | 时长=%s | 耗尽账户=%s", scope, until.Sub(now).Round(time.Second), current.id), "source", peer)
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
		if cooling, _ := s.pool.Status(current.id); now.Before(cooling.Cooldown[model.ID]) {
			s.mu.Unlock()
			break
		}
		current.internal++
		until := now.Add(time.Duration(current.internal) * shortCooldown)
		s.pool.Limit(current.id, model.ID, until)
		s.mu.Unlock()
		limit(model.ID, until)
	case upstream.Status == http.StatusNotFound && state == "NOT_FOUND":
		limit(model.ID, now.Add(shortCooldown))
	default:
		return false
	}
	return true
}

// connectionFailure 判断错误是否为发出请求或读取响应头时的连接错误（连接断开、HTTP/2 流被重置等），不含认证失败与取消
func connectionFailure(err error) bool {
	var transport *url.Error
	return errors.As(err, &transport) && !unauthorized(err) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// unauthorized 判断错误是否为账户认证失败：上游 401 或 token 端点拒绝刷新
func unauthorized(err error) bool {
	var retrieve *oauth2.RetrieveError
	return errors.As(err, &retrieve) && refused(retrieve) || status(err) == http.StatusUnauthorized
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
	if record, ok := s.store.Account(id); ok {
		record.Enabled = false
		if err := s.store.Save(&record); err != nil {
			reason += "；写回账户失败：" + err.Error()
		}
	}
	s.mu.Lock()
	s.stop(id, reason)
	s.mu.Unlock()
}

// forbid 处理账户级的 403：需要验证时移出调度并记为待验证，违反服务条款时停用账户，消息中的空白序列按一个空格匹配；返回 upstream 是否属于这两种
func (s *Service) forbid(ctx context.Context, id string, upstream *antigravity.UpstreamError) bool {
	if upstream.Status != http.StatusForbidden {
		return false
	}
	_, message := upstream.Message()
	message = strings.Join(strings.Fields(strings.ToLower(message)), " ")
	switch reason := upstream.Reason(); {
	case reason == "VALIDATION_REQUIRED" || strings.Contains(message, "verify your account"):
		slog.WarnContext(ctx, "账户需要验证，停止请求", "source", id)
		s.mu.Lock()
		s.stop(id, verifyReason(upstream.Body))
		s.mu.Unlock()
	case reason == "TOS_VIOLATION" || strings.Contains(message, "terms of service"):
		slog.ErrorContext(ctx, "账户违反服务条款，已停用", "source", id)
		s.disable(id, "违反服务条款已停用："+string(upstream.Body))
	default:
		return false
	}
	return true
}

// CountTokens 以与生成相同的模型、会话与系统提示估算请求的 promptTokenCount，没有记录时间的消息取当前时间；Gemini 与 Claude 族在同一会话于该模型上一次成功生成的消息是本次的前缀时，取其上游用量并只估算该次上游 contents 之后新增的 contents，GPT-OSS 族计整个信封
func (s *Service) CountTokens(ctx context.Context, request antigravity.GenerateRequest) (int64, error) {
	request.System = dropBilling(request.System)
	composer, err := s.composer(s.live.Load())
	if err != nil {
		return 0, &Error{Status: http.StatusInternalServerError, Message: "系统提示模板: " + err.Error()}
	}
	model, err := s.model(request)
	if err != nil {
		return 0, err
	}
	id := identify(request)
	session, ok := s.conversations.find(id)
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
	device, plugins, experiments := s.countProfile(ctx, request.AccountID)
	composed, err := composer.Compose(request, model, session.Cascade, localTimes(times, device), device, plugins, experiments)
	if err != nil {
		return 0, &Error{Status: http.StatusInternalServerError, Message: "系统提示模板: " + err.Error()}
	}
	envelope, err := antigravity.BuildEnvelope("", model, antigravity.NewConversation(session.Cascade, session.Trajectory, request.Contents), composed.System, composed.Contents, composed.Tools, composed.Config, time.Now())
	if err != nil {
		return 0, &Error{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if usage, ok := s.conversations.usage(id, model.ID); ok && model.ModelProvider != antigravity.ProviderOpenAI {
		rest := envelope.Request.Contents[usage.Contents:]
		return usage.Tokens + usage.replay(model.ModelProvider, rest) + tokencount.Prompt(model.ModelProvider, antigravity.WireRequest{Contents: rest}), nil
	}
	return tokencount.Prompt(model.ModelProvider, envelope.Request), nil
}

// countProfile 返回计数时组装系统提示所用的设备档案、推荐插件段的货架正文与布尔实验开关：取指定账户的，否则取最早添加的就绪账户的；账户还没有保存过插件市场目录时先等第一次目录读取结束
func (s *Service) countProfile(ctx context.Context, id string) (antigravity.Device, string, map[string]bool) {
	for _, record := range s.store.Accounts() {
		if current := s.account(record.ID); current != nil && (id == "" || id == record.ID) {
			current.client.AwaitCatalog(ctx)
			return current.device, current.client.Plugins(), current.client.Experiments()
		}
	}
	return antigravity.Device{}, "", nil
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

// skip 返回请求不能使用的账户判断：已尝试的账户，指定 AccountID 时其余账户，Accounts 非空时不在其中的账户，以及没有被请求指定的停用账户（手动预热临时初始化的账户）
func skip(request antigravity.GenerateRequest, tried map[string]bool, enabled func(string) bool) func(string) bool {
	return func(id string) bool {
		return tried[id] || request.AccountID != "" && id != request.AccountID || len(request.Accounts) > 0 && !slices.Contains(request.Accounts, id) || id != request.AccountID && !enabled(id)
	}
}

// relay 转发一次上游尝试的事件：客户端尚未收到正文、函数调用与媒体时按失败原因经 open 在同一账户重新生成或换账户，最后一次重新生成不核对非 strict 声明的必填参数，此前已向客户端写出事件时先发出 EventRestart，正常结束时记下会话状态、把会话粘连到该账户、归还槽位并关闭输出；request 为非流式时事件暂存到结束，没有要求保留异常结束时，重新生成用完仍以格式错误的函数调用、意外的工具调用或格式错误的回复结束、且没有正文、函数调用、媒体与可见思考的生成以 AbnormalFinishError 结束
func (s *Service) relay(ctx context.Context, deadline *watchdog, stream *upstreamStream, open func(error, string) (*upstreamStream, error), request antigravity.GenerateRequest, id identity, cascade string, tier prompt.Tier) <-chan antigravity.Event {
	events := make(chan antigravity.Event)
	// send 把事件交给客户端，首个正文、媒体或函数调用记入账本的首次输出时刻
	send := func(event antigravity.Event) {
		if content(event) || event.Kind == antigravity.EventToolCall {
			requestdb.TraceFrom(ctx).Output()
		}
		deliver(ctx, events, event)
	}
	go func() {
		defer deadline.stop()
		regenerated := 0
		for {
			var held []antigravity.Event
			delivered, committed, calling, reasoned := false, false, false, false
			emit := func(event antigravity.Event) {
				calling = calling || event.Kind == antigravity.EventToolCall
				committed = committed || content(event)
				reasoned = reasoned || event.Kind == antigravity.EventReasoning && event.Text != ""
				if stream.standby != nil && (committed || calling) {
					supersede(ctx, stream.standby, "另一方已开始输出内容")
					stream.standby = nil
					s.hedges.Add(-1)
				}
				if request.Unary || calling || event.Kind == antigravity.EventError || !committed && (event.Kind == antigravity.EventUsage || event.Kind == antigravity.EventFinish) {
					held = append(held, event)
					return
				}
				for _, pending := range held {
					send(pending)
				}
				held = nil
				delivered = true
				send(event)
			}
			reported, final, err := forward(stream.events, emit, stream.out, deadline, stream.cancel)
			sent := committed && !request.Unary
			retry := regenerable(ctx, err, final, sent, committed || calling)
			regenerate := retry != nil && open != nil && regenerated < regenerations
			if retry != nil && open != nil && !regenerate && err == nil && !calling && !request.KeepAbnormalFinish && (request.Config.HideThinking || !reasoned) && abnormalFinish(final.FinishReason) {
				err = &antigravity.AbnormalFinishError{Reason: final.FinishReason, Message: final.FinishMessage, Usage: reported}
				for index, event := range held {
					if event.Kind == antigravity.EventFinish {
						held[index] = antigravity.Event{Kind: antigravity.EventError, Err: err}
					}
				}
			}
			if standby := stream.standby; standby != nil {
				stream.standby = nil
				s.hedges.Add(-1)
				if retry != nil && s.takeover(ctx, standby, tier) {
					record := stream.record(err, final)
					record.Usage = reported
					requestdb.TraceFrom(ctx).AddAttempt(record)
					if err != nil && status(err) == 0 {
						s.fail(ctx, stream.current, stream.lease.Model, err, tier)
					}
					stream.lease.Release()
					requestdb.TraceFrom(ctx).SetAccount(standby.lease.ID)
					slog.WarnContext(ctx, "先返回首个事件的一方在给出内容前失败，改用另一方 | 原因="+retry.Error(), "source", standby.lease.ID)
					if delivered {
						deliver(ctx, events, antigravity.Event{Kind: antigravity.EventRestart})
					}
					stream = standby
					continue
				}
				if retry == nil {
					supersede(ctx, standby, "另一方已结束")
				}
			}
			record := stream.record(err, final)
			switched := !regenerate && err != nil && s.fail(ctx, stream.current, stream.lease.Model, err, tier)
			if regenerate || switched && open != nil && !delivered && ctx.Err() == nil {
				record.Usage = reported
			}
			requestdb.TraceFrom(ctx).AddAttempt(record)
			if final.Kind == antigravity.EventFinish && final.Err != nil && !regenerate {
				slog.WarnContext(ctx, "上游在函数调用之后异常结束，按 STOP 结束 | 错误="+final.Err.Error(), "source", stream.current.id)
			}
			if regenerate {
				regenerated++
				if err != nil && status(err) == 0 {
					s.fail(ctx, stream.current, stream.lease.Model, err, tier)
				}
				stream.lease.Release()
				next, err := open(retry, stream.lease.ID)
				if err != nil {
					deliver(ctx, events, antigravity.Event{Kind: antigravity.EventError, Err: err})
					close(events)
					return
				}
				if regenerated == regenerations && next.out.tools != nil {
					next.out.tools.arguments = nil
				}
				slog.WarnContext(ctx, fmt.Sprintf("客户端尚未收到内容，重新生成 | 第 %d 次 | 原因=%v", regenerated, retry), "source", next.lease.ID)
				if delivered {
					deliver(ctx, events, antigravity.Event{Kind: antigravity.EventRestart})
				}
				stream = next
				continue
			}
			if switched && open != nil && !delivered && ctx.Err() == nil {
				stream.lease.Release()
				next, err := open(err, "")
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
				send(event)
			}
			succeeded := err == nil && reported != nil
			if succeeded {
				s.pool.Bind(cascade, stream.lease.ID, stream.started)
			}
			if succeeded && stream.out != nil {
				if err := s.conversations.record(cascade, id, stream.out.reply(), stream.lease.Model.ID, len(stream.out.prompt.Contents), stream.out.prefixTokens(*reported), reported.ReasoningTokens, stream.out.signatures()); err != nil {
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

// abnormalFinish 判断结束原因是否为格式错误的函数调用、意外的工具调用或格式错误的回复
func abnormalFinish(reason string) bool {
	switch strings.ToUpper(reason) {
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "MALFORMED_RESPONSE":
		return true
	}
	return false
}

// content 判断事件是否为客户端收到后不能随重新生成丢弃的正文或媒体；函数调用暂存到流结束，不在此列
func content(event antigravity.Event) bool {
	switch event.Kind {
	case antigravity.EventText:
		return event.Text != ""
	case antigravity.EventMedia:
		return true
	}
	return false
}

// regenerable 返回客户端尚未收到正文与媒体时可从头重新生成的原因：可重试的流错误（见 retryable）、已发出函数调用后流中断或没有结束原因，或以函数调用格式错误、意外的工具调用、格式错误的回复结束，本次尝试没有正文、媒体与函数调用（produced 为假）时另含以 STOP 结束；sent 为真或客户端已取消时返回 nil
func regenerable(ctx context.Context, err error, final antigravity.Event, sent, produced bool) error {
	if sent || ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return retryable(err)
	}
	if final.Kind == antigravity.EventFinish {
		if final.Err != nil {
			return retryable(final.Err)
		}
		switch strings.ToUpper(final.FinishReason) {
		case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL", "MALFORMED_RESPONSE":
			return fmt.Errorf("上游以 %s 结束", final.FinishReason)
		case "STOP":
			if !produced {
				return fmt.Errorf("上游以 STOP 结束且没有内容")
			}
		}
	}
	return nil
}

// retryable 返回流错误中可再请求一次的原因：流中断（提前结束、连接重置、HTTP/2 流重置、TLS 记录错误等读取错误）、流尾 5xx 与不符合工具约定的函数调用；上游 4xx、内容策略结束、输入拒绝、其他本地错误与超时返回 nil
func retryable(err error) error {
	var upstream *antigravity.UpstreamError
	var policy *antigravity.ContentPolicyError
	var feedback *antigravity.PromptFeedbackError
	var local *Error
	var call *callError
	switch {
	case errors.As(err, &call):
		return err
	case errors.As(err, &upstream):
		if upstream.Status >= http.StatusInternalServerError {
			return err
		}
		return nil
	case errors.As(err, &policy), errors.As(err, &feedback), errors.As(err, &local), errors.Is(err, requestdb.ErrRequestTimeout), errors.Is(err, context.Canceled):
		return nil
	}
	return err
}

// forward 把上游事件经 out 处理后交给 emit，返回最后一次转发的用量、上游最后一个事件与流中的错误；正文、思考、签名或函数调用事件使累计输出超过 out 的上限时丢弃它及其后的上游事件，以已转发部分的用量和 MAX_TOKENS 结束，Claude 与 GPT-OSS 的块不带逐块累计的输出用量，累计输出按已转发正文、思考与函数调用的本地计数，输入取块上的上游用量，没有时按信封的本地计数；out 发出结束事件或核对失败后以 cancel 关闭本次尝试的上游并丢弃其后的事件；首个事件为下线提示且其后流未完成时改发 404 错误事件，首个事件为最低客户端版本提示时不论流如何结束都改发带提示原文的 502 错误事件；每个上游事件为 deadline 重新计时，上游因超时中断时错误改为 REQUEST_TIMEOUT；客户端取消后 emit 丢弃事件，上游照常排空
func forward(upstream <-chan antigravity.Event, emit func(antigravity.Event), out *output, deadline *watchdog, cancel context.CancelFunc) (*antigravity.Usage, antigravity.Event, error) {
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
		if count == 1 && event.Kind == antigravity.EventText && (strings.Contains(event.Text, retiredText) || strings.HasPrefix(event.Text, unsupportedText)) || len(held) != 0 && event.Kind == antigravity.EventUsage {
			held = append(held, event)
			continue
		}
		if event.Kind == antigravity.EventError && deadline.timedOut() {
			event.Err = requestdb.ErrRequestTimeout
		}
		switch {
		case len(held) != 0 && strings.HasPrefix(held[0].Text, unsupportedText):
			event = antigravity.Event{Kind: antigravity.EventError, Err: &Error{Status: http.StatusBadGateway, Message: held[0].Text}}
			held = nil
		case len(held) != 0 && event.Kind == antigravity.EventError && errors.Is(event.Err, antigravity.ErrIncompleteStream):
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
			cancel()
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

// effortLevel 返回客户端思考参数对应的档位，xhigh 与 max 为 xhigh，预算分界取 1024 与 8192，未指定与负数的动态预算为空，由模型默认档位决定
func effortLevel(config antigravity.GenerationConfig) string {
	switch strings.ToLower(config.ReasoningEffort) {
	case "none", "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh", "max":
		return "xhigh"
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
func sectionTokens(model antigravity.CatalogModel, conversation string, device antigravity.Device, plugins string, experiments map[string]bool) []int {
	tokens := prompt.SectionTokens(model, conversation, device, plugins, experiments)
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
