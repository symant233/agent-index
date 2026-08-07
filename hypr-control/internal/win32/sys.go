package win32

import (
	"fmt"
)

// LockWorkstation 锁屏。
func LockWorkstation() error {
	r1, _, err := procLockWorkStation.Call()
	if r1 == 0 {
		return fmt.Errorf("LockWorkStation: %v", err)
	}
	return nil
}
