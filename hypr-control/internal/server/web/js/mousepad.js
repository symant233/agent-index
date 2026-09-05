'use strict';
// 触控板手势（Pointer Events 逐指跟踪，各手势互斥）：
//   单指滑动移动指针（速度感知缩放，无死区）· 单指点按=左键
//   单指/双击第二下长按不动（~450ms）= 右键
//   双击第二下按住并移动 = 拖拽（左键按下 + 同步移动，松手释放）
//   双指滑动 = 垂直/水平滚动（按中点位移、主导轴判定）
//   双指快速同按同抬 = 右键 · 三指快速同按同抬 = 中键
//   滚轮滚动 · 右键/中键按钮仍可用
const Mousepad = (() => {
  const THROTTLE_MS = 12;   // 位移发送节流
  const BASE_SPEED = 0.5;   // 基准速度 px/ms（1 倍）
  const MIN_FACTOR = 1.0;   // 最慢时的位移缩放：低速 1:1（CSS px → 光标 px），
                            // 保证慢移足够远；不再向下压缩（原 0.5 会把慢移减半）
  const MAX_FACTOR = 4.0;   // 最快时的位移缩放
  const SCROLL_NOTCH_PX = 30; // 双指滚动：每 30px 中点位移发一格（±120）

  const DBL_TAP_MS = 250;   // 双击窗口；同时是单击延迟确认时长：
                            // 第一下 tap 挂起，窗口内无第二下按下才落地为单击
                            // （触控板标准行为，换取拖拽/长按不破坏主机选区）
  const DRAG_MOVE_PX = 10;  // 双击第二下超过此位移 → 触发拖拽
  const HOLD_MS = 450;      // 长按（不动）触发右键的时长
  const HOLD_MOVE_PX = 12;  // 长按允许的最大位移
  const TAP_MOVE_PX = 12;   // 点按判定：抬起时累计位移上限
  const MULTI_MS = 300;     // 多指手势：按下到全部抬起的总时长窗口
  const MULTI_MOVE_PX = 25; // 多指手势允许的最大累计位移

  const pad = document.getElementById('mousepad');
  const pointers = new Map();   // pointerId → {x, y, sx, sy}

  // ---- 单指指针移动（速度感知缩放） ----
  let lastSent = 0;
  let lastMoveT = 0, smoothSpeed = 0;
  let accX = 0, accY = 0;

  // ---- 手势状态（每轮手势开始时复位） ----
  let totalMove = 0;        // 本轮手势累计位移（原始 px，判定用）
  let lastTapUp = 0;        // 上一次点按抬起时刻（双击判定）
  let lastDragEnd = 0;      // 上一次拖拽结束时刻
  let dragArm = false;      // 双击第二下按住中（移动→拖拽 / 超时→右键 / 快抬→点按）
  let dragging = false;     // 拖拽已触发（左键按住中）
  let holdTimer = null;     // 长按定时器
  let holdFired = false;    // 长按右键已触发
  let multiGauge = null;    // 多指手势判定 {count, downT}
  let multiFired = false;   // 多指点按已触发（残留移动不生效）
  let multiTouched = false; // 本轮手势出现过 ≥2 指（抑制点按）
  let tapPending = false;   // 第一下 tap 挂起中（延迟确认，见 armTap）
  let tapTimer = null;      // 挂起 tap 的落地定时器
  let dragArmWasSecond = false; // 本次 dragArm 是否来自双击第二下

  // ---- 双指滚动状态 ----
  let lastMid = null;       // 双指中点（上次事件位置；null=当前不在双指滚动）
  let accSX = 0, accSY = 0; // 滚动累计位移（未满一格的余量保留）
  let scrolled2 = false;    // 本轮双指手势已产生滚动（抑制松手时误触双指右键）

  function send(body) {
    Api.control('/api/control/mouse', body).catch(window.__hctrlError || console.error);
  }

  // twoMid 返回恰好两指时的中点位置。
  function twoMid() {
    const pts = [...pointers.values()];
    return { x: (pts[0].x + pts[1].x) / 2, y: (pts[0].y + pts[1].y) / 2 };
  }

  // flushAcc 把累计位移取整发送，小数余量保留到下一窗口：
  // 慢速移动时每个节流窗口只积累零点几像素，直接清零会系统性丢步，
  // 表现为"手指挪了很多、光标没怎么动"。
  function flushAcc() {
    const dx = Math.round(accX), dy = Math.round(accY);
    if (dx !== 0 || dy !== 0) send({ action: 'move', dx, dy });
    accX -= dx;
    accY -= dy;
  }

  function cancelHold() {
    if (holdTimer) { clearTimeout(holdTimer); holdTimer = null; }
  }

  // armTap 把第一下 tap 挂起，DBL_TAP_MS 内无第二下按下才落地为单击。
  // 第二下按住期间由手势出口决定：快抬→补发（双击）、移动→丢弃（拖拽不
  // 破坏选区）、长按→丢弃（右键作用于原选区）。
  function armTap() {
    cancelTap();
    tapPending = true;
    tapTimer = setTimeout(() => {
      tapTimer = null;
      if (tapPending) {
        tapPending = false;
        send({ action: 'click', button: 'left' });
      }
    }, DBL_TAP_MS);
  }

  function cancelTap() {
    if (tapTimer) { clearTimeout(tapTimer); tapTimer = null; }
    tapPending = false;
  }

  function armHold() {
    cancelHold();
    holdTimer = setTimeout(() => {
      holdTimer = null;
      // 到期仍单指、未移动多少、未在拖拽 → 右键
      if (pointers.size === 1 && !dragging && !holdFired && totalMove < HOLD_MOVE_PX) {
        holdFired = true;
        dragArm = false;
        cancelTap(); // 长按生效：挂起的单击不补发（右键作用于原选区）
        send({ action: 'click', button: 'right' });
      }
    }, HOLD_MS);
  }

  function trackMove(dx, dy) {
    let factor = 1;
    const now = performance.now();
    if (lastMoveT > 0) {
      const dt = Math.max(now - lastMoveT, 1);
      const inst = Math.hypot(dx, dy) / dt;
      smoothSpeed = 0.55 * smoothSpeed + 0.45 * inst;
      factor = Math.min(MAX_FACTOR, Math.max(MIN_FACTOR, smoothSpeed / BASE_SPEED));
    }
    lastMoveT = now;
    accX += dx * factor;
    accY += dy * factor;
    const t = Date.now();
    if (t - lastSent < THROTTLE_MS) return;
    lastSent = t;
    flushAcc();
  }

  // ---- pointer 事件 ----
  pad.addEventListener('pointerdown', (e) => {
    e.preventDefault();
    pad.setPointerCapture(e.pointerId);
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY, sx: e.clientX, sy: e.clientY });

    if (pointers.size === 1) {
      // 新一轮单指手势：复位判定状态
      totalMove = 0;
      holdFired = false;
      multiFired = false;
      multiGauge = null;
      multiTouched = false;
      lastMoveT = 0; smoothSpeed = 0;
      lastMid = null; accSX = 0; accSY = 0; scrolled2 = false;
      const now = performance.now();
      const inDblWindow = now - lastTapUp < DBL_TAP_MS && now - lastDragEnd >= DBL_TAP_MS;
      if (inDblWindow && tapPending) {
        // 双击第二下：挂起的第一下先不发（由本下手势的出口决定补发/丢弃）
        cancelTap();
        dragArm = true;
        dragArmWasSecond = true;
      } else {
        dragArm = false;
        dragArmWasSecond = false;
      }
      // 长按计时：普通单指长按 与 双击第二下按住不动，都触发右键
      armHold();
      return;
    }
    // 新手指加入：取消长按与移动残留，进入/更新多指判定
    cancelHold();
    cancelTap(); // 多指手势丢弃挂起的第一下（不补发）
    flushAcc();
    lastMid = null; accSX = 0; accSY = 0; scrolled2 = false; // 滚动状态从头累计
    multiTouched = true;
    if (!multiFired) {
      if (!multiGauge) multiGauge = { count: pointers.size, downT: performance.now() };
      else multiGauge.count = Math.max(multiGauge.count, pointers.size);
    }
    if (dragArm && !dragging) dragArm = false; // 多指按下使双击拖拽失效
  });

  pad.addEventListener('pointermove', (e) => {
    const p = pointers.get(e.pointerId);
    if (!p) return;
    const dx = e.clientX - p.x;
    const dy = e.clientY - p.y;
    p.x = e.clientX; p.y = e.clientY;

    if (pointers.size > 1) {
      totalMove += Math.hypot(dx, dy);
      if (pointers.size !== 2) { lastMid = null; return; } // 三指及以上不滚动
      // 双指滑动 = 滚动：取两指中点位移，按主导轴发垂直/水平滚轮
      const m = twoMid();
      if (lastMid) {
        accSX += m.x - lastMid.x;
        accSY += m.y - lastMid.y;
      }
      lastMid = m;
      while (Math.abs(accSY) >= SCROLL_NOTCH_PX || Math.abs(accSX) >= SCROLL_NOTCH_PX) {
        if (Math.abs(accSY) >= Math.abs(accSX)) {
          // 与鼠标滚轮同手感（Windows 默认）：手指下滑 = 页面向下滚，delta 为负
          send({ action: 'scroll', delta: accSY > 0 ? -120 : 120 });
          accSY -= accSY > 0 ? SCROLL_NOTCH_PX : -SCROLL_NOTCH_PX;
        } else {
          send({ action: 'hscroll', delta: accSX > 0 ? 120 : -120 });
          accSX -= accSX > 0 ? SCROLL_NOTCH_PX : -SCROLL_NOTCH_PX;
        }
        scrolled2 = true;
      }
      return;
    }
    if (multiFired || holdFired) return; // 多指点按/长按右键后的残留移动不生效

    totalMove = Math.max(totalMove, Math.hypot(e.clientX - p.sx, e.clientY - p.sy));
    if (totalMove > HOLD_MOVE_PX) cancelHold();

    if (dragArm) {
      // 双击第二下：超过阈值位移 → 实际触发拖拽（左键按下）
      if (!dragging && totalMove >= DRAG_MOVE_PX) {
        dragging = true;
        cancelHold();
        send({ action: 'down', button: 'left' });
      }
      if (!dragging) return; // 预备期小幅移动不移动指针（双击/拖拽判定的死区）
    }
    trackMove(dx, dy); // 正常移动：立即生效，无死区
  });

  pad.addEventListener('pointerup', (e) => {
    const p = pointers.get(e.pointerId);
    if (!p) return;
    pointers.delete(e.pointerId);

    if (multiFired) {
      // 多指点按已触发：等全部手指离开后结束本轮手势
      if (pointers.size === 0) {
        multiFired = false;
        multiGauge = null;
        totalMove = 0;
      }
      return;
    }

    if (pointers.size > 0) {
      // 还有手指在屏上：不做点按判定；多指判定 gauge 保留
      // （双指 tap 两根手指先后抬起，最后一根抬起时才判定）
      flushAcc();
      cancelHold();
      lastMid = null; // 双指抬走一根：滚动结束
      return;
    }

    // 最后一根手指抬起：收尾判定
    cancelHold();
    flushAcc();
    const now = performance.now();
    const wasDragging = dragging;
    if (dragging) {
      // 拖拽结束：挂起的第一下不补发（拖拽生效 = 单击被消费为按住）
      cancelTap();
      send({ action: 'up', button: 'left' });
      dragging = false;
      lastDragEnd = now;
    }
    dragArm = false;

    // 多指手势判定：全部按下的时间窗内、位移小、未滚动、指头数对 → 右键/中键
    if (multiGauge && !multiFired) {
      const dt = now - multiGauge.downT;
      const n = multiGauge.count;
      if (dt <= MULTI_MS && totalMove < MULTI_MOVE_PX && !scrolled2 && (n === 2 || n === 3)) {
        send({ action: 'click', button: n === 2 ? 'right' : 'middle' });
      }
      multiGauge = null;
      return; // 多指手势不参与点按判定
    }

    // 单指点按：未长按、未拖拽、未多指、位移小 → 左键（挂起，延迟落地）
    if (!holdFired && !wasDragging && !multiTouched && totalMove < TAP_MOVE_PX) {
      if (dragArmWasSecond) {
        // 双击第二下快抬：补发挂起的第一下 + 本下 → 主机收到双击
        send({ action: 'click', button: 'left' });
        send({ action: 'click', button: 'left' });
        lastTapUp = 0; // 双击完成，紧接的第三下不算双击窗口
      } else {
        armTap();
        lastTapUp = now;
      }
    }
  });

  pad.addEventListener('pointercancel', (e) => {
    // 系统接管/取消：清状态；拖拽中必须释放左键，避免主机上按键卡死
    pointers.delete(e.pointerId);
    cancelHold();
    cancelTap();
    flushAcc();
    if (dragging) send({ action: 'up', button: 'left' });
    dragArm = false; dragging = false;
    multiGauge = null; multiFired = false;
    lastMid = null; accSX = 0; accSY = 0; scrolled2 = false;
    if (pointers.size === 0) totalMove = 0;
  });

  pad.addEventListener('wheel', (e) => {
    e.preventDefault();
    send({ action: 'scroll', delta: e.deltaY < 0 ? 120 : -120 });
  }, { passive: false });

  document.getElementById('btn-rbtn').addEventListener('click', () =>
    send({ action: 'click', button: 'right' }));
  document.getElementById('btn-mbtn').addEventListener('click', () =>
    send({ action: 'click', button: 'middle' }));

  function init() { /* 事件在模块加载时已绑定 */ }

  return { init, send };
})();
