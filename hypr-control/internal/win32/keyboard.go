package win32

import "fmt"

// KeyTap 模拟单键的一次按下与释放。
func KeyTap(name string) error {
	return Chord([]string{name})
}

// Chord 模拟组合键：所有键同时按下，再全部释放（逆序抬起）。
// 例如 Chord([]string{"ctrl", "c"}) 触发 Ctrl+C。
// 注意：Ctrl+Alt+Del 是系统安全序列，SendInput 无法模拟。
func Chord(names []string) error {
	down, up, err := chordInputs(names)
	if err != nil {
		return err
	}
	if err := sendInputs(down); err != nil {
		return err
	}
	return sendInputs(up)
}

// chordInputs 生成组合键的按下/抬起输入序列（纯函数，便于测试）。
func chordInputs(names []string) (down, up [][]byte, err error) {
	vks := make([]uint16, 0, len(names))
	for _, n := range names {
		vk, ok := keyVK(n)
		if !ok {
			return nil, nil, fmt.Errorf("未知按键: %q", n)
		}
		vks = append(vks, vk)
	}
	if len(vks) == 0 {
		return nil, nil, fmt.Errorf("按键列表为空")
	}

	for _, vk := range vks {
		down = append(down, keyInput(vk, 0, false))
	}
	for i := len(vks) - 1; i >= 0; i-- {
		up = append(up, keyInput(vks[i], 0, true))
	}
	return down, up, nil
}
