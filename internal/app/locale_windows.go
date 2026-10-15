package app

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// getUserDefaultLocaleName 是 kernel32 的 GetUserDefaultLocaleName
var getUserDefaultLocaleName = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultLocaleName")

// systemLocales 返回 Windows 用户默认区域设置名，即 ICU 默认区域；排序后缀（如 es-ES_tradnl）按 ICU 转为 @ 变体
func systemLocales() []string {
	buffer := make([]uint16, 85)
	if length, _, _ := getUserDefaultLocaleName.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer))); length == 0 {
		return nil
	}
	return []string{strings.Replace(windows.UTF16ToString(buffer), "_", "@", 1)}
}
