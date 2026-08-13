// Замер раскладки экрана задачи настоящим движком (DK-284). Разметку экрана
// собирает renderTask, здесь она повторена той же вёрсткой: браузеру нужен
// живой DOM, а поднимать ради замера весь дашборд с ручками незачем. Скрипт
// кладёт разметку в страницу дашборда, меряет ширины и складывает ответ в
// заголовок окна: go-тест поднимает его из выдачи chrome --dump-dom.
//
// Полосу действий renderTask ставит в разметку по ширине окна: на ноутбуке
// после чипов, на телефоне последней. Стенд получает нужный случай параметром
// (`?bar=under` и `?bar=over`) и смотрит не только ширины, но и то, сходится
// ли обход табом с картинкой: таб идёт по разметке, и полоса, уехавшая вниз
// одними стилями, осталась бы под ним первой.
const under = new URLSearchParams(location.search).get("bar") === "under";

const BAR = `
<div class="card abar"><button class="btn btn-acc">Сохранить</button><span class="div"></span><button class="btn">Живой статус</button><div class="error"></div></div>`;

const HEAD = `
  <div class="crumb"><span class="crumb-back">Доска devkit</span><span class="idsm">DK-284</span><span class="chip">In progress</span><span class="stale dashed">12 авг</span></div>
  <div class="thead"><span class="idbig">DK-284</span><textarea class="tedit">dashboard: экран задачи на телефоне</textarea></div>
  <div class="tchips"><span class="pick">тип</span><span class="pick">цена</span><span class="chip dashed">P2</span></div>`;

const FILE = `
    <div class="card fpanel">
      <div class="fhead"><b>docs/tasks/DK-284.md</b></div>
      <div class="fbody"><textarea>текст постановки</textarea></div>
    </div>`;

const RANK = `
      <div class="card rcard rfolded">
        <div class="rtop"><div class="rhead"><b>Ранг</b></div><div class="rbig"><span class="v">34</span><span class="f">= 25+7+2+0+0</span></div><button class="rfold">развернуть</button></div>
        <div class="rbody"><div class="rrow"><span class="nm">Серьёзность</span><span class="why">почему</span><span class="pick">25</span></div></div>
      </div>`;

const DEPS = `
      <div class="card dcard">
        <div class="dhead">Заблокировано задачами</div>
        <div class="dnone">Никого не ждёт.</div>
      </div>`;

// Раскладка телефона: колонок нет, блоки идут потоком, и правой колонки в
// разметке не появляется вовсе. Раскладка ноутбука: описание слева, ранг и
// зависимости в правой колонке.
const GRID = under
  ? `<div class="tgrid">` + RANK + FILE + DEPS + `</div>`
  : `<div class="tgrid">` + FILE + `<div class="rrail">` + RANK + DEPS + `</div></div>`;

document.getElementById("groups").innerHTML =
  '<div class="tpage">' + HEAD + (under ? GRID + BAR : BAR + GRID) + "</div>";

function box(sel) {
  const node = document.querySelector(sel);
  if (!node) throw new Error("на экране нет " + sel);
  return node.getBoundingClientRect();
}

// Обход табом идёт по разметке, и сойтись с картинкой он должен на обеих
// ширинах: следующая под табом кнопка не может стоять выше предыдущей.
// Ряд считается одним уровнем, поэтому мелкий разбег по вертикали не в счёт.
function tabMatchesEyes() {
  const nodes = document.querySelectorAll(
    ".tpage button, .tpage textarea, .tpage input, .tpage select, .tpage [tabindex]");
  let seen = -1e6;
  for (const node of nodes) {
    const rect = node.getBoundingClientRect();
    if (rect.width === 0 && rect.height === 0) continue;
    if (rect.top < seen - 8) return false;
    seen = Math.max(seen, rect.top);
  }
  return true;
}

const out = [
  "screen=" + Math.round(document.documentElement.clientWidth),
  "fpanel=" + Math.round(box(".fpanel").width),
  "dcard=" + Math.round(box(".dcard").width),
  "rcard=" + Math.round(box(".rcard").width),
  "actmsg=" + Math.round(box("#actmsg").height),
  "bar-under=" + (box(".abar").top > box(".fpanel").top ? "1" : "0"),
  "tab-order=" + (tabMatchesEyes() ? "1" : "0"),
];
document.title = out.join(" ");
