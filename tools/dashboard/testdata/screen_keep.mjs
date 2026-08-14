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
  node.setAttribute = () => {};
  node.removeAttribute = () => {};
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
  // Обработчики документа складываются так же, как узловые: горячей клавише
  // («/» ставит курсор в поле поиска) больше неоткуда взяться.
  handlers: {},
  addEventListener: (name, fn) => { doc.handlers[name] = fn; },
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
// подходят, когда все их классы есть у узла (`.tmuxbar.onpane` узлу без onpane
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
// работа видна, у строки In progress без работы gone. Заказ дописывается той
// же разметкой.
function marked(list, key) {
  return list.map((r) => {
    const live = works().find((w) => w.id === r.id);
    const run = live ? live.via : (key === "in-progress" ? "gone" : "");
    const order = rowOrder(key, r);
    return Object.assign({}, r, run ? { run } : {}, order ? { order } : {});
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
  return running ? [{ id: "XR-3", via: "tmux", title: "строка доски номер 3" }] : [];
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
let draftOut = { state: "open", file: "docs/tasks/drafts/XR-D2.md", note: "груминг записи не касался" };
let grooming = false;
let groomAsk = null;
let dropped = null;

function groomWorks() {
  return grooming ? [{ id: "XR-D2", via: "tmux", title: "вторая запись накопителя" }] : works();
}

const talk = [
  { seq: 1, role: "user", text: "как дела с витком", time: "2026-08-13T10:00:00+03:00" },
  { seq: 2, role: "assistant", text: "виток идёт, задачи режу", time: "2026-08-13T10:01:00+03:00" },
  { seq: 3, role: "assistant", text: "нарезал три штуки", time: "2026-08-13T10:02:00+03:00" },
];

function reply(body) {
  return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(body) });
}

function refuse(status, body) {
  return Promise.resolve({ ok: false, status, json: () => Promise.resolve(body) });
}

// Игрушечный taskctl под ручкой PATCH (DK-324): правка слагаемых пересчитывает
// сумму с полосой и переставляет очередь по паре ключей «ранг убыванием, номер
// возрастанием» (insertIdx в tools/taskctl/board.go), а ответ называет
// фактическое место строки соседями сверху и снизу. Порядок тут не выдумка
// стенда: считать его на клиенте как раз и нельзя, клиентский расчёт это
// превью.
const patched = [];

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
  return p.then(() => ({ ok: true, status: 200, json: () => Promise.resolve(body) }));
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

// Игрушечные таймеры: карточка ответа гаснет по времени, и ждать его
// по-честному стенду нечем. Заказанное складывается в список, а срабатывает по
// команде стенда.
const timers = [];
let timerSeq = 0;

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
      return reply({ projects: [{ name: "demo", works: groomWorks(), sections: { check: 1 } }] });
    }
    if (path.endsWith("/runs") && post) {
      running = true;
      started.push(init && init.body ? JSON.parse(init.body) : {});
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
      if (!r) return refuse(404, { error: "на доске demo нет строки " + id });
      const sect = id === "XR-1" ? "in-progress" : "backlog";
      const order = rowOrder(sect, r);
      return reply({
        project: "demo", id,
        row: Object.assign({}, r, { sect, section: sect }, order ? { order } : {}),
        after: [], blocks: [],
      });
    }
    if (path === "/api/harnesses") return reply(harnessBody());
    if (path.endsWith("/board")) return reply(boardBody());
    if (path.endsWith("/drafts")) return reply({ drafts });
    if (path.endsWith("/outcome")) return reply(draftOut);
    if (path.endsWith("/groom") && post) {
      groomAsk = JSON.parse(init.body).ask || "";
      const gid = path.slice(path.indexOf("/drafts/") + "/drafts/".length, path.indexOf("/groom"));
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
      const head = [mine, alien, loose].find((s) => s.id === sid) || { id: sid };
      return reply({ session: sid, head, items: talk });
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
const card = find(groups, "card-backlog");
if (!card) fail("доска не собралась: карточки Backlog на экране нет");
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
if (find(groups, "card-backlog") !== card) {
  fail("карточка секции пересобрана заново, хотя строки в ней те же");
}

// Строка в работе, за которой не стоит живой сессии, помечена этим, а не
// выглядит очередью: до DK-317 оборванный конвейер был неотличим от штатного
// ожидания.
const goalRow = find(groups, "XR-1");
if (!dump(goalRow).includes("сессии нет")) {
  fail("строка в работе без живой сессии ничем не помечена: " + dump(goalRow));
}

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
if (!dump(now).includes("работает")) {
  fail("у идущей работы в строке нет признака выполнения: " + dump(now));
}
if (doc.activeElement.textContent !== "Стоп") {
  fail("после нажатия фокус ушёл со строки: " + dump(doc.activeElement));
}

// Выбор подписки в самой кнопке запуска (DK-326). Широкая часть поднимает
// работу на подписке по умолчанию, узкая открывает список, а строка списка
// запускает работу на своей подписке без второго нажатия. Проверяется тело
// запроса: нарисованный список без доехавшего имени это ровно та поломка,
// ради которой задача и заведена.
if (started[started.length - 1].harness !== harnessOne.name) {
  fail("широкая часть кнопки ушла не на подписку по умолчанию: " +
    JSON.stringify(started[started.length - 1]));
}
running = false;
await sandbox.refresh();
await settle();
const pickRow = find(groups, "XR-5");
const grp = byClass(pickRow, "split");
if (!grp) fail("у строки нет составной кнопки запуска: " + dump(pickRow));
// Вид кнопки держит макет «11 Подписка при запуске»: половины одного цвета,
// узкая часть с галочкой-рамкой, стык без зазора. До DK-336 узкая часть была
// отдельной кнопкой со значком и отступом.
const more = grp.children[1];
if (!String(more.className).includes("more2") || !String(more.className).includes("btn-acc")) {
  fail("узкая часть кнопки не по макету: " + more.className);
}
if (!byClass(more, "car")) fail("в узкой части нет галочки-рамки: " + dump(more));
const pop = byClass(grp, "hpop");
if (!pop || !pop.hidden) fail("список подписок открыт до нажатия на стрелку");
more.handlers.click({ stopPropagation: () => {} });
if (pop.hidden) fail("стрелка не открыла список подписок");
// Шапка, две подписки и подвал: макет держит все три части, и без шапки с
// подвалом список не говорит ни что выбирают, ни откуда он взялся.
if (!dump(byClass(pop, "hph")).includes("На какой подписке запустить")) {
  fail("у списка подписок нет шапки: " + dump(pop));
}
if (!dump(byClass(pop, "hfoot")).includes("agentctl harness")) {
  fail("подвал списка не называет источник и срок выбора: " + dump(pop));
}
const hrows = pop.children.filter((kid) => String(kid.className).includes("hrow"));
if (hrows.length !== 2) {
  fail("в списке подписок " + hrows.length + " строк, ждал две: " + dump(pop));
}
if (!String(hrows[0].className).includes("on") || String(hrows[1].className).includes("on")) {
  fail("подписка по умолчанию в списке не подсвечена: " + hrows.map((r) => r.className).join(" | "));
}
if (!byClass(hrows[0], "chip") || !dump(hrows[0]).includes("по умолчанию")) {
  fail("признак «по умолчанию» стоит не чипом: " + dump(hrows[0]));
}
// Остаток квоты в строке: подписку выбирают ровно из-за него. Полоски идут те
// же, что в блоке квоты, а возраст снимка стоит у каждой строки, не только у
// протухшей.
const meters = (hrows[0].children || []).filter((kid) => String(kid.className).includes("qrow"));
if (meters.length !== 2) {
  fail("в строке подписки не две полоски остатка: " + dump(hrows[0]));
}
if (!dump(hrows[0]).includes("52%") || !dump(hrows[0]).includes("снимок 3м назад")) {
  fail("строка подписки молчит про остаток или возраст снимка: " + dump(hrows[0]));
}
if (!dump(hrows[1]).includes("снимка квоты нет")) {
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
if (!dump(groups).includes("Выполни XR-6")) {
  fail("надпись под кнопкой не называет заказ дословно: " + dump(groups).slice(0, 400));
}
// Там же сказано, куда поедет работа: при двух подписках надпись называет ту,
// на которой поднимет широкая часть кнопки (макет, фрейм состояний). До
// DK-336 подпись появлялась только там, где выбирать не из чего, и человек
// жал кнопку, не зная, чью квоту он тратит.
if (!dump(groups).includes("Поедет на " + harnessOne.name + ", подписок на машине две")) {
  fail("надпись под полосой действий не называет подписку по умолчанию: " +
    dump(groups).slice(0, 400));
}
timers.length = 0;
byId.get("flashes").replaceChildren();
taskRun.handlers.click({ stopPropagation: () => {} });
await settle();
if (sandbox.location.hash !== "demo/agent/XR-6") {
  fail("удачный запуск с экрана задачи не увёл на экран этой работы: " + sandbox.location.hash);
}
if (!find(groups, "agent-panes-XR-6")) {
  fail("после перехода экран агента не собрался: " + dump(groups).slice(0, 300));
}
if (!dump(byId.get("flashes")).includes("сессия поднята")) {
  fail("переход на экран работы стёр карточку ответа на нажатие: " + dump(byId.get("flashes")));
}
for (const t of timers.splice(0)) t.fn();
byId.get("flashes").replaceChildren();

// Черновики: строки и прокрутка переживают обновление, а кнопка разбора зовёт
// груминг грумингом (DK-321).
running = false;
await go("#demo/drafts");
const wrap = find(groups, "XR-D2");
if (!wrap) fail("накопитель не собрался: записи XR-D2 нет");
if (!button(wrap, "Провести груминг")) {
  fail("кнопка строки накопителя не зовёт груминг грумингом: " + dump(wrap));
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

// Груминг со строки накопителя ведёт на экран записи, а не на общий экран
// агента: у того нет ни текста черновика, ни исхода разбора (LLD DK-328,
// «Отвергнутое»). Подсказка кнопки называет заказ дословно, а переход не
// теряет карточку ответа (DK-286).
const d3Row = find(groups, "XR-D3");
if (!d3Row) fail("в накопителе нет записи XR-D3: " + dump(groups).slice(0, 300));
const d3Groom = button(d3Row, "Провести груминг");
if (!d3Groom) fail("у записи XR-D3 нет кнопки груминга: " + dump(d3Row));
if (!String(d3Groom.title).includes("Проведи груминг XR-D3")) {
  fail("подсказка кнопки груминга не называет заказ дословно: " + JSON.stringify(d3Groom.title));
}
timers.length = 0;
byId.get("flashes").replaceChildren();
d3Groom.handlers.click({ stopPropagation: () => {} });
await settle();
if (sandbox.location.hash !== "demo/draft/XR-D3") {
  fail("груминг со строки накопителя увёл не на экран записи: " + sandbox.location.hash);
}
if (!find(groups, "draft-head")) {
  fail("после перехода экран записи не собрался: " + dump(groups).slice(0, 300));
}
if (!dump(byId.get("flashes")).includes("груминг XR-D3 поднят")) {
  fail("переход на экран записи стёр карточку ответа на нажатие: " + dump(byId.get("flashes")));
}
for (const t of timers.splice(0)) t.fn();
byId.get("flashes").replaceChildren();

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

// Строка накопителя ведёт на экран записи: разворот текста в строке уступил
// место одному экрану с текстом, ходом груминга и его исходом.
wrap.handlers.click({ target: makeNode("span") });
if (sandbox.location.hash !== "demo/draft/XR-D2") {
  fail("нажатие на строку накопителя не открыло экран записи: " + sandbox.location.hash);
}

// Экран черновика, состояние первое: груминг идёт. Живой хвост берётся снимком
// tmux-сессии, и рядом стоит стоп; кнопки подъёма второго разбора нет.
grooming = true;
await go("#demo/draft/XR-D2");
let dhead = find(groups, "draft-head");
if (!dhead) fail("экран черновика не собрался: " + dump(groups).slice(0, 200));
if (!dump(dhead).includes("груминг идёт")) {
  fail("идущий груминг ничем не помечен: " + dump(dhead));
}
if (button(dhead, "Провести груминг")) {
  fail("поверх идущего груминга экран предлагает поднять второй: " + dump(dhead));
}
const runCol = find(groups, "draft-col-run");
if (!runCol || !dump(runCol).includes("хвост груминга")) {
  fail("живого хвоста груминга на экране нет: " + dump(runCol));
}
// Стоп стоит в шапке карточки хода, рядом с именем сессии, которую он снимает
// (макет 12), и собран мелкой красной кнопкой. В шапке экрана его больше нет.
const runStop = button(runCol, "Остановить груминг");
if (!runStop) fail("у идущего груминга нет стопа в шапке карточки хода: " + dump(runCol));
if (!String(runStop.className).includes("btn-sm") ||
  !String(runStop.className).includes("btn-danger")) {
  fail("стоп груминга собран не мелкой красной кнопкой: " + runStop.className);
}
if (button(dhead, "Остановить груминг")) {
  fail("стоп груминга остался и в шапке экрана: " + dump(dhead));
}

// Колонка хода видна и на телефоне. Класс tmuxbar на ней гасил её целиком:
// правило `.tmuxbar{display:none}` заведено для вкладок экрана агента, а на
// экране черновика вкладок нет, и onpane ей никто не ставит. С телефона за
// идущим грумингом было не посмотреть, хотя шапка звала его идущим (браузерная
// приёмка DK-321). Классы берутся с нарисованного узла, а правила из
// настоящего style.css: своих представлений о стилях у стенда нет.
const phoneCSS = mediaBlock(findCSS, "@media (max-width:900px)");
if (!phoneCSS) fail("в style.css нет телефонной части: правило про колонку хода искать негде");
const runClasses = String(runCol.className).split(" ").filter(Boolean);
if (phoneDisplay(phoneCSS, runClasses) === "none") {
  fail("на телефоне колонка хода груминга погашена стилями (классы " +
    runClasses.join(", ") + "): живого хвоста с телефона не видно");
}
// Место в сетке у неё при этом своё: погашенная колонка и колонка без области
// это разные поломки, и вторая тут тоже не годится.
if (!/\.dgrid\{[^}]*grid-template-areas:"out" "run" "text"/.test(phoneCSS)) {
  fail("телефонная сетка экрана черновика не отводит места ходу груминга");
}

// Обновление по фокусу окна не рвёт хвост: карточка хода собирается один раз
// на заход, как панели экрана агента.
await sandbox.refresh();
await settle();
if (find(groups, "draft-col-run") !== runCol) {
  fail("обновление пересобрало карточку хода груминга: хвост оборвался бы на каждом фокусе окна");
}

// Состояние второе: груминг кончился исходом. Исходов четыре, и каждый назван
// вместе со следом, по которому сервер его узнал.
grooming = false;
const outcomes = [
  [{ state: "row", note: "груминг завёл строку: XR-D2 стоит на доске demo, секция Backlog" },
    "Черновик оформлен строкой", "Открыть задачу XR-D2"],
  [{ state: "attached", task: "XR-4", task_file: "docs/tasks/XR-4.md",
    note: "груминг признал запись частью стоящей работы: текст уехал разделом «Из черновика XR-D2» в docs/tasks/XR-4.md" },
  "Черновик приписан к стоящей строке", "Открыть XR-4"],
  [{ state: "deferred", deferred: "2026-08-05", reason: "ждём повторного случая",
    file: "docs/tasks/drafts/XR-D2.md",
    note: "груминг отложил запись 2026-08-05: она осталась в накопителе" },
  "Черновик отложен", ""],
  [{ state: "dropped", note: "следов груминга нет ни одного: файла docs/tasks/drafts/XR-D2.md не видно" },
    "Черновик удалён", ""],
];
for (const [out, head, go2] of outcomes) {
  draftOut = out;
  await go("#demo/draft/XR-D2");
  const col = find(groups, "draft-col-out");
  if (!col || !dump(col).includes(head)) {
    fail("исход " + out.state + " на экране не назван: " + dump(col));
  }
  if (!dump(col).includes(out.note)) {
    fail("экран не сказал, каким следом узнан исход " + out.state + ": " + dump(col));
  }
  if (go2 && !button(col, go2)) {
    fail("у исхода " + out.state + " нет перехода туда, куда груминг увёл запись: " + dump(col));
  }
}

// Состояние третье: груминг кончился вопросом. Вопрос стоит карточкой, под ним
// поле уточнения, и ответ уходит новой ходкой, а не в закрывшуюся сессию.
draftOut = {
  state: "open", file: "docs/tasks/drafts/XR-D2.md",
  question: "Какой из двух дублей оставить, XR-D2 или XR-D3?",
  session: "abcdef1234567890",
  note: "груминг вышел, не оставив следа на диске",
};
await go("#demo/draft/XR-D2");
let outCol = find(groups, "draft-col-out");
if (!dump(outCol).includes("Груминг кончился вопросом")) {
  fail("кончившийся вопросом груминг назван иначе: " + dump(outCol));
}
if (!dump(outCol).includes(draftOut.question)) {
  fail("вопрос груминга не стоит на экране: " + dump(outCol));
}
if (!dump(outCol).includes("новой ходкой")) {
  fail("экран не говорит, что ответ уходит новой ходкой: " + dump(outCol));
}
const ask = tag(outCol, "TEXTAREA");
if (!ask) fail("поля уточнения на экране нет: " + dump(outCol));
ask.value = "оставить XR-D2, второй дубль снять";
ask.focus();

await sandbox.refresh();
await settle();

outCol = find(groups, "draft-col-out");
if (tag(outCol, "TEXTAREA") !== ask || ask.value !== "оставить XR-D2, второй дубль снять") {
  fail("обновление стёрло набранное уточнение: " + ask.value);
}
if (doc.activeElement !== ask) fail("обновление отобрало фокус у поля уточнения");
const againBtn = button(outCol, "Повторить груминг");
if (!String(againBtn.className).includes("dend")) {
  fail("«Повторить груминг» стоит не по правому краю поля: " + againBtn.className);
}
againBtn.handlers.click({ stopPropagation: () => {} });
await settle();
if (groomAsk !== "оставить XR-D2, второй дубль снять") {
  fail("повторная ходка ушла без уточнения: " + JSON.stringify(groomAsk));
}

// Удаление: подтверждение раскрывается на месте, без причины запрос не уходит,
// а набранная причина переживает обновление экрана.
draftOut = { state: "open", file: "docs/tasks/drafts/XR-D2.md", note: "груминг записи не касался" };
await go("#demo/draft/XR-D2");
outCol = find(groups, "draft-col-out");
const del = button(outCol, "Удалить");
if (!del) fail("удаления с экрана записи нет: " + dump(outCol));
const confirm = byClass(outCol, "dconfirm");
if (!confirm || !confirm.hidden) fail("подтверждение удаления раскрыто до нажатия");
del.handlers.click({ stopPropagation: () => {} });
if (confirm.hidden) fail("нажатие «Удалить» не раскрыло подтверждение с полем причины");
const why = tag(confirm, "INPUT");
if (!why) fail("в подтверждении нет поля причины: " + dump(confirm));
// Раскладка подтверждения по макету 12: причина своей строкой, кнопки под ней
// одной строкой и полного размера, без мелких btn-sm.
const drow = byClass(confirm, "drow");
if (!drow || drow.children.length !== 2) {
  fail("кнопки подтверждения стоят не своей строкой: " + dump(confirm));
}
for (const kid of drow.children) {
  if (String(kid.className).includes("btn-sm")) {
    fail("кнопка подтверждения осталась мелкой: " + kid.className);
  }
}
button(confirm, "Удалить черновик").handlers.click({ stopPropagation: () => {} });
await settle();
if (dropped !== null) fail("черновик удалился без причины: " + JSON.stringify(dropped));
if (!dump(byId.get("flashes")).includes("жду причину")) {
  fail("отказ без причины не сказан словами: " + dump(byId.get("flashes")));
}
for (const t of timers.splice(0)) t.fn();
byId.get("flashes").replaceChildren();

why.value = "мусор из одного слова show";
await sandbox.refresh();
await settle();
if (tag(byClass(find(groups, "draft-col-out"), "dconfirm"), "INPUT") !== why) {
  fail("обновление закрыло подтверждение удаления вместе с набранной причиной");
}
button(byClass(find(groups, "draft-col-out"), "dconfirm"), "Удалить черновик")
  .handlers.click({ stopPropagation: () => {} });
await settle();
if (dropped !== "мусор из одного слова show") {
  fail("причина удаления не уехала в ручку: " + JSON.stringify(dropped));
}
for (const t of timers.splice(0)) t.fn();
byId.get("flashes").replaceChildren();
// Чат: лента, поле ввода и поток событий переживают обновление, а пришедшая
// реплика не трогает соседних.
await go("#demo/chat/XR-1");
const thread = find(groups, "chat-thread-XR-1");
if (!thread) fail("экран чата не собрался: " + dump(groups).slice(0, 200));
// Цикл цели не идёт: чат говорит это словами и держит ту же ручку подъёма
// витка, что кнопка в ленте (DK-319). Молчаливое «ждёт витка» здесь и было
// бедой: человек писал завершившемуся агенту и ждал ответа.
const note = find(thread, "chat-note");
if (!dump(note).includes("Цикл цели не идёт")) {
  fail("чат молчит о стоящем цикле: " + dump(note));
}
const raise = button(note, "Поднять виток");
if (!raise || raise.hidden) fail("при стоящем цикле в чате нет ручки подъёма витка");
raise.handlers.click({ stopPropagation: () => {} });
await settle();
if (!running) fail("кнопка «Поднять виток» не позвала ручку запуска работы");
running = false;

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
if (raise !== button(find(thread, "chat-note"), "Поднять виток") || !raise.hidden) {
  fail("плашка чата не узнала о поднявшейся работе: ручка подъёма витка осталась на месте");
}
if (!dump(find(thread, "chat-note")).includes("Сообщение уйдёт агенту")) {
  fail("при работающем агенте плашка чата не обещает доставку витку: " + dump(find(thread, "chat-note")));
}
if (!dump(find(groups, "chat-head")).includes("агент работает")) {
  fail("шапка чата не узнала о работе агента: " + dump(find(groups, "chat-head")));
}
if (doc.activeElement !== ta) fail("обновление шапки отобрало фокус у поля ввода");
if (wasWorks !== works) fail("стенд подменил источник работ мимо ручек");

// Цикл цели ведёт не только tmux-сессия дашборда: цель из реестра и живое окно
// человека для сервера та же живая работа (goalIdle в messages.go), и плашка
// обязана говорить то же, что ответит ручка сообщения. Разойдись признаки,
// экран показывал бы «Цикл цели не идёт» там, где ручка обещает виток
// (замечание ревью DK-319).
for (const via of ["registry", "session"]) {
  chatWork[0].via = via;
  await sandbox.refresh();
  await settle();
  const shown = find(thread, "chat-note");
  if (dump(shown).includes("Цикл цели не идёт")) {
    fail("плашка чата зовёт стоящим цикл, который ведёт " + via + ": " + dump(shown));
  }
  if (!dump(shown).includes("Сообщение уйдёт агенту")) {
    fail("плашка чата при работе via " + via + " не обещает доставку витку: " + dump(shown));
  }
  const up = button(shown, "Поднять виток");
  if (!up || !up.hidden) fail("при работе via " + via + " в чате осталась кнопка подъёма витка");
  const kill = find(thread, "chat-stop");
  if (!kill || !kill.hidden) fail("кнопка стопа зажглась у работы via " + via + ", которую стоп не берёт");
}
// Признак возвращается к tmux-сессии: дальше стенд идёт разделом экрана
// агента, писанным поверх работающей tmux-сессии.
chatWork[0].via = "tmux";

// Экран агента: у задачи два разговора, и в одну ленту они не смешиваются
// (DK-290). Экран перечитывается по фокусу окна, а список сессий стоит по
// времени последней записи: соседняя работа, дописавшая свой транскрипт,
// выходит наверх, и собранная заново лента открывалась по нулевому элементу,
// то есть меняла разговор под читающим.
const talkStreams = () => streams.filter((s) => String(s.url).includes("/sessions/"));
const liveTalks = () => talkStreams().filter((s) => !s.closed);
const talkReply = (es, seq, text) => {
  es.onmessage({ data: JSON.stringify({ seq, role: "assistant", text, time: "2026-08-13T10:05:00+03:00" }) });
};

sessions = [mine, alien];
await go("#demo/agent/XR-1");
const panes = find(groups, "agent-panes-XR-1");
if (!panes) fail("экран агента не собрался: " + dump(groups).slice(0, 300));
const seg = byClass(panes, "tseg");
if (!seg || seg.children.length !== 2) {
  fail("разговоров задачи на экране агента не видно списком: " + dump(panes).slice(0, 300));
}
// Подпись разговора: время последней записи, дерево или ветка, первая реплика.
// Время берётся тем же переводом, что и на экране, иначе стенд держал бы пояс
// машины, на которой его гоняют.
for (const want of [sandbox.localTime(alien.mtime), alien.tree, alien.first, mine.first]) {
  if (!dump(seg).includes(want)) {
    fail("в списке разговоров нет подписи " + JSON.stringify(want) + ": " + dump(seg));
  }
}
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(mine.id)) {
  fail("открыт не свежий разговор: " + JSON.stringify(liveTalks().map((s) => s.url)));
}
const ours = liveTalks()[0];
talkReply(ours, 1, "ход первого разговора");
await settle();
if (!dump(panes).includes("ход первого разговора")) {
  fail("реплика открытого разговора не встала в ленту: " + dump(panes).slice(0, 300));
}

// Соседняя работа дописала транскрипт и вышла в списке наверх, а окно вернуло
// себе фокус.
sessions = [alien, mine];
await sandbox.refresh();
await settle();

if (find(groups, "agent-panes-XR-1") !== panes) {
  fail("обновление по фокусу окна пересобрало панели экрана агента");
}
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(mine.id)) {
  fail("обновление сменило открытый разговор на соседний: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}
if (!dump(panes).includes(mine.id.slice(0, 8))) {
  fail("подпись ленты не называет открытый разговор: " + dump(panes).slice(0, 300));
}
if (!dump(panes).includes("ход первого разговора")) {
  fail("обновление собрало ленту заново: прежние реплики пропали");
}
talkReply(ours, 2, "ещё реплика первого");
await settle();
if (!dump(panes).includes("ещё реплика первого")) {
  fail("после обновления лента дописывается мимо экрана");
}

// Переключение на соседний разговор: лента собирается заново им одним,
// дострение прежнего снимается, и реплики двух разговоров рядом не стоят.
seg.children[1].handlers.click();
await settle();
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(alien.id)) {
  fail("переключатель не открыл соседний разговор: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}
talkReply(liveTalks()[0], 1, "ход соседнего разговора");
await settle();
if (!dump(panes).includes("ход соседнего разговора")) {
  fail("реплика соседнего разговора не встала в ленту после переключения");
}
if (dump(panes).includes("ход первого разговора")) {
  fail("лента склеила разговоры: реплики прежнего остались под репликами соседнего");
}

// Уход с экрана и возврат: список опять стоит свежим разговором сверху, а
// открывается выбранный человеком.
sessions = [mine, alien];
await go("#demo");
await go("#demo/agent/XR-1");
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(alien.id)) {
  fail("возврат на экран открыл не выбранный разговор: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}

// Уход с экрана на середине запроса за сессиями: запоздавший ответ поднимал
// поток уже после того, как остальные закрыли, и закрывать его было некому.
slowSessions = true;
await go("#demo");
sandbox.location.hash = "#demo/agent/XR-1";
await sandbox.refresh();
sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();
if (liveTalks().length) {
  fail("уход с экрана оставил поток разговора живым: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}

// Вкладка по умолчанию на телефоне (DK-305): у живой работы это «Журнал», а
// у законченной «Лог витка». Журнал законченной работы пуст, а разговор ради
// него и приходят смотреть на узкий экран: второе касание экрану больше не
// нужно.
slowSessions = false;
sessions = [mine, alien];
await go("#demo/agent/XR-1");
let livePanes = find(groups, "agent-panes-XR-1");
if (!livePanes) fail("экран агента живой работы не собрался");
let tabSeg = byClass(livePanes, "seg");
if (!tabSeg) fail("на экране агента нет переключателя вкладок телефона");
if (tabSeg.children[0].className !== "on" || tabSeg.children[1].className === "on") {
  fail("у живой работы вкладкой по умолчанию встал не «Журнал»: " + dump(tabSeg));
}
let grid = livePanes.children[1];
if (!String(grid.children[0].className).includes("onpane")) {
  fail("у живой работы панель «Журнал» не открыта вкладкой: " + grid.children[0].className);
}

// Обновление ленты не перебивает ручной выбор человека: панели собираются
// один раз на заход и обновлением по фокусу окна не пересобираются.
tabSeg.children[1].handlers.click();
await settle();
if (tabSeg.children[1].className !== "on" || tabSeg.children[0].className === "on") {
  fail("нажатие на вкладку «Лог витка» её не выбрало: " + dump(tabSeg));
}
await sandbox.refresh();
await settle();
if (byClass(find(groups, "agent-panes-XR-1"), "seg").children[1].className !== "on") {
  fail("обновление ленты сбросило выбранную человеком вкладку");
}

// Работа кончилась: тот же id больше не встречается среди works, экран агента
// открывается вкладкой «Лог витка» по умолчанию.
const wasChatId = chatWork[0].id;
chatWork[0].id = "XR-9";
await go("#demo");
await go("#demo/agent/XR-1");
chatWork[0].id = wasChatId;
const donePanes = find(groups, "agent-panes-XR-1");
if (!donePanes) fail("экран агента законченной работы не собрался");
tabSeg = byClass(donePanes, "seg");
if (tabSeg.children[1].className !== "on" || tabSeg.children[0].className === "on") {
  fail("у законченной работы вкладкой по умолчанию встал не «Лог витка»: " + dump(tabSeg));
}
grid = donePanes.children[1];
if (!String(grid.children[1].className).includes("onpane")) {
  fail("у законченной работы панель «Лог витка» не открыта вкладкой: " + grid.children[1].className);
}

// Разговор живой сессии, чью задачу узнать не удалось (DK-294). Карточка такой
// сессии в полосе живых работ стояла мёртвой: экран агента открывался по ID
// задачи, а его-то у неё и нет, при том что транскрипт лежит на диске. Теперь
// карточка ведёт на тот же экран вторым входом, по id сессии.
chatWork.push({ kind: "session", via: "session", session: loose.id, note: "задача не распознана" });
await go("#demo");
const band = byId.get("live");
const looseCard = band.children[band.children.length - 1];
if (!dump(looseCard).includes("интерактивная сессия")) {
  fail("в полосе живых работ нет карточки нераспознанной сессии: " + dump(band));
}
const looseLabel = tag(looseCard, "B");
if (!looseLabel || !looseLabel.handlers.click) {
  fail("карточка нераспознанной сессии никуда не ведёт: разговор с полосы не открыть");
}
looseLabel.handlers.click();
if (!String(sandbox.location.hash).endsWith("demo/session/" + loose.id)) {
  fail("карточка ведёт не на разговор по id сессии: " + sandbox.location.hash);
}

await go("#demo/session/" + loose.id);
const sessPanes = find(groups, "agent-panes-session-" + loose.id);
if (!sessPanes) fail("экран разговора по id сессии не собрался: " + dump(groups).slice(0, 300));
const sessHead = find(groups, "agent-head");
if (!dump(sessHead).includes(loose.first)) {
  fail("шапка разговора взята не из первой реплики: " + dump(sessHead));
}
if (!dump(sessHead).includes("задача не распознана")) {
  fail("шапка разговора молчит о том, что задача не узнана: " + dump(sessHead));
}
// Журнал и tmux названы отсутствующими, а не нарисованы пустыми: оба ищутся по
// ID задачи, и брать их у такого разговора неоткуда.
for (const want of ["Журнал агента ищется по ID задачи", "tmux-сессия ищется по ID задачи"]) {
  if (!dump(sessPanes).includes(want)) {
    fail("на экране разговора нет слов о нехватке (" + want + "): " + dump(sessPanes).slice(0, 300));
  }
}
if (streams.some((s) => !s.closed && String(s.url).includes("/log?stream=1"))) {
  fail("экран разговора поднял поток журнала, которого у сессии нет");
}
// Лента дострачивается потоком этой самой сессии.
if (liveTalks().length !== 1 || !liveTalks()[0].url.includes(loose.id)) {
  fail("на экране разговора открыт не поток этой сессии: " +
    JSON.stringify(liveTalks().map((s) => s.url)));
}
talkReply(liveTalks()[0], 1, "ход нераспознанного разговора");
await settle();
if (!dump(sessPanes).includes("ход нераспознанного разговора")) {
  fail("реплика не встала в ленту экрана, открытого по id сессии");
}
chatWork.pop();

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
const bcard = layout(find(groups, "card-backlog"));
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
const card2 = layout(find(groups, "card-backlog"));
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
if (find(groups, "card-backlog") !== card2 || !row6.classList.contains("dragrow")) {
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
const cardM = layout(find(groups, "card-backlog"));
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
const card3 = layout(find(groups, "card-backlog"));
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

console.log("частичная перерисовка: доска, черновики и лента чата держат место и фокус, " +
  "экран агента держит выбранный разговор, разговор нераспознанной сессии " +
  "открывается с полосы работ по id сессии, ответ на нажатие не двигает раскладку; " +
  "поиск: выдача своим экраном, набор одним запросом, поле держит курсор, " +
  "косая черта и лупа ведут в поиск; подписки: широкая часть кнопки идёт на " +
  "подписку по умолчанию, строка списка на свою, без выбора стрелки нет, " +
  "вид кнопки и списка по макету; кнопки действий: стоп красный, "
  + "подтверждение удаления полноразмерное своей строкой; " +
  "экран черновика: три состояния груминга, уточнение новой ходкой, удаление с причиной; " +
  "перетаскивание: коридор со щелями на живом списке, долгое нажатие против " +
  "пролистывания, правка одной ценностью и откат с ожидаемой разбивкой");
