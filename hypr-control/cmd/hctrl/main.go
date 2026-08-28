// Command hctrl 是 hypr-control 的单二进制入口：
// server 子命令以常驻进程形式提供局域网遥控服务，
// 其余子命令通过本机管理通道执行状态控制与设备认证。
package main

import (
	"fmt"
	"os"
)

const usage = `hctrl —— 局域网主机遥控器（服务端 + 管理 CLI）

用法:
  hctrl serve [--port N] [--data-dir DIR] [--daemon]   启动常驻控制服务
        （--daemon 后台运行并释放当前终端，日志写入数据目录 hctrl.log）
  hctrl status                                查询服务运行状态
  hctrl restart [--port N] [--data-dir DIR]   重启服务
  hctrl kill                                  强制停止服务
  hctrl devices list                          列出已登记与待授权设备
  hctrl devices allow <PIN>                   允许一个待授权设备（输入网页显示的 6 位 PIN）
  hctrl devices deny <ID|PIN>                 拒绝一个待授权设备
  hctrl devices revoke <ID>                   吊销一个已授权设备
  hctrl plugins list                          列出全部插件及状态（初始均为禁用）
  hctrl plugins enable <名称>                 启用插件
  hctrl plugins disable <名称>                禁用插件
  hctrl autostart enable|disable|status       注册/移除/查看开机自启动
  hctrl -h | --help                           显示本帮助

内置插件:
  shutdown-volume   系统关机/重启事件后、蓝牙音箱断开前，自动把主音量
                    降到 20%，避免音箱以大音量播放断连提示音。

示例:
  hctrl serve --daemon
  hctrl devices list
  hctrl devices allow 483920
  hctrl plugins enable shutdown-volume
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	case "serve", "server": // server 为兼容旧命令的隐藏别名
		err = cmdServe(args[1:])
	case "status":
		err = cmdStatus(args[1:])
	case "restart":
		err = cmdRestart(args[1:])
	case "kill":
		err = cmdKill(args[1:])
	case "devices":
		err = cmdDevices(args[1:])
	case "plugins":
		err = cmdPlugins(args[1:])
	case "autostart":
		err = cmdAutostart(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "未知子命令: %q\n\n%s", args[0], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "hctrl:", err)
		os.Exit(1)
	}
}
