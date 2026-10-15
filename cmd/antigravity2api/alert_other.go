//go:build desktop && !windows

package main

import (
	"fmt"
	"os"
)

// alert 把桌面应用启动前的错误写到标准错误，macOS 与 Linux 的桌面启动器会收集它
func alert(err error) {
	fmt.Fprintln(os.Stderr, err)
}
