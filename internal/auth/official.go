package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
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
)

// officialCredential 表示官方客户端保存的凭据 JSON
type officialCredential struct {
	Token      oauth2.Token `json:"token"`
	AuthMethod string       `json:"auth_method,omitempty"`
}

// ImportOfficial 读取本机官方客户端保存的 OAuth token 并作为账户加入 store
func ImportOfficial(ctx context.Context, store *Store) ([]*Account, error) {
	data, err := readOfficial()
	if err != nil {
		return nil, err
	}
	var credential officialCredential
	if err := json.Unmarshal(data, &credential); err != nil {
		return nil, fmt.Errorf("解析官方客户端凭据: %w", err)
	}
	account, err := store.Add(ctx, &credential.Token)
	if err != nil {
		return nil, err
	}
	return []*Account{account}, nil
}

// SwitchOfficial 结束运行中的官方客户端，把账户 token 写入官方客户端的凭据存储后以原启动参数重新启动它
func SwitchOfficial(store *Store, id string) error {
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
	if err := writeOfficial(token); err != nil {
		return err
	}
	return startOfficial(app, args)
}

// writeOfficial 按官方客户端登录后保存的格式写入凭据：keyring 不可用标记 1 小时内有效时写 ~/.gemini/jetski-standalone-oauth-token，系统凭据库已有可读凭据时一并更新（官方客户端优先读取它），否则写系统凭据库；系统凭据库不可写时与官方客户端一样写入标记并改写该文件
func writeOfficial(token *oauth2.Token) error {
	data, err := json.Marshal(officialCredential{
		Token:      oauth2.Token{AccessToken: token.AccessToken, TokenType: token.Type(), RefreshToken: token.RefreshToken, Expiry: token.Expiry},
		AuthMethod: "consumer",
	})
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
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
