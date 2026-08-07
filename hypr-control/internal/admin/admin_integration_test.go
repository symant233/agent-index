package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"hypr-control/internal/config"
	"hypr-control/internal/devices"
)

// TestAdminAPIFlow 验证管理 API 全链路：status / list / allow / deny / revoke。
func TestAdminAPIFlow(t *testing.T) {
	dir := t.TempDir()
	store, err := devices.Open(filepath.Join(dir, config.DevicesFileName))
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(store, "test-secret")
	srv.port = config.DefaultPort
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// 模拟服务启动时写入的 admin.json。
	if err := writeAdminInfo(filepath.Join(dir, config.AdminFileName), srv.Addr(), "test-secret", 12345, config.DefaultPort); err != nil {
		t.Fatal(err)
	}
	cl, err := NewClient(dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 模拟网页登记两台设备。
	d1, err := store.Register("dev-1", "手机", "192.168.1.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	d2, err := store.Register("dev-2", "平板", "192.168.1.9", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	_ = d2 // dev-2 状态后续通过 list 校验

	// status
	st, err := cl.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "running" || st.ListenAddr != fmt.Sprintf("0.0.0.0:%d", config.DefaultPort) || st.Pending != 2 || st.Authorized != 0 {
		t.Fatalf("status 异常: %+v", st)
	}

	// list 能看到 pending 设备与 PIN
	list, err := cl.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("设备数 = %d, want 2", len(list))
	}
	if list[0].PIN == "" || list[0].Status != devices.StatusPending {
		t.Fatalf("list 未展示 pending 设备 PIN: %+v", list[0])
	}

	// 错误 PIN 拒绝
	if err := cl.Allow(ctx, "000000"); err == nil {
		t.Fatal("错误 PIN 应失败")
	}

	// 正确 PIN 授权 dev-1
	if err := cl.Allow(ctx, d1.PIN); err != nil {
		t.Fatal(err)
	}
	list, _ = cl.ListDevices(ctx)
	var dev1, dev2 *Device
	for i := range list {
		switch list[i].ID {
		case "dev-1":
			dev1 = &list[i]
		case "dev-2":
			dev2 = &list[i]
		}
	}
	if dev1 == nil || dev1.Status != devices.StatusAuthorized || dev1.Token == "" {
		t.Fatalf("allow 后 dev-1 状态错误: %+v", dev1)
	}
	if dev2 == nil || dev2.Status != devices.StatusPending {
		t.Fatalf("dev-2 应仍为 pending: %+v", dev2)
	}

	// 授权后令牌可查询
	if _, ok := store.LookupByToken(dev1.Token); !ok {
		t.Fatal("allow 后 token 应可查询")
	}

	// deny dev-2（按 PIN）
	if err := cl.Deny(ctx, dev2.PIN); err != nil {
		t.Fatal(err)
	}
	list, _ = cl.ListDevices(ctx)
	for _, d := range list {
		if d.ID == "dev-2" && d.Status != devices.StatusDenied {
			t.Fatalf("deny 后 dev-2 状态 = %q, want denied", d.Status)
		}
	}

	// revoke dev-1：token 失效
	if err := cl.Revoke(ctx, "dev-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.LookupByToken(dev1.Token); ok {
		t.Fatal("revoke 后 token 应失效")
	}
}

// TestAdminSecretRequired 验证管理接口强制校验 secret。
func TestAdminSecretRequired(t *testing.T) {
	store, _ := devices.Open(filepath.Join(t.TempDir(), config.DevicesFileName))
	srv := NewServer(store, "test-secret")
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/api/admin/status", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 secret 应 401, got %d", resp.StatusCode)
	}
	var e struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil || e.Error == "" {
		t.Fatalf("错误响应应含 error 字段: %v", err)
	}
}
