// Стенд кнопки разговоров в шапке (POC DK-397, ветка poc-chat).
//
// Живой случай: «при нажатии кнопки открытия чатов открывается чат открытой
// задачи, по идее эта кнопка просто открывает чат, для открытия чата задачи
// есть отдельная кнопка на ней же» (замечание пользователя). Кнопка шапки
// подхватывала задачу текущего экрана и уводила в её разговор, а обычный
// разговор с экрана задачи было не открыть вовсе.
//
// Предмет стенда: адрес, который кнопка кладёт в хэш. С экрана задачи он такой
// же, как с экрана доски, то есть общий, а номер задачи в него не попадает.
//
// Зовётся: node testdata/poc_chatsbtn.mjs static/app.js

import { makeSandbox, settle, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "DK", sections: [{ key: "in-progress", rows: [
  { id: "DK-606", title: "LLD: приёмы прозы", sect: "in-progress" }] }] };

const { sandbox, byId } = makeSandbox(app, (path) => {
  const p = String(path);
  if (p.includes("/sessions/")) return { items: [], total: 0 };
  if (p.includes("/chats")) return { chats: [], models: [], days: 3 };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});
await settle();

const btn = byId.get("chats");
if (!btn) fail("кнопки разговоров в шапке нет вовсе");

function press(hash) {
  sandbox.location.hash = "#" + hash;
  (btn.handlers.click || btn.onclick)({});
  return String(sandbox.location.hash || "");
}

// Панель это хвост адреса: экран под ней остаётся прежним, а разговор
// называется тем, что стоит после /chat/. Его и смотрим.
function chatOf(hash) {
  const at = hash.indexOf("/chat/");
  return at < 0 ? "" : hash.slice(at + "/chat/".length);
}

// С экрана задачи: разговор открывается общим, номер задачи в него не едет.
const fromTask = chatOf(press("devkit/DK-606"));
if (!fromTask) fail("кнопка шапки не открыла разговор вовсе");
if (fromTask.includes("DK-606")) {
  fail("кнопка шапки увела в чат открытой задачи: " + fromTask);
}

// С экрана доски разговор тот же самый: у кнопки одна дорога на всех экранах.
const fromBoard = chatOf(press("devkit/board"));
if (fromBoard !== fromTask) {
  fail("с доски и с задачи кнопка открывает разное: " + fromBoard + " против " + fromTask);
}

console.log("poc_chatsbtn: ok");
