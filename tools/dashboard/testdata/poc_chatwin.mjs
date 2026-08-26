// Стенд окна списка диалогов (замечание пользователя: список разросся). На
// живой машине в общем списке сто сорок пять разговоров, и человек ходит в
// него за сегодняшним. Панель берёт окно свежести, кнопка «показать раньше»
// догружает следующую ступень, а поиск окна не знает вовсе: набрал запрос,
// значит ищется по всей машине.
//
// Зовётся: node testdata/poc_chatwin.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, tag, byClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };
const hour = 3600 * 1000;
const stamp = (ms) => new Date(Date.now() - ms).toISOString();

// Окно сервера: свежий разговор плюс живой старик. Дальше в глубине лежат
// разговоры недельной и месячной давности, и приезжают они ступенями.
const win = [
  { id: "aaaa1111-1111", project: "demo", title: "сегодняшний разговор",
    mtime: stamp(hour), state: "dead", tasks: [] },
  { id: "cccc3333-3333", project: "demo", title: "старый, но живой",
    mtime: stamp(240 * hour), state: "live", idle: true, tasks: [] },
];
const week = win.concat([
  { id: "bbbb2222-2222", project: "demo", title: "беседа пятидневной давности",
    mtime: stamp(120 * hour), state: "dead", tasks: [] },
]);
const all = week.concat([
  { id: "dddd4444-4444", project: "other", title: "разбор релиза прошлого месяца",
    mtime: stamp(720 * hour), state: "dead", tasks: [] },
]);

const daysOf = (path) => {
  const m = /[?&]days=(\d+)/.exec(path);
  return m ? Number(m[1]) : null;
};

const { sandbox, asked, store } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) {
    const days = daysOf(path);
    if (days === null) return { chats: win, models: [], days: 3, older: true };
    if (days === 0) return { chats: all, models: [], days: 0, older: false };
    if (days === 7) return { chats: week, models: [], days: 7, older: true };
    return { chats: all, models: [], days, older: false };
  }
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});
store.set("devkit.chat.filter", "0");

// --- панель берёт окно сервера, а не весь список ---
sandbox.location.hash = "#demo/chat/board";
const st = await sandbox.chatState("demo", "board", board);
const first = asked.filter((p) => p.includes("/chats?all=1"));
if (!first.length) fail("список взят не общей ручкой машины: " + JSON.stringify(asked));
if (first.some((p) => p.includes("days="))) {
  fail("панель сама назначила окно, а рубеж живёт одним местом на сервере: " + first[0]);
}
if (st.chats.length !== 2) fail("в состоянии не окно сервера: " + st.chats.length);

const anchor = makeNode("div");
sandbox.chatDropOpen("demo", st, anchor);
const drop = anchor.children[anchor.children.length - 1];
const rows = byClass(drop, "cdrows");
const rowsOf = () => rows.querySelectorAll(".cdrow");
if (rowsOf().length !== 2) fail("список показал не окно: " + rowsOf().length);

// --- «показать раньше» догружает следующую ступень ---
const more = deepBtn(drop, "показать раньше");
if (!more) fail("кнопки «показать раньше» нет, а раньше разговоры есть: " + dump(rows).slice(0, 300));
more.handlers.click({});
await settle();
if (!asked.some((p) => p.includes("days=7"))) {
  fail("кнопка не сходила за следующей ступенью окна: " + JSON.stringify(asked));
}
if (rowsOf().length !== 3) fail("догруженное окно не встало в список: " + rowsOf().length);
if (!dump(rows).includes("беседа пятидневной давности")) {
  fail("разговор следующего окна не показан: " + dump(rows).slice(0, 300));
}

// --- поиск идёт по всей машине, а не по загруженному окну ---
const find = tag(drop, "INPUT");
find.value = "релиза";
find.handlers.input();
await settle();
if (!asked.some((p) => p.includes("days=0"))) {
  fail("поиск остался в окне и за полным списком не сходил: " + JSON.stringify(asked));
}
if (rowsOf().length !== 1 || !dump(rows).includes("разбор релиза прошлого месяца")) {
  fail("поиск не нашёл разговор глубже окна: " + dump(rows).slice(0, 300));
}
// За поиском кнопки окна нет: список и так приехал целиком.
if (deepBtn(drop, "показать раньше")) {
  fail("за поиском по всей машине висит кнопка «показать раньше»");
}

// --- стёртый запрос оставляет догруженный список, второго захода нет ---
const asksBefore = asked.filter((p) => p.includes("/chats")).length;
find.value = "";
find.handlers.input();
await settle();
if (asked.filter((p) => p.includes("/chats")).length !== asksBefore) {
  fail("стирание запроса погнало ещё один заход за списком");
}
if (rowsOf().length !== 4) fail("догруженный список после стирания запроса потерялся: " + rowsOf().length);

console.log("ok: панель берёт окно сервера, «показать раньше» ведёт по ступеням, " +
  "поиск догружает всю машину и находит разговор глубже окна");
