import { createRequire } from "node:module";
import assert from "node:assert/strict";
const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext();
try {
  assert.equal((await context.request.post(base + "/api/login", { data: { token } })).status(), 200);
  await context.route(base + "/", async (route) => {
    const response = await route.fetch();
    const html = (await response.text()).replace('<script type="module" src="/assets/app.js', '<script src="/assets/app.js');
    await route.fulfill({ response, body: html });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  await page.goto(base + "/#demo");
  await page.waitForFunction(() => typeof modelPick === "function");
  const got = await page.evaluate(async () => {
    const models = [
      { model: "glm-5.3", harness: "other-default", tier: "pro", default: true },
      { model: "gpt-6.2-sol", harness: "codextest", tier: "pro", replaces: ["gpt-6-sol", "gpt-6.1-sol"] },
      { model: "gpt-6.2-astra", harness: "codextest", tier: "max", replaces: ["gpt-6-astra", "gpt-6.1-astra"] },
      { model: "gpt-6.1-terra", harness: "codextest", tier: "base" },
      { model: "gpt-6-luna", harness: "codextest", tier: "mini" },
    ];
    localStorage.setItem("devkit.chat.model", JSON.stringify({ model: "gpt-6.1-sol", harness: "codextest" }));
    const fresh = modelPick("demo", { models, addr: "new" });
    const historic = modelPick("demo", { models, addr: "history", sid: "history",
      entry: { model: "gpt-6.1-sol", pickHarness: "codextest" } });
    document.body.append(fresh, historic);
    const options = (host) => [...host.querySelectorAll("option")].map((o) => ({ model: o.value, disabled: o.disabled, selected: o.selected }));
    const menu = (host) => [...host.querySelectorAll(".menu [data-model]")].map((n) => n.dataset.model);
    const preferences = [];
    for (const model of ["gpt-6-sol", "gpt-6.1-sol", "gpt-6-astra", "gpt-6.1-astra", "gpt-6.1-terra", "gpt-6-luna", "unknown-model"]) {
      localStorage.setItem("devkit.chat.model", JSON.stringify({ model, harness: "codextest" }));
      preferences.push(chatModelPref(models));
    }
    localStorage.setItem("devkit.chat.model", JSON.stringify({ model: "gpt-6.1-sol", harness: "codextest" }));
    let blankOrder;
    const oldFetch = window.fetch;
    window.fetch = async (url, opts = {}) => {
      if (String(url).endsWith("/chats") && !opts.method) {
        return new Response(JSON.stringify({ models, chats: [] }));
      }
      if (String(url).endsWith("/chats/blank")) {
        blankOrder = JSON.parse(opts.body);
        return new Response(JSON.stringify({ id: "" }));
      }
      return oldFetch(url, opts);
    };
    await chatBlankMake("demo", "");
    const solBlankOrder = blankOrder;
    localStorage.setItem("devkit.chat.model", JSON.stringify({ model: "gpt-6.1-astra", harness: "codextest" }));
    const astraFresh = modelPick("demo", { models, addr: "new-astra" });
    await chatBlankMake("demo", "");
    window.fetch = oldFetch;
    return { fresh: options(fresh), astraFresh: options(astraFresh), historic: options(historic), freshMenu: menu(fresh), historicMenu: menu(historic), blankOrder: solBlankOrder, astraBlankOrder: blankOrder, preferences };
  });
  assert.deepEqual(got.fresh.filter((o) => o.selected).map((o) => o.model), ["gpt-6.2-sol"]);
  assert.ok(got.fresh.every((o) => o.model !== "gpt-6.1-sol"), "прежнее предпочтение не возвращается в новый выбор");
  assert.deepEqual(got.historic.find((o) => o.model === "gpt-6.1-sol"), { model: "gpt-6.1-sol", selected: true, disabled: true });
  for (const menu of [got.freshMenu, got.historicMenu]) {
    assert.ok(menu.includes("gpt-6.2-sol") && menu.includes("gpt-6.2-astra"), "меню предлагает обе актуальные модели");
    assert.ok(!menu.includes("gpt-6.1-sol"), "историческая подпись не становится действием меню");
  }
  assert.deepEqual(got.blankOrder, { id: "", model: "gpt-6.2-sol", harness: "codextest" });
  assert.deepEqual(got.astraBlankOrder, { id: "", model: "gpt-6.2-astra", harness: "codextest" });
  assert.deepEqual(got.astraFresh.filter((o) => o.selected).map((o) => o.model), ["gpt-6.2-astra"]);
  assert.deepEqual(got.preferences, [
    { model: "gpt-6.2-sol", harness: "codextest" },
    { model: "gpt-6.2-sol", harness: "codextest" },
    { model: "gpt-6.2-astra", harness: "codextest" },
    { model: "gpt-6.2-astra", harness: "codextest" },
    { model: "gpt-6.1-terra", harness: "codextest" },
    { model: "gpt-6-luna", harness: "codextest" },
    { model: "glm-5.3", harness: "other-default" },
  ], "известные прежние ID сохраняют семейство и подписку, остальные предпочтения сохраняют прежнее поведение");
  const transition = await page.evaluate(async () => {
    const models = [{ model: "gpt-6.2-sol", harness: "codextest", tier: "pro" }];
    const st = { models, addr: "live-task", sid: "as-live-task", entry: {
      model: "gpt-6.1-sol", pickHarness: "codextest", protocol: "codex", state: "live", own: false,
    } };
    const picker = modelPick("demo", st);
    document.body.append(picker);
    const calls = [];
    const originalFetch = window.fetch;
    const originalPaint = repaintChat;
    let paints = 0;
    window.fetch = async (url, opts = {}) => {
      calls.push({ path: String(url), method: opts.method, body: opts.body ? JSON.parse(opts.body) : null });
      return new Response(JSON.stringify({ session: st.sid, model: "gpt-6.2-sol", harness: "codextest" }));
    };
    repaintChat = async () => { paints += 1; };
    chatDraftWrite(st.addr, "моя непосланная реплика");
    try {
      await modelSwitch("demo", st, models[0], true);
      return { disabled: picker.querySelector("select").disabled, calls, paints, draft: chatDraftRead(st.addr) };
    } finally {
      window.fetch = originalFetch;
      repaintChat = originalPaint;
    }
  });
  assert.equal(transition.disabled, false, "живой Codex задачи позволяет явно выбрать актуальную модель");
  assert.deepEqual(transition.calls, [{ path: "/api/projects/demo/chats/as-live-task/model", method: "POST",
    body: { model: "gpt-6.2-sol", harness: "codextest" } }], "выбор не останавливает ход и не шлёт служебную реплику");
  assert.equal(transition.paints, 1);
  assert.equal(transition.draft, "моя непосланная реплика");
  assert.deepEqual(errors, []);
  console.log(JSON.stringify(got));
} finally {
  await browser.close();
}
