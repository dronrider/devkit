// Стенд тяги колонок табличного вида (POC DK-397, ветка poc-chat).
//
// Пользователь забраковал прежний вид словами «таблицы по сути тоже нет ни в
// одном разделе, нет колонок которые как правило можно двигать». Предмет
// стенда это классический жест: границу колонки в шапке тянут мышью, колонка
// растёт и жмётся вслед за курсором, ширина запоминается разделом и переживает
// перерисовку.
//
// Отдельно сторожатся три вещи, без которых жест обещает больше, чем делает:
// ширина доезжает до colgroup, откуда её читает движок (колонка у шапки со
// строкой одна, и дальше ширина едет сама); растяжимую колонку тянут за её
// границу соседней, иначе таблица вылезает за карточку; ручка стоит у всякой
// границы, кроме последней, справа от которой двигать нечего.
//
// Зовётся: node testdata/poc_tblcols.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { makeSandbox, settle, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const CSS = fs.readFileSync(path.join(path.dirname(app), "style.css"), "utf8");

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
const varOf = (name) => sandbox.document.documentElement.style.props[name] || "";
const px = (name) => Number(String(varOf(name)).replace("px", ""));

// Ширина по умолчанию читается из самой разметки: colgroup ставит колонке
// «var(--tc-tasks-rank, 58px)», и запасное значение это и есть умолчание из
// TBL_COLS. Копия числа в стенде разошлась бы с кодом молча, а стенд об этом
// не сказал бы: он сторожил бы прежнее умолчание.
const defw = (key) => {
  const col = allByClass(groups, "cw-" + key)[0];
  const said = String(((col || {}).style || {}).width || "");
  const m = /,\s*(\d+)px\)/.exec(said);
  if (!m) fail("умолчание ширины колонки «" + key + "» не читается из colgroup: " + said);
  return Number(m[1]);
};

// Тяга: нажатие на ручке, ход мыши, отпускание. Ход идёт через обработчики
// документа, как их и вешает жест: браузер шлёт pointermove не в ручку, а туда,
// куда уехал курсор.
const drag = (grip, from, to) => {
  grip.handlers.pointerdown({ clientX: from, stopPropagation: () => {}, preventDefault: () => {} });
  sandbox.document.handlers.pointermove({ clientX: to });
  sandbox.document.handlers.pointerup({});
};

// --- ручка стоит у всякой колонки, кроме последней ---
{
  await go("#demo");
  const list = cells("tasks");
  if (list.length !== 5) fail("колонок в шапке доски не пять: " + list.length);
  const grips = list.map((cell) => Boolean(byClass(cell, "tblg")));
  if (JSON.stringify(grips) !== JSON.stringify([true, true, true, true, false])) {
    fail("ручки тяги стоят не у тех границ: " + JSON.stringify(grips));
  }
}

// --- граница обычной колонки тянет её саму ---
{
  const grip = byClass(cells("tasks")[0], "tblg");
  const was = px("--tc-tasks-id");
  if (!(was > 0)) fail("ширина колонки номера не встала переменной: " + varOf("--tc-tasks-id"));
  drag(grip, 100, 140);
  if (px("--tc-tasks-id") !== was + 40) {
    fail("колонка номера не поехала за курсором: " + varOf("--tc-tasks-id") + ", было " + was);
  }
  // Ширина живёт своим ключом хранилища и переживает уход с экрана.
  const kept = JSON.parse(sandbox.localStorage.getItem("devkit.dash.tasks.tblcols") || "{}");
  if (kept.id !== was + 40) {
    fail("ширина не записалась в память раздела: " + JSON.stringify(kept));
  }
  await go("#demo/sess");
  await go("#demo");
  if (px("--tc-tasks-id") !== was + 40) {
    fail("ширина не пережила перерисовку: " + varOf("--tc-tasks-id"));
  }
}

// --- граница растяжимой колонки тянет соседнюю обратным ходом ---
// Своей ширины у названия нет, оно подбирает остаток строки. Курсор при этом
// всё равно едет туда, куда его тянут: влево это уже название и шире ранг.
// Тянем именно влево: ранг с тех пор, как подписан значком, стоит в сорок
// точек, и десять вправо упёрлись бы в общий пол ширины, а пол проверяется
// ниже своим случаем.
{
  const grip = byClass(cells("tasks")[1], "tblg");
  const was = px("--tc-tasks-rank");
  drag(grip, 510, 500);
  if (px("--tc-tasks-rank") !== was + 10) {
    fail("граница названия не забрала место у соседней колонки: " +
      varOf("--tc-tasks-rank") + ", было " + was);
  }
  if (varOf("--tc-tasks-title")) {
    fail("растяжимой колонке назначена ширина, строка вылезет за карточку: " +
      varOf("--tc-tasks-title"));
  }
}

// --- у всякой колонки со своей шириной своя ручка ---
// Колонку действий было не ужать вовсе: границы правили колонку слева от себя,
// у последней колонки границы справа нет, и ручка ей не доставалась, а
// соседнюю двигали сразу две границы (замечание пользователя). Сторожится
// счёт: границ столько же, сколько колонок со своей шириной, и каждая ручка
// правит свою.
{
  await go("#demo");
  const list = cells("tasks");
  const keys = ["id", "title", "rank", "date", "act"];
  const moved = [];
  list.forEach((cell, at) => {
    const grip = byClass(cell, "tblg");
    if (!grip) return;
    const was = keys.map((key) => px("--tc-tasks-" + key) || 0);
    drag(grip, 400, 420);
    const now = keys.map((key) => px("--tc-tasks-" + key) || 0);
    const hit = keys.filter((key, i) => now[i] !== was[i]);
    if (hit.length !== 1) {
      fail("ручка у границы " + at + " сдвинула колонок: " + JSON.stringify(hit));
    }
    moved.push(hit[0]);
  });
  const want = keys.filter((key) => key !== "title");
  if ([...moved].sort().join(",") !== [...want].sort().join(",")) {
    fail("ручки правят колонки " + moved.join(",") + ", а своя ширина есть у " + want.join(","));
  }
  if (!moved.includes("act")) fail("колонку действий не ужать: своей ручки у неё нет");
  // Память ширин после обхода портит следующие случаи: сбрасываем её.
  sandbox.localStorage.setItem("devkit.dash.tasks.tblcols", "{}");
  await go("#demo/sess");
  await go("#demo");
}

// --- пределы: колонка не схлопывается в ноль и не съедает строку ---
{
  const grip = byClass(cells("tasks")[1], "tblg");
  drag(grip, 500, 100);
  const wide = px("--tc-tasks-rank");
  if (!(wide <= 460)) fail("колонка съела строку целиком: " + wide + " точек");
  drag(grip, 100, 3000);
  const small = px("--tc-tasks-rank");
  if (!(small >= 32)) fail("колонку удалось схлопнуть до " + small + " точек");
}

// --- двойное нажатие возвращает ширину по умолчанию ---
{
  const grip = byClass(cells("tasks")[1], "tblg");
  grip.handlers.dblclick({ stopPropagation: () => {} });
  if (px("--tc-tasks-rank") !== defw("rank")) {
    fail("двойное нажатие не вернуло ширину по умолчанию: " + varOf("--tc-tasks-rank"));
  }
}

// --- мусор в памяти ширин список не роняет ---
{
  sandbox.localStorage.setItem("devkit.dash.tasks.tblcols", "{не json");
  await go("#demo/sess");
  await go("#demo");
  if (!head("tasks")) fail("шапка не собралась после мусора в памяти ширин");
  if (px("--tc-tasks-id") !== defw("id")) {
    fail("мусор в памяти не откатил ширину к умолчанию: " + varOf("--tc-tasks-id"));
  }
}

// --- пока граница едет, текст подписей не выделяется ---
// Курсор при тяге ходит по подписям колонок, и без запрета браузер красит их
// синим: жест выглядел бы выделением строки, а не тягой.
{
  const grip = byClass(cells("tasks")[0], "tblg");
  grip.handlers.pointerdown({ clientX: 10, stopPropagation: () => {}, preventDefault: () => {} });
  if (!sandbox.document.body.classList.contains("tbldrag")) {
    fail("тяга не запретила выделение текста: " + sandbox.document.body.className);
  }
  sandbox.document.handlers.pointerup({});
  if (sandbox.document.body.classList.contains("tbldrag")) {
    fail("запрет выделения остался после отпускания: " + sandbox.document.body.className);
  }
}

// --- тяга есть у всех трёх разделов ---
{
  for (const [hash, kind, count] of [["#demo/sess", "sess", 4], ["#demo/drafts", "drafts", 5]]) {
    await go(hash);
    const list = cells(kind);
    if (list.length !== count) {
      fail("колонок в шапке раздела " + kind + " не " + count + ": " + list.length);
    }
    const grips = list.filter((cell) => byClass(cell, "tblg")).length;
    if (grips !== count - 1) {
      fail("в разделе " + kind + " ручек тяги " + grips + " при " + count + " колонках");
    }
    const grip = byClass(list[0], "tblg");
    const name = "--tc-" + kind + "-" + (kind === "sess" ? "live" : "prio");
    const was = px(name);
    drag(grip, 200, 225);
    if (px(name) !== was + 25) {
      fail("в разделе " + kind + " колонка не поехала за курсором: " + varOf(name));
    }
  }
}

// --- ширина доезжает до самой таблицы, а не только до шапки ---
// Переменные кладёт скрипт, а читает их colgroup: разойдись эти два места
// именами, тяга правила бы переменную, которой никто не спрашивает. Колонка
// одна на шапку и на строку, поэтому доехав до colgroup, ширина доезжает и до
// списка.
{
  if (typeof sandbox.tblColgroup !== "function") {
    fail("колонок таблице не описано вовсе: colgroup не собирается, и ширины некуда положить");
  }
  for (const [kind, keys] of [["tasks", ["id", "rank", "date", "act"]],
    ["drafts", ["prio", "id", "date", "act"]],
    ["sess", ["live", "moved", "act"]]]) {
    const group = sandbox.tblColgroup(kind);
    const cols = (group.children || []).filter((c) => c.tagName === "COL");
    const said = cols.map((c) => String((c.style || {}).width || ""));
    for (const key of keys) {
      if (!said.some((w) => w.includes("--tc-" + kind + "-" + key))) {
        fail("колонка " + key + " раздела " + kind + " не читает свою ширину: " +
          JSON.stringify(said));
      }
    }
    // Растяжимой колонке ширины не назначено вовсе: остаток строки движок
    // отдаёт ей, а число тут увело бы таблицу за край карточки.
    if (said.filter((w) => !w).length !== 1) {
      fail("растяжимая колонка раздела " + kind + " не одна: " + JSON.stringify(said));
    }
  }
  // Раскладку колонок считает движок по colgroup, а не наши правила сетки.
  if (!/\.tbl\{[^}]*table-layout:fixed/.test(CSS.replace(/\s+/g, " "))) {
    fail("таблица не просит движок считать колонки по colgroup: table-layout:fixed нет");
  }
}

// --- на телефоне тянуть нечего, и ручка там погашена ---
// Таблица на узком экране переводится в блочный вид: строка раскладывается по
// областям, колонок в ней нет вовсе, и границе неоткуда взяться.
{
  const blocks = [...CSS.matchAll(/@media \(max-width:900px\)\{([\s\S]*?)\n\}/g)];
  if (!blocks.length) fail("правил узкого экрана в стилях не нашлось");
  const flat = blocks.map((m) => m[1].replace(/\s+/g, ""));
  if (!flat.some((one) => /\.tblg\{display:none\}/.test(one))) {
    fail("на узком экране ручка тяги осталась: правила .tblg{display:none} нет");
  }
  if (!flat.some((one) => /\.tbl\{display:block/.test(one))) {
    fail("на узком экране таблица осталась таблицей: правила .tbl{display:block} нет");
  }
  if (!flat.some((one) => /\.tbl>colgroup\{display:none\}/.test(one))) {
    fail("на узком экране колонки colgroup остались в силе: строка не ляжет по областям");
  }
}

console.log("poc_tblcols: ok");
