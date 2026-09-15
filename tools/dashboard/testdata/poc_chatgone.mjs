// Стенд снятого разговора: реплика в чат, чью сессию сняли перезапуском
// работы, уезжает обычной дорогой и поднимает ту же сессию резюмом.
//
// Живой случай DK-1011: человек писал в панель разговора task-DK-974. Работу по
// задаче подняли заново, и имя tmux-сессии реестр отдал новой записи. Панель
// реплику на сервер не отправила вовсе, пузырь встал «не доставлено» с
// кнопками «повторить», «отменить» и «открыть живой чат», а над полем ввода
// стояла та же фраза плашкой. Ручка /say эту дорогу умела с DK-713: разговор
// без имени tmux она продолжает через claude --resume под свежим именем.
//
// Предмет стенда: реплика уходит на /say, сервер отвечает дорогой resume,
// пузыря «не доставлено» и кнопок в панели нет, плашки над полем нет.
//
// Зовётся: node testdata/poc_chatgone.mjs static/app.js

import { makeSandbox, settle, tag, deepBtn, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const DEAD = "aaaa5030-1111-4111-8111-111111111111";
const LIVE = "bbbb5031-2222-4222-8222-222222222222";
const WHY = "сессия разговора снята: работу подняли заново, ответить в ней нечем";
const FRESH = "chat-DK-503-7";

// Снятый разговор приезжает с признаком gone и адресом выхода goneTo: имя его
// tmux-сессии реестр отдал разговору LIVE. Панель по этим полям дорогу реплики
// больше не гасит: процесса у разговора нет, и это ровно тот случай, который
// ручка продолжает резюмом.
const chats = [
  { id: DEAD, title: "Выполни DK-503", mtime: "2026-08-24T13:40:00+03:00",
    tasks: ["DK-503"], model: "opus", liveModel: "opus", own: true,
    state: "dead", tree: "", gone: WHY, goneTo: LIVE },
  { id: LIVE, title: "Выполни DK-503", mtime: "2026-08-24T14:00:00+03:00",
    tasks: ["DK-503"], model: "opus", liveModel: "opus", own: true,
    tmux: "task-DK-503", state: "live", tree: "", idle: true },
];
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const board = { sections: [{ key: "in-progress", rows: [
  { id: "DK-503", title: "расход подписки", sect: "in-progress" },
] }] };

const { sandbox, timers, store } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats, models };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});

// Ответ ручки такой же, каким его даёт сервер на разговор без имени tmux:
// поднят claude --resume под свежим именем, история продолжена.
const posts = [];
const plain = sandbox.fetch;
sandbox.fetch = (path, init) => {
  if (init && init.method === "POST") {
    posts.push({ path, body: init.body ? String(init.body) : "" });
    if (path.includes("/say")) {
      const body = { way: "resume", tmux: FRESH, model: "opus",
        message: "процесса у чата не было: поднят claude --resume в tmux-сессии " +
          FRESH + ", история продолжена" };
      return Promise.resolve({ ok: true, status: 200, statusText: "OK",
        text: () => Promise.resolve(JSON.stringify(body)),
        json: () => Promise.resolve(body) });
    }
  }
  return plain(path, init);
};

const st = await sandbox.chatState("demo", DEAD, board);
const panel = sandbox.chatPanel("demo", st);
const ta = tag(panel, "TEXTAREA");
const send = () => deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });

ta.value = "Ко мне вопросы в этом чате остались?";
send();
await settle();

// --- реплика уехала ручкой /say того же разговора ---
const say = posts.find((p) => p.path.includes("/say"));
if (!say) {
  fail("реплика в снятый разговор на сервер не уехала: " + JSON.stringify(posts));
}
if (!say.path.includes(DEAD)) {
  fail("реплика уехала мимо снятого разговора: " + say.path);
}
if (!say.body.includes("Ко мне вопросы в этом чате остались?")) {
  fail("на сервер уехал не текст человека: " + say.body);
}

// --- пузыря «не доставлено» и кнопок в панели нет ---
const said = dump(panel);
if (said.includes("не доставлено")) {
  fail("реплика, уехавшая резюмом, помечена недоставленной: " + said);
}
if (said.includes(WHY)) {
  fail("плашка снятого разговора осталась в панели: " + said);
}
for (const word of ["повторить", "отменить", "открыть живой чат", "поднять работу по задаче"]) {
  if (deepBtn(panel, word)) fail("в панели осталась кнопка «" + word + "»");
}

// --- поле ввода не заперто: писать в снятый разговор можно ---
if (deepBtn(panel, "Отправить").disabled) {
  fail("поле ввода снятого разговора заперто после отправки");
}

// --- смежный случай: неушедшее, пережившее перезапуск работы ---
// Реплика не ушла, пока сессия умирала, и осталась в очереди неушедшей («bad»).
// Работу подняли заново, разговор сняли. Дожим такой записи едет той же
// дорогой резюма, а не встаёт на причине: текст человека доезжает сам.
{
  store.set("devkit.chat.pend.demo/" + DEAD, JSON.stringify([
    { text: "ответ, не ушедший до перезапуска", wire: "ответ, не ушедший до перезапуска",
      born: Date.now() - 1000, state: "bad", why: "", tmux: "", id: "m-old" },
  ]));
  posts.length = 0;
  const st2 = await sandbox.chatState("demo", DEAD, board);
  sandbox.chatPanel("demo", st2);
  await settle();
  // Дожим очереди: у неушедшей записи он заведён таймером.
  for (let i = 0; i < 4; i += 1) {
    for (const t of timers.splice(0)) t.fn();
    await settle();
  }
  const again = posts.find((p) => p.path.includes("/say"));
  if (!again) {
    fail("дожим неушедшего в снятый разговор никуда не поехал: " + JSON.stringify(posts));
  }
  if (!again.body.includes("ответ, не ушедший до перезапуска")) {
    fail("дожим увёз не тот текст: " + again.body);
  }
  // Ключ записи тот же: сервер обязан узнать в дожиме ту же реплику.
  if (!again.body.includes("m-old")) {
    fail("дожим уехал без ключа записи, сервер примет его за новую реплику: " + again.body);
  }
}

console.log("ок: реплика в снятый разговор уехала ручкой /say и продолжена резюмом, " +
  "пузыря «не доставлено», плашки и кнопок в панели нет, дожим едет той же дорогой");
