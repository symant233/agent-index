// Package win32 通过 syscall 直接调用 user32.dll，
// 提供键盘/鼠标模拟、锁屏、音量与媒体控制。
package win32

import "syscall"

var (
	user32              = syscall.NewLazyDLL("user32.dll")
	procSendInput       = user32.NewProc("SendInput")
	procSetCursorPos    = user32.NewProc("SetCursorPos")
	procGetCursorPos    = user32.NewProc("GetCursorPos")
	procLockWorkStation = user32.NewProc("LockWorkStation")
)

// 常用虚拟键码（Virtual-Key Codes）。
const (
	VKBack         = 0x08
	VKTab          = 0x09
	VKReturn       = 0x0D
	VKShift        = 0x10
	VKControl      = 0x11
	VKMenu         = 0x12
	VKPause        = 0x13
	VKCapital      = 0x14
	VKEscape       = 0x1B
	VKSpace        = 0x20
	VKPrior        = 0x21 // PageUp
	VKNext         = 0x22 // PageDown
	VKEnd          = 0x23
	VKHome         = 0x24
	VKLeft         = 0x25
	VKUp           = 0x26
	VKRight        = 0x27
	VKDown         = 0x28
	VKSnapshot     = 0x2C // PrintScreen
	VKInsert       = 0x2D
	VKDelete       = 0x2E
	VKLWin         = 0x5B
	VKRWin         = 0x5C
	VKApps         = 0x5D // 应用键（右键菜单）
	VKNumlock      = 0x90
	VKScroll       = 0x91
	VKLShift       = 0xA0
	VKRShift       = 0xA1
	VKLCtrl        = 0xA2
	VKRCtrl        = 0xA3
	VKLAlt         = 0xA4
	VKRAlt         = 0xA5
	VKVolumeMute   = 0xAD
	VKVolumeDown   = 0xAE
	VKVolumeUp     = 0xAF
	VKMediaNext    = 0xB0
	VKMediaPrev    = 0xB1
	VKMediaStop    = 0xB2
	VKMediaPlayPause = 0xB3
)

// VKF1..VKF24 与 VKNumpad0..9 由 keyVK 中直接计算，不在此列出。
