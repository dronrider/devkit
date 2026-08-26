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

// Шапка колонок раздела (POC DK-397): подписи стоят той же сеткой, что и
// строка, и на телефоне обязаны лечь рядом чипов с переносом, а не унести
// раздел вбок.
const head = (kind, cols) => `<div class="thead h-${kind}">` + cols.map((c) =>
  (c ? `<button class="thc thb" type="button"><span class="thl">${c}</span></button>`
    : `<span class="thc thn"></span>`)).join("") + `</div>`;

const TASK_HEAD = head("tasks", ["Номер", "Задача", "Ранг", "Дата", ""]);
const SESS_HEAD = head("sess", ["Состояние", "Работа", "Идёт", ""]);
const DRAFT_HEAD = head("drafts", ["", "Приоритет", "Номер", "Задача", "Дата", ""]);

const TASK_ROWS = `
  <div class="shead bsec onsec">Blocked<span class="n">1</span></div>
  <div class="card bsec onsec"><div class="trow"><span class="id"><span class="sdot sd-wait"></span><span>DK-466</span></span><span class="tt"><span class="ttl">Дашборд: истёкший логин чата виден состоянием и чинится перезапуском</span><span class="rchips"><span class="chip c-p1">P1</span><span class="chip">M</span><span class="chip c-block cwhy">блок: вопрос: DK-466: ветка dk-466 отрезана от main, а весь чат-контур задачи живёт только в ветке poc-chat. В main...</span></span></span><span class="rank"><button class="rsum" type="button" aria-expanded="false">62</button><span class="rfold">50+5+3+0+4</span></span><span class="twhen"><span class="stale dashed">2026-08-22</span></span><span class="meta"><button class="btn btn-sm btn-ico"><svg data-ico="i-chat" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 12.5c0 3.6-3.6 6.5-8 6.5-.9 0-1.8-.1-2.6-.4L5 20.5l1.2-3.2C4.8 16.1 4 14.4 4 12.5 4 8.9 7.6 6 12 6s8 2.9 8 6.5Z"></path></svg></button><span class="split"><button class="btn btn-sm btn-acc">Выполнить</button><button class="btn btn-sm btn-acc more2" aria-expanded="false"><span class="car"></span></button><div class="hpop" hidden=""><span class="hph">На какой подписке запустить</span><button class="hrow on" type="button"><span class="h1"><b>claude-code</b><span class="chip">по умолчанию</span></span><div class="qrow"><em>week_all</em><span class="meter"><i style="width: 14%;"></i></span><b>14%</b><span class="qres">до 31.08</span></div><div class="qrow"><em>week_max</em><span class="meter"><i style="width: 1%;"></i></span><b>1%</b><span class="qres">до 31.08</span></div><span class="hnote qage q-fresh">снимок 10м назад</span></button><button class="hrow" type="button"><span class="h1"><b>glm-code</b></span><div class="qrow"><em>5h_all</em><span class="meter"><i style="width: 0%;"></i></span><b>0%</b></div><div class="qrow"><em>week_all</em><span class="meter"><i style="width: 32%;"></i></span><b>32%</b><span class="qres">до 29.08</span></div><span class="hnote qage q-old">снимок 13ч 26м назад</span></button><span class="hph">Каким ярусом</span><div class="tbar"><button class="tpick on" type="button">вердикт</button><button class="tpick" type="button">mini</button><button class="tpick" type="button">base</button><button class="tpick" type="button">pro</button><button class="tpick" type="button">max</button></div><span class="hfoot">Список включённых подписок машины, agentctl harness. Выбор действует на один запуск. Ярус называет вердикт agentctl pick.</span></div></span></span></div><div class="btier quiet">ждут задач<span class="n">17</span></div></div>
  <div class="shead bsec onsec">Backlog<span class="n">1, по рангу</span></div>
  <div class="card bsec onsec">
    <div class="trow">
      <span class="id"><span>DK-517</span></span>
      <span class="tt"><span class="ttl">${LONG} ${SOLID}</span>
        <span class="rchips"><span class="chip">M</span>
        <span class="chip c-check">без выката, сценарий пользовательский</span></span></span>
      <span class="rank on"><button class="rsum">62</button><span class="rfold">25+6+1+0+2</span></span>
      <span class="twhen"><span class="stale dashed">2026-08-20</span></span>
      <span class="meta"><button class="btn btn-sm btn-acc">Выполнить</button></span>
    </div>
  </div>`;

const SESS_ROWS = `
  <div class="card">
    ${SESS_HEAD}
    <div class="arow atalk">
      <span class="dot pulse"></span>
      <div class="ab">
        <div class="l1"><span class="tt">${LONG}</span>
          <span class="chip c-run">активна</span>
          <span class="chip">claude-opus-4-6-20260514</span>
          <span class="chip">мимо дашборда</span></div>
        <div class="l2"><a href="#demo/DK-479">DK-479</a>, Bash: ${SOLID}</div>
      </div>
      <span class="atime">3 ч 40 мин</span>
      <div class="aacts">
        <button class="btn btn-sm btn-ico">i</button>
        <button class="btn btn-sm btn-danger btn-ico sclose">x</button></div>
    </div>
  </div>`;

const DRAFT_ROWS = `
  <div class="card">
    ${DRAFT_HEAD}
    <div class="srow clicky dsrow">
      <button class="dpick"><span class="dbox"></span></button>
      <span class="dimp"><span class="chip">средний</span></span>
      <span class="id">DK-410</span>
      <span class="dtt"><span class="st">${LONG} ${SOLID}</span>
        <span class="rchips"><span class="chip">отложен 2026-09-01</span>
        <span class="chip c-wait">ждёт ответа</span></span></span>
      <span class="dwhen"><span class="stale dashed">2026-08-17</span></span>
      <span class="sm"><button class="btn btn-sm btn-ico">i</button></span>
    </div>
  </div>`;

const GROOM_BAR = `
  <div class="nbar">
    <span class="grun"><button class="btn btn-sm btn-acc">Провести груминг</button>
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
const body = { tasks: TABS + TASK_HEAD + TASK_ROWS, sess: TABS + SESS_ROWS,
  drafts: TABS + GROOM_BAR + DRAFT_ROWS, ask: TABS + ASK }[parts] || TABS + TASK_HEAD + TASK_ROWS;
document.getElementById("groups").innerHTML = body;
document.getElementById("pname").textContent = "devkit";
document.getElementById("psub").textContent = "задачи проекта";
const sel = document.getElementById("pselect");
if (sel) sel.innerHTML = "<option>devkit</option>";

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
].join(" ");
document.title = out;
