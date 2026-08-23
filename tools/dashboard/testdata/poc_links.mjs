// Стенд блока «Связи» экрана задачи (замечания пользователя по разделу): у
// каждой строки виден тип артефакта (LLD, цель, задача), у задачи род связи
// (после, держит) и дата закрытия, где сервер их назвал; длинный список
// свёрнут до первых строк и разворачивается кнопкой «ещё N».
//
// Зовётся: node testdata/poc_links.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Стилевой файл читается тем же стендом: перенос номера по строкам и
// ужимание названия видны только в CSS, мок разметки о них не знает.
const css = await readFile(join(dirname(fileURLToPath(import.meta.url)), "..", "static", "style.css"), "utf8");
const ruleOf = (sel) => {
  const m = new RegExp("(^|[\\n}])\\" + sel.slice(0, 1) + sel.slice(1) + "\\{([^}]*)\\}").exec(css);
  return m ? m[2] : "";
};

const app = appPathArg();
const row = { id: "XR-1", title: "задача со связями", type: "task", cost: "S", r: 5, sect: "backlog" };
const tail = [];
for (let n = 10; n < 22; n++) {
  tail.push({ id: "XR-" + n, kind: "задача", title: "задача номер " + n });
}
// Порядок повторяет серверный (пересмотр пользователя): блокирующая связь
// первой, дальше открытые по рангу, закрытая в самом низу, за свёрткой.
const links = {
  lld: [{ file: "lld/XR-1-design.md", title: "дизайн задачи", own: true }],
  tasks: [
    { id: "XR-2", kind: "задача", title: "ранняя задача", rel: "после" },
    { id: "XR-100", kind: "цель", title: "Цель: большой раздел" },
    ...tail,
    { id: "XR-136", kind: "задача", title: "панель чата в шапке", closed: "2026-07-01" },
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

// --- тип артефакта у каждой строки; подпись LLD короткая, свой дизайн
// отличает подсказка (замечание пользователя: «LLD задачи» не влезал) ---
const said = dump(card).replace(/\s+/g, " ");
if (!said.includes("LLD") || !said.includes("дизайн задачи")) {
  fail("строка LLD потеряла тип или название: " + said);
}
if (said.includes("LLD задачи")) {
  fail("подпись чипа LLD не сократилась: " + said.slice(0, 120));
}
const box = byClass(card, "lrel");
if (!box) fail("блока связанных задач нет: " + said);
const rowsIn = () => (box.children || []).filter((k) => String(k.className).includes("srow"));
const first = dump(rowsIn()[0]).replace(/\s+/g, " ");
if (!first.includes("XR-2") || !first.includes("после")) {
  fail("первой стоит не блокирующая связь с родом «после»: " + first);
}
const second = dump(rowsIn()[1]).replace(/\s+/g, " ");
if (!second.includes("цель") || !second.includes("XR-100")) {
  fail("у строки цели не виден тип: " + second);
}
if (!dump(rowsIn()[2]).includes("задача")) fail("у строки задачи нет типа: " + dump(rowsIn()[2]));

// --- длинный список свёрнут, «ещё N» разворачивает ---
if (rowsIn().length !== 8) {
  fail("свёрнутый список не из 8 строк: " + rowsIn().length);
}
if (rowsIn().some((k) => dump(k).includes("XR-136"))) {
  fail("закрытая всплыла выше свёртки, а должна лежать в самом низу");
}
const more = byClass(box, "lmore");
if (!more || !dump(more).includes("ещё 7")) {
  fail("кнопки «ещё 7» нет: " + dump(box).slice(-200));
}
more.handlers.click();
if (rowsIn().length !== 15) fail("клик не развернул хвост: " + rowsIn().length);
if (byClass(box, "lmore")) fail("кнопка «ещё» осталась после разворота");

// --- дата закрытия у нижней строки ---
const last = dump(rowsIn()[14]).replace(/\s+/g, " ");
if (!last.includes("XR-136") || !last.includes("закрыта 2026-07-01")) {
  fail("закрытая не в самом низу или без даты: " + last);
}

// --- номер не переносится, ужимается название ---
// Номер рвался по дефису (DK- на одной строке, число на другой): элемент ID
// обязан стоять целиком, а длинное название рядом честно ужиматься
// многоточием, не выталкивая номер.
const idRule = ruleOf(".dcard .lmid .id");
if (!idRule.includes("white-space:nowrap") || !idRule.includes("flex:none")) {
  fail("номер задачи в связях может переноситься или ужиматься: " + (idRule || "правила нет"));
}
const stRule = ruleOf(".dcard .lmid .st");
if (!stRule.includes("min-width:0") || !stRule.includes("text-overflow:ellipsis")) {
  fail("название рядом с номером не ужимается: " + (stRule || "правила нет"));
}

console.log("ok: связи несут тип, род и дату, длинный список свёрнут до 8 и разворачивается");
