package plugins

import (
	"encoding/json"
	"fmt"
	"os"
)

// FileStore 把插件启用状态持久化为数据目录下的 JSON 文件。
// 文件格式：{"enabled": ["shutdown-volume", ...]}
type FileStore struct {
	path string
}

// NewFileStore 构造指向 path 的文件存储。
func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

// state 是文件的 JSON 结构。
type state struct {
	Enabled []string `json:"enabled"`
}

// Load 读取启用集合；文件不存在时返回空集（所有插件保持初始禁用）。
func (s *FileStore) Load() (map[string]bool, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("读取插件状态失败: %w", err)
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("解析插件状态失败: %w", err)
	}
	out := make(map[string]bool, len(st.Enabled))
	for _, name := range st.Enabled {
		out[name] = true
	}
	return out, nil
}

// Save 原子写入启用集合（先写临时文件再 rename）。
func (s *FileStore) Save(enabled map[string]bool) error {
	st := state{Enabled: make([]string, 0, len(enabled))}
	for name, on := range enabled {
		if on {
			st.Enabled = append(st.Enabled, name)
		}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("写入插件状态失败: %w", err)
	}
	return os.Rename(tmp, s.path)
}
