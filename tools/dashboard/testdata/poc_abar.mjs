// Стенд экрана задачи (ветка poc-chat): полоса действий, просмотр постановки
// разметкой и правка по карандашу. Стенд поймал два настоящих бага: пустая
// карточка полосы действий висела рамкой у задачи, которую ведёт чужое окно
// (мера пустоты считала скрытые кнопки правки), и карандаш молчал вовсе над
// тронутой формой, потому что перерисовка экрана там запрещена и переключать
// режим ею нельзя.
//
// Зовётся: node testdata/poc_abar.mjs static/app.js

import { makeSandbox, settle, dump, tag, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";
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
const { sandbox, byId, posted } = makeSandbox(app, (path, init) => {
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
// Там же стоит и вход в разговор: чат по задаче это такое же действие над
// открытой задачей, и своей дороги ему не нужно (решение пользователя).
const read = deepBtn(byClass(groups, "tmodes"), "tpen");
if (!read) fail("кнопок режимов в строке статуса нет");
const reads = (byClass(groups, "tmodes").children || []).filter((k) => k.tagName === "BUTTON");
if (reads.length !== 3) fail("в строке статуса не три кнопки: " + reads.length);
const talk = (byClass(groups, "tmodes").children || [])
  .find((k) => String(k.title) === "Чат по задаче");
if (!talk) fail("входа в чат нет рядом с карандашом: " + dump(byClass(groups, "tmodes")));
// Кнопка берётся по подсказке, а не по месту в ряду: соседей у неё прибавилось.
const readBtn = reads.find((b) => String(b.title).includes("Режим чтения"));
if (!readBtn) fail("кнопки режима чтения нет: " + reads.map((b) => b.title).join(", "));
readBtn.handlers.click({ stopPropagation: () => {} });
if (!String(fpanel.className).includes("wide")) fail("режим чтения не включился");
readBtn.handlers.click({ stopPropagation: () => {} });
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
// Карандаш тоже узнаётся подсказкой: первым в ряду теперь стоит вход в чат.
const pen = (byClass(groups, "tmodes").children || [])
  .filter((k) => k.tagName === "BUTTON")
  .find((b) => String(b.title).includes("Править"));
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

// --- строка In progress: чем нажатие оборачивается ---
// Строку оценивает реестр этой машины. Есть наши сессии, живые или
// кончившиеся, значит работа наша: с ней разговаривают и её продолжают. Нет ни
// одной, значит живой работы за строкой не видно, и конвейер ей возвращён:
// третьего ответа, other («исполнителя не видно»), у признака больше нет, он
// попадал ровно в те задачи, которые человек вёл из дашборда.
{
  // Продолжение работы стоит кнопкой работы, первой в колонке: место кнопки от
  // состояния строки не зависит, а зависит от него только то, что она делает.
  const work = (box) => {
    const btn = byClass(box, "rmain") || byClass(box, "rstop");
    if (!btn) fail("у строки нет кнопки работы: " + dump(box));
    return btn;
  };
  const ours = sandbox.rowAction("demo", { id: "XR-1", title: "наша работа", run: "gone" }, "in-progress");
  if (work(ours).attrs["aria-label"] !== "Продолжить") {
    fail("у нашей кончившейся сессии нет продолжения: " + dump(ours));
  }
  const live = sandbox.rowAction("demo", { id: "XR-1", title: "живой чат", run: "session" }, "in-progress");
  if (work(live).attrs["aria-label"] !== "Продолжить") {
    fail("у живого чата задачи нет продолжения: " + dump(live));
  }
  if (dump(live).includes("ведёт другая сессия")) {
    fail("подпись про чужую сессию вернулась: " + dump(live));
  }
  // Наших сессий у строки нет ни одной: приписки про ненайденного исполнителя
  // тут больше нет, и кнопка запуска строке возвращена. Второму исполнителю
  // взяться неоткуда, живой работы за ней не видно. Разговора за такой строкой
  // тоже нет, и главной кнопкой у неё стоит сам запуск.
  const none = sandbox.rowAction("demo", { id: "XR-2", title: "без наших сессий" }, "in-progress");
  const run = byClass(none, "rmain");
  if (!run || run.attrs["aria-label"] !== "Продолжить") {
    fail("у строки без наших сессий пропал конвейер: " + dump(none));
  }
  if (dump(none).includes("исполнителя не видно")) {
    fail("снятая приписка про чужую машину вернулась: " + dump(none));
  }
  posted.length = 0;
  run.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!posted.some((p) => p.endsWith("/runs"))) {
    fail("нажатие на строке без наших сессий не завело работу: " + JSON.stringify(posted));
  }
  // Backlog не трогали: там запуск как был.
  const back = sandbox.rowAction("demo", { id: "XR-3", title: "новая" }, "backlog");
  if (!byClass(back, "rmain")) fail("в Backlog пропала кнопка запуска: " + dump(back));
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

console.log("строка доски: своя работа продолжается, строка без наших сессий запускается, " +
  "чип приёмки по её виду");

// --- кончившаяся сессия продолжается, а не заводит второго исполнителя ---
// Наши сессии у строки есть, просто ни одна не жива. Разговор с такой строкой
// продолжают тем же ходом, каким его вели, и конвейер поверх него не заводят:
// это был бы второй исполнитель на ту же строку (жалоба на DK-460).
{
  const ours = sandbox.rowAction("demo", { id: "XR-9", title: "грумили", run: "gone" }, "in-progress");
  const go = byClass(ours, "rmain");
  if (!go || go.attrs["aria-label"] !== "Продолжить") {
    fail("у кончившейся сессии пропало продолжение: " + dump(ours));
  }
  posted.length = 0;
  go.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!posted.some((p) => p.endsWith("/continue"))) {
    fail("продолжение кончившейся сессии пошло не той ручкой: " + JSON.stringify(posted));
  }
  if (posted.some((p) => p.endsWith("/runs"))) {
    fail("кончившаяся сессия завела второго исполнителя конвейером: " + JSON.stringify(posted));
  }
}

// --- строка Check: кнопка приёмки, и выбор запуска тот же, что у прочих ---
{
  const row = { id: "XR-5", title: "проверенная", sect: "check", accept: "mixed", harness: "glm" };
  const bar = sandbox.rowAction("demo", row, "check");
  const btn = byClass(bar, "rmain");
  if (!btn || btn.attrs["aria-label"] !== "Проверить") {
    fail("кнопки «Проверить» на строке Check нет: " + dump(bar));
  }
  // Подписок на машине этого стенда нет вовсе, и выбирать нечего ни одной
  // строке. Сторожится тут именно это: набор кнопок у строки Check тот же, что
  // у строки очереди, и прикреплённая подписка его больше не режет (замечание
  // пользователя о разном выборе по секциям).
  const kinds = (box) => allByClass(box, "btn").map((b) => String(b.className)
    .split(" ").filter((c) => c.startsWith("r")).join(".")).join(" | ");
  const queue = sandbox.rowAction("demo", { id: "XR-8", title: "в очереди" }, "backlog");
  if (kinds(bar) !== kinds(queue)) {
    fail("набор кнопок у Check и у очереди разный: «" + kinds(bar) + "» против «" +
      kinds(queue) + "»");
  }
  if (!String(btn.title || "").includes("glm")) {
    fail("подсказка не называет подписку задачи: " + btn.title);
  }
  if (!String(btn.title).includes("ваша приёмка")) {
    fail("подсказка не говорит, что нажатие это приёмка: " + btn.title);
  }
  const auto = sandbox.rowAction("demo",
    { id: "XR-6", title: "агентская", sect: "check", accept: "agent", harness: "claude-code" }, "check");
  const abtn = byClass(auto, "rmain");
  if (!abtn || !String(abtn.title || "").includes("claude-code")) {
    fail("у агентской приёмки та же кнопка с подписью не собралась: " + dump(auto));
  }
}

console.log("Check: одна кнопка с прикреплённой подпиской, кончившаяся сессия продолжается, " +
  "а не заводит второго исполнителя");

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
  // Кнопка чата стоит в панели рядом с карандашом, а класс у неё от строки
  // доски, где кнопки мельче: без своего роста она сидела ниже соседей на
  // четыре пикселя (замечание пользователя).
  const talkRule = ruleOf(css, ".tmodes .btn");
  if (!talkRule.includes("height:var(--ctl)")) {
    fail("кнопка чата в панели формы ниже соседей: " + JSON.stringify(talkRule));
  }
  if (!ruleOf(css, ".tmodes .btn-ico").includes("width:var(--ctl)")) {
    fail("кнопка чата в панели формы уже соседей: значок не по росту панели");
  }
}

console.log("форма: действия в командной панели, отдельной полосы под них нет");

// --- строка накопителя: тот же вход в разговор, что у строки доски ---
// Черновик это та же задача, просто в черновом исполнении, и обсуждать его
// надо тем же способом (решение пользователя).
{
  const row = sandbox.draftRow("demo", { id: "XR-D1", title: "мысль с телефона", age_words: "вчера" });
  const talk = allByClass(row, "btn").find((b) => String(b.title) === "Чат по задаче");
  if (!talk) fail("у строки накопителя нет входа в разговор: " + dump(row));
  if (!String(talk.className).includes("btn-ico")) fail("кнопка чата не значком: " + talk.className);
  sandbox.location.hash = "#demo/drafts";
  talk.handlers.click({ stopPropagation: () => {} });
  if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes("XR-D1")) {
    fail("чат черновика открыл не его разговор: " + sandbox.location.hash);
  }
  // Нажатие на кнопку не уводит внутрь записи: у строки своё нажатие.
  if (sandbox.location.hash.includes("/draft/")) {
    fail("кнопка чата утащила на экран записи: " + sandbox.location.hash);
  }
}

// --- форма документа: кнопки режимов стоят в строке названия ---
// У документа нет ни типа, ни цены, ни пометок, и строка статуса под одни
// кнопки занимала целую строку экрана (замечание пользователя).
{
  const view = sandbox.formPage({
    key: "doc", project: "demo", id: "XR-1",
    crumb: [{ text: "Доска demo", go: () => {} }],
    titleText: "LLD: разговор вокруг задачи",
    detail: { file: "docs/lld/XR-1.md", text: "# заголовок\n\nтекст", note: "документ пуст" },
    form: { text: "# заголовок\n\nтекст" },
    has: { file: true, pencil: true, read: true },
  });
  const modes = byClass(view.page, "tmodes");
  if (!modes) fail("кнопок режимов на форме документа нет");
  const line = byClass(view.page, "thline");
  if (!byClass(line, "tmodes")) fail("кнопки режимов стоят не в строке названия: " + dump(line));
  if (byClass(view.page, "tchips")) fail("под кнопки осталась своя строка статуса");

  // У формы задачи строка статуса своя, и кнопки остаются в ней.
  const task = sandbox.formPage({
    key: "task", project: "demo", id: "XR-2",
    crumb: [{ text: "Доска demo", go: () => {} }],
    form: { title: "задача", type: "task", cost: "M" },
    detail: { file: "docs/tasks/XR-2.md", text: "текст" },
    has: { title: true, type: true, cost: true, read: true, chat: true },
  });
  if (byClass(byClass(task.page, "thline"), "tmodes")) {
    fail("на форме задачи кнопки уехали из строки статуса");
  }
  if (!byClass(byClass(task.page, "tchips"), "tmodes")) {
    fail("на форме задачи кнопок режимов нет в строке статуса");
  }
}

console.log("формы: чат у черновика, кнопки режимов без пустой строки");

// --- блок ранга: просмотр текстом, поля правки карандашом ---
// Прежде он занимал две строки и держал пять жирных селектов даже в просмотре
// (замечание пользователя, дожим после первой правки): теперь просмотр это
// компактная строка «итог и слагаемые текстом», а поля появляются в режиме
// правки, той же кнопкой, что и остальная форма.
{
  const view = sandbox.formPage({
    key: "task", project: "demo", id: "XR-3",
    crumb: [{ text: "Доска demo", go: () => {} }],
    form: { title: "задача", type: "task", cost: "M", parts: [25, 4, 1, 0, 2] },
    detail: { file: "docs/tasks/XR-3.md", text: "текст" },
    has: { title: true, type: true, cost: true, rank: true },
  });
  const card = byClass(view.page, "rcard");
  if (!card) fail("блока ранга на форме нет");
  const said = dump(card);
  if (said.includes("RANKING.md")) fail("надпись про правило осталась: " + said);
  if (said.includes("Ранг")) fail("подпись «Ранг» осталась при крупном числе: " + said);
  // Итог считается из слагаемых формы и стоит один раз.
  const sum = byClass(card, "v");
  if (!sum || sum.textContent !== "32") fail("итог ранга не тот: " + (sum && sum.textContent));
  // Просмотр: класса правки нет, слагаемые стоят текстом при итоге.
  if (card.className.includes("redit")) fail("форма просмотра открыта в режиме правки: " + card.className);
  const rv = byClass(card, "rview");
  if (!rv || !rv.textContent.includes("серьёзность 25") || !rv.textContent.includes("рычаг 2")) {
    fail("в просмотре нет слагаемых текстом: " + (rv && rv.textContent));
  }
  // Полей правки пять, по числу слагаемых, и лежат они своей строкой.
  const rows = allByClass(card, "rrow");
  if (rows.length !== 5) fail("полей правки ранга не пять: " + rows.length);
  if (card.children.length !== 2) {
    fail("в блоке ранга не две части (итог и поля): " + card.children.length);
  }
  // Правка: карандаш даёт классу rcard режим redit, стили показывают поля.
  view.setEdit(true);
  if (!card.className.includes("redit")) fail("режим правки не включил поля ранга: " + card.className);
  view.setEdit(false);
  if (card.className.includes("redit")) fail("выход из правки не спрятал поля ранга");
  // Стили держат обещание: без redit тело с полями спрятано, в redit спрятан
  // текст слагаемых. Проверяется по самому style.css, мок разметки этого не
  // видит.
  const css0 = await readCss();
  const rule = (sel) => {
    const m = new RegExp("(^|[\\n}])" + sel.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") +
      "\\{([^}]*)\\}").exec(css0);
    return m ? m[2] : "";
  };
  if (!rule(".rcard:not(.redit) .rbody").includes("display:none")) {
    fail("в просмотре поля ранга не спрятаны стилем");
  }
  if (!rule(".rcard.redit .rview").includes("display:none")) {
    fail("в правке текст слагаемых не спрятан стилем");
  }
}

console.log("ранг: просмотр текстом при итоге, поля правки карандашом");
