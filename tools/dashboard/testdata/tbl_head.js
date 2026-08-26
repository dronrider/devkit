// Замер геометрии шапки колонок настоящим движком (POC DK-397, ветка
// poc-chat). Разбор правил стилей тут ничего не доказывает: высота шапки
// складывается из отступов, роста подписи и рамки, а место колонки из сетки,
// зазоров и отступов карточки. Меряет всё это браузер.
//
// Три вопроса, и все три поставил пользователь, забраковав прежний вид:
//   шапка ростом со строку, а не в полтора-два раза выше;
//   отступы у неё симметричные, а не «сверху 12, снизу 8»;
//   подпись стоит ровно над своей ячейкой во всех трёх разделах.
//
// Разметку скрипт кладёт руками той же вёрсткой, какой её собирает app.js:
// браузеру нужен живой DOM, а поднимать ради замера весь дашборд незачем.
// Ответ уходит в заголовок окна, откуда его читает go-тест.

const head = (kind, cols) => `<div class="tblh h-${kind}">` + cols.map((c, at) =>
  `<div class="tblc">` +
  (c ? `<button class="tblb" type="button"><span class="tbll">${c}</span></button>`
    : `<span class="tbln"></span>`) +
  (at + 1 < cols.length ? `<span class="tblg"></span>` : "") + `</div>`).join("") + `</div>`;

const TASKS = head("tasks", ["Номер", "Задача", "Ранг", "Дата", ""]) + `
  <div class="shead bsec onsec">Backlog<span class="n">1</span></div>
  <div class="card bsec onsec">
    <div class="trow">
      <span class="id"><span>DK-517</span></span>
      <span class="tt"><span class="ttl">Команды доски зовутся голой командой</span></span>
      <span class="rank"><button class="rsum" type="button">62</button></span>
      <span class="twhen"><span class="stale dashed">2026-08-20</span></span>
      <span class="meta"><button class="btn btn-sm btn-acc">Выполнить</button></span>
    </div>
  </div>`;

const SESS = `
  <div class="card">
    ${head("sess", ["Состояние", "Работа", "Идёт", "Активность", ""])}
    <div class="arow atalk">
      <span class="dot pulse"></span>
      <div class="ab">
        <div class="l1"><span class="tt">Груминг задачи DK-452 на доске</span></div>
        <div class="l2">DK-452, разговор</div>
      </div>
      <span class="atime">3 ч 40 мин</span>
      <div class="amoved"><span class="stale dashed">2026-08-22</span></div>
      <div class="aacts"><button class="btn btn-sm btn-ico">i</button></div>
    </div>
  </div>`;

const DRAFTS = `
  <div class="card">
    ${head("drafts", ["Приоритет", "Номер", "Задача", "Дата", ""])}
    <div class="srow clicky dsrow">
      <span class="dimp"><button class="dpick"><span class="dbox"></span></button>
        <span class="chip">средний</span></span>
      <span class="id">DK-410</span>
      <span class="dtt"><span class="st">Линт не видит файл задачи</span></span>
      <span class="dwhen"><span class="stale dashed">2026-08-17</span></span>
      <span class="sm"><button class="btn btn-sm btn-ico">i</button></span>
    </div>
  </div>`;

const kind = new URLSearchParams(location.search).get("bar") || "tasks";
const body = { tasks: TASKS, sess: SESS, drafts: DRAFTS }[kind] || TASKS;
document.getElementById("groups").innerHTML = body;
document.getElementById("pname").textContent = "devkit";
const sel = document.getElementById("pselect");
if (sel) sel.innerHTML = "<option>devkit</option>";

const ROW = { tasks: "trow", sess: "arow", drafts: "dsrow" }[kind];
const headNode = document.querySelector(".tblh");
const rowNode = document.querySelector("." + ROW);
const css = getComputedStyle(headNode);

// Насколько подпись колонки разошлась со своей ячейкой в строке. Меряется по
// левой кромке: правая у ячейки с многоточием живёт своей жизнью, а слева
// колонка либо стоит на месте, либо нет.
let off = 0;
const cells = [...headNode.children];
const kids = [...rowNode.children];
for (let at = 0; at < Math.min(cells.length, kids.length); at++) {
  const said = Math.abs(cells[at].getBoundingClientRect().left -
    kids[at].getBoundingClientRect().left);
  off = Math.max(off, Math.round(said));
}

const out = [
  "screen=" + document.documentElement.clientWidth,
  "cells=" + cells.length,
  "kids=" + kids.length,
  "headh=" + Math.round(headNode.getBoundingClientRect().height),
  "rowh=" + Math.round(rowNode.getBoundingClientRect().height),
  // Симметрия отступов: разность верхнего и нижнего в точках.
  "pad=" + Math.abs(Math.round(parseFloat(css.paddingTop) - parseFloat(css.paddingBottom))),
  "off=" + off,
  // Ручка тяги стоит у всякой колонки, кроме последней.
  "grips=" + headNode.querySelectorAll(".tblg").length,
  "gripw=" + Math.round((headNode.querySelector(".tblg") || { getBoundingClientRect:
    () => ({ width: 0 }) }).getBoundingClientRect().width),
].join(" ");
document.title = out;
