// Стенд подсказок даты (POC DK-397, ветка poc-chat).
//
// Пользователь навёл на дату в накопителе: «при наведении на эту дату есть
// только идиотская подпись, а нужно отображать точную дату с временем».
// Подсказки объясняли, что это за дата («дата последней правки записи: разбор
// двигает её же»), тогда как объяснение стоит в заголовке колонки, а к самой
// дате человек приходит за точностью: в ячейке день, а часа не видно нигде.
//
// Предмет стенда это одинаковое поведение во всех четырёх местах, где стоит
// дата: строка доски, крошки экрана задачи, запись накопителя и строка сессии.
// Где сервер знает время (запись накопителя, последняя реплика сессии),
// подсказка называет час с минутой; где он знает только день (строка доски,
// дату которой считает taskctl по git blame), подсказка не выдумывает времени,
// а называет день словами и давность.
//
// Зовётся: node testdata/poc_datetip.mjs static/app.js

import { makeSandbox, settle, byClass, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();

// Слова прежних подсказок: они объясняли колонку вместо того, чтобы показать
// дату точнее.
const EXPLAIN = ["двигает её же", "дата последней правки"];

const now = new Date();
const day = (back) => {
  const at = new Date(now.getTime() - back * 86400000);
  const two = (n) => String(n).padStart(2, "0");
  return at.getFullYear() + "-" + two(at.getMonth() + 1) + "-" + two(at.getDate());
};

const rows = [
  { id: "XR-101", title: "ясли для сессий", sect: "backlog", r: 40, r_parts: [10, 8, 7, 8, 7],
    moved: day(3), cost: "-", type: "task" },
];
const works = [
  { kind: "session", via: "session", session: "s-busy", own: true, live: "busy",
    title: "буквой раньше", started: Math.floor(now.getTime() / 1000) - 3 * 3600,
    moved: Math.floor(now.getTime() / 1000) - 5400 },
];
const drafts = [
  { id: "XR-D1", title: "автономный выкат", prio: "mid", written: day(9),
    moved: day(1), moved_at: Math.floor(now.getTime() / 1000) - 26 * 3600 },
];

const { sandbox, byId } = makeSandbox(app, (path_) => {
  if (path_ === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path_ === "/api/harnesses") return { harnesses: [] };
  if (path_ === "/api/notifications") return { items: [] };
  if (path_.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ key: "backlog", title: "Backlog", rows }] },
      works };
  }
  if (path_.endsWith("/works")) return { works };
  if (path_.endsWith("/drafts")) return { drafts };
  if (path_.includes("/tasks/")) {
    return { project: "demo", id: "XR-101", after: [], blocks: [], row: rows[0],
      text: "постановка", file: "docs/tasks/XR-101.md" };
  }
  if (path_.includes("/chats")) return { chats: [], models: [] };
  if (path_ === "/api/quota") return { harnesses: [] };
  return {};
});

const groups = byId.get("groups");
const go = async (hash) => {
  sandbox.location.hash = hash;
  await sandbox.refresh();
  await settle();
};
const tipOf = (node) => String((node && ((node.attrs || {}).title || node.title)) || "");

// Что обязана нести всякая подсказка даты: месяц словом (а не голые цифры) и
// ни слова о том, что эту дату двигает.
const sane = (where, tip) => {
  if (!tip) fail(where + ": у даты нет подсказки вовсе");
  for (const said of EXPLAIN) {
    if (tip.includes(said)) {
      fail(where + ": подсказка рассказывает про дату вместо того, чтобы её показать: " + tip);
    }
  }
  if (!/[а-яё]{3,}/i.test(tip)) {
    fail(where + ": подсказка не называет дату словами: " + tip);
  }
};

// --- строка доски: день, месяц словом и давность ---
{
  await go("#demo");
  const cell = byClass(allByClass(groups, "trow")[0], "twhen");
  const said = byClass(cell, "stale");
  const tip = tipOf(said);
  sane("строка доски", tip);
  if (!tip.includes("назад") && !tip.includes("сегодня") && !tip.includes("вчера")) {
    fail("строка доски: подсказка не говорит давности: " + tip);
  }
  // Времени у даты доски нет: её считает taskctl по git blame, и часа там не
  // было никогда. Выдумывать «00:00» подсказка не станет.
  if (/\d{1,2}:\d{2}/.test(tip)) {
    fail("строка доски: подсказка выдумала время, которого сервер не присылал: " + tip);
  }
}

// --- крошки экрана задачи: та же подсказка теми же словами ---
{
  await go("#demo/XR-101");
  const said = allByClass(groups, "stale").find((node) => tipOf(node));
  if (!said) fail("экран задачи: даты с подсказкой в крошках нет");
  sane("экран задачи", tipOf(said));
}

// --- запись накопителя: точное время ---
{
  await go("#demo/drafts");
  const cell = byClass(allByClass(groups, "dsrow")[0], "dwhen");
  const tip = tipOf(byClass(cell, "stale"));
  sane("накопитель", tip);
  if (!/\d{1,2}:\d{2}/.test(tip)) {
    fail("накопитель: сервер прислал точное время правки, а подсказка его не показывает: " + tip);
  }
  if (!tip.includes("назад") && !tip.includes("вчера")) {
    fail("накопитель: подсказка не говорит давности: " + tip);
  }
}

// --- строка сессии: время последней реплики ---
{
  await go("#demo/sess");
  const cell = byClass(allByClass(groups, "arow")[0], "amoved");
  const tip = tipOf(byClass(cell, "stale"));
  sane("строка сессии", tip);
  if (!/\d{1,2}:\d{2}/.test(tip)) {
    fail("строка сессии: подсказка не несёт времени последней реплики: " + tip);
  }
}

console.log("poc_datetip: ok");
