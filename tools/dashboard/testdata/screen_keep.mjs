// Стенд частичной перерисовки (DK-316). Предмет проверки это не написанное в
// исходнике, а то, что остаётся на экране после обновления: прокрутка списка,
// фокус на кнопке, раскрытая запись черновика, набранное в поле чата и живой
// поток событий. Проверкой текста статики такое не берётся, поэтому app.js
// поднимается в песочнице node с игрушечным DOM и игрушечным сервером.
//
// Игрушечный DOM повторяет от браузера ровно то, от чего страдал человек:
// опустевшая коробка сбрасывает прокрутку к нулю, а снятый с дерева узел
// теряет фокус. Пересобранный целиком экран на этом и валится, перерисованный
// по месту проходит.
//
// Тем же стендом проверяется поиск (DK-325): выдача занимает свой экран,
// набранное уезжает в адрес одним запросом по задержке, поле держит курсор
// между буквами, а вход в поиск есть и с клавиатуры, и с телефона.
//
// Им же проверяется выбор подписки в кнопке запуска (DK-326): широкая часть
// идёт на подписку по умолчанию, строка списка на свою, а там, где выбирать не
// из чего, стрелки нет и причина стоит подсказкой. Смотрит стенд тело
// запроса: нарисованный список без доехавшего имени это ровно та поломка, от
// которой задача и заведена.
//
// Последним шагом стенд смотрит ответ на нажатие: он приходит карточкой поверх
// экрана и не трогает ни одного узла из потока документа, поэтому раскладка от
// него не едет. Поток стенд берёт из настоящего index.html, а «поверх экрана»
// сверяет с настоящим style.css, чтобы проверка не выродилась в пересказ самой
// себя.
//
// Зовётся из go-теста (board_test.go), путь к статике приходит аргументом.

import fs from "node:fs";
import path from "node:path";
import vm from "node:vm";

const appPath = process.argv[2];
if (!appPath) {
  console.error("нужен путь до static/app.js");
  process.exit(2);
}

const fail = (msg) => { console.error(msg); process.exit(1); };

// Высота узла: у листа своя, у коробки сумма детских. По ней игрушечный DOM
// считает, сколько в списке можно прокрутить.
const LEAF = 40;
function height(node) {
  if (!node.children.length) return LEAF;
  return node.children.reduce((sum, kid) => sum + height(kid), 0);
}

function makeNode(tag) {
  const node = {
    tagName: String(tag || "div").toUpperCase(),
    className: "",
    textContent: "",
    title: "",
    href: "",
    hidden: false,
    disabled: false,
    value: "",
    placeholder: "",
    type: "",
    scrollTop: 0,
    scrollHeight: 0,
    clientHeight: 0,
    children: [],
    parentElement: null,
    style: {},
    dataset: {},
    handlers: {},
  };
  node.classList = {
    add: (...cls) => { node.className = (node.className + " " + cls.join(" ")).trim(); },
    remove: () => {},
    contains: (cls) => node.className.split(" ").includes(cls),
    toggle: () => {},
  };
  const detach = (kid) => {
    if (!kid || !kid.parentElement) return;
    const par = kid.parentElement;
    const at = par.children.indexOf(kid);
    if (at >= 0) par.children.splice(at, 1);
    kid.parentElement = null;
    blur(kid);
    reflow(par);
  };
  const adopt = (kid) => {
    detach(kid);
    kid.parentElement = node;
    return kid;
  };
  node.append = (...kids) => {
    for (const kid of kids) node.children.push(adopt(kid));
    reflow(node);
  };
  node.appendChild = (kid) => {
    node.children.push(adopt(kid));
    reflow(node);
    return kid;
  };
  node.prepend = (...kids) => {
    node.children.unshift(...kids.map(adopt));
    reflow(node);
  };
  node.replaceChildren = (...kids) => {
    for (const kid of node.children.slice()) detach(kid);
    node.children = kids.map(adopt);
    reflow(node);
  };
  node.insertBefore = (kid, ref) => {
    const at = ref ? node.children.indexOf(ref) : -1;
    adopt(kid);
    if (at < 0) node.children.push(kid);
    else node.children.splice(at, 0, kid);
    reflow(node);
    return kid;
  };
  node.removeChild = (kid) => { detach(kid); return kid; };
  node.remove = () => { detach(node); };
  node.after = () => {};
  node.setAttribute = () => {};
  node.removeAttribute = () => {};
  node.getBoundingClientRect = () => ({ top: 0, bottom: 0, height: 0, width: 0 });
  node.focus = () => { doc.activeElement = node; };
  node.scrollIntoView = () => {};
  node.addEventListener = (name, fn) => { node.handlers[name] = fn; };
  node.removeEventListener = () => {};
  node.querySelector = () => null;
  node.querySelectorAll = () => [];
  node.closest = () => null;
  node.cloneNode = () => makeNode(node.tagName);
  Object.defineProperty(node, "firstChild", {
    get: () => node.children[0] || null,
  });
  Object.defineProperty(node, "childElementCount", {
    get: () => node.children.length,
  });
  Object.defineProperty(node, "lastElementChild", {
    get: () => node.children[node.children.length - 1] || null,
  });
  if (node.tagName === "TEXTAREA" || node.tagName === "INPUT") {
    node.selectionStart = 0;
    node.selectionEnd = 0;
    node.setSelectionRange = (from, to) => {
      node.selectionStart = from;
      node.selectionEnd = to;
    };
  }
  return node;
}

// Опустевшая коробка сбрасывает прокрутку: браузер держит scrollTop в пределах
// содержимого, и пересобранный список уезжает к началу. Ради этого стенд и
// считает высоты.
function reflow(node) {
  node.scrollHeight = node.children.reduce((sum, kid) => sum + height(kid), 0);
  const max = Math.max(0, node.scrollHeight - node.clientHeight);
  if (node.scrollTop > max) node.scrollTop = max;
}

// Снятый с дерева узел теряет фокус: ровно так браузер и отбирает кнопку у
// пальца при полной пересборке экрана.
function blur(node) {
  if (!doc.activeElement) return;
  let cur = doc.activeElement;
  while (cur) {
    if (cur === node) {
      doc.activeElement = doc.body;
      return;
    }
    cur = cur.parentElement;
  }
}

const byId = new Map();
const doc = {
  createElement: makeNode,
  createTextNode: (text) => {
    const n = makeNode("#text");
    n.textContent = String(text);
    return n;
  },
  getElementById: (id) => {
    if (!byId.has(id)) {
      const node = makeNode("div");
      // Список прокручивается внутри себя (.groups в стилях), поэтому окно
      // стенда это его окно.
      if (id === "groups") node.clientHeight = 300;
      // Значки лежат в <template>, и берутся они из его content: в стенде
      // рисовать нечего, и запрос по нему ничего не находит.
      if (id === "icons") node.content = { querySelector: () => null };
      byId.set(id, node);
    }
    return byId.get(id);
  },
  // Обработчики документа складываются так же, как узловые: горячей клавише
  // («/» ставит курсор в поле поиска) больше неоткуда взяться.
  handlers: {},
  addEventListener: (name, fn) => { doc.handlers[name] = fn; },
};
doc.body = makeNode("body");
doc.activeElement = doc.body;

// Узел по ключу перерисовки: тем же способом его ищет и сама статика.
function find(root, key) {
  for (const kid of root.children || []) {
    if (kid.dataset && kid.dataset.pkey === key) return kid;
    const hit = find(kid, key);
    if (hit) return hit;
  }
  return null;
}

// Текст поддерева одной строкой: подписи лежат по разным узлам, а проверять
// удобнее целиком.
function dump(node) {
  if (!node) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(dump)].join(" ");
}

// Кнопка с такой подписью где-нибудь в поддереве.
function button(node, label) {
  if (!node) return null;
  if (node.tagName === "BUTTON" && node.textContent === label) return node;
  for (const kid of node.children || []) {
    const hit = button(kid, label);
    if (hit) return hit;
  }
  return null;
}

// Узел с таким классом где-нибудь в поддереве: списки разговоров ключей
// перерисовки не носят, и ищутся они классом.
function byClass(node, cls) {
  if (!node) return null;
  if (String(node.className || "").split(" ").includes(cls)) return node;
  for (const kid of node.children || []) {
    const hit = byClass(kid, cls);
    if (hit) return hit;
  }
  return null;
}

function tag(node, name) {
  if (!node) return null;
  if (node.tagName === name) return node;
  for (const kid of node.children || []) {
    const hit = tag(kid, name);
    if (hit) return hit;
  }
  return null;
}

const streams = [];
let running = false;

function row(id, title, extra) {
  return Object.assign({
    id,
    title,
    type: "task",
    p: "P2",
    cost: "M",
    r: 30,
    r_parts: [25, 3, 1, 0, 1],
    moved: "12 авг",
    after: [],
    notes: [],
  }, extra || {});
}

const rows = [row("XR-1", "Цель: дашборд без дёрганья", { type: "goal", cost: "XL" })];
for (let i = 2; i <= 9; i += 1) {
  rows.push(row("XR-" + i, "строка доски номер " + i));
}

// Признак идущей работы дописывает строкам сервер (boardRuns, tasks.go), и
// игрушечный сервер повторяет ровно это: у строки с живой работой это то, чем
// работа видна, у строки In progress без работы gone.
function marked(list, key) {
  return list.map((r) => {
    const live = works().find((w) => w.id === r.id);
    const run = live ? live.via : (key === "in-progress" ? "gone" : "");
    return Object.assign({}, r, run ? { run } : {});
  });
}

function boardBody() {
  return {
    board: {
      prefix: "XR",
      sections: [
        { key: "in-progress", title: "In progress", rows: marked([rows[0]], "in-progress") },
        { key: "backlog", title: "Backlog", rows: marked(rows.slice(1), "backlog") },
      ],
    },
    works: works(),
  };
}

function works() {
  return running ? [{ id: "XR-3", via: "tmux", title: "строка доски номер 3" }] : [];
}

// Подписки машины и то, чем их назвали при запуске: выбор в кнопке проверяется
// по телу запроса, а не по нарисованному, потому что ломается ровно оно.
// Имена выдуманные: их в клиенте нет ни одного, они приходят ответом ручки.
const harnessOne = { name: "подписка-раз", default: true, bin: "клиент-раз" };
const harnessTwo = { name: "подписка-два", default: false, bin: "клиент-два" };
let harnessList = [harnessOne, harnessTwo];
let harnessNote = "";
const started = [];

function harnessBody() {
  return { harnesses: harnessList, note: harnessNote };
}

const drafts = [
  { id: "XR-D1", title: "первая запись накопителя", age_words: "вчера" },
  { id: "XR-D2", title: "вторая запись накопителя", age_words: "сегодня" },
  { id: "XR-D3", title: "третья запись накопителя", age_words: "сегодня" },
];

const talk = [
  { seq: 1, role: "user", text: "как дела с витком", time: "2026-08-13T10:00:00+03:00" },
  { seq: 2, role: "assistant", text: "виток идёт, задачи режу", time: "2026-08-13T10:01:00+03:00" },
  { seq: 3, role: "assistant", text: "нарезал три штуки", time: "2026-08-13T10:02:00+03:00" },
];

function reply(body) {
  return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(body) });
}

// Медленный ответ: обещание, разрешающееся через десяток оборотов очереди
// микрозадач. Им ловится щель между запросом списка сессий и подъёмом потока
// ленты: уход с экрана попадает как раз в неё.
function slowReply(body) {
  let p = Promise.resolve();
  for (let i = 0; i < 20; i += 1) p = p.then(() => {});
  return p.then(() => ({ ok: true, status: 200, json: () => Promise.resolve(body) }));
}

// Разговоры задачи: у одной задачи их бывает несколько (взяли, спросили из
// соседнего окна, доделали другим деревом), и список стоит по времени
// последней записи, а не по выбору человека.
const mine = {
  id: "aaaa1111-1111", mtime: "2026-08-13T10:02:00+03:00", branch: "main",
  first: "Выполни XR-1", task: "XR-1", taskNote: "по первой реплике",
};
const alien = {
  id: "bbbb2222-2222", mtime: "2026-08-13T09:30:00+03:00", branch: "main", tree: "xr-1",
  first: "А какой агент делает XR-1?", task: "XR-1", taskNote: "по дереву задачи",
};
let sessions = [{ id: "abcdef1234567890", mtime: "2026-08-13T10:02:00+03:00", task: "XR-1" }];
let slowSessions = false;

// Поиск: сервер отвечает четырьмя группами в постоянном порядке (DK-325).
// Игрушечный сервер повторяет его контракт, а не выдумывает свой: доска,
// накопитель, архив и найденное в тексте файлов задач. Архивная строка едет с
// датой закрытия и без ранга с ценой, их в архиве нет.
const archived = [{ id: "XR-90", title: "Колокольчик в шапке", closed: "2026-08-01" }];
const searchAsked = [];
let slowSearch = false;

function searchReply(q) {
  const needle = String(q).trim().toLowerCase();
  const groups = [
    { key: "board", title: "Доска", rows: [] },
    { key: "drafts", title: "Черновики", rows: [] },
    { key: "archive", title: "Архив", rows: [] },
    { key: "text", title: "В тексте задач", rows: [] },
  ];
  if (needle.length < 2) return { project: "demo", q, groups, note: "Ждём двух символов." };
  const hit = (text) => String(text).toLowerCase().includes(needle);
  groups[0].rows = rows.filter((r) => hit(r.title) || hit(r.id))
    .map((r) => ({ id: r.id, title: r.title, sect: "backlog", section: "Backlog",
      type: r.type, p: r.p, cost: r.cost, r: r.r, r_parts: r.r_parts }));
  groups[1].rows = drafts.filter((d) => hit(d.title))
    .map((d) => ({ id: d.id, title: d.title, age_words: d.age_words }));
  groups[2].rows = archived.filter((a) => hit(a.title) || hit(a.id))
    .map((a) => ({ id: a.id, title: a.title, closed: a.closed, where: "архив" }));
  if (hit("цитата найденной строки")) {
    groups[3].rows = [{ id: "XR-7", title: "строка доски номер 7",
      file: "docs/tasks/XR-7.md", line: 12, quote: "цитата найденной строки файла" }];
  }
  const found = groups.reduce((sum, g) => sum + g.rows.length, 0);
  const resp = { project: "demo", q, groups };
  if (!found) {
    resp.note = "По запросу ничего нет. Ищем по номеру, заголовку и тексту файлов: " +
      "живая доска, черновики, архив.";
  }
  return resp;
}

// Игрушечные таймеры: карточка ответа гаснет по времени, и ждать его
// по-честному стенду нечем. Заказанное складывается в список, а срабатывает по
// команде стенда.
const timers = [];
let timerSeq = 0;

const sandbox = {
  console: { log: () => {}, error: () => {}, warn: () => {} },
  setTimeout: (fn, ms) => {
    timerSeq += 1;
    timers.push({ id: timerSeq, fn, ms });
    return timerSeq;
  },
  clearTimeout: (id) => {
    const at = timers.findIndex((t) => t.id === id);
    if (at >= 0) timers.splice(at, 1);
  },
  setInterval: () => 0,
  clearInterval: () => {},
  document: doc,
  window: {
    listeners: {},
    addEventListener: (name, fn) => { (sandbox.window.listeners[name] ||= []).push(fn); },
    removeEventListener: () => {},
    innerWidth: 1200,
    matchMedia: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }),
  },
  // Замена адреса без записи в историю: ею уезжает в адрес набранный запрос
  // поиска, и экран после неё собирается тем же путём, что и по ссылке.
  location: { hash: "#demo", href: "", replace: (hash) => { sandbox.location.hash = hash; } },
  localStorage: {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
  },
  EventSource: class {
    constructor(url) {
      this.url = url;
      this.listeners = {};
      streams.push(this);
    }
    addEventListener(name, fn) { this.listeners[name] = fn; }
    close() { this.closed = true; }
  },
  fetch: (path, init) => {
    const post = Boolean(init && init.method === "POST");
    if (path === "/api/projects") {
      return reply({ projects: [{ name: "demo", works: works(), sections: { check: 1 } }] });
    }
    if (path.endsWith("/runs") && post) {
      running = true;
      started.push(init && init.body ? JSON.parse(init.body) : {});
      return reply({ message: "сессия поднята" });
    }
    if (path === "/api/harnesses") return reply(harnessBody());
    if (path.endsWith("/board")) return reply(boardBody());
    if (path.endsWith("/drafts")) return reply({ drafts });
    if (path.includes("/drafts/")) return reply({ file: "docs/tasks/drafts/x.md", text: "текст записи" });
    if (path.includes("/sessions?task=")) {
      return slowSessions ? slowReply({ sessions }) : reply({ sessions });
    }
    if (path.includes("/sessions/")) return reply({ items: talk });
    if (path.includes("/search?q=")) {
      const q = decodeURIComponent(path.slice(path.indexOf("?q=") + 3));
      searchAsked.push(q);
      const answer = searchReply(q);
      if (slowSearch) {
        slowSearch = false;
        return slowReply(answer);
      }
      return reply(answer);
    }
    if (path === "/api/notifications") return reply({ items: [] });
    if (path === "/api/quota") return reply({ buckets: [] });
    return reply({});
  },
};
sandbox.globalThis = sandbox;

vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: "app.js" });

// Дать отработать цепочкам промисов: настоящего ожидания в стенде нет, всё
// решается за несколько оборотов очереди микрозадач.
const settle = async () => {
  for (let i = 0; i < 200; i += 1) await Promise.resolve();
};

const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

const groups = byId.get("groups");
await settle();

// Доска: прокрутка и фокус на кнопке переживают обновление по фокусу окна.
await go("#demo");
const card = find(groups, "card-backlog");
if (!card) fail("доска не собралась: карточки Backlog на экране нет");
const third = find(groups, "XR-3");
if (!third) fail("строки XR-3 на доске нет: " + dump(groups).slice(0, 200));
const act = button(third, "Выполнить");
if (!act) fail("у строки нет кнопки действия: " + dump(third));
groups.scrollTop = 240;
act.focus();

await sandbox.refresh();
await settle();

if (groups.scrollTop !== 240) {
  fail("обновление по фокусу окна сбило прокрутку списка: " + groups.scrollTop + " вместо 240");
}
if (doc.activeElement !== act) {
  fail("обновление отобрало фокус у кнопки строки: " + dump(doc.activeElement));
}
if (find(groups, "XR-3") !== third) {
  fail("строка без изменений пересобрана заново: узел уехал из-под пальца");
}
if (find(groups, "card-backlog") !== card) {
  fail("карточка секции пересобрана заново, хотя строки в ней те же");
}

// Строка в работе, за которой не стоит живой сессии, помечена этим, а не
// выглядит очередью: до DK-317 оборванный конвейер был неотличим от штатного
// ожидания.
const goalRow = find(groups, "XR-1");
if (!dump(goalRow).includes("сессии нет")) {
  fail("строка в работе без живой сессии ничем не помечена: " + dump(goalRow));
}

// Нажатие кнопки: строка обновляется на месте, экран не уезжает, а фокус
// остаётся на той же строке.
const untouched = find(groups, "XR-4");
act.handlers.click({ stopPropagation: () => {} });
if (!act.disabled) {
  fail("нажатая кнопка осталась живой: второе нажатие уйдёт вторым запуском");
}
await settle();

if (groups.scrollTop !== 240) {
  fail("нажатие кнопки сдвинуло экран: прокрутка " + groups.scrollTop + " вместо 240");
}
if (find(groups, "XR-4") !== untouched) {
  fail("нажатие на одной строке пересобрало соседнюю");
}
const now = find(groups, "XR-3");
if (!button(now, "Стоп")) {
  fail("строка не узнала о поднятой работе: " + dump(now));
}
if (!dump(now).includes("работает")) {
  fail("у идущей работы в строке нет признака выполнения: " + dump(now));
}
if (doc.activeElement.textContent !== "Стоп") {
  fail("после нажатия фокус ушёл со строки: " + dump(doc.activeElement));
}

// Выбор подписки в самой кнопке запуска (DK-326). Широкая часть поднимает
// работу на подписке по умолчанию, узкая открывает список, а строка списка
// запускает работу на своей подписке без второго нажатия. Проверяется тело
// запроса: нарисованный список без доехавшего имени это ровно та поломка,
// ради которой задача и заведена.
if (started[started.length - 1].harness !== harnessOne.name) {
  fail("широкая часть кнопки ушла не на подписку по умолчанию: " +
    JSON.stringify(started[started.length - 1]));
}
running = false;
await sandbox.refresh();
await settle();
const pickRow = find(groups, "XR-5");
const grp = byClass(pickRow, "rungrp");
if (!grp) fail("у строки нет составной кнопки запуска: " + dump(pickRow));
const menu = byClass(grp, "hmenu");
if (!menu || !menu.hidden) fail("список подписок открыт до нажатия на стрелку");
grp.children[1].handlers.click({ stopPropagation: () => {} });
if (menu.hidden) fail("стрелка не открыла список подписок");
if (menu.children.length !== 2) {
  fail("в списке подписок " + menu.children.length + " строк, ждал две: " + dump(menu));
}
if (!dump(menu).includes(harnessTwo.name) || !dump(menu).includes("по умолчанию")) {
  fail("список подписок не называет ни имён, ни подписки по умолчанию: " + dump(menu));
}
menu.children[1].handlers.click({ stopPropagation: () => {} });
await settle();
if (started[started.length - 1].harness !== harnessTwo.name) {
  fail("строка списка подняла работу не на своей подписке: " +
    JSON.stringify(started[started.length - 1]));
}
if (!menu.hidden) fail("список подписок остался открытым после выбора");

// Подписка на машине одна: стрелки нет вовсе, а причина висит подсказкой.
// Список не прочитан: то же самое, и запуск идёт как до этой задачи, без имени.
running = false;
for (const [list, note, why] of [
  [[harnessOne], "", "подписка на машине одна"],
  [[], "agentctl не нашёлся", "agentctl не нашёлся"],
]) {
  harnessList = list;
  harnessNote = note;
  await sandbox.refresh();
  await settle();
  const one = find(groups, "XR-6");
  if (byClass(one, "harrow")) fail("при одной подписке в строке осталась стрелка выбора: " + dump(one));
  const only = button(one, "Выполнить");
  if (!only) fail("строка осталась без кнопки запуска: " + dump(one));
  if (!String(only.title).includes(why)) {
    fail("причина отсутствия выбора не названа: " + JSON.stringify(only.title) + ", ждал про " + why);
  }
  only.handlers.click({ stopPropagation: () => {} });
  await settle();
  const sent = started[started.length - 1];
  if (list.length && sent.harness !== harnessOne.name) {
    fail("единственная подписка не доехала до запроса: " + JSON.stringify(sent));
  }
  if (!list.length && sent.harness) {
    fail("непрочитанный список всё равно назвал подписку: " + JSON.stringify(sent));
  }
  running = false;
}
harnessList = [harnessOne, harnessTwo];
harnessNote = "";
await sandbox.refresh();
await settle();

// Работа кончилась сама, и уведомитель сказал об этом: строка перечитывается
// событием, а не фокусом окна. Без этого смена статуса доезжала до списка
// только после ухода из окна и обратно.
running = false;
const bell = streams.find((s) => s.url.includes("/api/notifications"));
if (!bell) fail("поток уведомлений не поднят: перечитывать доску событием нечем");
bell.onmessage({
  data: JSON.stringify({ time: "2099-01-01T00:00:00", project: "demo", kind: "stop", id: "XR-3" }),
});
await settle();
const back = find(groups, "XR-3");
if (!button(back, "Выполнить")) {
  fail("событие уведомителя не перечитало строку: " + dump(back));
}
if (groups.scrollTop !== 240) {
  fail("перечитывание по событию сбило прокрутку: " + groups.scrollTop + " вместо 240");
}
// Карточка самого события гаснет по своему таймеру: дальше стенд смотрит в том
// же контейнере ответы на нажатие.
for (const t of timers.splice(0)) t.fn();

// Черновики: раскрытая запись и прокрутка переживают обновление.
running = false;
await go("#demo/drafts");
const wrap = find(groups, "XR-D2");
if (!wrap) fail("накопитель не собрался: записи XR-D2 нет");
wrap.children[0].handlers.click({ target: makeNode("span") });
await settle();
if (wrap.children[1].hidden) fail("текст записи не раскрылся по нажатию");
groups.scrollTop = 120;

await sandbox.refresh();
await settle();

if (find(groups, "XR-D2") !== wrap) {
  fail("список черновиков пересобран целиком: раскрытая запись закрылась");
}
if (wrap.children[1].hidden) fail("обновление закрыло раскрытую запись");
if (groups.scrollTop !== 120) {
  fail("обновление накопителя сбило прокрутку: " + groups.scrollTop + " вместо 120");
}

// Поиск (DK-325). Выдача занимает свой экран по адресу "#проект/find/<запрос>":
// запрос в адресе делает её ссылкой и переживает кнопку «назад». Группы стоят
// в постоянном порядке, совпадения подсвечены, архивная строка ранга и цены не
// выдумывает, а поле переживает набор буквы за буквой.
await go("#demo/find/" + encodeURIComponent("колокольчик"));
if (!find(groups, "find-q")) {
  fail("на экране выдачи нет поля запроса: " + dump(groups).slice(0, 200));
}
if (byId.get("hq").value !== "колокольчик") {
  fail("поле шапки не показывает запрос, с которым открыт экран: " + byId.get("hq").value);
}
const archCard = find(groups, "find-card-archive");
if (!archCard) fail("группы «Архив» на экране выдачи нет: " + dump(groups).slice(0, 300));
if (!dump(find(groups, "find-head-archive")).includes("Архив")) {
  fail("группа выдачи не подписана: " + dump(find(groups, "find-head-archive")));
}
if (!dump(archCard).includes("закрыта 2026-08-01")) {
  fail("архивная строка не несёт даты закрытия: " + dump(archCard));
}
if (byClass(archCard, "rank")) {
  fail("архивная строка показывает ранг, которого в архиве нет: " + dump(archCard));
}
const lit = byClass(archCard, "hit");
if (!lit || lit.textContent !== "Колокольчик") {
  fail("совпадение в заголовке не подсвечено: " + dump(archCard));
}
// Ненайденное и найденное различимы: сервер говорит, где искал, а клиент это
// показывает, а не молчит пустой карточкой.
await go("#demo/find/" + encodeURIComponent("тарабарщина"));
if (!dump(groups).includes("Ищем по номеру, заголовку и тексту файлов")) {
  fail("пустая выдача не говорит, где искали: " + dump(groups));
}
if (find(groups, "find-card-archive")) fail("по пустой выдаче нарисована группа архива");

// Строка доски ведёт на экран задачи, а найденное в тексте едет цитатой с
// путём файла.
await go("#demo/find/" + encodeURIComponent("доски номер 3"));
const boardCard = find(groups, "find-card-board");
if (!boardCard) fail("группы «Доска» на экране выдачи нет: " + dump(groups).slice(0, 300));
if (!byClass(boardCard, "rank")) {
  fail("строка доски в выдаче потеряла ранг: " + dump(boardCard));
}
boardCard.children[0].handlers.click();
if (sandbox.location.hash !== "demo/XR-3") {
  fail("строка выдачи не ведёт на экран задачи: " + sandbox.location.hash);
}
await go("#demo/find/" + encodeURIComponent("цитата найденной строки"));
const textCard = find(groups, "find-card-text");
if (!textCard) fail("группы «В тексте задач» нет: " + dump(groups).slice(0, 300));
for (const want of ["файла", "docs/tasks/XR-7.md:12", "строка доски номер 7"]) {
  if (!dump(textCard).includes(want)) {
    fail("в строке текстовой группы нет " + JSON.stringify(want) + ": " + dump(textCard));
  }
}
// Совпадение подсвечено и в цитате, а не только в заголовке.
const quoted = byClass(textCard, "fquote");
if (!quoted || !byClass(quoted, "hit")) {
  fail("совпадение в цитате не подсвечено: " + dump(textCard));
}

// Набор в поле шапки: буквы копятся, запрос уходит один раз по задержке, а
// экран выдачи собирается тем же путём, что и по ссылке.
await go("#demo");
const hq = byId.get("hq");
timers.length = 0;
const askedWas = searchAsked.length;
hq.value = "коло";
hq.handlers.input();
hq.value = "колокольчик";
hq.handlers.input();
if (searchAsked.length !== askedWas) {
  fail("каждая буква ушла своим запросом: " + JSON.stringify(searchAsked.slice(askedWas)));
}
for (const t of timers.splice(0)) t.fn();
if (sandbox.location.hash !== "#demo/find/" + encodeURIComponent("колокольчик")) {
  fail("набор в поле шапки не увёл на экран выдачи: " + sandbox.location.hash);
}
await sandbox.refresh();
await settle();
if (searchAsked.length !== askedWas + 1) {
  fail("на один набор ушло запросов: " + (searchAsked.length - askedWas));
}
if (!find(groups, "find-card-archive")) {
  fail("выдача по набранному запросу не собралась: " + dump(groups).slice(0, 300));
}

// Поле экрана переживает набор: пересобранное на каждой букве, оно теряло бы
// курсор вместе с набранным.
const screenQ = find(groups, "find-q");
const screenInput = screenQ.children[1];
screenInput.focus();
screenInput.value = "колокольчик в";
screenInput.handlers.input();
for (const t of timers.splice(0)) t.fn();
await sandbox.refresh();
await settle();
if (find(groups, "find-q") !== screenQ) fail("набор пересобрал поле запроса на экране выдачи");
if (doc.activeElement !== screenInput || screenInput.value !== "колокольчик в") {
  fail("набор отобрал у поля фокус или стёр набранное: " + screenInput.value);
}

// Запоздавший ответ не рисуется поверх свежего: пока сервер отвечал на прежний
// запрос, человек дописал слово, и чужие строки на экране выглядели бы выдачей.
slowSearch = true;
sandbox.location.hash = "#demo/find/" + encodeURIComponent("колокольчик");
const stale = sandbox.refresh();
sandbox.location.hash = "#demo/find/" + encodeURIComponent("цитата найденной строки");
await sandbox.refresh();
await stale;
await settle();
if (!find(groups, "find-card-text") || find(groups, "find-card-archive")) {
  fail("запоздавший ответ нарисован поверх свежей выдачи: " + dump(groups).slice(0, 300));
}

// Косая черта ставит курсор в поле шапки: руки на клавиатуре, и тянуться мышью
// ради поиска не надо.
doc.body.focus();
if (!doc.handlers.keydown) fail("горячей клавиши поиска нет: документ не слушает клавиатуру");
doc.handlers.keydown({ key: "/", preventDefault: () => {} });
if (doc.activeElement !== hq) {
  fail("косая черта не поставила курсор в поле поиска: " + dump(doc.activeElement));
}
// В поле ввода косая черта остаётся косой чертой.
screenInput.focus();
doc.handlers.keydown({ key: "/", preventDefault: () => {} });
if (doc.activeElement !== screenInput) {
  fail("косая черта в поле ввода перебросила курсор в шапку");
}

// Телефон: поля в шапке нет, и в поиск ведёт лупа рядом с колокольчиком. Экран
// открывается пустым запросом, и поле на нём сразу ждёт набора.
await go("#demo");
byId.get("find-btn").handlers.click();
if (sandbox.location.hash !== "demo/find/") {
  fail("лупа в шапке не открыла экран поиска: " + sandbox.location.hash);
}
await sandbox.refresh();
await settle();
const phoneQ = find(groups, "find-q");
if (!phoneQ) fail("экран поиска с телефона не собрался: " + dump(groups).slice(0, 300));
if (doc.activeElement !== phoneQ.children[1]) {
  fail("поле поиска на телефоне не ждёт набора: курсора в нём нет");
}
if (!dump(groups).includes("Ждём двух символов")) {
  fail("пустой запрос не говорит, чего ждёт: " + dump(groups).slice(0, 300));
}
// Поле на экране выдачи это телефонный узел: на ноутбуке его гасят стили, и
// стенд читает это в самом style.css, а не пересказывает себя.
const findCSS = fs.readFileSync(path.join(path.dirname(appPath), "style.css"), "utf8");
if (!/\.fqbar\{[^}]*display:none/.test(findCSS)) {
  fail("поле экрана выдачи не спрятано на ноутбуке: в шапке уже стоит своё");
}
if (!/\.hfbtn\{display:none\}/.test(findCSS) || !/\.hfbtn\{display:flex\}/.test(findCSS)) {
  fail("лупа шапки не отдана телефону: на ноутбуке её место занимает поле");
}

// Чат: лента, поле ввода и поток событий переживают обновление, а пришедшая
// реплика не трогает соседних.
await go("#demo/chat/XR-1");
const thread = find(groups, "chat-thread-XR-1");
if (!thread) fail("экран чата не собрался: " + dump(groups).slice(0, 200));
// Цикл цели не идёт: чат говорит это словами и держит ту же ручку подъёма
// витка, что кнопка в ленте (DK-319). Молчаливое «ждёт витка» здесь и было
// бедой: человек писал завершившемуся агенту и ждал ответа.
const note = find(thread, "chat-note");
if (!dump(note).includes("Цикл цели не идёт")) {
  fail("чат молчит о стоящем цикле: " + dump(note));
}
const raise = button(note, "Поднять виток");
if (!raise || raise.hidden) fail("при стоящем цикле в чате нет ручки подъёма витка");
raise.handlers.click({ stopPropagation: () => {} });
await settle();
if (!running) fail("кнопка «Поднять виток» не позвала ручку запуска работы");
running = false;

const feed = thread.children[0];
const list = feed.children[1];
const first = find(list, "seq-1");
if (!first) fail("лента чата пуста: " + dump(feed));
const ta = tag(thread, "TEXTAREA");
if (!ta) fail("в чате нет поля ввода");
ta.value = "набранный ответ";
ta.selectionStart = 7;
ta.selectionEnd = 7;
ta.focus();
const opened = streams.length;

await sandbox.refresh();
await settle();

if (find(groups, "chat-thread-XR-1") !== thread) {
  fail("обновление пересобрало разговор целиком");
}
if (tag(thread, "TEXTAREA") !== ta || ta.value !== "набранный ответ") {
  fail("обновление стёрло набранное в поле ввода: " + ta.value);
}
if (doc.activeElement !== ta) fail("обновление отобрало фокус у поля ввода чата");
if (streams.length !== opened) {
  fail("обновление переоткрыло поток событий: было " + opened + ", стало " + streams.length);
}
if (find(list, "seq-1") !== first) fail("обновление пересобрало прежние реплики ленты");

// Приход события: дописывается одна реплика, прежние стоят нетронутыми.
const es = streams[streams.length - 1];
es.onmessage({
  data: JSON.stringify({ seq: 4, role: "assistant", text: "и ещё одна", time: "2026-08-13T10:03:00+03:00" }),
});
await settle();
if (find(list, "seq-1") !== first) fail("приход реплики пересобрал ленту целиком");
if (!find(list, "seq-4")) fail("пришедшая реплика не встала в ленту");
if (doc.activeElement !== ta) fail("приход реплики отобрал фокус у поля ввода");

// Работа агента поднялась: в шапке появляется фишка, кнопка стопа зажигается,
// а разговор остаётся тем же.
running = true;
rows[0].id = "XR-1";
const chatWork = [{ id: "XR-1", via: "tmux", title: "Цель: дашборд без дёрганья" }];
const wasWorks = works;
sandbox.fetch = ((prev) => (path, init) => {
  if (path === "/api/projects") return reply({ projects: [{ name: "demo", works: chatWork }] });
  if (path.endsWith("/board")) {
    return reply({ board: boardBody().board, works: chatWork });
  }
  return prev(path, init);
})(sandbox.fetch);

await sandbox.refresh();
await settle();

if (find(groups, "chat-thread-XR-1") !== thread) {
  fail("поднявшаяся работа пересобрала разговор");
}
const stop = find(thread, "chat-stop");
if (!stop || stop.hidden) fail("кнопка стопа не появилась при работающем агенте");
if (raise !== button(find(thread, "chat-note"), "Поднять виток") || !raise.hidden) {
  fail("плашка чата не узнала о поднявшейся работе: ручка подъёма витка осталась на месте");
}
if (!dump(find(thread, "chat-note")).includes("Сообщение уйдёт агенту")) {
  fail("при работающем агенте плашка чата не обещает доставку витку: " + dump(find(thread, "chat-note")));
}
if (!dump(find(groups, "chat-head")).includes("агент работает")) {
  fail("шапка чата не узнала о работе агента: " + dump(find(groups, "chat-head")));
}
if (doc.activeElement !== ta) fail("обновление шапки отобрало фокус у поля ввода");
if (wasWorks !== works) fail("стенд подменил источник работ мимо ручек");

// Цикл цели ведёт не только tmux-сессия дашборда: цель из реестра и живое окно
// человека для сервера та же живая работа (goalIdle в messages.go), и плашка
// обязана говорить то же, что ответит ручка сообщения. Разойдись признаки,
// экран показывал бы «Цикл цели не идёт» там, где ручка обещает виток
// (замечание ревью DK-319).
for (const via of ["registry", "session"]) {
  chatWork[0].via = via;
  await sandbox.refresh();
  await settle();
  const shown = find(thread, "chat-note");
  if (dump(shown).includes("Цикл цели не идёт")) {
    fail("плашка чата зовёт стоящим цикл, который ведёт " + via + ": " + dump(shown));
  }
  if (!dump(shown).includes("Сообщение уйдёт агенту")) {
    fail("плашка чата при работе via " + via + " не обещает доставку витку: " + dump(shown));
  }
  const up = button(shown, "Поднять виток");
  if (!up || !up.hidden) fail("при работе via " + via + " в чате осталась кнопка подъёма витка");
  const kill = find(thread, "chat-stop");
  if (!kill || !kill.hidden) fail("кнопка стопа зажглась у работы via " + via + ", которую стоп не берёт");
}
// Признак возвращается к tmux-сессии: дальше стенд идёт разделом экрана
// агента, писанным поверх работающей tmux-сессии.
chatWork[0].via = "tmux";

// Экран агента: у задачи два разговора, и в одну ленту они не смешиваются
// (DK-290). Экран перечитывается по фокусу окна, а список сессий стоит по
// времени последней записи: соседняя работа, дописавшая свой транскрипт,
// выходит наверх, и собранная заново лента открывалась по нулевому элементу,
// то есть меняла разговор под читающим.
const talkStreams = () => streams.filter((s) => String(s.url).includes("/sessions/"));
const liveTalks = () => talkStreams().filter((s) => !s.closed);
const talkReply = (es, seq, text) => {
  es.onmessage({ data: JSON.stringify({ seq, role: "assistant", text, time: "2026-08-13T10:05:00+03:00" }) });
};

sessions = [mine, alien];
await go("#demo/agent/XR-1");
const panes = find(groups, "agent-panes-XR-1");
if (!panes) fail("экран агента не собрался: " + dump(groups).slice(0, 300));
const seg = byClass(panes, "tseg");
if (!seg || seg.children.length !== 2) {
  fail("разговоров задачи на экране агента не видно списком: " + dump(panes).slice(0, 300));
}
// Подпись разговора: время последней записи, дерево или ветка, первая реплика.
// Время берётся тем же переводом, что и на экране, иначе стенд держал бы пояс
// машины, на которой его гоняют.
for (const want of [sandbox.localTime(alien.mtime), alien.tree, alien.first, mine.first]) {
  if (!dump(seg).includes(want)) {
    fail("в списке разговоров нет подписи " + JSON.stringify(want) + ": " + dump(seg));
  }
}
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(mine.id)) {
  fail("открыт не свежий разговор: " + JSON.stringify(liveTalks().map((s) => s.url)));
}
const ours = liveTalks()[0];
talkReply(ours, 1, "ход первого разговора");
await settle();
if (!dump(panes).includes("ход первого разговора")) {
  fail("реплика открытого разговора не встала в ленту: " + dump(panes).slice(0, 300));
}

// Соседняя работа дописала транскрипт и вышла в списке наверх, а окно вернуло
// себе фокус.
sessions = [alien, mine];
await sandbox.refresh();
await settle();

if (find(groups, "agent-panes-XR-1") !== panes) {
  fail("обновление по фокусу окна пересобрало панели экрана агента");
}
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(mine.id)) {
  fail("обновление сменило открытый разговор на соседний: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}
if (!dump(panes).includes(mine.id.slice(0, 8))) {
  fail("подпись ленты не называет открытый разговор: " + dump(panes).slice(0, 300));
}
if (!dump(panes).includes("ход первого разговора")) {
  fail("обновление собрало ленту заново: прежние реплики пропали");
}
talkReply(ours, 2, "ещё реплика первого");
await settle();
if (!dump(panes).includes("ещё реплика первого")) {
  fail("после обновления лента дописывается мимо экрана");
}

// Переключение на соседний разговор: лента собирается заново им одним,
// дострение прежнего снимается, и реплики двух разговоров рядом не стоят.
seg.children[1].handlers.click();
await settle();
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(alien.id)) {
  fail("переключатель не открыл соседний разговор: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}
talkReply(liveTalks()[0], 1, "ход соседнего разговора");
await settle();
if (!dump(panes).includes("ход соседнего разговора")) {
  fail("реплика соседнего разговора не встала в ленту после переключения");
}
if (dump(panes).includes("ход первого разговора")) {
  fail("лента склеила разговоры: реплики прежнего остались под репликами соседнего");
}

// Уход с экрана и возврат: список опять стоит свежим разговором сверху, а
// открывается выбранный человеком.
sessions = [mine, alien];
await go("#demo");
await go("#demo/agent/XR-1");
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(alien.id)) {
  fail("возврат на экран открыл не выбранный разговор: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}

// Уход с экрана на середине запроса за сессиями: запоздавший ответ поднимал
// поток уже после того, как остальные закрыли, и закрывать его было некому.
slowSessions = true;
await go("#demo");
sandbox.location.hash = "#demo/agent/XR-1";
await sandbox.refresh();
sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();
if (liveTalks().length) {
  fail("уход с экрана оставил поток разговора живым: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}

// Вкладка по умолчанию на телефоне (DK-305): у живой работы это «Журнал», а
// у законченной «Лог витка». Журнал законченной работы пуст, а разговор ради
// него и приходят смотреть на узкий экран: второе касание экрану больше не
// нужно.
slowSessions = false;
sessions = [mine, alien];
await go("#demo/agent/XR-1");
let livePanes = find(groups, "agent-panes-XR-1");
if (!livePanes) fail("экран агента живой работы не собрался");
let tabSeg = byClass(livePanes, "seg");
if (!tabSeg) fail("на экране агента нет переключателя вкладок телефона");
if (tabSeg.children[0].className !== "on" || tabSeg.children[1].className === "on") {
  fail("у живой работы вкладкой по умолчанию встал не «Журнал»: " + dump(tabSeg));
}
let grid = livePanes.children[1];
if (!String(grid.children[0].className).includes("onpane")) {
  fail("у живой работы панель «Журнал» не открыта вкладкой: " + grid.children[0].className);
}

// Обновление ленты не перебивает ручной выбор человека: панели собираются
// один раз на заход и обновлением по фокусу окна не пересобираются.
tabSeg.children[1].handlers.click();
await settle();
if (tabSeg.children[1].className !== "on" || tabSeg.children[0].className === "on") {
  fail("нажатие на вкладку «Лог витка» её не выбрало: " + dump(tabSeg));
}
await sandbox.refresh();
await settle();
if (byClass(find(groups, "agent-panes-XR-1"), "seg").children[1].className !== "on") {
  fail("обновление ленты сбросило выбранную человеком вкладку");
}

// Работа кончилась: тот же id больше не встречается среди works, экран агента
// открывается вкладкой «Лог витка» по умолчанию.
const wasChatId = chatWork[0].id;
chatWork[0].id = "XR-9";
await go("#demo");
await go("#demo/agent/XR-1");
chatWork[0].id = wasChatId;
const donePanes = find(groups, "agent-panes-XR-1");
if (!donePanes) fail("экран агента законченной работы не собрался");
tabSeg = byClass(donePanes, "seg");
if (tabSeg.children[1].className !== "on" || tabSeg.children[0].className === "on") {
  fail("у законченной работы вкладкой по умолчанию встал не «Лог витка»: " + dump(tabSeg));
}
grid = donePanes.children[1];
if (!String(grid.children[1].className).includes("onpane")) {
  fail("у законченной работы панель «Лог витка» не открыта вкладкой: " + grid.children[1].className);
}

// Ответ на нажатие: он приходит карточкой поверх экрана и не двигает
// раскладку. Строкой над списком он стоял в потоке документа, и появление слов
// («конвейер задачи DK-136 поднят в tmux-сессии task-DK-136») уводило доску
// вниз на свою высоту: человек жал кнопку, а экран уезжал из-под пальца
// (приёмка DK-316). Стенд берёт поток из настоящего index.html, а не из своих
// представлений о нём: узел ответа лежал ровно там.
const staticDir = path.dirname(appPath);
const html = fs.readFileSync(path.join(staticDir, "index.html"), "utf8");
const css = fs.readFileSync(path.join(staticDir, "style.css"), "utf8");
const main = /<main class="bmain">([\s\S]*?)<\/main>/.exec(html);
if (!main) fail("в index.html нет блока <main class=\"bmain\">: поток экрана стенду не найти");
const flowIds = [...main[1].matchAll(/id="([^"]+)"/g)].map((m) => m[1]);
if (!flowIds.includes("groups")) fail("в потоке экрана нет списка #groups: разметка разъехалась со стендом");

// Контейнер карточек вынесен из потока стилями, и стенд читает это в самом
// style.css: без такой сверки проверка пересказывала бы саму себя.
if (!/\.flashes\{[^}]*position:fixed/.test(css)) {
  fail("контейнер .flashes стоит в потоке документа: карточка ответа будет двигать экран");
}

await go("#demo");
timers.length = 0;
const flashes = byId.get("flashes");
const flowWas = flowIds.map((id) => id + "=" + dump(byId.get(id)));
const said = "конвейер задачи DK-136 поднят в tmux-сессии task-DK-136";
sandbox.sayResult(said);

const flowNow = flowIds.map((id) => id + "=" + dump(byId.get(id)));
for (let i = 0; i < flowIds.length; i += 1) {
  if (flowWas[i] !== flowNow[i]) {
    fail("ответ на нажатие тронул узел из потока документа (#" + flowIds[i] +
      "): раскладка от него едет");
  }
}
if (flashes.children.length !== 1 || !dump(flashes).includes(said)) {
  fail("ответ на нажатие не всплыл карточкой поверх экрана: " + dump(flashes));
}

// Слова о начале сменяются словами об исходе, а не копятся столбиком.
sandbox.sayResult("стоп DK-136...");
if (flashes.children.length !== 1 || dump(flashes).includes(said)) {
  fail("ответы на нажатия копятся столбиком: " + dump(flashes));
}

// Удача гаснет сама.
const fireAll = () => {
  for (const t of timers.splice(0)) t.fn();
};
fireAll();
if (flashes.children.length) fail("карточка удачи не погасла по таймеру: " + dump(flashes));

// Отказ ждёт крестика: причину человек читает, а не ловит.
sandbox.sayResult("замок занят: сессия task-DK-136 уже поднята", true);
fireAll();
if (flashes.children.length !== 1) fail("карточка отказа погасла сама: причину не прочитать");
const refusal = flashes.children[0];
if (!String(refusal.className).includes("err")) {
  fail("отказ ничем не отличается от удачи: " + refusal.className);
}
const cross = tag(refusal, "BUTTON");
if (!cross) fail("на карточке отказа нет крестика: снять её нечем");
cross.handlers.click({ stopPropagation: () => {} });
if (flashes.children.length) fail("крестик не снял карточку отказа: " + dump(flashes));

// Уход с экрана снимает сказанное: слова о прежнем нажатии на новом экране
// висели бы без повода.
sandbox.sayResult("запуск DK-136...");
sandbox.sayResult("");
if (flashes.children.length) fail("пустой ответ не снял карточку: " + dump(flashes));

console.log("частичная перерисовка: доска, черновики и лента чата держат место и фокус, " +
  "экран агента держит выбранный разговор, ответ на нажатие не двигает раскладку; " +
  "поиск: выдача своим экраном, набор одним запросом, поле держит курсор, " +
  "косая черта и лупа ведут в поиск; подписки: широкая часть кнопки идёт на " +
  "подписку по умолчанию, строка списка на свою, без выбора стрелки нет");
