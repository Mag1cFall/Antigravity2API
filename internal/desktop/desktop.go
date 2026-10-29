//go:build desktop

// Package desktop 用 Wails 无边框窗口与托盘承载管理端
package desktop

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// Options 为桌面应用的运行参数
type Options struct {
	// Title 是应用名、窗口标题与托盘提示
	Title string
	// DataDir 是 WebView 数据目录
	DataDir string
	// Hidden 为 true 时启动后只显示托盘
	Hidden bool
	// Serve 在取得单实例锁后运行服务，直到 ctx 取消；开始监听后以可从本机访问的监听地址（主机与端口）调用 ready
	Serve func(ctx context.Context, ready func(address string)) error
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

// routePrefix 为页面报告当前路径与查询的消息前缀
const routePrefix = "desktop:route:"

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

// trayTemplate 是 macOS 菜单栏的单色模板图标，系统按菜单栏配色着色
//
//go:embed tray-template.png
var trayTemplate []byte

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
	// page 是带桌面宿主参数的管理端地址，服务开始监听时写入
	page := &url.URL{Scheme: "http", RawQuery: url.Values{shellQuery: {"desktop"}}.Encode()}
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return err
	}

	var app *application.App
	// window 为当前主窗口，关闭到托盘后窗口与 WebView 一并销毁，值为 nil
	var window atomic.Pointer[application.WebviewWindow]
	// opening 串行化主窗口的创建
	var opening sync.Mutex
	// placement 为上一个主窗口销毁前的位置、大小、最大化状态与页面路径，重建窗口时沿用
	var placement struct {
		sync.Mutex
		bounds application.Rect
		maxed  bool
		route  string
	}
	webview := true
	var open func() *application.WebviewWindow
	show := func() {
		if !webview {
			openBrowser(app, origin(page))
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
			} else if route, ok := strings.CutPrefix(message, routePrefix); ok && strings.HasPrefix(route, "/") {
				placement.Lock()
				placement.route = route
				placement.Unlock()
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
		slog.Warn("没有找到 WebView2 运行时，管理端在默认浏览器中打开")
		return serve(app, options, func(address string) {
			page.Host = address
			if !options.Hidden {
				app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { show() })
			}
		})
	}
	open = func() *application.WebviewWindow {
		placement.Lock()
		bounds, maxed, route := placement.bounds, placement.maxed, placement.route
		placement.Unlock()
		windowOptions := application.WebviewWindowOptions{
			Title:            options.Title,
			URL:              pageAt(page, route),
			Frameless:        true,
			Width:            1280,
			Height:           800,
			MinWidth:         960,
			MinHeight:        600,
			BackgroundColour: application.NewRGB(0xe6, 0xe7, 0xed),
		}
		if bounds.Width > 0 {
			windowOptions.InitialPosition = application.WindowXY
			windowOptions.X, windowOptions.Y, windowOptions.Width, windowOptions.Height = bounds.X, bounds.Y, bounds.Width, bounds.Height
		}
		created := app.Window.NewWithOptions(windowOptions)
		if maxed {
			created.Maximise()
		}
		window.Store(created)
		created.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) {
			maxed, minimised, bounds := created.IsMaximised(), created.IsMinimised(), created.Bounds()
			placement.Lock()
			placement.maxed = maxed
			if !maxed && !minimised {
				placement.bounds = bounds
			}
			placement.Unlock()
		})
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
	app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { show() })

	addTray(app, options, page, true, show)
	return serve(app, options, func(address string) {
		page.Host = address
		if !options.Hidden {
			open()
		}
	})
}

// serve 在后台运行服务，开始监听后以监听地址调用 ready（先行结束时不调用），再运行应用直到退出；服务先于退出结束时显示其错误并退出应用
func serve(app *application.App, options Options, ready func(address string)) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var served error
	finished := make(chan struct{})
	listening := make(chan string, 1)
	go func() {
		defer close(finished)
		served = options.Serve(ctx, func(address string) { listening <- address })
	}()
	select {
	case address := <-listening:
		ready(address)
	case <-finished:
	}
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

// origin 返回管理端地址的协议与主机部分
func origin(page *url.URL) string {
	return page.Scheme + "://" + page.Host
}

// pageAt 返回管理端在 route（路径与查询）上的地址，查询带桌面宿主参数；route 为空或无法解析时为首页
func pageAt(page *url.URL, route string) string {
	address, err := url.Parse(route)
	if route == "" || err != nil {
		return page.String()
	}
	query := address.Query()
	query.Set(shellQuery, "desktop")
	target := *page
	target.Path, target.RawQuery = address.Path, query.Encode()
	return target.String()
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
