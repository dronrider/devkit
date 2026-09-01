// Стенд единой шапки свёрнутых блоков ленты (сопутствующая правка к DK-693).
//
// Живой случай со снимка чата DK-656: блок «Фоновый агент: killed» держал
// шеврон вплотную к заголовку и без кнопки копирования, весть о завершении с
// длинной сводкой уводила шеврон за край экрана, а transcript-дисклеймер
// харнеса стоял вовсе без блока. Сборка шапок сведена в один сборщик, и стенд
// держит это правило: у всех свёрнутых блоков одна и та же шапка.
//
// Предмет стенда: подпись жирным, хвост с обрезкой растягивается, кнопки
// прижаты к правому краю и шеврон стоит последним. Снимается числом с
// настоящего style.css, а не глазом по снимку.
//
// Зовётся: node testdata/poc_feedhead.mjs static/app.js

import { makeSandbox, byClass, fail, dump, appPathArg }
  from "./poc_dom.mjs";
import { cssRules, layoutOf } from "./poc_css.mjs";

const app = appPathArg();
const rules = cssRules(app);

const { sandbox } = makeSandbox(app, () => ({}));

// Свойства шапки из настоящего style.css: хвост режется многоточием и не
// даёт кнопкам уехать, кнопки и подпись не сжимаются.
const headFit = (node) => layoutOf(node, {
  rules, width: 1440,
  want: ["flex", "min-width", "text-overflow", "white-space"],
});

const kids = (top) => Array.from(top.children || []);

// Разбор одной шапки: подпись, хвост, кнопки на концах. car это шеврон
// разворота, copy это кнопка копирования.
function checkHead(top, want) {
  const [bold, tail, copy, car] = kids(top);
  if (!bold || String(bold.tagName).toLowerCase() !== "b") {
    fail("шапка начинается не с подписи жирным: " + dump(top).slice(0, 200));
  }
  if (bold.textContent !== want.name) {
    fail("подпись шапки не отрезана по двоеточию: «" + bold.textContent +
      "» вместо «" + want.name + "»");
  }
  if (!tail || String(tail.tagName).toLowerCase() !== "span") {
    fail("хвоста шапки нет, кнопкам не к чему прижиматься: " + dump(top).slice(0, 200));
  }
  if (want.tail !== undefined && tail.textContent !== want.tail) {
    fail("хвост шапки не сводка после двоеточия: «" + tail.textContent + "»");
  }
  const fit = headFit(tail);
  if (fit.flex !== "1" || fit["min-width"] !== "0" ||
    fit["text-overflow"] !== "ellipsis" || fit["white-space"] !== "nowrap") {
    fail("хвост шапки не режется многоточием: " + JSON.stringify(fit));
  }
  if (want.tail && tail.title !== want.tail) {
    fail("у обрезанного хвоста нет подсказки с полным текстом: " + tail.title);
  }
  const last = kids(top)[kids(top).length - 1];
  if (String(last.className || "").split(" ").indexOf("foldc") < 0) {
    fail("шеврон не последний элемент шапки, он не прижат вправо: " +
      dump(top).slice(0, 250));
  }
  const carFit = headFit(last);
  if (!String(carFit.flex || "").replace("!important", "").trim().startsWith("none")) {
    fail("шеврон сжимается вместо того, чтобы стоять на месте: " + JSON.stringify(carFit));
  }
  if (want.copy) {
    if (!copy || String(copy.tagName).toLowerCase() !== "button" ||
      (copy.attrs || {})["aria-label"] !== "Копировать") {
      fail("кнопки копирования нет на месте перед шевроном: " + dump(top).slice(0, 250));
    }
  }
}

// --- весть о killed: короткая сводка после двоеточия ---
{
  const node = sandbox.chatItem({ role: "note", mark: "agent",
    note: "Фоновый агент: killed",
    text: "Background command \"grep -rn замена ~/проект\" was stopped" });
  const top = byClass(node, "foldh");
  if (!top) fail("весть о killed не собралась блоком с шапкой: " + dump(node).slice(0, 200));
  checkHead(top, { name: "Фоновый агент", tail: "killed", copy: true });
}

// --- весть о завершении: длинная сводка режется, а не уводит кнопки ---
{
  const sum = "Agent \"Правка сценария DK-656 по замечанию ревью\" finished";
  const node = sandbox.chatItem({ role: "note", mark: "agent",
    note: "Фоновый агент завершил работу: " + sum, text: "Отчёт субагента целиком" });
  const top = byClass(node, "foldh");
  if (!top) fail("весть о завершении не собралась блоком с шапкой: " + dump(node).slice(0, 200));
  checkHead(top, { name: "Фоновый агент завершил работу", tail: sum, copy: true });
}

// --- размышления и пересказ сжатия: та же шапка, что у вестей ---
{
  const node = sandbox.chatItem({ role: "thinking", spent: 45000,
    text: "Первая строка размышлений, за ней ещё много" });
  checkHead(byClass(node, "foldh"), { name: "Размышлял 45 с",
    tail: "Первая строка размышлений, за ней ещё много", copy: false });

  const node2 = sandbox.chatItem({ role: "note", mark: "compact",
    note: "начало разговора сжато в пересказ", text: "Пересказ съеденного начала" });
  checkHead(byClass(node2, "foldh"), { name: "начало разговора сжато в пересказ",
    tail: "", copy: true });
}

console.log("poc_feedhead: ок, у свёрнутых блоков одна шапка: подпись, хвост " +
  "с обрезкой, копирование и шеврон прижаты справа");
