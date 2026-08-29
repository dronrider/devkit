// Замер командной панели строки статуса настоящим движком. Ростом строки
// заведуют разные правила: у карандаша с кнопкой чтения свой размер по
// var(--ctl), у кнопок действий рост от .btn, у половин составной кнопки к
// этому добавляются рамки и радиусы. Разбором правил такое не берётся, и
// приёмка пользователя дважды нашла строку ступенькой, а стык половин двойной
// линией.
//
// Страницу собирает chromeStand, ответ уезжает в заголовок окна, как у
// split_run.js: go-тест поднимает его из выдачи chrome --dump-dom.
const params = new URLSearchParams(location.search);
// Раскрытый список подписок меряется тем же заходом: раскрытая кнопка не имеет
// права менять рост строки.
const open = params.get("bar") === "up";

const PANEL = `
  <div class="tchips">
    <span class="gap"></span>
    <div class="tmodes">
      <div class="tacts">
        <span class="split">
          <button class="btn btn-acc" id="wide">Выполнить</button>
          <button class="btn btn-acc more2" id="arrow"><span class="car"></span></button>
          <div class="hpop"${open ? "" : " hidden"}>
            <span class="hph">На какой подписке запустить</span>
            <button class="hrow on" type="button"><b class="hname">claude-code</b>
              <span class="hq"><em>week_all</em><b>52%</b></span></button>
          </div>
        </span>
        <button class="btn" id="plain">Грумить</button>
      </div>
      <button class="tpen" id="pen"><span class="car"></span></button>
      <button class="tpen" id="read"><span class="car"></span></button>
    </div>
  </div>`;

const host = document.getElementById("groups") || document.body;
host.innerHTML = PANEL;

const box = (id) => document.getElementById(id).getBoundingClientRect();
const css = (id) => getComputedStyle(document.getElementById(id));
const px = (v) => Math.round(parseFloat(v) || 0);

document.title = [
  "wide-h=" + Math.round(box("wide").height),
  "arrow-h=" + Math.round(box("arrow").height),
  "plain-h=" + Math.round(box("plain").height),
  "pen-h=" + Math.round(box("pen").height),
  "read-h=" + Math.round(box("read").height),
  // Стык половин: ноль это встык, минус один это наезд рамками в одну линию.
  "seam=" + Math.round(box("arrow").left - box("wide").right),
  // Углы скругляются только по внешним краям группы.
  "wide-rl=" + px(css("wide").borderTopLeftRadius),
  "wide-rr=" + px(css("wide").borderTopRightRadius),
  "arrow-rl=" + px(css("arrow").borderTopLeftRadius),
  "arrow-rr=" + px(css("arrow").borderTopRightRadius),
  "pen-r=" + px(css("pen").borderTopLeftRadius),
  "arrow-w=" + Math.round(box("arrow").width),
].join(" ");
