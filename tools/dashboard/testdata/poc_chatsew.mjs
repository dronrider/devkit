// Стенд шва ленты (снимки пользователя: мигание при пришивании нового чата и
// дубль «вы, 14:03» из оптимистичного пузыря и его транскриптной копии).
// Реплика человека в любой момент стоит в панели ровно одной копией:
// пришивание переносит память адреса подъёма к родившемуся диалогу (пузырь
// переживает переезд), начальный хвост ленты проходит эхо-сверку той же
// перерисовкой, а переключение диалога не мигает плашкой «чат открывается...»
// поверх живого содержимого.
//
// Зовётся: node testdata/poc_chatsew.mjs static/app.js

import { makeSandbox, settle, dump, fail, byClass, appPathArg } from "./poc_dom.mjs";

const SID = "dddd4444-0001";
const SID2 = "eeee5555-0002";
const FIRST = "первая реплика нового чата, достаточно длинная для пришивания";
const SECOND = "вторая реплика, пережившая пересборку панели с персистом";

const chats = [];
let tail1 = [];

const { sandbox, byId, streams } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [], sections: {} }] };
  if (path.endsWith("/board")) return {};
  if (path.includes("/chats/") && path.endsWith("/status")) return { live: false, busy: false };
  if (path.includes("/chats")) return { chats };
  if (path.includes("/sessions/" + SID2)) {
    return { session: SID2, head: { id: SID2 },
      items: [{ key: "n-1", role: "user", text: "реплика соседнего диалога",
        time: "2026-08-23T10:00:00+03:00" }], start: true };
  }
  if (path.includes("/sessions/" + SID)) {
    return { session: SID, head: { id: SID }, items: tail1, start: true };
  }
  return {};
});
await settle();

const pin = byId.get("cpin");
const count = (text) => dump(pin).split(text).length - 1;

// --- пришивание: реплика не пропадает и не двоится ---
// Отправка уже случилась: персист адреса подъёма держит реплику с именем
// tmux, реестр назвал сессию, а транскрипт ещё пуст (клиент не дописал).
sandbox.localStorage.setItem("devkit.chat.pend.demo/new",
  JSON.stringify([{ text: FIRST, wire: FIRST, born: Date.now(), state: "wait", tmux: "chat-abc" }]));
chats.push({ id: SID, title: "Новый чат", tmux: "chat-abc", tasks: [], state: "live" });

sandbox.location.hash = "#demo/chat/new";
await sandbox.refresh();
await settle();

if (count(FIRST) !== 1) {
  fail("пришивание к диалогу с пустым транскриптом оставило копий: " + count(FIRST) +
    ", реплика обязана пережить переезд панели одним пузырём");
}

// Эхо приезжает потоком: транскриптная копия встаёт, местный пузырь уходит той
// же перерисовкой, копия по-прежнему одна.
const es = streams.find((s) => String(s.url).includes(SID) && String(s.url).includes("stream"));
if (!es) fail("поток ленты пришитого диалога не открыт");
es.onmessage({ data: JSON.stringify({ key: "t-1", role: "user", text: FIRST,
  time: "2026-08-23T10:01:00+03:00" }) });
await settle();
if (count(FIRST) !== 1) {
  fail("после эха из потока копий " + count(FIRST) + ", ждал одну");
}
const pend1 = byClass(pin, "mlocal");
if (pend1 && dump(pend1).includes(FIRST)) {
  fail("местный пузырь не снят эхом из потока");
}

// --- дубль восстановленного пузыря (снимок с двумя «вы, 14:03») ---
// Персист держит реплику, транскрипт уже содержит её копию: пересборка панели
// обязана снять пузырь той же перерисовкой, которой хвост встаёт в ленту.
sandbox.localStorage.setItem("devkit.chat.pend.demo/" + SID,
  JSON.stringify([{ text: SECOND, wire: SECOND, born: Date.now(), state: "held", why: "ждёт эха" }]));
tail1 = [
  { key: "t-1", role: "user", text: FIRST, time: "2026-08-23T10:01:00+03:00" },
  { key: "t-2", role: "user", text: SECOND, time: "2026-08-23T10:03:00+03:00" },
];
sandbox.location.hash = "#demo/chat/" + SID;
const cpKey = "demo|" + SID;
// Пересборка той же панели: ключ открытого чата сбрасывается, как это делает
// repaintChat, иначе paintChat узнает адрес и не станет пересобирать.
sandbox.repaintChat && await sandbox.repaintChat();
await settle();
if (count(SECOND) !== 1) {
  fail("восстановленный пузырь дублирует транскриптную копию: копий " + count(SECOND) +
    " (ключ " + cpKey + ")");
}
if (sandbox.localStorage.getItem("devkit.chat.pend.demo/" + SID)) {
  fail("снятый эхом пузырь остался в персисте");
}

// --- переключение диалога не мигает плашкой поверх живого содержимого ---
const before = dump(pin);
if (!before.includes(FIRST)) fail("панель потеряла ленту до переключения");
const painting = sandbox.paintChat("demo", SID2, null, []);
if (dump(pin).includes("чат открывается")) {
  fail("переключение диалога мигнуло плашкой «чат открывается...» поверх живого");
}
if (!dump(pin).includes(FIRST)) {
  fail("прежнее содержимое снято до готовности нового диалога");
}
// Отклик виден в тот же ход: над прежним разговором встаёт строка перехода
// (её же меряет poc_bench_chat).
if (!dump(pin).includes("открывается другой разговор")) {
  fail("переключение не показало отклика в тот же ход");
}
await painting;
await settle();
if (!dump(pin).includes("реплика соседнего диалога")) {
  fail("переключение не довело до соседнего диалога: " + dump(pin).slice(0, 200));
}

console.log("ok: реплика стоит одной копией при пришивании, восстановлении " +
  "персиста и эхе из потока, переключение диалога без мигания плашкой");
