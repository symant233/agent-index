package win32

// 音量控制通过 SendInput 发送系统音量虚拟键（与真实按键盘音量键一致），
// 由系统统一处理，一次按键只步进/切换一次。
//
// 不使用 WM_APPCOMMAND 广播：广播会发给所有顶层窗口，多个响应者
// （音量弹窗、任务栏、第三方音频组件等）各自处理一次，导致
// 一次操作重复触发多次（音量疯狂跳动、静音反复切换）。

// volumeKeyInputs 生成一次音量键的按下+抬起输入序列（纯函数，便于测试）。
func volumeKeyInputs(vk uint16) [][]byte {
	return [][]byte{
		keyInput(vk, 0, false),
		keyInput(vk, 0, true),
	}
}

// VolumeUp 模拟按一次音量+键（系统默认步进）。
func VolumeUp() error { return sendInputs(volumeKeyInputs(VKVolumeUp)) }

// VolumeDown 模拟按一次音量-键（系统默认步进）。
func VolumeDown() error { return sendInputs(volumeKeyInputs(VKVolumeDown)) }

// VolumeMute 模拟按一次静音键，由系统切换静音/解除静音。
func VolumeMute() error { return sendInputs(volumeKeyInputs(VKVolumeMute)) }
