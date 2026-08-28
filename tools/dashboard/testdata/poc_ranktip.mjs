// Стенд подсказки ранга (POC DK-397, ветка poc-chat).
//
// Подсказок у ранга было две, и человек забраковал обе разом: «при наведении на
// ранг есть аж две подсказки. Одна появляется непосредственно в строке сразу и
// наползает на контент следующей колонки. А вторая всплывающая, которая
// появляется через секунду. При этом обе подсказки плохие. Нужно заменить на
// одну которая всплывающая. Это очень компактный блок с 5 строками где написано
// наименование показателя и число».
//
// Отсюда предмет стенда: в ячейке ранга не осталось ни строки слагаемых,
// разворачивавшейся прямо в колонке, ни родной подсказки браузера с её
// задержкой; вместо них один блок, где пять показателей RANKING.md названы
// словами и подписаны числами, а под ними итог. Блок стоит поверх строки, и
// это сторожится правилом стилей: строку он не двигает.
//
// Зовётся: node testdata/poc_ranktip.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { makeSandbox, settle, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const CSS = fs.readFileSync(path.join(path.dirname(app), "style.css"), "utf8");

// Слагаемые взяты разными нарочно: подсказка обязана показать каждое своим
// числом, а не пересказать сумму.
const PARTS = [50, 7, 3, 5, 4];
const rows = [
  { id: "XR-101", title: "ясли для сессий", sect: "backlog", r: 69, r_parts: PARTS,
    moved: "2026-08-20", cost: "M", type: "bug" },
];

const { sandbox, byId } = makeSandbox(app, (path_) => {
  if (path_ === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path_ === "/api/harnesses") return { harnesses: [] };
  if (path_ === "/api/notifications") return { items: [] };
  if (path_.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ key: "backlog", title: "Backlog", rows }] },
      works: [] };
  }
  if (path_.endsWith("/works")) return { works: [] };
  if (path_.endsWith("/drafts")) return { drafts: [] };
  if (path_.includes("/chats")) return { chats: [], models: [] };
  if (path_ === "/api/quota") return { harnesses: [] };
  return {};
});

const groups = byId.get("groups");
sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();

const cell = byClass(groups, "rank");
if (!cell) fail("ячейки ранга в строке доски нет");
const sum = byClass(cell, "rsum");
if (!sum) fail("суммы ранга в ячейке нет");

// --- первой подсказки, разворачивавшейся в строке, больше нет ---
if (byClass(cell, "rfold")) {
  fail("слагаемые по-прежнему разворачиваются строкой в самой ячейке: узел .rfold на месте");
}

// --- второй, родной подсказки браузера, тоже нет ---
if (sum.title) {
  fail("на сумме ранга осталась подсказка браузера с её задержкой: «" + sum.title + "»");
}
const walk = (node, seen = []) => {
  for (const kid of node.children || []) {
    seen.push(kid);
    walk(kid, seen);
  }
  return seen;
};
for (const node of walk(cell)) {
  if (node.title) fail("в ячейке ранга осталась подсказка браузера: «" + node.title + "»");
}

// --- осталась одна, своя ---
const tips = allByClass(cell, "rtip");
if (tips.length !== 1) fail("подсказок ранга в ячейке " + tips.length + ", а нужна одна");
const lines = allByClass(tips[0], "rtl");
if (lines.length !== PARTS.length + 1) {
  fail("в подсказке " + lines.length + " строк, а нужно пять показателей и итог под ними");
}
const said = lines.map((one) => [byClass(one, "rtn"), byClass(one, "rtv")]
  .map((n) => String((n || {}).textContent || "").trim()));

// Имена показателей сверяются с RANKING.md через тот же список, каким ранг
// правят на экране задачи: вторая копия имён разошлась бы с доком молча.
const NAMES = ["Серьёзность", "Ценность", "Неопределённость", "Поправка на баг", "Рычаг"];
NAMES.forEach((name, at) => {
  if (said[at][0] !== name) {
    fail("строка " + (at + 1) + " подсказки названа «" + said[at][0] + "», а по RANKING.md это «" +
      name + "»");
  }
  if (said[at][1] !== String(PARTS[at])) {
    fail("у показателя «" + name + "» в подсказке число " + said[at][1] +
      ", а слагаемое " + PARTS[at]);
  }
});
if (said[5][1] !== "69") fail("итог подсказки " + said[5][1] + ", а ранг строки 69");
if (!said[5][0]) fail("итог в подсказке не подписан словом");

// --- на телефоне наведения нет, и блок открывается нажатием ---
sum.handlers.click({ stopPropagation() {} });
if (!String(cell.className).split(" ").includes("on")) {
  fail("нажатие на сумму не открыло блок слагаемых: на телефоне его иначе не увидеть");
}
if (sum.attrs["aria-expanded"] !== "true") {
  fail("состояние блока не сказано для чтения с экрана: " + sum.attrs["aria-expanded"]);
}

// --- блок стоит поверх строки и показывается сразу ---
const flat = CSS.replace(/\s+/g, " ");
const rule = /\.rtip\{([^}]*)\}/.exec(flat);
if (!rule) fail("правила .rtip в стилях нет");
if (!/position:absolute/.test(rule[1])) {
  fail("блок слагаемых стоит в потоке ячейки и двигает соседнюю колонку: " + rule[1]);
}
if (!/z-index:/.test(rule[1])) fail("блок слагаемых не поднят над строкой: " + rule[1]);
if (!/\.rank:hover \.rtip,\.rank\.on \.rtip\{display:block\}/.test(flat)) {
  fail("блок не показывается наведением и нажатием одним правилом");
}
if (/\.rtip\{[^}]*(transition|animation)-delay/.test(flat)) {
  fail("показ блока отложен задержкой: подсказка через секунду это ровно то, что забраковали");
}

console.log("poc_ranktip: ok");
