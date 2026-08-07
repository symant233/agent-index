// Package control 定义主机遥控操作的统一接口 Backend，
// 真实实现基于 internal/win32（Windows），测试中可替换为 mock。
package control

import "hypr-control/internal/win32"

// Backend 描述服务端能对主机执行的遥控操作。
type Backend interface {
	// 键盘
	KeyTap(name string) error          // 单键：如 "a"、"f5"、"enter"
	Chord(names []string) error        // 组合键：如 ["ctrl","c"]
	// 鼠标（坐标为屏幕像素）
	MouseMoveRel(dx, dy int32) error   // 相对移动
	MouseMoveTo(x, y int) error        // 绝对定位
	MouseClick(button string) error    // left / right / middle
	MouseScroll(delta int32) error     // 滚轮，±120
	// 系统
	Lock() error                       // 锁屏
	VolumeUp() error
	VolumeDown() error
	VolumeMute() error
	MediaPlayPause() error
	MediaNext() error
	MediaPrev() error
	MediaStop() error
}

// Default 返回基于 user32.dll 的真实 Windows 后端。
func Default() Backend { return winBackend{} }

type winBackend struct{}

func (winBackend) KeyTap(name string) error { return win32.KeyTap(name) }
func (winBackend) Chord(names []string) error { return win32.Chord(names) }
func (winBackend) MouseMoveRel(dx, dy int32) error { return win32.MouseMoveRel(dx, dy) }
func (winBackend) MouseMoveTo(x, y int) error { return win32.MouseMoveTo(x, y) }
func (winBackend) MouseClick(button string) error { return win32.MouseClick(button) }
func (winBackend) MouseScroll(delta int32) error { return win32.MouseScroll(delta) }
func (winBackend) Lock() error { return win32.LockWorkstation() }
func (winBackend) VolumeUp() error { return win32.VolumeUp() }
func (winBackend) VolumeDown() error { return win32.VolumeDown() }
func (winBackend) VolumeMute() error { return win32.VolumeMute() }
func (winBackend) MediaPlayPause() error { return win32.MediaPlayPause() }
func (winBackend) MediaNext() error { return win32.MediaNext() }
func (winBackend) MediaPrev() error { return win32.MediaPrev() }
func (winBackend) MediaStop() error { return win32.MediaStop() }
