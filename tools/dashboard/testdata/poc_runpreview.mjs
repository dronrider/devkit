import { makeSandbox, settle, dump, byClass, allByClass, autoSet, appPathArg, fail }
  from "./poc_dom.mjs";

const app = appPathArg();
const first = { name: "firsttest", default: false,
  models: [{ tier: "pro", model: "first-pro", levels: ["low", "high"] }] };
const own = { name: "owntest", default: true,
  models: [{ tier: "base", model: "own-pro", levels: ["low", "high"] },
    { tier: "pro", model: "own-pro", levels: ["low", "high"] }] };
const click = (node) => node.handlers.click({ stopPropagation() {} });
const active = (pop) => allByClass(pop, "hrow").filter((r) => r.classList.contains("on"));
const selected = (pop, text) => {
  const rows = active(pop);
  if (rows.length !== 1 || !dump(rows[0]).includes(text)) {
    fail("ожидалась модель " + text + ": " + dump(pop));
  }
};

async function setup(list, { pinned = "", task = false, fresh = false, goal = false } = {}) {
  let answer = { tier: "pro", via: "owntest", model: "own-pro" };
  const posts = [], reads = [];
  const { sandbox: s } = makeSandbox(app, (path, init) => {
    if (init?.method === "POST") {
      posts.push({ path, body: JSON.parse(init.body) });
      return { message: "только стенд, процесс не поднят" };
    }
    if (path === "/api/harnesses") return { harnesses: list };
    if (path === "/api/projects") return { projects: [] };
    if (path === "/api/quota") return { harnesses: [] };
    if (path.includes("/pick")) { reads.push(path); return answer; }
    return {};
  });
  await settle();
  await s.loadHarnesses();
  let wide;
  const control = s.runControl("demo", fresh ? "" : "XR-1", (label) => {
    wide = s.barBtn("btn", label, "i-play"); return wide;
  }, "Разобрать", goal, "", "", pinned,
  (h, t, m, e) => s.groomDraft("demo", "XR-1", "", h, t, m, e),
  { list: task ? ["вердикт", "pro"] : ["pro"], now: task ? "вердикт" : "pro", auto: !goal });
  const pop = byClass(control, "hpop");
  const open = async () => { click(byClass(control, "more2")); await settle(); };
  const fire = async () => { click(wide); await settle(); };
  return { s, control, pop, posts, reads, open, fire, answer: (v) => { answer = v; } };
}

// Первая строка лестницы не должна заменять default, независимо от порядка.
for (const list of [[first, own], [own, first]]) {
  for (const fresh of [false, true]) {
    const c = await setup(list, { fresh });
    await c.open();
    selected(c.pop, "own-pro");
    if (c.reads.some((p) => p.includes("/tasks/"))) fail("черновик спрашивает вердикт строки задачи");
    if (!c.reads.some((p) => p.startsWith("/api/harnesses/pick?"))) fail("нет серверного назначения");
    await c.fire();
    if (c.posts.length !== 1 || c.posts[0].body.harness || c.posts[0].body.tier !== "pro") {
      fail("автоматический заказ изменён: " + JSON.stringify(c.posts));
    }
  }
}

{
  const c = await setup([first, own], { goal: true });
  await c.open();
  selected(c.pop, "own-pro");
  await c.fire();
  if (c.posts.length !== 1 || c.posts[0].body.harness) fail("цель потеряла назначение по умолчанию");
}

{
  const c = await setup([first, own]);
  await c.open();
  autoSet(c.pop, false);
  click(allByClass(c.pop, "hrow").find((r) => dump(r).includes("first-pro")));
  await c.fire();
  if (c.posts[0]?.body.harness !== "firsttest") fail("ручная подписка потеряна");
  selected(c.pop, "own-pro");
  autoSet(c.pop, false);
  click(allByClass(c.pop, "hrow").find((r) => dump(r).includes("first-pro")));
  autoSet(c.pop, true);
  selected(c.pop, "own-pro");
  await c.fire();
  if (c.posts[1]?.body.harness) fail("возврат к авто сохранил ручной выбор");
}

{
  const c = await setup([first, own], { pinned: "firsttest", task: true });
  await c.open();
  selected(c.pop, "first-pro");
  await c.fire();
  if (c.posts[0]?.body.harness !== "firsttest") fail("закреплённая Check потеряна");
}

for (const task of [false, true]) {
  const c = await setup([first, own], { task });
  await c.open();
  c.answer({ raw: { status: 502, text: JSON.stringify({ error: "назначение недоступно" }) } });
  click(byClass(c.control, "more2"));
  await c.open();
  if (active(c.pop).length || !dump(c.pop).includes("назначение недоступно")) fail("ошибка оставила ложную подсветку");
  if (!task) {
    await c.fire();
    if (c.posts.length) fail("нет назначения, но работа всё равно запущена");
    if (!dump(c.s.document.getElementById("flashes")).includes("назначение недоступно")) {
      fail("отказ автоматического запуска не показан вне меню");
    }
    c.answer({ tier: "pro", via: "owntest", model: "own-pro" });
    click(byClass(c.control, "more2"));
    await c.open();
    selected(c.pop, "own-pro");
    if (dump(c.pop).includes("назначение недоступно")) fail("прежний отказ остался после восстановления");
  }
}

{
  const c = await setup([first, own]);
  await c.open();
  c.answer({ tier: "pro", via: "firsttest", model: "first-pro" });
  await c.fire();
  if (c.posts.length) fail("сменившаяся пара запущена до показа пользователю");
  selected(c.pop, "first-pro");
  await c.fire();
  if (c.posts.length !== 1) fail("подтверждённое новое назначение не запущено");
}

console.log("предпросмотр запуска: default в обеих перестановках, новая запись, ручной/auto, Check, отказ, смена пары; реальных запусков 0");
