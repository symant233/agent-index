'use strict';
// API 客户端：设备 ID / token 管理，配对与控制请求封装。
const Api = (() => {
  const ID_KEY = 'hctrl_device_id';
  const TOKEN_KEY = 'hctrl_token';

  class AuthError extends Error {}

  function deviceId() {
    let id = localStorage.getItem(ID_KEY);
    if (!id) {
      id = (crypto.randomUUID && crypto.randomUUID()) ||
        ('dev-' + Date.now() + '-' + Math.random().toString(36).slice(2, 10));
      localStorage.setItem(ID_KEY, id);
    }
    return id;
  }

  function token() { return localStorage.getItem(TOKEN_KEY) || ''; }
  function saveToken(t) { if (t) localStorage.setItem(TOKEN_KEY, t); }
  function clearToken() { localStorage.removeItem(TOKEN_KEY); }

  // randomHex 生成 n 字节的十六进制随机串（crypto 优先，回退 Math.random）。
  function randomHex(bytes) {
    const buf = new Uint8Array(bytes);
    if (crypto && crypto.getRandomValues) {
      crypto.getRandomValues(buf);
    } else {
      for (let i = 0; i < bytes; i++) buf[i] = Math.floor(Math.random() * 256);
    }
    return Array.from(buf, b => b.toString(16).padStart(2, '0')).join('');
  }

  async function pair() {
    const resp = await fetch('/api/pair', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        device_id: deviceId(),
        name: (navigator.userAgent || '').slice(0, 40),
      }),
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) throw new Error(data.error || ('配对请求失败 ' + resp.status));
    // 始终采用服务端登记的真实设备 ID（新设备生成后、找回授权时都会返回），
    // 保证 localStorage 与设备表一致，避免重复授权。
    if (data.device && data.device.id) {
      localStorage.setItem(ID_KEY, data.device.id);
    }
    return data;
  }

  async function control(path, body, extraHeaders = {}) {
    // 防重放：时间戳 + 一次性 nonce（服务端校验窗口并去重）
    const ts = Math.floor(Date.now() / 1000).toString();
    const nonce = randomHex(32);
    const resp = await fetch(path, {
      method: 'POST',
      headers: Object.assign({
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + token(),
        'X-Hypr-Timestamp': ts,
        'X-Hypr-Nonce': nonce,
      }, extraHeaders),
      body: JSON.stringify(body || {}),
    });
    if (resp.status === 401) throw new AuthError('设备令牌失效，请重新配对');
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) throw new Error(data.error || ('控制请求失败 ' + resp.status));
    return data;
  }

  return { deviceId, token, saveToken, clearToken, pair, control, AuthError };
})();
