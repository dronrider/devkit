// Стенд выбора модели и глубины у груминга (ветка poc-chat, пересобран в
// DK-1570).
//
// Разбор черновика это такая же работа агента, как конвейер задачи, и платится
// он той же квотой: выбор был только у задачи, а груминг всегда шёл подпиской
// по умолчанию (замечание пользователя). Спрашивать про подписку с ярусом
// человек не хочет: рядом с кнопкой ему надо видеть, чем будет разбираться
// (решение пользователя). С DK-1570 рядом с кнопкой стоит стрелка того же
// меню, что и у запуска задачи: строки моделей с подписками и ползунок глубины,
// а в заказ с поля уезжают подписка, ярус и глубина, потому что имени модели
// ручка разбора не знает.
//
// Зовётся: node testdata/poc_groomsub.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const harnesses = [
  { name: "claude-code", bin: "claude", default: true,
    models: [{ tier: "base", model: "sonnet" }, { tier: "pro", model: "opus" }] },
  { name: "glm-code", bin: "glm",
    models: [{ tier: "base", model: "glm-5.3" }, { tier: "pro", model: "glm-5.4" }] },
];

const posted = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return { id: "XR-D1", session: "draft-XR-D1", message: "грумминг поднят" };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();

// В строке накопителя кнопки разбора нет вовсе: там отметка выбора.
const row = sandbox.draftRow("demo", { id: "XR-D1", title: "мысль с телефона", age_words: "вчера",
  order: "Проведи груминг XR-D1" });
if (deepBtn(row, "Грумить")) fail("кнопка разбора вернулась в строку: " + dump(row));
if (!byClass(row, "dpick")) fail("в строке накопителя нет отметки выбора: " + dump(row));

// Выбрана одна запись: полоса запуска собирается на неё. Правка выбора
// пересобирает полосу, поэтому её узлы берутся заново, а не держатся с прошлого
// раза.
const bar = sandbox.draftRunBar("demo", []);
const pickOne = () => {
  sandbox.draftPickSet("XR-D1", true);
  return { btn: deepBtn(bar, "Грумить"), more: byClass(bar, "more2"), pop: byClass(bar, "hpop") };
};
if (!pickOne().btn) fail("кнопки разбора над списком нет вовсе: " + dump(bar));
const confirm = async (node) => {
  node.handlers.click({ stopPropagation: () => {} });
  await settle();
};
const openMenu = () => {
  const parts = pickOne();
  if (!parts.more) fail("у кнопки разбора нет стрелки выбора: " + dump(bar));
  parts.more.handlers.click({ stopPropagation: () => {} });
  if (!parts.pop || parts.pop.hidden) fail("стрелка разбора не открыла меню: " + dump(bar));
  return parts;
};

// --- в меню разбора те же строки моделей и ползунок глубины ---
{
  const { pop } = openMenu();
  const text = dump(pop).replace(/\s+/g, " ");
  for (const want of ["Модель", "Глубина размышления",
    "opus pro, claude-code", "glm-5.3 base, glm-code"]) {
    if (!text.includes(want)) fail("в меню разбора нет «" + want + "»: " + text);
  }
  const marks = allByClass(pop, "effmark").map((b) => dump(b).trim());
  if (JSON.stringify(marks) !== JSON.stringify(["low", "medium", "high", "xhigh", "max"])) {
    fail("шкала глубины разбора не из пяти ступеней: " + JSON.stringify(marks));
  }
  // Умолчание подсвечено: модель подписки по умолчанию на ярусе разбора,
  // верхний ярус клиента разбору не нужен (RUN_TIER).
  const on = allByClass(pop, "hrow")
    .filter((h) => String(h.className).includes("on")).map((h) => dump(h).trim());
  if (on.join(",") !== "opus pro, claude-code") {
    fail("умолчание разбора не модель яруса RUN_TIER: " + on.join(","));
  }
}

// --- кнопка поднимает разбор тем, что подсвечено ---
await confirm(pickOne().btn);
if (!posted.length || !posted[0].path.includes("/groom")) {
  fail("кнопка не подняла разбор: " + JSON.stringify(posted));
}
// Наружу едут подписка и ярус: имени модели ручка разбора не знает, и ломать
// эту передачу выбор модели не должен.
if (!posted[0].body || posted[0].body.harness !== "claude-code" || posted[0].body.tier !== "pro") {
  fail("модель по умолчанию не развернулась в подписку с ярусом: " + JSON.stringify(posted[0].body));
}
if (posted[0].body.effort) {
  fail("без выбора человека глубина уехала в разбор: " + JSON.stringify(posted[0].body));
}

// --- выбранная строка уезжает своей подпиской и ярусом, глубина с ней ---
{
  const was = posted.length;
  const { pop } = openMenu();
  allByClass(pop, "hrow").find((h) => dump(h).includes("glm-5.3"))
    .handlers.click({ stopPropagation: () => {} });
  allByClass(pop, "effmark").find((b) => dump(b).trim() === "xhigh")
    .handlers.click({ stopPropagation: () => {} });
  if (posted.length !== was) {
    fail("выбор в меню сам поднял разбор: " + JSON.stringify(posted.slice(was)));
  }
  await confirm(pickOne().btn);
  const last = posted[posted.length - 1];
  if (!last.path.includes("/groom")) fail("выбор модели не поднял разбор: " + JSON.stringify(posted));
  if (!last.body || last.body.harness !== "glm-code" || last.body.tier !== "base") {
    fail("выбранная модель доехала до заказа не своей парой: " + JSON.stringify(last.body));
  }
  if (last.body.effort !== "xhigh") {
    fail("выбранная глубина не доехала до заказа: " + JSON.stringify(last.body));
  }
}

// --- нажатие на разбор не уводит внутрь записи ---
// Прежде кнопка стояла в строке, нажатие всплывало до её обработчика, и вместо
// запуска открывалась форма записи (замечание пользователя).
sandbox.location.hash = "#demo/drafts";
await confirm(pickOne().btn);
if (sandbox.location.hash !== "#demo/drafts") {
  fail("запуск разбора увёл с накопителя: " + sandbox.location.hash);
}

// --- у цели выбор модели такой же, как у задачи ---
// Витки цели платятся выбранной подпиской: имя едет оболочке цикла флагом.
// Прежде выбора у цели не было вовсе, а сервер отвечал на него отказом
// (замечание пользователя).
{
  const goal = { id: "XR-7", title: "Цель: панель разговора", sect: "backlog" };
  const box = sandbox.rowAction("demo", goal, "backlog");
  byClass(box, "rmain").handlers.contextmenu({
    preventDefault: () => {}, stopPropagation: () => {} });
  const menu = byClass(box, "rmenu");
  if (!menu) fail("у строки цели нет меню модели и глубины: " + dump(box));
  const rows = allByClass(menu, "hrow").map((h) => dump(h).trim());
  if (!rows.some((r) => r.includes("glm-code"))) {
    fail("в меню цели нет второй подписки машины: " + JSON.stringify(rows));
  }
  const was = posted.length;
  allByClass(menu, "hrow").find((h) => dump(h).includes("glm-code"))
    .handlers.click({ stopPropagation: () => {} });
  await settle();
  if (posted.length !== was) {
    fail("выбор строки в меню цели запустил работу сам: " + JSON.stringify(posted.slice(was)));
  }
  byClass(box, "rmain").handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (!last.path.includes("/runs")) {
    fail("запуск цели не ушёл: " + JSON.stringify(posted.slice(was)));
  }
  if (!last.body || last.body.harness !== "glm-code" || last.body.id !== "XR-7") {
    fail("до запуска цели не доехали ни ID, ни подписка: " + JSON.stringify(last.body));
  }
}

console.log("poc_groomsub: ok");
