package antigravity

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"

	"github.com/google/uuid"
)

// Device 表示账户对上游呈现的设备档案，TimeZone 为 IANA 时区名或 ±hh:mm 固定偏移
type Device struct {
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	UserName       string `json:"user_name"`
	AppDataDir     string `json:"app_data_dir"`
	TimeZone       string `json:"time_zone"`
	InstallationID string `json:"installation_id"`
}

// NewLoginDevice 以本机系统、架构与时区生成登录开始时的设备档案，installation_id 固定，用户名与目录待拿到邮箱后由 WithAccount 补齐
func NewLoginDevice() Device {
	return Device{
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		TimeZone:       localTimeZone(),
		InstallationID: uuid.NewString(),
	}
}

// NewDevice 以本机系统、架构与时区生成账户的设备档案，用户名取邮箱本地部分中的字母与数字
func NewDevice(email string) Device {
	return NewLoginDevice().WithAccount(email)
}

// WithAccount 以邮箱本地部分中的字母与数字补齐用户名与目录，保留系统、架构、时区与 installation_id
func (d Device) WithAccount(email string) Device {
	local, _, _ := strings.Cut(email, "@")
	name := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, local)
	appData := "/home/" + name + "/.gemini/antigravity"
	switch d.OS {
	case "windows":
		appData = `C:\Users\` + name + `\.gemini\antigravity`
	case "darwin":
		appData = "/Users/" + name + "/.gemini/antigravity"
	}
	d.UserName, d.AppDataDir = name, appData
	return d
}

// changelists 为官方各系统 language_server 的构建号，即 User-Agent 中的 cl
var changelists = map[string]string{"windows": "1005016104", "darwin": "1005016104", "linux": "1005016104"}

// spaced 返回档案系统的官方 language_server 的 protojson 是否在结构逗号后加空格：protobuf detrand 以构建号为种子，构建号为奇数时加
func (d Device) spaced() bool {
	changelist, _ := strconv.Atoi(changelists[d.OS])
	return changelist%2 == 1
}

// UserAgent 返回官方客户端在档案的系统与架构上发送的 User-Agent
func (d Device) UserAgent() string {
	return fmt.Sprintf("antigravity/hub/%s (aidev_client; os_type=%s; arch=%s; cl=%s)", ClientVersion, d.OS, d.Arch, changelists[d.OS])
}

// Hostname 返回由 installation_id 确定的主机名：Windows 为系统默认的 DESKTOP-加 7 位字母数字，macOS 为“用户名s-MacBook-Pro.local”，Linux 为“用户名-pc”
func (d Device) Hostname() string {
	switch d.OS {
	case "windows":
		sum := sha256.Sum256([]byte(d.InstallationID))
		return "DESKTOP-" + base32.StdEncoding.EncodeToString(sum[:])[:7]
	case "darwin":
		return d.UserName + "s-MacBook-Pro.local"
	}
	return d.UserName + "-pc"
}

// unleashInstance 返回 unleash 客户端库默认的 instanceId：系统用户名（Windows 带主机名前缀）、连字符与主机名
func (d Device) unleashInstance() string {
	if d.OS == "windows" {
		return d.Hostname() + `\` + d.UserName + "-" + d.Hostname()
	}
	return d.UserName + "-" + d.Hostname()
}

// Location 返回档案时区，无法解析的值按 UTC
func (d Device) Location() *time.Location {
	if location, err := time.LoadLocation(d.TimeZone); err == nil {
		return location
	}
	offset, _ := time.Parse("-07:00", d.TimeZone)
	_, seconds := offset.Zone()
	return time.FixedZone(d.TimeZone, seconds)
}

// localTimeZone 返回本机的 IANA 时区名，取自 TZ 或 /etc/localtime 链接；两者都没有时返回当前 UTC 偏移
func localTimeZone() string {
	if name := time.Local.String(); name != "Local" {
		return name
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok {
			return name
		}
	}
	return time.Now().Format("-07:00")
}
