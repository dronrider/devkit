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

// Кому достался курсор. Настоящего фокуса у мока дерева нет, а ставит его
// экран в нескольких местах, и стенду надо знать, куда курсор ушёл: на
// телефоне выехавшая ради него клавиатура закрывает пол-экрана.
export const focusLog = { node: null };

export function focused() {
  return focusLog.node;
}

export function focusDrop() {
  focusLog.node = null;
}

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
  node.append = (...kids) => {
    for (const kid of kids) {
      if (kid && typeof kid === "object") kid.parentNode = node;
    }
    node.children.push(...kids);
  };
  // Родитель проставляется всеми путями вставки, а не одним append: по нему
  // читается, лежит ли узел внутри коробки (клик мимо всплывашки), и половина
  // дерева без родителя врала бы, что узел стоит сам по себе.
  const own = (kids) => {
    for (const kid of kids) {
      if (kid && typeof kid === "object") kid.parentNode = node;
    }
  };
  node.appendChild = (kid) => { own([kid]); node.children.push(kid); return kid; };
  node.prepend = (...kids) => { own(kids); node.children.unshift(...kids); };
  node.replaceChildren = (...kids) => {
    for (const kid of node.children) {
      if (kid && typeof kid === "object" && kid.parentNode === node) kid.parentNode = null;
    }
    own(kids);
    node.children = kids;
  };
  // Вставка настоящая: узел, который уже стоит в этой коробке, переезжает, а
  // не раздваивается. Заглушка оставляла его на прежнем месте, и перерисовка
  // списка на месте (sync двигает узлы insertBefore) плодила в стенде вторые
  // копии заголовков, которых на экране нет.
  node.insertBefore = (kid, ref) => {
    const had = node.children.indexOf(kid);
    if (had >= 0) node.children.splice(had, 1);
    const at = ref ? node.children.indexOf(ref) : -1;
    if (at < 0) node.children.push(kid);
    else node.children.splice(at, 0, kid);
    if (kid && typeof kid === "object") kid.parentNode = node;
    return kid;
  };
  node.removeChild = (kid) => {
    const at = node.children.indexOf(kid);
    if (at >= 0) node.children.splice(at, 1);
    return kid;
  };
  // Снятие узла настоящее: заглушка оставляла снятое в дереве, и пересобранный
  // на месте список читался вторым (находка по тумблеру фильтра).
  node.remove = () => {
    const p = node.parentNode;
    if (!p) return;
    const at = p.children.indexOf(node);
    if (at >= 0) p.children.splice(at, 1);
    node.parentNode = null;
  };
  node.after = (kid) => { node.children.push(kid); };
  // Класс у svg ставится атрибутом (className там только на чтение), и без
  // этой связки byClass не видел бы ни сегментов кольца, ни дуги.
  node.setAttribute = (name, value) => {
    node.attrs[name] = String(value);
    if (name === "class") node.className = String(value);
  };
  node.removeAttribute = (name) => { delete node.attrs[name]; };
  node.getBoundingClientRect = () => ({ top: 0, bottom: 0, height: 0, width: 0 });
  node.focus = () => { focusLog.node = node; };
  node.scrollIntoView = () => {};
  node.addEventListener = (name, fn) => { node.handlers[name] = fn; };
  node.removeEventListener = (name) => { delete node.handlers[name]; };
  node.querySelector = (sel) => {
    const cls = String(sel).replace(/^\./, "");
    const hit = byClass(node, cls);
    if (hit) return hit;
    // Селектор без точки это имя тега: подписи для чтения с экрана вешаются на
    // сам input или select внутри собранного поля, и по классу их не найти.
    // Имя тега берётся обходом, а не общей функцией tag: у makeNode тег стоит
    // параметром и имя это перекрывает.
    return /^[a-z]+$/.test(String(sel)) ? byTag(node, String(sel).toUpperCase()) : null;
  };
  // Выборка списком идёт по последнему звену селектора: вложенность мок не
  // разбирает, а класс листа («.rrow .why» это «.why») отбирает ровно те узлы,
  // за которыми экран и приходит.
  node.querySelectorAll = (sel) => {
    const leaf = String(sel).trim().split(/\s+/).pop();
    if (leaf.startsWith(".")) return allByClass(node, leaf.slice(1)).filter((n) => n !== node);
    if (/^[a-z]+$/.test(leaf)) {
      const out = [];
      const walk = (n) => {
        for (const kid of n.children || []) {
          if (kid.tagName === leaf.toUpperCase()) out.push(kid);
          walk(kid);
        }
      };
      walk(node);
      return out;
    }
    return [];
  };
  node.closest = (sel) => (String(sel).replace(/^\./, "") === node.className ? node : null);
  // Лежит ли узел внутри этого поддерева. Обработчики строк спрашивают именно
  // так: нажатым приходит не кнопка, а её начинка, и сравнение с самой кнопкой
  // пропускало бы нажатие на значок внутри неё.
  node.contains = (other) => {
    if (!other || typeof other !== "object") return false;
    if (other === node) return true;
    for (const kid of node.children || []) {
      if (kid && kid.contains && kid.contains(other)) return true;
    }
    return false;
  };
  Object.defineProperty(node, "childElementCount", { get: () => node.children.length });
  // Высота считается по числу узлов внутри: прокрутка это предмет проверки, и
  // без модели высоты стенд не отличил бы вставшую ленту от съехавшей. Своя
  // высота (own) задаётся стендом там, где узел изображает картинку.
  node.own = 0;
  Object.defineProperty(node, "scrollHeight", {
    get: () => node.own + (node.children || []).reduce((n, k) => n + (k.scrollHeight || 0) + 20, 0),
  });
  Object.defineProperty(node, "firstChild", { get: () => node.children[0] || null });
  Object.defineProperty(node, "firstElementChild", {
    get: () => (node.children || []).find((k) => k && k.tagName && k.tagName !== "#text") || null,
  });
  return node;
}

// browserKids переводит children поддерева в такую же коллекцию, какую отдаёт
// браузер: индексы, length, item и обход циклом есть, а методов массива
// (includes, map, filter) нет вовсе. Мок держит children массивом ради самих
// стендов, и на этом послаблении в накопителе черновиков прошло нажатие строки
// через children.includes: в стенде оно считалось, а на экране падало с
// TypeError и запись не открывалась. Стенд, который смотрит за обработчиком
// клика, зовёт это на собранном узле и судит по браузерным правилам.
export function browserKids(node) {
  if (!node || typeof node !== "object") return node;
  const kids = node.children || [];
  for (const kid of kids) browserKids(kid);
  const col = {
    length: kids.length,
    item: (i) => kids[i] || null,
    [Symbol.iterator]: () => kids[Symbol.iterator](),
  };
  kids.forEach((kid, i) => { col[i] = kid; });
  Object.defineProperty(node, "children", { get: () => col, configurable: true });
  return node;
}

// dump сводит поддерево к тексту: стенды судят по написанному на экране.
export function dump(node) {
  if (!node) return "";
  const own = typeof node.textContent === "string" ? node.textContent : "";
  return [own, ...kidsOf(node).map(dump)].join(" ");
}

// kidsOf берёт детей списком, чем бы они ни были: у обычного узла мока это
// массив, а у прошедшего browserKids коллекция браузерных правил, и методов
// массива у неё нет.
function kidsOf(node) {
  return node && node.children ? Array.from(node.children) : [];
}

function byTag(node, name) {
  if (!node) return null;
  if (node.tagName === name) return node;
  for (const kid of node.children || []) {
    const hit = byTag(kid, name);
    if (hit) return hit;
  }
  return null;
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

// allByClass собирает все узлы с классом, а не первый: блоков субагента в
// ленте бывает много, и стенду важно их число.
export function allByClass(node, cls) {
  const out = [];
  const walk = (n) => {
    if (!n) return;
    if (String(n.className || "").split(" ").includes(cls)) out.push(n);
    for (const kid of n.children || []) walk(kid);
  };
  walk(node);
  return out;
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
// opts.realTimers переводит песочницу на настоящие таймеры и настоящую
// задержку ответов (opts.latency, миллисекунды): замеру отзывчивости нужны
// часы, а стендам поведения они только мешают, там время идёт оборотами
// микрозадач.
export function makeSandbox(appPath, reply, opts) {
  const byId = new Map();
  const moves = [];
  // Память браузера: стенд заводит её пустой, а opts.store наполняет заранее.
  // Прошлый заход человека виден коду только так, и без наполнения проверить
  // «состояние пережило перезагрузку» было нечем.
  const store = new Map(Object.entries((opts && opts.store) || {}));
  const timers = [];
  const streams = [];
  const asked = [];
  const posted = [];
  // Наблюдатели видимости, заведённые кодом панели. Стенд показывает узел
  // руками (seen), потому что раскладки в моке нет вовсе и «попал на экран»
  // тут событие, а не измеренная геометрия.
  const eyes = [];

  // Ответ мока умеет и text(): дашборд читает тело текстом и только потом
  // разбирает его как JSON, потому что перед ним стоит внешний вход, который
  // свой отказ пишет страницей html. Стенд отдаёт такую страницу, вернув из
  // reply объект {raw: {status, statusText, text}}.
  const ok = (body) => {
    const raw = body && body.raw ? body.raw : null;
    const text = raw ? String(raw.text || "") : JSON.stringify(body);
    const status = raw ? raw.status : 200;
    return Promise.resolve({
      ok: status >= 200 && status < 300,
      status,
      statusText: raw ? raw.statusText || "" : "OK",
      text: () => Promise.resolve(text),
      json: () => Promise.resolve(raw ? JSON.parse(text) : body),
    });
  };

  // Пачки обработчиков документа по имени события.
  const docBags = {};
  const sandbox = {
    console: { log: () => {}, error: () => {}, warn: () => {} },
    setTimeout: (fn, ms) => {
      if (opts && opts.realTimers) return setTimeout(fn, ms);
      timers.push({ fn, ms });
      return timers.length;
    },
    clearTimeout: (id) => {
      if (opts && opts.realTimers) return clearTimeout(id);
      if (id && timers[id - 1]) timers[id - 1].fn = () => {};
    },
    setInterval: () => 0,
    clearInterval: () => {},
    Date,
    JSON,
    Math,
    Promise,
    document: {
      handlers: {},
      visibilityState: "visible",
      createElement: (tag) => {
        const node = makeNode(tag);
        // Холст в стенде рисует не картинку, а её длину: предмет проверки это
        // сжатие, то есть сколько байт уедет на сервер и каким видом, а не
        // расстановка точек. Длина считается от размера холста и качества той
        // же пропорцией, что у настоящего jpeg: меньше сторона, короче тело.
        if (String(tag).toLowerCase() === "canvas") {
          node.getContext = () => ({
            fillStyle: "",
            fillRect: () => {},
            drawImage: () => {},
          });
          node.toDataURL = (kind, quality) => {
            const cells = Math.max(1, node.width * node.height);
            const bytes = Math.round(cells * 0.55 * (quality || 1));
            return "data:" + (kind || "image/png") + ";base64," + "A".repeat(Math.ceil(bytes * 4 / 3));
          };
        }
        return node;
      },
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
      // Обработчиков одного события у документа бывает несколько (всплывашки
      // закрываются общим кликом, а поиск ловит свою клавишу), и хранить
      // последний значило бы терять половину поведения. Наружу остаётся тот же
      // вид, handlers.click(ev): под именем лежит вызов всей пачки.
      addEventListener: (name, fn) => {
        const bag = docBags[name] || (docBags[name] = []);
        bag.push(fn);
        sandbox.document.handlers[name] = (ev) => { for (const f of [...bag]) f(ev); };
      },
      removeEventListener: (name, fn) => {
        const bag = docBags[name] || [];
        const at = bag.indexOf(fn);
        if (at >= 0) bag.splice(at, 1);
        if (!bag.length) delete sandbox.document.handlers[name];
      },
      body: makeNode("body"),
      // Переменные корня стенд помнит: ширину панели дашборд ставит именно
      // ими, и проверять её иначе нечем.
      documentElement: { style: {
        props: {},
        setProperty: (name, value) => { sandbox.document.documentElement.style.props[name] = value; },
      } },
    },
    window: {
      // Обработчики окна стенд помнит, а сам их не зовёт: переход по адресу в
      // браузере поднимает hashchange, и без этой памяти замерить его было
      // нечем (стенд открытия формы по ссылке). Зовёт их стенд руками, там же,
      // где браузер поднял бы событие: автоматический вызов подменял бы
      // соседним стендам их собственный порядок отрисовки.
      handlers: {},
      addEventListener: (type, fn) => {
        const bag = sandbox.window.handlers;
        bag[type] = (bag[type] || []).concat(fn);
      },
      removeEventListener: (type, fn) => {
        const bag = sandbox.window.handlers;
        bag[type] = (bag[type] || []).filter((own) => own !== fn);
      },
      // Событие окна словом: стенд поднимает его так же, как браузер.
      fire: (type, ev) => {
        for (const fn of sandbox.window.handlers[type] || []) fn(ev || {});
      },
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
    // Картинка в песочнице: настоящей расшифровки тут нет, размеры приезжают
    // из самого dataURL, куда их кладёт стенд («#<ширина>x<высота>»).
    Image: class {
      set src(value) {
        const m = /#(\d+)x(\d+)/.exec(String(value));
        this.width = m ? Number(m[1]) : 0;
        this.height = m ? Number(m[2]) : 0;
        const fire = () => {
          if (this.width && this.height) {
            if (this.onload) this.onload();
          } else if (this.onerror) this.onerror();
        };
        Promise.resolve().then(fire);
      }
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
    // Наблюдатель видимости: панель вешает его на ответы агента, а стенд
    // сообщает о показе через seen().
    IntersectionObserver: class {
      constructor(fn) {
        this.fn = fn;
        this.nodes = [];
        eyes.push(this);
      }
      observe(node) { this.nodes.push(node); }
      unobserve(node) { this.nodes = this.nodes.filter((n) => n !== node); }
      disconnect() { this.nodes = []; }
    },
    fetch: (path, init) => {
      asked.push(path);
      if (init && init.method === "POST") posted.push(path);
      const body = reply(path, init);
      const answer = () => ok(body === null || body === undefined ? {} : body);
      const wait = opts && opts.latency;
      if (!wait) return answer();
      // Задержка сети моделируется настоящим ожиданием: замер отвечает на
      // вопрос «через сколько человек увидит отклик», а не «сколько оборотов
      // сделал движок».
      return new Promise((go) => { setTimeout(() => { go(answer()); }, wait); });
    },
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: "app.js" });
  // seen сообщает наблюдателям, что узел показался на экране: так стенд
  // проигрывает прокрутку до ответа.
  const seen = (node) => {
    for (const eye of eyes) {
      if (eye.nodes.includes(node)) eye.fn([{ target: node, isIntersecting: true }], eye);
    }
  };
  return { sandbox, byId, moves, store, timers, streams, asked, posted, seen };
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
