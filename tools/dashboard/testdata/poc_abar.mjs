// Стенд экрана задачи (ветка poc-chat): полоса действий, просмотр постановки
// разметкой и правка по карандашу. Стенд поймал два настоящих бага: пустая
// карточка полосы действий висела рамкой у задачи, которую ведёт чужое окно
// (мера пустоты считала скрытые кнопки правки), и карандаш молчал вовсе над
// тронутой формой, потому что перерисовка экрана там запрещена и переключать
// режим ею нельзя.
//
// Зовётся: node testdata/poc_abar.mjs static/app.js

import { makeSandbox, settle, dump, tag, byClass, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const rows = [
  { id: "XR-1", title: "дашборд без дёрганья", type: "task", cost: "M", r: 30,
    r_parts: [25, 3, 1, 0, 1], sect: "in-progress" },
  { id: "XR-2", title: "строка доски", type: "task", cost: "S", r: 8,
    r_parts: [5, 2, 1, 0, 0], sect: "backlog" },
];

// Задачу XR-2 ведёт чужое окно: своих действий у неё нет вовсе, и полосе
// действий стоять пустой рамкой не за чем.
let works = [{ id: "XR-2", via: "session", title: "строка доски" }];

const app = appPathArg();
const { sandbox, byId } = makeSandbox(app, (path, init) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works }] };
  if (path.includes("/tasks/") && (!init || !init.method)) {
    const id = decodeURIComponent(path.slice(path.lastIndexOf("/") + 1));
    const row = rows.find((r) => r.id === id);
    if (!row) return { error: "нет строки" };
    return {
      project: "demo", id, row, after: [], blocks: [],
      file: "docs/tasks/" + id + ".md",
      text: "# " + id + "\n\nОписание со **списком**:\n\n- раз\n- два\n",
    };
  }
  if (path.endsWith("/board")) return { board: { sections: [{ key: "backlog", rows }] }, works };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path.startsWith("/api/notifications")) return { exists: true, items: [] };
  if (path === "/api/quota") return { buckets: [] };
  return {};
});

const groups = byId.get("groups");
const bar = () => byClass(groups, "abar");

// app.js на загрузке сам зовёт refresh: даём ему дорисовать доску, иначе его
// отрисовка ляжет поверх нашей и стенд будет судить о чужом экране.
await settle();
await sandbox.renderTask("demo", works, "XR-2");
await settle();

// --- полоса действий: пустой в разметку не встаёт ---
if (bar()) fail("полоса действий встала в разметку пустой: " + dump(bar()));

// --- просмотр постановки разметкой ---
const view = byClass(groups, "fview");
if (!view || view.hidden) fail("постановка открылась не просмотром: " + dump(groups).slice(0, 200));
if (!dump(view).includes("списком")) fail("разметка постановки не собралась: " + dump(view));
const fpanel = byClass(groups, "fpanel");
const ta0 = tag(fpanel, "TEXTAREA");
if (ta0 && !ta0.hidden) fail("в просмотре осталось открытое поле правки");

// --- разворот блока на всю колонку и обратно ---
const wide = deepBtn(fpanel, "fwide");
if (!wide) fail("кнопки разворота нет");
wide.handlers.click({ stopPropagation: () => {} });
if (!String(fpanel.className).includes("wide")) fail("разворот не включился");
wide.handlers.click({ stopPropagation: () => {} });
if (String(fpanel.className).includes("wide")) fail("обратная кнопка не свернула блок");

// --- правка формы приводит полосу вместе с кнопками ---
const title = byClass(groups, "tedit");
if (!title) fail("поля заголовка на экране нет");
title.value = "новый заголовок задачи";
title.handlers.input();
await settle();
const shown = bar();
if (!shown) fail("правка формы не привела полосу с кнопками");
const save = deepBtn(shown, "Сохранить");
if (!save || save.hidden) fail("кнопки «Сохранить» в полосе нет: " + dump(shown));
if (save.disabled) fail("«Сохранить» погашено на честной правке");

// --- карандаш переключает режим и над тронутой формой ---
// Перерисовка экрана над тронутой формой запрещена (она стёрла бы
// несохранённое), поэтому режим меняется по месту; раньше кнопка тут молчала.
const pen = deepBtn(groups, "tpen");
if (!pen) fail("карандаша правки нет");
pen.handlers.click({ stopPropagation: () => {} });
const panelNow = byClass(groups, "fpanel");
const taNow = tag(panelNow, "TEXTAREA");
const viewNow = byClass(panelNow, "fview");
if (!taNow || taNow.hidden) fail("карандаш не пустил в правку");
if (!viewNow || !viewNow.hidden) fail("в правке остался просмотр");
pen.handlers.click({ stopPropagation: () => {} });
if (!taNow.hidden || viewNow.hidden) fail("возврат в просмотр не сработал");

// --- колонок на экране задачи нет: ранг и зависимости своими строками ---
if (byClass(groups, "rrail")) fail("правая колонка осталась на экране задачи");

// --- у задачи со своими действиями полоса стоит и без правки ---
works = [];
await sandbox.renderTask("demo", works, "XR-1");
await settle();
if (!bar()) fail("полоса действий пропала там, где действия есть");
if (!deepBtn(bar(), "Продолжить")) fail("в полосе нет кнопки действия: " + dump(bar()));

console.log("экран задачи: пустая полоса действий не рисуется, постановка открывается " +
  "разметкой, разворот на колонку и обратно, правка приводит кнопки, карандаш работает " +
  "и над тронутой формой, колонок нет, действия задачи на месте");
