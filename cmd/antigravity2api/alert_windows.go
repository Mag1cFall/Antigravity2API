//go:build desktop && windows

package main

import (
	"syscall"
	"unsafe"

	"github.com/Mag1cFall/Antigravity2API/internal/i18n"
)

// alert 在桌面应用启动前出错时用系统消息框按系统界面语言显示原因，GUI 进程没有可见的标准错误
func alert(err error) {
	const iconError = 0x10
	text, _ := syscall.UTF16PtrFromString(i18n.Translate(i18n.SystemLocale(), err.Error()))
	caption, _ := syscall.UTF16PtrFromString(title)
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), iconError)
}
