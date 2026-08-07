// Package config 负责 hctrl 的运行配置：局域网控制端口与数据目录。
// 端口/文件名等固定值集中在 constants.go。
package config

import (
	"flag"
	"os"
	"path/filepath"
)

// Config 保存 hctrl 的运行配置。
type Config struct {
	// Port 是局域网控制 HTTP 端口（默认 config.DefaultPort）。
	Port int
	// DataDir 存放运行数据：设备表、管理通道信息。
	DataDir string
}

// Default 返回默认配置：端口 config.DefaultPort，数据目录 %LOCALAPPDATA%\hypr-control。
func Default() Config {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		base = os.TempDir()
	}
	return Config{
		Port:    DefaultPort,
		DataDir: filepath.Join(base, DataDirName),
	}
}

// ParseFlags 解析命令行 flag，未指定的项保持默认值。
func ParseFlags(args []string) (Config, error) {
	cfg, _, err := ParseFlagsWithArgs(args)
	return cfg, err
}

// ParseFlagsWithArgs 解析命令行 flag，并返回剩余的非 flag 参数。
func ParseFlagsWithArgs(args []string) (Config, []string, error) {
	cfg := Default()
	fs := flag.NewFlagSet("hctrl", flag.ContinueOnError)
	fs.IntVar(&cfg.Port, "port", cfg.Port, "局域网控制 HTTP 端口")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "数据目录（存放设备表与 admin secret）")
	if err := fs.Parse(args); err != nil {
		return cfg, nil, err
	}
	return cfg, fs.Args(), nil
}

// DevicesFile 返回设备表文件路径。
func (c Config) DevicesFile() string { return filepath.Join(c.DataDir, DevicesFileName) }

// AdminFile 返回管理通道信息文件路径（地址 + secret）。
func (c Config) AdminFile() string { return filepath.Join(c.DataDir, AdminFileName) }

// EnsureDataDir 确保数据目录存在（权限 0700）。
func (c Config) EnsureDataDir() error { return os.MkdirAll(c.DataDir, 0o700) }
