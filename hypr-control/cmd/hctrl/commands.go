package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"hypr-control/internal/admin"
	"hypr-control/internal/autostart"
	"hypr-control/internal/config"
	"hypr-control/internal/plugins"
)

// cfgFor 解析给定参数得到配置，并确保数据目录存在。
func cfgFor(args []string) (config.Config, error) {
	cfg, err := config.ParseFlags(args)
	if err != nil {
		return cfg, err
	}
	if err := cfg.EnsureDataDir(); err != nil {
		return cfg, fmt.Errorf("创建数据目录失败: %w", err)
	}
	return cfg, nil
}

// clientFor 解析参数并连接本机管理通道。
func clientFor(args []string) (*admin.Client, error) {
	cfg, err := cfgFor(args)
	if err != nil {
		return nil, err
	}
	return admin.NewClient(cfg.DataDir)
}

// cmdServe 启动常驻控制服务。
//
//	hctrl serve                    前台运行（阻塞）
//	hctrl serve --daemon           后台运行（释放当前终端）
func cmdServe(args []string) error {
	cfg := config.Default()
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.IntVar(&cfg.Port, "port", cfg.Port, "局域网控制 HTTP 端口")
	fs.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "数据目录（存放设备表与 admin secret）")
	daemon := fs.Bool("daemon", false, "后台运行（释放当前终端，日志写入数据目录 hctrl.log）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := cfg.EnsureDataDir(); err != nil {
		return err
	}
	if *daemon {
		return startDaemon(cfg)
	}
	return admin.RunServer(cfg)
}

// startDaemon 以 detached 方式在后台拉起服务进程，等待管理通道就绪后返回。
func startDaemon(cfg config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(cfg.DataDir, "hctrl.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	cmd := exec.Command(exe, "serve",
		"--port", strconv.Itoa(cfg.Port),
		"--data-dir", cfg.DataDir)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200, // DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP
	}
	if err := cmd.Start(); err != nil {
		logf.Close()
		return fmt.Errorf("后台启动失败: %w", err)
	}
	pid := cmd.Process.Pid
	logf.Close()

	// 等待管理通道就绪（覆盖防火墙放行等待，最长 30 秒）。
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		cl, err := admin.NewClient(cfg.DataDir)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, sErr := cl.Status(ctx)
			cancel()
			if sErr == nil {
				fmt.Printf("hypr-control 已在后台运行（PID %d），日志: %s\n", pid, filepath.Join(cfg.DataDir, "hctrl.log"))
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Printf("hypr-control 进程已拉起（PID %d），但管理通道暂未就绪（可能正在等待防火墙放行端口 %d），日志: %s\n",
		pid, cfg.Port, filepath.Join(cfg.DataDir, "hctrl.log"))
	return nil
}

func cmdStatus(args []string) error {
	cl, err := clientFor(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	st, err := cl.Status(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("版本: v%s\n状态: %s\n监听: %s\n管理通道: %s\n运行时长: %s\n",
		config.Version, st.State, st.ListenAddr, st.AdminAddr, st.Uptime.Round(time.Second))
	if enabled, cmd, err := autostart.Status(); err == nil {
		if enabled {
			fmt.Printf("开机自启动: 已注册（%s）\n", cmd)
		} else {
			fmt.Println("开机自启动: 未注册")
		}
	}
	return nil
}

// cmdAutostart 管理开机自启动注册。
func cmdAutostart(args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: hctrl autostart enable|disable|status")
		os.Exit(2)
	}
	switch args[0] {
	case "enable":
		cfg, _, err := config.ParseFlagsWithArgs(args[1:])
		if err != nil {
			return err
		}
		if err := cfg.EnsureDataDir(); err != nil {
			return err
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if err := autostart.Enable(exe, cfg.DataDir); err != nil {
			return err
		}
		fmt.Printf("已注册开机自启动: \"%s\" serve --daemon --data-dir \"%s\"\n", exe, cfg.DataDir)
	case "disable":
		if err := autostart.Disable(); err != nil {
			return err
		}
		fmt.Println("已移除开机自启动")
	case "status":
		enabled, cmd, err := autostart.Status()
		if err != nil {
			return err
		}
		if enabled {
			fmt.Printf("开机自启动: 已注册\n命令: %s\n", cmd)
		} else {
			fmt.Println("开机自启动: 未注册")
		}
	default:
		fmt.Fprintf(os.Stderr, "未知自启动子命令: %q\n", args[0])
		os.Exit(2)
	}
	return nil
}

func cmdRestart(args []string) error {
	cl, err := clientFor(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := cl.Restart(ctx); err != nil {
		return err
	}
	fmt.Println("服务已重启")
	return nil
}

func cmdKill(args []string) error {
	cl, err := clientFor(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cl.Kill(ctx); err != nil {
		return err
	}
	fmt.Println("服务已停止")
	return nil
}

func cmdDevices(args []string) error {
	// 全局 flag（--data-dir / --port）须位于子命令词之前，例如：
	//   hctrl devices --data-dir DIR allow <PIN>
	cfg, rest, err := config.ParseFlagsWithArgs(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "用法: hctrl devices list|allow <PIN>|deny <ID|PIN>|revoke <ID>")
		os.Exit(2)
	}
	cl, err := admin.NewClient(cfg.DataDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "list":
		devs, err := cl.ListDevices(ctx)
		if err != nil {
			return err
		}
		printDevices(devs)
	case "allow":
		if len(subArgs) < 1 {
			return fmt.Errorf("用法: hctrl devices allow <PIN>")
		}
		if err := cl.Allow(ctx, subArgs[0]); err != nil {
			return err
		}
		fmt.Printf("设备 %s 已授权\n", subArgs[0])
	case "deny":
		if len(subArgs) < 1 {
			return fmt.Errorf("用法: hctrl devices deny <ID|PIN>")
		}
		if err := cl.Deny(ctx, subArgs[0]); err != nil {
			return err
		}
		fmt.Printf("设备 %s 已拒绝\n", subArgs[0])
	case "revoke":
		if len(subArgs) < 1 {
			return fmt.Errorf("用法: hctrl devices revoke <ID>")
		}
		if err := cl.Revoke(ctx, subArgs[0]); err != nil {
			return err
		}
		fmt.Printf("设备 %s 已吊销\n", subArgs[0])
	default:
		fmt.Fprintf(os.Stderr, "未知设备子命令: %q\n", sub)
		os.Exit(2)
	}
	return nil
}

func printDevices(devs []admin.Device) {
	if len(devs) == 0 {
		fmt.Println("（无设备）")
		return
	}
	for _, d := range devs {
		pin := ""
		if d.Status == "pending" {
			pin = fmt.Sprintf("  PIN: %s", d.PIN)
		}
		fmt.Printf("%-14s %-10s %-16s %-20s %s%s\n",
			d.ID, d.Status, d.IP, d.Name, d.UA, pin)
	}
}

// cmdPlugins 插件管理：查询/启用/禁用。
//
//	hctrl plugins list                列出全部插件与状态
//	hctrl plugins enable <名称>       启用插件（如 shutdown-volume）
//	hctrl plugins disable <名称>      禁用插件
//
// 全局 flag（--data-dir / --port）须位于子命令词之前。
func cmdPlugins(args []string) error {
	cfg, rest, err := config.ParseFlagsWithArgs(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "用法: hctrl plugins list|enable <名称>|disable <名称>")
		os.Exit(2)
	}
	cl, err := admin.NewClient(cfg.DataDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "list", "ls":
		list, err := cl.ListPlugins(ctx)
		if err != nil {
			return err
		}
		printPlugins(list)
	case "enable", "on":
		if len(subArgs) < 1 {
			return fmt.Errorf("用法: hctrl plugins enable <名称>（hctrl plugins list 可查名称）")
		}
		if subArgs[0] == plugins.NameSocks5 {
			return enableSocks5(cfg, cl)
		}
		if err := cl.EnablePlugin(ctx, subArgs[0]); err != nil {
			return err
		}
		fmt.Printf("插件 %s 已启用\n", subArgs[0])
	case "disable", "off":
		if len(subArgs) < 1 {
			return fmt.Errorf("用法: hctrl plugins disable <名称>（hctrl plugins list 可查名称）")
		}
		if err := cl.DisablePlugin(ctx, subArgs[0]); err != nil {
			return err
		}
		fmt.Printf("插件 %s 已禁用\n", subArgs[0])
	default:
		fmt.Fprintf(os.Stderr, "未知插件子命令: %q\n", sub)
		os.Exit(2)
	}
	return nil
}

func printPlugins(list []admin.Plugin) {
	if len(list) == 0 {
		fmt.Println("（无插件）")
		return
	}
	for _, p := range list {
		state := "禁用"
		if p.Enabled {
			state = "启用"
		}
		hooks := strings.Join(p.Hooks, ",")
		if hooks == "" {
			hooks = "（无钩子，随启用/禁用启停）"
		}
		fmt.Printf("%-18s [%s] 钩子: %s\n    %s\n", p.Name, state, hooks, p.Desc)
	}
}

// enableSocks5 交互式配置并启用 socks5 代理插件：依次输入用户名、
// 密码（掩码）、监听端口（默认 6666），成功后打印实际监听端口。
// 已配置过时：用户名/密码直接回车保留原值，端口默认上次实际端口。
func enableSocks5(cfg config.Config, cl *admin.Client) error {
	// 先探测守护进程：避免用户输完凭据才报"服务未运行"。
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer pingCancel()
	if _, err := cl.Status(pingCtx); err != nil {
		return fmt.Errorf("连接管理通道失败（服务未运行？先执行 hctrl serve --daemon）: %w", err)
	}

	old, err := plugins.LoadSocks5Config(cfg.Socks5PluginFile())
	if err != nil {
		old = plugins.Socks5Config{} // 配置损坏视为未配置，走全新输入
	}

	fmt.Println("配置 SOCKS5 代理插件（凭据保存于本机数据目录，供局域网设备鉴权）")
	username, err := promptSocks5Username(old.Username)
	if err != nil {
		return err
	}
	password, err := promptSocks5Password(old.Password)
	if err != nil {
		return err
	}
	port, err := promptSocks5Port(old.Port)
	if err != nil {
		return err
	}

	setupCtx, setupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer setupCancel()
	st, err := cl.SetupSocks5(setupCtx, username, password, port)
	if err != nil {
		return err
	}

	host := "本机IP"
	if ip := lanIPv4(); ip != "" {
		host = ip
	}
	fmt.Printf("\nSOCKS5 代理已启动：%s:%d（用户名 %s）\n", host, st.Port, username)
	if st.Port != st.Requested {
		fmt.Printf("（请求端口 %d 被占用，已自动顺延）\n", st.Requested)
	}
	fmt.Println("把局域网设备的代理设置指向上述地址即可经本机访问内网。")
	return nil
}

// promptSocks5Username 提示输入用户名（非空；已有配置时回车保留）。
func promptSocks5Username(def string) (string, error) {
	for i := 0; i < 3; i++ {
		hint := ""
		if def != "" {
			hint = fmt.Sprintf("（回车保留 %s）", def)
		}
		s, err := readLine(fmt.Sprintf("用户名%s: ", hint))
		if err != nil {
			return "", fmt.Errorf("读取用户名失败: %w", err)
		}
		switch {
		case s == "" && def != "":
			return def, nil
		case s != "":
			return s, nil
		}
		fmt.Println("用户名不能为空")
	}
	return "", errors.New("连续三次未输入有效用户名")
}

// promptSocks5Password 提示输入密码（掩码回显；已有配置时回车保留原密码）。
func promptSocks5Password(def string) (string, error) {
	hint := ""
	if def != "" {
		hint = "（回车保留原密码）"
	}
	for i := 0; i < 3; i++ {
		s, err := readPassword(fmt.Sprintf("密码%s: ", hint))
		if err != nil {
			return "", fmt.Errorf("读取密码失败: %w", err)
		}
		switch {
		case s == "" && def != "":
			return def, nil
		case s != "":
			return s, nil
		}
		fmt.Println("密码不能为空")
	}
	return "", errors.New("连续三次未输入有效密码")
}

// promptSocks5Port 提示输入监听端口，回车用默认（上次实际端口或 6666）。
// 被占用时由服务端自动顺延，实际端口在启用结果中打印。
func promptSocks5Port(def int) (int, error) {
	if def == 0 {
		def = config.Socks5PortDefault
	}
	for i := 0; i < 3; i++ {
		s, err := readLine(fmt.Sprintf("监听端口 [%d]: ", def))
		if err != nil {
			return 0, fmt.Errorf("读取端口失败: %w", err)
		}
		if s == "" {
			return def, nil
		}
		n, perr := strconv.Atoi(s)
		if perr != nil || n < 1 || n > 65535 {
			fmt.Println("端口需为 1-65535 的整数")
			continue
		}
		return n, nil
	}
	return 0, errors.New("连续三次未输入有效端口")
}

// lanIPv4 返回本机一个局域网 IPv4 地址（非回环；私有网段优先），
// 无则空串（启用结果退化为提示"本机IP"）。
func lanIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	fallback := ""
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		if ip4.IsPrivate() {
			return ip4.String()
		}
		if fallback == "" {
			fallback = ip4.String()
		}
	}
	return fallback
}
