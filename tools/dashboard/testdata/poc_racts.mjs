// Стенд колонки действий строки доски (ветка poc-chat).
//
// В колонке стояли четыре кнопки разом: составная кнопка запуска с выбором
// подписки и яруса, чат, «Продолжить» и «Стоп». Колонка занимала 246 точек,
// больше, чем номер, ранг и дата вместе, а выбор подписки человек делает раз в
// десяток запусков. Кнопка теперь одна главная и всегда та, что нужна строке:
// у задачи с разговором это чат, у нетронутой очереди запуск. Рядом остаётся
// «Стоп» у идущей работы, остальное лежит под тремя точками.
//
// Предмет стенда: чем работает главная кнопка, виден ли стоп, закрывается ли
// меню тремя путями и не пропало ли со строки хоть одно действие.
//
// Зовётся: node testdata/poc_racts.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const tiers = [{ tier: "mini", model: "haiku" }, { tier: "base", model: "sonnet" },
  { tier: "pro", model: "opus" }, { tier: "max", model: "fable" }];
const harnesses = [
  { name: "claude-code", bin: "claude", default: true, models: tiers },
  { name: "glm-code", bin: "glm", models: [{ tier: "pro", model: "glm-5.3" }] },
];

const calls = [];
const { sandbox } = makeSandbox(app, (path, init) => {
  const way = init && init.method ? init.method : "GET";
  if (way !== "GET") calls.push({ way, path, body: init.body ? JSON.parse(init.body) : null });
  if (path === "/api/harnesses") return { harnesses };
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  if (path === "/api/quota") return { harnesses: [] };
  return { id: "XR-1", message: "готово" };
});
await settle();
await sandbox.loadHarnesses();

const fresh = { id: "XR-1", title: "нетронутая очередь", sect: "backlog" };
const talked = { id: "XR-2", title: "наша сессия кончилась", sect: "in-progress", run: "gone" };
const living = { id: "XR-3", title: "живой чат задачи", sect: "in-progress", run: "session" };
const busy = { id: "XR-4", title: "идёт конвейером", sect: "in-progress", run: "tmux" };

const main = (box) => byClass(box, "rmain");
const dots = (box) => byClass(box, "rdots");
const stop = (box) => byClass(box, "rstop");
const menu = (box) => byClass(box, "rmenu");
const chat = (box) => allByClass(box, "btn")
  .find((b) => String(b.attrs["aria-label"] || "").startsWith("Чат по задаче")) || null;
const press = (node) => node.handlers.click({ stopPropagation: () => {} });
const last = () => calls[calls.length - 1];

// --- главная кнопка меняется по состоянию строки ---
{
  const box = sandbox.rowAction("demo", fresh, "backlog");
  if (!main(box)) fail("у строки без разговора главная кнопка не запуск: " + dump(box));
  if (chat(box) === main(box)) fail("нетронутой очереди подана кнопка чата вместо запуска");
  if (String(main(box).attrs["aria-label"]) !== "Выполнить") {
    fail("запуск не назван подписью для чтения с экрана: " + main(box).attrs["aria-label"]);
  }
  for (const row of [talked, living, busy]) {
    const one = sandbox.rowAction("demo", row, "in-progress");
    if (main(one)) fail("у строки с разговором главной осталась кнопка запуска: " + row.id);
    const talk = chat(one);
    if (!talk) fail("у строки с разговором нет кнопки чата: " + dump(one));
    if (one.children[0] !== talk) {
      fail("чат у строки с разговором стоит не главной кнопкой: " + row.id);
    }
  }
}

// --- у идущей работы рядом с главной кнопкой стоит стоп ---
{
  const box = sandbox.rowAction("demo", busy, "in-progress");
  const off = stop(box);
  if (!off) fail("у идущей работы пропал стоп: " + dump(box));
  if (String(off.attrs["aria-label"]) !== "Стоп") {
    fail("стоп не назван подписью для чтения с экрана: " + off.attrs["aria-label"]);
  }
  // Прятать стоп в меню нельзя: жмут его, когда агент делает не то.
  if (menu(box) && dump(menu(box)).includes("Стоп")) fail("стоп уехал в меню");
  calls.length = 0;
  press(off);
  await settle();
  if (!last() || last().way !== "DELETE" || !last().path.includes("/runs/XR-4")) {
    fail("стоп пошёл не той ручкой: " + JSON.stringify(calls));
  }
  for (const row of [fresh, talked, living]) {
    if (stop(sandbox.rowAction("demo", row, row.sect))) {
      fail("строке без идущей работы предложен стоп: " + row.id);
    }
  }
}

// --- колонка держит две кнопки, а не четыре ---
{
  const wide = (key) => {
    const col = allByClass(sandbox.tblColgroup("tasks"), "cw-" + key)[0];
    const m = /,\s*(\d+)px\)/.exec(String(((col || {}).style || {}).width || ""));
    if (!m) fail("ширина колонки «" + key + "» не читается из colgroup");
    return Number(m[1]);
  };
  if (wide("act") > 110) fail("колонка действий шире рубежа: " + wide("act"));
  if (wide("act") > wide("rank") + wide("date")) {
    fail("колонка действий снова шире ранга с датой вместе: " + wide("act"));
  }
  for (const [row, sect] of [[fresh, "backlog"], [talked, "in-progress"],
    [living, "in-progress"], [busy, "in-progress"]]) {
    const box = sandbox.rowAction("demo", row, sect);
    // Считаются кнопки самой колонки: пункты меню лежат под тремя точками и
    // места в строке не занимают.
    const shown = allByClass(box, "btn").filter((b) => !byClass(menu(box) || box, b.className));
    if (allByClass(box, "btn").length > 2) {
      fail("в колонке снова больше двух кнопок (" + row.id + "): " +
        allByClass(box, "btn").map((b) => b.className).join(" | "));
    }
    if (!shown.length) fail("строка осталась вовсе без действия: " + row.id);
  }
}

// --- меню открывается и закрывается тремя путями ---
{
  const box = sandbox.rowAction("demo", fresh, "backlog");
  const btn = dots(box);
  if (!btn) fail("кнопки с тремя точками у строки нет: " + dump(box));
  if (!menu(box).hidden) fail("меню строки открыто до нажатия");
  // Повторное нажатие по своей кнопке.
  press(btn);
  if (menu(box).hidden) fail("меню не открылось нажатием");
  if (String(btn.attrs["aria-expanded"]) !== "true") fail("раскрытие не сказано читалке экрана");
  press(btn);
  if (!menu(box).hidden) fail("повторное нажатие меню не закрыло");
  // Клик мимо.
  press(btn);
  sandbox.document.handlers.click({ target: sandbox.document.body });
  if (!menu(box).hidden) fail("клик мимо меню не закрыл");
  // Escape.
  press(btn);
  sandbox.document.handlers.keydown({ key: "Escape", stopPropagation: () => {} });
  if (!menu(box).hidden) fail("Escape меню не закрыл");
  // Клик внутри меню его не закрывает: человек выбирает ярус и подписку подряд.
  press(btn);
  const tier = allByClass(menu(box), "tpick").find((t) => dump(t).trim() === "base");
  if (!tier) fail("выбора яруса в меню нет: " + dump(menu(box)));
  press(tier);
  sandbox.document.handlers.click({ target: tier });
  if (menu(box).hidden) fail("выбор яруса закрыл меню, а человек выбирает два ответа подряд");
}

// --- ни одно действие со строки не пропало ---
{
  // Очередь: запуск главной кнопкой, чат и выбор подписки с ярусом в меню.
  const box = sandbox.rowAction("demo", fresh, "backlog");
  calls.length = 0;
  press(main(box));
  await settle();
  if (!last() || last().way !== "POST" || !last().path.endsWith("/runs")) {
    fail("запуск главной кнопкой не ушёл: " + JSON.stringify(calls));
  }
  if (last().body.tier) fail("дашборд назвал ярус за вердикт: " + JSON.stringify(last().body));
  press(dots(box));
  const talk = deepBtn(menu(box), "Чат по задаче");
  if (!talk) fail("чата в меню очереди нет: " + dump(menu(box)));
  press(talk);
  await settle();
  if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes("XR-1")) {
    fail("чат из меню открыл не тот разговор: " + sandbox.location.hash);
  }
  // Подписка с ярусом: выбор яруса, потом строка подписки, и запуск уезжает
  // обоими ответами разом.
  const two = sandbox.rowAction("demo", fresh, "backlog");
  press(dots(two));
  press(allByClass(menu(two), "tpick").find((t) => dump(t).trim() === "max"));
  calls.length = 0;
  press(allByClass(menu(two), "hrow").find((h) => dump(h).includes("glm-code")));
  await settle();
  if (!last() || last().body.harness !== "glm-code" || last().body.tier !== "max") {
    fail("подписка с ярусом из меню не доехали до заказа: " + JSON.stringify(calls));
  }
  if (!menu(two).hidden) fail("запуск из меню его не закрыл");

  // Работа наша, но конвейером не идёт: продолжение лежит в меню и ходит своей
  // ручкой, а не заводит второго исполнителя запуском.
  for (const row of [talked, living]) {
    const one = sandbox.rowAction("demo", row, "in-progress");
    press(dots(one));
    const go = deepBtn(menu(one), "Продолжить");
    if (!go) fail("продолжения работы в меню нет: " + row.id + ", " + dump(menu(one)));
    calls.length = 0;
    press(go);
    await settle();
    if (!last() || !last().path.endsWith("/continue")) {
      fail("продолжение пошло не той ручкой: " + JSON.stringify(calls));
    }
    if (calls.some((c) => c.path.endsWith("/runs"))) {
      fail("продолжение завело второго исполнителя конвейером: " + row.id);
    }
  }
}

// --- строка, ждущая чужую задачу: кнопка гаснет с причиной, чат остаётся ---
{
  const box = sandbox.rowAction("demo",
    { id: "XR-5", title: "ждёт соседа", sect: "backlog", after: ["XR-1"] }, "backlog");
  const btn = main(box);
  if (!btn || !btn.disabled) fail("запуск заблокированной строки не погашен: " + dump(box));
  if (!String(btn.title || "").includes("сначала XR-1")) {
    fail("причина блока не названа подсказкой: " + btn.title);
  }
  press(dots(box));
  if (!deepBtn(menu(box), "Чат по задаче")) {
    fail("у заблокированной строки пропал чат: " + dump(menu(box)));
  }
}

// --- строка Check: подписка прикреплена, но выбор тот же, что у прочих ---
{
  const row = { id: "XR-6", title: "проверенная", sect: "check", accept: "mixed", harness: "glm-code" };
  const box = sandbox.rowAction("demo", row, "check");
  const btn = main(box);
  if (!btn || String(btn.attrs["aria-label"]) !== "Проверить и закрыть") {
    fail("у строки Check главная кнопка не приёмка: " + dump(box));
  }
  if (!String(btn.title || "").includes("glm-code") || !String(btn.title).includes("ваша приёмка")) {
    fail("подсказка приёмки не говорит ни про подписку, ни про приёмку: " + btn.title);
  }
  press(dots(box));
  // Прежде список подписок у строки Check снимался целиком, и меню отвечало на
  // один вопрос вместо двух: «выбор яруса и подписки различается по секциям»
  // (замечание пользователя). Прикрепление осталось умолчанием, а не запретом,
  // и подсвечена в списке та подписка, которой задачу вели.
  const list = allByClass(menu(box), "hrow");
  if (list.length !== 2) {
    fail("у строки Check список подписок не собрался: " + dump(menu(box)));
  }
  const lit = list.filter((r) => String(r.className).split(" ").includes("on"))
    .map((r) => dump(byClass(r, "hname")).trim());
  if (lit.join(",") !== "glm-code") {
    fail("у строки Check подсвечена не своя подписка: " + lit.join(","));
  }
}

console.log("poc_racts: ok");
