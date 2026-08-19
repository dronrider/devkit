// Стенд окна чатов (ветка poc-chat). Предмет проверки это собранная панель и её
// обработчики, а не текст исходника: половина здешних правок ломалась молча,
// когда ссылка в обработчике указывала в пустоту, а экран при этом рисовался.
// Стенд поймал два настоящих бага: падение отправки на несобранном echo (текст
// исчезал из поля, запрос не уходил) и подмену базы адреса при переходе в чат
// из раздела «Агенты».
//
// Зовётся: node testdata/poc_panel.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, tag, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const chats = [
  { id: "aaaa1111-1111", title: "Выполни XR-1", mtime: "2026-08-13T10:02:00+03:00",
    tasks: ["XR-1", "XR-9"], model: "sonnet", liveModel: "sonnet", own: true,
    tmux: "chat-XR-1-1", state: "live", tree: "xr-1", idle: true },
  { id: "bbbb2222-2222", title: "Верни XR-1 на доработку", mtime: "2026-08-12T09:00:00+03:00",
    tasks: ["XR-1"], model: "opus", state: "dead" },
  { id: "cccc3333-3333", title: "почини роутер", mtime: "2026-08-13T10:04:00+03:00",
    tasks: [], model: "opus", state: "vscode" },
];

const models = [
  { model: "haiku", tier: "mini", harness: "claude-code" },
  { model: "sonnet", tier: "base", harness: "claude-code" },
  { model: "opus", tier: "pro", harness: "claude-code", default: true },
  { model: "fable", tier: "max", harness: "claude-code" },
  { model: "glm-5.3", tier: "base", harness: "glm-code" },
];

const board = { sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "дашборд без дёрганья", sect: "in-progress" },
  { id: "XR-7", title: "Цель: панель разговора", sect: "in-progress" },
] }] };

const app = appPathArg();
const { sandbox, store, timers, moves } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats, models };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});

// --- адреса: старые маршруты живы, новые разбираются ---
for (const [hash, want] of [
  ["#demo/chat/XR-1", { proj: "demo", chat: "XR-1" }],
  ["#demo/agent/XR-1", { proj: "demo", id: "XR-1", chat: "XR-1" }],
  ["#demo/session/cccc3333-3333", { proj: "demo", chat: "cccc3333-3333" }],
  ["#demo/chat/board", { proj: "demo", chat: "board" }],
  ["#demo/chat/new", { proj: "demo", chat: "new" }],
]) {
  sandbox.location.hash = hash;
  const rt = sandbox.route();
  for (const [f, v] of Object.entries(want)) {
    if (rt[f] !== v) fail("адрес " + hash + ": " + f + " = " + JSON.stringify(rt[f]) + ", ждал " + JSON.stringify(v));
  }
}

// --- фильтр по задаче и выбор чата ---
sandbox.location.hash = "#demo";
let st = await sandbox.chatState("demo", "XR-1", board);
if (st.sid !== "aaaa1111-1111") fail("открыт не свежий чат задачи: " + st.sid);
if (sandbox.chatVisible(st).length !== 2) fail("фильтр по задаче не сработал");
store.set("devkit.chat.filter", "0");
if (sandbox.chatVisible(st).length !== 3) fail("выключенный фильтр всё равно режет список");
store.set("devkit.chat.filter", "1");

// --- дорога реплики по состоянию чата ---
for (const [sid, kind, off] of [["aaaa1111-1111", "say", false],
  ["bbbb2222-2222", "resume", false], ["cccc3333-3333", "resume", false]]) {
  const s2 = await sandbox.chatState("demo", sid, board);
  const way = sandbox.chatWay(s2);
  if (way.kind !== kind || Boolean(way.off) !== off) {
    fail("чат " + sid + ": дорога " + way.kind + "/" + way.off + ", ждал " + kind + "/" + off);
  }
}
const fresh = await sandbox.chatState("demo", "new:XR-1", board);
if (fresh.task !== "XR-1" || !fresh.fresh) fail("новый чат потерял задачу");

// --- шапка: модель, лейбл задачи, выпадающий список с поиском ---
st = await sandbox.chatState("demo", "XR-1", board);
const head = sandbox.chatHead("demo", st);
const sel = tag(head, "SELECT");
if (!sel) fail("выбора модели в шапке нет");
if (!dump(sel).includes("fable") || !dump(sel).includes("glm-5.3")) {
  fail("в списке моделей нет верхнего яруса или второй подписки: " + dump(sel));
}
if (!dump(head).includes("XR-1")) fail("номера задачи нет лейблом в шапке");
const line = head.children[0];
line.children[0].handlers.click({ stopPropagation: () => {} });
const drop = line.children[line.children.length - 1];
if (!String(drop.className).includes("cdrop")) fail("выпадающий список не открылся");
const rows = drop.children[1];
if (rows.children.length !== 2) fail("в списке не те строки: " + rows.children.length);
const find = drop.children[0];
find.value = "роутер";
find.handlers.input();
if (!dump(rows).includes("ничего не нашлось")) fail("поиск не сказал про пустую выдачу");

// --- отправка через собранную панель: тут падало на несобранном echo ---
{
  const stOld = await sandbox.chatState("demo", "aaaa1111-1111", board);
  const panel = sandbox.chatPanel("demo", stOld);
  const ta = tag(panel, "TEXTAREA");
  const send = deepBtn(panel, "Отправить");
  if (!ta || !send) fail("в панели нет поля или кнопки отправки");
  ta.value = "реплика в старый чат";
  send.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!dump(panel).includes("реплика в старый чат")) {
    fail("пузырь не встал в ленту: " + dump(panel).slice(0, 200));
  }
  const stNew = await sandbox.chatState("demo", "new:XR-1", board);
  const panelNew = sandbox.chatPanel("demo", stNew);
  const taNew = tag(panelNew, "TEXTAREA");
  taNew.value = "первая реплика нового чата";
  deepBtn(panelNew, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!dump(panelNew).includes("первая реплика нового чата")) {
    fail("пузырь не встал в пустом чате: " + dump(panelNew).slice(0, 200));
  }
}

// --- оптимистичный пузырь: три состояния и сверка эха ---
{
  const pend = makeNode("div");
  const echo = sandbox.makeEcho("demo", pend, makeNode("div"));
  const m = echo.add("первая реплика", () => {});
  if (!dump(pend).includes("отправляется")) fail("состояние отправки не названо");
  echo.sent(m);
  if (!dump(pend).includes("доставлено")) fail("удача не отмечена");
  echo.saw({ role: "user", text: "первая реплика" });
  if (dump(pend).includes("первая реплика")) fail("эхо не сняло местный пузырь");
  const bad = echo.add("вторая реплика", () => {});
  echo.bad(bad);
  if (!dump(pend).includes("не ушло") || !dump(pend).includes("повторить")) {
    fail("отказ не назван или нет кнопки повтора: " + dump(pend));
  }
  echo.saw({ role: "assistant", text: "вторая реплика" });
  if (!dump(pend).includes("вторая реплика")) fail("ответ агента съел местный пузырь");
}

// --- работа субагента: блок собирается, а живой не свёрнут ---
{
  const sub = [];
  for (let i = 0; i < 12; i++) {
    sub.push(i % 2
      ? { seq: i, role: "toolout", text: "вывод " + i, sub: "работа", time: "2026-08-13T09:00:00+03:00" }
      : { seq: i, role: "tool", tool: "Bash", text: "command: ls " + i, note: "ls", sub: "работа", time: "2026-08-13T09:00:00+03:00" });
  }
  const box = makeNode("div");
  const prev = sandbox.fetch;
  sandbox.fetch = (path, init) => {
    if (path.includes("/sessions/") && !path.includes("stream=1")) {
      return Promise.resolve({ ok: true, status: 200,
        json: () => Promise.resolve({ session: "sub1", head: { id: "sub1" }, items: sub, total: sub.length }) });
    }
    return prev(path, init);
  };
  sandbox.wireChatFeed("demo", box, "sub1");
  await settle();
  sandbox.fetch = prev;
  const blk = byClass(box, "subblk");
  if (!blk) fail("блок работы субагента не собрался: " + dump(box).slice(0, 200));
  // Вся лента из работы субагента: свёрнутый блок читался как пустой чат, и
  // ровно это пользователь и увидел (регресс тринадцатого круга POC).
  if (!String(blk.className).includes("open")) fail("живой блок субагента свёрнут");
  const body = blk.children[1];
  if (!body || body.hidden) fail("тело живого блока скрыто");
  if (!body.children.length) fail("в блоке субагента пусто");
  if (!dump(box).includes("ls")) fail("записи субагента не видны: " + dump(box).slice(0, 200));
}

// --- сломанная запись не роняет ленту целиком ---
{
  const boom = { seq: 0, role: "user", get text() { throw new Error("битая запись"); } };
  const node = sandbox.safeItem(sandbox.chatItem, boom);
  if (!dump(node).includes("не отрисовалась")) fail("заглушки на сломанной записи нет: " + dump(node));
}

// --- лента: размышления, вывод инструмента, служебные вставки ---
{
  const spent = sandbox.replyEl({ role: "thinking", text: "", spent: 9377 });
  if (!dump(spent).includes("Размышлял 9 с")) fail("длительность размышлений не названа");
  const quiet = sandbox.replyEl({ role: "thinking", text: "", spent: 0 });
  if (!dump(quiet).includes("Размышление")) fail("подпись размышлений не та: " + dump(quiet));
  const card = sandbox.toolPair(
    { role: "tool", tool: "Bash", text: "command: ls -la", note: "ls -la" },
    { role: "toolout", text: "итого 42" });
  const said = dump(card);
  if (!said.includes("Bash") || !said.includes("ls -la") || !said.includes("итого 42")) {
    fail("вызов и вывод не в одной карточке: " + said);
  }
  if (!deepBtn(card, "foldcp")) fail("кнопки копирования нет");
  // Вход и выход стоят отдельными строками со стрелками (замечание 7).
  if (!byClass(card, "pin") || !byClass(card, "pout")) {
    fail("вход и выход не разведены строками: " + said);
  }
  const note = sandbox.chatItem({ role: "note", text: "Фоновый агент завершил работу" });
  if (!dump(note).includes("Фоновый агент")) fail("служебная строка потерялась");
}

// --- разметка постановки: полный набор и никакого сырого HTML ---
{
  const md = sandbox.mdRender([
    "# Заголовок", "", "Текст с **жирным** и `кодом`.", "",
    "- раз", "- два", "",
    "| поле | смысл |", "| --- | --- |", "| a | б |", "",
    "> цитата", "", "```", "код <не тег>", "```",
  ].join("\n"));
  const said = dump(md);
  for (const want of ["Заголовок", "жирным", "кодом", "раз", "поле", "цитата", "код <не тег>"]) {
    if (!said.includes(want)) fail("разметка потеряла «" + want + "»: " + said);
  }
  if (!tag(md, "TABLE")) fail("таблица не собралась");
  if (!tag(md, "BLOCKQUOTE")) fail("цитата не собралась");
  if (!tag(md, "PRE")) fail("код-блок не собрался");
  if (tag(md, "НЕ")) fail("текст просочился разметкой");
  // Нумерованные списки: вложенность, продолжение пункта и начало не с единицы
  // (замечание 1 двенадцатого круга POC).
  const num = sandbox.mdRender([
    "1. первый", "2. второй", "   продолжение второго",
    "   1. вложенный", "3. третий", "", "4. новый список",
  ].join("\n"));
  const lists = [];
  const walk = (n, d) => {
    if (n.tagName === "OL" || n.tagName === "UL") lists.push([n.tagName, d, n.children.length, n.attrs.start]);
    for (const k of n.children || []) walk(k, d + 1);
  };
  walk(num, 0);
  if (lists.length !== 3) fail("нумерованные списки собрались не так: " + JSON.stringify(lists));
  if (lists[0][2] !== 3) fail("во внешнем списке не три пункта: " + JSON.stringify(lists[0]));
  if (lists[1][1] <= lists[0][1]) fail("вложенный список не вложен: " + JSON.stringify(lists));
  if (lists[2][3] !== "4") fail("список не продолжил нумерацию с четвёртого: " + JSON.stringify(lists[2]));
  if (!dump(num).includes("продолжение второго")) fail("продолжение пункта потерялось");
}

// --- выделение уезжает агенту префиксом, в ленте видно компактно ---
{
  const sel2 = { file: "docs/tasks/XR-1.md", text: 'первая строка\nвторая "в кавычках"' };
  const wire = sandbox.selPrefix(sel2) + "поправь этот текст";
  if (!wire.startsWith('<selection file="docs/tasks/XR-1.md">')) fail("префикс собран не так");
  if (!wire.includes('вторая "в кавычках"')) fail("кавычки в выделении поехали");
  if (!wire.endsWith("</selection>\nпоправь этот текст")) fail("реплика встала не после блока");
  const bubble = sandbox.chatItem({ role: "user", text: "поправь этот текст",
    sel: sel2.text, selFile: sel2.file, time: "2026-08-20T10:00:00+03:00" });
  const said = dump(bubble);
  if (!said.includes("поправь этот текст")) fail("слов человека нет в пузыре");
  if (!said.includes("с выделением")) fail("пометки о выделении нет");
}

// --- кнопка продолжения работы стоит и у задачи, и у цели (замечание 10) ---
{
  const stT = await sandbox.chatState("demo", "XR-1", board);
  if (!deepBtn(sandbox.chatPanel("demo", stT), "cgo")) {
    fail("у чата задачи нет кнопки продолжения");
  }
  // XR-7 это цель: до этого круга кнопку у неё прятали, а полоса «Продолжить»
  // оставалась стоять на экране.
  const stG2 = await sandbox.chatState("demo", "XR-7", board);
  if (!stG2.isGoal) fail("стенд не считает XR-7 целью");
  if (!deepBtn(sandbox.chatPanel("demo", stG2), "cgo")) {
    fail("у чата цели нет кнопки продолжения");
  }
}

// --- хват высоты стоит НАД полем: проверяется порядок узлов, а не стили ---
{
  const stG = await sandbox.chatState("demo", "aaaa1111-1111", board);
  const cbox = byClass(sandbox.chatPanel("demo", stG), "cbox");
  if (!cbox) fail("коробки ввода нет");
  const kids = cbox.children || [];
  const gripAt = kids.findIndex((k) => String(k.className || "").includes("tagrip"));
  const taAt = kids.findIndex((k) => k.tagName === "TEXTAREA");
  if (gripAt < 0) fail("хвата высоты нет вовсе");
  if (gripAt > taAt) fail("хват стоит под полем, а не над ним");
}

// --- черновик реплики переживает пересборку панели ---
{
  const stD = await sandbox.chatState("demo", "aaaa1111-1111", board);
  const ta1 = tag(sandbox.chatPanel("demo", stD), "TEXTAREA");
  ta1.value = "недописанная мысль";
  ta1.handlers.input();
  for (const t of timers.splice(0)) t.fn();
  const ta2 = tag(sandbox.chatPanel("demo", stD), "TEXTAREA");
  if (ta2.value !== "недописанная мысль") fail("черновик не вернулся: " + JSON.stringify(ta2.value));
}

// --- чат из раздела «Агенты»: панель поверх раздела, проект внутри адреса ---
{
  sandbox.location.hash = "#/agents";
  const addr = sandbox.chatAddr("другой-проект", "aaaa1111-1111");
  if (addr !== "другой-проект~aaaa1111-1111") fail("проект не уехал в адрес: " + addr);
  moves.length = 0;
  sandbox.openChat(addr);
  const to = sandbox.location.hash;
  if (!to.startsWith("#/agents/chat/")) fail("панель увела с раздела агентов: " + to);
  const rt = sandbox.route();
  if (!rt.agents || rt.chat !== addr) fail("хвост разобран не так: " + JSON.stringify(rt));
  const parts = sandbox.chatAddrParts("доска", addr);
  if (parts.project !== "другой-проект" || parts.addr !== "aaaa1111-1111") {
    fail("адрес не разобрался обратно: " + JSON.stringify(parts));
  }
  sandbox.location.hash = "#demo";
  if (sandbox.chatAddr("demo", "aaaa1111-1111") !== "aaaa1111-1111") {
    fail("на экране проекта в адрес влез проект");
  }
}

// --- панель переживает любую навигацию: все дороги разом ---
{
  for (const [name, hash] of [
    ["доска проекта", "demo"], ["экран задачи", "demo/XR-1"],
    ["уведомления", "demo/feed"], ["накопитель", "demo/drafts"],
    ["запись", "demo/draft/XR-D1"], ["новая задача", "demo/new"],
    ["раздел агентов", "/agents"], ["другой проект", "второй"], ["главная", ""],
  ]) {
    sandbox.location.hash = "#demo/XR-1/chat/aaaa1111-1111";
    sandbox.goKeepingChat(hash);
    const to = sandbox.location.hash;
    if (!to.includes("/chat/aaaa1111-1111")) fail("переход «" + name + "» потерял чат: " + to);
    if (sandbox.route().chat !== "aaaa1111-1111") fail("после «" + name + "» хвост разобран не так");
  }
}

// --- последний чат задачи помнится ---
{
  await settle();
  await sandbox.paintChat("demo", "bbbb2222-2222", board, []);
  await settle();
  const back = await sandbox.chatState("demo", "XR-1", board);
  if (back.sid !== "bbbb2222-2222") fail("последний чат задачи не вернулся: " + back.sid);
  store.set("devkit.chat.task.XR-1", "которого-нет");
  const gone = await sandbox.chatState("demo", "XR-1", board);
  if (gone.sid !== "aaaa1111-1111") fail("память увела на несуществующий чат: " + gone.sid);
}

// --- уведомления открытого чата молчат, чужие всплывают ---
{
  sandbox.location.hash = "#demo/chat/aaaa1111-1111";
  await sandbox.paintChat("demo", "aaaa1111-1111", board, []);
  await settle();
  const mine = { time: "2026-08-20T10:00:00", project: "demo", session: "aaaa1111-1111", title: "ход" };
  const alien = { time: "2026-08-20T10:00:00", project: "demo", session: "zzzz9999-9999", title: "ход" };
  if (sandbox.flashWorthy(mine, "2026-08-20T09:00:00", false)) fail("баннер про открытый чат не заглушён");
  if (!sandbox.flashWorthy(alien, "2026-08-20T09:00:00", false)) fail("чужой баннер заглушён напрасно");
  await sandbox.paintChat("demo", "", board, []);
  await settle();
  if (!sandbox.flashWorthy(mine, "2026-08-20T09:00:00", false)) fail("при закрытой панели баннер молчит");
}

console.log("окно чатов: адреса, фильтр, дорога реплики, шапка с моделью и поиском, " +
  "отправка обоих видов чата, оптимистичный пузырь, лента, разметка, выделение, " +
  "хват над полем, черновик, чат из раздела агентов, навигация, память задачи, тихие уведомления");
