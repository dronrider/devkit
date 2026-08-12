// Стенд обрыва связи для ленты чата (DK-281). Статику дашборд отдаёт как
// есть, браузера в тестах нет, а проверки текстом исходника ловят написанное,
// а не сделанное: случай, где fetch бросает исключение вместо ответа со
// статусом, виден только исполнением. Скрипт поднимает static/app.js в
// песочнице node с маленькой заглушкой DOM, роняет отправку исключением и
// смотрит, что осталось на экране и в памяти браузера.
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

const store = new Map();
const byId = new Map();

const sandbox = {
  // Своего вывода у статики в стенде нет: она пишет в консоль про оборванный
  // fetch на верхнем уровне (обновление доски), и этот шум путался бы с
  // разбором стенда.
  console: { log: () => {}, error: () => {}, warn: () => {} },
  setTimeout,
  clearTimeout,
  setInterval,
  clearInterval,
  document: {
    createElement: makeNode,
    createTextNode: (text) => {
      const n = makeNode("#text");
      n.textContent = String(text);
      return n;
    },
    getElementById: (id) => {
      if (!byId.has(id)) byId.set(id, makeNode("div"));
      return byId.get(id);
    },
    addEventListener: () => {},
    body: makeNode("body"),
  },
  window: { addEventListener: () => {}, innerWidth: 1200 },
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
  // Обрыв связи в браузере выглядит так: fetch не отдаёт ответ со статусом, а
  // бросает TypeError. Ровно это и есть авиарежим из сценария проверки.
  fetch: () => Promise.reject(new TypeError("Failed to fetch")),
};
sandbox.globalThis = sandbox;

vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: "app.js" });

const fail = (msg) => { console.error(msg); process.exit(1); };

const box = sandbox.document.createElement("div");
const out = sandbox.makeOutbox("demo", "XR-100", box);

let thrown = null;
try {
  await out.send("проверь ленту");
} catch (err) {
  thrown = err;
}
if (thrown) {
  fail("исключение обрыва связи ушло выше отправки: " + thrown);
}

const shown = dump(box);
if (!shown.includes("не ушло")) {
  fail("после обрыва связи реплика не подписана «не ушло»: " + shown);
}
if (!shown.includes("Повторить")) {
  fail("у неушедшей реплики нет кнопки «Повторить»: " + shown);
}
if (shown.includes("отправляется")) {
  fail("реплика осталась в состоянии отправки: " + shown);
}

const saved = JSON.parse(store.get("devkit.chat.sent.demo/XR-100") || "[]");
if (saved.length !== 1 || saved[0].state !== "failed" || saved[0].text !== "проверь ленту") {
  fail("неушедшая реплика не пережила бы перезагрузку: " + JSON.stringify(saved));
}

console.log("обрыв связи: реплика «не ушло» с повтором, запись сохранена");
