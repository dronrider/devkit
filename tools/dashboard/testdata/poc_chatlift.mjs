// Стенд запуска задачи с формы и панели после него (ветка poc-chat).
//
// Человек пришёл на форму задачи из разговора и нажал «Выполнить»: панель
// открывалась мёртвой, с заголовком «Чат не найден» и плашкой про снятую
// сессию (снимок пользователя). Причин было две. Адрес экрана вёз с собой
// хвост «/chat/...», а к нему приклеивался хвост уже открытой панели, и
// получался адрес из двух разговоров подряд. И сама адресация была не той:
// сессия конвейера называет себя в реестре через несколько секунд после
// команды, а панель уже требовала разговор по имени. Предмет стенда это
// ожидание подъёма: адрес панели, плашка, запертое поле и переезд на
// родившуюся сессию. Заодно тут сторожится подсказка запертого поля: «чат идёт
// в vscode, пишите там» отправляла человека в другой редактор ни за чем.
//
// Зовётся: node testdata/poc_chatlift.mjs static/app.js

import { makeSandbox, settle, dump, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const SID = "feedbeef-0002";
const SESS = "task-XR-002";
const row = { id: "XR-002", title: "Обычная задача", sect: "backlog", r: 30, cost: "-",
  type: "task", accept: "agent" };
const board = { prefix: "XR", sections: [{ key: "backlog", rows: [row] }] };
const harnesses = [{ name: "claude-code", bin: "claude", default: true,
  models: [{ tier: "pro", model: "opus" }] }];

// Список разговоров машины: сессия конвейера появляется в нём не сразу, и
// стенд управляет этим сам, как это делает время.
const chats = { list: [] };
const posted = [];

const { sandbox, store } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST" && path.endsWith("/runs")) {
    posted.push(path);
    return { id: "XR-002", kind: "task", session: SESS, tier: "pro",
      message: "конвейер задачи XR-002 поднят в tmux-сессии " + SESS };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/quota") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/tasks/XR-002")) {
    return { row, file: "docs/tasks/XR-002.md", text: "# XR-002: Обычная задача\n\n## Цель\n\nпроба\n" };
  }
  if (path.includes("/sessions/dead0000-1111")) {
    return { raw: { status: 404, statusText: "Not Found", text: '{"error": "транскрипта нет"}' } };
  }
  if (path.includes("/chats")) return { chats: chats.list, models: [] };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});

await settle();
await sandbox.loadHarnesses();

const liftKey = "devkit.chat.lift.demo/new:XR-002";
const hashNow = () => sandbox.location.hash.replace(/^#/, "");
const findTa = (node) => {
  if (node.tagName === "TEXTAREA") return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && findTa(kid);
    if (got) return got;
  }
  return null;
};

// --- запуск с формы разговор не трогает, а помечает активность ---
{
  // Человек разбирает итоги задачи в одном разговоре и жмёт «Выполнить» у
  // соседней: панель обязана остаться на прежнем разговоре, иначе человека
  // выбрасывает из чужого чата (замечание пользователя).
  sandbox.location.hash = "#demo/XR-002/chat/aaaa1111-1111";
  const acts = sandbox.taskActions("demo", "XR-002", row, []);
  let run = null;
  for (const node of acts) {
    run = run || deepBtn(node, "Выполнить");
  }
  if (!run) fail("на форме задачи нет кнопки запуска: " + acts.map(dump).join(" "));
  run.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!posted.length) fail("запуск не ушёл на сервер");
  if (!hashNow().includes("/chat/aaaa1111-1111")) {
    fail("запуск увёл панель с чужого разговора: " + sandbox.location.hash);
  }
  if (hashNow().includes("new:XR-002")) {
    fail("запуск сам открыл панель нового чата: " + sandbox.location.hash);
  }
  // След запуска остаётся памятью подъёма: по ней моргает кнопка чата задачи,
  // и по ней же панель, открытая человеком, встретит ожидание подъёма.
  if (store.get(liftKey) === undefined) fail("имя tmux подъёма не легло в память");
  if (!String(store.get(liftKey)).includes(SESS)) {
    fail("память подъёма помнит не ту сессию: " + store.get(liftKey));
  }
}

// --- кнопка чата задачи моргает рамкой, пока работа идёт ---
{
  const idle = sandbox.rowChatBtn("demo", { id: "XR-777" });
  if (String(idle.className).includes("chatlive")) {
    fail("кнопка чата спокойной задачи моргает: " + idle.className);
  }
  // Работа только запущена: сессия ещё не назвалась, а нажатие уже сработало,
  // и сказать об этом надо сразу.
  const justRun = sandbox.rowChatBtn("demo", { id: "XR-002" });
  if (!String(justRun.className).includes("chatlive")) {
    fail("после запуска кнопка чата задачи не помечена активностью: " + justRun.className);
  }
  // Идущая работа помечена и без памяти запуска: признак берётся у самой работы.
  const running = sandbox.rowChatBtn("demo", { id: "XR-003" },
    [{ id: "XR-003", kind: "task", via: "tmux", live: "busy" }]);
  if (!String(running.className).includes("chatlive")) {
    fail("кнопка чата идущей работы не помечена: " + running.className);
  }
  const done = sandbox.rowChatBtn("demo", { id: "XR-004" },
    [{ id: "XR-003", kind: "task", via: "tmux", live: "busy" }]);
  if (String(done.className).includes("chatlive")) {
    fail("кнопка чата чужой задачи помечена активностью: " + done.className);
  }
}

// --- панель ожидания: плашка о подъёме, запертое поле и никакого «не найден» ---
{
  const st = await sandbox.chatState("demo", "new:XR-002", board);
  if (st.lift !== SESS) fail("состояние панели не знает о подъёме: " + JSON.stringify(st.lift));
  if (st.lost) fail("панель объявила потерянным свежезаказанный разговор");
  const head = dump(sandbox.chatHead("demo", st)).replace(/\s+/g, " ");
  if (head.includes("Чат не найден")) fail("шапка ждущей панели хоронит разговор: " + head);
  if (!head.includes("Сессия поднимается")) fail("шапка молчит о подъёме: " + head.slice(0, 200));
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const said = dump(panel).replace(/\s+/g, " ");
  // Слова тут про агента, а не про сессию: сессия это наше устройство, а
  // человек ждёт того, с кем говорит.
  if (!said.includes("агент запускается")) {
    fail("строки о подъёме над лентой нет: " + said.slice(0, 300));
  }
  if (said.includes("сессия поднимается...")) {
    fail("на экран вылезла наша механика вместо дела человека: " + said.slice(0, 300));
  }
  if (said.includes("больше нет: сессия снята")) {
    fail("ждущая панель показывает плашку протухшего адреса: " + said.slice(0, 300));
  }
  const ta = findTa(panel);
  if (!ta) fail("в ждущей панели нет поля ввода");
  // Реплика отсюда подняла бы вторую сессию рядом с запущенной, и поле заперто
  // ровно на те секунды, пока первая называет себя.
  if (!ta.disabled) fail("поле ввода ждущей панели открыто: реплика подняла бы вторую сессию");
  if (/vscode/i.test(ta.placeholder)) {
    fail("подсказка поля отправляет в другой редактор: " + ta.placeholder);
  }
  if (!ta.placeholder.includes("поднимается")) {
    fail("запертое поле не назвало причину: " + ta.placeholder);
  }
}

// --- сессия назвалась: панель переезжает на неё сама и память вычищена ---
{
  chats.list = [{ id: SID, state: "live", tmux: SESS, project: "demo",
    tasks: ["XR-002"], title: "конвейер XR-002" }];
  const st = await sandbox.chatState("demo", "new:XR-002", board);
  if (st.sid !== SID || st.fresh) {
    fail("панель не пришилась к родившейся сессии: " + JSON.stringify([st.sid, st.fresh]));
  }
  if (st.lift) fail("признак подъёма остался у пришитой панели: " + st.lift);
  if (store.get(liftKey) !== undefined) fail("память подъёма не вычищена: " + store.get(liftKey));
  if (!String(sandbox.location.hash).includes(SID)) {
    fail("адрес панели остался эфемерным: " + sandbox.location.hash);
  }
}

// --- протухший адрес: поле заперто, но человека не отправляют в vscode ---
{
  const st = await sandbox.chatState("demo", "dead0000-1111", board);
  if (!st.lost) fail("стенд не воспроизвёл протухший адрес: " + JSON.stringify(st));
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  const ta = findTa(panel);
  if (!ta) fail("в панели протухшего адреса нет поля ввода");
  if (!ta.disabled) fail("поле протухшего адреса открыто");
  if (/vscode/i.test(ta.placeholder)) {
    fail("подсказка поля отправляет в другой редактор: " + ta.placeholder);
  }
  if (!ta.placeholder.trim()) fail("запертое поле молчит вовсе");
}

console.log("poc_chatlift: ok");
