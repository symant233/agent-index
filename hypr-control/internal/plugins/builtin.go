package plugins

import (
	"fmt"

	"hypr-control/internal/win32"
)

// 内置插件名称。
const (
	NameShutdownVolume = "shutdown-volume"
)

// DefaultTargetPercent 是 shutdown-volume 的默认目标音量（20%）。
const DefaultTargetPercent = 20

// ShutdownVolume 在系统关机/重启事件后、蓝牙音频断开前，
// 把默认音频设备的主音量降到目标百分比（默认 20%），
// 避免音箱在蓝牙断连瞬间以大音量播放断连提示音打扰他人。
//
// 覆盖的关机来源：
//   - 系统级关机/重启：服务端监听 WM_QUERYENDSESSION 广播（开始菜单、
//     shutdown 命令、其他软件发起均生效）
//   - 网页遥控关机：控制服务在执行电源操作前主动分发 shutdown 钩子
type ShutdownVolume struct {
	// TargetPercent 关机前的目标音量百分比（0-100）。
	TargetPercent float64
}

// NewShutdownVolume 以默认参数构造插件。
func NewShutdownVolume() *ShutdownVolume {
	return &ShutdownVolume{TargetPercent: DefaultTargetPercent}
}

// Name 实现 Plugin。
func (p *ShutdownVolume) Name() string { return NameShutdownVolume }

// Desc 实现 Plugin。
func (p *ShutdownVolume) Desc() string {
	return fmt.Sprintf("关机/重启前把系统主音量降到 %v%%（赶在蓝牙音箱断开前，避免断连提示音大音量外放）", p.TargetPercent)
}

// Hooks 实现 Plugin：只订阅 shutdown 钩子。
func (p *ShutdownVolume) Hooks() []Hook { return []Hook{HookShutdown} }

// OnHook 实现 Plugin：把主音量设到目标百分比。
//
// 系统给 WM_QUERYENDSESSION 的响应窗口约 5 秒；COM 调音量为毫秒级，
// 失败只返回错误（由 Manager 记日志），绝不阻塞关机。
func (p *ShutdownVolume) OnHook(hook Hook, ctx *Context) error {
	if hook != HookShutdown {
		return nil
	}
	before, berr := win32.GetMasterVolumePercent()
	if berr != nil {
		// 读当前值失败不放弃设置：目标音量与当前值无关。
		ctx.Logf("shutdown-volume: 读取当前音量失败（继续尝试设置）: %v", berr)
	}
	if err := win32.SetMasterVolumePercent(p.TargetPercent); err != nil {
		return fmt.Errorf("设置关机音量失败: %w", err)
	}
	after, aerr := win32.GetMasterVolumePercent()
	switch {
	case aerr != nil:
		ctx.Logf("shutdown-volume: 已下发目标音量 %v%%（设置后读取失败: %v）", p.TargetPercent, aerr)
	case berr == nil:
		ctx.Logf("shutdown-volume: 主音量 %v%% → %v%%（目标 %v%%）", before, after, p.TargetPercent)
	default:
		ctx.Logf("shutdown-volume: 主音量已设为 %v%%（目标 %v%%，设置前音量未知）", after, p.TargetPercent)
	}
	return nil
}
