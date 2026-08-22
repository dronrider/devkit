// Стенд общего списка чатов (требование пользователя): выпадающий список
// диалогов собирается по всей машине, а не по проекту. У каждой строки виден
// её проект, поиск находит разговор по имени проекта, фильтр по задаче стоит
// запросом поиска, а не жёсткой отсечкой, и переключение на чат чужого
// проекта не требует смены проекта доски: проект едет в самом адресе.
//
// Зовётся: node testdata/poc_allchats.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "своя задача", sect: "in-progress" },
] }] };

// Свежайший разговор машины живёт в чужом проекте: панель доски demo не
// вправе открывать его по умолчанию, но обязана показывать в списке.
const chats = [
  { id: "ffff9999-9999", project: "other", title: "чужой свежий разговор",
    mtime: "2026-08-14T10:00:00+03:00", state: "live", tasks: ["ZZ-7"], idle: true },
  { id: "aaaa1111-1111", project: "demo", title: "свой разговор по XR-1",
    mtime: "2026-08-13T10:00:00+03:00", state: "dead", tasks: ["XR-1"] },
  { id: "bbbb2222-2222", project: "demo", title: "свой свободный разговор",
    mtime: "2026-08-12T10:00:00+03:00", state: "dead", tasks: [] },
];

const { sandbox, asked, store } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats, models: [] };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});

// Первая часть смотрит весь список: фильтр по задаче выключен, он предмет
// отдельной проверки ниже.
store.set("devkit.chat.filter", "0");

// --- список приезжает общей ручкой машины ---
sandbox.location.hash = "#demo/chat/board";
const st = await sandbox.chatState("demo", "board", board);
if (!asked.some((p) => p.includes("/chats?all=1"))) {
  fail("панель берёт список не общей ручкой машины: " + JSON.stringify(asked));
}

// --- выбор по умолчанию остаётся в своём проекте ---
if (st.sid !== "aaaa1111-1111") {
  fail("панель доски открыла не свой свежий разговор: " + st.sid);
}

// --- строки списка подписаны проектом ---
const anchor = makeNode("div");
sandbox.chatDropOpen("demo", st, anchor);
const drop = anchor.children[anchor.children.length - 1];
const rows = drop.children[1];
if (rows.children.length !== 3) fail("в общем списке не все проекты: " + rows.children.length);
const saidRows = dump(rows).replace(/\s+/g, " ");
if (!saidRows.includes("other") || !saidRows.includes("demo")) {
  fail("принадлежность строк не видна: " + saidRows.slice(0, 300));
}

// --- поиск находит разговор по имени проекта ---
const find = drop.children[0];
find.value = "other";
find.handlers.input();
if (rows.children.length !== 1 || !dump(rows).includes("чужой свежий разговор")) {
  fail("поиск по имени проекта не отобрал чужой разговор: " + dump(rows).slice(0, 300));
}
// Стёртый запрос возвращает весь список машины: отсечки за поиском нет.
find.value = "";
find.handlers.input();
if (rows.children.length !== 3) fail("пустой запрос не вернул весь список: " + rows.children.length);

// --- переход на чужой разговор несёт проект в адресе ---
find.value = "other";
find.handlers.input();
rows.children[0].handlers.click({});
if (!String(sandbox.location.hash).includes("other~ffff9999-9999")) {
  fail("адрес чужого разговора без проекта: " + sandbox.location.hash);
}

// --- фильтр по задаче это запрос поиска, а не отсечка ---
store.set("devkit.chat.filter", "1");
sandbox.location.hash = "#demo/chat/XR-1";
const stTask = await sandbox.chatState("demo", "XR-1", board);
if (stTask.sid !== "aaaa1111-1111") fail("задачный адрес открыл не разговор задачи: " + stTask.sid);
const anchor2 = makeNode("div");
sandbox.chatDropOpen("demo", stTask, anchor2);
const drop2 = anchor2.children[anchor2.children.length - 1];
const find2 = drop2.children[0];
if (find2.value !== "XR-1") fail("задача не встала запросом поиска: " + JSON.stringify(find2.value));
const rows2 = drop2.children[1];
if (rows2.children.length !== 1 || !dump(rows2).includes("свой разговор по XR-1")) {
  fail("запрос задачи не отобрал её разговор: " + dump(rows2).slice(0, 300));
}
find2.value = "";
find2.handlers.input();
if (rows2.children.length !== 3) {
  fail("за фильтром задачи оказалась отсечка, стёртый запрос не вернул список: " + rows2.children.length);
}

console.log("ok: список чатов общий по машине, строки подписаны проектом, поиск знает проект, " +
  "фильтр задачи это запрос, переход на чужой чат едет с проектом в адресе");
