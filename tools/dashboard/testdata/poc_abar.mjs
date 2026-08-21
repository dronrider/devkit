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

// --- режим чтения: кнопка переехала в строку статуса к карандашу ---
const read = deepBtn(byClass(groups, "tmodes"), "tpen");
if (!read) fail("кнопок режимов в строке статуса нет");
const reads = (byClass(groups, "tmodes").children || []).filter((k) => k.tagName === "BUTTON");
if (reads.length !== 2) fail("в строке статуса не две кнопки режимов: " + reads.length);
reads[1].handlers.click({ stopPropagation: () => {} });
if (!String(fpanel.className).includes("wide")) fail("режим чтения не включился");
reads[1].handlers.click({ stopPropagation: () => {} });
if (String(fpanel.className).includes("wide")) fail("режим чтения не выключился");

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
const pen = (byClass(groups, "tmodes").children || []).filter((k) => k.tagName === "BUTTON")[0];
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

// --- у задачи в бэклоге полоса стоит и без правки: там своё действие ---
// Правку сперва отменяем: над тронутой формой перерисовка экрана запрещена, и
// следующий renderTask вернулся бы ни с чем.
const drop = deepBtn(bar(), "Отменить правку");
if (drop) drop.handlers.click({ stopPropagation: () => {} });
await settle();
works = [];
await sandbox.renderTask("demo", works, "XR-2");
await settle();
if (!bar()) fail("полоса действий пропала там, где действия есть");
if (!deepBtn(bar(), "Выполнить")) fail("в полосе нет кнопки действия: " + dump(bar()));

console.log("экран задачи: пустая полоса действий не рисуется, постановка открывается " +
  "разметкой, разворот на колонку и обратно, правка приводит кнопки, карандаш работает " +
  "и над тронутой формой, колонок нет, действия задачи на месте");

// --- строка In progress: своя работа против чужой машины ---
// Строку оценивает реестр этой машины. Есть наши сессии, живые или
// кончившиеся, значит работа наша: с ней разговаривают и её продолжают. Нет ни
// одной, значит задачу взяли в другом месте, и запускать её отсюда нечем.
{
  const ours = sandbox.rowAction("demo", { id: "XR-1", title: "наша работа", run: "gone" }, "in-progress");
  if (!deepBtn(ours, "Чат") || !deepBtn(ours, "Продолжить")) {
    fail("у нашей кончившейся сессии нет входа в чат и продолжения: " + dump(ours));
  }
  const live = sandbox.rowAction("demo", { id: "XR-1", title: "живой чат", run: "session" }, "in-progress");
  if (!deepBtn(live, "Чат") || !deepBtn(live, "Продолжить")) {
    fail("у живого чата задачи нет тех же кнопок: " + dump(live));
  }
  if (dump(live).includes("ведёт другая сессия")) {
    fail("подпись про чужую сессию вернулась: " + dump(live));
  }
  const alien = sandbox.rowAction("demo", { id: "XR-2", title: "чужая", run: "other" }, "in-progress");
  if (deepBtn(alien, "Продолжить") || deepBtn(alien, "Чат") || tag(alien, "BUTTON")) {
    fail("у чужой работы остались кнопки: " + dump(alien));
  }
  if (!dump(alien).includes("в работе на другой машине")) {
    fail("чужая работа не названа словами: " + dump(alien));
  }
  // Backlog не трогали: там запуск как был.
  const back = sandbox.rowAction("demo", { id: "XR-3", title: "новая" }, "backlog");
  if (!tag(back, "BUTTON")) fail("в Backlog пропала кнопка запуска: " + dump(back));
}

// --- чип проверенной строки говорит человеку, ждут ли его ---
{
  const mine = sandbox.checkChip({ id: "XR-1", sect: "check", accept: "mixed",
    notes: ["код слит", "без выката"] });
  if (!dump(mine).includes("ждёт вашей приёмки")) fail("приёмка человека не названа: " + dump(mine));
  if (!String(mine.title || "").includes("mixed") || !String(mine.title).includes("код слит")) {
    fail("детали не уехали в подсказку: " + mine.title);
  }
  const auto = sandbox.checkChip({ id: "XR-2", sect: "check", accept: "agent", notes: ["код слит"] });
  if (!dump(auto).includes("агент проверит сам")) fail("агентская приёмка не названа: " + dump(auto));
  if (sandbox.checkChip({ id: "XR-3", sect: "backlog", notes: [] })) {
    fail("чип приёмки вылез вне Check");
  }
}

console.log("строка доски: своя работа с кнопками, чужая машина без них, чип приёмки по её виду");
