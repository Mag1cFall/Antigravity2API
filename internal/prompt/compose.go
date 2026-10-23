package prompt

import (
	_ "embed"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

var (
	//go:embed official.txt
	official string
	//go:embed templates/full.system.tmpl
	fullSystem string
	//go:embed templates/full.user.tmpl
	fullUser string
	//go:embed templates/medium.system.tmpl
	mediumSystem string
	//go:embed templates/medium.user.tmpl
	mediumUser string
	//go:embed templates/aggressive.system.tmpl
	aggressiveSystem string
)

// clientIdentity 匹配客户端系统提示中行首带归属声明的身份句：You are 加以 agent、assistant 或 AI 结尾的身份短语，8 个字符内接 created by、built on、based on 等归属声明，到句号为止
var clientIdentity = regexp.MustCompile(`(?im)^([ \t]*)(?:you are|you're)\s+(?:an?\s+)?(?:[\w'-]+[\s,]+){0,8}(?:agent|assistant|ai)\b[^.!?\n<>]{0,8}?\b(?:(?:created|built|made|developed|designed|trained|powered|maintained|published|released|provided)\s+(?:by|on|upon)|based\s+on)\s+[^\n<>]{1,80}?\.([ \t]|$)`)

// neutralIdentity 为替换客户端身份句的中性身份句
const neutralIdentity = "You are an AI Agent."

// featuredSection 为推荐插件段的段名，没有推荐插件时该段为空
const featuredSection = "featured_plugins"

// funcs 为 system 模板可用的函数
var funcs = template.FuncMap{"sections": joinSections}

// builtins 为内置三档的模板
var builtins = map[Tier]templates{
	TierFull:       {System: fullSystem, User: fullUser},
	TierMedium:     {System: mediumSystem, User: mediumUser},
	TierAggressive: {System: aggressiveSystem},
}

// wrapTemplates 为各包装方式的 user 模板
var wrapTemplates = map[string]string{WrapFull: fullUser, WrapTime: mediumUser, WrapNone: ""}

// officialSections 为按段名切分的官方段模板，SectionNames 为官方原文中的段名顺序
var officialSections, SectionNames = parseSections(official)

// previewData 为预览使用的会话、设备与模型，客户端系统提示与用户消息为占位文字
var previewData = systemData{
	Client:           "<client system prompt>",
	ConversationID:   "<conversation-id>",
	AppDataDir:       `C:\Users\user\.gemini\antigravity`,
	Separator:        `\`,
	OS:               "windows",
	ModelDisplayName: "<model>",
	FeaturedPlugins:  "<featured plugins>\n\n",
}

// Section 为一个官方段的段名与以预览数据渲染的原文
type Section struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

// Preview 为一档以预览数据渲染的系统指令、首轮 user 消息与全部官方段
type Preview struct {
	System   string    `json:"system"`
	User     string    `json:"user"`
	Sections []Section `json:"sections"`
}

// systemData 为 system 模板与官方段模板的数据，Separator 为设备系统的路径分隔符，FeaturedPlugins 为推荐插件段的货架正文
type systemData struct {
	Sections         map[string]string
	Client           string
	ConversationID   string
	AppDataDir       string
	Separator        string
	OS               string
	ModelDisplayName string
	FeaturedPlugins  string
}

// userData 为 user 模板的数据
type userData struct {
	Text             string
	LocalTime        string
	First            bool
	ModelDisplayName string
}

// Composer 按档位用一组 system 与 user 模板把客户端请求改写成送往上游的形状
type Composer struct {
	tier   Tier
	system *template.Template
	user   *template.Template
}

// New 返回档位对应的 Composer，内置三档用内嵌模板，自定义档按 custom 选中的段与包装方式生成模板
func New(tier Tier, custom Custom) (*Composer, error) {
	source, ok := builtins[tier]
	if tier == TierCustom {
		var err error
		if source, err = custom.templates(); err != nil {
			return nil, err
		}
	} else if !ok {
		return nil, fmt.Errorf("unknown prompt tier %q", tier)
	}
	system, err := parse("system", source.System)
	if err != nil {
		return nil, err
	}
	c := &Composer{tier: tier, system: system}
	if source.User != "" {
		if c.user, err = parse("user", source.User); err != nil {
			return nil, err
		}
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// templates 按官方顺序以 sections 拼接选中的段，客户端系统提示接在最后，user 模板取包装方式对应的模板
func (c Custom) templates() (templates, error) {
	user, ok := wrapTemplates[c.Wrap]
	if !ok {
		return templates{}, fmt.Errorf("user 轮包装方式必须是 %s", strings.Join(Wraps, "、"))
	}
	var parts []string
	for _, name := range c.Sections {
		if !slices.Contains(SectionNames, name) {
			return templates{}, fmt.Errorf("没有名为 %s 的官方段，可选 %s", name, strings.Join(SectionNames, "、"))
		}
	}
	for _, name := range SectionNames {
		if slices.Contains(c.Sections, name) {
			parts = append(parts, strconv.Quote(name))
		}
	}
	if len(parts) == 0 {
		return templates{System: "{{.Client}}", User: user}, nil
	}
	return templates{System: "{{sections .Sections " + strings.Join(parts, " ") + "}}{{if .Client}}\n\n{{.Client}}{{end}}", User: user}, nil
}

// Render 以预览数据渲染档位的系统指令与首轮 user 消息，并列出全部官方段的原文
func Render(tier Tier, custom Custom) (Preview, error) {
	c, err := New(tier, custom)
	if err != nil {
		return Preview{}, err
	}
	data := previewData
	data.Sections = renderSections(data)
	preview := Preview{System: execute(c.system, data), User: "<user message>"}
	if c.user != nil {
		preview.User = execute(c.user, userData{Text: preview.User, LocalTime: "2026-10-15T14:00:00+08:00", First: true, ModelDisplayName: data.ModelDisplayName})
	}
	for _, name := range SectionNames {
		preview.Sections = append(preview.Sections, Section{Name: name, Text: data.Sections[name]})
	}
	return preview, nil
}

// Tier 返回 Composer 的档位
func (c *Composer) Tier() Tier {
	return c.tier
}

// Compose 以会话 ID、账户设备档案与推荐插件段的货架正文渲染系统指令并包装 user 轮，times[i] 为 contents[i] 首次出现时的本地时间；客户端系统提示中带归属声明的身份句改写为中性身份句，工具声明保持不变
func (c *Composer) Compose(request antigravity.GenerateRequest, model antigravity.CatalogModel, conversation string, times []string, device antigravity.Device, plugins string) antigravity.GenerateRequest {
	data := deviceData(model, conversation, device, plugins)
	data.Client = clientIdentity.ReplaceAllString(request.System, "${1}"+neutralIdentity+"${2}")
	data.Sections = renderSections(data)
	request.System = execute(c.system, data)
	if c.user != nil {
		request.Contents = c.wrap(request.Contents, times, model.DisplayName)
	}
	return request
}

// deviceData 返回以会话、设备档案与推荐插件段的货架正文填写的 system 模板数据
func deviceData(model antigravity.CatalogModel, conversation string, device antigravity.Device, plugins string) systemData {
	data := systemData{
		ConversationID:   conversation,
		AppDataDir:       device.AppDataDir,
		Separator:        "/",
		OS:               device.OS,
		ModelDisplayName: model.DisplayName,
		FeaturedPlugins:  plugins,
	}
	if device.OS == "windows" {
		data.Separator = `\`
	}
	return data
}

// SectionTokens 返回以会话、设备档案与推荐插件段的货架正文渲染的各官方段按 ceil(UTF-8 字节数/3) 计的 token 数，键为段名
func SectionTokens(model antigravity.CatalogModel, conversation string, device antigravity.Device, plugins string) map[string]int {
	tokens := make(map[string]int, len(officialSections))
	for name, text := range renderSections(deviceData(model, conversation, device, plugins)) {
		tokens[name] = (len(text) + 2) / 3
	}
	return tokens
}

// validate 以各字段都有值的数据试渲染模板，提前暴露字段名与段名错误
func (c *Composer) validate() error {
	data := systemData{Client: "client", ConversationID: "00000000-0000-0000-0000-000000000000", AppDataDir: "app", Separator: "/", OS: "os", ModelDisplayName: "model"}
	data.Sections = renderSections(data)
	if err := c.system.Execute(io.Discard, data); err != nil {
		return err
	}
	if c.user == nil {
		return nil
	}
	return c.user.Execute(io.Discard, userData{Text: "text", LocalTime: "2026-10-14T14:47:22+08:00", First: true, ModelDisplayName: "model"})
}

// wrap 用 user 模板包装每个含文本且不含工具结果的 user 轮，文本块以换行相连，其余 part 排在其后
func (c *Composer) wrap(contents []antigravity.Content, times []string, model string) []antigravity.Content {
	wrapped := slices.Clone(contents)
	first := true
	for index, content := range contents {
		if !userTurn(content) {
			continue
		}
		var texts []string
		var rest []antigravity.Part
		for _, part := range content.Parts {
			if part.Text != "" {
				texts = append(texts, part.Text)
			} else {
				rest = append(rest, part)
			}
		}
		text := execute(c.user, userData{Text: strings.Join(texts, "\n"), LocalTime: times[index], First: first, ModelDisplayName: model})
		wrapped[index].Parts = append([]antigravity.Part{{Text: text}}, rest...)
		first = false
	}
	return wrapped
}

// userTurn 判断消息是否为需要包装的 user 轮
func userTurn(content antigravity.Content) bool {
	if content.Role != antigravity.RoleUser {
		return false
	}
	text := false
	for _, part := range content.Parts {
		if part.FunctionResult != nil {
			return false
		}
		text = text || part.Text != ""
	}
	return text
}

// parse 解析模板，引用不存在的段名时执行报错
func parse(name, text string) (*template.Template, error) {
	t, err := template.New(name).Option("missingkey=error").Funcs(funcs).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}
	return t, nil
}

// parseSections 把官方原文切成以段名为键的模板并按出现顺序返回段名，各段以 <段名> 开头、</段名> 结尾、段间以换行相连
func parseSections(text string) (map[string]*template.Template, []string) {
	sections := map[string]*template.Template{}
	var names []string
	for text != "" {
		name := text[1:strings.IndexByte(text, '>')]
		end := strings.Index(text, "</"+name+">") + len(name) + 3
		sections[name] = template.Must(template.New(name).Parse(text[:end]))
		names = append(names, name)
		text = strings.TrimPrefix(text[end:], "\n")
	}
	return sections, names
}

// renderSections 以运行时字段渲染全部官方段，没有推荐插件时推荐插件段为空
func renderSections(data systemData) map[string]string {
	rendered := make(map[string]string, len(officialSections))
	for name, section := range officialSections {
		if name == featuredSection && data.FeaturedPlugins == "" {
			rendered[name] = ""
			continue
		}
		rendered[name] = execute(section, data)
	}
	return rendered
}

// joinSections 按 names 的顺序以换行连接已渲染的非空官方段，段名不存在时报错
func joinSections(rendered map[string]string, names ...string) (string, error) {
	var parts []string
	for _, name := range names {
		text, ok := rendered[name]
		if !ok {
			return "", fmt.Errorf("没有名为 %s 的官方段", name)
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// execute 渲染模板，模板已在 New 中以完整数据试渲染
func execute(t *template.Template, data any) string {
	var out strings.Builder
	if err := t.Execute(&out, data); err != nil {
		panic(err)
	}
	return out.String()
}
