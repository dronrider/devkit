// Стенд плашки «думает...» после явного стопа хода в панели чата (второй
// хвост второй приёмки DK-716, 2026-09-05).
//
// Живой случай: человек послал реплику, панель показала «агент работает...»,
// человек нажал «Стоп» в панели, ход прервался и привязка снялась, а плашка
// осталась гореть. Опрос /status считает занятость и по хвосту транскрипта,
// где незакрытый вызов инструмента висит до получаса (busyNow, sessions.go).
// После явного Escape плашка зависала бы на весь этот срок, хотя ответ
// самого стопа уже сказал, что ход кончен.
//
// Предмет стенда: клик по кнопке стопа в панели чата гасит плашку сразу,
// ответом самой ручки /stop, не дожидаясь опроса состояния.
//
// Зовётся: node testdata/poc_chatstopbusy.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const SID = "eeee7777-0001";

const posted = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  const method = init && init.method;
  if (method === "POST") posted.push(path);
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats") && path.endsWith("/stop")) {
    return { way: "escape", tmux: "chat-XR-1-1",
      message: "ход прерван: сессия жива и ждёт следующей реплики" };
  }
  if (path.includes("/chats") && path.endsWith("/say")) return { ok: true };
  if (path.includes("/chats") && path.endsWith("/status")) return { live: true, busy: true };
  if (path.includes("/chats")) {
    return { chats: [{ id: SID, state: "live", tmux: "chat-XR-1-1", idle: false,
      title: "живой разговор" }], models: [] };
  }
  if (path.includes("/sessions/" + SID)) return { items: [], start: true };
  if (path === "/api/harnesses") return { harnesses: [] };
  return {};
});

const board = { prefix: "XR", sections: [] };
const st = await sandbox.chatState("demo", SID, board);
if (st.sid !== SID) fail("состояние не встало на живую сессию: " + JSON.stringify(st));
const panel = sandbox.chatPanel("demo", st);
await settle();

const findTag = (node, tagName) => {
  if (!node) return null;
  if (node.tagName === tagName) return node;
  for (const kid of node.children || []) {
    const hit = findTag(kid, tagName);
    if (hit) return hit;
  }
  return null;
};

// --- реплика поднимает плашку работы ---
const ta = findTag(panel, "TEXTAREA");
if (!ta) fail("поля ввода в панели нет: " + dump(panel).slice(0, 300));
ta.value = "первая реплика хода";
ta.handlers.keydown({ key: "Enter", preventDefault: () => {} });
await settle();

const plate = byClass(panel, "busyrow");
if (!plate || plate.hidden) {
  fail("после отправки реплики плашка работы не встала: " + dump(panel).slice(0, 300));
}

// --- стоп в панели гасит плашку сразу, не дожидаясь опроса ---
const stopBtn = byClass(panel, "cstop");
if (!stopBtn) fail("в панели нет кнопки стопа хода: " + dump(panel).slice(0, 300));
stopBtn.handlers.click({ stopPropagation: () => {} });
await settle();

if (!posted.some((p) => p.includes("/stop"))) {
  fail("нажатие стопа не позвало ручку /stop: " + JSON.stringify(posted));
}
if (!plate.hidden) {
  fail("плашка работы осталась гореть после явного стопа хода: " + dump(plate));
}

console.log("poc_chatstopbusy: ok");
