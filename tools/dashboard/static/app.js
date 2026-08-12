// Экраны доски, задачи и живого статуса агента: список проектов, живые
// работы, секции со строками, запуск и стоп, журнал цикла с транскриптом.
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
// "#проект/DK-NNN" задача, "#проект/agent/DK-NNN" живой статус агента,
// "#проект/chat/DK-NNN" переписка с агентом цели, "#проект/feed" лента
// уведомлений.
function route() {
  const h = decodeURIComponent(location.hash.replace(/^#/, ""));
  const parts = h.split("/");
  if (!h) return { proj: "", id: "", home: true };
  if (parts.length >= 2 && parts[1] === "feed") {
    return { proj: parts[0], id: "", feed: true };
  }
  if (parts.length >= 2 && parts[1] === "new") {
    return { proj: parts[0], id: "", make: true };
  }
  if (parts.length >= 2 && parts[1] === "drafts") {
    return { proj: parts[0], id: parts[2] || "", drafts: true };
  }
  if (parts.length >= 3 && parts[1] === "agent") {
    return { proj: parts[0], id: parts[2], agent: true };
  }
  if (parts.length >= 3 && parts[1] === "chat") {
    return { proj: parts[0], id: parts[2], chat: true };
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
// не из ленты: запись уведомителя машинная и проекта в себе не несёт, а
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
    item.addEventListener("click", () => { location.hash = p.name; });
    nav.append(item);

    const opt = el("option", "", p.name);
    opt.value = p.name;
    opt.selected = current && p.name === current.name;
    sel.append(opt);
  }
  sel.onchange = () => { location.hash = sel.value; };
}

function sayResult(text, isError) {
  const box = document.getElementById("actmsg");
  box.textContent = text || "";
  box.className = isError ? "actmsg error" : "actmsg";
}

// Запуск и стоп. Ответ сервера показывается словами: и удача, и причина
// отказа (занятый замок, пропавший tmux или goal-run) видны с экрана.
async function startRun(project, id) {
  sayResult("запуск " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/runs",
    { method: "POST", body: { id } });
  sayResult(r.body.message || r.body.error || "", !r.ok);
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
  live.replaceChildren();
  for (const w of works || []) {
    const card = el("div", "lcard");
    card.append(el("span", "dot pulse"));
    // Интерактивную сессию без узнанной задачи вести некуда: экран агента
    // открывается по ID, и вместо имени работы карточка называет её видом.
    const name = w.id || "интерактивная сессия";
    // Работа подписана номером задачи и её заголовком: служебного goal-DK-112
    // в подписи нет, о занятии агента оно не говорит ничего.
    const label = el("b", w.id ? "" : "flat", name);
    if (w.id) {
      label.addEventListener("click", () => { location.hash = project + "/agent/" + w.id; });
    }
    card.append(label);
    if (w.title) card.append(el("span", "wname wtitle", w.title));
    if (w.via === "tmux") {
      const stop = withTip(el("button", "btn btn-sm btn-danger", "Стоп"), STOP_TIP);
      stop.addEventListener("click", () => { stopRun(project, w.id).catch(console.error); });
      card.append(stop);
    } else if (w.via === "session") {
      card.append(el("span", "via", w.note || "интерактивная сессия"));
    } else {
      card.append(el("span", "via", "ведёт другая сессия"));
    }
    live.append(card);
  }
}

function rowChips(row) {
  const chips = [];
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

// Действие прямо со строки: поднять конвейер или снять живую сессию, не заходя
// внутрь задачи. Ручки те же, что у экрана задачи (POST и DELETE runs), и
// ответ выходит в ту же строку результата.
function rowAction(project, row, works, sect) {
  const work = (works || []).find((w) => w.id === row.id);
  if (work && work.via === "session") {
    return el("span", "stale", "интерактивная сессия");
  }
  if (work && work.via !== "tmux") {
    return el("span", "stale", "ведёт другая сессия");
  }
  if (!work && row.after && row.after.length) {
    // Заблокированную маркером задачу конвейер брать не должен, и кнопка
    // говорит это сама: погашенная с причиной понятнее исчезнувшей.
    const wait = el("button", "btn btn-sm", actionLabel(sect));
    wait.disabled = true;
    return withTip(wait, "сначала " + row.after.join(", "));
  }
  const btn = el("button", "btn btn-sm" + (work ? "" : " btn-acc"), work ? "Стоп" : actionLabel(sect));
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    const call = work ? stopRun(project, row.id) : startRun(project, row.id);
    call.catch(console.error);
  });
  return btn;
}

function renderRow(project, row, works, sect) {
  const tr = el("div", "trow");
  tr.append(el("span", "id", row.id));
  const tt = el("span", "tt");
  tt.append(el("span", "ttl", row.title));
  for (const chip of rowChips(row)) tt.append(chip);
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
  meta.append(rowAction(project, row, works, sect));
  tr.append(meta);
  tr.addEventListener("click", () => { location.hash = project + "/" + row.id; });
  return tr;
}

function renderBoard(project, board, works) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const bar = el("div", "nbar");
  bar.append(newTaskButton(project, "Новая задача"), draftsButton(project));
  groups.append(bar);
  const byKey = {};
  for (const sec of board.sections || []) byKey[sec.key] = sec;
  for (const key of SECTION_ORDER) {
    const sec = byKey[key];
    if (!sec) continue;
    const head = el("div", "shead", sec.title);
    // Backlog стоит по рангу, и счётчик говорит это же: надписью под формой
    // задачи порядок объяснять больше не надо.
    head.append(el("span", "n", sec.rows.length + (key === "backlog" ? ", по рангу" : "")));
    groups.append(head);
    const card = el("div", "card");
    if (!sec.rows.length) {
      card.append(el("div", "empty", "Нет."));
    }
    for (const row of sec.rows) card.append(renderRow(project, row, works, key));
    groups.append(card);
  }
}

// Кнопка заведения: стоит и на доске проекта, и на главной, потому что мысль
// приходит вне машины, а не в тот момент, когда открыта нужная доска.
function newTaskButton(project, label) {
  const btn = el("button", "btn btn-acc", label);
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    location.hash = project + "/new";
  });
  return btn;
}

// Вход в накопитель черновиков стоит рядом с заведением, на доске и на
// главной: записанная с телефона мысль иначе видна только в файле, а разбирать
// её приходится с ноутбука.
function draftsButton(project) {
  const btn = el("button", "btn", "Черновики");
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    location.hash = project + "/drafts";
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
const taskDraft = { id: "", dirty: false, seen: "" };

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
  row.addEventListener("click", () => { location.hash = project + "/" + dep.id; });
  return row;
}

// Карточка зависимостей в обе стороны по макету «02 Задача»: кого ждёт задача
// и кто ждёт её. Обе стороны живут на доске одним маркером [после ...],
// поэтому вторая сторона это обратный поиск, а не вторая запись. Названы они
// словами, а не «После» и «Держит»: от тех читателю приходилось достраивать,
// кто кого ждёт.
function depsCard(project, id, after, blocks) {
  const card = el("div", "card");
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
// панели нет, она одна на всю форму. Заведение файла остаётся за taskctl (та
// же команда чинит ссылку в строке доски).
function filePanel(project, id, detail, form, touch) {
  const card = el("div", "card fpanel");
  const head = el("div", "fhead");
  head.append(el("b", "", detail.file || "docs/tasks/" + id + ".md"));
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
  const ta = el("textarea");
  ta.value = form.text;
  ta.setAttribute("aria-label", "текст файла задачи " + id);
  ta.addEventListener("input", () => { form.text = ta.value; touch(); });
  body.append(ta);
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
// Где поднимется работа: подпись кнопки говорит про заказ, а надпись рядом
// про место, откуда за ним смотреть.
function taskActionHint(isGoal, sect, id) {
  if (isGoal) {
    return "Цель поведёт оболочка goal-run в tmux-сессии goal-" + id +
      ", состояние следующий виток прочтёт с диска.";
  }
  const where = " в tmux-сессии task-" + id + ".";
  if (sect === "in-progress") {
    return "Начатую задачу конвейер продолжит с того места, где она стоит," + where;
  }
  if (sect === "check") {
    return "Проверенную задачу конвейер закроет" + where +
      " Агентский сценарий он прогонит сам, пользовательский оставит человеку.";
  }
  return "Задачу поднимет headless-сессия конвейера доски" + where;
}

// Полоса действий задачи: у живой работы её экран и стоп, у стоящей действие
// по статусу строки теми же словами, что и на доске. Собрана отдельной
// функцией, а не внутри экрана: тексты действий держат тесты, и смотреть им
// на весь renderTask ради одной полосы незачем.
function taskActions(project, id, row, works) {
  const out = [];
  const isGoal = /^Цель:/.test(row.title);
  const work = (works || []).find((w) => w.id === id);
  if (work) {
    const live = el("button", "btn", "Живой статус");
    live.addEventListener("click", () => { location.hash = project + "/agent/" + id; });
    out.push(live);
  }
  if (isGoal) {
    const chat = el("button", "btn", "Чат с агентом");
    chat.addEventListener("click", () => { location.hash = project + "/chat/" + id; });
    out.push(chat);
  }
  if (work && work.via === "tmux") {
    const stop = el("button", "btn btn-danger", "Остановить агента");
    // Последствия остановки живут подсказкой на самой кнопке: надписью рядом
    // они стояли указкой над всей полосой.
    withTip(stop, STOP_TIP);
    stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
    out.push(stop);
    return out;
  }
  if (work && work.via === "session") {
    out.push(el("span", "hint", "Задачу ведёт интерактивная сессия в окне агента: " +
      "остановить её из дашборда нечем, окно закрывает человек."));
    return out;
  }
  if (work) {
    out.push(el("span", "hint", "Задачу ведёт другая сессия (живой чат), tmux-сессии дашборда " +
      "у неё нет: остановить отсюда нечем, снимать там, где она поднята."));
    return out;
  }
  const label = actionLabel(row.sect);
  if (row.after && row.after.length) {
    // Заблокированную маркером задачу конвейер брать не должен: кнопка стоит
    // погашенной с причиной, а не пропадает с полосы.
    const wait = el("button", "btn", label);
    wait.disabled = true;
    out.push(withTip(wait, "сначала " + row.after.join(", ")));
    out.push(el("span", "hint", "Задача ждёт " + row.after.join(", ") +
      ": пока маркер стоит, конвейер её не возьмёт."));
    return out;
  }
  const start = el("button", "btn btn-acc", label);
  start.addEventListener("click", () => { startRun(project, id).catch(console.error); });
  out.push(start);
  out.push(el("span", "hint", taskActionHint(isGoal, row.sect, id)));
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
    row.addEventListener("click", () => { location.hash = project + "/" + task.id; });
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

  const crumb = el("div", "crumb");
  const back = el("span", "crumb-back", "Доска " + project);
  back.addEventListener("click", () => { location.hash = project; });
  crumb.append(back);
  groups.append(crumb);

  if (!r.ok) {
    const card = el("div", "card");
    card.append(el("div", "error", r.body.error || "задача не прочиталась"));
    groups.append(card);
    return;
  }
  const detail = r.body;
  const row = detail.row || {};
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
  const title = el("textarea", "tedit");
  title.value = form.title;
  title.setAttribute("aria-label", "заголовок задачи " + id);
  title.addEventListener("input", () => { form.title = title.value; touch(); });
  head.append(title);
  groups.append(head);

  const chips = el("div", "tchips");
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
  groups.append(chips);

  // Сохранение и действия одной полосой над содержимым (макет «02 Задача»):
  // отдельной карточки действий у задачи больше нет, а надписи про пустую
  // правку нет вовсе, о ней говорит погашенная кнопка. Сохранение одно на всю
  // форму: любое изменение поля включает кнопку, и по ней уезжает всё
  // изменённое разом.
  const bar = el("div", "card abar");
  const save = el("button", "btn btn-acc", "Сохранить");
  const drop = el("button", "btn", "Отменить правку");
  // Отменять нечего, пока правки нет: мёртвая кнопка на полосе только мешает.
  drop.hidden = true;
  const bad = el("div", "error", "");
  bar.append(save, drop, el("span", "div"));
  groups.append(bar);

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
    drop.hidden = !dirty;
  };
  save.addEventListener("click", () => {
    const patch = patchBody();
    const text = textBody();
    if (draftRefusal(form, text)) return;
    saveTaskDraft(project, id, Object.keys(patch).length ? patch : null, text).catch(console.error);
  });
  drop.addEventListener("click", () => {
    taskDraft.dirty = false;
    renderTask(project, works, id).catch(console.error);
  });

  for (const node of taskActions(project, id, row, works)) bar.append(node);
  bar.append(bad);

  // Состав цели стоит над содержимым: с экрана цели смотрят прежде всего на
  // него. Ждать его отрисовка задачи не обязана, состав приезжает отдельным
  // запросом и встаёт на своё место сам.
  if (/^Цель:/.test(row.title || "")) {
    const comp = el("div", "");
    groups.append(comp);
    goalComposition(project, id, comp).catch(console.error);
  }

  const rank = el("div", "card");
  const rhead = el("div", "rhead");
  rhead.append(el("b", "", "Ранг"), el("span", "stale", "по RANKING.md"));
  rank.append(rhead);
  const big = el("div", "rbig");
  big.append(el("span", "v", String(row.r)));
  big.append(el("span", "f", "= " + (row.r_parts || []).join("+")));
  rank.append(big);
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
    rank.append(line);
  });

  const rail = el("div", "rrail");
  rail.append(rank, depsCard(project, id, detail.after || [], detail.blocks || []));
  const grid = el("div", "tgrid");
  grid.append(filePanel(project, id, detail, form, touch), rail);
  groups.append(grid);

  taskDraft.id = id;
  taskDraft.dirty = false;
  taskDraft.seen = taskSeen(detail);
  touchForm();
}

// Живые потоки экрана агента: EventSource журнала и транскрипта, таймер
// снимка tmux. Закрываются при любом уходе с экрана, иначе соединения
// копились бы с каждым переходом.
let agentLive = [];
function closeAgentLive() {
  for (const stop of agentLive) stop();
  agentLive = [];
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
function mdRender(text) {
  const box = el("div", "md");
  const lines = String(text || "").split("\n");
  let list = null;
  let para = null;
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (/^\s*```/.test(line)) {
      const buf = [];
      for (i++; i < lines.length && !/^\s*```/.test(lines[i]); i++) buf.push(lines[i]);
      list = null;
      para = null;
      box.append(el("pre", "", buf.join("\n")));
      continue;
    }
    const head = line.match(/^(#{1,6})\s+(.*)$/);
    if (head) {
      list = null;
      para = null;
      const h = el("div", "mdh mdh" + head[1].length);
      mdInline(head[2], h);
      box.append(h);
      continue;
    }
    const item = line.match(/^\s*([-*+]|\d+[.)])\s+(.*)$/);
    if (item) {
      para = null;
      const tag = /\d/.test(item[1]) ? "ol" : "ul";
      if (!list || list.tagName.toLowerCase() !== tag) {
        list = el(tag);
        box.append(list);
      }
      const li = el("li");
      mdInline(item[2], li);
      list.append(li);
      continue;
    }
    if (!line.trim()) {
      list = null;
      para = null;
      continue;
    }
    if (para) {
      para.append(document.createTextNode("\n"));
      mdInline(line, para);
      continue;
    }
    list = null;
    para = el("p");
    mdInline(line, para);
    box.append(para);
  }
  return box;
}

function replyEl(item) {
  if (item.role === "thinking") return el("div", "think", "размышления свёрнуты");
  if (item.role === "tool") {
    const div = el("div", "tool");
    div.append(el("b", "", item.tool || "инструмент"));
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
// и до неё, поэтому меряется расстояние от низа, а не сверху.
function keepPlace(box, tail) {
  box.scrollTop = box.scrollHeight - tail;
}

// Подпись сессии в списке: узнанная задача с источником узнавания либо
// честное «задача не распознана» (DK-252).
function sessionSign(s) {
  return s.task ? s.task + ", " + (s.taskNote || "узнана") : (s.taskNote || "задача не распознана");
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

// Транскрипт: сессия, узнанная этой задачей (?task=), живое дострение через
// SSE, пагинация назад кнопкой «раньше» через ?before=. Свежую сессию проекта
// экран больше не берёт: при двух окнах по одному проекту под заголовком
// задачи шёл ход соседней работы (DK-252).
async function wireTranscript(project, tp, id) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/sessions?task=" + encodeURIComponent(id));
  if (!r.ok) {
    say(tp.body, "error", r.body.error || "сессии не прочитались");
    return;
  }
  const list = r.body.sessions || [];
  if (!list.length) {
    say(tp.body, "empty", r.body.note || "сессий этой задачи нет");
    await listOtherSessions(project, tp.body);
    return;
  }
  const s = list[0];
  tp.sub.textContent = s.id.slice(0, 8) + ".jsonl" + (s.branch ? ", " + s.branch : "") +
    " (" + sessionSign(s) + ")";
  const more = el("div", "more", "раньше");
  const feed = el("div");
  tp.body.append(more, feed);
  let firstSeq = null;
  // Кнопка «раньше» горит только когда раньше есть что показать: пока лента
  // пуста или упёрлась в начало, кнопка гаснет, а не живёт мёртвой.
  const updateMore = () => { more.hidden = firstSeq === null || firstSeq === 0; };
  updateMore();
  more.addEventListener("click", async () => {
    if (firstSeq === null || firstSeq === 0) return;
    const older = await api("/api/projects/" + encodeURIComponent(project) +
      "/sessions/" + encodeURIComponent(s.id) + "?before=" + firstSeq);
    if (!older.ok) return;
    const tail = tp.body.scrollHeight - tp.body.scrollTop;
    for (const item of (older.body.items || []).reverse()) {
      feed.prepend(replyEl(item));
      firstSeq = item.seq;
    }
    keepPlace(tp.body, tail);
    updateMore();
  });
  const es = new EventSource("/api/projects/" + encodeURIComponent(project) +
    "/sessions/" + encodeURIComponent(s.id) + "?stream=1");
  agentLive.push(() => es.close());
  // Пустая лента приходит событием note: слова вместо пустой коробки,
  // неотличимой от оборвавшегося потока.
  es.addEventListener("note", (ev) => {
    if (!feed.childElementCount) say(feed, "empty", ev.data);
  });
  es.onmessage = (ev) => {
    const item = JSON.parse(ev.data);
    const was = atBottom(tp.body);
    if (feed.firstChild && feed.firstChild.className === "empty") feed.replaceChildren();
    if (firstSeq === null) {
      firstSeq = item.seq;
      updateMore();
    }
    feed.append(replyEl(item));
    keepBottom(tp.body, was);
  };
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

// Экран живого статуса агента по макету DK-216 («03 Агент»): на ноутбуке
// журнал и транскрипт рядом, tmux полосой внизу; на телефоне те же панели
// табами.
function renderAgent(project, works, id) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();

  const crumb = el("div", "crumb");
  const back = el("span", "crumb-back", "Доска " + project);
  back.addEventListener("click", () => { location.hash = project; });
  crumb.append(back);
  groups.append(crumb);

  const work = (works || []).find((w) => w.id === id);
  const head = el("div", "ahead");
  if (work) head.append(el("span", "dot pulse"));
  // Шапка зовёт работу заголовком с доски, а имя сессии остаётся подписью:
  // goal-XR-100 о занятии агента не говорит ничего.
  const name = (work && work.via !== "session" ? work.kind + "-" : "") + id;
  const title = work && work.title;
  head.append(el("h2", title ? "wtitle" : "", title || name));
  if (title) head.append(el("span", "wname", name));
  if (work && work.via === "tmux") {
    head.append(el("span", "chip c-check", "tmux-сессия активна"));
    const stop = withTip(el("button", "btn btn-danger", "Остановить агента"), STOP_TIP);
    stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
    head.append(stop);
  } else if (work && work.via === "session") {
    // Кнопки стопа тут нет: сессию ведёт человек в окне, и снимать её дашборду
    // нечем.
    head.append(el("span", "chip c-check", "интерактивная сессия"));
  } else if (work) {
    head.append(el("span", "chip", "ведёт другая сессия"));
  } else {
    head.append(el("span", "chip", "работа не идёт"));
  }
  // Чат это переписка с циклом цели: у обычной задачи отправка получила бы
  // «не цель», и кнопка вела бы в тупик. Вид работы приходит со строки доски,
  // и интерактивная сессия обычной задачи сюда не попадает.
  if (!work || work.kind === "goal") {
    const chat = el("button", "btn", "Чат с агентом");
    chat.addEventListener("click", () => { location.hash = project + "/chat/" + id; });
    head.append(chat);
  }
  groups.append(head);

  const jp = pane("Журнал агента", "источник назовёт сервер");
  // Живой хвост назван без одушевления: обновляется журнал сам, а не «живёт».
  jp.head.append(el("span", "chip c-run", "хвост обновляется"));
  const tp = pane("Лог витка", "");
  const grid = el("div", "agrid");
  grid.append(jp.card, tp.card);
  const tm = el("div", "card tmuxbar");
  const tmHead = el("div", "phd");
  tmHead.append(el("b", "", "tmux"));
  const tmSub = el("span", "", "");
  tmHead.append(tmSub);
  tm.append(tmHead);

  // Телефон: те же панели табами, переключение классом onpane.
  const seg = el("div", "seg");
  const tabs = [jp.card, tp.card, tm];
  ["Журнал", "Лог витка", "tmux"].forEach((name, i) => {
    const d = el("div", i === 0 ? "on" : "", name);
    d.addEventListener("click", () => {
      Array.from(seg.children).forEach((x, j) => { x.className = j === i ? "on" : ""; });
      tabs.forEach((p, j) => p.classList.toggle("onpane", j === i));
    });
    seg.append(d);
  });
  tabs[0].classList.add("onpane");

  groups.append(seg, grid, tm);
  wireJournal(project, id, jp.body, jp.sub);
  wireTranscript(project, tp, id).catch(console.error);
  wireTmux(id, tm, tmSub);
}

// Экран чата с агентом по макету DK-216 («04 Переписка»). Ход и ответы
// читаются из транскрипта сессии, узнанной этой целью (API DK-219), а
// сообщение человека уходит в раздел «Входящие» файла цели: писать в идущую
// сессию механики нет, сообщение агент прочитает при следующем запуске, и
// надпись говорит это прямо.
function dayEl(date) {
  const day = el("div", "day");
  day.append(el("i"), document.createTextNode(date), el("i"));
  return day;
}

function chatBubble(who, text, meta) {
  const wrap = el("div", "msg" + (who === "вы" ? " me" : ""));
  const bb = el("div", "bb");
  bb.append(mdRender(text));
  wrap.append(bb);
  wrap.append(el("div", "mm", who + ", " + meta));
  return wrap;
}

// Текстовая реплика человека или агента: свёрнутые инструменты и размышления
// в чат не идут, им место в транскрипте.
function chatTalk(item) {
  return (item.role === "user" || item.role === "assistant") && Boolean(item.text);
}

// Сколько реплик читается при открытии: чат открывается концом разговора, и
// тянуть всю сессию ради последних слов незачем. Остальное подаёт «раньше».
const CHAT_TAIL = 40;

// Лента чата: последние реплики и поле ввода видны сразу, история
// подгружается кнопкой «раньше» вверх. Сессия берётся узнанная этой целью
// (?task=), чужая в чат не попадает (DK-252). Пустоты различимы: «сессий этой
// цели нет», «транскриптов нет вовсе» и «в транскрипте нет реплик» это разные
// слова.
async function wireChatFeed(project, feed, id) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/sessions?task=" + encodeURIComponent(id));
  if (!r.ok) {
    say(feed, "error", r.body.error || "сессии не прочитались");
    return;
  }
  const list = r.body.sessions || [];
  if (!list.length) {
    say(feed, "empty", "цель не гонялась: " + (r.body.note || "транскриптов сессий нет"));
    await listOtherSessions(project, feed);
    return;
  }
  const sid = list[0].id;
  const more = el("div", "more", "раньше");
  const box = el("div", "mlist");
  feed.replaceChildren(more, box);

  const talk = [];
  let lastSeq = -1;
  let firstSeq = null;
  let empty = "переписки пока нет: в транскрипте нет текстовых реплик";
  // Кнопка «раньше» горит, только когда раньше есть что показать: упёршись в
  // начало разговора, она гаснет, а не живёт мёртвой.
  const updateMore = () => { more.hidden = firstSeq === null || firstSeq === 0; };
  // Лента перерисовывается целиком: разделители дней зависят от соседей, и
  // догруженная история переставляет их сама собой. Взгляд при этом держится
  // якорем: у нижнего края лента остаётся у нижнего края, а из истории её
  // вниз не бросает, потому что мерится расстояние до низа.
  const draw = () => {
    const bottom = atBottom(feed);
    const tail = feed.scrollHeight - feed.scrollTop;
    if (!talk.length) {
      box.replaceChildren(el("div", "empty", empty));
      return;
    }
    box.replaceChildren();
    let day = "";
    for (const item of talk) {
      const key = localDayKey(item.time);
      if (key && key !== day) {
        box.append(dayEl(localDay(item.time)));
        day = key;
      }
      const when = item.time ? localTime(item.time) + ", " : "";
      box.append(chatBubble(item.role === "user" ? "вы" : "агент", item.text,
        when + "из транскрипта"));
    }
    if (bottom) keepBottom(feed, true);
    else keepPlace(feed, tail);
  };

  const first = await api("/api/projects/" + encodeURIComponent(project) +
    "/sessions/" + encodeURIComponent(sid) + "?n=" + CHAT_TAIL);
  if (first.ok) {
    const items = first.body.items || [];
    if (items.length) firstSeq = items[0].seq;
    for (const item of items) {
      lastSeq = item.seq;
      if (chatTalk(item)) talk.push(item);
    }
    if (first.body.note) empty = first.body.note;
  }
  // Открытие хвостом: последние слова разговора видны сразу, листать вниз от
  // начала сессии не приходится.
  draw();
  feed.scrollTop = feed.scrollHeight;
  updateMore();

  more.addEventListener("click", async () => {
    if (firstSeq === null || firstSeq === 0) return;
    const older = await api("/api/projects/" + encodeURIComponent(project) +
      "/sessions/" + encodeURIComponent(sid) + "?before=" + firstSeq + "&n=" + CHAT_TAIL);
    if (!older.ok) return;
    const items = older.body.items || [];
    if (items.length) firstSeq = items[0].seq;
    talk.unshift(...items.filter(chatTalk));
    draw();
    updateMore();
  });

  const es = new EventSource("/api/projects/" + encodeURIComponent(project) +
    "/sessions/" + encodeURIComponent(sid) + "?stream=1");
  agentLive.push(() => es.close());
  es.onmessage = (ev) => {
    const item = JSON.parse(ev.data);
    if (item.seq <= lastSeq) return;
    lastSeq = item.seq;
    if (firstSeq === null) {
      firstSeq = item.seq;
      updateMore();
    }
    if (!chatTalk(item)) return;
    talk.push(item);
    draw();
  };
}

// Лежащие во «Входящих» строки: сообщение отправлено, но запуска, который его
// прочитает, ещё не было, и это честно называется ожиданием. Пустой раздел
// тоже говорит словами: пустая коробка неотличима от неотрисованной.
async function loadPending(project, id, box) {
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/goals/" + encodeURIComponent(id) + "/message");
  box.replaceChildren();
  if (!r.ok) {
    box.append(el("div", "error", r.body.error || "«Входящие» не прочитались"));
    return;
  }
  const pending = r.body.pending || [];
  if (!pending.length) {
    box.append(el("div", "empty", r.body.note || "во «Входящих» пусто: непрочитанных сообщений нет"));
    return;
  }
  for (const line of pending) {
    box.append(chatBubble("вы", line, "подхвачено следующим витком"));
  }
}

async function sendMessage(project, id, ta, pendbox) {
  const text = ta.value.trim();
  if (!text) return;
  sayResult("отправка сообщения для " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/goals/" + encodeURIComponent(id) + "/message",
    { method: "POST", body: { text } });
  let said = r.body.message || r.body.error || "";
  if (r.ok && r.body.note) said += " (" + r.body.note + ")";
  sayResult(said, !r.ok);
  if (r.ok) {
    ta.value = "";
    await loadPending(project, id, pendbox);
  }
}

function renderChat(project, works, id) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();

  const crumb = el("div", "crumb");
  const back = el("span", "crumb-back", "Доска " + project);
  back.addEventListener("click", () => { location.hash = project; });
  crumb.append(back);
  groups.append(crumb);

  const work = (works || []).find((w) => w.id === id);
  const head = el("div", "ahead");
  if (work) head.append(el("span", "dot pulse"));
  head.append(el("h2", "", "goal-" + id));
  if (work && work.via === "tmux") {
    head.append(el("span", "chip c-run", "агент работает"));
  } else if (work && work.via === "session") {
    head.append(el("span", "chip c-check", "интерактивная сессия"));
  } else if (work) {
    head.append(el("span", "chip", "ведёт другая сессия"));
  } else {
    head.append(el("span", "chip", "агент не работает"));
  }
  groups.append(head);

  const thread = el("div", "chatwrap");
  const feed = el("div", "msgs chatfeed");
  const pendbox = el("div", "msgs");
  thread.append(feed, pendbox);

  // Плашка про судьбу сообщения закрывается крестиком: прочитав её однажды,
  // держать её над полем ввода незачем.
  const note = el("div", "cnote");
  const said = el("span");
  said.append(el("b", "", "Сообщение уйдёт агенту."));
  said.append(document.createTextNode(" Он отреагирует на него на следующей рабочей итерации."));
  const close = el("button", "nx");
  close.setAttribute("aria-label", "Закрыть");
  close.title = "Закрыть";
  close.append(icon("close"));
  close.addEventListener("click", () => { note.remove(); });
  note.append(said, close);
  thread.append(note);

  const box = el("div", "cbox");
  const ta = el("textarea");
  ta.placeholder = "Написать агенту...";
  const row = el("div", "crow");
  if (work && work.via === "tmux") {
    const stop = withTip(el("button", "btn btn-danger", "Остановить агента"), STOP_TIP);
    stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
    row.append(stop);
  }
  const send = el("button", "btn btn-acc", "Отправить");
  send.addEventListener("click", () => { sendMessage(project, id, ta, pendbox).catch(console.error); });
  row.append(send);
  box.append(ta, row);
  thread.append(box);
  thread.append(el("div", "stopnote", STOP_TIP));
  groups.append(thread);

  wireChatFeed(project, feed, id).catch(console.error);
  loadPending(project, id, pendbox).catch(console.error);
}

// Раздел «Черновики» (#проект/drafts): накопитель docs/tasks/drafts/ списком,
// текст записи по нажатию и действие «Оформить». Черновик не виден на доске, и
// без этого раздела записанная с телефона мысль лежит в файле, до которого с
// телефона не добраться. Разбор поднимает ту же механику, что и конвейер
// задачи: сессия агента с заказом груминга, после которого строка оказывается
// в Backlog.
const DRAFTS_HINT = "Записанные мимо доски идеи: метаданных у них нет, ранг и " +
  "тип выдаст груминг, он же заведёт строку.";
const GROOM_HINT = "«Оформить» поднимает сессию груминга: она разберёт запись " +
  "и доведёт её до строки Backlog либо снимет с причиной.";

async function groomDraft(project, id) {
  sayResult("подъём груминга " + id + "...");
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/drafts/" + encodeURIComponent(id) + "/groom", { method: "POST", body: {} });
  sayResult(r.body.message || r.body.error || "", !r.ok);
  return r.ok;
}

// Текст черновика читается по нажатию: список показывает первую строку, а
// разбирать запись, не прочитав её целиком, нечем.
async function draftText(project, id, box) {
  box.replaceChildren(el("div", "stale", "чтение..."));
  const r = await api("/api/projects/" + encodeURIComponent(project) +
    "/drafts/" + encodeURIComponent(id));
  box.replaceChildren();
  if (!r.ok) {
    box.append(el("div", "error", r.body.error || "черновик не прочитался"));
    return;
  }
  box.append(el("div", "fhead", r.body.file || ""));
  const text = el("pre", "log");
  text.textContent = r.body.text || "";
  box.append(text);
}

function draftRow(project, d) {
  const row = el("div", "srow");
  row.append(el("span", "id", d.id));
  row.append(el("span", "st", d.title || ""));
  const meta = el("span", "sm");
  meta.append(el("span", "stale", d.age_words || ""));
  if (d.deferred) meta.append(el("span", "chip", "отложен " + d.deferred));
  const groom = el("button", "btn btn-sm btn-acc", "Оформить");
  meta.append(groom);
  row.append(meta);
  const box = el("div", "dtext");
  box.hidden = true;
  row.addEventListener("click", (ev) => {
    if (ev.target === groom) return;
    box.hidden = !box.hidden;
    if (!box.hidden) draftText(project, d.id, box).catch(console.error);
  });
  groom.addEventListener("click", (ev) => {
    ev.stopPropagation();
    groom.disabled = true;
    groomDraft(project, d.id).then((ok) => {
      groom.disabled = false;
      // Сессия поднята, и смотреть за ней там же, где за остальными работами:
      // разбор идёт под тем же ID, каким черновик станет строкой.
      if (ok) location.hash = project + "/agent/" + d.id;
    }).catch((err) => { groom.disabled = false; console.error(err); });
  });
  const wrap = el("div", "");
  wrap.append(row, box);
  return wrap;
}

async function renderDrafts(project) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const crumb = el("div", "crumb");
  const back = el("span", "crumb-back", "Доска " + project);
  back.addEventListener("click", () => { location.hash = project; });
  crumb.append(back);
  groups.append(crumb);

  const bar = el("div", "nbar");
  bar.append(newTaskButton(project, "Новая задача"), el("span", "hint", DRAFTS_HINT));
  groups.append(bar);

  const r = await api("/api/projects/" + encodeURIComponent(project) + "/drafts");
  const card = el("div", "card");
  groups.append(card);
  if (!r.ok) {
    card.append(el("div", "error", r.body.error || "накопитель не прочитался"));
    return;
  }
  const drafts = r.body.drafts || [];
  const head = el("div", "chd");
  head.append(el("b", "", "Черновики"));
  head.append(el("span", "cnt", drafts.length + " " +
    plural(drafts.length, "запись", "записи", "записей")));
  card.append(head);
  // Пустой накопитель говорит словами сервера: пустая карточка неотличима от
  // неотрисованной.
  if (!drafts.length) {
    card.append(el("div", "empty", r.body.note || "черновиков нет"));
    return;
  }
  for (const d of drafts) card.append(draftRow(project, d));
  const foot = el("div", "nbar");
  foot.append(el("span", "hint", GROOM_HINT));
  groups.append(foot);
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
  "После заведения откроется карточка задачи.";
// Взять в работу можно только сохранённое: у ненаписанной строки нет ни ID,
// ни статуса, по которому конвейер выбирает заказ. Кнопки запуска на этом
// экране нет вовсе, и сказано это словами, а не погашенной кнопкой.
const NEW_RUN_HINT = "Взять в работу можно с карточки задачи: до заведения " +
  "у неё нет ни ID, ни статуса, от которого конвейер берёт заказ.";
const NEW_PLACEHOLDER = "Что нужно сделать и зачем";
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
const newForm = { project: "", draft: false, text: "", type: "task", cost: "-", parts: [0, 0, 0, 0, 0], file: true };

function resetNewForm(project) {
  newForm.project = project;
  newForm.draft = false;
  newForm.text = "";
  newForm.type = "task";
  newForm.cost = "-";
  newForm.parts = [0, 0, 0, 0, 0];
  newForm.file = true;
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
  board.addEventListener("click", () => { location.hash = project; });
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
  back.addEventListener("click", () => { location.hash = project; });
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

  const withFile = el("label", "nfcheck");
  const flag = el("input");
  flag.type = "checkbox";
  flag.checked = newForm.file;
  flag.addEventListener("change", () => { newForm.file = flag.checked; });
  withFile.append(flag, el("span", "", "завести файл задачи по шаблону (taskctl file)"));
  box.append(withFile);

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
    withFile.hidden = draft;
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
    bad.textContent = newForm.type === "task" && Number(newForm.parts[3]) === 5 ? BUG_PART_REFUSAL
      : !newForm.text.trim() ? "заголовок задачи пустым не бывает" : "";
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
      file: newForm.file,
    };
    makeTask(project, body, [send]).then((done) => {
      if (!done) return;
      resetNewForm(project);
      // Заведённая строка открывается сразу: с телефона следующий шаг это
      // дописать постановку, а искать её глазами по Backlog неудобно.
      if (done.id) location.hash = project + "/" + done.id;
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
  // Событие было, а баннера человек не видел: причина стоит рядом, иначе
  // молчащий канал неотличим от исправного.
  if (!n.sent && n.result) b.append(el("div", "t2", "баннера не было: " + n.result));
  if (n.id) {
    const acts = el("div", "acts");
    if (n.kind === "stop") {
      const up = el("button", "btn btn-acc", "Поднять виток");
      up.addEventListener("click", () => { startRun(project, n.id).catch(console.error); });
      const jrn = el("a", "", "Журнал агента");
      jrn.href = "#" + project + "/agent/" + n.id;
      acts.append(up, jrn);
    } else {
      const open = el("button", "btn", "Открыть " + n.id);
      open.addEventListener("click", () => { location.hash = project + "/" + n.id; });
      acts.append(open);
    }
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
// времени, нажатие ведёт в ленту. Копится не больше трёх, последнее сверху.
const FLASH_LIFE = 8000;
const FLASH_MAX = 3;

// Момент подключения к потоку: хвост журнала, который сервер отдаёт сразу,
// всплывать не должен. Флеш это про то, что случилось при открытом окне.
let flashSince = "";

function showFlash(n) {
  const box = document.getElementById("flashes");
  if (!box) return;
  const card = el("div", "flash");
  card.append(el("span", "pdot " + (n.kind === "task" ? "pd-run" : "pd-warn")));
  const body = el("div", "ft");
  body.append(el("b", "", n.title || "событие ленты"));
  if (n.body) body.append(el("span", "", n.body));
  const life = el("div", "flife");
  life.style.animationDuration = FLASH_LIFE + "ms";
  body.append(life);
  card.append(body, el("span", "fw", "сейчас"));
  card.addEventListener("click", () => {
    card.remove();
    location.hash = (shownProject || route().proj) + "/feed";
  });
  box.prepend(card);
  while (box.childElementCount > FLASH_MAX) box.lastElementChild.remove();
  setTimeout(() => { card.remove(); }, FLASH_LIFE);
}

// Всплывать ли уведомлению: событие старше подключения это хвост журнала,
// который сервер отдаёт сразу, и всплывать ему незачем; на открытой ленте
// событие и так дописывается строкой, второй раз его показывать не надо.
function flashWorthy(n, since, onFeed) {
  return Boolean(n && n.time) && n.time > since && !onFeed;
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
  head.append(el("h2", "", "Лента"));
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
    "Лента машинная: в ней события всех досок сразу, а «Поднять виток» поднимает работу в проекте " +
    project + "."));

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
    row.addEventListener("click", () => { location.hash = p.name; });
    card.append(row);
  }
  groups.append(card);
  const quota = el("div", "card qcard squota");
  quota.id = "quota-card";
  groups.append(quota);
  paintQuota();
  if (!shownProject) return;
  const bar = el("div", "nbar");
  bar.append(newTaskButton(shownProject, "Новая задача"), draftsButton(shownProject));
  groups.append(bar);
}

// Остаток подписок (макет «00 Главная», блок в подвале боковой колонки). Имён
// харнесов тут нет ни одного: что показывать, целиком решает ответ сервера, а
// он собран из каталога снимков. На ноутбуке блок стоит в колонке над кнопкой
// выхода, на телефоне колонки нет вовсе, и то же самое едет карточкой на
// главную: остаток нужен как раз с телефона, чтобы понять, пора ли притормозить.
let quotaView = null;

// quotaWhen сжимает момент сброса до дня и месяца: в колонке шириной с ладонь
// год и минуты места не стоят, а полный момент остаётся подсказкой.
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
    const parts = [];
    if (h.age) parts.push("снимок " + h.age + " назад");
    if (h.stale) parts.push(h.note || "протух");
    else if (h.note) parts.push(h.note);
    for (const w of h.warns || []) parts.push(w);
    out.push(el("div", "qnote" + (h.stale ? " stale" : ""), parts.join(", ")));
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
  groups.replaceChildren();
  const card = el("div", "card");
  card.append(el("div", "error", text));
  groups.append(card);
}

// Проект, который сейчас на экране: по нему разделы боковой колонки строят
// свой переход, когда в хэше проекта ещё нет.
let shownProject = "";

async function refresh() {
  closeAgentLive();
  const { body } = await api("/api/projects");
  const projects = body.projects || [];
  const current = currentProject(projects);
  const rt = route();
  // Проект помнится и на главной: с неё раздел «Доска» ведёт на тот проект,
  // который откроется по имени, а не на пустой хэш.
  shownProject = current ? current.name : "";
  // Точка на колокольчике живёт отдельно от экрана: она нужна и на доске, и на
  // главной, а ждать её ответа экрану незачем.
  refreshBellDot().catch(console.error);
  // Остаток подписок тоже живёт отдельно от экрана: он стоит над любым из них,
  // а держать экран ради чтения пары файлов незачем.
  refreshQuota().catch(console.error);
  renderSidebar(projects, rt.home ? null : current);
  document.getElementById("brand-note").textContent =
    projects.length + " " + plural(projects.length, "проект", "проекта", "проектов");
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
  const r = await api("/api/projects/" + encodeURIComponent(current.name) + "/board");
  if (!r.ok) {
    document.getElementById("psub").textContent = "";
    showError(r.body.error || ("доска не прочиталась (" + r.status + ")"));
    return;
  }
  const board = r.body.board || {};
  renderLive(current.name, r.body.works);
  markNav(rt);
  if (rt.feed) {
    document.getElementById("psub").textContent = "лента уведомлений";
    renderFeed(current.name);
    return;
  }
  if (rt.id && rt.agent) {
    document.getElementById("psub").textContent = "живой статус " + rt.id;
    renderAgent(current.name, r.body.works, rt.id);
    return;
  }
  if (rt.id && rt.chat) {
    document.getElementById("psub").textContent = "чат с агентом " + rt.id;
    renderChat(current.name, r.body.works, rt.id);
    return;
  }
  if (rt.id) {
    document.getElementById("psub").textContent = rt.id;
    await renderTask(current.name, r.body.works, rt.id);
    return;
  }
  document.getElementById("psub").textContent =
    "доска docs/TASKS.md" + (board.prefix ? ", " + board.prefix : "");
  renderBoard(current.name, board, r.body.works);
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
  const on = rt.home ? "home" : rt.feed ? "feed" : "board";
  for (const [name, ids] of [["home", ["nav-home", "tab-home"]],
    ["board", ["nav-board", "tab-board"]],
    ["feed", ["bell"]]]) {
    for (const id of ids) {
      document.getElementById(id).classList.toggle("on", name === on);
    }
  }
}

for (const [id, tail] of [["nav-board", ""], ["tab-board", ""],
  ["bell", "/feed"]]) {
  document.getElementById(id).addEventListener("click", () => {
    // Имя проекта берётся то, что показано: на главной хэш пуст, и раздел без
    // имени увёл бы на "#/feed".
    location.hash = (shownProject || route().proj) + tail;
  });
}

// Переход на главную это логотип в левом верхнем углу: на ноутбуке он стоит
// вверху боковой колонки, на телефоне слева в шапке, кнопки «На главную» нет
// нигде. На телефоне то же место занимает и первая нижняя вкладка.
for (const id of ["logo-side", "logo-top", "nav-home", "tab-home"]) {
  document.getElementById(id).addEventListener("click", () => {
    // Пустой хэш это главная. Пустая строка оставила бы в адресе прежний "#x",
    // поэтому решётка ставится явно.
    location.hash = "#";
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
  sayResult("");
  refresh().catch(console.error);
});
// Доска перечитывается по фокусу окна, как решил LLD: событийного источника
// у неё нет, а постоянный опрос ест батарею телефона.
window.addEventListener("focus", () => { refresh().catch(console.error); });
// Блок квоты рисуется до первого ответа сервера: пустая рамка в подвале
// колонки читалась бы как «подписок нет».
paintQuota();
wireFlash();
refresh().catch(console.error);
