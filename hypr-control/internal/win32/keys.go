package win32

import "strings"

// keyVK 把按键名解析为虚拟键码。名称大小写不敏感。
// 支持：单字符 a-z、0-9；修饰键与功能键见下表；F1-F24；numpad0-9。
func keyVK(name string) (uint16, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if len(n) == 1 {
		if c := n[0]; c >= 'a' && c <= 'z' {
			return uint16(c - 'a' + 'A'), true
		}
		if c := n[0]; c >= '0' && c <= '9' {
			return uint16(c), true // '0'=0x30 .. '9'=0x39
		}
	}
	if strings.HasPrefix(n, "f") && len(n) > 1 {
		if num := atoi(n[1:]); num >= 1 && num <= 24 {
			return uint16(VKF1 - 1 + num), true // VK_F1=0x70
		}
	}
	if strings.HasPrefix(n, "numpad") {
		if num := atoi(n[6:]); num >= 0 && num <= 9 {
			return uint16(VKNumpad0 + num), true // VK_NUMPAD0=0x60
		}
	}

	table := map[string]uint16{
		"backspace":  VKBack,
		"back":       VKBack,
		"tab":        VKTab,
		"enter":      VKReturn,
		"return":     VKReturn,
		"shift":      VKShift,
		"lshift":     VKLShift,
		"rshift":     VKRShift,
		"ctrl":       VKControl,
		"control":    VKControl,
		"lctrl":      VKLCtrl,
		"rctrl":      VKRCtrl,
		"alt":        VKMenu,
		"lalt":       VKLAlt,
		"ralt":       VKRAlt,
		"pause":      VKPause,
		"capslock":   VKCapital,
		"esc":        VKEscape,
		"escape":     VKEscape,
		"space":      VKSpace,
		"pgup":       VKPrior,
		"pageup":     VKPrior,
		"prior":      VKPrior,
		"pgdn":       VKNext,
		"pagedown":   VKNext,
		"next":       VKNext,
		"end":        VKEnd,
		"home":       VKHome,
		"left":       VKLeft,
		"up":         VKUp,
		"right":      VKRight,
		"down":       VKDown,
		"printscreen": VKSnapshot,
		"prtsc":      VKSnapshot,
		"insert":     VKInsert,
		"delete":     VKDelete,
		"del":        VKDelete,
		"win":        VKLWin,
		"lwin":       VKLWin,
		"rwin":       VKRWin,
		"menu":       VKApps,
		"apps":       VKApps,
		"numlock":    VKNumlock,
		"scrolllock": VKScroll,
		"volumemute": VKVolumeMute,
		"volumedown": VKVolumeDown,
		"volumeup":   VKVolumeUp,
		"medianext":  VKMediaNext,
		"mediaprev":  VKMediaPrev,
		"mediastop":  VKMediaStop,
		"playpause":  VKMediaPlayPause,
	}
	vk, ok := table[n]
	return vk, ok
}

// VK_F1 与 VK_NUMPAD0 起始值。
const (
	VKF1       = 0x70
	VKNumpad0  = 0x60
)

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return -1
		}
		n = n*10 + int(c-'0')
	}
	return n
}
