package admin

import (
	"encoding/json"
	"os"
)

// adminInfo 是 admin.json 的内容，供 CLI 连接管理通道。
type adminInfo struct {
	Addr   string `json:"addr"`
	Secret string `json:"secret"`
	PID    int    `json:"pid"`
	Port   int    `json:"port"`
}

// writeAdminInfo 原子写入管理通道信息。
func writeAdminInfo(path, addr, secret string, pid, port int) error {
	b, err := json.MarshalIndent(adminInfo{Addr: addr, Secret: secret, PID: pid, Port: port}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
