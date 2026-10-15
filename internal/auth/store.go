package auth

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

const (
	// accountFile 为账户资料文件名
	accountFile = "account.json"
	// tokenFile 为 OAuth token 文件名
	tokenFile = "token.json"
)

// Account 表示 auth/<ID>/account.json 中的一个账户，Device 为导入时生成的设备档案
type Account struct {
	ID       string             `json:"id"`
	Email    string             `json:"email"`
	Label    string             `json:"label,omitempty"`
	Proxy    string             `json:"proxy,omitempty"`
	Project  string             `json:"project"`
	Tier     string             `json:"tier"`
	PaidTier string             `json:"paid_tier,omitempty"`
	Region   string             `json:"region,omitempty"`
	Device   antigravity.Device `json:"device"`
	Enabled  bool               `json:"enabled"`
	// Priority 为调度优先级，越大越先使用，默认 0
	Priority int       `json:"priority,omitempty"`
	AddedAt  time.Time `json:"added_at"`
}

// Rank 返回账户套餐的等级：Ultra 为 2，Pro 为 1，其余为 0，有付费套餐时按付费套餐判断，与管理端套餐徽标一致
func (a Account) Rank() int {
	tier := strings.ToLower(cmp.Or(a.PaidTier, a.Tier))
	switch {
	case strings.Contains(tier, "ultra"):
		return 2
	case strings.Contains(tier, "pro"):
		return 1
	}
	return 0
}

// Store 管理账户目录下各账户的资料与 token，proxy 为添加账户时读取邮箱所用的代理
type Store struct {
	dir      string
	proxy    antigravity.Proxy
	mu       sync.Mutex
	accounts map[string]Account
	tokens   map[string]*oauth2.Token
}

// Open 加载 dir 下各子目录的 account.json 与 token.json，目录不存在时返回空账户库
func Open(dir string, proxy antigravity.Proxy) (*Store, error) {
	store := &Store{dir: dir, proxy: proxy, accounts: make(map[string]Account), tokens: make(map[string]*oauth2.Token)}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var account Account
		if err := readJSON(filepath.Join(dir, entry.Name(), accountFile), &account); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		var token oauth2.Token
		if err := readJSON(filepath.Join(dir, entry.Name(), tokenFile), &token); err != nil {
			return nil, err
		}
		store.accounts[account.ID] = account
		store.tokens[account.ID] = &token
	}
	return store, nil
}

// Sync 重新读取账户目录：加入其他进程写入的新账户，refresh token 与内存不同的账户换用目录中的资料与 token，移除目录已删除的账户，返回是否有变化
func (s *Store) Sync() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	seen := make(map[string]bool, len(entries))
	changed := false
	for _, entry := range entries {
		var account Account
		var token oauth2.Token
		if !entry.IsDir() || readJSON(filepath.Join(s.dir, entry.Name(), accountFile), &account) != nil {
			continue
		}
		seen[account.ID] = true
		if readJSON(filepath.Join(s.dir, entry.Name(), tokenFile), &token) != nil {
			continue
		}
		if current, ok := s.tokens[account.ID]; ok && current.RefreshToken == token.RefreshToken {
			continue
		}
		s.accounts[account.ID], s.tokens[account.ID] = account, &token
		changed = true
	}
	for id := range s.accounts {
		if !seen[id] {
			delete(s.accounts, id)
			delete(s.tokens, id)
			changed = true
		}
	}
	return changed, nil
}

// Accounts 按添加时间返回全部账户的副本
func (s *Store) Accounts() []*Account {
	s.mu.Lock()
	defer s.mu.Unlock()
	accounts := make([]*Account, 0, len(s.accounts))
	for _, account := range s.accounts {
		accounts = append(accounts, &account)
	}
	slices.SortFunc(accounts, func(a, b *Account) int {
		return cmp.Or(a.AddedAt.Compare(b.AddedAt), cmp.Compare(a.ID, b.ID))
	})
	return accounts
}

// Save 原子写回 account.json
func (s *Store) Save(account *Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSON(filepath.Join(s.dir, account.ID, accountFile), account); err != nil {
		return err
	}
	s.accounts[account.ID] = *account
	return nil
}

// SaveToken 原子写回 token.json
func (s *Store) SaveToken(id string, token *oauth2.Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeJSON(filepath.Join(s.dir, id, tokenFile), token); err != nil {
		return err
	}
	s.tokens[id] = token
	return nil
}

// Token 返回账户最近保存的 token
func (s *Store) Token(id string) *oauth2.Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[id]
}

// Add 取 id_token 中的邮箱，没有 id_token 时请求 userinfo，以邮箱为 ID 写入 token 与资料；新账户生成设备档案，已有账户保留其余字段；项目、套餐与地区由服务初始化账户时写回
func (s *Store) Add(ctx context.Context, token *oauth2.Token) (*Account, error) {
	latest := token
	email := tokenEmail(token)
	if email == "" {
		source := antigravity.NewTokenSource(token, s.proxy, func(refreshed *oauth2.Token) error {
			latest = refreshed
			return nil
		})
		user, err := antigravity.NewClient("", source, antigravity.NewDevice(""), s.proxy).Userinfo(ctx)
		if err != nil {
			return nil, err
		}
		email = user.Email
	}
	account := &Account{ID: email, Email: email, Device: antigravity.NewDevice(email), Enabled: true, AddedAt: time.Now()}
	s.mu.Lock()
	if existing, ok := s.accounts[email]; ok {
		account = &existing
	}
	s.mu.Unlock()
	if err := s.SaveToken(account.ID, latest); err != nil {
		return nil, err
	}
	if err := s.Save(account); err != nil {
		return nil, err
	}
	return account, nil
}

// tokenEmail 返回 token 响应中 id_token 载荷的邮箱，登录换取的 token 带 id_token，官方客户端保存的 token 没有
func tokenEmail(token *oauth2.Token) string {
	idToken, _ := token.Extra("id_token").(string)
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	json.Unmarshal(payload, &claims)
	return claims.Email
}

// Remove 删除账户目录
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.RemoveAll(filepath.Join(s.dir, id)); err != nil {
		return err
	}
	delete(s.accounts, id)
	delete(s.tokens, id)
	return nil
}

// readJSON 读取并解码 JSON 文件
func readJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// writeJSON 先写同目录临时文件再替换目标，保证读者只看到完整文件
func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
