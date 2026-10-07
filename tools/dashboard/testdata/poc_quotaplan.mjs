// Стенд блока Token Plan (макет DK-1304): две строки показа собраны из чисел
// снимка, и ни одна из них не выдаёт на экран строковое приведение узла.
//
// Живой случай: карточка квоты подписки mimo показывала во второй строке
// вместо процента текст "[object HTMLElement]" — узел строки был положен в
// узел через textContent. Судит стенд именно написанное на экране: процент с
// тремя знаками и слово темпа читаются, хвоста "object" нигде нет.
//
// Зовётся: node testdata/poc_quotaplan.mjs static/app.js

import { makeSandbox, settle, dump, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Числа показа сняты со снимка подписки mimo: лимит пакета в миллиардах,
// процент месяца с тремя знаками, слово темпа и подписи строк от сервера.
const quota = {
  dir: "/home/.devkit/quota",
  harnesses: [{
    name: "mimo",
    taken: "2026-10-07T17:46",
    age: "0м",
    age_sec: 0,
    buckets: [],
    plan: {
      spent_5h: 0,
      window: "5ч",
      left: 69179403896,
      limit: 82000000000,
      pct: 15.635,
      tempo: "в темпе",
      spent_text: "0 кред",
      pair_text: "69.2B/82B",
      pct_text: "15.635%",
    },
  }],
};

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/quota") return quota;
  return {};
});
await settle();

const plate = byId.get("quota");
await sandbox.refreshQuota();
await settle();

const said = dump(plate);
if (said.includes("[object")) {
  fail("в строке блока узел вместо текста: " + said);
}

// Обе строки показа стоят узлами tp-row: первая несёт траты окна с остатком,
// вторая процент с темпом.
const rows = allByClass(plate, "tp-row");
if (rows.length !== 2) {
  fail("показ Token Plan не из двух строк: " + rows.length + " (" + said + ")");
}
const first = dump(rows[0]);
if (!first.includes("0 кред") || !first.includes("69.2B/82B")) {
  fail("первая строка без трат окна или остатка: " + first);
}
const second = dump(rows[1]);
if (!second.includes("15.635%")) {
  fail("процент не дошёл до второй строки: " + second);
}
if (!second.includes("в темпе")) {
  fail("слово темпа не дошло до второй строки: " + second);
}
if (second.includes("[object")) {
  fail("вторая строка вывела приведение узла: " + second);
}

console.log("poc_quotaplan: ok");
