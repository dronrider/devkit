// Стенд перехода к разговору агента (DK-280). Проверка текстом исходника тут
// не годится: предмет проверки это набор кнопок, который экран собирает по
// живой работе, и лента, которую он строит по ответу сервера с несколькими
// сессиями. Стенд поднимает static/app.js в песочнице node с маленькой
// заглушкой DOM, зовёт taskActions и wireTranscript и смотрит, что вышло на
// экран.
//
// Зовётся из go-теста (sessions_test.go), путь к статике приходит аргументом.

import fs from "node:fs";
import vm from "node:vm";

const appPath = process.argv[2];
if (!appPath) {
  console.error("нужен путь до static/app.js");
  process.exit(2);
}

function makeNode(tag) {
  const node = {
    tagName: String(tag || "div").toUpperCase(),
    className: "",
    textContent: "",
    title: "",
    hidden: false,
    disabled: false,
    value: "",
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

// Текст поддерева одной строкой: подписи кнопок лежат в отдельных узлах.
function dump(node) {
  if (!node) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(dump)].join(" ");
}

const fail = (msg) => { console.error(msg); process.exit(1); };

// Кнопка полосы действий по подписи: подпись уезжает и в aria-label, по нему
// кнопка и ищется.
function button(nodes, label) {
  return nodes.find((n) => n.attrs && n.attrs["aria-label"] === label);
}

// Открытые потоки: экран транскрипта дострение снимает при переключении на
// соседний разговор, и стенд смотрит, какой поток остался живым.
const streams = [];

// Узлы страницы по id: статика ищет их при загрузке, и один и тот же id должен
// отдавать один и тот же узел.
const byId = new Map();

// Что попросили у сервера: по списку видно, за чьими репликами сходил экран.
const asked = [];

let sessions = [];
let note = "";

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
  document: {
    createElement: makeNode,
    createTextNode: (text) => {
      const n = makeNode("#text");
      n.textContent = String(text);
      return n;
    },
    // Значки лежат в разметке страницы, в стенде её нет: шаблон отдаётся
    // пустым, icon() падает обратно на пустой узел, и полосе действий это не
    // мешает.
    getElementById: (id) => {
      if (!byId.has(id)) {
        const node = makeNode("div");
        node.content = { querySelector: () => null };
        byId.set(id, node);
      }
      return byId.get(id);
    },
    addEventListener: () => {},
    body: makeNode("body"),
  },
  window: {
    addEventListener: () => {},
    removeEventListener: () => {},
    innerWidth: 1200,
    matchMedia: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }),
  },
  location: { hash: "", href: "", replace: () => {} },
  localStorage: {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
  },
  EventSource: class {
    constructor(url) {
      this.url = url;
      this.closed = false;
      // Уведомитель держит свой поток с самой загрузки, стенду он не о чём:
      // считаются только ленты разговоров.
      if (String(url).includes("/sessions/")) streams.push(this);
    }
    addEventListener() {}
    close() { this.closed = true; }
  },
  fetch: (path) => {
    asked.push(path);
    if (path.includes("/sessions?task=")) {
      return reply(note ? { sessions, note } : { sessions });
    }
    return reply({ sessions: [] });
  },
};
sandbox.globalThis = sandbox;

vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: "app.js" });

const settle = async () => {
  for (let i = 0; i < 50; i += 1) await Promise.resolve();
};

// Случай 1: работа кончилась. Переход к разговору всё равно на полосе, и ведёт
// он на экран агента этой задачи. Ровно этого не было до DK-280: кнопка стояла
// только у живой работы, и с экрана задачи разговор было не открыть.
const row = { title: "dashboard: разговор законченной работы", sect: "check", r: 37 };
const done = sandbox.taskActions("demo", "XR-101", row, []);
const talk = button(done, "Разговор агента");
if (!talk) {
  fail("на полосе задачи без живой работы нет перехода к разговору: " + dump({ children: done }));
}
talk.handlers.click();
if (sandbox.location.hash !== "demo/agent/XR-101") {
  fail("переход к разговору ведёт не на экран агента задачи: " + sandbox.location.hash);
}

// Случай 2: работа идёт. Подпись говорит про живой ход, а не про запись, и
// кнопка стопа на месте.
const live = sandbox.taskActions("demo", "XR-101", row,
  [{ id: "XR-101", kind: "task", via: "tmux" }]);
if (!button(live, "Живой статус")) {
  fail("у живой работы переход назван не живым статусом: " + dump({ children: live }));
}
if (button(live, "Разговор агента")) {
  fail("у живой работы на полосе две кнопки об одном экране");
}
if (!button(live, "Остановить агента")) {
  fail("у живой работы пропала кнопка стопа");
}

// Случай 3: у задачи два разговора. Открыт свежий, второй ждёт переключателем,
// и переключение снимает дострение прежней ленты.
sessions = [
  { id: "bbb-22222222", mtime: "2026-08-12T10:00:00Z", branch: "dk-280", task: "XR-101", taskNote: "по дереву задачи" },
  { id: "aaa-11111111", mtime: "2026-08-10T09:00:00Z", branch: "main", task: "XR-101", taskNote: "по первой реплике" },
];
note = "обход прерван по времени: просмотрено 40 транскриптов из 200";
const tp = { sub: makeNode("span"), body: makeNode("div") };
await sandbox.wireTranscript("demo", tp, "XR-101");
await settle();

let shown = dump(tp.body);
if (!shown.includes("обход прерван по времени")) {
  fail("слова сервера о неполном обходе не дошли до экрана: " + shown);
}
// Подпись разговора это день и время последней записи в поясе читающего:
// ожидание берётся тем же переводом, что и на экране, иначе стенд держал бы
// пояс и язык машины, на которой его гоняют.
const tabText = (s) => sandbox.localDay(s.mtime) + ", " + sandbox.localTime(s.mtime);
for (const s of sessions) {
  if (!shown.includes(tabText(s))) {
    fail("разговор " + s.id + " не назван в переключателе днём и временем: " + shown);
  }
}
if (!dump(tp.sub).includes("bbb-2222")) {
  fail("открыт не свежий разговор: " + dump(tp.sub));
}
if (streams.length !== 1 || !streams[0].url.includes("bbb-22222222")) {
  fail("лента открыта не по свежему разговору: " + JSON.stringify(streams.map((s) => s.url)));
}

// Переключение на прежний разговор: лента собирается заново, а поток прежней
// закрывается, иначе на экран сыпались бы реплики двух сессий разом.
const seg = tp.body.children.find((n) => n.className === "seg");
if (!seg || seg.children.length !== 2) {
  fail("переключателя разговоров на экране нет: " + shown);
}
seg.children[1].handlers.click();
await settle();
if (!streams[0].closed) {
  fail("дострение прежнего разговора осталось живым после переключения");
}
if (streams.length !== 2 || !streams[1].url.includes("aaa-11111111")) {
  fail("переключение не открыло второй разговор: " + JSON.stringify(streams.map((s) => s.url)));
}
shown = dump(tp.body);
if (!dump(tp.sub).includes("aaa-1111") || !shown.includes(tabText(sessions[0]))) {
  fail("после переключения потерялись подпись или переключатель: " + shown + " / " + dump(tp.sub));
}

// Случай 4: разговоров нет вовсе. Экран не молчит: слова сервера видны, и
// рядом список сессий проекта, чтобы работа не пропадала без объяснения.
sessions = [];
note = "сессий задачи XR-101 нет: просмотрены все 200 транскриптов проекта";
const empty = { sub: makeNode("span"), body: makeNode("div") };
await sandbox.wireTranscript("demo", empty, "XR-101");
await settle();
if (!dump(empty.body).includes("просмотрены все 200 транскриптов")) {
  fail("пустой разговор молчит вместо слов сервера: " + dump(empty.body));
}

console.log("экран задачи: переход к разговору стоит и без живой работы, разговоры задачи" +
  " переключаются, неполный обход и пустой ответ сказаны словами");
