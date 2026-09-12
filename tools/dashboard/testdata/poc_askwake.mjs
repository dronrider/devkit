// Стенд живучести опроса вопроса при обрыве связи (DK-964).
//
// Живой случай: чат груминга 2026-09-12, агент спросил в 14:54, человек
// ответил в 15:36, а галочки при строках вариантов встали только после
// перезагрузки страницы. Опрос вопроса (watchClientAsk) ходит на сервер раз в
// ASK_POLL без try/catch вокруг самого запроса: первый же отвергнутый fetch
// (обрыв связи, сон ноутбука, внешний вход) обрывал цепочку setTimeout, и
// следующий шаг не планировался никогда. Индикатор занятости рядом (tick
// около строки 10396) это переживает.
//
// Предмет стенда: опрос переживает один отвергнутый fetch подряд, следующий
// шаг планируется сам, и, дождавшись его, панель вешает галочки при строках
// вариантов без перезагрузки.
//
// Зовётся: node testdata/poc_askwake.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, dump, fail, appPathArg }
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

const at = (n) => new Date(Date.now() - (100 - n) * 60000).toISOString();
const answer = (n, text) => ({ key: "t:" + n, seq: n, role: "assistant", time: at(n), text });

const ask = {
  kind: "agent",
  task: "DK-964",
  until,
  text: "«печать»: кто собирает текст вопроса в чат?",
  options: [{ text: "утилита печатает блок" }, { text: "агент собирает текст" }],
};

// Первый запрос ручки /ask отвергается, как отвергается fetch при обрыве
// связи: reply бросает исключение синхронно внутри мока fetch, и await api()
// в watchClientAsk встречает ровно ту же неудачу, что настоящий разрыв сети.
let askCalls = 0;
const now = { items: [] };

const { sandbox, timers } = makeSandbox(app, (path, init) => {
  if (path.includes("/ask")) {
    askCalls += 1;
    if (askCalls === 1) throw new TypeError("Failed to fetch");
    return { session: SID, task: "DK-964", ask };
  }
  if (path.includes("/sessions/")) {
    return { session: SID, head: { id: SID }, items: now.items, total: now.items.length };
  }
  if (path.includes("/chats")) return { chats: [], models: [], days: 3, older: false };
  return {};
});

const chat = { id: SID, project: "demo", title: "разбор развилок", state: "live", idle: true, tasks: [] };
const liveSt = () => ({
  project: "demo", addr: SID, sid: SID, task: "DK-964",
  chats: [chat], entry: chat, models: [],
});

now.items = [answer(1, block)];
sandbox.location.hash = "#demo/chat/" + SID;
const panel = sandbox.chatPanel("demo", liveSt());
await settle();
await settle();

const feedOf = () => byClass(panel, "chatfeed");
const picksOf = () => allByClass(feedOf(), "caskpick");

// --- первый отказ /ask не роняет галочки, но и не должен обрывать опрос ---
if (askCalls !== 1) fail("первый шаг опроса не ушёл на сервер: вызовов /ask " + askCalls);
if (picksOf().length) fail("галочки встали без успешного ответа /ask");

// --- шаг опроса перепланирован после отказа ---
// Тут и падал старый код: await api(...) бросал исключение без try/catch,
// цепочка setTimeout обрывалась, и следующего шага в очереди таймеров не
// было вовсе до самой перезагрузки страницы.
const askTick = timers.find((t) => t.ms === 3000 && t.fn);
if (!askTick) {
  fail("опрос вопроса не перепланировал следующий шаг после отвергнутого fetch: " +
    "вопрос стоял бы до перезагрузки страницы, как в живом случае DK-964");
}

// --- следующий шаг опроса приходит успешным ответом и вешает галочки ---
askTick.fn();
askTick.fn = null;
await settle();
await settle();
if (askCalls !== 2) fail("следующий шаг опроса не дошёл до сервера: вызовов /ask " + askCalls);
const picks = picksOf();
if (picks.length !== 5) {
  fail("галочек при вариантах не пять после восстановления опроса, а " + picks.length + ": " +
    dump(feedOf()).slice(0, 500));
}

console.log("poc_askwake: опрос вопроса переживает отвергнутый fetch и вешает галочки без перезагрузки");
