// Стенд зазора между заголовком и остатком времени в шапке вопроса (DK-784).
//
// Две шапки вопросов (пароль и агент) строят элемент b с заголовком и span с
// остатком времени рядом. Без правила отступа они слипаются в одно слово.
// Правило живёт в стиле .caskh .n и задаёт margin-left.
//
// Зовётся: node testdata/poc_caskhgap.mjs static/app.js

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

// Правила по точному селектору.
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

// --- зазор у остатка времени в шапке .caskh ---

const gap = ruleOf(".caskh .n");
if (!Object.keys(gap).length) {
  fail("правила .caskh .n в стилях нет вовсе");
}

const ml = String(gap["margin-left"] || "");
if (!ml || ml === "0" || ml === "0px" || ml === "auto") {
  fail("зазор слева не задан или ноль: « " + ml + "»");
}

// --- зазор задан числом, а не пробелом в разметке ---

const num = /^([0-9.]+)px$/.exec(ml);
if (!num) {
  fail("margin-left имеет неожиданный формат: « " + ml + "» (ожидалось число в px)");
}

console.log("poc_caskhgap: ok");
