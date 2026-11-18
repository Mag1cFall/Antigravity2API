package app

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/auth"
)

// transferWorkers 是导入账户 JSON 时同时换取 token 的账户数
const transferWorkers = 4

// maxTransferSize 是导入内容的大小上限
const maxTransferSize = 16 << 20

// transferFailure 是导入失败的一个账户
type transferFailure struct {
	Account string `json:"account"`
	Error   string `json:"error"`
}

// exportAccounts 返回账户 JSON 供管理端复制：查询参数 id 指定账户（可重复），没有时导出全部；内容为 Antigravity Manager 可导入的数组，各项另带本服务的账户资料与设备档案
func (s *server) exportAccounts(w http.ResponseWriter, r *http.Request) {
	items := s.deps.Store.Export(r.URL.Query()["id"])
	if items == nil {
		items = []auth.Transfer{}
	}
	slog.InfoContext(r.Context(), fmt.Sprintf("账户已导出 | 数量=%d", len(items)), "source", "auth")
	writeJSON(w, http.StatusOK, items)
}

// saveAccounts 把账户 JSON 写入服务所在电脑的文件夹：ids 为空时导出全部，split 为真时每个账户一个文件，只有一个账户的文件以邮箱命名；返回写入的文件，有文件写入失败时删除本次已写入的文件
func (s *server) saveAccounts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs   []string `json:"ids"`
		Dir   string   `json:"dir"`
		Split bool     `json:"split"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items := s.deps.Store.Export(input.IDs)
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "no_accounts", "没有可导出的账户")
		return
	}
	batches := [][]auth.Transfer{items}
	if input.Split {
		batches = batches[:0]
		for _, item := range items {
			batches = append(batches, []auth.Transfer{item})
		}
	}
	files := []string{}
	for _, batch := range batches {
		name := "antigravity2api-accounts-" + time.Now().Format("20060102") + ".json"
		if len(batch) == 1 {
			name = "antigravity2api-" + batch[0].Email + ".json"
		}
		path, err := saveFile(input.Dir, name, 0o600, func(file io.Writer) error {
			encoder := json.NewEncoder(file)
			encoder.SetIndent("", "  ")
			return encoder.Encode(batch)
		})
		if err != nil {
			for _, written := range files {
				os.Remove(written)
			}
			writeError(w, http.StatusBadRequest, "export_failed", err.Error())
			return
		}
		files = append(files, path)
	}
	slog.InfoContext(r.Context(), fmt.Sprintf("账户已导出 | 数量=%d | 文件=%s", len(items), strings.Join(files, ",")), "source", "auth")
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// importSource 是待识别的一份账户内容：Content 为粘贴或拖入的原文，Path 为服务所在电脑上的文件，Name 为显示名
type importSource struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Path    string `json:"path"`
}

// discoveredAccount 是账户内容中识别出的一个账户，Source 为所在内容的显示名，Exists 表示账户库中已有该邮箱
type discoveredAccount struct {
	auth.Transfer
	Source string `json:"source"`
	Exists bool   `json:"exists"`
}

// discoverAccounts 识别各份内容中的账户而不导入，按 refresh token 去重；服务所在电脑上的文件由服务读取，读不到或没有账户的内容列为失败项
func (s *server) discoverAccounts(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Sources []importSource `json:"sources"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4*maxTransferSize)
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	accounts := []discoveredAccount{}
	failed := []transferFailure{}
	seen := map[string]bool{}
	for _, source := range input.Sources {
		data := []byte(source.Content)
		if source.Path != "" {
			source.Name = filepath.Base(source.Path)
			content, err := readTransferFile(source.Path)
			if err != nil {
				failed = append(failed, transferFailure{Account: source.Name, Error: err.Error()})
				continue
			}
			data = content
		}
		items := auth.ParseTransfers(data)
		if len(items) == 0 {
			failed = append(failed, transferFailure{Account: source.Name, Error: "内容中没有 Antigravity 账户的 refresh token"})
			continue
		}
		for _, item := range items {
			if seen[item.RefreshToken] {
				continue
			}
			seen[item.RefreshToken] = true
			_, exists := s.deps.Store.Account(item.Email)
			accounts = append(accounts, discoveredAccount{Transfer: item, Source: source.Name, Exists: item.Email != "" && exists})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts, "failed": failed})
}

// readTransferFile 读取服务所在电脑上的账户文件，超过 maxTransferSize 时返回错误
func readTransferFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxTransferSize+1))
	if err == nil && len(data) > maxTransferSize {
		err = fmt.Errorf("文件超过 %d MB", maxTransferSize>>20)
	}
	return data, err
}

// importAccountFile 导入账户 JSON 或含 refresh token 的文本：同时最多 transferWorkers 个账户以 refresh token 换取 access token、读取邮箱后写入，返回导入的账户与失败项并在后台刷新账户池
func (s *server) importAccountFile(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTransferSize))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items := auth.ParseTransfers(data)
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "no_accounts", "内容中没有 Antigravity 账户的 refresh token")
		return
	}
	var (
		mu       sync.Mutex
		workers  sync.WaitGroup
		slots    = make(chan struct{}, transferWorkers)
		imported = []string{}
		failed   = []transferFailure{}
	)
	for _, item := range items {
		slots <- struct{}{}
		workers.Go(func() {
			defer func() { <-slots }()
			account, err := s.deps.Service.Import(r.Context(), item)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, transferFailure{Account: cmp.Or(item.Email, item.RefreshToken[:min(len(item.RefreshToken), 10)]+"…"), Error: err.Error()})
				return
			}
			imported = append(imported, account.ID)
		})
	}
	workers.Wait()
	slog.InfoContext(r.Context(), fmt.Sprintf("账户 JSON 导入完成 | 账户=%s | 失败=%d", strings.Join(imported, ","), len(failed)), "source", "auth")
	if len(imported) != 0 {
		s.refreshLater()
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": imported, "failed": failed})
}
