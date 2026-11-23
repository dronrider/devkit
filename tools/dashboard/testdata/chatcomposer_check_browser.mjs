import assert from "node:assert/strict";
import { createRequire } from "node:module";

const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token, id, text] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const auth = await context.request.post(base + "/api/login", { data: { token } });
  assert.equal(auth.status(), 200);
  const page = await context.newPage();
  page.setDefaultTimeout(7500);
  await page.goto(base + "/#demo/" + id + "/chat/" + id, { waitUntil: "domcontentloaded" });
  const panel = page.locator("#cpin .cslot:not(.off)");
  const field = panel.locator("textarea");
  const button = panel.getByRole("button", { name: "Начать работу", exact: true });
  await field.fill(text);
  const result = page.waitForResponse(resp => resp.request().method() === "POST" &&
    new URL(resp.url()).pathname === "/api/projects/demo/runs");
  await button.click();
  const response = await result;
  const body = await response.json();
  assert.equal(response.status(), 409, JSON.stringify(body));
  const request = response.request().postDataJSON();
  assert.equal(request.id, id);
  assert.equal(request.intent, "chat");
  assert.equal(request.text, text);
  assert.match(body.error, /приёмк.*карточк/);
  assert.equal(body.session, undefined);
  if (text) await panel.locator(".mlocal .m-held .mwhy").waitFor({ state: "visible" });
  assert.equal(await field.inputValue(), text);
  assert.equal(await page.evaluate(key => localStorage.getItem(key) || "", "devkit.chat.draft." + id), text);
} finally {
  await browser.close();
}
