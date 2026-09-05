package win32

import "testing"

// TestClipboardRoundTrip 真实读写剪贴板并恢复原内容（-short 模式跳过）。
func TestClipboardRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真实剪贴板操作")
	}
	orig, err := ClipboardGet()
	if err != nil {
		t.Fatalf("读取原剪贴板: %v", err)
	}
	if orig != "" {
		t.Cleanup(func() { ClipboardSet(orig) }) // 结束后恢复原内容
	}

	const want = "hctrl 剪贴板测试 ABC123"
	if err := ClipboardSet(want); err != nil {
		t.Fatalf("写入剪贴板: %v", err)
	}
	got, err := ClipboardGet()
	if err != nil {
		t.Fatalf("读取剪贴板: %v", err)
	}
	if got != want {
		t.Fatalf("往返内容不一致: got %q, want %q", got, want)
	}
}

// TestKeyVKSymbols 验证符号键名与单字符映射到正确的 OEM 虚拟键码。
func TestKeyVKSymbols(t *testing.T) {
	cases := []struct {
		name string
		want uint16
	}{
		{"-", VKOemMinus}, {"minus", VKOemMinus},
		{"=", VKOemPlus}, {"equal", VKOemPlus},
		{"[", VKOemOpenBrackets}, {"bracketleft", VKOemOpenBrackets},
		{"]", VKOemCloseBrackets}, {"bracketright", VKOemCloseBrackets},
		{"\\", VKOemPipe}, {"backslash", VKOemPipe},
		{";", VKOemSemicolon}, {"semicolon", VKOemSemicolon},
		{"'", VKOemQuotes}, {"quote", VKOemQuotes},
		{",", VKOemComma}, {".", VKOemPeriod},
		{"/", VKOemQuestion}, {"`", VKOemTilde},
	}
	for _, tc := range cases {
		vk, ok := keyVK(tc.name)
		if !ok || vk != tc.want {
			t.Errorf("keyVK(%q) = (%#x, %v), want (%#x, true)", tc.name, vk, ok, tc.want)
		}
	}
	// Shift 变体字符（如 "!"）不直接映射，由调用方组合 shift+1
	if _, ok := keyVK("!"); ok {
		t.Error(`keyVK("!") 应不可解析（shift 变体由调用方组合）`)
	}
}

// TestListWindowsSmoke 枚举真实窗口：至少 1 个、句柄与标题非空。
func TestListWindowsSmoke(t *testing.T) {
	ws := ListWindows()
	if len(ws) == 0 {
		t.Fatal("窗口列表为空（至少应有测试运行自身的终端窗口）")
	}
	for _, w := range ws {
		if w.Handle == 0 {
			t.Fatalf("存在零句柄窗口: %+v", w)
		}
		if w.Title == "" {
			t.Fatalf("存在空标题窗口: %+v", w)
		}
	}
	t.Logf("枚举到 %d 个窗口，首个: %q (active=%v)", len(ws), ws[0].Title, ws[0].Active)
}
