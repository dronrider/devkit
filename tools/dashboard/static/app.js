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

function renderSidebar(projects, current) {
  const nav = document.getElementById("projects");
  const sel = document.getElementById("pselect");
  nav.replaceChildren();
  sel.replaceChildren();
  for (const p of projects) {
    const item = el("div", "sitem" + (current && p.name === current.name ? " on" : ""));
    if (p.works && p.works.length) {
      item.append(el("span", "dot pulse"));
    }
    item.append(document.createTextNode(p.name));
    const n = el("span", "n", p.works && p.works.length ? p.works.length + " в работе" : "тихо");
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
    const name = (w.kind === "goal" ? "goal-" : "") + w.id;
    const label = el("b", "", name);
    label.addEventListener("click", () => { location.hash = project + "/agent/" + w.id; });
    card.append(label);
    if (w.via === "tmux") {
      const stop = el("button", "btn btn-sm", "Стоп");
      stop.addEventListener("click", () => { stopRun(project, w.id).catch(console.error); });
      card.append(stop);
    } else {
      card.append(el("span", "via", "ведётся снаружи"));
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
  if (row.after && row.after.length) chips.push(el("span", "chip", "после " + row.after.join(", ")));
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

// Действие прямо со строки: взять в работу или снять живую сессию, не заходя
// внутрь задачи. Ручки те же, что у экрана задачи (POST и DELETE runs), и
// ответ выходит в ту же строку результата.
function rowAction(project, row, works) {
  const work = (works || []).find((w) => w.id === row.id);
  if (work && work.via !== "tmux") {
    return el("span", "stale", "ведётся снаружи");
  }
  const btn = el("button", "btn btn-sm" + (work ? "" : " btn-acc"), work ? "Стоп" : "В работу");
  btn.addEventListener("click", (ev) => {
    ev.stopPropagation();
    const call = work ? stopRun(project, row.id) : startRun(project, row.id);
    call.catch(console.error);
  });
  return btn;
}

function renderRow(project, row, works) {
  const tr = el("div", "trow");
  tr.append(el("span", "id", row.id));
  const tt = el("span", "tt");
  tt.append(el("span", "ttl", row.title));
  for (const chip of rowChips(row)) tt.append(chip);
  tr.append(tt);
  const meta = el("span", "meta");
  meta.append(rankCell(row));
  // Дата последней правки строки вместо возраста днями: считает её taskctl по
  // git blame, клиент только показывает.
  if (row.moved) {
    const moved = el("span", "stale", "правка " + row.moved);
    moved.title = "дата последней правки строки на доске: перевод в статус двигает её же";
    meta.append(moved);
  }
  meta.append(rowAction(project, row, works));
  tr.append(meta);
  tr.addEventListener("click", () => { location.hash = project + "/" + row.id; });
  return tr;
}

function renderBoard(project, board, works) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const bar = el("div", "nbar");
  bar.append(newTaskButton(project, "Новая задача"));
  bar.append(el("span", "hint",
    "По умолчанию черновик: метаданные ему выдаст грумминг, а полная строка требует ранга."));
  groups.append(bar);
  const byKey = {};
  for (const sec of board.sections || []) byKey[sec.key] = sec;
  for (const key of SECTION_ORDER) {
    const sec = byKey[key];
    if (!sec) continue;
    const head = el("div", "shead", sec.title);
    head.append(el("span", "n", String(sec.rows.length)));
    groups.append(head);
    const card = el("div", "card");
    if (!sec.rows.length) {
      card.append(el("div", "empty", "Нет."));
    }
    for (const row of sec.rows) card.append(renderRow(project, row, works));
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

// Бакет P рукой не выбирается, и экран обязан это объяснять: иначе
// отсутствие списка читается как забытое поле.
const P_HINT = "Бакет P не выбирается рукой: он считается из суммы R по RANKING.md, " +
  "поэтому правятся слагаемые, а P и место строки в Backlog выводит taskctl.";

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
    // «Держит» это та же зависимость с другой стороны: снимается она у той
    // строки, в чьём заголовке стоит маркер [после ...].
    const call = side === "after" ? dropDep(project, id, dep.id) : dropDep(project, dep.id, id);
    call.catch(console.error);
  });
  row.append(drop);
  row.addEventListener("click", () => { location.hash = project + "/" + dep.id; });
  return row;
}

// Карточка зависимостей в обе стороны по макету «02 Задача»: кого ждёт строка
// и кто ждёт её. Обе стороны живут на доске одним маркером [после ...],
// поэтому «держит» это обратный поиск, а не вторая запись.
function depsCard(project, id, after, blocks) {
  const card = el("div", "card");
  card.append(el("div", "dhead", "После, ждёт их"));
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

  card.append(el("div", "dhead", "Держит, ждут её"));
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

// Пометка «строка обновилась»: живое обновление при открытой правке молчаливо
// подменяло значения полей, и это признано дефектом на пользовательской
// проверке. Свежие данные ждут кнопки, ввод остаётся на месте.
function taskStale(project, works, id) {
  if (document.getElementById("tstale")) return;
  const box = el("div", "tstale");
  box.id = "tstale";
  box.append(el("span", "", "строка обновилась, перечитать"));
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
  if (!form.title.trim()) return "заголовок строки пустым не бывает";
  return "";
}

// Экран задачи по макету DK-216 («02 Задача»): шапка со строкой доски,
// карточка действия, ранг со слагаемыми и зависимости в обе стороны. Строку
// правит taskctl на стороне сервера, поэтому правится ровно то, что есть в
// строке: заголовок, тип, слагаемые ранга и цена; порядок строк выводится из
// ранга, перетаскивания мимо ранга нет.
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
    card.append(el("div", "error", r.body.error || "строка не прочиталась"));
    groups.append(card);
    return;
  }
  const detail = r.body;
  const row = detail.row || {};
  if (row.section) crumb.append(el("span", "chip", row.section));
  if (row.moved) crumb.append(el("span", "stale", "правка строки " + row.moved));

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
  title.setAttribute("aria-label", "заголовок строки " + id);
  title.addEventListener("input", () => { form.title = title.value; touch(); });
  head.append(title);
  groups.append(head);

  const chips = el("div", "tchips");
  if (/^Цель:/.test(row.title)) chips.append(el("span", "chip c-goal", "цель"));
  chips.append(pickField("тип", TYPE_VALUES, form.type, (v) => { form.type = v; touch(); }));
  chips.append(pickField("цена", COST_VALUES, form.cost, (v) => { form.cost = v; touch(); }));
  if (row.p === "P0" || row.p === "P1") chips.append(el("span", "chip c-p1", row.p));
  else chips.append(el("span", "chip", row.p));
  if (row.fail) chips.append(el("span", "chip c-block", "провал: " + row.fail));
  if (row.block) chips.append(el("span", "chip c-block", "блок: " + row.block));
  for (const note of row.notes || []) {
    if (/^код слит/.test(note) || /^без выката/.test(note)) chips.append(el("span", "chip c-check", note));
  }
  groups.append(chips);
  groups.append(el("div", "hint phint", P_HINT));

  // Одна кнопка на всю форму: любое изменение поля включает её, и по ней
  // уезжает всё изменённое разом. Двух правок, у заголовка и у файла, тут
  // больше нет, с телефона это разваливало правку на два похода.
  const bar = el("div", "card tsave");
  const btns = el("div", "tbtns");
  const save = el("button", "btn btn-acc", "Сохранить");
  const drop = el("button", "btn", "Отменить правку");
  btns.append(save, drop);
  const note = el("div", "hint", "");
  const bad = el("div", "error", "");
  bar.append(btns, note, bad);
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
    note.textContent = dirty
      ? "Изменённое уедет одной кнопкой: строка через taskctl, файл задачи целиком."
      : "Правки нет: кнопка включится, как только поменяется поле.";
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

  const isGoal = /^Цель:/.test(row.title);
  const work = (works || []).find((w) => w.id === id);
  const act = el("div", "card act");
  if (work) {
    const live = el("button", "btn", "Живой статус");
    live.addEventListener("click", () => { location.hash = project + "/agent/" + id; });
    act.append(live);
  }
  if (isGoal) {
    const chat = el("button", "btn", "Переписка");
    chat.addEventListener("click", () => { location.hash = project + "/chat/" + id; });
    act.append(chat);
  }
  if (work && work.via === "tmux") {
    const stop = el("button", "btn", "Стоп");
    stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
    act.append(stop);
    act.append(el("div", "hint", "Идёт tmux-сессия " + work.kind + "-" + id +
      ". Стоп это стоп сессии; возобновление это новый запуск, читающий состояние с диска."));
  } else if (work) {
    act.append(el("div", "hint", "Цикл ведётся снаружи (живой чат), без tmux-сессии дашборда: стоп там, где он поднят."));
  } else {
    const start = el("button", "btn btn-acc", "В работу");
    start.addEventListener("click", () => { startRun(project, id).catch(console.error); });
    act.append(start);
    act.append(el("div", "hint", isGoal
      ? "Цель поднимет оболочка goal-run в tmux-сессии goal-" + id + "."
      : "Задачу поднимет headless-сессия конвейера доски в tmux-сессии task-" + id + "."));
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
  rank.append(el("div", "rnote",
    "Порядок в Backlog выводится из ранга: правятся слагаемые, перетаскивания мимо ранга нет."));

  const rail = el("div", "rrail");
  rail.append(act, rank, depsCard(project, id, detail.after || [], detail.blocks || []));
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
  return { card, sub: subEl, body };
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
function wireJournal(project, id, body) {
  body.classList.add("log");
  const es = new EventSource("/api/projects/" + encodeURIComponent(project) +
    "/goals/" + encodeURIComponent(id) + "/log?stream=1");
  agentLive.push(() => es.close());
  es.addEventListener("note", (ev) => { say(body, "empty", ev.data); });
  es.onmessage = (ev) => {
    if (body.firstChild && body.firstChild.className === "empty") body.replaceChildren();
    body.append(logLine(ev.data));
    body.scrollTop = body.scrollHeight;
  };
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
  const when = item.time ? ", " + item.time.slice(11, 16) : "";
  turn.append(el("div", "th", (item.role === "user" ? "человек" : "агент") + when));
  turn.append(el("div", "tb", item.text || ""));
  return turn;
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
    for (const item of (older.body.items || []).reverse()) {
      feed.prepend(replyEl(item));
      firstSeq = item.seq;
    }
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
    if (feed.firstChild && feed.firstChild.className === "empty") feed.replaceChildren();
    if (firstSeq === null) {
      firstSeq = item.seq;
      updateMore();
    }
    feed.append(replyEl(item));
    tp.body.scrollTop = tp.body.scrollHeight;
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
    row.append(el("span", "chip c-check", "жива"));
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
  head.append(el("h2", "", (work ? work.kind + "-" : "") + id));
  if (work && work.via === "tmux") {
    head.append(el("span", "chip c-check", "tmux-сессия жива"));
    const stop = el("button", "btn", "Стоп");
    stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
    head.append(stop);
  } else if (work) {
    head.append(el("span", "chip", "ведётся снаружи"));
  } else {
    head.append(el("span", "chip", "работа не идёт"));
  }
  if (!work || work.kind === "goal") {
    const chat = el("button", "btn", "Переписка");
    chat.addEventListener("click", () => { location.hash = project + "/chat/" + id; });
    head.append(chat);
  }
  groups.append(head);

  const jp = pane("Журнал цикла", ".devkit/goal-" + id + ".log");
  const tp = pane("Транскрипт", "");
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
  ["Журнал", "Транскрипт", "tmux"].forEach((name, i) => {
    const d = el("div", i === 0 ? "on" : "", name);
    d.addEventListener("click", () => {
      Array.from(seg.children).forEach((x, j) => { x.className = j === i ? "on" : ""; });
      tabs.forEach((p, j) => p.classList.toggle("onpane", j === i));
    });
    seg.append(d);
  });
  tabs[0].classList.add("onpane");

  groups.append(seg, grid, tm);
  wireJournal(project, id, jp.body);
  wireTranscript(project, tp, id).catch(console.error);
  wireTmux(id, tm, tmSub);
}

// Экран переписки по макету DK-216 («04 Переписка»). Ход и ответы читаются
// из транскрипта свежей сессии проекта (API DK-219), а сообщение человека
// уходит в раздел «Входящие» файла цели: писать в идущий процесс механики
// нет, сообщение прочитает следующий виток, и надпись говорит это прямо.
function dayEl(date) {
  const day = el("div", "day");
  day.append(el("i"), document.createTextNode(date), el("i"));
  return day;
}

function chatBubble(who, text, meta) {
  const wrap = el("div", "msg" + (who === "вы" ? " me" : ""));
  wrap.append(el("div", "bb", text));
  wrap.append(el("div", "mm", who + ", " + meta));
  return wrap;
}

// Лента переписки: текстовые реплики человека и агента, без свёрнутых
// инструментов и размышлений, дострение через SSE. Сессия берётся узнанная
// этой целью (?task=), чужая в переписку не попадает (DK-252). Пустоты
// различимы: «сессий этой цели нет», «транскриптов нет вовсе» и «в
// транскрипте нет реплик» это разные слова.
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
  let lastSeq = -1;
  let lastDay = "";
  const append = (item) => {
    if ((item.role !== "user" && item.role !== "assistant") || !item.text) return false;
    const day = (item.time || "").slice(0, 10);
    if (day && day !== lastDay) {
      feed.append(dayEl(day));
      lastDay = day;
    }
    const when = item.time ? item.time.slice(11, 16) + ", " : "";
    feed.append(chatBubble(item.role === "user" ? "вы" : "агент", item.text, when + "из транскрипта"));
    return true;
  };
  const first = await api("/api/projects/" + encodeURIComponent(project) +
    "/sessions/" + encodeURIComponent(sid) + "?n=500");
  if (first.ok) {
    for (const item of first.body.items || []) {
      lastSeq = item.seq;
      append(item);
    }
  }
  if (!feed.childElementCount) {
    say(feed, "empty", (first.ok && first.body.note) ||
      "переписки пока нет: в транскрипте нет текстовых реплик");
  }
  feed.scrollTop = feed.scrollHeight;
  const es = new EventSource("/api/projects/" + encodeURIComponent(project) +
    "/sessions/" + encodeURIComponent(sid) + "?stream=1");
  agentLive.push(() => es.close());
  es.onmessage = (ev) => {
    const item = JSON.parse(ev.data);
    if (item.seq <= lastSeq) return;
    lastSeq = item.seq;
    if (feed.firstChild && feed.firstChild.className === "empty") {
      feed.replaceChildren();
      lastDay = "";
    }
    if (append(item)) feed.scrollTop = feed.scrollHeight;
  };
}

// Лежащие во «Входящих» строки: сообщение отправлено, но виток его ещё не
// подхватил, и это честно называется ожиданием. Пустой раздел тоже говорит
// словами: пустая коробка неотличима от неотрисованной.
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
    box.append(chatBubble("вы", line, "ждёт витка: лежит во «Входящих» файла цели"));
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
    head.append(el("span", "chip c-run", "цикл идёт"));
  } else if (work) {
    head.append(el("span", "chip", "ведётся снаружи"));
  } else {
    head.append(el("span", "chip", "цикл не идёт"));
  }
  groups.append(head);

  const thread = el("div", "chatwrap");
  const feed = el("div", "msgs");
  const pendbox = el("div", "msgs");
  thread.append(feed, pendbox);

  const note = el("div", "cnote");
  note.append(el("b", "", "Следующему витку."));
  note.append(document.createTextNode(
    " Сообщение ляжет в файл цели и уйдёт следующему витку, идущий виток его не увидит."));
  thread.append(note);

  const box = el("div", "cbox");
  const ta = el("textarea");
  ta.placeholder = "Написать следующему витку...";
  const row = el("div", "crow");
  if (work && work.via === "tmux") {
    const stop = el("button", "btn", "Стоп цикла");
    stop.addEventListener("click", () => { stopRun(project, id).catch(console.error); });
    row.append(stop);
  }
  const send = el("button", "btn btn-acc", "Отправить");
  send.addEventListener("click", () => { sendMessage(project, id, ta, pendbox).catch(console.error); });
  row.append(send);
  box.append(ta, row);
  thread.append(box);
  thread.append(el("div", "stopnote",
    "Стоп цикла это стоп сессии текущего витка; возобновление это новый запуск, " +
    "и следующий виток прочтёт доску, файл цели и сообщение с диска."));
  groups.append(thread);

  wireChatFeed(project, feed, id).catch(console.error);
  loadPending(project, id, pendbox).catch(console.error);
}

// Экран заведения (#проект/new). Черновик это путь по умолчанию: с телефона
// мысль приходит целиком, а ранг там считать нечем, и грумминг всё равно
// разберёт запись позже (taskctl add --id). Полная строка лежит под
// разворотом: ей нужны заголовок, тип, цена и все пять слагаемых ранга.
const DRAFT_HINT = "Черновик ляжет в docs/tasks/drafts/: метаданных у него нет, " +
  "ID выдаёт taskctl, а ранг и тип выдаст грумминг накопителя.";
const FULL_HINT = "Полная строка встаёт в Backlog сразу: место в нём выводится из ранга, " +
  "и все пять слагаемых обязательны.";

// Поля формы переживают перерисовку: доска перечитывается по фокусу окна, и
// без этого набранный текст пропадал бы при первом же переключении на другое
// окно. Форма одна на экран, как и черновик экрана задачи.
const newForm = { project: "", text: "", full: false, title: "", type: "task", cost: "-", parts: [0, 0, 0, 0, 0], file: true };

function resetNewForm(project) {
  newForm.project = project;
  newForm.text = "";
  newForm.full = false;
  newForm.title = "";
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
  sayResult("заведение строки...");
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
    ? "Файл " + done.file + " лежит в накопителе, на доске строки у него нет."
    : "Файл лежит в накопителе, на доске строки у него нет."));
  box.append(el("div", "hint",
    "Разберёт его грумминг: он выдаст ранг с типом и заведёт строку (taskctl add --id)."));
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
  card.append(el("div", "nfhead", "Новая задача в " + project));
  const box = el("div", "nfbody");
  const ta = el("textarea");
  ta.value = newForm.text;
  ta.placeholder = "Мысль с телефона...";
  ta.setAttribute("aria-label", "текст черновика");
  const bad = el("div", "error", "");
  const btns = el("div", "tbtns");
  const send = el("button", "btn btn-acc", "Записать черновик");
  const more = el("button", "btn", newForm.full ? "Свернуть полную строку" : "Полная строка");
  btns.append(send, more);
  box.append(ta, el("div", "hint", DRAFT_HINT), btns, bad);
  card.append(box);
  groups.append(card);

  const full = el("div", "card nform");
  full.hidden = !newForm.full;
  groups.append(full);
  full.append(el("div", "nfhead", "Полная строка"));
  const fbox = el("div", "nfbody");
  full.append(fbox);

  const title = el("input");
  title.value = newForm.title;
  title.placeholder = "Заголовок строки";
  title.setAttribute("aria-label", "заголовок строки");
  fbox.append(title);

  const chips = el("div", "tchips");
  chips.append(pickField("тип", TYPE_VALUES, newForm.type, (v) => { newForm.type = v; touch(); }));
  chips.append(pickField("цена", COST_VALUES, newForm.cost, (v) => { newForm.cost = v; touch(); }));
  fbox.append(chips);

  const sum = el("div", "rbig");
  const sumv = el("span", "v", "0");
  const sumf = el("span", "f", "");
  sum.append(sumv, sumf);
  fbox.append(sum);
  RANK_PARTS.forEach((part, i) => {
    const line = el("div", "rrow");
    line.append(el("span", "nm", part.name));
    line.append(el("span", "why", part.why));
    line.append(pickField("", part.values, newForm.parts[i], (v) => {
      newForm.parts[i] = Number(v);
      touch();
    }));
    fbox.append(line);
  });
  fbox.append(el("div", "hint", P_HINT));

  const withFile = el("label", "nfcheck");
  const flag = el("input");
  flag.type = "checkbox";
  flag.checked = newForm.file;
  flag.addEventListener("change", () => { newForm.file = flag.checked; });
  withFile.append(flag, el("span", "", "завести файл задачи по шаблону (taskctl file)"));
  fbox.append(withFile);

  const fbad = el("div", "error", "");
  const fbtns = el("div", "tbtns");
  const make = el("button", "btn btn-acc", "Завести строку");
  fbtns.append(make);
  fbox.append(el("div", "hint", FULL_HINT), fbtns, fbad);

  const all = [send, more, make];
  // Рубежи те же, что у ручек: поправка на баг не про новую работу, а строки
  // без заголовка и черновика без текста не бывает.
  const touch = () => {
    newForm.text = ta.value;
    newForm.title = title.value;
    const parts = newForm.parts;
    sumv.textContent = String(parts.reduce((a, b) => a + Number(b), 0));
    sumf.textContent = "= " + parts.join("+");
    send.disabled = !newForm.text.trim();
    fbad.textContent = newForm.type === "task" && Number(parts[3]) === 5 ? BUG_PART_REFUSAL
      : !newForm.title.trim() ? "заголовок строки пустым не бывает" : "";
    make.disabled = Boolean(fbad.textContent);
    bad.textContent = "";
  };
  ta.addEventListener("input", touch);
  title.addEventListener("input", touch);
  more.addEventListener("click", () => {
    newForm.full = !newForm.full;
    full.hidden = !newForm.full;
    more.textContent = newForm.full ? "Свернуть полную строку" : "Полная строка";
  });
  send.addEventListener("click", () => {
    const text = ta.value.trim();
    if (!text) return;
    makeDraft(project, text, all).then((done) => {
      if (done) {
        resetNewForm(project);
        draftDone(project, done);
      }
    }).catch(console.error);
  });
  make.addEventListener("click", () => {
    if (fbad.textContent) return;
    const body = {
      title: newForm.title.trim(),
      type: newForm.type,
      cost: newForm.cost,
      r_parts: newForm.parts.map(Number),
      file: newForm.file,
    };
    makeTask(project, body, all).then((done) => {
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
  { kind: "stop", name: "Стопы" },
  { kind: "wait", name: "wait-human" },
  { kind: "task", name: "Задачи" },
];
const FEED_ICONS = { stop: "i-stop", wait: "i-wait", task: "i-done" };
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
  const ico = el("div", "nico " + (FEED_ICONS[n.kind] || "i-other"));
  ico.append(el("i"));
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
      const jrn = el("a", "", "Журнал цикла");
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

function renderFeed(project) {
  // Заход на ленту гасит точку: всё, что было до этой минуты, человек видит
  // прямо сейчас.
  markFeedSeen(nowStamp());
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const head = el("div", "nhead");
  head.append(el("h2", "", "Лента"));
  head.append(el("span", "sub", "уведомления машины: стопы работ, зов человека, завершённые задачи"));
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
function renderHome(projects) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const card = el("div", "card");
  if (!projects.length) card.append(el("div", "empty", "Проектов нет."));
  for (const p of projects) {
    const row = el("div", "prow");
    if (p.works && p.works.length) row.append(el("span", "dot pulse"));
    row.append(el("b", "", p.name));
    row.append(el("span", "stale", p.works && p.works.length
      ? p.works.length + " " + plural(p.works.length, "работа", "работы", "работ") + " в ходу"
      : "тихо"));
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
  bar.append(newTaskButton(shownProject, "Новая задача"));
  bar.append(el("span", "hint",
    "Ляжет на доску проекта " + shownProject + ", другой выбирается строкой списка."));
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
    document.getElementById("psub").textContent = "новая задача";
    markNav(rt);
    renderNew(current.name);
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
    document.getElementById("psub").textContent = "переписка " + rt.id;
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

// Кнопка на главную: она в шапке, а шапка стоит над любым экраном, поэтому
// уход к списку проектов есть и с задачи, и с живого статуса, и с ленты. На
// телефоне то же место занимает первая нижняя вкладка.
for (const id of ["gohome", "nav-home", "tab-home"]) {
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
refresh().catch(console.error);
