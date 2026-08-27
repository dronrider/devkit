// Стенд укладки колонок в ширину таблицы (POC DK-397, ветка poc-chat).
//
// Пользователь вытянул колонки тягой и получил строку за карточкой: кнопка
// запуска встала на самой кромке, кнопка разговора уехала на фон страницы, а
// номер задачи слева обрезался («-4...»). Причина в том, что тяга правила
// ширину каждой колонки сама по себе: предел стоял на одну колонку, а сумму
// колонок с шириной таблицы никто не сверял, и растяжимая колонка отдавала
// остаток до нуля.
//
// Отсюда предмет стенда: сколько границу ни тяни, в любую сторону и в любом
// разделе, сумма колонок укладывается в место таблицы, а растяжимой остаётся
// её нижний предел. Отдельным случаем сторожатся ширины из памяти: человек
// натянул их на широком мониторе, а открыл страницу на узком, и числа обязаны
// ужаться при чтении.
//
// Зовётся: node testdata/poc_tblfit.mjs static/app.js

import fs from "node:fs";
import { makeSandbox, settle, byClass, allByClass, fail, appPathArg } from "./poc_dom.mjs";

const app = appPathArg();
const SRC = fs.readFileSync(app, "utf8");

// Пределы читаются из самого кода: копия чисел в стенде разошлась бы с экраном
// молча, и стенд сторожил бы прежний предел.
const num = (name, why) => {
  const m = new RegExp("const " + name + " = (\\d+)").exec(SRC);
  if (!m) fail(why);
  return Number(m[1]);
};
const COL_MIN = num("TBL_COL_MIN", "нижнего предела колонки в коде нет вовсе");
// Нижний предел растяжимой колонки: без него замер идёт по одной только сумме,
// и видно, на сколько точек строка вылезла за карточку. Отсутствие самого
// предела это тоже дефект, но говорится о нём последним, когда числа уже
// названы.
const floor = /const TBL_FLEX_MIN = (\d+)/.exec(SRC);
const FLEX_MIN = floor ? Number(floor[1]) : 0;

const rows = [
  { id: "XR-101", title: "ясли для сессий", sect: "backlog", r: 40, r_parts: [10, 8, 7, 8, 7],
    moved: "2026-08-20", cost: "-" },
];
const works = [
  { kind: "session", via: "session", session: "s-busy", own: true, live: "busy",
    title: "буквой раньше", started: 3000, moved: 9000 },
];
const drafts = [
  { id: "XR-D1", title: "автономный выкат", prio: "mid", written: "2026-08-10", age_days: 3 },
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

const head = (kind) => allByClass(groups, "tblh")
  .find((h) => String(h.className).split(" ").includes("h-" + kind)) || null;
const cells = (kind) => [...((head(kind) || {}).children || [])];
const px = (name) => Number(String(sandbox.document.documentElement.style.props[name] || "")
  .replace("px", "")) || 0;

// Колонки раздела читаются из разметки таблицы: класс cw-<ключ> у col, а
// растяжимую видно по отсутствию ширины. Список ключей в стенде разъехался бы
// с кодом, стоило разделу завести колонку.
const colsOf = (kind) => {
  if (typeof sandbox.tblColgroup !== "function") fail("colgroup не собирается: колонок нет");
  const group = sandbox.tblColgroup(kind);
  return (group.children || []).filter((c) => c.tagName === "COL").map((c) => ({
    key: String(c.className || "").replace("cw-", ""),
    flex: !String(((c.style || {}).width) || ""),
  }));
};

// Место, в которое таблица обязана уложиться, называет сам экран.
const room = () => {
  if (typeof sandbox.tblRoom !== "function") {
    fail("места таблицы никто не считает (функции tblRoom нет): ширины колонок сверять не с чем");
  }
  return sandbox.tblRoom();
};

// Что видно на экране: сумма колонок со своей шириной и остаток растяжимой.
const shape = (kind) => {
  const cols = colsOf(kind);
  let sum = 0;
  for (const col of cols) {
    if (col.flex) continue;
    const w = px("--tc-" + kind + "-" + col.key);
    if (!w) fail("колонка " + col.key + " раздела " + kind + " осталась без ширины");
    sum += w;
  }
  const space = room();
  return { sum, room: space, flex: space - sum };
};

// Главная проверка: строка не вылезает за карточку ни справа, ни слева.
// Таблица стоит во всю ширину места, колонки складываются в него же, и
// растяжимой остаётся не меньше её предела.
const inCard = (kind, why) => {
  const got = shape(kind);
  if (got.sum + FLEX_MIN > got.room) {
    fail("раздел " + kind + ", " + why + ": колонки заняли " + got.sum +
      " точек при месте таблицы " + got.room + ", растяжимой осталось " + got.flex +
      " при пределе " + FLEX_MIN + ". Строка вылезает за карточку");
  }
  return got;
};

const drag = (grip, from, to) => {
  grip.handlers.pointerdown({ clientX: from, stopPropagation: () => {}, preventDefault: () => {} });
  sandbox.document.handlers.pointermove({ clientX: to });
  sandbox.document.handlers.pointerup({});
};

const SECTS = [["#demo", "tasks"], ["#demo/sess", "sess"], ["#demo/drafts", "drafts"]];
const KEYS = { tasks: "devkit.dash.tasks.tblcols", sess: "devkit.dash.sess.tblcols",
  drafts: "devkit.dash.drafts.tblcols" };

// --- тяга до упора в обе стороны, все три раздела ---
{
  for (const [hash, kind] of SECTS) {
    for (const [side, from, to] of [["вправо", 0, 5000], ["влево", 5000, 0]]) {
      await go(hash);
      const list = cells(kind);
      list.forEach((cell, at) => {
        const grip = byClass(cell, "tblg");
        if (!grip) return;
        drag(grip, from, to);
        inCard(kind, "граница " + at + " утянута " + side);
      });
      const got = inCard(kind, "все границы утянуты " + side);
      console.log("  " + kind + ", тяга " + side + ": место " + got.room +
        ", колонки " + got.sum + ", растяжимой " + got.flex);
      // Ужать колонку ниже её предела тяга тоже не должна: схлопнутая в ноль
      // колонка это та же беда с другого конца.
      for (const col of colsOf(kind)) {
        if (col.flex) continue;
        const w = px("--tc-" + kind + "-" + col.key);
        if (w < COL_MIN) {
          fail("раздел " + kind + ": колонка " + col.key + " схлопнута до " + w + " точек");
        }
      }
      sandbox.localStorage.removeItem(KEYS[kind]);
    }
  }
}

// --- ширины из памяти ужимаются под нынешнее окно ---
// Человек натянул колонки на широком мониторе, а открыл страницу на узком:
// числа в памяти те же, а места под них нет, и уложить их обязано чтение.
{
  const wide = sandbox.window.innerWidth;
  for (const [hash, kind] of SECTS) {
    const kept = {};
    for (const col of colsOf(kind)) {
      if (!col.flex) kept[col.key] = 460;
    }
    sandbox.localStorage.setItem(KEYS[kind], JSON.stringify(kept));
    sandbox.window.innerWidth = 1000;
    await go(hash);
    const got = inCard(kind, "ширины из памяти на узком окне");
    console.log("  " + kind + ", память с широкого экрана: место " + got.room +
      ", колонки " + got.sum + ", растяжимой " + got.flex);
    // Память при этом остаётся прежней: обрезает чтение, а не запись, иначе
    // один заход с ноутбука отнимал бы ширины навсегда.
    const said = JSON.parse(sandbox.localStorage.getItem(KEYS[kind]) || "{}");
    for (const key of Object.keys(kept)) {
      if (said[key] !== kept[key]) {
        fail("раздел " + kind + ": чтение переписало память ширин, было " +
          kept[key] + ", стало " + said[key]);
      }
    }
    sandbox.window.innerWidth = wide;
    sandbox.localStorage.removeItem(KEYS[kind]);
  }
}

// --- окно сузили при том же экране ---
// Ширины стоят переменными корня, и без пересчёта они переживают тягу окна за
// угол: список не пересобирается, а место под него стало меньше.
{
  const wide = sandbox.window.innerWidth;
  await go("#demo");
  cells("tasks").forEach((cell) => {
    const grip = byClass(cell, "tblg");
    if (grip) drag(grip, 5000, 0);
  });
  sandbox.window.innerWidth = 900;
  sandbox.window.fire("resize");
  const got = inCard("tasks", "окно сузили с " + wide + " до 900 точек");
  console.log("  tasks, окно сузили до 900: место " + got.room + ", колонки " + got.sum +
    ", растяжимой " + got.flex);
  sandbox.window.innerWidth = wide;
  sandbox.localStorage.removeItem(KEYS.tasks);
}

// --- тяга берёт место у соседей, а не у таблицы ---
// Пока одна колонка растёт, прочие стоят на месте: место ей отдаёт растяжимая
// колонка, и упор границы наступает там, где у растяжимой остаётся предел.
{
  // Заход через соседний раздел: список пересобирается заново, и переменные
  // ширин встают от чистой памяти, а не остаются от прошлого случая.
  await go("#demo/sess");
  await go("#demo");
  const all = colsOf("tasks");
  const flexAt = all.findIndex((c) => c.flex);
  const keys = all.filter((c) => !c.flex).map((c) => c.key);
  const wide = () => keys.map((key) => px("--tc-tasks-" + key));
  // Колонку растит та сторона, куда её граница отдаёт место: левее растяжимой
  // это ход вправо, правее её ход обратный.
  cells("tasks").forEach((cell, at) => {
    const grip = byClass(cell, "tblg");
    if (!grip) return;
    const was = wide();
    if (at < flexAt) drag(grip, 0, 5000); else drag(grip, 5000, 0);
    const now = wide();
    const lost = keys.filter((key, i) => now[i] < was[i]);
    if (lost.length) {
      fail("тяга границы " + at + " отняла место у колонок " + lost.join(",") +
        ": место при тяге берётся у растяжимой колонки, а не у соседей по своей ширине");
    }
  });
  const got = inCard("tasks", "все колонки растянуты до упора");
  if (got.flex > FLEX_MIN + 2) {
    fail("тяга до упора не дошла до предела растяжимой: у неё осталось " + got.flex +
      " точек при пределе " + FLEX_MIN + ", а границы уже стоят");
  }
}

if (!floor) {
  fail("растяжимой колонке не задан нижний предел (TBL_FLEX_MIN): соседи забирают остаток " +
    "до нуля, и таблица уезжает за карточку тем же концом");
}

console.log("poc_tblfit: ok");
