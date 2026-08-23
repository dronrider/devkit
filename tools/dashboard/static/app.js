// Экраны доски и задачи плюс панель разговора справа: список проектов, живые
// работы, секции со строками, запуск и стоп, журнал витка и лента разговора.
// Клиент только рисует готовый JSON и шлёт команды (решение
// LLD DK-112); все тексты вставляются через textContent, HTML из данных не
// собирается. Стоп называется стопом: возобновление это новый запуск,
// читающий состояние с диска.

const SECTION_ORDER = ["in-progress", "check", "backlog", "blocked"];

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
function viewSnap() {
  const groups = document.getElementById("groups");
  const focus = focusSnap();
  const key = anchorKey(groups, focus);
  const at = key ? findKey(groups, key) : null;
  return {
    groups,
    top: groups ? groups.scrollTop : 0,
    focus,
    key,
    at: at ? at.getBoundingClientRect().top : 0,
  };
}

function viewBack(snap) {
  if (!snap || !snap.groups) return;
  const groups = snap.groups;
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
function outerFail(status, statusText, text) {
  const who = "внешний вход";
  if (status === 413) {
    return "снимок слишком большой для внешнего входа (413): " +
      "уменьшите картинку или отправьте её меньшим куском";
  }
  if (status === 502 || status === 504) {
    return who + " не дозвался дашборда (" + status + "): попробуйте ещё раз";
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
// грумингом и его исходом, "#проект/feed" лента уведомлений. Экран «Агенты»
// проекту не принадлежит и стоит за "#/agents": голое "#agents" отняло бы имя
// у проекта, названного так же.
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
  if (parts.length >= 2 && parts[1] === "new") {
    return { proj: parts[0], id: "", make: true };
  }
  if (parts.length >= 2 && parts[1] === "drafts") {
    return { proj: parts[0], id: parts[2] || "", drafts: true };
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
  nav.replaceChildren();
  sel.replaceChildren();
  for (const p of projects) {
    const item = el("div", "sitem" + (current && p.name === current.name ? " on" : ""));
    const st = projectState(p);
    item.append(el("span", "pdot" + (st.cls ? " " + st.cls : "")));
    item.append(document.createTextNode(p.name));
    const n = el("span", "n", st.short);
    item.append(n);
    item.addEventListener("click", () => { goKeepingChat(p.name); });
    nav.append(item);

    const opt = el("option", "", p.name);
    opt.value = p.name;
    opt.selected = current && p.name === current.name;
    sel.append(opt);
  }
  sel.onchange = () => { goKeepingChat(sel.value); };
  // Счётчик у раздела «Агенты» считает тот же список, что рисует экран: два
  // разных числа рядом читались бы как потерянная работа.
  const live = allWorks(projects).length;
  document.getElementById("nav-agents-n").textContent =
    live ? live + " " + plural(live, "работа", "работы", "работ") : "";
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
async function startRun(project, id, harness, afterOk) {
  sayResult("запуск " + id + (harness ? " на подписке " + harness : "") + "...");
  const body = harness ? { id, harness } : { id };
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/runs",
    { method: "POST", body });
  const said = r.body.message || r.body.error || "";
  if (r.ok && afterOk) {
    await goKeepingResult(afterOk);
    sayResult(said, false);
    return;
  }
  sayResult(said, !r.ok);
  if (r.ok) await refresh();
}

async function stopRun(project, id) {
  sayResult("стоп " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/runs/" + encodeURIComponent(id),
    { method: "DELETE" });
  sayResult(r.body.message || r.body.error || "", !r.ok);
  if (r.ok) await refresh();
}

// Лента живых сессий с экрана проекта ушла: карточки занимали строку над самой
// доской и повторяли раздел «Агенты», где та же работа разобрана подробнее.
// Осталась полоска с числом работ и дорогой туда: сессии живут в двух местах,
// в обзоре агентов и в разговоре (решение пользователя).
function renderLive(project, works) {
  const live = document.getElementById("live");
  const n = (works || []).length;
  if (!n) {
    live.replaceChildren();
    return;
  }
  const line = el("div", "lline");
  line.append(el("span", "dot pulse"));
  line.append(el("b", "", "работает " + n + " " + plural(n, "агент", "агента", "агентов")));
  const go = el("button", "alink", "показать");
  go.type = "button";
  go.addEventListener("click", (ev) => {
    ev.stopPropagation();
    goKeepingChat("/agents");
  });
  line.append(go);
  line.title = "Работы всех досок разобраны в разделе «Агенты»";
  live.replaceChildren(line);
}

// Признак идущей работы стоит в самой строке и приезжает её полем (row.run):
// клиент ничего не сводит со списком работ, потому что сведённый по ID признак
// умел отвечать только про живую сессию с тем же номером, а строка в работе
// без сессии выглядела штатной очередью. Слова признака: работа идёт либо
// сессии за строкой нет.
function runChip(row) {
  // «Сессии нет» и «сессия ждёт ввода» на одной строке противоречили друг
  // другу: первое говорит признак работы (row.run), второе поле ожидания, и
  // источники у них разные. Ждущая строка это и есть строка без хода, поэтому
  // при живом ожидании про пропавшую сессию сказано чипом ожидания, а второй
  // чип снимается (POC ветки poc-chat).
  const waiting = row.waiting && row.waiting.state;
  if (row.run === "gone") return waiting ? null : el("span", "chip", "сессии нет");
  // Идущая работа сказана кружком у номера (rowDot), и чипа со словом
  // «работает» рядом больше нет: одно и то же состояние стояло в строке
  // дважды, зелёной точкой и зелёным чипом.
  return null;
}

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

// Кто ведёт работу: та же тройка случаев, что была в шапке экрана агента.
// Стоп у tmux-сессии дашборда живёт кнопкой полосы действий рядом, а у сессии
// человека его нет вовсе: снимать чужое окно дашборду нечем.
function liveChip(work) {
  if (!work) return null;
  if (work.via === "tmux") return el("span", "chip c-run", "tmux-сессия активна");
  if (work.via === "session") return el("span", "chip c-check", "интерактивная сессия");
  return el("span", "chip", "сессия кончилась");
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
  // Признак работы стоит первым чипом: он про то, что происходит со строкой
  // прямо сейчас, а тип с ценой про то, чем она заведена. Идущую работу чип
  // больше не называет, её говорит кружок у номера.
  const run = runChip(row);
  if (run) chips.push(run);
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
  if (row.fail) chips.push(el("span", "chip c-block", "провал: " + row.fail));
  if (row.block) chips.push(el("span", "chip c-block", "блок: " + row.block));
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

// Ранг в строке это одна сумма: расшифровка слагаемых нужна изредка, а места
// в строке ест столько же, сколько заголовок. На ноутбуке слагаемые приходят
// подсказкой при наведении, на телефоне наведения нет, поэтому та же сумма
// разворачивает их нажатием.
function rankCell(row) {
  const parts = (row.r_parts || []).join("+");
  const cell = el("span", "rank");
  const sum = el("button", "rsum", String(row.r));
  sum.type = "button";
  cell.append(sum);
  if (!parts) return cell;
  sum.title = "R = " + parts + " по RANKING.md";
  sum.setAttribute("aria-expanded", "false");
  sum.setAttribute("aria-label", "ранг " + row.r + ", слагаемые " + parts);
  const fold = el("span", "rfold", parts);
  cell.append(fold);
  sum.addEventListener("click", (ev) => {
    // Нажатие на сумму не уводит внутрь задачи: на телефоне это единственный
    // способ увидеть слагаемые, и переход отнял бы его.
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
const ACTION_BY_SECT = { "in-progress": "Продолжить", check: "Проверить и закрыть" };

function actionLabel(sect) {
  return ACTION_BY_SECT[sect] || "Выполнить";
}

// Статус задачи русским словом: ключи секций приходят с доски
// (taskctl list --json), а на экране статус зовётся так, как о нём говорят.
// Словарь один на весь клиент: два перевода одного ключа разошлись бы врозь.
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
async function loadHarnesses() {
  const r = await api("/api/harnesses");
  harnessView = r.ok ? r.body
    : { harnesses: [], note: r.body.error || "список подписок не прочитан (" + r.status + ")" };
}

function harnesses() {
  return (harnessView && harnessView.harnesses) || [];
}

// Подписка по умолчанию: на неё идёт широкая часть кнопки. Признак ставит
// машинный слой, а без признака берётся первая в списке, чтобы кнопка работала
// и на полураскрытом конфиге.
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

// У цели выбора нет: виток поднимает оболочка цикла своей сессией, и передать
// ей подписку нечем. Сервер отвечает на такой запрос тем же отказом.
const GOAL_HARNESS_TIP = "виток цели поднимает оболочка цикла: он идёт на подписке по умолчанию";

// Строка списка подписок (макет «11 Подписка при запуске»): имя, чип «по
// умолчанию» и остаток квоты. Остаток тут не для красоты, подписку выбирают
// ровно из-за него, а снимок берётся из того же ответа, которым нарисован блок
// квоты в колонке. Снимка нет, значит так и написано, а не нарисован ноль.
// Подписка по умолчанию подсвечена: широкая часть кнопки поднимает работу
// именно на ней, и в списке это видно без чтения подписи.
function harnessRow(h) {
  const row = el("button", "hrow" + (h.default ? " on" : ""));
  row.type = "button";
  const head = el("span", "h1");
  head.append(el("b", "", h.name));
  if (h.default) head.append(el("span", "chip", "по умолчанию"));
  row.append(head);
  const snap = (quotaView && (quotaView.harnesses || []).find((q) => q.name === h.name)) || null;
  const buckets = snap ? (snap.buckets || []) : [];
  if (!snap) {
    row.append(el("span", "hnote", "снимка квоты нет, остаток неизвестен"));
    return row;
  }
  // Две полоски: столько же, сколько стоит в блоке квоты, и больше в строку
  // выбора не влезает даже на ноутбуке.
  for (const b of buckets.slice(0, 2)) row.append(quotaRow(b));
  if (!buckets.length) row.append(el("span", "hnote", snap.note || "бакетов в снимке нет"));
  // Возраст снимка стоит у каждой строки, а не у одной старой: остаток,
  // снятый час назад, и остаток, снятый минуту назад, это разные ответы на
  // вопрос «хватит ли квоты». Возраст виден цветом той же градацией, что и в
  // блоке квоты, а слова «протух» тут нет по той же причине: оно не говорило,
  // насколько всё плохо (замечание 21 двенадцатого круга POC).
  else if (snap.age) {
    row.append(el("span", "hnote qage " + quotaAgeClass(snap.age_sec),
      "снимок " + snap.age + " назад"));
  } else if (snap.stale) {
    // Возрасту верить нельзя вовсе: момента снятия нет либо часы разошлись.
    // Тут остаётся причина словами, её сервер и называет.
    row.append(el("span", "hnote stale", snap.note || "возраст снимка неизвестен"));
  }
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

function runControl(project, id, make, label, isGoal, tip, afterOk, pinned) {
  const wide = make(label);
  if (tip) withTip(wide, tip);
  // Кнопка гаснет до ответа: пока запуск идёт, строка выглядит прежней, и
  // второе нажатие уходило вторым запуском, а возвращалось отказом «работа уже
  // идёт».
  const fire = (node, harness) => {
    node.disabled = true;
    startRun(project, id, harness, afterOk).catch(console.error).finally(() => { node.disabled = false; });
  };
  wide.addEventListener("click", (ev) => {
    ev.stopPropagation();
    fire(wide, pinned || harnessDefault());
  });
  const list = harnesses();
  // Прикреплённая подписка снимает выбор вовсе: список подписок у такой кнопки
  // отвечал не на тот вопрос.
  if (pinned || isGoal || list.length < 2) {
    const why = pinned ? "" : (isGoal ? GOAL_HARNESS_TIP : harnessWhy());
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
  pop.append(el("span", "hph", "На какой подписке запустить"));
  // Список подписок стоит на общем учёте всплывашек: закрывают его те же три
  // пути, что и список кольца.
  let held = null;
  const shut = () => {
    pop.hidden = true;
    more.setAttribute("aria-expanded", "false");
    held = null;
  };
  for (const h of list) {
    const row = harnessRow(h);
    row.addEventListener("click", (ev) => {
      ev.stopPropagation();
      popupDrop(held);
      shut();
      fire(wide, h.name);
    });
    pop.append(row);
  }
  // Подвал говорит, откуда список и насколько выбор действует: без этих двух
  // строк человек не знает, чем список пополнить и не запомнил ли дашборд его
  // выбор на будущее.
  pop.append(el("span", "hfoot",
    "Список включённых подписок машины, agentctl harness. Выбор действует на один запуск."));
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
    if (!pop.hidden) pop.classList.toggle("up", noRoomBelow(more));
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

// Сколько места просит раскрытый список: шапка, две строки с полосками и
// подвал. Точная высота известна только после вставки, а решение о стороне
// принимается до неё, поэтому тут запас по макету.
const HPOP_ROOM = 260;

// Действие прямо со строки: поднять конвейер или снять живую сессию, не заходя
// внутрь задачи. Ручки те же, что у экрана задачи (POST и DELETE runs), и
// ответ выходит в ту же строку результата. Что со строкой сейчас, говорит её
// признак работы, а не поиск по списку работ.
function rowAction(project, row, sect) {
  const live = row.run && row.run !== "gone" && row.run !== "other";
  // Исполнителя не видно: ни привязанной сессии, ни сессии, чей хвост работает
  // этим ID. Запускать строку отсюда значит завести второго исполнителя, а
  // утверждать «другая машина» значит врать: сессия без привязки выглядит так
  // же (жалоба пользователя на DK-481).
  if (row.run === "other") {
    // Скрыт тут только конвейер: второй исполнитель на ту же строку это
    // столкновение, а разговор нет. Обсуждать чужую работу приходится именно
    // отсюда, и прежнее правило заодно с запуском убирало и чат (замечание
    // пользователя).
    return withTip(el("span", "stale", "исполнителя не видно"),
      "на этой машине у задачи нет ни привязанной сессии, ни сессии, которая по " +
      "ней работает: её либо взяли в другом месте, либо ведут окном без привязки. " +
      "Запускать её отсюда поэтому нечем, а поговорить о ней можно, кнопкой чата");
  }
  // Работа наша, просто идёт не нашей tmux-сессией, а живым чатом: подпись
  // «ведёт другая сессия» врала, а вход в разговор прятала. Кнопки тут те же,
  // что доступны в панели: разговор и продолжение.
  if (live && row.run !== "tmux") return rowChatActions(project, row);
  if (!live && row.after && row.after.length) {
    // Заблокированную маркером задачу конвейер брать не должен, и кнопка
    // говорит это сама: погашенная с причиной понятнее исчезнувшей.
    const wait = el("button", "btn btn-sm", actionLabel(sect));
    wait.disabled = true;
    return withTip(wait, "сначала " + row.after.join(", "));
  }
  // Наши сессии есть, но все кончились: запускать конвейер поверх них нечего,
  // разговор продолжается той же кнопкой, что в панели.
  if (row.run === "gone") return rowChatActions(project, row);
  if (!live) {
    // Строка списка остаётся на доске и после нажатия (DK-316): экран не
    // уезжает из-под пальца, и afterOk тут не передаётся. Заказ дословно всё
    // равно виден по наведению.
    const pin = checkPin(Object.assign({ sect: sect }, row));
    const hint = pin ? checkTip(Object.assign({ sect: sect }, row))
      : orderHint(row.order, row.accept, sect, row.id);
    return runControl(project, row.id, (label) => el("button", "btn btn-sm btn-acc", label),
      actionLabel(sect), /^Цель:/.test(row.title), hint, "", pin);
  }
  const btn = el("button", "btn btn-sm btn-danger", "Стоп");
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    // Кнопка гаснет до ответа: пока стоп идёт, строка выглядит прежней, и
    // второе нажатие уходило вторым запросом.
    btn.disabled = true;
    stopRun(project, row.id).catch(console.error).finally(() => { btn.disabled = false; });
  });
  // Стоп это всегда одиночная кнопка без стрелки: обёртка без класса split
  // ради стиля не нужна (CSS достаёт кнопку descendant-селектором, .trow
  // .meta .btn), но нужна ради глубины. Составная кнопка запуска держит
  // span.split (wide, more, pop), и позиционный путь focusSnap/focusBack
  // (строки 82-113, DK-316) считает индексы по глубине от .meta: голая кнопка
  // тут стояла бы на уровень выше составного Run, и переход между ними
  // промахивался бы мимо кнопки. Вырожденный Run (runControl, ниже) той же
  // причины ради тоже обёрнут в такой же пустой span, а не отдаёт голую
  // кнопку: глубина одна для всех сочетаний Run/Стоп на одной строке.
  const grp = el("span");
  grp.append(btn);
  return grp;
}

// opts.quiet это тихая подача строки, ждущей чужой задачи: она стоит в Blocked
// нижним ярусом и не должна спорить с парковками, у которых человека и правда
// ждут.
function renderRow(project, row, sect, opts) {
  const quiet = Boolean(opts && opts.quiet);
  const tr = el("div", "trow" + (quiet ? " rwait" : ""));
  // Кружок состояния живёт внутри ячейки номера, а не отдельной колонкой:
  // сетка строки трёхколоночная, и четвёртый элемент разъехался бы по ней.
  const idc = el("span", "id");
  const dot = rowDot(project, row);
  if (dot) idc.append(dot);
  idc.append(el("span", "", row.id));
  tr.append(idc);
  const tt = el("span", "tt");
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
  tr.append(tt);
  const meta = el("span", "meta");
  meta.append(rankCell(row));
  // Дата последней правки вместо возраста днями: считает её taskctl по git
  // blame, клиент только показывает. Слова «правка» рядом с датой нет,
  // объяснение пришло подсказкой по наведению.
  if (row.moved) {
    meta.append(withTip(el("span", "stale dashed", row.moved),
      "дата последней правки задачи на доске: перевод в статус двигает её же"));
  }
  meta.append(rowChatBtn(project, row));
  meta.append(rowAction(project, row, sect));
  tr.append(meta);
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
  if (sect === "backlog" && !quiet) wireDrag(project, tr, row);
  return tr;
}

// Отпечаток строки доски: всё, из чего она нарисована, вместе с секцией, от
// которой идёт подпись кнопки. Живая работа в отпечаток входит сама, полем
// строки. У строки, где не изменилось ничего, узел переживает обновление
// нетронутым вместе с фокусом на кнопке.
function rowSign(row, sect) {
  return JSON.stringify(row) + "|" + sect + "|" + harnessSign();
}

// Отпечаток списка подписок: им нарисована кнопка запуска, и строка, не
// знающая про смену списка, держала бы стрелку выбора там, где выбирать уже
// нечего, до самой перезагрузки страницы.
function harnessSign() {
  return harnesses().map((h) => h.name).join(",") + "|" + ((harnessView && harnessView.note) || "");
}

// Разделы доски на телефоне разложены по двум табам: In progress и Check это
// то, что уже двинулось и ведётся сессиями, Backlog и Blocked это очередь.
// Четыре секции подряд на экране в 390 пикселей не читаются: до бэклога надо
// прокрутить чужую работу, а сам список у бэклога длиннее всех.
function sectionTab(key) {
  return key === "backlog" || key === "blocked" ? "back" : "sess";
}

// Класс блока раздела: секция помечена своим табом и гасится стилями телефона,
// когда открыт другой. Секции не выкидываются из разметки, потому что на
// ноутбуке табов нет вовсе и там видны все сразу.
function sectionClass(base, key, tab) {
  return base + " bsec" + (sectionTab(key) === tab ? " onsec" : "");
}

// Какая половина доски открыта на телефоне: сессии (In progress и Check) либо
// задачи (Backlog и Blocked). На ноутбуке видны обе, и признак там не при чём.
// Умолчание это сессии: экран доски открывают, чтобы посмотреть на идущую
// работу, а не на очередь.
let boardTab = "sess";

// Открытый таб отмечается по месту, без перерисовки доски: список уже собран,
// и пересобирать его ради подсветки значило бы ронять прокрутку и фокус.
function markBoardTab() {
  const groups = document.getElementById("groups");
  for (const node of groups.querySelectorAll(".bsec")) {
    node.classList.toggle("onsec", node.dataset.tab === boardTab);
  }
  const bar = groups.querySelector(".ktabs");
  if (!bar) return;
  const now = boardKindNow("tasks");
  for (const btn of bar.children) {
    btn.classList.toggle("onktab", btn.dataset.kind === now);
  }
}

// Заведение задачи на телефоне это плавающий плюс над нижними вкладками:
// полоса кнопок съедала полэкрана ещё до первой строки доски.
function newTaskFab(project) {
  const btn = el("button", "fab", "+");
  btn.type = "button";
  btn.title = "Новая задача в " + project;
  btn.setAttribute("aria-label", "Новая задача в " + project);
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    goKeepingChat(project + "/new");
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
        const head = el("div", "btier" + (quiet ? " quiet" : ""), label);
        head.append(el("span", "n", String(list.length)));
        return head;
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

// Табы экрана доски. На ноутбуке их два, задачи и накопитель. На телефоне
// между ними встаёт третий, «Сессии»: прежде тем же самым занимался свой
// переключатель под полосой табов, и два ряда переключателей подряд отвечали
// на один вопрос, «что показать» (замечание пользователя). Раздел «Агенты»
// живёт своей вкладкой и на телефоне, и на ноутбуке, третьего таба на широком
// экране не просит.
function boardKinds() {
  const kinds = [["tasks", "Задачи"], ["drafts", "Черновики"]];
  if (narrowScreen()) kinds.splice(1, 0, ["sess", "Сессии"]);
  return kinds;
}

// Какой таб открыт: у накопителя свой адрес, а задачи с сессиями это два вида
// одного экрана доски, и разводит их полоса секций (boardTab).
function boardKindNow(kind) {
  if (kind === "drafts") return "drafts";
  return narrowScreen() && boardTab === "sess" ? "sess" : "tasks";
}

function boardKindBar(project, kind) {
  const bar = el("div", "ktabs");
  const now = boardKindNow(kind);
  for (const [key, label] of boardKinds()) {
    const btn = el("button", "ktab" + (key === now ? " onktab" : ""), label);
    btn.type = "button";
    btn.dataset.kind = key;
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      // Открытое считается на нажатии, а не на сборке полосы: половины доски
      // переключаются по месту, узлы кнопок при этом живут дальше, и знание,
      // снятое при сборке, устаревало после первого же переключения.
      if (key === boardKindNow(kind)) return;
      if (key === "drafts") {
        goKeepingChat(project + "/drafts");
        return;
      }
      // Задачи и сессии это один экран: с накопителя туда ведёт переход, а на
      // самом экране меняется только показанная половина, по месту и без
      // перерисовки списка.
      boardTab = key === "sess" ? "sess" : "back";
      if (kind === "drafts") {
        goKeepingChat(project);
        return;
      }
      markBoardTab();
    });
    bar.append(btn);
  }
  return bar;
}

function renderBoard(project, board) {
  const groups = document.getElementById("groups");
  const items = [{
    key: "board-kind",
    // Отпечаток несёт открытую половину и ширину экрана: от первой зависит
    // подсветка таба, от второй сам их набор (на телефоне табов три). Без
    // этого перерисовка оставляла бы на экране полосу, собранную по-старому.
    sign: [project, boardKindNow("tasks"), narrowScreen()].join("|"),
    make: () => boardKindBar(project, "tasks"),
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
    items.push({
      key: "head-" + key,
      sign: sec.title + "|" + secRows.length,
      make: () => {
        const head = el("div", sectionClass("shead", key, boardTab), sec.title);
        head.dataset.tab = sectionTab(key);
        // Backlog стоит по рангу, и счётчик говорит это же: надписью под
        // формой задачи порядок объяснять больше не надо.
        head.append(el("span", "n", secRows.length + (key === "backlog" ? ", по рангу" : "")));
        return head;
      },
    });
    const rows = key === "blocked" ? blockedItems(project, parked, heldRows)
      : secRows.map((row) => ({
        key: row.id,
        sign: rowSign(row, key),
        make: () => renderRow(project, row, key),
      }));
    if (!rows.length) {
      rows.push({ key: "empty", sign: "", make: () => el("div", "empty", "Нет.") });
    }
    // Отпечаток карточки собран из отпечатков строк: не изменилась ни одна,
    // значит в карточку можно не заходить вовсе.
    items.push({
      key: "card-" + key,
      sign: rows.map((r) => r.key + "=" + r.sign).join("\n"),
      make: () => {
        const card = el("div", sectionClass("card", key, boardTab));
        card.dataset.tab = sectionTab(key);
        sync(card, rows);
        return card;
      },
      fill: (card) => { sync(card, rows); },
    });
  }
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
    const mark = el("div", cls, text);
    const at = gap < rest.length ? findKey(drag.card, rest[gap].id) : null;
    drag.card.insertBefore(mark, at);
    drag.slots[gap] = mark;
    drag.marks.push(mark);
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
  drag.tag = el("span", "dtag", "");
  drag.tr.append(drag.tag);
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

// Кнопка заведения на доске проекта: мысль приходит вне машины, а не в тот
// момент, когда открыта нужная доска. На главной такой полосы больше нет,
// заведение живёт плюсом у самой карточки проекта.
function newTaskButton(project, label) {
  const btn = el("button", "btn btn-acc", label);
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    goKeepingChat(project + "/new");
  });
  return btn;
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

function linkTaskRow(project, t) {
  const row = el("div", "srow clicky");
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
  row.addEventListener("click", () => { goKeepingChat(project + "/" + t.id); });
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
    const top = el("div", "foldh");
    top.append(el("b", "", sec.name));
    top.append(el("span", "", lineWord(sec.lines.length)));
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

// Где поднимется работа: подпись кнопки называет заказ дословно, а надпись
// рядом про место, откуда за ним смотреть.
function taskActionHint(isGoal, row, id) {
  if (isGoal) {
    return "Цель поведёт оболочка goal-run в tmux-сессии goal-" + id +
      ", состояние следующий виток прочтёт с диска.";
  }
  const tip = orderHint(row.order, row.accept, row.sect, id);
  if (row.sect === "check" && row.accept === "user") return tip;
  const hint = tip || ("Задачу поднимет headless-сессия конвейера доски в tmux-сессии task-" + id + ".");
  return row.sect === "check"
    ? hint + " Агентский сценарий он прогонит сам, пользовательский оставит человеку."
    : hint;
}

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
// конвейера. Экран после удачи уходит в чат: смотреть за продолжением надо там.
async function continueTask(project, id) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/tasks/" + encodeURIComponent(id) + "/continue", { method: "POST", body: {} });
  if (!r.ok) {
    sayResult(r.body.error || "продолжить не вышло", true);
    return;
  }
  // Сервер сам решил, что делать: разбудил живую сессию, поднял резюм или
  // завёл первый чат. Экрану остаётся сказать это словами и открыть чат.
  if (r.body.message) sayResult(r.body.message);
  if (r.body.way === "fresh") {
    // ID сессии рождается позже команды: чат встанет в списке первым ходом, а
    // пока открывается список задачи.
    openChat(chatAddr(project, id));
    return;
  }
  openChat(chatAddr(project, r.body.session || id));
}

function taskActions(project, id, row, works) {
  const out = [];
  const isGoal = /^Цель:/.test(row.title);
  const work = (works || []).find((w) => w.id === id);
  // Входа в разговор с экрана задачи больше нет (POC ветки poc-chat): окно
  // чатов открывает один значок в шапке дашборда, и открытое с задачи оно
  // фильтрует список по ней. Кнопка рядом с действиями строки заводила ещё
  // одну дорогу в то же место и путала разговор с работой.
  if (work && work.via === "tmux") {
    const stop = barBtn("btn btn-danger", "Остановить агента", "i-stop");
    // Последствия остановки живут подсказкой на самой кнопке: надписью рядом
    // они стояли указкой над всей полосой.
    withTip(stop, STOP_TIP);
    stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
    out.push(stop);
    return out;
  }
  // Работа наша, но не нашей tmux-сессией: снимать нечем, а разговаривать и
  // продолжать есть чем, и делается это в панели. Подписи «ведёт другая
  // сессия» тут больше нет: она врала про чужую машину (замечание
  // пользователя).
  if (work) return out;
  // Задачу взяли в другом месте: наших сессий у неё нет ни одной, и запускать
  // её отсюда значит завести второго исполнителя на ту же строку.
  if (row.run === "other") {
    out.push(el("span", "hint", "Исполнителя задачи на этой машине не видно: " +
      "ни привязанной сессии, ни сессии, чья работа идёт по этому ID. Её либо " +
      "взяли в другом месте, либо ведут окном без привязки, и запускать её " +
      "отсюда нечем: это был бы второй исполнитель на ту же строку. Поговорить " +
      "о ней можно: окно чатов в шапке открывается с фильтром по задаче, и новый " +
      "разговор поднимается у нас, конвейера он не трогает."));
    return out;
  }
  const label = actionLabel(row.sect);
  if (row.after && row.after.length) {
    // Заблокированную маркером задачу конвейер брать не должен: кнопка стоит
    // погашенной с причиной, а не пропадает с полосы.
    const wait = barBtn("btn", label, "i-play");
    wait.disabled = true;
    out.push(withTip(wait, "сначала " + row.after.join(", ")));
    out.push(el("span", "hint", "Задача ждёт " + row.after.join(", ") +
      ": пока маркер стоит, конвейер её не возьмёт."));
    return out;
  }
  // Удачный запуск ведёт на экран этой работы: до DK-286 нажатие оставляло
  // человека на прежнем месте, а работа уходила в tmux-сессию незримо.
  // Проверенная строка с пользовательской приёмкой закрывается тут же, без
  // сессии агента, и вести после неё некуда.
  const closesWithoutSession = row.sect === "check" && row.accept === "user";
  const afterOk = closesWithoutSession ? "" : taskChatHash(project, id);
  if (label === ACTION_BY_SECT["in-progress"]) {
    // Продолжение работы переехало в чат отдельной кнопкой рядом с отправкой
    // (замечание 10): продолжают её оттуда же, откуда разговаривают, а полоса
    // с одной кнопкой на экране не нужна. Цель тут не исключение: её
    // диспетчерскую сессию продолжают той же кнопкой, и до этого круга полоса
    // у цели оставалась стоять.
    return out;
  }
  const pin = checkPin(row);
  out.push(runControl(project, id, (name) => barBtn("btn btn-acc", name, "i-play"), label, isGoal,
    pin ? checkTip(row) : orderHint(row.order, row.accept, row.sect, id), afterOk, pin));
  let hint = taskActionHint(isGoal, row, id);
  // Причина, по которой выбирать не из чего, стоит в той же подписи под
  // полосой: на широком экране место для неё есть, и подсказкой по наведению
  // она бы там пряталась.
  const why = isGoal ? GOAL_HARNESS_TIP : harnessWhy();
  if (why) hint += " " + why.charAt(0).toUpperCase() + why.slice(1) + ".";
  out.push(el("span", "hint", hint));
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
  const row = el("div", "srow" + (task.done ? " done" : ""));
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
  if (task.note) meta.append(el("span", "stale", task.note));
  row.append(meta);
  // Закрытая задача уехала в архив, и экрана задачи у неё нет: строка доски
  // читается только у живой.
  if (!task.done && task.section) {
    row.addEventListener("click", () => { goKeepingChat(project + "/" + task.id); });
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
    const back = el("span", "crumb-back", step.text);
    back.addEventListener("click", step.go);
    crumb.append(back);
  });
  // Номер второй раз, мелким: на телефоне доска, номер и статус стоят одной
  // строкой, и большой номер рядом с заголовком там прячется стилями.
  for (const chip of cfg.crumbChips || []) crumb.append(chip);
  page.append(keyed(crumb, cfg.key + "-crumb"));
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
  const drop = barBtn("btn", "Отменить правку", "close");
  const sep = el("span", "div");
  const bad = el("div", "error", "");
  save.hidden = true;
  drop.hidden = true;
  sep.hidden = true;
  bar.append(save, drop, sep);
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
  out.save = save;
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
    if (!notes.length && save.hidden && !bad.textContent) {
      if (placed) {
        bar.remove();
        placed = false;
      }
      return;
    }
    if (placed) return;
    // На телефоне полоса идёт под содержимым, на ноутбуке над ним, теми же
    // местами, что держит раскладка экрана.
    if (narrow.matches) page.append(bar);
    else chips.after(bar);
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

async function renderTask(project, works, id) {
  const groups = document.getElementById("groups");
  const r = await api(taskPath(project, id));
  if (taskDraft.id === id && taskDraft.dirty) {
    // В форме лежит правка: перерисовка стёрла бы её вместе с введённым
    // текстом, поэтому экран остаётся как есть, а уехавшая строка отмечается
    // пометкой.
    if (r.ok && taskSeen(r.body) !== taskDraft.seen) taskStale(project, works, id);
    return;
  }
  groups.replaceChildren();
  const board = { text: "Доска " + project, go: () => { goKeepingChat(project); } };

  if (!r.ok) {
    const page = el("div", "tpage");
    const crumb = el("div", "crumb");
    const back = el("span", "crumb-back", board.text);
    back.addEventListener("click", board.go);
    const card = el("div", "card");
    card.append(el("div", "error", r.body.error || "задача не прочиталась"));
    page.append(crumb, card);
    crumb.append(back);
    groups.append(page);
    return;
  }
  const detail = r.body;
  const row = detail.row || {};
  const crumbChips = [el("span", "idsm", row.id)];
  if (row.section) crumbChips.push(el("span", "chip", row.section));
  if (row.moved) {
    crumbChips.push(withTip(el("span", "stale dashed", row.moved),
      "дата последней правки задачи на доске: перевод в статус двигает её же"));
  }

  // Закрытая задача открывается чтением: строки на доске у неё нет, править
  // нечего, и экран показывает заголовок с датой закрытия и файл постановки.
  // Прежде выдача поиска высаживала на такой задаче отказ, и нажатие на
  // найденную строку выглядело сломанным (замечание 4).
  if (row.closed) {
    crumbChips.push(el("span", "chip c-check", "закрыта " + row.closed));
    groups.append(formPage({
      key: "task", project, id, detail, crumb: [board], crumbChips,
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
  const chips = [runChip(row), liveChip(work), stageChip(row), waitChip(row)].filter(Boolean);
  if (isGoal) chips.push(el("span", "chip c-goal", "цель"));
  const tail = [withTip(el("span", "chip dashed" +
    (row.p === "P0" || row.p === "P1" ? " c-p1" : ""), row.p), P_HINT)];
  if (row.fail) tail.push(el("span", "chip c-block", "провал: " + row.fail));
  if (row.block) tail.push(el("span", "chip c-block", "блок: " + row.block));
  const check = checkChip(row);
  if (check) tail.push(check);

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
    key: "task", project, id, detail, crumb: [board], crumbChips,
    num: row.id, titleLabel: "заголовок задачи " + id, form, chips, tailChips: tail, top,
    links: detail.links || null,
    has: { title: true, type: true, cost: true, rank: true, deps: true, chat: true,
      file: true, make: true, pencil: true, read: true },
    after: detail.after || [], blocks: detail.blocks || [],
    actions: taskActions(project, id, row, works),
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
  groups.append(view.page);

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

// Ссылка из реплики: кликается только http и https, а javascript: и data:
// остаются текстом. Чужая вкладка открывается без доступа к нашей (noopener).
function mdLink(text, href) {
  if (!/^https?:\/\//i.test(href)) return document.createTextNode(text);
  const a = el("a", "", text);
  a.href = href;
  a.target = "_blank";
  a.rel = "noopener noreferrer";
  return a;
}

// Строчная разметка: код в обратных кавычках, ссылка скобками, жирный,
// курсив и голый адрес. Разбор идёт по одному совпадению за раз, остаток
// уходит текстовым узлом.
const MD_INLINE = /`([^`]+)`|\[([^\]]+)\]\(([^)\s]+)\)|(\*\*|__)([\s\S]+?)\4|(\*|_)([\s\S]+?)\6|(https?:\/\/[^\s<>"']+)/;

function mdInline(text, into) {
  let rest = String(text);
  for (;;) {
    const m = MD_INLINE.exec(rest);
    if (!m) break;
    if (m.index) into.append(document.createTextNode(rest.slice(0, m.index)));
    if (m[1] !== undefined) {
      into.append(el("code", "", m[1]));
    } else if (m[2] !== undefined) {
      into.append(mdLink(m[2], m[3]));
    } else if (m[5] !== undefined) {
      const b = el("b");
      mdInline(m[5], b);
      into.append(b);
    } else if (m[7] !== undefined) {
      const i = el("i");
      mdInline(m[7], i);
      into.append(i);
    } else {
      into.append(mdLink(m[8], m[8]));
    }
    rest = rest.slice(m.index + m[0].length);
  }
  if (rest) into.append(document.createTextNode(rest));
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

function mdRender(text) {
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
      mdInline(head[2], h);
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
        mdInline(c.trim(), th);
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
          mdInline(c.trim(), td);
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
      mdInline(quote[1], q);
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
      mdInline(item[4], li);
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
      mdInline(line.trim(), li);
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
      mdInline(line, para);
      continue;
    }
    stack.length = 0;
    list = null;
    para = el("p");
    mdInline(line, para);
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

// Свёрнутый блок с разворотом по клику: заголовок остаётся строкой ленты, а
// тело раскрывается на месте. Так показываются размышления и вызовы
// инструментов, которых на экране бывает больше, чем самого разговора. copy это
// текст для кнопки копирования; без него кнопки нет.
function foldEl(cls, head, text, sub, copy) {
  const box = el("div", cls + " fold");
  const top = el("div", "foldh");
  top.append(el("b", "", head));
  if (sub) top.append(el("span", "", sub));
  if (copy) top.append(copyBtn(copy));
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
  body.append(mdRender(item.text || ""));
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
  const atStart = el("div", "feed-start", FEED_START);
  atStart.hidden = true;
  const box = el("div", opts.list || "");
  opts.box.replaceChildren(atStart, box);

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
    const said = item.text.trim();
    for (let i = talk.length - 1; i >= 0; i--) {
      if (isSaid(talk[i]) && String(talk[i].text || "").trim() === said) talk.splice(i, 1);
    }
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
    const marks = feedMarks(talk);
    if (!talk.length) {
      sync(box, empty ? [{ key: "empty", sign: empty, make: () => el("div", "empty", empty) }] : []);
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
          sign: [item.role, item.time, item.text, next.text, item.sub || "", next.fail,
            mark].join("|"),
          make: () => feedRow(safePair(opts.pair, item, next), item, next, mark),
        });
        i++;
        continue;
      }
      items.push({
        key: itemKey(item),
        sign: [item.role, item.time, item.text, item.sub || "", item.fail, marks[i]].join("|"),
        make: () => feedRow(safeItem(opts.item, item), item, null, marks[i]),
      });
    }
    sync(box, items);
    if (bottom) keepBottom(scroll, true);
    else if (prepending) keepPlace(scroll, rest);
    else scroll.scrollTop = top;
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
    const add = items.filter((it) => fresh(it) && keep(it));
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
    row.append(el("span", "chip c-check", "активна"));
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

// Подпись сидит внутри пузыря, справа внизу: снаружи она занимала свою строку
// на каждое сообщение и растягивала ленту вдвое (замечание 6 двенадцатого
// круга POC). Пустая подпись не рисуется вовсе.
function chatBubble(who, text, meta) {
  const wrap = el("div", "msg" + (who === "вы" ? " me" : ""));
  const bb = el("div", "bb");
  bb.append(mdRender(text));
  const said = meta ? who + ", " + meta : who;
  const foot = el("div", "mm", said);
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
// сырой простынёй в моноширинном блоке.
function reportCard(head, text) {
  const box = el("div", "svc fold");
  const top = el("div", "foldh");
  top.append(el("b", "", head));
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

// Ключ записи в ленте: устойчивый ключ сервера («источник:номер в файле»),
// а у ответа без него старый номер. По этому же ключу лента отсеивает
// повторы и просит следующую страницу истории.
function itemKey(item) {
  return item.key ? "k-" + item.key : "seq-" + item.seq;
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

function feedRow(node, item, out, mark) {
  const row = el("div", "frow r-" + (item.role || "") + (item.sub ? " sub" : "") +
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
    const wrap = chatBubble(who, item.text, bits.join(", "));
    if (item.sel) wrap.append(selFold(item.selFile || "постановка", item.sel));
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

function wireChatFeed(project, feed, sid, onItem) {
  chatShotProject = project;
  chatShotSid = sid;
  return wireFeed(project, sid, {
    onItem,
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
// свой экран, а не под предмет разговора. Диапазон закрывает и узкую колонку
// рядом с доской, и половину ноутбучного экрана.
// Диапазон меряет ленту, а не панель целиком: колонки списка разговоров в
// панели больше нет, и --cw достаётся ленте без остатка. Пока колонка стояла
// призраком, те же 320..640 давали ленте 148..468, и панель почти не
// раздвигалась (замечание 3).
const CHAT_W_KEY = "devkit.chat.width";
const CHAT_W_MIN = 320;
const CHAT_W_MAX = 640;
const CHAT_W_DEF = 420;

function chatClamp(w) {
  return Math.max(CHAT_W_MIN, Math.min(CHAT_W_MAX, Math.round(w) || CHAT_W_DEF));
}

// Ширина уезжает в корень переменной, а не в стиль самой панели: медиазапросу
// узкого экрана переменную перебить нечем, а вот объявление ширины он меняет
// на свою, и панель занимает экран целиком без спора со стилем узла.
function putChatWidth(w) {
  const px = chatClamp(w);
  document.documentElement.style.setProperty("--cw", px + "px");
  return px;
}

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
    localStorage.setItem(CHAT_W_KEY, String(chatClamp(w)));
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
function goKeepingChat(hash) {
  const chat = route().chat;
  location.hash = chat ? hash + "/chat/" + chat : hash;
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
  repaintChatOnly();
}

function closeChat() {
  // Закрытие рукой снимает и память: вернувшись, человек видит дашборд, а не
  // разговор, от которого он ушёл нарочно.
  chatLastSet("");
  // Панель уходит с экрана сразу, до всякой истории и сети: экран под ней уже
  // нарисован, ждать от закрытия нечего вовсе.
  shutChatPanel();
  if (chatDepth > 0) {
    chatDepth -= 1;
    history.back();
    return;
  }
  // Пришли по ссылке снаружи: возвращаться некуда, и панель закрывается
  // заменой адреса на тот экран, что под ней.
  history.pushState({}, "", "#" + chatBase());
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
  pin.replaceChildren();
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
async function chatState(project, addr, board) {
  const st = { addr, sid: "", task: "", chats: [], entry: null, note: "",
    error: "", models: [], fresh: false, lost: false };
  if (chatIsNew(addr)) {
    st.fresh = true;
    st.task = chatNewTask(addr);
  } else if (chatIsTask(addr)) {
    st.task = addr;
  } else if (addr && addr !== CHAT_BOARD) {
    st.sid = addr;
  }
  const r = await api(chatsURL(project) + "?all=1");
  if (!r.ok) {
    st.error = r.body.error || "список чатов не прочитался";
    return st;
  }
  st.chats = r.body.chats || [];
  st.note = r.body.note || "";
  if (r.body.models) st.models = r.body.models;
  // Пришивание застрявшего нового адреса: первая реплика уходила в чат,
  // которого ещё не было, сессия родилась позже (клиент стоял на вопросе в
  // своём терминале), а панель возвращалась на эфемерный адрес new и молчала,
  // хотя транскрипт давно жив. Родившийся диалог узнаётся по имени tmux из
  // персиста реплики либо по самой первой реплике, и панель переезжает на
  // живой sid прямо в этой сборке, без перерисовки.
  if (st.fresh) {
    const sewn = chatSewn(project, addr, st.chats);
    if (sewn) {
      echoMove(project, addr, sewn);
      chatLastSet(sewn);
      history.replaceState({ chat: sewn }, "", "#" + chatBase() + "/chat/" + sewn);
      st.addr = sewn;
      st.sid = sewn;
      st.fresh = false;
      st.task = "";
    }
  }
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
  st.entry = st.chats.find((c) => c.id === st.sid) || null;
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
  const t = (c.title || "").trim();
  if (t) return t.length > 70 ? t.slice(0, 70) + "..." : t;
  return "чат " + c.id.slice(0, 8);
}

function chatWhen(c) {
  return c.mtime ? localDay(c.mtime) + ", " + localTime(c.mtime) : "";
}

// Строка выпадающего списка: заголовок, время, состояние и задачи, которых
// разговор касался. Задач бывает несколько: одна сессия двигает не одну
// строку, и привязка тут один ко многим.
function chatOption(project, c, current) {
  const row = el("div", "cdrow" + (c.id === current ? " on" : ""));
  row.append(withFull(el("b", "", chatTitle(c)), chatTitle(c)));
  const chips = el("div", "cchips");
  // Принадлежность видна первой: список общий по машине, и без имени проекта
  // строки разных досок неотличимы. Свой проект тоже назван, пустое место у
  // части строк читалось бы пропажей.
  chips.append(el("span", "chip c-proj", c.project || project));
  // Живой чат различается занятостью: работает агент или ждёт реплики.
  const busyNow = c.state === "live" && !c.idle;
  chips.append(el("span", "chip" + (busyNow ? " c-run" : ""),
    busyNow ? "работает" : CHAT_STATE_WORD[c.state] || c.state));
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

// Список с поиском: поле сверху, дальше строки всех диалогов машины. Поиск
// идёт по заголовку, по ID сессии, по задачам и по имени проекта, потому что
// ищут диалог всеми четырьмя способами. Фильтр по задаче панели это стартовое
// состояние того же поиска, а не жёсткая отсечка: стереть запрос значит
// увидеть весь список.
function chatDropOpen(project, st, anchor) {
  chatDropShut();
  popupsShut(null);
  const box = el("div", "cdrop");
  const find = el("input");
  find.type = "text";
  find.placeholder = "Поиск чата";
  find.setAttribute("aria-label", "Поиск чата");
  if (st.task && chatFilterOn()) find.value = st.task;
  const rows = el("div", "cdrows");
  const draw = () => {
    const q = find.value.trim().toLowerCase();
    const list = st.chats.filter((c) => {
      if (!q) return true;
      return (c.title || "").toLowerCase().includes(q) ||
        c.id.toLowerCase().includes(q) ||
        (c.tasks || []).join(" ").toLowerCase().includes(q) ||
        (c.project || project).toLowerCase().includes(q);
    });
    rows.replaceChildren();
    for (const c of list) rows.append(chatOption(project, c, st.sid));
    if (!list.length) {
      // Пустому списку словами отвечает сервер, чем бы ни было поле поиска:
      // «ничего не нашлось» имеет смысл, только когда искали среди чего-то.
      rows.append(el("div", "hint", st.chats.length && q ? "по запросу ничего не нашлось" :
        (st.note || "чатов тут нет")));
    }
  };
  find.addEventListener("input", draw);
  draw();
  box.append(find, rows);
  anchor.append(box);
  chatDropSet(box);
  find.focus();
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
  // Живая сессия показывается своей настоящей моделью: клиент уже поднят, и
  // молча показывать выбор поверх работающей модели значило бы врать
  // (замечание пользователя: выбран opus, работает fable). Невступивший выбор
  // виден пометкой рядом и вступает только перезапуском с подтверждением.
  // У мёртвой и новой сессии стоит выбор: он и возьмётся подъёмом или резюмом.
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
  const why = (st.models || []).find((m) => m.model === shown);
  model.title = why ? shown + ": ярус " + why.tier + ", подписка " + why.harness : "Модель агента";
  if (alien) {
    model.disabled = true;
    model.title = "Модель выбрана в самом vscode: с дашборда она сменится только на резюме этого чата.";
  }
  const box = el("div", "cmodel");
  box.append(model);

  const harnessOf = (name) => (((st.models || []).find((m) => m.model === name) || {}).harness) || "";
  const mainHarness = (((st.models || []).find((m) => m.default) || {}).harness) || "";
  let ask = null;
  const askShut = () => { if (ask) { ask.remove(); ask = null; } };
  const backToShown = () => {
    for (const o of model.children) o.selected = o.value === shown;
    model.value = shown;
  };
  // Плашка подтверждения: без него живому чату ничего не меняется. Для
  // разговора на второй подписке кнопки нет, есть честный текст: её заказ
  // явную модель не несёт (модель называет сама подписка), а история
  // разговора живёт в профиле подписки, и на другой её не продолжить.
  const restartAsk = (pick) => {
    askShut();
    ask = el("div", "cnote modeln");
    const second = harnessOf(shown) && harnessOf(shown) !== mainHarness;
    const cross = harnessOf(pick) !== harnessOf(shown);
    if (second || cross) {
      ask.append(el("b", "", "Модель задаёт подписка"));
      const who = harnessOf(shown) || harnessOf(pick);
      ask.append(el("span", "", second
        ? "Разговор идёт на подписке " + who + ": модель называет она сама, явную модель " +
          "её заказ не несёт, и перезапуск её не сменит. Нужная модель выбирается " +
          "подъёмом нового чата."
        : "История разговора живёт в профиле подписки " + who + ", и на другой подписке " +
          "её не продолжить. Нужная модель выбирается подъёмом нового чата."));
      const okBtn = el("button", "btn btn-sm", "Понятно");
      okBtn.type = "button";
      okBtn.addEventListener("click", (ev) => { ev.stopPropagation(); backToShown(); askShut(); });
      ask.append(okBtn);
    } else {
      ask.append(el("b", "", shown + " -> " + pick + ", перезапустить?"));
      ask.append(el("span", "", "Живой клиент остаётся на своей модели, смена вступает " +
        "перезапуском: сессия снимется и поднимется резюмом с новой моделью, история сохранится."));
      const go = el("button", "btn btn-sm btn-acc", "Перезапустить с " + pick);
      go.type = "button";
      go.addEventListener("click", (ev) => {
        ev.stopPropagation();
        go.disabled = true;
        modelRestart(project, st, pick).catch(console.error).finally(() => { go.disabled = false; });
      });
      const drop = el("button", "btn btn-sm", "Отмена");
      drop.type = "button";
      drop.addEventListener("click", (ev) => { ev.stopPropagation(); backToShown(); askShut(); });
      const row = el("div", "mrow");
      row.append(go, drop);
      ask.append(row);
    }
    box.append(ask);
  };
  model.addEventListener("change", () => {
    const pick = model.value;
    if (!st.sid || !isLive) {
      // Сессии нет либо она кончилась: выбор действует сам, его возьмёт
      // ближайший подъём или резюм, как и раньше.
      chatModelSet(pick);
      if (!st.sid) {
        sayResult("модель нового чата: " + pick);
        return;
      }
      api(chatsURL(project) + "/" + encodeURIComponent(st.sid) + "/model",
        { method: "POST", body: { model: pick } })
        .then((r) => { sayResult(r.body.message || r.body.error || "", !r.ok); })
        .catch(console.error);
      return;
    }
    if (pick === shown) {
      askShut();
      return;
    }
    restartAsk(pick);
  });
  // Сохранённый выбор, не совпавший с живой моделью, виден ожидающим: клик по
  // пометке открывает ту же плашку перезапуска.
  if (isLive && !alien && cur && shown && cur !== shown) {
    const pend = el("button", "cdpend", shown + " -> " + cur);
    pend.type = "button";
    pend.title = "Выбрана " + cur + ", работает " + shown + ": смена вступит перезапуском или резюмом";
    pend.addEventListener("click", (ev) => { ev.stopPropagation(); restartAsk(cur); });
    box.append(pend);
  }
  return box;
}

// modelRestart перезапускает живой разговор с новой моделью: записывает выбор,
// снимает сессию целиком (не Escape и не клин, свой род стопа) и поднимает тот
// же разговор резюмом. Резюм берёт модель из памяти диалога, поэтому запись
// выбора идёт первой.
async function modelRestart(project, st, pick) {
  sayResult("перезапускаю разговор с " + pick + "...");
  const at = chatsURL(project) + "/" + encodeURIComponent(st.sid);
  const set = await api(at + "/model", { method: "POST", body: { model: pick } });
  if (!set.ok) {
    sayResult(apiSaid(set), true);
    return;
  }
  const drop = await api(at + "/stop", { method: "POST", body: { drop: true } });
  if (!drop.ok) {
    sayResult(drop.body.error || "сессия не снялась", true);
    return;
  }
  const r = await api(at + "/say", { method: "POST", body: { text: CHAT_REMODEL } });
  sayResult(apiSaid(r), !r.ok);
  if (r.ok && r.body.way === "resume") chatWait(project, r.body.tmux).catch(console.error);
  await repaintChat();
}

// Реплика перезапуска со сменой модели: своих слов человек не говорил, и
// говорить за него нельзя, это заказ продолжения.
const CHAT_REMODEL = "Разговор перезапущен со сменой модели. " +
  "Продолжай с того места, где остановился.";

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
// Зазор между сегментами в единицах длины дуги: тот же, что в макете.
// Дорожка бегущей дуги: она идёт внутри шкалы, чтобы не закрывать деления.
const RING_SPIN = 11.4;
const RING_SPIN_LEN = 2 * Math.PI * RING_SPIN;

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
    "stroke-width": 3.2, "stroke-linecap": "butt",
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

// Деления кольца это пункты плана сессии, как их написал сам агент вызовом
// TodoWrite: закрашенное сделано, подсвеченное идёт, пустое ждёт. Плана нет,
// значит делений нет вовсе: ровная дорожка честнее выдуманной шкалы.
function ringPlan(box, plan) {
  const done = plan.filter((it) => it.state === "completed").length;
  // План выполнен целиком: кольцо замыкается одной дугой. Щели между
  // делениями тут читались бы как незакрытые пункты, которых нет.
  if (done === plan.length) {
    box.append(ringArc("seg on", RING_LEN, 0));
    return;
  }
  if (plan.length > RING_MAX_SEGS) {
    const step = RING_LEN / plan.length;
    box.append(ringArc("seg", RING_LEN, 0));
    if (done > 0) box.append(ringArc("seg on", RING_LEN * (done / plan.length), 0));
    // Длинный план без единого закрытого пункта рисовался ровной дорожкой, и
    // кольцо было не отличить от кольца без плана: человек дописывал пункт за
    // пунктом и не видел на экране ничего (жалоба пользователя). Идущий пункт
    // отмечен засечкой, и она ползёт по кольцу с каждым закрытым.
    const at = plan.findIndex((it) => it.state === "in_progress");
    if (at >= 0) box.append(ringArc("seg here", Math.max(step - 1, 1), at * step));
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
    const asleep = !next || next.state === pulseEmptyState || next.state === pulseSilentState;
    const ghost = !plan.length && asleep;
    wrap.className = "ringwrap r-" + ((next && next.state) || "empty") +
      (ghost ? " ghost" : "") + (open ? " open" : "");
    if (ghost) g.append(ringArc("ghost", RING_LEN, 0));
    else if (plan.length) ringPlan(g, plan);
    else ringTrack(g);
    // Бегущая дуга: она и значит, что события текут. Крутит её анимация, а не
    // опрос, поэтому между заходами на сервер кольцо не замирает.
    const comet = svgEl("circle", "comet");
    svgAttrs(comet, {
      cx: 18, cy: 18, r: RING_SPIN, fill: "none", "stroke-width": 1.6,
      "stroke-linecap": "round",
      "stroke-dasharray": (RING_SPIN_LEN * 0.17).toFixed(2) + " " + (RING_SPIN_LEN * 0.83).toFixed(2),
    });
    g.append(comet);
    box.append(g);
    // В середине стоят работающие, а у ждущего кольца ждущие. Простаивающие
    // сюда не идут: сложенные с работающими они врали, что работа кипит, тогда
    // как второй разговор задачи стоит без хода второй час.
    const num = ringNumber(next);
    if (num) {
      const node = svgEl("text", "rnum");
      svgAttrs(node, { x: 18, y: 18, "text-anchor": "middle", "dominant-baseline": "central" });
      node.textContent = num;
      box.append(node);
    }
    fillPop(pop, project, next);
    wrap.replaceChildren(box, pop);
    // Подсказка у кольца одна, всплывающим списком: браузерная подсказка поверх
    // него говорила то же самое вторым разом и перекрывала сам список.
    const tip = [ringTally(next), pulseWords(next, Date.now())].filter(Boolean).join(". ");
    wrap.setAttribute("aria-label", tip);
  };
  wrap.ringFill(p);
  return wrap;
}

function ringNumber(p) {
  if (!p) return "";
  if (p.state === "waiting") return String(p.waiting || 1);
  return p.working > 0 ? String(p.working) : "";
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

function wireRing(project, st, slot) {
  const put = (p) => {
    // Узел кольца переживает тик: пересборка снимала бы открытый список.
    const has = slot.children && slot.children[0];
    if (has && has.ringFill) {
      has.ringFill(p);
      return;
    }
    slot.replaceChildren(pulseRing(project, p));
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
const CHAT_BIND_HINT = "Привязка ложится в реестр чатов: разговор станет работой " +
  "этой задачи и встанет в её ленту. Пустое значение снимает привязку.";

function chatBindOpen(project, st, line) {
  chatDropShut();
  const menu = el("div", "cdrop cdbind");
  menu.append(el("div", "dwhy", "К какой задаче этот разговор"));
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
  const go = el("button", "btn btn-sm btn-acc", "Привязать");
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
  menu.append(el("div", "hint", CHAT_BIND_HINT));
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
      goKeepingChat(project + "/" + st.task);
    });
    pick.append(lab);
  }
  // У каждого состояния панели своё имя, «чата нет» не говорит ни одно: у
  // нового чата диалога ещё нет по замыслу, протухший адрес назван находкой
  // честно, старый разговор глубже видимого списка подписан своим ID, а пустой
  // проект говорит про пустоту, не про поломку (замечания пользователя).
  const picked = st.fresh ? "Новый чат"
    : st.lost ? "Чат не найден"
    : st.entry ? chatTitle(st.entry)
    : st.sid ? "чат " + st.sid.slice(0, 8)
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
    if (!st.task) {
      switchChat(CHAT_NEW);
      return;
    }
    chatDropShut();
    const menu = el("div", "cdrop cdmenu");
    for (const [label, addr] of [
      ["Чат задачи " + st.task, CHAT_NEW + ":" + st.task],
      ["Произвольный чат", CHAT_NEW],
    ]) {
      const opt = el("div", "cdrow", label);
      opt.addEventListener("click", () => { chatDropShut(); switchChat(addr); });
      menu.append(opt);
    }
    line.append(menu);
    chatDropSet(menu);
  });
  line.append(add);

  // Привязка к задаче стоит рядом с заведением нового чата: обе про то, чей
  // это разговор. У пустого адреса привязывать нечего, сессии ещё нет.
  if (st.sid) {
    const bind = el("button", "cdbtn" + (st.task ? "" : " warn"));
    bind.append(icon("i-in"));
    // Чем узнана задача разговора, говорит сервер (bindTask): «задача не с
    // доски проекта», «свободный чат», «говорит о XR-1». Раньше это стояло
    // плашкой под заголовком, а плашка занимала строку под то, что и так
    // сказано значком (замечание пользователя).
    const said = (st.entry && st.entry.note) ? " (" + st.entry.note + ")" : "";
    bind.title = st.task ? "Разговор привязан к " + st.task + said + ": сменить или снять"
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
      if (chatDrop) chatDropOpen(project, st, line);
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
function rowChatBtn(project, row) {
  const talk = el("button", "btn btn-sm btn-ico");
  talk.append(icon("i-chat"));
  withTip(talk, "Чат по задаче");
  talk.setAttribute("aria-label", "Чат по задаче " + row.id);
  talk.addEventListener("click", (ev) => {
    ev.stopPropagation();
    openChat(chatAddr(project, row.id));
  });
  return talk;
}

// Продолжение разговора: сам вход в чат стоит у строки отдельной кнопкой, тут
// остаётся только подъём хода.
function rowChatActions(project, row) {
  const grp = el("span");
  const go = el("button", "btn btn-sm btn-acc", "Продолжить");
  go.addEventListener("click", (ev) => {
    ev.stopPropagation();
    go.disabled = true;
    continueTask(project, row.id).catch(console.error).finally(() => { go.disabled = false; });
  });
  grp.append(go);
  return grp;
}

// Кнопка стопа: красный квадрат в кружке рядом с отправкой. Прерывает ход, а не
// сессию: следующая реплика попадёт в тот же разговор с его памятью, а полное
// завершение живёт на экране задачи и в кнопке остановки конвейера.
function chatStopBtn(project, st) {
  const stop = el("button", "cstop");
  stop.title = "Прервать текущий ход агента: сессия останется жить";
  stop.setAttribute("aria-label", stop.title);
  stop.append(icon("i-stop"));
  stop.addEventListener("click", (ev) => {
    ev.stopPropagation();
    stop.disabled = true;
    stopChat(project, st.sid).catch(console.error).finally(() => { stop.disabled = false; });
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
async function answerTask(project, id, text) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/tasks/" + encodeURIComponent(id) + "/message", { method: "POST", body: { text } });
  sayResult(r.body.message || r.body.error || (r.ok ? "ответ уехал" : "ответ не ушёл"), !r.ok);
  return r.ok;
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
  if (chatWaitsTask(st)) return { kind: "task", off: false, why: "" };
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

// Слово клина приезжает записью чата: считает его сервер, у которого есть и
// реестр процессов, и список tmux-сессий, и время последнего хода.
function chatStuckWord(st) {
  return (st && st.entry && st.entry.stuck) || "";
}

// Плашка клина: что случилось и одна кнопка выхода. Кнопка делает два шага
// подряд, потому что человеку они видятся одним: снимает зависший процесс
// (Escape мёртвому терминалу подать некуда) и поднимает разговор резюмом той же
// сессии. Недоставленные реплики к вводной резюма приклеит сервер.
// Слово третьего рода, дословно как на сервере (stuckAskWord): клиент стоит
// на вопросе в своём терминале, это не клин, и снимать процесс тут нельзя.
const STUCK_ASK_WORD = "ждёт ответа в терминале";

function stuckNote(project, st, word) {
  const note = el("div", "cnote stuckn");
  if (word === STUCK_ASK_WORD) {
    // Вопрос задан в терминале (разрешение, доверие каталогу первого
    // запуска), и ответить можно только там: плашка называет tmux-сессию
    // дословно, чтобы человек знал, куда attach (живой случай chat-13 на
    // второй подписке).
    note.append(el("b", "", "Клиент ждёт ответа в терминале."));
    const tmux = (st.entry && st.entry.tmux) || "";
    note.append(el("span", "", "Агент задал вопрос в своём окне (разрешение " +
      "или доверие каталогу), и ответить можно только там." +
      (tmux ? " Откройте сессию:" : "")));
    if (tmux) note.append(el("code", "attachcmd", "tmux attach -t " + tmux));
    return note;
  }
  note.append(el("b", "", "Чат завис (" + word + ")."));
  note.append(el("span", "", "Ход агента стоит, реплики копятся в очереди, " +
    "которую уже некому разобрать. Нажмите продолжить: зависший процесс снимется, " +
    "а разговор поднимется резюмом с того же места."));
  const go = el("button", "btn btn-sm btn-acc", "Продолжить");
  go.type = "button";
  go.addEventListener("click", (ev) => {
    ev.stopPropagation();
    go.disabled = true;
    unwedge(project, st).catch(console.error).finally(() => { go.disabled = false; });
  });
  note.append(go);
  return note;
}

// Выход из клина: снятие процесса, потом резюм. Второй шаг идёт только после
// удачи первого: подъём резюма поверх живого зависшего клиента завёл бы второго
// агента на тот же разговор.
async function unwedge(project, st) {
  sayResult("снимаю зависший процесс...");
  const kill = await api(chatsURL(project) + "/" + encodeURIComponent(st.sid) + "/stop",
    { method: "POST", body: { kill: true } });
  if (!kill.ok) {
    sayResult(kill.body.error || "процесс не снялся", true);
    return;
  }
  sayResult(kill.body.message || "процесс снят");
  const r = await api(chatsURL(project) + "/" + encodeURIComponent(st.sid) + "/say",
    { method: "POST", body: { text: CHAT_UNWEDGE } });
  sayResult(apiSaid(r), !r.ok);
  if (r.ok && r.body.way === "resume") chatWait(project, r.body.tmux).catch(console.error);
  await repaintChat();
}

// Реплика, которой поднимается разговор после клина. Своих слов человек тут не
// говорил, и говорить за него нельзя: это заказ продолжения, а не его реплика.
const CHAT_UNWEDGE = "Разговор завис, процесс был снят и поднят заново. " +
  "Продолжай с того места, где остановился.";

// Подъём нового диалога и ожидание его ID. Сессия рождается позже команды, и
// ID приходит из реестра по имени tmux-сессии: дашборд опрашивает список, пока
// он не встанет, и переключается на живой диалог сам.
async function chatRaise(project, st, text, model, onTmux) {
  const body = { text, model };
  if (st.task) body.id = st.task;
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

// chatByTmux спрашивает у сервера диалог по имени tmux-сессии: так дашборд
// узнаёт ID сессии, родившейся позже команды подъёма.
async function chatByTmux(project, name) {
  const list = await api(chatsURL(project) + "?tmux=" + encodeURIComponent(name));
  return (list.ok && (list.body.chats || [])[0]) || null;
}

// chatSewLoop опрашивает реестр, пока человек стоит на адресе addr, и
// пришивает панель к найденному диалогу: память адреса подъёма вычищается,
// панель переезжает на живой sid. Уводить человека с другого экрана нельзя,
// поэтому каждый заход сверяется с адресом панели.
async function chatSewLoop(project, name, addr, step, tries) {
  for (let i = 0; i < tries; i += 1) {
    await new Promise((ok) => setTimeout(ok, step));
    if (!addr || route().chat !== addr) return false;
    const hit = await chatByTmux(project, name);
    if (hit) {
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
// помечается причиной, но со счетов не снимается.
async function chatWait(project, name, addr) {
  for (let i = 0; i < 40; i += 1) {
    await new Promise((ok) => setTimeout(ok, 1500));
    const hit = await chatByTmux(project, name);
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
    // причина на пузыре (onHeld) либо свой предельный срок.
    raise() {
      row.hidden = false;
      what.textContent = "сессия поднимается...";
      stop = Date.now() + LIMIT;
      if (poll) clearTimeout(poll);
      poll = null;
    },
    // Запись транскрипта говорит, чем агент занят прямо сейчас: размышления,
    // вызов инструмента, и наконец сам ответ, на котором индикатор гаснет.
    saw(item) {
      if (row.hidden) return;
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
    }));
  } catch (err) {
    // Приватное окно запрещает хранилище: панель тогда живёт без памяти о
    // неушедшем, но работает.
    return [];
  }
}

// Причина у пузыря, пережившего перерисовку или перезагрузку до подтверждения:
// сама отправка успела уйти, но эха из транскрипта панель ещё не видела.
const ECHO_LOST_WHY = "доставка не подтверждена: реплика ушла, но эха из " +
  "транскрипта ещё нет";

function echoWrite(project, addr, list) {
  try {
    // Переживают выгрузку панели не только неушедшие: held ждёт эха из
    // транскрипта, wait это отправка в полёте, и оба обязаны стоять в ленте
    // после перерисовки, пока эхо их не сняло (первая реплика нового чата
    // пропадала с экрана ровно из-за того, что жил только bad).
    const keep = list.filter((m) => m.state === "bad" || m.state === "held" || m.state === "wait")
      .map((m) => ({ text: m.text, wire: m.wire, born: m.born, state: m.state,
        why: m.why || "", tmux: m.tmux || "" }));
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
  const save = () => { if (addr) echoWrite(project, addr, mine); };

  const draw = () => {
    box.replaceChildren();
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
          drop(m);
          // Пустая очередь нового чата гасит плашку подъёма: отменённая
          // первая реплика возвращает панель в чистое состояние.
          if (!mine.length && out.onGone) out.onGone();
        });
        wrap.append(undo);
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
      drop(m);
      again(m.text);
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
      mine.push({ key: "local-" + seq, text: rec.text, wire: rec.wire,
        state: rec.state, why: rec.why, born: rec.born, tmux: rec.tmux, retry: resend });
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
    add(text, retry, wire, sel, pic) {
      seq += 1;
      const m = { key: "local-" + seq, text, wire: wire || text, sel, pic,
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
    held(m, why) {
      if (!mine.includes(m)) return;
      m.state = "held";
      m.why = why;
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
    // Эхо из ленты: та же реплика человека, пришедшая из транскрипта. Сверка по
    // тексту, потому что своего идентификатора у реплики в журнале нет вовсе.
    saw(item) {
      // Чужая реплика эхом не считается, даже придя ролью user: сказал её
      // другой агент, и снимать ею свой пузырь значит терять свою же отправку.
      if (item.role !== "user" || item.who || !item.text) return;
      // Сервер отрезал префикс выделения и вернул слова человека отдельно,
      // поэтому сверять можно и по ним, и по всему отправленному.
      const said = item.text.trim();
      const hit = mine.find((m) => m.text.trim() === said || m.wire.trim() === said);
      if (hit) drop(hit);
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
  // Дожим неушедшего зовёт ту же отправку, что и кнопка: post объявлен ниже,
  // и ссылка на него берётся лениво, чтобы очередь поднялась вместе с панелью.
  const echo = makeEcho(project, pend, feed, st.addr || st.sid,
    (again) => post(again, null, null));
  chatLive.push(echo.clear, echo.stop);

  if (way.why) {
    const note = el("div", "cnote" + (way.off ? " idle" : ""));
    note.append(el("span", "", way.why));
    wrap.append(note);
  }
  // Клин виден плашкой над полем ввода: разговор в нём выглядит работающим,
  // реплики уходят «успешно», а хода нет и не будет, пока зависший процесс не
  // снят. Выход из клина один и стоит тут же кнопкой (инцидент с чатом DK-460).
  const stuck = chatStuckWord(st);
  if (stuck) wrap.append(stuckNote(project, st, stuck));
  const busy = makeBusy(project, wrap);
  // Причина на пузыре гасит плашку работы: агент не работает, и мигать о
  // работе поверх причины значило бы врать.
  echo.onHeld = () => busy.off();
  // Отменённая последняя реплика гасит плашку подъёма: ждать больше нечего,
  // и новый чат возвращается в чистое состояние.
  echo.onGone = () => busy.off();
  // Вернувшийся на панель нового чата человек застаёт то же ожидание, что и
  // до ухода: реплика в полёте держит плашку о подъёме сессии, а не пустоту.
  if (chatIsNew(st.addr) && echo.waiting()) busy.raise();
  // И само ожидание тоже возобновляется: опрос реестра прежней вкладки умер
  // вместе с ней, а реплика в персисте помнит имя tmux своего подъёма. Как
  // только сессия назовётся, панель переедет на живой sid и покажет транскрипт
  // (пришивание, вторая половина chatSewn: там список, тут ещё не родившееся).
  if (chatIsNew(st.addr)) {
    const names = echo.raised();
    if (names.length) {
      chatSewLoop(project, names[names.length - 1], st.addr, 2000, 150).catch(console.error);
    }
  }
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
  const ta = el("textarea");
  ta.placeholder = way.off ? "чат идёт в vscode, пишите там"
    : (way.kind === "task" ? "Ответ задаче " + st.task + "..." : "Написать агенту...");
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
  ta.value = chatDraftRead(st.addr);
  const savedHeight = chatDraftHeight(st.addr);
  if (savedHeight > 0) ta.style.height = savedHeight + "px";
  let draftTimer = null;
  ta.addEventListener("input", () => {
    if (draftTimer) clearTimeout(draftTimer);
    draftTimer = setTimeout(() => { chatDraftWrite(st.addr, ta.value); }, CHAT_DRAFT_WAIT);
  });
  chatLive.push(() => {
    // Уход с разговора дописывает черновик немедленно: отложенная запись до
    // закрытия вкладки могла не успеть.
    if (draftTimer) clearTimeout(draftTimer);
    chatDraftWrite(st.addr, ta.value);
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
  // Выделение подхватывается, пока человек его держит: снял выделение и начал
  // печатать, значит оно уже не при чём.
  const catchSel = () => {
    const live = grabSelection();
    if (live) {
      pinnedSel = live;
      drawClips();
    }
  };
  document.addEventListener("selectionchange", catchSel);
  chatLive.push(() => document.removeEventListener("selectionchange", catchSel));
  // Возврат на разговор показывает то, что при нём приложено: черновик уже
  // вернулся в поле, картинка возвращается блоком.
  drawClips();
  // Продолжить работу задачи можно прямо отсюда: сервер сам решит, будить ли
  // живую сессию каналом или поднимать резюм (ручка /continue).
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

  const post = (text, sel, pic) => {
    // Пузырь встаёт в ленту до похода на сервер, как в мессенджерах: ждать
    // ответа ручки, а потом ещё и записи в транскрипт, значит показывать
    // человеку пустоту в ответ на нажатие.
    // Агенту едет реплика с префиксом выделения, а в ленте пузырь показывает
    // слова человека и пометку: простыня выделения в ленте закрыла бы разговор.
    const wire0 = sel ? selPrefix(sel) + text : text;
    const m = echo.add(text, (again) => post(again, sel, pic), wire0, sel, pic);
    send.disabled = true;
    const done = () => { send.disabled = Boolean(way.off); };
    // Дорога реплики выбрана заранее, а путь вложения приклеивается к ней
    // первой строкой: у всех трёх дорог он один и тот же.
    const go = (wire) => {
      if (way.kind === "task") {
        // Реплика ждущей задаче: ручка кладёт её безадресной строкой во вход.
        // Ленты у такой строки нет, и пузырь тут единственный след ответа,
        // поэтому панель после удачи не перерисовывается: перерисовка стирала
        // пузырь сразу же, и нажатие выглядело так, будто ничего не случилось.
        answerTask(project, st.task, wire)
          .then((ok) => { if (ok) echo.sent(m); else echo.bad(m); })
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
            if (got === "waiting") echo.held(m, CHAT_WAIT_WHY);
            else echo.sent(m);
          })
          .catch((err) => { echo.bad(m); busy.off(); console.error(err); })
          .finally(done);
        return;
      }
      busy.on(st.sid);
      api(chatsURL(project) + "/" + encodeURIComponent(st.sid) + "/say",
        { method: "POST", body: { text: wire } })
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
            sayResult(r.body.error || "реплика не ушла", true);
            return;
          }
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
        .finally(done);
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
    post(text, pinnedSel || grabSelection(), shot);
    pinnedSel = null;
    shot = null;
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
  if (chatStoppable(st)) row.append(chatStopBtn(project, st));
  row.append(send);
  // Порядок узлов и есть положение хвата: полоса стоит первой в коробке, то
  // есть над полем.
  box.append(grip, ta, row);
  wrap.append(box);

  chatLive.push(busy.off);
  if (st.error) {
    say(feed, "error", st.error);
  } else if (st.lost) {
    // Ленту потерянного адреса не открыть, и честнее сказать это словами, чем
    // показать пустоту или ошибку загрузки.
    say(feed, "empty", "разговора с этим адресом в проекте нет");
  } else if (!st.sid) {
    say(feed, "empty", st.fresh
      ? "новый чат: напишите первую реплику, она и поднимет сессию"
      : (st.note || "чатов тут пока нет, заведите новый кнопкой «+»"));
  } else {
    wireChatFeed(project, feed, st.sid, (item) => {
      busy.saw(item);
      echo.saw(item);
    }).catch(console.error);
  }
  return wrap;
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
  const key = project + "|" + (addr || "");
  if (!addr || !project) {
    if (chatOpen) {
      closeChatLive();
      chatDropShut();
      chatOpen = "";
      pin.replaceChildren();
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
  if (!pin.children.length) {
    pin.replaceChildren(el("div", "empty", "чат открывается..."));
  } else if (!String((pin.children[0] || {}).className || "").includes("cswap")) {
    pin.prepend(el("div", "empty cswap", "открывается другой разговор..."));
  }
  const rows = board || await chatBoardOf(project);
  const st = await chatState(project, addr, rows);
  if (gen !== chatGen) return;
  chatShown = { project, sid: st.sid || "", task: st.task || "" };
  // Открытый чат закрепляется за задачей: следующее открытие панели с её
  // экрана вернёт этот же чат, а не первый из списка.
  if (st.task && st.sid) chatTaskLastSet(st.task, st.sid);
  pin.replaceChildren(chatHead(project, st), chatPanel(project, st));
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
const DRAFTS_HINT = "Записанные мимо доски идеи: метаданных у них нет, ранг и " +
  "тип выдаст груминг, он же заведёт строку.";
const GROOM_HINT = "«Провести груминг» поднимает сессию разбора: она доведёт " +
  "запись до строки Backlog либо снимет её с причиной. Ход разбора и его исход " +
  "видны на экране записи.";

// afterOk это хэш экрана записи: заполнен со строки накопителя, до DK-286
// нажатие там уводило на общий экран агента, у которого нет ни текста
// записи, ни исхода разбора (LLD DK-328, «Отвергнутое»). С экрана самой
// записи afterOk не передают, там уже стоит экран этой работы.
async function groomDraft(project, id, ask, afterOk) {
  sayResult("подъём груминга " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/drafts/" + encodeURIComponent(id) + "/groom",
    { method: "POST", body: ask ? { ask } : {} });
  const said = r.body.message || r.body.error || "";
  if (r.ok && afterOk) {
    await goKeepingResult(afterOk);
    sayResult(said, false);
    return true;
  }
  sayResult(said, !r.ok);
  return r.ok;
}

// DRAFT_PRIO переводит уровень разбора в слово чипа: имя уровня латиницей
// живёт в поле prio ответа taskctl, а экран накопителя говорит по-русски, тем
// же словом, каким уровень стоит в файле черновика и в draft list.
const DRAFT_PRIO = { high: "высокий", mid: "средний", low: "низкий" };

// Строка накопителя ведёт на экран записи, а кнопка груминга остаётся и в ней:
// накопитель разбирают пачкой, не заходя внутрь каждой записи (LLD DK-328).
function draftRow(project, d) {
  const row = el("div", "srow clicky");
  row.append(el("span", "id", d.id));
  // Заголовок записи режется той же кромкой, что и заголовок строки доски, и
  // подсказка с полным текстом тут нужна ровно так же: длинную мысль с
  // телефона иначе не прочитать, не заходя внутрь (замечание пользователя).
  row.append(withFull(el("span", "st", d.title || ""), d.title || ""));
  const meta = el("span", "sm");
  meta.append(el("span", "stale", d.age_words || ""));
  if (d.prio) meta.append(el("span", "chip", DRAFT_PRIO[d.prio] || d.prio));
  if (d.deferred) meta.append(el("span", "chip", "отложен " + d.deferred));
  // Черновик это та же задача, просто в черновом исполнении, и обсуждать его с
  // агентом надо тем же способом: кнопка та же, значок тот же, панель
  // открывается с привязкой к его ID (решение пользователя).
  const talk = rowChatBtn(project, d);
  meta.append(talk);
  const groom = el("button", "btn btn-sm btn-acc", "Провести груминг");
  if (d.order) withTip(groom, "Заказ агенту: «" + d.order + "».");
  meta.append(groom);
  row.append(meta);
  row.addEventListener("click", (ev) => {
    if (ev.target === groom || ev.target === talk) return;
    goKeepingChat(project + "/draft/" + d.id);
  });
  groom.addEventListener("click", (ev) => {
    ev.stopPropagation();
    groom.disabled = true;
    // Разбор идёт под тем же ID, каким черновик станет строкой, и смотреть за
    // ним удобнее на экране записи: ход, исход и повторная ходка стоят там.
    groomDraft(project, d.id, "", project + "/draft/" + d.id)
      .catch((err) => { console.error(err); })
      .finally(() => { groom.disabled = false; });
  });
  return row;
}

// Накопитель рисуется после ответа сервера, а не до него: очищенный заранее
// экран моргал бы пустотой на каждом обновлении по фокусу окна, а список уезжал
// бы к началу из-под пальца.
async function renderDrafts(project) {
  const groups = document.getElementById("groups");
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/drafts");
  const items = [{
    key: "board-kind",
    sign: project + "|drafts",
    // Дорога назад к задачам это тот же таб, что и привёл сюда: хлебной
    // крошки над накопителем больше нет, она вела туда же вторым способом.
    make: () => boardKindBar(project, "drafts"),
  }, {
    key: "drafts-bar",
    sign: project,
    make: () => {
      const bar = el("div", "nbar");
      bar.append(newTaskButton(project, "Новая задача"), el("span", "hint", DRAFTS_HINT));
      return bar;
    },
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
  const rows = [{
    key: "drafts-head",
    sign: String(drafts.length),
    make: () => {
      const head = el("div", "chd");
      head.append(el("b", "", "Черновики"));
      head.append(el("span", "cnt", drafts.length + " " +
        plural(drafts.length, "запись", "записи", "записей")));
      return head;
    },
  }];
  // Пустой накопитель говорит словами сервера: пустая карточка неотличима от
  // неотрисованной.
  if (!drafts.length) {
    const note = r.body.note || "черновиков нет";
    rows.push({ key: "empty", sign: note, make: () => el("div", "empty", note) });
  }
  // Ключ строки это ID черновика: обновление по фокусу окна трогает только те
  // строки, что изменились, и список не уезжает из-под пальца.
  for (const d of drafts) {
    rows.push({ key: d.id, sign: JSON.stringify(d), make: () => draftRow(project, d) });
  }
  items.push({
    key: "drafts-card",
    sign: rows.map((row) => row.key + "=" + row.sign).join("\n"),
    make: () => {
      const card = el("div", "card");
      sync(card, rows);
      return card;
    },
    fill: (card) => { sync(card, rows); },
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
  // В разделе «Агенты» поле фильтрует его собственные строки: доска тут ни при
  // чём, и уводить отсюда в выдачу по задачам значит отвечать не на тот вопрос
  // (замечание пользователя).
  if (rt.agents) {
    const q = String(value).trim();
    const base = "/agents" + (q ? "/" + encodeURIComponent(q) : "");
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






// Сохранение записи накопителя: текст правится тем же полем, что и постановка
// задачи, и уезжает целиком одной ручкой. Пустой текст отбивается до похода на
// сервер: он затёр бы запись, а удаление у черновика своё, с причиной.
async function saveDraftText(project, id, text) {
  sayResult("сохранение черновика " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/drafts/" + encodeURIComponent(id), { method: "PUT", body: { text } });
  sayResult(apiSaid(r), !r.ok);
  if (!r.ok) return false;
  taskDraft.dirty = false;
  // Сохранение возвращает просмотр: правка кончилась, и держать поле открытым
  // незачем.
  taskDraft.edit = false;
  await refresh();
  return true;
}

// Исход груминга словами: что случилось и что дальше. Сам след читает сервер
// (ручка /drafts/<id>/outcome), его слова стоят рядом отдельной строкой, и
// придумывать их второй раз на клиенте незачем. Экран POC эту карточку потерял
// вместе с переделкой на общую форму, и разбор кончался молча: человек не
// видел ни заведённой строки, ни вопроса грумера.
const DRAFT_PHASES = {
  row: {
    head: "Черновик оформлен строкой",
    next: "Дальше работа идёт по задаче: ранг и цена у неё уже стоят.",
  },
  attached: {
    head: "Черновик приписан к стоящей строке",
    next: "Дальше работа идёт по задаче-приёмнику, текст записи лежит разделом в её файле.",
  },
  deferred: {
    head: "Черновик отложен",
    next: "Запись осталась в накопителе: груминг вернётся к ней, когда причина отпадёт.",
  },
  dropped: {
    head: "Черновик удалён",
    next: "Записи больше нет, причина удаления лежит сообщением коммита доски.",
  },
  question: {
    head: "Груминг кончился вопросом",
    next: "Ответ уходит новым заходом: агент перечитает черновик вместе с уточнением.",
  },
  open: {
    head: "Груминга не было",
    next: "Разбор поднимается кнопкой «Провести груминг».",
  },
  error: {
    head: "Исход груминга не прочитался",
    next: "Пока сервер не ответил, разбор с экрана не поднимается: причина стоит выше.",
  },
};

// Состояние экрана: живая работа с тем же ID это идущий груминг, а
// кончившийся разбор без единого следа на диске, но со словом агента в
// транскрипте, это вопрос.
function draftPhase(out, running) {
  if (running) return "running";
  const state = (out && out.state) || "open";
  if (state === "open") return out && out.question ? "question" : "open";
  return state;
}

// Карточка исхода: заголовок состояния, слова сервера про след, следующий шаг
// и переход туда, куда груминг увёл запись.
function draftOutcomeCard(project, id, out, phase) {
  const card = el("div", "card dcard");
  const head = el("div", "phd");
  const said = DRAFT_PHASES[phase] || DRAFT_PHASES.open;
  head.append(el("b", "", said.head));
  card.append(head);
  const body = el("div", "dbd");
  if (out.note) {
    body.append(el("div", phase === "error" ? "error" : "dsay", out.note));
  }
  if (phase === "deferred" && out.reason) {
    body.append(el("div", "dwhy", "Причина: " + out.reason));
  }
  body.append(el("div", "hint", said.next));
  if (phase === "row") {
    const go = el("button", "btn btn-sm", "Открыть задачу " + id);
    go.addEventListener("click", () => { goKeepingChat(project + "/" + id); });
    body.append(go);
  }
  if (phase === "attached" && out.task) {
    const go = el("button", "btn btn-sm", "Открыть задачу " + out.task);
    go.addEventListener("click", () => { goKeepingChat(project + "/" + out.task); });
    body.append(go);
  }
  card.append(body);
  return card;
}

const DRAFT_ASK_HINT = "Уточнение уедет заказом новому заходу груминга: " +
  "прежняя сессия кончилась вопросом и своих ходов больше не делает.";

// Карточка вопроса: последнее слово агента разметкой, поле уточнения и
// повторный заход. Без неё вопрос грумера виден только в ленте разговора, а на
// экране записи разбор выглядел брошенным.
function draftAskCard(project, id, question) {
  const card = el("div", "card dcard");
  const head = el("div", "phd");
  head.append(el("b", "", "Вопрос груминга"));
  card.append(head);
  const body = el("div", "dbd");
  body.append(mdRender(question));
  body.append(el("div", "dwhy", "Что ответить грумингу"));
  const field = el("textarea", "dask");
  field.placeholder = "Уточнение для нового захода груминга";
  body.append(field);
  body.append(el("div", "hint", DRAFT_ASK_HINT));
  const again = el("button", "btn btn-acc", "Повторить груминг");
  again.addEventListener("click", () => {
    again.disabled = true;
    groomDraft(project, id, field.value.trim()).then((ok) => {
      again.disabled = false;
      if (ok) {
        field.value = "";
        refresh().catch(console.error);
      }
    }).catch((err) => { again.disabled = false; console.error(err); });
  });
  body.append(again);
  card.append(body);
  return card;
}

// Опрос конца груминга: пока разбор идёт, состояние записи перечитывается по
// кругу, той же механикой, какой панель разговора дожидается доставки реплики.
// Таймер один: следующий заводит очередная перерисовка, пока разбор жив, а
// конец разбора круг обрывает сам, без своего таймера. Уход с экрана снимает
// опрос вместе с остальными живыми потоками (agentLive).
const DRAFT_GROOM_POLL = 3000;
let draftPoll = null;
let draftPollWired = false;
function watchDraftGroom() {
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
  const [text, chats, outcome] = await Promise.all([
    api(base),
    api("/api/projects/" + encodeURIComponent(project) + "/chats?task=" + encodeURIComponent(id)),
    api(base + "/outcome"),
  ]);
  // Неотвеченный исход не выдаётся за «груминга не было»: молчание сервера и
  // нетронутая запись это разные вещи, и причина отказа видна словами.
  const out = outcome.ok ? outcome.body
    : { note: outcome.body.error || "исход груминга не прочитался" };
  const running = Boolean((works || []).find((w) => w.id === id));
  // Конец груминга виден без перезагрузки страницы: пока разбор идёт, экран
  // сам дожидается исхода опросом, и пометка уходит вместе с его приходом.
  // Опрос стоит до сторожа правки: иначе он глох бы на время редактирования.
  if (running) watchDraftGroom();
  // В поле лежит правка: перерисовка по фокусу окна стёрла бы её, и экран
  // остаётся как есть.
  if (taskDraft.id === id && taskDraft.dirty) return;
  const said = text.ok ? String(text.body.text || "") : "";
  // Груминг уже шёл, значит есть его чат: вместо кнопки ссылка туда.
  const groomChat = ((chats.ok && chats.body.chats) || [])[0] || null;
  const phase = outcome.ok ? draftPhase(out, running) : "error";
  sync(groups, [{
    key: "draft-page",
    sign: [id, said, running, groomChat ? groomChat.id : "", text.body.error || "",
      phase, JSON.stringify(out)].join("|"),
    make: () => {
      const form = { text: said };
      const chips = [el("span", "chip", "черновик")];
      if (running) chips.push(el("span", "chip c-run", "груминг идёт"));
      const actions = [];
      if (groomChat) {
        const go = barBtn("btn", "Чат груминга", "i-chat");
        go.addEventListener("click", () => { openChat(chatAddr(project, groomChat.id)); });
        actions.push(go);
      } else if (!running) {
        // Пока разбор идёт, поднять второй нечем: кнопка рядом с пометкой
        // «груминг идёт» звала запустить грумера поверх работающего.
        const groom = barBtn("btn btn-acc", "Провести груминг", "i-play");
        if (text.ok && text.body.order) withTip(groom, "Заказ агенту: «" + text.body.order + "».");
        groom.addEventListener("click", () => {
          groom.disabled = true;
          groomDraft(project, id).then((ok) => {
            groom.disabled = false;
            if (ok) refresh().catch(console.error);
          }).catch((err) => { groom.disabled = false; console.error(err); });
        });
        actions.push(groom);
      }
      // Исход разбора стоит над текстом записи: он и есть ответ на вопрос
      // «чем кончился груминг», ради которого экран открывают. У идущего
      // разбора своей карточки нет: она повторяла бы пометку «груминг идёт»
      // из шапки, а исход появится на её месте сам, опросом выше.
      const lead = [];
      if (phase !== "running") lead.push(draftOutcomeCard(project, id, out, phase));
      if (phase === "question") lead.push(draftAskCard(project, id, out.question));
      return formPage({
        key: "draft", project, id, lead,
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
        has: { file: true, pencil: true, read: true, chat: true },
        penLabel: "Править запись",
        edit: taskDraft.id === id && taskDraft.edit,
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
        onSave: () => { saveDraftText(project, id, form.text).catch(console.error); },
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
const DRAFT_HINT = "Ляжет в docs/tasks/drafts/, ID выдаст taskctl. " +
  "На доске его не будет, пока груминг не заведёт задачу.";
const FULL_HINT = "Встанет в Backlog сразу, место выведется из ранга. " +
  "Файл задачи docs/tasks/<ID>.md заведётся вместе со строкой. " +
  "После заведения откроется карточка задачи.";
// Взять в работу можно только сохранённое: у ненаписанной строки нет ни ID,
// ни статуса, по которому конвейер выбирает заказ. Кнопки запуска на этом
// экране нет вовсе, и сказано это словами, а не погашенной кнопкой.
const NEW_RUN_HINT = "Взять в работу можно с карточки задачи: до заведения " +
  "у неё нет ни ID, ни статуса, от которого конвейер берёт заказ.";
const NEW_PLACEHOLDER = "Что нужно сделать и зачем";
// Вид приёмки выбирается закрытым списком, а не текстом: свободный ввод на
// телефоне дороже двух тапов, а значения всего три (DK-301).
const ACCEPT_VALUES = ["agent", "mixed", "user"];
// Шесть барьеров закрыты LLD DK-292 (решение 1): ключ едет в --barrier как
// есть. Первый пункт списка остаётся пустым: pickField выбирает первый пункт
// сам, и без пустого «выбрать барьер» неотправленная форма уезжала бы
// барьером «глаза», которого человек не называл.
const BARRIER_PLACEHOLDER = "выбрать барьер";
const BARRIER_VALUES = ["", "глаза", "доступ", "необратимость", "секрет", "согласие", "событие"];
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
  parts: [0, 0, 0, 0, 0], accept: "agent", barrier: "", reason: "" };

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

function makeDraft(project, text, btns) {
  return makeNew(project, "/drafts", { text }, btns, "запись черновика...");
}

function makeTask(project, body, btns) {
  return makeNew(project, "/tasks", body, btns, "заведение задачи...");
}

// Подтверждение записанного черновика: ID, путь файла и что с ним будет
// дальше. Уводить с экрана некуда, строки на доске у черновика нет.
function draftDone(project, done) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const card = el("div", "card nform");
  card.append(el("div", "nfhead", "Черновик " + (done.id || "") + " записан"));
  const box = el("div", "nfbody");
  box.append(el("div", "hint", done.file
    ? "Файл " + done.file + " лежит в накопителе, на доске задачи у него нет."
    : "Файл лежит в накопителе, на доске задачи у него нет."));
  box.append(el("div", "hint",
    "Разберёт его груминг: он выдаст ранг с типом и заведёт задачу (taskctl add --id)."));
  const btns = el("div", "tbtns");
  const again = el("button", "btn btn-acc", "Записать ещё");
  again.addEventListener("click", () => {
    resetNewForm(project);
    renderNew(project);
  });
  const board = el("button", "btn", "На доску");
  board.addEventListener("click", () => { goKeepingChat(project); });
  btns.append(again, board);
  box.append(btns);
  card.append(box);
  groups.append(card);
}

function renderNew(project) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  if (newForm.project !== project) resetNewForm(project);

  // Переключатель стоит над шапкой: одна форма, два места, куда ляжет
  // написанное.
  const swch = el("div", "swch");
  const asTask = el("div", newForm.draft ? "" : "on", "Задача");
  const asDraft = el("div", newForm.draft ? "on" : "", "Черновик");
  swch.append(asTask, asDraft);

  // Пометка про груминг стоит только у черновика и говорит сразу обе правды: и
  // чего у него нет, и кто это выдаст.
  const note = el("div", "dnote");
  note.append(el("b", "", DRAFT_NOTE_HEAD), document.createTextNode(" " + DRAFT_NOTE));
  note.hidden = !newForm.draft;

  // Метаданные у черновика гасятся, а не прячутся: видно, чего он лишён, и
  // форма не перестраивается при переключении.
  const typeOff = el("span", "chip", DRAFT_OFF_TYPE);
  const costOff = el("span", "chip", DRAFT_OFF_COST);

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
  box.append(el("div", "hint", P_HINT), acceptBox, el("div", "hint", ACCEPT_HINT),
    barrierHint, reasonField);

  // Взять в работу с формы нечего, и сказано это словами, а не погашенной
  // кнопкой: у ненаписанной строки нет ни ID, ни статуса, по которому конвейер
  // выбирает заказ.
  const hint = el("div", "hint", FULL_HINT);
  const runHint = el("div", "hint", NEW_RUN_HINT);

  let view = null;
  view = formPage({
    key: "new", project, id: "",
    crumb: [{ text: "Доска " + project, go: () => { goKeepingChat(project); } }],
    lead: [swch, note], extra: [card],
    // Форма заведения это та же правка задачи с пустыми полями: правка тут
    // включена всегда, и выключать её нечем, экран для неё и открыт.
    has: { title: true, type: true, cost: true, rank: true },
    titleHint: NEW_PLACEHOLDER, titleTall: true,
    titleLabel: "заголовок задачи или текст черновика",
    tailChips: [typeOff, costOff],
    form: newForm, edit: true, always: true,
    saveLabel: newForm.draft ? "Записать черновик" : "Завести задачу",
    actions: [hint, runHint],
    check: () => {
      if (view) paint();
      // Рубежи те же, что у ручек: поправка на баг не про новую работу, строки
      // без заголовка и черновика без текста не бывает, а у не агентского вида
      // барьер обязателен.
      if (newForm.draft) {
        return { dirty: true, refusal: newForm.title.trim() ? "" : "черновик пустым не бывает" };
      }
      return { dirty: true, refusal: draftRefusal(newForm, null) ||
        (newForm.accept !== "agent" && !newForm.barrier
          ? "у не агентского вида назван барьер из шести: без него приёмка повисает без причины"
          : "") };
    },
    onSave: () => {
      const text = newForm.title.trim();
      if (!text) return;
      const send = view.save;
      if (newForm.draft) {
        makeDraft(project, text, [send]).then((done) => {
          if (!done) return;
          resetNewForm(project);
          draftDone(project, done);
        }).catch(console.error);
        return;
      }
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
      makeTask(project, body, [send]).then((done) => {
        if (!done) return;
        resetNewForm(project);
        // Заведённая строка открывается сразу: с телефона следующий шаг это
        // дописать постановку, а искать её глазами по Backlog неудобно.
        if (done.id) goKeepingChat(project + "/" + done.id);
        else renderNew(project);
      }).catch(console.error);
    },
  });
  groups.append(view.page);

  // Режим меняет подписи и гасит лишнее, но не перестраивает форму: поля
  // остаются на своих местах, а написанное переживает переключение.
  function paint() {
    const draft = newForm.draft;
    asTask.className = draft ? "" : "on";
    asDraft.className = draft ? "on" : "";
    note.hidden = !draft;
    view.typePick.hidden = draft;
    view.costPick.hidden = draft;
    typeOff.hidden = !draft;
    costOff.hidden = !draft;
    view.rank.classList.toggle("off", draft);
    // Приёмку с барьером у черновика заполняет груминг, а у агентского вида
    // барьера нет вовсе: там прячется одно поле, а не вся карточка.
    card.hidden = draft;
    const bare = newForm.accept === "agent";
    barrierPick.hidden = bare;
    barrierHint.hidden = bare;
    reasonField.hidden = bare;
    runHint.hidden = draft;
    view.rankSum.textContent = draft ? "-"
      : String(newForm.parts.reduce((a, b) => a + Number(b), 0));
    view.rankNote.textContent = draft ? DRAFT_OFF_RANK : "= " + newForm.parts.join("+");
    // У черновика подсказка шкалы ни к чему, и на её месте стоит одна подпись
    // про то, кто эти поля заполнит.
    const whys = view.rank.querySelectorAll(".rrow .why");
    RANK_PARTS.forEach((part, i) => {
      whys[i].textContent = draft ? (i === 0 ? DRAFT_OFF_PARTS : "") : part.why;
    });
    for (const pick of view.rank.querySelectorAll(".rrow .pick")) pick.hidden = draft;
    view.save.rename(draft ? "Записать черновик" : "Завести задачу");
    hint.textContent = draft ? DRAFT_HINT : FULL_HINT;
  }

  for (const [node, draft] of [[asTask, false], [asDraft, true]]) {
    node.addEventListener("click", () => {
      newForm.draft = draft;
      sayResult("");
      view.touch();
    });
  }
  view.touch();
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

function flashWorthy(n, since, onFeed) {
  return Boolean(n && n.time) && n.time > since && !onFeed && !flashMuted(n);
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
    if (!flashWorthy(n, flashSince, route().feed)) return;
    flashSince = n.time;
    // Точка на колокольчике загорается тем же событием: ждать фокуса окна,
    // чтобы узнать о случившемся при открытом окне, было бы странно.
    showBellDot(n.time > feedSeen());
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

function makePlus(project) {
  const btn = el("button", "pplus", "+");
  btn.type = "button";
  btn.title = "Завести в " + project;
  btn.setAttribute("aria-label", "Завести в " + project);
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    const had = homeMenu;
    homeMenuShut();
    // Повторное нажатие по тому же плюсу закрывает меню, а не собирает его
    // заново под пальцем.
    if (had && had.dataset.project === project) return;
    // Соседняя всплывашка уходит с открытием этой: два раскрытых списка разом
    // экран не показывает ни в одном месте дашборда.
    popupsShut(null);
    const menu = el("div", "pmenu");
    menu.dataset.project = project;
    // Оба пункта ведут на тот же экран заведения (#проект/new), что и кнопка с
    // доски: форма там одна на оба случая, а пункт меню только выставляет её
    // переключатель.
    for (const [label, draft] of [["Задача", false], ["Черновик", true]]) {
      const opt = el("div", "pmrow", label);
      opt.addEventListener("click", (e) => {
        e.stopPropagation();
        homeMenuShut();
        resetNewForm(project);
        newForm.draft = draft;
        goKeepingChat(project + "/new");
      });
      menu.append(opt);
    }
    btn.parentNode.append(menu);
    homeMenu = menu;
    homeMenuHeld = popupHold(menu, homeMenuShut);
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
function allWorks(projects) {
  const out = [];
  for (const p of projects || []) {
    for (const w of p.works || []) out.push({ project: p.name, work: w });
  }
  return out;
}

// Вид работы словами: чипы отвечают, кто её ведёт и чем она видна. Цель
// названа целью и в интерактивном окне, потому что переписка открыта ровно у
// неё.
function workChips(project, w) {
  const chips = [el("span", "chip", project)];
  if (w.kind === "goal") chips.push(el("span", "chip c-goal", "агент цели"));
  if (w.via === "registry") chips.push(el("span", "chip c-check", "сессия кончилась"));
  // Разговор о задаче назван собой: сессия живая и номер задачи у неё свой, но
  // строку она не ведёт, и кнопок конвейера на доске у той не появляется.
  else if (w.talk) {
    chips.push(withTip(el("span", "chip", "разговор о задаче"),
      "чат задачу не ведёт: строка на доске от него своей не становится"));
  } else if (w.via === "session") chips.push(el("span", "chip", "интерактивная сессия"));
  else if (w.kind !== "goal") chips.push(el("span", "chip", "конвейер задачи"));
  return chips;
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

function agentRow(project, w, now) {
  const row = el("div", "arow");
  const addr = workChatAddr(w);
  const tips = [];
  if (addr) tips.push("Открыть разговор этой работы");
  if (!agentOwn(w)) {
    tips.push("сессия поднята мимо дашборда: остановить работу можно там, где она поднята");
  }
  if (tips.length) row.title = tips.join(". ");
  if (addr) {
    row.classList.add("atalk");
    row.addEventListener("click", () => { openChat(chatAddr(project, addr)); });
  }
  row.append(el("span", "dot" + (w.via === "registry" ? " dot-other" : " pulse")));
  const box = el("div", "ab");
  const line = el("div", "l1");
  // Заголовок задачи идёт первым: имя сессии goal-DK-112 о занятии агента не
  // говорит ничего, и место ему в подписи.
  // Подпись это задача, а у сессии без задачи заголовок чата, который сервер
  // берёт той же лестницей, что список чатов (замечание 1 восьмого круга).
  line.append(el("span", "tt", w.title || w.id || w.note || "чат без задачи"));
  for (const chip of workChips(project, w)) line.append(chip);
  // Подпись собирается узлами, а не одной строкой: номер задачи в ней это
  // ссылка, и склеенный текст ссылкой быть не может.
  const sub = el("div", "l2");
  if (w.id) sub.append(workTaskLink(project, w.id));
  const tail = workSub(w);
  if (tail) sub.append(document.createTextNode((w.id ? ", " : "") + tail));
  box.append(line, sub);
  row.append(box);

  const acts = el("div", "aacts");
  const age = workAge(w.started, now);
  if (age) acts.append(el("span", "atime", age));
  // Разговор есть у любой строки: и у работы из реестра, чью сессию дашборд не
  // видит, и у сессии без задачи. Вход в чат один на цель и задачу, это одна и
  // та же панель, а ручку для реплики выбирает она сама (DK-435). Панель
  // встаёт хвостом поверх текущего раздела, а не уводит на доску.
  if (addr) {
    const talk = el("button", "btn btn-sm btn-ico");
    talk.append(icon("i-chat"));
    withTip(talk, "Чат агента");
    talk.setAttribute("aria-label", "Чат агента " + (w.id || w.session || ""));
    talk.addEventListener("click", (ev) => {
      ev.stopPropagation();
      openChat(chatAddr(project, addr));
    });
    acts.append(talk);
  }
  // Работа из реестра поднята мимо дашборда, и кнопки остановки у неё нет.
  // Словами это в строке больше не стоит: приписка занимала полстроки и ломала
  // ряд, а сказать ей было нечего сверх того, что видно по отсутствию кнопки
  // (замечание пользователя). Знание уехало в подсказку строки, где и лежат
  // остальные метаданные.
  if (w.via === "tmux" && w.id) {
    const stop = withTip(el("button", "btn btn-sm btn-danger", "Остановить"), STOP_TIP);
    stop.addEventListener("click", (ev) => {
      ev.stopPropagation();
      stopRun(project, w.id).catch(console.error);
    });
    acts.append(stop);
  }
  row.append(acts);
  return row;
}

// Раздел «Агенты» разложен на два таба: свои сессии, поднятые дашбордом, и
// прочие, поднятые мимо него (цикл цели из реестра, окно человека). Признак
// один и приезжает работой (own): им же объясняется, почему у чужой строки нет
// кнопки остановки, и разъехаться этим двум местам нельзя.
let agentTab = "own";

function agentTabs() {
  return [["own", "Дашборд"], ["other", "Прочие"]];
}

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

function renderAgents(projects, q) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const all = allWorks(projects);
  const found = all.filter((item) => agentMatch(item, q));
  const list = found.filter((item) => agentOwn(item.work) === (agentTab === "own"));

  // Полоса табов та же, что на доске: два вида одного экрана, и переключаются
  // они по месту.
  const bar = el("div", "ktabs");
  for (const [key, label] of agentTabs()) {
    const btn = el("button", "ktab" + (key === agentTab ? " onktab" : ""), label);
    btn.type = "button";
    const n = found.filter((item) => agentOwn(item.work) === (key === "own")).length;
    if (n) btn.append(el("span", "n", String(n)));
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      if (key === agentTab) return;
      agentTab = key;
      renderAgents(projects, q);
    });
    bar.append(btn);
  }
  groups.append(bar);

  const card = el("div", "card");
  if (!list.length) {
    const empty = el("div", "empty");
    if (q) {
      empty.append(el("b", "", "По запросу ничего не нашлось."));
      // Найденное в соседнем табе называется прямо: молчаливая пустота при
      // непустой выдаче рядом читается как «нет нигде».
      const near = found.length - list.length;
      empty.append(document.createTextNode(near
        ? "Ищем по заголовку работы, задаче, проекту и модели. В соседнем табе нашлось " +
          near + " " + plural(near, "работа", "работы", "работ") + "."
        : "Ищем по заголовку работы, задаче, проекту и модели."));
    } else if (agentTab === "other") {
      empty.append(el("b", "", "Чужих сессий сейчас нет."));
      empty.append(document.createTextNode(
        "Сюда попадают работы, поднятые мимо дашборда: цикл цели из реестра и окно человека."));
    } else {
      empty.append(el("b", "", "Агентов сейчас нет."));
      empty.append(document.createTextNode(
        "Запустите задачу с доски: кнопка «В работу» есть в строке задачи и на её экране."));
    }
    card.append(empty);
  }
  const now = Date.now();
  for (const item of list) card.append(agentRow(item.project, item.work, now));
  groups.append(card);
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

function quotaRow(b) {
  const row = el("div", "qrow");
  row.append(el("em", "", b.name));
  const meter = el("span", "meter");
  const fill = el("i");
  fill.style.width = Math.max(0, Math.min(100, b.used_pct)) + "%";
  meter.append(fill);
  row.append(meter);
  row.append(el("b", "", b.used_pct + "%"));
  const when = quotaWhen(b.reset);
  if (when) {
    const res = el("span", "qres", "до " + when);
    res.title = "сброс " + b.reset;
    row.append(res);
  }
  return row;
}

// quotaNodes собирает узлы блока. Пустота тут говорит словами, какая она:
// каталога снимков нет, каталог пуст и снимок без бакетов это три разных
// причины, и молчащий блок был бы неотличим от отработавшего.
function quotaNodes(view) {
  const out = [el("h4", "", "Квота подписок")];
  if (!view) {
    out.push(el("div", "qnote", "снимки читаются..."));
    return out;
  }
  if (view.note) out.push(el("div", "qnote", view.note));
  for (const h of view.harnesses || []) {
    out.push(el("div", "qsub", h.name));
    for (const b of h.buckets || []) out.push(quotaRow(b));
    // Возраст снимка виден цветом, а не словом «протух»: слово ничего не
    // говорило о том, насколько всё плохо, и стояло почти всегда (замечание 21).
    const note = el("div", "qnote");
    if (h.age) note.append(el("span", "qage " + quotaAgeClass(h.age_sec), "снимок " + h.age + " назад"));
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
  return [rt.proj, rt.id, rt.home, rt.agents, rt.feed, rt.make, rt.drafts,
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
  if (screen !== shownScreen || !rt.draft) closeAgentLive();
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
  const hqClear = document.getElementById("hq-clear");
  if (hqClear) hqClear.hidden = !(hq && hq.value);
  shownBoard = null;
  shownWorks = [];
  const { body } = await api("/api/projects");
  const projects = body.projects || [];
  const current = currentProject(projects);
  // Проект помнится и на главной: с неё раздел «Доска» ведёт на тот проект,
  // который откроется по имени, а не на пустой хэш.
  shownProject = current ? current.name : "";
  // Точка на колокольчике живёт отдельно от экрана: она нужна и на доске, и на
  // главной, а ждать её ответа экрану незачем.
  refreshBellDot().catch(console.error);
  // Остаток подписок тоже живёт отдельно от экрана: он стоит над любым из них,
  // а держать экран ради чтения пары файлов незачем.
  refreshQuota().catch(console.error);
  // А вот список подписок экран ждёт: из него собрана кнопка запуска, и
  // пришедший позже он перерисовал бы кнопку под пальцем. Ждать тут дёшево,
  // ходит запрос один раз на загрузку страницы.
  await loadHarnesses();
  renderSidebar(projects, rt.home || rt.agents ? null : current);
  document.getElementById("brand-note").textContent =
    projects.length + " " + plural(projects.length, "проект", "проекта", "проектов");
  if (rt.agents) {
    // Экран собран из того же ответа, что и колонка: живые работы всех
    // проектов приходят одним запросом, и доска ему не нужна.
    headName("Агенты");
    document.getElementById("psub").textContent = rt.q ? "поиск по сессиям" : "все активные задачи";
    renderLive("", []);
    markNav(rt);
    renderAgents(projects, rt.q || "");
    return;
  }
  if (rt.home) {
    headName("Проекты");
    // Приписки у заголовка главной нет вовсе: список досок под ним и так
    // говорит, что это главная, а откуда они взялись, спрашивают у конфига, а
    // не у шапки (замечание пользователя).
    document.getElementById("psub").textContent = "";
    renderLive("", []);
    markNav(rt);
    renderHome(projects);
    return;
  }
  if (!current) {
    headName("Проектов нет");
    document.getElementById("psub").textContent = "";
    showError((body.errors || []).join("; ") || "в корнях конфига не нашлось ни одной доски docs/TASKS.md");
    return;
  }
  headName(current.name);
  renderLive(current.name, current.works);
  if (rt.make) {
    // Форме заведения доска не нужна: лишний поход за ней стоил бы своего
    // подпроцесса taskctl на каждый фокус окна.
    document.getElementById("psub").textContent = newForm.draft ? "новый черновик" : "новая задача";
    markNav(rt);
    renderNew(current.name);
    return;
  }
  if (rt.drafts) {
    // Накопителю доска тоже не нужна: он читается своей ручкой, и лишний поход
    // за доской стоил бы подпроцесса taskctl на каждый фокус окна.
    document.getElementById("psub").textContent = "черновики";
    markNav(rt);
    await renderDrafts(current.name);
    return;
  }
  if (rt.find) {
    // Выдаче доска не нужна: поиск живёт своей ручкой и сам берёт доску из
    // кэша сервера вместе с накопителем и архивом.
    document.getElementById("psub").textContent = "поиск задач";
    markNav(rt);
    await renderFind(current.name, rt.q);
    return;
  }
  if (rt.draft && rt.id) {
    // Экрану записи доска тоже не нужна: идущий груминг виден среди живых
    // работ проекта, а исход разбора читает своей ручкой сервер.
    document.getElementById("psub").textContent = "черновик " + rt.id;
    markNav(rt);
    await renderDraft(current.name, current.works, rt.id);
    return;
  }
  if (rt.doc) {
    // Документу доска не нужна: он читается своей ручкой.
    document.getElementById("psub").textContent = "docs/" + rt.path;
    markNav(rt);
    await renderDoc(current.name, rt.path);
    return;
  }
  if (rt.lldList) {
    // Списку LLD доска тоже не нужна: раздел живёт своей ручкой.
    document.getElementById("psub").textContent = "LLD проекта";
    markNav(rt);
    await renderLld(current.name, rt.q);
    return;
  }
  const r = await api("/api/projects/" + encodeURIComponent(current.name) + "/board");
  if (!r.ok) {
    document.getElementById("psub").textContent = "";
    showError(r.body.error || ("доска не прочиталась (" + r.status + ")"));
    return;
  }
  const board = r.body.board || {};
  shownBoard = board;
  shownWorks = r.body.works || [];
  renderLive(current.name, r.body.works);
  markNav(rt);
  if (rt.feed) {
    document.getElementById("psub").textContent = "уведомления";
    renderFeed(current.name);
    return;
  }
  if (rt.id) {
    document.getElementById("psub").textContent = rt.id;
    await renderTask(current.name, r.body.works, rt.id);
    return;
  }
  // Путь доски и префикс ID строкой в шапке не стоят: имя файла с префиксом
  // человек читал каждый раз заново, отвечая на вопрос, которого не задавал
  // (замечание пользователя). Приписка говорит, что за экран открыт, а знание
  // осталось подсказкой на самом названии проекта: там его берут, когда надо.
  document.getElementById("psub").textContent = "задачи проекта";
  headName(current.name, "доска docs/TASKS.md" + (board.prefix ? ", префикс " + board.prefix : ""));
  renderBoard(current.name, board);
}

// Заголовок раздела в шапке: имя и подсказка ставятся одним заходом. Порознь
// подсказка переживала переход на соседний экран и висела там, объясняя чужое
// название.
function headName(name, tip) {
  const node = document.getElementById("pname");
  node.textContent = name;
  node.title = tip || "";
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
function markNav(rt) {
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
  const on = rt.home ? "home" : rt.agents ? "agents" : rt.feed ? "feed"
    : rt.find ? "find" : rt.make ? "make"
    : rt.lldList || rt.doc ? "lld" : "board";
  for (const [name, ids] of [["home", ["nav-home", "tab-home"]],
    ["board", ["nav-board", "tab-board"]],
    ["lld", ["nav-lld"]],
    ["agents", ["nav-agents", "tab-agents"]],
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
  // С экрана задачи окно открывается с её адресом: список тогда отфильтрован по
  // задаче, а выключает фильтр значок в шапке окна. С экрана проекта список
  // идёт весь; с главной экрана под панелью нет вовсе, и адрес собирается от
  // показанного проекта, как это делают разделы шапки.
  const rt = route();
  const addr = rt.id && chatIsTask(rt.id) ? rt.id : CHAT_BOARD;
  if (rt.proj) {
    openChat(addr);
    return;
  }
  if (shownProject) location.hash = shownProject + "/chat/" + addr;
});

for (const [id, tail] of [["nav-board", ""], ["tab-board", ""],
  ["make-btn", "/new"],
  ["nav-lld", "/lld"], ["bell", "/feed"], ["find-btn", "/find/"]]) {
  document.getElementById(id).addEventListener("click", () => {
    // Имя проекта берётся то, что показано: на главной хэш пуст, и раздел без
    // имени увёл бы на "#/feed". Открытый разговор переезжает вместе с
    // переходом: лента уведомлений его больше не закрывает.
    goKeepingChat((shownProject || route().proj) + tail);
  });
}

// Раздел «Агенты» имени проекта не просит: он показывает работы всех досок
// сразу, и хэш у него один на весь дашборд.
for (const id of ["nav-agents", "tab-agents"]) {
  document.getElementById(id).addEventListener("click", () => { goKeepingChat("/agents"); });
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
// Поле поиска в шапке живёт разметкой, а не сборкой экрана: шапка стоит над
// любым из них, и перерисовка доски поле не задевает.
wireFindField(document.getElementById("hq"), document.getElementById("hq-clear"));
// Хват панели разговора и её ширина живут той же разметкой: панель стоит над
// любым экраном, и запомненная ширина ставится до первой отрисовки, чтобы
// открытая по ссылке панель не прыгала с умолчания на своё.
putChatWidth(chatWidth());
wireChatGrab(document.getElementById("cgrab"));
wireFindKey();
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
