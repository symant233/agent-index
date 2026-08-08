# hypr-control

局域网主机遥控器：一个常驻的 Go 服务端 + 管理 CLI（单二进制 `hctrl`）。
像电视遥控器一样，对运行它的 Windows 主机执行锁屏、模拟键盘按键（单个/组合）、
鼠标移动与点击、系统音量、媒体播放控制等操作。

- **网页控制（P0）**：局域网设备访问 `https://<主机IP>:8080` 打开遥控器网页（HTTPS 加密，首次访问需在主机上经 CLI 授权）。
- **传输安全**：控制通道使用 HTTPS（自签名证书）+ 时间戳/nonce 防重放。
- **REST 控制（P2）**：暂不提供对第三方的公开 REST 通道；控制 API 仅供网页内部使用。
- **仅支持 Windows**：通过 `user32.dll`（`SendInput` / `SetCursorPos` / `LockWorkStation` / `WM_APPCOMMAND`）实现，零外部依赖、无 CGo。

## 目录结构（分层）

```
hypr-control/
├── cmd/hctrl/            # 入口：server 子命令 + 管理 CLI 子命令
├── internal/
│   ├── config/           # 配置（端口 8080、数据目录 %LOCALAPPDATA%\hypr-control）
│   ├── win32/            # user32.dll syscall 封装（键盘/鼠标/锁屏/音量/媒体）
│   ├── control/          # Backend 接口 + 真实 Windows 实现（测试可替换 mock）
│   ├── devices/          # 设备表：JSON 持久化、PIN 配对、token 签发
│   ├── admin/            # 本机管理通道（127.0.0.1 随机端口 + secret）+ CLI 客户端
│   └── server/           # 局域网控制 HTTP 服务 + go:embed 前端
│       └── web/          # 前端（纯 HTML/CSS/JS，无构建）
│           ├── index.html
│           ├── css/style.css
│           └── js/       # api.js / pair.js / mousepad.js / remote.js / app.js
└── build.ps1             # 构建脚本
```

## 构建

```powershell
./build.ps1               # go build + go vet + go test
# 或手动：
go build -o hctrl.exe ./cmd/hctrl
```

## 使用

### 1. 启动服务

```powershell
hctrl serve                      # 前台运行（阻塞当前终端）
hctrl serve --daemon             # 后台运行，立即释放终端（日志：数据目录\hctrl.log）
hctrl serve --port 7000          # 指定端口
hctrl serve --data-dir D:\data   # 指定数据目录
```

> 旧命令 `hctrl server` 仍可用（隐藏别名）。

### 2. 网页配对（首次访问）

1. 在手机/其他设备浏览器打开 **`https://<主机IP>:8080`**（控制端口，默认 8080；本机可用 `https://localhost:8080`）。
   > 首次访问会提示自签名证书不受信任，选择“高级 → 继续访问”即可（证书 10 年有效，只需一次）。
   > 注意：`hctrl status` 显示的"管理通道"（如 `127.0.0.1:16687`）是本机 CLI 专用端口，**不是网页**，其他设备无法访问。
2. 页面显示本设备的 **6 位 PIN**。
3. 在主机上执行授权：

```powershell
hctrl devices list                # 查看待授权设备与 PIN
hctrl devices allow 123456        # 输入网页显示的 PIN 允许该设备
```

4. 网页自动进入遥控器界面（token 保存在浏览器 `localStorage`）。

> 拒绝：`hctrl devices deny <ID|PIN>`；吊销：`hctrl devices revoke <ID>`。

### 3. 服务管理

```powershell
hctrl status                      # 查询运行状态
hctrl restart                     # 重启（服务端自动拉起新进程）
hctrl kill                        # 优雅停止
```

> 全局参数（`--port` / `--data-dir`）须位于子命令词之前，例如
> `hctrl devices --data-dir D:\data list`。

## CLI 参考

| 命令 | 说明 |
| --- | --- |
| `hctrl serve [--port N] [--data-dir DIR] [--daemon]` | 启动常驻控制服务（`--daemon` 后台运行） |
| `hctrl status` | 查询服务运行状态（监听地址、管理通道、时长、设备数） |
| `hctrl restart` | 重启服务 |
| `hctrl kill` | 优雅停止服务 |
| `hctrl devices list` | 列出全部设备（含待授权 PIN） |
| `hctrl devices allow <PIN>` | 按 PIN 授权设备 |
| `hctrl devices deny <ID\|PIN>` | 拒绝待授权设备 |
| `hctrl devices revoke <ID>` | 吊销已授权设备 |
| `hctrl autostart enable\|disable\|status` | 注册/移除/查看开机自启动（HKCU Run） |

## 控制 API（网页内部使用）

| 端点 | 请求体 | 说明 |
| --- | --- | --- |
| `POST /api/pair` | `{"device_id","name"}` | 登记/查询设备（pending→PIN，authorized→token） |
| `POST /api/control/key` | `{"key":"enter"}` | 单键（a-z、0-9、f1-f24、方向键、enter/esc/space/win…） |
| `POST /api/control/keys` | `{"keys":["ctrl","c"]}` | 组合键（≤8 个） |
| `POST /api/control/mouse` | `{"action":"move","dx","dy"}` 等 | 相对移动 / `move_to`(x,y) / `click`(left\|right\|middle) / `scroll`(±120) |
| `POST /api/control/volume` | `{"action":"up\|down\|mute"}` | 系统音量 |
| `POST /api/control/media` | `{"action":"playpause\|next\|prev\|stop"}` | 媒体控制 |
| `POST /api/control/lock` | `{}` | 锁屏 |
| `POST /api/control/power` | `{"action":"shutdown\|restart"}` | 延时 10 秒关机/重启（可 `shutdown /a` 取消） |

控制请求需携带已授权设备的令牌：`Authorization: Bearer <token>`（token 在设备授权后由 `/api/pair` 返回）。

所有控制请求还必须携带防重放头（HTTPS 之外的额外防护，防止抓包重放）：
- `X-Hypr-Timestamp`：Unix 秒级时间戳，与服务器时间差超过 120 秒即拒绝
- `X-Hypr-Nonce`：一次性随机标识（≥16 字符），重复使用即拒绝

## 安全说明

- **传输加密**：控制通道为 HTTPS（自签名证书，首次生成后存于数据目录 `cert.pem`/`key.pem`，有效期 10 年）。
- **防重放**：控制 API 校验 `X-Hypr-Timestamp`（120 秒窗口）+ `X-Hypr-Nonce`（一次性去重），抓包重放会被拒绝。
- **首访拦截**：未授权设备只能访问配对页与 `/api/pair`，无法调用任何控制接口。
- **管理通道隔离**：CLI 经 `127.0.0.1` 随机端口 + 随机 secret（`%LOCALAPPDATA%\hypr-control\admin.json`）通信，不暴露到局域网。
- **`Ctrl+Alt+Del` 无法模拟**：它是 Windows 安全注意序列，`SendInput` 不允许注入。
- **令牌吊销**：`hctrl devices revoke` 后该设备 token 立即失效。
- **同机用户**：同机其他用户进程可读取 `admin.json`（与"同机用户本就有完全控制权"的风险级别一致）。
- 建议在可信局域网使用；如需公网暴露请另行加固（如 VPN / 反向代理 + TLS）。

## 已知限制

- **防火墙/安全软件**：首次启动监听 `0.0.0.0:8080` 时，Windows 防火墙或第三方安全软件可能弹出授权提示，需手动放行；在授权前端口绑定可能挂起数十秒（服务日志会提示）。
- **不要使用 6000-6009 端口**：这些是 X11 端口，Chrome/Edge/Firefox 会以 `ERR_UNSAFE_PORT` 拒绝访问（`ERR_UNSAFE_PORT` 是浏览器本地拦截，与服务无关，请求不会发出）。
- 端口在代理/TUN 软件环境下用 `tcp4` 监听以避免 IPv6 双栈探测挂起（已处理）。
- 重启时若端口释放较慢，服务会自动重试监听至多 30 秒。
