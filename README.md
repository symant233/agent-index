# agent-index

本仓库用于存放 AI 生成的实用工具。每个工具位于独立的子目录中，自带源码、文档与构建脚本，可独立使用。

## 工具列表

### hypr-control — 局域网主机遥控器

用手机/平板浏览器把 Windows 主机当电视遥控器操控：锁屏、模拟键盘按键（单个/组合）、鼠标移动与点击、音量与媒体控制。

- **目录**：`hypr-control/`
- **技术栈**：Go 单二进制（`hctrl`），`user32.dll`/`winmm.dll` 系统调用，无外部依赖
- **快速开始**：

  ```powershell
  cd hypr-control
  go build -o hctrl.exe ./cmd/hctrl
  .\hctrl.exe serve --daemon     # 后台启动，默认端口 8080
  ```

  浏览器访问 `http://<主机IP>:8080`，网页显示 6 位 PIN，在主机上执行 `hctrl devices allow <PIN>` 授权后即可控制。

- **详细文档**：见 [hypr-control/README.md](hypr-control/README.md)

## 目录结构

```
agent-index/
├── hypr-control/    # 局域网主机遥控器（服务端 + CLI）
└── README.md        # 本文件
```
