package app

import (
	"cmp"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

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

// exportAccounts 以下载文件返回账户 JSON：查询参数 id 指定账户（可重复），没有时导出全部；内容为 Antigravity Manager 可导入的数组，各项另带本服务的账户资料与设备档案
func (s *server) exportAccounts(w http.ResponseWriter, r *http.Request) {
	items := s.deps.Store.Export(r.URL.Query()["id"])
	if items == nil {
		items = []auth.Transfer{}
	}
	slog.InfoContext(r.Context(), fmt.Sprintf("账户已导出 | 数量=%d", len(items)), "source", "auth")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="antigravity2api-accounts-`+time.Now().Format("20060102")+`.json"`)
	writeJSON(w, http.StatusOK, items)
}

// importAccountFile 导入账户 JSON 或含 refresh token 的文本：同时最多 transferWorkers 个账户以 refresh token 换取 access token、读取邮箱后写入，本服务导出的资料一并写回，返回导入的账户与失败项并在后台刷新账户池
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
			id, err := s.importTransfer(r, item)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, transferFailure{Account: cmp.Or(item.Email, item.RefreshToken[:min(len(item.RefreshToken), 10)]+"…"), Error: err.Error()})
				return
			}
			imported = append(imported, id)
		})
	}
	workers.Wait()
	slog.InfoContext(r.Context(), fmt.Sprintf("账户 JSON 导入完成 | 账户=%s | 失败=%d", strings.Join(imported, ","), len(failed)), "source", "auth")
	if len(imported) != 0 {
		s.refreshLater()
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": imported, "failed": failed})
}

// importTransfer 以导入项的 refresh token 写入账户，再写回本服务导出的备注名、代理、优先级、启用状态与 AI 积分设置
func (s *server) importTransfer(r *http.Request, item auth.Transfer) (string, error) {
	account, err := s.deps.Store.AddWithDevice(r.Context(), &oauth2.Token{RefreshToken: item.RefreshToken}, item.Device)
	if err != nil {
		return "", err
	}
	if item.Device.InstallationID == "" {
		return account.ID, nil
	}
	account.Label, account.Proxy, account.Priority, account.AICredits = item.Label, item.Proxy, item.Priority, item.AICredits
	if item.Enabled != nil {
		account.Enabled = *item.Enabled
	}
	return account.ID, s.deps.Store.Save(account)
}
