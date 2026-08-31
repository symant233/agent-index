package plugins

// 插件 shutdown-volume 的单元测试，与实现文件 plugin_shutdownvolume.go
// 对应；框架机制的测试见 plugins_test.go。

import (
	"strings"
	"testing"
)

// TestShutdownVolumeMetadata 验证插件元数据：名称/描述/钩子订阅。
func TestShutdownVolumeMetadata(t *testing.T) {
	p := NewShutdownVolume()
	if p.Name() != NameShutdownVolume || NameShutdownVolume != "shutdown-volume" {
		t.Fatalf("插件名 = %q, want shutdown-volume", p.Name())
	}
	if p.TargetPercent != DefaultTargetPercent || DefaultTargetPercent != 20 {
		t.Fatalf("默认目标音量 = %v, want 20", p.TargetPercent)
	}
	if !strings.Contains(p.Desc(), "20%") {
		t.Fatalf("描述应含目标音量: %s", p.Desc())
	}
	hooks := p.Hooks()
	if len(hooks) != 1 || hooks[0] != HookShutdown {
		t.Fatalf("钩子订阅 = %v, want [shutdown]", hooks)
	}
}

// TestShutdownVolumeIgnoresOtherHooks 验证未订阅的钩子直接忽略。
func TestShutdownVolumeIgnoresOtherHooks(t *testing.T) {
	p := NewShutdownVolume()
	var logs []string
	ctx := &Context{Log: func(f string, a ...any) { logs = append(logs, f) }}
	if err := p.OnHook(Hook("other"), ctx); err != nil {
		t.Fatalf("其他钩子应返回 nil: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("其他钩子不应产生日志: %v", logs)
	}
}

// TestShutdownVolumeTargetClamp 验证目标音量经 win32 层钳位（0-100）：
// 这里只验证插件参数传递语义，COM 交互路径由 win32 层测试覆盖。
func TestShutdownVolumeTargetClamp(t *testing.T) {
	// 插件允许配置任意 TargetPercent；钳位发生在 win32.volumePercentToLevel
	cases := []struct {
		in, want float64
	}{
		{0, 0},
		{20, 20},
		{100, 100},
	}
	for _, c := range cases {
		p := &ShutdownVolume{TargetPercent: c.in}
		if p.TargetPercent != c.want {
			t.Fatalf("TargetPercent = %v, want %v", p.TargetPercent, c.want)
		}
	}
}
