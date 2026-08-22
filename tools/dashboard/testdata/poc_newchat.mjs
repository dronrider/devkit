// Стенд инициализации нового чата (пачка замечаний пользователя): заголовок
// панели говорит «Новый чат», а не «чата нет»; пустая лента стоит без приписки
// про транскрипт; первая реплика не пропадает, пока сессия рождается, ошибка
// про реестр человеку не показывается, а найденный диалог пришивается сам.
//
// Живой повод: клиент, поднятый в каталоге без записи доверия, встаёт на
// вопросе в своём терминале, сессия не называет себя минутами, и прежний код
// хоронил пузырь первой реплики и пугал ошибкой «ещё не назвала себя в
// реестре».
//
// Зовётся: node testdata/poc_newchat.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { sections: [] };
let found = false;
// Сессия, чьего транскрипта в проекте нет: протухший адрес панели.
const LOST = "deadc0de-0000-4000-8000-000000000000";
// Старый разговор глубже видимого верха списка: транскрипт на диске есть.
const DEEP = "01dcafe0-0000-4000-8000-000000000000";

const { sandbox, timers, store } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats") && init && init.method === "POST") {
    return { tmux: "chat-1", model: "opus", tree: "/x" };
  }
  if (path.includes("/chats?tmux=")) {
    return { chats: found ? [{ id: "f00dcafe-0001", state: "live" }] : [] };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path.includes("/sessions/" + LOST)) {
    return { raw: { status: 404, statusText: "Not Found",
      text: JSON.stringify({ error: "транскрипта " + LOST + " нет среди сессий проекта demo" }) } };
  }
  if (path.includes("/sessions/")) return { items: [], start: true };
  if (path === "/api/harnesses") return { harnesses: [] };
  return {};
});

// Панель нового чата: адрес new, сессии нет.
sandbox.location.hash = "#demo/chat/new";
const st = await sandbox.chatState("demo", "new", board);
if (!st.fresh) fail("адрес new не дал свежего состояния: " + JSON.stringify(st));

// --- заголовок говорит «Новый чат» ---
const head = sandbox.chatHead("demo", st);
const said = dump(head).replace(/\s+/g, " ");
if (said.includes("чата нет")) fail("заголовок нового чата говорит «чата нет»: " + said);
if (!said.includes("Новый чат")) fail("заголовок не назвал новый чат: " + said);

// --- пустая лента без приписки про транскрипт ---
const panel = sandbox.chatPanel("demo", st);
await settle();
const shown = dump(panel).replace(/\s+/g, " ");
if (shown.includes("чат пуст") || shown.includes("в транскрипте нет")) {
  fail("у пустого чата осталась приписка про транскрипт: " + shown);
}

// --- первая реплика: пузырь живёт всё ожидание ---
const ta = (function find(node) {
  if (node.tagName === "TEXTAREA") return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && find(kid);
    if (got) return got;
  }
  return null;
})(panel);
if (!ta) fail("поля ввода в панели нет");
ta.value = "первая реплика нового чата";
ta.handlers.keydown({ key: "Enter", preventDefault: () => {} });
await settle();

const bubbleIn = (node) => dump(node).includes("первая реплика нового чата");
if (!bubbleIn(panel)) fail("пузырь первой реплики не встал в панель");

// --- нить ленты у одинокого пузыря начинается точкой, а не обрубком ---
// Метка gtop у строки та же, что у реплики человека в ленте: стиль
// .frow.gtop.f-bub::before режет линию до точки, без метки она висела в
// воздухе от верха контейнера (замечание пользователя по снимку).
const pendRow = (function find(node) {
  if (String(node.className || "").includes("frow")) return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && find(kid);
    if (got) return got;
  }
  return null;
})(byClass(panel, "mlocal"));
if (!pendRow || !String(pendRow.className).includes("gtop")) {
  fail("строка одинокого пузыря без метки gtop, нить начнётся в воздухе: " +
    (pendRow && pendRow.className));
}

// --- плашка о подъёме сессии видна, а не пустота ---
const plate = byClass(panel, "busyrow");
if (!plate || plate.hidden || !dump(plate).includes("сессия поднимается")) {
  fail("плашки о подъёме сессии нет: " + (plate ? dump(plate) : "узла нет"));
}

// Опрос реестра: дёргаются только таймеры ожидания chatWait (1.5s), по одному,
// как их и ставит цикл. Список всё это время пуст: клиент стоит на вопросе в
// своём терминале.
const fired = new Set();
const fireNext = async (ms) => {
  for (let i = 0; i < timers.length; i += 1) {
    if (fired.has(i) || timers[i].ms !== ms) continue;
    fired.add(i);
    timers[i].fn();
    await settle();
    return true;
  }
  return false;
};
for (let i = 0; i < 5; i += 1) {
  if (!await fireNext(1500)) fail("опрос реестра не пошёл (таймеров 1.5s нет)");
}
if (!bubbleIn(panel)) fail("пузырь пропал во время ожидания сессии");

// --- перерисовка панели не хоронит реплику ---
// Список чатов обновляется сам, панель перерисовывается, и до правки пузырь
// жил только в памяти старой панели: реплика исчезала с экрана.
const panel2 = sandbox.chatPanel("demo", st);
await settle();
if (!bubbleIn(panel2)) {
  fail("после перерисовки панели первая реплика пропала: " + dump(panel2).slice(0, 300));
}
// Молодой пузырь после перерисовки остаётся «отправляется», без причины: срок
// считается от времени отправки из персиста, а оно только что.
if (!dump(panel2).includes("отправляется")) {
  fail("молодой восстановленный пузырь потерял часики: " + dump(panel2).slice(0, 300));
}
// И плашка о подъёме сессии на месте: реплика в полёте, пустота врала бы.
const plate2 = byClass(panel2, "busyrow");
if (!plate2 || plate2.hidden || !dump(plate2).includes("сессия поднимается")) {
  fail("после перерисовки плашка о подъёме пропала");
}

// --- причина считается от времени отправки из персиста ---
// Человек ушёл с панели и вернулся через минуту: таймера той перерисовки, где
// была отправка, больше нет, а причина всё равно обязана появиться. Стенд
// старит запись в хранилище и собирает панель заново.
{
  const key = "devkit.chat.pend.demo/new";
  const recs = JSON.parse(store.get(key) || "[]");
  if (!recs.length || recs[0].state !== "wait") {
    fail("реплика в полёте не легла в персист: " + store.get(key));
  }
  recs[0].born = Date.now() - 70000;
  store.set(key, JSON.stringify(recs));
  const panel3 = sandbox.chatPanel("demo", st);
  await settle();
  if (!await fireNext(0)) fail("переворот созревшего пузыря не запланирован");
  const said3 = dump(panel3).replace(/\s+/g, " ");
  if (!said3.includes("дольше обычного")) {
    fail("у созревшего пузыря нет причины от времени отправки: " + said3.slice(0, 400));
  }
  if (!said3.includes("не доставлено")) fail("созревший пузырь не помечен недоставленным: " + said3.slice(0, 400));
  const plate3 = byClass(panel3, "busyrow");
  if (plate3 && !plate3.hidden) fail("плашка о подъёме мигает поверх причины");
}

// --- ожидание сверх обычного: причина на пузыре, ошибки на экране нет ---
while (await fireNext(1500)) { /* добиваем все быстрые опросы */ }
const everywhere = dump(sandbox.document.body) + dump(panel) + dump(panel2);
if (everywhere.includes("не назвала себя в реестре")) {
  fail("ошибка про реестр показана человеку, а это штатное ожидание");
}

// --- сессия назвалась: лента пришивается без действий человека ---
found = true;
if (!await fireNext(5000)) fail("фонового опроса (5s) после минуты нет");
await settle();
if (!String(sandbox.location.hash).includes("f00dcafe-0001")) {
  fail("панель не переехала на найденный диалог: " + sandbox.location.hash);
}

// --- протухший адрес: «Чат не найден», а не «чата нет» ---
// Панель попадает сюда сохранённым адресом чата, которого больше нет в списке:
// сессию сняли, либо она так и не родилась (клиент встал на вопросе доверия в
// своём терминале). Различает протухшее и просто старое точечная проба ленты.
store.set("devkit.chat.last", LOST);
sandbox.location.hash = "#demo/chat/" + LOST;
const lostSt = await sandbox.chatState("demo", LOST, board);
if (!lostSt.lost) fail("протухший адрес не помечен: " + JSON.stringify(lostSt));
const lostHead = dump(sandbox.chatHead("demo", lostSt)).replace(/\s+/g, " ");
if (lostHead.includes("чата нет")) fail("шапка протухшего адреса говорит «чата нет»: " + lostHead);
if (!lostHead.includes("Чат не найден")) fail("шапка не назвала находку: " + lostHead);
const lostPanel = sandbox.chatPanel("demo", lostSt);
await settle();
const lostSaid = dump(lostPanel).replace(/\s+/g, " ");
if (!lostSaid.includes("больше нет")) fail("в панели нет причины про снятый чат: " + lostSaid);
const lostTa = (function find(node) {
  if (node.tagName === "TEXTAREA") return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && find(kid);
    if (got) return got;
  }
  return null;
})(lostPanel);
if (!lostTa || !lostTa.disabled) fail("ввод в потерянный чат не погашен");
// Память панели вычищена: следующий вход в проект не вернёт «Чат не найден».
if (store.get("devkit.chat.last")) {
  fail("протухший адрес остался в памяти: " + store.get("devkit.chat.last"));
}

// --- старый разговор глубже списка не хоронится ---
const deepSt = await sandbox.chatState("demo", DEEP, board);
if (deepSt.lost) fail("старый разговор с транскриптом записан в потерянные");
const deepHead = dump(sandbox.chatHead("demo", deepSt)).replace(/\s+/g, " ");
if (deepHead.includes("чата нет") || deepHead.includes("Чат не найден")) {
  fail("шапка старого разговора хоронит его: " + deepHead);
}
if (!deepHead.includes("чат " + DEEP.slice(0, 8))) {
  fail("старый разговор не подписан своим ID: " + deepHead);
}

console.log("ok: новый чат называется новым, лента без приписки, первая реплика живёт до пришивания, протухший адрес назван находкой");
