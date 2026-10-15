package socks5

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestServer 在 127.0.0.1 随机端口启动测试服务器，返回地址与停止函数。
func newTestServer(t *testing.T, user, pass string) (string, func()) {
	t.Helper()
	s := NewServer(user, pass, nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Serve(ln); err != nil {
		t.Fatal(err)
	}
	return ln.Addr().String(), func() { s.Close() }
}

// dialSocks5 手写最小 SOCKS5 客户端：方法协商 → 用户名/密码鉴权 →
// CONNECT（域名 ATYP），成功返回隧道连接。
func dialSocks5(t *testing.T, srvAddr, user, pass, targetHost string, targetPort uint16) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp", srvAddr, 3*time.Second)
	if err != nil {
		t.Fatalf("连接代理失败: %v", err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// 方法协商：只声明用户名/密码法。
	if _, err := conn.Write([]byte{socksVersion, 0x01, authUserPass}); err != nil {
		t.Fatal(err)
	}
	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		t.Fatalf("读方法选择失败: %v", err)
	}
	if head[0] != socksVersion || head[1] != authUserPass {
		t.Fatalf("方法选择 = %x, want %x", head, []byte{socksVersion, authUserPass})
	}

	// RFC 1929 子协商。
	if _, err := conn.Write(authSubnegotiation(user, pass)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		t.Fatalf("读鉴权结果失败: %v", err)
	}
	if head[0] != authVersion || head[1] != 0x00 {
		t.Fatalf("鉴权被拒绝: ver=%d status=%d", head[0], head[1])
	}

	// CONNECT 请求（域名 ATYP）。
	req := []byte{socksVersion, cmdConnect, 0x00, atypDomain, byte(len(targetHost))}
	req = append(req, targetHost...)
	var portB [2]byte
	binary.BigEndian.PutUint16(portB[:], targetPort)
	req = append(req, portB[:]...)
	if _, err := conn.Write(req); err != nil {
		t.Fatal(err)
	}

	// 应答：VER REP RSV ATYP BND.ADDR BND.PORT（ATYP 1 时固定 10 字节）。
	var rep [4]byte
	if _, err := io.ReadFull(conn, rep[:]); err != nil {
		t.Fatalf("读应答头失败: %v", err)
	}
	if rep[0] != socksVersion {
		t.Fatalf("应答版本 = %d", rep[0])
	}
	if rep[1] != repSucceeded {
		t.Fatalf("CONNECT 失败: REP=0x%02x", rep[1])
	}
	skipBindAddr(t, conn, rep[3])
	conn.SetDeadline(time.Time{})
	return conn
}

// authSubnegotiation 构造 RFC 1929 用户名/密码子协商报文。
func authSubnegotiation(user, pass string) []byte {
	b := []byte{authVersion, byte(len(user))}
	b = append(b, user...)
	b = append(b, byte(len(pass)))
	return append(b, pass...)
}

// skipBindAddr 按应答中的 ATYP 跳过 BND.ADDR/BND.PORT 字段。
func skipBindAddr(t *testing.T, conn net.Conn, atyp byte) {
	t.Helper()
	var size int
	switch atyp {
	case atypIPv4:
		size = 4 + 2
	case atypDomain:
		var l [1]byte
		if _, err := io.ReadFull(conn, l[:]); err != nil {
			t.Fatal(err)
		}
		size = 1 + int(l[0]) + 2
	case atypIPv6:
		size = 16 + 2
	default:
		t.Fatalf("应答地址类型异常: 0x%02x", atyp)
	}
	if _, err := io.CopyN(io.Discard, conn, int64(size)); err != nil {
		t.Fatal(err)
	}
}

// TestConnectThroughTunnel 端到端：鉴权成功后经隧道完成一次 HTTP 请求。
func TestConnectThroughTunnel(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "hello via socks5")
	}))
	defer backend.Close()

	// 解析后端端口，以域名 ATYP 发送（代理端再解析拨号）。
	addr := strings.TrimPrefix(backend.URL, "http://")
	host, portStr, _ := net.SplitHostPort(addr)
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)

	srvAddr, stop := newTestServer(t, "alice", "s3cret")
	defer stop()

	conn := dialSocks5(t, srvAddr, "alice", "s3cret", host, port)
	defer conn.Close()

	// 隧道内发 HTTP 请求。
	req := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", addr)
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	resp, err := io.ReadAll(bufio.NewReader(conn))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resp), "hello via socks5") {
		t.Fatalf("隧道响应异常: %q", string(resp))
	}
}

// TestWrongPassword 验证密码错误时鉴权被拒。
func TestWrongPassword(t *testing.T) {
	srvAddr, stop := newTestServer(t, "alice", "s3cret")
	defer stop()

	conn, err := net.Dial("tcp", srvAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	conn.Write([]byte{socksVersion, 0x01, authUserPass})
	var head [2]byte
	io.ReadFull(conn, head[:])
	conn.Write(authSubnegotiation("alice", "wrong-pass"))
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		t.Fatal(err)
	}
	if head[1] != 0x01 {
		t.Fatalf("错密码鉴权 status = %d, want 1（失败）", head[1])
	}
}

// TestWrongUsername 验证未知用户名同样被拒。
func TestWrongUsername(t *testing.T) {
	srvAddr, stop := newTestServer(t, "alice", "s3cret")
	defer stop()

	conn, err := net.Dial("tcp", srvAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	conn.Write([]byte{socksVersion, 0x01, authUserPass})
	var head [2]byte
	io.ReadFull(conn, head[:])
	conn.Write(authSubnegotiation("bob", "s3cret"))
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		t.Fatal(err)
	}
	if head[1] != 0x01 {
		t.Fatalf("错用户名鉴权 status = %d, want 1（失败）", head[1])
	}
}

// TestNoAuthMethodRejected 验证不声明用户名/密码法的客户端被拒绝
//（本代理要求鉴权，核心安全语义）。
func TestNoAuthMethodRejected(t *testing.T) {
	srvAddr, stop := newTestServer(t, "alice", "s3cret")
	defer stop()

	conn, err := net.Dial("tcp", srvAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// 只声明"无需鉴权"法（0x00）。
	if _, err := conn.Write([]byte{socksVersion, 0x01, 0x00}); err != nil {
		t.Fatal(err)
	}
	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		t.Fatal(err)
	}
	if head[0] != socksVersion || head[1] != 0xFF {
		t.Fatalf("无鉴权法应答 = %x, want 05 FF", head)
	}
	// 服务端应随即关闭连接。
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	one := make([]byte, 1)
	if n, err := conn.Read(one); err == nil && n > 0 {
		t.Fatalf("拒绝后应关闭连接，但收到了数据: %x", one[:n])
	}
}

// TestConnectOnlyCommandsRejected 验证 BIND/UDP ASSOCIATE 回命令不支持。
func TestConnectOnlyCommandsRejected(t *testing.T) {
	for _, cmd := range []byte{0x02, 0x03} { // BIND, UDP ASSOCIATE
		srvAddr, stop := newTestServer(t, "alice", "s3cret")
		conn := dialSocks5Raw(t, srvAddr, "alice", "s3cret", cmd, "127.0.0.1", 80)
		var rep [4]byte
		if _, err := io.ReadFull(conn, rep[:]); err != nil {
			t.Fatalf("cmd=%d 读应答失败: %v", cmd, err)
		}
		if rep[1] != repCommandNotSupported {
			t.Fatalf("cmd=0x%02x REP = 0x%02x, want 0x07", cmd, rep[1])
		}
		conn.Close()
		stop()
	}
}

// dialSocks5Raw 带自定义 CMD 的握手（供命令拒绝测试用）。
func dialSocks5Raw(t *testing.T, srvAddr, user, pass string, cmd byte, host string, port uint16) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srvAddr)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte{socksVersion, 0x01, authUserPass})
	var head [2]byte
	io.ReadFull(conn, head[:])
	conn.Write(authSubnegotiation(user, pass))
	io.ReadFull(conn, head[:])
	req := []byte{socksVersion, cmd, 0x00, atypIPv4, 127, 0, 0, 1}
	var portB [2]byte
	binary.BigEndian.PutUint16(portB[:], port)
	req = append(req, portB[:]...)
	conn.Write(req)
	return conn
}

// TestIPv4Target 验证 IPv4 ATYP 请求（区别于域名）。
func TestIPv4Target(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "v4 ok")
	}))
	defer backend.Close()

	addr := strings.TrimPrefix(backend.URL, "http://")
	host, portStr, _ := net.SplitHostPort(addr)
	var port uint16
	fmt.Sscanf(portStr, "%d", &port)
	ip := net.ParseIP(host).To4()

	srvAddr, stop := newTestServer(t, "u", "p")
	defer stop()

	conn, err := net.Dial("tcp", srvAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))

	// 握手后以 IPv4 ATYP 发起 CONNECT（4 字节地址 + 2 字节端口）。
	conn.Write([]byte{socksVersion, 0x01, authUserPass})
	var head [2]byte
	io.ReadFull(conn, head[:])
	conn.Write(authSubnegotiation("u", "p"))
	io.ReadFull(conn, head[:])
	req := append([]byte{socksVersion, cmdConnect, 0x00, atypIPv4}, ip...)
	var portB [2]byte
	binary.BigEndian.PutUint16(portB[:], port)
	req = append(req, portB[:]...)
	conn.Write(req)

	var rep [4]byte
	if _, err := io.ReadFull(conn, rep[:]); err != nil {
		t.Fatalf("读应答失败: %v", err)
	}
	if rep[1] != repSucceeded {
		t.Fatalf("IPv4 目标 CONNECT 失败: REP=0x%02x", rep[1])
	}
	skipBindAddr(t, conn, rep[3])
}

// TestBadVersionClosed 验证非 SOCKS5 客户端被直接关闭。
func TestBadVersionClosed(t *testing.T) {
	srvAddr, stop := newTestServer(t, "u", "p")
	defer stop()

	conn, err := net.Dial("tcp", srvAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	conn.Write([]byte{0x04, 0x01, 0x00}) // SOCKS4 greeting
	one := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := conn.Read(one); err == nil && n > 0 {
		t.Fatalf("非 SOCKS5 客户端应被关闭，但收到了数据: %x", one[:n])
	}
}

// TestCloseReleasesPort 验证 Close 后端口可立即重绑（插件 Stop→Start 依赖）。
func TestCloseReleasesPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	s := NewServer("u", "p", nil)
	if err := s.Serve(ln); err != nil {
		t.Fatal(err)
	}

	// 有连接在途时 Close，也不应阻塞。
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close 阻塞超过 3 秒")
	}

	ln2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("Close 后同端口重绑失败: %v", err)
	}
	ln2.Close()
}
