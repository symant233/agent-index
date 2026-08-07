// Package devices 管理请求访问本机的设备：登记、PIN 配对、授权/拒绝/吊销，
// 并以 JSON 文件持久化到数据目录。
package devices

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// 设备状态。
const (
	StatusPending    = "pending"
	StatusAuthorized = "authorized"
	StatusDenied     = "denied"
)

// PINLength 是配对 PIN 的位数。
const PINLength = 6

// pinMax = 10^PINLength，用于生成指定位数的随机 PIN。
const pinMax = 1000000

// 常见错误。
var (
	ErrNotFound    = errors.New("设备不存在")
	ErrPINMismatch = errors.New("PIN 不匹配（请核对网页上显示的 6 位 PIN）")
	ErrNotPending  = errors.New("该设备当前不是待授权状态")
	ErrNoToken     = errors.New("该设备无有效授权令牌")
)

// Device 描述一台访问设备。
type Device struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	IP           string     `json:"ip"`
	UA           string     `json:"ua"`
	Status       string     `json:"status"` // pending / authorized / denied
	PIN          string     `json:"pin,omitempty"`
	Token        string     `json:"token,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	AuthorizedAt *time.Time `json:"authorized_at,omitempty"`
}

// Authorized 报告设备是否已授权。
func (d Device) Authorized() bool { return d.Status == StatusAuthorized }

// Store 是并发安全的设备表，按 ID 与 Token 索引。
type Store struct {
	mu      sync.Mutex
	path    string
	byID    map[string]*Device
	byToken map[string]*Device
}

// Open 从 path 加载设备表；文件不存在时返回空表。
func Open(path string) (*Store, error) {
	s := &Store{
		path:    path,
		byID:    make(map[string]*Device),
		byToken: make(map[string]*Device),
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("读取设备表失败: %w", err)
	}
	var list []Device
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("解析设备表失败: %w", err)
	}
	for i := range list {
		d := &list[i]
		s.byID[d.ID] = d
		if d.Token != "" {
			s.byToken[d.Token] = d
		}
	}
	return s, nil
}

// Register 登记一台设备。客户端携带稳定设备 ID（浏览器生成并存 localStorage）：
//   - 已授权：原样返回（含 token），供直接控制
//   - 待授权：返回原 PIN，不重复生成
//   - 已拒绝或新设备：进入 pending 并生成新 PIN
func (s *Store) Register(id, name, ip, ua string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if d, ok := s.byID[id]; ok {
		d.IP, d.UA, d.Name = ip, ua, name
		switch d.Status {
		case StatusAuthorized:
			if d.Token == "" {
				d.Token = NewToken()
			}
		case StatusDenied:
			d.Status = StatusPending
			d.PIN = s.newPINLocked()
		}
		s.saveLocked()
		return *d, nil
	}

	d := &Device{
		ID:        id,
		Name:      name,
		IP:        ip,
		UA:        ua,
		Status:    StatusPending,
		PIN:       s.newPINLocked(),
		CreatedAt: time.Now(),
	}
	s.byID[d.ID] = d
	s.saveLocked()
	return *d, nil
}

// AuthorizeByPIN 按 6 位 PIN 允许设备：pending → authorized，并签发令牌。
func (s *Store) AuthorizeByPIN(pin string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, d := range s.byID {
		if d.Status == StatusPending && d.PIN != "" && d.PIN == pin {
			d.Status = StatusAuthorized
			d.Token = NewToken()
			now := time.Now()
			d.AuthorizedAt = &now
			d.PIN = ""
			s.byToken[d.Token] = d
			s.saveLocked()
			return *d, nil
		}
	}
	return Device{}, ErrPINMismatch
}

// Deny 拒绝一台待授权设备（按 ID 或 PIN）：pending → denied。
func (s *Store) Deny(idOrPIN string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	d := s.byID[idOrPIN]
	if d == nil {
		for _, x := range s.byID {
			if x.Status == StatusPending && x.PIN == idOrPIN {
				d = x
				break
			}
		}
	}
	if d == nil {
		return ErrNotFound
	}
	if d.Status != StatusPending {
		return ErrNotPending
	}
	d.Status = StatusDenied
	d.PIN = ""
	s.saveLocked()
	return nil
}

// Revoke 吊销已授权设备：authorized → denied，令牌失效。
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	d := s.byID[id]
	if d == nil {
		return ErrNotFound
	}
	if d.Token != "" {
		delete(s.byToken, d.Token)
	}
	d.Token = ""
	d.Status = StatusDenied
	d.PIN = ""
	s.saveLocked()
	return nil
}

// LookupByToken 按令牌查找已授权设备。
func (s *Store) LookupByToken(token string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.byToken[token]
	if !ok || d.Status != StatusAuthorized {
		return Device{}, false
	}
	return *d, true
}

// List 返回全部设备的快照（按创建时间排序）。
func (s *Store) List() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.byID))
	for _, d := range s.byID {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Count 统计各状态设备数量。
func (s *Store) Count() (pending, authorized int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.byID {
		switch d.Status {
		case StatusPending:
			pending++
		case StatusAuthorized:
			authorized++
		}
	}
	return pending, authorized
}

// newPINLocked 生成不与现有 pending 冲突的 6 位 PIN（须持锁调用）。
func (s *Store) newPINLocked() string {
	for {
		pin := randomPIN()
		conflict := false
		for _, d := range s.byID {
			if d.Status == StatusPending && d.PIN == pin {
				conflict = true
				break
			}
		}
		if !conflict {
			return pin
		}
	}
}

// saveLocked 原子写设备表（临时文件 + 重命名，须持锁调用）。
func (s *Store) saveLocked() error {
	list := make([]Device, 0, len(s.byID))
	for _, d := range s.byID {
		list = append(list, *d)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func randomPIN() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(err) // 系统熵源不可用时无法继续
	}
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return fmt.Sprintf("%0*d", PINLength, n%pinMax)
}

// NewToken 生成 64 位十六进制随机令牌。
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
