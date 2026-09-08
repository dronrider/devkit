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

// Реплика без блока: обычный нумерованный список плана. Галочкам тут делать
// нечего, и стенд следит, чтобы панель не вешала их на всякий список подряд.
const plan = ["Разобрал по порядку.", "", "1. читаю разбор", "2. правлю код"].join("\n");

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

// --- обычный нумерованный список галочек не получает ---
{
  const panel = await panelWith([answer(1, plan)]);
  if (picksOf(panel).length) fail("галочки повесились на список, который вопросом не был");
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

console.log("poc_askpick: галочка при каждой строке варианта, строка ответа в поле ввода, " +
  "снятая отметка её уносит, прежнего блока с кнопками нет");
