package plugins

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hypr-control/internal/config"
)

// freePort 找一个当前空闲的 TCP 端口（存在极小的竞用窗口，测试场景够用）。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// newTestSocks5 构造带临时数据目录的 socks5 插件（回环监听：
// 测试不触达外部网卡，避免触发 Windows 防火墙放行询问）。
func newTestSocks5(t *testing.T) *Socks5 {
	t.Helper()
	p := NewSocks5(t.TempDir())
	p.SetListenHostForTest("127.0.0.1")
	return p
}

// TestSocks5Metadata 验证插件元信息（名称是 CLI/持久化键，不可改动）。
func TestSocks5Metadata(t *testing.T) {
	p := NewSocks5("")
	if p.Name() != "socks5" {
		t.Fatalf("Name = %q, want socks5", p.Name())
	}
	if p.Desc() == "" {
		t.Fatal("Desc 不能为空")
	}
	// 生命周期型插件不订阅钩子，随启用/禁用启停。
	if len(p.Hooks()) != 0 {
		t.Fatalf("Hooks = %v, want 空", p.Hooks())
	}
	if err := p.OnHook(HookShutdown, &Context{}); err != nil {
		t.Fatalf("OnHook 应为 no-op: %v", err)
	}
}

// TestSocks5SetupValidates 验证 Setup 的参数校验。
func TestSocks5SetupValidates(t *testing.T) {
	p := newTestSocks5(t)
	port := freePort(t)
	cases := []struct {
		name, user, pass string
		port             int
	}{
		{"空用户名", "", "pw", port},
		{"空密码", "u", "", port},
		{"端口过小", "u", "pw", 0},
		{"端口过大", "u", "pw", 65536},
		{"用户名超长", string(make([]byte, 256)), "pw", port},
		{"密码超长", "u", string(make([]byte, 256)), port},
	}
	for _, c := range cases {
		if _, err := p.Setup(c.user, c.pass, c.port); err == nil {
			t.Fatalf("%s: 应校验失败", c.name)
		}
	}
	// 校验失败不应留下监听或配置。
	if p.runPort != 0 {
		t.Fatal("校验失败不应启动监听")
	}
}

// TestSocks5SetupServesAndPersists 端到端：Setup 后可建立 TCP 连接，
// 配置粘滞落盘，重新构造实例 Start 即自动恢复监听。
func TestSocks5SetupServesAndPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, config.Socks5PluginFileName)
	port := freePort(t)

	p := NewSocks5(dir)
	p.SetListenHostForTest("127.0.0.1")
	actual, err := p.Setup("alice", "secret", port)
	if err != nil {
		t.Fatal(err)
	}
	if actual != port {
		t.Fatalf("Setup 返回端口 = %d, want %d", actual, port)
	}

	// 端口上已有监听（能建立 TCP 连接即视为在跑）。
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", actual), 2*time.Second)
	if err != nil {
		t.Fatalf("Setup 后端口未监听: %v", err)
	}
	conn.Close()

	// 配置已持久化（含粘滞端口）。
	cfg, err := LoadSocks5Config(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Username != "alice" || cfg.Password != "secret" || cfg.Port != actual {
		t.Fatalf("持久化配置 = %+v", cfg)
	}

	// 重新构造实例（模拟守护进程重启），Start 应自动恢复监听。
	p.Stop()
	p2 := NewSocks5(dir)
	p2.SetListenHostForTest("127.0.0.1")
	if err := p2.Start(&Context{}); err != nil {
		t.Fatalf("Start 恢复监听失败: %v", err)
	}
	conn, err = net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", actual), 2*time.Second)
	if err != nil {
		t.Fatalf("重启后端口未监听: %v", err)
	}
	conn.Close()
	p2.Stop()
}

// TestSocks5PortIncrement 验证请求端口被占用时自增顺延，实际端口粘滞落盘。
func TestSocks5PortIncrement(t *testing.T) {
	dir := t.TempDir()

	// 占住端口 base，Setup(base) 应顺延到 base+1。
	occupier, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(freePort(t))))
	if err != nil {
		t.Fatal(err)
	}
	defer occupier.Close()
	base := occupier.Addr().(*net.TCPAddr).Port

	p := NewSocks5(dir)
	p.SetListenHostForTest("127.0.0.1")
	actual, err := p.Setup("u", "p", base)
	if err != nil {
		t.Fatal(err)
	}
	if actual != base+1 {
		t.Fatalf("顺延端口 = %d, want %d", actual, base+1)
	}

	// 粘滞：配置记录的是实际端口，再次 Setup 同参应幂等返回同端口。
	cfg, err := LoadSocks5Config(filepath.Join(dir, config.Socks5PluginFileName))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != actual {
		t.Fatalf("粘滞端口 = %d, want %d", cfg.Port, actual)
	}
	got, started, err := p.start("u", "p", actual)
	if err != nil || started {
		t.Fatalf("幂等 Setup 应 no-op: port=%d started=%v err=%v", got, started, err)
	}
}

// TestSocks5StartStopIdempotent 验证 Start/Stop 幂等与未配置时的行为。
func TestSocks5StartStopIdempotent(t *testing.T) {
	p := newTestSocks5(t)

	// 未配置：Start 不报错、不监听。
	if err := p.Start(&Context{}); err != nil {
		t.Fatalf("未配置 Start 应 no-op: %v", err)
	}
	if p.runPort != 0 {
		t.Fatal("未配置 Start 不应启动监听")
	}

	// Stop 未启动时幂等。
	p.Stop()
	p.Stop()

	// 配置 → Start → Stop → Stop：不 panic、端口释放。
	port := freePort(t)
	if _, err := p.Setup("u", "p", port); err != nil {
		t.Fatal(err)
	}
	p.Stop()
	p.Stop()
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("Stop 后端口不应可连")
	}
}

// TestSocks5SetupRestartChangesPort 验证再次 Setup 换端口时旧监听被替换。
func TestSocks5SetupRestartChangesPort(t *testing.T) {
	p := newTestSocks5(t)
	port1 := freePort(t)
	if _, err := p.Setup("u", "p", port1); err != nil {
		t.Fatal(err)
	}
	oldSrv := p.srv

	port2 := freePort(t)
	actual, err := p.Setup("u2", "p2", port2)
	if err != nil {
		t.Fatal(err)
	}
	if actual != port2 {
		t.Fatalf("换端口 Setup 返回 = %d, want %d", actual, port2)
	}
	if p.srv == oldSrv {
		t.Fatal("换配置后应重建 Server 实例")
	}
	if p.runUser != "u2" || p.runPass != "p2" {
		t.Fatalf("运行中凭据未更新: %q/%q", p.runUser, p.runPass)
	}
	// 旧端口已释放（可重新绑定）。
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port1))
	if err != nil {
		t.Fatalf("旧端口未被释放: %v", err)
	}
	ln.Close()
}

// TestSocks5StartConfigError 验证配置文件损坏时 Start 返回错误。
func TestSocks5StartConfigError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, config.Socks5PluginFileName)
	if err := os.WriteFile(bad, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewSocks5(dir)
	if err := p.Start(&Context{}); err == nil {
		t.Fatal("配置损坏时 Start 应报错")
	}
}

// TestSocks5StartReportsError 验证端口全忙时 Start 报错（经 Manager 冒泡）。
func TestSocks5StartReportsError(t *testing.T) {
	// 占住 10 个连续端口，顺延空间耗尽。
	var occupiers []net.Listener
	defer func() {
		for _, l := range occupiers {
			l.Close()
		}
	}()
	base := freePort(t)
	for i := 0; i < config.Socks5PortTries; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(base+i)))
		if err != nil {
			t.Skipf("无法占住测试端口段: %v", err)
		}
		occupiers = append(occupiers, ln)
	}

	dir := t.TempDir()
	p := NewSocks5(dir)
	p.SetListenHostForTest("127.0.0.1")
	if _, err := p.Setup("u", "p", base); err == nil {
		t.Fatal("端口全忙时 Setup 应报错")
	}
	if p.srv != nil {
		t.Fatal("失败后不应残留监听")
	}
}
