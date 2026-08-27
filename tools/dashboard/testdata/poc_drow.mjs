// Стенд строки накопителя одним рядом (ветка poc-chat).
//
// После отметки выбора строка разъехалась на две: колонок в разметке стало на
// одну больше, чем ставило правило .srow, а лежало оно ниже по файлу и той же
// силы, поэтому перебивало колонки накопителя. Заодно строка показывала
// возраст записи днями («3 дня»), хотя на доске в этом месте стоит дата правки
// (замечание пользователя). Предмет стенда это порядок ячеек слева направо
// (отметка с важностью, номер, наименование, дата, чат), дата вместо дней и
// сходимость числа колонок с числом ячеек. С табличным видом (POC DK-397)
// строка это настоящая строка таблицы, колонки описаны в colgroup, и спорить о
// сетке больше нечему: расставляет колонки движок.
//
// Зовётся: node testdata/poc_drow.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { makeSandbox, browserKids, settle, dump, byClass, fail, appPathArg }
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

const row = browserKids(sandbox.draftRow("demo", draft));
const kids = [...row.children];
const cls = (node) => String((node && node.className) || "");

// --- порядок ячеек слева направо ---
{
  const want = ["dimp", "id", "dtt", "dwhen", "sm"];
  const got = kids.map((k) => want.find((w) => cls(k).split(" ").includes(w)) || cls(k) || "?");
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    fail("ячейки строки идут не тем порядком: " + JSON.stringify(got) + ", жду " + JSON.stringify(want));
  }
}

// --- отметка выбора и важность живут одной колонкой ---
// Врозь они занимали две, и подпись «Приоритет» в шапке переставала влезать,
// стоило сортировке добавить к ней значок направления (замечание пользователя).
{
  const imp = kids[0];
  if (!dump(imp).includes("средний")) {
    fail("важность записи не встала первой ячейкой: " + dump(row));
  }
  if (!byClass(imp, "dpick")) {
    fail("отметка выбора уехала из колонки важности: " + dump(imp));
  }
  if (kids.some((k, at) => at && byClass(k, "dpick"))) {
    fail("отметка выбора осталась своей колонкой: " + JSON.stringify(kids.map(cls)));
  }
}

// --- дата правки вместо возраста днями, и стоит она своей ячейкой ---
{
  const when = kids[3];
  const said = dump(when).replace(/\s+/g, " ");
  if (!said.includes("2026-08-20")) fail("даты правки записи в своей ячейке нет: " + said);
  for (const days of ["3 дня", "вчера", "неделю назад"]) {
    if (said.includes(days)) fail("возраст днями остался в строке: " + said);
  }
  // Кнопка чата стоит последней, после даты: порядок назвал пользователь.
  const meta = kids[4];
  // Кнопки хвоста лежат общей коробкой racts, той же, что у строки доски.
  const rack = byClass(meta, "racts") || byClass(meta, "cin") || meta;
  const inner = rack.children || [];
  const last = inner[inner.length - 1];
  if (!cls(last).includes("btn")) fail("последней в строке стоит не кнопка чата: " + dump(meta));
}

// --- строка это строка таблицы, а ячейки её ячейки ---
// Прежде накопитель стоял своей сеткой, и подписи в шапке приходилось
// приставлять к ячейкам руками. Теперь колонку у подписи со строкой считает
// движок, а для этого разметка обязана быть настоящей таблицей.
{
  if (row.tagName !== "TR") fail("строка накопителя не строка таблицы: " + row.tagName);
  const bad = kids.filter((k) => k.tagName !== "TD").map((k) => k.tagName);
  if (bad.length) fail("ячейки строки накопителя не ячейки таблицы: " + JSON.stringify(bad));
}

// --- колонок в разметке столько же, сколько ячеек в строке ---
// Ширины колонок едут в colgroup, и разойдись оно со строкой числом колонок,
// подписи в шапке встали бы мимо ячеек.
{
  const group = sandbox.tblColgroup("drafts");
  const cols = (group.children || []).filter((c) => c.tagName === "COL");
  if (cols.length !== kids.length) {
    fail("колонок в colgroup " + cols.length + ", а ячеек в строке " + kids.length +
      ": подписи встанут мимо");
  }
  // Ширина колонки читается из переменной раздела: тягу границы держит она, и
  // без неё правка ширины до строки не доедет.
  const wide = cols.filter((c) => String((c.style || {}).width || "").includes("--tc-drafts-"));
  if (wide.length !== cols.length - 1) {
    fail("ширины колонок накопителя не читают переменных раздела: " +
      JSON.stringify(cols.map((c) => (c.style || {}).width || "")));
  }
}

// --- наименование ужимается, а не переносится ---
{
  const css = fs.readFileSync(path.join(path.dirname(app), "style.css"), "utf8");
  const rule = /\.dsrow \.st\{([^}]*)\}/.exec(css);
  if (!rule || !rule[1].includes("text-overflow:ellipsis")) {
    fail("наименование записи на узком экране рвётся переносом, а не режется кромкой");
  }
  const meta = /\.dsrow \.sm>\.cin\{([^}]*)\}/.exec(css);
  if (!meta || !meta[1].includes("flex-wrap:nowrap")) {
    fail("приписки строки накопителя уезжают под заголовок второй строкой");
  }
}

console.log("poc_drow: ok");
