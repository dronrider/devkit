// Стенд чата цели (DK-938).
//
// Живой случай: цель DK-902 шла циклом, человек нажал кнопку чата на форме цели
// и попал во вчерашний груминг той же задачи. Над лентой стоял чужой заголовок
// и красная плашка «разговор остановлен», а реплика отсюда подняла бы резюм
// мёртвой сессии и до «Входящих» цели не доехала бы.
//
// Предмет стенда:
//   чат идущего витка (скрытая запись с полем goal) шлёт реплику ручкой цели
//   во «Входящие», а не в /say живой сессии: виток это `claude -p`, и резюм
//   поднял бы второго агента рядом с ним;
//   адрес цели при стоящем цикле чужой груминг не выбирает, шапка называет
//   задачу, плашка говорит, что цикл не идёт, а реплика ложится во «Входящие»;
//   чат человека о цели остаётся своим разговором, и реплика ему идёт в сессию.
//
// Зовётся: node testdata/poc_goalchat.mjs static/app.js

import { makeSandbox, settle, tag, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-100", title: "Цель: пробный цикл", sect: "in-progress", run_chat: "7eaa707c-0000-4000-8000-000000000001" },
  { id: "XR-7", title: "задача, которую трогал виток", sect: "in-progress" },
] }] };
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const chats = [
  // Виток цикла: запись скрытая, трогал он чужую задачу, а цель называет сам.
  { id: "7eaa707c-0000-4000-8000-000000000001", project: "demo", title: "Цикл цели XR-100", state: "live", idle: false,
    hidden: true, goal: "XR-100", tasks: ["XR-7"], mtime: "2026-09-11T12:00:00+03:00" },
  // Вчерашний груминг той же задачи: его и открывала кнопка.
  { id: "ba1e0826-0000-4000-8000-000000000002", project: "demo", title: "Разбор и груминг задачи XR-100", state: "dead",
    tasks: ["XR-100"], mtime: "2026-09-10T12:00:00+03:00" },
  // Разговор человека о цели: живой, свой, без поля goal.
  { id: "cccc3333-0000-4000-8000-000000000003", project: "demo", title: "разговор о цели", state: "live", idle: true,
    tasks: ["XR-100"], mtime: "2026-09-11T11:00:00+03:00" },
];

// Всё, что панель отправила или прочитала: по этому и судит стенд.
const posts = [];
const reads = [];
let live = true;

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
    return { way: "socket", pid: 1, where: "живая сессия" };
  }
  reads.push(p);
  if (p.includes("/goals/")) return { id: "XR-100", pending: [], delivered: [], live };
  if (p.includes("/sessions/")) {
    const sid = p.slice(p.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (p.includes("/chats")) return { chats, models, days: 3, older: false };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});
await settle();

const say = (panel, text) => {
  tag(panel, "TEXTAREA").value = text;
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
};
const sent = (from) => posts.slice(from);
const text = (node) => JSON.stringify(dump(node));

// --- кейс 1-2: цикл жив, кнопка открыла его виток по sid ---
const works = [{ id: "XR-100", kind: "goal", via: "tmux", session: "7eaa707c-0000-4000-8000-000000000001", live: "busy" }];
let st = await sandbox.chatState("demo", "7eaa707c-0000-4000-8000-000000000001", board, works);
if (st.sid !== "7eaa707c-0000-4000-8000-000000000001" || !st.entry) fail("чат витка не открылся по своему sid: " + JSON.stringify(st.sid));
if (st.task !== "XR-100") {
  fail("шапка витка ушла к чужой строке " + JSON.stringify(st.task) + ": цель называет поле goal");
}
let panel = sandbox.chatPanel("demo", st);
await settle();
if (!text(panel).includes("Сообщение уйдёт агенту")) {
  fail("над полем живого витка нет слов про доставку агенту: " + text(panel));
}
if (text(panel).includes("разговор остановлен")) fail("живой виток нарисован остановленным");
let from = posts.length;
say(panel, "ответ на вопрос витка");
await settle();
let got = sent(from);
if (got.some((p) => p.path.includes("/say"))) {
  fail("реплика витку ушла в /say и подняла бы резюм второго агента: " + JSON.stringify(got));
}
const goalPost = got.find((p) => p.path === "/api/projects/demo/goals/XR-100/message");
if (!goalPost || goalPost.body.text !== "ответ на вопрос витка") {
  fail("реплика из чата витка не легла ручкой цели во «Входящие»: " + JSON.stringify(got));
}

// --- кейс 3: цикл не идёт, кнопка ведёт по адресу цели ---
// Живого разговора о цели нет, есть только вчерашний груминг: его кнопка и
// открывала. Живой чат человека адрес цели выбрал бы законно (DK-446), его
// ведёт следующий кусок стенда.
live = false;
const human = chats.splice(2, 1)[0];
const readFrom = reads.length;
st = await sandbox.chatState("demo", "XR-100", board, []);
if (st.sid) fail("адрес цели выбрал чужой чат " + st.sid + ": реплика подняла бы его резюмом");
panel = sandbox.chatPanel("demo", st);
await settle();
if (reads.slice(readFrom).some((p) => p.includes("/sessions/ba1e0826-0000-4000-8000-000000000002"))) {
  fail("панель цели читала ленту чужого груминга");
}
const head = text(sandbox.chatHead("demo", st));
if (head.includes("Разбор и груминг")) fail("шапка чата цели назвала чужой груминг: " + head);
if (!text(panel).includes("Цикл цели не идёт")) {
  fail("стоящий цикл не назван над полем ввода: " + text(panel));
}
from = posts.length;
say(panel, "прочитай, когда поднимешься");
await settle();
got = sent(from);
if (got.length !== 1 || got[0].path !== "/api/projects/demo/goals/XR-100/message") {
  fail("реплика стоящей цели ушла мимо «Входящих»: " + JSON.stringify(got));
}

// --- чат человека о цели остаётся своим разговором ---
// Цель ведут и живым чатом дашборда (DK-446): адрес цели выбирает его, и
// реплика идёт в его сессию.
chats.push(human);
st = await sandbox.chatState("demo", "XR-100", board, []);
if (st.sid !== human.id || st.goal) {
  fail("адрес цели не открыл живой чат человека о ней: " + JSON.stringify([st.sid, st.goal]));
}
st = await sandbox.chatState("demo", "cccc3333-0000-4000-8000-000000000003", board, works);
if (st.goal) fail("чат человека о цели принят за виток: реплика ушла бы мимо его сессии");
panel = sandbox.chatPanel("demo", st);
from = posts.length;
say(panel, "это мне, а не витку");
await settle();
got = sent(from);
if (!got.some((p) => p.path.includes("/chats/cccc3333-0000-4000-8000-000000000003/say"))) {
  fail("реплика в чат человека о цели ушла не в его сессию: " + JSON.stringify(got));
}

console.log("poc_goalchat: ok");
