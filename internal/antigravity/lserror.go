package antigravity

import (
	"context"
	"embed"
	"io/fs"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// lsErrorLimit 为官方 errorreport 读取 goroutine 栈的缓冲区字节数，栈更长时截断
	lsErrorLimit = 8192
	// slashResolutions 为界面渲染后 GetSlashCommands 解析会话配置的次数
	slashResolutions = 2
)

// lsErrorRate 为官方 language_server 上报一次 LS_ERROR 的抽样率：实验开关 record-error-sampling-rates 没有列出 LS_ERROR 时取默认值 0.1
var lsErrorRate = 0.1

// lsErrorFiles 为各系统 LS_ERROR 的 goroutine 栈模板，文件名为 lserror_<系统>_<路径>_<查询>.txt.gz：路径 slash 为界面 GetSlashCommands，exec 为执行器准备一轮发送；查询 general 为默认通用模型，planner 为规划模型
//
//go:embed lserror_*.txt.gz
var lsErrorFiles embed.FS

// lsErrorTemplates 为按 <系统>_<路径>_<查询> 索引的栈模板
var lsErrorTemplates = loadLSErrors()

// goroutineNumber 匹配栈文本中的 goroutine 编号
var goroutineNumber = regexp.MustCompile(`goroutine \d+`)

// loadLSErrors 解压全部栈模板
func loadLSErrors() map[string][]byte {
	out := map[string][]byte{}
	names, _ := fs.Glob(lsErrorFiles, "lserror_*.txt.gz")
	for _, name := range names {
		gz, _ := lsErrorFiles.ReadFile(name)
		out[strings.TrimSuffix(strings.TrimPrefix(name, "lserror_"), ".txt.gz")] = gunzip(gz)
	}
	return out
}

// lsError 把一个时刻为 at 的 LS_ERROR 事件（码 14，载荷在扩展字段 15）放入上报队列：栈取本系统 path 路径 lookup 查询的模板，第一处 goroutine 编号换成 goroutine、第二处（创建者）换成 creator，指针按本进程偏移平移后截到 lsErrorLimit 字节，载荷时刻比事件早 0–1 ms；没有模板的系统不记录
func (c *Client) lsError(at time.Time, path, lookup string, goroutine, creator int) {
	template, ok := lsErrorTemplates[c.device.OS+"_"+path+"_"+lookup]
	if !ok {
		return
	}
	c.mu.Lock()
	state, shift := c.state, c.hub.shift
	c.mu.Unlock()
	numbers := []int{goroutine, creator}
	text := goroutineNumber.ReplaceAllFunc(template, func(match []byte) []byte {
		if len(numbers) == 0 {
			return match
		}
		number := numbers[0]
		numbers = numbers[1:]
		return []byte("goroutine " + strconv.Itoa(number))
	})
	text = shift.apply(text)
	report := appendBytes(nil, 1, []byte("LS_ERROR"))
	report = appendVarint(report, 2, uint64(at.UnixMilli()-int64(between(0, 1))))
	report = appendBytes(report, 3, text[:min(len(text), lsErrorLimit)])
	c.log(state.structuredEvent(at, c.device, eventShape{flags: true}, 14, 15, appendBytes(nil, 1, report)))
}

// lookups 按 lsErrorRate 对 count 次同一查询的失败逐次抽样，抽中的记为时刻 at 的 LS_ERROR 事件
func (c *Client) lookups(at time.Time, path, lookup string, count, goroutine, creator int) {
	for range count {
		if rand.Float64() < lsErrorRate {
			c.lsError(at, path, lookup, goroutine, creator)
		}
	}
}

// slashErrors 记录界面渲染后 GetSlashCommands 解析会话配置时被抽中的查询失败：渲染 117–479 ms 后在启动时的 goroutine 中解析 slashResolutions 次，规划模型的查询晚 8–10 ms；ctx 结束时返回
func (c *Client) slashErrors(ctx context.Context, rendered time.Time) {
	if until(ctx, rendered.Add(time.Duration(between(117, 479))*time.Millisecond)) != nil {
		return
	}
	c.mu.Lock()
	goroutine := c.hub.goroutine
	c.mu.Unlock()
	c.lookups(time.Now(), "slash", "general", slashResolutions, goroutine, 0)
	if pause(ctx, 8, 10) == nil {
		c.lookups(time.Now(), "slash", "planner", slashResolutions, goroutine, 0)
	}
}

// sendErrors 记录一次用户发送解析会话配置时被抽中的查询失败：goroutine 编号先增加 943–1168，执行器在发送事件前 1 ms 到后 6 ms 解析一次，规划模型的查询晚 2–3 ms，执行器 goroutine 的创建者编号与它相差 −33–75；新会话的界面另在 opened 时刻以早 260–280 的 goroutine 经 GetSlashCommands 解析一次，规划模型的查询晚 2 ms
func (c *Client) sendErrors(sent, opened time.Time) {
	c.mu.Lock()
	c.hub.goroutine += between(943, 1168)
	goroutine := c.hub.goroutine
	c.mu.Unlock()
	if !opened.IsZero() {
		slash := goroutine - between(260, 280)
		c.lookups(opened, "slash", "general", 1, slash, 0)
		c.lookups(opened.Add(2*time.Millisecond), "slash", "planner", 1, slash, 0)
	}
	resolved, creator := sent.Add(time.Duration(between(-1, 6))*time.Millisecond), goroutine+between(-33, 75)
	c.lookups(resolved, "exec", "general", 1, goroutine, creator)
	c.lookups(resolved.Add(time.Duration(between(2, 3))*time.Millisecond), "exec", "planner", 1, goroutine, creator)
}
