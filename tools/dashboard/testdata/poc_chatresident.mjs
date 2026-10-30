// Стенд чата задачи, чьим носителем журнал назвал окно конвейера (DK-1521).
//
// Журнал назначений пишет носителем ключ tmux-<имя окна>, а ленту держит сессия,
// чей хук старта записался в реестр с транскриптом. До правки ручка адреса
// отдавала сам ключ, и панель задачи встречала человека пустой лентой с
// обрубком «чат tmux-tas» в заголовке. Стенд проходит дверь задачи трижды:
// поднимающая вкладка ждёт на сыром ключе плашкой подъёма, после разрешения
// ключа в жильца она узнаёт свой подъём по отдельному полю окна и держит
// ожидание над открывшейся лентой, а свежая вкладка без памяти подъёма
// открывает ленту и заголовок разговора сразу.
//
// Зовётся: node testdata/poc_chatresident.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const SID = "feedbeef-0003";
const WIN = "task-XR-002";
const KEY = "tmux-" + WIN;
const row = { id: "XR-002", title: "Обычная задача", sect: "backlog", r: 30, cost: "-",
  type: "task", accept: "agent" };
const board = { prefix: "XR", sections: [{ key: "backlog", rows: [row] }] };
const harnesses = [{ name: "claude-code", bin: "claude", default: true,
  models: [{ tier: "pro", model: "opus" }] }];

// Ручка адреса в двух состояниях мира. Пока сессия конвейера не назвала себя
// в реестре, жильца у окна нет, и адрес остаётся самим ключом. Назвавшись,
// сессия становится жильцом с транскриптом, и ручка отдаёт её разговор вместе
// с именем окна отдельным полем.
const hand = { chat: KEY, window: "" };
// Список разговоров машины оживает вместе с жильцом.
const chats = { list: [] };

const { sandbox, store } = makeSandbox(app, (path, init) => {
  if (path.includes("/tasks/XR-002/chat")) {
    return { id: "XR-002", project: "demo", chat: hand.chat, window: hand.window,
      source: "работа", since: new Date().toISOString() };
  }
  if (path.includes("/tasks/XR-002")) {
    return { row, file: "docs/tasks/XR-002.md", text: "# XR-002: Обычная задача\n\n## Цель\n\nпроба\n" };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/quota") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board, works: [] };
  // Разговора с ключом окна не существует: до правки панель стояла на самом
  // ключе, и точечная проба ленты находила именно этот отказ.
  if (path.includes("/sessions/" + encodeURIComponent(KEY))) {
    return { raw: { status: 404, statusText: "Not Found", text: '{"error": "разговора с ключом окна нет"}' } };
  }
  if (path.includes("/sessions/" + SID)) {
    return { items: [{ role: "assistant", text: "лента жильца окна жива",
      time: new Date().toISOString() }], start: true };
  }
  if (path.includes("/sessions/")) return { items: [], start: true };
  if (path.includes("/chats")) return { chats: chats.list, models: [] };
  return {};
});

await settle();
await sandbox.loadHarnesses();

const liftKey = "devkit.chat.lift.demo/new:XR-002";
const findTa = (node) => {
  if (node.tagName === "TEXTAREA") return node;
  for (const kid of node.children || []) {
    const got = typeof kid === "object" && findTa(kid);
    if (got) return got;
  }
  return null;
};
const headOf = (st) => dump(sandbox.chatHead("demo", st)).replace(/\s+/g, " ");
const buildPanel = async (st) => {
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  return panel;
};

// --- поднимающая вкладка ждёт на сыром ключе плашкой подъёма ---
{
  // Запуск задачи оставил памяти подъёма имя окна, а сессия конвейера ещё не
  // назвала себя в реестре: ручка отдаёт сам ключ, и панель обязана ждать
  // рождения сессии, а не хоронить адрес из-за транскрипта, которого нет.
  store.set(liftKey, JSON.stringify({ tmux: WIN, born: Date.now() }));
  sandbox.location.hash = "#demo/XR-002/chat/XR-002";
  const st = await sandbox.chatState("demo", "XR-002", board);
  if (st.lift !== WIN) fail("ожидание подъёма не встало на сыром ключе: " + JSON.stringify(st.lift));
  if (st.sid) fail("панель пришлась на разговор до рождения сессии: " + st.sid);
  if (st.lost) fail("сырой ключ объявлен протухшим адресом");
  const head = headOf(st);
  if (!head.includes("Сессия поднимается")) fail("шапка молчит о подъёме: " + head.slice(0, 200));
  const panel = await buildPanel(st);
  const said = dump(panel).replace(/\s+/g, " ");
  if (!said.includes("агент запускается")) {
    fail("плашки подъёма над пустой лентой нет: " + said.slice(0, 300));
  }
  const ta = findTa(panel);
  if (!ta) fail("в ждущей панели нет поля ввода");
  if (!ta.disabled) fail("поле ввода открыто при неродившейся сессии");
}

// --- разрешение ключа: подъём узнаётся по полю окна и держится над лентой ---
{
  // Сессия назвалась: реестр видит её транскрипт, и ручка адреса разрешает
  // ключ в разговор жильца, а имя окна отдаёт отдельным полем. Поднимающая
  // вкладка сверяет подъём именно с этим полем, и ожидание переживает
  // разрешение: реплика уходила бы в жильца наугад, а подъём ещё может
  // оказаться прежним хозяином имени.
  chats.list = [{ id: SID, state: "live", tmux: WIN, project: "demo",
    tasks: ["XR-002"], title: "конвейер XR-002", born: new Date().toISOString() }];
  hand.chat = SID;
  hand.window = WIN;
  const st = await sandbox.chatState("demo", "XR-002", board);
  if (st.sid !== SID) fail("разрешённый адрес не открыл разговор жильца: " + JSON.stringify(st.sid));
  if (st.lift !== WIN) {
    fail("ожидание подъёма не пережило разрешение ключа: " + JSON.stringify(st.lift));
  }
  const head = headOf(st);
  if (!head.includes("Сессия поднимается")) {
    fail("шапка сняла ожидание подъёма после разрешения: " + head.slice(0, 200));
  }
  if (head.includes("Чат не найден")) fail("шапка хоронит разговор жильца: " + head.slice(0, 200));
  const panel = await buildPanel(st);
  const said = dump(panel).replace(/\s+/g, " ");
  if (!said.includes("лента жильца окна жива")) {
    fail("лента разговора жильца не открылась под ожиданием: " + said.slice(0, 300));
  }
  const ta = findTa(panel);
  if (!ta) fail("в панели жильца нет поля ввода");
  if (!ta.disabled) fail("поле ввода открыто при живом подъёме");
  if (!ta.placeholder.includes("поднимается")) {
    fail("запертое поле не назвало причину: " + ta.placeholder);
  }
}

// --- свежая вкладка без памяти подъёма: лента и заголовок жильца окна ---
{
  // Соседняя вкладка и перезагрузка памяти подъёма не знают: их дорога это
  // разрешение ключа сервером, и разговор жильца открывается сразу, с
  // заголовком и открытым полем.
  store.delete(liftKey);
  const st = await sandbox.chatState("demo", "XR-002", board);
  if (st.sid !== SID) fail("свежая вкладка не встала на жильца окна: " + JSON.stringify(st.sid));
  if (st.lost) fail("панель объявила потерянным разговор жильца окна");
  if (st.lift) fail("вкладка без памяти подъёма объявила подъём: " + JSON.stringify(st.lift));
  const head = headOf(st);
  if (head.includes("Чат не найден")) fail("шапка хоронит разговор жильца: " + head.slice(0, 200));
  if (/чат tmux-/.test(head)) {
    fail("шапка показывает обрубок ключа окна вместо заголовка: " + head.slice(0, 200));
  }
  if (!head.includes("конвейер XR-002")) {
    fail("шапка не назвала заголовок разговора жильца: " + head.slice(0, 200));
  }
  const panel = await buildPanel(st);
  const said = dump(panel).replace(/\s+/g, " ");
  if (!said.includes("лента жильца окна жива")) {
    fail("лента разговора жильца не открылась: " + said.slice(0, 300));
  }
  const ta = findTa(panel);
  if (!ta) fail("в панели жильца нет поля ввода");
  if (ta.disabled) fail("поле ввода заперто чужим подъёмом: " + ta.placeholder);
}

console.log("poc_chatresident: ok");
