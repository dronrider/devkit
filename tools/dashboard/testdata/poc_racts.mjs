// Стенд колонки действий строки доски (ветка poc-chat).
//
// В колонке стояли четыре кнопки разом: составная кнопка запуска с выбором
// подписки и яруса, чат, «Продолжить» и «Стоп». Колонка занимала 246 точек,
// больше, чем номер, ранг и дата вместе, а выбор подписки человек делает раз в
// десяток запусков, и всё лишнее уехало под три точки.
//
// Следом выяснилось, что главную кнопку выбирало невидимое состояние строки: у
// одних строк In progress стоял чат, у других запуск, и разницу давала запись
// работы, которая на строке ничем не подписана («логика главной кнопки
// непонятна», замечание пользователя). Теперь место кнопки не зависит ни от
// чего: слева кнопка работы, справа кнопка разговора, дальше три точки.
//
// Предмет стенда: порядок кнопок один у всякой строки, кнопка работы делает то,
// что обещает подписью, чат стоит у каждой строки, меню закрывается тремя
// путями и ни одно действие со строки не пропало.
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
const check = { id: "XR-7", title: "на приёмке", sect: "check", accept: "human" };

const main = (box) => byClass(box, "rmain");
const dots = (box) => byClass(box, "rdots");
const stop = (box) => byClass(box, "rstop");
const menu = (box) => byClass(box, "rmenu");
const chat = (box) => allByClass(box, "btn")
  .find((b) => String(b.attrs["aria-label"] || "").startsWith("Чат по задаче")) || null;
const press = (node) => node.handlers.click({ stopPropagation: () => {} });
const last = () => calls[calls.length - 1];

// --- места кнопок одни и те же у всякой строки ---
//
// Первой стоит кнопка работы, второй кнопка разговора. Прежде первой была то
// одна, то другая, и решала это запись работы за строкой, которой на строке не
// видно.
{
  for (const [row, sect] of [[fresh, "backlog"], [talked, "in-progress"],
    [living, "in-progress"], [busy, "in-progress"], [check, "check"]]) {
    const box = sandbox.rowAction("demo", row, sect);
    const work = main(box) || stop(box);
    if (!work) fail("у строки нет кнопки работы: " + row.id + ", " + dump(box));
    if (box.children[0] !== work) {
      fail("кнопка работы стоит не первой: " + row.id + ", " + dump(box));
    }
    const talk = chat(box);
    if (!talk) fail("у строки нет кнопки разговора: " + row.id + ", " + dump(box));
    if (box.children[1] !== talk) {
      fail("кнопка разговора стоит не второй: " + row.id + ", " + dump(box));
    }
  }
}

// --- кнопка работы делает то, что обещает подписью ---
{
  // Нетронутая очередь: запуск конвейером.
  const box = sandbox.rowAction("demo", fresh, "backlog");
  if (String(main(box).attrs["aria-label"]) !== "Выполнить") {
    fail("запуск не назван подписью для чтения с экрана: " + main(box).attrs["aria-label"]);
  }
  // Строка Check зовётся своим словом: приёмка это не «Выполнить».
  const done = sandbox.rowAction("demo", check, "check");
  if (String(main(done).attrs["aria-label"]) !== "Проверить и закрыть") {
    fail("у строки Check кнопка работы названа не приёмкой: " + main(done).attrs["aria-label"]);
  }
  // Разговор за строкой уже есть: кнопка зовётся «Продолжить» и ходит своей
  // ручкой, а не заводит второго исполнителя конвейером (жалоба на DK-460).
  for (const row of [talked, living]) {
    const one = sandbox.rowAction("demo", row, "in-progress");
    if (String(main(one).attrs["aria-label"]) !== "Продолжить") {
      fail("у строки с разговором кнопка работы названа не продолжением: " + row.id);
    }
    calls.length = 0;
    press(main(one));
    await settle();
    if (!last() || !last().path.endsWith("/continue")) {
      fail("продолжение пошло не той ручкой: " + row.id + ", " + JSON.stringify(calls));
    }
    if (calls.some((c) => c.path.endsWith("/runs"))) {
      fail("продолжение завело второго исполнителя конвейером: " + row.id);
    }
  }
}

// --- у идущей работы кнопкой работы стоит стоп ---
{
  const box = sandbox.rowAction("demo", busy, "in-progress");
  const off = stop(box);
  if (!off) fail("у идущей работы пропал стоп: " + dump(box));
  if (box.children[0] !== off) fail("стоп стоит не на месте кнопки работы: " + dump(box));
  if (main(box)) fail("у идущей работы рядом со стопом остался запуск: " + dump(box));
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

// --- колонка держит три кнопки, а не четыре ---
{
  const wide = (key) => {
    const col = allByClass(sandbox.tblColgroup("tasks"), "cw-" + key)[0];
    const m = /,\s*(\d+)px\)/.exec(String(((col || {}).style || {}).width || ""));
    if (!m) fail("ширина колонки «" + key + "» не читается из colgroup");
    return Number(m[1]);
  };
  // Рубеж стоит по трём кнопкам значками с зазорами и боковыми отступами
  // ячейки: больше в колонке нечему стоять.
  if (wide("act") > 140) fail("колонка действий шире рубежа: " + wide("act"));
  if (wide("act") > wide("rank") + wide("date")) {
    fail("колонка действий снова шире ранга с датой вместе: " + wide("act"));
  }
  for (const [row, sect] of [[fresh, "backlog"], [talked, "in-progress"],
    [living, "in-progress"], [busy, "in-progress"]]) {
    const box = sandbox.rowAction("demo", row, sect);
    // Считаются кнопки самой колонки: пункты меню лежат под тремя точками и
    // места в строке не занимают.
    const shown = allByClass(box, "btn").filter((b) => !byClass(menu(box) || box, b.className));
    if (allByClass(box, "btn").length > 3) {
      fail("в колонке снова больше трёх кнопок (" + row.id + "): " +
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
  // Очередь: запуск кнопкой работы, разговор своей кнопкой, выбор подписки с
  // уровнем модели под тремя точками.
  const box = sandbox.rowAction("demo", fresh, "backlog");
  calls.length = 0;
  press(main(box));
  await settle();
  if (!last() || last().way !== "POST" || !last().path.endsWith("/runs")) {
    fail("запуск кнопкой работы не ушёл: " + JSON.stringify(calls));
  }
  if (last().body.tier) fail("дашборд назвал ярус за вердикт: " + JSON.stringify(last().body));
  // Пункт «Чат по задаче» из меню убран: он открывал ровно тот же разговор, что
  // и кнопка рядом, и человек считал его входом в новый чат (замечание
  // пользователя).
  press(dots(box));
  if (deepBtn(menu(box), "Чат по задаче")) {
    fail("чат вернулся вторым входом в меню: " + dump(menu(box)));
  }
  press(dots(box));
  press(chat(box));
  await settle();
  if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes("XR-1")) {
    fail("кнопка разговора открыла не тот чат: " + sandbox.location.hash);
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

  // Разговор открывается со всякой строки, чем бы она ни была занята: правило
  // «чат есть у каждой задачи» объясняется одной фразой.
  for (const [row, sect] of [[talked, "in-progress"], [living, "in-progress"],
    [busy, "in-progress"], [check, "check"]]) {
    const one = sandbox.rowAction("demo", row, sect);
    press(chat(one));
    await settle();
    if (!sandbox.location.hash.includes("chat/") || !sandbox.location.hash.includes(row.id)) {
      fail("кнопка разговора у строки " + row.id + " открыла не тот чат: " + sandbox.location.hash);
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
  // Разговор у неё остаётся своей кнопкой: строку нельзя запустить, но спросить
  // по ней есть о чём.
  if (!chat(box)) fail("у заблокированной строки пропал чат: " + dump(box));
  if (box.children[1] !== chat(box)) {
    fail("у заблокированной строки чат стоит не на своём месте: " + dump(box));
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
