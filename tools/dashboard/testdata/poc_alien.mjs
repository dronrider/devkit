// Стенд чужой работы (ветка poc-chat): строка задачи, которую ведут на другой
// машине.
//
// Прежнее правило прятало у такой строки всё разом, и вместе с запуском
// конвейера пропадал вход в разговор: обсудить чужую задачу с дашборда было
// негде (замечание пользователя). Предмет стенда в том, что скрыт остался
// только конвейер: подпись про чужую машину на месте, кнопки запуска и
// продолжения нет, а кнопка чата есть и ведёт в панель этой задачи.
//
// Зовётся: node testdata/poc_alien.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const { sandbox } = makeSandbox(app, () => ({}));

const alien = { id: "DK-460", title: "релогин не будит живые сессии",
  sect: "in-progress", run: "other" };
const ours = { id: "DK-397", title: "дашборд агентской разработки",
  sect: "in-progress", run: "tmux" };

// Кнопка чата в строке: подписи у неё нет вовсе, значок и подсказка.
const chatBtn = (row) => allByClass(row, "btn").find((b) => String(b.title) === "Чат по задаче");

// --- у чужой работы остались подпись и чат, конвейера нет ---
{
  const row = sandbox.renderRow("demo", alien, "in-progress");
  const said = dump(row);
  if (!said.includes("в работе на другой машине")) {
    fail("подпись про чужую машину пропала: " + said);
  }
  if (!chatBtn(row)) fail("кнопки чата у чужой работы нет: " + said);
  const btns = allByClass(row, "btn").map((b) => b.textContent);
  for (const gone of ["Выполнить", "Продолжить", "Проверить", "Стоп"]) {
    if (btns.includes(gone)) fail("у чужой работы осталась кнопка конвейера: " + gone);
  }
}

// --- чат ведёт в панель именно этой задачи ---
{
  const row = sandbox.renderRow("demo", alien, "in-progress");
  chatBtn(row).handlers.click({ stopPropagation: () => {} });
  await settle();
  // Экрана проекта под панелью в стенде нет, и адрес собирается с проектом
  // внутри, как это делает раздел «Агенты».
  if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes("DK-460")) {
    fail("чат чужой задачи открыл не её разговор: " + sandbox.location.hash);
  }
}

// --- чат стоит у каждой строки доски, чем бы она ни была занята ---
// Правило объясняется одной фразой, «чат есть у каждой задачи», и остаточных
// условий, прячущих кнопку, быть не должно (замечание пользователя).
{
  const cases = [
    ["очередь", { id: "DK-1", title: "новая", sect: "backlog" }, "backlog"],
    ["очередь с маркером", { id: "DK-2", title: "ждёт", sect: "backlog", after: ["DK-1"] }, "backlog"],
    ["проверка", { id: "DK-3", title: "на проверке", sect: "check", accept: "mixed" }, "check"],
    ["блок", { id: "DK-4", title: "стоит", sect: "blocked", block: "ждём ключ" }, "blocked"],
    ["своя живая", ours, "in-progress"],
    ["чужая машина", alien, "in-progress"],
  ];
  for (const [what, row, sect] of cases) {
    const node = sandbox.renderRow("demo", row, sect);
    if (!chatBtn(node)) fail("кнопки чата нет у строки (" + what + "): " + dump(node));
  }
}

// --- своя живая работа осталась прежней: стоп на месте ---
{
  const row = sandbox.renderRow("demo", ours, "in-progress");
  const btns = allByClass(row, "btn").map((b) => b.textContent);
  if (!btns.includes("Стоп")) {
    fail("у своей живой работы пропал стоп: " + JSON.stringify(btns));
  }
  if (byClass(row, "stale")) fail("своя работа подписалась чужой машиной");
}

// --- ожидание в строке одно: кружок со словами в подсказке ---
{
  const waiting = { id: "DK-5", title: "спросил агент", sect: "in-progress", run: "session",
    waiting: { state: "сессия ждёт ввода", note: "спросил агент", questions: ["какой из двух?"] } };
  const row = sandbox.renderRow("demo", waiting, "in-progress");
  if (dump(row).includes("сессия ждёт ввода")) {
    fail("текстовая метка ожидания осталась в строке: " + dump(row));
  }
  const dot = byClass(row, "sdot");
  if (!dot || !String(dot.title).includes("сессия ждёт ввода")) {
    fail("состояние ожидания пропало из подсказки кружка: " + (dot && dot.title));
  }
}

console.log("poc_alien: ok");
