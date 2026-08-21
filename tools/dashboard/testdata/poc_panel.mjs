// Стенд окна чатов (ветка poc-chat). Предмет проверки это собранная панель и её
// обработчики, а не текст исходника: половина здешних правок ломалась молча,
// когда ссылка в обработчике указывала в пустоту, а экран при этом рисовался.
// Стенд поймал два настоящих бага: падение отправки на несобранном echo (текст
// исчезал из поля, запрос не уходил) и подмену базы адреса при переходе в чат
// из раздела «Агенты».
//
// Зовётся: node testdata/poc_panel.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, tag, byClass, allByClass, deepBtn, fail, appPathArg }
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
const { sandbox, store, timers, moves, posted } = makeSandbox(app, (path) => {
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

// --- строка отправки: модель, а в шапке лейбл задачи и выпадающий список ---
st = await sandbox.chatState("demo", "XR-1", board);
const head = sandbox.chatHead("demo", st);
if (tag(head, "SELECT")) fail("выбор модели остался в шапке");
const sendRow = byClass(sandbox.chatPanel("demo", st), "crow");
const sel = tag(sendRow, "SELECT");
if (!sel) fail("выбора модели нет в строке отправки: " + dump(sendRow).slice(0, 200));
if (!dump(sel).includes("fable") || !dump(sel).includes("glm-5.3")) {
  fail("в списке моделей нет верхнего яруса или второй подписки: " + dump(sel));
}
// Имена короткие: ярус с подпиской ушли в подсказку, скобок в списке нет.
if (dump(sel).includes("(")) fail("в именах моделей остались скобки: " + dump(sel));
// Выбор стоит левее кнопки продолжения работы.
const order = (sendRow.children || []).map((k) => String(k.className || "") + "/" + k.tagName);
const atModel = order.findIndex((k) => k.includes("cmodel"));
const atGo = order.findIndex((k) => k.includes("cgo"));
if (atModel < 0 || atGo < 0 || atModel > atGo) {
  fail("выбор модели стоит не слева от кнопки продолжения: " + JSON.stringify(order));
}
if (!dump(head).includes("XR-1")) fail("номера задачи нет лейблом в шапке");
const line = byClass(head, "chline");
line.children[0].handlers.click({ stopPropagation: () => {} });
const drop = line.children[line.children.length - 1];
if (!String(drop.className).includes("cdrop")) fail("выпадающий список не открылся");
const rows = drop.children[1];
if (rows.children.length !== 2) fail("в списке не те строки: " + rows.children.length);
const find = drop.children[0];
find.value = "роутер";
find.handlers.input();
if (!dump(rows).includes("ничего не нашлось")) fail("поиск не сказал про пустую выдачу");

// --- выбор модели живёт до явной смены, фактическая стоит пометкой ---
// Прежде список показывал модель из транскрипта, и выбор ею затирался: человек
// ставил fable, ответ приходил прежней моделью, и в списке снова стоял opus.
{
  const stPick = await sandbox.chatState("demo", "aaaa1111-1111", board);
  stPick.entry = Object.assign({}, stPick.entry, { model: "fable", liveModel: "opus" });
  const headPick = sandbox.chatPanel("demo", stPick);
  const selPick = tag(headPick, "SELECT");
  const chosen = selPick.children.filter((o) => o.selected).map((o) => o.value);
  if (chosen.length !== 1 || chosen[0] !== "fable") {
    fail("выбор человека не устоял против фактической модели: " + JSON.stringify(chosen));
  }
  const mark = byClass(headPick, "cdlive");
  if (!mark || !dump(mark).includes("opus")) {
    fail("фактическая модель не названа пометкой: " + dump(headPick).slice(0, 200));
  }
  // Совпали выбор с фактической, значит и говорить не о чем.
  const stSame = await sandbox.chatState("demo", "aaaa1111-1111", board);
  stSame.entry = Object.assign({}, stSame.entry, { model: "opus", liveModel: "opus" });
  if (byClass(sandbox.chatPanel("demo", stSame), "cdlive")) {
    fail("пометка стоит там, где выбор и фактическая модель одна");
  }
}

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

// feedOf поднимает ленту на готовом списке записей: сервер тут не нужен, а
// предмет проверки это собранные из записей блоки.
async function feedOf(items, sid) {
  const box = makeNode("div");
  const prev = sandbox.fetch;
  sandbox.fetch = (path, init) => {
    if (path.includes("/sessions/") && !path.includes("stream=1")) {
      return Promise.resolve({ ok: true, status: 200,
        json: () => Promise.resolve({ session: sid, head: { id: sid }, items,
          total: items.length }) });
    }
    return prev(path, init);
  };
  sandbox.wireChatFeed("demo", box, sid);
  await settle();
  sandbox.fetch = prev;
  return box;
}

// --- работа субагента идёт той же лентой, без блока ---
// Свёрнутый блок с заголовком и счётом ходов был нашей выдумкой: человек
// просил видеть работу агента так же, как её видно в vscode. Записи стоят в
// ленте обычными карточками, а принадлежность помечена отступом с точкой.
{
  const sub = [];
  for (let i = 0; i < 12; i++) {
    sub.push(i % 2
      ? { seq: i, key: "a:" + i, role: "toolout", text: "вывод " + i, sub: "работа", time: "2026-08-13T09:00:00+03:00" }
      : { seq: i, key: "a:" + i, role: "tool", tool: "Bash", text: "command: ls " + i, note: "ls", sub: "работа", time: "2026-08-13T09:00:00+03:00" });
  }
  const box = await feedOf(sub, "sub1");
  if (byClass(box, "subblk")) fail("работа субагента снова собралась в блок: " + dump(box).slice(0, 200));
  const lines = allByClass(box, "sub");
  if (lines.length !== 6) fail("строк субагента " + lines.length + ", ожидал шесть карточек");
  const dot = byClass(box, "fdot");
  if (!dot || dot.title !== "субагент: работа") {
    fail("пометка принадлежности не называет заказ: " + (dot && dot.title));
  }
  if (!dump(box).includes("ls")) fail("записи субагента не видны: " + dump(box).slice(0, 200));
}

// --- работа субагента вперемешку с разговором: всё в одной ленте по порядку ---
// Прежде сервер сваливал весь боковой журнал в хвост ленты, и разговор,
// шедший с работой субагента вперемешку, уезжал за тысячу записей вверх: на
// экране это читалось как один пузырь на весь чат. Записи приходят слитыми по
// времени, и лента ставит их подряд, помечая чужие.
{
  const mixed = [];
  let seq = 0;
  const say = (role, text) => mixed.push({ seq: seq, key: "m:" + seq++, role, text,
    time: "2026-08-13T09:00:00+03:00" });
  const work = (n, text) => {
    for (let i = 0; i < n; i++) {
      mixed.push({ seq: seq, key: "a:" + seq++, role: "assistant", text: text + " " + i,
        sub: "работа", time: "2026-08-13T09:00:00+03:00" });
    }
  };
  say("user", "разбери находку");
  work(4, "смотрю дерево");
  say("assistant", "нашёл причину в разборе");
  say("user", "тогда правь и проверь стендом");
  work(3, "правлю разбор");
  const box = await feedOf(mixed, "mix1");
  if (byClass(box, "subblk")) fail("лента снова свернула работу субагента в блок");
  if (allByClass(box, "sub").length !== 7) {
    fail("помечено чужих записей " + allByClass(box, "sub").length + ", ожидал семь");
  }
  const seen = dump(box);
  for (const line of ["разбери находку", "нашёл причину", "тогда правь", "смотрю дерево 0",
    "правлю разбор 2"]) {
    if (!seen.includes(line)) fail("запись ленты потерялась: " + line);
  }
  // Порядок тот же, в каком всё это шло: работа субагента стоит между
  // репликами, а не собрана в конец.
  if (seen.indexOf("смотрю дерево 0") > seen.indexOf("нашёл причину")) {
    fail("работа субагента уехала за реплики: " + seen);
  }
  if (seen.indexOf("тогда правь") > seen.indexOf("правлю разбор 0")) {
    fail("реплика человека уехала за работу субагента: " + seen);
  }
  // Реплики самой сессии ничем не помечены: пометка говорит именно о чужом
  // ходе, а не украшает ленту.
  const own = allByClass(box, "sub").map((n) => dump(n));
  if (own.some((t) => t.includes("разбери находку"))) {
    fail("реплика человека помечена как чужой ход: " + JSON.stringify(own));
  }
}

// --- длинный хвост работы субагента: лента остаётся лентой ---
// У субагента, которого продолжают через SendMessage не первый день, записей
// тысячи. Блок с окном и кнопкой «показать все» их прятал, а человек просил
// обратного: пусть стоят в ленте, как в vscode, и листаются вместе со всем.
{
  const huge = [{ seq: 0, key: "m:0", role: "user", text: "продолжай, я жду отчёта",
    time: "2026-08-13T09:00:00+03:00" }];
  for (let i = 0; i < 120; i++) {
    huge.push({ seq: i + 1, key: "a:" + i, role: "assistant", text: "ход " + i + ".",
      sub: "разбор", time: "2026-08-13T09:00:00+03:00" });
  }
  const box = await feedOf(huge, "huge1");
  if (byClass(box, "subblk") || byClass(box, "submore")) {
    fail("хвост работы субагента снова спрятан блоком с кнопкой");
  }
  if (allByClass(box, "sub").length !== 120) {
    fail("нарисовано чужих записей " + allByClass(box, "sub").length + ", ожидал все 120");
  }
  const seen = dump(box);
  if (!seen.includes("продолжай, я жду отчёта")) fail("реплика человека перед работой пропала");
  if (!seen.includes("ход 0.") || !seen.includes("ход 119.")) {
    fail("края длинной работы не нарисованы: " + seen.slice(0, 200));
  }
}

// --- лента на общей линии: кружок против каждой записи, цвет по исходу ---
// Записи разговора соединены одной вертикальной линией, как в vscode: кружок
// серый у нейтральных, зелёный у сделанного инструментом, красный у упавшего.
{
  const rail = [
    { seq: 0, key: "m:0", role: "user", text: "проверь сборку", time: "2026-08-13T09:00:00+03:00" },
    { seq: 1, key: "m:1", role: "tool", tool: "Bash", note: "go build ./...", text: "command: go build ./...",
      time: "2026-08-13T09:00:01+03:00" },
    { seq: 2, key: "m:2", role: "toolout", text: "готово", time: "2026-08-13T09:00:02+03:00" },
    { seq: 3, key: "m:3", role: "tool", tool: "Bash", note: "go test ./...", text: "command: go test ./...",
      time: "2026-08-13T09:00:03+03:00" },
    { seq: 4, key: "m:4", role: "toolout", text: "FAIL", fail: true, time: "2026-08-13T09:00:04+03:00" },
    { seq: 5, key: "a:0", role: "assistant", text: "смотрю дерево", sub: "разбор",
      time: "2026-08-13T09:00:05+03:00" },
  ];
  const box = await feedOf(rail, "rail1");
  const rows = allByClass(box, "frow");
  if (rows.length !== 4) fail("строк ленты " + rows.length + ", ожидал четыре");
  const kind = (row) => String(byClass(row, "fdot").className).split(" ").slice(1).join("");
  if (kind(rows[0]) !== "") fail("у реплики человека кружок покрашен: " + kind(rows[0]));
  if (kind(rows[1]) !== "ok") fail("удачный ход не помечен зелёным: " + kind(rows[1]));
  if (kind(rows[2]) !== "bad") fail("упавший ход не помечен красным: " + kind(rows[2]));
  // Запись субагента стоит на той же линии, но глубже, и кружок подписан заказом.
  const deep = rows[3];
  if (!String(deep.className).split(" ").includes("sub")) {
    fail("запись субагента не отодвинута вглубь: " + deep.className);
  }
  if (byClass(deep, "fdot").title !== "субагент: разбор") {
    fail("кружок записи субагента не назвал заказ: " + byClass(deep, "fdot").title);
  }
}

// --- нить рвётся по границам работы ---
// Линия связывает события одного захода: реплика человека его начинает,
// финальный текст агента закрывает, между заходами щель. Сплошная нить через
// весь разговор читалась одной бесконечной работой.
{
  const rail = [
    { seq: 0, key: "g:0", role: "user", text: "проверь сборку", time: "2026-08-13T09:00:00+03:00" },
    { seq: 1, key: "g:1", role: "tool", tool: "Bash", note: "go build ./...",
      text: "command: go build ./...", time: "2026-08-13T09:00:01+03:00" },
    { seq: 2, key: "g:2", role: "toolout", text: "готово", time: "2026-08-13T09:00:02+03:00" },
    { seq: 3, key: "g:3", role: "assistant", text: "сборка зелёная", time: "2026-08-13T09:00:03+03:00" },
    { seq: 4, key: "g:4", role: "user", text: "теперь тесты", time: "2026-08-13T09:00:10+03:00" },
    { seq: 5, key: "g:5", role: "tool", tool: "Bash", note: "go test ./...",
      text: "command: go test ./...", time: "2026-08-13T09:00:11+03:00" },
  ];
  const box = await feedOf(rail, "rail-groups");
  const rows = allByClass(box, "frow");
  const has = (row, cls) => String(row.className).split(" ").includes(cls);
  if (!has(rows[0], "gtop")) fail("реплика человека не начала группу: " + rows[0].className);
  if (has(rows[1], "gtop") || has(rows[1], "gend")) {
    fail("ход внутри работы разорвал нить: " + rows[1].className);
  }
  if (!has(rows[2], "gend")) {
    fail("финальный текст агента не закрыл группу: " + rows[2].className);
  }
  if (!has(rows[3], "gtop")) fail("вторая реплика человека не начала группу: " + rows[3].className);
  // Работа идёт, слова агента ещё не сказаны: рвать нить рано.
  if (has(rows[4], "gend")) fail("незакрытая работа закрыта раньше времени: " + rows[4].className);
}

// --- делегирование и конец фоновой работы помечены одной синей меткой ---
// Работа ушла субагенту и вернулась: два конца одного события, и в ленте они
// узнаются с одного взгляда.
{
  const rail = [
    { seq: 0, key: "d:0", role: "tool", tool: "Agent", about: "разбор находки",
      args: { subagent_type: "exec-high", prompt: "Разбери находку." },
      time: "2026-08-13T09:00:00+03:00" },
    { seq: 1, key: "d:1", role: "toolout", text: "агент поднят", time: "2026-08-13T09:00:01+03:00" },
    { seq: 2, key: "d:2", role: "note", note: "Фоновый агент завершил работу", mark: "agent",
      text: "Агент разобрал находку.", time: "2026-08-13T09:01:00+03:00" },
    { seq: 3, key: "d:3", role: "note", text: "Команда /clear", time: "2026-08-13T09:02:00+03:00" },
    { seq: 4, key: "d:4", role: "tool", tool: "Bash", note: "go build ./...",
      text: "command: go build ./...", time: "2026-08-13T09:03:00+03:00" },
    { seq: 5, key: "d:5", role: "toolout", text: "готово", time: "2026-08-13T09:03:01+03:00" },
    { seq: 6, key: "d:6", role: "tool", tool: "SendMessage", note: "правь по замечанию",
      about: "продолжение субагента", text: "message: правь по замечанию",
      args: { message: "правь по замечанию" },
      time: "2026-08-13T09:04:00+03:00" },
    { seq: 7, key: "d:7", role: "toolout", text: "принято", time: "2026-08-13T09:04:01+03:00" },
    { seq: 8, key: "a:0", role: "note", note: "чужая сессия -> субагенту", text: "загляни в LLD",
      sub: "разбор находки", time: "2026-08-13T09:05:00+03:00" },
  ];
  const box = await feedOf(rail, "rail-deleg");
  const rows = allByClass(box, "frow");
  const kind = (row) => String(byClass(row, "fdot").className).split(" ").slice(1).join("");
  if (kind(rows[0]) !== "deleg") fail("делегирование не помечено синим: " + kind(rows[0]));
  if (kind(rows[1]) !== "deleg") fail("конец фоновой работы не помечен синим: " + kind(rows[1]));
  if (kind(rows[2]) === "deleg") fail("обычная служебка покрашена синим: " + kind(rows[2]));
  // Обычный ход метку делегирования не носит: у него прежний исход работы.
  if (kind(rows[3]) !== "ok") fail("обычный инструмент помечен не исходом: " + kind(rows[3]));
  // Продолжение уже поднятого субагента это та же передача работы: метка та же.
  if (kind(rows[4]) !== "deleg") fail("продолжение субагента не помечено синим: " + kind(rows[4]));
  // Рамка чужой сессии часть той же пары: своей карточки в ленте у неё нет.
  if (kind(rows[5]) !== "deleg") fail("реплика субагенту не помечена синим: " + kind(rows[5]));
  // Реплика агенту рисуется как команда: сверху сама реплика, снизу ответ.
  const sendCard = rows[4];
  const inLine = byClass(sendCard, "tin");
  const outLine = byClass(sendCard, "tout");
  if (!inLine || !outLine) fail("реплика агенту стоит не парой вход и выход: " + dump(sendCard));
  if (!dump(inLine).includes("правь по замечанию")) {
    fail("в строке входа не сама реплика: " + dump(inLine));
  }
  if (!dump(outLine).includes("принято")) fail("ответа ручки в карточке нет: " + dump(outLine));
}

// --- конец фоновой работы: один свёрнутый блок с отчётом внутри ---
// Прежде рядом стояли два элемента одного события: служебная строка со сводкой
// и сырой финальный текст субагента. Теперь это один блок, разворот по клику.
{
  const done = sandbox.chatItem({ role: "note", mark: "agent",
    note: "Фоновый агент завершил работу: вычитка готова",
    text: "## Итог\nГотово, семнадцать замечаний.\n\nsha: abc1234" });
  const said = dump(done);
  if (!said.includes("вычитка готова")) fail("в заголовке блока нет сути: " + said);
  if (!said.includes("семнадцать замечаний")) fail("отчёта внутри блока нет: " + said);
  if (!String(done.className).includes("fold")) fail("блок не свёрнут: " + done.className);
  const body = byClass(done, "fmd");
  if (!body || !body.hidden) fail("отчёт открыт сразу, а не по клику: " + said);
  // Разметка отчёта рисуется разметкой, а не сырой простынёй.
  if (!byClass(body, "mdh")) fail("заголовок отчёта не разобран разметкой: " + dump(body));
  byClass(done, "foldh").handlers.click({ stopPropagation: () => {} });
  if (body.hidden) fail("клик не развернул отчёт");
  // Без отчёта блок остаётся одной строкой без разворота.
  const bare = sandbox.chatItem({ role: "note", mark: "agent",
    note: "Фоновый агент завершил работу" });
  if (byClass(bare, "foldc")) fail("у пустого завершения появился разворот: " + dump(bare));
}

// --- план сессии: блок на экране задачи и пустое состояние ---
// План агент пишет себе сам вызовом TodoWrite, сервер отдаёт его последним
// списком. Плана нет, значит и блока нет: заглушка говорила бы о нашей
// бедности, а не о задаче.
{
  const plan = [
    { text: "разобрать находку", state: "completed", active: "Разбираю находку" },
    { text: "починить стрим", state: "in_progress", active: "Чиню стрим" },
    { text: "прогнать стенды", state: "pending", active: "Гоняю стенды" },
  ];
  const list = sandbox.planList(plan);
  const rows = allByClass(list, "prow2");
  if (rows.length !== 3) fail("в списке плана не три пункта: " + rows.length);
  if (!String(rows[0].className).includes("p-completed")) {
    fail("сделанный пункт не помечен состоянием: " + rows[0].className);
  }
  if (!String(rows[1].className).includes("p-in_progress")) {
    fail("идущий пункт не помечен состоянием: " + rows[1].className);
  }
  if (!dump(rows[1]).includes("Чиню стрим")) {
    fail("идущий пункт назван не формой «делаю»: " + dump(rows[1]));
  }
  if (!dump(rows[2]).includes("прогнать стенды")) fail("ждущий пункт пропал: " + dump(rows[2]));
  // Пустой план это пустой список: строить из него блок незачем.
  if (allByClass(sandbox.planList([]), "prow2").length) fail("пустой план нарисовал строки");
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
  // Ход командой стоит заголовком и блоком под ним: в заголовке имя с
  // пояснением хода, в блоке две строки в две колонки, слева направление
  // стрелкой. Копирование при команде, рядом с ним разворот блока на всю
  // высоту содержимого.
  const card = sandbox.toolPair(
    { role: "tool", tool: "Bash", text: "command: ls -la", note: "ls -la",
      about: "смотрю каталог" },
    { role: "toolout", text: "итого 42" });
  const said = dump(card);
  if (!said.includes("Bash") || !said.includes("ls -la") || !said.includes("итого 42")) {
    fail("ход и вывод не встали одной записью: " + said);
  }
  if (said.includes("IN") || said.includes("OUT")) {
    fail("направление снова подписано словами: " + said);
  }
  if (String(card.className).split(" ").includes("tool")) {
    fail("ход снова обёрнут карточкой: " + card.className);
  }
  if (card.children.length !== 2) {
    fail("частей у хода " + card.children.length + ", ожидал две: заголовок и блок");
  }
  const head = byClass(card, "thline");
  const nameEl = tag(head, "B");
  if (!nameEl || nameEl.textContent !== "Bash") {
    fail("имя инструмента стоит не жирным первым: " + dump(head));
  }
  const about = byClass(head, "tabout");
  if (!about || about.textContent !== "смотрю каталог") {
    fail("пояснение хода не встало в заголовок: " + dump(head));
  }
  const body = byClass(card, "tbox");
  const inLine = byClass(body, "tin");
  const outLine = byClass(body, "tout");
  if (!inLine || !outLine) fail("строк команды и вывода нет: " + dump(body));
  for (const [line, what] of [[inLine, "команды"], [outLine, "вывода"]]) {
    const ico = byClass(line, "tico");
    if (!ico || line.children[0] !== ico) {
      fail("у строки " + what + " нет стрелки первой колонкой: " + dump(line));
    }
  }
  if (byClass(inLine, "tcmd").textContent !== "ls -la") {
    fail("в строке команды стоит не команда: " + dump(inLine));
  }
  if (!deepBtn(inLine, "foldcp")) fail("кнопки копирования при команде нет: " + dump(inLine));
  if (deepBtn(outLine, "foldcp")) fail("у вывода завелась своя кнопка копирования");
  const bare = sandbox.toolPair(
    { role: "tool", tool: "Bash", text: "command: go build ./...", note: "go build ./..." },
    { role: "toolout", text: "" });
  if (byClass(bare, "tout")) fail("пустой вывод нарисован строкой: " + dump(bare));

  // Чтение файла это одна строка с именем файла и прочитанным куском, без
  // всякого блока: смотреть в ленте там нечего.
  const read = sandbox.toolPair({ role: "tool", tool: "Read",
    note: "/Users/rider/projects/devkit/docs/map.md",
    args: { file_path: "/Users/rider/projects/devkit/docs/map.md", offset: "10", limit: "20" } }, null);
  const readLine = byClass(read, "tcmd");
  if (!readLine || readLine.textContent !== "map.md (строки 10-29)") {
    fail("строка чтения собрана не по образцу: " + dump(read));
  }
  if (byClass(read, "tbox") || byClass(read, "tdiff")) {
    fail("у чтения файла завёлся блок: " + dump(read));
  }

  // Правка файла это строка «Edit файл», подпись о сделанном и дифф: снятые
  // строки одним цветом, поставленные другим, общие как есть.
  const edit = sandbox.toolPair({ role: "tool", tool: "Edit",
    note: "/Users/rider/projects/devkit/tools/dashboard/static/app.js",
    args: { file_path: "/Users/rider/projects/devkit/tools/dashboard/static/app.js",
      old_string: "общая строка\nбыло тут\nхвост", new_string: "общая строка\nстало тут\nхвост" } },
    { role: "toolout", text: "" });
  if (byClass(edit, "tcmd").textContent !== "app.js") {
    fail("в строке правки стоит не имя файла: " + dump(edit));
  }
  if (!dump(byClass(edit, "tsaid")).includes("Изменено")) {
    fail("правка не подписана сделанным: " + dump(edit));
  }
  const diff = byClass(edit, "tdiff");
  if (!diff) fail("диффа у правки нет: " + dump(edit));
  const kinds = diff.children.map((n) => String(n.className).split(" ")[1]);
  if (kinds.join(",") !== "d-ctx,d-del,d-add,d-ctx") {
    fail("дифф собран не построчным сравнением: " + kinds.join(",") + " | " + dump(diff));
  }
  if (!dump(diff).includes("было тут") || !dump(diff).includes("стало тут")) {
    fail("в диффе нет самих строк правки: " + dump(diff));
  }

  // Запись файла это тот же дифф, где всё поставлено заново, а длинный дифф
  // обрезан по высоте, и его разворачивает кнопка.
  const long = [];
  for (let i = 0; i < 20; i++) long.push("строка " + i);
  const write = sandbox.toolPair({ role: "tool", tool: "Write", note: "docs/new.md",
    args: { file_path: "docs/new.md", content: long.join("\n") } }, null);
  const wdiff = byClass(write, "tdiff");
  if (!wdiff || wdiff.children.some((n) => !String(n.className).includes("d-add"))) {
    fail("запись файла нарисована не одними поставленными строками: " + dump(write));
  }
  if (!String(wdiff.className).includes("cut")) fail("длинный дифф не обрезан по высоте");
  const more = deepBtn(write, "submore");
  if (!more || !String(more.textContent).includes("показать целиком")) {
    fail("кнопки разворота длинного диффа нет: " + dump(write));
  }
  more.handlers.click({ stopPropagation: () => {} });
  if (String(wdiff.className).includes("cut")) fail("кнопка не развернула дифф");

  // Прочие инструменты идут одной строкой со своим главным доводом.
  const grep = sandbox.toolPair({ role: "tool", tool: "Grep", note: "func toolPair" }, null);
  if (byClass(grep, "tcmd").textContent !== "func toolPair" || byClass(grep, "tbox")) {
    fail("прочий инструмент нарисован не одной строкой: " + dump(grep));
  }

  const note = sandbox.chatItem({ role: "note", text: "Фоновый агент завершил работу" });
  if (!dump(note).includes("Фоновый агент")) fail("служебная строка потерялась");
  // Служебка с телом (уведомление о фоновом агенте, реплика работающему
  // субагенту) рисуется тем же видом, что и ход командой: строка заголовка и
  // под ней один блок с содержимым. Стрелок направления тут нет, потому что
  // направления нет (замечание 8).
  const sent = sandbox.chatItem({ role: "note", note: "диспетчер -> субагенту",
    text: "Почини стрим, он молчит." });
  const shown = dump(sent);
  if (!shown.includes("диспетчер -> субагенту")) {
    fail("реплика диспетчера не подписана: " + shown);
  }
  if (shown.includes("sent a message while you were working")) {
    fail("рамка харнеса доехала в ленту: " + shown);
  }
  if (!String(sent.className).split(" ").includes("trow2")) {
    fail("реплика диспетчера нарисована не ходом: " + sent.className);
  }
  const sentBox = byClass(sent, "tbox");
  if (!sentBox || !dump(sentBox).includes("Почини стрим")) {
    fail("текста реплики диспетчера нет в блоке: " + shown);
  }
  if (byClass(sentBox, "tico")) fail("у служебки появилась стрелка направления: " + dump(sentBox));
  if (!deepBtn(sentBox, "foldcp")) fail("в блоке служебки нет копирования: " + dump(sentBox));
  const grow = deepBtn(sentBox, "foldar");
  if (!grow) fail("в блоке служебки нет разворота: " + dump(sentBox));
  grow.handlers.click({ stopPropagation: () => {} });
  if (!String(sentBox.className).split(" ").includes("open")) {
    fail("разворот не раскрыл блок служебки: " + sentBox.className);
  }
  // Реплика субагенту тем же видом приезжает и вызовом SendMessage.
  const dispatch = sandbox.toolPair(
    { role: "tool", tool: "SendMessage", note: "Пять замечаний ревью",
      args: { to: "ac42d29", summary: "Пять замечаний ревью", message: "Разбери каждое замечание." } },
    null);
  const said2 = dump(dispatch);
  if (!said2.includes("SendMessage") || !said2.includes("Разбери каждое замечание.")) {
    fail("реплика субагенту нарисована без текста: " + said2);
  }
  if (!byClass(dispatch, "tbox")) fail("у реплики субагенту нет блока: " + said2);
  // Задание субагенту приезжает тем же блоком: заказ бывает на две страницы, и
  // строкой без разворота он загромождал ленту.
  const task = sandbox.toolPair(
    { role: "tool", tool: "Agent", about: "разбор находки",
      note: "Разбери находку и вернись с причиной",
      args: { subagent_type: "exec-high", description: "разбор находки",
        prompt: "Разбери находку и вернись с причиной." } },
    null);
  const said4 = dump(task);
  if (!said4.includes("Agent") || !said4.includes("Разбери находку и вернись с причиной.")) {
    fail("задание субагенту нарисовано без заказа: " + said4);
  }
  if (!byClass(task, "tbox")) fail("у задания субагенту нет блока: " + said4);
  if (!said4.includes("exec-high")) fail("в заголовке задания нет вида субагента: " + said4);
  // Прежнее имя вызова узнаётся тем же правилом: харнесы зовут его по-разному.
  const old = sandbox.toolPair({ role: "tool", tool: "Task", about: "старое имя вызова",
    args: { subagent_type: "exec-low", prompt: "Сделай рутину." } }, null);
  if (!byClass(old, "tbox") || !dump(old).includes("Сделай рутину.")) {
    fail("вызов по прежнему имени нарисован без блока: " + dump(old));
  }
  // Простыня скилла в ленту не идёт вовсе, от него остаётся строка «Skill имя».
  const skill = sandbox.toolPair(
    { role: "tool", tool: "Skill", note: "board-groom", args: { skill: "board-groom" } },
    { role: "toolout", text: "Launching skill: board-groom" });
  const said3 = dump(skill);
  if (!said3.includes("Skill") || !said3.includes("board-groom")) {
    fail("вызов скилла не назван строкой: " + said3);
  }
  if (byClass(skill, "tbox")) fail("у вызова скилла появился блок: " + said3);
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

// --- вставка картинки: превью в строке отправки и адрес миниатюры ---
{
  const stP = await sandbox.chatState("demo", "aaaa1111-1111", board);
  const panel = sandbox.chatPanel("demo", stP);
  const ta = tag(panel, "TEXTAREA");
  if (!ta || !ta.handlers.paste) fail("поле ввода не слушает вставку");
  // Настоящий PNG одним пикселем: важно, что в src ляжет целый dataURL с
  // префиксом, иначе браузер покажет значок битого изображения.
  const png = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
  ta.handlers.paste({
    preventDefault: () => {},
    clipboardData: { items: [{ type: "image/png", getAsFile: () => ({ name: "снимок.png" }) }] },
  });
  // FileReader в моке синхронный: отдаём dataURL сразу.
  await settle();
  const clip = byClass(panel, "cclip");
  if (!clip) fail("блок превью не встал в строку отправки");
  const img = tag(clip, "IMG");
  if (!img) fail("в блоке превью нет картинки");
  if (!String(img.src).startsWith("data:image/")) {
    fail("в превью не целый dataURL: " + String(img.src).slice(0, 40));
  }
  // Миниатюра в ленте просит картинку у сессии-владельца из самого пути, а не
  // у открытой: иначе ручка отвечает 404 и остаётся битый значок.
  const url = sandbox.shotURL("/Users/rider/.devkit/uploads/zzzz9999-9999/снимок.png");
  if (!url.includes("zzzz9999-9999")) fail("адрес миниатюры бьёт мимо владельца: " + url);
  if (!url.includes("/shot?name=")) fail("адрес миниатюры собран не так: " + url);
}

// --- отправленный снимок остаётся в ленте миниатюрой и разворачивается ---
// Полноразмерная картинка в ленте закрывала бы весь разговор, а маленькая без
// разворота нечитаема: на снимке показывают мелочь вроде сдвинутого кружка.
{
  const item = { role: "user", text: "вот что вижу", time: "2026-08-13T10:02:00+03:00",
    shot: "/Users/rider/.devkit/uploads/aaaa1111-1111/снимок.png" };
  const node = sandbox.chatItem(item);
  const pic = byClass(node, "mshot");
  if (!pic || pic.tagName !== "IMG") fail("снимка миниатюрой в ленте нет: " + dump(node));
  if (!String(pic.src).includes("/shot?name=")) fail("миниатюра просит не ту ручку: " + pic.src);
  const bodyKids = () => sandbox.document.body.children.filter(
    (k) => String(k.className || "").includes("shotbig"));
  if (bodyKids().length) fail("разворот снимка висит на странице до нажатия");
  pic.handlers.click();
  const lens = bodyKids()[0];
  if (!lens) fail("нажатие на миниатюру не развернуло снимок");
  const big = tag(lens, "IMG");
  if (!big || String(big.src) !== String(pic.src)) {
    fail("развёрнут не тот снимок: " + String(big && big.src));
  }
  lens.handlers.click();
  if (bodyKids().length) fail("нажатие по развороту его не закрыло");
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

// --- вложение переживает переключение чата вместе с черновиком ---
{
  const png = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
  const stA = await sandbox.chatState("demo", "aaaa1111-1111", board);
  const one = sandbox.chatPanel("demo", stA);
  tag(one, "TEXTAREA").handlers.paste({
    preventDefault: () => {},
    clipboardData: { items: [{ type: "image/png", getAsFile: () => ({ name: "снимок.png" }) }] },
  });
  await settle();
  if (!byClass(one, "cclip")) fail("картинка не встала в строку отправки");
  // Уход на соседний разговор и возврат: вложение должно вернуться само.
  const stB = await sandbox.chatState("demo", "bbbb2222-2222", board);
  const two = sandbox.chatPanel("demo", stB);
  if (byClass(two, "cclip")) fail("вложение уехало в чужой разговор");
  const back = sandbox.chatPanel("demo", stA);
  const clip = byClass(back, "cclip");
  if (!clip) fail("вложение потерялось при переключении чата");
  const img = tag(clip, "IMG");
  if (!img || String(img.src) !== png) {
    fail("вернулась не та картинка: " + String(img && img.src).slice(0, 40));
  }
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
    ["поиск", "demo/find/" + encodeURIComponent("колокольчик")],
  ]) {
    sandbox.location.hash = "#demo/XR-1/chat/aaaa1111-1111";
    sandbox.goKeepingChat(hash);
    const to = sandbox.location.hash;
    if (!to.includes("/chat/aaaa1111-1111")) fail("переход «" + name + "» потерял чат: " + to);
    if (sandbox.route().chat !== "aaaa1111-1111") fail("после «" + name + "» хвост разобран не так");
  }
}

// --- набор в поиске панель не закрывает ---
// Десять дорог чат переживал, а поиск нет: замена адреса на каждой букве
// хвоста панели не дописывала, и первая же буква сносила разговор с экрана.
{
  sandbox.location.hash = "#demo/find/" + encodeURIComponent("коло") + "/chat/aaaa1111-1111";
  sandbox.findGo("колокольчик");
  const to = sandbox.location.hash;
  if (!to.includes("/chat/aaaa1111-1111")) fail("набор в поиске потерял чат: " + to);
  if (!to.includes("find/")) fail("набор в поиске увёл с выдачи: " + to);
  // Заход в поиск с доски хвост тоже везёт.
  sandbox.location.hash = "#demo/chat/aaaa1111-1111";
  sandbox.findGo("колокольчик");
  if (!sandbox.location.hash.includes("/chat/aaaa1111-1111")) {
    fail("заход в поиск с доски потерял чат: " + sandbox.location.hash);
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

console.log("окно чатов: адреса, фильтр, дорога реплики, модель в строке отправки, шапка с поиском, " +
  "отправка обоих видов чата, оптимистичный пузырь, лента, разметка, выделение, " +
  "хват над полем, черновик, чат из раздела агентов, навигация, память задачи, тихие уведомления");

// --- ожидание в шапке: без чипа, ответ обычным полем ---
// Врезка над полем ввода носила своё поле ответа, и полей в панели выходило
// два. Чип, пришедший ей на смену, говорил то же самое, что кольцо и строка
// состояния рядом, и снят следом. Вопрос с адресом ответа остались подсказкой
// строки состояния, а обычная реплика припаркованной задачи уходит во вход
// строки: живой сессии за ней нет, и адресованную реплику не взял бы никто.
{
  const st = {
    addr: "XR-1", sid: "", task: "XR-1", chats: [], entry: null, fresh: false,
    title: "дашборд без дёрганья", isGoal: false,
    wait: { state: "припаркована вопросом", note: "парковка",
      questions: ["Какой из двух дублей оставить, XR-D2 или XR-D3?"] },
  };
  const panel = sandbox.chatPanel("demo", st);
  if (byClass(panel, "wcard")) fail("врезка вопроса снова стоит в панели");
  const fields = (node) => (node.tagName === "TEXTAREA" ? 1 : 0) +
    (node.children || []).reduce((n, kid) => n + fields(kid), 0);
  if (fields(panel) !== 1) fail("полей ввода в панели не одно: " + fields(panel));
  const head = sandbox.chatHead("demo", st);
  if (byClass(head, "c-wait")) fail("чип ожидания снова стоит в шапке: " + dump(head).slice(0, 200));
  const tip = sandbox.waitChatTip(st, st.wait);
  for (const want of ["парковка", "Какой из двух дублей", "во вход задачи XR-1"]) {
    if (!tip.includes(want)) fail("в подсказке ожидания нет " + want + ": " + tip);
  }
  // Обычное поле панели уходит ручкой задачи, а не подъёмом нового чата.
  posted.length = 0;
  const ta = tag(panel, "TEXTAREA");
  ta.value = "оставить XR-D2, второй дубль снять";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!posted.some((p) => p.includes("/tasks/XR-1/message"))) {
    fail("реплика ушла не ручкой задачи: " + JSON.stringify(posted));
  }
  if (posted.some((p) => p.endsWith("/chats"))) fail("реплика подняла новый чат вместо ответа строке");
  // Живая сессия за задачей забирает реплику себе: ждать может и она сама.
  const live = Object.assign({}, st, { sid: "aaaa1111-1111",
    entry: { id: "aaaa1111-1111", state: "live", tasks: ["XR-1"] } });
  if (sandbox.chatWay(live).kind !== "say") {
    fail("реплика живой сессии ушла мимо неё: " + sandbox.chatWay(live).kind);
  }
  if (!sandbox.waitChatTip(live, live.wait).includes("живой сессии")) {
    fail("подсказка живой сессии не назвала адрес ответа: " + sandbox.waitChatTip(live, live.wait));
  }
  // У цели ожидание тоже видно, а вот ручкой задачи её реплики не уходят:
  // цель ведут своим ходом, и подсказка говорит про сессию.
  const goal = Object.assign({}, st, { isGoal: true });
  const gtip = sandbox.waitChatTip(goal, goal.wait);
  if (!gtip) fail("подсказка ожидания у цели пропала");
  if (gtip.includes("во вход задачи")) fail("подсказка цели обещает ручку задачи: " + gtip);
  if (sandbox.chatWay(goal).kind === "task") fail("реплика цели ушла во вход задачи");
  // Состояние разговора из метаданных снято: кольцо со строкой состояния уже
  // сказали его, а третьим оно спорило с ними (агент работает, метаданные
  // говорят «ждёт реплики»).
  const meta = byClass(sandbox.chatHead("demo", live), "cmeta");
  if (meta && String(meta.textContent).includes("ждёт реплики")) {
    fail("состояние разговора вернулось в метаданные: " + meta.textContent);
  }
}

console.log("ожидание задачи: без чипа, вопрос с адресом ответа подсказкой строки состояния, " +
  "поле ввода одно, реплика припаркованной строки уходит ручкой задачи");

// --- постановка на форме: один блок в каждом режиме ---
// Описание стояло на экране дважды, разметкой и полем ввода разом, и правился
// нижний блок: правило display у поля перебивало признак hidden.
{
  const detail = { file: "docs/tasks/XR-1.md", text: "# XR-1\nтекст постановки" };
  const shown = (card, mode) => {
    const view = byClass(card, "fview");
    const ta = tag(card, "TEXTAREA");
    if (!view || !ta) fail("в постановке нет пары «разметка и поле»: " + mode);
    const live = [!view.hidden, !ta.hidden].filter(Boolean).length;
    if (live !== 1) fail("в режиме «" + mode + "» блоков описания видно " + live + ", ожидал один");
    return { view, ta };
  };
  const read = sandbox.filePanel("demo", "XR-1", detail, { text: detail.text },
    () => {}, false, false);
  const r = shown(read, "просмотр");
  if (r.view.hidden || !r.ta.hidden) fail("по умолчанию открыт не просмотр разметкой");
  const edit = sandbox.filePanel("demo", "XR-1", detail, { text: detail.text },
    () => {}, true, false);
  const e = shown(edit, "правка");
  if (!e.view.hidden || e.ta.hidden) fail("по карандашу открыт не режим правки");
  // Переключение идёт по месту, а не перерисовкой экрана.
  read.setEdit(true);
  shown(read, "переключение карандашом");
}

console.log("постановка на форме: один блок описания в каждом режиме, просмотр по умолчанию");

// --- боковой журнал: пузырей человека из него не бывает ---
// Заказ субагенту лежит первой записью его журнала ролью user, и после слияния
// он рисовался жёлтой простынёй, будто это сказал человек. Тот же текст уже
// стоит компактной карточкой вызова Agent, второй раз он не нужен.
{
  const feed = [
    { seq: 0, key: "m:0", role: "user", text: "вычитай LLD", time: "2026-08-21T10:00:00+03:00" },
    { seq: 1, key: "m:1", role: "tool", tool: "Agent", note: "Вычитка LLD DK-459",
      about: "Вычитка LLD DK-459", text: "description: Вычитка LLD DK-459",
      time: "2026-08-21T10:00:01+03:00" },
    { seq: 2, key: "a:0", role: "assistant", text: "смотрю документ", sub: "вычитка",
      time: "2026-08-21T10:00:02+03:00" },
    { seq: 3, key: "a:1", role: "note", note: "диспетчер -> субагенту", text: "поторопись",
      sub: "вычитка", time: "2026-08-21T10:00:03+03:00" },
  ];
  const box = await feedOf(feed, "sub-order");
  // Пузырь человека тут ровно один, свой: у него свой класс, и записи
  // субагента в него попадать не должны никогда.
  const mine2 = allByClass(box, "me");
  if (mine2.length !== 1) fail("пузырей человека в ленте " + mine2.length + ", ожидал один");
  if (!dump(mine2[0]).includes("вычитай LLD")) {
    fail("пузырь остался не от человека: " + dump(mine2[0]));
  }
  const said = dump(box);
  if (!said.includes("Вычитка LLD DK-459")) fail("карточка вызова Agent пропала: " + said.slice(0, 200));
  // Реплика диспетчера субагенту служебной строкой, а не пузырём.
  // Реплика диспетчера субагенту это служебная строка со своей подписью, а не
  // пузырь: сказал её не человек этого чата.
  const svc = allByClass(box, "frow").filter((n) => dump(n).includes("диспетчер"));
  if (!svc.length) fail("реплики диспетчера в ленте нет: " + said.slice(0, 250));
  if (byClass(svc[0], "me")) fail("реплика диспетчера нарисована пузырём человека");
}

console.log("боковой журнал: заказ субагенту не двоится пузырём, чужие реплики служебной строкой");
