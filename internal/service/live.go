package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

// liveURLs 为 Live 两种上游双向流在账本中记录的地址
var liveURLs = map[bool]string{
	false: transcriptionURL,
	true:  "https://aicode.googleapis.com/google.gca.aicode.v1alpha.JetskiRemoteAgentProxyService/StreamVoice",
}

// OpenLive 按模型打开 Live 上游双向流并读取第一条上游消息
// 转写模型走 BidiGenerateContent（会话期间占槽位），Agent 模型走语音模式 StreamVoice（打开后归还槽位）
// 401 时丢弃 token 在同一账户重试一次，其余失败按 fail 换号最多 accountLimit 个，不做限速等待与连接错误再试
// 协议与双流流程见 docs/protocol.md“语音转写双向流”与“语音模式双向流”
func (s *Service) OpenLive(ctx context.Context, request antigravity.LiveRequest) (*antigravity.LiveSession, error) {
	model, err := s.model(antigravity.GenerateRequest{Model: request.Model, AccountID: request.AccountID, Accounts: request.Accounts})
	if err != nil {
		return nil, err
	}
	voice := model.Agent
	if model.Kind != antigravity.KindTranscription && !voice {
		var supported []string
		for _, candidate := range s.pool.Models() {
			if candidate.Kind == antigravity.KindTranscription || candidate.Agent {
				supported = append(supported, candidate.ID)
			}
		}
		slices.Sort(supported)
		return nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("model %s does not support bidiGenerateContent; Live sessions use a transcription model or an agent model (%s)", model.ID, strings.Join(slices.Compact(supported), ", "))}
	}
	trace := requestdb.TraceFrom(ctx)
	tier := prompt.Tier(s.live.Load().SystemPrompt)
	requestType := "audio_transcription"
	if voice {
		requestType = "voice"
	}
	tried := make(map[string]bool)
	var last error
	for attempts := 1; ; attempts++ {
		lease, err := s.pool.Acquire(ctx, model.ID, "", skip(antigravity.GenerateRequest{AccountID: request.AccountID, Accounts: request.Accounts}, tried, s.store.Enabled), s.lowerTier(model))
		if err != nil {
			if last != nil {
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
		trace.SetAccount(lease.ID)
		trace.SetUpstream(lease.Model.ID, lease.Model.QuotaGroup(), requestType)
		started := time.Now()
		observer := s.observerModel(lease.ID)
		session, err := openLive(ctx, current, lease.Model, voice, observer, request.Setup)
		if status(err) == http.StatusUnauthorized {
			trace.AddAttempt(liveAttempt(lease.ID, voice, started, err))
			current.source.Expire()
			started = time.Now()
			session, err = openLive(ctx, current, lease.Model, voice, observer, request.Setup)
		}
		if err == nil {
			session.Account = lease.ID
			if voice {
				lease.Release()
			}
			session.Close = func(cause error) {
				session.Stream.Close()
				if session.ObserverStream != nil {
					session.ObserverStream.Close()
				}
				if !voice {
					lease.Release()
				}
				trace.AddAttempt(liveAttempt(lease.ID, voice, started, cause))
			}
			return session, nil
		}
		trace.AddAttempt(liveAttempt(lease.ID, voice, started, err))
		lease.Release()
		var local *Error
		if errors.As(err, &local) || ctx.Err() != nil {
			return nil, err
		}
		if !s.fail(ctx, current, lease.Model, err, tier) || attempts >= accountLimit(err) {
			return nil, err
		}
		last = err
	}
}

// openLive 在账户上打开上游双向流，发出 setup 并读取第一条上游消息；语音模式先按官方顺序打开识别用户语音的转写模型流
func openLive(ctx context.Context, current *account, model antigravity.CatalogModel, voice bool, observer string, setup func(antigravity.CatalogModel) ([][]byte, error)) (*antigravity.LiveSession, error) {
	messages, err := setup(model)
	if err != nil {
		return nil, &Error{Status: http.StatusBadRequest, Message: err.Error()}
	}
	session := &antigravity.LiveSession{Model: model, Voice: voice}
	open := current.client.OpenLive
	if voice {
		if observer == "" {
			return nil, &Error{Status: http.StatusServiceUnavailable, Message: "voice sessions need a transcription model to recognize speech, and this account does not list one"}
		}
		session.OpenObserver = func(ctx context.Context) (antigravity.LiveConn, error) {
			stream, err := current.client.OpenLive(ctx)
			if err != nil {
				return nil, err
			}
			if err := stream.Send(antigravity.ObserverSetupMessage("models/" + observer)); err != nil {
				stream.Close()
				return nil, err
			}
			if _, err := stream.Recv(); err != nil {
				stream.Close()
				return nil, err
			}
			return stream, nil
		}
		if session.ObserverStream, err = session.OpenObserver(ctx); err != nil {
			return nil, err
		}
		open = current.client.OpenVoice
	}
	stream, err := open(ctx)
	if err == nil {
		for _, message := range messages {
			if err = stream.Send(message); err != nil {
				break
			}
		}
		if err == nil {
			session.First, err = stream.Recv()
		}
		if err != nil {
			stream.Close()
		}
	}
	if err != nil {
		if session.ObserverStream != nil {
			session.ObserverStream.Close()
		}
		return nil, err
	}
	session.Stream = stream
	return session, nil
}

// observerModel 返回账户目录中语音模式识别用户语音所用的转写模型
func (s *Service) observerModel(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, model := range s.catalogs[id] {
		if model.Kind == antigravity.KindTranscription {
			return model.ID
		}
	}
	return ""
}

// liveAttempt 返回 Live 上游双向流的账本记录：上游错误取其状态码，连接错误状态码为 0
func liveAttempt(account string, voice bool, started time.Time, err error) requestdb.Attempt {
	record := requestdb.Attempt{Account: account, URL: liveURLs[voice], Status: http.StatusOK, DurationMS: time.Since(started).Milliseconds()}
	var upstream *antigravity.UpstreamError
	switch {
	case errors.As(err, &upstream):
		record.Status = upstream.Status
		record.Response = upstream.Body
	case err != nil && !errors.Is(err, context.Canceled):
		record.Status, record.Error = 0, err.Error()
	}
	return record
}
