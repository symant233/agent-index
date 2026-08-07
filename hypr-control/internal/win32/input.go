package win32

import (
	"encoding/binary"
	"fmt"
	"unsafe"
)

// INPUT 结构类型常量。
const (
	inputMouse    = 0
	inputKeyboard = 1
	inputHardware = 2

	// keybdInput.dwFlags 取值
	keyEventKeyUp   = 0x0002
	keyEventUnicode = 0x0004
)

var ptrSize = int(unsafe.Sizeof(uintptr(0)))

// inputSize 返回 C 的 sizeof(INPUT)：
//
//	amd64: type(4) + pad(4) + union(32) = 40
//	386:   type(4) + union(24)          = 28
func inputSize() int {
	if ptrSize == 8 {
		return 40
	}
	return 28
}

// unionOffset 返回 INPUT 中 union 的起始偏移。
func unionOffset() int {
	if ptrSize == 8 {
		return 8
	}
	return 4
}

// unionSize 返回 INPUT 中 union 的大小（MOUSEINPUT 最大）。
func unionSize() int {
	if ptrSize == 8 {
		return 32
	}
	return 24
}

// buildInput 组装一个 INPUT 结构（字节视图），union 部分由调用方按
// MOUSEINPUT 布局构造（其为最大成员，KEYBDINPUT 布局是其前缀子集）。
func buildInput(typ uint32, union []byte) []byte {
	buf := make([]byte, inputSize())
	binary.LittleEndian.PutUint32(buf[0:], typ)
	copy(buf[unionOffset():], union[:min(len(union), unionSize())])
	return buf
}

// keyInput 构造键盘输入。vk 非零时用 VK 模式；scan 非零时用扫描码模式。
func keyInput(vk uint16, scan uint16, keyUp bool) []byte {
	var u [32]byte
	binary.LittleEndian.PutUint16(u[0:], vk)
	binary.LittleEndian.PutUint16(u[2:], scan)
	flags := uint32(0)
	if vk == 0 {
		flags |= keyEventUnicode
	}
	if keyUp {
		flags |= keyEventKeyUp
	}
	binary.LittleEndian.PutUint32(u[4:], flags)
	// time=0（由系统填充），dwExtraInfo=0
	return buildInput(inputKeyboard, u[:])
}

// mouseInputRaw 构造鼠标输入。flags 为 MOUSEEVENTF_* 组合。
func mouseInputRaw(dx, dy int32, mouseData, flags uint32) []byte {
	var u [32]byte
	binary.LittleEndian.PutUint32(u[0:], uint32(dx))
	binary.LittleEndian.PutUint32(u[4:], uint32(dy))
	binary.LittleEndian.PutUint32(u[8:], mouseData)
	binary.LittleEndian.PutUint32(u[12:], flags)
	return buildInput(inputMouse, u[:])
}

// sendInputs 批量注入输入事件。items 合并为连续缓冲区后一次 SendInput 提交。
func sendInputs(items [][]byte) error {
	if len(items) == 0 {
		return nil
	}
	var buf []byte
	for _, it := range items {
		buf = append(buf, it...)
	}
	r1, _, err := procSendInput.Call(
		uintptr(len(items)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(inputSize()),
	)
	if int(r1) != len(items) {
		return fmt.Errorf("SendInput: 期望注入 %d 个输入，实际 %d (%v)", len(items), int(r1), err)
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
