package win32

// 媒体控制通过 SendInput 发送系统媒体虚拟键（与真实键盘媒体键一致），
// 由系统统一处理一次，避免 WM_APPCOMMAND 广播被多个窗口重复响应。

// mediaKeyInputs 生成一次媒体键的按下+抬起输入序列（纯函数，便于测试）。
func mediaKeyInputs(vk uint16) [][]byte {
	return [][]byte{
		keyInput(vk, 0, false),
		keyInput(vk, 0, true),
	}
}

// MediaPlayPause 播放/暂停切换。
func MediaPlayPause() error { return sendInputs(mediaKeyInputs(VKMediaPlayPause)) }

// MediaNext 下一曲。
func MediaNext() error { return sendInputs(mediaKeyInputs(VKMediaNext)) }

// MediaPrev 上一曲。
func MediaPrev() error { return sendInputs(mediaKeyInputs(VKMediaPrev)) }

// MediaStop 停止。
func MediaStop() error { return sendInputs(mediaKeyInputs(VKMediaStop)) }
