// Package socks5 实现带用户名/密码鉴权的 SOCKS5 代理服务器（RFC 1928/1929）。
//
// 仅支持 CONNECT 命令（TCP 隧道）：局域网设备的浏览器/系统代理访问网页
// 与绝大多数应用只需要 CONNECT；BIND 与 UDP ASSOCIATE 不支持。
// 目标为域名时在本机解析，因此天然使用本机 DNS，内网域名可直达。
//
// 本包只做协议，不落盘不读配置；监听端口探测与配置持久化由插件层负责。
package socks5

import (
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 协议常量（RFC 1928/1929）。
const (
	socksVersion = 0x05
	authVersion  = 0x01
	authUserPass = 0x02 // 方法协商中的"用户名/密码"子协商法

	cmdConnect = 0x01

	atypIPv4   = 0x01
	atypDomain = 0x03
	atypIPv6   = 0x04

	repSucceeded          = 0x00
	repGeneralFailure     = 0x01
	repNetworkUnreachable = 0x03
	repHostUnreachable    = 0x04
	repConnectionRefused  = 0x05
	repTTLExpired         = 0x06
	repCommandNotSupported = 0x07
)

// 握手与拨号限时：防止慢速/异常客户端长期占用连接。
const (
	handshakeTimeout = 30 * time.Second
	dialTimeout      = 10 * time.Second
)

// Server 是一个 SOCKS5 代理服务器。凭据在构造时固定；
// 换凭据/换端口由调用方 Close 后重建（插件层负责）。
type Server struct {
	username, password string
	log                func(format string, args ...any)

	mu     sync.Mutex
	ln     net.Listener
	conns  map[net.Conn]struct{} // 在途客户端连接（Close 时统一关闭以解阻塞转发）
	closed bool
}

// NewServer 以固定凭据构造服务器。log 可为 nil（不输出日志）。
func NewServer(username, password string, log func(format string, args ...any)) *Server {
	return &Server{
		username: username,
		password: password,
		log:      log,
		conns:    make(map[net.Conn]struct{}),
	}
}

// Serve 在 ln 上启动服务：同步登记监听后立即返回，接受循环在后台运行。
// 绑定由调用方完成，本方法返回即代表监听就绪（Close 立刻生效）。
// 同一 Server 只能 Serve 一次；Close 后不可复用。
func (s *Server) Serve(ln net.Listener) error {
	s.mu.Lock()
	if s.ln != nil || s.closed {
		s.mu.Unlock()
		return errors.New("socks5 服务已启动或已关闭")
	}
	s.ln = ln
	s.mu.Unlock()
	go s.acceptLoop(ln)
	return nil
}

// acceptLoop 接受连接直至 Close 关闭监听。非 Close 导致的失败记日志后退出。
func (s *Server) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if !closed {
				s.logf("接受连接失败，停止服务: %v", err)
			}
			return
		}
		go s.handleConn(conn)
	}
}

// Close 停止接受新连接并关闭全部在途连接（隧道两端随之结束）。
// 不等待在途 goroutine 退出：监听 socket 已关闭、端口立即可重绑，
// 进程常驻场景下残留转发 goroutine 会随连接关闭自行结束。
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	for c := range s.conns {
		c.Close()
	}
	return err
}

func (s *Server) handleConn(conn net.Conn) {
	s.mu.Lock()
	s.conns[conn] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		conn.Close()
	}()
	if err := s.serve(conn); err != nil {
		s.logf("会话异常（%s）: %v", conn.RemoteAddr(), err)
	}
}

// serve 处理单个连接的完整生命周期：鉴权 → 请求 → 拨号 → 转发。
func (s *Server) serve(conn net.Conn) error {
	// 握手全程限时。
	conn.SetDeadline(time.Now().Add(handshakeTimeout))

	if err := s.auth(conn); err != nil {
		return err
	}

	req, err := readRequest(conn)
	if err != nil {
		return err
	}
	if req.cmd != cmdConnect {
		writeReply(conn, repCommandNotSupported, nil)
		return fmt.Errorf("不支持的命令 0x%02x（仅支持 CONNECT）", req.cmd)
	}

	// 域名在本机解析，天然使用本机 DNS（内网域名可直达）。
	conn.SetDeadline(time.Now().Add(dialTimeout))
	up, err := net.DialTimeout("tcp", req.target, dialTimeout)
	if err != nil {
		writeReply(conn, replyForDialError(err), nil)
		return fmt.Errorf("拨号 %s 失败: %w", req.target, err)
	}
	defer up.Close()

	// 成功应答携带出口连接的本地地址（BND.ADDR/BND.PORT）。
	writeReply(conn, repSucceeded, up.LocalAddr())

	// 建连后取消握手期限，进入纯转发模式。
	conn.SetDeadline(time.Time{})
	s.logf("%s → %s", conn.RemoteAddr(), req.target)

	// 双向转发：任一侧结束即关闭上游，另一侧的拷贝随之解阻塞。
	go func() {
		io.Copy(up, conn)
		up.Close()
	}()
	io.Copy(conn, up)
	return nil
}

// auth 完成方法协商与用户名/密码子协商（RFC 1929）。
// 本代理要求鉴权：未提供用户名/密码法的客户端直接拒绝。
func (s *Server) auth(conn net.Conn) error {
	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return fmt.Errorf("读取方法协商失败: %w", err)
	}
	if head[0] != socksVersion {
		return fmt.Errorf("非 SOCKS5 客户端（版本 0x%02x）", head[0])
	}
	methods := make([]byte, head[1])
	if len(methods) > 0 {
		if _, err := io.ReadFull(conn, methods); err != nil {
			return fmt.Errorf("读取方法列表失败: %w", err)
		}
	}
	if !containsByte(methods, authUserPass) {
		conn.Write([]byte{socksVersion, 0xFF}) // NO ACCEPTABLE METHODS
		return errors.New("客户端未提供用户名/密码鉴权法")
	}
	if _, err := conn.Write([]byte{socksVersion, authUserPass}); err != nil {
		return err
	}

	// RFC 1929 子协商：VER ULEN UNAME PLEN PASSWD。
	var ah [2]byte
	if _, err := io.ReadFull(conn, ah[:]); err != nil {
		return fmt.Errorf("读取鉴权头失败: %w", err)
	}
	if ah[0] != authVersion {
		return fmt.Errorf("鉴权子协商版本错误 0x%02x", ah[0])
	}
	user, err := readLenBytes(conn, ah[1])
	if err != nil {
		return fmt.Errorf("读取用户名失败: %w", err)
	}
	var pl [1]byte
	if _, err := io.ReadFull(conn, pl[:]); err != nil {
		return fmt.Errorf("读取密码长度失败: %w", err)
	}
	pass, err := readLenBytes(conn, pl[0])
	if err != nil {
		return fmt.Errorf("读取密码失败: %w", err)
	}

	// 恒定时间比较，避免时序侧信道。
	okU := subtle.ConstantTimeCompare([]byte(s.username), user)
	okP := subtle.ConstantTimeCompare([]byte(s.password), pass)
	if okU != 1 || okP != 1 {
		conn.Write([]byte{authVersion, 0x01}) // status 非 0 即失败
		return errors.New("用户名或密码错误")
	}
	if _, err := conn.Write([]byte{authVersion, 0x00}); err != nil {
		return err
	}
	return nil
}

// readLenBytes 读取 1 字节长度前缀的字段（按调用方解析好的长度 n）。
func readLenBytes(conn io.Reader, n byte) ([]byte, error) {
	if n == 0 {
		return nil, nil
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(conn, b); err != nil {
		return nil, err
	}
	return b, nil
}

func containsByte(haystack []byte, needle byte) bool {
	for _, b := range haystack {
		if b == needle {
			return true
		}
	}
	return false
}

// socksRequest 是解析后的 CONNECT 请求。
type socksRequest struct {
	cmd    byte
	target string // host:port
}

// readRequest 读取并解析请求：VER CMD RSV ATYP DST.ADDR DST.PORT。
func readRequest(conn net.Conn) (socksRequest, error) {
	var head [4]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return socksRequest{}, fmt.Errorf("读取请求头失败: %w", err)
	}
	if head[0] != socksVersion {
		return socksRequest{}, fmt.Errorf("请求版本错误 0x%02x", head[0])
	}
	var host string
	switch head[3] {
	case atypIPv4:
		var b [4]byte
		if _, err := io.ReadFull(conn, b[:]); err != nil {
			return socksRequest{}, fmt.Errorf("读取 IPv4 地址失败: %w", err)
		}
		host = net.IP(b[:]).String()
	case atypDomain:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			return socksRequest{}, fmt.Errorf("读取域名长度失败: %w", err)
		}
		b := make([]byte, l[0])
		if _, err := io.ReadFull(conn, b); err != nil {
			return socksRequest{}, fmt.Errorf("读取域名失败: %w", err)
		}
		host = string(b)
	case atypIPv6:
		var b [16]byte
		if _, err := io.ReadFull(conn, b[:]); err != nil {
			return socksRequest{}, fmt.Errorf("读取 IPv6 地址失败: %w", err)
		}
		host = net.IP(b[:]).String()
	default:
		return socksRequest{}, fmt.Errorf("不支持的地址类型 0x%02x", head[3])
	}
	var portB [2]byte
	if _, err := io.ReadFull(conn, portB[:]); err != nil {
		return socksRequest{}, fmt.Errorf("读取端口失败: %w", err)
	}
	port := binary.BigEndian.Uint16(portB[:])
	return socksRequest{cmd: head[1], target: net.JoinHostPort(host, strconv.Itoa(int(port)))}, nil
}

// writeReply 回写应答：VER REP RSV ATYP BND.ADDR BND.PORT。
// bindAddr 非空时作为 BND（成功应答携带出口连接本地地址）；nil 时置零。
func writeReply(conn net.Conn, rep byte, bindAddr net.Addr) {
	b := []byte{socksVersion, rep, 0x00, atypIPv4, 0, 0, 0, 0, 0, 0}
	if tcp, ok := bindAddr.(*net.TCPAddr); ok {
		if ip4 := tcp.IP.To4(); ip4 != nil {
			copy(b[4:8], ip4)
		}
		b[8] = byte(tcp.Port >> 8)
		b[9] = byte(tcp.Port)
	}
	conn.Write(b)
}

// replyForDialError 把常见拨号错误映射为 SOCKS5 REP 码，无法识别时
// 回一般性失败（0x01）。Windows 的错误文本经字符串特征匹配。
func replyForDialError(err error) byte {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return repHostUnreachable
		}
		return repNetworkUnreachable
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return repTTLExpired
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "refused"):
		return repConnectionRefused
	case strings.Contains(msg, "unreachable"):
		return repNetworkUnreachable
	}
	return repGeneralFailure
}

func (s *Server) logf(format string, args ...any) {
	if s.log != nil {
		s.log(format, args...)
	}
}
