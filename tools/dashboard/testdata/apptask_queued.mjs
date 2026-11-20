import { makeSandbox, makeNode, settle, dump, tipOf, tag, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const sid = "as111111-1111";
const chats = [{ id: sid, title: "Рабочий разговор", protocol: "codex",
  model: "модель-codex", pickHarness: "codextest", state: "dead", idle: true, tasks: [] }];
const board = { sections: [] };
const models = [{ model: "модель-codex", harness: "codextest", protocol: "codex", tier: "pro" }];
const { sandbox, timers, store } = makeSandbox(appPathArg(), (path) => {
  if (path.includes("/chats")) return { chats, models };
  if (path.includes("/sessions/")) return { session: sid, head: { id: sid }, items: [], total: 0 };
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});
const message = "Реплика для следующего хода";
const reason = "реплика сохранена во входе рабочего разговора Codex";
const originalFetch = sandbox.fetch;
let sends = 0;
sandbox.fetch = (path, init) => {
  if (init?.method === "POST" && path.endsWith("/say")) {
    sends += 1;
    return Promise.resolve({ ok: true, status: 202,
      text: () => Promise.resolve(JSON.stringify({ way: "queued", session: sid, message: reason })) });
  }
  return originalFetch(path, init);
};
let activeEcho;
const makeEcho = sandbox.makeEcho;
sandbox.makeEcho = (...args) => { activeEcho = makeEcho(...args); return activeEcho; };
const state = await sandbox.chatState("demo", sid, board);
const panel = sandbox.chatPanel("demo", state);
sandbox.makeEcho = makeEcho;
tag(panel, "TEXTAREA").value = message;
deepBtn(panel, "Отправить").handlers.click({ stopPropagation() {} });
await settle();

const key = "devkit.chat.pend.demo/" + sid;
const readPending = () => JSON.parse(store.get(key) || "[]");
const kept = readPending();
if (sends !== 1 || kept.length !== 1 || kept[0].state !== "held" || !kept[0].queue ||
    kept[0].text !== message || kept[0].why !== reason) {
  fail("ответ 202 должен оставить реплику в очереди: " + JSON.stringify(kept));
}
if (!dump(panel).includes("в очереди") || !tipOf(panel).includes(reason)) {
  fail("панель не показала состояние очереди с причиной: " + dump(panel));
}

activeEcho.stop();
const queue = makeNode("div");
const reloaded = sandbox.makeEcho("demo", queue, makeNode("div"), sid, () => { sends += 1; });
for (const timer of timers.splice(0)) timer.fn();
await settle();
const restored = readPending();
if (sends !== 1 || restored.length !== 1 || !restored[0].queue || restored[0].why !== reason) {
  fail("реплика потерялась или повторилась после перезагрузки: " + JSON.stringify(restored));
}
if (!dump(queue).includes("в очереди") || !tipOf(queue).includes(reason)) {
  fail("обновлённая панель потеряла состояние очереди с причиной: " + dump(queue));
}
reloaded.reconcile([{ role: "user", text: message, key: "app:delivered" }]);
if (readPending().length !== 0) fail("появление реплики в ленте не сняло отметку ожидания");
reloaded.stop();
