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
  if (path.endsWith("/drafts")) return { drafts: [] };
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

const head = (kind) => allByClass(groups, "thead")
  .find((h) => String(h.className).split(" ").includes("h-" + kind)) || null;
const col = (kind, label) => allByClass(head(kind) || {}, "thb")
  .find((btn) => dump(btn).includes(label)) || null;
const click = async (btn) => {
  btn.handlers.click({ stopPropagation: () => {} });
  await settle();
};
const on = (btn) => Boolean(btn) && String(btn.className).split(" ").includes("thon");

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
  if (allByClass(groups, "thead").length !== 1) {
    fail("шапок у доски больше одной: " + allByClass(groups, "thead").length);
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
  for (const label of ["Состояние", "Работа", "Идёт"]) {
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
  if (JSON.stringify(kids) !== JSON.stringify(["dot", "ab", "atime", "aacts"])) {
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

console.log("poc_tbl: ok");
