//go:build desktop && windows

package main

import (
	"os"
	"syscall"
)

// attachConsole 把 GUI 子系统的进程接到父进程的控制台，让子命令能读写终端
func attachConsole() {
	const attachParentProcess = ^uintptr(0)
	if r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("AttachConsole").Call(attachParentProcess); r == 0 {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = in
	}
}
