// Стенд единообразия выбора запуска (POC DK-397, ветка poc-chat, пересобран
// в DK-1570).
//
// «Выбор яруса и подписки различается по секциям: у задач в Check только ярус,
// у задач в работе ещё и подписка» (замечание пользователя). Разошлись они
// из-за прикреплённой подписки: строку Check закрывают той, которой её вели, и
// список подписок у неё снимался целиком. Прикрепление осталось, но правит оно
// теперь умолчание, а не состав меню: разделы одни и те же и там, и там, а
// подсвечена строка той подписки, на которую уйдёт запуск от главной кнопки.
//
// Строка меню с DK-1570 называет модель, ярус и подписку разом, а глубина
// размышления стоит ползунком ниже.
//
// Зовётся: node testdata/poc_pickalike.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const harnessOne = { name: "подписка-раз", default: true,
  models: [{ tier: "base", model: "sonnet", levels: ["low", "medium", "high", "xhigh"] },
    { tier: "pro", model: "opus", levels: ["low", "medium", "high", "xhigh"] },
    { tier: "max", model: "fable", levels: ["low", "medium", "high", "xhigh"] }] };
const harnessTwo = { name: "подписка-два",
  models: [{ tier: "pro", model: "glm-5.3", levels: ["low", "high", "max"] }] };

const board = { prefix: "XR", sections: [
  { key: "check", title: "Check", rows: [
    // Задачу вели второй подпиской: закрытие пойдёт ею же, и в меню подсвечена
    // обязана быть её строка, а не машинное умолчание.
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

// --- разделы в обоих меню одни и те же ---
const asks = (box) => allByClass(box, "hph").map((n) => dump(n).trim());
if (asks(check).join(" | ") !== asks(back).join(" | ")) {
  fail("разделы разные: Check спрашивает «" + asks(check).join(", ") +
    "», очередь «" + asks(back).join(", ") + "»");
}
if (JSON.stringify(asks(check)) !== JSON.stringify(["Модель", "Глубина размышления"])) {
  fail("разделы меню не «Модель» и «Глубина размышления»: " + asks(check).join(", "));
}

// --- строки подписок стоят у обеих строк, полоса уровней уехала в чипы ---
for (const [id, box] of [["XR-1", check], ["XR-2", back]]) {
  const rows = allByClass(box, "hrow");
  if (rows.length !== 4) {
    fail("в меню строки " + id + " строк лестницы " + rows.length + ", а на машине четыре");
  }
  if (allByClass(box, "tpick").length) {
    fail("в меню строки " + id + " осталась полоса отдельных уровней: " + dump(box));
  }
}
// Шкала следует за подсвеченной подпиской (шкала v4): у Check это glm с тремя
// ступенями, у очереди без подсветки общий набор из пяти.
const checkMarks = allByClass(check, "effmark").map((b) => dump(b).trim());
if (JSON.stringify(checkMarks) !== JSON.stringify(["low", "high", "max"])) {
  fail("у строки Check шкала не из трёх ступеней glm: " + JSON.stringify(checkMarks));
}
const backMarks = allByClass(back, "effmark").map((b) => dump(b).trim());
if (JSON.stringify(backMarks) !== JSON.stringify(["low", "medium", "high", "xhigh", "max"])) {
  fail("у строки очереди шкала не из пяти ступеней: " + JSON.stringify(backMarks));
}

// --- подсвечена та подписка, на которую уйдёт запуск ---
const lit = (box) => allByClass(box, "hrow")
  .filter((r) => String(r.className).split(" ").includes("on"))
  .map((r) => dump(r).replace(/\s+/g, " ").trim());
if (lit(check).join(",") !== "glm-5.3 pro, подписка-два") {
  fail("у строки Check подсвечена не своя подписка: " + lit(check).join(",") +
    ", задачу вели на " + harnessTwo.name);
}
// Очедь едет вердиктом: подсвеченной строки нет, выбор остаётся за агентом
// назначения.
if (lit(back).length) {
  fail("у строки очереди подсвечена подписка без выбора человека: " + lit(back).join(","));
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

// --- строка модели: имя, чип и полоски остатка, возраст в подсказке ---
{
  const first = allByClass(back, "hrow")[0];
  if (dump(first).replace(/\s+/g, " ").trim() !== "sonnet base, подписка-раз нед") {
    fail("строка модели не «имя, чип и полоски»: " + dump(first));
  }
  const widths = allByClass(first, "meter").map((m) => m.children[0].style.width);
  if (JSON.stringify(widths) !== JSON.stringify(["52%"])) {
    fail("недельная полоска не доехала до строки: " + JSON.stringify(widths));
  }
  if (!String(first.title || "").includes("снимок 3м назад")) {
    fail("подсказка строки потеряла возраст снимка: " + first.title);
  }
  // Вторая подписка без снимка: полосок нет, а подсказка говорит почему.
  const bare = allByClass(back, "hrow").find((r) => dump(r).includes("подписка-два"));
  if (allByClass(bare, "meter").length) {
    fail("подписка без снимка рисует полоски: " + dump(bare));
  }
  if (!String(bare.title || "").includes("снимка нет")) {
    fail("подписка без снимка молчит о причине: " + bare.title);
  }
}

console.log("выбор запуска: разделы те же в Check и в очереди, подсвечена подписка запуска, " +
  "строка модели называет ярус и подписку");
