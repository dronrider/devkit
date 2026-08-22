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

// --- у чужой работы остались подпись и чат, конвейера нет ---
{
  const act = sandbox.rowAction("demo", alien, "in-progress");
  const said = dump(act);
  if (!said.includes("в работе на другой машине")) {
    fail("подпись про чужую машину пропала: " + said);
  }
  const btns = allByClass(act, "btn").map((b) => b.textContent);
  if (!btns.includes("Чат")) fail("кнопки чата у чужой работы нет: " + JSON.stringify(btns));
  for (const gone of ["Выполнить", "Продолжить", "Проверить", "Стоп"]) {
    if (btns.includes(gone)) fail("у чужой работы осталась кнопка конвейера: " + gone);
  }
}

// --- чат ведёт в панель именно этой задачи ---
{
  const act = sandbox.rowAction("demo", alien, "in-progress");
  const talk = allByClass(act, "btn").find((b) => b.textContent === "Чат");
  talk.handlers.click({ stopPropagation: () => {} });
  await settle();
  // Экрана проекта под панелью в стенде нет, и адрес собирается с проектом
  // внутри, как это делает раздел «Агенты».
  if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes("DK-460")) {
    fail("чат чужой задачи открыл не её разговор: " + sandbox.location.hash);
  }
}

// --- своя живая работа осталась прежней: чат и стоп ---
{
  const act = sandbox.rowAction("demo", ours, "in-progress");
  const btns = allByClass(act, "btn").map((b) => b.textContent);
  if (!btns.includes("Стоп")) {
    fail("у своей живой работы пропал стоп: " + JSON.stringify(btns));
  }
  if (byClass(act, "stale")) fail("своя работа подписалась чужой машиной");
}

console.log("poc_alien: ok");
