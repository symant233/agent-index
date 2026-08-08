package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"hypr-control/internal/admin"
	"hypr-control/internal/autostart"
	"hypr-control/internal/config"
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
//   hctrl serve                    前台运行（阻塞）
//   hctrl serve --daemon           后台运行（释放当前终端）
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
	fmt.Printf("状态: %s\n监听: %s\n管理通道: %s\n运行时长: %s\n",
		st.State, st.ListenAddr, st.AdminAddr, st.Uptime.Round(time.Second))
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
