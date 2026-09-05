'use strict';
// 虚拟键盘：无输入框、不走手机输入法，点按直接映射为主机按键。
//   Ctrl/Alt/Win 点按锁定（高亮），按任一字符键时一并消费；
//   Shift 单发：打一个字符后自动复位；
//   符号层（#+=）切换数字与常用半角符号，Shift 变体（如 ! = shift+1）自动组合。
const Keyboard = (() => {
  const MODS = [
    { label: 'Esc', name: 'esc', type: 'key' },
    { label: 'Ctrl', name: 'ctrl', type: 'mod' },
    { label: 'Alt', name: 'alt', type: 'mod' },
    { label: 'Win', name: 'win', type: 'mod' },
    { label: '⇧ Shift', name: 'shift', type: 'shift' },
  ];
  const LETTER_ROWS = [
    ['q', 'w', 'e', 'r', 't', 'y', 'u', 'i', 'o', 'p'],
    ['a', 's', 'd', 'f', 'g', 'h', 'j', 'k', 'l'],
    ['z', 'x', 'c', 'v', 'b', 'n', 'm', { label: '⌫', name: 'backspace', type: 'key' }],
  ];
  const SYMBOL_ROWS = [
    ['1', '2', '3', '4', '5', '6', '7', '8', '9', '0'],
    ['!', '@', '#', '$', '%', '^', '&', '*', '(', ')'],
    ['-', '=', '[', ']', '\\', ';', "'", '`', '/'],
  ];
  const BOTTOM_ROW = [
    { label: '#+=', type: 'sym' },
    { label: ',', type: 'glyph', g: ',' },
    { label: '␣ 空格', name: 'space', type: 'key', wide: true },
    { label: '.', type: 'glyph', g: '.' },
    { label: '⏎', name: 'enter', type: 'key' },
  ];

  // GLYPH 是字符 → {k: 主机键名, s: 是否需 shift} 的映射。
  // 符号的 shift 变体（! = shift+1 等）发送时自动组合 shift 修饰键。
  const GLYPH = (() => {
    const m = {};
    'abcdefghijklmnopqrstuvwxyz'.split('').forEach((c) => { m[c] = { k: c, s: false }; });
    [['-', 'minus'], ['=', 'equal'], ['[', 'bracketleft'], [']', 'bracketright'],
      ['\\', 'backslash'], [';', 'semicolon'], ["'", 'quote'], ['`', 'grave'], ['/','slash'],
    ].forEach(([g, k]) => { m[g] = { k, s: false }; });
    [['!', '1'], ['@', '2'], ['#', '3'], ['$', '4'], ['%', '5'], ['^', '6'], ['&', '7'],
      ['*', '8'], ['(', '9'], [')', '0'], ['_', 'minus'], ['+', 'equal'],
      ['{', 'bracketleft'], ['}', 'bracketright'], ['|', 'backslash'],
      [':', 'semicolon'], ['"', 'quote'], ['~', 'grave'],
      ['<', 'comma'], ['>', 'period'], ['?', 'slash'],
    ].forEach(([g, k]) => { m[g] = { k, s: true }; });
    [',', '.'].forEach((c) => { m[c] = { k: c, s: false }; });
    '1234567890'.split('').forEach((c) => { m[c] = { k: c, s: false }; });
    return m;
  })();

  let panel, rowsEl;
  const latched = new Set(); // 已锁定的修饰键（ctrl/alt/win）
  let shiftOn = false;       // Shift 单发状态
  let symbols = false;       // 符号层

  // send 把一个按键发给主机：有锁定修饰键时发组合键，用后清空全部锁定。
  function send(name, needsShift) {
    const keys = [];
    latched.forEach((m) => keys.push(m));
    if (needsShift || shiftOn) keys.push('shift');
    keys.push(name);
    const p = keys.length === 1
      ? Api.control('/api/control/key', { key: keys[0] })
      : Api.control('/api/control/keys', { keys });
    p.catch(window.__hctrlError || console.error);
    shiftOn = false;
    latched.clear();
    render();
  }

  function makeBtn(spec) {
    const b = document.createElement('button');
    b.className = 'kb-key' + (spec.wide ? ' wide' : '');
    b.textContent = spec.label || '';
    if (spec.type === 'mod') {
      b.classList.add('mod');
      if (latched.has(spec.name)) b.classList.add('on');
      b.addEventListener('click', () => {
        if (latched.has(spec.name)) latched.delete(spec.name);
        else latched.add(spec.name);
        render();
      });
    } else if (spec.type === 'shift') {
      b.classList.add('mod');
      if (shiftOn) b.classList.add('on');
      b.addEventListener('click', () => { shiftOn = !shiftOn; render(); });
    } else if (spec.type === 'sym') {
      b.classList.add('mod');
      if (symbols) b.classList.add('on');
      b.addEventListener('click', () => { symbols = !symbols; render(); });
    } else {
      b.addEventListener('click', () => {
        if (spec.type === 'glyph') {
          const gl = GLYPH[spec.g];
          send(gl.k, gl.s);
        } else {
          send(spec.name, false);
        }
      });
    }
    return b;
  }

  function render() {
    rowsEl.innerHTML = '';

    const r0 = document.createElement('div');
    r0.className = 'kb-row';
    MODS.forEach((m) => r0.appendChild(makeBtn(m)));
    rowsEl.appendChild(r0);

    const rows = symbols ? SYMBOL_ROWS : LETTER_ROWS;
    rows.forEach((row) => {
      const div = document.createElement('div');
      div.className = 'kb-row';
      row.forEach((item) => {
        if (typeof item === 'string') {
          const label = symbols ? item : (shiftOn ? item.toUpperCase() : item);
          div.appendChild(makeBtn({ label, type: 'glyph', g: item }));
        } else {
          div.appendChild(makeBtn(item));
        }
      });
      rowsEl.appendChild(div);
    });

    const rBottom = document.createElement('div');
    rBottom.className = 'kb-row';
    BOTTOM_ROW.forEach((s) => rBottom.appendChild(makeBtn(s)));
    rowsEl.appendChild(rBottom);
  }

  function close() {
    panel.classList.add('hidden');
    latched.clear();
    shiftOn = false;
    symbols = false;
  }

  function init() {
    panel = document.getElementById('keyboard-panel');
    rowsEl = document.getElementById('kb-rows');
    document.getElementById('btn-keyboard').addEventListener('click', () => {
      panel.classList.remove('hidden');
      render();
    });
    document.getElementById('btn-keyboard-close').addEventListener('click', close);
    panel.addEventListener('click', (ev) => {
      if (ev.target === panel) close(); // 点遮罩关闭
    });
  }

  return { init };
})();
