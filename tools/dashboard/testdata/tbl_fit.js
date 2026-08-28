// Замер того, влезает ли в колонку её собственное содержимое (POC DK-397,
// ветка poc-chat). Длинное название задачи режется многоточием, и это верно:
// названию колонки не хватит никогда. А короткая метка (чип уровня, номер,
// дата, подпись кнопки, слово в шапке) режется только от нехватки ширины, и
// «средн» вместо «средний» это дефект, а не кромка.
//
// Разбором стилей такое не берётся: ширину слова считает шрифт, а место под
// него складывается из ширины колонки, боковых отступов ячейки и зазоров
// внутри ряда. Меряет всё это браузер, и меряет так же, как видит человек:
// написанное шире того места, где оно стоит.
//
// Ширины колонок стенд не выдумывает, их кладёт в window.TBLFIT go-тест,
// вычитав TBL_COLS прямо из static/app.js: копия чисел в стенде разошлась бы
// с экраном, и замер сторожил бы вымысел.
//
// Ответ уходит в заголовок окна, откуда его читает go-тест.

const FIT = window.TBLFIT || {};
const kind = new URLSearchParams(location.search).get("bar") || "tasks";
const cols = FIT[kind] || [];

const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;");

// Значок направления сортировки стоит у всякой подписи нарочно: на экране он
// висит у одной колонки, но встать может у любой, и мерить надо тот случай,
// когда подписи тесно.
// Подпись колонки бывает словом и бывает значком: колонка хода носит в шапке
// значок, и место под него занимает такое же, как слово, значит и мерить его
// надо тем же замером.
const headLabel = (c) => c.ico
  ? `<span class="tblico" data-fit="${c.key}"><svg viewBox="0 0 24 24"></svg></span>`
  : `<span class="tbll" data-fit="${c.key}">${esc(c.label)}</span>`;

const head = () => `<thead><tr class="tblh h-${kind}">` + cols.map((c, at) =>
  `<th class="tblc" scope="col">` +
  (c.label
    ? `<button class="tblb" type="button">` + headLabel(c) +
      (c.first ? `<svg viewBox="0 0 24 24"><path d="M6 9l6 6 6-6"></path></svg>` : "") +
      `</button>`
    : `<span class="tbln"></span>`) +
  (at + 1 < cols.length ? `<span class="tblg"></span>` : "") + `</th>`).join("") + `</tr></thead>`;

const colgroup = () => `<colgroup>` + cols.map((c) =>
  c.flex ? `<col>` : `<col style="width:${c.w}px">`).join("") + `</colgroup>`;

// Начинка ячейки берётся худшим случаем, какой строка правда показывает: у
// возраста сессии это «меньше минуты», у ранга сотня, у подписи кнопки самое
// длинное слово действия, у уровня разбора самое длинное слово чипа.
const CELLS = {
  tasks: {
    id: `<td class="id"><span class="sdot sd-wait"></span><span data-fit="id">DK-517</span></td>`,
    title: `<td class="tt"><span class="cin"><span class="ttl">Команды доски зовутся голой командой без обвязки</span>` +
      `<span class="rchips"><span class="chip">M</span></span></span></td>`,
    rank: `<td class="rank"><button class="rsum" type="button" data-fit="rank">100</button></td>`,
    date: `<td class="twhen"><span class="stale dashed" data-fit="date">2026-08-20</span></td>`,
    // Хвост строки это две кнопки значками: работа и разговор. Слов на них нет
    // вовсе, и мерить тут надо не обрубок подписи, а то, влезает ли сам ряд:
    // колонка стоит ровно по нему.
    act: `<td class="meta"><span class="cin"><span class="racts" data-fit="act">` +
      `<button class="btn btn-sm btn-ico rmain"><svg viewBox="0 0 24 24"></svg></button>` +
      `<button class="btn btn-sm btn-ico rchat"><svg viewBox="0 0 24 24"></svg></button>` +
      `</span></span></td>`,
  },
  sess: {
    live: `<td class="live"><span class="dot pulse" data-fit="live"></span></td>`,
    title: `<td class="ab"><div class="l1"><span class="tt">Груминг задачи DK-452 на доске проекта</span>` +
      `<span class="rchips"><span class="chip c-run">идёт</span></span></div>` +
      `<div class="l2">DK-452, разговор</div></td>`,
    moved: `<td class="amoved"><span class="stale dashed" data-fit="moved">2026-08-22</span></td>`,
    act: `<td class="aacts"><span class="cin"><span class="racts" data-fit="act">` +
      `<button class="btn btn-sm btn-ico rchat"><svg viewBox="0 0 24 24"></svg></button>` +
      `<button class="btn btn-sm btn-danger btn-ico sclose"><svg viewBox="0 0 24 24">` +
      `</svg></button></span></span></td>`,
  },
  drafts: {
    prio: `<td class="dimp"><span class="cin"><button class="dpick"><span class="dbox"></span></button>` +
      `<span class="chip" data-fit="prio" data-alt="средний|низкий">высокий</span></span></td>`,
    id: `<td class="id"><span data-fit="id">DK-410</span></td>`,
    title: `<td class="dtt"><span class="cin"><span class="st">Линт не видит файл задачи, заведённой руками</span>` +
      `<span class="rchips"><span class="chip">отложен 20 авг</span></span></span></td>`,
    date: `<td class="dwhen"><span class="stale dashed" data-fit="date">2026-08-17</span></td>`,
    act: `<td class="sm"><span class="cin"><span class="racts" data-fit="act">` +
      `<button class="btn btn-sm btn-ico rchat"><svg viewBox="0 0 24 24"></svg></button>` +
      `</span></span></td>`,
  },
};

const ROW = { tasks: "trow", sess: "arow", drafts: "dsrow" }[kind] || "trow";
const body = `<tbody><tr class="${ROW}">` +
  cols.map((c) => (CELLS[kind] || {})[c.key] || `<td></td>`).join("") + `</tr></tbody>`;

document.getElementById("groups").innerHTML =
  `<table class="tbl t-${kind}">${colgroup()}${head()}${body}</table>`;
document.getElementById("pname").textContent = "devkit";
const sel = document.getElementById("pselect");
if (sel) sel.innerHTML = "<option>devkit</option>";

// Насколько написанное шире того места, где оно стоит. Узел с обрезкой отдаёт
// это разностью scrollWidth и clientWidth; узел без обрезки просто вылезает за
// ячейку, и ту же разность отдаёт сама ячейка. Меряется и то, и другое, потому
// что от колонки к колонке обрезка стоит по-разному, а глазу человека разницы
// между обрубком и вылезшим словом нет.
const over = (node) => {
  if (!node) return 0;
  let cut = Math.max(0, Math.round(node.scrollWidth - node.clientWidth));
  // Слово в ячейке приходит не одно: возраст сессии бывает и «меньше минуты»,
  // и «123 ч 45 мин», а уровень разбора трёх слов сразу. Колонка обязана
  // вмещать самое длинное из них, поэтому меряются все, а в ответ идёт худшее.
  const alt = node.getAttribute && node.getAttribute("data-alt");
  if (alt) {
    const was = node.textContent;
    for (const word of alt.split("|")) {
      node.textContent = word;
      cut = Math.max(cut, Math.max(0, Math.round(node.scrollWidth - node.clientWidth)));
    }
    node.textContent = was;
  }
  return cut;
};

// Самая узкая ширина, при которой колонка ещё ничего не режет: ни начинку
// строки, ни собственную подпись в шапке. Считается не формулой, а сужением
// настоящей колонки до первого обрубка: место под метку складывается из
// отступов ячейки, зазоров ряда и ширины слова, которую знает только шрифт.
// Разность между этим числом и шириной из TBL_COLS и есть тот запас, который
// колонка держит впустую, отнимая место у названия работы.
const colNodes = [...document.querySelectorAll("colgroup col")];

// Перелив ряда за кромку ячейки. Обрубок многоточием ловится разностью
// scrollWidth и clientWidth, но так ловится не всё: кнопки в хвосте строки
// стоят flex:none внутри жмущегося ряда, и при нехватке места они не режутся, а
// вылезают за ячейку, оставляя обе ширины ряда равными. Поэтому меряется ещё и
// геометрия: где кончается написанное и где кончается место под него.
// Открепившиеся узлы (кружок состояния в отступе, ручка тяги на границе)
// считать нечем, они и стоят снаружи по замыслу.
const spill = (box) => {
  const cs = getComputedStyle(box);
  const rect = box.getBoundingClientRect();
  const left = rect.left + parseFloat(cs.paddingLeft);
  const right = rect.right - parseFloat(cs.paddingRight);
  let out = 0;
  for (const node of box.querySelectorAll("*")) {
    if (getComputedStyle(node).position === "absolute") continue;
    const one = node.getBoundingClientRect();
    if (!one.width) continue;
    out = Math.max(out, Math.round(one.right - right), Math.round(left - one.left));
  }
  return Math.max(0, out);
};

// Что держит ширину колонки: данные в строке или подпись в шапке. Вопрос не
// праздный: у колонки хода ширину держало слово, и колонка под кружок в девять
// точек занимала восемьдесят. Меряется порознь, сужением до первого обрубка
// сперва по одной строке, потом по одной шапке; наружу идут оба числа, и
// большее из них и есть тот, кто держит.
const minWidth = (c, at, cell, only) => {
  const node = colNodes[at];
  if (!node) return 0;
  const was = node.style.width;
  const label = document.querySelector('.tblh [data-fit="' + c.key + '"]');
  const cell2 = label ? label.closest("th") : null;
  const body = () => {
    let bad = Math.max(over(cell), spill(cell));
    for (const n of cell.querySelectorAll("[data-fit]")) bad = Math.max(bad, over(n));
    return bad;
  };
  const headCut = () => Math.max(over(label), cell2 ? spill(cell2) : 0);
  const cut = () => {
    if (only === "body") return body();
    if (only === "head") return headCut();
    return Math.max(body(), headCut());
  };
  let px = c.w;
  while (px > 24) {
    node.style.width = (px - 1) + "px";
    if (cut() > 1) break;
    px -= 1;
  }
  node.style.width = was;
  return px;
};

const out = ["screen=" + document.documentElement.clientWidth, "cols=" + cols.length];
const row = document.querySelector("." + ROW);
const cells = [...row.children];
cols.forEach((c, at) => {
  // Растяжимая колонка держит название задачи, и многоточие на нём это верное
  // поведение, а не дефект: мерить там нечего.
  if (c.flex) return;
  const cell = cells[at];
  let cut = over(cell);
  for (const node of cell.querySelectorAll("[data-fit]")) cut = Math.max(cut, over(node));
  out.push("cut_" + c.key + "=" + cut);
  // Подпись в шапке это такая же короткая метка: колонка, которой не хватает
  // ширины на собственное имя со значком порядка, режет его тем же обрубком.
  const label = document.querySelector('.tblh [data-fit="' + c.key + '"]');
  out.push("head_" + c.key + "=" + over(label));
  out.push("w_" + c.key + "=" + Math.round(cell.getBoundingClientRect().width));
  out.push("min_" + c.key + "=" + minWidth(c, at, cell));
  out.push("body_" + c.key + "=" + minWidth(c, at, cell, "body"));
  out.push("head_min_" + c.key + "=" + minWidth(c, at, cell, "head"));
});
// --- вид строки: величины, которые обязаны совпадать у трёх разделов ---
// Разделы собирались разными заходами и разошлись по виду: размеры значков,
// высота строки, отступы ячеек, кегль подписей (замечание пользователя «стиль
// отображения контента разный на всех табах»). Разбором стилей такое не
// берётся: величина складывается из правила раздела, общего правила таблицы и
// медиазапроса раздела, поэтому меряется готовая раскладка.
const cs = (node, prop) => {
  if (!node) return 0;
  const said = getComputedStyle(node).getPropertyValue(prop);
  return Math.round(parseFloat(said) || 0);
};
const box = (node) => (node ? Math.round(node.getBoundingClientRect().width) : 0);
const high = (node) => (node ? Math.round(node.getBoundingClientRect().height) : 0);
const cellsAll = [...row.children];
const lastCell = cellsAll[cellsAll.length - 1];
const look = {
  rowh: high(row),
  padl: cs(cellsAll[0], "padding-left"),
  // Боковой отступ внутренней ячейки: у крайних свои поля, держащие кромку
  // карточки, а этот отступ общий и стоит между всякими двумя колонками.
  padin: cs(cellsAll[1], "padding-left"),
  padinr: cs(cellsAll[1], "padding-right"),
  padr: cs(lastCell, "padding-right"),
  padt: cs(lastCell, "padding-top"),
  padb: cs(lastCell, "padding-bottom"),
  align: { top: 1, middle: 2, baseline: 3, bottom: 4 }[
    getComputedStyle(cellsAll[0]).verticalAlign] || 0,
  actgap: cs(lastCell.querySelector(".cin") || lastCell, "gap"),
  // Самая высокая начинка ячейки в строке: ряд кнопок в хвосте, двухстрочная
  // подпись работы у сессий, заголовок с чипами. Вертикальный отступ ячейки
  // ужимали до пяти точек, и стенду надо знать, во что этот отступ упирается:
  // строка ниже своей начинки читается тесной, а начинку она обязана вместить
  // при любом отступе.
  // Складываются высоты прямых детей, а не берётся высота самой ячейки: ячейка
  // тянется ростом со строку всегда, и сверять строку с ней значило бы сверять
  // число с самим собой. Открепившийся кружок пропускается, он висит в отступе
  // и своей высоты строке не добавляет.
  fill: Math.max(...cellsAll.map((one) => [...one.children]
    .filter((kid) => getComputedStyle(kid).position !== "absolute")
    .reduce((was, kid) => was + high(kid), 0))),
};
// Кнопка-значок в хвосте строки: её величина, величина самого значка и рамка.
const ico = lastCell.querySelector(".btn-ico") || lastCell.querySelector("button");
look.ico = box(ico);
look.icoh = high(ico);
look.icosvg = box(ico && ico.querySelector("svg"));
look.icorad = cs(ico, "border-radius");
// Заголовок строки, вспомогательный текст и чип: у каждого раздела свой узел,
// но кегль у них общий по смыслу («это заголовок», «это подпись»).
const TTL = { tasks: ".ttl", sess: ".l1 .tt", drafts: ".st" }[kind];
const ttl = document.querySelector("." + ROW + " " + TTL);
look.ttlfs = cs(ttl, "font-size");
look.ttlw = Math.round(parseFloat(getComputedStyle(ttl).fontWeight) || 0);
const sub = document.querySelector("." + ROW + " .stale");
look.subfs = cs(sub, "font-size");
const chip = document.querySelector("." + ROW + " .chip");
look.chiph = high(chip);
look.chipfs = cs(chip, "font-size");
look.chipgap = cs(document.querySelector("." + ROW + " .rchips"), "gap");
for (const key of Object.keys(look)) out.push("l_" + key + "=" + look[key]);

document.title = out.join(" ");
