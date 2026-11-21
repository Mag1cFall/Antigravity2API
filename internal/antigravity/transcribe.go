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
	"google.golang.org/grpc/mem"
)

const (
	// aicodeTarget 为语音转写 gRPC 连接的目标
	aicodeTarget = "aicode.googleapis.com:443"
	// bidiMethod 为官方语音转写打开的双向流方法
	bidiMethod = "/google.gca.aicode.v1alpha.PredictionService/BidiGenerateContent"
	// transcriptionMIME 为音频块的 MIME，16 kHz 单声道 16 位小端 PCM
	transcriptionMIME = "audio/pcm;rate=16000"
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

// Transcribe 经 BidiGenerateContent 双向流按实时节奏转写 16 kHz 单声道 PCM
// 帧格式与发送节奏见 docs/protocol.md“语音转写双向流”
func (c *Client) Transcribe(ctx context.Context, model string, pcm []byte) (Transcription, error) {
	started := time.Now()
	stream, err := c.OpenLive(ctx)
	if err != nil {
		return Transcription{}, transcriptionError(ctx, err)
	}
	defer stream.Close()
	if err := stream.Send(setupMessage(model)); err != nil {
		return Transcription{}, transcriptionError(ctx, err)
	}
	var result Transcription
	var texts []string
	receive := func() (serverMessage, error) {
		raw, err := stream.Recv()
		if err != nil {
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
	if err := stream.Send(realtimeInput(5, nil)); err != nil {
		return Transcription{}, transcriptionError(ctx, err)
	}
	ticker := time.NewTicker(transcriptionInterval)
	defer ticker.Stop()
	for offset := 0; offset < len(pcm); {
		due := min(len(pcm), (int(time.Since(started)/transcriptionInterval)+1)*VoiceMicFrame)
		for ; offset < due; offset += VoiceMicFrame {
			if err := stream.Send(realtimeInput(1, audioBlob(pcm[offset:min(offset+VoiceMicFrame, len(pcm))]))); err != nil {
				return Transcription{}, transcriptionError(ctx, err)
			}
		}
		if offset < len(pcm) {
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return Transcription{}, ctx.Err()
			}
		}
	}
	if err := stream.Send(realtimeInput(6, nil)); err != nil {
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

// transcriptionError 在调用方取消时返回取消原因，否则返回带方法名的 gRPC 错误
func transcriptionError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if strings.HasPrefix(err.Error(), "aicode ") {
		return err
	}
	return fmt.Errorf("aicode BidiGenerateContent: %w", err)
}

// setupMessage 编码 setup：模型、关闭自动语音活动检测与输入转写
func setupMessage(model string) []byte {
	setup := appendString(nil, 1, model)
	setup = appendBytes(setup, 5, appendBytes(nil, 6, appendVarint(nil, 1, 1)))
	setup = appendBytes(setup, 8, nil)
	return appendBytes(nil, 1, setup)
}

// realtimeInput 编码 realtimeInput 的一个字段：1 为音频，5 为 activity start，6 为 activity end
func realtimeInput(field int, value []byte) []byte {
	return appendBytes(nil, 3, appendBytes(nil, field, value))
}

// audioBlob 编码一个音频块
func audioBlob(data []byte) []byte {
	return appendBytes(appendString(nil, 1, transcriptionMIME), 2, data)
}

// serverMessage 表示服务端消息中转写所需的字段
type serverMessage struct {
	setupComplete      bool
	inputTranscription string
}

// parseServerMessage 解码 setupComplete 与 serverContent 中的 inputTranscription
func parseServerMessage(raw []byte) (serverMessage, error) {
	var message serverMessage
	err := walkWire(raw, func(field int, value []byte, _ uint64) error {
		switch field {
		case 1:
			message.setupComplete = true
		case 2:
			return walkWire(value, func(field int, value []byte, _ uint64) error {
				if field != 6 {
					return nil
				}
				return walkWire(value, func(field int, value []byte, _ uint64) error {
					if field == 1 {
						message.inputTranscription = string(value)
					}
					return nil
				})
			})
		}
		return nil
	})
	return message, err
}

// dialAICode 按账户代理建立到 aicode 的 TCP 连接：http 与 https 代理以 User-Agent 为 agent 的 CONNECT 请求建立隧道，socks5 代理经 SOCKS5，没有代理时直连
func (c *Client) dialAICode(ctx context.Context, address, agent string) (net.Conn, error) {
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
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Host: address}, Header: http.Header{"User-Agent": {agent}}}
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
