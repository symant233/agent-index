package win32

import (
	"fmt"
	"os/exec"
	"strconv"
)

// Shutdown 延时（秒）后关机；延时期间可在主机上执行 `shutdown /a` 取消。
func Shutdown(delaySeconds int) error {
	if err := exec.Command("shutdown.exe", "/s", "/t", strconv.Itoa(delaySeconds)).Run(); err != nil {
		return fmt.Errorf("shutdown: %v", err)
	}
	return nil
}

// Restart 延时（秒）后重启；延时期间可在主机上执行 `shutdown /a` 取消。
func Restart(delaySeconds int) error {
	if err := exec.Command("shutdown.exe", "/r", "/t", strconv.Itoa(delaySeconds)).Run(); err != nil {
		return fmt.Errorf("restart: %v", err)
	}
	return nil
}
