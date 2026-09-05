// Экраны доски и задачи плюс панель разговора справа: список проектов, живые
// работы, секции со строками, запуск и стоп, журнал витка и лента разговора.
// Клиент только рисует готовый JSON и шлёт команды (решение
// LLD DK-112); все тексты вставляются через textContent, HTML из данных не
// собирается. Стоп называется стопом: возобновление это новый запуск,
// читающий состояние с диска.

// Порядок секций на экране, а не с доски: сервер отдаёт их своим порядком, а
// раскладывает список этот перечень. Blocked стоит выше Backlog нарочно.
// Припаркованная задача ждёт человека, и под очередью в сотню строк её никто
// не видел: «Blocked стоит после Backlog, а задачи там требуют внимания»
// (замечание пользователя).
const SECTION_ORDER = ["in-progress", "check", "blocked", "backlog"];

function el(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

// Частичная перерисовка (DK-316). Экран перечитывается по фокусу окна и по
// приходу события, и собранный заново список уводит из-под пальца всё сразу:
// место, на котором стоял человек, кнопку под фокусом, раскрытую запись
// черновика. Слой обновляет только изменившиеся узлы: у узла есть ключ (кто
// это) и отпечаток (из чего он нарисован). Совпал отпечаток, значит рисовать
// нечего, разошёлся, значит меняется один этот узел, а соседи остаются как
// стояли.

// Ключ и отпечаток живут на самом узле: между обновлениями клиентская часть
// не хранит состояние, а перерисовка приходит к уже нарисованному дереву и
// читает их оттуда.
function keyed(node, key, sign) {
  node.dataset.pkey = key;
  node.dataset.psign = sign === undefined ? "" : String(sign);
  return node;
}

// Дети коробки по описаниям вида {key, sign, make, fill}: make рисует узел с
// нуля, fill правит нарисованный по месту. Без fill изменившийся узел
// рисуется заново, но встаёт туда же, где стоял, и соседей это не задевает;
// fill нужен там, где узел держит живое: открытый поток событий, набранный
// текст, раскрытую запись.
function sync(box, items) {
  const was = new Map();
  for (const kid of Array.from(box.children)) {
    const key = kid.dataset && kid.dataset.pkey;
    if (key) was.set(key, kid);
  }
  const want = [];
  for (const item of items) {
    const sign = item.sign === undefined ? "" : String(item.sign);
    let node = was.get(item.key);
    if (!node) {
      node = item.make();
    } else if ((node.dataset.psign || "") !== sign) {
      if (item.fill) item.fill(node);
      else node = item.make();
    }
    want.push(keyed(node, item.key, sign));
  }
  // Порядок правится по месту: узел, стоящий там, где надо, не трогается
  // вовсе. Опустевшая коробка сбрасывает прокрутку, поэтому лишнее снимается
  // по одному, а не общим replaceChildren.
  let at = 0;
  for (const node of want) {
    if (box.children[at] !== node) box.insertBefore(node, box.children[at] || null);
    at += 1;
  }
  while (box.children.length > want.length) {
    box.removeChild(box.children[box.children.length - 1]);
  }
  return want;
}

// Узел по ключу где угодно в поддереве: перерисовка могла заменить сам узел, и
// найти его снова можно только по ключу, а не по прежней ссылке.
function findKey(root, key) {
  for (const kid of (root && root.children) || []) {
    if (kid.dataset && kid.dataset.pkey === key) return kid;
    const hit = findKey(kid, key);
    if (hit) return hit;
  }
  return null;
}

// Где стоял фокус: ключ ближайшего узла списка и номера детей от него вниз.
// Дорогой, а не ссылкой, потому что узел под фокусом перерисовка могла
// заменить, а место его в списке осталось прежним. Фокус вне списков (поле
// шапки, боковая колонка) слой не трогает: там ничего и не перерисовывается.
function focusSnap() {
  const node = document.activeElement;
  if (!node || !node.parentElement) return null;
  const path = [];
  let cur = node;
  while (cur.parentElement && !(cur.dataset && cur.dataset.pkey)) {
    path.unshift(Array.prototype.indexOf.call(cur.parentElement.children, cur));
    cur = cur.parentElement;
  }
  const key = cur.dataset && cur.dataset.pkey;
  if (!key) return null;
  const snap = { node, key, path };
  // Каретка в поле ввода это часть места: вернуть фокус в набранный текст и
  // поставить курсор в начало значит потерять то же самое.
  if (typeof node.selectionStart === "number") {
    snap.from = node.selectionStart;
    snap.to = node.selectionEnd;
  }
  return snap;
}

function focusBack(snap) {
  if (!snap) return;
  // Узел пережил обновление: трогать фокус незачем, лишний focus() ещё и
  // дёрнул бы экран к нему.
  if (document.activeElement === snap.node) return;
  let node = findKey(document.getElementById("groups"), snap.key);
  for (const at of snap.path) node = node && node.children[at];
  if (!node || !node.focus) return;
  node.focus();
  if (snap.from !== undefined && typeof node.setSelectionRange === "function") {
    node.setSelectionRange(snap.from, snap.to);
  }
}

// Якорь прокрутки: узел, по которому после перерисовки видно, сдвинулась ли
// картинка. Берётся тот, на котором стоял фокус, иначе первый видимый узел
// списка: на него человек и смотрит.
function anchorKey(groups, focus) {
  if (!groups) return "";
  if (focus && findKey(groups, focus.key)) return focus.key;
  const top = groups.getBoundingClientRect().top;
  for (const kid of groups.children) {
    if (!(kid.dataset && kid.dataset.pkey)) continue;
    if (kid.getBoundingClientRect().bottom > top) return kid.dataset.pkey;
  }
  return "";
}

// Место на экране целиком: прокрутка списка, фокус и якорь. Список прокручен
// внутри себя (.groups), а не окном, поэтому мерится он, а не страница.
// Сколько раз человек листал список. Обновление идёт через сеть, и пока ответ
// летит, палец успевает увести список: вернуть тогда прежнее место значит
// дёрнуть экран из-под пальца, и читать список на ходу становится нельзя
// (замечание пользователя). Считается тут именно человеческое листание:
// перерисовка свою прокрутку двигает синхронно, а событие о ней браузер шлёт
// позже, когда возвращать уже нечего.
let scrollSeen = 0;

// Слушатель ставится один раз на узел списка: узел живёт всю страницу, а
// экраны в нём сменяются.
function wireScrollWatch() {
  const groups = document.getElementById("groups");
  if (!groups || groups.dataset.scrollwired) return;
  groups.dataset.scrollwired = "1";
  groups.addEventListener("scroll", () => { scrollSeen += 1; });
}

function viewSnap() {
  const groups = document.getElementById("groups");
  wireScrollWatch();
  const focus = focusSnap();
  const key = anchorKey(groups, focus);
  const at = key ? findKey(groups, key) : null;
  return {
    groups,
    top: groups ? groups.scrollTop : 0,
    seen: scrollSeen,
    focus,
    key,
    at: at ? at.getBoundingClientRect().top : 0,
  };
}

function viewBack(snap) {
  if (!snap || !snap.groups) return;
  const groups = snap.groups;
  // Человек листал, пока летел ответ: место у списка теперь его, а не наше.
  // Возврат снимка тут и есть тот рывок, на который жалуются («экран
  // дёргается в момент обновления»).
  if (snap.seen !== undefined && scrollSeen !== snap.seen) {
    focusBack(snap.focus);
    return;
  }
  // Прокрутку возвращать приходится тем экранам, которые собираются целиком:
  // опустевший список сбрасывает её в ноль. Перерисованный по месту её и не
  // терял, и присваивание тут ничего не меняет.
  if (groups.scrollTop !== snap.top) groups.scrollTop = snap.top;
  focusBack(snap.focus);
  // Сдвиг раскладки над списком: полоса живых работ прибавляет карточку с
  // поднятой работой, и список едет вниз на её высоту. У верха списка сдвигать
  // нечего, там картинка и так стоит на месте.
  if (!snap.key || !snap.top) return;
  const node = findKey(groups, snap.key);
  if (!node) return;
  const dy = node.getBoundingClientRect().top - snap.at;
  if (dy) groups.scrollTop = snap.top + dy;
}

async function api(path, opts) {
  const init = {};
  if (opts && opts.method) init.method = opts.method;
  if (opts && opts.body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(opts.body);
  }
  const resp = await fetch(path, init);
  if (resp.status === 401) {
    location.href = "/login";
    throw new Error("нужен вход");
  }
  return { ok: resp.ok, status: resp.status, body: await apiBody(resp) };
}

// Ответ бывает и не от дашборда: до него стоит внешний вход, и свой отказ
// (413 на длинное тело, 502 на упавший бэкенд) он пишет страницей html.
// Разбор такой страницы падал SyntaxError, и человек читал жалобу движка js
// вместо причины (жалоба пользователя на снимок через внешний вход). Тут
// нечитаемое тело сводится к тем же полям, что у ответа дашборда: код и слова.
async function apiBody(resp) {
  const text = await resp.text();
  try {
    return JSON.parse(text);
  } catch (e) {
    return { error: outerFail(resp.status, resp.statusText, text) };
  }
}

// Слова про отказ внешнего входа: известные коды названы по-человечески, у
// остальных берётся код со статусом, а хвост страницы отбрасывается, в нём
// разметка.
//
// У 502 и 504 сказан один факт: связи с дашбордом на машине человека нет.
// Догадок о причинах тут нет нарочно, ни строкой, ни подсказкой: уснувший
// ноутбук, моргнувшая сеть и перезапуск дашборда человеку не помогали и
// читались лишними словами (замечание пользователя). Коды разведены одним
// словом: 502 это связи нет вовсе, 504 это истёк срок ожидания.
//
// Хвоста про судьбу набранного тут нет: он не влезал в строку рядом с самим
// отказом, а неушедшая реплика и так видна в ленте своим пузырём с кнопкой
// повтора, набранное же остаётся в форме и без обещаний.
function outerFail(status, statusText, text) {
  const who = "внешний вход";
  if (status === 413) {
    return "снимок слишком большой для внешнего входа (413): " +
      "уменьшите картинку или отправьте её меньшим куском";
  }
  const gone = "не удалось установить связь с дашбордом на вашем компьютере";
  if (status === 502) {
    return gone + " (502).";
  }
  if (status === 504) {
    return gone + ", истёк срок ожидания (504).";
  }
  const said = String(text || "").replace(/<[^>]*>/g, " ").replace(/\s+/g, " ").trim();
  return who + " ответил " + status + (statusText ? " " + statusText : "") +
    (said ? ": " + truncate(said, 120) : "");
}

// Всплывающие контейнеры экрана (список кольца, выбор подписки, меню плюса,
// выпадающий список чатов) закрываются одинаково и тремя путями: повторным
// нажатием по своей кнопке, кликом мимо и Escape. Прежде каждый заводил свою
// закрывалку, у кольца её не было вовсе, и открытый список висел до ухода с
// экрана (жалоба пользователя). Учёт общий нарочно: правило «мимо закрывает»
// разъезжается ровно тогда, когда его пишут по разу на всплывашку.
const popupsOpen = new Set();

// popupHold ставит открытую всплывашку на учёт: node это её контейнер, клик
// внутри него закрытия не значит, shut прячет её саму.
function popupHold(node, shut) {
  const held = { node, shut };
  popupsOpen.add(held);
  return held;
}

// popupDrop снимает всплывашку с учёта, не закрывая: так уходит та, которую
// закрыли своей же кнопкой.
function popupDrop(held) {
  if (held) popupsOpen.delete(held);
}

// nodeInside отвечает, лежит ли узел внутри контейнера. Обход идёт по
// родителям, а не через node.contains: в дереве стенда contains это проверка
// класса, и всплывашка закрывалась бы от клика по себе самой.
function nodeInside(box, node) {
  for (let at = node; at; at = at.parentNode) {
    if (at === box) return true;
  }
  return false;
}

// popupsShut закрывает открытые всплывашки, кроме той, внутри которой стоит
// названный узел. Без узла закрываются все: так уходит список, когда рядом
// открывают соседний.
function popupsShut(target) {
  for (const held of [...popupsOpen]) {
    if (target && nodeInside(held.node, target)) continue;
    popupsOpen.delete(held);
    held.shut();
  }
}

document.addEventListener("click", (ev) => {
  if (!popupsOpen.size) return;
  popupsShut(ev.target);
});

document.addEventListener("keydown", (ev) => {
  if (ev.key !== "Escape" || !popupsOpen.size) return;
  // Escape гасит только всплывашки: панель разговора и формы держат свои
  // ответы на ту же клавишу, и отбирать её у них нечем.
  if (ev.stopPropagation) ev.stopPropagation();
  popupsShut(null);
});

// Хэш это экран: пустой хэш главная (список проектов), "#проект" доска,
// "#проект/DK-NNN" задача, "#проект/draft/DK-NNN" запись накопителя с
// грумингом и его исходом, "#проект/sess" сессии проекта, "#проект/feed" лента
// уведомлений. Прежний адрес раздела «Агенты» ("#/agents" с запросом хвостом)
// никуда не ведёт сам по себе: он переезжает на таб сессий текущего проекта
// вместе с набранным запросом.
//
// Панель разговора это хвост адреса, а не свой экран (LLD DK-430, решение 5):
// "#проект/chat/<адрес>" открывает её над доской, "#проект/DK-NNN/chat/<адрес>"
// над экраном задачи. Хвост отрезается первым, остальное читается как раньше,
// поэтому панель встаёт над любым экраном проекта и своего состояния не
// заводит. Адрес разговора это либо id сессии, либо ID задачи, и второе значит
// «последний разговор этой задачи».
//
// Старые адреса ведут в ту же панель и продолжают работать ссылками:
// "#проект/agent/DK-NNN" это экран задачи с открытой панелью её разговора,
// "#проект/session/<id>" доска с панелью этой сессии, а "#проект/chat/DK-NNN"
// ложится в новую форму как есть.
function route() {
  const h = decodeURIComponent(location.hash.replace(/^#/, ""));
  const cutChat = h.indexOf("/chat/");
  if (cutChat >= 0) {
    const rt = routeScreen(h.slice(0, cutChat));
    rt.chat = h.slice(cutChat + "/chat/".length);
    return rt;
  }
  const old = h.match(/^([^/]+)\/(agent|session)\/(.+)$/);
  if (old) {
    const rt = routeScreen(old[1] + (old[2] === "agent" ? "/" + old[3] : ""));
    rt.chat = old[3];
    return rt;
  }
  return routeScreen(h);
}

function routeScreen(h) {
  const parts = h.split("/");
  if (!h) return { proj: "", id: "", home: true };
  // Запрос раздела живёт в адресе, как у выдачи поиска задач: набранное
  // переживает обновление экрана и кнопку «назад».
  if (h === "/agents" || parts[0] === "" && parts[1] === "agents") {
    return { proj: "", id: "", agents: true, q: parts.slice(2).join("/") };
  }
  if (parts.length >= 2 && parts[1] === "feed") {
    return { proj: parts[0], id: "", feed: true };
  }
  // Заведение спрашивает, что заводят, и только потом открывает свою форму:
  // одна форма на оба случая мешала поля черновика с полями строки доски
  // (замечание пользователя про кашу полей). Вид стоит в адресе, поэтому на
  // форму можно сослаться и вернуться в неё кнопкой «назад».
  if (parts.length >= 2 && parts[1] === "new") {
    return { proj: parts[0], id: "", make: true, kind: parts[2] || "" };
  }
  if (parts.length >= 2 && parts[1] === "drafts") {
    return { proj: parts[0], id: parts[2] || "", drafts: true };
  }
  // Сессии проекта это третий таб доски: свой адрес нужен ему по той же
  // причине, что и накопителю, чтобы таб переживал обновление и кнопку
  // «назад», а набранный запрос жил в адресе.
  if (parts.length >= 2 && parts[1] === "sess") {
    return { proj: parts[0], id: "", sess: true, q: parts.slice(2).join("/") };
  }
  // Запрос стоит в адресе (LLD DK-328): выдача становится ссылкой и переживает
  // кнопку «назад». Хвост собирается обратно, потому что косая черта в самом
  // запросе разрезала бы его на части.
  if (parts.length >= 2 && parts[1] === "find") {
    return { proj: parts[0], id: "", find: true, q: parts.slice(2).join("/") };
  }
  if (parts.length >= 3 && parts[1] === "draft") {
    return { proj: parts[0], id: parts[2], draft: true };
  }
  // Путь документа относителен docs/, как в колонке «Ссылка» доски: хвост
  // склеивается обратно, потому что косые черты в нём свои.
  if (parts.length >= 3 && parts[1] === "doc") {
    return { proj: parts[0], id: "", doc: true, path: parts.slice(2).join("/") };
  }
  // Раздел LLD; запрос поиска живёт в адресе, как у выдачи поиска задач.
  if (parts.length >= 2 && parts[1] === "lld") {
    return { proj: parts[0], id: "", lldList: true, q: parts.slice(2).join("/") };
  }
  const cut = h.indexOf("/");
  if (cut < 0) return { proj: h, id: "" };
  return { proj: h.slice(0, cut), id: h.slice(cut + 1) };
}

// Выбранный проект переживает уход на главную. Хэш главной пуст, и без памяти
// раздел «Доска» вёл оттуда на первый проект списка, а не на тот, с которого
// человек ушёл: выбор приходилось делать заново после каждого захода на
// главную (замечание пользователя).
const LAST_PROJECT_KEY = "devkit.dash.project";

function lastProject() {
  try {
    return localStorage.getItem(LAST_PROJECT_KEY) || "";
  } catch (e) {
    return "";
  }
}

function keepProject(name) {
  if (!name) return;
  try {
    localStorage.setItem(LAST_PROJECT_KEY, name);
  } catch (e) {
    // Приватный режим браузера запрещает запись: память выбора это удобство, и
    // ронять из-за неё экран незачем.
  }
}

function currentProject(projects) {
  const want = route().proj;
  // Проект из хэша запоминается: он и есть выбор человека, а не догадка экрана.
  if (want) {
    const named = projects.find((p) => p.name === want);
    if (named) keepProject(named.name);
    if (named) return named;
  }
  const kept = projects.find((p) => p.name === lastProject());
  return kept || projects[0] || null;
}

// Состояние проекта одним кружком (макет «00 Главная»): серый работы нет,
// зелёный идёт работа, красный требуется внимание. Внимание берётся с доски, а
// не из ленты: лента говорит о случившемся, а кружок о том, что ждёт сейчас, и
// задача в Check ждёт ровно человека. Цветом одним такое не сказать, поэтому
// рядом с зелёным и красным стоит причина словами, а у серого её нет: сказать
// о нём нечего.
function projectState(p) {
  const works = (p.works || []).length;
  if (works) {
    return { cls: "pd-run pulse", short: works + " в работе" };
  }
  const check = (p.sections && p.sections.check) || 0;
  if (check) return { cls: "pd-warn", short: "ждёт проверки" };
  return { cls: "", short: "" };
}

function renderSidebar(projects, current) {
  const nav = document.getElementById("projects");
  const sel = document.getElementById("pselect");
  // Колонка живёт на том же слое, что доска и лента: обновление по фокусу окна
  // перебирало её целиком, и открытая выпадашка шапки схлопывалась под рукой
  // вместе с колонкой (DK-411). Отпечаток строки это её вид: имя, подсветка
  // выбранного проекта, кружок состояния и число рядом.
  const here = current ? current.name : "";
  sync(nav, projects.map((p) => {
    const st = projectState(p);
    return {
      key: p.name,
      sign: [p.name === here ? "on" : "", st.cls || "", st.short || ""].join("|"),
      make: () => {
        const item = el("div", "sitem" + (p.name === here ? " on" : ""));
        item.append(el("span", "pdot" + (st.cls ? " " + st.cls : "")));
        item.append(document.createTextNode(p.name));
        item.append(el("span", "n", st.short));
        item.addEventListener("click", () => { goKeepingChat(p.name); });
        return item;
      },
    };
  }));
  sync(sel, projects.map((p) => ({
    key: p.name,
    sign: p.name === here ? "on" : "",
    make: () => {
      const opt = el("option", "", p.name);
      opt.value = p.name;
      opt.selected = p.name === here;
      return opt;
    },
  })));
  sel.onchange = () => { goKeepingChat(sel.value); };
  // Счётчика работ в колонке больше нет: сессии переехали в таб доски, и число
  // стоит на самом табе, рядом с тем списком, который оно считает.
}

// Ответ на нажатие всплывает карточкой поверх экрана, а не строкой над
// списком. Строкой он стоял в потоке документа, и появление слов о нажатии
// («конвейер задачи DK-136 поднят в tmux-сессии task-DK-136») двигало доску
// вниз на свою высоту: человек жал кнопку, а экран уезжал из-под пальца
// (приёмка DK-316). Карточка лежит в том же фиксированном углу, что и события
// ленты, и не трогает раскладку ни появлением, ни уходом.
//
// Ответ на экране один: слова о начале («запуск DK-136...») сменяются словами
// об исходе, а не копятся столбиком. Удача гаснет сама, отказ ждёт крестика:
// причину отказа человек читает, а не ловит.
const RESULT_LIFE = 6000;
let resultToast = null;

function sayResult(text, isError) {
  if (resultToast) resultToast.dismiss();
  resultToast = null;
  if (!text) return;
  const body = el("div", "ft");
  body.append(el("b", "", text));
  resultToast = toast({
    parts: [body],
    body,
    life: isError ? 0 : RESULT_LIFE,
    cls: isError ? "res err" : "res",
  });
}

// Переход на экран поднятой работы после удачного запуска (DK-286): обычный
// уход с экрана снимает сказанное (хэш-обработчик ниже стирает прежний
// ответ), а тут снимать нечего. Человек только что нажал кнопку и ждёт ровно
// этих слов. Флаг одноразовый: следующий хэш-переход, чей бы он ни был, снова
// стирает карточку, как и раньше.
let keepResult = false;

async function goKeepingResult(hash) {
  keepResult = true;
  goKeepingChat(hash);
  await refresh();
}

// Запуск и стоп. Ответ сервера показывается словами: и удача, и причина
// отказа (занятый замок, пропавший tmux или goal-run) видны с экрана.
// Подписка едет полем harness: пусто значит «как раньше», на подписке по
// умолчанию. afterOk это хэш экрана этой работы: заполнен на экране задачи,
// пуст на строке списка, где человек нарочно остаётся на доске (DK-316).
async function startRun(project, id, harness, afterOk, tier) {
  sayResult("запуск " + id + (harness ? " на подписке " + harness : "") + "...");
  const body = { id };
  if (harness) body.harness = harness;
  // Ярус едет только выбранный рукой: пустое поле это «как назначено», и ярус
  // тогда называет вердикт agentctl pick на стороне сервера.
  if (tier) body.tier = tier;
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/runs",
    { method: "POST", body });
  const said = r.body.message || r.body.error || "";
  if (r.ok && afterOk) {
    await goKeepingResult(afterOk);
    // Панель запуск не трогает вовсе: он оставляет след памятью подъёма, а
    // кнопка чата задачи по ней моргает рамкой.
    markRunLive(project, id, r.body.session || "");
    sayResult(said, false);
    return;
  }
  sayResult(said, !r.ok);
  if (r.ok) await refresh();
}

// Стоп строки. Сессию называют, когда по строке работает не одна: сервер тогда
// отвечает списком, а выбор остаётся за человеком (DK-716). Ответ возвращается
// целиком: строку таба сессий снимает с экрана тот, кто звал, и делать это по
// неудавшейся ручке нельзя, а список для выбора читает кнопка стопа.
async function stopRun(project, id, session) {
  sayResult("стоп " + id + "...");
  const tail = session ? "?session=" + encodeURIComponent(session) : "";
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/runs/"
    + encodeURIComponent(id) + tail, { method: "DELETE" });
  // Список сессий это не отказ, а вопрос: слова про него говорит сама кнопка
  // рядом с выбором, и красная строка результата тут сбивала бы с толку.
  const asks = !r.ok && r.body && Array.isArray(r.body.sessions) && r.body.sessions.length > 1;
  if (!asks) sayResult(r.body.message || r.body.error || "", !r.ok);
  if (r.ok) await refresh();
  return r;
}

// Полоски «работает N агентов» над доской больше нет вовсе. Она пережила два
// переезда и обессмыслилась на втором: сессии стоят своим табом с числом на
// нём, а строка над доской повторяла это число и уводила туда же вторым
// способом (решение пользователя).


// Кружок состояния перед номером строки: одна точка на все случаи вместо
// россыпи чипов. Зелёная это живая работа, жёлтая ожидание человека, серая
// работа не наша (чужая сессия) или ожидание снаружи. Строка, за которой
// никто не стоит, кружка не носит вовсе: пустая точка на каждой строке доски
// ела бы место и ничего не говорила. Слова состояния приходят подсказкой, а не
// текстом рядом: место в строке отдано заголовку задачи.
function rowDot(project, row) {
  const w = row.waiting;
  const live = row.run && row.run !== "gone";
  const own = row.run === "tmux" || row.run === "session";
  let kind = "";
  let tip = "";
  if (w && w.state) {
    // Ожидание человека читается раньше живости: пока агент ждёт ответа, ход
    // задачи стоит, и точка обязана звать человека, а не докладывать, что
    // сессия жива.
    kind = "sd-wait";
    const qs = w.questions || [];
    tip = w.state + ", источник: " + (w.note || "не назван") + "." +
      (qs.length ? " Вопрос: " + qs.join("; ") : "");
  } else if (live && own) {
    kind = "sd-run pulse";
    tip = "работает: живая сессия ведёт задачу";
  } else if (live) {
    kind = "sd-out";
    tip = "работа идёт снаружи: задачу ведёт чужая сессия";
  } else if (row.stage === STAGE_OUTSIDE) {
    kind = "sd-out";
    tip = "ждём снаружи: проверка, блокер или чужая работа";
  } else {
    return null;
  }
  const dot = withTip(el("span", "sdot " + kind), tip);
  dot.setAttribute("aria-label", tip);
  // С кружка идущей работы есть ход в разговор: со строки туда вёл чип
  // «работает», и вместе с ним дорога бы пропала.
  if (live) {
    dot.classList.add("clicky");
    dot.addEventListener("click", (ev) => {
      ev.stopPropagation();
      location.hash = boardChatHash(project, row.id);
    });
  }
  return dot;
}

// Этап ожидания не нас: словарь этапов держит его этим словом (internal/stage).
const STAGE_OUTSIDE = "снаружи";

// Состояние работы на форме задачи: те же слова и тот же вид чипа, что в табе
// сессий, потому что состояние одно на весь дашборд. Кто её ведёт (наша
// tmux-сессия, окно человека, поднятое мимо дашборда), стоит подсказкой: это
// про происхождение работы, а не про то, идёт ли она.
function liveChip(work) {
  if (!work) return null;
  const chip = workLiveChip(work, Date.now());
  if (!chip) return null;
  const who = work.via === "tmux" ? "сессия дашборда"
    : work.via === "session" ? "интерактивная сессия"
    : "сессии дашборда нет, работа поднята мимо него";
  return withTip(chip, who);
}

// Этап работы строки: вид деятельности словом и сколько он идёт. Запись кладут
// конвейер и taskctl (~/.devkit/runs), дашборд её только читает и приносит
// полями row.stage и row.stage_since.
function stageChip(row) {
  if (!row.stage) return null;
  const age = workAge(row.stage_since, Date.now());
  return el("span", "chip", age ? row.stage + ", " + age : row.stage);
}

// Обратный отсчёт до срока: те же слова, что у возраста работы, только вперёд.
// Возраст момента, отстоящего назад на остаток, и есть сам остаток, поэтому
// счёт тут один на оба направления.
function waitLeft(until, now) {
  if (!until) return "";
  const left = until - Math.floor(now / 1000);
  if (left <= 0) return "срок вышел";
  return workAge(Math.floor(now / 1000) - left, now);
}

// Подпись ожидания: состояние словом и обратный отсчёт до срока. Срок знает
// только первый источник, признак ожидания; у парковки и у повода из журнала
// его нет вовсе, и приписывать им отсчёт значило бы выдумывать знание.
function waitWords(w, now) {
  if (!w || !w.state) return "";
  const left = waitLeft(w.until, now);
  return left ? w.state + ", " + left : w.state;
}

// Чип ожидания человека: поле waiting строки доски (LLD DK-430, решение 4).
// Источник стоит в подсказке рядом с вопросом: «спросил агент» и «повод из
// журнала уведомителя» это разной точности знание, и на чипе видно, какое.
function waitChip(row) {
  const w = row.waiting;
  if (!w || !w.state) return null;
  const chip = el("span", "chip c-wait", waitWords(w, Date.now()));
  const qs = w.questions || [];
  const tip = "Ждут человека, источник: " + (w.note || "не назван") + "." +
    (qs.length ? " Вопрос: " + qs.join("; ") : "");
  return withTip(chip, tip);
}

function rowChips(project, row) {
  const chips = [];
  // Своего чипа у признака работы нет: идущую работу говорит кружок у номера,
  // а кончившуюся не говорит никто. Чип «сессии нет» отсюда снят, он занимал
  // место в каждой строке In progress и не звал ни к какому действию
  // (замечание пользователя).
  if (/^Цель:/.test(row.title)) chips.push(el("span", "chip c-goal", "цель"));
  if (row.type && row.type !== "task") chips.push(el("span", "chip", row.type));
  if (row.p === "P0" || row.p === "P1") chips.push(el("span", "chip c-p1", row.p));
  if (row.cost && row.cost !== "-") chips.push(el("span", "chip", row.cost));
  // Маркер [после ...] со строки доски назван теми же словами, что и блок
  // зависимостей: «после DK-248» требовало достроить, кто кого ждёт.
  // Держащая задача это дорога до неё, а не подпись: строка стоит в Blocked
  // ярусом «ждут задач», и первый же вопрос к ней «а что там с DK-311».
  // Держащих бывает несколько, и чип на каждую свой.
  for (const dep of row.after || []) {
    const chip = el("button", "chip clicky c-after", "после " + dep);
    chip.type = "button";
    chip.addEventListener("click", (ev) => {
      ev.stopPropagation();
      goKeepingChat(project + "/" + dep);
    });
    chips.push(withTip(chip, "строка ждёт задачу " + dep + ", нажатие открывает её"));
  }
  // Чипа ожидания в строке доски нет: то же самое состояние стоит кружком у
  // номера, а слова к нему приходят подсказкой. Два указателя на одно
  // состояние съедали строку, в которой и так тесно (замечание пользователя).
  // На экране задачи чип остаётся: там место есть, и читают его глазами, а не
  // наводят мышь.
  // Причина провала и причина блока это фразы человека, а не слово-метка: в
  // строке они режутся кромкой, а целиком приходят подсказкой. Нерезаный чип
  // уносил строку за край экрана, и раздел уезжал вбок горизонтальной
  // прокруткой (замечание пользователя про мобильный вид).
  if (row.fail) chips.push(withFull(el("span", "chip c-block cwhy", "провал: " + row.fail), row.fail));
  if (row.block) chips.push(withFull(el("span", "chip c-block cwhy", "блок: " + row.block), row.block));
  const check = checkChip(row);
  if (check) chips.push(check);
  return chips;
}

// Чип проверенной строки говорит человеку, ждут ли его: у видов mixed и user
// строка без него не закроется, у agent проверку закрывает прогон. Служебные
// детали (код слит, без выката, сам вид словом) уходят в подсказку: на строке
// они занимали место, отвечая не на тот вопрос (замечание пользователя).
function checkChip(row) {
  const notes = (row.notes || []).filter((n) => /^код слит/.test(n) || /^без выката/.test(n));
  if (row.sect !== "check" && !notes.length) return null;
  if (row.sect !== "check") return null;
  const mine = row.accept !== "agent";
  const chip = el("span", "chip " + (mine ? "c-wait" : "c-check"),
    mine ? "ждёт вашей приёмки" : "агент проверит сам");
  const bits = ["вид приёмки: " + (row.accept || "agent")].concat(notes);
  if (row.barrier) bits.push("барьер: " + row.barrier);
  return withTip(chip, bits.join(", ") + ".");
}

// Ранг в строке это одна сумма, а слагаемые приходят всплывающим блоком: пять
// показателей по RANKING.md именем и числом, под ними итог.
//
// Подсказок тут было две, и человек забраковал обе разом: «одна появляется
// непосредственно в строке сразу и наползает на контент следующей колонки, а
// вторая всплывающая, которая появляется через секунду». Первой была строка
// слагаемых, разворачивавшаяся прямо в ячейке; второй родная подсказка
// браузера, у которой своя задержка и свой вид. Осталась одна, своя: встаёт
// поверх строки, ничего в ней не двигает и показывается без выжидания.
//
// Ранг стоит и ячейкой строки доски, и припиской в выдаче поиска, а это две
// разные разметки: в таблице это td, в обычном списке span.
function rankCell(row, tag) {
  const parts = row.r_parts || [];
  const cell = el(tag || "span", "rank");
  const sum = el("button", "rsum", String(row.r));
  sum.type = "button";
  cell.append(sum);
  if (parts.length !== RANK_PARTS.length) return cell;
  // Имена показателей берутся из того же списка, каким ранг правят на экране
  // задачи: вторая копия имён разошлась бы с RANKING.md молча.
  const said = RANK_PARTS.map((one, at) => one.name + " " + parts[at]).join(", ");
  sum.setAttribute("aria-expanded", "false");
  sum.setAttribute("aria-label", "ранг " + row.r + ", слагаемые: " + said);
  const tip = el("div", "rtip");
  tip.setAttribute("role", "tooltip");
  const line = (name, num, cls) => {
    const one = el("div", "rtl" + (cls ? " " + cls : ""));
    one.append(el("span", "rtn", name), el("span", "rtv", String(num)));
    return one;
  };
  RANK_PARTS.forEach((one, at) => tip.append(line(one.name, parts[at])));
  tip.append(line("Ранг", row.r, "rtsum"));
  cell.append(tip);
  sum.addEventListener("click", (ev) => {
    // Нажатие держит блок открытым и не уводит внутрь задачи: на телефоне
    // наведения нет, и это единственный способ увидеть слагаемые.
    ev.stopPropagation();
    const on = cell.classList.toggle("on");
    sum.setAttribute("aria-expanded", on ? "true" : "false");
  });
  return cell;
}

// Действие зависит от статуса строки: из Backlog задачу выполняют, начатую
// продолжают, проверенную закрывают. Подпись кнопки идёт от той же секции, по
// которой сервер собирает промпт конвейеру, иначе кнопка обещала бы одно, а
// агент получал другое.
//
// Подписи тут короткие в одно слово: на телефоне кнопка стоит своей строкой, и
// «Проверить и закрыть» занимала её половину («кнопки слишком длинные»,
// замечание пользователя). Закрытие из подписи ушло, а не пропало: о нём
// говорит подсказка кнопки, и оно следствие проверки, а не второе действие.
const ACTION_BY_SECT = { "in-progress": "Продолжить", check: "Проверить" };

function actionLabel(sect) {
  return ACTION_BY_SECT[sect] || "Выполнить";
}

// Статус задачи русским словом: ключи секций приходят с доски
// (taskctl list --json), а на экране статус зовётся так, как о нём говорят.
// Словарь один на весь клиент: два перевода одного ключа разошлись бы врозь.
// Словарь состояний работы: одно слово на весь дашборд. Прежде одно и то же
// состояние звалось в трёх местах по-разному («работает» в табе сессий,
// «tmux-сессия активна» на форме задачи, «активна» в снимке tmux), и человек
// читал их как разные вещи (замечание пользователя). Машинные состояния
// приезжают полем live, а переводит их только этот словарь.
const LIVE_WORD = {
  busy: "активна",
  waiting: "ждёт ответа",
  idle: "простаивает",
  dead: "сессии нет",
};

const SECT_WORD = {
  backlog: "в очереди",
  "in-progress": "в работе",
  check: "на проверке",
  blocked: "заблокирована",
};

// Подписки машины: список приезжает ручкой /api/harnesses, а собирает его
// agentctl. Имён харнесов в клиенте нет ни одного, как и в сервере: третья
// подписка появится в списке сама, стоит ей включиться на машине.
let harnessView = null;

// Список читается на каждой сборке экрана, а не один раз на загрузку: своего
// состояния клиент между обновлениями не держит нигде, и включённая на машине
// подписка обязана появиться в кнопке без перезагрузки страницы. Стоит это
// одного запроса, подпроцесс за ним держит память процесса на стороне сервера.
// Ждёт его экран только в первый раз, когда показать всё равно нечего: дальше
// список перечитывается в стороне, а кнопка собирается тем, что было верно
// мгновение назад. Ожидание на каждом переходе стоило человеку круга по сети
// перед формой задачи (замер poc_bench_task).
async function loadHarnesses() {
  const going = api("/api/harnesses").then((r) => {
    harnessView = r.ok ? r.body
      : { harnesses: [], note: r.body.error || "список подписок не прочитан (" + r.status + ")" };
  });
  if (harnessView) {
    going.catch(console.error);
    return;
  }
  await going;
}

function harnesses() {
  return (harnessView && harnessView.harnesses) || [];
}

// Подписка по умолчанию: на неё идёт широкая часть кнопки. Признак ставит
// машинный слой, а без признака берётся первая в списке, чтобы кнопка работала
// и на полураскрытом конфиге.
// TIER_VERDICT это не ярус, а отказ от выбора: назначает его тогда вердикт
// agentctl pick на стороне сервера, как и велит правило доски. Стоит он первым
// у запуска задачи и выбран там по умолчанию; у цели и у разбора черновика
// вердикта нет, там по умолчанию pro.
const TIER_VERDICT = "вердикт";

// RUN_TIER это ярус по умолчанию для работ, которые заказывает сам дашборд.
// Разбор черновика это работа среднего веса: верхний ярус ей не нужен, а
// дефолт клиента бывает как раз верхним (замечание пользователя).
const RUN_TIER = "pro";

// Ярусы раскладки машины. Лестница у подписок одна и та же (mini, base, pro,
// max), а модель под ярусом своя, поэтому список собирается с подписки по
// умолчанию, а недостающее добирается у соседей: короткая лестница одной
// подписки не должна отнимать выбор у остальных.
function harnessTiers() {
  const out = [];
  const list = harnesses();
  const own = list.find((h) => h.default);
  for (const h of own ? [own].concat(list) : list) {
    for (const m of h.models || []) {
      if (m.tier && !out.includes(m.tier)) out.push(m.tier);
    }
  }
  return out;
}

function harnessDefault() {
  const list = harnesses();
  return ((list.find((h) => h.default) || list[0] || {}).name) || "";
}

// Куда поедет работа и есть ли из чего выбирать. Молчания тут нет ни в одном
// случае: и подписка на машине одна, и непрочитанный список, и живой выбор
// названы словами, потому что нажимают кнопку не глядя в список (макет «11
// Подписка при запуске», фрейм состояний).
function harnessWhy() {
  const list = harnesses();
  if (!list.length) {
    return (harnessView && harnessView.note) || "список подписок не прочитан: запуск идёт как раньше";
  }
  if (list.length === 1) return "поедет на " + list[0].name + ", подписка на машине одна";
  return "поедет на " + harnessDefault() + ", подписок на машине " + numWord(list.length);
}

// Число подписок словом: «подписок на машине две» читается как речь, а цифра в
// подписи рядом с кнопкой смотрится счётчиком.
const NUM_WORDS = ["ноль", "одна", "две", "три", "четыре", "пять"];

function numWord(n) {
  return NUM_WORDS[n] || String(n);
}

// Строка списка подписок: одна полоса на подписку, и на ней имя с остатком
// квоты двумя числами. Прежняя строка везла имя, чип «по умолчанию», две
// полоски-градусника с датами сброса и возраст снимка, то есть четыре яруса на
// каждую подписку: «блок выбора подписки в меню слишком жирный, дублирует блок
// на главной странице» (замечание пользователя). Блок квоты в колонке никуда
// не делся, и полное состояние читают там; тут выбирают, чем запустить, и для
// этого хватает двух процентов.
//
// pin это подписка, на которой пойдёт запуск от главной кнопки. Подсвечена в
// списке именно она, а не машинное умолчание: подсветка отвечает на вопрос
// «что будет, если не выбирать», и у задачи Check ответ свой (её вели своей
// подпиской). Всё, что ушло со строки (даты сброса, возраст снимка, признак
// умолчания), осталось подсказкой по наведению: место оно не занимает.
function harnessRow(h, pin) {
  const own = pin ? h.name === pin : Boolean(h.default);
  const row = el("button", "hrow" + (own ? " on" : ""));
  row.type = "button";
  row.append(el("b", "hname", h.name));
  const snap = quotaEvery(quotaView).find((q) => q.name === h.name) || null;
  const buckets = snap ? (snap.buckets || []) : [];
  const said = [h.name + (h.default ? ", подписка по умолчанию" : "")];
  for (const b of buckets.slice(0, 2)) {
    const one = el("span", "hq");
    // Короткое имя бакета стоит у числа: без него два процента подряд не
    // говорят, какой из них про общий лимит, а какой про окно.
    one.append(el("em", "", bucketWord(b.name)));
    one.append(el("b", "", b.used_pct + "%"));
    row.append(one);
    const when = quotaWhen(b.reset);
    said.push(bucketWord(b.name) + " " + b.used_pct + "%" +
      (when ? (b.expired ? ", окно сбросилось " : ", сброс ") + when : ""));
  }
  // Остатка нет вовсе: молчание тут неотличимо от нуля, и причина названа
  // словами прямо в строке.
  if (!buckets.length) {
    // Пометка неизвестного остатка остаётся: нарисованный ноль читался бы как
    // «квота цела», а её тут просто не снимали.
    row.append(el("span", "hq hnote stale", (snap && snap.note) || "снимка квоты нет"));
  } else if (snap && snap.age) {
    said.push("снимок " + snap.age + " назад");
  } else if (snap && snap.stale) {
    said.push(snap.note || "возраст снимка неизвестен");
  }
  withTip(row, said.join("; "));
  return row;
}

// Кнопка запуска с выбором подписки (макет «Кнопка запуска», LLD DK-328,
// решение 3). Широкая часть поднимает работу на подписке по умолчанию, то есть
// частый путь не подорожал ни на одно нажатие; узкая открывает список, и
// нажатие на строку списка запускает работу на ней же, без второго нажатия.
// Выбор действует на один запуск, а не на экран: две работы рядом идут на
// разных подписках, и переключателя-настройки для этого не нужно.
// tip называет заказ дословно (orderHint), afterOk это экран, куда ведёт
// удачный запуск: пусто на строке списка, где человек нарочно остаётся на
// доске (DK-316), и заполнено на экране задачи, откуда до DK-286 нажатие
// вело в никуда.
// Строка Check закрывается одной кнопкой и без выбора подписки: нажатие
// человека на строке с его приёмкой это и есть его согласие, а подписка у
// задачи уже своя, та, на которой её начинали. Кнопка называет её подсказкой,
// чтобы человек знал, чем пойдёт закрытие (решение пользователя).
function checkPin(row) {
  if (!row || row.sect !== "check") return "";
  return row.harness || harnessDefault() || "";
}

function checkTip(row) {
  const pin = checkPin(row);
  const how = pin ? "Закрытие пойдёт подпиской " + pin +
    (row.harness ? " (на ней задачу и вели)." : " (подписка по умолчанию: чем вели задачу, машина не помнит).")
    : "";
  const who = row.accept === "agent" ? "Проверку прогонит агент и закроет строку."
    : "Нажатие это ваша приёмка: дальше агент прогонит проверку и закроет строку.";
  return how ? who + " " + how : who;
}

// Тело выбора запуска: строки подписок, полоса ярусов и подвал под ними. Живёт
// оно в двух местах, во всплывашке составной кнопки и в меню строки доски, и
// собрано одной функцией нарочно: разъехавшись, эти два выбора отвечали бы на
// один вопрос по-разному.
//
// into это коробка, куда всё кладётся, fire зовётся именем выбранной подписки,
// а tell выбранным ярусом: ярус живёт у того, кто кнопку собрал, потому что
// запускает работу и широкая половина кнопки, мимо этого списка.
function runPickBody(into, opts) {
  const list = opts.list || [];
  const pickHarness = Boolean(opts.pickHarness);
  const tierList = opts.tiers || [];
  let tier = opts.tier || "";
  if (pickHarness) {
    into.append(el("span", "hph", "На какой подписке запустить"));
    for (const h of list) {
      const row = harnessRow(h, opts.pin || "");
      row.addEventListener("click", (ev) => {
        ev.stopPropagation();
        opts.fire(h.name);
      });
      into.append(row);
    }
  }
  // Полоса ярусов: выбор тут не запускает работу, а меняет вес модели, которым
  // она поедет. Запускают её потом широкой половиной либо строкой подписки, и
  // список от выбора яруса не закрывается: человек выбирает два ответа подряд.
  //
  // Подпись полосы зовётся «Уровень модели», а не прежним вопросом про ярус:
  // слово это живёт в правилах доски, а в самой полосе на него отвечают
  // именами моделей, и человек читал вопрос дважды (замечание пользователя).
  // Подвала под полосой нет вовсе: он объяснял, откуда список и надолго ли
  // выбор, и был забракован прямой оценкой пользователя.
  if (tierList.length > 1) {
    into.append(el("span", "hph", "Уровень модели"));
    const bar = el("div", "tbar");
    const marks = [];
    for (const name of tierList) {
      const btn = el("button", "tpick" + (name === tier ? " on" : ""), name);
      btn.type = "button";
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        tier = name;
        opts.tell(name);
        for (const m of marks) m.classList.toggle("on", m === btn);
      });
      marks.push(btn);
      bar.append(btn);
    }
    into.append(bar);
  }
}
// run это способ поднять работу выбранной подпиской. По умолчанию это конвейер
// задачи, а груминг черновика поднимает себя сам: выбор подписки у него тот же,
// потому что разбор это такая же работа агента (замечание пользователя).
function runControl(project, id, make, label, isGoal, tip, afterOk, pinned, run, tiers) {
  // isGoal тут остаётся ради подписи, а не ради запрета: выбор подписки у цели
  // такой же, как у задачи, и разводить их поведением больше незачем.
  const wide = make(label);
  if (tip) withTip(wide, tip);
  // Кнопка гаснет до ответа: пока запуск идёт, строка выглядит прежней, и
  // второе нажатие уходило вторым запуском, а возвращалось отказом «работа уже
  // идёт».
  // Ярус выбирается там же, где подписка: подписка это контур и квота, а ярус
  // вес модели. Разбор черновика ходил дефолтом самого клиента, то есть верхним
  // ярусом, которого никто не выбирал (замечание пользователя), и теперь ярус
  // назван: pro по умолчанию, другой берут осознанно.
  // tiers это либо список ярусов, либо пара «список и выбранный по умолчанию»:
  // у запуска задачи умолчание это вердикт, у цели и разбора pro.
  const tierList = (tiers && tiers.list) || tiers || [];
  let tier = (tiers && tiers.now) || (tierList.includes(RUN_TIER) ? RUN_TIER : (tierList[0] || ""));
  // Наружу вердикт едет пустым полем: имени такого яруса в раскладке нет, его
  // называет сервер.
  const tierOut = () => (tier === TIER_VERDICT ? "" : tier);
  const fire = (node, harness) => {
    node.disabled = true;
    const going = run ? run(harness, tierOut()) : startRun(project, id, harness, afterOk, tierOut());
    Promise.resolve(going).catch(console.error).finally(() => { node.disabled = false; });
  };
  wide.addEventListener("click", (ev) => {
    ev.stopPropagation();
    fire(wide, pinned || harnessDefault());
  });
  const list = harnesses();
  // Выбирать есть что, когда подписок на машине больше одной либо когда у
  // кнопки свой выбор яруса: с одной подпиской и ярусом список отвечал бы на
  // вопрос, которого никто не задавал.
  //
  // Прикреплённая подписка выбора больше не снимает, она им лишь правит
  // умолчание. Прежде строка Check показывала один ярус, а строка в работе ярус
  // с подписками, и человек видел два разных списка на один вопрос («выбор
  // яруса и подписки различается по секциям», замечание пользователя). Теперь
  // список один везде, а прикреплённая подписка стоит в нём подсвеченной: на
  // неё и уйдёт запуск, если не выбирать.
  const pickHarness = list.length >= 2;
  if (!pickHarness && tierList.length < 2) {
    const why = pinned ? "" : harnessWhy();
    if (why) withTip(wide, tip ? tip + " " + why : why);
    // Кнопка без стрелки выбора тоже стоит в пустом span, а не голой: составная
    // кнопка ниже держит span.split той же глубины, и rowAction для Стопа
    // (DK-349) обёрнут так же. Разная глубина между вырожденным Run и Стоп на
    // одной строке промахивала позиционный focusSnap/focusBack мимо кнопки
    // (app.js:82-113, DK-316).
    const solo = el("span");
    solo.append(wide);
    return solo;
  }
  const grp = el("span", "split");
  grp.append(wide);
  const pop = el("div", "hpop");
  pop.hidden = true;
  // Список подписок стоит на общем учёте всплывашек: закрывают его те же три
  // пути, что и список кольца.
  let held = null;
  const shut = () => {
    pop.hidden = true;
    more.setAttribute("aria-expanded", "false");
    held = null;
  };
  runPickBody(pop, {
    list,
    pickHarness,
    pin: pinned || harnessDefault(),
    tiers: tierList,
    tier,
    fire: (name) => {
      popupDrop(held);
      shut();
      fire(wide, name);
    },
    tell: (name) => { tier = name; },
  });
  const more = el("button", wide.className + " more2");
  more.append(el("span", "car"));
  more.setAttribute("aria-label", "Выбрать подписку");
  more.setAttribute("aria-expanded", "false");
  withTip(more, "Выбрать подписку: " + list.map((h) => h.name).join(", "));
  more.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (!pop.hidden) {
      popupDrop(held);
      shut();
      return;
    }
    popupsShut(null);
    pop.hidden = false;
    more.setAttribute("aria-expanded", "true");
    held = popupHold(pop, shut);
    // Вверх список раскрывается там, где под кнопкой не хватает места: на
    // телефоне кнопка стоит низко, и раскрытый вниз список уезжает под нижние
    // вкладки. Считается это по месту кнопки на экране, а не по ширине окна:
    // низко она стоит и на ноутбуке, если доска прокручена до конца.
    if (!pop.hidden) {
      pop.classList.toggle("up", noRoomBelow(more));
      // Горизонталь считается числом: у списка, который шире места и слева от
      // кнопки, и справа, верной стороны нет вовсе.
      popFit(more, pop);
    }
  });
  grp.append(more, pop);
  return grp;
}

// Хватает ли под узлом места на раскрытый список. Мерить нечем (стенд, старый
// браузер), значит раскрываем вниз, как было: догадка тут хуже прежнего
// поведения.
function noRoomBelow(node) {
  if (!node.getBoundingClientRect || !window.innerHeight) return false;
  const box = node.getBoundingClientRect();
  if (!box || !box.bottom) return false;
  return box.bottom + HPOP_ROOM > window.innerHeight;
}

// Сколько места просит раскрытый список: две шапки, строки подписок в одну
// полосу и полоса ярусов. Точная высота известна только после вставки, а
// решение о стороне принимается до неё, поэтому тут запас по макету.
const HPOP_ROOM = 200;

// Горизонталь раскрытого списка. Место ему называется числом, а не выбором
// стороны: сторона отвечала на вопрос «влево от кнопки или вправо», а у
// телефона верного ответа среди двух нет вовсе, потому что список шире свободного
// места с обеих сторон. Тут список ставится туда, где висел бы всегда (правым
// краем на кнопке), а потом задвигается в границы главной части экрана.
//
// Ширина спрашивается у самого списка, а не берётся из стилей: на телефоне она
// своя, и второе объявление её в коде разошлось бы с первым молча. Мерить
// нечем (стенд, старый браузер), значит список остаётся там, куда его поставил
// стиль: догадка тут хуже.
function popFit(node, pop) {
  if (!pop || !pop.style) return;
  pop.style.left = "";
  pop.style.right = "";
  if (!node.getBoundingClientRect || !pop.getBoundingClientRect) return;
  const host = pop.parentNode && pop.parentNode.getBoundingClientRect
    ? pop.parentNode.getBoundingClientRect() : null;
  const box = node.getBoundingClientRect();
  const list = pop.getBoundingClientRect();
  const width = (list && list.width) || 0;
  if (!host || !box || !width) return;
  const edgeL = screenLeft() + HPOP_EDGE;
  const edgeR = screenRight() - HPOP_EDGE;
  let want = box.right - width;
  if (want + width > edgeR) want = edgeR - width;
  if (want < edgeL) want = edgeL;
  pop.style.left = Math.round(want - host.left) + "px";
  // Правый край снимается вместе с постановкой левого: в стилях список висит
  // на правом крае кнопки, и оба края разом растянули бы его во всю ширину.
  pop.style.right = "auto";
}

// Правый край места: у главной части экрана он совпадает с краем окна, и
// мерить надо именно окно, а не документ, который бывает шире от прокрутки.
function screenRight() {
  const doc = document.documentElement;
  return (doc && doc.clientWidth) || window.innerWidth || 0;
}

// Левый край места, отведённого всплывашке: режет её главная часть экрана, а
// не окно, и мерить надо её. Нечем мерить, значит краем считается край окна.
function screenLeft() {
  const box = document.getElementById("groups");
  const rect = box && box.getBoundingClientRect ? box.getBoundingClientRect() : null;
  return (rect && rect.left) || 0;
}

// Зазор от края: список, прижатый вплотную к границе, читается как обрезанный.
const HPOP_EDGE = 8;

// Пункт меню строки: та же кнопка, что и в меню плюса, только со своим
// действием. Кнопкой, а не подписью, потому что жмут её и пальцем, и с
// клавиатуры, и читалка экрана обязана назвать её кнопкой.
function menuRow(label, tip, go) {
  const opt = el("button", "pmrow", label);
  opt.type = "button";
  if (tip) withTip(opt, tip);
  opt.addEventListener("click", (ev) => {
    ev.stopPropagation();
    go(opt);
  });
  return opt;
}

// Есть ли за строкой разговор. От этого зависит, чем работает главная кнопка
// строки: у задачи с разговором это вход в чат, у нетронутой очереди запуск.
// Спрашивается это у самой строки (row.run): работа за ней идёт, шла нашей
// сессией или кончилась вместе с ней, значит чату есть что показать. У строки
// без единой сессии кнопка чата открывала бы пустой разговор, а нужен ей
// запуск, и он самое частое действие очереди (решение пользователя).
function rowTalks(row) {
  return Boolean(row && row.run);
}

// Действие прямо со строки: поднять конвейер, снять живую сессию или зайти в
// разговор, не открывая задачу. Ручки те же, что у экрана задачи (POST и DELETE
// runs), и ответ выходит в ту же строку результата.
//
// Место кнопки не зависит ни от чего: слева кнопка работы, справа кнопка
// разговора, дальше три точки. Прежде главную кнопку выбирало состояние строки
// (есть за ней запись работы, значит чат, нет, значит запуск), и в одной секции
// In progress у одних строк стоял чат, у других запуск: «логика главной кнопки
// непонятна» (замечание пользователя). Признак этот к тому же невидим, запись
// работы на строке ничем не подписана, и угадать по строке, что даст нажатие,
// было нельзя.
//
// Кнопка работы отвечает за одно: двинуть задачу дальше. Что именно она
// сделает, видно по ней самой, а не по скрытому признаку:
//   красный «Стоп», когда работа идёт нашей tmux-сессией;
//   жёлтый пуск с подписью секции («Выполнить», «Продолжить», «Проверить и
//   закрыть») во всех прочих случаях.
// Разговор за строкой уже есть, значит пуск не заводит второго исполнителя, а
// поднимает ход в том же разговоре: сервер разбудит живую сессию или поднимет
// резюм (жалоба на DK-460). Разговора нет, значит пуск заказывает работу
// конвейером, и подписку с уровнем модели для неё выбирают под тремя точками.
//
// Кнопка разговора стоит у всякой строки, чем бы та ни была занята: чат к
// конвейеру не привязан, и правило от этого объясняется одной фразой, «чат есть
// у каждой задачи». Прежде она то стояла главной, то пряталась в меню, и один и
// тот же пункт «Чат по задаче» открывал ровно тот же разговор, что и кнопка.

// Идёт ли по строке ход прямо сейчас. Ход идёт и тогда, когда агент стоит с
// вопросом к человеку: сессия жива, ответ двинет её дальше, и снимать её
// человеку есть за что.
function rowOnRun(row) {
  return Boolean(row && (row.run_busy || (row.waiting && row.waiting.state)));
}

// Работа идёт нашей tmux-сессией, и снять её есть чем: только у такой строки
// кнопка работы это красный «Стоп», во всех прочих случаях там жёлтый пуск с
// подписью действия секции. Правило одно на строку доски и на полосу действий
// задачи, и живёт оно тут, а не двумя списками условий в двух местах.
//
// Разошлись эти два места на живом случае DK-543: на форме задачи кнопку
// выбирало существование tmux-сессии, и у простаивающего разговора (сессия
// жива, последняя реплика была две минуты назад) стоял «Стоп», хотя снимать
// было нечего, а строке нужен был пуск (замечание пользователя).
function rowOurRun(row) {
  return Boolean(row) && (row.run === "tmux" || row.run === "chat") && rowOnRun(row);
}

// Что сделает «Стоп» у этой строки. Работу конвейера снимают целиком: сессия у
// него служебная, возобновление это новый запуск, и состояние он прочтёт с
// диска. Работу, идущую в окне разговора, так снимать нельзя, у неё память
// человека: там прерывают ход, кладут конец работы в реестр и оставляют
// разговор жить (DK-716). Кнопка обязана говорить это до нажатия: два разных
// исхода под одним значком человек различает только подсказкой.
function stopTip(row) {
  // Стоп уже нажат и дожимается: ход прерван, а субагенты, которым агент раздал
  // работу фоном, дописывают своё. Кнопка тут остаётся на месте нарочно, работа
  // ведь идёт, и подсказка обязана сказать, что второго нажатия не нужно.
  if (row && row.run_stopping) {
    return "Стоп нажат: ход прерван, а фоновые субагенты ещё дорабатывают. "
      + "Всякий их ход прервётся тем же стопом, и работа по задаче снимется, когда они встанут.";
  }
  if (row && row.run === "chat") {
    return "Стоп: текущий ход агента прервётся, работа по задаче снимется, "
      + "а разговор останется жить и следующую реплику возьмёт.";
  }
  return "Стоп: " + STOP_TIP;
}

// Выбор рабочей сессии для стопа. По строке работает не одна, и сервер вместо
// остановки первой попавшейся отвечает списком: какая из них «та самая», знает
// только человек, а прерванный чужой ход стоит потерянной работы. Всплывашка
// та же, что у выбора подписки, и закрывается она теми же тремя путями.
function stopPickShow(box, btn, project, row, r) {
  const list = (r && r.body && Array.isArray(r.body.sessions)) ? r.body.sessions : [];
  if (!r || r.ok || list.length < 2) return;
  // Прошлый выбор снимается: второе нажатие иначе вешало бы вторую всплывашку
  // поверх первой.
  for (const gone of (box.children || []).slice()) {
    if (String(gone.className || "").includes("spick")) gone.remove();
  }
  const menu = el("div", "pmenu rmenu spick");
  let held = null;
  const shut = () => { menu.hidden = true; held = null; menu.remove(); };
  for (const one of list) {
    const when = one.moved ? whenAgo({ at: new Date(one.moved * 1000), exact: true }, Date.now()) : "";
    const what = one.live === "waiting" ? "ждёт ответа" : "идёт ход";
    const label = one.tmux || one.session;
    menu.append(menuRow(label, [one.title, what, when].filter(Boolean).join(", "), () => {
      popupDrop(held);
      shut();
      btn.disabled = true;
      stopRun(project, row.id, one.session)
        .catch(console.error).finally(() => { btn.disabled = false; });
    }));
  }
  box.append(menu);
  popupsShut(null);
  menu.classList.toggle("up", noRoomBelow(btn));
  held = popupHold(menu, shut);
  sayResult(r.body.error || "", false);
}

function rowAction(project, row, sect) {
  const grp = el("span", "racts");
  // Работа наша и идёт нашей tmux-сессией: её снимают со строки. Спрашивается
  // это общим правилом (rowOurRun): полоса действий задачи выбирает кнопку тем
  // же вопросом, и разойтись им теперь негде.
  const ours = rowOurRun(row);
  // Ход идёт, но сессия не наша: снимать нечего, а продолжение уехало бы в
  // живой ход посторонней сессии. Кнопка тут стоит погашенной с причиной.
  const busy = !ours && Boolean(row.run_busy);
  const talks = rowTalks(row);
  // Выбор подписки и уровня собирается до кнопки: лежит он под тремя точками,
  // а само нажатие на кнопку запуска идёт умолчанием.
  let pick = null;
  // Причина, по которой у строки выбирать нечего. Три точки стоят у всякой
  // строки, и погашенные они обязаны сказать, почему погашены: молчащая кнопка
  // неотличима от сломанной.
  let pickWhy = "";
  // Закрывалка меню объявлена раньше самого меню: зовёт её и строка подписки
  // внутри него, а собирается меню последним, когда известен весь состав.
  let shutMenu = () => {};
  let main;
  if (ours) {
    main = el("button", "btn btn-sm btn-danger btn-ico rstop");
    main.append(icon("i-stop"));
    main.setAttribute("aria-label", "Стоп");
    withTip(main, stopTip(row));
    pickWhy = "по строке идёт ход нашей сессией, и подписку ему на ходу не сменить";
    main.addEventListener("click", (ev) => {
      ev.stopPropagation();
      // Кнопка гаснет до ответа: пока стоп идёт, строка выглядит прежней, и
      // второе нажатие уходило вторым запросом.
      main.disabled = true;
      stopRun(project, row.id)
        .then((r) => { stopPickShow(grp, main, project, row, r); })
        .catch(console.error).finally(() => { main.disabled = false; });
    });
  } else if (busy) {
    const label = actionLabel(sect);
    main = el("button", "btn btn-sm btn-ico rmain");
    main.append(icon("i-play"));
    main.disabled = true;
    main.setAttribute("aria-label", label);
    withTip(main, label + ": по строке идёт ход, и вводная продолжения уехала бы в живую "
      + "сессию посреди него. Сессия эта не наша, и снять её отсюда нечем.");
    pickWhy = "по строке идёт чужой ход";
  } else if (talks) {
    const label = actionLabel(sect);
    main = el("button", "btn btn-sm btn-acc btn-ico rmain");
    main.append(icon("i-play"));
    main.setAttribute("aria-label", label);
    withTip(main, label + ": сервер поднимет ход в том же разговоре, разбудив живую "
      + "сессию или подняв резюм. Второго исполнителя конвейером тут не заводят.");
    pickWhy = "продолжение идёт в том же разговоре, и подписку ему выбрал первый заход";
    main.addEventListener("click", (ev) => {
      ev.stopPropagation();
      main.disabled = true;
      continueTask(project, row.id).catch(console.error).finally(() => { main.disabled = false; });
    });
  } else {
    const label = actionLabel(sect);
    if (row.after && row.after.length) {
      // Заблокированную маркером задачу конвейер брать не должен, и кнопка
      // говорит это сама: погашенная с причиной понятнее исчезнувшей.
      main = el("button", "btn btn-sm btn-ico rmain");
      main.append(icon("i-play"));
      main.disabled = true;
      main.setAttribute("aria-label", label);
      withTip(main, label + ": сначала " + row.after.join(", "));
      pickWhy = "сначала " + row.after.join(", ");
    } else {
      // Строка списка остаётся на доске и после нажатия (DK-316): экран не
      // уезжает из-под пальца, и afterOk тут не передаётся. Заказ дословно всё
      // равно виден по наведению.
      const pin = checkPin(Object.assign({ sect: sect }, row));
      const hint = pin ? checkTip(Object.assign({ sect: sect }, row))
        : orderHint(row.order, row.accept, sect, row.id);
      // Уровень модели выбирается там же, где подписка. У задачи по умолчанию
      // стоит вердикт: назначает исполнителя и ярус agentctl pick, а не глаз
      // диспетчера, и человек его лишь переопределяет. У цели вердикта на весь
      // цикл нет, там умолчание pro.
      const goal = /^Цель:/.test(row.title);
      const tierList = goal ? harnessTiers() : [TIER_VERDICT].concat(harnessTiers());
      let tier = goal ? RUN_TIER : TIER_VERDICT;
      // Наружу вердикт едет пустым полем: имени такого яруса в раскладке нет,
      // его называет сервер.
      const tierOut = () => (tier === TIER_VERDICT ? "" : tier);
      const list = harnesses();
      // Прикреплённая подписка (строка Check идёт той, которой вели задачу)
      // выбора не снимает: она правит умолчание, а список тот же, что у строки
      // в работе.
      const pickHarness = list.length >= 2;
      main = el("button", "btn btn-sm btn-acc btn-ico rmain");
      main.append(icon("i-play"));
      main.setAttribute("aria-label", label);
      // Причина, по которой выбирать не из чего, стоит в подсказке самой
      // кнопки: списка подписок у такой строки нет вовсе, и сказать об этом
      // больше негде.
      const why = pickHarness ? "" : harnessWhy();
      // Дорога к выбору названа тут же: сам запуск идёт умолчанием, а подписку
      // с уровнем выбирают под тремя точками рядом.
      const more = (pickHarness || tierList.length > 1)
        ? "Подписку и уровень модели выбирают под тремя точками."
        : "";
      withTip(main, [label + ".", hint, why, more].filter(Boolean).join(" "));
      const fire = (harness) => {
        main.disabled = true;
        startRun(project, row.id, harness, "", tierOut())
          .catch(console.error).finally(() => { main.disabled = false; });
      };
      main.addEventListener("click", (ev) => {
        ev.stopPropagation();
        fire(pin || harnessDefault());
      });
      // Выбирать есть что, когда подписок на машине больше одной либо когда у
      // запуска свой выбор уровня модели: с одной подпиской и одним уровнем
      // список отвечал бы на вопрос, которого никто не задавал.
      if (pickHarness || tierList.length > 1) {
        const box = el("span", "rpick");
        runPickBody(box, {
          list,
          pickHarness,
          pin: pin || harnessDefault(),
          tiers: tierList,
          tier,
          fire: (name) => { shutMenu(); fire(name); },
          tell: (name) => { tier = name; },
        });
        pick = box;
      } else {
        pickWhy = harnessWhy();
      }
    }
  }
  grp.append(main);
  grp.append(rowChatBtn(project, row));
  // Три точки стоят у всякой строки третьей кнопкой. Прошлый заход снял их
  // вовсе, а выбор подписки увёл под правую кнопку мыши и долгое нажатие:
  // «выбор подписки правой кнопкой плохое решение, ты даже не видишь, что
  // такой функционал есть, можешь только догадаться, плюс в мобильном виде это
  // вообще не работает» (замечание пользователя). Обещает выбор теперь сама
  // кнопка, а правая кнопка мыши осталась вторым входом для тех, кто к ней
  // привык.
  //
  // Стоят они и там, где выбирать нечего, только погашенными: прежде их ставил
  // состав меню, и у одной строки точки были, а у соседней нет (замечание
  // пользователя). Причина, по которой кнопка погашена, лежит в её подсказке
  // ровно так же, как у погашенного запуска.
  const dots = el("button", "btn btn-sm btn-ico rdots");
  dots.append(icon("i-dots"));
  dots.setAttribute("aria-label", "Подписка и уровень модели");
  grp.append(dots);
  if (!pick) {
    dots.disabled = true;
    withTip(dots, "Подписка и уровень модели: " + (pickWhy || "выбирать нечего"));
    return grp;
  }
  let held = null;
  // Меню строки закрывается теми же тремя путями, что и остальные всплывашки
  // дашборда: повторным нажатием по своей кнопке, кликом мимо и Escape.
  const menu = el("div", "pmenu rmenu");
  menu.hidden = true;
  menu.append(pick);
  shutMenu = () => {
    menu.hidden = true;
    dots.setAttribute("aria-expanded", "false");
    held = null;
  };
  dots.setAttribute("aria-expanded", "false");
  withTip(dots, "Подписка и уровень модели для запуска");
  const openPick = () => {
    if (!menu.hidden) {
      popupDrop(held);
      shutMenu();
      return;
    }
    // Соседняя всплывашка уходит с открытием этой: два раскрытых списка разом
    // экран не показывает ни в одном месте дашборда.
    popupsShut(null);
    menu.hidden = false;
    dots.setAttribute("aria-expanded", "true");
    held = popupHold(menu, shutMenu);
    // Вверх меню раскрывается там, где под кнопкой не хватает места: строка
    // стоит низко, и раскрытое вниз оно уезжает под нижние вкладки.
    menu.classList.toggle("up", noRoomBelow(dots));
  };
  dots.addEventListener("click", (ev) => {
    ev.stopPropagation();
    openPick();
  });
  // Правая кнопка мыши на самом запуске: второй вход к тому же выбору. Сам по
  // себе он не виден, и единственным входом ему быть нельзя, а рядом с
  // видимыми тремя точками он никому не мешает.
  main.addEventListener("contextmenu", (ev) => {
    if (ev.preventDefault) ev.preventDefault();
    if (ev.stopPropagation) ev.stopPropagation();
    openPick();
  });
  grp.append(menu);
  return grp;
}

// opts.quiet это тихая подача строки, ждущей чужой задачи: она стоит в Blocked
// нижним ярусом и не должна спорить с парковками, у которых человека и правда
// ждут.
function renderRow(project, row, sect, opts) {
  const quiet = Boolean(opts && opts.quiet);
  const tr = freshMark(el("tr", "trow" + (quiet ? " rwait" : "")), row.id);
  // Кружок состояния живёт внутри ячейки номера, а не отдельной колонкой: у
  // колонки была бы своя подпись в шапке, а сказать о ней нечего, кружок и так
  // читается рядом с номером.
  const idc = el("td", "id");
  const dot = rowDot(project, row);
  if (dot) idc.append(dot);
  idc.append(el("span", "", row.id));
  tr.append(idc);
  const { cell: ttc, box: tt } = tblCell("tt");
  tt.append(withFull(el("span", "ttl", row.title), row.title));
  // Чипы лежат своей коробкой, а не россыпью рядом с заголовком: на телефоне
  // они уходят под него отдельной строкой, и заголовку достаётся вся ширина.
  // Рядом с заголовком они ширины не отдавали, и от длинного названия
  // оставался столбик обрубков.
  const chips = rowChips(project, row);
  if (chips.length) {
    const box = el("span", "rchips");
    for (const chip of chips) box.append(chip);
    tt.append(box);
  }
  tr.append(ttc);
  // Ранг и дата стоят своими колонками, а не приписками в хвосте: по ним
  // сортирует шапка, и колонка, которой нет в таблице, подписи в шапке не
  // соответствует ничем.
  tr.append(rankCell(row, "td"));
  // Дата последней правки вместо возраста днями: считает её taskctl по git
  // blame, клиент только показывает. Слова «правка» рядом с датой нет,
  // объяснение пришло подсказкой по наведению.
  const when = el("td", "twhen");
  if (row.moved) {
    when.append(withTip(el("span", "stale dashed", row.moved),
      whenTip(row.moved)));
  }
  tr.append(when);
  const { cell: metac, box: meta } = tblCell("meta");
  meta.append(rowAction(project, row, sect));
  tr.append(metac);
  tr.addEventListener("click", () => {
    // После броска строки браузер шлёт клик тем же нажатием: перетаскивание не
    // уводит внутрь задачи.
    if (dragAteClick()) return;
    goKeepingChat(project + "/" + row.id);
  });
  // Двигается перетаскиванием только очередь: в остальных секциях порядок
  // ручной и от ранга не зависит, там жест обещал бы то, чего не делает.
  // Перетаскивание живёт в самой очереди: ждущая задач строка нарисована в
  // Blocked, и жест там обещал бы место в списке, которого она не занимает.
  // Своим порядком очередь стоит только тогда, когда её не переставила шапка:
  // в списке по чужой колонке место строки ничего не обещает, и жест двигал бы
  // ценность вслепую.
  if (sect === "backlog" && !quiet && !tblSort("tasks").col) wireDrag(project, tr, row);
  return tr;
}

// Отпечаток строки доски: всё, из чего она нарисована, вместе с секцией, от
// которой идёт подпись кнопки. Живая работа в отпечаток входит сама, полем
// строки. У строки, где не изменилось ничего, узел переживает обновление
// нетронутым вместе с фокусом на кнопке.
function rowSign(row, sect) {
  return JSON.stringify(row) + "|" + sect + "|" + harnessSign() +
    (freshRow === row.id ? "|fresh" : "") + "|" + tblHeadSign("tasks");
}

// Только что заведённая строка. Заводят её с формы, а ищут потом глазами среди
// соседей, и пометка отвечает на вопрос «где она»: у доски строка стоит по
// рангу, у накопителя по времени, и в обоих списках новая запись оказывается не
// там, куда смотрит человек (замечание пользователя). Метка одноразовая: её
// снимает та же отрисовка, что её показала, и следующая перерисовка списка
// оставляет строку обычной.
let freshRow = "";

function freshMark(node, id) {
  if (!id || id !== freshRow) return node;
  node.classList.add("fresh");
  freshRow = "";
  return node;
}

// Отпечаток списка подписок: им нарисована кнопка запуска, и строка, не
// знающая про смену списка, держала бы стрелку выбора там, где выбирать уже
// нечего, до самой перезагрузки страницы.
function harnessSign() {
  return harnesses().map((h) => h.name).join(",") + "|" + ((harnessView && harnessView.note) || "");
}

// Половин у доски больше нет: сессии уехали в свой таб, и телефон показывает
// все четыре раздела подряд, как их показывает ноутбук. Прежде In progress с
// Check прятались за переключателем половин, и он отвечал на тот же вопрос,
// что и полоса табов над ним (решение пользователя).

// Заведение задачи на телефоне это плавающий плюс над нижними вкладками:
// полоса кнопок съедала полэкрана ещё до первой строки доски.
function newTaskFab(project) {
  const btn = el("button", "fab", "+");
  btn.type = "button";
  btn.title = "Завести в " + project;
  btn.setAttribute("aria-label", "Завести в " + project);
  // Тот же выбор, что у кнопки на ноутбуке: меню раскрывается над плюсом, а не
  // уводит на экран. На телефоне это и есть главная дорога к заведению.
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    makeMenuAt(btn, project);
  });
  return btn;
}

// Blocked собран из двух ярусов: сверху настоящие парковки с причиной, их
// подача прежняя, ниже строки, ждущие чужой задачи. Непустой ярус подписан
// всегда, даже когда он в секции один: без подписи тихая строка читалась бы
// припаркованной, а семь строк под словом Blocked не говорят, чего ждут.
// Порядок внутри яруса прежний, по рангу с доски.
function blockedItems(project, parked, held) {
  const items = [];
  const tier = (label, list, quiet) => {
    if (!list.length) return;
    items.push({
      key: "tier-" + (quiet ? "tasks" : "human"),
      sign: label + "|" + list.length,
      make: () => {
        const band = tblBand("tasks", "btier-band");
        const head = el("div", "btier" + (quiet ? " quiet" : ""), label);
        head.append(el("span", "n", String(list.length)));
        band.cell.append(head);
        return band.tr;
      },
    });
    for (const row of list) {
      items.push({
        key: row.id,
        // Секция строки в отпечатке своя: ярус решает и подпись кнопки, и
        // тишину строки, и без него переехавшая строка не перерисовалась бы.
        sign: rowSign(row, quiet ? "blocked-wait" : "blocked"),
        // Ждущая задач строка остаётся строкой очереди: кнопка у неё та же
        // погашенная «Выполнить» с причиной, а не «заблокирована».
        make: () => renderRow(project, row, quiet ? "backlog" : "blocked", { quiet: quiet }),
      });
    }
  };
  tier("ждут человека", parked, false);
  tier("ждут задач", held, true);
  return items;
}

// Экран доски открывается одним из двух: задачами или накопителем черновиков.
// Своего раздела в меню у черновиков больше нет: они лежат на той же доске, и
// разделом стояли наравне с «Агентами», у которых обзор всех проектов сразу
// (решение пользователя). Старый адрес #проект/drafts никуда не делся, он
// открывает второй таб.
// Узкий экран это телефон: там колонки нет, разделы живут нижними вкладками, и
// доска раскладывается по табам целиком.
function narrowScreen() {
  return Boolean(window.matchMedia && window.matchMedia("(max-width:900px)").matches);
}

// Табы экрана доски: задачи, сессии проекта и накопитель черновиков. Три их на
// любой ширине. Раздела «Агенты» с обзором всех досок сразу больше нет:
// сессии это работа проекта, и место им на его доске, а сквозной обзор машины
// живёт общим списком разговоров в панели (решение пользователя). У каждого
// таба свой адрес: таб переживает обновление, кнопку «назад» и ссылку.
function boardKinds() {
  return [["tasks", "Задачи"], ["sess", "Сессии"], ["drafts", "Черновики"]];
}

// Адрес таба: у задач это сама доска, у остальных её хвост.
function boardKindHash(project, key) {
  if (key === "drafts") return project + "/drafts";
  if (key === "sess") return project + "/sess";
  return project;
}

function boardKindNow(kind) {
  return kind === "drafts" || kind === "sess" ? kind : "tasks";
}

// Числа на табах доски: задачи считает доска, сессии список работ, черновики
// сервер чтением накопителя. Держатся они одним местом, потому что показать их
// надо на любом из трёх экранов, а знает каждый экран только своё.
let shownCounts = { tasks: 0, sess: 0, drafts: 0 };

function countsSet(part) {
  shownCounts = Object.assign({}, shownCounts, part || {});
}

// Табличный вид трёх разделов доски (POC DK-397). Задачи, сессии и накопитель
// это три списка об одном и том же, и выглядели они по-разному: у накопителя
// над списком стояло слово «Черновики» с числом, повторявшее таб, а порядок
// правила кнопка о двух положениях. Приём тут классический и один на все три:
// шапка колонок, нажатие на подпись сортирует по колонке, второе нажатие
// разворачивает порядок, направление видно значком. Состав колонок у раздела
// свой, потому что и строки у них разные.
//
// Ширину колонки человек правит сам: границу в шапке тянут мышью, и ширины
// запоминаются разделом. Растяжимая колонка одна, она и подбирает остаток.
//
// Колонка описывается тут целиком: подпись в шапку, слово для подсказки
// («Сортировать по рангу», а не по имени колонки в именительном), первое
// направление порядка и ширина в точках. Колонка без first это место строки,
// которое не сортируют: хвост с кнопками. Ширины отсюда едут переменными
// корня, и по ним стоят и шапка, и строка (tblWidthsPut ниже).
//
// Ровно одна колонка раздела растяжимая (flex): она забирает остаток строки, и
// тяга границы правит не её, а соседнюю. Иначе строка вылезала бы за карточку
// и уводила раздел в горизонтальную прокрутку.
const TBL_COLS = {
  tasks: [
    // Колонка номера шире прочих на отступ строки: в нём висит кружок
    // состояния, и ширина колонки этот отступ включает. Числа тут ходят за
    // боковым отступом ячейки: с пяти точек он ушёл в двенадцать, оттуда в
    // восемь, оттуда в четыре, и колонки каждый раз ужимались на разницу.
    // Место под собственную подпись колонка обязана иметь при любом отступе, а
    // лишнего держать не должна: первое меряет TestBoardTableLabelsNotCut,
    // второе TestBoardTableCellsNoDeadSpace.
    { key: "id", label: "Номер", by: "номеру", first: "asc", w: 82 },
    { key: "title", label: "Задача", by: "названию", first: "asc", flex: true },
    // Заголовок у ранга значком по той же причине, что у хода: замер показал,
    // что ширину колонки держит слово «Ранг» со значком направления (пятьдесят
    // четыре точки), а само содержимое двузначное число (тридцать). Колонка
    // читалась огромной при крошечном числе внутри, и человек это назвал.
    // Столбики говорят о величине оценки, сортировка и подсказка словами
    // остались на кнопке.
    { key: "rank", label: "Ранг", ico: "i-rank", by: "рангу", first: "desc", w: 40 },
    { key: "date", label: "Дата", by: "дате", first: "desc", w: 76 },
    // Колонка действий держит три кнопки значками, свои боковые отступы и
    // зазоры между ними: кнопку работы, кнопку разговора и три точки с
    // выбором подписки и уровня модели. Считается ширина по ним: тридцать
    // точек на кнопку, шесть на зазор и столько же на отступ с каждой стороны.
    // Три точки стоят у всякой строки, и колонка от состояния строки не
    // дёргается. Прежде тот же состав занимал 136 точек, и лишнее тут не в
    // кнопке, а в отступах (решение пользователя).
    { key: "act", label: "", w: 114 },
  ],
  sess: [
    // Заголовок у колонки хода значком, а не словом: слово «Ход» требовало под
    // себя восьмидесяти точек, и место это колонка ела у названия работы. Снять
    // заголовок вовсе человек не дал («ты вообще убрал название колонки Ход, от
    // этого стало только хуже»), и правильный размен тут другой: значок вместо
    // слова плюс обычный боковой отступ вместо расширенного. Сортировка от
    // этого никуда не делась, а колонку называет словами подсказка кнопки.
    { key: "live", label: "Ход", ico: "i-pulse", by: "ходу работы", first: "asc", w: 40 },
    { key: "title", label: "Работа", by: "названию работы", first: "asc", flex: true },
    // Дата последней содержательной реплики. Колонки возраста сессии рядом
    // больше нет: «Идёт» и «Активность» человек прочитал как одно и то же, а
    // полезна вторая (сессия висит третьи сутки и замолчала час назад). Возраст
    // не потерян, он уехал в подсказку этой же даты.
    { key: "moved", label: "Активность", by: "последней активности", first: "desc", w: 108 },
    // Действия сессии те же двумя значками, что и у строки доски: чат и
    // снятие. Ширина считается тем же счётом, что и у доски, только кнопок
    // тут две.
    { key: "act", label: "", w: 78 },
  ],
  drafts: [
    // Отметка выбора и уровень разбора живут одной колонкой: врозь они
    // занимали две, и подпись «Приоритет» переставала влезать, стоило шапке
    // добавить к ней значок направления (замечание пользователя). Ширина стоит
    // по самому длинному слову чипа вместе с отметкой: прежних 112 точек не
    // хватало на собственное содержимое, и «средний» читался «средн». Тридцать
    // две точки колонка вернула названию двумя заходами: сперва с её первой
    // ячейки сняли отступ под кружок, которого у накопителя нет вовсе, потом
    // вдвое ужались боковой отступ и зазор между галочкой и чипом («расстояние
    // между ними можно спокойно сократить вдвое», слова пользователя).
    { key: "prio", label: "Приоритет", by: "приоритету", first: "desc", w: 108 },
    { key: "id", label: "Номер", by: "номеру", first: "asc", w: 70 },
    { key: "title", label: "Задача", by: "названию", first: "asc", flex: true },
    { key: "date", label: "Дата", by: "дате", first: "desc", w: 76 },
    // Действие у записи одно, разговор, и колонка стоит по нему: кнопка и два
    // боковых отступа тем же счётом, что у доски и сессий.
    { key: "act", label: "", w: 42 },
  ],
};

// Порядок, каким раздел открывается впервые. У доски он пустой нарочно: строки
// стоят так, как их сложила сама доска (очередь по рангу, прочие секции руками),
// и подменять этот порядок своим экран не вправе, пока человек не попросил.
const TBL_DEFAULT = {
  tasks: { col: "", dir: "" },
  sess: { col: "live", dir: "asc" },
  drafts: { col: "date", dir: "desc" },
};

const TBL_SORT_KEY = {
  tasks: "devkit.dash.tasks.sort",
  sess: "devkit.dash.sess.sort",
  drafts: "devkit.dash.drafts.sort",
};

// Ключ памяти ширин свой у каждого раздела. Имя сменилось вместе с переходом
// на таблицу: прежние числа мерили колонку своей сетки, где боковые отступы
// строки лежали снаружи, и в таблице те же числа дали бы другую картинку.
const TBL_WIDE_KEY = {
  tasks: "devkit.dash.tasks.tblcols",
  sess: "devkit.dash.sess.tblcols",
  drafts: "devkit.dash.drafts.tblcols",
};

// Накопитель помнил свой порядок словом ещё до шапки (DK-353, кнопка о двух
// положениях), и ключ хранилища у него тот же. Прежние значения переводятся в
// колонку с направлением: иначе выбор человека сбросился бы на первой же
// отрисовке нового вида.
const TBL_SORT_OLD = { fresh: "date:desc", title: "title:asc" };

function tblSort(sect) {
  const def = TBL_DEFAULT[sect] || { col: "", dir: "" };
  let got = "";
  try {
    got = localStorage.getItem(TBL_SORT_KEY[sect]) || "";
  } catch (err) {
    got = "";
  }
  if (TBL_SORT_OLD[got]) got = TBL_SORT_OLD[got];
  const at = got.indexOf(":");
  const col = at < 0 ? "" : got.slice(0, at);
  const dir = at < 0 ? "" : got.slice(at + 1);
  const known = (TBL_COLS[sect] || []).some((c) => c.key === col && c.first);
  if (!known || (dir !== "asc" && dir !== "desc")) return { col: def.col, dir: def.dir };
  return { col, dir };
}

function keepTblSort(sect, now) {
  try {
    localStorage.setItem(TBL_SORT_KEY[sect], now.col ? now.col + ":" + now.dir : "");
  } catch (err) {
    // Приватное окно браузера запрещает запись: выбор тогда живёт до ухода с
    // экрана, а список работает.
  }
}

// Пределы тяги. Меньше нижнего в колонке не остаётся даже подписи, выше
// верхнего одна колонка съедает строку целиком, и растяжимой не остаётся
// ничего.
const TBL_COL_MIN = 32;
const TBL_COL_MAX = 460;
// Нижний предел растяжимой колонки. Своей ширины у неё нет, она подбирает
// остаток строки, и без этого предела соседи забирали остаток до нуля: таблица
// с table-layout:fixed становится шире карточки на всю сумму колонок, кнопки
// уезжают на фон страницы, а номер задачи режется слева (снимок пользователя).
const TBL_FLEX_MIN = 160;
// Запасная мера места, когда мерить нечего: боковая колонка проекта и поля
// страницы. Настоящее место меряется живым списком, это число работает до
// первой сборки экрана.
const TBL_ROOM_SIDE = 320;

// Место, в которое таблица обязана уложиться: она стоит во всю ширину списка
// разделов, и сумма колонок больше этой ширины уводит строку за карточку.
// Меряется живой список, а не окно: панель разговора, боковая колонка и поля
// страницы съедают своё, и складывать их числами тут значило бы держать копию
// раскладки.
function tblRoom() {
  const box = typeof document !== "undefined" && document.getElementById
    ? document.getElementById("groups")
    : null;
  const live = box ? Number(box.clientWidth) || 0 : 0;
  if (live > 0) return live;
  const win = typeof window !== "undefined" && Number(window.innerWidth) || 0;
  return win > 0 ? Math.max(TBL_COL_MIN, win - TBL_ROOM_SIDE) : 0;
}

// Сколько места остаётся колонке key, если прочие стоят как стоят. Растяжимой
// оставляется её нижний предел: место при тяге берётся у соседей, а не у
// таблицы, и упор границы это ровно тот случай, когда брать больше не у кого.
function tblCap(sect, widths, key) {
  const cols = TBL_COLS[sect] || [];
  const room = tblRoom();
  if (!room) return TBL_COL_MAX;
  let others = 0;
  let flex = 0;
  for (const col of cols) {
    if (col.flex) {
      flex = TBL_FLEX_MIN;
      continue;
    }
    if (col.key !== key) others += Number(widths[col.key]) || 0;
  }
  return Math.max(TBL_COL_MIN, room - flex - others);
}

// Уложить ширины в место таблицы. Нужно это на чтении: числа в памяти человек
// натянул на широком мониторе, а открыть страницу может на узкой, и тогда
// сохранённое не влезает ни в какую тягу. Лишнее снимается долями с тех
// колонок, которым есть что отдать, чтобы ужималась широкая, а не узкая.
function tblFitWidths(sect, widths) {
  const cols = (TBL_COLS[sect] || []);
  const keys = cols.filter((col) => !col.flex).map((col) => col.key);
  const room = tblRoom();
  if (!room || !keys.length) return widths;
  const left = room - (cols.some((col) => col.flex) ? TBL_FLEX_MIN : 0);
  const sum = () => keys.reduce((was, key) => was + widths[key], 0);
  // Проходов немного и они конечны: каждый либо снимает всё лишнее, либо
  // упирает часть колонок в нижний предел и уменьшает делимое.
  for (let pass = 0; pass < 8; pass++) {
    const over = sum() - left;
    if (over <= 0) break;
    const give = keys.filter((key) => widths[key] > TBL_COL_MIN);
    const free = give.reduce((was, key) => was + widths[key] - TBL_COL_MIN, 0);
    if (!free) break;
    for (const key of give) {
      const part = Math.ceil(over * (widths[key] - TBL_COL_MIN) / free);
      widths[key] = Math.max(TBL_COL_MIN, widths[key] - Math.min(part, over));
    }
  }
  return widths;
}

// Ширины колонок раздела, какими их оставил человек. Чужие ключи и мусор из
// хранилища отбрасываются молча: ширина это удобство, и падать из-за неё
// список не должен.
function tblWidths(sect) {
  const out = {};
  let got = "";
  try {
    got = localStorage.getItem(TBL_WIDE_KEY[sect]) || "";
  } catch (err) {
    got = "";
  }
  let said = null;
  try {
    said = got ? JSON.parse(got) : null;
  } catch (err) {
    said = null;
  }
  for (const col of TBL_COLS[sect] || []) {
    if (col.flex) continue;
    const px = said && Number(said[col.key]);
    out[col.key] = Number.isFinite(px) && px > 0
      ? Math.min(TBL_COL_MAX, Math.max(TBL_COL_MIN, Math.round(px)))
      : col.w;
  }
  // Память держит то, что человек натянул, а место называет экран: ужимает
  // чтение, а не запись, иначе один заход с ноутбука обрезал бы ширины
  // навсегда.
  return tblFitWidths(sect, out);
}

function keepTblWidths(sect, widths) {
  try {
    localStorage.setItem(TBL_WIDE_KEY[sect], JSON.stringify(widths));
  } catch (err) {
    // Как и с порядком: запрет записи оставляет ширины живущими до ухода с
    // экрана, а таблица работает.
  }
}

// Ширины кладутся переменными корня, а не в разметку строк: строк на экране
// сотня, а перерисовка списка идёт по кругу, и вписывать сетку в каждую
// значило бы переписывать её на каждом тике опроса. Правило сетки лежит в css
// один раз и читает эти переменные, а узкий экран их просто не спрашивает.
function tblWidthsAll() {
  for (const sect of Object.keys(TBL_COLS)) tblWidthsPut(sect);
}

function tblWidthsPut(sect) {
  const widths = tblWidths(sect);
  const root = document.documentElement;
  if (!root || !root.style || !root.style.setProperty) return widths;
  for (const key of Object.keys(widths)) {
    root.style.setProperty("--tc-" + sect + "-" + key, widths[key] + "px");
  }
  return widths;
}

// Что станет с порядком от нажатия на подпись: чужая колонка открывается своим
// первым направлением, своя разворачивается.
function tblSortNext(sect, col) {
  const now = tblSort(sect);
  const def = (TBL_COLS[sect] || []).find((c) => c.key === col);
  if (!def || !def.first) return now;
  if (now.col !== col) return { col, dir: def.first };
  return { col, dir: now.dir === "asc" ? "desc" : "asc" };
}

// Отпечаток шапки: от порядка зависит и подсветка колонки, и значок
// направления, и без него шапка держала бы значок там, где он уже не стоит.
function tblHeadSign(sect) {
  const now = tblSort(sect);
  return sect + "|" + now.col + "|" + now.dir;
}

// Какую колонку двигает граница между i-й и следующей. Растяжимая колонка
// забирает остаток строки, своей ширины у неё нет вовсе, и тянуть её саму
// нечем. Поэтому граница левее растяжимой правит колонку слева от себя, а
// граница правее правит колонку справа от себя обратным ходом: курсор в обоих
// случаях едет туда, куда его тянут, а место отдаёт и забирает растяжимая.
//
// Счёт при таком правиле сходится: границ ровно столько же, сколько колонок со
// своей шириной, и у каждой такой колонки своя ручка. Прежнее правило («граница
// правит колонку слева, а у растяжимой соседнюю справа») оставляло последнюю
// колонку без ручки вовсе: колонку действий было не ужать, а соседнюю двигали
// сразу две границы (замечание пользователя).
function tblGripAim(cols, at) {
  const flexAt = cols.findIndex((col) => col.flex);
  if (flexAt < 0 || at < flexAt) {
    const own = cols[at];
    return own && !own.flex ? { key: own.key, sign: 1 } : null;
  }
  const next = cols[at + 1];
  return next && !next.flex ? { key: next.key, sign: -1 } : null;
}

// Тяга границы колонки. Ширина считается от той, что была на нажатии, а не от
// замера узла: замерять сетку по ходу движения значит гонять раскладку на
// каждый пиксель, а число у нас и так своё.
function tblGrip(sect, aim, onDone) {
  const grip = el("span", "tblg");
  grip.setAttribute("role", "separator");
  grip.setAttribute("aria-label", "Потянуть границу колонки");
  let from = 0;
  let was = 0;
  const move = (ev) => {
    const widths = tblWidths(sect);
    // Верхний упор считается по месту таблицы, а не одним числом на все
    // колонки: тяга обязана останавливаться там, где растяжимой колонке
    // остаётся её нижний предел, иначе строка вылезает за карточку.
    const cap = Math.min(TBL_COL_MAX, tblCap(sect, widths, aim.key));
    const px = Math.min(cap,
      Math.max(TBL_COL_MIN, was + (Number(ev.clientX) - from) * aim.sign));
    widths[aim.key] = Math.round(px);
    keepTblWidths(sect, widths);
    tblWidthsPut(sect);
  };
  const up = () => {
    document.removeEventListener("pointermove", move);
    document.removeEventListener("pointerup", up);
    if (grip.classList) grip.classList.remove("on");
    // Пока граница едет, курсор ходит по подписям колонок, и браузер выделяет
    // их текст синим: тяга выглядела бы выделением строки, а не тягой.
    if (document.body && document.body.classList) document.body.classList.remove("tbldrag");
    if (onDone) onDone();
  };
  grip.addEventListener("pointerdown", (ev) => {
    if (ev.stopPropagation) ev.stopPropagation();
    if (ev.preventDefault) ev.preventDefault();
    from = Number(ev.clientX) || 0;
    was = tblWidths(sect)[aim.key] || 0;
    grip.classList.add("on");
    if (document.body && document.body.classList) document.body.classList.add("tbldrag");
    document.addEventListener("pointermove", move);
    document.addEventListener("pointerup", up);
  });
  // Двойное нажатие возвращает колонке её ширину по умолчанию: промахнувшись
  // тягой, человек иначе подгонял бы точки обратно на глаз.
  grip.addEventListener("dblclick", (ev) => {
    if (ev.stopPropagation) ev.stopPropagation();
    const def = (TBL_COLS[sect] || []).find((c) => c.key === aim.key);
    if (!def) return;
    const widths = tblWidths(sect);
    widths[aim.key] = def.w;
    keepTblWidths(sect, widths);
    tblWidthsPut(sect);
    if (onDone) onDone();
  });
  return grip;
}

// Таблица раздела. Списки стояли на своей сетке: шапка одним гридом, строки
// другим, ширины связывались переменными, и подписи всё равно вставали мимо
// ячеек. Теперь это настоящая таблица браузера, и колонки раскладывает движок:
// table-layout:fixed читает ширины из colgroup, а место ячейки в строке от
// места подписи в шапке не отличается ничем, потому что колонка у них одна.
function tblTable(sect) {
  return el("table", "tbl t-" + sect);
}

// Колонки таблицы. Ширины лежат переменными корня (tblWidthsPut выше), и col
// читает их оттуда: тяга границы правит переменную, а таблица встаёт заново
// сама, без пересборки разметки. Растяжимой колонке ширины не назначается
// вовсе, остаток строки движок отдаёт ей.
function tblColgroup(sect) {
  const group = el("colgroup");
  for (const col of TBL_COLS[sect] || []) {
    const one = el("col", "cw-" + col.key);
    if (!col.flex) {
      one.style.width = "var(--tc-" + sect + "-" + col.key + ", " + col.w + "px)";
    }
    group.append(one);
  }
  return group;
}

// Полоса во всю ширину таблицы: заголовок секции, подпись яруса, щель жеста и
// слово пустого списка стоят строкой с одной ячейкой на все колонки. Начинка
// кладётся в ячейку, а не в саму строку: подача у этих полос прежняя, и своих
// стилей им заводить не надо.
function tblBand(sect, cls) {
  const tr = el("tr", "band" + (cls ? " " + cls : ""));
  const cell = el("td", "bcell");
  cell.setAttribute("colspan", String((TBL_COLS[sect] || []).length));
  tr.append(cell);
  return { tr, cell };
}

// Ячейка строки с начинкой в ряд. Флексом сама ячейка быть не может: table-cell
// с чужим display перестаёт быть ячейкой таблицы, движок заворачивает её в
// безымянную, и колонка уезжает. Поэтому ряд живёт вложенной коробкой, а
// ячейка остаётся ячейкой.
function tblCell(cls) {
  const cell = el("td", cls);
  const box = el("span", "cin");
  cell.append(box);
  return { cell, box };
}

// Шапка колонок. Направление рисуется значком свёртки: своих стрелок у статики
// нет, а этот набор уже лежит в разметке и читается тем же жестом, вверх это
// по возрастанию. Ячейка шапки это th той же колонки, что и ячейка строки:
// подпись стоит над своей ячейкой не по нашему счёту точек, а потому что
// колонка у них общая.
function tblHead(sect, onPick) {
  const now = tblSort(sect);
  tblWidthsPut(sect);
  const head = el("thead");
  const row = el("tr", "tblh h-" + sect);
  head.append(row);
  const cols = TBL_COLS[sect] || [];
  cols.forEach((col, at) => {
    const cell = el("th", "tblc");
    cell.setAttribute("scope", "col");
    // Ключ колонки стоит в разметке: по нему колонку находят стенды и правила
    // стилей, а подпись у колонки состояния пустая, и звать её больше нечем.
    cell.setAttribute("data-col", col.key);
    if (!col.first) {
      cell.append(el("span", "tbln", col.label || ""));
    } else {
      const on = now.col === col.key;
      const btn = el("button", "tblb" + (on ? " tblon" : ""));
      btn.type = "button";
      // Подпись колонки бывает словом и бывает значком. Значок занимает место
      // одной буквы, а сортировку и подсказку несёт та же кнопка: колонка
      // остаётся подписанной, просто подпись у неё рисованная.
      if (col.ico) {
        const mark = el("span", "tblico");
        mark.append(icon(col.ico));
        btn.append(mark);
      } else {
        btn.append(el("span", "tbll", col.label));
      }
      if (on) btn.append(icon(now.dir === "asc" ? "i-unfold" : "i-fold"));
      // Подсказка говорит, что случится от нажатия, и говорит по-русски:
      // прежнее «Поставить список по колонке» человек читать отказался.
      // Колонка, подписанная значком, называет себя тут же словами: одно
      // «развернуть порядок» над рисунком ничего не называет.
      const worded = col.label && !col.ico;
      const say = on
        ? (worded ? "Развернуть порядок" : "Развернуть порядок по " + col.by)
        : "Сортировать по " + col.by;
      withTip(btn, say);
      btn.setAttribute("aria-label", say);
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        keepTblSort(sect, tblSortNext(sect, col.key));
        onPick();
      });
      cell.append(btn);
    }
    // Граница тянется у всякой колонки, кроме последней: справа от неё двигать
    // нечего. Жест живёт широким экраном, на узком строка раскладывается по
    // областям и колонок там нет вовсе (css гасит саму ручку).
    const aim = at + 1 < cols.length ? tblGripAim(cols, at) : null;
    if (aim) cell.append(tblGrip(sect, aim));
    row.append(cell);
  });
  return head;
}

// Тело таблицы под шапкой. Разделов у доски четыре, и каждый идёт своим tbody:
// перерисовка по ключам ходит внутрь одного тела, а соседние секции стоят
// нетронутыми.
function tblBodyItem(key, sign, items, cls) {
  return {
    key,
    sign,
    make: () => {
      const body = el("tbody", cls || "");
      sync(body, items);
      return body;
    },
    fill: (body) => { sync(body, items); },
  };
}

// Сравнение двух строк по колонке. Наружу отдаётся готовая функция сортировки:
// раздел даёт свой съёмник значений, а направление и разбор равных одни на все
// три списка.
function tblSorted(list, sect, valueOf, tieOf) {
  const now = tblSort(sect);
  if (!now.col) return list.slice();
  const dir = now.dir === "asc" ? 1 : -1;
  return list.slice().sort((a, b) => {
    const av = valueOf(a, now.col);
    const bv = valueOf(b, now.col);
    let by = 0;
    if (typeof av === "string" || typeof bv === "string") {
      by = String(av || "").localeCompare(String(bv || ""), "ru");
    } else {
      by = (Number(av) || 0) - (Number(bv) || 0);
    }
    if (by) return by * dir;
    // Равные значения разбираются вторым ключом раздела, а не оставляются на
    // произвол движка: список, переставляющийся сам по себе между обновлениями,
    // читается как поломка. Направление уезжает туда же: у дня записи второй
    // ключ это её номер, и в перевёрнутом списке он обязан перевернуться тоже.
    return tieOf ? tieOf(a, b, now.dir) : 0;
  });
}


function boardKindBar(project, kind) {
  const bar = el("div", "ktabs");
  const now = boardKindNow(kind);
  for (const [key, label] of boardKinds()) {
    const btn = el("button", "ktab" + (key === now ? " onktab" : ""), label);
    btn.type = "button";
    btn.dataset.kind = key;
    // Число стоит баджем у каждого таба, а не у одного: вид один на все три,
    // иначе счётчик читается как признак самого таба (замечание пользователя).
    const n = shownCounts[key] || 0;
    if (n) btn.append(el("span", "n", String(n)));
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      if (key === now) return;
      goKeepingChat(boardKindHash(project, key));
    });
    bar.append(btn);
  }
  return bar;
}

// Отпечаток нарисованной доски: строки как они есть, число живых работ (по
// нему стоит бадж таба) и список подписок (из него собрана кнопка запуска). Всё
// прочее в ответе ручки экрана не касается, и класть его в отпечаток значит
// перерисовывать список от чужого движения: время последнего хода работы
// меняется каждые несколько секунд, а строки задач от него не зависят.
function boardPaintSign(board, works) {
  const rows = [];
  for (const sec of board.sections || []) {
    for (const row of sec.rows || []) rows.push(JSON.stringify(row));
  }
  return rows.join("\n") + "|" + ((works || []).length) + "|" + harnessSign() +
    "|" + (freshRow || "");
}

// Что нарисовано на доске сейчас: экран и отпечаток его данных.
let boardPainted = { screen: "", sign: "" };

function taskValue(row, col) {
  if (col === "title") return String(row.title || "");
  if (col === "rank") return Number(row.r) || 0;
  if (col === "date") return String(row.moved || "");
  return rowNum(row.id);
}

// Строки секции в том порядке, какой выбрала шапка. Пустая колонка это порядок
// самой доски, и тогда список не трогается вовсе: очередь стоит по рангу,
// прочие секции руками, и подменять это своим порядком экран не вправе.
function tasksSorted(rows) {
  return tblSorted(rows, "tasks", taskValue, (a, b) => rowNum(a.id) - rowNum(b.id));
}

// Строка, за которой опрос обязан ходить: та, что двинется сама, без нашего
// нажатия. Поводов три, и все три это чужой ход, а не наш: живая работа за
// строкой любого рода, ожидание ответа человека и живая сессия с тем же ID в
// списке работ.
//
// Прежде круг заводила одна tmux-работа, то есть строка, уже стоящая под
// «Стопом». Переход он умел заметить ровно один, конец работы, а начало
// проспал: агент, вставший с вопросом к человеку, идёт по всему серверу
// разговором (tmuxTalk), признака работы у строки нет, круга нет. Человек
// отвечал агенту в чате, агент шёл дальше, а «Стоп» на строке не вставал до
// перезагрузки страницы (живой случай пользователя). Замкнуто это было само на
// себя: круг узнавал о ходе по тому же признаку, ради которого и заводился.
//
// Ожидание тут повод наравне с работой: ответ снимает признак, и следующий же
// заход круга видит ход. Строка с кончившимися сессиями (gone) сама не
// двинется, и за ней круг не ходит.
function rowMoves(row, byID) {
  if (!row || !row.id) return false;
  if (row.waiting) return true;
  if (row.run && row.run !== "gone") return true;
  const w = byID.get(row.id);
  return Boolean(w && w.live && w.live !== WORK_DEAD);
}

// Живая сессия проекта, ещё не назвавшая своей строки. Работой она становится
// не подъёмом, а первой командой доски (agentctl stage, taskctl move), и до неё
// строки у сессии нет вовсе. Круг, заведённый только по строкам, этот переход
// проспать обязан: строка узнаёт о работе тем же заходом, которого нет.
//
// Живой случай приёмки DK-716: человек написал в чат по задаче, работа
// открылась через полминуты, а кнопка «Выполнить» так и стояла вместо «Стопа»
// до перезагрузки страницы.
function workMoves(w) {
  return Boolean(w && w.live && w.live !== WORK_DEAD);
}

// Идёт ли доске опрос: хватает одной строки, которая может двинуться сама, либо
// одной живой сессии, которая эту строку вот-вот присвоит.
function boardMoves(board, works) {
  const byID = new Map();
  for (const w of works || []) {
    if (w && w.id && !byID.has(w.id)) byID.set(w.id, w);
  }
  if ((works || []).some(workMoves)) return true;
  return (board.sections || []).some((sec) =>
    (sec.rows || []).some((row) => rowMoves(row, byID)));
}

function renderBoard(project, board, works) {
  const groups = document.getElementById("groups");
  // Число задач считается по самой доске: секции у неё те же, что и на экране.
  let rowsN = 0;
  for (const sec of board.sections || []) rowsN += (sec.rows || []).length;
  countsSet({ tasks: rowsN, sess: (works || []).length });
  const items = [{
    key: "board-kind",
    // Отпечаток несёт открытый таб и числа на нём: от первого зависит
    // подсветка, от вторых баджи.
    sign: [project, "tasks", rowsN, (works || []).length, shownCounts.drafts].join("|"),
    make: () => boardKindBar(project, "tasks"),
  }];
  // Доска это одна таблица на все четыре секции, а не таблица на секцию: шапка
  // колонок тогда одна, стоит она в той же таблице, что и строки, и колонки у
  // них общие. Секция идёт своим tbody с заголовком-полосой сверху.
  const parts = [{
    key: "cols",
    sign: "cols",
    make: () => tblColgroup("tasks"),
  }, {
    key: "head",
    sign: tblHeadSign("tasks"),
    make: () => tblHead("tasks", () => { renderBoard(project, board, works); }),
  }];
  // Полосы кнопок под табами больше нет: «Черновики» переехали в левое меню
  // отдельным разделом со своим адресом, а «Новая задача» в шапку рядом с
  // поиском. Обе мозолили глаза на самой доске, ради которой экран и открыт
  // (замечание пользователя).
  const byKey = {};
  for (const sec of board.sections || []) byKey[sec.key] = sec;
  // Строка Backlog с незакрытым маркером «после DK-NNN» показывается в Blocked
  // нижним ярусом: запустить её нельзя, а в очереди она отвечала не на тот
  // вопрос, ради которого очередь и открывают («чем заняться сейчас»). Доска в
  // git от этого не меняется: статус строки прежний, это группировка вида.
  // Держащей считается задача, которая ещё стоит на доске: закрытая уезжает в
  // архив, и маркер на неё больше никого не держит.
  const onBoard = new Set();
  for (const sec of board.sections || []) for (const row of sec.rows || []) onBoard.add(row.id);
  const holdersOf = (row) => (row.after || []).filter((dep) => onBoard.has(dep));
  const backRows = (byKey.backlog && byKey.backlog.rows) || [];
  const freeRows = backRows.filter((row) => !holdersOf(row).length);
  const heldRows = backRows.filter((row) => holdersOf(row).length);
  // Снимок нарисованной очереди: по нему жест считает коридор и щели, и берётся
  // он с той же доски, которой нарисован список.
  backlogView = { project, rows: freeRows };
  for (const key of SECTION_ORDER) {
    let sec = byKey[key];
    // Ждущие задач строки есть, а секции с доски не пришло вовсе: рисуем её
    // сами, иначе они пропали бы с экрана вместе со своим ярусом.
    if (!sec && key === "blocked" && heldRows.length) sec = { key, title: "Blocked", rows: [] };
    if (!sec) continue;
    const parked = key === "blocked" ? sec.rows || [] : [];
    const secRows = key === "backlog" ? freeRows
      : key === "blocked" ? parked.concat(heldRows) : sec.rows || [];
    const rows = [{
      key: "shead",
      sign: sec.title + "|" + secRows.length,
      make: () => {
        const band = tblBand("tasks", "secband");
        const head = el("div", "shead", sec.title);
        // Backlog стоит по рангу, и счётчик говорит это же: надписью под
        // формой задачи порядок объяснять больше не надо.
        head.append(el("span", "n", secRows.length + (key === "backlog" ? ", по рангу" : "")));
        band.cell.append(head);
        return band.tr;
      },
    }];
    for (const item of key === "blocked"
      ? blockedItems(project, tasksSorted(parked), tasksSorted(heldRows))
      : tasksSorted(secRows).map((row) => ({
        key: row.id,
        sign: rowSign(row, key),
        make: () => renderRow(project, row, key),
      }))) {
      rows.push(item);
    }
    if (rows.length === 1) {
      rows.push({
        key: "empty",
        sign: "",
        make: () => {
          const band = tblBand("tasks", "bempty");
          band.cell.append(el("div", "empty", "Нет."));
          return band.tr;
        },
      });
    }
    // Отпечаток тела собран из отпечатков строк: не изменилась ни одна, значит
    // внутрь секции можно не заходить вовсе.
    parts.push(tblBodyItem("sec-" + key, rows.map((r) => r.key + "=" + r.sign).join("\n"),
      rows, "tsec"));
  }
  items.push({
    key: "board-table",
    sign: parts.map((p) => p.key + "=" + p.sign).join("\n"),
    make: () => {
      const table = tblTable("tasks");
      sync(table, parts);
      return table;
    },
    fill: (table) => { sync(table, parts); },
  });
  items.push({ key: "board-fab", sign: project, make: () => newTaskFab(project) });
  sync(groups, items);
}

// Перетаскивание строки в очереди (LLD DK-328, решение 1). Порядок Backlog
// выводится из ранга, класть строку в позицию нечем, и жест правит ровно одно
// слагаемое, ценность: серьёзность это суждение об ущербе и полоса, её
// перетаскиванием не двигают вовсе; неопределённость снимается разбором, а не
// желанием; поправка на баг привязана к типу строки; рычаг проверяется, а не
// назначается.
//
// Отсюда коридор строки: при серьёзности S и сумме прочих мягких слагаемых M
// доступны ранги от S+M до S+M+10, и полосу серьёзности такой коридор не
// переезжает никогда, десять меньше двадцати пяти. Считает целевой ранг щели
// клиент, но это превью по той доске, которую видел экран: доску правят
// соседние сессии и цикл цели, а фактическое место называет ответ ручки.
const DRAG_HOLD = 400;
const DRAG_SLOP = 6;
const DRAG_LIT = 2400;

// Строки очереди в том порядке, в каком они нарисованы: жест считает по ним, а
// не по разметке, потому что ранг с номером в разметке не лежат.
let backlogView = { project: "", rows: [] };

// Живой жест: от нажатия до броска. Между обновлениями экрана состояния клиент
// не держит нигде, и это не исключение: строку либо держат прямо сейчас, либо
// жеста нет.
let drag = null;

function dragOn() {
  return Boolean(drag && drag.on);
}

// Клик, который остался от броска: браузер шлёт его тем же нажатием, и без
// этого каждый жест кончался бы заходом внутрь задачи.
let dragClick = false;

function dragAteClick() {
  if (!dragClick) return false;
  dragClick = false;
  return true;
}

// Номер строки: вторым ключом сортировки очереди идёт он, возрастанием
// (insertIdx в tools/taskctl/board.go).
function rowNum(id) {
  const hit = /(\d+)\s*$/.exec(String(id || ""));
  return hit ? Number(hit[1]) : 0;
}

// Коридор строки: пол, потолок, сегодняшняя ценность и сегодняшний ранг.
// Слагаемых не пять, значит и коридора нет: такую строку жест не берёт.
function dragCorridor(row) {
  const parts = (row && row.r_parts) || [];
  if (parts.length !== RANK_PARTS.length) return null;
  const low = parts[0] + parts[2] + parts[3] + parts[4];
  return { low, high: low + 10, value: parts[1], r: low + parts[1] };
}

// Полоса P считается из суммы ранга (bucket в tools/taskctl/board.go), рукой
// не ставится. У верхнего края коридора мягкие слагаемые под потолок как раз
// перетаскивают строку через порог, и молчать об этом нельзя: полоса
// единственное, что видно на доске без разворота ранга.
function rankBand(r) {
  if (r >= 75) return "P0";
  if (r >= 50) return "P1";
  if (r >= 25) return "P2";
  return "P3";
}

// Какие ранги вообще ставят строку в эту щель, без оглядки на коридор: ниже
// соседа сверху и выше соседа снизу по паре ключей «ранг убыванием, номер
// возрастанием». Пустой промежуток (low больше high) значит щель, в которую
// строка не встаёт никаким рангом: так выходит у соседей с общим рангом и у
// соседей с соседними рангами, когда номер строки не попадает между их
// номерами.
function gapNeed(above, below, num) {
  const low = below ? below.r + (num < rowNum(below.id) ? 0 : 1) : 0;
  const high = above ? above.r - (num > rowNum(above.id) ? 0 : 1) : 100;
  return { low, high };
}

// Причина, по которой щель мертва. Слов тут два случая: строка не встаёт между
// этими соседями ни одним рангом, и щель просит ранг за краем коридора.
function gapWhy(cor, above, below, num, id) {
  const need = gapNeed(above, below, num);
  if (need.low > need.high) {
    const up = num < rowNum(above.id) ? above.r : above.r + 1;
    const down = num > rowNum(below.id) ? below.r : below.r - 1;
    return "места нет: между " + above.id + " и " + below.id + " строка " + id +
      " не встаёт ни одним рангом, выше " + above.id + " ставит ранг " + up +
      ", ниже " + below.id + " ранг " + down;
  }
  return need.low > cor.high ? dragCeil(cor, id) : dragFloor(cor, id);
}

function dragCeil(cor, id) {
  return "выше не поднять: жест правит только ценность, и её потолок даёт " +
    id + " ранг " + cor.high;
}

function dragFloor(cor, id) {
  return "ниже не опустить: жест правит только ценность, и её пол даёт " +
    id + " ранг " + cor.low;
}

// Куда целится щель под номером gap: щель ноль это место над первой чужой
// строкой, щель N это место под последней. Из подходящих рангов берётся
// ближайший к сегодняшнему, а на равном расстоянии меньший: жест меняет
// ценность ровно настолько, насколько нужно, чтобы встать на выбранное место,
// и не раздувает её впрок.
function gapAim(list, id, gap) {
  const rows = list || [];
  const cor = dragCorridor(rows.find((row) => row.id === id));
  if (!cor) return null;
  const rest = rows.filter((row) => row.id !== id);
  const above = gap > 0 ? rest[gap - 1] : null;
  const below = gap < rest.length ? rest[gap] : null;
  const num = rowNum(id);
  const aim = { above, below, r: null, value: null, why: "" };
  for (let r = cor.low; r <= cor.high; r += 1) {
    if (above && !(r < above.r || (r === above.r && num > rowNum(above.id)))) continue;
    if (below && !(r > below.r || (r === below.r && num < rowNum(below.id)))) continue;
    if (aim.r === null || Math.abs(r - cor.r) < Math.abs(aim.r - cor.r)) aim.r = r;
  }
  if (aim.r === null) {
    aim.why = gapWhy(cor, above, below, num, id);
    return aim;
  }
  aim.value = aim.r - cor.low;
  return aim;
}

// Ярлык, который едет со строкой, пока её держат: что станет с рангом, с
// ценностью и с полосой. Мёртвая щель говорит свою причину тем же ярлыком:
// молчащий под пальцем жест неотличим от сломанного.
function dragTagText(cor, aim) {
  if (!aim || aim.r === null) return (aim && aim.why) || "";
  if (aim.r === cor.r) return "ранг " + cor.r + ", ценность " + cor.value + ": место то же";
  let text = "ранг " + cor.r + " -> " + aim.r +
    ", ценность " + cor.value + " -> " + aim.value;
  if (rankBand(aim.r) !== rankBand(cor.r)) {
    text += ", полоса " + rankBand(cor.r) + " -> " + rankBand(aim.r);
  }
  return text;
}

function wireDrag(project, tr, row) {
  tr.addEventListener("pointerdown", (ev) => { dragTake(project, tr, row, ev); });
  tr.addEventListener("pointermove", (ev) => { dragMove(ev); });
  tr.addEventListener("pointerup", (ev) => { dragDrop(ev); });
  tr.addEventListener("pointercancel", () => { dragQuit(); });
  // Прокрутку под взятой строкой отменяет обработчик, а не стили. Судьбу
  // касания браузер решает по первому движению пальца и смотрит при этом на
  // touch-action, каким он был в момент касания: класс, приезжающий через
  // 400 мс удержания, ему уже не указ. Не отменив это движение, касание уезжает
  // прокруткой, указатель отменяется (pointercancel), и жест умирает, не
  // начавшись: браузерная приёмка нашла ровно это. Слушатель нарочно не
  // passive, только такому браузер разрешает отменить прокрутку, а до взятия
  // строки он ничего не отменяет, и список листается пальцем как прежде.
  tr.addEventListener("touchmove", (ev) => {
    if (!dragOn()) return;
    if (ev.cancelable) ev.preventDefault();
  }, { passive: false });
}

function dragTake(project, tr, row, ev) {
  if (drag || (ev.button !== undefined && ev.button !== 0)) return;
  // Нажатие на кнопку строки жестом не становится: у кнопки запуска, суммы
  // ранга и выбора подписки свои дела.
  if (ev.target && ev.target.closest && ev.target.closest("button, select, a")) return;
  if (!tr.parentElement || !dragCorridor(row)) return;
  drag = {
    project, row, tr, id: row.id, card: tr.parentElement,
    y: ev.clientY, on: false, hold: 0, gap: -1, aim: null, marks: [], slots: [],
  };
  if (ev.pointerType === "touch") {
    // Пальцем строка берётся долгим нажатием: короткое касание по-прежнему
    // открывает задачу, а пролистывание списка остаётся пролистыванием
    // (телефонная доска DK-285).
    drag.hold = setTimeout(() => { dragStart(ev.pointerId); }, DRAG_HOLD);
  }
}

function dragMove(ev) {
  if (!drag) return;
  const dy = ev.clientY - drag.y;
  if (!drag.on) {
    // Палец поехал раньше, чем строка взялась: это пролистывание, и жест
    // снимается вовсе. Мышью строка берётся сразу, порог тут только затем,
    // чтобы клик остался кликом.
    if (ev.pointerType === "touch") {
      if (Math.abs(dy) > DRAG_SLOP) dragQuit();
      return;
    }
    if (Math.abs(dy) < DRAG_SLOP) return;
    dragStart(ev.pointerId);
  }
  // Пока строку держат, список под ней не прокручивается: жест и прокрутка
  // спорят за одно движение пальца.
  if (ev.cancelable && ev.preventDefault) ev.preventDefault();
  dragAim(ev.clientY);
}

function dragStart(pointerId) {
  if (!drag || drag.on) return;
  clearTimeout(drag.hold);
  drag.on = true;
  drag.tr.classList.add("dragrow");
  if (drag.tr.setPointerCapture && pointerId !== undefined) {
    drag.tr.setPointerCapture(pointerId);
  }
  dragDraw();
  dragAim(drag.y);
}

// Коридор рисуется прямо на списке: за его краем строки приглушены, у самого
// края стоит причина, а живые щели подписаны рангом, который в них нужен.
// Мёртвая щель внутри коридора подписана словами: там места нет ни одному
// рангу, и молчащая щель выглядела бы промахом руки.
function dragDraw() {
  const list = backlogView.rows || [];
  const cor = dragCorridor(drag.row);
  drag.list = list;
  drag.cor = cor;
  const rest = list.filter((row) => row.id !== drag.id);
  for (const row of rest) {
    const node = findKey(drag.card, row.id);
    if (node && (row.r > cor.high || row.r < cor.low)) node.classList.add("dimrow");
  }
  drag.aims = [];
  for (let gap = 0; gap <= rest.length; gap += 1) {
    drag.aims.push(gapAim(list, drag.id, gap));
  }
  let first = -1;
  let last = -1;
  drag.aims.forEach((aim, gap) => {
    if (!aim || aim.r === null) return;
    if (first < 0) first = gap;
    last = gap;
  });
  drag.aims.forEach((aim, gap) => {
    let cls = "gslot";
    let text = "";
    if (aim && aim.r !== null) {
      cls += " glive";
      text = "ранг " + aim.r;
    } else if (gap > first && gap < last) {
      cls += " gdead";
      text = aim.why;
    } else if (gap === first - 1) {
      cls += " gedge";
      text = dragCeil(cor, drag.id);
    } else if (gap === last + 1) {
      cls += " gedge";
      text = dragFloor(cor, drag.id);
    } else {
      // Дальше края коридора подписывать нечего: там уже приглушено.
      return;
    }
    // Щель это строка таблицы во всю ширину: между строками секции вставить
    // коробку больше нельзя, там живут только строки.
    const band = tblBand("tasks", "gslot-band");
    const mark = el("div", cls, text);
    band.cell.append(mark);
    const at = gap < rest.length ? findKey(drag.card, rest[gap].id) : null;
    drag.card.insertBefore(band.tr, at);
    drag.slots[gap] = mark;
    drag.marks.push(band.tr);
  });
  // Середины строк снимаются один раз, после расстановки щелей: дальше картинка
  // стоит на месте, и щель под пальцем считается по снимку, а не по едущей
  // раскладке.
  drag.mids = rest.map((row) => {
    const node = findKey(drag.card, row.id);
    if (!node) return 0;
    const box = node.getBoundingClientRect();
    return box.top + box.height / 2;
  });
  // Ярлык пересчёта едет в ячейке названия: приписать его самой строке нельзя,
  // в строке таблицы живут только ячейки.
  drag.tag = el("span", "dtag", "");
  // Ярлык стоит в ячейке названия своей строкой под ней, а не в ряду с
  // заголовком: в ряду он отнимал бы у названия половину колонки.
  const box = drag.tr.querySelector(".tt");
  (box || drag.tr).append(drag.tag);
}

function dragAim(y) {
  if (!drag || !drag.on) return;
  let gap = 0;
  while (gap < drag.mids.length && drag.mids[gap] < y) gap += 1;
  if (gap === drag.gap) return;
  if (drag.slots[drag.gap]) drag.slots[drag.gap].classList.remove("gnow");
  if (drag.slots[gap]) drag.slots[gap].classList.add("gnow");
  drag.gap = gap;
  drag.aim = drag.aims[gap];
  drag.tag.textContent = dragTagText(drag.cor, drag.aim);
}

// Строка отпущена: щели, приглушение и ярлык снимаются, а правка уезжает
// ручкой. Место, откуда строку взяли, правкой не считается, и запроса за ним
// не уходит.
function dragDrop() {
  if (!drag) return;
  if (!drag.on) {
    dragQuit();
    return;
  }
  const { project, id, cor, aim } = drag;
  dragQuit();
  if (!aim || aim.r === null) {
    sayResult(aim ? aim.why : "", true);
    return;
  }
  if (aim.r === cor.r) return;
  dragSend(project, id, cor, aim).catch(console.error);
}

function dragQuit() {
  if (!drag) return;
  clearTimeout(drag.hold);
  if (drag.on) dragClick = true;
  for (const mark of drag.marks) mark.remove();
  if (drag.tag) drag.tag.remove();
  drag.tr.classList.remove("dragrow");
  for (const row of drag.list || []) {
    const node = findKey(drag.card, row.id);
    if (node) node.classList.remove("dimrow");
  }
  drag = null;
}

// Правка уезжает тем же путём, что и от формы задачи: PATCH со слагаемыми, а
// дальше taskctl set --rank. Своей записи в docs/TASKS.md дашборд не заводит,
// порядок строк переставляет утилита.
async function dragSend(project, id, cor, aim) {
  sayResult(id + ": ценность " + cor.value + " -> " + aim.value + "...");
  const r = await api(taskPath(project, id), {
    method: "PATCH", body: { r_parts: [null, aim.value, null, null, null] },
  });
  if (!r.ok) {
    sayResult(r.body.error || "", true);
    return;
  }
  await refresh();
  const place = r.body.place || null;
  dragLit(id);
  sayDrop(dropText(id, cor, aim, place), () => { dragBack(project, id, cor, place); });
}

// Строка результата пишется по факту записи, а не по превью: в ответе ручки
// стоит свежая разбивка и соседи по доске. Разошёлся факт с превью, значит так
// и сказано: уехать могли соседи, а не решение человека.
function dropText(id, cor, aim, place) {
  const r = place ? place.r : aim.r;
  const parts = (place && place.r_parts) || [];
  const value = parts.length ? parts[1] : aim.value;
  let text = id + ": ценность " + cor.value + " -> " + value +
    ", ранг " + cor.r + " -> " + r;
  const band = (place && place.p) || rankBand(r);
  if (band !== rankBand(cor.r)) text += ", полоса " + rankBand(cor.r) + " -> " + band;
  return text + ". " + dropWhere(aim, place);
}

function dropWhere(aim, place) {
  if (!place) return "Место строки в очереди сервер не назвал";
  const above = place.above ? place.above.id : "";
  const below = place.below ? place.below.id : "";
  const same = above === (aim.above ? aim.above.id : "") &&
    below === (aim.below ? aim.below.id : "");
  const head = same ? "Строка встала " : "Доска успела уехать, строка встала ";
  if (above && below) return head + "между " + above + " и " + below;
  if (below) return head + "первой в очереди, перед " + below;
  if (above) return head + "последней в очереди, после " + above;
  return head + "единственной строкой в очереди";
}

// «Вернуть» кладёт обратно слагаемые, а не место: пока человек читал строку
// результата, соседи могли переехать сами. Ожидаемая разбивка едет вместе с
// правкой, и чужую руку ручка отбивает словами, а не затирает молча.
async function dragBack(project, id, cor, place) {
  sayResult("возврат ценности " + id + "...");
  const body = { r_parts: [null, cor.value, null, null, null] };
  if (place && place.r_parts) body.expect_r_parts = place.r_parts;
  const r = await api(taskPath(project, id), { method: "PATCH", body });
  if (!r.ok) {
    sayResult(r.body.error || "", true);
    return;
  }
  await refresh();
  dragLit(id);
  sayResult(id + ": ценность вернулась на " + cor.value + ", ранг " + cor.r);
}

// Что изменилось, видно в самой строке: слагаемые ранга и чип полосы
// подсвечиваются на пару секунд после броска. Строка при этом переехала на
// новое место, и найти её глазами иначе нечем.
function dragLit(id) {
  const node = findKey(document.getElementById("groups"), id);
  if (!node) return;
  node.classList.add("litrow");
  setTimeout(() => { node.classList.remove("litrow"); }, DRAG_LIT);
}

// Строка результата с кнопкой «Вернуть»: она живёт дольше обычного ответа на
// нажатие, потому что её читают и по ней решают, а не просто замечают.
const DROP_LIFE = 12000;

function sayDrop(text, undo) {
  if (resultToast) resultToast.dismiss();
  resultToast = null;
  const body = el("div", "ft");
  body.append(el("b", "", text));
  const back = el("button", "btn btn-sm", "Вернуть");
  back.type = "button";
  back.addEventListener("click", (ev) => {
    ev.stopPropagation();
    back.disabled = true;
    undo();
  });
  resultToast = toast({ parts: [body, back], body, life: DROP_LIFE, cls: "res" });
}

// Слагаемые ранга по RANKING.md: имя, короткая подсказка и допустимые
// значения. Правятся по месту списком, а не полем ввода: на телефоне это
// родной барабан, и невозможного значения в списке просто нет. Сумму R и
// бакет P считает taskctl, экран их не пересчитывает.
const RANK_PARTS = [
  {
    name: "Серьёзность",
    why: "ущерб, если не делать: 75 пользоваться нельзя, 50 основной сценарий сломан, 25 заметное трение, 0 косметика",
    values: [0, 25, 50, 75],
  },
  {
    name: "Ценность",
    why: "чистая польза: 8-10 ради этого и затевается, 4-7 ощутимое улучшение, 1-3 небольшое удобство",
    values: [0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10],
  },
  {
    name: "Неопределённость",
    why: "сколько разбираться до исполнения: 5 нужен спайк, 3 развилки в деталях, 1 всё ясно; стык сервер-клиент это минимум 3",
    values: [0, 1, 2, 3, 4, 5],
  },
  {
    name: "Поправка на баг",
    why: "5, если чинится дефект или регресс, а не делается новое",
    values: [0, 5],
  },
  {
    name: "Рычаг",
    why: "будущая работа: 5 разблокирует чужую, 4 ускоряет свою и агента, 2 небольшая своя польза",
    values: [0, 1, 2, 3, 4, 5],
  },
];
const COST_VALUES = ["-", "S", "M", "L", "XL"];
const TYPE_VALUES = ["task", "bug", "LLD"];

// Поправка на баг живёт у дефекта, а не у новой работы: у типа task слагаемое
// не ставится. Тот же отказ теми же словами держит ручка PATCH, клиент только
// не даёт зря сходить на сервер.
const BUG_PART_REFUSAL = "поправка на баг у типа task не ставится: по RANKING.md " +
  "она про дефект или регресс, а не про новую работу; смени тип на bug";

// Бакет P рукой не выбирается, и экран обязан это объяснять: иначе отсутствие
// списка читается как забытое поле. Объяснение висит подсказкой на самом
// бакете, надписью под рядом метаданных оно стояло указкой.
const P_HINT = "Бакет считается из суммы ранга, рукой не ставится: правьте слагаемые.";

// Что случится по кнопке остановки: сессия снимается, а состояние остаётся на
// диске. Одни и те же слова стоят на экране задачи, агента и в чате.
const STOP_TIP = "Сессия агента будет завершена, при возобновлении состояние агента " +
  "возобновится с диска.";

// Подсказка по наведению на чипе или кнопке: короткое пояснение, за которым
// экран не обязан держать отдельную надпись.
function withTip(node, text) {
  node.title = text;
  return node;
}

// Черновик экрана задачи: пока в форме есть правка, живое обновление её не
// затирает. Экран один, поэтому и черновик один.
// edit это режим правки экрана задачи: по умолчанию задача открывается на
// просмотр, а карандаш рядом с названием пускает в поля. Признак живёт вместе с
// черновиком правки и снимается сохранением.
const taskDraft = { id: "", dirty: false, seen: "", edit: false };

// Ключ узла экрана задачи в списке: по нему обновление находит нарисованное и
// сверяет отпечаток, вместо того чтобы собирать экран заново.
const TASK_PKEY = "task-page";

// Правки строки, зависимостей и файла: всё уходит в API, а тот зовёт taskctl.
// Ответ показывается словами, и удача, и отказ утилиты (кривая разбивка
// ранга, цикл зависимостей). После удачной правки экран перечитывает данные:
// порядок строк на доске выводится из ранга и мог поехать.
async function sendTaskEdit(path, method, body) {
  const r = await api(path, { method, body });
  sayResult(apiSaid(r), !r.ok);
  if (r.ok) await refresh();
  return r.ok;
}

// Слова ответа ручки одной строкой: удача, отказ и приписка утилиты. Собраны
// они в одном месте, потому что говорят ими все правки доски.
function apiSaid(r) {
  const said = r.body.message || r.body.error || "";
  return r.ok && r.body.note ? said + " (" + r.body.note + ")" : said;
}

function taskPath(project, id, tail) {
  return "/api/projects/" + encodeURIComponent(project) + "/tasks/" + encodeURIComponent(id) + (tail || "");
}

// Сохранение черновика: строка и файл задачи правятся одной формой и уезжают
// одной кнопкой. Ручек на сервере по-прежнему две (PATCH на строку, PUT на
// файл), склейка живёт здесь. Не прошла первая, вторая не идёт, а черновик
// остаётся в полях: половина сохранённой правки хуже отказа.
async function saveTaskDraft(project, id, patch, text) {
  sayResult("сохранение " + id + "...");
  const said = [];
  const call = async (path, method, body) => {
    const r = await api(path, { method, body });
    said.push(r.body.message || r.body.error || "");
    if (r.ok && r.body.note) said.push(r.body.note);
    return r.ok;
  };
  if (patch && !await call(taskPath(project, id), "PATCH", patch)) {
    sayResult(said.join("; "), true);
    return false;
  }
  if (text !== null && !await call(taskPath(project, id, "/file"), "PUT", { text })) {
    sayResult(said.join("; "), true);
    return false;
  }
  sayResult(said.join("; "));
  taskDraft.dirty = false;
  // Сохранение возвращает просмотр: правка кончилась, и держать поля открытыми
  // незачем (замечание 1 девятого круга POC).
  taskDraft.edit = false;
  await refresh();
  return true;
}

async function addDep(project, id, dep) {
  sayResult(id + " после " + dep + "...");
  return sendTaskEdit(taskPath(project, id, "/deps"), "POST", { id: dep });
}

async function dropDep(project, id, dep) {
  sayResult("снятие зависимости " + id + " от " + dep + "...");
  return sendTaskEdit(taskPath(project, id, "/deps/" + encodeURIComponent(dep)), "DELETE");
}

async function makeTaskFile(project, id) {
  sayResult("заведение файла задачи " + id + "...");
  return sendTaskEdit(taskPath(project, id, "/file"), "POST", {});
}

// Значение, правимое по месту: подпись и список допустимых значений. Выбор
// уходит в черновик формы, а не на сервер: сервер зовёт одна кнопка.
function pickField(label, values, cur, onPick) {
  const wrap = el("label", "pick");
  if (label) wrap.append(el("span", "pl", label));
  const sel = el("select");
  for (const v of values) {
    const opt = el("option", "", String(v));
    opt.value = String(v);
    opt.selected = String(v) === String(cur);
    sel.append(opt);
  }
  sel.addEventListener("change", () => { onPick(sel.value); });
  wrap.append(sel);
  return wrap;
}

function depRow(project, id, side, dep) {
  const row = el("div", "drow");
  row.append(el("span", "id", dep.id));
  row.append(withFull(el("span", "dt", dep.title || dep.note || ""), dep.title || dep.note || ""));
  if (dep.section) {
    row.append(el("span", "chip" + (dep.sect === "in-progress" ? " c-run" : ""), dep.section));
  }
  if (dep.r) {
    const rank = el("span", "rank");
    rank.append(el("b", "", String(dep.r)));
    row.append(rank);
  }
  const drop = el("button", "btn btn-sm", "Снять");
  drop.addEventListener("click", (ev) => {
    ev.stopPropagation();
    // Вторая сторона это та же зависимость наоборот: снимается она у той
    // задачи, в чьём заголовке стоит маркер [после ...].
    const call = side === "after" ? dropDep(project, id, dep.id) : dropDep(project, dep.id, id);
    call.catch(console.error);
  });
  row.append(drop);
  row.addEventListener("click", () => { goKeepingChat(project + "/" + dep.id); });
  return row;
}

// Карточка зависимостей в обе стороны по макету «02 Задача»: кого ждёт задача
// и кто ждёт её. Обе стороны живут на доске одним маркером [после ...],
// поэтому вторая сторона это обратный поиск, а не вторая запись. Названы они
// словами, а не «После» и «Держит»: от тех читателю приходилось достраивать,
// кто кого ждёт.
// Блок «Связи» (круг 2 POC DK-470, доработка по замечаниям): у каждой строки
// виден тип артефакта (LLD, цель, задача), у связанной задачи род связи
// (после, держит) и дата закрытия, где они есть у источника. Первым идёт LLD
// самой задачи, за ним остальные дизайны из текста, ниже задачи по номеру;
// каждая строка это дорога на форму документа или экран задачи.

// LINKS_SHOW это видимая часть длинного списка связей: у цели упоминаний
// бывают десятки, остальное разворачивает кнопка «ещё N». Хвост из одной
// строки не прячется, кнопка заняла бы столько же места.
const LINKS_SHOW = 8;

// Строка связанной задачи. Дорога у неё двоякая: у записи накопителя своей
// формы задачи нет, и ведёт она на экран черновика (тем же поворотом, каким
// уходит туда упоминание в реплике), у прочих на форму задачи. За мёртвым ID
// не стоит ничего, и дороги у него нет вовсе: клик привёл бы на отказ «нет
// строки», а связь до клика выглядела бы живой.
function linkTaskRow(project, t) {
  const row = el("div", "srow" + (t.gone ? " lgone" : " clicky"));
  row.append(el("span", "chip", t.kind || "задача"));
  const mid = el("div", "lmid");
  mid.append(el("span", "id", t.id));
  const title = t.title || t.note || "";
  mid.append(withFull(el("span", t.title ? "st" : "st lnone", title), t.id + " " + title));
  row.append(mid);
  const meta = [];
  if (t.rel) meta.push(t.rel);
  if (t.closed) meta.push("закрыта " + t.closed);
  if (meta.length) row.append(el("span", "lmeta", meta.join(", ")));
  if (!t.gone) {
    const to = t.draft ? project + "/draft/" + t.id : project + "/" + t.id;
    row.addEventListener("click", () => { goKeepingChat(to); });
  }
  return row;
}

function linksCard(project, links) {
  const card = el("div", "card dcard");
  card.append(el("div", "dhead", "Связи"));
  for (const doc of links.lld || []) {
    const row = el("div", "srow clicky");
    // Подпись у чипа одна: «LLD задачи» не влезал в колонку типа. Свой дизайн
    // от упомянутого отличают порядок (свой стоит первым) и подсказка.
    row.append(withTip(el("span", "chip", "LLD"),
      doc.own ? "LLD самой задачи" : "LLD, упомянутый в постановке"));
    row.append(withFull(el("span", "st", doc.title || doc.file), doc.title || doc.file));
    row.addEventListener("click", () => { goKeepingChat(project + "/doc/" + doc.file); });
    card.append(row);
  }
  const tasks = links.tasks || [];
  if (tasks.length) {
    const box = el("div", "lrel");
    const head = tasks.length > LINKS_SHOW + 1 ? tasks.slice(0, LINKS_SHOW) : tasks;
    for (const t of head) box.append(linkTaskRow(project, t));
    if (head.length < tasks.length) {
      const more = el("button", "lmore", "ещё " + (tasks.length - head.length));
      more.addEventListener("click", () => {
        more.remove();
        for (const t of tasks.slice(head.length)) box.append(linkTaskRow(project, t));
      });
      box.append(more);
    }
    card.append(box);
  }
  return card;
}

function depsCard(project, id, after, blocks) {
  const card = el("div", "card dcard");
  card.append(el("div", "dhead", "Заблокировано задачами"));
  if (!after.length) card.append(el("div", "empty", "Никого не ждёт."));
  for (const dep of after) card.append(depRow(project, id, "after", dep));

  const add = el("div", "dadd");
  const inp = el("input");
  inp.placeholder = "DK-NNN";
  inp.setAttribute("aria-label", "ID задачи, после которой делается эта");
  const btn = el("button", "btn btn-sm", "Добавить");
  const send = () => {
    const dep = inp.value.trim().toUpperCase();
    if (dep) addDep(project, id, dep).catch(console.error);
  };
  btn.addEventListener("click", send);
  inp.addEventListener("keydown", (ev) => { if (ev.key === "Enter") send(); });
  add.append(inp, btn);
  card.append(add);

  card.append(el("div", "dhead", "Блокирует выполнение задач"));
  if (!blocks.length) card.append(el("div", "empty", "Её никто не ждёт."));
  for (const dep of blocks) card.append(depRow(project, id, "blocks", dep));
  return card;
}

// Панель файла задачи: текст правится прямо в поле, своей кнопки сохранения у
// панели нет, она одна на всю форму. Файл кладёт сам add вместе со строкой, и
// кнопка «Завести файл» достаётся только дыре: строке до рубежа либо файлу,
// снятому руками (taskctl file заодно чинит ссылку в строке доски).
// Служебные разделы файла задачи. Файл разрастается ходом работ (у DK-397
// журнал с итогом и вклейками занимали почти половину из 655 строк), и на
// форме такие разделы сворачиваются строками-аккордеонами. Список имён живёт
// одним местом: точные имена плюс префикс вклеек черновиков. Хранение не
// трогается, сворачивает только просмотр, правка видит файл целиком.
const FOLD_SECTIONS = ["Журнал", "Ход работы", "Итог", "Входящие", "Грумминг"];
const FOLD_SECTION_PREFIX = "Из черновика";

function foldSection(name) {
  const said = String(name || "").trim();
  return FOLD_SECTIONS.includes(said) || said.startsWith(FOLD_SECTION_PREFIX);
}

// Разрез файла на разделы по заголовкам второго уровня: шапка до первого
// «## » остаётся куском без имени и не сворачивается никогда.
function mdSections(text) {
  const out = [];
  let cur = { name: "", lines: [] };
  for (const ln of String(text || "").split("\n")) {
    const m = /^##\s+(.+?)\s*$/.exec(ln);
    if (m) {
      if (cur.lines.length) out.push(cur);
      cur = { name: m[1], lines: [] };
    }
    cur.lines.push(ln);
  }
  if (cur.lines.length) out.push(cur);
  return out;
}

// Объём раздела словами: по нему видно, что прячется за свёрнутой строкой.
function lineWord(n) {
  const t = n % 10;
  const h = n % 100;
  if (t === 1 && h !== 11) return n + " строка";
  if (t >= 2 && t <= 4 && (h < 12 || h > 14)) return n + " строки";
  return n + " строк";
}

// Просмотр файла по разделам: постановка развёрнута, служебное свёрнуто до
// строки с именем и объёмом, разворот по клику. Незнакомый раздел по
// умолчанию развёрнут: свёртка не должна прятать неизвестное молча. Тело
// свёрнутого рендерится лениво, первым разворотом: журнал в полторы сотни
// строк не стоит разметки, пока на него не смотрят.
function mdRenderSections(text) {
  const box = el("div", "fsecs");
  let plain = [];
  const flushPlain = () => {
    if (!plain.length) return;
    box.append(mdRender(plain.join("\n")));
    plain = [];
  };
  for (const sec of mdSections(text)) {
    if (!sec.name || !foldSection(sec.name)) {
      plain = plain.concat(sec.lines);
      continue;
    }
    flushPlain();
    const fold = el("div", "ffold fold");
    const top = foldHead(sec.name, lineWord(sec.lines.length));
    const car = foldCar();
    top.append(car);
    const bodyEl = el("div", "ffoldb");
    bodyEl.hidden = true;
    let drawn = false;
    top.addEventListener("click", () => {
      if (!drawn) {
        bodyEl.append(mdRender(sec.lines.join("\n")));
        drawn = true;
      }
      bodyEl.hidden = !bodyEl.hidden;
      car.set(!bodyEl.hidden);
      fold.classList.toggle("open", !bodyEl.hidden);
    });
    fold.append(top, bodyEl);
    box.append(fold);
  }
  flushPlain();
  return box;
}

// Блок постановки: по умолчанию просмотр разметки, правка по карандашу
// (замечание 1 девятого круга POC). Сырой текст в поле ввода читается хуже
// собранного: постановка длинная, со списками и таблицами, и глазами её берут
// куда чаще, чем правят. Разметчик свой, тот же, что в ленте: внешних скриптов
// CSP дашборда не пускает, а тащить библиотеку ради шести правил незачем.
// Текст в дерево кладётся только узлами (createTextNode), никакого innerHTML,
// поэтому угловые скобки из постановки остаются текстом и разметкой не станут.
function filePanel(project, id, detail, form, touch, edit, canMake) {
  const card = el("div", "card fpanel");
  const head = el("div", "fhead");
  // Путь файла постановки в шапке блока не нужен: человек читает описание, а
  // не разбирается, где оно лежит (замечание 12). Шапка остаётся ради кнопки
  // «Завести файл» у задачи без постановки.
  head.append(el("span", "gap"));
  const body = el("div", "fbody");
  card.append(body);
  // Шапка встаёт в разметку только с живой кнопкой: после переезда карандаша,
  // чтения и действий в командную панель у задачи с файлом в ней не осталось
  // ничего, и над описанием стояла пустая полоса с разделителем. Прятать её
  // стилями нельзя, рамка с отступами осталась бы на экране, поэтому узла
  // просто нет (та же мера, что у полосы действий).
  const placeHead = () => {
    const live = Array.from(head.children).some(
      (n) => !n.hidden && !String(n.className || "").split(" ").includes("gap"));
    if (!live) head.remove();
    else if (!head.parentNode) card.prepend(head);
  };
  placeHead();

  if (!detail.file) {
    // Дыра чинится только у строки доски: у записи накопителя пропавший файл
    // это след груминга, и заводить его заново нечем.
    if (canMake) {
      const make = el("button", "btn btn-sm", "Завести файл");
      make.addEventListener("click", () => { makeTaskFile(project, id).catch(console.error); });
      head.append(make);
      placeHead();
    }
    if (detail.doc) {
      // Ссылка строки ведёт в другой документ, обычно LLD: постановка есть,
      // и панель показывает её собранной, а не рапортует о дыре. Правится
      // такой документ на своей форме, файл задачи заводится той же кнопкой.
      const view = el("div", "fview");
      view.dataset.file = "docs/" + detail.doc;
      view.replaceChildren(mdRender(detail.docText || ""));
      body.append(view);
    } else {
      body.append(el("div", "empty", detail.note || "файла задачи нет"));
    }
    return card;
  }
  // Разворот стал режимом чтения задачи, и кнопка его переехала к карандашу в
  // строку статуса: два переключателя вида стоят рядом, а не по разным углам
  // экрана (замечание 2). Сама ручка отдаётся наружу, кнопку рисует экран.
  // Пара к ней живёт тут: развёрнутая постановка накрывает собой всю страницу
  // вместе со строкой статуса, и включённый режим чтения выключать было нечем
  // (замечание 6 четырнадцатого круга POC). Кнопка стоит в углу самой
  // постановки и видна только в режиме чтения.
  const out = el("button", "fwide");
  out.hidden = true;
  out.title = "Выйти из режима чтения";
  out.setAttribute("aria-label", out.title);
  out.append(icon("close"));
  out.addEventListener("click", () => { if (card.onWideOff) card.onWideOff(); });
  head.append(out);
  card.onWideOff = null;
  card.setWide = (on) => {
    card.classList.toggle("wide", on);
    out.hidden = !on;
    placeHead();
  };

  const ta = el("textarea");
  ta.value = form.text;
  ta.setAttribute("aria-label", "текст файла задачи " + id);
  ta.addEventListener("input", () => { form.text = ta.value; touch(); });
  // Просмотр: из этого блока берётся выделение, которое уезжает агенту
  // контекстом, поэтому у него свой класс и свой признак файла.
  const view = el("div", "fview");
  view.dataset.file = detail.file || "docs/tasks/" + id + ".md";
  const paint = () => {
    if (String(form.text || "").trim()) view.replaceChildren(mdRenderSections(form.text));
    else view.replaceChildren(el("div", "empty", "файл задачи пуст"));
  };
  paint();
  body.append(view, ta);
  // Переключение режима идёт по месту, а не перерисовкой экрана: перерисовка
  // над тронутой формой запрещена (она стёрла бы несохранённое), и карандаш на
  // ней молчал бы вовсе, что неотличимо от сломанной кнопки. Заодно так видно
  // несохранённую правку: просмотр собирается из формы, а не из того, что
  // лежит на диске.
  card.setEdit = (on) => {
    view.hidden = on;
    ta.hidden = !on;
    if (!on) paint();
  };
  card.setEdit(Boolean(edit));
  return card;
}

// Слепок показанного: по нему видно, что строка или файл уехали под руками.
// Сравнивается ровно то, что нарисовано на экране.
function taskSeen(detail) {
  const row = detail.row || {};
  return JSON.stringify([row.title, row.type, row.cost, row.p, row.r, row.r_parts,
    row.section, row.fail, row.block, row.notes, row.moved, detail.text || "", detail.file || "",
    detail.doc || ""]);
}

// Пометка «задача обновилась»: живое обновление при открытой правке молчаливо
// подменяло значения полей, и это признано дефектом на пользовательской
// проверке. Свежие данные ждут кнопки, ввод остаётся на месте.
function taskStale(project, works, id) {
  if (document.getElementById("tstale")) return;
  const box = el("div", "tstale");
  box.id = "tstale";
  box.append(el("span", "", "задача обновилась, перечитать"));
  const btn = el("button", "btn btn-sm", "Перечитать");
  btn.addEventListener("click", () => {
    // Перечитывает рука: правка в форме при этом теряется, и решает это
    // пользователь, а не таймер фокуса.
    taskDraft.dirty = false;
    renderTask(project, works, id).catch(console.error);
  });
  box.append(btn);
  document.getElementById("groups").prepend(box);
}

// Рубеж формы, тот же, что у ручки: поправка на баг не про новую работу, а
// пустой текст затёр бы постановку. Отказ виден до похода на сервер.
function draftRefusal(form, text) {
  if (form.type === "task" && Number(form.parts[3]) === 5) return BUG_PART_REFUSAL;
  if (text !== null && !text.trim()) return "пустой текст затёр бы постановку файла задачи";
  if (!form.title.trim()) return "заголовок задачи пустым не бывает";
  return "";
}

// Экран задачи по макету DK-216 («02 Задача»): шапка со строкой доски,
// карточка действия, ранг со слагаемыми и зависимости в обе стороны. Строку
// правит taskctl на стороне сервера, поэтому правится ровно то, что есть в
// строке: заголовок, тип, слагаемые ранга и цена; порядок строк выводится из
// ранга, перетаскивания мимо ранга нет.
// Заказ агенту дословно, той же строкой, что унесёт headless-сессии runPrompt
// на сервере (row.order). Подсказка кнопки читает готовое поле, а не
// пересказывает его ветвление вторым разбором на клиенте: второй разбор рано
// или поздно разошёлся бы с настоящим заказом (DK-286). Проверенная строка с
// пользовательской приёмкой закрывается прямо с экрана командой taskctl, без
// сессии агента, и у неё нет заказа вовсе (closeFromCheck).
function orderHint(order, accept, sect, id) {
  if (sect === "check" && accept === "user") {
    return "Закроется командой taskctl close, без сессии агента: приёмка пользовательская, " +
      "человек уже принял работу глазами.";
  }
  if (!order) return "";
  return "Конвейер получит заказ «" + order + "» в tmux-сессии task-" + id + ".";
}

// Надписи под полосой действий больше нет вовсе. Она пересказывала устройство
// («конвейер получит заказ в tmux-сессии task-DK-452, поедет на такую-то
// подписку») там, где человек жмёт кнопку с понятной подписью, и стояла
// плашкой в самом начале экрана (замечание пользователя). Заказ и подписка
// никуда не делись: заказ остался подсказкой самой кнопки, а подписку
// называет её выпадашка.

// Полоса действий задачи: у живой работы её экран и стоп, у стоящей действие
// по статусу строки теми же словами, что и на доске. Собрана отдельной
// функцией, а не внутри экрана: тексты действий держат тесты, и смотреть им
// на весь renderTask ради одной полосы незачем.
// Кнопка полосы действий: значок и слово рядом. На телефоне слово прячут
// стили, и полоса становится рядом значков под палец, поэтому подпись уезжает
// ещё и в aria-label, иначе с телефона кнопка остаётся без имени.
function barBtn(cls, label, ico) {
  const btn = el("button", cls);
  const lb = el("span", "lb", label);
  btn.append(icon(ico), lb);
  btn.setAttribute("aria-label", label);
  // Подпись правится по месту: на форме заведения она меняется переключателем,
  // а пересобранная кнопка теряла бы обработчик вместе с погашенным видом.
  btn.rename = (name) => {
    lb.textContent = name;
    btn.setAttribute("aria-label", name);
  };
  return btn;
}

// Продолжение работы задачи: сервер сам решает, будить живой разговор каналом
// или поднимать резюм, а не нашедший разговора откатывается на прежний запуск
// конвейера. Экран после удачи остаётся на месте, а кнопка чата задачи
// помечается активностью.
async function continueTask(project, id) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/tasks/" + encodeURIComponent(id) + "/continue", { method: "POST", body: {} });
  if (!r.ok) {
    sayResult(r.body.error || "продолжить не вышло", true);
    return;
  }
  // Сервер сам решил, что делать: разбудил живую сессию, поднял резюм или
  // завёл первый чат. Экрану остаётся сказать это словами и пометить, что у
  // задачи пошла активность: панель тут не открывается по той же причине, что
  // и на запуске, разговор человека этим переходом рвался бы.
  if (r.body.message) sayResult(r.body.message);
  markRunLive(project, id, r.body.session || "");
  await refresh();
}

// Полоса собирается по самой строке, а не по списку работ: признаки идущего
// хода приезжают со строкой и с формы, и с доски (tasks.go, handleTask), и
// кнопку тут выбирает то же правило, что в строке доски.
function taskActions(project, id, row) {
  const out = [];
  const isGoal = /^Цель:/.test(row.title);
  // Входа в разговор с экрана задачи больше нет (POC ветки poc-chat): окно
  // чатов открывает один значок в шапке дашборда, и открытое с задачи оно
  // фильтрует список по ней. Кнопка рядом с действиями строки заводила ещё
  // одну дорогу в то же место и путала разговор с работой.
  if (rowOurRun(row)) {
    // Значком, без подписи: та же кнопка в списке задач стоит так же
    // (rowAction), и подпись рядом с иконкой на форме была лишним расхождением
    // вида одной и той же остановки (приёмка 2026-09-05).
    const stop = el("button", "btn btn-danger btn-ico rstop");
    stop.append(icon("i-stop"));
    stop.setAttribute("aria-label", "Стоп");
    // Последствия остановки живут подсказкой на самой кнопке: надписью рядом
    // они стояли указкой над всей полосой.
    withTip(stop, STOP_TIP);
    stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
    out.push(stop);
    return out;
  }
  // Ход идёт, но сессия не наша: снимать нечем, а вводная продолжения уехала бы
  // в живой ход посторонней сессии. Подписи «ведёт другая сессия» тут больше
  // нет: она врала про чужую машину (замечание пользователя).
  if (row.run_busy) return out;
  // Плашки про ненайденного исполнителя тут больше нет. Форму она портила
  // целым абзацем, а говорила неправду: человек вёл задачу из дашборда и
  // читал, что работа идёт где-то ещё (решение пользователя). Запуск такой
  // строке возвращён: живой работы за ней нет, и второму исполнителю взяться
  // неоткуда.
  const label = actionLabel(row.sect);
  if (row.after && row.after.length) {
    // Заблокированную маркером задачу конвейер брать не должен: кнопка стоит
    // погашенной с причиной, а не пропадает с полосы.
    const wait = barBtn("btn", label, "i-play");
    wait.disabled = true;
    // Чего ждёт задача, сказано подсказкой кнопки и карточкой зависимостей
    // ниже. Третьей строкой то же самое стояло плашкой поперёк экрана.
    out.push(withTip(wait, "сначала " + row.after.join(", ")));
    return out;
  }
  // Удачный запуск ведёт на экран этой работы: до DK-286 нажатие оставляло
  // человека на прежнем месте, а работа уходила в tmux-сессию незримо.
  // Проверенная строка с пользовательской приёмкой закрывается тут же, без
  // сессии агента, и вести после неё некуда.
  const closesWithoutSession = row.sect === "check" && row.accept === "user";
  const afterOk = closesWithoutSession ? "" : project + "/" + id;
  if (label === ACTION_BY_SECT["in-progress"]) {
    // Продолжение работы переехало в чат отдельной кнопкой рядом с отправкой
    // (замечание 10): продолжают её оттуда же, откуда разговаривают, а полоса
    // с одной кнопкой на экране не нужна. Цель тут не исключение: её
    // диспетчерскую сессию продолжают той же кнопкой, и до этого круга полоса
    // у цели оставалась стоять.
    return out;
  }
  const pin = checkPin(row);
  // Выбор яруса тот же, что и у строки списка: экран задачи и доска не должны
  // предлагать разное.
  // Подсказка строки Check говорит, что закрытие идёт следом за проверкой: из
  // подписи кнопки это слово ушло ради ширины, и сказать о нём должно место
  // рядом. Спрашивается она по секции, а не по прикреплённой подписке: без
  // подписок на машине приколоть нечего, а закрывать строку всё равно закроют.
  out.push(runControl(project, id, (name) => barBtn("btn btn-acc", name, "i-play"), label, isGoal,
    row.sect === "check" ? checkTip(row) : orderHint(row.order, row.accept, row.sect, id),
    afterOk, pin, null,
    isGoal ? { list: harnessTiers(), now: RUN_TIER }
      : { list: [TIER_VERDICT].concat(harnessTiers()), now: TIER_VERDICT }));
  return out;
}

// Состав цели сабтасками (макет «06 Цель»). Источник это раздел «Задачи цели»
// файла цели плюс строки доски и архива, всё это сводит сервер. Живые задачи
// стоят сверху, закрытые свёрнуты в одну строку: у долгой цели их набирается
// больше, чем помещается на экран, а смотрят в состав ради того, что ещё не
// сделано.
const SECT_CHIP = {
  "in-progress": "c-run",
  check: "c-check",
  blocked: "c-block",
  archive: "c-check",
};

const MONTHS_SHORT = ["янв", "фев", "мар", "апр", "мая", "июн",
  "июл", "авг", "сен", "окт", "ноя", "дек"];

// Дата закрытия днём и месяцем: год в строке состава занимает место, а
// закрытая на этой неделе задача от закрытой в прошлом году отличается днём.
function shortDate(iso) {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso || "");
  if (!m) return iso || "";
  return String(Number(m[3])) + " " + MONTHS_SHORT[Number(m[2]) - 1];
}

function goalTaskRow(project, task) {
  // Открывается строка нажатием, и курсор об этом говорит: класс clicky несёт
  // указатель и подсветку, без него строка ловила клик, а под мышью выглядела
  // текстом (замечание пользователя). Закрытая задача уехала в архив, экрана у
  // неё нет, и указателя тоже.
  const opens = task.draft || (!task.done && task.section);
  const row = el("div", "srow" + (task.done ? " done" : "") + (opens ? " clicky" : ""));
  row.append(el("span", "id", task.id));
  // Заголовок берётся со строки доски или из архива, а судьба из нарезки
  // остаётся под ним подсказкой: строка доски говорит, что это за задача, а
  // нарезка, зачем она цели.
  const st = el("span", "st", task.title || task.fate || "");
  // Полный заголовок и судьба задачи в цели идут одной подсказкой: строка
  // режется кромкой, и без заголовка целиком подсказка объясняла бы обрезок.
  const said = [task.title || task.fate || "", task.title ? task.fate : ""]
    .filter(Boolean).join(". ");
  withFull(st, said);
  row.append(st);
  const meta = el("span", "sm");
  if (task.r) meta.append(el("span", "rank", String(task.r)));
  if (task.done) {
    meta.append(el("span", "chip c-check", "закрыта " + shortDate(task.closed)));
  } else if (task.section) {
    meta.append(el("span", "chip " + (SECT_CHIP[task.sect] || ""), task.section));
  }
  // Задача, которая пока лежит записью накопителя, помечена словом, а не
  // объяснением: «черновик» это её этап, и экран записи открывается той же
  // строкой.
  if (task.draft) meta.append(el("span", "chip", "черновик"));
  if (task.note) meta.append(el("span", "stale", task.note));
  row.append(meta);
  if (opens) {
    const to = task.draft ? project + "/draft/" + task.id : project + "/" + task.id;
    row.addEventListener("click", () => { goKeepingChat(to); });
  }
  return row;
}

function goalCounts(c) {
  const parts = [];
  if (c.closed) parts.push(c.closed + " закрыто");
  if (c.running) parts.push(c.running + " в работе");
  if (c.ahead) parts.push(c.ahead + " впереди");
  return parts.join(", ");
}

async function goalComposition(project, id, into) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/goals/" + encodeURIComponent(id) + "/tasks");
  const card = el("div", "card comp");
  const head = el("div", "chd");
  head.append(el("b", "", "Задачи цели"));
  if (!r.ok) {
    card.append(head, el("div", "empty", r.body.error || "состав цели не прочитался"));
    into.append(card);
    return;
  }
  const tasks = r.body.tasks || [];
  const counts = r.body.counts || {};
  head.append(el("span", "cnt", goalCounts(counts)));
  const bar = el("div", "prog");
  const total = counts.total || 0;
  for (const [n, cls] of [[counts.closed, "done"], [counts.running, "run"]]) {
    const seg = el("i", cls);
    seg.style.width = (total ? Math.round((n || 0) * 100 / total) : 0) + "%";
    bar.append(seg);
  }
  head.append(bar, el("span", "gap"));
  head.append(el("span", "stale", "нарезка из " + (r.body.file || "файла цели")));
  card.append(head);
  // Пустота различима: сервер называет её словами, и они честнее пустой
  // карточки, из которой не видно, нарезана цель или файл её не найден.
  if (r.body.note) {
    card.append(el("div", "empty", r.body.note));
    into.append(card);
    return;
  }
  const live = tasks.filter((t) => !t.done);
  const done = tasks.filter((t) => t.done);
  for (const task of live) card.append(goalTaskRow(project, task));
  if (done.length) {
    const fold = el("div", "more", "Ещё " + done.length + " " +
      plural(done.length, "закрытая задача", "закрытые задачи", "закрытых задач"));
    const box = el("div", "");
    box.hidden = true;
    for (const task of done) box.append(goalTaskRow(project, task));
    fold.addEventListener("click", () => {
      box.hidden = !box.hidden;
      fold.textContent = box.hidden
        ? "Ещё " + done.length + " " + plural(done.length, "закрытая задача", "закрытые задачи", "закрытых задач")
        : "Свернуть закрытые";
    });
    card.append(fold, box);
  }
  into.append(card);
}

// Одна форма на три экрана: задачу, черновик и заведение. Разметка у них общая
// (крошки, шапка с номером и заголовком, строка статуса с кнопками режимов,
// полоса действий, ранг, описание и зависимости), а расходятся они тем, какие
// блоки включены и что делает кнопка действия. Порознь эти три отрисовки
// разъезжались отступами и кнопками, и правка одной до двух других не доходила.
//
// cfg.has перечисляет включённые блоки, cfg.chips и cfg.actions приносят
// готовые пометки и кнопки экрана, cfg.check называет рубеж формы, а сохранение
// с отменой приходят обработчиками: что уезжает на сервер, знает сам экран.
function formPage(cfg) {
  const has = cfg.has || {};
  const form = cfg.form || {};
  const page = el("div", "tpage");
  const out = { page };

  const crumb = el("div", "crumb");
  (cfg.crumb || []).forEach((step, i) => {
    if (i) crumb.append(el("span", "crumb-sep", "/"));
    // Шаг без дороги это не ссылка, а подпись: последняя крошка документа несёт
    // его путь, и вести ей некуда. Прежде путь стоял припиской в шапке
    // страницы, а её больше нет.
    const back = el("span", step.go ? "crumb-back" : "crumb-here", step.text);
    if (step.go) back.addEventListener("click", step.go);
    crumb.append(back);
  });
  // Крошек может не быть вовсе (экран задачи: дорога на доску живёт названием
  // проекта в шапке): пустая строка встала бы над заголовком своим отступом.
  if (crumb.children.length) page.append(keyed(crumb, cfg.key + "-crumb"));
  for (const node of cfg.lead || []) page.append(node);

  // Шапка: крупный номер и заголовок. Полем он правится там, где лежит строкой
  // доски; у черновика заголовок это первая строка записи, и правят её в самом
  // тексте.
  const head = el("div", "thline");
  if (cfg.num) head.append(el("span", "idbig", cfg.num));
  let title = null;
  if (has.title) {
    title = el("textarea", "tedit" + (cfg.titleTall ? " tbig" : ""));
    title.value = form.title || "";
    title.rows = 1;
    if (cfg.titleHint) title.placeholder = cfg.titleHint;
    title.setAttribute("aria-label", cfg.titleLabel || "заголовок задачи");
    // Высота по содержимому: заголовок в одну строку держал поле на три, и
    // место уходило в пустоту. Считается она после вставки в дерево, потому что
    // до неё scrollHeight равен нулю.
    const fit = () => {
      title.style.height = "auto";
      if (title.scrollHeight) title.style.height = title.scrollHeight + "px";
    };
    title.addEventListener("input", () => { form.title = title.value; fit(); touch(); });
    setTimeout(fit, 0);
    head.append(title);
  } else {
    head.append(el("div", "tedit ro dtitle", cfg.titleText || cfg.num || ""));
  }
  page.append(keyed(head, cfg.key + "-head"));

  const chips = el("div", "tchips");
  for (const chip of cfg.chips || []) chips.append(chip);
  if (has.type) {
    out.typePick = pickField("тип", TYPE_VALUES, form.type, (v) => { form.type = v; touch(); });
    chips.append(out.typePick);
  }
  if (has.cost) {
    out.costPick = pickField("цена", COST_VALUES, form.cost, (v) => { form.cost = v; touch(); });
    chips.append(out.costPick);
  }
  for (const chip of cfg.tailChips || []) chips.append(chip);
  // Кнопки режимов прижимаются к правому краю строки статуса: там для них есть
  // свободное место, а заголовок остаётся заголовком.
  const modes = el("div", "tmodes");
  // Действия экрана стоят тут же, левее карандаша: отдельная полоса под
  // строкой статуса держала две кнопки и целую карточку под них, а команды
  // формы и без того собраны в одном углу (решение пользователя).
  const acts = el("div", "tacts");
  modes.append(acts);
  // Строка статуса бывает пустой: у документа нет ни типа, ни цены, ни пометок,
  // и одни кнопки режимов занимали целую строку экрана (замечание
  // пользователя). Тогда они уезжают в строку названия, как на форме задачи, а
  // сама строка статуса в разметку не встаёт вовсе.
  const bareChips = !chips.children.length;
  if (bareChips) {
    modes.classList.add("thmodes");
    head.append(modes);
  } else {
    chips.append(el("span", "gap"), modes);
    page.append(keyed(chips, cfg.key + "-chips"));
  }
  out.chips = chips;

  let file = null;
  let edit = Boolean(cfg.edit);
  let dressPen = () => {};
  // Блок ранга живёт в двух видах: просмотр показывает компактный текст итога
  // со слагаемыми, поля правки появляются карандашом. Ссылка ленивая: сам блок
  // собирается ниже, когда setEdit уже объявлен.
  let rankMode = () => {};
  // Правка включается карандашом: по умолчанию экран открывается на просмотр, и
  // описание собрано разметкой, а не лежит сырым текстом в поле. Режим меняется
  // по месту, а не перерисовкой: перерисовка над тронутой формой запрещена, она
  // стёрла бы несохранённое, и карандаш на ней молчал бы вовсе.
  const setEdit = (on) => {
    edit = on;
    if (title) {
      title.readOnly = !on;
      title.classList.toggle("ro", !on);
    }
    if (file && file.setEdit) file.setEdit(on);
    rankMode(on);
    dressPen();
    if (cfg.onEdit) cfg.onEdit(on);
  };
  out.setEdit = setEdit;
  // Вход в разговор стоит в той же строке, что карандаш и режим чтения: это
  // такое же действие над открытой задачей, и отдельной дороги ему не нужно
  // (решение пользователя). Кнопка та же, что в строке доски и в накопителе,
  // чтобы вход в чат везде выглядел одинаково.
  if (has.chat && cfg.id) {
    modes.append(rowChatBtn(cfg.project, { id: cfg.id }));
  }
  if (has.pencil) {
    const pen = el("button", "tpen");
    dressPen = () => {
      pen.classList.toggle("on", edit);
      pen.replaceChildren(icon(edit ? "close" : "i-pen"));
      pen.title = edit ? "Закончить правку" : (cfg.penLabel || "Править задачу");
      pen.setAttribute("aria-label", pen.title);
    };
    pen.addEventListener("click", () => { setEdit(!edit); });
    modes.append(pen);
  } else if (cfg.penOff) {
    // Правка заперта не поломкой, и экран обязан это сказать: карандаш стоит
    // на своём месте погашенным, а причина замка приходит подсказкой. Признак
    // disabled тут не ставится, браузер не показывает подсказку на выключенной
    // кнопке, а нажимать нечего и так, обработчика у неё нет.
    const pen = withTip(el("button", "tpen off"), cfg.penOff);
    pen.append(icon("i-pen"));
    pen.setAttribute("aria-disabled", "true");
    pen.setAttribute("aria-label", cfg.penOff);
    modes.append(pen);
  }
  // Режим чтения: описание занимает всю колонку, остальное уходит с глаз. Пара
  // к этой кнопке стоит в углу самого описания, иначе развёрнутый текст накрыл
  // бы строку статуса вместе с переключателем.
  let setRead = () => {};
  if (has.read) {
    const read = el("button", "tpen");
    read.append(icon("i-read"));
    setRead = (on) => {
      read.classList.toggle("on", on);
      read.title = on ? "Выйти из режима чтения" : "Режим чтения";
      read.setAttribute("aria-label", read.title);
      // Страница помечается целиком, а не по одному узлу: описание в режиме
      // чтения лежит поверх колонки, и всё, что стоит под ним, обязано уйти с
      // глаз. Прежде уходило не всё: командная панель строки статуса выписана
      // из потока своим слоем и рисовалась поверх текста, отчего кнопка
      // «Выполнить» стояла на первых строках постановки (замечание
      // пользователя, узкий экран). Мера общая по построению: что бы ни
      // появилось на странице завтра, в режиме чтения его не видно.
      page.classList.toggle("reading", on);
      if (file && file.setWide) file.setWide(on);
    };
    setRead(false);
    read.addEventListener("click", () => { setRead(!read.classList.contains("on")); });
    modes.append(read);
  }

  // Сохранение и действия одной полосой над содержимым (макет «02 Задача»):
  // отдельной карточки действий нет, а надписи про пустую правку нет вовсе. У
  // нетронутой формы нет и самих кнопок правки, они приходят с первым
  // изменением поля вместе с разделителем; на форме заведения сохранять есть
  // что с первого касания, и кнопка там стоит всегда.
  const bar = el("div", "card abar");
  const save = barBtn("btn btn-acc", cfg.saveLabel || "Сохранить", "i-done");
  // Вторая кнопка сохранения: тот же рубеж и та же ручка, но своя дорога после
  // записи. Форма записи черновика стоит на паре «Сохранить» и «Сохранить и
  // грумить», и обе кнопки живут и гаснут вместе.
  const more = cfg.saveMore
    ? barBtn("btn btn-acc", cfg.saveMore.label, cfg.saveMore.icon || "i-play")
    : null;
  const drop = barBtn("btn", "Отменить правку", "close");
  const sep = el("span", "div");
  const bad = el("div", "error", "");
  save.hidden = true;
  drop.hidden = true;
  sep.hidden = true;
  bar.append(save);
  if (more) {
    more.hidden = true;
    bar.append(more);
    more.addEventListener("click", () => {
      if (!more.disabled && cfg.saveMore.onSave) cfg.saveMore.onSave();
    });
  }
  // Выход с формы стоит рядом с сохранением: решение «записать или уйти»
  // человек принимает в одном месте, и держать выход в другом углу экрана
  // значило бы искать его глазами. У правки задачи выхода нет, там его роль
  // играет «Отменить правку».
  const quit = cfg.onQuit ? barBtn("btn bquit", cfg.quitLabel || "Отмена", "close") : null;
  if (quit) {
    bar.append(quit);
    quit.addEventListener("click", () => { cfg.onQuit(quit); });
  }
  out.quit = quit;
  bar.append(drop, sep);
  // Кнопки уезжают в командную панель, а подписи причин остаются полосой: в
  // углу строки статуса им не поместиться, а без них погашенная кнопка молчит
  // о том, чего ждёт.
  const actions = cfg.actions || [];
  const notes = [];
  for (const node of actions) {
    if (String(node.className || "").split(" ").includes("hint")) notes.push(node);
    else acts.append(node);
  }
  for (const node of notes) bar.append(node);
  bar.append(bad);
  save.addEventListener("click", () => { if (!save.disabled && cfg.onSave) cfg.onSave(); });
  drop.addEventListener("click", () => { if (cfg.onDrop) cfg.onDrop(); });
  out.bar = bar;
  out.title = title;
  out.save = save;
  out.saveMore = more;
  out.bad = bad;

  // Полоса в разметку не встаёт, пока показывать в ней нечего: прятать её
  // стилями нельзя, рамка карточки осталась бы на экране. Мера пустоты это
  // действия экрана и тронутая форма, а не наличие кнопки: «Сохранить» с
  // «Отменить» лежат тут всегда, просто скрытыми.
  let placed = false;
  const narrow = window.matchMedia("(max-width:900px)");
  const placeBar = (force) => {
    if (force) {
      bar.remove();
      placed = false;
    }
    // Мера пустоты теперь одна: правка. Действия уехали в командную панель, и
    // полоса живёт ради «Сохранить», «Отменить правку» и отказа.
    if (!notes.length && save.hidden && (!more || more.hidden) && !quit && !bad.textContent) {
      if (placed) {
        bar.remove();
        placed = false;
      }
      return;
    }
    if (placed) return;
    // На телефоне полоса идёт под содержимым, на ноутбуке над ним, теми же
    // местами, что держит раскладка экрана. Строки статуса может не быть вовсе
    // (форма черновика: ни типа, ни цены, ни пометок), и тогда полоса встаёт
    // после шапки: у узла вне дерева after не делает ничего, и кнопка
    // «Записать черновик» пропадала с экрана совсем.
    if (narrow.matches) page.append(bar);
    else if (chips.parentNode) chips.after(bar);
    else head.after(bar);
    placed = true;
  };
  out.placeBar = placeBar;

  // Рубеж формы спрашивается у экрана: он же говорит, тронута ли она. Отказ
  // гасит кнопку и стоит словами рядом, а не после похода на сервер.
  function touch() {
    const state = (cfg.check ? cfg.check() : null) || {};
    const dirty = Boolean(state.dirty) || Boolean(cfg.always);
    const refusal = dirty ? state.refusal || "" : "";
    bad.textContent = refusal;
    save.disabled = !dirty || Boolean(refusal);
    save.hidden = !dirty;
    if (more) {
      more.disabled = save.disabled;
      more.hidden = save.hidden;
    }
    drop.hidden = !dirty || Boolean(cfg.always);
    sep.hidden = drop.hidden;
    placeBar(false);
  }
  out.touch = touch;

  for (const node of cfg.top || []) page.append(node);

  // Ранг стоит над описанием: на телефоне он сворачивается в одну строку с
  // суммой, и переносить его вниз незачем. Свёрнут он с самого начала,
  // разворачивает нажатие на строку; на ноутбуке карточка открыта всегда.
  if (has.rank) {
    const rank = el("div", "card rcard rfolded");
    const rtop = el("div", "rtop");
    // Слева итог крупно, дальше сами поля правки. Надписи «Ранг» и «по
    // RANKING.md» тут больше нет: крупное число в этом месте экрана ничем
    // другим быть не может, а откуда правило, спрашивают в самом RANKING.md.
    // Нет и повтора слагаемых числами справа: те же пять значений стоят в
    // полях правки строкой ниже, и блок занимал вдвое больше места, чем
    // говорил (замечание пользователя).
    const big = el("div", "rbig");
    const sum = el("span", "v", "0");
    const note = el("span", "f", "");
    // В просмотре слагаемые стоят компактной строкой текста при итоге, а
    // жирные поля правки не показываются вовсе: читателю нужны значения, а не
    // пять селектов (замечание пользователя). Поля приходят карандашом, вместе
    // с остальной правкой формы.
    const view = el("span", "rview");
    big.append(sum, note, view);
    // Разворот это настоящая кнопка, и клавиатура достаётся ей даром: Enter и
    // пробел жмут её сами. Ширину при этом никто не спрашивает, кнопку прячут
    // стили, а спрятанная кнопка ни в обход табом, ни под палец не попадает.
    const fold = el("button", "rfold", "развернуть");
    fold.setAttribute("aria-expanded", "false");
    rtop.append(big, fold);
    const foldRank = () => {
      const shut = rank.classList.toggle("rfolded");
      fold.textContent = shut ? "развернуть" : "свернуть";
      fold.setAttribute("aria-expanded", shut ? "false" : "true");
    };
    // Нажатие на всю строку остаётся: пальцем целятся в неё, а не в слово. С
    // кнопки нажатие дальше не идёт, иначе ранг свернулся бы дважды.
    rtop.addEventListener("click", foldRank);
    fold.addEventListener("click", (ev) => {
      ev.stopPropagation();
      foldRank();
    });
    rank.append(rtop);
    const rbody = el("div", "rbody");
    rank.append(rbody);
    // Итог считается из слагаемых формы, а не берётся готовым числом строки:
    // правка слагаемого видна суммой сразу, до сохранения.
    const drawRank = () => {
      sum.textContent = String(form.parts.reduce((a, b) => a + Number(b), 0));
      view.textContent = RANK_PARTS
        .map((part, i) => part.name.toLowerCase() + " " + Number(form.parts[i]))
        .join(", ");
    };
    rankMode = (on) => { rank.classList.toggle("redit", on); };
    RANK_PARTS.forEach((part, i) => {
      const line = el("div", "rrow");
      line.append(el("span", "nm", part.name));
      line.append(el("span", "why", part.why));
      line.append(pickField("", part.values, form.parts[i], (v) => {
        // Правится одно слагаемое, остальные остаются прежними: пропущенное
        // сервер берёт из строки, а не считает нулём.
        form.parts[i] = Number(v);
        drawRank();
        touch();
      }));
      rbody.append(line);
    });
    drawRank();
    page.append(rank);
    out.rank = rank;
    out.rankSum = sum;
    out.rankNote = note;
  }

  // Описание и зависимости идут одной строкой под рангом. Колонок на экране
  // нет ни на какой ширине: ранг ушёл из правой колонки строкой во всю ширину,
  // и держать колонку ради одних зависимостей стало не за чем.
  const grid = el("div", "tgrid");
  if (has.file || has.deps) page.append(grid);
  if (has.file) {
    file = filePanel(cfg.project, cfg.id, cfg.detail || {}, form, touch, edit, has.make);
    // Кнопка в углу развёрнутого описания и кнопка в строке статуса это один
    // переключатель: нажатая любая из них возвращает страницу в обычный вид.
    file.onWideOff = () => { setRead(false); };
    grid.append(keyed(file, cfg.key + "-text"));
    out.file = file;
  }
  if (has.deps) grid.append(depsCard(cfg.project, cfg.id, cfg.after || [], cfg.blocks || []));
  if (cfg.links && ((cfg.links.lld || []).length || (cfg.links.tasks || []).length)) {
    grid.append(linksCard(cfg.project, cfg.links));
  }
  for (const node of cfg.extra || []) page.append(node);

  setEdit(edit);
  watchTaskLayout(placeBar);
  touch();
  return out;
}

// Место полосы действий зависит от ширины окна, и держит его подписка, а не
// снимок в момент отрисовки: окно растягивают, планшет поворачивают, и экран
// при этом не перерисовывается. Подписка одна на весь дашборд: следующая
// отрисовка формы снимает прежнюю, иначе слушатели копились бы с каждым
// переходом и двигали полосу в выброшенной разметке.
let taskLayoutWatch = null;
function watchTaskLayout(placeBar) {
  if (taskLayoutWatch) {
    taskLayoutWatch.mq.removeEventListener("change", taskLayoutWatch.place);
    taskLayoutWatch = null;
  }
  const mq = window.matchMedia("(max-width:900px)");
  const place = () => { placeBar(true); };
  place();
  mq.addEventListener("change", place);
  taskLayoutWatch = { mq, place };
}

// Экран записи вместо формы задачи: адрес заменяется, а не толкается в
// историю, иначе «назад» возвращало бы на тот же ID, а он ведёт сюда же, и
// выхода из пары экранов не было бы вовсе. Хвост разговора едет с ним: панель
// это хвост адреса, и подмена экрана под ней разговор не рвёт.
function goDraftInstead(project, id) {
  const chat = route().chat;
  const to = "#" + project + "/draft/" + id + (chat ? "/chat/" + chat : "");
  if (location.hash !== to) history.replaceState(history.state || {}, "", to);
  return refresh();
}

// Оболочка экрана задачи: хлебная крошка с номером и слова о том, что строка
// читается. Рисуется она в тот же ход, что и нажатие, чтобы человек видел
// ответ на своё нажатие сразу, а не через круг по сети (тот же приём, что у
// переключения разговоров). Настоящий экран встаёт поверх неё, когда приедет
// строка.
function taskShell(project, id) {
  const groups = document.getElementById("groups");
  const page = el("div", "tpage");
  // Крошек у экрана задачи нет, и оболочка их не рисует: иначе первый же ход
  // показывал бы строку, которая тут же пропадёт под настоящим экраном.
  const card = el("div", "card");
  card.append(el("div", "hint", "Читаем " + id + "..."));
  page.append(card);
  // Оболочка встаёт тем же ключом, что и настоящий экран, и с отпечатком,
  // какого у данных не бывает: приехавшая строка сменит её одной подменой
  // узла, а не пересборкой коробки.
  groups.replaceChildren(keyed(page, TASK_PKEY, "shell"));
}

// Отпечаток нарисованного экрана задачи: весь ответ ручки, из которого он и
// собран. Ручка отдаёт строку, текст постановки, связи и зависимости, и всё
// это читается с диска, поэтому от времени отпечаток не зависит: возврат в
// окно при нетронутой задаче даёт ту же строку, а значит и рисовать нечего.
// Живая работа приезжает списком проектов, и её признаки берутся полями, а не
// целым объектом: время последнего хода меняется каждые несколько секунд, а
// экран от него не зависит (тот же выбор, что у доски).
function taskPaintSign(project, id, r, works) {
  const work = (works || []).find((w) => w.id === id);
  return [project, id, r.ok ? "ok" : "no" + r.status,
    JSON.stringify(r.body || null),
    work ? [work.live || "", work.via || "", work.model || "", work.harness || ""].join(",") : "",
    harnessSign()].join("|");
}

// Узел экрана среди детей коробки: экран лежит прямым ребёнком, и искать его
// обходом поддерева незачем.
function paintedNode(box, key) {
  for (const kid of (box && box.children) || []) {
    if (kid.dataset && kid.dataset.pkey === key) return kid;
  }
  return null;
}

// pre это заказ строки, отправленный до сборки экрана: переход по ссылке шлёт
// его в тот же ход, что и запрос списка проектов, и ждёт их вместе, а не по
// очереди. Пусто у обычной перерисовки, и тогда строка заказывается тут.
async function renderTask(project, works, id, pre) {
  const groups = document.getElementById("groups");
  const r = await (pre || api(taskPath(project, id)));
  // За ID стоит запись накопителя, а не строка доски: так приходит упоминание
  // черновика в разговоре, у которого своей строки ещё нет. Формы задачи для
  // него не существует, и экран уходит на запись, а не показывает отказ.
  if (!r.ok && r.body && r.body.draft) {
    return goDraftInstead(project, r.body.draft);
  }
  if (taskDraft.id === id && taskDraft.dirty) {
    // В форме лежит правка: перерисовка стёрла бы её вместе с введённым
    // текстом, поэтому экран остаётся как есть, а уехавшая строка отмечается
    // пометкой.
    if (r.ok && taskSeen(r.body) !== taskDraft.seen) taskStale(project, works, id);
    return;
  }
  // Экран уже нарисован из этих же данных: обновление по фокусу окна не
  // трогает тогда ничего вовсе. Пересборка уводила из-под руки всё сразу,
  // место чтения длинного файла цели, раскрытый аккордеон и каретку в поле
  // правки (DK-411).
  const sign = taskPaintSign(project, id, r, works);
  const shown = paintedNode(groups, TASK_PKEY);
  if (shown && (shown.dataset.psign || "") === sign) return;
  // Экран собирается заново, значит и живые потоки его уходят вместе с ним:
  // журнал витка и план агента поднимутся заново на новом узле.
  closeAgentLive();
  // Готовый узел встаёт на место прежнего одной подменой, а не на опустевшей
  // коробке: список тогда не теряет прокрутку.
  const place = (page) => { sync(groups, [{ key: TASK_PKEY, sign, make: () => page }]); };

  if (!r.ok) {
    const page = el("div", "tpage");
    const card = el("div", "card");
    card.append(el("div", "error", r.body.error || "задача не прочиталась"));
    page.append(card);
    place(page);
    return;
  }
  const detail = r.body;
  const row = detail.row || {};
  // Крошек у экрана задачи нет вовсе. Ссылка на доску переехала в название
  // проекта наверху страницы, а номер стоит крупно над заголовком: строка с
  // тем же номером и той же ссылкой была третьим показом одного и того же
  // (разбор пользователя). Состояние строки и дата правки уехали в полосу
  // чипов, к типу с ценой.
  const stateChips = [];
  if (row.moved) {
    stateChips.push(withTip(el("span", "stale dashed", row.moved), whenTip(row.moved)));
  }

  // Закрытая задача открывается чтением: строки на доске у неё нет, править
  // нечего, и экран показывает заголовок с датой закрытия и файл постановки.
  // Прежде выдача поиска высаживала на такой задаче отказ, и нажатие на
  // найденную строку выглядело сломанным (замечание 4).
  if (row.closed) {
    place(formPage({
      key: "task", project, id, detail,
      chips: [el("span", "chip c-check", "закрыта " + row.closed)].concat(stateChips),
      num: row.id, titleText: row.title || id,
      form: { text: detail.text || "" },
      has: { file: true, read: true, chat: true },
    }).page);
    return;
  }

  // Черновик формы: поля правятся у себя, а на сервер уезжают вместе.
  const base = (row.r_parts || []).map(Number);
  const form = {
    title: row.title || "",
    type: row.type || "task",
    cost: row.cost || "-",
    parts: base.slice(),
    text: detail.text || "",
  };
  const isGoal = /^Цель:/.test(row.title || "");
  const work = (works || []).find((w) => w.id === id);

  // Тот же признак работы, что и в строке списка, и теми же словами: решение
  // «продолжить или не трогать» принимают чаще всего на этом экране. Признаки
  // живости и этап работы переехали сюда с экрана агента (DK-435): разговор
  // ушёл в панель, а чем занята задача и кто её ведёт это предмет самой задачи.
  // Состояние строки идёт первым чипом полосы: раньше оно стояло отдельной
  // строкой над заголовком, рядом со ссылкой на доску, и полоса с типом, ценой
  // и бакетом начиналась мимо него (решение пользователя).
  const chips = [row.section ? el("span", "chip", row.section) : null,
    liveChip(work), stageChip(row), waitChip(row)].filter(Boolean);
  if (isGoal) chips.push(el("span", "chip c-goal", "цель"));
  const tail = [withTip(el("span", "chip dashed" +
    (row.p === "P0" || row.p === "P1" ? " c-p1" : ""), row.p), P_HINT)];
  if (row.fail) tail.push(withFull(el("span", "chip c-block cwhy", "провал: " + row.fail), row.fail));
  if (row.block) tail.push(withFull(el("span", "chip c-block cwhy", "блок: " + row.block), row.block));
  const check = checkChip(row);
  if (check) tail.push(check);
  for (const chip of stateChips) tail.push(chip);

  const patchBody = () => {
    const out = {};
    if (form.title.trim() && form.title.trim() !== (row.title || "")) out.title = form.title.trim();
    if (form.type !== (row.type || "task")) out.type = form.type;
    if (form.cost !== (row.cost || "-")) out.cost = form.cost;
    const parts = RANK_PARTS.map((_, i) => (form.parts[i] === base[i] ? null : form.parts[i]));
    if (parts.some((v) => v !== null)) out.r_parts = parts;
    return out;
  };
  // Текст файла едет, только когда он вправду изменён: PUT переписывает
  // постановку целиком, и лишний заход коммитил бы её впустую.
  const textBody = () => (detail.file && form.text !== (detail.text || "") ? form.text : null);

  // Состав цели стоит над содержимым: с экрана цели смотрят прежде всего на
  // него. Ждать его отрисовка задачи не обязана, состав приезжает отдельным
  // запросом и встаёт на своё место сам.
  const top = [];
  if (isGoal) {
    const comp = el("div", "");
    top.push(comp);
    goalComposition(project, id, comp).catch(console.error);
  }

  const view = formPage({
    key: "task", project, id, detail,
    num: row.id, titleLabel: "заголовок задачи " + id, form, chips, tailChips: tail, top,
    links: detail.links || null,
    has: { title: true, type: true, cost: true, rank: true, deps: true, chat: true,
      file: true, make: true, pencil: true, read: true },
    after: detail.after || [], blocks: detail.blocks || [],
    actions: taskActions(project, id, row),
    // Признак правки живёт в черновике: следующая честная перерисовка экрана
    // (она бывает после сохранения) откроет задачу тем же режимом.
    edit: taskDraft.id === id && taskDraft.edit,
    onEdit: (on) => {
      taskDraft.id = id;
      taskDraft.edit = on;
    },
    check: () => {
      const patch = patchBody();
      const text = textBody();
      const dirty = Object.keys(patch).length > 0 || text !== null;
      taskDraft.id = id;
      taskDraft.dirty = dirty;
      return { dirty, refusal: draftRefusal(form, text) };
    },
    onSave: () => {
      const patch = patchBody();
      const text = textBody();
      if (draftRefusal(form, text)) return;
      saveTaskDraft(project, id, Object.keys(patch).length ? patch : null, text).catch(console.error);
    },
    onDrop: () => {
      taskDraft.dirty = false;
      taskDraft.edit = false;
      renderTask(project, works, id).catch(console.error);
    },
  });
  place(view.page);

  // План агента блоком под постановкой: те же пункты, что делениями на кольце
  // в шапке разговора. Плана нет, значит блока нет вовсе: заглушка «плана нет»
  // говорила бы о нашей бедности, а не о задаче.
  wireTaskPlan(project, id, view.page);

  // Журнал витка стоит последним блоком экрана, под целью и постановкой
  // (замечание 13): туда за ним и идут, а сверху он отжимал вниз то, ради чего
  // экран открывают. Панель стоит только у цели и у задачи с живой работой: у
  // остальных источника у журнала нет вовсе.
  if (isGoal || work) {
    const jp = pane("Журнал витка", "источник назовёт сервер");
    // Зелёная точка вместо слов «хвост дописывается»: живость журнала это
    // состояние, а не сообщение, и строкой текста она занимала место шапки.
    jp.head.append(el("span", "dot pulse"));
    // Отступ сверху: журнал стоит последним блоком, и без него он слипался с
    // зависимостями над собой в одну простыню.
    jp.card.classList.add("jbottom");
    view.page.append(jp.card);
    wireJournal(project, id, jp.body, jp.sub);
  }

  taskDraft.id = id;
  taskDraft.dirty = false;
  taskDraft.seen = taskSeen(detail);
  view.touch();
}

// Живые потоки экрана: EventSource журнала витка, таймер снимка груминга.
// Закрываются при любом уходе с экрана, иначе соединения копились бы с каждым
// переходом.
let agentLive = [];

// Поколение живых потоков экрана: уход с него его меняет. Журнал сначала ходит
// за ответом сервера и только потом поднимает EventSource, и запоздавший ответ
// прежнего экрана открывал поток уже после того, как остальные закрыли: он
// дописывал строки в снятую с экрана коробку (DK-290). Ответ чужого поколения
// дальше не идёт.
let liveGen = 0;
function closeAgentLive() {
  liveGen += 1;
  for (const stop of agentLive) stop();
  agentLive = [];
}

// Живые потоки панели разговора живут своим списком и своим поколением: панель
// стоит над любым экраном и переход между экранами переживает открытой, а уход
// экрана снимает только его потоки. Общий счётчик означал бы, что доска,
// перечитанная по фокусу окна, гасит ленту открытого рядом разговора.
let chatLive = [];
let chatGen = 0;
function closeChatLive() {
  chatGen += 1;
  for (const stop of chatLive) stop();
  chatLive = [];
}

function pane(title, sub) {
  const card = el("div", "card pane");
  const head = el("div", "phd");
  head.append(el("b", "", title));
  const subEl = el("span", "", sub);
  head.append(subEl);
  const body = el("div", "pbd");
  card.append(head, body);
  return { card, head, sub: subEl, body };
}

function say(box, cls, text) {
  box.replaceChildren(el("div", cls, text));
}

// Строка журнала: время отдельным тоном, как в макете «03 Агент».
function logLine(line) {
  const div = el("div");
  const m = line.match(/^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}) ([\s\S]*)$/);
  if (m) {
    div.append(el("span", "t", m[1]), document.createTextNode(" " + m[2]));
  } else {
    div.textContent = line;
  }
  return div;
}

// Журнал цикла: SSE-хвост .devkit/goal-<ID>.log. Отсутствие журнала сервер
// называет событием note, слова видны вместо пустого экрана.
function wireJournal(project, id, body, sub) {
  body.classList.add("log");
  const es = new EventSource("/api/projects/" + encodeURIComponent(project) +
    "/goals/" + encodeURIComponent(id) + "/log?stream=1");
  agentLive.push(() => es.close());
  // Источник журнала говорит сервер: у цели под оболочкой это goal-<ID>.log, у
  // цели, которую ведёт живой чат, раздел «Журнал» файла цели (DK-255).
  es.addEventListener("source", (ev) => { sub.textContent = ev.data; });
  es.addEventListener("note", (ev) => { say(body, "empty", ev.data); });
  es.onmessage = (ev) => {
    if (body.firstChild && body.firstChild.className === "empty") body.replaceChildren();
    body.append(logLine(ev.data));
    body.scrollTop = body.scrollHeight;
  };
}

// Время реплики в поясе того, кто смотрит: в транскрипте оно записано с
// зоной, и вырезка символов из строки показывала бы UTC. Нераспознанная метка
// не выдумывается, от неё остаются те же символы, что и раньше.
function localTime(stamp) {
  const d = new Date(stamp);
  if (!stamp || isNaN(d.getTime())) return String(stamp || "").slice(11, 16);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// Разделитель дня и ключ, по которому реплики в него собираются: день тоже
// местный, иначе полуночная реплика уезжает в соседнюю дату.
function localDay(stamp) {
  const d = new Date(stamp);
  if (!stamp || isNaN(d.getTime())) return String(stamp || "").slice(0, 10);
  return d.toLocaleDateString([], { day: "numeric", month: "long" });
}

function localDayKey(stamp) {
  const d = new Date(stamp);
  if (!stamp || isNaN(d.getTime())) return String(stamp || "").slice(0, 10);
  return d.toDateString();
}

// Метка времени из того, что приехало с сервера: сессии и накопитель шлют
// unix-секунды, доска шлёт голый день («2026-08-20»), потому что дату строки
// считает taskctl по git blame и времени в ней нет. Точность едет вместе с
// меткой: чего сервер не сказал, того подсказка не выдумывает.
function whenStamp(mark) {
  if (typeof mark === "number" || /^\d+$/.test(String(mark || ""))) {
    const secs = Number(mark);
    if (!secs) return null;
    const at = new Date(secs * 1000);
    return isNaN(at.getTime()) ? null : { at, exact: true };
  }
  const said = String(mark || "");
  const day = /^(\d{4})-(\d{2})-(\d{2})/.exec(said);
  if (!day) return null;
  const at = new Date(Number(day[1]), Number(day[2]) - 1, Number(day[3]));
  return isNaN(at.getTime()) ? null : { at, exact: said.length > 10 };
}

// Давность словами: чем ближе метка, тем она полезнее, и дальше месяца
// подсказка про неё молчит, там читается сама дата.
function whenAgo(stamp, now) {
  const from = Number(now) || Date.now();
  const mins = Math.floor((from - stamp.at.getTime()) / 60000);
  if (mins < 0) return "";
  const days = Math.floor(mins / 60 / 24);
  if (!stamp.exact) {
    if (days <= 0) return "сегодня";
    if (days === 1) return "вчера";
    return days <= 30 ? days + " " + plural(days, "день", "дня", "дней") + " назад" : "";
  }
  if (mins < 1) return "только что";
  if (mins < 60) return mins + " " + plural(mins, "минуту", "минуты", "минут") + " назад";
  const hours = Math.floor(mins / 60);
  if (hours < 24) return hours + " " + plural(hours, "час", "часа", "часов") + " назад";
  return days <= 30 ? days + " " + plural(days, "день", "дня", "дней") + " назад" : "";
}

// Подсказка даты одна на все списки: точная дата со временем и давность
// словами. Прежде подсказка объясняла, что это за дата («перевод в статус
// двигает её же»), а к дате человек наводится за точностью: в ячейке стоит
// день, времени не видно нигде, а что это за дата, сказано в заголовке колонки
// (замечание пользователя).
// Месяц берётся словом из общего списка MONTHS (он же у заголовков дней ленты):
// подсказка говорит по-русски при любой раскладке браузера, а
// toLocaleDateString на английской машине отдавал бы «August 20, 2026» рядом с
// русским «8 дней назад».
function whenTip(mark, now) {
  const stamp = whenStamp(mark);
  if (!stamp) return "";
  const at = stamp.at;
  const two = (n) => String(n).padStart(2, "0");
  const day = at.getDate() + " " + MONTHS[at.getMonth()] + " " + at.getFullYear();
  const said = stamp.exact
    ? day + " в " + two(at.getHours()) + ":" + two(at.getMinutes())
    : day;
  const ago = whenAgo(stamp, now);
  return ago ? said + ", " + ago : said;
}

// Ссылка из реплики: кликается только http и https, а javascript: и data:
// остаются текстом. Чужая вкладка открывается без доступа к нашей (noopener).
// Ссылка на файл репозитория («[tasks/DK-397.md](../tasks/DK-397.md)») в чужую
// вкладку не ведёт вовсе: такой путь открывается экраном дашборда, и разбирает
// его тот же mentionAddr, что и голое упоминание в тексте.
function mdLink(text, href, where) {
  if (/^https?:\/\//i.test(href)) {
    const a = el("a", "", text);
    a.href = href;
    a.target = "_blank";
    a.rel = "noopener noreferrer";
    return a;
  }
  const addr = where
    ? mentionAddr(where, String(href).replace(/^(?:\.{1,2}\/)+/, "")) || mentionAddr(where, text)
    : "";
  if (addr) return mdGo(text, addr);
  return document.createTextNode(text);
}

// Префиксы досок по проекту (DK, XR): по ним автоссылка узнаёт в реплике ID
// задачи, а обратной дорогой выбирает проект, куда вести. Список приезжает тем
// же ответом, что и колонка проектов, своего запроса тут нет.
const boardPrefixes = new Map();

function rememberPrefixes(projects) {
  for (const p of projects || []) {
    if (p && p.name && p.prefix) boardPrefixes.set(p.name, String(p.prefix).toUpperCase());
  }
}

function projectOfPrefix(pfx) {
  for (const [name, own] of boardPrefixes) {
    if (own === pfx) return name;
  }
  return "";
}

// Проект открытой ленты: автоссылки в репликах ведут в него, а не в проект,
// открытый на доске, потому что разговор бывает чужой. Ставится он там же, где
// адрес картинки (chatShotProject): пузырь рисуется без контекста, и назвать
// проект больше нечем.
let chatFeedProject = "";

// Проект ленты ставится одной дверью, а не присваиванием по месту: зовут её и
// лента разговора, и очередь своих реплик, и обе рисуют одни и те же пузыри.
function chatFeedIn(project) {
  chatFeedProject = project || "";
}

// Проект открытой ленты словом: разметка пузыря спрашивает его тут.
function chatFeedAt() {
  return chatFeedProject;
}

// Куда ведёт упоминание из реплики. ID задачи ведёт на её экран, и строка на
// доске тут по-прежнему не спрашивается: закрытую задачу экран открывает
// архивом, а за ID без строки и без архива стоит запись накопителя, и на неё
// экран задачи уходит сам, по слову сервера (goDraftInstead). Прежде разбор
// на этом и останавливался, ID черновика приводил на форму несуществующей
// задачи, и ссылка с виду не открывалась. Судит тут префикс: имя со своим префиксом
// ведёт в свой проект, с чужим в тот, чья это доска, а незнакомое («UTF-8»,
// «COVID-19») остаётся текстом. Путь документа ведёт на его экран, а файл
// задачи на ту же форму, что и ID.
function mentionAddr(where, text) {
  const path = String(text || "").replace(/^(?:\.{1,2}\/)+/, "");
  if (/\.md$/i.test(path)) {
    const doc = path.replace(/^docs\//, "");
    const draft = doc.match(/^tasks\/drafts\/(.+)\.md$/);
    if (draft) return where + "/draft/" + draft[1];
    const task = doc.match(/^tasks\/([A-Za-z][A-Za-z0-9]*-\d+)\.md$/);
    if (task) return where + "/" + task[1].toUpperCase();
    if (/^(?:tasks|lld)\//.test(doc)) return where + "/doc/" + doc;
    return "";
  }
  const id = path.toUpperCase();
  const cut = id.indexOf("-");
  if (cut <= 0 || !/^[A-Z][A-Z0-9]*-\d+$/.test(id)) return "";
  const pfx = id.slice(0, cut);
  if (boardPrefixes.get(where) === pfx) return where + "/" + id;
  const alien = projectOfPrefix(pfx);
  return alien ? alien + "/" + id : "";
}

// Ссылка внутрь дашборда: адрес стоит в href, чтобы её можно было открыть
// соседней вкладкой и скопировать, а нажатие идёт через goKeepingChat, иначе
// хэш затёр бы хвост открытой панели и разговор закрывался бы сам собой.
function mdGo(what, addr) {
  // Подписью бывает и готовый узел: путь документа в обратных кавычках остаётся
  // моноширинным, а ссылкой становится обёртка вокруг него.
  const a = el("a", "mdgo");
  if (typeof what === "string") a.textContent = what;
  else a.append(what);
  a.href = "#" + addr;
  a.addEventListener("click", (ev) => {
    if (ev.preventDefault) ev.preventDefault();
    if (ev.stopPropagation) ev.stopPropagation();
    goFromChat(addr);
  });
  return a;
}

// Упоминание задачи или документа в обычном тексте реплики. Разбор идёт только
// по тому, что осталось от строчной разметки текстом: ID в обратных кавычках и
// в блоке кода сюда не попадает вовсе, и команда с путём остаётся командой.
const MD_MENTION = /(^|[^\w/.-])((?:docs\/)?(?:tasks|lld)\/[\w./-]+\.md|[A-Za-z][A-Za-z0-9]{0,7}-\d+)(?![\w-])/;

function mdText(text, into, where) {
  let rest = String(text);
  for (;;) {
    const m = where ? MD_MENTION.exec(rest) : null;
    if (!m) break;
    const at = m.index + m[1].length;
    const end = at + m[2].length;
    const addr = mentionAddr(where, m[2]);
    if (!addr) {
      // Вести некуда: чужой префикс, незнакомый документ. Упоминание остаётся
      // текстом целиком, и разбор продолжается за ним.
      into.append(document.createTextNode(rest.slice(0, end)));
      rest = rest.slice(end);
      continue;
    }
    if (at) into.append(document.createTextNode(rest.slice(0, at)));
    into.append(mdGo(m[2], addr));
    rest = rest.slice(end);
  }
  if (rest) into.append(document.createTextNode(rest));
}

// Строчная разметка: код в обратных кавычках, ссылка скобками, жирный,
// курсив и голый адрес. Разбор идёт по одному совпадению за раз, остаток
// уходит текстовым узлом.
const MD_INLINE = /`([^`]+)`|\[([^\]]+)\]\(([^)\s]+)\)|(\*\*|__)([\s\S]+?)\4|(\*|_)([\s\S]+?)\6|(https?:\/\/[^\s<>"']+)/;

function mdInline(text, into, where) {
  let rest = String(text);
  for (;;) {
    const m = MD_INLINE.exec(rest);
    if (!m) break;
    if (m.index) mdText(rest.slice(0, m.index), into, where);
    if (m[1] !== undefined) {
      // Код в обратных кавычках разбор не трогает: там команды и флаги. Одно
      // исключение это путь документа репозитория: в кавычках его пишут и
      // агенты, и человек, и это имя документа, а не команда, так что ссылка
      // там ожидаема (замечание пользователя по снимку). Вид кода при этом
      // остаётся, ссылкой становится обёртка. Блок кода тройными кавычками не
      // трогается вовсе: он до строчного разбора не доходит.
      const code = el("code", "", m[1]);
      const said = String(m[1]).trim();
      const addr = where && /\.md$/i.test(said) ? mentionAddr(where, said) : "";
      into.append(addr ? mdGo(code, addr) : code);
    } else if (m[2] !== undefined) {
      into.append(mdLink(m[2], m[3], where));
    } else if (m[5] !== undefined) {
      const b = el("b");
      mdInline(m[5], b, where);
      into.append(b);
    } else if (m[7] !== undefined) {
      const i = el("i");
      mdInline(m[7], i, where);
      into.append(i);
    } else {
      into.append(mdLink(m[8], m[8], where));
    }
    rest = rest.slice(m.index + m[0].length);
  }
  if (rest) mdText(rest, into, where);
}

// Минимальный markdown реплик: заголовки, списки, код-блоки, строчный код,
// жирный и курсив, ссылки. Свой разбор без внешней библиотеки, и весь текст
// кладётся в узлы через textContent, поэтому разметка из реплики остаётся
// буквами: <script> в ответе агента виден словами, а не исполняется.
// Широкое содержимое (таблица, код) едет в своей прокрутке: страница от него
// вбок не разъезжается.
function wrapScroll(node) {
  const box = el("div", "mdscroll");
  box.append(node);
  return box;
}

function mdRender(text, where) {
  const box = el("div", "md");
  const lines = String(text || "").split("\n");
  // stack это открытые списки по уровням вложенности: у каждого свой отступ,
  // вид (нумерованный или маркерами) и последний пункт, к которому цепляется
  // продолжение строки.
  const stack = [];
  let list = null;
  let para = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^\s*```/.test(line)) {
      const buf = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) buf.push(lines[i]);
      stack.length = 0;
      list = null;
      para = null;
      box.append(el("pre", "", buf.join("\n")));
      continue;
    }
    const head = line.match(/^(#{1,6})\s+(.*)$/);
    if (head) {
      stack.length = 0;
      list = null;
      para = null;
      const h = el("div", "mdh mdh" + head[1].length);
      mdInline(head[2], h, where);
      box.append(h);
      continue;
    }
    // Таблица: строка с трубами, под ней разделитель из дефисов. Разбор
    // нарочно узкий, ровно под то, что встречается в постановках; выравнивание
    // из разделителя не читается, оно тут ни на что не влияет.
    if (/\|/.test(line) && i + 1 < lines.length && /^\s*\|?[\s:-]*-[\s:|-]*$/.test(lines[i + 1])) {
      list = null;
      para = null;
      const cells = (ln) => ln.replace(/^\s*\|/, "").replace(/\|\s*$/, "").split("|");
      const table = el("table", "mdt");
      const thead = el("thead");
      const hrow = el("tr");
      for (const c of cells(line)) {
        const th = el("th");
        mdInline(c.trim(), th, where);
        hrow.append(th);
      }
      thead.append(hrow);
      table.append(thead);
      const tbody = el("tbody");
      i += 2;
      for (; i < lines.length && /\|/.test(lines[i]) && lines[i].trim(); i++) {
        const tr = el("tr");
        for (const c of cells(lines[i])) {
          const td = el("td");
          mdInline(c.trim(), td, where);
          tr.append(td);
        }
        tbody.append(tr);
      }
      i--;
      table.append(tbody);
      box.append(wrapScroll(table));
      continue;
    }
    // Цитата: полоса слева, содержимое разбирается тем же строчным разбором.
    const quote = line.match(/^\s*>\s?(.*)$/);
    if (quote) {
      list = null;
      para = null;
      const q = el("blockquote", "mdq");
      mdInline(quote[1], q, where);
      box.append(q);
      continue;
    }
    // Список: маркер или номер. Разбор помнит отступ и номер начала, потому
    // что от них зависит и вложенность, и с какой цифры список продолжится.
    // До этого нумерация всегда начиналась с единицы, вложенный список
    // сливался с внешним, а строка продолжения пункта («висячий» отступ)
    // становилась отдельным абзацем и рвала список надвое (замечание 1
    // двенадцатого круга POC).
    const item = line.match(/^(\s*)([-*+]|\d+)([.)]?)\s+(.*)$/);
    if (item && (item[3] || /[-*+]/.test(item[2]))) {
      para = null;
      const pad = item[1].replace(/\t/g, "    ").length;
      const ordered = /\d/.test(item[2]);
      const kind = ordered ? "ol" : "ul";
      // Уровень вложенности идёт по отступу: свой список на каждый новый
      // отступ, возврат к меньшему закрывает вложенные.
      while (stack.length && stack[stack.length - 1].pad > pad) stack.pop();
      let top = stack[stack.length - 1];
      if (!top || top.pad < pad || top.kind !== kind) {
        const box2 = el(kind);
        if (ordered) {
          // Нумерация продолжается с той цифры, которую написал человек:
          // список, начатый с четвёртого пункта, так и читается.
          const from = Number(item[2]);
          if (from > 1) box2.setAttribute("start", String(from));
        }
        if (top && top.pad < pad) top.li.append(box2);
        else box.append(box2);
        top = { pad, kind, box: box2, li: null };
        stack.push(top);
      }
      const li = el("li");
      mdInline(item[4], li, where);
      top.box.append(li);
      top.li = li;
      list = top.box;
      continue;
    }
    // Продолжение пункта: строка с отступом под открытым списком принадлежит
    // последнему пункту, а не начинает абзац.
    if (stack.length && /^\s+\S/.test(line) && stack[stack.length - 1].li) {
      const li = stack[stack.length - 1].li;
      li.append(document.createTextNode(" "));
      mdInline(line.trim(), li, where);
      continue;
    }
    if (!line.trim()) {
      // Пустая строка закрывает списки: следующий список начнётся заново, и
      // нумерация в нём пойдёт со своей цифры.
      stack.length = 0;
      list = null;
      para = null;
      continue;
    }
    if (para) {
      para.append(document.createTextNode("\n"));
      mdInline(line, para, where);
      continue;
    }
    stack.length = 0;
    list = null;
    para = el("p");
    mdInline(line, para, where);
    box.append(para);
  }
  return box;
}

// Шеврон разворота: он один на все свёрнутые блоки ленты. Прежде размышления и
// блок фонового агента показывали плюс с минусом, а карточки хода шеврон, и
// одно и то же действие выглядело двумя разными (замечание пользователя).
function foldCar() {
  const car = el("span", "foldc foldar");
  car.set = (open) => {
    car.replaceChildren(icon(open ? "i-unfold" : "i-fold"));
    car.title = open ? "Свернуть" : "Раскрыть";
    car.setAttribute("aria-label", car.title);
  };
  car.set(false);
  return car;
}

// Шапка свёрнутого блока: короткая подпись, длинный хвост с обрезкой и кнопки,
// прижатые к правому краю. Одна на все свёрнутые блоки ленты, чтобы разворот и
// копирование стояли в одних и тех же местах у размышлений, ходов и вестей о
// фоновой работе, каким бы длинным ни был заголовок (замечание пользователя по
// снимку чата DK-656). Хвост пустым всё равно присутствует: он растягивается и
// прижимает кнопки вправо.
function foldHead(name, sub, copy) {
  const top = el("div", "foldh");
  top.append(el("b", "", name));
  const tail = el("span", "", sub || "");
  if (sub) tail.title = sub;
  top.append(tail);
  if (copy) top.append(copyBtn(copy));
  return top;
}

// Свёрнутый блок с разворотом по клику: заголовок остаётся строкой ленты, а
// тело раскрывается на месте. Так показываются размышления и вызовы
// инструментов, которых на экране бывает больше, чем самого разговора. copy это
// текст для кнопки копирования; без него кнопки нет.
function foldEl(cls, head, text, sub, copy) {
  const box = el("div", cls + " fold");
  const top = foldHead(head, sub, copy);
  const car = foldCar();
  top.append(car);
  const body = el("pre", "foldb", text);
  body.hidden = true;
  top.addEventListener("click", () => {
    body.hidden = !body.hidden;
    car.set(!body.hidden);
    box.classList.toggle("open", !body.hidden);
  });
  box.append(top, body);
  return box;
}

// Кнопка копирования при свёрнутом блоке: команду с выводом уносят в терминал
// целиком, и выделять их мышью из ленты неудобно. Ответ виден на самой кнопке:
// молчаливое копирование неотличимо от несработавшего.
function copyBtn(text) {
  const btn = el("button", "foldcp");
  btn.title = "Копировать";
  btn.setAttribute("aria-label", "Копировать");
  btn.append(icon("i-copy"));
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    const done = () => {
      btn.classList.add("ok");
      setTimeout(() => { btn.classList.remove("ok"); }, 1500);
    };
    const nav = window.navigator;
    if (nav && nav.clipboard && nav.clipboard.writeText) {
      nav.clipboard.writeText(text).then(done).catch(() => { btn.classList.add("bad"); });
      return;
    }
    btn.classList.add("bad");
  });
  return btn;
}

// Кнопка разворота блока хода: содержимое в ленте обрезано парой строк, и
// длинный вывод читают, не уходя в терминал. Стрелка вниз раскрывает блок на
// всю высоту, вверх сворачивает обратно (замечание 7).
function growBtn(box) {
  const btn = el("button", "foldcp foldar");
  const mark = () => {
    const open = box.classList.contains("open");
    btn.title = open ? "Свернуть" : "Раскрыть";
    btn.setAttribute("aria-label", btn.title);
    btn.replaceChildren(icon(open ? "i-unfold" : "i-fold"));
  };
  mark();
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    box.classList.toggle("open");
    mark();
  });
  return btn;
}

// Первая строка длинного текста для заголовка свёрнутого блока: по ней видно,
// о чём он, не разворачивая.
function foldPeek(text, n) {
  const line = String(text || "").split("\n").find((l) => l.trim()) || "";
  return line.length > n ? line.slice(0, n) + "..." : line;
}

// Длительность размышлений словами: секунды до минуты, дальше минуты. Точность
// тут не нужна вовсе, нужен порядок величины.
function thinkWord(ms) {
  const sec = Math.max(1, Math.round(ms / 1000));
  if (sec < 60) return sec + " с";
  return Math.round(sec / 60) + " мин";
}

function replyEl(item) {
  if (item.role === "note") {
    // Служебная вставка харнеса: уведомление о фоновом агенте, слэш-команда.
    // Стоит серой строкой между репликами, а не пузырём: пузырь означает, что
    // это сказал человек, а сказал это харнес.
    // Подписанная служебка это чужая реплика, доехавшая до работающего агента
    // (диспетчер субагенту, чужая сессия): текст у неё длинный, и в ленте она
    // стоит свёрнутой строкой с подписью, разворачивается кликом.
    if (item.note) {
      // Конец фоновой работы это заголовок со сводкой, а внутри полный отчёт
      // субагента разметкой: сырым пузырём рядом он стоял вторым элементом
      // того же события (замечание пользователя по снимку).
      if (item.mark === "agent") return reportCard(item.note, item.text || "");
      // Пересказ съеденного начала разговора: несколько тысяч слов, которые
      // харнес кладёт от имени человека. В ленте он стоял пузырём человека, и
      // выглядело это так, будто человек сам написал портянку по-английски
      // (замечание пользователя). Вид у него тот же, что у прочих свёрнутых
      // блоков: заголовок, разворот кликом, кнопка копирования.
      if (item.mark === "compact") {
        return foldEl("compact", item.note, item.text || "", "", item.text || "");
      }
      return bodyCard(item.note, "", item.text || "");
    }
    return el("div", "svcline", item.text || "служебное сообщение");
  }
  if (item.role === "thinking") {
    // Размышления идут текстом, а не меткой (POC ветки poc-chat): прежде
    // сервер выбрасывал текст, и в ленте стояло «размышления свёрнуты», из
    // чего ничего не следовало. Текста может не быть вовсе (модель отдаёт
    // размышления запечатанными), и тогда в ленте стоит длительность, как
    // «Thought for 5s» в vscode: сколько агент думал, видно всегда.
    const spent = item.spent ? "Размышлял " + thinkWord(item.spent) : "Размышление...";
    if (!item.text) return el("div", "think", spent);
    return foldEl("think", spent, item.text, foldPeek(item.text, 90));
  }
  if (item.role === "toolout") {
    // Вывод без своего вызова бывает на стыке страниц истории: рисуется он той
    // же отчёркнутой строкой, что и вывод при ходе.
    return toolOutLine(item.text || "");
  }
  if (item.role === "tool") {
    return toolPair(item, null);
  }
  const turn = el("div", "turn");
  const when = item.time ? ", " + localTime(item.time) : "";
  turn.append(el("div", "th", (item.role === "user" ? "человек" : "агент") + when));
  const body = el("div", "tb");
  body.append(mdRender(item.text || "", chatFeedAt()));
  turn.append(body);
  return turn;
}

// Якорь ленты: низ держится, только когда человек и так смотрит в низ. Пока
// он читает историю выше, дописанная реплика прокрутку не трогает.
function atBottom(box) {
  return box.scrollHeight - box.scrollTop - box.clientHeight < 60;
}

// Дописывание с сохранением взгляда: что дописано вниз, то видно сразу, если
// лента и так стояла внизу.
function keepBottom(box, was) {
  if (was) box.scrollTop = box.scrollHeight;
}

// Догрузка истории вверх: после вставки старых реплик видно то же место, что
// и до неё, поэтому меряется расстояние от низа, а не сверху. Тот же расчёт
// восстанавливает место при возврате на разговор (DK-434), а там запомненное
// расстояние бывает больше свежего хвоста, и без выравнивания по нулю лента
// встала бы выше своего собственного верха.
function keepPlace(box, tail) {
  box.scrollTop = Math.max(0, box.scrollHeight - tail);
}

// Подпись сессии в списке: узнанная задача с источником узнавания либо
// заголовок самого разговора. Разряд привязки виден словами: «ведёт» стоит на
// записи реестра чатов, «говорит о» на угадывании по транскрипту, и работой
// задачи считается только первое (DK-431). Разговор без задачи это свободный
// чат, и отчёта о том, чего дашборд про него не узнал, в подписи нет.
function sessionSign(s) {
  const title = (s.first || "").trim();
  if (s.task) return s.task + (title ? ", " + title : "");
  return title || "чат " + String(s.id || "").slice(0, 8);
}

// Привязка разговора к задаче рукой: ответ на сессию, чью задачу угадать
// нечем. Пустое значение снимает привязку, и сессия перестаёт считаться
// работой задачи (ручка DK-431).
async function bindSession(project, sid, task) {
  sayResult(task ? "привязка сессии к " + task + "..." : "снятие привязки...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/sessions/" + encodeURIComponent(sid) + "/task", { method: "POST", body: { task } });
  sayResult(r.body.message || r.body.error || "", !r.ok);
  if (r.ok) await refresh();
}

// Прочие сессии под словами о пустоте: работа рядом идёт, её видно списком, но
// в ленту задачи она не лезет. Список нужен ровно затем, чтобы нераспознанная
// сессия не пропадала молча.
async function listOtherSessions(project, box) {
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/sessions");
  if (!r.ok) return;
  const list = r.body.sessions || [];
  if (!list.length) return;
  box.append(el("div", "hint", "Сессии проекта:"));
  for (const s of list.slice(0, 10)) {
    box.append(el("div", "hint",
      s.id.slice(0, 8) + ", " + (s.mtime || "").slice(11, 16) + ", " + sessionSign(s)));
  }
}

// Подпись разговора в шапке панели: день и время последней записи, за ними
// дерево или ветка, в которых он шёл. Дата берётся в поясе клиента. Одного
// времени для различения мало: груминг и исполнение одной задачи идут в
// разных деревьях с разницей в минуты, и разводит их как раз дерево (DK-290).
function sessionTab(s) {
  const when = s.mtime ? localDay(s.mtime) + ", " + localTime(s.mtime) : s.id.slice(0, 8);
  const where = s.tree || s.branch || "";
  return where ? when + ", " + where : when;
}

// Сколько реплик читается при открытии: разговор открывается концом, и тянуть
// всю сессию ради последних слов незачем. Столько же подгружает прокрутка
// вверх, столько же шлёт первым делом поток (repliesDefault в sessions.go).
const CHAT_TAIL = 40;

// Страница истории: хвост открытия маленький, чтобы разговор вставал быстро, а
// назад лента ходит крупными кусками. Сорока записями история кончалась через
// пол-экрана, и прокрутка упиралась в подгрузку каждые несколько оборотов
// колеса, а у сессии их тысячи (жалоба «чат плохо листается»).
const CHAT_PAGE = 250;

// Пустая лента называется словами и на клиенте: молчащая коробка неотличима
// от оборвавшегося потока. Те же слова шлёт сервер (emptyTranscriptNote), и
// приехавшие с ответа заменяют эти.
const EMPTY_TALK = "в транскрипте пока нет реплик";

// Начало разговора тоже названо словами: без надписи попытка подгрузить
// историю у самого верха ленты неотличима от зависшего запроса (DK-434).
const FEED_START = "это начало чата";

// Пауза перед своим переподключением потока: браузерный ретрай идёт сам, и
// торопиться впереди него незачем, а упавший сервер не должен получить очередь
// переподключений со всех вкладок разом.
const streamRetry = 2000;

// Сколько лента терпит тишину потока, прежде чем дочитать хвост опросом.
// Полторы минуты длиннее любой паузы между ходами живого агента и короче
// того, что человек согласен считать зависанием.
const streamQuiet = 90000;

// Сколько реплик тянет догон: пропуск за время сна ноутбука бывает длиннее
// хвоста открытия, а лишние реплики отсеются по seq.
const repliesCatch = 200;

// Порог подгрузки: запрос уходит чуть раньше, чем взгляд дойдёт до верха
// ленты, и новые реплики успевают встать в дерево до того, как их было бы
// видно (то же на глаз, что порог низа в atBottom).
const LOAD_MARGIN = 80;

// Запас над взглядом: пока сверху меньше полутора экранов истории, лента тянет
// следующую страницу, не дожидаясь, когда человек упрётся в край. Порог в
// восемьдесят пикселей срабатывал ровно в тот момент, когда прокрутка уже
// встала.
const LOAD_AHEAD = 1.75;

// Сколько страниц лента поднимает за один заход вверх. Быстрое листание
// съедает страницу раньше, чем приедет следующая, поэтому за упором тянется
// ещё одна, но не больше: бесконечный цикл на пустом разговоре не нужен.
const LOAD_BURST = 2;

// Позиция ленты каждого разговора живёт, пока открыта вкладка, а ключом ей
// служит ID сессии. Уход с экрана и возврат восстанавливают её без похода на
// диск, поэтому не localStorage, а память вкладки (тот же выбор для панели
// чата в решении 5 LLD DK-430). Слушатель прокрутки пишет сюда на каждый
// сдвиг, а открытие ленты читает её, прежде чем решить, куда встать: вниз
// или на прежнее место. Расстояние от низа (`rest`) само по себе не годится:
// свежий заход приносит только хвост, а до ухода лента могла стоять глубже,
// после подгрузки истории. Без глубины (числа поднятых страниц) место мерилось бы против
// чужой, куда меньшей ленты, и клампилось бы к нулю (замечание ревью
// DK-434).
const feedPlace = new Map();

function sessionURL(project, sid) {
  return "/api/projects/" + encodeURIComponent(project) +
    "/sessions/" + encodeURIComponent(sid);
}

// Лента разговора одним куском на оба экрана (DK-371). До неё экран агента и
// чат цели держали по своей копии разбора реплик, стрима и пагинации, и всякая
// правка ленты делалась дважды. Параметры называют разницу: адрес источника
// (проект и сессия), размер хвоста, отбор и разметка реплики, коробка ленты и
// коробка прокрутки, когда лента лежит внутри чужой.
//
// Устройство одно на оба экрана: хвост приезжает обычным ответом, дальше
// дострение потоком с отсевом по seq (свой хвост поток шлёт заново), а
// история подгружается сама, от прокрутки вверх, через ?before=, и
// пересобирает ленту (DK-434: кнопка «раньше» была костылём и мешала, вместо
// неё видна только надпись начала разговора, когда раньше правда нечего
// показать). Лента считается целиком, а перерисовывается по месту:
// разделители дней зависят от соседей, и догруженная история переставляет их
// сама собой, но реплика с прежним ключом и отпечатком остаётся тем же узлом,
// поэтому приход снизу разговор не дёргает. Взгляд держится якорем: у нижнего
// края лента остаётся у нижнего края, а из истории её вниз не бросает, потому
// что мерится расстояние до низа; тем же якорем лента встаёт на прежнее место
// при возврате на разговор (feedPlace). Если до ухода была подгрузка вверх,
// перед этим тем же движением, что и от прокрутки, досбирается та же глубина
// истории, иначе высота свежего хвоста меньше прежней, и якорь мимо места.
async function wireFeed(project, sid, opts) {
  // Поколение приходит от того, кто ленту поднял: у панели разговора оно своё,
  // и перерисованный рядом экран её не гасит.
  const era = opts.era || (() => liveGen);
  const gen = era();
  const scroll = opts.scroll;
  const keep = opts.talk || (() => true);
  const tail = opts.tail || CHAT_TAIL;
  const page = opts.page || CHAT_PAGE;
  // Возврат в открытый разговор поднимает ленту на её же узлах: записи стоят,
  // где стояли, и приехавшая дельта дописывается по ключам (sync ниже). Прежде
  // лента чистилась тут всегда, и вернувшийся человек смотрел на пустоту, пока
  // едет ответ сервера.
  const had = opts.keep ? opts.box.querySelector(".feed-start") : null;
  const list = opts.keep ? opts.box.querySelector("." + (opts.list || "mlist")) : null;
  const atStart = had || el("div", "feed-start", FEED_START);
  if (!had) atStart.hidden = true;
  const box = list || el("div", opts.list || "");
  if (!had || !list) opts.box.replaceChildren(atStart, box);

  // Снятая лента помечает себя сама, а не только общим списком остановок:
  // поток поднимается после ответа сервера, и уход с ленты застаёт кусок на
  // await. Без пометки переключение на соседний разговор оставляло бы позади
  // ленту, которая откроет поток уже после того, как её закрыли.
  const live = { closed: false, es: null };
  opts.live.push(() => {
    live.closed = true;
    if (live.es) live.es.close();
  });
  const gone = () => live.closed || gen !== era();

  const talk = [];
  // Своё место в ленте у записи одно и то же от заезда к заезду: ключ
  // «источник:номер в файле». Номер в слитой ленте (seq) плывёт, потому что
  // боковые журналы растут, и пагинация по нему давала нахлёсты и дыры.
  // shown держит уже показанное: и хвост потока, и догон присылают то же самое
  // по второму разу.
  const shown = new Set();
  let firstKey = null;
  // Сколько страниц истории поднято сверх хвоста: по этому числу лента
  // возвращается на прежнюю глубину, вернувшись к разговору.
  let pages = 0;
  let atFirst = false;
  // Пустая строка тут не «возьми умолчание», а «плашки не надо вовсе»: у
  // ленты чата пустота говорит сама за себя.
  let empty = opts.empty === undefined ? EMPTY_TALK : opts.empty;
  let loadingOlder = false;
  // Надпись начала горит, только когда раньше правда нечего показать: пока
  // лента пуста или ещё не упёрлась в начало разговора, надпись не видна.
  // Начало называет сервер: своих номеров у клиента больше нет.
  const updateStart = () => { atStart.hidden = !atFirst; };
  updateStart();
  // Запись уже в ленте: ключ приходит с сервера, а у старого ответа без ключа
  // остаётся номер, и тогда отсев идёт по нему.
  const fresh = (item) => {
    const key = itemKey(item);
    if (shown.has(key)) return false;
    shown.add(key);
    return true;
  };

  // Реплика человека приходит двумя источниками сразу: журналом отправленного
  // (её пишет сам дашборд, чтобы её видели все открытые экраны) и эхом из
  // транскрипта, когда клиент агента до неё дошёл. Пришедшее эхо вытесняет
  // запись журнала: сверка по тексту, своего идентификатора у реплики в
  // транскрипте нет.
  const isSaid = (item) => String((item && item.key) || "").startsWith("said-");
  const dropSaid = (item) => {
    if (!item || item.role !== "user" || !item.text || isSaid(item)) return;
    for (let i = talk.length - 1; i >= 0; i--) {
      if (isSaid(talk[i]) && sameSay(talk[i].text, item.text)) talk.splice(i, 1);
    }
  };
  // Запись журнала, уже стоящая в ленте, второй раз не встаёт. Дорог у неё
  // две: та же запись под другим ключом (поток и обычный ответ считали номера
  // по-разному) и эхо из транскрипта, приехавшее раньше журнала. Обе давали
  // одну реплику двумя пузырями, и уходил дубль только с полной пересборкой
  // ленты (жалоба пользователя: «задублировалось, потом чат обновился и
  // дубляж пропал»).
  const seenSaid = (item) => {
    if (!item || !isSaid(item)) return false;
    const at = String(item.time || "");
    for (const it of talk) {
      if (it.role !== item.role) continue;
      if (isSaid(it)) {
        if (String(it.time || "") === at && sayNorm(it.text) === sayNorm(item.text)) return true;
        continue;
      }
      if (item.role === "user" && sameSay(it.text, item.text)) return true;
    }
    return false;
  };

  // Приписка сверху (страница истории) двигает всё содержимое вниз, и держать
  // место надо расстоянием до низа. Приписка снизу высоту выше взгляда не
  // трогает, и там держится сам scrollTop: мерка «до низа» уводила взгляд на
  // высоту пришедшего, и читающий историю терял строку с каждой записью агента
  // (жалоба пользователя).
  let prepending = false;
  const draw = () => {
    const bottom = atBottom(scroll);
    const rest = scroll.scrollHeight - scroll.scrollTop;
    const top = scroll.scrollTop;
    const subs = feedSubs(talk);
    const thread = feedThread(subs);
    const marks = feedMarks(talk).map((m, i) => (m + " " + subs[i]).trim());
    // Склеенная пара это одна запись с одним кружком: верхний кусок нити ей
    // достаётся от первой записи пары, нижний от второй. Метки нити едут
    // отдельным полем, а не частью подписи записи: подпись сравнивают при
    // сборке, а цвет соседа меняется и там, где сама запись не тронута, и
    // запись от этого пересобиралась бы вхолостую (сторож feed_shared).
    const threadOf = (a, b) => {
      const out = [];
      if (thread[b].includes("to-deleg")) out.push("to-deleg");
      if (thread[a].includes("ti-deleg")) out.push("ti-deleg");
      return out.join(" ");
    };
    if (!talk.length) {
      sync(box, empty ? [{ key: "empty", sign: empty, make: () => el("div", "empty", empty) }] : []);
      if (opts.onFeed) opts.onFeed(talk);
      return;
    }
    const items = [];
    let day = "";
    for (let i = 0; i < talk.length; i++) {
      const item = talk[i];
      if (opts.days) {
        const key = localDayKey(item.time);
        if (key && key !== day) {
          items.push({ key: "day-" + key, sign: key, make: () => dayEl(localDay(item.time)) });
          day = key;
        }
      }
      // Пометка ленты рисуется разделителем, тем же приёмом, что и граница
      // дня: смена модели это не чья-то реплика, а рубеж в разговоре, и
      // пузырь для неё был бы враньём. Приезжает пометка журналом разговора,
      // поэтому она переживает и перерисовку панели, и перезагрузку страницы.
      if (item.role === "mark") {
        items.push({
          key: itemKey(item),
          sign: "mark|" + item.text,
          make: () => markEl(item.text),
        });
        continue;
      }
      // Вывод инструмента идёт следом за своим вызовом и рисуется вместе с
      // ним одной карточкой: два блока подряд про один и тот же ход распухали
      // ленту вдвое (замечание 1). Склейку делает лента, а не разбор: сервер
      // отдаёт записи как они лежат в транскрипте, и пагинация назад не должна
      // зависеть от того, попал ли вывод в ту же страницу, что вызов.
      // Записи субагента идут той же лентой, что и свои: свёрнутый блок с
      // заголовком и счётом ходов был нашей выдумкой, а человек просил видеть
      // работу агента так же, как её видно в vscode. Принадлежность помечена
      // скромно: отступ с полосой слева и точка с подсказкой, чей это ход.
      const next = talk[i + 1];
      if (item.role === "tool" && next && next.role === "toolout" && opts.pair &&
        item.sub === next.sub) {
        const mark = (marks[i] + " " + marks[i + 1]).trim();
        items.push({
          key: itemKey(item),
          thread: threadOf(i, i + 1),
          sign: [item.role, item.time, item.text, next.text, item.sub || "", next.fail,
            mark].join("|"),
          make: () => feedRow(safePair(opts.pair, item, next), item, next, mark),
        });
        i++;
        continue;
      }
      items.push({
        key: itemKey(item),
        thread: threadOf(i, i),
        sign: [item.role, item.time, item.text, item.sub || "", item.fail, marks[i]].join("|"),
        make: () => feedRow(safeItem(opts.item, item), item, null, marks[i]),
      });
    }
    feedThreadPaint(sync(box, items), items);
    if (bottom) keepBottom(scroll, true);
    else if (prepending) keepPlace(scroll, rest);
    else scroll.scrollTop = top;
    // Лента перерисована, и тут же сверяется всё, что панель держит своим:
    // местный пузырь отправки снимается той же перерисовкой, которой его
    // копия встала из ленты. Сверка идёт по всему содержимому ленты, а не по
    // одной приехавшей записи: записи доезжают четырьмя дорогами, и всякая,
    // что кладёт реплику мимо разбора приехавшего куска (страница истории,
    // пересборка догоном), оставляла пузырь висеть вторым экземпляром.
    if (opts.onFeed) opts.onFeed(talk);
  };

  // Подгрузка вверх: раньше её звала кнопка, теперь зовёт прокрутка, а тело
  // осталось тем же, включая якорь через draw(). Флаг не пускает второй
  // запрос, пока висит первый: прокрутка у самого верха шлёт событие на
  // каждый пиксель.
  const loadOlder = async () => {
    if (loadingOlder || firstKey === null || atFirst) return;
    loadingOlder = true;
    const older = await api(sessionURL(project, sid) +
      "?before=" + encodeURIComponent(firstKey) + "&n=" + page);
    loadingOlder = false;
    if (gone() || !older.ok) return;
    const items = older.body.items || [];
    // Отсев по ключу тут не украшение: страницы истории приходят с сервера,
    // который каждый раз собирает ленту заново, и запись с края страницы
    // приезжала вторым разом.
    const add = items.filter((it) => fresh(it) && keep(it) && !seenSaid(it));
    if (items.length) {
      firstKey = itemCursor(items[0]);
      pages += 1;
    }
    atFirst = Boolean(older.body.start);
    talk.unshift(...add);
    prepending = true;
    draw();
    prepending = false;
    updateStart();
  };

  // Возврат глубже хвоста: свежий заход приносит только хвост (CHAT_TAIL), а
  // до ухода лента могла стоять дальше, после одной или нескольких подгрузок.
  // Без досбора той же глубины прежнее место мерилось бы против чужой, куда
  // меньшей высоты, и клампилось бы к нулю (замечание ревью DK-434). Подъём
  // истории не держит открытие: он идёт после того, как хвост уже показан и
  // поток открыт, поэтому открытию хвостом не мешает. Если подгрузка не даёт
  // прогресса (сеть отказала, гонка), цикл не крутится вхолостую и встаёт на
  // ту глубину, которую успел набрать.
  // Досбор истории при возврате на разговор это та же приписка сверху: место
  // держится расстоянием до низа, как и при обычной подгрузке.
  const restorePlace = async (place) => {
    while (!gone() && pages < (place.pages || 0) && !atFirst) {
      const was = pages;
      await loadOlder();
      if (pages === was) break;
    }
    if (gone()) return;
    keepPlace(scroll, place.rest);
  };

  // Начальный экран дорисовывается не за один заход: миниатюры вложений
  // догружаются картинками, большие блоки работы субагента меряются браузером
  // позже, разметка переставляет высоту. Прокрутка при этом уже выставлена, и
  // всякая дорисовка сдвигает содержимое из-под неё: визуально лента
  // «прыгает вверх» ровно на выросшую высоту. Держит её одна точка: пока
  // человек сам не крутанул, место переставляется заново после каждой
  // дорисовки (замечание про холодное открытие).
  let hold = null;
  const applyHold = () => {
    if (!hold || gone()) return;
    if (hold.rest === null) scroll.scrollTop = scroll.scrollHeight;
    else keepPlace(scroll, hold.rest);
  };
  // Удержание кончается либо жестом человека, либо своим сроком: дорисовки
  // начального экрана укладываются в него с запасом, а держать позицию вечно
  // значило бы отнять прокрутку.
  const HOLD_MS = 2500;
  const dropHold = () => {
    hold = null;
    for (const name of ["wheel", "touchstart", "pointerdown", "keydown"]) {
      scroll.removeEventListener(name, dropHold);
    }
  };
  const holdPlace = (rest) => {
    hold = { rest: rest === undefined ? null : rest };
    for (const name of ["wheel", "touchstart", "pointerdown", "keydown"]) {
      scroll.addEventListener(name, dropHold);
    }
    // Дорисовка картинки события прокрутки не шлёт, поэтому ловится её load;
    // событие не всплывает, и слушатель ставится на перехвате.
    scroll.addEventListener("load", applyHold, true);
    for (const ms of [30, 120, 350, 800, 1600, HOLD_MS]) setTimeout(applyHold, ms);
    setTimeout(dropHold, HOLD_MS);
    opts.live.push(dropHold);
  };

  // Место ленты пишется на каждый сдвиг прокрутки: и для будущего возврата к
  // этому разговору, и как повод подгрузить историю, когда взгляд подошёл к
  // верху. Глубина (число поднятых страниц) едет вместе с местом: без неё возврат не знает,
  // сколько истории досбирать до прежней высоты. Коробка прокрутки переживает
  // переключение вкладок разговора (тот же tp.body у соседних сессий), поэтому
  // слушатель снимается сам при уходе с ленты, иначе он копился бы с каждым
  // переключением.
  // Запас над взглядом меряется экранами коробки, а не пикселями: на телефоне
  // и на широком мониторе «полтора экрана» это разная высота, а ощущение одно.
  const ahead = () => Math.max(LOAD_MARGIN, Math.round((scroll.clientHeight || 0) * LOAD_AHEAD));
  // Подъём истории идёт с запасом и в несколько страниц: одна страница на
  // быстром листании кончается раньше, чем приедет следующая, и прокрутка
  // упиралась в край. Заход один, повторные вызовы он проглатывает сам.
  let filling = false;
  const fillAbove = async () => {
    if (filling || gone()) return;
    filling = true;
    try {
      for (let i = 0; i < LOAD_BURST; i++) {
        if (gone() || atFirst || scroll.scrollTop >= ahead()) return;
        const was = pages;
        await loadOlder();
        if (pages === was) return;
      }
    } finally {
      filling = false;
    }
  };

  const onScroll = () => {
    if (gone()) return;
    feedPlace.set(sid, { bottom: atBottom(scroll), rest: scroll.scrollHeight - scroll.scrollTop, pages });
    if (scroll.scrollTop < ahead()) fillAbove();
  };
  scroll.addEventListener("scroll", onScroll);
  opts.live.push(() => scroll.removeEventListener("scroll", onScroll));

  const first = await api(sessionURL(project, sid) + "?n=" + tail);
  if (gone()) return;
  if (first.ok) {
    const items = first.body.items || [];
    if (items.length) firstKey = itemCursor(items[0]);
    atFirst = Boolean(first.body.start);
    for (const item of items) {
      if (!fresh(item)) continue;
      if (!keep(item)) continue;
      if (seenSaid(item)) continue;
      dropSaid(item);
      talk.push(item);
      // Начальный хвост проходит через тот же onItem, что поток и догон:
      // эхо-сверка снимает местный пузырь той же перерисовкой, которой его
      // копия встаёт из транскрипта. Без этого пузырь, переживший пересборку
      // панели, стоял дублем под своей же репликой до следующего события
      // ленты (снимок пользователя с двумя «вы, 14:03»).
      if (opts.onItem) opts.onItem(item);
    }
    if (first.body.note) empty = first.body.note;
  }
  // Открытие хвостом: последние слова разговора видны сразу, листать вниз от
  // начала сессии не приходится. Место встаёт по памяти этого разговора
  // (DK-434): конец, если разговор открывается впервые или стоял внизу,
  // иначе прежнее место из feedPlace, а если оно стояло дальше хвоста, перед
  // этим досбирается та же глубина (restorePlace), не дожидаясь жеста
  // прокрутки.
  draw();
  const place = feedPlace.get(sid);
  if (place && !place.bottom) restorePlace(place);
  else scroll.scrollTop = scroll.scrollHeight;
  updateStart();
  holdPlace(place && !place.bottom ? place.rest : null);

  // Пропущенное дочитывается запросом, а не потоком: поток шлёт только новое,
  // и всё, что случилось между обрывом и переподключением, до ленты не доедет
  // никогда. Ровно это и видел человек после сна ноутбука: вкладку браузер
  // задушил, поток умер, а вернувшаяся страница показывала ленту такой, какой
  // она была двадцать минут назад, и лечилось это только F5.
  //
  // Догон просит хвост целиком (repliesCatch), а не одни пропущенные записи, и
  // сшивается с лентой по месту, а не приклеивается к её концу. Место сшивки
  // это последняя запись куска, которая уже стоит в ленте: всё после неё
  // новое, всё до неё либо уже показано, либо история глубже открытого окна.
  // Без этой сшивки возврат к вкладке выкладывал под последнюю реплику весь
  // хвост из ста шестидесяти старых записей, и человек видел в чате разговор,
  // которого там быть не должно.
  let catching = false;
  const catchUp = async () => {
    if (catching || gone()) return;
    catching = true;
    try {
      const r = await api(sessionURL(project, sid) + "?n=" + repliesCatch);
      if (!r.ok || gone()) return;
      const items = r.body.items || [];
      let cut = -1;
      for (let i = items.length - 1; i >= 0; i--) {
        if (shown.has(itemKey(items[i]))) {
          cut = i;
          break;
        }
      }
      // Знакомого в куске нет вовсе: либо лента пуста, либо разговор ушёл
      // дальше, чем длина хвоста, и между показанным и приехавшим дыра.
      // Дописывать к дыре нечестно, лента пересобирается тем же куском, как
      // это сделала бы перезагрузка страницы.
      let reset = false;
      if (cut < 0 && talk.length) {
        talk.length = 0;
        shown.clear();
        pages = 0;
        firstKey = null;
        reset = true;
      }
      let added = false;
      for (let i = cut + 1; i < items.length; i++) {
        const item = items[i];
        if (!fresh(item)) continue;
        if (firstKey === null) firstKey = itemCursor(item);
        if (!keep(item)) continue;
        if (seenSaid(item)) continue;
        dropSaid(item);
        talk.push(item);
        added = true;
        if (opts.onItem) opts.onItem(item);
      }
      if (added) {
        // Начало разговора называет только пересборка: у обычного догона
        // выше ленты стоит своя история, и признак начала к ней не относится.
        if (reset) atFirst = Boolean(r.body.start);
        draw();
        updateStart();
      }
    } catch (err) {
      // Связи нет: догоняться нечем, вернётся она, вернётся и событие online.
    } finally {
      catching = false;
    }
  };

  // Поток пересоздаётся сам: EventSource ретраит только пока жив, а
  // задушенная браузером вкладка возвращается с мёртвым объектом, у которого
  // readyState навсегда CLOSED. Пересоздание идёт вместе с дочитыванием, и
  // порядок тут важен: сперва новый поток, потом догон, иначе между ними
  // осталась бы своя щель.
  const openStream = () => {
    if (gone()) return;
    if (live.es) live.es.close();
    const es = new EventSource(sessionURL(project, sid) + "?stream=1");
    live.es = es;
    // Пустая лента приходит и событием note: разговор, поднятый при пустом
    // транскрипте, называет пустоту словами, не дожидаясь первой реплики.
    es.addEventListener("note", (ev) => {
      if (talk.length) return;
      empty = ev.data;
      draw();
    });
    es.onmessage = (ev) => {
      seen = Date.now();
      const item = JSON.parse(ev.data);
      // Свой хвост поток шлёт заново, и без отсева те же реплики легли бы в
      // ленту вторым разом. Отсев по ключу, а не по номеру: номер записи
      // считается местом в слитой ленте и от заезда к заезду плывёт.
      if (!fresh(item)) return;
      if (firstKey === null) {
        firstKey = itemCursor(item);
        updateStart();
      }
      if (!keep(item)) return;
      if (seenSaid(item)) return;
      dropSaid(item);
      talk.push(item);
      draw();
      if (opts.onItem) opts.onItem(item);
    };
    es.onerror = () => {
      if (gone()) return;
      // Обрыв это не повод рвать всё: браузер ретраит сам, и второй поток
      // рядом с его попыткой удвоил бы события. Пересоздание идёт только там,
      // где ретраить уже некому, и с паузой, чтобы упавший сервер не получил
      // очередь переподключений со всех открытых вкладок.
      if (es.readyState !== 2) return;
      setTimeout(() => {
        if (gone() || live.es !== es) return;
        openStream();
        catchUp();
      }, streamRetry);
    };
  };

  // Метка последнего события потока: по ней видно, молчит он живой тишиной или
  // умер незаметно.
  let seen = Date.now();
  openStream();

  // Возврат к вкладке и вернувшаяся сеть: догоняем хвост и поднимаем поток
  // заново, если его уже некому ретраить. Спящий ноутбук и заблокированный
  // экран это ровно этот случай.
  const wake = () => {
    if (gone()) return;
    if (document.visibilityState === "hidden") return;
    if (!live.es || live.es.readyState === 2) openStream();
    seen = Date.now();
    catchUp();
  };
  document.addEventListener("visibilitychange", wake);
  window.addEventListener("online", wake);
  opts.live.push(() => {
    document.removeEventListener("visibilitychange", wake);
    window.removeEventListener("online", wake);
  });

  // Страховка на молчащий поток: сессия работает, а событий нет дольше срока,
  // значит поток умер тихо, и хвост дочитывается опросом. Без неё оставался бы
  // случай, когда вкладка всё время на виду, а связь оборвалась так, что
  // onerror не пришёл вовсе.
  const guard = setInterval(() => {
    if (gone()) return;
    if (Date.now() - seen < streamQuiet) return;
    seen = Date.now();
    if (!live.es || live.es.readyState === 2) openStream();
    catchUp();
  }, streamQuiet);
  opts.live.push(() => clearInterval(guard));
}

// tmux: сессия работы и снимок пейна через capture-pane; событийного
// источника у снимка нет, экран перечитывает его по таймеру, пока открыт
// (решение LLD).
function wireTmux(id, card, sub) {
  const row = el("div", "tmuxrow");
  const snap = el("pre", "snap");
  card.append(row, snap);
  const load = async () => {
    const r = await api("/api/tmux");
    if (!r.ok) {
      row.replaceChildren(el("span", "error", r.body.error || "tmux не отвечает"));
      snap.textContent = "";
      return;
    }
    const list = r.body.sessions || [];
    const hit = list.find((x) => x.name === "goal-" + id || x.name === "task-" + id);
    if (!hit) {
      row.replaceChildren(el("span", "empty",
        "tmux-сессия не найдена: ни goal-" + id + ", ни task-" + id +
        (r.body.note ? " (" + r.body.note + ")" : "")));
      snap.textContent = "";
      return;
    }
    row.replaceChildren();
    row.append(el("b", "", hit.name));
    row.append(el("span", "", hit.windows + " " + plural(hit.windows, "окно", "окна", "окон")));
    if (hit.created) {
      row.append(el("span", "", "создана " + new Date(hit.created * 1000).toTimeString().slice(0, 5)));
    }
    row.append(el("span", "chip c-check", LIVE_WORD.busy));
    const pr = await api("/api/tmux/" + encodeURIComponent(hit.name));
    snap.textContent = pr.ok ? (pr.body.text || "") : (pr.body.error || "");
    if (sub) sub.textContent = hit.name;
  };
  load().catch(console.error);
  const t = setInterval(() => { load().catch(console.error); }, 5000);
  agentLive.push(() => clearInterval(t));
}

// Строка задачи по id: ищет во всех секциях доски разом, а не только в той,
// что видна фильтром. Экран агента и чат зовут её ради заголовка «Цель:»,
// когда живой работы нет и спросить не у кого (DK-296).
function boardRow(board, id) {
  for (const sec of (board && board.sections) || []) {
    for (const row of sec.rows || []) {
      if (row.id === id) return row;
    }
  }
  return null;
}

// Переписка есть только у цели: строки без заголовка «Цель:», как и id, чьей
// строки на доске не нашлось, отправкой получили бы отказ ручки. Гейт общий
// для экрана агента и чата, чтобы кнопка и прямая ссылка не разошлись снова
// (DK-296).
function isGoalRow(board, id) {
  const row = boardRow(board, id);
  return !!(row && /^Цель:/.test(row.title));
}

// Лента панели разговора по макету DK-216 («04 Переписка»): пузыри реплик с
// разделителями дней. Ход и ответы читаются из транскрипта сессии, а реплика
// человека уходит той ручкой, какую назвал сервер (решение 2 LLD DK-430).
function dayEl(date) {
  const day = el("div", "day");
  day.append(el("i"), document.createTextNode(date), el("i"));
  return day;
}

// Пометка ленты бывает с хвостом терминала: смерть сессии называет причину
// строками своей панели, и они идут под разделителем моноширинным блоком
// (DK-728). Одной строкой хвост читался бы простынёй без переносов.
function markEl(text) {
  const cut = String(text).indexOf("\n");
  if (cut < 0) return dayEl(text);
  const wrap = el("div");
  wrap.append(dayEl(String(text).slice(0, cut)), el("div", "marktail", String(text).slice(cut + 1)));
  return wrap;
}

// Подпись сидит внутри пузыря, справа внизу: снаружи она занимала свою строку
// на каждое сообщение и растягивала ленту вдвое (замечание 6 двенадцатого
// круга POC). Пустая подпись не рисуется вовсе.
function chatBubble(who, text, meta, tip, quote) {
  const wrap = el("div", "msg" + (who === "вы" ? " me" : ""));
  const bb = el("div", "bb");
  // Цитата стоит первой строкой пузыря, над словами: так её ставят
  // мессенджеры, и так она читается ответом на названное.
  if (quote) bb.append(quote);
  // Упоминания задач и документов в реплике становятся ссылками, и ведут они в
  // проект этой ленты: разговор бывает чужой, а адрес на доске свой.
  bb.append(mdRender(text, chatFeedAt()));
  const said = meta ? who + ", " + meta : who;
  const foot = el("div", "mm", said);
  // Служебная обвязка живёт подсказкой подписи: в пузыре ей места нет, а
  // терять её незачем (адрес доставки у реплики, ушедшей каналом панели).
  if (tip) withFull(foot, tip);
  // Копирование сообщения целиком той же кнопкой, что у блоков команд: реплику
  // уносят в редактор и в другой чат, а выделять её мышью из ленты неудобно, у
  // длинной ещё и прокрутка убегает (замечание пользователя). Кнопка стоит у
  // обоих пузырей, и своего, и агентского: причина одна и та же.
  if (String(text || "").trim()) foot.append(copyBtn(text));
  bb.append(foot);
  wrap.append(bb);
  return wrap;
}

// Реплика в ленте панели: слова человека и агента идут пузырями, а свёрнутый
// вызов инструмента и размышления строкой между ними. Лента тут одна на оба
// прежних экрана (решение 6 LLD DK-430): по пузырям читают разговор, по
// строкам инструментов видят, чем агент занят прямо сейчас. Прежде это
// показывала полоса tmux, у работы из чужого окна всегда пустая.
// Вызов инструмента и его вывод рисуются одной карточкой (замечание 1 седьмого
// круга POC): прежде это были два блока подряд, команда в одном и её вывод в
// другом, и лента распухала вдвое на ровном месте. Склейка идёт по соседству:
// вывод инструмента всегда стоит следом за своим вызовом.
function toolPair(call, out) {
  const name = call.tool || "инструмент";
  const args = call.args || {};
  // Каждый инструмент рисуется по-своему, как в vscode: чтение файла одной
  // строкой с диапазоном, правка диффом, команда с её выводом, остальные одной
  // строкой с главным доводом. До этого всё сводилось к виду команды, и строка
  // «Read» показывала путь так же, как Bash показывает скрипт.
  if (name === "Read") return toolOneLine(name, readSign(call, args));
  if (name === "Edit" || name === "MultiEdit" || name === "NotebookEdit") {
    return toolDiffCard(name, fileSign(call, args), "Изменено",
      diffLines(args.old_string || "", args.new_string || args.new_source || ""));
  }
  if (name === "Write") {
    return toolDiffCard(name, fileSign(call, args), "Записано",
      addedLines(args.content || ""));
  }
  // Реплика субагенту это текст, а не команда: направления у неё нет, и блок
  // под заголовком один (замечание 8). Задание субагенту устроено так же:
  // простыня заказа лезла в ленту строкой без разворота и загромождала её
  // (замечание 10 четырнадцатого круга POC).
  // Отправка человеку в панель это реплика разговора, а не ход инструмента:
  // грумер DK-509 ответил человеку каналом, в ленте это стояло строкой
  // «Пояснение вопроса -> uds:...sock», и человек вопроса в чате не увидел
  // вовсе. Такой ход помечает сервер (human), и рисуется он обычным пузырём
  // агента. Служебное (адрес доставки и признак успеха) уходит в подсказку
  // подписи: в самом пузыре ему места нет.
  if (name === "SendMessage" && call.human) {
    return humanSend(call, out, args);
  }
  // Реплика агенту это ход с ответом, как команда: сверху сама реплика, снизу
  // подтверждение доставки. Прежде она стояла телом без ответа, а рядом ту же
  // реплику повторяла рамка из бокового журнала (жалоба пользователя).
  if (name === "SendMessage") {
    return bashCard(name, call, out, args.message || args.content || call.text || "");
  }
  if (isDeleg(call)) {
    const who = args.subagent_type || "";
    const said = [call.about || "", who].filter(Boolean).join(", ");
    return bodyCard(name, said, args.prompt || call.text || "");
  }
  if (name !== "Bash") return toolOneLine(name, call.about || call.note || "");
  return bashCard(name, call, out);
}

// humanSend рисует пузырь агента из хода отправки в панель. Текст берётся из
// самого сообщения, а служебная обвязка становится подсказкой подписи: адрес
// доставки и ответ канала человеку не нужны, но и терять их незачем.
function humanSend(call, out, args) {
  const text = args.message || args.content || call.text || "";
  const said = ["доставка " + (args.to || args.recipient || "каналом сессий"),
    out && out.text ? out.text : ""].filter(Boolean).join("; ");
  return chatBubble("агент", text, "каналом панели", said,
    call.quote ? quoteEl(call) : null);
}

// Ход командой: заголовок с пояснением и блок из двух строк, вход и выход со
// стрелками. Копирование одно, при команде.
function bashCard(name, call, out, lead) {
  const cmd = lead || call.note || foldPeek(call.text || "", 200);
  const said = out && out.text ? out.text : "";
  const box = el("div", "trow2");
  const head = el("div", "thline");
  head.append(el("b", "", name));
  head.append(el("span", "tabout", call.about || ""));
  box.append(head);
  const body = el("div", "tbox");
  const top = toolLine("i-in", "tin");
  const line = el("span", "tcmd", cmd);
  line.title = cmd;
  top.append(line);
  top.append(growBtn(body));
  top.append(copyBtn((call.text || "") + (call.text && said ? "\n" : "") + said));
  body.append(top);
  if (said) body.append(toolOutLine(said));
  box.append(body);
  return box;
}

// Блок завершения фоновой работы: заголовок с сутью и свёрнутый отчёт внутри.
// Отчёт это обычный текст агента, и рисуется он разметкой, как реплика, а не
// сырой простынёй в моноширинном блоке. Шапка собирается общим сборщиком:
// подпись до двоеточия стоит жирным, сводка после него уходит в хвост с
// обрезкой, а копирование и разворот прижаты справа, как у прочих блоков.
function reportCard(head, text) {
  const box = el("div", "svc fold");
  const cut = head.indexOf(": ");
  const name = cut < 0 ? head : head.slice(0, cut);
  const sub = cut < 0 ? "" : head.slice(cut + 2);
  const top = foldHead(name, sub, text);
  const car = foldCar();
  if (text) top.append(car);
  const body = el("div", "foldb fmd");
  if (text) body.append(mdRender(text));
  body.hidden = true;
  if (text) {
    top.addEventListener("click", () => {
      body.hidden = !body.hidden;
      car.set(!body.hidden);
      box.classList.toggle("open", !body.hidden);
    });
  }
  box.append(top, body);
  return box;
}

// Ход с одним телом: строка заголовка и под ней блок с содержимым. Стрелок
// направления тут нет вовсе: уведомление харнеса и реплика субагенту это один
// текст, разбивать его на вход и выход не на чем. Плотность и кнопки те же,
// что у блока команды (замечание 8).
function bodyCard(name, about, text) {
  const box = el("div", "trow2");
  const head = el("div", "thline");
  head.append(el("b", "", name));
  head.append(el("span", "tabout", about || ""));
  box.append(head);
  if (!text) return box;
  const body = el("div", "tbox");
  const line = el("div", "tline tout tbare");
  line.append(el("pre", "ttext", text));
  line.append(growBtn(body));
  line.append(copyBtn(text));
  body.append(line);
  box.append(body);
  return box;
}

// Ход одной строкой: имя инструмента и его главный довод. Блока под ним нет
// вовсе: у чтения файла и у поиска смотреть в ленте нечего.
function toolOneLine(name, sign) {
  const box = el("div", "trow2");
  const head = el("div", "thline");
  head.append(el("b", "", name));
  const lead = el("span", "tcmd", sign);
  lead.title = sign;
  head.append(lead);
  box.append(head);
  return box;
}

// Имя файла из пути: в ленте важно, что за файл, а не где он лежит. Полный
// путь остаётся подсказкой на самой строке.
function baseName(path) {
  const parts = String(path || "").split("/");
  return parts[parts.length - 1] || String(path || "");
}

function fileSign(call, args) {
  return baseName(args.file_path || args.path || call.note || "");
}

// Подпись чтения: файл и прочитанный кусок строками, если он назван.
function readSign(call, args) {
  const file = fileSign(call, args);
  const from = Number(args.offset || 0);
  const count = Number(args.limit || 0);
  if (!count && !from) return file;
  const start = from || 1;
  const end = count ? start + count - 1 : 0;
  return file + " (строки " + start + (end ? "-" + end : " и дальше") + ")";
}

// Дифф правки: общий кусок сверху и снизу остаётся как есть, посередине снятые
// строки и поставленные. Ничего умнее построчного сравнения тут не нужно: ход
// правки читается и так, а полный текст всегда лежит в самом файле.
function diffLines(was, now) {
  const a = String(was).split("\n");
  const b = String(now).split("\n");
  let head = 0;
  while (head < a.length && head < b.length && a[head] === b[head]) head++;
  let tail = 0;
  while (tail < a.length - head && tail < b.length - head &&
    a[a.length - 1 - tail] === b[b.length - 1 - tail]) tail++;
  const out = [];
  for (let i = 0; i < head; i++) out.push(["ctx", a[i]]);
  for (let i = head; i < a.length - tail; i++) out.push(["del", a[i]]);
  for (let i = head; i < b.length - tail; i++) out.push(["add", b[i]]);
  for (let i = a.length - tail; i < a.length; i++) out.push(["ctx", a[i]]);
  return out;
}

// Запись файла целиком это тот же дифф, где всё поставлено заново.
function addedLines(text) {
  return String(text).split("\n").map((ln) => ["add", ln]);
}

// Сколько строк диффа видно до разворота: длинная правка иначе занимает экран
// целиком, а по ленте ходят взглядом.
const DIFF_LINES = 12;

// Карточка правки: строка «инструмент файл», подпись о том, что с файлом
// сделано, и сам дифф с подсветкой.
function toolDiffCard(name, file, said, lines) {
  const box = el("div", "trow2");
  const head = el("div", "thline");
  head.append(el("b", "", name));
  const lead = el("span", "tcmd", file);
  lead.title = file;
  head.append(lead);
  box.append(head);
  if (!lines.length) return box;
  box.append(el("div", "tsaid", said));
  const diff = el("div", "tdiff");
  for (const [kind, text] of lines) {
    diff.append(el("div", "dline d-" + kind, text === "" ? " " : text));
  }
  box.append(diff);
  if (lines.length > DIFF_LINES) {
    diff.classList.add("cut");
    const more = el("button", "submore", "показать целиком, строк " + lines.length);
    more.addEventListener("click", (ev) => {
      ev.stopPropagation();
      diff.classList.remove("cut");
      more.hidden = true;
    });
    box.append(more);
  }
  return box;
}

// Строка блока: направление стрелкой в первой колонке, дальше своё содержимое.
function toolLine(ico, cls) {
  const line = el("div", "tline " + cls);
  const mark = el("span", "tico");
  mark.append(icon(ico));
  line.append(mark);
  return line;
}

// Вывод хода: та же строка со стрелкой влево. Обрезка до пары строк живёт в
// стилях, а не в тексте: полный вывод остаётся в разметке, и его по-прежнему
// можно выделить и скопировать.
function toolOutLine(text) {
  const line = toolLine("i-out", "tout");
  line.append(el("pre", "ttext", text));
  return line;
}

// Приложенное выделение свёрнутым блоком при пузыре: развернуть по клику.
// Простыня постановки в ленте закрыла бы собой сам разговор.
function selFold(file, text) {
  return foldEl("selq", "выделение", text, file, text);
}

function pickFold(screen, text) {
  return foldEl("selq", "место экрана", text, screen, text);
}

// Блок работы субагента: заголовок с подписью и счётом ходов, внутри та же
// лента. Свёрнут по умолчанию, разворачивается кликом; последняя строка видна
// в заголовке, чтобы по ней было видно, чем субагент занят прямо сейчас.
// Пояс безопасности ленты: сборка одной записи не должна ронять всю ленту.
// Битая или незнакомая запись рисуется заглушкой, остальные встают как ни в чём
// не бывало, а причина уходит в консоль (регресс тринадцатого круга POC: пустой
// чат вместо разговора).
function safeItem(make, item) {
  try {
    return make(item);
  } catch (err) {
    console.error("запись ленты не отрисовалась", err);
    return el("div", "svcline", "запись не отрисовалась");
  }
}

function safePair(make, call, out) {
  try {
    return make(call, out);
  } catch (err) {
    console.error("карточка инструмента не отрисовалась", err);
    return el("div", "svcline", "запись не отрисовалась");
  }
}

// Цитата ответа. Механика «ответ на сообщение» решается так же, как в
// мессенджерах: узкой строкой внутри самого пузыря, а не полосой над полем
// ввода. Пару «реплика человека -> первый текстовый пузырь агента после неё»
// считает сервер, панель рисует готовое (поля quote, quoteKey, quoteMany).
//
// Лента, в которую ведёт цитата: разговор на экране один, и ссылка на его
// ленту нужна обработчику нажатия, живущему в пузыре.
let chatFeedBox = null;

// Подъём живого у собранной панели: её отдаёт chatPanel наружу, а слот пула
// держит его при себе и зовёт, когда человек возвращается в этот разговор.
let chatArm = null;

// Переподъём кольца в шапке, тем же порядком: опрос кольца живёт в chatLive и
// умирает любым уходом из разговора, а возврат из пула поднимает живое панели.
// Без этой памяти кольцо замирало в состоянии ухода: работа давно кончилась, а
// кружок крутился с оставшимися пунктами до обновления страницы (бага
// пользователя).
let chatRingArm = null;

// Сколько горит подсветка исходной реплики после перехода по цитате.
const QUOTE_LIT = 1000;

// quoteEl это строка цитаты в пузыре ответа: кусок реплики человека с
// обрезкой, нажатие ведёт к ней в ленте и подсвечивает её на секунду.
function quoteEl(item) {
  const said = String(item.quote || "");
  const box = el("div", "qref");
  box.append(el("span", "qbar"));
  box.append(el("span", "qtext", said));
  const many = item.quoteMany ? "; реплик было несколько, показана последняя" : "";
  box.title = "Ответ на: " + said + many;
  box.setAttribute("role", "button");
  box.setAttribute("aria-label", box.title);
  box.addEventListener("click", (ev) => {
    ev.stopPropagation();
    const node = chatFeedBox && item.quoteKey ? findKey(chatFeedBox, "k-" + item.quoteKey) : null;
    if (!node) return;
    node.scrollIntoView({ block: "center" });
    // Подсветка гаснет сама: она отвечает на вопрос «куда меня увели», и
    // держать её дольше секунды значит красить ленту без повода.
    if (node.classList) {
      node.classList.add("lit");
      setTimeout(() => { node.classList.remove("lit"); }, QUOTE_LIT);
    }
  });
  return box;
}


// Ключ записи в ленте: устойчивый ключ сервера («источник:номер в файле»),
// а у ответа без него старый номер. По этому же ключу лента отсеивает
// повторы и просит следующую страницу истории.
function itemKey(item) {
  return item.key ? "k-" + item.key : "seq-" + item.seq;
}

// Одна и та же реплика человека доезжает до ленты двумя дорогами: журналом
// отправленного (его пишет сам дашборд) и эхом из транскрипта. Сверять их
// точным совпадением строк нельзя: по дороге к транскрипту реплика обрастает
// рамкой канала и приписками заказа, а пробелы с переводами строк меняет и
// клиент, и разбор. Поэтому сверка идёт по нормализованному сырому тексту, а
// не по тому, что видно в ленте: показанное собрано разметкой, и сравнивать
// его с отправленным значит сравнивать разное.
function sayNorm(text) {
  return String(text === undefined || text === null ? "" : text).replace(/\s+/g, " ").trim();
}

function sameSay(a, b) {
  const x = sayNorm(a);
  const y = sayNorm(b);
  if (!x || !y) return false;
  return x === y || x.endsWith(y) || y.endsWith(x);
}

// То же самое, но как его понимает сервер: ключ записи либо старый номер.
// Этим значением лента и просит следующую страницу истории.
function itemCursor(item) {
  return item.key ? String(item.key) : String(item.seq);
}

// Строка ленты на общей вертикальной линии: слева тонкая серая нить через весь
// разговор, на ней кружок против первой строки записи. Так ход работы читается
// сверху вниз одной колонкой, как в vscode. Цвет кружка говорит об исходе:
// серый у нейтральных записей, зелёный у сделанного инструментом, красный у
// упавшего. Запись субагента стоит на той же нити, но глубже: работа чужая, а
// хронология общая.
// Границы работы в ленте: вертикальная линия связывает события одной работы и
// рвётся, когда работа сделана. Реплика человека начинает группу, финальный
// текст агента её закрывает, а между группами остаётся щель. Сплошная нить
// через весь разговор читалась одной бесконечной работой, и глазом было не
// найти, где кончился один заход и начался другой (замечание пользователя).
//
// Финальный текст это последний текст агента до следующей реплики человека:
// пока агент ходит инструментами, работа идёт, и рвать нить рано. Не сказавшая
// ни слова группа (агент ещё работает) не закрывается вовсе.
function feedMarks(list) {
  const marks = list.map(() => "");
  let start = -1;
  const close = (upto) => {
    for (let j = upto; j > start; j--) {
      const it = list[j];
      if (it.role === "assistant" && String(it.text || "").trim()) {
        marks[j] = (marks[j] + " gend").trim();
        return;
      }
    }
  };
  for (let i = 0; i < list.length; i++) {
    if (list[i].role !== "user") continue;
    if (start >= 0) close(i - 1);
    marks[i] = (marks[i] + " gtop").trim();
    start = i;
  }
  if (start >= 0) close(list.length - 1);
  return marks;
}

// Работа субагента идёт отрезком, а не россыпью записей: начинает её вызов
// Agent, дальше боковой журнал перемежается ходами диспетчера, и кончается всё
// вестью «фоновый агент завершил работу». Кружки у обоих концов синие, они и
// есть границы работы. Нить, помеченная по одной только записи журнала,
// начиналась не с того кружка и рвалась на каждом ходе диспетчера посередине:
// «синяя нить начинается не с сообщения субагента, а со следующего сообщения и
// завершается, не доходя до блока про конец фоновой работы». Отсюда
// отрезок считается по всему списку: subtop это первая запись работы, subend
// последняя, sub всё, что между ними.
function feedSubs(list) {
  const out = list.map(() => "");
  // Вызов, ещё не подтверждённый ни одной записью журнала: субагент мог и не
  // подняться, и красить нитью один вызов нечего.
  let call = -1;
  let span = -1;
  let last = -1;
  const close = (end) => {
    if (span < 0 || end < span) return;
    for (let j = span; j <= end; j++) out[j] = "sub";
    out[span] += " subtop";
    out[end] += " subend";
    span = -1;
    call = -1;
    last = -1;
  };
  for (let i = 0; i < list.length; i++) {
    const it = list[i];
    if (it.sub) {
      if (span < 0) span = call >= 0 ? call : i;
      last = i;
      continue;
    }
    // Конец работы диспетчер пишет своей записью, и она в отрезок входит: это
    // тот же кружок передачи, только обратной.
    if (span >= 0 && it.role === "note" && it.mark === "agent") {
      close(i);
      continue;
    }
    if (span < 0 && isDeleg(it)) call = i;
  }
  close(last);
  return out;
}

// feedThread красит нить между кружками. Отрезок это промежуток от кружка
// записи до кружка следующей, и цвет у него один: синий, пока идёт работа
// субагента, общий во всё остальное время. У записи от этого два куска нити,
// верхний от прошлого отрезка и нижний от своего, и меняются они ровно на
// кружке. Считать это в стилях нечем: соседей знает только лента, а класса
// «предыдущая была такой-то» в CSS нет.
function feedThread(subs) {
  // Отрезок subend это последний кружок работы: ниже него нить уже общая.
  const deleg = subs.map((m) => m.includes("sub") && !m.includes("subend"));
  return subs.map((_, i) => {
    const marks = [];
    if (deleg[i]) marks.push("to-deleg");
    if (i > 0 && deleg[i - 1]) marks.push("ti-deleg");
    if (i === 0) marks.push("thead");
    if (i === subs.length - 1) marks.push("ttail");
    return marks.join(" ");
  });
}

// feedThreadPaint красит нить по собранной ленте. Классы ставятся после сборки,
// а не при рождении записи: цвет зависит от соседей, а соседи меняются и от
// подгруженной страницы истории, и от новой записи в конце. Края ленты
// считаются тут же: выше первого кружка и ниже последнего нити нет.
function feedThreadPaint(shown, items) {
  const rows = [];
  for (let i = 0; i < shown.length; i++) {
    const node = shown[i];
    const marks = " " + ((items[i] && items[i].thread) || "") + " ";
    for (const cls of ["to-deleg", "ti-deleg"]) {
      node.classList.toggle(cls, marks.includes(" " + cls + " "));
    }
    if (String(node.className || "").split(" ").includes("frow")) rows.push(node);
  }
  for (let i = 0; i < rows.length; i++) {
    rows[i].classList.toggle("thead", i === 0);
    rows[i].classList.toggle("ttail", i === rows.length - 1);
  }
}

function feedRow(node, item, out, mark) {
  const row = el("div", "frow r-" + (item.role || "") +
    (mark ? " " + mark : ""));
  const dot = el("span", "fdot " + dotKind(item, out));
  if (item.sub) {
    dot.title = "субагент: " + item.sub;
    dot.setAttribute("aria-label", dot.title);
  }
  // Класс тут свой, не общий fbody: тем именем зовётся тело панели файла, и
  // от него строка ленты получала чужие поля в 14 и 18 пикселей. Отсюда и
  // расходились кружки на широком экране: у чужого правила своя телефонная
  // поправка, и высота первой строки записи ехала за шириной окна.
  const body = el("div", "frowb");
  body.append(node);
  row.append(dot, body);
  row.className += " " + leadKind(node);
  return row;
}

// Высоту первой строки записи задаёт не роль, а то, чем запись нарисована:
// пузырь, заголовок свёрнутого блока, строка инструмента или голая строка
// текста. Роль тут обманывает: размышление с текстом рисуется свёрнутым
// блоком, а без текста голой строкой, и кружок у второго уезжал вниз на
// высоту рамки с полями (замечание 1).
function leadKind(node) {
  const cls = " " + String((node && node.className) || "") + " ";
  if (cls.indexOf(" fold ") >= 0) return "f-fold";
  if (cls.indexOf(" trow2 ") >= 0) return "f-head";
  if (cls.indexOf(" msg ") >= 0 || cls.indexOf(" turn ") >= 0) return "f-bub";
  if (cls.indexOf(" tline ") >= 0) return "f-tline";
  return "f-line";
}

// Вызов, которым работа уходит агенту. Имя зависит от харнеса и от того, что
// именно делается: Agent и Task поднимают субагента, SendMessage продолжает уже
// поднятого. Работа уходит в обоих случаях, и метка у них одна: в ленте
// пользователя делегирование идёт как раз продолжениями, и серая точка на них
// рвала пару «ушло, вернулось» (замечание по снимку).
const delegTools = ["Agent", "Task", "SendMessage"];

function isDeleg(item) {
  // Отправка человеку в панель работы никому не передаёт: это реплика
  // разговора, и синей метки передачи у неё нет.
  if (item && item.human) return false;
  return Boolean(item && item.role === "tool" && delegTools.includes(item.tool));
}

// Исход записи цветом: пока ответа инструмента нет, ход считается идущим и
// кружок нейтрален, ошибка инструмента приходит признаком fail.
// Работа, отданная субагенту, и весть о том, что фоновый агент её закончил, это
// одно событие с двух концов, и метка у них общая, синяя: в ленте видно, куда
// работа ушла и когда вернулась (замечание 9). Начало узнаётся по имени
// инструмента, конец по машинной пометке записи, а не по словам заголовка.
function dotKind(item, out) {
  if (isDeleg(item)) return "deleg";
  // Реплика, доехавшая до работающего субагента, это та же передача работы:
  // служебная строка из бокового журнала носит ту же метку, что и вызов.
  if (item.role === "note" && (item.mark === "agent" || item.sub)) return "deleg";
  if (item.role !== "tool") return item.role === "toolout" && item.fail ? "bad" : "";
  if (!out) return "";
  return out.fail ? "bad" : "ok";
}

function chatItem(item) {
  // Пустой ответ инструмента в ленту не идёт: он есть только затем, чтобы
  // сервер видел закрытый вызов.
  if (item.role === "toolout" && !item.text) return el("span", "");
  if (item.role === "note") return replyEl(item);
  if ((item.role === "user" || item.role === "assistant") && item.text) {
    // Реплика, пришедшая каналом живых сессий, подписана источником: «с
    // дашборда» это реплика самого человека, он её и написал, а дашборд только
    // донёс. Обёртку канала сервер уже снял, тут остаётся чистый текст.
    // Автора называет сервер, когда он не тот, о ком говорит роль: каналом
    // живых сессий в ленту приходят слова другого агента, а роль у записи всё
    // равно user. «Вы» остаётся только у того, что человек написал сам, и
    // пузырь у чужих слов нейтральный, не пользовательский: подпись врала
    // заодно с цветом (замечание пользователя).
    const who = item.who || (item.role === "user" ? "вы" : "агент");
    // «из транскрипта» человеку не говорит ничего: он и так читает ленту
    // разговора. Остаётся время, а при нём источник, если он не обычный.
    const bits = [];
    if (item.time) bits.push(localTime(item.time));
    if (item.note) bits.push(item.note);
    if (item.sel) bits.push("с выделением");
    if (item.pick) bits.push("с местом экрана");
    const wrap = chatBubble(who, item.text, bits.join(", "), "", item.quote ? quoteEl(item) : null);
    if (item.sel) wrap.append(selFold(item.selFile || "постановка", item.sel));
    // Места экрана стоят при пузыре своим свёрнутым блоком: в самой реплике им
    // места нет, а терять описатель незачем, по нему находят место в коде.
    if (item.pick) wrap.append(pickFold(item.pickScreen || "", item.pick));
    if (item.shot) {
      // Картинка лежит файлом на машине, и браузеру её отдаёт ручка вложений.
      const thumb = shotThumb(shotURL(item.shot), baseName(item.shot));
      thumb.addEventListener("error", () => {
        thumb.hidden = true;
        wrap.append(el("div", "svcline", "картинка не открылась: " + item.shot));
      });
      wrap.append(thumb);
    }
    return wrap;
  }
  return replyEl(item);
}

// Лента панели: последние реплики видны сразу, история подгружается сама от
// прокрутки вверх (DK-434). Разговор тут уже выбран (его адрес разбирает
// chatState), и сама лента приезжает общим куском (wireFeed): панели она
// достаётся с разделителями дней и своим поколением живых потоков, чтобы
// перерисованный рядом экран её не гасил.
// Адрес ленты для миниатюр вложений: chatItem рисуется без контекста, а ручка
// картинки живёт при чате, и её надо чем-то назвать.
let chatShotProject = "";
let chatShotSid = "";

// Адрес картинки собирается из её же пути: там стоит сессия-владелец, а лента
// бывает открыта другой (разговор продолжили резюмом, реплику принесло из
// чужой сессии). Взятая с открытой сессии, ручка отвечала 404, и в ленте
// оставался значок битого изображения (замечание тринадцатого круга POC).
// Вложенный снимок в ленте: миниатюра, которая по нажатию разворачивается во
// весь экран. Полноразмерная картинка прямо в ленте съедала бы её целиком, а
// маленькая без разворота нечитаема: на снимке человек показывает мелочь вроде
// сдвинутого кружка (замечание 5 четырнадцатого круга POC).
function shotThumb(src, name) {
  const thumb = el("img", "mshot");
  thumb.alt = "вложенный снимок";
  thumb.title = "Открыть снимок целиком";
  thumb.src = src;
  thumb.addEventListener("click", () => { shotOpen(src, name); });
  return thumb;
}

// Разворот снимка поверх страницы. Узел живёт при body, а не в ленте: лента
// прокручивается и режется своей коробкой, и картинка внутри неё разворачиваться
// некуда. Закрывается нажатием куда угодно и клавишей Esc.
let shotLens = null;

function shotOpen(src, name) {
  shotShut();
  const box = el("div", "shotbig");
  box.id = "shotbig";
  box.setAttribute("role", "dialog");
  box.setAttribute("aria-label", "снимок " + (name || "чата"));
  const pic = el("img");
  pic.alt = "снимок целиком";
  pic.src = src;
  box.append(pic);
  const shut = el("button", "shotx");
  shut.title = "Закрыть снимок";
  shut.setAttribute("aria-label", shut.title);
  shut.append(icon("close"));
  box.append(shut);
  box.addEventListener("click", () => { shotShut(); });
  shotLens = box;
  document.body.append(box);
  document.addEventListener("keydown", shotKey);
}

function shotKey(ev) {
  if (ev.key === "Escape") shotShut();
}

function shotShut() {
  const box = shotLens;
  shotLens = null;
  document.removeEventListener("keydown", shotKey);
  if (!box) return;
  if (box.parentElement && box.parentElement.removeChild) box.parentElement.removeChild(box);
  else if (document.body.removeChild) document.body.removeChild(box);
}

function shotURL(path) {
  const parts = String(path || "").split("/").filter(Boolean);
  const name = parts[parts.length - 1] || "";
  const owner = parts.length > 1 ? parts[parts.length - 2] : chatShotSid;
  return chatsURL(chatShotProject) + "/" + encodeURIComponent(owner) +
    "/shot?name=" + encodeURIComponent(name);
}

function wireChatFeed(project, feed, sid, onItem, onFeed, keep) {
  chatShotProject = project;
  chatShotSid = sid;
  chatFeedIn(project);
  return wireFeed(project, sid, {
    onItem,
    onFeed,
    keep,
    box: feed,
    scroll: feed,
    list: "mlist",
    tail: CHAT_TAIL,
    days: true,
    item: chatItem,
    pair: toolPair,
    // Пустая лента остаётся пустой без приписки: пустой чат и так выглядит
    // пустым, а «в транскрипте нет ни одной реплики» читалось поломкой
    // (замечание пользователя).
    empty: "",
    live: chatLive,
    era: () => chatGen,
  });
}

// Состояния своей реплики. Строка встаёт в ленту сразу, ещё до ответа
// сервера, и сама говорит, что с ней: на слабой связи молчание с надписью в
// углу экрана неотличимо от непрошедшей отправки, и человек жмёт «Отправить»
// второй раз. Нажимать на самой реплике нечего: неушедшее дожимает дашборд,
// как это делают телеграм с сигналом, а человеку остаётся смотреть.
const SENT_META = {
  queued: "в очереди",
  waiting: "ждёт витка",
  delivered: "доставлено агенту",
  read: "прочитано агентом",
  // Реплика разговора и реплика задачи ложатся строкой во вход, и списка
  // лежащего у этих ручек нет: обещать «доставлено» по ним нечем, а честное
  // состояние это лежащая строка, которую заберёт ближайший ход.
  lying: "лежит во входе чата",
};

// Доставка приходит без человека: подхват (hooks/chat-in.py) вносит лежащую
// строку прямо в идущий виток, и узнать об этом дашборд может только
// перечитыванием «Входящих». Шаг короткий, чтение дешёвое, а без него
// доставленная реплика выглядела бы ждущей до следующего открытия чата.
const OUTBOX_POLL = 15000;

// Подпись доставленной реплики. Время у отметки есть всегда, а сессия не
// всегда: строку, съеденную вопросом витка (--ask), не называл никакой виток,
// и пустая скобка там была бы вопросом без ответа.
function deliveredMeta(mark) {
  if (!mark) return SENT_META.delivered;
  const at = mark.at ? localTime(mark.at) : "";
  return SENT_META.delivered + (at ? " в " + at : "") +
    (mark.session ? ", сессия " + String(mark.session).slice(0, 8) : "");
}

// Шаг повтора: первая задержка короткая, дальше удвоение до потолка, чтобы
// сутки без связи не превратились в тысячи запросов. Через OUTBOX_STUCK
// подпись реплики перестаёт молчать о причине и начинает считать время:
// молчаливое «в очереди» на пятой минуте неотличимо от отправленного.
const OUTBOX_FIRST = 2000;
const OUTBOX_MAX = 60000;
const OUTBOX_STUCK = 60000;

// Свои отправки помнит браузер (приём DK-246): отметок прочитанного у сервера
// нет, «Входящие» знают только лежащее, а подхваченная витком строка из них
// просто уходит. Дашборд держит список своих реплик сам и по пропаже строки
// из «Входящих» показывает её прочитанной; в другом браузере такого следа не
// будет, зато своего источника правды дашборд не заводит, состояние остаётся
// в файле цели.
const SENT_KEY = "devkit.chat.sent.";
const SENT_MAX = 50;

function sentRead(project, id) {
  try {
    const raw = JSON.parse(localStorage.getItem(SENT_KEY + project + "/" + id) || "[]");
    if (!Array.isArray(raw)) return [];
    return raw.filter((m) => m && m.text).map((m) => ({
      text: m.text,
      // Записи прошлой версии дашборда («отправляется...» и «не ушло»)
      // становятся очередью: их как раз и надо дожать, а не выбросить.
      state: SENT_META[m.state] ? m.state : "queued",
      at: m.at || "",
      line: m.line || "",
      since: m.since || Date.now(),
    }));
  } catch (err) {
    // Приватное окно запрещает хранилище, битую запись читать тоже нечем:
    // чат тогда живёт без памяти о прошлых отправках, но работает.
    return [];
  }
}

// Здесь же и живёт очередь исходящих: неушедшая реплика лежит в том же
// списке состоянием «в очереди», поэтому перезагрузка страницы её не теряет,
// а следующее открытие чата подхватывает и дожимает.
function sentWrite(project, id, list) {
  try {
    const keep = list.slice(-SENT_MAX).map((m) => ({
      text: m.text, state: m.state, at: m.at, line: m.line || "", since: m.since || 0,
    }));
    localStorage.setItem(SENT_KEY + project + "/" + id, JSON.stringify(keep));
  } catch (err) {
    return;
  }
}

// Ручка сообщения цели: реплика ложится в раздел «Входящие» файла цели, и
// адрес этот у очереди исходящих параметром, а не в её теле. Панель разговора
// (DK-435) поднимает ту же очередь над другой ручкой.
function goalMessageURL(project, id) {
  return "/api/projects/" + encodeURIComponent(project) +
    "/goals/" + encodeURIComponent(id) + "/message";
}

// Отправленное человеком под лентой чата: свои реплики со своими состояниями
// плюс чужие строки «Входящих» (их мог положить другой браузер или рука).
// Пустота говорит словами: пустая коробка неотличима от неотрисованной.
function makeOutbox(project, id, box, url, onLive, opts) {
  // Список лежащего есть только у ручки цели: «Входящие» читаются тем же
  // адресом, каким пишутся. У ручек разговора и задачи чтения нет вовсе, и
  // очередь тогда живёт без сверки: отправленная реплика подписана лежащей во
  // входе, а не выдуманным «доставлено».
  const readable = !(opts && opts.read === false);
  const mine = sentRead(project, id);
  let others = [];
  // Отметки доставки лежащих строк, ключ это строка «Входящих» целиком.
  let marks = new Map();
  let empty = "во «Входящих» пусто: непрочитанных сообщений нет";
  let failed = "";
  // Заход очереди идёт по одному: два параллельных дожима слали бы один и тот
  // же текст дважды. told помнит, о какой реплике человеку уже сказали, чтобы
  // повторы не молотили флешем в углу.
  let timer = null;
  let wait = OUTBOX_FIRST;
  let pumping = false;
  let stopped = false;
  const told = new Set();

  // Долгая неудача не выглядит отправленной: после OUTBOX_STUCK подпись
  // называет причину и считает минуты, а до того «в очереди» одинаково
  // подходит и идущему запросу, и первым неудачным попыткам.
  const label = (m) => {
    if (m.state === "delivered") return deliveredMeta(m.mark);
    if (m.state !== "queued") return SENT_META[m.state];
    const held = m.since ? Date.now() - m.since : 0;
    if (held < OUTBOX_STUCK) return SENT_META.queued;
    return "в очереди " + Math.max(1, Math.round(held / 60000)) + " мин, связи нет";
  };

  const bubble = (m) => {
    const wrap = chatBubble("вы", m.text, (m.at ? m.at + ", " : "") + label(m));
    wrap.classList.add("m-" + m.state);
    if (m.state === "queued" && label(m) !== SENT_META.queued) wrap.classList.add("m-stuck");
    return wrap;
  };

  const draw = () => {
    box.replaceChildren();
    // Чужая строка тоже несёт своё состояние: положить её мог другой браузер
    // или рука, а доставка у неё та же самая.
    for (const line of others) {
      box.append(chatBubble("вы", line,
        marks.has(line) ? deliveredMeta(marks.get(line)) : "ждёт витка"));
    }
    for (const m of mine) box.append(bubble(m));
    // Пустота говорится словами там, где лежащее вообще читается: у ручек без
    // чтения пустая коробка это просто отсутствие своих реплик, и слова про
    // «Входящие» там были бы о чужом предмете.
    if (readable && !others.length && !mine.length) box.append(el("div", "empty", empty));
    if (failed) box.append(el("div", "error", failed));
  };

  // Сверка с «Входящими»: своя строка на месте и с отметкой значит
  // «доставлено агенту», на месте без отметки значит «ждёт витка», пропавшая
  // значит подхваченная, и след её остаётся в ленте прочитанным.
  const read = async () => {
    if (!readable) return;
    let r;
    try {
      r = await api(url);
    } catch (err) {
      // Обрыв связи и на чтении говорит словами: пустая коробка под лентой
      // неотличима от опустевших «Входящих».
      failed = "«Входящие» не прочитались: " + (err && err.message ? err.message : "связи нет");
      draw();
      return;
    }
    if (!r.ok) {
      failed = r.body.error || "«Входящие» не прочитались";
      draw();
      return;
    }
    failed = "";
    const pending = r.body.pending || [];
    if (r.body.note) empty = r.body.note;
    marks = new Map((r.body.delivered || []).map((mark) => [mark.line, mark]));
    // Живость витка сервер судит правилом подхвата, а не списком работ
    // экрана, и плашка чата берёт ответ отсюда. Ответ без поля живости
    // (старый сервер) плашку не трогает: выдуманное «доставим за минуты»
    // хуже прежних слов про следующий виток.
    if (onLive && typeof r.body.live === "boolean") onLive(r.body.live);
    const known = new Set(mine.map((m) => m.line).filter(Boolean));
    others = pending.filter((line) => !known.has("- " + line));
    for (const m of mine) {
      if (m.state === "queued") continue;
      const line = String(m.line).replace(/^- /, "");
      if (!pending.includes(line)) {
        m.state = "read";
        m.mark = null;
        continue;
      }
      m.mark = marks.get(line) || null;
      m.state = m.mark ? "delivered" : "waiting";
    }
    sentWrite(project, id, mine);
    draw();
  };

  // Перечитывание идёт по кругу, пока чат открыт: доставку делает не человек,
  // и без своего отсчёта состояние реплики менялось бы только следующей
  // отправкой. Заход один: таймер заводится в конце чтения, а не рядом с ним.
  let poll = null;
  const load = async () => {
    try {
      await read();
    } finally {
      if (readable && !stopped && poll === null) {
        poll = setTimeout(() => { poll = null; load().catch(console.error); }, OUTBOX_POLL);
      }
    }
  };

  // Неудача оставляет реплику в очереди: дожимать её будет pump, а человеку
  // говорится один раз, дальше за состояние отвечает сама реплика.
  const hold = (m, said) => {
    m.state = "queued";
    if (!told.has(m)) {
      told.add(m);
      sayResult(said, true);
    }
    sentWrite(project, id, mine);
    draw();
    return false;
  };

  const post = async (m) => {
    let r;
    try {
      r = await api(url, { method: "POST", body: { text: m.text } });
    } catch (err) {
      // Оборванная связь это не ответ со статусом: fetch бросает исключение, и
      // без перехвата реплика вылетела бы из очереди с необработанной ошибкой.
      // Ровно этот случай видно с телефона в авиарежиме.
      return hold(m, "сообщение в очереди: " + (err && err.message ? err.message : "связи нет"));
    }
    let said = r.body.message || r.body.error || "";
    if (r.ok && r.body.note) said += " (" + r.body.note + ")";
    if (!r.ok) return hold(m, said || "сообщение в очереди, отправлю снова");
    sayResult(said);
    m.line = r.body.line || "";
    m.state = readable ? "waiting" : "lying";
    // Повтор сервер кладёт в ту же строку «Входящих», и второй пузырь на неё
    // был бы тем же обманом, что и вторая строка в файле цели.
    const twin = mine.findIndex((o) => o !== m && o.line && o.line === m.line);
    if (twin >= 0) mine.splice(mine.indexOf(m), 1);
    sentWrite(project, id, mine);
    draw();
    await load();
    return true;
  };

  // Задержка перед следующим заходом очереди: растёт от неудачи к неудаче,
  // сбрасывается удачной отправкой и возвращением сети.
  const plan = () => {
    if (stopped || timer) return;
    timer = setTimeout(() => { timer = null; pump().catch(console.error); }, wait);
    wait = Math.min(wait * 2, OUTBOX_MAX);
  };

  // Очередь дожимается сама: пока в списке есть «в очереди», дашборд пробует
  // отправку снова, по растущей задержке, по событию online и при следующем
  // открытии чата. Дублей от этого не будет, и держится это свойство на
  // сервере: ручка message узнаёт свою неподхваченную строку во «Входящих» и
  // второй раз её не кладёт (pendingSame в messages.go, DK-281). Сломав там
  // узнавание, здесь получат по строке на каждый повтор.
  //
  // Флаг stopped проверяется после каждого ожидания, и это не перестраховка:
  // уход с экрана застаёт цикл на await, снятого таймера ему мало, и без
  // проверки он доживал бы до следующего plan(). Вернувшийся в тот же чат
  // человек поднял бы вторую очередь на ту же запись, и один текст слали бы
  // два цикла разом (замечание ревью DK-287).
  const pump = async () => {
    if (pumping || stopped) return;
    pumping = true;
    try {
      for (;;) {
        const m = mine.find((o) => o.state === "queued");
        if (!m || stopped) return;
        const sent = await post(m);
        if (stopped) return;
        if (!sent) {
          plan();
          return;
        }
        wait = OUTBOX_FIRST;
      }
    } finally {
      pumping = false;
    }
  };

  // Вернувшаяся сеть это повод не ждать отсчёта: телефон вышел из метро, и
  // сообщение уходит сразу.
  const wake = () => {
    if (stopped) return;
    wait = OUTBOX_FIRST;
    if (timer) {
      clearTimeout(timer);
      timer = null;
    }
    pump().catch(console.error);
  };
  window.addEventListener("online", wake);

  const stop = () => {
    stopped = true;
    if (timer) clearTimeout(timer);
    timer = null;
    if (poll) clearTimeout(poll);
    poll = null;
    window.removeEventListener("online", wake);
  };

  const send = async (text) => {
    const m = {
      text,
      state: "queued",
      at: localTime(new Date().toISOString()),
      line: "",
      since: Date.now(),
    };
    mine.push(m);
    if (mine.length > SENT_MAX) mine.splice(0, mine.length - SENT_MAX);
    // Запись до первой попытки: закрытая на полуслове вкладка не должна
    // унести текст с собой.
    sentWrite(project, id, mine);
    draw();
    await pump();
    return m.state !== "queued";
  };

  return { load, send, draw, pump, stop };
}

async function sendMessage(project, id, ta, out) {
  const text = ta.value.trim();
  if (!text) return;
  ta.value = "";
  sayResult("отправка сообщения для " + id + "...");
  await out.send(text);
}

// Плашка над полем ввода. При идущем цикле в ней сказано, когда агент
// прочитает сообщение, и закрывается она крестиком: прочитав её однажды,
// держать её над полем незачем. При стоящем цикле в ней сказано прямо, что
// читать сообщение некому, и рядом стоит та же ручка подъёма витка, что кнопка
// в ленте: до DK-319 строка ложилась во «Входящие» с молчаливым «ждёт витка», а
// человек ждал ответа от закончившего работу агента. Отказ крестиком не
// закрывается, спрятанный отказ это то же молчание. Признак running приходит
// той же работой цели, какую сервер считает живой (goalIdle в messages.go):
// цикл в чужом окне и цель из реестра для плашки живые, хотя кнопка стопа их и
// не берёт.
//
// Минуты до доставки плашка обещает не по этому признаку, а по живости витка
// с ручки сообщения (goalLive в mail.go, DK-136): убитая оболочка запись
// реестра не снимает, и цель с мёртвым циклом держит на экране работу, до
// которой доставлять некому. Живость приходит перечитыванием «Входящих» и
// живёт на самой плашке, поэтому перерисовка по доске её не теряет. Плашка
// называет доставку, а не действие: до идущего витка реплика доходит за
// минуты, а развернуть работу он может и позже, на границе шага.
function fillChatNote(note, running, live) {
  const said = findKey(note, "chat-said");
  const start = findKey(note, "chat-start");
  const close = findKey(note, "chat-close");
  if (!said || !start || !close) return;
  if (live !== undefined) note.dataset.live = live ? "1" : "0";
  note.dataset.running = running ? "1" : "";
  said.replaceChildren();
  if (running && note.dataset.live === "1") {
    said.append(el("b", "", "Сообщение уйдёт агенту."));
    said.append(document.createTextNode(
      " Идущий виток получит его за минуты, на ближайшем законченном ходе, " +
      "а развернуть работу может и позже, на границе шага."));
  } else if (running) {
    said.append(el("b", "", "Сообщение уйдёт агенту."));
    said.append(document.createTextNode(" Он отреагирует на него на следующей рабочей итерации."));
  } else {
    said.append(el("b", "", "Цикл цели не идёт."));
    said.append(document.createTextNode(
      " Сообщение ляжет во «Входящие» файла цели и будет лежать там, пока виток не поднят."));
  }
  note.className = running ? "cnote" : "cnote idle";
  start.hidden = running;
  close.hidden = !running;
  if (!running) note.hidden = false;
}


// ---- Панель разговора (LLD DK-430, решения 5 и 6) ----
//
// Разговор открывается панелью справа поверх любого экрана проекта, и доска
// остаётся домом. Прежде разговор жил двумя экранами: «Живой статус» показывал
// ленту без поля ввода, «Чат с агентом» ту же ленту с полем, но заведён был
// одним целям, а с экрана задачи разговор не открывался вовсе. Теперь это один
// элемент: одна лента, одна очередь исходящих, одно поле ввода. Этап работы,
// журнал витка, кнопка стопа и признаки живости остались на экране задачи, где
// они и есть предмет, а полоса tmux убрана совсем.

// Ширина панели одна на весь дашборд, а не на задачу: человек ставит её под
// свой экран, а не под предмет разговора.
// Диапазон меряет ленту, а не панель целиком: колонки списка разговоров в
// панели больше нет, и --cw достаётся ленте без остатка. Пока колонка стояла
// призраком, те же 320..640 давали ленте 148..468, и панель почти не
// раздвигалась (замечание 3).
// Верхнего предела в точках у панели больше нет: разговор бывает главным
// делом экрана, а доска при нём справкой, и упор в 640 точек на широком
// мониторе мешал (замечание пользователя). Потолок теперь меряется самим
// окном: панель тянется почти во весь экран, оставляя доске узкую полосу.
// Нижний предел остался прежним: уже 320 точек лента нечитаема.
const CHAT_W_KEY = "devkit.chat.width";
const CHAT_W_MIN = 320;
const CHAT_W_DEF = 420;
// Полоса доски, которая остаётся видной при самой широкой панели: за неё
// берутся, чтобы вернуть панель назад, и съедать её целиком незачем.
const CHAT_W_KEEP = 72;

function chatMax() {
  const win = typeof window !== "undefined" && window.innerWidth ? window.innerWidth : 0;
  if (!win) return CHAT_W_MIN * 4;
  return Math.max(CHAT_W_MIN, win - CHAT_W_KEEP);
}

function chatClamp(w) {
  return Math.max(CHAT_W_MIN, Math.min(chatMax(), Math.round(w) || CHAT_W_DEF));
}

// Ширина уезжает в корень переменной, а не в стиль самой панели: медиазапросу
// узкого экрана переменную перебить нечем, а вот объявление ширины он меняет
// на свою, и панель занимает экран целиком без спора со стилем узла.
function putChatWidth(w) {
  const px = chatClamp(w);
  document.documentElement.style.setProperty("--cw", px + "px");
  // Панель отнимает ширину у доски рядом, и место таблицы становится другим:
  // колонки укладываются в него тут же, иначе строка вылезала бы за карточку
  // на всё время, пока панель открыта.
  tblWidthsAll();
  return px;
}

// Память ширины хранит запрошенное человеком, а обрезает его окно при чтении:
// панель, растянутая на широком мониторе, не должна усыхать навсегда от
// одного захода с ноутбука.
function chatWidth() {
  let saved = 0;
  try {
    saved = Number(localStorage.getItem(CHAT_W_KEY)) || 0;
  } catch (err) {
    // Приватное окно запрещает хранилище: панель тогда живёт шириной по
    // умолчанию, но работает.
    saved = 0;
  }
  return chatClamp(saved || CHAT_W_DEF);
}

function saveChatWidth(w) {
  try {
    localStorage.setItem(CHAT_W_KEY, String(Math.max(CHAT_W_MIN, Math.round(w) || CHAT_W_DEF)));
  } catch (err) {
    return;
  }
}

// Хват за левый край панели: тянут её к середине экрана, поэтому ширина это
// расстояние от правого края окна до пальца. Захват указателя нужен затем,
// чтобы быстрый жест не терял панель, уехав курсором на ленту.
// Хват ширины панели. Тянуть его можно только с зажатой кнопкой, и это
// проверяется на каждом движении, а не одним флагом: отпускание за пределами
// окна, над чужим фреймом или потеря захвата оставляли флаг поднятым, и панель
// потом сужалась от одного проведения курсора над полоской, без нажатия вовсе
// (замечание 7 четвёртого круга POC).
// Хват высоты поля ввода: тянут за полосу над полем, поле растёт вверх. Мера
// та же, что у хвата ширины панели: тяга проверяется по зажатой кнопке на
// каждом движении, а не одним флагом, иначе потерянное отпускание оставляет
// поле тянущимся за курсором.
const TA_MIN = 44;
const TA_MAX = 420;

function wireTaGrip(grip, ta, done) {
  let from = 0;
  let base = 0;
  let held = false;
  const height = () => {
    const box = ta.getBoundingClientRect ? ta.getBoundingClientRect() : null;
    return box && box.height ? box.height : ta.offsetHeight || TA_MIN;
  };
  const set = (h) => {
    ta.style.height = Math.max(TA_MIN, Math.min(TA_MAX, Math.round(h))) + "px";
  };
  grip.addEventListener("pointerdown", (ev) => {
    if (ev.button !== undefined && ev.button !== 0) return;
    held = true;
    from = ev.clientY;
    base = height();
    if (grip.setPointerCapture) grip.setPointerCapture(ev.pointerId);
    if (ev.preventDefault) ev.preventDefault();
  });
  grip.addEventListener("pointermove", (ev) => {
    if (!held) return;
    if (ev.buttons === 0) {
      held = false;
      return;
    }
    // Тянут вверх, поле растёт: разница берётся с обратным знаком, потому что
    // ось экрана считает вниз.
    set(base + (from - ev.clientY));
  });
  const release = () => {
    if (held && done) done(height());
    held = false;
  };
  grip.addEventListener("pointerup", release);
  grip.addEventListener("pointercancel", release);
  grip.addEventListener("lostpointercapture", release);
  window.addEventListener("pointerup", release);
}

function wireChatGrab(grab) {
  let held = 0;
  const width = (ev) => putChatWidth(window.innerWidth - ev.clientX);
  const release = (ev) => {
    if (!held) return;
    held = 0;
    saveChatWidth(ev && ev.clientX !== undefined ? width(ev) : chatWidth());
  };
  grab.addEventListener("pointerdown", (ev) => {
    // Тянут левой кнопкой: правая открывает меню, и хват под ней остался бы
    // зажатым после того, как меню закрыли.
    if (ev.button !== undefined && ev.button !== 0) return;
    held = ev.pointerId === undefined ? 1 : ev.pointerId + 1;
    if (grab.setPointerCapture) grab.setPointerCapture(ev.pointerId);
    if (ev.preventDefault) ev.preventDefault();
  });
  grab.addEventListener("pointermove", (ev) => {
    if (!held) return;
    // Кнопка отпущена мимо нас: движение с пустым buttons это уже не тяга, и
    // держаться за флаг тут нельзя. Ровно так терялось отпускание за краем
    // окна.
    if (ev.buttons === 0) {
      release(ev);
      return;
    }
    width(ev);
  });
  grab.addEventListener("pointerup", release);
  grab.addEventListener("pointercancel", release);
  // Захват теряется и без отпускания: другое окно перехватило указатель,
  // система показала жест. Без этого слушателя тяга оставалась бы включённой.
  grab.addEventListener("lostpointercapture", release);
  // Отпускание за пределами полоски ловится окном: захват его обычно приносит
  // сам, но на потерянном захвате остаётся только это.
  window.addEventListener("pointerup", release);
  window.addEventListener("blur", () => release(null));
}

// Экран под панелью: адрес без хвоста разговора. Старые адреса ложатся сюда же,
// поэтому «закрыть» с них ведёт на доску или на экран задачи, а не в пустоту.
// Последний открытый разговор помнится между заходами: человек возвращается в
// дашборд к тому же диалогу, а не к пустой панели (замечание 19).
const CHAT_LAST_KEY = "devkit.chat.last";

// Набранная, но не отправленная реплика живёт в localStorage по ключу диалога:
// текст бывает длинным и обдуманным, а перезагрузка страницы уносила его
// целиком (замечание 4 седьмого круга POC). Пишется он с задержкой, чтобы не
// дёргать хранилище на каждую букву, и стирается удачной отправкой.
const CHAT_DRAFT_KEY = "devkit.chat.draft.";

// Последний открытый чат задачи. Чатов у задачи бывает несколько, и открывать
// каждый раз первый из списка значит терять тот, в котором человек только что
// разговаривал (замечание 1 десятого круга POC). Ключ по задаче, а не общий:
// у соседней задачи свой последний чат.
const CHAT_TASK_KEY = "devkit.chat.task.";

function chatTaskLast(task) {
  try {
    return localStorage.getItem(CHAT_TASK_KEY + task) || "";
  } catch (err) {
    return "";
  }
}

function chatTaskLastSet(task, sid) {
  try {
    if (task && sid) localStorage.setItem(CHAT_TASK_KEY + task, sid);
  } catch (err) {
    // приватный режим браузера: память живёт до перезагрузки
  }
}

// Вложение хранится рядом с черновиком, но в памяти страницы, а не в
// localStorage: снимок это dataURL в мегабайты, хранилище на нём отказывает по
// квоте и уносит заодно текст черновика. Переключение разговора память не
// трогает, и вставленная картинка возвращается вместе с недописанной репликой
// (замечание 6).
const chatShots = new Map();

function chatShotRead(addr) {
  return chatShots.get(addr) || null;
}

function chatShotWrite(addr, pic) {
  if (pic) chatShots.set(addr, pic);
  else chatShots.delete(addr);
}

function chatDraftRead(addr) {
  try {
    return localStorage.getItem(CHAT_DRAFT_KEY + addr) || "";
  } catch (err) {
    return "";
  }
}

// Высота поля ввода это спутник черновика, а не вечная настройка дашборда:
// пока набранное лежит, поле остаётся тем же, каким его растянули, а отправка
// уносит и текст, и высоту (замечание пользователя про сброс после отправки).
function chatDraftHeight(addr) {
  try {
    return Number(localStorage.getItem(CHAT_DRAFT_KEY + addr + ".h")) || 0;
  } catch (err) {
    return 0;
  }
}

function chatDraftHeightWrite(addr, h) {
  try {
    if (h > 0) localStorage.setItem(CHAT_DRAFT_KEY + addr + ".h", String(Math.round(h)));
    else localStorage.removeItem(CHAT_DRAFT_KEY + addr + ".h");
  } catch (err) {
    // приватный режим браузера: высота живёт до перезагрузки
  }
}

function chatDraftWrite(addr, text) {
  try {
    if (text) localStorage.setItem(CHAT_DRAFT_KEY + addr, text);
    else localStorage.removeItem(CHAT_DRAFT_KEY + addr);
  } catch (err) {
    // приватный режим браузера: черновик живёт до перезагрузки
  }
}

// Задержка записи черновика: набор идёт быстрее, чем стоит трогать диск.
const CHAT_DRAFT_WAIT = 400;

function chatLastSet(addr) {
  try {
    if (addr) localStorage.setItem(CHAT_LAST_KEY, addr);
    else localStorage.removeItem(CHAT_LAST_KEY);
  } catch (err) {
    // приватный режим браузера: память живёт до перезагрузки
  }
}

function chatLast() {
  try {
    return localStorage.getItem(CHAT_LAST_KEY) || "";
  } catch (err) {
    return "";
  }
}

// Переход по дашборду при открытой панели: хвост разговора приклеивается к
// новому адресу, и панель переживает переход, вместо того чтобы закрываться на
// каждом нажатии раздела (замечания 18 и 19). Панель это хвост, экран под ней
// меняется сам по себе, и рвать разговор ради смены экрана незачем.
// Замена адреса без новой записи в истории: так переезжают старые адреса на
// новые места. Хвост разговора едет с ними, панель это хвост.
function goSame(hash) {
  const chat = chatKeepTail(hash);
  const to = "#" + (chat ? hash + "/chat/" + chat : hash);
  if (location.hash !== to) history.replaceState(history.state || {}, "", to);
}

function goKeepingChat(hash) {
  const chat = chatKeepTail(hash);
  location.hash = chat ? hash + "/chat/" + chat : hash;
}

// Хвост разговора, уезжающий на новый адрес. Часть адресов панели ничего не
// значит сама по себе и читается «в том проекте, что сейчас на доске»: новый
// чат, чат задачи и общий чат доски. Смена проекта в шапке перечитывала такой
// хвост заново, и открытый разговор devkit сменялся пустым чатом соседней
// доски прямо под рукой (жалоба пользователя про «переключается на новый
// пустой диалог»). Тут хвост закрепляется за своим проектом ровно в тот
// момент, когда доска под панелью меняется: дальше адрес несёт проект собой и
// переезда не замечает, как давно не замечает его адрес из ленты.
function chatKeepTail(to) {
  const chat = route().chat;
  if (!chat) return "";
  const here = route().proj;
  const there = routeScreen(String(to || "").replace(/^#/, "")).proj;
  if (!here || !there || here === there) return chat;
  // Проект уже назван в самом адресе: перечитывать его не по чему.
  if (chat.includes(CHAT_PROJ_SEP)) return chat;
  // Адрес сессии значит один и тот же разговор с любой доски, и короткий хвост
  // ему дороже: закреплять там нечего.
  if (!chatIsNew(chat) && !chatIsTask(chat) && chat !== CHAT_BOARD) return chat;
  return here + CHAT_PROJ_SEP + chat;
}

function chatBase() {
  const h = decodeURIComponent(location.hash.replace(/^#/, ""));
  const cut = h.indexOf("/chat/");
  const rest = cut >= 0 ? h.slice(0, cut) : h;
  const old = rest.match(/^([^/]+)\/(agent|session)\/(.+)$/);
  if (old) return old[1] + (old[2] === "agent" ? "/" + old[3] : "");
  return rest;
}

// Сколько раз панель открывали в этой вкладке: по счётчику видно, есть ли куда
// возвращаться. Крестик тогда работает той же кнопкой «назад», и доска встаёт
// на прежнее место, а не перерисовывается сверху.
let chatDepth = 0;

// Адрес, которым панель толкнула историю в последний раз. Одного счётчика мало:
// адрес с тех пор мог смениться помимо панели (ссылка из ленты, набранный
// хвост, возврат кнопкой браузера), и «назад» уводил тогда не на экран под
// панелью, а на прежний чужой. Крестик работает кнопкой «назад» только на том
// самом адресе, который панель и толкнула.
let chatPushed = "";

function hashText() {
  return decodeURIComponent(String(location.hash || "").replace(/^#/, ""));
}

// Адрес разговора над экраном задачи и над доской: панель это хвост, и то, что
// под ней, открытие не трогает.
function taskChatHash(project, id) {
  return project + "/" + id + "/chat/" + id;
}

function boardChatHash(project, addr) {
  return project + "/chat/" + addr;
}

// Адрес чата с явным проектом. Раздел «Агенты» показывает работы всех досок
// разом, и своего проекта у него нет вовсе: панель, открытая оттуда, иначе
// взяла бы первый попавшийся и открыла чужой чат. Проект едет в самом адресе
// через «~», потому что хвост /chat/ это одна строка (замечание 3 восьмого
// круга POC). На экране проекта хвост остаётся коротким, как был.
const CHAT_PROJ_SEP = "~";

function chatAddr(project, addr) {
  return route().proj ? addr : project + CHAT_PROJ_SEP + addr;
}

// Разбор адреса обратно: проект и сам адрес чата.
function chatAddrParts(project, addr) {
  const cut = String(addr || "").indexOf(CHAT_PROJ_SEP);
  if (cut < 0) return { project, addr };
  return { project: addr.slice(0, cut), addr: addr.slice(cut + 1) };
}

// Адрес общего чата доски: привязки у него нет вовсе, и слово это не ID ни
// задачи, ни сессии (решение 7 LLD DK-430). Панель по нему открывает свежий
// разговор проекта без задачи, а список слева показывает остальные такие же.
const CHAT_BOARD = "board";

function chatIsBoard(addr) {
  return addr === CHAT_BOARD;
}

function openChat(addr) {
  const to = "#" + chatBase() + "/chat/" + addr;
  chatLastSet(addr);
  if (location.hash === to) return;
  history.pushState({ chat: addr }, "", to);
  chatDepth += 1;
  chatPushed = to.replace(/^#/, "");
  // Полоска возврата уходит с экрана сразу, а не ответом сети: разговор уже
  // открывается, и звать обратно в него больше некуда.
  paintChatBack();
  refresh().catch(console.error);
}

// Переключение разговора списком: адрес заменяется, а не толкается в историю.
// Пять просмотренных разговоров иначе стоили бы пяти нажатий «назад» до доски,
// а экран под панелью от переключения не меняется вовсе.
// Двинулся ли один хвост разговора: экран тот же и запрос тот же, значит под
// панелью перерисовывать нечего. Набор буквы в поиске экран не меняет (запрос в
// ключ не входит нарочно), но выдачу обновить обязан, поэтому запрос
// сравнивается отдельно.
function chatOnlyMove(rt) {
  return Boolean(shownScreen) && screenKey(rt) === shownScreen && (rt.q || "") === shownQuery;
}

// Панель это хвост адреса, и её движение экрана под ней не касается. Полный
// обход (список проектов, подписки, доска, пересборка списка строк) на каждое
// переключение чата стоил человеку задержки на ровном месте: до правки первый
// отклик приходил через ответ сети, а не сразу (замер poc_bench_chat).
function repaintChatOnly() {
  paintChat(shownProject, route().chat, shownBoard, shownWorks).catch(console.error);
}

function switchChat(addr) {
  const to = "#" + chatBase() + "/chat/" + addr;
  chatLastSet(addr);
  if (location.hash === to) return;
  history.replaceState({ chat: addr }, "", to);
  // Замена стоит на той же записи истории: если её толкнула панель, то «назад»
  // с переключённого разговора по-прежнему ведёт на экран под ним.
  if (chatPushed) chatPushed = to.replace(/^#/, "");
  repaintChatOnly();
}

// Переход по ссылке из разговора. На широком экране панель стоит сбоку, экран
// под ней меняется сам, и рвать разговор незачем. На телефоне панель занимает
// экран целиком, и показать переход было негде: человек нажимал на ID задачи
// в реплике и оставался в той же ленте, а ссылка выглядела мёртвой (замечание
// пользователя). Тут панель отдаёт экран цели: хвост разговора уходит из
// адреса, сам разговор остаётся в памяти, и полоска возврата приводит в него
// обратно одним касанием. Кнопка «назад» браузера ведёт туда же: адрес с
// хвостом остался прежней записью истории.
function goFromChat(addr) {
  const chat = route().chat;
  if (!chat || !narrowScreen()) {
    goKeepingChat(addr);
    return;
  }
  chatLastSet(chat);
  shutChatPanel();
  location.hash = addr;
  paintChatBack();
}

// Полоска возврата в разговор: стоит на телефоне, когда панель отдала экран
// переходу по ссылке, а разговор ещё помнится. На широком экране панель никуда
// не девалась, и возвращаться неоткуда. Закрытый рукой разговор память снимает,
// и полоска уходит вместе с ней.
function paintChatBack() {
  const back = document.getElementById("cback");
  if (!back) return;
  const last = chatLast();
  const show = Boolean(last) && !route().chat && narrowScreen();
  back.hidden = !show;
  if (!show) {
    back.dataset.addr = "";
    back.replaceChildren();
    return;
  }
  if (back.dataset.addr === last && back.children.length) return;
  back.dataset.addr = last;
  const btn = barBtn("btn cbackbtn", "Вернуться в разговор", "i-chat");
  btn.addEventListener("click", () => { openChat(last); });
  back.replaceChildren(btn);
}

function closeChat() {
  // Закрытие рукой снимает и память: вернувшись, человек видит дашборд, а не
  // разговор, от которого он ушёл нарочно.
  chatLastSet("");
  // Панель уходит с экрана сразу, до всякой истории и сети: экран под ней уже
  // нарисован, ждать от закрытия нечего вовсе.
  shutChatPanel();
  if (chatDepth > 0 && hashText() === chatPushed) {
    chatDepth -= 1;
    chatPushed = "";
    history.back();
    return;
  }
  // Пришли по ссылке снаружи: возвращаться некуда, и панель закрывается
  // заменой адреса на тот экран, что под ней.
  history.pushState({}, "", "#" + chatBase());
}

// Пул панелей разговоров. Переключение чата пересобирало панель заново, и
// возврат в уже открытый разговор стоил похода в сеть за состоянием: человек
// смотрел на «чат открывается...» там, где всё уже было нарисовано. Открытая
// панель теперь не сносится, а прячется, и возврат показывает готовый узел.
//
// Прячется слот через content-visibility, а не display:none: браузер держит
// отрисовку скрытого поддерева, и повторный показ не платит за раскладку.
// Бонусом при узле остаются позиция прокрутки и раскрытые блоки.
//
// Свежесть спрятанного никто не сторожит: опроса по спрятанным чатам нет
// вовсе, живые потоки уходящего разговора гасит closeChatLive, а дельта
// доезжает обычным путём, уже после мгновенного показа.
const CHAT_POOL_MAX = 6;

// Ключ разговора -> узел слота. Порядок вставки это и есть свежесть: ключ
// показанного переставляется в конец, вытесняется давний.
const chatPool = new Map();

// chatSlotKeys перечисляет разговоры пула от давнего к свежему. Снаружи по
// нему видно, что именно лежит готовым и что вытеснено.
function chatSlotKeys() {
  return Array.from(chatPool.keys());
}

function chatSlotFace(node, on) {
  node.className = "cslot" + (on ? "" : " off");
  node.setAttribute("aria-hidden", on ? "false" : "true");
}

// chatSlotShow показывает слот ключа и прячет остальные. Пустой ключ прячет
// все: так уходит закрытая панель, не теряя готовых узлов.
function chatSlotShow(key) {
  for (const [k, node] of chatPool) chatSlotFace(node, k === key);
}

// chatSlotPut кладёт свежесобранный разговор в слот и показывает его. Слот с
// тем же ключом переиспользуется: узел стоит на месте, меняется его начинка.
function chatSlotPut(pin, key, kids) {
  let node = chatPool.get(key);
  if (!node) {
    node = el("div", "cslot");
    pin.append(node);
  }
  node.replaceChildren(...kids);
  chatPool.delete(key);
  chatPool.set(key, node);
  // Вытеснение по давности: узел сносится совсем, и такой чат вернётся
  // обычным путём, сборкой заново.
  while (chatPool.size > CHAT_POOL_MAX) {
    const old = chatPool.keys().next().value;
    const dead = chatPool.get(old);
    chatPool.delete(old);
    if (dead && dead.remove) dead.remove();
  }
  // Плашки ожидания в контейнере панели живут до готового разговора: с приходом
  // слота им там делать нечего.
  for (const kid of Array.from(pin.children || [])) {
    if (kid !== node && !String(kid.className || "").includes("cslot")) {
      if (kid.remove) kid.remove();
    }
  }
  chatSlotShow(key);
  return node;
}

// Снятие панели по месту: узел прячется, живые потоки разговора закрываются, а
// экран под панелью остаётся как был.
function shutChatPanel() {
  const panel = document.getElementById("cpanel");
  const pin = document.getElementById("cpin");
  if (!panel || !pin) return;
  closeChatLive();
  chatDropShut();
  chatOpen = "";
  chatFill = null;
  chatShown = { project: "", sid: "", task: "" };
  // Готовые узлы остаются в пуле: закрытую панель открывают тем же разговором,
  // и сносить его ради закрытия значит платить за возврат второй раз.
  chatSlotShow("");
  for (const kid of Array.from(pin.children || [])) {
    if (!String(kid.className || "").includes("cslot") && kid.remove) kid.remove();
  }
  panel.hidden = true;
}

// Адрес разговора это либо id сессии, либо ID задачи, и второе значит
// «последний разговор этой задачи». Форма ID проверяется тут же: у сессии он
// длиннее и с дефисами внутри шестнадцатеричных кусков.
const CHAT_TASK_RE = /^[A-Za-zА-Яа-я]{2,8}-\d+$/;

function chatIsTask(addr) {
  return CHAT_TASK_RE.test(addr || "");
}

function sessionMessageURL(project, sid) {
  return sessionURL(project, sid) + "/message";
}

// ---- Панель разговора, переделка POC (ветка poc-chat) ----
//
// Диалог это сессия харнеса, один к одному с транскриптом. Список диалогов
// стоит выпадающим списком с поиском в шапке окна, отдельной колонки под него
// нет. Реплика едет прямо в процесс: живому агенту через tmux send-keys,
// кончившемуся резюмом той же сессии, новому диалогу первым аргументом
// клиента. Отложенной доставки и очереди исходящих тут больше нет вовсе.

// Адрес пустого диалога: он заведён кнопкой «+», сессии у него ещё нет, и
// поднимет её первая реплика человека. Задача едет хвостом через двоеточие
// («new:DK-397»): заведённый с экрана задачи диалог поднимается в её дереве и
// с её же фильтром списка, а без хвоста это разговор ни о чём конкретном.
const CHAT_NEW = "new";

function chatIsNew(addr) {
  return addr === CHAT_NEW || String(addr || "").startsWith(CHAT_NEW + ":");
}

function chatNewTask(addr) {
  const tail = String(addr || "").slice(CHAT_NEW.length + 1);
  return chatIsTask(tail) ? tail : "";
}

// Память подъёма конвейера. Кнопка «Выполнить» поднимает tmux-сессию, а имени в
// реестре у неё ещё нет: она назовётся сама, первым своим ходом, через
// несколько секунд. Адресовать панель этим разговором нечем, и до правки она
// вставала на адрес несуществующего чата и объявляла его протухшим («Чат не
// найден» сразу после удачного запуска, снимок пользователя). Тут ждёт имя
// tmux: по нему панель узнаёт родившийся диалог и переезжает на него сама.
// Память переживает перезагрузку вкладки, потому что опрос реестра умирает
// вместе с ней, а сессия поднимается своим ходом.
const LIFT_KEY = "devkit.chat.lift.";

// Дольше этого срока ожидание не живёт: клиент, вставший на вопросе в своём
// терминале, сессию так и не назовёт, и вечная плашка о подъёме врала бы.
const LIFT_LIVE = 15 * 60 * 1000;

function liftKey(project, addr) {
  return LIFT_KEY + project + "/" + addr;
}

function chatLiftSet(project, addr, tmux) {
  try {
    localStorage.setItem(liftKey(project, addr), JSON.stringify({ tmux, born: Date.now() }));
  } catch (err) {
    // приватный режим браузера: ожидание живёт до перезагрузки
  }
}

function chatLiftOf(project, addr) {
  try {
    const raw = JSON.parse(localStorage.getItem(liftKey(project, addr)) || "null");
    if (!raw || !raw.tmux) return "";
    if (Date.now() - (raw.born || 0) > LIFT_LIVE) {
      chatLiftDrop(project, addr);
      return "";
    }
    return String(raw.tmux);
  } catch (err) {
    return "";
  }
}

// Давность подъёма в миллисекундах: по ней таб сессий отличает «сессия ещё
// поднимается» от «сессии нет». Минус один значит, что подъёма не помним вовсе.
function chatLiftAge(project, addr) {
  try {
    const raw = JSON.parse(localStorage.getItem(liftKey(project, addr)) || "null");
    if (!raw || !raw.tmux || !raw.born) return -1;
    return Date.now() - raw.born;
  } catch (err) {
    return -1;
  }
}

function chatLiftDrop(project, addr) {
  try {
    localStorage.removeItem(liftKey(project, addr));
  } catch (err) {
    // см. chatLiftSet
  }
}

// Что делает удачный запуск с панелью: ничего. Прежде он открывал её сам, и
// человек, разбиравший итоги задачи в одном разговоре, вылетал из него, стоило
// нажать «Выполнить» у соседней задачи (замечание пользователя). Панель
// открывает человек, а запуск оставляет след: имя поднятой сессии ложится в
// память подъёма, и по ней кнопка чата этой задачи мягко моргает рамкой, пока
// работа идёт. Откроет человек чат задачи сам, панель встретит его тем же
// ожиданием подъёма, что и раньше.
function markRunLive(project, id, sess) {
  if (!sess) return;
  chatLiftSet(project, CHAT_NEW + ":" + id, sess);
}

// Пошла ли у задачи активность: её работа идёт прямо сейчас либо запуск был
// только что и сессия ещё поднимается. Второе видно памятью подъёма: между
// нажатием и первым ходом сессии проходят секунды, и всё это время экран обязан
// говорить, что нажатие сработало.
function taskLively(project, id, works) {
  if (!id) return false;
  if (workOnRun((works || []).find((x) => x.id === id))) return true;
  return Boolean(chatLiftOf(project, CHAT_NEW + ":" + id));
}

// Машинные имена состояний: их присылает сервер, и сравнивать с ними надо теми
// же словами, что он пишет.
const WORK_BUSY = "busy";
const WORK_WAIT = "waiting";

// Идёт ли ход в самой работе. Тот же вопрос, что rowOnRun задаёт строке доски,
// только строка получает ответ признаком сервера, а тут он читается прямо из
// состояния работы. Ожидание ответа человека это тоже идущий ход: сессия жива и
// пойдёт дальше с ответом.
function workOnRun(w) {
  return Boolean(w) && (w.live === WORK_BUSY || w.live === WORK_WAIT);
}

// Идущая работа задачи или записи накопителя, как её видит tmux. Отличается от
// taskLively тем, что памяти подъёма не спрашивает: та живёт четверть часа и
// после умершей сессии, и там, где решают «поднимать ли», нужна правда о tmux.
function workBusy(id, works) {
  const w = (works || shownWorks || []).find((x) => x.id === id);
  return workOnRun(w) ? w : null;
}

// Имя tmux идущей работы. По нему пустая панель находит сессию, которой ещё нет
// в списке чатов: реестр чатов собирается из транскриптов, и разговор
// появляется в нём с первой репликой агента, а работа видна в tmux сразу.
function workSession(id, works) {
  const w = workBusy(id, works);
  return (w && w.session) || "";
}

// chatSewn ищет диалог, который родила застрявшая на адресе new реплика.
// Узнавание двумя ключами по убыванию силы: имя tmux-сессии подъёма, если
// персист его помнит, и сама первая реплика, ведь она уехала клиенту первым
// аргументом и легла в транскрипт первой, то есть стала заголовком диалога в
// списке. Пустой ответ значит, что сессия ещё не родилась либо реплика
// пропала вместе с tmux, и тогда пузырь честно остаётся held.
function chatSewn(project, addr, chats) {
  for (const rec of echoRead(project, addr)) {
    if (rec.state !== "held" && rec.state !== "wait") continue;
    if (rec.tmux) {
      const hit = (chats || []).find((c) => c.tmux === rec.tmux);
      if (hit) return hit.id;
    }
    // Первая реплика сверяется со своим полем, а не с заголовком: заголовок
    // у диалога бывает от харнеса и с первой репликой не совпадает.
    const hit = (chats || []).find((c) => sameSaid(c.first || c.title, rec.wire || rec.text));
    if (hit) return hit.id;
  }
  return "";
}

// sameSaid сверяет заголовок диалога с отправленным текстом. Заголовок это
// первая строка первой реплики, обрезанная сервером с «...» на хвосте
// (firstLine в sessions.go), поэтому от отправленного берётся первая строка,
// у заголовка снимается многоточие, и сравнивается общая голова. Порог длины
// держит сверку от ложных пришиваний на коротких репликах вроде «да».
const SEW_MIN = 24;

function sameSaid(title, said) {
  let a = String(title || "").trim();
  if (a.endsWith("...")) a = a.slice(0, -3);
  const b = (String(said || "").split("\n", 1)[0] || "").trim();
  const n = Math.min(a.length, b.length);
  if (n < SEW_MIN) return false;
  return a.slice(0, n) === b.slice(0, n);
}

// Состояние переключателя фильтра по задаче живёт в localStorage: человек
// ставит его один раз под свою привычку, а не на каждое открытие окна.
const CHAT_FILTER_KEY = "devkit.chat.filter";
// Последняя выбранная модель: новый диалог заводится ею же, иначе выбор
// приходилось бы повторять на каждом «+».
const CHAT_MODEL_KEY = "devkit.chat.model";
// Что делать со скрытыми в архив: не показывать (умолчание), показывать вместе
// со всеми, показывать только их. Положение живёт в localStorage и переживает
// перезагрузку страницы: разбирают накопитель заходами, и выставлять вид списка
// заново на каждом заходе человеку незачем.
const CHAT_ARCH_KEY = "devkit.chat.arch";
const CHAT_ARCH_MODES = ["off", "all", "only"];
const CHAT_ARCH_WORD = {
  off: "Архивные скрыты: показать все",
  all: "Показаны все разговоры, включая архивные: оставить только архивные",
  only: "Показаны только архивные: вернуться к списку без них",
};

function chatFilterOn() {
  try {
    return localStorage.getItem(CHAT_FILTER_KEY) !== "0";
  } catch (err) {
    return true;
  }
}

function chatFilterSet(on) {
  try {
    localStorage.setItem(CHAT_FILTER_KEY, on ? "1" : "0");
  } catch (err) {
    // Приватный режим браузера запрещает запись: переключатель тогда живёт
    // до перезагрузки, и это лучше упавшего окна.
  }
}

function chatArchMode() {
  try {
    const got = localStorage.getItem(CHAT_ARCH_KEY);
    return CHAT_ARCH_MODES.includes(got) ? got : CHAT_ARCH_MODES[0];
  } catch (err) {
    return CHAT_ARCH_MODES[0];
  }
}

function chatArchSet(mode) {
  try {
    localStorage.setItem(CHAT_ARCH_KEY, mode);
  } catch (err) {
    // Приватный режим браузера запрещает запись: положение тогда живёт до
    // перезагрузки, и это лучше упавшего окна.
  }
}

// Кнопка одна, положений три, и ходят они по кругу в том порядке, в каком их
// спрашивают: сперва спрятать, потом посмотреть всё, потом разобрать сам архив.
function chatArchNext(mode) {
  const at = CHAT_ARCH_MODES.indexOf(mode);
  return CHAT_ARCH_MODES[(at + 1) % CHAT_ARCH_MODES.length];
}

// chatArchShown режет список по выбранному положению кнопки. Пустой поиск
// (draw при !q) идёт через него, а набранный запрос (DK-726) режет список сам,
// мимо этой функции: положение кнопки прячет разговор только для глаза, а не
// от поиска, иначе убранное было бы недостижимо без кругового нажатия кнопки.
function chatArchShown(list, mode) {
  if (mode === "all") return list;
  if (mode === "only") return list.filter((c) => c.archived);
  return list.filter((c) => !c.archived);
}

// dropChat закрывает незачатую запись насовсем. Ручка отказывает записи с
// сессией и разговору с лентой, поэтому отказ тут не поломка, а слова о том,
// куда идти, и показать их надо человеку.
async function dropChat(project, sid) {
  const r = await api(chatsURL(project) + "/" + encodeURIComponent(sid) + "/drop",
    { method: "POST", body: {} });
  if (!r.ok) sayResult(r.body.error || "запись не закрылась", true);
  return r.ok;
}

// archiveChat убирает разговор в архив и возвращает обратно той же ручкой.
// Живую сессию убранного снимает сервер, тут о ней речи нет.
async function archiveChat(project, sid, on) {
  const r = await api(chatsURL(project) + "/" + encodeURIComponent(sid) + "/archive",
    { method: "POST", body: { archived: on } });
  if (!r.ok) sayResult(r.body.error || "архив не записался", true);
  return r.ok;
}

function chatModelPref() {
  try {
    return localStorage.getItem(CHAT_MODEL_KEY) || "opus";
  } catch (err) {
    return "opus";
  }
}

function chatModelSet(m) {
  try {
    localStorage.setItem(CHAT_MODEL_KEY, m);
  } catch (err) {
    // см. chatFilterSet
  }
}

function chatsURL(project) {
  return "/api/projects/" + encodeURIComponent(project) + "/chats";
}

// Состояние окна чатов: список диалогов всей машины, выбранный диалог и
// задача, по которой список фильтруется. Список общий (?all=1), а не
// проектный: диалог ищут по всем проектам разом, и переключение на чужой не
// требует смены проекта доски. Приезжает он целиком и фильтруется на клиенте:
// переключатель фильтра тогда работает мгновенно, без похода на сервер.
async function chatState(project, addr, board, works) {
  const st = { addr, sid: "", task: "", chats: [], entry: null, note: "",
    error: "", models: [], fresh: false, lost: false, project };
  if (chatIsNew(addr)) {
    st.fresh = true;
    st.task = chatNewTask(addr);
  } else if (chatIsTask(addr)) {
    st.task = addr;
  } else if (addr && addr !== CHAT_BOARD) {
    st.sid = addr;
  }
  const r = await api(chatsURL(project) + "?all=1" + chatKeepArg(st));
  if (!r.ok) {
    st.error = r.body.error || "список чатов не прочитался";
    return st;
  }
  st.chats = r.body.chats || [];
  // Незачатый разговор: запись заведена кнопкой «+», сессии за ней ещё нет, и
  // поднимет её первая реплика. Адрес тут настоящий, серверный, а не общий на
  // всю вкладку «new»: таких разговоров человек заводит сколько угодно, у
  // каждого своя строка списка и свой набранный текст.
  const rec = addr ? st.chats.find((c) => c.id === addr && c.blank) : null;
  if (rec) {
    st.blank = addr;
    st.entry = rec;
    // Ленты у записи нет, и sid тут пустой: панель встречает человека тем же
    // экраном нового чата, каким встречала прежде, а поле ввода открыто.
    st.sid = "";
    st.fresh = true;
  }
  // Выросшая запись строкой не показывается: разговор уже стоит в списке своей
  // строкой, а запись осталась дорожным знаком для панели, которая на ней
  // стоит.
  st.chats = st.chats.filter((c) => !(c.blank && c.grown));
  st.note = r.body.note || "";
  // Окно списка: сервер отдаёт свежие разговоры плюс живые, а days говорит,
  // какое окно приехало, older говорит, что раньше есть ещё. Ниже по обоим
  // полям рисуется «показать раньше», и по days же поиск понимает, что список
  // приехал не весь.
  st.days = r.body.days || 0;
  st.older = Boolean(r.body.older);
  if (r.body.models) st.models = r.body.models;
  // Причина пустого выбора моделей: список приезжает от agentctl целиком, и
  // без него выбирать нечем. Молчание тут неотличимо от «моделей и правда одна»
  // (замечание пользователя: «нельзя поменять модель в новом чате»).
  st.modelsNote = r.body.models_note || "";
  // Пришивание застрявшего нового адреса: первая реплика уходила в чат,
  // которого ещё не было, сессия родилась позже (клиент стоял на вопросе в
  // своём терминале), а панель возвращалась на эфемерный адрес new и молчала,
  // хотя транскрипт давно жив. Родившийся диалог узнаётся по имени tmux из
  // персиста реплики либо по самой первой реплике, и панель переезжает на
  // живой sid прямо в этой сборке, без перерисовки.
  if (st.fresh) {
    // Подъём конвейера узнаётся тем же ключом, что и подъём с первой реплики:
    // именем tmux-сессии. Разница только в том, откуда имя взялось, из ответа
    // запуска или из персиста реплики.
    const lift = chatLiftOf(project, addr);
    const born = lift ? st.chats.find((c) => c.tmux === lift) : null;
    // Пришивание сервер знает твёрже вкладки: он сам сверяет имя поднятой
    // tmux-сессии с реестром и помнит ID выросшей сессии. Дорога эта работает и
    // после перезагрузки, и в соседней вкладке, где памяти подъёма нет вовсе.
    const sewn = (rec && rec.grown) || (born ? born.id : chatSewn(project, addr, st.chats));
    if (!sewn && lift) st.lift = lift;
    if (sewn) {
      chatLiftDrop(project, addr);
      echoMove(project, addr, sewn);
      chatLastSet(sewn);
      history.replaceState({ chat: sewn }, "", "#" + chatBase() + "/chat/" + sewn);
      st.addr = sewn;
      st.sid = sewn;
      st.fresh = false;
      st.blank = "";
      st.entry = null;
      st.task = "";
    }
  }
  // Сессия записи поднимается: имя её tmux запись помнит сама, и плашка о
  // подъёме встаёт даже там, где памяти подъёма нет, в соседней вкладке или
  // после перезагрузки.
  if (st.blank && st.fresh && rec.tmux) st.lift = st.lift || rec.tmux;
  // Задача адреса это фильтр, а не сам чат: открытый ею список показывает чаты
  // задачи. Открывается тот, в котором человек разговаривал последним, а если
  // такого нет или он пропал из списка, то свежий. Выбор по умолчанию ходит
  // только по своему проекту: общий список начинается с чужих разговоров, и
  // без этой оговорки панель доски открывала бы чат соседнего проекта.
  // Кандидатов по задаче даёт сама задача, а не chatVisible: тот слушает
  // переключатель фильтра списка, и с выключенным фильтром кнопка чата на
  // форме DK-459 открывала последний разговор всего проекта, чат DK-397
  // (живой случай). Нет у задачи своих диалогов, значит пустой sid, и панель
  // открывает новый чат с её привязкой.
  if (!st.sid && !st.fresh) {
    const list = (st.task ? st.chats.filter((c) => (c.tasks || []).includes(st.task))
      : chatVisible(st)).filter((c) => !c.project || c.project === project);
    const want = st.task ? chatTaskLast(st.task) : "";
    const kept = want && list.find((c) => c.id === want);
    if (kept) st.sid = kept.id;
    else if (list.length) st.sid = list[0].id;
  }
  // Память подъёма живёт не только у адреса new: разбор записи накопителя
  // поднимают кнопкой её строки, а чат по ней открывают тем же ID, каким
  // открывают чат задачи. Панель с привязкой и без диалога ждёт ровно ту же
  // сессию, что и адрес new, и плашка с запертым полем ей нужна та же.
  // Незачатой записи это не касается: она ждёт свою сессию, а не работу задачи,
  // и запирать поле ввода из-за чужого конвейера ей не за что.
  if (!st.sid && st.task && !st.lift && !st.blank) {
    st.lift = chatLiftOf(project, CHAT_NEW + ":" + st.task) ||
      workSession(st.task, works);
  }
  // Исход подъёма, кончившегося смертью: слова о нём стоят в панели вместо
  // обещания «сессия вот-вот назовётся». Память подъёма к этому времени уже
  // снята, и ждать панели больше нечего (DK-728).
  st.dead = st.sid ? null : chatDeadOf(project, st.addr || addr);
  if (!st.entry) st.entry = st.chats.find((c) => c.id === st.sid) || null;
  // Проект самого разговора: список общий по машине, и открытый чат бывает не
  // из того проекта, что стоит на доске. Все ручки чата (лента, реплика, стоп,
  // модель, вложение) адресуются его проектом, а не проектом доски.
  st.project = (st.entry && st.entry.project) || project;
  // Адрес, которого нет в списке, это одно из двух: старый разговор глубже
  // видимого верха (транскрипт на диске есть, лента откроется) либо протухшая
  // память (чат снят, сессия умерла или так и не родилась, как у клиента,
  // вставшего на вопросе доверия в терминале). Различает их точечная проба
  // ленты: решать по одному списку значило бы хоронить старые разговоры.
  if (st.sid && !st.entry) {
    const probe = await api(sessionURL(project, st.sid) + "?n=1");
    st.lost = !probe.ok;
    // Протухший адрес уходит из памяти панели сразу: иначе человек возвращался
    // бы на «Чат не найден» при каждом входе в проект.
    if (st.lost && chatLast() === st.addr) chatLastSet("");
  }
  // Задача берётся у самого диалога, когда адрес её не назвал: по ней
  // подписывается шапка и заводится следующий диалог в том же дереве.
  if (!st.task && st.entry && (st.entry.tasks || []).length) st.task = st.entry.tasks[0];
  // Задача панели живёт только на своей доске: хвост адреса переживает смену
  // проекта, и задача devkit не должна ехать ни в шапку, ни в заказ нового
  // чата соседнего проекта (живой случай: «+» в it-road-course поднимал
  // chat-DK-397-2 с чужой привязкой). Чужая задача узнаётся по префиксу доски;
  // доска без префикса судить не берётся и оставляет всё как есть.
  const prefix = board && board.prefix ? String(board.prefix).toUpperCase() + "-" : "";
  if (st.task && prefix && !st.task.toUpperCase().startsWith(prefix)) st.task = "";
  if (st.task) {
    st.isGoal = isGoalRow(board, st.task);
    const row = boardRow(board, st.task);
    st.title = row ? row.title : "";
    // Ожидание человека едет в панель вместе с задачей: пока строка стоит с
    // вопросом, ответить ей больше неоткуда, а живой сессии у неё нет.
    st.wait = row ? row.waiting : null;
  }
  return st;
}

// Задача, с которой заводят следующий разговор кнопкой «+». Она не та же, что
// задача панели: та живёт доской, по которой открыт экран, и гаснет у чужого
// префикса, а тут спрашивают самого собеседника, чей это разговор. Разговор
// заводится в проекте самого собеседника, поэтому чужой задаче тут взяться
// неоткуда. Живой случай: чат дашборда шёл по XR-005 на доске с префиксом DK,
// и «+» молча заводил свободный чат, не спросив вида (жалоба пользователя).
function chatMakeTask(st) {
  if (st.task) return st.task;
  const own = st.entry && (st.entry.tasks || [])[0];
  return own || "";
}

// Разговоры, которые остаются в списке любого возраста: открытый панелью и
// последний разговор задачи. Окно режет список по свежести, и без этой
// оговорки адрес старой беседы выпадал бы из выпадашки, а кнопка задачи
// заводила бы новый чат вместо её собственного.
function chatKeepArg(st) {
  const keep = [];
  if (st.sid) keep.push(st.sid);
  const last = st.task ? chatTaskLast(st.task) : "";
  if (last && !keep.includes(last)) keep.push(last);
  return keep.length ? "&keep=" + encodeURIComponent(keep.join(",")) : "";
}

// Лестница окон списка: первую ступень называет сервер (трое суток), а дальше
// «показать раньше» ведёт по этим, и последняя это весь список машины. Порог
// первого окна тут не повторяется нарочно, он живёт одним местом на сервере.
const CHAT_WINDOW_DAYS = [7, 30, 0];

function chatMoreDays(days) {
  for (const step of CHAT_WINDOW_DAYS) {
    if (step === 0 || step > days) return step;
  }
  return 0;
}

// chatLoadWindow перечитывает список другим окном: нулём приезжает весь список
// машины, им живут и поиск, и последняя ступень «показать раньше».
async function chatLoadWindow(project, st, days) {
  const r = await api(chatsURL(project) + "?all=1&days=" + days + chatKeepArg(st));
  if (!r.ok) return false;
  st.chats = (r.body.chats || []).filter((c) => !(c.blank && c.grown));
  st.note = r.body.note || "";
  st.days = r.body.days || 0;
  st.older = Boolean(r.body.older);
  return true;
}

// Заголовок дня в списке разговоров: сегодня и вчера словами, дальше датой.
// Год приписывается только к чужому году: транскрипты с машины не исчезают, в
// глубине списка лежат и прошлогодние разговоры, а у свежих год это шум.
// Месяцы и ключ дня общие с лентой уведомителя, второго календаря панель не
// заводит.
function chatDayHead(day) {
  const parts = String(day || "").split("-");
  if (parts.length !== 3) return "без времени";
  const now = new Date();
  if (day === isoDay(now)) return "сегодня";
  if (day === isoDay(new Date(now.getTime() - 86400000))) return "вчера";
  const said = Number(parts[2]) + " " + (MONTHS[Number(parts[1]) - 1] || "");
  return Number(parts[0]) === now.getFullYear() ? said : said + " " + parts[0];
}

// chatGroups раскладывает список по группам: открытый чат своей группой
// сверху, до него не надо долистывать, под ним подписанная группа активных,
// а дальше группы по дням, сегодня, вчера и датой. Подпись у активных не
// лишняя: без неё трёхдневный живой чат вставал выше сегодняшнего мёртвого, и
// заголовок «сегодня» уезжал вниз, будто сортировка сбоила, хотя порядок по
// времени был верным всегда (замечание пользователя, разбор DK-656).
function chatGroups(list, current) {
  const out = [];
  const rest = current ? list.filter((c) => c.id !== current) : list;
  const top = current ? list.find((c) => c.id === current) : null;
  if (top) out.push({ head: "открытый чат", rows: [top] });
  const live = rest.filter((c) => c.state === "live");
  if (live.length) out.push({ head: "активные", rows: live });
  let day = null;
  let bag = null;
  for (const c of rest) {
    if (c.state === "live") continue;
    const key = c.mtime ? isoDay(new Date(c.mtime)) : "";
    if (!bag || key !== day) {
      day = key;
      bag = { head: chatDayHead(key), rows: [] };
      out.push(bag);
    }
    bag.rows.push(c);
  }
  return out;
}

// Что видно в списке: все диалоги проекта либо только диалоги задачи, если
// адрес её назвал и фильтр включён.
function chatVisible(st) {
  if (!st.task || !chatFilterOn()) return st.chats;
  return st.chats.filter((c) => (c.tasks || []).includes(st.task));
}

// Состояние диалога словами. Три штуки, и все три меняют то, что будет с
// репликой: живому агенту она уйдёт в процесс, кончившемуся поднимет резюм, а
// в окно vscode дашборду писать нечем.
const CHAT_STATE_WORD = {
  live: "ждёт реплики",
  vscode: "в vscode",
  dead: "процесса нет",
  // Четвёртое состояние это заведённый, но не начатый разговор: сессии за ним
  // нет и не было, поднимет её первая реплика человека.
  blank: "не начат",
};

// Заголовок диалога: первая реплика человека, обрезанная, как это делает
// расширение Claude Code для vscode. Имени диалог не требует.
// Полное название подсказкой: заголовки задач и чатов режутся многоточием
// почти всегда, и прочитать обрезанное было негде. Ставится она всегда, а не
// по замеру ширины: замер стоит перерисовки, а лишняя подсказка не мешает.
function withFull(node, text) {
  const said = String(text || "").trim();
  if (said) node.title = said;
  return node;
}

function chatTitle(c) {
  // Пустого аргумента у живых вызовов не осталось (шапка разбирает состояния
  // сама), но на всякий пожарный слова отсутствия тут больше не живут.
  if (!c) return "Чат не найден";
  // В незачатом разговоре не сказано ни слова, и заголовку взяться неоткуда:
  // строка называется тем, чем она человеку и является.
  if (c.blank) return "Новый чат";
  const t = (c.title || "").trim();
  if (t) return t.length > 70 ? t.slice(0, 70) + "..." : t;
  return "чат " + c.id.slice(0, 8);
}

function chatWhen(c) {
  return c.mtime ? localDay(c.mtime) + ", " + localTime(c.mtime) : "";
}

// Кнопка уборки в архив: стоит в самой строке, потому что убирают пачкой и как
// раз из списка, и ходить за этим в панель значило бы открывать каждый разговор
// по очереди (замечание пользователя про десяток отработавших чатов).
function chatArchBtn(project, c, done) {
  const put = el("button", "cdarch");
  put.append(icon(c.archived ? "i-out" : "i-box"));
  put.title = c.archived ? "Вернуть из архива" : "Убрать в архив";
  put.setAttribute("aria-label", put.title);
  // Уборка живого разговора снимает его сессию с машины, и спрашивается это
  // вторым нажатием, как и всякое другое снятие сессии в панели. Записи без
  // процесса вопрос не задаётся: терять там нечего (решение пользователя).
  let armed = !chatArchAsks(c);
  put.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (!armed) {
      armed = true;
      put.classList.add("armed");
      withTip(put, "Точно убрать в архив? Сессия снимется с машины. Разговор " +
        "останется в архиве и вернётся оттуда подъёмом резюмом.");
      put.setAttribute("aria-label", "Точно убрать в архив: сессия снимется");
      return;
    }
    const on = !c.archived;
    // Строка уходит сразу, не дожидаясь ответа: ответ тут только про запись,
    // а список уже показывает то, что человек попросил. Кличка перемены
    // (archived или restored) идёт вместе с ID: список держит строку на месте
    // до следующего открытия, а панель текущего разговора при уборке
    // переключается на следующий (развилки DK-656).
    c.archived = on;
    if (done) done(c.id, on ? "archived" : "restored");
    archiveChat(project, c.id, on).then((ok) => {
      if (ok) return;
      c.archived = !on;
      if (done) done(c.id, on ? "restored" : "archived");
    });
  });
  return put;
}

// chatArchAsks отвечает, спрашивать ли перед уборкой. Спрашивается только у
// живого разговора и только на пути в архив: возврат из архива ничего не
// снимает, а у записи без процесса снимать нечего.
function chatArchAsks(c) {
  if (!c || c.archived) return false;
  return c.state === "live" || Boolean(c.sock) || Boolean(c.pid);
}

// Кнопка закрытия незачатой записи. Пустую она закрывает первым же нажатием:
// терять там нечего. Запись с набранным текстом сперва спрашивает, и вопрос
// стоит там же, где ответ, подсказкой на взведённой кнопке: текст человек писал
// руками, и молчаливая потеря его была бы хуже лишнего нажатия.
function chatDropBtn(project, c, done) {
  const btn = el("button", "cdarch cdrop-x");
  btn.append(icon("close"));
  const said = c.draft ? "Закрыть запись: в ней остался набранный текст"
    : "Закрыть пустую запись";
  btn.title = said;
  btn.setAttribute("aria-label", said);
  let armed = !c.draft;
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (!armed) {
      armed = true;
      btn.classList.add("armed");
      withTip(btn, "Точно закрыть? Набранный текст пропадёт вместе с записью.");
      btn.setAttribute("aria-label", "Точно закрыть запись с текстом");
      return;
    }
    btn.disabled = true;
    dropChat(project, c.id).then((ok) => {
      btn.disabled = false;
      if (ok && done) done(c.id, "dropped");
    }).catch(console.error);
  });
  return btn;
}

// Строка выпадающего списка: заголовок, время, состояние и задачи, которых
// разговор касался. Задач бывает несколько: одна сессия двигает не одну
// строку, и привязка тут один ко многим.
function chatOption(project, c, current, done) {
  const row = el("div", "cdrow" + (c.id === current ? " on" : "") + (c.archived ? " arch" : ""));
  row.append(withFull(el("b", "", chatTitle(c)), chatTitle(c)));
  // Незачатую запись закрывают насовсем, а не прячут в архив: хранить в ней
  // нечего, и архив оставил бы тот же мусор, только за ширмой («закрыть их я не
  // могу, просто нет такой возможности», замечание пользователя). Запись с
  // сессией и разговор с лентой уходят прежней дорогой, архивом: там есть что
  // хранить, а живую сессию закрытие не трогает.
  if (c.blank && !c.grown && !c.tmux) {
    row.append(chatDropBtn(project, c, done));
  } else {
    row.append(chatArchBtn(project, c, done));
  }
  const chips = el("div", "cchips");
  // Принадлежность видна первой: список общий по машине, и без имени проекта
  // строки разных досок неотличимы. Свой проект тоже назван, пустое место у
  // части строк читалось бы пропажей.
  chips.append(el("span", "chip c-proj", c.project || project));
  // Живой чат различается занятостью: работает агент или ждёт реплики.
  const busyNow = c.state === "live" && !c.idle;
  chips.append(el("span", "chip" + (busyNow ? " c-run" : ""),
    busyNow ? LIVE_WORD.busy : CHAT_STATE_WORD[c.state] || c.state));
  // Кончившаяся подписка (DK-647): выпадающий список это то место, где
  // человек видит все разговоры разом, и молчащий чат обязан отличаться от
  // просто занятого агента до открытия панели.
  if (c.quota) chips.append(el("span", "chip c-quota", c.quota));
  if (c.archived) chips.append(el("span", "chip", "в архиве"));
  if (c.model) chips.append(el("span", "chip", c.model));
  for (const t of (c.tasks || []).slice(0, 4)) chips.append(el("span", "chip", t));
  if (c.harness) chips.append(el("span", "chip", c.harness));
  row.append(chips);
  row.append(el("span", "cfirst", chatWhen(c) + (c.tree ? ", " + c.tree : "")));
  row.addEventListener("click", () => {
    chatDropShut();
    // Разговор чужого проекта открывается со своим проектом в адресе: панель
    // это хвост, и смены проекта доски переход не требует.
    const foreign = c.project && c.project !== project;
    switchChat(foreign ? c.project + CHAT_PROJ_SEP + c.id : c.id);
  });
  return row;
}

// Выпадающий список открыт: узел живёт при шапке, а не при документе, чтобы
// закрытие панели уносило его с собой.
let chatDrop = null;
let chatDropHeld = null;

function chatDropShut() {
  popupDrop(chatDropHeld);
  chatDropHeld = null;
  if (chatDrop) {
    chatDrop.remove();
    chatDrop = null;
  }
}

// chatDropSet ставит открытый список на общий учёт всплывашек: закрывают его
// те же три пути, что список кольца и выбор подписки.
function chatDropSet(node) {
  chatDrop = node;
  chatDropHeld = popupHold(node, chatDropShut);
}

// chatPanelNext выбирает разговор для панели, когда открытый ею чат ушёл в
// архив: свой список задачи, если панель к ней привязана, иначе видимый
// список, тем же проектом, что и сама панель, без только что убранного и без
// архивных. Нет ни одного, значит панели открывать нечего, и она уходит на
// новый чат (развилка DK-656: второй заход в список после уборки не нужен).
function chatPanelNext(project, st, goneId) {
  const scoped = st.task ? st.chats.filter((c) => (c.tasks || []).includes(st.task))
    : chatVisible(st);
  const rest = scoped.filter((c) => (!c.project || c.project === project) &&
    c.id !== goneId && !c.archived);
  return rest.length ? rest[0].id : CHAT_NEW;
}

// Список с поиском: поле сверху, дальше строки всех диалогов машины. Поиск
// идёт по заголовку, по ID сессии, по задачам и по имени проекта, потому что
// ищут диалог всеми четырьмя способами. Фильтр по задаче панели это стартовое
// состояние того же поиска, а не жёсткая отсечка: стереть запрос значит
// увидеть весь список.
// again значит переоткрытие своей же рукой (тумблер фильтра): состав списка
// правится на месте, и ходить за перечнем заново тут не за чем.
function chatDropOpen(project, st, anchor, again) {
  chatDropShut();
  popupsShut(null);
  const box = el("div", "cdrop");
  const top = el("div", "cdtop");
  const find = el("input");
  find.type = "text";
  find.placeholder = "Поиск чата";
  find.setAttribute("aria-label", "Поиск чата");
  if (st.task && chatFilterOn()) find.value = st.task;
  // Архивные показывает кнопка справа от поиска: три её положения это три
  // разных списка, и поиск идёт по тому, который выбран.
  const arch = el("button", "cdarchbtn");
  const paintArch = () => {
    const mode = chatArchMode();
    arch.replaceChildren(icon("i-box"));
    arch.className = "cdarchbtn" + (mode === "off" ? "" : " on");
    arch.title = CHAT_ARCH_WORD[mode];
    arch.setAttribute("aria-label", arch.title);
    arch.setAttribute("data-mode", mode);
  };
  const rows = el("div", "cdrows");
  // Догрузка идёт одна за раз: поиск и «показать раньше» ходят за одним и тем
  // же списком, и два запроса подряд перезаписывали бы друг друга.
  let loading = false;
  // Перечитывание списка тем же окном идёт своим счётом и словами о себе не
  // говорит: человек открыл список, а не просил искать.
  let fresh = false;
  // Порядок строк замирает, пока список открыт: без замера уборка пачкой
  // переставляла соседей под курсором на каждое нажатие («уборка гасит строку
  // на месте», развилка автора DK-656). Место замеряется первой отрисовкой, а
  // новое (поиск, «показать раньше») встаёт в конец своей группы.
  let order = null;
  const freezeOrder = (list) => {
    if (order) return;
    order = new Map();
    list.forEach((c, i) => order.set(c.id, i));
  };
  // ID, тронутый уборкой в этом открытии, с тем значением архива, что видел
  // человек: строка остаётся в списке гашёной (клеймом «в архиве» и
  // потускневшей рамкой) до следующего открытия, каким бы ни было положение
  // кнопки архива, а не выкидывается сразу. Значение держит клиент, а не
  // свежий ответ сервера: перечитка списка идёт следом за уборкой, не дожидаясь
  // её собственного ответа, и более быстрый GET мог обогнать ещё не
  // подтверждённый POST, вернув старое значение и погасив строку раньше
  // времени.
  const justToggled = new Map();
  const draw = () => {
    const q = find.value.trim().toLowerCase();
    paintArch();
    for (const [id, wanted] of justToggled) {
      const c = st.chats.find((x) => x.id === id);
      if (c) c.archived = wanted;
    }
    // Положение кнопки архива режет список только при пустом запросе: набранный
    // текст ищет по всей машине мимо кнопки (DK-726), а найденную архивную
    // строку помечает клеймом «в архиве» сам chatOption.
    let list = q ? st.chats : chatArchShown(st.chats, chatArchMode());
    if (!q) {
      const have = new Set(list.map((c) => c.id));
      const pin = (id) => {
        if (!id || have.has(id)) return;
        const c = st.chats.find((x) => x.id === id);
        if (c) { list.push(c); have.add(id); }
      };
      for (const id of justToggled.keys()) pin(id);
      pin(st.sid);
    }
    list = list.filter((c) => {
      if (!q) return true;
      return (c.title || "").toLowerCase().includes(q) ||
        c.id.toLowerCase().includes(q) ||
        (c.tasks || []).join(" ").toLowerCase().includes(q) ||
        (c.project || project).toLowerCase().includes(q);
    });
    if (!q) {
      freezeOrder(list);
      list = list.slice().sort((a, b) =>
        (order.has(a.id) ? order.get(a.id) : Infinity) -
        (order.has(b.id) ? order.get(b.id) : Infinity));
    }
    rows.replaceChildren();
    for (const g of chatGroups(list, st.sid)) {
      if (g.head) rows.append(el("div", "cdday", g.head));
      for (const c of g.rows) rows.append(chatOption(project, c, st.sid, done));
    }
    if (!list.length) {
      // Пустому списку словами отвечает сервер, чем бы ни было поле поиска:
      // «ничего не нашлось» имеет смысл, только когда искали среди чего-то.
      // Пустой архив это свой случай: разговоры есть, просто убранных среди них
      // нет, и молчать об этом значит показывать пустоту без причины.
      let said = st.note || "чатов тут нет";
      if (st.chats.length && q) {
        said = "по запросу ничего не нашлось";
      } else if (st.chats.length && chatArchMode() === "only") {
        said = "в архиве пусто";
      } else if (st.chats.length && !q) {
        // Загруженный набор есть, а показать нечего: положение кнопки спрятало
        // его целиком, например единственный разговор задачи убран в архив.
        // Молчать тут значит читаться как «такого разговора нет», хотя он есть
        // и находится поиском или другим положением кнопки.
        const hidden = st.chats.filter((c) =>
          chatArchMode() === "only" ? !c.archived : c.archived).length;
        if (hidden) {
          const noun = plural(hidden, "разговор", "разговора", "разговоров");
          const verb = hidden === 1 ? "скрыт" : "скрыты";
          said = hidden + " " + noun + " " + verb + " кнопкой архива, ищите запросом";
        }
      }
      rows.append(el("div", "hint", said));
    }
    if (loading) rows.append(el("div", "hint", "ищем по всей машине..."));
    // Окно снимается кнопкой в конце списка: следующая ступень догружается
    // тем же запросом, что и весь список. За поиском кнопки нет вовсе, там
    // список и так приехал целиком.
    // Кнопка стоит, пока окно вообще есть: со снятым окном (days=0) глубже
    // потолка ручки список не достаёт, и кнопке было бы нечего догружать.
    if (!q && st.older && st.days && !loading) {
      const more = el("button", "cdmore", "показать раньше");
      more.addEventListener("click", () => {
        if (loading) return;
        loading = true;
        draw();
        chatLoadWindow(project, st, chatMoreDays(st.days)).then(() => {
          loading = false;
          draw();
        });
      });
      rows.append(more);
    }
  };
  // Список живёт в памяти вкладки, а разговоры родятся и закрываются мимо неё:
  // свой новый чат заводится тут же в панели, соседний в другой вкладке или с
  // телефона, а заголовок дописывает фоновая работа. Прежде перечень читался
  // один раз при сборке панели, и возврат в открытый разговор брал её из пула
  // готовой, поэтому нового чата в списке не было до перезагрузки страницы
  // (жалоба пользователя с шагами). Теперь каждое открытие спрашивает список
  // заново, тем же окном, каким он был прочитан.
  const refresh = () => {
    if (fresh) return;
    fresh = true;
    chatLoadWindow(project, st, st.days).then((ok) => {
      fresh = false;
      // Рисуется только свой список: пока ответ ехал, человек мог закрыть его
      // или открыть заново, и чужой узел трогать нечем.
      if (ok && chatDrop === box) draw();
    }).catch((e) => { fresh = false; console.error(e); });
  };
  // Строка закрыта или убрана в архив: список перерисовывается сразу, не
  // дожидаясь ответа, а свежий перечень подтверждает это следом. Закрытие
  // (kind "dropped") стирает запись насовсем, уборка ("archived"/"restored")
  // держит её на месте гашёной до следующего открытия. Убрали в архив тот
  // самый разговор, что открыт в панели, значит панель уходит на следующий:
  // второй заход в список для этого не нужен (развилка автора DK-656).
  function done(id, kind) {
    if (kind === "dropped") {
      if (id) st.chats = st.chats.filter((c) => c.id !== id);
    } else if (id && (kind === "archived" || kind === "restored")) {
      justToggled.set(id, kind === "archived");
    }
    if (kind === "archived" && id === st.sid) switchChat(chatPanelNext(project, st, id));
    draw();
    refresh();
  }
  find.addEventListener("input", () => {
    // Поиск общий по всей машине и окна не знает: набрали запрос, значит
    // список догружается целиком, и дальше ищется по всему, что есть на
    // машине. Один заход за панель: догруженное остаётся в состоянии.
    if (find.value.trim() && st.days && !loading) {
      loading = true;
      chatLoadWindow(project, st, 0).then(() => {
        loading = false;
        draw();
      });
    }
    draw();
  });
  arch.addEventListener("click", (ev) => {
    ev.stopPropagation();
    chatArchSet(chatArchNext(chatArchMode()));
    // Смена положения это другой список не хуже нового открытия: три набора
    // должны остаться настоящими три (положения кнопки задача DK-656 не
    // трогает), а не подмешанным прежним замером.
    order = null;
    justToggled.clear();
    draw();
  });
  draw();
  top.append(find, arch);
  box.append(top, rows);
  anchor.append(box);
  chatDropSet(box);
  if (!again) refresh();
  // Курсор в поле поиска ставится только там, где клавиатуре есть где встать.
  // На телефоне она выезжает на пол-экрана раньше, чем человек успел взглянуть
  // на список, и первым делом её приходится убирать («фокус сразу попадает на
  // поле поиска и выезжает клавиатура», замечание пользователя). Порог тот же,
  // каким отличается весь мобильный вид.
  if (!narrowScreen()) find.focus();
}

// Шапка окна: выбор диалога, «+», модель, переключатель фильтра и крестик.
// Больше входов в разговор нигде нет: с экрана задачи окно открывает тот же
// значок в шапке дашборда.
// Выбор модели стоит в строке отправки, слева от кнопки продолжения: меняют
// модель перед репликой, а не перед чтением ленты, и тянуться за ней в шапку
// было незачем (замечание 8 четырнадцатого круга POC). Имена в списке короткие:
// ярус с подпиской ушли в подсказку, скобки из списка ушли совсем.
function modelPick(project, st) {
  const model = el("select", "cdsel");
  model.setAttribute("aria-label", "Модель агента");
  const live = st.entry ? st.entry.liveModel : "";
  const cur = st.entry ? st.entry.model || chatModelPref() : chatModelPref();
  const isLive = Boolean(st.entry && st.entry.state === "live");
  // Чужую живую сессию выбором с дашборда не переубедить: её клиент уже
  // поднят, и модель у него своя до самого резюма.
  const alien = Boolean(isLive && !st.entry.own);
  // Живая сессия показывается своей настоящей моделью: молча показывать выбор
  // поверх работающей модели значило бы врать (замечание пользователя: выбран
  // opus, работает fable). Своя живая сессия называет модель записью подъёма,
  // а не транскриптом, поэтому смена видна в списке сразу же.
  const shown = isLive && live ? live : cur;
  // Лестница приезжает от agentctl: имя модели, ярус и подписка, чьей квотой
  // она платится. Своего перечня имён у панели нет, иначе новая подписка на
  // машине не появилась бы тут вовсе.
  const opts = (st.models || []).slice();
  for (const name of [cur, shown]) {
    if (name && !opts.some((m) => m.model === name)) opts.unshift({ model: name, tier: "", harness: "" });
  }
  for (const m of opts) {
    const o = el("option", "", m.model);
    o.value = m.model;
    if (m.tier) o.title = m.tier + ", " + m.harness;
    if (m.model === shown) o.selected = true;
    model.append(o);
  }
  // Пустая лестница видна там, где человек её и ищет: он открывает список и
  // находит в нём одну строку. Строкой же и сказано, что выбора нет, а причина
  // стоит подсказкой на самом списке.
  if (!(st.models || []).length) {
    const none = el("option", "", "выбора нет: лестница моделей не приехала");
    none.value = "";
    none.disabled = true;
    model.append(none);
  }
  const why = (st.models || []).find((m) => m.model === shown);
  model.title = why ? shown + ": ярус " + why.tier + ", подписка " + why.harness
    : (st.modelsNote || "Модель агента");
  const harnessOf = (name) => (((st.models || []).find((m) => m.model === name) || {}).harness) || "";
  const mainHarness = (((st.models || []).find((m) => m.default) || {}).harness) || "";
  // Живой разговор на второй подписке моделью отсюда не переубедить: смена
  // модели это рамка devkit-remodel, а она поднимает резюм в каталоге
  // первой подписки, история же разговора живёт в каталоге второй, и на
  // другой подписке её не продолжить. Селектор там не действие, а честный
  // текст: подписку называет подсказка. Отдельной метки с именем подписки
  // рядом не стоит: имя модели и так называет подписку однозначно, а метка
  // выглядела кнопкой и путала.
  const own = harnessOf(shown);
  const second = Boolean(isLive && own && mainHarness && own !== mainHarness);
  if (alien) {
    model.disabled = true;
    model.title = "Модель выбрана в самом vscode: с дашборда она сменится только на резюме этого чата.";
  } else if (second) {
    model.disabled = true;
    model.title = "Разговор идёт на подписке " + own + ": сменить модель живого разговора " +
      "панель не может, история живёт в каталоге этой подписки. Нужная модель выбирается " +
      "подъёмом нового чата.";
  }
  const box = el("div", "cmodel");
  box.append(model);
  model.addEventListener("change", () => {
    // Запертый список это текст, а не действие: у чужого окна и у второй
    // подписки менять отсюда нечего, и молча писать выбор в память диалога
    // значило бы обещать смену, которой не будет.
    if (model.disabled) return;
    const pick = model.value;
    if (pick === shown) return;
    chatModelSet(pick);
    // Про удачную смену карточка не всплывает: выбранное имя стоит в самом
    // списке, а у заведённого разговора про смену говорит разделитель ленты.
    // Карточка поверх экрана повторяла это третий раз (замечание
    // пользователя). Отказ карточкой остаётся: он ничем больше не виден.
    if (!st.sid) {
      if (st.blank) {
        chatModelKeep(project, st.blank, pick);
        // Выбранное имя нужно тут же и подъёму: он читает модель из записи
        // панели, а перерисовка приедет позже реплики.
        if (st.entry) st.entry.model = pick;
      }
      return;
    }
    modelSwitch(project, st, pick, isLive && !alien && !second).catch(console.error);
  });
  return box;
}

// modelSwitch меняет модель диалога прямо выбором в списке, без единого
// подтверждения: человек уже сказал, чего хочет, а плашка «перезапустить?»
// была вторым нажатием на ту же мысль (замечание пользователя). Выбор
// записывается в память диалога, и живой разговор тут же снимается и
// поднимается резюмом новой моделью: контекст резюм сохраняет, а на ходу
// модель клиенту не подменить. Мёртвому разговору хватает записи: его поднимет
// ближайший резюм.
//
// Удача идёт молча: про смену говорит разделитель в ленте («модель изменена:
// fable -> opus»), его пишет в журнал разговора сама ручка модели, и карточка
// поверх экрана говорила то же самое вторым голосом. Отказ карточкой остаётся:
// разделителя на него не будет, и без карточки человек читал бы ленту старой
// модели, думая, что сменил её.
async function modelSwitch(project, st, pick, live) {
  const at = chatsURL(project) + "/" + encodeURIComponent(st.sid);
  const set = await api(at + "/model", { method: "POST", body: { model: pick } });
  if (!set.ok) {
    sayResult(apiSaid(set), true);
    return;
  }
  if (!live) {
    await repaintChat();
    return;
  }
  const drop = await api(at + "/stop", { method: "POST", body: { drop: true } });
  if (!drop.ok) {
    sayResult(drop.body.error || "сессия не снялась", true);
    await repaintChat();
    return;
  }
  const r = await api(at + "/say", { method: "POST", body: { text: chatRemodelSay(pick) } });
  if (!r.ok) sayResult(apiSaid(r), true);
  if (r.ok && r.body.way === "resume") chatWait(project, r.body.tmux).catch(console.error);
  await repaintChat();
}

// Заказ продолжения после смены модели. Своих слов человек тут не говорил, и
// говорить за него нельзя: заказ едет служебной рамкой, которую лента не
// показывает пузырём вовсе. Про саму смену в ленте говорит разделитель, его
// пишет в журнал разговора ручка модели.
function chatRemodelSay(pick) {
  return "<devkit-remodel>Разговор продолжается моделью " + pick +
    ". Продолжай с того места, где остановился.</devkit-remodel>";
}

// Кольцо агентов в шапке разговора (макет пользователя). Пять сегментов это
// фазы конвейера задачи, бегущая поверх них дуга значит «события в транскрипте
// текут», число в середине это агенты в чатах задачи. Всё приезжает одной
// ручкой-агрегатом /pulse: собирать это на клиенте значило бы четыре запроса на
// каждый оборот опроса.
// Состояния пульса словами: те же, что зовёт сервер. Спящим считается и
// молчащий чат, и разговор без единой живой сессии.
const pulseEmptyState = "empty";
const pulseSilentState = "silent";

const RING_NS = "http://www.w3.org/2000/svg";
const RING_R = 15;
const RING_LEN = 2 * Math.PI * RING_R;
// Толщина кольца одна на всё, что по нему рисуется: и деления этапов, и
// бегущая подсветка занятости берут её отсюда.
const RING_W = 3.2;
// Бегущая подсветка занятости идёт по самому кольцу этапов: тем же радиусом и
// той же толщиной, что деления. Тонкой дугой внутри она читалась вторым
// кружком, отдельным прибором рядом с кольцом, а не жизнью этой работы
// (замечание пользователя). Сегменты под ней видны насквозь, за это отвечает
// прозрачность в стилях. Длина отрезка это доля кольца.
const RING_SPIN_PART = 0.14;

function svgEl(tag, cls) {
  const node = document.createElementNS(RING_NS, tag);
  if (cls) node.setAttribute("class", cls);
  return node;
}

function svgAttrs(node, attrs) {
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, String(v));
  return node;
}

// Давность словами. Секунды тут нужны: разница между «12 с» и «4 мин» и есть
// вся разница между работающим агентом и молчащим, а workAge ниже минуты
// говорит «меньше минуты» и обе склеивает.
function pulseAge(sec, now) {
  if (!sec) return "";
  const d = Math.max(0, Math.floor(now / 1000 - sec));
  if (d < 60) return d + " с";
  if (d < 3600) return Math.floor(d / 60) + " мин";
  return Math.floor(d / 3600) + " ч " + Math.floor((d % 3600) / 60) + " мин";
}

// Строка состояния под названием разговора говорит про сам этот разговор, а не
// про задачу целиком. Кольцо рядом считает задачу и показывает, что ждёт
// соседняя сессия; в словах под названием такое ожидание врало бы, что вопрос
// задан здесь, и человек искал бы в ленте вопрос, которого в ней нет.
function pulseSubject(p) {
  if (!p) return null;
  // Открытого разговора может не быть вовсе (панель стоит на задаче без чатов),
  // и тогда говорить приходится про кольцо целиком. Слова шкалы в обоих случаях
  // одни: шкала считает задачу, а не разговор.
  const it = p.own || { state: p.state, tool: p.tool, about: p.about, sub: p.sub,
    since: p.since };
  return { state: it.state, tool: it.tool, about: it.about, sub: it.sub,
    since: it.since, held: it.held,
    wait: p.own_wait || (p.own ? null : p.wait),
    wait_since: it.wait_since };
}

// Сколько довода хода помещается в строку состояния. Сервер режет его до
// полусотни знаков, и на телефоне этого много: под названием разговора строка
// одна, и длинный довод выдавливал бы из неё всё остальное.
const WHY_MAX = 44;

// Обрезка по длине с многоточием из трёх точек: одним символом многоточие
// писать нельзя (правила символов), а без обрезки длинный довод выдавливает
// из строки состояния всё остальное.
function truncate(text, max) {
  const t = String(text || "");
  return t.length > max ? t.slice(0, max - 3).trimEnd() + "..." : t;
}

// Части строки состояния, каждая своим полем. Имя инструмента и довод хода
// разведены нарочно: склеенные в одно предложение, они читались кашей
// («последний ход SendMessage Кольцо врёт прогрессом: чинить класс» это имя
// инструмента, слипшееся с началом реплики), и человек не понимал, где
// кончается одно и начинается другое.
function pulseParts(p, now) {
  const it = pulseSubject(p);
  if (!it || it.state === "empty") return [{ text: "живых сессий нет" }];
  if (it.state === "waiting") {
    const from = it.wait_since || (it.wait || {}).since;
    const age = pulseAge(from, now);
    return [{ text: "вопрос человеку" }, { text: age ? age + " без ответа" : "" }];
  }
  // Приставка («последний ход») цепляется к первому непустому полю: у записи
  // старого харнеса имени инструмента может не быть вовсе, и приставка тогда
  // пропала бы вместе с ним.
  // Кусок команды с путём в строку не идёт: он занимал её целиком и читался
  // мусором («SC=/private/tmp/...»), а сказать должен был одно, чем агент
  // занят. Смотреть команду целиком есть где, в самой ленте.
  const move = (lead) => {
    const list = [
      { text: it.sub || "", cls: "csub" },
      { text: it.tool || "", cls: "ctool" },
    ];
    const head = list.find((x) => x.text);
    if (head && lead) head.lead = lead;
    return list;
  };
  if (it.state === "silent" || it.state === "idle") {
    // Простой это не тишина в эфире, а сессия без хода: она жива и её можно
    // спросить. Слово выбрано так, чтобы его не читали как вопрос человеку.
    const age = pulseAge(it.since, now);
    return [{ text: "простаивает" + (age ? " " + age : "") }].concat(
      it.tool || it.about ? move("последний ход ") : []);
  }
  const age = pulseAge(it.since, now);
  // Ход, который идёт прямо сейчас, подписан «идёт», а не давностью: у долгой
  // команды в журнале одна запись в начале, и «две минуты назад» читалось бы
  // как «агент замолчал», тогда как он занят ровно этой командой.
  const when = it.held ? "идёт " + age : age + " назад";
  // Прогресса цели тут нет: его несёт кольцо делениями, и второй раз словами
  // он только забивал строку.
  return move("").concat([{ text: age ? when : "" }]);
}

// Те же части одной строкой: подсказка кольца и заголовок окна берут слова,
// а не разметку.
function pulseWords(p, now) {
  return pulseParts(p, now).map((x) => (x.text ? (x.lead || "") + x.text : ""))
    .filter(Boolean).join(" | ");
}

// Чем занят агент строкой списка: работающий назван инструментом и давностью
// хода, ждущий сроком без ответа, простаивающий сроком простоя. Простой и
// ожидание тут разные слова нарочно: у первого никто ничего не спрашивал.
function pulseAgentParts(a, now) {
  if (a.state === "waiting") {
    const age = pulseAge(a.wait_since || a.since, now);
    return [{ text: "ждёт ответа" }, { text: age ? age + " без ответа" : "" }];
  }
  const age = pulseAge(a.since, now);
  if (a.state === "silent" || a.state === "idle") {
    return [{ text: "простаивает" + (age ? " " + age : "") }];
  }
  return [
    { text: a.sub || "", cls: "csub" },
    { text: a.tool || "", cls: "ctool" },
    { text: truncate(a.about || "", WHY_MAX), cls: "cwhy" },
    { text: age ? (a.held ? "идёт " + age : age) : "" },
  ];
}

function pulseAgentWords(a, now) {
  return pulseAgentParts(a, now).map((x) => x.text).filter(Boolean).join(" | ");
}

// С какого момента ждут: человеку нужен не только срок, но и час, с которого
// разговор стоит. Час без даты: ожидание старше суток тут не живёт, его снимает
// страховка сторожка.
function pulseAgentSince(a) {
  if (a.state !== "waiting" || !a.wait_since) return "";
  return "с " + localTime(new Date(a.wait_since * 1000).toISOString());
}

// Деления шкалы: закрашены пройденные, остальные лежат разметкой. Процентов у
// деления нет, оно либо пройдено, либо нет.
//
// Шкала бывает трёх видов, и разница между ними не косметическая. У задачи с
// записью этапов это пять фаз конвейера. У цели конвейер неприменим по смыслу:
// она не проходит код с ревью, а режется на задачи, и её ход это доля закрытых.
// А там, где источник молчит, шкалы нет вовсе: кольцо остаётся индикатором
// состояния, потому что нарисованное деление человек читает как знание о ходе
// работы, которого у дашборда нет.

function ringArc(cls, span, at) {
  const c = svgEl("circle", cls);
  svgAttrs(c, {
    cx: 18, cy: 18, r: RING_R, fill: "none",
    "stroke-width": RING_W, "stroke-linecap": "butt",
    "stroke-dasharray": Math.max(span, 0.01).toFixed(2) + " " + (RING_LEN - span).toFixed(2),
    "stroke-dashoffset": (-at).toFixed(2),
  });
  return c;
}

// Дорожка кольца. Шкалы хода на нём больше нет вовсе: деления задач нарезки и
// фазы конвейера были немой разметкой («что значат девять сегментов и шесть
// зелёных?»), а ход работы показан там, где ему место, блоком «Задачи цели» на
// экране цели и этапом на экране задачи. Кольцо осталось про агентов: сколько
// работает, кто ждёт, каким цветом.
function ringTrack(box) {
  box.append(ringArc("track", RING_LEN, 0));
}

// Сколько пунктов плана кольцо рисует делениями. Длинный план это уже пунктир,
// и вместо него закрашивается доля сделанного.
const RING_MAX_SEGS = 12;
const RING_GAP = 3;

// Сколько закрытых этапов кольцо оставляет на виду. Кольцо показывает текущую
// работу, а не историю сессии: у сессии, которая делегирует третий день,
// этапов набирается под сотню, дольки становятся тоньше волоса и сливаются в
// сплошную полосу (замечание пользователя). В окне остаются все незакрытые
// этапы и последние закрытые, полное число живёт в подсказке.
const RING_DONE_KEEP = 5;

// Окно кольца: незакрытые этапы целиком, закрытые последние. Порядок пунктов
// сохраняется, потому что он и есть ход работы. Если и после этого этапов
// больше, чем кольцо умеет показать дольками, остаются свежие: сплошная дуга
// вместо долек это и есть та полоса, на которую жаловался человек.
function ringWindow(plan) {
  const list = plan || [];
  const done = list.filter((it) => it.state === "completed");
  const skip = Math.max(0, done.length - RING_DONE_KEEP);
  let seen = 0;
  const kept = list.filter((it) => {
    if (it.state !== "completed") return true;
    seen += 1;
    return seen > skip;
  });
  return kept.length > RING_MAX_SEGS ? kept.slice(kept.length - RING_MAX_SEGS) : kept;
}

// Деления кольца это этапы работы сессии: закрашенное сделано, подсвеченное
// идёт, пустое ждёт. Этапов нет, значит делений нет вовсе: ровная дорожка
// честнее выдуманной шкалы. Сюда приходит окно (ringWindow), поэтому долек
// всегда столько, сколько кольцо умеет показать: прежде длинный план рисовался
// одной сплошной дугой с засечкой, и она читалась слипшейся полосой.
function ringPlan(box, plan) {
  const done = plan.filter((it) => it.state === "completed").length;
  // План выполнен целиком: кольцо замыкается одной дугой. Щели между
  // делениями тут читались бы как незакрытые пункты, которых нет.
  if (done === plan.length) {
    box.append(ringArc("seg on", RING_LEN, 0));
    return;
  }
  const step = RING_LEN / plan.length;
  const arc = Math.max(step - RING_GAP, 1);
  plan.forEach((it, i) => {
    const cls = it.state === "completed" ? " on" : it.state === "in_progress" ? " here" : "";
    box.append(ringArc("seg" + cls, arc, i * step));
  });
}

// Список плана: галочка у сделанного, стрелка у идущего, пусто у ждущего.
// Идущий пункт назван формой «делаю» (activeForm), как его пишет сам агент.
function planList(plan) {
  const box = el("div", "plist");
  for (const it of plan) {
    const row = el("div", "prow2 p-" + (it.state || "pending"));
    row.append(el("span", "pmark", it.state === "completed" ? "+"
      : it.state === "in_progress" ? ">" : ""));
    row.append(el("span", "ptext2",
      it.state === "in_progress" && it.active ? it.active : it.text));
    // Работа субагента помечена: в файле плана сессии такого пункта нет вовсе,
    // он приехал с розданной работы, боковым журналом либо планом самого
    // субагента, и человек, сверяющий кольцо с планом, иначе читает его чужим
    // (живой разбор сессии devkit-2e).
    if (it.src === "sub") {
      row.append(withTip(el("span", "psub", "субагент"),
        "пункт розданной работы: боковой журнал вызова или план самого субагента, а не план сессии"));
    }
    box.append(row);
  }
  return box;
}

// Кольцо целиком. Разметка одна на все четыре состояния, состояние это класс
// обёртки: разводить четыре ветки сборки значило бы держать четыре разметки.
function pulseRing(project, p) {
  // Узел кольца живёт весь заход, а не пересобирается каждым тиком пульса:
  // пересборка снимала открытый по клику список, и он закрывался от любой
  // записи агента в ленте (жалоба пользователя). Данные обновляются на месте.
  const wrap = el("div", "ringwrap");
  const pop = el("div", "pop");
  // Список открывается и закрывается кликом по кольцу, а мимо и Escape его
  // гасит общий учёт всплывашек. Наведением он больше не показывается вовсе:
  // показ по hover держал список открытым поверх снятого класса, и второй
  // клик по кольцу выглядел не работающим (жалоба пользователя).
  let held = null;
  const shut = () => {
    wrap.classList.remove("open");
    held = null;
  };
  wrap.addEventListener("click", (ev) => {
    ev.stopPropagation();
    // Клик по строке списка ведёт в свой разговор и закрытия не значит: сюда
    // он доходит только с пустого места контейнера.
    if (ev.target && ev.target !== wrap && nodeInside(pop, ev.target)) return;
    if (wrap.classList.contains("open")) {
      popupDrop(held);
      shut();
      return;
    }
    popupsShut(null);
    wrap.classList.add("open");
    held = popupHold(wrap, shut);
  });
  wrap.ringFill = (next) => {
    const open = wrap.classList.contains("open");
    const box = svgEl("svg", "ring");
    svgAttrs(box, { viewBox: "0 0 36 36", width: 36, height: 36 });
    const g = svgEl("g", "");
    g.setAttribute("transform", "rotate(-90 18 18)");
    // Ореол ожидания лежит под сегментами: он пульсирует вокруг кольца, а не
    // поверх числа.
    const halo = svgEl("circle", "halo");
    svgAttrs(halo, { cx: 18, cy: 18, r: 17.4, fill: "none", "stroke-width": 1.2 });
    box.append(halo);
    const plan = (next && next.plan) || [];
    // Плана нет и все спят: кольца нет вовсе, вместо него тонкий контур той же
    // кликабельной зоны. Серый бублик обещал бы работу, которой нет, а вход в
    // список агентов нужен и тут.
    // Парковка идёт сюда же: ожидание строки это её состояние, и бублик с
    // числом обещал бы живую работу, которой нет вовсе.
    const asleep = !next || next.state === pulseEmptyState || next.state === pulseSilentState ||
      Boolean(next && next.parked);
    const ghost = !plan.length && asleep;
    wrap.className = "ringwrap r-" + ((next && next.state) || "empty") +
      (next && next.parked ? " parked" : "") +
      (ghost ? " ghost" : "") + (open ? " open" : "");
    // Сегменты и счёт в центре идут по окну: сотня долек за три дня работы
    // сливалась в сплошную полосу и не говорила ни о чём.
    const shown = ringWindow(plan);
    if (ghost) g.append(ringArc("ghost", RING_LEN, 0));
    else if (shown.length) ringPlan(g, shown);
    else ringTrack(g);
    // Бегущая подсветка: она и значит, что события текут. Крутит её анимация, а
    // не опрос, поэтому между заходами на сервер кольцо не замирает. Идёт она
    // тем же радиусом и той же толщиной, что деления, то есть подсвечивает
    // само кольцо, а дробь в середине не задевает вовсе.
    const comet = svgEl("circle", "comet");
    svgAttrs(comet, {
      cx: 18, cy: 18, r: RING_R, fill: "none", "stroke-width": RING_W,
      "stroke-linecap": "round",
      "stroke-dasharray": (RING_LEN * RING_SPIN_PART).toFixed(2) + " " +
        (RING_LEN * (1 - RING_SPIN_PART)).toFixed(2),
    });
    g.append(comet);
    box.append(g);
    // В середине стоят работающие, а у ждущего кольца ждущие. Простаивающие
    // сюда не идут: сложенные с работающими они врали, что работа кипит, тогда
    // как второй разговор задачи стоит без хода второй час.
    // Дробь в середине считается за сессию целиком, а не по окну показа:
    // числитель по окну залипал на пятёрке навсегда (окно держит пять
    // последних закрытых), и продвижения работы по кольцу видно не было
    // (решение пользователя). Окно осталось тем, чем и было, нарезкой
    // сегментов: сотня долек за три дня работы сливалась в сплошную полосу.
    const num = ringNumber(next, plan);
    if (num) box.append(ringNum(num));
    fillPop(pop, project, next);
    wrap.replaceChildren(box, pop);
    // Подсказка у кольца одна, всплывающим списком: браузерная подсказка поверх
    // него говорила то же самое вторым разом и перекрывала сам список.
    const tip = [ringStages(plan, shown), ringTally(next), pulseWords(next, Date.now())]
      .filter(Boolean).join(". ");
    wrap.setAttribute("aria-label", tip);
  };
  wrap.ringFill(p);
  return wrap;
}

// Что стоит в середине кольца: ход работы этапами, «выполнено/всего». Число
// агентов там ничего не говорило человеку («отображение количества агентов
// неинформативно», замечание пользователя): агент один и тот же, а знать надо,
// сколько шагов работы позади. Считается дробь за сессию целиком: по окну
// показа числитель залипал на пятёрке навсегда, потому что окно держит ровно
// пять последних закрытых этапов, и продвижение работы по кольцу было не
// видно. Этапов нет вовсе, значит в середине остаётся прежнее: ждущие у
// ждущего кольца, работающие у работающего.
// Число в середине кольца. Ход по этапам стоит дробью в два яруса: выполнено
// сверху, всего снизу, между ними тонкая черта. В строку («5/7») каждое число
// делило ширину с соседом и с косой чертой, и крупный шрифт задевал дугу, а
// мелкий не читался (решение пользователя). В два яруса каждому числу
// достаётся вся ширина просвета, и шрифт берётся крупнее. Прочие значения
// (ждущие, работающие) это одно число, и ярус у него один. Числа тут бывают
// двузначными с обеих сторон (43/57 у сессии, которая делегирует третий день),
// и просвет кольца под это и мерялся.
function ringNum(num) {
  const parts = String(num).split("/");
  if (parts.length !== 2) {
    const one = svgEl("text", "rnum");
    svgAttrs(one, { x: 18, y: 18, "text-anchor": "middle", "dominant-baseline": "central" });
    one.textContent = num;
    return one;
  }
  // Трёхзначный ярус в просвет кольца тем же кеглем не влезает, а сессии с
  // сотней этапов бывают: такой дроби кегль сбавляется классом, и место ей
  // считает та же вёрстка.
  const wide = Math.max(parts[0].length, parts[1].length) > 2;
  const g = svgEl("g", "rfrac" + (wide ? " rlong" : ""));
  // Ярусы стоят вплотную к черте: просвет кольца это круг радиусом около
  // четырнадцати, и дробь целиком укладывается в его середину.
  const top = svgEl("text", "rnum");
  svgAttrs(top, { x: 18, y: 12.4, "text-anchor": "middle", "dominant-baseline": "central" });
  top.textContent = parts[0];
  const line = svgEl("line", "rbar");
  // Черта чуть шире самого длинного яруса и заметно внутри дуги: этапы считают
  // за сессию целиком, и ярус это одна или две цифры.
  const half = wide ? 7 : (Math.max(parts[0].length, parts[1].length) > 1 ? 6 : 4.5);
  svgAttrs(line, { x1: 18 - half, x2: 18 + half, y1: 18, y2: 18, "stroke-width": 0.9 });
  const low = svgEl("text", "rnum");
  svgAttrs(low, { x: 18, y: 23.6, "text-anchor": "middle", "dominant-baseline": "central" });
  low.textContent = parts[1];
  g.append(top, line, low);
  return g;
}

// list тут это весь план сессии, а не окно показа: дробь считает сессию
// целиком.
function ringNumber(p, list) {
  list = list || [];
  if (list.length) {
    return list.filter((it) => it.state === "completed").length + "/" + list.length;
  }
  if (!p) return "";
  // Ждёт сама строка, а разговоров за ней нет: единица в середине читалась как
  // счёт ждущих агентов, которых нет ни одного. Про блокировку тут говорят
  // слова рядом с кольцом.
  if (p.parked) return "";
  if (p.state === "waiting") return String(p.waiting || 1);
  return p.working > 0 ? String(p.working) : "";
}

// Этапы словами для подсказки: сколько их за сессию и сколько из них видно
// сегментами. Дробь в середине говорит про сессию целиком, а дольками кольцо
// показывает только окно, и без этих слов человек считал бы дольки, не сходясь
// с дробью.
function ringStages(plan, shown) {
  const list = plan || [];
  const all = list.length;
  if (!all) return "";
  const done = list.filter((it) => it.state === "completed").length;
  const said = "этапов за сессию " + done + " из " + all;
  const seen = (shown || []).length;
  return seen && seen < all ? said + ", сегментами видно " + seen + " последних" : said;
}

// Подпись кольца: сколько кого. Число в середине говорит про работающих, и без
// подписи человек не узнал бы, что рядом стоит второй разговор той же задачи.
function ringTally(p) {
  if (!p || !p.count) return "живых сессий нет";
  const bits = [];
  if (p.working) bits.push(p.working + " " + plural(p.working, "работает", "работают", "работают"));
  if (p.waiting) bits.push(p.waiting + " " + plural(p.waiting, "ждёт ответа", "ждут ответа", "ждут ответа"));
  if (p.idle) bits.push(p.idle + " " + plural(p.idle, "простаивает", "простаивают", "простаивают"));
  return bits.join(", ") || p.count + " " + plural(p.count, "разговор", "разговора", "разговоров");
}

// Всплывающий список агентов задачи. Строка ведёт в свой разговор: кольцо тут
// не только показывает, но и есть дорога до того, кто ждёт.
function ringPop(project, p) {
  const pop = el("div", "pop");
  fillPop(pop, project, p);
  return pop;
}

// Содержимое списка на месте: сам узел поповера живёт весь заход, иначе
// открытый список закрывался бы каждым тиком пульса.
function fillPop(pop, project, p) {
  pop.replaceChildren();
  // План сессии стоит первым: деления кольца это он, и без списка они немые.
  const plan = (p && p.plan) || [];
  if (plan.length) pop.append(planList(plan));
  const list = (p && p.agents) || [];
  const now = Date.now();
  if (!list.length) {
    pop.append(el("div", "prow pempty", "живых сессий нет"));
    return;
  }
  for (const a of list) {
    const row = el("div", "prow" + (a.own ? " own" : ""));
    row.append(el("span", "pdot p-" + a.state));
    const who = el("div", "pwho");
    const name = el("div", "pname");
    name.append(el("b", "", a.name));
    // Пометка открытого разговора стоит отдельным словом, а не приписью к
    // имени: приписанная, она резалась вместе с ним и пропадала первой.
    if (a.own) name.append(el("span", "pown", "открыт"));
    who.append(name);
    // Предмет разговора второй строкой: два чата одной задачи различаются
    // только им, и без него человек спрашивает, откуда в кольце второй агент.
    if (a.title) who.append(el("span", "ptitle", a.title));
    // Занятие идёт третьей строкой, а не колонкой справа: в двух колонках
    // моноширинная команда и имя захода отнимали ширину друг у друга, и
    // резались оба.
    const what = el("div", "pwhat");
    for (const part of pulseAgentParts(a, now)) {
      if (!part.text) continue;
      if (what.children.length) what.append(el("span", "csep", "|"));
      what.append(el("span", part.cls || "", part.text));
    }
    const from = pulseAgentSince(a);
    if (from) what.append(el("span", "pfrom", " " + from));
    who.append(what);
    row.append(who);
    row.addEventListener("click", (ev) => {
      ev.stopPropagation();
      switchChat(a.session);
    });
    pop.append(row);
  }
}

// Пульс опрашивается сам: шапка панели собирается один раз на открытие
// разговора, и перерисовка экрана её не трогает, поэтому кольцо ходит за своей
// ручкой по таймеру и меняет только себя.
const PULSE_POLL = 5000;

// Ручка спрашивается и про задачу, и про открытый разговор разом: кольцо
// считает задачу целиком, а слова под названием отвечают за тот чат, который
// человек видит, и собирать их двумя заходами незачем.
function pulseURL(project, st) {
  const q = [];
  if (st.task) q.push("task=" + encodeURIComponent(st.task));
  if (st.sid) q.push("sid=" + encodeURIComponent(st.sid));
  return "/api/projects/" + encodeURIComponent(project) + "/pulse?" + q.join("&");
}

// Блокировка строки словами рядом с кольцом. Прежде о ней сообщал один красный
// ореол с цифрой в середине: он моргал, как тревога, и не говорил ни чем задача
// заблокирована, ни что это вообще блокировка (замечание пользователя по
// снимку DK-466). Блокировка это состояние, а не событие, и место ей в словах.
function ringBlockSay(slot, p) {
  if (slot.blockChip) {
    slot.blockChip.remove();
    slot.blockChip = null;
  }
  if (!p || !p.parked || !p.block) return;
  const chip = withFull(el("span", "chip c-block cwhy", "блок: " + p.block), p.block);
  slot.append(chip);
  slot.blockChip = chip;
}

function wireRing(project, st, slot) {
  const put = (p) => {
    // Узел кольца переживает тик: пересборка снимала бы открытый список.
    const has = slot.children && slot.children[0];
    if (has && has.ringFill) has.ringFill(p);
    else slot.replaceChildren(pulseRing(project, p));
    ringBlockSay(slot, p);
  };
  const load = async () => {
    const r = await api(pulseURL(project, st));
    if (r.ok) put(r.body);
  };
  load().catch(console.error);
  const t = setInterval(() => { load().catch(console.error); }, PULSE_POLL);
  chatLive.push(() => clearInterval(t));
}

// Привязка разговора к задаче рукой. Дашборд узнаёт задачу сессии по реестру
// чатов, по первой реплике и по имени бокового дерева, и все три способа мимо,
// когда человек поднял клиента сам: чат чужой подписки к задаче не привяжется
// никогда, и в ленте задачи его не будет. Ручка привязки есть (DK-431), а
// вызова с экрана после переделки POC не осталось вовсе.
//
// Меню говорит про то, что тут делают, и больше ни про что. У разговора без
// задачи это привязка, у привязанного смена и снятие, и каждое названо своей
// кнопкой. Абзаца про реестр чатов и про снятие пустым значением тут нет
// нарочно: механику меню не пересказывает, а снятие делает кнопка, а не
// догадка про пустое поле (замечание пользователя).
function chatBindOpen(project, st, line) {
  chatDropShut();
  const menu = el("div", "cdrop cdbind");
  menu.append(el("div", "dwhy", st.task ? "Привязка к " + st.task : "Укажите задачу для привязки"));
  const field = el("input", "cbindin");
  field.type = "text";
  field.value = st.task || "";
  field.placeholder = "например, " + (st.task || "DK-123");
  field.setAttribute("aria-label", "номер задачи");
  menu.append(field);
  const send = (task) => {
    chatDropShut();
    bindSession(project, st.sid, task).catch(console.error);
  };
  const go = el("button", "btn btn-sm btn-acc", st.task ? "Сменить" : "Привязать");
  go.addEventListener("click", (ev) => {
    ev.stopPropagation();
    send(field.value.trim().toUpperCase());
  });
  menu.append(go);
  if (st.task) {
    const off = el("button", "btn btn-sm", "Снять привязку");
    off.addEventListener("click", (ev) => { ev.stopPropagation(); send(""); });
    menu.append(off);
  }
  menu.addEventListener("click", (ev) => { ev.stopPropagation(); });
  line.append(menu);
  chatDropSet(menu);
}

function chatHead(project, st) {
  const head = el("div", "chead");
  const line = el("div", "chline");

  const pick = el("button", "cdpick");
  // Номер задачи стоит лейблом при названии диалога, а не отдельной кнопкой
  // «Экран DK-397» под шапкой: место экономится, а нажатие ведёт туда же
  // (замечание 17).
  if (st.task) {
    const lab = withFull(el("span", "cdtask", st.task), st.title ? st.task + ": " + st.title : st.task);
    lab.addEventListener("click", (ev) => {
      ev.stopPropagation();
      goFromChat(project + "/" + st.task);
    });
    pick.append(lab);
  }
  // У каждого состояния панели своё имя, «чата нет» не говорит ни одно: у
  // нового чата диалога ещё нет по замыслу, протухший адрес назван находкой
  // честно, старый разговор глубже видимого списка подписан своим ID, а пустой
  // проект говорит про пустоту, не про поломку (замечания пользователя).
  const picked = st.lift ? "Сессия поднимается"
    : st.fresh ? "Новый чат"
    : st.lost ? "Чат не найден"
    : st.entry ? chatTitle(st.entry)
    : st.sid ? "чат " + st.sid.slice(0, 8)
    // Панель, открытая по задаче, называется задачей. «Чатов пока нет» тут
    // стояло парой с такой же строкой в ленте, и человек читал два сообщения о
    // пустоте подряд там, где панель уже открыта (замечание пользователя).
    : st.task ? "задача " + st.task
    : "Чатов пока нет";
  pick.append(withFull(el("b", "", picked), picked));
  const car = el("span", "cdcar");
  car.append(icon("i-caret"));
  pick.append(car);
  pick.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (chatDrop) {
      chatDropShut();
      return;
    }
    chatDropOpen(project, st, line);
  });
  line.append(pick);

  const add = el("button", "cdbtn");
  add.append(icon("i-plus"));
  add.title = "Новый чат";
  add.setAttribute("aria-label", "Новый чат");
  add.addEventListener("click", (ev) => {
    ev.stopPropagation();
    // Из контекста задачи новый чат бывает двух видов: про задачу (агент
    // получит её контекст хуком старта) и свободный, ни к чему не привязанный
    // (замечание 13). Без задачи выбирать не из чего, и меню не открывается.
    // Спрашивают тут разговор, а не панель: у панели задача своя, ею же
    // фильтруется список, и разговору, привязанному чужой доской, она пуста.
    const made = chatMakeTask(st);
    if (!made) {
      chatBlankMake(project, "").catch(console.error);
      return;
    }
    chatDropShut();
    const menu = el("div", "cdrop cdmenu");
    for (const [label, id] of [
      ["Чат задачи " + made, made],
      ["Произвольный чат", ""],
    ]) {
      const opt = el("div", "cdrow", label);
      opt.addEventListener("click", () => { chatDropShut(); chatBlankMake(project, id).catch(console.error); });
      menu.append(opt);
    }
    line.append(menu);
    chatDropSet(menu);
  });
  line.append(add);

  // Привязка к задаче стоит рядом с заведением нового чата: обе про то, чей
  // это разговор. У пустого адреса привязывать нечего, сессии ещё нет.
  //
  // Кнопка называет то, чего не хватает. У свободного чата это привязка, и
  // кнопка подсвечена. У привязанного привязка уже сделана и видна чипом
  // задачи, поэтому предлагать её второй раз нечем: кнопка тут говорит про
  // смену и снятие, а слова «привязать к задаче» из неё ушли (замечание
  // пользователя по снимку).
  if (st.sid) {
    const bind = el("button", "cdbtn" + (st.task ? "" : " warn"));
    bind.append(icon("i-in"));
    // Чем узнана задача разговора, говорит сервер (bindTask): «задача не с
    // доски проекта», «свободный чат», «говорит о XR-1». Раньше это стояло
    // плашкой под заголовком, а плашка занимала строку под то, что и так
    // сказано значком (замечание пользователя).
    const said = (st.entry && st.entry.note) ? " (" + st.entry.note + ")" : "";
    bind.title = st.task ? "Привязка к " + st.task + said + ": сменить или снять"
      : "Свободный чат" + said + ": привязать к задаче рукой";
    bind.setAttribute("aria-label", bind.title);
    bind.addEventListener("click", (ev) => {
      ev.stopPropagation();
      if (chatDrop) {
        chatDropShut();
        return;
      }
      chatBindOpen(project, st, line);
    });
    line.append(bind);
  }

  // Переключатель фильтра стоит справа и виден только там, где есть что
  // фильтровать: без задачи в адресе фильтровать нечем, и погашенная кнопка
  // там обещала бы несуществующее действие.
  if (st.task) {
    const filt = el("button", "cdbtn" + (chatFilterOn() ? " on" : ""));
    filt.append(icon("i-filter"));
    filt.title = chatFilterOn()
      ? "Список отфильтрован по " + st.task + ": нажмите, чтобы видеть все чаты"
      : "Список показывает все чаты: нажмите, чтобы оставить только " + st.task;
    filt.setAttribute("aria-label", filt.title);
    // Тумблер правит только состав выпадающего списка, а не экран: прежде он
    // звал общую перерисовку, и от переключения фильтра пересобирались панель
    // с лентой, хотя видимое содержимое то же самое (жалоба пользователя).
    const paintFilt = () => {
      filt.className = "cdbtn" + (chatFilterOn() ? " on" : "");
      filt.title = chatFilterOn()
        ? "Список отфильтрован по " + st.task + ": нажмите, чтобы видеть все чаты"
        : "Список показывает все чаты: нажмите, чтобы оставить только " + st.task;
      filt.setAttribute("aria-label", filt.title);
    };
    filt.addEventListener("click", (ev) => {
      ev.stopPropagation();
      chatFilterSet(!chatFilterOn());
      paintFilt();
      // Открытый список пересобирается на месте: он и есть всё, что меняет
      // фильтр. Закрытый соберётся с новым фильтром сам, когда его откроют.
      if (chatDrop) chatDropOpen(project, st, line, true);
    });
    line.append(filt);
  }

  const shut = el("button", "nx");
  shut.setAttribute("aria-label", "Закрыть панель");
  shut.title = "Закрыть панель";
  shut.append(icon("close"));
  shut.addEventListener("click", () => { chatDropShut(); closeChat(); });
  line.append(shut);
  // Кольцо агентов стоит слева от названия разговора, а не в строке доски:
  // человек смотрит в панель, когда разговаривает, и состояние захода нужно
  // ему тут же. Название с подписью уезжают в колонку справа от кольца.
  const ct = el("div", "ct");
  ct.append(line);
  const slot = el("div", "rslot");
  head.append(slot, ct);

  // Плашки под заголовком нет вовсе: подпись привязки уехала подсказкой на
  // значок привязки, метаданные разговора на его название, а состояние читается
  // кольцом и самой лентой. Строка под заголовком повторяла соседей и занимала
  // место, которого в шапке нет (замечание пользователя).
  if (st.entry) {
    const bits = [chatWhen(st.entry)];
    if ((st.entry.tasks || []).length) bits.push(st.entry.tasks.join(", "));
    if (st.entry.tree) bits.push(st.entry.tree);
    if (st.entry.tmux) bits.push("tmux " + st.entry.tmux);
    const tip = bits.filter(Boolean).join(", ");
    if (tip) {
      pick.title = tip;
      pick.setAttribute("aria-label", chatTitle(st.entry) + ": " + tip);
    }
  }
  wireRing(project, st, slot);
  // Шапка отдаёт переподъём кольца наружу, как панель отдаёт свой: слот пула
  // позовёт его при возврате в разговор.
  chatRingArm = () => wireRing(project, st, slot);
  return head;
}

// Блок плана на экране задачи. План берётся тем же пульсом, что кормит кольцо
// в шапке разговора: он уже знает ведущую живую сессию задачи, и второго
// разбора транскрипта тут не нужно. Пока плана нет, блока на экране нет.
function wireTaskPlan(project, id, page) {
  const card = pane("План агента", "");
  card.card.classList.add("tplan");
  card.card.hidden = true;
  page.append(card.card);
  const put = (p) => {
    const plan = (p && p.plan) || [];
    card.card.hidden = !plan.length;
    if (!plan.length) return;
    const who = (p.own && p.own.name) || (p.agents || []).map((a) => a.name)[0] || "";
    card.sub.textContent = who ? "сессия " + who : "";
    card.body.replaceChildren(planList(plan));
  };
  const load = async () => {
    const r = await api("/api/projects/" + encodeURIComponent(project) +
      "/pulse?task=" + encodeURIComponent(id));
    if (r.ok) put(r.body);
  };
  load().catch(console.error);
  const t = setInterval(() => { load().catch(console.error); }, PULSE_POLL);
  agentLive.push(() => clearInterval(t));
}

// Прерывать ход можно у своей работающей tmux-сессии: занятость приходит
// записью реестра (idle), а принадлежность именем tmux. Окно vscode это чужой
// процесс, мёртвая сессия ход не ведёт вовсе.
function chatStoppable(st) {
  const e = st && st.entry;
  return Boolean(e && e.state === "live" && e.tmux && !e.idle);
}

// Кнопки строки, чью работу ведёт наша сессия: разговор и продолжение. Стопа
// тут нет: снимать нечего, tmux-сессии дашборда у такой работы не бывает.
// Вход в разговор задачи прямо со строки. Стоит у каждой строки доски, чем бы
// она ни была занята: чат к конвейеру не привязан, у задачи без сессии он
// поднимет новый разговор, а у чужой работы разговор идёт у нас и её не
// трогает. Правило от этого объясняется одной фразой, «чат есть у каждой
// задачи», и человеку не приходится гадать, где кнопка есть, а где нет
// (замечание пользователя).
//
// Подпись у кнопки значком, а не словом: рядом стоит «Продолжить», и два
// слова подряд слипались в кашу.
// Ожидание ответа кнопка чата не помечает вовсе: признак живёт чипом у самой
// строки (waitChip), и кружок на кнопке дублировал его, вися рядом криво
// (замечание пользователя). Кнопка отвечает за одно, вход в разговор.
// works приезжают тем же обходом, что нарисовал строку: у строки доски это
// работы её проекта, у записи накопителя их нет вовсе, и тогда признак берётся
// у последнего обхода экрана.
// Кнопка разговора одна на все три раздела: тот же значок той же величины, та
// же подсказка по наведению, тот же класс. Строка сессии строила её своим
// куском кода, и значок чата у неё жил своей жизнью (замечание пользователя про
// разный вид табов).
function chatIconBtn(tip, aria, open, lively) {
  const talk = el("button", "btn btn-sm btn-ico rchat");
  talk.append(icon("i-chat"));
  // Мягко моргающая рамка: работа пошла, и разговор с ней есть где смотреть.
  // Панель при этом никого никуда не уводит, человек решает сам.
  if (lively) talk.classList.add("chatlive");
  withTip(talk, tip);
  talk.setAttribute("aria-label", aria);
  talk.addEventListener("click", (ev) => {
    ev.stopPropagation();
    open();
  });
  return talk;
}

function rowChatBtn(project, row, works) {
  const lively = taskLively(project, row.id, works || shownWorks);
  // Адрес разговора с идущим ходом называет сама строка (run_chat): при
  // нескольких рабочих сессиях сервер выбрал свежайшую по последней реплике.
  // Прежде иконка открывала адрес задачи, панель показывала список её чатов, и
  // до живого разговора человек делал ещё один клик, выбирая его глазами по
  // времени (DK-716). Работы за строкой нет, значит адрес задачи и есть
  // правильный вход: он откроет список или заведёт новый чат.
  const addr = row.run_chat || row.id;
  return chatIconBtn(lively ? "Чат по задаче: работа идёт" : "Чат по задаче",
    "Чат по задаче " + row.id, () => { openChat(chatAddr(project, addr)); }, lively);
}

// Кнопка стопа: красный квадрат в кружке рядом с отправкой. Прерывает ход, а не
// сессию: следующая реплика попадёт в тот же разговор с его памятью, а полное
// завершение живёт на экране задачи и в кнопке остановки конвейера.
//
// Плашка «думает...» гасится тут же, ответом самого стопа, а не опросом
// /status: тот считает занятость и по хвосту транскрипта, где незакрытый
// вызов инструмента висит до получаса, и после явного прерывания плашка
// зависала бы на весь этот срок, хотя сервер уже знает, что ход кончен
// (приёмка 2026-09-05).
function chatStopBtn(project, st, busy) {
  const stop = el("button", "cstop");
  stop.title = "Прервать текущий ход агента: сессия останется жить";
  stop.setAttribute("aria-label", stop.title);
  stop.append(icon("i-stop"));
  stop.addEventListener("click", (ev) => {
    ev.stopPropagation();
    stop.disabled = true;
    stopChat(project, st.sid)
      .then((ok) => { if (ok) busy.off(); })
      .catch(console.error)
      .finally(() => { stop.disabled = false; });
  });
  return stop;
}

async function stopChat(project, sid) {
  const r = await api(chatsURL(project) + "/" + encodeURIComponent(sid) + "/stop",
    { method: "POST", body: {} });
  sayResult(r.body.message || r.body.error || (r.ok ? "ход прерван" : "прервать не вышло"), !r.ok);
  return r.ok;
}

// Палец вместо мыши: у грубого указателя нет ни Shift под большим пальцем, ни
// привычки к Enter как отправке. Спрашивается это у самого браузера, а не
// угадывается по ширине окна: планшет с клавиатурой шире телефона, а указатель
// у него тот же.
function touchPointer() {
  try {
    return Boolean(window.matchMedia && window.matchMedia("(pointer: coarse)").matches);
  } catch (err) {
    return false;
  }
}

// Ответ задаче безадресной строкой: ручка та же, какой пользуется сторожок.
// Отдаётся тело ответа целиком, а не одна удача: ручка говорит, есть ли у
// задачи ведущая сессия, и от этого зависит, доставлена реплика или ждёт во
// входе. Пусто значит отказ ручки.
// Снятие лежащей во входе задачи реплики: отмена в панели убирает её не только
// с экрана, но и из очереди. Без этого отменённая реплика уезжала агенту первым
// же ходом, а человек считал её снятой (живой случай DK-466).
async function dropTaskLine(project, id, text) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/tasks/" + encodeURIComponent(id) + "/message", { method: "DELETE", body: { text } });
  sayResult(r.body.message || r.body.error || (r.ok ? "реплика снята" : "снять не вышло"), !r.ok);
  return r.ok;
}

async function answerTask(project, id, text) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/tasks/" + encodeURIComponent(id) + "/message", { method: "POST", body: { text } });
  sayResult(r.body.message || r.body.error || (r.ok ? "ответ уехал" : "ответ не ушёл"), !r.ok);
  return r.ok ? (r.body || {}) : null;
}

// Ждёт ли ответа сама задача, а не разговор: строка стоит с вопросом, и живой
// сессии за ней нет. У цели такого случая нет вовсе, её реплики уходят своей
// ручкой.
function chatWaitsTask(st) {
  if (st.isGoal || !st.task) return false;
  if (!st.wait || !st.wait.state) return false;
  return !st.entry || st.entry.state !== "live";
}

// Вопрос строки словами: состояние, источник, сам вопрос и адрес ответа. Висит
// подсказкой на поле ввода, а не отдельной плашкой: плашка над лентой заводила
// второе поле ответа рядом с обычным.
function waitChatTip(st, w) {
  if (!w || !w.state || !st.task) return "";
  const qs = w.questions || [];
  const kind = chatWay(st).kind;
  let where = " Ответ поднимет сессию задачи и уедет в неё.";
  if (kind === "task") {
    where = " Ответ уйдёт во вход задачи " + st.task +
      " безадресной строкой: по ней сторожок разбудит строку.";
  } else if (kind === "say") {
    where = " Ответ уйдёт живой сессии задачи.";
  }
  return w.state + ", источник: " + (w.note || "не назван") + "." +
    (qs.length ? " Вопрос: " + qs.join("; ") : "") + where;
}

// Куда уйдёт реплика и почему. Мера приходит с сервера состоянием диалога, а
// не считается на глаз: ошибка тут стоит реплики, ушедшей мимо адресата.
function chatWay(st) {
  // Задача стоит с вопросом, а живой сессии за ней нет: обычная реплика
  // уходит безадресной строкой во вход задачи (ручка /tasks/<ID>/message), и
  // по ней сторожок будит строку. Адресованную мёртвой сессии реплику не взял
  // бы никто, а новый чат унёс бы ответ мимо ждущей строки. Раньше на этот
  // случай в панели стояла врезка со своим полем ответа, и над лентой было два
  // поля ввода сразу (POC ветки poc-chat).
  // Снятый и пересозданный разговор: имя его tmux-сессии реестр отдал другому
  // разговору, работу подняли заново. Реплике сюда ехать некуда, и молча
  // увезти её в задачу нельзя: ровно так она и уехала посторонней сессии.
  // Пузырь честно встаёт недоставленным, а выход это живой чат задачи.
  if (st.entry && st.entry.gone) {
    return { kind: "gone", off: false, why: st.entry.gone, to: st.entry.goneTo || "" };
  }
  // Кончившаяся подписка: харнес берёт реплику клавишами так же охотно, как
  // живой, а хода ей не даёт и не даст до сброса окна (материал разбора
  // DK-647, немая смерть от ретрая). Причину и срок называет сервер, панель
  // только повторяет их над лентой, ввод при этом не гасится: реплика всё
  // равно уедет и встанет в очередь харнеса.
  if (st.entry && st.entry.quota) {
    return { kind: "quota", off: false, why: st.entry.quota };
  }
  if (chatWaitsTask(st)) return { kind: "task", off: false, why: "" };
  // Сессия конвейера поднимается: реплика отсюда ушла бы первым аргументом
  // клиента и подняла бы вторую сессию рядом с той, которую только что
  // запустили. Поле заперто на те секунды, пока сессия называет себя в реестре,
  // а плашка о подъёме стоит над ним.
  if (st.lift) {
    return { kind: "lift", off: true,
      hint: "сессия поднимается, ответить можно будет через несколько секунд", why: "" };
  }
  if (st.fresh || !st.sid) return { kind: "new", off: false, why: "" };
  // Протухший адрес: писать некуда, и резюм по несуществующей сессии обещал бы
  // доставку, которой не будет. Причина стоит плашкой, ввод погашен.
  if (st.lost) {
    return { kind: "lost", off: true,
      why: "Чата с этим адресом больше нет: сессия снята или так и не " +
        "назвалась. Выберите диалог в шапке или начните новый кнопкой «+»." };
  }
  const state = st.entry ? st.entry.state : "dead";
  if (state === "live") return { kind: "say", off: false, why: "" };
  // Окна vscode отдельным случаем больше нет: канал самого клиента достаёт
  // любую живую сессию, и «пишите там» осталось бы отказом без причины.
  // Молчание тут честное: реплике есть куда ехать в обоих оставшихся случаях,
  // и объяснять человеку механику доставки на каждом экране незачем.
  return { kind: "resume", off: false, why: "" };
}

// Клин лечится сам. Плашки с кнопкой «Продолжить» тут больше нет вовсе: чат
// должен просто работать, а решение при твёрдом признаке клина одно и очевидно,
// и спрашивать человека незачем (решение пользователя). Твёрдость признака
// считает сервер полем heal, он же помнит перезапуски и не даёт лечить один
// разговор дважды подряд: снятие процесса необратимо.
//
// Лечение это два шага в строгом порядке. Сначала снятие зависшего процесса
// (Escape мёртвому терминалу подать некуда), потом резюм той же сессии.
// Недоставленные реплики к вводной резюма приклеит сервер. Второй шаг идёт
// только после удачи первого: резюм поверх живого зависшего клиента завёл бы
// второго агента на тот же разговор.

// Разговоры, которые лечатся прямо сейчас. Панель пересобирается на каждой
// перерисовке, и без памяти лечение уходило бы вторым заходом поверх первого.
const HEALING = new Set();

async function healWedge(project, st, busy) {
  const sid = st && st.sid;
  if (!sid || HEALING.has(sid)) return;
  HEALING.add(sid);
  try {
    const url = chatsURL(project) + "/" + encodeURIComponent(sid);
    const claim = await api(url + "/heal", { method: "POST", body: {} });
    // Отказ молчит. Сервер уже сказал в ленту всё, что надо было сказать, а
    // повторять это тревогой поверх ленты незачем.
    if (!claim.ok || !claim.body.claim) return;
    // Пока идёт лечение, состояние одно и честное. Прежде рядом с плашкой
    // клина мигало «агент работает...», хотя ход стоял намертво (снимок
    // пользователя).
    busy.heal();
    const kill = await api(url + "/stop", { method: "POST", body: { kill: true } });
    if (!kill.ok) {
      await api(url + "/heal", { method: "POST", body: { done: false } });
      busy.off();
      await repaintChat();
      return;
    }
    const r = await api(url + "/say", { method: "POST", body: { text: CHAT_UNWEDGE } });
    await api(url + "/heal", { method: "POST", body: { done: Boolean(r.ok) } });
    if (r.ok && r.body.way === "resume") chatWait(project, r.body.tmux).catch(console.error);
    busy.off();
    await repaintChat();
  } finally {
    HEALING.delete(sid);
  }
}

// Реплика, которой поднимается разговор после клина. Своих слов человек тут не
// говорил, и говорить за него нельзя: это заказ продолжения, а не его реплика.
const CHAT_UNWEDGE = "Разговор завис, процесс был снят и поднят заново. " +
  "Продолжай с того места, где остановился.";

// ---- Разлогиненный разговор (DK-466) ----
//
// Клиент с истёкшим входом отвечает служебной строкой на любую реплику и не
// делает ни хода. Прежде на экране это выглядело обычным разговором: отказ
// стоял пузырём в ленте, а состояние оставалось живым, и человек читал
// «Login expired» как ответ агента. Так встали два разговора DK-397 два дня
// подряд.
//
// От клина это отличается тем, что перезапуск сам по себе не лечит: пока на
// машине не сделан /login, поднятый заново клиент разлогинен ровно так же.
// Поэтому тут не самолечение, а плашка с порядком действий и кнопка на второй
// шаг. Признак разлогина считает сервер (поле login у разговора и пометка
// logout у ответа в ленте), панель английских слов не разбирает.

// Реплика, которой разговор поднимается после входа. Своих слов человек тут не
// говорил, и говорить за него нельзя: это заказ продолжения.
const CHAT_RELOGIN = "Разговор встал: у клиента истёк вход, процесс снят и " +
  "поднят заново после /login. Продолжай с того места, где остановился.";

// Может ли дашборд перезапустить этот разговор. Чужое окно (vscode) он не
// поднимал, tmux-сессии у такого разговора нет, и снимать ему нечего: кнопка
// там обещала бы работу, которой не будет.
function loginFixable(st) {
  if (!st || !st.sid) return false;
  const e = st.entry;
  if (!e) return true;
  return Boolean(e.tmux) || e.state !== "live";
}

// Разлогин говорит с человеком лентой, а не щитком над полем ввода. Плашку
// пользователь на приёмке отверг прямо: «выглядит просто ужасающе, нужно
// сделать, чтобы весь процесс был в чате». Поэтому тут те же пузыри, что у
// разговора: сообщение о разлогине с двумя кнопками, ответ со ссылкой и полем
// кода, слова исхода. Стоит блок последним в разговоре, под лентой и над полем
// ввода, и лента его не перерисовывает: она живёт своими узлами.
function loginBubble(text) {
  const wrap = el("div", "msg");
  const bb = el("div", "bb");
  const foot = el("div", "mm", "дашборд");
  // Внутри записи входа живут кнопки, ссылка и поле кода. Стоят они в том же
  // пузыре, между словами и подписью: снаружи пузыря они читались бы отдельной
  // плашкой, от которой пользователь и отказался.
  const extras = [];
  let md = mdRender(text || "", chatFeedAt());
  const draw = () => bb.replaceChildren(md, ...extras, foot);
  draw();
  wrap.append(bb);
  // Строка ленты берётся тем же сборщиком, что у записи транскрипта: без неё
  // запись остаётся без левой колонки с нитью и кружком, то есть выглядит
  // гостем в ленте (замечание пользователя на приёмке).
  const row = feedRow(wrap, { role: "assistant" }, null, "");
  return {
    wrap, bb, row,
    // Слова переписываются перерисовкой пузыря: текст записи ленты живёт в
    // узле разметки, и подменять ему содержимое строкой значит терять разбор.
    say: (text) => { md = mdRender(text || "", chatFeedAt()); draw(); },
    bad: (on) => { bb.classList.toggle("loginbad", Boolean(on)); },
    own: (node) => { extras.push(node); draw(); },
  };
}

// Дорога входа зависит от того, где браузер. С самой машины клиент ловит код
// сам, и поле кода не показывается вовсе: шаг один, открыть ссылку. С другого
// устройства возврат вести некуда, и код набирается руками.
function loginTalk(project, st, busy) {
  const box = el("div", "msgs mlocal cbyetalk");
  box.hidden = true;
  const talk = { box, ask: "", lastUser: "" };
  const first = loginBubble("Требуется повторная аутентификация. Войдите " +
    "заново или нажмите «Перезапустить», если уже прошли аутентификацию в " +
    "консоли или другом чате.");
  box.append(first.row);
  if (!loginFixable(st)) {
    first.say("Требуется повторная аутентификация. Разговор поднят не " +
      "дашбордом, снять его отсюда нечем: сделайте /login на машине и " +
      "перезапустите разговор в том окне, где он идёт.");
    return talk;
  }

  // Метка стоит на строке ленты, а не на пузыре: строка и есть запись, её же
  // гасят и её же ищет стенд.
  const step = loginBubble("");
  step.row.classList.add("loginstep");
  step.row.hidden = true;
  const said = loginBubble("");
  said.row.classList.add("loginsaid");
  said.row.hidden = true;

  // Ссылка человеческая: адрес авторизации это четыреста знаков, и портянкой
  // он занимал весь экран. Полный адрес живёт в самой ссылке, а рядом стоит
  // кнопка копирования: вход с другого устройства начинается с того, что
  // адрес переносят руками.
  const linkRow = el("div", "loginrow");
  const link = el("a", "loginurl", "Страница входа Claude");
  link.target = "_blank";
  link.rel = "noopener";
  linkRow.append(link);
  step.own(linkRow);

  const codeRow = el("div", "loginrow");
  codeRow.hidden = true;
  const code = el("input", "logincode");
  code.type = "text";
  code.placeholder = "код авторизации";
  code.autocomplete = "off";
  code.setAttribute("aria-label", "Код авторизации");
  const send = el("button", "btn btn-sm btn-acc", "Подтвердить");
  codeRow.append(code, send);
  step.own(codeRow);
  box.append(step.row, said.row);

  const say = (text, bad) => {
    said.say(text || "");
    said.bad(bad);
    said.row.hidden = !text;
  };

  async function start() {
    say("", false);
    busy.heal();
    const r = await api(chatsURL(project) + "/login", { method: "POST", body: {} });
    busy.off();
    if (!r.ok) {
      say(r.body.error || "вход не поднялся", true);
      return;
    }
    step.say(r.body.way === "local"
      ? "Откройте страницу входа и пройдите аутентификацию. Код дашборд " +
        "возьмёт сам: браузер вернётся прямо в клиент."
      : "Перейдите по ссылке и пройдите процесс аутентификации. Далее " +
        "скопируйте код и введите его в поле ниже.");
    link.href = r.body.url;
    linkRow.replaceChildren(link, copyBtn(r.body.url));
    codeRow.hidden = r.body.way !== "code";
    step.row.hidden = false;
    if (r.body.way === "local") waitLoop().catch(console.error);
  }

  // Вход петлёй ждётся опросом: ручка стоит на поллинге панели и отвечает
  // «ещё идёт», пока человек в браузере.
  async function waitLoop() {
    for (;;) {
      const r = await api(chatsURL(project) + "/login/wait", { method: "POST", body: {} });
      if (r.ok && r.body && r.body.waiting) continue;
      if (!r.ok) {
        say(r.body.error || "вход не прошёл", true);
        return;
      }
      await done();
      return;
    }
  }

  async function sendCode() {
    const text = code.value.trim();
    if (!text) {
      say("Введите код, который показала страница входа.", true);
      return;
    }
    busy.heal();
    const r = await api(chatsURL(project) + "/login/code",
      { method: "POST", body: { code: text } });
    busy.off();
    if (!r.ok) {
      // Отказ называется словами клиента целиком: человеку разбираться с
      // «OAuth error: Request failed with status code 400», а пересказ отнял
      // бы у него разбор.
      say(r.body.error || "код не отправился", true);
      return;
    }
    code.value = "";
    await done();
  }

  // После входа разговор поднимается сам и доделывает то, на чём встал:
  // человеку не за чем нажимать вторую кнопку, он уже сказал, чего хочет.
  // Кнопки на это время запираются и уходят с экрана. Гаснет блок приходом
  // свежего ответа в ленту, а это секунды, и в это окно второе нажатие
  // отправило бы тот же запрос вторым разом (замечание ревью).
  async function done() {
    talk.done = true;
    lock(true);
    step.row.hidden = true;
    say("Вход сделан. Поднимаю разговор и повторяю запрос, на котором он встал.", false);
    await loginRestart(project, st, busy, talk.ask);
  }

  const row = el("div", "loginbtns");
  first.own(row);
  const enter = el("button", "btn btn-sm btn-acc", "Войти");
  const go = el("button", "btn btn-sm", "Перезапустить");
  // Запертые кнопки и уходят с экрана: запертая кнопка на виду обещает работу,
  // которой по ней не будет.
  const lock = (on) => {
    enter.disabled = on;
    go.disabled = on;
    row.hidden = on;
  };
  const free = () => { if (!talk.done) lock(false); };
  // Запертость проверяет сам обработчик, а не только атрибут кнопки: атрибут
  // держит палец, а второе нажатие приходит и мимо него (повтор запроса из
  // очереди событий, чужой скрипт, стенд).
  enter.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (enter.disabled || talk.done) return;
    enter.disabled = true;
    start().finally(() => { if (!talk.done) enter.disabled = false; });
  });
  go.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (go.disabled || talk.done) return;
    lock(true);
    loginRestart(project, st, busy, talk.ask).catch(console.error).finally(free);
  });
  row.append(enter, go);
  send.addEventListener("click", (ev) => {
    ev.stopPropagation();
    send.disabled = true;
    sendCode().finally(() => { send.disabled = false; });
  });
  code.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") send.click();
  });
  return talk;
}

// Ответ агента поднимает и гасит блок: служебная строка про истёкший вход
// (пометка сервера) поднимает, любой настоящий ответ гасит. Реплика человека
// состояния не трогает, но запоминается: это и есть запрос, который отказ
// оборвал, и после входа разговор доделывает именно его.
function loginSaw(talk, item) {
  if (!talk || !item) return;
  if (item.role === "user" && item.text) {
    talk.lastUser = item.text;
    return;
  }
  if (item.role !== "assistant" || !item.text) return;
  talk.box.hidden = !item.logout;
  if (item.logout && talk.lastUser) talk.ask = talk.lastUser;
}

// Та же мера по всей ленте: записи доезжают четырьмя дорогами, и состояние
// считается по последнему ответу в приехавшем, а не по одной записи.
function loginSawAll(talk, list) {
  for (const item of list || []) loginSaw(talk, item);
}

// Перезапуск разлогиненного разговора: снять сессию и поднять её резюмом. Шаги
// те же и в том же порядке, что у самолечения клина, и порядок этот обязателен:
// резюм поверх живого клиента завёл бы второго агента на тот же разговор.
// Сессии, которой уже нет, снятие отвечает удачей («уже закрыта»), и резюм
// после него идёт как обычно. Реплика подъёма это прерванный запрос человека,
// когда он известен: разговор доделывает начатое, а не начинает с чистого
// листа. Своих слов человек в этом случае не говорил заново, и говорить за
// него нельзя, поэтому запасная реплика зовёт продолжить, а не выдумывает
// заказ.
async function loginRestart(project, st, busy, ask) {
  const sid = st && st.sid;
  if (!sid) return;
  const url = chatsURL(st.project || project) + "/" + encodeURIComponent(sid);
  busy.heal();
  const drop = await api(url + "/stop", { method: "POST", body: { drop: true } });
  if (!drop.ok) {
    busy.off();
    // Вход к этому месту принят, не вышел именно перезапуск: разговор живёт
    // в окне, которого у дашборда в реестре нет, и резюм поверх него поднял
    // бы второго агента. Сказать дорогу, а не красный сбой: живой клиент
    // возьмёт новый вход сам, стоит послать реплику ещё раз.
    sayResult("Вход принят, а перезапуститься не вышло: " +
      (drop.body.error || "сессию разговора не удалось снять") +
      ". Пошлите реплику ещё раз: живой разговор подхватит новый вход сам.", true);
    return;
  }
  const text = String(ask || "").trim() || CHAT_RELOGIN;
  const r = await api(url + "/say", { method: "POST", body: { text } });
  busy.off();
  if (!r.ok) {
    sayResult(r.body.error || "разговор не поднялся резюмом", true);
    return;
  }
  if (r.body.way === "resume") chatWait(project, r.body.tmux).catch(console.error);
  sayResult(ask
    ? "разговор перезапущен: прерванный запрос повторён"
    : "разговор перезапущен: продолжение поднято резюмом");
  await repaintChat();
}

// Подъём нового диалога и ожидание его ID. Сессия рождается позже команды, и
// ID приходит из реестра по имени tmux-сессии: дашборд опрашивает список, пока
// он не встанет, и переключается на живой диалог сам.
// chatSayWanted это разговор, заведённый только что нажатием «+». Курсор в поле
// ввода ставится ровно у него: человек, заводящий чат, собирается писать, и
// вторым нажатием в поле он платил за то, что и так сказал (замечание
// пользователя). Открытый прежний разговор фокус не перехватывает: туда
// приходят и читать, а на телефоне выехавшая клавиатура закрыла бы ленту, ради
// которой человек и пришёл.
let chatSayWanted = "";
let chatSayNode = null;

// chatSayFocusFresh ставит курсор в поле ввода свежего разговора. Зовётся после
// того, как панель встала в дерево: фокус на неприткнутом узле браузер молча
// теряет.
function chatSayFocusFresh(st) {
  const id = (st && (st.blank || st.addr)) || "";
  if (!chatSayWanted || chatSayWanted !== id) return;
  chatSayWanted = "";
  if (chatSayNode && !chatSayNode.disabled && chatSayNode.focus) chatSayNode.focus();
}

// chatBlankMake заводит разговор кнопкой «+». Заводится он на сервере, со
// своим ID, и с этой минуты живёт строкой списка: сессию поднимет первая
// реплика, а до неё разговор всё равно есть. Прежде нового чата не
// существовало нигде, кроме адреса вкладки («new», один на всю вкладку), и
// человек не мог ни увидеть его в списке, ни завести рядом второй, ни
// набрать в них разное (жалоба пользователя).
async function chatBlankMake(project, task) {
  const r = await api(chatsURL(project) + "/blank",
    { method: "POST", body: { id: task || "", model: chatModelPref() } });
  if (!r.ok || !r.body.id) {
    sayResult(r.body.error || "новый чат не завёлся", true);
    return "";
  }
  chatSayWanted = r.body.id;
  switchChat(r.body.id);
  return r.body.id;
}

// chatDraftPush держит набранный текст при самой записи. У начатого разговора
// черновик держит вкладка, у незачатого держать его негде: транскрипта нет. Он
// же говорит уборке, что запись человеку нужна, и брошенной её не считают.
function chatDraftPush(project, id, text) {
  if (!id) return;
  api(chatsURL(project) + "/" + encodeURIComponent(id) + "/draft",
    { method: "POST", body: { text } }).catch(console.error);
}

// chatModelKeep пишет модель незачатого разговора в его память: выбор, сделанный
// до первой реплики, обязан пережить перезагрузку вкладки и уехать в подъём.
function chatModelKeep(project, id, model) {
  api(chatsURL(project) + "/" + encodeURIComponent(id) + "/model",
    { method: "POST", body: { model } }).catch(console.error);
}

async function chatRaise(project, st, text, model, onTmux) {
  const body = { text, model };
  if (st.task) body.id = st.task;
  // Подъём из записи пришивается к ней: разговор останется той же строкой
  // списка, на которой он начался, и адрес панели переедет на живую сессию сам.
  if (st.blank) body.chat = st.blank;
  const r = await api(chatsURL(project), { method: "POST", body });
  if (!r.ok) {
    sayResult(r.body.error || "чат не поднялся", true);
    return false;
  }
  // Имя tmux уезжает вызвавшему сразу: он кладёт его в персист реплики, и
  // дорога к родившемуся диалогу переживает закрытие вкладки.
  if (onTmux) onTmux(r.body.tmux);
  return chatWait(project, r.body.tmux, st.addr);
}

// chatLookTmux спрашивает у сервера диалог по имени tmux-сессии: так дашборд
// узнаёт ID сессии, родившейся позже команды подъёма. Тем же ответом приезжает
// исход подъёма: сервер сверяет на этом заходе, жива ли поднятая сессия, и
// мёртвая называется словами с хвостом её терминала (DK-728). Ожидание кончается
// либо родившимся диалогом, либо причиной, а не молчанием до конца опроса.
async function chatLookTmux(project, name) {
  const list = await api(chatsURL(project) + "?tmux=" + encodeURIComponent(name));
  if (!list.ok) return { hit: null, dead: null };
  return { hit: (list.body.chats || [])[0] || null, dead: list.body.dead || null };
}

// Память о смерти подъёма. Сервер помнит её сам и повторит любому заходу, а тут
// она живёт ради панели: слова о смерти встают на место обещания «сессия
// вот-вот назовётся», и ждать больше нечего.
const chatDeadSaid = new Map();

function chatDeadKey(project, addr) {
  return String(project || "") + "/" + String(addr || "");
}

// Возврат говорит, новая ли это смерть: повторный заход опроса приносит ту же,
// и перерисовывать панель на каждый заход незачем.
function chatDeadSet(project, addr, dead) {
  if (!dead || !dead.why) return false;
  const key = chatDeadKey(project, addr);
  const was = chatDeadSaid.get(key);
  if (was && was.tmux === dead.tmux) return false;
  chatDeadSaid.set(key, dead);
  return true;
}

function chatDeadOf(project, addr) {
  return chatDeadSaid.get(chatDeadKey(project, addr)) || null;
}

// Смерть подъёма разом гасит ожидание: память подъёма снимается, панель
// перерисовывается словами о смерти, а тому, кто смотрит на другой экран,
// причина приезжает карточкой.
function chatDeadTell(project, addr, dead) {
  chatLiftDrop(project, addr);
  if (!chatDeadSet(project, addr, dead)) return;
  sayResult(dead.why, true);
  if (chatHere(project, addr)) repaintChatOnly();
}

// chatSewLoop опрашивает реестр, пока человек стоит на адресе addr, и
// пришивает панель к найденному диалогу: память адреса подъёма вычищается,
// панель переезжает на живой sid. Уводить человека с другого экрана нельзя,
// поэтому каждый заход сверяется с адресом панели.
// Панель уже стоит на этом адресе: пересобирать её незачем, а опрос реестра
// завести надо. Своего опроса её сборка не завела: имени сессии тогда ещё не
// было, оно рождается нажатием, которое идёт позже.
function chatSewHere(project, id, name) {
  if (!chatHere(project, id)) return;
  chatSewLoop(project, name, route().chat, 2000, 150).catch(console.error);
}

// Стоит ли панель на этом разговоре. Один и тот же разговор адресуется двумя
// видами хвоста, коротким и с проектом впереди, и сравнение строк считало их
// разными: опрос реестра после смены проекта бросал ждать родившуюся сессию на
// первом же заходе, а панель оставалась перед пустой лентой.
function chatHere(project, addr) {
  const rt = route();
  const here = chatAddrParts(rt.proj, rt.chat || "");
  const want = chatAddrParts(project, addr || "");
  return Boolean(addr) && here.project === want.project && here.addr === want.addr;
}

async function chatSewLoop(project, name, addr, step, tries) {
  for (let i = 0; i < tries; i += 1) {
    await new Promise((ok) => setTimeout(ok, step));
    if (!chatHere(project, addr)) return false;
    const look = await chatLookTmux(project, name);
    if (look.dead) {
      chatDeadTell(project, addr, look.dead);
      return "dead";
    }
    const hit = look.hit;
    if (hit) {
      chatLiftDrop(project, addr);
      echoMove(project, addr, hit.id);
      switchChat(hit.id);
      return true;
    }
  }
  return false;
}

// Ожидание ID поднятой сессии: она рождается позже команды и называет себя в
// реестре сама, первым своим ходом. Дорога одна на оба подъёма, и с кнопки
// нового чата, и с реплики в чат, у которого сессии не было.
//
// Обычно это секунды, но клиент бывает встаёт на вопросе в собственном
// терминале (доверие каталогу, логин), и тогда сессия не родится, пока человек
// не ответит там. Ошибкой такое ожидание не считается и на экран не идёт:
// реплика уже у клиента, аргументом запуска, и чат пришивается сам, как
// только сессия назовётся (прежний текст «ещё не назвала себя в реестре»
// читался провалом и хоронил первую реплику). Возврат: true это найденный
// диалог, "waiting" это ожидание сверх обычного, им пузырь первой реплики
// помечается причиной, но со счетов не снимается, "dead" это смерть поднятой
// сессии, названная сервером: ждать больше нечего, и причина уже в панели.
async function chatWait(project, name, addr) {
  for (let i = 0; i < 40; i += 1) {
    await new Promise((ok) => setTimeout(ok, 1500));
    const look = await chatLookTmux(project, name);
    if (look.dead) {
      chatDeadTell(project, addr || route().chat, look.dead);
      return "dead";
    }
    const hit = look.hit;
    if (hit) {
      // Память адреса подъёма переезжает к найденному диалогу: пузырь первой
      // реплики стоит, пока его не снимет эхо из ленты.
      if (addr) echoMove(project, addr, hit.id);
      switchChat(hit.id);
      return true;
    }
  }
  // Дальше опрос идёт фоном и реже: сессия назовётся, когда человек ответит
  // клиенту, и лента пришьётся без его действий здесь (охраняемый цикл
  // chatSewLoop переключает панель, только пока человек сам стоит на ней).
  chatSewLoop(project, name, addr, 5000, 60).catch(console.error);
  return "waiting";
}

// Причина на пузыре первой реплики, когда подъём идёт дольше обычного: не
// провал, а ожидание, у которого назван виновник и обещан исход.
const CHAT_WAIT_WHY = "сессия поднимается дольше обычного, возможно клиент " +
  "ждёт ответа в своём терминале; чат встанет сам, как только сессия назовётся";

// Индикатор живой работы агента (замечание третьего круга POC). После отправки
// реплики в ленте была тишина до готового ответа, и отправка была неотличима от
// непрошедшей. Индикатор встаёт под лентой сразу по нажатию, называет текущее
// действие словами (думает, имя инструмента) по записям транскрипта и гаснет с
// первым куском ответа либо когда реестр говорит, что сессия снова idle.
function makeBusy(project, box) {
  const row = el("div", "busyrow");
  row.hidden = true;
  const dot = el("span", "dot pulse");
  const what = el("span", "busytext", "агент работает...");
  row.append(dot, what);
  box.append(row);
  let poll = null;
  let stop = 0;
  const off = () => {
    row.hidden = true;
    row.classList.remove("stop");
    if (poll) clearTimeout(poll);
    poll = null;
  };
  // Индикатор не висит вечно ни при какой поломке: через свой срок он гаснет
  // сам. Молчащий агент бывает и от упавшего клиента, и вечная мигалка врала бы
  // о работе, которой нет.
  const LIMIT = 10 * 60 * 1000;
  const tick = async (sid) => {
    if (row.hidden) return;
    if (Date.now() > stop) return off();
    try {
      const r = await api(chatsURL(project) + "/" + encodeURIComponent(sid) + "/status");
      // Занятость сервер считает по транскрипту, а не по полю реестра: у
      // сессий vscode оно пустое всегда. Тишина в журнале в первые секунды
      // после реплики это не «агент закончил», а «агент ещё не начал писать»,
      // и гасить по ней нельзя (замечание 18).
      const young = shown && Date.now() - shown < 6000;
      if (r.ok && r.body.live && !r.body.busy && !young) return off();
    } catch (err) {
      // Обрыв связи не гасит индикатор: работа идёт, видно её просто нечем.
    }
    poll = setTimeout(() => tick(sid), 1500);
  };
  return {
    on(sid) {
      row.hidden = false;
      row.classList.remove("stop");
      what.textContent = "агент работает...";
      stop = Date.now() + LIMIT;
      if (poll) clearTimeout(poll);
      // Первый опрос с задержкой: реестр помечает сессию занятой не мгновенно,
      // и мгновенный опрос застал бы ещё idle и погасил индикатор сразу.
      poll = setTimeout(() => tick(sid), 2500);
    },
    // Подъём сессии нового чата: опрашивать нечего, сессии ещё нет, но и
    // пустота под пузырём читалась как «ничего не происходит» (замечание
    // пользователя по снимку). Плашку гасит пришивание ленты (уход с панели),
    // причина на пузыре (onHeld) либо свой предельный срок. Слова тут про
    // агента, а не про сессию: сессия это наше устройство, человек ждёт того,
    // с кем говорит.
    raise() {
      row.hidden = false;
      row.classList.remove("stop");
      what.textContent = "агент запускается, это несколько секунд";
      stop = Date.now() + LIMIT;
      if (poll) clearTimeout(poll);
      poll = null;
    },
    // Слова тут про дело человека, а не про наше устройство. «Разговор
    // перезапускается» это мы снимаем процесс клиента и поднимаем его резюмом,
    // и человеку в этом нет ничего: «совершенно непонятно, зачем это видеть
    // пользователю и что это значит» (замечание пользователя). Ему нужно
    // знать, кто пропал, надолго ли и не потерялось ли сказанное.
    heal() {
      row.hidden = false;
      row.classList.remove("stop");
      what.textContent = "агент возвращается, это несколько секунд, сказанное не потеряно";
      stop = Date.now() + LIMIT;
      if (poll) clearTimeout(poll);
      poll = null;
    },
    // Разговор без процесса: индикатор не прячется, а сразу встаёт остановкой
    // вместо отдельной строки с кнопкой (DK-701). Молчание тут неотличимо от
    // живого разговора, и человек не знал, поднимется ли чат от реплики
    // (замечание пользователя про разговор из архива). Дот и подпись красятся,
    // опрос не заводится: поднимать нечего, пока не написана реплика.
    stopped() {
      row.hidden = false;
      row.classList.add("stop");
      what.textContent = "разговор остановлен: реплика поднимет его снова";
      stop = 0;
      if (poll) clearTimeout(poll);
      poll = null;
    },
    // Запись транскрипта говорит, чем агент занят прямо сейчас: размышления,
    // вызов инструмента, и наконец сам ответ, на котором индикатор гаснет.
    // Остановку запись не трогает: пока «Стоп» этого состояния не снят явным
    // on(), проезжающая мимо запись статики значения не имеет.
    saw(item) {
      if (row.hidden || row.classList.contains("stop")) return;
      stop = Date.now() + LIMIT;
      if (item.role === "assistant" && item.text) return off();
      if (item.role === "thinking") { what.textContent = "думает..."; return; }
      if (item.role === "tool") {
        what.textContent = (item.tool || "инструмент") + (item.note ? ": " + item.note : "");
        return;
      }
      if (item.role === "toolout") { what.textContent = "читает вывод..."; }
    },
    off,
  };
}

// Свои реплики, ещё не вернувшиеся из транскрипта. Пузырь встаёт в ленту сразу
// по нажатию, как в мессенджерах: ждать, пока клиент запишет реплику в журнал и
// её принесёт поток, значит показывать человеку пустоту в ответ на отправку.
// Дубля от этого нет: пришедшее из потока эхо узнаётся по тексту среди свежих
// реплик человека и заменяет собой местный пузырь (замечание пятого круга POC).
// Живое выделение в блоке постановки. Берётся оно ровно оттуда: выделение в
// ленте, в чужой карточке или в поле ввода контекстом не считается, иначе к
// реплике липло бы всё, что человек когда-то подсветил мышью. Файл называется
// самим блоком (data-file), чтобы агент знал, что именно ему показали.
function grabSelection() {
  const sel = window.getSelection ? window.getSelection() : null;
  if (!sel || sel.isCollapsed) return null;
  const text = String(sel.toString() || "");
  if (!text.trim()) return null;
  const node = sel.anchorNode;
  const host = node && (node.nodeType === 1 ? node : node.parentNode);
  const view = host && host.closest ? host.closest(".fview") : null;
  if (!view) return null;
  return { file: (view.dataset && view.dataset.file) || "постановка", text };
}

// Префикс выделения. Текст едет как есть, вместе с кавычками и переносами:
// править чужой текст по дороге нельзя, агент должен увидеть ровно то, что
// человек выделил. Разделитель это закрывающий тег на своей строке, поэтому
// внутри выделения он не встретится случайно.
// Снимок ужимается перед отправкой. Ретина-экран отдаёт png в несколько
// мегабайт, внешний вход рубит такое тело своим 413, и до дашборда оно не
// доезжает вовсе (жалоба пользователя: локально работало, снаружи нет).
// Длинная сторона сводится к потолку, картинка перекодируется в jpeg, и, пока
// тело длиннее предела, качество с размером понижаются шагами. Мелкую
// картинку (иконка, кусок текста) трогать незачем: png там и меньше, и чётче.
const SHOT_SMALL = 120 * 1024;
const SHOT_BYTES = 900 * 1024;
const SHOT_STEPS = [[1600, 0.85], [1600, 0.7], [1200, 0.6], [900, 0.5]];

// Длина тела: base64 длиннее самих данных на треть, и считать надо байты, а
// не буквы, иначе предел срабатывал бы раньше времени.
function shotBytes(dataURL) {
  const text = String(dataURL || "");
  const cut = text.indexOf(",");
  return Math.floor((cut >= 0 ? text.length - cut - 1 : text.length) * 3 / 4);
}

function loadImage(src) {
  return new Promise((ok, no) => {
    const img = new Image();
    img.onload = () => ok(img);
    img.onerror = () => no(new Error("картинка не разобралась"));
    img.src = src;
  });
}

// Один шаг сжатия: перерисовка в холст нужного размера и вывод в jpeg.
function shotDraw(img, side, quality) {
  const w = img.width || 0;
  const h = img.height || 0;
  if (!w || !h) return "";
  const k = Math.min(1, side / Math.max(w, h));
  const c = document.createElement("canvas");
  c.width = Math.max(1, Math.round(w * k));
  c.height = Math.max(1, Math.round(h * k));
  const ctx = c.getContext && c.getContext("2d");
  if (!ctx) return "";
  // Под jpeg нужен непрозрачный низ: прозрачности он не умеет, и на её месте
  // вышла бы чернота.
  ctx.fillStyle = "#ffffff";
  ctx.fillRect(0, 0, c.width, c.height);
  ctx.drawImage(img, 0, 0, c.width, c.height);
  return c.toDataURL("image/jpeg", quality);
}

// Среда без холста (старый браузер, стенд) картинку не ужимает, а отправляет
// как есть: молча потерять вложение хуже, чем упереться в предел входа.
async function shrinkShot(pic) {
  if (!pic || !pic.data || shotBytes(pic.data) <= SHOT_SMALL) return pic;
  let img = null;
  try {
    img = await loadImage(pic.data);
  } catch (e) {
    return pic;
  }
  let best = "";
  for (const [side, quality] of SHOT_STEPS) {
    const out = shotDraw(img, side, quality);
    if (!out) break;
    best = out;
    if (shotBytes(out) <= SHOT_BYTES) break;
  }
  if (!best) return pic;
  return { data: best, kind: "image/jpeg", name: pic.name || "снимок" };
}

// Префикс картинки: агент читает файл сам, поэтому ему нужен путь, а не байты.
// Форма та же, что у выделения: строка перед репликой, разбирает её сервер.
function shotPrefix(path) {
  return '<screenshot file="' + path + '">\nвставлен снимок экрана\n</screenshot>\n';
}

// Пикер элементов экрана. Замечание про место на экране человек описывает
// словами («та строка с рангом слева»), и исполнитель ищет это место в app.js
// наугад. Пикер даёт ткнуть в само место: в реплику уезжает описатель, по
// которому место находится грепом по говорящим классам.
//
// В реплику едет не живой узел, а снимок, снятый в момент нажатия: экран
// перерисовывается каждые несколько секунд, и ссылка на узел к отправке уже
// протухла бы.
const PICK_TEXT_MAX = 120;

// pickAttrs собирает говорящие атрибуты узла: id, data-поля и ID задачи. У
// строк задач он и различает двадцать одинаковых строк.
function pickAttrs(node) {
  const out = [];
  if (node.id) out.push("id=" + node.id);
  const data = node.dataset || {};
  for (const name of Object.keys(data)) {
    const said = String(data[name] || "");
    if (said) out.push("data-" + name + "=" + foldPeek(said, 40));
  }
  return out;
}

function pickWord(node) {
  const cls = String(node.className || "").trim().split(/\s+/).filter(Boolean);
  return (node.tagName || "div").toLowerCase() + (cls.length ? "." + cls.join(".") : "");
}

// pickOf снимает описатель узла: сам узел, его говорящие атрибуты, обрезанный
// текст, пара уровней родителей и экран из адреса.
function pickOf(node) {
  const chain = [];
  let up = node.parentNode;
  for (let i = 0; i < 2 && up && up.tagName; i += 1) {
    chain.push(pickWord(up));
    up = up.parentNode;
  }
  const text = String(node.textContent || "").replace(/\s+/g, " ").trim();
  return {
    what: pickWord(node),
    attrs: pickAttrs(node),
    text: foldPeek(text, PICK_TEXT_MAX),
    chain,
    screen: String(location.hash || "").replace(/^#/, ""),
  };
}

// pickSay это одна строка описателя, какой её увидит агент.
function pickSay(pick) {
  const bits = [pick.what];
  if (pick.attrs.length) bits.push(pick.attrs.join(" "));
  if (pick.text) bits.push("«" + pick.text + "»");
  if (pick.chain.length) bits.push("внутри " + pick.chain.join(" < "));
  return bits.join(", ");
}

// Блок описателей стоит префиксом реплики, той же дорогой, что и выделение
// текста. Вид блока свой: выделение это чужие слова, а тут места экрана.
function pickPrefix(list) {
  const screen = (list[0] && list[0].screen) || "";
  return '<picked screen="' + screen + '">\n' +
    list.map((p) => "- " + pickSay(p)).join("\n") + "\n</picked>\n";
}

// pickZone отвечает, можно ли ткнуть в этот узел. Панель разговора из зоны
// выбора исключена: там стоят сами фишки и поле ввода, и тыкать в них незачем.
function pickZone(node) {
  if (!node || !node.tagName) return false;
  return !(node.closest && node.closest("#cpanel"));
}

function selPrefix(sel) {
  return '<selection file="' + sel.file + '">\n' + sel.text + "\n</selection>\n";
}

// Очередь исходящих панели: неушедшая реплика не пропадает с перезагрузкой и
// дожимается сама. Пузырь с кнопкой «повторить» остался (человек вправе
// дожать руками), но ждать его нажатия дашборд больше не обязан: телефон
// теряет связь в метро и в лифте, и реплика, набранная там, уходила в никуда,
// стоило закрыть вкладку.
//
// Хранится только неудача: удачно ушедшая реплика живёт журналом разговора на
// сервере (outbox.go), а «отправляется...» в момент закрытия вкладки редкость,
// и восстанавливать её значило бы слать второй раз то, что уже уехало.
const ECHO_KEY = "devkit.chat.pend.";
// Ключ записи очереди исходящих: он и есть имя реплики для сервера. Одна
// запись это один ключ, сколько бы попыток она ни пережила, и по нему сервер
// отбрасывает повтор, ответ на который до панели не доехал (живой случай: пять
// одинаковых копий одной реплики в одной сессии).
let msgSeq = 0;
function msgKey() {
  msgSeq += 1;
  return "m-" + Date.now().toString(36) + "-" + msgSeq + "-" +
    Math.floor(Math.random() * 1e6).toString(36);
}
// Шаг автодожима: первая пауза короткая, дальше удвоение до потолка, чтобы
// сутки без связи не обернулись тысячами запросов. Те же числа, что у очереди
// «Входящих» (OUTBOX_FIRST, OUTBOX_MAX): очередь одна по смыслу.
const ECHO_FIRST = 2000;
const ECHO_MAX = 60000;

function echoRead(project, addr) {
  try {
    const raw = JSON.parse(localStorage.getItem(ECHO_KEY + project + "/" + addr) || "[]");
    if (!Array.isArray(raw)) return [];
    // Восстановленное отдаётся как записано, вместе со временем отправки:
    // судьбу «отправляется» решает не сам факт перерисовки, а прошедший с
    // отправки срок, и считает его makeEcho от born из персиста.
    // tmux это имя сессии, которую подняла эта реплика: по нему панель,
    // вернувшаяся на адрес new, пришивает себя к родившемуся диалогу.
    return raw.filter((m) => m && m.text).map((m) => ({
      text: String(m.text), wire: String(m.wire || m.text), born: m.born || Date.now(),
      state: m.state === "held" || m.state === "wait" ? m.state : "bad",
      why: m.why ? String(m.why) : "",
      tmux: m.tmux ? String(m.tmux) : "",
      // Ключ записи переживает перезагрузку вместе с ней: по нему сервер узнаёт
      // повтор, а вкладка, поднятая заново, шлёт ту же запись, а не новую.
      id: m.id ? String(m.id) : msgKey(),
    }));
  } catch (err) {
    // Приватное окно запрещает хранилище: панель тогда живёт без памяти о
    // неушедшем, но работает.
    return [];
  }
}

// Причина у пузыря, пережившего перерисовку или перезагрузку до подтверждения.
// Сама отправка успела уйти, но эха из транскрипта панель ещё не видела.
// Прежде тут стояли «доставка не подтверждена» и «эхо из транскрипта», то есть
// наше устройство целиком: человеку нужно знать, дошло ли и почему это до сих
// пор непонятно.
const ECHO_LOST_WHY = "дошло ли, неизвестно. Агент этого ещё не повторил";

// Причина у реплики, которая легла во вход задачи и ждёт там. Ведущей сессии у
// задачи нет ни одной, и забрать безадресную строку некому: подхват отдаёт её
// только той сессии, что задачу ведёт. Слова эти сервер говорит и сам, а тут
// они запасные, на случай ответа без причины.
const TASK_NO_LEAD_WHY = "работа по задаче не идёт, отвечать некому, и реплика " +
  "ждёт во входе задачи";

function echoWrite(project, addr, list) {
  try {
    // Переживают выгрузку панели не только неушедшие: held ждёт эха из
    // транскрипта, wait это отправка в полёте, и оба обязаны стоять в ленте
    // после перерисовки, пока эхо их не сняло (первая реплика нового чата
    // пропадала с экрана ровно из-за того, что жил только bad).
    const keep = list.filter((m) => m.state === "bad" || m.state === "held" || m.state === "wait")
      .map((m) => ({ text: m.text, wire: m.wire, born: m.born, state: m.state,
        why: m.why || "", tmux: m.tmux || "", id: m.id || "", to: m.to || "",
        task: m.task || "" }));
    if (keep.length) {
      localStorage.setItem(ECHO_KEY + project + "/" + addr, JSON.stringify(keep));
    } else {
      localStorage.removeItem(ECHO_KEY + project + "/" + addr);
    }
  } catch (err) {
    return;
  }
}

// Пришивание переносит память адреса подъёма к родившемуся диалогу, а не
// стирает её: пузырь первой реплики обязан пережить переезд панели и уйти
// только своим эхом из ленты. Чистка на месте оставляла промежуток без единой
// копии реплики, пока лента нового sid ехала с сервера, и человек видел
// мигание (снимок пользователя). Старый ключ снимается, чтобы пузырь не
// воскресал при следующем нажатии «+».
function echoMove(project, from, to) {
  try {
    const key = ECHO_KEY + project + "/";
    const raw = localStorage.getItem(key + from);
    if (raw && from !== to) {
      let add = [];
      let was = [];
      try { add = JSON.parse(raw); } catch (err) { add = []; }
      try { was = JSON.parse(localStorage.getItem(key + to) || "[]"); } catch (err) { was = []; }
      const list = (Array.isArray(was) ? was : []).concat(Array.isArray(add) ? add : []);
      if (list.length) localStorage.setItem(key + to, JSON.stringify(list));
    }
    localStorage.removeItem(key + from);
  } catch (err) {
    return;
  }
}

function makeEcho(project, box, feedBox, addr, resend) {
  // Ключ у местной реплики свой и сквозной: sync рисует ленту по ключам, и
  // без устойчивого ключа пузырь пересобирался бы на каждой перерисовке.
  let seq = 0;
  const mine = [];
  // Записи ленты, которыми уже снят чей-то пузырь: сверка идёт на каждой
  // перерисовке, и без памяти вторая одинаковая реплика снималась бы копией
  // первой.
  const claimed = new Set();
  const save = () => { if (addr) echoWrite(project, addr, mine); };

  // Заглушка ленты и своя реплика на экране не уживаются: лента пустого чата
  // просит написать первую реплику, и стоять эта просьба поверх уже отправленной
  // реплики не должна. Человек читал такой экран как потерю: «сообщение просто
  // улетает в никуда» (замечание пользователя), хотя пузырь стоял тут же, под
  // просьбой его написать. Снятая заглушка помнится и возвращается, если
  // отправленное отменили и ленты так и нет.
  let saidHole = null;
  const feedSay = () => {
    if (!feedBox) return;
    const kids = feedBox.children ? [...feedBox.children] : [];
    if (mine.length) {
      if (saidHole) return;
      const at = kids.find((k) => String(k.className || "").split(" ").includes("empty"));
      if (at) {
        saidHole = at;
        at.remove();
      }
      return;
    }
    if (saidHole && !kids.length) {
      feedBox.append(saidHole);
      saidHole = null;
    }
  };

  const draw = () => {
    box.replaceChildren();
    feedSay();
    // Свои пузыри рисуются мимо ленты, а ссылки в них те же самые: проект
    // называется тут, потому что рисовать их могут и до первой отрисовки ленты.
    chatFeedIn(project);
    for (const m of mine) {
      // Реплика, которую взяли, но которой не дали хода (агент стоит на
      // вопросе разрешения в своём окне), доставленной не считается: пузырь
      // называет причину, а дожимать её нечем, повтор лёг бы в ту же очередь.
      const meta = m.state === "held" ? "не доставлено: " + (m.why || "агенту её не отдали")
        : m.state === "bad" ? (stopped ? "не ушло" : "не ушло, дожимаю")
        : m.state === "sent" ? "доставлено" : "отправляется...";
      const wrap = chatBubble("вы", m.text, m.sel ? meta + ", с выделением" : meta);
      wrap.classList.add("m-local", "m-" + m.state);
      if (m.sel) wrap.append(selFold(m.sel.file, m.sel.text));
      if (m.pic) wrap.append(shotThumb(m.pic.data, m.pic.name));
      if (m.state === "bad" || m.state === "held") {
        const again = el("button", "linkish", "повторить");
        again.addEventListener("click", () => {
          const text = m.text;
          drop(m);
          if (m.retry) m.retry(text);
        });
        wrap.append(again);
        // Отмена рядом с повтором: реплика уходит из очереди и из персиста,
        // дожимать её больше нечего. Без отмены недоставленное висело вечно,
        // и снять его можно было только повтором (замечание пользователя).
        const undo = el("button", "linkish", "отменить");
        undo.addEventListener("click", () => {
          // Реплика, которая лежит во входе задачи, снимается и оттуда: убрать
          // пузырь с экрана мало, строка в очереди уехала бы агенту первым же
          // ходом, а человек считал бы её отменённой.
          if (m.task) dropTaskLine(project, m.task, m.wire || m.text).catch(console.error);
          drop(m);
          // Пустая очередь нового чата гасит плашку подъёма: отменённая
          // первая реплика возвращает панель в чистое состояние.
          if (!mine.length && out.onGone) out.onGone();
        });
        wrap.append(undo);
        // Выход из кончившегося разговора: живой разговор, занявший его имя.
        // Без него человеку оставался повтор в то же никуда.
        if (m.to) {
          const away = el("button", "linkish", "открыть живой чат");
          away.addEventListener("click", () => {
            const text = m.text;
            drop(m);
            chatDraftWrite(m.to, text);
            switchChat(m.to);
          });
          wrap.append(away);
        }
        // Реплика лежит во входе задачи и ждёт её сессию: выход отсюда это
        // сама задача, где работу и поднимают. Пузырь при этом остаётся, текст
        // человека из него никуда не девается.
        if (m.task) {
          const lift = el("button", "linkish", "поднять работу по задаче");
          lift.title = "Реплика уже лежит во входе задачи " + m.task +
            ": сессия заберёт её первым же ходом.";
          lift.addEventListener("click", () => { goFromChat(project + "/" + m.task); });
          wrap.append(lift);
        }
      }
      // Местный пузырь встаёт в ту же разметку строки ленты, что и запись из
      // транскрипта: без обёртки frow он рисовался без левой колонки с нитью и
      // кружком, и по приходе эха реплика прыгала вправо на ширину этой колонки.
      // Метка gtop та же, что у реплики человека в ленте: без неё нить
      // начиналась с верха контейнера, обрубком в воздухе над точкой
      // (замечание пользователя по снимку нового чата).
      box.append(feedRow(wrap, { role: "user" }, null, "gtop"));
    }
    // Лента доезжает до своей реплики: человек нажал отправку и обязан увидеть,
    // что она встала.
    if (feedBox) feedBox.scrollTop = feedBox.scrollHeight;
  };

  const drop = (m) => {
    const at = mine.indexOf(m);
    if (at >= 0) mine.splice(at, 1);
    save();
    draw();
  };

  // Автодожим: пока в списке есть неушедшее, панель пробует отправку снова по
  // растущей паузе и без неё по событию online. Заход один, повторные вызовы
  // он проглатывает: два дожима подряд слали бы один и тот же текст дважды.
  let timer = null;
  let wait = ECHO_FIRST;
  let stopped = false;
  const pump = () => {
    if (stopped) return;
    const bad = mine.filter((m) => m.state === "bad");
    if (!bad.length) return;
    for (const m of bad) {
      const again = m.retry || resend;
      if (!again) continue;
      // Пока попытка не разрешилась, второй её не заводится: неразрешённая
      // отправка это не отказ, и слать поверх неё значило бы множить копии на
      // ровном месте. Ключ записи едет в повтор, чтобы сервер узнал в нём ту же
      // реплику, а не следующую.
      if (m.flying) continue;
      drop(m);
      again(m.text, m.id);
    }
  };
  const plan = () => {
    if (stopped || timer) return;
    timer = setTimeout(() => { timer = null; pump(); }, wait);
    wait = Math.min(wait * 2, ECHO_MAX);
  };
  // Вернувшаяся сеть это повод не ждать отсчёта: телефон вышел из метро, и
  // реплика уходит сразу.
  const wake = () => {
    if (stopped) return;
    wait = ECHO_FIRST;
    if (timer) {
      clearTimeout(timer);
      timer = null;
    }
    pump();
  };
  if (typeof window !== "undefined" && window.addEventListener) {
    window.addEventListener("online", wake);
  }

  // Судьба восстановленного «отправляется» решается сроком от времени
  // отправки из персиста, а не таймером в памяти той перерисовки, где была
  // отправка: человек уходит с панели и возвращается, а причина обязана
  // появиться те же 60 секунд спустя (замечание пользователя «чат зависает и
  // ничего не происходит»). Молодой пузырь остаётся ждать свой остаток срока.
  const flips = [];
  const ripen = (m) => {
    const left = (m.born || 0) + ECHO_WAIT_MS - Date.now();
    const flip = () => {
      if (!mine.includes(m) || m.state !== "wait") return;
      m.state = "held";
      m.why = chatIsNew(addr) ? CHAT_WAIT_WHY : ECHO_LOST_WHY;
      save();
      draw();
      if (out.onHeld) out.onHeld();
    };
    // Даже созревший пузырь переворачивается отложенно: синхронный вызов
    // прошёл бы до сборки out, на чей хук onHeld опирается сам переворот.
    flips.push(setTimeout(flip, Math.max(0, left)));
  };

  // Неушедшее с прошлого захода: перезагрузка страницы больше не теряет
  // набранное. Выделение и картинка с ним не едут, они живут одним заходом, и
  // восстановленная реплика уходит словами (POC, замечание про телефон).
  if (addr) {
    for (const rec of echoRead(project, addr)) {
      seq += 1;
      mine.push({ key: "local-" + seq, id: rec.id, text: rec.text, wire: rec.wire,
        state: rec.state, why: rec.why, born: rec.born, tmux: rec.tmux, to: rec.to,
        retry: (again) => resend(again, rec.id) });
    }
    if (mine.length) {
      draw();
      // Дожим только неушедшему: held ждёт эха или руки человека, и слать его
      // самим значило бы дублировать реплику, которую клиент уже держит.
      if (mine.some((m) => m.state === "bad")) plan();
      for (const m of mine) {
        if (m.state === "wait") ripen(m);
      }
    }
  }

  const out = {
    // Перерисовка по требованию. Зовёт её сборка панели после того, как
    // напишет ленте её слова: заглушка встаёт последней и накрыла бы уже
    // стоящие пузыри, а вкладка, поднятая заново, застаёт ровно этот порядок.
    redraw() {
      draw();
    },
    // Уход с панели гасит дожим: вернувшийся человек поднимет его заново, а
    // два цикла на одну запись слали бы её дважды.
    stop() {
      stopped = true;
      if (timer) clearTimeout(timer);
      timer = null;
      for (const t of flips) clearTimeout(t);
      flips.length = 0;
      if (typeof window !== "undefined" && window.removeEventListener) {
        window.removeEventListener("online", wake);
      }
    },
    // Пузырь встаёт до похода на сервер: отправка видна мгновенно.
    // wire это то, что ушло агенту (с префиксом выделения), и сверка эха идёт
    // по нему: в транскрипте лежит именно он. text это слова человека, их и
    // видно в пузыре.
    add(text, retry, wire, sel, pic, id) {
      seq += 1;
      // Ключ записи держится за саму запись: повтор той же реплики едет с тем
      // же ключом, иначе сервер видит не повтор, а новую реплику.
      const m = { key: "local-" + seq, id: id || msgKey(), text, wire: wire || text, sel, pic,
        state: "wait", born: Date.now(), retry };
      mine.push(m);
      // Запись сразу: перерисовка панели во время отправки съедала пузырь
      // вместе с текстом, и первая реплика нового чата пропадала с экрана.
      save();
      draw();
      return m;
    },
    // Ручка ответила удачей: реплика ушла, но эха из транскрипта ещё нет.
    // Пометка снимается по сроку, чтобы часики не висели вечно там, где эхо не
    // придёт вовсе (клиент старой версии, чужой формат записи).
    sent(m) {
      if (!mine.includes(m)) return;
      m.state = "sent";
      wait = ECHO_FIRST;
      save();
      draw();
      setTimeout(() => { drop(m); }, ECHO_HOLD);
    },
    bad(m) {
      if (!mine.includes(m)) return;
      m.state = "bad";
      save();
      draw();
      plan();
    },
    // Реплику взяли, а хода ей не дали: причина стоит в пузыре, дожима нет.
    // Автодожим тут только множил бы очередь у вставшего клиента. Панель
    // узнаёт о причине хуком onHeld и гасит свою плашку работы: агент не
    // работает, и мигать о работе поверх причины значило бы врать.
    held(m, why, to, task) {
      if (!mine.includes(m)) return;
      m.state = "held";
      m.why = why;
      // Адрес выхода едет в персист вместе с пузырём: перезагрузка страницы
      // не должна уносить дорогу к живому разговору.
      if (to !== undefined) m.to = to || "";
      // Задача едет туда же: у реплики, которая ждёт во входе, выход это не
      // соседний разговор, а сама задача, откуда поднимают работу.
      if (task !== undefined) m.task = task || "";
      save();
      draw();
      if (out.onHeld) out.onHeld();
    },
    // Есть ли реплики в полёте: по ним панель нового чата держит плашку о
    // подъёме сессии после перерисовки.
    waiting() {
      return mine.some((m) => m.state === "wait");
    },
    // Реплика подняла сессию: имя tmux едет в персист сразу, как его назвал
    // сервер. Без этого закрытая до пришивания вкладка теряла дорогу к
    // родившемуся диалогу, и панель на адресе new молчала вечно.
    mark(m, tmux) {
      if (!mine.includes(m) || !tmux) return;
      m.tmux = tmux;
      save();
    },
    // Имена tmux у реплик, ждущих пришивания: по ним панель нового адреса
    // опрашивает реестр, пока человек стоит на ней.
    raised() {
      const names = [];
      for (const m of mine) {
        if ((m.state === "wait" || m.state === "held") && m.tmux && !names.includes(m.tmux)) {
          names.push(m.tmux);
        }
      }
      return names;
    },
    // Сверка с лентой: пузырь отправки снимается, как только его копия
    // появилась в ленте, откуда бы лента её ни взяла (журнал отправленного,
    // эхо из транскрипта, догон после спящей вкладки, страница истории).
    // Сверяется весь показанный кусок, а не одна свежая запись: копия встаёт
    // в ленту по времени, то есть выше следующих за ней ходов инструмента, а
    // пузырь всегда висит в самом хвосте, и сверка «по последней записи»
    // своей же реплики не узнавала (снимок пользователя: реплика из
    // транскрипта на своём месте, ниже ход Bash, а под ним тот же текст
    // пузырём «вы, доставлено»).
    // Сверка идёт по сырому тексту отправленного, а не по показанному:
    // в ленте текст собран разметкой, и сравнивать с ним отправленное значит
    // сравнивать разное.
    reconcile(list) {
      if (!mine.length || !list || !list.length) return;
      let went = false;
      for (const m of mine.slice()) {
        // Одна запись ленты снимает не больше одного пузыря: две одинаковые
        // реплики подряд это две реплики, а не дубль.
        const hit = list.find((it) => it && it.role === "user" && !it.who && it.text &&
          !claimed.has(itemKey(it)) && (sameSay(it.text, m.text) || sameSay(it.text, m.wire)));
        if (!hit) continue;
        claimed.add(itemKey(hit));
        const at = mine.indexOf(m);
        if (at >= 0) mine.splice(at, 1);
        went = true;
      }
      if (!went) return;
      save();
      draw();
    },
    // Эхо одной записи: тот же разбор, что и у сверки со всей лентой.
    saw(item) {
      out.reconcile([item]);
    },
    // Чистка снимает пузыри с экрана, но не из памяти браузера: неушедшее
    // ждёт там возвращения человека в этот же разговор.
    clear() {
      mine.length = 0;
      draw();
    },
    pump,
  };
  return out;
}

// Сколько местный пузырь держится после удачной отправки, не дождавшись эха.
// Клиент пишет реплику в транскрипт своим ходом, и десяти секунд на это с
// запасом хватает; дальше пузырь снимается, чтобы часики не висели вечно.
const ECHO_HOLD = 10000;

// Срок, после которого «отправляется» без подтверждения получает причину:
// столько же длится быстрая фаза опроса реестра в chatWait (40 заходов по
// полторы секунды), и оба ожидания кончаются одним словом.
const ECHO_WAIT_MS = 60000;

// Тело окна: лента, свои неотражённые реплики, подпись про доставку, индикатор
// работы и поле ввода.
function chatPanel(project, st) {
  const wrap = el("div", "chatwrap");
  const way = chatWay(st);
  const feed = el("div", "msgs chatfeed");
  // Свои реплики, ещё не вернувшиеся из транскрипта, стоят сразу под лентой:
  // они и есть её продолжение, просто эха у них пока нет.
  const pend = el("div", "msgs mlocal");
  wrap.append(feed, pend);
  // Очередь своих реплик поднимает arm() внизу: она же поднимает её заново,
  // когда человек возвращается в этот разговор. Отправка и лента берут echo
  // по ссылке в момент нажатия, поэтому переподнятая очередь доезжает до них
  // сама.
  let echo = null;

  if (way.why) {
    const note = el("div", "cnote" + (way.off ? " idle" : ""));
    note.append(el("span", "", way.why));
    wrap.append(note);
  }
  const busy = makeBusy(project, wrap);
  // Клин лечится сам и молча. Плашки над полем ввода тут больше нет: разговор
  // должен просто работать, а постфактум в ленте остаётся одна спокойная
  // строка, которую пишет сервер (решение пользователя).
  if (st.entry && st.entry.heal) healWedge(project, st, busy).catch(console.error);
  // Разлогин не лечится сам: сперва человеку надо войти на машине, и до этого
  // перезапуск поднял бы такого же разлогиненного клиента. Состояние стоит
  // плашкой над полем ввода, а второй шаг починки лежит на её кнопке.
  const bye = loginTalk(project, st, busy);
  bye.box.hidden = !(st.entry && st.entry.login);
  // Блок стоит там же, где стоят свои реплики. Под лентой, над полем ввода,
  // тем же узлом сообщений и тем же пузырём: это продолжение разговора, а не
  // щиток при нём.
  wrap.append(bye.box);
  // Вопрос клиента кнопками: поднятый в незнакомом каталоге клиент встаёт на
  // вопросе о доверии, а следом на вопросе про внешние импорты правил, и до
  // ответа не делает ни хода. Человек этих вопросов не видел вовсе: лента
  // пустая, реплика висит недоставленной, а ответить можно было только руками
  // в tmux (замечание пользователя: «не хочу каждый раз чинить что-то через
  // тебя»). Блок стоит над полем ввода и обновляется опросом, пока панель
  // открыта.
  // Тем же блоком приходит вопрос агента: заход спросил человека инструментом
  // ожидания и стоит, пока ответа нет. Прежде такой вопрос стоял плашкой без
  // кнопок, а свой собственный не показывался вовсе (DK-652).
  const askBox = el("div", "cask");
  askBox.hidden = true;
  wrap.append(askBox);
  // Разговор без процесса красит общий индикатор остановкой вместо отдельной
  // строки с кнопкой (DK-701). Кнопки «Поднять» тут больше нет: молчание не
  // отличалось от живого разговора, и человек нажимал её не зная, поднимется
  // чат или нет (замечание пользователя про разговор из архива). Первая же
  // реплика поднимает разговор сама, а состояние видно цветом и словами.
  if (way.kind === "resume") busy.stopped();

  const box = el("div", "cbox");
  // Поле ввода тянется за верхний край, а не за нижний: снизу у него кнопка
  // отправки, и родной уголок стоял поверх неё, а расти полю надо вверх, в
  // сторону ленты. Прошлая попытка переворачивала коробку и поле встречными
  // отражениями, и они складывались в тождество, то есть не делали ничего
  // (замечание 3 седьмого круга POC). Родного способа переставить уголок в CSS
  // нет вовсе, поэтому он погашен, а сверху стоит своя полоса хвата.
  const grip = el("div", "tagrip");
  grip.setAttribute("role", "separator");
  grip.setAttribute("aria-label", "Высота поля ввода");
  const ta = el("textarea", "csay");
  // Поле ввода последней собранной панели: по нему ставится курсор у только что
  // заведённого разговора. Панель пересобирается целиком на каждом открытии, и
  // держать узел где-то ещё незачем.
  chatSayNode = ta;
  // Запертое поле называет свою причину одной строкой. Прежде тут стояло «чат
  // идёт в vscode, пишите там»: отдельного случая окон vscode в разборе давно
  // нет, и запертым полем кончается протухший адрес, куда писать некуда вовсе,
  // так что подсказка отправляла человека в другой редактор ни за чем
  // (замечание пользователя).
  ta.placeholder = way.off ? (way.hint || "писать сюда некуда") : "Написать агенту...";
  ta.disabled = Boolean(way.off);
  ta.setAttribute("aria-label", "Реплика в чат");
  // Вопрос строки, его источник и адрес ответа висят подсказкой на самом поле:
  // строки состояния под заголовком больше нет, а знать, куда уедет реплика,
  // человеку надо ровно тут.
  const waitTip = waitChatTip(st, st.wait);
  if (waitTip) {
    ta.title = waitTip;
    ta.setAttribute("aria-label", "Реплика в чат. " + waitTip);
  }
  // Высота поля возвращается вместе с черновиком: растянутое поле переживает
  // перезагрузку ровно пока в нём лежит ненаписанное.
  wireTaGrip(grip, ta, (h) => { chatDraftHeightWrite(st.addr, h); });
  // Черновик возвращается при открытии разговора и пишется по ходу набора.
  // Черновик незачатого разговора приезжает от него самого, когда вкладка про
  // него не знает: разговор ждёт с набранным текстом и в другом браузере.
  ta.value = chatDraftRead(st.addr) ||
    (st.blank && st.entry ? st.entry.draft || "" : "");
  const draftSave = (text) => {
    chatDraftWrite(st.addr, text);
    if (st.blank) chatDraftPush(project, st.blank, text);
  };
  const savedHeight = chatDraftHeight(st.addr);
  if (savedHeight > 0) ta.style.height = savedHeight + "px";
  let draftTimer = null;
  ta.addEventListener("input", () => {
    if (draftTimer) clearTimeout(draftTimer);
    draftTimer = setTimeout(() => { draftSave(ta.value); }, CHAT_DRAFT_WAIT);
  });
  chatLive.push(() => {
    // Уход с разговора дописывает черновик немедленно: отложенная запись до
    // закрытия вкладки могла не успеть.
    if (draftTimer) clearTimeout(draftTimer);
    draftSave(ta.value);
  });
  const row = el("div", "crow");
  // Приложенное к реплике видно до отправки, а не только после: слева от края
  // формы стоят блоки вложений, первым выделение, вторым картинка (замечания
  // 3 и 4). Раньше человек нажимал «отправить» и только в ленте узнавал, уехало
  // ли выделение.
  const clips = el("div", "cclips");
  row.append(clips);
  let pinnedSel = null;
  let shot = chatShotRead(st.addr);
  // Места экрана, выбранные пикером: описатели, снятые в момент нажатия.
  let picks = [];
  const drawClips = () => {
    // Отрисовка блоков заодно и запоминает вложение: меняют его только тут же
    // рядом, и держать запись в трёх местах незачем.
    chatShotWrite(st.addr, shot);
    clips.replaceChildren();
    if (pinnedSel) {
      const chip = el("div", "cclip");
      chip.append(el("b", "", "выделение"));
      chip.append(el("span", "", foldPeek(pinnedSel.text, 40)));
      const off = el("button", "cclipx");
      off.append(icon("close"));
      off.title = "Убрать выделение";
      off.addEventListener("click", () => { pinnedSel = null; drawClips(); });
      chip.append(off);
      clips.append(chip);
    }
    for (const pick of picks) {
      const chip = el("div", "cclip cpickchip");
      chip.append(el("b", "", "место"));
      chip.append(withFull(el("span", "", foldPeek(pickSay(pick), 40)), pickSay(pick)));
      const off = el("button", "cclipx");
      off.append(icon("close"));
      off.title = "Убрать место";
      off.setAttribute("aria-label", off.title);
      off.addEventListener("click", () => {
        picks = picks.filter((p) => p !== pick);
        drawClips();
      });
      chip.append(off);
      clips.append(chip);
    }
    if (shot) {
      const chip = el("div", "cclip");
      const thumb = el("img", "cshot");
      // Не загрузившаяся картинка называет себя словами: значок битого
      // изображения от браузера человеку ничего не объясняет, и ровно на нём
      // спор о «кривой вставке» и стоял (замечание тринадцатого круга POC).
      thumb.addEventListener("error", () => {
        thumb.hidden = true;
        chip.append(el("span", "bad", "картинка не показалась"));
      });
      thumb.src = shot.data;
      chip.append(thumb);
      chip.append(el("span", "", shot.name));
      const off = el("button", "cclipx");
      off.append(icon("close"));
      off.title = "Убрать картинку";
      off.addEventListener("click", () => { shot = null; drawClips(); });
      chip.append(off);
      clips.append(chip);
    }
  };
  // Пикер мест: кнопка включает выбор, наведение подсвечивает элемент рамкой,
  // нажатие кладёт его описатель в набор и гасится (иначе сработало бы само
  // место), Esc и повторное нажатие кнопки выходят.
  const pickBtn = el("button", "cdbtn");
  pickBtn.append(icon("i-pick"));
  let picking = false;
  let lit = null;
  const litOff = () => {
    if (lit && lit.classList) lit.classList.remove("pickhi");
    lit = null;
  };
  const pickOver = (ev) => {
    const node = ev && ev.target;
    if (!pickZone(node)) return;
    litOff();
    lit = node;
    if (node.classList) node.classList.add("pickhi");
  };
  const pickHit = (ev) => {
    const node = ev && ev.target;
    if (!pickZone(node)) return;
    // Нажатие гасится: человек показывает место, а не жмёт то, что под ним.
    if (ev.preventDefault) ev.preventDefault();
    if (ev.stopPropagation) ev.stopPropagation();
    picks = picks.concat([pickOf(node)]);
    drawClips();
  };
  const pickKey = (ev) => {
    if (ev && ev.key === "Escape") pickSet(false);
  };
  function pickSet(on) {
    picking = on;
    pickBtn.className = "cdbtn" + (on ? " on" : "");
    pickBtn.title = on
      ? "Выбор места на экране идёт: нажмите на элемент, Esc выходит"
      : "Показать место на экране: включить выбор";
    pickBtn.setAttribute("aria-label", pickBtn.title);
    if (on) {
      document.addEventListener("mouseover", pickOver);
      document.addEventListener("click", pickHit, true);
      document.addEventListener("keydown", pickKey);
      return;
    }
    litOff();
    document.removeEventListener("mouseover", pickOver);
    document.removeEventListener("click", pickHit, true);
    document.removeEventListener("keydown", pickKey);
  }
  pickSet(false);
  pickBtn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    pickSet(!picking);
  });
  chatLive.push(() => pickSet(false));

  // Выделение подхватывается, пока человек его держит: снял выделение и начал
  // печатать, значит оно уже не при чём.
  const catchSel = () => {
    const live = grabSelection();
    if (live) {
      pinnedSel = live;
      drawClips();
    }
  };
  // Возврат на разговор показывает то, что при нём приложено: черновик уже
  // вернулся в поле, картинка возвращается блоком.
  drawClips();
  // Продолжить работу задачи можно прямо отсюда: сервер сам решит, будить ли
  // живую сессию каналом или поднимать резюм (ручка /continue).
  row.append(pickBtn);
  row.append(modelPick(project, st));
  if (st.task) {
    const go = el("button", "cgo");
    go.title = st.isGoal ? "Продолжить цель " + st.task : "Продолжить работу по " + st.task;
    go.setAttribute("aria-label", go.title);
    go.append(icon("i-play-sm"));
    go.addEventListener("click", () => {
      go.disabled = true;
      continueTask(project, st.task).catch(console.error).finally(() => { go.disabled = false; });
    });
    row.append(go);
  }
  const send = el("button", "btn btn-acc", "Отправить");
  send.disabled = Boolean(way.off);
  // Вложение уезжает на сервер раньше самой реплики: агенту нужен путь, а он
  // рождается только после записи файла. Сессии у чата к этому моменту может и
  // не быть (новый разговор, чужая задача), и тогда файл ложится под свежим
  // ключом: прежде вложение без сессии пропадало молча, картинка не уезжала, и
  // человек узнавал об этом только от агента (жалоба на чат DK-460).
  const shotKey = () => st.sid || "new-" + Date.now();

  const putShot = async (raw0) => {
    if (!raw0) return { path: "", error: "" };
    // Ужимается снимок здесь, а не при вставке: в коробке отправки человек
    // видит то, что вставил, а на сервер едет то, что пролезет во внешний вход.
    const pic = await shrinkShot(raw0);
    // dataURL это «data:<тип>;base64,<данные>»: режется он по первой запятой,
    // в самом base64 запятых нет. Тип берётся из dataURL, а не из типа
    // буфера: буфер иногда называет вид иначе, чем то, что реально пришло.
    const raw = String(pic.data);
    const cut = raw.indexOf(",");
    const kind = (raw.match(/^data:([^;,]+)/) || [])[1] || pic.kind;
    const r = await api(chatsURL(project) + "/" + encodeURIComponent(shotKey()) + "/shot", {
      method: "POST",
      body: { kind, data: cut >= 0 ? raw.slice(cut + 1) : raw },
    });
    if (!r.ok) return { path: "", error: r.body.error || "картинка не легла" };
    if (!r.body.path) return { path: "", error: "сервер не назвал путь вложения" };
    return { path: r.body.path, error: "" };
  };

  const post = (text, sel, pic, key, took) => {
    // Пузырь встаёт в ленту до похода на сервер, как в мессенджерах: ждать
    // ответа ручки, а потом ещё и записи в транскрипт, значит показывать
    // человеку пустоту в ответ на нажатие.
    // Агенту едет реплика с префиксом выделения, а в ленте пузырь показывает
    // слова человека и пометку: простыня выделения в ленте закрыла бы разговор.
    const spots = took && took.length ? pickPrefix(took) : "";
    const wire0 = spots + (sel ? selPrefix(sel) + text : text);
    const m = echo.add(text, (again, id) => post(again, sel, pic, id, took), wire0, sel, pic, key);
    send.disabled = true;
    const done = () => { send.disabled = Boolean(way.off); };
    // Дорога реплики выбрана заранее, а путь вложения приклеивается к ней
    // первой строкой: у всех трёх дорог он один и тот же.
    const go = (wire) => {
      if (way.kind === "gone") {
        // Никуда не отправляется: адресата нет, и обещать доставку нечем.
        // Пузырь остаётся с причиной, повтором, отменой и выходом в живой чат.
        echo.held(m, way.why, way.to);
        done();
        return;
      }
      if (way.kind === "task") {
        // Реплика ждущей задаче: ручка кладёт её безадресной строкой во вход.
        // Ленты у такой строки нет, и пузырь тут единственный след ответа,
        // поэтому панель после удачи не перерисовывается: перерисовка стирала
        // пузырь сразу же, и нажатие выглядело так, будто ничего не случилось.
        answerTask(project, st.task, wire)
          .then((got) => {
            if (!got) {
              echo.bad(m);
              return;
            }
            // Ведущей сессии у задачи нет: строка лежит во входе и ждёт её.
            // Пузырь остаётся недоставленным с причиной и дорогой к задаче,
            // откуда работу поднимают. Прежде тут стояло «доставлено», пузырь
            // снимался через десять секунд, и реплика уходила безадресной в
            // чужой ход (живой случай DK-466).
            if (got.undelivered) echo.held(m, got.why || TASK_NO_LEAD_WHY, "", st.task);
            else echo.sent(m);
          })
          .catch((err) => { echo.bad(m); console.error(err); })
          .finally(done);
        return;
      }
      if (way.kind === "new") {
        // Плашка о подъёме встаёт сразу: между отправкой и пришиванием ленты
        // проходят секунды, и пустота под пузырём читалась как зависание.
        busy.raise();
        chatRaise(project, st, wire, st.entry ? st.entry.model : chatModelPref(),
          (name) => echo.mark(m, name))
          .then((got) => {
            if (got === false) {
              echo.bad(m);
              busy.off();
              return;
            }
            // Долгий подъём не хоронит реплику: пузырь остаётся с причиной и
            // ждёт эха, а фоновый опрос chatWait пришьёт ленту сам. Плашку
            // гасит сама причина, хуком onHeld.
            if (got === "dead") {
              // Реплика уехала клиенту аргументом запуска и умерла вместе с
              // ним: доставленной ей не бывать, и причина стоит на пузыре.
              const dead = chatDeadOf(project, st.addr);
              echo.held(m, (dead && dead.why) || "сессия подъёма умерла");
              busy.off();
            } else if (got === "waiting") echo.held(m, CHAT_WAIT_WHY);
            else echo.sent(m);
          })
          .catch((err) => { echo.bad(m); busy.off(); console.error(err); })
          .finally(done);
        return;
      }
      busy.on(st.sid);
      // Ключ записи едет с каждой попыткой: по нему сервер отличает повтор от
      // следующей реплики и второй раз ту же не доставляет.
      m.flying = true;
      api(chatsURL(project) + "/" + encodeURIComponent(st.sid) + "/say",
        { method: "POST", body: { text: wire, msg_id: m.id } })
        .then((r) => {
          if (!r.ok) {
            // Отказ ручки (сокет не отозвался, разговора нет): пузырь остаётся с
            // пометкой и кнопкой повтора, а не пропадает молча, унося с собой
            // набранное. Удача молчит: реплика и так видна в ленте.
            busy.off();
            echo.bad(m);
            // Отказ с именем клина (клиент не подтвердил доставку) перечитывает
            // панель сразу: сервер уже запомнил молчащий канал, и плашка клина
            // с кнопкой выхода обязана встать здесь, а не при следующем заходе.
            if (r.body.stuck) repaintChat().catch(console.error);
            // Про первый неудачный заход человеку сообщать нечего: реплику
            // дожимает очередь сама, а пузырь уже помечен и держит кнопку
            // повтора. Счёт отказов тут общий с экраном: связь одна на всё, и
            // порог у неё один (LOST_TRIES). Слова приходят, только когда она
            // не вернулась за несколько заходов подряд (замечание пользователя
            // про уведомление о штатной ситуации).
            lostTries += 1;
            if (lostTries >= LOST_TRIES) {
              sayResult(r.body.error || "реплика не ушла", true);
            }
            return;
          }
          // Реплика ушла, значит связь есть: счёт отказов обнуляется тем же
          // местом, что и у экрана.
          lostGone();
          // Слова дороги (DK-480): ответ отпустил запертый вопрос, строка с !
          // уехала текстом мимо терминала. Удача обычной доставки молчит.
          if (r.body.note) sayResult(r.body.note);
          if (r.body.stuck) echo.held(m, r.body.stuck);
          else echo.sent(m);
          // Реплика подняла сессию сама (чата без сессии не бывает после первой
          // же реплики): дальше разговор идёт в ней, и панель переезжает туда.
          if (r.body.way === "start") {
            chatWait(project, r.body.tmux).catch(console.error);
            return;
          }
          // Резюм поднимает новую tmux-сессию, и состояние диалога меняется:
          // список надо перечитать, иначе следующая реплика опять пошла бы
          // резюмом и завела второго агента.
          if (r.body.way === "resume") repaintChat().catch(console.error);
        })
        .catch((err) => {
          busy.off();
          echo.bad(m);
          console.error(err);
        })
        .finally(() => {
          m.flying = false;
          done();
        });
    };
    // Не легло вложение, значит реплика не уходит вовсе: агент ответил бы на
    // половину сказанного, а человек считал бы, что картинку тот видит.
    // Причина сказана словами, пузырь остаётся с пометкой и повтором.
    putShot(pic)
      .then((got) => {
        if (got.error) {
          echo.bad(m);
          sayResult("картинка не ушла: " + got.error, true);
          done();
          return;
        }
        go(got.path ? shotPrefix(got.path) + wire0 : wire0);
      })
      .catch((err) => {
        echo.bad(m);
        sayResult("картинка не ушла: " + err, true);
        done();
        console.error(err);
      });
  };

  // Ответ на вопрос агента уезжает той же дорогой, что реплика из поля: тот же
  // пузырь в ленте, тот же выбор дороги, та же ручка. Второго способа отправки
  // виджет не заводит (DK-652).
  st.askSay = (text) => post(text, null, null, null, null);

  const fire = () => {
    const text = ta.value.trim();
    if (!text || send.disabled) return;
    ta.value = "";
    if (draftTimer) clearTimeout(draftTimer);
    chatDraftWrite(st.addr, "");
    // Отправка уносит и высоту: поле возвращается к своему обычному росту,
    // растянутым его держало ненаписанное.
    chatDraftHeightWrite(st.addr, 0);
    ta.style.height = "";
    // Выделенный в постановке кусок уезжает вместе с репликой: человек
    // выделяет абзац, пишет «поправь этот текст», и агент получает и слова, и
    // сам текст (замечание 3 девятого круга POC).
    post(text, pinnedSel || grabSelection(), shot, null, picks);
    pinnedSel = null;
    shot = null;
    picks = [];
    // Выбор кончился вместе с репликой: места уехали, и держать экран в режиме
    // выбора незачем.
    pickSet(false);
    drawClips();
  };
  // Enter отправляет, перенос строки идёт через Shift+Enter: разговор набирают
  // короткими репликами, и тянуться к кнопке на каждой из них незачем.
  // Вставка картинки из буфера: скриншот кладётся блоком в строку отправки, а
  // агенту уезжает ссылкой на файл, который он прочитает своим Read (замечание
  // 4). Бинарной передачи через канал сессий тут нет и не нужно.
  ta.addEventListener("paste", (ev) => {
    const items = (ev.clipboardData && ev.clipboardData.items) || [];
    for (const it of items) {
      if (!it.type || it.type.indexOf("image/") !== 0) continue;
      const file = it.getAsFile && it.getAsFile();
      if (!file) continue;
      if (ev.preventDefault) ev.preventDefault();
      const reader = new FileReader();
      reader.onload = () => {
        shot = { data: String(reader.result || ""), kind: it.type, name: "снимок" };
        drawClips();
      };
      reader.readAsDataURL(file);
      return;
    }
  });
  // Enter на телефоне это перевод строки, а не отправка: виртуальная
  // клавиатура шлёт его тем же ключом, и недописанная реплика уезжала с
  // полуслова (жалоба пользователя). Устройство различается указателем:
  // грубый указатель это палец. На столе всё как было, а Cmd или Ctrl с
  // Enter шлют всегда, привычной парой.
  ta.addEventListener("keydown", (ev) => {
    if (ev.key !== "Enter" || ev.isComposing) return;
    if (ev.metaKey || ev.ctrlKey) {
      ev.preventDefault();
      fire();
      return;
    }
    if (ev.shiftKey || touchPointer()) return;
    ev.preventDefault();
    fire();
  });
  send.addEventListener("click", fire);
  // Стоп стоит рядом с отправкой и виден только там, где прерывать есть что и
  // чем: сессия работает и живёт в нашей tmux. У окна vscode и у мёртвой
  // сессии клавиатуры отсюда нет, и кнопка там обещала бы несуществующее.
  if (chatStoppable(st)) row.append(chatStopBtn(project, st, busy));
  row.append(send);
  // Порядок узлов и есть положение хвата: полоса стоит первой в коробке, то
  // есть над полем.
  box.append(grip, ta, row);
  wrap.append(box);

  // Живое панели поднимается тут, одной функцией на два случая: первую сборку
  // и возврат в этот же разговор из пула. Уход с разговора гасит его потоки
  // (closeChatLive), и прежде поднять их обратно можно было только пересборкой
  // всей панели: она стоила второй перерисовки, а лента, собранная заново,
  // мигала пустотой между показом и приездом дельты (жалоба пользователя).
  // Разметка при возврате остаётся прежней, поднимаются только потоки, а
  // дельта ленты дописывается по ключам в тот же список.
  //
  // again это возврат: одноразовое (лечение клина, пришивание нового чата,
  // слова пустой ленты) при нём не повторяется, оно уже сделано и стоит на
  // экране.
  const arm = (again) => {
    // Переход по цитате ищет исходную реплику в ленте этого разговора: панель
    // на экране одна, и ссылка на её ленту живёт тут.
    chatFeedBox = feed;
    chatLive.push(() => {
      if (chatFeedBox === feed) chatFeedBox = null;
    });
    // Дожим неушедшего зовёт ту же отправку, что и кнопка. Очередь поднимается
    // заново: остановленная закрытием, она больше не дожимает и не принимает
    // реплик, и вернувшийся в разговор человек писал бы в мёртвую очередь.
    echo = makeEcho(project, pend, feed, st.addr || st.sid,
      (more, id) => post(more, null, null, id));
    chatLive.push(echo.clear, echo.stop);
    // Причина на пузыре гасит плашку работы: агент не работает, и мигать о
    // работе поверх причины значило бы врать.
    echo.onHeld = () => busy.off();
    // Отменённая последняя реплика гасит плашку подъёма: ждать больше нечего,
    // и новый чат возвращается в чистое состояние.
    echo.onGone = () => busy.off();
    chatLive.push(busy.off);
    // Вернувшийся на панель нового чата человек застаёт то же ожидание, что и
    // до ухода: реплика в полёте держит плашку о подъёме сессии, а не пустоту.
    if (!st.dead && (chatIsNew(st.addr) ? (echo.waiting() || st.lift) : (!st.sid && st.lift))) busy.raise();
    // И само ожидание тоже возобновляется: опрос реестра прежней вкладки умер
    // вместе с ней, а реплика в персисте помнит имя tmux своего подъёма. Как
    // только сессия назовётся, панель переедет на живой sid и покажет
    // транскрипт (пришивание, вторая половина chatSewn).
    // Ждёт сессию не только адрес new: чат записи накопителя открывают её же
    // ID, разбор поднимают отдельной кнопкой, и панель для такого адреса
    // собирается один раз (живой случай: человек открыл пустой чат записи,
    // поднял разбор, кружок ожил, а лента осталась пустой до перезагрузки).
    if (!again && (chatIsNew(st.addr) || st.blank || (!st.sid && st.task))) {
      const names = echo.raised();
      // Имя подъёма берётся у реплики, а нет её, так у запуска работы: у
      // конвейера задачи и у разбора записи это память подъёма, а у идущей
      // работы её живая tmux-сессия. Ждут они одного и того же, сессии,
      // которая вот-вот назовётся.
      const name = names.length ? names[names.length - 1] : st.lift;
      if (name && !st.dead) chatSewLoop(project, name, st.addr, 2000, 150).catch(console.error);
    }
    // Клин лечится сам и молча. Плашки над полем ввода тут больше нет: разговор
    // должен просто работать, а постфактум в ленте остаётся одна спокойная
    // строка, которую пишет сервер (решение пользователя).
    if (!again && st.entry && st.entry.heal) healWedge(project, st, busy).catch(console.error);
    // Выделение подхватывается, пока человек его держит.
    document.addEventListener("selectionchange", catchSel);
    chatLive.push(() => document.removeEventListener("selectionchange", catchSel));
    watchClientAsk(project, st, askBox);
    if (st.error) {
      if (!again) say(feed, "error", st.error);
    } else if (st.lost) {
      // Ленту потерянного адреса не открыть, и честнее сказать это словами, чем
      // показать пустоту или ошибку загрузки.
      if (!again) say(feed, "empty", "разговора с этим адресом в проекте нет");
    } else if (!st.sid) {
      if (!again) {
        // Слова пустой ленты пишутся до пузырей и после них снимаются: реплика,
        // пережившая перезагрузку, уже стоит в панели, и просить написать её
        // ещё раз экран не вправе.
        say(feed, "empty", st.dead
          ? st.dead.why + (st.dead.tail ? "\n\nПоследние строки терминала:\n" + st.dead.tail : "")
          : st.lift
          ? "работа поднята, сессия " + st.lift + " вот-вот назовётся в реестре: " +
            "разговор откроется сам, ждать нажатий не надо"
          : st.fresh
          ? "новый чат: напишите первую реплику, она и поднимет сессию"
          // Про пустоту говорит либо заголовок панели, либо лента, но не оба
          // разом: у задачи имя стоит в заголовке, и ленте остаётся одно
          // короткое предложение про то, с чего начать.
          : st.task
          ? "разговоров по задаче ещё не было, напишите первую реплику"
          : (st.note || "чатов тут пока нет, заведите новый кнопкой «+»"));
        echo.redraw();
      }
    } else {
      // keep это возврат: лента поднимается на том же узле, старые записи
      // остаются на местах, а приехавшая дельта дописывается по ключам.
      wireChatFeed(project, feed, st.sid, (item) => {
        busy.saw(item);
        loginSaw(bye, item);
      }, (list) => {
        echo.reconcile(list);
        loginSawAll(bye, list);
      }, again).catch(console.error);
    }
  };
  arm(false);
  // Панель отдаёт свой подъём наружу: слот пула запомнит его и позовёт, когда
  // человек вернётся в этот разговор.
  chatArm = arm;
  return wrap;
}

// Как часто панель переспрашивает клиента о вопросе, на котором он стоит.
// Вопрос приходит не мгновенно: клиент рисует его через секунду-другую после
// подъёма, и без опроса человек снова остался бы перед пустой лентой.
const ASK_POLL = 3000;

// Через сколько панель перечитывает снимок после своего же хода. Клиент рисует
// новый экран не мгновенно, а ждать целый шаг опроса (ASK_POLL) значит держать
// человека перед прежним видом блока.
const ASK_MOVE = 400;

// Опрос вопроса клиента: пока панель открыта и вопрос не отвечен, она
// переспрашивает сервер. Снимок панели tmux стоит подпроцесса, поэтому ходит
// опрос только у разговора с живой tmux-сессией дашборда: у чужого окна
// спрашивать нечего и нечем.
function watchClientAsk(project, st, box) {
  const sid = st.sid;
  // Живой tmux тут больше не условие. Вопрос агента лежит признаком ожидания, и
  // разговору с чужим окном он приходит наравне с нашим: спрашивал заход, а не
  // клиент. Дорогой разбор панели остался на сервере и заводится только там,
  // где tmux-сессия и правда есть (DK-652).
  if (!sid) return;
  let stop = false;
  chatLive.push(() => { stop = true; });
  const tick = async () => {
    if (stop) return;
    const r = await api(chatsURL(st.project || project) + "/" + encodeURIComponent(sid) + "/ask");
    if (stop) return;
    const ask = (r.ok && r.body.ask) || null;
    paintClientAsk(project, st, box, ask, tick);
    // Опрос идёт и при открытом вопросе: виджет меняется не только от наших
    // нажатий (человек вправе ответить и руками в tmux), а перерисовка стоит
    // на подписи снимка, поэтому лишних сборок блока это не даёт.
    setTimeout(() => { tick().catch(console.error); }, ASK_POLL);
  };
  tick().catch(console.error);
}

// Блок вопроса: сам вопрос словами и кнопка на каждый вариант. Ответ уезжает
// клиенту клавишами, а дашборд ничего за человека не подтверждает и в конфиг
// подписки не пишет: решение остаётся его, меняется только место, где он его
// принимает.
// Слова служебных пунктов виджета. Клиент печатает их по-английски, панель
// говорит по-русски, и слова тут свои, а не пересказ: «Next» это переход к
// следующему шагу опроса, «Submit» конец опроса, «Chat about this» выход из
// опроса в обычный разговор (замечание пользователя про английские кнопки).
const ASK_WORD = {
  next: "Дальше",
  submit: "Готово",
  free: "Ответить своими словами",
  chat: "Обсудить в чате",
};

// Блок вопроса: шаги табами, варианты списком, свободный ответ своей строкой.
// Ответ уезжает клиенту клавишами, а дашборд ничего за человека не
// подтверждает и в конфиг подписки не пишет: решение остаётся его, меняется
// только место, где он его принимает.
//
// Перерисовывается тут один этот блок, а не лента: ответ на шаг опроса не
// повод собирать разговор заново (замечание пользователя).
// Каркас соседнего шага: пока снимок панели не приехал, под переехавшим
// подчёркиванием стоит имя шага и слова о том, что он открывается. Данных
// соседнего шага у панели нет вовсе (виджет отдаёт только открытый), и рисовать
// вместо них прежние варианты значило бы показывать чужой ответ как свой.
function askStepShell(box, name) {
  const keep = [];
  for (const kid of box.children || []) {
    const cls = String(kid.className || "");
    if (cls.includes("caskh") || cls.includes("caskst")) keep.push(kid);
  }
  const shell = el("div", "caskwait");
  shell.append(el("b", "", name || "Шаг"));
  shell.append(el("span", "", "открывается..."));
  box.replaceChildren(...keep, shell);
  // Подпись снимка снимается: следующий ответ ручки обязан собрать блок
  // заново, даже если сам снимок с прошлого раза не изменился.
  box.dataset.ask = "";
}

// Строка варианта: отметка слева, слова рядом, пояснение второй строкой
// мельче. Рисуется она одинаково у вопроса клиента и у вопроса агента, и
// разница между ними только в том, куда уходит нажатие.
function askOptLine(opt, pick) {
  const line = el("button", "caskopt" + (opt.mark === "on" ? " on" : ""));
  line.type = "button";
  // Отмеченный вариант виден отметкой, а не словом «отмечено» в тексте.
  if (opt.mark) line.append(el("span", "caskbox"));
  const words = el("span", "caskwords");
  words.append(el("span", "casklabel", opt.text));
  if (opt.desc) words.append(el("span", "caskwhy", opt.desc));
  line.append(words);
  line.setAttribute("aria-pressed", opt.mark === "on" ? "true" : "false");
  line.addEventListener("click", (ev) => {
    ev.stopPropagation();
    pick();
  });
  return line;
}

// Свободный ответ: пункт списка, а поле под списком и только после выбора.
// Прежде поле стояло в ряду вариантов и просилось быть заполненным всегда
// (замечание пользователя).
function askFreeField(list, box, hint, say) {
  const pick = el("button", "caskopt caskown");
  pick.type = "button";
  pick.append(el("span", "caskwords", ASK_WORD.free));
  const free = el("div", "caskfree");
  free.hidden = true;
  const field = el("input", "dwhyin");
  field.type = "text";
  field.placeholder = "Свой ответ";
  const go = el("button", "btn btn-sm btn-acc", "Отправить");
  const fire = () => {
    const said = String(field.value || "").trim();
    if (!said) {
      sayResult(hint, true);
      return;
    }
    say(said);
  };
  go.addEventListener("click", (ev) => { ev.stopPropagation(); fire(); });
  field.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      ev.preventDefault();
      fire();
    }
  });
  free.append(field, go);
  pick.addEventListener("click", (ev) => {
    ev.stopPropagation();
    free.hidden = !free.hidden;
    pick.classList.toggle("on", !free.hidden);
    if (!free.hidden && field.focus) field.focus();
  });
  list.append(pick);
  box.append(free);
}

// Блок вопроса помощника пароля askpass (DK-772): sudo или ssh без терминала
// зовут дашборд вместо своего диалога, и закрытое поле тут же в панели
// заменяет терминальный ввод. Отвечается вопрос своей дорогой,
// POST .../askpass, а не общей ask: там текст ответа разбирается как вариант
// или свободный ответ клиента, и пароль с ними мешать нельзя. Значение поля
// никуда, кроме тела этого запроса, не уходит: ни в ленту, ни в
// localStorage.
function paintAskpassAsk(project, st, box, ask) {
  const head = el("div", "caskh");
  head.append(el("b", "", "Пароль просит sudo"));
  const left = waitLeft(ask.until, Date.now());
  if (left) head.append(el("span", "n", left === "срок вышел" ? left : "осталось " + left));
  box.replaceChildren(head);
  box.append(el("div", "caskhint", ask.text || "sudo просит пароль"));

  const row = el("div", "caskfree");
  const field = el("input", "dwhyin");
  field.type = "password";
  field.autocomplete = "off";
  field.placeholder = "Пароль";
  const send = el("button", "btn btn-sm btn-acc", "Отправить");
  const cancel = el("button", "btn btn-sm", "Отменить");
  let busy = false;
  const post = async (body) => {
    if (busy) return;
    busy = true;
    box.classList.add("busy");
    try {
      const r = await api(chatsURL(st.project || project) + "/" +
        encodeURIComponent(st.sid) + "/askpass", { method: "POST", body });
      if (!r.ok) sayResult(r.body.error || "ответ не ушёл", true);
      field.value = "";
    } finally {
      busy = false;
      box.classList.remove("busy");
    }
  };
  send.addEventListener("click", (ev) => {
    ev.stopPropagation();
    const typed = field.value;
    if (!typed) {
      sayResult("пароль пуст: печатать нечего", true);
      return;
    }
    post({ id: ask.id, text: typed });
  });
  cancel.addEventListener("click", (ev) => {
    ev.stopPropagation();
    post({ id: ask.id, cancel: true });
  });
  field.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter") {
      ev.preventDefault();
      send.click();
    }
  });
  row.append(field, send, cancel);
  box.append(row);
}

function paintClientAsk(project, st, box, ask, again) {
  // Блок пересобирается только тогда, когда снимок и правда сменился: опрос
  // ходит по кругу, и сборка на каждый заход стирала бы набранное в поле
  // своего ответа и мигала бы на ровном месте.
  const sign = ask ? JSON.stringify(ask) : "";
  if (box.dataset.ask === sign) return;
  box.dataset.ask = sign;
  // Занятость прошлого вопроса снимается вместе с его снимком: класс вешает
  // отправка ответа, а снять его некому, и следующий вопрос того же захода
  // стоял приглушённым и без нажатий до перезагрузки страницы (живой случай
  // в чате груминга).
  box.classList.remove("busy");
  // Вопрос помощника askpass идёт до общей проверки на варианты: у него их
  // нет вовсе, поле пароля не вариант, и раньше этой строки его смахнул бы
  // guard пустых options ниже.
  if (ask && ask.kind === "askpass") {
    box.hidden = false;
    paintAskpassAsk(project, st, box, ask);
    return;
  }
  if (!ask || !(ask.options || []).length) {
    box.hidden = true;
    box.replaceChildren();
    return;
  }
  box.hidden = false;
  // Вопрос агента рисуется тем же виджетом, а отвечается по-своему: не
  // клавишами в чужое окно, а репликой во вход разговора, откуда её берёт
  // ждущий заход.
  if (ask.kind === "agent") {
    // Сюда заходят только со сменившимся снимком, и отмеченное на узле
    // относилось к прежнему вопросу. Открытый шаг тоже свой у каждой пачки.
    box.askStep = 0;
    box.askSaid = null;
    paintAgentAsk(st, box, ask);
    return;
  }
  const review = ask.kind === "review";
  const head = el("div", "caskh");
  head.append(el("b", "", review ? "Ответы опроса" : "Клиент ждёт ответа"));
  box.replaceChildren(head);
  // Заголовок по-русски, а вопрос с вариантами идут словами клиента, и на этом
  // стыке легко принять чужой текст за наш. Строка под заголовком называет, кто
  // спрашивает, и говорит главное: пока ответа нет, сессия стоит.
  if (!review) {
    box.append(el("div", "caskhint",
      "Спрашивает сам клиент, своими словами. Пока ответа нет, он не делает ни хода."));
  }

  // Отправка чего угодно в клиент: пока она идёт, блок не гаснет и не
  // подменяется словами «ждём клиента», а только перестаёт слушать нажатия.
  // Прежде блок стирался на каждый ответ, и это читалось перезагрузкой.
  let busy = false;
  const send = async (order) => {
    if (busy) return false;
    busy = true;
    box.classList.add("busy");
    try {
      const r = await api(chatsURL(st.project || project) + "/" +
        encodeURIComponent(st.sid) + "/ask", { method: "POST", body: order });
      if (!r.ok) sayResult(r.body.error || "ответ не ушёл", true);
      else if (r.body.message) sayResult(r.body.message, false);
      return r.ok;
    } finally {
      busy = false;
      box.classList.remove("busy");
    }
  };
  // После всякого хода панель перечитывает снимок и рисует блок заново по
  // месту: виджет мог переключить таб, отметить флажок или показать сводку.
  const afterMove = (ok) => {
    if (ok) setTimeout(() => { again().catch(console.error); }, ASK_MOVE);
  };

  // Шаги опроса это табы, и ходить по ним человек вправе свободно: ответ на
  // текущий шаг для перехода не нужен, ответы копятся у клиента (так устроен
  // сам виджет, проверено на живой панели).
  // Полоса шагов это та же полоса табов, что у доски (задачи, сессии,
  // черновики): подчёркивание открытого, тот же размер и отступы. Кнопки в
  // рамках читались набором действий, а шаг опроса это место, где человек
  // сейчас стоит (решение пользователя).
  //
  // Переход отмечается в тот же ход: нажатие шлёт клавиши в клиент и ждёт
  // нового снимка панели, а это полсекунды, и всё это время подчёркнут был
  // прежний таб. Теперь подчёркивание переезжает сразу, а под ним стоит
  // каркас соседнего шага, пока снимок не приехал; приехавший снимок рисует
  // блок начисто и всё расставляет по правде.
  const steps = ask.steps || [];
  if (steps.length) {
    const bar = el("div", "ktabs caskst");
    const tabs = [];
    steps.forEach((step, i) => {
      const tab = el("button", "ktab" + (step.now ? " onktab" : ""), step.name);
      tab.type = "button";
      if (step.done) tab.append(el("span", "n", "ответ есть"));
      withTip(tab, step.now ? "Этот шаг открыт"
        : (step.done ? "Шаг отвечен: можно вернуться и поменять ответ" : "Перейти к этому шагу"));
      tab.addEventListener("click", (ev) => {
        ev.stopPropagation();
        if (tab.classList.contains("onktab")) return;
        for (const own of tabs) own.classList.toggle("onktab", own === tab);
        askStepShell(box, step.name);
        send({ step: i + 1 }).then(afterMove).catch(console.error);
      });
      tabs.push(tab);
      bar.append(tab);
    });
    box.append(bar);
  }

  // Сводка ответов: последний ответ и есть отправка, и второго опроса тут нет.
  // Дашборд проходит сводку сам, когда отвечено всё; сюда она доезжает только
  // с предупреждением клиента, и тогда решает человек.
  if (review) {
    if (ask.warn) box.append(el("div", "caskwarn", ask.warn));
    const list = el("div", "casklist");
    for (const said of ask.said || []) {
      const row = el("div", "caskdone");
      row.append(el("b", "", said.q || ""));
      row.append(el("span", "caskwhy", said.a || ""));
      list.append(row);
    }
    box.append(list);
    const row = el("div", "caskr");
    const at = (ask.options || []).findIndex((o) => o.kind === "submit");
    if (at >= 0) {
      const go = el("button", "btn btn-sm btn-acc", "Отправить ответы");
      go.addEventListener("click", (ev) => {
        ev.stopPropagation();
        send({ option: at + 1 }).then(afterMove).catch(console.error);
      });
      row.append(go);
    }
    box.append(row);
    return;
  }

  box.append(el("div", "casks", ask.text || ""));
  // Варианты идут списком, каждый своей строкой: отметка слева, слова рядом, а
  // пояснение клиента второй строкой мельче. Прежде они стояли рядом кнопками
  // вперемешку со свободным ответом, и читать их было нечем.
  const list = el("div", "casklist");
  const row = el("div", "caskr");
  let freeAt = -1;
  (ask.options || []).forEach((opt, i) => {
    if (opt.kind === "free") {
      freeAt = i;
      return;
    }
    // Кнопки самого виджета стоят под списком, а не в нём: они не варианты.
    if (opt.kind === "next" || opt.kind === "submit" || opt.kind === "chat") {
      const btn = el("button", "btn btn-sm" + (opt.kind === "chat" ? "" : " btn-acc"),
        ASK_WORD[opt.kind] || opt.text);
      withTip(btn, opt.kind === "chat"
        ? "Выйти из опроса и обсудить его в разговоре"
        : "Кнопка самого опроса: она отправляет отмеченное");
      btn.addEventListener("click", (ev) => {
        ev.stopPropagation();
        send({ option: i + 1 }).then(afterMove).catch(console.error);
      });
      row.append(btn);
      return;
    }
    list.append(askOptLine(opt, () => {
      send({ option: i + 1 }).then(afterMove).catch(console.error);
    }));
  });
  box.append(list);

  if (freeAt >= 0) {
    askFreeField(list, box, "свой ответ пустой: напишите словами, что передать клиенту",
      (said) => { send({ option: freeAt + 1, text: said }).then(afterMove).catch(console.error); });
  }
  box.append(row);
}

// Вопрос агента над полем ввода. Заход спросил человека штатным
// AskUserQuestion, хук перехватил вызов и кончил ход рубежом: вопрос обязан
// быть виден сразу, без наведения мыши и без терминала сессии (живой случай
// DK-650, где восемь минут ожидания кончились парковкой, и цель DK-713,
// которая простояла четыре часа, потому что панель диалог не показала
// вовсе). Прежде свой вопрос доезжал одной подсказкой на поле ввода, а
// варианты терялись по дороге вовсе.
//
// Отвечается он не как вопрос клиента: реплика уходит той же дорогой, что
// написанная руками, ручкой разговора, и ложится во вход, откуда её забирает
// ждущий заход. Умерший заход ответа не теряет: строка остаётся во входе, и её
// берёт тот, кто задачу продолжит.
function paintAgentAsk(st, box, ask) {
  const steps = ask.steps || [];
  const many = steps.length > 1;
  // Отмеченное человеком живёт на самом узле: опрос перерисовывает блок только
  // тогда, когда вопрос сменился, и до отправки отметки обязаны стоять.
  if (!box.askSaid) box.askSaid = {};
  const said = box.askSaid;
  const at = many ? Math.min(box.askStep || 0, steps.length - 1) : 0;
  const open = many ? steps[at] : ask;
  box.replaceChildren();
  const head = el("div", "caskh");
  head.append(el("b", "", ask.task ? "Вопрос от задачи " + ask.task : "Вопрос агента"));
  const left = waitLeft(ask.until, Date.now());
  if (left) head.append(el("span", "n", left === "срок вышел" ? left : "осталось " + left));
  box.append(head);
  box.append(el("div", "caskhint", (many
    ? "Спрашивает агент задачи, вопросов несколько. Пройдите шаги и отправьте ответ одной репликой."
    : "Спрашивает агент задачи. Пока ответа нет, его заход стоит.") +
    ((ask.rest || []).length ? " Следом ждут ответа " + ask.rest.join(", ") + "." : "")));

  // Ответ уезжает одной репликой: ручка ответа сама снимает признак ожидания
  // на сервере, и второй ответ уже некому забрать.
  let sent = false;
  const answer = (text) => {
    if (sent || !text) return;
    sent = true;
    box.classList.add("busy");
    if (st.askSay) st.askSay(text);
    else sayResult("ответить некуда: разговор не собран", true);
  };

  if (many) {
    const bar = el("div", "ktabs caskst");
    steps.forEach((step, i) => {
      const tab = el("button", "ktab" + (i === at ? " onktab" : ""), step.name);
      tab.type = "button";
      if (said[i]) tab.append(el("span", "n", "ответ есть"));
      withTip(tab, i === at ? "Этот шаг открыт"
        : (said[i] ? "Шаг отвечен: можно вернуться и поменять ответ" : "Перейти к этому шагу"));
      tab.addEventListener("click", (ev) => {
        ev.stopPropagation();
        // Переход тут ничего никуда не шлёт: вопросы приехали все разом, и
        // ждать снимка соседнего шага не от кого.
        box.askStep = i;
        paintAgentAsk(st, box, ask);
      });
      bar.append(tab);
    });
    box.append(bar);
  }
  box.append(el("div", "casks", open.text || ""));

  const list = el("div", "casklist");
  for (const opt of open.options || []) {
    if (opt.kind === "free") continue;
    const mark = many ? { text: opt.text, desc: opt.desc, mark: said[at] === opt.text ? "on" : "off" } : opt;
    list.append(askOptLine(mark, () => {
      if (!many) {
        answer(opt.text);
        return;
      }
      said[at] = said[at] === opt.text ? "" : opt.text;
      paintAgentAsk(st, box, ask);
    }));
  }
  box.append(list);
  askFreeField(list, box, "свой ответ пустой: напишите словами, что передать агенту", (text) => {
    if (!many) {
      answer(text);
      return;
    }
    said[at] = text;
    paintAgentAsk(st, box, ask);
  });

  if (many) {
    const row = el("div", "caskr");
    const go = el("button", "btn btn-sm btn-acc", "Отправить ответ");
    withTip(go, "Ответы всех шагов уедут одной репликой");
    go.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const lines = steps
        .map((step, i) => (said[i] ? step.text + ": " + said[i] : ""))
        .filter(Boolean);
      if (!lines.length) {
        sayResult("отвечать нечем: отметьте вариант хотя бы на одном шаге", true);
        return;
      }
      answer(lines.join("\n"));
    });
    row.append(go);
    box.append(row);
  }
}

// Какой разговор сейчас стоит в панели: «проект|адрес».
let chatOpen = "";

let chatFill = null;

function repaintChat() {
  chatOpen = "";
  return refresh();
}

async function paintChat(project, addr, board, works) {
  // Адрес мог приехать с проектом внутри: так его собирает раздел «Агенты»,
  // у которого своего проекта нет. Доска тогда чужая, и брать её из кэша
  // экрана нельзя.
  if (addr && String(addr).includes(CHAT_PROJ_SEP)) {
    const parts = chatAddrParts(project, addr);
    if (parts.project !== project) board = null;
    project = parts.project;
    addr = parts.addr;
  }
  // Полоска возврата живёт тем же обходом, что и панель: она и есть её след
  // на экране, пока экран отдан переходу.
  paintChatBack();
  const panel = document.getElementById("cpanel");
  const pin = document.getElementById("cpin");
  const side = document.getElementById("clist");
  if (side) {
    // Колонки со списком разговоров больше нет: список это выпадающий список в
    // шапке окна, а узел разметки остаётся пустым, чтобы не трогать её ради POC.
    side.hidden = true;
    side.replaceChildren();
  }
  if (!panel || !pin) return;
  // Разговор общий по машине, и по sid он один и тот же, с какого проекта на
  // доске на него ни смотри: смена проекта в шапке меняет доску под панелью, а
  // саму панель не трогает вовсе. Пока проект стоял в ключе, тот же чат
  // считался другим, панель пересобиралась и содержимое пропадало (регресс
  // общего списка разговоров). Проект остаётся в ключе только у адресов, без
  // него ничего не значащих: новый чат, чат задачи и общий чат доски заводятся
  // на доске проекта.
  const ownAddr = addr && !chatIsNew(addr) && !chatIsTask(addr) && addr !== CHAT_BOARD;
  const key = (ownAddr ? "sid" : project) + "|" + (addr || "");
  if (!addr || !project) {
    if (chatOpen) {
      closeChatLive();
      chatDropShut();
      chatOpen = "";
      chatSlotShow("");
    }
    // Закрытая панель никого не заглушает: молчат уведомления только того
    // разговора, который человек видит.
    chatShown = { project: "", sid: "", task: "" };
    panel.hidden = true;
    return;
  }
  putChatWidth(chatWidth());
  panel.hidden = false;
  if (chatOpen === key) return;
  closeChatLive();
  chatDropShut();
  chatFill = null;
  chatOpen = key;
  const gen = chatGen;
  // Плашка ожидания только у пустой панели: пришивание нового чата и
  // переключение диалога держат прежнее содержимое до готового нового, иначе
  // между ними мигал пустой «чат открывается...» (снимок пользователя).
  // Отклик при этом виден в тот же ход (его сторожит замер poc_bench_chat):
  // над прежним разговором встаёт строка перехода, а не пустота вместо него.
  // Возврат в уже открытый разговор виден сразу: готовый узел показывается
  // тем же ходом, без похода в сеть за состоянием. Свежесть догоняет ниже,
  // обычной сборкой, и меняет начинку того же слота.
  const kept = chatPool.get(key);
  if (kept) {
    // Возврат в открытый разговор окончателен: готовый узел показывается тем
    // же ходом, и пересборки за ним не идёт. Заново поднимаются только живые
    // потоки панели, а дельта ленты дописывается в тот же список по ключам.
    // Прежде следом ехала полная пересборка, и панель мигала дважды на одно
    // нажатие (жалоба пользователя).
    chatSlotShow(key);
    panel.classList.remove("cload");
    if (kept.arm) {
      chatShown = Object.assign({ project: "", sid: "", task: "" }, kept.shown || {});
      kept.arm(true);
      // Кольцо поднимается тем же возвратом: его опрос умер вместе с chatLive
      // уходящего разговора, и заново его никто не поднимал.
      if (kept.ringArm) kept.ringArm();
      return;
    }
  } else if (!chatPool.size) {
    // Плашка ожидания только у пустой панели: разговор правда поднимается
    // впервые, и показать вместо него нечего.
    pin.replaceChildren(el("div", "empty", "чат открывается..."));
  } else {
    // Переход в ещё не открытый разговор: прежнее содержимое стоит до готового
    // нового, а о ходе говорит полоска над панелью. Словами тут больше не
    // говорят: надпись об открытии мелькала поверх живого разговора и врала
    // там, где переход шёл тем же ходом (жалоба пользователя).
    panel.classList.add("cload");
  }
  const rows = board || await chatBoardOf(project);
  const st = await chatState(project, addr, rows, works);
  if (gen !== chatGen) return;
  // Ручки разговора живут при его собственном проекте: транскрипт ищется по
  // дереву проекта, и лента чужого разговора, спрошенная у соседней доски,
  // отвечала пустотой. Доска под панелью при этом остаётся той, что выбрана в
  // шапке.
  project = st.project || project;
  chatShown = { project, sid: st.sid || "", task: st.task || "" };
  // Открытый чат закрепляется за задачей: следующее открытие панели с её
  // экрана вернёт этот же чат, а не первый из списка.
  if (st.task && st.sid) chatTaskLastSet(st.task, st.sid);
  const slot = chatSlotPut(pin, key, [chatHead(project, st), chatPanel(project, st)]);
  panel.classList.remove("cload");
  // Разговор, заведённый только что: курсор стоит в поле ввода. Открытый
  // прежний разговор фокуса не получает, его открывают и читать.
  chatSayFocusFresh(st);
  // Слот помнит, чем поднять своё живое и что в нём стоит: возврат в этот
  // разговор обходится показом и подъёмом, без похода в сеть за состоянием.
  slot.arm = chatArm;
  slot.ringArm = chatRingArm;
  slot.shown = chatShown;
  chatArm = null;
  chatRingArm = null;
}

// Доска панели: над экранами, которые её не читают (накопитель, поиск, лента),
// строку задачи взять неоткуда, а по ней панель узнаёт цель и закрытую задачу.
// Ответ сервера кэширован по времени правки доски, и лишний заход дешёв.
async function chatBoardOf(project) {
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/board");
  return r.ok ? (r.body.board || {}) : {};
}

// Раздел «Черновики» (#проект/drafts): накопитель docs/tasks/drafts/ списком.
// Черновик не виден на доске, и без этого раздела записанная с телефона мысль
// лежит в файле, до которого с телефона не добраться. Разбор поднимает ту же
// механику, что и конвейер задачи: сессия агента с заказом груминга, после
// которого строка оказывается в Backlog. Сама запись со своим текстом, ходом
// разбора и исходом живёт экраном ниже: одно место лучше двух.
// Порядок накопителя на экране это дело экрана, а не утилиты: taskctl печатает
// записи по возрастанию ID, и груминг читает их этим порядком, а человек
// приходит за тем, что записал последним, и ждёт его первой строкой. Правит
// порядок шапка колонок, общая у трёх разделов доски (tblHead выше). Кнопка о
// двух положениях, стоявшая тут до POC, свою работу шапке и отдала: два
// порядка на всю карточку отвечали не на тот вопрос, а по важности записи
// список не строился вовсе.

// День записи одной строкой. У записи без строки «записан» поле written пустое,
// и день выводится из возраста, который taskctl меряет правкой файла: иначе
// записи с телефона собирались бы кучей в хвосте списка.
function draftDay(d) {
  if (d.written) return String(d.written);
  const days = Number(d.age_days);
  const at = new Date();
  at.setDate(at.getDate() - (Number.isFinite(days) ? days : 0));
  return at.toISOString().slice(0, 10);
}

// Номер записи из её ID: внутри одного дня он и решает порядок, потому что
// растёт по времени записи.
function draftNum(d) {
  const m = /(\d+)\s*$/.exec(String(d.id || ""));
  return m ? Number(m[1]) : 0;
}

// Уровень разбора числом: им и стоит колонка важности. Записи без уровня
// приходят с пустым полем, и лежат они ниже всех названных при убывании и выше
// при возрастании, как и всякое отсутствие значения.
const DRAFT_PRIO_N = { high: 3, mid: 2, low: 1 };

function draftValue(d, col) {
  if (col === "prio") return DRAFT_PRIO_N[d.prio] || 0;
  if (col === "id") return draftNum(d);
  if (col === "title") return String(d.title || "");
  return draftDay(d);
}

function draftsSorted(drafts) {
  return tblSorted(drafts, "drafts", draftValue,
    (a, b, dir) => (draftNum(b) - draftNum(a)) * (dir === "asc" ? -1 : 1));
}

const GROOM_HINT = "«Грумить» поднимает сессию разбора: она доведёт " +
  "запись до строки Backlog либо снимет её с причиной. Ход разбора и его исход " +
  "видны на экране записи.";

// afterOk это хэш экрана записи: заполнен со строки накопителя, до DK-286
// нажатие там уводило на общий экран агента, у которого нет ни текста
// записи, ни исхода разбора (LLD DK-328, «Отвергнутое»). С экрана самой
// записи afterOk не передают, там уже стоит экран этой работы.
async function groomDraft(project, id, afterOk, harness, tier) {
  sayResult("подъём груминга " + id + "...");
  const order = {};
  // Подписка едет только выбранная: пустое поле это «как раньше», и сервер
  // поднимает разбор подпиской по умолчанию.
  if (harness) order.harness = harness;
  // Ярус едет тем же телом: пустое поле это pro, и сверяет имя с раскладкой
  // машины сам сервер, как сверяет имя подписки.
  if (tier) order.tier = tier;
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/drafts/" + encodeURIComponent(id) + "/groom",
    { method: "POST", body: order });
  const said = r.body.message || r.body.error || "";
  // Имя поднятой сессии остаётся следом: по нему моргает кнопка чата записи,
  // пока разбор идёт, а пустая панель, открытая по этой записи, ждёт ту же
  // сессию и переезжает на неё, как только та назовётся в реестре. Без следа
  // панель молчала до перезагрузки страницы (живой случай пользователя).
  if (r.ok && r.body.session) {
    markRunLive(project, id, r.body.session);
    chatSewHere(project, id, r.body.session);
  }
  if (r.ok && afterOk) {
    await goKeepingResult(afterOk);
    sayResult(said, false);
    return true;
  }
  sayResult(said, !r.ok);
  return r.ok;
}

// Выбор черновиков под разбор: отметки в строках, запуск один на выбранное.
// Прежде кнопка груминга стояла в каждой строке, и разбирать
// накопитель приходилось по одной записи, а нажатие всплывало до обработчика
// строки и уводило на экран записи вместо запуска (решение пользователя).
// Память живёт экраном: уход с накопителя её очищает, потому что выбор это
// намерение сейчас, а не настройка.
const draftPick = new Set();
// Перерисовка полосы запуска: её зовёт всякая правка выбора. Ставится она
// самой полосой, а до первой отрисовки её нет.
let draftBarPaint = null;
// Отрисовка отметки в строке, по одной на запись. Своё нажатие строка рисует
// сама, но снятие выбора приходит со стороны (после запуска разбора), и без
// этой памяти отметки оставались бы гореть при пустом выборе.
const draftPickSays = new Map();

function draftPickSet(id, on) {
  if (on) draftPick.add(id);
  else draftPick.delete(id);
  const say = draftPickSays.get(id);
  if (say) say();
  if (draftBarPaint) draftBarPaint();
}

function draftPickClear() {
  const was = [...draftPick];
  draftPick.clear();
  for (const id of was) {
    const say = draftPickSays.get(id);
    if (say) say();
  }
  if (draftBarPaint) draftBarPaint();
}

// Полоса запуска разбора: одна кнопка на выбранное и поле модели рядом с ней.
// Пока ничего не отмечено, кнопки нет вовсе: гашеная кнопка отвечала на вопрос,
// которого никто не задавал, а что отметки бывают, говорит подпись рядом
// (решение пользователя).
function draftRunBar(project, works) {
  const bar = el("div", "nbar");
  draftRunBarFill(bar, project, works);
  return bar;
}

// Модель разбора живёт экраном, а не полосой: полоса пересобирается на всякую
// отметку в списке, и выбор возвращался бы к умолчанию на каждое нажатие.
let draftModel = "";

// Плоская лестница моделей машины: подписка с ярусом развёрнуты в имя модели.
// Собирается она из той же раскладки (agentctl harness), которой живёт выбор
// подписки у запуска задачи: своего перечня имён у дашборда нет ни одного, и
// новая подписка появляется в выборе сама. Повторы имени внутри подписки
// отсеиваются: у второй подписки верхние ярусы сложены одной моделью, и две
// одинаковые строки в списке читались бы ошибкой.
function modelLadder() {
  const out = [];
  for (const h of harnesses()) {
    const seen = [];
    for (const m of h.models || []) {
      if (!m.model || seen.includes(m.model)) continue;
      seen.push(m.model);
      out.push({ model: m.model, tier: m.tier || "", harness: h.name,
        pin: Boolean(h.default) && m.tier === RUN_TIER });
    }
  }
  return out;
}

// Чем поедет разбор: выбранное имя, а без выбора модель подписки по умолчанию
// на ярусе RUN_TIER. Наружу едут подписка и ярус, как ехали раньше: ручка
// разбора имени модели не знает вовсе, и знать ей его незачем.
function modelLadderPick(name) {
  const list = modelLadder();
  return list.find((m) => m.model === name) || list.find((m) => m.pin) || list[0] || null;
}

// Поле выбора модели рядом с кнопкой: человек спрашивает «чем будет
// разбираться», и ответ на это ярусом с подпиской заставлял его держать в
// голове их раскладку. Ярус с подпиской остались тем, что уезжает в заказ.
function draftModelPick() {
  const list = modelLadder();
  if (!list.length) return null;
  const now = modelLadderPick(draftModel);
  const sel = el("select", "cdsel");
  sel.setAttribute("aria-label", "Модель разбора");
  for (const m of list) {
    const o = el("option", "", m.model);
    o.value = m.model;
    o.title = m.tier ? "ярус " + m.tier + ", подписка " + m.harness : "подписка " + m.harness;
    if (now && m.model === now.model) o.selected = true;
    sel.append(o);
  }
  if (now) sel.value = now.model;
  const why = (m) => (m ? m.model + ": ярус " + m.tier + ", подписка " + m.harness
    + ". Список из раскладки машины, agentctl harness." : "Модель разбора");
  withTip(sel, why(now));
  sel.addEventListener("change", () => {
    draftModel = sel.value;
    withTip(sel, why(modelLadderPick(draftModel)));
  });
  return sel;
}

function draftRunBarFill(bar, project, works) {
  // Перерисовка полосы это её собственное дело: отметка в строке зовёт её через
  // draftBarPaint, не трогая список.
  draftBarPaint = () => { draftRunBarFill(bar, project, works); };
  const picked = [...draftPick];
  if (!picked.length) {
    bar.replaceChildren(el("span", "hint",
      "Отметьте записи в списке, и разбор поднимется на выбранные."));
    return;
  }
  const grp = el("span", "grun");
  // Число стоит в самой подписи: подтверждения перед подъёмом больше нет, и
  // сказать, сколько сессий встанет, надо на кнопке.
  const btn = el("button", "btn btn-sm btn-acc", "Грумить (" + picked.length + ")");
  withTip(btn, GROOM_HINT);
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    // Кнопка гаснет на время подъёма: пачка идёт по одной сессии, и второе
    // нажатие подняло бы их дважды. Прежде тут стояло подтверждение карточкой,
    // а исходная кнопка оставалась доступной поверх открытого вопроса
    // (замечание пользователя по снимку).
    btn.disabled = true;
    const now = modelLadderPick(draftModel);
    Promise.resolve(draftGroomStart(project, works, now ? now.harness : "", now ? now.tier : ""))
      .catch(console.error).finally(() => { btn.disabled = false; });
  });
  grp.append(btn);
  const sel = draftModelPick();
  if (sel) grp.append(sel);
  bar.replaceChildren(grp, el("span", "hint", "Выбрано " + picked.length + " " +
    plural(picked.length, "запись", "записи", "записей") +
    ", каждая пойдёт своим разговором."));
}

// Подъём разбора на выбранные записи. Подтверждения перед ним нет: выбор
// отметками это и есть осознанное действие человека, а второй вопрос поверх
// него он назвал лишним. Записи, у которых разбор уже идёт, пропускаются, а не
// роняют пачку отказом, и сказано про них строкой итога.
async function draftGroomStart(project, works, harness, tier) {
  const picked = [...draftPick];
  if (!picked.length) return 0;
  // Идущий разбор тут судится живой работой, а не памятью подъёма: память
  // живёт четверть часа и после умершей сессии, и повторный разбор той же
  // записи она запирала бы на всё это время. Кнопке чата память нужна ровно
  // затем, чтобы моргать между нажатием и первым ходом сессии, а воротам
  // пачки нужна правда о tmux.
  const going = picked.filter((id) => Boolean(workBusy(id, works)));
  const todo = picked.filter((id) => !going.includes(id));
  if (!todo.length) {
    sayResult("разбор идёт у всех выбранных записей (" + going.join(", ") +
      "): поднимать нечего", true);
    return 0;
  }
  return draftGroomRun(project, todo, harness, tier, going);
}

// Подъём идёт по одной сессии: ручка разбора поднимает свою, и слать их разом
// значило бы драться за один и тот же tmux-замок.
async function draftGroomRun(project, ids, harness, tier, skipped) {
  let done = 0;
  const bad = [];
  for (const id of ids) {
    // afterOk тут пустой: пачка остаётся на накопителе, уводить её на экран
    // одной записи некуда.
    if (await groomDraft(project, id, "", harness, tier)) done += 1;
    else bad.push(id);
  }
  draftPickClear();
  // Итог одной строкой, как у запуска задачи: сколько поднялось, что
  // пропущено и что не поднялось вовсе.
  const skip = skipped || [];
  sayResult("поднято " + done + " " + plural(done, "сессия", "сессии", "сессий") +
    " разбора" +
    (skip.length ? ", пропущено " + skip.length + " с идущим разбором: " + skip.join(", ") : "") +
    (bad.length ? ", не поднялось: " + bad.join(", ") : ""), bad.length > 0);
  await refresh();
  return done;
}

// DRAFT_PRIO переводит уровень разбора в слово чипа: имя уровня латиницей
// живёт в поле prio ответа taskctl, а экран накопителя говорит по-русски, тем
// же словом, каким уровень стоит в файле черновика и в draft list.
const DRAFT_PRIO = { high: "высокий", mid: "средний", low: "низкий" };

// Строка накопителя ведёт на экран записи, а разбор запускается не из неё:
// накопитель разбирают пачкой, и отметка выбора стоит в строке, а кнопка
// запуска одна над списком (LLD DK-328, решение пользователя).
function draftRow(project, d) {
  const row = freshMark(el("tr", "dsrow clicky"), d.id);
  // Отметка выбора это кнопка, а не флажок браузера: палец попадает в неё
  // целиком, а состояние читается с самой кнопки, а не с её начинки.
  const pick = el("button", "dpick" + (draftPick.has(d.id) ? " on" : ""));
  pick.type = "button";
  pick.append(el("span", "dbox"));
  const pickSay = () => {
    const on = draftPick.has(d.id);
    pick.classList.toggle("on", on);
    pick.setAttribute("aria-pressed", on ? "true" : "false");
    withTip(pick, on ? "Снять выбор с " + d.id : "Выбрать " + d.id + " для разбора");
    pick.setAttribute("aria-label", "Выбрать " + d.id + " для разбора");
  };
  pickSay();
  draftPickSays.set(d.id, pickSay);
  pick.addEventListener("click", (ev) => {
    ev.stopPropagation();
    draftPickSet(d.id, !draftPick.has(d.id));
    pickSay();
  });
  // Отметка выбора и уровень разбора стоят одной колонкой. Врозь они занимали
  // две, и подпись «Приоритет» в шапке переставала влезать, стоило сортировке
  // добавить к ней значок направления (замечание пользователя). Ячейка есть у
  // всякой записи, у записи без уровня в том числе: иначе соседние колонки
  // съезжали бы на одну.
  const { cell: impc, box: imp } = tblCell("dimp");
  imp.append(pick);
  if (d.prio) imp.append(el("span", "chip", DRAFT_PRIO[d.prio] || d.prio));
  row.append(impc);
  row.append(el("td", "id", d.id));
  // Заголовок записи режется той же кромкой, что и заголовок строки доски, и
  // подсказка с полным текстом тут нужна ровно так же: длинную мысль с
  // телефона иначе не прочитать, не заходя внутрь (замечание пользователя).
  // Живёт он в своей ячейке вместе с приписками: колонка названия растёт по
  // наведению, и приписки обязаны ехать вместе с ней, а не стоять поодаль.
  const { cell: ttc, box: tt } = tblCell("dtt");
  tt.append(withFull(el("span", "st", d.title || ""), d.title || ""));
  const chips = [];
  if (d.deferred) chips.push(el("span", "chip", "отложен " + d.deferred));
  // Разбор спросил и ждёт ответа: признак тот же и теми же словами, что у
  // строки доски, своего у накопителя нет.
  const waits = waitChip(d);
  if (waits) chips.push(waits);
  if (chips.length) {
    const box = el("span", "rchips");
    for (const chip of chips) box.append(chip);
    tt.append(box);
  }
  row.append(ttc);
  // Дата правки записи вместо возраста днями и теми же словами, что у строки
  // доски: возраст «3 дня» отвечал не на тот вопрос (замечание пользователя).
  // Колонка у неё своя: по ней список и сортируется нажатием в шапке.
  const when = el("td", "dwhen");
  if (d.moved) {
    when.append(withTip(el("span", "stale dashed", d.moved),
      whenTip(d.moved_at || d.moved)));
  }
  row.append(when);
  const { cell: metac, box: meta } = tblCell("sm");
  // Черновик это та же задача, просто в черновом исполнении, и обсуждать его с
  // агентом надо тем же способом: кнопка та же, значок тот же, панель
  // открывается с привязкой к его ID (решение пользователя).
  const talk = rowChatBtn(project, d);
  const acts = el("span", "racts");
  acts.append(talk);
  meta.append(acts);
  row.append(metac);
  row.addEventListener("click", (ev) => {
    // Нажатым оказывается не сама кнопка, а её начинка (значок у чата,
    // стрелка у выбора подписки), и спрашивать надо, лежит ли нажатое внутри
    // кнопки, а не равно ли оно ей. Прежняя проверка звала includes у
    // children, а children в браузере это HTMLCollection без методов массива:
    // нажатие на строку накопителя падало с TypeError, не доходя до перехода,
    // и запись не открывалась ни с доски, ни с телефонного таба.
    if (talk.contains(ev.target) || pick.contains(ev.target)) return;
    goKeepingChat(project + "/draft/" + d.id);
  });
  return row;
}

// Накопитель рисуется после ответа сервера, а не до него: очищенный заранее
// экран моргал бы пустотой на каждом обновлении по фокусу окна, а список уезжал
// бы к началу из-под пальца.
async function renderDrafts(project, works) {
  const groups = document.getElementById("groups");
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/drafts");
  // Число записей тут точнее счёта сервера: накопитель только что прочитан
  // своей ручкой.
  countsSet({ sess: (works || []).length,
    drafts: ((r.ok && r.body.drafts) || []).length });
  const items = [{
    key: "board-kind",
    sign: [project, "drafts", shownCounts.tasks, (works || []).length,
      shownCounts.drafts].join("|"),
    // Дорога назад к задачам это тот же таб, что и привёл сюда: хлебной
    // крошки над накопителем больше нет, она вела туда же вторым способом.
    make: () => boardKindBar(project, "drafts"),
  }, {
    key: "drafts-run",
    // Полоса запуска перерисовывается сама, изнутри: от выбора её подпись не
    // зависит нарочно, иначе отметка перебирала бы весь список на каждое
    // нажатие. А вот живые работы в подпись входят: по ним полоса решает, у
    // какой записи разбор уже идёт, и собранная однажды она отвечала бы по
    // позапрошлому состоянию машины.
    sign: [project, (works || []).filter((w) => w.live === WORK_BUSY || w.live === WORK_WAIT)
      .map((w) => w.id).join(",")].join("|"),
    make: () => draftRunBar(project, works),
    fill: (bar) => { draftRunBarFill(bar, project, works); },
  }];
  if (!r.ok) {
    const text = r.body.error || "накопитель не прочитался";
    items.push({
      key: "drafts-card",
      sign: "error|" + text,
      make: () => {
        const card = el("div", "card");
        card.append(el("div", "error", text));
        return card;
      },
    });
    sync(groups, items);
    return;
  }
  const drafts = r.body.drafts || [];
  // Выбор живёт этим экраном и этим списком: записи, которой больше нет в
  // накопителе (её разобрали), в выборе тоже быть не должно.
  const here = new Set(drafts.map((d) => d.id));
  for (const id of [...draftPick]) {
    if (!here.has(id)) draftPick.delete(id);
  }
  // Память отрисовок чистится по списку, а не целиком: строка, пережившая
  // обновление, свою отрисовку не перезаводит (её узел тот же), и снос всей
  // памяти оставил бы такие строки без кисти.
  for (const id of [...draftPickSays.keys()]) {
    if (!here.has(id)) draftPickSays.delete(id);
  }
  // Карточка списка держится в переменной: переключение порядка перебирает её
  // строки на месте, не ходя за ними на сервер. Данных для порядка в ответе
  // хватает, и второй запрос ради перестановки был бы лишним.
  let cardNode = null;
  const rowsFor = () => {
    const out = [];
    // Пустой накопитель говорит словами сервера: пустая таблица неотличима от
    // неотрисованной.
    if (!drafts.length) {
      const note = r.body.note || "черновиков нет";
      out.push({
        key: "empty",
        sign: note,
        make: () => {
          const band = tblBand("drafts", "bempty");
          band.cell.append(el("div", "empty", note));
          return band.tr;
        },
      });
    }
    // Ключ строки это ID черновика: обновление по фокусу окна трогает только те
    // строки, что изменились, и список не уезжает из-под пальца.
    for (const d of draftsSorted(drafts)) {
      // Отметка выбора входит в подпись строки: сама по себе она рисуется своим
      // же нажатием, а вот снятие выбора после запуска приходит со стороны, и без
      // подписи строка осталась бы отмеченной при пустом выборе.
      out.push({ key: d.id,
        sign: JSON.stringify(d) + (freshRow === d.id ? "|fresh" : "") +
          (draftPick.has(d.id) ? "|pick" : ""),
        make: () => draftRow(project, d) });
    }
    return out;
  };
  // Шапка колонок вместо слова «Черновики» с числом: и слово, и число уже
  // стоят табом над списком, а место над колонками занимали зря. Порядок
  // правится нажатием на подпись, тем же приёмом, что у доски и сессий.
  const partsFor = () => {
    const rows = rowsFor();
    const parts = [{ key: "cols", sign: "cols", make: () => tblColgroup("drafts") }];
    if (drafts.length) {
      parts.push({
        key: "drafts-head",
        sign: tblHeadSign("drafts"),
        make: () => tblHead("drafts", () => { paintRows(); }),
      });
    }
    parts.push(tblBodyItem("drafts-body",
      rows.map((row) => row.key + "=" + row.sign).join("\n"), rows));
    return parts;
  };
  const paintRows = () => { if (cardNode) sync(cardNode, partsFor()); };
  const parts = partsFor();
  items.push({
    key: "drafts-card",
    sign: parts.map((part) => part.key + "=" + part.sign).join("\n"),
    make: () => {
      const table = tblTable("drafts");
      cardNode = table;
      sync(table, partsFor());
      return table;
    },
    fill: (table) => {
      cardNode = table;
      sync(table, partsFor());
    },
  });
  if (drafts.length) {
    items.push({
      key: "drafts-foot",
      sign: "",
      make: () => {
        const foot = el("div", "nbar");
        foot.append(el("span", "hint", GROOM_HINT));
        return foot;
      },
    });
  }
  sync(groups, items);
}

// Поиск задач (LLD DK-328, решение 2). Поле стоит в шапке доски и на самом
// экране выдачи, а выдача занимает свой экран по адресу
// "#проект/find/<запрос>": выпадающий список под полем на телефоне дрался бы с
// клавиатурой за нижнюю половину экрана. Ищет сервер одной ручкой, а клиент не
// подменяет её чтением всей доски: архив с сотнями строк на телефон не тянут.

// Задержка ввода: пока человек печатает, каждая буква не уходит своим
// запросом. Четверть секунды это промежуток между словами, а не между
// буквами.
const FIND_WAIT = 250;

// Слова пустых случаев приезжают с сервера (состав источников знает он), а
// эти остаются на крайние случаи: ответ без слов и оборванная связь.
const FIND_EMPTY = "По запросу ничего нет.";

let findTimer = null;

// Поколение запроса: ответ на прежний запрос приходит после того, как человек
// дописал слово, и нарисованный поверх свежей выдачи он показывал бы чужие
// строки. Отменяется тут именно отрисовка: сам ответ сервера уже в пути.
let findGen = 0;

function findInput(cls, q) {
  const box = el("div", cls);
  const ico = el("span", "fico");
  ico.append(icon("i-find"));
  box.append(ico);
  const input = el("input", "");
  input.type = "text";
  input.value = q || "";
  input.placeholder = "Поиск задач";
  input.setAttribute("autocomplete", "off");
  input.setAttribute("aria-label", "Поиск по доске, черновикам и архиву");
  const clear = el("button", "fclear");
  clear.setAttribute("aria-label", "Сбросить поиск");
  clear.append(icon("close"));
  wireFindField(input, clear);
  // Крестик стоит после поля: fill экрана выдачи ищет поле вторым узлом.
  box.append(input, clear);
  return box;
}

// Поле поиска: набор с задержкой, ввод отправляет запрос сразу. Поля два, в
// шапке и на экране выдачи, и ведут они себя одинаково. Крестик сброса это
// та же дорога, что Escape: запрос стирается, экран возвращается к доске.
function wireFindField(input, clear) {
  const showClear = () => { if (clear) clear.hidden = !input.value; };
  const reset = () => {
    clearTimeout(findTimer);
    input.value = "";
    showClear();
    findGo("");
  };
  input.addEventListener("input", () => {
    showClear();
    findType(input.value);
  });
  input.addEventListener("keydown", (ev) => {
    // Escape это отказ от поиска: поле пустеет, и экран возвращается к тому,
    // что было под выдачей. Ждать при этом нечего, срок набора снимается.
    if (ev.key === "Escape") {
      reset();
      return;
    }
    if (ev.key !== "Enter") return;
    clearTimeout(findTimer);
    findGo(input.value);
  });
  if (clear) {
    clear.addEventListener("click", () => {
      reset();
      if (input.focus) input.focus();
    });
    showClear();
  }
}

// Набор с задержкой: запрос уезжает в адрес, а не в ручку напрямую, и экран
// выдачи собирается тем же путём, каким открывается по ссылке.
function findType(value) {
  clearTimeout(findTimer);
  findTimer = setTimeout(() => { findGo(value); }, FIND_WAIT);
}

// Столько знаков сервер ждёт от запроса (searchMinQuery в search.go): короче
// он не ищет, и экран выдачи до этого порога не открывается вовсе.
const FIND_MIN = 2;

function findGo(value) {
  const rt = route();
  // В табе сессий поле фильтрует его собственные строки: доска тут ни при чём,
  // и уводить отсюда в выдачу по задачам значит отвечать не на тот вопрос
  // (замечание пользователя).
  if (rt.sess) {
    const q = String(value).trim();
    const base = rt.proj + "/sess" + (q ? "/" + encodeURIComponent(q) : "");
    // Пустой запрос это весь раздел, и адрес у него прежний: экран тут не
    // меняется, меняется только отбор строк.
    const hash = "#" + (rt.chat ? base + "/chat/" + rt.chat : base);
    if (hash !== "#" + location.hash.replace(/^#/, "")) location.replace(hash);
    return;
  }
  const project = shownProject || route().proj;
  if (!project) return;
  const q = String(value).trim();
  // Пустое поле это отказ от поиска, а не поиск пустоты: экран выдачи уходит
  // целиком и открывается доска. Прежде адрес оставался «.../find/», выдача
  // висела прежними строками, а следом за ней вставала пустая с надписью про
  // два символа, и лечилось это только перезагрузкой (замечание пользователя).
  // Короче двух знаков это ещё не запрос: сервер такой не ищет и отвечает
  // словами про два символа, а экран выдачи на месте доски показывал бы пустоту
  // с этой надписью. Пока запрос не набран, стоит доска.
  if (Array.from(q).length < FIND_MIN) {
    if (!route().find) return;
    goKeepingChat(project);
    return;
  }
  const base = project + "/find/" + encodeURIComponent(q);
  // Открытый разговор переезжает и с каждой набранной буквой. Замена адреса
  // хвост панели не дописывала, и поиск оставался единственной дорогой, что
  // закрывает чат: первая же буква сносила его с экрана.
  const chat = route().chat;
  const hash = "#" + (chat ? base + "/chat/" + chat : base);
  if (hash === "#" + location.hash.replace(/^#/, "")) return;
  // Набор это не переход: каждая буква отдельной записью в истории браузера
  // превратила бы «назад» в перемотку по буквам. Переход на экран выдачи
  // записью остаётся, с него «назад» и возвращает на доску.
  if (route().find) location.replace(hash);
  else goKeepingChat("#" + base);
}

// Курсор в поле поиска по косой черте: руки на клавиатуре, и тянуться мышью к
// шапке ради поиска не надо. В поле ввода косая черта остаётся косой чертой.
function wireFindKey() {
  document.addEventListener("keydown", (ev) => {
    if (ev.key !== "/" || ev.metaKey || ev.ctrlKey || ev.altKey) return;
    const at = document.activeElement;
    const tag = at && at.tagName;
    if (tag === "INPUT" || tag === "TEXTAREA" || (at && at.isContentEditable)) return;
    if (ev.preventDefault) ev.preventDefault();
    const field = document.getElementById("hq");
    if (field && field.focus) field.focus();
  });
}

// Подсветка совпадений: текст режется на куски вокруг запроса, найденное
// уходит в <mark>. Регистр не важен, как и на сервере. Куски вставляются
// текстовыми узлами, HTML из данных по-прежнему не собирается.
function markHits(box, text, q) {
  const src = String(text === undefined || text === null ? "" : text);
  const needle = String(q || "").trim().toLowerCase();
  if (!needle) {
    box.append(document.createTextNode(src));
    return box;
  }
  const low = src.toLowerCase();
  let at = 0;
  for (;;) {
    const hit = low.indexOf(needle, at);
    if (hit < 0) break;
    if (hit > at) box.append(document.createTextNode(src.slice(at, hit)));
    box.append(el("mark", "hit", src.slice(hit, hit + needle.length)));
    at = hit + needle.length;
  }
  if (at < src.length) box.append(document.createTextNode(src.slice(at)));
  return box;
}

// Строка выдачи. Группы рисуются одним путём, а разница между ними это то, что
// у строки заполнено: у доски секция с ценой и рангом, у черновика возраст, у
// архивной строки дата закрытия (ранга и цены в архиве нет вовсе), у найденной
// в тексте цитата с местом файла.
function findRow(project, key, row, q) {
  // Строка выдачи нажимается, и курсор об этом говорит: со стрелкой она
  // читалась как подпись, а не как дорога (замечание 3 четырнадцатого круга
  // POC). Класс тот же, каким помечены нажимаемые строки накопителя.
  const tr = el("div", "srow clicky");
  tr.append(el("span", "id", row.id));
  const st = withFull(el("span", "st fst"), row.title || "");
  markHits(st, row.title || "", q);
  if (row.quote) {
    const quote = el("div", "fquote");
    markHits(quote, row.quote, q);
    st.append(quote);
  }
  tr.append(st);
  const meta = el("span", "sm");
  if (row.section) meta.append(el("span", "chip", row.section));
  if (row.closed) meta.append(el("span", "chip", "закрыта " + row.closed));
  if (row.where) meta.append(el("span", "chip", row.where));
  if (row.type && row.type !== "task") meta.append(el("span", "chip", row.type));
  if (row.cost && row.cost !== "-") meta.append(el("span", "chip", row.cost));
  if (row.age_words) meta.append(el("span", "stale", row.age_words));
  if (row.file) {
    meta.append(el("span", "stale", row.file + (row.line ? ":" + row.line : "")));
  }
  if (row.r) meta.append(rankCell(row));
  tr.append(meta);
  // Черновик ведёт на свой экран, найденный LLD на форму документа, остальное
  // на экран задачи: закрытая задача открывается там же, файлом и без правок.
  tr.addEventListener("click", () => {
    goKeepingChat(key === "drafts" ? project + "/draft/" + row.id
      : row.file && row.file.startsWith("docs/lld/") ? project + "/doc/" + row.file.slice("docs/".length)
      : project + "/" + row.id);
  });
  return tr;
}

function findGroupItems(project, group, q) {
  const rows = (group.rows || []).map((row) => ({
    key: group.key + "-" + row.id,
    sign: JSON.stringify(row) + "|" + q,
    make: () => findRow(project, group.key, row, q),
  }));
  if (group.note) {
    rows.push({ key: group.key + "-note", sign: group.note, make: () => el("div", "error", group.note) });
  }
  // Урезанная выдача не выглядит полной: хвост назван числом и тем, что с ним
  // делать.
  if (group.more) {
    const tail = "ещё " + group.more + " " +
      plural(group.more, "совпадение", "совпадения", "совпадений") + ", уточните запрос";
    rows.push({ key: group.key + "-more", sign: tail, make: () => el("div", "empty", tail) });
  }
  const total = (group.rows || []).length + (group.more || 0);
  return [{
    key: "find-head-" + group.key,
    sign: group.title + "|" + total,
    make: () => {
      const head = el("div", "chd");
      head.append(el("b", "", group.title));
      head.append(el("span", "cnt", String(total)));
      return head;
    },
  }, {
    key: "find-card-" + group.key,
    sign: rows.map((r) => r.key + "=" + r.sign).join("\n"),
    make: () => {
      const card = el("div", "card");
      sync(card, rows);
      return card;
    },
    fill: (card) => { sync(card, rows); },
  }];
}

// Форма LLD (круг 2 POC DK-470): документ docs/lld собранной разметкой, той
// же формой, что задача и черновик, без лишних рамок и полей. По умолчанию
// чтение, правку включает карандаш, сохранение уводит текст ручкой PUT /doc
// и коммитом; режим чтения разворачивает текст на всю страницу.
const docDraft = { path: "", text: "", dirty: false, edit: false };
async function renderDoc(project, path) {
  const groups = document.getElementById("groups");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/doc?path=" + encodeURIComponent(path));
  if (!r.ok) {
    sync(groups, [{
      key: "doc-err",
      sign: path + "|" + r.status,
      make: () => el("div", "error", r.body.error || ("документ не прочитался (" + r.status + ")")),
    }]);
    return;
  }
  if (docDraft.path !== path) {
    docDraft.path = path;
    docDraft.text = "";
    docDraft.dirty = false;
    docDraft.edit = false;
  }
  const said = r.body.text || "";
  const form = { text: docDraft.dirty ? docDraft.text : said };
  const name = (path.split("/").pop() || "").replace(/\.md$/, "");
  const taskId = (name.match(/^[A-Za-z]+-[0-9]+/) || [""])[0];
  // Заголовок берётся из первой решётки документа; ID из него уходит в номер
  // рядом и второй раз в заголовке не повторяется.
  let titleText = (said.split("\n").find((ln) => ln.startsWith("# ")) || "").replace(/^#\s*/, "") || name;
  if (taskId && titleText.startsWith(taskId + ":")) {
    titleText = titleText.slice(taskId.length + 1).trim();
  }
  const crumb = [
    { text: "Доска " + project, go: () => { goKeepingChat(project); } },
    { text: "LLD", go: () => { goKeepingChat(project + "/lld"); } },
  ];
  if (taskId) {
    crumb.push({ text: taskId, go: () => { goKeepingChat(project + "/" + taskId); } });
  }
  // Путь документа последней крошкой: по нему файл открывают в редакторе, и
  // другого места, где его видно, на экране нет.
  crumb.push({ text: "docs/" + path });
  sync(groups, [{
    key: "doc-page",
    sign: [project, path, said.length, docDraft.dirty, docDraft.edit].join("|"),
    make: () => formPage({
      key: "doc", project, id: taskId || name,
      crumb,
      num: taskId,
      titleText,
      detail: { file: "docs/" + path, text: said, note: "документ пуст" },
      form,
      has: { file: true, pencil: true, read: true },
      penLabel: "Править документ",
      edit: docDraft.edit,
      onEdit: (on) => {
        docDraft.path = path;
        docDraft.edit = on;
      },
      check: () => {
        const dirty = form.text !== said;
        docDraft.path = path;
        docDraft.dirty = dirty;
        docDraft.text = form.text;
        return { dirty, refusal: form.text.trim() ? "" : "пустой текст затёр бы документ" };
      },
      onSave: () => { saveDoc(project, path, form.text).catch(console.error); },
      onDrop: () => {
        docDraft.dirty = false;
        docDraft.edit = false;
        const shown = findKey(groups, "doc-page");
        if (shown) shown.dataset.psign = "";
        renderDoc(project, path).catch(console.error);
      },
    }).page,
  }]);
}

async function saveDoc(project, path, text) {
  sayResult("сохранение docs/" + path + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/doc?path=" + encodeURIComponent(path), { method: "PUT", body: { text } });
  sayResult(apiSaid(r), !r.ok);
  if (!r.ok) return;
  docDraft.dirty = false;
  docDraft.edit = false;
  const shown = findKey(document.getElementById("groups"), "doc-page");
  if (shown) shown.dataset.psign = "";
  await renderDoc(project, path);
}

// Раздел LLD (круг 2 POC DK-470): дизайны проекта одним списком, свежие
// сверху. Строка показывает задачу, заголовок документа и дату правки;
// поиск идёт по имени, заголовку и тексту, совпадение по тексту едет
// цитатой, как в поиске задач. Запрос живёт в адресе, как у выдачи поиска.
let lldGen = 0;
let lldTimer = 0;
function lldType(project, value) {
  clearTimeout(lldTimer);
  lldTimer = setTimeout(() => {
    const base = project + "/lld/" + encodeURIComponent(String(value).trim());
    const chat = route().chat;
    const hash = "#" + (chat ? base + "/chat/" + chat : base);
    if (hash === "#" + location.hash.replace(/^#/, "")) return;
    if (route().lldList) location.replace(hash);
    else goKeepingChat("#" + base);
  }, FIND_WAIT);
}

async function renderLld(project, q) {
  const groups = document.getElementById("groups");
  const head = [{
    key: "lld-crumb",
    sign: project,
    make: () => {
      const crumb = el("div", "crumb");
      const back = el("span", "crumb-back", "Доска " + project);
      back.addEventListener("click", () => { goKeepingChat(project); });
      crumb.append(back);
      return crumb;
    },
  }, {
    key: "lld-q",
    sign: project,
    make: () => {
      const box = el("div", "fqbar");
      const ico = el("span", "fico");
      ico.append(icon("i-find"));
      box.append(ico);
      const input = el("input", "");
      input.type = "text";
      input.value = q || "";
      input.placeholder = "Поиск LLD";
      input.setAttribute("autocomplete", "off");
      input.setAttribute("aria-label", "Поиск LLD по названию и тексту");
      input.addEventListener("input", () => { lldType(project, input.value); });
      box.append(input);
      return box;
    },
    fill: (box) => {
      const input = box.children[1];
      if (document.activeElement !== input && input.value !== q) input.value = q;
    },
  }];
  const fresh = !findKey(groups, "lld-q");
  if (fresh) sync(groups, head);
  const gen = ++lldGen;
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/lld" + (q ? "?q=" + encodeURIComponent(q) : ""));
  if (gen !== lldGen) return;
  if (!r.ok) {
    sync(groups, [...head, {
      key: "lld-err",
      sign: String(r.status),
      make: () => el("div", "error", r.body.error || ("список LLD не прочитался (" + r.status + ")")),
    }]);
    return;
  }
  const rows = r.body.rows || [];
  const items = rows.map((row) => ({
    key: "lld-" + row.file,
    sign: JSON.stringify(row) + "|" + q,
    make: () => {
      const tr = el("div", "srow clicky");
      tr.append(el("span", "id", row.id || ""));
      const st = withFull(el("span", "st fst"), row.title || row.file);
      markHits(st, row.title || row.file, q);
      if (row.quote) {
        const quote = el("div", "fquote");
        markHits(quote, row.quote, q);
        st.append(quote);
      }
      tr.append(st);
      const meta = el("span", "sm");
      meta.append(el("span", "stale", "docs/" + row.file + (row.line ? ":" + row.line : "")));
      if (row.date) meta.append(el("span", "chip", row.date));
      tr.append(meta);
      tr.addEventListener("click", () => { goKeepingChat(project + "/doc/" + row.file); });
      return tr;
    },
  }));
  if (!items.length) {
    items.push({
      key: "lld-empty",
      sign: q || "",
      make: () => el("div", "empty", q ? "ничего не нашлось, уточните запрос" : "в docs/lld пусто"),
    });
  }
  sync(groups, [...head, ...items]);
}

async function renderFind(project, q) {
  const groups = document.getElementById("groups");
  const head = [{
    key: "find-crumb",
    sign: project,
    make: () => {
      const crumb = el("div", "crumb");
      const back = el("span", "crumb-back", "Доска " + project);
      back.addEventListener("click", () => { goKeepingChat(project); });
      crumb.append(back);
      return crumb;
    },
  }, {
    key: "find-q",
    sign: project + "|" + q,
    make: () => findInput("fqbar", q),
    // Поле переживает перерисовку: пересобранное на каждой букве, оно теряло
    // бы курсор вместе с набранным. Значение правится только вне фокуса, иначе
    // набор дёргался бы под пальцами.
    fill: (box) => {
      const input = box.children[1];
      if (document.activeElement !== input && input.value !== q) input.value = q;
    },
  }];
  // Поле шапки синхронизирует с адресом сам paint: тут оно не трогается.
  // Первый заход рисует крошку с полем сразу, до ответа сервера: пустой экран
  // не давал бы набрать запрос. Дальше прежняя выдача стоит, пока не приехала
  // новая, и экран не моргает пустотой на каждой букве.
  const fresh = !findKey(groups, "find-q");
  if (fresh) {
    sync(groups, head);
    // Пустой запрос это заход с лупы: поле открылось ради набора, и экрану не
    // нужно второго касания ради курсора. Курсор ставится после вставки узла в
    // документ, а не при сборке: фокус на неприкреплённом узле пуст, и
    // приёмка находила ровно это (хвост DK-325).
    if (!q) {
      const box = findKey(groups, "find-q");
      if (box) box.children[1].focus();
    }
  }
  const gen = ++findGen;
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/search?q=" + encodeURIComponent(q));
  if (gen !== findGen) return;
  const items = head.slice();
  if (!r.ok) {
    const text = r.body.error || "поиск не отработал (" + r.status + ")";
    items.push({
      key: "find-error",
      sign: text,
      make: () => {
        const card = el("div", "card");
        card.append(el("div", "error", text));
        return card;
      },
    });
    sync(groups, items);
    return;
  }
  for (const group of r.body.groups || []) {
    if (!(group.rows || []).length && !group.note) continue;
    for (const item of findGroupItems(project, group, q)) items.push(item);
  }
  // Пустая выдача говорит словами сервера, где искали: молчаливая пустота
  // неотличима от «архив в поиск не входит».
  if (items.length === head.length) {
    const note = r.body.note || FIND_EMPTY;
    items.push({ key: "find-empty", sign: note, make: () => el("div", "empty", note) });
  }
  sync(groups, items);
}

// Удаление записи накопителя: причина уезжает сообщением коммита доски, и без
// неё ручка запись не трогает.
async function dropDraft(project, id, reason) {
  sayResult("удаление черновика " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/drafts/" + encodeURIComponent(id), { method: "DELETE", body: { reason } });
  sayResult(apiSaid(r), !r.ok);
  return r.ok;
}






// Запись переписал кто-то ещё, пока человек правил её тут: текст с диска
// показывается рядом с формой целиком, а набранное остаётся в поле. Свести их
// за человека нечем, второго писателя тут двое, разбор и вторая вкладка, и что
// оставить, знает только он.
function showDraftClash(said) {
  const page = findKey(document.getElementById("groups"), "draft-page");
  if (!page) return;
  const was = page.querySelector(".dclash");
  if (was) was.remove();
  const card = el("div", "card dclash");
  const head = el("div", "chd");
  head.append(el("b", "", "Запись на диске изменилась"));
  head.append(el("span", "stale", "набранное осталось в поле, ниже текст с диска"));
  card.append(head, el("pre", "dclash-text", said));
  page.append(card);
}

// Сохранение записи накопителя: текст правится тем же полем, что и постановка
// задачи, и уезжает целиком одной ручкой. Пустой текст отбивается до похода на
// сервер: он затёр бы запись, а удаление у черновика своё, с причиной. База это
// хэш текста, с которым экран открывался: писателей у записи двое, человек и
// разбор, и без базы сохранение молча затирало бы чужую правку.
async function saveDraftText(project, id, text, base) {
  sayResult("сохранение черновика " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/drafts/" + encodeURIComponent(id), { method: "PUT", body: { text, base } });
  sayResult(apiSaid(r), !r.ok);
  if (!r.ok) {
    // Правка остаётся тронутой: перерисовка над тронутой формой не идёт, и
    // набранное лежит в поле, пока человек не решит его судьбу.
    if (r.status === 409 && r.body.text) showDraftClash(r.body.text);
    return false;
  }
  taskDraft.dirty = false;
  // Сохранение возвращает просмотр: правка кончилась, и держать поле открытым
  // незачем.
  taskDraft.edit = false;
  await refresh();
  return true;
}

// Карточек исхода груминга на форме записи нет ни одной: разговор с агентом
// всегда идёт в чате, там же видно и чем кончился разбор, а на доске исход
// виден по факту, строкой или её отсутствием (решение пользователя). Прежде
// форма пересказывала след разбора шестью состояниями, и человек читал их
// вместо самой записи.

// Груминг это его собственная сессия (draftSession, имя task-<ID>), а не
// всякий разговор про запись. Прежде идущим разбором считалась любая работа с
// тем же ID, и открытый чат о черновике показывал форме «груминг идёт», хотя
// tmux-сессии разбора не было вовсе (замечание пользователя по DK-502). Тот же
// признак разводит работу и разговор у строк доски (talk).
function draftGrooming(works, id) {
  return Boolean((works || []).find((w) => w.id === id && w.via === "tmux" && !w.talk));
}

// Опрос движения работы: пока по строке может пойти или кончиться ход, экран
// перечитывается по кругу, той же механикой, какой панель разговора дожидается
// доставки реплики. Зовут его два экрана: запись накопителя ждёт исхода
// разбора, а доска ждёт у своих строк и начала работы, и конца. Груминг идёт
// живым чатом, и его сессия переживает конец разбора: без опроса строка стояла
// бы под «Стопом» до перезагрузки страницы (замечание пользователя). Таймер один: следующий заводит очередная перерисовка, пока
// работа жива, а её конец круг обрывает сам, без своего таймера. Уход с экрана
// снимает опрос вместе с остальными живыми потоками (agentLive).
const DRAFT_GROOM_POLL = 3000;
let draftPoll = null;
let draftPollWired = false;
function watchRunning() {
  if (!draftPollWired) {
    draftPollWired = true;
    agentLive.push(() => {
      if (draftPoll !== null) clearTimeout(draftPoll);
      draftPoll = null;
      draftPollWired = false;
    });
  }
  if (draftPoll !== null) return;
  draftPoll = setTimeout(() => {
    draftPoll = null;
    refresh().catch(console.error);
  }, DRAFT_GROOM_POLL);
}

async function renderDraft(project, works, id) {
  const groups = document.getElementById("groups");
  const base = "/api/projects/" + encodeURIComponent(project) + "/drafts/" + encodeURIComponent(id);
  // Экран записи собран той же формой, что и экран задачи: та же шапка, та же
  // разметка вместо сырого текста, те же кнопки режимов справа. Выключены у
  // него ранг, зависимости и поля строки доски: их запись получит только от
  // груминга, и показывать их пустыми не за чем.
  const text = await api(base);
  const running = draftGrooming(works, id);
  // Конец груминга виден без перезагрузки страницы: пока разбор идёт, экран
  // сам дожидается исхода опросом, и пометка уходит вместе с его приходом.
  // Опрос стоит до сторожа правки: иначе он глох бы на время редактирования.
  if (running) watchRunning();
  // В поле лежит правка: перерисовка по фокусу окна стёрла бы её, и экран
  // остаётся как есть.
  if (taskDraft.id === id && taskDraft.dirty) return;
  const said = text.ok ? String(text.body.text || "") : "";
  // База правки приезжает тем же ответом, что и текст: она уедет обратно с
  // сохранением, и разошедшуюся ручка отобьёт вместо тихого затирания.
  const hash = (text.ok && text.body.hash) || "";
  // Ожидание разговора приезжает тем же ответом, что и текст записи: его считает
  // сервер по признаку ожидания, как и у строки доски.
  const waiting = (text.ok && text.body.waiting) || null;
  // Замок редактора (решение 3 LLD DK-354): пока разбор идёт, запись
  // принадлежит агенту, он её читает и уносит исходом, и правка человека либо
  // пропала бы под ним, либо сделала бы исход ответом не на тот текст.
  // Отпирает замок живое ожидание: агент спит в инструменте ожидания, файла не
  // трогает, и ответ правкой текста это законная дорога. Тем же правилом
  // отвечает и ручка, замок экрана тут не единственный сторож.
  const locked = running && !waiting;
  sync(groups, [{
    key: "draft-page",
    sign: [id, said, hash, running, locked, text.body.error || "", JSON.stringify(waiting)].join("|"),
    make: () => {
      const form = { text: said };
      const chips = [el("span", "chip", "черновик")];
      if (running) chips.push(el("span", "chip c-run", "груминг идёт"));
      // Разбор спросил и ждёт ответа: чип тот же, что у строки накопителя и у
      // строки доски. Кружка на кнопке чата тут больше нет, он дублировал этот
      // же чип (замечание пользователя).
      const waits = waitChip({ waiting });
      if (waits) chips.push(waits);
      const actions = [];
      // Вход в разговор тут один и стоит значком, как у строк задач: кнопка
      // «Чат груминга» вела в тот же самый чат, что и значок рядом, и две
      // двери в одну комнату человек читал как две разные (замечание
      // пользователя).
      if (running) {
        // Забрать запись у агента больше нечем, и стоп стоит тут же, в ряду
        // кнопок шапки: прежде под него уходила отдельная карточка во всю
        // полосу, а слова её дублировали чип «груминг идёт» (замечание
        // пользователя). Значок без подписи, как у чата и режима чтения рядом,
        // слово ушло в подсказку.
        const stop = withTip(el("button", "btn btn-danger btn-ico dstop"),
          "Стоп: " + STOP_TIP);
        stop.append(icon("i-stop"));
        stop.setAttribute("aria-label", "Стоп");
        stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
        actions.push(stop);
      } else {
        // Пока разбор идёт, поднять второй нечем: кнопка рядом с пометкой
        // «груминг идёт» звала запустить грумера поверх работающего.
        const groom = runControl(project, id,
          (label) => barBtn("btn btn-acc", label, "i-play"),
          "Грумить", false,
          text.ok && text.body.order ? "Заказ агенту: «" + text.body.order + "»." : "",
          "", "",
          // Несохранённая правка уезжает на диск до подъёма разбора: иначе
          // агент прочитал бы старый текст, и потеря была бы молчаливой.
          (harness, tier) => (form.text !== said
            ? saveDraftText(project, id, form.text, hash)
            : Promise.resolve(true))
            .then((ok) => ok && groomDraft(project, id, "", harness, tier))
            .then((ok) => {
              if (ok) refresh().catch(console.error);
              return ok;
            }),
          harnessTiers());
        actions.push(groom);
      }
      // Карточек исхода разбора на форме нет ни одной. Разговор с агентом у
      // нас всегда идёт в чате, и место исхода там же, а на доске он виден по
      // факту: строка появилась или нет (решение пользователя). Форма держит
      // саму запись и действия над ней, и больше ничего.
      return formPage({
        key: "draft", project, id,
        // Дорога на доску была только через накопитель, и с экрана записи её не
        // было вовсе.
        crumb: [
          { text: "Доска " + project, go: () => { goKeepingChat(project); } },
          { text: "Черновики", go: () => { goKeepingChat(project + "/drafts"); } },
        ],
        num: id,
        // Заголовок записи это её первая строка, и правят его в самом тексте.
        titleText: (said.split("\n").find((ln) => ln.trim()) || id).replace(/^#+\s*/, ""),
        // Пропавший файл это не поломка экрана, а след исхода: груминг мог
        // увести запись строкой, припиской или удалением.
        detail: { file: text.ok ? text.body.file || "" : "", text: said,
          note: text.ok ? "запись пуста" : text.body.error || "текст записи не прочитался" },
        form, chips, actions,
        has: { file: true, pencil: !locked, read: true, chat: true },
        penLabel: "Править запись",
        // Причина замка живёт подсказкой погашенного карандаша: пропавшая
        // кнопка читалась бы поломкой экрана, а карточки с этими словами на
        // экране больше нет.
        penOff: locked ? "Правка заперта: разбор идёт, запись у агента. "
          + "Вернуть её можно стопом." : "",
        edit: taskDraft.id === id && taskDraft.edit && !locked,
        onEdit: (on) => {
          taskDraft.id = id;
          taskDraft.edit = on;
        },
        check: () => {
          const dirty = form.text !== said;
          taskDraft.id = id;
          taskDraft.dirty = dirty;
          return { dirty, refusal: form.text.trim() ? "" : "пустой текст затёр бы запись черновика" };
        },
        onSave: () => { saveDraftText(project, id, form.text, hash).catch(console.error); },
        onDrop: () => {
          taskDraft.dirty = false;
          taskDraft.edit = false;
          // Отпечаток снимается руками: данные с сервера те же, и без этого
          // перерисовка оставила бы на экране прежний узел вместе с брошенной
          // правкой в поле.
          const shown = findKey(document.getElementById("groups"), "draft-page");
          if (shown) shown.dataset.psign = "";
          renderDraft(project, works, id).catch(console.error);
        },
      }).page;
    },
  }]);
}

// Экран заведения (#проект/new) по макету «07 Заведение»: форма одна на оба
// случая и повторяет правку задачи, те же поля в том же порядке и та же сумма
// ранга. Переключатель наверху меняет только то, куда ляжет написанное. В
// режиме черновика поля не прячутся, а гасятся с подписью, кто их заполнит:
// перестроенная форма скрывала бы от человека, чего черновик лишён.
const DRAFT_NOTE_HEAD = "Черновику доступен только груминг.";
const DRAFT_NOTE = "Задачи на доске у него нет, в работу его не взять: " +
  "ранг и тип выдаст разбор накопителя, он же заведёт задачу.";
// Подписей под формой заведения больше нет ни у черновика, ни у задачи. Они
// пересказывали устройство (куда ляжет файл, кто выдаст ID, что откроется
// после записи) и объясняли кнопки, чьи подписи и без того их называют
// (замечание пользователя). Что остаётся черновику, сказано пометкой сверху,
// и это единственная его особенность, которой не видно глазами.
const NEW_PLACEHOLDER = "Что нужно сделать и зачем";

// Черновик пишется по SCQA, и форма кладёт в поле готовый шаблон разделов:
// заголовок первой строкой, дальше подразделы третьего уровня с пустыми
// местами под текст. Полей на каждый раздел форма не заводит: «нужно было
// просто в поле редактирования вставить шаблон с разделами, а пользователь сам
// заполнит их, так гораздо гибче» (решение пользователя). Написанное в
// шаблоне человек волен править как угодно: снять раздел, добавить свой,
// писать сплошным текстом. Размеченный текст утилита записи кладёт как есть.
const DRAFT_PLACEHOLDER = "Заголовок-исход первой строкой, дальше разделы";
const DRAFT_TEMPLATE = ["", "", "### Ситуация", "", "", "### Осложнение", "",
  "", "### Вопрос", "", "", "### Гипотеза", ""].join("\n");
// Порог заголовка держит и утилита записи, но узнавать о нём после похода на
// сервер поздно: тот же рубеж стоит на самой форме.
const DRAFT_TITLE_LIMIT = 72;

// Рубеж формы черновика ровно тот же, что у утилиты записи: непустая первая
// строка и её длина. Разделов форма не спрашивает вовсе: человек правит текст
// руками, и отбивать запись за снятый раздел значило бы спорить с его же
// правкой (решение пользователя).
function draftFormRefusal(form) {
  const head = (String(form.title || "").split("\n", 1)[0] || "").trim();
  if (!head) return "черновик пустым не бывает: первой строкой идёт заголовок-исход";
  if (head.length > DRAFT_TITLE_LIMIT) {
    return "заголовок длиннее " + DRAFT_TITLE_LIMIT + " символов: по нему черновик узнают в накопителе";
  }
  return "";
}
// Вид приёмки выбирается закрытым списком, а не текстом: свободный ввод на
// телефоне дороже двух тапов, а значения всего три (DK-301).
const ACCEPT_VALUES = ["agent", "mixed", "user"];
// Шесть барьеров закрыты LLD DK-292 (решение 1): ключ едет в --barrier как
// есть. Первый пункт списка остаётся пустым: pickField выбирает первый пункт
// сам, и без пустого «выбрать барьер» неотправленная форма уезжала бы
// барьером «глаза», которого человек не называл.
const BARRIER_PLACEHOLDER = "выбрать барьер";
const BARRIER_VALUES = ["", "глаза", "доступ", "необратимость", "секрет", "согласие", "событие"];
// Уровень разбора спрашивается прямо на записи (DK-520): груминг заходит по
// накопителю сверху, и метка, поставленная им, появлялась бы тогда, когда
// разбор уже идёт. Значение по умолчанию средний: обычная очередь.
const PRIO_VALUES = ["high", "mid", "low"];
const PRIO_HINT = "уровень разбора на глаз: high разбирать ближайшим заходом, " +
  "mid обычная очередь, low когда-нибудь; потом его правит груминг";
const ACCEPT_HINT = "Вид приёмки решает, кто проверяет задачу: агентский вид " +
  "закрывается прогоном, у остальных часть шагов остаётся человеку.";
const ACCEPT_BARRIER_HINT = "Барьер называется из шести, и у каждого своя причина: " +
  "без названного барьера вид не поднимается.";
// Погашенные поля черновика подписаны тем, кто их заполнит.
const DRAFT_OFF_TYPE = "тип выдаст груминг";
const DRAFT_OFF_COST = "цена выдаст груминг";
const DRAFT_OFF_RANK = "ранг выведет груминг";
const DRAFT_OFF_PARTS = "поля те же, что у задачи, но пока не заполняются";

// Поля формы переживают перерисовку: доска перечитывается по фокусу окна, и
// без этого набранный текст пропадал бы при первом же переключении на другое
// окно. Форма одна на экран, как и черновик экрана задачи. Поле написанного
// тоже одно: у задачи это заголовок строки, у черновика текст записи, и
// переключатель их не теряет.
const newForm = { project: "", draft: false, title: "", type: "task", cost: "-",
  parts: [0, 0, 0, 0, 0], accept: "agent", barrier: "", reason: "", prio: "mid",
  seeded: false };

function resetNewForm(project) {
  newForm.project = project;
  newForm.draft = false;
  newForm.title = "";
  newForm.type = "task";
  newForm.cost = "-";
  newForm.parts = [0, 0, 0, 0, 0];
  newForm.accept = "agent";
  newForm.barrier = "";
  newForm.reason = "";
  newForm.prio = "mid";
  newForm.seeded = false;
}

// Форма набрана хоть чем-то: пустую закрываем молча, а над набранной сперва
// спрашиваем. Мера тут одна на оба вида, поля чужого вида стоят нетронутыми.
function newFormFilled() {
  const text = String(newForm.title || "");
  // Шаблон разделов сам по себе не написанное: форма с одним шаблоном
  // закрывается молча, как закрывалась пустая.
  if (newForm.draft) return text.trim() !== "" && text !== DRAFT_TEMPLATE;
  if (text.trim()) return true;
  return newForm.type !== "task" || newForm.cost !== "-" ||
    newForm.parts.some((n) => Number(n) !== 0) ||
    newForm.accept !== "agent" || Boolean(newForm.barrier) ||
    Boolean(String(newForm.reason || "").trim());
}

// Отправка гасит кнопки на время запроса: повторное нажатие на медленной
// связи заводило бы вторую строку тем же текстом, а откатить это нечем.
async function sendNew(btns, call) {
  for (const b of btns) b.disabled = true;
  try {
    return await call();
  } finally {
    for (const b of btns) b.disabled = false;
  }
}

// Заведение записи и заведение строки это одна ручка POST с разным адресом и
// разным телом: складывать их в одну функцию с флагом незачем, а переписывать
// поход на сервер дважды тем более.
async function makeNew(project, tail, body, btns, saying) {
  sayResult(saying);
  return sendNew(btns, async () => {
    const r = await api("/api/projects/" + encodeURIComponent(project) + tail,
      { method: "POST", body });
    sayResult(apiSaid(r), !r.ok);
    return r.ok ? r.body : null;
  });
}

function makeDraft(project, text, prio, btns) {
  return makeNew(project, "/drafts", { text, prio }, btns, "запись черновика...");
}

function makeTask(project, body, btns) {
  return makeNew(project, "/tasks", body, btns, "заведение задачи...");
}

// kind это вид заводимого: draft либо task. Форма собирается только своими
// полями, и переключателя над ней больше нет: вид выбран до неё, на своём
// экране.
function renderNew(project, kind) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  if (newForm.project !== project) resetNewForm(project);
  newForm.draft = kind === "draft";

  const draft = newForm.draft;
  // Шаблон разделов встаёт в поле один раз, при заходе на форму. Экран
  // перечитывается по фокусу окна, и класть шаблон каждой перерисовкой значило
  // бы возвращать его в очищенное рукой поле, споря с человеком.
  const seeded = draft && !newForm.seeded && !newForm.title.trim();
  if (draft && !newForm.seeded) {
    if (seeded) newForm.title = DRAFT_TEMPLATE;
    newForm.seeded = true;
  }
  // Шаблон принадлежит черновику: поле у обеих форм одно, и нетронутый шаблон,
  // уехав на форму задачи, встал бы заголовком строки. Написанное рукой при
  // этом переезжает, как переезжало и раньше.
  if (!draft && newForm.seeded && newForm.title === DRAFT_TEMPLATE) {
    newForm.title = "";
    newForm.seeded = false;
  }
  // Пометка про груминг стоит только у черновика и говорит сразу обе правды: и
  // чего у него нет, и кто это выдаст.
  const note = el("div", "dnote");
  note.append(el("b", "", DRAFT_NOTE_HEAD), document.createTextNode(" " + DRAFT_NOTE));

  // Уровень стоит у самого верха формы, рядом с пометкой про груминг: это
  // единственное, что черновик спрашивает сверх текста (DK-520).
  const prioBox = el("div", "accbox");
  const prioPick = pickField("уровень разбора", PRIO_VALUES, newForm.prio, (v) => {
    newForm.prio = v;
    view.touch();
  });
  prioPick.querySelector("select").setAttribute("aria-label", "уровень разбора записи накопителя");
  prioBox.append(prioPick);
  const prioHint = el("div", "hint", PRIO_HINT);

  // Вид приёмки, барьер и причина (DK-301): вид закрытым списком из трёх,
  // барьер из шести показывается только у не агентского вида, и причина без
  // него не пишется. Списки и на телефоне остаются нативными select: два тапа
  // это вся работа, которую тут можно сделать всерьёз.
  const card = el("div", "card");
  const box = el("div", "nfbody");
  card.append(box);
  const acceptBox = el("div", "accbox");
  const acceptPick = pickField("вид приёмки", ACCEPT_VALUES, newForm.accept, (v) => {
    newForm.accept = v;
    if (v === "agent") newForm.barrier = "";
    view.touch();
  });
  acceptPick.querySelector("select").setAttribute("aria-label", "вид приёмки задачи");
  acceptBox.append(acceptPick);
  const barrierPick = pickField("барьер", BARRIER_VALUES, newForm.barrier, (v) => {
    newForm.barrier = v;
    view.touch();
  });
  const barrierSel = barrierPick.querySelector("select");
  barrierSel.firstElementChild.textContent = BARRIER_PLACEHOLDER;
  barrierSel.setAttribute("aria-label", "барьер приёмки");
  acceptBox.append(barrierPick);
  const barrierHint = el("div", "hint", ACCEPT_BARRIER_HINT);
  const reasonField = el("div", "");
  reasonField.append(el("span", "flab", "Почему обход не годится"));
  const reason = el("input");
  reason.type = "text";
  reason.value = newForm.reason;
  reason.placeholder = "что мешает проверить агенту";
  reason.setAttribute("aria-label", "причина непригодности обхода");
  reason.addEventListener("input", () => { newForm.reason = reason.value; view.touch(); });
  reasonField.append(reason);
  // Подписи про бакет P на форме нет: самого бакета тут не показано, ставить
  // его нечем, и надпись отвечала на незаданный вопрос. Вид приёмки с барьером
  // остаётся подписан: он решает, кто будет проверять работу, и по одному
  // слову в списке этого не прочесть.
  box.append(acceptBox, el("div", "hint", ACCEPT_HINT), barrierHint, reasonField);

  let view = null;
  // Уход с формы. Заведение занимает экран целиком, и выйти с него было нечем,
  // ни крестика, ни отмены (замечание пользователя). Пустая форма закрывается
  // сразу, набранная спрашивает второй раз, как спрашивает снятие сессии:
  // написанное не должно пропадать от промаха по кнопке или по клавише.
  let quitArmed = false;
  const leaveNew = (btn) => {
    if (newFormFilled() && !quitArmed) {
      quitArmed = true;
      if (btn) {
        btn.classList.add("armed");
        btn.rename(draft ? "Черновик не записан, выйти?" : "Задача не заведена, выйти?");
      }
      return;
    }
    resetNewForm(project);
    // Возврат туда, откуда пришли: черновик заводят из накопителя, задачу с
    // доски.
    goKeepingChat(draft ? project + "/drafts" : project);
    refresh().catch(console.error);
  };
  // Обе кнопки записи ходят одной ручкой и расходятся только дорогой после
  // неё. «Сохранить» возвращает в накопитель, и следующая запись начинается
  // оттуда же, с плюса на списке. «Сохранить и грумить» поднимает разбор без
  // лишних вопросов и открывает экран записи, где виден его ход.
  const saveDraft = (groom) => {
    if (draftFormRefusal(newForm)) return;
    // Уезжает написанное как есть: разметку разделов утилита записи узнаёт
    // сама, а снятый или добавленный рукой раздел это дело автора.
    const text = newForm.title.trim();
    const btns = [view.save, view.saveMore].filter(Boolean);
    makeDraft(project, text, newForm.prio, btns).then(async (done) => {
      if (!done) return;
      resetNewForm(project);
      // Записанное ищут в накопителе, и метка ведёт туда глаз: свежая запись
      // оказывается не там, куда человек смотрит.
      freshRow = done.id || "";
      if (groom && done.id) {
        await groomDraft(project, done.id, project + "/draft/" + done.id);
        return;
      }
      goKeepingChat(project + "/drafts");
      await refresh();
    }).catch(console.error);
  };
  view = formPage({
    key: "new", project, id: "",
    // Крошки «Доска <проект>» у формы нет: она вела туда же, куда ведёт шапка
    // страницы, и дорога назад стояла на экране дважды (замечание
    // пользователя).
    // У черновика на экране только он сам: ни карточки приёмки, ни полей
    // строки доски. У задачи наоборот, ни слова про груминг.
    lead: draft ? [note, prioBox, prioHint] : [], extra: draft ? [] : [card],
    // Форма заведения это та же правка задачи с пустыми полями: правка тут
    // включена всегда, и выключать её нечем, экран для неё и открыт.
    has: draft ? { title: true } : { title: true, type: true, cost: true, rank: true },
    titleHint: draft ? DRAFT_PLACEHOLDER : NEW_PLACEHOLDER, titleTall: true,
    titleLabel: draft ? "текст черновика" : "заголовок задачи",
    form: newForm, edit: true, always: true,
    saveLabel: draft ? "Сохранить" : "Завести задачу",
    // У черновика кнопок сохранения две, и расходятся они только дорогой
    // после записи (LLD DK-354, решение 5). Промежуточной карточки с
    // «Записать ещё» и «На доску» между ними нет: обе её дороги закрыты
    // возвратами самих кнопок.
    saveMore: draft ? { label: "Сохранить и грумить", onSave: () => { saveDraft(true); } } : null,
    quitLabel: draft ? "Не записывать" : "Не заводить",
    onQuit: (btn) => { leaveNew(btn); },
    actions: [],
    check: () => {
      if (view) paint();
      // Рубежи те же, что у ручек: поправка на баг не про новую работу, строки
      // без заголовка и черновика без текста не бывает, а у не агентского вида
      // барьер обязателен.
      if (newForm.draft) {
        return { dirty: true, refusal: draftFormRefusal(newForm) };
      }
      return { dirty: true, refusal: draftRefusal(newForm, null) ||
        (newForm.accept !== "agent" && !newForm.barrier
          ? "у не агентского вида назван барьер из шести: без него приёмка повисает без причины"
          : "") };
    },
    onSave: () => {
      if (newForm.draft) {
        saveDraft(false);
        return;
      }
      const text = newForm.title.trim();
      if (!text) return;
      const send = view.save;
      const body = {
        title: text,
        type: newForm.type,
        cost: newForm.cost,
        r_parts: newForm.parts.map(Number),
        accept: newForm.accept,
      };
      if (newForm.accept !== "agent") {
        body.barrier = newForm.barrier;
        body.reason = newForm.reason.trim();
      }
      makeTask(project, body, [send]).then(async (done) => {
        if (!done) return;
        resetNewForm(project);
        // Заведённая строка открывается сразу: с телефона следующий шаг это
        // дописать постановку, а искать её глазами по Backlog неудобно. На
        // доске она при этом уже стоит помеченной: возврат туда рисует список
        // свежим ответом, а не тем, что дашборд помнил до заведения.
        freshRow = done.id || "";
        if (!done.id) {
          renderNew(project);
          return;
        }
        goKeepingChat(project + "/" + done.id);
        // Перерисовка идёт своим ходом, а не событием адреса: адрес меняется не
        // всегда (заводили со своего же экрана), и тогда события не приходит
        // вовсе, а экран остаётся с прежними данными (замечание пользователя
        // про «приходится обновлять страницу»).
        await refresh();
      }).catch(console.error);
    },
  });
  groups.append(view.page);

  // Форма собрана под свой вид, и переключать в ней нечего: полей чужого вида
  // на ней нет вовсе. Обход тут остаётся ради одного живого поля, барьера: он
  // виден только у не агентской приёмки.
  function paint() {
    if (draft) return;
    const bare = newForm.accept === "agent";
    barrierPick.hidden = bare;
    barrierHint.hidden = bare;
    reasonField.hidden = bare;
    view.rankSum.textContent = String(newForm.parts.reduce((a, b) => a + Number(b), 0));
    view.rankNote.textContent = "= " + newForm.parts.join("+");
  }

  // Курсор встаёт на первую строку шаблона: с заголовка человек и начинает, а
  // ниже его ждут готовые разделы. Ставится он только вместе со свежим
  // шаблоном: перерисовка по фокусу окна не должна выдёргивать курсор из того
  // раздела, где человек пишет.
  if (seeded && view.title && view.title.focus) {
    view.title.focus();
    if (view.title.setSelectionRange) view.title.setSelectionRange(0, 0);
  }

  // Escape уводит с формы той же дорогой, что и кнопка: руки на клавиатуре, и
  // тянуться к кнопке ради отказа не надо. Вопрос над набранной формой при
  // этом виден глазами, потому что взводится та же кнопка.
  newEscape = () => { leaveNew(view.quit); };

  view.touch();
}

// Escape на форме заведения: обработчик один на страницу, а форма подставляет
// в него свою дорогу. Живёт он ровно пока открыт экран заведения: с чужого
// экрана клавиша не должна уводить никуда.
let newEscape = null;

function wireNewKey() {
  document.addEventListener("keydown", (ev) => {
    // Всплывашка гасится той же клавишей, и пока она открыта, Escape её и
    // гасит: уводить с экрана заодно с ней значило бы терять форму на
    // случайном промахе.
    if (ev.key !== "Escape" || popupsOpen.size) return;
    if (!route().make || !newEscape) return;
    if (ev.preventDefault) ev.preventDefault();
    newEscape();
  });
}

// Экран ленты уведомлений по макету DK-216 («05 Лента»): три типа событий DoD
// (стоп работы, зов человека, завершение задачи), фильтры по типам,
// группировка по дням и действие у стопа. События сервер берёт из журнала
// уведомителя ~/.devkit/notify.log, живут они на SSE: стоп, случившийся при
// открытом экране, доезжает без перезагрузки страницы.
const FEED_FILTERS = [
  { kind: "", name: "Все" },
  { kind: "stop", name: "Остановки" },
  { kind: "wait", name: "Ожидание пользователя" },
  { kind: "task", name: "Задачи" },
];
const FEED_ICONS = { stop: "i-stop", wait: "i-wait", task: "i-done" };

// Значок берётся копией из разметки страницы (макет «05 Лента»): фигуры
// событий и колокольчика собирались рамками стилей и читались обрубками, а
// внешних картинок и шрифта значков у статики нет.
function icon(name) {
  const tpl = document.getElementById("icons");
  const node = tpl && tpl.content.querySelector('[data-ico="' + name + '"]');
  return node ? node.cloneNode(true) : el("i");
}
const MONTHS = ["января", "февраля", "марта", "апреля", "мая", "июня",
  "июля", "августа", "сентября", "октября", "ноября", "декабря"];

function isoDay(d) {
  return d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") +
    "-" + String(d.getDate()).padStart(2, "0");
}

// Заголовок дня: уведомитель пишет местное время, поэтому сегодня и вчера
// считаются местным же календарём, а не сдвигом в UTC.
function dayLabel(day) {
  const parts = day.split("-");
  if (parts.length !== 3) return day || "без даты";
  const human = Number(parts[2]) + " " + (MONTHS[Number(parts[1]) - 1] || "");
  const now = new Date();
  if (day === isoDay(now)) return "Сегодня, " + human;
  if (day === isoDay(new Date(now.getTime() - 86400000))) return "Вчера, " + human;
  return human + " " + parts[0];
}

function feedItemEl(project, n) {
  const item = el("div", "nitem");
  const kind = FEED_ICONS[n.kind] || "i-other";
  const ico = el("div", "nico " + kind);
  ico.append(icon(kind));
  item.append(ico);
  const b = el("div", "nb2");
  b.append(withFull(el("div", "t1", n.title), n.title));
  if (n.body) b.append(el("div", "t2", n.body));
  // Про недошедший баннер человеку в списке событий сказать нечего: он читает
  // список, а не разбирает канал доставки, и строка «фокус не определился,
  // зовём» была для него служебным шумом. Причина осталась в журнале
  // уведомителя, где её и разбирают (замечание 3 девятого круга POC).
  // Задачу и проект событие несёт своими полями (DK-323), и переход идёт по
  // ним: работа поднимается там, где событие случилось, а не в открытом на
  // экране проекте. Поля у события нет только у старых строк журнала, и тогда
  // проектом остаётся открытый.
  const to = n.project || project;
  if (n.id) {
    // Номер задачи виден прямо в строке события: сервер тянет его из реестра
    // чатов, когда само событие задачу не назвало (замечание 5). Нажатие ведёт
    // на строку доски.
    const tag = el("span", "nid", n.id);
    tag.addEventListener("click", (ev) => {
      ev.stopPropagation();
      goKeepingChat(to + "/" + n.id);
    });
    b.append(tag);
    const acts = el("div", "acts");
    if (n.kind === "stop") {
      const up = el("button", "btn btn-acc", "Поднять виток");
      up.addEventListener("click", () => { startRun(to, n.id).catch(console.error); });
      acts.append(up);
    }
    const open = el("button", "btn", "Открыть " + n.id);
    open.addEventListener("click", () => { goKeepingChat(to + "/" + n.id); });
    const jrn = el("a", "", "Чат агента");
    jrn.href = "#" + taskChatHash(to, n.id);
    acts.append(open, jrn);
    b.append(acts);
  } else {
    // Событие без задачи бывает честно: самопроверка канала, авария контура,
    // строка журнала старше полей. Объяснять это каждой карточкой не надо,
    // отсутствие кнопки говорит само; печатается только спорная привязка,
    // которую сервер назвал словами (под обрезанный ID сессии попали две
    // записи реестра, и выбирать между ними наугад нельзя).
    if (n.note) b.append(el("div", "t2", n.note));
  }
  // Событие про ждущий чат ведёт прямо в него: до этого «ждёт ввода» стояло
  // строкой без выхода, и человек искал чат руками (замечание 19).
  if (n.chat) {
    const acts = el("div", "acts");
    const go = el("button", "btn", "Открыть чат");
    go.addEventListener("click", () => { openChat(chatAddr(to, n.chat)); });
    acts.append(go);
    b.append(acts);
  }
  item.append(b);
  item.append(el("div", "ntime", (n.time || "").slice(11, 16)));
  return item;
}

// Свежие сверху, дни отдельными карточками: так лента читается сверху вниз,
// как её и рисует макет.
function renderFeedItems(box, project, items, note) {
  if (!items.length) {
    say(box, "empty", note || "уведомлений нет");
    return;
  }
  box.replaceChildren();
  let day = "";
  let card = null;
  for (const n of items) {
    const d = (n.time || "").slice(0, 10);
    if (d !== day || !card) {
      day = d;
      card = el("div", "card");
      box.append(el("div", "nday", dayLabel(d)), card);
    }
    card.append(feedItemEl(project, n));
  }
}

// Точка на колокольчике вместо счётчика непрочитанного (решение DK-246):
// отметок прочитанного у сервера нет и не будет, поэтому «новое» считается по
// времени последнего захода на ленту, а держит его сам браузер. Из двух
// честных вариантов это тот, который не врёт числом: точка говорит, что после
// прошлого захода события были, и не притворяется, что знает, сколько их
// человек прочитал.
const FEED_SEEN_KEY = "devkit.feed.seen";

function feedSeen() {
  try {
    return localStorage.getItem(FEED_SEEN_KEY) || "";
  } catch (err) {
    // Приватное окно запрещает хранилище: точка тогда просто не загорается,
    // ломать из-за этого экран не за что.
    return "";
  }
}

function markFeedSeen(time) {
  if (!time || time <= feedSeen()) return;
  try {
    localStorage.setItem(FEED_SEEN_KEY, time);
  } catch (err) {
    return;
  }
  showBellDot(false);
}

function showBellDot(on) {
  document.getElementById("bell-dot").hidden = !on;
}

// Время в журнале уведомителя местное и без зоны, поэтому и заход считается
// местными часами браузера. Дашборд обычно смотрят с той же машины, а на чужой
// с уехавшими часами точка ошибётся в одну сторону: покажет лишнее или
// смолчит до следующего события.
function nowStamp() {
  const d = new Date();
  const p = (v) => String(v).padStart(2, "0");
  return isoDay(d) + "T" + p(d.getHours()) + ":" + p(d.getMinutes()) + ":" +
    p(d.getSeconds());
}

// Колокольчик знает про новое, не открывая ленту: обычный (не потоковый)
// ответ ленты стоит одного чтения журнала, и ходит он тем же кругом, что и
// обновление доски, то есть по фокусу окна и смене экрана.
async function refreshBellDot() {
  const r = await api("/api/notifications");
  const items = (r.body && r.body.items) || [];
  const last = items.length ? items[items.length - 1].time || "" : "";
  const seen = feedSeen();
  // Первый заход в браузере: точка не загорается на всей прошлой истории
  // журнала, отсчёт начинается с этой минуты.
  if (!seen) {
    markFeedSeen(nowStamp());
    return;
  }
  showBellDot(Boolean(last) && last > seen);
}

// Полка ждущих (DK-696): все задачи и разговоры машины, вставшие на вопросе
// человеку, одним местом в шапке. Прежде ждущая строка была видна только чипом
// в Blocked, и после парковки человек искал её глазами среди семи соседей, а
// свёрнутая секция добавляла к дороге ещё один щелчок. Полка стоит вне секции и
// открывается с любого экрана, потому что шапка одна на все.
const WAIT_POLL = 15000;

// Список приезжает машинной ручкой и живёт до следующего захода: полку
// открывают нажатием, и собирать её ответом сети значило бы показывать пустое
// место, пока идёт запрос.
let waitList = { items: [], errors: [] };

async function refreshWaits() {
  const r = await api("/api/waiting");
  const body = (r.ok && r.body) || {};
  waitList = { items: body.items || [], errors: body.errors || [] };
  paintWaitBadge();
  // Открытая полка обновляется на месте: закрывать её под рукой человека
  // значило бы отбирать список ровно тогда, когда он его читает.
  if (waitShelf) fillWaitShelf(waitShelf);
}

// Число ждущих клеймом на кнопке. Кнопка стоит всегда, а не появляется с первым
// вопросом: место, приходящее и уходящее, человек не запоминает, и искать его
// пришлось бы заново каждый раз.
function paintWaitBadge() {
  const btn = document.getElementById("waits");
  if (!btn) return;
  const n = waitList.items.length;
  const dot = document.getElementById("waits-n");
  if (dot) {
    dot.textContent = String(n);
    dot.hidden = !n;
  }
  btn.classList.toggle("wsome", Boolean(n));
  btn.title = n ? n + " " + plural(n, "ждёт", "ждут", "ждут") + " ответа" : "Никто не ждёт ответа";
  btn.setAttribute("aria-label", btn.title);
}

// Слова строки полки: состояние и сколько уже ждут. Отсчёт до срока тут не
// повторяется, его говорит чип самой строки доски; полке важно обратное, как
// давно человек молчит.
function waitAgeWords(w, now) {
  const age = pulseAge(w.since, now);
  return age ? age + " без ответа" : "";
}

let waitShelf = null;
let waitShelfHeld = null;

function waitShelfShut() {
  popupDrop(waitShelfHeld);
  waitShelfHeld = null;
  if (waitShelf) {
    waitShelf.remove();
    waitShelf = null;
  }
}

// Строка полки ведёт в разговор ждущего: адрес выбрал сервер (сессия точнее
// задачи), а проект едет в самом адресе, потому что полка машинная и открывают
// её и с главной, где своего проекта нет вовсе. Парковка это исключение: у неё
// нет ни сессии, ни живого разговора, вопрос лежит в причине блока самой
// строки, и щелчок ведёт туда же, а не в чат, где вопроса нет и быть не может
// (замечание человека по приёмке DK-696, 2026-09-05: DK-565).
function waitRow(it, now) {
  const row = el("div", "wsrow");
  const head = el("div", "wshrow");
  head.append(el("span", "chip c-proj", it.project));
  if (it.id) head.append(el("b", "", it.id));
  const w = it.waiting || {};
  head.append(el("span", "chip c-wait", w.state || "ждёт ответа"));
  const age = waitAgeWords(w, now);
  if (age) head.append(el("span", "wsage", age));
  row.append(head);
  if (it.title) row.append(withFull(el("div", "wstitle", it.title), it.title));
  // Вопрос стоит в самой строке, а не подсказкой: за ним сюда и приходят, и
  // читать его наведением значило бы открывать разговор ради одной фразы.
  const qs = w.questions || [];
  if (qs.length) row.append(el("div", "wsq", qs.join("; ")));
  row.append(el("div", "wsnote", w.note || "источник не назван"));
  row.addEventListener("click", () => {
    waitShelfShut();
    if (w.source === "parked" && it.id) {
      location.hash = it.project + "/" + it.id;
      return;
    }
    const at = shownProject || route().proj;
    openChat(it.project && it.project !== at ? it.project + CHAT_PROJ_SEP + it.addr : it.addr);
  });
  return row;
}

function fillWaitShelf(box) {
  box.replaceChildren();
  const now = Date.now();
  const head = el("div", "wshead", "Ждут ответа");
  head.append(el("span", "n", String(waitList.items.length)));
  box.append(head);
  const rows = el("div", "wsrows");
  for (const it of waitList.items) rows.append(waitRow(it, now));
  box.append(rows);
  if (!waitList.items.length) rows.append(el("div", "hint", "никто не ждёт ответа"));
  // Отказ доски виден прямо тут: пустая полка при нечитаемой доске это не
  // «никто не ждёт», и молчать об этом значит врать спокойным видом.
  for (const why of waitList.errors) box.append(el("div", "hint", "доска не прочиталась, " + why));
}

function waitShelfOpen(btn, host) {
  const had = waitShelf;
  waitShelfShut();
  // Повторное нажатие по кнопке закрывает полку, а не собирает её заново.
  if (had) return;
  popupsShut(null);
  const box = el("div", "wshelf");
  fillWaitShelf(box);
  (host || btn.parentNode).append(box);
  waitShelf = box;
  waitShelfHeld = popupHold(box, waitShelfShut);
  // Список перечитывается открытием: между заходами опроса вопрос успевает и
  // прийти, и уйти, а человек смотрит на полку ровно затем, чтобы узнать, как
  // дела сейчас.
  refreshWaits().catch(console.error);
}

// Флеш-уведомление в углу окна (макет «05 Лента»): новое событие всплывает
// поверх любого экрана и гаснет само, полоска под текстом показывает остаток
// времени. Крестик и смахивание пальцем убирают карточку на месте, не трогая
// открытый экран, тап по телу ведёт в ленту. Копится не больше трёх,
// последнее сверху; на узком экране видна только верхняя (см.
// `.flash:not(:first-child)` в style.css), три подряд там закрывают собой
// весь рабочий экран.
const FLASH_LIFE = 8000;
const FLASH_MAX = 3;

// Момент подключения к потоку: хвост журнала, который сервер отдаёт сразу,
// всплывать не должен. Флеш это про то, что случилось при открытом окне.
let flashSince = "";

// Смахивание отличают от дрожания пальца при тапе пройденным расстоянием:
// короткий сдвиг остаётся тапом и ведёт в ленту, дальний (48 пикселей,
// заметно больше случайного дрожания) закрывает карточку без перехода.
function flashSwiped(dx) {
  return Math.abs(dx) >= 48;
}

// Карточка поверх экрана: общий каркас всплывающего события ленты и ответа на
// нажатие. Контейнер `.flashes` вынесен из потока документа (`position:fixed`
// в style.css), поэтому ни появление карточки, ни её уход на экране ничего не
// двигают. У двух случаев общий ровно каркас: карточка, крестик, полоска
// остатка времени с таймером и потолок числа карточек; смахивание пальцем и
// переход в ленту остаются событию, ответ на нажатие никуда не ведёт. Жизнь в
// ноль означает карточку без таймера: она ждёт крестика, и так живёт отказ.
function toast(spec) {
  const box = document.getElementById("flashes");
  if (!box) return null;
  const card = el("div", "flash" + (spec.cls ? " " + spec.cls : ""));
  const close = el("button", "nx");
  close.setAttribute("aria-label", "Закрыть");
  close.title = "Закрыть";
  close.append(icon("close"));
  let timer = 0;
  const dismiss = () => {
    clearTimeout(timer);
    card.remove();
  };
  if (spec.life) {
    const bar = el("div", "flife");
    bar.style.animationDuration = spec.life + "ms";
    spec.body.append(bar);
    timer = setTimeout(dismiss, spec.life);
  }
  close.addEventListener("click", (ev) => {
    ev.stopPropagation();
    dismiss();
  });
  card.append(...spec.parts, close);
  box.prepend(card);
  while (box.childElementCount > FLASH_MAX) box.lastElementChild.remove();
  return { card, dismiss };
}

function showFlash(n) {
  const dot = el("span", "pdot " + (n.kind === "task" ? "pd-run" : "pd-warn"));
  const body = el("div", "ft");
  body.append(el("b", "", n.title || "событие ленты"));
  if (n.body) body.append(el("span", "", n.body));
  const shown = toast({
    parts: [dot, body, el("span", "fw", "сейчас")],
    body,
    life: FLASH_LIFE,
  });
  if (!shown) return;
  const { card, dismiss } = shown;
  // Смахивание работает через pointer-события: они покрывают и тач, и мышь
  // одним обработчиком, второй код на touchstart/touchmove не нужен.
  let startX = null;
  let dx = 0;
  card.addEventListener("pointerdown", (ev) => {
    startX = ev.clientX;
    dx = 0;
    card.classList.add("dragging");
    card.setPointerCapture(ev.pointerId);
  });
  card.addEventListener("pointermove", (ev) => {
    if (startX === null) return;
    dx = ev.clientX - startX;
    card.style.transform = "translateX(" + dx + "px)";
    card.style.opacity = String(Math.max(1 - Math.abs(dx) / 260, 0.2));
  });
  const release = () => {
    if (startX === null) return;
    startX = null;
    card.classList.remove("dragging");
    if (flashSwiped(dx)) {
      dismiss();
      return;
    }
    card.style.transform = "";
    card.style.opacity = "";
  };
  card.addEventListener("pointerup", release);
  card.addEventListener("pointercancel", release);
  card.addEventListener("click", () => {
    // Смахивание не должно попутно уводить в ленту: клик проверяет тот же
    // пройденный путь, что и release.
    if (flashSwiped(dx)) return;
    dismiss();
    goKeepingChat((shownProject || route().proj) + "/feed");
  });
}

// Всплывать ли уведомлению: событие старше подключения это хвост журнала,
// который сервер отдаёт сразу, и всплывать ему незачем; на открытой ленте
// событие и так дописывается строкой, второй раз его показывать не надо.
// Какой разговор сейчас открыт в панели: по нему уведомления молчат. Событие
// про диалог, который человек и так читает, всплывать баннером не должно, он
// смотрит прямо на него (замечание 4 шестого круга POC).
let chatShown = { project: "", sid: "", task: "" };

// Уведомление про открытый разговор: своя сессия либо своя задача в том же
// проекте. Про чужую работу баннер остаётся, там человек ничего не видит.
function flashMuted(n) {
  if (!n || !chatShown.project) return false;
  if (n.project && n.project !== chatShown.project) return false;
  if (chatShown.sid && n.session && n.session === chatShown.sid) return true;
  return Boolean(chatShown.task && n.id && n.id === chatShown.task);
}

// Карточка это второй показ уже доставленного баннера: уведомитель сам решает,
// чему быть громким (поле level), и отмечает, дошёл ли баннер до человека
// (поле sent), поэтому карточкой всплывает только доставленное громкое
// событие. Фоновые поводы и повторы, задушенные дросселем, остаются в ленте
// строками: при живом конвейере их набирается до трёх на один законченный
// ход, и столбик одинаковых карточек закрывал собой рабочий экран.
function flashWorthy(n, since, onFeed) {
  return Boolean(n && n.time) && n.time > since && !onFeed && !flashMuted(n)
    && n.sent && n.level === "громкий";
}

// Событие уведомителя это и повод перечитать открытый список задач: статус
// строки двигает агент у себя, и до списка это доезжало только по фокусу окна,
// то есть после ухода из окна и обратно. Постоянного опроса при этом не
// заводится, ход идёт на пришедшее событие своего проекта.
function boardEcho(n) {
  const rt = route();
  if (!rt.proj || rt.id || rt.feed || rt.drafts || rt.make) return;
  if (n && n.project && n.project !== rt.proj) return;
  refresh().catch(console.error);
}

// Поток флеша живёт отдельно от экранов: он не закрывается при переходах,
// иначе уведомление приходило бы только на ленте, где оно и так видно строкой.
function wireFlash() {
  flashSince = nowStamp();
  const es = new EventSource("/api/notifications?stream=1");
  es.onmessage = (ev) => {
    let n;
    try {
      n = JSON.parse(ev.data);
    } catch (err) {
      return;
    }
    // Хвост журнала, который сервер отдаёт при подключении, доску не дёргает:
    // перечитывать её на каждое вчерашнее событие значило бы гонять taskctl
    // пачкой на ровном месте.
    if (Boolean(n && n.time) && n.time > flashSince) boardEcho(n);
    if (!Boolean(n && n.time) || n.time <= flashSince) return;
    // Отбор карточки идёт до движения курсора потока: курсор и точка
    // колокольчика идут любым свежим событием, а не только всплывшим, иначе
    // фоновые поводы и повторы, задушенные дросселем, выпадали бы и из
    // колокольчика, оставаясь строками самой ленты.
    const worthy = flashWorthy(n, flashSince, route().feed);
    flashSince = n.time;
    // Точка на колокольчике загорается тем же событием: ждать фокуса окна,
    // чтобы узнать о случившемся при открытом окне, было бы странно.
    showBellDot(n.time > feedSeen());
    if (!worthy) return;
    showFlash(n);
  };
}

function renderFeed(project) {
  // Заход на ленту гасит точку: всё, что было до этой минуты, человек видит
  // прямо сейчас.
  markFeedSeen(nowStamp());
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  // Заголовок не пересказывает состав ленты: что в неё попадает, говорит
  // значок информации по наведению.
  const head = el("div", "nhead");
  head.append(el("h2", "", "Уведомления"));
  const info = el("span", "tipwrap");
  const knob = el("span", "tipq", "i");
  knob.setAttribute("aria-label", "Что попадает в ленту");
  knob.addEventListener("click", () => { info.classList.toggle("on"); });
  info.append(knob, el("div", "tipbox", "На этом экране отображаются все уведомления от агентов"));
  head.append(info);
  const chips = el("div", "filters");
  const list = el("div", "ngroups");
  groups.append(head, chips, list);
  groups.append(el("div", "chan",
    "Лента машинная: в ней события всех досок сразу, а действие у события ведёт в тот проект, " +
    "где оно случилось."));

  let kind = "";
  let items = [];
  const wire = () => {
    // Отбор держит сервер, экран ходит тем же параметром, что и smoke; поток
    // прежнего фильтра закрывается, иначе соединения копились бы на каждом
    // нажатии.
    closeAgentLive();
    items = [];
    say(list, "empty", "лента читается...");
    const es = new EventSource("/api/notifications?stream=1" + (kind ? "&kind=" + kind : ""));
    agentLive.push(() => es.close());
    let pending = 0;
    const redraw = () => {
      if (pending) return;
      pending = setTimeout(() => { pending = 0; renderFeedItems(list, project, items); }, 0);
    };
    es.addEventListener("note", (ev) => {
      items = [];
      renderFeedItems(list, project, items, ev.data);
    });
    es.onmessage = (ev) => {
      let n;
      try {
        n = JSON.parse(ev.data);
      } catch (err) {
        return;
      }
      items.unshift(n);
      // Событие пришло на открытую ленту: оно уже прочитано, и точка на
      // колокольчике по нему не загорается.
      markFeedSeen(n.time);
      redraw();
    };
  };
  for (const f of FEED_FILTERS) {
    const chip = el("span", "fchip" + (f.kind === kind ? " on" : ""), f.name);
    chip.addEventListener("click", () => {
      kind = f.kind;
      for (const c of chips.children) c.classList.remove("on");
      chip.classList.add("on");
      wire();
    });
    chips.append(chip);
  }
  wire();
}

// Главная это список проектов, и на неё уводит кнопка «На главную» с любого
// экрана. На ноутбуке проекты стоят и в боковой колонке, на телефоне колонки
// нет, а барабан выбора виден только на доске, поэтому свой экран у списка
// нужен обоим форм-факторам.
// Легенда кружков: на ноутбуке всплывает по знаку у заголовка, на телефоне
// наведения нет, и тот же знак разворачивает её нажатием.
const DOT_LEGEND = [
  ["", "нет активных задач"],
  ["pd-run", "идёт работа агентов"],
  ["pd-warn", "требуется внимание, задача приостановлена или ожидает пользователя"],
];

function dotLegend() {
  const wrap = el("span", "tipwrap");
  const knob = el("span", "tipq", "?");
  knob.setAttribute("aria-label", "Статусы индикатора");
  const box = el("div", "tipbox");
  box.append(el("b", "", "Статусы индикатора"));
  for (const [cls, why] of DOT_LEGEND) {
    const line = el("div", "tipl");
    line.append(el("span", "pdot" + (cls ? " " + cls : "")), document.createTextNode(why));
    box.append(line);
  }
  knob.addEventListener("click", () => { wrap.classList.toggle("on"); });
  wrap.append(knob, box);
  return wrap;
}

// Причина словами рядом с кружком: что именно идёт и почему проект ждёт
// человека. У тихого проекта её нет.
function projectWhy(p) {
  const works = p.works || [];
  if (works.length) {
    const ids = works.map((w) => w.id || "интерактивная сессия").join(", ");
    return works.length + " " + plural(works.length, "задача", "задачи", "задач") +
      " в работе: " + ids;
  }
  const check = (p.sections && p.sections.check) || 0;
  if (check) {
    return "ждёт проверки: " + check + " " +
      plural(check, "задача", "задачи", "задач") + " в Check";
  }
  return "";
}

// Плюс у карточки проекта: заведение принадлежит проекту, и на общей странице
// ему место у самой карточки, а не полосой кнопок внизу. Полоса называла один
// проект из списка, и завести задачу в соседний с главной было нечем
// (замечание пользователя). Дорог за плюсом две, задача и черновик, поэтому он
// открывает меню, а не ведёт сразу.
let homeMenu = null;
let homeMenuHeld = null;

function homeMenuShut() {
  popupDrop(homeMenuHeld);
  homeMenuHeld = null;
  if (homeMenu) {
    homeMenu.remove();
    homeMenu = null;
  }
}

// Меню заведения: два пункта, черновик и задача, и выбор ведёт сразу в свою
// форму. Стоит оно у кнопки заведения везде, где та есть: у плюса карточки на
// главной и у кнопки на доске. Промежуточного экрана выбора тут нет: он стоил
// человеку лишнего перехода там, где хватает выпадашки (решение пользователя).
// Закрывается меню теми же тремя путями, что остальные всплывашки дашборда:
// повторным нажатием, кликом мимо и Escape.
// host это узел, внутри которого ляжет меню: по умолчанию сосед кнопки, а
// кнопке шапки меню кладётся в неё саму, потому что место всплывашке задаёт
// ближайший предок с position, и у ряда значков шапки такого предка нет.
function makeMenuAt(btn, project, host) {
  const had = homeMenu;
  homeMenuShut();
  // Повторное нажатие по той же кнопке закрывает меню, а не собирает его
  // заново под пальцем.
  if (had && had.dataset.project === project) return;
  // Соседняя всплывашка уходит с открытием этой: два раскрытых списка разом
  // экран не показывает ни в одном месте дашборда.
  popupsShut(null);
  const menu = el("div", "pmenu");
  menu.dataset.project = project;
  for (const [label, draft] of [["Черновик", true], ["Задача", false]]) {
    const opt = el("div", "pmrow", label);
    opt.addEventListener("click", (e) => {
      e.stopPropagation();
      homeMenuShut();
      resetNewForm(project);
      newForm.draft = draft;
      goKeepingChat(project + "/new/" + (draft ? "draft" : "task"));
    });
    menu.append(opt);
  }
  (host || btn.parentNode).append(menu);
  homeMenu = menu;
  homeMenuHeld = popupHold(menu, homeMenuShut);
}

function makePlus(project) {
  const btn = el("button", "pplus", "+");
  btn.type = "button";
  btn.title = "Завести в " + project;
  btn.setAttribute("aria-label", "Завести в " + project);
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    makeMenuAt(btn, project);
  });
  return btn;
}

function renderHome(projects) {
  homeMenuShut();
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  // Легенда стоит у заголовка шапки, а не отдельной строкой списка: заголовок
  // экрана шапка и рисует.
  document.getElementById("hlegend").replaceChildren(dotLegend());
  const card = el("div", "card");
  if (!projects.length) card.append(el("div", "empty", "Проектов нет."));
  for (const p of projects) {
    const row = el("div", "prow");
    const st = projectState(p);
    row.append(el("span", "pdot" + (st.cls ? " " + st.cls : "")));
    row.append(el("b", "", p.name));
    const total = Object.values(p.sections || {}).reduce((a, b) => a + b, 0);
    if (p.prefix) {
      row.append(el("span", "chip", p.prefix + ", " + total + " " +
        plural(total, "задача", "задачи", "задач")));
    }
    const why = projectWhy(p);
    if (why) row.append(el("span", "stale", why));
    row.append(makePlus(p.name));
    row.addEventListener("click", () => { goKeepingChat(p.name); });
    card.append(row);
  }
  groups.append(card);
  const quota = el("div", "card qcard squota");
  quota.id = "quota-card";
  groups.append(quota);
  paintQuota();
}

// Экран «Агенты» (макет «08 Агенты»): живые работы всех проектов одним
// списком. Своей ручки у экрана нет: works приходят в ответе /api/projects
// вместе со списком проектов, и второго похода на сервер экран не стоит.
// Вид работы словами: чипы отвечают, кто её ведёт и чем она видна. Цель
// названа целью и в интерактивном окне, потому что переписка открыта ровно у
// неё.
// Чипы строки сессии: только то, чего не говорит ничто другое в той же
// строке. Прежде их было шесть, и разбор с человеком снёс пять: проект
// повторял таб доски, «агент цели» повторял название работы, «интерактивная
// сессия» не значила ничего (headless-заходов в списке и так не видно),
// «разговор о задаче» ушёл словом к номеру задачи, а состояние несут кружок и
// давность. Осталось то, ради чего на таб и заходят: чем работа идёт
// (подписка и модель) и почему у неё нет кнопки закрытия.
function workChips(w) {
  const chips = [];
  // Внешняя сессия это единственный намёк на то, почему у строки нет кнопки:
  // слова тут те же, что в хвосте строки, чтобы одно и то же не объяснялось
  // двумя разными способами (решение пользователя).
  if (!agentOwn(w)) {
    chips.push(withTip(el("span", "chip", "внешняя"), WORK_FOREIGN_TIP));
  }
  // Чем работа идёт: подписка и модель. Ради этого на таб и заходят, когда на
  // машине две подписки: работа на glm и работа на подписке по умолчанию
  // выглядели одинаково, а платятся разной квотой (замечание пользователя).
  // Подписка стоит только там, где она известна честно: у окна, поднятого мимо
  // дашборда в чужом доме, её взять неоткуда, и тогда остаётся одна модель.
  if (w.harness) {
    chips.push(withTip(el("span", "chip", w.harness),
      "подписка, чьей квотой платится работа: узнана по дому её журнала"));
  }
  if (w.model) {
    chips.push(withTip(el("span", "chip", w.model),
      w.harness ? "модель работы на подписке " + w.harness : "модель работы"));
  }
  return chips;
}

// Род работы словом у номера задачи: разговор о задаче её не ведёт, строка на
// доске от него своей не становится. Прежде это был чип, и он читался плохо: у
// ведущей работы парного чипа не было вовсе, и разница висела в воздухе
// (разбор пользователя).
function workKindWord(w) {
  return w && w.talk ? "разговор" : "";
}

// Подпись под заголовком: ID, статус со строки доски, чем работа видна и имя
// сессии, по которому её находят в tmux. У работы, чьей строки на доске нет,
// статуса в подписи тоже нет: взять его неоткуда.
function workSub(w) {
  const parts = [];
  if (SECT_WORD[w.sect]) parts.push(SECT_WORD[w.sect]);
  if (w.via === "registry") parts.push("сессии дашборда нет");
  if (w.note) parts.push(w.note);
  // Имя сессии называется там, где она есть: у работы из реестра дашборд
  // сессии не видит, и выдуманное task-BB-7 отправило бы искать её в tmux.
  const name = w.via === "session" ? w.session : (w.via === "tmux" && w.id ? w.kind + "-" + w.id : "");
  if (name) parts.push("сессия " + name);
  return parts.join(", ");
}

function workAge(started, now) {
  if (!started) return "";
  const mins = Math.floor((now / 1000 - started) / 60);
  if (mins < 1) return "меньше минуты";
  if (mins < 60) return mins + " мин";
  return Math.floor(mins / 60) + " ч " + (mins % 60) + " мин";
}

function goButton(label, hash) {
  const btn = el("button", "btn btn-sm", label);
  btn.addEventListener("click", () => { goKeepingChat(hash); });
  return btn;
}

// Куда ведёт разговор строки агентов: у сессии, которую дашборд видит, это её
// собственный чат, а у работы без сессии остаётся адрес задачи, и панель
// поднимет разговор о ней. Панель встаёт хвостом поверх раздела, доска под ней
// не меняется.
function workChatAddr(w) {
  return w.session || w.id || "";
}

// Номер задачи в подписи это ссылка на её форму: из списка агентов человек
// уходит либо в разговор, либо в саму задачу, и обе дороги нужны у каждой
// строки (замечание пользователя). Кликом по строке открывается чат, поэтому
// ссылка гасит всплытие.
function workTaskLink(project, id) {
  const link = el("button", "alink", id);
  link.type = "button";
  link.title = "Открыть задачу " + id;
  link.addEventListener("click", (ev) => {
    ev.stopPropagation();
    goKeepingChat(project + "/" + id);
  });
  return link;
}

// Состояние работы словами и видом кружка. Зелёным горит только идущий ход:
// прежде зелёным была всякая живая сессия, и три десятка окон, молчавших
// часами, выглядели работающими (замечание пользователя по снимку). Само
// состояние считает сервер (workState в board.go), тут оно только называется:
// разъехавшись, кружок и слова мерили бы работу по-разному.
const WORK_LIVE = {
  busy: { word: LIVE_WORD.busy, chip: "c-run", dot: "pulse" },
  waiting: { word: LIVE_WORD.waiting, chip: "c-wait", dot: "dot-wait" },
  idle: { word: LIVE_WORD.idle, chip: "", dot: "dot-idle" },
  dead: { word: LIVE_WORD.dead, chip: "", dot: "dot-other" },
};

// Работа без состояния: сервер его не назвал (старая сборка, поломка разбора),
// и назвать за него нечем. Неизвестность это не работа: прежде такая строка
// горела зелёным кружком, потому что зелёный был умолчанием, и снимать её
// предлагалось наравне с живой (замечание пользователя). Своих слов у
// неизвестности нет ни одного лишнего: серый кружок и честная подпись.
const WORK_UNKNOWN = { word: "состояние неизвестно", chip: "", dot: "dot-other" };

function workKnown(w) {
  return Boolean(w && WORK_LIVE[w.live]);
}

function workLive(w) {
  if (!w) return null;
  return WORK_LIVE[w.live] || WORK_UNKNOWN;
}

// Давность последнего хода словами: по ней видно, живая работа или молчащая.
// Времени может не быть вовсе (реестр без поля времени), и тогда экран молчит,
// а не показывает эпоху.
function workMoved(w, now) {
  return w && w.moved ? pulseAge(w.moved, now) : "";
}

// День последней содержательной реплики и её время. Метка приезжает секундами
// unix, а колонка активности говорит днём: точное время уходит в подсказку,
// потому что в колонке шириной в сотню точек его не показать.
function workMovedDay(moved) {
  const at = new Date(Number(moved) * 1000);
  if (!moved || isNaN(at.getTime())) return "";
  const two = (n) => String(n).padStart(2, "0");
  return at.getFullYear() + "-" + two(at.getMonth() + 1) + "-" + two(at.getDate());
}

function workMovedTime(moved) {
  const at = new Date(Number(moved) * 1000);
  if (!moved || isNaN(at.getTime())) return "";
  return workMovedDay(moved) + " " + at.toLocaleTimeString([],
    { hour: "2-digit", minute: "2-digit" });
}

// Давность работы словами и подсказкой. У разговора с репликами это возраст
// последнего хода, а у разговора, где содержательных реплик не нашлось вовсе,
// честное «реплик нет» со временем начала в подсказке. Сервер считает этот
// возраст по последней реплике человека или агента, а не по времени правки
// файла: транскрипт трогают и при мёртвом содержимом, и брошенный трое суток
// назад разговор стоял простаивающим минуту (разбор пользователя).
function workSaid(w, now) {
  const age = workMoved(w, now);
  if (w && w.silent) {
    return {
      tail: ", реплик нет",
      tip: age
        ? "содержательных реплик в транскрипте нет, разговор заведён " + age + " назад"
        : "содержательных реплик в транскрипте нет, и времени начала разговора тоже не видно",
    };
  }
  return {
    tail: age ? " " + age : "",
    tip: age ? "последний ход " + age + " назад" : "времени последнего хода не видно",
  };
}

function workLiveChip(w, now) {
  const said = workLive(w);
  if (!said) return null;
  if (said === WORK_UNKNOWN) {
    return withTip(el("span", "chip", said.word),
      "сервер не назвал состояния этой работы: ни «активна», ни «простаивает» тут не " +
      "обещано, и снятие ей поэтому не предлагается");
  }
  const age = workSaid(w, now);
  const text = said.word + (said.word === LIVE_WORD.busy ? "" : age.tail);
  const chip = el("span", "chip" + (said.chip ? " " + said.chip : ""), text);
  return withTip(chip, said.word + ": " + age.tip);
}

// Снятие сессии: живой ход это не повод держать окно вечно, и закрыть его надо
// оттуда, где оно видно. Механика та же, что у снятия под перезапуск при смене
// модели (drop) и у стопа конвейера, второй тут не заводится.
async function closeSession(project, w) {
  if (w.session) {
    const r = await api(chatsURL(w.project || project) + "/" + encodeURIComponent(w.session) + "/stop",
      { method: "POST", body: { drop: true } });
    sayResult(r.body.message || r.body.error || "", !r.ok);
    if (r.ok) {
      // Строка уходит из списка тем же ходом, а не следующим обходом экрана:
      // сюда приходят разгребать живые работы, и снятая, оставшаяся стоять,
      // читается как несработавшее нажатие (замечание пользователя, у него
      // tmux-сессий стало меньше, а строка осталась).
      dropSessionRow(w);
      await refresh();
    }
    return r.ok;
  }
  if (w.id) {
    // У работы из tmux своего id сессии в ответе нет: его заполняет только
    // транскрипт (Work.Session в board.go). Снимается такая сессия ручкой
    // работы, а имя ей сервер собирает из вида и номера, task-DK-1.
    const ok = await stopRun(project, w.id);
    if (ok) dropSessionRow(w);
    return ok;
  }
  return false;
}

// Машинное имя состояния «сессии не видно»: сервер называет им работу, чьей
// tmux-сессии на машине нет.
const WORK_DEAD = "dead";

// Сколько после нажатия работа держится в табе, даже если сессии ещё не видно.
// Между нажатием и первым ходом клиента проходят секунды, и всё это время
// строка обязана стоять: пропавшая читалась бы как несработавшее нажатие.
const SESS_LIFT_GRACE = 2 * 60 * 1000;

// Таб сессий показывает живые работы. Разговор, чьей tmux-сессии не видно,
// уходит из таба независимо от того, как она кончилась: снял ли её дашборд,
// убил ли её человек снаружи, вышел ли клиент сам. Прежде уходили только
// снятые из дашборда (память sessGone), а умершие сами висели строками «сессии
// нет 7 мин» с припиской «снимать нечем», и сделать с ними было нечего
// (снимок пользователя). Из общего списка чатов такой разговор никуда не
// девается и открывается резюмом, как и раньше.
//
// Цикл цели из реестра тут не мёртв: его сессию дашборд не видит вовсе, а сам
// цикл идёт (via registry), и прятать его строку значило бы врать о работе
// машины. Только что поднятая работа держится льготным окном: она ещё не жива,
// но и не мертва.
function workShownLive(project, w) {
  if (!w) return false;
  if (w.via !== "session" || w.live !== WORK_DEAD) return true;
  if (!w.id) return false;
  const age = chatLiftAge(project, CHAT_NEW + ":" + w.id);
  return age >= 0 && age < SESS_LIFT_GRACE;
}

function sessionsShown(project, works) {
  return (works || []).filter((w) => workShownLive(project, w));
}

// Снятые сессии и время их снятия. Сервер узнаёт о снятии не мгновенно: он
// смотрит список tmux, а тот успевает ответить по-старому, и строка возвращалась
// следующим же обходом (замечание пользователя: tmux-сессий стало меньше, а
// строка осталась). Память короткая: она держит строку снятой, пока сервер не
// договорит своё, и сама рассасывается.
const sessGone = new Map();
const SESS_GONE_LIVE = 60 * 1000;

function sessGoneMark(w) {
  sessGone.set(workKey(w), Date.now());
}

function sessGoneHides(w) {
  const born = sessGone.get(workKey(w));
  if (!born) return false;
  if (Date.now() - born > SESS_GONE_LIVE) {
    sessGone.delete(workKey(w));
    return false;
  }
  return true;
}

// Снятая строка уходит с экрана сразу: узел находится по тому же ключу, каким
// его кладёт перерисовка списка, а память снятых держит её убранной, пока
// сервер не перестанет её отдавать.
function dropSessionRow(w) {
  sessGoneMark(w);
  const card = findKey(document.getElementById("groups"), "sess-card");
  const node = card && findKey(card, workKey(w));
  if (node && node.remove) node.remove();
}

// Кнопка закрытия с подтверждением для работающей сессии: снять идущий ход
// молча нельзя, а простаивающую держать вторым нажатием незачем (решение
// пользователя). Подтверждение живёт в самой кнопке: карточка поверх строки
// увела бы список из-под пальца.
function closeSessionBtn(project, w) {
  // Закрытие идёт значком, а не словом: в хвосте строки рядом со «Стопом» и
  // чатом слово «Закрыть» съедало колонку действий, а крестик читается сам.
  // Подтверждение слову с крестика не уходит: кнопка вооружается тем же вторым
  // нажатием, а сказано об этом подсказкой и подписью для чтения с экрана.
  const btn = el("button", "btn btn-sm btn-danger btn-ico sclose");
  btn.append(icon("close"));
  btn.setAttribute("aria-label", "Закрыть сессию");
  // Закрывать нечем: имени tmux-сессии у работы нет, и кнопка стоит
  // погашенной. Сам факт «действия нет» это и есть объяснение, разбор в
  // подсказке.
  if (!workDrops(w)) {
    btn.disabled = true;
    return withTip(btn, WORK_FOREIGN_TIP);
  }
  const busy = w.live === "busy" || w.live === "waiting";
  // Работа из списка tmux приходит и без состояния (его называет транскрипт, а
  // у конвейерной сессии его нет): такая держится вторым нажатием наравне с
  // занятой, потому что снятое обратно не поднимается.
  const known = workKnown(w);
  withTip(btn, busy || !known
    ? "Сессия занята: нажмите второй раз, чтобы снять её вместе с идущим ходом"
    : "Снять tmux-сессию: разговор останется в транскрипте, продолжить его можно резюмом");
  let armed = !busy && known;
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (!armed) {
      armed = true;
      // Взведённая кнопка видна собой: крестик наливается цветом отказа, и
      // вопрос стоит в подсказке с подписью для чтения с экрана. Текстом его
      // тут больше не написать, кнопка со значком места под фразу не держит.
      btn.classList.add("armed");
      // Снятие сессии это не потеря разговора, и сказать это надо ровно тут:
      // человек жмёт вторую кнопку, решая, не потеряет ли он написанное.
      withTip(btn, "Точно закрыть? Снимется только сессия. Разговор останется " +
        "в общем списке чатов и продолжится резюмом.");
      btn.setAttribute("aria-label", "Точно закрыть сессию");
      return;
    }
    btn.disabled = true;
    closeSession(project, w).catch(console.error).finally(() => { btn.disabled = false; });
  });
  return btn;
}

function agentRow(project, w, now) {
  const row = el("tr", "arow");
  const addr = workChatAddr(w);
  const tips = [];
  // Нажатие по строке ведёт туда же, куда ведёт строка доски: на экран задачи,
  // за которой идёт работа. Прежде оно открывало чат, и кнопка чата в той же
  // строке делала ровно это же, а до самой задачи со списка сессий было не
  // добраться иначе как ссылкой на номер (замечание пользователя). У работы без
  // задачи (разговор без строки) своего экрана нет, и там нажатие остаётся
  // входом в разговор.
  const go = w.id
    ? () => { goKeepingChat(project + "/" + w.id); }
    : (addr ? () => { openChat(chatAddr(project, addr)); } : null);
  if (w.id) tips.push("Открыть задачу " + w.id);
  else if (addr) tips.push("Открыть разговор этой работы");
  // Слова про внешнюю сессию тут те же, что у чипа и у хвоста строки: одно и
  // то же не должно объясняться тремя способами (решение пользователя).
  if (!agentOwn(w)) tips.push(WORK_FOREIGN_TIP);
  if (tips.length) row.title = tips.join(". ");
  if (go) {
    row.classList.add("atalk");
    row.addEventListener("click", go);
  }
  const said = workLive(w);
  // Кружок несёт состояние цветом и бегом, и он же говорит его словами: чип
  // «активна» рядом повторял то же самое третий раз и из строки убран
  // (разбор пользователя), а знание никуда не делось.
  const dot = el("span", "dot " + said.dot);
  withTip(dot, said.word + ": " + workSaid(w, now).tip);
  // Кружок лежит в ячейке колонки состояния, а не сам ею работает: ячейка
  // держит ширину колонки, а кружку своя ширина в девять точек.
  const live = el("td", "live");
  live.append(dot);
  row.append(live);
  const box = el("td", "ab");
  const line = el("div", "l1");
  // Заголовок задачи идёт первым: имя сессии goal-DK-112 о занятии агента не
  // говорит ничего, и место ему в подписи.
  // Подпись это задача, а у сессии без задачи заголовок чата, который сервер
  // берёт той же лестницей, что список чатов (замечание 1 восьмого круга).
  line.append(el("span", "tt", w.title || w.id || w.note || "чат без задачи"));
  // Состояние стоит первым чипом, кроме идущей работы: у неё состояние несут
  // бегущий кружок строки и время работы справа, и слово «активна» между ними
  // было третьим указателем на одно и то же (разбор пользователя).
  const liveChipNode = workLive(w) === WORK_LIVE.busy ? null : workLiveChip(w, now);
  // Чипы лежат той же коробкой, что у строки доски и у записи накопителя:
  // россыпью рядом с заголовком они стояли своим зазором и на телефоне не
  // умели уехать под него отдельной строкой.
  const chips = [];
  if (liveChipNode) chips.push(liveChipNode);
  for (const chip of workChips(w)) chips.push(chip);
  if (chips.length) {
    const chipbox = el("span", "rchips");
    for (const chip of chips) chipbox.append(chip);
    line.append(chipbox);
  }
  // Подпись собирается узлами, а не одной строкой: номер задачи в ней это
  // ссылка, и склеенный текст ссылкой быть не может.
  const sub = el("div", "l2");
  if (w.id) sub.append(workTaskLink(project, w.id));
  // Род работы стоит словом сразу у номера: «DK-397, разговор» против номера
  // без приписки у той работы, которая задачу ведёт.
  const kind = workKindWord(w);
  if (w.id && kind) sub.append(document.createTextNode(", " + kind));
  const tail = workSub(w);
  if (tail) sub.append(document.createTextNode((w.id ? ", " : "") + tail));
  box.append(line, sub);
  row.append(box);

  // Дата последней активности одной колонкой: возраст сессии стоял слева своей
  // колонкой «Идёт», и человек прочитал две колонки как одну («показывают
  // похоже одно и то же»). Полезна из них вторая, а возраст уехал в подсказку
  // рядом с точным временем реплики.
  const act = el("td", "amoved");
  if (w.moved) {
    const age = workAge(w.started, now);
    const said = workSaid(w, now);
    act.append(withTip(el("span", "stale dashed", workMovedDay(w.moved)),
      whenTip(w.moved, now) + (age ? ", сессия идёт " + age : "") +
      (w.silent ? ", " + said.tip : "")));
  }
  row.append(act);
  const { cell: actsc, box: actsbox } = tblCell("aacts");
  // Хвост строки собран той же коробкой, что у строки доски: зазор кнопок
  // держит одно правило на все три раздела, а не своё в каждом.
  const acts = el("span", "racts");
  actsbox.append(acts);
  // Разговор есть у любой строки: и у работы из реестра, чью сессию дашборд не
  // видит, и у сессии без задачи. Вход в чат один на цель и задачу, это одна и
  // та же панель, а ручку для реплики выбирает она сама (DK-435). Панель
  // встаёт хвостом поверх текущего раздела, а не уводит на доску.
  if (addr) {
    acts.append(chatIconBtn("Чат агента", "Чат агента " + (w.id || w.session || ""),
      () => { openChat(chatAddr(project, addr)); }));
  }
  // Работа из реестра поднята мимо дашборда, и кнопки остановки у неё нет.
  // Словами это в строке больше не стоит: приписка занимала полстроки и ломала
  // ряд, а сказать ей было нечего сверх того, что видно по отсутствию кнопки
  // (замечание пользователя). Знание уехало в подсказку строки, где и лежат
  // остальные метаданные.
  if (workRunning(w)) {
    // Стоп значком, как и в строке доски: слово рядом с чатом и крестиком
    // забирало колонку под подпись, а знание уехало в подсказку.
    const stop = withTip(el("button", "btn btn-sm btn-danger btn-ico rstop"), "Стоп: " + STOP_TIP);
    stop.append(icon("i-stop"));
    stop.setAttribute("aria-label", "Стоп");
    stop.addEventListener("click", (ev) => {
      ev.stopPropagation();
      stopRun(project, w.id).catch(console.error);
    });
    acts.append(stop);
  } else {
    // Сессия живёт в нашей tmux, но ход в ней не идёт: «Стоп» ей не адресован,
    // а закрыть окно человеку было нечем вовсе (замечание пользователя: «я
    // ничего не могу сделать с этими сессиями»). Прежде кнопка стояла только у
    // строки с id сессии, и разговорные сессии конвейера, которых у сервера
    // большинство, оставались с одним «Стопом» от несуществующего хода.
    //
    // Работу, которую закрыть нечем, та же кнопка называет собой погашенной.
    // Прежде вместо неё стояла приписка «поднята вне дашборда», и это был
    // третий способ сказать одно и то же: чип «внешняя», подсказка строки и
    // слова в хвосте. Погашенная кнопка говорит «действия нет» короче любых
    // слов и не отнимает у строки колонку (решение пользователя).
    acts.append(closeSessionBtn(project, w));
  }
  row.append(actsc);
  return row;
}

// Идущий конвейер: работу подняла tmux-сессия дашборда, и ход в ней идёт.
// Кнопка у такой строки называется «Стоп», потому что снимает она работу, а не
// разговор. Разговорная сессия (talk) конвейером не считается: ход в ней
// кончился, осталось живое окно, и снимают его закрытием.
//
// Про ход спрашивается то же, что у строки доски и у полосы действий задачи:
// живое окно без хода это не работа, и «Стоп» ему не адресован. Прежде тут
// стояло одно существование сессии, и простаивающая сессия конвейера получала
// красный «Стоп» вместо закрытия окна.
function workRunning(w) {
  return Boolean(w && w.via === "tmux" && w.id && !w.talk && workOnRun(w));
}

// Есть ли у строки что снимать. Вопрос этот клиент больше не решает сам: имя
// tmux-сессии приезжает работой (Work.Tmux), и оно же есть и доказательство
// подъёма дашбордом, и то, что снимается. Прежде клиент выводил это из вида
// работы и наличия id сессии, а сервер называл своей всякую сессию с именем
// конвейера, и признак стоял на форме имени, а не на факте.
function workDrops(w) {
  return Boolean(w && w.tmux);
}

// Чужая работа это работа, которую дашборд не поднимал: у неё нет имени
// tmux-сессии, и потому нет ни закрытия, ни обещания его. Слова тут одни на
// все случаи: цикл цели, поднятый в другом месте, окно vscode и свой терминал
// закрываются одинаково, там же, где открыты, а три разных объяснения одного
// бессилия человек читал как три разных беды (решение пользователя).
const WORK_FOREIGN_TIP = "Работу поднимал не дашборд (своё окно, свой терминал, " +
  "цикл в другом месте): закрывается она там же, где открыта";

// Своя работа это та, чью сессию поднял дашборд: признак приезжает работой
// (own), им помечена строка чипом, и им же гасится кнопка закрытия.
function agentOwn(w) {
  return Boolean(w && w.own);
}

// Что видно в строке словами, по тому и ищем: заголовок работы, задача, проект
// и модель. Поиск раздела свой нарочно: поле шапки уводило отсюда в выдачу по
// доске, то есть отвечало не на тот вопрос, который тут задают (замечание
// пользователя).
function agentMatch(item, q) {
  if (!q) return true;
  const w = item.work;
  const hay = [w.title, w.id, w.note, item.project, w.model, w.session,
    w.sect, w.kind].filter(Boolean).join(" ").toLowerCase();
  return q.toLowerCase().split(/\s+/).filter(Boolean).every((word) => hay.includes(word));
}

// Таб сессий на доске проекта. Список один: происхождение видно чипом в
// строке, а вложенных табов «Дашборд» и «Прочие» тут больше нет, они делили
// один короткий список надвое. Чужие проекты сюда не попадают вовсе: их сессии
// видны переключением проекта, а сквозной обзор машины живёт общим списком
// разговоров в панели (решение пользователя).
// Порядок сессий на экране: сперва работающие, за ними ждущие ответа, дальше
// простаивающие, мёртвые в самом низу. Внутри группы свежий ход выше: список
// отвечает на вопрос «чем машина занята сейчас», и молчащий второй день
// разговор стоять над идущим ходом не должен. Работа, о состоянии которой
// сказать нечего, идёт к простаивающим, а не к мёртвым: неизвестность это не
// повод хоронить.
const WORK_LIVE_ORDER = { busy: 0, waiting: 1, idle: 2, dead: 3 };

function workOrder(w) {
  const said = WORK_LIVE_ORDER[w && w.live];
  return said === undefined ? WORK_LIVE_ORDER.idle : said;
}

// Порядок раздела правит шапка колонок, как правит его у доски и накопителя.
// Умолчание тут прежнее, по состоянию: список отвечает на вопрос «чем машина
// занята сейчас». Второй ключ на все колонки один, свежий ход выше: молчащий
// второй день разговор стоять над идущим ходом не должен ни в каком порядке.
function sortSessions(list) {
  const now = Math.floor(Date.now() / 1000);
  const valueOf = (item, col) => {
    const w = item.work;
    if (col === "title") return String(w.title || w.id || w.note || "");
    // Колонка «Идёт» это возраст сессии, а не время её начала: убыванием тут
    // встаёт самая давняя, у неё и спрашивают, что висит дольше всех. Работа
    // без времени начала идёт хвостом, возрастом её мерить нечем.
    if (col === "age") return w.started ? now - Number(w.started) : -1;
    // Последняя содержательная реплика. Работа, о которой не сказано ничего,
    // идёт хвостом при убывании, как всякое отсутствие значения.
    if (col === "moved") return Number(w.moved) || 0;
    return workOrder(w);
  };
  return tblSorted(list, "sess", valueOf,
    (a, b) => (b.work.moved || 0) - (a.work.moved || 0));
}

// Ключ строки: сессия, а у работы без своей сессии её вид с задачей. По нему
// перерисовка находит прежний узел, и строка, у которой ничего не изменилось,
// переживает обновление вместе с фокусом и подтверждением закрытия.
function workKey(w) {
  return w.session || (w.kind || "work") + "-" + (w.id || w.note || "");
}

function workSign(w, now) {
  return [w.live || "", w.moved || 0, w.title || "", w.sect || "", w.note || "",
    w.model || "", w.harness || "", w.talk ? 1 : 0, w.silent ? 1 : 0,
    workSaid(w, now).tail].join("|");
}

// Строки таба сессий: рисуются по месту, узел за узлом. Полная пересборка
// экрана тут запрещена ценой: список живой, он перерисовывается по кругу, и
// обход всего экрана на каждый тик это ровно те тормоза, которые лечила
// правка панели (замер poc_bench_chat).
function paintSessionRows(table, project, works, q) {
  const list = sortSessions(sessionsShown(project, works).map((w) => ({ project, work: w }))
    .filter((item) => agentMatch(item, q) && !sessGoneHides(item.work)));
  const now = Date.now();
  const rows = list.map((item) => ({
    key: workKey(item.work),
    sign: workSign(item.work, now),
    make: () => agentRow(item.project, item.work, now),
  }));
  if (!rows.length) {
    rows.push({
      key: "sess-empty",
      sign: q ? "q" : "all",
      make: () => {
        const band = tblBand("sess", "bempty");
        const empty = el("div", "empty");
        if (q) {
          empty.append(el("b", "", "По запросу ничего не нашлось."));
          empty.append(document.createTextNode(
            "Ищем по заголовку работы, задаче и модели."));
        } else {
          empty.append(el("b", "", "Сессий проекта сейчас нет."));
          empty.append(document.createTextNode(
            "Запустите задачу с доски: кнопка «В работу» есть в строке задачи и на её экране."));
        }
        band.cell.append(empty);
        return band.tr;
      },
    });
  }
  // Шапка едет вместе со строками, а не выше по экрану: живой опрос
  // перерисовывает таблицу по кругу, и шапка, положенная мимо него, теряла бы
  // подсветку выбранной колонки на первом же тике. Пустому списку шапка не
  // нужна: подписывать нечего.
  const parts = [{ key: "cols", sign: "cols", make: () => tblColgroup("sess") }];
  if (list.length) {
    parts.push({
      key: "sess-head",
      sign: tblHeadSign("sess"),
      make: () => tblHead("sess", () => { paintSessionRows(table, project, works, q); }),
    });
  }
  parts.push(tblBodyItem("sess-body", rows.map((r) => r.key + "=" + r.sign).join("\n"), rows));
  sync(table, parts);
}

function renderSessions(project, works, q) {
  const groups = document.getElementById("groups");
  // Счётчик таба считает то же, что показывает список: бадж «5» над списком из
  // двух строк отправлял бы человека искать пропавшие три.
  const live = sessionsShown(project, works);
  countsSet({ sess: live.length });
  const nodes = sync(groups, [{
    key: "board-kind",
    sign: [project, "sess", shownCounts.tasks, live.length,
      shownCounts.drafts].join("|"),
    make: () => boardKindBar(project, "sess"),
  }, {
    // Шапка и строки живут одной таблицей: список тут один, и колонка у
    // подписи со строкой общая.
    key: "sess-card",
    sign: "card",
    make: () => tblTable("sess"),
  }]);
  paintSessionRows(nodes[nodes.length - 1], project, works, q);
  watchSessions(project, q);
}

// Как часто открытый таб переспрашивает состояние сессий. Живой список это его
// смысл: сессия начала ход, поднялась наверх и позеленела, кончила, опустилась.
// Частота названа тут же, где и порядок, а рубеж простоя, с которого работа
// перестаёт считаться работой, живёт у сервера (workIdleAfter в board.go):
// вопросы это разные, и мерить их одним числом нельзя.
const SESS_POLL = 4000;

let sessPoll = null;
let sessWired = false;

// Опрос гаснет при уходе с таба вместе с остальными живыми потоками экрана, и
// сторожевая переменная тут та же, что у экрана черновика: таймер один на таб,
// сколько бы раз его ни перерисовали.
function watchSessions(project, q) {
  if (!sessWired) {
    sessWired = true;
    agentLive.push(() => {
      if (sessPoll !== null) clearTimeout(sessPoll);
      sessPoll = null;
      sessWired = false;
    });
  }
  if (sessPoll !== null) return;
  sessPoll = setTimeout(() => {
    sessPoll = null;
    pollSessions(project, q).catch(console.error);
  }, SESS_POLL);
}

// Заход опроса: работы спрашиваются своей ручкой, а не общим списком проектов,
// и приезжают в те же строки. Молчание сервера экран не трогает вовсе:
// следующий заход спросит снова, а стирать список ради обрыва связи незачем.
async function pollSessions(project, q) {
  const rt = route();
  if (!rt.sess || rt.proj !== project) return;
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/works");
  const now = route();
  if (!now.sess || now.proj !== project) return;
  if (r.ok) renderSessions(project, r.body.works || [], q);
  else watchSessions(project, q);
}

// Остаток подписок (макет «00 Главная», блок в подвале боковой колонки). Имён
// харнесов тут нет ни одного: что показывать, целиком решает ответ сервера, а
// он собран из каталога снимков. На ноутбуке блок стоит в колонке над кнопкой
// выхода, на телефоне колонки нет вовсе, и то же самое едет карточкой на
// главную: остаток нужен как раз с телефона, чтобы понять, пора ли притормозить.
let quotaView = null;

// quotaWhen сжимает момент сброса до дня и месяца: в колонке шириной с ладонь
// год и минуты места не стоят, а полный момент остаётся подсказкой.
// Пороги свежести снимка одной точкой правды: зелёный до пятнадцати минут,
// жёлтый до сорока пяти, дальше красный.
const QUOTA_FRESH = 15 * 60;
const QUOTA_WARM = 45 * 60;

function quotaAgeClass(sec) {
  if (!sec && sec !== 0) return "";
  if (sec <= QUOTA_FRESH) return "q-fresh";
  if (sec <= QUOTA_WARM) return "q-warm";
  return "q-old";
}

function quotaWhen(reset) {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(reset || "");
  return m ? m[3] + "." + m[2] : "";
}

// Имя бакета для показа: приставка «window» у окна подписки занимала треть
// строки и не влезала по ширине колонки, а сказать ей было нечего (замечание
// пользователя). Ключи данных при этом прежние: режется только показ.
function bucketWord(name) {
  return String(name || "").replace(/^window/, "");
}

function quotaRow(b) {
  const row = el("div", "qrow" + (b.expired ? " expired" : ""));
  const name = el("em", "", bucketWord(b.name));
  name.title = b.name;
  row.append(name);
  const meter = el("span", "meter");
  const fill = el("i");
  fill.style.width = Math.max(0, Math.min(100, b.used_pct)) + "%";
  meter.append(fill);
  row.append(meter);
  row.append(el("b", "", b.used_pct + "%"));
  const when = quotaWhen(b.reset);
  if (when) {
    // Прошедший сброс подписывается словами: окно уже сбросилось, процент в
    // снимке со времени до сброса, и рисовать его как живой «до даты» значило
    // бы показывать потраченным то, что подписка уже вернула (DK-633).
    const res = el("span", "qres" + (b.expired ? " stale" : ""),
      (b.expired ? "сброшен " : "до ") + when);
    res.title = b.expired
      ? "окно сбросилось " + b.reset + ", цифра со времени до сброса и ждёт пересъёма"
      : "сброс " + b.reset;
    row.append(res);
  }
  return row;
}

// quotaNodes собирает узлы блока. Пустота тут говорит словами, какая она:
// каталога снимков нет, каталог пуст и снимок без бакетов это три разных
// причины, и молчащий блок был бы неотличим от отработавшего.
// Все подписки машины со снимками и без: снимки приходят ответом ручки, а
// список подписок тем же ответом, из которого собрана кнопка запуска. Подписка
// без снимка это не пропуск строки, а неизвестный остаток, и сказано это
// словами (замечание пользователя: цифры подписки без съёмщика выглядели
// такими же свежими, как у снимаемой тиком демона).
function quotaEvery(view) {
  const list = ((view && view.harnesses) || []).slice();
  const seen = new Set(list.map((h) => h.name));
  for (const h of harnesses()) {
    if (seen.has(h.name)) continue;
    list.push({ name: h.name, missing: true, buckets: [],
      note: "снимка нет: остаток неизвестен, снять командой agentctl quota refresh" });
  }
  list.sort((a, b) => String(a.name).localeCompare(String(b.name)));
  return list;
}

// Развёрнута ли причина отказа. Состояние живёт снаружи блока: плашка
// перерисовывается на каждом ответе ручки, и раскрытое человеком схлопывалось бы
// у него под рукой.
let quotaWhyOpen = false;

// quotaFailNodes собирает отказ обновления. На экране несколько слов, причина
// приходит нажатием: тот, кто отказал, пишет человеку в терминал целым абзацем,
// и абзац этот вставал в колонку шириной с ладонь портянкой (замечание
// пользователя).
function quotaFailNodes(fail) {
  const bad = el("div", "qnote qfail");
  bad.append(el("span", "", fail.reason));
  const why = [];
  if (fail.detail) why.push(fail.detail);
  if (fail.age) why.push("последняя попытка " + fail.age + " назад");
  if (fail.dir) why.push("каталог вызова " + fail.dir);
  bad.title = why.join(", ");
  const out = [bad];
  if (!why.length) return out;
  const more = el("button", "qwhy-b", quotaWhyOpen ? "скрыть" : "почему");
  more.onclick = () => {
    quotaWhyOpen = !quotaWhyOpen;
    paintQuota();
  };
  bad.append(more);
  if (quotaWhyOpen) out.push(el("div", "qnote qwhy", why.join("; ")));
  return out;
}

// quotaTook это час и минута снимка: человеку важнее знать, что цифры от 18:51,
// чем почему они не поехали дальше (замечание пользователя).
function quotaTook(taken) {
  const m = /T(\d{2}:\d{2})/.exec(taken || "");
  return m ? m[1] : "";
}

function quotaNodes(view) {
  const out = [el("h4", "", "Квота подписок")];
  if (!view) {
    out.push(el("div", "qnote", "снимки читаются..."));
    return out;
  }
  if (view.note) out.push(el("div", "qnote", view.note));
  // Отказ обновления стоит прямо в плашке, над снимками: возраст снимка тут и
  // так написан, а без причины он читался как «дашборд забыл про квоту». В
  // журнале эта же строка лежала одна и та же каждые десять минут, а человек
  // смотрел на трёхчасовой снимок и объяснения не имел (живой случай).
  if (view.fail && view.fail.reason) {
    out.push(...quotaFailNodes(view.fail));
  }
  for (const h of quotaEvery(view)) {
    out.push(el("div", "qsub", h.name));
    // Подписка без снимка стоит строкой со словами, а не пропуском: список
    // снимков собирается по файлам каталога, и подписка без съёмщика не
    // попадала в него вовсе, а молчание читалось как «всё в порядке».
    if (h.missing) {
      out.push(el("div", "qnote stale", h.note || "снимка нет: остаток неизвестен"));
      continue;
    }
    for (const b of h.buckets || []) out.push(quotaRow(b));
    // Возраст снимка виден цветом, а не словом «протух»: слово ничего не
    // говорило о том, насколько всё плохо, и стояло почти всегда (замечание 21).
    const note = el("div", "qnote");
    // Давность снимка стоит цифрой у каждой подписки, и сравнение человек
    // делает сам: приписка «раньше остальных» у старшего была лишним словом.
    if (h.age) {
      const took = quotaTook(h.taken);
      note.append(el("span", "qage " + quotaAgeClass(h.age_sec),
        (took ? "снимок от " + took + ", " : "снимок ") + h.age + " назад"));
    }
    const rest = [];
    // Причина остаётся словами там, где возрасту верить нельзя вовсе: часы
    // разошлись, момента снятия нет.
    if (h.note) rest.push(h.note);
    for (const w of h.warns || []) rest.push(w);
    if (rest.length) note.append(el("span", "", (h.age ? ", " : "") + rest.join(", ")));
    out.push(note);
  }
  return out;
}

// paintQuota рисует блок там, где он сейчас есть: колонка стоит над любым
// экраном, карточка живёт только на главной.
function paintQuota() {
  for (const id of ["quota", "quota-card"]) {
    const box = document.getElementById(id);
    if (box) box.replaceChildren(...quotaNodes(quotaView));
  }
}

async function refreshQuota() {
  const r = await api("/api/quota");
  quotaView = r.body;
  paintQuota();
}

// Сколько отказов связи подряд экран терпит молча. Одиночный отказ это штатная
// жизнь ноутбука: он засыпает, сеть моргает, а сам дашборд перезапускается на
// каждом выкате, и уведомления о таком человек не заказывал («зачем о штатной
// ситуации делать уведомление», замечание пользователя). Экран просто ходит
// заново, а сказать словами есть о чём, только когда связь не вернулась за
// несколько заходов подряд.
const LOST_TRIES = 3;

// Пауза между молчаливыми повторами: три захода с ней укладываются примерно в
// десять секунд, то есть говорить экран начинает тогда же, когда человек и сам
// заметит, что данные не едут.
const LOST_RETRY = 3000;

let lostTries = 0;
let lostTimer = null;

// Повтор захода после отказа связи. Таймер один: заходов бывает несколько
// (фокус окна, опрос), и каждый заводил бы свой.
function lostAgain() {
  if (lostTimer !== null) return;
  lostTimer = setTimeout(() => {
    lostTimer = null;
    refresh().catch(console.error);
  }, LOST_RETRY);
}

// Связь вернулась: счётчик обнуляется, а карточка уходит сама. Экран после
// удачного захода собирается заново и снял бы её и так, но экраны собираются
// по-разному, и надёжнее снять её тут одним местом.
function lostGone() {
  lostTries = 0;
  const card = lostCard();
  if (card && card.remove) card.remove();
}

// Карточка потери связи, если она стоит на экране. Ищется она среди прямых
// детей списка: своего ключа перерисовки у неё нет, она гостья на чужом экране.
function lostCard() {
  const groups = document.getElementById("groups");
  for (const kid of (groups && groups.children) || []) {
    if (String(kid.className || "").split(" ").includes("lostc")) return kid;
  }
  return null;
}

// Устойчивый отказ связи одной спокойной строкой. Это не поломка, а временная
// потеря связи (типовой случай это наш же перезапуск при выкате), поэтому вид
// у карточки спокойный, а не красный. Перечисление причин ушло в подсказку:
// поверх интерфейса оно разрасталось красной простынёй на пол-экрана
// (замечание пользователя).
function showLost(why) {
  const groups = document.getElementById("groups");
  const had = lostCard();
  const card = had || el("div", "card lostc");
  card.replaceChildren();
  const line = el("div", "lostline", "связь с дашбордом прервалась, экран обновится сам");
  withTip(line, why || "");
  card.append(line);
  if (!had) groups.append(card);
}

function showError(text) {
  const groups = document.getElementById("groups");
  // Экран уходит целиком, вместе с ним и живые потоки: чат свою ленту при
  // обновлении переживает, но пережить снятый с экрана разговор она не может.
  closeAgentLive();
  groups.replaceChildren();
  const card = el("div", "card");
  card.append(el("div", "error", text));
  groups.append(card);
}

// Проект, который сейчас на экране: по нему разделы боковой колонки строят
// свой переход, когда в хэше проекта ещё нет.
let shownProject = "";

// Доска и живые работы последнего обхода: панель разговора рисуется после
// экрана и берёт их отсюда, а не ходит за ними второй раз. Экран, которому
// доска не нужна (накопитель, поиск, лента), оставляет их пустыми, и панель
// тогда спрашивает доску сама.
let shownBoard = null;
let shownWorks = [];

// Экран, который сейчас нарисован: по нему обновление отличает перечитанное
// от перехода. Тому же экрану возвращается место и фокус, а переход открывает
// новый экран сверху и снимает живые потоки прежнего.
let shownScreen = "";

// Запрос, с которым экран нарисован: выдача поиска и раздел «Агенты» держат
// набранное в адресе, а в ключ экрана оно не входит.
let shownQuery = "";

function screenKey(rt) {
  // Запрос в ключ не входит: набор буквы это не переход на другой экран, и
  // выдача обязана перерисоваться по месту, а не собраться заново под пальцем.
  // Разговора в ключе нет вовсе: панель это хвост адреса, она стоит своим
  // узлом и своими потоками, а экран под ней от её открытия не меняется и
  // собираться заново не должен.
  return [rt.proj, rt.id, rt.home, rt.agents, rt.feed, rt.make, rt.drafts, rt.sess,
    rt.find, rt.draft, rt.doc, rt.path, rt.lldList].join("|");
}

// Обновление экрана с сохранением места: перерисовка идёт по месту, а те
// экраны, что собираются целиком, теряют прокрутку на опустевшем списке, и
// она возвращается снимком. Переход это другой экран, ему прежнее место не
// принадлежит.
async function refresh() {
  // Пока строку держат, экран не перерисовывается вовсе: перерисовка увела бы
  // из-под пальца и саму строку, и щели с коридором, нарисованные по той
  // доске, с которой жест считает. Обновление вернётся сразу после броска, его
  // зовёт и сама правка.
  if (dragOn()) return;
  const was = shownScreen;
  const snap = viewSnap();
  try {
    await paint();
  } finally {
    if (shownScreen === was) viewBack(snap);
    else if (snap.groups) snap.groups.scrollTop = 0;
  }
  // Панель рисуется после экрана и по тому же обходу: доска и живые работы у
  // неё уже прочитаны, а открытую панель перерисовка экрана не трогает.
  await paintChat(shownProject, route().chat, shownBoard, shownWorks);
}

async function paint() {
  const rt = route();
  const screen = screenKey(rt);
  // Живые потоки закрываются при уходе с экрана, а не на каждом обновлении:
  // чат свою ленту, очередь исходящих и поле ввода переживает целиком, и
  // рвать ради перечитанной доски поток событий незачем (DK-316). Экран агента
  // держит так же журнал, ленту разговора и снимок tmux (DK-290), а экран
  // черновика снимок tmux с ходом груминга. Остальные экраны собираются
  // заново, и их потоки перед сборкой снимаются.
  // Живые потоки экрана закрываются при уходе с него, а не на каждом
  // обновлении: экран черновика держит хвост груминга, и рвать его ради
  // перечитанной доски незачем (DK-316). Панель разговора живёт своим списком
  // потоков и сюда не входит вовсе.
  // Экран задачи держит свои потоки так же, как экран черновика: журнал витка
  // и план агента живут, пока экран стоит. Обновление по фокусу окна экран не
  // пересобирает (DK-411), и рвать ради него журнал незачем; собирая экран
  // заново, renderTask снимает потоки сам.
  if (screen !== shownScreen || !(rt.draft || rt.id)) closeAgentLive();
  // Выбор черновиков это намерение сейчас, а не настройка: смена экрана его
  // снимает. Перерисовка того же экрана (обновление по фокусу окна, ответ
  // сервера) выбор переживает, иначе отметки таяли бы под рукой.
  if (screen !== shownScreen) {
    draftPick.clear();
    draftBarPaint = null;
  }
  // Прежний экран нужен ниже: оболочку задачи ставит только переход, а
  // обновление того же экрана (фокус окна, опрос) стирало бы ею уже собранную
  // форму.
  const wasScreen = shownScreen;
  // Переход это всегда сборка заново: под тем же ключом экрана лежит теперь
  // разметка соседнего раздела (доска сменилась накопителем и вернулась), и
  // отпечаток прошлой отрисовки к ней не относится.
  if (screen !== wasScreen) boardPainted = { screen: "", sign: "" };
  shownScreen = screen;
  // Запрос в ключ экрана не входит нарочно (набор буквы это не переход), но
  // помнить набранное всё равно надо: по нему обновление отличает «сменился
  // один хвост разговора» от «человек набрал следующую букву».
  shownQuery = rt.q || "";
  // Поле шапки всегда отражает адрес экрана: запрос живёт в адресе (образец
  // раздела «Агенты»), переживает переход на форму и возврат кнопкой «назад»,
  // а у экранов без запроса поле пустеет. Прежде после возврата с формы в поле
  // оставалась строка поиска при полном списке, и поле противоречило выдаче
  // (замечание пользователя). Набираемое под фокусом не трогается.
  const hq = document.getElementById("hq");
  if (hq && document.activeElement !== hq && hq.value !== shownQuery) hq.value = shownQuery;
  // Слова в пустом поле называют то, что оно ищет по месту: в разделе «Агенты»
  // оно фильтрует сессии раздела (findGo), и «Поиск задач» обещал бы там выдачу
  // по доске. Значения поля это не касается, его правит синхронизация выше.
  if (hq) {
    hq.placeholder = rt.sess ? "Поиск сессий" : "Поиск задач";
    hq.setAttribute("aria-label", rt.sess
      ? "Поиск по сессиям проекта"
      : "Поиск по доске, черновикам и архиву");
  }
  const hqClear = document.getElementById("hq-clear");
  if (hqClear) hqClear.hidden = !(hq && hq.value);
  shownBoard = null;
  shownWorks = [];
  // Экран задачи по ссылке из ленты это самый частый переход дашборда, и ждать
  // ради него полного обхода сети незачем: человек жаловался на две-три
  // секунды до формы при ручках сервера в десятки миллисекунд (замер
  // poc_bench_task). Оболочка встаёт в тот же ход, до единого запроса, а сама
  // строка едет своей ручкой параллельно списку проектов, а не после него.
  // Правку в форме оболочка не трогает: перерисовка стёрла бы набранное.
  // Проект берётся из адреса, и первый заход по ссылке (открытая вкладка,
  // закладка) идёт этой же дорогой: имя проекта в адресе уже есть, а не
  // нашлось оно на машине, значит экран заменит собой отказ.
  const quick = Boolean(rt.id && !rt.feed && rt.proj &&
    (!shownProject || rt.proj === shownProject) &&
    !(taskDraft.id === rt.id && taskDraft.dirty));
  // Заказ, которого никто не дождался (ушли с экрана, не приехал список
  // проектов), не должен падать необработанным обещанием: отказ едет тем же
  // видом, каким его отдаёт api, и экран задачи покажет его словами.
  const pre = quick ? api(taskPath(rt.proj, rt.id)).catch((err) => ({
    ok: false, status: 0, body: { error: String((err && err.message) || err) },
  })) : null;
  if (quick && screen !== wasScreen) taskShell(rt.proj, rt.id);
  const list = await api("/api/projects");
  // Ответ мог приехать после ухода с экрана: обход идёт и по фокусу окна, и по
  // опросу, и рисовать им чужой экран нельзя (замечание пользователя про
  // «в корнях конфига не нашлось ни одной доски» при живых досках).
  if (screenKey(route()) !== screen) return;
  const body = list.body || {};
  // Список не приехал вовсе (обрыв связи, отказ входа перед сервером) это не
  // пустой конфиг: молчание сети и «досок нет» разные вещи, и вторая фраза тут
  // врала бы про настройку. Уже показанное при этом остаётся на экране: экран
  // не должен пустеть от одного неудачного захода.
  if (!list.ok) {
    const why = body.error || ("сервер не ответил (" + list.status + ")");
    lostTries += 1;
    // Уже показанное остаётся на экране в любом случае: экран не должен
    // пустеть от неудачного захода.
    lostAgain();
    // Одиночный отказ проходит молча: экран сам сходит ещё раз.
    if (lostTries < LOST_TRIES) return;
    // Связи нет уже несколько заходов подряд: сказать пора, но одной строкой и
    // спокойным видом. Пустому экрану строка встаёт карточкой, а на собранном
    // хватает той же строки поверх него.
    if (document.getElementById("groups").children.length && !lostCard()) {
      sayResult("связь с дашбордом прервалась, экран обновится сам", false);
      return;
    }
    headName("Связи нет");
    showLost(why);
    return;
  }
  lostGone();
  const projects = body.projects || [];
  // Префиксы досок оседают тут: по ним автоссылка в реплике узнаёт ID задачи и
  // выбирает проект, чья это доска.
  rememberPrefixes(projects);
  const current = currentProject(projects);
  // Проект помнится и на главной: с неё раздел «Доска» ведёт на тот проект,
  // который откроется по имени, а не на пустой хэш.
  shownProject = current ? current.name : "";
  // Точка на колокольчике живёт отдельно от экрана: она нужна и на доске, и на
  // главной, а ждать её ответа экрану незачем.
  refreshBellDot().catch(console.error);
  // Полка ждущих живёт тем же порядком: она стоит над любым экраном, и держать
  // экран ради обхода досок незачем.
  refreshWaits().catch(console.error);
  // Остаток подписок тоже живёт отдельно от экрана: он стоит над любым из них,
  // а держать экран ради чтения пары файлов незачем.
  refreshQuota().catch(console.error);
  // А вот список подписок экран ждёт: из него собрана кнопка запуска, и
  // пришедший позже он перерисовал бы кнопку под пальцем. Ждать тут дёшево,
  // ходит запрос один раз на загрузку страницы.
  await loadHarnesses();
  renderSidebar(projects, rt.home ? null : current);
  document.getElementById("brand-note").textContent =
    projects.length + " " + plural(projects.length, "проект", "проекта", "проектов");
  // Раздела «Агенты» больше нет: сессии переехали в таб доски. Старый адрес
  // ведёт туда же вместе с набранным запросом, чтобы ссылки и память вкладки
  // не ломались от переезда.
  if (rt.agents && current) {
    const q = rt.q ? "/" + rt.q : "";
    goSame(current.name + "/sess" + q);
    return refresh();
  }
  if (rt.home) {
    headName("Проекты");
    markNav(rt);
    renderHome(projects);
    return;
  }
  if (!current) {
    // Сюда доходит только пустой список от ответившего сервера: обрыв связи
    // разобран выше, а поздний ответ чужого экрана сюда не доходит вовсе.
    headName("Проектов нет");
    showError((body.errors || []).join("; ") || "в корнях конфига не нашлось ни одной доски docs/TASKS.md");
    return;
  }
  // Числа табов приезжают тем же ответом, что и список проектов: экран, на
  // котором открыт один таб, о соседних сам ничего не знает.
  const sects = current.sections || {};
  countsSet({
    tasks: Object.keys(sects).reduce((n, key) => n + (sects[key] || 0), 0),
    sess: (current.works || []).length,
    drafts: current.drafts || 0,
  });
  // Дорога назад стоит там, откуда есть куда возвращаться: экран задачи,
  // запись накопителя, документ и список LLD висят под доской, и с них уходят
  // одним нажатием. Сама доска дороги не носит ни в одном из своих табов
  // (задачи, сессии, накопитель, выдача поиска): все три это её разделы, и
  // «назад» вело бы с доски на неё же (замечание пользователя). Форма
  // заведения дороги тоже не носит, у неё свой выход рядом с записью. Доска
  // ниже ставит то же место со своей подсказкой.
  if (rt.id || rt.doc || rt.lldList) {
    headName(current.name, "", () => { goKeepingChat(current.name); });
  } else {
    headHere("Доска " + current.name, "");
  }
  if (rt.make) {
    // Форме заведения доска не нужна: лишний поход за ней стоил бы своего
    // подпроцесса taskctl на каждый фокус окна.
    // Вид заводимого стоит в адресе, и голый #проект/new это задача: выбор
    // делает выпадашка у кнопки заведения, а адрес без вида остаётся живым
    // входом для ссылки и закладки.
    const kind = rt.kind === "draft" ? "draft" : "task";
    markNav(rt);
    renderNew(current.name, kind);
    return;
  }
  if (rt.drafts) {
    // Накопителю доска тоже не нужна: он читается своей ручкой, и лишний поход
    // за доской стоил бы подпроцесса taskctl на каждый фокус окна.
    markNav(rt);
    await renderDrafts(current.name, current.works);
    return;
  }
  if (rt.sess) {
    // Списку сессий доска не нужна: работы приезжают тем же ответом, что и
    // список проектов, и второго похода на сервер таб не стоит.
    markNav(rt);
    renderSessions(current.name, current.works, rt.q || "");
    return;
  }
  if (rt.find) {
    // Выдаче доска не нужна: поиск живёт своей ручкой и сам берёт доску из
    // кэша сервера вместе с накопителем и архивом.
    markNav(rt);
    await renderFind(current.name, rt.q);
    return;
  }
  if (rt.draft && rt.id) {
    // Экрану записи доска тоже не нужна: идущий груминг виден среди живых
    // работ проекта, а исход разбора читает своей ручкой сервер.
    markNav(rt);
    await renderDraft(current.name, current.works, rt.id);
    return;
  }
  if (rt.doc) {
    // Документу доска не нужна: он читается своей ручкой.
    markNav(rt);
    await renderDoc(current.name, rt.path);
    return;
  }
  if (rt.lldList) {
    // Списку LLD доска тоже не нужна: раздел живёт своей ручкой.
    markNav(rt);
    await renderLld(current.name, rt.q);
    return;
  }
  if (rt.id) {
    // Экрану задачи доска не нужна: строку он читает своей ручкой, а живые
    // работы приезжают тем же ответом, что и список проектов. Поход за доской
    // стоил тут целого круга по сети и подпроцесса taskctl на каждый переход.
    markNav(rt);
    // Заказ отдаётся экрану только если он про тот же проект: имя из адреса и
    // выбранный проект расходятся, когда в адресе стоит незнакомая доска.
    await renderTask(current.name, current.works || [], rt.id,
      current.name === rt.proj ? pre : null);
    return;
  }
  const r = await api("/api/projects/" + encodeURIComponent(current.name) + "/board");
  if (!r.ok) {
    showError(r.body.error || ("доска не прочиталась (" + r.status + ")"));
    return;
  }
  const board = r.body.board || {};
  shownBoard = board;
  shownWorks = r.body.works || [];
  markNav(rt);
  if (rt.feed) {
    renderFeed(current.name);
    return;
  }
  // Путь доски и префикс ID строкой в шапке не стоят: имя файла с префиксом
  // человек читал каждый раз заново, отвечая на вопрос, которого не задавал
  // (замечание пользователя). Знание осталось подсказкой на самом названии
  // проекта: там его берут, когда надо.
  headHere("Доска " + current.name,
    "доска docs/TASKS.md" + (board.prefix ? ", префикс " + board.prefix : ""));
  // Круг заводится по данным, а не по перерисовке, и потому стоит он выше
  // отпечатка. Стоя ниже, круг обрывался на первом же заходе, который ничего
  // не менял: отпечаток совпадал, ответ уходил отсюда, перерисовки не было, а
  // следующий таймер заводила как раз она. Опрос жил ровно один заход, и доска
  // возвращалась к перечитыванию по фокусу окна.
  if (boardMoves(board, r.body.works || [])) watchRunning();
  // Данные те же, что и в прошлый заход: списка не касаемся вовсе. Строку
  // задачи двигает человек, а не время, и перебирать сотню строк каждые
  // три секунды (столько ходит круг у живой работы) незачем: перерисовка по
  // месту дёшева, но не бесплатна, а пока она идёт, список стоит под пальцем
  // (замечание пользователя про дёрганье при листании).
  const paintSign = boardPaintSign(board, r.body.works || []);
  const groupsBox = document.getElementById("groups");
  if (boardPainted.screen === screen && boardPainted.sign === paintSign &&
    groupsBox && groupsBox.children.length) {
    return;
  }
  boardPainted = { screen, sign: paintSign };
  renderBoard(current.name, board, r.body.works || []);
}

// Заголовок раздела в шапке: имя и подсказка ставятся одним заходом. Порознь
// подсказка переживала переход на соседний экран и висела там, объясняя чужое
// название.
// Куда ведёт название проекта в шапке. Обработчик садится на узел один раз, а
// не при каждой отрисовке: заголовок переживает переход, и вешать на него по
// слушателю на заход значило бы копить их сотнями.
let headGo = null;
document.getElementById("pname").addEventListener("click", () => {
  if (headGo) headGo();
});

// go это дорога с названия: с экрана внутри проекта шапка читается «Назад на
// доску», со стрелкой влево, и ведёт на доску проекта. Имя проекта из этих
// слов ушло, на его месте стоит ответ на вопрос, куда уведёт нажатие (просьба
// пользователя), а где человек находится, видно рядом: на широком экране
// текущий проект подсвечен в списке слева, на телефоне он выбран в списке той
// же шапки. Своей ссылки на доску экран задачи не носит, она стояла второй
// такой же строкой ниже (решение пользователя).
function headName(name, tip, go) {
  const node = document.getElementById("pname");
  headGo = go || null;
  if (go) {
    // Стрелка берётся из тех же значков разметки, что и остальные: рисовать её
    // рамками стилей выходило обрубком. Слова лежат своим узлом, потому что
    // черта дороги идёт под ними: протянутая на всю ссылку, она перечёркивала
    // бы стрелку.
    const back = icon("i-out");
    back.setAttribute("class", "hgoi");
    node.replaceChildren(back, el("span", "hgot", "Назад на доску"));
  } else {
    node.replaceChildren(document.createTextNode(name));
  }
  node.title = tip || "";
  node.className = go ? "hgo" : "";
  return node;
}

// Шапка самой доски: дороги на себя она не носит, «назад» вело бы с доски на
// неё же. Место названо теми же словами и тем же кеглем, что и ссылка, только
// без стрелки и без нажатия.
function headHere(name, tip) {
  const node = headName(name, tip);
  node.classList.add("hhere");
  return node;
}

function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}

// Разделы боковой колонки и нижних вкладок: главная ведёт на список проектов,
// доска на свой экран текущего проекта, открытый раздел подсвечен. Лента
// разделом больше не стоит, вход в неё это колокольчик в шапке, и открытая
// лента подсвечивает его.
// Значок чатов в шапке моргает рамкой, когда у открытой задачи идёт работа: на
// её форме своей кнопки чата нет, вход в разговор тут один, и след запуска
// человек ищет глазами именно здесь.
function markChatsLive(rt) {
  const btn = document.getElementById("chats");
  if (!btn) return;
  const lively = Boolean(rt.proj && rt.id) && taskLively(rt.proj, rt.id, shownWorks);
  btn.classList.toggle("chatlive", lively);
}

function markNav(rt) {
  markChatsLive(rt);
  // На самой главной логотип погашен: он никуда не ведёт, и подсветка по
  // наведению обещала бы переход, которого нет.
  for (const id of ["logo-side", "logo-top"]) {
    const logo = document.getElementById(id);
    logo.classList.toggle("here", Boolean(rt.home));
    logo.title = rt.home ? "Вы и так на главной" : "На главную";
  }
  // Легенда кружков живёт на главной: её рисует renderHome, а с остальных
  // экранов она убирается вместе с ними.
  if (!rt.home) document.getElementById("hlegend").replaceChildren();
  // Накопитель это таб доски, а не раздел: открытые черновики подсвечивают
  // «Доску», как её подсвечивает и экран записи.
  const on = rt.home ? "home" : rt.feed ? "feed"
    : rt.find ? "find" : rt.make ? "make"
    : rt.lldList || rt.doc ? "lld" : "board";
  for (const [name, ids] of [["home", ["nav-home", "tab-home"]],
    ["board", ["nav-board", "tab-board"]],
    ["lld", ["nav-lld"]],
    ["make", ["make-btn"]],
    ["feed", ["bell"]],
    ["find", ["find-btn"]]]) {
    for (const id of ids) {
      document.getElementById(id).classList.toggle("on", name === on);
    }
  }
}

// Вход в общий чат доски стоит в шапке рядом с лентой: разговор без задачи
// открывается той же панелью с пустой привязкой, своего экрана у него нет
// (LLD DK-430, решение 7).
document.getElementById("chats").addEventListener("click", () => {
  // Кнопка шапки открывает разговор и ничего к нему не привязывает. Прежде она
  // с экрана задачи открывала чат этой задачи, и другого способа открыть
  // обычный разговор с задачи не было вовсе: «по идее эта кнопка просто
  // открывает чат, для открытия чата задачи есть отдельная кнопка на ней же»
  // (замечание пользователя). Чат задачи так и остаётся за своей кнопкой в
  // командной панели задачи.
  const rt = route();
  if (rt.proj) {
    openChat(CHAT_BOARD);
    return;
  }
  if (shownProject) location.hash = shownProject + "/chat/" + CHAT_BOARD;
});

// Полка ждущих открывается своей кнопкой в шапке, рядом с колокольчиком: лента
// говорит, что случилось, а полка кто ждёт прямо сейчас, и своего экрана ей не
// нужно (DK-696).
document.getElementById("waits").addEventListener("click", (ev) => {
  ev.stopPropagation();
  waitShelfOpen(document.getElementById("waits"));
});

// Число на кнопке живёт своим кругом: заходы на экран бывают редкими, а вопрос
// приходит когда угодно, и узнавать о нём только при переходе значило бы
// молчать ровно тогда, когда человек и так сидит на одном экране.
setInterval(() => { refreshWaits().catch(console.error); }, WAIT_POLL);

// Кнопка заведения в шапке спрашивает вид тем же меню, что плюс карточки
// проекта и плавающий плюс телефона. Прежде она вела прямо на форму задачи, и
// завести черновик с доски было нечем вовсе (замечание пользователя): дорога к
// нему оставалась одна, через накопитель.
document.getElementById("make-btn").addEventListener("click", (ev) => {
  ev.stopPropagation();
  const btn = document.getElementById("make-btn");
  const project = shownProject || route().proj;
  // Проекта нет вовсе (пустой конфиг, оборванная связь): заводить некуда, и
  // меню обещало бы форму, которой не откроется.
  if (project) makeMenuAt(btn, project, btn);
});

for (const [id, tail] of [["nav-board", ""], ["tab-board", ""],
  ["nav-lld", "/lld"], ["bell", "/feed"], ["find-btn", "/find/"]]) {
  document.getElementById(id).addEventListener("click", () => {
    // Имя проекта берётся то, что показано: на главной хэш пуст, и раздел без
    // имени увёл бы на "#/feed". Открытый разговор переезжает вместе с
    // переходом: лента уведомлений его больше не закрывает.
    goKeepingChat((shownProject || route().proj) + tail);
  });
}

// Переход на главную это логотип в левом верхнем углу: на ноутбуке он стоит
// вверху боковой колонки, на телефоне слева в шапке, кнопки «На главную» нет
// нигде. На телефоне то же место занимает и первая нижняя вкладка.
for (const id of ["logo-side", "logo-top", "nav-home", "tab-home"]) {
  document.getElementById(id).addEventListener("click", () => {
    // Пустой хэш это главная. Пустая строка оставила бы в адресе прежний "#x",
    // поэтому решётка ставится явно. Открытая панель переезжает вместе с
    // переходом, как и на всех остальных дорогах.
    const chat = route().chat;
    location.hash = chat ? "#/chat/" + chat : "#";
  });
}

// Боковая колонка сворачивается: нужна она редко, а место занимает всегда
// (замечание пользователя). Свёрнутая пропадает целиком, и её ширина достаётся
// доске и панели разговора, а помнится это между заходами тем же способом, что
// и ширина панели.
const SIDE_OFF_KEY = "devkit.side.off";

function sideFolded() {
  try {
    return localStorage.getItem(SIDE_OFF_KEY) === "1";
  } catch (err) {
    // Приватное окно запрещает хранилище: колонка тогда встречает развёрнутой,
    // а сворачивается и разворачивается по-прежнему.
    return false;
  }
}

// Показать колонку или убрать. Таблице тут же пересчитываются колонки: ширина
// экрана стала другой, и без пересчёта строка держала бы прежнюю раскладку до
// следующей перерисовки, как это было при тяге панели разговора.
function putSideFold(off) {
  const screen = document.getElementById("screen");
  if (screen) screen.classList.toggle("sideoff", off);
  const fold = document.getElementById("side-fold");
  if (fold) fold.setAttribute("aria-expanded", off ? "false" : "true");
  tblWidthsAll();
}

function foldSide(off) {
  try {
    localStorage.setItem(SIDE_OFF_KEY, off ? "1" : "0");
  } catch (err) {
    // Память недоступна: на этот заход колонка всё равно свернётся.
  }
  putSideFold(off);
}

for (const [id, off] of [["side-fold", true], ["side-show", false]]) {
  const btn = document.getElementById(id);
  if (btn) btn.addEventListener("click", () => { foldSide(off); });
}
putSideFold(sideFolded());

document.getElementById("logout").addEventListener("click", async () => {
  await fetch("/api/logout", { method: "POST" });
  location.href = "/login";
});

window.addEventListener("hashchange", () => {
  // Двинулся один хвост разговора, а экран тот же: перерисовывать под панелью
  // нечего, и полный обход тут был чистым ожиданием сети (замечание
  // пользователя про тормоза переключения и закрытия).
  if (chatOnlyMove(route())) {
    repaintChatOnly();
    return;
  }
  // Уход с экрана это отказ от черновика: он держит ровно ту задачу, которая
  // на экране.
  taskDraft.dirty = false;
  // Переход сразу после удачного запуска карточку ответа не снимает: флаг
  // одноразовый, а дальше уход с экрана снимает сказанное, как и раньше.
  if (keepResult) keepResult = false;
  else sayResult("");
  refresh().catch(console.error);
});
// Доска перечитывается по фокусу окна, как решил LLD: событийного источника
// у неё нет, а постоянный опрос ест батарею телефона.
window.addEventListener("focus", () => { refresh().catch(console.error); });
// Возврат на вкладку это тот же повод, а фокуса при нём бывает и нет: телефон
// гасит экран и будит его, вкладка уходит в фон и возвращается, и браузер
// отбивает это одним visibilitychange. Страница, пролежавшая в фоне, иначе
// показывает состояние часовой давности, и человек читает его как настоящее
// (живой случай приёмки DK-716, экран с телефона).
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") refresh().catch(console.error);
});
// Место под таблицу меняется не одной перерисовкой: окно тянут за угол, экран
// поворачивают, панель разговора забирает половину ширины. Ширины при этом
// перекладываются на месте, без пересборки списка: числа лежат переменными
// корня, и поставить их заново дешевле, чем собрать строки.
window.addEventListener("resize", tblWidthsAll);
// Поле поиска в шапке живёт разметкой, а не сборкой экрана: шапка стоит над
// любым из них, и перерисовка доски поле не задевает.
wireFindField(document.getElementById("hq"), document.getElementById("hq-clear"));
// Хват панели разговора и её ширина живут той же разметкой: панель стоит над
// любым экраном, и запомненная ширина ставится до первой отрисовки, чтобы
// открытая по ссылке панель не прыгала с умолчания на своё.
putChatWidth(chatWidth());
wireChatGrab(document.getElementById("cgrab"));
wireFindKey();
wireNewKey();
// Блок квоты рисуется до первого ответа сервера: пустая рамка в подвале
// колонки читалась бы как «подписок нет».
paintQuota();
wireFlash();
// Последний открытый разговор возвращается сам: адрес без хвоста при
// запомненном диалоге дорисовывается им, и человек застаёт дашборд там же, где
// оставил (замечание 19). Закрытый рукой диалог память снимает, и восстанавливать
// тогда нечего.
(() => {
  const last = chatLast();
  if (last && !route().chat) {
    history.replaceState({ chat: last }, "", "#" + chatBase() + "/chat/" + last);
  }
})();
refresh().catch(console.error);
