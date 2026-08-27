// Стенд повтора и отмены недоставленной реплики (POC DK-397, ветка poc-chat).
//
// Живой случай DK-466, продолжение: человек написал в панель заблокированной
// задачи, реплика легла в очередь недоставленной, он нажал повтор, и панель
// опустела совсем. Сервер при этом отработал верно: строка уже лежала в чате, и
// второй он не завёл, а вот ответ об этом уходил без признака недоставки, и
// панель считала реплику доставленной.
//
// Предмет стенда со стороны экрана: повтор оставляет пузырь на месте и говорит
// словами, что реплика уже в очереди, а отмена снимает её не только с экрана,
// но и из самой очереди задачи.
//
// Зовётся: node testdata/poc_taskretry.mjs static/app.js

import { makeSandbox, settle, tag, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const WHY = "работа по задаче не идёт, отвечать некому, и реплика ждёт во входе задачи";
const QUEUE = "реплика уже лежит в очереди задачи и ждёт первого хода её сессии";
const SAID = "А почему задача заблокирована?";

const board = { sections: [{ key: "blocked", rows: [
  { id: "DK-466", title: "паттерн диспетчера-агента", sect: "blocked",
    block: "нужен ответ пользователя",
    waiting: { state: "ждёт ответа", note: "блок строки", questions: ["почему заблокирована"] } },
] }] };

// Очередь задачи живёт в стенде списком строк: сервер кладёт реплику раз, а
// повтор узнаёт свою копию и второй строки не заводит.
const queue = [];
const calls = [];
const { sandbox, timers } = makeSandbox(app, (path, init) => {
  const way = (init && init.method) || "GET";
  if (path.includes("/tasks/DK-466/message")) {
    const sent = JSON.parse((init && init.body) || "{}");
    calls.push({ way, text: String(sent.text || "") });
    if (way === "DELETE") {
      const at = queue.indexOf(sent.text);
      if (at >= 0) queue.splice(at, 1);
      return { task: "DK-466", chat: "task-DK-466", dropped: at >= 0 ? 1 : 0,
        message: at >= 0 ? "реплика снята из очереди задачи"
          : "реплики во входе задачи уже нет: её забрал ход агента" };
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

// --- первая реплика легла в очередь и стоит недоставленной ---
{
  if (queue.length !== 1) fail("реплика не легла в очередь задачи: " + JSON.stringify(queue));
  const said = dump(panel);
  if (!said.includes(SAID)) fail("текста человека в панели нет: " + said.slice(0, 300));
  if (!said.includes("не доставлено")) fail("пузырь отчитался доставкой: " + said.slice(0, 300));
}

// --- повтор оставляет пузырь и говорит, что реплика уже в очереди ---
{
  const again = deepBtn(panel, "повторить");
  if (!again) fail("повтора у недоставленной реплики нет");
  again.handlers.click({ stopPropagation: () => {} });
  await settle();
  const said = dump(panel);
  if (!said.includes(SAID)) {
    fail("после повтора панель опустела, текст человека пропал: " + said.slice(0, 400));
  }
  if (!said.includes("не доставлено")) {
    fail("после повтора пузырь отчитался доставкой, и панель его снимет: " + said.slice(0, 400));
  }
  if (!said.includes("уже лежит в очереди")) {
    fail("повтор не сказал человеку, что реплика уже в очереди: " + said.slice(0, 400));
  }
  if (queue.length !== 1) fail("повтор завёл вторую строку в очереди: " + JSON.stringify(queue));
  // Таймеры панели пузырь тоже переживают: доставленную реплику панель снимает
  // по сроку, а эта не доставлена.
  for (let i = 0; i < 3; i++) {
    for (const t of timers.splice(0)) t.fn();
    await settle();
  }
  if (!dump(panel).includes(SAID)) {
    fail("пузырь снялся по таймеру после повтора: " + dump(panel).slice(0, 400));
  }
}

// --- отмена снимает реплику из самой очереди задачи ---
{
  const undo = deepBtn(panel, "отменить");
  if (!undo) fail("отмены у недоставленной реплики нет");
  undo.handlers.click({ stopPropagation: () => {} });
  await settle();
  const drops = calls.filter((c) => c.way === "DELETE");
  if (drops.length !== 1) {
    fail("отмена не пошла в очередь задачи: " + JSON.stringify(calls));
  }
  if (!drops[0].text.includes(SAID)) {
    fail("отмена сняла не ту реплику: " + JSON.stringify(drops));
  }
  if (queue.length) fail("реплика осталась в очереди задачи: " + JSON.stringify(queue));
  if (dump(panel).includes(SAID)) {
    fail("отменённый пузырь остался на экране: " + dump(panel).slice(0, 300));
  }
}

console.log("poc_taskretry: повтор оставляет пузырь со словами про очередь, " +
  "отмена снимает реплику и из очереди задачи");
