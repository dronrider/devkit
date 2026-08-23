// Стенд свёртки служебных разделов файла задачи на форме (замечание
// пользователя: у DK-397 журнал с итогом и вклейками съедали почти половину
// из 655 строк). Просмотр раскладывает файл по разделам: постановка стоит
// развёрнутой, служебные разделы (Журнал, Ход работы, Итог, Входящие, вклейки
// «Из черновика...») сворачиваются строкой с именем и объёмом и
// разворачиваются кликом, незнакомый раздел по умолчанию развёрнут, а режим
// правки видит файл целиком.
//
// Зовётся: node testdata/poc_taskfold.mjs static/app.js

import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const { sandbox } = makeSandbox(app, () => ({}));

const text = [
  "# XR-1: пробная задача",
  "",
  "## Цель",
  "видимая цель постановки",
  "",
  "## Журнал",
  "- запись журнала раз",
  "- запись журнала два",
  "",
  "## Неведомый раздел",
  "неизвестное содержимое видно",
  "",
  "## Из черновика DK-1 (записан вчера)",
  "вклейка черновика скрыта",
].join("\n");

const form = { text };
const card = sandbox.filePanel("demo", "XR-1", { file: "docs/tasks/XR-1.md" },
  form, () => {}, false, false);
await settle();

const folds = [];
(function walk(node) {
  if (String(node.className || "").split(" ").includes("ffold")) folds.push(node);
  for (const kid of node.children || []) {
    if (typeof kid === "object") walk(kid);
  }
})(card);

// Видимое: в песочнице hidden не прячет узлы из dump, поэтому свёрнутость
// меряется лучше, чем стилями: тело свёрнутого раздела не отрисовано вовсе,
// его рендерит первый разворот.
const shown = dump(card).replace(/\s+/g, " ");
if (!shown.includes("видимая цель постановки")) {
  fail("постановка свёрнута, а обязана стоять развёрнутой: " + shown.slice(0, 300));
}
if (!shown.includes("неизвестное содержимое видно")) {
  fail("незнакомый раздел спрятан молча: " + shown.slice(0, 300));
}
if (shown.includes("запись журнала раз")) fail("журнал развёрнут по умолчанию");
if (shown.includes("вклейка черновика скрыта")) fail("вклейка черновика развёрнута по умолчанию");

// Свёрнутые строки называют раздел и объём: у журнала четыре строки куска,
// заголовок, две записи и пустая перед следующим разделом.
if (!shown.includes("Журнал") || !shown.includes("4 строки")) {
  fail("строка журнала без имени или объёма: " + shown.slice(0, 400));
}
if (folds.length !== 2) fail("свёрнутых разделов " + folds.length + ", ждал журнал и вклейку");

// Разворот по клику: тело журнала рендерится и становится видимым.
const logFold = folds.find((f) => dump(f).includes("Журнал"));
if (!logFold) fail("аккордеона журнала нет");
const top = logFold.children[0];
top.handlers.click({});
await settle();
const opened = dump(logFold).replace(/\s+/g, " ");
if (!opened.includes("запись журнала раз") || !opened.includes("запись журнала два")) {
  fail("разворот не показал журнал: " + opened.slice(0, 300));
}
const bodyEl = logFold.children[1];
if (bodyEl.hidden) fail("тело журнала осталось спрятанным после клика");
// Повторный клик сворачивает обратно.
top.handlers.click({});
if (!bodyEl.hidden) fail("повторный клик не свернул журнал");

// Режим правки видит файл целиком: сворачивает только просмотр.
card.setEdit(true);
const ta = (function find(node) {
  if (node.tagName === "TEXTAREA") return node;
  for (const kid of node.children || []) {
    const hit = typeof kid === "object" && find(kid);
    if (hit) return hit;
  }
  return null;
})(card);
if (!ta || ta.hidden) fail("поле правки не показалось");
if (!ta.value.includes("запись журнала раз") || !ta.value.includes("вклейка черновика скрыта")) {
  fail("правка видит не весь файл: " + ta.value.slice(0, 200));
}

console.log("ok: служебные разделы файла свёрнуты строками с объёмом, постановка и " +
  "незнакомое развёрнуты, разворот кликом, правка видит файл целиком");
