package prompt

import (
	_ "embed"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

var (
	//go:embed official.txt
	official string
	//go:embed templates/full.user.tmpl
	fullUser string
	//go:embed templates/medium.user.tmpl
	mediumUser string
)

// clientIdentity 匹配客户端系统提示中行首带归属声明的身份句：You are 加以 agent、assistant 或 AI 结尾的身份短语，8 个字符内接 created by、built on、based on 等归属声明，到句号为止
var clientIdentity = regexp.MustCompile(`(?im)^([ \t]*)(?:you are|you're)\s+(?:an?\s+)?(?:[\w'-]+[\s,]+){0,8}(?:agent|assistant|ai)\b[^.!?\n<>]{0,8}?\b(?:(?:created|built|made|developed|designed|trained|powered|maintained|published|released|provided)\s+(?:by|on|upon)|based\s+on)\s+[^\n<>]{1,80}?\.([ \t]|$)`)

// neutralIdentity 为替换客户端身份句的中性身份句
const neutralIdentity = "You are an AI Agent."

// keywordsLine 匹配客户端系统提示中含 RFC 2119 keywords 的一行与行尾换行，不区分大小写；上游拒绝带这一写法的关键词说明句
var keywordsLine = regexp.MustCompile(`(?im)^.*RFC 2119 keywords.*\n?`)

// featuredSection 为推荐插件段的段名，没有推荐插件时该段为空
const featuredSection = "featured_plugins"

// clientSuffix 为 system 模板中接在官方段之后的客户端系统提示，与官方段隔一个空行
const clientSuffix = "{{if .Client}}\n\n{{.Client}}{{end}}"

// Sections 为按官方顺序排列的官方段模板原文，officialSections 为各段解析后的模板
var Sections, officialSections = parseSections(official)

// SectionNames 为官方原文中的段名顺序
var SectionNames = sectionNames()

// Builtins 为内置三档的模板
var Builtins = map[Tier]Custom{
	TierFull:       {System: join(SectionNames...), User: fullUser},
	TierMedium:     {System: join("identity", "guidelines", "communication_style"), User: mediumUser},
	TierAggressive: {System: join("identity")},
}

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

// Section 为一个官方段的段名与可放进 system 模板的原文，推荐插件段的原文只在有货架正文时输出
type Section struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Preview 为一档以预览数据渲染的系统指令与首轮 user 消息
type Preview struct {
	System string `json:"system"`
	User   string `json:"user"`
}

// systemData 为 system 模板与官方段模板的数据，Separator 为设备系统的路径分隔符，FeaturedPlugins 为推荐插件段的货架正文
type systemData struct {
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

// New 返回档位对应的 Composer，内置三档用 Builtins 的模板，自定义档用 custom 的模板
func New(tier Tier, custom Custom) (*Composer, error) {
	source, ok := Builtins[tier]
	if tier == TierCustom {
		source = custom
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

// Render 以预览数据渲染档位的系统指令与首轮 user 消息
func Render(tier Tier, custom Custom) (Preview, error) {
	c, err := New(tier, custom)
	if err != nil {
		return Preview{}, err
	}
	system, err := execute(c.system, previewData)
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{System: system, User: "<user message>"}
	if c.user != nil {
		if preview.User, err = execute(c.user, userData{Text: preview.User, LocalTime: "2026-10-15T14:00:00+08:00", First: true, ModelDisplayName: previewData.ModelDisplayName}); err != nil {
			return Preview{}, err
		}
	}
	return preview, nil
}

// Tier 返回 Composer 的档位
func (c *Composer) Tier() Tier {
	return c.tier
}

// Compose 以会话 ID、账户设备档案与推荐插件段的货架正文渲染系统指令并包装 user 轮，times[i] 为 contents[i] 首次出现时的本地时间；客户端系统提示中带归属声明的身份句改写为中性身份句，含 RFC 2119 keywords 的行删去，工具声明保持不变；模板执行出错时返回该错误
func (c *Composer) Compose(request antigravity.GenerateRequest, model antigravity.CatalogModel, conversation string, times []string, device antigravity.Device, plugins string) (antigravity.GenerateRequest, error) {
	data := deviceData(model, conversation, device, plugins)
	data.Client = keywordsLine.ReplaceAllString(clientIdentity.ReplaceAllString(request.System, "${1}"+neutralIdentity+"${2}"), "")
	var err error
	if request.System, err = execute(c.system, data); err != nil {
		return request, err
	}
	if c.user != nil {
		request.Contents, err = c.wrap(request.Contents, times, model.DisplayName)
	}
	return request, err
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
	data := deviceData(model, conversation, device, plugins)
	tokens := make(map[string]int, len(officialSections))
	for name, section := range officialSections {
		if name == featuredSection && plugins == "" {
			tokens[name] = 0
			continue
		}
		text, _ := execute(section, data)
		tokens[name] = (len(text) + 2) / 3
	}
	return tokens
}

// validate 以各字段都有值的数据试渲染模板，提前暴露字段名错误
func (c *Composer) validate() error {
	data := systemData{Client: "client", ConversationID: "00000000-0000-0000-0000-000000000000", AppDataDir: "app", Separator: "/", OS: "os", ModelDisplayName: "model", FeaturedPlugins: "plugins"}
	if err := c.system.Execute(io.Discard, data); err != nil {
		return err
	}
	if c.user == nil {
		return nil
	}
	return c.user.Execute(io.Discard, userData{Text: "text", LocalTime: "2026-10-14T14:47:22+08:00", First: true, ModelDisplayName: "model"})
}

// wrap 用 user 模板包装每个含文本且不含工具结果的 user 轮，文本块以换行相连，其余 part 排在其后
func (c *Composer) wrap(contents []antigravity.Content, times []string, model string) ([]antigravity.Content, error) {
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
		text, err := execute(c.user, userData{Text: strings.Join(texts, "\n"), LocalTime: times[index], First: first, ModelDisplayName: model})
		if err != nil {
			return nil, err
		}
		wrapped[index].Parts = append([]antigravity.Part{{Text: text}}, rest...)
		first = false
	}
	return wrapped, nil
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

// parse 解析模板
func parse(name, text string) (*template.Template, error) {
	t, err := template.New(name).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}
	return t, nil
}

// parseSections 把官方原文切成各段的原文与模板，各段以 <段名> 开头、</段名> 结尾、段间以换行相连；推荐插件段的原文包在 FeaturedPlugins 的条件中
func parseSections(text string) ([]Section, map[string]*template.Template) {
	var sections []Section
	parsed := map[string]*template.Template{}
	for text != "" {
		name := text[1:strings.IndexByte(text, '>')]
		end := strings.Index(text, "</"+name+">") + len(name) + 3
		source := text[:end]
		parsed[name] = template.Must(template.New(name).Parse(source))
		if name == featuredSection {
			source = "{{if .FeaturedPlugins}}" + source + "{{end}}"
		}
		sections = append(sections, Section{Name: name, Source: source})
		text = strings.TrimPrefix(text[end:], "\n")
	}
	return sections, parsed
}

// sectionNames 返回 Sections 的段名
func sectionNames() []string {
	names := make([]string, len(Sections))
	for index, section := range Sections {
		names[index] = section.Name
	}
	return names
}

// join 返回按 names 的顺序以换行连接各官方段原文、末尾接客户端系统提示的 system 模板，推荐插件段与它之前的换行只在有货架正文时输出
func join(names ...string) string {
	var out strings.Builder
	for index, name := range names {
		source := Sections[slices.Index(SectionNames, name)].Source
		switch {
		case index == 0:
			out.WriteString(source)
		case name == featuredSection:
			out.WriteString(strings.Replace(source, "}}", "}}\n", 1))
		default:
			out.WriteString("\n" + source)
		}
	}
	return out.String() + clientSuffix
}

// execute 渲染模板
func execute(t *template.Template, data any) (string, error) {
	var out strings.Builder
	err := t.Execute(&out, data)
	return out.String(), err
}
