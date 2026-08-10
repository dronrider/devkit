// Экраны доски и задачи: список проектов, живые работы, секции со строками,
// запуск и стоп. Клиент только рисует готовый JSON и шлёт команды (решение
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

// Хэш это экран: "#проект" доска, "#проект/DK-NNN" задача.
function route() {
  const h = decodeURIComponent(location.hash.replace(/^#/, ""));
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
    label.addEventListener("click", () => { location.hash = project + "/" + w.id; });
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

function renderRow(project, row) {
  const tr = el("div", "trow");
  tr.append(el("span", "id", row.id));
  const tt = el("span", "tt");
  tt.append(el("span", "ttl", row.title));
  for (const chip of rowChips(row)) tt.append(chip);
  tr.append(tt);
  const meta = el("span", "meta");
  const rank = el("span", "rank");
  rank.append(el("b", "", String(row.r)));
  rank.append(document.createTextNode(" " + (row.r_parts || []).join("+")));
  meta.append(rank);
  const age = (row.notes || []).find((n) => /не двигалась/.test(n));
  if (age) meta.append(el("span", "stale", age));
  tr.append(meta);
  tr.addEventListener("click", () => { location.hash = project + "/" + row.id; });
  return tr;
}

function renderBoard(project, board) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
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
    for (const row of sec.rows) card.append(renderRow(project, row));
    groups.append(card);
  }
}

function findBoardRow(board, id) {
  for (const sec of board.sections || []) {
    for (const row of sec.rows || []) {
      if (row.id === id) return { row, section: sec.title };
    }
  }
  return null;
}

// Экран задачи по макету DK-216 («02 Задача»): шапка со строкой доски и
// карточка действия. Запуск поднимает цель оболочкой goal-run, задачу
// headless-сессией конвейера; стоп снимает tmux-сессию.
function renderTask(project, board, works, id) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();

  const crumb = el("div", "crumb");
  const back = el("span", "crumb-back", "Доска " + project);
  back.addEventListener("click", () => { location.hash = project; });
  crumb.append(back);
  const hit = findBoardRow(board, id);
  if (hit) crumb.append(el("span", "chip", hit.section));
  groups.append(crumb);

  if (!hit) {
    const card = el("div", "card");
    card.append(el("div", "error", "на доске " + project + " нет строки " + id));
    groups.append(card);
    return;
  }
  const row = hit.row;
  const head = el("div", "thead");
  head.append(el("span", "idbig", row.id));
  head.append(el("h2", "", row.title));
  groups.append(head);
  const chips = el("div", "tchips");
  for (const chip of rowChips(row)) chips.append(chip);
  const rank = el("span", "rank");
  rank.append(el("b", "", String(row.r)));
  rank.append(document.createTextNode(" " + (row.r_parts || []).join("+")));
  chips.append(rank);
  groups.append(chips);

  const isGoal = /^Цель:/.test(row.title);
  const work = (works || []).find((w) => w.id === id);
  const act = el("div", "card act");
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
  groups.append(act);
}

function showError(text) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const card = el("div", "card");
  card.append(el("div", "error", text));
  groups.append(card);
}

async function refresh() {
  const { body } = await api("/api/projects");
  const projects = body.projects || [];
  const current = currentProject(projects);
  renderSidebar(projects, current);
  document.getElementById("brand-note").textContent =
    projects.length + " " + plural(projects.length, "проект", "проекта", "проектов");
  if (!current) {
    document.getElementById("pname").textContent = "Проектов нет";
    document.getElementById("psub").textContent = "";
    showError((body.errors || []).join("; ") || "в корнях конфига не нашлось ни одной доски docs/TASKS.md");
    return;
  }
  document.getElementById("pname").textContent = current.name;
  renderLive(current.name, current.works);
  const r = await api("/api/projects/" + encodeURIComponent(current.name) + "/board");
  if (!r.ok) {
    document.getElementById("psub").textContent = "";
    showError(r.body.error || ("доска не прочиталась (" + r.status + ")"));
    return;
  }
  const board = r.body.board || {};
  renderLive(current.name, r.body.works);
  const id = route().id;
  if (id) {
    document.getElementById("psub").textContent = id;
    renderTask(current.name, board, r.body.works, id);
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

document.getElementById("logout").addEventListener("click", async () => {
  await fetch("/api/logout", { method: "POST" });
  location.href = "/login";
});

window.addEventListener("hashchange", () => { sayResult(""); refresh().catch(console.error); });
// Доска перечитывается по фокусу окна, как решил LLD: событийного источника
// у неё нет, а постоянный опрос ест батарею телефона.
window.addEventListener("focus", () => { refresh().catch(console.error); });
refresh().catch(console.error);
