package antigravity

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// voiceMethod 为官方语音模式打开的双向流方法
const voiceMethod = "/google.gca.aicode.v1alpha.JetskiRemoteAgentProxyService/StreamVoice"

// liveDescriptors 为 language_server 内嵌的 aicode BidiGenerateContent 消息描述符，由 Lab live_descriptor.py 导出
//
//go:embed live.desc.gz
var liveDescriptors []byte

// liveTypes 为 BidiGenerateContent 客户端与服务端消息的描述符
var liveTypes = sync.OnceValues(func() ([2]protoreflect.MessageDescriptor, error) {
	reader, err := gzip.NewReader(bytes.NewReader(liveDescriptors))
	if err != nil {
		return [2]protoreflect.MessageDescriptor{}, err
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return [2]protoreflect.MessageDescriptor{}, err
	}
	var set descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(data, &set); err != nil {
		return [2]protoreflect.MessageDescriptor{}, err
	}
	files, err := protodesc.NewFiles(&set)
	if err != nil {
		return [2]protoreflect.MessageDescriptor{}, err
	}
	var types [2]protoreflect.MessageDescriptor
	for index, name := range []protoreflect.FullName{"google.gca.aicode.v1main.BidiGenerateContentClientMessage", "google.gca.aicode.v1main.BidiGenerateContentServerMessage"} {
		descriptor, err := files.FindDescriptorByName(name)
		if err != nil {
			return types, err
		}
		types[index] = descriptor.(protoreflect.MessageDescriptor)
	}
	return types, nil
})

// LiveClientMessage 把 Gemini Live API 的客户端 JSON 消息编码为 aicode BidiGenerateContentClientMessage，字段按编号顺序写出，字段名可用 camelCase 或 snake_case，上游消息没有的字段返回错误
func LiveClientMessage(data []byte) ([]byte, error) {
	types, err := liveTypes()
	if err != nil {
		return nil, err
	}
	message := dynamicpb.NewMessage(types[0])
	if err := protojson.Unmarshal(data, message); err != nil {
		return nil, err
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(message)
}

// LiveServerMessage 把 aicode BidiGenerateContentServerMessage 解码为 Gemini Live API 的服务端 JSON 消息，去掉公开协议没有的 serverContent.undecidedInputTranscription
func LiveServerMessage(raw []byte) ([]byte, error) {
	types, err := liveTypes()
	if err != nil {
		return nil, err
	}
	message := dynamicpb.NewMessage(types[1])
	if err := proto.Unmarshal(raw, message); err != nil {
		return nil, err
	}
	if content := types[1].Fields().ByName("server_content"); message.Has(content) {
		inner := message.Mutable(content).Message()
		inner.Clear(inner.Descriptor().Fields().ByName("undecided_input_transcription"))
	}
	return protojson.Marshal(message)
}

// LiveStream 表示一条 aicode gRPC 双向流，收发已编码的 protobuf 消息
type LiveStream struct {
	conn   *grpc.ClientConn
	stream grpc.ClientStream
	cancel context.CancelFunc
	send   sync.Mutex
}

// Send 发送一条消息，可并发调用
func (s *LiveStream) Send(message []byte) error {
	s.send.Lock()
	defer s.send.Unlock()
	return s.stream.SendMsg(&message)
}

// CloseSend 结束发送，上游读完已发送的消息后结束流
func (s *LiveStream) CloseSend() error {
	s.send.Lock()
	defer s.send.Unlock()
	return s.stream.CloseSend()
}

// Recv 读取下一条消息，流正常结束时返回 io.EOF，上游以错误状态结束时返回 grpcError 换成的 UpstreamError
func (s *LiveStream) Recv() ([]byte, error) {
	var raw []byte
	if err := s.stream.RecvMsg(&raw); err != nil {
		return nil, grpcError(err)
	}
	return raw, nil
}

// grpcHTTP 为 gRPC 状态码对应的 HTTP 状态码（google.rpc.Code 的标准映射）
var grpcHTTP = map[codes.Code]int{
	codes.Canceled: 499, codes.Unknown: http.StatusInternalServerError, codes.InvalidArgument: http.StatusBadRequest,
	codes.DeadlineExceeded: http.StatusGatewayTimeout, codes.NotFound: http.StatusNotFound, codes.AlreadyExists: http.StatusConflict,
	codes.PermissionDenied: http.StatusForbidden, codes.ResourceExhausted: http.StatusTooManyRequests, codes.FailedPrecondition: http.StatusBadRequest,
	codes.Aborted: http.StatusConflict, codes.OutOfRange: http.StatusBadRequest, codes.Unimplemented: http.StatusNotImplemented,
	codes.Internal: http.StatusInternalServerError, codes.Unavailable: http.StatusServiceUnavailable, codes.DataLoss: http.StatusInternalServerError,
	codes.Unauthenticated: http.StatusUnauthorized,
}

// grpcError 把上游返回的 gRPC 状态换成与 cloudcode 错误同形的 UpstreamError：HTTP 状态取标准映射，正文为 {"error":{code,message,status,details}}，details 保留 ErrorInfo 的 reason 与 metadata 和 RetryInfo 的 retryDelay；连接错误与 io.EOF 原样返回
func grpcError(err error) error {
	if errors.Is(err, io.EOF) {
		return err
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	code, known := grpcHTTP[st.Code()]
	if !known || st.Code() == codes.Canceled {
		return err
	}
	type detail struct {
		Type       string            `json:"@type"`
		Reason     string            `json:"reason,omitempty"`
		Domain     string            `json:"domain,omitempty"`
		Metadata   map[string]string `json:"metadata,omitempty"`
		RetryDelay string            `json:"retryDelay,omitempty"`
	}
	var details []detail
	for _, item := range st.Details() {
		switch info := item.(type) {
		case *errdetails.ErrorInfo:
			details = append(details, detail{Type: "type.googleapis.com/google.rpc.ErrorInfo", Reason: info.Reason, Domain: info.Domain, Metadata: info.Metadata})
		case *errdetails.RetryInfo:
			details = append(details, detail{Type: "type.googleapis.com/google.rpc.RetryInfo", RetryDelay: strconv.FormatFloat(info.RetryDelay.AsDuration().Seconds(), 'f', -1, 64) + "s"})
		}
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": st.Message(), "status": codeName(st.Code()), "details": details}})
	return &UpstreamError{Status: code, Body: body}
}

// codeName 返回 gRPC 状态码的 google.rpc.Code 名称，如 PERMISSION_DENIED
func codeName(code codes.Code) string {
	name := code.String()
	var out []byte
	for index := 0; index < len(name); index++ {
		c := name[index]
		if index > 0 && c >= 'A' && c <= 'Z' {
			out = append(out, '_')
		}
		out = append(out, c&^0x20)
	}
	return string(out)
}

// Close 取消流并关闭连接，阻塞中的 Send 随之返回
func (s *LiveStream) Close() {
	s.cancel()
	s.conn.Close()
}

// openStream 以 grpc-go 向 aicode 打开一条双向流：每条流各用一条经账户代理的连接，userAgent 不为空时作为 grpc-go User-Agent 的前缀，也用于代理 CONNECT
func (c *Client) openStream(ctx context.Context, method, userAgent string) (*LiveStream, error) {
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
		grpc.WithPerRPCCredentials(bearer{c.source}),
		grpc.WithContextDialer(func(ctx context.Context, address string) (net.Conn, error) {
			return c.dialAICode(ctx, address, connectAgent(userAgent))
		}),
	}
	if userAgent != "" {
		options = append(options, grpc.WithUserAgent(userAgent))
	}
	connection, err := grpc.NewClient("passthrough:///"+aicodeTarget, options...)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(ctx, cancel)
	stream, err := connection.NewStream(streamCtx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, method, grpc.ForceCodecV2(rawCodec{}))
	if err != nil {
		stop()
		cancel()
		connection.Close()
		return nil, fmt.Errorf("aicode %s: %w", methodName(method), grpcError(err))
	}
	return &LiveStream{conn: connection, stream: stream, cancel: func() { stop(); cancel() }}, nil
}

// connectAgent 返回代理 CONNECT 的 User-Agent：与 grpc-go 写出的 User-Agent 相同
func connectAgent(userAgent string) string {
	if userAgent == "" {
		return grpcUserAgent
	}
	return userAgent + " " + grpcUserAgent
}

// methodName 返回 gRPC 方法路径的最后一段
func methodName(method string) string {
	for index := len(method) - 1; index >= 0; index-- {
		if method[index] == '/' {
			return method[index+1:]
		}
	}
	return method
}

// OpenLive 按官方语音转写打开 BidiGenerateContent 双向流，User-Agent 为 grpc-go 默认值
func (c *Client) OpenLive(ctx context.Context) (*LiveStream, error) {
	return c.openStream(ctx, bidiMethod, "")
}

// OpenVoice 按官方语音模式打开 StreamVoice 双向流，User-Agent 以账户设备档案的客户端 User-Agent 开头
func (c *Client) OpenVoice(ctx context.Context) (*LiveStream, error) {
	return c.openStream(ctx, voiceMethod, c.device.UserAgent())
}

// VoiceSetup 表示 StreamVoice 的 setup：会话 ID、音色、麦克风采样率、计划模型枚举与语音识别的语言提示；用户语音的识别结果由客户端经转写模型取得后发送
type VoiceSetup struct {
	Cascade   string
	Voice     string
	Rate      int
	PlanModel int
	Languages []string
}

// VoiceMicRate 为官方语音模式麦克风音频的采样率
const VoiceMicRate = 16000

// VoiceOutputRate 为 StreamVoice 语音输出的采样率
const VoiceOutputRate = 24000

// VoiceMicFrame 为官方每 40 ms 发送的麦克风音频字节数
const VoiceMicFrame = 1280

// voiceClient 把 VoiceClientMessage 包成 StreamVoiceRequest
func voiceClient(field int, value []byte) []byte {
	return appendBytes(nil, 1, appendBytes(nil, field, value))
}

// VoiceSetupMessage 编码官方 setup：会话、音色、采样率、计划模型、发送客户端转写与语言提示
func VoiceSetupMessage(setup VoiceSetup) []byte {
	b := appendString(nil, 1, setup.Cascade)
	b = appendString(b, 2, setup.Voice)
	b = appendVarint(b, 3, uint64(setup.Rate))
	b = appendVarint(b, 4, uint64(setup.PlanModel))
	b = appendVarint(b, 6, 1)
	if len(setup.Languages) > 0 {
		hint := appendVarint(nil, 1, 3)
		hint = appendVarint(hint, 2, 1)
		for _, language := range setup.Languages {
			hint = appendString(hint, 5, language)
		}
		b = appendBytes(b, 13, hint)
	}
	return voiceClient(1, b)
}

// VoiceSettingsMessage 编码 setup 之后官方发送的 settings_update
func VoiceSettingsMessage(planModel int) []byte {
	return voiceClient(3, appendVarint(nil, 1, uint64(planModel)))
}

// VoiceMicMessage 编码一段麦克风音频
func VoiceMicMessage(pcm []byte) []byte {
	return voiceClient(2, pcm)
}

// VoiceTranscriptMessage 编码转写模型对用户语音的识别结果，final 为该段识别结束
func VoiceTranscriptMessage(text string, final bool) []byte {
	b := appendString(nil, 1, text)
	if final {
		b = appendVarint(b, 2, 1)
	}
	return voiceClient(5, b)
}

// ObserverSetupMessage 编码官方语音模式中转写模型双向流的 setup：模型、自动语音活动检测（结束灵敏度高、静默 500 ms）与输入转写
func ObserverSetupMessage(model string) []byte {
	detection := appendVarint(nil, 1, 0)
	detection = appendVarint(detection, 4, 1)
	detection = appendVarint(detection, 5, 500)
	setup := appendString(nil, 1, model)
	setup = appendBytes(setup, 5, appendBytes(nil, 6, detection))
	setup = appendBytes(setup, 8, nil)
	return appendBytes(nil, 1, setup)
}

// ObserverAudioMessage 编码发给转写模型的一段 16 kHz 麦克风音频
func ObserverAudioMessage(pcm []byte) []byte {
	return realtimeInput(1, audioBlob(pcm))
}

// ParseObserverMessage 返回转写模型消息中的识别文本：识别中的临时文本 final 为 false，识别结束的文本 final 为 true；setup 为 setupComplete
func ParseObserverMessage(raw []byte) (text string, final, setup bool, err error) {
	err = walkWire(raw, func(field protowire.Number, value []byte, number uint64) error {
		switch field {
		case 1:
			setup = true
		case 2:
			return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
				if field != 6 && field != 11 {
					return nil
				}
				final = field == 6
				return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
					if field == 1 {
						text = string(value)
					}
					return nil
				})
			})
		}
		return nil
	})
	return text, final, setup, err
}

// VoiceReplyMessage 编码对 language_server_call 的一个回复，end 为 true 时以空状态结束该调用
func VoiceReplyMessage(call int64, response []byte, end bool) []byte {
	b := appendVarint(nil, 1, uint64(call))
	if end {
		b = appendBytes(b, 3, nil)
	} else {
		b = appendBytes(b, 2, response)
	}
	return voiceClient(4, b)
}

// 语音模式中服务端调用 language_server 的方法
const (
	// VoiceStreamState 为订阅会话状态的流式调用
	VoiceStreamState = 1
	// VoiceSendMessage 为把用户请求交给智能体的调用
	VoiceSendMessage = 2
	// VoiceCancel 为取消智能体当前回合的调用
	VoiceCancel = 3
)

// VoiceMessage 表示一条 StreamVoiceResponse 中客户端用到的内容
type VoiceMessage struct {
	// Audio 为 24 kHz 单声道 16 位小端 PCM 语音
	Audio []byte
	// State 为会话状态：1 启动中、2 聆听中、3 重试中、4 停止中、5 失败
	State int
	// Failure 为失败状态的说明
	Failure string
	// Call 不为空时为服务端对 language_server 的调用
	Call *VoiceCall
	// Event 为事件名
	Event string
	// Speech 为语音的转写片段，Seq 为语音回合序号
	Speech string
	// User 为用户语音的识别结果，UserFinal 表示识别结束
	User      string
	UserFinal bool
	Seq       int
	// Clear 表示用户插话，客户端应停止播放已收到的语音
	Clear bool
}

// VoiceCall 表示服务端对 language_server 的一次调用，Cancel 为取消该调用
type VoiceCall struct {
	ID      int64
	Method  int
	Request []byte
	Cancel  bool
}

// ParseVoiceMessage 解码 StreamVoiceResponse
func ParseVoiceMessage(raw []byte) (VoiceMessage, error) {
	var message VoiceMessage
	err := walkWire(raw, func(field protowire.Number, value []byte, number uint64) error {
		if field != 1 {
			return nil
		}
		return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
			switch field {
			case 1:
				message.Audio = value
			case 2:
				return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
					switch field {
					case 1:
						message.State = int(number)
					case 3:
						message.Failure = string(value)
					}
					return nil
				})
			case 3:
				call := &VoiceCall{}
				message.Call = call
				return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
					switch field {
					case 1:
						call.ID = int64(number)
					case 2:
						return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
							switch field {
							case 1:
								call.Method = int(number)
							case 2:
								call.Request = value
							}
							return nil
						})
					case 3:
						call.Cancel = true
					}
					return nil
				})
			case 4:
				return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
					if field == 1 {
						message.Event = string(value)
					}
					return nil
				})
			case 7:
				return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
					switch field {
					case 1:
						message.Speech = string(value)
					case 2:
						message.Seq = int(number)
					}
					return nil
				})
			case 8:
				message.UserFinal = true
				return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
					switch field {
					case 1:
						message.User = string(value)
					case 2:
						message.Seq = int(number)
					case 3:
						message.UserFinal = number == 0
					}
					return nil
				})
			case 9:
				message.Clear = true
			}
			return nil
		})
	})
	return message, err
}

// walkWire 依次回调消息的各字段：长度前缀字段给出内容，varint 字段给出数值
func walkWire(raw []byte, visit func(field protowire.Number, value []byte, number uint64) error) error {
	for len(raw) > 0 {
		field, kind, length := protowire.ConsumeTag(raw)
		if length < 0 {
			return protowire.ParseError(length)
		}
		raw = raw[length:]
		var value []byte
		var number uint64
		switch kind {
		case protowire.BytesType:
			value, length = protowire.ConsumeBytes(raw)
		case protowire.VarintType:
			number, length = protowire.ConsumeVarint(raw)
		default:
			length = protowire.ConsumeFieldValue(field, kind, raw)
		}
		if length < 0 {
			return protowire.ParseError(length)
		}
		raw = raw[length:]
		if err := visit(field, value, number); err != nil {
			return err
		}
	}
	return nil
}

// VoiceHandoff 表示语音模式交给智能体的一次请求：文字（含官方语音说明与对话记录）与附带的语音片段
type VoiceHandoff struct {
	Text  []string
	Media []Blob
}

// ParseVoiceHandoff 解码 SendUserCascadeMessageRequest 中的文字项（字段 2）与媒体（字段 14）
func ParseVoiceHandoff(raw []byte) (VoiceHandoff, error) {
	var handoff VoiceHandoff
	err := walkWire(raw, func(field protowire.Number, value []byte, number uint64) error {
		switch field {
		case 2:
			return walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
				if field == 1 {
					handoff.Text = append(handoff.Text, string(value))
				}
				return nil
			})
		case 14:
			var blob Blob
			err := walkWire(value, func(field protowire.Number, value []byte, number uint64) error {
				switch field {
				case 1:
					blob.MIME = string(value)
				case 2:
					blob.Data = value
				}
				return nil
			})
			if blob.Data != nil {
				handoff.Media = append(handoff.Media, blob)
			}
			return err
		}
		return nil
	})
	return handoff, err
}

// 智能体状态更新中的会话运行状态
const (
	voiceIdle    = 1
	voiceRunning = 2
)

// stepGenerating 为智能体步骤生成中的状态，完成为 stepDone
const stepGenerating = 8

// agentUpdate 编码 StreamAgentStateUpdatesResponse：轨迹、运行状态、可选的主轨迹步骤更新与结束原因
func agentUpdate(trajectory string, status int, steps []byte, finished bool) []byte {
	update := appendString(nil, 2, trajectory)
	update = appendVarint(update, 3, uint64(status))
	if finished {
		update = appendBytes(update, 6, appendVarint(nil, 1, 4))
	}
	if steps != nil {
		update = appendBytes(update, 7, appendBytes(nil, 1, steps))
	}
	return appendBytes(nil, 1, update)
}

// stepsUpdate 编码一个步骤的更新：下标、步骤与轨迹总步数
func stepsUpdate(index int, step []byte) []byte {
	b := appendBytes(nil, 1, appendVarintRaw(nil, uint64(index)))
	b = appendBytes(b, 2, step)
	return appendVarint(b, 3, uint64(index+1))
}

// appendVarintRaw 追加不带字段标签的 varint，用于 packed 重复字段
func appendVarintRaw(b []byte, value uint64) []byte {
	return protowire.AppendVarint(b, value)
}

// VoiceIdleState 返回订阅建立时官方回复的空闲状态
func VoiceIdleState(trajectory string) []byte {
	return agentUpdate(trajectory, voiceIdle, []byte{}, false)
}

// VoiceRunningState 返回智能体开始处理时的运行状态
func VoiceRunningState(trajectory string) []byte {
	return agentUpdate(trajectory, voiceRunning, nil, false)
}

// VoiceGeneratingState 返回回复步骤生成中的状态，index 为回复步骤的下标
func VoiceGeneratingState(trajectory string, index int) []byte {
	return agentUpdate(trajectory, voiceRunning, stepsUpdate(index, appendVarint(nil, 4, stepGenerating)), false)
}

// VoiceReplyState 返回回复步骤完成的状态，语音模式朗读 text
func VoiceReplyState(trajectory string, index int, text string) []byte {
	step := appendVarint(nil, 4, stepDone)
	step = appendBytes(step, 20, appendString(nil, 1, text))
	return agentUpdate(trajectory, voiceRunning, stepsUpdate(index, step), false)
}

// VoiceFinishedState 返回回合结束、智能体回到空闲的状态
func VoiceFinishedState(trajectory string) []byte {
	return agentUpdate(trajectory, voiceIdle, nil, true)
}

// ModelEnum 返回目录条目在 language_server 模型枚举中的值
func ModelEnum(model CatalogModel) (int, bool) {
	value, ok := sessionFixed.Models[model.Model]
	return value, ok
}
