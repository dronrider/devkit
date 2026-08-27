// Замер колонки действий настоящим движком (POC DK-397, ветка poc-chat).
//
// «Логика главной кнопки непонятна» (замечание пользователя): в одной секции у
// одних строк первой стояла кнопка чата, у других запуск, и решала это запись
// работы за строкой, которой на строке не видно. Кнопки развели по местам:
// слева работа, справа разговор, дальше три точки, и три точки бывают не у
// всякой строки.
//
// Разбором стилей это не берётся: место кнопки складывается из ширины колонки,
// боковых отступов ячейки, зазоров ряда и того, куда ряд жмётся. Меряется тут
// одно число: на сколько разъехались по горизонтали кнопки работы у строки с
// тремя точками и у строки без них. Ноль значит, что колонка кнопок стоит
// столбиком и палец с глазом всегда находят её на одном месте.
//
// Разметку скрипт кладёт руками той же вёрсткой, какой её собирает app.js.
// Ширины колонок приезжают из TBL_COLS отдельным скриптом (window.TBLFIT):
// копия чисел в стенде разошлась бы с кодом молча.

const FIT = window.TBLFIT || {};

const cols = (kind) => `<colgroup>` + (FIT[kind] || []).map((c) =>
  c.flex ? `<col>` : `<col style="width:${c.w}px">`).join("") + `</colgroup>`;

const head = (kind) => `<thead><tr class="tblh h-${kind}">` + (FIT[kind] || []).map((c, at) =>
  `<th class="tblc" scope="col">` +
  (c.label ? `<button class="tblb" type="button"><span class="tbll">${c.label}</span></button>`
    : `<span class="tbln"></span>`) +
  (at + 1 < (FIT[kind] || []).length ? `<span class="tblg"></span>` : "") + `</th>`)
  .join("") + `</tr></thead>`;

const ICO = (cls) => `<button class="btn btn-sm btn-ico ${cls}"><svg viewBox="0 0 24 24"></svg></button>`;

// Две строки одной секции: у первой за строкой есть разговор, и трёх точек ей
// не нужно; у второй разговора нет, и под тремя точками лежит выбор запуска.
const row = (id, dots) => `
    <tr class="trow">
      <td class="id"><span>${id}</span></td>
      <td class="tt"><span class="cin"><span class="ttl">строка доски ${id}</span></span></td>
      <td class="rank"><button class="rsum" type="button">40</button></td>
      <td class="twhen"><span class="stale dashed">2026-08-26</span></td>
      <td class="meta"><span class="cin"><span class="racts">${ICO("rmain")}${ICO("")}${
  dots ? ICO("rdots") : ""}</span></span></td>
    </tr>`;

document.getElementById("groups").innerHTML =
  `<table class="tbl t-tasks">${cols("tasks")}${head("tasks")}
  <tbody class="tsec">${row("DK-1", false)}${row("DK-2", true)}</tbody></table>`;
document.getElementById("pname").textContent = "devkit";
const sel = document.getElementById("pselect");
if (sel) sel.innerHTML = "<option>devkit</option>";

const rows = [...document.querySelectorAll("tr.trow")];
const workX = rows.map((tr) => Math.round(
  tr.querySelector(".rmain").getBoundingClientRect().left));
const cell = rows.map((tr) => Math.round(tr.querySelector(".meta").getBoundingClientRect().width));
const btns = rows.map((tr) => tr.querySelectorAll(".racts .btn").length);
// Влезает ли ряд кнопок в свою колонку: правый край последней кнопки против
// правого края ячейки за вычетом её отступа.
const spill = rows.map((tr) => {
  const box = tr.querySelector(".meta").getBoundingClientRect();
  const pad = parseFloat(getComputedStyle(tr.querySelector(".meta")).paddingRight);
  const list = [...tr.querySelectorAll(".racts .btn")];
  const last = list[list.length - 1].getBoundingClientRect();
  return Math.round(last.right - (box.right - pad));
});

document.title = [
  "screen=" + document.documentElement.clientWidth,
  "rows=" + rows.length,
  "btns0=" + btns[0],
  "btns1=" + btns[1],
  // Разъезд кнопки работы по горизонтали между строками разного состава.
  "workoff=" + Math.abs(workX[0] - workX[1]),
  "cellw=" + Math.min(...cell),
  // Насколько ряд кнопок вылез за своё место: больше нуля это узкая колонка.
  "spill=" + Math.max(...spill),
].join(" ");
