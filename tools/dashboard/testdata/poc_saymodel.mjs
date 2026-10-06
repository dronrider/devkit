// Стенд выбора модели в новом разговоре (POC DK-397, ветка poc-chat).
//
// Живой случай: «нельзя поменять модель в новом чате» (замечание
// пользователя). Список моделей дашборд не сочиняет, он целиком приезжает от
// agentctl, и пока лестница ярусов до него не доехала, выпадающий список
// схлопывался в одну строку с текущей моделью, молча и без объяснений.
//
// Предмет стенда три стороны. Лестница приехала: выбор в незачатом разговоре
// виден весь, выбранное запоминается за записью и уезжает в подъём первой
// репликой, то есть сессия рождается на выбранной модели. Повтор имени у двух
// подписок различим: строка списка называет квоту текстом, и выбранная пара
// едет в память записи и в подъём как есть (DK-1281). Лестницы нет: список
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
  // Честный повтор имени у второй подписки: модели с одним именем у двух
  // подписок в списке две, и различает их только квота в тексте строки.
  { model: "sonnet", tier: "base", harness: "glm-code" },
];
const blank = { id: "blank-7", project: "demo", blank: true, state: "not-started", idle: true,
  model: "opus", pickHarness: "claude-code", mtime: "2026-08-29T12:00:00+03:00", tasks: [] };

let ladder = models;
let note = "";
const kept = [];
const raised = [];
const { sandbox, timers } = makeSandbox(app, (path, init) => {
  const p = String(path);
  if (init && init.method === "POST") {
    if (p.endsWith("/model")) {
      kept.push(JSON.parse(init.body));
      // Ручка модели пишет выбор в память записи: следующий список отдаст его.
      const pick = JSON.parse(init.body);
      blank.model = pick.model;
      blank.pickHarness = pick.harness;
      return { model: pick.model, harness: pick.harness };
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
for (const want of ["haiku (claude-code)", "opus (claude-code)"]) {
  if (!names.includes(want)) fail("модели " + want + " в выборе нет: " + names.join(", "));
}
// Повтор имени у двух подписок стоит двумя строками, и каждая называет квоту:
// голое имя в этом списке не выбирает ничего, кроме первой попавшейся квоты.
for (const want of ["sonnet (claude-code)", "sonnet (glm-code)"]) {
  if (!names.includes(want)) fail("повтор имени не подписан квотой: " + names.join(", "));
}

// Старая запись без подписки в выборе: имя в лестнице стоит подписанной
// строкой, и голого ряда рядом с ней быть не должно, а выбранной оказывается
// сама подписанная строка: владелец у безымянного подъёма тот же (замечание
// 1 ревью DK-1281).
blank.pickHarness = "";
st = await sandbox.chatState("demo", "blank-7", board);
panel = sandbox.chatPanel("demo", st);
await settle();
box = sel(panel);
const bare = (box.children || []).map((o) => String(o.textContent || ""));
if (bare.includes("opus")) {
  fail("голая строка имени стоит рядом с подписанной: " + bare.join(", "));
}
const onName = (box.children || []).find((o) => o.selected);
if (!onName || String(onName.textContent || "") !== "opus (claude-code)") {
  fail("выбор без подписки не лёг на подписанную строку имени: " +
    (onName ? onName.textContent : "нет выбранной строки"));
}
blank.pickHarness = "claude-code";

// Выбор повтора запоминается за записью парой, а не одним именем: сессия
// обязана подняться квотой выбранной строки, и имя без подписки сюда не
// доезжает. Стенд выбирает вторую строку повтора флагом selected, как это
// делает сам экран: value у повтора одно на две строки.
const repeat = (box.children || []).find((o) => String(o.textContent || "") === "sonnet (glm-code)");
if (!repeat) fail("строки повтора с квотой glm-code в списке нет");
repeat.selected = true;
box.handlers.change({});
await settle();
const keptPair = kept.find((k) => k.model === "sonnet" && k.harness === "glm-code");
if (!keptPair) fail("выбор повтора не уехал парой с подпиской: " + JSON.stringify(kept));

// Первая реплика поднимает сессию на выбранной паре: подписка подъёма это
// подписка выбора, а не первая, у которой имя домашнее.
st = await sandbox.chatState("demo", "blank-7", board);
panel = sandbox.chatPanel("demo", st);
await settle();
const pick = st.entry && st.entry.model
  ? { model: st.entry.model, harness: st.entry.pickHarness || "" }
  : { model: "opus", harness: "" };
const raise = sandbox.chatRaise("demo", st, "первая реплика", pick, () => {});
await settle();
await tick(timers, 4);
await raise;
if (!raised.length) fail("подъём не состоялся вовсе");
if (raised[0].model !== "sonnet" || raised[0].harness !== "glm-code") {
  fail("сессия поднята не выбранной парой: " + JSON.stringify(raised[0]));
}
if (raised[0].chat !== "blank-7") {
  fail("подъём не пришит к записи разговора: " + JSON.stringify(raised[0]));
}

// Живой разговор показывается моделью с квотой своего транскрипта: короткое
// имя гасило и поставщика агрегатора, и квоту, и человек читал «fable» у
// разговора, идущего чужой подпиской (DK-1281).
blank.state = "live";
blank.liveModel = "anthropic/claude-opus-5.5";
blank.harness = "routerai";
st = await sandbox.chatState("demo", "blank-7", board);
panel = sandbox.chatPanel("demo", st);
await settle();
box = sel(panel);
const shownLive = (box.children || []).find((o) => o.selected);
if (!shownLive || String(shownLive.textContent || "") !== "anthropic/claude-opus-5.5 (routerai)") {
  fail("живой разговор не назван моделью с квотой: " +
    (shownLive ? shownLive.textContent : "нет выбранной строки"));
}

// Лестницы нет: список говорит словами, что выбирать нечем.
blank.state = "not-started";
blank.liveModel = "";
blank.harness = "";
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

// Имени в лестнице нет вовсе: строка без квоты это честный способ сказать,
// что владелец у неё один по порядку харнессов, и такая строка остаётся.
blank.model = "fable";
blank.pickHarness = "";
ladder = models;
note = "";
st = await sandbox.chatState("demo", "blank-7", board);
panel = sandbox.chatPanel("demo", st);
await settle();
box = sel(panel);
const stranger = (box.children || []).find((o) => String(o.textContent || "") === "fable");
if (!stranger) fail("имени вне лестницы нет строкой выбора вовсе");
if (stranger && !stranger.selected) fail("строка имени вне лестницы не выбрана");

console.log("poc_saymodel: ok");
