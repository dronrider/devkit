// Стенд цитаты в пузыре ответа (разбор пользователя: полоса чипов над полем
// ввода была костылём, механика «ответ на сообщение» решается цитатой внутри
// пузыря, как в телеграме и сигнале).
//
// Предмет проверки: цитата стоит в пузыре ответа и ведёт к исходной реплике с
// подсветкой, полосы чипов над полем ввода нет вовсе, на пачку реплик
// цитируется последняя, а вёрстка пузырь не раздувает.
//
// Зовётся: node testdata/poc_quote.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg } from "./poc_dom.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";

const app = appPathArg();
const css = readFileSync(join(dirname(app), "style.css"), "utf8");
const src = readFileSync(app, "utf8");
const at = (n) => new Date(Date.now() - (100 - n) * 60000).toISOString();

// Лента приезжает готовой: пару «реплика человека -> ответ агента» считает
// сервер, панель рисует названное им (quote, quoteKey, quoteMany).
const ask = (n, text) => ({ key: "t:" + n, seq: n, role: "user", time: at(n), text });
const tool = (n) => ({ key: "t:" + n, seq: n, role: "tool", time: at(n), tool: "Bash",
  note: "разбор", text: "command: Bash" });
const answer = (n, text, quote) => Object.assign(
  { key: "t:" + n, seq: n, role: "assistant", time: at(n), text }, quote || {});

let items = [];
const { sandbox, timers } = makeSandbox(app, (path) => {
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items, total: items.length };
  }
  if (path.includes("/chats")) return { chats: [], models: [], days: 3, older: false };
  return {};
});

const chat = { id: "aaaa1111-1111", project: "demo", title: "разбор замечаний",
  state: "live", idle: true, tasks: [] };
const panelWith = async (list) => {
  items = list;
  sandbox.location.hash = "#demo/chat/aaaa1111-1111";
  const st = { project: "demo", addr: "aaaa1111-1111", sid: "aaaa1111-1111",
    chats: [chat], entry: chat, models: [] };
  const panel = sandbox.chatPanel("demo", st);
  await settle();
  await settle();
  return { panel, feed: byClass(panel, "chatfeed") };
};

// --- полосы чипов над полем ввода нет ни в разметке, ни в стилях, ни в памяти ---
{
  const { panel } = await panelWith([ask(1, "перепиши абзац"), answer(2, "Переписал.",
    { quote: "перепиши абзац", quoteKey: "t:1" })]);
  if (byClass(panel, "cunread")) fail("полоса чипов осталась в панели: " + dump(panel).slice(0, 200));
  for (const cls of ["cunread", "cuchip", "cuask", "cuoff"]) {
    if (css.includes("." + cls)) fail("стиль чипов «" + cls + "» остался в style.css");
  }
  if (src.includes("devkit.chat.read") || src.includes("makeUnread")) {
    fail("память прочитанности осталась в статике");
  }
}

// --- цитата стоит в пузыре ответа и ведёт к исходной реплике ---
{
  const { feed } = await panelWith([
    ask(1, "почему стенд зелёный на старом коде"),
    tool(2),
    answer(3, "Проверка стояла не на том поле.",
      { quote: "почему стенд зелёный на старом коде", quoteKey: "t:1" }),
  ]);
  const bubble = sandbox.findKey(feed, "k-t:3");
  if (!bubble) fail("пузыря ответа нет в ленте: " + dump(feed).slice(0, 300));
  const quote = byClass(bubble, "qref");
  if (!quote) fail("цитаты в пузыре ответа нет: " + dump(bubble).slice(0, 300));
  if (!dump(quote).includes("почему стенд зелёный")) {
    fail("цитата не назвала реплику человека: " + dump(quote));
  }
  // Цитата стоит первой строкой пузыря, над словами ответа.
  const bb = byClass(bubble, "bb");
  if (!bb || bb.children[0] !== quote) {
    fail("цитата стоит не первой строкой пузыря: " + dump(bb).slice(0, 300));
  }
  // Ходы инструментов цитат не носят.
  if (byClass(sandbox.findKey(feed, "k-t:2") || {}, "qref")) {
    fail("цитата приписана ходу инструмента");
  }
  // Нажатие ведёт к исходной реплике и подсвечивает её на секунду.
  const src1 = sandbox.findKey(feed, "k-t:1");
  if (!src1) fail("исходной реплики нет в ленте");
  let went = null;
  src1.scrollIntoView = (opts) => { went = opts || {}; };
  quote.handlers.click({ stopPropagation: () => {} });
  if (!went) fail("нажатие цитаты не увело ленту к исходной реплике");
  if (!String(src1.className).split(" ").includes("lit")) {
    fail("исходная реплика не подсвечена: " + src1.className);
  }
  // Отложенное гашение подсветки: в моке таймеры не идут сами, их прогоняет
  // стенд.
  for (const t of timers.slice()) t.fn();
  if (String(src1.className).split(" ").includes("lit")) {
    fail("подсветка исходной реплики не погасла сама");
  }
}

// --- на пачку реплик цитируется последняя, и об этом сказано в подсказке ---
{
  const { feed } = await panelWith([
    ask(1, "первое замечание про архив"),
    ask(2, "второе замечание про чипы"),
    ask(3, "третье замечание про пузырь"),
    answer(4, "Разобрал все три разом.",
      { quote: "третье замечание про пузырь", quoteKey: "t:3", quoteMany: true }),
  ]);
  const quote = byClass(sandbox.findKey(feed, "k-t:4"), "qref");
  if (!quote) fail("цитаты у ответа на пачку нет");
  if (!dump(quote).includes("третье замечание")) {
    fail("цитируется не последняя реплика пачки: " + dump(quote));
  }
  if (!String(quote.title || "").includes("несколько")) {
    fail("подсказка молчит о том, что реплик было несколько: " + JSON.stringify(quote.title));
  }
}

// --- вёрстка: одна строка, мельче основного текста, черта слева, пузырь не шире ---
{
  const rule = (css.match(/\.msg \.qref\{[^}]*\}/) || [])[0] || "";
  if (!rule) fail("правила цитаты нет в style.css");
  if (!/white-space:\s*nowrap/.test(css.match(/\.msg \.qref \.qtext\{[^}]*\}/)[0])) {
    fail("цитата переносится на вторую строку: пузырь раздуется");
  }
  if (!/text-overflow:\s*ellipsis/.test(css.match(/\.msg \.qref \.qtext\{[^}]*\}/)[0])) {
    fail("длинная цитата не режется многоточием");
  }
  if (!/min-width:\s*0/.test(rule)) fail("цитата не ужимается на узком экране: нет min-width:0");
  if (!/max-width:\s*100%/.test(rule)) fail("цитата шире пузыря: нет max-width:100%");
  if (!/var\(--sp\d\)/.test(rule)) fail("отступы цитаты взяты не из общей лестницы: " + rule);
  const size = (rule.match(/font:400 ([\d.]+)px/) || [])[1];
  const base = (css.match(/\.msg \.bb\{[^}]*font:400 ([\d.]+)px/) || [])[1];
  if (!size || !base || Number(size) >= Number(base)) {
    fail("цитата не мельче основного текста: " + size + " против " + base);
  }
  const bar = (css.match(/\.msg \.qref \.qbar\{[^}]*\}/) || [])[0] || "";
  if (!/width:\s*2px/.test(bar)) fail("слева у цитаты нет тонкой вертикальной черты: " + bar);
}

console.log("ok: цитата стоит первой строкой пузыря ответа, ведёт к исходной реплике с " +
  "подсветкой на секунду, на пачку цитируется последняя, полосы чипов нет, пузырь не раздут");
