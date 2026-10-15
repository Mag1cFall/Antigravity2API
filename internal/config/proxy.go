package config

import (
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// systemProxy 返回环境变量中的代理，没有时返回系统代理设置中的代理，都没有时直连
func systemProxy(r *http.Request) (*url.URL, error) {
	if proxy, err := http.ProxyFromEnvironment(r); proxy != nil || err != nil {
		return proxy, err
	}
	server, bypass := platformProxy()
	return parseSystemProxy(server, bypass, r)
}

// parseSystemProxy 按 Windows 代理设置的格式选择代理：server 为 host:port 或 http=…;https=…;socks=…，bypass 为分号分隔的例外主机，<local> 表示不含点的主机名
func parseSystemProxy(server, bypass string, r *http.Request) (*url.URL, error) {
	server = strings.TrimSpace(server)
	host := strings.ToLower(r.URL.Hostname())
	if server == "" || host == "localhost" || isLoopback(host) {
		return nil, nil
	}
	for _, pattern := range strings.Split(strings.ToLower(bypass), ";") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "<local>" && !strings.Contains(host, ".") {
			return nil, nil
		}
		if matched, _ := path.Match(pattern, host); pattern != "" && matched {
			return nil, nil
		}
	}
	if !strings.Contains(server, "=") {
		return proxyAddress("http", server)
	}
	entries := map[string]string{}
	for _, entry := range strings.Split(server, ";") {
		if scheme, address, ok := strings.Cut(strings.TrimSpace(entry), "="); ok {
			entries[strings.ToLower(scheme)] = strings.TrimSpace(address)
		}
	}
	if address := entries[r.URL.Scheme]; address != "" {
		return proxyAddress("http", address)
	}
	if address := entries["socks"]; address != "" {
		return proxyAddress("socks5", address)
	}
	return nil, nil
}

// proxyAddress 把 host:port 或带协议的地址转为代理 URL
func proxyAddress(scheme, address string) (*url.URL, error) {
	if strings.Contains(address, "://") {
		return url.Parse(address)
	}
	return &url.URL{Scheme: scheme, Host: address}, nil
}

// isLoopback 判断主机名是否为回环 IP
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
