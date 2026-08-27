// Стенд яруса запуска задачи и цели (ветка poc-chat).
//
// Конвейер задачи и витки цели шли дефолтом клиента: модель в команде не
// называлась вовсе, и работа уходила верхним ярусом, которого ей никто не
// назначал. Назначает ярус вердикт agentctl pick (правило доски: исполнителя и
// ярус называет вердикт, а не глаз диспетчера), а человек его переопределяет.
// Про команду и вердикт отвечает сервер (TestRunStartTierFromVerdict), предмет
// этого стенда кнопка: что уезжает в заказ.
//
// Зовётся: node testdata/poc_runtier.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

// Выбор яруса с подпиской живёт в меню строки под тремя точками, а главная
// кнопка запускает работу тем, что в этом меню выбрано. Меню открывается
// нажатием, как это делает человек.
const openMenu = (box) => {
  const dots = byClass(box, "rdots");
  if (!dots) fail("у строки нет кнопки с тремя точками: " + dump(box));
  dots.handlers.click({ stopPropagation: () => {} });
  return byClass(box, "rmenu");
};
const runBtn = (box) => {
  const btn = byClass(box, "rmain");
  if (!btn) fail("у строки нет главной кнопки запуска: " + dump(box));
  return btn;
};

const app = appPathArg();

const tiers = [{ tier: "mini", model: "haiku" }, { tier: "base", model: "sonnet" },
  { tier: "pro", model: "opus" }, { tier: "max", model: "fable" }];
const harnesses = [
  { name: "claude-code", bin: "claude", default: true, models: tiers },
  { name: "glm-code", bin: "glm", models: [{ tier: "pro", model: "glm-5.3" }] },
];

const posted = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    return { id: "XR-002", session: "task-XR-002", message: "конвейер поднят", tier: "pro" };
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();

const task = { id: "XR-002", title: "Обычная задача", sect: "backlog", r: 30, cost: "-" };
const goal = { id: "XR-100", title: "Цель: пробный цикл", sect: "backlog", r: 41, cost: "XL" };
const last = () => posted[posted.length - 1];

// --- у задачи первым стоит вердикт и он выбран ---
{
  const box = sandbox.rowAction("demo", task, "backlog");
  const pop = openMenu(box);
  if (!pop) fail("у кнопки запуска нет списка выбора: " + dump(box));
  const picks = allByClass(pop, "tpick").map((p) => dump(p).trim());
  if (picks[0] !== "вердикт") fail("первым ярусом стоит не вердикт: " + JSON.stringify(picks));
  const on = allByClass(pop, "tpick").filter((p) => String(p.className).includes("on"))
    .map((p) => dump(p).trim());
  if (JSON.stringify(on) !== JSON.stringify(["вердикт"])) {
    fail("по умолчанию выбран не вердикт: " + JSON.stringify(on));
  }
  // Приписки под полосой больше нет вовсе: она объясняла, откуда ярусы, надолго
  // ли выбор и кто называет ярус, и пользователь забраковал её прямой оценкой.
  // Вердикт при этом никуда не делся, он стоит первой кнопкой полосы и выбран.
  for (const gone of ["вердикт agentctl pick", "agentctl harness",
    "Выбор действует на один запуск"]) {
    if (dump(pop).includes(gone)) fail("под полосой уровней вернулась приписка «" + gone + "»");
  }
  if (byClass(pop, "hfoot")) fail("подвал списка вернулся: " + dump(pop));
}

// --- запуск без выбора уезжает без яруса: его назовёт вердикт ---
{
  const box = sandbox.rowAction("demo", task, "backlog");
  runBtn(box).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!last() || !last().path.endsWith("/runs")) fail("запуск не ушёл: " + JSON.stringify(posted));
  if (last().body.tier) {
    fail("дашборд назвал ярус за вердикт: " + JSON.stringify(last().body));
  }
}

// --- выбор человека перебивает вердикт ---
{
  const box = sandbox.rowAction("demo", task, "backlog");
  const pop = openMenu(box);
  allByClass(pop, "tpick").find((p) => dump(p).trim() === "base")
    .handlers.click({ stopPropagation: () => {} });
  await settle();
  runBtn(box).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!last().body || last().body.tier !== "base") {
    fail("выбранный человеком ярус не доехал до заказа: " + JSON.stringify(last().body));
  }
}

// --- у цели вердикта нет, по умолчанию pro ---
{
  const box = sandbox.rowAction("demo", goal, "backlog");
  const pop = openMenu(box);
  const picks = allByClass(pop, "tpick").map((p) => dump(p).trim());
  if (picks.includes("вердикт")) {
    fail("у цели предложен вердикт, которого на весь цикл нет: " + JSON.stringify(picks));
  }
  const on = allByClass(pop, "tpick").filter((p) => String(p.className).includes("on"))
    .map((p) => dump(p).trim());
  if (JSON.stringify(on) !== JSON.stringify(["pro"])) {
    fail("у цели по умолчанию выбран не pro: " + JSON.stringify(on));
  }
  runBtn(box).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!last().body || last().body.tier !== "pro" || last().body.id !== "XR-100") {
    fail("ярус цели не доехал до заказа: " + JSON.stringify(last().body));
  }
}

// --- ярус и подписка едут вместе ---
{
  const box = sandbox.rowAction("demo", task, "backlog");
  const pop = openMenu(box);
  allByClass(pop, "tpick").find((p) => dump(p).trim() === "max")
    .handlers.click({ stopPropagation: () => {} });
  await settle();
  allByClass(pop, "hrow").find((h) => dump(h).includes("glm-code"))
    .handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!last().body || last().body.tier !== "max" || last().body.harness !== "glm-code") {
    fail("ярус с подпиской разъехались: " + JSON.stringify(last().body));
  }
}

console.log("poc_runtier: ok");
