// Стенд тяги колонок табличного вида (POC DK-397, ветка poc-chat).
//
// Пользователь забраковал прежний вид словами «таблицы по сути тоже нет ни в
// одном разделе, нет колонок которые как правило можно двигать». Предмет
// стенда это классический жест: границу колонки в шапке тянут мышью, колонка
// растёт и жмётся вслед за курсором, ширина запоминается разделом и переживает
// перерисовку.
//
// Отдельно сторожатся три вещи, без которых жест обещает больше, чем делает:
// ширина доезжает до самой строки, а не только до шапки (правило сетки строки
// обязано читать те же переменные); растяжимую колонку тянут за её границу
// соседней, иначе строка вылезает за карточку; ручка стоит у всякой границы,
// кроме последней, справа от которой двигать нечего.
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
  const kept = JSON.parse(sandbox.localStorage.getItem("devkit.dash.tasks.cols") || "{}");
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
// всё равно едет туда, куда его тянут: вправо это шире название и уже ранг.
{
  const grip = byClass(cells("tasks")[1], "tblg");
  const was = px("--tc-tasks-rank");
  drag(grip, 500, 510);
  if (px("--tc-tasks-rank") !== was - 10) {
    fail("граница названия не отдала место соседней колонке: " +
      varOf("--tc-tasks-rank") + ", было " + was);
  }
  if (varOf("--tc-tasks-title")) {
    fail("растяжимой колонке назначена ширина, строка вылезет за карточку: " +
      varOf("--tc-tasks-title"));
  }
}

// --- пределы: колонка не схлопывается в ноль и не съедает строку ---
{
  const grip = byClass(cells("tasks")[2], "tblg");
  drag(grip, 500, 100);
  const small = px("--tc-tasks-rank");
  if (!(small >= 32)) fail("колонку удалось схлопнуть до " + small + " точек");
  drag(grip, 100, 3000);
  const big = px("--tc-tasks-rank");
  if (!(big <= 460)) fail("колонка съела строку целиком: " + big + " точек");
}

// --- двойное нажатие возвращает ширину по умолчанию ---
{
  const grip = byClass(cells("tasks")[2], "tblg");
  grip.handlers.dblclick({ stopPropagation: () => {} });
  if (px("--tc-tasks-rank") !== 44) {
    fail("двойное нажатие не вернуло ширину по умолчанию: " + varOf("--tc-tasks-rank"));
  }
}

// --- мусор в памяти ширин список не роняет ---
{
  sandbox.localStorage.setItem("devkit.dash.tasks.cols", "{не json");
  await go("#demo/sess");
  await go("#demo");
  if (!head("tasks")) fail("шапка не собралась после мусора в памяти ширин");
  if (px("--tc-tasks-id") !== 60) {
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
  for (const [hash, kind, count] of [["#demo/sess", "sess", 5], ["#demo/drafts", "drafts", 5]]) {
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

// --- ширина доезжает до строки, а не только до шапки ---
// Переменные кладёт скрипт, а читает их правило сетки в стилях: разойдись эти
// два места именами, шапка ездила бы отдельно от списка.
{
  for (const [sel, name] of [[".trow", "--tc-tasks-"], [".dsrow", "--tc-drafts-"],
    [".arow", "--tc-sess-"]]) {
    let saw = false;
    const re = new RegExp("(?:^|[\\n}])\\s*\\" + sel + "\\s*\\{([^}]*)\\}", "g");
    for (const m of CSS.matchAll(re)) {
      if (m[1].includes("grid-template-columns") && m[1].includes(name)) saw = true;
    }
    if (!saw) fail("сетка строки " + sel + " не читает ширины " + name + ": тяга до списка не доедет");
  }
}

// --- на телефоне тянуть нечего, и ручка там погашена ---
// Строка узкого экрана раскладывается по областям, колонок в ней нет вовсе.
{
  const blocks = [...CSS.matchAll(/@media \(max-width:900px\)\{([\s\S]*?)\n\}/g)];
  if (!blocks.length) fail("правил узкого экрана в стилях не нашлось");
  const off = blocks.some((m) => /\.tblg\{display:none\}/.test(m[1].replace(/\s+/g, "")));
  if (!off) fail("на узком экране ручка тяги осталась: правила .tblg{display:none} нет");
}

console.log("poc_tblcols: ok");
