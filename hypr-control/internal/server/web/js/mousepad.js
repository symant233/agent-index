'use strict';
// 触控板：根据手指滑动速度缩放鼠标位移（滑得快 → 移得多，滑得慢 → 移得少），
// 节流窗口内累积位移后一次发送（不丢帧、更跟手），点按=左键，滚轮滚动，右键/中键按钮。
const Mousepad = (() => {
  const THROTTLE_MS = 12;   // 发送节流（更短 → 更跟手、少卡顿）
  const BASE_SPEED = 0.5;   // 基准速度 px/ms：0.5px/ms 即 1 倍（整体更灵敏）
  const MIN_FACTOR = 0.5;   // 最慢时的位移缩放
  const MAX_FACTOR = 4.0;   // 最快时的位移缩放

  let x0 = null, y0 = null, moved = false, lastSent = 0;
  let lastMoveT = 0, smoothSpeed = 0;
  let accX = 0, accY = 0;   // 节流窗口内累积位移

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
      accX = accY = 0;
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

      // 累积位移（缩放后），节流到期一次发送，避免丢帧导致卡顿
      accX += dx * factor;
      accY += dy * factor;
      const t = Date.now();
      if (t - lastSent < THROTTLE_MS) return;
      lastSent = t;
      send({ action: 'move', dx: Math.round(accX), dy: Math.round(accY) });
      accX = accY = 0;
    });

    pad.addEventListener('pointerup', () => {
      x0 = null;
      // 发送残留累积位移（若尚未发送）
      if (accX !== 0 || accY !== 0) {
        send({ action: 'move', dx: Math.round(accX), dy: Math.round(accY) });
        accX = accY = 0;
      }
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
