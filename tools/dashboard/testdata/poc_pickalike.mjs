// Стенд единообразия выбора запуска (POC DK-397, ветка poc-chat).
//
// «Выбор яруса и подписки различается по секциям: у задач в Check только ярус,
// у задач в работе ещё и подписка» (замечание пользователя). Разошлись они
// из-за прикреплённой подписки: строку Check закрывают той, которой её вели, и
// список подписок у неё снимался целиком. Прикрепление осталось, но правит оно
// теперь умолчание, а не состав списка: вопросов в меню два и там, и там, а
// подсвечена в списке та подписка, на которую уйдёт запуск от главной кнопки.
//
// Заодно сторожится состав самой строки подписки: одна полоса, имя и два
// процента остатка, и ничего больше.
//
// Зовётся: node testdata/poc_pickalike.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const harnessOne = { name: "подписка-раз", default: true,
  models: [{ tier: "base" }, { tier: "pro" }, { tier: "max" }] };
const harnessTwo = { name: "подписка-два", models: [{ tier: "pro" }] };

const board = { prefix: "XR", sections: [
  { key: "check", title: "Check", rows: [
    // Задачу вели второй подпиской: закрытие пойдёт ею же, и в списке
    // подсвечена обязана быть она, а не машинное умолчание.
    { id: "XR-1", title: "на приёмке", sect: "check", harness: harnessTwo.name,
      accept: "human" }] },
  { key: "backlog", title: "Backlog", rows: [
    { id: "XR-2", title: "в очереди", sect: "backlog", r: 40, r_parts: [20, 5, 5, 5, 5] }] },
] };

const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR" }] };
  if (path === "/api/harnesses") return { harnesses: [harnessOne, harnessTwo] };
  if (path === "/api/quota") {
    return { harnesses: [{ name: harnessOne.name, age: "3м", age_sec: 180, buckets: [
      { name: "week_all", used_pct: 52, reset: "2026-08-31T14:59" },
      { name: "week_max", used_pct: 7, reset: "2026-08-31T14:59" }] }] };
  }
  if (path === "/api/notifications") return { items: [] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const groups = byId.get("groups");
sandbox.location.hash = "#demo";
await sandbox.refresh();
await settle();

const rowOf = (id) => allByClass(groups, "trow")
  .find((tr) => dump(byClass(tr, "id")).trim() === id) || null;

// Выбор запуска раскрывается правой кнопкой на самой кнопке запуска: трёх
// точек у строки больше нет, под ними лежал ровно этот выбор.
const pickOf = (id) => {
  const tr = rowOf(id);
  if (!tr) fail("строки " + id + " нет на доске");
  const btn = byClass(tr, "rmain");
  if (!btn) fail("у строки " + id + " нет кнопки запуска: " + dump(tr));
  btn.handlers.contextmenu({ preventDefault: () => {}, stopPropagation: () => {} });
  const box = byClass(tr, "rpick");
  if (!box) fail("в меню строки " + id + " нет выбора запуска: " + dump(tr));
  return box;
};

const check = pickOf("XR-1");
const back = pickOf("XR-2");

// --- вопросов в обоих меню поровну, и это те же вопросы ---
const asks = (box) => allByClass(box, "hph").map((n) => dump(n).trim());
if (asks(check).join(" | ") !== asks(back).join(" | ")) {
  fail("вопросы разные: Check спрашивает «" + asks(check).join(", ") +
    "», очередь «" + asks(back).join(", ") + "»");
}
if (asks(check).length !== 2) {
  fail("вопросов в меню не два: " + asks(check).join(", "));
}
if (!asks(check).includes("Уровень модели")) {
  fail("подпись полосы моделей не «Уровень модели»: " + asks(check).join(", "));
}

// --- список подписок стоит у обеих строк ---
for (const [id, box] of [["XR-1", check], ["XR-2", back]]) {
  const rows = allByClass(box, "hrow");
  if (rows.length !== 2) {
    fail("в меню строки " + id + " подписок " + rows.length + ", а на машине две");
  }
  if (!allByClass(box, "tpick").length) {
    fail("в меню строки " + id + " нет полосы уровней модели");
  }
}

// --- подсвечена та подписка, на которую уйдёт запуск ---
const lit = (box) => allByClass(box, "hrow")
  .filter((r) => String(r.className).split(" ").includes("on"))
  .map((r) => dump(byClass(r, "hname")).trim());
if (lit(check).join(",") !== harnessTwo.name) {
  fail("у строки Check подсвечена не своя подписка: " + lit(check).join(",") +
    ", задачу вели на " + harnessTwo.name);
}
if (lit(back).join(",") !== harnessOne.name) {
  fail("у строки очереди подсвечена не подписка по умолчанию: " + lit(back).join(","));
}

// --- приписки про раскладку машины нет ни в одном меню ---
for (const [id, box] of [["XR-1", check], ["XR-2", back]]) {
  if (byClass(box, "hfoot")) fail("подвал списка вернулся в меню строки " + id);
  for (const gone of ["agentctl harness", "Выбор действует на один запуск",
    "Ярус называет вердикт"]) {
    if (dump(box).includes(gone)) {
      fail("в меню строки " + id + " вернулась приписка «" + gone + "»");
    }
  }
}

// --- строка подписки это одна полоса: имя и два процента ---
{
  const first = allByClass(back, "hrow")[0];
  if (byClass(first, "chip")) fail("чип вернулся в строку подписки: " + dump(first));
  if (byClass(first, "meter") || byClass(first, "qrow")) {
    fail("полоска-градусник вернулась в строку подписки: " + dump(first));
  }
  const nums = allByClass(first, "hq");
  if (nums.length !== 2) fail("в строке подписки не два числа остатка: " + dump(first));
  if (!dump(first).includes("52%") || !dump(first).includes("7%")) {
    fail("строка подписки молчит про остаток: " + dump(first));
  }
  if (dump(first).includes("до 31") || dump(first).includes("снимок 3м назад")) {
    fail("дата сброса или возраст снимка вернулись в строку: " + dump(first));
  }
  if (!String(first.title || "").includes("снимок 3м назад")) {
    fail("подсказка строки потеряла возраст снимка: " + first.title);
  }
}

console.log("выбор запуска: вопросы те же в Check и в очереди, подсвечена подписка запуска, " +
  "строка подписки в одну полосу");
