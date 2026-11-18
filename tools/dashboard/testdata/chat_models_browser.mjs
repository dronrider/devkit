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
      { model: "gpt-6.2-sol", harness: "codextest", tier: "pro", default: true },
      { model: "gpt-6.2-astra", harness: "codextest", tier: "max" },
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
    window.fetch = oldFetch;
    return { fresh: options(fresh), historic: options(historic), freshMenu: menu(fresh), historicMenu: menu(historic), blankOrder };
  });
  assert.deepEqual(got.fresh.filter((o) => o.selected).map((o) => o.model), ["gpt-6.2-sol"]);
  assert.ok(got.fresh.every((o) => o.model !== "gpt-6.1-sol"), "прежнее предпочтение не возвращается в новый выбор");
  assert.deepEqual(got.historic.find((o) => o.model === "gpt-6.1-sol"), { model: "gpt-6.1-sol", selected: true, disabled: true });
  for (const menu of [got.freshMenu, got.historicMenu]) {
    assert.ok(menu.includes("gpt-6.2-sol") && menu.includes("gpt-6.2-astra"), "меню предлагает обе актуальные модели");
    assert.ok(!menu.includes("gpt-6.1-sol"), "историческая подпись не становится действием меню");
  }
  assert.deepEqual(got.blankOrder, { id: "", model: "gpt-6.2-sol", harness: "codextest" });
  assert.deepEqual(errors, []);
  console.log(JSON.stringify(got));
} finally {
  await browser.close();
}
