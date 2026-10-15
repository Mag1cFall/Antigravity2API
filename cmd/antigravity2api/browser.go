package main

import (
	"os/exec"
	"runtime"
)

// openBrowser 用系统默认浏览器打开链接，Windows 上与官方客户端相同经 rundll32 打开
func openBrowser(link string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", link).Start()
	case "darwin":
		return exec.Command("open", link).Start()
	}
	return exec.Command("xdg-open", link).Start()
}
