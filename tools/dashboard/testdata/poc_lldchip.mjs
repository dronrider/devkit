// Стенд чипа LLD в блоке «Связи» (замечание пользователя: подпись «LLD
// задачи» не влезала в колонку типа). Подпись у чипа одна, «LLD»; свой дизайн
// от упомянутого отличают порядок (свой стоит первым, его отдаёт сервер) и
// подсказка на чипе. Колонка типа в строках состава растёт под длинную
// подпись, а не режет её: на случай будущих артефактов с длинными именами.
//
// Зовётся: node testdata/poc_lldchip.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { makeSandbox, settle, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const { sandbox } = makeSandbox(app, () => ({}));
await settle();

const card = sandbox.linksCard("demo", {
  lld: [
    { own: true, file: "docs/lld/XR-1.md", title: "свой дизайн" },
    { own: false, file: "docs/lld/XR-2.md", title: "упомянутый дизайн" },
  ],
  tasks: [],
});

const chips = [];
(function walk(node) {
  if (String(node.className || "").split(" ").includes("chip")) chips.push(node);
  for (const kid of node.children || []) {
    if (typeof kid === "object") walk(kid);
  }
})(card);
if (chips.length !== 2) fail("чипов LLD " + chips.length + ", ждал два");

// Подпись короткая и одна на оба рода, различие живёт в подсказке.
for (const chip of chips) {
  if (dump(chip).trim() !== "LLD") {
    fail("подпись чипа не сократилась до «LLD»: " + JSON.stringify(dump(chip)));
  }
}
if (!String(chips[0].title || "").includes("самой задачи")) {
  fail("свой LLD не отличить: подсказки про свой дизайн нет, title=" +
    JSON.stringify(chips[0].title));
}
if (!String(chips[1].title || "").includes("упомянут")) {
  fail("упомянутый LLD не отличить: подсказки нет, title=" +
    JSON.stringify(chips[1].title));
}
// Свой стоит первым: порядок тоже часть различения.
const shown = dump(card);
if (shown.indexOf("свой дизайн") > shown.indexOf("упомянутый дизайн")) {
  fail("свой LLD не первый в списке: " + shown.slice(0, 200));
}

// Колонка типа растёт под текст, а не режет его: обе раскладки .srow держат
// minmax, а не голые 60px.
const css = fs.readFileSync(path.join(path.dirname(app), "style.css"), "utf8");
const cols = [...css.matchAll(/\.srow\{grid-template-columns:([^;}]+)/g)].map((m) => m[1]);
if (!cols.length) fail("правил раскладки .srow в style.css нет");
for (const c of cols) {
  if (/^\d+px/.test(c.trim())) {
    fail("колонка типа заперта жёсткой шириной и режет длинную подпись: " + c);
  }
}
if (!cols.some((c) => c.includes("minmax"))) {
  fail("колонка типа не растёт под текст: minmax в .srow нет");
}

console.log("ok: чип LLD короткий, свой дизайн отличают порядок и подсказка, " +
  "колонка типа растёт под длинную подпись");
