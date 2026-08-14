// Замер раскладки доски настоящим движком (DK-285). Разметку списка собирает
// renderBoard, здесь она повторена той же вёрсткой: браузеру нужен живой DOM, а
// поднимать ради замера весь дашборд с ручками незачем. Скрипт кладёт разметку
// в страницу дашборда, меряет ширины и складывает ответ в заголовок окна:
// go-тест поднимает его из выдачи chrome --dump-dom.
//
// Предмет замера это ширина заголовка строки, место чипов под ним, полоса
// разделов в одну строку и плавающий плюс над нижними вкладками. Разбором
// правил стилей такое не берётся: заголовок схлопывался не написанным в одном
// месте, а сложением флекса с чипами, которые ширину не отдают.
const ROW_CHIPS = `
      <span class="rchips">
        <span class="chip">M</span>
        <span class="chip c-check">без выката, сценарий пользовательский</span>
      </span>`;

const ROW = `
    <div class="trow">
      <span class="id">DK-112</span>
      <span class="tt"><span class="ttl">Цель: доска devkit читается с телефона целиком</span>` + ROW_CHIPS + `</span>
      <span class="meta">
        <span class="rank"><button class="rsum">35</button></span>
        <span class="stale dashed">12 авг</span>
        <button class="btn btn-sm btn-acc">Выполнить</button>
      </span>
    </div>`;

const TABS = `
  <div class="btabs">
    <button class="btab onbtab">Сессии</button>
    <button class="btab">Бэклог</button>
    <button class="btab">Черновики</button>
  </div>`;

const BAR = `
  <div class="nbar bbar">
    <button class="btn btn-acc">Новая задача</button>
    <button class="btn">Черновики</button>
  </div>`;

const SECTIONS = `
  <div class="shead bsec onsec">In progress<span class="n">1</span></div>
  <div class="card bsec onsec">` + ROW + `</div>
  <div class="shead bsec">Backlog<span class="n">1, по рангу</span></div>
  <div class="card bsec">` + ROW + `</div>`;

const FAB = `<button class="fab" title="Новая задача в devkit">+</button>`;

document.getElementById("groups").innerHTML = TABS + BAR + SECTIONS + FAB;

function box(sel) {
  const node = document.querySelector(sel);
  if (!node) throw new Error("на экране нет " + sel);
  return node.getBoundingClientRect();
}

// Полоса разделов стоит в одну строку тогда, когда все табы держат один
// верхний край: перенос второго ряда виден именно так, а не по высоте полосы.
function tabsInRow() {
  const tabs = Array.from(document.querySelectorAll(".btab"));
  if (tabs.length < 2) return false;
  const tops = tabs.map((t) => t.getBoundingClientRect().top);
  return Math.max(...tops) - Math.min(...tops) < 2;
}

// Подпись таба не должна резаться: в одну строку полоса влезает и обрубками,
// а читать их не легче, чем прежние кнопки в два ряда.
function tabClipped() {
  return Array.from(document.querySelectorAll(".btab"))
    .some((t) => t.scrollWidth > t.clientWidth + 1);
}

// Шапка доски заполняется как на живом экране: имя проекта, подпись, поле
// поиска, лупа и колокольчик. Пустая шапка влезала бы в любую ширину, и замер
// на ней ничего не говорил бы о телефоне (DK-325).
document.getElementById("pname").textContent = "devkit";
document.getElementById("psub").textContent = "доска docs/TASKS.md, DK";
document.getElementById("pselect").innerHTML = "<option>devkit</option>";

const ttl = box(".trow .ttl");
const chips = box(".trow .rchips");
const fab = box(".fab");
const seen = document.querySelector(".bsec:not(.onsec)").getBoundingClientRect();
const head = box(".bhead");

const out = [
  "hfind=" + Math.round(box(".hfind").width),
  "hfbtn=" + Math.round(box(".hfbtn").width),
  // Шапка вылезла за экран, если её правый край ушёл за ширину окна: поле с
  // лупой съели бы имя проекта и колокольчик разом.
  "head-over=" + (head.right > document.documentElement.clientWidth + 1 ? "1" : "0"),
  "head-h=" + Math.round(head.height),
  "screen=" + Math.round(document.documentElement.clientWidth),
  "ttl=" + Math.round(ttl.width),
  "ttl-h=" + Math.round(ttl.height),
  "chips-under=" + (chips.top >= ttl.bottom - 2 ? "1" : "0"),
  "tabs=" + Math.round(box(".btabs").height),
  "tabs-row=" + (tabsInRow() ? "1" : "0"),
  "tab-clip=" + (tabClipped() ? "1" : "0"),
  "other-tab=" + Math.round(seen.height),
  "fab=" + Math.round(fab.width),
  "fab-hits-tabs=" + (fab.width > 0 && fab.bottom > document.documentElement.clientHeight - 78 ? "1" : "0"),
  "bar=" + Math.round(box(".bbar").height),
];
document.title = out.join(" ");
