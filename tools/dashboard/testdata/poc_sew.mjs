// Стенд пришивания застрявшего нового чата (живой случай 29fc49de, tmux
// chat-DK-397-2): первая реплика уходила в чат, которого ещё не было, клиент
// вставал на вопросе в своём терминале, сессия рождалась уже после ухода
// человека, а панель возвращалась на эфемерный адрес new с held-пузырём и
// молчала, хотя транскрипт давно жив. Проверяется дорога целиком: имя tmux
// подъёма ложится в персист реплики, панель на адресе new пришивает себя к
// родившемуся диалогу по этому имени либо по самой первой реплике, а пока
// сессии нет, опрашивает реестр и переезжает, как только та назовётся.
//
// Зовётся: node testdata/poc_sew.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const board = { sections: [] };
const ECHO = "devkit.chat.pend.demo/new";
const SID = "feedbeef-0001";

function findTa(node) {
  if (node.tagName === "TEXTAREA") return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && findTa(kid);
    if (got) return got;
  }
  return null;
}

// --- имя tmux подъёма ложится в персист реплики ---
{
  const { sandbox, store } = makeSandbox(app, (path, init) => {
    if (path.includes("/chats") && init && init.method === "POST") {
      return { tmux: "chat-9", model: "opus", tree: "/x" };
    }
    if (path.includes("/chats")) return { chats: [], models: [] };
    if (path.includes("/sessions/")) return { items: [], start: true };
    return {};
  });
  sandbox.location.hash = "#demo/chat/new";
  const st = await sandbox.chatState("demo", "new", board);
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const ta = findTa(panel);
  if (!ta) fail("поля ввода нет");
  ta.value = "первая реплика ушла в чат которого ещё не было";
  ta.handlers.keydown({ key: "Enter", preventDefault: () => {} });
  await settle();
  const recs = JSON.parse(store.get(ECHO) || "[]");
  if (!recs.length || recs[0].tmux !== "chat-9") {
    fail("имя tmux подъёма не легло в персист реплики: " + store.get(ECHO));
  }
}

// --- возврат на адрес new: диалог уже в списке, панель переезжает сама ---
{
  const { sandbox, store, moves } = makeSandbox(app, (path) => {
    if (path.includes("/chats")) return { chats: [
      { id: SID, state: "live", tmux: "chat-9", title: "первая реплика ушла в чат" },
    ], models: [] };
    if (path.includes("/sessions/")) return { items: [], start: true };
    return {};
  });
  store.set(ECHO, JSON.stringify([{ text: "первая реплика ушла в чат которого ещё не было",
    wire: "первая реплика ушла в чат которого ещё не было", born: Date.now() - 300000,
    state: "held", why: "ждёт", tmux: "chat-9" }]));
  sandbox.location.hash = "#demo/chat/new";
  const st = await sandbox.chatState("demo", "new", board);
  if (st.sid !== SID || st.fresh) {
    fail("панель не пришилась к родившемуся диалогу по имени tmux: " + JSON.stringify(st.sid));
  }
  if (!String(sandbox.location.hash).includes(SID)) {
    fail("адрес панели остался эфемерным: " + sandbox.location.hash);
  }
  if (store.get(ECHO)) fail("память адреса подъёма не вычищена: " + store.get(ECHO));
  // Лента открывается живым sid и показывает транскрипт, а не заглушку нового
  // чата: в этом и была немота.
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const said = dump(panel).replace(/\s+/g, " ");
  if (said.includes("напишите первую реплику")) {
    fail("пришитая панель всё ещё зовёт написать первую реплику: " + said.slice(0, 200));
  }
}

// --- записи прежней версии без tmux пришиваются по первой реплике ---
{
  // Живой случай 29fc49de как он есть: заголовок строки это summary харнеса
  // и с первой репликой не совпадает, сверка обязана идти по полю first.
  const { sandbox, store } = makeSandbox(app, (path) => {
    if (path.includes("/chats")) return { chats: [
      { id: SID, state: "live", tmux: "chat-DK-397-2",
        title: "Анализ обратной связи и план действий",
        first: "Нужно проверить обратную связь которую оставили пользователи курса и..." },
    ], models: [] };
    if (path.includes("/sessions/")) return { items: [], start: true };
    return {};
  });
  store.set(ECHO, JSON.stringify([{
    text: "Нужно проверить обратную связь которую оставили пользователи курса и составить план действий",
    born: Date.now() - 300000, state: "held", why: "ждёт" }]));
  sandbox.location.hash = "#demo/chat/new";
  const st = await sandbox.chatState("demo", "new", board);
  if (st.sid !== SID) fail("пришивания по первой реплике нет: " + JSON.stringify(st.sid));
  // Короткая реплика («да») диалог не усыновляет: сверка требует длины.
  store.set(ECHO, JSON.stringify([{ text: "да", born: Date.now(), state: "held" }]));
  sandbox.location.hash = "#demo/chat/new";
  const st2 = await sandbox.chatState("demo", "new", board);
  if (st2.sid) fail("короткая реплика пришила чужой диалог: " + st2.sid);
}

// --- сессии ещё нет: панель опрашивает реестр и переезжает, когда та назовётся ---
{
  let born = false;
  const { sandbox, store, timers } = makeSandbox(app, (path) => {
    if (path.includes("/chats?tmux=")) {
      return { chats: born ? [{ id: SID, state: "live", tmux: "chat-9" }] : [] };
    }
    if (path.includes("/chats")) return { chats: [], models: [] };
    if (path.includes("/sessions/")) return { items: [], start: true };
    return {};
  });
  store.set(ECHO, JSON.stringify([{ text: "первая реплика ушла в чат которого ещё не было",
    born: Date.now() - 300000, state: "held", why: "ждёт", tmux: "chat-9" }]));
  sandbox.location.hash = "#demo/chat/new";
  const st = await sandbox.chatState("demo", "new", board);
  if (st.sid) fail("панель пришилась к сессии, которой нет: " + st.sid);
  sandbox.chatPanel("demo", st);
  await settle();
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
  if (!await fireNext(2000)) fail("опроса реестра с панели new нет (таймеров 2s нет)");
  if (String(sandbox.location.hash).includes(SID)) fail("панель уехала до рождения сессии");
  born = true;
  if (!await fireNext(2000)) fail("второго захода опроса нет");
  if (!String(sandbox.location.hash).includes(SID)) {
    fail("родившаяся сессия не пришила панель: " + sandbox.location.hash);
  }
  if (store.get(ECHO)) fail("память адреса подъёма пережила пришивание: " + store.get(ECHO));
}

console.log("ok: имя tmux в персисте реплики, возврат на new пришивается по имени и по первой " +
  "реплике, короткая реплика чужого не усыновляет, не родившаяся сессия доезжает опросом");
