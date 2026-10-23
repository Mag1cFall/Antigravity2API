// Package main 是 antigravity2api 的命令行入口：serve 运行服务，login、import、accounts、status 管理账户
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/oauth2"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/app"
	"github.com/Mag1cFall/Antigravity2API/internal/auth"
	"github.com/Mag1cFall/Antigravity2API/internal/config"
	"github.com/Mag1cFall/Antigravity2API/internal/logging"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
	"github.com/Mag1cFall/Antigravity2API/internal/service"
	"github.com/Mag1cFall/Antigravity2API/internal/webui"
)

// title 是应用名、窗口标题与桌面版数据目录名
const title = "Antigravity2API"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// dispatch 执行子命令，首个参数不是子命令时返回 false
func dispatch(ctx context.Context, args []string) (bool, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false, nil
	}
	switch args[0] {
	case "serve":
		return true, serveCommand(ctx, args[1:], false)
	case "login":
		return true, loginCommand(ctx, args[1:])
	case "import":
		return true, importCommand(ctx, args[1:])
	case "switch":
		return true, switchCommand(args[1:])
	case "accounts":
		return true, accountsCommand(args[1:])
	case "status":
		return true, statusCommand(ctx, args[1:])
	case "version":
		fmt.Println(app.Version())
		return true, nil
	}
	return true, fmt.Errorf("未知子命令 %q，可用：serve、login、import、switch、accounts、status、version", args[0])
}

// options 是各子命令共用的启动参数
type options struct {
	overrides config.Overrides
}

// bind 在 flags 上注册 --listen、--auth、--runtime 与 --proxy
func (o *options) bind(flags *flag.FlagSet) {
	o.overrides.BindFlags(flags)
}

// load 读取当前目录的 .env 并应用命令行覆盖
func (o options) load() (config.Config, error) {
	cfg, err := config.Load(config.File)
	if err != nil {
		return config.Config{}, err
	}
	if err := config.ValidateProxies(o.overrides.Proxy); err != nil {
		return config.Config{}, err
	}
	o.overrides.Apply(&cfg)
	return cfg, nil
}

// liveOf 把配置放进运行期可替换的指针
func liveOf(cfg config.Config) *atomic.Pointer[config.Config] {
	live := new(atomic.Pointer[config.Config])
	live.Store(&cfg)
	return live
}

// proxyOf 返回按当前配置选择代理的函数
func proxyOf(live *atomic.Pointer[config.Config]) antigravity.Proxy {
	return func(r *http.Request) (*url.URL, error) {
		return live.Load().ProxyURL(r)
	}
}

// serveCommand 解析 serve 的参数并运行服务，openDefault 为 --open-ui 的默认值；在线更新完成后以原参数启动新版本
func serveCommand(ctx context.Context, args []string, openDefault bool) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	var o options
	o.bind(flags)
	openUI := flags.Bool("open-ui", openDefault, "启动后用系统浏览器打开管理端，不带任何参数启动时默认打开")
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := o.load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var updated atomic.Bool
	err = serve(ctx, "web", cfg, o.overrides, *openUI, func() { updated.Store(true); cancel() })
	if !updated.Load() {
		return err
	}
	return relaunch(args)
}

// relaunch 以 serve 子命令与原参数启动替换后的可执行文件且不再打开管理端：Windows 上新进程沿用当前控制台，其他系统原地替换进程
func relaunch(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args = append(append([]string{"serve"}, args...), "--open-ui=false")
	if runtime.GOOS == "windows" {
		command := exec.Command(executable, args...)
		command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
		return command.Start()
	}
	return syscall.Exec(executable, append([]string{executable}, args...), os.Environ())
}

// serve 装配日志、账本、账户库与服务，在 cfg.Listen 上运行到 ctx 结束，restart 在在线更新安装完成后调用
func serve(ctx context.Context, host string, cfg config.Config, overrides config.Overrides, openUI bool, restart func()) error {
	live := liveOf(cfg)
	logger, err := logging.New(filepath.Join(cfg.RuntimeDir, "logs"), cfg.LogLevel, cfg.LogFormat, cfg.LogFile, cfg.LogMaxTotalMB)
	if err != nil {
		return err
	}
	defer logger.Close()
	ledger, err := requestdb.Open(filepath.Join(cfg.RuntimeDir, "requests.db"))
	if err != nil {
		return err
	}
	defer ledger.Close()
	store, err := auth.Open(cfg.AuthDir, proxyOf(live))
	if err != nil {
		return err
	}
	if len(store.Accounts()) == 0 {
		fmt.Fprintln(os.Stderr, "没有账户，执行 antigravity2api login 添加，或在管理端添加")
	}
	svc, err := service.New(store, service.Options{Live: live, Ledger: ledger})
	if err != nil {
		return err
	}
	if openUI {
		go openWhenReady(ctx, cfg.Listen)
	}
	return app.Serve(ctx, live, app.Deps{Service: svc, Store: store, Ledger: ledger, Logger: logger, Overrides: overrides, Host: host, UI: webui.Handler(), Restart: restart})
}

// openWhenReady 等管理 API 可访问后用系统浏览器打开管理端
func openWhenReady(ctx context.Context, listen string) {
	base := "http://" + loopback(listen)
	for ctx.Err() == nil {
		response, err := http.Get(base + "/api/status")
		if err == nil {
			response.Body.Close()
			if err := openBrowser(base + "/"); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// loginCommand 完成 Google 登录并把账户加入本地账户库或远程服务
func loginCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	var o options
	o.bind(flags)
	code := flags.Bool("code", false, "打印授权链接并粘贴授权码，用于没有浏览器的环境")
	cloud := flags.Bool("cloud", false, "以 Continue with Google Cloud 的企业 WIF 方式登录")
	provider := flags.String("provider", "", "企业 WIF 登录的 provider 名")
	remote := flags.String("remote", "", "把账户添加到远程服务，值为其管理端地址，例如 http://server:8017")
	token := flags.String("token", "", "远程服务管理端生成的配对令牌")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *cloud && (*remote != "" || *code) {
		return errors.New("--cloud 在本机以浏览器登录并写入本机账户库，不能与 --remote、--code 同时使用")
	}
	if *remote != "" && *token == "" {
		return errors.New("--remote 需要同时提供 --token")
	}
	cfg, err := o.load()
	if err != nil {
		return err
	}
	proxy := proxyOf(liveOf(cfg))
	device := antigravity.NewLoginDevice()
	if *cloud {
		login, err := antigravity.LoginWIF(ctx, *provider, proxy, func(link string) error {
			fmt.Println("在浏览器中完成 Continue with Google Cloud 登录：")
			fmt.Println(link)
			return openBrowser(link)
		})
		if err != nil {
			return err
		}
		store, err := auth.Open(cfg.AuthDir, proxy)
		if err != nil {
			return err
		}
		account, err := store.AddWIF(ctx, login, device)
		if err != nil {
			return err
		}
		fmt.Printf("已添加企业账户 %s\n", account.Email)
		return nil
	}
	prelogin, stop := context.WithCancel(ctx)
	defer stop()
	go antigravity.NewPrelogin(device, proxy).Run(prelogin)
	var authorized *oauth2.Token
	if *code {
		authorized, err = antigravity.LoginTerminal(ctx, proxy, promptCode)
	} else {
		authorized, err = antigravity.LoginBrowser(ctx, proxy, func(link string) error {
			fmt.Println("在浏览器中完成 Google 登录：")
			fmt.Println(link)
			return openBrowser(link)
		})
	}
	stop()
	if err != nil {
		return err
	}
	if *remote != "" {
		return pair(ctx, *remote, *token, authorized)
	}
	store, err := auth.Open(cfg.AuthDir, proxy)
	if err != nil {
		return err
	}
	account, err := store.AddWithDevice(ctx, authorized, device)
	if err != nil {
		return err
	}
	fmt.Printf("已添加账户 %s\n", account.Email)
	return nil
}

// promptCode 打印授权链接并读取用户粘贴的授权码
func promptCode(link string) (string, error) {
	fmt.Println("在任意设备的浏览器中打开链接完成 Google 登录，把页面给出的授权码粘贴到这里：")
	fmt.Println(link)
	fmt.Print("授权码: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// pair 把登录得到的 token 经配对令牌提交给远程服务
func pair(ctx context.Context, remote, token string, authorized *oauth2.Token) error {
	body, err := json.Marshal(authorized)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(remote, "/")+"/api/pairing/accounts", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("远程服务返回 %d: %s", response.StatusCode, data)
	}
	var result struct {
		Account string `json:"account"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return err
	}
	fmt.Printf("已添加账户 %s 到 %s\n", result.Account, remote)
	return nil
}

// importCommand 从本机官方客户端导入账户
func importCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	var o options
	o.bind(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := o.load()
	if err != nil {
		return err
	}
	store, err := auth.Open(cfg.AuthDir, proxyOf(liveOf(cfg)))
	if err != nil {
		return err
	}
	accounts, err := auth.ImportOfficial(ctx, store)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		fmt.Printf("已导入账户 %s\n", account.Email)
	}
	return nil
}

// switchCommand 把账户写入本机官方客户端并重新启动官方客户端
func switchCommand(args []string) error {
	flags := flag.NewFlagSet("switch", flag.ContinueOnError)
	var o options
	o.bind(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("用法: antigravity2api switch [参数] <账户邮箱>")
	}
	cfg, err := o.load()
	if err != nil {
		return err
	}
	store, err := auth.Open(cfg.AuthDir, proxyOf(liveOf(cfg)))
	if err != nil {
		return err
	}
	if err := auth.SwitchOfficial(store, flags.Arg(0)); err != nil {
		return err
	}
	fmt.Printf("官方客户端已切换到账户 %s\n", flags.Arg(0))
	return nil
}

// accountsCommand 列出本地账户库中的账户
func accountsCommand(args []string) error {
	flags := flag.NewFlagSet("accounts", flag.ContinueOnError)
	var o options
	o.bind(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := o.load()
	if err != nil {
		return err
	}
	store, err := auth.Open(cfg.AuthDir, proxyOf(liveOf(cfg)))
	if err != nil {
		return err
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "账户\t套餐\t状态\t添加时间")
	for _, account := range store.Accounts() {
		state := "启用"
		if !account.Enabled {
			state = "停用"
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", account.Email, account.Tier, state, account.AddedAt.Local().Format(time.DateTime))
	}
	return writer.Flush()
}

// statusCommand 读取运行中服务的状态与账户额度
func statusCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	var o options
	o.bind(flags)
	if err := flags.Parse(args); err != nil {
		return err
	}
	cfg, err := o.load()
	if err != nil {
		return err
	}
	base := "http://" + loopback(cfg.Listen)
	var status struct {
		Host      string    `json:"host"`
		Version   string    `json:"version"`
		Listen    string    `json:"listen"`
		StartedAt time.Time `json:"started_at"`
	}
	if err := getJSON(ctx, base+"/api/status", &status); err != nil {
		return fmt.Errorf("服务未在 %s 运行: %w", cfg.Listen, err)
	}
	fmt.Printf("%s %s 监听 %s，自 %s 运行\n", title, status.Version, status.Listen, status.StartedAt.Local().Format(time.DateTime))
	var accounts struct {
		Accounts []struct {
			ID    string `json:"id"`
			State string `json:"state"`
			Tier  string `json:"tier"`
			Error string `json:"error"`
			Quota []struct {
				BucketID          string  `json:"bucket_id"`
				Window            string  `json:"window"`
				RemainingFraction float64 `json:"remaining_fraction"`
				ResetTime         string  `json:"reset_time"`
			} `json:"quota"`
		} `json:"accounts"`
	}
	if err := getJSON(ctx, base+"/api/accounts", &accounts); err != nil {
		return err
	}
	writer := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "账户\t状态\t套餐\t额度")
	for _, account := range accounts.Accounts {
		var quota []string
		for _, bucket := range account.Quota {
			quota = append(quota, fmt.Sprintf("%s %.0f%%", bucket.BucketID, bucket.RemainingFraction*100))
		}
		detail := strings.Join(quota, " ")
		if account.Error != "" {
			detail = account.Error
		}
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", account.ID, account.State, account.Tier, detail)
	}
	return writer.Flush()
}

// getJSON 读取管理 API 的 JSON 响应
func getJSON(ctx context.Context, target string, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		return fmt.Errorf("%s 返回 %d: %s", target, response.StatusCode, data)
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// loopback 把监听地址换成本机可访问的地址
func loopback(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listen
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
