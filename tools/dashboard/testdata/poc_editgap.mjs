// Стенд зазора над полем правки (ветка poc-chat, DK-577).
//
// Живой случай: пользователь выделил на экране задачи поле заголовка в режиме
// правки. «При включении редактирования вот этот блок верхней границей
// касается элементов в блоке выше». В покое коробки поля не видно, и зазора
// никто не замечал; в правке коробка проявляется и упирается в то, что стоит
// над ней.
//
// Предмет стенда: расстояние до соседа сверху одинаково в покое и в правке, и
// оно не ноль. Меряется числом с настоящего style.css на узком экране и на
// широком, по всем местам, где текст правится.
//
// Зовётся: node testdata/poc_editgap.mjs static/app.js [--show]

import { makeSandbox, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";
import { cssRules, layoutOf, deepFind, hasClass } from "./poc_css.mjs";

const app = appPathArg();
const show = process.argv.includes("--show");
const rules = cssRules(app);
const WANT = ["margin", "margin-top", "margin-bottom", "padding", "padding-top", "gap"];
const fit = (node, width) => layoutOf(node, { rules, width, want: WANT });

// Число из объявления вида «10px» или «14px 18px»: сверху стоит первое.
const upOf = (text, side) => {
  if (!text) return null;
  const parts = String(text).trim().split(/\s+/);
  const top = parts[0];
  const bottom = parts.length >= 3 ? parts[2] : parts[0];
  const pick = side === "top" ? top : bottom;
  const m = /^(-?[0-9.]+)px$/.exec(pick);
  return m ? Number(m[1]) : (pick === "0" ? 0 : null);
};

// Верхний край поля совпадает с верхним краем строки, в которой оно стоит:
// строка это flex по базовой линии, и отбивают её от соседа сверху её же
// отступы, а не отступы поля. Поэтому мерится строка, а поле только когда
// стоит в потоке само по себе.
const rowOf = (node, width) => {
  const parent = node.parentNode;
  if (!parent) return node;
  const box = fit(parent, width);
  return box.gap !== undefined ? parent : node;
};

// Зазор до соседа сверху: свой отступ плюс отступ соседа снизу, а у первого
// ребёнка вместо соседа поле родителя.
const gapOf = (from, width) => {
  const node = rowOf(from, width);
  const own = fit(node, width);
  let up = upOf(own["margin-top"], "top");
  if (up === null) up = upOf(own.margin, "top");
  if (up === null) up = 0;
  const parent = node.parentNode;
  const kids = ((parent && parent.children) || []).filter((k) => !k.hidden);
  const at = kids.indexOf(node);
  if (at > 0) {
    const prev = fit(kids[at - 1], width);
    let down = upOf(prev["margin-bottom"], "bottom");
    if (down === null) down = upOf(prev.margin, "bottom");
    return up + (down === null ? 0 : down);
  }
  if (!parent) return up;
  const box = fit(parent, width);
  let pad = upOf(box["padding-top"], "top");
  if (pad === null) pad = upOf(box.padding, "top");
  let flex = upOf(box.gap, "top");
  return up + (pad === null ? 0 : pad) + (flex === null ? 0 : flex);
};

const board = { prefix: "XR", sections: [{ name: "In progress", rows: [
  { id: "XR-226", title: "заголовок задачи", type: "feat", rank: 5, cost: "M",
    status: "in-progress" }] }] };
const { sandbox, byId } = makeSandbox(app, (path) => {
  if (path === "/api/projects") return { projects: [{ name: "demo", prefix: "XR", works: [] }] };
  if (path === "/api/harnesses") return { harnesses: [] };
  if (path.endsWith("/board")) return { board, works: [] };
  if (path.includes("/tasks/XR-226")) {
    return { row: board.sections[0].rows[0], file: "docs/tasks/XR-226.md",
      text: "## Что происходит\n\nпостановка задачи", links: [], after: [], blocks: [] };
  }
  if (path.endsWith("/drafts")) return { drafts: [], works: [] };
  if (path.includes("/chats")) return { chats: [], models: [] };
  return {};
});

sandbox.location.hash = "#demo/XR-226";
await sandbox.refresh();
await settle();
const page = byClass(byId.get("groups"), "tpage");
if (!page) fail("экран задачи не собрался: " + dump(byId.get("groups")).slice(0, 200));

const tagIs = (name) => (n) => String(n.tagName || "").toLowerCase() === name;
const title = deepFind(byClass(page, "thline"), tagIs("textarea"))[0];
if (!title) fail("поля заголовка на экране задачи нет");
const body = byClass(page, "fbody");
if (!body) fail("панели постановки на экране задачи нет");
const view = byClass(body, "fview");
const area = deepFind(body, tagIs("textarea"))[0];
if (!view || !area) fail("у панели постановки нет пары просмотр-правка");

// Правка включается тем же способом, что в самом экране: у заголовка снимается
// метка покоя, у панели меняется видимый узел.
const places = [];
for (const width of [390, 1440]) {
  title.classList.add("ro");
  view.hidden = false;
  area.hidden = true;
  const restTitle = gapOf(title, width);
  const restText = gapOf(view, width);
  title.classList.remove("ro");
  view.hidden = true;
  area.hidden = false;
  const liveTitle = gapOf(title, width);
  const liveText = gapOf(area, width);
  places.push(["заголовок задачи", width, restTitle, liveTitle],
    ["постановка задачи", width, restText, liveText]);
}

for (const [name, width, rest, live] of places) {
  if (show) console.log("[" + width + "] " + name + ": покой " + rest + ", правка " + live);
  if (rest !== live) {
    fail("на " + width + " точках у «" + name + "» правка двигает соседей: зазор " +
      "сверху в покое " + rest + ", в правке " + live);
  }
  if (!(rest > 0)) {
    fail("на " + width + " точках у «" + name + "» поле правки упирается в блок " +
      "выше: зазор сверху " + rest);
  }
}

console.log("ок: зазор над полем правки одинаков в покое и в правке и не ноль, " +
  "снято числом с заголовка задачи и панели постановки на 390 и 1440 точках");
