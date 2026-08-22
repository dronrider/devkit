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

// --- у работы с задачей обе дороги, чем бы она ни была поднята ---
for (const which of ["tmux", "registry"]) {
  const w = works[which];
  const row = rowOf(w);
  const link = byClass(row, "alink");
  if (!link || link.textContent !== w.id) {
    fail(which + ": номер задачи не ссылка: " + dump(row));
  }
  if (!btns(row).includes("Чат")) {
    fail(which + ": входа в разговор нет: " + JSON.stringify(btns(row)));
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
  if (!dump(reg).includes("поднята мимо дашборда")) {
    fail("реестровая работа не сказала, почему стопа нет: " + dump(reg));
  }
}

// --- разговор открывается кнопкой и нажатием на строку, адрес от сессии ---
{
  const row = rowOf(works.tmux);
  sandbox.location.hash = "#/agents";
  allByClass(row, "btn").find((b) => b.textContent === "Чат")
    .handlers.click({ stopPropagation: () => {} });
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
  if (!btns(row).includes("Чат")) fail("у сессии без задачи нет разговора");
  sandbox.location.hash = "#/agents";
  row.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!sandbox.location.hash.includes(works.bare.session)) {
    fail("разговор сессии без задачи открылся не по её ID: " + sandbox.location.hash);
  }
}

console.log("poc_agents: ok");
