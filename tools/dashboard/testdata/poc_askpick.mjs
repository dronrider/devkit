// Стенд вопроса агента текстом в ленте (DK-1374).
//
// Карточка «Вопрос ждёт ответа» отвергнута на приёмке DK-1235: вопрос
// вставал коробкой с рамкой цвета ожидания над полем ввода и не читался
// репликой разговора. Теперь вопрос идёт текстом в ленте, строки вариантов
// несут галочки и чуть подсвечены, а ответ уезжает обычной репликой из того
// же поля ввода.
//
// Галочки вешаются на строки вариантов по признаку ожидания: пока жив
// признак, они стоят, а реплика «кэш» от продления кеша и своя реплика
// человека их не снимают. При промахе копирования блока агентом панель
// дописывает те же строки вопроса текстом той же формы, и галочки висят на
// них. Плашек про отсутствующий блок нет вовсе.
//
// Предмет стенда: нет карточки и шапки «Вопрос ждёт ответа», галочки стоят
// при строках вариантов в ленте, строки вариантов подсвечены, при промахе
// копирования панель показывает те же строки текстом той же формы, реплика
// «кэш» не снимает галочек, отметка собирает строку ответа «имя N» в поле
// ввода, а снятие признака уносит галочки и строку.
//
// Зовётся: node testdata/poc_askpick.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID = "aaaa1111-1111-4111-8111-111111111111";
const since = Math.floor(Date.now() / 1000) - 120;
const until = Math.floor(Date.now() / 1000) + 300;

// Строка с задачей и временем (DK-1204). Панель держит её первой строкой
// вопроса текстом, карточки с шапкой нет (DK-1374).
const sinceAt = new Date(since * 1000);
const metaLine = "вопрос по DK-1374, спросил в " +
  String(sinceAt.getHours()).padStart(2, "0") + ":" + String(sinceAt.getMinutes()).padStart(2, "0");

// Блок вопроса слово в слово таким, каким его печатает taskctl decide --chat
// и вставляет агент в реплику. Те же строки панель дописывает текстом той же
// формы при промахе копирования.
const block = [
  metaLine,
  "",
  "«печать»: кто собирает текст вопроса в чат?",
  "1. рекомендую: утилита печатает блок",
  "2. агент собирает текст",
  "",
  "«ответ»: какой формы ответ человека?",
  "1. рекомендую: имя развилки и номер варианта",
  "2. только словами",
  "3. кнопкой панели",
  "",
  "ответ строкой: «имя 1, имя 2», своими словами или «по рекомендации»",
].join("\n");

// Реплика с блоком: так её вставляет агент после команды --chat.
const withBlock = block;

// Промах копирования блока: агент вопрос упомянул, а блок в реплику не
// вставил. Раньше галочки вовсе не вставали, а панель молчала или врала
// плашкой; теперь панель дописывает те же строки текстом той же формы.
const copyMiss = "Вопрос по развилкам задал, отвечай в ленте.";

// Служебная реплика продления кеша (cachekeep). Приходит после вопроса от
// имени человека и прежде гасила поиск блока с конца: state.seen=false и
// ложная плашка «Заход ждёт ответа».
const cacheKeep = "кэш";

// Обычный нумерованный список плана: галочки на него не вешаются, отметки
// стоят только при строках вариантов вопроса.
const plan = ["Разобрал по порядку.", "", "1. читаю разбор", "2. правлю код"].join("\n");

const at = (n) => new Date(Date.now() - (100 - n) * 60000).toISOString();
const answer = (n, text) => ({ key: "t:" + n, seq: n, role: "assistant", time: at(n), text });
// Реплика человека: её пузырь несёт класс «me». Реплика «кэш» приходит тем же
// путём и не должна снимать галочки.
const mine = (n, text) => ({ key: "t:" + n, seq: n, role: "user", time: at(n), text });

// Признак ожидания, как его отдаёт ручка вопроса и собирает agentAskOf:
// имя развилки едет полем fork, вопрос и варианты полем text и options.
const askOf = (...qs) => {
  const steps = qs.map(([fork, text, ...opts], i) => ({
    name: i + 1 + ". " + text.slice(0, 28),
    now: i === 0,
    fork: fork || "",
    snapshot: "q1-" + (i+1),
    text,
    options: opts.map((opt, j) => ({
      text: opt,
      desc: j === 0 ? "советует агент." : "",
    })),
  }));
  const out = {
    kind: "agent",
    task: "DK-1374",
    tree: "/stand/demo",
    snapshot: steps[0].snapshot,
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
  ["ответ", "«ответ»: какой формы ответ человека?", "имя развилки и номер варианта", "только словами", "кнопкой панели"],
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

// Свободный вопрос совсем без вариантов: текст держит строку, отметок нет.
const askBare = {
  kind: "agent",
  task: "DK-1374",
  since,
  until,
  text: "какой контур у выката?",
};

// Развилка составов кейсов (DK-969). Варианты едут головами составов, а
// раскладка кейсов с номерами лежит в тексте вопроса признака (chatQuestions):
// без неё человек не ответит «состав 2, минус 4» (DK-1318, DK-1235).
const lineupText = [
  "«кейсы»: какие пользовательские кейсы у записи?",
  "",
  "раскладка кейсов развилки «кейсы», по кейсу на строку:",
  "",
  "состав 1, сбалансированный:",
  "1. человек видит три состава",
  "2. кейсы правятся по номерам",
  "3. широкий несёт находки обхода",
  "",
  "состав 2, узкий:",
  "1. человек видит три состава",
  "2. кейсы правятся по номерам",
  "",
  "правка состава: «состав 2, минус 4, плюс <свой кейс>», номер кейса из раскладки выше",
].join("\n");

const askLineup = askOf(
  ["кейсы", lineupText, "сбалансированный состав, 3 кейса", "узкий состав, 2 кейса", "широкий состав, 4 кейса"],
);

const now = { ask, items: [], said: [] };

const { sandbox, timers, streams } = makeSandbox(app, (path, init) => {
  const post = init && init.method === "POST";
  if (post && path.includes("/say")) {
    now.said.push(JSON.parse(init.body));
    return { way: "ask", chat: "task-DK-1374" };
  }
  if (path.includes("/ask")) {
    return now.ask ? { session: SID, task: "DK-1374", ask: now.ask }
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
  project: "demo", addr: SID, sid: SID, task: "DK-1374",
  chats: [chat], entry: chat, models: [],
});

const panelWith = async (items) => {
  sandbox.localStorage.removeItem("devkit.askdraft." + SID);
  sandbox.chatDraftWrite(SID, "");
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
const picksOf = (panel) => allByClass(feedOf(panel), "caskpick");
const click = (node) => node.handlers.click({ stopPropagation: () => {} });
const settleMove = async () => {
  await settle();
  for (const t of timers.splice(0)) t.fn();
  await settle();
};

// --- карточки нет, вопрос стоит текстом в ленте ---
{
  const panel = await panelWith([answer(1, withBlock)]);
  const box = boxOf(panel);
  if (box && !box.hidden) {
    fail("карточка встала над полем ввода: " + dump(box).slice(0, 300));
  }
  const feed = feedOf(panel);
  const said = dump(feed);
  if (!said.includes("кто собирает текст вопроса в чат?")) {
    fail("в ленте нет текста вопроса: " + said.slice(0, 500));
  }
  for (const gone of ["Вопрос ждёт ответа", "Заход ждёт ответа", "не вышло разобрать", "блок вопроса"]) {
    if (said.includes(gone)) fail("в ленте осталась карточная обвязка: " + gone);
  }
}

// --- галочки стоят при строках вариантов в ленте ---
{
  const panel = await panelWith([answer(1, withBlock)]);
  const picks = picksOf(panel);
  if (picks.length !== 5) {
    fail("галочек при строках вариантов не пять, а " + picks.length + ": " +
      dump(feedOf(panel)).slice(0, 700));
  }
  for (const pick of picks) {
    const parent = pick.parentNode;
    if (!parent || parent.tagName !== "LI") {
      fail("галочка стоит не при строке варианта: " + ((parent || {}).tagName || "?"));
    }
    if (!String(parent.className || "").split(" ").includes("casklit")) {
      fail("строка варианта не подсвечена: " + (parent.className || ""));
    }
  }
  if (allByClass(boxOf(panel) || {}, "caskpick").length) {
    fail("галочки висят на карточке, а не в ленте");
  }
}

// --- промах копирования: панель дописывает те же строки текстом той же формы ---
{
  const panel = await panelWith([answer(1, copyMiss)]);
  const feed = feedOf(panel);
  const said = dump(feed);
  for (const want of [
    "вопрос по DK-1374",
    "«печать»: кто собирает текст вопроса в чат?",
    "утилита печатает блок",
    "агент собирает текст",
    "«ответ»: какой формы ответ человека?",
  ]) {
    if (!said.includes(want)) {
      fail("панель не дописала строку «" + want + "» при промахе копирования: " + said.slice(0, 700));
    }
  }
  for (const gone of ["Вопрос ждёт ответа", "Заход ждёт ответа", "не вышло разобрать", "скопируй блок"]) {
    if (said.includes(gone)) fail("плашка про отсутствующий блок вернулась: " + gone);
  }
  const picks = picksOf(panel);
  if (picks.length !== 5) {
    fail("при промахе копирования галочки не встали на строки вариантов: " + picks.length);
  }
}

// --- реплика «кэш» не снимает галочки ---
{
  const panel = await panelWith([answer(1, withBlock), mine(2, cacheKeep)]);
  const picks = picksOf(panel);
  if (picks.length !== 5) {
    fail("реплика «кэш» сняла галочки: " + picks.length + ": " + dump(feedOf(panel)).slice(0, 500));
  }
  const said = dump(feedOf(panel));
  for (const gone of ["Заход ждёт ответа", "не вышло разобрать", "блок вопроса"]) {
    if (said.includes(gone)) fail("ложная плашка встала от реплики «кэш»: " + gone);
  }
}

// --- своя реплика человека не снимает галочки ---
{
  const panel = await panelWith([answer(1, withBlock), mine(2, "поясню словами")]);
  if (picksOf(panel).length !== 5) {
    fail("своя реплика сняла галочки: " + picksOf(panel).length);
  }
}

// --- отметка собирает строку ответа «имя N», снятая её убирает ---
{
  const panel = await panelWith([answer(1, withBlock)]);
  const ta = sayOf(panel);
  const picks = picksOf(panel);
  click(picks[0]);
  if (ta.value !== "") {
    fail("отметка собрала строку ответа не так: " + JSON.stringify(ta.value));
  }
  if (!String(picks[0].className).split(" ").includes("on")) fail("отмеченная галочка не помечена");
  if (picks[0].attrs["aria-checked"] !== "true") fail("отметка не названа для чтения с экрана");
  click(picks[4]);
  if (ta.value !== "") {
    fail("вторая отметка легла в строку ответа не так: " + JSON.stringify(ta.value));
  }
  click(picks[0]);
  if (ta.value !== "") {
    fail("снятая отметка не убралась из поля ввода: " + JSON.stringify(ta.value));
  }
  click(picks[4]);
  if (ta.value !== "") fail("поле ввода не опустело со снятой отметкой: " + JSON.stringify(ta.value));

  ta.value = "сначала поясню";
  click(picks[1]);
  if (ta.value !== "сначала поясню") {
    fail("строка ответа затёрла написанное человеком: " + JSON.stringify(ta.value));
  }
  click(picks[1]);
  if (ta.value !== "сначала поясню") {
    fail("снятая отметка унесла слова человека: " + JSON.stringify(ta.value));
  }
}

// --- отправляет обычная кнопка чата ---
{
  const panel = await panelWith([answer(1, withBlock)]);
  const ta = sayOf(panel);
  click(picksOf(panel)[3]);
  if (ta.value !== "") fail("строка ответа не собралась: " + JSON.stringify(ta.value));
  deepBtn(byClass(panel, "crow"), "Отправить").handlers.click({});
  await settle();
  if (now.said.length !== 1 || now.said[0].text !== "" || now.said[0].selection.questions[0].fork !== "ответ" || now.said[0].selection.questions[0].ids[0] !== "2") {
    fail("ответ уехал не обычной репликой чата: " + JSON.stringify(now.said));
  }
  if (ta.value !== "") fail("поле ввода не опустело после отправки: " + JSON.stringify(ta.value));
}

// --- ответа не ждут: галочек нет ---
{
  now.ask = null;
  const panel = await panelWith([answer(1, withBlock)]);
  await settleMove();
  if (picksOf(panel).length) fail("галочки стоят без признака ожидания");
  const box = boxOf(panel);
  if (box && !box.hidden) fail("карточка стоит без признака ожидания: " + dump(box).slice(0, 300));
  now.ask = ask;
}

// --- снятие признака уносит галочки и строку ответа ---
{
  const panel = await panelWith([answer(1, withBlock)]);
  const ta = sayOf(panel);
  click(picksOf(panel)[0]);
  if (ta.value !== "") fail("строка ответа не собралась перед снятием признака");
  click(deepBtn(panel, "Убрать выбор из сообщения"));
  now.ask = null;
  await settleMove();
  if (picksOf(panel).length) fail("галочки остались после снятого признака");
  if (ta.value !== "") fail("строка ответа осталась в поле ввода: " + JSON.stringify(ta.value));
  now.ask = ask;
}

// --- свободный вопрос с вариантами: текст есть, галочек нет ---
{
  now.ask = askFree;
  const panel = await panelWith([answer(1, copyMiss)]);
  const feed = feedOf(panel);
  const said = dump(feed);
  if (!said.includes("чем держать очередь выката?")) {
    fail("панель не показала свободный вопрос: " + said.slice(0, 500));
  }
  if (!said.includes("замком конвейера")) {
    fail("панель потеряла варианты свободного вопроса: " + said.slice(0, 500));
  }
  if (picksOf(panel).length) {
    fail("свободный вопрос получил галочки: " + picksOf(panel).length);
  }
  for (const gone of ["Вопрос ждёт ответа", "Заход ждёт ответа"]) {
    if (said.includes(gone)) fail("свободный вопрос оброс чужой обвязкой: " + gone);
  }
  now.ask = ask;
}

// --- свободный вопрос без вариантов: текст держит строку ---
{
  now.ask = askBare;
  const panel = await panelWith([answer(1, copyMiss)]);
  const said = dump(feedOf(panel));
  if (!said.includes("какой контур у выката?")) {
    fail("панель не показала текст вопроса без вариантов: " + said.slice(0, 400));
  }
  if (picksOf(panel).length) fail("у вопроса без вариантов есть галочки");
  now.ask = ask;
}

// --- одна развилка: fork читается из общего поля, steps не нужен ---
{
  now.ask = askOne;
  const panel = await panelWith([answer(1, withBlock)]);
  const ta = sayOf(panel);
  const picks = picksOf(panel);
  if (picks.length !== 2) fail("у одной развилки не две галочки, а " + picks.length);
  click(picks[1]);
  if (ta.value !== "") {
    fail("ответ одиночного вопроса собрался не так: " + JSON.stringify(ta.value));
  }
  now.ask = ask;
}

// --- в признаке пачка вопросов: галочки стоят на всех вариантах ---
{
  const panel = await panelWith([answer(1, withBlock)]);
  const said = dump(feedOf(panel));
  for (const want of ["кто собирает текст вопроса в чат?", "какой формы ответ человека?",
    "утилита печатает блок", "имя развилки и номер", "кнопкой панели", "агент собирает текст"]) {
    if (!said.includes(want)) {
      fail("лента потеряла из пачки «" + want + "»: " + said.slice(0, 600));
    }
  }
  if (picksOf(panel).length !== 5) {
    fail("на пачке не пять галочек, а " + picksOf(panel).length);
  }
}

// --- раскладка кейсов в ленте: номера читаются для правки «минус 4» ---
{
  now.ask = askLineup;
  const panel = await panelWith([answer(1, copyMiss)]);
  const said = dump(feedOf(panel));
  for (const want of [
    "сбалансированный состав, 3 кейса",
    "узкий состав, 2 кейса",
    "человек видит три состава",
    "кейсы правятся по номерам",
    "широкий несёт находки обхода",
    "состав 2, узкий:",
    "правка состава: «состав 2, минус 4, плюс <свой кейс>»",
  ]) {
    if (!said.includes(want)) {
      fail("лента потеряла раскладку «" + want + "»: " + said.slice(0, 700));
    }
  }
  if (picksOf(panel).length !== 3) {
    fail("галочки составов не три, а " + picksOf(panel).length);
  }
  const ta = sayOf(panel);
  click(picksOf(panel)[1]);
  if (ta.value !== "") {
    fail("отметка состава собрала не ту строку ответа: " + JSON.stringify(ta.value));
  }
  now.ask = ask;
}

// --- галочки на нумерованном списке плана не вешаются ---
{
  const panel = await panelWith([answer(1, plan)]);
  const feed = feedOf(panel);
  const said = dump(feed);
  if (!said.includes("читаю разбор")) fail("лента не показала план: " + said.slice(0, 300));
  const picks = picksOf(panel);
  if (picks.length !== 5) {
    fail("галочки вешаются на чужой нумерованный список: " + picks.length);
  }
  for (const pick of picks) {
    const parent = pick.parentNode;
    if (parent && parent.tagName === "LI" && dump(parent).includes("читаю разбор")) {
      fail("галочка села на строку плана");
    }
  }
}

// --- старый список с теми же словами вариантов не крадёт галочки ---
{
  // Прошлый вопрос о слиянии уже стоит в ленте словами «да» и «нет».
  // Новый вопрос спрашивает теми же словами, и агент блок не вставил.
  // Галочки обязаны сесть на допись нового вопроса, старый список цел.
  now.ask = askOf(
    ["сход", "«сход»: сливать в main сейчас?", "да", "нет"],
  );
  const panel = await panelWith([
    answer(1, [metaLine, "", "«сход»: сливать в main?", "1. рекомендую: да", "2. нет"].join("\n")),
    answer(2, copyMiss),
  ]);
  const feed = feedOf(panel);
  const said = dump(feed);
  if (!said.includes("сливать в main сейчас?")) {
    fail("панель не дописала новый вопрос: " + said.slice(0, 500));
  }
  const picks = picksOf(panel);
  if (picks.length !== 2) {
    fail("галочек у нового вопроса не две, а " + picks.length + ": " + said.slice(0, 700));
  }
  const fb = byClass(feed, "askfb");
  if (!fb) {
    fail("панель не дописала строки нового вопроса");
  }
  for (const pick of picks) {
    const parent = pick.parentNode;
    if (!parent) fail("галочка без строки");
    if (!fb.contains(parent)) {
      fail("галочка села на чужой список вместо дописи");
    }
  }
  now.ask = ask;
}

// --- частично переписанные варианты уводят все галочки на допись ---
{
  // Агент переписал строку варианта, и в реплике осталось два слова из трёх.
  // Частичный набор не считается находкой, все галочки идут на допись.
  now.ask = askOf(
    ["сход", "«сход»: сливать в main сейчас?", "да", "нет", "может быть"],
  );
  const panel = await panelWith([
    answer(1, [metaLine, "", "«сход»: сливать в main сейчас?", "1. да", "2. нет"].join("\n")),
  ]);
  const feed = feedOf(panel);
  const said = dump(feed);
  if (!said.includes("может быть")) {
    fail("панель не дописала пропавший вариант: " + said.slice(0, 500));
  }
  const picks = picksOf(panel);
  if (picks.length !== 3) {
    fail("при частичном совпадении галочек не три, а " + picks.length + ": " + said.slice(0, 700));
  }
  const fb = byClass(feed, "askfb");
  if (!fb) fail("при частичном совпадении нет дописи");
  for (const pick of picks) {
    if (!fb.contains(pick.parentNode)) {
      fail("галочка села на частичный список вместо дописи");
    }
  }
  now.ask = ask;
}

// --- строка с задачей и временем стоит у вопроса текстом ---
{
  // Блок без строки меты (старая копия): панель подписывает вопрос строкой
  // с задачей и временем, карточку с шапкой она не рисует.
  const panel = await panelWith([answer(1, [
    "«печать»: кто собирает текст вопроса в чат?",
    "1. рекомендую: утилита печатает блок",
    "2. агент собирает текст",
  ].join("\n"))]);
  const said = dump(feedOf(panel));
  if (!said.includes("вопрос по DK-1374") || !said.includes("спросил в")) {
    fail("в ленте нет строки с задачей и временем: " + said.slice(0, 500));
  }
  for (const gone of ["Вопрос ждёт ответа", "Заход ждёт ответа"]) {
    if (said.includes(gone)) fail("вместо строки меты встала карточка: " + gone);
  }
}

console.log("poc_askpick: вопрос идёт текстом в ленте, галочки стоят при строках вариантов, " +
  "строки подсвечены, при промахе копирования панель дописывает те же строки, " +
  "чужой список с теми же словами не крадёт галочки, частичный набор уводит их на допись, " +
  "строка с задачей и временем стоит текстом, реплика «кэш» не снимает галочек, " +
  "отметка собирает «имя N»");
