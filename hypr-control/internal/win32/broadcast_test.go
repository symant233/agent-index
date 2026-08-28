package win32

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

// TestEndSessionWndProcRoutesMessages 用直接调用窗口过程的方式验证
// 消息路由：QUERYENDSESSION/ENDSESSION 触发回调，其他消息透传
// DefWindowProcW（不触发回调）。
func TestEndSessionWndProcRoutesMessages(t *testing.T) {
	var calls atomic.Int32
	restore := installTestListener(func() { calls.Add(1) })
	defer restore()

	// 关机广播：应触发回调并同意结束会话
	if r := endSessionWndProc(0, wmQueryEndSession, 0, 0); r != 1 {
		t.Fatalf("QUERYENDSESSION 应答 = %d, want 1（同意）", r)
	}
	if calls.Load() != 1 {
		t.Fatalf("QUERYENDSESSION 回调次数 = %d, want 1", calls.Load())
	}

	// 会话结束确认（wParam=1）：兜底再触发一次
	if r := endSessionWndProc(0, wmEndSession, 1, 0); r != 0 {
		t.Fatalf("ENDSESSION 应答 = %d, want 0", r)
	}
	if calls.Load() != 2 {
		t.Fatalf("ENDSESSION 后回调次数 = %d, want 2", calls.Load())
	}

	// 会话取消（wParam=0）：不触发
	endSessionWndProc(0, wmEndSession, 0, 0)
	if calls.Load() != 2 {
		t.Fatalf("取消的 ENDSESSION 不应触发回调, 次数 = %d", calls.Load())
	}

	// 普通消息（如 WM_GETMINMAXINFO=0x24）：交 DefWindowProcW，不触发回调。
	// 不对返回值做断言：DefWindowProcW 对部分消息合法返回 0。
	endSessionWndProc(0, 0x24, 0, 0)
	if calls.Load() != 2 {
		t.Fatalf("普通消息不应触发回调, 次数 = %d", calls.Load())
	}
}

// TestEndSessionWndProcNilListener 验证无监听器时不 panic、正常应答。
func TestEndSessionWndProcNilListener(t *testing.T) {
	restore := installTestListener(nil)
	defer restore()
	if r := endSessionWndProc(0, wmQueryEndSession, 0, 0); r != 1 {
		t.Fatalf("无监听器时 QUERYENDSESSION 应答 = %d, want 1", r)
	}
}

// installTestListener 临时设置全局监听器（测试辅助），返回恢复函数。
func installTestListener(cb func()) (restore func()) {
	old := activeListener.Load()
	if cb == nil {
		activeListener.Store(nil)
	} else {
		activeListener.Store(&shutdownController{state: &endSessionState{callback: cb}})
	}
	return func() { activeListener.Store(old) }
}

// TestRunWithTimeoutCompletes 验证回调在预算内完成时正常返回。
func TestRunWithTimeoutCompletes(t *testing.T) {
	var done bool
	runWithTimeout(func() { done = true }, time.Second)
	if !done {
		t.Fatal("回调未执行")
	}
}

// TestRunWithTimeoutBudget 验证超时分支：慢回调不拖死调用方。
func TestRunWithTimeoutBudget(t *testing.T) {
	start := time.Now()
	var once sync.Once
	released := make(chan struct{})
	runWithTimeout(func() {
		// 模拟 3 秒慢回调，但被 50ms 预算切断
		once.Do(func() { close(released) })
		<-released
		time.Sleep(3 * time.Second)
	}, 50*time.Millisecond)
	elapsed := time.Since(start)
	if elapsed > time.Second {
		t.Fatalf("runWithTimeout 未在预算内返回: %v", elapsed)
	}
}

// TestStructLayouts 复核监听器依赖的两个 Windows 结构布局。
func TestStructLayouts(t *testing.T) {
	if ptrSize != 8 {
		t.Skip("非 64 位环境，跳过布局断言")
	}
	if got := unsafe.Sizeof(wndClassExW{}); got != 80 {
		t.Fatalf("sizeof(WNDCLASSEXW) = %d, want 80", got)
	}
	if got := unsafe.Sizeof(msgW{}); got != 48 {
		t.Fatalf("sizeof(MSG) = %d, want 48", got)
	}
}
