import { createRequire } from "node:module";
import assert from "node:assert/strict";
const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token] = process.argv.slice(2);
const browser = await chromium.launch({ executablePath, headless: true });
const context = await browser.newContext();
const failures = [];
const check = (actual, expected, message) => {
  try { assert.deepEqual(actual, expected, message); }
  catch (error) { failures.push(error.message); }
};
const gate = () => {
  let release, arrived;
  const held = new Promise((resolve) => { release = resolve; });
  const started = new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error("запрос не дошёл до HTTP-барьера")), 30000);
    arrived = () => { clearTimeout(timeout); resolve(); };
  });
  return { release, arrived, held, started };
};
try {
  assert.equal((await context.request.post(base + "/api/login", { data: { token } })).status(), 200);
  // Доступ к функциям настоящего приложения нужен только стенду, исходник не меняется.
  await context.route(base + "/", async (route) => {
    const response = await route.fetch();
    await route.fulfill({ response, body: (await response.text()).replace('<script type="module" src="/assets/app.js', '<script src="/assets/app.js') });
  });
  const page = await context.newPage();
  const errors = [];
  page.on("pageerror", (error) => errors.push(String(error)));
  await page.goto(base + "/#demo");
  await page.waitForFunction(() => typeof chatPanel === "function" && document.querySelector("#groups").children.length);
  await page.evaluate(() => {
    refresh = async () => {};
    switchChat = (addr) => { window.switched = addr; };
    window.mountPending = (extra) => {
      closeChatLive();
      const st = { project: "demo", addr: "", sid: "", task: "", chats: [],
        models: [{ model: "stand-a", harness: "codex", tier: "pro" }], ...extra };
      if (st.entry) st.entry.id = st.entry.id || st.sid || st.blank || st.addr;
      chatOpen = "demo|" + st.addr;
      document.getElementById("cpanel").hidden = false;
      chatSlotPut(document.getElementById("cpin"), chatOpen, [chatHead("demo", st), chatPanel("demo", st)]);
    };
  });
  const ta = page.locator("#cpin .cslot:not(.off) textarea");
  const send = page.locator("#cpin .cslot:not(.off) .csend");
  const mount = (st) => page.evaluate((st) => mountPending(st), st);
  const paste = () => page.evaluate(() => {
    const data = new DataTransfer();
    data.setData("text/plain", "новое вложение ".repeat(500));
    document.querySelector("#cpin .cslot:not(.off) textarea").dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
  });
  let clipNumber = 0;
  await page.route("**/api/projects/demo/chats/*/clip", (route) => route.fulfill({ json: { path: "/tmp/new-" + (++clipNumber) + ".txt" } }));

  // Каждый ответ удерживается после снятия HTTP body. До его освобождения
  // вводятся новый текст, новое вложение и новый выбор глубины.
  let taskNumber = 100;
  for (const kind of ["run", "raise"]) for (const outcome of ["success", "error"]) for (const next of ["high", "low"]) {
    const label = kind + "/" + outcome + "/" + next;
    const addr = kind === "run" ? "XR-" + (++taskNumber) : "blank-pending-" + outcome + "-" + next;
    const target = "as-pending-" + label.replaceAll("/", "-");
    const task = kind === "run" ? addr : "";
    await page.evaluate((addr) => {
      chatClipsWrite(addr, [{ kind: "frag", name: "отправленное вложение", path: "/tmp/sent.txt" }]);
      chatEffortSet("demo", addr, "low");
    }, addr);
    await mount(kind === "run" ? { addr, task, unassigned: true, entry: null } :
      { addr, blank: addr, fresh: true, entry: { model: "stand-a", pickHarness: "codex", state: "blank" } });
    const pending = gate();
    let posted;
    const path = kind === "run" ? "**/api/projects/demo/runs" : "**/api/projects/demo/chats";
    await page.route(path, async (route) => {
      posted = route.request().postDataJSON();
      pending.arrived();
      await pending.held;
      await route.fulfill(outcome === "success"
        ? { json: { way: "appserver", session: target } }
        : { status: 409, json: { error: "запуск отклонён", unknown: true } });
    });
    await ta.fill("исходная реплика");
    await send.click();
    await pending.started;
    check(posted.effort, "low", label + ": отправлен выбранный low");
    check(posted.text.includes("/tmp/sent.txt") && posted.text.includes("исходная реплика"), true, label + ": отправлен исходный снимок");
    check(await ta.isDisabled(), false, label + ": следующий черновик доступен во время запуска");
    await ta.fill("новый черновик " + label);
    await paste();
    await page.waitForFunction((addr) => (chatClipsRead(addr) || []).some((clip) => clip.path?.startsWith("/tmp/new-")), addr);
    await page.evaluate(({ addr, next }) => chatEffortSet("demo", addr, next), { addr, next });
    pending.release();
    if (outcome === "success") {
      await page.waitForFunction((target) => window.switched === target, target);
      check(await send.isDisabled(), true, label + ": старый маршрут закрыт до готовности панели");
    } else await page.waitForFunction(() => !document.querySelector("#cpin .cslot:not(.off) .csend").disabled);
    check(await ta.inputValue(), "новый черновик " + label, label + ": старый ответ сохранил новый текст");
    check(await page.evaluate((addr) => chatEffortOf("demo", addr), outcome === "success" ? task || target : addr), next,
      label + ": старый ответ сохранил новый effort");
    if (outcome === "error") {
      check(await page.evaluate((addr) => chatDraftRead(addr), addr), "новый черновик " + label, label + ": новый текст сохранён после отказа");
      const paths = await page.evaluate((addr) => (chatClipsRead(addr) || []).map((clip) => clip.path), addr);
      check(paths.length === 1 && paths[0].startsWith("/tmp/new-"), true, label + ": новый черновик сохранил только свои вложения");
      await mount(kind === "run" ? { addr, task, unassigned: true, entry: null } :
        { addr, blank: addr, fresh: true, entry: { model: "stand-a", pickHarness: "codex", state: "blank" } });
      check(await ta.inputValue(), "новый черновик " + label, label + ": повторное открытие сохранило новый текст");
      check(await page.locator("#cpin .cslot:not(.off) .cclip").count(), 1, label + ": повторное открытие сохранило новое вложение");
      await page.unroute(path);
      continue;
    }
    check(await page.evaluate((target) => chatDraftRead(target), target), "новый черновик " + label, label + ": текст переехал к новому sid");
    const moved = await page.evaluate((target) => (chatClipsRead(target) || []).map((clip) => clip.path), target);
    check(moved.length === 1 && moved[0].startsWith("/tmp/new-"), true, label + ": переехало только новое вложение");
    check(await page.evaluate((addr) => chatEffortOf("demo", addr), task ? target : addr), "",
      label + ": у выбора остался один ключ");
    // Пересборка панели по новому адресу читает перенесённый черновик.
    await mount({ addr: target, sid: target, task, entry: { protocol: "codex", state: "live", model: "stand-a", pickHarness: "codex" } });
    check(await ta.inputValue(), "новый черновик " + label, label + ": поле нового sid показывает черновик");
    check(await send.isEnabled(), true, label + ": у нового sid доступна отправка");
    check(await page.evaluate((addr) => chatDraftRead(addr), addr), "", label + ": закрытие панели не вернуло старый ключ");
    check(await page.locator("#cpin .cslot:not(.off) .cclip").count(), 1, label + ": поле нового sid показывает вложение");
    const sayPath = "**/api/projects/demo/chats/" + target + "/say";
    let following;
    await page.route(sayPath, (route) => {
      following = route.request().postDataJSON();
      return route.fulfill({ json: { way: "appserver", session: target } });
    });
    await send.click();
    await page.waitForFunction(() => !document.querySelector("#cpin .cslot:not(.off) .csend").disabled);
    check(following?.effort, next, label + ": новый sid отправил новый effort следующим запросом");
    check(following?.text.includes("новый черновик " + label) && following?.text.includes("/tmp/new-"), true,
      label + ": следующий запрос содержит новый текст и вложение");
    check(await page.evaluate((addr) => chatEffortOf("demo", addr), task || target), "", label + ": свой успешный выбор расходуется");
    await page.unroute(sayPath);
    await page.unroute(path);
  }

  // Без нового черновика отказ возвращает отправленный снимок в поле.
  await mount({ addr: "XR-rejected", task: "XR-rejected", unassigned: true, entry: null });
  await page.evaluate(() => chatEffortSet("demo", "XR-rejected", "low"));
  await ta.fill("вернуть после отказа");
  await paste();
  await page.waitForFunction(() => (chatClipsRead("XR-rejected") || [])[0]?.path);
  await page.route("**/api/projects/demo/runs", (route) => route.fulfill({ status: 409, json: { error: "запуск отклонён" } }));
  await send.click();
  await page.waitForFunction(() => !document.querySelector("#cpin .cslot:not(.off) .csend").disabled);
  check(await ta.inputValue(), "вернуть после отказа", "run/error: исходный текст восстановлен в пустое поле");
  check(await page.locator("#cpin .cslot:not(.off) .cclip").count(), 1, "run/error: исходное вложение восстановлено");
  check(await page.evaluate(() => chatEffortOf("demo", "XR-rejected")), "low", "run/error: неуспешный выбор не расходуется");
  await page.unroute("**/api/projects/demo/runs");

  // Пустое поле тоже может быть новым выбором человека после отправки.
  await mount({ addr: "XR-cleared", task: "XR-cleared", unassigned: true, entry: null });
  const cleared = gate();
  await page.route("**/api/projects/demo/runs", async (route) => {
    cleared.arrived();
    await cleared.held;
    await route.fulfill({ status: 409, json: { error: "запуск отклонён" } });
  });
  await ta.fill("исходная реплика до очистки");
  await send.click();
  await cleared.started;
  await ta.fill("новый черновик");
  await ta.fill("");
  cleared.release();
  await page.waitForFunction(() => !document.querySelector("#cpin .cslot:not(.off) .csend").disabled);
  check(await ta.inputValue(), "", "run/error: новый пустой черновик сохранён");
  check(await page.evaluate(() => chatDraftRead("XR-cleared")), "", "run/error: новый пустой черновик сохранён в хранилище");
  await page.unroute("**/api/projects/demo/runs");

  // Важен и повторный выбор low: сравнение одного значения без ревизии
  // сочло бы новый выбор прежним. Две последовательные отправки проверяют,
  // что сохранённый выбор действительно уезжает следующим запросом.
  for (const next of ["high", "low"]) {
    const sid = "as-say-" + next;
    await page.evaluate((sid) => chatEffortSet("demo", sid, "low"), sid);
    await mount({ addr: sid, sid, entry: { protocol: "codex", state: "live", model: "stand-a", pickHarness: "codex" } });
    const pending = gate();
    const bodies = [];
    const path = "**/api/projects/demo/chats/" + sid + "/say";
    await page.route(path, async (route) => {
      bodies.push(route.request().postDataJSON());
      if (bodies.length === 1) { pending.arrived(); await pending.held; }
      await route.fulfill({ json: { way: "appserver", session: sid } });
    });
    await ta.fill("первая реплика");
    await send.click();
    await pending.started;
    await page.evaluate(({ sid, next }) => chatEffortSet("demo", sid, next), { sid, next });
    pending.release();
    await page.waitForFunction(() => !document.querySelector("#cpin .cslot:not(.off) .csend").disabled);
    check(bodies[0].effort, "low", "say: первый запрос содержит low");
    check(await page.evaluate((sid) => chatEffortOf("demo", sid), sid), next, "say: ответ не расходует новый " + next);
    await ta.fill("следующая реплика");
    await send.click();
    await page.waitForFunction(() => !document.querySelector("#cpin .cslot:not(.off) .csend").disabled);
    check(bodies[1]?.effort, next, "say: следующий запрос содержит новый " + next);
    check(await page.evaluate((sid) => chatEffortOf("demo", sid), sid), "", "say: свой успешный выбор расходуется");
    await page.unroute(path);
  }
  check(errors, [], "ошибки приложения");
  assert.deepEqual(failures, [], "границы отправленного снимка");
  console.log(JSON.stringify({ delayedRun: ["success", "error"], delayedRaise: ["success", "error"], draftAndClipsMoved: true, delayedSay: ["high", "low"], nextTurnEffort: true }));
} finally { await context.close(); await browser.close(); }
