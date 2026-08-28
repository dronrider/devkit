// Замер кнопок в хвосте строки настоящим движком (POC DK-397, ветка poc-chat).
//
// «Сейчас кнопки в задаче, сессии, черновике на мобилке разного размера и
// расположения, да и компоновка похоже отличается» (замечание пользователя).
// Прежний заход свёл величины на широком экране, а телефон остался врозь:
// строка доски держала кнопку в 36 точек, запись накопителя в 30, а сессия
// растягивала свои во всю ширину и уносила их отдельной полосой под строку.
//
// Разбором стилей это не берётся: размер кнопки складывается из правил кнопки,
// раскладки строки и того, куда жмётся её хвост, а раскладка на телефоне своя
// у каждого раздела. Меряет тут браузер и меряет тем же, чем видит человек:
// размером кнопки, зазором между ними, отступом колонки и местом хвоста в
// строке.
//
// Разметку скрипт кладёт руками той же вёрсткой, какой её собирает app.js, а
// ширины колонок приезжают из TBL_COLS отдельным скриптом (window.TBLFIT):
// копия чисел в стенде разошлась бы с кодом молча.
//
// Ответ уходит в заголовок окна, откуда его читает go-тест.

const FIT = window.TBLFIT || {};
const kind = new URLSearchParams(location.search).get("bar") || "tasks";
const cols = FIT[kind] || [];

const colgroup = () => `<colgroup>` + cols.map((c) =>
  c.flex ? `<col>` : `<col style="width:${c.w}px">`).join("") + `</colgroup>`;

const head = () => `<thead><tr class="tblh h-${kind}">` + cols.map((c, at) =>
  `<th class="tblc" scope="col">` +
  (c.label ? `<button class="tblb" type="button"><span class="tbll">${c.label}</span></button>`
    : `<span class="tbln"></span>`) +
  (at + 1 < cols.length ? `<span class="tblg"></span>` : "") + `</th>`).join("") + `</tr></thead>`;

const ICO = (cls) => `<button class="btn btn-sm btn-ico ${cls}" type="button">` +
  `<svg viewBox="0 0 24 24"></svg></button>`;

// Строки трёх разделов ровно того состава, какой они держат на экране: у
// задачи работа, разговор и три точки, у сессии разговор и снятие, у записи
// накопителя один разговор.
const ROWS = {
  tasks: `<tr class="trow">
      <td class="id"><span class="sdot sd-wait"></span><span>DK-517</span></td>
      <td class="tt"><span class="cin"><span class="ttl">Команды доски зовутся голой командой без обвязки</span></span></td>
      <td class="rank"><button class="rsum" type="button">100</button></td>
      <td class="twhen"><span class="stale dashed">2026-08-20</span></td>
      <td class="meta"><span class="cin"><span class="racts">${
    ICO("btn-acc rmain")}${ICO("rchat")}${ICO("rdots")}</span></span></td>
    </tr>`,
  sess: `<tr class="arow atalk">
      <td class="live"><span class="dot pulse"></span></td>
      <td class="ab"><div class="l1"><span class="tt">Груминг задачи DK-452 на доске проекта</span></div>
        <div class="l2">DK-452, разговор</div></td>
      <td class="amoved"><span class="stale dashed">2026-08-20</span></td>
      <td class="aacts"><span class="cin"><span class="racts">${
    ICO("rchat")}${ICO("rstop btn-danger")}</span></span></td>
    </tr>`,
  drafts: `<tr class="dsrow clicky">
      <td class="dimp"><span class="cin"><button class="dpick" type="button"><span class="dbox"></span></button>
        <span class="chip">средний</span></span></td>
      <td class="id">DK-518</td>
      <td class="dtt"><span class="cin"><span class="st">Расход подписки виден пачкой, а не одной задачей</span></span></td>
      <td class="dwhen"><span class="stale dashed">2026-08-20</span></td>
      <td class="sm"><span class="cin"><span class="racts">${ICO("rchat")}</span></span></td>
    </tr>`,
};

document.getElementById("groups").innerHTML =
  `<table class="tbl t-${kind}">${colgroup()}${head()}<tbody class="tsec">${ROWS[kind]}</tbody></table>`;
document.getElementById("pname").textContent = "devkit";
const sel = document.getElementById("pselect");
if (sel) sel.innerHTML = "<option>devkit</option>";

const row = document.querySelector("tbody.tsec>tr");
const acts = row.querySelector(".racts");
const cell = acts.closest("td");
const cs = getComputedStyle(cell);
const gaps = getComputedStyle(acts);
const btns = [...acts.querySelectorAll(".btn")];
const rb = row.getBoundingClientRect();
const ab = acts.getBoundingClientRect();
const boxes = btns.map((b) => b.getBoundingClientRect());
const round = (n) => Math.round(n);

// Зазор берётся у самой коробки хвоста, а не расстоянием между кнопками:
// раздел с одной кнопкой иначе нечем было бы сверить с разделом о трёх.
const gap = parseFloat(gaps.columnGap || gaps.gap || "0") || 0;

document.title = [
  "screen=" + document.documentElement.clientWidth,
  "btns=" + btns.length,
  // Размер кнопки: наименьший и наибольший по ряду. Разошлись они внутри
  // одного ряда, значит кнопки разного размера уже в одной строке.
  "btnwmin=" + round(Math.min(...boxes.map((b) => b.width))),
  "btnwmax=" + round(Math.max(...boxes.map((b) => b.width))),
  "btnhmin=" + round(Math.min(...boxes.map((b) => b.height))),
  "btnhmax=" + round(Math.max(...boxes.map((b) => b.height))),
  "gap=" + round(gap),
  "padl=" + round(parseFloat(cs.paddingLeft)),
  "padr=" + round(parseFloat(cs.paddingRight)),
  // Место хвоста в строке: отступ его правого края от правого края строки и
  // высота от верха строки. Хвост, уехавший отдельной полосой под строку,
  // виден по второму числу, прижатый не туда по первому.
  "tail=" + round(rb.right - ab.right),
  "top=" + round(ab.top - rb.top),
  "rowh=" + round(rb.height),
  "roww=" + round(rb.width),
  "actw=" + round(ab.width),
].join(" ");
