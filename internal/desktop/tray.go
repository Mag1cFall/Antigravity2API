//go:build desktop

package desktop

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/i18n"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// quotaInterval 是托盘额度摘要的刷新间隔
const quotaInterval = time.Minute

// localePrefix 为页面报告界面语言的消息前缀
const localePrefix = "desktop:locale:"

// themePrefix 为页面报告界面配色（dark 或 light）的消息前缀
const themePrefix = "desktop:theme:"

// quotaGroups 是额度组，Argon 组只在有账户带该组额度时显示
var quotaGroups = []string{"gemini", "gemini-argon-wtu", "3p"}

// uiLocale 是托盘菜单与对话框所用的界面语言：页面加载前为系统界面语言，之后跟随页面
var uiLocale atomic.Value

// locale 返回托盘菜单与对话框当前所用的界面语言
func locale() string {
	value, _ := uiLocale.Load().(string)
	return value
}

// trayAccount 是托盘从 GET /api/accounts 读取的账户字段
type trayAccount struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"`
	Quota []struct {
		BucketID          string  `json:"bucket_id"`
		RemainingFraction float64 `json:"remaining_fraction"`
	} `json:"quota"`
	Shared map[string][]string `json:"shared"`
}

// tray 是托盘图标与它的菜单，reread 让菜单立即按最新账户与界面语言重建，dark 为页面报告的界面配色是否为深色
type tray struct {
	app    *application.App
	item   *application.SystemTray
	reread chan struct{}
	dark   atomic.Pointer[bool]
	// latest 是最近一次构建的菜单项，Windows 自绘菜单弹出时读取
	latest atomic.Pointer[[]trayEntry]
}

// trayEntry 是托盘菜单的一项：label 为空时为分隔线，run 为空时为不可点击的说明行，sub 非空时为子菜单，checkable 为真时按 checked 显示勾选
type trayEntry struct {
	label              string
	run                func()
	checkable, checked bool
	sub                []trayEntry
}

// addTray 创建托盘图标并按平台惯例设置单击、双击与右键（见 attach）；菜单依次为各额度组剩余、各账户额度、刷新额度、显示窗口（window 为真时）、在浏览器中打开、复制 API 地址、开机启动与退出，文字按界面语言；应用启动后每分钟、刷新额度后与界面语言变化时读取额度并重建菜单
func addTray(app *application.App, options Options, page *url.URL, window bool, show func()) *tray {
	uiLocale.Store(i18n.SystemLocale())
	item := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		item.SetTemplateIcon(trayTemplate)
	} else {
		item.SetIcon(icon)
	}
	item.SetTooltip(options.Title)
	current := &tray{app: app, item: item, reread: make(chan struct{}, 1)}
	build := func(accounts []trayAccount) []trayEntry {
		text := trayTexts[locale()]
		entries := quotaEntries(text, accounts)
		entries = append(entries, trayEntry{label: text.refresh, run: func() {
			if response, err := http.Post(origin(page)+"/api/accounts/refresh", "application/json", nil); err == nil {
				response.Body.Close()
			}
			current.rebuild()
		}}, trayEntry{})
		if window {
			entries = append(entries, trayEntry{label: text.show, run: show})
		}
		enabled, _ := app.Autostart.IsEnabled()
		return append(entries,
			trayEntry{label: text.browser, run: func() { openBrowser(app, origin(page)) }},
			trayEntry{label: text.copy, run: func() { app.Clipboard.SetText(origin(page)) }},
			trayEntry{},
			trayEntry{label: text.autostart, checkable: true, checked: enabled, run: func() {
				toggle := app.Autostart.Disable
				if !enabled {
					toggle = func() error { return enableAutostart(app) }
				}
				if err := toggle(); err != nil {
					showError(app, err)
				}
				current.rebuild()
			}},
			trayEntry{},
			trayEntry{label: text.quit, run: app.Quit},
		)
	}
	current.update(build(nil))
	current.attach(show)
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			client := &http.Client{Timeout: 10 * time.Second}
			for {
				if accounts, err := readAccounts(client, origin(page)); err == nil {
					current.update(build(accounts))
				}
				select {
				case <-time.After(quotaInterval):
				case <-current.reread:
				}
			}
		}()
	})
	return current
}

// rebuild 让菜单立即重新读取额度并重建
func (t *tray) rebuild() {
	select {
	case t.reread <- struct{}{}:
	default:
	}
}

// handle 处理页面报告的界面语言与配色：语言变化时按新语言重建菜单，配色决定之后弹出的菜单深浅；不是这两种消息时返回 false
func (t *tray) handle(message string) bool {
	if value, ok := strings.CutPrefix(message, localePrefix); ok {
		if _, known := trayTexts[value]; known && value != locale() {
			uiLocale.Store(value)
			t.rebuild()
		}
		return true
	}
	if value, ok := strings.CutPrefix(message, themePrefix); ok {
		dark := value == "dark"
		t.dark.Store(&dark)
		return true
	}
	return false
}

// readAccounts 经回环地址读取管理 API 的账户列表
func readAccounts(client *http.Client, base string) ([]trayAccount, error) {
	response, err := client.Get(base + "/api/accounts")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /api/accounts: %s", response.Status)
	}
	var body struct {
		Accounts []trayAccount `json:"accounts"`
	}
	err = json.NewDecoder(response.Body).Decode(&body)
	return body.Accounts, err
}

// quotaEntries 返回各额度组在已启用账户上的平均可用比例（同额度池的账户只计一次）与各账户额度子菜单，没有账户时没有子菜单
func quotaEntries(text trayText, accounts []trayAccount) []trayEntry {
	var entries []trayEntry
	for _, group := range quotaGroups {
		total, count := 0.0, 0
		counted := map[string]bool{}
		for _, account := range accounts {
			fraction, ok := account.available(group)
			if account.State == "disabled" || !ok || slices.ContainsFunc(account.Shared[group], func(peer string) bool { return counted[peer] }) {
				continue
			}
			counted[account.ID] = true
			total, count = total+fraction, count+1
		}
		value := "—"
		if count > 0 {
			value = percent(total / float64(count))
		} else if group == "gemini-argon-wtu" {
			continue
		}
		entries = append(entries, trayEntry{label: text.groups[group] + " " + strings.Replace(text.remaining, "{percent}", value, 1)})
	}
	if len(accounts) == 0 {
		return entries
	}
	var sub []trayEntry
	for _, account := range accounts {
		name := account.ID
		if account.Label != "" {
			name = account.Label
		}
		var parts []string
		for _, group := range quotaGroups {
			value := "—"
			if fraction, ok := account.available(group); ok {
				value = percent(fraction)
			} else if group == "gemini-argon-wtu" {
				continue
			}
			parts = append(parts, text.groups[group]+" "+value)
		}
		detail := strings.Join(parts, " · ")
		if account.State == "disabled" {
			detail = text.disabled
		}
		sub = append(sub, trayEntry{label: name + "  " + detail})
	}
	return append(entries, trayEntry{label: text.perAccount, sub: sub})
}

// available 返回账户在一个额度组的可用比例，即 5 小时与每周中较低的一项，没有该组额度时 ok 为 false
func (a trayAccount) available(group string) (float64, bool) {
	fraction, ok := 1.0, false
	for _, bucket := range a.Quota {
		if antigravity.BucketGroup(bucket.BucketID) == group {
			fraction, ok = min(fraction, bucket.RemainingFraction), true
		}
	}
	return fraction, ok
}

// percent 把比例写成向下取整的百分数
func percent(fraction float64) string {
	return fmt.Sprintf("%.0f%%", math.Floor(fraction*100))
}

// trayText 是托盘菜单在一种界面语言下的文字，额度组名称与“剩余”“已停用”“刷新额度”与管理端同语言的用词相同
type trayText struct {
	groups                                   map[string]string
	remaining, perAccount, disabled, refresh string
	show, browser, copy, autostart, quit     string
}

// trayTexts 是各界面语言的托盘菜单文字
var trayTexts = map[string]trayText{
	"zh-CN": {
		groups:    map[string]string{"gemini": "Gemini", "gemini-argon-wtu": "Gemini Argon", "3p": "Claude 与 GPT"},
		remaining: "剩余 {percent}", perAccount: "各账户额度", disabled: "已停用", refresh: "刷新额度",
		show: "显示窗口", browser: "在浏览器中打开", copy: "复制 API 地址", autostart: "开机启动", quit: "退出",
	},
	"zh-TW": {
		groups:    map[string]string{"gemini": "Gemini", "gemini-argon-wtu": "Gemini Argon", "3p": "Claude 與 GPT"},
		remaining: "剩餘 {percent}", perAccount: "各帳號額度", disabled: "已停用", refresh: "重新整理額度",
		show: "顯示視窗", browser: "在瀏覽器中開啟", copy: "複製 API 位址", autostart: "開機時啟動", quit: "結束",
	},
	"en": {
		groups:    map[string]string{"gemini": "Gemini", "gemini-argon-wtu": "Gemini Argon", "3p": "Claude & GPT"},
		remaining: "{percent} left", perAccount: "Quota by Account", disabled: "Disabled", refresh: "Refresh Quota",
		show: "Show Window", browser: "Open in Browser", copy: "Copy API Address", autostart: "Start at Login", quit: "Quit",
	},
	"ja": {
		groups:    map[string]string{"gemini": "Gemini", "gemini-argon-wtu": "Gemini Argon", "3p": "Claude と GPT"},
		remaining: "残り {percent}", perAccount: "アカウント別クォータ", disabled: "無効", refresh: "クォータを更新",
		show: "ウィンドウを表示", browser: "ブラウザで開く", copy: "API アドレスをコピー", autostart: "ログイン時に起動", quit: "終了",
	},
	"ko": {
		groups:    map[string]string{"gemini": "Gemini", "gemini-argon-wtu": "Gemini Argon", "3p": "Claude 및 GPT"},
		remaining: "{percent} 남음", perAccount: "계정별 할당량", disabled: "사용 중지됨", refresh: "할당량 새로고침",
		show: "창 표시", browser: "브라우저에서 열기", copy: "API 주소 복사", autostart: "로그인 시 실행", quit: "종료",
	},
	"fr": {
		groups:    map[string]string{"gemini": "Gemini", "gemini-argon-wtu": "Gemini Argon", "3p": "Claude et GPT"},
		remaining: "{percent} restants", perAccount: "Quota par compte", disabled: "Désactivé", refresh: "Actualiser le quota",
		show: "Afficher la fenêtre", browser: "Ouvrir dans le navigateur", copy: "Copier l’adresse de l’API", autostart: "Lancer à l’ouverture de session", quit: "Quitter",
	},
	"de": {
		groups:    map[string]string{"gemini": "Gemini", "gemini-argon-wtu": "Gemini Argon", "3p": "Claude & GPT"},
		remaining: "{percent} übrig", perAccount: "Kontingent pro Konto", disabled: "Deaktiviert", refresh: "Kontingent aktualisieren",
		show: "Fenster anzeigen", browser: "Im Browser öffnen", copy: "API-Adresse kopieren", autostart: "Bei Anmeldung starten", quit: "Beenden",
	},
}
