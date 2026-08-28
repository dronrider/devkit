// Стенд стороны раскрытия списка подписок (POC DK-397, ветка poc-chat).
//
// Живой случай: на форме задачи кнопка «Выполнить» переносится на второй ряд,
// когда ширины не хватает, и встаёт у левого края экрана. Список подписок висит
// правым краем на кнопке и растёт влево, а у главной части экрана свой
// overflow: вылезший за её край список не ложится поверх боковой колонки, а
// обрезается ею, и с экрана его видно наполовину (замечание пользователя).
//
// Предмет стенда: список выбирает сторону по месту, а не всегда растёт влево.
// Раскладки в моке нет вовсе, поэтому место называется размерами узлов: стенд
// подкладывает их сам, как это делает браузер.
//
// Зовётся: node testdata/poc_hpop.mjs static/app.js

import { makeSandbox, settle, byClass, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const rows = [{ id: "XR-1", title: "запуск с выбором подписки", sect: "backlog", type: "task",
  cost: "S", r: 20, r_parts: [8, 4, 4, 2, 2], moved: "2026-08-24" }];

// Подписок на машине две: с одной выбирать нечего, и списка нет вовсе.
const harnesses = [{ name: "подписка-раз", default: true, tiers: ["pro", "max"] },
  { name: "подписка-два", tiers: ["pro"] }];

const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path.includes("/tasks/") && (!init || !init.method)) {
    return { project: "demo", id: "XR-1", row: rows[0], after: [], blocks: [],
      file: "docs/tasks/XR-1.md", text: "# XR-1\n\nПостановка.\n" };
  }
  if (path.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ key: "backlog", title: "Backlog", rows }] },
      works: [] };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path.endsWith("/works")) return { works: [] };
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [], buckets: [] };
  if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
  return {};
});

const groups = byId.get("groups");
await settle();
await sandbox.renderTask("demo", [], "XR-1");
await settle();

const acts = byClass(groups, "tacts");
if (!acts) fail("на форме задачи нет места под действия: " + dump(groups).slice(0, 300));
const split = byClass(acts, "split");
if (!split) fail("у кнопки запуска нет узкой части выбора подписки: " + dump(acts));
const more = deepBtn(split, "more2");
const pop = byClass(split, "hpop");
if (!more || !pop) fail("стрелки выбора или списка подписок в разметке нет: " + dump(split));

// Главная часть экрана начинается за боковой колонкой: всё, что левее, режется
// её границей. Список широкий, кнопка узкая.
groups.getBoundingClientRect = () => ({ left: 208, top: 60, right: 1400, bottom: 900 });
pop.getBoundingClientRect = () => ({ width: 340, height: 220, left: 0, right: 340,
  top: 0, bottom: 220 });

const open = () => {
  if (!pop.hidden) more.handlers.click({ stopPropagation: () => {} });
  more.handlers.click({ stopPropagation: () => {} });
};
const cls = () => String(pop.className || "").split(" ");

// --- кнопка у левого края: список разворачивается вправо ---
{
  more.getBoundingClientRect = () => ({ left: 250, right: 280, top: 300, bottom: 330,
    width: 30, height: 30 });
  open();
  if (pop.hidden) fail("список подписок не раскрылся вовсе");
  if (!cls().includes("rt")) {
    fail("список у левого края растёт влево и уходит под колонку меню: " + pop.className);
  }
}

// --- места слева хватает: список висит на кнопке, как и раньше ---
{
  more.getBoundingClientRect = () => ({ left: 900, right: 930, top: 300, bottom: 330,
    width: 30, height: 30 });
  open();
  if (cls().includes("rt")) {
    fail("список посреди экрана развёрнут вправо, хотя слева места вдоволь: " + pop.className);
  }
}

// --- мерить нечем: сторона остаётся прежней, догадка тут хуже ---
{
  more.getBoundingClientRect = () => ({ left: 0, right: 0, top: 0, bottom: 0,
    width: 0, height: 0 });
  open();
  if (cls().includes("rt")) fail("без размеров узла список всё же переставлен: " + pop.className);
}

console.log("poc_hpop: ok");
