// Стенд строки без узнанного исполнителя (ветка poc-chat): задача стоит в
// работе, а живой работы за ней на этой машине не видно.
//
// Стенд начинался с чужой машины: строке с признаком other дашборд прятал
// конвейер и подписывал её словами «исполнителя не видно». Признака этого
// больше нет, и приписки тоже: попадали они ровно в те задачи, которые человек
// вёл из дашборда, а «взяли в другом месте» по этой машине не проверяется
// вовсе. Предмет стенда теперь обратный: снятого нет, конвейер строке возвращён,
// а кнопка чата стоит у неё, как и у всякой другой строки доски.
//
// Зовётся: node testdata/poc_alien.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const { sandbox } = makeSandbox(app, () => ({}));

// Наших сессий у строки нет ни одной: признак работы приезжает пустым.
const nolead = { id: "DK-460", title: "релогин не будит живые сессии",
  sect: "in-progress" };
const ours = { id: "DK-397", title: "дашборд агентской разработки",
  sect: "in-progress", run: "tmux" };

// Кнопка чата в строке: подписи у неё нет вовсе, значок и подсказка. У строки
// с разговором она стоит главной кнопкой, у нетронутой очереди лежит пунктом
// меню под тремя точками, и ищется в обоих местах разом.
const chatBtn = (row) => {
  const dots = byClass(row, "rdots");
  if (dots) dots.handlers.click({ stopPropagation: () => {} });
  return allByClass(row, "btn").find((b) => String(b.title) === "Чат по задаче")
    || allByClass(row, "pmrow").find((b) => b.textContent === "Чат по задаче")
    || null;
};

// --- приписки про чужую машину нет, конвейер и чат на месте ---
{
  const row = sandbox.renderRow("demo", nolead, "in-progress");
  const said = dump(row);
  for (const gone of ["исполнителя не видно", "ведёт другая сессия", "другой машине"]) {
    if (said.includes(gone)) fail("снятая подпись про чужую машину вернулась: " + said);
  }
  if (byClass(row, "stale")) fail("строка подписалась чужой машиной: " + said);
  if (!chatBtn(row)) fail("кнопки чата у строки нет: " + said);
  // Второму исполнителю тут взяться неоткуда: живой работы за строкой нет, и
  // конвейер ей возвращён.
  const run = byClass(row, "rmain");
  if (!run || run.attrs["aria-label"] !== "Продолжить") {
    fail("у строки без узнанного исполнителя пропал конвейер: " + dump(row));
  }
  if (byClass(row, "rstop")) fail("строке без живой работы предложен стоп: " + dump(row));
}

// --- чат ведёт в панель именно этой задачи ---
{
  const row = sandbox.renderRow("demo", nolead, "in-progress");
  chatBtn(row).handlers.click({ stopPropagation: () => {} });
  await settle();
  // Экрана проекта под панелью в стенде нет, и адрес собирается с проектом
  // внутри, как это делает раздел «Агенты».
  if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes("DK-460")) {
    fail("чат задачи открыл не её разговор: " + sandbox.location.hash);
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
    ["без узнанного исполнителя", nolead, "in-progress"],
  ];
  for (const [what, row, sect] of cases) {
    const node = sandbox.renderRow("demo", row, sect);
    if (!chatBtn(node)) fail("кнопки чата нет у строки (" + what + "): " + dump(node));
  }
}

// --- своя живая работа осталась прежней: стоп на месте ---
{
  const row = sandbox.renderRow("demo", ours, "in-progress");
  if (!byClass(row, "rstop")) {
    fail("у своей живой работы пропал стоп: " + dump(row));
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
