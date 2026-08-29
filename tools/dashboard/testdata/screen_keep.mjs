// Стенд частичной перерисовки (DK-316) и экранов, которые ею живут. Предмет
// проверки это не написанное в исходнике, а то, что остаётся на экране после
// обновления: прокрутка списка, фокус на кнопке, набранное в поле чата, живой
// поток событий, набранное уточнение груминга и раскрытое подтверждение
// удаления. Проверкой текста статики такое не берётся, поэтому app.js
// поднимается в песочнице node с игрушечным DOM и игрушечным сервером. Тем же
// стендом проверяются состояния экрана черновика (DK-321): их рисует статика
// по ответу сервера, и сверять их разбором исходника значит пересказывать его.
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
// Он же держит состав кнопок действий по макетам 11 и 12 (DK-337): стоп в
// строке доски красный, подтверждение удаления собрано полноразмерными
// кнопками своей строкой, повторная ходка груминга стоит по правому краю поля.
//
// Им же проверяется выбор подписки в кнопке запуска (DK-326): широкая часть
// идёт на подписку по умолчанию, строка списка на свою, а там, где выбирать не
// из чего, стрелки нет и причина стоит подсказкой. Смотрит стенд тело
// запроса: нарисованный список без доехавшего имени это ровно та поломка, от
// которой задача и заведена. Вид кнопки стенд держит по принятому макету «11
// Подписка при запуске» (DK-336): половины одного цвета со стыком без зазора,
// галочка рамкой, а в списке шапка, подсветка подписки по умолчанию, чип,
// остаток квоты полосками и подвал с источником списка.
//
// Смотрит стенд и ответ на нажатие: он приходит карточкой поверх экрана и не
// трогает ни одного узла из потока документа, поэтому раскладка от него не
// едет. Поток стенд берёт из настоящего index.html, а «поверх экрана» сверяет с
// настоящим style.css, чтобы проверка не выродилась в пересказ самой себя.
//
// Последним разделом идёт перетаскивание строки очереди (DK-324): расчёт
// коридора и щелей проверяется прямыми вызовами по краевым случаям, а сам жест
// игрушечными событиями указателя. Предмет тут это нарисованный на живом списке
// коридор, тело запроса, слова после броска и откат с ожидаемой разбивкой.
// Игрушечный DOM ради этого считает вертикаль списка: щель под пальцем берётся
// из неё, как в браузере.
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
    listenOpts: {},
  };
  // Список классов настоящий, со снятием и переключением: перетаскивание им и
  // приглушает зону за коридором, и подсвечивает щель под пальцем, а
  // заглушка-пустышка показывала бы, что снятое так и висит.
  const classes = () => String(node.className).split(" ").filter(Boolean);
  node.classList = {
    add: (...cls) => {
      const list = classes();
      for (const cl of cls) if (!list.includes(cl)) list.push(cl);
      node.className = list.join(" ");
    },
    remove: (...cls) => { node.className = classes().filter((cl) => !cls.includes(cl)).join(" "); },
    contains: (cls) => classes().includes(cls),
    toggle: (cls, on) => {
      const want = on === undefined ? !classes().includes(cls) : Boolean(on);
      if (want) node.classList.add(cls);
      else node.classList.remove(cls);
      return want;
    },
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
  // Экран задачи ставит полосу действий сразу за шапкой этим вызовом
  // (watchTaskLayout, static/app.js): без него полоса нигде не встаёт в
  // дерево, и кнопка запуска стенду не найти.
  node.after = (...kids) => {
    const par = node.parentElement;
    if (!par) return;
    const ref = par.children[par.children.indexOf(node) + 1] || null;
    for (const kid of kids) par.insertBefore(kid, ref);
  };
  // Атрибуты запоминаются: подпись кнопки полосы действий живёт в aria-label, и
  // по нему стенд её и находит.
  node.attrs = {};
  node.setAttribute = (name, value) => { node.attrs[name] = String(value); };
  node.removeAttribute = (name) => { delete node.attrs[name]; };
  // Вертикаль узла: настоящих размеров у стенда нет, и по умолчанию их нет ни
  // у кого. Раскладка включается коробке отдельно (layout ниже), и тогда её
  // дети лежат сверху вниз: жест берёт из этих чисел щель под пальцем.
  node.getBoundingClientRect = () => {
    const par = node.parentElement;
    if (!par || !par.laid) return { top: 0, bottom: 0, height: 0, width: 0 };
    let top = 0;
    for (const kid of par.children) {
      if (kid === node) break;
      top += height(kid);
    }
    const own = height(node);
    return { top, bottom: top + own, height: own, width: 0 };
  };
  node.setPointerCapture = () => {};
  node.releasePointerCapture = () => {};
  // Фокус берёт только прикреплённый к документу узел: собранный, но ещё не
  // вставленный узел браузер не фокусирует вовсе, и вызов на нём проходит
  // молча. Приёмка нашла ровно это: поле поиска фокусировалось внутри make(),
  // до вставки, и человеку нужно было второе касание (хвост DK-325).
  node.focus = () => {
    if (attached(node)) doc.activeElement = node;
  };
  node.scrollIntoView = () => {};
  // Условия подписки лежат рядом с самим обработчиком: отменить прокрутку
  // браузер разрешает только слушателю, объявленному не passive, и проверять
  // это надо по подписке, а не по тому, звался ли preventDefault в стенде.
  node.addEventListener = (name, fn, opts) => {
    node.handlers[name] = fn;
    node.listenOpts[name] = opts;
  };
  node.removeEventListener = () => {};
  node.querySelector = () => null;
  node.querySelectorAll = () => [];
  node.closest = () => null;
  // Лежит ли узел внутри поддерева: обработчики строк спрашивают нажатое
  // именно так, потому что приходит к ним не сама кнопка, а её начинка.
  node.contains = (other) => {
    if (!other || typeof other !== "object") return false;
    if (other === node) return true;
    for (const kid of node.children || []) {
      if (kid && kid.contains && kid.contains(other)) return true;
    }
    return false;
  };
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

// Узел прикреплён к документу, если его цепочка родителей упирается в узел
// страницы: в стенде это тело документа и коробки по id (#groups и соседи),
// сами они лежат готовыми. Собранный статикой узел до вставки в такую коробку
// висит сам по себе, и браузер его не фокусирует.
function attached(node) {
  let cur = node;
  while (cur.parentElement) cur = cur.parentElement;
  if (cur === doc.body) return true;
  for (const root of byId.values()) {
    if (root === cur) return true;
  }
  return false;
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

const store = new Map();
const byId = new Map();
const doc = {
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
  // («/» ставит курсор в поле поиска) больше неоткуда взяться. Одного события
  // документ слушает по нескольку раз (всплывашки гасятся Escape, форма
  // заведения им же закрывается, поиск ловит косую черту), и хранить
  // последний значило бы терять половину поведения: наружу вид прежний,
  // doc.handlers.keydown(ev), под именем лежит вызов всей пачки. Снятие тоже
  // настоящее: панель разговора вешает на документ слежение за выделением и
  // снимает его при уходе, и молчащий заглушкой стенд об этом бы не узнал.
  handlers: {},
  bags: {},
  addEventListener: (name, fn) => {
    const bag = doc.bags[name] || (doc.bags[name] = []);
    bag.push(fn);
    doc.handlers[name] = (ev) => { for (const f of [...bag]) f(ev); };
  },
  removeEventListener: (name, fn) => {
    const bag = doc.bags[name] || [];
    const at = bag.indexOf(fn);
    if (at >= 0) bag.splice(at, 1);
    if (!bag.length) delete doc.handlers[name];
  },
};
// Корневой узел нужен ширине панели разговора: она уезжает переменной стиля,
// а не размером самого узла.
doc.documentElement = makeNode("html");
doc.documentElement.style.setProperty = () => {};
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

// Кнопка полосы действий: подпись у неё лежит в отдельном узле, зато уезжает в
// aria-label, по нему кнопка и ищется.
function barButton(node, label) {
  if (!node) return null;
  if (node.tagName === "BUTTON" && node.attrs && node.attrs["aria-label"] === label) return node;
  for (const kid of node.children || []) {
    const hit = barButton(kid, label);
    if (hit) return hit;
  }
  return null;
}

// Кнопка с такой подписью где-нибудь в поддереве. Подпись у кнопок строки
// доски лежит не текстом, а меткой для чтения с экрана: они собраны значками, а
// слово ушло в подсказку.
function button(node, label) {
  if (!node) return null;
  if (node.tagName === "BUTTON" &&
    (node.textContent === label || node.attrs["aria-label"] === label)) return node;
  for (const kid of node.children || []) {
    const hit = button(kid, label);
    if (hit) return hit;
  }
  return null;
}

// Кнопка полосы действий задачи (barBtn, static/app.js): подпись лежит в
// дочернем узле рядом со значком, а не прямо в textContent самой кнопки.
function actionButton(node, label) {
  if (!node) return null;
  if (node.tagName === "BUTTON" && dump(node).trim() === label) return node;
  for (const kid of node.children || []) {
    const hit = actionButton(kid, label);
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

// Все узлы с таким классом: строк подписки в меню несколько, и число их само
// по себе предмет проверки.
function allByClass(node, cls) {
  const out = [];
  const walk = (n) => {
    if (!n) return;
    if (String(n.className || "").split(" ").includes(cls)) out.push(n);
    for (const kid of n.children || []) walk(kid);
  };
  walk(node);
  return out;
}

// Раскладка коробки: её дети получают вертикаль, всем прочим узлам дерева
// по-прежнему нечего отдать. Считается она на каждый запрос, поэтому вставшая
// между строками щель двигает всё, что ниже, как в браузере.
function layout(box) {
  box.laid = true;
  return box;
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

// Тело медиазапроса из настоящего style.css: правила телефона живут в нём, и
// читать их стенд обязан из файла, а не из своих представлений о нём. Границы
// берутся счётом фигурных скобок, как их считает и разбор браузера.
function mediaBlock(css, head) {
  const at = css.indexOf(head);
  if (at < 0) return "";
  const from = css.indexOf("{", at);
  if (from < 0) return "";
  let depth = 0;
  for (let i = from; i < css.length; i += 1) {
    if (css[i] === "{") depth += 1;
    if (css[i] === "}") {
      depth -= 1;
      if (!depth) return css.slice(from + 1, i);
    }
  }
  return "";
}

// Чем кончится display у узла с такими классами. Правила из одних классов
// подходят, когда все их классы есть у узла (`.card.dcol-run` узлу без dcol-run
// не достаётся), а из подошедших побеждает последнее: специфичность у них
// равная, и браузер выбирает так же.
function phoneDisplay(css, classes) {
  let out = "";
  const rules = /([^{}]+)\{([^{}]*)\}/g;
  let m;
  while ((m = rules.exec(css))) {
    const sel = m[1].trim();
    if (!/^(\.[A-Za-z0-9_-]+)+$/.test(sel)) continue;
    if (!sel.split(".").filter(Boolean).every((cls) => classes.includes(cls))) continue;
    const d = /display:\s*([a-z-]+)/.exec(m[2]);
    if (d) out = d[1];
  }
  return out;
}

const streams = [];
let running = false;
// Чья работа поднята: по умолчанию XR-3, как раньше, а POST /runs переставляет
// на id из тела запроса, иначе строка с фокусной проверкой (XR-6) не узнает о
// своей же работе.
let runningId = "XR-3";

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

const rows = [row("XR-1", "Цель: дашборд без дёрганья",
  // Этап работы приезжает полем строки: пишут его конвейер и taskctl, а дашборд
  // только читает (stage.go). После DK-435 его показывает экран задачи.
  { type: "goal", cost: "XL", stage: "разработка", stage_since: Math.floor(Date.now() / 1000) - 900 })];
for (let i = 2; i <= 9; i += 1) {
  rows.push(row("XR-" + i, "строка доски номер " + i));
}

// Заказ дословно, тем же способом, каким его собирает сервер (rowOrder,
// tools/dashboard/runs.go): у цели нет заказа, следующий виток сочиняет
// goal-run.py, а не дашборд.
function rowOrder(sect, r) {
  if (r.type === "goal") return "";
  if (sect === "in-progress") return "Продолжай выполнение " + r.id;
  if (sect === "check") return "Закрой " + r.id;
  return "Выполни " + r.id;
}

// Признак идущей работы дописывает строкам сервер (boardRuns, tasks.go), и
// игрушечный сервер повторяет ровно это: у строки с живой работой это то, чем
// работа видна, у строки In progress без работы gone. Идущий ход едет вторым
// признаком (run_busy): живое окно и идущий в нём ход это разные вещи, и «Стоп»
// кнопка получает только по второму. Заказ дописывается той же разметкой.
function marked(list, key) {
  return list.map((r) => {
    const live = works().find((w) => w.id === r.id);
    const run = live ? live.via : (key === "in-progress" ? "gone" : "");
    const busy = Boolean(live && live.live === "busy");
    const order = rowOrder(key, r);
    return Object.assign({}, r, run ? { run } : {}, busy ? { run_busy: true } : {},
      order ? { order } : {});
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
  // Состояние работы приезжает полем live, как его шлёт сервер: по нему экран
  // и называет её словом из словаря.
  return running ? [{ id: runningId, via: "tmux", live: "busy",
    title: "строка доски номер " + runningId.slice(3) }] : [];
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

// Снимки квоты: у первой подписки свежий с двумя бакетами, у второй его нет
// вовсе. Строка списка рисует остаток из этого же ответа, и «снимка нет» в ней
// обязано быть написано словами, а не нарисовано нулевой полоской.
function quotaBody() {
  return {
    dir: "/home/.devkit/quota",
    harnesses: [{
      name: harnessOne.name,
      age: "3м",
      stale: false,
      buckets: [
        { name: "week_all", used_pct: 52 },
        { name: "week_max", used_pct: 50 },
      ],
    }],
  };
}

// Заказ дословно, тем же способом, каким его собирает сервер (groomPrompt,
// tools/dashboard/drafts.go): подсказка кнопки читает готовое поле.
const drafts = [
  { id: "XR-D1", title: "первая запись накопителя", age_words: "вчера", order: "Проведи груминг XR-D1" },
  { id: "XR-D2", title: "вторая запись накопителя", age_words: "сегодня", order: "Проведи груминг XR-D2" },
  { id: "XR-D3", title: "третья запись накопителя", age_words: "сегодня", order: "Проведи груминг XR-D3" },
];

// Экран черновика (DK-321): исход груминга сервер читает следами на диске, а
// стенд подставляет его ответ прямо. Груминг идёт, пока жива работа с тем же
// ID, и стенд держит её отдельным признаком: ход разбора берётся снимком
// tmux-сессии, тем же, что на экране агента.
let grooming = false;
// Чат груминга: он и есть ход разбора, и по нему экран решает, звать ли
// разбор кнопкой или вести в стоящий чат.
let groomChat = null;

let groomAsk = null;
const groomed = [];
let dropped = null;

function groomWorks() {
  return grooming ? [{ id: "XR-D2", via: "tmux", live: "busy",
    title: "вторая запись накопителя" }] : works();
}

const talk = [
  { seq: 1, role: "user", text: "как дела с витком", time: "2026-08-13T10:00:00+03:00" },
  { seq: 2, role: "assistant", text: "виток идёт, задачи режу", time: "2026-08-13T10:01:00+03:00" },
  { seq: 3, role: "assistant", text: "нарезал три штуки", time: "2026-08-13T10:02:00+03:00" },
];

function reply(body) {
  return Promise.resolve({ ok: true, status: 200, statusText: "OK",
    text: () => Promise.resolve(JSON.stringify(body)), json: () => Promise.resolve(body) });
}

function refuse(status, body) {
  return Promise.resolve({ ok: false, status,
    text: () => Promise.resolve(JSON.stringify(body)), json: () => Promise.resolve(body) });
}

// Игрушечный taskctl под ручкой PATCH (DK-324): правка слагаемых пересчитывает
// сумму с полосой и переставляет очередь по паре ключей «ранг убыванием, номер
// возрастанием» (insertIdx в tools/taskctl/board.go), а ответ называет
// фактическое место строки соседями сверху и снизу. Порядок тут не выдумка
// стенда: считать его на клиенте как раз и нельзя, клиентский расчёт это
// превью.
const patched = [];

// Куда уходили POST-запросы: по ним видно, какой ручкой панель отправила
// реплику, ручкой цели, разговора или задачи.
const posted = [];

// Чужая правка, приехавшая вместе с ответом: соседи в ответе не те, что
// насчитал экран, пока шёл жест.
let boardAhead = null;

function idNum(id) {
  return Number(String(id).replace(/\D+/g, ""));
}

function sortBoard() {
  const tail = rows.slice(1).sort((a, b) => (b.r - a.r) || (idNum(a.id) - idNum(b.id)));
  rows.length = 1;
  for (const r of tail) rows.push(r);
}

function nearRow(row) {
  return row ? { id: row.id, title: row.title, r: row.r } : null;
}

function patchRow(id, body) {
  const row = rows.find((r) => r.id === id);
  if (!row) return refuse(404, { error: "на доске demo нет строки " + id });
  if (body.expect_r_parts && String(body.expect_r_parts) !== String(row.r_parts)) {
    return refuse(409, {
      error: "строку поправили, ранг сейчас " + row.r + " (" + row.r_parts.join("+") +
        "), откат не применён",
    });
  }
  row.r_parts = row.r_parts.map((was, i) => {
    const v = (body.r_parts || [])[i];
    return v === null || v === undefined ? was : v;
  });
  row.r = row.r_parts.reduce((sum, v) => sum + v, 0);
  row.p = row.r >= 75 ? "P0" : row.r >= 50 ? "P1" : row.r >= 25 ? "P2" : "P3";
  sortBoard();
  const back = rows.slice(1);
  const at = back.findIndex((r) => r.id === id);
  const place = { sect: "backlog", r: row.r, r_parts: row.r_parts, p: row.p };
  const above = nearRow(back[at - 1]);
  const below = nearRow(back[at + 1]);
  if (above) place.above = above;
  if (below) place.below = below;
  if (boardAhead) {
    Object.assign(place, boardAhead);
    boardAhead = null;
  }
  return reply({ id, message: id + ": R -> " + row.r, place });
}

// Медленный ответ: обещание, разрешающееся через десяток оборотов очереди
// микрозадач. Им ловится щель между запросом списка сессий и подъёмом потока
// ленты: уход с экрана попадает как раз в неё.
function slowReply(body) {
  let p = Promise.resolve();
  for (let i = 0; i < 20; i += 1) p = p.then(() => {});
  return p.then(() => ({ ok: true, status: 200,
    text: () => Promise.resolve(JSON.stringify(body)), json: () => Promise.resolve(body) }));
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
// Разговор, чью задачу узнать не удалось: в полосе живых работ он стоит
// карточкой без номера, и открывается она по id сессии, а не по ID задачи
// (DK-294).
const loose = {
  id: "cccc3333-3333", mtime: "2026-08-13T10:04:00+03:00", branch: "main",
  first: "почини роутер, доступы в local-docs", taskNote: "задача не распознана",
};
// Ручка для реплики и причина словами приезжают в шапке разговора (DK-438):
// панель по ним и решает, куда слать и когда гасить ввод.
const headExtra = { reply: "session", replyNote: "" };
// Реестр разговоров проекта (ручка /chats): панель берёт список отсюда, а не
// из живых сессий задачи, и открывает по нему свежий разговор. Задачи у
// разговора списком, а состояние словом, как их отдаёт сервер.
function chatOf(s, tasks) {
  return { id: s.id, title: s.first, mtime: s.mtime, tasks: tasks || (s.task ? [s.task] : []),
    state: "live", tree: s.tree || "", branch: s.branch || "", model: "opus" };
}
function chatList() {
  return [mine, alien, loose].map((s) => chatOf(s));
}
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

// Разговор с целью: лежащие строки «Входящих», отметки доставки и живость витка.
// Стенд правит её как правит файлы живая машина, а дашборд узнаёт о правке
// своим перечитыванием, без перезагрузки страницы.
const mail = { pending: [], delivered: [], live: false };

// Игрушечные таймеры: карточка ответа гаснет по времени, и ждать его
// по-честному стенду нечем. Заказанное складывается в список, а срабатывает по
// команде стенда.
const timers = [];
let timerSeq = 0;

// Все спрошенные адреса: по ним видно, за чем экран ходит, а за чем нет.
const asked = [];

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
  // История: панель разговора открывается pushState, поэтому «назад» её
  // закрывает и возвращает доску на прежнее место. Стенд помнит стопку сам,
  // hashchange по ней зовётся тем же ходом, что и в браузере.
  history: {
    stack: [],
    pushState: (state, title, url) => {
      sandbox.history.stack.push(sandbox.location.hash);
      sandbox.location.hash = String(url).replace(/^#/, "");
    },
    back: () => {
      const was = sandbox.history.stack.pop();
      sandbox.location.hash = was === undefined ? "" : was;
      for (const fn of sandbox.window.listeners.hashchange || []) fn();
    },
  },
  // Хранилище настоящее: в нём живёт ширина панели разговора и очередь
  // исходящих, и заглушка с вечным null проверяла бы не то.
  localStorage: {
    getItem: (key) => (store.has(key) ? store.get(key) : null),
    setItem: (key, value) => { store.set(key, String(value)); },
    removeItem: (key) => { store.delete(key); },
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
    if (post) posted.push(path);
    asked.push(path);
    if (path === "/api/projects") {
      return reply({ projects: [{ name: "demo", works: groomWorks(), sections: { check: 1 } }] });
    }
    if (path.endsWith("/runs") && post) {
      running = true;
      const sent = init && init.body ? JSON.parse(init.body) : {};
      if (sent.id) runningId = sent.id;
      started.push(sent);
      return reply({ message: "сессия поднята" });
    }
    if (path.includes("/tasks/") && init && init.method === "PATCH") {
      const sent = JSON.parse(init.body);
      patched.push(sent);
      return patchRow(decodeURIComponent(path.slice(path.lastIndexOf("/") + 1)), sent);
    }
    // Экран задачи (#проект/<ID>): строка та же, что на доске, разметка заказа
    // и полосы действий та же самая (handleTask, tools/dashboard/tasks.go).
    if (path.includes("/tasks/") && (!init || !init.method)) {
      const id = decodeURIComponent(path.slice(path.lastIndexOf("/") + 1));
      const r = rows.find((x) => x.id === id);
      if (!r) {
        // Закрытая задача уехала в архив, но экран у неё есть: сервер отдаёт
        // строку архива с датой закрытия и файл постановки из архива.
        const arch = archived.find((a) => a.id === id);
        if (arch) {
          return reply({ project: "demo", id, after: [], blocks: [],
            row: { id, title: arch.title, closed: arch.closed, type: "task", cost: "-" },
            file: "docs/tasks/archive/2026/" + id + ".md", text: "# " + id + "\n\nпостановка из архива" });
        }
        return refuse(404, { error: "на доске demo нет строки " + id });
      }
      const sect = id === "XR-1" ? "in-progress" : "backlog";
      // Признаки работы форма задачи получает те же, что и строка доски:
      // сервер размечает их одной разметкой на оба экрана (handleTask зовёт те
      // же runMarks и busyMarks, tasks.go). Иначе одна и та же задача
      // выглядела бы на форме не так, как в списке.
      return reply({
        project: "demo", id,
        row: Object.assign({}, marked([r], sect)[0], { sect, section: sect }),
        after: [], blocks: [],
        file: "docs/tasks/" + id + ".md",
        // Постановка со служебным разделом: такие разделы экран сворачивает
        // строкой с объёмом, и раскрытый раздел это одно из того, что человек
        // терял на пересборке экрана (DK-411).
        text: "# " + id + "\n\nпостановка задачи\n\n## Журнал\n\n" +
          "запись журнала раз\nзапись журнала два\n",
      });
    }
    if (path === "/api/harnesses") return reply(harnessBody());
    if (path.endsWith("/board")) return reply(boardBody());
    if (path.endsWith("/drafts")) return reply({ drafts });
    if (path.includes("/chats?task=")) return reply({ chats: groomChat ? [groomChat] : [] });
    if (path.includes("/chats")) return reply({ chats: chatList() });
    if (path.endsWith("/groom") && post) {
      groomAsk = JSON.parse(init.body).ask || "";
      const gid = path.slice(path.indexOf("/drafts/") + "/drafts/".length, path.indexOf("/groom"));
      // Каждый поднятый разбор помнится отдельно: пачка поднимает по сессии на
      // запись, и проверять надо все, а не последнюю.
      groomed.push(gid);
      return reply({ message: "груминг " + gid + " поднят в tmux-сессии task-" + gid });
    }
    if (path.includes("/drafts/") && init && init.method === "DELETE") {
      dropped = JSON.parse(init.body).reason || "";
      return reply({ message: "XR-D2 удалён как протухший" });
    }
    if (path.includes("/drafts/")) {
      return reply({ file: "docs/tasks/drafts/XR-D2.md", text: "текст записи\nвторая строка" });
    }
    if (path === "/api/tmux") {
      return reply({ sessions: grooming ? [{ name: "task-XR-D2", windows: 1, created: 0 }] : [] });
    }
    if (path.startsWith("/api/tmux/")) return reply({ text: "хвост груминга" });
    if (path.includes("/sessions?task=")) {
      return slowSessions ? slowReply({ sessions }) : reply({ sessions });
    }
    // Разговор по id сессии: сервер отдаёт вместе с лентой шапку, из неё экран
    // и собирает заголовок, когда задача разговора не узнана.
    if (path.includes("/sessions/")) {
      const sid = path.slice(path.indexOf("/sessions/") + "/sessions/".length).split("?")[0];
      const found = [mine, alien, loose].find((s) => s.id === sid) || { id: sid };
      return reply({ session: sid, head: Object.assign({}, found, headExtra), items: talk });
    }
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
    // Чат цели: «Входящие» файла цели, отметки доставки подхвата реплики
    // (.devkit/goal-<ID>.mail) и живость витка, которую сервер судит правилом
    // подхвата (goalLive, tools/dashboard/mail.go).
    if (path.includes("/sessions/") && path.endsWith("/message")) {
      return reply({ session: "aaaa1111-1111", chat: "task-XR-1",
        message: "реплика легла в разговор task-XR-1 дерева сессии" });
    }
    if (path.includes("/message")) {
      if (post) {
        const line = "2026-08-15 10:00, из дашборда: " + JSON.parse(init.body).text;
        mail.pending.push(line);
        return reply({ id: "XR-1", line: "- " + line,
          message: "сообщение легло во «Входящие» файла цели XR-1" });
      }
      return reply({ id: "XR-1", pending: mail.pending, delivered: mail.delivered, live: mail.live });
    }
    if (path === "/api/notifications") return reply({ items: [] });
    if (path === "/api/quota") return reply(quotaBody());
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
const card = find(groups, "sec-backlog");
if (!card) fail("доска не собралась: карточки Backlog на экране нет");
// Заголовок раздела: путь доски с префиксом ID стоял строкой рядом с названием
// проекта и отвечал на вопрос, которого никто не задавал (замечание
// пользователя). Приписки «задачи проекта» рядом тоже нет: открытый таб виден
// подсветкой, и слово повторяло её (решение пользователя). Знание про файл
// доски осталось подсказкой на самом названии.
{
  const name = byId.get("pname");
  if (!String(name.title || "").includes("docs/TASKS.md")) {
    fail("путь доски пропал вовсе, а он нужен подсказкой: " + JSON.stringify(name.title));
  }
}
const third = find(groups, "XR-3");
if (!third) fail("строки XR-3 на доске нет: " + dump(groups).slice(0, 200));
const act = button(third, "Выполнить");
if (!act) fail("у строки нет кнопки действия: " + dump(third));
// Подсказка кнопки называет заказ дословно, той же строкой, что уйдёт
// headless-сессии (row.order): до DK-286 надпись была общей, и нажимать
// было страшно.
if (!String(act.title).includes("Выполни XR-3")) {
  fail("подсказка кнопки строки не называет заказ дословно: " + JSON.stringify(act.title));
}
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
if (find(groups, "sec-backlog") !== card) {
  fail("карточка секции пересобрана заново, хотя строки в ней те же");
}

// Строка в работе без живой сессии стоит без чипа: приписка «сессии нет»
// занимала место в каждой такой строке и ни к какому действию не звала
// (замечание пользователя). Что со строкой происходит, говорит кружок у
// номера, а делать с ней что-то предлагает кнопка.
const goalRow = find(groups, "XR-1");
if (dump(goalRow).includes("сессии нет")) {
  fail("чип «сессии нет» вернулся в строку доски: " + dump(goalRow));
}
// Составной кнопки запуска в строке больше нет вовсе: выбор подписки уехал в
// меню под тремя точками (DK-349 про глубину узлов тем и снят).
if (byClass(goalRow, "split")) fail("кнопка запуска цели обёрнута в split: " + dump(goalRow));

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
// Вид кнопок держат макеты 11 и 12 (DK-337): стоп в строке красный, как и на
// карточке живой работы, а не серый.
if (!String(button(now, "Стоп").className).includes("btn-danger")) {
  fail("стоп в строке нарисован не красной кнопкой: " + button(now, "Стоп").className);
}
// Стоп это одиночная кнопка без узкой части, не обёрнута в split (DK-349).
if (byClass(now, "split")) fail("стоп в строке обёрнут в split: " + dump(now));
// Главной кнопкой у поднятой работы стоит чат, а стоп идёт рядом с ним: за
// разговором с идущей работой ходят чаще, чем снимают её.
if (byClass(now, "rmain")) fail("у идущей работы главной осталась кнопка запуска: " + dump(now));
// Признак выполнения стоит кружком у номера, а не словом в чипе: зелёная
// точка с подсказкой вместо чипа «работает» (POC ветки poc-chat).
const dot = byClass(now, "sdot");
if (!dot || !String(dot.className).includes("sd-run")) {
  fail("у идущей работы в строке нет зелёного кружка: " + (dot && dot.className) + " " + dump(now));
}
if (!String(dot.title).includes("работает")) {
  fail("кружок идущей работы молчит о состоянии: " + dot.title);
}
if (dump(now).includes("работает")) {
  fail("рядом с кружком снова стоит чип со словом: " + dump(now));
}
// Строка, за которой никто не стоит, кружка не носит вовсе.
if (byClass(find(groups, "XR-4"), "sdot")) {
  fail("кружок встал у строки без живой работы: " + dump(find(groups, "XR-4")));
}
// Остальные случаи кружка проверяются прямо на строке данных: ожидание
// человека читается раньше живости, чужая сессия и ожидание снаружи серые.
{
  const kinds = (row) => {
    const d = sandbox.rowDot("demo", row);
    return d ? String(d.className) : "";
  };
  const wait = { id: "XR-9", run: "tmux", waiting: { state: "ждёт ответа", note: "спросил агент",
    questions: ["Какой дубль оставить?"] } };
  if (!kinds(wait).includes("sd-wait")) fail("ждущая строка не жёлтая: " + kinds(wait));
  const tip = String(sandbox.rowDot("demo", wait).title);
  if (!tip.includes("спросил агент") || !tip.includes("Какой дубль")) {
    fail("подсказка ожидания без источника или вопроса: " + tip);
  }
  if (!kinds({ id: "XR-9", run: "registry" }).includes("sd-out")) {
    fail("чужая сессия нарисована не серым: " + kinds({ id: "XR-9", run: "registry" }));
  }
  if (!kinds({ id: "XR-9", stage: "снаружи" }).includes("sd-out")) {
    fail("ожидание снаружи нарисовано не серым: " + kinds({ id: "XR-9", stage: "снаружи" }));
  }
  if (kinds({ id: "XR-9", run: "gone" })) fail("оборванный конвейер получил кружок");
  if (kinds({ id: "XR-9" })) fail("строка без работы получила кружок");
}
if (!String(doc.activeElement.className || "").includes("btn")) {
  fail("после нажатия фокус ушёл со строки: " + dump(doc.activeElement));
}

// Выбор подписки при запуске (DK-326) живёт всплывашкой кнопки запуска. Само
// нажатие поднимает работу на подписке по умолчанию, правая кнопка и долгое
// нажатие открывают выбор, а строка подписки запускает работу на своей без
// второго нажатия. Проверяется тело запроса: нарисованный список без доехавшего
// имени это ровно та поломка, ради которой задача и заведена.
if (started[started.length - 1].harness !== harnessOne.name) {
  fail("широкая часть кнопки ушла не на подписку по умолчанию: " +
    JSON.stringify(started[started.length - 1]));
}
running = false;
await sandbox.refresh();
await settle();
const pickRow = find(groups, "XR-5");
const grp = byClass(pickRow, "racts");
if (!grp) fail("у строки нет ряда действий: " + dump(pickRow));
if (!byClass(grp, "rmain")) fail("у свободной строки нет главной кнопки запуска: " + dump(grp));
const more = byClass(grp, "rdots");
if (!more) fail("у строки нет трёх точек с выбором подписки: " + dump(grp));
if (more.disabled) fail("у свободной строки три точки погашены: " + dump(grp));
const pop = byClass(grp, "rmenu");
if (!pop || !pop.hidden) fail("выбор подписки открыт до нажатия");
more.handlers.click({ stopPropagation: () => {} });
if (pop.hidden) fail("три точки не открыли выбор подписки");
// Шапка и две подписки. Подвала под списком нет вовсе: он объяснял, откуда
// список и надолго ли выбор, и пользователь забраковал его прямой оценкой.
if (!dump(byClass(pop, "hph")).includes("На какой подписке запустить")) {
  fail("у списка подписок нет шапки: " + dump(pop));
}
if (byClass(pop, "hfoot")) fail("подвал списка вернулся: " + dump(pop));
if (dump(pop).includes("agentctl harness")) {
  fail("приписка про раскладку машины вернулась в список: " + dump(pop));
}
const hrows = allByClass(pop, "hrow");
if (hrows.length !== 2) {
  fail("в списке подписок " + hrows.length + " строк, ждал две: " + dump(pop));
}
if (!String(hrows[0].className).includes("on") || String(hrows[1].className).includes("on")) {
  fail("подписка запуска в списке не подсвечена: " + hrows.map((r) => r.className).join(" | "));
}
// Строка подписки это одна полоса: имя и два процента остатка, и ничего
// больше. Прежняя везла ещё чип «по умолчанию», две полоски-градусника с
// датами сброса и возраст снимка, и меню от этого раздувалось вчетверо
// (замечание пользователя). Признак умолчания остался подсветкой, а даты с
// возрастом ушли в подсказку строки.
if (byClass(hrows[0], "chip")) fail("чип вернулся в строку подписки: " + dump(hrows[0]));
if (byClass(hrows[0], "qrow") || byClass(hrows[0], "meter")) {
  fail("полоска-градусник вернулась в строку подписки: " + dump(hrows[0]));
}
if (dump(hrows[0]).includes("снимок 3м назад")) {
  fail("возраст снимка вернулся в строку подписки: " + dump(hrows[0]));
}
const quotas = allByClass(hrows[0], "hq");
if (quotas.length !== 2) {
  fail("в строке подписки не два числа остатка: " + dump(hrows[0]));
}
if (!dump(hrows[0]).includes("52%") || !dump(hrows[0]).includes("week_all")) {
  fail("строка подписки молчит про остаток: " + dump(hrows[0]));
}
// Подсказка везёт всё, что ушло со строки: возраст снимка и дату сброса.
if (!String(hrows[0].title || "").includes("снимок 3м назад")) {
  fail("подсказка строки потеряла возраст снимка: " + hrows[0].title);
}
if (!dump(hrows[1]).includes("снимка нет")) {
  fail("строка без снимка квоты об этом молчит: " + dump(hrows[1]));
}
if (!dump(pop).includes(harnessTwo.name)) {
  fail("список подписок не называет имён: " + dump(pop));
}
hrows[1].handlers.click({ stopPropagation: () => {} });
await settle();
if (started[started.length - 1].harness !== harnessTwo.name) {
  fail("строка списка подняла работу не на своей подписке: " +
    JSON.stringify(started[started.length - 1]));
}
if (!pop.hidden) fail("список подписок остался открытым после выбора");

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
  if (byClass(one, "more2")) fail("при одной подписке в строке осталась стрелка выбора: " + dump(one));
  if (byClass(one, "split")) fail("кнопка запуска при " + why + " обёрнута в split: " + dump(one));
  // Выбирать нечего, и меню строке не нужно: под тремя точками остаётся один
  // чат, а вот выбора подписки с ярусом там уже нет.
  if (allByClass(one, "hrow").length) {
    fail("при " + why + " в меню строки предложен выбор подписки: " + dump(one));
  }
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

// Фокус переживает подъём работы (DK-349, DK-316): кнопки строки лежат одним
// рядом на одной глубине, и позиционный путь focusSnap/focusBack
// (app.js:82-113) после смены состава кнопок попадает в кнопку той же строки, а
// не мимо неё. Первой в ряду стоит кнопка работы, и у поднятой работы это
// «Стоп»: место у кнопок своё, меняется только то, что они делают.
harnessList = [harnessOne];
harnessNote = "";
running = false;
await sandbox.refresh();
await settle();
const degRow = find(groups, "XR-6");
const degRun = button(degRow, "Выполнить");
if (!degRun) fail("вырожденная строка осталась без кнопки запуска: " + dump(degRow));
degRun.focus();
degRun.handlers.click({ stopPropagation: () => {} });
await settle();
if (started[started.length - 1].id !== "XR-6") {
  fail("кнопка вырожденной строки подняла не ту работу: " + JSON.stringify(started[started.length - 1]));
}
await sandbox.refresh();
await settle();
const degLive = find(groups, "XR-6");
const degStop = button(degLive, "Стоп");
if (!degStop) fail("вырожденная строка не показала Стоп после запуска: " + dump(degLive));
const degTalk = button(degLive, "Чат по задаче XR-6");
if (!degTalk) fail("у поднятой работы пропала кнопка чата: " + dump(degLive));
if (doc.activeElement !== degStop) {
  fail("подъём работы увёл фокус мимо кнопок строки: " + dump(doc.activeElement));
}
running = false;
degStop.handlers.click({ stopPropagation: () => {} });
await settle();
await sandbox.refresh();
await settle();
const degBack = find(groups, "XR-6");
const degAgain = button(degBack, "Выполнить");
if (!degAgain) fail("вырожденная строка не вернула кнопку запуска после Стопа: " + dump(degBack));
if (doc.activeElement !== degAgain) {
  fail("переход Стоп -> Run на вырожденной строке промахнулся мимо кнопки: " + dump(doc.activeElement));
}
running = false;

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

// Экран задачи (#проект/<ID>): подсказка кнопки называет заказ дословно, той
// же строкой, что уйдёт headless-сессии (row.order, handleTask в
// tools/dashboard/tasks.go), а удачный запуск ведёт на экран этой работы,
// не оставляя человека на месте (DK-286).
running = false;
await go("#demo/XR-6");
const taskRun = actionButton(groups, "Выполнить");
if (!taskRun) fail("на экране задачи нет кнопки запуска: " + dump(groups).slice(0, 300));
if (!String(taskRun.title).includes("Выполни XR-6")) {
  fail("подсказка кнопки запуска не называет заказ дословно: " + JSON.stringify(taskRun.title));
}
// Надписи под полосой действий нет вовсе: она пересказывала устройство
// («конвейер получит заказ в tmux-сессии, поедет на claude-code») там, где
// человек жмёт кнопку с понятной подписью (замечание пользователя). Заказ
// остался подсказкой самой кнопки, а подписку называет её выпадашка.
for (const gone of ["Конвейер получит заказ", "в tmux-сессии task-XR-6",
  "Поедет на " + harnessOne.name]) {
  if (dump(groups).includes(gone)) {
    fail("под полосой действий осталась надпись «" + gone + "»: " + dump(groups).slice(0, 400));
  }
}
timers.length = 0;
byId.get("flashes").replaceChildren();
taskRun.handlers.click({ stopPropagation: () => {} });
await settle();
// Запуск разговор не открывает вовсе: человек, разбиравший итоги соседней
// задачи, вылетал из её чата на каждое нажатие «Выполнить» (замечание
// пользователя). Экран остаётся на задаче, а след запуска виден рамкой у
// кнопки её чата.
if (sandbox.location.hash !== "demo/XR-6") {
  fail("удачный запуск увёл с экрана задачи: " + sandbox.location.hash);
}
if (!dump(byId.get("flashes")).includes("сессия поднята")) {
  fail("переход на экран работы стёр карточку ответа на нажатие: " + dump(byId.get("flashes")));
}
for (const t of timers.splice(0)) t.fn();
byId.get("flashes").replaceChildren();

// Черновики: строки и прокрутка переживают обновление, а разбор запускается не
// из строки. Кнопка в строке уводила на форму записи вместо запуска (нажатие
// всплывало до обработчика строки), и устройство пересмотрено: строки держат
// отметки выбора, а запуск один на выбранное и стоит над списком (решение
// пользователя).
running = false;
await go("#demo/drafts");
const wrap = find(groups, "XR-D2");
if (!wrap) fail("накопитель не собрался: записи XR-D2 нет");
if (button(wrap, "Грумить")) {
  fail("кнопка разбора осталась в строке накопителя: " + dump(wrap));
}
if (!byClass(wrap, "dpick")) fail("в строке накопителя нет отметки выбора: " + dump(wrap));
// Заголовок записи режется кромкой строки, и подсказка с полным текстом у неё
// такая же, как у строки доски: мысль с телефона длинная, а читать её, заходя
// внутрь каждой записи, незачем (замечание пользователя).
{
  const st = byClass(wrap, "st");
  if (!st) fail("в строке накопителя нет заголовка: " + dump(wrap));
  if (st.title !== "вторая запись накопителя") {
    fail("у строки накопителя нет подсказки с полным заголовком: " + JSON.stringify(st.title));
  }
}
groups.scrollTop = 120;

await sandbox.refresh();
await settle();

if (find(groups, "XR-D2") !== wrap) {
  fail("список черновиков пересобран целиком: строки уехали из-под пальца");
}
if (groups.scrollTop !== 120) {
  fail("обновление накопителя сбило прокрутку: " + groups.scrollTop + " вместо 120");
}

// Пока ничего не выбрано, запускать нечего, и кнопки нет вовсе: гашеная кнопка
// отвечала на вопрос, которого никто не задавал, а что отметки бывают, говорит
// подпись рядом (решение пользователя).
// Число выбранных стоит в самой подписи кнопки: подтверждения перед подъёмом
// нет, и сказать, сколько сессий встанет, надо до нажатия.
const runBtn = (n) => button(groups, "Грумить" + (n ? " (" + n + ")" : ""));
{
  if (runBtn()) fail("кнопка запуска стоит при пустом выборе: " + dump(groups).slice(0, 400));
  const said = dump(groups).replace(/\s+/g, " ");
  if (!said.includes("Отметьте записи")) {
    fail("при пустом выборе не сказано, откуда берётся разбор: " + said.slice(0, 400));
  }
}

// Выбор двух записей поднимает по сессии на каждую, и экран остаётся на месте.
{
  groomed.length = 0;
  timers.length = 0;
  byId.get("flashes").replaceChildren();
  for (const id of ["XR-D1", "XR-D3"]) {
    const row = find(groups, id);
    if (!row) fail("в накопителе нет записи " + id + ": " + dump(groups).slice(0, 300));
    byClass(row, "dpick").handlers.click({ stopPropagation: () => {} });
    await settle();
  }
  const btn = runBtn(2);
  if (!btn) fail("кнопка запуска не назвала число выбранных: " + dump(groups).slice(0, 400));
  // Подтверждения нет вовсе: нажатие поднимает разбор сразу, а вопрос поверх
  // сделанного выбора человек назвал лишним. Прежняя карточка вдобавок
  // оставляла исходную кнопку доступной поверх себя.
  btn.handlers.click({ stopPropagation: () => {} });
  await settle();
  const said = dump(groups).replace(/\s+/g, " ");
  for (const word of ["Поднимется", "Поднять 2", "Отмена"]) {
    if (said.includes(word)) fail("подтверждение вернулось на экран: " + word);
  }
  if (JSON.stringify(groomed) !== JSON.stringify(["XR-D1", "XR-D3"])) {
    fail("подъём пошёл не по сессии на запись: " + JSON.stringify(groomed));
  }
  // Экран остаётся на накопителе: запуск это не переход к записи.
  if (String(sandbox.location.hash).replace(/^#/, "") !== "demo/drafts") {
    fail("запуск разбора увёл с накопителя: " + sandbox.location.hash);
  }
  // Выбор снят: пачка сделана, и отметки не висят до следующего захода.
  const marked = (node) => {
    let count = String(node.className || "").includes("dpick") &&
      String(node.className || "").includes("on") ? 1 : 0;
    for (const kid of node.children || []) count += marked(kid);
    return count;
  };
  if (marked(groups)) fail("после запуска отметки выбора остались: " + marked(groups));
  for (const t of timers.splice(0)) t.fn();
}

// Идущий разбор пропускается словами, а не отказом всей пачке.
{
  grooming = true;
  await go("#demo/drafts");
  groomed.length = 0;
  for (const id of ["XR-D1", "XR-D2"]) {
    const row = find(groups, id);
    byClass(row, "dpick").handlers.click({ stopPropagation: () => {} });
    await settle();
  }
  runBtn(2).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(groomed) !== JSON.stringify(["XR-D1"])) {
    fail("поднялся не только непочатый разбор: " + JSON.stringify(groomed));
  }
  // Про пропущенное сказано строкой итога, как у запуска задачи, а не
  // карточкой поверх экрана.
  const said = dump(byId.get("flashes")).replace(/\s+/g, " ");
  if (!said.includes("пропущено")) {
    fail("про идущий разбор не сказано ни слова: " + said.slice(0, 400));
  }
  if (!said.includes("XR-D2")) fail("не названо, какую запись пропускают: " + said.slice(0, 400));
  grooming = false;
  for (const t of timers.splice(0)) t.fn();
  byId.get("flashes").replaceChildren();
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

// Строка выдачи нажимается, и это видно до нажатия: класс нажимаемой строки
// тот же, что у накопителя, а с ним приходит и курсор (замечание 3).
await go("#demo/find/" + encodeURIComponent("колокольчик"));
const litRow = find(groups, "find-card-archive").children[0];
if (!litRow.classList.contains("clicky")) {
  fail("строка выдачи не помечена нажимаемой: " + litRow.className);
}

// Закрытая задача из выдачи открывается экраном чтения, а не отказом: строки на
// доске у неё нет, зато есть файл в архиве (замечание 4).
litRow.handlers.click();
await settle();
if (sandbox.location.hash !== "demo/XR-90") {
  fail("архивная строка выдачи не увела на экран задачи: " + sandbox.location.hash);
}
await sandbox.refresh();
await settle();
{
  const shown = dump(groups);
  if (!shown.includes("закрыта 2026-08-01")) fail("экран закрытой задачи не назвал дату: " + shown.slice(0, 300));
  if (!shown.includes("постановка из архива")) fail("экран закрытой задачи не показал файл: " + shown.slice(0, 300));
  if (shown.includes("Сохранить")) fail("у закрытой задачи нарисованы правки: " + shown.slice(0, 400));
}

// Черновик из выдачи ведёт на свой экран, а не в общий накопитель: разбор
// начинают с той записи, которую нашли.
await go("#demo/find/" + encodeURIComponent("XR-D2"));
const draftHit = find(groups, "find-card-drafts");
if (draftHit) {
  draftHit.children[0].handlers.click();
  if (sandbox.location.hash !== "demo/draft/XR-D2") {
    fail("черновик выдачи не увёл на свой экран: " + sandbox.location.hash);
  }
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
// Курсор ставится один раз, на заходе: обновление по фокусу окна тянуло бы его
// обратно в поле у человека, который уже ушёл читать выдачу.
doc.body.focus();
await sandbox.refresh();
await settle();
if (doc.activeElement !== doc.body) {
  fail("обновление вернуло курсор в поле поиска: " + dump(doc.activeElement));
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
// Курсор нажимаемой строки берётся из статики, а не с честного слова стенда:
// класс без правила это та же стрелка, от которой строка выдачи и читалась
// подписью (замечание 3 четырнадцатого круга POC).
if (!/\.srow\.clicky\{cursor:pointer\}/.test(findCSS.replace(/\s+/g, ""))) {
  fail("у нажимаемой строки нет курсора руки: правила .srow.clicky в статике нет");
}

// Строка накопителя ведёт на экран записи: разворот текста в строке уступил
// место одному экрану с текстом, ходом груминга и его исходом.
wrap.handlers.click({ target: makeNode("span") });
if (sandbox.location.hash !== "demo/draft/XR-D2") {
  fail("нажатие на строку накопителя не открыло экран записи: " + sandbox.location.hash);
}

// Экран черновика: шапка с ID, полоса пометок, полоса действий, исход разбора
// и текст записи разметкой. Карточки хода и удаления с экрана сняты
// двенадцатым кругом POC (замечание 15): разбор идёт живым чатом груминга, и
// смотрят его там же, а не снимком tmux рядом. Собран экран той же формой, что
// задача и заведение (общий formPage), поэтому груминг стоит кнопкой полосы
// действий, там же, где у задачи «Выполнить», а не среди пометок. Исхода
// разбора форма не пересказывает вовсе: он виден в чате и на доске.
grooming = true;
await go("#demo/draft/XR-D2");
const dhead = find(groups, "draft-head");
if (!dhead) fail("экран черновика не собрался: " + dump(groups).slice(0, 200));
if (!dump(dhead).includes("XR-D2")) fail("шапка записи не назвала ID: " + dump(dhead));
// Пометки стоят своей полосой: шапка держит ID с заголовком, и перерисовываются
// они врозь.
let dchips = find(groups, "draft-chips");
if (!dchips) fail("полосы пометок черновика нет: " + dump(groups).slice(0, 200));
if (!dump(dchips).includes("черновик")) fail("запись не помечена черновиком: " + dump(dchips));
if (!dump(dchips).includes("груминг идёт")) {
  fail("идущий груминг ничем не помечен: " + dump(dchips));
}
if (barButton(groups, "Грумить")) {
  fail("поверх идущего груминга экран предлагает поднять второй: " + dump(groups));
}
// Пока разбор идёт, запись принадлежит агенту: карандаш с неё уходит, а режим
// чтения остаётся, читать её никто не мешает. Замок и его слова проверяет свой
// стенд, poc_draftlock.
if (barButton(dchips, "Править запись")) {
  fail("карандаш стоит под живым разбором: " + dump(dchips));
}
if (!barButton(dchips, "Режим чтения")) {
  fail("у записи нет режима чтения: " + dump(dchips));
}
// Текст записи стоит разметкой, а не сырым файлом.
const dtext = find(groups, "draft-text");
if (!dtext || !dump(dtext).includes("текст записи")) {
  fail("текста записи на экране нет: " + dump(dtext));
}

// Груминг кончился, чата от него не осталось: кнопка разбора возвращается на
// место, и заказ агенту виден подсказкой.
grooming = false;
await go("#demo/draft/XR-D2");
dchips = find(groups, "draft-chips");
if (dump(dchips).includes("груминг идёт")) fail("кончившийся груминг помечен идущим: " + dump(dchips));
const groomBtn = barButton(groups, "Грумить");
if (!groomBtn) fail("кнопки груминга нет на записи без разбора: " + dump(groups).slice(0, 300));
// Разбор кончился, замок снят: карандаш возвращается на место.
if (!barButton(dchips, "Править запись")) {
  fail("карандаш не вернулся после разбора: " + dump(dchips));
}
groomBtn.handlers.click({ stopPropagation: () => {} });
await settle();
if (groomAsk === null) fail("кнопка груминга не позвала ручку разбора");
groomAsk = null;

// Разговор о записи разбором не считается: груминг это его собственная
// tmux-сессия, а всякий чат про черновик это просто чат (замечание
// пользователя по живой записи DK-502). Кнопка разбора при таком соседе
// остаётся на месте.
groomChat = { id: "gggg4444-4444", title: "Груминг XR-D2", mtime: "2026-08-13T10:00:00+03:00",
  tasks: ["XR-D2"], state: "dead" };
await go("#demo/draft/XR-D2");
if (barButton(groups, "Чат груминга")) {
  fail("на форме вернулась вторая дверь в тот же чат: " + dump(groups).slice(0, 300));
}
if (!barButton(groups, "Грумить")) {
  fail("кнопка разбора пропала из-за чата о записи: " + dump(groups).slice(0, 300));
}

// Обновление по фокусу окна экран не пересобирает: ключи те же, и узлы стоят
// прежние.
const keptHead = find(groups, "draft-head");
await sandbox.refresh();
await settle();
if (find(groups, "draft-head") !== keptHead) {
  fail("обновление пересобрало шапку записи, хотя ответ сервера тот же");
}
groomChat = null;

// Карточек исхода на форме нет ни одной, и за исходом экран больше не ходит
// вовсе: ручка `/drafts/{id}/outcome` снесена, разговор с агентом всегда идёт в
// чате, там же виден и исход, а на доске он виден по факту, строкой или её
// отсутствием (решение пользователя).
{
  asked.length = 0;
  await go("#demo/draft/XR-D2");
  const shown = dump(groups).replace(/\s+/g, " ");
  for (const gone of ["Черновик оформлен строкой", "Черновик приписан", "Черновик отложен",
    "Черновик удалён", "Груминг кончился", "Груминга не было", "Исход груминга",
    "Открыть задачу"]) {
    if (shown.includes(gone)) {
      fail("исход пересказан на форме («" + gone + "»): " + shown.slice(0, 300));
    }
  }
  if (asked.some((p) => p.includes("/outcome"))) {
    fail("экран записи всё ещё ходит за исходом: " + JSON.stringify(asked));
  }
  // Сама запись при этом на месте: форма держит черновик и действия над ним.
  if (!shown.includes("текст записи")) {
    fail("вместе с карточками исхода пропала сама запись: " + shown.slice(0, 300));
  }
}

// Панель разговора (DK-435): она стоит справа поверх любого экрана проекта и
// живёт хвостом адреса. Лента, поле ввода и поток событий переживают
// обновление экрана под ней, а пришедшая реплика не трогает соседних.
const cpin = byId.get("cpin");
// В контейнере панели живёт пул слотов: показан один, прочие спрятаны и лежат
// готовыми к возврату. Стенд смотрит на показанный, спрятанное это память
// пула, а не экран.
const livePin = () => (cpin.children || []).find(
  (n) => String(n.className || "").includes("cslot") &&
    !String(n.className || "").split(" ").includes("off")) || cpin;
const panelNode = byId.get("cpanel");
await go("#demo/chat/XR-1");
if (panelNode.hidden) fail("панель разговора не открылась по адресу с хвостом");
const talkBox = byClass(livePin(), "chatwrap");
if (!talkBox) fail("тело панели не собралось: " + dump(cpin).slice(0, 300));
// Экран под панелью остался доской: панель это хвост адреса, а не свой экран.
if (!find(groups, "sec-backlog")) {
  fail("открытие панели пересобрало экран под ней: доски больше нет");
}

const feed = talkBox.children[0];
const list = feed.children[1];
const first = find(list, "seq-1");
if (!first) fail("лента панели пуста: " + dump(feed));
const ta = tag(talkBox, "TEXTAREA");
if (!ta) fail("в панели нет поля ввода");
if (ta.disabled) fail("поле ввода погашено без причины: " + dump(note));
ta.value = "набранный ответ";
ta.selectionStart = 7;
ta.selectionEnd = 7;
ta.focus();
const opened = streams.length;

await sandbox.refresh();
await settle();

if (byClass(livePin(), "chatwrap") !== talkBox) {
  fail("обновление экрана пересобрало панель целиком");
}
if (tag(talkBox, "TEXTAREA") !== ta || ta.value !== "набранный ответ") {
  fail("обновление стёрло набранное в поле ввода: " + ta.value);
}
if (doc.activeElement !== ta) fail("обновление отобрало фокус у поля ввода панели");
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

// Работа агента поднялась: плашка перестаёт звать цикл стоящим, а разговор
// остаётся тем же. Панель собрана один раз на разговор, и правится в ней от
// перерисовки ровно эта плашка.
running = true;
// Работа идёт по XR-1: игрушечный сервер держит её одним источником, и
// признаки строки собираются по нему же, иначе форма задачи узнает о работе
// не то, что список.
runningId = "XR-1";
rows[0].id = "XR-1";
const chatWork = [{ id: "XR-1", via: "tmux", live: "busy", title: "Цель: дашборд без дёрганья" }];
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

if (byClass(livePin(), "chatwrap") !== talkBox) {
  fail("поднявшаяся работа пересобрала панель");
}
if (doc.activeElement !== ta) fail("обновление отобрало фокус у поля ввода");
if (wasWorks !== works) fail("стенд подменил источник работ мимо ручек");

// Работа задачи ведётся не только tmux-сессией дашборда: цель из реестра и
// живое окно человека для сервера та же живая работа (goalIdle в messages.go).
// Панель на этом не пересобирается ни при одном из признаков: она живёт
// адресом, а не списком работ.
for (const via of ["registry", "session"]) {
  chatWork[0].via = via;
  await sandbox.refresh();
  await settle();
  if (byClass(livePin(), "chatwrap") !== talkBox) {
    fail("работа via " + via + " пересобрала панель разговора");
  }
}
chatWork[0].via = "tmux";

// Отправленная реплика встаёт местным пузырём сразу под лентой и сама говорит,
// что с ней: на слабой связи молчание неотличимо от непрошедшей отправки, и
// человек жмёт «Отправить» второй раз. Очередь «Входящих» цели тут больше не
// при чём: реплика панели уезжает в саму сессию, а лежащее у цели живёт своим
// стендом (outbox_offline).
const pendbox = talkBox.children[1];
ta.value = "стой, не туда";
button(talkBox, "Отправить").handlers.click({ stopPropagation: () => {} });
await settle();
if (!dump(pendbox).includes("стой, не туда")) {
  fail("отправленная реплика не встала местным пузырём: " + dump(pendbox));
}
if (!dump(pendbox).includes("доставлено") && !dump(pendbox).includes("отправляется")) {
  fail("местный пузырь молчит о судьбе реплики: " + dump(pendbox));
}
if (byClass(livePin(), "chatwrap") !== talkBox) fail("отправка пересобрала панель целиком");
if (doc.activeElement !== ta) fail("отправка отобрала фокус у поля ввода");
// Эхо из транскрипта снимает местный пузырь: реплика уже стоит в ленте, и два
// одинаковых пузыря подряд читались как отправленная дважды.
es.onmessage({
  data: JSON.stringify({ seq: 5, role: "user", text: "стой, не туда",
    time: "2026-08-13T10:04:00+03:00" }),
});
await settle();
if (dump(pendbox).includes("стой, не туда")) {
  fail("эхо из ленты не сняло местный пузырь: " + dump(pendbox));
}

// Продолжение работы стоит той же кнопкой в панели: сервер сам решает, будить
// ли живую сессию каналом или поднимать резюм (ручка /continue). Прежде тут
// стояла плашка «Цикл цели не идёт» с подъёмом витка, и отвечала она только за
// цели, а с задачей продолжать разговор было нечем.
const goOn = byClass(livePin(), "cgo");
if (!goOn) fail("в панели нечем продолжить работу задачи: " + dump(cpin).slice(0, 300));
if (!String(goOn.title).includes("XR-1")) {
  fail("кнопка продолжения не назвала задачу: " + goOn.title);
}
posted.length = 0;
goOn.handlers.click({ stopPropagation: () => {} });
await settle();
if (!posted.find((path) => path.includes("/tasks/XR-1/continue"))) {
  fail("кнопка продолжения не позвала ручку: " + JSON.stringify(posted));
}

// Панель над экраном задачи: тот же хвост адреса, и «назад» закрывает её,
// возвращая экран под ней на прежнее место. Открывает панель кнопка полосы
// действий, отдельного экрана разговора больше нет.
const talkStreams = () => streams.filter((s) => String(s.url).includes("/sessions/"));
const liveTalks = () => talkStreams().filter((s) => !s.closed);
const nextSeq = talk[talk.length - 1].seq + 1;
const talkReply = (es2, seq, text) => {
  es2.onmessage({ data: JSON.stringify({ seq, role: "assistant", text, time: "2026-08-13T10:05:00+03:00" }) });
};

sessions = [mine, alien];
await go("#demo");
await go("#demo/XR-1");
// Своей кнопки разговора на полосе действий задачи больше нет (POC ветки
// poc-chat): разговор открывает значок окна чатов в шапке, и с задачи он
// приходит тем же хвостом адреса. Полоса при этом обязана остаться про работу,
// а не про разговор.
if (barButton(groups, "Живой статус") || barButton(groups, "Разговор агента")) {
  fail("на полосе действий задачи снова заведён свой вход в разговор");
}
// Режим чтения включается и выключается: развёрнутая постановка накрывает
// строку статуса вместе со своей кнопкой, и выйти из режима было нечем
// (замечание 6). Пара к ней стоит в углу самой постановки.
{
  const on = barButton(groups, "Режим чтения");
  if (!on) fail("кнопки режима чтения на экране задачи нет: " + dump(groups).slice(0, 200));
  const panelFile = byClass(groups, "fpanel");
  // Шапка блока с этой кнопкой встаёт в разметку вместе с режимом чтения: вне
  // его в шапке пусто, а пустой её на экране нет вовсе.
  if (barButton(panelFile, "Выйти из режима чтения")) {
    fail("кнопка выхода стоит в разметке вне режима чтения: " + dump(panelFile).slice(0, 200));
  }
  on.handlers.click();
  if (!panelFile.classList.contains("wide")) fail("режим чтения не развернул постановку");
  const back = barButton(panelFile, "Выйти из режима чтения");
  if (!back || back.hidden) fail("в режиме чтения кнопки выхода нет: " + dump(panelFile).slice(0, 200));
  back.handlers.click();
  if (panelFile.classList.contains("wide")) fail("кнопка выхода не свернула постановку");
  if (barButton(panelFile, "Выйти из режима чтения")) {
    fail("после выхода кнопка выхода осталась в разметке");
  }
  if (on.classList.contains("on")) fail("кнопка режима чтения осталась нажатой после выхода");
}

await go("#demo/XR-1/chat/XR-1");
if (sandbox.location.hash.replace(/^#/, "") !== "demo/XR-1/chat/XR-1") {
  fail("разговор задачи открылся не хвостом адреса: " + sandbox.location.hash);
}
if (panelNode.hidden) fail("панель не открылась с экрана задачи");
if (!find(groups, "tpage") && !dump(groups).includes("XR-1")) {
  fail("экран задачи ушёл из-под панели: " + dump(groups).slice(0, 200));
}
// Лента панели поднята по свежему разговору задачи: их у неё два, и открывается
// последний (вкладка чатов это отдельная строка DK-436).
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(mine.id)) {
  fail("панель открыла не свежий разговор задачи: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}
talkReply(liveTalks()[0], nextSeq, "ход открытого разговора");
await settle();
if (!dump(cpin).includes("ход открытого разговора")) {
  fail("реплика открытого разговора не встала в ленту: " + dump(cpin).slice(0, 300));
}
// Крестик работает кнопкой «назад»: панель закрывается, а экран задачи под ней
// остаётся тем же самым.
// Крестик ищется подписью, а не первой попавшейся кнопкой шапки: слева от
// него стоят выбор диалога, новый чат и модель.
const shut = barButton(byClass(livePin(), "chead"), "Закрыть панель");
if (!shut) fail("в шапке панели нет крестика: закрыть её нечем");
shut.handlers.click({ stopPropagation: () => {} });
await settle();
if (sandbox.location.hash.replace(/^#/, "") !== "demo/XR-1") {
  fail("крестик панели увёл не на экран под ней: " + sandbox.location.hash);
}
if (!panelNode.hidden) fail("панель осталась открытой после крестика");
if (liveTalks().length) {
  fail("закрытая панель оставила живой поток ленты: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}

// Старые адреса ведут в ту же панель: ссылка на живой статус открывает экран
// задачи с разговором, ссылка на сессию открывает доску с ним же.
await go("#demo/agent/XR-1");
if (panelNode.hidden) fail("старый адрес живого статуса не открыл панель");
if (!dump(groups).includes("XR-1")) {
  fail("старый адрес живого статуса открыл не экран задачи: " + dump(groups).slice(0, 200));
}
await go("#demo/session/" + loose.id);
if (panelNode.hidden) fail("старый адрес разговора сессии не открыл панель");
if (!find(groups, "sec-backlog")) {
  fail("старый адрес разговора сессии открыл не доску: " + dump(groups).slice(0, 200));
}
// Разговор без узнанной задачи: заголовок берётся из первой реплики. Подписи
// «задача не распознана» с привязкой рукой в шапке больше нет (замечание 21
// POC): узнают разговор по первой реплике, а отчёт о том, чего дашборд не
// узнал, места в шапке не стоил.
const looseHead = byClass(livePin(), "chead");
if (!dump(looseHead).includes(loose.first)) {
  fail("шапка панели взята не из первой реплики: " + dump(looseHead));
}
if (dump(looseHead).includes("задача не распознана")) {
  fail("в шапке снова стоит отчёт о неузнанной задаче: " + dump(looseHead));
}
if (streams.some((s) => !s.closed && String(s.url).includes("/log?stream=1"))) {
  fail("панель разговора подняла поток журнала, который живёт на экране задачи");
}
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(loose.id)) {
  fail("в панели открыт не поток этой сессии: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}

// Поле ввода не гаснет и у кончившегося разговора: канал клиента достаёт любую
// живую сессию, а не нашедший её сервер поднимает резюм, и «пишите там» стало
// бы отказом без причины (POC ветки poc-chat). Прежде тут гасли ввод и отправка
// с названной причиной, и причин было четыре.
headExtra.reply = "";
headExtra.replyNote = "дерева сессии больше нет: разговор кончился, и продолжить его некому";
await go("#demo");
await go("#demo/chat/" + loose.id);
const offWrap = byClass(livePin(), "chatwrap");
const offTa = tag(offWrap, "TEXTAREA");
if (!offTa) fail("в панели кончившегося разговора нет поля ввода: " + dump(offWrap).slice(0, 200));
if (offTa.disabled) fail("поле ввода погашено, хотя реплике есть куда ехать");
if (button(offWrap, "Отправить").disabled) {
  fail("кнопка отправки погашена, хотя реплике есть куда ехать");
}
headExtra.reply = "session";
headExtra.replyNote = "";

// Живая сессия задачи, которой цель не является: ввод стоит, а реплика уходит
// ручкой разговора, не ручкой цели.
sessions = [mine];
rows[0].title = "Живая задача без цели";
await go("#demo");
await go("#demo/chat/XR-1");
const taskWrap = byClass(livePin(), "chatwrap");
const taskTa = tag(taskWrap, "TEXTAREA");
if (!taskTa || taskTa.disabled) {
  fail("у живого разговора обычной задачи погашено поле ввода: " + dump(find(taskWrap, "chat-note")));
}
taskTa.value = "проверь тесты";
button(taskWrap, "Отправить").handlers.click({ stopPropagation: () => {} });
await settle();
// Реплика уходит ручкой разговора (/chats/<id>/say), а не ручкой цели: до
// слияния экранов сюда же ехали сообщения целям, и живой сессии они доставались
// через «Входящие».
if (!posted.some((path) => path.includes("/chats/" + mine.id + "/say"))) {
  fail("реплика живого разговора ушла не ручкой разговора: " + JSON.stringify(posted));
}
rows[0].title = "Цель: дашборд без дёрганья";
sessions = [mine, alien];

// Поиск при открытой панели (замечание 4 четырнадцатого круга POC): набранное в
// шапке уводит на экран выдачи, а разговор остаётся на месте. Поиск был
// единственной дорогой, что закрывала чат: первая же буква сносила панель с
// экрана, и человек терял разговор, пока искал строку для него.
await go("#demo");
await go("#demo/chat/XR-1");
if (panelNode.hidden) fail("панель не открылась перед заходом в поиск");
const findHq = byId.get("hq");
timers.length = 0;
findHq.value = "доски номер 3";
findHq.handlers.input();
for (const t of timers.splice(0)) t.fn();
if (sandbox.location.hash.replace(/^#/, "") !==
    "demo/find/" + encodeURIComponent("доски номер 3") + "/chat/XR-1") {
  fail("набор в шапке потерял хвост разговора: " + sandbox.location.hash);
}
await sandbox.refresh();
await settle();
if (panelNode.hidden) fail("переход в поиск закрыл панель разговора");
if (!byClass(livePin(), "chatwrap")) {
  fail("тело панели ушло с экрана выдачи: " + dump(cpin).slice(0, 200));
}
const foundRow = find(groups, "find-card-board");
if (!foundRow) fail("экран выдачи под панелью не собрался: " + dump(groups).slice(0, 300));

// Строка выдачи нажимается и при открытой панели: ведёт на экран задачи, и
// разговор переезжает туда же хвостом адреса.
foundRow.children[0].handlers.click();
await settle();
if (sandbox.location.hash.replace(/^#/, "") !== "demo/XR-3/chat/XR-1") {
  fail("строка выдачи увела мимо задачи или уронила разговор: " + sandbox.location.hash);
}
await sandbox.refresh();
await settle();
if (panelNode.hidden) fail("переход из выдачи в задачу закрыл панель");
if (!dump(groups).includes("XR-3")) {
  fail("нажатие строки выдачи не открыло экран задачи: " + dump(groups).slice(0, 200));
}

// Режим чтения на этом же экране выключается той же кнопкой, какой включён:
// парная кнопка в углу постановки нужна была потому, что развёрнутая
// постановка накрывает строку статуса, но и сама строка обязана переключать
// режим в обе стороны (замечание 6).
{
  const on = barButton(groups, "Режим чтения");
  if (!on) fail("кнопки режима чтения на экране задачи из выдачи нет");
  const panelFile = byClass(groups, "fpanel");
  on.handlers.click();
  if (!panelFile.classList.contains("wide")) fail("режим чтения не развернул постановку");
  if (!on.classList.contains("on")) fail("кнопка режима чтения не показывает, что режим включён");
  on.handlers.click();
  if (panelFile.classList.contains("wide")) {
    fail("вторым нажатием та же кнопка режим чтения не выключила");
  }
  if (on.classList.contains("on")) fail("кнопка осталась нажатой при выключенном режиме");
}
await go("#demo");

// Ширина панели помнится одним числом на весь дашборд. Схлопнутая до нуля
// панель бесполезна, и пол ей стоит прежний; потолка в точках у неё больше
// нет, он меряется окном стенда (1400 точек).
if (sandbox.chatWidth() !== 420) fail("ширина по умолчанию не та: " + sandbox.chatWidth());
store.set("devkit.chat.width", "900");
if (sandbox.chatWidth() !== 900) fail("широкая панель обрезана: " + sandbox.chatWidth());
store.set("devkit.chat.width", "99999");
const roomy = sandbox.window.innerWidth - 72;
if (sandbox.chatWidth() !== roomy) {
  fail("ширина не прижата к окну: " + sandbox.chatWidth() + ", окно " + sandbox.window.innerWidth);
}
store.set("devkit.chat.width", "100");
if (sandbox.chatWidth() !== 320) fail("ширина не прижата к полу: " + sandbox.chatWidth());
sandbox.saveChatWidth(505);
if (store.get("devkit.chat.width") !== "505") {
  fail("ширина не запомнилась между заходами: " + store.get("devkit.chat.width"));
}

// Экран задачи забрал себе то, что ушло из разговора: журнал витка, признак
// живости, этап работы и кнопку стопа (DK-435).
await go("#demo");
await go("#demo/XR-1");
// Подсказка доски не переживает перехода: на экране задачи она объясняла бы
// чужое название.
if (String(byId.get("pname").title || "")) {
  fail("подсказка доски осталась на экране задачи: " + JSON.stringify(byId.get("pname").title));
}
if (!dump(groups).includes("Журнал витка")) {
  fail("журнал витка не переехал на экран задачи: " + dump(groups).slice(0, 300));
}
// Состояние работы названо словарным словом, одним на весь дашборд: прежде
// форма задачи говорила «tmux-сессия активна», таб сессий «работает», а снимок
// tmux «активна», и это читалось как три разных состояния.
if (!dump(groups).includes("активна")) {
  fail("признака живости на экране задачи нет: " + dump(groups).slice(0, 300));
}
// Снятие сессии зовётся одним словом на весь дашборд: «Остановить агента»,
// «Остановить» и «Стоп» были тремя подписями одного действия.
if (!barButton(groups, "Стоп")) {
  fail("кнопки стопа на экране задачи нет: " + dump(groups).slice(0, 300));
}
if (!dump(groups).includes("разработка")) {
  fail("этап работы не встал на экран задачи: " + dump(groups).slice(0, 300));
}
chatWork.pop();
await go("#demo");

// Возвращение в окно на экране задачи (DK-411): обновление по фокусу идёт тем
// же слоем частичной перерисовки, что и на доске. Строка не менялась, значит
// собирать нечего: узлы экрана остаются теми же, прокрутка стоит там, где её
// оставили, каретка из поля правки не выпадает, а раскрытый служебный раздел
// файла не захлопывается. До DK-411 фокус окна звал полную пересборку, и
// терялось это всё разом.
await go("#demo/XR-3");
// Опорный узел экрана это шапка с номером и заголовком: крошек у задачи нет
// вовсе, дорога на доску живёт названием проекта в шапке страницы.
const tcrumb = find(groups, "task-head");
if (!tcrumb) fail("экран задачи не собрался: " + dump(groups).slice(0, 300));

// Раскрыть служебный раздел файла.
const jfold = byClass(groups, "ffold");
if (!jfold) fail("свёрнутого раздела на экране задачи нет: " + dump(groups).slice(0, 400));
jfold.children[0].handlers.click({});
await settle();
if (!dump(jfold).includes("запись журнала раз")) {
  fail("раздел не раскрылся кликом: " + dump(jfold).slice(0, 300));
}

// Войти в правку карандашом и поставить каретку в середину текста.
const pen = barButton(groups, "Править задачу");
if (!pen) fail("карандаша на экране задачи нет: " + dump(groups).slice(0, 400));
pen.handlers.click({});
await settle();
const area = byClass(groups, "fbody");
const edit = tag(byClass(groups, "fcard") || groups, "TEXTAREA");
if (!edit || edit.hidden) fail("поле правки не показалось: " + dump(groups).slice(0, 400));
edit.focus();
edit.selectionStart = 7;
edit.selectionEnd = 7;
groups.scrollTop = 260;

// Тот самый возврат в окно: слушатель фокуса зовётся так же, как его зовёт
// браузер.
const askedBefore = asked.length;
for (const fn of sandbox.window.listeners.focus || []) fn();
await settle();

if (find(groups, "task-head") !== tcrumb) {
  fail("возврат в окно пересобрал экран задачи: узел уехал из-под руки");
}
if (groups.scrollTop !== 260) {
  fail("возврат в окно сбил прокрутку экрана задачи: " + groups.scrollTop + " вместо 260");
}
if (doc.activeElement !== edit) {
  fail("возврат в окно отобрал фокус у поля правки: " + dump(doc.activeElement));
}
if (edit.selectionStart !== 7) {
  fail("каретка выпала из поля правки: " + edit.selectionStart + " вместо 7");
}
// Раздел ищется на экране заново: снятый с дерева узел помнит своё раскрытое
// тело и один сам по себе ничего не доказывает.
const foldNow = byClass(groups, "ffold");
if (foldNow !== jfold || !dump(foldNow).includes("запись журнала раз")) {
  fail("возврат в окно захлопнул раскрытый раздел файла: " + dump(foldNow).slice(0, 300));
}
if (area && area.hidden === true && !edit.hidden) {
  fail("правка закрылась сама: " + dump(groups).slice(0, 300));
}
// За строкой экран сходил, а вот план агента заново не заказывал: узел тот же,
// и живые потоки его никто не рвал.
const askedNow = asked.slice(askedBefore);
if (askedNow.some((a) => a.includes("/pulse?task="))) {
  fail("возврат в окно переподнял живые потоки экрана задачи: " + askedNow.join(" "));
}

// Колонка проектов на том же слое: строка проекта переживает возврат в окно
// тем же узлом.
const nav = byId.get("projects");
const navItem = nav.children[0];
if (!navItem) fail("колонка проектов пуста");
for (const fn of sandbox.window.listeners.focus || []) fn();
await settle();
if (nav.children[0] !== navItem) {
  fail("возврат в окно пересобрал колонку проектов");
}

// Правка, уехавшая под руками, экран всё-таки перерисовывает: слой держит
// место, а не старые данные.
const xr3 = rows.find((x) => x.id === "XR-3");
const xr3Was = xr3.title;
xr3.title = "заголовок сменился на доске";
for (const fn of sandbox.window.listeners.focus || []) fn();
await settle();
if (find(groups, "task-head") === tcrumb) {
  fail("уехавшая строка не перерисовала экран задачи: слой держит старые данные");
}
// Заголовок задачи правится полем, и новый текст лежит в его значении, а не в
// разметке.
const titleField = byClass(find(groups, "task-head") || groups, "tedit");
if (!titleField || titleField.value !== "заголовок сменился на доске") {
  fail("новый заголовок не встал на экран: " + (titleField && titleField.value));
}
xr3.title = xr3Was;
await go("#demo");

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

// Командная панель строки статуса: карандаш, кнопка режима чтения и кнопки
// действий стоят в одну строку, и рост у них общий. Кнопки действий приехали
// туда позже остальных и держали свои 36px от .btn, отчего строка выглядела
// ступенькой. Высота, радиус и стык половин составной кнопки читаются из
// настоящего style.css.
const cssRule = (sel) => {
  const esc = sel.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const m = new RegExp("(?:^|[},])\\s*" + esc + "\\{([^}]*)\\}", "m").exec(css);
  if (!m) fail("в style.css нет правила " + sel);
  return m[1];
};
const cssProp = (sel, prop) => {
  const m = new RegExp("(?:^|;)" + prop + ":([^;]+)").exec(cssRule(sel));
  return m ? m[1].trim() : "";
};
if (cssProp(".tacts .btn", "height") !== cssProp(".tpen", "height")) {
  fail("кнопки действий командной панели разошлись с карандашом по высоте: " +
    cssProp(".tacts .btn", "height") + " против " + cssProp(".tpen", "height"));
}
if (cssProp(".btn", "border-radius") !== cssProp(".tpen", "border-radius")) {
  fail("радиус кнопки разошёлся с радиусом карандаша: " +
    cssProp(".btn", "border-radius") + " против " + cssProp(".tpen", "border-radius"));
}
// Радиус в самой панели не переопределяется: правило бьёт и по узкой половине
// составной кнопки, которой скруглять надо только внешний край, и стык двух
// половин расходился скруглениями (жалоба пользователя).
if (cssProp(".tacts .btn", "border-radius")) {
  fail("панель переопределяет радиус кнопок и ломает углы составной кнопки: " +
    cssProp(".tacts .btn", "border-radius"));
}
// Отступы правятся только у обычной кнопки: у узкой половины они свои (padding:0
// и ширина в 30 пикселей), и общее правило растягивало её.
if (cssProp(".tacts .btn", "padding") || !cssProp(".tacts .btn:not(.more2)", "padding")) {
  fail("отступы кнопок панели правятся общим правилом, вместе с узкой половиной");
}
// Стык половин это один пиксель: своя рамка есть у каждой, и без наезда они
// давали двойную линию.
if (cssProp(".split .more2", "margin-left") !== "-1px") {
  fail("узкая половина составной кнопки не наезжает на широкую: стык в две рамки, " +
    JSON.stringify(cssProp(".split .more2", "margin-left")));
}
if (!/^1px /.test(cssProp(".split .more2", "border-left"))) {
  fail("разделитель половин составной кнопки не в один пиксель: " +
    cssProp(".split .more2", "border-left"));
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

// Перетаскивание строки очереди (DK-324, LLD DK-328, решение 1). Считать
// разбором исходника тут нечего: предмет проверки это коридор со щелями,
// который жест рисует на живом списке, тело запроса и слова после броска.
// Расчёт щели проверяется отдельно, прямыми вызовами: краевые случаи (верх,
// низ, равные ранги, глухая щель, единственная строка) пальцем не набрать.

for (const name of ["dragCorridor", "gapAim", "dragTagText"]) {
  if (typeof sandbox[name] !== "function") {
    fail("в статике нет расчёта коридора и щелей (" + name + "): перетаскивать строку нечем");
  }
}

// Коридор строки: серьёзность плюс прочие мягкие слагаемые дают пол, ценность
// добирает до потолка. Полосу серьёзности такой коридор не переезжает никогда.
const cor = sandbox.dragCorridor({ r_parts: [25, 3, 1, 0, 1] });
if (!cor || cor.low !== 27 || cor.high !== 37 || cor.value !== 3 || cor.r !== 30) {
  fail("коридор строки посчитан не по слагаемым: " + JSON.stringify(cor));
}
if (sandbox.dragCorridor({ r_parts: [25, 3] })) {
  fail("у строки с неполной разбивкой ранга взялся коридор: жест такую строку брать не должен");
}

// Щели считаются по паре ключей сортировки. Список тут выдуманный, зато
// краевые случаи в нём стоят рядом: верх, низ, общий ранг соседей и
// единственная строка.
const aimRow = (id, r, parts) => ({ id, r, r_parts: parts });
const line = [
  aimRow("XR-2", 86, [75, 9, 1, 0, 1]),
  aimRow("XR-3", 36, [25, 9, 1, 0, 1]),
  aimRow("XR-4", 34, [25, 7, 1, 0, 1]),
  aimRow("XR-5", 34, [25, 7, 1, 0, 1]),
  aimRow("XR-6", 30, [25, 3, 1, 0, 1]),
  aimRow("XR-7", 29, [25, 2, 1, 0, 1]),
  aimRow("XR-8", 28, [25, 1, 1, 0, 1]),
  aimRow("XR-9", 3, [0, 1, 1, 0, 1]),
];
const aimAt = (gap) => sandbox.gapAim(line, "XR-6", gap);
// Щель ноль это самый верх списка: туда ценностью не дотянуться, и причина
// названа потолком коридора.
if (aimAt(0).r !== null || !aimAt(0).why.includes("выше не поднять")) {
  fail("верхняя щель взялась мимо коридора: " + JSON.stringify(aimAt(0)));
}
if (!aimAt(0).why.includes("ранг 37")) {
  fail("причина у верхнего края не называет потолка коридора: " + aimAt(0).why);
}
// Под самой нижней строкой то же самое, только полом.
const bottom = aimAt(7);
if (bottom.r !== null || !bottom.why.includes("ниже не опустить") || !bottom.why.includes("ранг 27")) {
  fail("нижняя щель взялась мимо коридора: " + JSON.stringify(bottom));
}
// Живая щель: ранг ближайший к сегодняшнему из тех, что ставят строку на место.
if (aimAt(1).r !== 37 || aimAt(1).value !== 10) fail("щель под первой строкой: " + JSON.stringify(aimAt(1)));
if (aimAt(2).r !== 35 || aimAt(2).value !== 8) fail("щель между XR-3 и XR-4: " + JSON.stringify(aimAt(2)));
// Место, откуда строку взяли: ранг тот же, и жест его не раздувает.
if (aimAt(4).r !== 30 || aimAt(4).value !== 3) fail("своя щель строки: " + JSON.stringify(aimAt(4)));
// Равные ранги и номер строки: XR-6 старше обоих номером, и между двумя
// строками с рангом 34 ей места нет ни при каком ранге.
const dead = aimAt(3);
if (dead.r !== null) fail("глухая щель между равными рангами приняла строку: " + JSON.stringify(dead));
for (const want of ["места нет", "выше XR-4 ставит ранг 35", "ниже XR-5 ранг 34"]) {
  if (!dead.why.includes(want)) fail("глухая щель не сказала " + JSON.stringify(want) + ": " + dead.why);
}
// Номер решает при равном ранге: XR-6 встаёт выше XR-8 с общим рангом 28.
if (aimAt(5).r !== 28 || aimAt(5).value !== 1) {
  fail("щель под XR-7 не взяла ранг соседа по номеру: " + JSON.stringify(aimAt(5)));
}
// Единственная строка: щель у неё одна, и это её же место.
const alone = [aimRow("XR-6", 30, [25, 3, 1, 0, 1])];
if (sandbox.gapAim(alone, "XR-6", 0).r !== 30 || sandbox.gapAim(alone, "XR-6", 1).r !== 30) {
  fail("у единственной строки щель не приняла её собственный ранг");
}
// Полоса P считается из суммы, и у верхнего края коридора жест перетаскивает
// строку через порог: молчать об этом нельзя.
const jump = sandbox.dragTagText({ low: 40, high: 50, value: 9, r: 49 },
  { r: 50, value: 10, above: null, below: null });
if (jump !== "ранг 49 -> 50, ценность 9 -> 10, полоса P2 -> P1") {
  fail("ярлык у края коридора не назвал переезда полосы: " + jump);
}

// Жест на живом списке. Доска стенда получает разные ранги: до сих пор строки
// стояли с общим, и щели на ней были бы все глухие.
for (const row of line) {
  const at = rows.find((r) => r.id === row.id);
  if (!at) continue;
  at.r = row.r;
  at.r_parts = row.r_parts.slice();
  at.p = row.r >= 50 ? "P1" : "P2";
}
sortBoard();
running = false;
await go("#demo");
const bcard = layout(find(groups, "sec-backlog"));
const held = find(bcard, "XR-6");
if (!held) fail("строки XR-6 в очереди нет: " + dump(bcard).slice(0, 200));
const midOf = (id) => {
  const box = find(bcard, id).getBoundingClientRect();
  return box.top + box.height / 2;
};
const touch = (name, y, extra) => held.handlers[name](Object.assign({
  pointerId: 1, pointerType: "touch", clientY: y, cancelable: true,
  target: held, preventDefault: () => {}, stopPropagation: () => {},
}, extra || {}));
const holdFire = () => {
  const wait = timers.pop();
  if (!wait) fail("долгое нажатие не завело таймера удержания");
  wait.fn();
};

// Судьбу касания браузер решает по первому движению пальца: не отменил его
// никто, значит это прокрутка, и указатель отменяется (pointercancel). Стенд
// повторяет тут правило браузера, а не проверяет сам себя: touch-action браузер
// читает в момент касания, и класс, приезжающий через долгое нажатие, ему уже
// не указ, поэтому взятая строка срывалась на первом же движении пальца
// (браузерная приёмка DK-324). Отдаёт true, когда движение перехвачено и жест
// дожил.
const swipe = (node, y) => {
  let stopped = false;
  if (node.handlers.touchmove) {
    node.handlers.touchmove({
      cancelable: true, target: node, touches: [{ clientY: y }],
      preventDefault: () => { stopped = true; },
    });
  }
  if (stopped) return true;
  if (node.handlers.pointercancel) {
    node.handlers.pointercancel({ pointerId: 1, pointerType: "touch" });
  }
  return false;
};

// Жест живёт только в очереди: строка из другой секции пальцем не берётся.
if (find(groups, "XR-1").handlers.pointerdown) {
  fail("строка вне Backlog отвечает на нажатие пальцем: там порядок ручной, и жест обещал бы лишнее");
}

// Пролистывание остаётся пролистыванием: палец поехал раньше, чем сработало
// удержание, и жест снимается вовсе.
timers.length = 0;
touch("pointerdown", midOf("XR-6"));
if (!timers.length) fail("долгое нажатие не завело таймера удержания");
touch("pointermove", midOf("XR-6") + 40);
for (const t of timers.splice(0)) t.fn();
if (byClass(bcard, "gslot")) fail("пролистывание списка подняло щели перетаскивания");
if (held.classList.contains("dragrow")) fail("пролистывание списка взяло строку");
touch("pointerup", midOf("XR-6") + 40);

// Пока строку не взяли, палец принадлежит списку: движение никто не
// перехватывает, и прокрутка жива. Слушает его строка сама, и подписка не
// passive: отменить прокрутку браузер разрешает только такой.
timers.length = 0;
touch("pointerdown", midOf("XR-6"));
if (typeof held.handlers.touchmove !== "function") {
  fail("строка не слушает движение пальца: отменить прокрутку под взятой строкой нечем");
}
const swipeOpts = held.listenOpts.touchmove;
if (!swipeOpts || swipeOpts.passive !== false) {
  fail("подписка на движение пальца объявлена passive, такой браузер прокрутку отменить не даст: " +
    JSON.stringify(swipeOpts));
}
if (swipe(held, midOf("XR-6") + 40)) {
  fail("палец перехвачен до взятия строки: список перестал прокручиваться пальцем");
}
for (const t of timers.splice(0)) t.fn();
touch("pointerup", midOf("XR-6") + 40);

// Короткое касание по-прежнему открывает задачу.
timers.length = 0;
sandbox.location.hash = "#demo";
touch("pointerdown", midOf("XR-6"));
touch("pointerup", midOf("XR-6"));
held.handlers.click({ target: held, stopPropagation: () => {} });
if (sandbox.location.hash !== "demo/XR-6") {
  fail("короткое касание перестало открывать задачу: " + sandbox.location.hash);
}
timers.length = 0;
await go("#demo");

// Долгое нажатие: строка взялась, коридор нарисован прямо на списке.
const card2 = layout(find(groups, "sec-backlog"));
const row6 = find(card2, "XR-6");
const grab = (name, y, extra) => row6.handlers[name](Object.assign({
  pointerId: 1, pointerType: "touch", clientY: y, cancelable: true,
  target: row6, preventDefault: () => {}, stopPropagation: () => {},
}, extra || {}));
const mid2 = (id) => {
  const box = find(card2, id).getBoundingClientRect();
  return box.top + box.height / 2;
};
timers.length = 0;
grab("pointerdown", mid2("XR-6"));
holdFire();
if (!row6.classList.contains("dragrow")) fail("долгое нажатие не взяло строку: " + row6.className);
// Первое движение пальца после удержания: браузер обязан получить отказ от
// прокрутки, иначе он отменит указатель и взятая строка сорвётся.
if (!swipe(row6, mid2("XR-6") + 12)) {
  fail("первое движение пальца ушло прокруткой: браузер отменил указатель, и взятая строка сорвалась");
}
if (!row6.classList.contains("dragrow")) fail("жест не пережил первого движения пальца");
if (!find(card2, "XR-2").classList.contains("dimrow") ||
    !find(card2, "XR-9").classList.contains("dimrow")) {
  fail("зона за коридором не приглушена: строки, до которых жест не дотягивается, выглядят живыми");
}
if (find(card2, "XR-3").classList.contains("dimrow")) {
  fail("строка внутри коридора приглушена вместе с чужими");
}
const slots = card2.children.filter((kid) => String(kid.className).includes("gslot"));
if (!slots.length) fail("щели на списке не нарисованы: целиться некуда");
const slotText = slots.map(dump).join(" | ");
for (const want of ["ранг 37", "ранг 35", "места нет", "выше не поднять", "ниже не опустить"]) {
  if (!slotText.includes(want)) {
    fail("среди щелей нет " + JSON.stringify(want) + ": " + slotText);
  }
}

// Обновление по фокусу окна взятую строку не трогает: пересобранный список увёл
// бы её из-под пальца вместе с коридором.
await sandbox.refresh();
await settle();
if (find(groups, "sec-backlog") !== card2 || !row6.classList.contains("dragrow")) {
  fail("обновление пересобрало список под пальцем");
}
if (!card2.children.filter((kid) => String(kid.className).includes("gslot")).length) {
  fail("обновление стёрло щели у взятой строки");
}

// Целимся в щель между XR-3 и XR-4: ярлык называет и ранг, и ценность.
grab("pointermove", (mid2("XR-3") + mid2("XR-4")) / 2);
const tagged = dump(row6);
if (!tagged.includes("ранг 30 -> 35, ценность 3 -> 8")) {
  fail("ярлык взятой строки не назвал пересчёта: " + tagged);
}
// Глухая щель говорит причину тем же ярлыком, а не молчит.
grab("pointermove", (mid2("XR-4") + mid2("XR-5")) / 2);
if (!dump(row6).includes("места нет")) {
  fail("на глухой щели ярлык замолчал: " + dump(row6));
}

// Бросок: правка уезжает ручкой одной ценностью, а строка результата пишется
// по ответу сервера.
grab("pointermove", (mid2("XR-3") + mid2("XR-4")) / 2);
const sentWas = patched.length;
grab("pointerup", (mid2("XR-3") + mid2("XR-4")) / 2);
await settle();
if (patched.length !== sentWas + 1) {
  fail("бросок не отправил правки: запросов " + (patched.length - sentWas));
}
const sent = patched[patched.length - 1];
if (String(sent.r_parts) !== String([null, 8, null, null, null])) {
  fail("жест правит не одну ценность: " + JSON.stringify(sent));
}
const drop = byId.get("flashes");
if (!dump(drop).includes("XR-6: ценность 3 -> 8, ранг 30 -> 35")) {
  fail("строка результата не назвала пересчёта: " + dump(drop));
}
if (!dump(drop).includes("Строка встала между XR-3 и XR-4")) {
  fail("строка результата не назвала места по ответу сервера: " + dump(drop));
}
// Клик, оставшийся от броска, внутрь задачи не уводит.
sandbox.location.hash = "#demo";
row6.handlers.click({ target: row6, stopPropagation: () => {} });
if (sandbox.location.hash !== "#demo") {
  fail("бросок строки увёл на экран задачи: " + sandbox.location.hash);
}
// Строка после броска подсвечена: слагаемые с полосой видно в самой строке.
if (!find(groups, "XR-6").classList.contains("litrow")) {
  fail("после броска строка ничем не помечена: найти её глазами на новом месте нечем");
}

// «Вернуть» кладёт обратно слагаемые и едет с ожидаемой разбивкой.
const undoBtn = button(drop, "Вернуть");
if (!undoBtn) fail("у строки результата нет кнопки «Вернуть»: " + dump(drop));
undoBtn.handlers.click({ stopPropagation: () => {} });
await settle();
const undo = patched[patched.length - 1];
if (String(undo.r_parts) !== String([null, 3, null, null, null])) {
  fail("откат вернул не прежнюю ценность: " + JSON.stringify(undo));
}
if (String(undo.expect_r_parts) !== String([25, 8, 1, 0, 1])) {
  fail("откат уехал без ожидаемой разбивки: " + JSON.stringify(undo));
}
if (rows.find((r) => r.id === "XR-6").r !== 30) {
  fail("откат не вернул ранга строки: " + rows.find((r) => r.id === "XR-6").r);
}

// Мышью строка берётся сразу, но с порогом: дрожание руки на нажатии обязано
// остаться кликом. Отпущенная там же, откуда взята, строка правкой не считается
// и запроса за собой не тянет.
for (const t of timers.splice(0)) t.fn();
await go("#demo");
const cardM = layout(find(groups, "sec-backlog"));
const rowM = find(cardM, "XR-6");
const midM = (id) => {
  const box = find(cardM, id).getBoundingClientRect();
  return box.top + box.height / 2;
};
const mouse = (name, y) => rowM.handlers[name]({
  pointerId: 2, pointerType: "mouse", button: 0, clientY: y, cancelable: true,
  target: rowM, preventDefault: () => {}, stopPropagation: () => {},
});
const homeY = midM("XR-6");
mouse("pointerdown", homeY);
mouse("pointermove", homeY + 2);
if (rowM.classList.contains("dragrow")) {
  fail("мышь взяла строку от дрожания в пару пикселей: клик перестал быть кликом");
}
mouse("pointermove", homeY + 20);
if (!rowM.classList.contains("dragrow")) fail("мышью строка за порогом не взялась");
const idleWas = patched.length;
mouse("pointermove", midM("XR-6"));
mouse("pointerup", midM("XR-6"));
await settle();
if (patched.length !== idleWas) {
  fail("строка, отпущенная на своём месте, ушла правкой: " + JSON.stringify(patched[patched.length - 1]));
}

// Строку успели поправить с другой стороны: откат отбит словами с сегодняшним
// рангом, и человек решает сам.
for (const t of timers.splice(0)) t.fn();
byId.get("flashes").replaceChildren();
await go("#demo");
const card3 = layout(find(groups, "sec-backlog"));
const row6b = find(card3, "XR-6");
const mid3 = (id) => {
  const box = find(card3, id).getBoundingClientRect();
  return box.top + box.height / 2;
};
const hold3 = (name, y) => row6b.handlers[name]({
  pointerId: 1, pointerType: "touch", clientY: y, cancelable: true,
  target: row6b, preventDefault: () => {}, stopPropagation: () => {},
});
timers.length = 0;
hold3("pointerdown", mid3("XR-6"));
holdFire();
// Соседняя сессия успела переставить доску, пока шёл жест: соседи в ответе не
// те, что насчитал экран.
boardAhead = { above: { id: "XR-2", r: 86 }, below: { id: "XR-3", r: 36 } };
hold3("pointermove", (mid3("XR-3") + mid3("XR-4")) / 2);
hold3("pointerup", (mid3("XR-3") + mid3("XR-4")) / 2);
await settle();
if (!dump(byId.get("flashes")).includes("Доска успела уехать")) {
  fail("разошедшееся с превью место названо превью: " + dump(byId.get("flashes")));
}
// Чужая правка под откатом: ожидаемая разбивка разошлась, и ручка отвечает
// словами.
rows.find((r) => r.id === "XR-6").r_parts = [25, 4, 1, 0, 1];
rows.find((r) => r.id === "XR-6").r = 31;
button(byId.get("flashes"), "Вернуть").handlers.click({ stopPropagation: () => {} });
await settle();
if (!dump(byId.get("flashes")).includes("строку поправили")) {
  fail("откат поверх чужой правки прошёл молча: " + dump(byId.get("flashes")));
}
if (rows.find((r) => r.id === "XR-6").r !== 31) {
  fail("отбитый откат всё равно переписал строку: " + rows.find((r) => r.id === "XR-6").r);
}
for (const t of timers.splice(0)) t.fn();

// Главная без приписки: список досок под заголовком и так говорит, что это
// главная, а путь доски там не к месту вовсе (замечание пользователя).
await go("#");
if (String(byId.get("pname").title || "")) {
  fail("подсказка доски осталась на главной: " + JSON.stringify(byId.get("pname").title));
}

console.log("частичная перерисовка: доска, черновики и лента панели держат место и фокус, " +
  "панель разговора открывается хвостом адреса и закрывается крестиком назад, " +
  "старые адреса ведут в неё же, поле ввода живое, а работу продолжает своя кнопка, " +
  "экран задачи держит журнал витка, этап и стоп, " +
  "ответ на нажатие не двигает раскладку; " +
  "поиск: выдача своим экраном, набор одним запросом, поле держит курсор, " +
  "косая черта и лупа ведут в поиск; подписки: широкая часть кнопки идёт на " +
  "подписку по умолчанию, строка списка на свою, без выбора стрелки нет, " +
  "вид кнопки и списка по макету; кнопки действий: стоп красный, "
  + "подтверждение удаления полноразмерное своей строкой; " +
  "экран черновика: пометка идущего груминга, кнопка разбора, дорога в его чат, " +
  "исход разбора словами сервера и вопрос грумера с ответом; " +
  "перетаскивание: коридор со щелями на живом списке, долгое нажатие против " +
  "пролистывания, правка одной ценностью и откат с ожидаемой разбивкой");
