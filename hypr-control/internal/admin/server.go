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
)

// Server 提供仅监听 127.0.0.1 的管理 HTTP 服务，
// 供本机 CLI（hctrl status / devices ... / restart / kill）调用。
type Server struct {
	store   *devices.Store
	secret  string
	started time.Time

	// 由 RunServer 注入
	port        int    // 控制服务端口（status 展示用）
	startArgs   []string // 服务启动参数（重启时复用）
	onShutdown  func()   // 进程级退出回调（默认 os.Exit）

	mu  sync.Mutex
	ln  net.Listener
	srv *http.Server
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
func RunServer(cfg config.Config) error {
	if err := cfg.EnsureDataDir(); err != nil {
		return err
	}

	store, err := devices.Open(cfg.DevicesFile())
	if err != nil {
		return err
	}

	secret := devices.NewToken()
	adminSrv := NewServer(store, secret)
	adminSrv.port = cfg.Port
	adminSrv.startArgs = os.Args[1:]
	adminSrv.onShutdown = func() { os.Exit(0) }

	if err := adminSrv.Listen(); err != nil {
		return err
	}

	// 写入管理通道信息，供 CLI 读取。
	if err := writeAdminInfo(cfg.AdminFile(), adminSrv.Addr(), secret, os.Getpid(), cfg.Port); err != nil {
		adminSrv.Close()
		return err
	}

	if err := startControl(store, cfg); err != nil {
		adminSrv.Close()
		return fmt.Errorf("控制服务启动失败: %w", err)
	}

	log.Printf("hypr-control 已启动：管理通道 %s，控制端口 %d", adminSrv.Addr(), cfg.Port)
	select {} // 阻塞；退出由管理 API 或外部信号触发
}
