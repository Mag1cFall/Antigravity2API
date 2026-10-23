package app

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"

	quic "github.com/refraction-networking/uquic"
	"github.com/refraction-networking/uquic/qlog"
	"github.com/refraction-networking/uquic/qlogwriter"
	"github.com/refraction-networking/uquic/quicvarint"
	qtls "github.com/refraction-networking/utls"
	"github.com/sardanioss/qpack"
	"golang.org/x/net/http2/hpack"
)

// h3Conn 是一条到更新清单主机的 HTTP/3 连接：握手完成即发控制流，QPACK 编码器与解码器流首次使用时才写入流类型；confirmed 在收到 HANDSHAKE_DONE 时关闭
type h3Conn struct {
	conn      *quic.Conn
	control   *quic.SendStream
	decoder   *quic.SendStream
	encoder   *quic.SendStream
	qpack     *qpack.Decoder
	settings  chan struct{}
	confirmed chan struct{}
	capacity  uint64
	acked     bool
}

// dialH3 以 Chromium 的 Initial 向 host:443 发起 QUIC 握手，Initial 航班发出或拨号结束时调用 sent；握手完成后依次打开控制、QPACK 解码器、QPACK 编码器单向流并发出控制流，连接空闲超时后关闭并释放 UDP 套接字
func dialH3(ctx context.Context, host string, sent func()) (*h3Conn, error) {
	defer sent()
	address, err := net.ResolveUDPAddr("udp", net.JoinHostPort(host, "443"))
	if err != nil {
		return nil, err
	}
	socket, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	transport := &quic.Transport{Conn: socket}
	dialer := &quic.UTransport{Transport: transport, QUICSpec: quicSpec()}
	confirmed := make(chan struct{})
	trace := &flightTrace{sent: sent, confirmed: sync.OnceFunc(func() { close(confirmed) })}
	config := quicConfig.Clone()
	config.Tracer = func(context.Context, bool, quic.ConnectionID) qlogwriter.Trace { return trace }
	conn, err := dialer.Dial(ctx, address, &qtls.Config{ServerName: host, NextProtos: []string{"h3"}}, config)
	if err != nil {
		transport.Close()
		return nil, err
	}
	context.AfterFunc(conn.Context(), func() { transport.Close() })
	c := &h3Conn{conn: conn, qpack: qpack.NewDecoderWithCapacity(65536), settings: make(chan struct{}), confirmed: confirmed}
	if c.control, err = conn.OpenUniStream(); err == nil {
		if c.decoder, err = conn.OpenUniStream(); err == nil {
			c.encoder, err = conn.OpenUniStream()
		}
	}
	if err == nil {
		_, err = c.control.Write(controlPreface())
	}
	if err != nil {
		conn.CloseWithError(0, "")
		return nil, err
	}
	go c.accept()
	return c, nil
}

// flightTrace 是 QUIC 连接的 qlog 记录器：记到第二个发出的包，即 Initial 航班的最后一个数据报时调用 sent，收到 HANDSHAKE_DONE 时调用 confirmed
type flightTrace struct {
	packets   atomic.Int32
	sent      func()
	confirmed func()
}

// AddProducer 返回自身
func (t *flightTrace) AddProducer() qlogwriter.Recorder {
	return t
}

// SupportsSchemas 不声明任何 qlog 模式
func (t *flightTrace) SupportsSchemas(string) bool {
	return false
}

// RecordEvent 在第二个 PacketSent 时调用 sent，在带 HANDSHAKE_DONE 的 PacketReceived 时调用 confirmed
func (t *flightTrace) RecordEvent(event qlogwriter.Event) {
	switch event := event.(type) {
	case qlog.PacketSent:
		if t.packets.Add(1) == 2 {
			t.sent()
		}
	case qlog.PacketReceived:
		for _, frame := range event.Frames {
			if _, ok := frame.Frame.(*qlog.HandshakeDoneFrame); ok {
				t.confirmed()
			}
		}
	}
}

// Close 无需释放资源
func (t *flightTrace) Close() error {
	return nil
}

// controlPreface 是 quiche QuicSendControlStream 的首次写入：流类型、按标识符升序的 SETTINGS（含一个 GREASE 设置）与一个 GREASE 帧
func controlPreface() []byte {
	settings := [][2]uint64{{0x01, 65536}, {0x06, 262144}, {0x07, 100}, {0x33, 1}, {0x1f*uint64(rand.Uint32()) + 0x21, uint64(rand.Uint32())}}
	slices.SortFunc(settings, func(a, b [2]uint64) int { return cmp.Compare(a[0], b[0]) })
	var payload []byte
	for _, setting := range settings {
		payload = quicvarint.Append(quicvarint.Append(payload, setting[0]), setting[1])
	}
	seed := rand.Uint32()
	grease := make([]byte, seed%4)
	for i := range grease {
		grease[i] = byte(rand.Uint32())
	}
	return appendFrame(appendFrame([]byte{0x00}, 0x04, payload), 0x1f*uint64(seed)+0x21, grease)
}

// appendFrame 追加一个 HTTP/3 帧
func appendFrame(dst []byte, kind uint64, payload []byte) []byte {
	return append(quicvarint.Append(quicvarint.Append(dst, kind), uint64(len(payload))), payload...)
}

// accept 读取对端单向流：控制流的 SETTINGS 给出 QPACK 动态表容量，QPACK 编码器流的指令写入解码动态表，其余流读完丢弃
func (c *h3Conn) accept() {
	for {
		stream, err := c.conn.AcceptUniStream(context.Background())
		if err != nil {
			return
		}
		go func() {
			r := bufio.NewReader(stream)
			kind, err := quicvarint.Read(r)
			switch {
			case err != nil:
			case kind == 0x00:
				c.readSettings(r)
			case kind == 0x02:
				for {
					instruction, err := encoderInstruction(r)
					if err != nil || c.qpack.ProcessEncoderInstructions(instruction) != nil {
						return
					}
				}
			}
			io.Copy(io.Discard, r)
		}()
	}
}

// readSettings 读取对端控制流的第一个帧（SETTINGS），记下 QPACK_MAX_TABLE_CAPACITY
func (c *h3Conn) readSettings(r *bufio.Reader) {
	kind, err := quicvarint.Read(r)
	if err != nil || kind != 0x04 {
		return
	}
	length, err := quicvarint.Read(r)
	if err != nil {
		return
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return
	}
	settings := bytes.NewReader(payload)
	for settings.Len() > 0 {
		id, _ := quicvarint.Read(settings)
		value, _ := quicvarint.Read(settings)
		if id == 0x01 {
			c.capacity = value
		}
	}
	close(c.settings)
}

// encoderInstruction 读出 QPACK 编码器流上的一条完整指令
func encoderInstruction(r *bufio.Reader) ([]byte, error) {
	first, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	instruction := []byte{first}
	readInt := func(prefix uint8) (uint64, error) {
		value := uint64(instruction[len(instruction)-1] & (1<<prefix - 1))
		if value < 1<<prefix-1 {
			return value, nil
		}
		for shift := 0; ; shift += 7 {
			b, err := r.ReadByte()
			if err != nil {
				return 0, err
			}
			instruction = append(instruction, b)
			value += uint64(b&0x7f) << shift
			if b&0x80 == 0 {
				return value, nil
			}
		}
	}
	readString := func(prefix uint8) error {
		length, err := readInt(prefix)
		if err != nil {
			return err
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		instruction = append(instruction, data...)
		return nil
	}
	nextString := func() error {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		instruction = append(instruction, b)
		return readString(7)
	}
	switch {
	case first&0x80 != 0:
		if _, err = readInt(6); err == nil {
			err = nextString()
		}
	case first&0x40 != 0:
		if err = readString(5); err == nil {
			err = nextString()
		}
	default:
		_, err = readInt(5)
	}
	return instruction, err
}

// confirm 等待对端以 HANDSHAKE_DONE 确认握手，连接先结束时返回结束原因
func (c *h3Conn) confirm() error {
	select {
	case <-c.confirmed:
		return nil
	case <-c.conn.Context().Done():
		return context.Cause(c.conn.Context())
	}
}

// get 以 Chromium 的 QPACK 编码发出 GET 并读取响应：控制流先发 PRIORITY_UPDATE，编码器流插入全部非静态表精确匹配的字段，HEADERS 只引用表项；返回状态码、alt-svc 与正文
func (c *h3Conn) get(ctx context.Context, target *url.URL, headers [][2]string) (int, string, []byte, error) {
	select {
	case <-c.settings:
	case <-ctx.Done():
		return 0, "", nil, ctx.Err()
	}
	stream, err := c.conn.OpenStreamSync(ctx)
	if err != nil {
		return 0, "", nil, err
	}
	stop := context.AfterFunc(ctx, func() { stream.CancelRead(0x10c); stream.CancelWrite(0x10c) })
	defer stop()
	priority := quicvarint.Append(nil, uint64(stream.StreamID()))
	for _, header := range headers {
		if header[0] == "priority" {
			priority = append(priority, header[1]...)
		}
	}
	instructions, block := c.encode(append([][2]string{{":method", "GET"}, {":authority", target.Host}, {":scheme", "https"}, {":path", target.RequestURI()}}, headers...))
	if _, err := c.control.Write(appendFrame(nil, 0xf0700, priority)); err != nil {
		return 0, "", nil, err
	}
	if len(instructions) > 0 {
		if _, err := c.encoder.Write(instructions); err != nil {
			return 0, "", nil, err
		}
	}
	if _, err := stream.Write(appendFrame(nil, 0x01, block)); err != nil {
		return 0, "", nil, err
	}
	stream.Close()
	r := bufio.NewReader(stream)
	status, altSvc, body := 0, "", []byte(nil)
	for {
		kind, err := quicvarint.Read(r)
		if errors.Is(err, io.EOF) {
			return status, altSvc, body, nil
		}
		if err != nil {
			return 0, "", nil, err
		}
		length, err := quicvarint.Read(r)
		if err != nil {
			return 0, "", nil, err
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, "", nil, err
		}
		switch kind {
		case 0x00:
			body = append(body, payload...)
		case 0x01:
			decode := c.qpack.Decode(payload)
			for {
				field, err := decode()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return 0, "", nil, err
				}
				switch field.Name {
				case ":status":
					status, _ = strconv.Atoi(field.Value)
				case "alt-svc":
					altSvc = field.Value
				}
			}
			if payload[0] != 0 {
				if err := c.acknowledge(stream.StreamID()); err != nil {
					return 0, "", nil, err
				}
			}
		}
	}
}

// acknowledge 在 QPACK 解码器流上确认引用了动态表的头块，首次写入带流类型
func (c *h3Conn) acknowledge(id quic.StreamID) error {
	data := appendInt(nil, 7, 0x80, uint64(id))
	if !c.acked {
		data = append([]byte{0x03}, data...)
		c.acked = true
	}
	_, err := c.decoder.Write(data)
	return err
}

// encode 按 quiche QpackEncoder 在空动态表上的规则编码 fields：静态表精确匹配用静态索引，其余字段以静态表名称引用或字面量名称插入动态表，头块以相对索引引用；对端不允许动态表时全部用字面量
func (c *h3Conn) encode(fields [][2]string) ([]byte, []byte) {
	capacity := min(c.capacity, 65536)
	var instructions []byte
	if capacity > 0 {
		instructions = appendInt([]byte{0x02}, 5, 0x20, capacity)
	}
	entries := make([]int, len(fields))
	inserted := 0
	for i, field := range fields {
		exact, name := staticIndex(field[0], field[1])
		entries[i] = -1
		switch {
		case exact >= 0 || capacity == 0:
			continue
		case name >= 0:
			instructions = appendString(appendInt(instructions, 6, 0xc0, uint64(name)), 7, 0x00, field[1])
		default:
			instructions = appendString(appendString(instructions, 5, 0x40, field[0]), 7, 0x00, field[1])
		}
		entries[i] = inserted
		inserted++
	}
	block := []byte{0x00, 0x00}
	if inserted > 0 {
		block = appendInt(appendInt(nil, 8, 0x00, uint64(inserted)%(2*(c.capacity/32))+1), 7, 0x00, 0)
	}
	for i, field := range fields {
		exact, name := staticIndex(field[0], field[1])
		switch {
		case entries[i] >= 0:
			block = appendInt(block, 6, 0x80, uint64(inserted-1-entries[i]))
		case exact >= 0:
			block = appendInt(block, 6, 0xc0, uint64(exact))
		case name >= 0:
			block = appendString(appendInt(block, 4, 0x50, uint64(name)), 7, 0x00, field[1])
		default:
			block = appendString(appendString(block, 3, 0x20, field[0]), 7, 0x00, field[1])
		}
	}
	return instructions, block
}

// appendInt 追加 HPACK/QPACK 前缀整数，flags 为前缀之前的高位
func appendInt(dst []byte, prefix uint8, flags byte, value uint64) []byte {
	limit := uint64(1)<<prefix - 1
	if value < limit {
		return append(dst, flags|byte(value))
	}
	dst = append(dst, flags|byte(limit))
	for value -= limit; value >= 0x80; value >>= 7 {
		dst = append(dst, byte(value)|0x80)
	}
	return append(dst, byte(value))
}

// appendString 追加 HPACK/QPACK 字符串：Huffman 编码更短时用 Huffman，H 位紧挨长度前缀
func appendString(dst []byte, prefix uint8, flags byte, s string) []byte {
	if length := hpack.HuffmanEncodeLength(s); length < uint64(len(s)) {
		return hpack.AppendHuffmanString(appendInt(dst, prefix, flags|1<<prefix, length), s)
	}
	return append(appendInt(dst, prefix, flags, uint64(len(s))), s...)
}

// staticIndex 返回 name、value 在 QPACK 静态表中的精确匹配索引与第一个同名索引，没有时为 -1
func staticIndex(name, value string) (int, int) {
	exact, named := -1, -1
	for i, entry := range qpackStatic {
		if entry[0] != name {
			continue
		}
		if named < 0 {
			named = i
		}
		if entry[1] == value {
			exact = i
			break
		}
	}
	return exact, named
}

// qpackStatic 是 RFC 9204 附录 A 的 QPACK 静态表
var qpackStatic = [...][2]string{
	{":authority", ""},
	{":path", "/"},
	{"age", "0"},
	{"content-disposition", ""},
	{"content-length", "0"},
	{"cookie", ""},
	{"date", ""},
	{"etag", ""},
	{"if-modified-since", ""},
	{"if-none-match", ""},
	{"last-modified", ""},
	{"link", ""},
	{"location", ""},
	{"referer", ""},
	{"set-cookie", ""},
	{":method", "CONNECT"},
	{":method", "DELETE"},
	{":method", "GET"},
	{":method", "HEAD"},
	{":method", "OPTIONS"},
	{":method", "POST"},
	{":method", "PUT"},
	{":scheme", "http"},
	{":scheme", "https"},
	{":status", "103"},
	{":status", "200"},
	{":status", "304"},
	{":status", "404"},
	{":status", "503"},
	{"accept", "*/*"},
	{"accept", "application/dns-message"},
	{"accept-encoding", "gzip, deflate, br"},
	{"accept-ranges", "bytes"},
	{"access-control-allow-headers", "cache-control"},
	{"access-control-allow-headers", "content-type"},
	{"access-control-allow-origin", "*"},
	{"cache-control", "max-age=0"},
	{"cache-control", "max-age=2592000"},
	{"cache-control", "max-age=604800"},
	{"cache-control", "no-cache"},
	{"cache-control", "no-store"},
	{"cache-control", "public, max-age=31536000"},
	{"content-encoding", "br"},
	{"content-encoding", "gzip"},
	{"content-type", "application/dns-message"},
	{"content-type", "application/javascript"},
	{"content-type", "application/json"},
	{"content-type", "application/x-www-form-urlencoded"},
	{"content-type", "image/gif"},
	{"content-type", "image/jpeg"},
	{"content-type", "image/png"},
	{"content-type", "text/css"},
	{"content-type", "text/html; charset=utf-8"},
	{"content-type", "text/plain"},
	{"content-type", "text/plain;charset=utf-8"},
	{"range", "bytes=0-"},
	{"strict-transport-security", "max-age=31536000"},
	{"strict-transport-security", "max-age=31536000; includesubdomains"},
	{"strict-transport-security", "max-age=31536000; includesubdomains; preload"},
	{"vary", "accept-encoding"},
	{"vary", "origin"},
	{"x-content-type-options", "nosniff"},
	{"x-xss-protection", "1; mode=block"},
	{":status", "100"},
	{":status", "204"},
	{":status", "206"},
	{":status", "302"},
	{":status", "400"},
	{":status", "403"},
	{":status", "421"},
	{":status", "425"},
	{":status", "500"},
	{"accept-language", ""},
	{"access-control-allow-credentials", "FALSE"},
	{"access-control-allow-credentials", "TRUE"},
	{"access-control-allow-headers", "*"},
	{"access-control-allow-methods", "get"},
	{"access-control-allow-methods", "get, post, options"},
	{"access-control-allow-methods", "options"},
	{"access-control-expose-headers", "content-length"},
	{"access-control-request-headers", "content-type"},
	{"access-control-request-method", "get"},
	{"access-control-request-method", "post"},
	{"alt-svc", "clear"},
	{"authorization", ""},
	{"content-security-policy", "script-src 'none'; object-src 'none'; base-uri 'none'"},
	{"early-data", "1"},
	{"expect-ct", ""},
	{"forwarded", ""},
	{"if-range", ""},
	{"origin", ""},
	{"purpose", "prefetch"},
	{"server", ""},
	{"timing-allow-origin", "*"},
	{"upgrade-insecure-requests", "1"},
	{"user-agent", ""},
	{"x-forwarded-for", ""},
	{"x-frame-options", "deny"},
	{"x-frame-options", "sameorigin"},
}
