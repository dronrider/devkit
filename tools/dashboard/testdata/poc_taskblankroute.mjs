import { makeSandbox, settle, tag, deepBtn, dump, fail, appPathArg } from "./poc_dom.mjs";

const SAID = "Нужно довести задачу до завершения.\nМакеты согласованы. Продолжай";
for (const state of ["live", "waiting", "gone"]) for (const outcome of ["reject", "tmux", "grown"]) {
  const reject = outcome === "reject";
  const board = { sections: [{ key: "blocked", rows: [{ id: "XR-4", sect: "blocked",
    waiting: { state: "ждёт ответа", note: "блок строки" } }] }] };
  const posts = [];
  const blank = { id: "blank-route", blank: true, state: "blank", project: "demo",
    tasks: ["XR-4"], model: "glm-5.3-flash", pickHarness: "glm-code-2", draft: SAID };
  const old = { id: "old-session", project: "demo", tasks: ["XR-4"], state, model: "opus" };
  const { sandbox, store, timers } = makeSandbox(appPathArg(), (path, init) => {
    if (init?.method === "POST") {
      const body = JSON.parse(init.body || "{}");
      posts.push({ path, body });
      if (path.endsWith("/chats") && reject) return { raw: { status: 502, text: '{"error":"подъём отказал"}' } };
      if (path.endsWith("/chats") && outcome === "grown") return { way: "new", session: "new-session" };
      if (path.endsWith("/chats")) return { way: "new", tmux: "chat-XR-4-2" };
      return {};
    }
    if (path.includes("?tmux=")) return { chats: [{ id: "new-session", project: "demo", state: "live", tasks: ["XR-4"] }] };
    if (path.includes("/chats")) return { chats: [blank, old], models: [] };
    return {};
  });
  const st = await sandbox.chatState("demo", blank.id, board);
  if (st.task !== "XR-4" || !st.wait) fail("стенд потерял ожидание задачи");
  const panel = sandbox.chatPanel("demo", st);
  const ta = tag(panel, "TEXTAREA");
  ta.value = SAID;
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation() {} });
  await settle();
  const starts = posts.filter(p => p.path === "/api/projects/demo/chats");
  if (starts.length !== 1) fail("первая реплика не подняла свой чат: " + JSON.stringify(posts));
  const sent = starts[0].body;
  if (sent.chat !== blank.id || sent.id !== "XR-4" || sent.text !== SAID ||
      sent.model !== "glm-5.3-flash" || sent.harness !== "glm-code-2") fail("адрес, текст или подписка потеряны: " + JSON.stringify(sent));
  if (posts.some(p => p.path.includes("/message") || p.path.includes("old-session/say"))) fail("отправка разбудила прежний чат");
  const pendingAt = id => JSON.parse(store.get("devkit.chat.pend.demo/" + id) || "[]");
  if (reject) {
    const pending = pendingAt(blank.id);
    if (pending.length !== 1 || pending[0].wire !== SAID || pending[0].state === "sent") fail("ошибка подъёма потеряла реплику или выдала доставку: " + JSON.stringify(pending));
    if (!SAID.split("\n").every(line => dump(panel).includes(line))) fail("после ошибки пропал текст реплики");
  } else if (outcome === "tmux") {
    const pending = pendingAt(blank.id);
    if (pending.length !== 1 || pending[0].tmux !== "chat-XR-4-2" || pending[0].state !== "wait") fail("подъём не держит реплику в ожидании своего окна: " + JSON.stringify(pending));
    const poll = timers.find(t => t.ms === 1500);
    if (!poll) fail("панель не ждёт новый разговор");
    poll.fn();
    await settle();
    const delivered = pendingAt("new-session");
    if (delivered.length !== 1 || delivered[0].wire !== SAID) fail("реплика не переехала в выбранный новый разговор: " + JSON.stringify(delivered));
    if (posts.some(p => p.path.includes("old-session"))) fail("подтверждение затронуло прежнюю сессию");
  }
  if (outcome === "grown" && pendingAt("new-session")[0]?.wire !== SAID) fail("повтор не открыл уже выросший разговор");
  if (sandbox.chatWay({ ...st, lift: "chat-XR-4-2" }).off !== true) fail("повтор доступен во время подъёма");
  if (sandbox.chatWay({ ...st, blank: "", fresh: false, sid: old.id, entry: old }).kind !== (state === "live" ? "say" : "task")) fail("изменился маршрут прежнего чата");
}
console.log("ok");
