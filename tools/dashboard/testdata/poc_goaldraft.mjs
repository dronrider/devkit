// Стенд состава цели: черновик меткой и курсор на строке (POC DK-397, ветка
// poc-chat).
//
// Задача, которая пока лежит записью накопителя, подписывалась в составе цели
// фразой «ни на доске, ни в архиве: строкой задача ещё не заведена», и человек
// читал её как поломку состава. Открыть такую запись со строки было нечем.
// Заодно строка состава ловила клик, а курсор над ней оставался текстовым.
//
// Предмет стенда: у черновика метка «черновик» вместо фразы, нажатие открывает
// экран записи, у живой задачи нажатие открывает её форму, обе строки несут
// класс clicky, а у закрытой и у безымянной задачи ни класса, ни перехода нет.
//
// Зовётся: node testdata/poc_goaldraft.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const goal = { id: "XR-100", title: "Цель: пробный цикл", sect: "in-progress", type: "goal",
  cost: "XL", r: 41, r_parts: [25, 9, 3, 0, 4], moved: "2026-08-26" };

// Четыре судьбы состава: запись накопителя, живая строка доски, закрытая
// задача и ID, за которым не лежит ничего.
const composed = [
  { id: "XR-011", fate: "первая по нарезке", title: "экран записи открывается из состава",
    draft: true, done: false },
  { id: "XR-012", fate: "вторая", title: "живая строка", sect: "in-progress",
    section: "в работе", r: 30, done: false },
  { id: "XR-013", fate: "третья", title: "закрытая", done: true, closed: "2026-08-20",
    sect: "archive", section: "закрыта" },
  { id: "XR-014", fate: "четвёртая", done: false,
    note: "ни на доске, ни в архиве: строкой задача ещё не заведена" },
];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.includes("/goals/") && path.endsWith("/tasks")) {
    return { goal: "XR-100", file: "docs/tasks/XR-100.md", tasks: composed,
      counts: { total: 4, closed: 1, running: 1, ahead: 2 } };
  }
  if (path.includes("/tasks/") && (!init || !init.method)) {
    return { project: "demo", id: "XR-100", row: goal, after: [], blocks: [],
      file: "docs/tasks/XR-100.md", text: "# XR-100\n\nПостановка.\n" };
  }
  if (path.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [
      { key: "in-progress", title: "In progress", rows: [goal] }] }, works: [] };
  }
  if (path.endsWith("/works")) return { works: [] };
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path === "/api/harnesses") return { harnesses: [{ name: "claude-code", tiers: ["pro"] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [], buckets: [] };
  if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
  return {};
});

const groups = byId.get("groups");
await settle();
await sandbox.renderTask("demo", [], "XR-100");
await settle();

const card = allByClass(groups, "card").find((c) => String(c.className).includes("comp"));
if (!card) fail("состава цели на форме нет: " + dump(groups).slice(0, 400));

// Строки состава по ID: закрытые лежат в свёрнутой коробке, поэтому берутся
// все строки карточки целиком.
const rows = new Map();
for (const row of allByClass(card, "srow")) {
  const id = byClass(row, "id");
  if (id) rows.set(String(id.textContent), row);
}
for (const id of ["XR-011", "XR-012", "XR-013", "XR-014"]) {
  if (!rows.has(id)) fail("в составе нет строки " + id + ": " + dump(card).slice(0, 400));
}

const said = (row) => dump(row);
// Адрес заглушки живёт без решётки, у браузера она есть: сверяется путь.
const where = () => String(sandbox.location.hash).replace(/^#/, "");
const clicky = (row) => String(row.className).split(" ").includes("clicky");

// --- черновик: метка вместо фразы и дорога на экран записи ---
{
  const row = rows.get("XR-011");
  if (!said(row).includes("черновик")) {
    fail("у записи накопителя нет метки «черновик»: " + said(row));
  }
  if (said(row).includes("ни на доске")) {
    fail("у записи накопителя осталась фраза про отсутствие строки: " + said(row));
  }
  if (!clicky(row)) fail("строка черновика без класса clicky, курсор над ней текстовый");
  if (!row.handlers.click) fail("строка черновика не открывается нажатием");
  row.handlers.click({});
  await settle();
  if (!where().startsWith("demo/draft/XR-011")) {
    fail("нажатие на черновик увело на " + where() + ", жду экран записи");
  }
}

// --- живая задача: прежний переход и тот же курсор ---
{
  await sandbox.renderTask("demo", [], "XR-100");
  await settle();
  const card2 = allByClass(groups, "card").find((c) => String(c.className).includes("comp"));
  const row = allByClass(card2, "srow").find((r) => {
    const id = byClass(r, "id");
    return id && String(id.textContent) === "XR-012";
  });
  if (!clicky(row)) fail("строка живой задачи без класса clicky, курсор над ней текстовый");
  row.handlers.click({});
  await settle();
  if (!where().startsWith("demo/XR-012")) {
    fail("нажатие на живую задачу увело на " + where() + ", жду её форму");
  }
}

// --- закрытая и безымянная: открывать нечего, курсор прежний ---
{
  await sandbox.renderTask("demo", [], "XR-100");
  await settle();
  const card3 = allByClass(groups, "card").find((c) => String(c.className).includes("comp"));
  const back = new Map();
  for (const row of allByClass(card3, "srow")) {
    const id = byClass(row, "id");
    if (id) back.set(String(id.textContent), row);
  }
  for (const id of ["XR-013", "XR-014"]) {
    const row = back.get(id);
    if (clicky(row)) fail(id + ": строка помечена clicky, а открывать нечего");
    if (row.handlers.click) fail(id + ": строка ловит нажатие, а экрана у неё нет");
  }
  if (!said(back.get("XR-014")).includes("ни на доске")) {
    fail("у задачи без записи пропала прежняя приписка: " + said(back.get("XR-014")));
  }
}

console.log("состав цели: черновик меткой, ссылка на запись, курсор указателем");
