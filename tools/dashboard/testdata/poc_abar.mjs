// Стенд экрана задачи (ветка poc-chat): полоса действий, просмотр постановки
// разметкой и правка по карандашу. Стенд поймал два настоящих бага: пустая
// карточка полосы действий висела рамкой у задачи, которую ведёт чужое окно
// (мера пустоты считала скрытые кнопки правки), и карандаш молчал вовсе над
// тронутой формой, потому что перерисовка экрана там запрещена и переключать
// режим ею нельзя.
//
// Зовётся: node testdata/poc_abar.mjs static/app.js

import { makeSandbox, settle, dump, tag, byClass, deepBtn, fail, appPathArg } from "./poc_dom.mjs";
import { readFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Стилевой файл читается тем же стендом: клип по родителю виден только в CSS,
// а мок разметки о нём не знает.
const readCss = () => readFile(join(dirname(fileURLToPath(import.meta.url)), "..", "static", "style.css"), "utf8");

const ruleOf = (css, sel) => {
  const m = new RegExp("(^|[\\n}])\\" + sel.slice(0, 1) + sel.slice(1) + "\\{([^}]*)\\}").exec(css);
  return m ? m[2] : "";
};

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

// --- у блока описания нет пустой шапки ---
// Карандаш, чтение и действия уехали в командную панель, и в шапке блока не
// осталось ничего: над описанием стояла пустая полоса с разделителем. Узла
// нет вовсе, а не спрятан стилем, иначе рамка с отступами осталась бы видна.
if (byClass(fpanel, "fhead")) fail("у блока описания осталась пустая шапка: " + dump(fpanel).slice(0, 200));

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

// --- у задачи в бэклоге действие стоит в командной панели ---
// Правку сперва отменяем: над тронутой формой перерисовка экрана запрещена, и
// следующий renderTask вернулся бы ни с чем.
const drop = deepBtn(bar(), "Отменить правку");
if (drop) drop.handlers.click({ stopPropagation: () => {} });
await settle();
works = [];
await sandbox.renderTask("demo", works, "XR-2");
await settle();
if (!deepBtn(byClass(groups, "tacts"), "Выполнить")) {
  fail("кнопки действия нет в командной панели: " + dump(byClass(groups, "tmodes")));
}
if (bar() && deepBtn(bar(), "Выполнить")) fail("кнопка осталась и в старой полосе");

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
  // У чужой работы скрыт конвейер, а не разговор: обсуждать её человеку
  // приходится здесь, и чат её не трогает (замечание пользователя).
  const alien = sandbox.rowAction("demo", { id: "XR-2", title: "чужая", run: "other" }, "in-progress");
  if (deepBtn(alien, "Продолжить") || deepBtn(alien, "Выполнить")) {
    fail("у чужой работы остались кнопки конвейера: " + dump(alien));
  }
  if (!deepBtn(alien, "Чат")) {
    fail("у чужой работы пропал вход в разговор: " + dump(alien));
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

// --- разговорный чат строку не присваивает ---
// Груминг и привязка рукой это чтение задачи, а не работа над ней: строка с
// одним таким чатом остаётся чужой, конвейера у неё нет (жалоба на DK-460).
// Сам разговор при этом доступен: он и был поводом открыть такую строку.
{
  const alien = sandbox.rowAction("demo", { id: "XR-9", title: "грумили", run: "other" }, "in-progress");
  if (deepBtn(alien, "Продолжить") || deepBtn(alien, "Выполнить")) {
    fail("строку присвоил разговорный чат: у неё появился конвейер");
  }
  if (!deepBtn(alien, "Чат")) fail("у строки с разговорным чатом нет входа в него");
}

// --- строка Check: одна кнопка, без выбора подписки ---
{
  const row = { id: "XR-5", title: "проверенная", sect: "check", accept: "mixed", harness: "glm" };
  const bar = sandbox.rowAction("demo", row, "check");
  const btn = deepBtn(bar, "Проверить и закрыть");
  if (!btn) fail("кнопки «Проверить и закрыть» на строке Check нет: " + dump(bar));
  if (dump(bar).includes("Выбрать подписку") || byClass(bar, "hpop")) {
    fail("выпадашка подписки осталась на кнопке Check: " + dump(bar));
  }
  if (!String(btn.title || "").includes("glm")) {
    fail("подсказка не называет подписку задачи: " + btn.title);
  }
  if (!String(btn.title).includes("ваша приёмка")) {
    fail("подсказка не говорит, что нажатие это приёмка: " + btn.title);
  }
  const auto = sandbox.rowAction("demo",
    { id: "XR-6", title: "агентская", sect: "check", accept: "agent", harness: "claude-code" }, "check");
  const abtn = deepBtn(auto, "Проверить и закрыть");
  if (!abtn || !String(abtn.title || "").includes("claude-code")) {
    fail("у агентской приёмки та же кнопка с подписью не собралась: " + dump(auto));
  }
}

console.log("Check: одна кнопка с прикреплённой подпиской, разговорный чат строку не присваивает");

// --- действия формы переехали в командную панель ---
// Отдельная полоса под строкой статуса держала карточку ради двух кнопок.
// Кнопки стоят там же, где карандаш и режим чтения, левее их, а подписи причин
// остаются полосой: в углу им не поместиться.
{
  await sandbox.renderTask("demo", [], "XR-2");
  await settle();
  const scr = groups;
  const panel = byClass(scr, "tmodes");
  if (!panel) fail("командной панели на форме нет");
  const acts = byClass(panel, "tacts");
  if (!acts) fail("места под действия в панели нет: " + dump(panel).slice(0, 200));
  if (!deepBtn(acts, "Выполнить")) fail("кнопка запуска не переехала в панель: " + dump(panel));
  const kids = panel.children.map((k) => String(k.className || ""));
  if (kids.indexOf("tacts") !== 0) fail("действия стоят правее карандаша: " + kids.join(","));
  const bar = byClass(scr, "abar");
  if (bar && deepBtn(bar, "Выполнить")) fail("кнопка осталась и в старой полосе");
}

// Список подписки у кнопки запуска раскрывается поверх всего: до переезда он
// уходил под блок меню, и выбрать в нём было нечего. Клип по родителю тут
// важнее координат: спрятанный overflow резал бы список молча.
{
  const css = await readCss();
  const acts = ruleOf(css, ".tacts");
  if (!acts.includes("position:relative")) fail(".tacts без своей системы координат: " + acts);
  if (!/z-index:\d+/.test(acts)) fail(".tacts без высоты в слоях: список уйдёт под меню");
  for (const sel of [".tmodes", ".tacts", ".tchips"]) {
    const rule = ruleOf(css, sel);
    if (/overflow[^;]*:(hidden|clip|auto|scroll)/.test(rule)) {
      fail("родитель списка подписки режет содержимое: " + sel + " {" + rule + "}");
    }
  }
}

console.log("форма: действия в командной панели, отдельной полосы под них нет");
