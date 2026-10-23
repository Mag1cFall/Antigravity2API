//go:build desktop && windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// kernel32 是控制台函数所在的系统库
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

// attachConsole 让 GUI 子系统的桌面版附加到启动它的控制台，没有控制台时新建一个；启动时没有的标准输入输出改用该控制台
func attachConsole() {
	const attachParentProcess = ^uintptr(0)
	if attached, _, _ := kernel32.NewProc("AttachConsole").Call(attachParentProcess); attached == 0 {
		if allocated, _, _ := kernel32.NewProc("AllocConsole").Call(); allocated == 0 {
			return
		}
	}
	if !inherited(os.Stdin) {
		if input, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
			os.Stdin = input
		}
	}
	if !inherited(os.Stdout) || !inherited(os.Stderr) {
		if output, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
			if !inherited(os.Stdout) {
				os.Stdout = output
			}
			if !inherited(os.Stderr) {
				os.Stderr = output
			}
		}
	}
}

// inherited 判断启动时是否继承了有效的标准句柄，例如重定向的文件或管道
func inherited(file *os.File) bool {
	handle := file.Fd()
	return handle != 0 && handle != uintptr(windows.InvalidHandle)
}
