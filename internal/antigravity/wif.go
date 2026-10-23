package antigravity

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

const (
	// EndpointBusiness 为企业 WIF 账户的 cloudcode 端点
	EndpointBusiness = "https://businessaicode.googleapis.com"
	// wifAuthorizeURL 为企业 WIF 登录的授权地址
	wifAuthorizeURL = "https://auth.cloud.google/authorize"
	// wifTokenURL 为企业 WIF 登录换取 token 的 STS 地址
	wifTokenURL = "https://sts.googleapis.com/v1/oauthtoken"
	// AuthMethodConsumer 为个人账户的认证方式
	AuthMethodConsumer = "personal_consumer"
	// AuthMethodEnterprise 为企业许可证账户的认证方式
	AuthMethodEnterprise = "business_licensed_enterprise"
)

// OAuthConfigWIF 为官方客户端“Continue with Google Cloud”企业 WIF 登录使用的 OAuth 客户端，经 auth.cloud.google 授权、STS 换取 token，无客户端密钥
var OAuthConfigWIF = oauth2.Config{
	ClientID: "a2a6f0e3e-3ff0-462b-869a-47277df4c806",
	Endpoint: oauth2.Endpoint{
		AuthURL:   wifAuthorizeURL,
		TokenURL:  wifTokenURL,
		AuthStyle: oauth2.AuthStyleInParams,
	},
	Scopes: []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"openid",
		"email",
		"profile",
		"offline_access",
	},
}

// WIFLogin 为企业 WIF 登录的结果：STS token 与 provider 名
type WIFLogin struct {
	Token    *oauth2.Token
	Provider string
}

// LoginWIF 在本机回环端口等待 auth.cloud.google 的授权回调，open 负责打开授权链接，授权码经 STS 换取 token
func LoginWIF(ctx context.Context, provider string, proxy Proxy, open func(url string) error) (*WIFLogin, error) {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return nil, err
	}
	defer listener.Close()
	config := OAuthConfigWIF
	config.RedirectURL = fmt.Sprintf("http://localhost:%d/auth/callback", listener.Addr().(*net.TCPAddr).Port)
	verifier := oauth2.GenerateVerifier()
	state := randomState()
	callbacks := make(chan callback, 1)
	server := &http.Server{Handler: callbackHandler(state, callbacks)}
	go server.Serve(listener)
	defer server.Close()
	link := config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("provider", provider))
	if err := open(link); err != nil {
		return nil, err
	}
	select {
	case result := <-callbacks:
		if result.err != nil {
			return nil, result.err
		}
		token, err := config.Exchange(exchangeContext(ctx, proxy), result.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return nil, err
		}
		return &WIFLogin{Token: token, Provider: provider}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// onboardRequest 为 onboardUser 的请求体
type onboardRequest struct {
	TierID                  string         `json:"tierId"`
	CloudAICompanionProject string         `json:"cloudaicompanionProject,omitempty"`
	Metadata                assistMetadata `json:"metadata"`
}

// operation 为长操作响应，done 为真时取 response 中的项目
type operation struct {
	Name     string `json:"name"`
	Done     bool   `json:"done"`
	Response struct {
		CloudAICompanionProject string `json:"cloudaicompanionProject"`
	} `json:"response"`
}

// OnboardUser 向 businessaicode 发出 onboardUser 并轮询长操作直到完成，返回开通的项目
func (c *Client) OnboardUser(ctx context.Context, tier string) (string, error) {
	var op operation
	if err := c.post(ctx, c.base+"/v1internal:onboardUser", onboardRequest{TierID: tier, Metadata: assistMetadata{IDEType: ideType}}, &op); err != nil {
		return "", err
	}
	for !op.Done {
		if op.Name == "" {
			return "", errors.New("onboardUser 长操作缺少名称")
		}
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
			return "", ctx.Err()
		}
		name := op.Name
		op = operation{}
		if err := c.get(ctx, c.base+"/v1internal/"+name, &op); err != nil {
			return "", err
		}
	}
	project := op.Response.CloudAICompanionProject
	if project == "" {
		return "", errors.New("onboardUser 完成后没有项目")
	}
	c.mu.Lock()
	c.project = project
	c.mu.Unlock()
	return project, nil
}

// BusinessAssist 按企业 WIF 账户的序列读取套餐与项目：prod loadCodeAssist，此后 cloudcode 请求走 businessaicode
func (c *Client) BusinessAssist(ctx context.Context) (LoadCodeAssistResponse, error) {
	assist, err := c.LoadCodeAssist(ctx, EndpointProd)
	if err != nil {
		return LoadCodeAssistResponse{}, err
	}
	c.base = EndpointBusiness
	return assist, nil
}

// BusinessInitialize 按企业 WIF 账户的登录后序列初始化：BusinessAssist 判定套餐，未开通时 onboardUser，随后目录走 businessaicode；businessaicode 请求携带 X-Goog-User-Project
func (c *Client) BusinessInitialize(ctx context.Context) (Initialization, error) {
	var result Initialization
	assist, err := c.BusinessAssist(ctx)
	if err != nil {
		return Initialization{}, err
	}
	if assist.CloudAICompanionProject == "" {
		tier := assist.CurrentTier.ID
		if assist.PaidTier != nil {
			tier = assist.PaidTier.ID
		}
		if _, err := c.OnboardUser(ctx, tier); err != nil {
			return Initialization{}, err
		}
		if assist, err = c.LoadCodeAssist(ctx, EndpointBusiness); err != nil {
			return Initialization{}, err
		}
	}
	result.Assist = assist
	if result.User, err = c.Userinfo(ctx); err != nil {
		return Initialization{}, err
	}
	if result.Catalog, err = c.FetchAvailableModels(ctx); err != nil {
		return Initialization{}, err
	}
	c.recordCatalog(result.Catalog)
	return result, nil
}

// userProject 为发往 businessaicode 的请求设置 X-Goog-User-Project
func (c *Client) userProject(request *http.Request) {
	c.mu.Lock()
	project := c.project
	c.mu.Unlock()
	if project != "" && request.URL.Host == "businessaicode.googleapis.com" {
		request.Header.Set("X-Goog-User-Project", project)
	}
}
