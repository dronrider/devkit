// Стенд вопроса клиента в панели (ветка poc-chat).
//
// Клиент, поднятый в незнакомом каталоге, встаёт на вопросе о доверии («Yes, I
// trust this folder»), а следом на вопросе про внешние импорты правил, и до
// ответа не делает ни хода. Человек этих вопросов не видел вовсе: лента
// пустая, реплика висит недоставленной, а ответить можно было только руками в
// tmux (замечание пользователя: «не хочу каждый раз чинить что-то через
// тебя»). Предмет стенда: вопрос приходит в панель блоком с кнопками, нажатие
// шлёт ответ клиенту и ожидания не роняет, а спрашивать нечего, значит блока
// нет вовсе.
//
// Тот же блок показывает и опрос агента с вариантами (живой случай: агент
// спросил в сессии chat-2, панель не показала вопрос вовсе, человек писал
// реплики, а клиент их не читал, потому что ждал выбора). Вид опроса разобран
// по снимку пользователем и переделан: шаги это табы, по которым ходят
// свободно, варианты идут списком с пояснениями, свободный ответ живёт своей
// строкой под списком и только после выбора, слова кнопок русские, а сводку
// ответов дашборд проходит сам.
//
// Зовётся: node testdata/poc_clientask.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID = "aaaa1111-1111-4111-8111-111111111111";
const ask = {
  text: "Accessing workspace: /Users/rider/projects/xr-proxy Quick safety check: " +
    "Is this a project you created or one you trust?",
  options: [{ text: "Yes, I trust this folder" }, { text: "No, exit" }],
  at: 1,
  keys: "digit",
};

// Опрос агента, как его отдаёт разбор живой панели: шаги табами, варианты с
// пояснениями и флажками, свободный ответ, кнопки самого виджета.
const poll = {
  text: "Где именно MAX ломается под прокси?",
  options: [
    { text: "На телефоне (Android-клиент)", mark: "off", desc: "Туннель поднят приложением" },
    { text: "На устройствах за роутером", mark: "off", desc: "OpenWRT с перехватом TCP" },
    { text: "Везде одинаково", mark: "on", desc: "И там, и там" },
    { text: "Type something", mark: "off", kind: "free" },
    { text: "Next", kind: "next" },
    { text: "Chat about this", kind: "chat" },
  ],
  at: 1,
  keys: "arrows",
  steps: [{ name: "Место", now: true }, { name: "Симптом" }, { name: "Сроки", done: true }],
};

// Следующий шаг опроса: приходит переходом по табу либо ответом.
const pollNext = {
  text: "Что именно происходит с MAX?",
  options: [
    { text: "Не открываются картинки", mark: "off" },
    { text: "Звонки не соединяются", mark: "on" },
    { text: "Next", kind: "next" },
  ],
  at: 1,
  keys: "arrows",
  steps: [{ name: "Место", done: true }, { name: "Симптом", now: true }, { name: "Сроки" }],
};

// Сводка опроса: её виджет показывает, когда отвечено не всё. Отвечено всё,
// значит сводку проходит сервер, и до панели она не доезжает вовсе.
const review = {
  kind: "review",
  warn: "You have not answered all questions",
  said: [{ q: "Что именно?", a: "Картинки" }, { q: "Когда началось?", a: "Вчера" }],
  options: [{ text: "Submit answers", kind: "submit" }, { text: "Cancel" }],
  at: 1,
  keys: "digit",
  steps: [{ name: "Место" }, { name: "Симптом", done: true }, { name: "Сроки", done: true, now: true }],
};

// Что отдаёт ручка вопроса и что у неё спросили: стенд правит первое и смотрит
// второе.
const now = { ask, orders: [], next: null };

const { sandbox, timers } = makeSandbox(app, (path, init) => {
  if (path.includes("/ask") && init && init.method === "POST") {
    now.orders.push(JSON.parse(init.body));
    if (now.next) {
      now.ask = now.next;
      now.next = null;
    }
    return { message: "" };
  }
  if (path.includes("/ask")) return now.ask ? { session: SID, tmux: "chat-2", ask: now.ask }
    : { session: SID, tmux: "chat-2", note: "клиент chat-2 ни о чём не спрашивает" };
  if (path.includes("/sessions/")) return { items: [], start: true };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const st = { addr: SID, sid: SID, project: "demo", chats: [], entry: { id: SID, state: "live",
  tmux: "chat-2", idle: true }, models: [] };

// Панель поднимается один раз на стенд: предмет проверки в том числе и то, что
// блок правится по месту, а не пересобирается вместе с лентой.
const panel = sandbox.chatPanel("demo", st);
await settle();
const boxOf = () => byClass(panel, "cask");
const optsOf = () => allByClass(boxOf(), "caskopt");
const tabsOf = () => allByClass(boxOf(), "cstep");
// Ход панели после нажатия: она перечитывает снимок отложенным заходом.
const settleMove = async () => {
  await settle();
  for (const t of timers.splice(0)) t.fn();
  await settle();
};
// Подмена вопроса на стенде: панель перечитывает снимок сама, но только пока
// вопроса нет, поэтому смена вида идёт через паузу молчания клиента.
const showAsk = async (next) => {
  now.ask = null;
  await settleMove();
  now.ask = next;
  await settleMove();
};

// --- вопрос доверия: тот же блок, кнопки по вариантам ---
{
  const box = boxOf();
  if (!box || box.hidden) fail("блока вопроса в панели нет: " + dump(panel).slice(0, 300));
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("Клиент ждёт ответа")) fail("блок не назвал себя: " + said);
  if (!said.includes("xr-proxy")) fail("в блоке нет самого вопроса: " + said);
  const words = optsOf().map((o) => dump(o).replace(/\s+/g, " ").trim());
  if (words.length !== 2 || !words[0].includes("Yes, I trust this folder")) {
    fail("варианты собрались не списком: " + JSON.stringify(words));
  }
  optsOf()[0].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.orders) !== JSON.stringify([{ option: 1 }])) {
    fail("ответ ушёл не тем пунктом: " + JSON.stringify(now.orders));
  }
  // Блок не стирается на время ответа: прежде на его месте появлялись слова
  // «ответ отправлен, ждём клиента», и это читалось перезагрузкой.
  if (dump(box).includes("ждём клиента")) fail("блок подменился словами ожидания: " + dump(box));
}

// --- опрос: варианты списком, пояснения второй строкой, отметка без слов ---
{
  now.orders.length = 0;
  await showAsk(poll);
  const box = boxOf();
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("Где именно MAX ломается")) fail("в блоке нет вопроса опроса: " + said);
  const rows = optsOf();
  // Три варианта, свободный ответ строкой списка, а кнопки виджета не в нём.
  const labels = rows.map((r) => (byClass(r, "casklabel") || byClass(r, "caskwords")).textContent);
  if (JSON.stringify(labels.slice(0, 3)) !== JSON.stringify(
    ["На телефоне (Android-клиент)", "На устройствах за роутером", "Везде одинаково"])) {
    fail("варианты собрались не списком: " + JSON.stringify(labels));
  }
  // Пояснение клиента видно второй строкой, а не теряется.
  const why = byClass(rows[0], "caskwhy");
  if (!why || why.textContent !== "Туннель поднят приложением") {
    fail("пояснение варианта не показано: " + dump(rows[0]));
  }
  // Отмеченный вариант помечен отметкой, а не словом «отмечено» в тексте.
  if (said.includes("отмечено")) fail("отметка сказана отладочным словом: " + said);
  const on = rows.filter((r) => String(r.className).includes("on"))
    .map((r) => (byClass(r, "casklabel") || byClass(r, "caskwords")).textContent);
  if (JSON.stringify(on) !== JSON.stringify(["Везде одинаково"])) {
    fail("отмеченный вариант не помечен отметкой: " + JSON.stringify(on));
  }
  if (!byClass(rows[2], "caskbox")) fail("у варианта с флажком нет самой отметки: " + dump(rows[2]));
  // Выбор уезжает пунктом по порядку остановок.
  rows[1].handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.orders) !== JSON.stringify([{ option: 2 }])) {
    fail("выбор ушёл не тем пунктом: " + JSON.stringify(now.orders));
  }
}

// --- слова кнопок русские, английских в блоке не осталось ---
{
  now.orders.length = 0;
  await showAsk(poll);
  const said = dump(boxOf()).replace(/\s+/g, " ");
  for (const gone of ["Next", "Submit", "Type something", "Chat about this"]) {
    if (said.includes(gone)) fail("в блоке осталось английское слово «" + gone + "»: " + said);
  }
  for (const want of ["Дальше", "Ответить своими словами", "Обсудить в чате"]) {
    if (!said.includes(want)) fail("в блоке нет русского слова «" + want + "»: " + said);
  }
}

// --- свободный ответ: поле под списком и только после выбора ---
{
  now.orders.length = 0;
  await showAsk(poll);
  const box = boxOf();
  const free = byClass(box, "caskfree");
  if (!free) fail("строки свободного ответа в блоке нет: " + dump(box).slice(0, 300));
  if (!free.hidden) fail("поле свободного ответа стоит раскрытым до выбора: " + dump(free));
  // Поле стоит под списком, а не в ряду вариантов.
  if (byClass(byClass(box, "casklist"), "caskfree")) {
    fail("поле свободного ответа вкорячено в список вариантов: " + dump(box).slice(0, 300));
  }
  const pick = optsOf().find((r) => dump(r).includes("Ответить своими словами"));
  if (!pick) fail("в списке нет пункта свободного ответа: " + dump(box).slice(0, 300));
  pick.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (free.hidden) fail("выбор своего ответа не раскрыл поле");
  const field = tag(free, "INPUT");
  if (!field || field.placeholder !== "Свой ответ") {
    fail("подсказка поля не «Свой ответ»: " + (field ? field.placeholder : "поля нет"));
  }
  deepBtn(free, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (now.orders.length) fail("пустой свой ответ уехал клиенту: " + JSON.stringify(now.orders));
  field.value = "ломается только на телефоне";
  deepBtn(free, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.orders) !== JSON.stringify([
    { option: 4, text: "ломается только на телефоне" }])) {
    fail("свой ответ уехал не так: " + JSON.stringify(now.orders));
  }
}

// --- шаги это табы: переход не требует ответа и ленту не пересобирает ---
{
  now.orders.length = 0;
  await showAsk(poll);
  const box = boxOf();
  const tabs = tabsOf();
  if (tabs.length !== 3) fail("шагов опроса не три: " + tabs.map((t) => t.textContent));
  const openNow = tabs.filter((t) => String(t.className).includes("now")).map((t) => t.textContent);
  if (JSON.stringify(openNow) !== JSON.stringify(["Место"])) {
    fail("открытый шаг не помечен: " + JSON.stringify(tabs.map((t) => t.className)));
  }
  const done = tabs.filter((t) => String(t.className).includes("on")).map((t) => t.textContent);
  if (!done.includes("Сроки")) fail("отвеченный шаг не помечен: " + JSON.stringify(done));
  // Переход по табу это не ответ: уезжает шаг, а не пункт.
  now.next = pollNext;
  tabs[1].handlers.click({ stopPropagation: () => {} });
  await settleMove();
  if (JSON.stringify(now.orders) !== JSON.stringify([{ step: 2 }])) {
    fail("переход по табу ушёл не шагом: " + JSON.stringify(now.orders));
  }
  // Блок тот же самый узел: правится он по месту, лента не пересобирается.
  if (boxOf() !== box) fail("блок опроса пересобран заново вместо правки по месту");
  if (!dump(box).includes("Что именно происходит с MAX?")) {
    fail("после перехода по табу блок не показал новый шаг: " + dump(box).slice(0, 300));
  }
  const nowTab = tabsOf().filter((t) => String(t.className).includes("now")).map((t) => t.textContent);
  if (JSON.stringify(nowTab) !== JSON.stringify(["Симптом"])) {
    fail("после перехода открытым помечен не тот шаг: " + JSON.stringify(nowTab));
  }
  // Нажатие на открытый таб никуда не ходит: переходить некуда.
  now.orders.length = 0;
  tabsOf().find((t) => String(t.className).includes("now"))
    .handlers.click({ stopPropagation: () => {} });
  await settle();
  if (now.orders.length) fail("нажатие на открытый шаг ушло запросом: " + JSON.stringify(now.orders));
}

// --- сводка: итог с одной кнопкой, а не ещё один опрос ---
{
  now.orders.length = 0;
  await showAsk(review);
  const box = boxOf();
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("Ответы опроса")) fail("сводка не назвала себя: " + said);
  // Ответы стоят сводкой, а не вариантами выбора.
  const rows = allByClass(box, "caskdone").map((r) => dump(r).replace(/\s+/g, " ").trim());
  if (rows.length !== 2 || !rows[0].includes("Что именно?") || !rows[0].includes("Картинки")) {
    fail("сводка ответов не собралась: " + JSON.stringify(rows));
  }
  if (!said.includes("not answered all questions")) {
    fail("предупреждение сводки потерялось: " + said);
  }
  // Кнопка одна, и она по-русски.
  const btns = allByClass(byClass(box, "caskr"), "btn").map((b) => b.textContent);
  if (JSON.stringify(btns) !== JSON.stringify(["Отправить ответы"])) {
    fail("у сводки не одна русская кнопка: " + JSON.stringify(btns));
  }
  deepBtn(box, "Отправить ответы").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.orders) !== JSON.stringify([{ option: 1 }])) {
    fail("отправка сводки ушла не тем пунктом: " + JSON.stringify(now.orders));
  }
}

// --- спрашивать нечего: блока нет вовсе ---
{
  await showAsk(null);
  const box = boxOf();
  if (box && !box.hidden) fail("блок вопроса стоит у молчащего клиента: " + dump(box));
}

// --- у чужого окна вопрос не спрашивается вовсе ---
{
  const asked = [];
  const { sandbox: other } = makeSandbox(app, (path) => {
    asked.push(path);
    if (path.includes("/sessions/")) return { items: [], start: true };
    if (path.includes("/chats")) return { chats: [], models: [] };
    return {};
  });
  const alien = Object.assign({}, st, { entry: { id: SID, state: "vscode", tmux: "" } });
  other.chatPanel("demo", alien);
  await settle();
  if (asked.some((p) => p.includes("/ask"))) {
    fail("панель спросила вопрос у окна без нашей tmux: " + JSON.stringify(asked));
  }
}

console.log("poc_clientask: вопрос блоком в панели, опрос табами и списком, " +
  "свой ответ строкой под списком, слова русские, сводка итогом с одной кнопкой");
