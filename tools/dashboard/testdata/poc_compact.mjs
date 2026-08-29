// Стенд записи о сжатии разговора (ветка poc-chat, DK-577).
//
// Живой случай: в чате «Выполнение XR-279» человек нашёл портянку на несколько
// тысяч слов, начинающуюся с «This session is being continued from a previous
// conversation that ran out of context». Харнес кладёт пересказ съеденного
// начала записью роли user, и лента честно рисовала его пузырём человека.
// «Блок выглядит как моё сообщение. Нужно отобразить его стандартно, в
// свёрнутом виде, как мы это делаем с остальными блоками, с кнопками
// развернуть и копировать».
//
// Предмет стенда: такая запись стоит свёрнутым блоком с заголовком русскими
// словами, разворачивается кликом и копируется кнопкой, а пузырём человека не
// выглядит. Кусок пересказа тут короткий: портянка целиком стенду не нужна.
//
// Зовётся: node testdata/poc_compact.mjs static/app.js

import { makeSandbox, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { deepFind, hasClass } from "./poc_css.mjs";

const app = appPathArg();
const SUMMARY = "This session is being continued from a previous conversation " +
  "that ran out of context.\nSummary: разобрал очередь слияния и снял два замечания.";

let items = [];
const { sandbox } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path.includes("/sessions/")) {
    const sid = path.slice(path.indexOf("/sessions/") + 10).split("?")[0];
    return { session: sid, head: { id: sid }, items, total: items.length };
  }
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

const at = "2026-07-30T13:45:41+03:00";
items = [
  { key: "m-1", role: "user", text: "продолжай", time: at },
  { key: "m-2", role: "note", mark: "compact", note: "начало разговора сжато в пересказ",
    text: SUMMARY, time: at },
];

const panel = sandbox.chatPanel("demo", {
  addr: "s", sid: "aaaa5779-4444", chats: [], models: [], project: "demo",
  entry: { tmux: "chat-9", state: "live" },
});
await settle();

const feed = byClass(panel, "chatfeed");
if (!feed) fail("ленты разговора нет: " + dump(panel).slice(0, 200));

// Свёрнутый блок стоит, и пересказ в нём спрятан.
const fold = deepFind(feed, hasClass("compact"))[0];
if (!fold) fail("пересказ не собран свёрнутым блоком: " + dump(feed).slice(0, 300));
if (!String(fold.className).includes("fold")) {
  fail("блок пересказа не того вида, что прочие свёрнутые: " + fold.className);
}
const top = byClass(fold, "foldh");
if (!top) fail("шапки у свёрнутого блока нет: " + dump(fold).slice(0, 200));
const head = dump(top).replace(/\s+/g, " ");
if (!head.includes("начало разговора сжато")) {
  fail("заголовок пересказа не сказан словами: " + head.slice(0, 200));
}
for (const own of ["контекст", "токен", "context"]) {
  if (head.toLowerCase().includes(own)) {
    fail("в заголовке устройство харнеса вместо дела человека: " + head.slice(0, 200));
  }
}
const body = deepFind(fold, hasClass("foldb"))[0];
if (!body) fail("тела у свёрнутого блока нет: " + dump(fold).slice(0, 200));
if (!body.hidden) fail("пересказ показан развёрнутым, а его прячут");
if (!String(body.textContent || "").includes("разобрал очередь слияния")) {
  fail("пересказ потерян: " + String(body.textContent || "").slice(0, 120));
}

// Пузырём человека это больше не выглядит: своя реплика в ленте одна.
const mine = deepFind(feed, hasClass("me"));
if (mine.length !== 1) {
  fail("своих реплик в ленте " + mine.length + ", а человек писал одну");
}
for (const m of mine) {
  if (dump(m).includes("This session is being continued")) {
    fail("пересказ остался пузырём человека: " + dump(m).slice(0, 200));
  }
}

// Разворот кликом и кнопка копирования, как у прочих свёрнутых блоков.
// Кнопка копирования у свёрнутых блоков подписана не текстом, а меткой для
// чтения с экрана: ищется она по ней.
const copy = deepFind(fold, (n) => String(n.className || "").includes("foldcp"))[0];
if (!copy) fail("копировать пересказ нечем: " + dump(fold).slice(0, 200));
top.handlers.click({});
await settle();
if (body.hidden) fail("клик по шапке не развернул пересказ");

console.log("ок: пересказ съеденного начала стоит свёрнутым блоком со своим " +
  "заголовком, разворотом и копированием, а пузырём человека не выглядит");
