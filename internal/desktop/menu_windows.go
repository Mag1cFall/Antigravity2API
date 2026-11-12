//go:build desktop

package desktop

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// menuClass 为自绘托盘菜单的窗口类名
const menuClass = "Antigravity2APITrayMenu"

// dwmwaWindowCornerPreference 为 DWM 的窗口圆角属性，dwmwcpRoundSmall 为 Windows 11 菜单使用的小圆角
const (
	dwmwaWindowCornerPreference w32.DWMWINDOWATTRIBUTE = 33
	dwmwcpRoundSmall            uint32                 = 3
)

var (
	// createCompatibleBitmap 为 gdi32 的 CreateCompatibleBitmap，用于双缓冲绘制
	createCompatibleBitmap = windows.NewLazySystemDLL("gdi32.dll").NewProc("CreateCompatibleBitmap")
	// notifyIconGetRect 为 shell32 的 Shell_NotifyIconGetRect，返回托盘图标的屏幕位置
	notifyIconGetRect = windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconGetRect")
)

// menuPalette 为自绘菜单一种配色的颜色，与管理端下拉菜单相同
type menuPalette struct {
	background, border, text, muted, hover, check w32.COLORREF
}

var (
	// darkPalette 为深色配色的菜单颜色
	darkPalette = menuPalette{w32.RGB(0x14, 0x14, 0x14), w32.RGB(0x2b, 0x2b, 0x2b), w32.RGB(0xff, 0xff, 0xff), w32.RGB(0xae, 0xae, 0xae), w32.RGB(0x28, 0x29, 0x2c), w32.RGB(0x22, 0x6b, 0xee)}
	// lightPalette 为浅色配色的菜单颜色
	lightPalette = menuPalette{w32.RGB(0xff, 0xff, 0xff), w32.RGB(0xd7, 0xde, 0xe3), w32.RGB(0x15, 0x15, 0x15), w32.RGB(0x4a, 0x4b, 0x50), w32.RGB(0xe5, 0xe9, 0xf0), w32.RGB(0x00, 0x53, 0xe2)}
)

// menuFonts 为各界面语言的菜单字体，中文与管理端相同用微软雅黑 UI，其他语言为 Segoe UI
var menuFonts = map[string]string{"zh-CN": "Microsoft YaHei UI", "zh-TW": "Microsoft YaHei UI"}

// trayMenu 为弹出中的自绘菜单或它展开的子菜单，只在主线程上访问；rows 为各项在客户区的矩形，limit 为所在显示器工作区的高度
type trayMenu struct {
	hwnd     w32.HWND
	entries  []trayEntry
	rows     []w32.RECT
	limit    int
	hot      int
	tracking bool
	dpi      int
	colors   menuPalette
	font     w32.HFONT
	glyph    w32.HFONT
	child    *trayMenu
	parent   *trayMenu
}

var (
	// activeMenu 为当前弹出的自绘菜单，没有时为 nil
	activeMenu *trayMenu
	// registerMenu 保证自绘菜单的窗口类只注册一次
	registerMenu sync.Once
)

// attach 让双击托盘图标在新的 goroutine 中显示窗口，单击不做任何事，右键或键盘打开托盘图标的上下文菜单时由 message 弹出自绘菜单
func (t *tray) attach(show func()) {
	t.item.OnDoubleClick(func() { go show() })
}

// update 记录最新的菜单项，下次弹出菜单时使用
func (t *tray) update(entries []trayEntry) {
	t.latest.Store(&entries)
}

// message 拦截托盘图标的 WM_CONTEXTMENU，在托盘图标处弹出自绘菜单；NOTIFYICON_VERSION_4 下 lParam 高字为图标编号，wParam 为右键或键盘打开菜单时的屏幕坐标
func (t *tray) message(hwnd uintptr, msg uint32, wParam, lParam uintptr) (uintptr, bool) {
	if msg != w32.WM_USER+1 || lParam&0xffff != w32.WM_CONTEXTMENU {
		return 0, false
	}
	x, y := int32(int16(wParam)), int32(int16(wParam>>16))
	anchor := w32.RECT{Left: x, Top: y, Right: x, Bottom: y}
	identifier := struct {
		Size   uint32
		Window uintptr
		ID     uint32
		GUID   windows.GUID
	}{Window: hwnd, ID: uint32(lParam >> 16)}
	identifier.Size = uint32(unsafe.Sizeof(identifier))
	var icon w32.RECT
	if result, _, _ := notifyIconGetRect.Call(uintptr(unsafe.Pointer(&identifier)), uintptr(unsafe.Pointer(&icon))); result == 0 {
		anchor = icon
	}
	t.popup(anchor)
	return 0, true
}

// popup 在托盘图标 anchor 旁弹出自绘菜单并让它获得焦点，已有弹出的菜单时先关闭它；配色取页面报告的界面配色，页面没有报告时取系统配色
func (t *tray) popup(anchor w32.RECT) {
	if activeMenu != nil {
		w32.DestroyWindow(activeMenu.hwnd)
	}
	registerMenu.Do(func() {
		class := w32.WNDCLASSEX{
			Style:     w32.CS_DROPSHADOW,
			WndProc:   syscall.NewCallback(menuProc),
			Instance:  w32.GetModuleHandle(""),
			Cursor:    w32.LoadCursorWithResourceID(0, w32.IDC_ARROW),
			ClassName: w32.MustStringToUTF16Ptr(menuClass),
		}
		class.Size = uint32(unsafe.Sizeof(class))
		w32.RegisterClassEx(&class)
	})
	colors := lightPalette
	if dark := t.dark.Load(); dark != nil && *dark || dark == nil && w32.IsCurrentlyDarkMode() {
		colors = darkPalette
	}
	menu := newMenu(*t.latest.Load(), anchor, colors)
	width, height := menu.layout()
	left, top := placeMenu(anchor, width, height, workArea(anchor))
	activeMenu = menu
	menu.create(left, top, width, height, 0)
	w32.SetForegroundWindow(menu.hwnd)
}

// newMenu 按 anchor 所在显示器的 DPI、工作区与界面语言的字体准备一个菜单
func newMenu(entries []trayEntry, anchor w32.RECT, colors menuPalette) *trayMenu {
	var dpiX, dpiY w32.UINT
	w32.GetDPIForMonitor(w32.MonitorFromRect(&anchor, w32.MONITOR_DEFAULTTONEAREST), w32.MDT_EFFECTIVE_DPI, &dpiX, &dpiY)
	face := menuFonts[locale()]
	if face == "" {
		face = "Segoe UI"
	}
	work := workArea(anchor)
	menu := &trayMenu{entries: entries, hot: -1, dpi: int(dpiX), colors: colors, limit: int(work.Bottom - work.Top)}
	menu.font = createFont(face, menu.px(14))
	menu.glyph = createFont("Segoe MDL2 Assets", menu.px(12))
	return menu
}

// create 创建并显示菜单窗口，extra 为额外的扩展窗口样式
func (m *trayMenu) create(left, top, width, height int, extra uint) {
	m.hwnd = w32.CreateWindowEx(w32.WS_EX_TOOLWINDOW|w32.WS_EX_TOPMOST|extra, w32.MustStringToUTF16Ptr(menuClass), w32.MustStringToUTF16Ptr("Antigravity2API"), w32.WS_POPUP, left, top, width, height, 0, 0, w32.GetModuleHandle(""), nil)
	corner, border := dwmwcpRoundSmall, m.colors.border
	w32.DwmSetWindowAttribute(m.hwnd, dwmwaWindowCornerPreference, unsafe.Pointer(&corner), unsafe.Sizeof(corner))
	w32.DwmSetWindowAttribute(m.hwnd, w32.DwmwaBorderColor, unsafe.Pointer(&border), unsafe.Sizeof(border))
	if extra&w32.WS_EX_NOACTIVATE != 0 {
		w32.ShowWindow(m.hwnd, w32.SW_SHOWNOACTIVATE)
	} else {
		w32.ShowWindow(m.hwnd, w32.SW_SHOW)
	}
}

// workArea 返回 anchor 所在显示器的工作区
func workArea(anchor w32.RECT) w32.RECT {
	info := w32.MONITORINFO{}
	info.CbSize = uint32(unsafe.Sizeof(info))
	w32.GetMonitorInfo(w32.MonitorFromRect(&anchor, w32.MONITOR_DEFAULTTONEAREST), &info)
	return info.RcWork
}

// placeMenu 返回菜单左上角：左边对齐 anchor、底边贴着 anchor 上方，右侧放不下时右边对齐 anchor，上方放不下时贴着 anchor 下方，再限制在工作区内
func placeMenu(anchor w32.RECT, width, height int, work w32.RECT) (int, int) {
	left, top := int(anchor.Left), int(anchor.Top)-height
	if left+width > int(work.Right) {
		left = int(anchor.Right) - width
	}
	if top < int(work.Top) {
		top = int(anchor.Bottom)
	}
	return max(int(work.Left), min(left, int(work.Right)-width)), max(int(work.Top), min(top, int(work.Bottom)-height))
}

// createFont 创建字号为 size 像素的 ClearType 字体
func createFont(face string, size int) w32.HFONT {
	font := w32.LOGFONT{Height: -int32(size), Weight: w32.FW_NORMAL, CharSet: w32.DEFAULT_CHARSET, Quality: w32.CLEARTYPE_QUALITY}
	copy(font.FaceName[:w32.LF_FACESIZE-1], windows.StringToUTF16(face))
	return w32.CreateFontIndirect(&font)
}

// px 把 96 DPI 下的像素换算为菜单所在显示器的像素
func (m *trayMenu) px(value int) int {
	return w32.MulDiv(value, m.dpi, 96)
}

// layout 计算各项位置并返回菜单大小：操作项高 32、说明行高 28、分隔线高 9，左右留白 16，与管理端下拉菜单相同；高度超过工作区时其余各项另起一列
func (m *trayMenu) layout() (int, int) {
	dc := w32.GetDC(0)
	defer w32.ReleaseDC(0, dc)
	previous := w32.SelectObject(dc, m.font)
	defer w32.SelectObject(dc, previous)
	top, bottom := 1+m.px(4), m.limit-m.px(4)-1
	left, y, height, widest, first := 1, top, top, 0, 0
	m.rows = m.rows[:0]
	column := func(end int) {
		width := max(widest+m.px(64), m.px(180))
		for index := first; index < end; index++ {
			m.rows[index].Left, m.rows[index].Right = int32(left), int32(left+width)
		}
		left, widest, first = left+width, 0, end
	}
	for index, entry := range m.entries {
		size := m.px(32)
		switch {
		case entry.label == "":
			size = m.px(9)
		case entry.run == nil && entry.sub == nil:
			size = m.px(28)
		}
		if y+size > bottom && index > first {
			column(index)
			y = top
		}
		m.rows = append(m.rows, w32.RECT{Top: int32(y), Bottom: int32(y + size)})
		y += size
		height = max(height, y)
		if entry.label != "" {
			text := windows.StringToUTF16(entry.label)
			var extent w32.SIZE
			w32.GetTextExtentPoint32(dc, &text[0], len(text)-1, &extent)
			widest = max(widest, int(extent.CX))
		}
	}
	column(len(m.entries))
	return left + 1, height + m.px(4) + 1
}

// paint 在 dc 上绘制菜单：边框、背景、高亮项、文字、分隔线、勾选与子菜单箭头，说明行用次要文字颜色
func (m *trayMenu) paint(dc w32.HDC, width, height int32) {
	fill(dc, w32.RECT{Right: width, Bottom: height}, m.colors.border)
	fill(dc, w32.RECT{Left: 1, Top: 1, Right: width - 1, Bottom: height - 1}, m.colors.background)
	w32.SetBkMode(dc, w32.TRANSPARENT)
	padding := int32(m.px(16))
	for index, entry := range m.entries {
		row := m.rows[index]
		if entry.label == "" {
			middle := (row.Top + row.Bottom) / 2
			fill(dc, w32.RECT{Left: row.Left, Top: middle, Right: row.Right, Bottom: middle + 1}, m.colors.border)
			continue
		}
		color := m.colors.text
		if entry.run == nil && entry.sub == nil {
			color = m.colors.muted
		}
		if index == m.hot {
			fill(dc, row, m.colors.hover)
		}
		w32.SelectObject(dc, m.font)
		w32.SetTextColor(dc, color)
		label := w32.RECT{Left: row.Left + padding, Top: row.Top, Right: row.Right - 2*padding, Bottom: row.Bottom}
		drawText(dc, entry.label, &label, w32.DT_LEFT)
		mark := ""
		switch {
		case entry.checkable && entry.checked:
			mark = ""
			w32.SetTextColor(dc, m.colors.check)
		case entry.sub != nil:
			mark = ""
		}
		if mark != "" {
			w32.SelectObject(dc, m.glyph)
			glyph := w32.RECT{Left: row.Right - padding - int32(m.px(16)), Top: row.Top, Right: row.Right - padding, Bottom: row.Bottom}
			drawText(dc, mark, &glyph, w32.DT_CENTER)
		}
	}
}

// fill 用 color 填充矩形
func fill(dc w32.HDC, rect w32.RECT, color w32.COLORREF) {
	brush := w32.CreateSolidBrush(color)
	w32.FillRect(dc, &rect, brush)
	w32.DeleteObject(brush)
}

// drawText 在矩形内垂直居中绘制单行文字，align 为水平对齐方式
func drawText(dc w32.HDC, text string, rect *w32.RECT, align uint32) {
	value := windows.StringToUTF16(text)
	w32.DrawText(dc, value, len(value)-1, rect, align|w32.DT_SINGLELINE|w32.DT_VCENTER|w32.DT_NOPREFIX)
}

// selectable 判断一项能否高亮：操作项与子菜单可以，分隔线与说明行不可以
func (e trayEntry) selectable() bool {
	return e.label != "" && (e.run != nil || e.sub != nil)
}

// hit 返回客户区坐标 x、y 处的可高亮项，其余位置返回 -1
func (m *trayMenu) hit(x, y int32) int {
	for index, entry := range m.entries {
		if row := m.rows[index]; entry.selectable() && x >= row.Left && x < row.Right && y >= row.Top && y < row.Bottom {
			return index
		}
	}
	return -1
}

// next 返回从 from 起沿 direction 方向的下一个可高亮项，首尾相接
func (m *trayMenu) next(from, direction int) int {
	count := len(m.entries)
	for range count {
		from = (from + direction + count) % count
		if m.entries[from].selectable() {
			return from
		}
	}
	return m.hot
}

// highlight 高亮第 index 项并以它的文字作为窗口标题，供屏幕阅读器朗读，index 为 -1 时取消高亮；高亮子菜单项时在其旁展开子菜单，高亮其他项时收起子菜单
func (m *trayMenu) highlight(index int) {
	if index == m.hot {
		return
	}
	m.hot = index
	title := "Antigravity2API"
	if index >= 0 {
		title = m.entries[index].label
	}
	w32.SetWindowText(m.hwnd, title)
	w32.InvalidateRect(m.hwnd, nil, false)
	if m.child != nil {
		w32.DestroyWindow(m.child.hwnd)
	}
	if index >= 0 && m.entries[index].sub != nil {
		m.expand(index)
	}
}

// expand 在第 index 项右侧展开它的子菜单，右侧放不下时放在左侧；子菜单不获得焦点，焦点留在菜单上
func (m *trayMenu) expand(index int) {
	frame := w32.GetWindowRect(m.hwnd)
	row := w32.RECT{Left: frame.Left, Top: frame.Top + m.rows[index].Top, Right: frame.Right, Bottom: frame.Top + m.rows[index].Bottom}
	child := newMenu(m.entries[index].sub, row, m.colors)
	child.parent = m
	width, height := child.layout()
	work := workArea(row)
	left := int(frame.Right)
	if left+width > int(work.Right) {
		left = int(frame.Left) - width
	}
	top := max(int(work.Top), min(int(row.Top)-child.px(4)-1, int(work.Bottom)-height))
	m.child = child
	child.create(left, top, width, height, w32.WS_EX_NOACTIVATE)
}

// choose 关闭菜单并执行第 index 项，子菜单项只展开
func (m *trayMenu) choose(index int) {
	run := m.entries[index].run
	if run == nil {
		return
	}
	w32.DestroyWindow(m.hwnd)
	go run()
}

// menuOf 返回 hwnd 对应的菜单或子菜单，都不是时返回 nil
func menuOf(hwnd w32.HWND) *trayMenu {
	switch menu := activeMenu; {
	case menu == nil:
		return nil
	case menu.hwnd == hwnd:
		return menu
	case menu.child != nil && menu.child.hwnd == hwnd:
		return menu.child
	}
	return nil
}

// menuProc 为自绘菜单的窗口过程：鼠标悬停高亮、点击执行，方向键、Home、End 移动高亮，回车或空格执行，Esc 或失去焦点时关闭；子菜单只绘制
func menuProc(hwnd w32.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	menu := menuOf(hwnd)
	if menu == nil {
		return w32.DefWindowProc(hwnd, msg, wParam, lParam)
	}
	switch msg {
	case w32.WM_PAINT:
		var paint w32.PAINTSTRUCT
		dc := w32.BeginPaint(hwnd, &paint)
		client := w32.GetClientRect(hwnd)
		memory := w32.CreateCompatibleDC(dc)
		bitmap, _, _ := createCompatibleBitmap.Call(dc, uintptr(client.Right), uintptr(client.Bottom))
		previous := w32.SelectObject(memory, bitmap)
		menu.paint(memory, client.Right, client.Bottom)
		w32.BitBlt(dc, 0, 0, int(client.Right), int(client.Bottom), memory, 0, 0, w32.SRCCOPY)
		w32.SelectObject(memory, previous)
		w32.DeleteObject(bitmap)
		w32.DeleteDC(memory)
		w32.EndPaint(hwnd, &paint)
		return 0
	case w32.WM_PRINTCLIENT:
		client := w32.GetClientRect(hwnd)
		menu.paint(wParam, client.Right, client.Bottom)
		return 0
	case w32.WM_ERASEBKGND:
		return 1
	case w32.WM_DESTROY:
		if menu.child != nil {
			w32.DestroyWindow(menu.child.hwnd)
		}
		w32.DeleteObject(menu.font)
		w32.DeleteObject(menu.glyph)
		if menu.parent != nil {
			menu.parent.child = nil
		} else {
			activeMenu = nil
		}
		return w32.DefWindowProc(hwnd, msg, wParam, lParam)
	}
	if menu.parent != nil {
		return w32.DefWindowProc(hwnd, msg, wParam, lParam)
	}
	switch msg {
	case w32.WM_MOUSEMOVE:
		if !menu.tracking {
			track := w32.TRACKMOUSEEVENT{DwFlags: w32.TME_LEAVE, HwndTrack: hwnd}
			track.CbSize = uint32(unsafe.Sizeof(track))
			menu.tracking = w32.TrackMouseEvent(&track)
		}
		menu.highlight(menu.hit(int32(int16(lParam)), int32(int16(lParam>>16))))
		return 0
	case w32.WM_MOUSELEAVE:
		menu.tracking = false
		if menu.child == nil {
			menu.highlight(-1)
		}
		return 0
	case w32.WM_LBUTTONUP, w32.WM_RBUTTONUP:
		if index := menu.hit(int32(int16(lParam)), int32(int16(lParam>>16))); index >= 0 {
			menu.choose(index)
		}
		return 0
	case w32.WM_KEYDOWN:
		switch wParam {
		case w32.VK_ESCAPE:
			w32.DestroyWindow(hwnd)
		case w32.VK_DOWN:
			menu.highlight(menu.next(menu.hot, 1))
		case w32.VK_UP:
			if menu.hot < 0 {
				menu.highlight(menu.next(0, -1))
			} else {
				menu.highlight(menu.next(menu.hot, -1))
			}
		case w32.VK_HOME:
			menu.highlight(menu.next(-1, 1))
		case w32.VK_END:
			menu.highlight(menu.next(0, -1))
		case w32.VK_RETURN, w32.VK_SPACE:
			if menu.hot >= 0 {
				menu.choose(menu.hot)
			}
		}
		return 0
	case w32.WM_ACTIVATE:
		if wParam&0xffff == w32.WA_INACTIVE {
			w32.PostMessage(hwnd, w32.WM_CLOSE, 0, 0)
		}
	}
	return w32.DefWindowProc(hwnd, msg, wParam, lParam)
}
