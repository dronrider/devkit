// Стенд вертикального шага ленты (сопутствующая правка к DK-702).
//
// Живой случай со снимка чата DK-656: две капсулы «Фоновый агент» стояли
// друг к другу просторнее, чем к соседним репликам. Сборка кладёт всякую
// запись ленты в обёртку строки с кружком нити (feedRow), и зазор между
// соседями набирается полями самих записей, а они у видов разные: реплика
// несла шесть сверху, капсула служебки по три с обеих сторон, карточки
// инструмента по два. Стенд держит правило: шаг несёт строка, полем сверху
// одним токеном, запись внутри строки полей по вертикали не несёт, а нить
// идёт сквозь шаг парой --mt и не рвётся.
//
// Предмет стенда: поля решаются из настоящего style.css по цепочке предков,
// а не глазом по снимку. Записи собираются настоящими сборщиками чата и
// кладутся в список настоящей обёрткой строки, как это делает лента:
// запись-сирота без обёртки жила бы по другим правилам, и первый раунд
// правки прошёл стенд и не применился на экране. Колонка локальных записей
// (разговор входа, местный пузырь отправки) меряется той же мерой: записи
// входа обязаны не отличаться от записей ленты (DK-577).
//
// Зовётся: node testdata/poc_feedstep.mjs static/app.js

import { makeSandbox, fail, dump, appPathArg }
  from "./poc_dom.mjs";
import { cssRules, layoutOf } from "./poc_css.mjs";

const app = appPathArg();
const rules = cssRules(app);

const { sandbox } = makeSandbox(app, () => ({}));

// Строка ленты берётся тем же сборщиком, что у живой ленты в wireFeed.
const rowOf = (item) => sandbox.feedRow(sandbox.chatItem(item), item, null, "");

// Список того же состава, что в живой ленте: реплики, капсулы фоновой
// работы, размышления, ход инструментом, вывод без вызова, служебная строка,
// оголовок дня и реплика без текста.
const list = sandbox.el("div", "mlist");
list.append(
  rowOf({ role: "user", text: "Реплика человека" }),
  rowOf({ role: "note", mark: "agent",
    note: "Фоновый агент: killed", text: "Весть о фоновой работе" }),
  rowOf({ role: "note", mark: "agent",
    note: "Фоновый агент завершил работу: сводка", text: "Отчёт целиком" }),
  rowOf({ role: "note", mark: "compact",
    note: "начало разговора сжато в пересказ", text: "Пересказ" }),
  rowOf({ role: "thinking", spent: 45000, text: "Размышления" }),
  rowOf({ role: "thinking", spent: 1000 }),
  rowOf({ role: "tool", tool: "Read", args: {} }),
  rowOf({ role: "toolout", text: "вывод без своего вызова" }),
  rowOf({ role: "note", text: "служебная строка" }),
  sandbox.dayEl("сегодня"),
  rowOf({ role: "assistant" }),
  rowOf({ role: "assistant", text: "Ответ агента" }),
);

// Колонка локальных записей: тем же сборщиком, что и разговор входа.
const local = sandbox.el("div", "mlocal");
local.append(rowOf({ role: "assistant", text: "Запись разговора входа" }));

// Поля узла из настоящего style.css. Сокращение раскладывается по сторонам,
// точечные margin-top и margin-bottom перекрывают свою сторону, как в
// браузере при каскаде одного узла.
function sidesOf(node) {
  const got = layoutOf(node, {
    rules, width: 1440,
    want: ["margin", "margin-top", "margin-bottom"],
  });
  const sides = { top: "", bottom: "" };
  if (got.margin) {
    const parts = got.margin.split(/\s+/);
    sides.top = parts[0];
    sides.bottom = parts.length > 2 ? parts[2] : parts[parts.length - 1];
  }
  if (got["margin-top"]) sides.top = got["margin-top"];
  if (got["margin-bottom"]) sides.bottom = got["margin-bottom"];
  return sides;
}

const px = (v) => Number(String(v || "").replace(/px$/, ""));

// Кружок сидит на первой строке записи двумя согласными числами: --dotc это
// его середина для нити, top это его край у самого кружка. Расхождение
// означает, что одно сдвинули, а другое забыли.
function dotCheck(row, num) {
  const dot = (row.children || []).find((n) =>
    String(n.className || "").split(" ").includes("fdot"));
  if (!dot) fail("у строки " + num + " нет кружка нити");
  const mid = layoutOf(row, { rules, width: 1440, want: ["--dotc"] })["--dotc"];
  const top = layoutOf(dot, { rules, width: 1440, want: ["top"] }).top;
  if (px(mid) !== px(top) + 5.5) {
    fail("кружок разошёлся с нитью у " + row.className + " " + num +
      ": --dotc " + mid + ", top " + top);
  }
}

const kids = Array.from(list.children || []);
if (kids.length < 10) {
  fail("записи ленты не собрались: " + dump(list).slice(0, 300));
}
let rows = 0;
kids.forEach((kid, at) => {
  const cls = String(kid.className || "").split(" ");
  const num = "№" + (at + 1);
  if (!cls.includes("frow")) {
    // Оголитель дней не заворачивается в строку и несёт своё поле сверху.
    const sides = sidesOf(kid);
    if (sides.top !== "var(--feedgap)") {
      fail("поле сверху не шагом ленты у " + kid.className + " " + num +
        ": " + JSON.stringify(sides));
    }
    return;
  }
  rows++;
  const sides = sidesOf(kid);
  if (sides.top !== "var(--feedgap)" || sides.bottom !== "0") {
    fail("шаг не у строки " + num + ": " + JSON.stringify(sides) +
      ", строка должна нести поле сверху одним токеном и ничего снизу");
  }
  const mt = layoutOf(kid, { rules, width: 1440, want: ["--mt"] })["--mt"];
  if (mt !== "var(--feedgap)") {
    fail("нить не проведена сквозь шаг у строки " + num + ": --mt " + mt);
  }
  dotCheck(kid, num);
  const body = (kid.children || []).find((n) =>
    String(n.className || "").split(" ").includes("frowb"));
  for (const rec of (body && body.children) || []) {
    const inner = sidesOf(rec);
    if (inner.top !== "0" || inner.bottom !== "0") {
      fail("запись в строке несёт свои поля у " + rec.className + " " + num +
        ": " + JSON.stringify(inner) +
        ", поля видов складываются с полем строки и разводят соседей");
    }
  }
});
if (rows < 10) {
  fail("строк ленты собралось меньше видов записей: " + rows);
}

for (const kid of Array.from(local.children || [])) {
  const sides = sidesOf(kid);
  if (sides.top !== "var(--feedgap)" || sides.bottom !== "0") {
    fail("шаг не у локальной строки: " + JSON.stringify(sides));
  }
  const body = (kid.children || []).find((n) =>
    String(n.className || "").split(" ").includes("frowb"));
  for (const rec of (body && body.children) || []) {
    const inner = sidesOf(rec);
    if (inner.top !== "0" || inner.bottom !== "0") {
      fail("локальная запись несёт свои поля у " + rec.className + ": " +
        JSON.stringify(inner) + ", а обязана смотреться записью ленты");
    }
  }
}

console.log("poc_feedstep: ок, у всех " + rows + " строк поле сверху одним " +
  "токеном, нить сквозь шаг проведена, записи внутри без своих полей: " +
  "шаг ленты ровный");
