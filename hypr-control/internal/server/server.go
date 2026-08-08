// Package server 提供局域网控制 HTTP 服务（P0）：
// 网页 UI 静态托管、设备配对 API 与遥控控制 API。
// 所有控制请求必须携带已授权设备的 token。
package server

import (
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"hypr-control/internal/config"
	"hypr-control/internal/control"
	"hypr-control/internal/devices"
)

//go:embed all:web
var webFS embed.FS

// Control 是控制服务的处理器集合。
type Control struct {
	store   *devices.Store
	backend control.Backend

	// 防重放：nonce → 过期时间（防止抓包重放控制命令）。
	nonceMu sync.Mutex
	nonces  map[string]time.Time
}

// 防重放参数。
const (
	// replayWindow 允许的请求时间戳与服务器时间差（秒）。
	replayWindow = 120 * time.Second
	// nonceTTL 记录 nonce 的最长保留时间（大于窗口即可）。
	nonceTTL = 300 * time.Second
)

// Start 在 0.0.0.0:cfg.Port 上启动控制服务并返回。
// 同一端口同时服务 HTTPS 与明文 HTTP（明文自动 302 重定向到 https）。
// 监听端口占用时重试（重启流程中旧进程可能尚未完全释放端口）。
func Start(store *devices.Store, backend control.Backend, cfg config.Config) (*http.Server, error) {
	c := &Control{store: store, backend: backend}

	certFile, keyFile, err := EnsureCert(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("准备 TLS 证书失败: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("加载 TLS 证书失败: %w", err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}}

	srv := &http.Server{
		Handler:           c.redirectHandler(c.handler()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(cfg.Port))
	ln, err := listenWithRetry(addr)
	if err != nil {
		return nil, err
	}
	go func() {
		if err := srv.Serve(&dualListener{Listener: ln, cfg: tlsCfg}); err != nil && err != http.ErrServerClosed {
			log.Printf("控制服务异常退出: %v", err)
		}
	}()
	return srv, nil
}

// redirectHandler 把明文 HTTP 请求 302 重定向到同主机 HTTPS（https://<host><uri>）。
func (c *Control) redirectHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil {
			target := "https://" + r.Host + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// handler 组装完整路由：控制/配对 API + 静态网页。
func (c *Control) handler() http.Handler {
	mux := c.routes()
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err) // embed 资源缺失属于构建期错误
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return mux
}

// routes 组装路由：配对接口公开，控制接口需设备 token。
func (c *Control) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/pair", c.handlePair)
	mux.HandleFunc("POST /api/control/key", c.auth(c.handleKey))
	mux.HandleFunc("POST /api/control/keys", c.auth(c.handleKeys))
	mux.HandleFunc("POST /api/control/mouse", c.auth(c.handleMouse))
	mux.HandleFunc("POST /api/control/volume", c.auth(c.handleVolume))
	mux.HandleFunc("POST /api/control/media", c.auth(c.handleMedia))
	mux.HandleFunc("POST /api/control/lock", c.auth(c.handleLock))
	mux.HandleFunc("POST /api/control/power", c.auth(c.handlePower))
	return mux
}

// listenWithRetry 端口占用时最多重试 30 秒（每 500ms 一次），
// 覆盖重启/强杀后端口释放延迟。
// 使用 tcp4：tcp 网络在部分 Windows 环境（代理/TUN 软件）下对 0.0.0.0
// 会触发 IPv6 双栈探测，导致 bind 挂起数十秒；tcp4 即时返回。
func listenWithRetry(addr string) (net.Listener, error) {
	deadline := time.Now().Add(30 * time.Second)
	attempts := 0
	for {
		ln, err := net.Listen("tcp4", addr)
		if err == nil {
			return ln, nil
		}
		attempts++
		if attempts <= 5 {
			log.Printf("警告: 监听 %s 失败(第%d次): %v（若持续失败，请检查防火墙/安全软件是否拦截了端口监听并需手动放行）", addr, attempts, err)
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// auth 包装控制处理器：校验设备 token（Authorization: Bearer <token>）
// 与防重放头（X-Hypr-Timestamp 秒级时间戳 + X-Hypr-Nonce 一次性请求标识）。
func (c *Control) auth(h func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		if token == "" {
			writeErr(w, http.StatusUnauthorized, "缺少设备令牌（Authorization: Bearer <token>）")
			return
		}
		if _, ok := c.store.LookupByToken(token); !ok {
			writeErr(w, http.StatusUnauthorized, "设备令牌无效或已吊销")
			return
		}
		if !c.checkReplay(w, r) {
			return
		}
		h(w, r)
	}
}

// checkReplay 校验时间戳窗口与 nonce 唯一性，失败时写响应并返回 false。
func (c *Control) checkReplay(w http.ResponseWriter, r *http.Request) bool {
	tsStr := r.Header.Get("X-Hypr-Timestamp")
	nonce := r.Header.Get("X-Hypr-Nonce")
	if tsStr == "" || nonce == "" {
		writeErr(w, http.StatusBadRequest, "缺少防重放头：X-Hypr-Timestamp / X-Hypr-Nonce")
		return false
	}
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "时间戳格式错误")
		return false
	}
	diff := time.Now().Unix() - ts
	if diff > int64(replayWindow.Seconds()) || diff < -int64(replayWindow.Seconds()) {
		writeErr(w, http.StatusUnauthorized, "请求时间戳超出允许窗口，拒绝（防重放）")
		return false
	}
	if len(nonce) < 16 || len(nonce) > 128 {
		writeErr(w, http.StatusBadRequest, "nonce 长度必须为 16-128 字符")
		return false
	}

	c.nonceMu.Lock()
	defer c.nonceMu.Unlock()
	if c.nonces == nil {
		c.nonces = make(map[string]time.Time)
	}
	now := time.Now()
	for k, exp := range c.nonces {
		if now.After(exp) {
			delete(c.nonces, k)
		}
	}
	// 用定长哈希作 key：nonce 本身不落 map，避免超长/巨量 nonce 造成内存占用。
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(nonce)))
	if _, dup := c.nonces[key]; dup {
		writeErr(w, http.StatusUnauthorized, "请求标识（nonce）已使用，拒绝（防重放）")
		return false
	}
	c.nonces[key] = now.Add(nonceTTL)
	return true
}

// bearerToken 从请求头提取 Bearer 令牌。
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}
