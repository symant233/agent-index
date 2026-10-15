package plugins

// 插件 socks5：在本机启动带用户名/密码鉴权的 SOCKS5 代理服务器
//（RFC 1928/1929，仅 CONNECT/TCP）。局域网设备把代理指向本机 IP:端口，
// 流量即经本机网络出站，可访问内网资源（域名在本机解析，走本机 DNS）。
//
// 与钩子型插件（shutdown-volume）不同，这是生命周期型插件：
// 不订阅钩子，实现 Starter/Stopper 可选接口随启用/禁用启停，
// 守护进程启动时 Manager 对已启用插件调用 Start。
//
// 配置交互：`hctrl plugins enable socks5`（CLI 依次输入用户名/密码/端口），
// 经管理 API 调 Setup 一步完成"存配置 + 启动 + 置为启用"。
// 配置持久化在数据目录 plugin-socks5.json：明文保存代理凭据，
// 权限 0600，仅本机当前用户可读。
//
// 本文件为一个独立插件的全部实现；机制代码见 plugins.go，
// 注册登记见 registry.go。

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"hypr-control/internal/config"
	"hypr-control/internal/socks5"
)

// 插件名称（CLI 启停用的键，发布后不可更改）。
const NameSocks5 = "socks5"

// Socks5Config 是插件持久化配置（plugin-socks5.json）。
type Socks5Config struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Port     int    `json:"port"` // 上次实际绑定成功的端口（粘滞：下次启动首选）
}

// LoadSocks5Config 读取配置文件；文件不存在返回零值配置（视为未配置）。
// 导出供 CLI 在交互式启用时提供默认值。
func LoadSocks5Config(path string) (Socks5Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Socks5Config{}, nil
		}
		return Socks5Config{}, fmt.Errorf("读取 socks5 配置失败: %w", err)
	}
	var cfg Socks5Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Socks5Config{}, fmt.Errorf("解析 socks5 配置失败: %w", err)
	}
	return cfg, nil
}

// Socks5 插件：管理 SOCKS5 代理服务器的配置与启停。
type Socks5 struct {
	configPath string // 配置文件路径；空表示不持久化（测试）
	logf       func(format string, args ...any)

	listenHost string // 监听主机；默认 0.0.0.0（局域网可达）

	mu      sync.Mutex
	srv     *socks5.Server
	runUser string
	runPass string
	runPort int // 当前监听端口；0 表示未监听
}

// 编译期断言：实现 Plugin 与两个可选生命周期接口。
var (
	_ Plugin  = (*Socks5)(nil)
	_ Starter = (*Socks5)(nil)
	_ Stopper = (*Socks5)(nil)
)

// NewSocks5 构造插件。dataDir 为数据目录；空串禁用配置持久化（测试用）。
func NewSocks5(dataDir string) *Socks5 {
	p := &Socks5{
		listenHost: "0.0.0.0",
		logf: func(format string, args ...any) {
			log.Printf("[plugin] socks5: "+format, args...)
		},
	}
	if dataDir != "" {
		p.configPath = filepath.Join(dataDir, config.Socks5PluginFileName)
	}
	return p
}

// SetListenHostForTest 覆盖监听主机。测试注入 127.0.0.1：
// 回环监听不触发 Windows 防火墙放行询问（0.0.0.0 会按程序弹窗）。
func (p *Socks5) SetListenHostForTest(host string) { p.listenHost = host }

// Name 实现 Plugin。
func (p *Socks5) Name() string { return NameSocks5 }

// Desc 实现 Plugin。
func (p *Socks5) Desc() string {
	return "SOCKS5 代理：向局域网设备提供带用户名/密码鉴权的代理通道，经本机网络访问内网资源"
}

// Hooks 实现 Plugin：生命周期型插件不订阅钩子，随启用/禁用启停。
func (p *Socks5) Hooks() []Hook { return nil }

// OnHook 实现 Plugin：未订阅任何钩子，不会被调用。
func (p *Socks5) OnHook(Hook, *Context) error { return nil }

// Setup 校验并保存配置，重启监听，返回实际绑定端口。
// 端口被占用时自增顺延（最多 config.Socks5PortTries 个），实际端口
// 粘滞写回配置（下次启动首选，避免重启后端口漂移导致设备代理失效）。
func (p *Socks5) Setup(username, password string, port int) (int, error) {
	switch {
	case username == "":
		return 0, fmt.Errorf("用户名不能为空")
	case len(username) > 255:
		return 0, fmt.Errorf("用户名过长（SOCKS5 协议上限 255 字节）")
	case password == "":
		return 0, fmt.Errorf("密码不能为空")
	case len(password) > 255:
		return 0, fmt.Errorf("密码过长（SOCKS5 协议上限 255 字节）")
	case port < 1 || port > 65535:
		return 0, fmt.Errorf("端口需为 1-65535 的整数")
	}

	actual, started, err := p.start(username, password, port)
	if err != nil {
		return 0, err
	}
	if err := p.saveConfig(Socks5Config{Username: username, Password: password, Port: actual}); err != nil {
		return actual, fmt.Errorf("代理已监听 %s:%d，但保存配置失败（重启后不会自动恢复）: %w", p.listenHost, actual, err)
	}
	if started {
		p.logf("已监听 %s:%d（请求端口 %d），用户名 %q", p.listenHost, actual, port, username)
	}
	return actual, nil
}

// Start 实现 Starter：守护进程启动时由 Manager 调用（插件处于启用状态）。
// 无配置/配置不完整时只记日志不报错（等待用户交互式启用）；
// 已在运行时幂等返回。
func (p *Socks5) Start(_ *Context) error {
	cfg, err := p.loadConfig()
	if err != nil {
		return err
	}
	if cfg.Username == "" || cfg.Password == "" || cfg.Port == 0 {
		p.logf("已启用但尚未配置，执行 hctrl plugins enable socks5 完成配置")
		return nil
	}
	port, started, err := p.start(cfg.Username, cfg.Password, cfg.Port)
	if err != nil {
		return err
	}
	if started {
		p.logf("已随守护进程启动，监听 %s:%d（用户名 %q）", p.listenHost, port, cfg.Username)
	}
	return nil
}

// Stop 实现 Stopper：停止代理监听（幂等）。
func (p *Socks5) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv == nil {
		return
	}
	p.srv.Close()
	p.srv = nil
	p.runPort = 0
	p.logf("代理已停止")
}

// start 启动（或按新配置重启）代理监听。
// 返回实际端口与是否新启动（false 表示已在按相同配置运行，幂等跳过）。
func (p *Socks5) start(username, password string, port int) (int, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.srv != nil && p.runUser == username && p.runPass == password && p.runPort == port {
		return p.runPort, false, nil
	}
	// 先停旧实例：否则目标端口恰为旧监听端口时，探测必然失败。
	if p.srv != nil {
		p.srv.Close()
		p.srv = nil
		p.runPort = 0
	}
	srv := socks5.NewServer(username, password, p.logf)
	var lastErr error
	for i := 0; i < config.Socks5PortTries; i++ {
		try := port + i
		// 显式 tcp4：Go 对通配地址默认监听 IPv6 双栈，Windows 上不会与
		// 已存在的 IPv4 端口占用冲突，端口顺延探测会漏判（同控制服务的
		// tcp4 处理，见 README 已知限制）。
		ln, err := net.Listen("tcp4", net.JoinHostPort(p.listenHost, strconv.Itoa(try)))
		if err != nil {
			lastErr = err
			continue
		}
		if err := srv.Serve(ln); err != nil {
			ln.Close()
			return 0, false, fmt.Errorf("socks5 服务启动失败: %w", err)
		}
		p.srv = srv
		p.runUser, p.runPass, p.runPort = username, password, try
		if try != port {
			p.logf("端口 %d 被占用，已自动顺延至 %d", port, try)
		}
		return try, true, nil
	}
	return 0, false, fmt.Errorf("端口 %d 起连续 %d 个端口均被占用（最后的错误: %v）",
		port, config.Socks5PortTries, lastErr)
}

// loadConfig 读取持久化配置；未启用持久化时返回零值配置。
func (p *Socks5) loadConfig() (Socks5Config, error) {
	if p.configPath == "" {
		return Socks5Config{}, nil
	}
	return LoadSocks5Config(p.configPath)
}

// saveConfig 原子写入配置（0600，凭据仅本机当前用户可读）。
func (p *Socks5) saveConfig(cfg Socks5Config) error {
	if p.configPath == "" {
		return nil
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.configPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("写入 socks5 配置失败: %w", err)
	}
	return os.Rename(tmp, p.configPath)
}
