// Замер горизонтального разъезда разделов доски настоящим движком (замечание
// пользователя: «в мобильном виде в списке задач появился паразитный
// горизонтальный скролл, весь раздел ездит вбок»). Правило вёрстки прежнее:
// тело страницы вбок не ездит никогда, а широкое содержимое ужимается внутри
// своего контейнера.
//
// Разметку трёх табов доски скрипт кладёт руками той же вёрсткой, какой её
// собирает app.js: браузеру нужен живой DOM, а поднимать ради замера весь
// дашборд с ручками незачем. Ответ уходит в заголовок окна, откуда его берёт
// go-тест.

// Заголовок взят с живой доски: самая длинная строка проекта devkit (129
// знаков). Короткая строка влезает в любую ширину, и замер на ней ничего не
// говорил бы о телефоне.
const LONG = "Ревьювер не дешевеет ярусом, когда в правке участвует слой без автотестов: признак и его источник живут в одном месте";

// Неразрывный кусок: путь и команда без пробелов приезжают и в заголовок
// задачи, и в подпись сессии, и в текст черновика. Именно на таком куске
// страница и уезжает вбок, если его нечем разорвать.
const SOLID = "tools/dashboard/testdata/poc_clientask.mjs:GOWORK=off/go/test/./tools/dashboard/-run/TestChatAskAnswerSendsKeys";

// Причина блока это фраза человека, а не слово-метка: на живой доске она
// приезжает целым предложением. Нерезаный чип с такой фразой и уносил строку
// за край экрана, а раздел ездил вбок горизонтальной прокруткой (замечание
// пользователя про мобильный вид).
const WHY = "вопрос: DK-466: ветка dk-466 отрезана от main, а весь чат-контур задачи живёт только в ветке poc-chat";

// Шапка колонок раздела (POC DK-397): на телефоне таблица переводится в
// блочный вид, шапка ложится рядом чипов сортировки, а строка раскладывается по
// областям. Разметка тут та же, что собирает app.js.
// Колонки приезжают из самого app.js отдельным скриптом (window.TBLFIT):
// переписанные руками ширины расходились с кодом молча, и стенд сторожил
// раскладку, которой на экране уже нет. У колонки действий в этом списке стояли
// 246 точек ещё долго после того, как их стало 136 (разбор POC DK-397).
const FIT = window.TBLFIT || {};

const cols = (list) => `<colgroup>` + list.map((c) =>
  c.flex ? `<col>` : `<col style="width:${c.w}px">`).join("") + `</colgroup>`;

const head = (kind, list) => `<thead><tr class="tblh h-${kind}">` + list.map((c, at) =>
  `<th class="tblc" scope="col">` +
  (c.label ? `<button class="tblb" type="button"><span class="tbll">${c.label}</span></button>`
    : `<span class="tbln"></span>`) +
  (at + 1 < list.length ? `<span class="tblg"></span>` : "") + `</th>`).join("") + `</tr></thead>`;

const TASK_COLS = FIT.tasks || [];
const SESS_COLS = FIT.sess || [];
const DRAFT_COLS = FIT.drafts || [];
if (!TASK_COLS.length || !SESS_COLS.length || !DRAFT_COLS.length) {
  document.title = "screen=0 колонки не приехали из app.js";
  throw new Error("нет window.TBLFIT: стенд мерил бы раскладку из головы");
}

const band = (inside) => `<tr class="band secband"><td class="bcell" colspan="5">${inside}</td></tr>`;

const TASK_ROWS = `<table class="tbl t-tasks">${cols(TASK_COLS)}${head("tasks", TASK_COLS)}
  <tbody class="tsec">
    ${band('<div class="shead">Blocked<span class="n">1</span></div>')}
    <tr class="trow">
      <td class="id"><span class="sdot sd-wait"></span><span>DK-466</span></td>
      <td class="tt"><span class="cin"><span class="ttl">Дашборд: истёкший логин чата виден состоянием и чинится перезапуском</span><span class="rchips"><span class="chip c-p1">P1</span><span class="chip">M</span><span class="chip c-block cwhy">блок: ${WHY}</span></span></span></td>
      <td class="rank"><button class="rsum" type="button" aria-expanded="false">62</button><span class="rfold">50+5+3+0+4</span></td>
      <td class="twhen"><span class="stale dashed">2026-08-22</span></td>
      <td class="meta"><span class="cin"><span class="racts"><button class="btn btn-sm btn-ico rmain"><svg data-ico="i-play" viewBox="0 0 24 24"></svg></button><button class="btn btn-sm btn-ico"><svg data-ico="i-chat" viewBox="0 0 24 24"></svg></button></span></span></td>
    </tr>
    ${band('<div class="btier quiet">ждут задач<span class="n">17</span></div>')}
    ${band('<div class="shead">Backlog<span class="n">1, по рангу</span></div>')}
    <tr class="trow">
      <td class="id"><span>DK-517</span></td>
      <td class="tt"><span class="cin"><span class="ttl">${LONG} ${SOLID}</span>
        <span class="rchips"><span class="chip">M</span>
        <span class="chip c-check">без выката, сценарий пользовательский</span></span></span></td>
      <td class="rank on"><button class="rsum">62</button><span class="rfold">25+6+1+0+2</span></td>
      <td class="twhen"><span class="stale dashed">2026-08-20</span></td>
      <td class="meta"><span class="cin"><span class="racts"><button class="btn btn-sm btn-ico rmain"><svg data-ico="i-play" viewBox="0 0 24 24"></svg></button><button class="btn btn-sm btn-ico"><svg data-ico="i-chat" viewBox="0 0 24 24"></svg></button></span></span></td>
    </tr>
  </tbody></table>`;

const SESS_ROWS = `<table class="tbl t-sess">${cols(SESS_COLS)}${head("sess", SESS_COLS)}
  <tbody>
    <tr class="arow atalk">
      <td class="live"><span class="dot pulse"></span></td>
      <td class="ab">
        <div class="l1"><span class="tt">${LONG}</span>
          <span class="chip c-run">активна</span>
          <span class="chip">claude-opus-4-6-20260514</span>
          <span class="chip">мимо дашборда</span></div>
        <div class="l2"><a href="#demo/DK-479">DK-479</a>, Bash: ${SOLID}</div>
      </td>
      <td class="atime">3 ч 40 мин</td>
      <td class="amoved"><span class="stale dashed">2026-08-22</span></td>
      <td class="aacts"><span class="cin">
        <button class="btn btn-sm btn-ico">i</button>
        <button class="btn btn-sm btn-danger btn-ico sclose">x</button></span></td>
    </tr>
  </tbody></table>`;

const DRAFT_ROWS = `<table class="tbl t-drafts">${cols(DRAFT_COLS)}${head("drafts", DRAFT_COLS)}
  <tbody>
    <tr class="dsrow clicky">
      <td class="dimp"><span class="cin"><button class="dpick"><span class="dbox"></span></button>
        <span class="chip">средний</span></span></td>
      <td class="id">DK-410</td>
      <td class="dtt"><span class="cin"><span class="st">${LONG} ${SOLID}</span>
        <span class="rchips"><span class="chip">отложен 2026-09-01</span>
        <span class="chip c-wait">ждёт ответа</span></span></span></td>
      <td class="dwhen"><span class="stale dashed">2026-08-17</span></td>
      <td class="sm"><span class="cin"><button class="btn btn-sm btn-ico">i</button></span></td>
    </tr>
  </tbody></table>`;

const GROOM_BAR = `
  <div class="nbar">
    <span class="grun"><button class="btn btn-sm btn-acc">Грумить</button>
      <select class="cdsel"><option>claude-opus-4-6-20260514</option></select></span>
    <span class="drun"></span>
    <span class="hint">Выбрано 2 записи, каждая пойдёт своим разговором.</span>
  </div>`;

const ASK = `
  <div class="cask">
    <div class="caskh"><b>Клиент ждёт ответа</b></div>
    <div class="ktabs caskst">
      <button class="ktab onktab">Площадка<span class="n">ответ есть</span></button>
      <button class="ktab">Тип неисправности<span class="n">ответ есть</span></button>
      <button class="ktab">Сроки</button>
      <button class="ktab">Submit</button>
    </div>
    <div class="casks">Где именно MAX ломается под прокси? ${SOLID}</div>
  </div>`;

const TABS = `
  <div class="ktabs">
    <button class="ktab onktab">Задачи<span class="n">128</span></button>
    <button class="ktab">Сессии<span class="n">12</span></button>
    <button class="ktab">Черновики<span class="n">81</span></button>
  </div>`;

const parts = new URLSearchParams(location.search).get("bar") || "tasks";
const body = { tasks: TABS + TASK_ROWS, sess: TABS + SESS_ROWS,
  drafts: TABS + GROOM_BAR + DRAFT_ROWS, ask: TABS + ASK }[parts] || TABS + TASK_ROWS;
document.getElementById("groups").innerHTML = body;
// Шапка страницы заполняется вместе с разделом: тело вбок не ездит никогда, а
// уносить его умеет и она. Имя проекта берётся длинное нарочно: в выпадашке
// стоят имена всех проектов машины, и жмётся она по самому длинному из них.
document.getElementById("pname").textContent = "it-road-course";
const sel = document.getElementById("pselect");
if (sel) {
  sel.innerHTML = ["devkit", "goonies", "it-road-course", "xr-proxy-и-длинное-имя-проекта"]
    .map((name) => "<option>" + name + "</option>").join("");
}

const screen = document.documentElement.clientWidth;

// Кто именно вылезает: узел, чей правый край ушёл за окно, и который при этом
// сам не шире своего родителя по вине содержимого. Ответ отдаётся именем
// класса: по нему видно, что ужимать.
function worst() {
  let name = "";
  let over = 0;
  const box0 = document.getElementById("groups").getBoundingClientRect();
  const edge = box0.left + document.getElementById("groups").clientWidth;
  for (const node of document.querySelectorAll("#groups *")) {
    const box = node.getBoundingClientRect();
    // Виноватых два рода: узел, чей край ушёл за правую кромку раздела, и
    // узел, чьё содержимое шире его самого (неразрывный путь в подписи). У
    // второго край на месте, а вбок едет его текст.
    const past = Math.round(box.right - edge);
    if (past > over) {
      over = past;
      name = (node.className || node.tagName).toString().split(" ").join(".");
    }
  }
  return { name, over };
}

// Симптом это прокрутка самого раздела: ширина его содержимого больше его
// ширины. Обрезанный кромкой текст (заголовок строки, длинный чип) за край
// выходит нарочно и прокрутки не даёт, поэтому мерой служит scrollWidth, а имя
// виноватого берётся у коробок для разбора.
const bad = worst();
const groups = document.getElementById("groups");

// Кружок состояния на узком экране: он стоит слева от того, к чему относится, и
// прижиматься к тексту вплотную не должен. Меряется зазор от правой кромки
// кружка до левой кромки соседа; кружка на табе нет, значит и вопроса нет.
const NOROW = -1000;
const NOWIDTH = -2000;

function dotGap() {
  const row = document.querySelector(".arow") || document.querySelector(".trow");
  if (!row) return NOROW;
  const dot = row.querySelector(".dot") || row.querySelector(".sdot");
  if (!dot) return NOROW;
  const box = dot.getBoundingClientRect();
  if (!box.width) return NOWIDTH;
  // Сосед это тот, кто и правда стоит на одной полосе с кружком. Строка на
  // телефоне разложена по полосам, и заголовок работы уехал под кружок своей
  // полосой, а рядом с ним стоит дата активности: слипнуться кружок может
  // только с тем, что стоит с ним в ряд.
  const mate = [...row.querySelectorAll(".ab, .amoved, .id span:not(.sdot)")].find((node) => {
    const near = node.getBoundingClientRect();
    return near.width && near.top < box.bottom && near.bottom > box.top;
  });
  if (!mate) return NOROW;
  return Math.round(mate.getBoundingClientRect().left - box.right);
}
const out = [
  "screen=" + screen,
  "doc=" + Math.round(document.documentElement.scrollWidth),
  "body=" + Math.round(document.body.scrollWidth),
  // Раздел это свой скроллер: .groups просит overflow-y, а браузер делает
  // прокручиваемой и вторую ось, и вбок ездит именно он, а не тело страницы.
  "groups=" + Math.round(groups.scrollWidth),
  "gclient=" + Math.round(groups.clientWidth),
  // Мера разъезда: на сколько содержимое раздела шире самого раздела.
  "over=" + Math.max(0, Math.round(groups.scrollWidth - groups.clientWidth)),
  "widest=" + bad.over,
  "who=" + (bad.name || "none"),
  "dotgap=" + dotGap(),
].join(" ");
document.title = out;
