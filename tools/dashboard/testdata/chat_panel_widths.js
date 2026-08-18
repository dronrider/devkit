// Замер панели разговора настоящим движком (DK-435). Ширина панели, граница
// 1100 точек и узкий экран это сложение правил из разных мест стилей с
// раскладкой .screen, и проверкой текста css такое не берётся: правила стоят на
// месте, а панель при этом режет доску пополам либо схлопывается в полосу.
// Скрипт снимает с панели атрибут hidden, кладёт в неё разметку разговора,
// ставит ширину той же переменной --cw, какой её ставит хват, и складывает
// замеры в заголовок окна: go-тест поднимает их из выдачи chrome --dump-dom.
//
// Случай приходит параметром: `?bar=w320` и `?bar=w640` это две ширины хвата,
// `?bar=none` панель с умолчанием стилей, `?bar=hidden` доска без панели вовсе
// (по ней видно, режет доску панель или лежит поверх).
const want = new URLSearchParams(location.search).get("bar") || "none";

const PANEL = `
  <div class="chead">
    <div class="ct"><b>dashboard: панель разговора справа</b><span class="cts">19 авг, 10:02, devkit-dk-435</span></div>
    <button class="nx"></button>
    <div class="crow2"><span class="chip">ведёт DK-435, по дереву задачи</span><button class="btn btn-sm">Экран задачи</button></div>
  </div>
  <div class="chatwrap">
    <div class="msgs chatfeed"><div class="mlist"><div class="msg"><div class="bb">лента разговора</div><div class="mm">агент, 10:02, из транскрипта</div></div></div></div>
    <div class="msgs"></div>
    <div class="cnote"><span>Реплика ляжет во вход разговора.</span><button class="nx"></button></div>
    <div class="cbox"><textarea placeholder="Написать агенту..."></textarea><div class="crow"><button class="btn btn-acc">Отправить</button></div></div>
  </div>`;

// Доска под панелью: без содержимого .bmain схлопывается по своим отступам, и
// сравнивать её ширину было бы не с чем.
document.getElementById("groups").innerHTML =
  '<div class="card bcard"><div class="bhd"><b>Backlog</b></div><div class="trow"><span class="tt">строка доски</span></div></div>';

const panel = document.getElementById("cpanel");
if (want !== "hidden") {
  panel.hidden = false;
  document.getElementById("cpin").innerHTML = PANEL;
}
if (want === "w320") document.documentElement.style.setProperty("--cw", "320px");
if (want === "w640") document.documentElement.style.setProperty("--cw", "640px");

function box(sel) {
  const node = document.querySelector(sel);
  if (!node) throw new Error("на экране нет " + sel);
  return node.getBoundingClientRect();
}

const screen = Math.round(document.documentElement.clientWidth);
const main = box(".bmain");
const out = [
  "screen=" + screen,
  "main-w=" + Math.round(main.width),
  "main-right=" + Math.round(main.right),
];
if (want !== "hidden") {
  const rect = box(".cpanel");
  const grab = box(".cgrab");
  out.push(
    "panel-w=" + Math.round(rect.width),
    "panel-left=" + Math.round(rect.left),
    "panel-right=" + Math.round(rect.right),
    // Доска сжата панелью: её правый край не заходит под левый край панели.
    // Панель поверх доски даёт обратное, и это ровно та разница, ради которой
    // заведён рубеж 1100 точек.
    "board-cut=" + (main.right <= rect.left + 1 ? "1" : "0"),
    "fixed=" + (getComputedStyle(panel).position === "fixed" ? "1" : "0"),
    "grab-w=" + Math.round(grab.width),
    // Поле ввода должно оставаться пригодным к набору на любой ширине: панель,
    // ужатая до полосы, читается как поломка.
    "input-w=" + Math.round(box(".cbox textarea").width),
    "feed-w=" + Math.round(box(".chatfeed").width));
}
document.title = out.join(" ");
