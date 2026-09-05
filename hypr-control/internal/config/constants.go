package config

// 固定值集中定义：需要调整时只改这里，不要在各处写死。
const (
	// Version 是程序版本号（语义化版本）。每次发版递增：
	// 破坏性/行为变更升次版本（1.1.0），修复与微调升修订号（1.0.1）。
	// 通过 /api/pair 下发给网页顶栏展示，hctrl status 也会显示。
	Version = "1.1.1"

	// DefaultPort 是局域网控制 HTTP 默认端口。
	// 注意：不要使用 6000-6009（X11），浏览器会以 ERR_UNSAFE_PORT 拒绝访问。
	DefaultPort = 8080

	// AdminHost 是管理通道监听主机（仅本机，不暴露到局域网）。
	AdminHost = "127.0.0.1"

	// DataDirName 是数据目录名（位于 %LOCALAPPDATA% 下）。
	DataDirName = "hypr-control"

	// AdminFileName 是管理通道信息文件名（地址 + secret）。
	AdminFileName = "admin.json"

	// DevicesFileName 是设备表文件名。
	DevicesFileName = "devices.json"

	// PluginsFileName 是插件启用状态文件名。
	PluginsFileName = "plugins.json"
)
