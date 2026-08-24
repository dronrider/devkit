// Стенд выбора подписки у груминга (ветка poc-chat).
//
// Разбор черновика это такая же работа агента, как конвейер задачи: выбор
// подписки у него был только у задачи, а груминг всегда шёл подпиской по
// умолчанию, и заплатить за него другой квотой было нечем (замечание
// пользователя). Предмет стенда это составная кнопка разбора над списком:
// широкая половина поднимает разбор подпиской по умолчанию, а выбранная из
// списка едет в заказ именем. Стоит она над накопителем, а не в строке: строки
// держат отметки выбора, и запуск один на выбранное (решение пользователя).
//
// Зовётся: node testdata/poc_groomsub.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const harnesses = [
  { name: "claude-code", bin: "claude", default: true },
  { name: "glm-code", bin: "glm" },
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
if (deepBtn(row, "Провести груминг")) fail("кнопка разбора вернулась в строку: " + dump(row));
if (!byClass(row, "dpick")) fail("в строке накопителя нет отметки выбора: " + dump(row));

// Выбрана одна запись: полоса запуска собирается на неё. Правка выбора
// пересобирает полосу, поэтому её узлы берутся заново, а не держатся с прошлого
// раза.
const bar = sandbox.draftRunBar("demo", []);
const pickOne = () => {
  sandbox.draftPickSet("XR-D1", true);
  const split = byClass(bar, "split");
  return { split, pop: split && byClass(split, "hpop"),
    wide: deepBtn(bar, "Разобрать выбранное") };
};
if (!pickOne().wide) fail("кнопки разбора над списком нет вовсе: " + dump(bar));
// Подтверждение стоит между нажатием и подъёмом: сессий поднимется столько,
// сколько выбрано, и сказать об этом надо до нажатия.
const confirm = async (node) => {
  node.handlers.click({ stopPropagation: () => {} });
  await settle();
  const go = deepBtn(bar, "Поднять 1");
  if (!go) fail("подтверждения перед подъёмом нет: " + dump(bar));
  go.handlers.click({ stopPropagation: () => {} });
  await settle();
};

// --- у разбора есть выбор подписки, как у запуска задачи ---
{
  const { split, pop } = pickOne();
  if (!split) fail("у разбора нет составной кнопки с выбором подписки: " + dump(bar));
  if (!pop) fail("списка подписок у разбора нет: " + dump(split));
  const names = allByClass(pop, "hrow").map((h) => dump(h).trim().split(/\s+/)[0]);
  if (!names.includes("glm-code") || !names.includes("claude-code")) {
    fail("в списке подписок разбора не обе подписки машины: " + JSON.stringify(names));
  }
}

// --- широкая половина поднимает разбор подпиской по умолчанию ---
await confirm(pickOne().wide);
if (!posted.length || !posted[0].path.includes("/groom")) {
  fail("широкая половина не подняла разбор: " + JSON.stringify(posted));
}
// Широкая половина едет подпиской по умолчанию, той же дорогой, что у запуска
// задачи: имя её в заказе стоять может, а чужого имени там быть не должно.
const wideName = (posted[0].body || {}).harness || "";
if (wideName && wideName !== "claude-code") {
  fail("широкая половина поднимает разбор не подпиской по умолчанию: " + wideName);
}

// --- выбранная подписка едет в заказ именем ---
const pick = allByClass(pickOne().pop, "hrow").find((h) => dump(h).includes("glm-code"));
await confirm(pick);
const last = posted[posted.length - 1];
if (!last.path.includes("/groom")) fail("выбор подписки не поднял разбор: " + JSON.stringify(posted));
if (!last.body || last.body.harness !== "glm-code") {
  fail("выбранная подписка не доехала до заказа разбора: " + JSON.stringify(last.body));
}

// --- нажатие на разбор не уводит внутрь записи ---
// Прежде кнопка стояла в строке, нажатие всплывало до её обработчика, и вместо
// запуска открывалась форма записи (замечание пользователя).
sandbox.location.hash = "#demo/drafts";
await confirm(pickOne().wide);
if (sandbox.location.hash !== "#demo/drafts") {
  fail("запуск разбора увёл с накопителя: " + sandbox.location.hash);
}

// --- у цели выбор подписки такой же, как у задачи ---
// Витки цели платятся выбранной подпиской: имя едет оболочке цикла флагом.
// Прежде выбора у цели не было вовсе, а сервер отвечал на него отказом
// (замечание пользователя).
{
  const goal = { id: "XR-7", title: "Цель: панель разговора", sect: "backlog" };
  const box = sandbox.rowAction("demo", goal, "backlog");
  const split = byClass(box, "split");
  if (!split) fail("у строки цели нет выбора подписки: " + dump(box));
  const list = allByClass(split, "hrow").map((h) => dump(h).trim().split(/\s+/)[0]);
  if (!list.includes("glm-code")) {
    fail("в списке подписок цели нет второй подписки машины: " + JSON.stringify(list));
  }
  const was = posted.length;
  allByClass(split, "hrow").find((h) => dump(h).includes("glm-code"))
    .handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (posted.length === was || !last.path.includes("/runs")) {
    fail("выбор подписки не поднял цель: " + JSON.stringify(posted.slice(was)));
  }
  if (!last.body || last.body.harness !== "glm-code" || last.body.id !== "XR-7") {
    fail("до запуска цели не доехали ни ID, ни подписка: " + JSON.stringify(last.body));
  }
}

console.log("poc_groomsub: ok");
