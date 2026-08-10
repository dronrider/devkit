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

// Хэш это экран: "#проект" доска, "#проект/DK-NNN" задача,
// "#проект/agent/DK-NNN" живой статус агента, "#проект/chat/DK-NNN"
// переписка с агентом цели.
function route() {
  const h = decodeURIComponent(location.hash.replace(/^#/, ""));
  const parts = h.split("/");
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
  groups.append(act);
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

// Транскрипт: свежая сессия проекта, живое дострение через SSE, пагинация
// назад кнопкой «раньше» через ?before=.
async function wireTranscript(project, tp) {
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/sessions");
  if (!r.ok) {
    say(tp.body, "error", r.body.error || "сессии не прочитались");
    return;
  }
  const list = r.body.sessions || [];
  if (!list.length) {
    say(tp.body, "empty", r.body.note || "транскриптов нет");
    return;
  }
  const s = list[0];
  tp.sub.textContent = s.id.slice(0, 8) + ".jsonl" + (s.branch ? ", " + s.branch : "");
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
  wireTranscript(project, tp).catch(console.error);
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
// инструментов и размышлений, дострение через SSE. Пустоты различимы:
// «цель не гонялась» (сессий нет) и «в транскрипте нет реплик» это разные
// слова.
async function wireChatFeed(project, feed) {
  const r = await api("/api/projects/" + encodeURIComponent(project) + "/sessions");
  if (!r.ok) {
    say(feed, "error", r.body.error || "сессии не прочитались");
    return;
  }
  const list = r.body.sessions || [];
  if (!list.length) {
    say(feed, "empty", "цель не гонялась: " + (r.body.note || "транскриптов сессий нет"));
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

  wireChatFeed(project, feed).catch(console.error);
  loadPending(project, id, pendbox).catch(console.error);
}

function showError(text) {
  const groups = document.getElementById("groups");
  groups.replaceChildren();
  const card = el("div", "card");
  card.append(el("div", "error", text));
  groups.append(card);
}

async function refresh() {
  closeAgentLive();
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
  const rt = route();
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
    renderTask(current.name, board, r.body.works, rt.id);
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
