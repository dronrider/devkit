// Замер составной кнопки запуска настоящим движком (DK-336). Кнопку собирает
// runControl, здесь её разметка повторена той же вёрсткой: браузеру нужен живой
// DOM, а поднимать ради замера весь дашборд с ручками незачем. Скрипт кладёт
// разметку в страницу дашборда, меряет края и складывает ответ в заголовок
// окна: go-тест поднимает его из выдачи chrome --dump-dom.
//
// Предмет замера это то, что видно глазами и не берётся разбором правил: стык
// половин без зазора (приёмка нашла кнопку, распавшуюся на две), ширина узкой
// части под палец и список, который не уезжает за край телефона и не лезет под
// нижние вкладки. Правило стилей тут пишется одно, а картинку даёт сложение
// флекса, радиусов и позиционирования.
const params = new URLSearchParams(location.search);
// Сторона раскрытия приходит параметром: вниз список открывается там, где под
// кнопкой есть место, вверх на телефоне, где под ней нижние вкладки.
const up = params.get("bar") === "up";

// Строка подписки это одна полоса: имя и два процента остатка. Полоски-
// градусники, даты сброса, чип умолчания и возраст снимка со строки сняты, они
// стоят подсказкой (замечание пользователя про жирный блок выбора).
const POP = `
      <div class="hpop${up ? " up" : ""}">
        <span class="hph">На какой подписке запустить</span>
        <button class="hrow on" type="button" title="claude-code, подписка по умолчанию">
          <b class="hname">claude-code</b>
          <span class="hq"><em>week_all</em><b>52%</b></span>
          <span class="hq"><em>week_max</em><b>50%</b></span>
        </button>
        <button class="hrow" type="button">
          <b class="hname">glm-code</b>
          <span class="hq hnote">снимка квоты нет</span>
        </button>
        <span class="hph">Уровень модели</span>
        <div class="tbar"><button class="tpick on" type="button">вердикт</button>
          <button class="tpick" type="button">base</button>
          <button class="tpick" type="button">pro</button></div>
      </div>`;

const PANEL = `
  <div class="card tpage">
    <div class="tchips">
      <span class="chip">задача</span><span class="chip">S</span>
      <span class="gap"></span>
      <div class="tmodes">
        <div class="tacts">
          <span class="split">
            <button class="btn btn-acc"><svg viewBox="0 0 24 24"></svg><span class="lb">Выполнить</span></button>
            <button class="btn btn-acc more2"><span class="car"></span></button>` + POP + `
          </span>
        </div>
        <button class="btn btn-ico tpen"><svg viewBox="0 0 24 24"></svg></button>
      </div>
    </div>
  </div>`;

// Составная кнопка стоит командной панелью экрана задачи и полосой запуска
// накопителя: из строки доски она ушла, там осталась одна главная кнопка, а
// выбор подписки уехал в меню под тремя точками. Меряется она там, где стоит:
// замер вне своего места говорил бы о вёрстке, которой на экране не бывает.
document.getElementById("groups").innerHTML = PANEL;

function box(sel) {
  const node = document.querySelector(sel);
  if (!node) throw new Error("на экране нет " + sel);
  return node.getBoundingClientRect();
}

const wide = box(".split .btn:not(.more2)");
const arrow = box(".split .more2");
const pop = box(".hpop");
const car = box(".car");
const hrow = box(".hrow");
const screen = document.documentElement.clientWidth;
// Нижние вкладки живут в разметке дашборда и на телефоне стоят внизу экрана:
// раскрытый вниз список уезжает под них, и это ровно тот случай, ради которого
// заведено раскрытие вверх.
const tabs = document.querySelector(".ptabs").getBoundingClientRect();

// Карандаш стоит в той же панели соседней кнопкой: по нему меряется узкая
// половина составной кнопки, своего числа у неё нет.
const pen = box(".tpen");

const out = [
  "screen=" + Math.round(screen),
  // Стык: левый край узкой части стоит там же, где правый край широкой. Зазор
  // в пиксель это два кубика вместо одной кнопки.
  "seam=" + Math.round(arrow.left - wide.right),
  "arrow-w=" + Math.round(arrow.width),
  "pen-w=" + Math.round(pen.width),
  "arrow-h=" + Math.round(arrow.height),
  "wide-h=" + Math.round(wide.height),
  "car=" + Math.round(car.width),
  "pop-w=" + Math.round(pop.width),
  // Список висит под правым краем кнопки: уехавший за экран край это половина
  // списка, до которой не дотянуться.
  "pop-right=" + Math.round(arrow.right - pop.right),
  "pop-over=" + (pop.right > screen + 1 || pop.left < -1 ? "1" : "0"),
  // Раскрытый вверх список кончается над кнопкой и до вкладок не достаёт.
  "pop-above=" + (pop.bottom <= wide.top + 1 ? "1" : "0"),
  "pop-under-tabs=" + (pop.bottom > tabs.top + 1 ? "1" : "0"),
  "hrow-h=" + Math.round(hrow.height),
];
document.title = out.join(" ");
