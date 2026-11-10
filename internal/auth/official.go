package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

const (
	// officialService 为官方客户端在系统凭据库中的服务名
	officialService = "gemini"
	// officialUser 为官方客户端在系统凭据库中的用户名
	officialUser = "antigravity"
	// officialFile 为系统凭据库不可用时官方客户端在 ~/.gemini 下保存凭据的文件名
	officialFile = "jetski-standalone-oauth-token"
	// officialMarker 为 ~/.gemini/cache 下的标记文件，修改时间在 1 小时内时官方客户端改用文件保存凭据
	officialMarker = "antigravity-keyring-unavailable"
	// cliFile 为官方 agy CLI 在系统凭据库不可用时保存凭据的会话文件，相对 ~/.gemini
	cliFile = "antigravity-cli/antigravity-oauth-token"
	// officialConsumer 为官方客户端凭据中个人账户的 auth_method
	officialConsumer = "consumer"
	// officialCloud 为官方客户端凭据中“Continue with Google Cloud”登录的 auth_method
	officialCloud = "gcp"
)

// officialCredential 表示官方客户端保存的凭据 JSON，字段与其 StoredToken 相同；企业账户的项目、地区与许可证由官方客户端选择许可证后写入
type officialCredential struct {
	Token            oauth2.Token `json:"token"`
	AuthMethod       string       `json:"auth_method,omitempty"`
	IDTokenEmail     string       `json:"id_token_email,omitempty"`
	WIFProvider      string       `json:"wif_provider,omitempty"`
	SavedWIFProvider string       `json:"saved_wif_provider,omitempty"`
}

// ImportOfficial 读取本机官方客户端保存的 OAuth token 并作为账户加入 store，auth_method 为 gcp 的凭据成为企业账户并沿用其 WIF provider
func ImportOfficial(ctx context.Context, store *Store) ([]*Account, error) {
	data, err := readOfficial()
	if err != nil {
		return nil, err
	}
	var credential officialCredential
	if err := json.Unmarshal(data, &credential); err != nil {
		return nil, fmt.Errorf("解析官方客户端凭据: %w", err)
	}
	from := identity{email: credential.IDTokenEmail}
	if credential.AuthMethod == officialCloud {
		from.method, from.provider = antigravity.AuthMethodEnterprise, credential.WIFProvider
	}
	account, err := store.add(ctx, &credential.Token, from, store.proxy, func(*Account) {})
	if err != nil {
		return nil, err
	}
	return []*Account{account}, nil
}

// SwitchOfficial 结束运行中的官方客户端，把账户的 token 写入官方客户端的凭据存储后以原启动参数重新启动它，写入失败时同样重新启动
func SwitchOfficial(store *Store, id string) error {
	account, _ := store.Account(id)
	token := store.Token(id)
	if token == nil {
		return fmt.Errorf("账户不存在: %s", id)
	}
	app, err := officialApp()
	if err != nil {
		return err
	}
	args, err := stopOfficial(app)
	if err != nil {
		return err
	}
	return errors.Join(writeOfficial(account, token), startOfficial(app, args))
}

// writeOfficial 按官方客户端登录后保存的格式写入凭据，企业账户写 auth_method gcp、邮箱与 WIF provider：keyring 不可用标记 1 小时内有效时写 ~/.gemini/jetski-standalone-oauth-token，系统凭据库已有可读凭据时一并更新（官方客户端优先读取它），否则写系统凭据库；系统凭据库不可写时与官方客户端一样写入标记并改写该文件；agy CLI 的会话文件存在时同样改写
func writeOfficial(account Account, token *oauth2.Token) error {
	credential := officialCredential{
		Token:      oauth2.Token{AccessToken: token.AccessToken, TokenType: token.Type(), RefreshToken: token.RefreshToken, Expiry: token.Expiry},
		AuthMethod: officialConsumer,
	}
	if account.AuthMethod == antigravity.AuthMethodEnterprise {
		credential.AuthMethod, credential.IDTokenEmail = officialCloud, account.Email
		credential.WIFProvider, credential.SavedWIFProvider = account.WIFProvider, account.WIFProvider
	}
	data, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cli := filepath.Join(home, ".gemini", filepath.FromSlash(cliFile))
	if _, err := os.Stat(cli); err == nil {
		if err := os.WriteFile(cli, data, 0o600); err != nil {
			return err
		}
	}
	if marker, err := os.Stat(filepath.Join(home, ".gemini", "cache", officialMarker)); err == nil && time.Since(marker.ModTime()) < time.Hour {
		if err := os.WriteFile(filepath.Join(home, ".gemini", officialFile), data, 0o600); err != nil {
			return err
		}
		if _, err := keyring.Get(officialService, officialUser); err != nil {
			return nil
		}
	}
	if err := keyring.Set(officialService, officialUser, string(data)); err != nil {
		cache := filepath.Join(home, ".gemini", "cache")
		if err := os.MkdirAll(cache, 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cache, officialMarker), nil, 0o600); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(home, ".gemini", officialFile), data, 0o600)
	}
	return nil
}

// readOfficial 依次从系统凭据库与 ~/.gemini/jetski-standalone-oauth-token 读取官方客户端凭据
func readOfficial() ([]byte, error) {
	secret, keyringErr := keyring.Get(officialService, officialUser)
	if keyringErr == nil {
		return []byte(secret), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".gemini", officialFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("未找到官方客户端凭据: 系统凭据库 %s:%s: %v; %s: %w", officialService, officialUser, keyringErr, path, err)
	}
	return data, nil
}
