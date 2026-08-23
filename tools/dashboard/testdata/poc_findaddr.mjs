// Стенд согласованности поиска шапки с адресом (баг: после перехода на форму
// и возврата строка поиска оставалась в поле при полном списке). Запрос живёт
// в адресе экрана (образец раздела «Агенты»): поле всегда отражает адрес,
// переход на форму его пустит, возврат на выдачу вернёт. Крестик сброса в
// поле идёт той же дорогой, что Escape: стирает запрос и возвращает доску.
//
// Зовётся: node testdata/poc_findaddr.mjs static/app.js

import { makeSandbox, settle, dump, fail, byClass, appPathArg } from "./poc_dom.mjs";

const { sandbox, byId } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [], sections: {} }] };
  if (path.endsWith("/board")) return { board: { sections: [] } };
  if (path.includes("/search?q=")) {
    return { groups: [{ key: "board", title: "Доска",
      rows: [{ id: "XR-1", title: "найденная строка" }] }] };
  }
  if (path.includes("/tasks/XR-1")) {
    return { project: "demo", id: "XR-1", after: [], blocks: [],
      row: { id: "XR-1", title: "найденная строка", type: "task", cost: "S" },
      file: "docs/tasks/XR-1.md", text: "# XR-1\n\nпостановка" };
  }
  if (path.includes("/chats")) return { chats: [] };
  return {};
});
await settle();

const hq = byId.get("hq");
// На коде без крестика узла нет вовсе: заглушка даёт стенду упасть словами
// про спрятанный крестик, а не исключением.
const clearBtn = byId.get("hq-clear") || { hidden: true, handlers: {} };
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

// Выдача по адресу: поле шапки отражает запрос, крестик виден.
await go("#demo/find/" + encodeURIComponent("найденная"));
if (hq.value !== "найденная") {
  fail("поле шапки не отражает запрос адреса: " + JSON.stringify(hq.value));
}
if (clearBtn.hidden) fail("крестик сброса спрятан при живом запросе");

// Переход на форму задачи: адрес без запроса, поле пустеет, и полный экран
// не противоречит полю.
await go("#demo/XR-1");
if (hq.value !== "") {
  fail("после перехода на форму поле держит прежний запрос: " + JSON.stringify(hq.value));
}
if (!clearBtn.hidden) fail("крестик горит на экране без запроса");

// Возврат на выдачу: запрос живёт в адресе и возвращается вместе с ним.
await go("#demo/find/" + encodeURIComponent("найденная"));
if (hq.value !== "найденная") fail("возврат на выдачу не вернул запрос в поле");

// Крестик шапки: запрос стирается, экран уходит на доску, тем же путём, что
// Escape.
if (!clearBtn.handlers || !clearBtn.handlers.click) fail("крестик шапки не подключён");
clearBtn.handlers.click({});
await settle();
if (hq.value !== "") fail("крестик не стёр запрос из поля");
if (sandbox.location.hash.replace(/^#/, "") !== "demo") {
  fail("крестик не вернул на доску: " + sandbox.location.hash);
}
await sandbox.refresh();
await settle();
if (!clearBtn.hidden) fail("крестик горит после сброса");

// Крестик стоит и в поле экрана выдачи и ведёт туда же.
await go("#demo/find/" + encodeURIComponent("найденная"));
const bar = byClass(byId.get("groups"), "fqbar");
if (!bar) fail("поля выдачи нет: " + dump(byId.get("groups")).slice(0, 200));
const barClear = (bar.children || []).find((k) => String(k.className || "").includes("fclear"));
if (!barClear) fail("в поле выдачи нет крестика сброса");
barClear.handlers.click({});
await settle();
if (sandbox.location.hash.replace(/^#/, "") !== "demo") {
  fail("крестик выдачи не вернул на доску: " + sandbox.location.hash);
}

console.log("ok: поле шапки отражает адрес на всех экранах, запрос переживает " +
  "форму и возврат, крестики шапки и выдачи стирают запрос дорогой Escape");
