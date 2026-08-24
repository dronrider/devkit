// Стенд повторов очереди исходящих (ветка poc-chat): окно «стоп -> реплика ->
// резюм».
//
// Живой случай: человек написал реплику в момент, когда разговор пересоздавался
// сменой модели, и она доехала до сессии пятью одинаковыми копиями подряд. Пять
// отдельных отправок с разницей в минуты: ответ ручки до панели не доезжал,
// панель считала реплику неушедшей и слала снова, а узнать в этих отправках
// одну и ту же запись было нечем. Предмет стенда это ключ записи: он один и тот
// же у первой попытки и у всех повторов, и второй отправки той же записи не
// заводится, пока первая не разрешилась.
//
// Зовётся: node testdata/poc_saydup.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, tag, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const chats = [
  { id: "aaaa1111-1111", title: "Выполни XR-1", mtime: "2026-08-24T10:02:00+03:00",
    tasks: ["XR-1"], model: "opus", liveModel: "opus", own: true,
    tmux: "chat-XR-1-1", state: "live", tree: "xr-1", idle: true },
];
const models = [{ model: "opus", tier: "pro", harness: "claude-code", default: true }];
const board = { sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "панель разговора", sect: "in-progress" },
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

// Попытки отправки: путь и разобранное тело. По ним и считается, сколько раз
// реплика уехала и с каким ключом.
const tries = [];
// Судьба следующей отправки: «мимо» это ответ ручки отказом (сессию сняли), а
// «висит» это ответ, которого панель не дождётся вовсе.
let sayFate = "ok";
let hang = null;
const answer = (body, ok) => Promise.resolve({ ok: ok !== false, status: ok === false ? 502 : 200,
  text: () => Promise.resolve(JSON.stringify(body)),
  json: () => Promise.resolve(body) });

const plain = sandbox.fetch;
sandbox.fetch = (path, init) => {
  if (init && init.method === "POST" && path.includes("/say")) {
    tries.push({ path, body: init.body ? JSON.parse(init.body) : null });
    if (sayFate === "hang") return new Promise((go) => { hang = go; });
    if (sayFate === "miss") {
      return answer({ error: "сокет чата не взял реплику: сессия снята" }, false);
    }
    return answer({ way: "socket", pid: 1, where: "живая сессия" });
  }
  return plain(path, init);
};

const fireTimers = () => {
  for (const t of timers.splice(0)) t.fn();
};

const st = await sandbox.chatState("demo", "aaaa1111-1111", board);
const panel = sandbox.chatPanel("demo", st);
const ta = tag(panel, "TEXTAREA");
const send = () => deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });

// --- реплика в окно снятой сессии: отказ, дожим, тот же ключ ---
sayFate = "miss";
ta.value = "держи реплику в момент смены модели";
send();
await settle();
if (tries.length !== 1) fail("первая отправка ушла " + tries.length + " раз: " + JSON.stringify(tries));
const first = tries[0].body;
if (!first || !first.msg_id) {
  fail("у реплики нет ключа записи, повтор от новой реплики не отличить: " + JSON.stringify(first));
}
if (first.text !== "держи реплику в момент смены модели") {
  fail("на ручку уехал не тот текст: " + JSON.stringify(first));
}

// Сессию подняли резюмом, канал ожил: дожим уходит на ручку.
sayFate = "ok";
fireTimers();
await settle();
if (tries.length !== 2) {
  fail("дожим сходил " + (tries.length - 1) + " раз вместо одного: " + JSON.stringify(tries));
}
if (tries[1].body.msg_id !== first.msg_id) {
  fail("повтор уехал новым ключом, сервер увидит вторую реплику: " +
    JSON.stringify([first.msg_id, tries[1].body.msg_id]));
}
if (tries[1].body.text !== first.text) {
  fail("повтор увёз не тот текст: " + JSON.stringify(tries[1].body));
}

// Дальше дожимать нечего: реплика доставлена, и таймеры новых отправок не
// заводят. Пять копий в живом случае это ровно вот такой цикл.
fireTimers();
await settle();
fireTimers();
await settle();
if (tries.length !== 2) {
  fail("после удачной доставки панель слала снова: попыток " + tries.length);
}

// --- следующая реплика человека это своя запись, а не повтор ---
ta.value = "вопрос выше уже не актуален";
send();
await settle();
if (tries.length !== 3) fail("вторая реплика не ушла: " + JSON.stringify(tries));
if (tries[2].body.msg_id === first.msg_id) {
  fail("новой реплике достался ключ прошлой: сервер отбросит её как повтор");
}

// --- пока попытка не разрешилась, второй такой же не заводится ---
sayFate = "hang";
ta.value = "реплика в подвисшую ручку";
send();
await settle();
const stuck = tries.length;
fireTimers();
await settle();
fireTimers();
await settle();
if (tries.length !== stuck) {
  fail("панель слала поверх неразрешённой попытки: попыток " + (tries.length - stuck + 1));
}
if (hang) hang(await answer({ way: "socket" }));
await settle();

// --- очередь помнит ключ через перезагрузку страницы ---
// Вкладка, поднятая заново, шлёт ту же запись, а не новую: иначе перезагрузка
// в метро оборачивается ещё одной копией реплики.
{
  const pend = makeNode("div");
  const echo = sandbox.makeEcho("demo", pend, makeNode("div"), "aaaa1111-1111", () => {});
  const m = echo.add("реплика, пережившая перезагрузку", () => {});
  if (!m.id) fail("запись очереди завелась без ключа");
  echo.bad(m);
  echo.stop();
  const raw = store.get("devkit.chat.pend.demo/aaaa1111-1111");
  if (!raw) fail("неушедшая реплика не легла в память вкладки вовсе");
  const kept = JSON.parse(raw);
  if (!kept.length || kept[0].id !== m.id) {
    fail("в памяти вкладки запись без своего ключа: " + raw);
  }
}

console.log("poc_saydup: ok");
