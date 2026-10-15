//go:build desktop && windows

package main

import (
	"syscall"
	"unsafe"
)

// alert 在桌面应用启动前出错时用系统消息框显示原因，GUI 进程没有可见的标准错误
func alert(err error) {
	const iconError = 0x10
	text, _ := syscall.UTF16PtrFromString(err.Error())
	caption, _ := syscall.UTF16PtrFromString(title)
	syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), iconError)
}
