// Стенд накопителя с общим чтением состава согласования (DK-1623).
//
// Накопитель прежде звал ручку подготовленных задач на каждую запись: при
// сотнях черновиков две волны запросов клали канал дашборда и посредника,
// вторую волну поднимало восстановление связи. Состав согласования читается
// одним общим ответом на весь накопитель, и его объём не зависит от числа
// записей.
//
// Предмет стенда: общая ручка зовётся один раз на открытие экрана и один раз
// на обновление, ручка записи из списка не зовётся вовсе, а карточки из
// общего ответа встают в секцию согласования.
//
// Зовётся: node testdata/poc_draftsbulk.mjs static/app.js

import { makeSandbox, settle, dump, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const drafts = [];
for (let i = 1; i <= 12; i += 1) {
  drafts.push({ id: "XR-D" + i, title: "мысль " + i, prio: "mid", type: "task",
    moved: "2026-11-06", age_words: "сегодня", order: "Проведи груминг XR-D" + i });
}
const view = (id, proposals) => ({ id, snapshot: "s-" + id, basis: "b-" + id,
  state: { version: 1, state: "active", proposals } });
const bulk = { views: drafts.map((d) => d.id === "XR-D1"
  ? view("XR-D1", [{ id: "chat-delivery", board_id: "XR-99", revision: 1, outcome: "add",
    title: "Доставка чата", text: "# Доставка чата\n\nОписание" }])
  : view(d.id, null)) };

const { sandbox, byId, asked } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/projects/demo/drafts") return { drafts };
  if (path === "/api/projects/demo/drafts/prepared") return bulk;
  if (path === "/api/projects/demo/drafts/types") return { types: [{ name: "task", title: "Задача" }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});
function check(ok, text) { if (!ok) throw new Error(text); }
const perDraft = () => asked.filter((p) => /\/drafts\/[^/]+\/prepared$/.test(p));
const bulkCount = () => asked.filter((p) => p.endsWith("/drafts/prepared")).length;

sandbox.location.hash = "demo/drafts";
await sandbox.refresh(); await settle();

// --- состав согласования читается одним общим ответом ---
{
  check(bulkCount() === 1, "общая ручка состава не зовётся ровно один раз: " + asked.join(", "));
}

// --- из списка накопителя ручка записи не зовётся вовсе ---
{
  const per = perDraft();
  check(per.length === 0, "накопитель читает подготовленные задачи по каждой записи (" +
    per.length + " шт.): " + per.join(", "));
}

// --- карточки из общего ответа встают в секцию согласования ---
{
  const groups = dump(byId.get("groups"));
  check(groups.includes("Согласовать заведение"), "секция согласования не нарисована");
  check(groups.includes("Доставка чата"), "карточка из общего ответа не попала в секцию");
  check(groups.includes("XR-99"), "номер карточки из общего ответа не показан");
}

// --- обновление экрана поднимает второй общий ответ, а не волну по записям ---
{
  await sandbox.refresh(); await settle();
  check(bulkCount() === 2, "обновление не поднимает один общий ответ: " + bulkCount());
  const per = perDraft();
  check(per.length === 0, "обновление читает подготовленные задачи по записям: " + per.join(", "));
}

console.log("состав согласования читается одним общим ответом при любом числе записей");
