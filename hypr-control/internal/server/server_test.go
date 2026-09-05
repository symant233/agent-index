package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"hypr-control/internal/config"
	"hypr-control/internal/control"
	"hypr-control/internal/devices"
)

// mockBackend 记录后端调用，便于断言参数传递。
type mockBackend struct {
	mu    sync.Mutex
	calls []string
}

// hookedDispatcher 记录插件分发调用（测试电源顺序用）。
type hookedDispatcher struct {
	mu    sync.Mutex
	calls []string
}

func (h *hookedDispatcher) Dispatch(hook string, _ func(string, ...any)) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, "hook:"+hook)
	return nil
}

// newHookedTestServer 构造带插件分发记录的测试服务。
func newHookedTestServer(t *testing.T) (*httptest.Server, *mockBackend, *hookedDispatcher, *devices.Store) {
	t.Helper()
	store, err := devices.Open(filepath.Join(t.TempDir(), config.DevicesFileName))
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockBackend{}
	hd := &hookedDispatcher{}
	c := &Control{store: store, backend: mock, plugins: hd}
	ts := httptest.NewServer(c.handler())
	t.Cleanup(ts.Close)
	return ts, mock, hd, store
}

func (m *mockBackend) record(s string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, s)
}

// knownKeys 模拟 win32 的按键名解析（真实实现见 internal/win32）。
var knownKeys = map[string]bool{
	"enter": true, "ctrl": true, "c": true, "win": true, "d": true, "shift": true,
}

func (m *mockBackend) KeyTap(name string) error {
	if !knownKeys[name] {
		return fmt.Errorf("未知按键: %q", name)
	}
	m.record("key:" + name)
	return nil
}
func (m *mockBackend) Chord(names []string) error {
	for _, n := range names {
		if !knownKeys[n] {
			return fmt.Errorf("未知按键: %q", n)
		}
	}
	m.record("keys:" + strings.Join(names, "+"))
	return nil
}
func (m *mockBackend) MouseMoveRel(dx, dy int32) error {
	m.record("move_rel")
	return nil
}
func (m *mockBackend) MouseMoveTo(x, y int) error     { m.record("move_to"); return nil }
func (m *mockBackend) MouseClick(button string) error { m.record("click:" + button); return nil }
func (m *mockBackend) MouseScroll(delta int32) error  { m.record("scroll"); return nil }
func (m *mockBackend) MouseHScroll(delta int32) error { m.record("hscroll"); return nil }
func (m *mockBackend) MouseDown(button string) error  { m.record("down:" + button); return nil }
func (m *mockBackend) MouseUp(button string) error    { m.record("up:" + button); return nil }
func (m *mockBackend) Lock() error                    { m.record("lock"); return nil }
func (m *mockBackend) PowerShutdown() error           { m.record("power:shutdown"); return nil }
func (m *mockBackend) PowerRestart() error            { m.record("power:restart"); return nil }
func (m *mockBackend) VolumeUp() error                { m.record("volume:up"); return nil }
func (m *mockBackend) VolumeDown() error              { m.record("volume:down"); return nil }
func (m *mockBackend) VolumeMute() error              { m.record("volume:mute"); return nil }
func (m *mockBackend) MediaPlayPause() error          { m.record("media:playpause"); return nil }
func (m *mockBackend) MediaNext() error               { m.record("media:next"); return nil }
func (m *mockBackend) MediaPrev() error               { m.record("media:prev"); return nil }
func (m *mockBackend) MediaStop() error               { m.record("media:stop"); return nil }

func (m *mockBackend) ClipboardGet() (string, error) {
	m.record("clipboard:get")
	return "剪贴板内容", nil
}
func (m *mockBackend) ClipboardSet(text string) error {
	m.record("clipboard:set")
	return nil
}
func (m *mockBackend) ListWindows() ([]control.WindowInfo, error) {
	m.record("windows:list")
	return []control.WindowInfo{
		{Handle: 111, Title: "前台窗口", Active: true},
		{Handle: 222, Title: "后台窗口", Active: false},
	}, nil
}
func (m *mockBackend) FocusWindow(handle uint64) error {
	m.record(fmt.Sprintf("windows:focus:%d", handle))
	return nil
}

// newTestServer 构造带 mock 后端与真实设备存储的测试服务。
func newTestServer(t *testing.T) (*httptest.Server, *mockBackend, *devices.Store, string) {
	t.Helper()
	store, err := devices.Open(filepath.Join(t.TempDir(), config.DevicesFileName))
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockBackend{}
	c := &Control{store: store, backend: mock}
	ts := httptest.NewServer(c.handler())
	t.Cleanup(ts.Close)
	return ts, mock, store, ""
}

// authorizeDevice 模拟 CLI 允许设备并返回其 token。
func authorizeDevice(t *testing.T, store *devices.Store, pin string) string {
	t.Helper()
	d, err := store.AuthorizeByPIN(pin)
	if err != nil {
		t.Fatal(err)
	}
	return d.Token
}

func doJSON(t *testing.T, method, url, token, body string) *http.Response {
	t.Helper()
	return doJSONHeaders(t, method, url, token, body, replayHeaders())
}

// doJSONHeaders 与 doJSON 相同，但允许覆盖/追加请求头（用于防重放测试）。
func doJSONHeaders(t *testing.T, method, url, token, body string, extra map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// replayHeaders 生成合法的时间戳+nonce 防重放头。
func replayHeaders() map[string]string {
	return map[string]string{
		"X-Hypr-Timestamp": strconv.FormatInt(time.Now().Unix(), 10),
		"X-Hypr-Nonce":     fmt.Sprintf("nonce-%d-%d", time.Now().UnixNano(), rand.Int63()),
	}
}

func TestPairFlow(t *testing.T) {
	ts, _, store, _ := newTestServer(t)

	// 新设备 → pending + PIN
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/pair", "", `{"device_id":"dev-1","name":"手机"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pair 状态 = %d", resp.StatusCode)
	}
	var out struct {
		Status string `json:"status"`
		Device struct {
			ID  string `json:"id"`
			PIN string `json:"pin"`
		} `json:"device"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Status != "pending" || len(out.Device.PIN) != 6 {
		t.Fatalf("pair 结果异常: %+v", out)
	}

	// CLI 允许
	token := authorizeDevice(t, store, out.Device.PIN)

	// 再次 pair → authorized + token
	resp2 := doJSON(t, http.MethodPost, ts.URL+"/api/pair", "", `{"device_id":"dev-1","name":"手机"}`)
	var out2 struct {
		Status string `json:"status"`
		Device struct {
			Token string `json:"token"`
		} `json:"device"`
	}
	json.NewDecoder(resp2.Body).Decode(&out2)
	resp2.Body.Close()
	if out2.Status != "authorized" || out2.Device.Token != token {
		t.Fatalf("授权后 pair 异常: %+v", out2)
	}

	// 缺少 device_id
	resp3 := doJSON(t, http.MethodPost, ts.URL+"/api/pair", "", `{}`)
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺少 device_id 应 400, got %d", resp3.StatusCode)
	}
	resp3.Body.Close()
}

func TestControlRequiresToken(t *testing.T) {
	ts, _, _, _ := newTestServer(t)

	// 无 token
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/control/key", "", `{"key":"enter"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 伪造 token
	resp2 := doJSON(t, http.MethodPost, ts.URL+"/api/control/key", "bogus-token", `{"key":"enter"}`)
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("伪造 token 应 401, got %d", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func TestControlEndpoints(t *testing.T) {
	ts, mock, store, _ := newTestServer(t)

	// 登记并授权设备
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/pair", "", `{"device_id":"dev-9","name":"平板"}`)
	var out struct {
		Device struct {
			PIN string `json:"pin"`
		} `json:"device"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	token := authorizeDevice(t, store, out.Device.PIN)

	cases := []struct {
		path string
		body string
		want string // mock 记录的调用
	}{
		{"/api/control/key", `{"key":"enter"}`, "key:enter"},
		{"/api/control/keys", `{"keys":["ctrl","c"]}`, "keys:ctrl+c"},
		{"/api/control/keys", `{"keys":["win","d"]}`, "keys:win+d"},
		{"/api/control/mouse", `{"action":"move","dx":10,"dy":-5}`, "move_rel"},
		{"/api/control/mouse", `{"action":"move_to","x":800,"y":600}`, "move_to"},
		{"/api/control/mouse", `{"action":"click","button":"right"}`, "click:right"},
		{"/api/control/mouse", `{"action":"down","button":"left"}`, "down:left"},
		{"/api/control/mouse", `{"action":"up","button":"left"}`, "up:left"},
		{"/api/control/mouse", `{"action":"scroll","delta":-120}`, "scroll"},
		{"/api/control/mouse", `{"action":"hscroll","delta":120}`, "hscroll"},
		{"/api/control/volume", `{"action":"up"}`, "volume:up"},
		{"/api/control/volume", `{"action":"mute"}`, "volume:mute"},
		{"/api/control/media", `{"action":"next"}`, "media:next"},
		{"/api/control/media", `{"action":"playpause"}`, "media:playpause"},
		{"/api/control/clipboard", `{"action":"set","text":"hello"}`, "clipboard:set"},
		{"/api/control/windows", `{"action":"focus","handle":123}`, "windows:focus:123"},
	}
	for _, tc := range cases {
		mock.mu.Lock()
		before := len(mock.calls)
		mock.mu.Unlock()
		r := doJSON(t, http.MethodPost, ts.URL+tc.path, token, tc.body)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("%s %s 状态 = %d, body=%s", tc.path, tc.body, r.StatusCode, readBody(r))
		}
		r.Body.Close()
		mock.mu.Lock()
		got := mock.calls[before:]
		mock.mu.Unlock()
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s %s: mock 调用 = %v, want [%s]", tc.path, tc.body, got, tc.want)
		}
	}

	// lock
	r := doJSON(t, http.MethodPost, ts.URL+"/api/control/lock", token, `{}`)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("lock 状态 = %d", r.StatusCode)
	}
	r.Body.Close()
	mock.mu.Lock()
	last := mock.calls[len(mock.calls)-1]
	mock.mu.Unlock()
	if last != "lock" {
		t.Fatalf("lock 调用 = %s", last)
	}

	// power（危险操作，需 X-Hypr-Confirm 确认头）
	for _, tc := range []struct{ action, want string }{
		{"shutdown", "power:shutdown"},
		{"restart", "power:restart"},
	} {
		hdr := replayHeaders()
		hdr["X-Hypr-Confirm"] = tc.action
		rp := doJSONHeaders(t, http.MethodPost, ts.URL+"/api/control/power", token,
			fmt.Sprintf(`{"action":%q}`, tc.action), hdr)
		if rp.StatusCode != http.StatusOK {
			t.Fatalf("power %s（带确认头）状态 = %d", tc.action, rp.StatusCode)
		}
		rp.Body.Close()
		mock.mu.Lock()
		last = mock.calls[len(mock.calls)-1]
		mock.mu.Unlock()
		if last != tc.want {
			t.Fatalf("power %s 调用 = %s, want %s", tc.action, last, tc.want)
		}
	}

	// 参数校验：未知按键 / 未知动作 / 空 keys
	bad := []struct {
		path string
		body string
	}{
		{"/api/control/key", `{"key":""}`},
		{"/api/control/key", `{"key":"nonsense"}`},
		{"/api/control/keys", `{"keys":[]}`},
		{"/api/control/mouse", `{"action":"bogus"}`},
		{"/api/control/volume", `{"action":"bogus"}`},
		{"/api/control/media", `{"action":"bogus"}`},
		{"/api/control/power", `{"action":"shutdown"}`}, // 无确认头
		{"/api/control/power", `{"action":"bogus"}`},
	}
	for _, tc := range bad {
		r := doJSON(t, http.MethodPost, ts.URL+tc.path, token, tc.body)
		if r.StatusCode != http.StatusBadRequest {
			t.Errorf("%s %s 应 400, got %d", tc.path, tc.body, r.StatusCode)
		}
		r.Body.Close()
	}
}

// TestReplayProtection 验证防重放：缺少/过期时间戳、重复 nonce 均被拒绝。
func TestReplayProtection(t *testing.T) {
	ts, mock, store, _ := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/pair", "", `{"device_id":"dev-rp","name":"x"}`)
	var out struct {
		Device struct {
			PIN string `json:"pin"`
		} `json:"device"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	token := authorizeDevice(t, store, out.Device.PIN)
	_ = mock

	// 1. 缺少防重放头 → 400
	r := doJSONHeaders(t, http.MethodPost, ts.URL+"/api/control/key", token, `{"key":"enter"}`, map[string]string{})
	if r.StatusCode != http.StatusBadRequest {
		t.Fatalf("缺少防重放头应 400, got %d", r.StatusCode)
	}
	r.Body.Close()

	// 2. 过期时间戳 → 401
	old := map[string]string{
		"X-Hypr-Timestamp": strconv.FormatInt(time.Now().Unix()-1000, 10),
		"X-Hypr-Nonce":     "stale-nonce-00000000000000",
	}
	r = doJSONHeaders(t, http.MethodPost, ts.URL+"/api/control/key", token, `{"key":"enter"}`, old)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期时间戳应 401, got %d", r.StatusCode)
	}
	r.Body.Close()

	// 3. 同一 nonce 复用 → 第二次拒绝
	hdr := replayHeaders()
	r1 := doJSONHeaders(t, http.MethodPost, ts.URL+"/api/control/key", token, `{"key":"enter"}`, hdr)
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("首次合法请求应 200, got %d", r1.StatusCode)
	}
	r1.Body.Close()
	r2 := doJSONHeaders(t, http.MethodPost, ts.URL+"/api/control/key", token, `{"key":"enter"}`, hdr)
	if r2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("重复 nonce 应 401, got %d", r2.StatusCode)
	}
	r2.Body.Close()

	// 4. 超长 nonce → 400（防内存 DoS）
	long := map[string]string{
		"X-Hypr-Timestamp": strconv.FormatInt(time.Now().Unix(), 10),
		"X-Hypr-Nonce":     strings.Repeat("a", 200),
	}
	r3 := doJSONHeaders(t, http.MethodPost, ts.URL+"/api/control/key", token, `{"key":"enter"}`, long)
	if r3.StatusCode != http.StatusBadRequest {
		t.Fatalf("超长 nonce 应 400, got %d", r3.StatusCode)
	}
	r3.Body.Close()
}

func TestStaticPage(t *testing.T) {
	ts, _, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("首页状态 = %d", resp.StatusCode)
	}
	if !strings.Contains(readBody(resp), "hypr-control") {
		t.Fatal("首页未包含 hypr-control 标识")
	}
}

// TestHTTPRedirect 验证明文 HTTP 请求被 302 重定向到 https。
func TestHTTPRedirect(t *testing.T) {
	store, err := devices.Open(filepath.Join(t.TempDir(), config.DevicesFileName))
	if err != nil {
		t.Fatal(err)
	}
	c := &Control{store: store, backend: &mockBackend{}}
	rts := httptest.NewServer(c.redirectHandler(c.handler()))
	defer rts.Close()

	// 禁止自动跟随重定向，以便断言 302 本身。
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(rts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Fatalf("明文请求应 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "https://") {
		t.Fatalf("Location 应为 https, got %q", loc)
	}
	if !strings.Contains(loc, "/") {
		t.Fatalf("Location 应包含路径, got %q", loc)
	}
}

func readBody(r *http.Response) string {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

// TestPowerDispatchesShutdownHookBeforePower 验证电源操作先分发插件
// shutdown 钩子、再执行后端关机/重启（保证插件动作赶在系统断开蓝牙前）。
func TestPowerDispatchesShutdownHookBeforePower(t *testing.T) {
	ts, mock, hd, store := newHookedTestServer(t)

	// 登记并授权设备
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/pair", "", `{"device_id":"dev-plug","name":"x"}`)
	var out struct {
		Device struct {
			PIN string `json:"pin"`
		} `json:"device"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	token := authorizeDevice(t, store, out.Device.PIN)

	for _, tc := range []struct{ action, wantPower string }{
		{"shutdown", "power:shutdown"},
		{"restart", "power:restart"},
	} {
		mock.mu.Lock()
		mock.calls = nil
		mock.mu.Unlock()
		hd.mu.Lock()
		hd.calls = nil
		hd.mu.Unlock()

		hdr := replayHeaders()
		hdr["X-Hypr-Confirm"] = tc.action
		r := doJSONHeaders(t, http.MethodPost, ts.URL+"/api/control/power", token,
			fmt.Sprintf(`{"action":%q}`, tc.action), hdr)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("power %s 状态 = %d", tc.action, r.StatusCode)
		}
		r.Body.Close()

		// 顺序断言：hook:shutdown 必须在 power:<action> 之前
		mock.mu.Lock()
		hd.mu.Lock()
		merged := append(append([]string(nil), hd.calls...), mock.calls...)
		hd.mu.Unlock()
		mock.mu.Unlock()
		if len(merged) != 2 || merged[0] != "hook:shutdown" || merged[1] != tc.wantPower {
			t.Fatalf("%s 调用顺序 = %v, want [hook:shutdown %s]", tc.action, merged, tc.wantPower)
		}
	}
}

// TestPowerNoPluginsStillWorks 验证未装配插件时电源操作不受影响。
func TestPowerNoPluginsStillWorks(t *testing.T) {
	ts, mock, store, _ := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/pair", "", `{"device_id":"dev-np","name":"x"}`)
	var out struct {
		Device struct {
			PIN string `json:"pin"`
		} `json:"device"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	token := authorizeDevice(t, store, out.Device.PIN)

	hdr := replayHeaders()
	hdr["X-Hypr-Confirm"] = "shutdown"
	r := doJSONHeaders(t, http.MethodPost, ts.URL+"/api/control/power", token, `{"action":"shutdown"}`, hdr)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("无插件时 power 状态 = %d", r.StatusCode)
	}
	r.Body.Close()
	mock.mu.Lock()
	last := mock.calls[len(mock.calls)-1]
	mock.mu.Unlock()
	if last != "power:shutdown" {
		t.Fatalf("无插件时 power 调用 = %s", last)
	}
}

// TestClipboardAndWindows 覆盖剪贴板读取与窗口列表的响应内容，
// 以及两个新端点的参数校验分支。
func TestClipboardAndWindows(t *testing.T) {
	ts, mock, store, _ := newTestServer(t)

	resp := doJSON(t, http.MethodPost, ts.URL+"/api/pair", "", `{"device_id":"dev-clip","name":"测试"}`)
	var out struct {
		Device struct {
			PIN string `json:"pin"`
		} `json:"device"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	token := authorizeDevice(t, store, out.Device.PIN)

	// clipboard get：返回 mock 中的文本
	mock.mu.Lock()
	before := len(mock.calls)
	mock.mu.Unlock()
	r := doJSON(t, http.MethodPost, ts.URL+"/api/control/clipboard", token, `{"action":"get"}`)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("clipboard get 状态 = %d, body=%s", r.StatusCode, readBody(r))
	}
	var got struct {
		Text string `json:"text"`
	}
	json.NewDecoder(r.Body).Decode(&got)
	r.Body.Close()
	if got.Text != "剪贴板内容" {
		t.Fatalf("clipboard get text = %q", got.Text)
	}
	mock.mu.Lock()
	calls := mock.calls[before:]
	mock.mu.Unlock()
	if len(calls) != 1 || calls[0] != "clipboard:get" {
		t.Fatalf("clipboard get mock 调用 = %v", calls)
	}

	// windows list：返回窗口数组与激活标记
	r = doJSON(t, http.MethodPost, ts.URL+"/api/control/windows", token, `{"action":"list"}`)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("windows list 状态 = %d", r.StatusCode)
	}
	var wl struct {
		Windows []control.WindowInfo `json:"windows"`
	}
	json.NewDecoder(r.Body).Decode(&wl)
	r.Body.Close()
	if len(wl.Windows) != 2 || wl.Windows[0].Handle != 111 ||
		!wl.Windows[0].Active || wl.Windows[1].Active {
		t.Fatalf("windows list = %+v", wl.Windows)
	}

	// 参数校验：未知 action / set 缺 text / focus 缺 handle
	for _, tc := range []struct{ path, body string }{
		{"/api/control/clipboard", `{"action":"boom"}`},
		{"/api/control/clipboard", `{"action":"set"}`},
		{"/api/control/windows", `{"action":"boom"}`},
		{"/api/control/windows", `{"action":"focus"}`},
	} {
		r := doJSON(t, http.MethodPost, ts.URL+tc.path, token, tc.body)
		if r.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s %s 应 400, got %d", tc.path, tc.body, r.StatusCode)
		}
		r.Body.Close()
	}
}
