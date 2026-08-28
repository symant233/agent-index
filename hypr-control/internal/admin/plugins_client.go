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
