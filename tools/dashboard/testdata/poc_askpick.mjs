// Стенд галочек при строках вариантов (DK-864).
//
// Вопрос человеку приходит текстом в ленте: блок печатает утилита из перечня
// развилок записи, агент вставляет его в реплику как есть. Панель по признаку
// ожидания добавляет к такой реплике одну галочку слева от каждой строки
// варианта. Прежде на её месте стоял блок с кнопками и табами: он читался
// формой поверх разговора, запирал чат до ответа и терял рекомендацию по
// дороге (пять претензий пользователя).
//
// Предмет стенда: галочка встаёт при каждой строке варианта и только при ней,
// отметка собирает строку ответа в поле ввода, снятая её оттуда убирает, а
// прежнего блока с кнопками, табами и своей отправкой нет вовсе.
//
// Зовётся: node testdata/poc_askpick.mjs static/app.js

import { makeSandbox, settle, dump, byClass, allByClass, deepBtn, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const SID = "aaaa1111-1111-4111-8111-111111111111";
const until = Math.floor(Date.now() / 1000) + 300;

// Блок вопроса слово в слово таким, каким его печатает taskctl decide --chat.
const block = [
  "Спросил по развилкам записи.",
  "",
  "«печать»: кто собирает текст вопроса в чат?",
  "1. рекомендую: утилита печатает блок из перечня развилок",
  "2. агент собирает текст по шаблону скилла",
  "",
  "«ответ»: какой формы ответ человека?",
  "1. рекомендую: имя развилки и номер варианта",
  "2. только словами",
  "3. кнопкой панели",
  "",
  "ответ строкой: «имя 1, имя 2», своими словами или «по рекомендации»",
].join("\n");

// Блок первого раунда интервью: развилка составов кейсов, а под последней
// строкой блока раскладка кейсов по одному на строку (DK-969). Номера в
// раскладке тоже стоят в начале строки, и галочек они не получают: отмечают
// составы, а кейсы человек правит номерами прямо в строке ответа.
const cases = [
  "Спросил про кейсы записи.",
  "",
  "«кейсы»: какие кейсы у DK-969? рекомендую сбалансированный: узкий теряет кейс (3)",
  "1. рекомендую: сбалансированный состав, 3 кейса",
  "2. узкий состав, 2 кейса",
  "3. широкий состав, 4 кейса",
  "",
  "ответ строкой: «имя 1, имя 2», своими словами или «по рекомендации»",
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

// Тот же блок, завёрнутый агентом в ограждение кода (DK-892). Общие правила
// письма харнеса кладут сниппеты в тройные кавычки, и на блок вопроса они
// наползают тоже: так вышло у DK-891 и у DK-1177. Рисовка собирает ограждённое
// одним узлом pre, пунктов списка внутри не появляется, и галочкам сесть было
// некуда.
const fenced = [
  "Спросил по развилкам записи.",
  "",
  "```",
  "«печать»: кто собирает текст вопроса в чат?",
  "1. рекомендую: утилита печатает блок из перечня развилок",
  "2. агент собирает текст по шаблону скилла",
  "",
  "«ответ»: какой формы ответ человека?",
  "1. рекомендую: имя развилки и номер варианта",
  "2. только словами",
  "3. кнопкой панели",
  "",
  "ответ строкой: «имя 1, имя 2», своими словами или «по рекомендации»",
  "```",
].join("\n");

// Реплика с ограждённым блоком и настоящим сниппетом команды рядом: снимается
// ограждение только вокруг блока, а команда остаётся кодом.
const withCode = [
  "Спросил, а команду показываю заодно.",
  "",
  "```sh",
  "taskctl decide DK-864 --chat",
  "```",
  "",
  "```",
  "«печать»: кто собирает текст вопроса в чат?",
  "1. рекомендую: утилита печатает блок из перечня развилок",
  "2. агент собирает текст по шаблону скилла",
  "",
  "ответ строкой: «имя 1, имя 2», своими словами или «по рекомендации»",
  "```",
].join("\n");

// Реплика без блока: обычный нумерованный список плана. Галочкам тут делать
// нечего, и стенд следит, чтобы панель не вешала их на всякий список подряд.
const plan = ["Разобрал по порядку.", "", "1. читаю разбор", "2. правлю код"].join("\n");

// Пересказ прошлого вопроса: имя развилки и номера на месте, а последней
// строки блока нет. Отвечать тут не на что, вопрос давно закрыт, и галочки
// звали бы к ответу второй раз.
const retell = [
  "Напомню, о чём спрашивал вчера.",
  "",
  "«печать»: кто собирает текст вопроса в чат?",
  "1. утилита печатает блок из перечня развилок",
  "2. агент собирает текст по шаблону скилла",
].join("\n");

const at = (n) => new Date(Date.now() - (100 - n) * 60000).toISOString();
const answer = (n, text) => ({ key: "t:" + n, seq: n, role: "assistant", time: at(n), text });

// Признак ожидания, как его отдаёт ручка вопроса: заход спросил человека и
// стоит. Вариантами тут панель не рисует ничего, они уже стоят в реплике.
const ask = {
  kind: "agent",
  task: "DK-864",
  until,
  text: "«печать»: кто собирает текст вопроса в чат?",
  options: [{ text: "утилита печатает блок" }, { text: "агент собирает текст" }],
};

const now = { ask, items: [], said: [] };

const { sandbox, timers } = makeSandbox(app, (path, init) => {
  const post = init && init.method === "POST";
  if (post && path.includes("/say")) {
    now.said.push(JSON.parse(init.body).text);
    return { way: "ask", chat: "task-DK-864" };
  }
  if (path.includes("/ask")) {
    return now.ask ? { session: SID, task: "DK-864", ask: now.ask }
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
  project: "demo", addr: SID, sid: SID, task: "DK-864",
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
const picksOf = (panel) => allByClass(feedOf(panel), "caskpick");
const click = (node) => node.handlers.click({ stopPropagation: () => {} });
const settleMove = async () => {
  await settle();
  for (const t of timers.splice(0)) t.fn();
  await settle();
};

// --- галочка стоит при каждой строке варианта и только при ней ---
{
  const panel = await panelWith([answer(1, block)]);
  const picks = picksOf(panel);
  if (picks.length !== 5) {
    fail("галочек при вариантах не пять, а " + picks.length + ": " + dump(feedOf(panel)).slice(0, 500));
  }
  // Галочка стоит внутри самой строки варианта, а не отдельным блоком рядом.
  for (const pick of picks) {
    if (!pick.parentNode || pick.parentNode.tagName !== "LI") {
      fail("галочка стоит не при строке варианта: " + (pick.parentNode || {}).tagName);
    }
    if (pick.parentNode.children[0] !== pick) fail("галочка стоит не слева от слов варианта");
  }
  // Кроме галочки панель не добавляет ничего: ни рамки, ни шапки, ни табов, ни
  // своей кнопки отправки.
  const box = byClass(panel, "cask");
  if (box && !box.hidden) fail("прежний блок вопроса стоит над полем ввода: " + dump(box).slice(0, 400));
  const feed = dump(feedOf(panel));
  for (const gone of ["Вопрос от задачи", "Отправить ответ", "Обсудить в чате", "осталось"]) {
    if (feed.includes(gone)) fail("в реплике осталась обвязка прежнего блока: " + gone);
  }
  if (allByClass(feedOf(panel), "ktab").length) fail("шаги вопроса остались табами в ленте");
  if (deepBtn(feedOf(panel), "Отправить")) fail("у вопроса осталась своя кнопка отправки");
  // Слова блока стоят в ленте как есть: галочка ничего из них не съела.
  if (!feed.includes("утилита печатает блок из перечня развилок")) {
    fail("слова варианта потерялись из реплики: " + feed.slice(0, 400));
  }
}

// --- блок в ограждении кода: галочки встают всё равно ---
{
  const panel = await panelWith([answer(1, fenced)]);
  const picks = picksOf(panel);
  if (picks.length !== 5) {
    fail("галочек при ограждённом блоке не пять, а " + picks.length + ": " +
      dump(feedOf(panel)).slice(0, 600));
  }
  for (const pick of picks) {
    if (!pick.parentNode || pick.parentNode.tagName !== "LI") {
      fail("галочка при ограждённом блоке стоит не при строке варианта: " +
        (pick.parentNode || {}).tagName);
    }
  }
  // Ограждение ушло вместе с рисовкой: блок читается словами, а не кодом.
  if (allByClass(feedOf(panel), "mdcode").length) {
    fail("блок вопроса остался куском кода: " + dump(feedOf(panel)).slice(0, 400));
  }
  const ta = sayOf(panel);
  click(picks[0]);
  if (ta.value !== "печать 1") {
    fail("отметка ограждённого блока собрала строку ответа не так: " + JSON.stringify(ta.value));
  }
  // Блок разобран, и подсказка вместо галочек тут не нужна ни на каком круге.
  await settleMove();
  await settleMove();
  const box = boxOf(panel);
  if (box && !box.hidden) fail("подсказка встала при разобранном блоке: " + dump(box).slice(0, 400));
}

// --- сниппет команды рядом с блоком остаётся кодом ---
{
  const panel = await panelWith([answer(1, withCode)]);
  if (picksOf(panel).length !== 2) {
    fail("галочек при блоке рядом с командой не две, а " + picksOf(panel).length);
  }
  const code = allByClass(feedOf(panel), "mdcode");
  if (code.length !== 1) fail("кусков кода в реплике не один, а " + code.length);
  if (!dump(code[0]).includes("taskctl decide DK-864 --chat")) {
    fail("кодом осталось не то ограждение: " + dump(code[0]).slice(0, 300));
  }
}

// --- отметка собирает строку ответа в поле ввода, снятая её убирает ---
{
  const panel = await panelWith([answer(1, block)]);
  const ta = sayOf(panel);
  const picks = picksOf(panel);
  click(picks[0]);
  if (ta.value !== "печать 1") fail("отметка собрала строку ответа не так: " + JSON.stringify(ta.value));
  if (!String(picks[0].className).split(" ").includes("on")) fail("отмеченная галочка не помечена");
  if (picks[0].attrs["aria-checked"] !== "true") fail("отметка не названа для чтения с экрана");
  // Вторая развилка дописывается в ту же строку через запятую: этот вид
  // разбирает taskctl decide --answer.
  click(picks[4]);
  if (ta.value !== "печать 1, ответ 3") {
    fail("вторая отметка легла в строку ответа не так: " + JSON.stringify(ta.value));
  }
  // Снятая отметка уносит свой кусок и не трогает соседний.
  click(picks[0]);
  if (ta.value !== "ответ 3") {
    fail("снятая отметка не убралась из поля ввода: " + JSON.stringify(ta.value));
  }
  click(picks[4]);
  if (ta.value !== "") fail("поле ввода не опустело со снятой отметкой: " + JSON.stringify(ta.value));

  // Написанное человеком отметка не затирает и не уносит с собой.
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

// --- отправляет обычная кнопка чата, своей у галочек нет ---
{
  const panel = await panelWith([answer(1, block)]);
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

// --- ответа не ждут: галочек нет ни при каком списке ---
{
  now.said.length = 0;
  now.ask = null;
  const panel = await panelWith([answer(1, block)]);
  await settleMove();
  if (picksOf(panel).length) fail("галочки стоят без признака ожидания");
  now.ask = ask;
}

// --- галочки идут на составы, а раскладка кейсов их не получает ---
{
  const panel = await panelWith([answer(1, cases)]);
  const picks = picksOf(panel);
  if (picks.length !== 3) {
    fail("галочек не три, по составу на каждую: " + picks.length + "\n" + dump(feedOf(panel)).slice(0, 600));
  }
  const ta = sayOf(panel);
  click(picks[1]);
  if (ta.value !== "кейсы 2") fail("состав лёг в строку ответа не так: " + JSON.stringify(ta.value));
  // Кейсы раскладки остались словами: правит их человек номерами в строке
  // ответа, «состав 2, минус 4, плюс свой».
  const feed = dump(feedOf(panel));
  if (!feed.includes("широкий несёт находки обхода")) {
    fail("раскладка кейсов потерялась из реплики: " + feed.slice(0, 400));
  }
}

// --- обычный нумерованный список галочек не получает ---
{
  const panel = await panelWith([answer(1, plan)]);
  if (picksOf(panel).length) fail("галочки повесились на список, который вопросом не был");
}

// --- пересказ прошлого вопроса галочек не получает ---
{
  const panel = await panelWith([answer(1, retell)]);
  if (picksOf(panel).length) {
    fail("галочки повесились на пересказ вопроса без последней строки блока");
  }
}

// --- вопросов в ленте два: галочки идут при последнем ---
{
  const second = block.replace("«печать»", "«выкат»").replace("«ответ»", "«срок»");
  const panel = await panelWith([answer(1, block), answer(2, second)]);
  const ta = sayOf(panel);
  const picks = picksOf(panel);
  if (picks.length !== 5) fail("галочек при последнем вопросе не пять, а " + picks.length);
  click(picks[0]);
  if (ta.value !== "выкат 1") {
    fail("отмечен вариант отвеченного вопроса: " + JSON.stringify(ta.value));
  }
}

// --- галочки идут при живом блоке, а не при пересказе выше него ---
{
  const panel = await panelWith([answer(1, retell), answer(2, block)]);
  const picks = picksOf(panel);
  if (picks.length !== 5) fail("галочек при живом блоке не пять, а " + picks.length);
  const ta = sayOf(panel);
  click(picks[0]);
  if (ta.value !== "печать 1") fail("отмечен вариант не того блока: " + JSON.stringify(ta.value));
}

// --- ответ снял ожидание: галочки уходят с отвеченного блока ---
{
  const panel = await panelWith([answer(1, block)]);
  const ta = sayOf(panel);
  click(picksOf(panel)[0]);
  if (ta.value !== "печать 1") fail("строка ответа не собралась перед снятием признака");
  now.ask = null;
  await settleMove();
  if (picksOf(panel).length) fail("галочки остались при отвеченном блоке");
  if (ta.value !== "") fail("строка ответа осталась в поле ввода: " + JSON.stringify(ta.value));
  now.ask = ask;
}

// --- признак живой, а блока в реплике нет: панель говорит это словами ---
{
  const panel = await panelWith([answer(1, retell)]);
  const box = boxOf(panel);
  // Первый круг молчит: реплика с блоком приходит позже признака, и подсказка
  // с первого круга мигала бы на каждом штатном вопросе.
  if (!box) fail("узла блока вопроса в панели нет вовсе");
  if (!box.hidden) fail("подсказка встала с первого круга опроса: " + dump(box).slice(0, 400));
  await settleMove();
  if (box.hidden) fail("панель промолчала о живом признаке без разобранного блока");
  const said = dump(box);
  // Вопрос человек читает словами признака, а рядом стоит, чем отвечать.
  if (!said.includes("кто собирает текст вопроса в чат?")) {
    fail("подсказка не назвала вопрос признака: " + said.slice(0, 500));
  }
  if (!said.includes("обычной репликой")) {
    fail("подсказка не сказала, чем отвечать: " + said.slice(0, 500));
  }
  if (!said.includes("утилита печатает блок")) {
    fail("подсказка не назвала варианты признака: " + said.slice(0, 500));
  }
  // Кнопок у подсказки нет: ответ уезжает обычной репликой чата.
  if (deepBtn(box, "Отправить")) fail("у подсказки своя кнопка отправки");
  if (allByClass(box, "caskopt").length) fail("варианты подсказки стали кнопками");
  // Ответа больше не ждут, значит и подсказке места нет.
  now.ask = null;
  await settleMove();
  if (!box.hidden) fail("подсказка осталась после снятого признака");
  now.ask = ask;
}

console.log("poc_askpick: галочка при каждой строке варианта, строка ответа в поле ввода, " +
  "снятая отметка её уносит, ограждённый блок отмечается наравне с открытым, " +
  "а живой признак без блока оборачивается подсказкой");
