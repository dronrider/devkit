import assert from "node:assert/strict";
import { createRequire } from "node:module";

const { chromium } = createRequire(import.meta.url)("playwright");
const [base, executablePath, token, mode] = process.argv.slice(2);
const active = "#cpin .cslot:not(.off)";
const firstText = "первая реплика";
const fragment = "длинный текст вложения ".repeat(500);
const models = [{ model: "stand-a", harness: "codex", tier: "pro", levels: ["low", "high"] }];
const runURL = "/api/projects/demo/runs";
const chatsURL = "/api/projects/demo/chats";
const fresh = (id) => ({ id, blank: true, state: "blank", title: id, model: "stand-a", pickHarness: "codex" });
const live = (id, task) => ({ id, state: "live", title: id, model: "stand-a", pickHarness: "codex",
  protocol: "codex", tasks: task ? [task] : [] });

function barrier(label) {
  let arrived, reply;
  const request = new Promise(resolve => { arrived = resolve; });
  const response = new Promise(resolve => { reply = resolve; });
  return {
    reply,
    async request() {
      let timer;
      try {
        return await Promise.race([request, new Promise((_, reject) => {
          timer = setTimeout(() => reject(Error("Барьер не достигнут: " + label)), 9000);
        })]);
      } finally {
        clearTimeout(timer);
      }
    },
    async hold(route) {
      arrived({ url: new URL(route.request().url()), body: route.request().postDataJSON() });
      const result = await response;
      if (result.abort) await route.abort("failed");
      else await route.fulfill(result);
    },
  };
}

async function composer(context) {
  const auth = await context.request.post(base + "/api/login", { data: { token } });
  assert.equal(auth.status(), 200);
  await context.route(base + "/", async route => {
    const response = await route.fetch();
    const html = (await response.text()).replace('<script type="module" src="/assets/app.js', '<script src="/assets/app.js');
    await route.fulfill({ response, body: html });
  });
  const page = await context.newPage();
  page.setDefaultTimeout(7500);
  const errors = [];
  page.on("pageerror", error => errors.push(String(error)));
  const state = { chats: [], items: [], launch: null, list: null, clip: null, say: null, reads: [] };
  await page.route("**/api/projects/demo/**", async route => {
    const method = route.request().method();
    const url = new URL(route.request().url());
    const path = url.pathname;
    const suffix = path.split("/").at(-1);
    if (method === "GET" && path === chatsURL && url.searchParams.has("all")) {
      if (state.list && url.searchParams.get("keep")?.split(",").includes(state.list.sid)) {
        return state.list.gate.hold(route);
      }
      return route.fulfill({ json: { chats: state.chats, models } });
    }
    if (method === "GET" && path.endsWith("/tasks/XR-4/chat")) {
      return route.fulfill({ json: { id: "XR-4", kind: "без разговоров" } });
    }
    if (method === "POST" && path === state.launch?.url) return state.launch.gate.hold(route);
    if (method === "POST" && suffix === "draft") return route.fulfill({ json: {} });
    if (suffix === "clip" || suffix === "shot") {
      if (method === "POST") {
        if (state.clip.gate) return state.clip.gate.hold(route);
        return route.fulfill({ json: { path: state.clip.path, bytes: Buffer.byteLength(fragment) } });
      }
      state.reads.push(url.searchParams.get("name"));
      if (suffix === "shot") return route.fulfill({ contentType: "image/png", body: png });
      return route.fulfill({ contentType: "text/plain", body: fragment });
    }
    if (method === "POST" && suffix === "say" && state.say) return state.say(route);
    if (method === "GET" && path.includes("/sessions/")) {
      if (url.searchParams.has("stream")) {
        return route.fulfill({ contentType: "text/event-stream", body: ": fixture\n\n" });
      }
      return route.fulfill({ json: { session: suffix, head: {}, items: state.items, start: true } });
    }
    if (method === "GET" && suffix === "status") return route.fulfill({ json: { live: false, busy: false } });
    return route.continue();
  });

  const ta = page.locator(active + " textarea");
  return {
    page, state, errors, ta,
    send: page.locator(active + " .csend"),
    chips: page.locator(active + " .cclips .cclip"),
    bubbles: page.locator(active + " .chatfeed .mlocal > *"),
    async open(addr) {
      await page.goto(base + "/#demo/chat/" + addr, { waitUntil: "domcontentloaded" });
      await ta.waitFor({ state: "visible" });
    },
    async switched(old, sid) {
      await page.waitForFunction(({ old, sid, active }) => location.hash.endsWith("/chat/" + sid) &&
        document.querySelector(active + " textarea") && document.querySelector(active + " textarea") !== old,
      { old, sid, active });
    },
    async paste(kind = "frag") {
      const accepted = await ta.evaluate((node, { text, image }) => {
        const data = new DataTransfer();
        if (image) data.items.add(new File([new Uint8Array(image)], "screen.png", { type: "image/png" }));
        else data.setData("text/plain", text);
        return !node.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
      }, { text: fragment, image: kind === "image" ? [...png] : null });
      assert.equal(accepted, true, "обработчик принял вложение");
    },
    async effort(level) {
      await page.locator(active + " .cdsel").click();
      await page.locator(active + ' .effmark[data-level="' + level + '"]:visible').click();
      await page.keyboard.press("Escape");
    },
    read(addr) {
      return page.evaluate(addr => ({
        text: chatDraftRead(addr), effort: chatEffortOf("demo", addr), height: chatDraftHeight(addr),
        files: (chatClipsStoredRead(addr) || []).map(c => c.path),
      }), addr);
    },
  };
}

const png = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==", "base64");

function launch(s) {
  const run = mode.startsWith("runs-");
  const from = run ? "XR-4" : "blank-sew";
  const sid = "session-sew";
  const gate = barrier(mode + " /runs или /chats");
  s.state.chats = run ? [] : [fresh(from)];
  s.state.launch = { url: run ? runURL : chatsURL, gate };
  return { from, sid, gate, run, openNew() {
    s.state.chats = [live(sid, run ? from : "")];
    gate.reply({ json: { way: "appserver", session: sid } });
  } };
}

async function handoff(s) {
  const start = launch(s);
  const { from, sid, gate, run } = start;
  await s.open(from);
  const old = await s.ta.elementHandle();
  const list = barrier(mode + " GET списка");
  s.state.list = { sid, gate: list };
  const path = "/stand/late.txt";
  s.state.clip = { path };
  await s.ta.fill(firstText);
  await s.send.click();
  assert.equal((await gate.request()).body.text, firstText);
  await s.ta.fill("черновик до ответа");
  start.openNew();
  assert.equal((await list.request()).url.searchParams.get("keep"), sid);
  assert.ok(await old.isVisible(), "старое поле видно при задержке списка");
  const draft = "черновик после ответа";
  await s.ta.fill(draft);
  await s.paste();
  await s.effort("high");
  s.state.list = null;
  list.reply({ json: { chats: s.state.chats, models } });
  await s.switched(old, sid);
  assert.equal(await s.ta.inputValue(), draft);
  assert.equal(await s.chips.count(), 1);
  assert.equal(await s.send.isEnabled(), true);
  assert.equal((await s.read(from)).text, "");
  assert.equal((await s.read(run ? from : sid)).effort, "high");
  if (!run) assert.equal((await s.read(from)).effort, "");
  assert.equal(await s.bubbles.count(), 1);

  const next = barrier(mode + " следующая реплика");
  s.state.say = route => next.hold(route);
  await s.send.click();
  const request = await next.request();
  assert.ok(request.url.pathname.endsWith("/" + sid + "/say"));
  assert.match(request.body.text, /^<fragment file="\/stand\/late\.txt">/);
  assert.ok(request.body.text.endsWith(draft));
  assert.equal(request.body.effort, "high");
  next.reply({ json: { way: "appserver" } });
}

async function lateUpload(s) {
  const start = launch(s);
  const { from, sid, gate } = start;
  await s.open(from);
  const old = await s.ta.elementHandle();
  const image = mode.endsWith("-image");
  const upload = barrier("POST вложения");
  s.state.clip = { gate: upload };
  await s.ta.fill(firstText);
  await s.send.click();
  assert.equal((await gate.request()).body.text, firstText);
  await s.ta.fill("следующий черновик");
  await s.paste(image ? "image" : "frag");
  const request = await upload.request();
  assert.ok(request.url.pathname.endsWith("/" + from + (image ? "/shot" : "/clip")));
  if (image) assert.equal(request.body.kind, "image/png");
  else assert.equal(request.body.text, fragment);
  start.openNew();
  await s.switched(old, sid);
  assert.equal(await s.chips.count(), 1);
  const path = "/stand/blank-sew/delayed." + (image ? "png" : "txt");
  upload.reply({ json: { path, bytes: image ? png.length : Buffer.byteLength(fragment) } });
  await s.page.waitForFunction(({ sid, path }) => (chatClipsRead(sid) || [])[0]?.path === path, { sid, path });
  assert.deepEqual((await s.read(sid)).files, [path], "ответ загрузки сохраняет путь в новом sid");
  await s.page.reload({ waitUntil: "domcontentloaded" });
  await s.chips.first().waitFor({ state: "visible" });
  assert.equal(await s.ta.inputValue(), "следующий черновик");
  if (image) {
    await s.chips.first().locator(".cshot").click();
    const src = await s.page.locator(".shotbig img").getAttribute("src");
    assert.equal(src, chatsURL + "/" + from + "/shot?name=delayed.png");
  } else {
    await s.chips.first().dispatchEvent("click");
    await s.page.locator(".fragbig").waitFor({ state: "visible" });
  }
  assert.ok(s.state.reads.length, "восстановленный чип читает файл по сохранённому пути");
}

async function retry(s) {
  const sid = "session-retry";
  const duplicate = mode === "retry-after-lost-answer";
  const reload = mode === "retry-after-reload";
  s.state.chats = [live(sid)];
  await s.open(sid);
  await s.effort("low");
  const first = barrier("первый POST /say");
  const second = barrier("повтор POST /say");
  let tries = 0;
  s.state.say = route => (++tries === 1 ? first : second).hold(route);
  await s.ta.fill(firstText);
  await s.send.click();
  const sent = (await first.request()).body;
  assert.equal(sent.effort, "low");
  assert.ok(sent.msg_id);
  await s.ta.fill("следующий черновик");
  if (!duplicate) await s.effort("high");
  first.reply(duplicate ? { abort: true } : { status: 409, json: { error: "отказ стенда" } });
  await s.page.locator(active + " .mlocal .m-bad").waitFor({ state: "visible" });
  if (reload) {
    await s.page.reload({ waitUntil: "domcontentloaded" });
    await s.ta.waitFor({ state: "visible" });
  }
  const repeated = (await second.request()).body;
  assert.deepEqual(repeated, sent, "повтор посылает тот же запрос");
  second.reply({ json: duplicate ? { way: "dup", was: "appserver" } : { way: "appserver" } });
  await s.page.locator(active + " .mlocal .m-sent").waitFor({ state: "visible" });
  assert.equal((await s.read(sid)).effort, duplicate ? "" : "high");
  assert.equal(await s.ta.inputValue(), "следующий черновик");
}

async function taskReceipt(s) {
  const sid = "session-queued";
  const item = (seq, id) => ({
    role: "user", seq, key: "app:" + seq, text: firstText,
    app: { method: "devkit/appTaskMessage", item: { id } },
  });
  s.state.chats = [live(sid, "XR-4")];
  s.state.items = [item(1, "earlier-message")];
  const first = barrier("первая отправка рабочего чата");
  const again = barrier("повтор рабочего чата");
  let tries = 0;
  s.state.say = route => (++tries === 1 ? first : again).hold(route);
  await s.open(sid);
  await s.page.locator(active + " .chatfeed .mlist .frow").waitFor();
  await s.ta.fill(firstText);
  await s.send.click();
  const sent = (await first.request()).body;
  first.reply({ abort: true });
  await s.page.locator(active + " .mlocal .m-bad").waitFor();
  await s.page.reload({ waitUntil: "domcontentloaded" });
  await s.page.locator(active + " .chatfeed .mlist .frow").waitFor();
  assert.equal(await s.bubbles.count(), 1, "старая реплика не снимает пузырь без квитанции");
  assert.deepEqual((await again.request()).body, sent);
  again.reply({ json: { way: "dup", was: "appserver", app_message_id: "new-message" } });
  await s.page.locator(active + " .mlocal .m-sent").waitFor();
  assert.equal(await s.bubbles.count(), 1, "старая реплика не совпадает с ID новой");
  s.state.items.push(item(2, "new-message"));
  await s.page.reload({ waitUntil: "domcontentloaded" });
  await s.page.locator(active + " .chatfeed .mlist .frow").nth(1).waitFor();
  assert.equal(await s.bubbles.count(), 0, "точное эхо снимает локальный пузырь");
}

async function crashBeforeReceipt(s) {
  const taskChat = mode === "crash-task-before-receipt";
  const sid = "session-queued";
  const wrap = active + " .mlocal";
  const key = "devkit.chat.pend.demo/" + sid;
  const row = { ...live(sid, "XR-4"), protocol: taskChat ? "codex" : "" };
  s.state.chats = [row];
  const first = barrier("HTTP ответ ещё не получен");
  let sends = 0;
  s.state.say = route => {
    sends++;
    return first.hold(route).catch(() => {});
  };
  const snapshot = page => page.evaluate(key => (JSON.parse(localStorage.getItem(key) || "[]"))
    .map(({ id, state, appMessageID }) => ({ id, state, appMessageID })), key);
  await s.open(sid);
  await s.ta.fill(firstText);
  await s.send.click();
  const sent = (await first.request()).body;
  assert.deepEqual(await snapshot(s.page), [{ id: sent.msg_id, state: "wait", appMessageID: "" }]);

  const context = s.page.context();
  const cd = await context.newCDPSession(s.page);
  const crashed = s.page.waitForEvent("crash");
  cd.send("Page.crash").catch(() => {});
  await crashed;
  await s.page.close();
  first.reply({ json: taskChat ? { way: "appserver", app_message_id: "new-message" } : { way: "socket" } });

  const reopened = await composer(context);
  reopened.state.chats = [row];
  if (taskChat) {
    reopened.state.items = [{
      role: "user", seq: 1, key: "app:1", text: firstText,
      app: { method: "devkit/appTaskMessage", item: { id: "new-message" } },
    }];
  }
  const probes = [];
  reopened.state.say = route => {
    const body = route.request().postDataJSON();
    if (!body.receipt_only) sends++;
    probes.push(body);
    if (probes.length === 1) return route.fulfill({ status: 503, json: { error: "временный отказ" } });
    return route.fulfill({ json: { way: "dup", was: "appserver", app_message_id: "new-message" } });
  };
  await reopened.page.clock.install();
  await reopened.open(sid);
  if (taskChat) {
    await reopened.page.locator(active + " .chatfeed .mlist .frow").waitFor();
    await reopened.page.waitForFunction(() => document.querySelector("#cpin .mlocal .m-wait"));
  }
  await reopened.page.clock.fastForward(61000);
  if (taskChat) {
    await reopened.page.locator(wrap + " .m-local").waitFor({ state: "detached" });
    assert.deepEqual(probes, Array(2).fill({ msg_id: sent.msg_id, receipt_only: true }));
    assert.deepEqual(await snapshot(reopened.page), []);
    await reopened.page.reload({ waitUntil: "domcontentloaded" });
    await reopened.page.locator(active + " .chatfeed .mlist .frow").waitFor();
    assert.equal(await reopened.bubbles.count(), 0);
    assert.equal(probes.length, 2, "обычное обновление не ищет уже снятую квитанцию");
  } else {
    await reopened.page.locator(wrap + " .m-held").waitFor();
    assert.deepEqual(await snapshot(reopened.page), [{ id: sent.msg_id, state: "held", appMessageID: "" }]);
    assert.deepEqual(probes, [], "неизвестная доставка tmux не повторяется");
  }
  assert.equal(sends, 1, "отправка текста ровно одна");
  console.log(JSON.stringify({ taskChat, sends, claim: sent.msg_id, probes, bubbles: await reopened.bubbles.count() }));
}

async function crashBeforeQueue(s) {
  const feedBefore404 = mode === "crash-task-feed-during-receipt";
  const exhausted = mode === "crash-task-feed-receipt-exhausted";
  const transientFailures = exhausted ? 2 : Number(mode === "crash-task-feed-receipt-503");
  let failuresLeft = transientFailures;
  const sid = "session-queued";
  const key = "devkit.chat.pend.demo/" + sid;
  const row = live(sid, "XR-4");
  s.state.chats = [row];
  const original = barrier("первый запрос ещё не записан в очередь");
  let sends = 0;
  s.state.say = route => {
    sends++;
    return original.hold(route).catch(() => {});
  };
  const snapshot = page => page.evaluate(key => (JSON.parse(localStorage.getItem(key) || "[]"))
    .map(({ id, state, appMessageID, why }) => ({ id, state, appMessageID, why })), key);
  await s.open(sid);
  await s.ta.fill(firstText);
  await s.send.click();
  const sent = (await original.request()).body;
  assert.deepEqual(await snapshot(s.page), [{ id: sent.msg_id, state: "wait", appMessageID: "", why: "" }]);

  const context = s.page.context();
  const cd = await context.newCDPSession(s.page);
  const crashed = s.page.waitForEvent("crash");
  cd.send("Page.crash").catch(() => {});
  await crashed;
  await s.page.close();

  const reopened = await composer(context);
  reopened.state.chats = [row];
  const probes = [];
  let accepted = false;
  const missing = { status: 404, json: { error: "квитанция не найдена" } };
  const firstLookup = barrier("первое чтение квитанции");
  reopened.state.say = route => {
    const request = route.request().postDataJSON();
    if (!request.receipt_only) sends++;
    probes.push({ request, accepted });
    if (feedBefore404 && probes.length === 1) return firstLookup.hold(route);
    if (!accepted) return route.fulfill(missing);
    if (failuresLeft > 0) {
      failuresLeft--;
      return route.fulfill({ status: 503, json: { error: "временный отказ чтения" } });
    }
    return route.fulfill({ json: { way: "dup", was: "appserver", app_message_id: "new-message" } });
  };
  await reopened.page.clock.install();
  const miss = reopened.page.waitForResponse(r => r.url().endsWith("/say") && r.status() === 404);
  const stream = reopened.page.waitForResponse(r => r.url().includes("/sessions/" + sid + "?stream=1"));
  await reopened.open(sid);
  await Promise.all([feedBefore404 ? firstLookup.request() : miss, stream]);
  const waiting = [{ id: sent.msg_id, state: "wait", appMessageID: "", why: "" }];
  const outstanding = await snapshot(reopened.page);
  assert.deepEqual(outstanding, waiting);
  const item = (seq, id) => ({
    role: "user", seq, key: "app:" + seq, text: firstText,
    app: { method: "devkit/appTaskMessage", item: { id } },
  });
  const pull = () => reopened.page.evaluate(() => window.dispatchEvent(new Event("online")));
  const feed = reopened.page.locator(active + " .chatfeed .mlist .frow");
  if (!feedBefore404) {
    const anotherMiss = reopened.page.waitForResponse(r => r.url().endsWith("/say") && r.status() === 404);
    reopened.state.items = [item(1, "other-message")];
    await pull();
    await anotherMiss;
    assert.deepEqual(await snapshot(reopened.page), waiting, "чужой ID с тем же текстом не подтверждает реплику");
    reopened.state.items.push({ role: "assistant", seq: 2, key: "app:2", text: "ещё один ответ" });
    await pull();
    await feed.nth(1).waitFor();
    assert.equal(probes.length, 2, "перерисовка с прежним ID не вызывает повторного чтения");
  }
  const unavailable = () => reopened.page.waitForResponse(r => r.url().endsWith("/say") && r.status() === 503);
  const firstFailure = transientFailures ? unavailable() : null;
  accepted = true;
  original.reply({ json: { way: "appserver", app_message_id: "new-message" } });
  reopened.state.items.push(item(3, "new-message"));
  await pull();
  await feed.nth(reopened.state.items.length - 1).waitFor();
  if (feedBefore404) {
    assert.equal(probes.length, 1, "во время чтения второй запрос не запускается");
    firstLookup.reply(missing);
    await miss;
  }
  if (firstFailure) {
    await (await firstFailure).finished();
    const secondFailure = exhausted ? unavailable() : null;
    await reopened.page.clock.runFor(2500);
    if (secondFailure) {
      await (await secondFailure).finished();
      await reopened.page.clock.runFor(5000);
      assert.equal(probes.filter(probe => probe.accepted).length, 2, "таймер исчерпал бюджет");
      assert.deepEqual(await snapshot(reopened.page), waiting, "квитанция всё ещё неизвестна");
      reopened.state.items.push({ role: "assistant", seq: 4, key: "app:4", text: "ещё один ответ" });
      await pull();
      await feed.nth(3).waitFor();
      assert.equal(probes.filter(probe => probe.accepted).length, 2, "прежние ID не дают нового бюджета");
      reopened.state.items.push(item(5, "another-message"));
      await pull();
      await feed.nth(4).waitFor();
    }
  }
  await reopened.page.locator(active + " .mlocal .m-local").waitFor({ state: "detached" });
  await reopened.page.evaluate(() => window.dispatchEvent(new Event("online")));
  await reopened.page.clock.fastForward(61000);
  const storage = await snapshot(reopened.page);
  const bubbles = await reopened.bubbles.count();
  console.log(JSON.stringify({ feedBefore404, transientFailures, sends, probes, outstanding, storage, bubbles, feedID: "new-message" }));
  const request = { msg_id: sent.msg_id, receipt_only: true };
  assert.equal(sends, 1, "исходный запрос отправляет текст ровно один раз");
  const misses = Array(feedBefore404 ? 1 : 2).fill({ request, accepted: false });
  const readsAfterAcceptance = Array(1 + transientFailures).fill({ request, accepted: true });
  assert.deepEqual(probes, [...misses, ...readsAfterAcceptance]);
  assert.deepEqual(storage, [], "точная квитанция снимает пузырь и запись очереди");
  assert.equal(bubbles, 0);
}

async function attachmentOnly(s) {
  const start = launch(s);
  const { from, sid, gate, run } = start;
  const path = run ? "/stand/attachment.txt" : "/stand/screen.png";
  s.state.clip = { path };
  await s.open(from);
  const old = await s.ta.elementHandle();
  await s.paste(run ? "frag" : "image");
  await s.chips.first().waitFor({ state: "visible" });
  assert.equal(await s.ta.inputValue(), "");
  await s.send.click();
  const { body } = await gate.request();
  assert.ok(body.text.startsWith(run ? '<fragment file="' : '<screenshot file="'));
  assert.ok(body.text.includes(path));
  assert.equal(await s.bubbles.count(), 1);
  start.openNew();
  await s.switched(old, sid);
  assert.equal(await s.bubbles.count(), 1, "пузырь виден в новом чате до истории");
  const attachment = run ? ".fragq" : ".mshot";
  assert.equal(await s.bubbles.locator(attachment).count(), 1);
  // Приезжает разобранная история без сырого wire. Другой файл при пустом
  // тексте не должен снять ожидающий пузырь.
  s.state.items = [{ role: "user", seq: 1, text: "",
    [run ? "frags" : "shots"]: [run ? "/stand/other.txt" : "/stand/other.png"] }];
  await s.page.reload({ waitUntil: "domcontentloaded" });
  await s.page.locator(active + " .chatfeed .frow").nth(1).waitFor();
  assert.equal(await s.bubbles.count(), 1);
  s.state.items = [{ role: "user", seq: 2, text: "", [run ? "frags" : "shots"]: [path] }];
  await s.page.reload({ waitUntil: "domcontentloaded" });
  await s.page.locator(active + " .chatfeed .mlist " + attachment).waitFor();
  assert.equal(await s.bubbles.count(), 0, "история снимает совпавший пузырь");
  assert.equal(await s.page.locator(active + " .chatfeed .frow " + attachment).count(), 1);
  assert.equal(await s.page.evaluate(() => echoMatches(
    { text: "слова", wire: "слова" },
    { text: "слова", wire: "слова", wireReady: false, clips: [{ kind: "frag", path: "" }] },
  )), false, "пока файл грузится, прежняя реплика с теми же словами не снимает пузырь");
}

async function height(s) {
  const { from, gate } = launch(s);
  await s.open(from);
  const grip = s.page.getByRole("separator", { name: "Высота поля ввода" });
  const drag = async delta => {
    const box = await grip.boundingBox();
    const x = box.x + box.width / 2, y = box.y + box.height / 2;
    await s.page.mouse.move(x, y);
    await s.page.mouse.down();
    await s.page.mouse.move(x, y - delta, { steps: 5 });
    await s.page.mouse.up();
  };
  await s.ta.fill(firstText);
  await drag(70);
  const firstHeight = (await s.read(from)).height;
  assert.ok(firstHeight >= 100);
  await s.send.click();
  assert.equal((await gate.request()).body.text, firstText);
  assert.equal((await s.read(from)).height, 0);
  await drag(150);
  const pendingHeight = (await s.read(from)).height;
  assert.ok(pendingHeight > firstHeight);
  gate.reply({ status: 409, json: { error: "запуск отклонён стендом" } });
  await s.page.locator(active + " .mlocal .m-held").waitFor({ state: "visible" });
  assert.equal(await s.ta.inputValue(), firstText);
  assert.equal((await s.read(from)).height, pendingHeight);
  assert.equal(Math.round((await s.ta.boundingBox()).height), pendingHeight);
}

const scenarios = {
  "runs-handoff": handoff, "raise-handoff": handoff, "raise-upload": lateUpload, "raise-upload-image": lateUpload,
  "retry-after-refusal": retry, "retry-after-reload": retry, "retry-after-lost-answer": retry,
  "retry-task-receipt": taskReceipt,
  "crash-task-before-receipt": crashBeforeReceipt, "crash-tmux-before-receipt": crashBeforeReceipt,
  "crash-task-before-queue": crashBeforeQueue, "crash-task-feed-during-receipt": crashBeforeQueue,
  "crash-task-feed-receipt-503": crashBeforeQueue, "crash-task-feed-receipt-exhausted": crashBeforeQueue,
  "runs-fragment": attachmentOnly, "raise-image": attachmentOnly, "runs-height": height,
};
const browser = await chromium.launch({ executablePath, headless: true });
try {
  const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
  const s = await composer(context);
  await scenarios[mode](s);
  assert.deepEqual(s.errors, []);
  console.log(mode + " в Chromium " + browser.version());
} finally {
  await browser.close();
}
