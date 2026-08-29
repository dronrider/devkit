// Стенд вида записей входа (ветка poc-chat, DK-577).
//
// Живой случай: пользователь на приёмке сказал, что вход в чате читается
// гостем в ленте. «Я просил сделать блоки прямо в чате, но они отличаются от
// стандартных блоков чата по размеру и оформлению. Должно быть как чат, всё
// органично».
//
// Предмет стенда: раскладка записи входа снимается числом с настоящей
// разметки и настоящего style.css и сверяется с раскладкой обычной записи
// ленты. Разница допускается только там, где несёт смысл: внутри записи входа
// живут кнопки, ссылка и поле кода, у обычной реплики их нет. Меряется на
// узком экране (390) и на широком (1440).
//
// Зовётся: node testdata/poc_loginfit.mjs static/app.js [--show]

import { makeSandbox, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { cssRules, layoutOf, deepFind, hasClass } from "./poc_css.mjs";

const app = appPathArg();
const rules = cssRules(app);

// Свойства, которыми меряется вид записи. Список закрытый нарочно: сверять всё
// подряд значит ловить шум, а не расхождение.
const WANT = ["max-width", "display", "gap", "margin-top", "margin", "padding",
  "padding-bottom", "border-radius", "font", "font-size", "color", "background",
  "border", "border-color", "overflow-wrap", "opacity", "align-self", "text-align"];
const fitOf = (node, width) => layoutOf(node, { rules, width, want: WANT });
const show = process.argv.includes("--show");
const BYE = "Login expired. Please run /login";


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

const out = (sid) => ({
  addr: sid, sid, task: "DK-397", chats: [], models: [], project: "demo",
  entry: { tmux: "chat-9", state: "live", login: true },
});

items = [
  { key: "m-1", role: "user", text: "посчитай остаток бюджета",
    time: "2026-08-29T19:00:00+03:00" },
  { key: "m-2", role: "assistant", text: BYE, logout: true,
    time: "2026-08-29T19:00:02+03:00" },
];

const panel = sandbox.chatPanel("demo", out("aaaa5779-1111"));
await settle();

const feed = byClass(panel, "chatfeed");
const talk = byClass(panel, "cbyetalk");
if (!feed) fail("ленты разговора нет: " + dump(panel));
if (!talk || talk.hidden) fail("записей входа нет: " + dump(panel));

// Сверяется с ответом агента, а не со своей репликой: своя красится отдельно
// («.msg.me»), а запись входа своей не считается.
const feedMsg = deepFind(feed, hasClass("msg")).find((n) => !hasClass("me")(n));
const loginMsg = deepFind(talk, hasClass("msg"))[0];
if (!feedMsg) fail("в ленте нет ни одной записи: " + dump(feed));
if (!loginMsg) fail("вход не собран записью ленты: " + dump(talk));
const kidOf = (node, cls) => deepFind(node, hasClass(cls))[0];
// Ритм текста живёт на абзаце внутри разметки, а не на её корне.
const paraOf = (node) => deepFind(kidOf(node, "md"),
  (n) => String(n.tagName || "").toLowerCase() === "p")[0];

const pairs = [
  ["запись", feedMsg, loginMsg],
  ["пузырь", kidOf(feedMsg, "bb"), kidOf(loginMsg, "bb")],
  ["текст", kidOf(feedMsg, "md"), kidOf(loginMsg, "md")],
  ["подпись", kidOf(feedMsg, "mm"), kidOf(loginMsg, "mm")],
  ["абзац", paraOf(feedMsg), paraOf(loginMsg)],
];

for (const width of [390, 1440]) {
  for (const [name, a, b] of pairs) {
    if (!a) fail("у записи ленты нет узла «" + name + "»");
    if (!b) fail("у записи входа нет узла «" + name + "»: " + dump(loginMsg));
    const one = fitOf(a, width);
    const two = fitOf(b, width);
    if (show) {
      console.log("[" + width + "] " + name);
      for (const k of WANT) {
        if (one[k] === undefined && two[k] === undefined) continue;
        const mark = one[k] === two[k] ? "  " : "!!";
        console.log("   " + mark + " " + k + ": лента " + (one[k] ?? "-") +
          " | вход " + (two[k] ?? "-"));
      }
    }
    for (const k of WANT) {
      if (one[k] !== two[k]) {
        fail("на " + width + " точках у узла «" + name + "» свойство " + k +
          " разошлось: лента «" + (one[k] ?? "нет") + "», вход «" +
          (two[k] ?? "нет") + "»");
      }
    }
  }
}

// Разница, которая несёт смысл, остаётся: внутри записи входа живут кнопки,
// ссылка и поле кода, и они стоят в том же пузыре, а не рядом с ним.
const bb = kidOf(loginMsg, "bb");
const own = deepFind(talk, (n) => String(n.className || "").includes("loginbtns"))[0];
if (!own) fail("кнопок входа в записи нет: " + dump(talk));
for (let n = own; n; n = n.parentNode) {
  if (n === bb) break;
  if (!n.parentNode) fail("кнопки входа стоят вне пузыря записи: " + dump(talk));
}

console.log("ок: записи входа сняты числом с настоящего style.css и сошлись с " +
  "записью ленты на 390 и 1440 точках, а кнопки живут внутри пузыря");
