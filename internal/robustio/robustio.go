//go:build !windows

// Package robustio 提供在 Windows 上对文件暂时被其他进程占用时重试的文件操作
package robustio

import "os"

// Rename 以 os.Rename 替换文件
func Rename(from, to string) error {
	return os.Rename(from, to)
}
