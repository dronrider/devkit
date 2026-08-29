// Стенд нажимаемости вариантов в блоке вопроса клиента (ветка poc-chat).
//
// Живой случай со снимка: клиент встал на вопросе про изучение окружения,
// дашборд его поймал и показал варианты «Yes», «Not now», «Don't show again»,
// а человек спросил, что это за странность. Механика работала, нажатие
// отправляло ответ, но выглядели варианты простым текстом: рамки нет, заливки
// нет, признака нажимаемости нет вовсе. Человек шёл отвечать в терминал, ради
// чего блок и заводился.
//
// Предмет стенда: вариант виден кнопкой до всякого наведения (рамка и заливка,
// как у кнопок панели), отвечает на наведение и на нажатие, а на узком экране
// берётся пальцем. Меряется числами настоящего style.css, а сам узел берётся
// из собранного блока: вопрос тут в виде, а механику держит poc_clientask.mjs.
//
// Зовётся: node testdata/poc_caskopt.mjs static/app.js

import { fail, appPathArg } from "./poc_dom.mjs";
import { cssRules } from "./poc_css.mjs";

const app = appPathArg();
const rules = cssRules(app);

const declOf = (text) => {
  const out = {};
  for (const part of String(text).split(";")) {
    const [k, v] = part.split(":");
    if (!k || v === undefined) continue;
    out[k.trim()] = v.trim();
  }
  return out;
};

// Правила по точному селектору, в порядке файла: у блока вопроса селекторы
// простые, и общий разбор каскада тут не нужен.
const ruleOf = (sel, media) => {
  const out = {};
  for (const r of rules) {
    if ((r.media || "") !== (media || "")) continue;
    for (const one of r.sel.split(",")) {
      if (one.trim() !== sel) continue;
      Object.assign(out, declOf(r.decl));
    }
  }
  return out;
};

const narrow = rules.find((r) => /max-width:\s*900px/.test(r.media || ""));
if (!narrow) fail("узкого экрана в стилях нет вовсе: мерить палец не на чем");

// --- вариант виден кнопкой до наведения ---

const rest = ruleOf(".caskopt");
if (!Object.keys(rest).length) fail("правила .caskopt в стилях нет вовсе");

const bord = String(rest.border || "");
if (!bord || /transparent/.test(bord) || /^0/.test(bord) || bord === "none") {
  fail("вариант стоит без видимой рамки: «" + bord + "»");
}
const fill = String(rest.background || "");
if (!fill || fill === "none" || fill === "transparent") {
  fail("вариант стоит без заливки: «" + fill + "»");
}
if (!/var\(--/.test(bord) || !/var\(--/.test(fill)) {
  fail("вариант красится мимо палитры панели: рамка «" + bord + "», заливка «" + fill + "»");
}
if (rest.cursor !== "pointer") fail("у варианта нет курсора нажимаемого: " + rest.cursor);

// --- отклик на наведение и на нажатие ---

const hover = ruleOf(".caskopt:hover");
if (!Object.keys(hover).length) fail("вариант не отвечает на наведение");
if (String(hover["border-color"] || "") === bord.split(" ").pop() &&
  String(hover.background || "") === fill) {
  fail("наведение ничего не меняет: вариант и так такой");
}
const press = ruleOf(".caskopt:active");
if (!Object.keys(press).length) fail("вариант не отвечает на нажатие");

// Кольцо фокуса достаётся ходьбе с клавиатуры, а не пальцу: тот же приём, что
// у кнопок панели (замечание пользователя про серый контур после нажатия).
if (!Object.keys(ruleOf(".caskopt:focus-visible")).length) {
  fail("варианта не видно с клавиатуры: кольца фокуса нет");
}

// --- палец на узком экране ---

const tap = ruleOf(".caskopt", narrow.media);
const high = /^([0-9.]+)px$/.exec(String(tap["min-height"] || ""));
if (!high || Number(high[1]) < 44) {
  fail("на узком экране вариант ниже пальца: " + (tap["min-height"] || "своей высоты нет"));
}

// --- язык тот же, что у кнопок панели ---

const btn = ruleOf(".btn");
if (rest["border-radius"] !== btn["border-radius"]) {
  fail("скругление варианта своё, а не кнопочное: " + rest["border-radius"] +
    " против " + btn["border-radius"]);
}

console.log("poc_caskopt: ok");
