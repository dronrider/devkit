// Стенд группировки и замера порядка в списке чатов (DK-656).
//
// Живой случай, три части. Список сортируется по активности, и открытый сейчас
// разговор оказывался где-то в середине: до него приходилось долистывать.
// Живая сессия шла первой группой без подписи, и трёхдневной давности живой
// чат вставал выше сегодняшнего мёртвого, а заголовок «сегодня» съезжал
// вниз, будто сортировка сбоила. Уборка пачкой в архив перерисовывала список
// на каждое нажатие, и соседняя строка успевала встать под курсор.
//
// Предмет стенда: открытый список начинается с текущего разговора отдельной
// подписанной группой, под ней подписанная группа живых, дальше дни. Пока
// список открыт, уборка гасит строку на месте и не переставляет соседей;
// новый порядок берётся только следующим открытием.
//
// Зовётся: node testdata/poc_chatgroup.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [] };
const hour = 3600 * 1000;
const stamp = (ms) => new Date(Date.now() - ms).toISOString();

const CUR = "aaaa1111-1111";
const LIVE = "bbbb2222-2222";
const TODAY = "cccc3333-3333";
const YDAY = "dddd4444-4444";

// Живая сессия трёхдневной давности стоит рядом с сегодняшними мёртвыми
// разговорами: живая случая ровно тот порядок, который до правки читался как
// поломка сортировки.
const chats = [
  { id: CUR, project: "demo", title: "текущий разговор", state: "dead",
    mtime: stamp(2 * hour), tasks: [] },
  { id: LIVE, project: "demo", title: "живая сессия", state: "live", idle: true,
    mtime: stamp(72 * hour), tasks: [] },
  { id: TODAY, project: "demo", title: "сегодняшний разговор", state: "dead",
    mtime: stamp(3 * hour), tasks: [] },
  { id: YDAY, project: "demo", title: "вчерашняя беседа", state: "dead",
    mtime: stamp(26 * hour), tasks: [] },
];

let sent = null;
// Ручка архива держит признак так же, как сервер: следующая перечитка списка
// (её зовёт refresh после каждой уборки) обязана видеть то же, что уже
// нарисовано, иначе стенд гонялся бы за своим же мороком.
const { sandbox, store } = makeSandbox(app, (path, init) => {
  const m = /\/chats\/([^/]+)\/archive$/.exec(path);
  if (m) {
    sent = { path, body: JSON.parse((init && init.body) || "{}") };
    const row = chats.find((c) => c.id === m[1]);
    if (row) row.archived = sent.body.archived;
    return { session: "x", archived: sent.body.archived, message: "готово" };
  }
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});
store.set("devkit.chat.arch", "off");

sandbox.location.hash = "#demo/chat/" + CUR;
const st = await sandbox.chatState("demo", CUR, board);

const anchor = makeNode("div");
const open = () => {
  sandbox.chatDropShut();
  sandbox.chatDropOpen("demo", st, anchor);
  const drop = anchor.children[anchor.children.length - 1];
  return { drop, rows: byClass(drop, "cdrows") };
};

// Заголовки групп и порядок строк снимаются одним обходом: заголовок это узел
// с классом cdday, строка это cdrow, и оба идут вперемешку в порядке показа.
const heads = (rows) => allByClass(rows, "cdday").map((n) => dump(n).trim());
const titles = (rows) => allByClass(rows, "cdrow").map((n) => dump(n));
const has = (rows, what) => titles(rows).some((said) => said.includes(what));

// --- порядок групп: текущий разговор, живые, дни ---
{
  const { rows } = open();
  const got = heads(rows);
  if (got.length !== 4 || got[0] !== "текущий разговор" || got[1] !== "живые") {
    fail("порядок подписей групп не текущий+живые+дни: " + JSON.stringify(got));
  }
  const all = titles(rows);
  const rowNodes = allByClass(rows, "cdrow");
  if (!all[0].includes("текущий разговор") || !String(rowNodes[0].className).includes(" on")) {
    fail("первая строка списка не текущий разговор с подсветкой: " +
      all[0].slice(0, 200) + " | " + rowNodes[0].className);
  }
  if (!all[1].includes("живая сессия")) {
    fail("группа живых не сразу после текущего разговора: " + JSON.stringify(all.map((s) => s.slice(0, 60))));
  }
  // Живая сессия трёхдневной давности не должна вставать перед «сегодня»:
  // подпись группы решает порядок чтения, а не возраст записи внутри неё.
  const todayAt = all.findIndex((s) => s.includes("сегодняшний разговор"));
  const liveAt = all.findIndex((s) => s.includes("живая сессия"));
  if (liveAt > todayAt) fail("живая сессия оказалась после «сегодня»: " + JSON.stringify(all));
}

// --- уборка гасит строку на месте, соседей не переставляет ---
{
  const { rows } = open();
  const before = titles(rows).length;
  const row = allByClass(rows, "cdrow").find((n) => dump(n).includes("сегодняшний разговор"));
  if (!row) fail("строки сегодняшнего разговора нет: " + dump(rows).slice(0, 300));
  const put = byClass(row, "cdarch");
  sent = null;
  put.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sent || sent.body.archived !== true) fail("уборка не позвала ручку архива: " + JSON.stringify(sent));
  if (titles(rows).length !== before) {
    fail("уборка изменила число строк на месте: было " + before + ", стало " + titles(rows).length);
  }
  if (!has(rows, "сегодняшний разговор")) {
    fail("убранная строка пропала сразу, а должна погаснуть на месте: " + dump(rows).slice(0, 300));
  }
  const still = allByClass(rows, "cdrow").find((n) => dump(n).includes("сегодняшний разговор"));
  if (!String(still.className).includes("arch")) {
    fail("убранная строка не помечена архивной: " + still.className);
  }
  // Соседняя строка (вчерашняя беседа) осталась на своём месте, а не встала
  // туда, где была убранная.
  const order = titles(rows).map((s) => s.slice(0, 40));
  if (!has(rows, "вчерашняя беседа") || order[order.length - 1].indexOf("вчерашняя") === -1) {
    fail("соседняя строка сдвинулась после уборки: " + JSON.stringify(order));
  }
}

// --- новый порядок берётся только следующим открытием ---
{
  const { rows } = open();
  if (has(rows, "сегодняшний разговор")) {
    fail("после переоткрытия убранный разговор должен уйти (положение «не показывать»): " +
      dump(rows).slice(0, 300));
  }
  if (titles(rows).length !== 3) {
    fail("после переоткрытия список не пересчитался: строк " + titles(rows).length);
  }
}

console.log("poc_chatgroup: ok, текущий разговор и живые стоят подписанными группами сверху, " +
  "а уборка гасит строку на месте до следующего открытия");
