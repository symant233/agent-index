package win32

import (
	"math"
	"testing"
	"unsafe"
)

// TestParseGUID 验证 GUID 字符串解析（用 mmdeviceapi 的真实 CLSID/IID）。
func TestParseGUID(t *testing.T) {
	cases := []struct {
		str   string
		data1 uint32
		data2 uint16
		data3 uint16
		data4 [8]byte
	}{
		{"{BCDE0395-E52F-467C-8E3D-C4579291692E}", 0xBCDE0395, 0xE52F, 0x467C, [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}},
		{"{A95664D2-9614-4F35-A746-DE8DB63617E6}", 0xA95664D2, 0x9614, 0x4F35, [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}},
		{"{5CDF2C82-841E-4546-9722-0CF74078229A}", 0x5CDF2C82, 0x841E, 0x4546, [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A}},
	}
	for _, c := range cases {
		g := parseGUID(c.str)
		if g.Data1 != c.data1 || g.Data2 != c.data2 || g.Data3 != c.data3 || g.Data4 != c.data4 {
			t.Errorf("parseGUID(%s) = %+v, want data1=%08x data2=%04x data3=%04x data4=% x",
				c.str, g, c.data1, c.data2, c.data3, c.data4)
		}
	}
}

// TestVolumePercentLevelRoundTrip 验证百分比与 COM 标量的换算及钳位。
func TestVolumePercentLevelRoundTrip(t *testing.T) {
	cases := []struct {
		percent float64
		want    float32
	}{
		{0, 0.0},
		{20, 0.2},
		{37.5, 0.375},
		{100, 1.0},
		{-5, 0.0},  // 下界钳位
		{150, 1.0}, // 上界钳位
	}
	for _, c := range cases {
		got := volumePercentToLevel(c.percent)
		if math.Abs(float64(got-c.want)) > 1e-9 {
			t.Errorf("volumePercentToLevel(%v) = %v, want %v", c.percent, got, c.want)
		}
	}
	// 往返：任意 0-100 值往返误差可忽略
	for _, p := range []float64{0, 1, 12.5, 20, 55, 99, 100} {
		back := volumeLevelToPercent(volumePercentToLevel(p))
		if math.Abs(back-p) > 0.01 {
			t.Errorf("往返 %v%% → %v%%", p, back)
		}
	}
}

// TestMsgLayout 验证 MSG 结构布局（amd64 下 48 字节）。
func TestMsgLayout(t *testing.T) {
	if ptrSize != 8 {
		t.Skip("非 64 位环境，跳过布局断言")
	}
	if got := unsafe.Sizeof(msgW{}); got != 48 {
		t.Fatalf("sizeof(MSG) = %d, want 48", got)
	}
}

// TestWCESize 验证 WNDCLASSEXW 结构布局。
func TestWCESize(t *testing.T) {
	if ptrSize != 8 {
		t.Skip("非 64 位环境，跳过布局断言")
	}
	// amd64: 4+4+8+4+4(+4对齐)+handle*5(40)+ptr*2(16)=80
	if got := unsafe.Sizeof(wndClassExW{}); got != 80 {
		t.Fatalf("sizeof(WNDCLASSEXW) = %d, want 80", got)
	}
}
