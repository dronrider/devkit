// Стенд чипов непрочитанных ответов (замечание пользователя: в плотном потоке
// замечаний ответ агента теряется среди ходов инструментов, человек спрашивает,
// куда лёг ответ, и ищет его прокруткой).
//
// Предмет проверки: чип появляется на пришедший и непрочитанный ответ, показ
// ответа на экране гасит чип сам, крестик гасит принудительно, пачка реплик с
// одним ответом даёт чипы в один якорь, а пока агент работает, чипа нет вовсе.
//
// Зовётся: node testdata/poc_unread.mjs static/app.js

import { makeSandbox, makeNode, settle, dump, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const at = (n) => new Date(Date.now() - (100 - n) * 60000).toISOString();

// Лента разговора приезжает записями сервера: у каждой свой ключ, по нему
// панель и держит узел записи.
const ask = (n, text) => ({ key: "t:" + n, seq: n, role: "user", time: at(n), text });
const answer = (n, text) => ({ key: "t:" + n, seq: n, role: "assistant", time: at(n), text });
const tool = (n, name) => ({ key: "t:" + n, seq: n, role: "tool", time: at(n), tool: name,
  note: "разбор", text: "command: " + name });

let items = [];
const { sandbox, store, seen } = makeSandbox(app, (path) => {
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items, total: items.length };
  }
  if (path.includes("/chats")) return { chats: [], models: [], days: 3, older: false };
  return {};
});
store.set("devkit.chat.filter", "0");

const chat = { id: "aaaa1111-1111", project: "demo", title: "разбор замечаний",
  state: "live", idle: true, tasks: [] };

// panelWith поднимает панель на заданной ленте и отдаёт её узлы.
const panelWith = async (list) => {
  items = list;
  sandbox.location.hash = "#demo/chat/aaaa1111-1111";
  const st = { project: "demo", addr: "aaaa1111-1111", sid: "aaaa1111-1111",
    chats: [chat], entry: chat, models: [] };
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  await settle();
  return { panel, feed: byClass(panel, "chatfeed"), strip: byClass(panel, "cunread") };
};

const chips = (strip) => allByClass(strip, "cuchip");
// Записи лежат не прямо в ленте, а в её коробке mlist: узел ищется по ключу
// той же функцией, которой его ищет сама панель.
const nodeOf = (feed, key) => sandbox.findKey(feed, "k-" + key);

// --- пока агент работает, чипа нет ---
{
  const { panel, strip } = await panelWith([ask(1, "перепиши абзац про архив"), tool(2, "Bash")]);
  if (!strip) fail("полоски непрочитанных нет в панели: " + dump(panel).slice(0, 300));
  if (chips(strip).length) {
    fail("чип встал на реплику, ответа на которую ещё нет: " + dump(strip).slice(0, 300));
  }
  if (!strip.hidden) fail("пустая полоска непрочитанных занимает место над строкой отправки");
}

// --- пришедший ответ даёт чип, и чип ведёт к ответу ---
{
  const { strip, feed } = await panelWith([
    ask(1, "перепиши абзац про архив"),
    tool(2, "Bash"),
    answer(3, "Переписал, стало короче."),
  ]);
  const got = chips(strip);
  if (got.length !== 1) fail("на непрочитанный ответ чипа нет: " + dump(strip).slice(0, 300));
  if (!dump(got[0]).includes("перепиши абзац про архив")) {
    fail("чип не назван началом реплики человека: " + dump(got[0]));
  }
  if (strip.hidden) fail("полоска с чипом спрятана");
  // Нажатие ведёт к ответу и гасит чип: человек его увидел.
  const node = nodeOf(feed, "t:3");
  if (!node) fail("узла ответа нет в ленте: " + dump(feed).slice(0, 300));
  let went = null;
  node.scrollIntoView = (opts) => { went = opts || {}; };
  byClass(got[0], "cuask").handlers.click({ stopPropagation: () => {} });
  if (!went) fail("нажатие чипа не увело ленту к ответу");
  if (chips(strip).length) fail("чип не погас после перехода к ответу");
  if (!store.get("devkit.chat.read.aaaa1111-1111").includes("k-t:3")) {
    fail("прочитанное не легло в память вкладки: " +
      JSON.stringify(store.get("devkit.chat.read.aaaa1111-1111")));
  }
}

// --- показ ответа на экране гасит чип сам ---
{
  store.set("devkit.chat.read.aaaa1111-1111", "[]");
  const { strip, feed } = await panelWith([
    ask(1, "почему стенд зелёный на старом коде"),
    answer(2, "Потому что проверка стояла не на том поле."),
  ]);
  if (chips(strip).length !== 1) fail("чипа нет до показа ответа: " + dump(strip).slice(0, 300));
  seen(nodeOf(feed, "t:2"));
  if (chips(strip).length) fail("показ ответа на экране не погасил чип: " + dump(strip).slice(0, 300));
}

// --- крестик гасит принудительно ---
{
  store.set("devkit.chat.read.aaaa1111-1111", "[]");
  const { strip } = await panelWith([
    ask(1, "сверь отбойник кеша"),
    answer(2, "Сменился."),
  ]);
  const got = chips(strip);
  if (got.length !== 1) fail("чипа нет перед проверкой крестика: " + dump(strip).slice(0, 300));
  const off = byClass(got[0], "cuoff");
  if (!off) fail("у чипа нет крестика: " + dump(got[0]));
  off.handlers.click({ stopPropagation: () => {} });
  if (chips(strip).length) fail("крестик не погасил чип: " + dump(strip).slice(0, 300));
}

// --- пачка реплик с одним ответом даёт чипы в один якорь ---
{
  store.set("devkit.chat.read.aaaa1111-1111", "[]");
  const { strip } = await panelWith([
    ask(1, "первое замечание про архив"),
    ask(2, "второе замечание про чипы"),
    ask(3, "третье замечание про пузырь"),
    tool(4, "Read"),
    answer(5, "Разобрал все три разом."),
  ]);
  const got = chips(strip);
  if (got.length !== 3) fail("пачка реплик дала чипов не по числу реплик: " + got.length);
  const said = got.map((c) => dump(c)).join(" ");
  for (const what of ["первое замечание", "второе замечание", "третье замечание"]) {
    if (!said.includes(what)) fail("чипа на реплику «" + what + "» нет: " + said.slice(0, 300));
  }
  // Якорь у всех трёх один: раздваивать ответ незачем, гасятся они вместе.
  const pairs = sandbox.chatAskPairs([
    ask(1, "первое"), ask(2, "второе"), answer(3, "ответ"),
  ]);
  if (pairs.length !== 2 || pairs[0].answer !== pairs[1].answer) {
    fail("пачка реплик ведёт не в один ответ: " + JSON.stringify(pairs.map((p) => p.answer.key)));
  }
}

// --- служебное ответом не считается, а отправка человеку считается ---
{
  if (sandbox.chatIsAnswer(tool(1, "Bash"))) fail("ход инструмента сочтён ответом агента");
  if (sandbox.chatIsAnswer({ role: "thinking", text: "думаю" })) fail("размышление сочтено ответом");
  if (sandbox.chatIsAnswer({ role: "note", text: "служебка" })) fail("служебная строка сочтена ответом");
  if (!sandbox.chatIsAnswer({ role: "tool", tool: "SendMessage", human: true, text: "ответ" })) {
    fail("ответ человеку каналом не сочтён ответом агента");
  }
  // Реплика другого агента, приехавшая каналом, вопросом человека не является.
  if (sandbox.chatIsAsk({ role: "user", text: "привет", who: "агент-диспетчер" })) {
    fail("слова другого агента сочтены вопросом человека");
  }
}

console.log("ok: чип встаёт на пришедший непрочитанный ответ, ведёт к нему, гаснет " +
  "показом и крестиком, пачка реплик ведёт в один якорь, во время работы агента чипа нет");
