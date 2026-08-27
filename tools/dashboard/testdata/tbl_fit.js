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
const head = () => `<thead><tr class="tblh h-${kind}">` + cols.map((c, at) =>
  `<th class="tblc" scope="col">` +
  (c.label
    ? `<button class="tblb" type="button"><span class="tbll" data-fit="${c.key}">` +
      `${esc(c.label)}</span>` +
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
    // Хвост строки это три кнопки значками: работа, разговор и три точки. Слов
    // на них нет вовсе, и мерить тут надо не обрубок подписи, а то, влезает ли
    // сам ряд: колонка стоит ровно по нему.
    act: `<td class="meta"><span class="cin"><span class="racts" data-fit="act">` +
      `<button class="btn btn-sm btn-ico rmain"><svg viewBox="0 0 24 24"></svg></button>` +
      `<button class="btn btn-sm btn-ico"><svg viewBox="0 0 24 24"></svg></button>` +
      `<button class="btn btn-sm btn-ico rdots"><svg viewBox="0 0 24 24"></svg></button>` +
      `</span></span></td>`,
  },
  sess: {
    live: `<td class="live"><span class="dot pulse" data-fit="live"></span></td>`,
    title: `<td class="ab"><div class="l1"><span class="tt">Груминг задачи DK-452 на доске проекта</span></div>` +
      `<div class="l2">DK-452, разговор</div></td>`,
    age: `<td class="atime" data-fit="age" data-alt="123 ч 45 мин|59 мин">меньше минуты</td>`,
    moved: `<td class="amoved"><span class="stale dashed" data-fit="moved">2026-08-22</span></td>`,
    act: `<td class="aacts"><span class="cin"><button class="btn btn-sm btn-ico">` +
      `<svg viewBox="0 0 24 24"><path d="M6 9l6 6 6-6"></path></svg></button>` +
      `<button class="btn btn-sm btn-ico sclose"><svg viewBox="0 0 24 24">` +
      `<path d="M6 9l6 6 6-6"></path></svg></button></span></td>`,
  },
  drafts: {
    prio: `<td class="dimp"><span class="cin"><button class="dpick"><span class="dbox"></span></button>` +
      `<span class="chip" data-fit="prio" data-alt="средний|низкий">высокий</span></span></td>`,
    id: `<td class="id"><span data-fit="id">DK-410</span></td>`,
    title: `<td class="dtt"><span class="cin"><span class="st">Линт не видит файл задачи, заведённой руками</span></span></td>`,
    date: `<td class="dwhen"><span class="stale dashed" data-fit="date">2026-08-17</span></td>`,
    act: `<td class="sm"><span class="cin"><button class="btn btn-sm btn-ico">` +
      `<svg viewBox="0 0 24 24"><path d="M6 9l6 6 6-6"></path></svg></button></span></td>`,
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
});
document.title = out.join(" ");
