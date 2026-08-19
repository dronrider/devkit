// Общий мок дерева и окружения для стендов POC (ветка poc-chat).
//
// Стенды POC гоняют настоящий static/app.js в песочнице vm поверх мока DOM:
// браузера в прогоне нет, а проверять надо собранную разметку и поведение
// обработчиков, а не текст исходника. Мок вынесен сюда, потому что стендов
// трое, а копия мока в каждом разъезжалась бы: узел, научившийся чему-то в
// одном стенде, в соседнем этого не умел бы.
//
// Каждый стенд зовётся так: node testdata/poc_<имя>.mjs static/app.js

import fs from "node:fs";
import vm from "node:vm";

export function fail(msg) {
  console.error(msg);
  process.exit(1);
}

// makeNode это узел дерева ровно с теми свойствами, за которые берётся app.js.
// hidden тут не прячет узел из обхода: стенды смотрят сам признак, как это
// делает и человек, глядя на экран.
export function makeNode(tag) {
  const node = {
    tagName: String(tag || "div").toUpperCase(),
    className: "",
    textContent: "",
    title: "",
    href: "",
    hidden: false,
    disabled: false,
    readOnly: false,
    value: "",
    placeholder: "",
    rows: 0,
    scrollTop: 0,
    scrollHeight: 0,
    clientHeight: 0,
    offsetHeight: 0,
    children: [],
    attrs: {},
    style: {},
    dataset: {},
    handlers: {},
  };
  node.classList = {
    add: (...cls) => { node.className = (node.className + " " + cls.join(" ")).trim(); },
    remove: (...cls) => {
      node.className = node.className.split(" ").filter((c) => c && !cls.includes(c)).join(" ");
    },
    contains: (cls) => node.className.split(" ").includes(cls),
    toggle: (cls, on) => {
      const has = node.className.split(" ").includes(cls);
      const want = on === undefined ? !has : Boolean(on);
      if (want && !has) node.classList.add(cls);
      if (!want && has) node.classList.remove(cls);
      return want;
    },
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
  node.after = (kid) => { node.children.push(kid); };
  node.setAttribute = (name, value) => { node.attrs[name] = String(value); };
  node.removeAttribute = (name) => { delete node.attrs[name]; };
  node.getBoundingClientRect = () => ({ top: 0, bottom: 0, height: 0, width: 0 });
  node.focus = () => {};
  node.scrollIntoView = () => {};
  node.addEventListener = (name, fn) => { node.handlers[name] = fn; };
  node.removeEventListener = (name) => { delete node.handlers[name]; };
  node.querySelector = (sel) => {
    const cls = String(sel).replace(/^\./, "");
    return byClass(node, cls) || (sel === "button" ? tag(node, "BUTTON") : null);
  };
  node.querySelectorAll = () => [];
  node.closest = (sel) => (String(sel).replace(/^\./, "") === node.className ? node : null);
  Object.defineProperty(node, "childElementCount", { get: () => node.children.length });
  Object.defineProperty(node, "firstChild", { get: () => node.children[0] || null });
  return node;
}

// dump сводит поддерево к тексту: стенды судят по написанному на экране.
export function dump(node) {
  if (!node) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...(node.children || []).map(dump)].join(" ");
}

export function tag(node, name) {
  if (!node) return null;
  if (node.tagName === name) return node;
  for (const kid of node.children || []) {
    const hit = tag(kid, name);
    if (hit) return hit;
  }
  return null;
}

export function byClass(node, cls) {
  if (!node) return null;
  if (String(node.className || "").split(" ").includes(cls)) return node;
  for (const kid of node.children || []) {
    const hit = byClass(kid, cls);
    if (hit) return hit;
  }
  return null;
}

// deepBtn ищет кнопку по подписи или по классу. Подпись у кнопок дашборда
// лежит в дочернем узле (barBtn кладёт значок и текст), поэтому по textContent
// самой кнопки её не найти.
export function deepBtn(node, what) {
  if (!node) return null;
  if (node.tagName === "BUTTON") {
    if (dump(node).includes(what) || String(node.className || "").includes(what)) return node;
  }
  for (const kid of node.children || []) {
    const hit = deepBtn(kid, what);
    if (hit) return hit;
  }
  return null;
}

// makeSandbox поднимает окружение и выполняет в нём app.js. Ответы сервера
// задаёт стенд функцией reply(path, init): вернула она объект, значит это тело
// удачного ответа, вернула null, значит ручка стенду не интересна.
export function makeSandbox(appPath, reply) {
  const byId = new Map();
  const moves = [];
  const store = new Map();
  const timers = [];
  const streams = [];
  const asked = [];
  const posted = [];

  const ok = (body) => Promise.resolve({
    ok: true, status: 200, json: () => Promise.resolve(body),
  });

  const sandbox = {
    console: { log: () => {}, error: () => {}, warn: () => {} },
    setTimeout: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
    clearTimeout: (id) => { if (id && timers[id - 1]) timers[id - 1].fn = () => {}; },
    setInterval: () => 0,
    clearInterval: () => {},
    Date,
    JSON,
    Math,
    Promise,
    document: {
      handlers: {},
      visibilityState: "visible",
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
      addEventListener: (name, fn) => { sandbox.document.handlers[name] = fn; },
      removeEventListener: (name) => { delete sandbox.document.handlers[name]; },
      body: makeNode("body"),
      documentElement: { style: { setProperty: () => {} } },
    },
    window: {
      addEventListener: () => {},
      removeEventListener: () => {},
      innerWidth: 1400,
      innerHeight: 900,
      matchMedia: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {} }),
      prompt: () => null,
      getSelection: () => null,
      navigator: { clipboard: { writeText: () => Promise.resolve() } },
    },
    location: { hash: "", href: "", replace: (h) => { sandbox.location.hash = h; } },
    history: {
      pushState: (state, title, url) => { moves.push(["push", url]); sandbox.location.hash = url; },
      replaceState: (state, title, url) => { moves.push(["replace", url]); sandbox.location.hash = url; },
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
        this.readyState = 1;
        this.closed = false;
        this.listeners = {};
        streams.push(this);
      }
      addEventListener(name, fn) { this.listeners[name] = fn; }
      close() { this.closed = true; this.readyState = 2; }
    },
    // Чтение вставленного файла: мок отдаёт готовый dataURL сразу, потому что
    // предмет стенда это то, что попадёт в src, а не сама расшифровка.
    FileReader: class {
      readAsDataURL(file) {
        this.result = (file && file.dataURL) ||
          "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
        if (this.onload) this.onload();
      }
    },
    fetch: (path, init) => {
      asked.push(path);
      if (init && init.method === "POST") posted.push(path);
      const body = reply(path, init);
      return ok(body === null || body === undefined ? {} : body);
    },
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: "app.js" });
  return { sandbox, byId, moves, store, timers, streams, asked, posted };
}

// settle прокручивает микрозадачи: ответы стенда готовы сразу, и обработчикам
// хватает нескольких оборотов, чтобы досчитать.
export async function settle(n) {
  for (let i = 0; i < (n || 90); i += 1) await Promise.resolve();
}

export function appPathArg() {
  const p = process.argv[2];
  if (!p) fail("нужен путь до static/app.js");
  return p;
}
