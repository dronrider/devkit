// Стенд ручной глубины размышления (DK-1570).
//
// Уровень работы назначал вердикт agentctl pick, и дотянуться до иной глубины
// можно было только словами в самой команде. Теперь у всякого запуска есть
// общее меню модели и глубины: у кнопки пуска, у трёх точек строки и у выбора
// модели в разговоре. Предмет стенда это состав того меню и проезд выбора:
// уровень доехал до заказа запуска и до тела реплики, а пустой выбор оставил
// заказ вердикту. Выбор живёт один запуск, слова в команде остаются сильнее,
// а неудачный подъём выбор не съедает.
//
// Зовётся: node testdata/poc_effmenu.mjs static/app.js

import { makeSandbox, settle, dump, tag, byClass, allByClass, deepBtn, fail, appPathArg,
  autoBox, autoSet }
  from "./poc_dom.mjs";

const app = appPathArg();

const tiers = [{ tier: "mini", model: "haiku", levels: ["low", "medium", "high", "xhigh"] },
  { tier: "base", model: "sonnet", levels: ["low", "medium", "high", "xhigh"] },
  { tier: "pro", model: "opus", levels: ["low", "medium", "high", "xhigh"] }];
const harnesses = [
  { name: "claude-code", bin: "claude", default: true, models: tiers },
  { name: "glm-code", bin: "glm",
    models: [{ tier: "pro", model: "glm-5.3", levels: ["low", "high", "max"] }] },
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
  // Незачатый разговор: сессии за ним нет, и поднимет его первая реплика.
  { id: "blank-eff", project: "demo", blank: true, state: "not-started", idle: true,
    model: "opus", pickHarness: "claude-code", mtime: "2026-08-12T09:30:00+03:00", tasks: [] },
];
const chatModels = tiers.map((m) => ({ model: m.model, tier: m.tier, harness: "claude-code" }))
  .concat([{ model: "glm-5.3", tier: "pro", harness: "glm-code" }]);
const board = { sections: [{ key: "in-progress", rows: [
  { id: "XR-1", title: "панель разговора", sect: "in-progress" },
] }] };

const posted = [];
let raiseFail = false;
const { sandbox } = makeSandbox(app, (path, init) => {
  if (init && init.method === "POST") {
    posted.push({ path, body: init.body ? JSON.parse(init.body) : null });
    if (path.endsWith("/runs")) return { id: "XR-1", session: "task-XR-1", message: "конвейер поднят" };
    if (path.includes("/say")) return { way: "resume", tmux: "chat-XR-1-2" };
    // Подъём незачатого разговора падает по желанию стенда: неудачная дорога
    // тут предмет проверки, а не помеха прогону.
    if (path.endsWith("/chats")) {
      if (raiseFail) return { raw: { status: 502, statusText: "Bad Gateway",
        text: JSON.stringify({ error: "чат не поднялся" }) } };
      return { way: "new", tmux: "chat-eff-1" };
    }
    return {};
  }
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/quota") return quota;
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.endsWith("/pick")) return { tier: "base", via: "claude-code", effort: "high" };
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

// --- состав меню: группы подписок с квотами, модели внутри, ползунок ступеней ---
{
  const { pop } = openRowMenu();
  if (!pop) fail("у строки нет меню модели и глубины");
  const text = dump(pop).replace(/\s+/g, " ");
  for (const want of ["Модель", "Автоматически", "Глубина размышления",
    "claude-code", "glm-code", "haiku mini", "opus pro", "glm-5.3 pro"]) {
    if (!text.includes(want)) fail("в меню нет «" + want + "»: " + text);
  }
  // Переключатель стоит в первой строке списка моделей и включён по умолчанию:
  // форма задачи открывается парой машины, а не ручным выбором (DK-1570).
  const box = autoBox(pop);
  if (!box) fail("в меню формы задачи нет переключателя «Автоматически»");
  if (!box.checked) fail("переключатель «Автоматически» открылся снятым");
  if (!byClass(pop, "is-auto")) fail("меню не помечено включённым переключателем: " + dump(pop));
  // Первый уровень это подписки: по строке группы на подписку из ответа ручки,
  // полоски квот стоят на группе, а у строк моделей их больше нет.
  const grows = allByClass(pop, "grow");
  if (grows.length !== 2) fail("групп подписок в меню не две: " + grows.length);
  const rows = allByClass(pop, "hrow");
  if (rows.length !== 4) fail("строк моделей в меню не четыре: " + rows.length);
  if (rows.some((r) => allByClass(r, "meter").length)) {
    fail("полоски квот вернулись на строку модели: " + dump(rows[0]));
  }
  // Меню открывается с развёрнутой группой текущей пары, точка стоит у её имени.
  const opened = grows.filter((g) => String(g.className).includes("open"));
  if (opened.length !== 1 || !dump(opened[0]).includes("claude-code")) {
    fail("меню открылось не с группой пары машины: " +
      JSON.stringify(grows.map((g) => dump(g))));
  }
  const held = grows.filter((g) => String(g.className).includes("hold"));
  if (held.length !== 1 || !dump(held[0]).includes("claude-code")) {
    fail("точка текущей пары стоит не у группы подписки по умолчанию: " +
      JSON.stringify(grows.map((g) => dump(g))));
  }
  // Подписка со снимком несёт обе полоски на строке группы, подписка без
  // снимка ни одной: пустая полоска читалась бы нулём, а ноль у остатка это
  // исчерпанный лимит.
  const rich = grows.find((g) => dump(g).includes("claude-code"));
  const widths = allByClass(rich, "meter").map((m) => m.children[0].style.width);
  if (JSON.stringify(widths) !== JSON.stringify(["62%", "18%"])) {
    fail("полоски окна и недели не доехали до строки группы: " + JSON.stringify(widths));
  }
  const bare = grows.find((g) => dump(g).includes("glm-code"));
  if (allByClass(bare, "meter").length) {
    fail("подписка без снимка рисует полоски: " + dump(bare));
  }
  if (!String(bare.title || "").includes("снимка нет")) {
    fail("строка группы не говорит про снимок: " + bare.title);
  }
  const levels = allByClass(pop, "effmark").map((b) => dump(b).trim());
  if (JSON.stringify(levels) !== JSON.stringify(["low", "medium", "high", "xhigh", "max"])) {
    fail("шкала глубины не из пяти уровней: " + JSON.stringify(levels));
  }
}

// --- свёртка по подписке: развёрнутая группа одна, выбор её не сворачивает ---
{
  const { pop } = openRowMenu();
  await settle();
  const grows = allByClass(pop, "grow");
  const glmGrp = grows.find((g) => dump(g).includes("glm-code"));
  const ccGrp = grows.find((g) => dump(g).includes("claude-code"));
  const isOpen = (g) => String(g.className).includes("open");
  // При включённом переключателе группы погашены и не нажимаются: клик по
  // свёрнутой подписке ничего не раскрывает.
  glmGrp.handlers.click({ stopPropagation: () => {} });
  if (isOpen(glmGrp)) fail("включённый переключатель пустил руку к группам подписок");
  if (!String(ccGrp.className).includes("verdgrp")) {
    fail("группа пары машины не осталась яркой среди погашенных: " + ccGrp.className);
  }
  autoSet(pop, false);
  // Клик по группе раскрывает её модели и сворачивает прежнюю группу.
  glmGrp.handlers.click({ stopPropagation: () => {} });
  if (!isOpen(glmGrp) || isOpen(ccGrp)) {
    fail("клик по подписке не сменил раскрытую группу: " +
      JSON.stringify(grows.map((g) => g.className)));
  }
  // Выбор строки моделей группу не сворачивает и меню не закрывает.
  const glmRow = allByClass(pop, "hrow").find((h) => dump(h).includes("glm-5.3"));
  glmRow.handlers.click({ stopPropagation: () => {} });
  if (!isOpen(glmGrp)) fail("выбор модели свернул её группу");
  if (pop.hidden) fail("выбор модели закрыл меню до выбора глубины");
  // Точка уехала на группу ручного выбора.
  if (!String(glmGrp.className).includes("hold") ||
    String(ccGrp.className).includes("hold")) {
    fail("точка текущей пары не уехала на группу выбора: " +
      JSON.stringify(grows.map((g) => g.className)));
  }
  // Повторный клик по раскрытой группе сворачивает её, и раскрытых не остаётся.
  glmGrp.handlers.click({ stopPropagation: () => {} });
  if (grows.some(isOpen)) fail("повторный клик не свернул раскрытую группу");
  ccGrp.handlers.click({ stopPropagation: () => {} });
  if (!isOpen(ccGrp) || isOpen(glmGrp)) fail("соседняя подписка раскрылась поверх прежней");
}

// --- включённый переключатель не пускает руку к строкам и шкале ---
{
  const { box, pop } = openRowMenu();
  const before = markOn(pop);
  allByClass(pop, "grow").find((h) => dump(h).includes("glm"))
    .handlers.click({ stopPropagation: () => {} });
  allByClass(pop, "hrow").find((h) => dump(h).includes("glm"))
    .handlers.click({ stopPropagation: () => {} });
  allByClass(pop, "effmark").find((b) => dump(b).trim() === "max")
    .handlers.click({ stopPropagation: () => {} });
  if (JSON.stringify(markOn(pop)) !== JSON.stringify(before)) {
    fail("при включённом переключателе рука сдвинула глубину: " + JSON.stringify(markOn(pop)));
  }
  // Снятый переключатель возвращает ручной выбор, и он снова доезжает до дела.
  autoSet(pop, false);
  if (byClass(pop, "is-auto")) fail("снятый переключатель оставил меню в автоматическом виде");
  const openGrp = (name) => allByClass(pop, "grow").find((h) => dump(h).includes(name))
    .handlers.click({ stopPropagation: () => {} });
  openGrp("glm");
  allByClass(pop, "hrow").find((h) => dump(h).includes("glm"))
    .handlers.click({ stopPropagation: () => {} });
  allByClass(pop, "effmark").find((b) => dump(b).trim() === "max")
    .handlers.click({ stopPropagation: () => {} });
  if (JSON.stringify(markOn(pop)) !== JSON.stringify(["max"])) {
    fail("снятый переключатель не вернул ручной выбор: " + JSON.stringify(markOn(pop)));
  }
  // Обратно включённый переключатель снимает ручную пару и возвращает шкалу
  // пары машины.
  autoSet(pop, true);
  if (!byClass(pop, "is-auto")) fail("включённый переключатель не пометил меню: " + dump(pop));
  if (JSON.stringify(markOn(pop)) !== JSON.stringify(["high"])) {
    fail("включённый переключатель не вернул бегунок к вердикту: " + JSON.stringify(markOn(pop)));
  }
  byClass(box, "rmain").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (last().body.tier || last().body.effort || last().body.harness) {
    fail("включённый переключатель назвал за человека пару: " + JSON.stringify(last().body));
  }
}

// --- шкала следует за выбранной подпиской: у семей ступени свои (v4) ---
{
  const { pop } = openRowMenu();
  autoSet(pop, false);
  allByClass(pop, "grow").find((h) => dump(h).includes("glm"))
    .handlers.click({ stopPropagation: () => {} });
  allByClass(pop, "hrow").find((h) => dump(h).includes("glm"))
    .handlers.click({ stopPropagation: () => {} });
  const glm = allByClass(pop, "effmark").map((b) => dump(b).trim());
  if (JSON.stringify(glm) !== JSON.stringify(["low", "high", "max"])) {
    fail("шкала glm не из трёх ступеней: " + JSON.stringify(glm));
  }
  allByClass(pop, "grow").find((h) => dump(h).includes("claude-code"))
    .handlers.click({ stopPropagation: () => {} });
  allByClass(pop, "hrow").find((h) => dump(h).includes("opus"))
    .handlers.click({ stopPropagation: () => {} });
  const ant = allByClass(pop, "effmark").map((b) => dump(b).trim());
  if (JSON.stringify(ant) !== JSON.stringify(["low", "medium", "high", "xhigh"])) {
    fail("шкала anthropic не из четырёх ступеней: " + JSON.stringify(ant));
  }
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
  autoSet(pop, false);
  allByClass(pop, "grow").find((h) => dump(h).includes("glm"))
    .handlers.click({ stopPropagation: () => {} });
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
  // Переключатель вернулся включённым не только классом, но и отметкой: снятая
  // галочка при автоматическом меню читалась бы ручным выбором (DK-1570).
  if (!autoBox(pop).checked) {
    fail("после запуска переключатель остался снятым на вид");
  }
  if (!byClass(pop, "is-auto")) fail("после запуска меню осталось в ручном виде");
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
  autoSet(pop, false);
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
  // В разговоре переключателя нет: там модель и глубину выбирают руками
  // (DK-1570, замечание пользователя).
  if (autoBox(menu)) fail("в меню разговора появился переключатель «Автоматически»");
  // Свёртка в разговоре та же: группы подписок, развёрнута группа выбранной
  // модели (DK-1570, макет свёртки).
  const chatGrows = allByClass(menu, "grow");
  if (chatGrows.length !== 2) fail("в меню разговора групп подписок не две: " + chatGrows.length);
  const chatOpen = chatGrows.filter((g) => String(g.className).includes("open"));
  if (chatOpen.length !== 1 || !dump(chatOpen[0]).includes("claude-code")) {
    fail("в меню разговора развёрнута не группа выбранной модели: " +
      JSON.stringify(chatGrows.map((g) => dump(g))));
  }
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

// --- неудачный подъём не съедает ручную глубину ---
{
  // Отказ ручки подъёма не поднял запуска, и выбор человека обязан пережить
  // его: следующая попытка едет тем же уровнем, а тратит выбор только
  // удалась попытка (слово way из ответа).
  const st = await sandbox.chatState("demo", "blank-eff", board);
  const panel = sandbox.chatPanel("demo", st);
  const sel = tag(panel, "SELECT");
  const menu = byClass(panel, "menu");
  if (!menu) fail("у незачатого разговора нет меню модели и глубины");
  sel.handlers.mousedown({ preventDefault() {}, stopPropagation() {} });
  allByClass(menu, "effmark").find((b) => dump(b).trim() === "max")
    .handlers.click({ stopPropagation: () => {} });
  const raises = () => posted.filter((p) => p.path.endsWith("/chats"));
  const say = (text) => {
    sandbox.chatRaise("demo", st, text, { model: "opus", harness: "claude-code" }, () => {})
      .catch(() => {});
  };
  raiseFail = true;
  say("поднимись с глубиной");
  await settle();
  if (!raises().length || raises()[0].body.effort !== "max") {
    fail("ручная глубина не уехала с попыткой подъёма: " + JSON.stringify(raises()));
  }
  raiseFail = false;
  say("ещё раз с глубиной");
  await settle();
  const retry = raises()[1];
  if (!retry || retry.body.effort !== "max") {
    fail("неудачный подъём съел ручную глубину: " + JSON.stringify(raises()));
  }
  say("третья реплика");
  await settle();
  if (raises()[2] && raises()[2].body.effort) {
    fail("удачный подъём не потратил выбор: " + JSON.stringify(raises()[2].body));
  }
}

// --- мини-полоска глубины в поле подписки показывает ручной выбор ---
{
  const st = await sandbox.chatState("demo", "bbbb2222-2222", board);
  const panel = sandbox.chatPanel("demo", st);
  const mbar = byClass(panel, "mbar");
  if (!mbar) fail("у поля модели в разговоре нет мини-полоски глубины");
  if (!mbar.hidden) fail("без ручного выбора мини-полоска не спрятана: " + dump(panel));
  const sel = tag(panel, "SELECT");
  const menu = byClass(panel, "menu");
  sel.handlers.mousedown({ preventDefault() {}, stopPropagation() {} });
  allByClass(menu, "effmark").find((b) => dump(b).trim() === "high")
    .handlers.click({ stopPropagation: () => {} });
  if (mbar.hidden) fail("выбор глубины не показал мини-полоску: " + dump(panel));
  if (mbar.dataset.level !== "high") {
    fail("мини-полоска называет не выбранный уровень: " + mbar.dataset.level);
  }
}

// --- чип глубины рядом с приоритетом рисуется из записи этапа ---
{
  const chip = sandbox.effortChip({ effort: "high" });
  if (!chip || !String(chip.className).includes("chip lvl")) {
    fail("у строки с уровнем нет чипа глубины: " + dump(chip));
  }
  if (dump(chip).trim() !== "high") {
    fail("чип глубины называет не уровень: " + dump(chip));
  }
  if (sandbox.effortChip({}) || sandbox.effortChip({ effort: "" })) {
    fail("чип глубины нарисовался без записи уровня");
  }
}

console.log("poc_effmenu: ok");
