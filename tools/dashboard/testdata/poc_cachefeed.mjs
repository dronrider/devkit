// Стенд обновляемой пометы продления кеша против страницы истории (DK-1349,
// замечание ревью).
//
// fresh() на всякой помете cachekeep вычёркивает прежние. Подгрузка истории
// через ?before= тянет старую помету в talk.unshift, и живая отметка с
// последним временем уезжала: вместо одной обновляемой строки в ленте вставала
// историческая. Стенд держит пару «живая помета + страница истории» и смотрит,
// какая из них осталась.
//
// Зовётся: node testdata/poc_cachefeed.mjs static/app.js

import { makeSandbox, settle, dump, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

const SID = "aaaa1111-2222-4222-8222-222222222222";
const LIVE_AT = "2026-10-10T12:05:00+03:00";
const OLD_AT = "2026-10-10T11:00:00+03:00";

// Часы в подписи те же, что у клиента: стенд и app.js делят Date, и местный
// пояс сдвигает их одинаково.
const hhmm = (stamp) => new Date(stamp).toLocaleTimeString([], {
  hour: "2-digit", minute: "2-digit",
});
const liveTime = hhmm(LIVE_AT);
const oldTime = hhmm(OLD_AT);

// Живая помета в хвосте: последнее продление, время самое свежее.
const liveMark = {
  key: "cachekeep", seq: 9, role: "note", mark: "cachekeep",
  text: "продление кеша", time: LIVE_AT,
};
const liveSay = {
  key: "m:8", seq: 8, role: "assistant", text: "виток идёт", time: "2026-10-10T12:04:00+03:00",
};
// Страница истории несёт ту же помету прошлым временем и рядом реплику
// человека, чтобы страница была не пустой.
const oldMark = {
  key: "cachekeep", seq: 2, role: "note", mark: "cachekeep",
  text: "продление кеша", time: OLD_AT,
};
const oldSay = {
  key: "m:1", seq: 1, role: "user", text: "подними виток", time: "2026-10-10T10:00:00+03:00",
};

const session = {
  id: SID, mtime: "2026-10-10T12:05:00+03:00", branch: "dk-1349",
  task: "DK-1349", taskNote: "замечания ревью", first: "работа по DK-1349",
};

const fail = (msg) => { console.error(msg); process.exit(1); };

const kit = makeSandbox(app, (path) => {
  if (path.includes("/sessions/")) {
    if (path.includes("before=")) {
      return { session: SID, head: session, items: [oldSay, oldMark], start: true };
    }
    return { session: SID, head: session, total: 3, items: [liveSay, liveMark] };
  }
  if (path === "/api/notifications") return { items: [] };
  return {};
});

const box = kit.sandbox.document.createElement("div");
kit.sandbox.wireChatFeed("demo", box, SID);
await settle();

const shown = () => dump(box);
const marks = () => (shown().match(/продление кеша/g) || []).length;

// Хвост открылся с живой пометой: одна строка «продление кеша».
if (!shown().includes("продление кеша")) {
  fail("живая помета продления не встала в хвост: " + shown().slice(0, 300));
}
if (shown().includes("подними виток")) {
  fail("история уехала в хвост без подгрузки: " + shown().slice(0, 300));
}
if (marks() !== 1) {
  fail("в хвосте " + marks() + " помет продления, ждал одну: " + shown().slice(0, 300));
}
if (!shown().includes(liveTime)) {
  fail("живая помета без своего времени " + liveTime + ": " + shown().slice(0, 300));
}

// Подгрузка вверх: страница истории приходит через ?before= и несёт старую
// помету той же строки. Порог подгрузки это scrollTop меньше запаса над
// взглядом; в моке достаточно вызвать слушатель прокрутки у верха.
box.clientHeight = 300;
box.scrollTop = 0;
if (!box.handlers.scroll) fail("лента не повесила слушатель прокрутки");
box.handlers.scroll();
for (let i = 0; i < 30; i += 1) {
  await settle();
  await Promise.resolve();
}

if (!kit.asked.some((p) => p.includes("before="))) {
  fail("подгрузка истории не ушла запросом: " + JSON.stringify(kit.asked));
}

const after = shown();
if (!after.includes("подними виток")) {
  fail("страница истории не подгрузилась: " + after.slice(0, 300) +
    " asked=" + JSON.stringify(kit.asked));
}
if (marks() !== 1) {
  fail("после подгрузки истории " + marks() + " помет продления, ждал одну: " +
    after.slice(0, 300));
}
// Живая помета остаётся с последним временем: историческая её не вытесняет.
if (!after.includes(liveTime)) {
  fail("историческая помета вытеснила живую, времени " + liveTime + " нет: " +
    after.slice(0, 300));
}
if (oldTime !== liveTime && after.includes(oldTime)) {
  fail("в ленте встала историческая помета со временем " + oldTime + ": " +
    after.slice(0, 300));
}

console.log("ok: страница истории не вытесняет живую помету продления, строка одна");
