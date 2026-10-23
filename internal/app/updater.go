package app

import (
	"bufio"
	"bytes"
	"context"
	stdtls "crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	xproxy "golang.org/x/net/proxy"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// updaterUserAgent 是官方客户端的 Electron 网络栈经 HTTP 代理建立隧道时 CONNECT 携带的 User-Agent
const updaterUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Antigravity/" + antigravity.ClientVersion + " Chrome/152.0.7977.130 Electron/44.4.3 Safari/537.36"

// updaterSettings 是官方更新器 HTTP/2 连接的 SETTINGS，顺序与取值同 Chromium 152
var updaterSettings = []http2.Setting{
	{ID: http2.SettingHeaderTableSize, Val: 65536},
	{ID: http2.SettingEnablePush, Val: 0},
	{ID: http2.SettingInitialWindowSize, Val: 6291456},
	{ID: http2.SettingMaxHeaderListSize, Val: 262144},
}

// updaterWindow 是官方更新器在 SETTINGS 之后发送的连接级 WINDOW_UPDATE 增量
const updaterWindow = 15663105

// updaterPriority 是官方更新器 HEADERS 帧的优先级：独占、依赖 0、权重 110（IDLE）
var updaterPriority = http2.PriorityParam{Exclusive: true, Weight: 109}

// updaterKeepAlive 是 Chromium 套接字的 TCP keep-alive 间隔
const updaterKeepAlive = 45 * time.Second

// updaterHello 是官方客户端 Electron 44（Chromium 152）的 ClientHello：Chrome 152 去掉 trust_anchors 扩展，有缓存的会话时末尾带 pre_shared_key
var updaterHello = tls.ClientHelloID{Client: "Electron", Version: "44", SpecFactory: func() (tls.ClientHelloSpec, error) {
	spec, err := profiles.Chrome_152_PSK.GetClientHelloSpec()
	spec.Extensions = slices.DeleteFunc(spec.Extensions, func(extension tls.TLSExtension) bool {
		generic, ok := extension.(*tls.GenericExtension)
		return ok && generic.Id == 0xca34
	})
	return spec, err
}}

// updater 以官方更新器的传输层形态发出 GET：一条 HTTP/2 连接保持到对端关闭，TLS 会话缓存跨连接保留，h3 为响应 alt-svc 通告的 HTTP/3 替代服务的到期时刻，late 为 QUIC 先就绪时仍在建立、建立后闲置到下一次读取的 TCP 连接
type updater struct {
	sessions tls.ClientSessionCache
	conn     *updaterConn
	via      string
	h3       time.Time
	late     chan updaterDial[*updaterConn]
}

// updaterConn 是一条到更新清单主机的 HTTP/2 连接
type updaterConn struct {
	raw       net.Conn
	tls       *tls.UConn
	framer    *http2.Framer
	encoder   *hpack.Encoder
	out       bytes.Buffer
	block     bytes.Buffer
	mu        sync.Mutex
	next      uint32
	active    atomic.Uint32
	away      atomic.Bool
	responses chan updaterPart
	done      chan struct{}
	err       error
}

// updaterPart 是当前流的一个响应帧
type updaterPart struct {
	status string
	altSvc string
	data   []byte
	end    bool
	err    error
}

// updaterDial 是一次 TCP 或 QUIC 拨号的结果
type updaterDial[T any] struct {
	conn T
	err  error
}

// get 经 proxy（为空时直连）以 headers 的顺序发出 GET，返回状态码与响应正文：直连且记住了 h3 替代服务时先发出 QUIC Initial 航班，再使用现有 HTTP/2 连接或开始 TCP，先就绪的连接承载请求，QUIC 连接在对端确认握手后才算就绪；落后的 QUIC 连接照常完成握手并空闲到超时，落后的 TCP 连接照常完成 TLS 握手后不发 HTTP/2 前言，闲置到下一次读取时关闭；其余情况在现有或新建的 HTTP/2 连接上发出
func (u *updater) get(ctx context.Context, target *url.URL, headers [][2]string, proxy *url.URL) (int, []byte, error) {
	via := ""
	if proxy != nil {
		via = proxy.String()
	}
	if u.late != nil {
		select {
		case late := <-u.late:
			if late.err == nil && late.conn != u.conn {
				late.conn.raw.Close()
			}
		default:
		}
		u.late = nil
	}
	if u.conn != nil && (u.conn.closed() || u.via != via) {
		u.conn.raw.Close()
		u.conn = nil
	}
	var quic chan updaterDial[*h3Conn]
	flight := make(chan struct{})
	dialCtx := ctx
	if proxy == nil && time.Now().Before(u.h3) {
		quic = make(chan updaterDial[*h3Conn], 1)
		dialCtx = context.WithoutCancel(ctx)
		go func() {
			conn, err := dialH3(dialCtx, target.Hostname(), sync.OnceFunc(func() { close(flight) }))
			if err == nil {
				err = conn.confirm()
			}
			quic <- updaterDial[*h3Conn]{conn, err}
		}()
	} else {
		close(flight)
	}
	existing := u.conn
	tcp := make(chan updaterDial[*updaterConn], 1)
	go func(dialed chan<- updaterDial[*updaterConn]) {
		<-flight
		if existing != nil {
			dialed <- updaterDial[*updaterConn]{conn: existing}
			return
		}
		conn, err := dialUpdater(dialCtx, target.Hostname(), proxy, u.sessions)
		dialed <- updaterDial[*updaterConn]{conn, err}
	}(tcp)
	var errs []error
	for tcp != nil || quic != nil {
		var status int
		var altSvc string
		var body []byte
		var err error
		select {
		case dial := <-tcp:
			tcp = nil
			if dial.err == nil && dial.conn != existing {
				dial.err = dial.conn.start()
			}
			if dial.err != nil {
				errs = append(errs, dial.err)
				continue
			}
			u.conn, u.via = dial.conn, via
			status, altSvc, body, err = dial.conn.get(ctx, target, headers)
		case dial := <-quic:
			quic = nil
			if dial.err != nil {
				errs = append(errs, dial.err)
				continue
			}
			u.late, tcp = tcp, nil
			status, altSvc, body, err = dial.conn.get(ctx, target, headers)
		case <-ctx.Done():
			u.late = tcp
			return 0, nil, ctx.Err()
		}
		if altSvc != "" {
			u.h3 = altSvcExpiry(altSvc, time.Now())
		}
		return status, body, err
	}
	return 0, nil, errors.Join(errs...)
}

// altSvcExpiry 返回 alt-svc 头中同端口 h3 替代服务的到期时刻：ma 缺省为 24 小时，没有该项（含 clear）时为零值
func altSvcExpiry(value string, now time.Time) time.Time {
	for entry := range strings.SplitSeq(value, ",") {
		params := strings.Split(entry, ";")
		if strings.TrimSpace(params[0]) != `h3=":443"` {
			continue
		}
		age := 86400
		for _, param := range params[1:] {
			if v, ok := strings.CutPrefix(strings.TrimSpace(param), "ma="); ok {
				age, _ = strconv.Atoi(v)
			}
		}
		return now.Add(time.Duration(age) * time.Second)
	}
	return time.Time{}
}

// dialUpdater 直连或经代理建立到 host:443 的 TCP 连接并以 Electron 的 ClientHello 完成 TLS 握手
func dialUpdater(ctx context.Context, host string, proxy *url.URL, sessions tls.ClientSessionCache) (*updaterConn, error) {
	address := net.JoinHostPort(host, "443")
	dialer := &net.Dialer{KeepAlive: updaterKeepAlive}
	var raw net.Conn
	var err error
	switch {
	case proxy == nil:
		raw, err = dialer.DialContext(ctx, "tcp", address)
	case proxy.Scheme == "socks5" || proxy.Scheme == "socks5h":
		var socks xproxy.Dialer
		if socks, err = xproxy.FromURL(proxy, dialer); err == nil {
			raw, err = socks.(xproxy.ContextDialer).DialContext(ctx, "tcp", address)
		}
	default:
		raw, err = tunnel(ctx, dialer, proxy, address)
	}
	if err != nil {
		return nil, err
	}
	conn := tls.UClient(raw, &tls.Config{ServerName: host, ClientSessionCache: sessions, OmitEmptyPsk: true}, updaterHello, true, false, false)
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	if protocol := conn.ConnectionState().NegotiatedProtocol; protocol != "h2" {
		raw.Close()
		return nil, fmt.Errorf("更新清单主机协商的协议为 %q", protocol)
	}
	c := &updaterConn{raw: raw, tls: conn, next: 1, responses: make(chan updaterPart, 16), done: make(chan struct{})}
	c.framer = http2.NewFramer(&c.out, conn)
	c.framer.ReadMetaHeaders = hpack.NewDecoder(updaterSettings[0].Val, nil)
	c.encoder = hpack.NewEncoder(&c.block)
	return c, nil
}

// start 发出 HTTP/2 前言、SETTINGS 与 WINDOW_UPDATE 并开始读取对端帧
func (c *updaterConn) start() error {
	err := c.write(func() error {
		c.out.WriteString(http2.ClientPreface)
		if err := c.framer.WriteSettings(updaterSettings...); err != nil {
			return err
		}
		return c.framer.WriteWindowUpdate(0, updaterWindow)
	})
	if err != nil {
		c.raw.Close()
		return err
	}
	go c.read()
	return nil
}

// tunnel 经 HTTP 或 HTTPS 代理以 Chromium 的 CONNECT 请求头建立到 address 的隧道，ctx 结束时中断进行中的读写
func tunnel(ctx context.Context, dialer *net.Dialer, proxy *url.URL, address string) (net.Conn, error) {
	port := proxy.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[proxy.Scheme]
	}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(proxy.Hostname(), port))
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	if proxy.Scheme == "https" {
		secure := stdtls.Client(conn, &stdtls.Config{ServerName: proxy.Hostname()})
		if err := secure.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		conn = secure
	}
	request := "CONNECT " + address + " HTTP/1.1\r\nHost: " + address + "\r\nProxy-Connection: keep-alive\r\nUser-Agent: " + updaterUserAgent + "\r\n"
	if proxy.User != nil {
		password, _ := proxy.User.Password()
		request += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+password)) + "\r\n"
	}
	if _, err := io.WriteString(conn, request+"\r\n"); err != nil {
		conn.Close()
		return nil, err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("代理拒绝 CONNECT：%s", response.Status)
	}
	return conn, nil
}

// closed 判断连接已关闭或收到 GOAWAY
func (c *updaterConn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return c.away.Load()
	}
}

// write 把 frames 写出的帧合成一次写入，使它们落在同一个 TLS 记录中
func (c *updaterConn) write(frames func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out.Reset()
	if err := frames(); err != nil {
		return err
	}
	_, err := c.tls.Write(c.out.Bytes())
	return err
}

// read 读取对端帧直到连接关闭：回应 SETTINGS 与 PING，把当前流的响应交给 responses；空闲时收到 GOAWAY 即关闭套接字
func (c *updaterConn) read() {
	defer close(c.done)
	defer c.raw.Close()
	for {
		frame, err := c.framer.ReadFrame()
		if err != nil {
			c.err = err
			return
		}
		current := frame.Header().StreamID == c.active.Load()
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			if !frame.IsAck() {
				err = c.write(c.framer.WriteSettingsAck)
			}
		case *http2.PingFrame:
			if !frame.IsAck() {
				err = c.write(func() error { return c.framer.WritePing(true, frame.Data) })
			}
		case *http2.GoAwayFrame:
			c.away.Store(true)
			if c.active.Load() == 0 {
				c.err = fmt.Errorf("对端发送 GOAWAY：%v", frame.ErrCode)
				return
			}
		case *http2.MetaHeadersFrame:
			if current {
				part := updaterPart{status: frame.PseudoValue("status"), end: frame.StreamEnded()}
				for _, field := range frame.RegularFields() {
					if field.Name == "alt-svc" {
						part.altSvc = field.Value
					}
				}
				c.responses <- part
			}
		case *http2.DataFrame:
			if current {
				c.responses <- updaterPart{data: bytes.Clone(frame.Data()), end: frame.StreamEnded()}
			}
		case *http2.RSTStreamFrame:
			if current {
				c.responses <- updaterPart{err: http2.StreamError{StreamID: frame.StreamID, Code: frame.ErrCode}, end: true}
			}
		}
		if err != nil {
			c.err = err
			return
		}
	}
}

// get 在新流上发出带 IDLE 优先级的 HEADERS 并等待响应结束，返回状态码、alt-svc 与正文；头块按 Chromium 的 HPACK 规则编码
func (c *updaterConn) get(ctx context.Context, target *url.URL, headers [][2]string) (int, string, []byte, error) {
	stream := c.next
	c.next += 2
	c.active.Store(stream)
	defer c.active.Store(0)
	err := c.write(func() error {
		c.block.Reset()
		c.encoder.WriteField(hpack.HeaderField{Name: ":method", Value: http.MethodGet})
		c.encoder.WriteField(hpack.HeaderField{Name: ":authority", Value: target.Host})
		c.encoder.WriteField(hpack.HeaderField{Name: ":scheme", Value: "https"})
		c.block.Write(pathLiteral(target.RequestURI()))
		for _, header := range headers {
			c.encoder.WriteField(hpack.HeaderField{Name: header[0], Value: header[1]})
		}
		return c.framer.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, BlockFragment: c.block.Bytes(), EndStream: true, EndHeaders: true, Priority: updaterPriority})
	})
	if err != nil {
		c.raw.Close()
		return 0, "", nil, err
	}
	status, altSvc, body := 0, "", []byte(nil)
	for {
		select {
		case part := <-c.responses:
			if part.err != nil {
				return 0, "", nil, part.err
			}
			if part.status != "" {
				status, _ = strconv.Atoi(part.status)
				altSvc = part.altSvc
			}
			body = append(body, part.data...)
			if part.end {
				if c.away.Load() {
					c.raw.Close()
				}
				return status, altSvc, body, nil
			}
		case <-c.done:
			return 0, "", nil, c.err
		case <-ctx.Done():
			c.raw.Close()
			return 0, "", nil, ctx.Err()
		}
	}
}

// pathLiteral 按 Chromium 的 HPACK 规则编码 :path：不索引的字面量，名称取静态表第 4 项，值在 Huffman 编码更短时用 Huffman
func pathLiteral(path string) []byte {
	return appendString([]byte{0x04}, 7, 0x00, path)
}
