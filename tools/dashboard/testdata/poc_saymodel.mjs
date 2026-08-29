// Стенд выбора модели в новом разговоре (POC DK-397, ветка poc-chat).
//
// Живой случай: «нельзя поменять модель в новом чате» (замечание
// пользователя). Список моделей дашборд не сочиняет, он целиком приезжает от
// agentctl, и пока лестница ярусов до него не доехала, выпадающий список
// схлопывался в одну строку с текущей моделью, молча и без объяснений.
//
// Предмет стенда две стороны. Лестница приехала: выбор в незачатом разговоре
// виден весь, выбранное запоминается за записью и уезжает в подъём первой
// репликой, то есть сессия рождается на выбранной модели. Лестницы нет: список
// говорит словами, что выбирать нечем, а причина стоит на нём подсказкой.
//
// Зовётся: node testdata/poc_saymodel.mjs static/app.js

import { makeSandbox, settle, tag, dump, fail, appPathArg } from "./poc_dom.mjs";

// Ожидание подъёма идёт по таймеру, а часы в песочнице стендовые: заводы
// прокручиваются руками, иначе подъём не досчитает вовсе.
async function tick(timers, times) {
  for (let i = 0; i < (times || 5); i += 1) {
    const bag = timers.splice(0, timers.length);
    for (const t of bag) t.fn();
    await settle();
  }
}

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };
const models = [
  { model: "haiku", tier: "mini", harness: "claude-code" },
  { model: "sonnet", tier: "base", harness: "claude-code" },
  { model: "opus", tier: "pro", harness: "claude-code", default: true },
];
const blank = { id: "blank-7", project: "demo", blank: true, state: "not-started", idle: true,
  model: "opus", mtime: "2026-08-29T12:00:00+03:00", tasks: [] };

let ladder = models;
let note = "";
const kept = [];
const raised = [];
const { sandbox, timers } = makeSandbox(app, (path, init) => {
  const p = String(path);
  if (init && init.method === "POST") {
    if (p.endsWith("/model")) {
      kept.push(JSON.parse(init.body).model);
      // Ручка модели пишет выбор в память записи: следующий список отдаст его.
      blank.model = JSON.parse(init.body).model;
      return { model: blank.model };
    }
    if (p.endsWith("/chats")) {
      raised.push(JSON.parse(init.body));
      return { tmux: "chat-9", model: JSON.parse(init.body).model };
    }
    return {};
  }
  if (p.includes("/sessions/")) return { items: [], total: 0 };
  // Опрос имени tmux после подъёма: сессия родилась и назвалась, и ожидание
  // подъёма в стенде кончается первым же заходом.
  if (p.includes("tmux=")) {
    return { chats: raised.length
      ? [{ id: "sess-9", project: "demo", tmux: "chat-9", state: "live",
           mtime: "2026-08-29T12:05:00+03:00", tasks: [] }]
      : [] };
  }
  if (p.includes("/chats")) {
    const out = { chats: [blank], models: ladder, days: 3, older: false };
    if (note) out.models_note = note;
    return out;
  }
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});
await settle();

function sel(panel) {
  const box = tag(panel, "SELECT");
  if (!box) fail("выбора модели в панели нет вовсе: " + dump(panel).slice(0, 300));
  return box;
}

// Лестница приехала: в списке стоят все модели, а не одна текущая.
let st = await sandbox.chatState("demo", "blank-7", board);
let panel = sandbox.chatPanel("demo", st);
await settle();
let box = sel(panel);
const names = (box.children || []).map((o) => String(o.textContent || ""));
for (const want of ["haiku", "sonnet", "opus"]) {
  if (!names.includes(want)) fail("модели " + want + " в выборе нет: " + names.join(", "));
}

// Выбор в незачатом разговоре запоминается за записью, а не за вкладкой: он
// обязан пережить перезагрузку и уехать в подъём.
box.value = "sonnet";
box.handlers.change({});
await settle();
if (!kept.includes("sonnet")) fail("выбор модели не уехал в память записи: " + kept.join(", "));

// Первая реплика поднимает сессию именно на выбранной модели.
st = await sandbox.chatState("demo", "blank-7", board);
panel = sandbox.chatPanel("demo", st);
await settle();
const raise = sandbox.chatRaise("demo", st, "первая реплика",
  st.entry ? st.entry.model : "", () => {});
await settle();
await tick(timers, 4);
await raise;
if (!raised.length) fail("подъём не состоялся вовсе");
if (raised[0].model !== "sonnet") {
  fail("сессия поднята не на выбранной модели: " + JSON.stringify(raised[0]));
}
if (raised[0].chat !== "blank-7") {
  fail("подъём не пришит к записи разговора: " + JSON.stringify(raised[0]));
}

// Лестницы нет: список говорит словами, что выбирать нечем.
ladder = [];
note = "лестница ярусов пуста: agentctl harness --json не назвал ни одной модели";
st = await sandbox.chatState("demo", "blank-7", board);
panel = sandbox.chatPanel("demo", st);
await settle();
box = sel(panel);
const said = (box.children || []).map((o) => String(o.textContent || "")).join(" | ");
if (!said.includes("выбора нет")) {
  fail("пустой выбор молчит, и человек читает его как «модель тут одна»: " + said);
}
if (!String(box.title).includes("лестница ярусов пуста")) {
  fail("причина пустого выбора не стоит подсказкой: " + box.title);
}

console.log("poc_saymodel: ok");
