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
const plain = sandbox.fetch;
sandbox.fetch = (path, init) => {
  if (init?.method === "POST" && path.includes("/say")) {
    tries += 1;
    return Promise.resolve({ ok: false, status: 409,
      text: () => Promise.resolve(JSON.stringify({ error: reason, refused: reason })) });
  }
  return plain(path, init);
};
const st = await sandbox.chatState("demo", sid, board);
const panel = sandbox.chatPanel("demo", st);
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
console.log("appchat_conflict: ok");
