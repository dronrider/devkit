// Стенд раздела «Агенты» (ветка poc-chat): две дороги у каждой строки.
//
// Прежде строки расходились: у работы из реестра стоял переход на задачу без
// чата, у работы конвейера чат без перехода на задачу, а у сессии без задачи
// не было ни того ни другого (замечание пользователя). Предмет стенда в том,
// что номер задачи это ссылка на её форму, а разговор открывается и кнопкой, и
// нажатием на саму строку, и адрес его берётся от сессии, когда она известна.
//
// Зовётся: node testdata/poc_agents.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const { sandbox } = makeSandbox(app, () => ({}));

const now = Date.now();
const works = {
  // Конвейер задачи: сессию поднял дашборд, её и слушаем.
  tmux: { id: "DK-397", kind: "task", via: "tmux", sect: "in-progress",
    title: "дашборд ведёт разговор", session: "aaaa1111-1111", started: now / 1000 - 600 },
  // Работа поднята мимо дашборда: сессии он не видит, а задача известна.
  registry: { id: "DK-470", kind: "task", via: "registry", sect: "in-progress",
    title: "экран документа", started: now / 1000 - 3600 },
  // Сессия без задачи: остаётся один разговор.
  bare: { kind: "session", via: "session", session: "bbbb2222-2222",
    note: "задача не узнана", started: now / 1000 - 60 },
};

const rowOf = (w) => sandbox.agentRow("demo", w, now);
const btns = (row) => allByClass(row, "btn").map((b) => b.textContent);
// Кнопка чата тут значком, как в строке доски: подписи у неё нет, узнаётся она
// подсказкой.
const chatBtn = (row) => allByClass(row, "btn").find((b) => String(b.title) === "Чат агента");

// --- у работы с задачей обе дороги, чем бы она ни была поднята ---
for (const which of ["tmux", "registry"]) {
  const w = works[which];
  const row = rowOf(w);
  const link = byClass(row, "alink");
  if (!link || link.textContent !== w.id) {
    fail(which + ": номер задачи не ссылка: " + dump(row));
  }
  if (!chatBtn(row)) fail(which + ": входа в разговор нет: " + dump(row));
  if (!String(chatBtn(row).className).includes("btn-ico")) {
    fail(which + ": кнопка чата не значком: " + chatBtn(row).className);
  }
  // Ссылка ведёт на форму задачи и не утаскивает за собой чат строки.
  sandbox.location.hash = "#/agents";
  link.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (sandbox.location.hash.replace(/^#/, "") !== "demo/" + w.id) {
    fail(which + ": ссылка увела не на задачу: " + sandbox.location.hash);
  }
}

// --- стоп стоит только у своей tmux-работы ---
{
  if (!btns(rowOf(works.tmux)).includes("Остановить")) {
    fail("у своей работы пропал стоп: " + JSON.stringify(btns(rowOf(works.tmux))));
  }
  const reg = rowOf(works.registry);
  if (btns(reg).includes("Остановить")) fail("реестровой работе достался стоп");
  // Приписки в строке больше нет: она занимала полстроки и ломала ряд, а
  // знание уехало в подсказку строки, где лежат остальные метаданные
  // (замечание пользователя).
  if (dump(reg).includes("поднята мимо дашборда")) {
    fail("приписка вернулась в строку: " + dump(reg));
  }
  if (!String(reg.title).includes("поднята мимо дашборда")) {
    fail("подсказка строки не говорит, почему стопа нет: " + reg.title);
  }
}

// --- разговор открывается кнопкой и нажатием на строку, адрес от сессии ---
{
  const row = rowOf(works.tmux);
  sandbox.location.hash = "#/agents";
  chatBtn(row).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes(works.tmux.session)) {
    fail("кнопка открыла не разговор этой сессии: " + sandbox.location.hash);
  }
  sandbox.location.hash = "#/agents";
  row.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes(works.tmux.session)) {
    fail("нажатие на строку не открыло разговор: " + sandbox.location.hash);
  }
}

// --- у работы без сессии разговор адресуется задачей ---
{
  const row = rowOf(works.registry);
  sandbox.location.hash = "#/agents";
  row.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes("DK-470")) {
    fail("разговор реестровой работы открылся не по задаче: " + sandbox.location.hash);
  }
}

// --- сессия без задачи: только разговор, ссылки нет ---
{
  const row = rowOf(works.bare);
  if (byClass(row, "alink")) fail("у сессии без задачи взялась ссылка на задачу");
  if (!chatBtn(row)) fail("у сессии без задачи нет разговора");
  sandbox.location.hash = "#/agents";
  row.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes(works.bare.session)) {
    fail("разговор сессии без задачи открылся не по её ID: " + sandbox.location.hash);
  }
}

// --- раздел разложен на два таба, признак тот же, что у подсказки ---
{
  const projects = [{ name: "demo", prefix: "XR", works: [
    { id: "XR-1", kind: "task", via: "tmux", title: "конвейер задачи", session: "aaaa1111",
      own: true, model: "opus", sect: "in-progress" },
    { id: "XR-2", kind: "goal", via: "registry", title: "цикл цели", sect: "in-progress" },
    { kind: "session", via: "session", session: "bbbb2222", note: "окно человека" },
  ] }];
  const groups = sandbox.document.getElementById("groups");
  const tabs = () => allByClass(groups, "ktab");
  const rows = () => allByClass(groups, "arow");
  const openTab = () => (tabs().find((t) => String(t.className).includes("onktab")) || {}).textContent;

  sandbox.renderAgents(projects, "");
  const names = tabs().map((t) => t.textContent.replace(/\d+$/, ""));
  if (names.join("|") !== "Дашборд|Прочие") fail("табов раздела нет: " + JSON.stringify(names));
  if (!openTab().startsWith("Дашборд")) fail("открыт не тот таб: " + openTab());
  if (rows().length !== 1 || !dump(rows()[0]).includes("конвейер задачи")) {
    fail("в табе дашборда не своя работа: " + dump(groups).slice(0, 200));
  }

  tabs()[1].handlers.click({ stopPropagation: () => {} });
  const other = rows().map((r) => dump(r)).join(" ");
  if (rows().length !== 2 || !other.includes("цикл цели") || !other.includes("окно человека")) {
    fail("в табе прочих не те работы: " + other);
  }
  // Признак таба и признак подсказки один и тот же: разъехавшись, они сказали
  // бы про одну строку разное.
  for (const row of rows()) {
    if (!String(row.title).includes("поднята мимо дашборда")) {
      fail("чужая строка не объясняет себя подсказкой: " + row.title);
    }
  }

  // --- поиск фильтрует сессии раздела, а не задачи доски ---
  sandbox.renderAgents(projects, "цикл");
  if (rows().length !== 1 || !dump(rows()[0]).includes("цикл цели")) {
    fail("поиск не нашёл работу по заголовку: " + dump(groups).slice(0, 200));
  }
  sandbox.renderAgents(projects, "opus");
  if (rows().length) fail("работа чужого таба нашлась не в своём: " + dump(groups).slice(0, 200));
  tabs()[0].handlers.click({ stopPropagation: () => {} });
  if (rows().length !== 1 || !dump(rows()[0]).includes("конвейер задачи")) {
    fail("поиск по модели не нашёл свою работу: " + dump(groups).slice(0, 200));
  }
  for (const [q, what] of [["XR-1", "задаче"], ["demo", "проекту"], ["конвейер", "заголовку"]]) {
    sandbox.renderAgents(projects, q);
    if (!rows().length) fail("поиск по " + what + " (" + q + ") ничего не нашёл");
  }
  sandbox.renderAgents(projects, "такого нет");
  if (rows().length) fail("поиск нашёл лишнее по чепухе");
  if (!dump(groups).includes("ничего не нашлось")) {
    fail("пустая выдача раздела молчит: " + dump(groups).slice(0, 200));
  }
}

console.log("poc_agents: ok");
