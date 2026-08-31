// Стенд вопроса агента в чате задачи (DK-652).
//
// Заход спросил человека командой taskctl ask и стоит, пока ответа нет. Вопрос
// по своей же задаче панель не показывала вовсе: от него оставалась подсказка
// на поле ввода, которую видно только с мышью (живой случай DK-650, восемь
// минут ожидания и парковка). Варианты терялись по дороге и у розданного
// вопроса: он приезжал плоскими строками без единой кнопки.
//
// Предмет стенда: блок с вопросом стоит над полем ввода без живой tmux-сессии,
// варианты видны кнопками, пачка вопросов идёт шагами, а ответ уезжает
// репликой разговора, не клавишами в чужое окно. Вид кнопок меряет
// poc_caskopt.mjs, тут собирается сам блок.
//
// Зовётся: node testdata/poc_agentask.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, tag, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID = "aaaa1111-1111-4111-8111-111111111111";
const until = Math.floor(Date.now() / 1000) + 300;
const freePick = { text: "свой ответ", kind: "free" };

// Одиночный вопрос, как его отдаёт ручка из признака ожидания.
const one = {
  kind: "agent",
  task: "XR-9",
  until,
  text: "куда катить",
  options: [
    { text: "в прод", desc: "советует агент. сразу" },
    { text: "в стенд" },
    freePick,
  ],
};

// Пачка вопросов: шаги едут разом, вопросы соседа стоят рядом с открытым.
const pack = {
  kind: "agent",
  task: "XR-9",
  until,
  rest: ["XR-8"],
  text: "куда катить",
  options: [{ text: "в прод" }, { text: "в стенд" }, freePick],
  steps: [
    {
      name: "1. куда катить",
      now: true,
      text: "куда катить",
      options: [{ text: "в прод" }, { text: "в стенд" }, freePick],
    },
    {
      name: "2. когда катить",
      text: "когда катить",
      options: [{ text: "утром" }, { text: "вечером" }, freePick],
    },
  ],
};

// Что отдаёт ручка вопроса и куда ушли ответы. Реплики разговора и реплики
// задачи считаются порознь: дороги у них разные, и путать их нельзя.
const now = { ask: one, said: [], toTask: [], keys: [] };

const { sandbox, timers } = makeSandbox(app, (path, init) => {
  const post = init && init.method === "POST";
  if (post && path.includes("/say")) {
    now.said.push(JSON.parse(init.body).text);
    return { way: "ask", chat: "task-XR-9", message: "ответ лёг во вход разговора" };
  }
  if (post && path.includes("/tasks/") && path.includes("/message")) {
    now.toTask.push(JSON.parse(init.body).text);
    return { way: "ask", task: "XR-9" };
  }
  if (post && path.includes("/ask")) {
    now.keys.push(JSON.parse(init.body));
    return {};
  }
  if (path.includes("/ask")) {
    return now.ask ? { session: SID, task: "XR-9", ask: now.ask }
      : { session: SID, note: "вопросов агента за ним нет" };
  }
  if (path.includes("/sessions/")) return { items: [], start: true };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

// Живой разговор задачи без нашей tmux-сессии: имени окна у записи нет вовсе,
// и вопрос агента обязан приехать всё равно.
const liveSt = () => ({
  addr: SID, sid: SID, project: "demo", task: "XR-9", chats: [], models: [],
  entry: { id: SID, state: "live", tmux: "", idle: true },
});

const boxOf = (panel) => byClass(panel, "cask");
const optsOf = (panel) => allByClass(boxOf(panel), "caskopt");
const tabsOf = (panel) => allByClass(boxOf(panel), "ktab");
const click = (node) => node.handlers.click({ stopPropagation: () => {} });
const settleMove = async () => {
  await settle();
  for (const t of timers.splice(0)) t.fn();
  await settle();
};

// --- одиночный вопрос: блок с кнопками без живой tmux ---
{
  now.ask = one;
  const panel = sandbox.chatPanel("demo", liveSt());
  await settle();
  const box = boxOf(panel);
  if (!box || box.hidden) fail("блока вопроса агента в панели нет: " + dump(panel).slice(0, 400));
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("Вопрос от задачи XR-9")) fail("блок не назвал задачу: " + said);
  if (!said.includes("осталось")) fail("обратного отсчёта в блоке нет: " + said);
  if (!said.includes("куда катить")) fail("самого вопроса в блоке нет: " + said);

  const rows = optsOf(panel);
  const labels = rows.map((r) => (byClass(r, "casklabel") || byClass(r, "caskwords")).textContent);
  if (JSON.stringify(labels) !== JSON.stringify(["в прод", "в стенд", "Ответить своими словами"])) {
    fail("варианты собрались не кнопками списка: " + JSON.stringify(labels));
  }
  for (const row of rows) {
    if (row.tagName !== "BUTTON") fail("вариант нарисован не кнопкой: " + row.tagName);
  }
  const why = byClass(rows[0], "caskwhy");
  if (!why || !why.textContent.includes("советует агент")) {
    fail("совет агента не показан второй строкой: " + dump(rows[0]));
  }

  // Нажатие уезжает репликой разговора, а не клавишами в чужое окно.
  click(rows[0]);
  await settle();
  if (JSON.stringify(now.said) !== JSON.stringify(["в прод"])) {
    fail("ответ ушёл не подписью варианта: " + JSON.stringify(now.said));
  }
  if (now.keys.length) fail("ответ поехал клавишами в tmux: " + JSON.stringify(now.keys));
  // Ожидание снимается первой же строкой во входе, и второму ответу ехать
  // некуда.
  click(rows[1]);
  await settle();
  if (now.said.length !== 1) fail("второй ответ на тот же вопрос уехал: " + JSON.stringify(now.said));
}

// --- свободный ответ: поле под списком и только после выбора ---
{
  now.said.length = 0;
  now.ask = one;
  const panel = sandbox.chatPanel("demo", liveSt());
  await settle();
  const box = boxOf(panel);
  const free = byClass(box, "caskfree");
  if (!free) fail("строки свободного ответа в блоке нет: " + dump(box).slice(0, 300));
  if (!free.hidden) fail("поле свободного ответа стоит раскрытым до выбора: " + dump(free));
  click(optsOf(panel).find((r) => dump(r).includes("Ответить своими словами")));
  await settle();
  if (free.hidden) fail("выбор своего ответа не раскрыл поле");
  const field = tag(free, "INPUT");
  deepBtn(free, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (now.said.length) fail("пустой свой ответ уехал агенту: " + JSON.stringify(now.said));
  field.value = "катим утром в стенд";
  deepBtn(free, "Отправить").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.said) !== JSON.stringify(["катим утром в стенд"])) {
    fail("свой ответ уехал не так: " + JSON.stringify(now.said));
  }
}

// --- пачка: шаги табами, ответы копятся, реплика одна ---
{
  now.said.length = 0;
  now.ask = pack;
  const panel = sandbox.chatPanel("demo", liveSt());
  await settle();
  const box = boxOf(panel);
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("XR-8")) fail("очередь следующих вопросов не названа: " + said);
  const tabs = tabsOf(panel);
  if (tabs.length !== 2) fail("шагов пачки не два: " + JSON.stringify(tabs.map((t) => t.textContent)));
  const open = tabs.filter((t) => String(t.className).includes("onktab")).map((t) => t.textContent);
  if (JSON.stringify(open) !== JSON.stringify(["1. куда катить"])) {
    fail("открытый шаг не помечен: " + JSON.stringify(open));
  }

  // Отметка на первом шаге ничего никуда не шлёт: ответ копится в панели.
  click(optsOf(panel)[0]);
  await settle();
  if (now.said.length || now.keys.length) {
    fail("отметка шага уехала запросом: " + JSON.stringify(now.said) + JSON.stringify(now.keys));
  }
  const marked = optsOf(panel).filter((r) => String(r.className).includes("on"))
    .map((r) => (byClass(r, "casklabel") || byClass(r, "caskwords")).textContent);
  if (JSON.stringify(marked) !== JSON.stringify(["в прод"])) {
    fail("отмеченный вариант не помечен отметкой: " + JSON.stringify(marked));
  }
  if (!byClass(optsOf(panel)[0], "caskbox")) {
    fail("у отмеченного варианта нет самой отметки: " + dump(optsOf(panel)[0]));
  }

  // Переход на соседний шаг тоже не ходит на сервер: вопросы приехали разом.
  click(tabsOf(panel)[1]);
  await settle();
  if (now.said.length || now.keys.length) fail("переход по шагу уехал запросом");
  const second = dump(boxOf(panel)).replace(/\s+/g, " ");
  if (!second.includes("когда катить") || second.includes("в прод")) {
    fail("под соседним табом стоят варианты не того шага: " + second.slice(0, 300));
  }
  click(optsOf(panel)[1]);
  await settle();

  // Кнопка отправки собирает ответы всех шагов в одну реплику.
  deepBtn(boxOf(panel), "Отправить ответ").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.said) !== JSON.stringify(["куда катить: в прод\nкогда катить: вечером"])) {
    fail("ответ пачки уехал не одной репликой: " + JSON.stringify(now.said));
  }
}

// --- заход умер, а признак живой: ответ уезжает во вход задачи ---
{
  now.said.length = 0;
  now.toTask.length = 0;
  now.ask = one;
  const deadSt = Object.assign(liveSt(), {
    entry: { id: SID, state: "dead", tmux: "" },
    wait: { state: "ждёт ответа", note: "спросил агент", questions: ["куда катить"], until },
  });
  const panel = sandbox.chatPanel("demo", deadSt);
  await settle();
  const box = boxOf(panel);
  if (!box || box.hidden) fail("у задачи без живой сессии блока вопроса нет: " + dump(panel).slice(0, 300));
  click(optsOf(panel)[0]);
  await settle();
  if (JSON.stringify(now.toTask) !== JSON.stringify(["в прод"])) {
    fail("ответ мимо входа задачи: " + JSON.stringify(now.toTask) + JSON.stringify(now.said));
  }
}

// --- вопроса нет: блока нет вовсе ---
{
  now.ask = null;
  const panel = sandbox.chatPanel("demo", liveSt());
  await settleMove();
  const box = boxOf(panel);
  if (box && !box.hidden) fail("блок стоит без вопроса: " + dump(box));
}

console.log("poc_agentask: блок с вопросом без живой tmux, варианты кнопками, " +
  "пачка шагами и одной репликой, ответ во вход задачи");
