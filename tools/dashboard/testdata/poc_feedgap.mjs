// Стенд промежутков в ленте разговора (ветка poc-chat).
//
// Записи ленты стояли с разными отступами: реплика просила сверху четырнадцать
// точек, ход инструмента восемь, заголовок дня снова четырнадцать. Лента от
// этого читалась рвано, а над свежей репликой зиял лишний воздух (замечание
// пользователя). Предмет стенда: промежуток один на все виды записей, назван
// одним местом, и первая запись воздуха сверху не просит.
//
// Зовётся: node testdata/poc_feedgap.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { fail, appPathArg } from "./poc_dom.mjs";

const css = fs.readFileSync(path.join(path.dirname(appPathArg()), "style.css"), "utf8");

// Тело правила по селектору: у ленты селекторы простые, и разбирать весь файл
// ради них незачем.
function ruleOf(sel) {
  const at = css.indexOf(sel + "{");
  if (at < 0) return "";
  const end = css.indexOf("}", at);
  return end < 0 ? "" : css.slice(at + sel.length + 1, end);
}

// --- промежуток назван одной переменной ---
const named = /--feedgap:\s*([0-9.]+)px/.exec(css);
if (!named) fail("промежутка ленты нет отдельной переменной: править его придётся по всем видам записей");
const gap = Number(named[1]);
if (!(gap > 0) || gap > 12) {
  fail("промежуток ленты " + gap + " точек: свежая реплика снова стоит в лишнем воздухе");
}

// --- все виды записей берут один и тот же промежуток ---
for (const [sel, what] of [[".msg", "реплика"], [".day", "заголовок дня"], [".tool", "ход инструмента"]]) {
  const rule = ruleOf(sel);
  if (!rule) fail("правила " + sel + " в стилях нет вовсе");
  if (!/margin(-top)?:\s*var\(--feedgap\)/.test(rule)) {
    fail(what + " (" + sel + ") стоит со своим отступом, а не общим: " + rule.slice(0, 120));
  }
  // Числом отступ больше не пишется: разъехавшись, виды записей и дали ту
  // рваную ленту, из-за которой стенд и заведён.
  if (/margin(-top)?:\s*\d/.test(rule)) {
    fail(what + " (" + sel + ") держит отступ числом: " + rule.slice(0, 120));
  }
}

// --- первая запись ленты воздуха сверху не просит ---
{
  const first = ruleOf(".mlist>:first-child");
  if (!first || !/margin-top:\s*0/.test(first)) {
    fail("первая запись ленты отбита сверху пустотой: отделять там нечего");
  }
}

console.log("poc_feedgap: ok");
