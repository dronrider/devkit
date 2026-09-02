// Стенд уборки разговоров в архив (требование пользователя: после разбора
// десяти черновиков десять отработавших чатов мозолят глаза, а окно по свежести
// их не прячет, они свежие). Убирают из самой строки списка, показ архивных
// правит кнопка о трёх положениях справа от поиска, положение переживает
// перезагрузку, и поиск считается с ним же.
//
// Зовётся: node testdata/poc_chatarch.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, tag, byClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };
const hour = 3600 * 1000;
const stamp = (ms) => new Date(Date.now() - ms).toISOString();

// Два убранных разговора и два обычных. Один из убранных живой: положение
// «показывать все» оставляет его в списке живым, возраст и уборка тут не судьи.
const chats = [
  { id: "aaaa1111-1111", project: "demo", title: "разобранный черновик",
    mtime: stamp(2 * hour), state: "dead", tasks: [], archived: true },
  { id: "bbbb2222-2222", project: "demo", title: "сегодняшний разговор",
    mtime: stamp(3 * hour), state: "dead", tasks: [] },
  { id: "cccc3333-3333", project: "demo", title: "живой убранный",
    mtime: stamp(200 * hour), state: "live", idle: true, tasks: [], archived: true },
  { id: "dddd4444-4444", project: "demo", title: "вчерашняя беседа",
    mtime: stamp(26 * hour), state: "dead", tasks: [] },
];

// Что уехало ручкой архива: последний адрес и последнее тело.
let sent = null;
const { sandbox, store } = makeSandbox(app, (path, init) => {
  if (path.includes("/archive")) {
    sent = { path, body: JSON.parse((init && init.body) || "{}") };
    return { session: "x", archived: sent.body.archived, message: "готово" };
  }
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});
store.set("devkit.chat.filter", "0");

sandbox.location.hash = "#demo/chat/board";
const st = await sandbox.chatState("demo", "board", board);

// Список открывается заново: так его видит человек, вернувшийся к панели, и так
// же он перечитывает положение кнопки из памяти вкладки.
const anchor = makeNode("div");
const open = () => {
  sandbox.chatDropShut();
  sandbox.chatDropOpen("demo", st, anchor);
  const drop = anchor.children[anchor.children.length - 1];
  return { drop, rows: byClass(drop, "cdrows") };
};
const titles = (rows) => rows.querySelectorAll(".cdrow").map((n) => dump(n));
const has = (rows, what) => titles(rows).some((said) => said.includes(what));

// --- по умолчанию архивных в списке нет ---
{
  const { drop, rows } = open();
  if (titles(rows).length !== 2) {
    fail("умолчание показало не два неубранных разговора: " + titles(rows).length);
  }
  if (has(rows, "разобранный черновик") || has(rows, "живой убранный")) {
    fail("убранные разговоры остались в списке: " + dump(rows).slice(0, 300));
  }
  // Кнопка положений стоит справа от поиска, в той же строке.
  const top = byClass(drop, "cdtop");
  if (!top) fail("шапки списка с полем поиска и кнопкой нет: " + dump(drop).slice(0, 300));
  if (!tag(top, "INPUT")) fail("поле поиска ушло из шапки списка: " + dump(top).slice(0, 200));
  const btn = byClass(top, "cdarchbtn");
  if (!btn) fail("кнопки архивных рядом с поиском нет: " + dump(top).slice(0, 200));
  if (btn.attrs["data-mode"] !== "off") {
    fail("умолчание кнопки не «не показывать»: " + btn.attrs["data-mode"]);
  }
}

// --- три положения дают три разных набора ---
{
  const seen = {};
  for (const want of ["all", "only", "off"]) {
    const { drop, rows } = open();
    byClass(drop, "cdarchbtn").handlers.click({ stopPropagation: () => {} });
    const now = byClass(drop, "cdarchbtn").attrs["data-mode"];
    if (now !== want) fail("кнопка встала не в ожидаемое положение: " + now + ", ждал " + want);
    // Наборы сверяются составом, а не длиной: «только архивные» и «без
    // архивных» бывают одной длины и при этом не пересекаются вовсе.
    seen[want] = titles(rows).sort().join("|");
    if (want === "all") {
      if (titles(rows).length !== 4) fail("«показывать все» показало не весь список: " + titles(rows).length);
      if (!has(rows, "живой убранный")) fail("живой убранный пропал из «показывать все»");
      // Живой стоит группой сверху и убранным быть не перестал: до первого
      // заголовка дня стоят живые, а не «сегодня».
      const kids = rows.children;
      const firstHead = kids.findIndex((n) => String(n.className).includes("cdday"));
      if (firstHead !== 1 || !dump(kids[0]).includes("живой убранный")) {
        fail("живой убранный не стоит группой живых сверху: " + dump(rows).slice(0, 300));
      }
    }
    if (want === "only") {
      if (titles(rows).length !== 2) fail("«только архивные» показало не два убранных: " + titles(rows).length);
      if (has(rows, "сегодняшний разговор")) fail("в «только архивные» попал обычный разговор");
      if (!has(rows, "разобранный черновик")) fail("убранный разговор не показан в своём положении");
    }
    if (want === "off" && (titles(rows).length !== 2 || has(rows, "живой убранный"))) {
      fail("круг положений не вернулся к списку без архивных: " + dump(rows).slice(0, 300));
    }
  }
  if (seen.all === seen.only || seen.only === seen.off || seen.all === seen.off) {
    fail("три положения дали не три разных набора: " + JSON.stringify(seen));
  }
}

// --- положение переживает перезагрузку страницы ---
{
  const { drop } = open();
  byClass(drop, "cdarchbtn").handlers.click({ stopPropagation: () => {} });
  if (store.get("devkit.chat.arch") !== "all") {
    fail("положение не легло в память вкладки: " + JSON.stringify(store.get("devkit.chat.arch")));
  }
  // Перезагрузка это новое открытие списка с той же памятью: положение берётся
  // оттуда, а не с умолчания.
  const again = open();
  if (byClass(again.drop, "cdarchbtn").attrs["data-mode"] !== "all") {
    fail("после перезагрузки кнопка вернулась к умолчанию");
  }
  if (titles(again.rows).length !== 4) {
    fail("после перезагрузки список не в сохранённом положении: " + titles(again.rows).length);
  }
  store.set("devkit.chat.arch", "off");
}

// --- поиск идёт мимо положения кнопки (DK-726), сама кнопка правит только
// пустой список ---
{
  const { drop, rows } = open();
  const find = tag(drop, "INPUT");
  find.value = "черновик";
  find.handlers.input();
  await settle();
  if (!has(rows, "разобранный черновик")) {
    fail("поиск не нашёл убранный разговор в положении «не показывать»: " + dump(rows).slice(0, 300));
  }
  if (!dump(rows).includes("в архиве")) {
    fail("найденная архивная строка не помечена клеймом: " + dump(rows).slice(0, 300));
  }
  find.value = "";
  find.handlers.input();
  await settle();
  if (has(rows, "разобранный черновик")) {
    fail("стёртый запрос не вернул кнопке прежнюю власть над списком: " + dump(rows).slice(0, 300));
  }
}

// --- уборка идёт из строки списка и уносит строку ---
{
  const { rows } = open();
  const row = rows.querySelectorAll(".cdrow").find((n) => dump(n).includes("сегодняшний разговор"));
  if (!row) fail("строки обычного разговора в списке нет: " + dump(rows).slice(0, 300));
  const put = byClass(row, "cdarch");
  if (!put) fail("действия уборки в строке списка нет: " + dump(row));
  sent = null;
  put.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sent || !sent.path.includes("/chats/bbbb2222-2222/archive")) {
    fail("уборка не позвала ручку архива: " + JSON.stringify(sent));
  }
  if (sent.body.archived !== true) fail("ручке уехала не уборка: " + JSON.stringify(sent.body));
  if (has(rows, "сегодняшний разговор")) {
    fail("убранный разговор остался в списке: " + dump(rows).slice(0, 300));
  }
  if (titles(rows).length !== 1) fail("список после уборки не сократился: " + titles(rows).length);
}

// --- дорога назад: тем же действием из архива ---
{
  store.set("devkit.chat.arch", "only");
  const { rows } = open();
  const row = rows.querySelectorAll(".cdrow").find((n) => dump(n).includes("разобранный черновик"));
  if (!row) fail("убранного разговора нет в положении «только архивные»: " + dump(rows).slice(0, 300));
  if (!dump(row).includes("в архиве")) fail("строка не помечена архивной: " + dump(row));
  sent = null;
  byClass(row, "cdarch").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sent || sent.body.archived !== false) {
    fail("возврат из архива не позвал ручку со снятым признаком: " + JSON.stringify(sent));
  }
  if (has(rows, "разобранный черновик")) {
    fail("вернувшийся разговор остался среди архивных: " + dump(rows).slice(0, 300));
  }
  store.set("devkit.chat.arch", "off");
  const back = open();
  if (!has(back.rows, "разобранный черновик")) {
    fail("вернувшийся из архива разговор не встал в обычный список: " + dump(back.rows).slice(0, 300));
  }
}

console.log("ok: убирают из строки списка, три положения кнопки дают три набора, " +
  "положение переживает перезагрузку, поиск считается с ним, дорога назад та же");
