// Стенд строк записей в общем списке разговоров (LLD DK-1422, решение 6).
// Назначение журнала собирает разговоры записи в одну её строку с ID,
// заголовком, видом и стадией, а носитель отрезка отдельной строкой не стоит.
// Кнопка вида переключает по кругу «Все», «Основные», «Обсуждения», чип цели
// открывает основной чат самой цели.
//
// Зовётся: node testdata/poc_chatmain.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, tag, byClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };
const hour = 3600 * 1000;
const stamp = (ms) => new Date(Date.now() - ms).toISOString();

// Две строки записей, свободное обсуждение и носитель отрезка. Строка задачи
// свежее всех, и адрес доски разрешается в неё: она же открытый чат панели,
// который список прижимает верхом при любом виде. Носитель приезжает в ответе
// для панели на адресе записи, а строкой списка он быть перестал, и список не
// рисует его ни видом, ни поиском.
const chats = [
  { id: "XR-4", project: "demo", title: "XR-4: Начатая задача", main: true,
    kind: "задача", stage: "разработка", goalOf: "XR-100", state: "live",
    idle: true, mtime: stamp(30 * 60 * 1000), tasks: ["XR-4"] },
  { id: "XR-100", project: "demo", title: "XR-100: Цель: пробный цикл",
    main: true, kind: "цель", state: "dead", mtime: stamp(3 * hour),
    tasks: ["XR-100"] },
  { id: "eeee1111-2222", project: "demo", title: "обсуждение по задаче",
    state: "dead", mtime: stamp(2 * hour), tasks: [] },
  { id: "tmux-task-XR-4", project: "demo", title: "разговор носителя",
    state: "live", idle: true, mtime: stamp(hour), tasks: [], carrier: "XR-4" },
];

const { sandbox, store } = makeSandbox(app, (path, init) => {
  if (path.includes("/tasks/XR-4/chat")) {
    return { id: "XR-4", project: "demo", chat: "tmux-task-XR-4" };
  }
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.includes("/sessions/")) return { items: [], start: true };
  if (path.includes("/board")) return { board, works: [] };
  return {};
});
store.set("devkit.chat.filter", "0");

sandbox.location.hash = "#demo/chat/board";
const st = await sandbox.chatState("demo", "board", board);

// Список открывается тем же кодом, которым его открывает человек, и каждый
// заход читает положение кнопок из памяти вкладки заново.
const anchor = makeNode("div");
const open = () => {
  sandbox.chatDropShut();
  sandbox.chatDropOpen("demo", st, anchor);
  const drop = anchor.children[anchor.children.length - 1];
  return { drop, rows: byClass(drop, "cdrows") };
};
const titles = (rows) => rows.querySelectorAll(".cdrow").map((n) => dump(n));
const has = (rows, what) => titles(rows).some((said) => said.includes(what));
const rowOf = (rows, what) =>
  rows.querySelectorAll(".cdrow").find((n) => dump(n).includes(what));
const kindBtn = (drop) => {
  const btn = byClass(drop, "cdkindbtn");
  if (!btn) fail("кнопки вида списка нет: " + dump(drop).slice(0, 200));
  return btn;
};

// --- строка записи: ID с заголовком, вид, стадия, цель, без кнопок ---
{
  const { rows } = open();
  const row = rowOf(rows, "XR-4: Начатая задача");
  if (!row) fail("строки записи в списке нет: " + dump(rows).slice(0, 300));
  const said = dump(row);
  for (const want of ["XR-100", "задача", "разработка"]) {
    if (!said.includes(want)) {
      fail("чип " + want + " не доехал до строки записи: " + said.slice(0, 300));
    }
  }
  if (byClass(row, "cdarch")) {
    fail("у строки записи стоит кнопка, которой у неё нет: " + said.slice(0, 300));
  }
}

// --- чип цели открывает основной чат самой цели ---
{
  const { rows } = open();
  const row = rowOf(rows, "XR-4: Начатая задача");
  const goal = byClass(row, "clicky");
  if (!goal || !dump(goal).includes("XR-100")) {
    fail("чипа цели в строке записи нет: " + dump(row).slice(0, 300));
  }
  goal.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes("XR-100")) {
    fail("чип цели не открыл чат цели: " + sandbox.location.hash);
  }
  sandbox.location.hash = "#demo/chat/board";
}

// --- носитель отрезка не рисуется ни видом, ни поиском ---
{
  const { drop, rows } = open();
  if (has(rows, "разговор носителя")) {
    fail("носитель отрезка стоит отдельной строкой: " + dump(rows).slice(0, 300));
  }
  const find = tag(drop, "INPUT");
  find.value = "носител";
  find.handlers.input();
  await settle();
  if (has(rows, "разговор носителя")) {
    fail("поиск нашёл носителя отрезка: " + dump(rows).slice(0, 300));
  }
  find.value = "";
  find.handlers.input();
  await settle();
}

// --- три положения кнопки вида дают три разных набора ---
{
  const seen = {};
  for (const want of ["Основные", "Обсуждения", "Все"]) {
    const { drop, rows } = open();
    const btn = kindBtn(drop);
    btn.handlers.click({ stopPropagation: () => {} });
    if (btn.textContent !== want) {
      fail("кнопка вида встала не в ожидаемое положение: " + btn.textContent +
        ", ждал " + want);
    }
    if (String(btn.className).includes("on") !== (want !== "Все")) {
      fail("подсветка кнопки вида не следует положению: " + btn.className);
    }
    // Наборы сверяются составом, а не длиной: у видов разное число строк,
    // и случайное совпадение длин всё равно даёт разные наборы.
    seen[want] = titles(rows).sort().join("|");
    if (want === "Основные") {
      if (titles(rows).length !== 2) {
        fail("«Основные» показали не две строки записей: " + titles(rows).length);
      }
      if (!has(rows, "XR-4: Начатая задача") || !has(rows, "XR-100: Цель: пробный цикл")) {
        fail("среди «Основных» не обе строки записей: " + dump(rows).slice(0, 300));
      }
    }
    if (want === "Обсуждения") {
      // Открытый чат прижимается верхом при любом виде: панель держит его, и
      // человек видит, где стоит, даже перебрав вид списка. Остальные строки
      // записей из «Обсуждений» уходят.
      if (titles(rows).length !== 2) {
        fail("«Обсуждения» показали не обсуждение с прижатым открытым чатом: " +
          titles(rows).length);
      }
      if (has(rows, "XR-100: Цель: пробный цикл")) {
        fail("в «Обсуждения» попала закрытая строка записи: " + dump(rows).slice(0, 300));
      }
      if (!has(rows, "обсуждение по задаче")) {
        fail("свободное обсуждение не показано своим видом: " + dump(rows).slice(0, 300));
      }
    }
    if (want === "Все") {
      if (titles(rows).length !== 3) {
        fail("«Все» показали не три строки без носителя: " + titles(rows).length);
      }
      if (has(rows, "разговор носителя")) {
        fail("в «Все» прорезался носитель отрезка: " + dump(rows).slice(0, 300));
      }
    }
  }
  if (seen["Основные"] === seen["Обсуждения"] ||
      seen["Обсуждения"] === seen["Все"] ||
      seen["Основные"] === seen["Все"]) {
    fail("три положения вида дали не три разных набора: " + JSON.stringify(seen));
  }
}

// --- положение вида переживает перезагрузку страницы ---
{
  const { drop } = open();
  kindBtn(drop).handlers.click({ stopPropagation: () => {} });
  if (store.get("devkit.chat.kind") !== "main") {
    fail("положение вида не легло в память вкладки: " +
      JSON.stringify(store.get("devkit.chat.kind")));
  }
  // Перезагрузка это новое открытие списка с той же памятью: вид берётся
  // оттуда, а не с умолчания.
  const again = open();
  if (kindBtn(again.drop).textContent !== "Основные") {
    fail("после перезагрузки кнопка вида вернулась к умолчанию");
  }
  if (!has(again.rows, "XR-4: Начатая задача") || has(again.rows, "обсуждение по задаче")) {
    fail("после перезагрузки список не в сохранённом виде: " +
      dump(again.rows).slice(0, 300));
  }
  store.set("devkit.chat.kind", "all");
}

// --- панель на адресе записи: открытый чат это строка записи ---
{
  // Ручка адреса называет носителя, и панель стоит на его разговоре, а список
  // держит запись строкой записи: открытой должна стоять она, а не носитель,
  // которого список не рисует вовсе.
  const onRec = await sandbox.chatState("demo", "XR-4", board, []);
  if (onRec.sid !== "tmux-task-XR-4") {
    fail("панель на адресе записи не встала на носителя: " + onRec.sid);
  }
  sandbox.chatDropShut();
  sandbox.chatDropOpen("demo", onRec, anchor);
  const drop = anchor.children[anchor.children.length - 1];
  const rows = byClass(drop, "cdrows");
  const said = dump(rows);
  if (!said.includes("открытый чат")) {
    fail("на адресе записи нет группы открытого чата: " + said.slice(0, 300));
  }
  const top = rows.querySelectorAll(".cdrow")[0];
  if (!dump(top).includes("XR-4: Начатая задача")) {
    fail("открытым чатом стоит не строка записи: " + dump(top).slice(0, 200));
  }
  if (!String(top.className).includes("on")) {
    fail("строка записи не подсвечена текущей: " + top.className);
  }
  if (has(rows, "разговор носителя")) {
    fail("носитель прорезался строкой на адресе записи: " + said.slice(0, 300));
  }
}

console.log("ок: строка записи стоит с видом, стадией и целью, кнопка строки не " +
  "даёт ни архива, ни крестика, чип цели открывает чат цели, носитель отрезка " +
  "не рисуется ни видом, ни поиском, три положения кнопки вида дают три " +
  "разных набора и переживают перезагрузку, а на адресе записи открытым " +
  "чатом стоит сама строка записи");
