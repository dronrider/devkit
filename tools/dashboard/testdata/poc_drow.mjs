// Стенд строки накопителя одним рядом (ветка poc-chat, колонки DK-1564).
//
// Строка накопителя ложится в общую таблицу с секцией согласования, и колонки
// у них одни (DK-1564, утверждённый макет): отметка выбора, номер, название,
// тип, ранг, цена, состояние и источник. Дата из строки ушла на экран записи,
// приоритет стал колонкой ранга словом, а кнопка чата уехала вовсе: вход в
// разговор держит экран записи, и строка ведёт туда касанием.
//
// Предмет стенда: порядок ячеек слева направо, слово ранга своей колонкой,
// состояние по факту без работ и ожидания, пустые цена и источник у записи,
// сходимость числа колонок с числом ячеек и кромка наименования.
//
// Зовётся: node testdata/poc_drow.mjs static/app.js

import fs from "node:fs";
import path from "node:path";
import { makeSandbox, browserKids, settle, dump, byClass, fail, appPathArg }
  from "./poc_dom.mjs";

const app = appPathArg();

const draft = { id: "XR-D1", title: "мысль с телефона, длинная и с телефона",
  age_words: "3 дня", moved: "2026-08-20", prio: "mid", type: "task",
  order: "Проведи груминг XR-D1" };

const { sandbox } = makeSandbox(app, (path2, init) => {
  if (init && init.method === "POST") return { message: "груминг поднят" };
  if (path2 === "/api/harnesses") return { harnesses: [] };
  if (path2 === "/api/projects") return { projects: [{ name: "demo", works: [] }] };
  if (path2.includes("/chats")) return { chats: [], models: [] };
  return {};
});
await settle();

const row = browserKids(sandbox.draftRow("demo", draft,
  { task: "Задача" }, []));
const kids = [...row.children];
const cls = (node) => String((node && node.className) || "");

// --- порядок ячеек слева направо ---
{
  const want = ["dimp", "id", "dtt", "dtype", "drank", "dsize", "dst", "dsrc"];
  const got = kids.map((k) => want.find((w) => cls(k).split(" ").includes(w)) || cls(k) || "?");
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    fail("ячейки строки идут не тем порядком: " + JSON.stringify(got) + ", жду " + JSON.stringify(want));
  }
}

// --- отметка выбора стоит первой колонкой без слова важности ---
// Важность записи читается колонкой ранга словом: в первой ячейке вместе с
// отметкой она занимала место и не сортировалась.
{
  const imp = kids[0];
  if (!byClass(imp, "dpick")) {
    fail("отметка выбора уехала из первой ячейки: " + dump(imp));
  }
  if (kids.some((k, at) => at && byClass(k, "dpick"))) {
    fail("отметка выбора осталась своей колонкой: " + JSON.stringify(kids.map(cls)));
  }
}

// --- тип, ранг и состояние стоят своими ячейками ---
{
  if (!dump(kids[3]).includes("Задача")) {
    fail("тип записи не встал своей колонкой словом словаря: " + dump(kids[3]));
  }
  if (!dump(kids[4]).includes("средний")) {
    fail("ранг записи не встал своей колонкой словом: " + dump(kids[4]));
  }
  if (!dump(kids[6]).includes("ждёт разбора")) {
    fail("состояние записи не названо по факту: " + dump(kids[6]));
  }
}

// --- цена и источник у записи молчат пустыми ячейками ---
// Колонки общие на обе секции, и у самой записи цены нет до заведения строки,
// а источника нет вовсе: пустая ячейка держит геометрию, а не пропадает.
{
  if (String(dump(kids[5])).trim()) fail("у записи появилась цена до заведения: " + dump(kids[5]));
  if (String(dump(kids[7])).trim()) fail("у записи появился источник: " + dump(kids[7]));
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
  // без неё правка ширины до строки не доедет. Растяжимой колонке места
  // переменной не нужно.
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
  // Дверь в исходный черновик ужимается в своей колонке: без кромки длинная
  // ссылка источника растягивала строку согласования.
  const src = /\.dsrow \.dsrc \.linkish\{([^}]*)\}/.exec(css);
  if (!src || !src[1].includes("text-overflow:ellipsis")) {
    fail("ссылка источника не ужимается многоточием в своей колонке");
  }
}

console.log("poc_drow: ok");

for (const [groomed, pending, want] of [[true,0,"разобран"],[true,1,"ждёт разбора"],[false,0,"ждёт разбора"]]) {
  const state = sandbox.draftRowState({...draft,groomed,ideas_undecided:pending},[]);
  if (state !== want) fail("состояние записи: " + state + ", жду " + want);
}
