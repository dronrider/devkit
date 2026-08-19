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

// Ручка реплики: живой разговор пишет адресно своей ручкой, кончившийся с живой
// задачей уходит ручкой задачи, цель остаётся на своей.
const ways = [];
st = await sandbox.chatState("demo", "XR-1", board, []);
let way = sandbox.chatWay("demo", st);
ways.push(way);
if (!way.url.includes("/sessions/" + mine.id + "/message") || way.off) {
  fail("живой разговор пишет не ручкой сессии: " + JSON.stringify(way));
}
headExtra.reply = "task";
headExtra.replyNote = "сессию поднимал дашборд, а tmux-сессии task-XR-1 в списке уже нет: " +
  "реплика уйдёт задаче XR-1, её возьмёт тот, кто её продолжит";
st = await sandbox.chatState("demo", "XR-1", board, []);
way = sandbox.chatWay("demo", st);
if (!way.url.includes("/tasks/XR-1/message") || way.off) {
  fail("кончившийся разговор с живой задачей не берёт ручку задачи: " + JSON.stringify(way));
}
if (!way.why.includes("её возьмёт тот, кто её продолжит")) {
  fail("смена ручки не названа словами: " + way.why);
}
st = await sandbox.chatState("demo", "XR-7", board, []);
way = sandbox.chatWay("demo", st);
if (!way.goal || !way.url.includes("/goals/XR-7/message")) {
  fail("реплика цели уходит не ручкой цели: " + JSON.stringify(way));
}
if (way.off) fail("панель отказывает цели вместо того, чтобы назвать её ручку");

// Четыре названные причины: дерева сессии нет, разговор ведёт цель, задача
// закрыта без живых сессий, разговор кончился без задачи за ним.
headExtra.reply = "";
headExtra.replyNote = "дерева сессии больше нет: разговор кончился, и продолжить его некому";
st = await sandbox.chatState("demo", loose.id, board, []);
way = sandbox.chatWay("demo", st);
if (!way.off || way.cause !== OFF_TREE) {
  fail("снесённое дерево сессии не гасит ввод названной причиной: " + JSON.stringify(way));
}
headExtra.replyNote = "задача XR-9 припаркована вопросом: разговор кончился, и продолжить его некому";
st = await sandbox.chatState("demo", loose.id, board, []);
way = sandbox.chatWay("demo", st);
if (!way.off || way.cause !== OFF_OVER || !way.bind) {
  fail("кончившийся разговор без задачи не даёт дороги привязкой: " + JSON.stringify(way));
}
headExtra.reply = "session";
headExtra.replyNote = "";
sessions = [];
sessionNote = "сессий задачи XR-404 нет: просмотрены все 200 транскриптов проекта";
st = await sandbox.chatState("demo", "XR-404", board, []);
way = sandbox.chatWay("demo", st);
if (!way.off || way.cause !== OFF_ARCHIVE) {
  fail("закрытая задача без сессий не названа архивом: " + JSON.stringify(way));
}

// Живая задача без единого разговора ввода не гасит: строка ляжет во вход
// задачи и дождётся первого хода.
st = await sandbox.chatState("demo", "XR-1", board, []);
way = sandbox.chatWay("demo", st);
if (way.off || !way.url.includes("/tasks/XR-1/message")) {
  fail("задача без разговора гасит ввод, хотя реплику ей класть есть куда: " + JSON.stringify(way));
}
// Слова сервера о пустоте видны в панели, а не проглочены молчанием.
const emptyPanel = sandbox.chatPanel("demo", st);
if (!dump(emptyPanel).includes("просмотрены все 200 транскриптов")) {
  fail("пустой разговор молчит вместо слов сервера: " + dump(emptyPanel));
}
sessions = [mine, older];
sessionNote = "";

// Погашенный ввод: поле, кнопка отправки и слова причины стоят вместе.
headExtra.reply = "";
headExtra.replyNote = "дерева сессии больше нет: разговор кончился, и продолжить его некому";
st = await sandbox.chatState("demo", loose.id, board, []);
const offPanel = sandbox.chatPanel("demo", st);
const offTa = tag(offPanel, "TEXTAREA");
if (!offTa || !offTa.disabled) fail("погашенный разговор оставил поле ввода живым");
if (offTa.placeholder !== "писать некому") {
  fail("погашенное поле молчит о том, что писать некому: " + offTa.placeholder);
}
if (!button(offPanel, "Отправить").disabled) fail("кнопка отправки жива у погашенного ввода");
if (!dump(offPanel).toLowerCase().includes("дерева сессии больше нет")) {
  fail("причина гашения не названа словами: " + dump(offPanel));
}
headExtra.reply = "session";
headExtra.replyNote = "";

// Отправка живого разговора уходит ручкой сессии, а не цели.
st = await sandbox.chatState("demo", "XR-1", board, []);
const panel = sandbox.chatPanel("demo", st);
const ta = tag(panel, "TEXTAREA");
if (!ta || ta.disabled) fail("живой разговор погасил поле ввода");
ta.value = "посмотри тесты";
button(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
await settle();
if (!posted.some((p) => p.includes("/sessions/" + mine.id + "/message"))) {
  fail("реплика ушла не ручкой разговора: " + JSON.stringify(posted));
}

// Шапка панели: заголовок со строки доски, подпись с разрядом привязки и
// перепривязка рукой.
const head = sandbox.chatHead("demo", st);
if (!dump(head).includes("дашборд без дёрганья")) {
  fail("шапка панели не названа заголовком задачи: " + dump(head));
}
if (!dump(head).includes("ведёт XR-1")) {
  fail("шапка панели не называет разряд привязки: " + dump(head));
}
if (!button(head, "Отвязать")) fail("у ведущего разговора нет отвязки в шапке: " + dump(head));

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

// Список разговоров при панели (DK-436): время, подписка, состояние и разряд
// привязки. Ошибка тут стоит того, что два разговора одной задачи в списке
// неразличимы, а человек открывает не тот.
sandbox.location.hash = "#demo/XR-1/chat/XR-1";
st = await sandbox.chatState("demo", "XR-1", board, []);
const listBox = sandbox.chatList("demo", st);
const listText = dump(listBox);
for (const want of ["Разговоры XR-1", "идёт", "ведёт XR-1", "кончился", "говорит о XR-1",
  "втораяtest", "Выполни XR-1"]) {
  if (!listText.includes(want)) fail("в списке разговоров нет " + want + ": " + listText);
}
// Открытый разговор отмечен в списке: без отметки не видно, который из двух
// сейчас в ленте.
const marked = [];
const walk = (node) => {
  if ((node.className || "").includes("crow3")) marked.push(node);
  for (const kid of node.children || []) walk(kid);
};
walk(listBox);
if (marked.length !== 2) fail("строк списка " + marked.length + ", ждал две");
if (!marked[0].className.includes("on") || marked[1].className.includes("on")) {
  fail("открытый разговор в списке не отмечен: " + marked.map((n) => n.className).join(" | "));
}
// Переключение разговора остаётся на том же экране и не копит историю.
moves.length = 0;
marked[1].handlers.click({ stopPropagation: () => {} });
if (moves.length !== 1 || moves[0][0] !== "replace") {
  fail("переключение разговора ходит по истории не заменой: " + JSON.stringify(moves));
}
if (!moves[0][1].includes("#demo/XR-1/chat/" + older.id)) {
  fail("переключение разговора сменило экран под панелью: " + moves[0][1]);
}

// Новый чат по задаче поднимается отсюда же и уходит своей ручкой: конвейер
// поднимается ручкой runs, и путать их нельзя.
posted.length = 0;
sandbox.window.prompt = () => "почему ранг такой низкий";
button(listBox, "Новый чат").handlers.click({ stopPropagation: () => {} });
await settle();
if (!posted.some((p) => p.endsWith("/projects/demo/chats"))) {
  fail("новый чат ушёл не своей ручкой: " + JSON.stringify(posted));
}
sandbox.window.prompt = () => null;

// У цели кнопки нового чата нет вовсе: разговор с целью идёт её ручкой, и
// поднятая сессия читала бы чужой вход.
const goalSt = await sandbox.chatState("demo", "XR-7", board, []);
if (button(sandbox.chatList("demo", goalSt), "Новый чат")) {
  fail("у цели предлагается поднять чат, хотя реплики уходят её ручкой");
}

// Общий чат доски: та же панель с пустой привязкой, список слева это разговоры
// проекта без задачи, и своего экрана «Чаты» нет.
sandbox.location.hash = "#demo";
st = await sandbox.chatState("demo", "board", board, []);
if (!st.free || st.task) fail("чат доски открылся с привязкой: " + JSON.stringify(st));
if (st.sid !== loose.id) fail("чат доски открыл не свежий разговор без задачи: " + st.sid);
const boardList = dump(sandbox.chatList("demo", st));
if (!boardList.includes("Разговоры доски") || !boardList.includes("без задачи")) {
  fail("список чата доски не назвал разговоры без задачи: " + boardList);
}
if (button(sandbox.chatList("demo", st), "Новый чат")) {
  fail("дашборд предлагает поднять разговор без задачи, а заказать такой сессии нечего");
}
// Шапка чата доски говорит про сам разговор, а привязки не выдумывает: словом
// «ведёт» тут называть нечего.
const boardHead = dump(sandbox.chatHead("demo", st));
if (!boardHead.includes(loose.first) || boardHead.includes("ведёт ")) {
  fail("шапка чата доски приписала разговору задачу: " + boardHead);
}
// Разговоров без задачи нет вовсе: слова сервера видны вместо пустой коробки, а
// панель называет себя сама.
free = [];
freeNote = "разговоров без задачи в проекте demo нет: просмотрено 12 транскриптов";
st = await sandbox.chatState("demo", "board", board, []);
if (!dump(sandbox.chatList("demo", st)).includes("просмотрено 12 транскриптов")) {
  fail("пустой список разговоров доски молчит вместо слов сервера");
}
if (!dump(sandbox.chatHead("demo", st)).includes("разговоры доски")) {
  fail("пустая панель доски осталась без имени: " + dump(sandbox.chatHead("demo", st)));
}
free = [loose];
freeNote = "";

console.log("список разговоров: время, подписка, состояние и разряд видны, переключение" +
  " заменяет адрес, новый чат уходит ручкой chats, чат доски открывается пустой привязкой");
console.log("панель разговора: адрес хвостом и старые ссылки живы, задачный адрес открывает" +
  " свежий разговор, ручка реплики приходит с сервера, четыре причины гашения названы словами," +
  " ширина тянется хватом и помнится");
