//go:build !windows

package config

// platformProxy 在 Windows 以外的系统没有额外的代理设置，系统代理来自环境变量
func platformProxy() (string, string) {
	return "", ""
}
