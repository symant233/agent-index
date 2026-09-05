package plugins

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakePlugin 测试用插件：记录钩子触发。
type fakePlugin struct {
	name  string
	hooks []Hook
	mu    sync.Mutex
	calls []Hook
	err   error
	panic bool
}

func (f *fakePlugin) Name() string  { return f.name }
func (f *fakePlugin) Desc() string  { return "测试插件 " + f.name }
func (f *fakePlugin) Hooks() []Hook { return f.hooks }
func (f *fakePlugin) OnHook(h Hook, _ *Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, h)
	if f.panic {
		panic("boom")
	}
	return f.err
}

func (f *fakePlugin) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugins.json")
	m, err := NewManager(NewFileStore(path))
	if err != nil {
		t.Fatal(err)
	}
	return m, path
}

// TestInitialDisabled 验证插件初始全部为禁用状态（核心需求）。
func TestInitialDisabled(t *testing.T) {
	m, _ := newTestManager(t)
	p := &fakePlugin{name: "p1", hooks: []Hook{HookShutdown}}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}

	if m.Enabled("p1") {
		t.Fatal("新注册插件必须初始禁用")
	}
	list := m.List()
	if len(list) != 1 || list[0].Enabled {
		t.Fatalf("List 应显示禁用: %+v", list)
	}

	// 禁用状态下钩子不分发
	got := m.Dispatch(HookShutdown, nil)
	if len(got) != 0 {
		t.Fatalf("禁用插件不应收到钩子: %v", got)
	}
	if p.callCount() != 0 {
		t.Fatal("禁用插件的 OnHook 不应被调用")
	}
}

// TestEnableDisablePersist 验证启用/禁用与跨实例持久化。
func TestEnableDisablePersist(t *testing.T) {
	m, path := newTestManager(t)
	p := &fakePlugin{name: "p1", hooks: []Hook{HookShutdown}}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}

	if err := m.Enable("p1"); err != nil {
		t.Fatal(err)
	}
	if !m.Enabled("p1") {
		t.Fatal("Enable 后应已启用")
	}
	if got := m.Dispatch(HookShutdown, nil); len(got) != 1 || got[0] != "p1" {
		t.Fatalf("Dispatch 返回 = %v, want [p1]", got)
	}
	if p.callCount() != 1 {
		t.Fatalf("OnHook 调用数 = %d, want 1", p.callCount())
	}

	// 禁用后不再分发
	if err := m.Disable("p1"); err != nil {
		t.Fatal(err)
	}
	if m.Enabled("p1") {
		t.Fatal("Disable 后应已禁用")
	}
	if got := m.Dispatch(HookShutdown, nil); len(got) != 0 {
		t.Fatalf("禁用后 Dispatch = %v, want 空", got)
	}

	// 新实例从文件恢复：应保持禁用（刚被禁用）
	m2, err := NewManager(NewFileStore(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := m2.Register(&fakePlugin{name: "p1", hooks: []Hook{HookShutdown}}); err != nil {
		t.Fatal(err)
	}
	if m2.Enabled("p1") {
		t.Fatal("重启后应保持禁用状态")
	}

	// 再启用 → 新实例应恢复启用
	if err := m.Enable("p1"); err != nil {
		t.Fatal(err)
	}
	m3, err := NewManager(NewFileStore(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := m3.Register(&fakePlugin{name: "p1", hooks: []Hook{HookShutdown}}); err != nil {
		t.Fatal(err)
	}
	if !m3.Enabled("p1") {
		t.Fatal("重启后应恢复启用状态")
	}
}

// TestUnknownPlugin 验证未知插件的启用/禁用报错。
func TestUnknownPlugin(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.Enable("ghost"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Enable(ghost) = %v, want ErrUnknown", err)
	}
	if err := m.Disable("ghost"); !errors.Is(err, ErrUnknown) {
		t.Fatalf("Disable(ghost) = %v, want ErrUnknown", err)
	}
}

// TestDispatchOrderAndIsolation 验证多插件按注册顺序分发、
// 单插件错误/panic 不影响其他插件。
func TestDispatchOrderAndIsolation(t *testing.T) {
	m, _ := newTestManager(t)
	var logs []string
	ctx := &Context{Log: func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }}

	pa := &fakePlugin{name: "a", hooks: []Hook{HookShutdown}}
	pb := &fakePlugin{name: "b", hooks: []Hook{HookShutdown}, err: errors.New("坏了")}
	pc := &fakePlugin{name: "c", hooks: []Hook{HookShutdown}, panic: true}
	pd := &fakePlugin{name: "d", hooks: []Hook{HookShutdown}}
	for _, p := range []*fakePlugin{pa, pb, pc, pd} {
		if err := m.Register(p); err != nil {
			t.Fatal(err)
		}
		if err := m.Enable(p.name); err != nil {
			t.Fatal(err)
		}
	}

	got := m.Dispatch(HookShutdown, ctx)
	if len(got) != 4 {
		t.Fatalf("Dispatch 执行插件 = %v, want 4 个", got)
	}
	for _, p := range []*fakePlugin{pa, pb, pc, pd} {
		if p.callCount() != 1 {
			t.Fatalf("插件 %s 调用数 = %d, want 1", p.Name(), p.callCount())
		}
	}
	// 错误与 panic 都应被记录而非逃逸
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "坏了") || !strings.Contains(joined, "panic") {
		t.Fatalf("错误/panic 未被记录: %v", logs)
	}
}

// TestHookSubscription 验证未订阅的钩子不分发；订阅其他钩子的插件不受影响。
func TestHookSubscription(t *testing.T) {
	m, _ := newTestManager(t)
	p := &fakePlugin{name: "p1", hooks: []Hook{HookShutdown}}
	if err := m.Register(p); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable("p1"); err != nil {
		t.Fatal(err)
	}
	// 其他钩子（未订阅）不分发
	if got := m.Dispatch(Hook("other"), nil); len(got) != 0 {
		t.Fatalf("未订阅钩子不应分发: %v", got)
	}
	if p.callCount() != 0 {
		t.Fatal("未订阅钩子的 OnHook 不应被调用")
	}
}

// TestListStableOrder 验证 List 按注册顺序输出且含元信息。
func TestListStableOrder(t *testing.T) {
	m, _ := newTestManager(t)
	for _, n := range []string{"c", "a", "b"} {
		if err := m.Register(&fakePlugin{name: n, hooks: []Hook{HookShutdown}}); err != nil {
			t.Fatal(err)
		}
	}
	list := m.List()
	if len(list) != 3 {
		t.Fatalf("List 长度 = %d", len(list))
	}
	wantOrder := []string{"c", "a", "b"}
	for i, w := range wantOrder {
		if list[i].Name != w {
			t.Fatalf("List[%d].Name = %s, want %s", i, list[i].Name, w)
		}
		if len(list[i].Hooks) != 1 || list[i].Hooks[0] != string(HookShutdown) {
			t.Fatalf("List[%d].Hooks = %v", i, list[i].Hooks)
		}
	}
}

// TestFileStoreAtomic 验证 Store 文件格式与空集处理。
func TestFileStoreAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugins.json")
	s := NewFileStore(path)

	// 文件不存在 → 空集（初始禁用语义）
	enabled, err := s.Load()
	if err != nil || len(enabled) != 0 {
		t.Fatalf("初始 Load = %v, %v", enabled, err)
	}

	if err := s.Save(map[string]bool{"a": true, "b": false, "c": true}); err != nil {
		t.Fatal(err)
	}
	enabled, err = s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 2 || !enabled["a"] || !enabled["c"] || enabled["b"] {
		t.Fatalf("Save/Load 往返 = %v", enabled)
	}
}

// TestLogfNilSafe 验证 Context.Logf 在 Log 未设置时不 panic。
func TestLogfNilSafe(t *testing.T) {
	ctx := &Context{}
	ctx.Logf("no panic %d", 1)
}

// TestRegisterBuiltins 验证内置插件清单注册：全部成功、无重名。
// 新增插件文件后本测试自动覆盖其注册正确性。
func TestRegisterBuiltins(t *testing.T) {
	m, _ := newTestManager(t)
	if err := RegisterBuiltins(m); err != nil {
		t.Fatalf("内置插件注册失败: %v", err)
	}
	list := m.List()
	if len(list) == 0 {
		t.Fatal("内置插件清单为空（registry.go 的 NewBuiltins 未登记任何插件？）")
	}
	seen := map[string]bool{}
	for _, p := range list {
		if p.Name == "" {
			t.Fatal("存在空名称插件")
		}
		if seen[p.Name] {
			t.Fatalf("插件重名: %s", p.Name)
		}
		seen[p.Name] = true
		// 每个插件必须声明至少一个钩子，且描述非空（CLI 展示用）
		if len(p.Hooks) == 0 {
			t.Fatalf("插件 %s 未声明任何钩子", p.Name)
		}
		if p.Desc == "" {
			t.Fatalf("插件 %s 缺少描述", p.Name)
		}
		// 注册后初始必须为禁用（核心语义，防新插件意外默认开启）
		if p.Enabled {
			t.Fatalf("插件 %s 初始状态应为禁用", p.Name)
		}
	}
	// shutdown-volume 必须在内置清单中
	if !seen[NameShutdownVolume] {
		t.Fatalf("内置插件 %s 未登记", NameShutdownVolume)
	}
}
