//go:build desktop && !windows

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// attach 让单击菜单栏或托盘图标在新的 goroutine 中显示窗口，右键由系统弹出 update 设置的菜单
func (t *tray) attach(show func()) {
	t.item.OnClick(func() { go show() })
}

// update 把菜单项转换为系统菜单设置到托盘图标
func (t *tray) update(entries []trayEntry) {
	t.item.SetMenu(nativeMenu(t.app.NewMenu(), entries))
}

// nativeMenu 把菜单项依次加入 menu 并返回它
func nativeMenu(menu *application.Menu, entries []trayEntry) *application.Menu {
	for _, entry := range entries {
		switch {
		case entry.label == "":
			menu.AddSeparator()
		case entry.sub != nil:
			nativeMenu(menu.AddSubmenu(entry.label), entry.sub)
		case entry.run == nil:
			menu.Add(entry.label).SetEnabled(false)
		case entry.checkable:
			menu.AddCheckbox(entry.label, entry.checked).OnClick(func(*application.Context) { go entry.run() })
		default:
			menu.Add(entry.label).OnClick(func(*application.Context) { go entry.run() })
		}
	}
	return menu
}

// message 只在 Windows 上拦截托盘消息
func (t *tray) message(uintptr, uint32, uintptr, uintptr) (uintptr, bool) {
	return 0, false
}
