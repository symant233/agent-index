package win32

import (
	"fmt"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procSetClipboardData = user32.NewProc("SetClipboardData")

	procGlobalAlloc  = kernel32.NewProc("GlobalAlloc")
	procGlobalLock   = kernel32.NewProc("GlobalLock")
	procGlobalUnlock = kernel32.NewProc("GlobalUnlock")
	procGlobalFree   = kernel32.NewProc("GlobalFree")
)

const (
	cfUnicodeText = 13 // CF_UNICODETEXT
	gmemMoveable  = 0x0002
)

// ClipboardGet 读取主机剪贴板的文本内容（CF_UNICODETEXT）。
// 剪贴板为空或不含文本时返回 "" 而非错误。
func ClipboardGet() (string, error) {
	if err := openClipboard(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil // 为空或非文本格式（图片/文件等）
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", fmt.Errorf("GlobalLock: 锁定剪贴板内存失败")
	}
	defer procGlobalUnlock.Call(h)

	return utf16PtrToString(p), nil
} // ClipboardSet 把文本写入主机剪贴板（替换现有内容）。
// 成功后内存所有权移交系统；失败路径自行释放，避免泄漏。
func ClipboardSet(text string) error {
	buf := utf16.Encode([]rune(text))
	u16 := make([]uint16, 0, len(buf)+1)
	u16 = append(u16, buf...)
	u16 = append(u16, 0)
	size := uintptr(len(u16)) * 2

	h, _, _ := procGlobalAlloc.Call(gmemMoveable, size)
	if h == 0 {
		return fmt.Errorf("GlobalAlloc: 分配剪贴板内存失败")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock: 锁定剪贴板内存失败")
	}
	dst := unsafe.Pointer(p)
	for i, c := range u16 {
		*(*uint16)(unsafe.Add(dst, uintptr(i)*2)) = c
	}
	procGlobalUnlock.Call(h)

	if err := openClipboard(); err != nil {
		procGlobalFree.Call(h)
		return err
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	r1, _, _ := procSetClipboardData.Call(cfUnicodeText, h)
	if r1 == 0 {
		procGlobalFree.Call(h) // SetClipboardData 失败时所有权仍在己方，需释放
		return fmt.Errorf("SetClipboardData: 写入剪贴板失败")
	}
	return nil // 成功后 h 归系统所有，不要 GlobalFree
}

// openClipboard 打开剪贴板，被其他进程短暂占用时重试。
func openClipboard() error {
	const tries, delay = 20, 10 * time.Millisecond
	for i := 0; i < tries; i++ {
		if r1, _, _ := procOpenClipboard.Call(0); r1 != 0 {
			return nil
		}
		time.Sleep(delay)
	}
	return fmt.Errorf("OpenClipboard: 剪贴板被其他进程占用")
}

// utf16PtrToString 读取 null 结尾的 UTF-16 字符串（剪贴板内存格式）。
func utf16PtrToString(p uintptr) string {
	if p == 0 {
		return ""
	}
	ptr := unsafe.Pointer(p)
	n := 0
	for *(*uint16)(unsafe.Add(ptr, uintptr(n)*2)) != 0 {
		n++
	}
	return string(utf16.Decode(unsafe.Slice((*uint16)(ptr), n)))
}
