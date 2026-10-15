package config

import "golang.org/x/sys/windows/registry"

// platformProxy 读取当前用户 Internet 设置中已启用的代理服务器与例外列表
func platformProxy() (string, string) {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return "", ""
	}
	defer key.Close()
	if enabled, _, err := key.GetIntegerValue("ProxyEnable"); err != nil || enabled == 0 {
		return "", ""
	}
	server, _, _ := key.GetStringValue("ProxyServer")
	bypass, _, _ := key.GetStringValue("ProxyOverride")
	return server, bypass
}
