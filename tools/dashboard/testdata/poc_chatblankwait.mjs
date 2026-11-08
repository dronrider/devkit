// Стенд первой реплики заготовки при живом ожидании строки (DK-1655).
//
// Живой случай: у задачи шёл разговор с живой сессией-носителем, строка стояла
// с вопросом. Человек завёл с этой задачей новый чат, дашборд пообещал «сессию
// поднимет первая реплика», а написанное из панели заготовки уехало во вход
// задачи и досталось прежнему носителю. Дорога задачи в chatWay стояла раньше
// ветки подъёма, а живость входа она смотрела у самой панели, у которой входа
// не бывает никогда.
//
// Предмет стенда: первая реплика заготовки поднимает её сессию, ручка задачи
// из панели не зовётся, а подсказка поля не обещает безадресной строки. Дорога
// задачи при этом остаётся себе у панели без свежей заготовки.
//
// Зовётся: node testdata/poc_chatblankwait.mjs static/app.js

import { makeSandbox, settle, tag, deepBtn, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Строка стоит с вопросом, а разговор задачи давно кончился: то самое ожидание
// строки, что перебивало подъём.
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-9", title: "починка чата задачи", sect: "in-progress",
    waiting: { state: "ждёт ответа", source: "ask", note: "спросил агент",
      questions: ["поднимать ли новый чат?"] } },
] }] };
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];

// Идентификатор записи держит вид «blank-<hex>» настоящего сервера: короткий
// «blank-1» разбирается как задача доски и уводил бы стенд мимо предмета
// проверки.
const BLANK = "blank-9f1c4a2e";
const chats = [
  { id: BLANK, project: "demo", blank: true, state: "not-started", idle: true,
    model: "opus", mtime: new Date().toISOString(), tasks: ["XR-9"] },
  { id: "sess-old", project: "demo", title: "разговор задачи", state: "dead",
    tmux: "task-XR-9", idle: true, model: "opus", tasks: ["XR-9"] },
];

const bodies = {};
const posted = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push(path);
    if (String(path).endsWith("/chats")) {
      bodies[path] = init.body ? JSON.parse(init.body) : null;
      return { tmux: "chat-9", model: "opus" };
    }
    return {};
  }
  if (path.includes("/chats")) return { chats, models, days: 3, older: false };
  if (path.endsWith("/chat")) return {};
  return {};
});

// --- заготовка с задачей при живом ожидании строки ---
sandbox.location.hash = "#demo/chat/" + BLANK;
const st = await sandbox.chatState("demo", BLANK, board);
if (st.blank !== BLANK || !st.fresh) {
  fail("панель не встала на заготовку: " + JSON.stringify([st.blank, st.fresh]));
}
if (st.task !== "XR-9") fail("заготовка потеряла свою задачу: " + st.task);
if (!st.wait || !st.wait.state) fail("ожидание строки не доехало до панели");

// Подъём старше ожидания строки: человек завёл новый чат намеренно.
const way = sandbox.chatWay(st);
if (way.kind !== "new" || way.off) {
  fail("первая реплика заготовки уходит не подъёмом: " + JSON.stringify(way));
}

// --- отправка: подъём зовёт свою запись, ручка задачи молчит ---
{
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const ta = tag(panel, "TEXTAREA");
  if (!ta) fail("в панели заготовки нет поля ввода");
  if (ta.disabled) fail("поле ввода заготовки заперто: " + ta.placeholder);
  if (String(ta.title).includes("безадресной строкой")) {
    fail("подсказка поля обещает вход задачи вместо подъёма: " + ta.title);
  }
  ta.value = "поднимись и разбери вопрос строки";
  ta.handlers.input();
  await settle();
  const send = deepBtn(panel, "Отправить");
  if (!send) fail("в панели нет кнопки отправки: " + dump(panel).slice(0, 300));
  send.handlers.click({ stopPropagation: () => {} });
  await settle();
  const taskSaid = posted.filter((p) => p.includes("/tasks/XR-9/message"));
  if (taskSaid.length) {
    fail("реплика ушла ручкой задачи во вход разговора: " + JSON.stringify(taskSaid));
  }
  const lift = bodies["/api/projects/demo/chats"];
  if (!lift || lift.chat !== BLANK) {
    fail("подъём не назвал запись заготовки: " + JSON.stringify(lift));
  }
  if (lift.id !== "XR-9") {
    fail("подъём потерял привязку к задаче: " + JSON.stringify(lift));
  }
  if (lift.text !== "поднимись и разбери вопрос строки") {
    fail("реплика не уехала подъёмом: " + JSON.stringify(lift));
  }
}

// --- дверь задачи без заготовки: дорога задачи остаётся себе ---
{
  const at = chats.findIndex((c) => c.id === BLANK);
  if (at >= 0) chats.splice(at, 1);
  const door = await sandbox.chatState("demo", "XR-9", board);
  if (door.sid !== "sess-old") fail("дверь задачи не открыла свой разговор: " + door.sid);
  const doorWay = sandbox.chatWay(door);
  if (doorWay.kind !== "task") {
    fail("дорога задачи пропала у панели без заготовки: " + JSON.stringify(doorWay));
  }
}

console.log("poc_chatblankwait: ok");
