// Стенд карточки вопроса из признака ожидания (DK-1235).
//
// Раньше панель вешала галочки на строки вариантов в тексте реплики: блок
// печатала утилита, агент вставлял его в реплику как есть, а панель разбирала
// слова. Реплика «кэш» от продления кеша и промах копирования блока гасили
// поиск, панель поднимала ложную плашку «Заход ждёт ответа» и оставляла
// человека без выбора. Теперь вопрос рисует карточка из самого признака
// ожидания: галочки стоят при вариантах на карточке, пока жив признак, и слов
// реплики для этого не спрашивают.
//
// Предмет стенда: карточка есть при живом признаке и без блока в ленте,
// реплика «кэш» и своя реплика человека галочек не снимают, плашек про
// отсутствующий блок нет вовсе, отметка собирает строку ответа «имя N» в
// поле ввода, а снятие признака уносит карточку и строку.
//
// Зовётся: node testdata/poc_askpick.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID = "aaaa1111-1111-4111-8111-111111111111";
const since = Math.floor(Date.now() / 1000) - 120;
const until = Math.floor(Date.now() / 1000) + 300;

// Слова реплики агента. Блок вопроса сюда больше не вставляется: в панели его
// рисует карточка из признака, а агенту команда прямо говорит не копировать.
const plain = "Спросил по развилкам записи, жду ответа.";

// Служебная реплика продления кеша (cachekeep). Приходит после вопроса от
// имени человека и прежде гасила поиск блока с конца: state.seen=false и
// ложная плашка «Заход ждёт ответа».
const cacheKeep = "кэш";

// Промах копирования блока: агент вопрос упомянул, а блок в реплику не
// вставил. Прежде галочки вовсе не вставали, а панель молчала или врала
// плашкой.
const copyMiss = "Вопрос по развилкам задал, отвечай в панели.";

// Обычный нумерованный список плана: карточке он безразличен, галочки на
// реплику не вешаются.
const plan = ["Разобрал по порядку.", "", "1. читаю разбор", "2. правлю код"].join("\n");

const at = (n) => new Date(Date.now() - (100 - n) * 60000).toISOString();
const answer = (n, text) => ({ key: "t:" + n, seq: n, role: "assistant", time: at(n), text });
// Реплика человека: её пузырь несёт класс «me». Реплика «кэш» приходит тем же
// путём и не должна снимать галочки карточки.
const mine = (n, text) => ({ key: "t:" + n, seq: n, role: "user", time: at(n), text });

// Признак ожидания, как его отдаёт ручка вопроса и собирает agentAskOf:
// имя развилки едет полем fork, вопрос и варианты полем text и options.
const askOf = (...qs) => {
  const steps = qs.map(([fork, text, ...opts], i) => ({
    name: i + 1 + ". " + text.slice(0, 28),
    now: i === 0,
    fork: fork || "",
    text,
    options: opts.map((opt, j) => ({
      text: opt,
      desc: j === 0 ? "рекомендую: " + opt : "",
    })),
  }));
  const out = {
    kind: "agent",
    task: "DK-1235",
    since,
    until,
    fork: steps[0].fork,
    text: steps[0].text,
    options: steps[0].options,
  };
  if (steps.length > 1) out.steps = steps;
  return out;
};

// Пачка двух развилок: промах DK-891 случился как раз на пачке.
const ask = askOf(
  ["печать", "«печать»: кто собирает текст вопроса в чат?", "утилита печатает блок", "агент собирает текст"],
  ["ответ", "«ответ»: какой формы ответ человека?", "имя развилки и номер варианта", "только словами"],
);

// Одна развилка: steps не заводится, fork лежит в общем поле.
const askOne = askOf(
  ["печать", "«печать»: кто собирает текст вопроса в чат?", "утилита печатает блок", "агент собирает текст"],
);

// Свободный вопрос `taskctl ask --question`: имени развилки нет, варианты идут
// строками без галочек.
const askFree = askOf(
  ["", "чем держать очередь выката?", "замком конвейера", "руками"],
);

// Свободный вопрос совсем без вариантов: карточка держит текст, отметок нет.
const askBare = {
  kind: "agent",
  task: "DK-1235",
  since,
  until,
  text: "какой контур у выката?",
};

const now = { ask, items: [], said: [] };

const { sandbox, timers, streams } = makeSandbox(app, (path, init) => {
  const post = init && init.method === "POST";
  if (post && path.includes("/say")) {
    now.said.push(JSON.parse(init.body).text);
    return { way: "ask", chat: "task-DK-1235" };
  }
  if (path.includes("/ask")) {
    return now.ask ? { session: SID, task: "DK-1235", ask: now.ask }
      : { session: SID, note: "вопросов агента за ним нет" };
  }
  if (path.includes("/sessions/")) {
    return { session: SID, head: { id: SID }, items: now.items, total: now.items.length };
  }
  if (path.includes("/chats")) return { chats: [], models: [], days: 3, older: false };
  return {};
});

const chat = { id: SID, project: "demo", title: "разбор развилок", state: "live", idle: true, tasks: [] };
const liveSt = () => ({
  project: "demo", addr: SID, sid: SID, task: "DK-1235",
  chats: [chat], entry: chat, models: [],
});

const panelWith = async (items) => {
  now.items = items;
  sandbox.location.hash = "#demo/chat/" + SID;
  const panel = sandbox.chatPanel("demo", liveSt());
  await settle();
  await settle();
  return panel;
};
const feedOf = (panel) => byClass(panel, "chatfeed");
const boxOf = (panel) => byClass(panel, "cask");
const sayOf = (panel) => byClass(panel, "csay");
const picksOf = (panel) => allByClass(boxOf(panel), "caskpick");
const click = (node) => node.handlers.click({ stopPropagation: () => {} });
const settleMove = async () => {
  await settle();
  for (const t of timers.splice(0)) t.fn();
  await settle();
};

// --- ложная плашка от реплики «кэш»: галочки стоят, плашки нет ---
{
  const panel = await panelWith([answer(1, plain), mine(2, cacheKeep)]);
  const box = boxOf(panel);
  const picks = picksOf(panel);
  if (picks.length !== 5) {
    fail("галочки карточки не пять после реплики «кэш», а " + picks.length + ": " +
      dump(box).slice(0, 600));
  }
  const said = dump(box);
  for (const gone of ["Заход ждёт ответа", "не вышло разобрать", "блок вопроса"]) {
    if (said.includes(gone)) fail("ложная плашка встала от реплики «кэш»: " + gone);
  }
  if (!said.includes("Вопрос ждёт ответа")) {
    fail("карточка не назвала живой вопрос: " + said.slice(0, 400));
  }
}

// --- промах копирования блока: галочки на карточке, плашки нет ---
{
  const panel = await panelWith([answer(1, copyMiss)]);
  const box = boxOf(panel);
  const picks = picksOf(panel);
  if (picks.length !== 5) {
    fail("без блока в ленте галочки карточки не встали: " + picks.length + ": " +
      dump(box).slice(0, 600));
  }
  const said = dump(box);
  for (const gone of ["Заход ждёт ответа", "не вышло разобрать", "скопируй блок"]) {
    if (said.includes(gone)) fail("плашка про отсутствующий блок вернулась: " + gone);
  }
}

// --- шапка карточки называет задачу и время вопроса ---
{
  const panel = await panelWith([answer(1, plain)]);
  const said = dump(boxOf(panel));
  if (!said.includes("DK-1235")) fail("шапка карточки не назвала задачу: " + said.slice(0, 400));
  if (!said.includes("спросил в ")) fail("шапка карточки не назвала время вопроса: " + said.slice(0, 400));
  if (!said.includes("кто собирает текст вопроса в чат?")) {
    fail("карточка не показала текст вопроса: " + said.slice(0, 500));
  }
  if (!said.includes("какой формы ответ человека?")) {
    fail("карточка потеряла второй вопрос пачки: " + said.slice(0, 600));
  }
}

// --- галочки стоят на карточке, а не в тексте реплики ---
{
  const panel = await panelWith([answer(1, plain)]);
  if (allByClass(feedOf(panel), "caskpick").length) {
    fail("галочки повесились на реплику, а не на карточку");
  }
  for (const pick of picksOf(panel)) {
    if (!pick.parentNode || pick.parentNode.className !== "caskflat") {
      fail("галочка стоит не при строке варианта карточки: " +
        (pick.parentNode || {}).className);
    }
  }
  // Прежнего блока с кнопками, табами и своей отправкой нет вовсе.
  const box = boxOf(panel);
  if (deepBtn(box, "Отправить")) fail("у карточки своя кнопка отправки");
  if (allByClass(box, "ktab").length) fail("вопрос остался табами");
  if (allByClass(box, "caskopt").length) fail("варианты карточки стали кнопками клиента");
}

// --- отметка собирает строку ответа «имя N», снятая её убирает ---
{
  const panel = await panelWith([answer(1, plain)]);
  const ta = sayOf(panel);
  const picks = picksOf(panel);
  click(picks[0]);
  if (ta.value !== "печать 1") {
    fail("отметка собрала строку ответа не так: " + JSON.stringify(ta.value));
  }
  if (!String(picks[0].className).split(" ").includes("on")) fail("отмеченная галочка не помечена");
  if (picks[0].attrs["aria-checked"] !== "true") fail("отметка не названа для чтения с экрана");
  click(picks[4]);
  if (ta.value !== "печать 1, ответ 3") {
    fail("вторая отметка легла в строку ответа не так: " + JSON.stringify(ta.value));
  }
  click(picks[0]);
  if (ta.value !== "ответ 3") {
    fail("снятая отметка не убралась из поля ввода: " + JSON.stringify(ta.value));
  }
  click(picks[4]);
  if (ta.value !== "") fail("поле ввода не опустело со снятой отметкой: " + JSON.stringify(ta.value));

  ta.value = "сначала поясню";
  click(picks[1]);
  if (ta.value !== "сначала поясню печать 2") {
    fail("строка ответа затёрла написанное человеком: " + JSON.stringify(ta.value));
  }
  click(picks[1]);
  if (ta.value !== "сначала поясню") {
    fail("снятая отметка унесла слова человека: " + JSON.stringify(ta.value));
  }
}

// --- отправляет обычная кнопка чата ---
{
  const panel = await panelWith([answer(1, plain)]);
  const ta = sayOf(panel);
  click(picksOf(panel)[3]);
  if (ta.value !== "ответ 2") fail("строка ответа не собралась: " + JSON.stringify(ta.value));
  deepBtn(byClass(panel, "crow"), "Отправить").handlers.click({});
  await settle();
  if (JSON.stringify(now.said) !== JSON.stringify(["ответ 2"])) {
    fail("ответ уехал не обычной репликой чата: " + JSON.stringify(now.said));
  }
  if (ta.value !== "") fail("поле ввода не опустело после отправки: " + JSON.stringify(ta.value));
}

// --- своя реплика человека не снимает галочки ---
{
  now.said.length = 0;
  const panel = await panelWith([answer(1, plain)]);
  const ta = sayOf(panel);
  click(picksOf(panel)[0]);
  if (ta.value !== "печать 1") fail("отметка не собралась перед репликой человека");
  await panelWith([answer(1, plain), mine(2, "поясню словами")]);
  const picks = picksOf(panel);
  if (picks.length !== 5) fail("своя реплика сняла галочки карточки: " + picks.length);
  if ((sayOf(panel).value || "") === "") {
    fail("своя реплика стёрла строку ответа в поле ввода");
  }
}

// --- ответа не ждут: карточки нет ---
{
  now.ask = null;
  const panel = await panelWith([answer(1, plain)]);
  await settleMove();
  const box = boxOf(panel);
  if (box && !box.hidden) fail("карточка стоит без признака ожидания: " + dump(box).slice(0, 300));
  if (picksOf(panel).length) fail("галочки стоят без признака ожидания");
  now.ask = ask;
}

// --- снятие признака уносит карточку и строку ответа ---
{
  const panel = await panelWith([answer(1, plain)]);
  const ta = sayOf(panel);
  click(picksOf(panel)[0]);
  if (ta.value !== "печать 1") fail("строка ответа не собралась перед снятием признака");
  now.ask = null;
  await settleMove();
  const box = boxOf(panel);
  if (box && !box.hidden) fail("карточка осталась после снятого признака");
  if (picksOf(panel).length) fail("галочки остались после снятого признака");
  if (ta.value !== "") fail("строка ответа осталась в поле ввода: " + JSON.stringify(ta.value));
  now.ask = ask;
}

// --- свободный вопрос с вариантами: карточка без галочек ---
{
  now.ask = askFree;
  const panel = await panelWith([answer(1, plain)]);
  const box = boxOf(panel);
  if (box.hidden) fail("карточка не встала на свободный вопрос");
  if (picksOf(panel).length) {
    fail("свободный вопрос получил галочки: " + picksOf(panel).length);
  }
  const said = dump(box);
  if (!said.includes("чем держать очередь выката?")) {
    fail("карточка не назвала свободный вопрос: " + said.slice(0, 400));
  }
  if (!said.includes("замком конвейера")) {
    fail("карточка потеряла варианты свободного вопроса: " + said.slice(0, 400));
  }
  for (const gone of ["Заход ждёт ответа", "1. «"]) {
    if (said.includes(gone)) fail("свободный вопрос оброс чужой обвязкой: " + gone);
  }
  now.ask = ask;
}

// --- свободный вопрос без вариантов: карточка держит текст ---
{
  now.ask = askBare;
  const panel = await panelWith([answer(1, plain)]);
  const box = boxOf(panel);
  if (box.hidden) fail("карточка не встала на вопрос без вариантов");
  if (!dump(box).includes("какой контур у выката?")) {
    fail("карточка не показала текст вопроса без вариантов: " + dump(box).slice(0, 400));
  }
  if (picksOf(panel).length) fail("у вопроса без вариантов есть галочки");
  now.ask = ask;
}

// --- одна развилка: fork читается из общего поля, steps не нужен ---
{
  now.ask = askOne;
  const panel = await panelWith([answer(1, plain)]);
  const ta = sayOf(panel);
  const picks = picksOf(panel);
  if (picks.length !== 2) fail("у одной развилки не две галочки, а " + picks.length);
  click(picks[1]);
  if (ta.value !== "печать 2") {
    fail("ответ одиночного вопроса собрался не так: " + JSON.stringify(ta.value));
  }
  now.ask = ask;
}

// --- реплика «кэш» после отметки: строка ответа и галочки на месте ---
{
  now.said.length = 0;
  const panel = await panelWith([answer(1, plain)]);
  const ta = sayOf(panel);
  click(picksOf(panel)[0]);
  if (ta.value !== "печать 1") fail("отметка не собралась до реплики «кэш»");
  await panelWith([answer(1, plain), mine(2, cacheKeep)]);
  if (picksOf(panel).length !== 5) {
    fail("реплика «кэш» сняла галочки: " + picksOf(panel).length);
  }
  if ((sayOf(panel).value || "") !== "печать 1") {
    fail("реплика «кэш» стёрла строку ответа: " + JSON.stringify(sayOf(panel).value));
  }
  const said = dump(boxOf(panel));
  if (said.includes("Заход ждёт ответа")) {
    fail("ложная плашка встала после отметки и реплики «кэш»");
  }
}

// --- в признаке пачка вопросов: карточка показывает все ---
{
  const panel = await panelWith([answer(1, plan)]);
  const box = boxOf(panel);
  const said = dump(box);
  for (const want of ["кто собирает текст вопроса в чат?", "какой формы ответ человека?",
    "утилита печатает блок", "имя развилки и номер", "печать 1", "ответ 1"]) {
    if (!said.includes(want)) {
      fail("карточка потеряла из пачки «" + want + "»: " + said.slice(0, 600));
    }
  }
  if (picksOf(panel).length !== 5) {
    fail("на пачке не пять галочек, а " + picksOf(panel).length);
  }
}

console.log("poc_askpick: карточка из признака держит галочки при реплике «кэш» и промахе " +
  "копирования, шапка зовёт задачу и время, отметка собирает «имя N», " +
  "снятие признака уносит карточку и строку ответа");
