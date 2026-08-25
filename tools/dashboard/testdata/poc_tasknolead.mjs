// Стенд реплики в задачу без ведущей сессии (ветка poc-chat).
//
// Живой случай DK-466: человек написал в панель заблокированной задачи «А
// почему задача заблокирована?». Живой сессии у задачи не было вовсе, реплика
// легла безадресной строкой во вход task-DK-466, панель отчиталась доставкой и
// сняла пузырь через десять секунд, а саму строку подхватила посторонняя живая
// сессия того же чекаута и прочитала вопрос посреди чужого хода.
//
// Предмет стенда: ручка говорит, что ведущей сессии нет, пузырь честно стоит
// недоставленным с причиной, текст человека никуда не девается и переживает
// таймеры, а рядом стоит выход к задаче, откуда поднимают работу.
//
// Зовётся: node testdata/poc_tasknolead.mjs static/app.js

import { makeSandbox, settle, tag, deepBtn, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const WHY = "работа по задаче не идёт, отвечать некому, и реплика ждёт во входе задачи";
const SAID = "А почему задача заблокирована?";

// Строка стоит с вопросом, живых разговоров у задачи нет ни одного.
const board = { sections: [{ key: "blocked", rows: [
  { id: "DK-466", title: "паттерн диспетчера-агента", sect: "blocked",
    block: "нужен ответ пользователя",
    waiting: { state: "ждёт ответа", note: "блок строки", questions: ["почему заблокирована"] } },
] }] };

const posts = [];
const { sandbox, timers } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posts.push({ path, body: init.body ? String(init.body) : "" });
    if (path.includes("/message")) {
      return { task: "DK-466", chat: "task-DK-466", undelivered: true, why: WHY,
        message: "реплика легла в чат task-DK-466 и ждёт там: " + WHY };
    }
    return {};
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

// --- реплика легла во вход задачи, а не в чужой разговор ---
{
  const msg = posts.filter((p) => p.path.includes("/tasks/DK-466/message"));
  if (msg.length !== 1) {
    fail("реплика ушла не во вход задачи: " + JSON.stringify(posts));
  }
  const other = posts.filter((p) => !p.path.includes("/tasks/DK-466/message"));
  if (other.length) {
    fail("реплика заодно уехала куда-то ещё: " + JSON.stringify(other));
  }
}

// --- пузырь недоставлен, причина названа, текст на месте ---
{
  const said = dump(panel);
  if (!said.includes("не доставлено")) {
    fail("пузырь отчитался доставкой, которой не было: " + said);
  }
  if (!said.includes(WHY)) fail("причина недоставки не названа словами: " + said);
  if (!said.includes(SAID)) fail("текст человека пропал из панели: " + said);
}

// --- выход к задаче, откуда поднимают работу ---
{
  if (!deepBtn(panel, "поднять работу по задаче")) {
    fail("выхода к задаче у недоставленной реплики нет: " + dump(panel));
  }
  if (!deepBtn(panel, "повторить")) fail("повтора у недоставленной реплики нет");
  if (!deepBtn(panel, "отменить")) fail("отмены у недоставленной реплики нет");
}

// --- пузырь переживает все таймеры панели ---
{
  for (let i = 0; i < 3; i++) {
    for (const t of timers.splice(0)) t.fn();
    await settle();
  }
  const said = dump(panel);
  if (!said.includes(SAID)) {
    fail("пузырь снялся по таймеру, и текст человека потерялся: " + said);
  }
  if (!said.includes("не доставлено")) {
    fail("после таймеров пузырь перестал говорить о недоставке: " + said);
  }
}

console.log("poc_tasknolead: реплика задаче без ведущей сессии стоит недоставленной " +
  "с причиной, текст цел, выход к задаче на месте");
