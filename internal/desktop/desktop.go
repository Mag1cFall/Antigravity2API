//go:build desktop

// Package desktop 用 Wails 无边框窗口与托盘承载管理端
package desktop

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Options 为桌面应用的运行参数
type Options struct {
	// URL 是管理端地址，主机部分为服务监听的回环地址与端口
	URL string
	// Title 是应用名、窗口标题与托盘提示
	Title string
	// DataDir 是 WebView 数据目录
	DataDir string
	// Hidden 为 true 时启动后只显示托盘
	Hidden bool
	// Serve 在取得单实例锁后运行服务，直到 ctx 取消
	Serve func(ctx context.Context) error
}

// HiddenFlag 是开机启动附带的启动参数名，cmd 解析到它时设置 Options.Hidden
const HiddenFlag = "hidden"

// uniqueID 是单实例锁与开机启动项的标识
const uniqueID = "com.mag1cfall.antigravity2api"

// shellQuery 是管理端识别桌面宿主的查询参数
const shellQuery = "shell"

// openPrefix 为页面请求用系统浏览器打开链接的消息前缀
const openPrefix = "desktop:open:"

// windowPrefix 为页面顶栏窗口按钮的消息前缀
const windowPrefix = "desktop:window:"

// maximisedEvent 是通知页面窗口最大化状态的事件名
const maximisedEvent = "desktop:maximised"

// closeToTray 表示关闭窗口后应用留在托盘，窗口与 WebView 一并销毁，再次显示时重建；Linux 的托盘依赖桌面环境扩展，关闭窗口即退出
var closeToTray = runtime.GOOS != "linux"

// windowActions 为页面顶栏窗口按钮对应的窗口操作
var windowActions = map[string]func(*application.WebviewWindow){
	"minimise":        func(window *application.WebviewWindow) { window.Minimise() },
	"toggle-maximise": (*application.WebviewWindow).ToggleMaximise,
	"close":           (*application.WebviewWindow).Close,
}

// icon 为托盘与应用图标
//
//go:embed icon.png
var icon []byte

// pageScript 在页面中转交外链与窗口按钮，并实现无边框窗口的拖动与边缘缩放
//
//go:embed page.js
var pageScript string

// loadedEvents 为各平台页面加载完成的窗口事件
var loadedEvents = []events.WindowEventType{
	events.Windows.WebViewNavigationCompleted,
	events.Mac.WebViewDidFinishNavigation,
	events.Linux.WindowLoadFinished,
}

// Run 取得单实例锁后启动服务，服务开始监听时打开窗口与托盘（隐藏启动只创建托盘，显示时创建窗口），阻塞到退出；已有实例时唤起它的窗口并退出本进程；Windows 缺少 WebView2 运行时时只创建托盘，管理端在默认浏览器中打开
func Run(options Options) error {
	page, err := windowURL(options.URL)
	if err != nil {
		return err
	}
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return err
	}

	var app *application.App
	// window 为当前主窗口，关闭到托盘后窗口与 WebView 一并销毁，值为 nil
	var window atomic.Pointer[application.WebviewWindow]
	// opening 串行化主窗口的创建
	var opening sync.Mutex
	webview := true
	var open func() *application.WebviewWindow
	show := func() {
		if !webview {
			openBrowser(app, options.URL)
			return
		}
		opening.Lock()
		current := window.Load()
		if current == nil {
			current = open()
		}
		opening.Unlock()
		current.Show()
		current.UnMinimise()
		current.Focus()
	}
	app = application.New(application.Options{
		Name: options.Title,
		Icon: icon,
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               uniqueID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { show() },
		},
		Windows: application.WindowsOptions{WebviewUserDataPath: dataDir, AdditionalBrowserArgs: []string{"--disable-lcd-text"}, DisableQuitOnLastWindowClosed: closeToTray},
		RawMessageHandler: func(_ application.Window, message string, _ *application.OriginInfo) {
			if target, ok := strings.CutPrefix(message, openPrefix); ok {
				openBrowser(app, target)
			} else if action, ok := strings.CutPrefix(message, windowPrefix); ok && windowActions[action] != nil {
				if current := window.Load(); current != nil {
					windowActions[action](current)
				}
			}
		},
	})

	if runtime.GOOS == "windows" && app.Env.Info().PlatformInfo["WebView2"] == "" {
		webview = false
		addTray(app, options, page, false, show)
		return serve(app, options, page, func(ready bool) {
			slog.Warn("没有找到 WebView2 运行时，管理端在默认浏览器中打开")
			if ready && !options.Hidden {
				app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { show() })
			}
		})
	}
	open = func() *application.WebviewWindow {
		created := app.Window.NewWithOptions(application.WebviewWindowOptions{
			Title:            options.Title,
			URL:              page.String(),
			Frameless:        true,
			Width:            1280,
			Height:           800,
			MinWidth:         960,
			MinHeight:        600,
			BackgroundColour: application.NewRGB(0xe6, 0xe7, 0xed),
		})
		window.Store(created)
		created.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) { window.CompareAndSwap(created, nil) })
		notifyMaximised := func(*application.WindowEvent) {
			created.ExecJS(fmt.Sprintf("window.dispatchEvent(new CustomEvent(%q,{detail:%t}))", maximisedEvent, created.IsMaximised()))
		}
		created.OnWindowEvent(events.Common.WindowDidResize, notifyMaximised)
		for _, loaded := range loadedEvents {
			created.OnWindowEvent(loaded, func(event *application.WindowEvent) {
				markReady(created)
				created.ExecJS(pageScript)
				notifyMaximised(event)
			})
		}
		return created
	}
	if !options.Hidden {
		open()
	}
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { show() })

	addTray(app, options, page, true, show)
	return serve(app, options, page, func(ready bool) {
		if current := window.Load(); !ready && current != nil {
			current.Hide()
		}
	})
}

// serve 在后台运行服务，开始监听或先行结束后以是否在监听调用 ready，再运行应用直到退出；服务先于退出结束时显示其错误并退出应用
func serve(app *application.App, options Options, page *url.URL, ready func(bool)) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var served error
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		served = options.Serve(ctx)
	}()
	ready(waitServing(page.Host, finished))
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go func() {
			<-finished
			if ctx.Err() != nil {
				return
			}
			if served != nil {
				showError(app, served)
			}
			app.Quit()
		}()
	})
	err := app.Run()
	cancel()
	<-finished
	return errors.Join(err, served)
}

// windowURL 返回带桌面宿主查询参数的窗口地址
func windowURL(raw string) (*url.URL, error) {
	page, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	query := page.Query()
	query.Set(shellQuery, "desktop")
	page.RawQuery = query.Encode()
	return page, nil
}

// waitServing 等到 address 接受 TCP 连接时返回 true，服务先结束时返回 false
func waitServing(address string, finished <-chan struct{}) bool {
	for {
		if conn, err := net.Dial("tcp", address); err == nil {
			conn.Close()
			return true
		}
		select {
		case <-finished:
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// enableAutostart 登记开机启动；能隐藏到托盘的平台启动后只显示托盘
func enableAutostart(app *application.App) error {
	options := application.AutostartOptions{Identifier: uniqueID}
	if closeToTray {
		options.Arguments = []string{"--" + HiddenFlag}
	}
	return app.Autostart.EnableWithOptions(options)
}

// markReady 让窗口立即执行 ExecJS，外部页面不会自行发送 wails:runtime:ready
func markReady(window *application.WebviewWindow) {
	window.HandleMessage("wails:runtime:ready")
}

// openBrowser 用系统浏览器打开 http 与 https 链接
func openBrowser(app *application.App, target string) {
	link, err := url.Parse(target)
	if err != nil || (link.Scheme != "http" && link.Scheme != "https") {
		return
	}
	if err := app.Browser.OpenURL(link.String()); err != nil {
		showError(app, err)
	}
}

// showError 用原生对话框显示错误，关闭对话框后返回
func showError(app *application.App, err error) {
	app.Dialog.Error().SetTitle(app.Config().Name).SetMessage(err.Error()).Show()
}
