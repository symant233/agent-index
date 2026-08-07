'use strict';
// 配对视图渲染与视图切换。
const Pair = (() => {
  const VIEWS = ['view-pair', 'view-denied', 'view-remote'];

  function showPending(data) {
    document.getElementById('pair-pin').textContent = data.device.pin;
    document.getElementById('pair-device-id').textContent = data.device.id;
    document.getElementById('pair-ip').textContent = data.device.ip || '(未知)';
    document.getElementById('pair-ua').textContent = data.device.ua || '(未知)';
    document.getElementById('pair-pending-msg').classList.remove('hidden');
  }

  function hideAll() {
    VIEWS.forEach(id => document.getElementById(id).classList.add('hidden'));
  }

  function show(id) {
    hideAll();
    document.getElementById(id).classList.remove('hidden');
  }

  return { showPending, hideAll, show };
})();
