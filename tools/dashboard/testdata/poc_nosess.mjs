// Стенд разговора без процесса (POC DK-397, ветка poc-chat; переписан DK-701).
//
// Живой случай: «чат, который я вернул из архива, больше не работает, при
// написании в него никакой реакции» (замечание пользователя). Архивирование
// снимает сессию, возврат её не поднимает, и разговор без процесса выглядит на
// экране точно так же, как живой. Реплика в него уезжает резюмом, ответ идёт
// через минуту, а человек всё это время не знает, работает ли чат вообще.
//
// Кнопка «Поднять» этот случай красила отдельной строкой и требовала нажатия,
// хотя хватало и первой реплики (DK-701). Строки и кнопки теперь нет вовсе:
// общий индикатор работы встаёт остановкой сам, без действия человека.
//
// Предмет стенда две стороны одного узла. Разговор без процесса красит общий
// индикатор остановкой и не держит кнопки. Уборка в архив, наоборот,
// спрашивает, когда снимает живую сессию, и не спрашивает у записи, за которой
// процесса нет: терять там нечего.
//
// Зовётся: node testdata/poc_nosess.mjs static/app.js

import { makeSandbox, settle, dump, tag, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "DK", sections: [{ key: "check", rows: [
  { id: "DK-606", title: "LLD: приёмы прозы", sect: "check" }] }] };
const models = [{ model: "fable", tier: "max", harness: "claude-code" }];

// Разговор, вернувшийся из архива: транскрипт есть, процесса нет.
const dead = { id: "b7cc7ae5", project: "devkit", title: "Груминг задачи DK-606",
  mtime: "2026-08-30T07:47:46Z", tasks: ["DK-606"], model: "fable", state: "dead",
  idle: true, own: true, tmux: "chat-DK-606-1", bound: "lead" };
const live = Object.assign({}, dead, { id: "live-1", state: "live", sock: "/tmp/s", pid: 42 });

let entry = dead;
const posted = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  const p = String(path);
  if (init && init.method === "POST") posted.push({ path: p, body: JSON.parse(init.body || "{}") });
  if (p.includes("/sessions/")) return { session: entry.id, head: { id: entry.id }, items: [], total: 0 };
  if (p.includes("/chats")) return { chats: [entry], models, days: 3, older: false };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});
await settle();

// Разговор без процесса: кнопки «Поднять» и строки «сессии нет» на панели нет
// вовсе, а общий индикатор работы стоит сразу, не дожидаясь нажатия, красится
// остановкой и говорит, что реплика поднимет разговор (DK-701).
let st = await sandbox.chatState("devkit", "b7cc7ae5", board);
let panel = sandbox.chatPanel("devkit", st);
await settle();
if (byClass(panel, "cnosess")) fail("строка «сессии нет» всё ещё на панели");
if (deepBtn(panel, "Поднять")) fail("кнопка «Поднять» всё ещё на панели");
const busyDead = byClass(panel, "busyrow");
if (!busyDead) fail("индикатора работы на панели нет");
if (busyDead.hidden) fail("индикатор остановки спрятан: работу не видно без реплики");
if (!String(busyDead.className || "").split(" ").includes("stop")) {
  fail("индикатор остановки не покрашен: класса stop нет");
}
const stoppedSaid = dump(busyDead);
if (!stoppedSaid.includes("реплика") || !stoppedSaid.includes("поднимет")) {
  fail("индикатор не говорит, что реплика поднимет разговор: " + stoppedSaid);
}

// Живой разговор ни кнопки, ни красной остановки не показывает: индикатор
// стоит спрятанным, пока по чату не идёт работа.
entry = live;
posted.length = 0;
st = await sandbox.chatState("devkit", "live-1", board);
panel = sandbox.chatPanel("devkit", st);
await settle();
if (byClass(panel, "cnosess")) fail("живому разговору строка «сессии нет» не нужна");
const busyLive = byClass(panel, "busyrow");
if (!busyLive || !busyLive.hidden) fail("индикатор живого разговора виден без работы");
if (String(busyLive && busyLive.className || "").split(" ").includes("stop")) {
  fail("живой разговор покрашен остановкой");
}

// Уборка живого разговора спрашивает: первое нажатие взводит кнопку, ручка
// молчит, второе убирает.
function archBtn(chat) {
  const holder = { children: [] };
  holder.append = (...kids) => { holder.children.push(...kids); };
  return sandbox.chatArchBtn("devkit", chat, () => {});
}

posted.length = 0;
const hot = Object.assign({}, live, { archived: false });
const btn = archBtn(hot);
(btn.handlers.click || btn.onclick)({ stopPropagation() {} });
await settle();
if (posted.length) fail("уборка живого разговора ушла на сервер с первого нажатия: " + JSON.stringify(posted));
if (!String(btn.className).includes("armed")) fail("кнопка уборки не взвелась: вопроса не видно");
if (!String((btn.attrs || {})["aria-label"] || "").includes("Точно")) {
  fail("взведённая кнопка не назвала вопроса словами");
}
(btn.handlers.click || btn.onclick)({ stopPropagation() {} });
await settle();
if (!posted.some((r) => r.path.endsWith("/archive") && r.body.archived === true)) {
  fail("второе нажатие не убрало разговор в архив: " + JSON.stringify(posted));
}

// Запись без процесса убирается первым нажатием: снимать нечего.
posted.length = 0;
const cold = Object.assign({}, dead, { archived: false });
const btn2 = archBtn(cold);
(btn2.handlers.click || btn2.onclick)({ stopPropagation() {} });
await settle();
if (!posted.some((r) => r.path.endsWith("/archive"))) {
  fail("разговор без процесса спросил лишнего: убирать у него нечего");
}

// Возврат из архива не спрашивает вовсе: он ничего не снимает.
posted.length = 0;
const back = Object.assign({}, live, { archived: true });
const btn3 = archBtn(back);
(btn3.handlers.click || btn3.onclick)({ stopPropagation() {} });
await settle();
if (!posted.some((r) => r.path.endsWith("/archive") && r.body.archived === false)) {
  fail("возврат из архива спросил подтверждения: снимать при возврате нечего");
}

console.log("poc_nosess: ok");
