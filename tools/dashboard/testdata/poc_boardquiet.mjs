// Стенд спокойного списка задач (ветка poc-chat).
//
// Список прыгал при листании: у доски с живой работой заведён круг обновления
// в три секунды, каждый заход тянул доску и перебирал строки, а возврат снимка
// прокрутки уводил список назад из-под пальца («экран дёргается в момент
// обновления, читать невозможно», замечание пользователя). Предмет стенда:
// данные те же, значит списка не касаемся вовсе; изменилась одна строка,
// значит перерисована ровно она; листание во время ответа сильнее снимка.
//
// Зовётся: node testdata/poc_boardquiet.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Доска настоящего размера: перебор сотни строк и есть та работа, которой
// список платил за каждый круг.
const rows = [];
for (let i = 1; i <= 60; i += 1) {
  rows.push({ id: "XR-" + i, title: "строка доски номер " + i, sect: "backlog",
    r: 40 - (i % 30), r_parts: [25, 5, 3, 0, 2], cost: "M", type: "task" });
}
const board = () => ({ prefix: "XR", sections: [
  { key: "in-progress", title: "In progress", rows: [
    { id: "XR-1000", title: "идущая работа", sect: "in-progress", run: "tmux" }] },
  { key: "backlog", title: "Backlog", rows: rows.map((r) => ({ ...r })) },
] });

// Живая работа: её время последнего хода тикает каждый заход, и список от
// этого зависеть не должен.
let moved = 1786000000;
const works = () => [{ id: "XR-1000", kind: "task", via: "tmux", live: "busy",
  own: true, moved: moved }];

const { sandbox, byId, timers } = makeSandbox(app, (path) => {
  if (path === "/api/projects") {
    return { projects: [{ name: "demo", prefix: "XR", works: works(),
      sections: { backlog: rows.length, "in-progress": 1 } }] };
  }
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board: board(), works: works() };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();

const rowNode = (id) => {
  for (const body of allByClass(groups, "tsec")) {
    for (const kid of body.children || []) {
      if (dump(kid).includes(id + " ")) return kid;
      if (String((kid.dataset || {}).pkey || "") === id) return kid;
    }
  }
  return null;
};

if (!rowNode("XR-7")) fail("доска не собралась: строки XR-7 нет");

// --- те же данные: узлы строк те же, прокрутка на месте ---
{
  const was = rowNode("XR-7");
  const neighbour = rowNode("XR-8");
  groups.scrollTop = 240;
  moved += 7; // живая работа сделала ход, строки задач от этого не зависят
  await sandbox.refresh();
  await settle();
  if (rowNode("XR-7") !== was) fail("строка пересобрана при тех же данных");
  if (rowNode("XR-8") !== neighbour) fail("соседняя строка пересобрана при тех же данных");
  if (groups.scrollTop !== 240) {
    fail("обновление сдвинуло прокрутку: " + groups.scrollTop + " вместо 240");
  }
}

// --- изменилась одна строка: перерисована ровно она ---
{
  const was = rowNode("XR-7");
  const neighbour = rowNode("XR-8");
  rows[6].title = "строка доски номер 7, поправленная";
  await sandbox.refresh();
  await settle();
  const now = rowNode("XR-7");
  if (now === was) fail("правка строки не доехала до экрана");
  if (!dump(now).includes("поправленная")) {
    fail("строка перерисована не своими словами: " + dump(now).slice(0, 120));
  }
  if (rowNode("XR-8") !== neighbour) {
    fail("правка одной строки пересобрала соседнюю: список перерисован целиком");
  }
  if (groups.scrollTop !== 240) {
    fail("правка одной строки сдвинула прокрутку: " + groups.scrollTop);
  }
}

// --- листание во время ответа сильнее снимка ---
// Ответ летит по сети, и палец за это время уводит список. Прежде вернувшийся
// снимок ставил прокрутку обратно, и это и был рывок.
{
  rows[7].title = "строка доски номер 8, тоже поправленная";
  const going = sandbox.refresh();
  // Человек листает, пока ответ в пути: событие прокрутки приходит от узла.
  groups.scrollTop = 900;
  if (groups.handlers && groups.handlers.scroll) groups.handlers.scroll({});
  await going;
  await settle();
  if (groups.scrollTop !== 900) {
    fail("обновление вернуло прокрутку из-под пальца: " + groups.scrollTop + " вместо 900");
  }
}

// --- круг живой работы заведён, но списка он не трогает ---
{
  const beat = timers.filter((t) => t.ms === 3000 && t.fn);
  if (!beat.length) fail("круга обновления у живой работы нет вовсе");
  const was = rowNode("XR-7");
  const last = beat[beat.length - 1];
  last.fn();
  last.fn = null;
  await settle();
  if (rowNode("XR-7") !== was) fail("круг живой работы пересобрал строку при тех же данных");
}

console.log("poc_boardquiet: ok");
