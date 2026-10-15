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
	"github.com/Mag1cFall/Antigravity2API/internal/balancer"
	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

// transcriptionURL 为语音转写双向流在账本中记录的地址
const transcriptionURL = "https://aicode.googleapis.com/google.gca.aicode.v1alpha.PredictionService/BidiGenerateContent"

// upstreamCall 以选定账户与其目录条目发出一次上游请求
type upstreamCall func(deadline context.Context, current *account, model antigravity.CatalogModel) (<-chan antigravity.Event, *antigravity.Exchange, error)

// wantsImage 判断客户端是否要求输出图片
func wantsImage(config antigravity.GenerationConfig) bool {
	return slices.ContainsFunc(config.ResponseModalities, func(modality string) bool { return strings.EqualFold(modality, "IMAGE") })
}

// generateMedia 按用途模型完成生成：图像模型发 image_gen 的 generateContent，转写模型把请求中的音频经 aicode 双向流转写为正文，Tab 补全模型以编辑器版的补全请求续写光标处的文字；要求输出图片而模型不生成图片时返回 400
func (s *Service) generateMedia(ctx context.Context, request antigravity.GenerateRequest, model antigravity.CatalogModel, tier prompt.Tier) (<-chan antigravity.Event, error) {
	switch model.Kind {
	case antigravity.KindImage:
		return s.generateImage(ctx, request, model, tier)
	case antigravity.KindTranscription:
		return s.transcribe(ctx, request, model, tier)
	case antigravity.KindTab:
		return s.complete(ctx, request, model, tier)
	}
	var images []string
	for _, candidate := range s.pool.Models() {
		if candidate.Kind == antigravity.KindImage {
			images = append(images, candidate.ID)
		}
	}
	return nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("model %s does not generate images; image output requires an image generation model (%s)", model.ID, strings.Join(images, ", "))}
}

// generateImage 以 image_gen 信封发出非流式 generateContent，新会话先写轨迹 ACL；图片、正文与签名按流式事件转发
func (s *Service) generateImage(ctx context.Context, request antigravity.GenerateRequest, model antigravity.CatalogModel, tier prompt.Tier) (<-chan antigravity.Event, error) {
	digests := chain(request)
	session, err := s.conversations.begin(digests, replies(request))
	if err != nil {
		return nil, err
	}
	conversation := antigravity.NewConversation(session.Cascade, session.Trajectory, request.Contents)
	contents := slices.DeleteFunc(cloneContents(request.Contents), func(content antigravity.Content) bool { return len(content.Parts) == 0 })
	return s.dispatch(ctx, request, model, session, digests, "image_gen", 2*conversation.Turn+2, tier, func(deadline context.Context, current *account, catalog antigravity.CatalogModel) (<-chan antigravity.Event, *antigravity.Exchange, error) {
		envelope, err := antigravity.BuildImageEnvelope(current.client.Project(), catalog, conversation, request.System, contents, request.Config, time.Now())
		if err != nil {
			return nil, nil, &Error{Status: http.StatusBadRequest, Message: err.Error()}
		}
		if session.New {
			current.client.WriteTrajectoryACL(deadline, session.Trajectory)
			session.New = false
		}
		return current.client.GenerateContent(deadline, envelope)
	})
}

// transcribe 把请求中的音频解码为 16 kHz PCM，经 aicode 双向流转写，转写文本作为正文输出
func (s *Service) transcribe(ctx context.Context, request antigravity.GenerateRequest, model antigravity.CatalogModel, tier prompt.Tier) (<-chan antigravity.Event, error) {
	blobs := audioParts(request.Contents)
	if len(blobs) == 0 {
		return nil, &Error{Status: http.StatusBadRequest, Message: fmt.Sprintf("model %s transcribes audio: the request contains no audio", model.ID)}
	}
	var pcm []byte
	for _, blob := range blobs {
		decoded, err := transcriptionPCM(blob)
		if err != nil {
			return nil, &Error{Status: http.StatusBadRequest, Message: err.Error()}
		}
		pcm = append(pcm, decoded...)
	}
	digests := chain(request)
	session, err := s.conversations.begin(digests, replies(request))
	if err != nil {
		return nil, err
	}
	return s.dispatch(ctx, request, model, session, digests, "audio_transcription", 0, tier, func(deadline context.Context, current *account, catalog antigravity.CatalogModel) (<-chan antigravity.Event, *antigravity.Exchange, error) {
		exchange := &antigravity.Exchange{URL: transcriptionURL}
		result, err := current.client.Transcribe(deadline, "models/"+catalog.ID, pcm)
		exchange.Response = []byte(strings.Join(result.Messages, "\n"))
		if err != nil {
			return nil, exchange, err
		}
		events := make(chan antigravity.Event, 2)
		if result.Text != "" {
			events <- antigravity.Event{Kind: antigravity.EventText, Text: result.Text, ProviderModel: catalog.ID}
		}
		events <- antigravity.Event{Kind: antigravity.EventFinish, FinishReason: "STOP", ProviderModel: catalog.ID}
		close(events)
		return events, exchange, nil
	})
}

// dispatch 按 Generate 的账户选择、401 重发与换号规则执行 call，requestType 与 step 写入账本
func (s *Service) dispatch(ctx context.Context, request antigravity.GenerateRequest, model antigravity.CatalogModel, session conversation, digests []string, requestType string, step int, tier prompt.Tier, call upstreamCall) (<-chan antigravity.Event, error) {
	trace := requestdb.TraceFrom(ctx)
	deadline, cancel := context.WithTimeout(ctx, s.live.Load().RequestTimeout)
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
		trace.SetAccount(lease.ID)
		trace.SetUpstream(lease.Model.ID, lease.Model.QuotaGroup(), requestType)
		trace.SetConversation(session.Cascade, step)
		started := time.Now()
		upstream, exchange, err := call(deadline, current, lease.Model)
		if status(err) == http.StatusUnauthorized {
			trace.AddAttempt(attempt(lease.ID, exchange, started, err, antigravity.Event{}))
			current.source.Expire()
			started = time.Now()
			upstream, exchange, err = call(deadline, current, lease.Model)
		}
		if err == nil {
			return s.relay(ctx, cancel, lease, current, upstream, exchange, started, nil, digests, session.Cascade, tier), nil
		}
		trace.AddAttempt(attempt(lease.ID, exchange, started, err, antigravity.Event{}))
		lease.Release()
		var local *Error
		if errors.As(err, &local) || !s.fail(ctx, current, lease.Model, err, tier) || attempts == maxAccounts || deadline.Err() != nil {
			cancel()
			return nil, err
		}
		last = err
	}
}
