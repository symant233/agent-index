package server

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"

	"hypr-control/internal/devices"
)

// writeJSON 输出 JSON 响应。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// writeErr 输出统一错误响应 {"error": "..."}。
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decodeBody 解析请求体 JSON，失败时写 400 并返回 false。
func decodeBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(r.Body).Decode(out); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return false
	}
	return true
}

// handlePair 登记访问设备：
//   - 新设备 / 被拒后再次访问：进入 pending 并返回 PIN（需在主机上 CLI 允许）
//   - 待授权设备重复访问：返回原 PIN
//   - 已授权设备访问：返回 token，可直接控制
func (c *Control) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID string `json:"device_id"`
		Name     string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.DeviceID == "" {
		writeErr(w, http.StatusBadRequest, "缺少 device_id（浏览器生成并持久化的稳定 ID）")
		return
	}
	if len(body.DeviceID) > 128 {
		writeErr(w, http.StatusBadRequest, "device_id 过长")
		return
	}

	ip := clientIP(r)
	d, err := c.store.Register(body.DeviceID, body.Name, ip, r.UserAgent())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	switch d.Status {
	case devices.StatusAuthorized:
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "authorized",
			"device": d,
		})
	case devices.StatusPending:
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "pending",
			"device": d,
		})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"status": d.Status,
			"device": d,
		})
	}
}

// clientIP 提取客户端 IP（优先 X-Forwarded-For 首段，回退 RemoteAddr）。
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// handleKey 单键：{"key":"enter"}
func (c *Control) handleKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Key == "" {
		writeErr(w, http.StatusBadRequest, "缺少 key")
		return
	}
	if err := c.backend.KeyTap(body.Key); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "key"})
}

// handleKeys 组合键：{"keys":["ctrl","c"]}
func (c *Control) handleKeys(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keys []string `json:"keys"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if len(body.Keys) == 0 {
		writeErr(w, http.StatusBadRequest, "keys 不能为空")
		return
	}
	if len(body.Keys) > 8 {
		writeErr(w, http.StatusBadRequest, "组合键数量过多（最多 8 个）")
		return
	}
	if err := c.backend.Chord(body.Keys); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "keys"})
}

// handleMouse 鼠标操作，action 取值：
//
//	move     相对移动 {"dx","dy"}
//	move_to  绝对定位 {"x","y"}
//	click    点击     {"button":"left|right|middle"}
//	scroll   滚轮     {"delta":±120}
func (c *Control) handleMouse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string  `json:"action"`
		DX     int32   `json:"dx"`
		DY     int32   `json:"dy"`
		X      int     `json:"x"`
		Y      int     `json:"y"`
		Button string  `json:"button"`
		Delta  int32   `json:"delta"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	var err error
	switch body.Action {
	case "move":
		err = c.backend.MouseMoveRel(body.DX, body.DY)
	case "move_to":
		err = c.backend.MouseMoveTo(body.X, body.Y)
	case "click":
		err = c.backend.MouseClick(body.Button)
	case "scroll":
		err = c.backend.MouseScroll(body.Delta)
	default:
		writeErr(w, http.StatusBadRequest, "未知鼠标动作: "+body.Action)
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": body.Action})
}

// handleVolume 音量：{"action":"up|down|mute"}
func (c *Control) handleVolume(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	var err error
	switch body.Action {
	case "up":
		err = c.backend.VolumeUp()
	case "down":
		err = c.backend.VolumeDown()
	case "mute":
		err = c.backend.VolumeMute()
	default:
		writeErr(w, http.StatusBadRequest, "未知音量动作: "+body.Action)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": body.Action})
}

// handleMedia 媒体控制：{"action":"playpause|next|prev|stop"}
func (c *Control) handleMedia(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	var err error
	switch body.Action {
	case "playpause":
		err = c.backend.MediaPlayPause()
	case "next":
		err = c.backend.MediaNext()
	case "prev":
		err = c.backend.MediaPrev()
	case "stop":
		err = c.backend.MediaStop()
	default:
		writeErr(w, http.StatusBadRequest, "未知媒体动作: "+body.Action)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": body.Action})
}

// handleLock 锁屏。
func (c *Control) handleLock(w http.ResponseWriter, _ *http.Request) {
	if err := c.backend.Lock(); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "lock"})
}
