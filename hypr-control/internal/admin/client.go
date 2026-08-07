package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"hypr-control/internal/config"
	"hypr-control/internal/devices"
)

// Device 是管理面看到的设备视图，直接复用 devices.Device。
type Device = devices.Device

// StatusInfo 是服务运行状态快照。
type StatusInfo struct {
	State      string        `json:"state"` // running / stopped
	ListenAddr string        `json:"listen_addr"`
	AdminAddr  string        `json:"admin_addr"`
	Uptime     time.Duration `json:"uptime"`
	Pending    int           `json:"pending_devices"`
	Authorized int           `json:"authorized_devices"`
}

// ErrNotRunning 表示服务未运行（admin.json 不存在或不可达）。
var ErrNotRunning = errors.New("服务未运行（请先执行 hctrl server）")

// Client 是管理 CLI 侧的本地 IPC 客户端。
type Client struct {
	addr   string
	secret string
	pid    int
	port   int
}

// NewClient 从数据目录读取管理通道信息文件（config.AdminFileName）并构造客户端。
func NewClient(dataDir string) (*Client, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, config.AdminFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotRunning
		}
		return nil, fmt.Errorf("读取管理通道信息失败: %w", err)
	}
	var info adminInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return nil, fmt.Errorf("解析管理通道信息失败: %w", err)
	}
	if info.Addr == "" || info.Secret == "" {
		return nil, errors.New("管理通道信息不完整")
	}
	return &Client{addr: info.Addr, secret: info.Secret, pid: info.PID, port: info.Port}, nil
}

// do 发起一个带管理密钥的管理 API 请求。
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.addr+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hypr-Secret", c.secret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接管理通道失败: %w（服务可能已停止）", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		json.Unmarshal(raw, &e)
		if e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("管理接口返回 %s", resp.Status)
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

// Status 查询服务运行状态。
func (c *Client) Status(ctx context.Context) (StatusInfo, error) {
	var st StatusInfo
	err := c.do(ctx, http.MethodGet, "/api/admin/status", nil, &st)
	return st, err
}

// ListDevices 列出全部设备（含待授权设备）。
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	var out []Device
	err := c.do(ctx, http.MethodGet, "/api/admin/devices", nil, &out)
	return out, err
}

// Allow 按 PIN 允许一个待授权设备。
func (c *Client) Allow(ctx context.Context, pin string) error {
	return c.do(ctx, http.MethodPost, "/api/admin/devices/allow", map[string]string{"pin": pin}, nil)
}

// Deny 拒绝一个待授权设备（按 ID 或 PIN）。
func (c *Client) Deny(ctx context.Context, idOrPin string) error {
	return c.do(ctx, http.MethodPost, "/api/admin/devices/deny", map[string]string{"id_or_pin": idOrPin}, nil)
}

// Revoke 吊销一个已授权设备。
func (c *Client) Revoke(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/api/admin/devices/revoke", map[string]string{"id": id}, nil)
}

// Restart 请求服务重启：管理接口确认后服务端自行拉起新进程并退出。
func (c *Client) Restart(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/admin/restart", nil, nil)
}

// Kill 请求服务停止：先优雅 shutdown，超时后强制终止进程。
func (c *Client) Kill(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err := c.do(shutdownCtx, http.MethodPost, "/api/admin/shutdown", nil, nil)
	if err == nil {
		// 等待服务退出并释放端口。
		waitDeadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(waitDeadline) {
			if !c.ping() {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return errors.New("服务未在预期时间内退出")
	}
	return fmt.Errorf("优雅停止失败: %w", err)
}

// ping 探测管理通道是否仍存活。
func (c *Client) ping() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := c.Status(ctx)
	return err == nil
}
