// Стенд строки накопителя одним рядом (ветка poc-chat).
//
// После отметки выбора строка разъехалась на две: колонок в разметке стало на
// одну больше, чем ставит правило .srow, а лежит оно ниже по файлу и той же
// силы, поэтому перебивало колонки накопителя. Заодно строка показывала
// возраст записи днями («3 дня»), хотя на доске в этом месте стоит дата правки
// (замечание пользователя). Предмет стенда это порядок ячеек слева направо
// (отметка, важность, номер, наименование, дата, чат), дата вместо дней и
// сходимость числа колонок с числом ячеек. С табличным видом (POC DK-397) дата
// стоит своей ячейкой, а не припиской в хвосте: по ней сортирует шапка, и
// колонки, которой нет в сетке, шапка обещать не вправе. Колонки считаются по
// широкой раскладке: узкая лежит своим правилом в @media и колонок держит
// меньше нарочно, там строка ложится в два ряда.
//
// Зовётся: node testdata/poc_drow.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { makeSandbox, browserKids, settle, dump, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const draft = { id: "XR-D1", title: "мысль с телефона, длинная и с телефона",
  age_words: "3 дня", moved: "2026-08-20", prio: "mid", order: "Проведи груминг XR-D1" };

const { sandbox } = makeSandbox(app, (path2, init) => {
  if (init && init.method === "POST") return { message: "груминг поднят" };
  if (path2 === "/api/harnesses") return { harnesses: [] };
  if (path2 === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path2.includes("/chats")) return { chats: [], models: [] };
  return {};
});
await settle();

// Широкая раскладка это стили без блоков @media: узкие правила лежат там и
// колонок держат меньше нарочно, а спор .srow с .dsrow идёт как раз в общей
// части файла.
function wideCss(css) {
  let out = "";
  let at = 0;
  while (at < css.length) {
    const start = css.indexOf("@media", at);
    if (start < 0) {
      out += css.slice(at);
      break;
    }
    out += css.slice(at, start);
    let depth = 0;
    let i = css.indexOf("{", start);
    if (i < 0) break;
    for (; i < css.length; i += 1) {
      if (css[i] === "{") depth += 1;
      else if (css[i] === "}") {
        depth -= 1;
        if (!depth) break;
      }
    }
    at = i + 1;
  }
  return out;
}

const row = browserKids(sandbox.draftRow("demo", draft));
const kids = [...row.children];
const cls = (node) => String((node && node.className) || "");

// --- порядок ячеек слева направо ---
{
  const want = ["dpick", "dimp", "id", "dtt", "dwhen", "sm"];
  const got = kids.map((k) => want.find((w) => cls(k).split(" ").includes(w)) || cls(k) || "?");
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    fail("ячейки строки идут не тем порядком: " + JSON.stringify(got) + ", жду " + JSON.stringify(want));
  }
}

// --- важность стоит перед номером своей ячейкой ---
{
  const imp = kids[1];
  if (!dump(imp).includes("средний")) {
    fail("важность записи не встала перед номером: " + dump(row));
  }
}

// --- дата правки вместо возраста днями, и стоит она своей ячейкой ---
{
  const when = kids[4];
  const said = dump(when).replace(/\s+/g, " ");
  if (!said.includes("2026-08-20")) fail("даты правки записи в своей ячейке нет: " + said);
  for (const days of ["3 дня", "вчера", "неделю назад"]) {
    if (said.includes(days)) fail("возраст днями остался в строке: " + said);
  }
  // Кнопка чата стоит последней, после даты: порядок назвал пользователь.
  const meta = kids[5];
  const last = (meta.children || [])[(meta.children || []).length - 1];
  if (!cls(last).includes("btn")) fail("последней в строке стоит не кнопка чата: " + dump(meta));
}

// --- колонок в разметке столько же, сколько ячеек в строке ---
// Правило .srow лежит ниже по файлу и той же силы, что .dsrow: колонки
// накопителя обязаны стоять после него, иначе строка рвётся на две.
{
  const css = wideCss(fs.readFileSync(path.join(path.dirname(app), "style.css"), "utf8"));
  // Последнее по файлу правило селектора: при равной силе побеждает оно, и
  // спорят тут ровно два селектора строки накопителя.
  const lastRule = (sel) => {
    let out = null;
    const re = new RegExp("(?:^|[\\n}])\\s*\\" + sel + "\\s*\\{([^}]*)\\}", "g");
    for (const m of css.matchAll(re)) {
      const grid = /grid-template-columns:([^;}]*)/.exec(m[1]);
      if (grid) out = { at: m.index, cols: grid[1].trim(), sel };
    }
    return out;
  };
  const own = lastRule(".dsrow");
  const common = lastRule(".srow");
  if (!own) fail("колонок строке накопителя не задано вовсе");
  if (common && common.at > own.at) {
    fail("колонки строки накопителя перебиты правилом " + common.sel + " (" + common.cols +
      "): строка порвётся на две");
  }
  const count = own.cols.split(/\s+(?![^(]*\))/).filter(Boolean).length;
  if (count !== kids.length) {
    fail("колонок " + count + " (" + own.cols + "), а ячеек в строке " + kids.length +
      ": строка порвётся на две");
  }
}

// --- наименование ужимается, а не переносится ---
{
  const css = fs.readFileSync(path.join(path.dirname(app), "style.css"), "utf8");
  const rule = /\.dsrow \.st\{([^}]*)\}/.exec(css);
  if (!rule || !rule[1].includes("text-overflow:ellipsis")) {
    fail("наименование записи на узком экране рвётся переносом, а не режется кромкой");
  }
  const meta = /\.dsrow \.sm\{([^}]*)\}/.exec(css);
  if (!meta || !meta[1].includes("grid-column:auto")) {
    fail("приписки строки накопителя уезжают под заголовок второй строкой");
  }
}

console.log("poc_drow: ok");
