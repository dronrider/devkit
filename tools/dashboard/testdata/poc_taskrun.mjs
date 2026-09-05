// Стенд кнопки работы на форме задачи (POC DK-397, ветка poc-chat).
//
// Живой случай: у задачи DK-543 на форме стоял красный «Стоп», а агент в чате
// простаивал. Работа приезжала разговором (talk) с живой tmux-сессией и
// последней репликой двухминутной давности (live=idle), а форма выбирала
// кнопку по существованию сессии, work.via === "tmux". Строка доски того же
// правила уже не держалась: она спрашивает у признаков сервера, идёт ли ход.
//
// Предмет стенда: кнопку на форме выбирает идущий ход, а не живое окно, и
// правило это одно на строку доски и на форму.
//
// Зовётся: node testdata/poc_taskrun.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, deepBtn, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// XR-1 это случай DK-543: сессия жива, ход в ней не идёт, а видна она
// разговором, и признака работы у строки нет вовсе.
// XR-2 та же простаивающая сессия, но своя и не разговорная: признак работы у
// строки стоит, а хода в ней нет.
// XR-3 идущий ход нашей сессией: вот у него «Стоп» и есть.
const rows = [
  { id: "XR-1", title: "разговор с простаивающей сессией", sect: "backlog", type: "task",
    cost: "S", r: 20, r_parts: [8, 4, 4, 2, 2], moved: "2026-08-24" },
  { id: "XR-2", title: "своя сессия без хода", sect: "backlog", type: "task",
    cost: "S", r: 20, r_parts: [8, 4, 4, 2, 2], moved: "2026-08-24", run: "tmux" },
  { id: "XR-3", title: "ход идёт нашей сессией", sect: "in-progress", type: "task",
    cost: "S", r: 20, r_parts: [8, 4, 4, 2, 2], moved: "2026-08-24",
    run: "tmux", run_busy: true },
];

const works = [
  { id: "XR-1", kind: "task", via: "tmux", own: true, tmux: "task-XR-1", talk: true,
    live: "idle", title: "разговор", started: 3000, moved: 9000 },
  { id: "XR-2", kind: "task", via: "tmux", own: true, tmux: "task-XR-2", live: "idle",
    title: "своя сессия", started: 3000, moved: 9000 },
  { id: "XR-3", kind: "task", via: "tmux", own: true, tmux: "task-XR-3", live: "busy",
    title: "работа", started: 3000, moved: 9000 },
];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path.includes("/tasks/") && (!init || !init.method)) {
    const id = decodeURIComponent(path.slice(path.lastIndexOf("/") + 1));
    const row = rows.find((r) => r.id === id);
    if (!row) return { error: "нет строки" };
    return { project: "demo", id, row, after: [], blocks: [],
      file: "docs/tasks/" + id + ".md", text: "# " + id + "\n\nПостановка.\n" };
  }
  if (path.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [
      { key: "backlog", title: "Backlog", rows: rows.filter((r) => r.sect === "backlog") },
      { key: "in-progress", title: "In progress",
        rows: rows.filter((r) => r.sect === "in-progress") },
    ] }, works };
  }
  if (path.endsWith("/works")) return { works };
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", tiers: ["pro"] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [], buckets: [] };
  if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
  return {};
});

const groups = byId.get("groups");
await settle();

// Действия формы стоят в командной панели: полоса под статусом их больше не
// держит, и искать их надо там, где они рисуются.
const formActs = async (id) => {
  await sandbox.renderTask("demo", works, id);
  await settle();
  const acts = byClass(groups, "tacts");
  if (!acts) fail("на форме " + id + " нет места под действия: " + dump(groups).slice(0, 300));
  return acts;
};

// --- простаивающая сессия: на форме пуск, а не «Стоп» ---
for (const [id, word] of [["XR-1", "разговор с простаивающей сессией"],
  ["XR-2", "своя простаивающая сессия"]]) {
  const acts = await formActs(id);
  const stop = deepBtn(acts, "rstop");
  if (stop) {
    fail(word + ": на форме стоит «Стоп», хотя ход в сессии не идёт и снимать нечего");
  }
  const run = deepBtn(acts, "Выполнить");
  if (!run) fail(word + ": на форме нет пуска: " + dump(acts));
  if (run.disabled) fail(word + ": пуск на форме погашен");
}

// --- идущий ход: на форме «Стоп», и он красный ---
{
  const acts = await formActs("XR-3");
  const stop = deepBtn(acts, "rstop");
  if (!stop) fail("у идущего хода на форме нет «Стопа»: " + dump(acts));
  if (!String(stop.className || "").split(" ").includes("btn-danger")) {
    fail("«Стоп» на форме не красный: " + stop.className);
  }
  if (stop.disabled) fail("«Стоп» на форме погашен");
  // Значком, без подписи: та же кнопка в списке задач стоит так же, задумано
  // было одинаково (приёмка 2026-09-05).
  if (dump(stop).includes("Стоп")) {
    fail("«Стоп» на форме остался с подписью рядом со значком: " + dump(stop));
  }
  if (deepBtn(acts, "Выполнить") || deepBtn(acts, "Продолжить")) {
    fail("у идущего хода на форме осталось продолжение: " + dump(acts));
  }
}

// --- правило одно: строка доски отвечает про те же задачи то же самое ---
{
  sandbox.location.hash = "#demo";
  await sandbox.refresh();
  await settle();
  const rowOf = (id) => allByClass(groups, "trow")
    .find((tr) => dump(byClass(tr, "id") || {}).includes(id)) || null;
  const mainOf = (id) => {
    const tr = rowOf(id);
    if (!tr) fail("строки " + id + " на доске нет");
    const acts = byClass(tr, "racts");
    const btn = (acts.children || []).find((k) => String(k.className || "").includes("btn"));
    if (!btn) fail("у строки " + id + " нет кнопки работы");
    return btn;
  };
  for (const id of ["XR-1", "XR-2"]) {
    if (String(mainOf(id).className || "").split(" ").includes("rstop")) {
      fail("строке " + id + " с простаивающей сессией доска предлагает «Стоп»");
    }
  }
  if (!String(mainOf("XR-3").className || "").split(" ").includes("rstop")) {
    fail("у идущего хода в строке доски не «Стоп»: " + mainOf("XR-3").className);
  }
}

console.log("poc_taskrun: ok");
