// Стенд пришивания панели, открытой чатом записи накопителя (ветка poc-chat).
//
// Живой случай пользователя, по шагам: человек заходит в накопитель, жмёт у
// записи «открыть чат», панель открывается пустым чатом, человек понимает, что
// груминга ещё не было, и поднимает разбор. Кружок оживает, а лента остаётся
// пустой до перезагрузки страницы. Пришивание у нас есть, но всё оно было
// заперто на адрес new: чат записи открывается её же ID, панель для такого
// адреса собирается один раз, и опроса реестра там не заводилось вовсе. Сессия
// разбора при этом называет себя в реестре чатов не сразу: список чатов
// собирается из транскриптов, и разговор появляется в нём с первой репликой
// агента.
//
// Предмет стенда: пустая панель записи ждёт сессию разбора и переезжает на неё
// тем же ходом, без перезагрузки. Обе дороги: разбор поднимают при открытой
// панели и панель открывают при идущем разборе.
//
// Зовётся: node testdata/poc_groomsew.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [] };
const ID = "XR-482";
const SESS = "task-XR-482";
const SID = "feedbeef-0482";

// Таймеры мока стоят, пока их не завести рукой: опрос реестра ходит по ним, и
// стенд играет за время.
const fireEvery = (timers, ms) => {
  const fired = new Set();
  return async () => {
    for (let i = 0; i < timers.length; i += 1) {
      if (fired.has(i) || timers[i].ms !== ms) continue;
      fired.add(i);
      timers[i].fn();
      await settle();
      return true;
    }
    return false;
  };
};

// --- разбор поднимают при открытой пустой панели записи ---
{
  let born = false;
  const { sandbox, timers, store } = makeSandbox(app, (path, init) => {
    if (path.includes("/chats?tmux=")) {
      return { chats: born ? [{ id: SID, state: "live", tmux: SESS, tasks: [ID] }] : [] };
    }
    if (path.includes("/chats")) return { chats: [], models: [] };
    if (path.includes("/groom") && init && init.method === "POST") {
      return { id: ID, kind: "task", session: SESS, message: "грумминг " + ID + " поднят" };
    }
    if (path.includes("/sessions/")) return { items: [], start: true };
    return {};
  });

  sandbox.location.hash = "#demo/drafts/chat/" + ID;
  const st = await sandbox.chatState("demo", ID, board);
  if (st.sid) fail("панель нашла разговор, которого ещё нет: " + st.sid);
  if (st.task !== ID) fail("панель открылась без привязки к записи: " + JSON.stringify(st.task));
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  if (!dump(panel).includes("напишите первую реплику")) {
    fail("пустая панель записи молчит не тем: " + dump(panel).replace(/\s+/g, " ").slice(0, 200));
  }

  // Человек поднимает разбор той же кнопкой, что и с накопителя.
  await sandbox.groomDraft("demo", ID);
  await settle();
  // След подъёма остался: по нему моргает кнопка чата записи, пока идёт разбор.
  const lift = store.get("devkit.chat.lift.demo/new:" + ID);
  if (!lift || !String(lift).includes(SESS)) {
    fail("подъём разбора не оставил следа: " + String(lift));
  }

  const tick = fireEvery(timers, 2000);
  if (!await tick()) fail("опроса реестра с панели записи нет: разбор доедет только перезагрузкой");
  if (String(sandbox.location.hash).includes(SID)) fail("панель уехала до рождения сессии");
  born = true;
  if (!await tick()) fail("второго захода опроса нет");
  if (!String(sandbox.location.hash).includes(SID)) {
    fail("поднявшаяся сессия разбора не пришила панель: " + sandbox.location.hash);
  }
}

// --- панель открывают, когда разбор уже идёт ---
{
  let born = false;
  const { sandbox, timers } = makeSandbox(app, (path) => {
    if (path.includes("/chats?tmux=")) {
      return { chats: born ? [{ id: SID, state: "live", tmux: SESS, tasks: [ID] }] : [] };
    }
    if (path.includes("/chats")) return { chats: [], models: [] };
    if (path.includes("/sessions/")) return { items: [], start: true };
    return {};
  });
  // Работа записи видна в tmux сразу, и кружок у строки от неё оживает: имя
  // сессии панель берёт там же, памяти подъёма у неё нет (разбор подняли
  // прошлым заходом или из соседней вкладки). Работы приезжают панели тем же
  // доводом, каким их получает экран.
  const works = [{ id: ID, session: SESS, live: "busy", via: "tmux" }];
  sandbox.location.hash = "#demo/drafts/chat/" + ID;
  const st = await sandbox.chatState("demo", ID, board, works);
  if (st.lift !== SESS) {
    fail("панель не узнала идущую работу записи: " + JSON.stringify(st.lift));
  }
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const said = dump(panel).replace(/\s+/g, " ");
  if (!said.includes("вот-вот назовётся в реестре")) {
    fail("панель при идущем разборе молчит про подъём сессии: " + said.slice(0, 250));
  }
  const tick = fireEvery(timers, 2000);
  born = true;
  if (!await tick()) fail("опроса реестра при идущем разборе нет");
  if (!String(sandbox.location.hash).includes(SID)) {
    fail("идущий разбор не пришил открытую панель: " + sandbox.location.hash);
  }
}

console.log("poc_groomsew: ok");
