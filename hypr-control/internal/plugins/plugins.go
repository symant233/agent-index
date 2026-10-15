// Package plugins 提供 hypr-control 的内置插件机制。
//
// 插件是编译进二进制的单元（非外部动态库），分两类：
//   - 钩子型：实现 Plugin 接口并向 Manager 声明关心的钩子
//    （如系统关机前、网页电源操作前），Manager 在钩子触发时分发；
//   - 生命周期型：不订阅钩子，额外实现可选的 Starter/Stopper 接口，
//     随启用/禁用启停（如 socks5 代理服务）。
//
// Manager 负责按名称查询/启用/禁用插件并持久化开关状态。
//
// 设计要点：
//   - 所有插件初始为禁用状态；用户通过 `hctrl plugins enable <名称>` 启用。
//   - 钩子分发是同步串行的，插件应快速返回（关机路径上有系统超时压力）。
//   - 钩子返回的错误只记日志，不阻断关机流程。
//   - 生命周期回调（Start/Stop）在 Manager 锁外执行，Start 的错误会
//     冒泡给启用命令，Stop 静默（幂等由插件保证）。
package plugins

import (
	"fmt"
	"sync"
)

// Hook 标识一个插件可订阅的事件点。
type Hook string

// 支持的钩子。
const (
	// HookShutdown 在系统关机/重启流程开始后、会话结束前触发：
	// 服务端收到 WM_QUERYENDSESSION 广播，或网页电源操作即将下发时。
	// 这是"蓝牙音箱断开前"的最后窗口，钩子必须尽快完成。
	HookShutdown Hook = "shutdown"
)

// Context 传给钩子的运行时信息。
type Context struct {
	// Log 用于插件输出诊断日志（写入服务日志）。
	Log func(format string, args ...any)
}

// Logf 按格式写一条插件日志；Log 未设置时不输出。
func (c *Context) Logf(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

// Plugin 是插件必须实现的最小接口。
type Plugin interface {
	// Name 返回插件的唯一名称（用于 CLI 查询/启用/禁用）。
	Name() string
	// Desc 返回一句话描述（供 CLI 展示）。
	Desc() string
	// Hooks 返回插件订阅的钩子列表；Manager 只把已订阅的钩子分发给它。
	// 生命周期型插件可不订阅任何钩子（返回 nil），随启用/禁用启停。
	Hooks() []Hook
	// OnHook 处理一次钩子触发。hook 指明哪个钩子触发（一个插件可订阅多个）。
	// 返回错误只被记录，不会中断其他插件或后续流程。
	OnHook(hook Hook, ctx *Context) error
}

// Starter 是生命周期型插件的可选扩展接口：需要随守护进程启停长期任务
//（如监听服务）的插件实现它。Manager 在守护进程启动时对已启用插件调用
// Start，并在每次 Enable 后调用（此时可能首次启动，也可能已在运行）。
type Starter interface {
	// Start 启动长期任务。已在运行时必须直接返回（幂等）；
	// 返回的错误会冒泡给调用方（启用命令的报错 / 启动日志）。
	// 实现不得回调 Manager（本方法调用期间不持有 Manager 锁）。
	Start(ctx *Context) error
}

// Stopper 是与 Starter 配套的可选扩展接口，Disable 时被调用。
type Stopper interface {
	// Stop 停止 Start 启动的任务；未在运行时直接返回（幂等）。
	// 实现不得回调 Manager（本方法调用期间不持有 Manager 锁）。
	Stop()
}

// ErrUnknown 表示指定名称的插件不存在。
var ErrUnknown = fmt.Errorf("未知插件")

// Manager 管理全部插件的注册、启停、钩子分发与生命周期回调。
// 并发安全；状态持久化由注入的 Store 负责。
type Manager struct {
	mu      sync.Mutex
	order   []string          // 插件注册顺序（list 输出稳定）
	byName  map[string]Plugin // 名称 → 插件
	subs    map[Hook][]Plugin // 钩子 → 订阅插件（按注册顺序）
	enabled map[string]bool   // 名称 → 是否启用（仅记录显式状态：启用为 true）
	store   Store
	logf    func(format string, args ...any) // 生命周期回调的日志输出（可为 nil）
}

// Store 抽象插件开关状态的持久化（数据目录 JSON 文件）。
type Store interface {
	// Load 返回已启用的插件名称集合；文件不存在时返回空集。
	Load() (map[string]bool, error)
	// Save 全量保存当前启用的插件名称集合。
	Save(enabled map[string]bool) error
}

// NewManager 构造管理器并从 store 恢复启用状态；store 为 nil 时不持久化。
// 恢复时保留全部启用项：已删除插件的残留状态不丢弃（避免误清用户数据），
// 同名插件再次注册时自动恢复启用。
func NewManager(store Store) (*Manager, error) {
	m := &Manager{
		byName:  make(map[string]Plugin),
		subs:    make(map[Hook][]Plugin),
		enabled: make(map[string]bool),
		store:   store,
	}
	if store == nil {
		return m, nil
	}
	enabled, err := store.Load()
	if err != nil {
		return nil, err
	}
	for name := range enabled {
		if enabled[name] {
			m.enabled[name] = true
		}
	}
	return m, nil
}

// Register 注册一个插件。名称重复时报错；注册后立即按持久化状态生效。
func (m *Manager) Register(p Plugin) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	name := p.Name()
	if name == "" {
		return fmt.Errorf("插件名称不能为空")
	}
	if _, dup := m.byName[name]; dup {
		return fmt.Errorf("插件 %q 重复注册", name)
	}
	m.byName[name] = p
	m.order = append(m.order, name)
	for _, h := range p.Hooks() {
		m.subs[h] = append(m.subs[h], p)
	}
	return nil
}

// SetLogf 设置生命周期回调（Start/StartEnabled）的日志输出函数。
// 守护进程装配时调用，注入后日志写入服务日志。
func (m *Manager) SetLogf(f func(format string, args ...any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logf = f
}

// Enabled 报告插件当前是否启用。未注册的插件返回 false。
func (m *Manager) Enabled(name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled[name]
}

// Get 按名称返回已注册的插件；未注册返回 nil。
func (m *Manager) Get(name string) Plugin {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byName[name]
}

// Enable 启用插件并持久化。未注册的插件返回 ErrUnknown。
// 先持久化成功再更新内存：Save 失败时 HTTP 报错且运行态不变，
// 避免"内存已启用、重启后回退"的 inconsistency。
// 持久化成功后对实现 Starter 的插件调用 Start（幂等，锁外执行）：
// 启动失败不回滚启用状态，但错误原样返回给调用方。
func (m *Manager) Enable(name string) error {
	m.mu.Lock()
	if _, ok := m.byName[name]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	if m.enabled[name] {
		m.mu.Unlock()
		return nil // 幂等：已启用直接成功（已运行的任务不动）
	}
	if err := m.storeSaveLocked(map[string]bool{name: true}); err != nil {
		m.mu.Unlock()
		return err
	}
	m.enabled[name] = true
	starter, _ := m.byName[name].(Starter)
	logf := m.logf
	m.mu.Unlock()

	if starter == nil {
		return nil
	}
	if err := m.safeStart(name, starter, logf); err != nil {
		return fmt.Errorf("插件 %s 已启用但启动失败: %w", name, err)
	}
	return nil
}

// Disable 禁用插件并持久化。未注册的插件返回 ErrUnknown。
// 持久化成功后才更新内存，语义同 Enable。
// 更新内存后对实现 Stopper 的插件调用 Stop（幂等，锁外执行）。
func (m *Manager) Disable(name string) error {
	m.mu.Lock()
	if _, ok := m.byName[name]; !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrUnknown, name)
	}
	if !m.enabled[name] {
		m.mu.Unlock()
		return nil // 幂等：已禁用直接成功
	}
	if err := m.storeSaveLocked(map[string]bool{name: false}); err != nil {
		m.mu.Unlock()
		return err
	}
	delete(m.enabled, name)
	stopper, _ := m.byName[name].(Stopper)
	m.mu.Unlock()

	if stopper != nil {
		stopper.Stop()
	}
	return nil
}

// StartEnabled 对全部已启用且实现 Starter 的插件调用 Start
//（守护进程启动时装配完成后调用一次）。单个插件失败只记日志，
// 不影响其他插件与启动流程。
func (m *Manager) StartEnabled() {
	m.mu.Lock()
	type named struct {
		name    string
		starter Starter
	}
	var starters []named
	for _, name := range m.order {
		if !m.enabled[name] {
			continue
		}
		if st, ok := m.byName[name].(Starter); ok {
			starters = append(starters, named{name: name, starter: st})
		}
	}
	logf := m.logf
	m.mu.Unlock()

	for _, st := range starters {
		if err := m.safeStart(st.name, st.starter, logf); err != nil && logf != nil {
			logf("插件 %s 启动失败: %v", st.name, err)
		}
	}
}

// safeStart 在锁外调用 Starter.Start，panic 视为插件缺陷但隔离，
// 避免拖垮启用命令或守护进程启动（语义同 Dispatch）。
func (m *Manager) safeStart(name string, st Starter, logf func(format string, args ...any)) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("插件 %s 启动中 panic: %v", name, r)
		}
	}()
	return st.Start(&Context{Log: logf})
}

// storeSaveLocked 计算把 name 置为 want 后的启用快照并持久化（调用方持有锁）。
// 先算目标快照再写文件，成功后调用方才把变更落到 m.enabled。
func (m *Manager) storeSaveLocked(apply map[string]bool) error {
	if m.store == nil {
		return nil
	}
	snap := m.enabledSnapshotLocked()
	for name, want := range apply {
		if want {
			snap[name] = true
		} else {
			delete(snap, name)
		}
	}
	return m.store.Save(snap)
}

// enabledSnapshotLocked 复制当前启用集合（调用方持有锁）。
func (m *Manager) enabledSnapshotLocked() map[string]bool {
	out := make(map[string]bool, len(m.enabled))
	for k, v := range m.enabled {
		out[k] = v
	}
	return out
}

// Info 是插件列表的单条视图。
type Info struct {
	Name    string   `json:"name"`
	Desc    string   `json:"desc"`
	Hooks   []string `json:"hooks"`
	Enabled bool     `json:"enabled"`
}

// List 返回全部插件（按注册顺序）及其启停状态。
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.order))
	for _, name := range m.order {
		p := m.byName[name]
		hooks := make([]string, 0, len(p.Hooks()))
		for _, h := range p.Hooks() {
			hooks = append(hooks, string(h))
		}
		out = append(out, Info{
			Name:    name,
			Desc:    p.Desc(),
			Hooks:   hooks,
			Enabled: m.enabled[name],
		})
	}
	return out
}

// Dispatch 向所有已启用且订阅了 hook 的插件分发一次钩子触发。
// 串行执行；单个插件的错误被记入日志，不影响其他插件。
// 返回本次实际执行的插件名列表（用于测试与日志）。
func (m *Manager) Dispatch(hook Hook, ctx *Context) []string {
	if ctx == nil {
		ctx = &Context{}
	}
	m.mu.Lock()
	subs := append([]Plugin(nil), m.subs[hook]...)
	var run []Plugin
	for _, p := range subs {
		if m.enabled[p.Name()] {
			run = append(run, p)
		}
	}
	m.mu.Unlock()

	var ran []string
	for _, p := range run {
		ran = append(ran, p.Name())
		func() {
			defer func() {
				// 插件是内置代码，panic 视为缺陷但仍要隔离，避免拖垮服务主流程。
				if r := recover(); r != nil {
					ctx.Logf("插件 %s 在钩子 %s 中 panic: %v", p.Name(), hook, r)
				}
			}()
			if err := p.OnHook(hook, ctx); err != nil {
				ctx.Logf("插件 %s 处理钩子 %s 失败: %v", p.Name(), hook, err)
			}
		}()
	}
	return ran
}
