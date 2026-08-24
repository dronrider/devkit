// Стенд снятого разговора (ветка poc-chat): реплика в чат, чью сессию сняли
// перезапуском работы.
//
// Живой случай: человек писал в панель разговора task-DK-503. Конвейер задачи
// подняли заново, старую tmux-сессию сняли, а имя её досталось новому
// разговору. Реплика ушла безадресной строкой во вход задачи, её забрала
// посторонняя живая сессия того же чекаута, и человек прочитал своё «Продолжай
// работу» в чужом разговоре. В панели при этом не было ни доставки, ни отказа.
//
// Предмет стенда: в снятый разговор реплика не уходит вовсе (ни на одну ручку),
// пузырь честно стоит недоставленным с причиной, а рядом с ним работают повтор,
// отмена и выход в живой чат, занявший имя.
//
// Зовётся: node testdata/poc_chatgone.mjs static/app.js

import { makeSandbox, settle, tag, deepBtn, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const DEAD = "aaaa5030-1111-4111-8111-111111111111";
const LIVE = "bbbb5031-2222-4222-8222-222222222222";
const WHY = "сессия разговора снята: работу подняли заново, ответить в ней нечем";

// Снятый разговор приезжает с признаком gone и адресом выхода goneTo: имя его
// tmux-сессии реестр отдал разговору LIVE.
const chats = [
  { id: DEAD, title: "Выполни DK-503", mtime: "2026-08-24T13:40:00+03:00",
    tasks: ["DK-503"], model: "opus", liveModel: "opus", own: true,
    state: "dead", tree: "", gone: WHY, goneTo: LIVE },
  { id: LIVE, title: "Выполни DK-503", mtime: "2026-08-24T14:00:00+03:00",
    tasks: ["DK-503"], model: "opus", liveModel: "opus", own: true,
    tmux: "task-DK-503", state: "live", tree: "", idle: true },
];
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const board = { sections: [{ key: "in-progress", rows: [
  { id: "DK-503", title: "расход подписки", sect: "in-progress" },
] }] };

const { sandbox, timers, store } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats, models };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});

// Любая отправка на любую ручку: стенд ловит их все разом, потому что предмет
// проверки это «не уехало никуда», а не «не уехало вот на эту ручку».
const posts = [];
const plain = sandbox.fetch;
sandbox.fetch = (path, init) => {
  if (init && init.method === "POST") {
    posts.push({ path, body: init.body ? String(init.body) : "" });
  }
  return plain(path, init);
};

const st = await sandbox.chatState("demo", DEAD, board);
const panel = sandbox.chatPanel("demo", st);
const ta = tag(panel, "TEXTAREA");
const send = () => deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });

ta.value = "Продолжай работу";
send();
await settle();

// --- реплика не уехала ни в какую сессию ---
if (posts.length) {
  fail("реплика в снятый разговор всё-таки уехала: " + JSON.stringify(posts));
}

// --- пузырь честно недоставлен, и причина названа словами ---
const said = dump(panel);
if (!said.includes("не доставлено")) {
  fail("пузырь не помечен недоставленным: " + said);
}
if (!said.includes(WHY)) {
  fail("причина недоставки не названа словами: " + said);
}
if (!said.includes("Продолжай работу")) {
  fail("набранное потерялось вместе с отказом: " + said);
}

// --- выход из снятого разговора: живой чат, занявший имя ---
const away = deepBtn(panel, "открыть живой чат");
if (!away) fail("выхода в живой чат нет, человеку остался повтор в то же никуда: " + said);

// --- повтор на месте и второй раз никуда не уходит ---
const again = deepBtn(panel, "повторить");
if (!again) fail("повтора у недоставленной реплики нет: " + said);
again.handlers.click({ stopPropagation: () => {} });
await settle();
if (posts.length) {
  fail("повтор увёз реплику в снятый разговор: " + JSON.stringify(posts));
}
if (!dump(panel).includes(WHY)) {
  fail("после повтора причина пропала: " + dump(panel));
}

// --- отмена снимает реплику совсем, и персист её не воскрешает ---
const undo = deepBtn(panel, "отменить");
if (!undo) fail("отмены у недоставленной реплики нет: " + dump(panel));
undo.handlers.click({ stopPropagation: () => {} });
await settle();
if (dump(panel).includes("Продолжай работу")) {
  fail("отменённая реплика осталась в ленте: " + dump(panel));
}
for (const [key, val] of store) {
  if (key.includes("chat.pend") && String(val).includes("Продолжай работу")) {
    fail("отменённая реплика осталась в персисте: " + key + " = " + val);
  }
}

// --- выход уносит набранное в живой чат ---
ta.value = "Продолжай работу";
send();
await settle();
deepBtn(panel, "открыть живой чат").handlers.click({ stopPropagation: () => {} });
await settle();
const draft = [...store].find(([k]) => k.includes("chat.draft.") && k.includes(LIVE));
if (!draft || !String(draft[1]).includes("Продолжай работу")) {
  fail("выход не унёс набранное в живой чат: " + JSON.stringify([...store.keys()]));
}

// --- смежный случай: недоставленное, пережившее перезапуск конвейера ---
// Реплика не ушла, пока сессия умирала, и осталась в очереди неушедшей («bad»).
// Работу подняли заново, разговор сняли. Дожим такой записи обязан остановиться
// на причине у СТАРОГО разговора, а не уехать молча в новый: молчаливый переезд
// это ровно то, чем реплика попала в чужую сессию.
{
  store.set("devkit.chat.pend.demo/" + DEAD, JSON.stringify([
    { text: "ответ, не ушедший до перезапуска", wire: "ответ, не ушедший до перезапуска",
      born: Date.now() - 1000, state: "bad", why: "", tmux: "", id: "m-old", to: "" },
  ]));
  posts.length = 0;
  const st2 = await sandbox.chatState("demo", DEAD, board);
  const panel2 = sandbox.chatPanel("demo", st2);
  await settle();
  // Дожим очереди: у неушедшей записи он заведён таймером.
  for (let i = 0; i < 4; i += 1) {
    for (const t of timers.splice(0)) t.fn();
    await settle();
  }
  if (posts.length) {
    fail("недоставленное после перезапуска уехало: " + JSON.stringify(posts));
  }
  const said2 = dump(panel2);
  if (!said2.includes("ответ, не ушедший до перезапуска")) {
    fail("недоставленное потерялось вместе со снятым разговором: " + said2);
  }
  if (!said2.includes(WHY)) {
    fail("недоставленное осталось без причины: " + said2);
  }
  // И оно осталось у старого разговора, а не переехало к новому.
  const moved = store.get("devkit.chat.pend.demo/" + LIVE);
  if (moved && String(moved).includes("ответ, не ушедший до перезапуска")) {
    fail("недоставленное молча переехало в новый разговор: " + moved);
  }
  const kept = store.get("devkit.chat.pend.demo/" + DEAD);
  if (!kept || !String(kept).includes("ответ, не ушедший до перезапуска")) {
    fail("недоставленное не осталось у старого разговора: " + kept);
  }
}

console.log("ок: реплика в снятый разговор никуда не уехала, названа недоставленной, повтор, отмена и выход работают, недоставленное осталось у старого разговора");
