// Стенд панели разговора (DK-435, решения 5 и 6 LLD DK-430). Предмет проверки
// тут не написанное в исходнике, а разобранный адрес и собранная панель: по
// какому адресу открывается разговор, какой ручкой уходит реплика и когда
// гаснет поле ввода. Текстом статики такое не берётся: причина гашения
// складывается из ответа сервера, строки доски и наличия сессии, и ошибка тут
// стоит реплики, ушедшей мимо адресата.
//
// Зовётся из go-теста (sessions_test.go), путь к статике приходит аргументом.

import fs from "node:fs";
import vm from "node:vm";

const appPath = process.argv[2];
if (!appPath) {
  console.error("нужен путь до static/app.js");
  process.exit(2);
}

const fail = (msg) => { console.error(msg); process.exit(1); };

function makeNode(tag) {
  const node = {
    tagName: String(tag || "div").toUpperCase(),
    className: "",
    textContent: "",
    title: "",
    hidden: false,
    disabled: false,
    value: "",
    placeholder: "",
    scrollTop: 0,
    scrollHeight: 0,
    clientHeight: 0,
    children: [],
    attrs: {},
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
  node.append = (...kids) => { node.children.push(...kids); };
  node.appendChild = (kid) => { node.children.push(kid); return kid; };
  node.prepend = (...kids) => { node.children.unshift(...kids); };
  node.replaceChildren = (...kids) => { node.children = kids; };
  node.insertBefore = (kid, ref) => {
    const at = ref ? node.children.indexOf(ref) : -1;
    if (at < 0) node.children.push(kid);
    else node.children.splice(at, 0, kid);
    return kid;
  };
  node.removeChild = (kid) => {
    const at = node.children.indexOf(kid);
    if (at >= 0) node.children.splice(at, 1);
    return kid;
  };
  node.remove = () => {};
  node.setAttribute = (name, value) => { node.attrs[name] = String(value); };
  node.removeAttribute = (name) => { delete node.attrs[name]; };
  node.getBoundingClientRect = () => ({ top: 0, bottom: 0, height: 0, width: 0 });
  node.focus = () => {};
  node.scrollIntoView = () => {};
  node.addEventListener = (name, fn) => { node.handlers[name] = fn; };
  node.removeEventListener = () => {};
  node.querySelector = () => null;
  node.querySelectorAll = () => [];
  node.closest = () => null;
  Object.defineProperty(node, "childElementCount", { get: () => node.children.length });
  Object.defineProperty(node, "firstChild", { get: () => node.children[0] || null });
  return node;
}

function dump(node) {
  if (!node) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(dump)].join(" ");
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

// Кнопка по подписи доступности: у значков шапки текста нет вовсе, и найти их
// можно только так.
function labelBtn(node, label) {
  if (!node) return null;
  if (node.tagName === "BUTTON" && node.attrs && node.attrs["aria-label"] === label) return node;
  for (const kid of node.children || []) {
    const hit = labelBtn(kid, label);
    if (hit) return hit;
  }
  return null;
}

function byClass(node, cls) {
  if (!node) return null;
  if (String(node.className || "").split(" ").includes(cls)) return node;
  for (const kid of node.children || []) {
    const hit = byClass(kid, cls);
    if (hit) return hit;
  }
  return null;
}

function button(node, label) {
  if (!node) return null;
  if (node.tagName === "BUTTON" && node.textContent === label) return node;
  for (const kid of node.children || []) {
    const hit = button(kid, label);
    if (hit) return hit;
  }
  return null;
}

const byId = new Map();
// Ходы по истории: переключение разговора обязано заменять адрес, а не копить
// его, иначе «назад» после пяти разговоров это пять нажатий до доски.
const moves = [];
const store = new Map();
const asked = [];
const posted = [];

// Разговоры проекта: у задачи их два, и панель открывает свежий. Третий это
// разговор без узнанной задачи, он открывается по id сессии.
const mine = {
  id: "aaaa1111-1111", mtime: "2026-08-13T10:02:00+03:00", branch: "dk-435",
  first: "Выполни XR-1", task: "XR-1", taskNote: "по дереву задачи", bound: "lead",
  reply: "session", live: true, harness: "втораяtest",
};
const older = {
  id: "bbbb2222-2222", mtime: "2026-08-12T09:00:00+03:00", branch: "dk-435",
  first: "Верни XR-1 на доработку", task: "XR-1", taskNote: "по первой реплике", bound: "about",
  reply: "", live: false,
};
const loose = {
  id: "cccc3333-3333", mtime: "2026-08-13T10:04:00+03:00", branch: "main",
  first: "почини роутер, доступы в local-docs", taskNote: "задача не распознана",
  reply: "session", live: true,
};

let sessions = [mine, older];
let sessionNote = "";
// Разговоры проекта без задачи: ими живёт общий чат доски, своего экрана у него
// нет (решение 7).
let free = [loose];
let freeNote = "";
// Реестр разговоров проекта (ручка /chats): панель берёт список отсюда целиком
// и фильтрует его у себя, поэтому задачи у записи списком, а состояние словом.
function chatOf(s, extra) {
  return Object.assign({
    id: s.id, title: s.first, mtime: s.mtime, tasks: s.task ? [s.task] : [],
    state: s.live ? "live" : "dead", idle: true, tree: s.tree || "",
    branch: s.branch || "", model: "opus", harness: s.harness || "",
  }, extra || {});
}
let chatsNote = "";
function chatRegistry() {
  return [chatOf(mine), chatOf(older), chatOf(loose)];
}
const headExtra = { reply: "session", replyNote: "" };
const board = { sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "дашборд без дёрганья", sect: "in-progress" },
  { id: "XR-7", title: "Цель: панель разговора", sect: "in-progress" },
] }] };

function reply(body) {
  return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(body) });
}

const sandbox = {
  console: { log: () => {}, error: () => {}, warn: () => {} },
  setTimeout: () => 0,
  clearTimeout: () => {},
  setInterval: () => 0,
  clearInterval: () => {},
  Date,
  JSON,
  document: {
    createElement: makeNode,
      // Кольцо агентов рисуется svg, и узлы у него из своего пространства имён.
      createElementNS: (ns, tag) => makeNode(tag),
    createTextNode: (text) => {
      const n = makeNode("#text");
      n.textContent = String(text);
      return n;
    },
    getElementById: (id) => {
      if (!byId.has(id)) {
        const node = makeNode("div");
        node.content = { querySelector: () => null };
        byId.set(id, node);
      }
      return byId.get(id);
    },
    addEventListener: () => {},
    removeEventListener: () => {},
    body: makeNode("body"),
    documentElement: { style: { setProperty: () => {} } },
  },
  window: {
    addEventListener: () => {},
    removeEventListener: () => {},
    removeEventListener: () => {},
    innerWidth: 1400,
    matchMedia: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }),
    prompt: () => null,
  },
  location: { hash: "#demo", href: "", replace: () => {} },
  history: {
    pushState: (state, title, url) => { moves.push(["push", url]); },
    replaceState: (state, title, url) => { moves.push(["replace", url]); },
    back: () => { moves.push(["back", ""]); },
  },
  localStorage: {
    getItem: (key) => (store.has(key) ? store.get(key) : null),
    setItem: (key, value) => { store.set(key, String(value)); },
    removeItem: (key) => { store.delete(key); },
  },
  EventSource: class {
    constructor(url) {
      this.url = url;
      this.closed = false;
    }
    addEventListener() {}
    close() { this.closed = true; }
  },
  fetch: (path, init) => {
    asked.push(path);
    if (init && init.method === "POST") posted.push(path);
    if (path.includes("/sessions?task=")) {
      return reply(sessionNote ? { sessions, note: sessionNote } : { sessions });
    }
    if (path.includes("/sessions?free=1")) {
      return reply(freeNote ? { sessions: free, note: freeNote } : { sessions: free });
    }
    if (path.includes("/sessions/")) {
      const sid = path.slice(path.indexOf("/sessions/") + "/sessions/".length).split("?")[0];
      const found = [mine, older, loose].find((s) => s.id === sid) || { id: sid };
      return reply({ session: sid, head: Object.assign({}, found, headExtra), items: [] });
    }
    if (path.includes("/chats") && !(init && init.method === "POST")) {
      return reply(chatsNote ? { chats: chatRegistry(), note: chatsNote }
        : { chats: chatRegistry() });
    }
    if (path.includes("/pulse")) {
      return reply({ task: "XR-1", state: "working", flow: true, count: 1, quiet: 60,
        phase: "код", about: "Bash go build", since: Math.floor(Date.now() / 1000) - 9,
        phases: [], agents: [],
        own: { session: mine.id, name: "chat-XR-1-1", state: "working", own: true,
          about: "Bash go build", since: Math.floor(Date.now() / 1000) - 9 } });
    }
    if (path.endsWith("/board")) return reply({ board, works: [] });
    return reply({});
  },
};
sandbox.globalThis = sandbox;

vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: "app.js" });

const settle = async () => {
  for (let i = 0; i < 60; i += 1) await Promise.resolve();
};

// Разбор адреса: панель это хвост, и то, что под ней, разбирается как раньше.
// Старые адреса ложатся в ту же форму, иначе умрут посланные себе ссылки.
// Слова причин лежат в статике константами, а константы верхнего уровня в
// песочницу не выносятся: стенд повторяет их строками, и разойдись они, причина
// перестанет называться.
const OFF_TREE = "дерева сессии больше нет";
const OFF_OVER = "разговор кончился, и продолжить его некому";
const OFF_ARCHIVE = "задача закрыта, и живых сессий у неё нет";

const routes = [
  ["#demo", { proj: "demo", id: "", chat: undefined }],
  ["#demo/chat/XR-1", { proj: "demo", id: "", chat: "XR-1" }],
  ["#demo/XR-1/chat/" + mine.id, { proj: "demo", id: "XR-1", chat: mine.id }],
  ["#demo/agent/XR-1", { proj: "demo", id: "XR-1", chat: "XR-1" }],
  ["#demo/session/" + loose.id, { proj: "demo", id: "", chat: loose.id }],
  ["#demo/drafts/chat/XR-1", { proj: "demo", id: "", chat: "XR-1" }],
];
for (const [hash, want] of routes) {
  sandbox.location.hash = hash;
  const rt = sandbox.route();
  for (const [field, value] of Object.entries(want)) {
    if (rt[field] !== value) {
      fail("адрес " + hash + ": поле " + field + " разобрано как " +
        JSON.stringify(rt[field]) + ", ожидал " + JSON.stringify(value));
    }
  }
}
if (!sandbox.route().drafts) fail("хвост разговора съел экран под собой: накопитель потерялся");

// Задачный адрес разворачивается в последний разговор задачи, а сессионный
// открывается как есть.
sandbox.location.hash = "#demo";
let st = await sandbox.chatState("demo", "XR-1", board, []);
if (st.sid !== mine.id) fail("панель открыла не свежий разговор задачи: " + st.sid);
if (st.task !== "XR-1" || st.isGoal) fail("разбор адреса задачи соврал: " + JSON.stringify(st));
st = await sandbox.chatState("demo", loose.id, board, []);
if (st.sid !== loose.id || st.task) {
  fail("разговор без задачи разобран с задачей: " + JSON.stringify(st));
}

// Куда уйдёт реплика. Живой разговор пишет в саму сессию, кончившийся поднимает
// резюм, а новый чат рождает сессию первой репликой. Отказов с погашенным вводом
// тут больше нет: канал клиента достаёт любую живую сессию, а не нашедшему её
// серверу есть что поднять, и «писать некому» осталось бы отказом без причины.
st = await sandbox.chatState("demo", "XR-1", board);
let way = sandbox.chatWay(st);
if (way.kind !== "say" || way.off) {
  fail("живой разговор пишет не в сессию: " + JSON.stringify(way));
}
st = await sandbox.chatState("demo", older.id, board);
way = sandbox.chatWay(st);
if (way.kind !== "resume" || way.off) {
  fail("кончившийся разговор не поднимает резюм: " + JSON.stringify(way));
}
st = await sandbox.chatState("demo", "new:XR-1", board);
way = sandbox.chatWay(st);
if (way.kind !== "new" || !st.fresh || st.task !== "XR-1") {
  fail("новый чат задачи разобран иначе: " + JSON.stringify([way, st.fresh, st.task]));
}

// Список чатов фильтруется задачей адреса, и переключатель фильтра его
// расширяет: два чата задачи против всех трёх разговоров проекта.
st = await sandbox.chatState("demo", "XR-1", board);
sandbox.chatFilterSet(true);
if (sandbox.chatVisible(st).length !== 2) {
  fail("фильтр по задаче пропустил чужие чаты: " +
    JSON.stringify(sandbox.chatVisible(st).map((c) => c.id)));
}
sandbox.chatFilterSet(false);
if (sandbox.chatVisible(st).length !== 3) {
  fail("снятый фильтр не показал все чаты проекта: " +
    JSON.stringify(sandbox.chatVisible(st).map((c) => c.id)));
}
sandbox.chatFilterSet(true);

// Отправка живого разговора уходит ручкой самого разговора, а не задачи и не
// цели: до слияния экранов реплика цели ехала во «Входящие» файла цели.
posted.length = 0;
const panel = sandbox.chatPanel("demo", st);
const ta = tag(panel, "TEXTAREA");
if (!ta || ta.disabled) fail("живой разговор погасил поле ввода");
ta.value = "посмотри тесты";
button(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
await settle();
if (!posted.some((path) => path.includes("/chats/" + mine.id + "/say"))) {
  fail("реплика ушла не ручкой разговора: " + JSON.stringify(posted));
}

// Шапка панели: название разговора, номер задачи лейблом при нём, состояние
// подписью и крестик. Заголовок строки доски тут больше не при чём: шапка
// называет разговор, а не задачу, их у одной задачи бывает несколько.
const head = sandbox.chatHead("demo", st);
if (!dump(head).includes(mine.first)) {
  fail("шапка панели не названа разговором: " + dump(head));
}
if (!dump(byClass(head, "cdtask")).includes("XR-1")) {
  fail("номера задачи при названии разговора нет: " + dump(head));
}
// Состояние разговора говорит строка под названием, а собирает её пульс: слово
// «ждёт реплики» из метаданных снято, потому что кольцо рядом говорило то же
// самое, а у работающего агента ещё и спорило с ним.
await settle();
const cts = byClass(head, "cts");
if (!cts || !dump(cts).includes("Bash go build")) {
  fail("шапка не сказала состояние разговора словами: " + dump(head));
}
if (dump(byClass(head, "cmeta")).includes("ждёт реплики")) {
  fail("состояние разговора вернулось в метаданные: " + dump(head));
}
if (!labelBtn(head, "Закрыть панель")) fail("в шапке панели нет крестика: " + dump(head));
if (!labelBtn(head, "Новый чат")) fail("в шапке панели нечем завести новый чат: " + dump(head));

// Ширина панели помнится одним числом на весь дашборд и не выходит за пределы.
if (sandbox.chatWidth() !== 420) fail("ширина по умолчанию не та: " + sandbox.chatWidth());
store.set("devkit.chat.width", "1200");
if (sandbox.chatWidth() !== 640) fail("ширина не прижата к потолку: " + sandbox.chatWidth());
store.set("devkit.chat.width", "10");
if (sandbox.chatWidth() !== 320) fail("ширина не прижата к полу: " + sandbox.chatWidth());
sandbox.saveChatWidth(505);
if (store.get("devkit.chat.width") !== "505") {
  fail("ширина не запомнилась между заходами: " + store.get("devkit.chat.width"));
}
const grab = makeNode("i");
sandbox.wireChatGrab(grab);
grab.handlers.pointerdown({ pointerId: 1, preventDefault: () => {} });
grab.handlers.pointermove({ clientX: 900 });
grab.handlers.pointerup({ clientX: 900 });
if (store.get("devkit.chat.width") !== "500") {
  fail("хват не запомнил ширину по правому краю окна: " + store.get("devkit.chat.width"));
}

// Выбор разговора: список падает из шапки, открытый в нём отмечен, поиск режет
// его по названию, а переключение заменяет адрес, не копя историю. Ошибка тут
// стоит того, что два разговора одной задачи неразличимы и человек открывает
// не тот.
sandbox.location.hash = "#demo/XR-1/chat/" + mine.id;
st = await sandbox.chatState("demo", "XR-1", board);
const anchor = makeNode("div");
sandbox.chatDropOpen("demo", st, anchor);
const drop = byClass(anchor, "cdrop");
if (!drop) fail("список разговоров не открылся: " + dump(anchor));
const rows = [];
const walk = (node) => {
  if (String(node.className || "").split(" ").includes("cdrow")) rows.push(node);
  for (const kid of node.children || []) walk(kid);
};
walk(drop);
if (rows.length !== 2) fail("строк списка " + rows.length + ", ждал две");
if (!rows[0].className.includes("on") || rows[1].className.includes("on")) {
  fail("открытый разговор в списке не отмечен: " + rows.map((n) => n.className).join(" | "));
}
const shown = dump(drop);
for (const want of ["Выполни XR-1", "Верни XR-1 на доработку", "ждёт реплики", "процесса нет",
  "XR-1", "opus"]) {
  if (!shown.includes(want)) fail("в списке разговоров нет " + want + ": " + shown);
}
const search = tag(drop, "INPUT");
if (!search) fail("в списке разговоров нет поиска: " + shown);
search.value = "доработку";
search.handlers.input({});
const cut = [];
const walkCut = (node) => {
  if (String(node.className || "").split(" ").includes("cdrow")) cut.push(node);
  for (const kid of node.children || []) walkCut(kid);
};
walkCut(drop);
if (cut.length !== 1 || !dump(cut[0]).includes("Верни XR-1")) {
  fail("поиск по списку разговоров не сузил его: " + dump(drop));
}
moves.length = 0;
cut[0].handlers.click({ stopPropagation: () => {} });
if (moves.length !== 1 || moves[0][0] !== "replace") {
  fail("переключение разговора ходит по истории не заменой: " + JSON.stringify(moves));
}
if (!moves[0][1].includes("#demo/XR-1/chat/" + older.id)) {
  fail("переключение разговора сменило экран под панелью: " + moves[0][1]);
}

// Разговоров у проекта нет вовсе: слова сервера видны вместо пустой коробки.
const wasNote = chatsNote;
chatsNote = "разговоров в проекте demo нет: просмотрено 12 транскриптов";
const emptyReg = chatRegistry;
chatRegistry = () => [];
st = await sandbox.chatState("demo", "XR-404", board);
const emptyAnchor = makeNode("div");
sandbox.chatDropOpen("demo", st, emptyAnchor);
if (!dump(emptyAnchor).includes("просмотрено 12 транскриптов")) {
  fail("пустой список разговоров молчит вместо слов сервера: " + dump(emptyAnchor));
}
chatRegistry = emptyReg;
chatsNote = wasNote;

console.log("список разговоров: состояние, модель и задачи видны, открытый отмечен, поиск" +
  " режет список, переключение заменяет адрес, пустота названа словами сервера");
console.log("панель разговора: адрес хвостом и старые ссылки живы, задачный адрес открывает" +
  " свежий разговор задачи, реплика уходит ручкой разговора, шапка называет разговор с задачей," +
  " ширина тянется хватом и помнится");
