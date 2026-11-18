package antigravity

import (
	"bytes"
	"compress/gzip"
	"context"
	crand "crypto/rand"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// preloginErrorOffset 为登录前第一个 clearcut 批次相对虚拟进程启动的最早时刻，官方新安装现场为 5.1–5.7 s
const preloginErrorOffset = 5100 * time.Millisecond

// preloginFiles 为各系统登录前 clearcut 批次的正文模板，文件名为 prelogin_<系统>_<序号>.bin.gz，序号为现场中的发出顺序
//
//go:embed prelogin_*.bin.gz
var preloginFiles embed.FS

// preloginTemplate 为一个系统的登录前 clearcut 模板：install 为模板中的 installation_id，batches 为按发出顺序排列的批次
type preloginTemplate struct {
	install []byte
	batches []preloginBatch
}

// preloginBatch 为一个登录前批次的正文模板与其中的上报时刻
type preloginBatch struct {
	data []byte
	sent uint64
}

// preloginTemplates 为各系统的登录前模板，登录只在本机系统上发起，故只含 windows 与 linux
var preloginTemplates = map[string]preloginTemplate{
	"windows": loadPreloginTemplate("windows"),
	"linux":   loadPreloginTemplate("linux"),
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

// loadPreloginTemplate 解压一个系统的各批次，取出上报时刻与 installation_id：有日志源 327 的批次时取其事件中的，否则取第一个带 installation_id 参数的 Hub 事件中的
func loadPreloginTemplate(system string) preloginTemplate {
	var t preloginTemplate
	names, _ := fs.Glob(preloginFiles, "prelogin_"+system+"_*.bin.gz")
	slices.Sort(names)
	for _, name := range names {
		gz, _ := preloginFiles.ReadFile(name)
		data := gunzip(gz)
		batch := preloginBatch{data: data}
		var source uint64
		for _, node := range pbParse(data) {
			switch {
			case node.num == 2 && node.wire == 0:
				source = node.u
			case node.num == 4 && node.wire == 0:
				batch.sent = node.u
			case node.num == 3 && node.wire == 2 && source == 327 && t.install == nil:
				t.install = preloginInstall(node.raw)
			case node.num == 3 && node.wire == 2 && t.install == nil:
				t.install = paramInstall(node.raw)
			}
		}
		t.batches = append(t.batches, batch)
	}
	return t
}

// paramInstall 从 Hub 事件的 installation_id 参数中取出 installation_id，没有该参数时为 nil
func paramInstall(event []byte) []byte {
	key := []byte("installation_id$")
	index := bytes.Index(event, key)
	if index < 0 {
		return nil
	}
	return event[index+len(key) : index+len(key)+36]
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

// rewritePrelogin 以设备 installation_id、当前时刻与随机标识改写一个登录前批次：时刻整体平移、模板 installation_id 换设备 installation_id、界面会话标识换新、错误追踪标识换新
func rewritePrelogin(template, templateInstall, deviceInstall []byte, deltaMs int64, browser []byte) []byte {
	return pbWalk(template, func(n *pbNode) {
		switch {
		case n.wire == 0:
			n.u = shiftTime(n.u, deltaMs)
		case n.wire != 2 || len(n.raw) == 0:
		case bytes.Equal(n.raw, templateInstall):
			n.raw = deviceInstall
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

// Prelogin 代登录开始时生成的设备在拿到 token 前发出官方 language_server 的登录前请求：按现场顺序与间隔发出的不带 Bearer 的 clearcut 批次；browser 为本进程改写模板用的界面会话标识
type Prelogin struct {
	client  *Client
	browser []byte
}

// NewPrelogin 以登录开始的设备档案创建登录前虚拟进程，日志中的账户记为 login
func NewPrelogin(device Device, proxy Proxy) *Prelogin {
	client := NewClient("login", staticToken{}, device, proxy)
	client.SetHub(HubState{}, nil)
	return &Prelogin{client: client, browser: []byte(uuid.NewString())}
}

// Run 在启动后 5.1–5.7 s 用设备 installation_id 发出该系统的第一个登录前批次（取官方 2.33.0 的新安装登录前现场），之后按模板中的上报时刻继续发出其余批次；ctx 结束时返回
func (p *Prelogin) Run(ctx context.Context) {
	template, ok := preloginTemplates[p.client.device.OS]
	if !ok {
		<-ctx.Done()
		return
	}
	select {
	case <-time.After(preloginErrorOffset + rand.N(600*time.Millisecond)):
	case <-ctx.Done():
		return
	}
	reported := time.Now()
	for _, batch := range template.batches {
		select {
		case <-time.After(time.Until(reported.Add(time.Duration(batch.sent-template.batches[0].sent) * time.Millisecond))):
		case <-ctx.Done():
			return
		}
		p.send(ctx, batch.data, batch.sent)
	}
	<-ctx.Done()
}

// send 以本进程的界面会话标识改写一个登录前批次并不带 Bearer 发出，时刻平移到当前
func (p *Prelogin) send(ctx context.Context, data []byte, sent uint64) {
	template := preloginTemplates[p.client.device.OS]
	body := rewritePrelogin(data, template.install, []byte(p.client.device.InstallationID), time.Now().UnixMilli()-int64(sent), p.browser)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointClearcut, bytes.NewReader(body))
	if err != nil {
		return
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	p.client.discard(p.client.h2, request)
}

// staticToken 是登录前虚拟进程用的空 token 源，不带 Bearer 的 clearcut 不读取它
type staticToken struct{}

// Token 返回空 token
func (staticToken) Token() (*oauth2.Token, error) { return &oauth2.Token{}, nil }
