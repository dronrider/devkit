// Замер воздуха вокруг таблицы раздела (POC DK-397, ветка poc-chat).
//
// Пользователь: «вертикальные отступы между главным меню и таблицей и таблицей
// и чатом нужно сократить вдвое». Оба зазора это не одно правило, а сумма:
// сверху низ полосы табов, её черта и верх карточки списка; справа поле
// страницы между карточкой и панелью разговора. Разбором стилей такую сумму не
// возьмёшь, поэтому меряет движок, и меряет на настоящей разметке index.html:
// стенд подменяет собой только app.js, а каскад вокруг тот же, что у человека.
//
// Ответ уходит в заголовок окна, откуда его читает go-тест.

const kind = new URLSearchParams(location.search).get("bar") || "tasks";
const KINDS = [["tasks", "Задачи"], ["sess", "Сессии"], ["drafts", "Черновики"]];

// Полоса табов лежит первым узлом внутри списка разделов, ровно так её кладёт
// boardKindBar в app.js: копия разметки тут нужна затем, чтобы зазор мерился
// между теми же двумя коробками, между которыми его видит человек.
const tabs = KINDS.map(([key, label]) =>
  `<button class="ktab${key === kind ? " onktab" : ""}" type="button">${label}` +
  `<span class="n">12</span></button>`).join("");

const ROW = { tasks: "trow", sess: "arow", drafts: "dsrow" }[kind] || "trow";
const table = `<table class="tbl t-${kind}">` +
  `<thead><tr class="tblh h-${kind}">` +
  `<th class="tblc"><button class="tblb" type="button"><span class="tbll">Номер</span></button></th>` +
  `<th class="tblc"><button class="tblb" type="button"><span class="tbll">Задача</span></button></th>` +
  `</tr></thead><tbody><tr class="${ROW}">` +
  `<td class="id"><span class="sdot sd-wait"></span><span>DK-517</span></td>` +
  `<td class="tt"><span class="cin"><span class="ttl">Строка списка</span></span></td>` +
  `</tr></tbody></table>`;

const groups = document.getElementById("groups");
groups.innerHTML = `<div class="ktabs">${tabs}</div>${table}`;
document.getElementById("pname").textContent = "devkit";
const sel = document.getElementById("pselect");
if (sel) sel.innerHTML = "<option>devkit</option>";
// Панель разговора стоит в разметке всегда, а показана бывает по хвосту
// адреса: без неё правого зазора нет вовсе, и мерить было бы нечего.
const panel = document.getElementById("cpanel");
if (panel) panel.removeAttribute("hidden");

const box = (node) => (node ? node.getBoundingClientRect() : null);
const cs = (node, prop) => {
  if (!node) return 0;
  return Math.round(parseFloat(getComputedStyle(node).getPropertyValue(prop)) || 0);
};

const bar = document.querySelector(".ktabs");
const tbl = document.querySelector(".tbl");
const head = document.querySelector(".bhead");
const main = document.querySelector(".bmain");

const out = [
  "screen=" + document.documentElement.clientWidth,
  // Воздух сверху: от нижней кромки полосы табов (черта входит в неё) до
  // верхней кромки карточки списка.
  "airtop=" + Math.round(box(tbl).top - box(bar).bottom),
  // Воздух справа: от правой кромки карточки до кромки панели разговора.
  "airright=" + Math.round(box(panel).left - box(tbl).right),
  // Слагаемые верхнего зазора: нижнее поле таба, черта полосы и верхнее поле
  // самой карточки. По ним видно, что именно ужимать.
  "tabpb=" + cs(document.querySelector(".ktab"), "padding-bottom"),
  "barbb=" + cs(bar, "border-bottom-width"),
  "barmb=" + cs(bar, "margin-bottom"),
  "tblmt=" + cs(tbl, "margin-top"),
  "groupspt=" + cs(groups, "padding-top"),
  // Слагаемые правого зазора: боковое поле страницы и рамка панели.
  "mainpr=" + cs(main, "padding-right"),
  "panbl=" + cs(panel, "border-left-width"),
  // Воздух над самой полосой табов: от шапки экрана до неё. Пользователь про
  // него не говорил, но стоит он рядом и одной лестницей с прочими.
  "barmt=" + cs(bar, "margin-top"),
  "headgap=" + Math.round(box(bar).top - box(head).bottom),
  // Зазор внутри ряда ячейки: нижняя граница разумности для воздуха вокруг
  // таблицы. Меньше него соседи по экрану читаются слипшимися.
  "rowgap=" + Math.round(parseFloat(
    getComputedStyle(document.documentElement).getPropertyValue("--rowgap")) || 0),
].join(" ");
document.title = out;
