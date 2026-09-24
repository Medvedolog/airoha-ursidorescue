// UrsidoRescue GUI mockup: shared top bar, sidebar and log behaviour.
// Static demo data only; no network, no serial port.
(function () {
  'use strict';
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
  const el = (tag, attrs = {}, kids = []) => {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (k === 'class') n.className = v;
      else if (k === 'text') n.textContent = v;
      else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v);
    }
    for (const c of [].concat(kids)) n.append(c);
    return n;
  };

  const BEAR = '<svg width="36" height="36" viewBox="0 0 256 256" aria-hidden="true">' +
    '<g fill="#c8873a"><circle cx="58" cy="72" r="32"/><circle cx="198" cy="72" r="32"/>' +
    '<path d="M128 40c52 0 84 32 84 82 0 54-36 92-84 92s-84-38-84-92c0-50 32-82 84-82z"/></g>' +
    '<g fill="#241610"><circle cx="58" cy="72" r="14"/><circle cx="198" cy="72" r="14"/>' +
    '<circle cx="98" cy="124" r="11"/><circle cx="158" cy="124" r="11"/></g>' +
    '<ellipse cx="128" cy="182" rx="48" ry="31" fill="#efc079"/>' +
    '<path d="M113 163h30a8 8 0 0 1 6 13l-13 12a6 6 0 0 1-8 0l-13-12a8 8 0 0 1 6-13z" fill="#241610"/></svg>';

  // ---------- top bar ----------
  function topBar() {
    const t = $('#top');
    if (!t) return;
    t.innerHTML =
      '<div class="brand">' + BEAR + '<div><b>UrsidoRescue</b><small>0.2.0-test17 · LAB</small></div></div>' +
      '<div class="chip"><span class="dot"></span><span class="mono">COM6</span><span class="k">115200 8N1</span></div>' +
      '<div class="chip"><span class="k">Профиль</span><b>XG-040G-MF · AN7583</b></div>' +
      '<div class="chip"><span class="k">NAND</span>Fudan FM25G02B</div>' +
      '<div class="spacer"></div>' +
      '<div class="seg" role="group" aria-label="Язык"><button type="button" aria-pressed="true">RU</button><button type="button" aria-pressed="false">EN</button></div>' +
      '<button type="button" class="stop" aria-label="Стоп: завершить безопасно, дальше ничего не писать">' +
      '<svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2" width="12" height="12" rx="2" fill="#fff"/></svg>СТОП</button>';
    segToggle($('.seg', t));
  }

  // ---------- sidebar ----------
  const MENU = [
    ['СПАСЕНИЕ', [['revive', 'Оживить «кирпич»', 'BootROM → RAM U-Boot', 'main.html'],
      ['stock', 'Вернуть сток', 'mtd16 / бэкап MedveFlasher', 'main.html'],
      ['fip', 'Починить загрузку', 'UBI-том fip', 'main.html'],
      ['nand', 'Залить весь NAND', 'сырой образ 256 МиБ', 'main.html']]],
    ['ИНСТРУМЕНТЫ', [['itb', 'Загрузить ITB в RAM', 'initramfs, без записи', 'main.html'],
      ['diag', 'Диагностика', 'только чтение', 'main.html'],
      ['term', 'UART-терминал', 'история, XMODEM', 'terminal.html']]],
    ['РАЗВЕДКА', [['probe', 'Porting Collector', 'новое Airoha-железо', 'main.html']]]
  ];
  const EXPERT = [['ex-uboot', 'RAM U-Boot и prompt', 'ручная работа'],
    ['ex-ubi', 'Записать UBI-том', 'существующий том'], ['ex-raw', 'Raw-запись MTD', 'bl2 / ubi']];

  function sideBar() {
    const s = $('#side');
    if (!s) return;
    const active = document.body.dataset.active || 'stock';
    const item = (id, label, hint, href, danger) => {
      const b = el('button', { type: 'button', class: 'item' + (danger ? ' danger' : '') },
        [el('span', { text: label }), el('small', { text: hint })]);
      if (id === active) b.setAttribute('aria-current', 'page');
      if (href) b.addEventListener('click', () => { location.href = href; });
      return b;
    };
    for (const [title, items] of MENU) {
      s.append(el('div', { class: 'grp' }, [el('h3', { text: title }),
        ...items.map(([id, l, h, href]) => item(id, l, h, href))]));
    }
    const exList = el('div', { class: 'grp', hidden: '' }, EXPERT.map(([id, l, h]) => item(id, l, h, null, true)));
    const exBtn = el('button', { type: 'button', class: 'expert', 'aria-expanded': 'false' },
      [el('span', { text: '⚠' }), el('span', { text: 'ЭКСПЕРТ', style: 'flex:1' }), el('span', { text: '▸' })]);
    exBtn.addEventListener('click', () => {
      const open = exList.hidden;
      exList.hidden = !open;
      exBtn.setAttribute('aria-expanded', String(open));
      exBtn.lastChild.textContent = open ? '▾' : '▸';
    });
    s.append(el('div', { class: 'grp' }, [exBtn, exList]));
    s.append(el('div', { class: 'sidefoot' }, [el('div', { text: 'Лог сессии' }),
      el('div', { class: 'mono', text: 'work/recovery-20260924-115802.uart.log' }),
      el('button', { type: 'button', class: 'ghost', text: 'Собрать пакет логов' })]));
  }

  // ---------- segmented toggles ----------
  function segToggle(group, onPick) {
    if (!group) return;
    $$('button', group).forEach((b) => b.addEventListener('click', () => {
      $$('button', group).forEach((x) => x.setAttribute('aria-pressed', String(x === b)));
      if (onPick) onPick(b.dataset.v);
    }));
  }

  // ---------- log dock ----------
  function logDock() {
    const dock = $('#log');
    if (!dock) return;
    const box = $('.lines', dock);
    const rows = $$('.ln', box);
    segToggle($('.filter', dock), (v) => {
      rows.forEach((r) => { r.hidden = !(v === 'all' || r.classList.contains(v)); });
      box.scrollTop = box.scrollHeight;
    });
    const pause = $('.pause', dock);
    const jump = $('.jump', dock);
    const setPaused = (p) => {
      pause.setAttribute('aria-pressed', String(p));
      pause.textContent = p ? 'Продолжить' : 'Пауза';
      jump.hidden = !p;
      if (!p) box.scrollTop = box.scrollHeight;
    };
    pause.addEventListener('click', () => setPaused(pause.getAttribute('aria-pressed') !== 'true'));
    jump.addEventListener('click', () => setPaused(false));
    box.addEventListener('wheel', (e) => { if (e.deltaY < 0) setPaused(true); }, { passive: true });
    const max = $('.maxi', dock);
    if (max) max.addEventListener('click', () => {
      const on = dock.classList.toggle('max');
      max.textContent = on ? '⤓' : '⤢';
      max.setAttribute('aria-label', on ? 'Свернуть лог' : 'Развернуть лог');
      box.scrollTop = box.scrollHeight;
    });
    box.scrollTop = box.scrollHeight;
  }

  // ---------- terminal ----------
  function terminal() {
    const hint = $('#termhint');
    if (!hint) return;
    segToggle($('.modes'), (v) => {
      hint.textContent = v === 'raw' ? 'клавиши уходят сразу · история устройства ↑/↓'
        : 'строка уходит по Enter · локальная история ↑/↓';
    });
    const pager = $('.pager');
    pager.addEventListener('click', () =>
      pager.setAttribute('aria-pressed', String(pager.getAttribute('aria-pressed') !== 'true')));
    const box = $('.term .lines');
    box.scrollTop = box.scrollHeight;
  }

  // ---------- confirm ----------
  function confirmGate() {
    const inp = $('#phrase');
    if (!inp) return;
    const go = $('#go');
    const want = inp.dataset.want;
    inp.addEventListener('input', () => {
      const v = inp.value;
      const ok = v === want;
      inp.classList.toggle('ok', ok);
      inp.classList.toggle('part', v.length > 0 && !ok);
      go.disabled = !ok;
    });
  }

  document.addEventListener('DOMContentLoaded', () => {
    topBar(); sideBar(); logDock(); terminal(); confirmGate();
  });
})();
