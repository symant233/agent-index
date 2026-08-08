'use strict';
// 触控板：根据手指滑动速度缩放鼠标位移（滑得快 → 移得多，滑得慢 → 移得少），
// 拖动发送相对移动（节流），点按=左键，滚轮滚动，右键/中键按钮。
const Mousepad = (() => {
  const THROTTLE_MS = 25;   // 发送节流
  const BASE_SPEED = 1.2;   // 基准速度 px/ms，视为 1 倍
  const MIN_FACTOR = 0.3;   // 最慢时的位移缩放
  const MAX_FACTOR = 3.0;   // 最快时的位移缩放

  let x0 = null, y0 = null, moved = false, lastSent = 0;
  let lastMoveT = 0, smoothSpeed = 0;

  function init() {
    const pad = document.getElementById('mousepad');

    pad.addEventListener('pointerdown', (e) => {
      e.preventDefault();
      pad.setPointerCapture(e.pointerId);
      x0 = e.clientX;
      y0 = e.clientY;
      moved = false;
      lastMoveT = 0;
      smoothSpeed = 0;
    });

    pad.addEventListener('pointermove', (e) => {
      if (x0 === null) return;
      const dx = e.clientX - x0;
      const dy = e.clientY - y0;
      x0 = e.clientX;
      y0 = e.clientY;
      if (Math.abs(dx) > 1 || Math.abs(dy) > 1) moved = true;

      // 速度感知：瞬时速度 → 指数平滑 → 位移缩放因子
      let factor = 1;
      const now = performance.now();
      if (lastMoveT > 0) {
        const dt = Math.max(now - lastMoveT, 1);
        const inst = Math.hypot(dx, dy) / dt; // px/ms
        smoothSpeed = 0.55 * smoothSpeed + 0.45 * inst;
        factor = Math.min(MAX_FACTOR, Math.max(MIN_FACTOR, smoothSpeed / BASE_SPEED));
      }
      lastMoveT = now;

      const t = Date.now();
      if (t - lastSent < THROTTLE_MS) return;
      lastSent = t;
      send({
        action: 'move',
        dx: Math.round(dx * factor),
        dy: Math.round(dy * factor),
      });
    });

    pad.addEventListener('pointerup', () => {
      x0 = null;
      if (!moved) send({ action: 'click', button: 'left' });
    });

    pad.addEventListener('wheel', (e) => {
      e.preventDefault();
      send({ action: 'scroll', delta: e.deltaY < 0 ? 120 : -120 });
    }, { passive: false });

    document.getElementById('btn-rbtn').addEventListener('click', () =>
      send({ action: 'click', button: 'right' }));
    document.getElementById('btn-mbtn').addEventListener('click', () =>
      send({ action: 'click', button: 'middle' }));
  }

  function send(body) {
    Api.control('/api/control/mouse', body).catch(window.__hctrlError || console.error);
  }

  return { init, send };
})();
