//go:build !windows

package app

import (
	"os"
	"strings"
)

// systemLocales 按 GLib g_get_language_names 的优先级返回语言名：LANGUAGE（冒号分隔）、LC_ALL、LC_MESSAGES、LANG 中第一个非空的值
func systemLocales() []string {
	for _, name := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := os.Getenv(name); value != "" {
			return strings.Split(value, ":")
		}
	}
	return nil
}
