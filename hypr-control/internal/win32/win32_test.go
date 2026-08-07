package win32

import (
	"encoding/binary"
	"testing"
)

func TestInputLayout(t *testing.T) {
	// 仅验证 amd64 布局；其他架构测试在对应环境运行。
	if ptrSize != 8 {
		t.Skip("非 64 位环境，跳过布局断言")
	}
	if got := inputSize(); got != 40 {
		t.Fatalf("inputSize() = %d, want 40", got)
	}
	if got := unionOffset(); got != 8 {
		t.Fatalf("unionOffset() = %d, want 8", got)
	}
}

func TestKeyInputLayout(t *testing.T) {
	b := keyInput(VKControl, 0, false)
	if got := binary.LittleEndian.Uint32(b[0:]); got != inputKeyboard {
		t.Fatalf("type = %d, want INPUT_KEYBOARD(%d)", got, inputKeyboard)
	}
	u := unionOffset()
	if got := binary.LittleEndian.Uint16(b[u:]); got != VKControl {
		t.Fatalf("wVk = %#x, want %#x", got, VKControl)
	}
	if got := binary.LittleEndian.Uint32(b[u+4:]); got != 0 {
		t.Fatalf("dwFlags = %#x, want 0 (key down)", got)
	}

	up := keyInput(VKControl, 0, true)
	if got := binary.LittleEndian.Uint32(up[u+4:]); got != keyEventKeyUp {
		t.Fatalf("dwFlags = %#x, want KEYEVENTF_KEYUP(%#x)", got, keyEventKeyUp)
	}
}

func TestMouseInputLayout(t *testing.T) {
	b := mouseInputRaw(-10, 25, 120, mouseEventWheel)
	u := unionOffset()
	if got := int32(binary.LittleEndian.Uint32(b[u:])); got != -10 {
		t.Fatalf("dx = %d, want -10", got)
	}
	if got := int32(binary.LittleEndian.Uint32(b[u+4:])); got != 25 {
		t.Fatalf("dy = %d, want 25", got)
	}
	if got := binary.LittleEndian.Uint32(b[u+8:]); got != 120 {
		t.Fatalf("mouseData = %d, want 120", got)
	}
	if got := binary.LittleEndian.Uint32(b[u+12:]); got != mouseEventWheel {
		t.Fatalf("dwFlags = %#x, want MOUSEEVENTF_WHEEL(%#x)", got, mouseEventWheel)
	}
}

func TestKeyVK(t *testing.T) {
	cases := []struct {
		name string
		want uint16
		ok   bool
	}{
		{"a", 0x41, true},
		{"A", 0x41, true},
		{"9", 0x39, true},
		{"f5", 0x74, true},
		{"F24", 0x87, true},
		{"f25", 0, false},
		{"numpad3", 0x63, true},
		{"ctrl", VKControl, true},
		{"lctrl", VKLCtrl, true},
		{"volumeup", VKVolumeUp, true},
		{"playpause", VKMediaPlayPause, true},
		{"nonsense", 0, false},
	}
	for _, c := range cases {
		got, ok := keyVK(c.name)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("keyVK(%q) = (%#x, %v), want (%#x, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestChordInputs(t *testing.T) {
	down, up, err := chordInputs([]string{"ctrl", "shift", "f5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(down) != 3 || len(up) != 3 {
		t.Fatalf("down/up 长度 = %d/%d, want 3/3", len(down), len(up))
	}
	u := unionOffset()
	readVK := func(b []byte) uint16 { return binary.LittleEndian.Uint16(b[u:]) }
	if readVK(down[0]) != VKControl || readVK(down[1]) != VKShift || readVK(down[2]) != 0x74 {
		t.Fatalf("按下顺序错误: %#x %#x %#x", readVK(down[0]), readVK(down[1]), readVK(down[2]))
	}
	if readVK(up[0]) != 0x74 || readVK(up[1]) != VKShift || readVK(up[2]) != VKControl {
		t.Fatalf("抬起顺序错误（应逆序）: %#x %#x %#x", readVK(up[0]), readVK(up[1]), readVK(up[2]))
	}
	// 抬起事件必须带 KEYUP 标志
	if got := binary.LittleEndian.Uint32(up[0][u+4:]); got != keyEventKeyUp {
		t.Fatalf("抬起事件 dwFlags = %#x, want KEYEVENTF_KEYUP", got)
	}

	if _, _, err := chordInputs([]string{"nonsense"}); err == nil {
		t.Fatal("未知按键应报错")
	}
}

// TestVolumeMediaKeyInputs 验证音量/媒体键输入序列：一次按键 = 按下+抬起两个事件，
// 按键码正确、抬起带 KEYUP 标志（避免真实注入系统操作）。
func TestVolumeMediaKeyInputs(t *testing.T) {
	cases := []struct {
		name string
		seq  [][]byte
		vk   uint16
	}{
		{"volume-up", volumeKeyInputs(VKVolumeUp), VKVolumeUp},
		{"volume-down", volumeKeyInputs(VKVolumeDown), VKVolumeDown},
		{"volume-mute", volumeKeyInputs(VKVolumeMute), VKVolumeMute},
		{"media-playpause", mediaKeyInputs(VKMediaPlayPause), VKMediaPlayPause},
		{"media-next", mediaKeyInputs(VKMediaNext), VKMediaNext},
		{"media-prev", mediaKeyInputs(VKMediaPrev), VKMediaPrev},
		{"media-stop", mediaKeyInputs(VKMediaStop), VKMediaStop},
	}
	u := unionOffset()
	for _, c := range cases {
		if len(c.seq) != 2 {
			t.Fatalf("%s: 事件数 = %d, want 2（按下+抬起）", c.name, len(c.seq))
		}
		if got := binary.LittleEndian.Uint16(c.seq[0][u:]); got != c.vk {
			t.Errorf("%s: 按下事件 VK = %#x, want %#x", c.name, got, c.vk)
		}
		if got := binary.LittleEndian.Uint32(c.seq[0][u+4:]); got != 0 {
			t.Errorf("%s: 按下事件 flags = %#x, want 0", c.name, got)
		}
		if got := binary.LittleEndian.Uint32(c.seq[1][u+4:]); got != keyEventKeyUp {
			t.Errorf("%s: 抬起事件 flags = %#x, want KEYEVENTF_KEYUP", c.name, got)
		}
	}
}
