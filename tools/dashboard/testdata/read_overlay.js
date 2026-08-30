// Замер режима чтения настоящим движком (POC DK-397, ветка poc-chat).
//
// Живой случай: «при включении режима чтения на форме задачи в мобильном виде
// кнопка "Выполнить" остаётся поверх текста задачи» (замечание пользователя).
// Режим чтения поднимает описание над всей колонкой (.fpanel.wide лежит
// absolute), а командная панель строки статуса выписана из потока своим слоем
// (.tacts, z-index 31) и рисуется поверх этого описания.
//
// Предмет замера это пересечение прямоугольников: кнопка над текстом и текст
// под ней. Разбором правил такое не берётся, слои складываются на живой
// раскладке, и на узком экране кнопка ложится ровно на первые строки описания.
//
// Разметка повторяет ту, что собирает taskFormPage: строка названия, строка
// статуса с чипами и командной панелью, под ними .tgrid с карточкой описания.

const PAGE = `
  <div class="tpage">
    <div class="thead">
      <span class="idbig">DK-397</span>
      <div class="tro">Цель: дашборд агентской разработки доведён до дела</div>
    </div>
    <div class="tchips">
      <span class="chip">M</span>
      <span class="chip c-check">без выката</span>
      <span class="gap"></span>
      <div class="tmodes">
        <div class="tacts">
          <button class="btn btn-sm btn-acc">Выполнить</button>
          <button class="btn btn-sm">Чат</button>
        </div>
        <button class="tpen">/</button>
        <button class="tpen on">R</button>
      </div>
    </div>
    <div class="tgrid">
      <div class="card fpanel">
        <div class="fhead"><span class="gap"></span><button class="fwide">x</button></div>
        <div class="fbody">
          <div class="fview">
            <div class="mdh mdh1">Постановка</div>
            <p>Первая строка описания, которую человек и приходит читать в режиме
            чтения: она стоит выше всех остальных и попадает под кнопку.</p>
            <p>Вторая строка описания.</p>
            <p>Третья строка описания.</p>
          </div>
        </div>
      </div>
    </div>
  </div>`;

document.getElementById("groups").innerHTML = PAGE;
document.getElementById("pname").textContent = "devkit";

function box(sel) {
  const node = document.querySelector(sel);
  if (!node) return null;
  return node.getBoundingClientRect();
}

// Площадь пересечения двух прямоугольников. Ноль значит, что кнопка текста не
// закрывает: отступ на глаз тут не судья, слои складываются как складываются.
function overlap(a, b) {
  if (!a || !b) return 0;
  const w = Math.min(a.right, b.right) - Math.max(a.left, b.left);
  const h = Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top);
  return w > 0 && h > 0 ? Math.round(w * h) : 0;
}

// Кто нарисован в точке кнопки: пересечение прямоугольников само по себе ещё не
// беда, бедой оно становится, когда сверху и правда чужой узел.
function ownerAt(rect) {
  if (!rect || rect.width === 0) return "нет";
  const node = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
  if (!node) return "нет";
  return node.closest(".fpanel") ? "панель" : "кнопка";
}

// Замер идёт в обоих видах страницы: обычном и в режиме чтения. Режим тут
// включается теми же классами, какие ставит сам экран (setRead и setWide),
// потому что нажимать в замере нечем.
function measure(mark) {
  const run = box(".tacts .btn-acc");
  const view = box(".fview");
  const panel = box(".fpanel");
  return [
    mark + "run-w=" + Math.round(run ? run.width : 0),
    mark + "view-w=" + Math.round(view ? view.width : 0),
    mark + "panel-h=" + Math.round(panel ? panel.height : 0),
    // Главное число стенда: сколько точек кнопки лежит поверх текста.
    mark + "run-over-view=" + overlap(run, view),
    mark + "acts-over-view=" + overlap(box(".tacts"), view),
    mark + "chips-over-view=" + overlap(box(".tchips"), view),
    mark + "head-over-view=" + overlap(box(".thead"), view),
    mark + "btn-on-top=" + (run && ownerAt(run) === "кнопка" ? "1" : "0"),
  ];
}

const out = ["screen=" + Math.round(document.documentElement.clientWidth)];
out.push(...measure("n-"));
document.querySelector(".tpage").classList.add("reading");
document.querySelector(".fpanel").classList.add("wide");
out.push(...measure("r-"));
document.title = out.join(" ");
