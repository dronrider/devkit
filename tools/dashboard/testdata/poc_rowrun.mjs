// Стенд кнопки работы в строке доски (POC DK-397, ветка poc-chat).
//
// Живой случай: у цели DK-446 шла работа нашей сессией (own, tmux, live=busy),
// а в строке стояло «Продолжить» вместо «Стопа». Кнопку выбирал признак строки
// run, а он называет путь, которым работу узнали: у целей это реестр, и по
// этому пути идущая работа выглядела остановленной. Нажатие увело бы вводную
// продолжения в живую сессию посреди её хода.
//
// Предмет стенда: кнопку выбирает живость нашей сессии, а не род работы и не
// путь, которым её видно; продолжения у идущей работы нет вовсе.
//
// Зовётся: node testdata/poc_rowrun.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Строки одного вида, работы разного рода: задача через tmux, цель через
// реестр, груминг через транскрипт. Все три идут нашей сессией.
const rows = [
  { id: "XR-1", title: "задача в работе", sect: "in-progress", r: 40, r_parts: [10, 8, 7, 8, 7],
    moved: "2026-08-20", cost: "-", type: "task", run: "tmux" },
  { id: "XR-2", title: "Цель: долгоживущие тексты", sect: "in-progress", r: 40,
    r_parts: [10, 8, 7, 8, 7], moved: "2026-08-20", cost: "-", type: "goal", run: "registry" },
  { id: "XR-3", title: "груминг в разговоре", sect: "in-progress", r: 40,
    r_parts: [10, 8, 7, 8, 7], moved: "2026-08-20", cost: "-", type: "task", run: "session" },
  // Ход идёт, но сессия чужая: снимать нечего, и продолжать нельзя тоже.
  { id: "XR-4", title: "работа с чужой машины", sect: "in-progress", r: 40,
    r_parts: [10, 8, 7, 8, 7], moved: "2026-08-20", cost: "-", type: "task", run: "registry" },
  // Наши сессии кончились: работа стоит, и продолжение это ровно то, что надо.
  { id: "XR-5", title: "оборванный конвейер", sect: "in-progress", r: 40,
    r_parts: [10, 8, 7, 8, 7], moved: "2026-08-20", cost: "-", type: "task", run: "gone" },
];

const works = [
  { id: "XR-1", kind: "task", via: "tmux", session: "s-1", own: true, tmux: "chat-XR-1-1",
    live: "busy", title: "задача в работе", started: 3000, moved: 9000 },
  { id: "XR-2", kind: "goal", via: "registry", session: "s-2", own: true, tmux: "chat-XR-2-1",
    live: "busy", title: "цель", started: 3000, moved: 9000 },
  { id: "XR-3", kind: "task", via: "session", session: "s-3", own: true, tmux: "chat-XR-3-1",
    live: "busy", title: "груминг", started: 3000, moved: 9000 },
  { id: "XR-4", kind: "task", via: "registry", session: "s-4", own: false, live: "busy",
    title: "чужая работа", started: 3000, moved: 9000 },
];

const { sandbox, byId } = makeSandbox(app, (path_) => {
  if (path_ === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path_ === "/api/harnesses") return { harnesses: [{ name: "claude-code", tiers: ["pro"] }] };
  if (path_ === "/api/notifications") return { items: [] };
  if (path_.endsWith("/board")) {
    return { board: { prefix: "XR",
      sections: [{ key: "in-progress", title: "In progress", rows }] }, works };
  }
  if (path_.endsWith("/works")) return { works };
  if (path_.endsWith("/drafts")) return { drafts: [] };
  if (path_.includes("/chats")) return { chats: [], models: [] };
  if (path_ === "/api/quota") return { harnesses: [] };
  return {};
});

const groups = byId.get("groups");
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
  if (!btn) fail("у строки " + id + " нет главной кнопки: " + JSON.stringify(acts.children.length));
  return btn;
};
const cls = (node) => String(node.className || "").split(" ");

// --- идущая работа даёт «Стоп» независимо от рода и пути ---
{
  for (const [id, word] of [["XR-1", "задача через tmux"], ["XR-2", "цель через реестр"],
    ["XR-3", "груминг через транскрипт"]]) {
    const btn = mainOf(id);
    if (!cls(btn).includes("rstop")) {
      fail(word + ": у идущей работы стоит не «Стоп», а " + btn.className +
        " с подписью " + JSON.stringify(btn.attrs["aria-label"]));
    }
    if (!cls(btn).includes("btn-danger")) {
      fail(word + ": кнопка стопа не красная: " + btn.className);
    }
    if (btn.disabled) fail(word + ": кнопка стопа погашена");
  }
}

// --- продолжения у идущей работы нет вовсе ---
{
  for (const id of ["XR-1", "XR-2", "XR-3"]) {
    const tr = rowOf(id);
    const said = dump(byClass(tr, "racts") || {});
    const labels = (byClass(tr, "racts").children || [])
      .map((k) => String((k.attrs || {})["aria-label"] || ""));
    if (labels.some((l) => l.includes("Продолжить"))) {
      fail("у идущей работы " + id + " осталось продолжение: " + JSON.stringify(labels) +
        " " + said);
    }
  }
}

// --- чужой идущей работе продолжение погашено с причиной ---
{
  const btn = mainOf("XR-4");
  if (cls(btn).includes("rstop")) fail("чужую сессию предлагают снять со строки доски");
  if (!btn.disabled) {
    fail("продолжение чужой идущей работы доступно: вводная уедет в живой ход");
  }
  const tip = String((btn.attrs || {}).title || btn.title || "");
  if (!tip.includes("ход")) fail("причина погашенной кнопки не названа: " + tip);
}

// --- работа кончилась: продолжение на месте и доступно ---
{
  const btn = mainOf("XR-5");
  if (cls(btn).includes("rstop")) fail("остановленной работе предлагают стоп");
  if (btn.disabled) fail("продолжение оборванного конвейера погашено");
  if (!String((btn.attrs || {})["aria-label"] || "").includes("Продолжить")) {
    fail("у оборванного конвейера не продолжение, а " + btn.attrs["aria-label"]);
  }
}

console.log("poc_rowrun: ok");
