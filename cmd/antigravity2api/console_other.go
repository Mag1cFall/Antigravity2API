//go:build desktop && !windows

package main

// attachConsole 在非 Windows 平台上无需处理，进程本身带终端
func attachConsole() {}
