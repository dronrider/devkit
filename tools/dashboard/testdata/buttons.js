// Замер кнопок действий настоящим движком (DK-337). Приёмка нашла, что к
// макетам приведена одна составная кнопка, а остальные остались прежних
// размеров: мелкая кнопка строки была на два пикселя ниже макетной, с другими
// полями и радиусом, а погашенная кнопка ничем не отличалась от живой. Числа
// такого рода не берутся чтением правил: высоту даёт сложение правила кнопки с
// правилами её места, и один и тот же класс в строке доски и в полосе действий
// меряется по-разному.
//
// Разметку кнопок собирают rowAction, taskActions и карточки экрана черновика,
// здесь она повторена той же вёрсткой; go-тест рядом сверяет, что статика
// собирает кнопки теми же классами.
const params = new URLSearchParams(location.search);
// Экран замера приходит параметром: строка доски, полоса действий задачи или
// карточки черновика.
const what = params.get("bar") || "row";

const ROW = `
  <div class="card">
    <div class="trow">
      <span class="id">DK-319</span>
      <span class="tt"><span class="ttl">Строка доски</span></span>
      <span class="meta">
        <span class="rank"><button class="rsum">40</button></span>
        <button class="btn btn-sm btn-acc" id="m-run">Выполнить</button>
      </span>
    </div>
    <div class="trow">
      <span class="id">DK-327</span>
      <span class="tt"><span class="ttl">Строка в работе</span></span>
      <span class="meta">
        <span class="rank"><button class="rsum">66</button></span>
        <button class="btn btn-sm btn-danger" id="m-stop">Стоп</button>
      </span>
    </div>
    <div class="trow">
      <span class="id">DK-324</span>
      <span class="tt"><span class="ttl">Заблокированная маркером</span></span>
      <span class="meta">
        <span class="rank"><button class="rsum">34</button></span>
        <button class="btn btn-sm btn-acc" id="m-wait" disabled>Выполнить</button>
      </span>
    </div>
  </div>`;

const ABAR = `
  <div class="card abar">
    <button class="btn" id="m-live">Живой статус</button>
    <span class="split"><button class="btn btn-acc" id="m-bar">Выполнить</button><button class="btn btn-acc more2" id="m-more"><span class="car"></span></button></span>
    <button class="btn btn-danger" id="m-kill">Остановить агента</button>
    <span class="hint">заказ агенту</span>
  </div>`;

const SINGLE = `
  <div class="card abar">
    <button class="btn" id="m-live">Живой статус</button>
    <button class="btn btn-acc" id="m-bar">Выполнить</button>
    <button class="btn btn-danger" id="m-kill">Остановить агента</button>
    <span class="hint">заказ агенту</span>
  </div>`;

// Карточка удаления черновика: причина своей строкой во всю ширину, кнопки под
// ней справа и полного размера (макет «12 Груминг черновика»).
const DROP = `
  <div class="card dcard">
    <div class="phd"><b>Удаление записи</b></div>
    <div class="dbd">
      <div class="dconfirm" id="m-box">
        <input class="dwhyin" id="m-why" placeholder="Чем запись протухла" />
        <div class="drow" id="m-drow">
          <button class="btn" id="m-no">Отмена</button>
          <button class="btn btn-danger" id="m-drop">Удалить черновик</button>
        </div>
      </div>
    </div>
  </div>`;

const ASK = `
  <div class="card dcard">
    <div class="phd"><b>Вопрос груминга</b></div>
    <div class="dbd" id="m-dbd">
      <div class="dwhy">Что ответить грумингу</div>
      <textarea class="dask" id="m-field"></textarea>
      <button class="btn btn-acc dend" id="m-again">Повторить груминг</button>
    </div>
  </div>`;

// Шапка карточки хода груминга: имя сессии слева, стоп у правого края (макет
// «12 Груминг черновика», фрейм состояний).
const RUN = `
  <div class="card dcol-run">
    <div class="phd" id="m-phd">
      <b>Ход груминга</b>
      <span>task-DK-329</span>
      <button class="btn btn-sm btn-danger" id="m-gstop">Остановить груминг</button>
    </div>
  </div>`;

const MARKUP = { row: ROW, bar: ABAR, single: SINGLE, drop: DROP, ask: ASK, run: RUN };
document.getElementById("groups").innerHTML = MARKUP[what] || ROW;

function box(sel) {
  const node = document.querySelector(sel);
  if (!node) throw new Error("на экране нет " + sel);
  return node.getBoundingClientRect();
}

// Размеры кнопки одним набором: высота, радиус и левое поле. Радиус и поля
// живут в стилях, а высота складывается из них и места кнопки, поэтому берутся
// и вычисленные свойства, и настоящий прямоугольник.
function shape(id) {
  const node = document.getElementById(id);
  if (!node) return [];
  const cs = getComputedStyle(node);
  const rect = node.getBoundingClientRect();
  return [
    id + "-h=" + Math.round(rect.height),
    id + "-w=" + Math.round(rect.width),
    id + "-r=" + Math.round(parseFloat(cs.borderTopLeftRadius) || 0),
    id + "-rr=" + Math.round(parseFloat(cs.borderTopRightRadius) || 0),
    id + "-pad=" + Math.round(parseFloat(cs.paddingLeft) || 0),
    id + "-fs=" + Math.round(parseFloat(cs.fontSize) || 0),
    id + "-dim=" + Math.round((parseFloat(cs.opacity) || 1) * 100),
  ];
}

let out = ["screen=" + Math.round(document.documentElement.clientWidth)];
if (what === "row") {
  out = out.concat(shape("m-run"), shape("m-stop"), shape("m-wait"));
} else if (what === "bar") {
  out = out.concat(shape("m-live"), shape("m-bar"), shape("m-more"));
} else if (what === "single") {
  out = out.concat(shape("m-live"), shape("m-bar"));
} else if (what === "drop") {
  // Меряется всё внутри самой коробки подтверждения: у карточки свои поля, и
  // считать от её края значило бы мерить их, а не раскладку кнопок.
  const conf = box("#m-box");
  const why = box("#m-why");
  const row = box("#m-drow");
  out = out.concat(shape("m-no"), shape("m-drop"), [
    // Поле причины идёт во всю ширину, а не делит строку с кнопками.
    "why-full=" + (why.width > conf.width - 3 ? "1" : "0"),
    "why-above=" + (why.bottom <= row.top + 1 ? "1" : "0"),
    // Кнопки прижаты к правому краю коробки.
    "row-right=" + Math.round(conf.right - row.right),
  ]);
} else if (what === "run") {
  const name = document.querySelector("#m-phd span").getBoundingClientRect();
  const stop = box("#m-gstop");
  out = out.concat(shape("m-gstop"), [
    // Стоп отодвинут от имени сессии автоматическим полем, а не стоит впритык
    // за ним на общем зазоре шапки: считать поля самой шапки для этого не надо.
    "gstop-gap=" + Math.round(stop.left - name.right),
  ]);
} else {
  // Кнопка повторной ходки стоит под полем и по его правому краю: считать от
  // края карточки мешают её поля.
  const field = box("#m-field");
  const again = box("#m-again");
  out = out.concat(shape("m-again"), [
    "again-right=" + Math.round(field.right - again.right),
    "again-under=" + (again.top >= field.bottom - 1 ? "1" : "0"),
  ]);
}
document.title = out.join(" ");
