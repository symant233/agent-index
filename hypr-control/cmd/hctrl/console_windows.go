// 终端交互输入助手：socks5 插件的交互式启用是本 CLI 的首个交互式命令。
//
// 控制台场景下密码输入经 Win32 ReadConsoleW 实现掩码回显（' *'，支持退格）；
// stdin 非控制台（重定向/管道）时退化为普通行读取（无掩码），保证脚本化可用。
// 控制台读走裸 Win32 API、非控制台读走 bufio，两条路径互不混用，避免缓冲冲突。

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
	procReadConsoleW   = kernel32.NewProc("ReadConsoleW")
)

// 控制台输入模式位（docs.microsoft.com/console/console-modes）。
const (
	enableLineInput = 0x0002
	enableEchoInput = 0x0004
)

// stdinReader 共享缓冲：非控制台路径的全部行读取都经它，
// 避免每次调用新建 bufio 丢掉已缓冲输入。
var stdinReader = bufio.NewReader(os.Stdin)

// readLine 读取一行普通输入（控制台回显）。
func readLine(prompt string) (string, error) {
	fmt.Print(prompt)
	if !isConsole(os.Stdin) {
		return readPlainLine()
	}
	buf := make([]uint16, 512)
	var n uint32
	r1, _, callErr := procReadConsoleW.Call(os.Stdin.Fd(),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)), 0)
	if r1 == 0 {
		return "", fmt.Errorf("读取输入失败: %v", callErr)
	}
	return strings.TrimRight(utf16ToUTF8(buf[:n]), "\r\n"), nil
}

// readPassword 读取一行密码输入：控制台关闭回显、逐字符回显 '*'（支持退格）；
// 非控制台退化为普通读取。输入内容不落日志。
func readPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	if !isConsole(os.Stdin) {
		fmt.Println("（stdin 非控制台，密码不以掩码显示）")
		return readPlainLine()
	}
	h := os.Stdin.Fd()
	var oldMode uint32
	r1, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(&oldMode)))
	if r1 == 0 {
		return "", errors.New("获取控制台模式失败")
	}
	// 关闭行输入与回显：ReadConsoleW 逐键返回，回显由本函数控制。
	newMode := oldMode &^ (enableLineInput | enableEchoInput)
	if r1, _, _ = procSetConsoleMode.Call(h, uintptr(newMode)); r1 == 0 {
		return "", errors.New("设置控制台模式失败")
	}
	defer procSetConsoleMode.Call(h, uintptr(oldMode)) // 无论成败都恢复原模式

	var chars []uint16
	buf := make([]uint16, 32) // 一次可读到一批（粘贴输入）
	for {
		var n uint32
		r1, _, callErr := procReadConsoleW.Call(h,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)), 0)
		if r1 == 0 {
			return "", fmt.Errorf("读取密码失败: %v", callErr)
		}
		for _, c := range buf[:n] {
			switch {
			case c == '\r' || c == '\n': // Enter：输入结束
				fmt.Println()
				return utf16ToUTF8(chars), nil
			case c == 0x08 || c == 0x7F: // Backspace / DEL
				if len(chars) > 0 {
					chars = chars[:len(chars)-1]
					fmt.Print("\b \b")
				}
			case c >= 0x20 && len(chars) < 256: // 可见字符（上限 256 字符）
				chars = append(chars, c)
				fmt.Print("*")
			}
		}
	}
}

// readPlainLine 非控制台（重定向/管道）路径：普通读一行，无掩码。
func readPlainLine() (string, error) {
	line, err := stdinReader.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return line, nil // 末行无换行符也算有效输入
		}
		return "", err
	}
	return line, nil
}

// isConsole 报告 f 是否为交互式控制台输入。
func isConsole(f *os.File) bool {
	var mode uint32
	r1, _, _ := procGetConsoleMode.Call(f.Fd(), uintptr(unsafe.Pointer(&mode)))
	return r1 != 0
}

// utf16ToUTF8 把 ReadConsoleW 返回的 UTF-16 序列转为字符串
//（代理对自动合成；末尾可能残留的 NUL 去除）。
func utf16ToUTF8(w []uint16) string {
	for len(w) > 0 && w[len(w)-1] == 0 {
		w = w[:len(w)-1]
	}
	return string(utf16.Decode(w))
}
