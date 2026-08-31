package admin

import (
	"hypr-control/internal/config"
	"hypr-control/internal/control"
	"hypr-control/internal/devices"
	"hypr-control/internal/plugins"
	"hypr-control/internal/server"
)

// startControl 启动局域网控制服务（0.0.0.0:port）。
// 插件管理器一并注入：控制服务在执行电源操作前分发插件钩子，
// 与系统级关机广播监听（RunServer 中启动）双保险覆盖。
func startControl(store *devices.Store, mgr *plugins.Manager, cfg config.Config) error {
	_, err := server.Start(store, control.Default(), pluginDispatcherAdapter{mgr}, cfg)
	return err
}
