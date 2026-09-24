// UrsidoRescue GUI mockup: shared top bar, sidebar, log and demo states.
// Static demo data only; no network, no serial port. In the real Web-GUI the
// STOP label, IDs and port state come from the core (app.CancelState, the
// session and PortOwner) over SSE; here the page's data-* attributes stand in.
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
  const body = () => document.body.dataset;

  const BEAR = '<svg width="36" height="36" viewBox="0 0 256 256" aria-hidden="true">' +
    '<g fill="#c8873a"><circle cx="58" cy="72" r="32"/><circle cx="198" cy="72" r="32"/>' +
    '<path d="M128 40c52 0 84 32 84 82 0 54-36 92-84 92s-84-38-84-92c0-50 32-82 84-82z"/></g>' +
    '<g fill="#241610"><circle cx="58" cy="72" r="14"/><circle cx="198" cy="72" r="14"/>' +
    '<circle cx="98" cy="124" r="11"/><circle cx="158" cy="124" r="11"/></g>' +
    '<ellipse cx="128" cy="182" rx="48" ry="31" fill="#efc079"/>' +
    '<path d="M113 163h30a8 8 0 0 1 6 13l-13 12a6 6 0 0 1-8 0l-13-12a8 8 0 0 1 6-13z" fill="#241610"/></svg>';
  const STOPSQ = '<svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><rect x="2" y="2" width="12" height="12" rx="2" fill="currentColor"/></svg>';

  // ---------- toast ----------
  let toastTimer = null;
  function toast(text) {
    let t = $('#toast');
    if (!t) { t = el('div', { id: 'toast', class: 'toast', role: 'status' }); $('#top').append(t); }
    t.textContent = text;
    t.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { t.hidden = true; }, 4200);
  }

  // ---------- top bar ----------
  function topBar() {
    const t = $('#top');
    if (!t) return;
    const port = body().port || '';
    const probe = body().mode === 'probe';
    t.innerHTML =
      '<div class="brand">' + BEAR + '<div><b>UrsidoRescue</b><small>0.2.0 · LAB</small></div></div>' +
      '<button type="button" class="chip" id="portchip" aria-haspopup="true" aria-expanded="false">' +
      (port ? '<span class="dot"></span><span class="mono">' + port + '</span><span class="k">115200 8N1</span>'
            : '<span class="dot off"></span><span>Порт не выбран</span>') + '<span class="k">▾</span></button>' +
      (probe ? '<span class="modechip">PORTING COLLECTOR</span>' : '') +
      (port ? '<div class="chip meta"><span class="k">Профиль</span><b>XG-040G-MF · AN7583</b></div>' +
              '<div class="chip meta"><span class="k">NAND</span>Fudan FM25G02B</div>' : '') +
      '<div class="spacer"></div>' +
      '<div class="seg" role="group" aria-label="Язык"><button type="button" aria-pressed="true">RU</button><button type="button" aria-pressed="false">EN</button></div>' +
      '<button type="button" class="stop" id="stop"></button>';
    segToggle($('.seg', t));
    renderStop();
    $('#stop').addEventListener('click', pressStop);
    $('#portchip').addEventListener('click', togglePorts);
  }

  // STOP label = CancelState from the core (UI_SPEC §17). Never disabled.
  function renderStop() {
    const b = $('#stop');
    if (!b) return;
    const d = body();
    const mode = d.stopMode || 'now';
    const requested = d.stopRequested === 'true';
    b.dataset.mode = mode;
    b.dataset.requested = String(requested);
    let big = 'СТОП', small = 'дальше ничего не отправлять';
    if (requested) {
      big = 'ОСТАНОВКА ЗАПРОШЕНА';
      small = mode === 'checkpoint' ? 'остановлюсь ' + (d.stopNote || 'в безопасной точке') : 'до следующей команды';
    } else if (mode === 'checkpoint') {
      small = d.stopNote || 'в безопасной точке';
    } else if (mode === 'unavailable') {
      big = 'ОСТАНОВКА НЕДОСТУПНА';
      small = d.stopNote || 'шаг нельзя прерывать';
    }
    b.innerHTML = STOPSQ + '<span><b>' + big + '</b><small>' + small + '</small></span>';
    b.setAttribute('aria-label', big + ': ' + small);
  }
  function pressStop() {
    const d = body();
    if ((d.stopMode || 'now') === 'unavailable') {
      toast('Остановка сейчас недоступна: ' + (d.stopNote || 'шаг нельзя прерывать') +
        '. Шаг будет доведён до проверки; после него СТОП снова станет доступен.');
      return;
    }
    if (d.stopRequested === 'true') { toast('Остановка уже запрошена.'); return; }
    d.stopRequested = 'true';
    renderStop();
    toast(d.stopMode === 'checkpoint'
      ? 'Остановка запрошена: текущий шаг будет доведён до проверки, затем операция остановится (' + (d.stopNote || '') + ').'
      : 'Остановка запрошена: следующая команда не будет отправлена.');
  }

  // Port popover (UI_SPEC §9): list, "new" mark, lease state.
  function togglePorts() {
    const chip = $('#portchip');
    let pop = $('#portpop');
    if (pop) { pop.remove(); chip.setAttribute('aria-expanded', 'false'); return; }
    const current = body().port || '';
    const lease = body().lease || '';
    const port = (name, desc, isNew) => {
      const b = el('button', { type: 'button', class: 'port', 'aria-current': String(name === current) }, [
        el('span', {}, [el('span', { class: 'mono', text: name }), el('small', { text: desc })])]);
      if (isNew) b.append(el('span', { class: 'new', text: 'НОВЫЙ' }));
      b.addEventListener('click', () => {
        if (lease) { toast('Порт занят операцией ' + lease + ': сменить порт можно после её завершения.'); return; }
        body().port = name; pop.remove(); topBar(); toast('Подключено: ' + name);
      });
      return b;
    };
    pop = el('div', { id: 'portpop', class: 'pop', role: 'dialog', 'aria-label': 'Выбор порта' }, [
      el('h4', { text: 'UART-ПОРТЫ' }),
      port('COM4', 'Bluetooth-последовательный порт'),
      port('COM6', 'USB-SERIAL CH340 · 1A86:7523', true),
    ]);
    if (lease) pop.append(el('div', { class: 'lease', text: 'Порт занят операцией ' + lease + '. Отключить и сменить порт можно после её завершения.' }));
    const disc = el('button', { type: 'button', class: 'ghost', text: 'Отключить' });
    if (lease || !current) disc.setAttribute('aria-disabled', 'true');
    disc.addEventListener('click', () => {
      if (lease) { toast('Отключить нельзя: порт занят операцией ' + lease + '.'); return; }
      body().port = ''; pop.remove(); topBar();
    });
    pop.append(el('div', { class: 'row' }, [el('button', { type: 'button', class: 'ghost', text: 'Обновить' }), disc]));
    pop.style.left = chip.offsetLeft + 'px';
    $('#top').append(pop);
    chip.setAttribute('aria-expanded', 'true');
  }

  // ---------- sidebar ----------
  const MENU = [
    ['СПАСЕНИЕ', [['revive', 'Оживить «кирпич»', 'BootROM → RAM U-Boot', 'bootrom.html'],
      ['stock', 'Вернуть сток', 'mtd16 / бэкап MedveFlasher', 'main.html'],
      ['fip', 'Починить загрузку', 'UBI-том fip', 'main.html'],
      ['nand', 'Залить весь NAND', 'сырой образ 256 МиБ', 'main.html']]],
    ['ИНСТРУМЕНТЫ', [['itb', 'Загрузить ITB в RAM', 'initramfs, без записи', 'main.html'],
      ['diag', 'Диагностика', 'только чтение', 'main.html'],
      ['term', 'UART-терминал', 'история, XMODEM', 'terminal.html']]],
    ['РАЗВЕДКА', [['probe', 'Porting Collector', 'новое Airoha-железо', 'probe.html']]]
  ];
  const EXPERT = [['ex-uboot', 'RAM U-Boot и prompt', 'ручная работа'],
    ['ex-ubi', 'Записать UBI-том', 'существующий том'], ['ex-raw', 'Raw-запись MTD', 'bl2 / ubi']];

  function sideBar() {
    const s = $('#side');
    if (!s) return;
    const active = body().active ?? 'stock';
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
    const sess = body().session === undefined ? 'stock-restore-7f3c' : body().session;
    s.append(el('div', { class: 'sidefoot' }, [el('div', { text: 'Сессия' }),
      el('div', { class: 'mono', text: sess ? 'work/sessions/…-' + sess + '/' : 'ещё не начата' }),
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

  // ---------- IDs: short on screen, full on copy ----------
  function ids() {
    $$('.ids button[data-full]').forEach((b) => b.addEventListener('click', () => {
      const full = b.dataset.full;
      const done = () => toast('Скопировано: ' + full);
      if (navigator.clipboard) navigator.clipboard.writeText(full).then(done, done); else done();
    }));
  }

  // ---------- demo phase switches ----------
  // [data-demo] groups switch body data (stop mode/note) and show [data-phase] blocks.
  function demos() {
    $$('[data-demo]').forEach((g) => segToggle(g, (v) => {
      const b = $('button[data-v="' + v + '"]', g);
      body().stopMode = b.dataset.stop || 'now';
      body().stopNote = b.dataset.note || '';
      body().stopRequested = 'false';
      $$('[data-phase]').forEach((x) => { x.hidden = x.dataset.phase !== v; });
      renderStop();
    }));
    const want = new URLSearchParams(location.search).get('phase');
    const pick = want && $('[data-demo] button[data-v="' + want + '"]');
    if (pick) { pick.click(); return; }
    const first = $('[data-demo] button[aria-pressed=true]');
    if (first) $$('[data-phase]').forEach((x) => { x.hidden = x.dataset.phase !== first.dataset.v; });
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
    if (pause) {
      pause.addEventListener('click', () => setPaused(pause.getAttribute('aria-pressed') !== 'true'));
      jump.addEventListener('click', () => setPaused(false));
      box.addEventListener('wheel', (e) => { if (e.deltaY < 0) setPaused(true); }, { passive: true });
    }
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

  document.addEventListener('click', (e) => {
    const pop = $('#portpop');
    if (pop && !pop.contains(e.target) && !$('#portchip').contains(e.target)) {
      pop.remove(); $('#portchip').setAttribute('aria-expanded', 'false');
    }
  });

  document.addEventListener('DOMContentLoaded', () => {
    topBar(); sideBar(); logDock(); terminal(); confirmGate(); ids(); demos();
  });
})();
