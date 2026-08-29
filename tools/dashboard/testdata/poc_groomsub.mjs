// Стенд выбора модели у груминга (ветка poc-chat).
//
// Разбор черновика это такая же работа агента, как конвейер задачи, и платится
// он той же квотой: выбор был только у задачи, а груминг всегда шёл подпиской
// по умолчанию (замечание пользователя). Спрашивать про подписку с ярусом
// человек не хочет: рядом с кнопкой ему надо видеть, чем будет разбираться,
// то есть имя модели (решение пользователя). Предмет стенда это полоса запуска
// над накопителем: кнопка «Грумить» и поле модели рядом, а в заказ с
// поля уезжают подписка и ярус, потому что имени модели ручка разбора не знает.
//
// Зовётся: node testdata/poc_groomsub.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, tag, fail, appPathArg }
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
  return { btn: deepBtn(bar, "Грумить"), sel: tag(bar, "SELECT") };
};
if (!pickOne().btn) fail("кнопки разбора над списком нет вовсе: " + dump(bar));
// Подтверждения между нажатием и подъёмом нет: выбор отметками это и есть
// осознанное действие, а разбор поднимается прямо с нажатия.
const confirm = async (node) => {
  node.handlers.click({ stopPropagation: () => {} });
  await settle();
};

// --- рядом с кнопкой видно, чем будет разбираться ---
{
  const { sel } = pickOne();
  if (!sel) fail("поля модели рядом с кнопкой разбора нет: " + dump(bar));
  const names = (sel.children || []).map((o) => dump(o).trim());
  for (const want of ["opus", "sonnet", "glm-5.3", "glm-5.4"]) {
    if (!names.includes(want)) {
      fail("в поле модели нет модели раскладки " + want + ": " + JSON.stringify(names));
    }
  }
  // По умолчанию стоит модель подписки по умолчанию на ярусе разбора: верхний
  // ярус клиента разбору не нужен (RUN_TIER).
  if (sel.value !== "opus") fail("по умолчанию выбрана не модель разбора: " + sel.value);
}

// --- кнопка поднимает разбор моделью, которая видна рядом ---
await confirm(pickOne().btn);
if (!posted.length || !posted[0].path.includes("/groom")) {
  fail("кнопка не подняла разбор: " + JSON.stringify(posted));
}
// Наружу едут подписка и ярус: имени модели ручка разбора не знает, и ломать
// эту передачу выбор модели не должен.
if (!posted[0].body || posted[0].body.harness !== "claude-code" || posted[0].body.tier !== "pro") {
  fail("модель по умолчанию не развернулась в подписку с ярусом: " + JSON.stringify(posted[0].body));
}

// --- выбранная модель уезжает своей подпиской и своим ярусом ---
{
  const { sel } = pickOne();
  sel.value = "glm-5.3";
  sel.handlers.change({});
  await settle();
  await confirm(pickOne().btn);
  const last = posted[posted.length - 1];
  if (!last.path.includes("/groom")) fail("выбор модели не поднял разбор: " + JSON.stringify(posted));
  if (!last.body || last.body.harness !== "glm-code" || last.body.tier !== "base") {
    fail("выбранная модель доехала до заказа не своей парой: " + JSON.stringify(last.body));
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

// --- у цели выбор подписки такой же, как у задачи ---
// Витки цели платятся выбранной подпиской: имя едет оболочке цикла флагом.
// Прежде выбора у цели не было вовсе, а сервер отвечал на него отказом
// (замечание пользователя).
{
  const goal = { id: "XR-7", title: "Цель: панель разговора", sect: "backlog" };
  const box = sandbox.rowAction("demo", goal, "backlog");
  // Выбор подписки живёт всплывашкой кнопки запуска: подписку с ярусом человек
  // выбирает раз в десяток запусков, и своей кнопки в строке ей не надо.
  byClass(box, "rmain").handlers.contextmenu({
    preventDefault: () => {}, stopPropagation: () => {} });
  const split = byClass(box, "rmenu");
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
