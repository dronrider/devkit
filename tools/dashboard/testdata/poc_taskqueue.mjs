// Стенд дожима реплики во вход задачи (POC DK-397, ветка poc-chat; кнопки сняты
// задачей DK-1011).
//
// Живой случай DK-466, продолжение: человек написал в панель заблокированной
// задачи, реплика легла в очередь, он нажал повтор, и панель опустела совсем.
// Сервер при этом отработал верно: строка уже лежала в чате, и второй он не
// завёл, а вот ответ об этом уходил без признака недоставки, и панель считала
// реплику доставленной. Кнопки повтора и отмены с тех пор ушли: неушедшее
// дожимает сам дашборд.
//
// Предмет стенда со стороны экрана: дожим неушедшей реплики второй строки в
// очереди задачи не заводит, пузырь остаётся на месте со словами про очередь и
// таймеры панели переживает.
//
// Зовётся: node testdata/poc_taskqueue.mjs static/app.js

import { makeSandbox, settle, tag, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const QUEUE = "реплика уже лежит в очереди задачи и ждёт первого хода её сессии";
const WHY = "работа по задаче не идёт, отвечать некому, и реплика ждёт во входе задачи";
const SAID = "А почему задача заблокирована?";

const board = { sections: [{ key: "blocked", rows: [
  { id: "DK-466", title: "паттерн диспетчера-агента", sect: "blocked",
    block: "нужен ответ пользователя",
    waiting: { state: "ждёт ответа", note: "блок строки", questions: ["почему заблокирована"] } },
] }] };

// Очередь задачи живёт в стенде списком строк: сервер кладёт реплику раз, а
// дожим узнаёт свою копию и второй строки не заводит. Первый заход при этом
// падает связью: ровно так реплика и становится неушедшей.
const queue = [];
const calls = [];
let refuse = true;
const { sandbox, timers } = makeSandbox(app, (path, init) => {
  const way = (init && init.method) || "GET";
  if (path.includes("/tasks/DK-466/message")) {
    const sent = JSON.parse((init && init.body) || "{}");
    calls.push({ way, text: String(sent.text || "") });
    if (refuse) {
      refuse = false;
      return { raw: { status: 502, statusText: "Bad Gateway",
        text: JSON.stringify({ error: "связи с дашбордом нет" }) } };
    }
    const repeat = queue.includes(sent.text);
    if (!repeat) queue.push(sent.text);
    return { task: "DK-466", chat: "task-DK-466", undelivered: true,
      repeat: repeat || undefined,
      why: repeat ? QUEUE : WHY,
      message: repeat ? "такая реплика уже лежит в чате task-DK-466" : "реплика легла в чат" };
  }
  if (path.includes("/pulse")) return { state: "waiting", count: 0, waiting: 1, parked: true };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});

const st = await sandbox.chatState("demo", "DK-466", board);
const panel = sandbox.chatPanel("demo", st);
const ta = tag(panel, "TEXTAREA");
if (!ta) fail("поля ввода в панели нет: " + dump(panel).slice(0, 300));
ta.value = SAID;
deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
await settle();

// --- отказ связи: реплика неушедшая, текст человека при ней ---
{
  if (queue.length) fail("упавшая отправка всё-таки легла в очередь: " + JSON.stringify(queue));
  const said = dump(panel);
  if (!said.includes(SAID)) fail("отказ унёс с собой текст человека: " + said.slice(0, 300));
  if (!said.includes("не ушло")) fail("отказ не назван в пузыре: " + said.slice(0, 300));
  for (const word of ["повторить", "отменить"]) {
    if (deepBtn(panel, word)) fail("у неушедшей реплики осталась кнопка «" + word + "»");
  }
}

// --- дожим уносит реплику сам и кладёт её в очередь задачи ---
{
  for (let i = 0; i < 4; i += 1) {
    for (const t of timers.splice(0)) t.fn();
    await settle();
  }
  if (queue.length !== 1) fail("дожим не довёз реплику до очереди задачи: " + JSON.stringify(queue));
  const said = dump(panel);
  if (!said.includes(SAID)) {
    fail("после дожима панель опустела, текст человека пропал: " + said.slice(0, 400));
  }
  if (!said.includes(WHY)) {
    fail("пузырь не назвал очередь задачи: " + said.slice(0, 400));
  }
}

// --- второй заход дожима второй строки не заводит ---
{
  for (let i = 0; i < 4; i += 1) {
    for (const t of timers.splice(0)) t.fn();
    await settle();
  }
  if (queue.length !== 1) fail("дожим завёл вторую строку в очереди: " + JSON.stringify(queue));
  const said = dump(panel);
  if (!said.includes(SAID)) {
    fail("пузырь снялся по таймеру, текст человека потерялся: " + said.slice(0, 400));
  }
  if (calls.some((c) => c.way === "DELETE")) {
    fail("панель зачем-то сняла строку из очереди задачи: " + JSON.stringify(calls));
  }
}

console.log("poc_taskqueue: дожим сам довозит реплику до очереди задачи, второй строки " +
  "не заводит, пузырь стоит со словами про очередь и без кнопок");
