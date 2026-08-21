package win32

import (
	"fmt"
	"os/exec"
	"strconv"
)

// Shutdown 立即关机（delaySeconds 为 0 时不可取消；>0 时可在延时期间执行 `shutdown /a` 取消）。
func Shutdown(delaySeconds int) error {
	if err := exec.Command("shutdown.exe", "/s", "/t", strconv.Itoa(delaySeconds)).Run(); err != nil {
		return fmt.Errorf("shutdown: %v", err)
	}
	return nil
}

// Restart 立即重启（delaySeconds 为 0 时不可取消；>0 时可在延时期间执行 `shutdown /a` 取消）。
func Restart(delaySeconds int) error {
	if err := exec.Command("shutdown.exe", "/r", "/t", strconv.Itoa(delaySeconds)).Run(); err != nil {
		return fmt.Errorf("restart: %v", err)
	}
	return nil
}
