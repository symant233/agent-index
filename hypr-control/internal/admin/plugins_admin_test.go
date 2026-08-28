package admin

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"hypr-control/internal/config"
	"hypr-control/internal/devices"
	"hypr-control/internal/plugins"
)

// fakeRegistry 测试用插件注册表（避免测试依赖真实 win32 音量调用）。
type fakeRegistry struct {
	mu        sync.Mutex
	list      []PluginInfo
	enabled   map[string]bool
	enableErr map[string]error
}

func newFakeRegistry() *fakeRegistry {
	return &fakeRegistry{
		enabled:   map[string]bool{},
		enableErr: map[string]error{},
	}
}

func (f *fakeRegistry) List() []PluginInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]PluginInfo, len(f.list))
	copy(out, f.list)
	return out
}

func (f *fakeRegistry) Enable(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enableErr[name]; err != nil {
		return err
	}
	f.enabled[name] = true
	return nil
}

func (f *fakeRegistry) Disable(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enableErr[name]; err != nil {
		return err
	}
	delete(f.enabled, name)
	return nil
}

// TestAdminPluginsAPI 验证插件管理 API 全链路：list / enable / disable / 错误分支。
func TestAdminPluginsAPI(t *testing.T) {
	dir := t.TempDir()
	store, err := devices.Open(filepath.Join(dir, config.DevicesFileName))
	if err != nil {
		t.Fatal(err)
	}

	// 真实 Manager + fake 插件（验证 adapter 转换与持久化联动）。
	mgr, err := plugins.NewManager(plugins.NewFileStore(filepath.Join(dir, "plugins.json")))
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(&stubPlugin{name: "p1", hooks: []plugins.Hook{plugins.HookShutdown}}); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(store, "test-secret")
	srv.plugins = pluginRegistryAdapter{mgr}
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	if err := writeAdminInfo(filepath.Join(dir, config.AdminFileName), srv.Addr(), "test-secret", 1, config.DefaultPort); err != nil {
		t.Fatal(err)
	}
	cl, err := NewClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 初始：全部禁用
	list, err := cl.ListPlugins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "p1" || list[0].Enabled {
		t.Fatalf("初始插件列表异常: %+v", list)
	}

	// 启用 → 状态变化且 Manager 感知
	if err := cl.EnablePlugin(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	list, _ = cl.ListPlugins(ctx)
	if !list[0].Enabled || !mgr.Enabled("p1") {
		t.Fatalf("启用后状态异常: list=%+v mgr=%v", list, mgr.Enabled("p1"))
	}

	// 禁用
	if err := cl.DisablePlugin(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	list, _ = cl.ListPlugins(ctx)
	if list[0].Enabled || mgr.Enabled("p1") {
		t.Fatalf("禁用后状态异常: list=%+v mgr=%v", list, mgr.Enabled("p1"))
	}

	// 未知插件报错（错误信息经 HTTP 透传）
	if err := cl.EnablePlugin(ctx, "ghost"); err == nil {
		t.Fatal("启用未知插件应失败")
	}
	if err := cl.DisablePlugin(ctx, "ghost"); err == nil {
		t.Fatal("禁用未知插件应失败")
	}
}

// TestAdminPluginsNil 验证未装配插件时接口可用（返回空列表 / 503）。
func TestAdminPluginsNil(t *testing.T) {
	dir := t.TempDir()
	store, err := devices.Open(filepath.Join(dir, config.DevicesFileName))
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(store, "test-secret")
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	if err := writeAdminInfo(filepath.Join(dir, config.AdminFileName), srv.Addr(), "test-secret", 1, config.DefaultPort); err != nil {
		t.Fatal(err)
	}
	cl, err := NewClient(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 未装配插件：list 返回空数组而非报错（CLI 兼容旧版本数据目录）
	list, err := cl.ListPlugins(ctx)
	if err != nil || len(list) != 0 {
		t.Fatalf("未装配插件时 list = %v, %v", list, err)
	}
	// enable/disable 返回 503
	if err := cl.EnablePlugin(ctx, "p1"); err == nil {
		t.Fatal("未装配插件时 enable 应失败")
	}
}

// stubPlugin 极简插件（复用 plugins 包接口）。
type stubPlugin struct {
	name  string
	hooks []plugins.Hook
}

func (s *stubPlugin) Name() string                                { return s.name }
func (s *stubPlugin) Desc() string                                { return "stub" }
func (s *stubPlugin) Hooks() []plugins.Hook                       { return s.hooks }
func (s *stubPlugin) OnHook(plugins.Hook, *plugins.Context) error { return nil }
