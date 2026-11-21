package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
	"github.com/Mag1cFall/Antigravity2API/internal/robustio"
	"github.com/Mag1cFall/Antigravity2API/internal/tokencount"
)

// geminiSignatureReplay 为 Gemini 族上游把上一次回答的思考签名计入输入时在该次 thoughtsTokenCount 之外另计的 token 数
const geminiSignatureReplay = 2

// conversationTTL 为会话记录在最后一次使用后的保留时长
const conversationTTL = 24 * time.Hour

// conversationTemp 为写入会话文件时同目录临时文件的名称前缀
const conversationTemp = ".conversation-"

// conversation 表示持久化保存的一个客户端会话状态
// 含会话标识、摘要链、消息首次出现时间、输入用量与思考签名，规则见 docs/protocol.md“labels 与会话标识”
type conversation struct {
	Cascade    string            `json:"cascade"`
	Trajectory string            `json:"trajectory"`
	Head       string            `json:"head"`
	Length     int               `json:"length"`
	Digest     string            `json:"digest"`
	Chain      []string          `json:"chain,omitempty"`
	Answer     string            `json:"answer,omitempty"`
	Times      []int64           `json:"times"`
	Prompt     *promptUsage      `json:"prompt,omitempty"`
	Signatures map[string]string `json:"signatures,omitempty"`
	Origins    map[string]string `json:"origins,omitempty"`
	// Accounts 是各账户发往上游的 cascade 与 trajectory 标识，第一个服务该会话的账户用 Cascade 与 Trajectory，其余账户各自另起一组
	Accounts map[string]upstreamIdentity `json:"accounts,omitempty"`
	Used     time.Time                   `json:"used"`
	New      bool                        `json:"-"`
}

// upstreamIdentity 是一个账户在会话中发往上游的 cascade 与 trajectory 标识
type upstreamIdentity struct {
	Cascade    string `json:"cascade"`
	Trajectory string `json:"trajectory"`
}

// promptUsage 表示一次成功生成的模型、规范消息条数、该处的链摘要、客户端系统提示、工具声明与拒绝句规则的摘要、上游 contents 条数、上游 promptTokenCount 与 thoughtsTokenCount
type promptUsage struct {
	Model    string `json:"model"`
	Length   int    `json:"length"`
	Digest   string `json:"digest"`
	Frame    string `json:"frame"`
	Contents int    `json:"contents"`
	Tokens   int64  `json:"tokens"`
	Thoughts int64  `json:"thoughts,omitempty"`
}

// replay 返回接续的 contents 中该次回答的思考签名按 thoughtsTokenCount 计入输入比按签名长度估算多出的 token 数，只用于 Gemini 族，占位签名不计入
func (p promptUsage) replay(provider string, contents []antigravity.WireContent) int64 {
	if provider != antigravity.ProviderGoogle || p.Thoughts == 0 || len(contents) == 0 || contents[0].Role != "model" {
		return 0
	}
	for _, part := range contents[0].Parts {
		if part.ThoughtSignature != "" && !sentinelSignatures[part.ThoughtSignature] {
			return p.Thoughts + geminiSignatureReplay - tokencount.Signature(provider, part.ThoughtSignature)
		}
	}
	return 0
}

// conversations 在内存与 dir 下每个会话一个 JSON 文件中保存会话记录
type conversations struct {
	dir   string
	mu    sync.Mutex
	items map[string]*conversation
	now   func() time.Time
}

// openConversations 读取 dir 下未过期的会话记录并删除写入中断留下的临时文件，目录不存在时创建
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
		if strings.HasPrefix(entry.Name(), conversationTemp) {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
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

// identity 表示一次请求用于匹配会话与计数缓存的指纹
// digests[0] 为会话分区摘要，其余项逐消息累积；摘要算法见 docs/protocol.md“labels 与会话标识”
type identity struct {
	digests []string
	answers []string
	anchors []string
	frame   string
}

// identify 返回请求的会话指纹
func identify(request antigravity.GenerateRequest) identity {
	frame, _ := json.Marshal(struct {
		System   string
		Tools    antigravity.Tools
		Rejected string
	}{request.System, request.Tools, prompt.RejectedDigest()})
	id := identity{digests: []string{digest("", []byte(request.Session))}, answers: make([]string, len(request.Contents)), frame: digest("", frame)}
	for index, content := range request.Contents {
		encoded, _ := json.Marshal(content)
		id.digests = append(id.digests, digest(id.digests[index], encoded))
		if content.Role != antigravity.RoleAssistant {
			continue
		}
		id.answers[index] = answerKey(content.Parts)
		for _, part := range content.Parts {
			signatures := []string{part.ThoughtSignature}
			if part.FunctionCall != nil {
				signatures = append(signatures, part.FunctionCall.ThoughtSignature)
			}
			for _, signature := range signatures {
				if signature != "" && !sentinelSignatures[signature] {
					id.anchors = append(id.anchors, signature)
				}
			}
		}
	}
	return id
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

// replySignatureKey 返回回答正文签名在会话签名表中的键：position 为回答在客户端消息中的序号，同一会话中相同正文的两次回答各有各的签名
func replySignatureKey(answer string, position int) string {
	return "reply:" + strconv.Itoa(position) + ":" + answer
}

// digest 返回上一项摘要与本项内容的 SHA-256
func digest(previous string, data []byte) string {
	hash := sha256.New()
	io.WriteString(hash, previous)
	hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}

// begin 按前缀、回退与思考签名依次匹配或新建会话，更新摘要链与消息时间并落盘
// 返回会话副本，新建时副本的 New 为真；匹配规则见 docs/protocol.md“labels 与会话标识”
func (c *conversations) begin(id identity) (conversation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	digests := id.digests
	length := len(digests) - 1
	item := c.match(id, func(item *conversation) bool {
		return length > 1 || item.Length < length || item.Prompt == nil || item.Prompt.Length < item.Length
	})
	shared := 0
	if item != nil {
		shared = item.Length
	}
	if length > 1 {
		if back, kept := c.rewind(digests); kept > shared {
			item, shared = back, kept
		}
	}
	if item == nil {
		item = c.anchor(id)
	}
	created := item == nil
	if created {
		item = &conversation{Cascade: uuid.NewString(), Trajectory: uuid.NewString()}
		c.items[item.Cascade] = item
	}
	times := item.Times[:min(shared, len(item.Times))]
	for len(times) < length {
		times = append(times, now.Unix())
	}
	item.Head, item.Length, item.Digest, item.Chain, item.Answer, item.Times, item.Used = digests[0], length, digests[length], history(digests), "", times, now
	c.sweep()
	snapshot := c.snapshot(item)
	snapshot.New = created
	return snapshot, c.save(item)
}

// identity 返回账户在会话 cascade 中发往上游的标识与该账户是否第一次服务该会话：第一个服务的账户用会话的 Cascade 与 Trajectory，其余账户第一次服务时另起一组并保存；会话已不在记录中时为会话的标识
func (c *conversations) identity(cascade, trajectory, account string) (upstreamIdentity, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.items[cascade]
	if item == nil {
		return upstreamIdentity{Cascade: cascade, Trajectory: trajectory}, false, nil
	}
	if known, ok := item.Accounts[account]; ok {
		return known, false, nil
	}
	assigned := upstreamIdentity{Cascade: item.Cascade, Trajectory: item.Trajectory}
	if len(item.Accounts) > 0 {
		assigned = upstreamIdentity{Cascade: uuid.NewString(), Trajectory: uuid.NewString()}
	}
	if item.Accounts == nil {
		item.Accounts = map[string]upstreamIdentity{}
	}
	item.Accounts[account] = assigned
	return assigned, len(item.Accounts) > 1, c.save(item)
}

// history 返回摘要链中链首之后、链位置之前各项的副本，链位置之前没有消息时为空
func history(digests []string) []string {
	if len(digests) <= 2 {
		return nil
	}
	return slices.Clone(digests[1 : len(digests)-1])
}

// rewind 返回从第 1 条消息起与 digests 相同条数最多的会话与相同条数
// 按链摘要二分查找，并列取最近使用者，第 1 条即不同时返回 nil 与 0
func (c *conversations) rewind(digests []string) (*conversation, int) {
	var best *conversation
	kept := 0
	for _, item := range c.items {
		count := sort.Search(min(len(item.Chain), len(digests)-1), func(i int) bool { return item.Chain[i] != digests[i+1] })
		if count > kept || count > 0 && count == kept && item.Used.After(best.Used) {
			best, kept = item, count
		}
	}
	return best, kept
}

// anchor 返回同一分区中签名表含有本次某个思考签名的会话，多个时取最近使用者，都没有时返回 nil
func (c *conversations) anchor(id identity) *conversation {
	if len(id.anchors) == 0 {
		return nil
	}
	var best *conversation
	for _, item := range c.items {
		if item.Head != id.digests[0] || len(item.Signatures) == 0 || best != nil && !item.Used.After(best.Used) {
			continue
		}
		saved := make(map[string]bool, len(item.Signatures))
		for _, signature := range item.Signatures {
			saved[signature] = true
		}
		if slices.ContainsFunc(id.anchors, func(signature string) bool { return saved[signature] }) {
			best = item
		}
	}
	return best
}

// find 返回链位置是本次前缀的会话副本，供计数使用且不改动记录
func (c *conversations) find(id identity) (conversation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.match(id, func(*conversation) bool { return true })
	if item == nil {
		return conversation{}, false
	}
	return c.snapshot(item), true
}

// match 在满足 accept 的会话中取链位置是本次前缀者，链位置最长者优先；并列时先取回答指纹与本次该位置消息一致者，再取最近使用者
func (c *conversations) match(id identity, accept func(*conversation) bool) *conversation {
	digests, answers := id.digests, id.answers
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

// record 记下会话一次成功生成的模型、上游输入用量、思考签名与回答指纹并落盘
// 回答正文签名以回答指纹与消息序号为键；仅当会话仍停在本次请求时更新回答指纹
func (c *conversations) record(cascade string, id identity, answer, model string, contents int, tokens, thoughts int64, signed signatureRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	item := c.items[cascade]
	if item == nil {
		return nil
	}
	digests := id.digests
	length := len(digests) - 1
	item.Prompt = &promptUsage{Model: model, Length: length, Digest: digests[length], Frame: id.frame, Contents: contents, Tokens: tokens, Thoughts: thoughts}
	if item.Length == length && item.Digest == digests[length] {
		item.Answer = answer
	}
	if (len(signed.calls) > 0 || signed.reply != "") && item.Signatures == nil {
		item.Signatures = map[string]string{}
	}
	maps.Copy(item.Signatures, signed.calls)
	if signed.reply != "" {
		item.Signatures[replySignatureKey(answer, length)] = signed.reply
	}
	if len(signed.origins) > 0 && item.Origins == nil {
		item.Origins = map[string]string{}
	}
	maps.Copy(item.Origins, signed.origins)
	return c.save(item)
}

// usage 返回与本次请求前缀一致、系统提示、工具声明与拒绝句规则相同、模型相同且消息最多的一次成功生成的输入用量
func (c *conversations) usage(id identity, model string) (promptUsage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	digests := id.digests
	var best *promptUsage
	for _, item := range c.items {
		prompt := item.Prompt
		if prompt == nil || prompt.Model != model || prompt.Frame != id.frame || prompt.Length >= len(digests) || digests[prompt.Length] != prompt.Digest {
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
	copied.Accounts = maps.Clone(item.Accounts)
	return copied
}

// save 先写同目录临时文件再替换会话文件
func (c *conversations) save(item *conversation) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	path := filepath.Join(c.dir, item.Cascade+".json")
	temp, err := os.CreateTemp(c.dir, conversationTemp+"*")
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
	return robustio.Rename(temp.Name(), path)
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
