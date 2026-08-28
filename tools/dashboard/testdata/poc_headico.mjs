// Стенд заголовка колонки хода (POC DK-397, ветка poc-chat).
//
// Прошлый заход снял у колонки заголовок вовсе, лишь бы ужать её с восьмидесяти
// точек до тридцати шести. Человек это забраковал: «в сессиях ты вообще убрал
// название колонки Ход, от этого стало только хуже. Надо было просто сделать
// колонку компактнее сохранив возможность сортировки по ней. Например заменив
// заголовок иконкой и уменьшив отступы в колонке».
//
// Отсюда предмет стенда: заголовок у колонки есть, нарисован он значком,
// сортировка по нажатию работает и разворачивается, а что это за колонка,
// подсказка говорит словами. Отдельно сторожится, что ужалась колонка
// отступами: расширенного поля под кружок у неё больше нет.
//
// Зовётся: node testdata/poc_headico.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { makeSandbox, settle, byClass, allByClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();
const dir = path.dirname(app);
const SRC = fs.readFileSync(app, "utf8");
const CSS = fs.readFileSync(path.join(dir, "style.css"), "utf8");
const HTML = fs.readFileSync(path.join(dir, "index.html"), "utf8");

const works = [
  { kind: "session", via: "session", session: "s-busy", own: true, live: "busy",
    title: "груминг накопителя", started: 3000, moved: 9000 },
];

const { sandbox, byId } = makeSandbox(app, (path_) => {
  if (path_ === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works }] };
  if (path_ === "/api/harnesses") return { harnesses: [] };
  if (path_ === "/api/notifications") return { items: [] };
  if (path_.endsWith("/board")) {
    return { board: { prefix: "XR", sections: [{ name: "В работе", key: "in-progress",
      rows: [{ id: "XR-1", title: "строка доски", r: 68, p: "P1", cost: "M",
        type: "feature", moved: "2026-08-25" }] }] }, works };
  }
  if (path_.endsWith("/works")) return { works };
  if (path_.endsWith("/drafts")) return { drafts: [] };
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
await go("#demo/sess");

const head = allByClass(groups, "tblh")
  .find((h) => String(h.className).split(" ").includes("h-sess"));
if (!head) fail("шапки раздела сессий на экране нет");
const cell = head.children[0];

// --- заголовок у колонки есть, и он нажимается ---
const btn = byClass(cell, "tblb");
if (!btn) fail("у колонки хода нет кнопки порядка: заголовок сняли вместе с сортировкой");

// --- заголовок нарисован значком, а не словом ---
if (!byClass(cell, "tblico")) {
  fail("в шапке колонки хода нет значка: подписывать её нечем");
}
const word = byClass(cell, "tbll");
if (word && String(word.textContent || "").trim()) {
  fail("колонка хода подписана словом «" + word.textContent + "»: под слово она " +
    "и занимала восемьдесят точек");
}
// Значок берётся из спрайта разметки, и спрашивать его надо по имени, которое
// там правда лежит: пустой значок так же безымян, как снятый заголовок.
const named = /key: "live",[^}]*ico: "([a-z0-9-]+)"/.exec(SRC);
if (!named) fail("у колонки хода в TBL_COLS не назван значок заголовка");
if (!HTML.includes('data-ico="' + named[1] + '"')) {
  fail("значок «" + named[1] + "» шапка просит, а в спрайте index.html его нет");
}

// --- подсказка называет колонку словами ---
const say = String(btn.attrs["aria-label"] || "");
if (say !== String(btn.title || "")) {
  fail("подсказка и подпись для чтения с экрана разошлись: " + say + " / " + btn.title);
}
if (!/ходу работы/.test(say)) {
  fail("подсказка колонки со значком не называет её словами: «" + say + "»");
}

// --- сортировка по колонке жива и разворачивается ---
const sortNow = () => sandbox.localStorage.getItem("devkit.dash.sess.sort") || "";
btn.handlers.click({ stopPropagation() {} });
await settle();
if (sortNow() !== "live:desc") {
  fail("нажатие на заголовок хода не развернуло порядок: " + sortNow());
}
const again = byClass(allByClass(groups, "tblh")
  .find((h) => String(h.className).split(" ").includes("h-sess")).children[0], "tblb");
again.handlers.click({ stopPropagation() {} });
await settle();
if (sortNow() !== "live:asc") {
  fail("порядок по ходу работы не развернулся обратно: " + sortNow());
}

// --- колонка ужата отступом, а не снятым заголовком ---
const flat = CSS.replace(/\s+/g, " ");
if (!/\.t-sess th:first-child,\s*\.t-sess td:first-child,/.test(flat)) {
  fail("первая ячейка сессий держит расширенный отступ под кружок, которого рядом " +
    "с текстом нет: правила с обычным отступом в стилях не нашлось");
}
const w = /key: "live",[^}]*w: (\d+)/.exec(SRC);
if (!w) fail("ширина колонки хода в TBL_COLS не читается");
if (Number(w[1]) > 56) {
  fail("колонка хода снова широкая: " + w[1] + " точек, а несёт она кружок со значком");
}

// --- та же мерка колонке ранга на доске задач ---
//
// Колонка ранга пришла к значку той же дорогой, что колонка хода: замер
// показал, что ширину держит слово «Ранг» со значком направления, а внутри
// двузначное число вдвое уже. Проверки тут те же, и стоят они рядом нарочно:
// правило одно на обе колонки, и разъезжаться им незачем.
await go("#demo");
const thead = allByClass(groups, "tblh")
  .find((h) => String(h.className).split(" ").includes("h-tasks"));
if (!thead) fail("шапки раздела задач на экране нет");
const rcell = Array.from(thead.children)
  .find((th) => byClass(th, "tblb") && /рангу/.test(String(byClass(th, "tblb").title || "")));
if (!rcell) fail("колонки ранга в шапке доски нет");

if (!byClass(rcell, "tblico")) {
  fail("в шапке колонки ранга нет значка: подписывать её нечем");
}
const rword = byClass(rcell, "tbll");
if (rword && String(rword.textContent || "").trim()) {
  fail("колонка ранга подписана словом «" + rword.textContent + "»: под слово она и " +
    "держала пятьдесят шесть точек при двузначном числе внутри");
}
const rnamed = /key: "rank",[^}]*ico: "([a-z0-9-]+)"/.exec(SRC);
if (!rnamed) fail("у колонки ранга в TBL_COLS не назван значок заголовка");
if (!HTML.includes('data-ico="' + rnamed[1] + '"')) {
  fail("значок «" + rnamed[1] + "» шапка просит, а в спрайте index.html его нет");
}

const rbtn = byClass(rcell, "tblb");
const rsay = String(rbtn.attrs["aria-label"] || "");
if (rsay !== String(rbtn.title || "")) {
  fail("у ранга подсказка и подпись для чтения с экрана разошлись: " + rsay);
}
if (!/рангу/.test(rsay)) {
  fail("подсказка колонки ранга не называет её словами: «" + rsay + "»");
}

const rankSort = () => sandbox.localStorage.getItem("devkit.dash.tasks.sort") || "";
rbtn.handlers.click({ stopPropagation() {} });
await settle();
if (rankSort() !== "rank:desc") {
  fail("нажатие на заголовок ранга не задало порядок: " + rankSort());
}

// Ширина колонки: она несёт двузначное число, и держать её должно содержимое, а
// не подпись. Сорок точек это тот же рубеж, что у хода со значком.
const rw = /key: "rank",[^}]*w: (\d+)/.exec(SRC);
if (!rw) fail("ширина колонки ранга в TBL_COLS не читается");
if (Number(rw[1]) > 40) {
  fail("колонка ранга снова широкая: " + rw[1] + " точек при двузначном числе внутри");
}

console.log("poc_headico: ok");
