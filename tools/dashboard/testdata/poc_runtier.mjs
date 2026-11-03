// Стенд яруса запуска задачи и цели (ветка poc-chat, пересобран в DK-1570).
//
// Конвейер задачи и витки цели шли дефолтом клиента: модель в команде не
// называлась вовсе, и работа уходила верхним ярусом, которого ей никто не
// назначал. Назначает ярус вердикт agentctl pick (правило доски: исполнителя и
// ярус называет вердикт, а не глаз диспетчера), а человек его переопределяет.
// Про команду и вердикт отвечает сервер (TestRunStartTierFromVerdict), предмет
// этого стенда строка доски: что уезжает в заказ и что подсвечено в меню под
// тремя точками.
//
// Зовётся: node testdata/poc_runtier.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

// Меню модели и глубины открывается тремя точками строки, а запуск идёт главной
// кнопкой. Выбор в меню не запускает работу, он лишь называет подписку с ярусом
// следующего запуска.
const openMenu = (box) => {
  const btn = byClass(box, "rdots");
  if (!btn) fail("у строки нет трёх точек: " + dump(box));
  btn.handlers.click({ stopPropagation: () => {} });
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
  if (path.endsWith("/pick")) return { tier: "base", via: "строка доски", effort: "medium" };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();

const task = { id: "XR-002", title: "Обычная задача", sect: "backlog", r: 30, cost: "-" };
const goal = { id: "XR-100", title: "Цель: пробный цикл", sect: "backlog", r: 41, cost: "XL" };
const last = () => posted[posted.length - 1];

// --- у задачи умолчание это вердикт: ни одна строка не подсвечена ---
{
  const box = sandbox.rowAction("demo", task, "backlog");
  const pop = openMenu(box);
  if (!pop) fail("у строки нет меню выбора: " + dump(box));
  const on = allByClass(pop, "hrow").filter((h) => String(h.className).includes("on"));
  if (on.length) fail("без выбора человека подсвечена строка лестницы: " + dump(on[0]));
  await settle();
  // Кольцо вердикта приезжает ответом ручки pick открытием меню: вердикт
  // принадлежит строке, а не разговору, и заранее его не видно.
  const verd = allByClass(pop, "effdot").filter((d) => String(d.className).includes("verd"))
    .map((d) => d.dataset.level);
  if (JSON.stringify(verd) !== JSON.stringify(["medium"])) {
    fail("кольцо вердикта не приехало на шкалу: " + JSON.stringify(verd));
  }
}

// --- запуск без выбора уезжает без яруса: его назовёт вердикт ---
{
  posted.length = 0;
  const box = sandbox.rowAction("demo", task, "backlog");
  runBtn(box).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!last() || !last().path.endsWith("/runs")) fail("запуск не ушёл: " + JSON.stringify(posted));
  if (last().body.tier || last().body.effort) {
    fail("дашборд назвал за вердикт то, чего человек не выбирал: " + JSON.stringify(last().body));
  }
}

// --- выбор человека перебивает вердикт ---
{
  posted.length = 0;
  const box = sandbox.rowAction("demo", task, "backlog");
  const pop = openMenu(box);
  allByClass(pop, "hrow").find((h) => dump(h).includes("sonnet"))
    .handlers.click({ stopPropagation: () => {} });
  await settle();
  if (posted.some((p) => p.path.endsWith("/runs"))) {
    fail("выбор строки модели запустил работу сам: " + JSON.stringify(posted));
  }
  runBtn(box).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!last().body || last().body.tier !== "base" || last().body.harness !== "claude-code") {
    fail("выбранный человеком ярус не доехал до заказа: " + JSON.stringify(last().body));
  }
}

// --- у цели вердикта нет, по умолчанию pro ---
{
  posted.length = 0;
  const box = sandbox.rowAction("demo", goal, "backlog");
  const pop = openMenu(box);
  await settle();
  const on = allByClass(pop, "hrow").filter((h) => String(h.className).includes("on"))
    .map((h) => dump(h).replace(/\s+/g, " ").trim());
  if (on.length !== 1 || !on[0].includes("opus")) {
    fail("у цели по умолчанию не подсвечен pro: " + JSON.stringify(on));
  }
  // Кольца на цели нет: вердикта на весь цикл не существует, и рисовать его
  // значило бы врать про уровень следующего витка.
  const verd = allByClass(pop, "effdot").filter((d) => String(d.className).includes("verd"));
  if (verd.length) fail("у цели на шкале стоит кольцо вердикта");
  runBtn(box).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!last().body || last().body.tier !== "pro" || last().body.id !== "XR-100") {
    fail("ярус цели не доехал до заказа: " + JSON.stringify(last().body));
  }
}

// --- ярус и подписка едут вместе ---
{
  posted.length = 0;
  const box = sandbox.rowAction("demo", task, "backlog");
  const pop = openMenu(box);
  allByClass(pop, "hrow").find((h) => dump(h).includes("glm-code"))
    .handlers.click({ stopPropagation: () => {} });
  await settle();
  runBtn(box).handlers.click({ stopPropagation: () => {} });
  await settle();
  if (!last().body || last().body.tier !== "pro" || last().body.harness !== "glm-code") {
    fail("ярус с подпиской разъехались: " + JSON.stringify(last().body));
  }
}

// --- выбор живёт один запуск: следующий уходит вердиктом снова ---
{
  posted.length = 0;
  const box = sandbox.rowAction("demo", task, "backlog");
  const run = async () => {
    runBtn(box).handlers.click({ stopPropagation: () => {} });
    await settle();
  };
  const pop = openMenu(box);
  allByClass(pop, "hrow").find((h) => dump(h).includes("glm-code"))
    .handlers.click({ stopPropagation: () => {} });
  await run();
  if (!last().body || last().body.harness !== "glm-code") {
    fail("выбранная подписка не доехала до своего запуска: " + JSON.stringify(last().body));
  }
  await run();
  if (last().body.tier || last().body.harness || last().body.effort) {
    fail("выбор пережил свой запуск: " + JSON.stringify(last().body));
  }
  // Подсветка строки ушла вместе с выбором: меню снова без человека.
  const shut = openMenu(box);
  const on = allByClass(shut, "hrow").filter((h) => String(h.className).includes("on"));
  if (on.length) fail("после запуска подсветка выбора осталась: " + dump(on[0]));
}

console.log("poc_runtier: ok");
