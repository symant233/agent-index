package server

import (
	"crypto/tls"
	"io"
	"net"
	"time"
)

// dualListener 让同一端口同时服务 HTTPS 与明文 HTTP：
// Accept 时窥探首字节，0x16（TLS ClientHello）走 TLS，其余按明文 HTTP 处理
// （由 handler 重定向到 https）。这样用户无论访问 http:// 还是 https:// 都可用。
type dualListener struct {
	net.Listener
	cfg *tls.Config
}

// classifyTimeout 是等待客户端首字节的最长时间（防止连接挂起）。
const classifyTimeout = 5 * time.Second

func (l *dualListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return classify(c, l.cfg), nil
}

// classify 判断连接是 TLS 还是明文 HTTP，返回相应包装的连接。
func classify(c net.Conn, cfg *tls.Config) net.Conn {
	buf := make([]byte, 1)
	c.SetReadDeadline(time.Now().Add(classifyTimeout))
	n, _ := io.ReadFull(c, buf)
	c.SetReadDeadline(time.Time{})
	pc := &peekConn{Conn: c, buf: buf[:n]}
	if n == 1 && buf[0] == 0x16 { // TLS handshake 首字节
		return tls.Server(pc, cfg)
	}
	return pc
}

// peekConn 把已窥探的首字节回填给读取流。
type peekConn struct {
	net.Conn
	buf []byte
	off int
}

func (p *peekConn) Read(b []byte) (int, error) {
	if p.off < len(p.buf) {
		n := copy(b, p.buf[p.off:])
		p.off += n
		return n, nil
	}
	return p.Conn.Read(b)
}
