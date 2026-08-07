'use strict';
// 触控板：拖动发送相对移动（节流），点按=左键，滚轮滚动，右键/中键按钮。
const Mousepad = (() => {
  let x0 = null, y0 = null, moved = false, lastSent = 0;
  const THROTTLE_MS = 25;

  function init() {
    const pad = document.getElementById('mousepad');

    pad.addEventListener('pointerdown', (e) => {
      e.preventDefault();
      pad.setPointerCapture(e.pointerId);
      x0 = e.clientX;
      y0 = e.clientY;
      moved = false;
    });

    pad.addEventListener('pointermove', (e) => {
      if (x0 === null) return;
      const dx = e.clientX - x0;
      const dy = e.clientY - y0;
      x0 = e.clientX;
      y0 = e.clientY;
      if (Math.abs(dx) > 1 || Math.abs(dy) > 1) moved = true;
      const now = Date.now();
      if (now - lastSent < THROTTLE_MS) return;
      lastSent = now;
      send({ action: 'move', dx: Math.round(dx), dy: Math.round(dy) });
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
