package admin

import (
	"hypr-control/internal/config"
	"hypr-control/internal/control"
	"hypr-control/internal/devices"
	"hypr-control/internal/server"
)

// startControl 启动局域网控制服务（0.0.0.0:port）。
func startControl(store *devices.Store, cfg config.Config) error {
	_, err := server.Start(store, control.Default(), cfg)
	return err
}
