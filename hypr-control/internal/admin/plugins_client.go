package admin

import (
	"context"
	"net/http"
)

// Plugin 是 CLI 侧的插件视图（与 PluginInfo 一致）。
type Plugin = PluginInfo

// ListPlugins 查询全部插件及启停状态。
func (c *Client) ListPlugins(ctx context.Context) ([]Plugin, error) {
	var out []Plugin
	err := c.do(ctx, http.MethodGet, "/api/admin/plugins", nil, &out)
	return out, err
}

// EnablePlugin 启用一个插件。
func (c *Client) EnablePlugin(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/api/admin/plugins/enable", map[string]string{"name": name}, nil)
}

// DisablePlugin 禁用一个插件。
func (c *Client) DisablePlugin(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodPost, "/api/admin/plugins/disable", map[string]string{"name": name}, nil)
}

// Socks5Status 是 socks5 代理的配置状态视图。
type Socks5Status struct {
	Port      int `json:"port"`      // 实际监听端口（请求端口被占用时已顺延）
	Requested int `json:"requested"` // 请求的端口
}

// SetupSocks5 配置并启用 socks5 代理插件，返回实际监听端口。
func (c *Client) SetupSocks5(ctx context.Context, username, password string, port int) (Socks5Status, error) {
	var out Socks5Status
	err := c.do(ctx, http.MethodPost, "/api/admin/plugins/socks5/setup",
		map[string]any{"username": username, "password": password, "port": port}, &out)
	return out, err
}
