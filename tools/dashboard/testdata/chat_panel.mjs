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

// Связь рвётся по этому флагу: мок fetch бросает исключение на отправке.
let offline = false;
// Задача, приехавшая ручкой привязки: по ней видно, что уехало с экрана.
let bound = null;

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
  node.append = (...kids) => {
    // Узел помнит родителя: без этого node.remove() был заглушкой, и снятый
    // выпадающий список оставался в дереве вторым.
    for (const kid of kids) {
      if (kid && typeof kid === "object") kid.parentNode = node;
    }
    node.children.push(...kids);
  };
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
  node.remove = () => {
    const p = node.parentNode;
    if (!p) return;
    const at = p.children.indexOf(node);
    if (at >= 0) p.children.splice(at, 1);
    node.parentNode = null;
  };
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
    // Слушатели окна тут не выброшены нарочно: очередь исходящих дожимает
    // неушедшее по событию online, и без записи слушателя это не проверить.
    listeners: {},
    addEventListener: (name, fn) => { (sandbox.window.listeners[name] ||= []).push(fn); },
    removeEventListener: (name, fn) => {
      const list = sandbox.window.listeners[name] || [];
      const at = list.indexOf(fn);
      if (at >= 0) list.splice(at, 1);
    },
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
    // Обрыв связи это не ответ со статусом, а исключение fetch: ровно так его
    // видно с телефона в авиарежиме, и очередь обязана его пережить.
    if (offline && init && init.method === "POST") {
      return Promise.reject(new Error("сети нет"));
    }
    if (path.includes("/sessions?task=")) {
      return reply(sessionNote ? { sessions, note: sessionNote } : { sessions });
    }
    if (path.includes("/sessions?free=1")) {
      return reply(freeNote ? { sessions: free, note: freeNote } : { sessions: free });
    }
    if (path.endsWith("/task") && init && init.method === "POST") {
      bound = JSON.parse(init.body).task;
      return reply({ message: "сессия привязана к " + bound });
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
        phase: "код", tool: "Bash", about: "SC=/private/tmp/claude-501/-Users-rider go build",
        scale: "закрыто 6 из 9", since: Math.floor(Date.now() / 1000) - 9,
        phases: [], agents: [],
        own: { session: mine.id, name: "chat-XR-1-1", state: "working", own: true,
          tool: "Bash", about: "SC=/private/tmp/claude-501/-Users-rider go build",
          since: Math.floor(Date.now() / 1000) - 9 } });
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

// Тумблер фильтра правит только выпадающий список: экран и панель от него не
// пересобираются. Прежде он звал общую перерисовку, и лента мигала и теряла
// место на переключении, хотя видимого менялся только состав списка.
{
  const head = sandbox.chatHead("demo", st);
  const line = byClass(head, "chline");
  const filt = labelBtn(head, "Список отфильтрован по XR-1: нажмите, чтобы видеть все чаты");
  if (!filt) fail("тумблера фильтра в шапке нет: " + dump(head));
  // Список открыт: в нём два чата задачи.
  sandbox.chatDropOpen("demo", st, line);
  const rows = (box) => {
    const out = [];
    const walk = (n) => {
      if (String(n.className || "").split(" ").includes("cdrow")) out.push(n);
      (n.children || []).forEach(walk);
    };
    walk(box);
    return out;
  };
  if (rows(line).length !== 2) fail("список открылся не отфильтрованным: " + rows(line).length);
  asked.length = 0;
  moves.length = 0;
  filt.handlers.click({ stopPropagation: () => {} });
  if (asked.length) fail("тумблер сходил на сервер за перерисовкой: " + JSON.stringify(asked));
  if (moves.length) fail("тумблер тронул адрес экрана: " + JSON.stringify(moves));
  if (byClass(head, "chline") !== line) fail("шапка пересобрана из-за тумблера");
  if (rows(line).length !== 3) {
    fail("список не пересобрался под снятый фильтр: " + rows(line).length);
  }
  if (String(filt.className).includes(" on")) fail("тумблер не сменил свой вид: " + filt.className);
  // Возврат: список снова только про задачу.
  filt.handlers.click({ stopPropagation: () => {} });
  if (rows(line).length !== 2) fail("возврат фильтра не сузил список: " + rows(line).length);
  sandbox.chatDropShut();
}

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
// Строки состояния под названием нет вовсе: имя инструмента и давность хода
// повторяли ленту, которая идёт прямо под шапкой, а живость и ожидание видны в
// кольце и его списке (замечание пользователя).
await settle();
if (byClass(head, "cts")) fail("строка состояния вернулась в шапку: " + dump(head));
if (byClass(head, "cmeta")) fail("приписка под заголовком вернулась: " + dump(head));
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

// --- подпись узнавания задачи: чужая доска не выдаётся за свою ---
// Стоит она подсказкой на значке привязки: плашка под заголовком снята вместе
// с контейнером, она занимала строку под то, что и так сказано значком.
{
  const was = chatRegistry;
  chatRegistry = () => [Object.assign({}, mine, { note: "задача не с доски проекта" })];
  st = await sandbox.chatState("demo", mine.id, board);
  const head = sandbox.chatHead("demo", st);
  if (byClass(head, "cnote")) fail("плашка подписи вернулась под заголовок: " + dump(head));
  if (byClass(head, "csub")) fail("контейнер под заголовком вернулся: " + dump(head));
  const bind = labelBtn(head, "Свободный чат (задача не с доски проекта): привязать к задаче рукой");
  if (!bind) fail("подписи про чужую доску нет на значке привязки: " + dump(head));
  chatRegistry = was;
}

// --- привязка разговора к задаче рукой ---
{
  st = await sandbox.chatState("demo", mine.id, board);
  const head = sandbox.chatHead("demo", st);
  const line = byClass(head, "chline");
  const bind = line.children.find((k) => String(k.className).includes("cdbtn") &&
    String(k.attrs && k.attrs.title || k.title || "").includes("привяз"));
  if (!bind) fail("кнопки привязки к задаче в шапке нет: " + dump(head));
  sandbox.chatDropShut();
  bind.handlers.click({ stopPropagation: () => {} });
  const menu = byClass(line, "cdbind");
  if (!menu) fail("окно привязки не открылось: " + dump(line));
  const field = tag(menu, "INPUT");
  if (!field) fail("поля номера задачи в окне привязки нет: " + dump(menu));
  field.value = "xr-7";
  posted.length = 0;
  button(menu, "Привязать").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!posted.some((path) => path.includes("/sessions/" + mine.id + "/task"))) {
    fail("привязка не позвала ручку задачи сессии: " + JSON.stringify(posted));
  }
  if (bound !== "XR-7") fail("номер задачи уехал не прописными: " + JSON.stringify(bound));
}

// --- очередь исходящих: неушедшее переживает перезагрузку и дожимается само ---
{
  chatRegistry = emptyReg;
  chatsNote = "";
  st = await sandbox.chatState("demo", mine.id, board);
  offline = true;
  posted.length = 0;
  const p1 = sandbox.chatPanel("demo", st);
  const ta1 = tag(p1, "TEXTAREA");
  ta1.value = "дожми это";
  button(p1, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!dump(p1).includes("не ушло, дожимаю")) {
    fail("неушедшая реплика не сказала про автодожим: " + dump(p1));
  }
  if (!dump(p1).includes("повторить")) {
    fail("кнопка ручного повтора пропала вместе с автодожимом: " + dump(p1));
  }
  const saved = store.get("devkit.chat.pend.demo/" + mine.id);
  if (!saved || !saved.includes("дожми это")) {
    fail("неушедшая реплика не легла в память браузера: " + saved);
  }

  // Перезагрузка страницы: панель собирается заново, и пузырь возвращается.
  sandbox.closeChatLive();
  const p2 = sandbox.chatPanel("demo", st);
  await settle();
  if (!dump(p2).includes("дожми это")) {
    fail("после перезагрузки неушедшая реплика потерялась: " + dump(p2));
  }

  // Связь вернулась: событие online дожимает очередь, ждать отсчёта не надо.
  offline = false;
  posted.length = 0;
  for (const fn of sandbox.window.listeners.online || []) fn();
  await settle();
  if (!posted.some((path) => path.includes("/chats/" + mine.id + "/say"))) {
    fail("по возвращении связи реплика не ушла сама: " + JSON.stringify(posted));
  }
  if (store.get("devkit.chat.pend.demo/" + mine.id)) {
    fail("ушедшая реплика осталась в очереди: " + store.get("devkit.chat.pend.demo/" + mine.id));
  }
}

console.log("подпись узнавания задачи: чужая доска названа словами в шапке");

console.log("привязка разговора к задаче: кнопка в шапке, поле номера, ручка сессии");

console.log("очередь исходящих панели: неушедшее говорит про дожим, переживает перезагрузку" +
  " и уходит само по возвращении связи");

console.log("список разговоров: состояние, модель и задачи видны, открытый отмечен, поиск" +
  " режет список, переключение заменяет адрес, пустота названа словами сервера");
console.log("панель разговора: адрес хвостом и старые ссылки живы, задачный адрес открывает" +
  " свежий разговор задачи, реплика уходит ручкой разговора, шапка называет разговор с задачей," +
  " ширина тянется хватом и помнится");

// --- стоп в строке отправки: только у своей работающей сессии ---
// Прерывать ход можно там, где есть чем: сессия живёт в нашей tmux. Окно
// vscode это чужой процесс, у мёртвой сессии хода нет вовсе.
{
  const base = await sandbox.chatState("demo", mine.id, board);
  const withEntry = (extra) => Object.assign({}, base,
    { entry: Object.assign({ id: mine.id, tasks: ["XR-1"] }, extra) });
  const busy = withEntry({ state: "live", tmux: "chat-XR-1-1", idle: false });
  const panel = sandbox.chatPanel("demo", busy);
  const stop = byClass(panel, "cstop");
  if (!stop) fail("у работающей сессии нет стопа в строке отправки: " + dump(panel).slice(0, 200));
  posted.length = 0;
  stop.handlers.click({ stopPropagation: () => {} });
  await settle();
  const hit = posted.filter((p) => p.includes("/stop"));
  if (hit.length !== 1 || !hit[0].includes("/chats/" + mine.id + "/stop")) {
    fail("стоп ушёл не ручкой прерывания: " + JSON.stringify(posted));
  }
  if (posted.some((p) => p.includes("kill") || p.includes("/runs/"))) {
    fail("стоп полез убивать сессию: " + JSON.stringify(posted));
  }
  // Ждущая реплики сессия хода не ведёт: прерывать нечего.
  if (byClass(sandbox.chatPanel("demo", withEntry({ state: "live", tmux: "chat-XR-1-1", idle: true })), "cstop")) {
    fail("стоп стоит у сессии, которая ждёт реплики");
  }
  // Чужое окно vscode и мёртвая сессия: клавиатуры отсюда нет.
  if (byClass(sandbox.chatPanel("demo", withEntry({ state: "vscode", idle: false })), "cstop")) {
    fail("стоп стоит у чужого окна vscode");
  }
  if (byClass(sandbox.chatPanel("demo", withEntry({ state: "dead", tmux: "chat-XR-1-1" })), "cstop")) {
    fail("стоп стоит у мёртвой сессии");
  }
  // Своей tmux у сессии нет: поднята она не нами, прерывать нечем.
  if (byClass(sandbox.chatPanel("demo", withEntry({ state: "live", idle: false })), "cstop")) {
    fail("стоп стоит у сессии без нашей tmux");
  }
}

console.log("стоп чата: кнопка у своей работающей сессии, ручка прерывания, сессия не убивается");

// --- отправка с телефона: Enter не шлёт недописанное ---
// Виртуальная клавиатура шлёт Enter тем же ключом, что и настольная, и реплика
// уезжала с полуслова. Устройство различается указателем, а не шириной окна.
{
  const st2 = await sandbox.chatState("demo", mine.id, board);
  const panelOf = () => sandbox.chatPanel("demo", st2);
  const keys = (panel) => tag(panel, "TEXTAREA").handlers.keydown;
  const press = (panel, ev) => {
    let stopped = false;
    keys(panel)(Object.assign({ key: "Enter", preventDefault: () => { stopped = true; } }, ev));
    return stopped;
  };
  // Палец: Enter это перевод строки, отправка только кнопкой.
  sandbox.window.matchMedia = () => ({ matches: true, addEventListener: () => {},
    removeEventListener: () => {} });
  const touch = panelOf();
  tag(touch, "TEXTAREA").value = "недописанная реплика";
  posted.length = 0;
  if (press(touch, {})) fail("на телефоне Enter перехвачен: строку не поставить");
  await settle();
  if (posted.some((p) => p.includes("/say"))) fail("на телефоне Enter отправил реплику");
  button(touch, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!posted.some((p) => p.includes("/say"))) fail("кнопка на телефоне не отправила реплику");
  // Мышь: Enter шлёт, Shift с Enter ставит строку, Cmd с Enter тоже шлёт.
  sandbox.window.matchMedia = () => ({ matches: false, addEventListener: () => {},
    removeEventListener: () => {} });
  const desk = panelOf();
  tag(desk, "TEXTAREA").value = "реплика со стола";
  posted.length = 0;
  if (!press(desk, {})) fail("на столе Enter не отправляет");
  await settle();
  if (!posted.some((p) => p.includes("/say"))) fail("на столе Enter не дошёл до ручки");
  posted.length = 0;
  tag(desk, "TEXTAREA").value = "вторая строка";
  if (press(desk, { shiftKey: true })) fail("Shift с Enter отправил вместо перевода строки");
  await settle();
  if (posted.some((p) => p.includes("/say"))) fail("Shift с Enter ушёл в ручку");
  if (!press(desk, { metaKey: true })) fail("Cmd с Enter не отправляет");
  await settle();
  if (!posted.some((p) => p.includes("/say"))) fail("Cmd с Enter не дошёл до ручки");
}

console.log("отправка: на телефоне Enter ставит строку и шлёт кнопка, на столе Enter и Cmd с Enter шлют");

// --- высота поля ввода живёт вместе с черновиком ---
// Растянутое поле переживает перезагрузку, пока в нём лежит ненаписанное, а
// отправка уносит и текст, и высоту: держать поле большим после неё незачем.
{
  const st3 = await sandbox.chatState("demo", mine.id, board);
  const grown = () => {
    const panel = sandbox.chatPanel("demo", st3);
    return { panel, ta: tag(panel, "TEXTAREA"), grip: byClass(panel, "tagrip") };
  };
  const first = grown();
  first.ta.value = "недописанная реплика";
  // Тяга вверх: поле выросло, и по концу тяги высота записана.
  first.ta.getBoundingClientRect = () => ({ height: 44 });
  first.grip.handlers.pointerdown({ button: 0, clientY: 300, pointerId: 1,
    preventDefault: () => {} });
  first.grip.handlers.pointermove({ buttons: 1, clientY: 200 });
  first.grip.handlers.pointerup({ clientY: 200 });
  const saved = store.get("devkit.chat.draft." + st3.addr + ".h");
  if (!saved) fail("высота поля не записалась по концу тяги");
  // Пересборка панели: высота вернулась вместе с черновиком.
  const again = grown();
  if (String(again.ta.style.height || "") !== saved + "px") {
    fail("высота не пережила пересборку: " + again.ta.style.height + ", ждал " + saved + "px");
  }
  // Отправка: и черновик, и высота сняты, поле вернулось к обычному росту.
  again.ta.value = "реплика";
  button(again.panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (store.get("devkit.chat.draft." + st3.addr + ".h")) {
    fail("после отправки высота осталась записанной");
  }
  if (again.ta.style.height) fail("после отправки поле осталось растянутым: " + again.ta.style.height);
}

console.log("поле ввода: высота живёт с черновиком и снимается отправкой");
