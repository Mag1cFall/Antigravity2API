package antigravity

import (
	"bytes"
	"compress/gzip"
	"context"
	crand "crypto/rand"
	_ "embed"
	"encoding/hex"
	"io"
	"math/rand/v2"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// preloginErrorOffset 为登录前错误上报相对虚拟进程启动的时刻，官方新安装现场为 5.6–6.6 s
const preloginErrorOffset = 5600 * time.Millisecond

//go:embed prelogin_windows_src327.bin.gz
var preloginWindows327 []byte

//go:embed prelogin_windows_src2747.bin.gz
var preloginWindows2747 []byte

//go:embed prelogin_linux_src327.bin.gz
var preloginLinux327 []byte

//go:embed prelogin_linux_src2747.bin.gz
var preloginLinux2747 []byte

// preloginTemplate 为一个系统的登录前错误上报模板：日志源 327 的结构化帧批次与 2747 的事件批次，install 为模板中的 installation_id
type preloginTemplate struct {
	src327   []byte
	src2747  []byte
	install  []byte
	sent327  uint64
	sent2747 uint64
}

// preloginTemplates 为各系统的登录前错误上报模板，登录只在本机系统上发起，故只含 windows 与 linux
var preloginTemplates = map[string]preloginTemplate{
	"windows": loadPreloginTemplate(preloginWindows327, preloginWindows2747),
	"linux":   loadPreloginTemplate(preloginLinux327, preloginLinux2747),
}

// gunzip 解压一段 gzip 字节
func gunzip(data []byte) []byte {
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		panic(err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		panic(err)
	}
	return out
}

// loadPreloginTemplate 解压两个批次并取出模板中的 installation_id 与上报时刻
func loadPreloginTemplate(gz327, gz2747 []byte) preloginTemplate {
	src327, src2747 := gunzip(gz327), gunzip(gz2747)
	t := preloginTemplate{src327: src327, src2747: src2747}
	for _, node := range pbParse(src327) {
		if node.num == 4 && node.wire == 0 {
			t.sent327 = node.u
		}
		if node.num == 3 && node.wire == 2 {
			t.install = preloginInstall(node.raw)
		}
	}
	for _, node := range pbParse(src2747) {
		if node.num == 4 && node.wire == 0 {
			t.sent2747 = node.u
		}
	}
	return t
}

// preloginInstall 从日志源 327 的一条事件中取出 ErrorTraceMetadata 里的 installation_id
func preloginInstall(event []byte) []byte {
	for _, ext := range pbParse(event) {
		if ext.num != 6 {
			continue
		}
		for _, meta := range pbParse(ext.raw) {
			if meta.num != 18 {
				continue
			}
			for _, inner := range pbParse(meta.raw) {
				if inner.num != 2 {
					continue
				}
				for _, field := range pbParse(inner.raw) {
					if field.num == 5 && field.wire == 2 {
						return field.raw
					}
				}
			}
		}
	}
	return nil
}

// hexAddress 匹配 goroutine 栈文本中的十六进制地址
var hexAddress = regexp.MustCompile(`0x[0-9a-f]+`)

// digits 判断字节是否全为 ASCII 数字
func digits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isHex32 判断字节是否为 32 个小写十六进制字符
func isHex32(b []byte) bool {
	if len(b) != 32 {
		return false
	}
	for _, c := range b {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// randomHex32 返回 16 字节随机数的十六进制
func randomHex32() []byte {
	var buf [16]byte
	crand.Read(buf[:])
	out := make([]byte, 32)
	hex.Encode(out, buf[:])
	return out
}

// shiftTime 把绝对毫秒时刻加 deltaMs、绝对微秒时刻加 deltaMs×1000，其他值不变
func shiftTime(v uint64, deltaMs int64) uint64 {
	switch {
	case v >= 1e12 && v < 1e13:
		return uint64(int64(v) + deltaMs)
	case v >= 1e15 && v < 1e16:
		return uint64(int64(v) + deltaMs*1000)
	}
	return v
}

// rewritePrelogin 以设备 installation_id、当前时刻与随机标识改写一个登录前批次：时刻整体平移、模板 installation_id 换设备 installation_id、界面会话标识换新、错误追踪标识换新、goroutine 栈地址按本进程固定偏移平移
func rewritePrelogin(template, templateInstall, deviceInstall []byte, deltaMs int64, browser []byte, imgDelta, heapDelta uint64) []byte {
	return pbWalk(template, func(n *pbNode) {
		switch {
		case n.wire == 0:
			n.u = shiftTime(n.u, deltaMs)
		case n.wire != 2 || len(n.raw) == 0:
		case bytes.Equal(n.raw, templateInstall):
			n.raw = deviceInstall
		case bytes.HasPrefix(n.raw, []byte("goroutine ")):
			n.raw = shiftAddresses(n.raw, imgDelta, heapDelta)
		case digits(n.raw):
			if v, err := strconv.ParseUint(string(n.raw), 10, 64); err == nil {
				if s := shiftTime(v, deltaMs); s != v {
					n.raw = []byte(strconv.FormatUint(s, 10))
				}
			}
		case isHex32(n.raw) && !pbRoundTrips(n.raw):
			n.raw = randomHex32()
		case isUUID(n.raw) && !pbRoundTrips(n.raw):
			n.raw = browser
		}
	})
}

// isUUID 判断字节是否为 8-4-4-4-12 的 uuid 文本
func isUUID(b []byte) bool {
	if len(b) != 36 {
		return false
	}
	for i, c := range b {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// shiftAddresses 把 goroutine 栈文本中的映像地址与堆地址分别加本进程固定偏移，低于 0x400000000000 视为堆地址
func shiftAddresses(text []byte, imgDelta, heapDelta uint64) []byte {
	return hexAddress.ReplaceAllFunc(text, func(match []byte) []byte {
		v, err := strconv.ParseUint(string(match[2:]), 16, 64)
		if err != nil {
			return match
		}
		delta := heapDelta
		if v >= 0x400000000000 {
			delta = imgDelta
		}
		return []byte("0x" + strconv.FormatUint((v+delta)&0xffffffffffff, 16))
	})
}

// Prelogin 代登录开始时生成的设备在拿到 token 前发出官方 language_server 的登录前请求：unleash 与一次不带 Bearer 的 clearcut 错误上报
type Prelogin struct {
	client    *Client
	companion *Companion
}

// NewPrelogin 以登录开始的设备档案创建登录前虚拟进程，日志中的账户记为 login
func NewPrelogin(device Device, proxy Proxy) *Prelogin {
	client := NewClient("login", staticToken{}, device, proxy)
	client.SetHub(HubState{}, nil)
	return &Prelogin{client: client, companion: NewCompanion(client)}
}

// Device 返回登录前虚拟进程的设备档案，拿到邮箱后由调用方补齐账户再交给账户池
func (p *Prelogin) Device() Device {
	return p.client.device
}

// Run 立即开始 unleash，并在启动后约 5.6 s 用设备 installation_id 发出一次登录前错误上报；ctx 结束时返回
func (p *Prelogin) Run(ctx context.Context) {
	go p.companion.Unleash(ctx)
	if _, ok := preloginTemplates[p.client.device.OS]; !ok {
		<-ctx.Done()
		return
	}
	select {
	case <-time.After(preloginErrorOffset + rand.N(time.Second)):
	case <-ctx.Done():
		return
	}
	p.reportErrors(ctx)
	<-ctx.Done()
}

// reportErrors 发出登录前的两个 clearcut 批次：日志源 327 的结构化帧与 2747 的事件，均不带 Bearer
func (p *Prelogin) reportErrors(ctx context.Context) {
	template := preloginTemplates[p.client.device.OS]
	deviceInstall := []byte(p.client.device.InstallationID)
	browser := []byte(uuid.NewString())
	imgDelta := uint64(rand.N(0x1000)) * 0x10000
	heapDelta := uint64(rand.N(0x40)) * 0x4000000
	now := time.Now().UnixMilli()
	for _, batch := range []struct {
		data []byte
		sent uint64
	}{{template.src327, template.sent327}, {template.src2747, template.sent2747}} {
		body := rewritePrelogin(batch.data, template.install, deviceInstall, now-int64(batch.sent), browser, imgDelta, heapDelta)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointClearcut, bytes.NewReader(body))
		if err != nil {
			continue
		}
		request.Header.Set("Content-Type", "application/octet-stream")
		p.client.discard(p.client.web, request)
	}
}

// staticToken 是登录前虚拟进程用的空 token 源，unleash 与不带 Bearer 的 clearcut 不读取它
type staticToken struct{}

// Token 返回空 token
func (staticToken) Token() (*oauth2.Token, error) { return &oauth2.Token{}, nil }
