// Стенд ручной глубины размышления (DK-1570).
//
// Уровень работы назначал вердикт agentctl pick, и дотянуться до иной глубины
// можно было только словами в самой команде. Теперь у всякого запуска есть
// общее меню модели и глубины: у кнопки пуска, у трёх точек строки и у выбора
// модели в разговоре. Предмет стенда это состав того меню и проезд выбора:
// уровень доехал до заказа запуска и до тела реплики, а пустой выбор оставил
// заказ вердикту. Выбор живёт один запуск, слова в команде остаются сильнее.
//
// Зовётся: node testdata/poc_effmenu.mjs static/app.js

import { makeSandbox, settle, dump, tag, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const tiers = [{ tier: "mini", model: "haiku" }, { tier: "base", model: "sonnet" },
  { tier: "pro", model: "opus" }];
const harnesses = [
  { name: "claude-code", bin: "claude", default: true, models: tiers },
  { name: "glm-code", bin: "glm", models: [{ tier: "pro", model: "glm-5.3" }] },
];
const quota = { harnesses: [
  { name: "claude-code", age: "2 мин", age_sec: 120, buckets: [
    { name: "window5h_all", used_pct: 62 },
    { name: "week_all", used_pct: 18 },
  ] },
] };

const chats = [
  { id: "bbbb2222-2222", title: "Верни XR-1 на доработку", mtime: "2026-08-12T09:00:00+03:00",
    tasks: ["XR-1"], model: "opus", state: "dead" },
];
const chatModels = tiers.map((m) => ({ model: m.model, tier: m.tier, harness: "claude-code" }));
const board = { sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "панель разговора", sect: "in-progress" },
] }] };

const posted = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    if (path.endsWith("/runs")) return { id: "XR-1", session: "task-XR-1", message: "конвейер поднят" };
    if (path.includes("/say")) return { way: "resume", tmux: "chat-XR-1-2" };
    return {};
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/quota") return quota;
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.endsWith("/pick")) return { tier: "base", via: "строка доски", effort: "high" };
  if (path.includes("/chats")) return { chats, models: chatModels };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items: [], total: 0 };
  }
  if (path.endsWith("/board")) return { board, works: [] };
  return {};
});
await settle();
await sandbox.loadHarnesses();
await sandbox.refreshQuota();
await settle();

const task = { id: "XR-1", title: "панель разговора", sect: "backlog", r: 30, cost: "-" };
const openRowMenu = () => {
  const box = sandbox.rowAction("demo", task, "backlog");
  const dots = byClass(box, "rdots");
  dots.handlers.click({ stopPropagation: () => {} });
  return { box, pop: byClass(box, "rmenu") };
};
const last = () => posted[posted.length - 1];
const markOn = (pop) => allByClass(pop, "effmark")
  .filter((b) => String(b.className).includes("on")).map((b) => dump(b).trim());

// --- состав меню: строки моделей с чипами и метрами, ползунок из пяти меток ---
{
  const { pop } = openRowMenu();
  if (!pop) fail("у строки нет меню модели и глубины");
  const text = dump(pop).replace(/\s+/g, " ");
  for (const want of ["Модель", "Глубина размышления",
    "haiku mini, claude-code", "opus pro, claude-code", "glm-5.3 pro, glm-code"]) {
    if (!text.includes(want)) fail("в меню нет «" + want + "»: " + text);
  }
  // Подписка со снимком несёт обе полоски, подписка без снимка ни одной:
  // пустая полоска читалась бы нулём, а ноль у остатка это исчерпанный лимит.
  const rows = allByClass(pop, "hrow");
  const rich = rows.find((h) => dump(h).includes("opus"));
  const widths = allByClass(rich, "meter").map((m) => m.children[0].style.width);
  if (JSON.stringify(widths) !== JSON.stringify(["62%", "18%"])) {
    fail("полоски окна и недели не доехали до строки: " + JSON.stringify(widths));
  }
  const bare = rows.find((h) => dump(h).includes("glm"));
  if (allByClass(bare, "meter").length) {
    fail("подписка без снимка рисует полоски: " + dump(bare));
  }
  const levels = allByClass(pop, "effmark").map((b) => dump(b).trim());
  if (JSON.stringify(levels) !== JSON.stringify(["low", "medium", "high", "xhigh", "max"])) {
    fail("шкала глубины не из пяти уровней: " + JSON.stringify(levels));
  }
}

// --- подпись про ярус без effort появляется при выборе его строки ---
{
  const { pop } = openRowMenu();
  const note = byClass(pop, "effnote");
  if (!note || !note.hidden) fail("подпись про mini стоит без выбора mini-строки");
  allByClass(pop, "hrow").find((h) => dump(h).includes("haiku"))
    .handlers.click({ stopPropagation: () => {} });
  if (note.hidden) fail("выбор mini-строки не показал подпись: " + dump(pop));
  if (!dump(note).includes("про effort в API не знает")) {
    fail("подпись не называет судьбу уровня словами: " + dump(note));
  }
  allByClass(pop, "hrow").find((h) => dump(h).includes("opus"))
    .handlers.click({ stopPropagation: () => {} });
  if (!note.hidden) fail("подпись mini осталась после ухода с mini-строки: " + dump(note));
}

// --- кольцо вердикта и бегунок на нём без выбора человека ---
{
  const { pop } = openRowMenu();
  await settle();
  const verd = allByClass(pop, "effdot").filter((d) => String(d.className).includes("verd"))
    .map((d) => d.dataset.level);
  if (JSON.stringify(verd) !== JSON.stringify(["high"])) {
    fail("кольцо вердикта не встало на шкалу: " + JSON.stringify(verd));
  }
  if (JSON.stringify(markOn(pop)) !== JSON.stringify(["high"])) {
    fail("без выбора человека бегунок стоит не на вердикте: " + JSON.stringify(markOn(pop)));
  }
}

// --- выбранный рукой уровень доезжает до заказа запуска и живёт один запуск ---
{
  const { box, pop } = openRowMenu();
  allByClass(pop, "hrow").find((h) => dump(h).includes("glm"))
    .handlers.click({ stopPropagation: () => {} });
  allByClass(pop, "effmark").find((b) => dump(b).trim() === "max")
    .handlers.click({ stopPropagation: () => {} });
  byClass(box, "rmain").handlers.click({ stopPropagation: () => {} });
  await settle();
  const body = last().body;
  if (!body || body.harness !== "glm-code" || body.tier !== "pro" || body.effort !== "max") {
    fail("выбор модели и глубины не доехал до заказа: " + JSON.stringify(body));
  }
  // Подсветка ушла с меню вместе с выбором: бегунок вернулся на вердикт, и
  // второй запуск едет вердиктом.
  if (JSON.stringify(markOn(pop)) !== JSON.stringify(["high"])) {
    fail("после запуска шкала держит ручной выбор: " + JSON.stringify(markOn(pop)));
  }
  byClass(box, "rmain").handlers.click({ stopPropagation: () => {} });
  await settle();
  const again = last().body;
  if (again.tier || again.effort || again.harness) {
    fail("выбор пережил свой запуск: " + JSON.stringify(again));
  }
}

// --- клик по дорожке округляется до ближайшей ступени ---
{
  const { box, pop } = openRowMenu();
  const track = byClass(pop, "efftrack");
  track.getBoundingClientRect = () => ({ left: 0, top: 0, bottom: 0, height: 0, width: 100 });
  track.handlers.click({ clientX: 96, stopPropagation: () => {} });
  if (JSON.stringify(markOn(pop)) !== JSON.stringify(["max"])) {
    fail("клик по дорожке не округлился до max: " + JSON.stringify(markOn(pop)));
  }
  byClass(box, "rmain").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (last().body.effort !== "max") {
    fail("уровень с дорожки не доехал до заказа: " + JSON.stringify(last().body));
  }
}

// --- меню разговора: открывается выбором модели, глубина едет в реплику ---
{
  const st = await sandbox.chatState("demo", "bbbb2222-2222", board);
  const panel = sandbox.chatPanel("demo", st);
  const sel = tag(panel, "SELECT");
  if (!sel) fail("выбора модели в панели разговора нет");
  const menu = byClass(panel, "menu");
  if (!menu) fail("меню модели и глубины не построено у выбора модели");
  sel.handlers.mousedown({ preventDefault() {}, stopPropagation() {} });
  if (menu.hidden) fail("нажатие на выбор модели не открыло меню");
  // Повторное нажатие закрывает: меню не висит над строкой реплики.
  sel.handlers.mousedown({ preventDefault() {}, stopPropagation() {} });
  if (!menu.hidden) fail("повторное нажатие не закрыло меню");
  sel.handlers.mousedown({ preventDefault() {}, stopPropagation() {} });
  if (menu.hidden) fail("меню не открылось повторно");

  const ta = tag(panel, "TEXTAREA");
  ta.value = "продолжай с глубиной";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const said = posted.filter((p) => p.path.includes("/say")).pop();
  if (said && said.body.effort) {
    fail("глубину повезло в реплику без выбора человека: " + JSON.stringify(said.body));
  }
  sel.handlers.mousedown({ preventDefault() {}, stopPropagation() {} });
  allByClass(menu, "effmark").find((b) => dump(b).trim() === "xhigh")
    .handlers.click({ stopPropagation: () => {} });
  ta.value = "теперь с глубиной";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const withDepth = posted.filter((p) => p.path.includes("/say")).pop();
  if (!withDepth || withDepth.body.effort !== "xhigh") {
    fail("ручная глубина не доехала до реплики: " + JSON.stringify(withDepth && withDepth.body));
  }
  // Выбор потрачен подъёмом: следующая реплика едет вердиктом.
  ta.value = "а теперь без";
  deepBtn(panel, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  const plain = posted.filter((p) => p.path.includes("/say")).pop();
  if (plain.body.effort) {
    fail("выбор глубины пережил свой запуск в разговоре: " + JSON.stringify(plain.body));
  }
}

console.log("poc_effmenu: ok");
