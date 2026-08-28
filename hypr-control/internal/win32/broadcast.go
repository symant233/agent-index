package win32

// 系统关机/重启事件监听。
//
// Windows 在关机/重启/注销前向所有顶层窗口广播 WM_QUERYENDSESSION
// （进程可拒绝）与 WM_ENDSESSION（会话即将结束，应答后系统开始终止进程）。
// 服务端创建一个隐藏顶层窗口接收广播：在 WM_QUERYENDSESSION 处理中
// 同步执行回调（如插件降音量），这是蓝牙音频链路断开前的最后窗口。
//
// 广播按 Z 序逐窗口送达：系统等待未响应窗口约 5 秒（HungAppTimeout）
// 后强制继续。因此回调必须毫秒级完成，本实现带超时保护，确保及时应答。
//
// 线程模型：窗口与消息循环必须固定在同一个 OS 线程上（窗口隶属线程），
// 用后台 goroutine + runtime.LockOSThread 承载（Go 官方推荐的窗口
// 消息循环写法），运行期间不解锁。

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var (
	procRegisterClassExW   = user32.NewProc("RegisterClassExW")
	procCreateWindowExW    = user32.NewProc("CreateWindowExW")
	procDefWindowProcW     = user32.NewProc("DefWindowProcW")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
	procGetModuleHandleW   = kernel32.NewProc("GetModuleHandleW")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

// 窗口消息。
const (
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
	wmDestroy         = 0x0002
)

// 自定义消息：向监听线程的消息队列投递退出指令。
const wmAppStopListener = 0x8001 // WM_APP + 1

// 钩子回调预算：QUERYENDSESSION 应答窗口约 5 秒，回调超时后直接放行应答，
// 避免拖慢系统关机（此时蓝牙保护可能已来不及，但系统流程优先）。
const endsessionCallbackBudget = 1500 * time.Millisecond

// wndClassExW 对应 WNDCLASSEXW 布局（amd64）。
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  uintptr
	lpszClassName uintptr
	hIconSm       syscall.Handle
}

// msgW 对应 MSG 布局（amd64，总长 48 字节）。
type msgW struct {
	hwnd    uintptr // 0-8
	message uint32  // 8-12
	_       uint32  // 12-16 对齐填充
	wParam  uintptr // 16-24
	lParam  uintptr // 24-32
	time    uint32  // 32-36
	ptX     int32   // 36-40
	ptY     int32   // 40-44
	_       uint32  // 44-48 对齐填充
}

// ShutdownListener 是系统关机事件监听器的句柄。
type ShutdownListener interface {
	// Stop 请求监听线程退出。服务进程常驻，一般无需调用；
	// 进程退出时窗口与线程随之销毁。
	//
	// 注意：监听器是一次性的——Stop 后同名窗口类已注册，再次调用
	// StartShutdownListener 必然失败（RegisterClassExW 返回
	// ERROR_CLASS_ALREADY_EXISTS），且窗口过程经全局单例查找 state，
	// 不支持多实例并存。当前服务常驻运行，不触发此限制。
	Stop()
}

// shutdownController 是监听器的实际控制结构。
type shutdownController struct {
	mu       sync.Mutex
	threadID uint32
	stopOnce sync.Once
	state    *endSessionState
}

// endSessionState 是监听线程与控制方之间的共享状态。
type endSessionState struct {
	callback func()
}

// activeListener 是当前进程的监听器控制器。
// 必须用 atomic 而非 mutex：窗口过程在 CreateWindowExW 期间就会被
// 同步调用（WM_GETMINMAXINFO 等创建期消息），若窗口过程与
// StartShutdownListener 抢同一把锁会造成死锁（端到端测试中实际发生过）。
var activeListener atomic.Pointer[shutdownController]

// StartShutdownListener 启动系统关机/重启事件监听。
// callback 在收到关机广播时被调用（监听线程上同步执行，带超时保护）。
// 进程内重复启动返回错误；监听goroutine 后台常驻，出错只记日志不重启。
func StartShutdownListener(callback func()) (ShutdownListener, error) {
	if callback == nil {
		return nil, fmt.Errorf("关机回调不能为空")
	}
	if activeListener.Load() != nil {
		return nil, fmt.Errorf("关机监听已启动")
	}

	c := &shutdownController{state: &endSessionState{callback: callback}}
	// CAS 抢占式发布：并发调用只有一个能成功（先发布再建窗口，
	// 创建期消息 WM_GETMINMAXINFO 等到达窗口过程时必须能取到 state）。
	if !activeListener.CompareAndSwap(nil, c) {
		return nil, fmt.Errorf("关机监听已启动")
	}

	ready := make(chan error, 1) // 监听线程就绪或失败
	go func() {
		runtime.LockOSThread() // 窗口/消息循环固定线程，运行期不解锁
		defer runtime.UnlockOSThread()
		if err := runListenerLoop(c, ready); err != nil {
			fmt.Printf("hypr-control: 关机监听退出: %v\n", err)
		}
	}()

	if err := <-ready; err != nil {
		// 失败回滚：仅当当前值仍是自己时清除（避免覆盖成功者的发布）。
		activeListener.CompareAndSwap(c, nil)
		return nil, err
	}
	return c, nil
}

// Stop 请求监听线程退出：向其消息队列 Post 退出消息。
func (c *shutdownController) Stop() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		tid := c.threadID
		c.mu.Unlock()
		if tid != 0 {
			procPostThreadMessageW.Call(uintptr(tid), wmAppStopListener, 0, 0)
		}
		if activeListener.Load() == c {
			activeListener.Store(nil)
		}
	})
}

// runListenerLoop 在当前（已锁定的）OS 线程上注册窗口类、创建隐藏窗口
// 并跑消息循环。窗口就绪后向 ready 发送 nil；失败发送错误。
func runListenerLoop(c *shutdownController, ready chan<- error) error {
	className, err := syscall.UTF16PtrFromString("hypr-control-shutdown-listener")
	if err != nil {
		ready <- err
		return err
	}

	// 记录线程 ID 供跨线程 Post 退出消息。
	tid, _, _ := procGetCurrentThreadId.Call()
	c.mu.Lock()
	c.threadID = uint32(tid)
	c.mu.Unlock()

	var wc wndClassExW
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	wc.lpfnWndProc = syscall.NewCallback(endSessionWndProc)
	wc.lpszClassName = uintptr(unsafe.Pointer(className))
	if r1, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r1 == 0 {
		err := fmt.Errorf("RegisterClassExW 失败")
		ready <- err
		return err
	}

	// 创建隐藏顶层窗口。hInstance 用 GetModuleHandleW(nil) 取本进程句柄
	// （CreateWindowExW 需要与窗口类一致的有效实例句柄）。
	hInst, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, _ := procCreateWindowExW.Call(
		0,                                  // dwExStyle
		uintptr(unsafe.Pointer(className)), // 窗口类
		0,                                  // 窗口名
		0,                                  // dwStyle：不可见
		0, 0, 0, 0,
		0,     // 无父窗口
		0,     // 无菜单
		hInst, // hInstance
		0,     // lpParam
	)
	if hwnd == 0 {
		err := fmt.Errorf("CreateWindowExW 失败")
		ready <- err
		return err
	}
	_ = hwnd // 窗口过程经全局单例 state 找到回调，hwnd 仅用于调试

	ready <- nil // 监听就绪

	// 消息循环：GetMessageW 返回 0 表示收到 WM_QUIT，-1 表示错误。
	var m msgW
	for {
		r1, _, _ := procGetMessageW.Call(
			uintptr(unsafe.Pointer(&m)),
			0, // 所有窗口
			0, 0,
		)
		switch int32(r1) {
		case -1:
			return fmt.Errorf("GetMessageW 失败")
		case 0:
			return nil // WM_QUIT
		}
		// 自定义退出消息：主动转入退出流程。
		if m.message == wmAppStopListener && m.hwnd == 0 {
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// endSessionWndProc 是监听窗口的窗口过程。
// 收到关机广播时同步执行回调（带超时保护），其余交 DefWindowProcW。
// 注意：本函数绝不能阻塞——CreateWindowExW 期间就会被同步调用，
// 且广播应答有系统超时预算。
func endSessionWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	c := activeListener.Load()

	switch msg {
	case wmQueryEndSession:
		// lParam: ENDSESSION_CLOSEAPP(1) / ENDSESSION_CRITICAL(0x80000000) 等。
		if c != nil && c.state.callback != nil {
			runWithTimeout(c.state.callback, endsessionCallbackBudget)
		}
		return 1 // 同意结束会话
	case wmEndSession:
		// wParam=1：会话确实要结束了。回调已在此前的 QUERYENDSESSION 执行过，
		// 这里再兜底执行一次（幂等：重复设音量无害）。
		if wParam == 1 && c != nil && c.state.callback != nil {
			runWithTimeout(c.state.callback, endsessionCallbackBudget)
		}
		return 0
	case wmDestroy:
		return 0
	}
	r1, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r1
}

// runWithTimeout 带超时执行 fn：超时后不再等待（fn 所在 goroutine 留给
// 运行时回收），保证广播应答不被拖死。
func runWithTimeout(fn func(), budget time.Duration) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(budget):
	}
}
