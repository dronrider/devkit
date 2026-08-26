// Стенд табличного вида трёх разделов доски (POC DK-397, ветка poc-chat).
//
// Задачи, сессии и накопитель это три списка об одном и том же, а выглядели
// они по-разному: у накопителя порядок правила кнопка о двух положениях, у
// задач с сессиями порядка не выбирали вовсе, ранг с датой лежали припиской в
// хвосте строки, а закрытие сессии занимало под слово «Закрыть» полколонки.
// Предмет стенда это общий приём: у каждого раздела своя шапка колонок, у
// колонки своя подпись, нажатие правит порядок, повторное разворачивает,
// направление видно значком, а выбор живёт в хранилище своим ключом.
//
// Отдельно проверяется, что жест очереди не обещает того, чего не делает:
// переставленный чужой колонкой список места строки не назначает, и
// перетаскивание там не заводится.
//
// Накопитель со своими колонками стоит в poc_dsort.mjs, тут задачи и сессии.
//
// Зовётся: node testdata/poc_tbl.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

// Очередь трёх строк: номер, ранг, дата правки и заголовок расходятся между
// собой нарочно, иначе порядок по одной колонке нельзя отличить от порядка по
// другой.
const rows = [
  { id: "XR-101", title: "ясли для сессий", sect: "backlog", r: 40, r_parts: [10, 8, 7, 8, 7],
    moved: "2026-08-20", cost: "-" },
  { id: "XR-102", title: "автономный выкат", sect: "backlog", r: 62, r_parts: [25, 9, 9, 10, 9],
    moved: "2026-08-11", cost: "-" },
  { id: "XR-103", title: "экран записи не открывается", sect: "backlog", r: 51,
    r_parts: [15, 9, 9, 9, 9], moved: "2026-08-24", cost: "-" },
];

// Сессии трёх состояний: умолчание раздела это состояние, и по нему занятая
// стоит выше простаивающей.
const works = [
  { kind: "session", via: "session", session: "s-idle", own: true, live: "idle",
    title: "буквой позже", started: 1000, moved: 5000 },
  { kind: "session", via: "session", session: "s-busy", own: true, live: "busy",
    title: "буквой раньше", started: 3000, moved: 9000 },
  { kind: "session", via: "session", session: "s-wait", own: true, live: "waiting",
    title: "буквой средне", started: 2000, moved: 7000 },
];

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ key: "backlog", title: "Backlog", rows }] },
      works };
  }
  if (path.endsWith("/works")) return { works };
  // Запись накопителя тут одна и нужна ради разметки: порядок и колонки
  // накопителя разобраны своим стендом (poc_dsort.mjs).
  if (path.endsWith("/drafts")) {
    return { drafts: [{ id: "XR-D1", title: "мысль с телефона", prio: "mid",
      written: "2026-08-10", moved: "2026-08-10" }] };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};

const head = (kind) => allByClass(groups, "tblh")
  .find((h) => String(h.className).split(" ").includes("h-" + kind)) || null;
const col = (kind, label) => allByClass(head(kind) || {}, "tblb")
  .find((btn) => dump(btn).includes(label)) || null;
const click = async (btn) => {
  btn.handlers.click({ stopPropagation: () => {} });
  await settle();
};
const on = (btn) => Boolean(btn) && String(btn.className).split(" ").includes("tblon");

// --- доска: шапка колонок над секциями ---
{
  await go("#demo");
  const h = head("tasks");
  if (!h) fail("шапки колонок над доской нет: " + dump(groups).replace(/\s+/g, " ").slice(0, 400));
  for (const label of ["Номер", "Задача", "Ранг", "Дата"]) {
    if (!col("tasks", label)) fail("в шапке доски нет колонки «" + label + "»: " + dump(h));
  }
  // Шапка одна на все секции: карточек у доски четыре, и шапка над каждой
  // перевешивала бы список из двух строк.
  if (allByClass(groups, "tblh").length !== 1) {
    fail("шапок у доски больше одной: " + allByClass(groups, "tblh").length);
  }
  // Своего порядка экран доске не назначает: строки стоят так, как их сложила
  // сама доска, и подсвеченной колонки при первом заходе нет.
  if (["Номер", "Задача", "Ранг", "Дата"].some((l) => on(col("tasks", l)))) {
    fail("доска открылась переставленной, хотя порядка никто не просил: " + dump(h));
  }
}

// Порядок строк доски по номеру, сверху вниз.
// Номер лежит не в самой ячейке, а во вложенном узле рядом с кружком
// состояния, и берётся он со всего поддерева ячейки.
const boardIds = () => allByClass(groups, "trow")
  .map((row) => (/XR-\d+/.exec(dump(byClass(row, "id"))) || ["?"])[0]);

// --- ранг и дата стоят своими колонками строки, а не припиской в хвосте ---
{
  const row = allByClass(groups, "trow")[0];
  const kids = [...row.children].map((k) => String(k.className || "").split(" ")[0]);
  if (JSON.stringify(kids) !== JSON.stringify(["id", "tt", "rank", "twhen", "meta"])) {
    fail("ячейки строки доски идут не тем порядком: " + JSON.stringify(kids));
  }
  if (!dump(byClass(row, "twhen")).includes("2026-08-")) {
    fail("дата правки не встала своей ячейкой: " + dump(row).replace(/\s+/g, " "));
  }
}

// --- нажатие на «Ранг» переставляет очередь, повторное разворачивает ---
{
  const was = boardIds();
  if (JSON.stringify(was) !== JSON.stringify(["XR-101", "XR-102", "XR-103"])) {
    fail("доска открылась не порядком самой доски: " + JSON.stringify(was));
  }
  await click(col("tasks", "Ранг"));
  if (JSON.stringify(boardIds()) !== JSON.stringify(["XR-102", "XR-103", "XR-101"])) {
    fail("очередь не встала по рангу, тяжёлые сверху: " + JSON.stringify(boardIds()));
  }
  if (!on(col("tasks", "Ранг"))) fail("шапка не подсветила колонку ранга");
  if (!tag(col("tasks", "Ранг"), "I")) fail("направление порядка не показано значком");
  await click(col("tasks", "Ранг"));
  if (JSON.stringify(boardIds()) !== JSON.stringify(["XR-101", "XR-103", "XR-102"])) {
    fail("повторное нажатие не развернуло порядок: " + JSON.stringify(boardIds()));
  }
}

// --- порядок по дате и по заголовку ---
{
  await click(col("tasks", "Дата"));
  if (JSON.stringify(boardIds()) !== JSON.stringify(["XR-103", "XR-101", "XR-102"])) {
    fail("очередь не встала по дате, свежие сверху: " + JSON.stringify(boardIds()));
  }
  await click(col("tasks", "Задача"));
  if (JSON.stringify(boardIds()) !== JSON.stringify(["XR-102", "XR-103", "XR-101"])) {
    fail("очередь не встала по заголовку: " + JSON.stringify(boardIds()));
  }
}

// --- жест очереди живёт только своим порядком доски ---
// Перетаскивание правит ценность строки, а выводится она из места в списке: в
// списке по чужой колонке место не обещает ничего, и жест там не заводится.
{
  const dragging = () => allByClass(groups, "trow")
    .filter((row) => Boolean(row.handlers.pointerdown)).length;
  if (dragging()) {
    fail("перетаскивание осталось в переставленной очереди: строк с жестом " + dragging());
  }
  // Выбор помнится своим ключом, отдельным от накопителя и сессий.
  if (sandbox.localStorage.getItem("devkit.dash.tasks.sort") !== "title:asc") {
    fail("порядок доски не записался своим ключом: " +
      sandbox.localStorage.getItem("devkit.dash.tasks.sort"));
  }
  // Возврат к порядку доски возвращает и жест: колонку снимают повторным
  // проходом по кругу, а тут проще стереть память и перерисовать.
  sandbox.localStorage.removeItem("devkit.dash.tasks.sort");
  await go("#demo/drafts");
  await go("#demo");
  if (dragging() !== 3) {
    fail("в своём порядке доски жест не завёлся: строк с жестом " + dragging());
  }
}

// --- сессии: та же шапка, свои колонки ---
{
  await go("#demo/sess");
  const h = head("sess");
  if (!h) fail("шапки колонок у сессий нет: " + dump(groups).replace(/\s+/g, " ").slice(0, 400));
  for (const label of ["Состояние", "Работа", "Идёт", "Активность"]) {
    if (!col("sess", label)) fail("в шапке сессий нет колонки «" + label + "»: " + dump(h));
  }
  if (!on(col("sess", "Состояние"))) {
    fail("сессии открылись не по состоянию: " + dump(h));
  }
}

// Порядок сессий: заголовок работы, сверху вниз.
const sessTitles = () => allByClass(groups, "arow")
  .map((row) => String((byClass(byClass(row, "ab"), "tt") || {}).textContent || "?"));

// --- умолчание по состоянию, нажатие правит колонку ---
{
  const was = sessTitles();
  if (JSON.stringify(was) !== JSON.stringify(["буквой раньше", "буквой средне", "буквой позже"])) {
    fail("сессии открылись не занятыми сверху: " + JSON.stringify(was));
  }
  await click(col("sess", "Работа"));
  if (JSON.stringify(sessTitles()) !==
    JSON.stringify(["буквой позже", "буквой раньше", "буквой средне"])) {
    fail("сессии не встали по заголовку работы: " + JSON.stringify(sessTitles()));
  }
  if (sandbox.localStorage.getItem("devkit.dash.sess.sort") !== "title:asc") {
    fail("порядок сессий не записался своим ключом: " +
      sandbox.localStorage.getItem("devkit.dash.sess.sort"));
  }
  // Живой опрос раздела шапку не теряет: он перерисовывает карточку по кругу.
  sandbox.renderSessions("demo", works, "");
  await settle();
  if (!on(col("sess", "Работа"))) {
    fail("опрос сессий сбросил выбранную колонку: " + dump(head("sess")));
  }
  if (JSON.stringify(sessTitles()) !==
    JSON.stringify(["буквой позже", "буквой раньше", "буквой средне"])) {
    fail("опрос сессий сбросил порядок: " + JSON.stringify(sessTitles()));
  }
}

// --- возраст сессии стоит своей колонкой, а закрытие идёт крестиком ---
{
  const row = allByClass(groups, "arow")[0];
  const kids = [...row.children].map((k) => String(k.className || "").split(" ")[0]);
  if (JSON.stringify(kids) !== JSON.stringify(["live", "ab", "atime", "amoved", "aacts"])) {
    fail("ячейки строки сессии идут не тем порядком: " + JSON.stringify(kids));
  }
  const close = byClass(row, "sclose");
  if (!close) fail("кнопки закрытия у сессии нет: " + dump(row).replace(/\s+/g, " "));
  if (String(close.textContent || "").trim()) {
    fail("кнопка закрытия осталась словом, а не значком: " + close.textContent);
  }
  if (!String(close.className).split(" ").includes("btn-ico")) {
    fail("кнопка закрытия не значковая: " + close.className);
  }
  if (!String(close.attrs["aria-label"] || "").includes("Закрыть")) {
    fail("у крестика нет подписи для чтения с экрана: " + JSON.stringify(close.attrs));
  }
}

// --- дата последней активности стоит своей колонкой и правит порядок ---
// Возраст в колонке «Идёт» отвечает на другой вопрос: он про то, сколько
// сессия живёт, а не про то, когда в ней последний раз что-то сказали
// (замечание пользователя).
{
  const moved = () => allByClass(groups, "arow")
    .map((row) => String(dump(byClass(row, "amoved")) || "").trim());
  if (moved().some((said) => !/\d{4}-\d{2}-\d{2}/.test(said))) {
    fail("даты последней активности в строке сессии нет: " + JSON.stringify(moved()));
  }
  // Порядок по ней настоящий: у трёх работ метки 9000, 7000 и 5000 секунд, и
  // убыванием сверху встаёт самая свежая.
  await click(col("sess", "Активность"));
  if (JSON.stringify(sessTitles()) !==
    JSON.stringify(["буквой раньше", "буквой средне", "буквой позже"])) {
    fail("сессии не встали по последней активности: " + JSON.stringify(sessTitles()));
  }
  await click(col("sess", "Активность"));
  if (JSON.stringify(sessTitles()) !==
    JSON.stringify(["буквой позже", "буквой средне", "буквой раньше"])) {
    fail("порядок по активности не развернулся: " + JSON.stringify(sessTitles()));
  }
}

// --- вид собран настоящей таблицей, а не своей сеткой ---
// Два прежних захода собирали таблицу из двух сеток: шапка одной, строки
// другой, ширины связывались переменными. Подписи вставали мимо ячеек, потому
// что сеток было две. Тут разметка настоящая, и колонку у подписи со строкой
// считает движок: шапка и строки обязаны лежать в одной таблице.
{
  for (const [hash, kind, rowCls] of [["#demo", "tasks", "trow"], ["#demo/sess", "sess", "arow"],
    ["#demo/drafts", "drafts", "dsrow"]]) {
    await go(hash);
    const h = head(kind);
    if (!h) fail("шапки раздела " + kind + " нет вовсе");
    if (h.tagName !== "TR") fail("шапка раздела " + kind + " не строка таблицы: " + h.tagName);
    const thead = h.parentNode;
    if (!thead || thead.tagName !== "THEAD") {
      fail("шапка раздела " + kind + " лежит мимо thead: " + (thead && thead.tagName));
    }
    const table = thead.parentNode;
    if (!table || table.tagName !== "TABLE") {
      fail("шапка раздела " + kind + " лежит мимо таблицы: " + (table && table.tagName));
    }
    const bad = [...h.children].filter((c) => c.tagName !== "TH").map((c) => c.tagName);
    if (bad.length) fail("ячейки шапки раздела " + kind + " не th: " + JSON.stringify(bad));
    // Колонки описаны в colgroup: оттуда движок берёт ширины, и правит их тяга
    // границы. Своих правил сетки у строки больше нет.
    const group = [...(table.children || [])].find((k) => k.tagName === "COLGROUP");
    if (!group) fail("в таблице раздела " + kind + " нет colgroup: колонкам неоткуда взять ширину");
    const cols = [...(group.children || [])].filter((c) => c.tagName === "COL");
    if (cols.length !== h.children.length) {
      fail("в разделе " + kind + " колонок " + cols.length + ", а ячеек шапки " +
        h.children.length);
    }
    // Строка лежит в той же таблице, что и шапка: разойдись они таблицами,
    // колонки снова считались бы двумя раскладками.
    const row = allByClass(groups, rowCls)[0];
    if (!row) fail("строки раздела " + kind + " на экране нет");
    if (row.tagName !== "TR") fail("строка раздела " + kind + " не строка таблицы: " + row.tagName);
    const cells = [...row.children].filter((c) => c.tagName !== "TD").map((c) => c.tagName);
    if (cells.length) fail("ячейки строки раздела " + kind + " не td: " + JSON.stringify(cells));
    if (row.children.length !== h.children.length) {
      fail("в разделе " + kind + " ячеек строки " + row.children.length + ", а колонок шапки " +
        h.children.length + ": подписи встанут мимо");
    }
    const body = row.parentNode;
    if (!body || body.tagName !== "TBODY" || body.parentNode !== table) {
      fail("строка раздела " + kind + " лежит в другой таблице, чем её шапка");
    }
  }
}

// --- подсказка сортировки говорит по-русски ---
// «Поставить список по колонке» человек читать отказался: подсказка обязана
// называть действие и колонку так, как её называют вслух.
{
  const tips = [];
  for (const kind of ["tasks", "sess"]) {
    await go(kind === "tasks" ? "#demo" : "#demo/sess");
    for (const btn of allByClass(head(kind) || {}, "tblb")) tips.push(String(btn.title || ""));
  }
  if (!tips.length) fail("подписей колонок не нашлось вовсе");
  for (const said of tips) {
    if (said.includes("Поставить список")) {
      fail("подсказка колонки осталась прежней: " + said);
    }
    if (!/^(Сортировать по |Развернуть порядок)/.test(said)) {
      fail("подсказка колонки говорит не о сортировке: " + said);
    }
  }
  if (!tips.includes("Сортировать по рангу")) {
    fail("колонка ранга не назвала себя в подсказке: " + JSON.stringify(tips));
  }
  if (!tips.includes("Развернуть порядок")) {
    fail("у выбранной колонки нет подсказки про разворот: " + JSON.stringify(tips));
  }
  // Подпись для чтения с экрана та же, что и подсказка: слепому читателю
  // достаётся то же самое, а не имя класса.
  const btn = col("sess", "Работа");
  if (btn.attrs["aria-label"] !== btn.title) {
    fail("подпись колонки для чтения с экрана разошлась с подсказкой: " +
      btn.attrs["aria-label"] + " против " + btn.title);
  }
}

console.log("poc_tbl: ok");
