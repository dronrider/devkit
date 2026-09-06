// Стенд склейки заходов в списке и в ленте (DK-723).
//
// Живой случай: конвейер задачи встаёт потолком проходов, воронкой молчания
// или вышедшим клиентом, кнопка поднимает новую сессию под тем же именем окна,
// и список чатов, ключующий строку по ID сессии, показывал такие заходы
// отдельными строками с одинаковыми заголовками. Человек не знал, в какую
// заходить, а история задачи собиралась по нескольким записям.
//
// Предмет стенда: склеенная строка стоит в списке одна и говорит числом, из
// скольких заходов она склеена; лента такой строки идёт сверху вниз от
// старшего захода к нынешнему, а границы заходов нарисованы чертой, как
// граница дня, а не пузырём чьей-то реплики.
//
// Зовётся: node testdata/poc_chatpast.mjs static/app.js

import { makeSandbox, settle, dump, allByClass, fail, appPathArg } from "./poc_dom.mjs";
import { deepFind, hasClass } from "./poc_css.mjs";

const HEAD = "cccc3333-3333-3333-3333-333333333333";
const PAST1 = "aaaa1111-1111-1111-1111-111111111111";
const PAST2 = "bbbb2222-2222-2222-2222-222222222222";

const FIRST = "первый заход разобрал постановку";
const SECOND = "второй заход написал тесты";
const NOW = "этот заход дописывает доку";

const MARK1 = "заход 1 из 3, сессия " + PAST1;
const MARK2 = "заход 2 из 3, сессия " + PAST2;
const MARK3 = "заход 3 из 3, сессия " + HEAD;

// Список отдаёт одну строку на окно: прошлые заходы приехали полем past, своих
// строк у них больше нет.
const chats = [{
  id: HEAD, title: "конвейер DK-723", project: "demo", state: "live", idle: true,
  tmux: "task-DK-723", tasks: ["DK-723"],
  mtime: "2026-09-04T12:30:00+03:00",
  past: [
    { id: PAST1, mtime: "2026-09-04T10:00:00+03:00" },
    { id: PAST2, mtime: "2026-09-04T11:00:00+03:00" },
  ],
}];

// Лента склеена сервером: разделитель перед каждым заходом, записи заходов по
// порядку, нынешний заход последним.
const items = [
  { key: "pass-" + PAST1 + ":0", role: "mark", text: MARK1, time: "2026-09-04T10:00:00+03:00" },
  { key: "p1:1", role: "assistant", text: FIRST, time: "2026-09-04T10:00:30+03:00" },
  { key: "pass-" + PAST2 + ":0", role: "mark", text: MARK2, time: "2026-09-04T11:00:00+03:00" },
  { key: "p2:1", role: "assistant", text: SECOND, time: "2026-09-04T11:00:30+03:00" },
  { key: "pass-" + HEAD + ":0", role: "mark", text: MARK3, time: "2026-09-04T12:00:00+03:00" },
  { key: "t:1", role: "assistant", text: NOW, time: "2026-09-04T12:30:00+03:00" },
];

const { sandbox, byId } = makeSandbox(appPathArg(), (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.includes("/chats/") && path.endsWith("/status")) return { live: true, busy: false };
  if (path.includes("/sessions/" + HEAD)) {
    return { session: HEAD, head: { id: HEAD, tasks: ["DK-723"] }, items, total: items.length, start: true };
  }
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.endsWith("/board")) return { board: { prefix: "XR", sections: [] }, works: [] };
  return {};
});

sandbox.location.hash = "#demo/chat/" + HEAD;
await sandbox.refresh();
await settle();

const pin = byId.get("cpin");
if (!pin) fail("панели разговора нет вовсе");
const feed = dump(pin);

// --- история читается сверху вниз ---
for (const [what, text] of [["первого захода", FIRST], ["второго захода", SECOND], ["нынешнего захода", NOW]]) {
  if (!feed.includes(text)) {
    fail("записи " + what + " в ленте нет: " + feed.slice(-400));
  }
}
const order = [FIRST, SECOND, NOW].map((t) => feed.indexOf(t));
if (!(order[0] < order[1] && order[1] < order[2])) {
  fail("заходы стоят в ленте не по порядку: " + JSON.stringify(order));
}

// --- границы заходов это черта, а не пузырь ---
for (const mark of [MARK1, MARK2, MARK3]) {
  const line = allByClass(pin, "day").find((n) => dump(n).includes(mark));
  if (!line) {
    fail("разделитель захода не встал чертой в ленту: " + mark + "\n" + feed.slice(-600));
  }
  if (feed.split(mark).length - 1 !== 1) {
    fail("разделитель захода нарисован дважды: " + mark);
  }
}

// --- в списке одна строка, и она говорит число заходов ---
const head = sandbox.chatHead("demo", await sandbox.chatState("demo", HEAD, { prefix: "XR", sections: [] }, []));
const pick = deepFind(head, hasClass("cdpick"))[0];
if (!pick) fail("кнопки списка чатов в шапке нет: " + dump(head).slice(0, 200));
pick.handlers.click({ stopPropagation: () => {} });
await settle();
const box = deepFind(head, hasClass("cdrop"))[0];
if (!box) fail("список чатов не открылся: " + dump(head).slice(0, 200));
const rows = deepFind(box, hasClass("cdrow"));
if (rows.length !== 1) {
  fail("строк списка " + rows.length + ", а заходы одного окна стоят одной: " + dump(box).slice(0, 400));
}
const row = dump(rows[0]);
if (!row.includes("заходов: 3")) {
  fail("строка не говорит, из скольких заходов она склеена: " + row.slice(0, 400));
}
for (const id of [PAST1, PAST2]) {
  if (row.includes(id)) {
    fail("прошлый заход назван в строке своим ID, а строка одна на окно: " + row.slice(0, 400));
  }
}

console.log("ок: заходы одного окна стоят одной строкой со счётом заходов, " +
  "лента склеена по порядку, границы заходов нарисованы чертой");
