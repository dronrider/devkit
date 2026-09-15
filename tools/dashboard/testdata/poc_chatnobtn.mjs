// Стенд пузыря без кнопок (DK-1011, слово пользователя: плашек и кнопок
// «повторить»/«отменить» в чате нет, чаты живые). Кнопки стояли у неушедшей и
// у недоставленной реплики, и человек разбирал руками то, что дашборд умеет
// сам: неушедшее дожимает автодожим, реплика в очереди лежит и ждёт своей
// сессии.
//
// Предмет стенда: ни в одном состоянии пузыря кнопок нет, а неушедшее уезжает
// дожимом само.
//
// Зовётся: node testdata/poc_chatnobtn.mjs static/app.js

import { makeSandbox, settle, dump, fail, byClass, deepBtn, appPathArg } from "./poc_dom.mjs";

const SID = "ffff6666-0001";
const BAD = "реплика, не ушедшая в живой чат";
const NEW = "первая реплика чата, сессия которого так и не родилась";
const BTNS = ["повторить", "отменить", "открыть живой чат", "поднять работу по задаче"];

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
// В контейнере панели живёт пул слотов: показан один, прочие спрятаны и лежат
// готовыми к возврату. Стенд смотрит на показанный, спрятанное это память
// пула, а не экран.
const livePin = () => (pin.children || []).find(
  (n) => String(n.className || "").includes("cslot") &&
    !String(n.className || "").split(" ").includes("off")) || pin;

const noButtons = (node, where) => {
  for (const word of BTNS) {
    if (deepBtn(node, word)) fail("в пузыре осталась кнопка «" + word + "» (" + where + ")");
  }
};

// --- неушедшее в живом чате: кнопок нет, дожим уносит реплику сам ---
sandbox.localStorage.setItem("devkit.chat.pend.demo/" + SID,
  JSON.stringify([{ text: BAD, wire: BAD, born: Date.now(), state: "bad", id: "m-bad" }]));
sandbox.location.hash = "#demo/chat/" + SID;
await sandbox.refresh();
await settle();

let box = byClass(livePin(), "mlocal");
if (!box || !dump(box).includes(BAD)) {
  fail("неушедший пузырь не восстановился: " + dump(pin).slice(0, 200));
}
if (!dump(box).includes("не ушло")) {
  fail("состояние неушедшей реплики не названо строкой: " + dump(box).slice(0, 300));
}
noButtons(box, "неушедшая реплика");

const wasPosted = posted.length;
for (let i = 0; i < 4; i += 1) {
  for (const t of timers.splice(0)) t.fn();
  await settle();
}
if (posted.length === wasPosted) {
  fail("дожим неушедшей реплики не пошёл: слать её теперь нечем, кнопки-то нет");
}

// --- дозревшая первая реплика нового чата: причина строкой, кнопок нет ---
sandbox.localStorage.setItem("devkit.chat.pend.demo/new",
  JSON.stringify([{ text: NEW, wire: NEW, born: Date.now() - 120000, state: "wait", id: "m-new" }]));
sandbox.location.hash = "#demo/chat/new";
await sandbox.refresh();
await settle();
// Просроченное «отправляется» дозревает своим таймером до причины.
for (const t of timers.splice(0)) t.fn();
await settle();
box = byClass(livePin(), "mlocal");
if (!box || !dump(box).includes(NEW)) fail("пузырь первой реплики не восстановился");
if (!dump(box).includes("дольше обычного")) {
  fail("у дозревшего пузыря нет причины строкой: " + dump(box).slice(0, 300));
}
noButtons(box, "дозревшая первая реплика");
// Текст человека при этом никуда не девается: снять пузырь рукой нечем, и
// потерять его тем более нельзя.
if (!sandbox.localStorage.getItem("devkit.chat.pend.demo/new")) {
  fail("дозревшая реплика пропала из персиста");
}
const busy = byClass(livePin(), "busyrow");
if (busy && !busy.hidden) fail("плашка подъёма горит поверх причины на пузыре");

console.log("ok: кнопок в пузыре нет ни в одном состоянии, состояние названо строкой, " +
  "неушедшее уезжает дожимом само");
