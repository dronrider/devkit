// Стенд пары памяти в сводке ресурсов (DK-1388, возврат 2026-10-17).
//
// Живой случай: сводка писала занятую и свободную память мегабайтами всегда,
// и на машине с десятками гигабайт строка «20068/13103 МБ» вытягивалась и
// место занимала зря. Замечание просило единицу по значению: от гигабайта
// гигабайты, меньше гигабайта мегабайты, строка короткая.
//
// Предмет стенда: сводка несёт одну пару «занято/свободно» с одной единицей
// на двоих, единицу выбирает большая доля, гигабайты идут с запятой в дроби
// тем же форматом, что объёмы владельцев, а меньшая доля в гигабайтах
// остаётся короткой «0,2». Числа стенд подаёт настоящей сводке и читает
// нарисованную строку, поэтому судит о виде, а не о тексте исходника.
//
// Зовётся: node testdata/poc_resmem.mjs static/app.js

import { makeSandbox, settle, byClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Память машины меняется между тремя состояниями: большая, меньше гигабайта
// и несимметричная, где занято меньше гигабайта, а свободно десятки.
// Последняя держит выбор единицы большей долей: решай занятая, и свободные
// гигабайты снова ехали бы мегабайтами.
const MEMS = [
  { pct: 60, used_kb: 20001234, total_kb: 33554432 },
  { pct: 40, used_kb: 209715, total_kb: 524288 },
  { pct: 1, used_kb: 204800, total_kb: 23273472 },
];
const PAIRS = ["19,1/12,9 ГБ", "205/307 МБ", "0,2/22,0 ГБ"];
let at = 0;

const { byId, timers } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path === "/api/notifications") return { items: [] };
  if (path === "/api/waiting") return { items: [], errors: [] };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  if (path.endsWith("/works")) return { works: [] };
  if (path.endsWith("/drafts")) return { drafts: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  if (path === "/api/resources") {
    return { at: at += 1, cpu: { pct: 8 }, mem: MEMS[at - 1] };
  }
  return {};
});
await settle();

// Пара живёт в сводке тела раздела: значки и проценты рядом, а дробь с
// разделителем читается одним взглядом.
const pairText = () => {
  const sum = byClass(byId.get("res"), "rsum");
  if (!sum) fail("сводки в теле раздела ресурсов нет");
  for (const kid of sum.children || []) {
    if (String((kid || {}).textContent || "").includes("/")) return kid.textContent;
  }
  fail("пара «занято/свободно» в сводке не нашлась");
};

// Первый круг опроса уже прошёл загрузкой модуля, следующие заводит таймер
// круга: стенд гоняет его сам, меняя память машины между кругами.
const beat = async () => {
  const one = timers.filter((t) => t.ms === 2500 && t.fn).pop();
  if (!one) fail("круг опроса ресурсов не заведён");
  const go = one.fn;
  one.fn = null;
  go();
  await settle();
};

for (let i = 0; i < PAIRS.length; i += 1) {
  if (i > 0) await beat();
  const got = pairText();
  if (got !== PAIRS[i]) {
    fail("пара памяти «" + got + "» вместо «" + PAIRS[i] + "»");
  }
}
console.log("пара памяти: " + PAIRS.join(" | "));
