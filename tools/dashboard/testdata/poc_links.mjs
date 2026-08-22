// Стенд блока «Связи» экрана задачи (замечания пользователя по разделу): у
// каждой строки виден тип артефакта (LLD, цель, задача), у задачи род связи
// (после, держит) и дата закрытия, где сервер их назвал; длинный список
// свёрнут до первых строк и разворачивается кнопкой «ещё N».
//
// Зовётся: node testdata/poc_links.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const row = { id: "XR-1", title: "задача со связями", type: "task", cost: "S", r: 5, sect: "backlog" };
const tail = [];
for (let n = 10; n < 22; n++) {
  tail.push({ id: "XR-" + n, kind: "задача", title: "задача номер " + n });
}
const links = {
  lld: [{ file: "lld/XR-1-design.md", title: "дизайн задачи", own: true }],
  tasks: [
    { id: "XR-100", kind: "цель", title: "Цель: большой раздел" },
    { id: "XR-2", kind: "задача", title: "ранняя задача", rel: "после" },
    { id: "XR-136", kind: "задача", title: "панель чата в шапке", closed: "2026-07-01" },
    ...tail,
  ],
};

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/tasks/") && (!init || !init.method)) {
    return { project: "demo", id: "XR-1", row, after: [], blocks: [],
      file: "docs/tasks/XR-1.md", text: "# XR-1\n\nтело\n", links };
  }
  if (path.endsWith("/board")) return { board: { sections: [{ key: "backlog", rows: [row] }] }, works: [] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
  if (path === "/api/quota") return { buckets: [] };
  return {};
});

await settle();
await sandbox.renderTask("demo", [], "XR-1");
await settle();

const groups = byId.get("groups");
const card = allByClass(groups, "dcard").find((c) => dump(c).includes("Связи"));
if (!card) fail("карточки «Связи» на экране нет: " + dump(groups).slice(0, 200));

// --- тип артефакта у каждой строки ---
const said = dump(card).replace(/\s+/g, " ");
if (!said.includes("LLD задачи") || !said.includes("дизайн задачи")) {
  fail("строка LLD потеряла тип или название: " + said);
}
const box = byClass(card, "lrel");
if (!box) fail("блока связанных задач нет: " + said);
const rowsIn = () => (box.children || []).filter((k) => String(k.className).includes("srow"));
const first = dump(rowsIn()[0]).replace(/\s+/g, " ");
if (!first.includes("цель") || !first.includes("XR-100")) {
  fail("у первой строки не виден тип «цель»: " + first);
}
if (!dump(rowsIn()[1]).includes("задача")) fail("у строки задачи нет типа: " + dump(rowsIn()[1]));

// --- род связи и дата закрытия ---
const relRow = rowsIn().find((k) => dump(k).includes("XR-2"));
if (!relRow || !dump(relRow).includes("после")) {
  fail("род связи «после» не показан: " + dump(relRow || box));
}
const closedRow = rowsIn().find((k) => dump(k).includes("XR-136"));
if (!closedRow || !dump(closedRow).includes("закрыта 2026-07-01")) {
  fail("дата закрытия не показана: " + dump(closedRow || box));
}

// --- длинный список свёрнут, «ещё N» разворачивает ---
if (rowsIn().length !== 8) {
  fail("свёрнутый список не из 8 строк: " + rowsIn().length);
}
const more = byClass(box, "lmore");
if (!more || !dump(more).includes("ещё 7")) {
  fail("кнопки «ещё 7» нет: " + dump(box).slice(-200));
}
more.handlers.click();
if (rowsIn().length !== 15) fail("клик не развернул хвост: " + rowsIn().length);
if (byClass(box, "lmore")) fail("кнопка «ещё» осталась после разворота");

console.log("ok: связи несут тип, род и дату, длинный список свёрнут до 8 и разворачивается");
