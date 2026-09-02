// Стенд поиска чата сквозь архив (DK-726).
//
// Кнопка архива режет список только для глаза, а поиск искал уже урезанный
// набор: строка `list = chatArchShown(st.chats, chatArchMode())` в
// static/app.js стояла раньше запроса, и убранный разговор не находился
// набранным текстом, пока человек не провёл кнопку по кругу. Пустая выдача
// при этом молчала, сколько разговоров вообще скрыто. Стенд поднимает
// настоящий static/app.js в node с заглушкой DOM (браузера в прогоне нет) и
// повторяет оба случая.
//
// Зовётся: node testdata/poc_chatfind.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, byClass, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };
const hour = 3600 * 1000;
const stamp = (ms) => new Date(Date.now() - ms).toISOString();

const archived1 = { id: "aaaa1111-1111", project: "demo",
  title: "разбор границы окна DK-726", mtime: stamp(hour), state: "dead",
  tasks: [], archived: true };
const archived2 = { id: "bbbb2222-2222", project: "demo",
  title: "второй убранный разговор", mtime: stamp(2 * hour), state: "dead",
  tasks: [], archived: true };
const live = { id: "cccc3333-3333", project: "demo",
  title: "сегодняшний разговор", mtime: stamp(3 * hour), state: "dead", tasks: [] };

// world.chats это то, что отдаёт ручка списка: стенд меняет набор между
// сценариями, а ручка каждый раз читает актуальный.
const world = { chats: [] };
const { sandbox, store } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats: world.chats, models: [], days: 3, older: false };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});
store.set("devkit.chat.arch", "off");

// Адрес панели не совпадает ни с одной строкой набора: пришпиливания текущего
// разговора тут нет, и список это ровно то, что даёт кнопка архива.
sandbox.location.hash = "#demo/chat/board";
const st = await sandbox.chatState("demo", "board", board);

const anchor = makeNode("div");
const open = () => {
  sandbox.chatDropShut();
  sandbox.chatDropOpen("demo", st, anchor);
  const drop = anchor.children[anchor.children.length - 1];
  return { drop, rows: byClass(drop, "cdrows") };
};
const titles = (rows) => rows.querySelectorAll(".cdrow").map((n) => dump(n));
const has = (rows, what) => titles(rows).some((said) => said.includes(what));

// --- запрос находит архивную запись при умолчании кнопки, и метит её ---
world.chats = [archived1, live];
st.chats = world.chats;
{
  const { rows: hidden } = open();
  if (has(hidden, "разбор границы")) {
    fail("умолчание кнопки не спрятало архивную запись: " + dump(hidden).slice(0, 300));
  }

  const { drop, rows } = open();
  const find = tag(drop, "INPUT");
  find.value = "разбор границы";
  find.handlers.input();
  await settle();
  if (!has(rows, "разбор границы окна DK-726")) {
    fail("поиск не нашёл архивную запись при спрятанном архиве: " + dump(rows).slice(0, 300));
  }
  if (!dump(rows).includes("в архиве")) {
    fail("найденная архивная строка не помечена клеймом «в архиве»: " + dump(rows).slice(0, 300));
  }
  const row = rows.querySelectorAll(".cdrow").find((n) => dump(n).includes("разбор границы"));
  if (!row || !String(row.className).includes("arch")) {
    fail("архивная строка не отмечена классом гашения: " + (row ? dump(row) : "строки нет"));
  }

  // Запрос, задевающий только живой разговор, архивную запись не тащит.
  find.value = "сегодняшний";
  find.handlers.input();
  await settle();
  if (has(rows, "разбор границы")) {
    fail("посторонний запрос всё равно вытащил архивную запись: " + dump(rows).slice(0, 300));
  }
  if (!has(rows, "сегодняшний разговор")) {
    fail("запрос не нашёл собственный разговор: " + dump(rows).slice(0, 300));
  }
}

// --- пустая выдача при спрятанном архиве называет число скрытого словами ---
world.chats = [archived1, archived2];
st.chats = world.chats;
{
  const { rows } = open();
  if (rows.querySelectorAll(".cdrow").length) {
    fail("два архивных разговора неожиданно попали в список без запроса: " + dump(rows).slice(0, 300));
  }
  const said = dump(rows);
  if (!said.includes("2") || !said.includes("архив")) {
    fail("пустая выдача не назвала число скрытых архивом разговоров: " + said);
  }
}

console.log("ok: поиск находит архивную запись при спрятанной кнопке, метит её клеймом «в архиве», " +
  "и пустая выдача называет число скрытых архивом разговоров");
