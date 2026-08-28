// Замер кружка состояния в строке списка (POC DK-397, ветка poc-chat).
//
// Две находки пользователя об одном кружке. Первая: «в разделе сессий кружок
// состояния хода стоит не по центру ячейки по вертикали» (строка после ужатия
// высоты стала ниже, и перекос стало видно). Вторая: на 390 точках кружок
// строки доски не виден вовсе, он вынесен за кромку строки и обрезается.
//
// Разбором стилей ни то, ни другое не берётся: кружок открепляется от потока,
// и где он окажется, считает движок из высоты ячейки, её полей и раскладки
// строки. Поэтому меряется готовая картинка: расстояние от кружка до верхней и
// нижней кромки ячейки, и попадание кружка в кромки самой строки.
//
// Ответ уходит в заголовок окна, откуда его читает go-тест.

const kind = new URLSearchParams(location.search).get("bar") || "tasks";

// Строка сессии двухстрочная нарочно: перекос кружка виден там, где ячейка
// выше одной строки текста, а у задач и накопителя строка одна.
const BODY = {
  tasks: `<table class="tbl t-tasks">
    <colgroup><col style="width:82px"><col></colgroup>
    <tbody><tr class="trow">
      <td class="id"><span class="sdot sd-wait"></span><span>DK-517</span></td>
      <td class="tt"><span class="cin"><span class="ttl">Команды доски зовутся голой командой без обвязки</span>
        <span class="rchips"><span class="chip">M</span></span></span></td>
    </tr></tbody></table>`,
  sess: `<table class="tbl t-sess">
    <colgroup><col style="width:40px"><col></colgroup>
    <tbody><tr class="arow atalk">
      <td class="live"><span class="dot pulse"></span></td>
      <td class="ab">
        <div class="l1"><span class="tt">Груминг задачи DK-452 на доске проекта</span>
          <span class="rchips"><span class="chip c-run">идёт</span></span></div>
        <div class="l2">DK-452, разговор</div>
      </td>
    </tr></tbody></table>`,
};

document.getElementById("groups").innerHTML = BODY[kind] || BODY.tasks;
document.getElementById("pname").textContent = "devkit";
const sel = document.getElementById("pselect");
if (sel) sel.innerHTML = "<option>devkit</option>";

const ROW = { tasks: "trow", sess: "arow" }[kind] || "trow";
const row = document.querySelector("." + ROW);
const dot = row.querySelector(".dot") || row.querySelector(".sdot");
const cell = dot.closest("td");
const box = (n) => n.getBoundingClientRect();
const groups = document.getElementById("groups");

const dotBox = box(dot);
const cellBox = box(cell);
const rowBox = box(row);
const groupsBox = box(groups);

const out = [
  "screen=" + document.documentElement.clientWidth,
  // Кружок по центру ячейки: сверху и снизу от него одно и то же расстояние.
  // В ответ идут оба, а не их разность: по разности не видно, куда съехало.
  "top=" + Math.round(dotBox.top - cellBox.top),
  "bot=" + Math.round(cellBox.bottom - dotBox.bottom),
  "cellh=" + Math.round(cellBox.height),
  "doth=" + Math.round(dotBox.height),
  "dotw=" + Math.round(dotBox.width),
  // Кружок целиком внутри строки и внутри списка: на 390 он вынесен за кромку
  // и режется, а невидимое состояние это то же, что состояние без признака.
  "outleft=" + Math.max(0, Math.round(rowBox.left - dotBox.left)),
  "outlist=" + Math.max(0, Math.round(groupsBox.left - dotBox.left)),
].join(" ");
document.title = out;
