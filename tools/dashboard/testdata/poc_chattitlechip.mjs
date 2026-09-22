// Стенд чипа задачи, продублированного заголовком (DK-879).
//
// Живой случай, разбор задачи. Заголовок известной головы стал коротким
// именем «DK-1120 работа»: номер задачи читается прямо в заголовке. Строка
// того же чата рисовала рядом чип «DK-1120» вторым разом, хотя он уже назван
// в заголовке — глаз читал один и тот же номер дважды на узкой строке
// смартфона. Чип обязан пропадать ровно тогда, когда его ID совпал с тем,
// каким начинается заголовок, и оставаться у чатов, чей заголовок номера не
// несёт (замечание пользователя 2026-09-22, разбор DK-879).
//
// Зовётся: node testdata/poc_chattitlechip.mjs static/app.js

import { makeSandbox, makeNode, byClass, allByClass, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const board = { prefix: "DK", sections: [] };

const NAMED = "aaaa1111-1111";
const SECOND = "bbbb2222-2222";
const PLAIN = "cccc3333-3333";
const COINCIDENT = "dddd4444-4444";

const chats = [
  // Заголовок несёт единственную задачу чата, поставлен дашбордом
  // (titleNamed): чип обязан пропасть целиком.
  { id: NAMED, project: "demo", title: "DK-1120 работа", state: "dead",
    mtime: "2026-09-22T10:00:00Z", tasks: ["DK-1120"], titleNamed: true },
  // Заголовок несёт первую задачу, вторая чипом остаётся: дубль снимается
  // точечно, а не гасит всю полосу чипов задач.
  { id: SECOND, project: "demo", title: "DK-1120 груминг", state: "dead",
    mtime: "2026-09-22T09:00:00Z", tasks: ["DK-1120", "DK-1200"], titleNamed: true },
  // Заголовок без номера: чип остаётся, дедуп его не касается (свободный чат,
  // подписанный из транскрипта, а не известной головой).
  { id: PLAIN, project: "demo", title: "разберись с подпиской", state: "dead",
    mtime: "2026-09-22T08:00:00Z", tasks: ["DK-1120"] },
  // Заголовок случайно начинается с ID своей же задачи, но назван не
  // дашбордом (эвристика по ai-title, titleNamed не стоит): чип остаётся,
  // текстовое совпадение дедуп не запускает (замечание ревью DK-879).
  { id: COINCIDENT, project: "demo", title: "DK-1120 в тексте реплики, а не имя",
    state: "dead", mtime: "2026-09-22T07:00:00Z", tasks: ["DK-1120"] },
];

const { sandbox, store } = makeSandbox(app, (path) => {
  if (path.includes("/chats")) return { chats, models: [], days: 3, older: false };
  if (path.includes("/sessions/")) return { items: [], start: true };
  return {};
});
store.set("devkit.chat.arch", "off");

sandbox.location.hash = "#demo/chat/" + NAMED;
const st = await sandbox.chatState("demo", NAMED, board);

const anchor = makeNode("div");
sandbox.chatDropOpen("demo", st, anchor);
const drop = anchor.children[anchor.children.length - 1];
const rows = byClass(drop, "cdrows");

const rowOf = (id) => allByClass(rows, "cdrow").find((n) => {
  const chat = chats.find((c) => c.id === id);
  return dump(n).includes(chat.title);
});
const taskChipsOf = (node) => allByClass(node, "chip")
  .map((n) => String(n.textContent || "").trim())
  .filter((t) => /^[A-Z]+-\d+$/.test(t));

// --- заголовок несёт единственную задачу: чип пропадает целиком ---
{
  const row = rowOf(NAMED);
  if (!row) fail("строка «DK-1120 работа» не нашлась: " + dump(rows).slice(0, 300));
  const chips = taskChipsOf(row);
  if (chips.length !== 0) {
    fail("чип DK-1120 не пропал у заголовка, который его уже назвал: " + JSON.stringify(chips));
  }
}

// --- заголовок несёт первую задачу, вторая остаётся чипом ---
{
  const row = rowOf(SECOND);
  if (!row) fail("строка второго чата не нашлась: " + dump(rows).slice(0, 300));
  const chips = taskChipsOf(row);
  if (chips.length !== 1 || chips[0] !== "DK-1200") {
    fail("вторая задача не осталась единственным чипом: " + JSON.stringify(chips));
  }
}

// --- заголовок без номера: чип задачи остаётся как был ---
{
  const row = rowOf(PLAIN);
  if (!row) fail("строка свободного чата не нашлась: " + dump(rows).slice(0, 300));
  const chips = taskChipsOf(row);
  if (chips.length !== 1 || chips[0] !== "DK-1120") {
    fail("чип задачи пропал у заголовка без номера: " + JSON.stringify(chips));
  }
}

// --- заголовок случайно начинается с ID, но назван не дашбордом: чип остаётся ---
{
  const row = rowOf(COINCIDENT);
  if (!row) fail("строка со случайным совпадением не нашлась: " + dump(rows).slice(0, 300));
  const chips = taskChipsOf(row);
  if (chips.length !== 1 || chips[0] !== "DK-1120") {
    fail("чип задачи пропал по текстовому совпадению без titleNamed: " + JSON.stringify(chips));
  }
}

console.log("poc_chattitlechip: ok, чип задачи не дублирует номер, уже стоящий в заголовке чата, " +
  "и не гасится текстовым совпадением без признака titleNamed");
