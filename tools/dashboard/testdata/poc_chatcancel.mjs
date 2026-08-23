// Стенд отмены недоставленной реплики (замечание пользователя: пузырь с
// «повторить» висел без дороги назад). Рядом с повтором стоит «отменить»:
// реплика уходит из очереди и из персиста, автодожим по ней останавливается,
// а отменённая первая реплика нового чата возвращает панель в чистое
// состояние: пустая лента нового чата, плашка подъёма погашена.
//
// Зовётся: node testdata/poc_chatcancel.mjs static/app.js

import { makeSandbox, settle, dump, fail, byClass, appPathArg } from "./poc_dom.mjs";

const SID = "ffff6666-0001";
const BAD = "реплика, не ушедшая в живой чат";
const NEW = "первая реплика чата, сессия которого так и не родилась";

const { sandbox, byId, timers, posted } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [], sections: {} }] };
  if (path.endsWith("/board")) return {};
  if (path.includes("/chats/") && path.endsWith("/status")) return { live: false, busy: false };
  if (path.includes("/chats")) {
    return { chats: [{ id: SID, title: "Живой диалог", tasks: [], state: "dead" }] };
  }
  if (path.includes("/sessions/" + SID)) {
    return { session: SID, head: { id: SID },
      items: [{ key: "t-1", role: "user", text: "старая реплика ленты",
        time: "2026-08-23T10:00:00+03:00" }], start: true };
  }
  return {};
});
await settle();

const pin = byId.get("cpin");

// --- недоставленное в живом чате: отмена снимает пузырь и дожим ---
sandbox.localStorage.setItem("devkit.chat.pend.demo/" + SID,
  JSON.stringify([{ text: BAD, wire: BAD, born: Date.now(), state: "bad" }]));
sandbox.location.hash = "#demo/chat/" + SID;
await sandbox.refresh();
await settle();

let box = byClass(pin, "mlocal");
if (!box || !dump(box).includes(BAD)) fail("недоставленный пузырь не восстановился: " + dump(pin).slice(0, 200));
const undoBtn = (function find(node) {
  if (node.tagName === "BUTTON" && dump(node).trim() === "отменить") return node;
  for (const kid of node.children || []) {
    const hit = typeof kid === "object" && find(kid);
    if (hit) return hit;
  }
  return null;
})(box);
if (!undoBtn) fail("рядом с «повторить» нет отмены: " + dump(box).slice(0, 300));
undoBtn.handlers.click({});
await settle();
if (dump(pin).includes(BAD)) fail("отменённая реплика осталась на экране");
if (sandbox.localStorage.getItem("devkit.chat.pend.demo/" + SID)) {
  fail("отменённая реплика осталась в персисте");
}
// Автодожим остановлен: срабатывание отложенных таймеров ничего не шлёт.
const wasPosted = posted.length;
for (const t of timers.splice(0)) t.fn();
await settle();
if (posted.length !== wasPosted) {
  fail("после отмены дожим снова пошёл слать: " + posted.slice(wasPosted).join(", "));
}

// --- первая реплика нового чата: отмена возвращает чистый новый чат ---
sandbox.localStorage.setItem("devkit.chat.pend.demo/new",
  JSON.stringify([{ text: NEW, wire: NEW, born: Date.now() - 120000, state: "wait" }]));
sandbox.location.hash = "#demo/chat/new";
await sandbox.refresh();
await settle();
// Просроченное «отправляется» дозревает своим таймером до причины с кнопками.
for (const t of timers.splice(0)) t.fn();
await settle();
box = byClass(pin, "mlocal");
if (!box || !dump(box).includes(NEW)) fail("пузырь первой реплики не восстановился");
const undo2 = (function find(node) {
  if (node.tagName === "BUTTON" && dump(node).trim() === "отменить") return node;
  for (const kid of node.children || []) {
    const hit = typeof kid === "object" && find(kid);
    if (hit) return hit;
  }
  return null;
})(box);
if (!undo2) fail("у дозревшего пузыря нового чата нет отмены: " + dump(box).slice(0, 300));
undo2.handlers.click({});
await settle();
if (dump(pin).includes(NEW)) fail("отменённая первая реплика осталась на экране");
if (sandbox.localStorage.getItem("devkit.chat.pend.demo/new")) {
  fail("отменённая первая реплика осталась в персисте");
}
if (!dump(pin).includes("новый чат: напишите первую реплику")) {
  fail("панель не вернулась в чистое состояние нового чата: " + dump(pin).slice(0, 300));
}
const busy = byClass(pin, "busyrow");
if (busy && !busy.hidden) fail("плашка подъёма сессии горит после отмены");

console.log("ok: отмена снимает недоставленную реплику из очереди и персиста, " +
  "дожим молчит, отменённая первая реплика возвращает чистый новый чат");
