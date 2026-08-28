// Стенд мёртвых и черновых связей на экране задачи (ветка poc-chat).
//
// Связи выводятся из упоминаний ID в тексте постановки, своего хранилища у них
// нет, и снятый ID из чужой постановки никуда не девается. Прежде такая строка
// звалась задачей и вела на форму, которой нет, а запись накопителя приезжала
// туда же и без названия (замечания пользователя: удалённые XR-483 остались
// живыми ссылками, черновики XR-50 показаны задачами без имени). Предмет
// стенда это показ обоих случаев: черновик назван черновиком, своим именем и
// ведёт в накопитель, мёртвый ID назван мёртвым и дороги не имеет.
//
// Зовётся: node testdata/poc_linkgone.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const css = await readFile(join(dirname(fileURLToPath(import.meta.url)), "..", "static", "style.css"), "utf8");

const app = appPathArg();
const row = { id: "XR-1", title: "задача со связями", type: "task", cost: "S", r: 5, sect: "backlog" };
// Ответ сервера дословно: поля draft и gone кладёт taskLinks, порядок тоже
// серверный, мёртвая строка лежит ниже живых.
const links = {
  lld: [],
  tasks: [
    { id: "XR-2", kind: "задача", title: "живая соседка", rel: "после" },
    { id: "XR-50", kind: "черновик", draft: true, title: "чат копит замечания, пока агент занят" },
    { id: "XR-483", kind: "нет записи", gone: true, note: "ни строки на доске, ни архива, ни черновика" },
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
const box = byClass(card, "lrel");
if (!box) fail("блока связанных задач нет: " + dump(card).slice(0, 200));
const rowsIn = (box.children || []).filter((k) => String(k.className).includes("srow"));
const rowOf = (id) => {
  const hit = rowsIn.find((k) => dump(k).includes(id));
  if (!hit) fail("строки " + id + " в связях нет: " + dump(box).slice(0, 300));
  return hit;
};

// --- черновик назван черновиком и своим именем ---
{
  const draft = rowOf("XR-50");
  const said = dump(draft).replace(/\s+/g, " ");
  if (!said.includes("черновик")) fail("запись накопителя не названа черновиком: " + said);
  if (!said.includes("чат копит замечания")) {
    fail("у черновика в связях нет названия из его файла: " + said);
  }
  if (said.includes("названия нет")) fail("черновик доехал с отговоркой про название: " + said);
}

// --- дорога черновика ведёт в накопитель, а не на форму задачи ---
{
  const draft = rowOf("XR-50");
  if (!String(draft.className).includes("clicky")) fail("строка черновика не кликается");
  if (!draft.handlers.click) fail("у строки черновика нет обработчика клика");
  draft.handlers.click({});
  const to = sandbox.location.hash.replace(/^#/, "");
  if (to !== "demo/draft/XR-50") {
    fail("черновик из связей увёл не на запись накопителя: " + (sandbox.location.hash || "адрес не менялся"));
  }
}

// --- мёртвый ID назван мёртвым, задачей не притворяется и дороги не имеет ---
{
  const dead = rowOf("XR-483");
  const said = dump(dead).replace(/\s+/g, " ");
  if (said.includes("задача")) fail("исчезнувший ID показан задачей: " + said);
  if (!said.includes("нет записи")) fail("у мёртвой связи не виден её род: " + said);
  if (!said.includes("ни строки на доске")) fail("мёртвая связь не сказала, где её искали: " + said);
  if (String(dead.className).includes("clicky")) fail("мёртвая связь выглядит дорогой: " + dead.className);
  if (dead.handlers.click) fail("мёртвая связь ведёт на отказ формы задачи, а не молчит");
  if (!String(dead.className).includes("lgone")) fail("мёртвой связи не досталось своего вида: " + dead.className);
}

// --- вид мёртвой строки: номер перечёркнут, курсора-руки нет ---
{
  const rule = /\.dcard \.srow\.lgone \.id\{([^}]*)\}/.exec(css);
  if (!rule || !rule[1].includes("line-through")) {
    fail("номер мёртвой связи не перечёркнут: " + (rule ? rule[1] : "правила нет"));
  }
}

// --- мёртвая строка лежит ниже живых, порядок сервера не переставлен ---
if (rowsIn.indexOf(rowOf("XR-483")) !== rowsIn.length - 1) {
  fail("мёртвая связь всплыла выше живых: " + dump(box).slice(0, 300));
}

console.log("ok: черновик в связях назван и ведёт в накопитель, исчезнувший ID мёртв и дороги не имеет");
