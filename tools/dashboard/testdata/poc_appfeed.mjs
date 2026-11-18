import { makeSandbox, makeNode, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const output = Array.from({ length: 80 }, (_, i) => "строка вывода " + i).join("\n") + "\nполный хвост команды";
const command = "printf 'проверка\\n'";
const { sandbox } = makeSandbox(appPathArg(), () => ({}));

function hasText(node, wants, where) {
  const text = dump(node);
  for (const want of wants) {
    if (!text.includes(want)) fail(where + ": нет «" + want + "»: " + text);
  }
}

function commandReply(overrides = {}, method = "item/completed") {
  const item = {
    type: "commandExecution", id: "cmd-1", command,
    aggregatedOutput: output, status: "completed", exitCode: 0, durationMs: 0,
    cwd: "/tmp/app-feed-project", ...overrides,
  };
  return {
    role: "tool", tool: "Bash", note: item.command, text: item.aggregatedOutput,
    fail: item.status === "failed", sub: "child-thread",
    app: { method, threadId: "child-thread", turnId: "turn-1", item },
  };
}

function checkCommand(card, expectedOutput, wants) {
  const input = byClass(card, "tcmd");
  if (!input || input.textContent !== command) fail("App: потеряна команда: " + dump(card));
  const line = byClass(card, "tout");
  const pre = line && byClass(line, "ttext");
  if (!pre || pre.textContent !== expectedOutput) {
    fail("App: готовая Bash-карточка потеряла полный вывод: " + dump(card));
  }
  hasText(byClass(card, "thline"), wants, "метаданные команды App");
}

const completed = commandReply();
const completedCard = sandbox.toolPair(completed, null);
checkCommand(completedCard, output,
  ["Bash", "завершено", "код 0", "0 мс", "/tmp/app-feed-project", "child-thread"]);
const grow = deepBtn(completedCard, "foldar");
if (!grow) fail("App: у длинного вывода нет разворота");
grow.handlers.click({ stopPropagation: () => {} });
if (!byClass(completedCard, "tbox").classList.contains("open")) {
  fail("App: кнопка не развернула длинный вывод");
}

const failed = commandReply({ id: "cmd-2", status: "failed", exitCode: 17,
  durationMs: 1250, aggregatedOutput: "ошибка сборки\nхвост ошибки" });
checkCommand(sandbox.toolPair(failed, null), failed.text,
  ["ошибка", "код 17", "1250 мс"]);

const running = commandReply({ id: "cmd-3", status: "inProgress", exitCode: undefined,
  durationMs: undefined, aggregatedOutput: "первая порция" }, "item/started");
const runningCard = sandbox.toolPair(running, null);
checkCommand(runningCard, "первая порция", ["выполняется"]);
if (/код |undefined|NaN/.test(dump(runningCard))) {
  fail("App: у запущенной команды выдуманы метаданные: " + dump(runningCard));
}

const empty = sandbox.toolPair(commandReply({ aggregatedOutput: "" }), null);
hasText(empty, ["завершено", "код 0", "0 мс"], "пустой вывод команды App");
if (byClass(empty, "tout")) fail("App: пустой вывод нарисован отдельной строкой");

const legacy = sandbox.toolPair({ role: "tool", tool: "Bash", note: "ls",
  text: "command: ls" }, { role: "toolout", text: "старый вывод" });
hasText(legacy, ["ls", "старый вывод"], "прежняя пара Bash/toolout");

const oldApp = { role: "tool", tool: "Bash", key: "app:9", note: "ls", text: "старый полный вывод", fail: true };
hasText(sandbox.toolPair(oldApp, null), ["ls", "старый полный вывод"], "прежний вывод app-server");
if (sandbox.dotKind(oldApp, null) !== "bad") fail("App: прежняя ошибка потеряла цвет");
const sameOutput = sandbox.toolPair({ role: "tool", tool: "Bash", key: "app:10", note: "pwd", text: "pwd" }, null);
const sameLine = byClass(sameOutput, "tout");
if (!sameLine || byClass(sameLine, "ttext").textContent !== "pwd") {
  fail("App: старый вывод, совпавший с командой, скрыт");
}
const noResult = sandbox.toolPair({ role: "tool", tool: "Bash", note: "ls", text: "command: ls" }, null);
if (byClass(noResult, "tout")) fail("Обычный вызов Bash без ответа получил фиктивный вывод");

const spawn = {
  role: "tool", tool: "Agent", note: "проверка субагентом", text: "",
  sub: "worker-thread",
  app: { method: "item/completed", threadId: "parent-thread", turnId: "turn-2", item: {
    type: "collabAgentToolCall", id: "spawn-1", tool: "spawnAgent", status: "completed",
    prompt: "Проверь регрессию.\nВерни полный отчёт.", model: "test-model",
    reasoningEffort: "high", receiverThreadIds: ["worker-thread"], agentsStates: {},
  } },
};
const spawnCard = sandbox.toolPair(spawn, null);
hasText(spawnCard, ["Agent", "spawnAgent", "завершено", "test-model", "high",
  "worker-thread", "Проверь регрессию.", "Верни полный отчёт."], "задание субагенту App");
if (!byClass(spawnCard, "tbox")) fail("App: задание субагенту осталось без блока");

const wait = {
  role: "tool", tool: "Agent", note: "ожидание субагента", text: "", sub: "worker-thread",
  app: { method: "item/completed", threadId: "parent-thread", turnId: "turn-2", item: {
    type: "collabAgentToolCall", id: "wait-1", tool: "wait", status: "completed",
    receiverThreadIds: ["worker-thread"],
    agentsStates: { "worker-thread": { status: "completed", message: "Проверка закончена.\nДефект исправлен." } },
  } },
};
const waitCard = sandbox.toolPair(wait, null);
hasText(waitCard, ["Agent", "wait", "worker-thread", "завершено",
  "Проверка закончена.", "Дефект исправлен."], "ответ субагента App");
if (/undefined|NaN/.test(dump(waitCard))) fail("App: ожидание без prompt испорчено: " + dump(waitCard));

const activity = { role: "note", mark: "agent", note: "Субагент: завершено",
  text: "Итог субагента.\nВсе сценарии проверены.", sub: "worker-thread" };
const activityCard = sandbox.chatItem(activity);
hasText(activityCard, ["Субагент", "завершено", "Итог субагента.", "Все сценарии проверены."],
  "отчёт субагента App");
const reportBody = byClass(activityCard, "foldb");
const reportHead = byClass(activityCard, "foldh");
if (!reportBody || !reportHead || !reportBody.hidden) fail("App: отчёт субагента не свёрнут");
reportHead.handlers.click();
if (reportBody.hidden) fail("App: отчёт субагента не раскрылся");

const onlyShot = { role: "user", text: "", shots: ["/tmp/uploads/blank-source/one.png"] };
const manyShots = { role: "user", text: "Два снимка", shots: ["/tmp/uploads/blank-source/two.png", "/tmp/uploads/as-current/three.png"] };
const items = [completed, failed, spawn, wait, activity, onlyShot, manyShots].map((item, i) => ({
  ...item, seq: i, key: "app:" + i, time: "2026-10-01T10:00:00Z",
}));
const feed = makeNode("div");
sandbox.fetch = (path) => {
  const body = path.includes("/sessions/")
    ? { session: "parent-thread", head: { id: "parent-thread" }, items, total: items.length }
    : {};
  return Promise.resolve({ ok: true, status: 200,
    text: () => Promise.resolve(JSON.stringify(body)), json: () => Promise.resolve(body) });
};
sandbox.wireChatFeed("demo", feed, "parent-thread");
await settle();
const outputs = allByClass(feed, "ttext");
if (!outputs.some((node) => node.textContent === output)) {
  fail("App: полная лента потеряла вывод завершённой команды: " + dump(feed));
}
hasText(feed, ["ошибка", "код 17", "1250 мс", "spawnAgent", "Проверь регрессию.",
  "Проверка закончена.", "Итог субагента."], "лента App");
if (dump(feed).includes("не отрисовалась")) fail("App: карточка упала внутри ленты");

function checkShots(box) {
  const thumbs = allByClass(box, "mshot");
  if (thumbs.length !== 3) fail("App: снимок без слов или несколько снимков потерялись");
  if (thumbs[0].src !== "/api/projects/demo/chats/blank-source/shot?name=one.png" ||
      thumbs[1].src !== "/api/projects/demo/chats/blank-source/shot?name=two.png" ||
      thumbs[2].src !== "/api/projects/demo/chats/as-current/shot?name=three.png") {
    fail("App: владелец снимка заменён адресом новой нити");
  }
  thumbs[0].handlers.click();
  const lens = byClass(sandbox.document.body, "shotbig");
  if (!lens || lens.children[0].src !== thumbs[0].src) fail("App: просмотр снимка не открылся");
  sandbox.shotShut();
}
checkShots(feed);
const reloaded = makeNode("div");
sandbox.wireChatFeed("demo", reloaded, "parent-thread");
await settle();
checkShots(reloaded);

console.log("App: полный вывод команд, метаданные, задания и отчёты субагентов в карточках и ленте");
