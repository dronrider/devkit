// Стенд связи цели с задачами через основные чаты (DK-1431).
//
// Живой случай: решение цели «уехало в ether» и до диспетчеров задач не
// доехало, а вопрос диспетчера задачи цели был некуда задать: у цели и задач
// основные чаты жили порознь, и переход между ними шёл глазами по доске.
//
// Предмет стенда:
//   носитель цели из журнала назначений открывает виток, пока цикл идёт:
//   панель стоит на транскрипте витка, и реплика уходит ручкой цели,
//   а не резюмом витка (LLD DK-1422, DK-938);
//   строка журнала кончившегося витка дверь цели не запирает: стоящая цель
//   поднимает сессию первой репликой, как у задачи (DK-1009);
//   шапка чата задачи несёт чип цели, и нажатие открывает основной чат той
//   записи (решение 6). Шапка цели задачи не перечисляет: поверхность чата
//   несёт только разговор, у служебных ручек записей нет в нём плашек и
//   полей, прямая реплика остаётся в поле разговора (DK-1484).
//
// Зовётся: node testdata/poc_goalmain.mjs static/app.js

import { makeSandbox, settle, tag, byClass, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-100", title: "Цель: пробный цикл", sect: "in-progress" },
  { id: "XR-004", title: "Первая задача", sect: "in-progress" },
  { id: "XR-003", title: "Вторая задача", sect: "backlog" },
] }] };
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const turn = "7eaa707c-0000-4000-8000-000000000001";
const oldTurn = "7eaa707c-0000-4000-8000-000000000009";
const taskChat = "dddd4444-0000-4000-8000-000000000004";
const chats = [
  // Живой разговор задачи XR-004: её основной чат.
  { id: taskChat, project: "demo", title: "работа над XR-004", state: "live", idle: false,
    tasks: ["XR-004"], mtime: "2026-09-11T12:00:00+03:00" },
];
// Ответы ручки адреса по записям: у задачи цель, у цели задачи.
const mains = {};
const setMain = (id, main) => { mains[id.toUpperCase()] = main; };

// Всё, что панель отправила: по этому и судит стенд.
const posts = [];
const opens = [];

const { sandbox } = makeSandbox(app, (path, init) => {
  const p = String(path);
  const way = init && init.method ? init.method : "GET";
  if (way === "POST") {
    posts.push({ path: p, body: init.body ? JSON.parse(init.body) : null });
    if (p.includes("/goals/")) {
      const text = init.body ? JSON.parse(init.body).text : "";
      return { id: "XR-100", line: "- 2026-09-11 12:00, из дашборда: " + text,
        message: "сообщение легло во «Входящие» файла цели XR-100" };
    }
    if (p.endsWith("/chats")) return { way: "new", tmux: "chat-XR-100-1", model: "opus" };
    return { way: "socket", pid: 1, where: "живая сессия" };
  }
  const chatMatch = p.match(/\/tasks\/([^/?]+)\/chat$/);
  if (chatMatch) {
    const main = mains[chatMatch[1].toUpperCase()];
    return main || {};
  }
  if (p.includes("/goals/")) return { id: "XR-100", pending: [], delivered: [], live: true };
  if (p.includes("/sessions/")) {
    const sid = p.slice(p.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (p.includes("/chats")) return { chats, models, days: 3, older: false };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});
await settle();

const text = (node) => JSON.stringify(dump(node));
const say = (panel, words) => {
  tag(panel, "TEXTAREA").value = words;
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
};
const sent = (from) => posts.slice(from);

// --- идущая цель: носитель из журнала открывает виток ---
// Журнал называет виток, работа цели идёт: панель открывает транскрипт витка и
// остаётся у ручки цели, а /say не поднимает второго агента рядом с циклом.
setMain("XR-100", { id: "XR-100", project: "demo", chat: turn, source: "работа",
  since: "2026-09-11 12:00", tasks: ["XR-004", "XR-003"] });
const runs = [{ id: "XR-100", kind: "goal", via: "tmux", session: turn, live: "busy" }];
let st = await sandbox.chatState("demo", "XR-100", board, runs);
if (st.sid !== turn) fail("журнальный носитель не открыл виток: " + JSON.stringify(st.sid));
if (!st.goal) fail("панель на витке из журнала ушла с ручки цели: реплика подняла бы второго агента");
let panel = sandbox.chatPanel("demo", st);
await settle();
let from = posts.length;
say(panel, "ответ витку из журнала");
await settle();
let got = sent(from);
if (got.some((p) => p.path.includes("/say"))) {
  fail("реплика витку из журнала ушла в /say: " + JSON.stringify(got));
}
if (!got.some((p) => p.path === "/api/projects/demo/goals/XR-100/message")) {
  fail("реплика витку из журнала не легла ручкой цели: " + JSON.stringify(got));
}

// --- стоящая цель: строка журнала не запирает дверь ---
// Журнал ещё называет кончившийся виток, а цикл стоит: дверь ведёт дорогой
// задачи, и первая реплика поднимает свою сессию с привязкой к цели (DK-1009).
setMain("XR-100", { id: "XR-100", project: "demo", chat: oldTurn, source: "работа",
  since: "2026-09-10 12:00", tasks: ["XR-004", "XR-003"] });
st = await sandbox.chatState("demo", "XR-100", board, []);
if (st.sid === oldTurn) fail("стоящая цель открыла транскрипт кончившегося витка: " + st.sid);
if (st.goal) fail("стоящая цель осталась на ручке цели: дверь ведёт дорогой задачи");
panel = sandbox.chatPanel("demo", st);
await settle();
from = posts.length;
say(panel, "прочитай, когда поднимешься");
await settle();
got = sent(from);
if (!got.some((p) => p.path === "/api/projects/demo/chats")) {
  fail("первая реплика стоящей цели не подняла сессию: " + JSON.stringify(got));
}

// --- шапка называет одну запись, в чате только разговор ---
// Переход к цели это навигация по записям, а не перенос назначения (решение 6):
// шапка чата задачи несёт чип цели. Шапка цели задачи не перечисляет, состав
// виден на экране цели. Служебным ручкам записей в разговоре места нет, прямая
// реплика остаётся в поле разговора (DK-1484).
from = posts.length;
let head = text(sandbox.chatHead("demo", st));
if (head.includes("XR-004") || head.includes("XR-003")) {
  fail("шапка цели перечисляет задачи: " + head);
}
panel = sandbox.chatPanel("demo", st);
await settle();
if (!st.isGoal) fail("запись цели не узналась целью: " + JSON.stringify(st.isGoal));
if (byClass(panel, "cbindin")) fail("в панели цели появилось служебное поле ввода");
if (deepBtn(panel, "Довести задачам")) fail("в панели цели стоит плашка решения задачам");
setMain("XR-004", { id: "XR-004", project: "demo", chat: taskChat, source: "работа",
  since: "2026-09-11 12:00", goal: "XR-100" });
st = await sandbox.chatState("demo", "XR-004", board, []);
head = text(sandbox.chatHead("demo", st));
if (!head.includes("цель XR-100")) fail("шапка задачи не назвала цель: " + head);
panel = sandbox.chatPanel("demo", st);
await settle();
if (byClass(panel, "cbindin")) fail("в панели задачи появилось служебное поле ввода");
if (deepBtn(panel, "Диспетчеру цели")) fail("в панели задачи стоит плашка строки диспетчеру цели");
const extra = sent(from).filter((p) => p.path.endsWith("/decide") || p.path.endsWith("/relay"));
if (extra.length) fail("служебные ручки получили вызов из панели: " + JSON.stringify(extra));

console.log("poc_goalmain: ok");
