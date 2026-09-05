'use strict';
// 应用入口：配对状态机（pending → authorized/denied）与视图切换。
(function main() {
  window.__hctrlError = (err) => {
    const el = document.getElementById('status-text');
    if (el) el.textContent = '⚠ ' + err.message;
    if (err instanceof Api.AuthError) {
      Api.clearToken();
      setTimeout(() => location.reload(), 1500);
    }
  };

  // showVersion 把服务端下发的版本号写入顶栏（服务端 config.Version 为唯一来源）
  function showVersion(data) {
    if (!data || !data.version) return;
    const el = document.getElementById('app-version');
    if (el) el.textContent = 'v' + data.version;
  }

  function showRemote() {
    Pair.show('view-remote');
    Remote.init();
    Mousepad.init();
    const el = document.getElementById('status-text');
    el.textContent = '已连接：' + location.hostname + '（' + location.port + '）';
  }

  async function pollUntilAuthorized() {
    for (let i = 0; i < 200; i++) {
      await new Promise(r => setTimeout(r, 3000));
      let d;
      try {
        d = await Api.pair();
      } catch (_) {
        continue; // 网络抖动，继续轮询
      }
      if (d.status === 'authorized') {
        Api.saveToken(d.device.token);
        showVersion(d);
        showRemote();
        return;
      }
      if (d.status === 'denied') {
        Pair.show('view-denied');
        return;
      }
      // 仍 pending：更新 PIN（理论上不变，保险起见）
      if (d.device && d.device.pin) {
        document.getElementById('pair-pin').textContent = d.device.pin;
      }
    }
  }

  (async () => {
    let data;
    try {
      data = await Api.pair();
    } catch (err) {
      document.body.innerHTML =
        '<div class="card"><h1>无法连接主机</h1><p class="hint">' + err.message +
        '</p><p class="hint">请确认主机上的 hypr-control 服务已启动（hctrl serve）且网络可达。</p></div>';
      return;
    }

    showVersion(data);
    if (data.status === 'authorized') {
      Api.saveToken(data.device.token);
      showRemote();
    } else if (data.status === 'pending') {
      Pair.show('view-pair');
      Pair.showPending(data);
      pollUntilAuthorized();
    } else {
      Pair.show('view-denied');
    }
  })();
})();
