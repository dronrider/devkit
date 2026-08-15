// Стенд очереди исходящих для ленты чата (DK-281, DK-287). Статику дашборд
// отдаёт как есть, браузера в тестах нет, а проверки текстом исходника ловят
// написанное, а не сделанное: обрыв связи, где fetch бросает исключение
// вместо ответа со статусом, и автоповтор по возвращении сети видны только
// исполнением. Скрипт поднимает static/app.js в песочнице node с маленькой
// заглушкой DOM, игрушечными часами и игрушечным сервером «Входящих», рвёт
// связь и смотрит, что осталось на экране, в памяти браузера и в файле цели.
//
// Стенд выбран вместо headless-chrome (testdata/task_narrow.js) потому, что
// здесь нужно управлять не размером окна, а связью, временем и событием
// online: в настоящем браузере обрыв связи так не подделать, а ждать
// растущую паузу по-честному значит держать тест минутами.
//
// Зовётся из go-теста (messages_test.go), путь к статике приходит аргументом.

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
    placeholder: "",
    scrollTop: 0,
    scrollHeight: 0,
    clientHeight: 0,
    children: [],
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
  node.replaceChildren = (...kids) => { node.children = kids; };
  node.remove = () => {};
  node.setAttribute = () => {};
  node.removeAttribute = () => {};
  node.getBoundingClientRect = () => ({ top: 0, bottom: 0, height: 0, width: 0 });
  node.focus = () => {};
  node.scrollIntoView = () => {};
  node.addEventListener = (name, fn) => { node.handlers[name] = fn; };
  node.removeEventListener = () => {};
  node.querySelector = () => null;
  node.querySelectorAll = () => [];
  node.closest = () => null;
  return node;
}

// Текст поддерева: подписи реплики лежат в отдельных узлах, и проверять их
// удобнее одной строкой.
function dump(node) {
  if (!node) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(dump)].join(" ");
}

// Классы поддерева: состояние реплики видно и по подписи, и по классу пузыря.
function classes(node) {
  if (!node) return "";
  return [node.className || "", ...(node.children || []).map(classes)].join(" ");
}

const store = new Map();
const byId = new Map();

// Игрушечные часы: растущая пауза измеряется минутами, и ждать её по-честному
// стенду нечем. Таймеры складываются в список, стенд сам решает, когда им
// сработать, и сам двигает время.
const timers = [];
let seq = 0;
let now = Date.UTC(2026, 7, 11, 10, 17);

// Игрушечные «Входящие»: строка ложится один раз, повтор того же текста
// сервер узнаёт и второй строки не заводит (pendingSame в messages.go).
const inbox = [];
// «up» связь есть, «down» запрос не доходит, «lost» сервер записал строку, а
// ответ до браузера не добрался, «hang» ответа нет вовсе, пока стенд его не
// отпустит.
let link = "lost";
let held = null;

// Сколько раз текст лёг во «Входящие» за всё время: лежащих строк мало,
// подхваченную витком строку файл цели уже не помнит, а агент её прочитал.
const laid = new Map();

function put(text) {
  const line = "11 авг, из дашборда: " + text;
  // Строка ложится один раз, пока она ещё лежит непрочитанной: ровно так
  // ведёт себя pendingSame на сервере.
  if (!inbox.includes(line)) {
    inbox.push(line);
    laid.set(text, (laid.get(text) || 0) + 1);
  }
  return "- " + line;
}

// Сколько строк одного сообщения лежит во «Входящих».
function count(text) {
  return inbox.filter((line) => line.endsWith(": " + text)).length;
}

// Виток забрал строку: во «Входящих» её больше нет.
function taken(text) {
  const at = inbox.indexOf("11 авг, из дашборда: " + text);
  if (at < 0) fail("витку нечего забирать: " + JSON.stringify(inbox));
  inbox.splice(at, 1);
}

function reply(body) {
  return Promise.resolve({
    ok: true,
    status: 200,
    json: () => Promise.resolve(body),
  });
}

const sandbox = {
  // Своего вывода у статики в стенде нет: она пишет в консоль про оборванный
  // fetch на верхнем уровне (обновление доски), и этот шум путался бы с
  // разбором стенда.
  console: { log: () => {}, error: () => {}, warn: () => {} },
  setTimeout: (fn, ms) => {
    seq += 1;
    timers.push({ id: seq, fn, ms });
    return seq;
  },
  clearTimeout: (id) => {
    const at = timers.findIndex((t) => t.id === id);
    if (at >= 0) timers.splice(at, 1);
  },
  setInterval: () => 0,
  clearInterval: () => {},
  Date: class extends Date {
    constructor(...args) {
      if (args.length) super(...args);
      else super(now);
    }
    static now() { return now; }
  },
  document: {
    createElement: makeNode,
    createTextNode: (text) => {
      const n = makeNode("#text");
      n.textContent = String(text);
      return n;
    },
    getElementById: (id) => {
      // Контейнера всплывающих карточек у стенда нет: предмет здесь очередь, а
      // не ответы поверх экрана. Без контейнера статика не падает, `toast`
      // просто возвращает null. Заведись он тут, таймер гаснущей карточки лёг
      // бы в ту же очередь, по которой стенд считает запланированные повторы.
      if (id === "flashes") return null;
      if (!byId.has(id)) byId.set(id, makeNode("div"));
      return byId.get(id);
    },
    addEventListener: () => {},
    body: makeNode("body"),
  },
  window: {
    listeners: {},
    addEventListener: (name, fn) => { (sandbox.window.listeners[name] ||= []).push(fn); },
    removeEventListener: (name, fn) => {
      const list = sandbox.window.listeners[name] || [];
      const at = list.indexOf(fn);
      if (at >= 0) list.splice(at, 1);
    },
    innerWidth: 1200,
  },
  location: { hash: "", href: "", replace: () => {} },
  localStorage: {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => { store.set(k, String(v)); },
    removeItem: (k) => { store.delete(k); },
  },
  EventSource: class {
    constructor() { this.readyState = 0; }
    close() {}
  },
  fetch: (path, init) => {
    const post = Boolean(init && init.method === "POST");
    // Запрос без ответа: стенду нужен зазор, в котором цикл очереди стоит на
    // await, чтобы застать уход с экрана посреди отправки.
    if (link === "hang") return new Promise((res, rej) => { held = { res, rej }; });
    if (link === "up" || (link === "lost" && post)) {
      const text = post ? JSON.parse(init.body).text : "";
      const line = post ? put(text) : "";
      // Оборванная связь в браузере выглядит так: fetch не отдаёт ответ со
      // статусом, а бросает TypeError. Ровно это и есть авиарежим из
      // сценария проверки, и «lost» это его худший вид: сервер строку уже
      // записал, а браузер об этом не узнал.
      if (link === "lost") return Promise.reject(new TypeError("Failed to fetch"));
      if (post) return reply({ message: "сообщение легло во «Входящие»", line });
      return reply({ pending: inbox.slice() });
    }
    return Promise.reject(new TypeError("Failed to fetch"));
  },
};
sandbox.globalThis = sandbox;

vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: "app.js" });

const fail = (msg) => { console.error(msg); process.exit(1); };

// Дать отработать цепочкам промисов: настоящего ожидания в стенде нет, всё
// решается за несколько оборотов очереди микрозадач.
const settle = async () => {
  for (let i = 0; i < 200; i += 1) await Promise.resolve();
};

// Сработать ближайшему запланированному повтору.
const tick = async () => {
  const t = timers.shift();
  if (!t) fail("повтор не запланирован: очередь встала навсегда");
  now += t.ms;
  t.fn();
  await settle();
  return t.ms;
};

const key = "devkit.chat.sent.demo/XR-100";
const saved = () => JSON.parse(store.get(key) || "[]");

// Сценарий 1: связь оборвана, сообщение отправлено, связь вернулась,
// сообщение ушло само, во «Входящих» одна строка.
const box = sandbox.document.createElement("div");
const out = sandbox.makeOutbox("demo", "XR-100", box);

let thrown = null;
try {
  await out.send("проверь ленту");
} catch (err) {
  thrown = err;
}
if (thrown) fail("исключение обрыва связи ушло выше отправки: " + thrown);
await settle();

let shown = dump(box);
if (!shown.includes("в очереди")) {
  fail("после обрыва связи реплика не подписана «в очереди»: " + shown);
}
if (shown.includes("Повторить")) {
  fail("на реплике осталась кнопка повтора: " + shown);
}
if (saved().length !== 1 || saved()[0].state !== "queued" || saved()[0].text !== "проверь ленту") {
  fail("реплика не легла в очередь хранилища: " + JSON.stringify(saved()));
}

// Пауза растёт: первый повтор ближе, второй дальше.
link = "down";
const first = await tick();
const second = await tick();
if (!(second > first)) {
  fail("пауза между повторами не растёт: " + first + " и " + second);
}
if (dump(box).includes("ждёт витка")) {
  fail("реплика без связи выглядит отправленной: " + dump(box));
}

// Долгая неудача говорит словами, а не молчит.
now += 5 * 60 * 1000;
link = "down";
await tick();
shown = dump(box);
if (!shown.includes("связи нет") || !/в очереди \d+ мин/.test(shown)) {
  fail("залежавшаяся реплика не говорит, сколько не уходит: " + shown);
}
if (!classes(box).includes("m-stuck")) {
  fail("залежавшаяся реплика ничем не отличается от свежей: " + classes(box));
}

// Связь вернулась: ни одного нажатия, сообщение уходит само.
link = "up";
for (const fn of sandbox.window.listeners.online || []) fn();
await settle();

shown = dump(box);
if (!shown.includes("ждёт витка")) {
  fail("по возвращении связи сообщение не ушло само: " + shown);
}
if (shown.includes("в очереди")) {
  fail("ушедшая реплика осталась в очереди: " + shown);
}
if (count("проверь ленту") !== 1) {
  fail("повторы завели во «Входящих» лишние строки: " + JSON.stringify(inbox));
}
if (saved().length !== 1 || saved()[0].state !== "waiting") {
  fail("состояние ушедшей реплики не сохранилось: " + JSON.stringify(saved()));
}

// Сценарий 2: перезагрузка страницы очередь не теряет. Новый экран поднимает
// список из того же хранилища и дожимает оставшееся.
link = "down";
const box2 = sandbox.document.createElement("div");
const out2 = sandbox.makeOutbox("demo", "XR-100", box2);
await out2.send("второе сообщение");
await settle();
if (saved().length !== 2 || saved()[1].state !== "queued") {
  fail("вторая реплика не встала в очередь: " + JSON.stringify(saved()));
}
out2.stop();

const box3 = sandbox.document.createElement("div");
const out3 = sandbox.makeOutbox("demo", "XR-100", box3);
out3.draw();
shown = dump(box3);
if (!shown.includes("второе сообщение") || !shown.includes("в очереди")) {
  fail("после перезагрузки очередь потерялась: " + shown);
}
link = "up";
await out3.pump();
await settle();
shown = dump(box3);
if (!shown.includes("ждёт витка")) {
  fail("поднятая после перезагрузки очередь не дожалась: " + shown);
}
if (count("второе сообщение") !== 1) {
  fail("после перезагрузки повтор завёл лишнюю строку: " + JSON.stringify(inbox));
}
out3.stop();

// Первая очередь уходит со сцены: у живой очереди тикает своё перечитывание
// «Входящих» (OUTBOX_POLL, DK-342), а проверки ниже смотрят ровно на планы
// третьей.
out.stop();

// Сценарий 3: уход с экрана посреди отправки цикл не переживает. Иначе
// вернувшийся в чат человек поднял бы вторую очередь на ту же запись, и один
// текст слали бы два цикла разом.
link = "hang";
const box4 = sandbox.document.createElement("div");
const out4 = sandbox.makeOutbox("demo", "XR-101", box4);
const sending = out4.send("третье сообщение");
await settle();
if (!held) fail("стенд не застал отправку на полпути");
if (timers.length) fail("повтор запланирован до ответа сервера");

out4.stop();
held.rej(new TypeError("Failed to fetch"));
held = null;
await sending;
await settle();
if (timers.length) {
  fail("stop() не остановил цикл: после ухода с экрана запланирован повтор");
}
// Событие online зомби-цикл тоже не будит.
for (const fn of sandbox.window.listeners.online || []) fn();
await settle();
if (timers.length || held) {
  fail("остановленный цикл ожил по событию online");
}

// Сценарий 4: виток забрал строку между попытками. Дедупликация на сервере
// держится на неподхваченной строке, и подхваченную она уже не узнаёт: тот же
// текст ляжет второй строкой. Риск принят решением в файле задачи, а стенд
// держит границу принятого, чтобы смена поведения не прошла молча.
link = "lost";
const box5 = sandbox.document.createElement("div");
const out5 = sandbox.makeOutbox("demo", "XR-102", box5);
await out5.send("четвёртое сообщение");
await settle();
if (count("четвёртое сообщение") !== 1) {
  fail("сервер не записал строку до потери ответа: " + JSON.stringify(inbox));
}
taken("четвёртое сообщение");
link = "up";
await out5.pump();
await settle();
// Двойка тут и есть принятая граница: подхваченную строку сервер уже не
// узнаёт, и повтор кладёт её заново, так что агент прочитает текст дважды.
// Лежащая строка при этом одна, дубль виден только по счёту положенного.
// Тест упадёт и когда дубль перестанет появляться, и это будет поводом
// переписать решение в файле задачи, а не молча поправить ожидание.
if ((laid.get("четвёртое сообщение") || 0) !== 2 || count("четвёртое сообщение") !== 1) {
  fail("подхваченная витком строка ведёт себя не как принято решением: " +
    laid.get("четвёртое сообщение") + " раз положено, " + JSON.stringify(inbox));
}
if (!dump(box5).includes("ждёт витка")) {
  fail("повтор после подхвата не довёл реплику до «ждёт витка»: " + dump(box5));
}
out5.stop();

console.log("очередь исходящих: обрыв связи держится, сеть вернулась и сообщение ушло само," +
  " перезагрузка очередь не потеряла, уход с экрана цикл гасит," +
  " во «Входящих» по одной строке на сообщение");
