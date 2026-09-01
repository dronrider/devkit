// Стенд вертикального шага ленты (сопутствующая правка к DK-702).
//
// Живой случай со снимка чата DK-656: две капсулы «Фоновый агент» стояли
// друг к другу вдвое просторнее, чем к соседним репликам. Колонка ленты
// флексовая, поля соседей в ней складываются, и блоки с полями с обеих
// сторон удваивали зазор между собой. Стенд держит правило: у всякой записи
// ленты поле только сверху и одним токеном, снизу поля нет.
//
// Предмет стенда: поля решаются из настоящего style.css по цепочке предков,
// а не глазом по снимку. Записи собираются настоящими сборщиками чата.
//
// Зовётся: node testdata/poc_feedstep.mjs static/app.js

import { makeSandbox, fail, dump, appPathArg }
  from "./poc_dom.mjs";
import { cssRules, layoutOf } from "./poc_css.mjs";

const app = appPathArg();
const rules = cssRules(app);

const { sandbox } = makeSandbox(app, () => ({}));

// Список того же состава, что в живой ленте: реплики, капсулы фоновой
// работы, размышления, ход инструментом, вывод без вызова, служебная строка,
// оголовок дня и реплика без текста.
const list = sandbox.el("div", "mlist");
list.append(
  sandbox.chatItem({ role: "user", text: "Реплика человека" }),
  sandbox.chatItem({ role: "note", mark: "agent",
    note: "Фоновый агент: killed", text: "Весть о фоновой работе" }),
  sandbox.chatItem({ role: "note", mark: "agent",
    note: "Фоновый агент завершил работу: сводка", text: "Отчёт целиком" }),
  sandbox.chatItem({ role: "note", mark: "compact",
    note: "начало разговора сжато в пересказ", text: "Пересказ" }),
  sandbox.chatItem({ role: "thinking", spent: 45000, text: "Размышления" }),
  sandbox.chatItem({ role: "thinking", spent: 1000 }),
  sandbox.chatItem({ role: "tool", tool: "Read", args: {} }),
  sandbox.chatItem({ role: "toolout", text: "вывод без своего вызова" }),
  sandbox.chatItem({ role: "note", text: "служебная строка" }),
  sandbox.dayEl("сегодня"),
  sandbox.chatItem({ role: "assistant" }),
  sandbox.chatItem({ role: "assistant", text: "Ответ агента" }),
);

// Поля записи из настоящего style.css. Сокращение раскладывается по сторонам,
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

const kids = Array.from(list.children || []);
if (kids.length < 10) {
  fail("записи ленты не собрались: " + dump(list).slice(0, 300));
}
kids.forEach((kid, at) => {
  const sides = sidesOf(kid);
  const kind = (kid.className || "?") + " №" + (at + 1);
  if (sides.top !== "var(--feedgap)") {
    fail("поле сверху не единым токеном у " + kind + ": " +
      JSON.stringify(sides) + ", зазор между соседями плывёт");
  }
  if (sides.bottom !== "0") {
    fail("поле снизу не нулевое у " + kind + ": " +
      JSON.stringify(sides) + ", соседние поля складываются");
  }
});

console.log("poc_feedstep: ок, у всех " + kids.length + " видов записей поле " +
  "сверху одним токеном, снизу поля нет: шаг ленты ровный");
