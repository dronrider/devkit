// Стенд выбора подписки у груминга (ветка poc-chat).
//
// Разбор черновика это такая же работа агента, как конвейер задачи: выбор
// подписки у него был только у задачи, а груминг всегда шёл подпиской по
// умолчанию, и заплатить за него другой квотой было нечем (замечание
// пользователя). Предмет стенда это составная кнопка разбора: широкая половина
// поднимает разбор подпиской по умолчанию, а выбранная из списка едет в заказ
// именем.
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

const row = sandbox.draftRow("demo", { id: "XR-D1", title: "мысль с телефона", age_words: "вчера",
  order: "Проведи груминг XR-D1" });
const wide = deepBtn(row, "Провести груминг");
if (!wide) fail("кнопки разбора в строке накопителя нет вовсе: " + dump(row));

// --- у разбора есть выбор подписки, как у запуска задачи ---
const split = byClass(row, "split");
if (!split) fail("у разбора нет составной кнопки с выбором подписки: " + dump(row));
const pop = byClass(split, "hpop");
if (!pop) fail("списка подписок у разбора нет: " + dump(split));
const names = allByClass(pop, "hrow").map((h) => dump(h).trim().split(/\s+/)[0]);
if (!names.includes("glm-code") || !names.includes("claude-code")) {
  fail("в списке подписок разбора не обе подписки машины: " + JSON.stringify(names));
}

// --- широкая половина поднимает разбор подпиской по умолчанию ---
wide.handlers.click({ stopPropagation: () => {} });
await settle();
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
const pick = allByClass(pop, "hrow").find((h) => dump(h).includes("glm-code"));
pick.handlers.click({ stopPropagation: () => {} });
await settle();
const last = posted[posted.length - 1];
if (!last.path.includes("/groom")) fail("выбор подписки не поднял разбор: " + JSON.stringify(posted));
if (!last.body || last.body.harness !== "glm-code") {
  fail("выбранная подписка не доехала до заказа разбора: " + JSON.stringify(last.body));
}

// --- нажатие на кнопку не уводит внутрь записи ---
sandbox.location.hash = "#demo/drafts";
wide.handlers.click({ stopPropagation: () => {} });
await settle();
if (sandbox.location.hash !== "#demo/drafts" && !sandbox.location.hash.includes("draft/XR-D1")) {
  fail("нажатие на разбор увело не туда: " + sandbox.location.hash);
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
