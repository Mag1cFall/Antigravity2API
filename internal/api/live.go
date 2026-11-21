package api

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/oov/audio/resampler"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

// liveVersions 为 Gemini Live API WebSocket 路径中的接口版本
var liveVersions = []string{"v1beta", "v1alpha"}

// livePath 返回该接口版本的 BidiGenerateContent WebSocket 路径
func livePath(version string) string {
	return "/ws/google.ai.generativelanguage." + version + ".GenerativeService.BidiGenerateContent"
}

const (
	// defaultVoice 为官方语音模式的默认音色
	defaultVoice = "Fola"
	// liveTurnQuiet 为语音回复最后一段音频之后判定该回合结束的静默时长
	liveTurnQuiet = 1500 * time.Millisecond
	// liveCloseReason 为 WebSocket 关闭原因的字节上限
	liveCloseReason = 123
)

// liveSetup 为 Live setup 中服务使用的字段，其余字段在转写会话中原样交给上游
type liveSetup struct {
	Model             string                 `json:"model"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig"`
	SystemInstruction *geminiContent         `json:"systemInstruction"`
	Tools             []geminiToolGroup      `json:"tools"`
	RealtimeInput     *struct {
		Detection *struct {
			Disabled bool `json:"disabled"`
		} `json:"automaticActivityDetection"`
		ActivityHandling string `json:"activityHandling"`
	} `json:"realtimeInputConfig"`
	InputTranscription  json.RawMessage `json:"inputAudioTranscription"`
	OutputTranscription json.RawMessage `json:"outputAudioTranscription"`
	SessionResumption   *struct {
		Handle string `json:"handle"`
	} `json:"sessionResumption"`
	Proactivity json.RawMessage `json:"proactivity"`
}

// liveSpeech 为 generationConfig.speechConfig 中服务使用的字段
type liveSpeech struct {
	VoiceConfig *struct {
		Prebuilt *struct {
			VoiceName string `json:"voiceName"`
		} `json:"prebuiltVoiceConfig"`
	} `json:"voiceConfig"`
	LanguageCode string `json:"languageCode"`
}

// liveClientMessage 为 setup 之后客户端消息中服务使用的字段
type liveClientMessage struct {
	RealtimeInput *struct {
		Audio          *geminiBlobPart  `json:"audio"`
		MediaChunks    []geminiBlobPart `json:"mediaChunks"`
		AudioStreamEnd bool             `json:"audioStreamEnd"`
		Video          json.RawMessage  `json:"video"`
		Text           *string          `json:"text"`
		ActivityStart  json.RawMessage  `json:"activityStart"`
		ActivityEnd    json.RawMessage  `json:"activityEnd"`
	} `json:"realtimeInput"`
	ClientContent json.RawMessage `json:"clientContent"`
	ToolResponse  *struct {
		FunctionResponses []json.RawMessage `json:"functionResponses"`
	} `json:"toolResponse"`
}

// liveError 表示以指定关闭码结束 Live 会话的错误，cause 为记入账本的原始错误
type liveError struct {
	code    websocket.StatusCode
	message string
	cause   error
}

// Error 返回关闭原因
func (e *liveError) Error() string { return e.message }

// invalidLive 返回客户端输入无效时以 1007 关闭的错误
func invalidLive(format string, args ...any) error {
	return &liveError{code: websocket.StatusInvalidFramePayloadData, message: fmt.Sprintf(format, args...)}
}

// handleGeminiLive 把 WebSocket 升级为 Gemini Live API 会话：第一条消息为 setup，转写模型的会话与上游 BidiGenerateContent 逐条互转，Agent 模型的会话经官方语音模式 StreamVoice 以语音对话，智能体回合由该模型生成
func (s *server) handleGeminiLive(w http.ResponseWriter, r *http.Request) {
	live, ok := s.service.(antigravity.LiveService)
	if !ok {
		writeRouteNotFound(w, r)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxPublicBodyBytes)
	ctx := r.Context()
	err = s.serveLive(ctx, conn, live)
	trace := requestdb.TraceFrom(ctx)
	var closed *liveError
	switch {
	case liveEnded(err):
		conn.Close(websocket.StatusNormalClosure, "")
	case errors.As(err, &closed):
		trace.Fail(cmp.Or(closed.cause, errors.New(closed.message)))
		conn.Close(closed.code, clipReason(closed.message))
	default:
		trace.Fail(err)
		conn.Close(websocket.StatusInternalError, clipReason(publicLiveError(err)))
	}
}

// liveEnded 报告会话是否由客户端关闭或上游正常结束
func liveEnded(err error) bool {
	return err == nil || errors.Is(err, io.EOF) || websocket.CloseStatus(err) != -1
}

// publicLiveError 返回上游或服务错误的公开说明
func publicLiveError(err error) string {
	var local httpStatusError
	if errors.As(err, &local) {
		return publicReason(err)
	}
	return "Upstream Live session failed: " + publicReason(err)
}

// clipReason 把关闭原因截到 WebSocket 允许的长度，不截断 UTF-8 字符
func clipReason(reason string) string {
	if len(reason) <= liveCloseReason {
		return reason
	}
	cut := liveCloseReason
	for cut > 0 && reason[cut]&0xC0 == 0x80 {
		cut--
	}
	return reason[:cut]
}

// serveLive 读取 setup、打开上游并运行会话直到任一方结束
func (s *server) serveLive(ctx context.Context, conn *websocket.Conn, live antigravity.LiveService) error {
	_, data, err := conn.Read(ctx)
	if err != nil {
		return err
	}
	data, err = geminiCamelKeys(data)
	if err != nil {
		return invalidLive("Invalid JSON payload: %v", err)
	}
	var first struct {
		Setup json.RawMessage `json:"setup"`
	}
	if err := json.Unmarshal(data, &first); err != nil || len(first.Setup) == 0 {
		return invalidLive("The first message must be setup")
	}
	var setup liveSetup
	if err := json.Unmarshal(first.Setup, &setup); err != nil {
		return invalidLive("Invalid setup: %v", err)
	}
	model := strings.TrimPrefix(strings.TrimSpace(setup.Model), "models/")
	if model == "" {
		return invalidLive("setup.model is required")
	}
	requestdb.TraceFrom(ctx).SetRequest(true, model)
	session, err := live.OpenLive(ctx, antigravity.LiveRequest{Model: model, Setup: func(catalog antigravity.CatalogModel) ([][]byte, error) {
		if catalog.Kind == antigravity.KindTranscription {
			return transcriptionSetup(first.Setup, catalog)
		}
		return voiceSetup(setup, catalog)
	}})
	if err != nil {
		var local httpStatusError
		if errors.As(err, &local) && local.HTTPStatus() == http.StatusBadRequest {
			return &liveError{code: websocket.StatusInvalidFramePayloadData, message: publicReason(err), cause: err}
		}
		return err
	}
	var cause error
	defer func() {
		if liveEnded(cause) {
			session.Close(nil)
		} else {
			session.Close(cause)
		}
	}()
	if session.Voice {
		cause = s.serveVoice(ctx, conn, session, setup)
	} else {
		cause = serveTranscription(ctx, conn, session)
	}
	return cause
}

// transcriptionSetup 把公开 setup 的模型换成目录条目后编码为上游 setup
func transcriptionSetup(raw json.RawMessage, model antigravity.CatalogModel) ([][]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	fields["model"], _ = json.Marshal("models/" + model.ID)
	setup, _ := json.Marshal(map[string]any{"setup": fields})
	message, err := antigravity.LiveClientMessage(setup)
	if err != nil {
		return nil, fmt.Errorf("setup for transcription model %s: %w", model.ID, err)
	}
	return [][]byte{message}, nil
}

// serveTranscription 在客户端 WebSocket 与上游转写双向流之间逐条互转消息，并在结束时估算用量记入账本
// 消息映射与用量口径见 docs/protocol.md「Live」
func serveTranscription(ctx context.Context, conn *websocket.Conn, session *antigravity.LiveSession) error {
	trace := requestdb.TraceFrom(ctx)
	var audioBytes, words atomic.Int64
	defer func() {
		input := audioBytes.Load() * transcriptionTokensPerSecond / (antigravity.VoiceMicRate * 2)
		output := words.Load()
		trace.Observe(antigravity.Event{Kind: antigravity.EventUsage, Usage: &antigravity.Usage{InputTokens: input, OutputTokens: output, TotalTokens: input + output}})
	}()
	forward := func(raw []byte) error {
		message, err := antigravity.LiveServerMessage(raw)
		if err != nil {
			return err
		}
		var content struct {
			ServerContent struct {
				InputTranscription struct {
					Text string `json:"text"`
				} `json:"inputTranscription"`
			} `json:"serverContent"`
		}
		if json.Unmarshal(message, &content) == nil && content.ServerContent.InputTranscription.Text != "" {
			words.Add(int64(len(strings.Fields(content.ServerContent.InputTranscription.Text))))
		}
		return conn.Write(ctx, websocket.MessageBinary, message)
	}
	if err := forward(session.First); err != nil {
		return err
	}
	upstream := make(chan error, 1)
	go func() {
		for {
			raw, err := session.Stream.Recv()
			if err == nil {
				err = forward(raw)
			}
			if err != nil {
				upstream <- err
				return
			}
		}
	}()
	client := make(chan error, 1)
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err == nil {
				var messages []transcriptionMessage
				messages, err = transcriptionInput(data)
				for _, message := range messages {
					if err != nil {
						break
					}
					if err = session.Stream.Send(message.data); err == nil {
						audioBytes.Add(message.audio)
					}
				}
			}
			if err != nil {
				client <- err
				return
			}
		}
	}()
	select {
	case err := <-upstream:
		session.Stream.Close()
		return err
	case err := <-client:
		session.Stream.Close()
		<-upstream
		return err
	}
}

// transcriptionTokensPerSecond 为估算转写会话输入 token 所用的每秒音频 token 数，与转写接口相同
const transcriptionTokensPerSecond = 25

// transcriptionMessage 是一条编码后的上游消息与其中的音频字节数
type transcriptionMessage struct {
	data  []byte
	audio int64
}

// transcriptionInput 把客户端消息按原顺序编码为上游消息：realtimeInput.mediaChunks 的每一块各为一条消息，其余 realtimeInput 字段随后为一条
func transcriptionInput(data []byte) ([]transcriptionMessage, error) {
	data, err := geminiCamelKeys(data)
	if err != nil {
		return nil, invalidLive("Invalid JSON payload: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, invalidLive("Invalid JSON payload: %v", err)
	}
	raw, ok := fields["realtimeInput"]
	if !ok {
		message, err := antigravity.LiveClientMessage(data)
		if err != nil {
			return nil, invalidLive("Invalid message for the transcription model: %v", err)
		}
		return []transcriptionMessage{{data: message}}, nil
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, invalidLive("Invalid realtimeInput: %v", err)
	}
	var inputs []map[string]json.RawMessage
	if chunks, ok := input["mediaChunks"]; ok {
		var blobs []geminiBlobPart
		if err := json.Unmarshal(chunks, &blobs); err != nil {
			return nil, invalidLive("Invalid realtimeInput.mediaChunks: %v", err)
		}
		delete(input, "mediaChunks")
		for _, blob := range blobs {
			key := "audio"
			if strings.HasPrefix(blob.MIME(), "image/") || strings.HasPrefix(blob.MIME(), "video/") {
				key = "video"
			}
			value, _ := json.Marshal(map[string]string{"mimeType": blob.MIME(), "data": blob.Data})
			inputs = append(inputs, map[string]json.RawMessage{key: value})
		}
	}
	if len(input) > 0 || len(inputs) == 0 {
		inputs = append(inputs, input)
	}
	messages := make([]transcriptionMessage, 0, len(inputs))
	for _, input := range inputs {
		var audio int64
		if raw, ok := input["audio"]; ok {
			var blob geminiBlobPart
			if json.Unmarshal(raw, &blob) == nil {
				audio = int64(base64.StdEncoding.DecodedLen(len(blob.Data)))
			}
		}
		fields["realtimeInput"], _ = json.Marshal(input)
		encoded, _ := json.Marshal(fields)
		message, err := antigravity.LiveClientMessage(encoded)
		if err != nil {
			return nil, invalidLive("Invalid message for the transcription model: %v", err)
		}
		messages = append(messages, transcriptionMessage{data: message, audio: audio})
	}
	return messages, nil
}

// voiceSetup 校验语音会话的 setup 并编码官方 StreamVoice setup 与 settings_update：音色取 speechConfig，语言提示取 speechConfig.languageCode，计划模型为该 Agent 模型
func voiceSetup(setup liveSetup, model antigravity.CatalogModel) ([][]byte, error) {
	enum, ok := antigravity.ModelEnum(model)
	if !ok {
		return nil, fmt.Errorf("model %s has no voice mode plan model", model.ID)
	}
	for _, modality := range setup.GenerationConfig.ResponseModalities {
		if !strings.EqualFold(modality, "AUDIO") {
			return nil, fmt.Errorf("voice sessions with model %s output audio; responseModalities %s is not supported", model.ID, modality)
		}
	}
	if input := setup.RealtimeInput; input != nil {
		if input.Detection != nil && input.Detection.Disabled {
			return nil, errors.New("voice sessions detect speech on the server; automaticActivityDetection.disabled is not supported")
		}
		if input.ActivityHandling == "NO_INTERRUPTION" {
			return nil, errors.New("voice sessions stop speaking when the user talks; activityHandling NO_INTERRUPTION is not supported")
		}
	}
	if setup.SessionResumption != nil && setup.SessionResumption.Handle != "" {
		return nil, errors.New("voice sessions cannot be resumed; remove sessionResumption.handle")
	}
	if rawJSONConfigured(setup.Proactivity) {
		return nil, errors.New("voice sessions do not support proactivity")
	}
	voice := defaultVoice
	var languages []string
	if len(setup.GenerationConfig.SpeechConfig) > 0 {
		var speech liveSpeech
		if err := json.Unmarshal(setup.GenerationConfig.SpeechConfig, &speech); err != nil {
			return nil, fmt.Errorf("generationConfig.speechConfig: %w", err)
		}
		if config := speech.VoiceConfig; config != nil && config.Prebuilt != nil && config.Prebuilt.VoiceName != "" {
			voice = config.Prebuilt.VoiceName
		}
		if speech.LanguageCode != "" {
			languages = []string{speech.LanguageCode}
		}
	}
	return [][]byte{
		antigravity.VoiceSetupMessage(antigravity.VoiceSetup{Cascade: uuid.NewString(), Voice: voice, Rate: antigravity.VoiceMicRate, PlanModel: enum, Languages: languages}),
		antigravity.VoiceSettingsMessage(enum),
	}, nil
}

// voiceSession 是一次语音会话的运行状态
type voiceSession struct {
	server  *server
	ctx     context.Context
	conn    *websocket.Conn
	stream  antigravity.LiveConn
	session *antigravity.LiveSession
	// observer 为当前识别用户语音的转写模型流
	observer   antigravity.LiveConn
	setup      liveSetup
	model      string
	trajectory string
	inputText  bool
	outputText bool
	// mic 为待发送的 16 kHz 麦克风音频
	mu      sync.Mutex
	mic     []byte
	stateID int64
	steps   int
	talking bool
	quiet   *time.Timer
	history []geminiContent
	tools   chan []json.RawMessage
	// turn 为最近登记的智能体回合
	turn     *voiceTurn
	resample *resampler.Resampler
	rate     int
	failed   chan error
	write    sync.Mutex
}

// voiceTurn 是一次交给智能体的回合，done 在回合退出后关闭
type voiceTurn struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// send 以二进制帧写出一条 JSON 服务端消息
func (v *voiceSession) send(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	v.write.Lock()
	defer v.write.Unlock()
	return v.conn.Write(v.ctx, websocket.MessageBinary, data)
}

// fail 记下第一个使会话结束的错误
func (v *voiceSession) fail(err error) {
	select {
	case v.failed <- err:
	default:
	}
}

// serveVoice 运行官方语音模式双向流会话，桥接麦克风音频、识别转写、语音回复与智能体函数调用
// 帧节奏与消息映射见 docs/protocol.md「语音模式双向流」与「Live」
func (s *server) serveVoice(ctx context.Context, conn *websocket.Conn, session *antigravity.LiveSession, setup liveSetup) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	v := &voiceSession{
		server: s, ctx: ctx, conn: conn, stream: session.Stream, session: session, observer: session.ObserverStream, setup: setup, model: session.Model.ID, trajectory: uuid.NewString(),
		inputText: len(setup.InputTranscription) > 0, outputText: len(setup.OutputTranscription) > 0,
		tools: make(chan []json.RawMessage, 1), failed: make(chan error, 1), rate: antigravity.VoiceMicRate,
	}
	v.quiet = time.AfterFunc(time.Hour, v.endTurn)
	v.quiet.Stop()
	defer v.quiet.Stop()
	var recognizing sync.WaitGroup
	defer func() {
		cancel()
		v.mu.Lock()
		observer := v.observer
		v.mu.Unlock()
		observer.Close()
		recognizing.Wait()
		v.mu.Lock()
		if v.observer != session.ObserverStream {
			v.observer.Close()
		}
		v.mu.Unlock()
	}()
	ready := make(chan struct{})
	go v.receive(session.First, ready)
	select {
	case <-ready:
	case err := <-v.failed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := v.send(map[string]any{"setupComplete": map[string]any{}}); err != nil {
		return err
	}
	go v.pace()
	go v.read()
	recognizing.Add(1)
	go func() {
		defer recognizing.Done()
		v.recognize()
	}()
	select {
	case err := <-v.failed:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// receive 处理上游消息，进入聆听状态时关闭 ready
func (v *voiceSession) receive(first []byte, ready chan struct{}) {
	raw := first
	opened := false
	for {
		message, err := antigravity.ParseVoiceMessage(raw)
		if err != nil {
			v.fail(err)
			return
		}
		switch {
		case message.State == 5:
			v.fail(fmt.Errorf("voice session failed: %s", message.Failure))
			return
		case message.State == 2 && !opened:
			opened = true
			close(ready)
		case message.Call != nil:
			if err := v.call(message.Call); err != nil {
				v.fail(err)
				return
			}
		case message.Audio != nil:
			err = v.audio(message.Audio)
		case message.Speech != "" && v.outputText:
			err = v.send(map[string]any{"serverContent": map[string]any{"outputTranscription": map[string]any{"text": message.Speech}}})
		case message.User != "" && message.UserFinal && v.inputText:
			err = v.send(map[string]any{"serverContent": map[string]any{"inputTranscription": map[string]any{"text": message.User}}})
		case message.Clear:
			v.mu.Lock()
			v.talking = false
			v.mu.Unlock()
			v.quiet.Stop()
			err = v.send(map[string]any{"serverContent": map[string]any{"interrupted": true}})
		}
		if err != nil {
			v.fail(err)
			return
		}
		if raw, err = v.stream.Recv(); err != nil {
			v.fail(err)
			return
		}
	}
}

// audio 写出一段 24 kHz 语音并重新计时回合结束
func (v *voiceSession) audio(pcm []byte) error {
	v.mu.Lock()
	v.talking = true
	v.mu.Unlock()
	v.quiet.Reset(liveTurnQuiet)
	return v.send(map[string]any{"serverContent": map[string]any{"modelTurn": map[string]any{"parts": []any{map[string]any{"inlineData": map[string]any{
		"mimeType": "audio/pcm;rate=" + strconv.Itoa(antigravity.VoiceOutputRate), "data": base64.StdEncoding.EncodeToString(pcm),
	}}}}}})
}

// endTurn 在语音回复静默 liveTurnQuiet 后发出 generationComplete 与 turnComplete
func (v *voiceSession) endTurn() {
	v.mu.Lock()
	talking := v.talking
	v.talking = false
	v.mu.Unlock()
	if !talking {
		return
	}
	if err := v.send(map[string]any{"serverContent": map[string]any{"generationComplete": true}}); err != nil {
		v.fail(err)
		return
	}
	if err := v.send(map[string]any{"serverContent": map[string]any{"turnComplete": true}}); err != nil {
		v.fail(err)
	}
}

// call 回复上游对 language_server 的调用：订阅会话状态回复空闲状态并保持，交给智能体的请求先以空回复结束再在后台生成，取消调用结束当前生成，其余调用以空回复结束
func (v *voiceSession) call(call *antigravity.VoiceCall) error {
	if call.Cancel {
		return nil
	}
	switch call.Method {
	case antigravity.VoiceStreamState:
		v.mu.Lock()
		v.stateID = call.ID
		v.mu.Unlock()
		return v.stream.Send(antigravity.VoiceReplyMessage(call.ID, antigravity.VoiceIdleState(v.trajectory), false))
	case antigravity.VoiceSendMessage:
		handoff, err := antigravity.ParseVoiceHandoff(call.Request)
		if err != nil {
			return err
		}
		if err := v.finish(call.ID); err != nil {
			return err
		}
		v.startTurn(handoff)
		return nil
	case antigravity.VoiceCancel:
		v.mu.Lock()
		if v.turn != nil {
			v.turn.cancel()
		}
		v.mu.Unlock()
	}
	return v.finish(call.ID)
}

// finish 以空回复结束一次调用
func (v *voiceSession) finish(call int64) error {
	if err := v.stream.Send(antigravity.VoiceReplyMessage(call, []byte{}, false)); err != nil {
		return err
	}
	return v.stream.Send(antigravity.VoiceReplyMessage(call, nil, true))
}

// state 在会话状态订阅上推送一次状态
func (v *voiceSession) state(update []byte) error {
	v.mu.Lock()
	id := v.stateID
	v.mu.Unlock()
	return v.stream.Send(antigravity.VoiceReplyMessage(id, update, false))
}

// startTurn 按收到的顺序登记一次智能体回合：取消上一回合，等它退出后再运行这一回合
func (v *voiceSession) startTurn(handoff antigravity.VoiceHandoff) {
	ctx, cancel := context.WithCancel(v.ctx)
	turn := &voiceTurn{cancel: cancel, done: make(chan struct{})}
	v.mu.Lock()
	previous := v.turn
	v.turn = turn
	index := v.steps + 1
	v.steps += 2
	v.mu.Unlock()
	go func() {
		defer close(turn.done)
		defer cancel()
		if previous != nil {
			previous.cancel()
			<-previous.done
		}
		v.handoff(ctx, handoff, index)
	}()
}

// handoff 以该模型回答语音模式交给智能体的请求：文字与语音片段作为用户回合接在会话历史后，函数调用经 toolCall 交给客户端并等待 toolResponse，回复文字以步骤完成状态交给语音朗读；生成失败时结束会话
func (v *voiceSession) handoff(ctx context.Context, handoff antigravity.VoiceHandoff, index int) {
	var parts []geminiPart
	for _, text := range handoff.Text {
		parts = append(parts, geminiPart{Text: &text})
	}
	for _, media := range handoff.Media {
		parts = append(parts, geminiPart{InlineData: &geminiBlobPart{MIMEType: media.MIME, Data: base64.StdEncoding.EncodeToString(media.Data)}})
	}
	v.history = append(v.history, geminiContent{Role: "user", Parts: parts})
	if ctx.Err() != nil {
		return
	}
	for _, update := range [][]byte{antigravity.VoiceRunningState(v.trajectory), antigravity.VoiceGeneratingState(v.trajectory, index)} {
		if err := v.state(update); err != nil {
			v.fail(err)
			return
		}
	}
	for {
		text, calls, err := v.generate(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			v.fail(err)
			return
		}
		var model []geminiPart
		if text != "" {
			model = append(model, geminiPart{Text: &text})
		}
		for _, call := range calls {
			part := geminiPart{}
			json.Unmarshal(append(append([]byte(`{"functionCall":`), call...), '}'), &part)
			model = append(model, part)
		}
		if len(model) > 0 {
			v.history = append(v.history, geminiContent{Role: "model", Parts: model})
		}
		if len(calls) == 0 {
			if err := v.state(antigravity.VoiceReplyState(v.trajectory, index, text)); err != nil {
				v.fail(err)
				return
			}
			if err := v.state(antigravity.VoiceFinishedState(v.trajectory)); err != nil {
				v.fail(err)
			}
			return
		}
		results, err := v.toolRound(ctx, calls)
		if err != nil {
			if ctx.Err() == nil {
				v.fail(err)
			}
			return
		}
		v.history = append(v.history, geminiContent{Role: "user", Parts: results})
	}
}

// generate 以 setup 的系统指令、工具与生成参数和会话历史经 Generate 生成一轮，返回正文与函数调用；生成不改写语音会话的账本记录，只把用量计入其中
func (v *voiceSession) generate(ctx context.Context) (string, []json.RawMessage, error) {
	config := v.setup.GenerationConfig
	config.ResponseModalities, config.SpeechConfig = nil, nil
	request, err := geminiRequest{Contents: v.history, SystemInstruction: v.setup.SystemInstruction, GenerationConfig: config, Tools: v.setup.Tools}.toGenerateRequest(newID("live"), v.model)
	if err != nil {
		return "", nil, invalidLive("%s", err.Error())
	}
	request.AccountID = v.session.Account
	events, err := v.server.service.Generate(requestdb.WithTrace(ctx, nil), request)
	if err != nil {
		return "", nil, err
	}
	var text strings.Builder
	var calls []json.RawMessage
	for event := range events {
		switch event.Kind {
		case antigravity.EventText:
			text.WriteString(event.Text)
		case antigravity.EventToolCall:
			call, _ := json.Marshal(map[string]any{"id": event.ToolCall.ID, "name": event.ToolCall.Name, "args": rawJSONValue(event.ToolCall.Arguments, map[string]any{})})
			calls = append(calls, call)
		case antigravity.EventUsage:
			requestdb.TraceFrom(v.ctx).Observe(event)
		case antigravity.EventError:
			return "", nil, event.Err
		}
	}
	return text.String(), calls, nil
}

// toolRound 把函数调用以 toolCall 发给客户端，等到每个调用的 toolResponse 后返回 functionResponse part
func (v *voiceSession) toolRound(ctx context.Context, calls []json.RawMessage) ([]geminiPart, error) {
	if err := v.send(map[string]any{"toolCall": map[string]any{"functionCalls": calls}}); err != nil {
		return nil, err
	}
	pending := make(map[string]bool, len(calls))
	for _, call := range calls {
		var head struct {
			ID string `json:"id"`
		}
		json.Unmarshal(call, &head)
		pending[head.ID] = true
	}
	var parts []geminiPart
	for len(pending) > 0 {
		select {
		case responses := <-v.tools:
			for _, response := range responses {
				var head struct {
					ID string `json:"id"`
				}
				json.Unmarshal(response, &head)
				if !pending[head.ID] {
					continue
				}
				delete(pending, head.ID)
				part := geminiPart{}
				if err := json.Unmarshal(append(append([]byte(`{"functionResponse":`), response...), '}'), &part); err != nil {
					return nil, invalidLive("Invalid toolResponse: %v", err)
				}
				parts = append(parts, part)
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return parts, nil
}

// read 处理客户端消息：音频转为 16 kHz 放入麦克风缓冲，函数结果交给进行中的智能体回合，语音会话不接受文字、视频、手动语音活动与 clientContent
func (v *voiceSession) read() {
	for {
		_, data, err := v.conn.Read(v.ctx)
		if err != nil {
			v.fail(err)
			return
		}
		if data, err = geminiCamelKeys(data); err != nil {
			v.fail(invalidLive("Invalid JSON payload: %v", err))
			return
		}
		var message liveClientMessage
		if err := json.Unmarshal(data, &message); err != nil {
			v.fail(invalidLive("Invalid JSON payload: %v", err))
			return
		}
		switch {
		case len(message.ClientContent) > 0:
			v.fail(invalidLive("Voice sessions take audio input; clientContent is not supported"))
			return
		case message.ToolResponse != nil:
			select {
			case v.tools <- message.ToolResponse.FunctionResponses:
			case <-v.ctx.Done():
				return
			}
		case message.RealtimeInput != nil:
			input := message.RealtimeInput
			if input.Text != nil || len(input.Video) > 0 || len(input.ActivityStart) > 0 || len(input.ActivityEnd) > 0 {
				v.fail(invalidLive("Voice sessions take realtimeInput.audio only"))
				return
			}
			blobs := input.MediaChunks
			if input.Audio != nil {
				blobs = append(blobs, *input.Audio)
			}
			for _, blob := range blobs {
				if err := v.micInput(blob); err != nil {
					v.fail(err)
					return
				}
			}
		}
	}
}

// micInput 把一段 PCM 音频转为 16 kHz 后放入麦克风缓冲，rate 缺省为 16000
func (v *voiceSession) micInput(blob geminiBlobPart) error {
	kind, parameters, err := mime.ParseMediaType(blob.MIME())
	if err != nil || kind != "audio/pcm" && kind != "audio/l16" {
		return invalidLive("Voice sessions take 16-bit PCM audio (audio/pcm;rate=16000), got %q", blob.MIME())
	}
	rate := antigravity.VoiceMicRate
	if value := parameters["rate"]; value != "" {
		if rate, err = strconv.Atoi(value); err != nil || rate <= 0 {
			return invalidLive("Invalid audio rate %q", value)
		}
	}
	pcm, err := base64.StdEncoding.DecodeString(blob.Data)
	if err != nil {
		return invalidLive("Invalid audio data: %v", err)
	}
	if rate != antigravity.VoiceMicRate {
		pcm = v.convert(pcm, rate)
	}
	v.mu.Lock()
	v.mic = append(v.mic, pcm...)
	v.mu.Unlock()
	return nil
}

// convert 以会话内保持状态的重采样器把 PCM 换为 16 kHz
func (v *voiceSession) convert(pcm []byte, rate int) []byte {
	if v.resample == nil || v.rate != rate {
		v.resample, v.rate = resampler.NewWithSkipZeros(1, rate, antigravity.VoiceMicRate, 10), rate
	}
	input := make([]float64, len(pcm)/2)
	for index := range input {
		input[index] = float64(int16(binary.LittleEndian.Uint16(pcm[2*index:]))) / 32768
	}
	output := make([]float64, len(input)*antigravity.VoiceMicRate/rate+16)
	_, written := v.resample.ProcessFloat64(0, input, output)
	out := make([]byte, 2*written)
	for index, sample := range output[:written] {
		binary.LittleEndian.PutUint16(out[2*index:], uint16(int16(math.Round(max(-32768, min(32767, sample*32768))))))
	}
	return out
}

// recognize 把转写模型的临时与最终识别结果发给语音流；转写模型流结束（上游每条流约 300 秒）后在同一账户上重新打开
func (v *voiceSession) recognize() {
	for {
		v.mu.Lock()
		observer := v.observer
		v.mu.Unlock()
		raw, err := observer.Recv()
		if err != nil {
			if v.ctx.Err() != nil {
				return
			}
			next, err := v.session.OpenObserver(v.ctx)
			if err != nil {
				v.fail(err)
				return
			}
			v.mu.Lock()
			if v.observer != v.session.ObserverStream {
				v.observer.Close()
			}
			v.observer = next
			v.mu.Unlock()
			continue
		}
		text, final, _, err := antigravity.ParseObserverMessage(raw)
		if err != nil {
			v.fail(err)
			return
		}
		if text == "" {
			continue
		}
		if err := v.stream.Send(antigravity.VoiceTranscriptMessage(text, final)); err != nil {
			v.fail(err)
			return
		}
	}
}

// pace 每 40 ms 向语音流与转写模型流发送 1280 字节麦克风音频，缓冲不足时补静音
func (v *voiceSession) pace() {
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	silence := make([]byte, antigravity.VoiceMicFrame)
	for {
		select {
		case <-ticker.C:
		case <-v.ctx.Done():
			return
		}
		v.mu.Lock()
		frame := silence
		if len(v.mic) > 0 {
			frame = slices.Clone(v.mic[:min(len(v.mic), antigravity.VoiceMicFrame)])
			v.mic = v.mic[len(frame):]
			if len(frame) < antigravity.VoiceMicFrame {
				frame = append(frame, silence[len(frame):]...)
			}
		}
		observer := v.observer
		v.mu.Unlock()
		if err := v.stream.Send(antigravity.VoiceMicMessage(frame)); err != nil {
			v.fail(err)
			return
		}
		observer.Send(antigravity.ObserverAudioMessage(frame))
	}
}
