package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tls "github.com/bogdanfinn/utls"
	"github.com/google/uuid"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// updaterManifest 是官方更新器读取的 Windows x64 更新清单
const updaterManifest = "https://antigravity-hub-auto-updater-974169037036.us-central1.run.app/manifest/latest-x64-win.yml"

// versionDelay 是启动后首次读取更新清单的等待时间，与官方更新器相同
const versionDelay = 10 * time.Second

// versionInterval 是读取更新清单的间隔，与官方更新器相同
const versionInterval = time.Hour

// versionTimeout 是一次读取更新清单的时限
const versionTimeout = time.Minute

// checkVersions 启动 10 秒后读取一次官方更新清单，此后每小时读取一次，最新版本缓存在内存中
func (s *server) checkVersions() {
	select {
	case <-time.After(versionDelay):
	case <-s.ctx.Done():
		return
	}
	ticker := time.NewTicker(versionInterval)
	defer ticker.Stop()
	client := &updater{sessions: tls.NewLRUClientSessionCache(0)}
	language := acceptLanguage()
	for {
		version, err := s.latestVersion(client, language)
		if err != nil {
			slog.Warn("读取官方客户端最新版本失败 | 错误="+err.Error(), "source", "service")
		} else {
			s.latest.Store(&version)
			slog.Info("官方客户端最新版本 | 版本="+version+" 当前="+antigravity.ClientVersion, "source", "service")
		}
		select {
		case <-ticker.C:
		case <-s.ctx.Done():
			return
		}
	}
}

// latestVersion 按实时代理配置，以官方更新器的传输层形态与请求头读取更新清单并返回其中的 version
func (s *server) latestVersion(client *updater, language string) (string, error) {
	staging, err := s.stagingID()
	if err != nil {
		return "", err
	}
	target, _ := url.Parse(updaterManifest + "?noCache=" + strconv.FormatInt(time.Now().UnixMilli(), 32))
	proxy, err := s.live.Load().ProxyURL(&http.Request{URL: target})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(s.ctx, versionTimeout)
	defer cancel()
	status, body, err := client.get(ctx, target, [][2]string{
		{"x-user-staging-id", staging}, {"x-app-version", antigravity.ClientVersion}, {"user-agent", "electron-builder"},
		{"cache-control", "no-cache"}, {"sec-fetch-site", "none"}, {"sec-fetch-mode", "no-cors"}, {"sec-fetch-dest", "empty"},
		{"accept-encoding", "gzip, deflate, br, zstd"}, {"accept-language", language}, {"priority", "u=4, i"},
	}, proxy)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", status)
	}
	for line := range strings.Lines(string(body)) {
		if version, ok := strings.CutPrefix(line, "version:"); ok {
			return strings.TrimSpace(version), nil
		}
	}
	return "", errors.New("更新清单没有 version")
}

// stagingID 返回运行目录 .updaterId 中的更新器标识，不存在时按 electron-updater 的方式以随机字节生成 UUID v5 并保存
func (s *server) stagingID() (string, error) {
	path := filepath.Join(s.boot.RuntimeDir, ".updaterId")
	data, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	seed := make([]byte, 4096)
	rand.Read(seed)
	id := uuid.NewSHA1(uuid.NameSpaceOID, seed).String()
	if err := os.MkdirAll(s.boot.RuntimeDir, 0o755); err != nil {
		return "", err
	}
	return id, os.WriteFile(path, []byte(id), 0o644)
}
