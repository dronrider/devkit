import { makeSandbox, makeNode, settle, dump, tag, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const sid = "as111111-1111";
const chats = [{ id: sid, title: "Сохранённый разговор", protocol: "codex",
  model: "модель-codex", pickHarness: "codextest", state: "dead", idle: true, tasks: [] }];
const board = { sections: [] };
const models = [{ model: "модель-codex", harness: "codextest", protocol: "codex", tier: "pro" }];
const { sandbox, timers, store } = makeSandbox(appPathArg(), (path) => {
  if (path.includes("/chats")) return { chats, models };
  if (path.includes("/sessions/")) return { session: sid, head: { id: sid }, items: [], total: 0 };
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});
const reason = "Разговор открыт в другом клиенте Codex. Закройте этот разговор и повторите отправку здесь.";
let tries = 0;
let released = false;
const plain = sandbox.fetch;
sandbox.fetch = (path, init) => {
  if (init?.method === "POST" && path.includes("/say")) {
    tries += 1;
    if (released) return Promise.resolve({ ok: true, status: 200,
      text: () => Promise.resolve(JSON.stringify({ way: "appserver" })) });
    return Promise.resolve({ ok: false, status: 409,
      text: () => Promise.resolve(JSON.stringify({ error: reason, refused: reason })) });
  }
  return plain(path, init);
};
let panelEcho;
const makeEcho = sandbox.makeEcho;
sandbox.makeEcho = (...args) => { panelEcho = makeEcho(...args); return panelEcho; };
const st = await sandbox.chatState("demo", sid, board);
const panel = sandbox.chatPanel("demo", st);
sandbox.makeEcho = makeEcho;
tag(panel, "TEXTAREA").value = "сохраните мою реплику";
deepBtn(panel, "Отправить").handlers.click({ stopPropagation() {} });
await settle();
if (tries !== 1) fail("не было первой попытки: " + tries);
const key = "devkit.chat.pend.demo/" + sid;
const kept = JSON.parse(store.get(key) || "[]");
if (kept.length !== 1 || kept[0].state !== "held" || kept[0].text !== "сохраните мою реплику" || kept[0].why !== reason) {
  fail("отказ не сохранил реплику с причиной: " + JSON.stringify(kept));
}
const flashes = sandbox.document.getElementById("flashes");
if (!dump(flashes).includes(reason)) fail("причина не показана сразу: " + dump(flashes));
for (const timer of timers.splice(0)) timer.fn();
await settle();
if (tries !== 1) fail("отказ автоматически повторился: " + tries);
let reloadedTries = 0;
const echo = sandbox.makeEcho("demo", makeNode("div"), makeNode("div"), sid, () => { reloadedTries += 1; });
for (const timer of timers.splice(0)) timer.fn();
await settle();
if (reloadedTries || tries !== 1) fail("отказ повторился после перезагрузки");
echo.stop();
tag(panel, "TEXTAREA").value = "сохраните мою реплику";
deepBtn(panel, "Отправить").handlers.click({ stopPropagation() {} });
await settle();
if (tries !== 2) fail("повторный отказ не проверен");
const pendingBox = makeNode("div");
const pending = sandbox.makeEcho("demo", pendingBox, makeNode("div"), sid, () => { reloadedTries += 1; });
const beforeHistory = store.get(key);
pending.reconcile([{ role: "user", text: "сохраните мою реплику", key: "app:old" }]);
if (store.get(key) !== beforeHistory) fail("историческое эхо сняло явно отклонённую реплику");
const retryAfterReload = pending.add("сохраните мою реплику");
pending.sent(retryAfterReload);
pending.reconcile([{ role: "user", text: retryAfterReload.text, key: "app:reload" }]);
const restoredRefusals = JSON.parse(store.get(key) || "[]");
if (restoredRefusals.length !== 2 || restoredRefusals.some((m) => m.state !== "held" || !m.refused)) {
  fail("успешный повтор с обновлённой панели остался без собственного эха: " + store.get(key));
}
pending.stop();
released = true;
tag(panel, "TEXTAREA").value = "сохраните мою реплику";
deepBtn(panel, "Отправить").handlers.click({ stopPropagation() {} });
await settle();
if (tries !== 3) fail("повтор после освобождения не отправлен");
const delivered = panelEcho;
delivered.reconcile([{ role: "user", text: "сохраните мою реплику", key: "app:10" }]);
const refusals = JSON.parse(store.get(key) || "[]");
if (refusals.length !== 2 || refusals.some((m) => m.state !== "held" || !m.refused)) {
  fail("успешный повтор не получил собственного эха: " + store.get(key));
}
delivered.stop();
const reloaded = sandbox.makeEcho("demo", makeNode("div"), makeNode("div"), sid, () => { reloadedTries += 1; });
for (const timer of timers.splice(0)) timer.fn();
await settle();
const afterReload = JSON.parse(store.get(key) || "[]");
if (reloadedTries || tries !== 3 || afterReload.length !== 2 || afterReload.some((m) => m.state !== "held" || !m.refused || m.why !== reason)) {
  fail("после обновления изменился исход отклонённых или успешной попыток: " + store.get(key));
}
reloaded.stop();
const equalAddr = "same-real-messages";
const equalKey = "devkit.chat.pend.demo/" + equalAddr;
const equal = sandbox.makeEcho("demo", makeNode("div"), makeNode("div"), equalAddr, () => {});
const firstEqual = equal.add("одинаковые настоящие сообщения");
equal.sent(firstEqual);
const secondEqual = equal.add("одинаковые настоящие сообщения");
equal.sent(secondEqual);
equal.reconcile([{ role: "user", text: firstEqual.text, key: "app:20" }]);
if (JSON.parse(store.get(equalKey) || "[]").length !== 1) fail("одно эхо сняло два настоящих сообщения");
equal.reconcile([{ role: "user", text: firstEqual.text, key: "app:21" }]);
if (store.get(equalKey)) fail("второе настоящее сообщение не снято своим эхом");
equal.stop();
const clipsAddr = "different-refused-attachments";
const clipsKey = "devkit.chat.pend.demo/" + clipsAddr;
const clips = sandbox.makeEcho("demo", makeNode("div"), makeNode("div"), clipsAddr, () => {});
for (const path of ["/fixture/first.png", "/fixture/second.png"]) {
  const m = clips.add("один текст с разными вложениями", null, sandbox.shotPrefix(path) + "один текст с разными вложениями", null, [{ kind: "image", path }]);
  clips.refused(m, reason);
}
const keptClips = JSON.parse(store.get(clipsKey) || "[]");
if (keptClips.length !== 2 || !keptClips[0].wire.includes("first.png") || !keptClips[1].wire.includes("second.png")) {
  fail("отклонённые разные вложения потеряны: " + store.get(clipsKey));
}
clips.stop();
console.log("appchat_conflict: ok");
