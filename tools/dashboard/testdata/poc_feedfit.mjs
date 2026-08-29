// Стенд выравнивания записей ленты (ветка poc-chat, DK-577).
//
// Живой случай со снимка: «Позиция блоков сообщений в чате слева или отступ
// плавает. Не понял, от чего это зависит. Все блоки чата должны быть одинаковой
// ширины и одинаково выровнены». Коробки записей при замере сошлись, а вот
// текст внутри них начинался в четырёх разных местах: у пузыря его отодвигали
// поля и рамка, у свёрнутого блока одна рамка, у хода инструмента ничего.
// Пользователь выбрал сводить край текста и на выросшую ленту согласился.
//
// Предмет стенда: у текста всех видов записи один левый край и одна правая
// граница, снятые числом с настоящего style.css. Виды берутся из живой ленты
// разговора (feed_kinds.json, снято с чата devkit 8257b5e0), а не выдуманы:
// служебная строка и пересказ сжатия дописаны в набор, потому что в том
// разговоре их не случилось. Меряется на узком экране и на широком.
//
// Зовётся: node testdata/poc_feedfit.mjs static/app.js [--show]

import { makeSandbox, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { cssRules, layoutOf, deepFind, hasClass } from "./poc_css.mjs";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";

const app = appPathArg();
const show = process.argv.includes("--show");
const rules = cssRules(app);
const WANT = ["padding", "padding-left", "padding-right", "margin", "margin-left",
  "margin-right", "max-width", "width", "align-self", "border", "border-left",
  "border-right", "background"];
const fit = (node, width) => layoutOf(node, { rules, width, want: WANT });

// Число из объявления: «17px» или «12px 16px» (слева это четвёртое или второе).
const sideOf = (text, side) => {
  if (text === undefined || text === null) return null;
  const p = String(text).trim().split(/\s+/);
  const left = p.length >= 4 ? p[3] : (p.length >= 2 ? p[1] : p[0]);
  const right = p.length >= 2 ? p[1] : p[0];
  const pick = side === "left" ? left : right;
  const m = /^(-?[0-9.]+)px$/.exec(pick);
  return m ? Number(m[1]) : (pick === "0" ? 0 : null);
};

const oneSide = (box, kind, side) => {
  let got = sideOf(box[kind + "-" + side], side);
  if (got === null) got = sideOf(box[kind], side);
  return got === null ? 0 : got;
};

// Ширина рамки со стороны: она тоже отодвигает текст.
const bordOf = (box, side) => {
  const one = box["border-" + side] || box.border;
  const m = /^\s*(-?[0-9.]+)px/.exec(String(one || ""));
  return m ? Number(m[1]) : 0;
};

// Отступ текста от края записи: складываются поля, рамки и отступы всех узлов
// от тела строки до узла с самим текстом. Считается именно от тела, а не от
// края ленты: свой отступ строки (нить с кружком, а у бокового журнала
// субагента ещё и ступенька вложенности) несёт смысл и остаётся, а вопрос
// стоит про одинаковость текста внутри записи.
const inkOf = (body, width, side) => {
  const said = deepFind(body, (n) =>
    !n.hidden && typeof n.textContent === "string" && n.textContent.trim() !== "");
  let best = null;
  for (const node of said) {
    let sum = 0;
    let hid = false;
    for (let n = node; n && n !== body; n = n.parentNode) {
      if (n.hidden) { hid = true; break; }
      // Своё поле узла с текстом считается наравне с полями предков: текст
      // отодвигает и оно.
      const box = fit(n, width);
      sum += oneSide(box, "margin", side) + oneSide(box, "padding", side) +
        bordOf(box, side);
    }
    if (hid) continue;
    if (best === null || sum < best) best = sum;
  }
  return best === null ? 0 : best;
};

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

items = JSON.parse(readFileSync(join(dirname(app), "..", "testdata", "feed_kinds.json"), "utf8"));

const panel = sandbox.chatPanel("demo", {
  addr: "s", sid: "aaaa5779-3333", chats: [], models: [], project: "demo",
  entry: { tmux: "chat-9", state: "live", login: true },
});
await settle();

const feed = byClass(panel, "chatfeed");
if (!feed) fail("ленты разговора нет: " + dump(panel).slice(0, 200));
const rows = deepFind(feed, hasClass("frow"));
if (rows.length < 4) fail("в ленте меньше записей, чем положено: " + rows.length);

// Запись входа собирается тем же сборщиком и стоит в ленте разговора наравне
// с прочими видами.
const talk = byClass(panel, "cbyetalk");
if (talk) rows.push(...deepFind(talk, hasClass("frow")));

const kindOf = (row) => {
  const cls = String(row.className || "");
  const lead = ["f-bub", "f-fold", "f-head", "f-line", "f-tline"].find((k) => cls.includes(k));
  const role = (cls.match(/r-([a-z]+)/) || [])[1] || "?";
  return role + "/" + (lead || "?");
};

for (const width of [390, 1440]) {
  const seen = [];
  for (const row of rows) {
    // Тело записи это то, что стоит в колонке рядом с кружком.
    const body = byClass(row, "frowb");
    if (!body) fail("у строки ленты нет тела: " + dump(row).slice(0, 150));
    const node = (body.children || []).find((n) => n && !n.hidden);
    if (!node) continue;
    seen.push({
      kind: kindOf(row),
      left: inkOf(body, width, "left"),
      right: inkOf(body, width, "right"),
    });
  }
  if (show) {
    console.log("[" + width + "]");
    for (const s of seen) {
      console.log("   " + s.kind.padEnd(18) + " слева " + s.left + ", справа " + s.right);
    }
  }
  const first = seen[0];
  for (const s of seen) {
    if (s.left !== first.left) {
      fail("на " + width + " точках левый край записей плавает: «" + first.kind +
        "» слева " + first.left + ", «" + s.kind + "» слева " + s.left);
    }
    if (s.right !== first.right) {
      fail("на " + width + " точках правая граница записей плавает: «" + first.kind +
        "» справа " + first.right + ", «" + s.kind + "» справа " + s.right);
    }
  }
}

console.log("ок: у всех видов записи ленты один левый край и одна правая " +
  "граница, снято числом на 390 и 1440 точках");
