package antigravity

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/oauth2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/mem"
	"google.golang.org/protobuf/encoding/protowire"
)

const (
	// aicodeTarget 为语音转写 gRPC 连接的目标
	aicodeTarget = "aicode.googleapis.com:443"
	// bidiMethod 为官方语音转写打开的双向流方法
	bidiMethod = "/google.gca.aicode.v1alpha.PredictionService/BidiGenerateContent"
	// transcriptionMIME 为音频块的 MIME，16 kHz 单声道 16 位小端 PCM
	transcriptionMIME = "audio/pcm;rate=16000"
	// transcriptionChunk 为每个音频块的字节数，即 40 ms 的 16 kHz PCM
	transcriptionChunk = 1280
	// transcriptionInterval 为相邻音频块的发送间隔
	transcriptionInterval = 40 * time.Millisecond
	// grpcUserAgent 为 grpc-go 写出的 User-Agent，也是经 HTTP 代理时 CONNECT 的 User-Agent
	grpcUserAgent = "grpc-go/" + grpc.Version
)

// rawCodec 原样收发已编码的 protobuf 字节，名称为空使请求的 content-type 为 application/grpc
type rawCodec struct{}

// Marshal 返回已编码的消息
func (rawCodec) Marshal(v any) (mem.BufferSlice, error) {
	return mem.BufferSlice{mem.SliceBuffer(*v.(*[]byte))}, nil
}

// Unmarshal 复制收到的消息
func (rawCodec) Unmarshal(data mem.BufferSlice, v any) error {
	*v.(*[]byte) = data.Materialize()
	return nil
}

// Name 返回空的 content-subtype
func (rawCodec) Name() string { return "" }

// bearer 以账户 token 为每次 gRPC 调用写入 authorization 元数据
type bearer struct {
	source oauth2.TokenSource
}

// GetRequestMetadata 返回 Bearer 授权头
func (b bearer) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	token, err := b.source.Token()
	if err != nil {
		return nil, err
	}
	return map[string]string{"authorization": token.Type() + " " + token.AccessToken}, nil
}

// RequireTransportSecurity 要求 TLS
func (bearer) RequireTransportSecurity() bool { return true }

// Transcription 表示一次语音转写的最终文本与流中各消息
type Transcription struct {
	Text     string
	Messages []string
}

// Transcribe 按官方语音输入的顺序转写 16 kHz 单声道 PCM：以 grpc-go 打开 BidiGenerateContent 双向流，发送 setup 并等到 setupComplete，再发 activity start，音频按 40 ms 一个 1280 字节的块以实时节奏发送，开头补发流打开以来应已录下的块，最后发 activity end 并结束发送；返回各 inputTranscription 文本以空格相连的结果
func (c *Client) Transcribe(ctx context.Context, model string, pcm []byte) (Transcription, error) {
	connection, err := grpc.NewClient("passthrough:///"+aicodeTarget,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{})),
		grpc.WithPerRPCCredentials(bearer{c.source}),
		grpc.WithContextDialer(c.dialAICode))
	if err != nil {
		return Transcription{}, err
	}
	defer connection.Close()
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	started := time.Now()
	stream, err := connection.NewStream(streamCtx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, bidiMethod, grpc.ForceCodecV2(rawCodec{}))
	if err != nil {
		return Transcription{}, transcriptionError(ctx, err)
	}
	send := func(message []byte) error { return stream.SendMsg(&message) }
	if err := send(setupMessage(model)); err != nil {
		return Transcription{}, transcriptionError(ctx, err)
	}
	var result Transcription
	var texts []string
	receive := func() (serverMessage, error) {
		var raw []byte
		if err := stream.RecvMsg(&raw); err != nil {
			return serverMessage{}, err
		}
		result.Messages = append(result.Messages, base64.StdEncoding.EncodeToString(raw))
		return parseServerMessage(raw)
	}
	first, err := receive()
	if err != nil {
		return result, transcriptionError(ctx, err)
	}
	if !first.setupComplete {
		return result, fmt.Errorf("aicode BidiGenerateContent: first message is not setupComplete")
	}
	received := make(chan error, 1)
	go func() {
		for {
			message, err := receive()
			if err != nil {
				received <- err
				return
			}
			if message.inputTranscription != "" {
				texts = append(texts, message.inputTranscription)
			}
		}
	}()
	if err := send(realtimeInput(5, nil)); err != nil {
		return Transcription{}, transcriptionError(ctx, err)
	}
	ticker := time.NewTicker(transcriptionInterval)
	defer ticker.Stop()
	for offset := 0; offset < len(pcm); {
		due := min(len(pcm), (int(time.Since(started)/transcriptionInterval)+1)*transcriptionChunk)
		for ; offset < due; offset += transcriptionChunk {
			if err := send(realtimeInput(1, audioBlob(pcm[offset:min(offset+transcriptionChunk, len(pcm))]))); err != nil {
				return Transcription{}, transcriptionError(ctx, err)
			}
		}
		if offset < len(pcm) {
			select {
			case <-ticker.C:
			case <-streamCtx.Done():
				return Transcription{}, transcriptionError(ctx, streamCtx.Err())
			}
		}
	}
	if err := send(realtimeInput(6, nil)); err != nil {
		return Transcription{}, transcriptionError(ctx, err)
	}
	if err := stream.CloseSend(); err != nil {
		return Transcription{}, transcriptionError(ctx, err)
	}
	if err := <-received; !errors.Is(err, io.EOF) {
		return Transcription{}, transcriptionError(ctx, err)
	}
	result.Text = strings.Join(texts, " ")
	return result, nil
}

// transcriptionError 在调用方取消时返回取消原因，否则返回 gRPC 错误
func transcriptionError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("aicode BidiGenerateContent: %w", err)
}

// setupMessage 编码 setup：模型、关闭自动语音活动检测与输入转写
func setupMessage(model string) []byte {
	detection := protowire.AppendVarint(protowire.AppendTag(nil, 1, protowire.VarintType), 1)
	config := appendMessage(nil, 6, detection)
	setup := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), model)
	setup = appendMessage(setup, 5, config)
	setup = appendMessage(setup, 8, nil)
	return appendMessage(nil, 1, setup)
}

// realtimeInput 编码 realtimeInput 的一个字段：1 为音频，5 为 activity start，6 为 activity end
func realtimeInput(field protowire.Number, value []byte) []byte {
	return appendMessage(nil, 3, appendMessage(nil, field, value))
}

// audioBlob 编码一个音频块
func audioBlob(data []byte) []byte {
	blob := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), transcriptionMIME)
	return protowire.AppendBytes(protowire.AppendTag(blob, 2, protowire.BytesType), data)
}

// appendMessage 追加一个嵌套消息字段
func appendMessage(b []byte, field protowire.Number, value []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(b, field, protowire.BytesType), value)
}

// serverMessage 表示服务端消息中转写所需的字段
type serverMessage struct {
	setupComplete      bool
	inputTranscription string
	turnComplete       bool
}

// parseServerMessage 解码 setupComplete 与 serverContent 中的 inputTranscription 和 turnComplete
func parseServerMessage(raw []byte) (serverMessage, error) {
	var message serverMessage
	err := walkFields(raw, func(field protowire.Number, value []byte) error {
		switch field {
		case 1:
			message.setupComplete = true
		case 2:
			return walkFields(value, func(field protowire.Number, value []byte) error {
				switch field {
				case 6:
					return walkFields(value, func(field protowire.Number, value []byte) error {
						if field == 1 {
							message.inputTranscription = string(value)
						}
						return nil
					})
				case 3:
					message.turnComplete = len(value) > 0 && value[0] == 1
				}
				return nil
			})
		}
		return nil
	})
	return message, err
}

// walkFields 依次回调消息的各字段，长度前缀字段给出内容，varint 字段给出单字节真值
func walkFields(raw []byte, visit func(protowire.Number, []byte) error) error {
	for len(raw) > 0 {
		field, kind, length := protowire.ConsumeTag(raw)
		if length < 0 {
			return protowire.ParseError(length)
		}
		raw = raw[length:]
		var value []byte
		switch kind {
		case protowire.BytesType:
			value, length = protowire.ConsumeBytes(raw)
		case protowire.VarintType:
			var number uint64
			number, length = protowire.ConsumeVarint(raw)
			value = []byte{byte(min(number, 1))}
		default:
			length = protowire.ConsumeFieldValue(field, kind, raw)
		}
		if length < 0 {
			return protowire.ParseError(length)
		}
		raw = raw[length:]
		if err := visit(field, value); err != nil {
			return err
		}
	}
	return nil
}

// dialAICode 按账户代理建立到 aicode 的 TCP 连接：http 与 https 代理以 grpc-go 的 CONNECT 请求建立隧道，socks5 代理经 SOCKS5，没有代理时直连
func (c *Client) dialAICode(ctx context.Context, address string) (net.Conn, error) {
	var target *url.URL
	if c.proxy != nil {
		var err error
		if target, err = c.proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: address}}); err != nil {
			return nil, err
		}
	}
	dialer := &net.Dialer{}
	if target == nil {
		return dialer.DialContext(ctx, "tcp", address)
	}
	switch target.Scheme {
	case "socks5", "socks5h":
		via, err := proxy.FromURL(target, dialer)
		if err != nil {
			return nil, err
		}
		return via.(proxy.ContextDialer).DialContext(ctx, "tcp", address)
	case "http", "https":
	default:
		return nil, fmt.Errorf("proxy scheme %q is not supported for aicode gRPC", target.Scheme)
	}
	port := target.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[target.Scheme]
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(target.Hostname(), port))
	if err != nil {
		return nil, err
	}
	if target.Scheme == "https" {
		conn = tls.Client(conn, &tls.Config{ServerName: target.Hostname()})
	}
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Host: address}, Header: http.Header{"User-Agent": {grpcUserAgent}}}
	if user := target.User; user != nil {
		password, _ := user.Password()
		request.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password)))
	}
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
		defer conn.SetDeadline(time.Time{})
	}
	if err := request.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		conn.Close()
		return nil, err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: %s", address, response.Status)
	}
	if reader.Buffered() != 0 {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: unexpected data after response", address)
	}
	return conn, nil
}
