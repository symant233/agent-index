package plugins

// 内置插件注册表：新增插件在此登记一行即可接入。
//
// 结构约定：每个插件一个文件（plugin_<名称>.go），文件内实现 Plugin 接口
// 并提供 New<类型>() 构造函数；本文件只负责"有哪些插件"的集中登记，
// 机制代码（接口/分发/持久化）见 plugins.go 与 store.go，互不掺和。
//
// 新增插件三步：
//  1. 新建 plugin_<名称>.go，实现 plugins.Plugin 接口；
//  2. 在 NewBuiltins() 中追加构造调用；
//  3. go test ./internal/plugins/ —— 通过即可，CLI list 自动可见。
//
// 注意：插件名即 CLI 启停用的键，一旦发布就不要再改
// （plugins.json 按名称恢复启用状态）。

import "fmt"

// NewBuiltins 返回全部内置插件实例。
// 顺序即 `hctrl plugins list` 的显示顺序。
func NewBuiltins() []Plugin {
	return []Plugin{
		NewShutdownVolume(),
	}
}

// RegisterBuiltins 把全部内置插件注册到 mgr。
// 内置插件均为编译期代码，重名属于编程错误，遇到即返回错误终止启动。
func RegisterBuiltins(mgr *Manager) error {
	for _, p := range NewBuiltins() {
		if err := mgr.Register(p); err != nil {
			return fmt.Errorf("注册内置插件 %s 失败: %w", p.Name(), err)
		}
	}
	return nil
}
