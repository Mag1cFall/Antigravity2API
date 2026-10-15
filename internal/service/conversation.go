package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// conversationTTL 为会话记录在最后一次使用后的保留时长
const conversationTTL = 24 * time.Hour

// conversation 表示一个客户端会话的 cascade 与 trajectory 标识、最近一次请求在摘要链中的位置与其成功生成的回答指纹、各条消息首次出现的 Unix 秒、最近一次成功生成的上游输入用量、各函数调用 ID 的思考签名与各签名摘要对应的模型族
type conversation struct {
	Cascade    string            `json:"cascade"`
	Trajectory string            `json:"trajectory"`
	Length     int               `json:"length"`
	Digest     string            `json:"digest"`
	Answer     string            `json:"answer,omitempty"`
	Times      []int64           `json:"times"`
	Prompt     *promptUsage      `json:"prompt,omitempty"`
	Signatures map[string]string `json:"signatures,omitempty"`
	Origins    map[string]string `json:"origins,omitempty"`
	Used       time.Time         `json:"used"`
	New        bool              `json:"-"`
}

// promptUsage 表示一次成功生成的模型、规范消息条数、该处的链摘要、上游 contents 条数与上游 promptTokenCount
type promptUsage struct {
	Model    string `json:"model"`
	Length   int    `json:"length"`
	Digest   string `json:"digest"`
	Contents int    `json:"contents"`
	Tokens   int64  `json:"tokens"`
}

// conversations 在内存与 dir 下每个会话一个 JSON 文件中保存会话记录
type conversations struct {
	dir   string
	mu    sync.Mutex
	items map[string]*conversation
	now   func() time.Time
}

// openConversations 读取 dir 下未过期的会话记录，目录不存在时创建
func openConversations(dir string) (*conversations, error) {
	c := &conversations{dir: dir, items: make(map[string]*conversation), now: time.Now}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var item conversation
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		c.items[item.Cascade] = &item
	}
	c.sweep()
	return c, nil
}

// chain 返回请求的摘要链：第 0 项为会话标识、客户端系统提示与工具声明的摘要，第 i 项由第 i-1 项与第 i 条消息算出
func chain(request antigravity.GenerateRequest) []string {
	head, _ := json.Marshal(struct {
		Session string
		System  string
		Tools   antigravity.Tools
	}{request.Session, request.System, request.Tools})
	digests := []string{digest("", head)}
	for _, content := range request.Contents {
		encoded, _ := json.Marshal(content)
		digests = append(digests, digest(digests[len(digests)-1], encoded))
	}
	return digests
}

// replies 返回请求中每条消息的回答指纹，非 assistant 消息为空
func replies(request antigravity.GenerateRequest) []string {
	keys := make([]string, len(request.Contents))
	for index, content := range request.Contents {
		if content.Role == antigravity.RoleAssistant {
			keys[index] = answerKey(content.Parts)
		}
	}
	return keys
}

// answerKey 返回一条回答的指纹：非思考正文按空白归一后相连，函数调用取名称与按键排序的参数
func answerKey(parts []antigravity.Part) string {
	var text strings.Builder
	var calls []string
	for _, part := range parts {
		switch {
		case part.FunctionCall != nil:
			var arguments any
			json.Unmarshal(part.FunctionCall.Arguments, &arguments)
			encoded, _ := json.Marshal(arguments)
			calls = append(calls, part.FunctionCall.Name+string(encoded))
		case !part.Thought:
			text.WriteString(part.Text)
		}
	}
	return digest("", []byte(strings.Join(strings.Fields(text.String()), " ")+"\x00"+strings.Join(calls, "\x00")))
}

// digest 返回上一项摘要与本项内容的 SHA-256
func digest(previous string, data []byte) string {
	sum := sha256.Sum256(append([]byte(previous), data...))
	return hex.EncodeToString(sum[:])
}

// begin 为生成请求取得会话：先找最近一次请求是本次严格前缀的会话，其次找本次与之相同但尚未成功生成的会话，都没有时新建；记下本次的链位置与新消息的时间并落盘，返回会话副本，新建时副本的 New 为真
func (c *conversations) begin(digests, answers []string) (conversation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	length := len(digests) - 1
	item := c.match(digests, answers, func(item *conversation) bool {
		return item.Length < length || item.Prompt == nil || item.Prompt.Length < item.Length
	})
	created := item == nil
	if created {
		item = &conversation{Cascade: uuid.NewString(), Trajectory: uuid.NewString()}
		c.items[item.Cascade] = item
	}
	times := item.Times[:min(item.Length, len(item.Times))]
	for len(times) < length {
		times = append(times, now.Unix())
	}
	item.Length, item.Digest, item.Answer, item.Times, item.Used = length, digests[length], "", times, now
	c.sweep()
	snapshot := c.snapshot(item)
	snapshot.New = created
	return snapshot, c.save(item)
}

// find 返回链位置是本次前缀的会话副本，供计数使用且不改动记录
func (c *conversations) find(digests, answers []string) (conversation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.match(digests, answers, func(*conversation) bool { return true })
	if item == nil {
		return conversation{}, false
	}
	return c.snapshot(item), true
}

// match 在满足 accept 的会话中取链位置是本次前缀者，链位置最长者优先；并列时先取回答指纹与本次该位置消息一致者，再取最近使用者
func (c *conversations) match(digests, answers []string, accept func(*conversation) bool) *conversation {
	answered := func(item *conversation) bool {
		return item.Answer != "" && item.Length < len(answers) && answers[item.Length] == item.Answer
	}
	var best *conversation
	for _, item := range c.items {
		if item.Length >= len(digests) || digests[item.Length] != item.Digest || !accept(item) {
			continue
		}
		if best == nil || item.Length > best.Length || item.Length == best.Length && (answered(item) && !answered(best) || answered(item) == answered(best) && item.Used.After(best.Used)) {
			best = item
		}
	}
	return best
}

// record 记下会话一次成功生成的模型、消息条数、上游 contents 条数、上游 promptTokenCount 与输出中的思考签名，会话仍停在本次请求时记下回答指纹
func (c *conversations) record(cascade string, digests []string, answer, model string, contents int, tokens int64, signed signatureRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.items[cascade]
	if item == nil {
		return nil
	}
	length := len(digests) - 1
	item.Prompt = &promptUsage{Model: model, Length: length, Digest: digests[length], Contents: contents, Tokens: tokens}
	if item.Length == length && item.Digest == digests[length] {
		item.Answer = answer
	}
	if len(signed.calls) > 0 && item.Signatures == nil {
		item.Signatures = map[string]string{}
	}
	maps.Copy(item.Signatures, signed.calls)
	if len(signed.origins) > 0 && item.Origins == nil {
		item.Origins = map[string]string{}
	}
	maps.Copy(item.Origins, signed.origins)
	return c.save(item)
}

// usage 返回与本次请求前缀一致、模型相同且消息最多的一次成功生成的输入用量
func (c *conversations) usage(digests []string, model string) (promptUsage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var best *promptUsage
	for _, item := range c.items {
		prompt := item.Prompt
		if prompt == nil || prompt.Model != model || prompt.Length >= len(digests) || digests[prompt.Length] != prompt.Digest {
			continue
		}
		if best == nil || prompt.Length > best.Length {
			best = prompt
		}
	}
	if best == nil {
		return promptUsage{}, false
	}
	return *best, true
}

// snapshot 返回会话记录的副本
func (c *conversations) snapshot(item *conversation) conversation {
	copied := *item
	copied.Times = append([]int64(nil), item.Times...)
	copied.Signatures = maps.Clone(item.Signatures)
	copied.Origins = maps.Clone(item.Origins)
	return copied
}

// save 先写同目录临时文件再替换会话文件
func (c *conversations) save(item *conversation) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	path := filepath.Join(c.dir, item.Cascade+".json")
	temp, err := os.CreateTemp(c.dir, ".conversation-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// sweep 删除空闲超过 conversationTTL 的会话及其文件
func (c *conversations) sweep() {
	now := c.now()
	for cascade, item := range c.items {
		if now.Sub(item.Used) < conversationTTL {
			continue
		}
		delete(c.items, cascade)
		os.Remove(filepath.Join(c.dir, cascade+".json"))
	}
}
