// Стенд закрытия незачатой записи из списка разговоров (POC DK-397, ветка
// poc-chat).
//
// Живой случай: список набивался записями «Новый чат», пустыми внутри, и убрать
// их было нечем вовсе («закрыть их я не могу, просто нет такой возможности»,
// замечание пользователя). Архив тут не ответ: он прячет мусор, а не убирает
// его, и хранить в пустой записи нечего.
//
// Предмет стенда это разбор строк по делу. Пустая запись закрывается насовсем и
// первым же нажатием. Запись с набранным текстом закрывается тоже, но сперва
// спрашивает: текст человек писал руками. Запись, за которой уже стоит сессия,
// и обычный разговор с лентой уходят прежней дорогой, архивом: живую сессию
// закрытие не трогает.
//
// Зовётся: node testdata/poc_chatdrop.mjs static/app.js

import { makeSandbox, makeNode, settle, byClass, allByClass, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "своя задача", sect: "in-progress" },
] }] };

const chats = [
  { id: "blank-empty", project: "demo", title: "Новый чат", blank: true,
    state: "not-started", idle: true, mtime: "2026-08-29T10:00:00+03:00" },
  { id: "blank-typed", project: "demo", title: "Новый чат", blank: true, draft: "недописанное",
    state: "not-started", idle: true, mtime: "2026-08-29T10:00:00+03:00" },
  { id: "blank-live", project: "demo", title: "Новый чат", blank: true, tmux: "devkit-demo-1",
    state: "not-started", idle: true, mtime: "2026-08-29T10:00:00+03:00" },
  { id: "aaaa1111-1111", project: "demo", title: "разговор с лентой",
    state: "dead", tasks: ["XR-1"], mtime: "2026-08-28T10:00:00+03:00" },
];

const asked = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") asked.push(path);
  if (path.includes("/chats")) return { chats, models: [] };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});

sandbox.location.hash = "#demo/chat/board";
const st = await sandbox.chatState("demo", "board", board);
await settle();

const anchor = makeNode("div");
sandbox.chatDropOpen("demo", st, anchor);
const drop = anchor.children[anchor.children.length - 1];
const rows = byClass(drop, "cdrows");
if (!rows) fail("в списке разговоров нет строк вовсе: " + dump(drop).slice(0, 300));

// Строка ищется по своему разговору: список идёт группами по дням, и порядок в
// нём стендовым числам не принадлежит.
function rowOf(id) {
  for (const row of allByClass(rows, "cdrow")) {
    if (dump(row).includes(id) || (row.said && row.said.includes(id))) return row;
  }
  return null;
}

// Строки списка подписи разговора не несут, ID в них нет: строка узнаётся по
// заголовку и чипам, поэтому берём их по порядку выдачи.
const all = allByClass(rows, "cdrow");
if (all.length !== chats.length) {
  fail("в списке не все разговоры: " + all.length + " из " + chats.length);
}
const shutOf = (row) => byClass(row, "cdrop-x");
const archOf = (row) => {
  const btn = byClass(row, "cdarch");
  return btn && !String(btn.className).includes("cdrop-x") ? btn : null;
};
const click = (btn) => { btn.handlers.click({ stopPropagation: () => {} }); };

// --- пустая запись: крестик закрытия, а не архив ---
{
  const row = all[0];
  const shut = shutOf(row);
  if (!shut) fail("у пустой записи нет кнопки закрытия: " + dump(row).slice(0, 300));
  if (archOf(row)) fail("у пустой записи осталась уборка в архив: мусор прячется, а не уходит");
  asked.length = 0;
  click(shut);
  await settle();
  if (!asked.some((p) => p.includes("/blank-empty/drop"))) {
    fail("нажатие не закрыло пустую запись: " + JSON.stringify(asked));
  }
}

// --- запись с набранным текстом: сперва вопрос, потом закрытие ---
{
  const row = all[1];
  const shut = shutOf(row);
  if (!shut) fail("у записи с текстом нет кнопки закрытия");
  asked.length = 0;
  click(shut);
  await settle();
  if (asked.some((p) => p.includes("/drop"))) {
    fail("запись с набранным текстом закрылась первым нажатием, без вопроса");
  }
  if (!String(shut.className).includes("armed")) {
    fail("взведённая кнопка ничем не отличается от спокойной: " + shut.className);
  }
  if (!String(shut.title || "").includes("Точно закрыть")) {
    fail("вопрос о потере текста не сказан вовсе: " + shut.title);
  }
  click(shut);
  await settle();
  if (!asked.some((p) => p.includes("/blank-typed/drop"))) {
    fail("второе нажатие не закрыло запись с текстом: " + JSON.stringify(asked));
  }
}

// --- запись с поднятой сессией: прежняя дорога, архив ---
{
  const row = all[2];
  if (shutOf(row)) fail("запись с живой сессией предлагает закрытие мимо самой сессии");
  if (!archOf(row)) fail("у записи с сессией пропала уборка в архив: " + dump(row).slice(0, 300));
}

// --- обычный разговор с лентой: архив, как и раньше ---
{
  const row = all[3];
  if (shutOf(row)) fail("разговор с лентой предлагает закрытие вместо архива");
  if (!archOf(row)) fail("у разговора с лентой пропала уборка в архив");
  asked.length = 0;
  click(archOf(row));
  await settle();
  if (!asked.some((p) => p.includes("/archive"))) {
    fail("уборка в архив перестала ходить своей ручкой: " + JSON.stringify(asked));
  }
}

console.log("poc_chatdrop: ok");
