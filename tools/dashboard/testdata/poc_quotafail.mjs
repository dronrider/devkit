// Стенд плашки квоты (ветка poc-chat): отказ обновления виден словами.
//
// Живой случай: снимок квоты стоял трёхчасовой давности, потому что обновление
// упиралось в вопрос клиента про доверие каталогу. Причина повторялась в
// журнале демона каждые десять минут, а на экране стояло только «снимок 3ч 12м
// назад», и объяснения человек не имел.
//
// Зовётся: node testdata/poc_quotafail.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Ответ ручки квоты: снимок один и старый, а обновление не проходит. Причина
// приезжает готовой строкой, сжимает её сервер (quotaFailWords).
const reason = "claude спрашивает про доверие каталогу, панель за этим вопросом недоступна";
let quota = {
  dir: "/home/.devkit/quota",
  harnesses: [{
    name: "claude-code",
    age: "3ч 12м",
    age_sec: 11520,
    stale: true,
    buckets: [{ name: "week_all", used_pct: 52 }],
  }],
  fail: { reason, dir: "/Users/rider", age: "4м" },
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
if (!said.includes("снимок 3ч 12м назад")) {
  fail("возраст снимка пропал из плашки: " + said);
}
if (!said.includes("обновление не проходит")) {
  fail("плашка молчит о том, что снимок перестал обновляться: " + said);
}
if (!said.includes(reason)) {
  fail("плашка не назвала причину отказа: " + said);
}
// Совет с командами в плашку не едет: место в колонке узкое, и читают его с
// одного взгляда.
if (said.includes("подтвердить доверие руками")) {
  fail("в плашку уехал совет вместо причины: " + said);
}

// Каталог вызова и давность попытки лежат подсказкой: в строке они заняли бы
// место причины, а спрашивают их реже.
const bad = byClass(plate, "qfail");
if (!bad) fail("строка отказа не отдельным узлом: " + said);
if (!String(bad.title).includes("/Users/rider") || !String(bad.title).includes("4м")) {
  fail("подсказка отказа не назвала ни каталога, ни давности попытки: " + bad.title);
}

// Обновление прошло: строка отказа уходит целиком, а не остаётся висеть.
quota = Object.assign({}, quota, { fail: null });
await sandbox.refreshQuota();
await settle();
const after = dump(plate);
if (after.includes("обновление не проходит")) {
  fail("отказ остался в плашке после удачного обновления: " + after);
}
if (!after.includes("снимок 3ч 12м назад")) {
  fail("вместе с отказом из плашки ушёл и снимок: " + after);
}

console.log("poc_quotafail: ok");
