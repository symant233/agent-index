// Package autostart 管理 hctrl 的开机自启动注册（HKCU Run 注册表键，
// 当前用户生效，无需管理员权限）。通过 advapi32.dll 直调，无外部依赖。
package autostart

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

// runKeyPath 是当前用户的开机启动注册表键。
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// valueName 是注册表中的值名。
const valueName = "hctrl"

// regErrFileNotFound = ERROR_FILE_NOT_FOUND（注册表 API 返回值）。
const regErrFileNotFound = 2

var (
	advapi32            = syscall.NewLazyDLL("advapi32.dll")
	procRegCreateKeyExW = advapi32.NewProc("RegCreateKeyExW")
	procRegSetValueExW  = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW = advapi32.NewProc("RegDeleteValueW")
)

const (
	keySetQuery = syscall.KEY_SET_VALUE | syscall.KEY_QUERY_VALUE
)

// Enable 写入开机自启动：注册表值为 "exePath" serve --daemon --data-dir "dataDir"。
func Enable(exePath, dataDir string) error {
	value := fmt.Sprintf(`"%s" serve --daemon --data-dir "%s"`, exePath, dataDir)
	return setRunValue(value)
}

// Disable 移除开机自启动注册。
func Disable() error {
	key, err := openRunKey(keySetQuery)
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(key)

	name, err := syscall.UTF16PtrFromString(valueName)
	if err != nil {
		return err
	}
	r1, _, e := procRegDeleteValueW.Call(uintptr(key), uintptr(unsafe.Pointer(name)))
	if r1 != 0 && r1 != regErrFileNotFound {
		return fmt.Errorf("删除自启动注册失败: %v", e)
	}
	return nil
}

// Status 查询自启动注册状态。
func Status() (enabled bool, command string, err error) {
	key, err := openRunKey(syscall.KEY_QUERY_VALUE)
	if err != nil {
		return false, "", err
	}
	defer syscall.RegCloseKey(key)

	name, err := syscall.UTF16PtrFromString(valueName)
	if err != nil {
		return false, "", err
	}
	var typ uint32
	var buf [1024]byte
	bufLen := uint32(len(buf))
	err = syscall.RegQueryValueEx(key, name, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &bufLen)
	if err == syscall.ERROR_FILE_NOT_FOUND {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("查询自启动注册失败: %v", err)
	}
	cmd := syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&buf[0])), bufLen/2))
	return true, strings.TrimSpace(cmd), nil
}

// setRunValue 写入 Run 键的 hctrl 值。
func setRunValue(value string) error {
	key, err := openRunKey(keySetQuery)
	if err != nil {
		return err
	}
	defer syscall.RegCloseKey(key)

	name, err := syscall.UTF16PtrFromString(valueName)
	if err != nil {
		return err
	}
	v, err := syscall.UTF16FromString(value)
	if err != nil {
		return err
	}
	r1, _, e := procRegSetValueExW.Call(
		uintptr(key),
		uintptr(unsafe.Pointer(name)),
		0,
		uintptr(syscall.REG_SZ),
		uintptr(unsafe.Pointer(&v[0])),
		uintptr(len(v)*2),
	)
	if r1 != 0 {
		return fmt.Errorf("写入自启动注册失败: %v", e)
	}
	return nil
}

// openRunKey 打开（必要时创建）Run 键。
func openRunKey(access uint32) (syscall.Handle, error) {
	var key syscall.Handle
	path, err := syscall.UTF16PtrFromString(runKeyPath)
	if err != nil {
		return 0, err
	}
	err = syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, path, 0, access, &key)
	if err == syscall.ERROR_FILE_NOT_FOUND {
		// 键不存在则创建
		r1, _, e2 := procRegCreateKeyExW.Call(
			uintptr(syscall.HKEY_CURRENT_USER),
			uintptr(unsafe.Pointer(path)),
			0, // Reserved
			0, // lpClass
			0, // dwOptions: REG_OPTION_NON_VOLATILE
			uintptr(access),
			0, // lpSecurityAttributes
			uintptr(unsafe.Pointer(&key)),
			0, // lpdwDisposition
		)
		if r1 != 0 {
			return 0, fmt.Errorf("创建开机启动注册表键失败: %v", e2)
		}
		return key, nil
	}
	if err != nil {
		return 0, fmt.Errorf("打开开机启动注册表键失败: %v", err)
	}
	return key, nil
}
