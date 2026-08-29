// Стенд плашки квоты (ветка poc-chat): отказ обновления виден коротко, причина
// приходит нажатием, возраст снимка стоит часом.
//
// Живой случай: снимок квоты стоял трёхчасовой давности, потому что обновление
// упиралось в вопрос клиента про доверие каталогу. Причина повторялась в
// журнале демона каждые десять минут, а на экране стояло только «снимок 3ч 12м
// назад», и объяснения человек не имел. Причину в плашку завели, и она приехала
// туда абзацем того, кто отказал: agentctl объясняет человеку в терминале
// целыми фразами, а колонка тут шириной с ладонь («почему опять эта портянка
// вылезла», замечание пользователя).
//
// Предмет стенда это длина строки на экране. Отказ подписан несколькими
// словами, абзац причины лежит за нажатием «почему», а час снимка стоит рядом с
// давностью: человеку важнее знать, что цифры от 18:51.
//
// Зовётся: node testdata/poc_quotafail.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Длина строки отказа на экране: несколько слов. Взята с запасом к тому, что
// пишет сервер, и заведомо короче любой фразы того, кто отказал.
const SAID_MAX = 40;

// Причина приходит готовой строкой, сжимает её сервер (quotaFailWords). Тут она
// та самая, что вылезла на экран портянкой.
const detail = "клиент упёрся в частоту обращений к панели /usage и цифр не показал. " +
  "Снимок встанет следующей попыткой, лимит подписки тут ни при чём.";
let quota = {
  dir: "/home/.devkit/quota",
  harnesses: [{
    name: "claude-code",
    taken: "2026-08-29T18:51",
    age: "3ч 12м",
    age_sec: 11520,
    stale: true,
    buckets: [{ name: "week_all", used_pct: 52 }],
  }],
  fail: { reason: "снимок не обновился", detail, dir: "/Users/rider", age: "4м" },
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
if (!said.includes("снимок от 18:51")) {
  fail("час снимка пропал из плашки: " + said);
}
if (!said.includes("3ч 12м назад")) {
  fail("возраст снимка пропал из плашки: " + said);
}

const bad = byClass(plate, "qfail");
if (!bad) fail("строка отказа не отдельным узлом: " + said);
const line = String(dump(bad)).replace(/почему|скрыть/g, "").trim();
if (!line.includes("снимок не обновился")) {
  fail("плашка молчит о том, что снимок перестал обновляться: " + line);
}
if ([...line].length > SAID_MAX) {
  fail("строка отказа на экране длиной в " + [...line].length + " знаков: " + line);
}
if (line.includes("частоту обращений") || line.includes("/Users/rider")) {
  fail("причина развёрнута прямо в строке: " + line);
}

// Причина и каталог вызова лежат подсказкой: спрашивают их реже, чем читают
// саму строку.
if (!String(bad.title).includes("частоту обращений") ||
    !String(bad.title).includes("/Users/rider") || !String(bad.title).includes("4м")) {
  fail("подсказка отказа не назвала ни причины, ни каталога, ни давности: " + bad.title);
}

// Нажатием причина разворачивается целиком.
const why = deepBtn(plate, "почему");
if (!why) fail("причину отказа нечем спросить: " + said);
why.onclick();
await settle();
const opened = dump(byId.get("quota"));
if (!opened.includes("частоту обращений") || !opened.includes("каталог вызова /Users/rider")) {
  fail("нажатие не развернуло причину: " + opened);
}

// Перерисовка плашки раскрытое не схлопывает: ручка отвечает каждые несколько
// секунд, и причина уезжала бы из-под руки.
await sandbox.refreshQuota();
await settle();
if (!dump(byId.get("quota")).includes("частоту обращений")) {
  fail("развёрнутая причина схлопнулась перерисовкой плашки");
}

// Обновление прошло: строка отказа уходит целиком, а не остаётся висеть.
quota = Object.assign({}, quota, { fail: null });
await sandbox.refreshQuota();
await settle();
const after = dump(byId.get("quota"));
if (after.includes("снимок не обновился") || after.includes("частоту обращений")) {
  fail("отказ остался в плашке после удачного обновления: " + after);
}
if (!after.includes("3ч 12м назад")) {
  fail("вместе с отказом из плашки ушёл и снимок: " + after);
}

console.log("poc_quotafail: ok");
