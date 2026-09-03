// Стенд причин гашения ввода (DK-727).
//
// Решение 6 LLD DK-430 держало четыре причины, по которым поле ввода панели
// гаснет, и четвёртой стоял кончившийся разговор без задачи. Код к тому
// времени уехал: реплика в такой разговор уезжает резюмом, и поле у него
// живое. Решение пересмотрено, а стенд держит пересмотренное обещание:
// перебирает состояния панели одно за другим и сверяет с каждым, заперто поле
// или открыто. Гасят два состояния, подъём сессии и адрес без транскрипта.
// Возврат четвёртой причины валит стенд, а не приёмку.
//
// Зовётся: node testdata/poc_chatnotask.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const FREE = "aaaa1111-1111-4111-8111-000000000001";
const TASKED = "aaaa1111-1111-4111-8111-000000000002";
const LIVE = "aaaa1111-1111-4111-8111-000000000003";
const GONE = "aaaa1111-1111-4111-8111-000000000004";
const LOST = "aaaa1111-1111-4111-8111-000000000005";

const row = { id: "XR-4", title: "Начатая задача", sect: "in-progress", r: 31,
  cost: "-", type: "task", accept: "agent" };
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [row] }] };

// Список разговоров машины: свободный кончившийся, кончившийся по задаче,
// живой и снятый вместе с отданным именем окна.
const chats = [
  { id: FREE, project: "demo", title: "разговор без задачи", state: "dead",
    tmux: "chat-7", idle: true, own: true, note: "свободный чат" },
  { id: TASKED, project: "demo", title: "разговор задачи", state: "dead",
    tmux: "chat-XR-4-1", tasks: ["XR-4"], idle: true, own: true },
  { id: LIVE, project: "demo", title: "живой разговор", state: "live",
    tmux: "chat-8", own: true },
  { id: GONE, project: "demo", title: "снятый разговор", state: "dead",
    tmux: "chat-9", own: true, gone: "имя окна отдано другому разговору",
    goneTo: LIVE },
];

const { sandbox } = makeSandbox(app, (path) => {
  const p = String(path);
  if (p.includes("/sessions/" + LOST)) {
    return { raw: { status: 404, statusText: "Not Found", text: '{"error": "транскрипта нет"}' } };
  }
  if (p.includes("/sessions/")) return { session: "", head: {}, items: [], total: 0 };
  if (p.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (p.endsWith("/board")) return { board, works: [] };
  return {};
});
await settle();

const findTa = (node) => {
  if (node.tagName === "TEXTAREA") return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && findTa(kid);
    if (got) return got;
  }
  return null;
};

// Поле ввода панели этого состояния: заперто оно или открыто и что говорит
// человеку. Спрашивается у самой панели, а не у вида доставки: гасит поле
// именно она, и сверять надо то, что увидит человек.
async function fieldOf(addr) {
  const st = await sandbox.chatState("demo", addr, board, []);
  const way = sandbox.chatWay(st);
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const ta = findTa(panel);
  if (!ta) fail("в панели разговора " + addr + " нет поля ввода");
  return { st, way, ta, said: dump(panel).replace(/\s+/g, " ") };
}

// --- кончившийся разговор без задачи: поле живое, реплика уедет резюмом ---
{
  const { st, way, ta, said } = await fieldOf(FREE);
  if (st.task) fail("стенд подсунул разговору задачу: " + st.task);
  if (st.lost) fail("стенд не воспроизвёл кончившийся разговор: он вышел протухшим адресом");
  if (way.kind !== "resume") fail("дорога реплики не резюм: " + JSON.stringify(way));
  if (way.off) fail("ввод кончившегося разговора без задачи погашен: " + JSON.stringify(way));
  if (ta.disabled) fail("поле ввода заперто: " + ta.placeholder);
  if (ta.placeholder !== "Написать агенту...") {
    fail("поле ввода живого разговора называет причину запрета: " + ta.placeholder);
  }
  // Молчания тут нет: панель говорит словами, что сессии за разговором не
  // осталось и поднимет её сама реплика. Иначе состояние неотличимо от живого.
  if (!said.includes("реплика поднимет её резюмом")) {
    fail("панель молчит о том, что сессии нет: " + said.slice(0, 300));
  }
}

// --- кончившийся разговор задачи: та же дорога и то же живое поле ---
{
  const { st, way, ta } = await fieldOf(TASKED);
  if (st.task !== "XR-4") fail("разговор потерял свою задачу: " + st.task);
  if (way.kind !== "resume") fail("дорога реплики разговора задачи не резюм: " + JSON.stringify(way));
  if (ta.disabled) fail("поле ввода кончившегося разговора задачи заперто: " + ta.placeholder);
}

// --- живой разговор: реплика идёт прямо в процесс ---
{
  const { way, ta } = await fieldOf(LIVE);
  if (way.kind !== "say") fail("дорога реплики живого разговора не прямая: " + JSON.stringify(way));
  if (ta.disabled) fail("поле ввода живого разговора заперто: " + ta.placeholder);
}

// --- снятый разговор с отданным именем окна: поле живое, причина словами ---
{
  const { way, ta, said } = await fieldOf(GONE);
  if (way.kind !== "gone") fail("снятый разговор не назван снятым: " + JSON.stringify(way));
  if (ta.disabled) fail("поле ввода снятого разговора заперто: " + ta.placeholder);
  if (!said.includes("имя окна отдано другому разговору")) {
    fail("причина снятого разговора не показана: " + said.slice(0, 300));
  }
}

// --- первая причина гашения: сессия поднимается ---
{
  const st = await sandbox.chatState("demo", "new:XR-4", board,
    [{ id: "XR-4", kind: "task", session: "task-XR-4", live: "busy" }]);
  if (!st.lift) fail("стенд не воспроизвёл подъём сессии: " + JSON.stringify(st.lift));
  const way = sandbox.chatWay(st);
  if (way.kind !== "lift" || !way.off) fail("подъём сессии не гасит ввода: " + JSON.stringify(way));
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const ta = findTa(panel);
  if (!ta.disabled) fail("поле ввода на время подъёма открыто: реплика подняла бы вторую сессию");
  if (!ta.placeholder.includes("поднимается")) {
    fail("запертое поле не назвало причину: " + ta.placeholder);
  }
}

// --- вторая причина гашения: адреса нет ни в списке, ни на диске ---
{
  const { way, ta } = await fieldOf(LOST);
  if (way.kind !== "lost" || !way.off) fail("протухший адрес не гасит ввода: " + JSON.stringify(way));
  if (!ta.disabled) fail("поле ввода протухшего адреса открыто");
  if (!ta.placeholder.trim()) fail("запертое поле молчит вовсе");
}

// --- других причин гашения нет ---
{
  const kinds = [];
  for (const addr of [FREE, TASKED, LIVE, GONE]) {
    const { way } = await fieldOf(addr);
    if (way.off) fail("состояние " + way.kind + " гасит ввод сверх названных причин");
    kinds.push(way.kind);
  }
  console.log("poc_chatnotask: ok, поле живое у " + kinds.join(", ") +
    "; гасят только lift и lost");
}
