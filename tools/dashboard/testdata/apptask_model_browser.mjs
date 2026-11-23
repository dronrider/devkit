import assert from "node:assert/strict";
import { createRequire } from "node:module";

const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token, sid, mode] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
try {
  const context = await browser.newContext();
  assert.equal((await context.request.post(base + "/api/login", { data: { token } })).status(), 200);
  await context.route(base + "/", async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, body: (await response.text()).replace(
      '<script type="module" src="/assets/app.js', '<script src="/assets/app.js') });
  });
  const page = await context.newPage();
  page.setDefaultTimeout(7500);
  if (mode === "menu") await checkMenu(page);
  else await checkChoiceOrder(page, context);
} finally {
  await browser.close();
}

async function checkMenu(page) {
  const work = "#cpin .cslot:not(.off) ";
  const model = page.locator(work + "select.cdsel");
  const menu = page.locator(work + ".cmodel .menu");
  const rejectedPreference = { model: "модель-codex2", harness: "codex2test" };
  const nextChoice = { model: "модель-next", harness: "codextest" };
  const url = base + "/api/projects/demo/chats/" + sid + "/model";
  const modelRequests = [];
  let reject = true;
  await page.route(url, async route => {
    modelRequests.push(route.request().postDataJSON());
    if (reject) {
      reject = false;
      await route.fulfill({ status: 409, json: { error: "каталог изменился" } });
    } else {
      await route.continue();
    }
  });
  const earlier = { model: "модель-pro", harness: "перваяtest" };
  const preference = (tab = page) => tab.evaluate(() => {
    const { model, harness } = JSON.parse(localStorage.getItem("devkit.chat.model"));
    return { model, harness };
  });
  await page.goto(base + "/#demo/chat/" + sid);
  await model.waitFor();
  await page.evaluate(pick => localStorage.setItem("devkit.chat.model", JSON.stringify(pick)), earlier);

  assert.equal(await model.inputValue(), "прежняя-модель");
  const options = await model.locator("option").evaluateAll(options => options.map(
    ({ dataset, disabled }) => ({ model: dataset.model, harness: dataset.harness, disabled })));
  await model.click();
  await menu.waitFor();
  const offered = await menu.locator(".mrow").evaluateAll(rows => rows.map(row => row.dataset.model));
  const foreign = model.locator('option[data-harness="codex2test"]');
  const disabled = await foreign.isDisabled();
  if (disabled) {
    await foreign.evaluate(option => {
      const select = option.parentElement;
      const current = select.selectedOptions[0];
      option.selected = true;
      select.dispatchEvent(new Event("change", { bubbles: true }));
      current.selected = true;
    });
    assert.deepEqual(modelRequests, []);
    assert.deepEqual(await preference(), earlier);
  }

  const next = menu.locator('.mrow[data-model="модель-next"]');
  const conflict = page.waitForResponse(response => response.url() === url && response.status() === 409);
  await next.click();
  await conflict;
  await page.getByText("каталог изменился", { exact: true }).waitFor();
  const denied = await preference();
  const shownAfterDenial = await model.inputValue();
  assert.deepEqual(offered, ["модель-codex", "модель-next"],
    "рабочее меню содержит только допустимые модели: " + JSON.stringify({ options, denied }));
  assert.equal(disabled, true, "другая подписка не выбирается из нативного списка");
  assert.deepEqual(options.find(option => option.model === "прежняя-модель"),
    { model: "прежняя-модель", harness: "codextest", disabled: true });
  assert.deepEqual(options.find(option => option.model === rejectedPreference.model),
    { ...rejectedPreference, disabled: true });
  assert.deepEqual(denied, earlier, "отказ не меняет предпочтение будущих чатов");
  assert.equal(shownAfterDenial, "прежняя-модель");

  if (!await menu.isVisible()) await model.click();
  const saved = page.waitForResponse(response => response.url() === url && response.status() === 200);
  await next.click();
  await saved;
  await page.waitForFunction(({ model, harness, selector }) => {
    const pick = JSON.parse(localStorage.getItem("devkit.chat.model"));
    return pick.model === model && pick.harness === harness &&
      document.querySelector(selector)?.value === model;
  }, { ...nextChoice, selector: work + "select.cdsel" });
  assert.deepEqual(modelRequests, [nextChoice, nextChoice]);
  console.log(JSON.stringify({ chromium: browser.version(), options, offered, denied, shownAfterDenial, modelRequests }));
}


async function checkChoiceOrder(page, context) {
  const work = "#cpin .cslot:not(.off) ";
  const model = page.locator(work + "select.cdsel");
  const menu = page.locator(work + ".cmodel .menu");
  const select = model => menu.locator('.mrow[data-model="' + model + '"]').click();
  const next = { model: "модель-next", harness: "codextest" };
  const primary = { model: "модель-codex", harness: "codextest" };
  const first = mode === "ordered" ? primary : next;
  const second = mode === "ordered" ? next : primary;
  const earlier = { model: "модель-pro", harness: "перваяtest" };
  const newChatChoice = { model: "модель-codex2", harness: "codex2test" };
  const url = base + "/api/projects/demo/chats/" + sid + "/model";
  const requests = [];
  let releaseFirst, signalPersisted;
  const gate = new Promise(resolve => { releaseFirst = resolve; });
  const firstPersisted = new Promise(resolve => { signalPersisted = resolve; });
  await page.route(url, async route => {
    const pick = route.request().postDataJSON();
    requests.push(pick);
    if (pick.model === first.model) {
      const response = await route.fetch();
      assert.equal(response.status(), 200, await response.text());
      signalPersisted();
      await gate;
      return route.fulfill({ response });
    }
    if (mode === "refused") {
      return route.fulfill({ status: 409, json: { error: "каталог изменился" } });
    }
    return route.continue();
  });
  const responseFor = (pick, status) => page.waitForResponse(response =>
    response.url() === url && response.status() === status &&
    response.request().postDataJSON().model === pick.model);
  const preference = (tab = page) => tab.evaluate(() => {
    const { model, harness } = JSON.parse(localStorage.getItem("devkit.chat.model"));
    return { model, harness };
  });

  await page.goto(base + "/#demo/chat/" + sid);
  await model.waitFor();
  await page.evaluate(pick => localStorage.setItem("devkit.chat.model", JSON.stringify(pick)), earlier);
  await model.click();
  await menu.waitFor();
  await select(first.model);
  await firstPersisted;
  assert.deepEqual(await preference(), earlier);

  const newChat = mode === "new-chat" || mode === "new-chat-other-tab";
  let newChatPage = page;
  if (mode === "new-chat-other-tab") {
    newChatPage = await context.newPage();
    newChatPage.setDefaultTimeout(7500);
    await newChatPage.goto(base + "/#demo/chat/" + sid);
    await newChatPage.locator(work + "select.cdsel").waitFor();
  }
  if (newChat) {
    const blank = await newChatPage.evaluate(() => chatBlankMake("demo", ""));
    assert.ok(blank);
    await newChatPage.waitForFunction(({ sid, selector }) =>
      location.hash.endsWith("/chat/" + sid) && document.querySelector(selector)?.value === "модель-pro",
    { sid: blank, selector: work + "select.cdsel" });
    const newMenu = newChatPage.locator(work + ".cmodel .menu");
    await newChatPage.locator(work + "select.cdsel").click();
    await newMenu.locator('.grow[data-group="codex2test"]').click();
    await newMenu.locator('.mrow[data-model="' + newChatChoice.model + '"]').click();
    assert.deepEqual(await preference(newChatPage), newChatChoice);
  } else {
    const response = responseFor(second, mode === "refused" ? 409 : 200);
    await select(second.model);
    await response;
    if (mode === "refused") {
      await page.getByText("каталог изменился", { exact: true }).waitFor();
      assert.deepEqual(await preference(), earlier);
    } else {
      await page.waitForFunction(({ model, selector }) =>
        document.querySelector(selector)?.value === model &&
        JSON.parse(localStorage.getItem("devkit.chat.model"))?.model === model,
      { ...second, selector: work + "select.cdsel" });
    }
  }

  const firstResponse = responseFor(first, 200);
  const refreshed = page.waitForResponse(response =>
    response.url().includes("/api/projects/demo/chats?all=1") && response.request().method() === "GET");
  releaseFirst();
  await firstResponse;
  await (await refreshed).finished();
  if (mode === "refused") {
    await page.waitForFunction(({ model, selector }) =>
      document.querySelector(selector)?.value === model &&
      JSON.parse(localStorage.getItem("devkit.chat.model"))?.model === model,
    { ...first, selector: work + "select.cdsel" });
  }
  const actual = await preference();
  const label = await newChatPage.locator(work + "select.cdsel").inputValue();
  const rows = await (await context.request.get(base + "/api/projects/demo/chats?all=1&keep=" + encodeURIComponent(sid))).json();
  const stored = rows.chats.find(row => row.id === sid).model;
  console.log(JSON.stringify({ mode, tabs: context.pages().length, requests, actual, label, stored }));
  assert.deepEqual(requests, newChat ? [first] : [first, second]);
  assert.equal(stored, next.model);
  assert.deepEqual(actual, newChat ? newChatChoice : next);
  assert.deepEqual(await preference(newChatPage), actual);
  assert.equal(label, newChat ? newChatChoice.model : next.model);
}
