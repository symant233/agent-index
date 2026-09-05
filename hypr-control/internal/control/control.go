// Package control 定义主机遥控操作的统一接口 Backend，
// 真实实现基于 internal/win32（Windows），测试中可替换为 mock。
package control

import "hypr-control/internal/win32"

// Backend 描述服务端能对主机执行的遥控操作。
type Backend interface {
	// 键盘
	KeyTap(name string) error   // 单键：如 "a"、"f5"、"enter"
	Chord(names []string) error // 组合键：如 ["ctrl","c"]
	// 鼠标（坐标为屏幕像素）
	MouseMoveRel(dx, dy int32) error // 相对移动
	MouseMoveTo(x, y int) error      // 绝对定位
	MouseClick(button string) error  // left / right / middle
	MouseScroll(delta int32) error   // 滚轮，±120
	MouseHScroll(delta int32) error  // 水平滚动，正右负左
	MouseDown(button string) error   // 按下不释放（拖拽用，与 MouseUp 配对）
	MouseUp(button string) error     // 释放（与 MouseDown 配对）
	// 系统
	Lock() error          // 锁屏
	PowerShutdown() error // 立即关机
	PowerRestart() error  // 立即重启
	VolumeUp() error
	VolumeDown() error
	VolumeMute() error
	MediaPlayPause() error
	MediaNext() error
	MediaPrev() error
	MediaStop() error
	// 剪贴板
	ClipboardGet() (string, error) // 读取主机剪贴板文本（空/非文本返回 ""）
	ClipboardSet(text string) error
	// 窗口
	ListWindows() ([]WindowInfo, error) // 可切换窗口列表（Z 序，前台在前）
	FocusWindow(handle uint64) error    // 激活指定窗口（句柄来自 ListWindows）
}

// WindowInfo 是一个可切换的顶层窗口。Handle 对客户端为不透明 ID，
// 激活时原样传回即可（窗口关闭后服务端会返回明确错误）。
type WindowInfo struct {
	Handle uint64 `json:"handle"`
	Title  string `json:"title"`
	Active bool   `json:"active"`
}

// Default 返回基于 user32.dll 的真实 Windows 后端。
func Default() Backend { return winBackend{} }

type winBackend struct{}

func (winBackend) KeyTap(name string) error        { return win32.KeyTap(name) }
func (winBackend) Chord(names []string) error      { return win32.Chord(names) }
func (winBackend) MouseMoveRel(dx, dy int32) error { return win32.MouseMoveRel(dx, dy) }
func (winBackend) MouseMoveTo(x, y int) error      { return win32.MouseMoveTo(x, y) }
func (winBackend) MouseClick(button string) error  { return win32.MouseClick(button) }
func (winBackend) MouseScroll(delta int32) error   { return win32.MouseScroll(delta) }
func (winBackend) MouseHScroll(delta int32) error  { return win32.MouseHScroll(delta) }
func (winBackend) MouseDown(button string) error   { return win32.MouseDown(button) }
func (winBackend) MouseUp(button string) error     { return win32.MouseUp(button) }
func (winBackend) Lock() error                     { return win32.LockWorkstation() }
func (winBackend) PowerShutdown() error            { return win32.Shutdown(0) }
func (winBackend) PowerRestart() error             { return win32.Restart(0) }
func (winBackend) VolumeUp() error                 { return win32.VolumeUp() }
func (winBackend) VolumeDown() error               { return win32.VolumeDown() }
func (winBackend) VolumeMute() error               { return win32.VolumeMute() }
func (winBackend) MediaPlayPause() error           { return win32.MediaPlayPause() }
func (winBackend) MediaNext() error                { return win32.MediaNext() }
func (winBackend) MediaPrev() error                { return win32.MediaPrev() }
func (winBackend) MediaStop() error                { return win32.MediaStop() }
func (winBackend) ClipboardGet() (string, error)   { return win32.ClipboardGet() }
func (winBackend) ClipboardSet(text string) error  { return win32.ClipboardSet(text) }

func (winBackend) ListWindows() ([]WindowInfo, error) {
	ws := win32.ListWindows()
	out := make([]WindowInfo, 0, len(ws))
	for _, w := range ws {
		out = append(out, WindowInfo(w))
	}
	return out, nil
}
func (winBackend) FocusWindow(handle uint64) error { return win32.FocusWindow(handle) }
