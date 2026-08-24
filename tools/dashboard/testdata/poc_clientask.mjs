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
// реплики, а клиент их не читал, потому что ждал выбора). У опроса своя
// механика: полоса шагов, флажки, свободный ответ полем и кнопка отправки
// самого виджета.
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

// Опрос агента, как его отдаёт разбор живой панели: шаги, флажки, свободный
// ответ и кнопка отправки виджета.
const poll = {
  text: "Где именно MAX ломается под прокси?",
  options: [
    { text: "На телефоне (Android-клиент)", mark: "off" },
    { text: "На устройствах за роутером", mark: "off" },
    { text: "Везде одинаково", mark: "on" },
    { text: "Type something", mark: "off", free: true },
    { text: "Next", submit: true },
    { text: "Chat about this" },
  ],
  at: 1,
  keys: "arrows",
  steps: [{ name: "Площадка" }, { name: "Симптом" }, { name: "Submit", done: true }],
};

// Следующий шаг опроса: он приходит после ответа, и панель обязана показать
// его так же, а не считать разговор продолженным.
const pollNext = {
  text: "Что именно происходит с MAX?",
  options: [
    { text: "Не открываются картинки", mark: "off" },
    { text: "Звонки не соединяются", mark: "off" },
    { text: "Next", submit: true },
  ],
  at: 1,
  keys: "arrows",
  steps: [{ name: "Площадка", done: true }, { name: "Симптом" }, { name: "Submit" }],
};

// Что отдаёт ручка вопроса и что у неё спросили: стенд правит первое и смотрит
// второе.
const now = { ask, answered: [], next: null };

const { sandbox, timers } = makeSandbox(app, (path, init) => {
  if (path.includes("/ask") && init && init.method === "POST") {
    now.answered.push(JSON.parse(init.body));
    // Многошаговый опрос: ответ на шаг приводит следующий, а не заканчивает
    // разговор. Обычный вопрос после ответа просто гаснет.
    now.ask = now.next || null;
    now.next = null;
    return { message: "ответ отправлен клиенту" };
  }
  if (path.includes("/ask")) return now.ask ? { session: SID, tmux: "chat-2", ask: now.ask }
    : { session: SID, tmux: "chat-2", note: "клиент chat-2 ни о чём не спрашивает" };
  if (path.includes("/sessions/")) return { items: [], start: true };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const st = { addr: SID, sid: SID, project: "demo", chats: [], entry: { id: SID, state: "live",
  tmux: "chat-2", idle: true }, models: [] };

// --- вопрос приходит блоком с кнопками ---
const panel = sandbox.chatPanel("demo", st);
await settle();
{
  const box = byClass(panel, "cask");
  if (!box || box.hidden) fail("блока вопроса в панели нет: " + dump(panel).slice(0, 300));
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("Клиент ждёт ответа")) fail("блок не назвал себя: " + said);
  if (!said.includes("xr-proxy")) fail("в блоке нет самого вопроса: " + said);
  const btns = allByClass(byClass(box, "caskr"), "btn").map((b) => b.textContent);
  if (JSON.stringify(btns) !== JSON.stringify(ask.options.map((o) => o.text))) {
    fail("кнопки собрались не по вариантам клиента: " + JSON.stringify(btns));
  }
}

// --- нажатие шлёт ответ клиенту и ожидание не роняет ---
{
  const box = byClass(panel, "cask");
  const yes = deepBtn(box, "Yes, I trust this folder");
  if (!yes) fail("кнопки согласия нет: " + dump(box));
  yes.handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.answered) !== JSON.stringify([{ option: 1 }])) {
    fail("ответ ушёл не тем пунктом: " + JSON.stringify(now.answered));
  }
  // Панель после ответа продолжает ждать клиента, а не гаснет молча.
  if (!dump(box).includes("ждём клиента")) {
    fail("после ответа панель молчит: " + dump(box));
  }
}

// --- опрос агента с вариантами: тот же блок, кнопка на каждый вариант ---
{
  now.ask = poll;
  now.answered.length = 0;
  const panel2 = sandbox.chatPanel("demo", st);
  await settle();
  const box = byClass(panel2, "cask");
  if (!box || box.hidden) fail("опрос агента не показан панелью вовсе: " + dump(panel2).slice(0, 300));
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("Где именно MAX ломается")) fail("в блоке нет самого вопроса: " + said);
  // Полоса шагов стоит: без неё непонятно, почему после ответа приходит
  // следующий вопрос, а не продолжение разговора.
  const steps = allByClass(byClass(box, "caskst") || box, "cstep").map((n) => n.textContent);
  if (JSON.stringify(steps) !== JSON.stringify(["Площадка", "Симптом", "Submit"])) {
    fail("полоса шагов опроса не собралась: " + JSON.stringify(steps));
  }
  // Кнопка на каждый вариант, включая кнопку отправки самого виджета. У
  // свободного ответа кнопки нет, у него поле.
  const btns = allByClass(byClass(box, "caskr"), "btn").map((b) => b.textContent);
  for (const want of ["На телефоне (Android-клиент)", "Next", "Chat about this"]) {
    if (!btns.some((b) => b.includes(want))) {
      fail("в блоке нет кнопки «" + want + "»: " + JSON.stringify(btns));
    }
  }
  // Отмеченный флажок виден словом: человек должен понимать, что уже выбрано.
  if (!btns.some((b) => b.includes("отмечено: Везде одинаково"))) {
    fail("отмеченный вариант не назван отмеченным: " + JSON.stringify(btns));
  }
  // Выбор уезжает пунктом по порядку остановок, а клавиши подбирает сервер.
  deepBtn(box, "На устройствах за роутером").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.answered) !== JSON.stringify([{ option: 2 }])) {
    fail("выбор ушёл не тем пунктом: " + JSON.stringify(now.answered));
  }
}

// --- свободный ответ: поле в блоке, текст уезжает тем же пунктом ---
{
  now.ask = poll;
  now.answered.length = 0;
  const panel3 = sandbox.chatPanel("demo", st);
  await settle();
  const box = byClass(panel3, "cask");
  const free = byClass(box, "caskfree");
  if (!free) fail("у свободного ответа нет поля: " + dump(box).slice(0, 300));
  const field = tag(free, "INPUT");
  if (!field) fail("в свободном ответе нет самого поля: " + dump(free));
  // Пустой текст клиенту не уезжает: он открыл бы поле и встал ждать.
  deepBtn(free, "Ответить своими словами").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (now.answered.length) fail("пустой свободный ответ уехал клиенту: " + JSON.stringify(now.answered));
  field.value = "ломается только на телефоне после обновления";
  deepBtn(free, "Ответить своими словами").handlers.click({ stopPropagation: () => {} });
  await settle();
  if (JSON.stringify(now.answered) !== JSON.stringify([
    { option: 4, text: "ломается только на телефоне после обновления" }])) {
    fail("свободный ответ уехал не так: " + JSON.stringify(now.answered));
  }
}

// --- многошаговый опрос: после ответа панель показывает следующий шаг ---
{
  now.ask = poll;
  now.answered.length = 0;
  now.next = pollNext;
  const panel4 = sandbox.chatPanel("demo", st);
  await settle();
  const box = byClass(panel4, "cask");
  deepBtn(box, "Next").handlers.click({ stopPropagation: () => {} });
  await settle();
  // Панель не замирает на «ответ отправлен»: она перечитывает снимок, и
  // следующий шаг встаёт на место прежнего.
  for (const t of timers.splice(0)) t.fn();
  await settle();
  const said = dump(box).replace(/\s+/g, " ");
  if (!said.includes("Что именно происходит с MAX?")) {
    fail("следующий шаг опроса панель не показала: " + said.slice(0, 300));
  }
  const steps = allByClass(byClass(box, "caskst") || box, "cstep")
    .filter((n) => String(n.className).includes("on")).map((n) => n.textContent);
  if (JSON.stringify(steps) !== JSON.stringify(["Площадка"])) {
    fail("полоса шагов не отметила пройденный шаг: " + JSON.stringify(steps));
  }
}

// --- спрашивать нечего: блока нет вовсе ---
{
  now.ask = null;
  const quiet = sandbox.chatPanel("demo", st);
  await settle();
  const box = byClass(quiet, "cask");
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

console.log("poc_clientask: ok");
