'use strict';
// 遥控器主界面交互：方向键/常用键、音量、媒体、锁屏、重新配对。
const Remote = (() => {
  // throttleClick 防抖：ms 内重复点击只生效一次（防双击/误触连发）。
  function throttleClick(handler, ms = 250) {
    let last = 0;
    return function (ev) {
      const now = Date.now();
      if (now - last < ms) return;
      last = now;
      handler(ev);
    };
  }

  function init() {
    const setStatus = (t) => {
      const el = document.getElementById('status-text');
      if (el) el.textContent = t;
    };

    // 单键（data-key）
    document.querySelectorAll('[data-key]').forEach(btn => {
      btn.addEventListener('click', () =>
        Api.control('/api/control/key', { key: btn.dataset.key }).catch(window.__hctrlError || console.error));
    });

    // 组合键（data-chord，JSON 数组）
    document.querySelectorAll('[data-chord]').forEach(btn => {
      btn.addEventListener('click', () => {
        let keys;
        try { keys = JSON.parse(btn.dataset.chord); } catch (_) { return; }
        Api.control('/api/control/keys', { keys }).catch(window.__hctrlError || console.error);
      });
    });

    // 音量（防抖：一次点击只触发一次）
    document.querySelectorAll('[data-vol]').forEach(btn => {
      btn.addEventListener('click', throttleClick(() =>
        Api.control('/api/control/volume', { action: btn.dataset.vol }).catch(window.__hctrlError || console.error)));
    });

    // 媒体（防抖：一次点击只触发一次）
    document.querySelectorAll('[data-media]').forEach(btn => {
      btn.addEventListener('click', throttleClick(() =>
        Api.control('/api/control/media', { action: btn.dataset.media }).catch(window.__hctrlError || console.error)));
    });

    // 锁屏（在“更多”面板内）
    document.getElementById('btn-lock').addEventListener('click', () => {
      if (confirm('确定要锁屏主机吗？')) {
        Api.control('/api/control/lock', {}).catch(window.__hctrlError || console.error);
      }
    });

    // “更多”面板：锁屏 / 重启 / 关机
    const panel = document.getElementById('more-panel');
    document.getElementById('btn-more').addEventListener('click', () => {
      panel.classList.remove('hidden');
    });
    document.getElementById('btn-more-close').addEventListener('click', () => {
      panel.classList.add('hidden');
    });
    panel.addEventListener('click', (ev) => {
      if (ev.target === panel) panel.classList.add('hidden'); // 点遮罩关闭
    });

    // 重启 / 关机（危险操作：确认 + 二次确认头；立即执行，不可取消）
    document.getElementById('btn-restart').addEventListener('click', () => {
      if (confirm('确定要立即重启主机吗？')) {
        Api.control('/api/control/power', { action: 'restart' }, { 'X-Hypr-Confirm': 'restart' })
          .catch(window.__hctrlError || console.error);
      }
    });
    document.getElementById('btn-shutdown').addEventListener('click', () => {
      if (confirm('确定要立即关机吗？')) {
        Api.control('/api/control/power', { action: 'shutdown' }, { 'X-Hypr-Confirm': 'shutdown' })
          .catch(window.__hctrlError || console.error);
      }
    });

    // ---- 窗口切换器：居中面板列出可切换窗口，点击激活 ----
    const winPanel = document.getElementById('window-panel');
    const winList = document.getElementById('window-list');

    function renderWindows(ws) {
      winList.innerHTML = '';
      if (!ws.length) {
        const div = document.createElement('div');
        div.className = 'hint';
        div.textContent = '没有可切换的窗口';
        winList.appendChild(div);
        return;
      }
      ws.forEach((w) => {
        const b = document.createElement('button');
        b.className = 'ctl window-item' + (w.active ? ' active' : '');
        b.textContent = (w.active ? '● ' : '') + w.title;
        b.title = w.title;
        b.addEventListener('click', throttleClick(async () => {
          try {
            await Api.control('/api/control/windows', { action: 'focus', handle: w.handle });
            winPanel.classList.add('hidden');
            setStatus('已切换窗口：' + w.title);
          } catch (err) {
            (window.__hctrlError || console.error)(err);
          }
        }));
        winList.appendChild(b);
      });
    }

    document.getElementById('btn-windows').addEventListener('click', async () => {
      winPanel.classList.remove('hidden');
      winList.innerHTML = '<div class="hint">加载中…</div>';
      try {
        const d = await Api.control('/api/control/windows', { action: 'list' });
        renderWindows(d.windows || []);
      } catch (err) {
        winList.innerHTML = '<div class="hint">获取窗口列表失败：' + err.message + '</div>';
      }
    });
    document.getElementById('btn-window-close').addEventListener('click', () =>
      winPanel.classList.add('hidden'));
    winPanel.addEventListener('click', (ev) => {
      if (ev.target === winPanel) winPanel.classList.add('hidden'); // 点遮罩关闭
    });

    // ---- 剪贴板同步：拷贝=电脑→手机，粘贴=手机→电脑 ----
    // 浏览器剪贴板 API（navigator.clipboard）在自签名证书下可能不可用，
    // 失败时降级为 textarea 面板手动中转。
    const clipPanel = document.getElementById('clip-panel');
    const clipText = document.getElementById('clip-text');

    function showClipPanel(mode, text) {
      // mode: copy=展示电脑剪贴板内容供手动复制；paste=编辑后发送到电脑
      document.getElementById('clip-title').textContent =
        mode === 'copy' ? '电脑剪贴板内容' : '粘贴到电脑';
      document.getElementById('clip-hint').textContent = mode === 'copy'
        ? '浏览器剪贴板不可用，请点选下方文本手动复制'
        : '请把要发送的内容粘贴到下方，点"发送到电脑"';
      clipText.value = text || '';
      document.getElementById('btn-clip-send').classList.toggle('hidden', mode !== 'paste');
      clipPanel.classList.remove('hidden');
      if (mode === 'copy') { clipText.focus(); clipText.select(); }
    }

    async function clipWrite(text) {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        try { await navigator.clipboard.writeText(text); return true; } catch (_) { /* 降级 */ }
      }
      return false;
    }
    async function clipRead() {
      if (navigator.clipboard && navigator.clipboard.readText) {
        try { return await navigator.clipboard.readText(); } catch (_) { /* 降级 */ }
      }
      return null;
    }

    document.getElementById('btn-clip-pull').addEventListener('click', throttleClick(async () => {
      try {
        const d = await Api.control('/api/control/clipboard', { action: 'get' });
        const text = d.text || '';
        if (!text) { setStatus('电脑剪贴板为空或非文本'); return; }
        if (await clipWrite(text)) setStatus('已复制电脑剪贴板（' + text.length + ' 字符）');
        else showClipPanel('copy', text);
      } catch (err) {
        (window.__hctrlError || console.error)(err);
      }
    }));

    document.getElementById('btn-clip-push').addEventListener('click', throttleClick(async () => {
      try {
        const text = await clipRead();
        if (text == null) { showClipPanel('paste', ''); return; }
        if (!text) { setStatus('手机剪贴板为空'); return; }
        await Api.control('/api/control/clipboard', { action: 'set', text });
        setStatus('已粘贴到电脑剪贴板（' + text.length + ' 字符）');
      } catch (err) {
        (window.__hctrlError || console.error)(err);
      }
    }));

    document.getElementById('btn-clip-send').addEventListener('click', throttleClick(async () => {
      const text = clipText.value;
      if (!text) return;
      try {
        await Api.control('/api/control/clipboard', { action: 'set', text });
        clipPanel.classList.add('hidden');
        setStatus('已发送到电脑剪贴板（' + text.length + ' 字符）');
      } catch (err) {
        (window.__hctrlError || console.error)(err);
      }
    }));
    document.getElementById('btn-clip-close').addEventListener('click', () =>
      clipPanel.classList.add('hidden'));
    clipPanel.addEventListener('click', (ev) => {
      if (ev.target === clipPanel) clipPanel.classList.add('hidden');
    });

    // 重新配对（清 token 回到配对流程）
    document.getElementById('btn-repair').addEventListener('click', () => {
      localStorage.removeItem('hctrl_token');
      location.reload();
    });
  }

  return { init };
})();
