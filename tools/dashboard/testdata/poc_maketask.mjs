// Стенд вида нового чата у привязанного разговора (ветка poc-chat). Жалоба
// пользователя: «нажал + в этом чате, а выбора, чат задачи это или
// произвольный, не было; разве он не привязан к задаче?».
//
// Разговор был привязан к задаче чужой доски (чат дашборда шёл по XR-005, а
// экран стоял на доске с префиксом DK), и панель задачу гасила: она у неё
// заодно фильтрует список разговоров и подписывает шапку, а чужая там ни к
// чему. Вид нового чата спрашивал ту же величину и потому не спрашивался
// вовсе.
//
// Предмет стенда: кнопка «+» смотрит на сам разговор, а не на панель. Есть у
// разговора задача, значит меню из двух пунктов и заказ по его задаче; список
// разговоров при этом не сужается. Задача из хвоста адреса, пережившая смену
// доски, кнопке по-прежнему не достаётся: там разговора нет вовсе.
//
// Зовётся: node testdata/poc_maketask.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const board = { prefix: "DK", sections: [{ key: "in-progress", rows: [
  { id: "DK-397", title: "дашборд агентской разработки", sect: "in-progress" },
] }] };

// Список машины: разговор чужой доски, разговор своей и свободный.
const chats = [
  { id: "aaaa1111-2222-3333", project: "demo", title: "Выполнение цели",
    tasks: ["XR-005"], note: "задача не с доски проекта", state: "live" },
  { id: "bbbb1111-2222-3333", project: "demo", title: "Разбор DK-397",
    tasks: ["DK-397"], state: "dead" },
  { id: "cccc1111-2222-3333", project: "demo", title: "Свободный разговор",
    state: "dead" },
];

const orders = [];
const { sandbox } = makeSandbox(appPathArg(), (path, init) => {
  if (init && init.method === "POST") {
    if (path.endsWith("/chats/blank")) {
      const body = init.body ? JSON.parse(init.body) : null;
      orders.push((body && body.id) || "");
      return { id: "blank-" + orders.length };
    }
    return {};
  }
  if (path.includes("/chats")) return { chats, models: [], days: 3 };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/sessions/")) return { session: "x", head: {}, items: [], total: 0 };
  return {};
});

const click = (node) => { node.handlers.click({ stopPropagation: () => {} }); };

// Меню вида встаёт в ту же строку шапки, где стоит сама кнопка.
const menuRows = (head) => allByClass(byClass(head, "chline"), "cdrow").map((r) => r.textContent);

// Строки выпадающего списка разговоров: его раскрывает первая кнопка строки.
const listRows = (head) => {
  const line = byClass(head, "chline");
  click(line.children[0]);
  return dump(byClass(line, "cdrows"));
};

// --- разговор чужой доски: выбор есть, а список не сузился ---
{
  const st = await sandbox.chatState("demo", "aaaa1111-2222-3333", board, []);
  if (st.task) fail("задача чужой доски уехала в панель: " + JSON.stringify(st.task));
  const head = sandbox.chatHead("demo", st);
  click(deepBtn(head, "cdbtn"));
  await settle();
  const said = menuRows(head);
  if (said.length !== 2 || said[0] !== "Чат задачи XR-005" || said[1] !== "Произвольный чат") {
    fail("«+» в привязанном разговоре не спросил вида: " + JSON.stringify(said));
  }
  if (orders.length) fail("«+» завёл чат, не дождавшись выбора: " + JSON.stringify(orders));
  // Пункт задачи заказывает чат её ID, а не пустотой.
  click(allByClass(byClass(head, "chline"), "cdrow")[0]);
  await settle();
  if (orders.length !== 1 || orders[0] !== "XR-005") {
    fail("заказ ушёл не по задаче разговора: " + JSON.stringify(orders));
  }
  // Побочного действия нет: список остался общим, как и был.
  const rows = listRows(sandbox.chatHead("demo", st));
  for (const word of ["Выполнение цели", "Разбор DK-397", "Свободный разговор"]) {
    if (!rows.includes(word)) {
      fail("список разговоров сузился по задаче: нет строки «" + word + "»");
    }
  }
}

// --- разговор своей доски: то же меню, задача панели на месте ---
{
  const st = await sandbox.chatState("demo", "bbbb1111-2222-3333", board, []);
  if (st.task !== "DK-397") fail("задача своей доски потерялась: " + JSON.stringify(st.task));
  const head = sandbox.chatHead("demo", st);
  click(deepBtn(head, "cdbtn"));
  await settle();
  const said = menuRows(head);
  if (said.length !== 2 || said[0] !== "Чат задачи DK-397") {
    fail("«+» в разговоре своей доски сломался: " + JSON.stringify(said));
  }
}

// --- свободный разговор: чат заводится сразу, выбирать не из чего ---
{
  const st = await sandbox.chatState("demo", "cccc1111-2222-3333", board, []);
  const head = sandbox.chatHead("demo", st);
  const was = orders.length;
  click(deepBtn(head, "cdbtn"));
  await settle();
  if (menuRows(head).length) fail("свободный разговор спросил вид, а задачи у него нет");
  if (orders.length !== was + 1 || orders[was] !== "") {
    fail("свободный разговор завёл чат с привязкой: " + JSON.stringify(orders));
  }
}

// --- чужая задача из хвоста адреса: разговора нет, и заказывать нечего ---
{
  const st = await sandbox.chatState("demo", "XR-777", { prefix: "DK", sections: [] }, []);
  if (st.task) fail("чужая задача адреса уехала в панель: " + JSON.stringify(st.task));
  const head = sandbox.chatHead("demo", st);
  const was = orders.length;
  click(deepBtn(head, "cdbtn"));
  await settle();
  if (menuRows(head).length) fail("хвост адреса чужой доски предложил свой чат задачи");
  if (orders.length !== was + 1 || orders[was] !== "") {
    fail("хвост адреса чужой доски заказал чат с привязкой: " + JSON.stringify(orders));
  }
}

console.log("ok: вид нового чата спрашивает сам разговор, список при этом " +
  "остаётся общим, а чужая задача адреса кнопке не достаётся");
