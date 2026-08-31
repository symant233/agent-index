package admin

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"hypr-control/internal/config"
	"hypr-control/internal/devices"
	"hypr-control/internal/plugins"
	"hypr-control/internal/win32"
)

// Server 提供仅监听 127.0.0.1 的管理 HTTP 服务，
// 供本机 CLI（hctrl status / devices ... / restart / kill）调用。
type Server struct {
	store   *devices.Store
	secret  string
	started time.Time
	plugins PluginRegistry

	// 由 RunServer 注入
	port       int      // 控制服务端口（status 展示用）
	startArgs  []string // 服务启动参数（重启时复用）
	onShutdown func()   // 进程级退出回调（默认 os.Exit）

	mu  sync.Mutex
	ln  net.Listener
	srv *http.Server
}

// PluginRegistry 是管理面对插件机制的只读视图（由 internal/plugins.Manager 实现）。
// 定义在 admin 包以避免 admin 直接依赖 plugins 的具体类型。
type PluginRegistry interface {
	List() []PluginInfo
	Enable(name string) error
	Disable(name string) error
}

// PluginInfo 与 plugins.Info 字段一致（JSON 直接透传）。
type PluginInfo struct {
	Name    string   `json:"name"`
	Desc    string   `json:"desc"`
	Hooks   []string `json:"hooks"`
	Enabled bool     `json:"enabled"`
}

// NewServer 构造管理服务。secret 用于 CLI 认证。
func NewServer(store *devices.Store, secret string) *Server {
	return &Server{
		store:   store,
		secret:  secret,
		started: time.Now(),
	}
}

// Addr 返回管理监听地址（如 "127.0.0.1:54321"）。
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Listen 在 config.AdminHost 随机端口上启动管理服务。
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", net.JoinHostPort(config.AdminHost, "0"))
	if err != nil {
		return fmt.Errorf("管理服务监听失败: %w", err)
	}
	s.mu.Lock()
	s.ln = ln
	s.srv = &http.Server{Handler: s}
	s.mu.Unlock()
	go s.srv.Serve(ln)
	return nil
}

// Close 关闭管理服务。
func (s *Server) Close() error {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv != nil {
		return srv.Close()
	}
	return nil
}

// ServeHTTP 实现管理 API。所有请求必须携带 secret。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// 根路径返回友好提示：管理通道仅供本机 CLI 使用，不是网页。
	if r.URL.Path == "/" && r.Method == http.MethodGet {
		writeJSON(w, map[string]string{
			"service": "hypr-control 管理通道（仅本机 CLI 可用）",
			"hint":    fmt.Sprintf("网页控制请访问控制端口（默认 %d，hctrl status 可查监听地址）", config.DefaultPort),
		})
		return
	}
	if r.Header.Get("X-Hypr-Secret") != s.secret {
		writeErr(w, http.StatusUnauthorized, "管理密钥错误")
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/admin/status":
		s.handleStatus(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/admin/devices":
		s.handleList(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/admin/devices/allow":
		s.handleAllow(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/admin/devices/deny":
		s.handleDeny(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/admin/devices/revoke":
		s.handleRevoke(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/admin/shutdown":
		s.handleShutdown(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/admin/restart":
		s.handleRestart(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/admin/plugins":
		s.handlePlugins(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/admin/plugins/enable":
		s.handlePluginEnable(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/admin/plugins/disable":
		s.handlePluginDisable(w, r)
	default:
		writeErr(w, http.StatusNotFound, "未知管理接口")
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	pending, authorized := s.store.Count()
	writeJSON(w, StatusInfo{
		State:      "running",
		ListenAddr: fmt.Sprintf("0.0.0.0:%d", s.port),
		AdminAddr:  s.Addr(),
		Uptime:     time.Since(s.started),
		Pending:    pending,
		Authorized: authorized,
	})
}

func (s *Server) handleList(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.store.List())
}

func (s *Server) handleAllow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PIN string `json:"pin"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	d, err := s.store.AuthorizeByPIN(body.PIN)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, d)
}

func (s *Server) handleDeny(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDOrPIN string `json:"id_or_pin"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.Deny(body.IDOrPIN); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"ok": "denied"})
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.store.Revoke(body.ID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"ok": "revoked"})
}

// handleShutdown 响应成功后退出当前进程。
func (s *Server) handleShutdown(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"ok": "shutting down"})
	time.AfterFunc(50*time.Millisecond, func() { s.exit(0) })
}

// handlePlugins 返回全部插件及启停状态。
func (s *Server) handlePlugins(w http.ResponseWriter, _ *http.Request) {
	if s.plugins == nil {
		writeJSON(w, []PluginInfo{})
		return
	}
	writeJSON(w, s.plugins.List())
}

// handlePluginEnable 启用一个插件：{"name":"shutdown-volume"}。
func (s *Server) handlePluginEnable(w http.ResponseWriter, r *http.Request) {
	s.handlePluginSet(w, r, true)
}

// handlePluginDisable 禁用一个插件：{"name":"shutdown-volume"}。
func (s *Server) handlePluginDisable(w http.ResponseWriter, r *http.Request) {
	s.handlePluginSet(w, r, false)
}

func (s *Server) handlePluginSet(w http.ResponseWriter, r *http.Request, enable bool) {
	if s.plugins == nil {
		writeErr(w, http.StatusServiceUnavailable, "插件机制不可用")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeErr(w, http.StatusBadRequest, "缺少 name")
		return
	}
	var err error
	if enable {
		err = s.plugins.Enable(body.Name)
	} else {
		err = s.plugins.Disable(body.Name)
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	result := "disabled"
	if enable {
		result = "enabled"
	}
	writeJSON(w, map[string]string{"ok": result, "name": body.Name})
}

// handleRestart 响应成功后以相同参数拉起新进程，再退出当前进程。
func (s *Server) handleRestart(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"ok": "restarting"})
	time.AfterFunc(50*time.Millisecond, func() {
		if err := spawnSelf(s.startArgs); err != nil {
			log.Printf("重启失败（将退出）: %v", err)
		}
		s.exit(0)
	})
}

func (s *Server) exit(code int) {
	if s.onShutdown != nil {
		s.onShutdown()
		return
	}
	os.Exit(code)
}

// spawnSelf 以 detached 方式重新拉起自身（继承原启动参数）。
// 必须脱离父进程的 job object 与 console：否则父进程退出时
// 新进程会被连带终止（如从任务计划/脚本启动时）。
func spawnSelf(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	return cmd.Start()
}

func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// RunServer 启动常驻服务：写入 admin.json → 启动管理 HTTP。
// 控制服务（0.0.0.0:port）由 internal/server 接入，见 startControl。
// 插件机制在此装配：注册内置插件 → 恢复启停状态 → 监听系统关机广播。
func RunServer(cfg config.Config) error {
	if err := cfg.EnsureDataDir(); err != nil {
		return err
	}

	store, err := devices.Open(cfg.DevicesFile())
	if err != nil {
		return err
	}

	// 插件机制：全部初始禁用，启停状态持久化在数据目录 plugins.json。
	mgr, err := plugins.NewManager(plugins.NewFileStore(cfg.PluginsFile()))
	if err != nil {
		return fmt.Errorf("插件状态加载失败: %w", err)
	}
	if err := mgr.Register(plugins.NewShutdownVolume()); err != nil {
		return fmt.Errorf("内置插件注册失败: %w", err)
	}

	// 系统关机/重启广播监听：WM_QUERYENDSESSION 到达即分发 shutdown 钩子。
	// 插件全部禁用时回调为空转（不执行任何操作），无额外开销。
	if _, err := win32.StartShutdownListener(func() {
		mgr.Dispatch(plugins.HookShutdown, &plugins.Context{
			Log: func(f string, a ...any) { log.Printf("[plugin] "+f, a...) },
		})
	}); err != nil {
		log.Printf("警告: 系统关机事件监听启动失败（插件仅能响应网页电源操作）: %v", err)
	}

	secret := devices.NewToken()
	adminSrv := NewServer(store, secret)
	adminSrv.port = cfg.Port
	adminSrv.startArgs = os.Args[1:]
	adminSrv.onShutdown = func() { os.Exit(0) }
	adminSrv.plugins = pluginRegistryAdapter{mgr}

	if err := adminSrv.Listen(); err != nil {
		return err
	}

	// 写入管理通道信息，供 CLI 读取。
	if err := writeAdminInfo(cfg.AdminFile(), adminSrv.Addr(), secret, os.Getpid(), cfg.Port); err != nil {
		adminSrv.Close()
		return err
	}

	if err := startControl(store, mgr, cfg); err != nil {
		adminSrv.Close()
		return fmt.Errorf("控制服务启动失败: %w", err)
	}
	log.Printf("hypr-control 已启动：管理通道 %s，控制端口 %d", adminSrv.Addr(), cfg.Port)
	select {} // 阻塞；退出由管理 API 或外部信号触发
}

// pluginRegistryAdapter 把 *plugins.Manager 适配为 admin.PluginRegistry，
// 隔离 admin 对 plugins 具体类型的依赖。
type pluginRegistryAdapter struct{ m *plugins.Manager }

func (a pluginRegistryAdapter) List() []PluginInfo {
	list := a.m.List()
	out := make([]PluginInfo, len(list))
	for i, p := range list {
		out[i] = PluginInfo(p)
	}
	return out
}

func (a pluginRegistryAdapter) Enable(name string) error  { return a.m.Enable(name) }
func (a pluginRegistryAdapter) Disable(name string) error { return a.m.Disable(name) }

// pluginDispatcherAdapter 把 *plugins.Manager 适配为 server.PluginDispatcher。
type pluginDispatcherAdapter struct{ m *plugins.Manager }

// Dispatch 按钩子名分发（server 侧以字符串传递钩子，此处转换类型）。
func (a pluginDispatcherAdapter) Dispatch(hook string, logf func(format string, args ...any)) []string {
	return a.m.Dispatch(plugins.Hook(hook), &plugins.Context{Log: logf})
}
