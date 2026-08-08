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

    // 重启 / 关机（危险操作：确认 + 说明 10 秒内可取消）
    document.getElementById('btn-restart').addEventListener('click', () => {
      if (confirm('确定要重启主机吗？将在 10 秒后重启（主机上可执行 shutdown /a 取消）。')) {
        Api.control('/api/control/power', { action: 'restart' })
          .catch(window.__hctrlError || console.error);
      }
    });
    document.getElementById('btn-shutdown').addEventListener('click', () => {
      if (confirm('确定要关机吗？将在 10 秒后关机（主机上可执行 shutdown /a 取消）。')) {
        Api.control('/api/control/power', { action: 'shutdown' })
          .catch(window.__hctrlError || console.error);
      }
    });

    // 重新配对（清 token 回到配对流程）
    document.getElementById('btn-repair').addEventListener('click', () => {
      localStorage.removeItem('hctrl_token');
      location.reload();
    });
  }

  return { init };
})();
