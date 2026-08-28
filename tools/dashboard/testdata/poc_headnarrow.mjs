// Стенд вёрстки шапки узкого экрана (замечание пользователя: один ряд шапки
// упирался примерно в 470 точек, и на обычном телефоне в 390-430 страница
// уезжала в горизонтальную прокрутку). На узком экране шапка ложится в две
// строки: имя с выбором проекта в первой, подпись экрана, легенда и
// кнопки-действия во второй. Правила читаются из настоящего style.css, а
// запас ширины первой строки сверяется с размерами кнопок и селекта из тех же
// стилей: флекс с переносом не ужимает элементы, а уносит селект на свою
// строку, стоит базовым ширинам перерасти строку.
//
// Зовётся: node testdata/poc_headnarrow.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { fail, appPathArg } from "./poc_dom.mjs";

const cssPath = path.join(path.dirname(appPathArg()), "style.css");
const css = fs.readFileSync(cssPath, "utf8");

// Тело медиазапроса счётом скобок, как в screen_keep.mjs: границы блока видит
// так же, как их видит разбор браузера.
function mediaBlock(text, head) {
  const at = text.indexOf(head);
  if (at < 0) return "";
  const from = text.indexOf("{", at);
  if (from < 0) return "";
  let depth = 0;
  for (let i = from; i < text.length; i += 1) {
    if (text[i] === "{") depth += 1;
    if (text[i] === "}") {
      depth -= 1;
      if (!depth) return text.slice(from + 1, i);
    }
  }
  return "";
}

const narrow = mediaBlock(css, "@media (max-width:520px)");
if (!narrow) fail("медиазапроса узкой шапки (max-width:520px) в style.css нет");

// Шапка переносится, и разрыв строки принудительный: ::after во всю ширину
// встаёт флекс-элементом между селектом и кнопками.
if (!/\.bhead\{[^}]*flex-wrap:wrap/.test(narrow)) {
  fail("узкая шапка не переносится: у .bhead нет flex-wrap:wrap");
}
if (!/\.bhead::after\{[^}]*width:100%/.test(narrow)) {
  fail("принудительного разрыва строки (.bhead::after во всю ширину) нет");
}

// Кнопки и легенда уезжают за разрыв, а селект остаётся в первой строке:
// порядок ему не назначается. Подписи экрана в шапке больше нет вовсе, она
// пересказывала подсвеченный таб (решение пользователя).
if (!/\.bhead \.bell[^{]*\{[^}]*order:/.test(narrow)) {
  fail("кнопки-действия не уехали во вторую строку: у .bhead .bell нет order");
}
if (!/#hlegend\{order:|#hlegend[^{]*\{[^}]*order:/.test(narrow)) {
  fail("легенда не уехала во вторую строку");
}
if (/\.bhead \.sub/.test(css)) {
  fail("подпись экрана вернулась в шапку: её место занял подсвеченный таб");
}
if (/\.pselect[^{]*\{[^}]*order:/.test(narrow)) {
  fail("селект проекта уехал из первой строки: у .pselect стоит order");
}

// Имя и подпись заперты шириной с многоточием, а не толкают строку в перенос.
const h2cap = /\.bhead h2\{[^}]*max-width:calc\(100% - (\d+)px\)[^}]*text-overflow:ellipsis/.exec(narrow);
if (!h2cap) fail("имя проекта не заперто шириной с многоточием (.bhead h2)");

// Приписка строки главной режется многоточием, а не распирает список до
// горизонтальной прокрутки.
if (!/\.prow \.stale\{[^}]*text-overflow:ellipsis/.test(narrow)) {
  fail("приписка строки главной не режется многоточием (.prow .stale)");
}

// Запас ширины сверяется с настоящими размерами из тех же стилей: контрол
// (--ctl), зазор шапки, поля .bmain телефона и доля селекта.
const ctl = Number((/--ctl:(\d+)px/.exec(css) || [])[1] || 0);
if (!ctl) fail("размер контролов --ctl из style.css не прочитался");
const gaps = [...css.matchAll(/\.bhead\{[^}]*gap:(\d+)px/g)];
const gap = Number(gaps.length ? gaps[gaps.length - 1][1] : 0);
if (!gap) fail("зазор .bhead из style.css не прочитался");
const phone = mediaBlock(css, "@media (max-width:900px)");
const pad = Number((/\.bmain\{padding:(\d+)px/.exec(phone) || [])[1] || 0);
if (!pad) fail("поля .bmain телефона из style.css не прочитались");
const share = Number((/\.pselect\{[^}]*max-width:(\d+)%/.exec(css) || [])[1] || 0);
if (!share) fail("доля ширины селекта из style.css не прочиталась");

// Первая строка на 390 точках: логотип, зазоры и селект обязаны влезать в то,
// что оставлено имени за вычетом запаса.
const inner = 390 - 2 * pad;
const rest = ctl + 2 * gap + Math.ceil((share / 100) * inner);
if (rest > Number(h2cap[1])) {
  fail("запас имени меньше соседей первой строки: " + rest + " > " + h2cap[1]);
}
// Вторая строка: четыре кнопки с зазорами влезают в ширину экрана. Прежде
// мерой тут был запас подписи, но подписи в шапке больше нет, и мерить надо
// тем, что осталось, самой строкой.
const btns = 4 * ctl + 4 * gap;
if (btns > inner) {
  fail("полоса кнопок не влезает во вторую строку: " + btns + " > " + inner);
}

console.log("ok: узкая шапка в две строки с принудительным разрывом, селект в " +
  "первой, кнопки во второй, ширины заперты с запасом под 390 точек");
