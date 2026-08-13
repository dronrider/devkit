// Замер раскладки экрана задачи настоящим движком (DK-284). Разметку экрана
// собирает renderTask, здесь она повторена той же вёрсткой: браузеру нужен
// живой DOM, а поднимать ради замера весь дашборд с ручками незачем. Скрипт
// кладёт разметку в страницу дашборда, меряет ширины и складывает ответ в
// заголовок окна: go-тест поднимает его из выдачи chrome --dump-dom.
//
// Порядок блоков тот же, что в renderTask: полоса действий лежит в разметке
// над сеткой, а под содержимое её уводит order на узком экране, и замер это
// видит по координатам, а не по строке в стилях.
const MARKUP = `
<div class="tpage">
  <div class="crumb"><span class="crumb-back">Доска devkit</span><span class="idsm">DK-284</span><span class="chip">In progress</span><span class="stale dashed">12 авг</span></div>
  <div class="thead"><span class="idbig">DK-284</span><textarea class="tedit">dashboard: экран задачи на телефоне</textarea></div>
  <div class="tchips"><span class="pick">тип</span><span class="pick">цена</span><span class="chip dashed">P2</span></div>
  <div class="card abar"><button class="btn btn-acc">Сохранить</button><span class="div"></span><button class="btn">Живой статус</button><div class="error"></div></div>
  <div class="tgrid">
    <div class="card fpanel">
      <div class="fhead"><b>docs/tasks/DK-284.md</b></div>
      <div class="fbody"><textarea>текст постановки</textarea></div>
    </div>
    <div class="rrail">
      <div class="card rcard rfolded">
        <div class="rtop"><div class="rhead"><b>Ранг</b></div><div class="rbig"><span class="v">34</span><span class="f">= 25+7+2+0+0</span></div><button class="rfold">развернуть</button></div>
        <div class="rbody"><div class="rrow"><span class="nm">Серьёзность</span><span class="why">почему</span><span class="pick">25</span></div></div>
      </div>
      <div class="card dcard">
        <div class="dhead">Заблокировано задачами</div>
        <div class="dnone">Никого не ждёт.</div>
      </div>
    </div>
  </div>
</div>`;

document.getElementById("groups").innerHTML = MARKUP;

function box(sel) {
  const node = document.querySelector(sel);
  if (!node) throw new Error("на экране нет " + sel);
  return node.getBoundingClientRect();
}

const out = [
  "screen=" + Math.round(document.documentElement.clientWidth),
  "fpanel=" + Math.round(box(".fpanel").width),
  "dcard=" + Math.round(box(".dcard").width),
  "rcard=" + Math.round(box(".rcard").width),
  "actmsg=" + Math.round(box("#actmsg").height),
  "bar-under=" + (box(".abar").top > box(".fpanel").top ? "1" : "0"),
];
document.title = out.join(" ");
