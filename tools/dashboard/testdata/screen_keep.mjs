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
// Зовётся из go-теста (board_test.go), путь к статике приходит аргументом.

import fs from "node:fs";
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
  addEventListener: () => {},
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

function boardBody() {
  return {
    board: {
      prefix: "XR",
      sections: [
        { key: "in-progress", title: "In progress", rows: [rows[0]] },
        { key: "backlog", title: "Backlog", rows: rows.slice(1) },
      ],
    },
    works: works(),
  };
}

function works() {
  return running ? [{ id: "XR-3", via: "tmux", title: "строка доски номер 3" }] : [];
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

const sandbox = {
  console: { log: () => {}, error: () => {}, warn: () => {} },
  setTimeout: () => 0,
  clearTimeout: () => {},
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
  location: { hash: "#demo", href: "", replace: () => {} },
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
      return reply({ message: "сессия поднята" });
    }
    if (path.endsWith("/board")) return reply(boardBody());
    if (path.endsWith("/drafts")) return reply({ drafts });
    if (path.includes("/drafts/")) return reply({ file: "docs/tasks/drafts/x.md", text: "текст записи" });
    if (path.includes("/sessions?task=")) {
      return reply({ sessions: [{ id: "abcdef1234567890", mtime: "2026-08-13T10:02:00+03:00", task: "XR-1" }] });
    }
    if (path.includes("/sessions/")) return reply({ items: talk });
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

// Нажатие кнопки: строка обновляется на месте, экран не уезжает, а фокус
// остаётся на той же строке.
const untouched = find(groups, "XR-4");
act.handlers.click({ stopPropagation: () => {} });
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
if (doc.activeElement.textContent !== "Стоп") {
  fail("после нажатия фокус ушёл со строки: " + dump(doc.activeElement));
}

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

// Чат: лента, поле ввода и поток событий переживают обновление, а пришедшая
// реплика не трогает соседних.
await go("#demo/chat/XR-1");
const thread = find(groups, "chat-thread-XR-1");
if (!thread) fail("экран чата не собрался: " + dump(groups).slice(0, 200));
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
if (!dump(find(groups, "chat-head")).includes("агент работает")) {
  fail("шапка чата не узнала о работе агента: " + dump(find(groups, "chat-head")));
}
if (doc.activeElement !== ta) fail("обновление шапки отобрало фокус у поля ввода");
if (wasWorks !== works) fail("стенд подменил источник работ мимо ручек");

console.log("частичная перерисовка: доска, черновики и лента чата держат место и фокус");
