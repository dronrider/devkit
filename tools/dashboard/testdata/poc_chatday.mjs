// Стенд группировки списка диалогов (замечание пользователя: список разросся).
// Строки идут группами: живые сверху, дальше по дням, сегодня и вчера словами,
// глубже датой. Порядок по свежести внутри групп прежний.
//
// Зовётся: node testdata/poc_chatday.mjs static/app.js

import { makeSandbox, makeNode, dump, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "XR", sections: [{ key: "in-progress", rows: [] }] };
const hour = 3600 * 1000;
const stamp = (ms) => new Date(Date.now() - ms).toISOString();

// Живой разговор тут старше всех прочих: группа живых стоит сверху и в этом
// смысл, к идущему разговору идут отвечать, а не искать его в глубине.
const chats = [
  { id: "aaaa1111-1111", project: "demo", title: "сегодняшний разговор",
    mtime: stamp(2 * hour), state: "dead", tasks: [] },
  { id: "bbbb2222-2222", project: "demo", title: "второй сегодняшний",
    mtime: stamp(5 * hour), state: "dead", tasks: [] },
  { id: "cccc3333-3333", project: "demo", title: "вчерашняя беседа",
    mtime: stamp(26 * hour), state: "dead", tasks: [] },
  { id: "dddd4444-4444", project: "demo", title: "позавчерашняя беседа",
    mtime: stamp(50 * hour), state: "dead", tasks: [] },
  { id: "eeee5555-5555", project: "demo", title: "старый, но живой",
    mtime: stamp(200 * hour), state: "live", idle: true, tasks: [] },
];

const { sandbox, store } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});
store.set("devkit.chat.filter", "0");

sandbox.location.hash = "#demo/chat/board";
const st = await sandbox.chatState("demo", "board", board);
const anchor = makeNode("div");
sandbox.chatDropOpen("demo", st, anchor);
const drop = anchor.children[anchor.children.length - 1];
const rows = drop.children[1];

// --- заголовки дней стоят в списке ---
const heads = rows.querySelectorAll(".cdday").map((n) => n.textContent);
if (!heads.length) fail("список идёт сплошняком, без заголовков дней: " + dump(rows).slice(0, 300));
if (heads[0] !== "живые разговоры") {
  fail("живые разговоры не стоят группой сверху: " + JSON.stringify(heads));
}
if (heads[1] !== "сегодня" || heads[2] !== "вчера") {
  fail("сегодня и вчера не названы словами: " + JSON.stringify(heads));
}
// Позавчерашний день назван датой, и года у него нет: год этот, свежий.
const older = heads[3] || "";
if (!/^\d{1,2} [а-я]+$/.test(older)) {
  fail("день глубже вчерашнего назван не датой: " + JSON.stringify(heads));
}

// --- порядок групп и строк ---
const said = dump(rows).replace(/\s+/g, " ");
const at = (what) => said.indexOf(what);
if (at("старый, но живой") > at("сегодняшний разговор")) {
  fail("живой разговор ушёл под сегодняшние: " + said.slice(0, 300));
}
if (at("сегодняшний разговор") > at("второй сегодняшний")) {
  fail("внутри дня порядок по свежести сломан: " + said.slice(0, 300));
}
if (at("вчерашняя беседа") > at("позавчерашняя беседа")) {
  fail("дни идут не свежими сверху: " + said.slice(0, 300));
}
// Живой разговор стоит в списке один раз: своей группой, а не дважды.
if (rows.querySelectorAll(".cdrow").length !== chats.length) {
  fail("строк в списке не столько, сколько разговоров: " + rows.querySelectorAll(".cdrow").length);
}

console.log("ok: список идёт группами, живые сверху, сегодня и вчера словами, " +
  "глубже датой, порядок по свежести внутри групп цел");
