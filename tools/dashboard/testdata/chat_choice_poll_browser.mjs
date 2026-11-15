import { createRequire } from "node:module";
import assert from "node:assert/strict";
const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext();
await context.addInitScript(() => {
  localStorage.setItem("devkit.poll.ask", "250");
});
try {
  assert.equal((await context.request.post(base + "/api/login", { data: { token } })).status(), 200);
  const page = await context.newPage();
  const sid = "aa144600-1111-4111-8111-111111111111";
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  await page.goto(base + "/#demo/chat/" + sid);
  const active = page.locator("input.caskpick:not(:disabled)");
  await active.first().waitFor();
  assert.deepEqual(await active.evaluateAll((nodes) => nodes.map((node) => node.checked)), [true, false, true]);
  await active.nth(1).check();
  await page.locator("textarea.csay").fill("пояснение к выбору");
  const readDraft = () => page.evaluate((sid) => {
    const draft = JSON.parse(localStorage.getItem("devkit.askdraft." + sid));
    return { ask: draft.ask, choices: draft.choices, selection: draft.selection, history: draft.history || [] };
  }, sid);
  const before = await readDraft();
  const response = await context.request.get(base + "/api/projects/demo/chats/" + sid + "/ask");
  const current = (await response.json()).ask;
  let mode = "error";
  await page.route("**/ask", (route) => {
    if (mode === "error") {
      mode = "pass";
      return route.fulfill({ status: 500, json: { error: "временная ошибка опроса" } });
    }
    if (mode === "new") return route.fulfill({ json: { ask: next } });
    if (mode === "empty") return route.fulfill({ json: {} });
    return route.continue();
  });
  const failed = await page.waitForResponse((r) => r.url().endsWith("/ask") && r.status() === 500);
  await failed.finished();
  const recovered = await page.waitForResponse((r) => r.url().endsWith("/ask") && r.status() === 200);
  await recovered.finished();
  await active.first().waitFor();
  assert.deepEqual(await active.evaluateAll((nodes) => nodes.map((node) => node.checked)), [true, true, true], "HTTP 500 и восстановление не сбрасывают изменённый выбор");
  assert.deepEqual(await readDraft(), before, "опрос сохраняет вопрос, choices, selection и историю");
  assert.equal(await page.locator("textarea.csay").inputValue(), "пояснение к выбору");
  const next = structuredClone(current);
  next.since += 1;
  next.scenario.snapshot += "-next";
  mode = "new";
  await page.waitForFunction((snapshot) => {
    const draft = JSON.parse(localStorage.getItem("devkit.askdraft.aa144600-1111-4111-8111-111111111111"));
    return draft?.selection?.questions[0]?.snapshot === snapshot;
  }, next.scenario.snapshot);
  assert.deepEqual(await active.evaluateAll((nodes) => nodes.map((node) => node.checked)), [true, false, true], "успешный новый снимок открывает новый выбор");
  mode = "empty";
  await active.first().waitFor({ state: "detached" });
  assert.equal(await active.count(), 0, "успешный пустой ответ снимает текущий вопрос");
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ transient500: true, recovered: true, changedChoice: true, newSnapshot: true, emptyAsk: true }));
} finally { await context.close(); await browser.close(); }
