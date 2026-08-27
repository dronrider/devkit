// Замер геометрии шапки колонок настоящим движком (POC DK-397, ветка
// poc-chat). Разбор правил стилей тут ничего не доказывает: высота шапки
// складывается из отступов, роста подписи и рамки, а место колонки из
// раскладки таблицы и отступов ячейки. Меряет всё это браузер.
//
// Три вопроса, и все три поставил пользователь, забраковав прежний вид:
//   шапка ростом со строку, а не в полтора-два раза выше;
//   отступы у неё симметричные, а не «сверху 12, снизу 8»;
//   подпись стоит ровно над своей ячейкой во всех трёх разделах.
//
// Расхождение меряется дважды: по кромке самой ячейки и по кромке того, что в
// ней написано. Первое в настоящей таблице сходится по устройству, колонка у
// подписи со строкой одна; второе сойдётся только если боковые отступы у th и
// td одни и те же, а это уже наша правка, и сторожить её надо.
//
// Разметку скрипт кладёт руками той же вёрсткой, какой её собирает app.js:
// браузеру нужен живой DOM, а поднимать ради замера весь дашборд незачем.
// Ответ уходит в заголовок окна, откуда его читает go-тест.

const cols = (kind, list) => `<colgroup>` + list.map((c) =>
  c.w ? `<col style="width:${c.w}px">` : `<col>`).join("") + `</colgroup>`;

const head = (kind, list) => `<thead><tr class="tblh h-${kind}">` + list.map((c, at) =>
  `<th class="tblc" scope="col">` +
  (c.label ? `<button class="tblb" type="button"><span class="tbll">${c.label}</span></button>`
    : `<span class="tbln"></span>`) +
  (at + 1 < list.length ? `<span class="tblg"></span>` : "") + `</th>`).join("") + `</tr></thead>`;

const TASK_COLS = [{ label: "Номер", w: 88 }, { label: "Задача" }, { label: "Ранг", w: 58 },
  { label: "Дата", w: 92 }, { label: "", w: 246 }];
// У колонки состояния подписи нет: она несёт кружок, и слово в шапке требовало
// под себя восемьдесят точек. Колонки возраста сессии тоже нет, её сняли: она
// показывала то же, что и активность рядом.
const SESS_COLS = [{ label: "", w: 36 }, { label: "Работа" },
  { label: "Активность", w: 124 }, { label: "", w: 92 }];
const DRAFT_COLS = [{ label: "Приоритет", w: 132 }, { label: "Номер", w: 70 },
  { label: "Задача" }, { label: "Дата", w: 92 }, { label: "", w: 38 }];

const TASKS = `<table class="tbl t-tasks">${cols("tasks", TASK_COLS)}${head("tasks", TASK_COLS)}
  <tbody class="tsec">
    <tr class="band secband"><td class="bcell" colspan="5">
      <div class="shead">Backlog<span class="n">1, по рангу</span></div></td></tr>
    <tr class="trow">
      <td class="id"><span class="sdot sd-wait"></span><span>DK-517</span></td>
      <td class="tt"><span class="cin"><span class="ttl">Команды доски зовутся голой командой</span>
        <span class="rchips"><span class="chip">M</span></span></span></td>
      <td class="rank"><button class="rsum" type="button">62</button></td>
      <td class="twhen"><span class="stale dashed">2026-08-20</span></td>
      <td class="meta"><span class="cin"><button class="btn btn-sm btn-ico">i</button>
        <button class="btn btn-sm btn-acc">Выполнить</button></span></td>
    </tr>
  </tbody></table>`;

const SESS = `<table class="tbl t-sess">${cols("sess", SESS_COLS)}${head("sess", SESS_COLS)}
  <tbody>
    <tr class="arow atalk">
      <td class="live"><span class="dot pulse"></span></td>
      <td class="ab">
        <div class="l1"><span class="tt">Груминг задачи DK-452 на доске</span></div>
        <div class="l2">DK-452, разговор</div>
      </td>
      <td class="amoved"><span class="stale dashed">2026-08-22</span></td>
      <td class="aacts"><span class="cin"><button class="btn btn-sm btn-ico">i</button></span></td>
    </tr>
  </tbody></table>`;

const DRAFTS = `<table class="tbl t-drafts">${cols("drafts", DRAFT_COLS)}${head("drafts", DRAFT_COLS)}
  <tbody>
    <tr class="dsrow clicky">
      <td class="dimp"><span class="cin"><button class="dpick"><span class="dbox"></span></button>
        <span class="chip">средний</span></span></td>
      <td class="id">DK-410</td>
      <td class="dtt"><span class="cin"><span class="st">Линт не видит файл задачи</span></span></td>
      <td class="dwhen"><span class="stale dashed">2026-08-17</span></td>
      <td class="sm"><span class="cin"><button class="btn btn-sm btn-ico">i</button></span></td>
    </tr>
  </tbody></table>`;

const kind = new URLSearchParams(location.search).get("bar") || "tasks";
const body = { tasks: TASKS, sess: SESS, drafts: DRAFTS }[kind] || TASKS;
document.getElementById("groups").innerHTML = body;
document.getElementById("pname").textContent = "devkit";
const sel = document.getElementById("pselect");
if (sel) sel.innerHTML = "<option>devkit</option>";

const ROW = { tasks: "trow", sess: "arow", drafts: "dsrow" }[kind];
const LIST = { tasks: TASK_COLS, sess: SESS_COLS, drafts: DRAFT_COLS }[kind] || TASK_COLS;
const headNode = document.querySelector(".tblh");
const rowNode = document.querySelector("." + ROW);
const css = getComputedStyle(headNode.children[0]);

// Насколько подпись колонки разошлась со своей ячейкой в строке. Меряется по
// левой кромке: правая у ячейки с многоточием живёт своей жизнью, а слева
// колонка либо стоит на месте, либо нет. Первое число про кромку ячейки,
// второе про кромку написанного в ней.
const leftOf = (node) => node.getBoundingClientRect().left;
// Кромка написанного это кромка ячейки плюс её левый отступ: отсюда начинается
// и подпись в шапке, и текст в строке, и разойтись они могут только отступами.
const inner = (cell) => leftOf(cell) + parseFloat(getComputedStyle(cell).paddingLeft);
let off = 0;
let offin = 0;
const cells = [...headNode.children];
const kids = [...rowNode.children];
for (let at = 0; at < Math.min(cells.length, kids.length); at++) {
  off = Math.max(off, Math.round(Math.abs(leftOf(cells[at]) - leftOf(kids[at]))));
  // Кромка написанного меряется там, где написано хоть что-то: у колонки без
  // подписи выравнивать нечего, а поле она держит своё, под кружок в отступе.
  if (!(LIST[at] || {}).label) continue;
  offin = Math.max(offin, Math.round(Math.abs(inner(cells[at]) - inner(kids[at]))));
}

// Боковой отступ подписи. Пользователь забраковал шапку словами «текст колонок
// стоит слишком близко к разделителю, не хватает отступа»: граница колонки
// рисуется ровно по кромке ячейки, и всё, что отделяет от неё подпись, это
// боковой отступ. Меряется он у внутренних колонок (у крайних свои поля,
// держащие кромку таблицы) и меряется у шапки со строкой порознь: разойдись
// они, подпись встала бы над своей колонкой, но не над своим текстом.
const side = (nodes) => {
  const out = { min: Infinity, sym: 0 };
  nodes.forEach((cell, at) => {
    if (at === 0 || at === nodes.length - 1) return;
    const cs = getComputedStyle(cell);
    const left = Math.round(parseFloat(cs.paddingLeft));
    const right = Math.round(parseFloat(cs.paddingRight));
    out.min = Math.min(out.min, left, right);
    out.sym = Math.max(out.sym, Math.abs(left - right));
  });
  if (!Number.isFinite(out.min)) out.min = 0;
  return out;
};
const sideHead = side(cells);
const sideRow = side(kids);

const out = [
  "screen=" + document.documentElement.clientWidth,
  // Боковой отступ подписи в шапке и содержимого в строке: числа обязаны
  // сойтись друг с другом, а внутри каждого сойтись слева с справа.
  "sideh=" + sideHead.min,
  "sidec=" + sideRow.min,
  "sidesym=" + Math.max(sideHead.sym, sideRow.sym),
  "cells=" + cells.length,
  "kids=" + kids.length,
  "headh=" + Math.round(headNode.getBoundingClientRect().height),
  "rowh=" + Math.round(rowNode.getBoundingClientRect().height),
  // Симметрия отступов ячейки шапки: разность верхнего и нижнего в точках.
  "pad=" + Math.abs(Math.round(parseFloat(css.paddingTop) - parseFloat(css.paddingBottom))),
  "off=" + off,
  "offin=" + offin,
  // Ручка тяги стоит у всякой колонки, кроме последней.
  "grips=" + headNode.querySelectorAll(".tblg").length,
  "gripw=" + Math.round((headNode.querySelector(".tblg") || { getBoundingClientRect:
    () => ({ width: 0 }) }).getBoundingClientRect().width),
].join(" ");
document.title = out;
