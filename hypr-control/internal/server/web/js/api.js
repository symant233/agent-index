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
    return data;
  }

  async function control(path, body) {
    const resp = await fetch(path, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + token(),
      },
      body: JSON.stringify(body || {}),
    });
    if (resp.status === 401) throw new AuthError('设备令牌失效，请重新配对');
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) throw new Error(data.error || ('控制请求失败 ' + resp.status));
    return data;
  }

  return { deviceId, token, saveToken, clearToken, pair, control, AuthError };
})();
