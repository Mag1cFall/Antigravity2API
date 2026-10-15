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
	maxAccounts = 2
	// shortCooldown 为 429 限速没有给出恢复时间时的冷却时长，也是连续 INTERNAL 500 每次递增的冷却时长
	shortCooldown = time.Minute
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

// Generate 去掉客户端系统提示中的计费行，按实时配置与目录确定模型，图像与转写模型及要求输出图片的请求交给 generateMedia，其余请求取得会话后选定账户，以该账户的设备档案组装信封并转发事件；首字节前按错误原因换账户重试，单个请求最多尝试 maxAccounts 个账户，指定账户时只用该账户；整个请求受 REQUEST_TIMEOUT 限制，每次上游尝试写入请求账本
func (s *Service) Generate(ctx context.Context, request antigravity.GenerateRequest) (<-chan antigravity.Event, error) {
	request.System = dropBilling(request.System)
	cfg := s.live.Load()
	composer, err := s.composer(cfg)
	if err != nil {
		return nil, &Error{Status: http.StatusInternalServerError, Message: "系统提示模板: " + err.Error()}
	}
	s.pool.Configure(cfg.AccountConcurrency, cfg.RoutingStrategy == "fill-first")
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
	request, out, err := prepare(request, model, signatureRecord{calls: session.Signatures, origins: session.Origins})
	if err != nil {
		return nil, err
	}
	conversation := antigravity.NewConversation(session.Cascade, session.Trajectory, request.Contents)
	times := session.Times
	for len(times) < len(request.Contents) {
		times = append(times, time.Now().Unix())
	}
	trace := requestdb.TraceFrom(ctx)
	deadline, cancel := context.WithTimeout(ctx, cfg.RequestTimeout)
	tried := make(map[string]bool)
	if request.AccountID != "" {
		s.mu.Lock()
		for id := range s.accounts {
			tried[id] = id != request.AccountID
		}
		s.mu.Unlock()
	}
	var last error
	for attempts := 1; ; attempts++ {
		lease, err := s.pool.Acquire(deadline, model.ID, session.Cascade, tried, s.lowerTier(model))
		if err != nil {
			cancel()
			var cooldown *balancer.CooldownError
			if last != nil && errors.As(err, &cooldown) {
				return nil, last
			}
			return nil, err
		}
		tried[lease.ID] = true
		current := s.account(lease.ID)
		if current == nil {
			lease.Release()
			continue
		}
		if session.New {
			current.client.StartTrajectory(deadline, session.Trajectory, request.Contents)
		}
		composed := composer.Compose(request, lease.Model, session.Cascade, localTimes(times, current.device), current.device)
		envelope, err := antigravity.BuildEnvelope(current.client.Project(), lease.Model, conversation, composed.System, composed.Contents, composed.Tools, composed.Config, time.Now())
		if err != nil {
			lease.Release()
			cancel()
			return nil, &Error{Status: http.StatusBadRequest, Message: err.Error()}
		}
		out.prompt = envelope.Request
		trace.SetAccount(lease.ID)
		trace.SetUpstream(lease.Model.ID, lease.Model.QuotaGroup(), envelope.RequestType)
		trace.SetConversation(session.Cascade, 2*conversation.Turn+1)
		started := time.Now()
		upstream, exchange, err := current.client.StreamGenerateContent(deadline, envelope)
		if status(err) == http.StatusUnauthorized {
			trace.AddAttempt(attempt(lease.ID, exchange, started, err, antigravity.Event{}))
			current.source.Expire()
			started = time.Now()
			upstream, exchange, err = current.client.StreamGenerateContent(deadline, envelope)
		}
		if err == nil {
			return s.relay(ctx, cancel, lease, current, upstream, exchange, started, out, digests, session.Cascade, composer.Tier()), nil
		}
		trace.AddAttempt(attempt(lease.ID, exchange, started, err, antigravity.Event{}))
		lease.Release()
		if !s.fail(ctx, current, lease.Model, err, composer.Tier()) || attempts == maxAccounts || deadline.Err() != nil {
			cancel()
			return nil, err
		}
		last = err
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

// fail 按上游错误原因处理账户并写日志：认证失败与需要验证的 403 移出调度，违反条款的 403 停用，QUOTA_EXHAUSTED 把该账户与同额度池账户的额度组冷却到重置时间，没有 ErrorInfo 与 RetryInfo 的 429 由请求内容触发、直接返回，其他 429 短冷却该模型，连续 INTERNAL 500 递增冷却该模型；返回是否换账户重试
func (s *Service) fail(ctx context.Context, current *account, model antigravity.CatalogModel, err error, tier prompt.Tier) bool {
	var upstream *antigravity.UpstreamError
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) || status(err) == http.StatusUnauthorized {
		slog.WarnContext(ctx, "账户认证失败，移出调度 | 错误="+err.Error(), "source", current.id)
		s.mu.Lock()
		s.stop(current.id, "认证失败："+err.Error())
		s.mu.Unlock()
		return true
	}
	if !errors.As(err, &upstream) {
		return true
	}
	now := time.Now()
	state, message := upstream.Message()
	reason := upstream.Reason()
	limit := func(scope string, until time.Time) {
		s.pool.Limit(current.id, scope, until)
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
	case reason == "QUOTA_EXHAUSTED":
		until := upstream.RetryAt(now)
		if reset, err := time.Parse(time.RFC3339, model.QuotaInfo.ResetTime); until.IsZero() && err == nil {
			until = reset
		}
		until = cmp.Or(until, now.Add(shortCooldown))
		limit(model.QuotaGroup(), until)
		for _, peer := range s.pool.Peers(current.id, model.QuotaGroup()) {
			s.pool.Limit(peer, model.QuotaGroup(), until)
			slog.WarnContext(ctx, fmt.Sprintf("同额度池账户冷却 | 范围=%s | 时长=%s | 耗尽账户=%s", model.QuotaGroup(), until.Sub(now).Round(time.Second), current.id), "source", peer)
		}
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

// CountTokens 以与生成相同的模型、会话与系统提示估算请求的 promptTokenCount，没有记录时间的消息取当前时间；同一会话在该模型上一次成功生成的消息是本次的前缀时，取其上游用量并只估算该次上游 contents 之后新增的 contents
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
	device := s.countDevice(request.AccountID)
	composed := composer.Compose(request, model, session.Cascade, localTimes(times, device), device)
	envelope, err := antigravity.BuildEnvelope("", model, antigravity.NewConversation(session.Cascade, session.Trajectory, request.Contents), composed.System, composed.Contents, composed.Tools, composed.Config, time.Now())
	if err != nil {
		return 0, &Error{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if usage, ok := s.conversations.usage(digests, model.ID); ok {
		return usage.Tokens + tokencount.Prompt(model.ModelProvider, antigravity.WireRequest{Contents: envelope.Request.Contents[usage.Contents:]}), nil
	}
	return tokencount.Prompt(model.ModelProvider, envelope.Request), nil
}

// countDevice 返回计数时组装系统提示所用的设备档案：指定账户的档案，否则为最早添加的就绪账户的档案
func (s *Service) countDevice(id string) antigravity.Device {
	for _, record := range s.store.Accounts() {
		if current := s.account(record.ID); current != nil && (id == "" || id == record.ID) {
			return current.device
		}
	}
	return antigravity.Device{}
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

// relay 按 out 处理并转发上游事件，结束后按原因处理流中的错误、写入本次上游尝试，正常结束时记下本次的上游输入用量、回答指纹与函数调用签名，再归还槽位并关闭输出，客户端收到结束时下一轮请求已能匹配会话；账户初始化后首次成功生成后读取额度摘要
func (s *Service) relay(ctx context.Context, cancel context.CancelFunc, lease *balancer.Lease, current *account, upstream <-chan antigravity.Event, exchange *antigravity.Exchange, started time.Time, out *output, digests []string, cascade string, tier prompt.Tier) <-chan antigravity.Event {
	events := make(chan antigravity.Event)
	go func() {
		defer cancel()
		reported, final, err := forward(ctx, upstream, events, out, cancel)
		if err != nil {
			s.fail(ctx, current, lease.Model, err, tier)
		}
		requestdb.TraceFrom(ctx).AddAttempt(attempt(lease.ID, exchange, started, err, final))
		succeeded := err == nil && reported != nil
		if succeeded && out != nil {
			if err := s.conversations.record(cascade, digests, out.reply(), lease.Model.ID, len(out.prompt.Contents), reported.InputTokens, out.signatures()); err != nil {
				slog.WarnContext(ctx, "写入会话记录失败 | 错误="+err.Error(), "source", lease.ID)
			}
		}
		lease.Release()
		close(events)
		if succeeded {
			s.quote(ctx, current)
		}
	}()
	return events
}

// forward 把上游事件经 out 处理后写入 events，返回最后一次转发的用量、上游最后一个事件与流中的错误；正文、思考、签名或函数调用事件使累计输出超过 out 的上限时丢弃它及其后的上游事件，以已转发部分的用量和 MAX_TOKENS 结束，Claude 与 GPT-OSS 的块不带逐块累计的输出用量，累计输出按已转发正文、思考与函数调用的本地计数，输入取块上的上游用量，没有时按信封的本地计数；out 发出结束事件或核对失败后关闭上游并丢弃其后的事件；首个事件为下线提示且其后流未完成时改发 404 错误事件；上游因请求超时中断时错误改为 REQUEST_TIMEOUT；客户端取消后继续排空上游
func forward(ctx context.Context, upstream <-chan antigravity.Event, events chan<- antigravity.Event, out *output, cancel context.CancelFunc) (*antigravity.Usage, antigravity.Event, error) {
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
			deliver(ctx, events, processed)
		}
	}
	truncated := false
	count := 0
	for event := range upstream {
		count++
		final = event
		if truncated {
			continue
		}
		if count == 1 && event.Kind == antigravity.EventText && strings.Contains(event.Text, retiredText) || len(held) != 0 && event.Kind == antigravity.EventUsage {
			held = append(held, event)
			continue
		}
		if event.Kind == antigravity.EventError && errors.Is(event.Err, context.DeadlineExceeded) {
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
				counted.OutputTokens += tokencount.Text(event.Text)
			case antigravity.EventReasoning:
				counted.ReasoningTokens += tokencount.Text(event.Text)
			case antigravity.EventToolCall:
				counted.OutputTokens += tokencount.Text(event.ToolCall.Name) + tokencount.Text(string(event.ToolCall.Arguments))
			}
		}
		if limit != nil && event.Kind != antigravity.EventUsage && produced(upstreamUsage, counted, out.provider) > *limit {
			truncated = true
			cancel()
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

// produced 返回累计输出与思考 token：有上游用量时取上游用量，否则按 Gemma 3 本地计数换算为模型族的 token 数
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

// effortLevel 返回客户端思考参数对应的档位，预算分界取 1024 与 8192，-1 动态预算视为 high，未指定时为空
func effortLevel(config antigravity.GenerationConfig) string {
	switch strings.ToLower(config.ReasoningEffort) {
	case "none", "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh", "max":
		return "high"
	}
	if config.ThinkingBudget == nil {
		return ""
	}
	switch budget := *config.ThinkingBudget; {
	case budget < 0:
		return "high"
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
