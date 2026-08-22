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

const { sandbox, timers } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats") && init && init.method === "POST") {
    return { tmux: "chat-1", model: "opus", tree: "/x" };
  }
  if (path.includes("/chats?tmux=")) {
    return { chats: found ? [{ id: "f00dcafe-0001", state: "live" }] : [] };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
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

console.log("ok: новый чат называется новым, лента без приписки, первая реплика живёт до пришивания");
