package devices

import (
	"path/filepath"
	"testing"

	"hypr-control/internal/config"
)

func TestRegisterAndAuthorize(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), config.DevicesFileName))
	if err != nil {
		t.Fatal(err)
	}

	d, err := s.Register("dev-1", "手机", "192.168.1.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != StatusPending {
		t.Fatalf("新设备状态 = %q, want pending", d.Status)
	}
	if len(d.PIN) != 6 {
		t.Fatalf("PIN = %q, 应为 6 位", d.PIN)
	}

	// 同 ID 重复登记：保持 pending 与原 PIN（不重复生成）
	d2, err := s.Register("dev-1", "手机", "192.168.1.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if d2.PIN != d.PIN {
		t.Fatalf("重复登记 PIN 变化: %q → %q", d.PIN, d2.PIN)
	}

	// 错误 PIN
	if _, err := s.AuthorizeByPIN("000000"); err != ErrPINMismatch {
		t.Fatalf("错误 PIN 应返回 ErrPINMismatch, got %v", err)
	}

	// 正确 PIN 授权
	auth, err := s.AuthorizeByPIN(d.PIN)
	if err != nil {
		t.Fatal(err)
	}
	if auth.Status != StatusAuthorized || auth.Token == "" {
		t.Fatalf("授权后状态 = %q, token 空? %v", auth.Status, auth.Token == "")
	}
	if auth.PIN != "" {
		t.Fatalf("授权后 PIN 应清空, got %q", auth.PIN)
	}

	// 令牌查询
	got, ok := s.LookupByToken(auth.Token)
	if !ok || got.ID != "dev-1" {
		t.Fatalf("按令牌查询失败: ok=%v id=%q", ok, got.ID)
	}

	// 已授权设备重复登记：返回授权状态与 token
	d3, err := s.Register("dev-1", "手机", "192.168.1.6", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if !d3.Authorized() || d3.Token != auth.Token {
		t.Fatalf("已授权设备重复登记应保持授权与同一 token")
	}

	// 吊销后 token 失效
	if err := s.Revoke("dev-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.LookupByToken(auth.Token); ok {
		t.Fatal("吊销后 token 应失效")
	}
	// 吊销后再登记：回到 pending 且新 PIN
	d4, err := s.Register("dev-1", "手机", "192.168.1.7", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if d4.Status != StatusPending || d4.PIN == "" {
		t.Fatalf("吊销后登记应回到 pending 并生成新 PIN, got %+v", d4)
	}
}

func TestDeny(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), config.DevicesFileName))

	d, _ := s.Register("dev-2", "平板", "192.168.1.9", "UA")
	if err := s.Deny(d.PIN); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0]; got.Status != StatusDenied {
		t.Fatalf("deny 后状态 = %q, want denied", got.Status)
	}

	// 非 pending 设备再 deny 应报错
	if err := s.Deny("dev-2"); err != ErrNotPending {
		t.Fatalf("已拒绝设备再 deny 应返回 ErrNotPending, got %v", err)
	}
	if err := s.Deny("不存在的设备"); err != ErrNotFound {
		t.Fatalf("未知设备 deny 应返回 ErrNotFound, got %v", err)
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), config.DevicesFileName)

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := s1.Register("dev-9", "电脑B", "10.0.0.2", "UA")
	if _, err := s1.AuthorizeByPIN(d.PIN); err != nil {
		t.Fatal(err)
	}

	// 重新打开应恢复状态
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	list := s2.List()
	if len(list) != 1 || list[0].Status != StatusAuthorized || list[0].Token == "" {
		t.Fatalf("持久化重载后状态错误: %+v", list)
	}
	if _, ok := s2.LookupByToken(list[0].Token); !ok {
		t.Fatal("重载后令牌应仍可查询")
	}
}

func TestCount(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), config.DevicesFileName))
	d1, _ := s.Register("a", "A", "1.1.1.1", "ua")
	s.Register("b", "B", "1.1.1.2", "ua")
	s.AuthorizeByPIN(d1.PIN)
	p, a := s.Count()
	if p != 1 || a != 1 {
		t.Fatalf("Count = (pending=%d, authorized=%d), want (1, 1)", p, a)
	}
}
