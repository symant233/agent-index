package win32

import (
	"fmt"
	"unsafe"
)

// MOUSEEVENTF_* 取值。
const (
	mouseEventMove       = 0x0001
	mouseEventLeftDown   = 0x0002
	mouseEventLeftUp     = 0x0004
	mouseEventRightDown  = 0x0008
	mouseEventRightUp    = 0x0010
	mouseEventMiddleDown = 0x0020
	mouseEventMiddleUp   = 0x0040
	mouseEventWheel      = 0x0800
	mouseEventHWheel     = 0x1000
	mouseEventAbsolute   = 0x8000
)

// mouseButtonFlags 把按键名解析为按下/抬起事件标志（纯函数，便于测试）。
func mouseButtonFlags(button string) (down, up uint32, err error) {
	switch button {
	case "left":
		return mouseEventLeftDown, mouseEventLeftUp, nil
	case "right":
		return mouseEventRightDown, mouseEventRightUp, nil
	case "middle":
		return mouseEventMiddleDown, mouseEventMiddleUp, nil
	default:
		return 0, 0, fmt.Errorf("未知鼠标按键: %q", button)
	}
}

// MouseMoveRel 相对当前指针位置移动 (dx, dy) 像素。
func MouseMoveRel(dx, dy int32) error {
	return sendInputs([][]byte{mouseInputRaw(dx, dy, 0, mouseEventMove)})
}

// MouseMoveTo 把指针移动到屏幕绝对坐标 (x, y)。
func MouseMoveTo(x, y int) error {
	r1, _, err := procSetCursorPos.Call(uintptr(x), uintptr(y))
	if r1 == 0 {
		return fmt.Errorf("SetCursorPos(%d,%d): %v", x, y, err)
	}
	return nil
}

// MouseClick 点击指定按键：left / right / middle（按下+抬起）。
func MouseClick(button string) error {
	down, up, err := mouseButtonFlags(button)
	if err != nil {
		return err
	}
	return sendInputs([][]byte{
		mouseInputRaw(0, 0, 0, down),
		mouseInputRaw(0, 0, 0, up),
	})
}

// MouseDown 按下指定按键不释放（用于拖拽；与 MouseUp 配对）。
func MouseDown(button string) error {
	down, _, err := mouseButtonFlags(button)
	if err != nil {
		return err
	}
	return sendInputs([][]byte{mouseInputRaw(0, 0, 0, down)})
}

// MouseUp 释放指定按键（与 MouseDown 配对）。
func MouseUp(button string) error {
	_, up, err := mouseButtonFlags(button)
	if err != nil {
		return err
	}
	return sendInputs([][]byte{mouseInputRaw(0, 0, 0, up)})
}

// MouseScroll 滚动滚轮。delta 为正向上、负向下，建议 ±120。
func MouseScroll(delta int32) error {
	return sendInputs([][]byte{mouseInputRaw(0, 0, uint32(delta), mouseEventWheel)})
}

// MouseHScroll 水平滚动。delta 为正向右、负向左，建议 ±120。
func MouseHScroll(delta int32) error {
	return sendInputs([][]byte{mouseInputRaw(0, 0, uint32(delta), mouseEventHWheel)})
}

// GetCursorPos 读取当前指针位置（屏幕坐标）。
func GetCursorPos() (x, y int, err error) {
	var pt struct{ X, Y int32 }
	r1, _, e := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	if r1 == 0 {
		return 0, 0, fmt.Errorf("GetCursorPos: %v", e)
	}
	return int(pt.X), int(pt.Y), nil
}
