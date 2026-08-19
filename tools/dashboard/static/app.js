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
  return { ok: resp.ok, status: resp.status, body: await resp.json() };
}

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
  if (h === "/agents") return { proj: "", id: "", agents: true };
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
  const cut = h.indexOf("/");
  if (cut < 0) return { proj: h, id: "" };
  return { proj: h.slice(0, cut), id: h.slice(cut + 1) };
}

function currentProject(projects) {
  const want = route().proj;
  const hit = projects.find((p) => p.name === want);
  return hit || projects[0] || null;
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

function renderLive(project, works) {
  const live = document.getElementById("live");
  const liveCard = (w) => {
    const card = el("div", "lcard");
    card.append(el("span", "dot pulse"));
    // Интерактивная сессия без узнанной задачи названа своим видом: имени
    // работы у неё нет. Ведёт она на тот же экран агента, только по id сессии:
    // разговор лежит на диске, и до DK-294 карточка стояла мёртвой.
    // Имя работы: номер задачи, а без него заголовок разговора, который
    // сервер кладёт в note (замечание 21).
    const name = w.id || w.note || "чат";
    const to = w.id ? boardChatHash(project, w.id)
      : w.session ? boardChatHash(project, w.session) : "";
    // Работа подписана номером задачи и её заголовком: служебного goal-DK-112
    // в подписи нет, о занятии агента оно не говорит ничего.
    const label = el("b", to ? "" : "flat", name);
    if (to) {
      label.addEventListener("click", () => { location.hash = to; });
    }
    card.append(label);
    if (w.title) card.append(el("span", "wname wtitle", w.title));
    if (w.via === "tmux") {
      const stop = withTip(el("button", "btn btn-sm btn-danger", "Стоп"), STOP_TIP);
      stop.addEventListener("click", () => { stopRun(project, w.id).catch(console.error); });
      card.append(stop);
    } else if (w.via === "session") {
      // Подпись вида нужна только там, где есть номер задачи: без него сам
      // заголовок уже стоит именем, и повторять его второй строкой незачем.
      if (w.id && w.note) card.append(el("span", "via", w.note));
    } else {
      card.append(el("span", "via", "ведёт другая сессия"));
    }
    return card;
  };
  // Полоса живых работ перерисовывается по месту: она стоит над списком, и
  // пересобранная целиком дёргала бы его при каждом обновлении.
  sync(live, (works || []).map((w, i) => ({
    key: w.id || w.session || ("live-" + i),
    sign: [w.id, w.title, w.via, w.note, w.session].join("|"),
    make: () => liveCard(w),
  })));
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
  if (!row.run) return null;
  const chip = el("span", "chip c-run");
  chip.append(el("span", "dot pulse"), el("span", "", "работает"));
  return chip;
}

// Кто ведёт работу: та же тройка случаев, что была в шапке экрана агента.
// Стоп у tmux-сессии дашборда живёт кнопкой полосы действий рядом, а у сессии
// человека его нет вовсе: снимать чужое окно дашборду нечем.
function liveChip(work) {
  if (!work) return null;
  if (work.via === "tmux") return el("span", "chip c-run", "tmux-сессия активна");
  if (work.via === "session") return el("span", "chip c-check", "интерактивная сессия");
  return el("span", "chip", "ведёт другая сессия");
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
  // прямо сейчас, а тип с ценой про то, чем она заведена. У идущей работы чип
  // ведёт на её живой статус: со строки туда не было хода вовсе.
  const run = runChip(row);
  if (run) {
    if (row.run !== "gone") {
      run.className += " clicky";
      run.addEventListener("click", (ev) => {
        ev.stopPropagation();
        location.hash = boardChatHash(project, row.id);
      });
    }
    chips.push(run);
  }
  if (/^Цель:/.test(row.title)) chips.push(el("span", "chip c-goal", "цель"));
  if (row.type && row.type !== "task") chips.push(el("span", "chip", row.type));
  if (row.p === "P0" || row.p === "P1") chips.push(el("span", "chip c-p1", row.p));
  if (row.cost && row.cost !== "-") chips.push(el("span", "chip", row.cost));
  // Маркер [после ...] со строки доски назван теми же словами, что и блок
  // зависимостей: «после DK-248» требовало достроить, кто кого ждёт.
  if (row.after && row.after.length) {
    chips.push(el("span", "chip", plural(row.after.length, "заблокирована задачей ",
      "заблокирована задачами ", "заблокирована задачами ") + row.after.join(", ")));
  }
  // Ожидание стоит раньше блока: припаркованную строку оба чипа описывают с
  // разных сторон, и первым читается тот, который говорит, чего ждут.
  const wait = waitChip(row);
  if (wait) chips.push(wait);
  if (row.fail) chips.push(el("span", "chip c-block", "провал: " + row.fail));
  if (row.block) chips.push(el("span", "chip c-block", "блок: " + row.block));
  for (const note of row.notes || []) {
    if (/^код слит/.test(note) || /^без выката/.test(note)) {
      chips.push(el("span", "chip c-check", note));
    }
  }
  return chips;
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
const ACTION_BY_SECT = { "in-progress": "Продолжить", check: "Закрыть" };

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
function runControl(project, id, make, label, isGoal, tip, afterOk) {
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
    fire(wide, harnessDefault());
  });
  const list = harnesses();
  if (isGoal || list.length < 2) {
    const why = isGoal ? GOAL_HARNESS_TIP : harnessWhy();
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
  for (const h of list) {
    const row = harnessRow(h);
    row.addEventListener("click", (ev) => {
      ev.stopPropagation();
      pop.hidden = true;
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
    pop.hidden = !pop.hidden;
    more.setAttribute("aria-expanded", pop.hidden ? "false" : "true");
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
  const live = row.run && row.run !== "gone";
  if (row.run === "session") {
    return el("span", "stale", "интерактивная сессия");
  }
  if (live && row.run !== "tmux") {
    return el("span", "stale", "ведёт другая сессия");
  }
  if (!live && row.after && row.after.length) {
    // Заблокированную маркером задачу конвейер брать не должен, и кнопка
    // говорит это сама: погашенная с причиной понятнее исчезнувшей.
    const wait = el("button", "btn btn-sm", actionLabel(sect));
    wait.disabled = true;
    return withTip(wait, "сначала " + row.after.join(", "));
  }
  if (!live) {
    // Строка списка остаётся на доске и после нажатия (DK-316): экран не
    // уезжает из-под пальца, и afterOk тут не передаётся. Заказ дословно всё
    // равно виден по наведению.
    return runControl(project, row.id, (label) => el("button", "btn btn-sm btn-acc", label),
      actionLabel(sect), /^Цель:/.test(row.title), orderHint(row.order, row.accept, sect, row.id));
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

function renderRow(project, row, sect) {
  const tr = el("div", "trow");
  tr.append(el("span", "id", row.id));
  const tt = el("span", "tt");
  tt.append(el("span", "ttl", row.title));
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
  if (sect === "backlog") wireDrag(project, tr, row);
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

function boardTabs() {
  return [
    { key: "sess", label: "Сессии" },
    { key: "back", label: "Бэклог" },
  ];
}

let boardTab = "sess";

// Полоса разделов (только телефон): два таба доски и переход в накопитель
// черновиков. Черновики стоят тем же табом, потому что с телефона мысль чаще
// записывают, чем разбирают, и путь к накопителю с доски короче не бывает.
function boardTabsBar(project) {
  const bar = el("div", "btabs");
  for (const tab of boardTabs()) {
    const btn = el("button", "btab" + (tab.key === boardTab ? " onbtab" : ""), tab.label);
    btn.type = "button";
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      boardTab = tab.key;
      markBoardTab();
    });
    bar.append(btn);
  }
  const drafts = el("button", "btab", "Черновики");
  drafts.type = "button";
  drafts.addEventListener("click", (ev) => {
    ev.stopPropagation();
    goKeepingChat(project + "/drafts");
  });
  bar.append(drafts);
  return bar;
}

// Открытый таб отмечается по месту, без перерисовки доски: список уже собран,
// и пересобирать его ради подсветки значило бы ронять прокрутку и фокус.
function markBoardTab() {
  const groups = document.getElementById("groups");
  for (const node of groups.querySelectorAll(".bsec")) {
    node.classList.toggle("onsec", node.dataset.tab === boardTab);
  }
  const bar = groups.querySelector(".btabs");
  if (!bar) return;
  boardTabs().forEach((tab, i) => {
    bar.children[i].classList.toggle("onbtab", tab.key === boardTab);
  });
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

function renderBoard(project, board) {
  const groups = document.getElementById("groups");
  const items = [{
    key: "board-tabs",
    sign: project,
    make: () => boardTabsBar(project),
  }, {
    key: "board-bar",
    sign: project,
    make: () => {
      // Полоса кнопок остаётся ноутбуку: на телефоне её место заняли табы и
      // плавающий плюс, и класс .bbar её там гасит.
      const bar = el("div", "nbar bbar");
      bar.append(newTaskButton(project, "Новая задача"), draftsButton(project));
      return bar;
    },
  }];
  const byKey = {};
  for (const sec of board.sections || []) byKey[sec.key] = sec;
  // Снимок нарисованной очереди: по нему жест считает коридор и щели, и берётся
  // он с той же доски, которой нарисован список.
  backlogView = { project, rows: (byKey.backlog && byKey.backlog.rows) || [] };
  for (const key of SECTION_ORDER) {
    const sec = byKey[key];
    if (!sec) continue;
    items.push({
      key: "head-" + key,
      sign: sec.title + "|" + sec.rows.length,
      make: () => {
        const head = el("div", sectionClass("shead", key, boardTab), sec.title);
        head.dataset.tab = sectionTab(key);
        // Backlog стоит по рангу, и счётчик говорит это же: надписью под
        // формой задачи порядок объяснять больше не надо.
        head.append(el("span", "n", sec.rows.length + (key === "backlog" ? ", по рангу" : "")));
        return head;
      },
    });
    const rows = sec.rows.map((row) => ({
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

// Кнопка заведения: стоит и на доске проекта, и на главной, потому что мысль
// приходит вне машины, а не в тот момент, когда открыта нужная доска.
function newTaskButton(project, label) {
  const btn = el("button", "btn btn-acc", label);
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    goKeepingChat(project + "/new");
  });
  return btn;
}

// Вход в накопитель черновиков стоит рядом с заведением, на доске и на
// главной: записанная с телефона мысль иначе видна только в файле, а разбирать
// её приходится с ноутбука.
// Подписи кнопок главной: доска там не одна, и «Новая задача» без имени
// заводила её молча в тот проект, который показан списком последним. Кнопка
// называет проект сама, потому что заголовка доски рядом с ней нет.
function homeBarLabels(project) {
  return { make: "Новая задача в " + project, drafts: "Черновики " + project };
}

function draftsButton(project, label) {
  const btn = el("button", "btn", label || "Черновики");
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    goKeepingChat(project + "/drafts");
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
  let said = r.body.message || r.body.error || "";
  if (r.ok && r.body.note) said += " (" + r.body.note + ")";
  sayResult(said, !r.ok);
  if (r.ok) await refresh();
  return r.ok;
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
  row.append(el("span", "dt", dep.title || dep.note || ""));
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
// Блок постановки: по умолчанию просмотр разметки, правка по карандашу
// (замечание 1 девятого круга POC). Сырой текст в поле ввода читается хуже
// собранного: постановка длинная, со списками и таблицами, и глазами её берут
// куда чаще, чем правят. Разметчик свой, тот же, что в ленте: внешних скриптов
// CSP дашборда не пускает, а тащить библиотеку ради шести правил незачем.
// Текст в дерево кладётся только узлами (createTextNode), никакого innerHTML,
// поэтому угловые скобки из постановки остаются текстом и разметкой не станут.
function filePanel(project, id, detail, form, touch, edit) {
  const card = el("div", "card fpanel");
  const head = el("div", "fhead");
  // Путь файла постановки в шапке блока не нужен: человек читает описание, а
  // не разбирается, где оно лежит (замечание 12). Шапка остаётся ради кнопки
  // «Завести файл» у задачи без постановки.
  head.append(el("span", "gap"));
  const body = el("div", "fbody");
  card.append(head, body);

  if (!detail.file) {
    const make = el("button", "btn btn-sm", "Завести файл");
    make.addEventListener("click", () => { makeTaskFile(project, id).catch(console.error); });
    head.append(make);
    body.append(el("div", "empty", detail.note || "файла задачи нет"));
    return card;
  }
  // Разворот стал режимом чтения задачи, и кнопка его переехала к карандашу в
  // строку статуса: два переключателя вида стоят рядом, а не по разным углам
  // экрана (замечание 2). Сама ручка отдаётся наружу, кнопку рисует экран.
  card.setWide = (on) => card.classList.toggle("wide", on);

  const ta = el("textarea");
  ta.value = form.text;
  ta.setAttribute("aria-label", "текст файла задачи " + id);
  ta.addEventListener("input", () => { form.text = ta.value; touch(); });
  // Просмотр: из этого блока берётся выделение, которое уезжает агенту
  // контекстом, поэтому у него свой класс и свой признак файла.
  const view = el("div", "fview");
  view.dataset.file = detail.file || "docs/tasks/" + id + ".md";
  const paint = () => {
    if (String(form.text || "").trim()) view.replaceChildren(mdRender(form.text));
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
    row.section, row.fail, row.block, row.notes, row.moved, detail.text || "", detail.file || ""]);
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
  btn.append(icon(ico), el("span", "lb", label));
  btn.setAttribute("aria-label", label);
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
  if (work && work.via === "session") return out;
  if (work) {
    out.push(el("span", "hint", "Задачу ведёт другая сессия (живой чат), tmux-сессии дашборда " +
      "у неё нет: остановить отсюда нечем, снимать там, где она поднята."));
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
  out.push(runControl(project, id, (name) => barBtn("btn btn-acc", name, "i-play"), label, isGoal,
    orderHint(row.order, row.accept, row.sect, id), afterOk));
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
  if (task.title && task.fate) withTip(st, task.fate);
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
  // Экран задачи лежит одним блоком: полоса действий переезжает по нему с
  // ширины на ширину, и ей нужен родитель, чей состав не зависит от того, что
  // ещё лежит на экране.
  const page = el("div", "tpage");
  groups.append(page);

  const crumb = el("div", "crumb");
  const back = el("span", "crumb-back", "Доска " + project);
  back.addEventListener("click", () => { goKeepingChat(project); });
  crumb.append(back);
  page.append(crumb);

  if (!r.ok) {
    const card = el("div", "card");
    card.append(el("div", "error", r.body.error || "задача не прочиталась"));
    page.append(card);
    return;
  }
  const detail = r.body;
  const row = detail.row || {};
  // Номер второй раз, мелким: на телефоне доска, номер и статус стоят одной
  // строкой, и большой номер рядом с заголовком там прячется стилями.
  crumb.append(el("span", "idsm", row.id));
  if (row.section) crumb.append(el("span", "chip", row.section));
  if (row.moved) {
    crumb.append(withTip(el("span", "stale dashed", row.moved),
      "дата последней правки задачи на доске: перевод в статус двигает её же"));
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
  // touch подставляется настоящий ниже, когда собрана кнопка: поля строятся
  // раньше её и зовут ссылку через эту обёртку.
  let touchForm = () => {};
  const touch = () => { touchForm(); };

  const head = el("div", "thead");
  head.append(el("span", "idbig", row.id));
  // Правка включается карандашом справа от названия: по умолчанию задача
  // открывается на просмотр, и постановка собрана разметкой, а не лежит сырым
  // текстом в поле (замечание 1 девятого круга POC). Признак живёт до ухода с
  // экрана: сохранение возвращает просмотр само.
  const editing = taskDraft.id === id && taskDraft.edit;
  const title = el("textarea", "tedit");
  title.value = form.title;
  title.setAttribute("aria-label", "заголовок задачи " + id);
  title.rows = 1;
  // Высота по содержимому (замечание 20): заголовок в одну строку держал поле
  // на три, и место уходило в пустоту. Считается она после вставки в дерево,
  // потому что до неё scrollHeight равен нулю.
  const fitTitle = () => {
    title.style.height = "auto";
    if (title.scrollHeight) title.style.height = title.scrollHeight + "px";
  };
  title.addEventListener("input", () => { form.title = title.value; fitTitle(); touch(); });
  setTimeout(fitTitle, 0);
  title.readOnly = !editing;
  title.classList.toggle("ro", !editing);
  head.append(title);
  const pencil = el("button", "tpen" + (editing ? " on" : ""));
  pencil.title = editing ? "Закончить правку" : "Править задачу";
  pencil.setAttribute("aria-label", pencil.title);
  pencil.append(icon(editing ? "close" : "i-pen"));
  let editNow = editing;
  pencil.addEventListener("click", () => {
    editNow = !editNow;
    taskDraft.id = id;
    // Признак живёт в черновике: следующая честная перерисовка экрана (она
    // бывает после сохранения) откроет задачу тем же режимом.
    taskDraft.edit = editNow;
    title.readOnly = !editNow;
    title.classList.toggle("ro", !editNow);
    pencil.classList.toggle("on", editNow);
    pencil.replaceChildren(icon(editNow ? "close" : "i-pen"));
    pencil.title = editNow ? "Закончить правку" : "Править задачу";
    pencil.setAttribute("aria-label", pencil.title);
    if (file.setEdit) file.setEdit(editNow);
  });
  // Кнопки режимов живут в строке статуса справа, а не при заголовке: там для
  // них есть свободное место, а заголовок остаётся заголовком (замечание 2).
  const modes = el("div", "tmodes");
  modes.append(pencil);
  page.append(head);

  const chips = el("div", "tchips");
  // Режим чтения: постановка занимает всю колонку, остальное уходит с глаз.
  const read = el("button", "tpen");
  read.title = "Режим чтения";
  read.setAttribute("aria-label", read.title);
  read.append(icon("i-read"));
  read.addEventListener("click", () => {
    const on = read.classList.toggle("on");
    read.title = on ? "Выйти из режима чтения" : "Режим чтения";
    read.setAttribute("aria-label", read.title);
    if (file.setWide) file.setWide(on);
  });
  modes.append(read);
  // Тот же признак работы, что и в строке списка, и теми же словами: решение
  // «продолжить или не трогать» принимают чаще всего на этом экране.
  const run = runChip(row);
  if (run) chips.append(run);
  // Признаки живости и этап работы переехали сюда с экрана агента (DK-435):
  // разговор ушёл в панель, а чем занята задача и кто её ведёт это предмет
  // самой задачи.
  const work = (works || []).find((w) => w.id === id);
  const live = liveChip(work);
  if (live) chips.append(live);
  const stage = stageChip(row);
  if (stage) chips.append(stage);
  const wait = waitChip(row);
  if (wait) chips.append(wait);
  if (/^Цель:/.test(row.title)) chips.append(el("span", "chip c-goal", "цель"));
  chips.append(pickField("тип", TYPE_VALUES, form.type, (v) => { form.type = v; touch(); }));
  chips.append(pickField("цена", COST_VALUES, form.cost, (v) => { form.cost = v; touch(); }));
  const p = el("span", "chip dashed" + (row.p === "P0" || row.p === "P1" ? " c-p1" : ""), row.p);
  chips.append(withTip(p, P_HINT));
  if (row.fail) chips.append(el("span", "chip c-block", "провал: " + row.fail));
  if (row.block) chips.append(el("span", "chip c-block", "блок: " + row.block));
  for (const note of row.notes || []) {
    if (/^код слит/.test(note) || /^без выката/.test(note)) chips.append(el("span", "chip c-check", note));
  }
  // Кнопки режимов прижимаются к правому краю строки статуса.
  chips.append(el("span", "gap"), modes);
  page.append(chips);

  // Сохранение и действия одной полосой над содержимым (макет «02 Задача»):
  // отдельной карточки действий у задачи больше нет, а надписи про пустую
  // правку нет вовсе. У нетронутой формы нет и самих кнопок правки: они
  // приходят с первым изменением поля вместе с разделителем. Сохранение одно
  // на всю форму, по нему уезжает всё изменённое разом, а отказ проверки
  // гасит кнопку и говорит причину рядом.
  const bar = el("div", "card abar");
  const save = barBtn("btn btn-acc", "Сохранить", "i-done");
  const drop = barBtn("btn", "Отменить правку", "close");
  // Пока правки нет, сохранять и отменять нечего: на телефоне две мёртвые
  // кнопки съедали полосу, а погашенная кнопка неотличима от живой.
  const sep = el("span", "div");
  save.hidden = true;
  drop.hidden = true;
  sep.hidden = true;
  const bad = el("div", "error", "");
  bar.append(save, drop, sep);
  // Расстановка полосы объявлена до формы: её зовёт touchForm с первой правки,
  // а собирается она ниже, когда известны действия задачи.
  let placeBar = null;

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

  touchForm = () => {
    const patch = patchBody();
    const text = textBody();
    const dirty = Object.keys(patch).length > 0 || text !== null;
    const refusal = dirty ? draftRefusal(form, text) : "";
    taskDraft.id = id;
    taskDraft.dirty = dirty;
    bad.textContent = refusal;
    save.disabled = !dirty || Boolean(refusal);
    save.hidden = !dirty;
    drop.hidden = !dirty;
    sep.hidden = !dirty;
    // Первая правка поля приводит полосу вместе с кнопками, отказ от правки
    // уносит её обратно, если своих действий у задачи нет.
    if (placeBar) placeBar(false);
  };
  save.addEventListener("click", () => {
    const patch = patchBody();
    const text = textBody();
    if (draftRefusal(form, text)) return;
    saveTaskDraft(project, id, Object.keys(patch).length ? patch : null, text).catch(console.error);
  });
  drop.addEventListener("click", () => {
    taskDraft.dirty = false;
    taskDraft.edit = false;
    renderTask(project, works, id).catch(console.error);
  });

  const actionNodes = taskActions(project, id, row, works);
  for (const node of actionNodes) bar.append(node);
  bar.append(bad);
  // Полоса в разметку не встаёт, пока показывать в ней нечего, и приходит
  // вместе с кнопками. Прошлая правка мерила пустоту наличием кнопки в полосе и
  // промахнулась: «Сохранить» с «Отменить» лежат тут всегда, просто скрытыми до
  // первой правки формы, и полоса выходила непустой всегда. Мера теперь по делу:
  // есть ли действия у задачи и тронута ли форма. Прятать это стилями нельзя,
  // рамка карточки осталась бы на экране (замечание 5, второй заход).
  let barPlaced = false;
  const barNarrow = window.matchMedia("(max-width:900px)");
  placeBar = (force) => {
    if (force) {
      bar.remove();
      barPlaced = false;
    }
    if (!actionNodes.length && !taskDraft.dirty) {
      if (barPlaced) {
        bar.remove();
        barPlaced = false;
      }
      return;
    }
    if (barPlaced) return;
    // На телефоне полоса идёт под содержимым, на ноутбуке над ним, теми же
    // местами, что держит раскладка экрана.
    if (barNarrow.matches) page.append(bar);
    else chips.after(bar);
    barPlaced = true;
  };

  // Журнал витка уехал в самый низ экрана (замечание 13): читают его редко, а
  // места он занимал столько же, сколько сама постановка, и отжимал её вниз.
  // Панель по-прежнему стоит только у цели и у задачи с живой работой: у
  // остальных источника у журнала нет вовсе.
  const wantJournal = /^Цель:/.test(row.title || "") || Boolean(work);

  // Состав цели стоит над содержимым: с экрана цели смотрят прежде всего на
  // него. Ждать его отрисовка задачи не обязана, состав приезжает отдельным
  // запросом и встаёт на своё место сам.
  if (/^Цель:/.test(row.title || "")) {
    const comp = el("div", "");
    page.append(comp);
    goalComposition(project, id, comp).catch(console.error);
  }

  // Ранг стоит там же, где стоял, над описанием: на телефоне он сворачивается
  // в одну строку с суммой, и переносить его вниз незачем. Свёрнут он с самого
  // начала, разворачивает нажатие на строку; на ноутбуке карточка открыта
  // всегда, и класс сворачивания там ни на что не влияет.
  const rank = el("div", "card rcard rfolded");
  const rtop = el("div", "rtop");
  const rhead = el("div", "rhead");
  rhead.append(el("b", "", "Ранг"), el("span", "stale", "по RANKING.md"));
  // Слева итог крупно, справа от него слагаемые в две строки: показателей
  // шесть, и одной строкой они переносились как попало (замечание 11).
  const big = el("div", "rbig");
  big.append(el("span", "v", String(row.r)));
  const terms = el("div", "rterms");
  RANK_PARTS.forEach((part, i) => {
    const cell = el("span", "rterm");
    cell.append(el("i", "", part.name));
    cell.append(el("b", "", String((row.r_parts || [])[i] === undefined ? "-" : row.r_parts[i])));
    terms.append(cell);
  });
  big.append(terms);
  // Разворот это настоящая кнопка, и клавиатура достаётся ей даром: Enter и
  // пробел жмут её сами. Ширину при этом никто не спрашивает, кнопку прячут
  // стили (.rfold на ноутбуке display:none), а спрятанная кнопка ни в обход
  // табом, ни под палец не попадает. Считать ширину в момент отрисовки
  // означало бы держать её потом руками: поворот планшета и растянутое окно
  // оставляли бы то фокусируемую пустышку, то ранг без клавиатуры.
  const fold = el("button", "rfold", "развернуть");
  fold.setAttribute("aria-expanded", "false");
  rtop.append(rhead, big, fold);
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
  RANK_PARTS.forEach((part, i) => {
    const line = el("div", "rrow");
    line.append(el("span", "nm", part.name));
    line.append(el("span", "why", part.why));
    line.append(pickField("", part.values, form.parts[i], (v) => {
      // Правится одно слагаемое, остальные остаются прежними: пропущенное
      // сервер берёт из строки, а не считает нулём.
      form.parts[i] = Number(v);
      touch();
    }));
    rbody.append(line);
  });

  const rail = el("div", "rrail");
  const deps = depsCard(project, id, detail.after || [], detail.blocks || []);
  const file = filePanel(project, id, detail, form, touch, editing);
  const grid = el("div", "tgrid");
  page.append(grid);
  // Блоки экрана встают в разметку туда же, где они нарисованы: полоса
  // действий на ноутбуке над содержимым, на телефоне под ним, ранг на телефоне
  // над описанием, зависимости под ним. Переставлять их стилями нельзя, order
  // двигает картинку, а обход табом идёт по разметке, и на телефоне таб уводил
  // с заголовка вниз на «Сохранить» и только потом возвращался вверх к
  // описанию (замечание ревью 9).
  watchTaskLayout({ page, chips, bar, grid, rail, file, rank, deps, placeBar });

  // Журнал витка стоит последним блоком экрана, под целью и постановкой
  // (замечание 13): туда за ним и идут, а сверху он отжимал вниз то, ради чего
  // экран открывают.
  if (wantJournal) {
    const jp = pane("Журнал витка", "источник назовёт сервер");
    // Зелёная точка вместо слов «хвост дописывается»: живость журнала это
    // состояние, а не сообщение, и строкой текста она занимала место шапки
    // (замечание 11).
    jp.head.append(el("span", "dot pulse"));
    // Отступ сверху: журнал стоит последним блоком, и без него он слипался с
    // зависимостями над собой в одну простыню.
    jp.card.classList.add("jbottom");
    page.append(jp.card);
    wireJournal(project, id, jp.body, jp.sub);
  }

  taskDraft.id = id;
  taskDraft.dirty = false;
  taskDraft.seen = taskSeen(detail);
  touchForm();
}

// Порядок блоков экрана задачи зависит от ширины окна, и держит его подписка,
// а не снимок в момент отрисовки: окно растягивают, планшет поворачивают, и
// экран при этом не перерисовывается. Раскладка на ноутбуке это две колонки,
// описание и правая колонка с рангом и зависимостями; на телефоне колонок нет,
// и блоки идут потоком: ранг, описание, зависимости, полоса действий.
// append переносит уже созданный узел, поэтому обе раскладки собираются из
// одних и тех же блоков. Подписка одна на весь дашборд: следующая отрисовка
// экрана задачи снимает прежнюю, иначе слушатели копились бы с каждым
// переходом и двигали блоки в выброшенной разметке.
let taskLayoutWatch = null;
function watchTaskLayout(parts) {
  if (taskLayoutWatch) {
    taskLayoutWatch.mq.removeEventListener("change", taskLayoutWatch.place);
    taskLayoutWatch = null;
  }
  const mq = window.matchMedia("(max-width:900px)");
  const place = () => {
    // Колонок на экране задачи больше нет ни на какой ширине. Ранг ушёл из
    // правой колонки строкой во всю ширину (замечание 3 четвёртого круга), и
    // держать колонку ради одних зависимостей стало не за чем: они встают
    // своей строкой под описанием, а описание занимает всю ширину. Раскладка
    // от этого одна на телефон и на ноутбук, и разъезжаться ей негде.
    parts.rail.remove();
    parts.rank.remove();
    parts.page.insertBefore(parts.rank, parts.grid);
    parts.grid.append(parts.file, parts.deps);
    parts.placeBar(true);
  };
  place();
  mq.addEventListener("change", place);
  taskLayoutWatch = { mq, place };
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
  const car = el("span", "foldc", "+");
  top.append(car);
  const body = el("pre", "foldb", text);
  body.hidden = true;
  top.addEventListener("click", () => {
    body.hidden = !body.hidden;
    car.textContent = body.hidden ? "+" : "-";
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
    return foldEl("toolout", "вывод", item.text || "", foldPeek(item.text, 100));
  }
  if (item.role === "tool") {
    const name = item.tool || "инструмент";
    if (item.text) return foldEl("tool", name, item.text, item.note || "");
    const div = el("div", "tool");
    div.append(el("b", "", name));
    if (item.note) div.append(el("span", "", item.note));
    return div;
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
// честное «задача не распознана» (DK-252). Разряд привязки виден словами:
// «ведёт» стоит на записи реестра чатов, «говорит о» на угадывании по
// транскрипту, и работой задачи считается только первое (DK-431).
// Подпись сессии: заголовок разговора, а не отчёт о том, чего дашборд про неё
// не узнал. «Задача не распознана» было честно, но бесполезно: узнают сессию по
// первой реплике, а не по отсутствию привязки (замечание 21).
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

// Позиция ленты каждого разговора живёт, пока открыта вкладка, а ключом ей
// служит ID сессии. Уход с экрана и возврат восстанавливают её без похода на
// диск, поэтому не localStorage, а память вкладки (тот же выбор для панели
// чата в решении 5 LLD DK-430). Слушатель прокрутки пишет сюда на каждый
// сдвиг, а открытие ленты читает её, прежде чем решить, куда встать: вниз
// или на прежнее место. Расстояние от низа (`rest`) само по себе не годится:
// свежий заход приносит только хвост, а до ухода лента могла стоять глубже,
// после подгрузки истории. Без глубины (`firstSeq`) место мерилось бы против
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
  let lastSeq = -1;
  let firstSeq = null;
  let empty = opts.empty || EMPTY_TALK;
  let loadingOlder = false;
  // Надпись начала горит, только когда раньше правда нечего показать: пока
  // лента пуста или ещё не упёрлась в начало разговора, надпись не видна.
  const updateStart = () => { atStart.hidden = firstSeq !== 0; };
  updateStart();

  const draw = () => {
    const bottom = atBottom(scroll);
    const rest = scroll.scrollHeight - scroll.scrollTop;
    if (!talk.length) {
      sync(box, [{ key: "empty", sign: empty, make: () => el("div", "empty", empty) }]);
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
      // Записи субагента собираются в один свёрнутый блок: их бывают сотни на
      // один вызов Task, и вперемешку с разговором они его хоронят (находка
      // тринадцатого круга POC). Внутри блока разбор тот же самый.
      if (item.sub) {
        const from = i;
        const label = item.sub;
        const inner = [];
        while (i < talk.length && talk[i].sub === label) inner.push(talk[i++]);
        i--;
        const last = inner[inner.length - 1];
        items.push({
          key: "sub-" + talk[from].seq,
          sign: [label, inner.length, last && last.text].join("|"),
          make: () => subBlock(label, inner, opts),
        });
        continue;
      }
      const next = talk[i + 1];
      if (item.role === "tool" && next && next.role === "toolout" && opts.pair) {
        items.push({
          key: "seq-" + item.seq,
          sign: [item.role, item.time, item.text, next.text].join("|"),
          make: () => opts.pair(item, next),
        });
        i++;
        continue;
      }
      items.push({
        key: "seq-" + item.seq,
        sign: [item.role, item.time, item.text].join("|"),
        make: () => opts.item(item),
      });
    }
    sync(box, items);
    if (bottom) keepBottom(scroll, true);
    else keepPlace(scroll, rest);
  };

  // Подгрузка вверх: раньше её звала кнопка, теперь зовёт прокрутка, а тело
  // осталось тем же, включая якорь через draw(). Флаг не пускает второй
  // запрос, пока висит первый: прокрутка у самого верха шлёт событие на
  // каждый пиксель.
  const loadOlder = async () => {
    if (loadingOlder || firstSeq === null || firstSeq === 0) return;
    loadingOlder = true;
    const older = await api(sessionURL(project, sid) +
      "?before=" + firstSeq + "&n=" + tail);
    loadingOlder = false;
    if (gone() || !older.ok) return;
    const items = older.body.items || [];
    if (items.length) firstSeq = items[0].seq;
    talk.unshift(...items.filter(keep));
    draw();
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
  const restorePlace = async (place) => {
    while (!gone() && place.firstSeq !== null && firstSeq !== null &&
      firstSeq !== 0 && firstSeq > place.firstSeq) {
      const before = firstSeq;
      await loadOlder();
      if (firstSeq === before) break;
    }
    if (gone()) return;
    keepPlace(scroll, place.rest);
  };

  // Место ленты пишется на каждый сдвиг прокрутки: и для будущего возврата к
  // этому разговору, и как повод подгрузить историю, когда взгляд подошёл к
  // верху. Глубина (firstSeq) едет вместе с местом: без неё возврат не знает,
  // сколько истории досбирать до прежней высоты. Коробка прокрутки переживает
  // переключение вкладок разговора (тот же tp.body у соседних сессий), поэтому
  // слушатель снимается сам при уходе с ленты, иначе он копился бы с каждым
  // переключением.
  const onScroll = () => {
    if (gone()) return;
    feedPlace.set(sid, { bottom: atBottom(scroll), rest: scroll.scrollHeight - scroll.scrollTop, firstSeq });
    if (scroll.scrollTop < LOAD_MARGIN) loadOlder();
  };
  scroll.addEventListener("scroll", onScroll);
  opts.live.push(() => scroll.removeEventListener("scroll", onScroll));

  const first = await api(sessionURL(project, sid) + "?n=" + tail);
  if (gone()) return;
  if (first.ok) {
    const items = first.body.items || [];
    if (items.length) firstSeq = items[0].seq;
    for (const item of items) {
      lastSeq = item.seq;
      if (keep(item)) talk.push(item);
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

  // Пропущенное дочитывается запросом, а не потоком: поток шлёт только новое,
  // и всё, что случилось между обрывом и переподключением, до ленты не доедет
  // никогда. Ровно это и видел человек после сна ноутбука: вкладку браузер
  // задушил, поток умер, а вернувшаяся страница показывала ленту такой, какой
  // она была двадцать минут назад, и лечилось это только F5.
  let catching = false;
  const catchUp = async () => {
    if (catching || gone()) return;
    catching = true;
    try {
      const r = await api(sessionURL(project, sid) + "?n=" + repliesCatch);
      if (!r.ok || gone()) return;
      const items = r.body.items || [];
      let added = false;
      for (const item of items) {
        if (item.seq <= lastSeq) continue;
        lastSeq = item.seq;
        if (firstSeq === null) firstSeq = item.seq;
        if (!keep(item)) continue;
        talk.push(item);
        added = true;
        if (opts.onItem) opts.onItem(item);
      }
      if (added) {
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
      // Свой хвост поток шлёт заново, и без отсева по seq те же реплики легли
      // бы в ленту вторым разом.
      if (item.seq <= lastSeq) return;
      lastSeq = item.seq;
      if (firstSeq === null) {
        firstSeq = item.seq;
        updateStart();
      }
      if (!keep(item)) return;
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
  bb.append(el("div", "mm", said));
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
  const cmd = call.text || "";
  const said = out && out.text ? out.text : "";
  const box = el("div", "tool fold pair");
  const top = el("div", "foldh");
  top.append(el("b", "", name));
  top.append(el("span", "", call.note || foldPeek(said, 80)));
  // Копирование и сворачивание стоят иконками справа вверху: подписи словами
  // занимали в карточке больше места, чем сама команда (замечание 7).
  const both = (cmd ? cmd : "") + (cmd && said ? "\n" : "") + said;
  top.append(copyBtn(both));
  const car = el("button", "foldcp foldar");
  car.append(icon("i-unfold"));
  top.append(car);
  // Вход и выход строками с иконками-стрелками: вправо ушло в инструмент,
  // влево вернулось из него.
  const body = el("div", "foldb pairb");
  const row = (ico, text, cls) => {
    const line = el("div", "pline " + cls);
    const mark = el("span", "pico");
    mark.append(icon(ico));
    line.append(mark, el("pre", "ptext", text));
    return line;
  };
  if (cmd) body.append(row("i-in", cmd, "pin"));
  if (said) body.append(row("i-out", said, "pout"));
  body.hidden = true;
  const flip = () => {
    body.hidden = !body.hidden;
    car.replaceChildren(icon(body.hidden ? "i-unfold" : "i-fold"));
    box.classList.toggle("open", !body.hidden);
  };
  car.addEventListener("click", (ev) => { ev.stopPropagation(); flip(); });
  top.addEventListener("click", flip);
  box.append(top, body);
  return box;
}

// Приложенное выделение свёрнутым блоком при пузыре: развернуть по клику.
// Простыня постановки в ленте закрыла бы собой сам разговор.
function selFold(file, text) {
  return foldEl("selq", "выделение", text, file, text);
}

// Блок работы субагента: заголовок с подписью и счётом ходов, внутри та же
// лента. Свёрнут по умолчанию, разворачивается кликом; последняя строка видна
// в заголовке, чтобы по ней было видно, чем субагент занят прямо сейчас.
function subBlock(label, inner, opts) {
  const box = el("div", "subblk fold");
  const top = el("div", "foldh");
  top.append(el("b", "", "субагент"));
  const tail = inner[inner.length - 1] || {};
  const peek = tail.tool ? tail.tool + (tail.note ? ": " + tail.note : "")
    : foldPeek(tail.text || "", 60);
  top.append(el("span", "", label + ", " + inner.length + " " +
    plural(inner.length, "запись", "записи", "записей") + (peek ? ", " + peek : "")));
  const car = el("button", "foldcp foldar");
  car.append(icon("i-unfold"));
  top.append(car);
  const body = el("div", "subbody");
  for (let j = 0; j < inner.length; j++) {
    const it = inner[j];
    const nx = inner[j + 1];
    if (it.role === "tool" && nx && nx.role === "toolout" && opts.pair) {
      body.append(opts.pair(it, nx));
      j++;
      continue;
    }
    if (it.role === "toolout" && !it.text) continue;
    body.append(opts.item(it));
  }
  body.hidden = true;
  const flip = () => {
    body.hidden = !body.hidden;
    car.replaceChildren(icon(body.hidden ? "i-unfold" : "i-fold"));
    box.classList.toggle("open", !body.hidden);
  };
  car.addEventListener("click", (ev) => { ev.stopPropagation(); flip(); });
  top.addEventListener("click", flip);
  box.append(top, body);
  return box;
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
    const who = item.role === "user" ? "вы" : "агент";
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
      const thumb = el("img", "mshot");
      thumb.src = chatsURL(chatShotProject) + "/" + encodeURIComponent(chatShotSid) +
        "/shot?name=" + encodeURIComponent(String(item.shot).split("/").pop());
      thumb.alt = "вложенный снимок";
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
    empty: "чат пуст: в транскрипте нет ни одной реплики",
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

function wireTaGrip(grip, ta) {
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
  const release = () => { held = false; };
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

function chatDraftRead(addr) {
  try {
    return localStorage.getItem(CHAT_DRAFT_KEY + addr) || "";
  } catch (err) {
    return "";
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
function switchChat(addr) {
  const to = "#" + chatBase() + "/chat/" + addr;
  chatLastSet(addr);
  if (location.hash === to) return;
  history.replaceState({ chat: addr }, "", to);
  refresh().catch(console.error);
}

function closeChat() {
  // Закрытие рукой снимает и память: вернувшись, человек видит дашборд, а не
  // разговор, от которого он ушёл нарочно.
  chatLastSet("");
  if (chatDepth > 0) {
    chatDepth -= 1;
    history.back();
    return;
  }
  // Пришли по ссылке снаружи: возвращаться некуда, и панель закрывается
  // заменой адреса на тот экран, что под ней.
  history.pushState({}, "", "#" + chatBase());
  refresh().catch(console.error);
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

// Состояние окна чатов: весь список диалогов проекта, выбранный диалог и
// задача, по которой список фильтруется. Список приезжает целиком и
// фильтруется на клиенте: переключатель фильтра тогда работает мгновенно, без
// похода на сервер.
async function chatState(project, addr, board) {
  const st = { addr, sid: "", task: "", chats: [], entry: null, note: "",
    error: "", models: [], fresh: false };
  if (chatIsNew(addr)) {
    st.fresh = true;
    st.task = chatNewTask(addr);
  } else if (chatIsTask(addr)) {
    st.task = addr;
  } else if (addr && addr !== CHAT_BOARD) {
    st.sid = addr;
  }
  const r = await api(chatsURL(project));
  if (!r.ok) {
    st.error = r.body.error || "список чатов не прочитался";
    return st;
  }
  st.chats = r.body.chats || [];
  st.note = r.body.note || "";
  if (r.body.models) st.models = r.body.models;
  // Задача адреса это фильтр, а не сам чат: открытый ею список показывает чаты
  // задачи. Открывается тот, в котором человек разговаривал последним, а если
  // такого нет или он пропал из списка, то свежий.
  if (!st.sid && !st.fresh) {
    const list = chatVisible(st);
    const want = st.task ? chatTaskLast(st.task) : "";
    const kept = want && list.find((c) => c.id === want);
    if (kept) st.sid = kept.id;
    else if (list.length) st.sid = list[0].id;
  }
  st.entry = st.chats.find((c) => c.id === st.sid) || null;
  // Задача берётся у самого диалога, когда адрес её не назвал: по ней
  // подписывается шапка и заводится следующий диалог в том же дереве.
  if (!st.task && st.entry && (st.entry.tasks || []).length) st.task = st.entry.tasks[0];
  if (st.task) {
    st.isGoal = isGoalRow(board, st.task);
    const row = boardRow(board, st.task);
    st.title = row ? row.title : "";
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
function chatTitle(c) {
  if (!c) return "чата нет";
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
  row.append(el("b", "", chatTitle(c)));
  const chips = el("div", "cchips");
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
    switchChat(c.id);
  });
  return row;
}

// Выпадающий список открыт: узел живёт при шапке, а не при документе, чтобы
// закрытие панели уносило его с собой.
let chatDrop = null;

function chatDropShut() {
  if (chatDrop) {
    chatDrop.remove();
    chatDrop = null;
  }
}

document.addEventListener("click", (ev) => {
  if (!chatDrop) return;
  if (chatDrop.contains(ev.target)) return;
  if (ev.target.closest && ev.target.closest(".cdpick")) return;
  chatDropShut();
});

// Список с поиском: поле сверху, дальше строки. Поиск идёт по заголовку, по ID
// сессии и по задачам, потому что ищут диалог всеми тремя способами.
function chatDropOpen(project, st, anchor) {
  chatDropShut();
  const box = el("div", "cdrop");
  const find = el("input");
  find.type = "text";
  find.placeholder = "Поиск чата";
  find.setAttribute("aria-label", "Поиск чата");
  const rows = el("div", "cdrows");
  const draw = () => {
    const q = find.value.trim().toLowerCase();
    const list = chatVisible(st).filter((c) => {
      if (!q) return true;
      return (c.title || "").toLowerCase().includes(q) ||
        c.id.toLowerCase().includes(q) ||
        (c.tasks || []).join(" ").toLowerCase().includes(q);
    });
    rows.replaceChildren();
    for (const c of list) rows.append(chatOption(project, c, st.sid));
    if (!list.length) {
      rows.append(el("div", "hint", q ? "по запросу ничего не нашлось" :
        (st.note || "чатов тут нет")));
    }
  };
  find.addEventListener("input", draw);
  draw();
  box.append(find, rows);
  anchor.append(box);
  chatDrop = box;
  find.focus();
}

// Шапка окна: выбор диалога, «+», модель, переключатель фильтра и крестик.
// Больше входов в разговор нигде нет: с экрана задачи окно открывает тот же
// значок в шапке дашборда.
function chatHead(project, st) {
  const head = el("div", "chead");
  const line = el("div", "chline");

  const pick = el("button", "cdpick");
  // Номер задачи стоит лейблом при названии диалога, а не отдельной кнопкой
  // «Экран DK-397» под шапкой: место экономится, а нажатие ведёт туда же
  // (замечание 17).
  if (st.task) {
    const lab = el("span", "cdtask", st.task);
    lab.addEventListener("click", (ev) => {
      ev.stopPropagation();
      goKeepingChat(project + "/" + st.task);
    });
    pick.append(lab);
  }
  pick.append(el("b", "", chatTitle(st.entry) + (st.fresh ? " (новый чат)" : "")));
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
    chatDrop = menu;
  });
  line.append(add);

  const model = el("select", "cdsel");
  model.setAttribute("aria-label", "Модель агента");
  // Показывается то, чем сессия работает на самом деле (модель из транскрипта),
  // а сохранённый выбор дашборда это лишь заказ на следующий подъём. Разойдись
  // они, врал бы именно выбор: у чужого окна модель ставят в самом vscode
  // (замечание 7 седьмого круга POC).
  const live = st.entry ? st.entry.liveModel : "";
  const cur = live || (st.entry ? st.entry.model || chatModelPref() : chatModelPref());
  // Чужую живую сессию выбором с дашборда не переубедить: её клиент уже
  // поднят, и модель у него своя до самого резюма.
  const alien = Boolean(st.entry && st.entry.state === "live" && !st.entry.own);
  // Лестница приезжает от agentctl: имя модели, ярус и подписка, чьей квотой
  // она платится. Своего перечня имён у панели нет, иначе новая подписка на
  // машине не появилась бы тут вовсе.
  const opts = (st.models || []).slice();
  if (cur && !opts.some((m) => m.model === cur)) opts.unshift({ model: cur, tier: "", harness: "" });
  for (const m of opts) {
    const label = m.tier ? m.model + " (" + m.tier + ", " + m.harness + ")" : m.model;
    const o = el("option", "", label);
    o.value = m.model;
    if (m.model === cur) o.selected = true;
    model.append(o);
  }
  if (alien) {
    model.disabled = true;
    model.title = "Модель выбрана в самом vscode: с дашборда она сменится только на резюме этого чата.";
  } else if (st.entry && st.entry.model && live && st.entry.model !== live) {
    model.title = "Сейчас работает " + live + ", выбранная " + st.entry.model +
      " возьмётся на следующем подъёме или резюме.";
  }
  model.addEventListener("change", () => {
    chatModelSet(model.value);
    if (!st.sid) {
      sayResult("модель нового чата: " + model.value);
      return;
    }
    api(chatsURL(project) + "/" + encodeURIComponent(st.sid) + "/model",
      { method: "POST", body: { model: model.value } })
      .then((r) => { sayResult(r.body.message || r.body.error || "", !r.ok); })
      .catch(console.error);
  });
  line.append(model);

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
    filt.addEventListener("click", () => {
      chatFilterSet(!chatFilterOn());
      repaintChat().catch(console.error);
    });
    line.append(filt);
  }

  const shut = el("button", "nx");
  shut.setAttribute("aria-label", "Закрыть панель");
  shut.title = "Закрыть панель";
  shut.append(icon("close"));
  shut.addEventListener("click", () => { chatDropShut(); closeChat(); });
  line.append(shut);
  head.append(line);

  const sub = el("div", "csub");
  if (st.entry) {
    const bits = [CHAT_STATE_WORD[st.entry.state] || st.entry.state, chatWhen(st.entry)];
    if ((st.entry.tasks || []).length) bits.push(st.entry.tasks.join(", "));
    if (st.entry.tree) bits.push(st.entry.tree);
    if (st.entry.tmux) bits.push("tmux " + st.entry.tmux);
    sub.textContent = bits.filter(Boolean).join(", ");
  } else if (st.fresh) {
    sub.textContent = "новый чат" + (st.task ? " про " + st.task : "") +
      ": первая реплика поднимет сессию";
  } else {
    sub.textContent = st.task ? "чатов задачи " + st.task + " нет" : "чат не выбран";
  }
  head.append(sub);
  return head;
}

// Куда уйдёт реплика и почему. Мера приходит с сервера состоянием диалога, а
// не считается на глаз: ошибка тут стоит реплики, ушедшей мимо адресата.
function chatWay(st) {
  if (st.fresh || !st.sid) return { kind: "new", off: false, why: "" };
  const state = st.entry ? st.entry.state : "dead";
  if (state === "live") return { kind: "say", off: false, why: "" };
  // Окна vscode отдельным случаем больше нет: канал самого клиента достаёт
  // любую живую сессию, и «пишите там» осталось бы отказом без причины.
  // Молчание тут честное: реплике есть куда ехать в обоих оставшихся случаях,
  // и объяснять человеку механику доставки на каждом экране незачем.
  return { kind: "resume", off: false, why: "" };
}

// Подъём нового диалога и ожидание его ID. Сессия рождается позже команды, и
// ID приходит из реестра по имени tmux-сессии: дашборд опрашивает список, пока
// он не встанет, и переключается на живой диалог сам.
async function chatRaise(project, st, text, model) {
  const body = { text, model };
  if (st.task) body.id = st.task;
  const r = await api(chatsURL(project), { method: "POST", body });
  if (!r.ok) {
    sayResult(r.body.error || "чат не поднялся", true);
    return false;
  }
  const name = r.body.tmux;
  for (let i = 0; i < 40; i += 1) {
    await new Promise((ok) => setTimeout(ok, 1500));
    const list = await api(chatsURL(project) + "?tmux=" + encodeURIComponent(name));
    const hit = (list.ok && (list.body.chats || [])[0]) || null;
    if (hit) {
      switchChat(hit.id);
      return;
    }
  }
  sayResult("сессия " + name + " ещё не назвала себя в реестре: чат встанет в списке сам", true);
}

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
// Префикс картинки: агент читает файл сам, поэтому ему нужен путь, а не байты.
// Форма та же, что у выделения: строка перед репликой, разбирает её сервер.
function shotPrefix(path) {
  return '<screenshot file="' + path + '">\nвставлен снимок экрана\n</screenshot>\n';
}

function selPrefix(sel) {
  return '<selection file="' + sel.file + '">\n' + sel.text + "\n</selection>\n";
}

function makeEcho(project, box, feedBox) {
  // Ключ у местной реплики свой и сквозной: sync рисует ленту по ключам, и
  // без устойчивого ключа пузырь пересобирался бы на каждой перерисовке.
  let seq = 0;
  const mine = [];

  const draw = () => {
    box.replaceChildren();
    for (const m of mine) {
      const meta = m.state === "bad" ? "не ушло"
        : m.state === "sent" ? "доставлено" : "отправляется...";
      const wrap = chatBubble("вы", m.text, m.sel ? meta + ", с выделением" : meta);
      wrap.classList.add("m-local", "m-" + m.state);
      if (m.sel) wrap.append(selFold(m.sel.file, m.sel.text));
      if (m.pic) {
        const thumb = el("img", "mshot");
        thumb.src = m.pic.data;
        wrap.append(thumb);
      }
      if (m.state === "bad") {
        const again = el("button", "linkish", "повторить");
        again.addEventListener("click", () => {
          const text = m.text;
          drop(m);
          if (m.retry) m.retry(text);
        });
        wrap.append(again);
      }
      box.append(wrap);
    }
    // Лента доезжает до своей реплики: человек нажал отправку и обязан увидеть,
    // что она встала.
    if (feedBox) feedBox.scrollTop = feedBox.scrollHeight;
  };

  const drop = (m) => {
    const at = mine.indexOf(m);
    if (at >= 0) mine.splice(at, 1);
    draw();
  };

  return {
    // Пузырь встаёт до похода на сервер: отправка видна мгновенно.
    // wire это то, что ушло агенту (с префиксом выделения), и сверка эха идёт
    // по нему: в транскрипте лежит именно он. text это слова человека, их и
    // видно в пузыре.
    add(text, retry, wire, sel, pic) {
      seq += 1;
      const m = { key: "local-" + seq, text, wire: wire || text, sel, pic,
        state: "wait", born: Date.now(), retry };
      mine.push(m);
      draw();
      return m;
    },
    // Ручка ответила удачей: реплика ушла, но эха из транскрипта ещё нет.
    // Пометка снимается по сроку, чтобы часики не висели вечно там, где эхо не
    // придёт вовсе (клиент старой версии, чужой формат записи).
    sent(m) {
      if (!mine.includes(m)) return;
      m.state = "sent";
      draw();
      setTimeout(() => { drop(m); }, ECHO_HOLD);
    },
    bad(m) {
      if (!mine.includes(m)) return;
      m.state = "bad";
      draw();
    },
    // Эхо из ленты: та же реплика человека, пришедшая из транскрипта. Сверка по
    // тексту, потому что своего идентификатора у реплики в журнале нет вовсе.
    saw(item) {
      if (item.role !== "user" || !item.text) return;
      // Сервер отрезал префикс выделения и вернул слова человека отдельно,
      // поэтому сверять можно и по ним, и по всему отправленному.
      const said = item.text.trim();
      const hit = mine.find((m) => m.text.trim() === said || m.wire.trim() === said);
      if (hit) drop(hit);
    },
    clear() {
      mine.length = 0;
      draw();
    },
  };
}

// Сколько местный пузырь держится после удачной отправки, не дождавшись эха.
// Клиент пишет реплику в транскрипт своим ходом, и десяти секунд на это с
// запасом хватает; дальше пузырь снимается, чтобы часики не висели вечно.
const ECHO_HOLD = 10000;

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
  const echo = makeEcho(project, pend, feed);
  chatLive.push(echo.clear);

  if (way.why) {
    const note = el("div", "cnote" + (way.off ? " idle" : ""));
    note.append(el("span", "", way.why));
    wrap.append(note);
  }

  const busy = makeBusy(project, wrap);
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
  ta.placeholder = way.off ? "чат идёт в vscode, пишите там" : "Написать агенту...";
  ta.disabled = Boolean(way.off);
  ta.setAttribute("aria-label", "Реплика в чат");
  wireTaGrip(grip, ta);
  // Черновик возвращается при открытии разговора и пишется по ходу набора.
  ta.value = chatDraftRead(st.addr);
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
  let shot = null;
  const drawClips = () => {
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
  // Продолжить работу задачи можно прямо отсюда: сервер сам решит, будить ли
  // живую сессию каналом или поднимать резюм (ручка /continue).
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
  // Вложение уезжает на сервер до самой реплики: агенту нужен путь, а он
  // рождается только после записи файла.
  const putShot = async (pic) => {
    if (!pic || !st.sid) return "";
    // dataURL это «data:<тип>;base64,<данные>»: режется он по первой запятой,
    // в самом base64 запятых нет. Тип берётся из dataURL, а не из типа
    // буфера: буфер иногда называет вид иначе, чем то, что реально пришло.
    const raw = String(pic.data);
    const cut = raw.indexOf(",");
    const kind = (raw.match(/^data:([^;,]+)/) || [])[1] || pic.kind;
    const r = await api(chatsURL(project) + "/" + encodeURIComponent(st.sid) + "/shot", {
      method: "POST",
      body: { kind, data: cut >= 0 ? raw.slice(cut + 1) : raw },
    });
    if (!r.ok) {
      sayResult(r.body.error || "картинка не легла", true);
      return "";
    }
    return r.body.path || "";
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
    if (way.kind === "new") {
      chatRaise(project, st, wire0, st.entry ? st.entry.model : chatModelPref())
        .then((ok) => { if (ok === false) echo.bad(m); else echo.sent(m); })
        .catch((err) => { echo.bad(m); console.error(err); })
        .finally(done);
      return;
    }
    busy.on(st.sid);
    putShot(pic).then((path) => api(chatsURL(project) + "/" + encodeURIComponent(st.sid) + "/say",
      { method: "POST", body: { text: path ? shotPrefix(path) + wire0 : wire0 } }))
      .then((r) => {
        if (!r.ok) {
          // Отказ ручки (сокет не отозвался, разговора нет): пузырь остаётся с
          // пометкой и кнопкой повтора, а не пропадает молча, унося с собой
          // набранное. Удача молчит: реплика и так видна в ленте.
          busy.off();
          echo.bad(m);
          sayResult(r.body.error || "реплика не ушла", true);
          return;
        }
        echo.sent(m);
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
  const fire = () => {
    const text = ta.value.trim();
    if (!text || send.disabled) return;
    ta.value = "";
    if (draftTimer) clearTimeout(draftTimer);
    chatDraftWrite(st.addr, "");
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
  ta.addEventListener("keydown", (ev) => {
    if (ev.key === "Enter" && !ev.shiftKey && !ev.isComposing) {
      ev.preventDefault();
      fire();
    }
  });
  send.addEventListener("click", fire);
  row.append(send);
  // Порядок узлов и есть положение хвата: полоса стоит первой в коробке, то
  // есть над полем.
  box.append(grip, ta, row);
  wrap.append(box);

  chatLive.push(busy.off);
  if (st.error) {
    say(feed, "error", st.error);
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
  pin.replaceChildren(el("div", "empty", "чат открывается..."));
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
  row.append(el("span", "st", d.title || ""));
  const meta = el("span", "sm");
  meta.append(el("span", "stale", d.age_words || ""));
  if (d.prio) meta.append(el("span", "chip", DRAFT_PRIO[d.prio] || d.prio));
  if (d.deferred) meta.append(el("span", "chip", "отложен " + d.deferred));
  const groom = el("button", "btn btn-sm btn-acc", "Провести груминг");
  if (d.order) withTip(groom, "Заказ агенту: «" + d.order + "».");
  meta.append(groom);
  row.append(meta);
  row.addEventListener("click", (ev) => {
    if (ev.target === groom) return;
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
    key: "drafts-crumb",
    sign: project,
    make: () => {
      const crumb = el("div", "crumb");
      const back = el("span", "crumb-back", "Доска " + project);
      back.addEventListener("click", () => { goKeepingChat(project); });
      crumb.append(back);
      return crumb;
    },
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
  wireFindField(input);
  box.append(input);
  return box;
}

// Поле поиска: набор с задержкой, ввод отправляет запрос сразу. Поля два, в
// шапке и на экране выдачи, и ведут они себя одинаково.
function wireFindField(input) {
  input.addEventListener("input", () => { findType(input.value); });
  input.addEventListener("keydown", (ev) => {
    if (ev.key !== "Enter") return;
    clearTimeout(findTimer);
    findGo(input.value);
  });
}

// Набор с задержкой: запрос уезжает в адрес, а не в ручку напрямую, и экран
// выдачи собирается тем же путём, каким открывается по ссылке.
function findType(value) {
  clearTimeout(findTimer);
  findTimer = setTimeout(() => { findGo(value); }, FIND_WAIT);
}

function findGo(value) {
  const project = shownProject || route().proj;
  if (!project) return;
  const hash = "#" + project + "/find/" + encodeURIComponent(String(value).trim());
  if (hash === "#" + location.hash.replace(/^#/, "")) return;
  // Набор это не переход: каждая буква отдельной записью в истории браузера
  // превратила бы «назад» в перемотку по буквам. Переход на экран выдачи
  // записью остаётся, с него «назад» и возвращает на доску.
  if (route().find) location.replace(hash);
  else goKeepingChat(hash);
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
  const tr = el("div", "srow");
  tr.append(el("span", "id", row.id));
  const st = el("span", "st fst");
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
  // Черновик ведёт в накопитель, остальное на экран задачи: закрытая задача
  // открывается там же и называет свой архив словами.
  tr.addEventListener("click", () => {
    goKeepingChat(key === "drafts" ? project + "/drafts" : project + "/" + row.id);
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
  // Поле шапки держит тот же запрос: экран выдачи открывается и по ссылке, и
  // кнопкой «назад», а поле при этом пустовало бы.
  const field = document.getElementById("hq");
  if (field && document.activeElement !== field && field.value !== q) field.value = q;
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

// Экран черновика (#проект/draft/<ID>) по макету DK-328, решение 4: слева
// текст записи с путём файла, справа то, что с ней сделали. Груминг живёт
// здесь же и зовётся грумингом: «Оформить» скрывало, что у разбора четыре
// исхода вплоть до удаления. Состояний три. Груминг идёт: живой хвост и стоп.
// Груминг кончился исходом: карточка называет исход и следующий шаг, а сам
// исход сервер прочитал следами на диске. Груминг кончился вопросом: вопрос
// стоит карточкой, под ним поле уточнения и повторная ходка.
const DRAFT_ASK_HINT = "Уточнение уедет новой ходкой груминга: агент перечитает " +
  "черновик и пойдёт с начала. Писать в закончившуюся сессию дашборду нечем, и " +
  "доставки в неё он не обещает.";
const DRAFT_DROP_HINT = "Причина уедет сообщением коммита доски: файла после " +
  "удаления нет, и живёт она только там.";

// Исход груминга словами: что случилось и что дальше. Сам след читает сервер,
// его слова стоят рядом отдельной строкой, и придумывать их второй раз на
// клиенте незачем.
const DRAFT_PHASES = {
  running: {
    head: "Груминг идёт",
    next: "Разбор кончится строкой, припиской, пометкой «отложен» либо удалением записи.",
  },
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
    next: "Ответить можно новой ходкой: агент перечитает черновик вместе с уточнением.",
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


async function dropDraft(project, id, reason) {
  sayResult("удаление черновика " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/drafts/" + encodeURIComponent(id), { method: "DELETE", body: { reason } });
  let said = r.body.message || r.body.error || "";
  if (r.ok && r.body.note) said += " (" + r.body.note + ")";
  sayResult(said, !r.ok);
  return r.ok;
}






async function renderDraft(project, works, id) {
  const groups = document.getElementById("groups");
  const base = "/api/projects/" + encodeURIComponent(project) + "/drafts/" + encodeURIComponent(id);
  // Экран записи приведён к экрану задачи (замечание 15 двенадцатого круга
  // POC): та же шапка, та же разметка вместо сырого текста, те же кнопки
  // режимов справа. Карточки «Груминга не было», «Удаление записи» и «Ход
  // груминга» сняты: первая говорила о пустоте, вторая дублировала исход, а
  // третья показывала хвост tmux, которого у живого чата груминга больше нет,
  // разбор виден в самом чате.
  const [text, chats] = await Promise.all([
    api(base),
    api("/api/projects/" + encodeURIComponent(project) + "/chats?task=" + encodeURIComponent(id)),
  ]);
  const running = Boolean((works || []).find((w) => w.id === id));
  const said = text.ok ? String(text.body.text || "") : "";
  const title = said.split("\n").find((ln) => ln.trim()) || "";
  // Груминг уже шёл, значит есть его чат: вместо кнопки ссылка туда.
  const groomChat = ((chats.ok && chats.body.chats) || [])[0] || null;

  const items = [{
    key: "draft-crumb",
    sign: project,
    make: () => {
      // Дорога на доску была только через накопитель, и с экрана записи её не
      // было вовсе.
      const crumb = el("div", "crumb");
      const board = el("span", "crumb-back", "Доска " + project);
      board.addEventListener("click", () => { goKeepingChat(project); });
      const list = el("span", "crumb-back", "Черновики");
      list.addEventListener("click", () => { goKeepingChat(project + "/drafts"); });
      crumb.append(board, el("span", "crumb-sep", "/"), list);
      return crumb;
    },
  }, {
    key: "draft-head",
    sign: [id, title, running, groomChat ? groomChat.id : ""].join("|"),
    make: () => {
      const head = el("div", "thead");
      head.append(el("span", "idbig", id));
      const name = el("div", "tedit ro dtitle", title || id);
      head.append(name);
      return head;
    },
  }, {
    key: "draft-chips",
    sign: [running, groomChat ? groomChat.id : ""].join("|"),
    make: () => {
      const chips = el("div", "tchips");
      chips.append(el("span", "chip", "черновик"));
      if (running) chips.append(el("span", "chip c-run", "груминг идёт"));
      chips.append(el("span", "gap"));
      const modes = el("div", "tmodes");
      if (groomChat) {
        const go = el("button", "btn btn-sm", "Чат груминга");
        go.addEventListener("click", () => { openChat(chatAddr(project, groomChat.id)); });
        modes.append(go);
      } else {
        const groom = el("button", "btn btn-sm btn-acc", "Провести груминг");
        if (text.ok && text.body.order) withTip(groom, "Заказ агенту: «" + text.body.order + "».");
        groom.addEventListener("click", () => {
          groom.disabled = true;
          groomDraft(project, id).then((ok) => {
            groom.disabled = false;
            if (ok) refresh().catch(console.error);
          }).catch((err) => { groom.disabled = false; console.error(err); });
        });
        modes.append(groom);
      }
      chips.append(modes);
      return chips;
    },
  }, {
    key: "draft-text",
    sign: [text.ok, said || text.body.error].join("|"),
    make: () => {
      const card = el("div", "card fpanel");
      const body = el("div", "fbody");
      if (!text.ok) {
        // Пропавший файл это не поломка экрана, а след исхода: груминг мог
        // увести запись строкой, припиской или удалением.
        body.append(el("div", "empty", text.body.error || "текст записи не прочитался"));
      } else if (said.trim()) {
        const view = el("div", "fview");
        view.dataset.file = text.body.file || "";
        view.append(mdRender(said));
        body.append(view);
      } else {
        body.append(el("div", "empty", "запись пуста"));
      }
      card.append(body);
      return card;
    },
  }];
  sync(groups, items);
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
const newForm = { project: "", draft: false, text: "", type: "task", cost: "-",
  parts: [0, 0, 0, 0, 0], accept: "agent", barrier: "", reason: "" };

function resetNewForm(project) {
  newForm.project = project;
  newForm.draft = false;
  newForm.text = "";
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

async function makeDraft(project, text, btns) {
  sayResult("запись черновика...");
  return sendNew(btns, async () => {
    const r = await api("/api/projects/" + encodeURIComponent(project) + "/drafts",
      { method: "POST", body: { text } });
    let said = r.body.message || r.body.error || "";
    if (r.ok && r.body.note) said += " (" + r.body.note + ")";
    sayResult(said, !r.ok);
    return r.ok ? r.body : null;
  });
}

async function makeTask(project, body, btns) {
  sayResult("заведение задачи...");
  return sendNew(btns, async () => {
    const r = await api("/api/projects/" + encodeURIComponent(project) + "/tasks",
      { method: "POST", body });
    let said = r.body.message || r.body.error || "";
    if (r.ok && r.body.note) said += " (" + r.body.note + ")";
    sayResult(said, !r.ok);
    return r.ok ? r.body : null;
  });
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

  const crumb = el("div", "crumb");
  const back = el("span", "crumb-back", "Доска " + project);
  back.addEventListener("click", () => { goKeepingChat(project); });
  crumb.append(back);
  groups.append(crumb);

  const card = el("div", "card nform");
  const head = el("div", "nfhead", (newForm.draft ? "Новый черновик в " : "Новая задача в ") + project);
  card.append(head);
  const box = el("div", "nfbody");
  card.append(box);
  groups.append(card);

  // Переключатель: одна форма, два места, куда ляжет написанное.
  const swch = el("div", "swch");
  const asTask = el("div", newForm.draft ? "" : "on", "Задача");
  const asDraft = el("div", newForm.draft ? "on" : "", "Черновик");
  swch.append(asTask, asDraft);
  box.append(swch);

  // Пометка про груминг стоит только у черновика и говорит сразу обе правды:
  // и чего у него нет, и кто это выдаст.
  const note = el("div", "dnote");
  note.append(el("b", "", DRAFT_NOTE_HEAD), document.createTextNode(" " + DRAFT_NOTE));
  note.hidden = !newForm.draft;
  box.append(note);

  const field = el("div", "");
  field.append(el("span", "flab", "Заголовок"));
  const ta = el("textarea");
  ta.value = newForm.text;
  ta.placeholder = NEW_PLACEHOLDER;
  ta.setAttribute("aria-label", "заголовок задачи или текст черновика");
  field.append(ta);
  box.append(field);

  // Метаданные и ранг у черновика гасятся, а не прячутся: видно, чего он
  // лишён, и форма не перестраивается при переключении.
  const meta = el("div", "meta2");
  const typePick = pickField("тип", TYPE_VALUES, newForm.type, (v) => { newForm.type = v; touch(); });
  const costPick = pickField("цена", COST_VALUES, newForm.cost, (v) => { newForm.cost = v; touch(); });
  const typeOff = el("span", "chip", DRAFT_OFF_TYPE);
  const costOff = el("span", "chip", DRAFT_OFF_COST);
  meta.append(typePick, costPick, typeOff, costOff);
  box.append(meta);

  const rankbox = el("div", "rankbox");
  const sum = el("div", "rbig");
  const sumv = el("span", "v", "0");
  const sumf = el("span", "f", "");
  sum.append(sumv, sumf);
  rankbox.append(sum);
  const picks = [];
  RANK_PARTS.forEach((part, i) => {
    const line = el("div", "rrow");
    line.append(el("span", "nm", part.name));
    line.append(el("span", "why", part.why));
    const pick = pickField("", part.values, newForm.parts[i], (v) => {
      newForm.parts[i] = Number(v);
      touch();
    });
    picks.push(pick);
    line.append(pick);
    rankbox.append(line);
  });
  box.append(rankbox);
  box.append(el("div", "hint", P_HINT));

  // Вид приёмки, барьер и причина (DK-301): вид закрытым списком из трёх,
  // барьер из шести показывается только у не агентского вида, и причина без
  // него не пишется. Списки и на телефоне остаются нативными select: два тапа
  // это вся работа, которую тут можно сделать всерьёз.
  const acceptBox = el("div", "accbox");
  const acceptPick = pickField("вид приёмки", ACCEPT_VALUES, newForm.accept, (v) => {
    newForm.accept = v;
    if (v === "agent") newForm.barrier = "";
    touch();
  });
  acceptPick.querySelector("select").setAttribute("aria-label", "вид приёмки задачи");
  acceptBox.append(acceptPick);
  const barrierPick = pickField("барьер", BARRIER_VALUES, newForm.barrier, (v) => {
    newForm.barrier = v;
    touch();
  });
  const barrierSel = barrierPick.querySelector("select");
  barrierSel.firstElementChild.textContent = BARRIER_PLACEHOLDER;
  barrierSel.setAttribute("aria-label", "барьер приёмки");
  acceptBox.append(barrierPick);
  box.append(acceptBox);
  box.append(el("div", "hint", ACCEPT_HINT));
  const barrierHint = el("div", "hint", ACCEPT_BARRIER_HINT);
  box.append(barrierHint);

  const reasonField = el("div", "");
  reasonField.append(el("span", "flab", "Почему обход не годится"));
  const reason = el("input");
  reason.type = "text";
  reason.value = newForm.reason;
  reason.placeholder = "что мешает проверить агенту";
  reason.setAttribute("aria-label", "причина непригодности обхода");
  reason.addEventListener("input", () => { newForm.reason = reason.value; touch(); });
  reasonField.append(reason);
  box.append(reasonField);

  const bad = el("div", "error", "");
  const btns = el("div", "tbtns");
  const send = el("button", "btn btn-acc", "Завести задачу");
  btns.append(send);
  const hint = el("div", "hint", FULL_HINT);
  const runHint = el("div", "hint", NEW_RUN_HINT);
  box.append(hint, runHint, btns, bad);

  // Режим меняет подписи и гасит лишнее, но не перестраивает форму: поля
  // остаются на своих местах, а написанное переживает переключение.
  const paint = () => {
    const draft = newForm.draft;
    head.textContent = (draft ? "Новый черновик в " : "Новая задача в ") + project;
    asTask.className = draft ? "" : "on";
    asDraft.className = draft ? "on" : "";
    note.hidden = !draft;
    typePick.hidden = draft;
    costPick.hidden = draft;
    typeOff.hidden = !draft;
    costOff.hidden = !draft;
    meta.classList.toggle("off", draft);
    rankbox.classList.toggle("off", draft);
    // У агентского вида барьера нет, и поля под ним не прячутся, а гасятся:
    // черновику их заполняет груминг, агентскому виду они не нужны вовсе.
    acceptBox.classList.toggle("off", draft);
    const bare = draft || newForm.accept === "agent";
    barrierPick.hidden = bare;
    barrierHint.hidden = bare;
    reasonField.hidden = bare;
    runHint.hidden = draft;
    for (const pick of picks) pick.hidden = draft;
    sumv.textContent = draft ? "-" : String(newForm.parts.reduce((a, b) => a + Number(b), 0));
    sumf.textContent = draft ? DRAFT_OFF_RANK : "= " + newForm.parts.join("+");
    // У черновика подсказка шкалы ни к чему, и на её месте стоит одна подпись
    // про то, кто эти поля заполнит.
    const whys = rankbox.querySelectorAll(".rrow .why");
    RANK_PARTS.forEach((part, i) => {
      whys[i].textContent = draft ? (i === 0 ? DRAFT_OFF_PARTS : "") : part.why;
    });
    send.textContent = draft ? "Записать черновик" : "Завести задачу";
    hint.textContent = draft ? DRAFT_HINT : FULL_HINT;
  };

  // Рубежи те же, что у ручек: поправка на баг не про новую работу, а строки
  // без заголовка и черновика без текста не бывает.
  const touch = () => {
    newForm.text = ta.value;
    paint();
    if (newForm.draft) {
      bad.textContent = "";
      send.disabled = !newForm.text.trim();
      return;
    }
    // Рубеж тот же, что у воротов add: у не агентского вида барьер обязателен,
    // и отказ называется словами до отправки, а не после.
    bad.textContent = newForm.type === "task" && Number(newForm.parts[3]) === 5 ? BUG_PART_REFUSAL
      : !newForm.text.trim() ? "заголовок задачи пустым не бывает"
      : newForm.accept !== "agent" && !newForm.barrier ?
        "у не агентского вида назван барьер из шести: без него приёмка повисает без причины"
      : "";
    send.disabled = Boolean(bad.textContent);
  };
  ta.addEventListener("input", touch);
  for (const [node, draft] of [[asTask, false], [asDraft, true]]) {
    node.addEventListener("click", () => {
      newForm.draft = draft;
      sayResult("");
      touch();
    });
  }
  send.addEventListener("click", () => {
    const text = ta.value.trim();
    if (!text || bad.textContent) return;
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
  });
  touch();
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
  b.append(el("div", "t1", n.title));
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

function renderHome(projects) {
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
    row.addEventListener("click", () => { goKeepingChat(p.name); });
    card.append(row);
  }
  groups.append(card);
  const quota = el("div", "card qcard squota");
  quota.id = "quota-card";
  groups.append(quota);
  paintQuota();
  if (!shownProject) return;
  const bar = el("div", "nbar");
  const labels = homeBarLabels(shownProject);
  bar.append(newTaskButton(shownProject, labels.make), draftsButton(shownProject, labels.drafts));
  groups.append(bar);
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
  if (w.via === "registry") chips.push(el("span", "chip c-check", "ведёт другая сессия"));
  else if (w.via === "session") chips.push(el("span", "chip", "интерактивная сессия"));
  else if (w.kind !== "goal") chips.push(el("span", "chip", "конвейер задачи"));
  return chips;
}

// Подпись под заголовком: ID, статус со строки доски, чем работа видна и имя
// сессии, по которому её находят в tmux. У работы, чьей строки на доске нет,
// статуса в подписи тоже нет: взять его неоткуда.
function workSub(w) {
  const parts = [];
  if (w.id) parts.push(w.id);
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

function agentRow(project, w, now) {
  const row = el("div", "arow");
  row.append(el("span", "dot" + (w.via === "registry" ? " dot-other" : " pulse")));
  const box = el("div", "ab");
  const line = el("div", "l1");
  // Заголовок задачи идёт первым: имя сессии goal-DK-112 о занятии агента не
  // говорит ничего, и место ему в подписи.
  // Подпись это задача, а у сессии без задачи заголовок чата, который сервер
  // берёт той же лестницей, что список чатов (замечание 1 восьмого круга).
  line.append(el("span", "tt", w.title || w.id || w.note || "чат без задачи"));
  for (const chip of workChips(project, w)) line.append(chip);
  box.append(line, el("div", "l2", workSub(w)));
  row.append(box);

  const acts = el("div", "aacts");
  const age = workAge(w.started, now);
  if (age) acts.append(el("span", "atime", age));
  if (w.via === "registry") {
    // Работа поднята мимо дашборда: её сессией он не распоряжается, и вместо
    // кнопок остаётся переход на задачу.
    acts.append(el("span", "stale",
      "работа поднята мимо дашборда: остановить можно там, где поднята"));
    if (w.id) acts.append(goButton("Открыть задачу", project + "/" + w.id));
  } else if (w.id) {
    // Вход в чат один на цель и задачу: после слияния экранов это одна и та же
    // панель, а ручку для реплики выбирает она сама (DK-435). Панель встаёт
    // поверх текущего раздела, а не уводит на доску: хвост /chat/ клеится к
    // адресу раздела, как он клеится к задаче (замечание 3 восьмого круга).
    const talk = el("button", "btn btn-sm", "Чат агента");
    talk.addEventListener("click", () => { openChat(chatAddr(project, w.id)); });
    acts.append(talk);
    if (w.via === "tmux") {
      const stop = withTip(el("button", "btn btn-sm btn-danger", "Остановить"), STOP_TIP);
      stop.addEventListener("click", () => { stopRun(project, w.id).catch(console.error); });
      acts.append(stop);
    }
  } else if (w.session) {
    // Задачу сессии узнать не удалось, и адресовать панель по ID нечем: чат
    // открывается по id сессии (DK-294).
    const talk = el("button", "btn btn-sm", "Чат");
    talk.addEventListener("click", () => { openChat(chatAddr(project, w.session)); });
    acts.append(talk);
  }
  row.append(acts);
  return row;
}

function renderAgents(projects) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const card = el("div", "card");
  const list = allWorks(projects);
  if (!list.length) {
    const empty = el("div", "empty");
    empty.append(el("b", "", "Агентов сейчас нет."));
    empty.append(document.createTextNode(
      "Запустите задачу с доски: кнопка «В работу» есть в строке задачи и на её экране."));
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

function screenKey(rt) {
  // Запрос в ключ не входит: набор буквы это не переход на другой экран, и
  // выдача обязана перерисоваться по месту, а не собраться заново под пальцем.
  // Разговора в ключе нет вовсе: панель это хвост адреса, она стоит своим
  // узлом и своими потоками, а экран под ней от её открытия не меняется и
  // собираться заново не должен.
  return [rt.proj, rt.id, rt.home, rt.agents, rt.feed, rt.make, rt.drafts,
    rt.find, rt.draft].join("|");
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
    document.getElementById("pname").textContent = "Агенты";
    document.getElementById("psub").textContent = "все активные задачи";
    renderLive("", []);
    markNav(rt);
    renderAgents(projects);
    return;
  }
  if (rt.home) {
    document.getElementById("pname").textContent = "Проекты";
    document.getElementById("psub").textContent = "главная, доски из корней конфига";
    renderLive("", []);
    markNav(rt);
    renderHome(projects);
    return;
  }
  if (!current) {
    document.getElementById("pname").textContent = "Проектов нет";
    document.getElementById("psub").textContent = "";
    showError((body.errors || []).join("; ") || "в корнях конфига не нашлось ни одной доски docs/TASKS.md");
    return;
  }
  document.getElementById("pname").textContent = current.name;
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
  document.getElementById("psub").textContent =
    "доска docs/TASKS.md" + (board.prefix ? ", " + board.prefix : "");
  renderBoard(current.name, board);
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
  const on = rt.home ? "home" : rt.agents ? "agents" : rt.feed ? "feed"
    : rt.find ? "find" : "board";
  for (const [name, ids] of [["home", ["nav-home", "tab-home"]],
    ["board", ["nav-board", "tab-board"]],
    ["agents", ["nav-agents", "tab-agents"]],
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
  ["bell", "/feed"], ["find-btn", "/find/"]]) {
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
wireFindField(document.getElementById("hq"));
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
