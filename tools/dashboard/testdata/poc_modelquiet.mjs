// Стенд молчаливой смены модели (ветка poc-chat).
//
// Про смену модели разговор говорит сам: в ленту ложится разделитель «модель
// изменена: fable -> opus», его пишет ручка модели в журнал разговора. Карточка
// поверх экрана повторяла это третьим голосом, и человек её отверг. Предмет
// стенда это тишина удачи при живой смене: запросы уходят прежним порядком, а
// карточки не всплывает ни одной. Отказ при этом остаётся видимым, и карточки
// других действий стенд сторожит тут же, чтобы тишина не расползлась на них.
//
// Зовётся: node testdata/poc_modelquiet.mjs static/app.js

import { makeSandbox, settle, dump, tag, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const chats = [
  { id: "aaaa1111-1111", title: "Выполни XR-1", mtime: "2026-08-13T10:02:00+03:00",
    tasks: ["XR-1"], model: "fable", liveModel: "fable", own: true,
    tmux: "chat-XR-1-1", state: "live", tree: "xr-1", idle: true },
  { id: "bbbb2222-2222", title: "Верни XR-1 на доработку", mtime: "2026-08-12T09:00:00+03:00",
    tasks: ["XR-1"], model: "opus", state: "dead" },
];

const models = [
  { model: "sonnet", tier: "base", harness: "claude-code" },
  { model: "opus", tier: "pro", harness: "claude-code", default: true },
  { model: "fable", tier: "max", harness: "claude-code" },
];

const board = { sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "дашборд без дёрганья", sect: "in-progress" },
] }] };

// Отказ ручки модели включается стендом: до него сервер отвечает удачей.
let modelRefuses = false;
const posted = [];
const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push(path);
    if (path.endsWith("/model")) {
      if (modelRefuses) {
        return { raw: { status: 502, statusText: "Bad Gateway",
          text: '{"error":"модель не записалась: диск только на чтение"}' } };
      }
      return { message: "модель чата теперь sonnet: она возьмётся на следующем подъёме" };
    }
    if (path.endsWith("/stop")) return { way: "drop", tmux: "chat-XR-1-1" };
    if (path.endsWith("/say")) return { way: "resume", tmux: "chat-XR-1-2" };
    if (path.endsWith("/runs")) return { message: "конвейер XR-1 поднят" };
    return {};
  }
  if (path.includes("/chats")) return { chats, models };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", bin: "claude", default: true }] };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  return {};
});

const flashes = () => byId.get("flashes") || { children: [] };
const cards = () => Array.from(flashes().children || []);
const wipe = () => { flashes().children = []; };

sandbox.location.hash = "#demo";

// --- живая смена: запросы прежние, карточки нет ---
{
  const st = await sandbox.chatState("demo", "aaaa1111-1111", board);
  const sel = tag(sandbox.chatPanel("demo", st), "SELECT");
  if (!sel) fail("выбора модели в панели нет вовсе");
  wipe();
  const was = posted.length;
  sel.value = "sonnet";
  sel.handlers.change({});
  await settle();
  const tail = posted.slice(was);
  const want = ["/api/projects/demo/chats/aaaa1111-1111/model",
    "/api/projects/demo/chats/aaaa1111-1111/stop",
    "/api/projects/demo/chats/aaaa1111-1111/say"];
  if (JSON.stringify(tail.slice(0, 3)) !== JSON.stringify(want)) {
    fail("живая смена модели пошла не тем порядком: " + JSON.stringify(tail));
  }
  if (cards().length) {
    fail("удачная смена модели всплыла карточкой: " + dump(flashes()));
  }
}

// --- мёртвый разговор: запись молча, разделитель скажет сам ---
{
  const st = await sandbox.chatState("demo", "bbbb2222-2222", board);
  const sel = tag(sandbox.chatPanel("demo", st), "SELECT");
  wipe();
  const was = posted.length;
  sel.value = "sonnet";
  sel.handlers.change({});
  await settle();
  if (!posted.slice(was).some((p) => p.endsWith("/model"))) {
    fail("выбор модели мёртвого разговора не записался: " + JSON.stringify(posted.slice(was)));
  }
  if (cards().length) {
    fail("запись модели мёртвого разговора всплыла карточкой: " + dump(flashes()));
  }
}

// --- новый чат: выбор запоминается молча ---
{
  const st = await sandbox.chatState("demo", "new:XR-1", board);
  if (st.sid) fail("у нового чата откуда-то взялась сессия: " + st.sid);
  const sel = tag(sandbox.chatPanel("demo", st), "SELECT");
  wipe();
  sel.value = "sonnet";
  sel.handlers.change({});
  await settle();
  if (cards().length) fail("выбор модели нового чата всплыл карточкой: " + dump(flashes()));
}

// --- отказ виден: разделителя на него не будет ---
{
  modelRefuses = true;
  const st = await sandbox.chatState("demo", "aaaa1111-1111", board);
  const sel = tag(sandbox.chatPanel("demo", st), "SELECT");
  wipe();
  sel.value = "opus";
  sel.handlers.change({});
  await settle();
  if (!cards().length) fail("отказ ручки модели прошёл молча: сменой это неотличимо от удачи");
  if (!dump(flashes()).includes("диск только на чтение")) {
    fail("карточка отказа не назвала причину: " + dump(flashes()));
  }
  modelRefuses = false;
}

// --- всплывашки других действий на месте ---
{
  wipe();
  await sandbox.startRun("demo", "XR-1", "", "");
  await settle();
  if (!cards().length || !dump(flashes()).includes("конвейер XR-1 поднят")) {
    fail("тишина расползлась на запуск работы: " + dump(flashes()));
  }
}

console.log("poc_modelquiet: ok");
