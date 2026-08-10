// Экран доски: список проектов, живые работы и секции со строками. Клиент
// только рисует готовый JSON (решение LLD DK-112); все тексты вставляются
// через textContent, HTML из данных не собирается.

const SECTION_ORDER = ["in-progress", "check", "backlog", "blocked"];

function el(tag, cls, text) {
  const node = document.createElement(tag);
  if (cls) node.className = cls;
  if (text !== undefined) node.textContent = text;
  return node;
}

async function api(path) {
  const resp = await fetch(path);
  if (resp.status === 401) {
    location.href = "/login";
    throw new Error("нужен вход");
  }
  return { ok: resp.ok, status: resp.status, body: await resp.json() };
}

function currentProject(projects) {
  const want = decodeURIComponent(location.hash.replace(/^#/, ""));
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

function renderLive(works) {
  const live = document.getElementById("live");
  live.replaceChildren();
  for (const w of works || []) {
    const card = el("div", "lcard");
    card.append(el("span", "dot pulse"));
    card.append(el("b", "", (w.kind === "goal" ? "goal-" : "") + w.id));
    card.append(el("span", "via", w.via === "tmux" ? "tmux" : "ведётся снаружи"));
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

function renderRow(row) {
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
  return tr;
}

function renderBoard(board) {
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
    for (const row of sec.rows) card.append(renderRow(row));
    groups.append(card);
  }
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
  renderLive(current.works);
  const r = await api("/api/projects/" + encodeURIComponent(current.name) + "/board");
  if (!r.ok) {
    document.getElementById("psub").textContent = "";
    showError(r.body.error || ("доска не прочиталась (" + r.status + ")"));
    return;
  }
  const board = r.body.board || {};
  document.getElementById("psub").textContent =
    "доска docs/TASKS.md" + (board.prefix ? ", " + board.prefix : "");
  renderLive(r.body.works);
  renderBoard(board);
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

window.addEventListener("hashchange", () => { refresh().catch(console.error); });
// Доска перечитывается по фокусу окна, как решил LLD: событийного источника
// у неё нет, а постоянный опрос ест батарею телефона.
window.addEventListener("focus", () => { refresh().catch(console.error); });
refresh().catch(console.error);
