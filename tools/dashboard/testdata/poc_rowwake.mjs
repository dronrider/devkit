// Стенд пробуждения строки живой сессией (доработка DK-716 после провала
// приёмки).
//
// Живой случай: человек открыл чат по задаче и написал «Выполни XR-034». Через
// полминуты сессия открыла этап, стала работой строки, а в списке задач так и
// стояла кнопка «Выполнить» вместо «Стопа». Появилась она только после
// перезагрузки страницы.
//
// Замкнуто это было само на себя. Круг обновления заводили строки, за которыми
// уже видна работа, а разговор до первой команды доски строки не присваивает:
// работа у него есть, строки нет. Круг, который заметил бы присвоение, не
// заводился именно потому, что присвоения ещё не случилось.
//
// Предмет стенда: живая сессия без строки заводит круг; присвоившая строку
// работа даёт ей «Стоп» на ближайшем заходе, без перезагрузки; возврат на
// вкладку перечитывает доску (телефон гасит экран и будит его без фокуса);
// подсказка «Стопа» говорит про дожим, когда стоп уже нажат.
//
// Зовётся: node testdata/poc_rowwake.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Строка в работе, за которой дашборд не видит ничего: чат про неё уже открыт,
// но команд доски эта сессия ещё не давала.
const freeRow = {
  id: "XR-34", title: "работа заказана разговором", sect: "in-progress", r: 40,
  r_parts: [10, 8, 7, 8, 7], moved: "2026-08-20", cost: "L", type: "task",
};
// Разговор человека: сессия живая, ход идёт, а строки у неё нет вовсе. Ровно
// это и было на экране в живом случае.
const talkWork = {
  id: "", kind: "session", via: "session", session: "s-34", own: true,
  tmux: "chat-XR-34-1", live: "busy", talk: true, moved: 1786000000,
};

// Сессия открыла этап: работа присвоила строку, и строка обязана показать
// «Стоп» вместо кнопки запуска.
const takenRow = { ...freeRow, run: "chat", run_busy: true, run_state: "busy",
  run_chat: "s-34", stage: "разработка" };
const takenWork = { ...talkWork, id: "XR-34", kind: "task", talk: false,
  rows: ["XR-34"], title: "работа заказана разговором" };
// Стоп нажат, а фоновые субагенты ещё дописывают своё: строка держит «Стоп», и
// подсказка объясняет, что второго нажатия не нужно.
const stoppingRow = { ...takenRow, run_stopping: true };
const stoppingWork = { ...takenWork, stopping: true };

let phase = "free";
const rowNow = () => (phase === "free" ? freeRow : phase === "taken" ? takenRow : stoppingRow);
const workNow = () => (phase === "free" ? talkWork : phase === "taken" ? takenWork : stoppingWork);
const boardNow = () => ({ prefix: "XR", sections: [
  { key: "in-progress", title: "In progress", rows: [rowNow()] },
] });

let boardHits = 0;
const { sandbox, byId, timers } = makeSandbox(app, (path_) => {
  if (path_ === "/api/projects") {
    return { projects: [{ name: "demo", prefix: "XR", works: [workNow()],
      sections: { "in-progress": 1 } }] };
  }
  if (path_ === "/api/harnesses") return { harnesses: [{ name: "claude-code", tiers: ["pro"] }] };
  if (path_ === "/api/notifications") return { items: [] };
  if (path_.endsWith("/board")) {
    boardHits += 1;
    return { board: boardNow(), works: [workNow()] };
  }
  if (path_.endsWith("/works")) return { works: [workNow()] };
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
const beats = () => timers.filter((t) => t.ms === 3000 && t.fn);

// --- строка без работы стоит с кнопкой запуска ---
{
  if (stops("XR-34")) fail("у строки без работы уже стоит «Стоп»: снимать нечего");
}

// --- круг заведён живой сессией, у которой строки ещё нет ---
// Здесь и падает старый код: работа без ID со строкой не сводилась, круга не
// было, и присвоение строки экран пропускал целиком.
{
  if (!beats().length) {
    fail("круга обновления при живом разговоре нет: взятие строки экран проспит");
  }
}

// --- сессия взяла строку: ближайший заход круга даёт «Стоп» ---
{
  phase = "taken";
  const beat = beats().pop();
  beat.fn();
  beat.fn = null;
  await settle();
  if (!stops("XR-34")) {
    const btn = mainOf("XR-34");
    fail("взятая разговором строка не показала «Стоп»: " + btn.className +
      " с подписью " + JSON.stringify((btn.attrs || {})["aria-label"]));
  }
  if (mainOf("XR-34").disabled) fail("«Стоп» у своей идущей работы погашен");
}

// --- возврат на вкладку перечитывает доску ---
// Телефон гасит экран и будит его, вкладка уходит в фон и возвращается, а
// фокуса окна при этом бывает и нет: браузер отбивает один visibilitychange.
{
  const was = boardHits;
  const hand = sandbox.document.handlers.visibilitychange;
  if (!hand) fail("возврат на вкладку доска не слушает: страница из фона показывает старое состояние");
  hand({});
  await settle();
  if (boardHits <= was) fail("возврат на вкладку доску не перечитал: заходов было " + was);
}

// --- стоп нажат: подсказка говорит про дожим ---
{
  phase = "stopping";
  const beat = beats().pop();
  if (!beat) fail("после взятия строки круг не завёлся заново");
  beat.fn();
  beat.fn = null;
  await settle();
  const btn = mainOf("XR-34");
  const tip = String(btn.title || (btn.attrs || {}).title || "");
  if (!stops("XR-34")) fail("во время остановки строка отдала «Стоп» кнопке запуска");
  if (!tip.includes("фоновые субагенты")) {
    fail("подсказка «Стопа» молчит про дожим: " + JSON.stringify(tip));
  }
}

console.log("poc_rowwake: ok");
