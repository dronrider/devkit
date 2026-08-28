// Стенд пробуждения строки доски (POC DK-397, ветка poc-chat).
//
// Живой случай: агент встал с вопросом к человеку, человек ответил ему в чате,
// агент пошёл дальше, а «Стоп» в строке так и не появился до перезагрузки
// страницы. Строка не заметила перехода «ожидание -> ход», и заметить его ей
// было нечем: круг обновления заводила только строка, уже стоящая под
// «Стопом». Ждущий агент идёт по серверу разговором, признака работы у строки
// нет, круга нет, а завестись кругу мешал ровно тот признак, ради которого он
// и нужен.
//
// Предмет стенда: круг ходит и за ждущей строкой; вышедшая в ход работа даёт
// строке «Стоп» на ближайшем заходе круга, без перезагрузки; круг переживает
// заход, который ничего не поменял.
//
// Зовётся: node testdata/poc_busywake.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Строка ждёт ответа человека: работа за ней есть, но ходом она не идёт, и
// признака работы (run) у строки нет вовсе, как его нет у разговора.
const waitRow = {
  id: "XR-7", title: "агент спросил и ждёт", sect: "in-progress", r: 40,
  r_parts: [10, 8, 7, 8, 7], moved: "2026-08-20", cost: "-", type: "task",
  waiting: { state: "ждёт ответа", source: "ask", note: "спросил агент",
    questions: ["чинить копией или общим модулем?"], since: 1786000000,
    until: 1786003600, session: "s-7" },
};
const waitWork = {
  id: "XR-7", kind: "task", via: "tmux", session: "s-7", own: true,
  tmux: "task-XR-7", live: "waiting", talk: true, title: "агент спросил",
  started: 3000, moved: 1786000000,
};

// Человек ответил, агент пошёл дальше: сервер называет ход признаком занятости
// и признаком работы нашей сессией.
const goRow = { ...waitRow, waiting: null, run: "tmux", run_busy: true };
const goWork = { ...waitWork, live: "busy", talk: false, moved: 1786000600 };

// Соседняя строка без всякой работы: за ней круг ходить не обязан, и она же
// сторожит, что круг заводит не всякая доска подряд.
const idleRow = {
  id: "XR-8", title: "строка очереди", sect: "backlog", r: 30,
  r_parts: [10, 8, 5, 5, 2], moved: "2026-08-20", cost: "M", type: "task",
};

let moving = false;
const rowsNow = () => [moving ? goRow : waitRow];
const worksNow = () => [moving ? goWork : waitWork];
const boardNow = () => ({ prefix: "XR", sections: [
  { key: "in-progress", title: "In progress", rows: rowsNow() },
  { key: "backlog", title: "Backlog", rows: [idleRow] },
] });

const { sandbox, byId, timers } = makeSandbox(app, (path_) => {
  if (path_ === "/api/projects") {
    return { projects: [{ name: "demo", prefix: "XR", works: worksNow(),
      sections: { backlog: 1, "in-progress": 1 } }] };
  }
  if (path_ === "/api/harnesses") return { harnesses: [{ name: "claude-code", tiers: ["pro"] }] };
  if (path_ === "/api/notifications") return { items: [] };
  if (path_.endsWith("/board")) return { board: boardNow(), works: worksNow() };
  if (path_.endsWith("/works")) return { works: worksNow() };
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
  if (!btn) fail("у строки " + id + " нет главной кнопки");
  return btn;
};
const stops = (id) => String(mainOf(id).className || "").split(" ").includes("rstop");
// Круги, заведённые и ещё не отработавшие: у отработавшего стенд снимает fn.
const beats = () => timers.filter((t) => t.ms === 3000 && t.fn);

// --- ждущая строка стоит без «Стопа» ---
{
  if (stops("XR-7")) fail("у ждущей ответа строки уже стоит «Стоп»: снимать нечего");
}

// --- круг заведён и за ждущей строкой ---
// Здесь и падает старый код: признака работы у строки нет, и круг у неё не
// заводился вовсе. Ответ человека такой строке было некому заметить.
{
  if (!beats().length) {
    fail("круга обновления у ждущей строки нет: ответ человека агенту строка проспит");
  }
}

// --- человек ответил: ближайший заход круга даёт строке «Стоп» ---
{
  moving = true;
  const beat = beats().pop();
  beat.fn();
  beat.fn = null;
  await settle();
  if (!stops("XR-7")) {
    const btn = mainOf("XR-7");
    fail("вышедшая в ход работа не дала строке «Стоп»: " + btn.className +
      " с подписью " + JSON.stringify((btn.attrs || {})["aria-label"]));
  }
  if (mainOf("XR-7").disabled) fail("«Стоп» у своей идущей работы погашен");
}

// --- круг переживает заход, который ничего не поменял ---
// Круг заводился перерисовкой списка, а заход с теми же данными до неё не
// доходит (отпечаток доски): опрос жил ровно один заход и глох.
{
  const beat = beats().pop();
  if (!beat) fail("после пробуждения строки круг не завёлся заново");
  beat.fn();
  beat.fn = null;
  await settle();
  if (!beats().length) {
    fail("заход с теми же данными оборвал круг: дальше доска ждёт фокуса окна");
  }
  if (!stops("XR-7")) fail("холостой заход круга снял «Стоп» с идущей работы");
}

console.log("poc_busywake: ok");
