// Стенд кнопки запуска разбора (ветка poc-chat).
//
// Кнопка звалась «Разобрать выбранное» и висела над списком всегда, гашеная при
// пустом выборе, а чем поедет разбор, спрашивалось у неё подпиской и ярусом.
// Человек называет эту работу грумингом и хочет видеть рядом с кнопкой модель,
// а не лестницу, из которой её надо выводить (решение пользователя). Предмет
// стенда это сама кнопка: имя, появление по выбору и то, что выбор рядом с ней
// остался общим меню модели и глубины, без третьей механики. Куда уезжают
// подписка с ярусом и глубина, сторожит poc_groomsub.
//
// Зовётся: node testdata/poc_groomtier.mjs static/app.js

import { makeSandbox, settle, dump, byClass, deepBtn, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const tiers = [{ tier: "mini", model: "haiku" }, { tier: "base", model: "sonnet" },
  { tier: "pro", model: "opus" }, { tier: "max", model: "fable" }];
const harnesses = [
  { name: "claude-code", bin: "claude", default: true, models: tiers },
  { name: "glm-code", bin: "glm", models: [{ tier: "base", model: "glm-5.3" }, { tier: "pro", model: "glm-5.4" }] },
];

const posted = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return { id: "XR-D1", session: "task-XR-D1", message: "груминг поднят", tier: "pro" };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();

// Полоса пересобирается на всякую правку выбора, поэтому её узлы берутся
// заново, а не держатся с прошлого раза.
const bar = sandbox.draftRunBar("demo", []);
const btn = () => deepBtn(bar, "Грумить");

// --- при пустом выборе кнопки нет вовсе ---
{
  if (btn()) fail("кнопка запуска стоит при пустом выборе: " + dump(bar));
  if (deepBtn(bar, "Разобрать выбранное")) fail("прежнее имя кнопки осталось: " + dump(bar));
  if (!dump(bar).includes("Отметьте записи")) {
    fail("при пустом выборе не сказано, откуда берётся разбор: " + dump(bar));
  }
}

// --- отметка ставит кнопку, и зовётся она грумингом ---
{
  sandbox.draftPickSet("XR-D1", true);
  if (!btn()) fail("отметка не поставила кнопку запуска: " + dump(bar));
  if (deepBtn(bar, "Разобрать выбранное")) fail("прежнее имя кнопки вернулось: " + dump(bar));
  if (!dump(bar).includes("Выбрано 1 запись")) {
    fail("полоса не говорит, сколько записей выбрано: " + dump(bar));
  }
}

// --- выбор у кнопки тот же, что у запуска задачи, третьей механики нет ---
// Составная кнопка и её меню это общий вход выбора на весь экран (DK-1570), а
// не отдельная механика груминга: подписка с ярусом и глубиной называются
// строками того же меню. Отдельного поля модели рядом с кнопкой больше нет.
{
  const split = byClass(bar, "split");
  if (!split) fail("у кнопки груминга нет стрелки общего меню: " + dump(bar));
  if (tag(bar, "SELECT")) fail("поле модели вернулось рядом с кнопкой: " + dump(bar));
  sandbox.draftPickSet("XR-D1", true);
  const more = byClass(bar, "more2");
  if (!more) fail("стрелка меню пропала у полосы с выбором: " + dump(bar));
}

// --- снятие отметки убирает кнопку обратно ---
{
  sandbox.draftPickSet("XR-D1", false);
  if (btn()) fail("кнопка осталась после снятия последней отметки: " + dump(bar));
}

// --- разбор всё так же поднимается ярусом разбора, а не дефолтом клиента ---
{
  sandbox.draftPickSet("XR-D1", true);
  // Подтверждения перед подъёмом нет: нажатие поднимает разбор сразу.
  btn().handlers.click({ stopPropagation: () => {} });
  await settle();
  const last = posted[posted.length - 1];
  if (!last || !last.path.includes("/groom")) fail("разбор не поднялся: " + JSON.stringify(posted));
  if (!last.body || last.body.tier !== "pro") {
    fail("в заказ разбора не уехал ярус разбора: " + JSON.stringify(last.body));
  }
}

console.log("poc_groomtier: ok");
