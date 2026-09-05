package win32

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindow                 = user32.NewProc("IsWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetWindowLongW           = user32.NewProc("GetWindowLongW")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procShowWindow               = user32.NewProc("ShowWindow")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procSwitchToThisWindow       = user32.NewProc("SwitchToThisWindow")

	dwmapi                    = syscall.NewLazyDLL("dwmapi.dll")
	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	wsExToolWindow = 0x00000080
	swRestore      = 0x09
	swMinimize     = 0x06
	dwmwaCloaked   = 14 // DWMWA_CLOAKED：隐藏的 UWP/挂起窗口
)

// shellClasses 是资源管理器外壳窗口类名，不属于可切换的应用窗口。
var shellClasses = map[string]bool{
	"Shell_TrayWnd":              true, // 任务栏
	"Progman":                    true, // 桌面
	"WorkerW":                    true, // 桌面壁纸层
	"Windows.UI.Core.CoreWindow": true, // 系统 UWP 托管 UI（开始菜单/搜索等）
}

// WindowInfo 是一个可切换的顶层窗口。
type WindowInfo struct {
	Handle uint64 // 窗口句柄（对手机端为不透明 ID，激活时原样传回）
	Title  string
	Active bool // 是否为当前前台窗口
}

// ListWindows 按枚举顺序（Z 序，前台窗口在前）返回可切换的应用窗口，
// 语义接近 Alt+Tab 列表：可见、有标题、非工具窗口、未 cloak、非外壳窗口。
func ListWindows() []WindowInfo {
	var out []WindowInfo
	fg, _, _ := procGetForegroundWindow.Call()

	cb := syscall.NewCallback(func(hwnd, lParam uintptr) uintptr {
		if !windowSwitchable(hwnd) {
			return 1 // 继续枚举
		}
		out = append(out, WindowInfo{
			Handle: uint64(hwnd),
			Title:  windowTitle(hwnd),
			Active: hwnd == fg,
		})
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return out
}

// FocusWindow 激活指定窗口（句柄来自 ListWindows）。
// Windows 限制后台进程直接抢前台（SetForegroundWindow 会失败/只闪任务栏），
// 依次尝试：
//  1. AttachThreadInput 同时附加前台窗口与目标窗口的输入线程；
//  2. 模拟一次 ALT 键——系统把前台权限授予最近接收过输入的进程；
//  3. SwitchToThisWindow（未公开 API，任务栏切换也用它）；
//  4. 最小化再还原（还原操作自带激活语义）。
func FocusWindow(handle uint64) error {
	h := uintptr(handle)
	if r1, _, _ := procIsWindow.Call(h); r1 == 0 {
		return fmt.Errorf("窗口已关闭，请刷新列表")
	}
	if r1, _, _ := procIsIconic.Call(h); r1 != 0 {
		procShowWindow.Call(h, swRestore) // 最小化先还原
	}

	threadOf := func(w uintptr) uintptr {
		t, _, _ := procGetWindowThreadProcessId.Call(w, 0)
		return t
	}
	curThread, _, _ := procGetCurrentThreadId.Call()
	var attached []uintptr
	attach := func(t uintptr) {
		if t != 0 && t != curThread {
			if r, _, _ := procAttachThreadInput.Call(curThread, t, 1); r != 0 {
				attached = append(attached, t)
			}
		}
	}
	isForeground := func() bool {
		r, _, _ := procGetForegroundWindow.Call()
		return r == h
	}

	fg, _, _ := procGetForegroundWindow.Call()
	attach(threadOf(fg))
	attach(threadOf(h))
	procSetForegroundWindow.Call(h)
	ok := isForeground()
	if !ok {
		sendAltTap()
		procSetForegroundWindow.Call(h)
		ok = isForeground()
	}
	for _, t := range attached {
		procAttachThreadInput.Call(curThread, t, 0)
	}

	if !ok {
		procSwitchToThisWindow.Call(h, 1)
		ok = isForeground()
	}
	if !ok {
		procShowWindow.Call(h, swMinimize)
		procShowWindow.Call(h, swRestore)
		ok = isForeground()
	}
	if !ok {
		return fmt.Errorf("系统拒绝了前台切换（该窗口可能处于全屏独占/提权状态），请重试")
	}
	return nil
}

// sendAltTap 模拟一次 ALT 按下+抬起，使本进程获得前台切换许可
// （系统把 SetForegroundWindow 权限授予最近接收输入的进程）。
func sendAltTap() {
	sendInputs([][]byte{keyInput(VKMenu, 0, false)})
	sendInputs([][]byte{keyInput(VKMenu, 0, true)})
}

// windowSwitchable 判断窗口是否值得出现在切换列表中。
func windowSwitchable(hwnd uintptr) bool {
	if r, _, _ := procIsWindowVisible.Call(hwnd); r == 0 {
		return false
	}
	if r, _, _ := procGetWindowTextLengthW.Call(hwnd); r == 0 {
		return false // 无标题（不可见辅助窗口）
	}
	// GWL_EXSTYLE = -20；Call 只收 uintptr，用补码表示负值
	if ex, _, _ := procGetWindowLongW.Call(hwnd, ^uintptr(19)); ex&wsExToolWindow != 0 {
		return false // 浮动工具条等小窗
	}
	if cloaked(hwnd) {
		return false // 已挂起/隐藏的 UWP 宿主窗口
	}
	cls := windowClass(hwnd)
	if shellClasses[cls] {
		return false
	}
	return true
}

// cloaked 报告窗口是否被 DWM cloak（UWP 挂起时窗口仍在但不可见）。
// 非 DWM 窗口查询失败时视为未 cloak。
func cloaked(hwnd uintptr) bool {
	if procDwmGetWindowAttribute.Find() != nil {
		return false
	}
	var val uint32
	r, _, _ := procDwmGetWindowAttribute.Call(hwnd, dwmwaCloaked,
		uintptr(unsafe.Pointer(&val)), unsafe.Sizeof(val))
	return r == 0 && val != 0
}

// windowTitle 读取窗口标题文本。
func windowTitle(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

// windowClass 读取窗口类名（用于过滤外壳窗口）。
func windowClass(hwnd uintptr) string {
	buf := make([]uint16, 64)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}
