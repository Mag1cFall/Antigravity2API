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
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// quotaInterval 是托盘额度摘要的刷新间隔
const quotaInterval = time.Minute

// quotaGroups 是额度组及其在托盘中的名称，与管理端一致
var quotaGroups = [][2]string{{"gemini", "Gemini"}, {"3p", "Claude 与 GPT"}}

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

// addTray 创建托盘图标，单击图标在新的 goroutine 中调用 show；菜单依次为各额度组剩余、各账户额度、刷新额度、显示窗口（window 为真时）、在浏览器中打开、复制 API 地址、开机启动与退出；应用启动后每分钟与刷新额度后读取额度并重建菜单
func addTray(app *application.App, options Options, page *url.URL, window bool, show func()) {
	tray := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(trayTemplate)
	} else {
		tray.SetIcon(icon)
	}
	tray.SetTooltip(options.Title)
	reread := make(chan struct{}, 1)
	build := func(accounts []trayAccount) *application.Menu {
		menu := app.NewMenu()
		addQuota(menu, accounts)
		menu.Add("刷新额度").OnClick(func(*application.Context) {
			go func() {
				if response, err := http.Post(origin(page)+"/api/accounts/refresh", "application/json", nil); err == nil {
					response.Body.Close()
				}
				select {
				case reread <- struct{}{}:
				default:
				}
			}()
		})
		menu.AddSeparator()
		if window {
			menu.Add("显示窗口").OnClick(func(*application.Context) { show() })
		}
		menu.Add("在浏览器中打开").OnClick(func(*application.Context) { openBrowser(app, origin(page)) })
		menu.Add("复制 API 地址").OnClick(func(*application.Context) {
			app.Clipboard.SetText(origin(page))
		})
		menu.AddSeparator()
		enabled, _ := app.Autostart.IsEnabled()
		menu.AddCheckbox("开机启动", enabled).OnClick(func(click *application.Context) {
			toggle := app.Autostart.Disable
			if click.IsChecked() {
				toggle = func() error { return enableAutostart(app) }
			}
			if err := toggle(); err != nil {
				click.ClickedMenuItem().SetChecked(!click.IsChecked())
				showError(app, err)
			}
		})
		menu.AddSeparator()
		menu.Add("退出").OnClick(func(*application.Context) { app.Quit() })
		return menu
	}
	tray.SetMenu(build(nil))
	tray.OnClick(func() { go show() })
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			client := &http.Client{Timeout: 10 * time.Second}
			for {
				if accounts, err := readAccounts(client, origin(page)); err == nil {
					tray.SetMenu(build(accounts))
				}
				select {
				case <-time.After(quotaInterval):
				case <-reread:
				}
			}
		}()
	})
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

// addQuota 在菜单中加入各额度组在已启用账户上的平均可用比例（同额度池的账户只计一次）与各账户额度子菜单，没有账户时不加子菜单
func addQuota(menu *application.Menu, accounts []trayAccount) {
	for _, group := range quotaGroups {
		total, count := 0.0, 0
		counted := map[string]bool{}
		for _, account := range accounts {
			fraction, ok := account.available(group[0])
			if account.State == "disabled" || !ok || slices.ContainsFunc(account.Shared[group[0]], func(peer string) bool { return counted[peer] }) {
				continue
			}
			counted[account.ID] = true
			total, count = total+fraction, count+1
		}
		text := "—"
		if count > 0 {
			text = percent(total / float64(count))
		}
		menu.Add(group[1] + " 剩余 " + text).SetEnabled(false)
	}
	if len(accounts) == 0 {
		return
	}
	submenu := menu.AddSubmenu("各账户额度")
	for _, account := range accounts {
		name := account.ID
		if account.Label != "" {
			name = account.Label
		}
		var parts []string
		for _, group := range quotaGroups {
			text := "—"
			if fraction, ok := account.available(group[0]); ok {
				text = percent(fraction)
			}
			parts = append(parts, group[1]+" "+text)
		}
		detail := strings.Join(parts, " · ")
		if account.State == "disabled" {
			detail = "已停用"
		}
		submenu.Add(name + "  " + detail).SetEnabled(false)
	}
}

// available 返回账户在一个额度组的可用比例，即 5 小时与每周中较低的一项，没有该组额度时 ok 为 false
func (a trayAccount) available(group string) (float64, bool) {
	fraction, ok := 1.0, false
	for _, bucket := range a.Quota {
		if prefix, _, _ := strings.Cut(bucket.BucketID, "-"); prefix == group {
			fraction, ok = min(fraction, bucket.RemainingFraction), true
		}
	}
	return fraction, ok
}

// percent 把比例写成向下取整的百分数
func percent(fraction float64) string {
	return fmt.Sprintf("%.0f%%", math.Floor(fraction*100))
}
