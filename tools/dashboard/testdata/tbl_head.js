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
const SESS_COLS = [{ label: "Ход", w: 76 }, { label: "Работа" }, { label: "Идёт", w: 104 },
  { label: "Активность", w: 108 }, { label: "", w: 110 }];
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
      <td class="atime">3 ч 40 мин</td>
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
  offin = Math.max(offin, Math.round(Math.abs(inner(cells[at]) - inner(kids[at]))));
}

const out = [
  "screen=" + document.documentElement.clientWidth,
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
